package gobridge

// TopologyCollector 真机冒烟（默认跳过的环境门 CODEX_TOPOLOGY_SMOKE=1 显式开启）。
// 只读采集当前机器真实现场，输出各维度与实例判定，供 phase1-collector-regression
// 人工/脚本核对；断言仅限"运行不 panic、字段合法"，不下结论。

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestTopologyCollectorRealSmoke(t *testing.T) {
	if os.Getenv("CODEX_TOPOLOGY_SMOKE") != "1" {
		t.Skip("set CODEX_TOPOLOGY_SMOKE=1 to run the real-env smoke")
	}
	c := NewTopologyCollector()
	if c == nil {
		t.Fatal("NewTopologyCollector returned nil")
	}
	ident := core.CodexWebTransportIdentity{Main: core.CodexWebTransportRoleState{ErrorCode: "unknown"},
		Observer: core.CodexWebTransportRoleState{ErrorCode: "unknown"}}
	out := c.Collect(context.Background(), ident)
	_, _ = json.MarshalIndent(out, "", "  ")
	t.Logf("bridge=%s/%s aggregate=%s/%s instances=%d", out.BridgeAttachment, out.BridgeErrorCode,
		out.DesktopAggregate, out.DesktopErrorCode, len(out.Instances))
	t.Logf("seatDaemon=%s/%s launchAgent=%s/%s attach=%s/%s version=%s/%s loopback=%s private=%s",
		out.SeatDaemon.Enum, out.SeatDaemon.ErrorCode, out.SeatLaunchAgent.Enum, out.SeatLaunchAgent.ErrorCode,
		out.AttachConfig.Enum, out.AttachConfig.ErrorCode, out.VersionCompatibility.Enum, out.VersionCompatibility.ErrorCode,
		out.LegacyManagedLoopback.Enum, out.LegacyDesktopPrivate.Enum)
	for _, inst := range out.Instances {
		t.Logf("instance pid=%d start=%s class=%s evidence=%+v", inst.PID, inst.StartTime, inst.Classification, inst.Evidence)
	}
}
