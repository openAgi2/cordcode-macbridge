package gobridge

// topology_aggregate.go —— 拓扑监视纯聚合状态机（implementation plan v2 §2.5/§4.2/§4.3）。
//
// 本文件只含纯函数与内存防抖状态机：不做任何系统调用/网络 I/O，可全部单测。
// - AggregateDesktop：实例分类 → §4.2 聚合真值表（split_present 不被 unresolved 抹掉）。
// - DeriveSyncHealth：bridgeAttachment × desktopAggregate → §4.3 syncHealth 全表。
// - TopologyDisplayState：逐维度展示防抖（§2.5 冻结规则），只延迟展示不改证据。

// DesktopInstanceClassification 是 §4.2 逐实例分类。
type DesktopInstanceClassification string

const (
	DesktopClassificationSharedOnly  DesktopInstanceClassification = "shared_only"
	DesktopClassificationPrivateOnly DesktopInstanceClassification = "private_only"
	DesktopClassificationDual        DesktopInstanceClassification = "dual"
	DesktopClassificationUnresolved  DesktopInstanceClassification = "unresolved"
)

// InstanceEvidenceKind 是已冻结的证据类型（§2.2 第 5 步）。
type InstanceEvidenceKind string

const (
	EvidenceKindSharedFD             InstanceEvidenceKind = "shared_fd"
	EvidenceKindPrivateStdio         InstanceEvidenceKind = "private_stdio"
	EvidenceKindForceStdioExperiment InstanceEvidenceKind = "force_stdio_experiment"
)

// InstanceEvidenceState：confirmed=正证据；absent=抽查到负证据；unavailable=采样失败。
type InstanceEvidenceState string

const (
	EvidenceStateConfirmed   InstanceEvidenceState = "confirmed"
	EvidenceStateAbsent      InstanceEvidenceState = "absent"
	EvidenceStateUnavailable InstanceEvidenceState = "unavailable"
)

// InstanceEvidence 是逐实例一条 tagged 证据（§2.4 tagged union 语义的内存形式）。
type InstanceEvidence struct {
	Kind  InstanceEvidenceKind
	State InstanceEvidenceState
}

// DesktopInstance 是 §4.2 枚举到的一个 Desktop 主实例。
type DesktopInstance struct {
	PID            int
	StartTime      string
	Classification DesktopInstanceClassification
	Evidence       []InstanceEvidence
}

// TopologyAggregate 是 §4.2 桌面聚合枚举（7 态）。
type TopologyAggregate string

const (
	AggregateDesktopAbsent TopologyAggregate = "desktop_absent"
	AggregateAllShared     TopologyAggregate = "all_shared"
	AggregateMixed         TopologyAggregate = "mixed"
	AggregateSplitPresent  TopologyAggregate = "split_present"
	AggregateUnknown       TopologyAggregate = "unknown"
)

// AggregateDesktop 实现 §4.2 真值表。未识别分类一律按 unresolved（未知实例不得默认健康）；
// split_present 仅在存在 private_only 且不存在 shared_only/dual 时成立，unresolved 不抹掉它。
func AggregateDesktop(instances []DesktopInstance) TopologyAggregate {
	if len(instances) == 0 {
		return AggregateDesktopAbsent
	}
	var sharedOnly, privateOnly, dual, unresolved bool
	for i := range instances {
		switch instances[i].Classification {
		case DesktopClassificationSharedOnly:
			sharedOnly = true
		case DesktopClassificationPrivateOnly:
			privateOnly = true
		case DesktopClassificationDual:
			dual = true
		default:
			unresolved = true
		}
	}
	switch {
	case dual:
		// 任一 dual 即 mixed（dual 同时含 shared 与 private 正证据）。
		return AggregateMixed
	case sharedOnly && privateOnly:
		return AggregateMixed
	case privateOnly:
		// 行 4：≥1 private_only 且无 shared_only/dual（unresolved 被容忍）。
		return AggregateSplitPresent
	case sharedOnly && !unresolved:
		// 行 2：全部实例均为 shared_only。
		return AggregateAllShared
	default:
		// 行 5：只有 shared_only+unresolved，或只有 unresolved。
		return AggregateUnknown
	}
}

// BridgeAttachment 是 §4.3 CordCode 附着枚举。
type BridgeAttachment string

const (
	AttachmentShared     BridgeAttachment = "shared"
	AttachmentPartial    BridgeAttachment = "partial"
	AttachmentAbsent     BridgeAttachment = "absent"
	AttachmentUnresolved BridgeAttachment = "unresolved"
)

// SyncHealth 是 §4.3 派生枚举。
type SyncHealth string

const (
	SyncHealthy       SyncHealth = "healthy"
	SyncNotApplicable SyncHealth = "not_applicable"
	SyncDegraded      SyncHealth = "degraded"
	SyncUnknown       SyncHealth = "unknown"
)

