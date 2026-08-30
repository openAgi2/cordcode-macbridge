package codexremote

// history_paginated_test.go drives the lazy-history primitives with the
// scenario shapes from the official thread_read.rs pagination baselines and
// the G0 live fixtures (attempt-009/010): summary pages in network desc
// order, items pages in asc order with per-entry turnId, cursor chaining,
// EOF via nextCursor=nil, and the owner-frozen resource gates.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type rpcCall struct {
	Method string
	Params map[string]any
}

func paginatedFake(t *testing.T, handler func(call rpcCall) (any, *RPCError)) (*Agent, *[]rpcCall) {
	t.Helper()
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_probe_"+strings.ToLower(t.Name()))
	t.Cleanup(func() { stream.Close() })
	calls := &[]rpcCall{}
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		call := rpcCall{Method: method, Params: map[string]any{}}
		_ = json.Unmarshal(params, &call.Params)
		*calls = append(*calls, call)
		return handler(call)
	})
	cl := NewClient(stream, 1)
	t.Cleanup(func() { cl.Close() })
	agent := New(nil)
	agent.BindClient(cl)
	return agent, calls
}

func threadMetaResult(historyMode string) map[string]any {
	return map[string]any{"thread": map[string]any{"id": "thread_probe", "historyMode": historyMode, "status": "idle"}}
}

func summaryTurn(id, status string, items ...map[string]any) map[string]any {
	return map[string]any{"id": id, "status": status, "itemsView": "summary", "items": items}
}

func itemEntry(turnID string, item map[string]any) map[string]any {
	return map[string]any{"turnId": turnID, "item": item}
}

// TestReadThreadSummaryFetchesMetaOnFirstPageOnly covers the §2.4 primitive:
// cursor=="" performs thread/read metadata + page 1; an older-page cursor
// fetches only the page.
func TestReadThreadSummaryFetchesMetaOnFirstPageOnly(t *testing.T) {
	agent, calls := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		switch call.Method {
		case "thread/read":
			if _, ok := call.Params["includeTurns"]; ok {
				t.Error("metadata read must omit includeTurns")
			}
			return threadMetaResult("paginated"), nil
		case "thread/turns/list":
			return map[string]any{"data": []any{summaryTurn("turn_new", "completed")}}, nil
		}
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})

	first, err := agent.ReadThreadSummary(context.Background(), "thread_probe", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Meta == nil || first.Meta.HistoryMode != "paginated" {
		t.Fatalf("meta = %+v", first.Meta)
	}
	if len(first.Page.Turns) != 1 || first.Page.Turns[0].ID != "turn_new" || !first.Page.EOF {
		t.Fatalf("page = %+v", first.Page)
	}
	if first.Page.Turns[0].ItemsView != remoteTurnItemsViewSummary {
		t.Fatalf("itemsView not preserved: %+v", first.Page.Turns[0])
	}

	older, err := agent.ReadThreadSummary(context.Background(), "thread_probe", "cur-old")
	if err != nil {
		t.Fatal(err)
	}
	if older.Meta != nil {
		t.Fatal("older pages must not re-read metadata")
	}
	for _, call := range *calls {
		if call.Method == "thread/turns/list" && call.Params["cursor"] != nil && call.Params["cursor"] != "cur-old" {
			// only the older walk passes a cursor
		}
	}
	olderCursorSeen := false
	for _, call := range *calls {
		if call.Method == "thread/turns/list" {
			if call.Params["itemsView"] != remoteTurnItemsViewSummary || call.Params["sortDirection"] != "desc" {
				t.Fatalf("turns/list params = %+v", call.Params)
			}
			if call.Params["cursor"] == "cur-old" {
				olderCursorSeen = true
			}
		}
	}
	if !olderCursorSeen {
		t.Fatal("older page must forward the upstream cursor")
	}
}

