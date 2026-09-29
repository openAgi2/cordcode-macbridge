package codexremote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// transportErrorSentinel matches the synthetic thread ids the CLOSED-SOURCE
// remote-control relay (chatgpt.com/backend-api/wham/remote/control) stamps on
// connection-level diagnostics — e.g. "Unexpected ack message received from
// client", which the relay synthesizes as a terminal error notification for
// every client ack (producer pinned by the 2026-09-13 three-layer literal
// exclusion in codex-rs/binary/Desktop-bundle; the ack trigger pinned by the
// 2026-09-14 breaker verification: inbound fell to zero while paired and
// connected once acks stopped). See the 2026-09-14 postmortem in think.md.
// Matched as raw payload prefix-contains so the breaker stays inside the
// transport layer.
var transportErrorSentinel = []byte(`"threadId":"__remote_control_transport`)

// FrameConn is one controller WSS (or a test double) that reads/writes envelopes.
type FrameConn interface {
	Write(Envelope) error
	Read() (Envelope, error)
	Close() error
}

// Stream is one environment+stream virtual app-server Transport. JSON-RPC
// payloads go in Send/Recv; this type wraps/unwraps controller envelopes.
type Stream struct {
	conn     FrameConn
	clientID string
	envID    string
	streamID string

	mu       sync.Mutex
	nextSeq  uint64
	cursor   string
	closed   bool
	closeErr error
	// lastHostActivity only advances on evidence from the app-server connection:
	// an active pong or a server message. Relay ACKs prove only that the relay
	// accepted our frame; the official ClientTracker does not use them as proof
	// that (client_id, stream_id) still names a live app-server connection.
	lastHostActivity time.Time
	// staleStreams records old stream ids seen on this controller connection so
	// each is diagnosed once. Their payloads must never cross the stream/epoch
	// boundary: JSON-RPC request ids restart for each Client.
	staleStreams map[string]struct{}
	// S-3 (disconnect-resilience plan §3.3): inbound envelope-level high-water
	// cursor over the HOST-direction seq space. server_message,
	// server_message_chunk and pong share that space and its monotonic counter
	// (E-12b attempt-011 live wire: seq 1..136 zero-missing with pongs
	// interleaved); inbound ack frames carry the client direction's own counter
	// and never advance this cursor. inSeg/inSegOK remember a mid-message chunk
	// position (plain and pong leave it unset).
	inSeq   uint64
	inSeg   int
	inSegOK bool
	// onEnvelopeGap fires once per newly detected envelope-level gap
	// (seq > lastSeq+1). Stream-level unknown attribution: the fan-out to
	// reconcilable threads is the Agent's decision (§3.3 bounded fan-out).
	onEnvelopeGap func()
	// S-7 (§3.7): the transport-ack breaker state machine. closed ("")
	// acks per envelope — byte-identical to the pre-S-7 behavior; open and
	// half-open stop per-envelope acking and send at most one probe ack per
	// ackBreakerProbeInterval, carrying the highest seen (seq, segment)
	// cursor. A stream NEVER returns to closed within its lifetime: with no
	// observable host-acceptance signal, re-arming is the next connection's
	// fresh stream (E-1 discipline).
	ackState      string // "" (closed) | ackStateOpen | ackStateHalfOpen
	ackArmedAt    time.Time
	probeCount    int
	sentinelCount int
	// nowFunc lets the breaker tests fake the clock.
	nowFunc func() time.Time
	inbound chan []byte
	done    chan struct{}

	asmMu    sync.Mutex
	assembly map[uint64]*chunkAssembly
}

const (
	ackStateOpen     = "open"
	ackStateHalfOpen = "half-open"
)

// ackBreakerProbeInterval is the S-7 probe cadence (plan §3.7: engineering
// choice benchmarked against the official refresh-failure backoff 24–36s,
// NOT a protocol timing claim — the sampling gate applies to S-4's lead, not
// here). Caps steady-state sentinel noise at 1/T and bounds buffer drain
// latency on the host side.
const ackBreakerProbeInterval = 30 * time.Second

type chunkAssembly struct {
	count int
	size  int
	got   int
	parts [][]byte
}

// NewStream starts the inbound reader. envID/streamID are required routing keys
// proven on the live controller wire (attempt-008).
func NewStream(conn FrameConn, clientID, envID, streamID string) *Stream {
	s := &Stream{
		conn:             conn,
		clientID:         clientID,
		envID:            envID,
		streamID:         streamID,
		lastHostActivity: time.Now(),
		inbound:          make(chan []byte, 64),
		done:             make(chan struct{}),
		assembly:         map[uint64]*chunkAssembly{},
	}
	go s.readLoop()
	return s
}

