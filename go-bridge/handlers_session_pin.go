package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Handlers for session pinning (置顶). See docs/protocol/bridge-v1.md「Session Pinning」.
//
// The pin store holds identity + pinnedAtMillis only (no summaries). These handlers:
//   - set_session_pinned: write the pin via the agent's SessionPinner receiver, then enrich
//     the returned identity-only SessionPin into a full wire session by resolving the real
//     backend summary (Claude catalog / Codex ListSessions / OpenCode proxy.getSession).
//   - list_pinned_sessions: enumerate pins across the backend, enrich each, prune a
//     definitively-missing session (e.g. OpenCode 404), and fail truthfully on transient
//     upstream error (no fabricated/partial summaries).
//
// list_sessions responses also get a pinnedAtMillis overlay so pinned rows carry pin state
// wherever they appear.

// pinEnrichSem caps concurrent upstream fan-out during list_pinned_sessions enrichment
// (OpenCode resolves N pins via N GET /session/<id> calls). Small bound keeps sidebar
// load off the critical path.
var pinEnrichSem = make(chan struct{}, 6)

func (h *Handlers) handleSetSessionPinned(conn Connection, msg WireMessage, agent core.Agent) {
	pinner, ok := agent.(core.SessionPinner)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "session pinning not supported for this backend"})
		return
	}
	var params struct {
		SessionID        string  `json:"sessionId"`
		Pinned           bool    `json:"pinned"`
		PinnedAtMillis   float64 `json:"pinnedAtMillis"`
		Directory        string  `json:"directory"`
	}
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.SessionID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}
	pinnedAt := time.Now().UTC()
	if params.PinnedAtMillis > 0 {
		pinnedAt = time.UnixMilli(int64(params.PinnedAtMillis)).UTC()
	}

	pin, err := pinner.SetSessionPinned(context.Background(), params.SessionID, params.Directory, params.Pinned, pinnedAt)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "pin_failed", Message: err.Error()})
		return
	}
	if pin == nil {
		// Unpin: no session envelope to return (mirrors the absence of a pin).
		conn.SendResult(msg.RequestID, map[string]interface{}{"session": nil}, nil)
		return
	}
	wire, gone, err := h.resolvePinWire(context.Background(), agent, *pin)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "pin_failed", Message: "pin recorded but summary resolution failed: " + err.Error()})
		return
	}
	if gone {
		// Session disappeared between pin and enrichment: keep the pin (it may be a transient
		// list/cache miss) but report truthfully. This is rare for a user-initiated pin.
		wire = minimalPinWire(*pin)
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"session": wire}, nil)
}

func (h *Handlers) handleListPinnedSessions(conn Connection, msg WireMessage, agent core.Agent) {
	pinner, ok := agent.(core.SessionPinner)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "session pinning not supported for this backend"})
		return
	}
	pins, err := pinner.ListPinnedSessions(context.Background())
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "pin_list_failed", Message: err.Error()})
		return
	}

	// Resolve summaries with bounded fan-out. Snapshot the pin identities first (the store
	// lock is NOT held during upstream calls); prune confirmed-gone entries after.
	out := make([]map[string]interface{}, 0, len(pins))
	var (
		pruneMu   sync.Mutex
		toPrune   []core.SessionPin
		firstErr  error
	)
	var wg sync.WaitGroup
	for i := range pins {
		pin := pins[i]
		wg.Add(1)
		pinEnrichSem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-pinEnrichSem }()
			wire, gone, err := h.resolvePinWire(context.Background(), agent, pin)
			if err != nil {
				pruneMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				pruneMu.Unlock()
				return
			}
			if gone {
				pruneMu.Lock()
				toPrune = append(toPrune, pin)
				pruneMu.Unlock()
				return
			}
			pruneMu.Lock()
			out = append(out, wire)
			pruneMu.Unlock()
		}()
	}
	wg.Wait()

	if firstErr != nil {
		// Strict-fail: one transient upstream error fails the RPC rather than rendering a
		// partially fabricated section. Confirmed-gone prunes are still applied below so a
		// retry is cleaner.
		h.prunePins(agent, toPrune)
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "pin_list_failed", Message: "failed to resolve pinned session summaries: " + firstErr.Error()})
		return
	}
	h.prunePins(agent, toPrune)
	conn.SendResult(msg.RequestID, map[string]interface{}{"sessions": out}, nil)
}

func (h *Handlers) prunePins(agent core.Agent, pins []core.SessionPin) {
	if h.pinStore == nil || len(pins) == 0 {
		return
	}
	backendID := agentBackendID(agent)
	for _, p := range pins {
		if !h.pinStore.RemoveEntry(backendID, p.SessionID, p.Directory) {
			// Fallback: scope-keyed entry; sweep by sessionID across scopes.
			h.pinStore.RemoveAll(backendID, p.SessionID)
		}
	}
}