// TestPaginatedHistoryReversesNetworkOrderAndWindowsByLimit mirrors the
// official desc pagination: pages arrive newest→oldest, the mapped history is
// oldest→newest, and limit keeps the most recent window.
func TestPaginatedHistoryReversesNetworkOrderAndWindowsByLimit(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		switch call.Method {
		case "thread/read":
			return threadMetaResult("paginated"), nil
		case "thread/turns/list":
			if call.Params["cursor"] == nil {
				return map[string]any{
					"data":       []any{summaryTurn("turn_3", "completed"), summaryTurn("turn_2", "completed")},
					"nextCursor": "cur-page-2",
				}, nil
			}
			return map[string]any{"data": []any{summaryTurn("turn_1", "completed")}}, nil
		case "thread/items/list":
			return map[string]any{"data": []any{itemEntry(turnIDOf(call), map[string]any{"type": "agentMessage", "id": "a_" + turnIDOf(call), "text": "t"})}}, nil
		}
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})

	turns, err := agent.GetTurnScopedRichHistory(context.Background(), "thread_probe", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 3 || turns[0].TurnID != "turn_1" || turns[2].TurnID != "turn_3" {
		t.Fatalf("order = %+v", turns)
	}
	windowed, err := agent.GetTurnScopedRichHistory(context.Background(), "thread_probe", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(windowed) != 2 || windowed[0].TurnID != "turn_2" || windowed[1].TurnID != "turn_3" {
		t.Fatalf("limit window = %+v", windowed)
	}
}

func turnIDOf(call rpcCall) string {
	if id, ok := call.Params["turnId"].(string); ok {
		return id
	}
	return ""
}

// TestLegacyHistoryUsesCompatFullReadOnlyOnExplicitMode pins the owner T0.5
// constraint: historyMode=legacy dispatches to thread/read includeTurns=true,
// and paginated threads never trigger that call.
func TestLegacyHistoryUsesCompatFullReadOnlyOnExplicitMode(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		switch call.Method {
		case "thread/read":
			if includeTurns, ok := call.Params["includeTurns"].(bool); ok && includeTurns {
				return map[string]any{"thread": map[string]any{
					"id": "thread_probe", "historyMode": "legacy", "status": "idle",
					"turns": []any{map[string]any{
						"id": "turn_legacy", "status": "completed",
						"items": []any{map[string]any{"type": "userMessage", "id": "u1", "text": "old world"}},
					}},
				}}, nil
			}
			return threadMetaResult("legacy"), nil
		}
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})

	turns, err := agent.GetTurnScopedRichHistory(context.Background(), "thread_probe", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].UserText != "old world" {
		t.Fatalf("legacy turns = %+v", turns)
	}
}

func TestUnknownHistoryModeFailsClosed(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		if call.Method == "thread/read" {
			return threadMetaResult(""), nil
		}
		if call.Method == "thread/read" || call.Method == "thread/turns/list" || call.Method == "thread/items/list" {
			t.Errorf("unexpected call after unknown historyMode: %s", call.Method)
		}
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})
	_, err := agent.GetTurnScopedRichHistory(context.Background(), "thread_probe", 0)
	if !errors.Is(err, ErrUnknownHistoryMode) {
		t.Fatalf("err = %v", err)
	}
}

// TestTurnItemsMirrorsOfficialInvariants asserts the request shapes of the
// items loop: fixed turnId, asc, frozen page limit, cursor chaining, and EOF
// via nextCursor=nil (the only EOF signal).
func TestTurnItemsMirrorsOfficialInvariants(t *testing.T) {
	agent, calls := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		if call.Method != "thread/items/list" {
			return nil, &RPCError{Code: -32601, Message: call.Method}
		}
		if call.Params["cursor"] == nil {
			return map[string]any{
				"data":       []any{itemEntry("turn_items", map[string]any{"type": "userMessage", "id": "u1", "text": "q"})},
				"nextCursor": "cur-items-2",
			}, nil
		}
		return map[string]any{"data": []any{itemEntry("turn_items", map[string]any{"type": "agentMessage", "id": "a1", "text": "r"})}}, nil
	})

	entries, err := agent.ReadTurnItems(context.Background(), "thread_probe", "turn_items")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Item.ID != "u1" || entries[1].Item.ID != "a1" {
		t.Fatalf("entries = %+v", entries)
	}
	for _, call := range *calls {
		if call.Method != "thread/items/list" {
			continue
		}
		if call.Params["turnId"] != "turn_items" {
			t.Fatalf("turnId drifted: %+v", call.Params)
		}
		if call.Params["sortDirection"] != "asc" {
			t.Fatalf("sortDirection = %v", call.Params["sortDirection"])
		}
		if limit, ok := call.Params["limit"].(float64); !ok || int(limit) != remoteItemsPageLimit {
			t.Fatalf("limit = %v", call.Params["limit"])
		}
	}
	second := (*calls)[1]
	if second.Params["cursor"] != "cur-items-2" {
		t.Fatalf("cursor chaining = %+v", second.Params)
	}
}

// TestTurnItemsUnknownTurnIsEmptySuccess pins the G0-amended official filter
// semantics: an unknown-but-well-formed turnId yields an empty page, never an
// error, never foreign content.
func TestTurnItemsUnknownTurnIsEmptySuccess(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		return map[string]any{"data": []any{}}, nil
	})
	entries, err := agent.ReadTurnItems(context.Background(), "thread_probe", "turn_missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v", entries)
	}
}

