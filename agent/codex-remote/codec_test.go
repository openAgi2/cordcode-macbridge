package codexremote

import (
	"encoding/json"
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
