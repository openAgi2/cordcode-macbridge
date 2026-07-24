package gobridge

import (
	"log/slog"
	"sync"
	"time"
)

// LiveFrameBuffer holds recent live (non-durable) event frames for devices that
// should have received them but had no online target at publish time.
//
// Contrast with durable outbox/mailbox (relay_offline.go): milestones are
// persisted indefinitely. Live frames are a short, memory-only window so a
// reconnect can replay the gap instead of jumping via history bulk.
//
// Design: docs/2026-07-24-external-codex-turn-live-frame-buffer-design.md
const (
	liveFrameBufferMaxAge     = 60 * time.Second
	liveFrameBufferMaxFrames  = 200 // per device per session
	liveFrameBufferMaxBytes   = 1 << 20
	liveFrameBufferGCInterval = 15 * time.Second
	liveInterestMaxAge        = 10 * time.Minute
)

// LiveFrameBuffer is safe for concurrent use.
type LiveFrameBuffer struct {
	mu       sync.Mutex
	buckets  map[string]*deviceLiveBucket    // deviceID -> bucket
	interest map[string]map[string]time.Time // deviceID -> sessionKey -> lastSeen
	maxAge   time.Duration
	maxN     int
	maxB     int
	now      func() time.Time
}

type deviceLiveBucket struct {
	sessions map[string]*liveSessionBuffer // backendID\x00sessionID
}

type liveSessionBuffer struct {
	frames     []bufferedLiveFrame
	totalBytes int
}

type bufferedLiveFrame struct {
	msg       EventMessage
	emittedAt time.Time
	bytes     int
}

func NewLiveFrameBuffer() *LiveFrameBuffer {
	b := &LiveFrameBuffer{
		buckets:  make(map[string]*deviceLiveBucket),
		interest: make(map[string]map[string]time.Time),
		maxAge:   liveFrameBufferMaxAge,
		maxN:     liveFrameBufferMaxFrames,
		maxB:     liveFrameBufferMaxBytes,
		now:      time.Now,
	}
	go b.gcLoop()
	return b
}

func sessionKey(backendID, sessionID string) string {
	return backendID + "\x00" + sessionID
}

// NoteInterest records that deviceID is watching backend/session (SetScope or
// successful live delivery). Survives observation RemoveDevice on soft prune so
// zero-target publishes still know who to buffer for.
func (b *LiveFrameBuffer) NoteInterest(deviceID, backendID, sessionID string) {
	if b == nil || deviceID == "" || backendID == "" || sessionID == "" {
		return
	}
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.interest[deviceID]
	if m == nil {
		m = make(map[string]time.Time)
		b.interest[deviceID] = m
	}
	m[sessionKey(backendID, sessionID)] = now
}

// InterestedDevices returns device IDs that recently watched this session.
func (b *LiveFrameBuffer) InterestedDevices(backendID, sessionID string) []string {
	if b == nil {
		return nil
	}
	key := sessionKey(backendID, sessionID)
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for deviceID, m := range b.interest {
		if at, ok := m[key]; ok && now.Sub(at) <= liveInterestMaxAge {
			out = append(out, deviceID)
		}
	}
	return out
}

func isLiveBufferableEvent(eventName string) bool {
	switch eventName {
	case "text_delta", "reasoning_delta", "tool_output_delta",
		"tool_started", "tool_finished",
		"turn_started", "user_message", "session_state_changed",
		"assistant_message_started", "assistant_message_delta", "assistant_message_finished",
		"reasoning_started", "reasoning_finished",
		"context_usage_updated":
		return true
	default:
		return false
	}
}