// TestReadTurnItemsAcceptsAllTenOfficialTypes keeps the detail decoder at
// the official ten-variant union floor (schema replay covers decode; proven
// claims stay limited to live-observed types per plan T1.4).
func TestReadTurnItemsAcceptsAllTenOfficialTypes(t *testing.T) {
	items := []map[string]any{
		{"type": "userMessage", "id": "i1", "text": "q"},
		{"type": "agentMessage", "id": "i2", "text": "a"},
		{"type": "reasoning", "id": "i3", "summary": []string{"s"}, "content": []string{}},
		{"type": "commandExecution", "id": "i4", "command": "ls", "status": "completed"},
		{"type": "fileChange", "id": "i5", "status": "completed", "changes": []any{}},
		{"type": "mcpToolCall", "id": "i6", "server": "s", "tool": "t", "status": "completed"},
		{"type": "dynamicToolCall", "id": "i7", "tool": "d", "status": "completed"},
		{"type": "plan", "id": "i8", "text": "p"},
		{"type": "webSearch", "id": "i9", "query": "q"},
		{"type": "contextCompaction", "id": "i10"},
	}
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		data := make([]any, 0, len(items))
		for _, item := range items {
			data = append(data, itemEntry("turn_all", item))
		}
		return map[string]any{"data": data}, nil
	})
	entries, err := agent.ReadTurnItems(context.Background(), "thread_probe", "turn_all")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Fatalf("entries = %d", len(entries))
	}
}

func TestTurnItemsForeignTurnEntryFails(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		return map[string]any{"data": []any{itemEntry("turn_other", map[string]any{"type": "agentMessage", "id": "a1", "text": "x"})}}, nil
	})
	if _, err := agent.ReadTurnItems(context.Background(), "thread_probe", "turn_items"); !errors.Is(err, ErrForeignTurnItem) {
		t.Fatalf("err = %v", err)
	}
}

func TestTurnItemsUnknownVariantFailsAtomically(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		return map[string]any{"data": []any{itemEntry("turn_items", map[string]any{"type": "functionCallOutput", "id": "future1"})}}, nil
	})
	if _, err := agent.ReadTurnItems(context.Background(), "thread_probe", "turn_items"); !errors.Is(err, ErrUnknownThreadItem) {
		t.Fatalf("err = %v", err)
	}
}

func TestTurnItemsRepeatedCursorFailsImmediately(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		return map[string]any{
			"data":       []any{itemEntry("turn_items", map[string]any{"type": "agentMessage", "id": "a1", "text": "x"})},
			"nextCursor": "cur-stuck",
		}, nil
	})
	if _, err := agent.ReadTurnItems(context.Background(), "thread_probe", "turn_items"); !errors.Is(err, ErrRepeatedCursor) {
		t.Fatalf("err = %v", err)
	}
}

func TestTurnItemsMaxPagesGate(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		cursor, _ := call.Params["cursor"].(string)
		return map[string]any{
			"data":       []any{itemEntry("turn_items", map[string]any{"type": "agentMessage", "id": "a1", "text": "x"})},
			"nextCursor": "cur-" + cursor + "-next",
		}, nil
	})
	if _, err := agent.ReadTurnItems(context.Background(), "thread_probe", "turn_items"); !errors.Is(err, ErrTurnItemsMaxPages) {
		t.Fatalf("err = %v", err)
	}
}

func TestTurnItemsMaxBytesGate(t *testing.T) {
	big := strings.Repeat("x", 60*1024)
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		cursor, _ := call.Params["cursor"].(string)
		return map[string]any{
			"data":       []any{itemEntry("turn_items", map[string]any{"type": "commandExecution", "id": "c1", "aggregatedOutput": big})},
			"nextCursor": "cur-" + cursor + "-more",
		}, nil
	})
	if _, err := agent.ReadTurnItems(context.Background(), "thread_probe", "turn_items"); !errors.Is(err, ErrTurnItemsMaxBytes) {
		t.Fatalf("err = %v", err)
	}
}

func TestTurnItemsTotalDeadlineGate(t *testing.T) {
	previous := remoteTurnItemsDeadline
	remoteTurnItemsDeadline = 150 * time.Millisecond
	t.Cleanup(func() { remoteTurnItemsDeadline = previous })
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		time.Sleep(400 * time.Millisecond)
		return map[string]any{"data": []any{}}, nil
	})
	if _, err := agent.ReadTurnItems(context.Background(), "thread_probe", "turn_items"); !errors.Is(err, ErrTurnItemsTimeout) {
		t.Fatalf("err = %v", err)
	}
}

