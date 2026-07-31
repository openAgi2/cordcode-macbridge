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
	// Critical: this goroutine must never silently die. snapshotSessions walks
	// Claude transcript files (209MB datasets, 16MB-per-line JSONL) and has, in
	// production, produced ZERO sessions_changed events across all logs since 7/5
	// — consistent with an unrecovered panic killing the watcher with no log line.
	// recover keeps the loop alive and emits a visible error so a future panic
	// can no longer hide. Control-plane only (guard #8); does not touch timeline.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("go-bridge: session discovery watcher recovered from panic — loop continuing",
				"error", r)
			// Re-arm by re-entering; ctx still governs exit.
			go h.runSessionDiscovery(ctx)
		}
	}()

	slog.Info("go-bridge: session discovery watcher started",
		"interval", sessionDiscoveryInterval.String())
	seen := map[string]map[string]bool{}
	h.snapshotSessions(ctx, seen, true)
	ticker := time.NewTicker(sessionDiscoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("go-bridge: session discovery watcher stopped (context done)")
			return
		case <-ticker.C:
			h.snapshotSessions(ctx, seen, false)
		}
	}
}

// snapshotSessions lists every backend's sessions once, records the ID set, and
// broadcasts "sessions_changed" for any backend whose set grew since the last scan.
func (h *Handlers) snapshotSessions(ctx context.Context, seen map[string]map[string]bool, seed bool) {
	tag := "poll"
	if seed {
		tag = "seed"
	}
	for id, agent := range h.Agents() {
		current, err := h.discoverySessionIDs(ctx, id, agent)
		if err != nil {
			// Previously this branch was silent. A recurring ListSessions error
			// leaves seen[id] at the seed snapshot forever → diff is always empty
			// → sessions_changed never fires, with no log to reveal why.
			slog.Warn("go-bridge: session discovery enumerate error (no broadcast)",
				"phase", tag, "backend", id, "error", err.Error())
			continue
		}
		prev := seen[id]
		seen[id] = current
		if seed || prev == nil {
			slog.Info("go-bridge: session discovery snapshot seeded",
				"backend", id, "sessionCount", len(current))
			continue
		}
		newIDs := diffNewSessions(prev, current)
		removedIDs := diffRemovedSessions(prev, current)
		slog.Info("go-bridge: session discovery snapshot",
			"backend", id, "prev", len(prev), "current", len(current), "new", len(newIDs), "removed", len(removedIDs))
		// Fire on ANY catalog change: new sessions OR removals/archives. A
		// sessions_changed signal means "the client's session list may be stale",
		// which includes sessions that disappeared (archived/deleted) — the client
		// refreshes list_sessions and its archived filter removes them. Without
		// this, archiving a session on Mac never propagated to web.
		if len(newIDs) > 0 || len(removedIDs) > 0 {
			slog.Info("go-bridge: sessions_changed (catalog changed)",
				"backend", id, "new", len(newIDs), "removed", len(removedIDs))
			h.deltaBatcher.Send(LogicalEvent{
				BackendID: id,
				Event:     "sessions_changed",
				Data:      map[string]interface{}{"backendId": id},
				Broadcast: true,
			})
		}
	}
}

// discoverySessionIDs returns the set of session IDs for a backend that this
// watcher diffs against the previous snapshot. It must enumerate the SAME set a
// client sees on list_sessions, otherwise a new session detected by the client
// is invisible to the poller and sessions_changed never fires.
//
// Claude is the special case: agent.ListSessions() resolves only the agent's
// single workDir project and returned 0 sessions in production (the workDir's
// encoded project key has no directory under ~/.claude/projects), so new Claude
// sessions — which may live under a different project dir — were never detected.
// The authoritative Claude catalog is h.claudeSessions (the global, all-project,
// fingerprinted snapshot that list_sessions serves); derive Claude IDs from it.
func (h *Handlers) discoverySessionIDs(ctx context.Context, id string, agent core.Agent) (map[string]bool, error) {
	if id == "claude" && h.claudeSessions != nil {
		all := h.claudeSessions.list("", nil)
		set := make(map[string]bool, len(all))
		for _, wire := range all {
			// Match what a client sees: archived sessions are hidden from the
			// active list (web session-grouping filters on archivedAtMillis), so
			// the poller must exclude them too — otherwise archiving a session
			// would not change the set and sessions_changed would never fire.
			if archivedMs, ok := wire["archivedAtMillis"]; ok {
				if ms, ok := archivedMs.(int64); ok && ms > 0 {
					continue
				}
				if f, ok := archivedMs.(float64); ok && f > 0 {
					continue
				}
			}
			sid, _ := wire["id"].(string)
			sid = strings.TrimSpace(sid)
			if sid != "" {
				set[sid] = true
			}
		}
		return set, nil
	}
	listCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	infos, err := agent.ListSessions(listCtx)
	if err != nil {
		return nil, err
	}
	return sessionIDSet(infos), nil
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

// diffRemovedSessions returns IDs present in prev but absent from current
// (archived/deleted sessions). Used to fire sessions_changed on removals, not
// just additions, so the client list refresh drops archived sessions.
func diffRemovedSessions(prev, current map[string]bool) []string {
	var out []string
	for sid := range prev {
		if !current[sid] {
			out = append(out, sid)
		}
	}
	return out
}
