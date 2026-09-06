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

func TestCodecToolCallToolInputIsHumanReadable(t *testing.T) {
	// ToolInput 契约是 "human-readable summary"（core/message.go）。owner 2026-08-28：
	// 直传原始 JSON 会让 iOS ticker detail 变成 `{"command": ...}` 并被 sanitizer
	// 打回通用「正在执行工具」。摘要规则必须与 history.go hydration 的
	// toolStepTitle 同源（冷/热渲染一致）。
	c := newSessionCodec("sess-toolsum")
	events := collect(t, c, []sessionEventWire{
		env("turn/start", 0, map[string]any{"turn": 1}),
		env("step/start", 1, map[string]any{"turn": 1, "step": 1}),
		env("tool/call", 2, map[string]any{"turn": 1, "step": 1, "callId": "c1", "name": "bash", "arguments": `{"command":"rg -n pattern src/","description":"search"}`}),
		env("tool/call", 3, map[string]any{"turn": 1, "step": 1, "callId": "c2", "name": "bash", "arguments": `"{\"command\":\"git status\"}"`}),
		env("tool/call", 4, map[string]any{"turn": 1, "step": 1, "callId": "c3", "name": "todo_write", "arguments": `{"todos":[{"id":"1"}]}`}),
		env("step/end", 5, map[string]any{"turn": 1, "step": 1}),
		env("turn/end", 6, map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}),
	})
	byID := map[string]core.Event{}
	for _, ev := range events {
		if ev.Type == core.EventToolUse {
			byID[ev.RequestID] = ev
		}
	}
	if got := byID["c1"].ToolInput; got != "rg -n pattern src/" {
		t.Fatalf("bash ToolInput = %q, want command value", got)
	}
	if got := byID["c2"].ToolInput; got != "git status" {
		t.Fatalf("string-wrapped arguments must unwrap to the command, got %q", got)
	}
	if got := byID["c3"].ToolInput; got != "" {
		t.Fatalf("unrecognized shape must yield empty summary (hydration parity), got %q", got)
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
// 状态（双 turn_started、身份断裂）。command 对现为受映射内容事件（官方
// GenericCommandCard 时间线行），session/title-llm-request 仍类②忽略。
func TestCodecKnownControlPlaneTypesDoNotReset(t *testing.T) {
	c := newSessionCodec("sess-cp")
	events := collect(t, c, []sessionEventWire{
		env("turn/start", 0, map[string]any{"turn": 1}),
		env("command/run", 1, map[string]any{"commandId": "c1", "name": "compact"}),
		env("command/done", 2, map[string]any{"commandId": "c1", "kind": "success"}),
		env("session/title-llm-request", 3, map[string]any{"titleProvider": "llm"}),
		env("approval/asked", 4, map[string]any{"id": "appr-1", "toolName": "write"}),
		env("approval/decided", 5, map[string]any{"id": "appr-1", "outcome": "allowed-once"}),
		env("step/start", 6, map[string]any{"turn": 1, "step": 1}),
		env("assistant/chunk", 7, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "block-start", "index": 0, "blockType": "text",
		}}),
		env("assistant/chunk", 8, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "text-delta", "index": 0, "text": "命令后仍可流式",
		}}),
		env("assistant/chunk", 9, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "block-end", "index": 0, "block": map[string]any{"type": "text", "text": "命令后仍可流式"},
		}}),
		env("step/end", 10, map[string]any{"turn": 1, "step": 1}),
		env("turn/end", 11, map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}),
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
	// command/run|done 现为映射内容：折叠出 running + settle（name 续接）两事件。
	var cmds []*core.SessionCommandEvent
	for _, ev := range events {
		if ev.Type == core.EventSessionCommand {
			cmds = append(cmds, ev.SessionCommand)
		}
	}
	if len(cmds) != 2 {
		t.Fatalf("command pair must fold to running+settle events: %d (want 2)", len(cmds))
	}
	if cmds[0].Kind != "running" || cmds[0].Name != "compact" {
		t.Fatalf("run event: kind=%q name=%q (want running/compact)", cmds[0].Kind, cmds[0].Name)
	}
	if cmds[1].Kind != "success" || cmds[1].Name != "compact" {
		t.Fatalf("done event must continue the run's name: kind=%q name=%q (want success/compact)", cmds[1].Kind, cmds[1].Name)
	}
}

