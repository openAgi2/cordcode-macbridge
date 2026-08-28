package codexremote

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFetchThreadListPaginatesAndFiltersDir(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	var calls []map[string]any
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		if method != "thread/list" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		var p map[string]any
		_ = json.Unmarshal(params, &p)
		calls = append(calls, p)
		if cur, _ := p["cursor"].(string); cur == "page2" {
			return map[string]any{
				"data": []any{map[string]any{"id": "t2", "name": "two", "updatedAt": int64(2), "cwd": "/ws"}},
			}, nil
		}
		return map[string]any{
			"data":       []any{map[string]any{"id": "t1", "name": "one", "updatedAt": int64(1), "cwd": "/ws"}},
			"nextCursor": "page2",
		}, nil
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(map[string]any{"skip_restore": true})
	agent.BindClient(cl)

	list, err := agent.FetchThreadList(context.Background(), "/ws")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "t1" || list[1].ID != "t2" {
		t.Fatalf("list=%+v", list)
	}
	if list[0].Directory != "/ws" || list[0].Summary != "one" {
		t.Fatalf("row=%+v", list[0])
	}
	if len(calls) != 2 {
		t.Fatalf("pages=%d", len(calls))
	}
	cwd, _ := calls[0]["cwd"].([]any)
	if len(cwd) != 1 || cwd[0] != "/ws" {
		t.Fatalf("cwd filter=%v", calls[0]["cwd"])
	}
	if calls[1]["cursor"] != "page2" {
		t.Fatalf("second page cursor=%v", calls[1]["cursor"])
	}
}

func TestFetchThreadListHeadDoesNotFollowCursor(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	n := 0
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		if method != "thread/list" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		n++
		var p map[string]any
		_ = json.Unmarshal(params, &p)
		if p["cursor"] != nil {
			t.Errorf("head probe must not send cursor: %v", p["cursor"])
		}
		return map[string]any{
			"data":       []any{map[string]any{"id": "t1", "name": "one", "cwd": "/ws"}},
			"nextCursor": "page2",
		}, nil
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	list, err := agent.FetchThreadListHead(context.Background(), "", 10)
	if err != nil || len(list) != 1 || list[0].ID != "t1" {
		t.Fatalf("head=%+v err=%v", list, err)
	}
	if n != 1 {
		t.Fatalf("head calls=%d", n)
	}
}
