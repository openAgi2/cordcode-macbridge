package gobridge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ————— 服务测试脚手架 —————

type fakeTopologyCollector struct {
	results []*CollectedTopology
	calls   int
}

func (f *fakeTopologyCollector) Collect(_ context.Context, _ core.CodexWebTransportIdentity) *CollectedTopology {
	i := f.calls
	f.calls++
	if i >= len(f.results) {
		i = len(f.results) - 1
	}
	return f.results[i]
}

type fakeIdentityProvider struct {
	snap core.CodexWebTransportIdentity
	err  error
}

func (f *fakeIdentityProvider) TransportIdentitySnapshot(context.Context) (core.CodexWebTransportIdentity, error) {
	return f.snap, f.err
}

func healthyCollected() *CollectedTopology {
	return &CollectedTopology{
		BridgeAttachment: AttachmentShared,
		BridgeErrorCode:  ErrorNone,
		DesktopAggregate: AggregateAllShared,
		DesktopErrorCode: ErrorNone,
		Instances: []DesktopInstance{{PID: 1234, StartTime: "2026-08-24T10:00:00Z",
			Classification: DesktopClassificationSharedOnly,
			Evidence:       []InstanceEvidence{{Kind: EvidenceKindSharedFD, State: EvidenceStateConfirmed}}}},
		SeatDaemon:            DimValue{Enum: "running", Source: DimSourceVersionProbe, ErrorCode: ErrorNone},
		SeatLaunchAgent:       DimValue{Enum: "healthy", Source: DimSourceLaunchdProbe, ErrorCode: ErrorNone},
		AttachConfig:          DimValue{Enum: "enabled", Source: DimSourceLaunchdProbe, ErrorCode: ErrorNone},
		VersionCompatibility:  DimValue{Enum: "effective_compatible", Source: DimSourceVersionProbe, ErrorCode: ErrorNone},
		LegacyManagedLoopback: DimValue{Enum: "absent", Source: DimSourceProcessTree, ErrorCode: ErrorNone},
		LegacyDesktopPrivate:  DimValue{Enum: "absent", Source: DimSourceProcessTree, ErrorCode: ErrorNone},
	}
}

func partialCollected() *CollectedTopology {
	c := healthyCollected()
	c.BridgeAttachment = AttachmentPartial
	return c
}

func newTestMonitor(coll *fakeTopologyCollector, ident *fakeIdentityProvider, epoch uint64) *TopologyMonitorService {
	return NewTopologyMonitorService(TopologyMonitorConfig{
		Collector:   coll,
		Identity:    ident,
		BridgeEpoch: func() uint64 { return epoch },
		Cadence:     30 * time.Second,
	})
}

// ————— 测试 —————

func TestServicePreFirstSampleUncertain(t *testing.T) {
	svc := newTestMonitor(&fakeTopologyCollector{results: []*CollectedTopology{healthyCollected()}},
		&fakeIdentityProvider{snap: mainObservedIdentity()}, 7)
	snap, err := svc.TopologySnapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := snap.Validate(); err != nil {
		t.Fatalf("pre-first-sample snapshot invalid: %v", err)
	}
	if snap.SyncHealth != string(SyncUnknown) {
		t.Fatalf("pre-first syncHealth = %q, want unknown", snap.SyncHealth)
	}
	if snap.BridgeEpoch != 7 {
		t.Fatalf("bridgeEpoch = %d, want 7", snap.BridgeEpoch)
	}
	for _, k := range DimensionKeys() {
		d := snap.Dimensions[k]
		if d.ErrorCode != ErrorNone {
			t.Errorf("dim %s pre-sample errorCode = %q, want none", k, d.ErrorCode)
		}
	}
	if len(snap.Instances) != 0 {
		t.Fatalf("pre-first instances must be empty: %+v", snap.Instances)
	}
}

