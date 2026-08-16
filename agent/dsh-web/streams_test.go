package dshweb

// §8-3 unit tests: codec mapping (copied stdio table + the two carrier
// adaptations), dual-stream pump routing, reconnect reopen, the
// SessionActivityProbing three states, and the host-frame refresh signal.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// env builds a session event envelope helper for codec tests.
func env(typ string, seq int64, data any) sessionEventWire {
	raw, _ := json.Marshal(data)
	return sessionEventWire{Type: typ, Seq: seq, Time: seq * 1000, Data: raw}
}

// collect drains the codec mapping for one envelope sequence.
func collect(t *testing.T, codec *sessionCodec, envs []sessionEventWire) []core.Event {
	t.Helper()
	var out []core.Event
	for i := range envs {
		events, err := codec.apply(&envs[i])
		if err != nil {
			t.Fatalf("apply %s (seq %d): %v", envs[i].Type, envs[i].Seq, err)
		}
		out = append(out, events...)
	}
	return out
}

func TestCodecMapsFullTurnSequence(t *testing.T) {
	c := newSessionCodec("sess-full")
	events := collect(t, c, []sessionEventWire{
		env("turn/start", 0, map[string]any{"turn": 1}),
		env("user/message", 1, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "跑一下测试"}},
			"source":  map[string]any{"kind": "user"}, "id": "m1",
		}),
		env("step/start", 2, map[string]any{"turn": 1, "step": 1}),
		env("assistant/chunk", 3, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "block-start", "index": 0, "blockType": "reasoning",
		}}),
		env("assistant/chunk", 4, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "reasoning-delta", "index": 0, "text": "推理",
		}}),
		env("assistant/chunk", 5, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "block-end", "index": 0, "block": map[string]any{"type": "reasoning", "text": "推理"},
		}}),
		env("assistant/chunk", 6, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "block-start", "index": 1, "blockType": "text",
		}}),
		env("assistant/chunk", 7, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "text-delta", "index": 1, "text": "你好",
		}}),
		env("assistant/chunk", 8, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "block-end", "index": 1, "block": map[string]any{"type": "text", "text": "你好"},
		}}),
		env("assistant/message", 9, map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{"content": []map[string]any{
				{"type": "reasoning", "text": "推理"},
				{"type": "text", "text": "你好"},
			}},
		}),
		env("step/end", 10, map[string]any{"turn": 1, "step": 1}),
		env("turn/end", 11, map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}),
	})

	wantTypes := []core.EventType{
		core.EventTurnStarted, core.EventUserMessage,
		core.EventThinking, core.EventText,
		core.EventResult,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("event count %d, want %d: %+v", len(events), len(wantTypes), events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event[%d] type %v, want %v", i, events[i].Type, want)
		}
	}
	if events[1].Content != "跑一下测试" || events[1].ItemID != "m1" {
		t.Fatalf("user message mapping: %+v", events[1])
	}
	if events[2].Content != "推理" || events[3].Content != "你好" {
		t.Fatalf("delta mapping: %+v %+v", events[2], events[3])
	}
	if !events[4].Done || events[4].Error != nil {
		t.Fatalf("terminal must be a clean completion: %+v", events[4])
	}
}

func TestCodecTurnErrorPassesReasonVerbatim(t *testing.T) {
	// 坑 7/8: the terminal carries the raw official reason — never collapsed.
	c := newSessionCodec("sess-err")
	events := collect(t, c, []sessionEventWire{
		env("turn/start", 0, map[string]any{"turn": 1}),
		env("step/start", 1, map[string]any{"turn": 1, "step": 1}),
		env("tool/call", 2, map[string]any{"turn": 1, "step": 1, "callId": "c1", "name": "bash", "arguments": `{"command":"ls"}`}),
		env("tool/result", 3, map[string]any{"turn": 1, "step": 1, "message": map[string]any{
			"source":  map[string]any{"kind": "tool", "callId": "c1"},
			"content": []map[string]any{{"type": "tool-result", "toolCallId": "c1", "isError": true, "content": []map[string]any{{"type": "text", "text": "boom 原始错误"}}}},
		}}),
		env("step/end", 4, map[string]any{"turn": 1, "step": 1}),
		env("turn/end", 5, map[string]any{"turn": 1, "reason": map[string]any{"kind": "error"}}),
	})
	var toolUse, toolResult, terminal *core.Event
	for i := range events {
		switch events[i].Type {
		case core.EventToolUse:
			toolUse = &events[i]
		case core.EventToolResult:
			toolResult = &events[i]
		case core.EventResult:
			terminal = &events[i]
		}
	}
	if toolUse == nil || toolUse.ToolName != "bash" || toolUse.RequestID != "c1" {
		t.Fatalf("tool use: %+v", toolUse)
	}
	if toolResult == nil || toolResult.ToolStatus != "failed" || toolResult.ToolResult != "boom 原始错误" {
		t.Fatalf("tool result: %+v", toolResult)
	}
	if terminal == nil || terminal.Error == nil {
		t.Fatalf("error turn must settle as an error terminal: %+v", terminal)
	}
	if got := terminal.Error.Error(); got != `turn ended with reason "error"` {
		t.Fatalf("terminal reason verbatim: %q", got)
	}
}

