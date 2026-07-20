package gobridge

// Session-discovery push (optional, multi-client-streaming-sync §6).
//
// MacBridge periodically lists each backend's sessions; when a NEW session appears
// (e.g. a turn opened in a native app while iOS sits on the session list), it
// broadcasts a "sessions_changed" event. Clients refresh list_sessions on receipt.
// This is a latency win only — clients also refresh on reconnect/foreground, and
// iOS posts sessionListNeedsRefresh on turn activity, so absence of this push is
// not a correctness gap. Routing relies on Broadcaster.Targets' all-backend /
// all-connections fallback (types.go Targets), which delivers a backend-scoped
// broadcast with empty SessionID to list-viewing clients.

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// sessionDiscoveryInterval is the snapshot cadence. Package var so tests can shrink it.
var sessionDiscoveryInterval = 60 * time.Second

// StartSessionDiscoveryWatcher launches the background new-session watcher. The
// first scan seeds the snapshot without broadcasting (no startup burst).
func (h *Handlers) StartSessionDiscoveryWatcher(ctx context.Context) {
	go h.runSessionDiscovery(ctx)
}

func (h *Handlers) runSessionDiscovery(ctx context.Context) {
	seen := map[string]map[string]bool{}
	h.snapshotSessions(ctx, seen, true)
	ticker := time.NewTicker(sessionDiscoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.snapshotSessions(ctx, seen, false)
		}
	}
}

// snapshotSessions lists every backend's sessions once, records the ID set, and
// broadcasts "sessions_changed" for any backend whose set grew since the last scan.
func (h *Handlers) snapshotSessions(ctx context.Context, seen map[string]map[string]bool, seed bool) {
	for id, agent := range h.Agents() {
		listCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		infos, err := agent.ListSessions(listCtx)
		cancel()
		if err != nil {
			continue
		}
		current := sessionIDSet(infos)
		prev := seen[id]
		seen[id] = current
		if seed || prev == nil {
			continue
		}
		if newIDs := diffNewSessions(prev, current); len(newIDs) > 0 {
			slog.Info("go-bridge: sessions_changed (new external session detected)", "backend", id, "count", len(newIDs))
			h.deltaBatcher.Send(LogicalEvent{
				BackendID: id,
				Event:     "sessions_changed",
				Data:      map[string]interface{}{"backendId": id},
				Broadcast: true,
			})
		}
	}
}

// sessionIDSet builds a set of non-empty, trimmed session IDs.
func sessionIDSet(infos []core.AgentSessionInfo) map[string]bool {
	set := make(map[string]bool, len(infos))
	for _, info := range infos {
		sid := strings.TrimSpace(info.ID)
		if sid != "" {
			set[sid] = true
		}
	}
	return set
}

// diffNewSessions returns IDs present in current but absent from prev.
func diffNewSessions(prev, current map[string]bool) []string {
	var out []string
	for sid := range current {
		if !prev[sid] {
			out = append(out, sid)
		}
	}
	return out
}
