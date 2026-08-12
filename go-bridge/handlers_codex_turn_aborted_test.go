package gobridge

// handlers_codex_turn_aborted_test.go 验证 §5.1 #7 terminal-fix 的 **producer 层**（layer 3）。
// chunk 1（5df5a28，projection_hydrate_transaction_test.go:815-907）只证明 reducer+gate
// （layer 1+2）能消费 *直接喂入* 的 turn_aborted/turn_error 事件。本文件补齐 §5.1 #7 line 211
// 指出的缺口："只写 reducer 端而不写产生侧是死代码" —— 证明真实 rollout JSONL（形态见
// owner 异常 session 019f5453：session_meta + event_msg task_started + response_item +
// event_msg turn_aborted）经产生侧合成后，能驱动 projection 收口，而不是永久 hydrating。
//
// 覆盖的产生侧改动：
//   - scanCodexTranscriptRelayEvents / codexRolloutEntryEvents（change #1）：event_msg
//     payload.type="turn_aborted" → codexRelayEvent{kind:"turn_aborted", turnID}。
//   - codexRelayEventToProjectionEvent（change #2）：turn_aborted → reducer "turn_aborted"。
//   - TurnDone（change #3）：turn_aborted 标记 turn 终态边界。
//   - codexSessionFileRelay switch（change #5）：live file-relay 增长出 turn_aborted →
//     发送 turn_aborted + broadcastIdleState。
//   - detectCodexTranscriptTask / scanCodexTranscriptTaskEvents（change #6）：turn_aborted
//     视同 idle 终态，避免 watch-loop 永久判 running。
//
// discriminator 设计：若产生侧缺失 turn_aborted 合成，content-less aborted turn 在 reducer
// 里只剩 turn_started、无终态 → WaitHydrateCommitReady 的 gate（NonTerminalTurnCountInSet==0）
// 永不满足 → 测试超时失败；turn 状态也不会是 "aborted"。故这些断言对产生侧改动是敏感的。

import (
	"context"
	"os"
	"testing"
	"time"
)

// realShapeAbortedRollout 构造一条形态等同 owner 异常 session 019f5453 的 rollout：
// session_meta → event_msg task_started（带 turn_id）→ response_item（developer 消息，
// 无 assistant 内容）→ event_msg turn_aborted（带 turn_id / reason / completed_at /
// duration_ms）。turn_id 用测试惯用 "turn-abort-1"，字段集合与真实 rollout 一致。
func realShapeAbortedRollout(turnID string) []string {
	return []string{
		`{"timestamp":"2026-07-12T11:16:40.000Z","type":"session_meta","payload":{"id":"` + turnID + `","cwd":"/tmp/ws"}}`,
		`{"timestamp":"2026-07-12T11:16:40.100Z","type":"event_msg","payload":{"type":"task_started","turn_id":"` + turnID + `"}}`,
		`{"timestamp":"2026-07-12T11:16:40.200Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"output_text","text":"system reminder"}]}}`,
		`{"timestamp":"2026-07-12T11:16:41.000Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"` + turnID + `","reason":"interrupted","completed_at":"2026-07-12T11:16:41.000Z","duration_ms":900}}`,
	}
}

// TestScanCodexTranscriptRelayEvents_TurnAborted_RealShape（change #1）：真实形态 rollout
// 经 scanCodexTranscriptRelayEvents → codexRolloutEntryEvents 必须产出一条 turn_aborted
// codexRelayEvent，且 turnID 取自 payload.turn_id。若产生侧未加 case "turn_aborted"，
// 该事件被静默丢弃 → len(events) 无 turn_aborted kind。
func TestScanCodexTranscriptRelayEvents_TurnAborted_RealShape(t *testing.T) {
	path := writeCodexRolloutFile(t, realShapeAbortedRollout("turn-abort-1")...)
	events := scanCodexTranscriptRelayEvents(path, 0)

	var aborted []codexRelayEvent
	for _, ev := range events {
		if ev.kind == "turn_aborted" {
			aborted = append(aborted, ev)
		}
	}
	if len(aborted) != 1 {
		t.Fatalf("turn_aborted events = %d, want exactly 1（产生侧必须从 event_msg 合成）: %+v", len(aborted), events)
	}
	if aborted[0].turnID != "turn-abort-1" {
		t.Fatalf("turn_aborted turnID = %q, want turn-abort-1（取自 payload.turn_id）", aborted[0].turnID)
	}
	// task_started 与 turn_aborted 都应出现（task_started 不应被 turn_aborted 吞掉）。
	hasTaskStarted := false
	for _, ev := range events {
		if ev.kind == "task_started" {
			hasTaskStarted = true
		}
	}
	if !hasTaskStarted {
		t.Fatalf("task_started missing from scan: %+v", events)
	}
}

