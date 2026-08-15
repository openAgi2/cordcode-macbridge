package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 这些测试覆盖 chatgpt-style-tool-activity-display 方案 Phase 1C L-α 的 rich-history builder
// 路径（cordcode-ios docs/2026-08-01 §8 1C#8 / §6.2.2 L-α）。
//
// richHistoryMessageBuilder.addToolUse 此前硬编码 title: toolName 且不保留 input（claudecode.go:880-903），
// 导致 rich-history 冷启动回放路径（不同于 go-bridge 的 relay-transcript 路径，后者由
// claudeEntryToProjectionEvents 处理）丢失 file path。L-α 修复：扩签名传入 input json.RawMessage，
// 用 summarizeInput 派生 path-bearing title（Edit/Write/Read → file_path），并保留 toolInput。

func TestRichHistoryBuilder_AddToolUse_PreservesPathBearingTitle(t *testing.T) {
	b := newRichHistoryMessageBuilder("msg-1", "assistant", time.Time{})
	input := json.RawMessage(`{"file_path":"src/views/Foo.swift","old_string":"a","new_string":"b"}`)
	id := b.addToolUse("tool-edit-1", "Edit", input)

	step, ok := b.Steps[id]
	if !ok {
		t.Fatalf("step not registered for id %q", id)
	}
	// L-α: title 派生自 input.file_path（path-bearing），而非硬编码 toolName
	if title, _ := step["title"].(string); title != "src/views/Foo.swift" {
		t.Errorf("title = %q, want src/views/Foo.swift (L-α: from input.file_path)", title)
	}
	// toolName 保持
	if tn, _ := step["toolName"].(string); tn != "Edit" {
		t.Errorf("toolName = %q, want Edit", tn)
	}
	// toolInput 保留（字符串形式）
	if ti, _ := step["toolInput"].(string); !strings.Contains(ti, "file_path") {
		t.Errorf("toolInput = %q, want JSON containing file_path (L-α preserved)", ti)
	}
	// input 结构化保留
	if _, ok := step["input"]; !ok {
		t.Error("step should retain structured input map (L-α)")
	}
}

func TestRichHistoryBuilder_AddToolUse_FallsBackToToolNameWhenNoInput(t *testing.T) {
	b := newRichHistoryMessageBuilder("msg-2", "assistant", time.Time{})
	// 无 input（空 RawMessage）→ title 回退为 toolName（不崩溃、不伪造 path）
	id := b.addToolUse("tool-bash-1", "Bash", nil)
	step := b.Steps[id]
	if title, _ := step["title"].(string); title != "Bash" {
		t.Errorf("title = %q, want Bash (fallback when no input)", title)
	}
	if _, hasToolInput := step["toolInput"]; hasToolInput {
		t.Error("should not set toolInput when input is empty")
	}
}

func TestRichHistoryBuilder_AddToolUse_BashDerivesCommandTitle(t *testing.T) {
	b := newRichHistoryMessageBuilder("msg-3", "assistant", time.Time{})
	input := json.RawMessage(`{"command":"git status --short"}`)
	id := b.addToolUse("tool-bash-2", "Bash", input)
	step := b.Steps[id]
	if title, _ := step["title"].(string); title != "git status --short" {
		t.Errorf("title = %q, want 'git status --short' (Bash command, L-α)", title)
	}
}
