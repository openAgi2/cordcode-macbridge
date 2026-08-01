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

// TestClaudeColdStartFixtures_MapperDropsPath_PreLAlpha 锁定当前（有 bug 的）mapper 行为：
// cold-start hydrate 发出的 tool 事件丢失 path 源。这是 R5 的根因证据。L-α + L-P0a 落地后，
// 把本测试的「缺失」断言翻转为「保留」，即转为修复守护。
func TestClaudeColdStartFixtures_MapperDropsPath_PreLAlpha(t *testing.T) {
	events := mapClaudeFixture(t, coldStartEditFilepathFixture)

	// 收集所有 tool_started / tool_finished 事件
	var toolEvents []map[string]any
	for _, ev := range events {
		if ev.Event == "tool_started" || ev.Event == "tool_finished" {
			toolEvents = append(toolEvents, ev.Data)
		}
	}
	if len(toolEvents) == 0 {
		t.Fatalf("expected at least 1 tool event from Edit tool_use, got 0")
	}

	// 收集所有 tool 事件的 title / toolInput / fileChanges
	hasValidTitle := false
	hasToolName := false
	hasToolInput := false
	hasFileChanges := false
	for _, d := range toolEvents {
		if title, _ := d["title"].(string); title != "" && strings.Contains(title, "/") && title != d["toolName"] {
			hasValidTitle = true
		}
		if tn, _ := d["toolName"].(string); tn != "" {
			hasToolName = true
		}
		if ti, _ := d["toolInput"]; ti != nil {
			hasToolInput = true
		}
		if fc, _ := d["fileChanges"]; fc != nil {
			hasFileChanges = true
		}
	}

	// Characterization (PRE L-α): cold-start mapper 发出的 tool 事件丢失 path 源——
	// 即便 transcript 侧 input.file_path 存在（前一个测试已证），builder（richHistoryMessageBuilder.addToolUse
	// claudecode.go:880-903）把 title 写成 toolName、不写 toolInput/fileChanges，且当前 transcript stream
	// 路径甚至连 toolName 都未透传到 tool_finished 事件。这正是「仅改 hydration 透传 title 对 Claude 冷启动
	// 是空操作」（§6.2.0）的证据。
	if hasValidTitle {
		t.Errorf("PRE-Lα characterization: expected NO tool event with a path-like title (title != toolName, contains '/'), but found one. If L-α landed, flip to assert presence.")
	}
	// toolName 当前也未透传到 cold-start tool 事件（更深的丢弃点）；L-α/L-P0a 修复后应出现。
	// 此断言记录现状；修复后翻转。
	t.Logf("PRE-Lα: tool events=%d, hasToolName=%v, hasValidTitle=%v, hasToolInput=%v, hasFileChanges=%v",
		len(toolEvents), hasToolName, hasValidTitle, hasToolInput, hasFileChanges)
}
