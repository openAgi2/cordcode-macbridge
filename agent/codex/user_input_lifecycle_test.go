package codex

// user_input_lifecycle_test.go 是 P1-regression：通过 raw-bytes 入口 handleRPCMessage 驱动
// 结构化用户输入的完整生命周期，覆盖设计 §8 端到端不变量。
//
// 与 user_input_session_test.go 的区别：本文件不直接调用 handleServerRequest/handleNotification，
// 而是喂入完整 JSON-RPC envelope 字节， thereby 也覆盖 §8.1 envelope 分类（修复前 method+id 被
// 误判为 response 的根因）。这是 adapter 接线后端到端行为的最小集成回归。
//
// 覆盖：
//   - request → pending → iOS resolve → wire round-trip → resolved(answered,ios)。
//   - 多题（single+options 与 text 混合）：wire answers 必须覆盖全部题，缺一即 invalid。
//   - 关键回归：item/tool/requestUserInput（method+id）必须路由到 server-request，不得被当 response 吞掉。
//   - serverRequest/resolved 原始字节通知 → answered(backend)。

import (
	"encoding/json"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// requestEnvelope 构造一条 item/tool/requestUserInput 的完整 JSON-RPC server-request 字节。
// id 作为普通 string 传入（json.Marshal 负责加引号），保证 canonical 派生为 id 原文。
func requestEnvelope(t *testing.T, id string, questions []codexRawQuestion) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "item/tool/requestUserInput",
		"params": map[string]any{
			"threadId":  "th1",
			"turnId":    "tu1",
			"itemId":    "it1",
			"questions": questions,
		},
	})
	if err != nil {
		t.Fatalf("marshal request envelope: %v", err)
	}
	return b
}

// TestLifecycle_RequestResolveRoundTrip：raw bytes 驱动 request→pending→resolve→resolved 全链路。
func TestLifecycle_RequestResolveRoundTrip(t *testing.T) {
	s, buf := newCaptureSession(t)
	s.handleRPCMessage(requestEnvelope(t, "req-1", []codexRawQuestion{
		{ID: "qb1", Question: "Which color?", Options: []codexRawOption{{Label: "Red"}, {Label: "Blue"}}},
	}))

	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	if ev := mustFindEvent(t, drainEvents(s), core.EventUserInputRequested); ev.UserInput.Status != core.UserInputStatusPending {
		t.Fatalf("raw request 应产 pending，实际 %q", ev.UserInput.Status)
	}

	qid := deriveQuestionID(iid, 0)
	opt0 := deriveOptionID(qid, 0)
	if _, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: opt0}}}}); err != nil {
		t.Fatalf("resolve 失败: %v", err)
	}

	id, answers := parseResponseFrame(t, buf)
	if id != `"req-1"` {
		t.Fatalf("wire response id = %s want \"req-1\"", id)
	}
	if row := answers["qb1"]; len(row.Answers) != 1 || row.Answers[0] != "Red" {
		t.Fatalf("wire answers[qb1] = %+v want [Red]", row.Answers)
	}
	if ev := mustFindEvent(t, drainEvents(s), core.EventUserInputResolved); ev.UserInput.ResolutionSource != "ios" {
		t.Fatalf("resolve 后应 answered/ios，实际 source=%q", ev.UserInput.ResolutionSource)
	}
}

// TestLifecycle_MultiQuestionBothShapes：single+options 与 text 混合，wire answers 必须覆盖全部题。
func TestLifecycle_MultiQuestionBothShapes(t *testing.T) {
	s, buf := newCaptureSession(t)
	s.handleRPCMessage(requestEnvelope(t, "req-1", []codexRawQuestion{
		{ID: "qb-color", Question: "Color?", Options: []codexRawOption{{Label: "Red"}}},
		{ID: "qb-name", Question: "Name?"},
	}))
	drainEvents(s) // 清 pending
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	qColor := deriveQuestionID(iid, 0)
	qName := deriveQuestionID(iid, 1)
	optColor := deriveOptionID(qColor, 0)

	if _, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer, []core.UserInputAnswer{
		{QuestionID: qColor, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: optColor}}},
		{QuestionID: qName, Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: "Ada"}}},
	}); err != nil {
		t.Fatalf("多题 answer 应成功，实际 %v", err)
	}
	_, answers := parseResponseFrame(t, buf)
	if len(answers) != 2 {
		t.Fatalf("wire answers 应含 2 题，实际 %d: %+v", len(answers), answers)
	}
	if row := answers["qb-color"]; len(row.Answers) != 1 || row.Answers[0] != "Red" {
		t.Fatalf("qb-color = %+v want [Red]", row.Answers)
	}
	if row := answers["qb-name"]; len(row.Answers) != 1 || row.Answers[0] != "Ada" {
		t.Fatalf("qb-name = %+v want [Ada]", row.Answers)
	}
}

