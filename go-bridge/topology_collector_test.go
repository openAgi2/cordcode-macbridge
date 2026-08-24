package gobridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ————— 测试脚手架 —————

func testCollectorCfg() TopologyCollectorConfig {
	return TopologyCollectorConfig{
		CodexHome:        "/fake/codex-home",
		DesktopAppPath:   "/Applications/ChatGPT.app",
		StandaloneBin:    "/bin/sh", // 真实存在的文件：通过 stat 检查；调用已被假探针接管
		DaemonSocket:     "/fake/codex-home/app-server-control/app-server-control.sock",
		LaunchAgentLabel: "org.openagi.cordcode.codex-app-server-daemon",
		Timeout:          5 * time.Second,
		PsPath:           "/usr/bin/ps",
		LsofPath:         "/usr/bin/lsof",
		LaunchctlPath:    "/bin/launchctl",
	}
}

// fakeCollector 用按 (bin, args-joined) 匹配的假探针构造采集器。
func fakeCollector(match func(bin string, args []string) (string, string, error)) *darwinCollector {
	c := newDarwinCollector(testCollectorCfg())
	if match != nil {
		c.run = func(_ context.Context, bin string, args ...string) (string, string, error) {
			return match(bin, args)
		}
	}
	return c
}

// happyProbe 返回"健康"场景：
//   - 一个 shared Desktop 主进程 1234，其后代 app-server 9001（含祖父 5000 的递归链）；
//   - daemon 7777 的 listener object 0xabc；1234/9001 各有一个 peer 命中 0xabc；
//   - app-server 9001 的 fd 0/1/2 是 CHR（无 stdio IPC）→ private absent → shared_only。
func happyProbe(mainCmd string, descendants ...string) func(string, []string) (string, string, error) {
	tree := "1234 1 " + mainCmd + "\n"
	tree += "5000 1234 /Applications/ChatGPT.app/Contents/Resources/codex app-server --listen ws://127.0.0.1:12345 --parent-pid 1234 --user-data-dir /tmp/ud\n"
	for _, d := range descendants {
		tree += d + " 5000 /Applications/ChatGPT.app/Contents/Resources/codex app-server --user-data-dir /tmp/ud --parent-pid 1234\n"
	}
	psRows := "1234 " + strings.Join([]string{"Tue", "Aug", "23", "17:00:00", "2026"}, " ") + " " + mainCmd + "\n"
	psRows += "9001 Tue Aug 23 17:00:10 2026 /Applications/ChatGPT.app/Contents/Resources/codex app-server --listen ws://127.0.0.1:12345 --user-data-dir /tmp/ud\n"
	return func(bin string, args []string) (string, string, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "lstart"):
			return psRows, "", nil
		case strings.Contains(joined, "ppid"):
			return tree, "", nil
		case joined == "-t /fake/codex-home/app-server-control/app-server-control.sock":
			return "7777\n", "", nil
		case strings.Contains(joined, "-p 7777") && strings.Contains(joined, "-U"):
			return "codex 7777 user 4u unix 0xabc /fake/codex-home/app-server-control/app-server-control.sock\n", "", nil
		case strings.Contains(joined, "-d"): // private FD 形态：无 PIPE/unix socketpair
			return "codex 9001 user 2w CHR 3,2 0t0 336 /dev/null\n", "", nil
		case strings.Contains(joined, "-U") && strings.Contains(joined, "-p "):
			return "ChatGPT 1234 user 6u unix ->0xabc\ncodex 9001 user 5u unix ->0xabc\n", "", nil
		case strings.Contains(joined, "daemon version"):
			return `{"status":"running","pid":7777}`, "", nil
		case joined == "--version":
			return "codex 0.0.1\n", "", nil
		case strings.Contains(joined, "list"):
			return "{\n\t\"PID\" = 7777;\n}", "", nil
		case strings.Contains(joined, "getenv"):
			return "1", "", nil
		}
		return "", "", nil
	}
}