// TestCodecCommandSideProductEventsDoNotReset：/plan /compact /goal /feedback
// 执行后官方按 rc.2 known-event-types 注册表追加的 durable 副产物必须不重置
// 码器且流式随后继续。command/run|done、plan/mode 与 goal/change 现为受映射
// 内容/投影事件（官方命令卡 + plan/goal 投影），compaction/{start,prune,
// summary,end}、feedback/record 仍类②忽略。未知类型仍走 default reset，
// 不可因扩清单而放开。
func TestCodecCommandSideProductEventsDoNotReset(t *testing.T) {
	c := newSessionCodec("sess-cmd")
	events := collect(t, c, []sessionEventWire{
		env("turn/start", 0, map[string]any{"turn": 1}),
		// /plan（有 open turn 时 plan/mode 在 in-turn 边界落；官方序 run → plan/mode → done）
		env("command/run", 1, map[string]any{"commandId": "cmd-x-1", "name": "plan", "source": map[string]any{"kind": "user"}}),
		env("plan/mode", 2, map[string]any{"active": true}),
		env("command/done", 3, map[string]any{"commandId": "cmd-x-1", "kind": "success", "text": "Plan mode on. Use /plan off to leave."}),
		// /compact 四事件族
		env("command/run", 4, map[string]any{"commandId": "cmd-x-2", "name": "compact"}),
		env("compaction/start", 5, map[string]any{"compactionId": "cp-1", "sourceCommandId": "cmd-x-2"}),
		env("compaction/prune", 6, map[string]any{"compactionId": "cp-1"}),
		env("compaction/summary", 7, map[string]any{"compactionId": "cp-1", "summary": "…"}),
		env("compaction/end", 8, map[string]any{"compactionId": "cp-1"}),
		env("command/done", 9, map[string]any{"commandId": "cmd-x-2", "kind": "success"}),
		// /goal、/feedback（feedback 官方 recordInput:false）。goal/change 现为
		// 受收编投影事件（全量快照；活体座位 2026-09-05 真实形状）。
		env("command/run", 10, map[string]any{"commandId": "cmd-x-3", "name": "goal"}),
		env("goal/change", 11, map[string]any{
			"kind": "goal/change", "version": 1, "operation": "create",
			"goal": map[string]any{
				"id": "goal-t-1", "revision": 1, "objective": "ship it",
				"phase": "active", "maxGoalRounds": 256,
			},
			"roundsStarted": 0, "createdAt": 1, "updatedAt": 1,
		}),
		env("command/done", 12, map[string]any{"commandId": "cmd-x-3", "kind": "success"}),
		env("command/run", 13, map[string]any{"commandId": "cmd-x-4", "name": "feedback"}),
		env("feedback/record", 14, map[string]any{"text": "great"}),
		env("command/done", 15, map[string]any{"commandId": "cmd-x-4", "kind": "success"}),
		// 命令后流式继续
		env("step/start", 16, map[string]any{"turn": 1, "step": 1}),
		env("assistant/chunk", 17, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "block-start", "index": 0, "blockType": "text",
		}}),
		env("assistant/chunk", 18, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "text-delta", "index": 0, "text": "命令后仍可流式",
		}}),
		env("assistant/chunk", 19, map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
			"type": "block-end", "index": 0, "block": map[string]any{"type": "text", "text": "命令后仍可流式"},
		}}),
		env("step/end", 20, map[string]any{"turn": 1, "step": 1}),
		env("turn/end", 21, map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}),
	})
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
		t.Fatalf("command side products must not reset the codec: %d turn_started (want 1)", turnStarted)
	}
	if textDeltas != 1 {
		t.Fatalf("delta after command side products: %d (want 1)", textDeltas)
	}
	if terminal != 1 {
		t.Fatalf("clean terminal: %d (want 1)", terminal)
	}

	// 4 命令对 → 各折叠 running + settle（共 8 条 session_command），done 续接 name。
	var cmds []*core.SessionCommandEvent
	var planViews []*core.PlanModeEvent
	for _, ev := range events {
		switch ev.Type {
		case core.EventSessionCommand:
			cmds = append(cmds, ev.SessionCommand)
		case core.EventSessionPlanMode:
			planViews = append(planViews, ev.PlanMode)
		}
	}
	if len(cmds) != 8 {
		t.Fatalf("four command pairs must fold to 8 events: %d", len(cmds))
	}
	wantKinds := []string{"running", "success", "running", "success", "running", "success", "running", "success"}
	wantNames := []string{"plan", "plan", "compact", "compact", "goal", "goal", "feedback", "feedback"}
	for i, cmd := range cmds {
		if cmd.Kind != wantKinds[i] || cmd.Name != wantNames[i] {
			t.Fatalf("cmd[%d]: kind=%q name=%q (want %q/%q)", i, cmd.Kind, cmd.Name, wantKinds[i], wantNames[i])
		}
	}
	// cmd-x-1 的 /plan 无 args → 官方折叠不处理；唯一 plan/mode 整值事件发一次
	// 视图 {active:true, pending:false}（之后无变化，不再 churn）。
	if len(planViews) != 1 || !planViews[0].Active || planViews[0].Pending {
		t.Fatalf("plan view: %+v (want exactly one {active:true pending:false})", planViews)
	}
	// 唯一 goal/change（create）→ 一条 session_goal 整值快照 {active}。
	var goalViews []*core.GoalEvent
	for _, ev := range events {
		if ev.Type == core.EventSessionGoal {
			goalViews = append(goalViews, ev.Goal)
		}
	}
	if len(goalViews) != 1 || goalViews[0] == nil || goalViews[0].Phase != "active" ||
		goalViews[0].Objective != "ship it" || goalViews[0].ID != "goal-t-1" {
		t.Fatalf("goal view: %+v (want exactly one active snapshot {id:goal-t-1 objective:ship it})", goalViews)
	}

	// 扩清单不得放开 default：未知类型仍 reset。
	cUnknown := newSessionCodec("sess-unk")
	turnStart := env("turn/start", 0, map[string]any{"turn": 1})
	if _, err := cUnknown.apply(&turnStart); err != nil {
		t.Fatal(err)
	}
	mystery := env("mystery/event", 1, map[string]any{"x": 1})
	if _, err := cUnknown.apply(&mystery); err == nil {
		t.Fatal("unknown required event type must still reset (class ③ default)")
	}
}

