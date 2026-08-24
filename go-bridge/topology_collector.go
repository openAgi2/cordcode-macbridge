package gobridge

// topology_collector.go —— 拓扑证据采集器公共层（implementation plan v2 §2.2/§3 C1/C2）。
//
// 平台分层：
//   - topology_collector_darwin.go（//go:build darwin）：lsof/ps/launchctl 只读采集流水线。
//   - topology_collector_stub.go（//!darwin）：NewTopologyCollector 返回 stubCollector。
//
// 安全（§2.2 第 8 步 / §7.2）：全程只读；不读会话内容；不终止/修改进程；不改 launchctl
// 环境；Command 只按固定参数执行，无 shell；正证据之外不读取 log 文件正文（仅 grep 字面
// "transport=stdio" 标记）。任意失败以维度/实例级 errorCode 呈现，绝不把"无证据"当健康。

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TopologyCollector 是一次只读采样的入口。Collect 不返回整体错误：单维失败以
// CollectedTopology 内的错误码呈现（失败可见，P2-4/§6 回滚原则）。
type TopologyCollector interface {
	Collect(ctx context.Context, identity core.CodexWebTransportIdentity) *CollectedTopology
}

// DimValue 是单个维度原始观测值（source/errorCode 为 DTO §2.3 枚举）。
type DimValue struct {
	Enum      string
	Source    string
	ErrorCode string
}

// CollectedTopology 是一次采集产出的全部原始观测（未防抖、未派生 syncHealth）。
type CollectedTopology struct {
	BridgeAttachment      BridgeAttachment
	BridgeErrorCode       string
	DesktopAggregate      TopologyAggregate
	DesktopErrorCode      string
	Instances             []DesktopInstance
	SeatDaemon            DimValue
	SeatLaunchAgent       DimValue
	AttachConfig          DimValue
	VersionCompatibility  DimValue
	LegacyManagedLoopback DimValue
	LegacyDesktopPrivate  DimValue
}

// TopologyCollectorConfig 冻结采集参数；零值由 NewPlatformCollector 填充默认。
type TopologyCollectorConfig struct {
	CodexHome        string        // CODEX_HOME，默认 $CODEX_HOME 或 $HOME/.codex
	DesktopAppPath   string        // 默认 /Applications/ChatGPT.app
	StandaloneBin    string        // 默认 <CodexHome>/packages/standalone/current/codex
	DaemonSocket     string        // 默认 <CodexHome>/app-server-control/app-server-control.sock
	LaunchAgentLabel string        // 默认 org.openagi.cordcode.codex-app-server-daemon
	Timeout          time.Duration // 单次探针预算，默认 5s（§2.5 probe timeout ≤5s）
	PsPath           string        // 默认 exec.LookPath("ps")；空且找不到 → not_implemented
	LsofPath         string        // 默认 exec.LookPath("lsof")
	LaunchctlPath    string        // 默认 exec.LookPath("launchctl")
}

// NewTopologyCollector 按平台构造采集器（darwin 完整流水线；其它平台恒 not_implemented）。
func NewTopologyCollector() TopologyCollector {
	return newPlatformCollector()
}

// stubCollector 是 C2 的非 darwin 实现：全部维度 not_implemented，不启动任何循环命令、
// 不产生 split 结论（P2-3）。放在公共层以便任何平台都能测试其行为。
type stubCollector struct{}

func newStubCollector() *stubCollector { return &stubCollector{} }

func (s *stubCollector) Collect(_ context.Context, _ core.CodexWebTransportIdentity) *CollectedTopology {
	return &CollectedTopology{
		BridgeAttachment:      AttachmentUnresolved,
		BridgeErrorCode:       ErrorNotImplemented,
		DesktopAggregate:      AggregateUnknown,
		DesktopErrorCode:      ErrorNotImplemented,
		Instances:             []DesktopInstance{},
		SeatDaemon:            DimValue{Enum: "unresolved", Source: DimSourceProcessTree, ErrorCode: ErrorNotImplemented},
		SeatLaunchAgent:       DimValue{Enum: "unresolved", Source: DimSourceLaunchdProbe, ErrorCode: ErrorNotImplemented},
		AttachConfig:          DimValue{Enum: "unresolved", Source: DimSourceLaunchdProbe, ErrorCode: ErrorNotImplemented},
		VersionCompatibility:  DimValue{Enum: "unknown", Source: DimSourceVersionProbe, ErrorCode: ErrorNotImplemented},
		LegacyManagedLoopback: DimValue{Enum: "unresolved", Source: DimSourceProcessTree, ErrorCode: ErrorNotImplemented},
		LegacyDesktopPrivate:  DimValue{Enum: "unresolved", Source: DimSourceProcessTree, ErrorCode: ErrorNotImplemented},
	}
}

