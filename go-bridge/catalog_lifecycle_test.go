package gobridge

// catalog_lifecycle_test.go 验证 Phase 1 chunk 1D：shared provider lifecycle primitives（设计 §4.3/§11）。
//
// 两组原语：
//  1. catalogLifecycleProvider：singleflight（同 scope 并发只调一次 inner）、bounded timeout（hung
//     上游不无限阻塞）、ctx 取消传播、leader error 共享给 waiter、不同 scope 不互锁、成功透传。
//  2. catalogBackoff：有上限指数退避序列 + GiveUp 阈值。
//
// 用可控 fake provider（计数 / 阻塞 / 受控错误），不触网络。

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// slowCatalogProvider 是可控 fake：计数 FetchPage0 调用；可选通过 releaseCh 阻塞到测试方 close
//（singleflight 时序）；可选返回 err；mode 决定是「尊重 ctx」还是「纯 sleep 无视 ctx」。
type slowCatalogProvider struct {
	mu        sync.Mutex
	calls     int
	releaseCh chan struct{} // 非 nil 时 FetchPage0 阻塞到 close
	err       error
	honorCtx  bool // true: 阻塞于 <-ctx.Done()；false: 纯 sleep（无视 ctx）
	sleepFor  time.Duration
}

func (s *slowCatalogProvider) FetchPage0(ctx context.Context, _ CatalogQuery) (CatalogPage0, error) {
	s.mu.Lock()
	s.calls++
	release := s.releaseCh
	honor := s.honorCtx
	sleep := s.sleepFor
	err := s.err
	s.mu.Unlock()

	if release != nil {
		// singleflight 时序：阻塞到测试方 close，让并发请求成为 waiter。
		select {
		case <-release:
		case <-ctx.Done():
			return CatalogPage0{}, ctx.Err()
		}
	}
	if honor {
		<-ctx.Done() // 尊重 ctx（模拟 ctx-aware 传输）
		return CatalogPage0{}, ctx.Err()
	}
	if sleep > 0 {
		time.Sleep(sleep) // 无视 ctx（模拟 slow 传输）
	}
	if err != nil {
		return CatalogPage0{}, err
	}
	return CatalogPage0{Sessions: synthSessions(1), Fingerprint: fingerprintCatalog(synthSessions(1))}, nil
}

func (s *slowCatalogProvider) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// ── catalogLifecycleProvider ───────────────────────────────────────────────────

// TimeoutFiresWhenInnerHonorsCtx：inner 尊重 ctx 时，decorator 的 bounded timeout 在 timeout 到期
// 触发，caller 在 ~timeout 内拿到 context.DeadlineExceeded（不等到 inner 自然返回）。
func TestCatalogLifecycle_TimeoutFires(t *testing.T) {
	inner := &slowCatalogProvider{honorCtx: true}
	p := newCatalogLifecycleProvider(inner, 30*time.Millisecond)

	start := time.Now()
	_, err := p.FetchPage0(context.Background(), CatalogQuery{BackendID: "codex", Limit: 1})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got err=%v, want DeadlineExceeded", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("timeout 未及时触发：elapsed=%v（timeout=30ms）", elapsed)
	}
}

// CancelPropagates：caller ctx 取消时，FetchPage0 立即返回 context.Canceled（连接断 / bridge 退出场景）。
func TestCatalogLifecycle_CancelPropagates(t *testing.T) {
	inner := &slowCatalogProvider{honorCtx: true}
	p := newCatalogLifecycleProvider(inner, 5*time.Second) // 长 timeout，确保是 caller ctx 触发
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := p.FetchPage0(ctx, CatalogQuery{BackendID: "codex", Limit: 1})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got err=%v, want Canceled", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("cancel 未及时传播：elapsed=%v", elapsed)
	}
}

// SingleflightDedupes：同 scope 并发两个 FetchPage0 → inner 只被调一次；两个 caller 拿到同一结果。
func TestCatalogLifecycle_SingleflightDedupes(t *testing.T) {
	inner := &slowCatalogProvider{releaseCh: make(chan struct{})}
	p := newCatalogLifecycleProvider(inner, 5*time.Second)
	q := CatalogQuery{BackendID: "codex", Directory: "/d", Limit: 1}

	var wg sync.WaitGroup
	type res struct {
		page CatalogPage0
		err  error
	}
	results := make([]res, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx].page, results[idx].err = p.FetchPage0(context.Background(), q)
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // 让两个 goroutine 都进入（一个 leader in-flight，一个 waiter）
	close(inner.releaseCh)            // 放行 leader
	wg.Wait()

	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner calls = %d, want 1（同 scope 须 singleflight）", got)
	}
	for i, r := range results {
		if r.err != nil {
			t.Errorf("caller[%d] err = %v, want nil", i, r.err)
		}
		if len(r.page.Sessions) != 1 {
			t.Errorf("caller[%d] sessions = %d, want 1", i, len(r.page.Sessions))
		}
	}
	// 两个 caller 拿到同一 fingerprint（同一结果）。
	if results[0].page.Fingerprint != results[1].page.Fingerprint {
		t.Error("两 caller fingerprint 不一致（应复用 leader 结果）")
	}
}