// TestCodecCommandFoldOfficialSemantics：官方 plan 投影折叠
// （packages/plan/plan-mode planProjectionDefinition）逐句镜像：
//   - run args 缺失 → 不进折叠（官方 `if args === undefined return state`）
//   - run wanted = args.trim() !== "off"；视图 pending = wanted ≠ active
//   - done(success) 且 wanted ≠ active → 保留 wanted；wanted == active → 清空
//   - plan/mode 整值替换 active 并清 wanted
//   - 变化才发 session_plan_mode（防 patch churn）
//
// 另覆盖 done-only（name 续接为空，镜像官方 CommandNode.name=null）与
// 词表外 kind / 缺 commandId 的 schema-violation reset。
func TestCodecCommandFoldOfficialSemantics(t *testing.T) {
	// /plan on（args="on"）→ 视图 {false, true}；done success 保留 wanted → 无新事件；
	// plan/mode active=true → {true, false}。
	c := newSessionCodec("sess-fold1")
	events := collect(t, c, []sessionEventWire{
		env("command/run", 1, map[string]any{"commandId": "p1", "name": "plan", "args": "on"}),
		env("command/done", 2, map[string]any{"commandId": "p1", "kind": "success", "text": "Plan mode on. Use /plan off to leave."}),
		env("plan/mode", 3, map[string]any{"active": true}),
	})
	var views []*core.PlanModeEvent
	for _, ev := range events {
		if ev.Type == core.EventSessionPlanMode {
			views = append(views, ev.PlanMode)
		}
	}
	if len(views) != 2 {
		t.Fatalf("on-path plan views: %+v (want [{false true} {true false}])", views)
	}
	if views[0].Active || !views[0].Pending || !views[1].Active || views[1].Pending {
		t.Fatalf("on-path plan views: %+v (want [{false true} {true false}])", views)
	}

	// 活跃态下 /plan off：run wanted=false → 视图 {true, true}（pending）；
	// done success 保留 wanted → 仍 {true,true} 无新事件；plan/mode → {false,false}。
	// 预热 plan/mode 帧（视图 {true,false}）在 collect 窗口外，不计入。
	c2 := newSessionCodec("sess-fold2")
	c2.apply(&[]sessionEventWire{env("plan/mode", 0, map[string]any{"active": true})}[0])
	events = collect(t, c2, []sessionEventWire{
		env("command/run", 1, map[string]any{"commandId": "p2", "name": "plan", "args": "off"}),
		env("command/done", 2, map[string]any{"commandId": "p2", "kind": "success", "text": "Leaving plan mode (applies from the next step)."}),
		env("plan/mode", 3, map[string]any{"active": false}),
	})
	views = nil
	for _, ev := range events {
		if ev.Type == core.EventSessionPlanMode {
			views = append(views, ev.PlanMode)
		}
	}
	if len(views) != 2 {
		t.Fatalf("off-path plan views: %+v (want [{true true} {false false}])", views)
	}
	if !views[0].Active || !views[0].Pending || views[1].Active || views[1].Pending {
		t.Fatalf("off-path plan views: %+v", views)
	}

	// done error 不保留 wanted：/plan on 失败 → 视图回落 {false, false} 一帧。
	c3 := newSessionCodec("sess-fold3")
	events = collect(t, c3, []sessionEventWire{
		env("command/run", 1, map[string]any{"commandId": "p3", "name": "plan", "args": "on"}),
		env("command/done", 2, map[string]any{"commandId": "p3", "kind": "error", "text": "Attachments cannot accompany /plan off."}),
	})
	views = nil
	for _, ev := range events {
		if ev.Type == core.EventSessionPlanMode {
			views = append(views, ev.PlanMode)
		}
	}
	if len(views) != 2 || views[0].Active || !views[0].Pending || views[1].Active || views[1].Pending {
		t.Fatalf("error-path plan views: %+v (want [{false true} {false false}])", views)
	}

	// done-only（未见 run）：name/args 空 —— 镜像官方 CommandNode.name=null。
	c4 := newSessionCodec("sess-fold4")
	events = collect(t, c4, []sessionEventWire{
		env("command/done", 1, map[string]any{"commandId": "d1", "kind": "success", "text": "Compacted 20 history items (~11695 tokens)."}),
	})
	var settle *core.SessionCommandEvent
	for _, ev := range events {
		if ev.Type == core.EventSessionCommand {
			settle = ev.SessionCommand
		}
	}
	if settle == nil || settle.CommandID != "d1" || settle.Kind != "success" || settle.Name != "" || settle.Text == "" {
		t.Fatalf("done-only settle: %+v", settle)
	}

	// 词表外 kind / 缺 commandId → schema violation reset（类③，防御性）。
	c5 := newSessionCodec("sess-fold5")
	badKind := env("command/done", 1, map[string]any{"commandId": "d2", "kind": "cancelled"})
	if _, err := c5.apply(&badKind); err == nil {
		t.Fatal("unknown done kind must reset")
	}
	badRun := env("command/run", 1, map[string]any{"commandId": "d3"})
	if _, err := c5.apply(&badRun); err == nil {
		t.Fatal("command/run missing name must reset")
	}
}

