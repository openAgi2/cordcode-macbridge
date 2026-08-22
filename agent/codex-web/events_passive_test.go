package codexweb

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestPassiveSubscribeResumesLoadedAndStartedThreads(t *testing.T) {
	var mu sync.Mutex
	resumed := map[string]int{}
	var peers []*fakePeer
	deps := LifecycleDeps{
		ResolveCodexBinary: func() (string, error) { return "/fake/codex", nil },
		SocketExists:       func(string) bool { return true },
		DialUDS: func(context.Context, string) (Transport, error) {
			peer := newFakePeer()
			peer.install(happyHandlers())
			peer.on("thread/loaded/list", func(int64, json.RawMessage) (any, *fakeRPCError) {
				return map[string]any{"data": []any{"th-loaded"}}, nil
			})
			peer.on("thread/resume", func(_ int64, params json.RawMessage) (any, *fakeRPCError) {
				var p struct {
					ThreadID string `json:"threadId"`
				}
				_ = json.Unmarshal(params, &p)
				mu.Lock()
				resumed[p.ThreadID]++
				mu.Unlock()
				return map[string]any{
					"thread":        map[string]any{"id": p.ThreadID},
					"model":         "m",
					"modelProvider": "mockpi",
				}, nil
			})
			mu.Lock()
			peers = append(peers, peer)
			mu.Unlock()
			return peer, nil
		},
	}
	agent := &Agent{lifecycleDeps: &deps, workDir: "/tmp"}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		mu.Lock()
		snapshot := append([]*fakePeer(nil), peers...)
		mu.Unlock()
		for _, peer := range snapshot {
			_ = peer.Close()
		}
	})
	if _, err := agent.Subscribe(ctx); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := resumed["th-loaded"]
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("observer did not resume loaded thread")
		}
		time.Sleep(10 * time.Millisecond)
	}

	notify, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "thread/started",
		"params":  map[string]any{"thread": map[string]any{"id": "th-new"}},
	})
	mu.Lock()
	observer := peers[len(peers)-1]
	mu.Unlock()
	observer.out <- notify

	deadline = time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := resumed["th-new"]
		mu.Unlock()
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("observer did not resume thread/started id")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAttachLiveThreadResumesObservedSession(t *testing.T) {
	var mu sync.Mutex
	resumed := map[string]int{}
	var peers []*fakePeer
	deps := LifecycleDeps{
		ResolveCodexBinary: func() (string, error) { return "/fake/codex", nil },
		SocketExists:       func(string) bool { return true },
		DialUDS: func(context.Context, string) (Transport, error) {
			peer := newFakePeer()
			peer.install(happyHandlers())
			peer.on("thread/loaded/list", func(int64, json.RawMessage) (any, *fakeRPCError) {
				return map[string]any{"data": []any{}}, nil
			})
			peer.on("thread/resume", func(_ int64, params json.RawMessage) (any, *fakeRPCError) {
				var p struct {
					ThreadID string `json:"threadId"`
				}
				_ = json.Unmarshal(params, &p)
				mu.Lock()
				resumed[p.ThreadID]++
				mu.Unlock()
				return map[string]any{
					"thread":        map[string]any{"id": p.ThreadID},
					"model":         "m",
					"modelProvider": "mockpi",
				}, nil
			})
			mu.Lock()
			peers = append(peers, peer)
			mu.Unlock()
			return peer, nil
		},
	}
	agent := &Agent{lifecycleDeps: &deps, workDir: "/tmp"}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		mu.Lock()
		snapshot := append([]*fakePeer(nil), peers...)
		mu.Unlock()
		for _, peer := range snapshot {
			_ = peer.Close()
		}
	})
	if _, err := agent.Subscribe(ctx); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	agent.AttachLiveThread(context.Background(), "th-open")
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := resumed["th-open"]
		mu.Unlock()
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("AttachLiveThread did not resume the observed thread")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
