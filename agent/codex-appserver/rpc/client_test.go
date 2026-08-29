package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type testTransport struct {
	mu      sync.Mutex
	sent    [][]byte
	frames  chan []byte
	closed  chan struct{}
	onSend  func([]byte)
	closeMu sync.Once
}

func newTestTransport() *testTransport {
	return &testTransport{frames: make(chan []byte, 256), closed: make(chan struct{})}
}

func (t *testTransport) Send(payload []byte) error {
	t.mu.Lock()
	t.sent = append(t.sent, append([]byte(nil), payload...))
	onSend := t.onSend
	t.mu.Unlock()
	if onSend != nil {
		onSend(payload)
	}
	return nil
}

func (t *testTransport) Recv() ([]byte, error) {
	select {
	case payload := <-t.frames:
		return payload, nil
	case <-t.closed:
		return nil, errors.New("test transport closed")
	}
}

func (t *testTransport) Close() error {
	t.closeMu.Do(func() { close(t.closed) })
	return nil
}

func (t *testTransport) push(value any) {
	payload, _ := json.Marshal(value)
	t.frames <- payload
}

func (t *testTransport) lastSent(tst *testing.T) map[string]any {
	tst.Helper()
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.sent) == 0 {
		tst.Fatal("no sent frame")
	}
	var frame map[string]any
	if err := json.Unmarshal(t.sent[len(t.sent)-1], &frame); err != nil {
		tst.Fatal(err)
	}
	return frame
}

func TestOutOfOrderResponseCorrelation(t *testing.T) {
	transport := newTestTransport()
	client := NewClient(transport, 7, Options{ErrorPrefix: "test"})
	defer client.Close()

	transport.onSend = func(payload []byte) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(payload, &request)
		delay := time.Duration(0)
		if request.Method == "slow" {
			delay = 30 * time.Millisecond
		}
		go func() {
			time.Sleep(delay)
			transport.push(map[string]any{
				"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"method": request.Method},
			})
		}()
	}

	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, method := range []string{"slow", "fast"} {
		wg.Add(1)
		go func(method string) {
			defer wg.Done()
			raw, rpcErr, err := client.Request(method, nil)
			if err != nil || rpcErr != nil {
				t.Errorf("%s: err=%v rpcErr=%v", method, err, rpcErr)
				return
			}
			var result struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(raw, &result)
			results <- result.Method
		}(method)
	}
	wg.Wait()
	close(results)
	seen := map[string]bool{}
	for result := range results {
		seen[result] = true
	}
	if !seen["slow"] || !seen["fast"] {
		t.Fatalf("correlation lost: %v", seen)
	}
}

func TestNotificationAndServerRequestKeepEpochAndIdentity(t *testing.T) {
	transport := newTestTransport()
	client := NewClient(transport, 42, Options{ErrorPrefix: "test"})
	defer client.Close()

	transport.push(map[string]any{
		"jsonrpc": "2.0", "method": "turn/started", "params": map[string]any{"threadId": "th"},
	})
	transport.push(map[string]any{
		"jsonrpc": "2.0", "id": 9, "method": "item/request", "params": map[string]any{"threadId": "th", "turnId": "tu"},
	})

	select {
	case notification := <-client.Notifications():
		if notification.Epoch != 42 || notification.Method != "turn/started" {
			t.Fatalf("notification=%+v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("notification timeout")
	}
	select {
	case request := <-client.ServerRequests():
		if request.Epoch != 42 || request.RequestID != "9" || request.ThreadID != "th" || request.TurnID != "tu" {
			t.Fatalf("server request=%+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("server request timeout")
	}
}

func TestServerRequestResponseAndErrorFraming(t *testing.T) {
	transport := newTestTransport()
	client := NewClient(transport, 1, Options{ErrorPrefix: "test"})
	defer client.Close()

	if err := client.RespondServerRequest("7", map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	response := transport.lastSent(t)
	if response["id"] != float64(7) || response["result"] == nil {
		t.Fatalf("response=%v", response)
	}

	if err := client.RespondServerRequestError("8", -32601, "unsupported"); err != nil {
		t.Fatal(err)
	}
	rejection := transport.lastSent(t)
	errorPayload, ok := rejection["error"].(map[string]any)
	if rejection["id"] != float64(8) || !ok || errorPayload["code"] != float64(-32601) {
		t.Fatalf("rejection=%v", rejection)
	}
}

func TestCancellationDropsPendingAndUsesBackendPrefix(t *testing.T) {
	transport := newTestTransport()
	client := NewClient(transport, 1, Options{ErrorPrefix: "backend-x"})
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := client.RequestContext(ctx, "thread/list", nil)
	if err == nil || !strings.Contains(err.Error(), "backend-x: request thread/list canceled") {
		t.Fatalf("err=%v", err)
	}
	if pending := client.PendingCount(); pending != 0 {
		t.Fatalf("pending=%d", pending)
	}
}

func TestLocalCloseAndRemoteTerminationAreSeparatePolicies(t *testing.T) {
	transport := newTestTransport()
	client := NewClient(transport, 1, Options{ErrorPrefix: "test"})
	if client.IsLocallyClosed() || client.IsTerminated() {
		t.Fatal("fresh client is open")
	}
	transport.Close()
	deadline := time.After(time.Second)
	for !client.IsTerminated() {
		select {
		case <-deadline:
			t.Fatal("remote termination not observed")
		case <-time.After(time.Millisecond):
		}
	}
	if client.IsLocallyClosed() {
		t.Fatal("remote termination must not rewrite local-close policy")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if !client.IsLocallyClosed() {
		t.Fatal("explicit Close must set local policy")
	}
}