// TestCodecGoalChangeLifecycle：goal/change 全量快照投影的完整生命周期
// （官方 domain.ts GoalSnapshotChangeMeta / GoalClearChangeMeta）。断言：
// create→pause→clear 每变化发一条 session_goal；同快照重复（同 revision 重放）
// 不 churn；clear 后发 nil payload 事件（远端清除横条）；未知 operation /
// 未知 phase / 缺 goal 快照 → fail visibly reset（不猜状态）。
func TestCodecGoalChangeLifecycle(t *testing.T) {
	c := newSessionCodec("sess-goal")
	snapshot := func(revision int64, phase, objective string) map[string]any {
		return map[string]any{
			"kind": "goal/change", "version": 1, "operation": phaseOf(phase, revision),
			"goal": map[string]any{
				"id": "goal-l-1", "revision": revision, "objective": objective,
				"phase": phase, "maxGoalRounds": 256,
			},
			"roundsStarted": 0, "createdAt": 1, "updatedAt": 2,
		}
	}
	events := collect(t, c, []sessionEventWire{
		env("goal/change", 1, snapshot(1, "active", "写个封神榜故事")),
		env("goal/change", 2, snapshot(1, "active", "写个封神榜故事")), // 同快照重放：不 churn
		env("goal/change", 3, snapshot(2, "paused", "写个封神榜故事")),
		env("goal/change", 4, map[string]any{
			"kind": "goal/change", "version": 1, "operation": "clear",
			"cleared": map[string]any{"id": "goal-l-1", "revision": 3}, "clearedAt": 3,
		}),
	})
	var goals []*core.GoalEvent
	for _, ev := range events {
		if ev.Type == core.EventSessionGoal {
			goals = append(goals, ev.Goal)
		}
	}
	if len(goals) != 3 {
		t.Fatalf("goal lifecycle must emit exactly 3 snapshots (create, pause, clear): %d", len(goals))
	}
	if goals[0] == nil || goals[0].Phase != "active" || goals[0].Objective != "写个封神榜故事" || goals[0].Revision != 1 {
		t.Fatalf("create snapshot: %+v", goals[0])
	}
	if goals[1] == nil || goals[1].Phase != "paused" || goals[1].Revision != 2 {
		t.Fatalf("pause snapshot: %+v", goals[1])
	}
	if goals[2] != nil {
		t.Fatalf("clear must emit a nil-payload snapshot event, got %+v", goals[2])
	}

	// 严格解码：未知 operation / 未知 phase / 快照形缺 goal → reset。
	for name, bad := range map[string]map[string]any{
		"unknown-operation": {"kind": "goal/change", "operation": "teleport"},
		"unknown-phase":     {"kind": "goal/change", "operation": "create", "goal": map[string]any{"id": "g", "revision": 1, "objective": "x", "phase": "dreaming"}},
		"missing-goal":      {"kind": "goal/change", "operation": "create"},
	} {
		cb := newSessionCodec("sess-goal-bad")
		if _, err := cb.apply(env2ptr(env("goal/change", 1, bad))); err == nil {
			t.Fatalf("%s must reset (fail visibly, never guess phase state)", name)
		}
	}
}

