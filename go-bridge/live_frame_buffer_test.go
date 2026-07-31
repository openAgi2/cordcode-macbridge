package gobridge

import (
	"testing"
	"time"
)

func TestLiveFrameBufferAppendAndSnapshot(t *testing.T) {
	b := NewLiveFrameBuffer()
	// freeze time via injecting now after construction is awkward; use real time + short maxAge
	b.mu.Lock()
	b.maxAge = time.Hour
	b.now = func() time.Time { return time.Unix(1000, 0) }
	b.mu.Unlock()

	msg1 := EventMessage{Type: "event", Event: "reasoning_delta", BackendID: "codex", SessionID: "s1", Seq: 1, PerSessionSeq: 1}
	msg2 := EventMessage{Type: "event", Event: "text_delta", BackendID: "codex", SessionID: "s1", Seq: 2, PerSessionSeq: 2}
	b.Append([]string{"dev_a"}, msg1)
	b.Append([]string{"dev_a"}, msg2)

	if n := b.FrameCount("dev_a"); n != 2 {
		t.Fatalf("FrameCount = %d, want 2", n)
	}
	frames := b.Snapshot("dev_a")
	if len(frames) != 2 {
		t.Fatalf("Snapshot len = %d, want 2", len(frames))
	}
	if frames[0].Event != "reasoning_delta" || frames[1].Event != "text_delta" {
		t.Fatalf("frames = %+v", frames)
	}
	// Idempotent snapshot
	if n := len(b.Snapshot("dev_a")); n != 2 {
		t.Fatalf("second Snapshot len = %d", n)
	}
}

func TestLiveFrameBufferIgnoresNonLiveEvents(t *testing.T) {
	b := NewLiveFrameBuffer()
	b.Append([]string{"dev_a"}, EventMessage{Event: "turn_completed", BackendID: "codex", SessionID: "s1"})
	if b.FrameCount("dev_a") != 0 {
		t.Fatal("durable milestone must not enter live buffer")
	}
}

func TestLiveFrameBufferTrimsByMaxFrames(t *testing.T) {
	b := NewLiveFrameBuffer()
	b.mu.Lock()
	b.maxN = 3
	b.maxAge = time.Hour
	b.now = func() time.Time { return time.Unix(1000, 0) }
	b.mu.Unlock()
	for i := 1; i <= 5; i++ {
		b.Append([]string{"dev_a"}, EventMessage{
			Event: "text_delta", BackendID: "codex", SessionID: "s1", Seq: i, PerSessionSeq: i,
		})
	}
	if n := b.FrameCount("dev_a"); n != 3 {
		t.Fatalf("FrameCount = %d, want 3 after cap", n)
	}
	frames := b.Snapshot("dev_a")
	if frames[0].Seq != 3 || frames[2].Seq != 5 {
		t.Fatalf("expected oldest dropped, got seqs %d..%d", frames[0].Seq, frames[2].Seq)
	}
}

func TestLiveFrameBufferExpiresByAge(t *testing.T) {
	b := NewLiveFrameBuffer()
	t0 := time.Unix(1000, 0)
	b.mu.Lock()
	b.maxAge = 10 * time.Second
	b.now = func() time.Time { return t0 }
	b.mu.Unlock()
	b.Append([]string{"dev_a"}, EventMessage{Event: "text_delta", BackendID: "codex", SessionID: "s1", Seq: 1})
	b.mu.Lock()
	b.now = func() time.Time { return t0.Add(30 * time.Second) }
	b.mu.Unlock()
	b.gcOnce()
	if b.FrameCount("dev_a") != 0 {
		t.Fatal("expired frames should be GC'd")
	}
}

func TestIsLiveBufferableEvent(t *testing.T) {
	if !isLiveBufferableEvent("reasoning_delta") || !isLiveBufferableEvent("tool_started") {
		t.Fatal("expected live deltas bufferable")
	}
	if isLiveBufferableEvent("turn_completed") {
		t.Fatal("turn_completed must stay on durable outbox path only")
	}
}
