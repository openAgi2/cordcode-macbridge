//go:build darwin

package gobridge

// topology_collector_darwin.go —— Darwin 只读证据采集流水线（implementation plan v2 §2.2 第 1–8 步）。
//
// 冻结实现说明（§2.2 步骤 5 的"实现说明"部分）：
//   - 枚举：ps -axo pid=,lstart=,command=，按 DesktopAppPath/Contents/MacOS/ChatGPT 主进程候选，
//     可选 --user-data-dir= 参数区分实例并定位日志目录；pid+startTime 作为一次性身份。
//   - 后代归属：ps -axo pid=,ppid=,command= 建树，BFS 递归（非 direct-child）归属 app-server 后代
//     （命令含 "/Contents/Resources/codex" 且 "app-server"）。
//   - shared 正证据：lsof 该实例主进程+后代集（-U）的 NAME 为 "->0x…" 的 peer token 与
//     daemon listener（lsof -t <socket> 的 NODE 列取 object）求交集；交集 ≥1 → confirmed。
//     daemon socket 不存在 = 合法负证据（absent）；lsof 失败/超时 = unavailable。
//   - private 正证据：(a) user-data-dir 下 24h 内修改的 *.log 含字面 "transport=stdio"
//     （只 grep 标记字面，不读正文会话内容）；(b) 存在 app-server 后代且
//     lsof -n -P -a -p <后代> -d 0,1,2 至少有 1 个 TYPE=PIPE 或 unix socketpair
//     （Desktop 151 已把 stdio IPC 从 PIPE 换成 socketpair）；
//     (c) force-stdio（CODEX_APP_SERVER_FORCE_CLI=1）仅用于 owner 隔离实验取证，
//     生产不下结论——隔离实例经 (b) 父链/pipe 判据仍可证，无需读 force env。
//   - 任一证据采样失败（权限/竞态/LookPath 缺失/超时）→ unavailable → 实例 unresolved，
//     绝不用"无证据"当健康；confirmed 与 confirmed 并存保留 dual，不被 unavailable 抹掉
//     （但 unavailable 存在时实例整体 unresolved，不产出误导性分类）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// probeState 是单条共享/私有证据的判定。
type probeState string

const (
	probeConfirmed   probeState = "confirmed"
	probeAbsent      probeState = "absent"
	probeUnavailable probeState = "unavailable"
)

type darwinCollector struct {
	cfg TopologyCollectorConfig
	// run 是探针执行器（生产 runTimed；测试注入假输出）。参数固定、无 shell。
	run func(ctx context.Context, bin string, args ...string) (string, string, error)
	// lookPath 解析系统命令（生产 exec.LookPath；测试注入缺失以覆盖 not_implemented）。
	lookPath func(name string) (string, error)
}

func newPlatformCollector() TopologyCollector { return newDarwinCollector(defaultTopologyConfig()) }

// newDarwinCollector 从 cfg 构造采集器（测试可直接注入 run/lookPath 假探针）。
func newDarwinCollector(cfg TopologyCollectorConfig) *darwinCollector {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &darwinCollector{cfg: cfg, lookPath: exec.LookPath, run: func(ctx context.Context, bin string, args ...string) (string, string, error) {
		return runTimed(ctx, bin, args, cfg.Timeout)
	}}
}

func defaultTopologyConfig() TopologyCollectorConfig {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".codex")
		}
	}
	return TopologyCollectorConfig{
		CodexHome:        home,
		DesktopAppPath:   "/Applications/ChatGPT.app",
		StandaloneBin:    filepath.Join(home, "packages", "standalone", "current", "codex"),
		DaemonSocket:     filepath.Join(home, "app-server-control", "app-server-control.sock"),
		LaunchAgentLabel: "org.openagi.cordcode.codex-app-server-daemon",
		Timeout:          5 * time.Second,
	}
}

// probe 执行一次带预算探针。bin 为空 → ErrorNotImplemented（命令缺失，P2-3）。
func (c *darwinCollector) probe(ctx context.Context, bin string, args ...string) (out, errCode string, ok bool) {
	if bin == "" {
		return "", ErrorNotImplemented, false
	}
	out, stderr, err := c.run(ctx, bin, args...)
	if err != nil {
		return "", classifyExecFailure(stderr, err), false
	}
	return out, "", true
}

