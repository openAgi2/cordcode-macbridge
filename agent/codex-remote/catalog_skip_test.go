package codexremote

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

// Regression: ChatGPT Desktop 26.924.20706 left a thread in thread/loaded/list
// whose rollout no longer exists on the desktop side; the app-server refuses
// its thread/resume with -32600 "no rollout found" (official ThreadNotFound).
// One such thread must not abort the whole live catalog attach.
func TestAttachLiveCatalogSkipsThreadWithRPCRefusal(t *testing.T) {
	clientConn, hostConn := LoopbackPair()
	stream := NewStream(clientConn, "client_skip", "env_desktop", "stream_skip")
	defer stream.Close()

	var mu sync.Mutex
	resumeCalls := map[string]int{}
	startEnvelopePeer(t, hostConn, func(_ int64, method string, params json.RawMessage) (any, *RPCError) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "thread/loaded/list":
			return map[string]any{"data": []any{"thread_broken", "thread_ok"}}, nil
		case "thread/resume":
			var request map[string]any
			_ = json.Unmarshal(params, &request)
			threadID, _ := request["threadId"].(string)
			resumeCalls[threadID]++
			if threadID == "thread_broken" {
				return nil, &RPCError{Code: -32600, Message: "no rollout found for thread id thread_broken"}
			}
			return map[string]any{"thread": map[string]any{"id": threadID}}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: method}
		}
	})

	cl := NewClient(stream, 1)
	defer cl.Close()
	agent := New(nil)
	agent.BindClient(cl)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if _, err := agent.SubscribeLive(ctx); err != nil {
		t.Fatalf("SubscribeLive: %v", err)
	}
	if err := agent.AttachLiveCatalog(context.Background()); err != nil {
		t.Fatalf("AttachLiveCatalog aborted on per-thread refusal: %v", err)
	}
	if err := agent.AttachLiveCatalog(context.Background()); err != nil {
		t.Fatalf("repeat AttachLiveCatalog: %v", err)
	}

	mu.Lock()
	brokenCalls, okCalls := resumeCalls["thread_broken"], resumeCalls["thread_ok"]
	mu.Unlock()
	if okCalls != 1 {
		t.Fatalf("thread_ok resume calls = %d, want 1 (attached despite sibling refusal)", okCalls)
	}
	if brokenCalls != 1 {
		t.Fatalf("thread_broken resume calls = %d, want 1 (skipped after first refusal, no 3s-loop retry spam)", brokenCalls)
	}
	agent.mu.Lock()
	_, stillSkipped := agent.attachSkipped["thread_broken"]
	agent.mu.Unlock()
	if !stillSkipped {
		t.Fatal("thread_broken not recorded in epoch skip set")
	}
}
