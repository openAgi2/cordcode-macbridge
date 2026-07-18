package gobridge

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
)

const (
	relayInboundQueueFrames = 256
	relayInboundQueueBytes  = 8 << 20
	relayInboundDeviceFIFO  = "__device_fifo__"
)

var relayInboundQueueOverflow atomic.Uint64

type relayInboundJob struct {
	raw        json.RawMessage
	message    WireMessage
	sessionKey string
	class      relayOutboundClass
}

// relayInboundScheduler is a per-device single executor. It removes the
// bridge-wide readLoop head-of-line block while retaining ingress FIFO within
// each session. Priority is considered only between session-queue heads.
type relayInboundScheduler struct {
	mu          sync.Mutex
	queues      map[string][]relayInboundJob
	keys        []string
	cursor      int
	frames      int
	bytes       int
	wake        chan struct{}
	stop        chan struct{}
	once        sync.Once
	dispatch    func(WireMessage)
	onHistory   func(sessionID, requestID string)
	onSupersede func(requestID string)
}

func newRelayInboundScheduler(dispatch func(WireMessage), onHistory func(string, string), onSupersede ...func(string)) *relayInboundScheduler {
	s := &relayInboundScheduler{
		queues: make(map[string][]relayInboundJob), wake: make(chan struct{}, 1),
		stop: make(chan struct{}), dispatch: dispatch, onHistory: onHistory,
	}
	if len(onSupersede) != 0 {
		s.onSupersede = onSupersede[0]
	}
	go s.run()
	return s
}

func (s *relayInboundScheduler) enqueue(raw json.RawMessage) error {
	var msg WireMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("decode relay inbound: %w", err)
	}
	return s.enqueueMessage(raw, msg)
}

func (s *relayInboundScheduler) enqueueMessage(raw json.RawMessage, msg WireMessage) error {
	key := relayInboundSessionKey(msg)
	class := relayOutboundNormal
	if msg.Type == "hello" || msg.Type == "ping" || msg.Type == "recovery_applied" {
		class = relayOutboundControl
	} else if msg.Type == "request" {
		class = classifyRelayRequest(msg.Method)
	}
	job := relayInboundJob{raw: append(json.RawMessage(nil), raw...), message: msg, sessionKey: key, class: class}

	s.mu.Lock()
	select {
	case <-s.stop:
		s.mu.Unlock()
		return fmt.Errorf("relay inbound scheduler closed")
	default:
	}
	if s.frames+1 > relayInboundQueueFrames || s.bytes+len(raw) > relayInboundQueueBytes {
		relayInboundQueueOverflow.Add(1)
		slog.Error("relay inbound queue overflow", "relay_queue_frames", s.frames, "relay_queue_bytes", s.bytes)
		s.mu.Unlock()
		return fmt.Errorf("relay inbound queue overflow")
	}
	_, keyExisted := s.queues[key]
	if msg.Type == "request" && msg.Method == "get_session_messages" && key != relayInboundDeviceFIFO {
		old := s.queues[key]
		kept := old[:0]
		for _, pending := range old {
			if pending.message.Type == "request" && pending.message.Method == "get_session_messages" {
				s.frames--
				s.bytes -= len(pending.raw)
				if s.onSupersede != nil {
					s.onSupersede(pending.message.RequestID)
				}
				continue
			}
			kept = append(kept, pending)
		}
		s.queues[key] = kept
		if s.onHistory != nil {
			s.onHistory(key, msg.RequestID)
		}
	}
	if !keyExisted {
		s.keys = append(s.keys, key)
	}
	s.queues[key] = append(s.queues[key], job)
	s.frames++
	s.bytes += len(raw)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

func relayInboundSessionKey(msg WireMessage) string {
	if msg.SessionID != "" {
		return msg.SessionID
	}
	if len(msg.Params) != 0 {
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && params.SessionID != "" {
			return params.SessionID
		}
	}
	return relayInboundDeviceFIFO
}

func (s *relayInboundScheduler) run() {
	for {
		select {
		case <-s.stop:
			return
		case <-s.wake:
			for {
				job, ok := s.pop()
				if !ok {
					break
				}
				if s.dispatch != nil {
					s.dispatch(job.message)
				}
			}
		}
	}
}

func (s *relayInboundScheduler) pop() (relayInboundJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frames == 0 || len(s.keys) == 0 {
		return relayInboundJob{}, false
	}
	for class := relayOutboundControl; class < relayOutboundClassCount; class++ {
		for offset := range s.keys {
			idx := (s.cursor + offset) % len(s.keys)
			key := s.keys[idx]
			queue := s.queues[key]
			if len(queue) == 0 || queue[0].class != class {
				continue
			}
			job := queue[0]
			s.queues[key] = queue[1:]
			s.frames--
			s.bytes -= len(job.raw)
			s.cursor = (idx + 1) % len(s.keys)
			return job, true
		}
	}
	return relayInboundJob{}, false
}

func (s *relayInboundScheduler) close() { s.once.Do(func() { close(s.stop) }) }
