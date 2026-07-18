package gobridge

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeEventClock struct{ now time.Time }

func (c *fakeEventClock) Now() time.Time          { return c.now }
func (c *fakeEventClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func bufferedMessage(epoch, backend, session, event string, seq int, replayable bool, data interface{}) EventMessage {
	return EventMessage{Type: "event", EventID: fmt.Sprintf("%s:%d", epoch, seq), Seq: seq, BridgeEpoch: epoch, BackendID: backend, SessionID: session, Event: event, Data: data, Replayable: replayable, Timestamp: int64(seq)}
}

func TestEventBufferReplaysCompleteOrderedInterval(t *testing.T) {
	b := NewEventBuffer(EventBufferConfig{})
	for seq := 1; seq <= 3; seq++ {
		b.Append(bufferedMessage("e", "codex", "s", "turn_started", seq, true, seq))
	}
	result := b.Replay("codex", "s", BridgeSessionCut{EventID: "e:1", Seq: 1})
	if result.Disposition != ReplayAvailable || len(result.Events) != 2 || result.Events[0].Seq != 2 || result.Events[1].Seq != 3 || result.Through.Seq != 3 {
		t.Fatalf("unexpected replay: %+v", result)
	}
}

func TestEventBufferNonReplayableTombstoneRequiresSnapshot(t *testing.T) {
	b := NewEventBuffer(EventBufferConfig{})
	b.Append(bufferedMessage("e", "codex", "s", "text_delta", 2, false, map[string]interface{}{"delta": "secret body"}))
	result := b.Replay("codex", "s", BridgeSessionCut{EventID: "e:1", Seq: 1})
	if result.Disposition != ReplaySnapshotRequired || result.Reason != "non_replayable_gap" {
		t.Fatalf("unexpected result: %+v", result)
	}
	record := b.RecordsForTesting()[0]
	if record.payloadBytes != 0 || record.chargedBytes < eventTombstoneMetadataCost || record.message.Data != nil {
		t.Fatalf("tombstone accounting/content = %+v", record)
	}
}

func TestEventBufferEventCapEvictionMakesCoverageConservative(t *testing.T) {
	b := NewEventBuffer(EventBufferConfig{MaxEvents: 2, MaxBytes: 1 << 20, TTL: time.Hour})
	for seq := 1; seq <= 3; seq++ {
		b.Append(bufferedMessage("e", "codex", "s", "turn_started", seq, true, nil))
	}
	if events, _ := b.Stats(); events != 2 {
		t.Fatalf("events=%d", events)
	}
	result := b.Replay("codex", "s", BridgeSessionCut{Seq: 0})
	if result.Disposition != ReplaySnapshotRequired || result.Reason != "coverage_evicted" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestEventBufferByteCapUsesSerializedSize(t *testing.T) {
	probe := bufferedMessage("e", "codex", "s", "turn_started", 1, true, strings.Repeat("x", 200))
	wide := NewEventBuffer(EventBufferConfig{MaxEvents: 10, MaxBytes: 1 << 20, TTL: time.Hour})
	wide.Append(probe)
	_, charge := wide.Stats()
	b := NewEventBuffer(EventBufferConfig{MaxEvents: 10, MaxBytes: charge + 8, TTL: time.Hour})
	b.Append(probe)
	probe.Seq, probe.EventID = 2, "e:2"
	b.Append(probe)
	if events, bytes := b.Stats(); events != 1 || bytes > charge+8 {
		t.Fatalf("events=%d bytes=%d charge=%d", events, bytes, charge)
	}
}

func TestEventBufferTTLBoundaryUsesInjectedClock(t *testing.T) {
	clock := &fakeEventClock{now: time.Unix(100, 0)}
	b := NewEventBuffer(EventBufferConfig{TTL: 10 * time.Second, Now: clock.Now})
	b.Append(bufferedMessage("e", "codex", "s", "turn_started", 1, true, nil))
	clock.Advance(10*time.Second - time.Nanosecond)
	if events, _ := b.Stats(); events != 1 {
		t.Fatalf("evicted before TTL: %d", events)
	}
	if result := b.Replay("codex", "s", BridgeSessionCut{}); result.Disposition != ReplayAvailable {
		t.Fatalf("before boundary: %+v", result)
	}
	clock.Advance(time.Nanosecond)
	if result := b.Replay("codex", "s", BridgeSessionCut{}); result.Disposition != ReplaySnapshotRequired {
		t.Fatalf("at boundary: %+v", result)
	}
}

func TestEventBufferTombstonesCountAgainstEventAndByteCaps(t *testing.T) {
	b := NewEventBuffer(EventBufferConfig{MaxEvents: 2, MaxBytes: eventTombstoneMetadataCost * 2, TTL: time.Hour})
	for seq := 1; seq <= 3; seq++ {
		b.Append(bufferedMessage("e", "codex", "s", "text_delta", seq, false, "ignored"))
	}
	if events, bytes := b.Stats(); events != 2 || bytes != eventTombstoneMetadataCost*2 {
		t.Fatalf("events=%d bytes=%d", events, bytes)
	}
}

func TestEventBufferSessionIsolation(t *testing.T) {
	b := NewEventBuffer(EventBufferConfig{})
	b.Append(bufferedMessage("e", "codex", "a", "turn_started", 1, true, nil))
	b.Append(bufferedMessage("e", "codex", "b", "turn_started", 2, true, nil))
	result := b.Replay("codex", "a", BridgeSessionCut{})
	if len(result.Events) != 1 || result.Events[0].SessionID != "a" || result.Through.Seq != 1 {
		t.Fatalf("unexpected replay: %+v", result)
	}
}

func TestEventBufferUnknownSessionKeepsConfirmedCursor(t *testing.T) {
	b := NewEventBuffer(EventBufferConfig{})
	cursor := BridgeSessionCut{EventID: "old:9", Seq: 9}
	result := b.Replay("codex", "missing", cursor)
	if result.Disposition != ReplayAvailable || result.Through != cursor || len(result.Events) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestEventBufferRejectsCursorAheadOfKnownHWM(t *testing.T) {
	b := NewEventBuffer(EventBufferConfig{})
	b.Append(bufferedMessage("e", "codex", "s", "turn_started", 2, true, nil))
	result := b.Replay("codex", "s", BridgeSessionCut{Seq: 3})
	if result.Disposition != ReplaySnapshotRequired || result.Reason != "cursor_ahead" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestEventBufferRebindMigratesRecordsAndCoverage(t *testing.T) {
	b := NewEventBuffer(EventBufferConfig{MaxEvents: 1, MaxBytes: 1 << 20, TTL: time.Hour})
	b.Append(bufferedMessage("e", "claude", "pending", "turn_started", 1, true, nil))
	b.Append(bufferedMessage("e", "claude", "pending", "turn_completed", 2, true, nil))
	b.Rebind("claude", "pending", "real")
	result := b.Replay("claude", "real", BridgeSessionCut{})
	if result.Disposition != ReplaySnapshotRequired || result.Through.Seq != 2 {
		t.Fatalf("unexpected rebound coverage: %+v", result)
	}
	if old := b.Replay("claude", "pending", BridgeSessionCut{}); old.Through.Seq != 0 {
		t.Fatalf("old key retained state: %+v", old)
	}
}

func TestEventPublisherAppendsExactlyOneRecordPerLogicalEvent(t *testing.T) {
	p := NewEventPublisher("epoch-buffer")
	p.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s", Event: "turn_started"})
	p.PublishLogical(LogicalEvent{BackendID: "codex", SessionID: "s", Event: "text_delta", Data: map[string]interface{}{"delta": "x"}})
	if events, _ := p.EventBuffer().Stats(); events != 2 {
		t.Fatalf("buffer events=%d", events)
	}
	if got := p.EventBuffer().Replay("codex", "s", BridgeSessionCut{}); got.Disposition != ReplaySnapshotRequired {
		t.Fatalf("unexpected replay: %+v", got)
	}
}

func BenchmarkEventBufferAppendHeap(b *testing.B) {
	buffer := NewEventBuffer(EventBufferConfig{MaxEvents: b.N + 1, MaxBytes: 1 << 30, TTL: time.Hour})
	message := bufferedMessage("benchmark-epoch", "codex", "session", "turn_started", 1, true, map[string]interface{}{"value": strings.Repeat("x", 256)})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		message.Seq = i + 1
		message.EventID = fmt.Sprintf("benchmark-epoch:%d", i+1)
		buffer.Append(message)
	}
}
