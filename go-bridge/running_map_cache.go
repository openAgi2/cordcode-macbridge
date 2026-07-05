package gobridge

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// runningMapCacheTTL is the freshness window for the cached Claude running map.
// 2s collapses iOS list_sessions refresh bursts (which can fire several times per
// second) while keeping UI state responsive and bounding staleness for Claude
// turns launched outside MacBridge to one TTL window.
const runningMapCacheTTL = 2 * time.Second

// runningMapCache memoises GetRunningSessionIDs for the Claude agent.
// GetRunningSessionIDs already reads active process state across all Claude
// sessions, so a single global entry per Handlers is correct — there is no
// per-session key.
//
// Correctness has two layers:
//   - TTL expiry (runningMapCacheTTL): bounds staleness for externally-launched
//     turns where no explicit invalidation fires (e.g. a Claude turn started from
//     another Terminal). The turn is reflected within one TTL window.
//   - Explicit invalidation via invalidate(): the sessionRegistry state-change
//     callback drops the cached entry whenever a MacBridge-tracked Claude session
//     transitions (send_message / turn_completed / abort / process exit), so
//     owned turns are reflected on the next list_sessions immediately.
//
// This is not a background scanner — it refreshes only on demand from the list
// path, so it cannot itself become a CPU source. The "live-but-idle Claude
// process must not be marked running" semantics are preserved because the cache
// stores the precise output of GetRunningSessionIDs verbatim.
type runningMapCache struct {
	mu         sync.Mutex
	cached     map[string]bool
	computedAt time.Time
	ttl        time.Duration

	// recompute is the underlying lookup (the Claude agent's GetRunningSessionIDs).
	// Invoked at most once per TTL window. Stored as a field so the production
	// wiring binds it to the registered Claude agent and tests can inject a
	// counting variant without a real agent.
	recompute func(ctx context.Context) (map[string]bool, error)

	// hits counts cache satisfactions; recomputes counts recompute invocations.
	// Atomic for race-free test assertions; informational only.
	hits      atomic.Int64
	recomputes atomic.Int64
}

func newRunningMapCache(recompute func(ctx context.Context) (map[string]bool, error)) *runningMapCache {
	return &runningMapCache{ttl: runningMapCacheTTL, recompute: recompute}
}

// newRunningMapCacheWithTTL is the test constructor: same cache with an explicit
// TTL so expiry can be exercised deterministically without real time pressure.
func newRunningMapCacheWithTTL(recompute func(ctx context.Context) (map[string]bool, error), ttl time.Duration) *runningMapCache {
	return &runningMapCache{ttl: ttl, recompute: recompute}
}

// get returns the cached running map if it is fresh, otherwise recomputes it.
// A nil cache or nil recompute returns nil (callers fall back to registry state).
// On recompute error the previous still-fresh entry (if any) is retained so a
// transient GetRunningSessionIDs failure does not wipe good state.
func (c *runningMapCache) get(ctx context.Context) map[string]bool {
	if c == nil || c.recompute == nil {
		return nil
	}
	c.mu.Lock()
	if c.cached != nil && time.Since(c.computedAt) < c.ttl {
		m := c.cached
		c.mu.Unlock()
		c.hits.Add(1)
		return m
	}
	c.mu.Unlock()

	running, err := c.recompute(ctx)
	if err != nil || running == nil {
		c.mu.Lock()
		m := c.cached
		c.mu.Unlock()
		return m
	}

	c.mu.Lock()
	c.cached = running
	c.computedAt = time.Now()
	c.mu.Unlock()
	c.recomputes.Add(1)
	return running
}

// invalidate drops the cached entry. Called by the sessionRegistry state-change
// callback (wired in newHandlersWithContext) so MacBridge-tracked Claude turns
// are reflected immediately instead of after the TTL window.
func (c *runningMapCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.cached = nil
	c.computedAt = time.Time{}
	c.mu.Unlock()
}
