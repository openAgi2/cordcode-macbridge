package gobridge

// Claude Code has no service-level broadcast API. Its normal file relay is
// intentionally session-scoped and starts only after a client opens a session.
// This watcher tails only NEW transcript bytes for Web Push; it never publishes
// timeline events and never creates hidden reducer state for unopened sessions.

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"
)

var claudeWebPushWatchInterval = 3 * time.Second

type claudeWebPushWatchState struct {
	path          string
	offset        int64
	scan          claudeRelayScanState
	currentTurnID string
	preview       claudeTurnTextAccumulator
}

type claudeWebPushWatcher struct {
	h      *Handlers
	states map[claudeSessionKey]*claudeWebPushWatchState
	seeded bool
}

func (h *Handlers) StartClaudeWebPushWatcher(ctx context.Context) {
	if h == nil || h.claudeSessions == nil || h.webPushPipeline == nil {
		return
	}
	watcher := &claudeWebPushWatcher{h: h, states: make(map[claudeSessionKey]*claudeWebPushWatchState)}
	go watcher.run(ctx)
}

func (w *claudeWebPushWatcher) run(ctx context.Context) {
	w.sweep()
	ticker := time.NewTicker(claudeWebPushWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep()
		}
	}
}

func (w *claudeWebPushWatcher) sweep() {
	if w == nil || w.h == nil || w.h.claudeSessions == nil {
		return
	}
	snapshot := w.h.claudeSessions.refresh(nil)
	if snapshot == nil {
		return
	}
	enabled := w.h.webPush != nil && w.h.webPush.SubscriptionCount() > 0
	seen := make(map[claudeSessionKey]struct{}, len(snapshot.Sorted))
	for _, entry := range snapshot.Sorted {
		seen[entry.Key] = struct{}{}
		cut, err := projectionJSONLStartCut(entry.FilePath)
		if err != nil {
			continue
		}
		state := w.states[entry.Key]
		if state == nil || state.path != entry.FilePath {
			state = &claudeWebPushWatchState{path: entry.FilePath}
			w.states[entry.Key] = state
			// First process-wide sweep and periods without an enrollment establish
			// a baseline only. A file created later while push is enabled is live
			// and must be consumed from byte zero.
			if !w.seeded || !enabled {
				state.offset = cut
				continue
			}
		}
		if cut < state.offset {
			// Truncate/replacement has no replay-safe identity. Re-seed rather than
			// turning historical rows into new notifications.
			state.offset = cut
			state.scan = claudeRelayScanState{}
			state.currentTurnID = ""
			state.preview.reset()
			continue
		}
		if cut == state.offset {
			continue
		}
		if !enabled || w.h.claudeSessionHasInteractiveRelay(entry.Key.SessionID) {
			// The interactive relay owns notifications for an opened session. Move
			// this observer's cut forward so it cannot replay the same rows later.
			state.offset = cut
			state.scan = claudeRelayScanState{}
			state.currentTurnID = ""
			state.preview.reset()
			continue
		}
		w.consumeGrowth(entry, state)
	}
	for key := range w.states {
		if _, ok := seen[key]; !ok {
			delete(w.states, key)
		}
	}
	w.seeded = true
}

func (h *Handlers) claudeSessionHasInteractiveRelay(sessionID string) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	running := h.relayRunning[sessionID] || h.agentRelayRunning[sessionID]
	h.mu.Unlock()
	if running {
		return true
	}
	return h.broadcaster != nil && h.broadcaster.HasSessionSubscriber("claude", sessionID)
}

func (w *claudeWebPushWatcher) consumeGrowth(entry claudeSessionIndexEntry, state *claudeWebPushWatchState) {
	if state.currentTurnID == "" && state.offset > 0 {
		state.currentTurnID = lastClaudeUserIdentityFromPath(state.path, state.offset)
	}
	f, err := os.Open(state.path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(state.offset, io.SeekStart); err != nil {
		return
	}
	scan, err := scanCompleteClaudeRelayEntriesFromReader(f, state.offset, &state.scan)
	if err != nil {
		slog.Warn("web-push: Claude unopened-session tail scan failed",
			"sessionPrefix", projectionSessionLogPrefix(entry.Key.SessionID), "error", err)
		return
	}
	state.offset += scan.ConsumedBytes
	for _, record := range scan.Records {
		if !record.Admitted {
			continue
		}
		events := claudeEntryToProjectionEvents(record.Entry, &state.currentTurnID, nil)
		state.preview.observe(events)
		for _, event := range events {
			if event.Event != "turn_completed" {
				continue
			}
			w.h.enqueueReplayFreeLivePush(
				"claude", entry.Key.SessionID, event.Event, event.Data,
				entry.Title, state.preview.preview(),
			)
		}
	}
	if scan.Poison != nil {
		slog.Error("web-push: Claude unopened-session tail quarantined invalid record",
			"sessionPrefix", projectionSessionLogPrefix(entry.Key.SessionID),
			"byteStart", scan.Poison.ByteStart, "byteEnd", scan.Poison.ByteEnd)
	}
}
