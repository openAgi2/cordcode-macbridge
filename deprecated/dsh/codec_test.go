package dsh

// Phase-1 codec tests (design §3.3 mapping, §3.6.1 active-turn state machine
// + identity matrix, §3.6.2 source.kind routing). Fixtures are derived from
// the frozen gate0 dumps (scripts/dsh-gate0/dumps/run{1,2,3}*.jsonl) — no
// real key involved.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const testNonce = "0123456789abcdef0123456789abcdef"

// envelope builds a root session.event envelope from raw data JSON.
func envelope(seq int, typ string, data string) *dshEvent {
	return &dshEvent{
		Type: typ,
		Seq:  seq,
		Time: 1786634455860,
		Data: json.RawMessage(data),
	}
}

// envelopeFull builds an envelope carrying extra raw top-level fields
// (e.g. ignorable) for ignorable-policy tests.
func envelopeFull(seq int, typ, data, ignorable string) *dshEvent {
	e := envelope(seq, typ, data)
	if ignorable != "" {
		e.Ignorable = json.RawMessage(ignorable)
	}
	return e
}

func mustEvents(t *testing.T, c *dshCodec, envs ...*dshEvent) []core.Event {
	t.Helper()
	var out []core.Event
	for _, env := range envs {
		evs, err := c.apply(env)
		if err != nil {
			t.Fatalf("apply(%s seq=%d) unexpected violation: %v", env.Type, env.Seq, err)
		}
		out = append(out, evs...)
	}
	return out
}

func mustViolation(t *testing.T, c *dshCodec, env *dshEvent) error {
	t.Helper()
	_, err := c.apply(env)
	if err == nil {
		t.Fatalf("apply(%s seq=%d) expected protocol violation, got none", env.Type, env.Seq)
	}
	if _, ok := err.(*dshProtocolError); !ok {
		t.Fatalf("expected *dshProtocolError, got %T: %v", err, err)
	}
	return err
}

// newTurn opens a well-formed turn 1 / step 1.
func openTurnStep1(t *testing.T, c *dshCodec) {
	t.Helper()
	mustEvents(t, c,
		envelope(0, "permission/preset", `{"preset":"workspace-write"}`),
		envelope(1, "turn/start", `{"turn":1}`),
		envelope(2, "step/start", `{"turn":1,"step":1}`),
	)
}

func TestTurnStartIdentity(t *testing.T) {
	c := newCodec(testNonce)
	evs := mustEvents(t, c, envelope(0, "turn/start", `{"turn":1}`))
	if len(evs) != 1 || evs[0].Type != core.EventTurnStarted {
		t.Fatalf("want one EventTurnStarted, got %+v", evs)
	}
	want := "p" + testNonce + "-t1"
	if evs[0].TurnID != want {
		t.Fatalf("TurnID = %q, want %q", evs[0].TurnID, want)
	}
}

func TestUserMessageSourceKindRouting(t *testing.T) {
	// source.kind=user → EventUserMessage bound to active turn.
	c := newCodec(testNonce)
	openTurnStep1(t, c)
	evs := mustEvents(t, c, envelope(3, "user/message",
		`{"content":[{"type":"text","text":"Run the bash command: echo hi"}],"source":{"kind":"user"},"role":"user","id":"cc875db3-bfb2-447b-8abb-5de10a6fc2f6"}`))
	if len(evs) != 1 || evs[0].Type != core.EventUserMessage {
		t.Fatalf("want one EventUserMessage, got %+v", evs)
	}
	if evs[0].Content != "Run the bash command: echo hi" {
		t.Fatalf("content = %q", evs[0].Content)
	}
	if evs[0].ItemID != "cc875db3-bfb2-447b-8abb-5de10a6fc2f6" {
		t.Fatalf("ItemID = %q, want data.id", evs[0].ItemID)
	}
	if evs[0].TurnID != "p"+testNonce+"-t1" {
		t.Fatalf("TurnID = %q, want active turn id", evs[0].TurnID)
	}

	// source.kind=plugin → no event (permission runtime context must never
	// overwrite the real user prompt, §3.6.2).
	evs = mustEvents(t, c, envelope(4, "user/message",
		`{"content":[{"type":"text","text":"Current runtime context."}],"source":{"kind":"plugin"},"role":"user","id":"plugin-ctx-1"}`))
	if len(evs) != 0 {
		t.Fatalf("plugin user/message must emit nothing, got %+v", evs)
	}

	// unknown source.kind → fail visibly, never guess user.
	c2 := newCodec(testNonce)
	openTurnStep1(t, c2)
	mustViolation(t, c2, envelope(3, "user/message",
		`{"content":[{"type":"text","text":"x"}],"source":{"kind":"mystery"},"role":"user","id":"u1"}`))
}