func (s *Stream) nextSeqLocked() uint64 {
	s.nextSeq++
	return s.nextSeq
}

func (s *Stream) Send(payload []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex-remote: stream closed")
	}
	parts := splitPayload(payload)
	if len(parts) == 1 {
		seq := s.nextSeqLocked()
		env := Envelope{
			Type:        typeClientMessage,
			ClientID:    s.clientID,
			EnvID:       s.envID,
			StreamID:    s.streamID,
			SeqID:       &seq,
			SkipHistory: false,
			Message:     json.RawMessage(payload),
		}
		s.mu.Unlock()
		return s.conn.Write(env)
	}
	if len(parts) > MaxSegments {
		s.mu.Unlock()
		return fmt.Errorf("codex-remote: payload needs %d segments, max %d", len(parts), MaxSegments)
	}
	seq := s.nextSeqLocked()
	s.mu.Unlock()
	size := len(payload)
	count := len(parts)
	for i, part := range parts {
		seg := i
		env := Envelope{
			Type:               typeClientMessageChunk,
			ClientID:           s.clientID,
			EnvID:              s.envID,
			StreamID:           s.streamID,
			SeqID:              &seq,
			SegmentID:          &seg,
			SegmentCount:       &count,
			MessageSizeBytes:   &size,
			MessageChunkBase64: encodeChunk(part),
		}
		if err := s.conn.Write(env); err != nil {
			return err
		}
	}
	return nil
}

func (s *Stream) Recv() ([]byte, error) {
	select {
	case payload, ok := <-s.inbound:
		if !ok {
			s.mu.Lock()
			err := s.closeErr
			s.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("codex-remote: stream closed")
		}
		return payload, nil
	case <-s.done:
		s.mu.Lock()
		err := s.closeErr
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("codex-remote: stream closed")
	}
}

func (s *Stream) Ping() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex-remote: stream closed")
	}
	seq := s.nextSeqLocked()
	env := Envelope{
		Type:        typePing,
		ClientID:    s.clientID,
		EnvID:       s.envID,
		StreamID:    s.streamID,
		SeqID:       &seq,
		State:       "foreground",
		SkipHistory: true,
	}
	s.mu.Unlock()
	return s.conn.Write(env)
}

func (s *Stream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.conn.Write(Envelope{
		Type:     typeClientClosed,
		ClientID: s.clientID,
		EnvID:    s.envID,
		StreamID: s.streamID,
	})
	err := s.conn.Close()
	select {
	case <-s.done:
	default:
	}
	return err
}

// RecordedCursor is the reconnect cursor observed on inbound envelopes, if any.
// Empty means x-codex-subscribe-cursor must not be sent.
func (s *Stream) RecordedCursor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor
}

// IdleFor returns time since the last app-server liveness evidence. Relay ACKs
// deliberately do not count: upstream ClientTracker answers Ping with
// PongStatus::Active only while the exact (client_id, stream_id) exists.
func (s *Stream) IdleFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastHostActivity)
}

func (s *Stream) markHostActivity() {
	s.mu.Lock()
	s.lastHostActivity = time.Now()
	s.mu.Unlock()
}

func (s *Stream) fail(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.closeErr = err
	s.mu.Unlock()
	_ = s.conn.Close()
}

// ownStaleStream identifies a frame for an older logical app-server connection.
// It is safe to discard only after client_id and env_id match exactly.
func (s *Stream) ownStaleStream(env Envelope) bool {
	return env.StreamID != "" && env.StreamID != s.streamID &&
		env.ClientID != "" && env.ClientID == s.clientID &&
		env.EnvID != "" && env.EnvID == s.envID
}

// dropStaleStreamID records a discarded historical stream id once. Never
// deliver its payload: a late id=1 response could otherwise satisfy the new
// epoch's id=1 initialize request and create a false-ready connection.
func (s *Stream) dropStaleStreamID(id string, reason error) {
	s.mu.Lock()
	if s.staleStreams == nil {
		s.staleStreams = map[string]struct{}{}
	}
	_, seen := s.staleStreams[id]
	if !seen && len(s.staleStreams) < 16 {
		s.staleStreams[id] = struct{}{}
	}
	want := s.streamID
	s.mu.Unlock()
	if !seen {
		slog.Warn("codex-remote stream dropping stale stream_id",
			"gotStreamID", id, "wantStreamID", want, "reason", reason.Error())
	}
}

