package codexremote

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func startPinPeer(t *testing.T, calls *[]map[string]any, respond func(method string, params map[string]any) (any, *RPCError)) *Client {
	t.Helper()
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	t.Cleanup(func() { stream.Close() })
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		var p map[string]any
		_ = json.Unmarshal(params, &p)
		*calls = append(*calls, p)
		return respond(method, p)
	})
	cl := NewClient(stream, 1)
	t.Cleanup(func() { cl.Close() })
	return cl
}

func boundAgent() *Agent {
	return New(map[string]any{"skip_restore": true})
}

func TestSetSessionPinnedPinSendsSectionMove(t *testing.T) {
	var calls []map[string]any
	agent := boundAgent()
	client := startPinPeer(t, &calls, func(method string, _ map[string]any) (any, *RPCError) {
		if method != "thread/section/move" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		return map[string]any{}, nil
	})
	agent.BindClient(client)

	at := time.UnixMilli(1_700_000_000_000).UTC()
	pin, err := agent.SetSessionPinned(context.Background(), "thread-1", "/ws", true, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls=%d", len(calls))
	}
	if calls[0]["threadId"] != "thread-1" || calls[0]["sectionId"] != pinnedThreadSectionID {
		t.Fatalf("move wire=%v", calls[0])
	}
	if pin == nil || pin.BackendID != BackendID || pin.SessionID != "thread-1" || !pin.PinnedAt.Equal(at) {
		t.Fatalf("pin=%+v", pin)
	}
}