// resolvePinWire builds the wire session map for a pin by resolving the real backend
// summary. Returns (wire, gone, err): gone=true means definitively missing (prune); err
// non-nil means transient upstream failure (fail the RPC).
func (h *Handlers) resolvePinWire(ctx context.Context, agent core.Agent, pin core.SessionPin) (map[string]interface{}, bool, error) {
	backendID := agentBackendID(agent)
	switch backendID {
	case "claudecode":
		return h.resolveClaudePin(pin)
	case "opencode":
		return h.resolveOpenCodePin(pin)
	default: // codex and any future file-backed backend
		return h.resolveAgentListPin(ctx, agent, pin)
	}
}

// resolveClaudePin looks the session up in the global Claude catalog (cached), which
// already scans every project transcript and sidecar. Not found => definitively gone.
func (h *Handlers) resolveClaudePin(pin core.SessionPin) (map[string]interface{}, bool, error) {
	if h.claudeSessions == nil {
		return nil, false, fmt.Errorf("claude session catalog not initialized")
	}
	all := h.claudeSessions.list("", nil)
	for _, w := range all {
		if id, _ := w["id"].(string); id == pin.SessionID {
			w["pinnedAtMillis"] = pin.PinnedAt.UnixMilli()
			return w, false, nil
		}
	}
	return nil, true, nil // not in catalog => gone
}

// resolveAgentListPin resolves via the agent's own ListSessions (Codex sessionListCache).
// Used for codex and any future backend whose list is agent-owned. Not found => gone.
func (h *Handlers) resolveAgentListPin(ctx context.Context, agent core.Agent, pin core.SessionPin) (map[string]interface{}, bool, error) {
	lister, ok := agent.(interface {
		ListSessions(context.Context) ([]core.AgentSessionInfo, error)
	})
	if !ok {
		// Agent has no list; return a minimal identity-only wire so the pin still shows.
		return minimalPinWire(pin), false, nil
	}
	sessions, err := lister.ListSessions(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, s := range sessions {
		if s.ID == pin.SessionID {
			wire := sessionsToWire([]core.AgentSessionInfo{s})[0]
			wire["pinnedAtMillis"] = pin.PinnedAt.UnixMilli()
			return wire, false, nil
		}
	}
	return nil, true, nil // not found => gone
}

// resolveOpenCodePin resolves via the OpenCode HTTP proxy. A 404 is a definitive "gone"
// (prune); any other upstream failure is transient (fail the RPC).
func (h *Handlers) resolveOpenCodePin(pin core.SessionPin) (map[string]interface{}, bool, error) {
	if h.ocProxy == nil {
		return nil, false, fmt.Errorf("opencode proxy not configured")
	}
	raw, err := h.ocProxy.getSession(pin.SessionID, pin.Directory)
	if err != nil {
		if IsOpenCodeNotFound(err) {
			return nil, true, nil // definitively gone
		}
		return nil, false, err // transient
	}
	wire := mapSession(raw)
	wire["pinnedAtMillis"] = pin.PinnedAt.UnixMilli()
	return wire, false, nil
}

// minimalPinWire is the identity-only fallback when no backend summary is available.
func minimalPinWire(pin core.SessionPin) map[string]interface{} {
	wire := map[string]interface{}{
		"id":             pin.SessionID,
		"title":          pin.SessionID,
		"backendId":      pin.BackendID,
		"pinnedAtMillis": pin.PinnedAt.UnixMilli(),
	}
	if pin.Directory != "" {
		wire["directory"] = pin.Directory
	}
	return wire
}

// overlayPinnedState sets pinnedAtMillis on list_sessions rows that are currently pinned,
// so a pinned session carries pin state wherever it appears in the regular list. Best-effort:
// if the store is unavailable, rows are returned unchanged.
func (h *Handlers) overlayPinnedState(wireSessions []map[string]interface{}, backendID string) {
	if h.pinStore == nil || len(wireSessions) == 0 {
		return
	}
	for _, w := range wireSessions {
		id, _ := w["id"].(string)
		if id == "" {
			continue
		}
		// OpenCode: scope by the row's own directory (most precise). Others: any scope.
		if backendID == "opencode" {
			if dir, _ := w["directory"].(string); dir != "" {
				if e, ok := h.pinStore.Get(backendID, pinScopeAbs(dir), id); ok {
					w["pinnedAtMillis"] = e.PinnedAtMillis
					continue
				}
			}
		}
		if e, ok := h.pinStore.GetAnyScope(backendID, id); ok {
			w["pinnedAtMillis"] = e.PinnedAtMillis
		}
	}
}

// pinScopeAbs normalizes a directory the same way agent/opencode/session_pin.go pinScope
// does (absolute path), so the list overlay matches the key used at pin time.
func pinScopeAbs(directory string) string {
	d := strings.TrimSpace(directory)
	if d == "" {
		return ""
	}
	abs, err := filepath.Abs(d)
	if err != nil {
		return filepath.Clean(d)
	}
	return abs
}

// agentBackendID returns the backend identifier used in the pin store. core.Agent.Name()
// returns "claudecode" / "codex" / "opencode", matching the pin store BackendID values.
func agentBackendID(agent core.Agent) string {
	return agent.Name()
}
