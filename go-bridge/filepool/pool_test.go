package filepool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/go-bridge/admission"
)

// helperConfig 构造一个测试友好的小配置。
func helperConfig() Config {
	return Config{
		PoolSize:          2,
		PerDeviceInFlight: 1, // < PoolSize，保留 1 个全局 slot
		PerDeviceQueued:   2,
		GlobalQueued:      4,
		ReadTimeout:       2 * time.Second,
		Health: admission.FileReadHealthConfig{
			PoolSize:            2,
			MinHealthyFileSlots: 1,
			DegradeAt:           1,
			StuckAgeMillis:      40,
		},
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (Config{PoolSize: 0}).Validate(); err == nil {
		t.Error("poolSize=0 应失败")
	}
	// perDeviceInFlight >= PoolSize：未保留 global slot，应失败
	bad := helperConfig()
	bad.PerDeviceInFlight = 2
	if err := bad.Validate(); err == nil {
		t.Error("perDeviceInFlight >= poolSize 应失败（未保留 global slot）")
	}
	// health invariant 违反（degradeAt > poolSize-minHealthy）
	bad = helperConfig()
	bad.Health.DegradeAt = 5
	if err := bad.Validate(); err == nil {
		t.Error("health degradeAt 越界应失败")
	}
	if err := helperConfig().Validate(); err != nil {
		t.Fatalf("合法配置应通过，got %v", err)
	}
}

func TestSubmitAndExecute(t *testing.T) {
	p, err := New(helperConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	var ran atomic.Int32
	done := make(chan struct{})
	if err := p.Submit(Job{
		DeviceID: "devA",
		Work: func(ctx context.Context) {
			ran.Add(1)
			close(done)
		},
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Work 未在 1s 内执行")
	}
	if ran.Load() != 1 {
		t.Errorf("ran=%d want 1", ran.Load())
	}
}

// 单设备不能占满整个 pool：PerDeviceInFlight=1 < PoolSize=2，
// 第二个设备仍有 slot 可用。
func TestReserveGlobalSlot(t *testing.T) {
	p, _ := New(helperConfig())
	defer p.Close()

	blockA := make(chan struct{})
	aStarted := make(chan struct{})
	bDone := make(chan struct{})

	// devA 占满其 1 个 in-flight slot，长时间阻塞
	p.Submit(Job{DeviceID: "devA", Work: func(ctx context.Context) {
		close(aStarted)
		<-blockA
	}})
	<-aStarted

	// devB 必须仍能在另一个 worker 上执行（global slot 未被 devA 占满）
	p.Submit(Job{DeviceID: "devB", Work: func(ctx context.Context) {
		close(bDone)
	}})
	select {
	case <-bDone:
	case <-time.After(time.Second):
		t.Fatal("devB 被饿死：global slot 未被保留")
	}
	close(blockA)
}

// per-device queued cap → ErrFileBusy
func TestPerDeviceQueuedBusy(t *testing.T) {
	p, _ := New(helperConfig())
	defer p.Close()

	block := make(chan struct{})
	started := make(chan struct{}, 1)
	p.Submit(Job{DeviceID: "devA", Work: func(ctx context.Context) {
		started <- struct{}{}
		<-block
	}})
	<-started

	// inflightDev[A]=1, PerDeviceInFlight=1 → 第 2 个进队列（pending=1, 1+1=2=PerDeviceQueued 仍允许）
	if err := p.Submit(Job{DeviceID: "devA", Work: func(ctx context.Context) {}}); err != nil {
		t.Fatalf("第 2 个应进队列，got %v", err)
	}
	// 第 3 个：inflight 1 + pending 1 = 2 >= PerDeviceQueued=2 → busy
	err := p.Submit(Job{DeviceID: "devA", Work: func(ctx context.Context) {}})
	if !errors.Is(err, ErrFileBusy) {
		t.Fatalf("第 3 个应 ErrFileBusy，got %v", err)
	}
	close(block)
}

// global queued cap → ErrFileBusy
func TestGlobalQueuedBusy(t *testing.T) {
	cfg := helperConfig()
	cfg.PerDeviceQueued = 8 // 抬高单设备 cap，让 global cap 先触发
	cfg.GlobalQueued = 4
	p, _ := New(cfg)
	defer p.Close()

	// 先占满 2 个 worker（devA + devB 各 1 in-flight），后续全部进队列
	block := make(chan struct{})
	for _, d := range []string{"devA", "devB"} {
		started := make(chan struct{}, 1)
		p.Submit(Job{DeviceID: d, Work: func(ctx context.Context) {
			started <- struct{}{}
			<-block
		}})
		<-started
	}
	// 填队列直到 global cap（4）：devC/D/E 各 1
	for _, d := range []string{"devC", "devD", "devE"} {
		if err := p.Submit(Job{DeviceID: d, Work: func(ctx context.Context) {}}); err != nil {
			t.Fatalf("填 %s 应 admit，got %v", d, err)
		}
	}
	// globalQueued 现已 3；第 4 个（devF）使 globalQueued=4 仍 admit
	if err := p.Submit(Job{DeviceID: "devF", Work: func(ctx context.Context) {}}); err != nil {
		t.Fatalf("devF 应 admit（globalQueued=4=cap），got %v", err)
	}
	// 第 5 个 → globalQueued 超 cap → busy
	err := p.Submit(Job{DeviceID: "devG", Work: func(ctx context.Context) {}})
	if !errors.Is(err, ErrFileBusy) {
		t.Fatalf("超 global cap 应 ErrFileBusy，got %v", err)
	}
	close(block)
}

// 卡住 worker → degrading：新 submit 返回 ErrFileDegraded，queued 任务 OnCancel(ErrFileDegraded)。
func TestStuckWorkerDegrades(t *testing.T) {
	p, _ := New(helperConfig())
	defer p.Close()

	release := make(chan struct{})
	aStarted := make(chan struct{})
	p.Submit(Job{DeviceID: "devA", Work: func(ctx context.Context) {
		close(aStarted)
		<-release // 模拟卡死的 syscall，超过 StuckAgeMillis=40
	}})
	<-aStarted

	// 再排一个 devA 任务，它会停在队列里（inflightDev[A]=1=cap）；degrade 时应被 OnCancel
	cancelled := make(chan error, 1)
	p.Submit(Job{
		DeviceID: "devA",
		Work:     func(ctx context.Context) { t.Error("queued job 不应执行 Work") },
		OnCancel: func(err error) { cancelled <- err },
	})

	// 等待 watchdog 触发 degrading（tick ~10ms，stuckAge 40ms）
	select {
	case err := <-cancelled:
		if !errors.Is(err, ErrFileDegraded) {
			t.Errorf("OnCancel err=%v want ErrFileDegraded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued 任务未被 OnCancel(ErrFileDegraded)")
	}

	// 新 submit 应被拒绝
	if err := p.Submit(Job{DeviceID: "devB", Work: func(ctx context.Context) {}}); !errors.Is(err, ErrFileDegraded) {
		t.Errorf("degrade 后新 submit err=%v want ErrFileDegraded", err)
	}
	// health 已进入 degrading（非 healthy）
	if snap := p.Health().Snapshot(); snap.State != admission.HealthDegrading {
		t.Errorf("health=%v want degrading", snap.State)
	}
	close(release)
}

// cooperative cancel：ReadTimeout 到期后 Work 的 ctx 触发，调用方据此丢弃结果。
func TestCooperativeDeadlineCancel(t *testing.T) {
	cfg := helperConfig()
	cfg.ReadTimeout = 40 * time.Millisecond
	p, _ := New(cfg)
	defer p.Close()

	observed := make(chan error, 1)
	p.Submit(Job{DeviceID: "devA", Work: func(ctx context.Context) {
		// 模拟分块读取间检查 ctx
		select {
		case <-ctx.Done():
			observed <- ctx.Err()
		case <-time.After(time.Second):
			observed <- nil
		}
	}})
	select {
	case err := <-observed:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("ctx err=%v want DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未观察到 deadline cancel")
	}
}

// Close 终结 queued 任务（OnCancel）并停止 workers。
func TestCloseDrainsQueued(t *testing.T) {
	p, _ := New(helperConfig())

	block := make(chan struct{})
	started := make(chan struct{}, 1)
	p.Submit(Job{DeviceID: "devA", Work: func(ctx context.Context) {
		started <- struct{}{}
		<-block
	}})
	<-started

	cancelled := make(chan error, 2)
	p.Submit(Job{DeviceID: "devA", Work: func(ctx context.Context) {}, OnCancel: func(err error) { cancelled <- err }})

	go p.Close()
	// queued 任务应被 OnCancel(ErrPoolClosed)
	select {
	case err := <-cancelled:
		if !errors.Is(err, ErrPoolClosed) {
			t.Errorf("close drain err=%v want ErrPoolClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued 任务未被 close drain")
	}
	close(block)

	// close 后 submit 返回 ErrPoolClosed
	if err := p.Submit(Job{DeviceID: "devX", Work: func(ctx context.Context) {}}); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("close 后 submit err=%v want ErrPoolClosed", err)
	}
}

// round-robin：两个设备交替获得调度（非严格交替，但都应最终执行）。
func TestRoundRobinFairness(t *testing.T) {
	cfg := helperConfig() // PoolSize=2, PerDeviceInFlight=1
	cfg.PerDeviceQueued = 8
	cfg.GlobalQueued = 16
	p, _ := New(cfg)
	defer p.Close()

	const N = 6
	var aCount, bCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(2)
		p.Submit(Job{DeviceID: "devA", Work: func(ctx context.Context) { aCount.Add(1); wg.Done() }})
		p.Submit(Job{DeviceID: "devB", Work: func(ctx context.Context) { bCount.Add(1); wg.Done() }})
	}
	wg.Wait()
	if aCount.Load() == 0 || bCount.Load() == 0 {
		t.Errorf("fairness 失败：a=%d b=%d（两者都应 > 0）", aCount.Load(), bCount.Load())
	}
}
