package gobridge

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

// captureConn records the last SendResult for assertions.
type pinTestConn struct {
	mu     sync.Mutex
	data   interface{}
	err    *WireError
	reqID  string
}

func (c *pinTestConn) SendJSON(any) {}
func (c *pinTestConn) SendResult(reqID string, data interface{}, err *WireError) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reqID = reqID
	c.data = data
	c.err = err
}
func (c *pinTestConn) SendEvent(string, string, string, interface{}) {}
func (c *pinTestConn) AuthedDevice() *TrustedDeviceRecord            { return nil }
func (c *pinTestConn) RemoteAddr() string                             { return "test:pin" }
func (c *pinTestConn) Close() error                                   { return nil }

// pinFakeAgent implements core.Agent (Name + StartSession + ListSessions + Stop) plus
// core.SessionPinner backed by a real pinstore. It does NOT implement SessionRenamer /
// SessionArchiver, so capability independence can be asserted.
type pinFakeAgent struct {
	name     string
	store    *pinstore.Store
	scope    string // pin key scope
	list     []core.AgentSessionInfo
	listErr  error
}

func (a *pinFakeAgent) Name() string { return a.name }
func (a *pinFakeAgent) StartSession(context.Context, string) (core.AgentSession, error) {
	return nil, errors.New("not used")
}
func (a *pinFakeAgent) Stop() error { return nil }
func (a *pinFakeAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return a.list, a.listErr
}

func (a *pinFakeAgent) SetSessionPinned(_ context.Context, sessionID, directory string, pinned bool, pinnedAt time.Time) (*core.SessionPin, error) {
	if a.store == nil {
		return nil, pinstore.ErrStoreUnavailable
	}
	if pinned && pinnedAt.IsZero() {
		pinnedAt = time.Now().UTC()
	}
	if err := a.store.SetPinned(a.name, a.scope, sessionID, directory, pinned, pinnedAt); err != nil {
		return nil, err
	}
	if !pinned {
		return nil, nil
	}
	return &core.SessionPin{BackendID: a.name, SessionID: sessionID, Directory: directory, PinnedAt: pinnedAt.UTC()}, nil
}
func (a *pinFakeAgent) ListPinnedSessions(context.Context) ([]core.SessionPin, error) {
	if a.store == nil {
		return nil, pinstore.ErrStoreUnavailable
	}
	entries := a.store.List(a.name)
	out := make([]core.SessionPin, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ToPin())
	}
	return out, nil
}

func newPinHandlers(t *testing.T) (*Handlers, *pinstore.Store) {
	t.Helper()
	h := NewHandlers()
	store := pinstore.New(t.TempDir())
	h.SetPinStore(store)
	return h, store
}

func pinMsg(method, backendID string, params map[string]interface{}) WireMessage {
	var raw json.RawMessage
	if params != nil {
		raw, _ = json.Marshal(params)
	}
	return WireMessage{RequestID: "req-1", BackendID: backendID, Method: method, Params: raw}
}

func TestDeriveBackendCapabilities_SessionPin(t *testing.T) {
	agent := &pinFakeAgent{name: "codex"} // implements SessionPinner, NOT renamer/archiver
	caps := deriveBackendCapabilities("codex", agent, "exec")
	if !contains(caps, "session_pin") {
		t.Fatalf("session_pin not advertised for SessionPinner agent: %v", caps)
	}
	// Codex lacks rename/archive, so session_mutation must NOT be advertised — proves
	// session_pin is independent of the mutation family (DC-1).
	if contains(caps, "session_mutation") {
		t.Fatalf("session_mutation must not be advertised without renamer+archiver: %v", caps)
	}
}

func TestSetSessionPinnedReturnsPinnedAtMillis(t *testing.T) {
	h, store := newPinHandlers(t)
	pt := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	agent := &pinFakeAgent{name: "codex", store: store, scope: "/home/.codex",
		list: []core.AgentSessionInfo{{ID: "s1", Summary: "hello", MessageCount: 3, ModifiedAt: pt}}}

	conn := &pinTestConn{}
	h.handleSetSessionPinned(conn, pinMsg("set_session_pinned", "codex", map[string]interface{}{
		"sessionId": "s1", "pinned": true, "pinnedAtMillis": float64(pt.UnixMilli()), "directory": "/work",
	}), agent)

	if conn.err != nil {
		t.Fatalf("unexpected error: %+v", conn.err)
	}
	env, ok := conn.data.(map[string]interface{})
	if !ok || env["session"] == nil {
		t.Fatalf("expected session envelope, got %+v", conn.data)
	}
	sess := env["session"].(map[string]interface{})
	if sess["id"] != "s1" {
		t.Fatalf("id=%v want s1", sess["id"])
	}
	if sess["pinnedAtMillis"] != pt.UnixMilli() {
		t.Fatalf("pinnedAtMillis=%v want=%d", sess["pinnedAtMillis"], pt.UnixMilli())
	}
	if sess["title"] != "hello" {
		t.Fatalf("summary not enriched from ListSessions: title=%v", sess["title"])
	}
}

