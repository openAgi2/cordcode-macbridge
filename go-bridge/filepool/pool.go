package filepool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/go-bridge/admission"
)

// ErrFileBusy 对应 plan §3.6.3 的 file.read_busy：per-device 或 global 队列/配额已满。
var ErrFileBusy = errors.New("file.read_busy")

// ErrFileDegraded 对应 plan §3.6.3 的 file.read_degraded：FileReadHealth 已进入 degrading/degraded。
var ErrFileDegraded = errors.New("file.read_degraded")

// ErrPoolClosed 在 pool 关闭后提交任务时返回。
var ErrPoolClosed = errors.New("filepool: closed")

// Config 是 file-read worker pool 的资源上限（plan §3.6.3 / A0.5）。
//
// 不变量（Validate 强制）：
//   - PoolSize > 0；PerDeviceInFlight < PoolSize（保留至少 1 个全局 slot，
//     单一 device 不能占满整个 pool，plan：另保留至少一个 global slot）。
//   - 各 cap > 0；ReadTimeout > 0。
//   - Health.Validate()（degradeAt <= poolSize - minHealthyFileSlots）。
type Config struct {
	PoolSize          uint32
	PerDeviceInFlight uint32 // 单设备最大并发执行；必须 < PoolSize（保留 global slot）
	PerDeviceQueued   uint32 // 单设备最大排队（含 in-flight）
	GlobalQueued      uint32 // 全局最大排队总数
	ReadTimeout       time.Duration
	Health            admission.FileReadHealthConfig
}

// Validate 校验资源不变式。配置 gate 失败时返回非 nil，调用方不得构造 pool。
func (c Config) Validate() error {
	if c.PoolSize == 0 {
		return fmt.Errorf("filepool: poolSize must be > 0")
	}
	if c.PerDeviceInFlight == 0 || c.PerDeviceInFlight >= c.PoolSize {
		return fmt.Errorf("filepool: perDeviceInFlight(%d) must be in (0, poolSize=%d) to reserve a global slot", c.PerDeviceInFlight, c.PoolSize)
	}
	if c.PerDeviceQueued == 0 {
		return fmt.Errorf("filepool: perDeviceQueued must be > 0")
	}
	if c.GlobalQueued == 0 {
		return fmt.Errorf("filepool: globalQueued must be > 0")
	}
	if c.ReadTimeout <= 0 {
		return fmt.Errorf("filepool: readTimeout must be > 0")
	}
	if err := c.Health.Validate(); err != nil {
		return fmt.Errorf("filepool: health config: %w", err)
	}
	return nil
}

// Job 是一次文件读取任务。DeviceID 是稳定认证设备 ID（plan：fair 身份）。
//
// 契约：Submit 返回 nil 后，pool 保证恰好调用 Work 或 OnCancel 之一：
//   - Work(ctx)：在 pool worker 上执行实际 I/O；ctx 带 ReadTimeout deadline，
//     调用方在 chunk 间检查 ctx，并在 commit 前再次校验 ctx.Err()==nil。
//   - OnCancel(err)：pool 在 admit 之后、Work 之前因 degrading 终止该任务时调用
//     （ErrFileDegraded）。Submit 同步拒绝（busy/degraded/closed）时不调用 OnCancel，
//     直接把 err 返回给调用方处理。
type Job struct {
	DeviceID string
	Work     func(ctx context.Context)
	OnCancel func(err error)
}

type pendingJob struct {
	job      Job
	ctx      context.Context
	cancel   context.CancelFunc
	startedAt time.Time // worker 取走时设置（watchdog 用）
}

// Pool 是全局专用 bounded file-read worker pool。
type Pool struct {
	cfg    Config
	health *Health

	mu           sync.Mutex
	queues       map[string][]*pendingJob
	deviceOrder  []string
	cursor       int
	inflightDev  map[string]uint32
	globalQueued int
	inflightJobs map[*pendingJob]struct{}
	rejecting    bool // degrading 已安装：新 admit 拒绝 + 队列已 drain
	closed       bool

	wake      chan struct{}
	stop      chan struct{}
	once      sync.Once
	wg        sync.WaitGroup
	wdStop    chan struct{}
}

