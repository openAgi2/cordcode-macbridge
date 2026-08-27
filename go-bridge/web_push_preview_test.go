package gobridge

import (
	"encoding/json"
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

// 生产时序回归（2026-08-27 18:40 取证）：同一扫描批内 thinking 行（stop_reason=end_turn）
// 先触发终态、text 行后到。终态若立即发布会让 push intent 在正文入 kernel 前计算——
// 通知正文退回固定文案。挂起到批尾统一发布后，intent 一次到位：turn 仍在 running
// （ActiveTurnID 可解析）、正文已入投影（预览完整）。
func TestClaudeBatchEndTerminalCarriesFullPreview(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)

	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_hold")
	publisher := NewEventPublisher("epoch-hold")
	publisher.SetWebPushCandidateSink(pipeline)
	handlers := NewHandlersWithContextAndEpoch(t.Context(), "epoch-hold")
	handlers.installEventPublisher(publisher)

	userRow := claudeTranscriptRelayEntry{Type: "user", UUID: "u-hold-1", Message: &struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	}{ID: "u-hold-1", Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hi"}]`)}}
	thinkingRow := claudeTranscriptRelayEntry{Type: "assistant", UUID: "a-hold-think", Message: &struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	}{ID: "a-hold-think", Role: "assistant", StopReason: "end_turn", Content: json.RawMessage(`[{"type":"thinking","thinking":"先想一下"}]`)}}
	textRow := claudeTranscriptRelayEntry{Type: "assistant", UUID: "a-hold-text", Message: &struct {
		ID         string          `json:"id"`
		Role       string          `json:"role"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	}{ID: "a-hold-text", Role: "assistant", StopReason: "end_turn", Content: json.RawMessage(`[{"type":"text","text":"落霞与孤鹜齐飞，天水共长天一色"}]`)}}

	currentTurn := ""
	running := false
	var held []claudeHeldTerminalEvent
	// 扫描批顺序 = 文件顺序：user → thinking(end_turn) → text(end_turn)
	handlers.deliverClaudeLegacyRow(userRow, "hold-1", "claude", &currentTurn, &running, 4242, &held)
	handlers.deliverClaudeLegacyRow(thinkingRow, "hold-1", "claude", &currentTurn, &running, 4242, &held)
	if got := pipeline.Drain(); len(got) != 0 {
		t.Fatalf("thinking-row terminal must be held, got %d early candidates", len(got))
	}
	handlers.deliverClaudeLegacyRow(textRow, "hold-1", "claude", &currentTurn, &running, 4242, &held)
	if len(held) != 2 {
		t.Fatalf("held terminals = %d, want 2 (both end_turn rows held)", len(held))
	}
	// 批尾统一发布（镜像 relay loop 的 flush）
	for _, ht := range held {
		handlers.sendSessionEventWithPushIntent(ht.sessionID, ht.backendID, "turn_completed", ht.data)
	}

	got := pipeline.Drain()
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1 (second terminal is no_change, deduped by kernel)", len(got))
	}
	if got[0].ContentPreview != "落霞与孤鹜齐飞，天水共长天一色" {
		t.Fatalf("preview = %q (terminal fired before text landed?)", got[0].ContentPreview)
	}
	if got[0].AnchorID != "u-hold-1" {
		t.Fatalf("anchor = %q, want u-hold-1", got[0].AnchorID)
	}
}

// 懒刷新回归：intent 时刻预览为空（claude thinking 行终态先于 text 行 / hydrate
// 窗口旧基线），dispatcher 发送前经 previewReader 重读 authoritative kernel 拿到完整
// 正文；reader 返回空时保留 candidate 自带预览，不引入第二裁判。
func TestDispatcherPreviewReaderRefreshesEmptyIntentPreview(t *testing.T) {
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatalf("LoadWebPushStore: %v", err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	d := NewWebPushDispatcher(store, pipeline, WebPushDispatcherConfig{})
	d.SetPreviewReader(func(c WebPushCandidate) string {
		if c.AnchorID == "turn-lazy-1" {
			return "发送前才落进投影的完整回复"
		}
		return ""
	})

	payload, _, _, err := d.buildPayload(WebPushCandidate{
		Kind: WebPushKindCompletion, BackendID: "claude", SessionID: "lz-1",
		AnchorID: "turn-lazy-1", NotificationKey: "claude|lz-1|turn-lazy-1|completed",
		SessionTitle: "懒刷新会话", ContentPreview: "",
	})
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	var decoded WebPushPayloadV1
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Notification.Body != "发送前才落进投影的完整回复" {
		t.Fatalf("body = %q (lazy refresh missing)", decoded.Notification.Body)
	}
	if decoded.Notification.Title != "CordCode · 懒刷新会话" {
		t.Fatalf("title = %q", decoded.Notification.Title)
	}

	// reader 返回空（kernel 仍无文本）：保留 candidate 自带预览。
	payload, _, _, err = d.buildPayload(WebPushCandidate{
		Kind: WebPushKindCompletion, BackendID: "claude", SessionID: "lz-2",
		AnchorID: "turn-lazy-2", NotificationKey: "claude|lz-2|turn-lazy-2|completed",
		SessionTitle: "懒刷新会话", ContentPreview: "intent 时已取到的预览",
	})
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Notification.Body != "intent 时已取到的预览" {
		t.Fatalf("body = %q (reader must not clobber non-empty intent preview)", decoded.Notification.Body)
	}

	// 两者皆空：回退固定文案（诚实回退，不编造）。
	payload, _, _, err = d.buildPayload(WebPushCandidate{
		Kind: WebPushKindCompletion, BackendID: "claude", SessionID: "lz-3",
		AnchorID: "turn-lazy-3", NotificationKey: "claude|lz-3|turn-lazy-3|completed",
	})
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Notification.Body != "Mac 上的会话已完成，点击查看结果" {
		t.Fatalf("fallback body = %q", decoded.Notification.Body)
	}
}