// TestLifecycle_MultiQuestionMissingOneIsInvalid：少答一题 → invalid_answer_shape，不写 backend。
func TestLifecycle_MultiQuestionMissingOneIsInvalid(t *testing.T) {
	s, buf := newCaptureSession(t)
	s.handleRPCMessage(requestEnvelope(t, "req-1", []codexRawQuestion{
		{ID: "qb-color", Question: "Color?", Options: []codexRawOption{{Label: "Red"}}},
		{ID: "qb-name", Question: "Name?"},
	}))
	drainEvents(s)
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	qColor := deriveQuestionID(iid, 0)
	optColor := deriveOptionID(qColor, 0)

	// 只答第一题，缺第二题。
	_, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer, []core.UserInputAnswer{
		{QuestionID: qColor, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: optColor}}},
	})
	uie, ok := err.(*core.UserInputError)
	if !ok || uie.Code != "invalid_answer_shape" {
		t.Fatalf("少答一题应 invalid_answer_shape，实际 %T %v", err, err)
	}
	if buf.Len() != 0 {
		t.Fatalf("invalid 不应写 backend，实际 %s", buf.String())
	}
}

// TestLifecycle_ServerRequestNotMisroutedAsResponse：关键回归（§3.2/§8.1）。
// item/tool/requestUserInput 携带 method+id；修复前 driver 因“有 id”把它当 response 丢进 handleResponse，
// 导致 pending map 查不到 id 而静默丢弃，永远不产 pending 事件。修复后必须路由到 server-request。
func TestLifecycle_ServerRequestNotMisroutedAsResponse(t *testing.T) {
	s, _ := newCaptureSession(t)
	s.handleRPCMessage(requestEnvelope(t, "req-7", []codexRawQuestion{
		{ID: "qb1", Question: "Pick?", Options: []codexRawOption{{Label: "A"}}},
	}))
	evs := drainEvents(s)
	// 必须产生 EventUserInputRequested（证明走了 server-request 分支，而非被 handleResponse 吞掉）。
	found := false
	for _, e := range evs {
		if e.Type == core.EventUserInputRequested {
			found = true
		}
	}
	if !found {
		t.Fatalf("method+id 的 requestUserInput 必须路由到 server-request 并产 pending 事件；" +
			"未找到 EventUserInputRequested 说明被误判为 response（§3.2 回归）")
	}
}

// TestLifecycle_ServerRequestResolvedViaRawNotification：raw bytes serverRequest/resolved → answered(backend)。
func TestLifecycle_ServerRequestResolvedViaRawNotification(t *testing.T) {
	s, _ := newCaptureSession(t)
	s.handleRPCMessage(requestEnvelope(t, "req-1", []codexRawQuestion{
		{ID: "qb1", Question: "Pick?", Options: []codexRawOption{{Label: "A"}}},
	}))
	drainEvents(s)

	// 另一端（Mac/other client）回答后，backend 下发 serverRequest/resolved notification。
	notif, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "serverRequest/resolved",
		"params":  map[string]any{"requestId": "req-1", "threadId": "th1"},
	})
	s.handleRPCMessage(notif)

	ev := mustFindEvent(t, drainEvents(s), core.EventUserInputResolved)
	if ev.UserInput.Status != core.UserInputStatusAnswered || ev.UserInput.ResolutionSource != "backend" {
		t.Fatalf("raw notification 应 answered/backend，实际 status=%q source=%q", ev.UserInput.Status, ev.UserInput.ResolutionSource)
	}
}
