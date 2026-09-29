package codexremote

// stream_s7_test.go covers the S-7 (disconnect-resilience plan §3.7) ack
// breaker state machine against the plan's eight assertions, using the
// nowFunc fake clock. Closed-state byte-identity (assertion ①) is covered by
// the pre-existing sentinel/ordinary-error tests in stream_test.go.

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

type s7Harness struct {
	stream *Stream
	host   FrameConn
	acks   chan Envelope
	clock  time.Time
}

func newS7Harness(t *testing.T) *s7Harness {
	t.Helper()
	clientConn, hostConn := LoopbackPair()
	t.Cleanup(func() { hostConn.Close() })
	stream := NewStream(clientConn, "c1", "e1", "s1")
	t.Cleanup(func() { stream.Close() })
	h := &s7Harness{
		stream: stream,
		host:   hostConn,
		acks:   make(chan Envelope, 64),
		clock:  time.Unix(1700000000, 0),
	}
	stream.nowFunc = func() time.Time { return h.clock }
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
	return h
}

func (h *s7Harness) advance(d time.Duration) { h.clock = h.clock.Add(d) }

func (h *s7Harness) writeMsg(seq uint64) error {
	s := strconv.FormatUint(seq, 10)
	return h.host.Write(Envelope{
		Type: typeServerMessage, ClientID: "c1", EnvID: "e1", StreamID: "s1",
		SeqID: &seq, Message: json.RawMessage(`{"n":` + s + `}`),
	})
}

func (h *s7Harness) writePong(seq uint64) error {
	return h.host.Write(Envelope{
		Type: typePong, ClientID: "c1", EnvID: "e1", StreamID: "s1",
		SeqID: &seq, Status: "active",
	})
}

func (h *s7Harness) writeSentinel(seq uint64) error {
	return h.host.Write(Envelope{
		Type: typeServerMessage, ClientID: "c1", EnvID: "e1", StreamID: "s1",
		SeqID:   &seq,
		Message: json.RawMessage(`{"jsonrpc":"2.0","method":"error","params":{"error":{"message":"Unexpected ack message received from client"},"willRetry":false,"threadId":"__remote_control_transport__","turnId":"__remote_control_transport__"}}`),
	})
}

func (h *s7Harness) drainAcks() []Envelope {
	var out []Envelope
	for {
		select {
		case env := <-h.acks:
			out = append(out, env)
		default:
			return out
		}
	}
}

// ⑦ A fresh stream starts closed.
func TestStreamS7FreshStreamIsClosed(t *testing.T) {
	h := newS7Harness(t)
	state, probes, sentinels := h.stream.AckBreakerSnapshot()
	if state != "closed" || probes != 0 || sentinels != 0 {
		t.Fatalf("fresh stream = %q/%d/%d, want closed/0/0", state, probes, sentinels)
	}
	if h.stream.AcksDisabled() {
		t.Fatal("fresh stream must ack")
	}
}

// ② open + age ≥ T + first frame → exactly one probe carrying the highest
// seen (seq, segment) cursor; the frame itself is NOT acked. The trigger
// frame is a chunk so the probe's segment mark is observable: the cursor at
// probe time is (trigger seq, segment 0).
func TestStreamS7ProbeCarriesHighestCursor(t *testing.T) {
	h := newS7Harness(t)
	// Prime the cursor with a fully consumed 3-segment chunk message.
	if err := h.writeChunkThroughHelper(1, 3); err != nil {
		t.Fatal(err)
	}
	waitForAcks(t, h, 3)
	if err := h.writeSentinel(2); err != nil { // arms the breaker (fresh seq)
		t.Fatal(err)
	}
	waitBreakerOpen(t, h)
	// Drain the acks of the priming frames.
	_ = h.drainAcks()
	h.advance(ackBreakerProbeInterval)
	if err := h.host.Write(Envelope{ // chunk probe trigger
		Type: typeServerMessageChunk, ClientID: "c1", EnvID: "e1", StreamID: "s1",
		SeqID: u64ptr(3), SegmentID: intptr(0), SegmentCount: intptr(2),
		MessageChunkBase64: encodeChunk([]byte("x")),
	}); err != nil {
		t.Fatal(err)
	}
	acks := waitForAcks(t, h, 1)
	if len(acks) != 1 {
		t.Fatalf("frames after probe trigger = %d, want exactly the one probe", len(acks))
	}
	probe := acks[0]
	if probe.SeqID == nil || *probe.SeqID != 3 {
		t.Fatalf("probe cursor seq = %v, want 3 (highest seen, incl. trigger)", probe.SeqID)
	}
	if probe.SegmentID == nil || *probe.SegmentID != 0 {
		t.Fatalf("probe must carry the chunk segment mark, got %v", probe.SegmentID)
	}
	state, probes, _ := h.stream.AckBreakerSnapshot()
	if state != ackStateHalfOpen || probes != 1 {
		t.Fatalf("state = %q probes = %d, want half-open/1", state, probes)
	}
}

