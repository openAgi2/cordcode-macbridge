package codexremote

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestSessionMutationsUseOfficialRPCsAndAuthoritativeRead(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_mutations", "env_desktop", "stream_mutations")
	defer stream.Close()

	var mu sync.Mutex
	var calls []string
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		mu.Lock()
		calls = append(calls, method)
		mu.Unlock()
		var p map[string]any
		if err := json.Unmarshal(params, &p); err != nil || p["threadId"] != "thread_mutation" {
			return nil, &RPCError{Code: -32602, Message: "bad thread id"}
		}
		switch method {
		case "thread/name/set":
			if len(p) != 2 || p["name"] != "official title" {
				return nil, &RPCError{Code: -32602, Message: "bad name params"}
			}
			return map[string]any{}, nil
		case "thread/archive", "thread/delete":
			if len(p) != 1 {
				return nil, &RPCError{Code: -32602, Message: "unexpected params"}
			}
			return map[string]any{}, nil
		case "thread/read":
			if _, exists := p["includeTurns"]; exists {
				return nil, &RPCError{Code: -32602, Message: "read must omit turns"}
			}
			name := "server-confirmed title"
			return map[string]any{"thread": map[string]any{
				"id": "thread_mutation", "name": name, "preview": "preview",
				"updatedAt": int64(123), "cwd": "/Projects/selected",
				"status": map[string]any{"type": "notLoaded"},
			}}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)

	if _, ok := any(agent).(core.SessionRenamer); !ok {
		t.Fatal("codex-remote must advertise SessionRenamer")
	}
	if _, ok := any(agent).(core.SessionArchiver); !ok {
		t.Fatal("codex-remote must advertise SessionArchiver")
	}
	if _, ok := any(agent).(core.SessionDeleter); !ok {
		t.Fatal("codex-remote must advertise SessionDeleter")
	}
	if _, ok := any(agent).(core.SessionInfoFetcher); !ok {
		t.Fatal("codex-remote must advertise SessionInfoFetcher")
	}

	renamed, err := agent.RenameSession(context.Background(), "thread_mutation", " official title ")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Summary != "server-confirmed title" || renamed.Directory != "/Projects/selected" || renamed.ModifiedAt.Unix() != 123 {
		t.Fatalf("rename result must come from thread/read: %+v", renamed)
	}
	archived, err := agent.ArchiveSession(context.Background(), "thread_mutation", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if archived.Summary != "server-confirmed title" {
		t.Fatalf("archive result must come from thread/read: %+v", archived)
	}
	if err := agent.DeleteSession(context.Background(), "thread_mutation"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	want := []string{"thread/name/set", "thread/read", "thread/archive", "thread/read", "thread/delete"}
	if len(got) != len(want) {
		t.Fatalf("calls=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls=%v want=%v", got, want)
		}
	}
}

func TestSessionMutationErrorsFailClosed(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_mutation_error", "env_desktop", "stream_mutation_error")
	defer stream.Close()
	startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
		return nil, &RPCError{Code: -32603, Message: "official failure: " + method}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)

	if _, err := agent.RenameSession(context.Background(), "thread", "title"); err == nil {
		t.Fatal("rename must propagate official RPC failure")
	}
	if _, err := agent.ArchiveSession(context.Background(), "thread", time.Time{}); err == nil {
		t.Fatal("archive must propagate official RPC failure")
	}
	if err := agent.DeleteSession(context.Background(), "thread"); err == nil {
		t.Fatal("delete must propagate official RPC failure")
	}
}

func TestSelectedWorkDirReachesThreadStart(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_cwd", "env_desktop", "stream_cwd")
	defer stream.Close()
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		if method != "thread/start" {
			return nil, &RPCError{Code: -32601, Message: method}
		}
		var p map[string]any
		if err := json.Unmarshal(params, &p); err != nil || len(p) != 1 || p["cwd"] != "/Projects/selected" {
			return nil, &RPCError{Code: -32602, Message: "selected cwd missing"}
		}
		return map[string]any{"thread": map[string]any{"id": "thread_selected"}}, nil
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(map[string]any{"work_dir": "/Users/jacklee"})
	agent.BindClient(cl)

	switcher, ok := any(agent).(core.WorkDirSwitcher)
	if !ok {
		t.Fatal("codex-remote must advertise WorkDirSwitcher")
	}
	switcher.SetWorkDir("/Projects/selected")
	if got := switcher.GetWorkDir(); got != "/Projects/selected" {
		t.Fatalf("work dir=%q", got)
	}
	session, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if got := session.CurrentSessionID(); got != "thread_selected" {
		t.Fatalf("session id=%q", got)
	}
}
