package codexremote

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestRemoteCodecDecodesTurnItemsAndTerminalStates(t *testing.T) {
	codec := NewLiveCodec()
	started := codec.Decode(Notification{Method: "turn/started", Params: json.RawMessage(`{"threadId":"th","turn":{"id":"turn"}}`)})
	if len(started) != 1 || started[0].Type != core.EventTurnStarted || codec.ActiveTurn("th") != "turn" {
		t.Fatalf("started=%+v active=%q", started, codec.ActiveTurn("th"))
	}
	tool := codec.Decode(Notification{Method: "item/started", Params: json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"commandExecution","id":"cmd","command":"echo ok","cwd":"/tmp","status":"inProgress"}}`)})
	if len(tool) != 1 || tool[0].Type != core.EventToolUse || tool[0].ToolName != "Bash" || tool[0].ItemID != "cmd" {
		t.Fatalf("tool=%+v", tool)
	}
	result := codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"commandExecution","id":"cmd","status":"completed","aggregatedOutput":"ok\n","exitCode":0}}`)})
	if len(result) != 1 || result[0].Type != core.EventToolResult || result[0].ToolResult != "ok\n" || result[0].ToolSuccess == nil || !*result[0].ToolSuccess {
		t.Fatalf("result=%+v", result)
	}
	completed := codec.Decode(Notification{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"th","turn":{"id":"turn","status":"completed"}}`)})
	if len(completed) != 1 || completed[0].Type != core.EventResult || !completed[0].Done || codec.ActiveTurn("th") != "" {
		t.Fatalf("completed=%+v active=%q", completed, codec.ActiveTurn("th"))
	}
}

func TestRemoteCodecMapsReasoningPlanUsageAndRetry(t *testing.T) {
	codec := NewLiveCodec()
	retry := codec.Decode(Notification{Method: "error", Params: json.RawMessage(`{"threadId":"th","turnId":"turn","willRetry":true,"error":{"message":"temporary"}}`)})
	if len(retry) != 1 || retry[0].Type != core.EventRetryStatus || retry[0].RetryAttempt != 1 {
		t.Fatalf("retry=%+v", retry)
	}
	reasoning := codec.Decode(Notification{Method: "item/reasoning/summaryTextDelta", Params: json.RawMessage(`{"threadId":"th","turnId":"turn","itemId":"r","delta":"thinking"}`)})
	if len(reasoning) != 1 || reasoning[0].Type != core.EventThinking || reasoning[0].Content != "thinking" {
		t.Fatalf("reasoning=%+v", reasoning)
	}
	plan := codec.Decode(Notification{Method: "turn/plan/updated", Params: json.RawMessage(`{"threadId":"th","turnId":"turn","plan":[{"step":"one","status":"inProgress"},{"step":"two","status":"completed"}]}`)})
	if len(plan) != 1 || plan[0].Type != core.EventPlan || len(plan[0].Plan) != 2 || plan[0].Plan[0].Status != "in_progress" {
		t.Fatalf("plan=%+v", plan)
	}
	usage := codec.Decode(Notification{Method: "thread/tokenUsage/updated", Params: json.RawMessage(`{"threadId":"th","tokenUsage":{"last":{"totalTokens":7,"inputTokens":4,"cachedInputTokens":1,"outputTokens":3,"reasoningOutputTokens":2},"total":{"totalTokens":10},"modelContextWindow":100}}`)})
	if len(usage) != 1 || usage[0].Type != core.EventContextUsageUpdated || usage[0].ContextUsage == nil || usage[0].ContextUsage.UsedTokens != 7 || usage[0].ContextUsage.ContextWindow != 100 {
		t.Fatalf("usage=%+v", usage)
	}
	if codec.UnknownMethods()["item/reasoning/summaryTextDelta"] != 0 {
		t.Fatal("known reasoning notification must not be counted unknown")
	}
}

func TestRemoteCodecDropsMalformedCompletionAndCountsOnlyUnknown(t *testing.T) {
	codec := NewLiveCodec()
	codec.Decode(Notification{Method: "turn/started", Params: json.RawMessage(`{"threadId":"th","turn":{"id":"turn"}}`)})
	if events := codec.Decode(Notification{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"th","turn":{"status":"completed"}}`)}); len(events) != 0 {
		t.Fatalf("missing official turn id must drop: %+v", events)
	}
	codec.Decode(Notification{Method: "future/notification", Params: json.RawMessage(`{}`)})
	if codec.UnknownMethods()["future/notification"] != 1 {
		t.Fatalf("unknown=%v", codec.UnknownMethods())
	}
	if codec.UnknownMethods()["thread/status/changed"] != 0 {
		t.Fatal("known no-op status notification must not be unknown")
	}
}

// 官方 turn/completed 通知携带 turn.durationMs（fixture: codex-web reconnect dump
// turn/completed durationMs:22）——decodeTurnCompleted 必须保留到 core.Event。
func TestDecodeTurnCompletedCarriesDurationMs(t *testing.T) {
	codec := NewLiveCodec()
	events := codec.Decode(Notification{Method: "turn/completed", Params: json.RawMessage(
		`{"threadId":"th","turn":{"id":"turn","status":"completed","durationMs":86000}}`)})
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].DurationMs != 86_000 {
		t.Fatalf("DurationMs = %d, want 86000", events[0].DurationMs)
	}
	// 缺席保持 0（官方 "if known"）。
	events = codec.Decode(Notification{Method: "turn/completed", Params: json.RawMessage(
		`{"threadId":"th","turn":{"id":"turn2","status":"completed"}}`)})
	if len(events) != 1 || events[0].DurationMs != 0 {
		t.Fatalf("absent durationMs must decode 0: %+v", events)
	}
}