// ③ Within T after a probe, ordinary frames are neither acked nor trigger a
// new probe; ④ a backlog of frames produces exactly one probe.
func TestStreamS7BacklogOneProbeNoPerEnvelopeAcks(t *testing.T) {
	h := newS7Harness(t)
	if err := h.writeMsg(1); err != nil {
		t.Fatal(err)
	}
	waitForAcks(t, h, 1)
	if err := h.writeSentinel(2); err != nil {
		t.Fatal(err)
	}
	waitBreakerOpen(t, h)
	h.advance(ackBreakerProbeInterval)
	for seq := uint64(3); seq <= 12; seq++ { // 10-frame backlog
		if err := h.writeMsg(seq); err != nil {
			t.Fatal(err)
		}
	}
	acks := waitForAcks(t, h, 1)
	if len(acks) != 1 || acks[0].SeqID == nil || *acks[0].SeqID != 3 {
		t.Fatalf("backlog acks = %+v, want exactly one probe at cursor 3 (first backlog frame)", acks)
	}
	time.Sleep(150 * time.Millisecond)
	if extra := h.drainAcks(); len(extra) != 0 {
		t.Fatalf("per-envelope acks leaked from armed breaker: %+v", extra)
	}
	_, probes, _ := h.stream.AckBreakerSnapshot()
	if probes != 1 {
		t.Fatalf("probes = %d, want 1 for the whole backlog", probes)
	}
	// ③: another frame within T → still nothing.
	if err := h.writeMsg(13); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if extra := h.drainAcks(); len(extra) != 0 {
		t.Fatalf("in-window frame acked/probed: %+v", extra)
	}
}

// ⑤ A sentinel arriving after a probe re-opens the breaker with a reset
// clock and never produces an ack storm.
func TestStreamS7SentinelAfterProbeReopens(t *testing.T) {
	h := newS7Harness(t)
	if err := h.writeMsg(1); err != nil {
		t.Fatal(err)
	}
	waitForAcks(t, h, 1)
	if err := h.writeSentinel(2); err != nil {
		t.Fatal(err)
	}
	waitBreakerOpen(t, h)
	h.advance(ackBreakerProbeInterval)
	if err := h.writeMsg(3); err != nil { // probe → half-open
		t.Fatal(err)
	}
	if acks := waitForAcks(t, h, 1); len(acks) != 1 {
		t.Fatalf("want exactly the probe, got %+v", acks)
	}
	// Sentinel while half-open → back to open, clock reset.
	if err := h.writeSentinel(4); err != nil {
		t.Fatal(err)
	}
	waitSentinelCount(t, h, 2)
	if state, _, sentinels := h.stream.AckBreakerSnapshot(); state != ackStateOpen || sentinels != 2 {
		t.Fatalf("state = %q sentinels = %d, want open/2", state, sentinels)
	}
	// Within T after the reset: frames produce nothing.
	h.advance(ackBreakerProbeInterval / 2)
	if err := h.writeMsg(5); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if extra := h.drainAcks(); len(extra) != 0 {
		t.Fatalf("ack storm after sentinel reopen: %+v", extra)
	}
}

