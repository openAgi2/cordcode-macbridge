package admission

import (
	"errors"
	"fmt"
)

// FileReadHealthConfig 是 plan §3.6.3 / A0.5 / A-1.6 的 file-read worker pool 资源上限与退化阈值。
// 三个正交状态机之一（FileReadHealth，与 RuntimeAdmission、Mac SupervisorState 正交）。
type FileReadHealthConfig struct {
	PoolSize            uint32 // 全局专用 bounded file-read worker pool 大小
	MinHealthyFileSlots uint32 // 损失多少 slot 之前仍算 healthy 的下限
	DegradeAt           uint32 // 不可用 slot 达到此值即进入 degrading（必须在全部 slot 损失前报警）
	StuckAgeMillis      uint32 // 单个 worker 卡住多久判定为 stuck（原子进入 degrading）
}

// ErrResourceInvariant 表示资源上限不变式被违反（配置 gate 失败）。
var ErrResourceInvariant = errors.New("file-read resource invariant violated")

// Validate 校验：degradeAt <= poolSize - minHealthyFileSlots（必须在全部 file slot 损失前报警）。
// 同时要求各字段语义非负、poolSize >= minHealthyFileSlots。
func (c FileReadHealthConfig) Validate() error {
	if c.PoolSize == 0 {
		return fmt.Errorf("%w: poolSize must be > 0", ErrResourceInvariant)
	}
	if c.MinHealthyFileSlots > c.PoolSize {
		return fmt.Errorf("%w: minHealthyFileSlots(%d) > poolSize(%d)", ErrResourceInvariant, c.MinHealthyFileSlots, c.PoolSize)
	}
	// healthySlots = poolSize - minHealthyFileSlots（checked）
	healthySlots, err := subChecked(c.PoolSize, c.MinHealthyFileSlots)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrResourceInvariant, err)
	}
	// degradeAt <= healthySlots：损失到 minHealthyFileSlots 之前必须已经报警。
	if c.DegradeAt > healthySlots {
		return fmt.Errorf("%w: degradeAt(%d) > poolSize(%d)-minHealthyFileSlots(%d)=%d (必须在全部 slot 损失前报警)",
			ErrResourceInvariant, c.DegradeAt, c.PoolSize, c.MinHealthyFileSlots, healthySlots)
	}
	if c.StuckAgeMillis == 0 {
		return fmt.Errorf("%w: stuckAge must be > 0", ErrResourceInvariant)
	}
	return nil
}

func subChecked(a, b uint32) (uint32, error) {
	if b > a {
		return 0, ErrBudgetOverflow
	}
	return a - b, nil
}

// FileReadHealthMachine 是 FileReadHealth 三态机（healthy → degrading → degraded），与
// AdmissionMachine 正交（plan §3.6.3：degraded+accepting 是正常组合）。stateEpoch 在每次状态转换时 +1。
type FileReadHealthMachine struct {
	stateEpoch uint64
	state      HealthState
	stuck      uint32 // 当前不可用/stuck worker 数
}

// NewFileReadHealthMachine 构造 healthy 态，stateEpoch 从 1 起。
func NewFileReadHealthMachine() *FileReadHealthMachine {
	return &FileReadHealthMachine{stateEpoch: 1, state: HealthHealthy}
}

// StateEpoch 返回当前 health stateEpoch（healthEpoch 即其同值回显）。
func (h *FileReadHealthMachine) StateEpoch() uint64 { return h.stateEpoch }

// Health 返回当前 health state。
func (h *FileReadHealthMachine) Health() HealthState { return h.state }

// MarkDegrading 在达到 stuck 阈值时原子进入 degrading 并递增 stateEpoch（plan §3.6.3）。
// 已 degraded 则幂等（不再递增）。
func (h *FileReadHealthMachine) MarkDegrading() {
	if h.state == HealthDegraded {
		return
	}
	if h.state != HealthDegrading {
		h.state = HealthDegrading
		h.stateEpoch++ // 每次转换恰增 1
	}
}

// MarkDegraded 在“拒绝已生效且队列已终态”后进入 degraded 并递增 stateEpoch；不可逆。
func (h *FileReadHealthMachine) MarkDegraded() {
	if h.state == HealthDegraded {
		return
	}
	h.state = HealthDegraded
	h.stateEpoch++
}

// ResetToHealthy 只在新 runtime generation 才允许（plan：本 generation 不回到 healthy），
// 因此 A-1 模型不提供此方法 —— 新 generation 由重建机器体现。
