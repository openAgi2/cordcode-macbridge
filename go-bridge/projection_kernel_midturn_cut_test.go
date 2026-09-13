package gobridge

import (
	"context"
	"testing"
	"time"
)

// 同族时序变体（真机 22:50 轮）：Stop hook 先开 hydrate（liveSnap=running），
// turn_completed 随后到达被 defer 进 pendingLive；冷 cut 同样落在 turn 中间。
// commit 时 cold=running、live=running（merge 不动）→ drain pendingLive 的
// turn_completed 必须把 baseline 收口成 idle 并 bump rev——否则 iOS 永远等不到
// 完成翻转。
func TestKernelHydrateCommitDeferredTurnCompletedSettlesBaseline(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	const sessionID = "claude-deferred-complete"

	kernel.IngestLive(EventMessage{
		BackendID: "claude", SessionID: sessionID, BridgeEpoch: "e1",
		PerSessionSeq: 1, Event: "user_message",
		Data: map[string]interface{}{"itemId": "u1", "turnId": "u1", "text": "Reply READY."},
	})
	admission, err := kernel.BeginHydrateTransaction(
		"claude", sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "/tmp/midturn.jsonl", Cursor: 26205},
		false, false, true,
	)
	if err != nil || !admission.Leader {
		t.Fatalf("admission = %+v err=%v", admission, err)
	}
	if !kernel.ApplyHydrateEvent(
		"claude", sessionID, "e1", "user_message",
		map[string]interface{}{"itemId": "u1", "turnId": "u1", "text": "Reply READY."},
	) {
		t.Fatal("cold user_message was not applied")
	}
	// turn_completed 在 hydrate 窗口内到达 → defer 进 pendingLive。
	kernel.IngestLive(EventMessage{
		BackendID: "claude", SessionID: sessionID, BridgeEpoch: "e1",
		PerSessionSeq: 2, Event: "turn_completed",
		Data: map[string]interface{}{"turnId": "u1"},
	})
	kernel.MarkHydrateSourceIngestComplete("claude", sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := kernel.WaitHydrateCommitReady(ctx, "claude", sessionID); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	commit, err := kernel.CommitHydrateTransaction("claude", sessionID)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if commit.Projection.Execution.Phase != "idle" {
		t.Fatalf("deferred turn_completed must settle baseline to idle, got phase=%s", commit.Projection.Execution.Phase)
	}
	if commit.Projection.SyncRev < 2 {
		t.Fatalf("deferred turn_completed must bump rev past baseline, got SyncRev=%d", commit.Projection.SyncRev)
	}
}
// 真机 22:50 轮第三变体：live turnId 与冷 user identity 不同源（claude live
// turnId 来自 prompt_id / assistant message id，冷 identity 是 user 行 uuid）。
// drain 的 turn_completed 带不匹配 turnId 时，reducer 应 fallback 到 armed
// ActiveTurnID（冷 user_message markRunning 的 identity）收口；若直接 return
// 则 baseline 永久 running（真机 headRev=1 不动即此形态）。
func TestKernelHydrateCommitDeferredTurnCompletedMismatchedIDStillSettles(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	const sessionID = "claude-mismatched-complete"

	kernel.IngestLive(EventMessage{
		BackendID: "claude", SessionID: sessionID, BridgeEpoch: "e1",
		PerSessionSeq: 1, Event: "user_message",
		Data: map[string]interface{}{"itemId": "u1", "turnId": "u1", "text": "Reply READY."},
	})
	admission, err := kernel.BeginHydrateTransaction(
		"claude", sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "/tmp/midturn.jsonl", Cursor: 26205},
		false, false, true,
	)
	if err != nil || !admission.Leader {
		t.Fatalf("admission = %+v err=%v", admission, err)
	}
	if !kernel.ApplyHydrateEvent(
		"claude", sessionID, "e1", "user_message",
		map[string]interface{}{"itemId": "u1", "turnId": "u1", "text": "Reply READY."},
	) {
		t.Fatal("cold user_message was not applied")
	}
	// turnId 与冷 identity 不同源（live 侧 assistant/prompt 身份）。
	kernel.IngestLive(EventMessage{
		BackendID: "claude", SessionID: sessionID, BridgeEpoch: "e1",
		PerSessionSeq: 2, Event: "turn_completed",
		Data: map[string]interface{}{"turnId": "msg_LIVE_assistant_id"},
	})
	kernel.MarkHydrateSourceIngestComplete("claude", sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := kernel.WaitHydrateCommitReady(ctx, "claude", sessionID); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	commit, err := kernel.CommitHydrateTransaction("claude", sessionID)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if commit.Projection.Execution.Phase != "idle" {
		t.Fatalf("mismatched-id turn_completed must still settle armed turn to idle, got phase=%s", commit.Projection.Execution.Phase)
	}
}