func TestStubCollectorNotImplemented(t *testing.T) {
	out := newStubCollector().Collect(context.Background(), coreZeroIdentity())
	if out.BridgeAttachment != AttachmentUnresolved || out.BridgeErrorCode != ErrorNotImplemented {
		t.Fatalf("stub bridge: %s/%s", out.BridgeAttachment, out.BridgeErrorCode)
	}
	if out.DesktopAggregate != AggregateUnknown || out.DesktopErrorCode != ErrorNotImplemented {
		t.Fatalf("stub aggregate: %s/%s", out.DesktopAggregate, out.DesktopErrorCode)
	}
	if len(out.Instances) != 0 {
		t.Fatalf("stub must not report instances: %d", len(out.Instances))
	}
	for name, d := range map[string]DimValue{
		"seatDaemon": out.SeatDaemon, "seatLaunchAgent": out.SeatLaunchAgent, "attachConfig": out.AttachConfig,
		"versionCompatibility": out.VersionCompatibility, "legacyManagedLoopback": out.LegacyManagedLoopback,
		"legacyDesktopPrivate": out.LegacyDesktopPrivate,
	} {
		if d.ErrorCode != ErrorNotImplemented {
			t.Errorf("stub %s errorCode = %q, want not_implemented", name, d.ErrorCode)
		}
	}
}

func TestCommandMissingNotImplemented(t *testing.T) {
	cfg := testCollectorCfg()
	cfg.PsPath, cfg.LsofPath, cfg.LaunchctlPath = "", "", ""
	cfg.StandaloneBin = "/nonexistent/standalone/codex"
	c := newDarwinCollector(cfg)
	c.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	// 即使命令缺失，也必须让命令路径为空 → 各维度 not_implemented；不报 split。
	out := c.Collect(context.Background(), coreZeroIdentity())
	if out.DesktopAggregate != AggregateUnknown || out.DesktopErrorCode != ErrorNotImplemented {
		t.Fatalf("missing commands aggregate = %s/%s, want unknown/not_implemented", out.DesktopAggregate, out.DesktopErrorCode)
	}
	if out.BridgeAttachment != AttachmentUnresolved || out.BridgeErrorCode != ErrorNotImplemented {
		t.Fatalf("missing commands bridge = %s/%s, want unresolved/not_implemented", out.BridgeAttachment, out.BridgeErrorCode)
	}
	if len(out.Instances) != 0 {
		t.Fatalf("missing commands must not enumerate instances: %d", len(out.Instances))
	}
	// lsof/launchctl 亦缺失：launchd 维度 not_implemented；standalone 缺失是产品文件缺失
	// （process_missing），与系统命令缺失分开。
	if out.SeatLaunchAgent.ErrorCode != ErrorNotImplemented || out.AttachConfig.ErrorCode != ErrorNotImplemented {
		t.Fatalf("launchd dims must be not_implemented: %+v/%+v", out.SeatLaunchAgent, out.AttachConfig)
	}
	if out.SeatDaemon.ErrorCode != ErrorProcessMissing || out.VersionCompatibility.ErrorCode != ErrorProcessMissing {
		t.Fatalf("standalone-missing dims must be process_missing: %+v/%+v", out.SeatDaemon, out.VersionCompatibility)
	}
}

func TestSharedOnlyClassification(t *testing.T) {
	main := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT --restore-last-session"
	c := fakeCollector(happyProbe(main))
	out := c.Collect(context.Background(), coreZeroIdentity())
	if len(out.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(out.Instances))
	}
	inst := out.Instances[0]
	if inst.Classification != DesktopClassificationSharedOnly {
		t.Fatalf("classification = %s, want shared_only (evidence %+v)", inst.Classification, inst.Evidence)
	}
	if inst.StartTime == "" {
		t.Fatalf("startTime must be RFC3339 from lstart")
	}
	if out.DesktopAggregate != AggregateAllShared {
		t.Fatalf("aggregate = %s, want all_shared", out.DesktopAggregate)
	}
	if out.LegacyManagedLoopback.Enum != "present" {
		t.Fatalf("legacyManagedLoopback = %s (ps row has managed-loopback listen cmd)", out.LegacyManagedLoopback.Enum)
	}
}