func TestSetSessionPinnedUnpinSendsNullSection(t *testing.T) {
	var calls []map[string]any
	agent := boundAgent()
	client := startPinPeer(t, &calls, func(method string, _ map[string]any) (any, *RPCError) {
		if method != "thread/section/move" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		return map[string]any{}, nil
	})
	agent.BindClient(client)

	pin, err := agent.SetSessionPinned(context.Background(), " thread-1 ", "/ws", false, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if pin != nil {
		t.Fatalf("unpin must return nil envelope, got %+v", pin)
	}
	if calls[0]["sectionId"] != nil {
		t.Fatalf("unpin sectionId must be JSON null, got %v", calls[0]["sectionId"])
	}
	// Trimmed id must be sent.
	if calls[0]["threadId"] != "thread-1" {
		t.Fatalf("threadId=%v", calls[0]["threadId"])
	}
}

func TestSetSessionPinnedUpstreamErrorPropagates(t *testing.T) {
	var calls []map[string]any
	agent := boundAgent()
	client := startPinPeer(t, &calls, func(_ string, _ map[string]any) (any, *RPCError) {
		return nil, &RPCError{Code: -32600, Message: "thread not found"}
	})
	agent.BindClient(client)

	pin, err := agent.SetSessionPinned(context.Background(), "thread-x", "/ws", true, time.Now())
	if err == nil || !strings.Contains(err.Error(), "thread not found") {
		t.Fatalf("err=%v", err)
	}
	if pin != nil {
		t.Fatalf("pin=%+v", pin)
	}
}

func TestListPinnedSessionsSendsSectionFilterAndOrderKeys(t *testing.T) {
	var calls []map[string]any
	agent := boundAgent()
	client := startPinPeer(t, &calls, func(method string, params map[string]any) (any, *RPCError) {
		if method != "thread/list" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		if cur, _ := params["cursor"].(string); cur == "page2" {
			return map[string]any{"data": []any{
				map[string]any{"id": "c", "name": "third"},
			}}, nil
		}
		return map[string]any{
			"data": []any{
				map[string]any{"id": "a", "name": "first"},
				map[string]any{"id": "b", "name": "second"},
			},
			"nextCursor": "page2",
		}, nil
	})
	agent.BindClient(client)

	pins, err := agent.ListPinnedSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 3 || pins[0].SessionID != "a" || pins[1].SessionID != "b" || pins[2].SessionID != "c" {
		t.Fatalf("pins=%+v", pins)
	}
	// Official order (row 0 first) must map to strictly descending keys so
	// iOS pinnedAt DESC keeps the official order.
	for i := 1; i < len(pins); i++ {
		if !pins[i].PinnedAt.Before(pins[i-1].PinnedAt) {
			t.Fatalf("order keys not descending: %v then %v", pins[i-1].PinnedAt, pins[i].PinnedAt)
		}
	}
	first := calls[0]
	if first["sectionId"] != pinnedThreadSectionID || first["sortKey"] != "section_position" || first["sortDirection"] != "asc" {
		t.Fatalf("list wire=%v", first)
	}
	if calls[1]["cursor"] != "page2" {
		t.Fatalf("second page cursor=%v", calls[1]["cursor"])
	}
}

func TestListPinnedSessionsEmptySectionIsNotError(t *testing.T) {
	var calls []map[string]any
	agent := boundAgent()
	client := startPinPeer(t, &calls, func(_ string, _ map[string]any) (any, *RPCError) {
		return map[string]any{"data": []any{}}, nil
	})
	agent.BindClient(client)

	pins, err := agent.ListPinnedSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 0 {
		t.Fatalf("pins=%+v", pins)
	}
}

func TestListPinnedSessionsStrictFail(t *testing.T) {
	var calls []map[string]any
	agent := boundAgent()
	client := startPinPeer(t, &calls, func(_ string, _ map[string]any) (any, *RPCError) {
		return nil, &RPCError{Code: -32000, Message: "boom"}
	})
	agent.BindClient(client)

	if _, err := agent.ListPinnedSessions(context.Background()); err == nil {
		t.Fatal("expected strict-fail error")
	}
}

func TestPinErrorsWhenNotConfigured(t *testing.T) {
	agent := boundAgent()
	if _, err := agent.SetSessionPinned(context.Background(), "t", "", true, time.Now()); err != ErrNotConfigured {
		t.Fatalf("set err=%v want ErrNotConfigured", err)
	}
	if _, err := agent.ListPinnedSessions(context.Background()); err != ErrNotConfigured {
		t.Fatalf("list err=%v want ErrNotConfigured", err)
	}
}

func TestMapCatalogThreadCarriesPinnedSectionDecoration(t *testing.T) {
	row := catalogThreadRow{ID: "t1", Name: "n", Cwd: "/ws"}
	if !mapCatalogThread(row).PinnedAt.IsZero() {
		t.Fatal("unsectioned row must not be pinned")
	}
	sectioned := catalogThreadRow{
		ID: "t2", Name: "n", Cwd: "/ws",
		SectionEnteredAt: 1_780_000_000,
	}
	sectioned.Section = &struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{ID: pinnedThreadSectionID, Name: "Pinned"}
	info := mapCatalogThread(sectioned)
	if info.PinnedAt.Unix() != 1_780_000_000 {
		t.Fatalf("PinnedAt=%v", info.PinnedAt)
	}
	other := sectioned
	other.Section = &struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{ID: "some-other-section", Name: "Work"}
	if !mapCatalogThread(other).PinnedAt.IsZero() {
		t.Fatal("non-pinned section must not mark the row pinned")
	}
}

func TestListPinnedSessionSummariesCarryRowSummaries(t *testing.T) {
	var calls []map[string]any
	client := startPinPeer(t, &calls, func(method string, _ map[string]any) (any, *RPCError) {
		if method != "thread/list" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		return map[string]any{
			"data": []any{
				map[string]any{"id": "a", "name": "First pinned", "cwd": "/ws", "updatedAt": int64(7)},
			},
		}, nil
	})
	agent := boundAgent()
	agent.BindClient(client)

	infos, err := agent.ListPinnedSessionSummaries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].ID != "a" || infos[0].Summary != "First pinned" || infos[0].Directory != "/ws" {
		t.Fatalf("infos=%+v", infos)
	}
	if infos[0].PinnedAt != officialPinnedOrderKey(0) {
		t.Fatalf("order key=%v", infos[0].PinnedAt)
	}
}
