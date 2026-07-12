package grokbuild

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// fakeAgentProcess answers each JSON-RPC request immediately on the same line
// it receives, before the client could register a waiter if registration were
// after write (regression for audit P0-2).
func startImmediateRPCResponder(t *testing.T) (stdinW io.WriteCloser, stdoutR io.ReadCloser, stop func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer outW.Close()
		sc := bufio.NewScanner(inR)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.Unmarshal(line, &req); err != nil {
				continue
			}
			var result any
			switch req.Method {
			case "initialize":
				result = map[string]any{
					"protocolVersion": 1,
					"agentCapabilities": map[string]any{
						"loadSession": true,
					},
					"authMethods": []map[string]any{
						{"id": "cached_token", "name": "cached_token"},
					},
				}
			case "authenticate":
				result = map[string]any{}
			case "session/new":
				result = map[string]any{"sessionId": "new-sess-1"}
			case "session/load":
				// Fail load to exercise fail-closed path in higher-level tests.
				resp, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(req.ID),
					"error":   map[string]any{"code": -32000, "message": "session missing"},
				})
				_, _ = outW.Write(append(resp, '\n'))
				continue
			case "session/prompt":
				result = map[string]any{"stopReason": "end_turn"}
			default:
				result = map[string]any{}
			}
			resultJSON, _ := json.Marshal(result)
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"result":  json.RawMessage(resultJSON),
			})
			_, _ = outW.Write(append(resp, '\n'))
		}
	}()

	stop = func() {
		_ = inW.Close()
		_ = inR.Close()
		<-done
	}
	return inW, outR, stop
}

