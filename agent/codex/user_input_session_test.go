package codex

// user_input_session_test.go 覆盖 P1 transport 接线后的端到端不变量（设计 §8）：
//
//   - handleServerRequest("item/tool/requestUserInput") → EventUserInputRequested(pending) +
//     registry 注册；questions 规范化（single/text 模式判定）。
//   - malformed questions → EventUserInputRequested(failed/invalid_backend_request)，不注册、不回写。
//   - ResolveUserInput(answer) → 写回 §8.2 wire envelope（每题 answers 恒 string[]，single 恰一）+
//     EventUserInputResolved(answered, source=ios)；同 clientActionID 重试幂等不再写。
//   - ResolveUserInput(reject) → response_not_supported（Codex canReject=false），不写 backend。
//   - 答案 shape 非法 → invalid_answer_shape，claim 释放回 pending，不写 backend。
//   - serverRequest/resolved notification → MarkExternallyResolved → answered(backend)，幂等无第二 part；
//     本端先解决后迟到的 notification 是幂等确认。
//
// 这些测试在真实 appServerSession 上驱动 handleServerRequest / handleNotification / ResolveUserInput，
// 通过捕获 stdin 写回断言 wire envelope，通过 events channel 断言 Kernel 投递事件。
//
// 依据：docs/2026-08-01-codex-claude-structured-user-input-design.md

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// captureWriteCloser 把写回 app-server 的 JSON-RPC response 落到 buffer，供断言。
type captureWriteCloser struct{ buf *bytes.Buffer }

func (c *captureWriteCloser) Write(p []byte) (int, error) { return c.buf.Write(p) }
func (c *captureWriteCloser) Close() error                { return nil }

// newCaptureSession 构造一个 alive 的 appServerSession，stdin 指向捕获 buffer，
// userInputReg 就绪，events channel buffered。
func newCaptureSession(t *testing.T) (*appServerSession, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	s := &appServerSession{
		events:  make(chan core.Event, 128),
		pending: make(map[int64]chan rpcResponseEnvelope),
		stdin:   &captureWriteCloser{buf: buf},
	}
	s.alive.Store(true)
	s.threadID.Store("th1")
	s.userInputReg = newUserInputRegistry()
	return s, buf
}

