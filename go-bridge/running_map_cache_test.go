package gobridge

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunningMapCache_BurstReusesCachedState proves the TTL cache collapses a
// refresh burst: many list_sessions requests within one TTL window share a single
// GetRunningSessionIDs call.
func TestRunningMapCache_BurstReusesCachedState(t *testing.T) {
	var calls int32
	cache := newRunningMapCache(func(ctx context.Context) (map[string]bool, error) {
		atomic.AddInt32(&calls, 1)
		return map[string]bool{"ses_a": true}, nil
	})
	for i := 0; i < 10; i++ {
		m := cache.get(context.Background())
		if !m["ses_a"] {
			t.Fatalf("burst get %d: missing ses_a", i)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("recompute called %d times across 10-request burst; want 1 (TTL cache collapse)", got)
	}
	if got := cache.hits.Load(); got != 9 {
		t.Fatalf("cache hits = %d; want 9", got)
	}
}

// TestRunningMapCache_TTLExpiryTriggersRecompute proves that after the TTL
// window the next get recomputes (preserving detection of Claude turns launched
// outside MacBridge within one TTL window).
func TestRunningMapCache_TTLExpiryTriggersRecompute(t *testing.T) {
	var calls int32
	cache := newRunningMapCacheWithTTL(func(ctx context.Context) (map[string]bool, error) {
		atomic.AddInt32(&calls, 1)
		return map[string]bool{"ses_a": true}, nil
	}, 20*time.Millisecond)

	cache.get(context.Background())
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("cold recompute calls = %d; want 1", got)
	}
	time.Sleep(35 * time.Millisecond)
	cache.get(context.Background())
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("after TTL expiry recompute calls = %d; want 2", got)
	}
}

// TestRunningMapCache_InvalidateForcesRecompute proves explicit invalidation
// (wired to the sessionRegistry state-change callback) drops the cached entry so
// the next get recomputes immediately, regardless of TTL.
func TestRunningMapCache_InvalidateForcesRecompute(t *testing.T) {
	var calls int32
	cache := newRunningMapCacheWithTTL(func(ctx context.Context) (map[string]bool, error) {
		atomic.AddInt32(&calls, 1)
		return map[string]bool{"ses_a": true}, nil
	}, time.Hour) // long TTL so only invalidate() can force a recompute

	cache.get(context.Background())
	cache.invalidate()
	cache.get(context.Background())
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("recompute calls after invalidate = %d; want 2", got)
	}
}

// TestRunningMapCache_NilCacheAndNilRecomputeReturnNil documents the fallback
// contract: callers (applyListRuntimeState) fall back to registry state when the
// running map is nil.
func TestRunningMapCache_NilCacheAndNilRecomputeReturnNil(t *testing.T) {
	var nilCache *runningMapCache
	if got := nilCache.get(context.Background()); got != nil {
		t.Fatalf("nil cache get = %v, want nil", got)
	}
	empty := newRunningMapCache(nil)
	if got := empty.get(context.Background()); got != nil {
		t.Fatalf("nil-recompute get = %v, want nil", got)
	}
}