func (c *darwinCollector) resolveBin(configured string, names ...string) string {
	if configured != "" {
		return configured
	}
	for _, n := range names {
		if p, err := c.lookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func (c *darwinCollector) Collect(ctx context.Context, identity core.CodexWebTransportIdentity) *CollectedTopology {
	res := &CollectedTopology{
		BridgeAttachment:      AttachmentUnresolved,
		DesktopAggregate:      AggregateUnknown,
		Instances:             []DesktopInstance{},
		SeatDaemon:            DimValue{Enum: "unresolved", Source: DimSourceVersionProbe, ErrorCode: ErrorUnknown},
		SeatLaunchAgent:       DimValue{Enum: "unresolved", Source: DimSourceLaunchdProbe, ErrorCode: ErrorUnknown},
		AttachConfig:          DimValue{Enum: "unresolved", Source: DimSourceLaunchdProbe, ErrorCode: ErrorUnknown},
		VersionCompatibility:  DimValue{Enum: "unknown", Source: DimSourceVersionProbe, ErrorCode: ErrorUnknown},
		LegacyManagedLoopback: DimValue{Enum: "unresolved", Source: DimSourceProcessTree, ErrorCode: ErrorUnknown},
		LegacyDesktopPrivate:  DimValue{Enum: "unresolved", Source: DimSourceProcessTree, ErrorCode: ErrorUnknown},
	}

	psBin := c.resolveBin(c.cfg.PsPath, "ps")
	lsofBin := c.resolveBin(c.cfg.LsofPath, "lsof")
	launchctlBin := c.resolveBin(c.cfg.LaunchctlPath, "launchctl")

	// 1) 进程表：枚举（pid,lstart,command）+ 树（pid,ppid,command）各一次调用。
	var psAll []parsePsProcess
	var psTree []parsePsTreeNode
	psOK := true
	if psBin == "" {
		psOK = false
		res.DesktopErrorCode = ErrorNotImplemented
		res.LegacyManagedLoopback.ErrorCode = ErrorNotImplemented
		res.LegacyDesktopPrivate.ErrorCode = ErrorNotImplemented
	} else {
		if out, code, ok := c.probe(ctx, psBin, "-axo", "pid=,lstart=,command="); !ok {
			psOK = false
			res.DesktopErrorCode = code
		} else if rows, err := ParsePsProcessRows(out); err != nil {
			psOK = false
			res.DesktopErrorCode = ErrorParseFailed
		} else {
			psAll = rows
		}
		if out, code, ok := c.probe(ctx, psBin, "-axo", "pid=,ppid=,command="); !ok {
			psOK = false
			res.DesktopErrorCode = code
		} else if rows, err := ParsePsTreeRows(out); err != nil {
			psOK = false
			res.DesktopErrorCode = ErrorParseFailed
		} else {
			psTree = rows
		}
		if !psOK {
			res.LegacyManagedLoopback.ErrorCode = res.DesktopErrorCode
			res.LegacyDesktopPrivate.ErrorCode = res.DesktopErrorCode
		}
	}

	// 2) daemon listener object 集合与 seat 判定（socket 缺失 = 合法负证据，非采样失败）。
	daemonObjs := map[string]bool{}
	daemonState := "absent" // absent|objects|probe_failed
	daemonErr := ""
	{
		sockErr := ""
		daemonPid := ""
		if bin := lsofBin; bin != "" {
			if out, code, ok := c.probe(ctx, bin, "-t", c.cfg.DaemonSocket); !ok {
				daemonPid, sockErr = "", code
			} else {
				daemonPid = strings.TrimSpace(out)
				sockErr = ""
			}
		} else {
			sockErr = ErrorNotImplemented
		}
		switch {
		case sockErr != "":
			daemonState = "probe_failed"
			daemonErr = sockErr
			res.SeatDaemon.ErrorCode = sockErr
		case daemonPid == "":
			if _, err := os.Lstat(c.cfg.DaemonSocket); err != nil {
				// 无 socket 文件 → daemon 未监听（负证据）。
				res.SeatDaemon.Enum = "stopped"
				res.SeatDaemon.ErrorCode = ErrorNone
			} else {
				// socket 存在但无 listener owner（socket 文件残留/竞态）。
				daemonState = "probe_failed"
				daemonErr = ErrorProcessMissing
				res.SeatDaemon.ErrorCode = ErrorProcessMissing
			}
		default:
			out, code, ok := c.probe(ctx, lsofBin, "-n", "-P", "-a", "-p", daemonPid, "-U")
			if !ok {
				daemonState = "probe_failed"
				daemonErr = code
				res.SeatDaemon.ErrorCode = code
			} else {
				daemonObjs = parseDaemonObjects(out, c.cfg.DaemonSocket)
				if len(daemonObjs) == 0 {
					daemonState = "probe_failed"
					daemonErr = ErrorParseFailed
					res.SeatDaemon.ErrorCode = ErrorParseFailed
				} else {
					daemonState = "objects"
				}
			}
		}
	}

	// 3) Desktop 实例流水线（§2.2 第 1–7 步）。
	if psOK {
		res.Instances = c.collectInstances(ctx, psAll, psTree, lsofBin, daemonObjs, daemonState)
		res.DesktopAggregate = AggregateDesktop(res.Instances)
		res.DesktopErrorCode = ErrorNone
	} else {
		res.Instances = []DesktopInstance{}
		res.DesktopAggregate = AggregateUnknown
	}

	// 4) bridgeAttachment（provider 身份 + FD 现场交叉，§2.1/§4.3）。
	att, errCode := c.collectAttachment(ctx, identity, lsofBin, daemonObjs, daemonState, daemonErr)
	res.BridgeAttachment = att
	res.BridgeErrorCode = errCode

	// 5) 其余维度。
	c.collectSeatDaemon(ctx, res)
	c.collectSeatLaunchAgent(ctx, launchctlBin, res)
	c.collectAttachConfig(ctx, launchctlBin, res)
	c.collectVersionCompatibility(ctx, res)
	c.collectLegacyManagedLoopback(ctx, psOK, psAll, res)
	c.collectLegacyDesktopPrivate(psOK, res)
	return res
}

// collectInstances 执行枚举→证据→分类流水线。daemonState=="objects" 时可判定 shared 正证据；
// "absent" 为合法负证据；"probe_failed" → shared unavailable。
func (c *darwinCollector) collectInstances(ctx context.Context, psAll []parsePsProcess, psTree []parsePsTreeNode, lsofBin string, daemonObjs map[string]bool, daemonState string) []DesktopInstance {
	byPID := map[int]parsePsTreeNode{}
	for _, n := range psTree {
		byPID[n.PID] = n
	}
	var out []DesktopInstance
	for _, row := range psAll {
		if !strings.Contains(row.Command, c.cfg.DesktopAppPath) || !strings.Contains(row.Command, "/Contents/MacOS/ChatGPT") {
			continue
		}
		out = append(out, c.collectInstance(ctx, psTree, byPID, row, lsofBin, daemonObjs, daemonState))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

func (c *darwinCollector) collectInstance(ctx context.Context, psTree []parsePsTreeNode, byPID map[int]parsePsTreeNode, row parsePsProcess, lsofBin string, daemonObjs map[string]bool, daemonState string) DesktopInstance {
	inst := DesktopInstance{PID: row.PID, Classification: DesktopClassificationUnresolved}
	// start-time 一次性身份（§2.2 第 2 步）：解析失败 → 无法防 PID 重用 → unresolved+parse_failed。
	if t, err := time.Parse("Mon Jan 2 15:04:05 2006", row.StartAt); err != nil {
		inst.Evidence = append(inst.Evidence,
			InstanceEvidence{Kind: EvidenceKindSharedFD, State: EvidenceStateUnavailable},
			InstanceEvidence{Kind: EvidenceKindPrivateStdio, State: EvidenceStateUnavailable})
		return inst
	} else {
		inst.StartTime = t.UTC().Format(time.RFC3339)
	}
	userDataDir := userDataDirOf(row.Command)

	// 后代 app-server 集（§2.2 第 3 步，递归归属）。
	desc := DescendantsOf(psTree, map[int]bool{row.PID: true})
	appServers := []int{}
	for pid := range desc {
		n, ok := byPID[pid]
		if !ok {
			continue
		}
		if strings.Contains(n.Command, "/Contents/Resources/codex") && strings.Contains(n.Command, "app-server") {
			appServers = append(appServers, pid)
		}
	}
	sort.Ints(appServers)

	sharedState, _ := c.sharedEvidence(ctx, lsofBin, row.PID, appServers, daemonObjs, daemonState)
	privateState, _ := c.privateEvidence(ctx, lsofBin, row.PID, userDataDir, appServers, byPID)

	// evidence 列表：confirmed/absent/unavailable 三态原样保留（tagged union）。
	if sharedState == probeConfirmed {
		inst.Evidence = append(inst.Evidence, InstanceEvidence{Kind: EvidenceKindSharedFD, State: EvidenceStateConfirmed})
	} else if sharedState == probeAbsent {
		inst.Evidence = append(inst.Evidence, InstanceEvidence{Kind: EvidenceKindSharedFD, State: EvidenceStateAbsent})
	} else {
		inst.Evidence = append(inst.Evidence, InstanceEvidence{Kind: EvidenceKindSharedFD, State: EvidenceStateUnavailable})
	}
	if privateState == probeConfirmed {
		inst.Evidence = append(inst.Evidence, InstanceEvidence{Kind: EvidenceKindPrivateStdio, State: EvidenceStateConfirmed})
	} else if privateState == probeAbsent {
		inst.Evidence = append(inst.Evidence, InstanceEvidence{Kind: EvidenceKindPrivateStdio, State: EvidenceStateAbsent})
	} else {
		inst.Evidence = append(inst.Evidence, InstanceEvidence{Kind: EvidenceKindPrivateStdio, State: EvidenceStateUnavailable})
	}

	// 分类（§2.2 第 6 步）：任一证据采样失败 → unresolved；both confirmed → dual 保留。
	switch {
	case sharedState == probeConfirmed && privateState == probeConfirmed:
		inst.Classification = DesktopClassificationDual
	case sharedState == probeConfirmed && privateState == probeAbsent:
		inst.Classification = DesktopClassificationSharedOnly
	case sharedState == probeAbsent && privateState == probeConfirmed:
		inst.Classification = DesktopClassificationPrivateOnly
	case sharedState == probeUnavailable || privateState == probeUnavailable:
		// 任一侧证据采样失败无法定论（防"无证据=健康"）。
		inst.Classification = DesktopClassificationUnresolved
	default:
		// 两者均 absent（实例在但无任何证据：启动中/无结论）：unresolved，不猜。
		inst.Classification = DesktopClassificationUnresolved
	}
	return inst
}

// userDataDirOf 提取 --user-data-dir= 参数（未指定 → ""，日志证据 (a) 不执行）。
func userDataDirOf(command string) string {
	fields := strings.Fields(command)
	for _, f := range fields {
		if strings.HasPrefix(f, "--user-data-dir=") {
			return strings.TrimPrefix(f, "--user-data-dir=")
		}
	}
	return ""
}

// sharedEvidence 判定单实例 shared 证据（§2.2 第 4 步）。
func (c *darwinCollector) sharedEvidence(ctx context.Context, lsofBin string, mainPid int, appServers []int, daemonObjs map[string]bool, daemonState string) (probeState, string) {
	switch daemonState {
	case "absent":
		// daemon 不存在 → 无共享可能（合法负证据）。
		return probeAbsent, ErrorNone
	case "probe_failed":
		return probeUnavailable, ErrorUnknown
	}
	if lsofBin == "" {
		return probeUnavailable, ErrorNotImplemented
	}
	pids := append([]int{mainPid}, appServers...)
	if len(pids) == 0 {
		return probeAbsent, ErrorNone
	}
	pidList := ""
	for i, p := range pids {
		if i > 0 {
			pidList += ","
		}
		pidList += fmt.Sprint(p)
	}
	out, code, ok := c.probe(ctx, lsofBin, "-n", "-P", "-a", "-p", pidList, "-U")
	if !ok {
		return probeUnavailable, code
	}
	if parsePeerCount(out, daemonObjs) > 0 {
		return probeConfirmed, ErrorNone
	}
	return probeAbsent, ErrorNone
}

// privateEvidence 判定单实例 private 证据（§2.2 第 5 步 (a)/(b)；(c) 仅实验不在此）。
func (c *darwinCollector) privateEvidence(ctx context.Context, lsofBin string, mainPid int, userDataDir string, appServers []int, byPID map[int]parsePsTreeNode) (probeState, string) {
	// (a) 日志标记（仅当 user-data-dir 已知且可读；不可读 → unavailable，不置负）。
	unavailable := false
	if userDataDir != "" {
		if hit, ok := scanTransportStdioLogs(userDataDir, 24*time.Hour); ok {
			if hit {
				return probeConfirmed, ErrorNone
			}
		} else {
			unavailable = true
		}
	}
	// (b) 父链 + stdio IPC FD 形态。Electron/Chromium 版本不同会用 PIPE 或
	// unix socketpair 承载 0/1/2；归属仍由 app-server 的 Desktop 父链保证。
	shapeFound := false
	for _, pid := range appServers {
		n, ok := byPID[pid]
		if !ok || !ParentChainContains(byPID, pid, mainPid) {
			continue
		}
		_ = n
		if lsofBin == "" {
			unavailable = true
			continue
		}
		out, code, ok := c.probe(ctx, lsofBin, "-n", "-P", "-a", "-p", fmt.Sprint(pid), "-d", "0,1,2")
		if !ok {
			unavailable = true
			_ = code
			continue
		}
		if hasStdioIPCShape(out) {
			shapeFound = true
		}
	}
	if shapeFound {
		return probeConfirmed, ErrorNone
	}
	if unavailable {
		return probeUnavailable, ErrorUnknown
	}
	return probeAbsent, ErrorNone
}

func hasStdioIPCShape(lsofOutput string) bool {
	for _, line := range strings.Split(lsofOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] == "COMMAND" {
			continue
		}
		fd := strings.TrimRight(fields[3], "urw")
		if fd != "0" && fd != "1" && fd != "2" {
			continue
		}
		if fields[4] == "PIPE" || strings.EqualFold(fields[4], "unix") {
			return true
		}
	}
	return false
}

// scanTransportStdioLogs 在 dir 下递归找 24h 内修改的 *.log，grep 字面 "transport=stdio"。
// ok=false 表示目录不可读（unavailable）；命中标记即 positive。
func scanTransportStdioLogs(dir string, within time.Duration) (hit bool, ok bool) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false, false
	}
	cutoff := time.Now().Add(-within)
	var walkErr error
	hit = false
	err = filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			walkErr = err
			return nil
		}
		if fi.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".log") || fi.ModTime().Before(cutoff) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			walkErr = err
			return nil
		}
		if strings.Contains(string(data), "transport=stdio") {
			hit = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, false
	}
	if hit {
		return true, true
	}
	if walkErr != nil {
		// 有子目录读不了：不能判负（unavailable）。
		return false, false
	}
	return false, true
}

