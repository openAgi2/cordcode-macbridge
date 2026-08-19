package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// sseFrame wraps one server event in the /global/event payload envelope.
func sseFrame(eventType string, properties map[string]any) string {
	payload := map[string]any{"type": eventType, "properties": properties}
	b, _ := json.Marshal(map[string]any{"payload": payload})
	return string(b)
}

func driveFrames(sub *sseSubscriber, frames ...string) {
	for _, frame := range frames {
		sub.handleRawEvent(frame)
	}
}

func drain(sub *sseSubscriber) []core.Event {
	var out []core.Event
	for {
		select {
		case ev := <-sub.events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func newDrivenSubscriber(t *testing.T, a *Agent) *sseSubscriber {
	t.Helper()
	// A real generation-pinned client: session.updated's usage recompute
	// fetches messages through it.
	c, err := a.clientFor(context.Background())
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	return newSSESubscriber(context.Background(), a, c)
}

func TestSSEUserMessageArmsTurnOnce(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)

	driveFrames(sub,
		sseFrame("message.updated", map[string]any{
			"info":      map[string]any{"id": "msg_u1", "role": "user"},
			"sessionID": "ses_1",
		}),
		sseFrame("message.part.delta", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_u1", "field": "text", "delta": "hello ",
		}),
		sseFrame("message.part.delta", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_u1", "field": "text", "delta": "world",
		}),
	)

	events := drain(sub)
	var userMsgs, turnsStarted []core.Event
	for _, ev := range events {
		switch ev.Type {
		case core.EventUserMessage:
			userMsgs = append(userMsgs, ev)
		case core.EventTurnStarted:
			turnsStarted = append(turnsStarted, ev)
		}
	}
	if len(userMsgs) == 0 || userMsgs[len(userMsgs)-1].Content != "hello world" {
		t.Fatalf("user prompt must accumulate deltas, got %+v", userMsgs)
	}
	if len(turnsStarted) != 1 {
		t.Fatalf("turn_started must fire exactly once per message id, got %d", len(turnsStarted))
	}
}

func TestSSEAssistantDeltasAndSnapshots(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)

	driveFrames(sub,
		sseFrame("message.updated", map[string]any{
			"info":      map[string]any{"id": "msg_u1", "role": "user"},
			"sessionID": "ses_1",
		}),
		sseFrame("message.updated", map[string]any{
			"info":      map[string]any{"id": "msg_a1", "role": "assistant"},
			"sessionID": "ses_1",
		}),
		sseFrame("message.part.delta", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_a1", "partID": "pt_1", "field": "text", "delta": "Hel",
		}),
		sseFrame("message.part.delta", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_a1", "partID": "pt_1", "field": "text", "delta": "lo",
		}),
		sseFrame("message.part.delta", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_a1", "partID": "pt_2", "field": "reasoning", "delta": "hmm",
		}),
		sseFrame("message.part.updated", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_a1",
			"part": map[string]any{"id": "pt_1", "type": "text", "text": "Hello"},
		}),
		sseFrame("message.part.updated", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_a1",
			"part": map[string]any{"id": "pt_1", "type": "text", "text": "Everything changed"},
		}),
	)

	events := drain(sub)
	var texts, replaces, thinking []core.Event
	for _, ev := range events {
		switch ev.Type {
		case core.EventText:
			texts = append(texts, ev)
		case core.EventTextReplace:
			replaces = append(replaces, ev)
		case core.EventThinking:
			thinking = append(thinking, ev)
		}
	}
	if len(texts) != 2 || texts[0].Content != "Hel" || texts[1].Content != "lo" {
		t.Fatalf("text deltas = %+v", texts)
	}
	if len(thinking) != 1 || thinking[0].Content != "hmm" {
		t.Fatalf("thinking = %+v", thinking)
	}
	if len(replaces) != 1 || replaces[0].Content != "Everything changed" {
		t.Fatalf("unrelated snapshot must EventTextReplace, got %+v", replaces)
	}
}

