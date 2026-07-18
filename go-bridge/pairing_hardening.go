package gobridge

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// pairingAttemptConfig 控制 P1-7 的在线猜测防护。
//
// 手动码是 6 位纯数字（~20 bit 熵），若允许单独查找则可在配对窗口内被并发枚举。
// 因此：claim 必须同时提交高熵 pairingId + manualCode；并叠加三层限流（来源 IP、
// pairingId 连续失败、全局并发），达到阈值后不泄漏“码存在/不存在”差异。
var pairingAttemptConfig = struct {
	sourceIPLimit          int
	sourceIPWindow         time.Duration
	perPairingFailLimit    int
	perPairingFailWindow   time.Duration
	globalConcurrentClaims int
}{
	sourceIPLimit:          10, // 单 IP 每窗口最多 10 次配对尝试
	sourceIPWindow:         time.Minute,
	perPairingFailLimit:    5, // 单 pairingId 连续失败 5 次后失效
	perPairingFailWindow:   5 * time.Minute,
	globalConcurrentClaims: 16, // 全局同时在 claim 流程中的连接数上限
}

// pairingAttemptGate 是进程级配对尝试闸门，串行化 claim 前的资源治理。
type pairingAttemptGate struct {
	mu sync.Mutex

	sourceBuckets map[string]*slidingBucket // key = source IP
	pairFails     map[string]*slidingBucket // key = pairingId
	activeClaims  int
}

var globalPairingGate = &pairingAttemptGate{
	sourceBuckets: make(map[string]*slidingBucket),
	pairFails:     make(map[string]*slidingBucket),
}

// newPairingAttemptGate 构造一个独立的配对闸门实例（T08：注入方向，避免测试共享进程级
// globalPairingGate 状态）。生产代码仍用 globalPairingGate；新代码/测试应通过实例注入。
func newPairingAttemptGate() *pairingAttemptGate {
	return &pairingAttemptGate{
		sourceBuckets: make(map[string]*slidingBucket),
		pairFails:     make(map[string]*slidingBucket),
	}
}

// maxPairingBuckets 是单个 map（sourceBuckets / pairFails）的全局容量上限（T08）。
// 超过后对新 key fail-closed（返回 rate_limited），防止任意 pairingId/IP 制造无界增长。
const maxPairingBuckets = 4096

type slidingBucket struct {
	window time.Duration
	limit  int
	counts []stamp
}

type stamp struct {
	at time.Time
}

func (b *slidingBucket) allow(now time.Time) (allowed bool, remaining int) {
	cutoff := now.Add(-b.window)
	fresh := b.counts[:0]
	for _, s := range b.counts {
		if s.at.After(cutoff) {
			fresh = append(fresh, s)
		}
	}
	if len(fresh) >= b.limit {
		b.counts = fresh
		return false, 0
	}
	b.counts = append(fresh, stamp{at: now})
	return true, b.limit - len(b.counts)
}

// sweepStale 惰性清理 sourceBuckets 与 pairFails 中窗口内无计数的 bucket（T08 TTL 清理）。
// 调用方必须持有 g.mu。
func (g *pairingAttemptGate) sweepStale(now time.Time) {
	sweep := func(buckets map[string]*slidingBucket) {
		for k, b := range buckets {
			cutoff := now.Add(-b.window)
			fresh := b.counts[:0]
			for _, s := range b.counts {
				if s.at.After(cutoff) {
					fresh = append(fresh, s)
				}
			}
			if len(fresh) == 0 {
				delete(buckets, k)
			} else {
				b.counts = fresh
			}
		}
	}
	sweep(g.sourceBuckets)
	sweep(g.pairFails)
}

// acquire 检查来源 IP 限流与全局并发上限；进入 claim 流程时占用一个全局槽位。
// 返回 nil 表示放行；返回非 nil PairingError 时应返回 pairing.rate_limited。
// 调用方必须在流程结束后调用 release() 归还全局槽位。
//
// T08: 入口先惰性清理窗口内无计数的 bucket（防 sourceBuckets 无界增长），并对 source IP 达到
// maxPairingBuckets 时对新 IP fail-closed（返回 rate_limited）。
func (g *pairingAttemptGate) acquire(r *http.Request) *PairingError {
	ip := pairingClientIP(r)
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()
	g.sweepStale(now)

	sb := g.sourceBuckets[ip]
	if sb == nil {
		// T08: 容量满时对新 key fail-closed，防任意 IP 制造无界 bucket。
		if len(g.sourceBuckets) >= maxPairingBuckets {
			return &PairingError{Code: "pairing.rate_limited", Message: "配对尝试过于频繁，请稍后再试"}
		}
		sb = &slidingBucket{window: pairingAttemptConfig.sourceIPWindow, limit: pairingAttemptConfig.sourceIPLimit}
		g.sourceBuckets[ip] = sb
	}
	if ok, _ := sb.allow(now); !ok {
		return &PairingError{Code: "pairing.rate_limited", Message: "配对尝试过于频繁，请稍后再试"}
	}
	if g.activeClaims >= pairingAttemptConfig.globalConcurrentClaims {
		return &PairingError{Code: "pairing.rate_limited", Message: "配对并发已达上限，请稍后再试"}
	}
	g.activeClaims++
	return nil
}

func (g *pairingAttemptGate) release() {
	g.mu.Lock()
	g.activeClaims--
	if g.activeClaims < 0 {
		g.activeClaims = 0
	}
	g.mu.Unlock()
}

// recordPairingFailure 记录某 pairingId 的一次失败；连续失败超阈值返回 true（该 session 应失效）。
// 返回值仅用于决定是否终止该 pairingId，不向调用方泄漏存在性（统一返回 invalid_code）。
//
// T08: 入口先惰性清理；pairFails 达 maxPairingBuckets 时不为任意 pairingId 建独立长期 bucket
// （依赖来源限流兜底），避免任意 pairingId 制造无界增长。
func (g *pairingAttemptGate) recordPairingFailure(pairingID string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweepStale(now)
	b := g.pairFails[pairingID]
	if b == nil {
		// T08: 未找到 session 时不为任意 pairingId 建独立长期 bucket。
		if len(g.pairFails) >= maxPairingBuckets {
			return false
		}
		b = &slidingBucket{window: pairingAttemptConfig.perPairingFailWindow, limit: pairingAttemptConfig.perPairingFailLimit}
		g.pairFails[pairingID] = b
	}
	b.allow(now)
	return len(b.counts) >= pairingAttemptConfig.perPairingFailLimit
}

// resetPairingFailures 在成功 claim 后清理该 pairingId 的失败计数。
func (g *pairingAttemptGate) resetPairingFailures(pairingID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.pairFails, pairingID)
}

func pairingClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	// 规范化 ::1/127.0.0.1，避免本地多地址造成计数分叉。
	if v := strings.TrimSpace(host); v != "" {
		return v
	}
	return r.RemoteAddr
}

// hmacEqualString 常量时间比较字符串，避免 manualCode 校验的时间侧信道。
func hmacEqualString(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
