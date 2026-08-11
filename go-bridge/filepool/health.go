// Package filepool 实现 plan §3.6.3 的 bounded file-read worker pool：
// 把 read_file_v2（及未来的文件类 RPC）的 I/O 从 per-device inbound scheduler
// 解耦到全局专用 bounded pool，避免单个卡住的 os.Read 阻塞同设备后续
// permission_response/question_reply/send_message 的 inbound dispatch。
//
// 三个正交状态机之一（FileReadHealth），与 RuntimeAdmission、Mac SupervisorState 正交。
// 本包只实现 Go 进程内的 pool + health；management HTTP（/internal/status 的
// fileReadHealth 字段、lease/commit/abort）属于 R1.11，不在本包。
package filepool

import (
	"sync"

	"github.com/openAgi2/cordcode-macbridge/go-bridge/admission"
)

// Health 是 admission.FileReadHealthMachine 的并发安全运行时包装。
// proof 模型本身非线程安全（简单字段读写，无内部锁）；本包装器在 mu 下串行化
// 所有访问，避免在运行时重新推导三态语义（plan §3.6.3：healthy → degrading → degraded）。
type Health struct {
	mu    sync.Mutex
	model *admission.FileReadHealthMachine
}

// NewHealth 构造 healthy 态机器（stateEpoch 从 1 起，与 proof 一致）。
func NewHealth() *Health {
	return &Health{model: admission.NewFileReadHealthMachine()}
}

// Snapshot 是 health 的不可变观察值。
type Snapshot struct {
	State admission.HealthState
	Epoch uint64
}

// Snapshot 在锁下读取当前 (state, epoch)。
func (h *Health) Snapshot() Snapshot {
	if h == nil || h.model == nil {
		return Snapshot{State: admission.HealthHealthy, Epoch: 1}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return Snapshot{State: h.model.Health(), Epoch: h.model.StateEpoch()}
}

// IsAccepting 当 health 仍允许新文件 RPC 进入时返回 true。
// 进入 degrading 即视为“拒绝已生效”（plan：degrading 安装新 file RPC 拒绝）。
func (h *Health) IsAccepting() bool {
	if h == nil {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.model.Health() == admission.HealthHealthy
}

// MarkDegrading 原子进入 degrading 并递增 stateEpoch（plan：达到 stuck 阈值）。
// 已 degraded 则幂等。
func (h *Health) MarkDegrading() Snapshot {
	if h == nil || h.model == nil {
		return Snapshot{State: admission.HealthHealthy, Epoch: 1}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.model.MarkDegrading()
	return Snapshot{State: h.model.Health(), Epoch: h.model.StateEpoch()}
}

// MarkDegraded 在“拒绝已生效且队列已终态”后进入 degraded（不可逆）。
func (h *Health) MarkDegraded() Snapshot {
	if h == nil || h.model == nil {
		return Snapshot{State: admission.HealthHealthy, Epoch: 1}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.model.MarkDegraded()
	return Snapshot{State: h.model.Health(), Epoch: h.model.StateEpoch()}
}
