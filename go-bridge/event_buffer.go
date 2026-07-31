package gobridge

import (
	"encoding/json"
	"sync"
	"time"
)

const eventTombstoneMetadataCost = 96

type BridgeSessionCut struct {
	EventID string `json:"eventId"`
	Seq     int    `json:"seq"`
}

type EventBufferConfig struct {
	MaxEvents int
	MaxBytes  int
	TTL       time.Duration
	Now       func() time.Time
}

type bufferedEvent struct {
	message      EventMessage
	backendID    string
	sessionID    string
	seq          int
	replayable   bool
	createdAt    time.Time
	payloadBytes int
	chargedBytes int
}

type ReplayDisposition string

const (
	ReplayAvailable        ReplayDisposition = "replay"
	ReplaySnapshotRequired ReplayDisposition = "snapshot_required"
)

type EventReplayResult struct {
	Disposition ReplayDisposition
	Events      []EventMessage
	Through     BridgeSessionCut
	Reason      string
}

type EventBuffer struct {
	mu             sync.Mutex
	config         EventBufferConfig
	records        []bufferedEvent
	chargedBytes   int
	latest         map[SubscriptionKey]BridgeSessionCut
	evictedThrough map[SubscriptionKey]int
}

func NewEventBuffer(config EventBufferConfig) *EventBuffer {
	if config.MaxEvents <= 0 {
		config.MaxEvents = 10_000
	}
	if config.MaxBytes <= 0 {
		config.MaxBytes = 16 << 20
	}
	if config.TTL <= 0 {
		config.TTL = 5 * time.Minute
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &EventBuffer{
		config:         config,
		latest:         make(map[SubscriptionKey]BridgeSessionCut),
		evictedThrough: make(map[SubscriptionKey]int),
	}
}

func (b *EventBuffer) Append(message EventMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.config.Now()
	b.evictExpiredLocked(now)
	key := SubscriptionKey{BackendID: message.BackendID, SessionID: message.SessionID}
	record := bufferedEvent{
		backendID:  message.BackendID,
		sessionID:  message.SessionID,
		seq:        message.Seq,
		replayable: message.Replayable,
		createdAt:  now,
	}
	if message.Replayable {
		encoded, _ := json.Marshal(message)
		record.message = message
		record.payloadBytes = len(encoded)
		record.chargedBytes = len(encoded)
	} else {
		// A tombstone preserves coverage without retaining delta content.
		record.message = EventMessage{
			Type: message.Type, EventID: message.EventID, Seq: message.Seq,
			BridgeEpoch: message.BridgeEpoch, SessionID: message.SessionID,
			BackendID: message.BackendID, Event: message.Event,
			Replayable: false, Timestamp: message.Timestamp,
		}
		record.chargedBytes = eventTombstoneMetadataCost
	}
	b.records = append(b.records, record)
	b.chargedBytes += record.chargedBytes
	b.latest[key] = BridgeSessionCut{EventID: message.EventID, Seq: message.Seq}
	b.enforceLimitsLocked()
}

func (b *EventBuffer) Replay(backendID, sessionID string, cursor BridgeSessionCut) EventReplayResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.evictExpiredLocked(b.config.Now())
	key := SubscriptionKey{BackendID: backendID, SessionID: sessionID}
	latest, known := b.latest[key]
	if !known {
		return EventReplayResult{Disposition: ReplayAvailable, Through: cursor}
	}
	if cursor.Seq > latest.Seq {
		return EventReplayResult{Disposition: ReplaySnapshotRequired, Through: latest, Reason: "cursor_ahead"}
	}
	if b.evictedThrough[key] > cursor.Seq {
		return EventReplayResult{Disposition: ReplaySnapshotRequired, Through: latest, Reason: "coverage_evicted"}
	}
	events := make([]EventMessage, 0)
	for _, record := range b.records {
		if record.backendID != backendID || record.sessionID != sessionID || record.seq <= cursor.Seq {
			continue
		}
		if !record.replayable {
			return EventReplayResult{Disposition: ReplaySnapshotRequired, Through: latest, Reason: "non_replayable_gap"}
		}
		events = append(events, record.message)
	}
	return EventReplayResult{Disposition: ReplayAvailable, Events: events, Through: latest}
}

func (b *EventBuffer) LatestCut(backendID, sessionID string) (BridgeSessionCut, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.evictExpiredLocked(b.config.Now())
	cut, ok := b.latest[SubscriptionKey{BackendID: backendID, SessionID: sessionID}]
	return cut, ok
}

func (b *EventBuffer) Rebind(backendID, oldSessionID, newSessionID string) {
	if oldSessionID == "" || newSessionID == "" || oldSessionID == newSessionID {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	oldKey := SubscriptionKey{BackendID: backendID, SessionID: oldSessionID}
	newKey := SubscriptionKey{BackendID: backendID, SessionID: newSessionID}
	for i := range b.records {
		if b.records[i].backendID == backendID && b.records[i].sessionID == oldSessionID {
			b.records[i].backendID = backendID
			b.records[i].sessionID = newSessionID
			b.records[i].message.SessionID = newSessionID
		}
	}
	if cut, ok := b.latest[oldKey]; ok {
		if existing, exists := b.latest[newKey]; !exists || cut.Seq > existing.Seq {
			b.latest[newKey] = cut
		}
		delete(b.latest, oldKey)
	}
	if seq := b.evictedThrough[oldKey]; seq > b.evictedThrough[newKey] {
		b.evictedThrough[newKey] = seq
	}
	delete(b.evictedThrough, oldKey)
}

func (b *EventBuffer) Stats() (events, bytes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.records), b.chargedBytes
}

func (b *EventBuffer) RecordsForTesting() []bufferedEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bufferedEvent(nil), b.records...)
}

func (b *EventBuffer) enforceLimitsLocked() {
	for len(b.records) > b.config.MaxEvents || b.chargedBytes > b.config.MaxBytes {
		b.evictOldestLocked()
	}
}

func (b *EventBuffer) evictExpiredLocked(now time.Time) {
	for len(b.records) > 0 && now.Sub(b.records[0].createdAt) >= b.config.TTL {
		b.evictOldestLocked()
	}
}

func (b *EventBuffer) evictOldestLocked() {
	if len(b.records) == 0 {
		return
	}
	record := b.records[0]
	b.records[0] = bufferedEvent{}
	b.records = b.records[1:]
	b.chargedBytes -= record.chargedBytes
	key := SubscriptionKey{BackendID: record.backendID, SessionID: record.sessionID}
	if record.seq > b.evictedThrough[key] {
		b.evictedThrough[key] = record.seq
	}
}