func TestUserMessageWithoutActiveTurnFails(t *testing.T) {
	// §3.6.1: user/message(user) with no active turn must fail visibly — no
	// UUID fallback (a fallback would split the user item from the assistant
	// turn's projection).
	c := newCodec(testNonce)
	err := mustViolation(t, c, envelope(0, "user/message",
		`{"content":[{"type":"text","text":"x"}],"source":{"kind":"user"},"role":"user","id":"u1"}`))
	if !strings.Contains(err.Error(), "no active turn") {
		t.Fatalf("violation should mention no active turn: %v", err)
	}
}

func TestAssistantChunkMapping(t *testing.T) {
	c := newCodec(testNonce)
	openTurnStep1(t, c)

	// text-delta → EventText with ItemID == TurnID (identity matrix); the
	// delta lives inside an open text block (real-wire block discipline).
	mustEvents(t, c, envelope(3, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`))
	evs := mustEvents(t, c, envelope(4, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"Done"}}`))
	if len(evs) != 1 || evs[0].Type != core.EventText || evs[0].Content != "Done" {
		t.Fatalf("text-delta mapping: %+v", evs)
	}
	if evs[0].ItemID != evs[0].TurnID || evs[0].TurnID != "p"+testNonce+"-t1" {
		t.Fatalf("assistant identity: ItemID=%q TurnID=%q", evs[0].ItemID, evs[0].TurnID)
	}
	// block-end matches the accumulated delta → no event, content folded.
	evs = mustEvents(t, c, envelope(5, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"block-end","index":0,"block":{"type":"text","text":"Done"}}}`))
	if len(evs) != 0 {
		t.Fatalf("block boundaries must not emit, got %+v", evs)
	}

	// reasoning-delta → EventThinking, same identity rule.
	mustEvents(t, c, envelope(6, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"block-start","index":1,"blockType":"reasoning"}}`))
	evs = mustEvents(t, c, envelope(7, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"reasoning-delta","index":1,"text":"The user"}}`))
	if len(evs) != 1 || evs[0].Type != core.EventThinking || evs[0].Content != "The user" {
		t.Fatalf("reasoning-delta mapping: %+v", evs)
	}
	if evs[0].ItemID != evs[0].TurnID {
		t.Fatalf("reasoning identity: ItemID=%q TurnID=%q", evs[0].ItemID, evs[0].TurnID)
	}
	mustEvents(t, c, envelope(8, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"block-end","index":1,"block":{"type":"reasoning","text":"The user"}}}`))

	// tool-call-delta (argumentsDelta, NOT arguments) → assemble-only, no event.
	mustEvents(t, c, envelope(9, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"block-start","index":2,"blockType":"tool-call"}}`))
	evs = mustEvents(t, c, envelope(10, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"tool-call-delta","index":2,"id":"call_00_x","name":"bash","argumentsDelta":"{}"}}`))
	if len(evs) != 0 {
		t.Fatalf("tool-call-delta must not emit tool start, got %+v", evs)
	}
	mustEvents(t, c, envelope(11, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"block-end","index":2,"block":{"type":"tool-call","id":"call_00_x","name":"bash","arguments":"{}"}}}`))

	// usage chunk → context-pressure projection (§3.7) + captured for the
	// turn terminal.
	evs = mustEvents(t, c, envelope(12, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"usage","usage":{"inputTokens":222,"outputTokens":78,"cacheReadTokens":1280,"reasoningTokens":12}}}`))
	if len(evs) != 1 || evs[0].Type != core.EventContextUsageUpdated || evs[0].ContextUsage == nil {
		t.Fatalf("usage chunk must emit context usage, got %+v", evs)
	}
	cu := evs[0].ContextUsage
	if cu.UsedTokens != 222+1280 || cu.TotalTokens != cu.UsedTokens || cu.CachedInputTokens != 1280 {
		t.Fatalf("usage formula: %+v", cu)
	}
	if c.lastUsage == nil || c.lastUsage.InputTokens != 222 || c.lastUsage.CacheReadTokens != 1280 {
		t.Fatalf("usage not captured: %+v", c.lastUsage)
	}

	// finish → internal boundary, no event.
	evs = mustEvents(t, c, envelope(13, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"finish","reason":{"kind":"tool-calls"}}}`))
	if len(evs) != 0 {
		t.Fatalf("finish must not emit, got %+v", evs)
	}

	// unknown chunk type → protocol violation.
	mustViolation(t, c, envelope(14, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"hologram-delta","index":0}}`))

	// unknown blockType → protocol violation.
	c3 := newCodec(testNonce)
	openTurnStep1(t, c3)
	mustViolation(t, c3, envelope(3, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"video"}}`))

	// block-end text disagreeing with the accumulated deltas → violation
	// (§3.7 peer check).
	c4 := newCodec(testNonce)
	openTurnStep1(t, c4)
	mustEvents(t, c4,
		envelope(3, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`),
		envelope(4, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"abc"}}`),
	)
	mustViolation(t, c4, envelope(5, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"block-end","index":0,"block":{"type":"text","text":"different"}}}`))
}