func (s *Stream) readLoop() {
	defer func() {
		close(s.inbound)
		close(s.done)
	}()
	for {
		env, err := s.conn.Read()
		if err != nil {
			s.fail(err)
			return
		}
		if err := env.routingOK(s.clientID, s.envID, s.streamID); err != nil {
			if s.ownStaleStream(env) {
				s.dropStaleStreamID(env.StreamID, err)
				continue
			} else {
				slog.Warn("codex-remote stream routing mismatch; failing stream",
					"error", err.Error(),
					"envelopeType", env.Type,
					"gotClientID", env.ClientID, "wantClientID", s.clientID,
					"gotEnvID", env.EnvID, "wantEnvID", s.envID,
					"gotStreamID", env.StreamID, "wantStreamID", s.streamID)
				s.fail(err)
				return
			}
		}
		if env.Cursor != nil && *env.Cursor != "" {
			s.mu.Lock()
			s.cursor = *env.Cursor
			s.mu.Unlock()
		}
		switch env.Type {
		case typeAck:
			// Client-direction receipt confirmation: carries the CLIENT's seq,
			// not the host-direction counter (attempt-011) — skip entirely,
			// never advances the inbound cursor and never probes.
			continue
		case typePong:
			// Match upstream ClientTracker: only Active proves this exact stream
			// exists; Unknown means the relay is reachable but the app-server
			// connection is gone.
			if env.Status != "active" {
				s.fail(fmt.Errorf("codex-remote: pong status %q: remote endpoint detached", env.Status))
				return
			}
			s.markHostActivity()
			// S-3: pong advances the shared host-direction cursor (F-R8-1) and
			// replays dedup like any envelope; F-R9-1: ack on arrival — the
			// only bounded drain for the host buffer when a wedged backend
			// produces no message envelopes at all.
			s.observeInboundEnvelope(env)
			s.ack(env)
			s.maybeProbe()
			continue
		case typeServerMessage:
			s.markHostActivity()
			// Sentinel FIRST, before the dedup drop (attempt-011 live wire:
			// the relay's sentinel errors arrive with retro seqs overlapping
			// already-delivered frames — deduping them away would keep the
			// breaker from ever arming on the real wire).
			s.observeTransportErrorSentinel(env.Message)
			if !s.observeInboundEnvelope(env) {
				// Duplicate replay: re-ack idempotently (buffer hygiene if the
				// earlier ack was lost) but never re-deliver.
				s.ack(env)
				continue
			}
			if err := s.deliver(env.Message); err != nil {
				s.fail(err)
				return
			}
			s.ack(env)
			s.maybeProbe()
		case typeServerMessageChunk:
			s.markHostActivity()
			if !s.observeInboundEnvelope(env) {
				s.ack(env)
				continue
			}
			payload, done, err := s.observeChunk(env)
			if err != nil {
				s.fail(err)
				return
			}
			if done {
				if err := s.deliver(payload); err != nil {
					s.fail(err)
					return
				}
				s.observeTransportErrorSentinel(payload)
			}
			s.ack(env)
			s.maybeProbe()
		default:
			// Unknown type: diagnose, do not leak payload, do not crash.
			continue
		}
	}
}

func (s *Stream) deliver(payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	select {
	case s.inbound <- append([]byte(nil), payload...):
		return nil
	case <-s.done:
		return fmt.Errorf("codex-remote: stream closed")
	}
}

