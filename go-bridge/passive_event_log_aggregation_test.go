package gobridge

import (
	"errors"
	"testing"
	"time"
)

func TestPassiveEventAggregatorSuppressesIdenticalStorm(t *testing.T) {
	current := time.Unix(1_000, 0)
	aggregator := newPassiveEventAggregator()
	aggregator.now = func() time.Time { return current }

	due, attrs := aggregator.observe("codex-remote", "__remote_control_transport__", "error", errors.New("remote endpoint detached"))
	if !due || attrs[0] != "reason" || attrs[1] != "first" {
		t.Fatalf("first event due=%v attrs=%v", due, attrs)
	}
	for i := 0; i < 10_000; i++ {
		current = current.Add(time.Millisecond)
		due, _ = aggregator.observe("codex-remote", "__remote_control_transport__", "error", errors.New("remote endpoint detached"))
		if due {
			t.Fatalf("identical repeat %d unexpectedly due", i)
		}
	}

	current = current.Add(passiveErrorLogInterval)
	due, attrs = aggregator.observe("codex-remote", "__remote_control_transport__", "error", errors.New("remote endpoint detached"))
	if !due {
		t.Fatal("periodic summary due")
	}
	keys := []string{"reason", "count", "ratePerSecond", "sessionFingerprint", "errorFingerprint"}
	for index, want := range keys {
		got := attrs[index*2]
		if got != want {
			t.Fatalf("attrs[%d] key = %v, want %v", index*2, got, want)
		}
	}
	if attrs[1] != "repeat" {
		t.Fatalf("reason = %v", attrs[1])
	}
}

func TestPassiveEventAggregatorLogsIdentityChangeImmediately(t *testing.T) {
	current := time.Unix(2_000, 0)
	aggregator := newPassiveEventAggregator()
	aggregator.now = func() time.Time { return current }

	due, _ := aggregator.observe("codex-remote", "session-one", "error", errors.New("first error"))
	if !due {
		t.Fatal("first error must log")
	}
	current = current.Add(time.Millisecond)
	due, _ = aggregator.observe("codex-remote", "session-one", "error", errors.New("first error"))
	if due {
		t.Fatal("identical immediate repeat should not log")
	}
	due, attrs := aggregator.observe("codex-remote", "session-one", "error", errors.New("different error"))
	if !due {
		t.Fatal("identity change must log immediately")
	}
	if attrs[1] != "error_identity_changed" {
		t.Fatalf("reason attrs = %v", attrs)
	}
}
