package gobridge

// catalog_lifecycle.go 是 Phase 1 chunk 1D：shared provider lifecycle primitives（设计 §4.3 / §11）。
// 它是所有 Phase 2-5 catalog client（Codex/Grok/Claude）复用的通用生命周期骨架，与具体 backend 传输
// 无关：
//
//   - catalogLifecycleProvider：装饰任意 SessionCatalogProvider，叠加 (1) per-scope singleflight
//     （§11「同一 scope 的并发刷新 singleflight」——同 scope 并发刷新只打一次上游）、(2) per-call
//     bounded timeout（§11「请求可取消且有明确 timeout」——hung 上游不无限阻塞）、(3) ctx 取消传播
//     （§11「请求可取消」——连接断 / bridge 退出时中止在飞请求）。它本身不持有持久连接：持久连接的
//     backend（Codex app-server / Grok ACP）在其 supervision loop 里组合本装饰器 + 自己的受管传输。
//   - catalogBackoff：有上限的指数退避策略（§11「断线后有上限的指数退避」）。纯计算器，不 sleep、
//     不计时——Phase 2-5 的 supervision loop 按返回的 Delay 重连，GiveUp 为真时停止。确定无 jitter，
//     便于单测。
//
// Phase 1 只交付并单测这两个原语；实际持久连接 supervision loop 随 Phase 2 第一个持久 catalog 连接
// （Codex）落地。OpenCode（§5.3）走既有 ocProxy HTTP（已由 1C 接 cursor v2），不经此装饰器。
//
// inner 契约：被装饰的 provider 必须在其上游请求里尊重传入 ctx（http.Request.WithContext /
// 带 ctx 的 JSON-RPC），timeout/cancel 才能真正生效。Phase 2-5 的 catalog client 传输均满足此契约。

import (
	"context"
	"math"
	"sync"
	"time"
)

// defaultCatalogFetchTimeout 是单次 catalog 上游请求的硬上限（§11「明确 timeout」）。
// 覆盖一次 list/thread 请求的合理上限；超过即返回 ctx.DeadlineExceeded，由调用方（1B cache）
// 走 cursor_stale / unavailable 路径，不无限阻塞 bridge。
const defaultCatalogFetchTimeout = 8 * time.Second

// catalogLifecycleProvider 装饰 SessionCatalogProvider，叠加 singleflight + bounded timeout + ctx
// 取消。实现 SessionCatalogProvider 接口，对 1B catalogSnapshotCache 透明（wrap 后注入即可）。
type catalogLifecycleProvider struct {
	inner   SessionCatalogProvider
	timeout time.Duration

	mu       sync.Mutex
	inFlight map[string]*catalogFlight
}

// catalogFlight 是一次 in-flight FetchPage0 的共享结果槽：leader 计算，waiters 复用同一结果。
// done close 即表示 result/err 已写就绪（leader 在 close 前于 mu 下写入，waiter 读到 close 后读取，
// happens-before 成立）。
type catalogFlight struct {
	done   chan struct{}
	result CatalogPage0
	err    error
}

// newCatalogLifecycleProvider 用给定 timeout 包装 inner。timeout<=0 → defaultCatalogFetchTimeout。
func newCatalogLifecycleProvider(inner SessionCatalogProvider, timeout time.Duration) *catalogLifecycleProvider {
	if timeout <= 0 {
		timeout = defaultCatalogFetchTimeout
	}
	return &catalogLifecycleProvider{
		inner:    inner,
		timeout:  timeout,
		inFlight: make(map[string]*catalogFlight),
	}
}

// FetchPage0 实现 SessionCatalogProvider：singleflight（同 scope 只调一次 inner）+ bounded timeout
// + ctx 取消。waiters 复用 leader 的结果（含 error）；waiter 自己的 ctx 先取消则返回其 ctx.Err()，
// leader 仍继续为其他 waiter 完成。
func (p *catalogLifecycleProvider) FetchPage0(ctx context.Context, q CatalogQuery) (result CatalogPage0, err error) {
	key := catalogScopeKey(q)

	p.mu.Lock()
	if f, exist := p.inFlight[key]; exist {
		p.mu.Unlock()
		select {
		case <-f.done:
			return f.result, f.err
		case <-ctx.Done():
			return CatalogPage0{}, ctx.Err()
		}
	}
	f := &catalogFlight{done: make(chan struct{})}
	p.inFlight[key] = f
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.inFlight, key)
		f.result, f.err = result, err
		close(f.done)
		p.mu.Unlock()
	}()

	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.inner.FetchPage0(callCtx, q)
}

// catalogBackoff 是有上限的指数退避策略（§11「断线后有上限的指数退避」）。纯计算器：不 sleep、
// 不计时。Phase 2-5 supervision loop 在每次重连失败后 step++，按 Delay 等待后重试；GiveUp 为真则
// 停止重连（catalog 进入 unavailable，由 1B cache 的 cursor_stale / stale 路径呈现）。确定无 jitter，
// 便于单测断言精确序列。
type catalogBackoff struct {
	Initial  time.Duration // 第 0 步延迟（如 500ms）
	Factor   float64       // 每步乘数（如 2.0）
	Max      time.Duration // 单步延迟上限（如 8s）
	MaxSteps int           // 重连步数上限；step>=MaxSteps 时 GiveUp（0 表示不设上限）
}

// defaultCatalogBackoff 是 catalog client 默认退避：500ms→1s→2s→4s→8s→8s，第 6 步起 GiveUp
//（总重连窗口约 23.5s，足以覆盖短暂抖动，又不至于长时间静默不可用）。
var defaultCatalogBackoff = catalogBackoff{
	Initial:  500 * time.Millisecond,
	Factor:   2.0,
	Max:      8 * time.Second,
	MaxSteps: 6,
}

// Delay 返回第 step 步的重连延迟（step 从 0 起）。step<0 → 0；GiveUp(step) → Max（调用方应先查
// GiveUp 决定是否停止）；否则 Initial * Factor^step，封顶 Max。
func (b catalogBackoff) Delay(step int) time.Duration {
	if step < 0 {
		return 0
	}
	if b.GiveUp(step) {
		return b.Max
	}
	d := float64(b.Initial) * math.Pow(b.Factor, float64(step))
	if d != d || d > float64(b.Max) { // NaN（d!=d）或超上限 → 封顶
		return b.Max
	}
	return time.Duration(d)
}

// GiveUp 报告是否达到重连步数上限（supervision loop 应停止重连）。
func (b catalogBackoff) GiveUp(step int) bool {
	return b.MaxSteps > 0 && step >= b.MaxSteps
}