func TestServiceFirstTickHealthy(t *testing.T) {
	svc := newTestMonitor(&fakeTopologyCollector{results: []*CollectedTopology{healthyCollected()}},
		&fakeIdentityProvider{snap: mainObservedIdentity()}, 42)
	svc.tick(context.Background())
	snap, _ := svc.TopologySnapshot(context.Background())
	if err := snap.Validate(); err != nil {
		t.Fatalf("snapshot invalid: %v", err)
	}
	if snap.SyncHealth != string(SyncHealthy) {
		t.Fatalf("syncHealth = %q, want healthy", snap.SyncHealth)
	}
	if snap.Dimensions[DimBridgeAttachment].Enum != string(AttachmentShared) ||
		snap.Dimensions[DimDesktopAggregate].Enum != string(AggregateAllShared) {
		t.Fatalf("dims = %+v/%+v", snap.Dimensions[DimBridgeAttachment], snap.Dimensions[DimDesktopAggregate])
	}
	if len(snap.Instances) != 1 || snap.Instances[0].Classification != string(DesktopClassificationSharedOnly) {
		t.Fatalf("instances = %+v", snap.Instances)
	}
}

func TestServiceBadgeDegradeNeedsTwoTicks(t *testing.T) {
	// shared→partial × all_shared → 派生 degraded；徽章 degrade N=2。
	svc := newTestMonitor(&fakeTopologyCollector{results: []*CollectedTopology{healthyCollected(), partialCollected()}},
		&fakeIdentityProvider{snap: mainObservedIdentity()}, 1)
	svc.tick(context.Background())
	svc.tick(context.Background())
	snap, _ := svc.TopologySnapshot(context.Background())
	if snap.SyncHealth != string(SyncHealthy) {
		t.Fatalf("after 1 degraded sample syncHealth = %q, want healthy (N=2 pending)", snap.SyncHealth)
	}
	// 维度证据原样呈现（不防抖）：bridge dim 已是 partial。
	if snap.Dimensions[DimBridgeAttachment].Enum != string(AttachmentPartial) {
		t.Fatalf("bridge dim = %q, want partial (raw evidence)", snap.Dimensions[DimBridgeAttachment].Enum)
	}
	svc.tick(context.Background())
	snap, _ = svc.TopologySnapshot(context.Background())
	if snap.SyncHealth != string(SyncDegraded) {
		t.Fatalf("after 2 consecutive degraded samples syncHealth = %q, want degraded", snap.SyncHealth)
	}
}

func TestServiceBadgeRecoveryNeedsThreeTicks(t *testing.T) {
	// 先进入 degraded（partial×2 连续），再恢复 shared —— recovery N=3。
	svc := newTestMonitor(&fakeTopologyCollector{results: []*CollectedTopology{healthyCollected(), partialCollected(), partialCollected(), healthyCollected()}},
		&fakeIdentityProvider{snap: mainObservedIdentity()}, 1)
	for i := 0; i < 3; i++ {
		svc.tick(context.Background())
	}
	if snap, _ := svc.TopologySnapshot(context.Background()); snap.SyncHealth != string(SyncDegraded) {
		t.Fatalf("degrade failed: %q", snap.SyncHealth)
	}
	svc.tick(context.Background())
	svc.tick(context.Background())
	if snap, _ := svc.TopologySnapshot(context.Background()); snap.SyncHealth != string(SyncDegraded) {
		t.Fatalf("recovery sample 2 must not flip: %q", snap.SyncHealth)
	}
	svc.tick(context.Background())
	snap, _ := svc.TopologySnapshot(context.Background())
	if snap.SyncHealth != string(SyncHealthy) {
		t.Fatalf("recovery sample 3 must flip to healthy: %q", snap.SyncHealth)
	}
}

func TestServiceDesktopAbsentImmediateNotApplicable(t *testing.T) {
	absent := healthyCollected()
	absent.DesktopAggregate = AggregateDesktopAbsent
	absent.Instances = nil
	svc := newTestMonitor(&fakeTopologyCollector{results: []*CollectedTopology{healthyCollected(), absent}},
		&fakeIdentityProvider{snap: mainObservedIdentity()}, 1)
	svc.tick(context.Background())
	before := svc.dims[DimDesktopAggregate].sampledAtMs
	svc.tick(context.Background())
	// tick2（idx1，奇数）持有 60s 维度：sampledAtMs 不变。
	if svc.dims[DimDesktopAggregate].sampledAtMs != before {
		t.Fatalf("60s dim must hold on odd tick: sampledAt %d -> %d", before, svc.dims[DimDesktopAggregate].sampledAtMs)
	}
	snap, _ := svc.TopologySnapshot(context.Background())
	if snap.Dimensions[DimDesktopAggregate].Enum != string(AggregateAllShared) {
		t.Fatalf("aggregate dim = %q, want all_shared (held)", snap.Dimensions[DimDesktopAggregate].Enum)
	}
	svc.tick(context.Background())
	snap, _ = svc.TopologySnapshot(context.Background())
	if snap.Dimensions[DimDesktopAggregate].Enum != string(AggregateDesktopAbsent) {
		t.Fatalf("aggregate dim = %q, want desktop_absent (immediate)", snap.Dimensions[DimDesktopAggregate].Enum)
	}
	// desktop_absent → 派生 not_applicable 立即展示（不防抖）。
	if snap.SyncHealth != string(SyncNotApplicable) {
		t.Fatalf("badge = %q, want not_applicable (immediate)", snap.SyncHealth)
	}
}