func TestCallRPC_RegistersWaiterBeforeWrite(t *testing.T) {
	stdinW, stdoutR, stop := startImmediateRPCResponder(t)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &grokSession{
		stdin:        stdinW,
		stdout:       stdoutR,
		events:       make(chan core.Event, 16),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	s.alive.Store(true)
	go s.readLoop()

	// Immediate response must still be captured.
	result, err := s.callRPC(1, "initialize", initializeParams{ProtocolVersion: 1}, 2*time.Second)
	if err != nil {
		t.Fatalf("callRPC: %v", err)
	}
	var init initializeResult
	if err := json.Unmarshal(result, &init); err != nil {
		t.Fatal(err)
	}
	if !init.AgentCapabilities.LoadSession.Enabled {
		t.Fatal("expected loadSession enabled")
	}
}

func TestSend_PendingPromptIDBeforeWrite(t *testing.T) {
	stdinW, stdoutR, stop := startImmediateRPCResponder(t)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &grokSession{
		agent:        &Agent{workDir: t.TempDir()},
		stdin:        stdinW,
		stdout:       stdoutR,
		events:       make(chan core.Event, 16),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	s.alive.Store(true)
	s.sessionID.Store("sess-1")
	go s.readLoop()

	if err := s.Send("hi", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for terminal result from immediate session/prompt response.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-s.events:
			if ev.Done && (ev.Type == core.EventResult || ev.Type == core.EventError) {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for prompt terminal event — pendingPromptID race?")
		}
	}
}

func TestCancelTurn_SendsSessionCancelNotification(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	_ = outW

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &grokSession{
		stdin:        inW,
		stdout:       outR,
		events:       make(chan core.Event, 4),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	s.alive.Store(true)
	s.sessionID.Store("sess-cancel")

	var got string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(inR)
		if sc.Scan() {
			got = sc.Text()
		}
	}()

	if err := s.CancelTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = inW.Close()
	wg.Wait()

	var n struct {
		Method string `json:"method"`
		Params struct {
			SessionID string `json:"sessionId"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(got), &n); err != nil {
		t.Fatalf("decode %q: %v", got, err)
	}
	if n.Method != "session/cancel" || n.Params.SessionID != "sess-cancel" {
		t.Fatalf("unexpected cancel payload: %+v", n)
	}
}

func TestEmit_DedupesTerminalDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &grokSession{
		events: make(chan core.Event, 4),
		ctx:    ctx,
		cancel: cancel,
	}
	s.emit(core.Event{Type: core.EventResult, Done: true})
	s.emit(core.Event{Type: core.EventError, Done: true, Content: "second"})
	if got := len(s.events); got != 1 {
		t.Fatalf("events=%d want 1", got)
	}
}

func TestLoadSession_FailDoesNotCreateNewViaCallRPC(t *testing.T) {
	// Ensure load error is returned to caller of loadSession (fail-closed).
	stdinW, stdoutR, stop := startImmediateRPCResponder(t)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &grokSession{
		agent:        &Agent{workDir: "/tmp"},
		stdin:        stdinW,
		stdout:       stdoutR,
		events:       make(chan core.Event, 8),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	s.alive.Store(true)
	go s.readLoop()

	err := s.loadSession("missing-id")
	if err == nil {
		t.Fatal("expected loadSession error")
	}
	// Session ID must not flip to a new id on failure.
	if s.CurrentSessionID() != "" && s.CurrentSessionID() != "missing-id" {
		// loadSession only stores id on success; empty is fine.
	}
	if s.CurrentSessionID() == "new-sess-1" {
		t.Fatal("load failure must not create new session id")
	}
}

// Ensure TurnCanceler is asserted at compile time and CancelTurn is not a no-op when dead.
func TestCancelTurn_NotAlive(t *testing.T) {
	s := &grokSession{}
	s.alive.Store(false)
	if err := s.CancelTurn(context.Background()); err == nil {
		t.Fatal("expected error when not alive")
	}
}

func TestTerminalDone_Atomic(t *testing.T) {
	var d atomic.Bool
	if !d.CompareAndSwap(false, true) {
		t.Fatal()
	}
	if d.CompareAndSwap(false, true) {
		t.Fatal("second CAS should fail")
	}
}

func TestNewSession_RejectsEmptySessionID(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	go func() {
		defer outW.Close()
		sc := bufio.NewScanner(inR)
		for sc.Scan() {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			_ = json.Unmarshal(sc.Bytes(), &req)
			// Always return empty sessionId for session/new.
			result := map[string]any{}
			if req.Method == "session/new" {
				result = map[string]any{"sessionId": "   "}
			} else if req.Method == "initialize" {
				result = map[string]any{
					"protocolVersion":   1,
					"agentCapabilities": map[string]any{"loadSession": true},
				}
			}
			rj, _ := json.Marshal(result)
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"result":  json.RawMessage(rj),
			})
			_, _ = outW.Write(append(resp, '\n'))
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &grokSession{
		agent:        &Agent{workDir: t.TempDir()},
		stdin:        inW,
		stdout:       outR,
		events:       make(chan core.Event, 4),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	s.alive.Store(true)
	go s.readLoop()

	err := s.newSession()
	if err == nil {
		t.Fatal("expected error for empty sessionId")
	}
	if !strings.Contains(err.Error(), "empty sessionId") {
		t.Fatalf("error=%v", err)
	}
	if s.CurrentSessionID() != "" {
		t.Fatalf("must not store empty id, got %q", s.CurrentSessionID())
	}
	_ = inW.Close()
}

func TestLogErrorClass_NoPayloadDump(t *testing.T) {
	err := fmt.Errorf("decode: %s", `{"prompt":"SECRET_TOKEN_VALUE"}`)
	cls := logErrorClass(err)
	if strings.Contains(cls, "SECRET_TOKEN_VALUE") {
		t.Fatalf("logErrorClass leaked payload: %q", cls)
	}
}

func TestShortID(t *testing.T) {
	if shortID("") != "empty" {
		t.Fatal(shortID(""))
	}
	if shortID("abcdefghij") != "abcdefgh" {
		t.Fatal(shortID("abcdefghij"))
	}
}

func TestSessionLoadParams_IncludeCwdAndMcpServers(t *testing.T) {
	// Wire contract: Grok rejects load without cwd + mcpServers (-32602).
	raw, err := encodeRequest(1, "session/load", sessionLoadParams{
		SessionID:  "abc",
		CWD:        "/tmp/project",
		McpServers: []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &req); err != nil {
		t.Fatal(err)
	}
	params, _ := req["params"].(map[string]any)
	if params["sessionId"] != "abc" {
		t.Fatalf("sessionId=%v", params["sessionId"])
	}
	if params["cwd"] != "/tmp/project" {
		t.Fatalf("cwd=%v", params["cwd"])
	}
	ms, ok := params["mcpServers"].([]any)
	if !ok || ms == nil {
		t.Fatalf("mcpServers missing or null: %T %v", params["mcpServers"], params["mcpServers"])
	}
}

func TestLoadSession_SendsRequiredFields(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	var gotParams map[string]any
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer outW.Close()
		sc := bufio.NewScanner(inR)
		for sc.Scan() {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params map[string]any  `json:"params"`
			}
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
				continue
			}
			if req.Method == "session/load" {
				gotParams = req.Params
			}
			result := map[string]any{}
			if req.Method == "session/load" {
				result = map[string]any{"sessionId": "abc"}
			}
			rj, _ := json.Marshal(result)
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"result":  json.RawMessage(rj),
			})
			_, _ = outW.Write(append(resp, '\n'))
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &Agent{workDir: "/Users/jacklee/Projects/cordcode-ios"}
	s := &grokSession{
		agent:        a,
		stdin:        inW,
		stdout:       outR,
		events:       make(chan core.Event, 4),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	s.alive.Store(true)
	go s.readLoop()

	if err := s.loadSession("019f5550-aa26-70d2-a481-91b6745bcfb9"); err != nil {
		t.Fatal(err)
	}
	_ = inW.Close()
	wg.Wait()

	if gotParams == nil {
		t.Fatal("no session/load seen")
	}
	if gotParams["cwd"] != "/Users/jacklee/Projects/cordcode-ios" {
		t.Fatalf("cwd=%v", gotParams["cwd"])
	}
	if _, ok := gotParams["mcpServers"]; !ok {
		t.Fatal("mcpServers missing")
	}
	if gotParams["sessionId"] != "019f5550-aa26-70d2-a481-91b6745bcfb9" {
		t.Fatalf("sessionId=%v", gotParams["sessionId"])
	}
}

func TestSource_DoesNotLogRawAgentLines(t *testing.T) {
	// Static guard: production log sites must not attach raw "line" or stderr "line" fields.
	raw, err := os.ReadFile("session.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	// Forbidden patterns that previously leaked agent-controlled content.
	banned := []string{
		`"line", string(line)`,
		`"line", scanner.Text()`,
		`slog.Debug("grokbuild: stderr", "line"`,
		`slog.Warn("grokbuild: decode failed", "error", err, "line"`,
	}
	for _, b := range banned {
		if strings.Contains(src, b) {
			t.Fatalf("sensitive log pattern still present: %s", b)
		}
	}
	if !strings.Contains(src, `"bytes", len(line)`) {
		t.Fatal("expected decode-failed log to record byte length only")
	}
	if !strings.Contains(src, `empty sessionId`) {
		t.Fatal("expected empty sessionId rejection")
	}
}