func TestDualEvidencePreserved(t *testing.T) {
	main := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT --restore-last-session"
	base := happyProbe(main)
	c := fakeCollector(func(bin string, args []string) (string, string, error) {
		joined := strings.Join(args, " ")
		// 先把 private FD 形态改成 PIPE（覆盖默认非 PIPE 输出），其余走 happyProbe。
		if strings.Contains(joined, "-d") {
			return "codex 9001 user 0u PIPE 0xdef /private/tmp/pipe\n", "", nil
		}
		return base(bin, args)
	})
	out := c.Collect(context.Background(), coreZeroIdentity())
	if len(out.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(out.Instances))
	}
	inst := out.Instances[0]
	// shared 与 private 正证据并存 → dual（不先到者覆盖）。
	if inst.Classification != DesktopClassificationDual {
		t.Fatalf("classification = %s, want dual (evidence %+v)", inst.Classification, inst.Evidence)
	}
	if out.DesktopAggregate != AggregateMixed {
		t.Fatalf("aggregate = %s, want mixed", out.DesktopAggregate)
	}
}

func TestPrivateOnlyWhenDaemonAbsent(t *testing.T) {
	main := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT --user-data-dir /tmp/isolated"
	base := happyProbe(main)
	c := fakeCollector(func(bin string, args []string) (string, string, error) {
		joined := strings.Join(args, " ")
		if joined == "-t /fake/codex-home/app-server-control/app-server-control.sock" {
			// daemon socket 不存在：lsof 空输出（等如无 listener）。
			return "", "", nil
		}
		if strings.Contains(joined, "-d") {
			return "codex 9001 user 0u PIPE 0xdef /private/tmp/pipe\n", "", nil
		}
		out, stderr, err := base(bin, args)
		if out != "" && strings.Contains(out, "0xabc") && strings.Contains(joined, "-p 7777") {
			// daemon 无 object：不提供对象集合。
			return "", "", nil
		}
		return out, stderr, err
	})
	out := c.Collect(context.Background(), coreZeroIdentity())
	if len(out.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(out.Instances))
	}
	inst := out.Instances[0]
	if inst.Classification != DesktopClassificationPrivateOnly {
		t.Fatalf("classification = %s, want private_only (evidence %+v)", inst.Classification, inst.Evidence)
	}
	if out.DesktopAggregate != AggregateSplitPresent {
		t.Fatalf("aggregate = %s, want split_present", out.DesktopAggregate)
	}
	if out.LegacyDesktopPrivate.Enum != "present" {
		t.Fatalf("legacyDesktopPrivate = %s, want present", out.LegacyDesktopPrivate.Enum)
	}
	// 关闭的 daemon（socket 缺失）是合法负证据：版本探针仍报告 running？
	// seatDaemon 由 version 探针裁决：本 fake 输出 running（探针独立于 socket）。
	if out.SeatDaemon.Enum != "running" {
		t.Fatalf("seatDaemon = %s, want running (version probe authoritative)", out.SeatDaemon.Enum)
	}
}

func TestPrivateOnlyWithDesktop151UnixSocketpairStdio(t *testing.T) {
	main := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT --user-data-dir /tmp/isolated"
	base := happyProbe(main)
	c := fakeCollector(func(bin string, args []string) (string, string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "-d 0,1,2") {
			return "COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME\n" +
				"codex 9001 user 0u unix 0xabc 0t0 ->0xdef\n", "", nil
		}
		return base(bin, args)
	})

	out := c.Collect(context.Background(), coreZeroIdentity())
	if len(out.Instances) != 1 || out.Instances[0].Classification != DesktopClassificationDual {
		t.Fatalf("Desktop 151 socketpair stdio classification = %+v, want dual", out.Instances)
	}
}

