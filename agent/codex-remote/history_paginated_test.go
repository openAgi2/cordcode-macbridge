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
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
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

// metricsCaptureHandler collects slog records so the owner-adjudicated
// resource-gate metrics (2026-08-30 #8) can be asserted per exit path.
type metricsCaptureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *metricsCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *metricsCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *metricsCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *metricsCaptureHandler) WithGroup(string) slog.Handler      { return h }

func (h *metricsCaptureHandler) attrs(r slog.Record) map[string]any {
	out := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.Any()
		return true
	})
	return out
}

func captureTurnItemsMetrics(t *testing.T, fn func()) map[string]any {
	t.Helper()
	handler := &metricsCaptureHandler{}
	previous := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(previous) })
	fn()
	for i := len(handler.records) - 1; i >= 0; i-- {
		if handler.records[i].Message == "codex-remote: turn items metrics" {
			return handler.attrs(handler.records[i])
		}
	}
	t.Fatal("turn items metrics record not emitted")
	return nil
}

// TestTurnItemsMetricsOnSuccess pins the owner-adjudicated field set on the
// EOF exit: every dimension named in the 2026-08-30 #8 ruling (pageCount,
// rawResponseBytes, decodedItemBytes, itemCount, item types, max item size
// and type, per-page and total timings, failGate) must be present and sane.
func TestTurnItemsMetricsOnSuccess(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		if call.Params["cursor"] == nil {
			return map[string]any{
				"data":       []any{itemEntry("turn_items", map[string]any{"type": "userMessage", "id": "u1", "text": "q"})},
				"nextCursor": "cur-2",
			}, nil
		}
		return map[string]any{"data": []any{itemEntry("turn_items", map[string]any{"type": "agentMessage", "id": "a1", "text": "answer-text"})}}, nil
	})
	var fetchErr error
	m := captureTurnItemsMetrics(t, func() {
		_, fetchErr = agent.ReadTurnItems(context.Background(), "thread_probe", "turn_items")
	})
	if fetchErr != nil {
		t.Fatal(fetchErr)
	}
	if m["failGate"] != "eof" {
		t.Fatalf("failGate = %v", m["failGate"])
	}
	if m["pageCount"] != int64(2) {
		t.Fatalf("pageCount = %v", m["pageCount"])
	}
	if m["itemCount"] != int64(2) {
		t.Fatalf("itemCount = %v", m["itemCount"])
	}
	raw, ok := m["rawResponseBytes"].(int64)
	if !ok || raw <= 0 {
		t.Fatalf("rawResponseBytes = %v", m["rawResponseBytes"])
	}
	decoded, ok := m["decodedItemBytes"].(int64)
	if !ok || decoded <= 0 || decoded > raw {
		t.Fatalf("decodedItemBytes = %v (raw %v)", m["decodedItemBytes"], raw)
	}
	types, ok := m["itemTypes"].(map[string]int)
	if !ok || types["userMessage"] != 1 || types["agentMessage"] != 1 {
		t.Fatalf("itemTypes = %v", m["itemTypes"])
	}
	if m["maxItemType"] != "agentMessage" {
		t.Fatalf("maxItemType = %v (expected the larger later item)", m["maxItemType"])
	}
}