func TestListPinnedSessionsEnrichesFromList(t *testing.T) {
	h, store := newPinHandlers(t)
	pt := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	agent := &pinFakeAgent{name: "codex", store: store, scope: "h",
		list: []core.AgentSessionInfo{
			{ID: "s1", Summary: "alpha"},
			{ID: "s2", Summary: "bravo"},
		}}
	// Pin two sessions; one (s3) will NOT be in the list -> definitively gone -> pruned.
	for _, sid := range []string{"s1", "s2", "s3"} {
		if _, err := agent.SetSessionPinned(context.Background(), sid, "/w", true, pt); err != nil {
			t.Fatalf("SetSessionPinned %s: %v", sid, err)
		}
	}

	conn := &pinTestConn{}
	h.handleListPinnedSessions(conn, pinMsg("list_pinned_sessions", "codex", nil), agent)
	if conn.err != nil {
		t.Fatalf("unexpected error: %+v", conn.err)
	}
	env := conn.data.(map[string]interface{})
	sessions := env["sessions"].([]map[string]interface{})
	if len(sessions) != 2 {
		t.Fatalf("expected 2 enriched pins (s3 pruned), got %d", len(sessions))
	}
	// s3 must have been pruned from the store.
	if _, ok := store.Get("codex", "h", "s3"); ok {
		t.Fatal("s3 was not pruned despite being gone from ListSessions")
	}
	// Each enriched row carries pinnedAtMillis.
	for _, s := range sessions {
		if _, ok := s["pinnedAtMillis"]; !ok {
			t.Fatalf("pinned row missing pinnedAtMillis: %+v", s)
		}
	}
}

func TestListPinnedSessionsFailsOnTransientError(t *testing.T) {
	h, store := newPinHandlers(t)
	pt := time.Now().UTC()
	agent := &pinFakeAgent{name: "codex", store: store, scope: "h",
		listErr: errors.New("upstream 503")}
	if _, err := agent.SetSessionPinned(context.Background(), "s1", "/w", true, pt); err != nil {
		t.Fatalf("SetSessionPinned: %v", err)
	}

	conn := &pinTestConn{}
	h.handleListPinnedSessions(conn, pinMsg("list_pinned_sessions", "codex", nil), agent)
	if conn.err == nil {
		t.Fatal("expected transient error to fail the RPC, got nil")
	}
	if conn.err.Code != "pin_list_failed" {
		t.Fatalf("error code=%q want pin_list_failed", conn.err.Code)
	}
	// Pin must NOT be pruned on transient failure (only confirmed-gone prunes).
	if _, ok := store.Get("codex", "h", "s1"); !ok {
		t.Fatal("pin pruned on transient failure; should be retained for retry")
	}
}

func TestSetSessionPinnedMissingSessionId(t *testing.T) {
	h, _ := newPinHandlers(t)
	agent := &pinFakeAgent{name: "codex"}
	conn := &pinTestConn{}
	h.handleSetSessionPinned(conn, pinMsg("set_session_pinned", "codex", map[string]interface{}{
		"pinned": true,
	}), agent)
	if conn.err == nil || conn.err.Code != "missing_param" {
		t.Fatalf("expected missing_param, got %+v", conn.err)
	}
}

func TestSetSessionPinnedUnsupportedBackend(t *testing.T) {
	h, _ := newPinHandlers(t)
	// bareAgent does not implement SessionPinner.
	bare := &bareAgent{name: "codex"}
	conn := &pinTestConn{}
	h.handleSetSessionPinned(conn, pinMsg("set_session_pinned", "codex", map[string]interface{}{
		"sessionId": "s1", "pinned": true,
	}), bare)
	if conn.err == nil || conn.err.Code != "not_supported" {
		t.Fatalf("expected not_supported, got %+v", conn.err)
	}
}

// bareAgent implements only the core.Agent base surface (no SessionPinner).
type bareAgent struct{ name string }

func (b *bareAgent) Name() string                                       { return b.name }
func (b *bareAgent) StartSession(context.Context, string) (core.AgentSession, error) { return nil, errors.New("no") }
func (b *bareAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error)   { return nil, nil }
func (b *bareAgent) Stop() error                                        { return nil }

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