// parseDaemonObjects 从 lsof -U 输出提取 listener（含 socket 路径的行）的 NODE 列（第 6 列）。
func parseDaemonObjects(output, socketPath string) map[string]bool {
	objs := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.Contains(strings.ToLower(line), strings.ToLower(socketPath)) {
			continue
		}
		objs[fields[5]] = true
	}
	return objs
}

// parsePeerCount 统计 NAME 为 "->0x…" 且命中 daemon object 的行数（与 verify 脚本一致）。
func parsePeerCount(output string, daemonObjs map[string]bool) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		for _, f := range strings.Fields(line) {
			if strings.HasPrefix(f, "->") && daemonObjs[strings.TrimPrefix(f, "->")] {
				count++
				break
			}
		}
	}
	return count
}

// collectAttachment 实现 §2.1/§4.3：provider 身份为主，FD 现场交叉；数量 ≥2 不作唯一判据。
func (c *darwinCollector) collectAttachment(ctx context.Context, identity core.CodexWebTransportIdentity, lsofBin string, daemonObjs map[string]bool, daemonState string, daemonErr string) (BridgeAttachment, string) {
	peerCount := 0
	presentErr := ""
	switch {
	case daemonState == "absent":
		// daemon 无 listener：provider 若声称在连则冲突 → unresolved。
		presentErr = ErrorProcessMissing
	case daemonState == "probe_failed":
		presentErr = daemonErr
		if presentErr == "" {
			presentErr = ErrorUnknown
		}
	default:
		if lsofBin == "" {
			presentErr = ErrorNotImplemented
		} else {
			out, code, ok := c.probe(ctx, lsofBin, "-n", "-P", "-a", "-p", fmt.Sprint(os.Getpid()), "-U")
			if !ok {
				presentErr = code
			} else {
				peerCount = parsePeerCount(out, daemonObjs)
			}
		}
	}

	mainState := roleAttachmentState(identity.Main, presentErr, peerCount)
	obsState := roleAttachmentState(identity.Observer, presentErr, peerCount)
	if mainState == "unresolved" || obsState == "unresolved" {
		code := presentErr
		if code == "" || code == ErrorNone {
			if identity.Main.ErrorCode != "" && identity.Main.ErrorCode != ErrorNone {
				code = identity.Main.ErrorCode
			} else if identity.Observer.ErrorCode != "" && identity.Observer.ErrorCode != ErrorNone {
				code = identity.Observer.ErrorCode
			} else {
				code = ErrorUnknown
			}
		}
		return AttachmentUnresolved, code
	}
	if mainState == "present" && obsState == "present" {
		if peerCount >= 2 {
			return AttachmentShared, ErrorNone
		}
		return AttachmentUnresolved, ErrorUnknown
	}
	if mainState == "present" || obsState == "present" {
		if peerCount >= 1 {
			return AttachmentPartial, ErrorNone
		}
		return AttachmentUnresolved, ErrorUnknown
	}
	return AttachmentAbsent, ErrorNone
}