func TestAssistantMessageValidateOnly(t *testing.T) {
	c := newCodec(testNonce)
	openTurnStep1(t, c)
	mustEvents(t, c,
		envelope(3, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`),
		envelope(4, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"Done"}}`),
		envelope(5, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"block-end","index":0,"block":{"type":"text","text":"Done"}}}`),
		envelope(6, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"usage","usage":{"inputTokens":222,"outputTokens":78,"cacheReadTokens":1280,"reasoningTokens":12}}}`),
	)
	// Assembled message agreeing with the chunks: no event — live text owner
	// is the chunk path (§3.7).
	evs := mustEvents(t, c, envelope(7, "assistant/message",
		`{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"Done"}],"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-chat"},"id":"2fad11bb"},"usage":{"inputTokens":222,"outputTokens":78,"cacheReadTokens":1280,"reasoningTokens":12},"sourceEventSeqs":[3,4,5,6],"surfaceOp":"append"}`))
	if len(evs) != 0 {
		t.Fatalf("assistant/message must be validate-only, got %+v", evs)
	}

	// Assembled text DISAGREEING with the chunk deltas → violation.
	c2 := newCodec(testNonce)
	openTurnStep1(t, c2)
	mustEvents(t, c2,
		envelope(3, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`),
		envelope(4, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"chunk text"}}`),
		envelope(5, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"block-end","index":0,"block":{"type":"text","text":"chunk text"}}}`),
	)
	mustViolation(t, c2, envelope(6, "assistant/message",
		`{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"assembled lies"}],"id":"m2"}}`))

	// Assembled usage disagreeing with the chunk usage → violation (双源相等).
	c3 := newCodec(testNonce)
	openTurnStep1(t, c3)
	mustEvents(t, c3, envelope(3, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"usage","usage":{"inputTokens":10,"outputTokens":2,"cacheReadTokens":0,"reasoningTokens":0}}}`))
	mustViolation(t, c3, envelope(4, "assistant/message",
		`{"turn":1,"step":1,"message":{"role":"assistant","content":[],"id":"m3"},"usage":{"inputTokens":99,"outputTokens":2,"cacheReadTokens":0,"reasoningTokens":0}}`))
}

