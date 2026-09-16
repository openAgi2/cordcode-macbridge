package gobridge

// Claude Code has no service-level broadcast API. Its normal file relay is
// intentionally session-scoped and starts only after a client opens a session.
// This watcher tails only NEW transcript bytes for Web Push; it never publishes
// timeline events and never creates hidden reducer state for unopened sessions.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

var claudeWebPushWatchInterval = 3 * time.Second
var claudeWebPushTerminalHold = 7 * time.Second

type claudeWebPushWatchState struct {
	path          string
	offset        int64
	scan          claudeRelayScanState
	currentTurnID string
	preview       claudeTurnTextAccumulator
	pendingTurnID string
	pendingData   map[string]interface{}
	pendingSince  time.Time
}

type claudeWebPushWatcher struct {
	h         *Handlers
	states    map[claudeSessionKey]*claudeWebPushWatchState
	startedAt time.Time
}

func (h *Handlers) StartClaudeWebPushWatcher(ctx context.Context) {
	if h == nil || h.claudeSessions == nil || h.webPushPipeline == nil {
		return
	}
	watcher := &claudeWebPushWatcher{h: h, states: make(map[claudeSessionKey]*claudeWebPushWatchState), startedAt: time.Now().UTC()}
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
	if w.startedAt.IsZero() {
		w.startedAt = time.Now().UTC()
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
			// First visibility is not evidence that the transcript was created after
			// this watcher: a Mac client opening an old session can make it visible.
			// Baseline all records timestamped before watcher startup, but retain
			// records from an already-live turn that completes after startup.
			state.offset = claudeFirstVisibleLiveCut(entry.FilePath, cut, w.startedAt)
			continue
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
			w.flushAgedTerminal(entry, state)
			continue
		}
		if !enabled || w.h.claudeSessionHasInteractiveRelay(entry.Key.SessionID) {
			// The interactive relay owns notifications for an opened session. Move
			// this observer's cut forward so it cannot replay the same rows later.
			state.offset = cut
			state.scan = claudeRelayScanState{}
			state.currentTurnID = ""
			state.preview.reset()
			state.pendingTurnID = ""
			state.pendingData = nil
			state.pendingSince = time.Time{}
			continue
		}
		w.consumeGrowth(entry, state)
	}
	for key := range w.states {
		if _, ok := seen[key]; !ok {
			delete(w.states, key)
		}
	}
}

func claudeFirstVisibleLiveCut(path string, completeCut int64, startedAt time.Time) int64 {
	f, err := os.Open(path)
	if err != nil {
		return completeCut
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var offset int64
	for offset < completeCut {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 || line[len(line)-1] != '\n' {
			return offset
		}
		var row struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(line, &row) == nil && strings.TrimSpace(row.Timestamp) != "" {
			if timestamp, err := time.Parse(time.RFC3339Nano, row.Timestamp); err == nil && !timestamp.Before(startedAt) {
				return offset
			}
		}
		offset += int64(len(line))
		if readErr != nil {
			return offset
		}
	}
	return offset
}

func (w *claudeWebPushWatcher) flushTerminal(entry claudeSessionIndexEntry, state *claudeWebPushWatchState) {
	if state == nil || state.pendingTurnID == "" {
		return
	}
	w.h.enqueueReplayFreeLivePush(
		"claude", entry.Key.SessionID, "turn_completed", state.pendingData,
		entry.Title, state.preview.preview(),
	)
	state.pendingTurnID = ""
	state.pendingData = nil
	state.pendingSince = time.Time{}
}

func (w *claudeWebPushWatcher) flushAgedTerminal(entry claudeSessionIndexEntry, state *claudeWebPushWatchState) {
	if state == nil || state.pendingTurnID == "" || state.pendingSince.IsZero() {
		return
	}
	if time.Since(state.pendingSince) >= claudeWebPushTerminalHold {
		w.flushTerminal(entry, state)
	}
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
		events := claudeEntryToProjectionEvents(record.Entry, &state.currentTurnID, nil, nil)
		for _, event := range events {
			if event.Event != "user_message" || state.pendingTurnID == "" {
				continue
			}
			nextTurnID, _ := event.Data["turnId"].(string)
			if nextTurnID != "" && nextTurnID != state.pendingTurnID {
				// A new user turn is authoritative closure for the prior turn even
				// if its second text-bearing terminal row was never written.
				w.flushTerminal(entry, state)
			}
		}
		state.preview.observe(events)
		for _, event := range events {
			if event.Event == "user_message" {
				if turnID, _ := event.Data["turnId"].(string); turnID != "" {
					state.pendingTurnID = ""
					state.pendingData = nil
					state.pendingSince = time.Time{}
				}
				continue
			}
			if event.Event != "turn_completed" {
				continue
			}
			turnID, _ := event.Data["turnId"].(string)
			if turnID == "" {
				continue
			}
			if state.pendingTurnID == "" {
				state.pendingTurnID = turnID
				state.pendingData = event.Data
				state.pendingSince = time.Now()
			} else if state.pendingTurnID == turnID {
				// Claude may write a textless end_turn followed by a text-bearing
				// end_turn for the same logical turn. Keep one pending candidate.
				state.pendingData = event.Data
			}
		}
	}
	if state.pendingTurnID != "" && state.preview.preview() != "" {
		w.flushTerminal(entry, state)
	}
	if scan.Poison != nil {
		slog.Error("web-push: Claude unopened-session tail quarantined invalid record",
			"sessionPrefix", projectionSessionLogPrefix(entry.Key.SessionID),
			"byteStart", scan.Poison.ByteStart, "byteEnd", scan.Poison.ByteEnd)
	}
}
