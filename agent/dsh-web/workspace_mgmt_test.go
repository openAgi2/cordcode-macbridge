package dshweb

// workspace_mgmt_test.go — S5 置顶/归档官方语义（OD-1=A）wire 契约：形状
// 全部钉死自 A5 生产座位活体证据
// (scripts/dshweb-phase0/alpha1-workspace-mgmt-wire.json)：
//   - pin/unpin：workspace/pinSession/unpinSession {request:{sessionId}} →
//     {pinnedSessionIds:[…]}（most-recently-pinned-first）；unpin 幂等；
//     bogus → session/not-found verbatim；
//   - archive/unarchive：workspace/archiveSession {request:{sessionId,
//     stopActivity:true}} → {archivedSessionIds:[…]}；unarchive 幂等；
//   - 列表行：官方 pin 集顺序编码为稳定降序 pinnedAt（iOS 置顶区按
//     pinnedAt DESC 排序，与官方 most-recently-pinned-first 一致）。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestSetSessionPinnedOfficialWire(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["workspace/pinSession"] = fakeRPCResponse{value: map[string]any{
		"pinnedSessionIds": []string{"sess-1"}}}
	f.handlers["workspace/unpinSession"] = fakeRPCResponse{value: map[string]any{
		"pinnedSessionIds": []string{}}}

	pin, err := a.SetSessionPinned(context.Background(), "sess-1", "", true, time.UnixMilli(123).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if pin == nil || pin.SessionID != "sess-1" || !pin.PinnedAt.Equal(time.UnixMilli(123).UTC()) {
		t.Fatalf("pin = %+v", pin)
	}
	calls := methodCalls(f, "workspace/pinSession")
	if len(calls) != 1 {
		t.Fatalf("pinSession calls = %d", len(calls))
	}
	var payload struct {
		Args struct {
			Request struct {
				SessionID string `json:"sessionId"`
			} `json:"request"`
		} `json:"args"`
	}
	if err := json.Unmarshal(calls[0], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Args.Request.SessionID != "sess-1" {
		t.Fatalf("args = %+v", payload.Args.Request)
	}

	// Unpin returns nil (no pin envelope) + the official unpin call.
	unpinned, err := a.SetSessionPinned(context.Background(), "sess-1", "", false, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if unpinned != nil {
		t.Fatalf("unpin must return nil, got %+v", unpinned)
	}
	if len(methodCalls(f, "workspace/unpinSession")) != 1 {
		t.Fatal("unpinSession call missing")
	}

	// Official error verbatim: bogus pin → session/not-found.
	f.handlers["workspace/pinSession"] = fakeRPCResponse{err: &RPCError{
		Code: "session/not-found",
		Message: "cannot pin session 'session-nonexistent': live sessions and session persistence hold no such session",
		Details: json.RawMessage(`{"sessionId":"session-nonexistent"}`)}}
	_, err = a.SetSessionPinned(context.Background(), "session-nonexistent", "", true, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "session/not-found") {
		t.Fatalf("bogus pin must surface the official error verbatim, got %v", err)
	}
}

func TestListPinnedSessionsOfficialOrderEncoding(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	// Baseline pin set: B pinned after A → official order [B, A].
	a.ws.applyBaseline(&workspaceBaseline{
		PinnedSessionIds: []string{"sess-B", "sess-A"},
	})
	pins, err := a.ListPinnedSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 2 || pins[0].SessionID != "sess-B" || pins[1].SessionID != "sess-A" {
		t.Fatalf("pins = %+v, want official order [B, A]", pins)
	}
	// Stable descending pinnedAt: index 0 (most recent) sorts first on iOS.
	if !pins[0].PinnedAt.After(pins[1].PinnedAt) {
		t.Fatalf("pinnedAt encoding must descend with official order: %v vs %v",
			pins[0].PinnedAt, pins[1].PinnedAt)
	}
	// The {type:'pinned'} increment replaces the whole set.
	a.ws.applyPinned([]string{"sess-C"})
	pins, _ = a.ListPinnedSessions(context.Background())
	if len(pins) != 1 || pins[0].SessionID != "sess-C" {
		t.Fatalf("pins after increment = %+v", pins)
	}
}

func TestArchiveAndUnarchiveOfficialWire(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["workspace/archiveSession"] = fakeRPCResponse{value: map[string]any{
		"archivedSessionIds": []string{"sess-1"}}}
	f.handlers["workspace/unarchiveSession"] = fakeRPCResponse{value: map[string]any{
		"archivedSessionIds": []string{}}}
	f.handlers["session/projections"] = fakeRPCResponse{value: map[string]any{
		"asOfSeq": 0, "values": map[string]any{}}}

	archived, err := a.ArchiveSession(context.Background(), "sess-1", time.UnixMilli(500).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if archived == nil || archived.ID != "sess-1" || !archived.ArchivedAt.Equal(time.UnixMilli(500).UTC()) {
		t.Fatalf("archived = %+v", archived)
	}
	calls := methodCalls(f, "workspace/archiveSession")
	if len(calls) != 1 {
		t.Fatalf("archiveSession calls = %d", len(calls))
	}
	var payload struct {
		Args struct {
			Request struct {
				SessionID    string `json:"sessionId"`
				StopActivity bool   `json:"stopActivity"`
			} `json:"request"`
		} `json:"args"`
	}
	if err := json.Unmarshal(calls[0], &payload); err != nil {
		t.Fatal(err)
	}
	// stopActivity:true — the only official form that succeeds for active
	// sessions (A5 negative evidence: plain archive → workspace/session-active).
	if payload.Args.Request.SessionID != "sess-1" || !payload.Args.Request.StopActivity {
		t.Fatalf("args = %+v, want {sessionId:sess-1, stopActivity:true}", payload.Args.Request)
	}

	restored, err := a.UnarchiveSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if restored == nil || restored.ID != "sess-1" || !restored.ArchivedAt.IsZero() {
		t.Fatalf("restored = %+v", restored)
	}
	if len(methodCalls(f, "workspace/unarchiveSession")) != 1 {
		t.Fatal("unarchiveSession call missing")
	}
}

func TestListSessionsStampsOfficialPinOrder(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session/list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"sessionId": "sess-A", "updatedAt": 1000},
			{"sessionId": "sess-B", "updatedAt": 2000},
			{"sessionId": "sess-plain", "updatedAt": 3000},
		}}}
	a.ws.applyBaseline(&workspaceBaseline{
		PinnedSessionIds: []string{"sess-B", "sess-A"},
	})
	rows, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pinned := map[string]time.Time{}
	for _, r := range rows {
		if !r.PinnedAt.IsZero() {
			pinned[r.ID] = r.PinnedAt
		}
	}
	if len(pinned) != 2 {
		t.Fatalf("pinned rows = %v, want sess-A + sess-B", pinned)
	}
	if !pinned["sess-B"].After(pinned["sess-A"]) {
		t.Fatalf("official order must encode as descending pinnedAt: B=%v A=%v",
			pinned["sess-B"], pinned["sess-A"])
	}
}

var _ core.SessionUnarchiver = (*Agent)(nil)