// roleAttachmentState：resolved-absent / present / unresolved（§4.3 语义，§2.1 FD 交叉）。
// ErrorCode 非 "none"（含空串=未采样、unknown/timeout/rpc_failed）一律 unresolved。
func roleAttachmentState(r core.CodexWebTransportRoleState, fdErr string, peerCount int) string {
	if r.ErrorCode != ErrorNone {
		return "unresolved"
	}
	if !r.Attached {
		return "absent" // ErrorCode==none 且未附着 = 明确未附着
	}
	if fdErr != "" {
		return "unresolved" // Attached 但 FD 现场不可判
	}
	if peerCount >= 1 {
		return "present"
	}
	return "unresolved" // Attached=true 但 lsof 无匹配 peer（§2.1）
}

// collectSeatDaemon 以 version_probe（<standalone> app-server daemon version JSON）为权威；
// 失败保持 unresolved+错误码；成功按 status 判 running/stopped。standalone 文件缺失 →
// process_missing（产品文件不存在，非系统命令缺失）。
func (c *darwinCollector) collectSeatDaemon(ctx context.Context, res *CollectedTopology) {
	if c.cfg.StandaloneBin == "" {
		res.SeatDaemon.ErrorCode = ErrorProcessMissing
		return
	}
	if _, err := os.Stat(c.cfg.StandaloneBin); err != nil {
		res.SeatDaemon.ErrorCode = ErrorProcessMissing
		return
	}
	out, code, ok := c.probe(ctx, c.cfg.StandaloneBin, "app-server", "daemon", "version")
	if !ok {
		res.SeatDaemon.ErrorCode = code
		return
	}
	var v struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &v); err != nil {
		res.SeatDaemon.ErrorCode = ErrorParseFailed
		return
	}
	if v.Status == "running" {
		res.SeatDaemon = DimValue{Enum: "running", Source: DimSourceVersionProbe, ErrorCode: ErrorNone}
	} else {
		res.SeatDaemon = DimValue{Enum: "stopped", Source: DimSourceVersionProbe, ErrorCode: ErrorNone}
	}
}

