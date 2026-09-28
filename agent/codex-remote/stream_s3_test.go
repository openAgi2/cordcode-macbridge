package codexremote

// stream_s3_test.go covers the S-3 (disconnect-resilience plan §3.3) inbound
// (seq, segment) cursor: replay dedup, chunk-segment progression (F-1),
// pong-shared cursor with no false gaps (F-R8-1), wedge-scenario pong acking
// (F-R9-1), chunk acks carrying SegmentID (E-12a), gap fan-out firing once
// per new gap (R2-A1), and the attempt-011 live-wire finding that sentinel
// frames with retro seqs still open the breaker.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// s3Harness wires a Stream against a loopback host side and collects the
// client→host ack frames in the background.
type s3Harness struct {
	stream *Stream
	host   FrameConn
	recv   chan []byte
	acks   chan Envelope
}

func newS3Harness(t *testing.T) *s3Harness {
	t.Helper()
	clientConn, hostConn := LoopbackPair()
	t.Cleanup(func() { hostConn.Close() })
	stream := NewStream(clientConn, "c1", "e1", "s1")
	t.Cleanup(func() { stream.Close() })
	h := &s3Harness{
		stream: stream,
		host:   hostConn,
		recv:   make(chan []byte, 64),
		acks:   make(chan Envelope, 64),
	}
	go func() {
		for {
			env, err := hostConn.Read()
			if err != nil {
				return
			}
			if env.Type == typeAck {
				h.acks <- env
			}
		}
	}()
	go func() {
		for {
			payload, err := stream.Recv()
			if err != nil {
				return
			}
			h.recv <- payload
		}
	}()
	return h
}

func (h *s3Harness) writeMsg(seq uint64, payload string) error {
	return h.host.Write(Envelope{
		Type: typeServerMessage, ClientID: "c1", EnvID: "e1", StreamID: "s1",
		SeqID: &seq, Message: json.RawMessage(payload),
	})
}

func (h *s3Harness) writeChunk(seq uint64, seg, count int, body string) error {
	b64 := base64.StdEncoding.EncodeToString([]byte(body))
	size := count * len(body)
	return h.host.Write(Envelope{
		Type: typeServerMessageChunk, ClientID: "c1", EnvID: "e1", StreamID: "s1",
		SeqID: &seq, SegmentID: &seg, SegmentCount: &count,
		MessageSizeBytes: &size, MessageChunkBase64: b64,
	})
}

func (h *s3Harness) writePong(seq uint64) error {
	return h.host.Write(Envelope{
		Type: typePong, ClientID: "c1", EnvID: "e1", StreamID: "s1",
		SeqID: &seq, Status: "active",
	})
}

func waitAck(t *testing.T, h *s3Harness, within time.Duration) Envelope {
	t.Helper()
	select {
	case env := <-h.acks:
		return env
	case <-time.After(within):
		t.Fatal("expected an outbound ack, got none")
		return Envelope{}
	}
}

func assertNoAck(t *testing.T, h *s3Harness, within time.Duration, what string) {
	t.Helper()
	select {
	case env := <-h.acks:
		t.Fatalf("unexpected ack for %s: seq=%v seg=%v", what, env.SeqID, env.SegmentID)
	case <-time.After(within):
	}
}