func TestToolCallAndResultMapping(t *testing.T) {
	c := newCodec(testNonce)
	openTurnStep1(t, c)
	mustEvents(t, c,
		envelope(3, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"block-start","index":1,"blockType":"tool-call"}}`),
		envelope(4, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"tool-call-delta","index":1,"id":"call_00_ahVZeE0rrsgxPCpMWoqm1631","name":"bash","argumentsDelta":"{\"command\": \"echo hi\"}"}}`),
		envelope(5, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"block-end","index":1,"block":{"type":"tool-call","id":"call_00_ahVZeE0rrsgxPCpMWoqm1631","name":"bash","arguments":"{\"command\": \"echo hi\"}"}}}`),
	)

	evs := mustEvents(t, c, envelope(6, "tool/call",
		`{"turn":1,"step":1,"callId":"call_00_ahVZeE0rrsgxPCpMWoqm1631","name":"bash","arguments":"{\"command\": \"echo hi\"}"}`))
	if len(evs) != 1 || evs[0].Type != core.EventToolUse {
		t.Fatalf("tool/call mapping: %+v", evs)
	}
	ev := evs[0]
	if ev.ToolName != "bash" || ev.RequestID != "call_00_ahVZeE0rrsgxPCpMWoqm1631" || ev.ItemID != ev.RequestID {
		t.Fatalf("tool identity: %+v", ev)
	}
	if ev.TurnID != "p"+testNonce+"-t1" {
		t.Fatalf("tool TurnID = %q", ev.TurnID)
	}
	if ev.ToolInput != `{"command": "echo hi"}` {
		t.Fatalf("tool input = %q", ev.ToolInput)
	}

	// tool/call arguments disagreeing with the accumulated deltas → violation.
	cMismatch := newCodec(testNonce)
	openTurnStep1(t, cMismatch)
	mustEvents(t, cMismatch,
		envelope(3, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"block-start","index":1,"blockType":"tool-call"}}`),
		envelope(4, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"tool-call-delta","index":1,"id":"call_x","name":"bash","argumentsDelta":"{}"}}`),
	)
	mustViolation(t, cMismatch, envelope(5, "tool/call",
		`{"turn":1,"step":1,"callId":"call_x","name":"bash","arguments":"{\"other\":1}"}`))

	// tool/result: double-layer content, isError:false → completed.
	evs = mustEvents(t, c, envelope(7, "tool/result",
		`{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"call_00_ahVZeE0rrsgxPCpMWoqm1631"},"content":[{"type":"tool-result","toolCallId":"call_00_ahVZeE0rrsgxPCpMWoqm1631","content":[{"type":"text","text":"hi\n"}],"isError":false}],"role":"user","id":"efd7d2b3-df03-4c99-8cdb-74bf4477064c"},"sourceEventSeqs":[4],"surfaceOp":"append"}`))
	if len(evs) != 1 || evs[0].Type != core.EventToolResult {
		t.Fatalf("tool/result mapping: %+v", evs)
	}
	res := evs[0]
	if res.ToolResult != "hi\n" {
		t.Fatalf("tool result text = %q", res.ToolResult)
	}
	if res.ToolName != "bash" {
		t.Fatalf("tool name attribution from call: %q", res.ToolName)
	}
	if res.ToolStatus != "completed" || res.ToolSuccess == nil || !*res.ToolSuccess {
		t.Fatalf("isError=false must map to completed/success: %+v", res)
	}
	if res.RequestID != "call_00_ahVZeE0rrsgxPCpMWoqm1631" || res.ItemID != res.RequestID {
		t.Fatalf("tool result identity: %+v", res)
	}

	// isError:true → failed.
	evs = mustEvents(t, c, envelope(8, "tool/result",
		`{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"call_err"},"content":[{"type":"tool-result","toolCallId":"call_err","content":[{"type":"text","text":"boom"}],"isError":true}],"role":"user","id":"r2"}}`))
	if evs[0].ToolStatus != "failed" || evs[0].ToolSuccess == nil || *evs[0].ToolSuccess {
		t.Fatalf("isError=true must map to failed: %+v", evs[0])
	}
}

func TestTodoWriteMapping(t *testing.T) {
	c := newCodec(testNonce)
	// Control-plane: works without any active turn, carries no TurnID.
	evs := mustEvents(t, c, envelope(0, "todo/write",
		`{"todos":[{"content":"apple","status":"pending"},{"content":"banana","status":"pending"},{"content":"cherry","status":"in_progress"}]}`))
	if len(evs) != 1 || evs[0].Type != core.EventPlan {
		t.Fatalf("todo/write mapping: %+v", evs)
	}
	if len(evs[0].Plan) != 3 || evs[0].Plan[0].Content != "apple" || evs[0].Plan[2].Status != "in_progress" {
		t.Fatalf("todo payload: %+v", evs[0].Plan)
	}
	if evs[0].TurnID != "" {
		t.Fatalf("control-plane event must carry no TurnID, got %q", evs[0].TurnID)
	}
}

func TestControlPlaneExplicitIgnores(t *testing.T) {
	c := newCodec(testNonce)
	evs := mustEvents(t, c,
		envelope(0, "permission/preset", `{"preset":"workspace-write"}`),
		envelope(1, "sandbox/mode", `{"mode":"workspace-write"}`),
		envelope(2, "approval/policy", `{"policy":"ask"}`),
		envelope(3, "agent/inbox/spliced", `{"target":"next-turn","start":0,"inserted":[]}`),
		envelope(4, "session/title", `{"title":"Run the bash command: echo","messageSeqs":[7],"source":{"kind":"fallback"}}`),
		envelope(5, "request/header", `{"header":{"config":{"provider":"deepseek-official","model":"deepseek-chat","maxTokens":2048},"system":"You are an agent."}}`),
	)
	if len(evs) != 0 {
		t.Fatalf("class-② control-plane events must be explicitly ignored, got %+v", evs)
	}
}

func TestRequestContextCapturesWindow(t *testing.T) {
	c := newCodec(testNonce)
	mustEvents(t, c, envelope(0, "request/context",
		`{"provider":"deepseek-official","model":"deepseek-chat","contextWindow":1000000}`))
	if c.contextWindow != 1000000 {
		t.Fatalf("contextWindow = %d", c.contextWindow)
	}
}

func TestTurnEndTerminals(t *testing.T) {
	// completed with usage fold-in.
	c := newCodec(testNonce)
	openTurnStep1(t, c)
	mustEvents(t, c,
		envelope(3, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"usage","usage":{"inputTokens":222,"outputTokens":78,"cacheReadTokens":1280,"reasoningTokens":12}}}`),
		envelope(4, "assistant/chunk", `{"turn":1,"step":1,"chunk":{"type":"finish","reason":{"kind":"stop"}}}`),
		envelope(5, "assistant/message", `{"turn":1,"step":1,"message":{"role":"assistant","content":[],"id":"m1"},"usage":{"inputTokens":222,"outputTokens":78,"cacheReadTokens":1280,"reasoningTokens":12}}`),
		envelope(6, "step/end", `{"turn":1,"step":1}`),
	)
	evs := mustEvents(t, c, envelope(7, "turn/end", `{"turn":1,"reason":{"kind":"completed"}}`))
	if len(evs) != 1 || evs[0].Type != core.EventResult || !evs[0].Done {
		t.Fatalf("turn/end completed: %+v", evs)
	}
	if evs[0].Error != nil {
		t.Fatalf("completed must not carry error: %v", evs[0].Error)
	}
	if evs[0].InputTokens != 222 || evs[0].OutputTokens != 78 {
		t.Fatalf("terminal token totals: %+v", evs[0])
	}
	if evs[0].TurnID != "p"+testNonce+"-t1" {
		t.Fatalf("terminal TurnID = %q", evs[0].TurnID)
	}

	// max-tokens is an accepted completion (run2), not an error.
	c2 := newCodec(testNonce)
	openTurnStep1(t, c2)
	mustEvents(t, c2, envelope(3, "step/end", `{"turn":1,"step":1}`))
	evs = mustEvents(t, c2, envelope(4, "turn/end", `{"turn":1,"reason":{"kind":"max-tokens"}}`))
	if evs[0].Error != nil || !evs[0].Done {
		t.Fatalf("max-tokens terminal: %+v", evs[0])
	}

	// Other reason kinds (error/aborted/…) close the turn as a visible
	// failure — never fabricated success.
	c3 := newCodec(testNonce)
	openTurnStep1(t, c3)
	mustEvents(t, c3, envelope(3, "step/end", `{"turn":1,"step":1}`))
	evs = mustEvents(t, c3, envelope(4, "turn/end", `{"turn":1,"reason":{"kind":"error"}}`))
	if evs[0].Error == nil || !evs[0].Done {
		t.Fatalf("error-kind terminal must carry an error: %+v", evs[0])
	}
}