// drainEvents 非阻塞地把已缓冲的事件全部读出（emit 是非阻塞发送，buffer 足够大不会丢）。
func drainEvents(s *appServerSession) []core.Event {
	var out []core.Event
	for {
		select {
		case ev := <-s.events:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// callRequestUserInput 直接驱动 server-request 分发（等价于 handleRPCMessage 收到 method+id）。
func callRequestUserInput(t *testing.T, s *appServerSession, rawID, paramsJSON string) {
	t.Helper()
	s.handleServerRequest("item/tool/requestUserInput", json.RawMessage(rawID), json.RawMessage(paramsJSON))
}

func mustFindEvent(t *testing.T, evs []core.Event, typ core.EventType) core.Event {
	t.Helper()
	for _, e := range evs {
		if e.Type == typ {
			return e
		}
	}
	t.Fatalf("未找到事件 %s（共 %d 个事件）", typ, len(evs))
	return core.Event{}
}

// --- handleRequestUserInput: pending + normalization ---

func TestRequestUserInputEmitsPendingSingle(t *testing.T) {
	s, buf := newCaptureSession(t)
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	callRequestUserInput(t, s, `"req-1"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Which color?","options":[{"label":"Red"},{"label":"Blue"}]}]
	}`)

	evs := drainEvents(s)
	ev := mustFindEvent(t, evs, core.EventUserInputRequested)
	if ev.UserInput == nil || ev.UserInput.Status != core.UserInputStatusPending {
		t.Fatalf("应发 pending requested 事件，实际 %+v", ev.UserInput)
	}
	if ev.UserInput.InteractionID != iid {
		t.Fatalf("interactionId = %q want %q", ev.UserInput.InteractionID, iid)
	}
	if !ev.UserInput.CanRespond || ev.UserInput.CanReject {
		t.Fatalf("Codex canRespond=true canReject=false，实际 respond=%v reject=%v", ev.UserInput.CanRespond, ev.UserInput.CanReject)
	}
	if len(ev.UserInput.Questions) != 1 {
		t.Fatalf("应规范化 1 题，实际 %d", len(ev.UserInput.Questions))
	}
	q := ev.UserInput.Questions[0]
	if q.ID != deriveQuestionID(iid, 0) || q.AnswerMode != core.UserInputAnswerModeSingle || len(q.Options) != 2 {
		t.Fatalf("规范化字段错: id=%q mode=%q opts=%d", q.ID, q.AnswerMode, len(q.Options))
	}
	if !q.Required {
		t.Fatalf("Codex 每题恒 required=true")
	}
	if s.userInputReg.Status(iid) != registryPending {
		t.Fatalf("registry 应注册为 pending")
	}
	if buf.Len() != 0 {
		t.Fatalf("request 阶段不应写回 backend，实际写了 %s", buf.String())
	}
}

func TestRequestUserInputTextModeNoOptions(t *testing.T) {
	s, _ := newCaptureSession(t)
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	callRequestUserInput(t, s, `"req-1"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Your name?"}]
	}`)
	ev := mustFindEvent(t, drainEvents(s), core.EventUserInputRequested)
	q := ev.UserInput.Questions[0]
	if q.AnswerMode != core.UserInputAnswerModeText || !q.AllowsCustomAnswer || len(q.Options) != 0 {
		t.Fatalf("无 options 应为 text 模式: mode=%q custom=%v opts=%d", q.AnswerMode, q.AllowsCustomAnswer, len(q.Options))
	}
	_ = iid
}

func TestRequestUserInputFailedOnMalformedQuestions(t *testing.T) {
	s, buf := newCaptureSession(t)
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	// question 缺 question 文本：envelope attributable 但无法规范化。
	callRequestUserInput(t, s, `"req-1"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","options":[{"label":"Red"}]}]
	}`)
	ev := mustFindEvent(t, drainEvents(s), core.EventUserInputRequested)
	if ev.UserInput.Status != core.UserInputStatusFailed {
		t.Fatalf("malformed questions 应发 failed，实际 %q", ev.UserInput.Status)
	}
	if ev.UserInput.DiagnosticCode != "invalid_backend_request" || ev.UserInput.CanRespond {
		t.Fatalf("failed 应 diagnosticCode=invalid_backend_request canRespond=false，实际 %q/%v",
			ev.UserInput.DiagnosticCode, ev.UserInput.CanRespond)
	}
	if s.userInputReg.Status(iid) != registryAbsent {
		t.Fatalf("failed 不应注册 responder，实际 registry status=%v", s.userInputReg.Status(iid))
	}
	if buf.Len() != 0 {
		t.Fatalf("failed 不应回写 backend（§8.1 step6），实际写了 %s", buf.String())
	}
}

// --- ResolveUserInput: answer happy path + wire envelope + idempotency ---

// parseResponseFrame 解析捕获 buffer 中的（最后一行）JSON-RPC response。
func parseResponseFrame(t *testing.T, buf *bytes.Buffer) (id string, answers map[string]struct {
	Answers []string `json:"answers"`
}) {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) == 0 || len(lines[len(lines)-1]) == 0 {
		t.Fatalf("捕获 buffer 为空，无 response")
	}
	var frame struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  struct {
			Answers map[string]struct {
				Answers []string `json:"answers"`
			} `json:"answers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(lines[len(lines)-1], &frame); err != nil {
		t.Fatalf("解析 response frame 失败: %v\nraw=%s", err, buf.String())
	}
	return string(frame.ID), frame.Result.Answers
}

func TestResolveUserInputAnswerSingleOption(t *testing.T) {
	s, buf := newCaptureSession(t)
	callRequestUserInput(t, s, `"req-1"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Which color?","options":[{"label":"Red"},{"label":"Blue"}]}]
	}`)
	drainEvents(s) // 清掉 requested

	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	qid := deriveQuestionID(iid, 0)
	opt0 := deriveOptionID(qid, 0) // Red

	res, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: opt0}}}})
	if err != nil {
		t.Fatalf("answer 应成功，实际 err=%v", err)
	}
	if res.Outcome != core.UserInputOutcomeAccepted {
		t.Fatalf("outcome = %q want accepted", res.Outcome)
	}

	id, answers := parseResponseFrame(t, buf)
	if id != `"req-1"` {
		t.Fatalf("response id 应原样回传 req-1，实际 %s", id)
	}
	row, ok := answers["qb1"]
	if !ok {
		t.Fatalf("wire answers 缺 backend question id qb1: %+v", answers)
	}
	if len(row.Answers) != 1 || row.Answers[0] != "Red" {
		t.Fatalf("single+option 应产出 [\"Red\"]，实际 %+v", row.Answers)
	}

	// 应发 resolved(answered, source=ios)。
	ev := mustFindEvent(t, drainEvents(s), core.EventUserInputResolved)
	if ev.UserInput.Status != core.UserInputStatusAnswered || ev.UserInput.ResolutionSource != "ios" {
		t.Fatalf("resolved 事件错: status=%q source=%q", ev.UserInput.Status, ev.UserInput.ResolutionSource)
	}

	// 幂等：同 clientActionID 重试不再写、不再发事件。
	before := buf.Len()
	res2, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: opt0}}}})
	if err != nil {
		t.Fatalf("幂等重试不应返回 error，实际 %v", err)
	}
	if res2.Outcome != core.UserInputOutcomeAccepted {
		t.Fatalf("幂等重试 outcome=%q want accepted", res2.Outcome)
	}
	if buf.Len() != before {
		t.Fatalf("幂等重试不应再写 backend，before=%d after=%d", before, buf.Len())
	}
	if len(drainEvents(s)) != 0 {
		t.Fatalf("幂等重试不应再发事件")
	}
}

func TestResolveUserInputAnswerText(t *testing.T) {
	s, buf := newCaptureSession(t)
	callRequestUserInput(t, s, `"req-1"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Your name?"}]
	}`)
	drainEvents(s)
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	qid := deriveQuestionID(iid, 0)

	if _, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: "  Ada  "}}}}); err != nil {
		t.Fatalf("text answer 应成功，实际 %v", err)
	}
	_, answers := parseResponseFrame(t, buf)
	row := answers["qb1"]
	if len(row.Answers) != 1 {
		t.Fatalf("text 应产出单元素 string[]，实际 %+v", row.Answers)
	}
}

// --- ResolveUserInput: reject not supported ---

func TestResolveUserInputRejectNotSupported(t *testing.T) {
	s, buf := newCaptureSession(t)
	callRequestUserInput(t, s, `"req-1"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Which color?","options":[{"label":"Red"}]}]
	}`)
	drainEvents(s)
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")

	_, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionReject, nil)
	uie, ok := err.(*core.UserInputError)
	if !ok {
		t.Fatalf("reject 应返回 *UserInputError，实际 %T %v", err, err)
	}
	if uie.Code != "response_not_supported" {
		t.Fatalf("reject code = %q want response_not_supported", uie.Code)
	}
	if buf.Len() != 0 {
		t.Fatalf("reject 不应写 backend，实际写了 %s", buf.String())
	}
	// registry 仍 pending（未被 reject 占用）。
	if s.userInputReg.Status(iid) != registryPending {
		t.Fatalf("reject 后 registry 应仍 pending，实际 %v", s.userInputReg.Status(iid))
	}
}

// --- ResolveUserInput: bad shape releases claim ---

func TestResolveUserInputBadShapeReleasesClaim(t *testing.T) {
	s, buf := newCaptureSession(t)
	callRequestUserInput(t, s, `"req-1"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Which color?","options":[{"label":"Red"}]}]
	}`)
	drainEvents(s)
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	qid := deriveQuestionID(iid, 0)

	// 未知 optionId：shape 非法。
	_, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "bogus"}}}})
	uie, ok := err.(*core.UserInputError)
	if !ok || uie.Code != "invalid_answer_shape" {
		t.Fatalf("未知 option 应 invalid_answer_shape，实际 %T %v", err, err)
	}
	if buf.Len() != 0 {
		t.Fatalf("shape 非法不应写 backend，实际写了 %s", buf.String())
	}
	if s.userInputReg.Status(iid) != registryPending {
		t.Fatalf("claim 失败应释放回 pending 允许重试，实际 %v", s.userInputReg.Status(iid))
	}

	// 重试（同 clientActionID）现在应能成功——证明 claim 已释放。
	opt0 := deriveOptionID(qid, 0)
	if _, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: opt0}}}}); err != nil {
		t.Fatalf("释放后重试应成功，实际 %v", err)
	}
}

// --- serverRequest/resolved notification: external resolve + idempotency ---

func feedServerRequestResolved(t *testing.T, s *appServerSession, requestID any) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"requestId": requestID, "threadId": "th1"})
	s.handleNotification("serverRequest/resolved", params)
}

func TestServerRequestResolvedExternalEmitsAnsweredBackend(t *testing.T) {
	s, _ := newCaptureSession(t)
	callRequestUserInput(t, s, `"req-1"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Which color?","options":[{"label":"Red"}]}]
	}`)
	drainEvents(s)
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")

	feedServerRequestResolved(t, s, "req-1")
	ev := mustFindEvent(t, drainEvents(s), core.EventUserInputResolved)
	if ev.UserInput.Status != core.UserInputStatusAnswered || ev.UserInput.ResolutionSource != "backend" {
		t.Fatalf("外部解决应 answered/source=backend，实际 status=%q source=%q", ev.UserInput.Status, ev.UserInput.ResolutionSource)
	}
	if s.userInputReg.Status(iid) != registryResolved {
		t.Fatalf("registry 应 resolved")
	}

	// 幂等：再次 notification 不产生第二 part。
	feedServerRequestResolved(t, s, "req-1")
	if len(drainEvents(s)) != 0 {
		t.Fatalf("重复 external resolved 应幂等，无第二事件")
	}
}

// 关键护栏（§8.3）：本端先回答成功后，迟到的 serverRequest/resolved 是幂等确认，无第二 revision/第二 part。
func TestServerRequestResolvedAfterLocalIsIdempotentConfirm(t *testing.T) {
	s, _ := newCaptureSession(t)
	callRequestUserInput(t, s, `"req-1"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Which color?","options":[{"label":"Red"}]}]
	}`)
	drainEvents(s)
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	qid := deriveQuestionID(iid, 0)
	opt0 := deriveOptionID(qid, 0)

	// 本端先回答。
	if _, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: opt0}}}}); err != nil {
		t.Fatalf("本端 answer 应成功，实际 %v", err)
	}
	drainEvents(s) // 清掉本端 resolved(ios)

	// 迟到的 serverRequest/resolved：不得产生第二 resolved 事件。
	feedServerRequestResolved(t, s, "req-1")
	if got := drainEvents(s); len(got) != 0 {
		t.Fatalf("本端已 resolved 后迟到的 notification 应幂等确认无第二事件，实际 %d 个: %+v", len(got), got)
	}
}

// --- ResolveUserInput on dead session ---

func TestResolveUserInputDeadSession(t *testing.T) {
	s, _ := newCaptureSession(t)
	callRequestUserInput(t, s, `"req-1"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Which color?","options":[{"label":"Red"}]}]
	}`)
	drainEvents(s)
	iid := deriveCodexInteractionID("string", "req-1", "th1", "tu1", "it1")
	s.alive.Store(false)

	_, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer, nil)
	uie, ok := err.(*core.UserInputError)
	if !ok || uie.Code != "session_not_active" {
		t.Fatalf("dead session 应 session_not_active，实际 %T %v", err, err)
	}
}

func TestResolveUserInputContextTimeoutReleasesClaimBeforeWrite(t *testing.T) {
	s, buf := newCaptureSession(t)
	callRequestUserInput(t, s, `"req-timeout"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Which color?","options":[{"label":"Red"}]}]
	}`)
	drainEvents(s)
	iid := deriveCodexInteractionID("string", "req-timeout", "th1", "tu1", "it1")
	qid := deriveQuestionID(iid, 0)
	optID := deriveOptionID(qid, 0)
	s.writeMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := s.ResolveUserInput(ctx, iid, "client-timeout", core.UserInputActionAnswer, []core.UserInputAnswer{{
		QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: optID}},
	}})
	s.writeMu.Unlock()
	if err == nil {
		t.Fatal("context timeout must fail")
	}
	if s.userInputReg.Status(iid) != registryPending {
		t.Fatal("timed-out write must release claim")
	}
	if buf.Len() != 0 {
		t.Fatal("timed-out write must not reach backend")
	}
}

func TestResolveUserInputConcurrentClaimReportsInProgress(t *testing.T) {
	s, buf := newCaptureSession(t)
	callRequestUserInput(t, s, `"req-race"`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Which color?","options":[{"label":"Red"}]}]
	}`)
	drainEvents(s)
	iid := deriveCodexInteractionID("string", "req-race", "th1", "tu1", "it1")
	qid := deriveQuestionID(iid, 0)
	answers := []core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: deriveOptionID(qid, 0)}}}}
	s.writeMu.Lock()
	firstDone := make(chan error, 1)
	go func() {
		_, err := s.ResolveUserInput(context.Background(), iid, "first", core.UserInputActionAnswer, answers)
		firstDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for s.userInputReg.Status(iid) != registryClaimed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	second, err := s.ResolveUserInput(t.Context(), iid, "second", core.UserInputActionAnswer, answers)
	if err != nil || second.Outcome != core.UserInputOutcomeInProgress || second.CurrentStatus != core.UserInputStatusPending {
		t.Fatalf("second writer = %+v, %v; want in_progress/pending", second, err)
	}
	s.writeMu.Unlock()
	if err := <-firstDone; err != nil {
		t.Fatalf("first writer: %v", err)
	}
	if lines := bytes.Count(bytes.TrimSpace(buf.Bytes()), []byte("\n")) + 1; lines != 1 {
		t.Fatalf("backend writes = %d, want 1", lines)
	}
}