// TestTurnItemsMetricsOnMaxBytesGate pins the failGate=max_bytes exit: the
// summary must record the OVERSHOOT cumulative raw bytes (including the page
// that tripped the gate) so real-turn data shows how far past the cap the
// turn actually reaches.
func TestTurnItemsMetricsOnMaxBytesGate(t *testing.T) {
	big := strings.Repeat("x", 60*1024)
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		cursor, _ := call.Params["cursor"].(string)
		return map[string]any{
			"data":       []any{itemEntry("turn_items", map[string]any{"type": "commandExecution", "id": "c1", "aggregatedOutput": big})},
			"nextCursor": "cur-" + cursor + "-more",
		}, nil
	})
	var fetchErr error
	m := captureTurnItemsMetrics(t, func() {
		_, fetchErr = agent.ReadTurnItems(context.Background(), "thread_probe", "turn_items")
	})
	if !errors.Is(fetchErr, ErrTurnItemsMaxBytes) {
		t.Fatal(fetchErr)
	}
	if m["failGate"] != "max_bytes" {
		t.Fatalf("failGate = %v", m["failGate"])
	}
	raw, ok := m["rawResponseBytes"].(int64)
	if !ok || raw <= int64(RemoteTurnItemsMaxBytes) {
		t.Fatalf("rawResponseBytes = %v must record the overshoot cumulative size", raw)
	}
	if m["error"] == "" {
		t.Fatal("error field must name the typed failure")
	}
	// Blind-spot fix (owner closed-evidence round): the gate page is decoded
	// and counted — the first metrics round left it at items=0.
	if itemCount, _ := m["itemCount"].(int64); itemCount < 8 {
		t.Fatalf("itemCount = %v — the gate page must be decoded and counted", itemCount)
	}
	if envelope, _ := m["envelopeBytes"].(int64); envelope <= 0 {
		t.Fatalf("envelopeBytes = %v must be > 0 (JSON envelope + escaping overhead)", envelope)
	}
	if largeItems, _ := m["largeItems"].(string); !strings.Contains(largeItems, "commandExecution") {
		t.Fatalf("largeItems = %q must list the gate page's items", largeItems)
	}
}

// TestTurnItemsDiagnosticWalksToEOF pins the closed-evidence walk (owner
// 2026-08-30 deep night): diagnostic bounds reach the real EOF across pages
// that would have tripped BOTH frozen gates, and the report carries the
// mapped history turn with its serialized size.
func TestTurnItemsDiagnosticWalksToEOF(t *testing.T) {
	// 30 pages of ~60KB command executions — past frozen 24 pages AND 512KB.
	big := strings.Repeat("x", 60*1024)
	pages := map[string]map[string]any{}
	for i := 0; i < 30; i++ {
		resp := map[string]any{
			"data": []any{itemEntry("turn_items", map[string]any{"type": "commandExecution", "id": fmt.Sprintf("c%d", i), "aggregatedOutput": big})},
		}
		if i < 29 {
			resp["nextCursor"] = fmt.Sprintf("cur-%d", i+1)
		}
		pages[fmt.Sprintf("cur-%d", i)] = resp
	}
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		cursor, _ := call.Params["cursor"].(string)
		if cursor == "" {
			cursor = "cur-0"
		}
		return pages[cursor], nil
	})
	report, err := agent.ReadTurnItemsDiagnostic(context.Background(), "thread_probe", "turn_items")
	if err != nil {
		t.Fatal(err)
	}
	if !report.Metrics.EOF {
		t.Fatal("diagnostic walk must reach the real EOF")
	}
	if report.Metrics.PageCount != 30 {
		t.Fatalf("pageCount = %d, want 30 (frozen max_pages is 24)", report.Metrics.PageCount)
	}
	if report.Metrics.RawResponseBytes <= RemoteTurnItemsMaxBytes {
		t.Fatalf("rawResponseBytes = %d must exceed the frozen gate", report.Metrics.RawResponseBytes)
	}
	if report.HistoryTurn == nil || report.HistoryTurnJSONBytes <= 0 {
		t.Fatalf("report must carry the mapped history turn and its JSON size: %+v", report.HistoryTurn)
	}
	if len(report.Entries) != 30 {
		t.Fatalf("entries = %d, want 30", len(report.Entries))
	}
}

