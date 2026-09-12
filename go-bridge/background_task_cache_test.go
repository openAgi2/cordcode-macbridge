package gobridge

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
	"time"
)

func TestBackgroundTaskCacheSingleFlightsIdenticalRevision(t *testing.T) {
	cache := newBackgroundTaskCache()
	release := make(chan struct{})
	var calls atomic.Int64
	var waiters sync.WaitGroup
	results := make([]error, 4)

	call := func(index int) {
		defer waiters.Done()
		_, err := cache.fetch(context.Background(), "codex-remote", "session", 12, func(context.Context) ([]core.BackgroundTask, error) {
			calls.Add(1)
			<-release
			return nil, nil
		})
		results[index] = err
	}
	waiters.Add(4)
	go call(0)
	for i := 1; i < 4; i++ {
		go call(i)
	}
	for {
		if calls.Load() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
		if calls.Load() > 1 {
			t.Fatalf("single-flight leader count = %d", calls.Load())
		}
	}
	close(release)
	waiters.Wait()
	if calls.Load() != 1 {
		t.Fatalf("scan calls = %d, want 1", calls.Load())
	}
	for _, err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackgroundTaskCacheCachesSuccessAndRetriesError(t *testing.T) {
	cache := newBackgroundTaskCache()
	calls := 0
	scan := func(context.Context) ([]core.BackgroundTask, error) {
		calls++
		return nil, nil
	}
	if _, err := cache.fetch(context.Background(), "codex-remote", "session", 20, scan); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.fetch(context.Background(), "codex-remote", "session", 20, scan); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("successful negative result was not cached: calls=%d", calls)
	}

	fail := func(context.Context) ([]core.BackgroundTask, error) { return nil, errors.New("real upstream failure") }
	if _, err := cache.fetch(context.Background(), "codex-remote", "session", 21, fail); err == nil {
		t.Fatal("first error must surface")
	}
	if _, err := cache.fetch(context.Background(), "codex-remote", "session", 21, func(context.Context) ([]core.BackgroundTask, error) {
		calls++
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("errors must not be cached as success: calls=%d", calls)
	}
}

func TestBackgroundTaskCacheUsesRevisionAndBoundedScan(t *testing.T) {
	previousTimeout := backgroundTaskScanTimeout
	backgroundTaskScanTimeout = time.Millisecond
	t.Cleanup(func() { backgroundTaskScanTimeout = previousTimeout })

	cache := newBackgroundTaskCache()
	calls := 0
	scan := func(ctx context.Context) ([]core.BackgroundTask, error) {
		calls++
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
			return nil, nil
		}
	}
	if _, err := cache.fetch(context.Background(), "codex-remote", "session", 30, scan); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded scan err = %v", err)
	}
	if _, err := cache.fetch(context.Background(), "codex-remote", "session", 31, scan); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("next revision err = %v", err)
	}
	if calls != 2 {
		t.Fatalf("revision change must not reuse result: calls=%d", calls)
	}
}
