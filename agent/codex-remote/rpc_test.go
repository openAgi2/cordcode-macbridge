package codexremote

import (
	"encoding/json"
	"testing"
	"time"
)

func startEnvelopePeer(t *testing.T, host FrameConn, handle func(id int64, method string, params json.RawMessage) (any, *RPCError)) {
	t.Helper()
	go func() {
		var seq uint64
		for {
			env, err := host.Read()
			if err != nil {
				return
			}
			if env.Type != typeClientMessage {
				continue
			}
			var req struct {
				ID     int64           `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(env.Message, &req) != nil || req.Method == "" {
				continue
			}
			result, rpcErr := handle(req.ID, req.Method, req.Params)
			seq++
			s := seq
			out := map[string]any{"jsonrpc": "2.0", "id": req.ID}
			if rpcErr != nil {
				out["error"] = map[string]any{"code": rpcErr.Code, "message": rpcErr.Message}
			} else {
				out["result"] = result
			}
			payload, _ := json.Marshal(out)
			_ = host.Write(Envelope{
				Type: typeServerMessage, ClientID: env.ClientID, EnvID: env.EnvID,
				StreamID: env.StreamID, SeqID: &s, Message: payload,
			})
		}
	}()
}

func TestRPCInitializeAndThreadListOverStream(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	startEnvelopePeer(t, hostConn, func(_ int64, method string, _ json.RawMessage) (any, *RPCError) {
		switch method {
		case "initialize":
			return map[string]any{"userAgent": "codex", "platformOs": "macos"}, nil
		case "thread/list":
			return map[string]any{"data": []any{map[string]any{"id": "thread_probe", "name": "probe"}}, "nextCursor": nil}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: "unknown"}
		}
	})
	cl := NewClient(stream, 1)
	defer cl.Close()
	raw, rpcErr, err := cl.Request("initialize", map[string]any{"clientInfo": map[string]any{"name": "codex_remote_phase0_probe"}})
	if err != nil || rpcErr != nil {
		t.Fatalf("initialize: %v %v", err, rpcErr)
	}
	if !json.Valid(raw) {
		t.Fatalf("result %s", raw)
	}
	raw, rpcErr, err = cl.Request("thread/list", map[string]any{"limit": 5})
	if err != nil || rpcErr != nil {
		t.Fatalf("thread/list: %v %v", err, rpcErr)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Data) != 1 || parsed.Data[0].ID != "thread_probe" {
		t.Fatalf("list = %s err=%v", raw, err)
	}
}

func TestRPCNotificationTurnStarted(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_probe", "env_desktop", "stream_primary")
	defer stream.Close()
	cl := NewClient(stream, 2)
	defer cl.Close()
	seq := uint64(1)
	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "turn/started",
		"params":  map[string]any{"threadId": "thread_probe", "turn": map[string]any{"id": "turn_1"}},
	})
	_ = hostConn.Write(Envelope{
		Type: typeServerMessage, ClientID: "client_probe", EnvID: "env_desktop",
		StreamID: "stream_primary", SeqID: &seq, Message: payload,
	})
	select {
	case n := <-cl.Notifications():
		if n.Method != "turn/started" {
			t.Fatalf("method = %s", n.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}