func TestHasStdioIPCShapeRejectsUnrelatedFDs(t *testing.T) {
	if !hasStdioIPCShape("codex 9001 user 0u PIPE 0xabc 16384 ->0xdef\n") {
		t.Fatal("PIPE fd 0 must be recognized")
	}
	if !hasStdioIPCShape("codex 9001 user 1u unix 0xabc 0t0 ->0xdef\n") {
		t.Fatal("unix socketpair fd 1 must be recognized")
	}
	if hasStdioIPCShape("codex 9001 user 6u unix 0xabc 0t0 ->0xdef\n") {
		t.Fatal("non-stdio unix fd must not be recognized")
	}
}

func TestPermissionFailureUnresolved(t *testing.T) {
	c := fakeCollector(func(bin string, args []string) (string, string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "lstart") || strings.Contains(joined, "ppid") {
			return "", "", errors.New("fork/exec: permission denied")
		}
		return "", "Operation not permitted", errors.New("exit status 1")
	})
	out := c.Collect(context.Background(), coreZeroIdentity())
	if out.DesktopAggregate != AggregateUnknown {
		t.Fatalf("ps permission: aggregate = %s, want unknown（不报 split）", out.DesktopAggregate)
	}
	if out.DesktopErrorCode != ErrorUnknown && out.DesktopErrorCode != ErrorPermission {
		// fork/exec 失败归类 Unknown（非直接 timeout/permission 文本）。
		t.Fatalf("ps failure code = %q", out.DesktopErrorCode)
	}
	if out.BridgeAttachment != AttachmentUnresolved || out.BridgeErrorCode != ErrorPermission {
		t.Fatalf("lsof permission: bridge = %s/%s, want unresolved/permission", out.BridgeAttachment, out.BridgeErrorCode)
	}
}

func TestAttachmentProviderFDPeeringConflict(t *testing.T) {
	// provider 声称 main+observer 都在连，但 bridge PID 现场无 peer → unresolved（§2.1）。
	main := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT"
	base := happyProbe(main)
	c := fakeCollector(func(bin string, args []string) (string, string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, fmt.Sprint(os.Getpid())) {
			return "", "", nil // bridge 自身无任何 daemon peer
		}
		return base(bin, args)
	})
	ident := core.CodexWebTransportIdentity{
		Epoch: 5, Endpoint: "/x/app-server-control.sock",
		Main:     core.CodexWebTransportRoleState{Attached: true, Epoch: 5, PeerKey: "/x#5", ErrorCode: "none"},
		Observer: core.CodexWebTransportRoleState{Attached: true, Epoch: 5, PeerKey: "/x#5", ErrorCode: "none"},
	}
	out := c.Collect(context.Background(), ident)
	if out.BridgeAttachment != AttachmentUnresolved {
		t.Fatalf("FD conflict: attachment = %s, want unresolved", out.BridgeAttachment)
	}
}

func TestAttachmentSharedTwoPeers(t *testing.T) {
	main := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT"
	base := happyProbe(main)
	c := fakeCollector(func(bin string, args []string) (string, string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, fmt.Sprint(os.Getpid())) && strings.Contains(joined, "-U") {
			return "codex 1111 user 3u unix ->0xabc\ncodex 1111 user 4u unix ->0xabc\n", "", nil
		}
		return base(bin, args)
	})
	ident := core.CodexWebTransportIdentity{
		Epoch: 5, Endpoint: "/x/app-server-control.sock",
		Main:     core.CodexWebTransportRoleState{Attached: true, Epoch: 5, PeerKey: "/x#5", ErrorCode: "none"},
		Observer: core.CodexWebTransportRoleState{Attached: true, Epoch: 5, PeerKey: "/x#5", ErrorCode: "none"},
	}
	out := c.Collect(context.Background(), ident)
	if out.BridgeAttachment != AttachmentShared {
		t.Fatalf("attachment = %s, want shared", out.BridgeAttachment)
	}
}