func TestActiveTurnStateMachineViolations(t *testing.T) {
	// Nested turn/start.
	c := newCodec(testNonce)
	openTurnStep1(t, c)
	mustEvents(t, c, envelope(3, "step/end", `{"turn":1,"step":1}`))
	mustViolation(t, c, envelope(4, "turn/start", `{"turn":2}`))

	// turn mismatch on assistant chunk.
	c2 := newCodec(testNonce)
	openTurnStep1(t, c2)
	mustViolation(t, c2, envelope(3, "assistant/chunk",
		`{"turn":2,"step":1,"chunk":{"type":"text-delta","index":0,"text":"x"}}`))

	// step mismatch on assistant chunk.
	c3 := newCodec(testNonce)
	openTurnStep1(t, c3)
	mustViolation(t, c3, envelope(3, "assistant/chunk",
		`{"turn":1,"step":2,"chunk":{"type":"text-delta","index":0,"text":"x"}}`))

	// chunk with no open step.
	c4 := newCodec(testNonce)
	mustEvents(t, c4, envelope(0, "turn/start", `{"turn":1}`))
	mustViolation(t, c4, envelope(1, "assistant/chunk",
		`{"turn":1,"step":1,"chunk":{"type":"text-delta","index":0,"text":"x"}}`))

	// nested step/start.
	c5 := newCodec(testNonce)
	openTurnStep1(t, c5)
	mustViolation(t, c5, envelope(3, "step/start", `{"turn":1,"step":2}`))

	// turn/end with open step.
	c6 := newCodec(testNonce)
	openTurnStep1(t, c6)
	mustViolation(t, c6, envelope(3, "turn/end", `{"turn":1,"reason":{"kind":"completed"}}`))

	// turn/end turn mismatch.
	c7 := newCodec(testNonce)
	openTurnStep1(t, c7)
	mustEvents(t, c7, envelope(3, "step/end", `{"turn":1,"step":1}`))
	mustViolation(t, c7, envelope(4, "turn/end", `{"turn":7,"reason":{"kind":"completed"}}`))

	// step/start outside any turn.
	c8 := newCodec(testNonce)
	mustViolation(t, c8, envelope(0, "step/start", `{"turn":1,"step":1}`))

	// user/message after terminal turn (no active turn) fails visibly.
	c9 := newCodec(testNonce)
	openTurnStep1(t, c9)
	mustEvents(t, c9, envelope(3, "step/end", `{"turn":1,"step":1}`))
	mustEvents(t, c9, envelope(4, "turn/end", `{"turn":1,"reason":{"kind":"completed"}}`))
	mustViolation(t, c9, envelope(5, "user/message",
		`{"content":[{"type":"text","text":"late"}],"source":{"kind":"user"},"role":"user","id":"late-1"}`))
}