// ⑥ Steady reject loop (probe → sentinel → T → probe…): sentinel count =
// probe count ≈ elapsed/T.
func TestStreamS7SteadyRejectLoopRateIsOnePerT(t *testing.T) {
	h := newS7Harness(t)
	if err := h.writeMsg(1); err != nil {
		t.Fatal(err)
	}
	waitForAcks(t, h, 1)
	if err := h.writeSentinel(2); err != nil {
		t.Fatal(err)
	}
	waitBreakerOpen(t, h)
	// 4 cycles: advance T, send a frame (probe), host answers with sentinel.
	seq := uint64(3)
	for cycle := 0; cycle < 4; cycle++ {
		h.advance(ackBreakerProbeInterval)
		if err := h.writeMsg(seq); err != nil {
			t.Fatal(err)
		}
		if acks := waitForAcks(t, h, 1); len(acks) != 1 {
			t.Fatalf("cycle %d: want one probe, got %+v", cycle, acks)
		}
		if err := h.writeSentinel(seq); err != nil {
			t.Fatal(err)
		}
		waitSentinelCount(t, h, 5+cycle-3) // 1 priming + cycle+1 loop sentinels
		seq++
	}
	state, probes, sentinels := h.stream.AckBreakerSnapshot()
	if state != ackStateOpen || probes != 4 || sentinels != 5 {
		t.Fatalf("state=%q probes=%d sentinels=%d, want open/4/5 (probe rate = 1/T)", state, probes, sentinels)
	}
}

// ⑧ Static-flow wedge: with no data frames, pongs drive the probe (F-R5-A3).
func TestStreamS7PongDrivesProbe(t *testing.T) {
	h := newS7Harness(t)
	if err := h.writeMsg(1); err != nil {
		t.Fatal(err)
	}
	waitForAcks(t, h, 1)
	if err := h.writeSentinel(2); err != nil {
		t.Fatal(err)
	}
	waitBreakerOpen(t, h)
	h.advance(ackBreakerProbeInterval)
	if err := h.writePong(3); err != nil { // only inbound flow
		t.Fatal(err)
	}
	acks := waitForAcks(t, h, 1)
	if len(acks) != 1 || acks[0].SeqID == nil || *acks[0].SeqID != 3 {
		t.Fatalf("pong-driven probe = %+v, want one probe at cursor 3", acks)
	}
	if acks[0].SegmentID != nil {
		t.Fatalf("pong-driven probe must not carry a segment mark: %+v", acks[0])
	}
}

func waitForAcks(t *testing.T, h *s7Harness, n int) []Envelope {
	t.Helper()
	deadline := time.After(2 * time.Second)
	var out []Envelope
	for len(out) < n {
		select {
		case env := <-h.acks:
			out = append(out, env)
		case <-deadline:
			t.Fatalf("timed out waiting for %d acks, got %+v", n, out)
		}
	}
	return out
}

// writeChunkThroughHelper feeds one full chunked message (count segments).
func (h *s7Harness) writeChunkThroughHelper(seq uint64, count int) error {
	for seg := 0; seg < count; seg++ {
		if err := h.host.Write(Envelope{
			Type: typeServerMessageChunk, ClientID: "c1", EnvID: "e1", StreamID: "s1",
			SeqID: &seq, SegmentID: &seg, SegmentCount: &count,
			MessageChunkBase64: encodeChunk([]byte("x")),
		}); err != nil {
			return err
		}
	}
	return nil
}

// waitBreakerOpen blocks until the readLoop has actually processed the
// sentinel — advancing the fake clock before that would stamp armedAt with
// the post-advance time and suppress the probe under test.
func waitBreakerOpen(t *testing.T, h *s7Harness) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if state, _, _ := h.stream.AckBreakerSnapshot(); state != "closed" {
			return
		}
		select {
		case <-deadline:
			t.Fatal("breaker never opened (sentinel unprocessed)")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func waitSentinelCount(t *testing.T, h *s7Harness, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if _, _, sentinels := h.stream.AckBreakerSnapshot(); sentinels >= want {
			return
		}
		select {
		case <-deadline:
			_, probes, sentinels := h.stream.AckBreakerSnapshot()
			t.Fatalf("sentinels = %d (probes %d), want >= %d", sentinels, probes, want)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func u64ptr(v uint64) *uint64 { return &v }
func intptr(v int) *int       { return &v }