// Append stores msg for each deviceID.
func (b *LiveFrameBuffer) Append(deviceIDs []string, msg EventMessage) {
	if b == nil || len(deviceIDs) == 0 {
		return
	}
	if !isLiveBufferableEvent(msg.Event) {
		return
	}
	encoded, _ := jsonMarshalLen(msg)
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, deviceID := range deviceIDs {
		if deviceID == "" {
			continue
		}
		m := b.interest[deviceID]
		if m == nil {
			m = make(map[string]time.Time)
			b.interest[deviceID] = m
		}
		m[sessionKey(msg.BackendID, msg.SessionID)] = now

		bucket := b.buckets[deviceID]
		if bucket == nil {
			bucket = &deviceLiveBucket{sessions: make(map[string]*liveSessionBuffer)}
			b.buckets[deviceID] = bucket
		}
		key := sessionKey(msg.BackendID, msg.SessionID)
		sess := bucket.sessions[key]
		if sess == nil {
			sess = &liveSessionBuffer{}
			bucket.sessions[key] = sess
		}
		sess.frames = append(sess.frames, bufferedLiveFrame{msg: msg, emittedAt: now, bytes: encoded})
		sess.totalBytes += encoded
		b.trimSessionLocked(sess, now)
	}
}

func (b *LiveFrameBuffer) trimSessionLocked(sess *liveSessionBuffer, now time.Time) {
	i := 0
	for i < len(sess.frames) && now.Sub(sess.frames[i].emittedAt) > b.maxAge {
		sess.totalBytes -= sess.frames[i].bytes
		i++
	}
	if i > 0 {
		sess.frames = append([]bufferedLiveFrame(nil), sess.frames[i:]...)
	}
	for len(sess.frames) > b.maxN || sess.totalBytes > b.maxB {
		if len(sess.frames) == 0 {
			break
		}
		sess.totalBytes -= sess.frames[0].bytes
		sess.frames = sess.frames[1:]
	}
	if sess.totalBytes < 0 {
		sess.totalBytes = 0
	}
}

// Snapshot returns non-expired frames for deviceID without clearing the buffer.
func (b *LiveFrameBuffer) Snapshot(deviceID string) []EventMessage {
	if b == nil || deviceID == "" {
		return nil
	}
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	bucket := b.buckets[deviceID]
	if bucket == nil {
		return nil
	}
	var out []EventMessage
	for _, sess := range bucket.sessions {
		b.trimSessionLocked(sess, now)
		for _, f := range sess.frames {
			out = append(out, f.msg)
		}
	}
	return out
}

func (b *LiveFrameBuffer) FrameCount(deviceID string) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	bucket := b.buckets[deviceID]
	if bucket == nil {
		return 0
	}
	n := 0
	for _, sess := range bucket.sessions {
		n += len(sess.frames)
	}
	return n
}

func (b *LiveFrameBuffer) gcLoop() {
	t := time.NewTicker(liveFrameBufferGCInterval)
	defer t.Stop()
	for range t.C {
		b.gcOnce()
	}
}

func (b *LiveFrameBuffer) gcOnce() {
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	for deviceID, bucket := range b.buckets {
		for key, sess := range bucket.sessions {
			b.trimSessionLocked(sess, now)
			if len(sess.frames) == 0 {
				delete(bucket.sessions, key)
			}
		}
		if len(bucket.sessions) == 0 {
			delete(b.buckets, deviceID)
		}
	}
	for deviceID, m := range b.interest {
		for key, at := range m {
			if now.Sub(at) > liveInterestMaxAge {
				delete(m, key)
			}
		}
		if len(m) == 0 {
			delete(b.interest, deviceID)
		}
	}
}

func jsonMarshalLen(msg EventMessage) (int, error) {
	n := 128 + len(msg.Event) + len(msg.SessionID) + len(msg.BackendID) + len(msg.EventID)
	if msg.Message != "" {
		n += len(msg.Message)
	}
	n += 512
	return n, nil
}

func LogLiveFrameBufferFlush(deviceID string, n int, firstSeq, lastSeq int) {
	if n <= 0 {
		return
	}
	slog.Info("live-frame-buffer flush",
		"deviceID", safeID(deviceID),
		"frames", n,
		"firstSeq", firstSeq,
		"lastSeq", lastSeq,
	)
}
