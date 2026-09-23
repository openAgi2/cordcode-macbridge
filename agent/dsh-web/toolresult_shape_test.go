package dshweb

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// 2026-09-23 S3 回归（scripts/dshweb-phase0/s3-inbox-live-regression.json）发现
// 的形状漂移：官方已退役 "tool-result" 内容块标签（deepseek-harness
// agent-team/src/projection.ts:43 "retired tool-result tags cannot enter …"）；
// alpha.1/alpha.2 journal 的 tool/result 是顶层 text 块 + message 级
// toolCallId/isError（llm/src/message.ts ToolResultMessage、session/src/
// repair.ts 构造、journal seq22/27/32 实测一致）。旧解析按退役标签扫描 →
// 每条 tool/result 都 reset 码器（真机日志 19:05–19:07：15 次 reset、16 个
// 重复 turn_started、claim splice 的 removal 事件丢失）。

func TestCodecToolResultOfficialShapeNoReset(t *testing.T) {
	c := newSessionCodec("sess-shape")
	events := collect(t, c, []sessionEventWire{
		env("turn/start", 0, map[string]any{"turn": 1}),
		env("step/start", 1, map[string]any{"turn": 1, "step": 1}),
		env("tool/call", 2, map[string]any{"turn": 1, "step": 1, "callId": "call_86fc", "name": "edit", "arguments": `{}`}),
		// journal seq22 实测形状（脱敏）：message 级 isError、顶层 text 块、无嵌套。
		env("tool/result", 3, map[string]any{"turn": 1, "step": 1, "message": map[string]any{
			"toolCallId": "call_86fc",
			"isError":    true,
			"source":     map[string]any{"kind": "tool", "callId": "call_86fc"},
			"content":    []map[string]any{{"type": "text", "text": "Error: cannot modify file: file has not been read"}},
		}}),
		env("step/end", 4, map[string]any{"turn": 1, "step": 1}),
		env("turn/end", 5, map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}),
	})
	var toolResult *core.Event
	for i := range events {
		if events[i].Type == core.EventToolResult {
			toolResult = &events[i]
		}
	}
	if toolResult == nil {
		t.Fatal("official-shape tool/result must map without codec reset")
	}
	if toolResult.ToolResult == "" {
		t.Fatalf("result text must come from top-level text blocks, got %q", toolResult.ToolResult)
	}
	if toolResult.ToolStatus != "failed" || toolResult.ToolSuccess == nil || *toolResult.ToolSuccess {
		t.Fatalf("message-level isError must map to failed, got status=%q", toolResult.ToolStatus)
	}
	if toolResult.RequestID != "call_86fc" || toolResult.ItemID != "call_86fc" {
		t.Fatalf("callId identity wrong: requestID=%q", toolResult.RequestID)
	}
}

func TestCodecRegistryMembersSkipWithoutReset(t *testing.T) {
	// known-event-types.ts 全表成员逐项核对后补齐的两个漏列成员。
	// workspace/changes 在真机 journal seq92 触发过 "unknown required event
	// type" reset（payload {turn}，workspace-changes deliverable 的按回合
	// 通告，摘要走独立 API）；developer/message 是工具增删的审计行。
	c := newSessionCodec("sess-registry")
	events := collect(t, c, []sessionEventWire{
		env("turn/start", 0, map[string]any{"turn": 1}),
		env("workspace/changes", 1, map[string]any{"turn": 1}),
		env("developer/message", 2, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "tool added"}},
			"source":  map[string]any{"kind": "user"},
		}),
		env("turn/end", 3, map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}),
	})
	var sawTerminal bool
	for _, e := range events {
		if e.Type == core.EventResult {
			sawTerminal = true
		}
	}
	if !sawTerminal {
		t.Fatal("registry members must skip as control-plane, not reset the codec")
	}
}

func TestHistoryToolResultOfficialShapeText(t *testing.T) {
	// 冷历史 pass 1 同一形状：顶层 text 块进 tool 输出，isError 映射失败态。
	evs := []sessionEventWire{
		mkHistoryEntry("turn/start", 100, `{"turn": 1}`),
		mkHistoryEntry("assistant/message", 101, `{
			"turn": 1, "step": 1,
			"message": {"role": "assistant", "content": [
				{"type": "tool-call", "id": "call_5b91", "name": "bash", "arguments": "{\"command\":\"ls\"}"}
			]}
		}`),
		mkHistoryEntry("tool/result", 102, `{
			"turn": 1, "step": 1,
			"message": {"toolCallId": "call_5b91", "source": {"kind": "tool", "callId": "call_5b91"},
			 "content": [{"type": "text", "text": "total 8"}, {"type": "text", "text": "drwxr-xr-x"}]}
		}`),
		mkHistoryEntry("turn/end", 200, `{"turn": 1, "reason": {"kind": "completed"}}`),
	}
	entries := mapHistoryEvents("sess-hist", evs)
	found := false
	for _, entry := range entries {
		for _, part := range entry.Parts {
			if part["type"] != "tool" {
				continue
			}
			step, _ := part["step"].(map[string]any)
			output, _ := step["output"].(map[string]any)
			if output["text"] != "total 8\ndrwxr-xr-x" {
				t.Fatalf("tool output must join top-level text blocks, got %q", output["text"])
			}
			if step["status"] != "completed" {
				t.Fatalf("message-level isError=false must map to completed, got %q", step["status"])
			}
			found = true
		}
	}
	if !found {
		t.Fatal("history tool part expected")
	}
}
