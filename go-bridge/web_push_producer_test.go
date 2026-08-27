package gobridge

import (
	"strings"
	"testing"
)

// D3 — producer 位点与单 owner 门（web push §8.1 位点清单 / Gate D）。
//
// 语义：
// - 样本门默认全关：EVT-TURN-1/EVT-PERM-1/EVT-INPUT-1/EVT-ERROR-1 未归档前，
//   两个 producer 位点对任何事件都不得产生 PushIntent；
// - 门开后仍要求完整 identity（turnId/requestId），缺失即不发送，不退回 session-only key；
// - 单一摄入所有者：agent relay 在跑时 passive 分支不可达（passiveFeedAllowed false），
//   同一事件只能有一个 producer；
// - 非清单事件（text_delta/question_asked/derived）永不产生 intent；
// - 外部 turn 的被动 terminal（PWA 不在线）在门开后仍产生 candidate。

func enableKindGateForTest(t *testing.T, kind WebPushNotificationKind) {
	t.Helper()
	prev := webPushKindGates[kind]
	webPushKindGates[kind] = webPushKindGate{GateID: prev.GateID, Passed: true}
	t.Cleanup(func() { webPushKindGates[kind] = prev })
}

func producerKernelWithRunningTurn(t *testing.T) *ProjectionKernel {
	t.Helper()
	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	kernel.IngestLive(EventMessage{
		BackendID: "codex", SessionID: "prod-1", BridgeEpoch: "e1",
		PerSessionSeq: 1, Event: "user_message",
		Data: map[string]interface{}{"turnId": "turn-42", "itemId": "turn-42", "text": "hi"},
	})
	return kernel
}

func TestProducerSampleGatesDefaultOff(t *testing.T) {
	for kind, gate := range webPushKindGates {
		if gate.Passed {
			t.Fatalf("kind %s gate must default to OFF until its real sample is archived", kind)
		}
		if gate.GateID == "" {
			t.Fatalf("kind %s missing gate id", kind)
		}
	}
	kernel := producerKernelWithRunningTurn(t)
	if intent := pushIntentForRelayTerminal(kernel, "codex", "prod-1", "turn_completed", map[string]interface{}{"done": true}, ""); intent != nil {
		t.Fatalf("completion intent produced with gate OFF: %+v", intent)
	}
	if intent := pushIntentForRelayTerminal(kernel, "codex", "prod-1", "permission_request", map[string]interface{}{"requestId": "r1"}, ""); intent != nil {
		t.Fatalf("permission intent produced with gate OFF: %+v", intent)
	}
	if intent := pushIntentForPassiveEvent(kernel, "codex", "prod-1", "turn_completed", nil, ""); intent != nil {
		t.Fatalf("passive intent produced with gate OFF: %+v", intent)
	}
}

func TestProducerCompletionIntentRequiresTurnIdentity(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	kernel := producerKernelWithRunningTurn(t)

	intent := pushIntentForRelayTerminal(kernel, "codex", "prod-1", "turn_completed", map[string]interface{}{"done": true}, "")
	if intent == nil {
		t.Fatal("gate ON + active turn must produce completion intent")
	}
	if intent.Kind != WebPushKindCompletion {
		t.Fatalf("kind = %s", intent.Kind)
	}
	if intent.NotificationKey != "codex|prod-1|turn-42|completed" {
		t.Fatalf("NotificationKey = %q", intent.NotificationKey)
	}
	if intent.AnchorKind != "turn" || intent.AnchorID != "turn-42" {
		t.Fatalf("anchor = %+v", intent)
	}

	// 无 kernel state（identity 缺失）→ 不发送，绝不退回 session-only key。
	bare := NewProjectionKernel(NewProjectionReducer(), nil)
	if intent := pushIntentForRelayTerminal(bare, "codex", "no-state", "turn_completed", nil, ""); intent != nil {
		t.Fatalf("identity-less completion must not produce intent: %+v", intent)
	}
	if intent := pushIntentForRelayTerminal(nil, "codex", "prod-1", "turn_completed", nil, ""); intent != nil {
		t.Fatalf("nil kernel must not produce intent: %+v", intent)
	}
}

func TestProducerPermissionIntentRequiresRequestIdentity(t *testing.T) {
	enableKindGateForTest(t, WebPushKindPermission)
	kernel := producerKernelWithRunningTurn(t)

	intent := pushIntentForRelayTerminal(kernel, "codex", "prod-1", "permission_request", map[string]interface{}{"requestId": "req-9"}, "")
	if intent == nil {
		t.Fatal("gate ON + requestId must produce permission intent")
	}
	if intent.NotificationKey != "codex|prod-1|req-9|permission" {
		t.Fatalf("NotificationKey = %q", intent.NotificationKey)
	}
	if intent.AnchorKind != "interaction" || intent.AnchorID != "req-9" {
		t.Fatalf("anchor = %+v", intent)
	}

	// itemId 兜底（dsh-web mux 形状）。
	if intent := pushIntentForRelayTerminal(kernel, "codex", "prod-1", "permission_request", map[string]interface{}{"itemId": "req-10"}, ""); intent == nil {
		t.Fatal("itemId fallback must work")
	}
	// 无 identity → 不发送。
	if intent := pushIntentForRelayTerminal(kernel, "codex", "prod-1", "permission_request", map[string]interface{}{}, ""); intent != nil {
		t.Fatalf("identity-less permission must not produce intent: %+v", intent)
	}
	// permission 门没开时 input 门开了也不能借道。
	if intent := pushIntentForRelayTerminal(kernel, "codex", "prod-1", "user_input_requested", map[string]interface{}{"interactionId": "i1"}, ""); intent != nil {
		t.Fatalf("input events are not a producer event in v1: %+v", intent)
	}
}