// New 构造并启动 pool（workers + watchdog）。cfg 必须先 Validate 通过。
func New(cfg Config) (*Pool, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &Pool{
		cfg:          cfg,
		health:       NewHealth(),
		queues:       make(map[string][]*pendingJob),
		inflightDev:  make(map[string]uint32),
		inflightJobs: make(map[*pendingJob]struct{}),
		wake:         make(chan struct{}, 1),
		stop:         make(chan struct{}),
		wdStop:       make(chan struct{}),
	}
	for i := uint32(0); i < cfg.PoolSize; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	go p.watchdog()
	return p, nil
}

// Health 返回运行时 health 包装（观察 state/epoch 用）。
func (p *Pool) Health() *Health { return p.health }

// Submit 尝试 admit 一个任务。
//   - 返回 nil：pool 已接管任务，稍后在 worker 上执行 Work，或在 degrading 时 OnCancel。
//   - 返回 ErrFileBusy/ErrFileDegraded/ErrPoolClosed：pool 未接管，调用方自行处理。
func (p *Pool) Submit(job Job) error {
	if job.Work == nil {
		return fmt.Errorf("filepool: job.Work is nil")
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	if p.rejecting || !p.health.IsAccepting() {
		p.mu.Unlock()
		return ErrFileDegraded
	}
	dev := job.DeviceID
	pendingForDev := uint32(len(p.queues[dev]))
	if p.inflightDev[dev]+pendingForDev >= p.cfg.PerDeviceQueued {
		p.mu.Unlock()
		return ErrFileBusy
	}
	if p.globalQueued >= int(p.cfg.GlobalQueued) {
		p.mu.Unlock()
		return ErrFileBusy
	}
	pj := &pendingJob{job: job}
	p.queues[dev] = append(p.queues[dev], pj)
	if pendingForDev == 0 {
		p.deviceOrder = append(p.deviceOrder, dev)
	}
	p.globalQueued++
	p.mu.Unlock()

	select {
	case p.wake <- struct{}{}:
	default:
	}
	return nil
}

// worker 从 per-device 队列按 round-robin 取任务执行。
func (p *Pool) worker() {
	defer p.wg.Done()
	for {
		pj, ok := p.pop()
		if !ok {
			return
		}
		// 执行：在 deadline ctx 下运行 Work；记录 startedAt 供 watchdog。
		ctx, cancel := context.WithTimeout(context.Background(), p.cfg.ReadTimeout)
		p.mu.Lock()
		pj.ctx = ctx
		pj.cancel = cancel
		pj.startedAt = time.Now()
		p.inflightJobs[pj] = struct{}{}
		p.mu.Unlock()

		pj.job.Work(ctx)

		p.mu.Lock()
		delete(p.inflightJobs, pj)
		p.inflightDev[pj.job.DeviceID]--
		if p.inflightDev[pj.job.DeviceID] == 0 {
			delete(p.inflightDev, pj.job.DeviceID)
		}
		p.mu.Unlock()
		cancel()
	}
}

// pop 按 round-robin 从 deviceOrder 取下一个满足 in-flight cap 的任务。
// 无可取任务时阻塞等待 wake；pool 关闭时返回 ok=false。
func (p *Pool) pop() (*pendingJob, bool) {
	for {
		p.mu.Lock()
		if pj := p.popLocked(); pj != nil {
			p.mu.Unlock()
			return pj, true
		}
		if p.closed {
			p.mu.Unlock()
			return nil, false
		}
		p.mu.Unlock()

		select {
		case <-p.wake:
		case <-p.stop:
			return nil, false
		}
	}
}

// popLocked 在已持锁前提下尝试取一个任务；round-robin + per-device in-flight cap。
// PerDeviceInFlight < PoolSize（Validate 强制）保证至少一个 worker 永远可用于其它设备。
func (p *Pool) popLocked() *pendingJob {
	n := len(p.deviceOrder)
	for offset := 0; offset < n; offset++ {
		idx := (p.cursor + offset) % n
		dev := p.deviceOrder[idx]
		queue := p.queues[dev]
		if len(queue) == 0 {
			continue
		}
		if p.inflightDev[dev] >= p.cfg.PerDeviceInFlight {
			continue
		}
		pj := queue[0]
		p.queues[dev] = queue[1:]
		p.globalQueued--
		p.inflightDev[dev]++
		p.cursor = (idx + 1) % n
		if len(p.queues[dev]) == 0 {
			p.removeDeviceLocked(dev)
		}
		return pj
	}
	return nil
}

// removeDeviceLocked 删除空设备槽并修正 cursor/deviceOrder。
func (p *Pool) removeDeviceLocked(dev string) {
	for i, d := range p.deviceOrder {
		if d == dev {
			p.deviceOrder = append(p.deviceOrder[:i], p.deviceOrder[i+1:]...)
			if len(p.deviceOrder) == 0 {
				p.cursor = 0
			} else if p.cursor >= len(p.deviceOrder) {
				p.cursor = 0
			}
			return
		}
	}
}

// watchdog 周期扫描 in-flight 任务；超过 stuckAge 即触发 degrading + drain 队列。
func (p *Pool) watchdog() {
	tick := time.NewTicker(stuckTickInterval(p.cfg.Health.StuckAgeMillis))
	defer tick.Stop()
	for {
		select {
		case <-p.wdStop:
			return
		case <-tick.C:
			p.scanStuck()
		}
	}
}

// stuckTickInterval 取 stuckAge/4，至少 5ms，避免测试里 tick 过粗。
func stuckTickInterval(stuckAgeMillis uint32) time.Duration {
	d := time.Duration(stuckAgeMillis) * time.Millisecond / 4
	if d < 5*time.Millisecond {
		d = 5 * time.Millisecond
	}
	return d
}

// scanStuck 检测卡住的 in-flight 任务；首次发现即安装 degrading 并 drain 队列。
func (p *Pool) scanStuck() {
	stuckAge := time.Duration(p.cfg.Health.StuckAgeMillis) * time.Millisecond
	now := time.Now()

	p.mu.Lock()
	stuck := false
	for pj := range p.inflightJobs {
		if !pj.startedAt.IsZero() && now.Sub(pj.startedAt) >= stuckAge {
			stuck = true
			break
		}
	}
	if !stuck || p.rejecting {
		// rejecting 已置位时不再重复 drain；MarkDegrading 幂等。
		p.mu.Unlock()
		return
	}
	// 安装 degrading：拒绝新 admit + 终结所有 queued 任务（ErrFileDegraded）。
	p.rejecting = true
	drained := p.drainLocked(ErrFileDegraded)
	p.mu.Unlock()

	p.health.MarkDegrading()
	_ = drained // drained 个 OnCancel 已在 drainLocked 内调用
}

// drainLocked 终结所有 queued（未执行）任务，对每个调用 OnCancel(err)。
// 必须在 p.mu 下调用。返回 drain 数量。in-flight 任务不在此处理（无法抢占阻塞 syscall）。
func (p *Pool) drainLocked(err error) int {
	n := 0
	for dev, queue := range p.queues {
		for _, pj := range queue {
			n++
			p.globalQueued--
			if pj.job.OnCancel != nil {
				// OnCancel 可能回调到 handler（SendResult），不在持锁状态下调用以防死锁。
				go pj.job.OnCancel(err)
			}
		}
		delete(p.queues, dev)
	}
	// 重建 deviceOrder（全部空）
	p.deviceOrder = p.deviceOrder[:0]
	p.cursor = 0
	return n
}

// Close 关闭 pool：停止 admit、终结 queued 任务、等待 workers 退出。
// in-flight 任务被 cancel（ctx），其 Work 自行按 ctx.Err() 丢弃 late result。
func (p *Pool) Close() {
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		drained := p.drainLocked(ErrPoolClosed)
		// cancel 所有 in-flight
		for pj := range p.inflightJobs {
			if pj.cancel != nil {
				pj.cancel()
			}
		}
		p.mu.Unlock()
		_ = drained
		close(p.stop)
		close(p.wdStop)
		// 唤醒可能在等待的 workers
		select {
		case p.wake <- struct{}{}:
		default:
		}
	})
	p.wg.Wait()
}
