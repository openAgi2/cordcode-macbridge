package gobridge

// topology_service.go —— topology monitor 采样服务（implementation plan v2 §2.3/§2.5/§2.6）。
//
// 职责：按冻结 cadence（FD/seat 30s、实例/launchd 60s）后台运行只读采集，逐维度持有
// 原始样本（enum/source/errorCode/sampledAtMs），经 TopologyDisplayState（§2.5 防抖）
// 得出展示值，派生 syncHealth（§4.3），并以 TopologySnapshotV1 对外输出。
// - 过期（ageMs > staleAfter）的维度在组装时立即展示其不确定枚举（证据不足必须可见）。
// - 后进 60s 维度在两次 30s tick 之间持有上次样本（ageMs 单调增长，达到 staleAfter 转不确定）。
// - 未采样（首个采样前）→ 各维度不确定枚举 + syncHealth=unknown + errorCode=none
//   （无样本不算失败；sample_pending 语义由 A1 DisplaySamplePending 承载，DTO 不新增字段）。
// - 无持久化；随 bridge 生灭；Start 绑定 ctx。

import (
	"context"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TopologyProvider 是管理 API `/internal/topology/snapshot` 的只读数据源。
type TopologyProvider interface {
	// TopologySnapshot 返回当前展示快照；服务端实现内部完成失败可见（恒有效 DTO）。
	TopologySnapshot(ctx context.Context) (*TopologySnapshotV1, error)
}

// TopologyMonitorConfig 注入监控服务的全部依赖。
type TopologyMonitorConfig struct {
	Collector   TopologyCollector
	Identity    core.CodexWebTransportIdentityProvider // 可为 nil → bridge 维度 not_implemented
	BridgeEpoch func() uint64                          // 与 Management RuntimeIdentity.BridgeEpoch 同源；可为 nil → 0
	Cadence     time.Duration                          // 默认 30s（§2.5 FD/seat；实例维度每 2 tick 一次）
}

// dimSpec 冻结每个维度：不确定枚举 / cadence / staleAfter（§2.5 数值） / 默认 source。
type dimSpec struct {
	key        string
	uncertain  string
	cadence    time.Duration
	staleAfter time.Duration
	source     string
}

var topologyDimSpecs = []dimSpec{
	{DimBridgeAttachment, string(AttachmentUnresolved), 30 * time.Second, 60 * time.Second, DimSourceProviderSnap},
	{DimDesktopAggregate, string(AggregateUnknown), 60 * time.Second, 120 * time.Second, DimSourceProcessTree},
	{DimSeatHealthDaemon, "unresolved", 30 * time.Second, 60 * time.Second, DimSourceVersionProbe},
	{DimSeatHealthLaunch, "unresolved", 30 * time.Second, 60 * time.Second, DimSourceLaunchdProbe},
	{DimAttachConfig, "unresolved", 60 * time.Second, 120 * time.Second, DimSourceLaunchdProbe},
	{DimVersionCompatibility, "unknown", 60 * time.Second, 120 * time.Second, DimSourceVersionProbe},
	{DimLegacyManaged, "unresolved", 60 * time.Second, 120 * time.Second, DimSourceProcessTree},
	{DimLegacyDesktop, "unresolved", 60 * time.Second, 120 * time.Second, DimSourceProcessTree},
}

type dimSample struct {
	spec        dimSpec
	enum        string // 原始采样 enum
	errorCode   string
	sampledAtMs int64
}

// TopologyMonitorService 实现 TopologyProvider 并持有采样循环。
type TopologyMonitorService struct {
	mu    sync.Mutex
	cfg   TopologyMonitorConfig
	state *TopologyDisplayState
	dims  map[string]*dimSample

	lastInstances    []TopologyInstance
	lastFinishedAtMs int64
	tickCount        int
	identEpochSeen   bool
	lastIdentEpoch   int64
	started          bool
}

func NewTopologyMonitorService(cfg TopologyMonitorConfig) *TopologyMonitorService {
	if cfg.Cadence <= 0 {
		cfg.Cadence = 30 * time.Second
	}
	s := &TopologyMonitorService{cfg: cfg, state: NewTopologyDisplayState(), dims: map[string]*dimSample{}}
	for _, spec := range topologyDimSpecs {
		s.dims[spec.key] = &dimSample{spec: spec, enum: spec.uncertain, errorCode: ErrorNone}
	}
	return s
}

// Start 启动后台采样循环；随 ctx 取消退出（bridge shutdown）。
func (s *TopologyMonitorService) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	go func() {
		ticker := time.NewTicker(s.cfg.Cadence)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()
	// 立即执行首个采样（不等第一个 tick）。
	s.tick(ctx)
}

func (s *TopologyMonitorService) tick(ctx context.Context) {
	nowMs := time.Now().UnixMilli()

	ident := core.CodexWebTransportIdentity{
		Main:     core.CodexWebTransportRoleState{ErrorCode: "unknown"},
		Observer: core.CodexWebTransportRoleState{ErrorCode: "unknown"},
	}
	identErr := ""
	if s.cfg.Identity != nil {
		if got, err := s.cfg.Identity.TransportIdentitySnapshot(ctx); err != nil {
			identErr = ErrorRPCFailed
		} else {
			ident = got
		}
	} else {
		identErr = ErrorNotImplemented
	}

	var col *CollectedTopology
	if s.cfg.Collector != nil {
		col = s.cfg.Collector.Collect(ctx, ident)
	} else {
		col = nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if identErr == "" {
		// transport 代际变更 → 防抖历史不可复用，清空展示状态（A1 Reset 语义）。
		if s.identEpochSeen && s.lastIdentEpoch != ident.Epoch {
			s.state.Reset()
		}
		s.identEpochSeen = true
		s.lastIdentEpoch = ident.Epoch
	}
	s.tickCount++
	idx := s.tickCount - 1 // 本 tick 的 0 基序号：tick0 必须刷新所有维度（含首样本）。

	for _, key := range DimensionKeys() {
		ds := s.dims[key]
		// 实例/launchd 维度（60s cadence）在奇数 tick（30s）持有上次样本。
		if ds.spec.cadence >= 60*time.Second && idx%2 == 1 {
			continue
		}
		enum, source, errCode := s.mapDimValue(ds, col, identErr)
		ds.enum = enum
		ds.errorCode = errCode
		ds.sampledAtMs = nowMs
		ds.spec.source = source
	}
	// 防抖只作用于派生 syncHealth 徽章（§2.5 degrade N=2/recovery N=3/unknown 立即/
	// desktop_absent→not_applicable 立即）；维度 enum 在快照里原样呈现证据。
	s.state.Observe(DisplayKeySyncHealth, string(s.deriveSyncHealthLocked()))
	s.lastFinishedAtMs = nowMs
	if col != nil {
		s.lastInstances = instancesFromCollected(col.Instances)
	}
}

// DisplayKeySyncHealth 是防抖状态机中派生 syncHealth 徽章的展示 key。
const DisplayKeySyncHealth = "syncHealth"

// deriveSyncHealthLocked 从当前桥接/聚合原始维度按 §4.3 派生 syncHealth。
func (s *TopologyMonitorService) deriveSyncHealthLocked() SyncHealth {
	return DeriveSyncHealth(
		BridgeAttachment(s.dims[DimBridgeAttachment].enum),
		TopologyAggregate(s.dims[DimDesktopAggregate].enum))
}

// mapDimValue 把一次收集结果映射到单个维度（含失败可见的错误码）。
func (s *TopologyMonitorService) mapDimValue(ds *dimSample, col *CollectedTopology, identErr string) (enum, source, errCode string) {
	if ds.spec.key == DimBridgeAttachment {
		if identErr != "" {
			return string(AttachmentUnresolved), DimSourceProviderSnap, identErr
		}
		if col == nil {
			return string(AttachmentUnresolved), DimSourceProviderSnap, ErrorNotImplemented
		}
		return string(col.BridgeAttachment), DimSourceProviderSnap, col.BridgeErrorCode
	}
	if col == nil {
		return ds.spec.uncertain, ds.spec.source, ErrorNotImplemented
	}
	switch ds.spec.key {
	case DimDesktopAggregate:
		return string(col.DesktopAggregate), DimSourceProcessTree, col.DesktopErrorCode
	case DimSeatHealthDaemon:
		return col.SeatDaemon.Enum, DimSourceVersionProbe, col.SeatDaemon.ErrorCode
	case DimSeatHealthLaunch:
		return col.SeatLaunchAgent.Enum, DimSourceLaunchdProbe, col.SeatLaunchAgent.ErrorCode
	case DimAttachConfig:
		return col.AttachConfig.Enum, DimSourceLaunchdProbe, col.AttachConfig.ErrorCode
	case DimVersionCompatibility:
		return col.VersionCompatibility.Enum, DimSourceVersionProbe, col.VersionCompatibility.ErrorCode
	case DimLegacyManaged:
		return col.LegacyManagedLoopback.Enum, DimSourceProcessTree, col.LegacyManagedLoopback.ErrorCode
	case DimLegacyDesktop:
		return col.LegacyDesktopPrivate.Enum, DimSourceProcessTree, col.LegacyDesktopPrivate.ErrorCode
	}
	return ds.spec.uncertain, ds.spec.source, ErrorUnknown
}

// TopologySnapshot 组装当前展示快照（始终有效 DTO；任何组成失败回落为全不确定快照）。
func (s *TopologyMonitorService) TopologySnapshot(ctx context.Context) (*TopologySnapshotV1, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	nowMs := time.Now().UnixMilli()
	snap := s.composeSnapshotLocked(nowMs)
	if err := snap.Validate(); err != nil {
		snap = s.uncertainSnapshotLocked(nowMs)
	}
	return snap, nil
}

func (s *TopologyMonitorService) composeSnapshotLocked(nowMs int64) *TopologySnapshotV1 {
	var epoch uint64
	if s.cfg.BridgeEpoch != nil {
		epoch = s.cfg.BridgeEpoch()
	}
	snap := &TopologySnapshotV1{
		SchemaVersion: TopologySchemaVersion,
		State:         TopologyStateEnabled,
		BridgeEpoch:   int64(epoch),
		SampledAtMs:   s.lastFinishedAtMs,
		Dimensions:    map[string]TopologyDim{},
	}
	if snap.SampledAtMs == 0 {
		snap.SampledAtMs = nowMs
	}
	for _, key := range DimensionKeys() {
		ds := s.dims[key]
		ageMs := int64(0)
		if ds.sampledAtMs > 0 {
			ageMs = nowMs - ds.sampledAtMs
			if ageMs < 0 {
				ageMs = 0
			}
		}
		stale := ds.sampledAtMs > 0 && time.Duration(ageMs)*time.Millisecond > ds.spec.staleAfter
		enum := ds.enum // 维度原始 enum 即证据，不防抖不延迟
		if stale {
			// 证据不足必须立刻可见：维度转其不确定枚举。
			enum = ds.spec.uncertain
		}
		snap.Dimensions[key] = TopologyDim{
			Enum:      enum,
			AgeMs:     ageMs,
			Stale:     stale,
			Source:    ds.spec.source,
			ErrorCode: ds.errorCode,
		}
	}
	// 徽章从"有效维度"（含过期映射）派生。防抖计数只能由 tick 的完成样本推进；
	// compose 读取只在转向 unknown（过期/证据不足，冻结为立即）时喂入，绝不加速 N=2/N=3。
	effect := string(DeriveSyncHealth(
		BridgeAttachment(snap.Dimensions[DimBridgeAttachment].Enum),
		TopologyAggregate(snap.Dimensions[DimDesktopAggregate].Enum)))
	var shown string
	if effect == string(SyncUnknown) {
		s.state.Observe(DisplayKeySyncHealth, effect)
		shown = effect
	} else {
		shown = s.state.Displayed(DisplayKeySyncHealth)
		if shown == DisplaySamplePending {
			shown = string(SyncUnknown)
		}
	}
	snap.SyncHealth = shown
	if len(s.lastInstances) > 0 {
		snap.Instances = s.lastInstances
	}
	return snap
}

// uncertainSnapshotLocked 是 validate 失败时的兜底（正常路径不会走到；fail-closed 不输出坏形状）。
func (s *TopologyMonitorService) uncertainSnapshotLocked(nowMs int64) *TopologySnapshotV1 {
	snap := &TopologySnapshotV1{
		SchemaVersion: TopologySchemaVersion,
		State:         TopologyStateEnabled,
		SampledAtMs:   nowMs,
		SyncHealth:    string(SyncUnknown),
		Dimensions:    map[string]TopologyDim{},
	}
	if s.cfg.BridgeEpoch != nil {
		snap.BridgeEpoch = int64(s.cfg.BridgeEpoch())
	}
	for _, spec := range topologyDimSpecs {
		snap.Dimensions[spec.key] = TopologyDim{Enum: spec.uncertain, AgeMs: 0, Stale: false, Source: spec.source, ErrorCode: ErrorUnknown}
	}
	return snap
}

func instancesFromCollected(insts []DesktopInstance) []TopologyInstance {
	out := make([]TopologyInstance, 0, len(insts))
	for _, i := range insts {
		ev := make([]TopologyInstanceEvidence, 0, len(i.Evidence))
		for _, e := range i.Evidence {
			ev = append(ev, TopologyInstanceEvidence{Kind: string(e.Kind), State: string(e.State)})
		}
		out = append(out, TopologyInstance{PID: i.PID, StartTime: i.StartTime, Classification: string(i.Classification), Evidence: ev})
	}
	return out
}
