package codexremote

import (
	"encoding/json"
	"fmt"
	"sync"
)

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
	inbound  chan []byte
	done     chan struct{}

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
		conn:     conn,
		clientID: clientID,
		envID:    envID,
		streamID: streamID,
		inbound:  make(chan []byte, 64),
		done:     make(chan struct{}),
		assembly: map[uint64]*chunkAssembly{},
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
			s.fail(err)
			return
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
			if env.Status != "" && env.Status != "active" {
				s.fail(fmt.Errorf("codex-remote: pong status %q", env.Status))
				return
			}
			continue
		case typeServerMessage:
			if err := s.deliver(env.Message); err != nil {
				s.fail(err)
				return
			}
			s.ack(env)
		case typeServerMessageChunk:
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

func (s *Stream) ack(env Envelope) {
	if env.SeqID == nil {
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