// phaseOf 把 revision 推到与相位一致的 operation（仅测试可读性用）。
func phaseOf(phase string, revision int64) string {
	switch {
	case phase == "active" && revision == 1:
		return "create"
	case phase == "paused":
		return "pause"
	default:
		return "edit"
	}
}

func env2ptr(e sessionEventWire) *sessionEventWire { return &e }

// TestToolStepTitleSubagentDisplayKeys：官方 subagent 工具的展示摘要参数
// description（tool-subagent src/index.ts："description of the delegated task,
// for display"）必须成为工具行标题；无 description 时回退 prompt 截断。
// 2026-09-05 owner 报障：iOS subagent 行标题为空（旧键表不认 description）。
func TestToolStepTitleSubagentDisplayKeys(t *testing.T) {
	live := toolStepTitle("subagent", []byte(`{"description":"写封神榜故事至/tmp","prompt":"创作一篇约 10,000 个汉字的封神榜题材原创故事…"}`))
	if live != "写封神榜故事至/tmp" {
		t.Fatalf("subagent title = %q, want the description display summary", live)
	}
	// 冷拉同源：string-wrapped arguments（DSH 历史把 arguments 记为 JSON 字符串）。
	wrapped := toolStepTitle("subagent", []byte(`"{\"description\":\"调研竞品\",\"prompt\":\"…\"}"`))
	if wrapped != "调研竞品" {
		t.Fatalf("wrapped subagent title = %q", wrapped)
	}
	// 无 description：回退 prompt，超长截断（80 rune，CJK 安全）。
	longPrompt := "第一步先做甲第二步做乙第三步做丙第四步做丁第五步做戊第六步做己第七步做庚第八步做辛第九步做壬第十步做癸收尾"
	fallback := toolStepTitle("subagent", []byte(`{"prompt":"`+longPrompt+`"}`))
	want := longPrompt
	if runes := []rune(want); len(runes) > 80 {
		want = string(runes[:80])
	}
	if fallback != want {
		t.Fatalf("prompt fallback title = %q, want %q", fallback, want)
	}
	// coding 工具主参数仍然优先于 description（键序契约）。
	if got := toolStepTitle("bash", []byte(`{"command":"git status","description":"查看状态"}`)); got != "git status" {
		t.Fatalf("bash title must stay command-first, got %q", got)
	}
}