// TestTurnItemsDetailFailureFailsWholeHistoryAtomic pins §2.2 for the
// interim rich-history path: one turn's detail failure must never yield a
// partial history.
func TestTurnItemsDetailFailureFailsWholeHistoryAtomic(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		switch call.Method {
		case "thread/read":
			return threadMetaResult("paginated"), nil
		case "thread/turns/list":
			return map[string]any{"data": []any{summaryTurn("turn_ok", "completed"), summaryTurn("turn_bad", "completed")}}, nil
		case "thread/items/list":
			if turnIDOf(call) == "turn_bad" {
				return map[string]any{"data": []any{itemEntry("turn_bad", map[string]any{"type": "totallyNew", "id": "x"})}}, nil
			}
			return map[string]any{"data": []any{itemEntry("turn_ok", map[string]any{"type": "agentMessage", "id": "a1", "text": "ok"})}}, nil
		}
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})
	turns, err := agent.GetTurnScopedRichHistory(context.Background(), "thread_probe", 0)
	if err == nil {
		t.Fatalf("expected atomic failure, got %+v", turns)
	}
	if !errors.Is(err, ErrUnknownThreadItem) {
		t.Fatalf("err = %v", err)
	}
}

// TestReasoningMappingIsSummaryOnlyPerG05 drives the four-state matrix from
// the G0 samples: summary-only maps; content is never used as a fallback and
// never leaks; empty summary yields no placeholder part.
func TestReasoningMappingIsSummaryOnlyPerG05(t *testing.T) {
	thread := &remoteThread{ID: "thread_g05", Turns: []remoteTurn{{
		ID: "turn_g05", Status: remoteTurnStatusCompleted,
		Items: []json.RawMessage{
			json.RawMessage(`{"type":"reasoning","id":"r1","summary":["step one"],"content":["tail that must not surface"]}`),
			json.RawMessage(`{"type":"reasoning","id":"r2","summary":[],"content":["content only"]}`),
			json.RawMessage(`{"type":"reasoning","id":"r3","summary":[],"content":[]}`),
		},
	}}}
	turn := mapRemoteHistoryTurns(thread, 0)[0]
	if len(turn.Parts) != 1 {
		t.Fatalf("parts = %+v", turn.Parts)
	}
	if turn.Parts[0]["content"] != "step one" || turn.Parts[0]["itemId"] != "r1" {
		t.Fatalf("reasoning part = %+v", turn.Parts[0])
	}
}

// TestInProgressTurnUsesSummaryPage keeps the live-attach helper off the
// includeTurns path: turns/list serves both history modes (G0 id-143).
func TestInProgressTurnUsesSummaryPage(t *testing.T) {
	agent, calls := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		switch call.Method {
		case "thread/turns/list":
			return map[string]any{"data": []any{
				summaryTurn("turn_done", "completed"),
				summaryTurn("turn_live", remoteTurnStatusInProgress),
			}}, nil
		}
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})
	if got := agent.inProgressTurn(context.Background(), "thread_probe"); got != "turn_live" {
		t.Fatalf("inProgressTurn = %q", got)
	}
	for _, call := range *calls {
		if call.Method == "thread/read" {
			t.Fatalf("inProgressTurn must not call thread/read: %+v", call.Params)
		}
	}
}

// TestReadUpstreamHistoryPageReversesToAscending drives the bridge-facing pager
// primitive (T2.1 §2.4): one desc summary page per call, mapped to ascending
// kernel order, upstream cursor passed through untouched, EOF as "".
func TestReadUpstreamHistoryPageReversesToAscending(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		switch call.Method {
		case "thread/read":
			return threadMetaResult("paginated"), nil
		case "thread/turns/list":
			if call.Params["cursor"] == nil {
				return map[string]any{
					"data": []any{
						summaryTurn("turn_3", "completed",
							map[string]any{"type": "userMessage", "id": "u3", "text": "q3"},
							map[string]any{"type": "agentMessage", "id": "a3", "text": "r3"}),
						summaryTurn("turn_2", "completed",
							map[string]any{"type": "userMessage", "id": "u2", "text": "q2"}),
					},
					"nextCursor": "cur-old",
				}, nil
			}
			return map[string]any{
				"data": []any{summaryTurn("turn_1", "completed",
					map[string]any{"type": "userMessage", "id": "u1", "text": "q1"})},
			}, nil
		}
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})

	first, err := agent.ReadUpstreamHistoryPage(context.Background(), "thread_probe", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Turns) != 2 || first.Turns[0].TurnID != "turn_2" || first.Turns[1].TurnID != "turn_3" {
		t.Fatalf("ascending order = %+v", first.Turns)
	}
	if first.NextCursor != "cur-old" {
		t.Fatalf("nextCursor passthrough = %q", first.NextCursor)
	}
	if first.Turns[1].UserItemID != "u3" || first.Turns[1].UserText != "q3" {
		t.Fatalf("mapped user slot = %+v", first.Turns[1])
	}

	older, err := agent.ReadUpstreamHistoryPage(context.Background(), "thread_probe", "cur-old")
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Turns) != 1 || older.Turns[0].TurnID != "turn_1" || older.NextCursor != "" {
		t.Fatalf("EOF page = %+v", older)
	}
}