// TestDetectCodexTranscriptTask_TurnAborted_IsIdle（change #6）：task_started + turn_aborted
// （无 task_complete）必须判定为 idle 终态。若 detectCodexTranscriptTask 未加 case "turn_aborted"，
// state 会永久停在 "running"，致 file-relay watch-loop 滞留一个已死 turn 的文件。
func TestDetectCodexTranscriptTask_TurnAborted_IsIdle(t *testing.T) {
	h := &Handlers{}
	path := writeCodexRolloutFile(t, realShapeAbortedRollout("turn-abort-1")...)

	state, turnID := h.detectCodexTranscriptTask(path)
	if state != "idle" {
		t.Fatalf("detectCodexTranscriptTask state = %q, want idle（turn_aborted 是终态）", state)
	}
	if turnID != "turn-abort-1" {
		t.Fatalf("detectCodexTranscriptTask turnID = %q, want turn-abort-1", turnID)
	}
	if got := h.detectCodexTranscriptTaskState(path); got != "idle" {
		t.Fatalf("detectCodexTranscriptTaskState = %q, want idle", got)
	}
}

// TestCodexColdHydrate_TurnAbortedSettlesProjection（change #1 + #2 + #3 端到端）：真实形态
// rollout 文件经完整 cold-hydrate 管线（produceProjectionHydrateRange → scan → map →
// ApplyHydrateEvent）+ source-complete，projection 的 turn 必须收口为 status="aborted"，
// gate 满足、可 commit。若产生侧未合成 turn_aborted，turn 只剩 turn_started 无终态 →
// WaitHydrateCommitReady 的 NonTerminalTurnCountInSet!=0 → 超时；turn 状态也不会是 aborted。
func TestCodexColdHydrate_TurnAbortedSettlesProjection(t *testing.T) {
	const (
		backendID  = "codex"
		sessionID  = "aborted-cold-hydrate"
		turnID     = "turn-abort-1"
	)
	handlers := newTestHandlers(t)
	path := writeCodexRolloutFile(t, realShapeAbortedRollout(turnID)...)

	kernel := handlers.projectionKernel
	admission, err := kernel.BeginHydrateTransaction(
		backendID, sessionID,
		ProjectionSourceDescriptor{Identity: sessionID},
		false, false, false,
	)
	if err != nil || !admission.Leader {
		t.Fatalf("admission=%+v err=%v", admission, err)
	}

	base := SessionProjection{}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	err = handlers.produceProjectionHydrateRange(
		context.Background(),
		backendID, sessionID, path,
		admission.StartCursor, fi.Size(),
		base,
		func(event projectionHydrateEvent) bool {
			kernel.ApplyHydrateEvent(
				backendID, sessionID,
				handlers.eventPublisher.BridgeEpoch(),
				event.Event, event.Data,
			)
			return true
		},
	)
	if err != nil {
		t.Fatalf("produceProjectionHydrateRange: %v", err)
	}
	kernel.MarkHydrateSourceIngestComplete(backendID, sessionID)

	// gate 必须满足（turn_aborted 终态 + source-EOF）。给 2s 余量；卡住=产生侧没合成终态。
	readyCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := kernel.WaitHydrateCommitReady(readyCtx, backendID, sessionID); err != nil {
		t.Fatalf("WaitHydrateCommitReady: %v（turn_aborted 产生侧未收口 → gate 永不满足）", err)
	}
	commit, err := kernel.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		t.Fatalf("CommitHydrateTransaction: %v", err)
	}
	if len(commit.Projection.Turns) != 1 {
		t.Fatalf("want 1 aborted turn committed, got %d: %+v", len(commit.Projection.Turns), commit.Projection.Turns)
	}
	if got := commit.Projection.Turns[0].Status; got != "aborted" {
		t.Fatalf("cold-hydrated aborted turn status = %q, want \"aborted\"", got)
	}
	if got := commit.Projection.Turns[0].TurnID; got != turnID {
		t.Fatalf("cold-hydrated aborted turn id = %q, want %q", got, turnID)
	}
}

// TestCodexFileRelay_TurnAborted_EmitsAndIdles（change #5）：live file-relay watch 的 rollout
// 增长出 event_msg turn_aborted 时，客户端必须收到 turn_aborted 事件 + idle 状态（与
// task_complete 的 turn_completed+idle 对称，但 abort 不发 completed 通知）。若 file-relay
// switch 未加 case "turn_aborted"，客户端收不到任何收口事件，turn 在 iOS 侧永久 running。
func TestCodexFileRelay_TurnAborted_EmitsAndIdles(t *testing.T) {
	const sessionID = "turn-aborted-relay"
	handlers, agent, client, serverConn := startCodexFileRelayFixture(t, sessionID,
		codexRolloutEvent("task_started"),
	)
	_ = readEventNames(t, client, 2) // running startup: turn_started + session_state_changed

	appendCodexRollout(t, agent.transcriptPath,
		`{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-1","reason":"interrupted","completed_at":"2026-07-12T11:16:41.000Z","duration_ms":900}}`,
	)
	events := readEventNames(t, client, 2) // turn_aborted + session_state_changed(idle)

	gotAborted, gotIdle := false, false
	for _, e := range events {
		switch e {
		case "turn_aborted":
			gotAborted = true
		case "session_state_changed":
			gotIdle = true
		}
	}
	if !gotAborted {
		t.Fatalf("events = %v, want turn_aborted present（live file-relay 必须收口 abort）", events)
	}
	if !gotIdle {
		t.Fatalf("events = %v, want session_state_changed(idle) after abort", events)
	}
	waitCodexFileRelayStopped(t, handlers, sessionID, serverConn)
}