func TestSSEToolLifecycleAndNoPrematureResult(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)

	driveFrames(sub,
		sseFrame("message.updated", map[string]any{
			"info":      map[string]any{"id": "msg_u1", "role": "user"},
			"sessionID": "ses_1",
		}),
		sseFrame("message.part.updated", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_a1",
			"part": map[string]any{
				"id": "pt_tool", "type": "tool",
				"tool": map[string]any{"id": "pt_tool", "toolName": "read",
					"state": map[string]any{"status": "running", "input": map[string]any{"path": "a.go"}}},
			},
		}),
		// Tool reaches completed AND the assistant message carries
		// time.completed — multi-step turns do exactly this mid-turn. Neither
		// may close the turn (设计 §4.3.3 红线).
		sseFrame("message.part.updated", map[string]any{
			"sessionID": "ses_1", "messageID": "msg_a1",
			"part": map[string]any{
				"id": "pt_tool", "type": "tool",
				"tool": map[string]any{"id": "pt_tool", "toolName": "read",
					"state": map[string]any{"status": "completed", "output": "contents"}},
			},
		}),
		sseFrame("message.updated", map[string]any{
			"sessionID": "ses_1",
			"info": map[string]any{
				"id": "msg_a1", "role": "assistant",
				"time": map[string]any{"created": 1, "completed": 2},
			},
		}),
	)

	events := drain(sub)
	for _, ev := range events {
		if ev.Type == core.EventResult {
			t.Fatalf("tool completion / assistant time.completed must NOT emit EventResult, got %+v", ev)
		}
	}
	var uses, results []core.Event
	for _, ev := range events {
		switch ev.Type {
		case core.EventToolUse:
			uses = append(uses, ev)
		case core.EventToolResult:
			results = append(results, ev)
		}
	}
	if len(uses) != 2 || uses[0].ToolName != "read" {
		t.Fatalf("tool uses = %+v", uses)
	}
	if len(results) != 1 || results[0].RequestID != "pt_tool" || results[0].ToolStatus != "completed" {
		t.Fatalf("tool results = %+v", results)
	}

	// Only session idle closes the turn — exactly once.
	driveFrames(sub, sseFrame("session.status", map[string]any{"sessionID": "ses_1", "type": "idle"}))
	events = drain(sub)
	results = nil
	for _, ev := range events {
		if ev.Type == core.EventResult {
			results = append(results, ev)
		}
	}
	if len(results) != 1 || !results[0].Done || results[0].Error != nil {
		t.Fatalf("idle must close exactly once with clean result, got %+v", results)
	}
}

func TestSSEZeroOutputIdleSurfacesTurnError(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)

	driveFrames(sub,
		sseFrame("message.updated", map[string]any{
			"info":      map[string]any{"id": "msg_u1", "role": "user"},
			"sessionID": "ses_1",
		}),
		sseFrame("session.status", map[string]any{"sessionID": "ses_1", "type": "idle"}),
	)

	events := drain(sub)
	var result *core.Event
	for i, ev := range events {
		if ev.Type == core.EventResult {
			result = &events[i]
		}
	}
	if result == nil {
		t.Fatal("zero-output idle must emit EventResult")
	}
	if result.Error == nil || !strings.Contains(result.Error.Error(), "may be unavailable") {
		t.Fatalf("error text must stay diagnosable, got %v", result.Error)
	}
	if !result.Done {
		t.Fatal("result must be Done")
	}
}

func TestSSEPermissionAsked(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)

	driveFrames(sub, sseFrame("permission.asked", map[string]any{
		"sessionID": "ses_1", "id": "perm_9", "tool": "bash", "title": "rm -rf build",
	}))

	events := drain(sub)
	found := false
	for _, ev := range events {
		if ev.Type == core.EventPermissionRequest {
			found = true
			if ev.RequestID != "perm_9" || ev.ToolName != "bash" {
				t.Fatalf("permission event = %+v", ev)
			}
		}
	}
	if !found {
		t.Fatal("permission.asked must emit EventPermissionRequest")
	}
}

func TestSSECatalogSignalsDoNotEnterChatStream(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)

	driveFrames(sub,
		sseFrame("session.created", map[string]any{"sessionID": "ses_new"}),
		sseFrame("session.deleted", map[string]any{"sessionID": "ses_gone"}),
	)

	if events := drain(sub); len(events) != 0 {
		t.Fatalf("catalog frames must not enter the chat stream, got %+v", events)
	}
	select {
	case <-agent.CatalogRefreshSignals():
	default:
		t.Fatal("session.created/deleted must signal catalog refresh")
	}
}