func TestServiceStaleDimShowsUncertainImmediately(t *testing.T) {
	svc := newTestMonitor(&fakeTopologyCollector{results: []*CollectedTopology{healthyCollected()}},
		&fakeIdentityProvider{snap: mainObservedIdentity()}, 1)
	svc.tick(context.Background())
	// 把 bridge 维度样本拨回 61s 前（bridge staleAfter=60s）。
	svc.dims[DimBridgeAttachment].sampledAtMs = time.Now().UnixMilli() - 61_000
	snap, _ := svc.TopologySnapshot(context.Background())
	d := snap.Dimensions[DimBridgeAttachment]
	if !d.Stale || d.Enum != string(AttachmentUnresolved) {
		t.Fatalf("stale bridge dim = %+v, want stale+unresolved", d)
	}
	if snap.SyncHealth != string(SyncUnknown) {
		t.Fatalf("badge with stale dim = %q, want unknown (immediate)", snap.SyncHealth)
	}
}

func TestServiceIdentityErrorUnresolved(t *testing.T) {
	svc := newTestMonitor(&fakeTopologyCollector{results: []*CollectedTopology{healthyCollected()}},
		&fakeIdentityProvider{err: errors.New("boom")}, 1)
	svc.tick(context.Background())
	snap, _ := svc.TopologySnapshot(context.Background())
	if snap.Dimensions[DimBridgeAttachment].Enum != string(AttachmentUnresolved) ||
		snap.Dimensions[DimBridgeAttachment].ErrorCode != ErrorRPCFailed {
		t.Fatalf("identity error dim = %+v, want unresolved/rpc_failed", snap.Dimensions[DimBridgeAttachment])
	}
	if snap.SyncHealth != string(SyncUnknown) {
		t.Fatalf("badge = %q, want unknown", snap.SyncHealth)
	}
}

func TestServiceEpochChangeResetsBadgeHistory(t *testing.T) {
	ident := &fakeIdentityProvider{snap: mainObservedIdentity()}
	svc := newTestMonitor(&fakeTopologyCollector{results: []*CollectedTopology{healthyCollected(), partialCollected()}},
		ident, 1)
	svc.tick(context.Background())
	// 代际变化：epoch 5→6；防抖历史必须清空（否则 partial 要 N=2 才展示）。
	ident.snap = core.CodexWebTransportIdentity{
		Epoch:    6,
		Endpoint: "/x/app-server-control.sock",
		Main:     core.CodexWebTransportRoleState{Attached: true, ErrorCode: "none"},
		Observer: core.CodexWebTransportRoleState{Attached: true, ErrorCode: "none"},
	}
	svc.tick(context.Background())
	snap, _ := svc.TopologySnapshot(context.Background())
	if snap.BridgeEpoch != 1 {
		t.Fatalf("snapshot bridgeEpoch = %d (runtime epoch, 非 provider epoch)", snap.BridgeEpoch)
	}
	if snap.SyncHealth != string(SyncDegraded) {
		t.Fatalf("after epoch change badge must show derived state immediately: %q", snap.SyncHealth)
	}
}

func mainObservedIdentity() core.CodexWebTransportIdentity {
	return core.CodexWebTransportIdentity{
		Epoch:    5,
		Endpoint: "/x/app-server-control.sock",
		Main:     core.CodexWebTransportRoleState{Attached: true, Epoch: 5, ErrorCode: "none"},
		Observer: core.CodexWebTransportRoleState{Attached: true, Epoch: 5, ErrorCode: "none"},
	}
}