// TestCodecSubagentSettledContextInjection：官方 continuation.ts settle 通知
// （注入父会话的 user/message，source{kind:"subagent-settled", form:"notice",
// summary, senderSessionId}）→ 一条 EventContextInjection（itemId "ctxinj:<seq>"，
// 与冷拉 history.go 同 id）；summary 空 / 其余 kind 维持 known-drop，不 reset 流。
func TestCodecSubagentSettledContextInjection(t *testing.T) {
	cb := newSessionCodec("sess-ctxinj")
	real := collect(t, cb, []sessionEventWire{
		env("turn/start", 0, map[string]any{"turn": 1}),
		env("user/message", 1, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Background subagent sess-bg finished after 1 round."},
				{"type": "text", "text": "已生成封神榜第一章。"},
			},
			"source": map[string]any{
				"kind": "subagent-settled", "form": "notice",
				"summary":         "Background subagent sess-bg finished after 1 round.",
				"senderSessionId": "sess-bg",
			},
		}),
		env("user/message", 2, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "形状未知"}},
			"source": map[string]any{"kind": "subagent-settled", "form": "notice"},
		}),
		env("user/message", 3, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "<goal_round>"}},
			"source": map[string]any{"kind": "goal"},
		}),
		env("user/message", 4, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "正常输入"}},
			"source": map[string]any{"kind": "user"},
		}),
	})
	var injections []*core.ContextInjectionEvent
	var userEchoes int
	for _, ev := range real {
		switch ev.Type {
		case core.EventContextInjection:
			injections = append(injections, ev.ContextInjection)
		case core.EventUserMessage:
			userEchoes++
		}
	}
	if len(injections) != 1 {
		t.Fatalf("want exactly 1 context injection (summary-bearring settle), got %d: %+v", len(injections), injections)
	}
	inj := injections[0]
	if inj.ItemID != "ctxinj:1" {
		t.Fatalf("itemId = %q, want ctxinj:1 (live/cold same identity)", inj.ItemID)
	}
	if inj.Kind != "subagent-settled" || inj.Form != "notice" {
		t.Fatalf("kind/form = %q/%q", inj.Kind, inj.Form)
	}
	if inj.Summary != "Background subagent sess-bg finished after 1 round." {
		t.Fatalf("summary not verbatim: %q", inj.Summary)
	}
	if inj.SenderSessionID != "sess-bg" {
		t.Fatalf("senderSessionId = %q", inj.SenderSessionID)
	}
	if inj.Text != "Background subagent sess-bg finished after 1 round.\n已生成封神榜第一章。" {
		t.Fatalf("text (joined model-facing body) = %q", inj.Text)
	}
	if userEchoes != 1 {
		t.Fatalf("kind=user echo count = %d, want 1 (goal/empty-settle stays dropped)", userEchoes)
	}
	// 注入不打断 activeTurn：settle 落在 turn 内时 turn 归属保持原样（后续
	// kind=user 行仍折进同一 dshw turn）。
	if got := cb.activeTurnID; got != dshwTurnID("sess-ctxinj", 1) {
		t.Fatalf("activeTurnID after injection = %q, want %q (injection must not steal the turn)", got, dshwTurnID("sess-ctxinj", 1))
	}
}