func TestSSETodoUpdatedIgnoredInPhase1(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{"/provider": `{}`}, "/tmp")
	sub := newDrivenSubscriber(t, agent)
	driveFrames(sub, sseFrame("todo.updated", map[string]any{
		"sessionID": "ses_1",
		"todos":     []any{map[string]any{"content": "t", "status": "pending"}},
	}))
	if events := drain(sub); len(events) != 0 {
		t.Fatalf("todo.updated must be ignored (todos not advertised), got %+v", events)
	}
}

func TestSSESessionUpdatedRecomputesUsageFromMessages(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{
		"/session/ses_1/message": lastAssistantPayload(),
		"/provider":              `{"all":[{"id":"zhipuai-coding-plan","models":{"glm-4.7":{"id":"glm-4.7","limit":{"context":128000}}}}],"connected":["zhipuai-coding-plan"]}`,
	}, "/tmp")
	sub := newDrivenSubscriber(t, agent)

	driveFrames(sub,
		sseFrame("message.updated", map[string]any{
			"info":      map[string]any{"id": "msg_u1", "role": "user"},
			"sessionID": "ses_1",
		}),
		// session.updated with idle status + top-level tokens present but
		// WRONG — the recompute must read the message-level truth instead.
		sseFrame("session.updated", map[string]any{
			"sessionID": "ses_1",
			"info": map[string]any{
				"id": "ses_1", "status": "idle",
				"tokens":  map[string]any{"input": 7, "output": 7},
				"modelID": "wrong-model", "providerID": "wrong-provider",
			},
		}),
	)

	// The recompute runs async (it must not stall the SSE read loop) — poll
	// for the usage event with a bounded deadline.
	var usage *core.ContextUsage
	deadline := time.After(5 * time.Second)
	for usage == nil {
		for _, ev := range drain(sub) {
			if ev.Type == core.EventContextUsageUpdated && ev.ContextUsage != nil {
				usage = ev.ContextUsage
			}
		}
		if usage != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("session.updated must recompute occupancy")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if usage.UsedTokens != 18457 || usage.ContextWindow != 128000 {
		t.Fatalf("usage must be message-level per §3.3 (used=18457 window=128000), got %+v", usage)
	}
	if cached := agent.cachedContextUsage("ses_1"); cached == nil || cached.UsedTokens != 18457 {
		t.Fatalf("usage must be remembered, got %+v", cached)
	}
}