// --- int64 request id precision preserved through wire write-back ---

func TestResolveUserInputPreservesInt64RequestID(t *testing.T) {
	s, buf := newCaptureSession(t)
	// 大整数 id（float64 反序列化会丢精度），json.RawMessage 必须原样回传。
	callRequestUserInput(t, s, `9007199254740993`, `{
		"threadId":"th1","turnId":"tu1","itemId":"it1",
		"questions":[{"id":"qb1","question":"Which color?","options":[{"label":"Red"}]}]
	}`)
	drainEvents(s)
	typ, canonical, ok := codexRequestIDType(float64(9007199254740993))
	// float64(9007199254740993) 反序列化为 9007199254740992（精度丢失），canonical 反映该值。
	if !ok {
		t.Fatalf("int64 id 应可分类")
	}
	iid := deriveCodexInteractionID(typ, canonical, "th1", "tu1", "it1")
	qid := deriveQuestionID(iid, 0)
	opt0 := deriveOptionID(qid, 0)

	if _, err := s.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: opt0}}}}); err != nil {
		t.Fatalf("answer 应成功，实际 %v", err)
	}
	// 响应 id 应是原始数字字节（json.RawMessage 原样回传），而不是被 float64 改写的值。
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	last := lines[len(lines)-1]
	if !bytes.Contains(last, []byte(`"id":9007199254740993`)) {
		t.Fatalf("响应应原样保留 int64 id 字节 9007199254740993，实际 %s", last)
	}
}