// ————— 公共解析与分类帮助函数（纯函数，任何平台可测）—————

// parsePsProcess 是 ps -axo pid=,lstart=,command= 的解析产物。
type parsePsProcess struct {
	PID     int
	StartAt string // lstart 原值（RFC3339 转换失败时保留给上层做 unresolved 判据）
	Command string
}

// ParsePsProcessRows 解析 ps 表格（列序：pid lstart*5 command）。命令含空格/参数不截断。
func ParsePsProcessRows(output string) ([]parsePsProcess, error) {
	var rows []parsePsProcess
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			return nil, errors.New("ps row short: " + line)
		}
		pid, err := parsePid(fields[0])
		if err != nil {
			return nil, err
		}
		rows = append(rows, parsePsProcess{
			PID:     pid,
			StartAt: strings.Join(fields[1:6], " "), // lstart = 5 个 token
			Command: strings.Join(fields[6:], " "),
		})
	}
	return rows, nil
}

// ParsePsTreeRows 解析 ps -axo pid=,ppid=,command=（进程树）。
type parsePsTreeNode struct {
	PID     int
	PPID    int
	Command string
}

func ParsePsTreeRows(output string) ([]parsePsTreeNode, error) {
	var rows []parsePsTreeNode
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, errors.New("ps tree row short: " + line)
		}
		pid, err := parsePid(fields[0])
		if err != nil {
			return nil, err
		}
		ppid, err := parsePid(fields[1])
		if err != nil {
			return nil, err
		}
		rows = append(rows, parsePsTreeNode{PID: pid, PPID: ppid, Command: strings.Join(fields[2:], " ")})
	}
	return rows, nil
}

func parsePid(s string) (int, error) {
	var pid int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("invalid pid: " + s)
		}
		pid = pid*10 + int(r-'0')
	}
	if pid <= 0 {
		return 0, errors.New("non-positive pid: " + s)
	}
	return pid, nil
}

// DescendantsOf 返回以 roots 为根的递归后代 PID 集（BFS；含 root 自身时不返回 root）。
func DescendantsOf(nodes []parsePsTreeNode, roots map[int]bool) map[int]bool {
	children := map[int][]int{}
	for _, n := range nodes {
		children[n.PPID] = append(children[n.PPID], n.PID)
	}
	out := map[int]bool{}
	queue := []int{}
	for r := range roots {
		queue = append(queue, r)
	}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if out[child] {
				continue
			}
			out[child] = true
			queue = append(queue, child)
		}
	}
	return out
}

// ParentChainContains 判断 startPid 的祖先链（含自身）是否含 root（防御性再验：后代集合
// 本身已由 DescendantsOf 保证，此处直接上溯复核）。
func ParentChainContains(byPID map[int]parsePsTreeNode, startPid, root int) bool {
	cur := startPid
	for step := 0; step < 512; step++ {
		if cur == root {
			return true
		}
		n, ok := byPID[cur]
		if !ok || n.PPID == 0 || n.PPID == cur {
			return false
		}
		cur = n.PPID
	}
	return false
}

// classifyExecFailure 把探针执行失败归类为冻结错误码（§2.3 表）。
func classifyExecFailure(stderr string, err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorTimeout
	}
	if strings.Contains(stderr, "Operation not permitted") || strings.Contains(stderr, "Permission denied") {
		return ErrorPermission
	}
	return ErrorUnknown
}

// runTimed 执行命令并限制预算（参数固定，无 shell）。
func runTimed(ctx context.Context, bin string, args []string, timeout time.Duration) (string, string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}
