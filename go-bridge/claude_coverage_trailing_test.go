package gobridge

import (
	"testing"
)

// 2026-10-20 真机事故回归（owner 白等 5 分钟）：尾部悬挂 tool_use turn（进程被
// kill、tool_result 永不落盘）不属于「未答 Ask」形态——旧检测器不认 → coverage
// 不触发 → commit gate 死等 15s×N。扩语义后一切尾部非终态 turn 都进 coverage
// 裁决（dead → 合成 turn_aborted 终态化，live → sourceIsLive/lease 放行）。
func TestTrailingCoverageDetectsDanglingToolUseTurn(t *testing.T) {
	snap := SessionProjection{Turns: []TurnProjection{
		{TurnID: "t-done", Status: "completed"},
		{TurnID: "t-ask", Status: "running", Assistant: &MessageProjection{Parts: []ProjectionPart{
			{Type: "user_input", UserInputStatus: "pending"},
		}}},
		{TurnID: "t-dangling-tool", Status: "running", Assistant: &MessageProjection{Parts: []ProjectionPart{
			{Type: "tool", ItemID: "tool-1", ToolStatus: "pending"},
			{Type: "text", Text: "devicectl 仍显示 unavailable。"},
		}}},
	}}
	ids := claudeTrailingCoverageNeededTurnIDs(snap)
	want := map[string]bool{"t-ask": false, "t-dangling-tool": false}
	for _, id := range ids {
		if _, ok := want[id]; ok {
			want[id] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("尾部非终态 turn %s 必须进入 coverage 裁决（got %v）", id, ids)
		}
	}
}

func TestTrailingCoverageIgnoresTerminalAndEmpty(t *testing.T) {
	snap := SessionProjection{Turns: []TurnProjection{
		{TurnID: "t-completed", Status: "completed"},
		{TurnID: "t-aborted", Status: "aborted"},
		{TurnID: "t-error", Status: "error"},
	}}
	if ids := claudeTrailingCoverageNeededTurnIDs(snap); len(ids) != 0 {
		t.Fatalf("全终态快照不得触发 coverage（got %v）", ids)
	}
	if ids := claudeTrailingCoverageNeededTurnIDs(SessionProjection{}); len(ids) != 0 {
		t.Fatalf("空快照不得触发 coverage")
	}
}

// 2026-10-21 评审 MAJOR-1：CLI 本地命令回显 turn（User 为 /cmd 文本、Assistant
// 无）必须从 coverage 豁免——d78100ee 扩语义把它计入后，dead 进程下被合成
// turn_aborted，会话被标失败（2026-10-11 /model 修复回归）。真正非终态 turn
//（悬挂 tool_use、未答 Ask）仍在 coverage 内（上两测锁定）。
func TestTrailingCoverageExemptsCommandEchoTurn(t *testing.T) {
	snap := SessionProjection{Turns: []TurnProjection{
		{TurnID: "t-done", Status: "completed"},
		{TurnID: "t-echo", Status: "running", User: &MessageProjection{Parts: []ProjectionPart{
			{Type: "text", Text: "/model haiku"},
		}}},
		{TurnID: "t-dangling-tool", Status: "running", Assistant: &MessageProjection{Parts: []ProjectionPart{
			{Type: "tool", ItemID: "tool-1", ToolStatus: "pending"},
		}}},
	}}
	ids := claudeTrailingCoverageNeededTurnIDs(snap)
	for _, id := range ids {
		if id == "t-echo" {
			t.Fatalf("命令回显 turn 不得进入 coverage（2026-10-11 回归，评审 MAJOR-1）: %v", ids)
		}
	}
	foundDangling := false
	for _, id := range ids {
		if id == "t-dangling-tool" {
			foundDangling = true
		}
	}
	if !foundDangling {
		t.Fatalf("悬挂 tool_use turn 必须保留在 coverage（2026-10-20 卡死修复语义）: %v", ids)
	}
}
