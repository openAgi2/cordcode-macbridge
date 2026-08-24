package gobridge

// topology_dto.go —— 拓扑监视管理 API DTO（implementation plan v2 §2.3/§2.4）。
//
// HTTP DTO 只留 go-bridge，不进 core（core 仅 optional provider 接口）。
// 冻结契约：
//   - schemaVersion 恒 "topology-monitor.v1"；state 恒 enabled|disabled。
//   - disabled 仅 {schemaVersion, state, bridgeEpoch, sampledAtMs}（无 syncHealth/dimensions/instances）。
//   - enabled 时 dimensions 恒为全部 8 个固定键，各键 enum 白名单见 topologyDimEnums。
//   - 未知 enum/非法 state/schemaVersion 不匹配 → 解码失败（fail-closed，绝不默认 healthy）。
//   - 未知 JSON 字段忽略（Go 侧与 Swift mirror 一致）。
//   - source 取值修正 §2.3 笔误：provider_snapshot（非 provider_shapshot）。

import (
	"encoding/json"
	"fmt"
	"time"
)

// TopologySchemaVersion 是冻结的 schemaVersion。
const TopologySchemaVersion = "topology-monitor.v1"

// TopologyState 是冻结的 state 枚举。
const (
	TopologyStateEnabled  = "enabled"
	TopologyStateDisabled = "disabled"
)

// 维度固定键（§2.4 全量 8 个）。
const (
	DimBridgeAttachment     = "topologyBridgeAttachment"
	DimDesktopAggregate     = "topologyDesktopAggregate"
	DimSeatHealthDaemon     = "seatHealthDaemon"
	DimSeatHealthLaunch     = "seatHealthLaunchAgent"
	DimAttachConfig         = "attachConfig"
	DimVersionCompatibility = "versionCompatibility"
	DimLegacyManaged        = "legacyManagedLoopback"
	DimLegacyDesktop        = "legacyDesktopPrivate"
)

// 维度数据源（§2.3；provider_snapshot 正拼）。
const (
	DimSourceLsofFDPeer   = "lsof_fd_peer"
	DimSourceLaunchdProbe = "launchd_probe"
	DimSourceVersionProbe = "version_probe"
	DimSourceProcessTree  = "process_tree"
	DimSourceProviderSnap = "provider_snapshot"
)

// 维度/实例错误码（§2.3 全表）。
const (
	ErrorNone           = "none"
	ErrorTimeout        = "timeout"
	ErrorPermission     = "permission"
	ErrorRPCFailed      = "rpc_failed"
	ErrorParseFailed    = "parse_failed"
	ErrorProcessMissing = "process_missing"
	ErrorNotImplemented = "not_implemented"
	ErrorUnknown        = "unknown"
)

// TopologyDim 是单个维度的观测值（ageMs 由 service 按完成时刻计算；客户端不比较本地时钟）。
type TopologyDim struct {
	Enum      string `json:"enum"`
	AgeMs     int64  `json:"ageMs"`
	Stale     bool   `json:"stale"`
	Source    string `json:"source"`
	ErrorCode string `json:"errorCode"`
}

// TopologyInstanceEvidence 是逐实例 tagged union 证据（unavailable=采样失败，绝不作 negatives）。
type TopologyInstanceEvidence struct {
	Kind  string `json:"kind"`
	State string `json:"state"`
}

// TopologyInstance 是 Desktop 主实例（PID+startTime 组合为一次性身份，防 PID 重用）。
type TopologyInstance struct {
	PID            int                        `json:"pid"`
	StartTime      string                     `json:"startTime"`
	Classification string                     `json:"classification"`
	Evidence       []TopologyInstanceEvidence `json:"evidence"`
}

// TopologySnapshotV1 是 GET /internal/topology/snapshot 的响应体（始终 200，state 区分启用）。
type TopologySnapshotV1 struct {
	SchemaVersion string                 `json:"schemaVersion"`
	State         string                 `json:"state"`
	BridgeEpoch   int64                  `json:"bridgeEpoch"`
	SampledAtMs   int64                  `json:"sampledAtMs"`
	SyncHealth    string                 `json:"syncHealth,omitempty"` // disabled 省略
	Dimensions    map[string]TopologyDim `json:"dimensions,omitempty"` // disabled 省略；enabled 恒 8 键
	Instances     []TopologyInstance     `json:"instances,omitempty"`  // 禁用省略
}

// 每维度 enum 白名单（§4.4 修订列；证据不足态为各维度自身枚举：多数为
// unresolved，topologyDesktopAggregate/versionCompatibility 为 unknown）。
var topologyDimEnums = map[string]map[string]bool{
	DimBridgeAttachment: {string(AttachmentShared): true, string(AttachmentPartial): true,
		string(AttachmentAbsent): true, string(AttachmentUnresolved): true},
	DimDesktopAggregate: {string(AggregateDesktopAbsent): true, string(AggregateAllShared): true,
		string(AggregateMixed): true, string(AggregateSplitPresent): true, string(AggregateUnknown): true},
	DimSeatHealthDaemon: {"running": true, "stopped": true, "unresolved": true},
	DimSeatHealthLaunch: {"healthy": true, "missing": true, "failed": true, "unresolved": true},
	DimAttachConfig:     {"enabled": true, "disabled": true, "unresolved": true},
	DimVersionCompatibility: {"effective_compatible": true, "probe_compatible": true,
		"probe_incompatible": true, "unknown": true},
	DimLegacyManaged: {"present": true, "absent": true, "unresolved": true},
	DimLegacyDesktop: {"present": true, "absent": true, "unresolved": true},
}

