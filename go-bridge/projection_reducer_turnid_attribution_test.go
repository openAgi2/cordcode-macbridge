package gobridge

// projection_reducer_turnid_attribution_test.go — 回归：turn 归属优先显式 turnId。
//
// 背景（PERF-S0B fixture 生成发现）：codex-web turn-scoped 冷 hydrate 事件同时携带
// turnId=官方 turn id 与 itemId=官方 item id；旧 reducer 只按 itemId 归属，导致官方
// turn 按 item 碎片化（真实 catalog 样本：2 官方 turns → 5 turns）。legacy/live 帧
// （无 turnId，itemId 即 turn id）必须保持原行为。

import (
	"testing"
)

func TestTextDeltaPrefersExplicitTurnIDOverItemID(t *testing.T) {
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	if _, err := kernel.BeginHydrateTransaction(
		"codex-web", "s-attrib", ProjectionSourceDescriptor{Identity: "s-attrib"}, false, false, false,
	); err != nil {
		t.Fatal(err)
	}
	kernel.ApplyHydrateEvent("codex-web", "s-attrib", "e", "user_message", map[string]interface{}{
		"itemId": "item-u1", "turnId": "01a0-official-turn", "text": "q",
	})
	// 官方形状：turnId=官方 turn，itemId=官方 item（≠turn）。
	kernel.ApplyHydrateEvent("codex-web", "s-attrib", "e", "text_delta", map[string]interface{}{
		"itemId": "item-a1", "turnId": "01a0-official-turn", "delta": "answer",
	})
	commit, err := kernel.CommitHydrateTransaction("codex-web", "s-attrib")
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Projection.Turns) != 1 {
		t.Fatalf("official turn fragmented: %d turns (%+v)", len(commit.Projection.Turns), commit.Projection.Turns)
	}
	turn := commit.Projection.Turns[0]
	if turn.TurnID != "01a0-official-turn" {
		t.Fatalf("turn id = %q, want official turn id", turn.TurnID)
	}
	if turn.Assistant == nil || len(turn.Assistant.Parts) == 0 || turn.Assistant.Parts[0].Text != "answer" {
		t.Fatalf("assistant content missing from official turn: %+v", turn.Assistant)
	}
}

func TestTextDeltaLegacyItemIDAttributionUnchanged(t *testing.T) {
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	if _, err := kernel.BeginHydrateTransaction(
		"claude", "s-legacy", ProjectionSourceDescriptor{Identity: "s-legacy"}, false, false, false,
	); err != nil {
		t.Fatal(err)
	}
	// legacy/live 帧：无 turnId，itemId 即 turn id（rollout/live_frame 约定）。
	kernel.ApplyHydrateEvent("claude", "s-legacy", "e", "user_message", map[string]interface{}{
		"itemId": "msg_1", "text": "q",
	})
	kernel.ApplyHydrateEvent("claude", "s-legacy", "e", "text_delta", map[string]interface{}{
		"itemId": "msg_1", "delta": "a1",
	})
	commit, err := kernel.CommitHydrateTransaction("claude", "s-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Projection.Turns) != 1 || commit.Projection.Turns[0].TurnID != "msg_1" {
		t.Fatalf("legacy attribution changed: %+v", commit.Projection.Turns)
	}
	if commit.Projection.Turns[0].Assistant == nil {
		t.Fatal("legacy assistant content lost")
	}
}

func TestReasoningDeltaPrefersExplicitTurnIDOverItemID(t *testing.T) {
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	if _, err := kernel.BeginHydrateTransaction(
		"codex-web", "s-reason", ProjectionSourceDescriptor{Identity: "s-reason"}, false, false, false,
	); err != nil {
		t.Fatal(err)
	}
	kernel.ApplyHydrateEvent("codex-web", "s-reason", "e", "user_message", map[string]interface{}{
		"itemId": "item-u1", "turnId": "turn-r", "text": "q",
	})
	kernel.ApplyHydrateEvent("codex-web", "s-reason", "e", "reasoning_delta", map[string]interface{}{
		"itemId": "item-r1", "turnId": "turn-r", "delta": "thinking",
	})
	commit, err := kernel.CommitHydrateTransaction("codex-web", "s-reason")
	if err != nil {
		t.Fatal(err)
	}
	if len(commit.Projection.Turns) != 1 {
		t.Fatalf("reasoning fragmented official turn: %+v", commit.Projection.Turns)
	}
	hasReasoning := false
	if commit.Projection.Turns[0].Assistant != nil {
		for _, part := range commit.Projection.Turns[0].Assistant.Parts {
			if part.Type == "reasoning" {
				hasReasoning = true
			}
		}
	}
	if !hasReasoning {
		t.Fatalf("reasoning missing from official turn: %+v", commit.Projection.Turns[0].Assistant)
	}
}