// observeInboundEnvelope runs the S-3 (seq, segment) cursor judgment for one
// host-direction envelope and reports whether it is NEW (true → process and
// deliver) or a DUPLICATE replay (false → skip delivery; the caller still
// re-acks idempotently). Judgment table mirrors the official ack-clear
// cursor semantics (websocket.rs:112-138):
//
//	seq < lastSeq                     → duplicate (replayed old envelope)
//	seq == lastSeq, prev plain/pong   → plain/pong duplicate; chunk is
//	  officially impossible (whole-or-fully-chunked) — logged, processed
//	  fail-visible, cursor untouched
//	seq == lastSeq, prev chunk        → chunk with segment_id <= lastSeg is a
//	  replay; segment_id > lastSeg is the next segment of the same message
//	  (F-1: never drop it); plain/pong officially impossible — logged and
//	  processed
//	seq > lastSeq                     → advance (chunk sets the segment mark,
//	  plain/pong clear it); seq > lastSeq+1 additionally records an
//	  envelope-level gap ONCE and fires the fan-out — the cursor advances
//	  immediately so one gap cannot re-trigger per frame (R2-A1)
func (s *Stream) observeInboundEnvelope(env Envelope) bool {
	if env.SeqID == nil {
		return true
	}
	seq := *env.SeqID
	isChunk := env.Type == typeServerMessageChunk
	var gapFrom, gapTo uint64
	hasGap := false
	s.mu.Lock()
	switch {
	case seq < s.inSeq:
		s.mu.Unlock()
		return false
	case seq == s.inSeq:
		if isChunk && env.SegmentID != nil {
			if s.inSegOK {
				if *env.SegmentID <= s.inSeg {
					s.mu.Unlock()
					return false
				}
				s.inSeg = *env.SegmentID
			} else {
				slog.Warn("codex-remote stream: chunk follows non-chunk at same seq (officially impossible); processing fail-visible",
					"streamID", s.streamID, "seq", seq, "segment", *env.SegmentID)
			}
		} else if s.inSegOK {
			slog.Warn("codex-remote stream: non-chunk follows chunk at same seq (officially impossible); processing fail-visible",
				"streamID", s.streamID, "seq", seq, "type", env.Type)
		} else {
			s.mu.Unlock()
			return false
		}
	default:
		if seq > s.inSeq+1 {
			gapFrom, gapTo = s.inSeq+1, seq-1
			hasGap = true
		}
		s.inSeq = seq
		if isChunk && env.SegmentID != nil {
			s.inSeg = *env.SegmentID
			s.inSegOK = true
		} else {
			s.inSegOK = false
		}
	}
	s.mu.Unlock()
	if hasGap {
		// Any envelope may be lost — possibly just a harmless pong; the lost
		// payload is gone and thread attribution is unknowable at stream
		// level (§3.3). Fail-visible: log exactly what is known, fan out to
		// the reconciler, never fabricate events.
		slog.Warn("codex-remote stream: inbound envelope gap detected (may be a pong or a message)",
			"streamID", s.streamID, "gapFrom", gapFrom, "gapTo", gapTo)
		if s.onEnvelopeGap != nil {
			s.onEnvelopeGap()
		}
	}
	return true
}

// observeTransportErrorSentinel transitions the ack breaker to open on an
// inbound payload carrying the transport layer's sentinel thread id — from
// ANY state, resetting the probe clock (§3.7). Called before the dedup drop
// for plain messages and after chunk reassembly.
func (s *Stream) observeTransportErrorSentinel(payload []byte) {
	if !bytes.Contains(payload, transportErrorSentinel) {
		return
	}
	s.mu.Lock()
	prev := s.ackState
	s.ackState = ackStateOpen
	s.ackArmedAt = s.nowLocked()
	s.sentinelCount++
	count := s.sentinelCount
	s.mu.Unlock()
	if prev == "" {
		slog.Warn("codex-remote transport rejects client acks; opening ack breaker",
			"streamID", s.streamID)
	} else {
		slog.Info("codex-remote ack breaker sentinel",
			"streamID", s.streamID, "prevState", prev, "sentinelCount", count)
	}
}

// DisableAcks turns off client acks without waiting for the next sentinel
// error (wired for callers that learn the rejection out-of-band).
func (s *Stream) DisableAcks() {
	s.mu.Lock()
	s.ackState = ackStateOpen
	s.ackArmedAt = s.nowLocked()
	s.mu.Unlock()
}

// AcksDisabled reports whether the breaker is armed (diagnostics/tests).
func (s *Stream) AcksDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ackState != ""
}

// AckBreakerSnapshot exposes the S-7 breaker state and counters (S-6
// diagnostics visibility; E-10 observation surface).
func (s *Stream) AckBreakerSnapshot() (state string, probes, sentinels int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ackState == "" {
		state = "closed"
	} else {
		state = s.ackState
	}
	return state, s.probeCount, s.sentinelCount
}

func (s *Stream) now() time.Time {
	s.mu.Lock()
	fn := s.nowFunc
	s.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return time.Now()
}

// maybeProbe sends at most one probe ack per ackBreakerProbeInterval while
// the breaker is armed (§3.7). The probe carries the highest seen host-
// direction (seq, segment) cursor: under the official (seq, segment_id or
// MAX) clear semantics it drains everything up to that point, so a host that
// resumed accepting acks drains its backlog at probe cadence even though
// this stream never returns to closed. Trigger frames are ALL inbound host-
// direction envelopes — pong included (F-R5-A3: on a wedged backend pong is
// the only inbound flow); ack frames are excluded upstream in readLoop.
func (s *Stream) maybeProbe() {
	s.mu.Lock()
	if s.ackState == "" || s.nowLocked().Sub(s.ackArmedAt) < ackBreakerProbeInterval {
		s.mu.Unlock()
		return
	}
	if s.inSeq == 0 {
		// Nothing seen yet: a probe would carry cursor 0 and clear nothing.
		// Restart the clock and wait for the next interval.
		s.ackArmedAt = s.nowLocked()
		s.mu.Unlock()
		return
	}
	seq := s.inSeq
	var seg *int
	if s.inSegOK {
		v := s.inSeg
		seg = &v
	}
	s.ackState = ackStateHalfOpen
	s.ackArmedAt = s.nowLocked()
	s.probeCount++
	probes := s.probeCount
	s.mu.Unlock()
	probe := Envelope{
		Type:      typeAck,
		ClientID:  s.clientID,
		EnvID:     s.envID,
		StreamID:  s.streamID,
		SeqID:     &seq,
		SegmentID: seg,
	}
	if err := s.conn.Write(probe); err != nil {
		slog.Warn("codex-remote ack breaker probe write failed",
			"streamID", s.streamID, "error", err)
		return
	}
	captureOutboundEnvelope(probe)
	slog.Info("codex-remote ack breaker probe sent",
		"streamID", s.streamID, "cursorSeq", seq, "cursorHasSegment", seg != nil, "probeCount", probes)
}