// TestTurnItemsDiagnosticGateStillFailsClosed pins that the diagnostic walk's
// own bounds fail closed with their own gate names — evidence bounds, never
// silent truncation, never an EOF claim at a bound.
func TestTurnItemsDiagnosticGateStillFailsClosed(t *testing.T) {
	huge := strings.Repeat("x", 1024*1024) // 1MB items → trips diag 16MB within ~16 pages
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		cursor, _ := call.Params["cursor"].(string)
		return map[string]any{
			"data":       []any{itemEntry("turn_items", map[string]any{"type": "commandExecution", "id": "c1", "aggregatedOutput": huge})},
			"nextCursor": "cur-" + cursor + "-more",
		}, nil
	})
	report, err := agent.ReadTurnItemsDiagnostic(context.Background(), "thread_probe", "turn_items")
	if !errors.Is(err, ErrTurnItemsMaxBytes) {
		t.Fatalf("err = %v", err)
	}
	if report == nil || report.Metrics.FailGate != "diag_max_bytes" {
		t.Fatalf("failGate = %+v", report)
	}
	if report.Metrics.EOF {
		t.Fatal("must not claim EOF at a diagnostic bound")
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

// ---------- T0.6 resume(excludeTurns + initialTurnsPage) production path ----------

func resumeResult(historyMode string, turns []any, nextCursor any, threadTurns []any) map[string]any {
	thread := map[string]any{"id": "thread_probe", "historyMode": historyMode, "status": "idle"}
	if threadTurns != nil {
		thread["turns"] = threadTurns
	}
	result := map[string]any{
		"thread":        thread,
		"model":         "gpt-test",
		"modelProvider": "openai",
	}
	if turns != nil || nextCursor != nil {
		page := map[string]any{}
		if turns != nil {
			page["data"] = turns
		}
		if nextCursor != nil {
			page["nextCursor"] = nextCursor
		}
		result["initialTurnsPage"] = page
	}
	return result
}

func assertRPCs(t *testing.T, calls *[]rpcCall, method string) []rpcCall {
	t.Helper()
	found := []rpcCall{}
	for _, call := range *calls {
		if call.Method == method {
			found = append(found, call)
		}
	}
	return found
}

// The single attach carries the official experimental shape and its page serves
// the paginated cold open with ZERO extra RPCs (probe: 1 RPC vs baseline 2).
// Precondition: the initialize-announced server version is probe-verified.
func TestResumeInitialTurnsPageServesColdOpen(t *testing.T) {
	agent, calls := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		switch call.Method {
		case "thread/resume":
			if call.Params["excludeTurns"] != true {
				t.Errorf("resume must keep excludeTurns:true, params = %+v", call.Params)
			}
			page, ok := call.Params["initialTurnsPage"].(map[string]any)
			if !ok {
				t.Fatalf("resume must carry initialTurnsPage, params = %+v", call.Params)
			}
			if page["limit"] != float64(remoteTurnsPageLimit) || page["sortDirection"] != "desc" ||
				page["itemsView"] != remoteTurnItemsViewSummary {
				t.Fatalf("initialTurnsPage shape = %+v", page)
			}
			return resumeResult("paginated",
				[]any{summaryTurn("turn_new", "completed"), summaryTurn("turn_old", "completed")},
				"cursor-older", []any{}), nil
		}
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})
	agent.NoteServerUserAgent(verifiedServerUserAgent)
	if err := agent.AttachLiveThread(context.Background(), "thread_probe"); err != nil {
		t.Fatal(err)
	}
	cold, err := agent.ReadColdHistory(context.Background(), "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	if cold.HistoryMode != "paginated" {
		t.Fatalf("mode = %s", cold.HistoryMode)
	}
	if ids := coldTurnIDs(cold); len(ids) != 2 || ids[0] != "turn_old" || ids[1] != "turn_new" {
		t.Fatalf("cold turns must be asc from the desc page: %v", ids)
	}
	if cold.Page.NextCursor != "cursor-older" {
		t.Fatalf("nextCursor = %q", cold.Page.NextCursor)
	}
	// The entire cold open cost exactly one RPC — no thread/read, no turns/list.
	if resumes := assertRPCs(t, calls, "thread/resume"); len(resumes) != 1 {
		t.Fatalf("resume count = %d", len(resumes))
	}
	if got := assertRPCs(t, calls, "thread/read"); len(got) != 0 {
		t.Fatalf("cached cold open must not re-read metadata: %+v", got)
	}
	if got := assertRPCs(t, calls, "thread/turns/list"); len(got) != 0 {
		t.Fatalf("cached cold open must not list turns: %+v", got)
	}
	// Consumed once: the next cold open pre-selects the baseline.
	if _, err := agent.ReadColdHistory(context.Background(), "thread_probe"); err == nil {
		t.Fatal("second cold open needs the baseline handler; fake rejects unknown methods")
	}
}

func coldTurnIDs(cold *core.ColdHistoryResult) []string {
	ids := make([]string, 0, len(cold.Page.Turns))
	for _, turn := range cold.Page.Turns {
		ids = append(ids, turn.TurnID)
	}
	return ids
}

