package gobridge

import (
	"testing"
)

// 2026-08-27 生产断链回归（监工指令 3 号 C4 取证）：claude/codex session file relay
// 的 turn_completed 经 sendSessionEventWithPushIntent（producer 位点 3）后，必须在
// 零在线 target 下仍产出 completion candidate——此前该路径不声明 intent，真实
// claude web turn（session 3150cefc…）完成后 dispatcher 静默零投递。
//
// 语义：
// - file relay 终态投递是这些 session 的事实 ingest owner（位点 1/2 都不覆盖）；
// - completion intent 的 turn 身份来自 kernel activeTurn（user_message 先行 arming）；
// - 非终态事件走位点 3 的通用尾巴（grok leader relay）不得产生 candidate。

func TestFileRelayTerminalSendProducesWebPushCandidate(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)

	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_fr_test")

	publisher := NewEventPublisher("epoch-fr")
	publisher.SetWebPushCandidateSink(pipeline)

	handlers := NewHandlersWithContextAndEpoch(t.Context(), "epoch-fr")
	handlers.installEventPublisher(publisher)

	// Arm the kernel's active turn (identity source for the completion intent) —
	// mirrors the real file-relay order: user_message row lands before the terminal row.
	handlers.publishEvent(LogicalEvent{
		BackendID: "claude",
		SessionID: "fr-1",
		Event:     "user_message",
		Data:      map[string]interface{}{"turnId": "turn-fr-1", "itemId": "turn-fr-1", "text": "hi"},
	})

	handlers.sendSessionEventWithPushIntent("fr-1", "claude", "turn_completed",
		map[string]interface{}{"turnId": "turn-fr-1", "done": true, "reason": "task_complete"})

	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1 (file-relay terminal must reach the dispatcher even with zero online targets)", len(got))
	}
	c := got[0]
	if c.Kind != WebPushKindCompletion || c.BackendID != "claude" || c.SessionID != "fr-1" {
		t.Fatalf("candidate = %+v", c)
	}
	if c.AnchorKind != "turn" || c.AnchorID != "turn-fr-1" {
		t.Fatalf("anchor = %+v (want turn/turn-fr-1)", c)
	}
	if c.NotificationKey != "claude|fr-1|turn-fr-1|completed" {
		t.Fatalf("notificationKey = %q", c.NotificationKey)
	}
	if c.EventID == "" {
		t.Fatal("candidate carries no EventID (dispatcher ledger needs it)")
	}
}

func TestFileRelayNonTerminalSendProducesNoCandidate(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)

	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_fr_test2")

	publisher := NewEventPublisher("epoch-fr2")
	publisher.SetWebPushCandidateSink(pipeline)

	handlers := NewHandlersWithContextAndEpoch(t.Context(), "epoch-fr2")
	handlers.installEventPublisher(publisher)

	handlers.publishEvent(LogicalEvent{
		BackendID: "claude",
		SessionID: "fr-2",
		Event:     "user_message",
		Data:      map[string]interface{}{"turnId": "turn-fr-2", "itemId": "turn-fr-2", "text": "hi"},
	})
	handlers.sendSessionEventWithPushIntent("fr-2", "claude", "text_delta", map[string]interface{}{"text": "x"})

	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("non-terminal via producer site 3 produced %d candidates, want 0", len(got))
	}
}
