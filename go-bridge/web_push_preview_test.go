package gobridge

import (
	"strings"
	"testing"
)

// web_push_preview_test.go —— 完成通知正文真实预览（owner 2026-08-27 决策，对齐
// Antigravity 通知样式）：正文取 authoritative kernel 该 turn 的 assistant text
// parts；reasoning/tool 不进通知；无文本诚实回退固定文案。语义锚点：
// - 预览来自权威投影（kernel reducer），不是 transcript 文件或 relay 原始行；
// - turn 未结算时 text parts 可能仍为 progress presentation，提取不依赖 final；
// - 截断/空白折叠有确定上限（webPushPreviewMaxRunes），通知体积有界。

// armKernelTurn 通过真实 publishEvent 流转武装 kernel：user_message arming →
// reasoning_delta（不得进预览）→ text_delta ×2（正文）。
func armKernelTurn(t *testing.T, h *Handlers, sessionID, turnID, delta1, delta2 string) {
	t.Helper()
	h.publishEvent(LogicalEvent{
		BackendID: "claude",
		SessionID: sessionID,
		Event:     "user_message",
		Data:      map[string]interface{}{"turnId": turnID, "itemId": turnID, "text": "hi"},
	})
	h.publishEvent(LogicalEvent{
		BackendID: "claude",
		SessionID: sessionID,
		Event:     "reasoning_delta",
		Data:      map[string]interface{}{"turnId": turnID, "itemId": turnID, "delta": "内部思考不进通知"},
	})
	h.publishEvent(LogicalEvent{
		BackendID: "claude",
		SessionID: sessionID,
		Event:     "text_delta",
		Data:      map[string]interface{}{"turnId": turnID, "itemId": turnID, "delta": delta1},
	})
	if delta2 != "" {
		h.publishEvent(LogicalEvent{
			BackendID: "claude",
			SessionID: sessionID,
			Event:     "text_delta",
			Data:      map[string]interface{}{"turnId": turnID, "itemId": turnID, "delta": delta2},
		})
	}
}

func TestWebPushCompletedTurnPreviewExtractsAssistantTextOnly(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)

	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_preview")
	publisher := NewEventPublisher("epoch-preview")
	publisher.SetWebPushCandidateSink(pipeline)
	handlers := NewHandlersWithContextAndEpoch(t.Context(), "epoch-preview")
	handlers.installEventPublisher(publisher)

	armKernelTurn(t, handlers, "pv-1", "turn-pv-1", "已完成登录修复，", "共改动 3 个文件")

	handlers.sendSessionEventWithPushIntent("pv-1", "claude", "turn_completed",
		map[string]interface{}{"turnId": "turn-pv-1", "done": true})

	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	c := got[0]
	if c.AnchorID != "turn-pv-1" {
		t.Fatalf("anchor = %q", c.AnchorID)
	}
	if c.ContentPreview != "已完成登录修复，共改动 3 个文件" {
		t.Fatalf("preview = %q (reasoning leaked or text missing)", c.ContentPreview)
	}
}

func TestWebPushCompletedTurnPreviewCollapsesAndTruncates(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)

	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_preview2")
	publisher := NewEventPublisher("epoch-preview2")
	publisher.SetWebPushCandidateSink(pipeline)
	handlers := NewHandlersWithContextAndEpoch(t.Context(), "epoch-preview2")
	handlers.installEventPublisher(publisher)

	long := strings.Repeat("字", 250)
	armKernelTurn(t, handlers, "pv-2", "turn-pv-2", long, "")

	handlers.sendSessionEventWithPushIntent("pv-2", "claude", "turn_completed",
		map[string]interface{}{"turnId": "turn-pv-2", "done": true})

	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	runes := []rune(got[0].ContentPreview)
	if len(runes) != webPushPreviewMaxRunes+1 {
		t.Fatalf("preview rune count = %d, want %d+ellipsis", len(runes), webPushPreviewMaxRunes)
	}
	if !strings.HasSuffix(got[0].ContentPreview, "…") {
		t.Fatalf("truncated preview must end with ellipsis, got tail %q", string(runes[len(runes)-3:]))
	}
}

func TestWebPushCompletedTurnPreviewFallsBackWhenNoText(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)

	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_preview3")
	publisher := NewEventPublisher("epoch-preview3")
	publisher.SetWebPushCandidateSink(pipeline)
	handlers := NewHandlersWithContextAndEpoch(t.Context(), "epoch-preview3")
	handlers.installEventPublisher(publisher)

	// 只有 user_message + reasoning（如纯思考 turn）：无 text parts → 预览为空，
	// 正文必须回退固定文案，不编造内容。
	handlers.publishEvent(LogicalEvent{
		BackendID: "claude",
		SessionID: "pv-3",
		Event:     "user_message",
		Data:      map[string]interface{}{"turnId": "turn-pv-3", "itemId": "turn-pv-3", "text": "hi"},
	})
	handlers.publishEvent(LogicalEvent{
		BackendID: "claude",
		SessionID: "pv-3",
		Event:     "reasoning_delta",
		Data:      map[string]interface{}{"turnId": "turn-pv-3", "itemId": "turn-pv-3", "delta": "只有思考"},
	})

	handlers.sendSessionEventWithPushIntent("pv-3", "claude", "turn_completed",
		map[string]interface{}{"turnId": "turn-pv-3", "done": true})

	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].ContentPreview != "" {
		t.Fatalf("reasoning-only turn must yield empty preview, got %q", got[0].ContentPreview)
	}
	_, body := buildWebPushNotificationText(got[0].Kind, got[0].SessionTitle, got[0].ContentPreview)
	if body != "Mac 上的会话已完成，点击查看结果" {
		t.Fatalf("fallback body = %q", body)
	}
}
