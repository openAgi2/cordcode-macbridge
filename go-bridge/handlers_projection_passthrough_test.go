package gobridge

import (
	"testing"
)

// 这些测试覆盖 chatgpt-style-tool-activity-display 方案 Phase 1A 的 macbridge hydration
// 透传（cordcode-ios docs/2026-08-01 §8 1A / §6.2.1 C-P0a / §6.2.2 L-P0a）。
//
// handlers_projection.go 的 cold-start hydration 原本只 copy itemId/toolName/toolStatus/toolResult，
// 丢弃了 Codex structured fileChanges 与 Claude path-bearing title/toolInput，导致 iOS 冷启动
// 无文件 path（R2/R5）。本测试验证 hydrateToolEventsFromStep 现在透传这些字段（存在时），
// 且不改变原有 4 字段的行为、不在字段缺失时伪造。

func TestHydrateToolEventsFromStep_PreservesCoreFields(t *testing.T) {
	events := hydrateToolEventsFromStep(map[string]any{
		"id":       "tool-1",
		"toolName": "Bash",
		"status":   "completed",
		"output":   "ok",
	})
	if len(events) != 2 || events[0].Event != "tool_started" || events[1].Event != "tool_finished" {
		t.Fatalf("expected tool_started + tool_finished, got %+v", events)
	}
	// tool_started 基础字段
	if events[0].Data["itemId"] != "tool-1" || events[0].Data["toolName"] != "Bash" {
		t.Errorf("tool_started core = %+v", events[0].Data)
	}
	// tool_finished 基础字段 + toolResult
	if events[1].Data["itemId"] != "tool-1" || events[1].Data["toolName"] != "Bash" {
		t.Errorf("tool_finished core = %+v", events[1].Data)
	}
	if events[1].Data["toolStatus"] != "completed" || events[1].Data["toolResult"] != "ok" {
		t.Errorf("tool_finished status/result = %+v", events[1].Data)
	}
	// 不存在的可选字段不应出现（不伪造）
	for _, key := range []string{"title", "toolInput", "fileChanges"} {
		if _, ok := events[1].Data[key]; ok {
			t.Errorf("tool_finished should not contain %q when step lacks it (no fabrication)", key)
		}
	}
}

func TestHydrateToolEventsFromStep_PassesThroughCodexFileChanges(t *testing.T) {
	// Codex structured fileChange（list 变体）应透传到 tool_started 与 tool_finished
	changes := []any{
		map[string]any{"path": "src/a.swift", "kind": "edit", "diff": "+x\n-y"},
	}
	events := hydrateToolEventsFromStep(map[string]any{
		"id":          "tool-fc",
		"toolName":    "Patch",
		"status":      "completed",
		"fileChanges": changes,
	})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	for i, ev := range events {
		fc, ok := ev.Data["fileChanges"]
		if !ok {
			t.Errorf("event[%d] (%s) missing fileChanges passthrough", i, ev.Event)
			continue
		}
		if fc == nil {
			t.Errorf("event[%d] (%s) fileChanges is nil", i, ev.Event)
		}
	}
}

func TestHydrateToolEventsFromStep_PassesThroughClaudeTitleAndToolInput(t *testing.T) {
	// Claude：title（live = file_path；L-α 后冷启动也为 file_path）+ toolInput 应透传
	events := hydrateToolEventsFromStep(map[string]any{
		"id":        "tool-edit",
		"toolName":  "Edit",
		"status":    "completed",
		"title":     "src/views/Foo.swift",
		"toolInput": `{"file_path":"src/views/Foo.swift"}`,
	})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	for i, ev := range events {
		if title, _ := ev.Data["title"].(string); title != "src/views/Foo.swift" {
			t.Errorf("event[%d] (%s) title = %q, want src/views/Foo.swift", i, ev.Event, title)
		}
		if ti, _ := ev.Data["toolInput"].(string); ti != `{"file_path":"src/views/Foo.swift"}` {
			t.Errorf("event[%d] (%s) toolInput = %q, want file_path json", i, ev.Event, ti)
		}
	}
}

func TestHydrateToolEventsFromStep_EmptyFieldsNotForwarded(t *testing.T) {
	// 空串 / <nil> 占位不应被当作有效值透传（避免 iOS 把 "" 当 path）
	events := hydrateToolEventsFromStep(map[string]any{
		"id":        "tool-empty",
		"toolName":  "Edit",
		"status":    "completed",
		"title":     "",
		"toolInput": "<nil>",
	})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	for i, ev := range events {
		for _, key := range []string{"title", "toolInput"} {
			if _, ok := ev.Data[key]; ok {
				t.Errorf("event[%d] (%s) should not forward empty/<nil> %q", i, ev.Event, key)
			}
		}
	}
}

func TestHydrateToolEventsFromStep_SkipsMissingID(t *testing.T) {
	// 无 id 的 step 不应发出事件（保持原有 continue 行为）
	events := hydrateToolEventsFromStep(map[string]any{
		"toolName": "Bash",
		"status":   "completed",
	})
	if len(events) != 0 {
		t.Errorf("step without id should emit nothing, got %d events", len(events))
	}
}