func TestSubscribeStreamsLiveSSEFrames(t *testing.T) {
	frames := make(chan string, 8)
	sse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			// Flush headers immediately so the client's Do() returns before
			// the first frame arrives (Subscribe must not block on frames).
			flusher.Flush()
			for frame := range frames {
				fmt.Fprintf(w, "data: %s\n\n", frame)
				flusher.Flush()
			}
			return
		}
		if _, pass, ok := r.BasicAuth(); !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case "/session":
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	// Cleanup order (LIFO): close frames first so the streaming handler
	// returns, THEN close the server — a plain defer would run before any
	// cleanup and wait on the handler forever.
	t.Cleanup(sse.Close)
	t.Cleanup(func() { close(frames) })

	agent, err := New(map[string]any{
		"opencode_web_url":  sse.URL,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := agent.(*Agent)

	events, err := a.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	frames <- sseFrame("message.updated", map[string]any{
		"info": map[string]any{"id": "msg_u1", "role": "user",
			"parts": []any{map[string]any{"type": "text", "text": "live hello"}}},
		"sessionID": "ses_1",
	})
	frames <- sseFrame("session.status", map[string]any{"sessionID": "ses_1", "type": "idle"})

	sawUser, sawResult := false, false
	deadline := time.After(5 * time.Second)
	for !(sawUser && sawResult) {
		select {
		case ev := <-events:
			if ev.Type == core.EventUserMessage {
				sawUser = true
			}
			if ev.Type == core.EventResult {
				sawResult = true
			}
		case <-deadline:
			t.Fatalf("live SSE frames not relayed (user=%v result=%v)", sawUser, sawResult)
		}
	}
}

func TestIsSessionActiveThreeStates(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{
		"/session/status": `{"ses_busy":{"type":"busy"}}`,
	}, "/tmp")
	ctx := context.Background()
	if !agent.IsSessionActive(ctx, "ses_busy") {
		t.Fatal("busy map hit must report active")
	}
	if agent.IsSessionActive(ctx, "ses_idle") {
		t.Fatal("missing key on 1.18 is a definitive idle verdict")
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	deadAgent, _ := New(map[string]any{
		"opencode_web_url":  url,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if !deadAgent.(*Agent).IsSessionActive(ctx, "ses_x") {
		t.Fatal("HTTP failure must report active (conservative)")
	}
}

func TestIsSessionActiveV2MissStaysConservative(t *testing.T) {
	s := &recordingServe{responses: map[string]string{
		"/global/health":      `{"healthy":true}`,
		"/api/health":         `{"healthy":true}`,
		"/api/session":        `{"data":[]}`,
		"/api/session/active": `{"ses_other":{"type":"busy"}}`,
	}}
	base := s.start(t)
	a, _ := New(map[string]any{
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	agent := a.(*Agent)
	if _, err := agent.clientFor(context.Background()); err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	if !agent.IsSessionActive(context.Background(), "ses_idle") {
		t.Fatal("v2 /api/session/active absence is NOT a global idle verdict — must stay active")
	}
	if !agent.IsSessionActive(context.Background(), "ses_other") {
		t.Fatal("v2 active hit must report active")
	}
}

// TestSSEStreamReconnectsAndHealsAfterDrop reproduces the owner-verified
// failure (2026-08-19): a stream that dies MID-TURN must (a) reconnect and
// keep relaying, and (b) settle the armed turn via the status map when the
// serve went idle during the gap — instead of leaving iOS stuck in 执行中.
func TestSSEStreamReconnectsAndHealsAfterDrop(t *testing.T) {
	var connCount atomic.Int64
	dropped := make(chan struct{}, 1)
	sse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pass, ok := r.BasicAuth(); !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/global/event":
			n := connCount.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			flusher.Flush()
			if n == 1 {
				// Connection 1: arm a turn, stream half the deltas, then DIE.
				fmt.Fprintf(w, "data: %s\n\n", sseFrame("message.updated", map[string]any{
					"info": map[string]any{"id": "msg_u1", "role": "user",
						"parts": []any{map[string]any{"type": "text", "text": "再讲个孙悟空的故事"}}},
					"sessionID": "ses_1",
				}))
				flusher.Flush()
				fmt.Fprintf(w, "data: %s\n\n", sseFrame("message.part.delta", map[string]any{
					"sessionID": "ses_1", "messageID": "msg_a1", "partID": "pt_1",
					"field": "text", "delta": "half of the story…",
				}))
				flusher.Flush()
				dropped <- struct{}{}
				return // closes the response mid-turn
			}
			// Connection 2+: stays open, sends nothing (the serve already
			// finished the turn during the gap — terminal event lost).
			<-r.Context().Done()
		case "/session/status":
			// Serve went idle while we were disconnected.
			_, _ = w.Write([]byte(`{}`))
		default:
			if r.URL.Path == "/global/health" {
				_, _ = w.Write([]byte(`{"healthy":true}`))
				return
			}
			if r.URL.Path == "/session" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer sse.Close()

	a, err := New(map[string]any{
		"opencode_web_url":  sse.URL,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)
	// Cancellable ctx so teardown ends the long-lived reconnect (connection 2
	// blocks in the handler until the client goes away — otherwise the
	// server's Close() waits forever).
	subCtx, cancelSub := context.WithCancel(context.Background())
	defer cancelSub()
	events, err := agent.Subscribe(subCtx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	sawUser, sawHalf, sawResult := false, false, false
	deadline := time.After(15 * time.Second)
	for !(sawUser && sawHalf && sawResult) {
		select {
		case ev := <-events:
			switch ev.Type {
			case core.EventUserMessage:
				sawUser = true
			case core.EventText:
				if strings.Contains(ev.Content, "half of the story") {
					sawHalf = true
				}
			case core.EventResult:
				// The armed turn saw assistant output pre-drop → clean result.
				if ev.Error != nil {
					t.Fatalf("turn with pre-drop output must settle clean, got %v", ev.Error)
				}
				sawResult = true
			}
		case <-deadline:
			t.Fatalf("reconnect/heal incomplete: user=%v half=%v result=%v (connections=%d)",
				sawUser, sawHalf, sawResult, connCount.Load())
		}
	}
	// The redial follows the min-backoff (1s) AFTER the heal — poll for it.
	reconnectDeadline := time.After(6 * time.Second)
	for connCount.Load() < 2 {
		select {
		case <-reconnectDeadline:
			t.Fatalf("subscriber must reconnect after the drop, connections=%d", connCount.Load())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
