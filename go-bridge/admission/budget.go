package admission

import (
	"errors"
	"fmt"
)

// ManagementTimeBudget 是 plan §3.6.3 / A0.5 冻结的 Management 时间预算字段集合。
// 所有加法用 checked arithmetic；任一 overflow 直接使配置 gate 失败，且必须同时满足三组不等式。
//
//	minimumCommitRemainingMillis >= commitHTTPTimeout + executorSchedulingMargin
//	leaseMin                     >= quiesceHTTPTimeout + safeDecodeBudget + minimumCommitRemainingMillis
//	leaseMax                     >= leaseMin
type ManagementTimeBudget struct {
	QuiesceHTTPTimeout           uint32 // Mac 等待 /internal/runtime/quiesce 响应的最坏超时
	SafeDecodeBudget             uint32 // safe 响应本地最坏调度/解码预算
	CommitHTTPTimeout            uint32 // Mac 等待 commit 响应的最坏超时
	AbortHTTPTimeout             uint32 // Mac 等待 abort 响应的最坏超时
	ExecutorSchedulingMargin     uint32 // Mac executor 调度裕量
	MinimumCommitRemainingMillis uint32 // 收到 safe 后剩余不足此值则不得 commit
	LeaseMin                     uint32 // lease 下限
	LeaseMax                     uint32 // lease 上限
}

// DefaultManagementTimeBudget is the single production budget shared by the Go lease
// owner and the Mac supervisor contract. The 30s lease is intentionally fixed (min=max);
// HTTP timeout and executor margin match ManagementAPIClient/RuntimeManager.
func DefaultManagementTimeBudget() ManagementTimeBudget {
	return ManagementTimeBudget{
		QuiesceHTTPTimeout:           2_000,
		SafeDecodeBudget:             500,
		CommitHTTPTimeout:            2_000,
		AbortHTTPTimeout:             2_000,
		ExecutorSchedulingMargin:     500,
		MinimumCommitRemainingMillis: 2_500,
		LeaseMin:                     30_000,
		LeaseMax:                     30_000,
	}
}

// ErrBudgetOverflow 表示某次 checked 加法溢出（uint32 域）。
var ErrBudgetOverflow = errors.New("management time budget overflow")

// addChecked 是 uint32 checked 加法；溢出返回 (0, ErrBudgetOverflow)。
func addChecked(a, b uint32) (uint32, error) {
	s := uint64(a) + uint64(b)
	if s > uint64(^uint32(0)) {
		return 0, ErrBudgetOverflow
	}
	return uint32(s), nil
}

// Validate 校验三组时间不等式（均 checked arithmetic）。任一不满足或溢出即返回错误。
// 数值本身（单位/合理性）由 A0.5 冻结；本函数只保证“配置合法时 lease/commit 不会卡在边界”。
func (b ManagementTimeBudget) Validate() error {
	// 不等式 1: minimumCommitRemainingMillis >= commitHTTPTimeout + executorSchedulingMargin
	rhs1, err := addChecked(b.CommitHTTPTimeout, b.ExecutorSchedulingMargin)
	if err != nil {
		return fmt.Errorf("inequality 1: %w", err)
	}
	if b.MinimumCommitRemainingMillis < rhs1 {
		return fmt.Errorf("inequality 1 violated: minimumCommitRemainingMillis(%d) < commitHTTPTimeout(%d)+executorSchedulingMargin(%d)=%d",
			b.MinimumCommitRemainingMillis, b.CommitHTTPTimeout, b.ExecutorSchedulingMargin, rhs1)
	}

	// 不等式 2: leaseMin >= quiesceHTTPTimeout + safeDecodeBudget + minimumCommitRemainingMillis
	tmp, err := addChecked(b.QuiesceHTTPTimeout, b.SafeDecodeBudget)
	if err != nil {
		return fmt.Errorf("inequality 2: %w", err)
	}
	rhs2, err := addChecked(tmp, b.MinimumCommitRemainingMillis)
	if err != nil {
		return fmt.Errorf("inequality 2: %w", err)
	}
	if b.LeaseMin < rhs2 {
		return fmt.Errorf("inequality 2 violated: leaseMin(%d) < quiesceHTTPTimeout(%d)+safeDecodeBudget(%d)+minimumCommitRemainingMillis(%d)=%d",
			b.LeaseMin, b.QuiesceHTTPTimeout, b.SafeDecodeBudget, b.MinimumCommitRemainingMillis, rhs2)
	}

	// 不等式 3: leaseMax >= leaseMin
	if b.LeaseMax < b.LeaseMin {
		return fmt.Errorf("inequality 3 violated: leaseMax(%d) < leaseMin(%d)", b.LeaseMax, b.LeaseMin)
	}

	// 语义约束：commit 与 abort 共享同一 minimumCommitRemainingMillis 判定；abort 也要求
	// 剩余量足够 abortHTTPTimeout + executorSchedulingMargin。把它作为额外一致性检查暴露。
	abortNeed, err := addChecked(b.AbortHTTPTimeout, b.ExecutorSchedulingMargin)
	if err != nil {
		return fmt.Errorf("abort consistency: %w", err)
	}
	if b.MinimumCommitRemainingMillis < abortNeed {
		// 这不是 §3.6.3 列出的三主不等式之一，但 abort 与 commit 共享 minimumCommitRemainingMillis
		// 时若小于 abort 预算则 abort 路径无裕量。作为 warning 级一致性失败返回。
		return fmt.Errorf("abort consistency violated: minimumCommitRemainingMillis(%d) < abortHTTPTimeout(%d)+executorSchedulingMargin(%d)=%d",
			b.MinimumCommitRemainingMillis, b.AbortHTTPTimeout, b.ExecutorSchedulingMargin, abortNeed)
	}
	return nil
}