func (c *darwinCollector) collectSeatLaunchAgent(ctx context.Context, launchctlBin string, res *CollectedTopology) {
	if launchctlBin == "" {
		res.SeatLaunchAgent.ErrorCode = ErrorNotImplemented
		return
	}
	out, code, ok := c.probe(ctx, launchctlBin, "list", c.cfg.LaunchAgentLabel)
	if !ok {
		res.SeatLaunchAgent.ErrorCode = code
		return
	}
	if strings.Contains(out, `"PID" =`) {
		res.SeatLaunchAgent.Enum = "healthy"
		res.SeatLaunchAgent.ErrorCode = ErrorNone
		return
	}
	res.SeatLaunchAgent.Enum = "failed"
	res.SeatLaunchAgent.ErrorCode = ErrorNone
}

func (c *darwinCollector) collectAttachConfig(ctx context.Context, launchctlBin string, res *CollectedTopology) {
	if launchctlBin == "" {
		res.AttachConfig.ErrorCode = ErrorNotImplemented
		return
	}
	out, code, ok := c.probe(ctx, launchctlBin, "getenv", "CODEX_APP_SERVER_USE_LOCAL_DAEMON")
	if !ok {
		res.AttachConfig.ErrorCode = code
		return
	}
	if strings.TrimSpace(out) == "1" {
		res.AttachConfig.Enum = "enabled"
	} else {
		res.AttachConfig.Enum = "disabled"
	}
	res.AttachConfig.ErrorCode = ErrorNone
}