// A legacy thread's attach page is NEVER trusted (candidate verified on
// paginated only): the cold open pre-selects metadata + compat full read.
func TestResumeInitialTurnsPageLegacyPreSelectsBaseline(t *testing.T) {
	agent, calls := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		switch call.Method {
		case "thread/resume":
			return resumeResult("legacy", []any{summaryTurn("L1", "completed")}, nil, []any{}), nil
		case "thread/read":
			if call.Params["includeTurns"] == true {
				return map[string]any{"thread": map[string]any{
					"id": "thread_probe", "historyMode": "legacy", "status": "idle",
					"turns": []any{summaryTurn("L1", "completed")},
				}}, nil
			}
			return threadMetaResult("legacy"), nil
		}
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})
	agent.NoteServerUserAgent(verifiedServerUserAgent)
	if err := agent.AttachLiveThread(context.Background(), "thread_probe"); err != nil {
		t.Fatal(err)
	}
	cold, err := agent.ReadColdHistory(context.Background(), "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	if cold.HistoryMode != "legacy" || len(cold.Page.Turns) != 1 || cold.Page.Turns[0].TurnID != "L1" {
		t.Fatalf("legacy cold = %+v", cold.Page)
	}
	if got := assertRPCs(t, calls, "thread/turns/list"); len(got) != 0 {
		t.Fatalf("legacy cold open must not hit paginated turns/list: %+v", got)
	}
}

// A non-empty thread.turns violates the probe contract (default full
// hydration): the breaker trips, the cold open uses baseline, and later
// attaches drop initialTurnsPage — no silent retry, no full-read fallback.
func TestResumeInitialTurnsPageContractViolationTripsBreaker(t *testing.T) {
	agent, calls := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		switch call.Method {
		case "thread/resume":
			if _, carries := call.Params["initialTurnsPage"]; carries {
				return resumeResult("paginated", nil, nil,
					[]any{summaryTurn("surprise", "completed")}), nil
			}
			return resumeResult("paginated", nil, nil, []any{}), nil
		case "thread/read":
			return threadMetaResult("paginated"), nil
		case "thread/turns/list":
			return map[string]any{"data": []any{summaryTurn("turn_new", "completed")}}, nil
		}
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})
	agent.NoteServerUserAgent(verifiedServerUserAgent)
	if err := agent.AttachLiveThread(context.Background(), "thread_probe"); err != nil {
		t.Fatal(err)
	}
	if !agent.resumeInitialPageBrokenForTest() {
		t.Fatal("contract violation must trip the per-process breaker")
	}
	cold, err := agent.ReadColdHistory(context.Background(), "thread_probe")
	if err != nil {
		t.Fatal(err)
	}
	if len(cold.Page.Turns) != 1 || cold.Page.Turns[0].TurnID != "turn_new" {
		t.Fatalf("breaker cold open must use the baseline: %+v", cold.Page)
	}
	if got := assertRPCs(t, calls, "thread/read"); len(got) != 1 {
		t.Fatalf("baseline metadata read missing: %+v", got)
	}
	if got := assertRPCs(t, calls, "thread/turns/list"); len(got) != 1 {
		t.Fatalf("baseline turns page missing: %+v", got)
	}
}

func (a *Agent) resumeInitialPageBrokenForTest() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.resumePageBroken
}

// verifiedServerUserAgent mirrors the shape codex-rs get_codex_user_agent
// answers for our initialize: "{originator}/{workspace-version} ({os} {ver};
// {arch}) {user_agent()} ({clientInfo.name}; {clientInfo.version})" — the
// token after the first segment's last "/" is the probe-verified version.
const verifiedServerUserAgent = "codex_remote/0.151.0-alpha.7.1 (Mac OS 26.5; arm64) codex_cli_rs/0.151.0-alpha.7.1 (codex_remote; 0)"