// nowLocked is now() for callers already holding s.mu.
func (s *Stream) nowLocked() time.Time {
	if s.nowFunc != nil {
		return s.nowFunc()
	}
	return time.Now()
}

func (s *Stream) ack(env Envelope) {
	if env.SeqID == nil {
		return
	}
	s.mu.Lock()
	disabled := s.ackState != ""
	s.mu.Unlock()
	if disabled {
		return
	}
	ackEnv := Envelope{
		Type:     typeAck,
		ClientID: s.clientID,
		EnvID:    s.envID,
		StreamID: s.streamID,
		SeqID:    env.SeqID,
	}
	// S-3 (E-12a): a chunk ack must carry its segment_id. Without it the ack
	// is the (seq, MAX) cursor and clears the host's replay buffer for the
	// WHOLE message — losing the message tail on a host-leg interruption with
	// no envelope-level gap ever showing (chunks share the message's seq).
	if env.Type == typeServerMessageChunk && env.SegmentID != nil {
		ackEnv.SegmentID = env.SegmentID
	}
	if err := s.conn.Write(ackEnv); err != nil {
		return
	}
	captureOutboundEnvelope(ackEnv)
}

func (s *Stream) observeChunk(env Envelope) ([]byte, bool, error) {
	if env.SeqID == nil || env.SegmentID == nil || env.SegmentCount == nil {
		return nil, false, fmt.Errorf("codex-remote: incomplete server chunk")
	}
	if *env.SegmentCount < 1 || *env.SegmentCount > MaxSegments {
		return nil, false, fmt.Errorf("codex-remote: bad segment_count")
	}
	part, err := decodeChunk(env.MessageChunkBase64)
	if err != nil {
		return nil, false, err
	}
	s.asmMu.Lock()
	defer s.asmMu.Unlock()
	if len(s.assembly) >= MaxConcurrentAssemblies {
		return nil, false, fmt.Errorf("codex-remote: too many chunk assemblies")
	}
	asm := s.assembly[*env.SeqID]
	if asm == nil {
		asm = &chunkAssembly{
			count: *env.SegmentCount,
			parts: make([][]byte, *env.SegmentCount),
		}
		if env.MessageSizeBytes != nil {
			asm.size = *env.MessageSizeBytes
		}
		s.assembly[*env.SeqID] = asm
	}
	if *env.SegmentID < 0 || *env.SegmentID >= asm.count {
		return nil, false, fmt.Errorf("codex-remote: segment_id out of range")
	}
	if asm.parts[*env.SegmentID] == nil {
		asm.parts[*env.SegmentID] = part
		asm.got++
	}
	if asm.got < asm.count {
		return nil, false, nil
	}
	var out []byte
	for _, p := range asm.parts {
		out = append(out, p...)
	}
	if len(out) > ReassembledMessageMaxBytes {
		delete(s.assembly, *env.SeqID)
		return nil, false, fmt.Errorf("codex-remote: reassembled message too large")
	}
	delete(s.assembly, *env.SeqID)
	return out, true, nil
}

// SubscribeCursorHeader returns the reconnect header value only when a real
// envelope cursor was observed. Decision record (disconnect-resilience plan
// S-5 / evidence E-1): the official host never delivers a cursor to the
// controller leg — cursor replay is a host↔relay mechanism only — so this
// repo deliberately does not implement controller-leg replay. RecordedCursor
// stays observation-only; callers must not fabricate a cursor.
func SubscribeCursorHeader(cursor string) (name, value string, ok bool) {
	if cursor == "" {
		return "x-codex-subscribe-cursor", "", false
	}
	return "x-codex-subscribe-cursor", cursor, true
}

var _ Transport = (*Stream)(nil)