func TestCodecBaselineTolerantAndGapResets(t *testing.T) {
	// Adaptation 1: first frame at seq 7 (mid-log join) is accepted.
	c := newSessionCodec("sess-base")
	events := collect(t, c, []sessionEventWire{
		env("turn/start", 7, map[string]any{"turn": 3}),
	})
	if len(events) != 1 || events[0].Type != core.EventTurnStarted {
		t.Fatalf("mid-log baseline: %+v", events)
	}
	// Gap: frame dropped with reset (no event), next frame re-baselines.
	if _, err := c.apply(&sessionEventWire{Type: "turn/end", Seq: 20, Time: 1,
		Data: mustJSON(map[string]any{"turn": 3, "reason": map[string]any{"kind": "completed"}})}); err == nil {
		t.Fatal("gap must error")
	}
	codecs := map[string]*sessionCodec{"s": c}
	feedWithReset(codecs, "s", &sessionEventWire{Type: "turn/end", Seq: 20, Time: 1,
		Data: mustJSON(map[string]any{"turn": 3, "reason": map[string]any{"kind": "completed"}})},
		func([]core.Event) {})
	if len(codecs) != 1 || codecs["s"] == c {
		t.Fatal("feedWithReset must replace the codec after a reset")
	}
}

func TestCodecOrphanTurnAdoption(t *testing.T) {
	// Adaptation 2: an in-flight external turn joins mid-stream — the chunk's
	// own turn/step data adopts the turn and the boundary is surfaced.
	c := newSessionCodec("sess-orphan")
	events := collect(t, c, []sessionEventWire{
		env("assistant/chunk", 4, map[string]any{"turn": 2, "step": 1, "chunk": map[string]any{
			"type": "block-start", "index": 0, "blockType": "text",
		}}),
		env("assistant/chunk", 5, map[string]any{"turn": 2, "step": 1, "chunk": map[string]any{
			"type": "text-delta", "index": 0, "text": "中段",
		}}),
	})
	if len(events) != 2 || events[0].Type != core.EventTurnStarted || events[1].Type != core.EventText {
		t.Fatalf("orphan adoption: %+v", events)
	}
	if events[0].TurnID == "" || events[1].Content != "中段" {
		t.Fatalf("adoption identities: %+v %+v", events[0], events[1])
	}
}