func TestAttachmentAbsentBothRoles(t *testing.T) {
	c := fakeCollector(happyProbe("/Applications/ChatGPT.app/Contents/MacOS/ChatGPT"))
	ident := core.CodexWebTransportIdentity{
		Main:     core.CodexWebTransportRoleState{Attached: false, ErrorCode: "none"},
		Observer: core.CodexWebTransportRoleState{Attached: false, ErrorCode: "none"},
	}
	out := c.Collect(context.Background(), ident)
	if out.BridgeAttachment != AttachmentAbsent {
		t.Fatalf("attachment = %s, want absent", out.BridgeAttachment)
	}
}

func TestParseHelpers(t *testing.T) {
	rows, err := ParsePsProcessRows("1234 Tue Aug 23 17:00:00 2026 /Applications/ChatGPT.app/Contents/MacOS/ChatGPT --flag\n\n")
	if err != nil || len(rows) != 1 {
		t.Fatalf("parse rows: %d rows err=%v", len(rows), err)
	}
	if rows[0].PID != 1234 || rows[0].StartAt != "Tue Aug 23 17:00:00 2026" || !strings.Contains(rows[0].Command, "--flag") {
		t.Fatalf("row parse mismatch: %+v", rows[0])
	}
	if _, err := ParsePsProcessRows("short"); err == nil {
		t.Fatal("short row must error")
	}
	tree, err := ParsePsTreeRows("9001 5000 codex app-server\n")
	if err != nil || len(tree) != 1 || tree[0].PPID != 5000 {
		t.Fatalf("tree parse: %+v err=%v", tree, err)
	}
	// 真实 macOS ps：PID 1 的 ppid=0 必须合法。
	tree, err = ParsePsTreeRows("1 0 /sbin/launchd\n")
	if err != nil || len(tree) != 1 || tree[0].PID != 1 || tree[0].PPID != 0 {
		t.Fatalf("ppid=0 parse: %+v err=%v", tree, err)
	}
	if _, err := ParsePsTreeRows("1 ? /sbin/launchd\n"); err == nil {
		t.Fatal("non-numeric ppid must error")
	}
	// 递归后代：1234 → 5000 → 9001。
	desc := DescendantsOf([]parsePsTreeNode{{PID: 5000, PPID: 1234}, {PID: 9001, PPID: 5000}},
		map[int]bool{1234: true})
	if !desc[5000] || !desc[9001] || len(desc) != 2 {
		t.Fatalf("descendants = %v, want {5000,9001}", desc)
	}
	if !ParentChainContains(map[int]parsePsTreeNode{5000: {PID: 5000, PPID: 1234}, 9001: {PID: 9001, PPID: 5000}}, 9001, 1234) {
		t.Fatal("parent chain 9001→1234 must contain root")
	}
}

func TestInstanceStartTimeIdentityFailure(t *testing.T) {
	main := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT"
	base := happyProbe(main)
	c := fakeCollector(func(bin string, args []string) (string, string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "lstart") {
			return "1234 garbage-lstart weekday month day 2026 /Applications/ChatGPT.app/Contents/MacOS/ChatGPT\n", "", nil
		}
		return base(bin, args)
	})
	out := c.Collect(context.Background(), coreZeroIdentity())
	if len(out.Instances) != 1 {
		t.Fatalf("instances = %d", len(out.Instances))
	}
	inst := out.Instances[0]
	// start-time 解析失败 → 一次性身份不可用 → unresolved（防伪装 PID 重用）。
	if inst.Classification != DesktopClassificationUnresolved || inst.StartTime != "" {
		t.Fatalf("identity failure must yield unresolved without startTime: %+v", inst)
	}
	for _, ev := range inst.Evidence {
		if ev.State != EvidenceStateUnavailable {
			t.Fatalf("identity failure evidence must be unavailable: %+v", inst.Evidence)
		}
	}
}