func TestSecondTurnAfterFirstCompletes(t *testing.T) {
	c := newCodec(testNonce)
	openTurnStep1(t, c)
	mustEvents(t, c, envelope(3, "step/end", `{"turn":1,"step":1}`))
	mustEvents(t, c, envelope(4, "turn/end", `{"turn":1,"reason":{"kind":"completed"}}`))

	// Multi-step turn 2: all steps attribute to the same TurnID/Assistant.
	evs := mustEvents(t, c,
		envelope(5, "turn/start", `{"turn":2}`),
		envelope(6, "step/start", `{"turn":2,"step":1}`),
		envelope(7, "assistant/chunk", `{"turn":2,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`),
		envelope(8, "assistant/chunk", `{"turn":2,"step":1,"chunk":{"type":"text-delta","index":0,"text":"a"}}`),
		envelope(9, "assistant/chunk", `{"turn":2,"step":1,"chunk":{"type":"block-end","index":0,"block":{"type":"text","text":"a"}}}`),
		envelope(10, "step/end", `{"turn":2,"step":1}`),
		envelope(11, "step/start", `{"turn":2,"step":2}`),
		envelope(12, "assistant/chunk", `{"turn":2,"step":2,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`),
		envelope(13, "assistant/chunk", `{"turn":2,"step":2,"chunk":{"type":"text-delta","index":0,"text":"b"}}`),
		envelope(14, "assistant/chunk", `{"turn":2,"step":2,"chunk":{"type":"block-end","index":0,"block":{"type":"text","text":"b"}}}`),
		envelope(15, "step/end", `{"turn":2,"step":2}`),
		envelope(16, "turn/end", `{"turn":2,"reason":{"kind":"completed"}}`),
	)
	turn2 := "p" + testNonce + "-t2"
	var textEvents []core.Event
	for _, e := range evs {
		if e.Type == core.EventText {
			textEvents = append(textEvents, e)
		}
	}
	if len(textEvents) != 2 {
		t.Fatalf("want 2 text events across steps, got %d", len(textEvents))
	}
	for _, e := range textEvents {
		if e.TurnID != turn2 || e.ItemID != turn2 {
			t.Fatalf("step %d text identity: TurnID=%q ItemID=%q", 0, e.TurnID, e.ItemID)
		}
	}
}

func TestUnknownRequiredTypeFailsIgnorableTrueSkips(t *testing.T) {
	// Unknown REQUIRED type → fail visibly.
	c := newCodec(testNonce)
	err := mustViolation(t, c, envelope(0, "compaction/start", `{"reason":"size"}`))
	if !strings.Contains(err.Error(), "compaction/start") {
		t.Fatalf("violation should name the type: %v", err)
	}

	// ignorable:true (the ONLY safe-skip marker) on an unknown type → skip.
	c2 := newCodec(testNonce)
	evs := mustEvents(t, c2, envelopeFull(0, "some/future-event", `{"x":1}`, `true`))
	if len(evs) != 0 {
		t.Fatalf("ignorable:true must skip, got %+v", evs)
	}

	// ignorable:false is REQUIRED (not a skip).
	c3 := newCodec(testNonce)
	mustViolation(t, c3, envelopeFull(0, "some/future-event", `{"x":1}`, `false`))

	// Non-boolean ignorable is REQUIRED (not a skip).
	c4 := newCodec(testNonce)
	mustViolation(t, c4, envelopeFull(0, "some/future-event", `{"x":1}`, `"true"`))
}

func TestSchemaViolationsFail(t *testing.T) {
	// Missing data payload.
	c := newCodec(testNonce)
	env := envelope(0, "turn/start", "")
	env.Data = nil
	mustViolation(t, c, env)

	// Malformed data JSON.
	c2 := newCodec(testNonce)
	env2 := envelope(0, "turn/start", `{"turn":`)
	mustViolation(t, c2, env2)

	// Wrong-typed field.
	c3 := newCodec(testNonce)
	mustViolation(t, c3, envelope(0, "turn/start", `{"turn":"one"}`))

	// tool/call missing callId.
	c4 := newCodec(testNonce)
	openTurnStep1(t, c4)
	mustViolation(t, c4, envelope(3, "tool/call", `{"turn":1,"step":1,"name":"bash","arguments":"{}"}`))
}