// DeriveSyncHealth 实现 §4.3 全表：shared 之外的附着一律 degraded、unresolved 一律 unknown。
func DeriveSyncHealth(attachment BridgeAttachment, aggregate TopologyAggregate) SyncHealth {
	switch attachment {
	case AttachmentShared:
		switch aggregate {
		case AggregateAllShared:
			return SyncHealthy
		case AggregateDesktopAbsent:
			return SyncNotApplicable
		case AggregateSplitPresent, AggregateMixed:
			return SyncDegraded
		default:
			return SyncUnknown
		}
	case AttachmentPartial, AttachmentAbsent:
		return SyncDegraded
	default:
		return SyncUnknown
	}
}

// ————— 展示防抖状态机（§2.5 冻结规则）—————

// 不确定展示值。
const (
	DisplaySamplePending string = "sample_pending"
	DisplayUncertain     string = "unresolved"
)

const (
	// debounceDegradeN 降级（新值为 degraded）连续一致样本数。
	debounceDegradeN = 2
	// debounceRecoveryN 恢复/其它确定态变化连续一致样本数（恢复比降级更保守）。
	debounceRecoveryN = 3
)

// dimDisplay 是单个维度的防抖状态。
type dimDisplay struct {
	displayed   string // 当前允许展示的值
	pending     string // 等待确认的目标值
	consecutive int    // 与 pending 连续的采样数
	everSeen    bool   // 是否已有完成样本（区分 sample_pending）
}

// TopologyDisplayState 是展示防抖状态机（纯内存、无 I/O；调用方负责并发边界）。
//
// 冻结规则（§2.5，作用于展示值——service 用它防抖派生 syncHealth 徽章；
// 维度原始 enum 在快照里原样呈现证据，不延迟）：
//   - 完成首个采样前：sample_pending；首个完成样本立即展示（无可数历史）。
//   - 值是不确定态（unknown/unresolved）、桌面聚合 desktop_absent 或派生
//     not_applicable（只能由 desktop_absent 这个实例枚举正结果派生）→ 立即展示。
//   - 当前展示为不确定态：单一完成样本即正证据，立即展示（防抖只用于确定→确定变化）。
//   - 其余确定→确定变化：新值 = degraded → N=2 连续同样；否则 N=3（恢复/其它）。
//   - 过期样本由 service 先映射为不确定枚举再喂入（证据不足必须立刻可见，
//     防抖≠隐瞒）；状态机不感知时钟（ageMs/staleAfter 是 service 层职责）。
//   - Reset：清空（bridge epoch 变更时调用）。
type TopologyDisplayState struct {
	dims map[string]*dimDisplay
}

// NewTopologyDisplayState 构造空状态；首个完成样本前所有维度为 sample_pending。
func NewTopologyDisplayState() *TopologyDisplayState {
	return &TopologyDisplayState{dims: map[string]*dimDisplay{}}
}

// Observe 喂入维度 key 的一次完成采样（value 为维度枚举或该维度的不确定枚举
// unresolved/unknown；样本过期时由调用方先映射为不确定枚举——staleAfter/ageMs 是
// service 层职责，状态机不感知时钟），返回该维度当前展示值。
func (s *TopologyDisplayState) Observe(key, value string) string {
	d := s.dims[key]
	if d == nil {
		d = &dimDisplay{displayed: DisplaySamplePending}
		s.dims[key] = d
	}
	if !d.everSeen {
		d.everSeen = true
		d.displayed = value
		d.pending = ""
		d.consecutive = 0
		return d.displayed
	}
	if isUncertainValue(value) {
		// 证据不足/过期立即覆盖展示（不防抖）。
		d.displayed = value
		d.pending = ""
		d.consecutive = 0
		return d.displayed
	}
	if isUncertainValue(d.displayed) {
		// 从不确定态恢复：一个完成样本即正证据，立即展示。
		d.displayed = value
		d.pending = ""
		d.consecutive = 0
		return d.displayed
	}
	if value == string(AggregateDesktopAbsent) || value == string(SyncNotApplicable) {
		// desktop_absent 是实例枚举的确定结果（正负态证据）；not_applicable 只能由它派生，
		// 两者均立即展示（§2.5 冻结行）。
		d.displayed = value
		d.pending = ""
		d.consecutive = 0
		return d.displayed
	}
	if d.pending == "" {
		if value == d.displayed {
			return d.displayed
		}
		d.pending = value
		d.consecutive = 1
	} else if value == d.pending {
		d.consecutive++
	} else {
		// 目标漂移：重置为新的候选，重新计数。
		d.pending = value
		d.consecutive = 1
	}
	n := debounceRecoveryN
	if value == string(SyncDegraded) {
		n = debounceDegradeN
	}
	if d.consecutive >= n {
		d.displayed = d.pending
		d.pending = ""
		d.consecutive = 0
	}
	return d.displayed
}

// Reset 清空全部维度状态（bridge epoch 变更时调用）。
func (s *TopologyDisplayState) Reset() {
	s.dims = map[string]*dimDisplay{}
}

// Displayed 返回 key 当前展示值（只读查询，不喂样本）。
func (s *TopologyDisplayState) Displayed(key string) string {
	if d := s.dims[key]; d != nil {
		return d.displayed
	}
	return DisplaySamplePending
}

func isUncertainValue(v string) bool {
	return v == DisplayUncertain || v == DisplaySamplePending || v == string(SyncUnknown)
}
