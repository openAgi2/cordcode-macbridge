package codexremote

// backoff.go — 重连退避，对齐官方 remote-control 策略
// (codex-rs app-server-transport websocket.rs: next_reconnect_delay):
// 指数增长、封顶、±10% 抖动；封顶当次返回后归零重数；成功由调用方 Reset。
// 基准取 1s（官方 200ms 是纯 WS 重连；我们的 restore 含 token 刷新 HTTP），
// 封顶 30s 与官方一致。

import (
	"math/rand"
	"sync"
	"time"
)

const (
	reconnectBackoffBase = 1 * time.Second
	reconnectBackoffCap  = 30 * time.Second
)

type reconnectBackoff struct {
	mu      sync.Mutex
	attempt int
	rng     *rand.Rand
}

func newReconnectBackoff() *reconnectBackoff {
	return &reconnectBackoff{rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

// Next 返回下一次重试前的等待时长，并推进轮次。
func (b *reconnectBackoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := reconnectBackoffBase
	for i := 0; i < b.attempt && d < reconnectBackoffCap; i++ {
		d *= 2
	}
	if d > reconnectBackoffCap {
		d = reconnectBackoffCap
	}
	b.attempt++
	if d >= reconnectBackoffCap {
		// 官方行为：封顶当次照常返回，随后从基准重数，避免长期故障时
		// 永远卡在最长间隔。
		b.attempt = 0
	}
	jitter := 0.9 + 0.2*b.rng.Float64()
	return time.Duration(float64(d) * jitter)
}

// Reset 在重连成功后调用，下一次失败从基准间隔重数。
func (b *reconnectBackoff) Reset() {
	b.mu.Lock()
	b.attempt = 0
	b.mu.Unlock()
}

// sleepInterruptible 分片睡眠，Agent.Stop 后最多 2s 内返回。
func (a *Agent) sleepInterruptible(d time.Duration) {
	deadline := time.Now().Add(d)
	for {
		a.mu.Lock()
		stopped := a.stopped
		a.mu.Unlock()
		if stopped {
			return
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return
		}
		if remain > 2*time.Second {
			remain = 2 * time.Second
		}
		time.Sleep(remain)
	}
}
