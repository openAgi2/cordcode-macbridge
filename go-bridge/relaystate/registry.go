package relaystate

import (
	"fmt"
	"time"
)

// Registry lifecycle（plan §3.6.4 双端 correlation registry）：active → retired，put-if-absent，
// duplicate/reuse 拒绝，bounded retired 集合。A-1 proof model（双端真实 map 是 R1）。
//
// server 顺序：strict envelope/capability → auth/scope → 联合预留 registry + worker quota →
// active put-if-absent → dispatch。任一 quota 不足不留 active 项，返回 file.read_busy。

// CorrelatedRegistry 是单端 correlation registry 的模型（active + retired 集合 + caps）。
type CorrelatedRegistry struct {
	active       map[string]bool
	retired      map[string]bool
	maxActive    int
	maxRetired   int
}

func NewCorrelatedRegistry(maxActive, maxRetired int) *CorrelatedRegistry {
	return &CorrelatedRegistry{
		active: make(map[string]bool), retired: make(map[string]bool),
		maxActive: maxActive, maxRetired: maxRetired,
	}
}

// PutIfAbsent：correlation key 原子登记。返回 (admitted, reason)。
//   - 已在 active => false, "already_active"（duplicate）
//   - 已在 retired => false, "reuse"（reuse 窗口内禁止复用；close generation）
//   - active 满 => false, "busy"（quota 不足，返回 file.read_busy）
//   - 否则登记，true, "admitted"
func (r *CorrelatedRegistry) PutIfAbsent(key string) (bool, string) {
	if r.active[key] {
		return false, "already_active"
	}
	if r.retired[key] {
		return false, "reuse"
	}
	if len(r.active) >= r.maxActive {
		return false, "busy"
	}
	r.active[key] = true
	return true, "admitted"
}

// Retire：把 active key 移到 retired 集合（response commit / chunked complete / cancel / error）。
// retired 只能 close（不静默 LRU）；超 maxRetired => false（调用方 close generation）。
func (r *CorrelatedRegistry) Retire(key string) bool {
	if !r.active[key] {
		return false
	}
	delete(r.active, key)
	if len(r.retired) >= r.maxRetired {
		return false // retired overflow -> 调用方 close transport generation
	}
	r.retired[key] = true
	return true
}

func (r *CorrelatedRegistry) ActiveCount() int    { return len(r.active) }
func (r *CorrelatedRegistry) RetiredCount() int   { return len(r.retired) }

// ── Deadline / clock 不变式（plan §3.6.4 阶段数值）─────────────────────────────
// server group max age 120s > client total cap 90s > pre-first-chunk idle 30s > inter-chunk idle 15s。
// absolute group deadline = index0Commit + 120s（不因 progress 滑动）。
const (
	ServerGroupMaxAge   = 120 * time.Second
	ClientTotalCap      = 90 * time.Second
	PreFirstChunkIdle   = 30 * time.Second
	InterChunkIdle      = 15 * time.Second
	AbsoluteGroupWindow = 120 * time.Second // index0Commit + 此值
)

// CheckDeadlineInvariants 校验阶段数值不变式（A0 冻结值；任一被破坏即配置 gate 失败）。
func CheckDeadlineInvariants() error {
	if !(ServerGroupMaxAge > ClientTotalCap && ClientTotalCap > PreFirstChunkIdle && PreFirstChunkIdle > InterChunkIdle) {
		return fmt.Errorf("deadline invariant violated: server=%v client=%v prefirst=%v interchunk=%v (need 120>90>30>15)",
			ServerGroupMaxAge, ClientTotalCap, PreFirstChunkIdle, InterChunkIdle)
	}
	return nil
}

// FakeClock 用于 deadline/fake-clock 测试（不依赖 wall clock）。
type FakeClock struct{ now time.Time }

func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t} }
func (c *FakeClock) Now() time.Time        { return c.now }
func (c *FakeClock) Advance(d time.Duration) time.Time {
	c.now = c.now.Add(d)
	return c.now
}

// PreFirstDeadline：request commit 后到 index0 的 deadline。
func PreFirstDeadline(requestCommit, now time.Time) bool {
	return now.Sub(requestCommit) >= PreFirstChunkIdle
}

// InterChunkExceeded：index0 后unfinished group 的 inter-chunk idle。
func InterChunkExceeded(lastChunkAt, now time.Time) bool {
	return now.Sub(lastChunkAt) >= InterChunkIdle
}

// ClientTotalExceeded：client 端 total cap（request commit 起计）。
func ClientTotalExceeded(requestCommit, now time.Time) bool {
	return now.Sub(requestCommit) >= ClientTotalCap
}

// ServerGroupExpired：server admission 到 index0/单帧 commit 的 max age。
func ServerGroupExpired(serverAdmitAt, now time.Time) bool {
	return now.Sub(serverAdmitAt) >= ServerGroupMaxAge
}
