// Package relaystate models the Relay request-aware bulk contract state machines (plan §3.6.4 / A-1.5).
//
// 这是 A-1 proof MODEL（不是 R1 runtime）：用确定性模型 + fake clock + table-driven 测试锁定
// cancel 原子状态机、registry active/retired 生命周期与 deadline 不变式，在写 R1 代码前暴露设计 bug。
// 真实 runtime 接线（双端 registry、writer admission、Relay framing）是 R1。
//
// cancel 唯一原子状态机（plan §3.6.4「cancel唯一原子状态机」）：
//   queued → reading → serializing → outboundQueued → committedToWriter → complete
// committedToWriter 是唯一 too_late 边界：Relay 为 index0 原子 commit 到 writer，Direct 为首个
// response frame 原子 commit。cancel 与 commit 在同一锁/actor 状态转移中裁决。
package relaystate

// CancelState：cancel 原子状态机的状态。
type CancelState int

const (
	StateQueued CancelState = iota
	StateReading
	StateSerializing
	StateOutboundQueued
	StateCommittedToWriter // too_late 边界：从此点起 cancel = too_late
	StateComplete
	StateCancelled // terminal（cancel 成功）
)

func (s CancelState) String() string {
	switch s {
	case StateQueued:
		return "queued"
	case StateReading:
		return "reading"
	case StateSerializing:
		return "serializing"
	case StateOutboundQueued:
		return "outboundQueued"
	case StateCommittedToWriter:
		return "committedToWriter"
	case StateComplete:
		return "complete"
	case StateCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// CancelOutcome：cancel 尝试的结果（稳定 code，对齐 plan §3.6.4 / §12.2）。
type CancelOutcome string

const (
	CancelCancelled CancelOutcome = "cancelled"
	CancelTooLate   CancelOutcome = "too_late" // 已过 committedToWriter 边界
	CancelNotFound  CancelOutcome = "not_found"
)

// 达到 committedToWriter 后 cancel 一律 too_late（index0/final frame 已原子 commit 到 writer）。
func IsTooLateBoundary(s CancelState) bool {
	return s >= StateCommittedToWriter
}

// TryCancel：在给定状态尝试 cancel。
//   - writerCommitted：StateOutboundQueued 时，writer 原子撤回是否成功（notCommitted）。
//     true = 已 commit（无法撤回）→ too_late；false = 未 commit → cancelled。
//   - StateQueued/Reading/Serializing → cancelled（cooperative cancel/stop，尚未入 writer）。
//   - StateCommittedToWriter/Complete → too_late。
//   - StateCancelled → 幂等 cancelled。
func TryCancel(state CancelState, writerCommitted bool) (CancelOutcome, CancelState) {
	switch state {
	case StateQueued, StateReading, StateSerializing:
		return CancelCancelled, StateCancelled
	case StateOutboundQueued:
		// 与 writer 原子撤回裁决：notCommitted => cancelled；已 commit => too_late
		if writerCommitted {
			return CancelTooLate, state
		}
		return CancelCancelled, StateCancelled
	case StateCommittedToWriter, StateComplete:
		return CancelTooLate, state
	case StateCancelled:
		return CancelCancelled, state // 幂等
	default:
		return CancelNotFound, state
	}
}

// Advance：状态机只允许向前转移（queued→…→complete）；非法转移返回 false。
func Advance(from, to CancelState) bool {
	// 允许的向前边
	edges := map[CancelState]CancelState{
		StateQueued:           StateReading,
		StateReading:         StateSerializing,
		StateSerializing:     StateOutboundQueued,
		StateOutboundQueued:  StateCommittedToWriter,
		StateCommittedToWriter: StateComplete,
	}
	next, ok := edges[from]
	return ok && next == to
}
