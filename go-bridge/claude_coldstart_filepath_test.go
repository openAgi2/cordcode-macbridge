package gobridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这些测试覆盖 chatgpt-style-tool-activity-display 方案的 Claude 冷启动样本硬门
// （cordcode-ios docs/2026-08-01 §6.5 / §6.2.2 / 1C / 1D，R3）。
//
// 样本 cold-start-edit-filepath.jsonl 是一份脱敏的 observed-derived-real-shape transcript：
// assistant 含 name=Edit 的 tool_use，且 input.file_path 存在；匹配 user tool_result 含英文
// success。这正是 R5 的真复现路径——cold-start hydrate 读 transcript 回放（richHistoryMessageBuilder），
// 而非 live kernel。
//
// 本测试是该路径的 falsification 基线：
//   1. 证明 transcript 侧 input.file_path 确实存在（path 源在 wire 上）。
//   2. 证明当前 cold-start mapper 发出的 tool_started/tool_finished 事件丢失 path 源——
//      无 title（或 title=toolName）、无 toolInput、无 fileChanges。
//   3. 当 L-α（builder 保留 input / 填 title=file_path）+ L-P0a（hydration 透传 title/toolInput）
//      落地后，本测试的「丢失」断言应被翻转为「保留」，成为修复守护。

const coldStartEditFilepathFixture = "cold-start-edit-filepath.jsonl"

// findToolUseInputFilePath 解析 fixture，返回 Edit tool_use 的 input.file_path（证明 path 源在 wire 上）。
func findToolUseInputFilePath(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(claudeSourceShapesDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		msg, _ := rec["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		for _, blk := range content {
			block, _ := blk.(map[string]any)
			if block["type"] == "tool_use" && block["name"] == "Edit" {
				input, _ := block["input"].(map[string]any)
				if fp, _ := input["file_path"].(string); fp != "" {
					return fp
				}
			}
		}
	}
	return ""
}

// TestClaudeColdStartFixtures_TranscriptContainsFilePath 证明 transcript 的 Edit tool_use
// 携带 input.file_path（path 源在 wire 上存在）。这是 1C 证伪的前提：path 不是「Claude 从不
// 产出」，而是「cold-start builder 丢弃」。
func TestClaudeColdStartFixtures_TranscriptContainsFilePath(t *testing.T) {
	fp := findToolUseInputFilePath(t, coldStartEditFilepathFixture)
	if fp == "" {
		t.Fatal("cold-start fixture must contain an Edit tool_use with input.file_path; got none")
	}
	if !strings.Contains(fp, "/") {
		t.Errorf("input.file_path = %q, expected a path containing '/' (extractPrimaryPath branch 2 needs a path-like title)", fp)
	}
	t.Logf("transcript input.file_path = %q (path source exists on wire)", fp)
}

// TestClaudeColdStartFixtures_MapperPreservesPath_PostLAlpha 验证 L-α（+ Phase 1A hydration
// 透传）修复后：cold-start mapper 发出的 tool_started/tool_finished 事件携带 path-bearing title、
// toolName 与 toolInput。这是 R5 冷启动 path 恢复的关键证据——此前（PRE-Lα）这些字段全部丢失。
//
// 修复路径：claudeEntryToProjectionEvents 的 tool_use 分支用 claudeSummarizeToolInput 从 input.file_path
// 派生 title，并通过 toolUseMeta 把 toolName/title/toolInput 从 assistant tool_use 关联到 user tool_result
// 的 tool_finished（Phase 1A hydration 再透传到 iOS）。
func TestClaudeColdStartFixtures_MapperPreservesPath_PostLAlpha(t *testing.T) {
	events := mapClaudeFixture(t, coldStartEditFilepathFixture)

	var started, finished []map[string]any
	for _, ev := range events {
		switch ev.Event {
		case "tool_started":
			started = append(started, ev.Data)
		case "tool_finished":
			finished = append(finished, ev.Data)
		}
	}
	if len(started) == 0 {
		t.Fatal("expected tool_started for the Edit tool_use, got 0")
	}
	if len(finished) == 0 {
		t.Fatal("expected tool_finished for the Edit tool_result, got 0")
	}

	// tool_started 必须携带 path-bearing title + toolName + toolInput
	s0 := started[0]
	if tn, _ := s0["toolName"].(string); tn != "Edit" {
		t.Errorf("tool_started toolName = %q, want Edit", tn)
	}
	if title, _ := s0["title"].(string); title != "src/views/SessionListView.swift" {
		t.Errorf("tool_started title = %q, want src/views/SessionListView.swift (path-bearing, L-α)", title)
	}
	if _, ok := s0["toolInput"]; !ok {
		t.Error("tool_started must carry toolInput (L-α)")
	}

	// tool_finished 必须也携带（通过 toolUseMeta 关联）
	f0 := finished[0]
	if tn, _ := f0["toolName"].(string); tn != "Edit" {
		t.Errorf("tool_finished toolName = %q, want Edit (correlated via toolUseMeta)", tn)
	}
	if title, _ := f0["title"].(string); title != "src/views/SessionListView.swift" {
		t.Errorf("tool_finished title = %q, want src/views/SessionListView.swift (correlated, L-α)", title)
	}
	// fileChanges 仍不期望（Claude driver 从不产出；L-P0b 才会引入）
	if _, ok := f0["fileChanges"]; ok {
		t.Error("fileChanges should still be absent (Claude driver never produces; L-P0b is separate)")
	}
}