func TestProducerNonWhitelistedEventsNeverProduceIntent(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	enableKindGateForTest(t, WebPushKindPermission)
	kernel := producerKernelWithRunningTurn(t)
	for _, event := range []string{
		"text_delta", "reasoning_delta", "tool_started", "sessions_changed",
		"question_asked", "todos_updated", "turn_started", "turn_error",
		"permission_resolved", "user_input_resolved", "session_state_changed",
	} {
		if intent := pushIntentForRelayTerminal(kernel, "codex", "prod-1", event, map[string]interface{}{"turnId": "t", "requestId": "r"}, ""); intent != nil {
			t.Fatalf("event %q must never produce intent: %+v", event, intent)
		}
		if intent := pushIntentForPassiveEvent(kernel, "codex", "prod-1", event, map[string]interface{}{"turnId": "t"}, ""); intent != nil {
			t.Fatalf("passive event %q must never produce intent: %+v", event, intent)
		}
	}
}

func TestProducerSingleOwnershipMutualExclusion(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	// agent relay 在跑 → passive 补投分支不可达（同一事件只有一个 producer）。
	if passiveFeedAllowed(true, true, true, "turn_completed") {
		t.Fatal("passive feed must be disallowed while agent relay is running (single ingest owner)")
	}
	if !passiveFeedAllowed(false, true, false, "text_delta") {
		t.Fatal("sanity: observation-covered session without relay is passive-fed")
	}
	// 外部 turn：无 observation 但有既有 kernel state 的 terminal（被动侧唯一兜底路径）。
	if !passiveFeedAllowed(false, false, true, "turn_completed") {
		t.Fatal("external-turn terminal with kernel state must be passive-fed")
	}
	if passiveFeedAllowed(false, false, true, "text_delta") {
		t.Fatal("non-terminal external events must not enter the kernel (no hidden timelines)")
	}
}

func TestProducerPassiveExternalTurnProducesCandidateEndToEnd(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_ext")

	kernel := NewProjectionKernel(NewProjectionReducer(), nil)
	// 既有 kernel state（用户曾打开过该 session 的外部 turn）。
	kernel.IngestLive(EventMessage{
		BackendID: "codex", SessionID: "ext-1", BridgeEpoch: "e1",
		PerSessionSeq: 1, Event: "user_message",
		Data: map[string]interface{}{"turnId": "turn-ext", "itemId": "turn-ext", "text": "external"},
	})

	publisher := NewEventPublisher("epoch-1")
	publisher.SetProjectionKernel(kernel)
	publisher.SetWebPushCandidateSink(pipeline)

	// 被动泵补投路径（passiveFeedAllowed 为真的形状）：terminal completion、零在线 target。
	allowed := passiveFeedAllowed(false, false, true, "turn_completed")
	if !allowed {
		t.Fatal("setup: external terminal should be allowed")
	}
	intent := pushIntentForPassiveEvent(kernel, "codex", "ext-1", "turn_completed", map[string]interface{}{"done": true}, "外部标题")
	if intent == nil {
		t.Fatal("external turn terminal must produce a push intent when the gate is on")
	}
	// 真实 completion 事件必须携带 turn identity（reducer 才能收口 running turn）；
	// 无 identity 的 completion 是 kernel NoChange → candidate 被 fail-closed 丢弃。
	publisher.PublishLogical(LogicalEvent{
		BackendID:  "codex",
		SessionID:  "ext-1",
		Event:      "turn_completed",
		Data:       map[string]interface{}{"turnId": "turn-ext", "itemId": "turn-ext"},
		PushIntent: intent,
	})
	publisher.PublishLogical(LogicalEvent{
		BackendID:  "codex",
		SessionID:  "ext-1",
		Event:      "turn_completed",
		Data:       map[string]interface{}{"done": true},
		PushIntent: intent, // 无 identity 的重复 completion：kernel NoChange → 不再入队
	})
	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("external-turn candidate = %d, want 1 (offline PWA is the audience)", len(got))
	}
	if !strings.Contains(got[0].NotificationKey, "turn-ext") {
		t.Fatalf("NotificationKey = %q", got[0].NotificationKey)
	}
}

func TestProducerKeyLayoutMatchesLedgerContract(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	kernel := producerKernelWithRunningTurn(t)
	intent := pushIntentForRelayTerminal(kernel, "codex", "prod-1", "turn_completed", nil, "")
	if intent == nil {
		t.Fatal("setup")
	}
	// ledger hash 是脱敏的：key 结构 (backend|session|turn|suffix) 不得出现在 hash 里。
	hash := WebPushNotificationKeyHash(intent.NotificationKey)
	if strings.Contains(hash, "turn-42") || strings.Contains(hash, "prod-1") {
		t.Fatalf("ledger hash leaks key fields: %q", hash)
	}
}