func TestRemoteCodecEmitsPlanReviewAfterOfficialPlanItem(t *testing.T) {
	codec := NewLiveCodec()
	codec.Decode(Notification{Method: "turn/started", Params: json.RawMessage(`{"threadId":"th","turn":{"id":"turn-1"}}`)})
	if events := codec.Decode(Notification{Method: "item/plan/delta", Params: json.RawMessage(
		`{"threadId":"th","turnId":"turn-1","itemId":"turn-1-plan","delta":"# Draft\n"}`)}); len(events) != 0 {
		t.Fatalf("plan delta must not emit timeline events: %+v", events)
	}
	if codec.UnknownMethods()["item/plan/delta"] != 0 {
		t.Fatal("item/plan/delta is official and must not count as unknown")
	}
	if events := codec.Decode(Notification{Method: "item/started", Params: json.RawMessage(
		`{"threadId":"th","turnId":"turn-1","item":{"type":"plan","id":"turn-1-plan","text":""}}`)}); len(events) != 0 {
		t.Fatalf("plan item start is not a tool: %+v", events)
	}
	if events := codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(
		`{"threadId":"th","turnId":"turn-1","item":{"type":"plan","id":"turn-1-plan","text":"# Final plan\n- first\n- second\n"}}`)}); len(events) != 0 {
		t.Fatalf("completed plan waits for turn/completed: %+v", events)
	}
	completed := codec.Decode(Notification{Method: "turn/completed", Params: json.RawMessage(
		`{"threadId":"th","turn":{"id":"turn-1","status":"completed"}}`)})
	if len(completed) != 2 {
		t.Fatalf("events = %d, want plan_review + turn result: %+v", len(completed), completed)
	}
	review := completed[0]
	if review.Type != core.EventPermissionRequest || review.PermissionKind != "plan_review" || review.RequestID != "turn-1-plan" {
		t.Fatalf("review = %+v", review)
	}
	if review.PlanReview == nil || review.PlanReview.Content != "# Final plan\n- first\n- second\n" || review.PlanReview.Title != "Final plan" {
		t.Fatalf("plan payload = %+v", review.PlanReview)
	}
	if got := strings.Join(review.PermissionActions, ","); got != "approve,requestChanges,quit" {
		t.Fatalf("actions = %q", got)
	}
	if completed[1].Type != core.EventResult || !completed[1].Done {
		t.Fatalf("turn result = %+v", completed[1])
	}
}

func TestRemoteCodecNoPlanReviewWithoutPlanItem(t *testing.T) {
	codec := NewLiveCodec()
	codec.Decode(Notification{Method: "turn/started", Params: json.RawMessage(`{"threadId":"th","turn":{"id":"turn-1"}}`)})
	completed := codec.Decode(Notification{Method: "turn/completed", Params: json.RawMessage(
		`{"threadId":"th","turn":{"id":"turn-1","status":"completed"}}`)})
	if len(completed) != 1 || completed[0].Type != core.EventResult {
		t.Fatalf("no plan item → no review card: %+v", completed)
	}
}

func TestRemoteCodecFailedTurnDoesNotEmitPlanReview(t *testing.T) {
	codec := NewLiveCodec()
	codec.Decode(Notification{Method: "turn/started", Params: json.RawMessage(`{"threadId":"th","turn":{"id":"turn-1"}}`)})
	codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(
		`{"threadId":"th","turnId":"turn-1","item":{"type":"plan","id":"turn-1-plan","text":"# Plan\n"}}`)})
	completed := codec.Decode(Notification{Method: "turn/completed", Params: json.RawMessage(
		`{"threadId":"th","turn":{"id":"turn-1","status":"failed","error":{"message":"boom"}}}`)})
	if len(completed) != 1 || completed[0].Type != core.EventError {
		t.Fatalf("failed turn = %+v", completed)
	}
}

func TestRemoteCodecNewTurnCancelsPendingPlanReview(t *testing.T) {
	codec := NewLiveCodec()
	codec.Decode(Notification{Method: "turn/started", Params: json.RawMessage(`{"threadId":"th","turn":{"id":"turn-1"}}`)})
	codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(
		`{"threadId":"th","turnId":"turn-1","item":{"type":"plan","id":"turn-1-plan","text":"# Plan\n"}}`)})
	codec.Decode(Notification{Method: "turn/completed", Params: json.RawMessage(
		`{"threadId":"th","turn":{"id":"turn-1","status":"completed"}}`)})
	next := codec.Decode(Notification{Method: "turn/started", Params: json.RawMessage(`{"threadId":"th","turn":{"id":"turn-2"}}`)})
	if len(next) != 2 || next[0].Type != core.EventPermissionResolved || next[0].RequestID != "turn-1-plan" {
		t.Fatalf("new turn must close the implement prompt: %+v", next)
	}
	if next[1].Type != core.EventTurnStarted {
		t.Fatalf("started = %+v", next[1])
	}
}

func TestRemoteCodecEmptyPlanTextDoesNotEmitReview(t *testing.T) {
	codec := NewLiveCodec()
	codec.Decode(Notification{Method: "turn/started", Params: json.RawMessage(`{"threadId":"th","turn":{"id":"turn-1"}}`)})
	codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(
		`{"threadId":"th","turnId":"turn-1","item":{"type":"plan","id":"turn-1-plan","text":"  \n"}}`)})
	completed := codec.Decode(Notification{Method: "turn/completed", Params: json.RawMessage(
		`{"threadId":"th","turn":{"id":"turn-1","status":"completed"}}`)})
	if len(completed) != 1 || completed[0].Type != core.EventResult {
		t.Fatalf("whitespace plan must not synthesize a review card: %+v", completed)
	}
}