func TestScanTransportStdioLogs(t *testing.T) {
	// 无标记 → negative。
	empty := t.TempDir()
	if hit, ok := scanTransportStdioLogs(empty, 24*time.Hour); hit || !ok {
		t.Fatalf("empty dir: hit=%v ok=%v, want false/true", hit, ok)
	}
	// 有标记 → positive。
	marked := t.TempDir()
	if err := os.WriteFile(filepath.Join(marked, "app.log"), []byte("session started transport=stdio\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if hit, ok := scanTransportStdioLogs(marked, 24*time.Hour); !hit || !ok {
		t.Fatalf("marker dir: hit=%v ok=%v, want true/true", hit, ok)
	}
	// 目录不存在 → unavailable。
	if _, ok := scanTransportStdioLogs(filepath.Join(empty, "nope"), 24*time.Hour); ok {
		t.Fatal("missing dir must be unavailable")
	}
	// 过期日志（mtime 2d 前）不算。
	stale := t.TempDir()
	old := filepath.Join(stale, "old.log")
	if err := os.WriteFile(old, []byte("transport=stdio\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if hit, ok := scanTransportStdioLogs(stale, 24*time.Hour); hit || !ok {
		t.Fatalf("old log must not hit: hit=%v ok=%v", hit, ok)
	}
}

func TestClassifyExecFailure(t *testing.T) {
	if got := classifyExecFailure("", context.DeadlineExceeded); got != ErrorTimeout {
		t.Fatalf("deadline → %q, want timeout", got)
	}
	if got := classifyExecFailure("Operation not permitted", errors.New("exit")); got != ErrorPermission {
		t.Fatalf("permission → %q", got)
	}
	if got := classifyExecFailure("", errors.New("boom")); got != ErrorUnknown {
		t.Fatalf("generic → %q, want unknown", got)
	}
}

func TestMixedTwoInstances(t *testing.T) {
	main1 := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT --restore-last-session"
	main2 := "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT --user-data-dir /tmp/isolated"
	c := fakeCollector(func(bin string, args []string) (string, string, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "lstart"):
			return "1234 Tue Aug 23 17:00:00 2026 " + main1 + "\n5678 Tue Aug 23 18:00:00 2026 " + main2 + "\n", "", nil
		case strings.Contains(joined, "ppid"):
			return "1234 1 " + main1 + "\n5678 1 " + main2 + "\n9001 1234 /Applications/ChatGPT.app/Contents/Resources/codex app-server --user-data-dir /tmp/ud\n", "", nil
		case joined == "-t /fake/codex-home/app-server-control/app-server-control.sock":
			return "7777\n", "", nil
		case strings.Contains(joined, "-p 7777") && strings.Contains(joined, "-U"):
			return "codex 7777 user 4u unix 0xabc /fake/codex-home/app-server-control/app-server-control.sock\n", "", nil
		case strings.Contains(joined, "-d"):
			return "codex 9001 user 0u PIPE 0xdef\n", "", nil
		case strings.Contains(joined, "-U") && strings.Contains(joined, "-p 1234"):
			return "ChatGPT 1234 user 6u unix ->0xabc\n", "", nil
		case strings.Contains(joined, "-U") && strings.Contains(joined, "-p 5678"):
			return "", "", nil // 隔离实例无共享连接
		case strings.Contains(joined, "daemon version"):
			return `{"status":"running"}`, "", nil
		}
		return "", "", nil
	})
	out := c.Collect(context.Background(), coreZeroIdentity())
	if len(out.Instances) != 2 {
		t.Fatalf("instances = %d, want 2", len(out.Instances))
	}
	// 5678 无 user-data-dir 日志目录可读（/tmp/isolated 未必存在）→ 日志证据 unavailable？
	// /tmp/isolated 存在与否不定：验证 private 证据至少经 (b) PIPE 形态成立。
	if out.DesktopAggregate != AggregateMixed {
		t.Fatalf("aggregate = %s, want mixed", out.DesktopAggregate)
	}
	if out.LegacyDesktopPrivate.Enum != "present" {
		t.Fatalf("legacyDesktopPrivate = %s, want present", out.LegacyDesktopPrivate.Enum)
	}
}

func coreZeroIdentity() core.CodexWebTransportIdentity {
	return core.CodexWebTransportIdentity{}
}
