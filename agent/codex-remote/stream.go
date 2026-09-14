package codexremote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// transportErrorSentinel matches the synthetic thread ids the remote-control
// transport layer stamps on connection-level diagnostics (e.g. the
// "Unexpected ack message received from client" error Desktop
// 0.154.0-alpha.6.2 emits for every client ack — see the 2026-09-14 ack
// storm: ~750 byte-identical notifications/s, 2.8MB/s relay ingress, all
// thread/list RPCs starved to their 12s deadline). Matched as raw payload
// prefix-contains so the breaker stays inside the transport layer.
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
	// acksDisabled is the transport-ack circuit breaker. Servers that reject
	// client acks answer each one with a sentinel error notification; acking
	// those answers again would self-oscillate at relay RTT (2026-09-14
	// storm). Connection-scoped: a fresh stream re-arms so a server that
	// later accepts acks regains the documented chunk-retention protocol.
	acksDisabled bool
	inbound      chan []byte
	done         chan struct{}

	asmMu    sync.Mutex
	assembly map[uint64]*chunkAssembly
}

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
			continue
		case typeServerMessage:
			s.markHostActivity()
			if err := s.deliver(env.Message); err != nil {
				s.fail(err)
				return
			}
			s.observeTransportErrorSentinel(env.Message)
			s.ack(env)
		case typeServerMessageChunk:
			s.markHostActivity()
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

// observeTransportErrorSentinel arms the ack circuit breaker when an inbound
// payload carries the transport layer's sentinel thread id. Called before
// ack(env) so the sentinel error itself is never answered.
func (s *Stream) observeTransportErrorSentinel(payload []byte) {
	if bytes.Contains(payload, transportErrorSentinel) {
		s.mu.Lock()
		armed := s.acksDisabled
		s.acksDisabled = true
		s.mu.Unlock()
		if !armed {
			slog.Warn("codex-remote transport rejects client acks; disabling acks for this stream",
				"streamID", s.streamID)
		}
	}
}

// DisableAcks turns off client acks without waiting for the next sentinel
// error (wired for callers that learn the rejection out-of-band).
func (s *Stream) DisableAcks() {
	s.mu.Lock()
	s.acksDisabled = true
	s.mu.Unlock()
}

// AcksDisabled reports the breaker state (diagnostics/tests).
func (s *Stream) AcksDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acksDisabled
}

func (s *Stream) ack(env Envelope) {
	if env.SeqID == nil {
		return
	}
	s.mu.Lock()
	disabled := s.acksDisabled
	s.mu.Unlock()
	if disabled {
		return
	}
	_ = s.conn.Write(Envelope{
		Type:     typeAck,
		ClientID: s.clientID,
		EnvID:    s.envID,
		StreamID: s.streamID,
		SeqID:    env.SeqID,
	})
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
// envelope cursor was observed. Owner-accepted known gap: live target never
// delivered one; callers must not fabricate it.
func SubscribeCursorHeader(cursor string) (name, value string, ok bool) {
	if cursor == "" {
		return "x-codex-subscribe-cursor", "", false
	}
	return "x-codex-subscribe-cursor", cursor, true
}

var _ Transport = (*Stream)(nil)