// DifferentScopesDontBlock：不同 scope 的并发请求互不阻塞，各自调 inner（不串行化）。
func TestCatalogLifecycle_DifferentScopesDontBlock(t *testing.T) {
	inner := &slowCatalogProvider{releaseCh: make(chan struct{})}
	p := newCatalogLifecycleProvider(inner, 5*time.Second)

	qA := CatalogQuery{BackendID: "codex", Directory: "/a", Limit: 1}
	qB := CatalogQuery{BackendID: "codex", Directory: "/b", Limit: 1}

	doneA := make(chan error, 1)
	doneB := make(chan error, 1)
	go func() {
		_, err := p.FetchPage0(context.Background(), qA)
		doneA <- err
	}()
	go func() {
		_, err := p.FetchPage0(context.Background(), qB)
		doneB <- err
	}()
	time.Sleep(30 * time.Millisecond)
	// 两个不同 scope 应各自成为 leader（不互锁）：inner.calls 应为 2，尽管 inner 仍阻塞在 releaseCh。
	if got := inner.callCount(); got != 2 {
		t.Fatalf("inner calls = %d, want 2（不同 scope 不应 singleflight 在一起）", got)
	}
	close(inner.releaseCh)
	if err := <-doneA; err != nil {
		t.Fatalf("qA err = %v", err)
	}
	if err := <-doneB; err != nil {
		t.Fatalf("qB err = %v", err)
	}
}

// LeaderErrorSharedWithWaiters：leader 的 error 被所有 waiter 复用（不丢、不掩盖）。
func TestCatalogLifecycle_LeaderErrorShared(t *testing.T) {
	sentinel := errors.New("upstream boom")
	inner := &slowCatalogProvider{releaseCh: make(chan struct{}), err: sentinel}
	p := newCatalogLifecycleProvider(inner, 5*time.Second)
	q := CatalogQuery{BackendID: "codex", Limit: 1}

	errs := make([]error, 3)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = p.FetchPage0(context.Background(), q)
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(inner.releaseCh)
	wg.Wait()

	if inner.callCount() != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.callCount())
	}
	for i, e := range errs {
		if !errors.Is(e, sentinel) {
			t.Errorf("caller[%d] err = %v, want sentinel %v（leader error 须共享）", i, e, sentinel)
		}
	}
}

// PassthroughOnSuccess：inner 成功时 decorator 透传结果不变。
func TestCatalogLifecycle_PassthroughSuccess(t *testing.T) {
	inner := &slowCatalogProvider{} // 无 release/err/sleep → 立即成功
	p := newCatalogLifecycleProvider(inner, time.Second)
	page, err := p.FetchPage0(context.Background(), CatalogQuery{BackendID: "codex", Limit: 1})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(page.Sessions) != 1 || page.Fingerprint == "" {
		t.Fatalf("page = %+v, want 1 session + fingerprint", page)
	}
}

// ── catalogBackoff ──────────────────────────────────────────────────────────────

// DelaySequence：默认 backoff 序列 500ms→1s→2s→4s→8s→8s（封顶 Max=8s），第 6 步 GiveUp。
func TestCatalogBackoff_DelaySequence(t *testing.T) {
	b := defaultCatalogBackoff
	want := []time.Duration{
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		8 * time.Second, // step 5：16s 封顶为 8s
	}
	for step, w := range want {
		if got := b.Delay(step); got != w {
			t.Errorf("Delay(%d) = %v, want %v", step, got, w)
		}
		if b.GiveUp(step) {
			t.Errorf("GiveUp(%d) = true, want false（MaxSteps 内不放弃）", step)
		}
	}
	// step >= MaxSteps → GiveUp
	if !b.GiveUp(6) {
		t.Errorf("GiveUp(6) = false, want true（达到 MaxSteps）")
	}
	if b.Delay(6) != b.Max {
		t.Errorf("Delay(6) = %v, want Max %v（GiveUp 后返回 Max）", b.Delay(6), b.Max)
	}
}

// NegativeStep：step<0 → Delay 0；GiveUp 不受负 step 影响（MaxSteps 阈值正向）。
func TestCatalogBackoff_NegativeStep(t *testing.T) {
	b := defaultCatalogBackoff
	if got := b.Delay(-1); got != 0 {
		t.Errorf("Delay(-1) = %v, want 0", got)
	}
	if b.GiveUp(-1) {
		t.Errorf("GiveUp(-1) = true, want false")
	}
}

// NoMaxSteps（MaxSteps=0 表示不设上限）：GiveUp 恒 false；Delay 持续封顶 Max 不无限增长。
func TestCatalogBackoff_NoMaxSteps(t *testing.T) {
	b := catalogBackoff{Initial: 100 * time.Millisecond, Factor: 2, Max: 1 * time.Second, MaxSteps: 0}
	for step := 0; step < 20; step++ {
		if b.GiveUp(step) {
			t.Errorf("MaxSteps=0 但 GiveUp(%d)=true", step)
		}
		if d := b.Delay(step); d > b.Max {
			t.Errorf("Delay(%d) = %v 超过 Max %v", step, d, b.Max)
		}
	}
	if b.Delay(50) != b.Max {
		t.Errorf("Delay(50) = %v, want Max %v（大 step 须封顶）", b.Delay(50), b.Max)
	}
}
