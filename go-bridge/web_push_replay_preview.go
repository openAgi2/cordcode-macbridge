package gobridge

import (
	"sort"
	"sync"
	"time"
)

// replayFreePreviewKey identifies one live turn without creating projection
// state. Backend is retained so the same logical session ID under different
// backends can never share text.
type replayFreePreviewKey struct {
	BackendID string
	SessionID string
	TurnID    string
}

type replayFreePreviewEntry struct {
	accumulator claudeTurnTextAccumulator
	lastSeen    time.Time
}

// replayFreePreviewCache is a bounded, in-memory bridge between service-level
// text deltas and a later terminal event. It is not a fallback data source and
// never reads history: if no live text was observed, Take returns "" and the
// notification honestly falls back to its fixed body.
type replayFreePreviewCache struct {
	mu      sync.Mutex
	entries map[replayFreePreviewKey]*replayFreePreviewEntry
}

func newReplayFreePreviewCache() *replayFreePreviewCache {
	return &replayFreePreviewCache{entries: make(map[replayFreePreviewKey]*replayFreePreviewEntry)}
}

func (c *replayFreePreviewCache) Observe(backendID, sessionID, turnID, delta string) {
	if c == nil || backendID == "" || sessionID == "" || turnID == "" || delta == "" {
		return
	}
	key := replayFreePreviewKey{BackendID: backendID, SessionID: sessionID, TurnID: turnID}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		if len(c.entries) >= 64 {
			c.evictOldestLocked()
		}
		entry = &replayFreePreviewEntry{}
		c.entries[key] = entry
	}
	entry.accumulator.append(delta)
	entry.lastSeen = time.Now()
}

func (c *replayFreePreviewCache) Take(backendID, sessionID, turnID string) string {
	if c == nil || backendID == "" || sessionID == "" || turnID == "" {
		return ""
	}
	key := replayFreePreviewKey{BackendID: backendID, SessionID: sessionID, TurnID: turnID}
	c.mu.Lock()
	entry := c.entries[key]
	delete(c.entries, key)
	c.mu.Unlock()
	if entry == nil {
		return ""
	}
	return entry.accumulator.preview()
}

func (c *replayFreePreviewCache) evictOldestLocked() {
	keys := make([]replayFreePreviewKey, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return c.entries[keys[i]].lastSeen.Before(c.entries[keys[j]].lastSeen)
	})
	delete(c.entries, keys[0])
}