// PR0 行 6 权限半边复现（2026-09-13 真机 e483289d）：hydrate 的 JSONL cut 只保证
// 完整行边界，不保证完整 turn——cut 落在 user 行之后、assistant result 行之前时，
// 冷 baseline 回放出幻影 running。若权威 live turn_completed 在 BeginHydrate 之前
// 已 Apply（主 reducer 已终态，事件因此不进 pendingLive），commit 必须保留 live 的
// 终态 execution；否则冷 baseline 把 running Restore 回去，session 永久卡执行中
// （iOS composer 停在 stop 态，没有任何后续事件会翻转它）。
func TestKernelHydrateCommitColdMidTurnCutKeepsLiveTerminal(t *testing.T) {
	kernel := NewProjectionKernel(nil, nil)
	const sessionID = "claude-midturn-cut"

	// 权威 live 真值：user_message + turn_completed → 主 reducer idle。
	kernel.IngestLive(EventMessage{
		BackendID: "claude", SessionID: sessionID, BridgeEpoch: "e1",
		PerSessionSeq: 1, Event: "user_message",
		Data: map[string]interface{}{"itemId": "u1", "turnId": "u1", "text": "Reply READY."},
	})
	kernel.IngestLive(EventMessage{
		BackendID: "claude", SessionID: sessionID, BridgeEpoch: "e1",
		PerSessionSeq: 2, Event: "turn_completed",
		Data: map[string]interface{}{"turnId": "u1"},
	})
	if snap, ok := kernel.reducer.Snapshot("claude", sessionID); !ok || snap.Execution.Phase != "idle" {
		t.Fatalf("live pre-state must be idle, got %+v", snap.Execution)
	}

	// 冷源 cut 落在 turn 中间：只回放 user 行（result 行在 cut 之后）。
	// sourceIsLive=true 复刻真机（claude CLI 活进程 → §3.1 live 信号 → commit
	// gate 放行 in-flight running partial）。
	admission, err := kernel.BeginHydrateTransaction(
		"claude", sessionID,
		ProjectionSourceDescriptor{Identity: sessionID, Path: "/tmp/midturn.jsonl", Cursor: 26205},
		false, false, true,
	)
	if err != nil || !admission.Leader {
		t.Fatalf("admission = %+v err=%v", admission, err)
	}
	if !kernel.ApplyHydrateEvent(
		"claude", sessionID, "e1", "user_message",
		map[string]interface{}{"itemId": "u1", "turnId": "u1", "text": "Reply with the single word READY."},
	) {
		t.Fatal("cold user_message was not applied")
	}
	kernel.MarkHydrateSourceIngestComplete("claude", sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := kernel.WaitHydrateCommitReady(ctx, "claude", sessionID); err != nil {
		t.Fatalf("wait ready: %v", err)
	}

	commit, err := kernel.CommitHydrateTransaction("claude", sessionID)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if commit.Projection.Execution.Phase != "idle" {
		t.Fatalf("commit must keep live terminal idle over cold mid-turn cut, got phase=%s", commit.Projection.Execution.Phase)
	}
	for _, turn := range commit.Projection.Turns {
		if turn.Status == "running" {
			t.Fatalf("commit must settle cold-armed running turn %s under terminal execution", turn.TurnID)
		}
	}
}