// TestRun3FrozenSequenceReplay replays a compacted but order- and shape-faithful
// slice of run3-sandbox.jsonl (permission stack + user/plugin double shape +
// two-step turn with reasoning/tool/text + usage + terminals).
func TestRun3FrozenSequenceReplay(t *testing.T) {
	c := newCodec(testNonce)
	lines := []string{
		`{"preset":"workspace-write"}`,                   // permission/preset
		`{"mode":"workspace-write"}`,                     // sandbox/mode
		`{"policy":"ask"}`,                               // approval/policy
		`{"target":"next-turn","start":0,"inserted":[]}`, // agent/inbox/spliced
		`{"turn":1}`,                                     // turn/start
		`{"target":"next-turn","start":0,"removedCount":1,"inserted":[]}`, // agent/inbox/spliced
		`{"turn":1,"step":1}`, // step/start
		`{"content":[{"type":"text","text":"Run the bash command: echo hi"}],"source":{"kind":"user"},"role":"user","id":"cc875db3-bfb2-447b-8abb-5de10a6fc2f6"}`, // user/message user
		`{"content":[{"type":"text","text":"Current runtime context."}],"source":{"kind":"plugin"},"role":"user","id":"8d0d1b1e-..."}`,                            // user/message plugin
		`{"title":"Run the bash command: echo","messageSeqs":[7],"source":{"kind":"fallback"}}`,                                                                   // session/title
		`{"header":{"config":{"provider":"deepseek-official","model":"deepseek-chat","maxTokens":2048}}}`,                                                         // request/header
		`{"provider":"deepseek-official","model":"deepseek-chat","contextWindow":1000000}`,                                                                        // request/context
		`{"turn":1,"step":1,"chunk":{"type":"block-start","index":0,"blockType":"reasoning"}}`,
		`{"turn":1,"step":1,"chunk":{"type":"reasoning-delta","index":0,"text":"The user wants me to run echo hi. Simple."}}`,
		`{"turn":1,"step":1,"chunk":{"type":"block-start","index":1,"blockType":"tool-call"}}`,
		`{"turn":1,"step":1,"chunk":{"type":"tool-call-delta","index":1,"id":"call_00_ahVZeE0rrsgxPCpMWoqm1631","name":"bash","argumentsDelta":"{\"command\": \"echo hi\"}"}}`,
		`{"turn":1,"step":1,"chunk":{"type":"block-end","index":0,"block":{"type":"reasoning","text":"The user wants me to run echo hi. Simple."}}}`,
		`{"turn":1,"step":1,"chunk":{"type":"block-end","index":1,"block":{"type":"tool-call","id":"call_00_ahVZeE0rrsgxPCpMWoqm1631","name":"bash","arguments":"{\"command\": \"echo hi\"}"}}}`,
		`{"turn":1,"step":1,"chunk":{"type":"usage","usage":{"inputTokens":222,"outputTokens":78,"cacheReadTokens":1280,"reasoningTokens":12}}}`,
		`{"turn":1,"step":1,"chunk":{"type":"finish","reason":{"kind":"tool-calls"}}}`,
		`{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"reasoning","text":"The user wants me to run echo hi. Simple."},{"type":"tool-call","id":"call_00_ahVZeE0rrsgxPCpMWoqm1631","name":"bash","arguments":"{\"command\": \"echo hi\"}"}],"id":"2fad11bb"},"usage":{"inputTokens":222,"outputTokens":78,"cacheReadTokens":1280,"reasoningTokens":12}}`, // assistant/message
		`{"turn":1,"step":1,"callId":"call_00_ahVZeE0rrsgxPCpMWoqm1631","name":"bash","arguments":"{\"command\": \"echo hi\"}"}`,                                                                                                                                                          // tool/call
		`{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"call_00_ahVZeE0rrsgxPCpMWoqm1631"},"content":[{"type":"tool-result","toolCallId":"call_00_ahVZeE0rrsgxPCpMWoqm1631","content":[{"type":"text","text":"hi\n"}],"isError":false}],"role":"user","id":"efd7d2b3"}}`, // tool/result
		`{"turn":1,"step":1}`, // step/end
		`{"turn":1,"step":2}`, // step/start
		`{"turn":1,"step":2,"chunk":{"type":"block-start","index":0,"blockType":"text"}}`,
		`{"turn":1,"step":2,"chunk":{"type":"text-delta","index":0,"text":"Done — the command output hi."}}`,
		`{"turn":1,"step":2,"chunk":{"type":"block-end","index":0,"block":{"type":"text","text":"Done — the command output hi."}}}`,
		`{"turn":1,"step":2,"chunk":{"type":"usage","usage":{"inputTokens":58,"outputTokens":9,"cacheReadTokens":1536,"reasoningTokens":0}}}`,
		`{"turn":1,"step":2,"chunk":{"type":"finish","reason":{"kind":"stop"}}}`,
		`{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"text","text":"Done — the command output hi."}],"id":"245a9ef1"},"usage":{"inputTokens":58,"outputTokens":9,"cacheReadTokens":1536,"reasoningTokens":0}}`,
		`{"turn":1,"step":2}`,                      // step/end
		`{"turn":1,"reason":{"kind":"completed"}}`, // turn/end
	}
	types := []string{
		"permission/preset", "sandbox/mode", "approval/policy", "agent/inbox/spliced",
		"turn/start", "agent/inbox/spliced", "step/start", "user/message", "user/message",
		"session/title", "request/header", "request/context",
		"assistant/chunk", "assistant/chunk", "assistant/chunk", "assistant/chunk",
		"assistant/chunk", "assistant/chunk", "assistant/chunk", "assistant/chunk",
		"assistant/message", "tool/call", "tool/result",
		"step/end", "step/start",
		"assistant/chunk", "assistant/chunk", "assistant/chunk", "assistant/chunk",
		"assistant/chunk", "assistant/message",
		"step/end", "turn/end",
	}
	if len(lines) != len(types) {
		t.Fatalf("fixture table mismatch: %d lines vs %d types", len(lines), len(types))
	}

	turnID := "p" + testNonce + "-t1"
	var got []core.Event
	for i, data := range lines {
		evs, err := c.apply(envelope(i, types[i], data))
		if err != nil {
			t.Fatalf("seq %d (%s) unexpected violation: %v", i, types[i], err)
		}
		got = append(got, evs...)
	}

	// Expected core event sequence (everything else is internal/ignored):
	want := []struct {
		typ   core.EventType
		turn  string
		item  string
		other string
	}{
		{core.EventTurnStarted, turnID, "", ""},
		{core.EventUserMessage, turnID, "cc875db3-bfb2-447b-8abb-5de10a6fc2f6", "Run the bash command: echo hi"},
		{core.EventThinking, turnID, turnID, "The user wants me to run echo hi. Simple."},
		{core.EventContextUsageUpdated, "", "", ""}, // step-1 usage: Used=222+1280
		{core.EventToolUse, turnID, "call_00_ahVZeE0rrsgxPCpMWoqm1631", "bash"},
		{core.EventToolResult, turnID, "call_00_ahVZeE0rrsgxPCpMWoqm1631", "hi\n"},
		{core.EventText, turnID, turnID, "Done — the command output hi."},
		{core.EventContextUsageUpdated, "", "", ""}, // step-2 usage: Used=58+1536
		{core.EventResult, turnID, "", ""},
	}
	if len(got) != len(want) {
		for i, e := range got {
			t.Logf("got[%d] = %s turn=%s item=%s", i, e.Type, e.TurnID, e.ItemID)
		}
		t.Fatalf("event count = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		e := got[i]
		if e.Type != w.typ {
			t.Fatalf("event[%d] type = %s, want %s", i, e.Type, w.typ)
		}
		if e.TurnID != w.turn {
			t.Fatalf("event[%d] (%s) TurnID = %q, want %q", i, e.Type, e.TurnID, w.turn)
		}
		if w.item != "" && e.ItemID != w.item {
			t.Fatalf("event[%d] (%s) ItemID = %q, want %q", i, e.Type, e.ItemID, w.item)
		}
		switch e.Type {
		case core.EventContextUsageUpdated:
			// §3.7 formula per snapshot; contextWindow from request/context.
			if e.ContextUsage == nil {
				t.Fatalf("context usage event without payload")
			}
			switch i {
			case 3:
				if e.ContextUsage.UsedTokens != 222+1280 || e.ContextUsage.ContextWindow != 1000000 {
					t.Fatalf("step-1 usage: %+v", e.ContextUsage)
				}
			case 7:
				if e.ContextUsage.UsedTokens != 58+1536 || e.ContextUsage.TotalTokens != e.ContextUsage.UsedTokens {
					t.Fatalf("step-2 usage: %+v", e.ContextUsage)
				}
			}
		case core.EventUserMessage, core.EventThinking, core.EventText, core.EventToolResult:
			var content string
			if e.Type == core.EventToolResult {
				content = e.ToolResult
			} else {
				content = e.Content
			}
			if content != w.other {
				t.Fatalf("event[%d] (%s) content = %q, want %q", i, e.Type, content, w.other)
			}
		case core.EventToolUse:
			if e.ToolName != w.other {
				t.Fatalf("event[%d] toolName = %q, want %q", i, e.ToolName, w.other)
			}
		case core.EventResult:
			// Last usage snapshot in the turn (step 2): 58/9.
			if e.InputTokens != 58 || e.OutputTokens != 9 {
				t.Fatalf("terminal tokens = %d/%d, want 58/9", e.InputTokens, e.OutputTokens)
			}
			if e.Error != nil || !e.Done {
				t.Fatalf("terminal must be Done without error: %+v", e)
			}
		}
	}

	// Codec fully reset after turn/end.
	if c.activeTurn != noTurn || c.activeStep != noStep || c.activeTurnID != "" {
		t.Fatalf("codec not reset: turn=%d step=%d id=%q", c.activeTurn, c.activeStep, c.activeTurnID)
	}
}