// Replay of an old plain seq: never re-delivered, but re-acked idempotently.
func TestStreamS3DuplicatePlainSkippedAndReAcked(t *testing.T) {
	h := newS3Harness(t)
	if err := h.writeMsg(1, `{"n":1}`); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-h.recv:
		if string(got) != `{"n":1}` {
			t.Fatalf("payload = %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first delivery missing")
	}
	first := waitAck(t, h, 2*time.Second)
	if first.SeqID == nil || *first.SeqID != 1 || first.SegmentID != nil {
		t.Fatalf("plain ack = %+v", first)
	}
	if err := h.writeMsg(1, `{"n":1}`); err != nil { // replay
		t.Fatal(err)
	}
	replayAck := waitAck(t, h, 2*time.Second) // re-acked, idempotent clear
	if replayAck.SeqID == nil || *replayAck.SeqID != 1 {
		t.Fatalf("replay ack = %+v", replayAck)
	}
	select {
	case got := <-h.recv:
		t.Fatalf("replayed envelope re-delivered: %s", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// F-1 negative: same seq, higher segment is the NEXT chunk of the same
// message — must be accepted; replaying a lower segment is dropped.
func TestStreamS3ChunkSegmentProgression(t *testing.T) {
	h := newS3Harness(t)
	if err := h.writeChunk(7, 0, 2, "part0|"); err != nil {
		t.Fatal(err)
	}
	ack0 := waitAck(t, h, 2*time.Second)
	if ack0.SegmentID == nil || *ack0.SegmentID != 0 {
		t.Fatalf("chunk ack must carry segment_id: %+v", ack0)
	}
	if err := h.writeChunk(7, 0, 2, "part0|"); err != nil { // replay seg 0
		t.Fatal(err)
	}
	replayAck := waitAck(t, h, 2*time.Second) // replay is re-acked idempotently
	if replayAck.SegmentID == nil || *replayAck.SegmentID != 0 {
		t.Fatalf("replay re-ack = %+v", replayAck)
	}
	if err := h.writeChunk(7, 1, 2, "part1"); err != nil { // next segment
		t.Fatal(err)
	}
	select {
	case got := <-h.recv:
		if string(got) != "part0|part1" {
			t.Fatalf("reassembled = %q, want part0|part1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second segment dropped — reassembly never completed (F-1)")
	}
	ack1 := waitAck(t, h, 2*time.Second)
	if ack1.SegmentID == nil || *ack1.SegmentID != 1 {
		t.Fatalf("second chunk ack segment = %v, want 1", ack1.SegmentID)
	}
}

// F-R8-1 closure: message N → pong N+1 → message N+2 must NOT produce a
// false gap; the pong advances the shared cursor and gets acked.
func TestStreamS3PongInterleaveNoFalseGap(t *testing.T) {
	h := newS3Harness(t)
	gaps := 0
	h.stream.onEnvelopeGap = func() { gaps++ }
	if err := h.writeMsg(1, `{"n":1}`); err != nil {
		t.Fatal(err)
	}
	if err := h.writePong(2); err != nil {
		t.Fatal(err)
	}
	if err := h.writeMsg(3, `{"n":3}`); err != nil {
		t.Fatal(err)
	}
	waitAck(t, h, 2*time.Second) // ack(seq=1)
	pongAck := waitAck(t, h, 2*time.Second)
	if pongAck.SeqID == nil || *pongAck.SeqID != 2 || pongAck.SegmentID != nil {
		t.Fatalf("pong must be acked with its seq and no segment: %+v", pongAck)
	}
	waitAck(t, h, 2*time.Second) // ack(seq=3)
	if gaps != 0 {
		t.Fatalf("false gap on pong interleave: %d (F-R8-1)", gaps)
	}
}

// F-R9-1 closure: with ONLY pongs flowing (wedged backend), every pong is
// acked on arrival — un-acked in-flight pongs stay ≤1 unconditionally.
func TestStreamS3WedgePongOnlyAcksEachPong(t *testing.T) {
	h := newS3Harness(t)
	for seq := uint64(1); seq <= 6; seq++ {
		if err := h.writePong(seq); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[uint64]bool{}
	for i := 0; i < 6; i++ {
		env := waitAck(t, h, 2*time.Second)
		if env.SeqID == nil {
			t.Fatalf("pong ack without seq: %+v", env)
		}
		if *env.SeqID != uint64(i+1) {
			t.Fatalf("pong ack order broken: got seq %d at position %d", *env.SeqID, i)
		}
		seen[*env.SeqID] = true
	}
	assertNoAck(t, h, 300*time.Millisecond, "excess pong acks")
	if len(seen) != 6 {
		t.Fatalf("acked pongs = %d, want 6", len(seen))
	}
}

// E-12a: every chunk ack carries its segment_id (no (seq,MAX) early clear).
func TestStreamS3ChunkAckCarriesSegmentID(t *testing.T) {
	h := newS3Harness(t)
	if err := h.writeChunk(9, 0, 3, "aa"); err != nil {
		t.Fatal(err)
	}
	if err := h.writeChunk(9, 1, 3, "bb"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		env := waitAck(t, h, 2*time.Second)
		if env.SegmentID == nil || *env.SegmentID != i {
			t.Fatalf("chunk ack %d missing segment_id: %+v", i, env)
		}
	}
}

// R2-A1: one gap fires the fan-out exactly once; the cursor advances past
// the gap so subsequent frames do not re-trigger; a NEW gap fires again.
func TestStreamS3GapFiresFanOutOncePerNewGap(t *testing.T) {
	h := newS3Harness(t)
	gaps := 0
	h.stream.onEnvelopeGap = func() { gaps++ }
	if err := h.writeMsg(1, `{"n":1}`); err != nil {
		t.Fatal(err)
	}
	if err := h.writeMsg(5, `{"n":5}`); err != nil { // gap 2..4
		t.Fatal(err)
	}
	if err := h.writeMsg(6, `{"n":6}`); err != nil {
		t.Fatal(err)
	}
	if err := h.writeMsg(9, `{"n":9}`); err != nil { // new gap 7..8
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for gaps < 2 {
		select {
		case <-deadline:
			t.Fatalf("gap fan-out fired %d times, want 2", gaps)
		case <-time.After(20 * time.Millisecond):
		}
	}
	time.Sleep(200 * time.Millisecond)
	if gaps != 2 {
		t.Fatalf("gap fan-out fired %d times, want exactly 2 (once per new gap)", gaps)
	}
}

// Attempt-011 live-wire finding: the relay's sentinel errors arrive with
// retro seqs overlapping already-delivered frames. The sentinel must still
// open the breaker even though the dedup layer drops the frame.
func TestStreamS3SentinelRetroSeqStillOpensBreaker(t *testing.T) {
	h := newS3Harness(t)
	for seq := uint64(1); seq <= 5; seq++ {
		if err := h.writeMsg(seq, fmt.Sprintf(`{"n":%d}`, seq)); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := `{"jsonrpc":"2.0","method":"error","params":{"error":{"message":"Unexpected ack message received from client"},"willRetry":false,"threadId":"__remote_control_transport__","turnId":"__remote_control_transport__"}}`
	if err := h.writeMsg(3, sentinel); err != nil { // retro seq
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		state, _, sentinels := h.stream.AckBreakerSnapshot()
		if state == ackStateOpen && sentinels >= 1 {
			return
		}
		select {
		case <-deadline:
			state, probes, sentinels := h.stream.AckBreakerSnapshot()
			t.Fatalf("breaker state=%q probes=%d sentinels=%d — retro-seq sentinel did not open it (attempt-011)", state, probes, sentinels)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Bounded fan-out: observed threads with an in-flight codec turn join the
// reconcile pending set; observed threads without one stay out.
func TestSignalInboundGapReconcileFansInFlightObserved(t *testing.T) {
	a := &Agent{}
	a.codec = NewLiveCodec()
	a.codec.setActiveTurn("th-a", "turn-a")
	a.codec.setActiveTurn("th-b", "turn-b")
	a.listeners = map[string]map[chan core.Event]struct{}{
		"th-a": {}, "th-b": {}, "th-c": {},
	}
	a.signalInboundGapReconcile()
	pending := a.PendingTurnReconciles()
	if len(pending) != 2 {
		t.Fatalf("pending = %v, want th-a+th-b only", pending)
	}
	set := map[string]bool{}
	for _, id := range pending {
		set[id] = true
	}
	if !set["th-a"] || !set["th-b"] || set["th-c"] {
		t.Fatalf("pending set wrong: %v (th-c has no in-flight turn)", pending)
	}
}