func TestServerVersionFromUserAgent(t *testing.T) {
	cases := []struct {
		userAgent string
		want      string
	}{
		{verifiedServerUserAgent, "0.151.0-alpha.7.1"},
		{"codex_remote/0.150.0-alpha.12.2 (Mac OS 26.1; arm64) codex_cli_rs/0.150.0-alpha.12.2", "0.150.0-alpha.12.2"},
		{"originator/1.2.3", "1.2.3"},
		// Unreadable shapes must never accidentally match an allowlist entry.
		{"no-slash-token", ""},
		{"", ""},
		{"originator/ 1.2.3 trailing", ""},
	}
	for _, tc := range cases {
		if got := serverVersionFromUserAgent(tc.userAgent); got != tc.want {
			t.Errorf("serverVersionFromUserAgent(%q) = %q, want %q", tc.userAgent, got, tc.want)
		}
	}
}

// The version gate is real: a server version that was never announced
// (initialize not answered yet) or is not in the probe-verified allowlist
// must pre-select the official 2-RPC baseline — the attach omits
// initialTurnsPage entirely (plan line 379), it does not probe-and-fallback.
func TestResumeInitialTurnsPageGateUnverifiedServerPreSelectsBaseline(t *testing.T) {
	for _, tc := range []struct {
		name      string
		userAgent string
	}{
		{"no initialize yet", ""},
		{"unlisted version", "codex_remote/0.152.0 (Mac OS 26.5; arm64) codex_cli_rs/0.152.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent, calls := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
				switch call.Method {
				case "thread/resume":
					if _, carries := call.Params["initialTurnsPage"]; carries {
						t.Errorf("unverified server must not receive initialTurnsPage, params = %+v", call.Params)
					}
					if call.Params["excludeTurns"] != true {
						t.Errorf("resume must keep excludeTurns:true, params = %+v", call.Params)
					}
					return resumeResult("paginated", nil, nil, []any{}), nil
				case "thread/read":
					return threadMetaResult("paginated"), nil
				case "thread/turns/list":
					return map[string]any{"data": []any{summaryTurn("turn_new", "completed")}}, nil
				}
				return nil, &RPCError{Code: -32601, Message: call.Method}
			})
			if tc.userAgent != "" {
				agent.NoteServerUserAgent(tc.userAgent)
			}
			if err := agent.AttachLiveThread(context.Background(), "thread_probe"); err != nil {
				t.Fatal(err)
			}
			cold, err := agent.ReadColdHistory(context.Background(), "thread_probe")
			if err != nil {
				t.Fatal(err)
			}
			if len(cold.Page.Turns) != 1 || cold.Page.Turns[0].TurnID != "turn_new" {
				t.Fatalf("unverified-version cold open must use the baseline: %+v", cold.Page)
			}
			if got := assertRPCs(t, calls, "thread/read"); len(got) != 1 {
				t.Fatalf("baseline metadata read missing: %+v", got)
			}
			if got := assertRPCs(t, calls, "thread/turns/list"); len(got) != 1 {
				t.Fatalf("baseline turns page missing: %+v", got)
			}
			if agent.resumeInitialPageCandidateOn() {
				t.Fatal("gate must stay closed for an unverified server version")
			}
		})
	}
}

// The gate keys on the client epoch's OWN version: a rebind (BindClient)
// clears a previously verified version, and the re-announced initialize
// re-opens it — a stale version from an old connection never gates the new
// one.
func TestResumeInitialTurnsPageGateVersionIsClientEpochScoped(t *testing.T) {
	agent, _ := paginatedFake(t, func(call rpcCall) (any, *RPCError) {
		return nil, &RPCError{Code: -32601, Message: call.Method}
	})
	agent.NoteServerUserAgent(verifiedServerUserAgent)
	if !agent.resumeInitialPageCandidateOn() {
		t.Fatal("verified version must open the gate")
	}
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe2", "env_desktop", "stream_probe_epoch")
	t.Cleanup(func() { stream.Close() })
	startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
		return nil, &RPCError{Code: -32601, Message: method}
	})
	cl2 := NewClient(stream, 2)
	t.Cleanup(func() { cl2.Close() })
	agent.BindClient(cl2) // rebind clears the epoch-scoped version
	if agent.resumeInitialPageCandidateOn() {
		t.Fatal("rebind must close the gate until the new initialize announces a version")
	}
	agent.NoteServerUserAgent(verifiedServerUserAgent)
	if !agent.resumeInitialPageCandidateOn() {
		t.Fatal("re-announced verified version must re-open the gate")
	}
}