// collectVersionCompatibility：shared FD 优先 → effective_compatible；否则 standalone --version
// 探针成功 → probe_compatible；失败 → unknown。probe_incompatible 需版本表，本 Phase 不产生。
func (c *darwinCollector) collectVersionCompatibility(ctx context.Context, res *CollectedTopology) {
	if res.BridgeAttachment == AttachmentShared {
		res.VersionCompatibility = DimValue{Enum: "effective_compatible", Source: DimSourceLsofFDPeer, ErrorCode: ErrorNone}
		return
	}
	if c.cfg.StandaloneBin == "" {
		res.VersionCompatibility.ErrorCode = ErrorProcessMissing
		return
	}
	if _, err := os.Stat(c.cfg.StandaloneBin); err != nil {
		res.VersionCompatibility.ErrorCode = ErrorProcessMissing
		return
	}
	if _, code, ok := c.probe(ctx, c.cfg.StandaloneBin, "--version"); !ok {
		res.VersionCompatibility.ErrorCode = code
		return
	}
	res.VersionCompatibility = DimValue{Enum: "probe_compatible", Source: DimSourceVersionProbe, ErrorCode: ErrorNone}
}

func (c *darwinCollector) collectLegacyManagedLoopback(ctx context.Context, psOK bool, psAll []parsePsProcess, res *CollectedTopology) {
	if !psOK {
		return // ErrorCode 已由调用方设置
	}
	_ = ctx
	found := false
	for _, row := range psAll {
		if strings.Contains(row.Command, "app-server") && strings.Contains(row.Command, "--listen") && strings.Contains(row.Command, "127.0.0.1:") {
			found = true
			break
		}
	}
	if found {
		res.LegacyManagedLoopback = DimValue{Enum: "present", Source: DimSourceProcessTree, ErrorCode: ErrorNone}
	} else {
		res.LegacyManagedLoopback = DimValue{Enum: "absent", Source: DimSourceProcessTree, ErrorCode: ErrorNone}
	}
}

// collectLegacyDesktopPrivate：任一实例 private confirmed → present；存在采样失败 → unresolved；
// 否则 absent。
func (c *darwinCollector) collectLegacyDesktopPrivate(psOK bool, res *CollectedTopology) {
	if !psOK {
		return
	}
	present, unresolved := false, false
	for _, inst := range res.Instances {
		for _, ev := range inst.Evidence {
			if ev.Kind == EvidenceKindPrivateStdio {
				switch ev.State {
				case EvidenceStateConfirmed:
					present = true
				case EvidenceStateUnavailable:
					unresolved = true
				}
			}
		}
	}
	switch {
	case present:
		res.LegacyDesktopPrivate = DimValue{Enum: "present", Source: DimSourceProcessTree, ErrorCode: ErrorNone}
	case unresolved:
		res.LegacyDesktopPrivate = DimValue{Enum: "unresolved", Source: DimSourceProcessTree, ErrorCode: ErrorUnknown}
	default:
		res.LegacyDesktopPrivate = DimValue{Enum: "absent", Source: DimSourceProcessTree, ErrorCode: ErrorNone}
	}
}