// ── Stream pump: routing + reconnect + refresh signal ──────────────────────

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func TestSubscribeRoutesExternalSessionToPassiveChannel(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.SetMuxFrames([]any{
		map[string]any{"type": "session/event", "sessionId": "ext-1",
			"event": map[string]any{"type": "turn/start", "seq": 0, "time": 1, "data": map[string]any{"turn": 1}}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := a.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	select {
	case ev := <-ch:
		if ev.Type != core.EventTurnStarted || ev.SessionID != "ext-1" {
			t.Fatalf("passive event: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("external session event never reached the passive channel")
	}
}

func TestBoundSessionReceivesOwnEventsNotPassive(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session.create"] = fakeRPCResponse{value: map[string]any{"sessionId": "own-1"}}
	f.hooks["session.history"] = func(_ []byte) fakeRPCResponse {
		return fakeRPCResponse{value: map[string]any{"events": []any{}, "hasMore": false}}
	}
	f.SetMuxFrames([]any{
		map[string]any{"type": "session/event", "sessionId": "own-1",
			"event": map[string]any{"type": "turn/start", "seq": 0, "time": 1, "data": map[string]any{"turn": 1}}},
	})

	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	passiveCh, err := a.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Drain passive first.
	go func() {
		for range passiveCh {
		}
	}()

	select {
	case ev := <-sess.Events():
		if ev.Type != core.EventTurnStarted || ev.SessionID != "own-1" {
			t.Fatalf("bound session event: %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bound session never received its mux event")
	}
	// Single-delivery rule: the passive channel must NOT carry own-1's frame
	// (best-effort negative check with a short window).
	select {
	case ev := <-passiveCh:
		t.Fatalf("bound session event leaked to passive channel: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestStreamReconnectReopensAfterDrop(t *testing.T) {
	// 坑 8-class resilience: official v1 has no since — recovery is reopen.
	f := newFakeDSHServer(t)
	defer f.Close()
	f.closeAfterPush = true
	f.SetMuxFrames([]any{
		map[string]any{"type": "session/subscribed", "sessionId": "s", "lastSeq": 0},
	})
	a := newTestAgent(t, f)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := a.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 10*time.Second, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.upgradeSeen["/api/events.mux"] >= 2
	}) {
		t.Fatalf("stream did not reopen after drop (dials=%d)", f.upgradeSeen["/api/events.mux"])
	}
}

func TestHostFrameTriggersCatalogRefreshSignal(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.SetHostFrames([]any{
		map[string]any{"type": "host/session-added", "sessionId": "new-1", "blank": false, "cwd": "/p"},
		map[string]any{"type": "host/session-status", "sessionId": "new-1", "running": true},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := a.CatalogRefreshSignals()
	if _, err := a.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-signals:
	case <-time.After(5 * time.Second):
		t.Fatal("host/session-added never signaled a catalog refresh")
	}
	// Running cache flip (badge data source, §4.3.1).
	if !waitFor(t, 5*time.Second, func() bool {
		running, known := a.running.get("new-1")
		return known && running
	}) {
		t.Fatal("host/session-status never flipped the running cache")
	}
}

// ── SessionActivityProbing 三态 (§4.3.2 M1) ────────────────────────────────

func TestIsSessionActiveThreeStates(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)

	// State 3 first (unknown + unreachable instance ⇒ conservative active):
	// point a fresh agent at a dead instance so the refresh call fails.
	dead := &Agent{}
	dead.resolver = NewResolver(
		WithProbeURLs([]string{"http://127.0.0.1:1"}),
		withManagedStarter(&countingStarter{fail: true}),
	)
	if !dead.IsSessionActive(context.Background(), "any") {
		t.Fatal("probe failure must be conservative ACTIVE")
	}

	// Known states via cache.
	a.running.mu.Lock()
	a.running.set = map[string]bool{"run-1": true, "idle-1": false}
	a.running.mu.Unlock()
	if !a.IsSessionActive(context.Background(), "run-1") {
		t.Fatal("running session must be active")
	}
	if a.IsSessionActive(context.Background(), "idle-1") {
		t.Fatal("known-idle session must NOT be active (dead-tail settle)")
	}

	// Unknown to cache but listed ⇒ refresh resolves it.
	f.handlers["session.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{{"sessionId": "fresh-1", "updatedAt": 1, "running": false, "blank": false}},
	}}
	if a.IsSessionActive(context.Background(), "fresh-1") {
		t.Fatal("refreshed idle session must not be active")
	}
	// Still-unknown after refresh ⇒ conservative active.
	if !a.IsSessionActive(context.Background(), "ghost") {
		t.Fatal("unknown-after-refresh must stay conservative ACTIVE")
	}
}

// ── 真机矩阵修复回归（2026-08-16）──────────────────────────────────────────

// TestCodecKnownControlPlaneTypesDoNotReset：真机日志证实 command/run、
// command/done、session/title-llm-request（官方 known-event-types 注册表）
// 会出现在 web profile 的 mux 流中；此前类③策略对它们重置码器，杀了 mid-turn
// 状态（双 turn_started、身份断裂）。三者无 timeline 内容，必须类②忽略。
func TestCodecKnownControlPlaneTypesDoNotReset(t *testing.T) {
	c := newSessionCodec("sess-cp")
	events := collect(t, c, []sessionEventWire{
		env("turn/start", 0, map[string]any{"turn": 1}),
		env("command/run", 1, map[string]any{"id": "c1"}),
		env("command/done", 2, map[string]any{"id": "c1"}),
		env("session/title-llm-request", 3, map[string]any{"titleProvider": "llm"}),
		env("step/start", 4, map[string]any{"turn": 1, "step": 1}),
		env("assistant/chunk", 5, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "block-start", "index": 0, "blockType": "text",
		}}),
		env("assistant/chunk", 6, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "text-delta", "index": 0, "text": "命令后仍可流式",
		}}),
		env("assistant/chunk", 7, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "block-end", "index": 0, "block": map[string]any{"type": "text", "text": "命令后仍可流式"},
		}}),
		env("step/end", 8, map[string]any{"turn": 1, "step": 1}),
		env("turn/end", 9, map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}),
	})
	// 恰一个 turn_started（无重置产生的第二个）+ delta 保留 + 干净收口。
	var turnStarted, textDeltas, terminal int
	for _, ev := range events {
		switch ev.Type {
		case core.EventTurnStarted:
			turnStarted++
		case core.EventText:
			textDeltas++
		case core.EventResult:
			terminal++
		}
	}
	if turnStarted != 1 {
		t.Fatalf("control-plane types must not reset the codec: %d turn_started (want 1)", turnStarted)
	}
	if textDeltas != 1 {
		t.Fatalf("delta after control-plane types: %d", textDeltas)
	}
	if terminal != 1 {
		t.Fatalf("clean terminal: %d", terminal)
	}
}