var topologyDimSources = map[string]bool{
	DimSourceLsofFDPeer: true, DimSourceLaunchdProbe: true, DimSourceVersionProbe: true,
	DimSourceProcessTree: true, DimSourceProviderSnap: true,
}

var topologyErrorCodes = map[string]bool{
	ErrorNone: true, ErrorTimeout: true, ErrorPermission: true, ErrorRPCFailed: true,
	ErrorParseFailed: true, ErrorProcessMissing: true, ErrorNotImplemented: true, ErrorUnknown: true,
}

var topologyInstanceClassifications = map[string]bool{
	string(DesktopClassificationSharedOnly): true, string(DesktopClassificationPrivateOnly): true,
	string(DesktopClassificationDual): true, string(DesktopClassificationUnresolved): true,
}

var topologyEvidenceKinds = map[string]bool{
	string(EvidenceKindSharedFD): true, string(EvidenceKindPrivateStdio): true,
	string(EvidenceKindForceStdioExperiment): true,
}

var topologyEvidenceStates = map[string]bool{
	string(EvidenceStateConfirmed): true, string(EvidenceStateAbsent): true,
	string(EvidenceStateUnavailable): true,
}

var topologySyncHealthValues = map[string]bool{
	string(SyncHealthy): true, string(SyncNotApplicable): true,
	string(SyncDegraded): true, string(SyncUnknown): true,
}

// DimensionKeys 返回 8 个固定维度键（用于断言/遍历）。
func DimensionKeys() []string {
	return []string{DimBridgeAttachment, DimDesktopAggregate, DimSeatHealthDaemon,
		DimSeatHealthLaunch, DimAttachConfig, DimVersionCompatibility,
		DimLegacyManaged, DimLegacyDesktop}
}

// Validate 校验快照符合冻结契约（构造侧与解码侧共用；fail-closed）。
func (s *TopologySnapshotV1) Validate() error {
	if s.SchemaVersion != TopologySchemaVersion {
		return fmt.Errorf("topology snapshot: schemaVersion = %q, want %q", s.SchemaVersion, TopologySchemaVersion)
	}
	switch s.State {
	case TopologyStateDisabled:
		if s.SyncHealth != "" || len(s.Dimensions) != 0 || len(s.Instances) != 0 {
			return fmt.Errorf("topology snapshot: disabled state must not carry syncHealth/dimensions/instances")
		}
		// bridgeEpoch/sampledAtMs 允许 0：0 = 未初始化（无 runtime identity），如实呈现不造假。
		return nil
	case TopologyStateEnabled:
		// 落空即报错（不默认 healthy）。
	default:
		return fmt.Errorf("topology snapshot: unknown state %q", s.State)
	}
	if !topologySyncHealthValues[s.SyncHealth] {
		return fmt.Errorf("topology snapshot: invalid syncHealth %q", s.SyncHealth)
	}
	for _, k := range DimensionKeys() {
		d, ok := s.Dimensions[k]
		if !ok {
			return fmt.Errorf("topology snapshot: missing dimension %q", k)
		}
		if !topologyDimEnums[k][d.Enum] {
			return fmt.Errorf("topology snapshot: invalid enum %q for dimension %q", d.Enum, k)
		}
		if !topologyDimSources[d.Source] {
			return fmt.Errorf("topology snapshot: invalid source %q for dimension %q", d.Source, k)
		}
		if !topologyErrorCodes[d.ErrorCode] {
			return fmt.Errorf("topology snapshot: invalid errorCode %q for dimension %q", d.ErrorCode, k)
		}
		if d.AgeMs < 0 {
			return fmt.Errorf("topology snapshot: negative ageMs for dimension %q", k)
		}
	}
	if len(s.Dimensions) != len(DimensionKeys()) {
		return fmt.Errorf("topology snapshot: %d dimensions present, want %d", len(s.Dimensions), len(DimensionKeys()))
	}
	for _, inst := range s.Instances {
		if inst.PID <= 0 {
			return fmt.Errorf("topology snapshot: instance with non-positive pid %d", inst.PID)
		}
		if !topologyInstanceClassifications[inst.Classification] {
			return fmt.Errorf("topology snapshot: invalid classification %q", inst.Classification)
		}
		if _, err := time.Parse(time.RFC3339, inst.StartTime); err != nil {
			return fmt.Errorf("topology snapshot: invalid instance startTime %q: %v", inst.StartTime, err)
		}
		for _, e := range inst.Evidence {
			if !topologyEvidenceKinds[e.Kind] {
				return fmt.Errorf("topology snapshot: invalid evidence kind %q", e.Kind)
			}
			if !topologyEvidenceStates[e.State] {
				return fmt.Errorf("topology snapshot: invalid evidence state %q", e.State)
			}
		}
	}
	return nil
}

// DecodeTopologySnapshot 严格解码：未知 enum/非法字段 → error（fail-closed）；未知字段忽略。
func DecodeTopologySnapshot(data []byte) (*TopologySnapshotV1, error) {
	var s TopologySnapshotV1
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("topology snapshot: decode: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Marshal 校验后输出两个空格缩进的 JSON（服务端发射路径与客户端 decode 共用同一结构）。
func (s *TopologySnapshotV1) Marshal() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(s, "", "  ")
}
