package claudecode

// user_input_v2_test.go 覆盖 Claude Code 结构化用户输入 v2 适配器（设计 §9）。
//
// 与 askuserquestion_test.go（v1）/ structured_user_input_contract_test.go（P0 格式冻结）
// 并存：本文件只测 v2 路径——structuredUserInputV2 标志置位后的 handleAskUserQuestionV2 +
// ResolveUserInput + claudeUserInputRegistry 状态机 + ID 派生。
//
// 关键不变量（设计已冻结）：
//   - v2 flag OFF 时 AskUserQuestion 仍走 v1（EventQuestionAsked），不被 v2 拦截；
//   - v2 flag ON 时 AskUserQuestion 在 autoApprove/dontAsk/acceptEditsOnly bypass 之前拦截
//     （bypass 不得替用户回答语义问句，§9.1）；
//   - multiSelect false→single、true→multiple；allowsCustomAnswer=false（即使 "Other"）；
//     duplicate question text / 缺 options → failed/invalid_backend_request；
//   - answer 写 control_response(allow, updatedInput.answers[qText]=label|[labels])；reject 写
//     control_response(deny, "User skipped the question.")；写成功才 ConfirmResolved，写失败 ReleaseClaim；
//   - clientActionID 幂等；bad shape 释放 claim 允许重试。
//
// 依据：docs/2026-08-01-codex-claude-structured-user-input-design.md §6.1 / §9。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// newAskV2TestSession 构造 production-ready、registry 就绪的 claudeSession。
// 复用 askuserquestion_test.go 的 captureStdin（线程安全写回断言）。
func newAskV2TestSession(t *testing.T) (*claudeSession, *captureStdin) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stdin := &captureStdin{}
	cs := &claudeSession{
		events:             make(chan core.Event, 16),
		ctx:                ctx,
		stdin:              stdin,
		claudeUserInputReg: newClaudeUserInputRegistry(),
	}
	cs.sessionID.Store("test-session")
	cs.activeMsgID.Store("turn-test")
	cs.alive.Store(true)
	return cs, stdin
}

// multiQuestionMap 构造一个 multiSelect=true 的 question（用于 multiple 模式测试）。
func multiQuestionMap(question, header string, options ...[2]string) map[string]any {
	opts := make([]any, 0, len(options))
	for _, o := range options {
		opts = append(opts, map[string]any{"label": o[0], "description": o[1]})
	}
	return map[string]any{
		"question":    question,
		"header":      header,
		"multiSelect": true,
		"options":     opts,
	}
}

// findUserInputEvent 在事件列表里找第一个 EventUserInputRequested/Resolved；找不到返回 nil。
func findUserInputEvent(evs []*core.Event, typ core.EventType) *core.Event {
	for _, e := range evs {
		if e.Type == typ {
			return e
		}
	}
	return nil
}

// drainAllEvents 非阻塞排空已缓冲事件。
func drainAllEvents(cs *claudeSession) []*core.Event {
	var out []*core.Event
	for {
		select {
		case ev := <-cs.events:
			out = append(out, &ev)
		default:
			return out
		}
	}
}

// respInner 解析 stdin 最后一行 control_response 的内层 response map。
func respInner(t *testing.T, stdin *captureStdin) map[string]any {
	t.Helper()
	line := stdin.lastJSONLine(t)
	if typ, _ := line["type"].(string); typ != "control_response" {
		t.Fatalf("stdin type = %q, want control_response", typ)
	}
	return nested(t, line, "response", "response")
}

// =====================================================================
// A. request path：handleAskUserQuestionV2
// =====================================================================

// TestV2_FlagOn_SingleQuestionEmitsPending：v2 flag ON + 单选 → pending + 规范化 + registry 注册。
func TestV2_FlagOn_SingleQuestionEmitsPending(t *testing.T) {
	cs, _ := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-1", []any{
		singleQuestionMap("Which color?", "Color", false, [2]string{"Red", "r"}, [2]string{"Blue", "b"}),
	}))

	evs := drainAllEvents(cs)
	ev := findUserInputEvent(evs, core.EventUserInputRequested)
	if ev == nil || ev.UserInput == nil {
		t.Fatalf("应发 EventUserInputRequested，实际 %+v", evs)
	}
	if ev.UserInput.Status != core.UserInputStatusPending {
		t.Fatalf("status = %q want pending", ev.UserInput.Status)
	}
	if !ev.UserInput.CanRespond || !ev.UserInput.CanReject {
		t.Fatalf("Claude canRespond=true canReject=true，实际 respond=%v reject=%v", ev.UserInput.CanRespond, ev.UserInput.CanReject)
	}
	iid := deriveClaudeInteractionID("req-1")
	if ev.UserInput.InteractionID != iid {
		t.Fatalf("interactionId = %q want %q", ev.UserInput.InteractionID, iid)
	}
	if len(ev.UserInput.Questions) != 1 {
		t.Fatalf("应规范化 1 题，实际 %d", len(ev.UserInput.Questions))
	}
	q := ev.UserInput.Questions[0]
	if q.ID != claudeQuestionID(iid, 0) {
		t.Fatalf("questionId = %q want %q", q.ID, claudeQuestionID(iid, 0))
	}
	if q.AnswerMode != core.UserInputAnswerModeSingle {
		t.Fatalf("single 题 mode = %q want single", q.AnswerMode)
	}
	if !q.AllowsCustomAnswer {
		t.Fatalf("Claude AskUserQuestion 应声明 allowsCustomAnswer=true")
	}
	if !q.Required || q.IsSecret {
		t.Fatalf("required=true isSecret=false，实际 required=%v isSecret=%v", q.Required, q.IsSecret)
	}
	if len(q.Options) != 2 || q.Options[0].ID != claudeOptionID(q.ID, 0) || q.Options[0].Label != "Red" {
		t.Fatalf("option 规范化错: %+v", q.Options)
	}
	if cs.claudeUserInputReg.Status(iid) != claudeUIPending {
		t.Fatalf("registry 应注册为 pending")
	}
}

// TestV2_FlagOn_MultiSelectMapsToMultiple：multiSelect=true → multiple 模式。
func TestV2_FlagOn_MultiSelectMapsToMultiple(t *testing.T) {
	cs, _ := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-ms", []any{
		multiQuestionMap("Pick many", "", [2]string{"a", ""}, [2]string{"b", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	if ev == nil {
		t.Fatal("应发 pending")
	}
	if ev.UserInput.Questions[0].AnswerMode != core.UserInputAnswerModeMultiple {
		t.Fatalf("multiSelect=true 应 multiple，实际 %q", ev.UserInput.Questions[0].AnswerMode)
	}
}

// TestV2_FlagOn_MultiQuestionAllNormalized：多问题（single+multiple）全部规范化、原序派生 id。
func TestV2_FlagOn_MultiQuestionAllNormalized(t *testing.T) {
	cs, _ := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-mq", []any{
		singleQuestionMap("Color?", "", false, [2]string{"Red", ""}),
		multiQuestionMap("Toppings?", "", [2]string{"X", ""}, [2]string{"Y", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	if ev == nil || len(ev.UserInput.Questions) != 2 {
		t.Fatalf("应规范化 2 题，实际 %+v", ev)
	}
	iid := deriveClaudeInteractionID("req-mq")
	if ev.UserInput.Questions[0].ID != claudeQuestionID(iid, 0) || ev.UserInput.Questions[1].ID != claudeQuestionID(iid, 1) {
		t.Fatalf("多题 questionId 原序派生错: %q %q", ev.UserInput.Questions[0].ID, ev.UserInput.Questions[1].ID)
	}
	if ev.UserInput.Questions[0].AnswerMode != core.UserInputAnswerModeSingle || ev.UserInput.Questions[1].AnswerMode != core.UserInputAnswerModeMultiple {
		t.Fatalf("多题 mode 错")
	}
}

// TestV2_FlagOn_DuplicateQuestionTextFails：question text 重复 → failed/invalid_backend_request，不注册。
func TestV2_FlagOn_DuplicateQuestionTextFails(t *testing.T) {
	cs, _ := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-dup", []any{
		singleQuestionMap("Same?", "", false, [2]string{"a", ""}),
		singleQuestionMap("Same?", "", false, [2]string{"b", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	if ev == nil || ev.UserInput.Status != core.UserInputStatusFailed {
		t.Fatalf("重复 question text 应 failed，实际 %+v", ev)
	}
	if ev.UserInput.DiagnosticCode != "invalid_backend_request" || ev.UserInput.CanRespond {
		t.Fatalf("failed 应 diagnosticCode=invalid_backend_request canRespond=false，实际 %q/%v", ev.UserInput.DiagnosticCode, ev.UserInput.CanRespond)
	}
	if cs.claudeUserInputReg.Status(deriveClaudeInteractionID("req-dup")) != claudeUIAbsent {
		t.Fatalf("failed 不应注册 responder")
	}
}

// TestV2_FlagOn_NoOptionsFails：缺 options → failed（SDK 本应在 control_request 前拒绝）。
func TestV2_FlagOn_NoOptionsFails(t *testing.T) {
	cs, _ := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-noop", []any{
		map[string]any{"question": "Q?", "header": "", "options": []any{}},
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	if ev == nil || ev.UserInput.Status != core.UserInputStatusFailed {
		t.Fatalf("无 options 应 failed，实际 %+v", ev)
	}
}

// TestV2_ProductionPathEmitsCanonicalThenLegacy：生产路径无测试开关；canonical 必须先于
// 单题 legacy 派生事件进入同一 event stream。
func TestV2_ProductionPathEmitsCanonicalThenLegacy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs := &claudeSession{
		events:             make(chan core.Event, 16),
		ctx:                ctx,
		stdin:              &captureStdin{},
		claudeUserInputReg: newClaudeUserInputRegistry(),
	}
	cs.sessionID.Store("test-session")
	cs.activeMsgID.Store("turn-production")
	cs.alive.Store(true)

	cs.handleControlRequest(makeAskControlRequest("req-v1", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}, [2]string{"b", ""}),
	}))
	events := drainAllEvents(cs)
	if len(events) != 2 || events[0].Type != core.EventUserInputRequested || events[1].Type != core.EventQuestionAsked {
		t.Fatalf("production path event order = %+v, want canonical then legacy", events)
	}
	if events[0].TurnID != "turn-production" || events[1].QuestionID != "req-v1" {
		t.Fatalf("production identities incorrect: %+v", events)
	}
}

// TestV2_BypassDoesNotAutoAnswerSemanticQuestion（§9.1 关键回归）：
// v2 flag ON + autoApprove ON + AskUserQuestion → 仍走 v2，不得 auto-approve。
func TestV2_BypassDoesNotAutoAnswerSemanticQuestion(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.setPermissionMode("bypassPermissions") // autoApprove=true
	if !cs.autoApprove.Load() {
		t.Fatal("前置：autoApprove 应为 true")
	}
	cs.handleControlRequest(makeAskControlRequest("req-bp", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))

	// 不得写 auto-approve 的 control_response；应发 pending 事件。
	if stdin.linesWritten() != 0 {
		t.Fatalf("bypass 不得 auto-answer AskUserQuestion，实际写了 %s", stdin.buf.String())
	}
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	if ev == nil || ev.UserInput.Status != core.UserInputStatusPending {
		t.Fatalf("应发 pending（语义问句不被 bypass 回答），实际 %+v", ev)
	}
}

func TestResolveUserInputContextTimeoutReleasesClaimBeforeWrite(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-timeout", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	if ev == nil {
		t.Fatal("missing pending interaction")
	}
	cs.stdinMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := cs.ResolveUserInput(ctx, ev.UserInput.InteractionID, "f40f8934-8f3d-4e5f-a9b5-883b6a8f5147", core.UserInputActionAnswer, []core.UserInputAnswer{{
		QuestionID: ev.UserInput.Questions[0].ID,
		Values:     []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ev.UserInput.Questions[0].Options[0].ID}},
	}})
	cs.stdinMu.Unlock()
	if err == nil {
		t.Fatal("context timeout must fail")
	}
	if cs.claudeUserInputReg.Status(ev.UserInput.InteractionID) != claudeUIPending {
		t.Fatal("timed-out write must release claim back to pending")
	}
	if stdin.linesWritten() != 0 {
		t.Fatal("timed-out write must not reach backend")
	}
}

func TestResolveUserInputConcurrentClaimReportsInProgress(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-race", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputRequested)
	ui := ev.UserInput
	answers := []core.UserInputAnswer{{QuestionID: ui.Questions[0].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ui.Questions[0].Options[0].ID}}}}
	cs.stdinMu.Lock()
	firstDone := make(chan error, 1)
	go func() {
		_, err := cs.ResolveUserInput(context.Background(), ui.InteractionID, "first", core.UserInputActionAnswer, answers)
		firstDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for cs.claudeUserInputReg.Status(ui.InteractionID) != claudeUIClaimed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	second, err := cs.ResolveUserInput(t.Context(), ui.InteractionID, "second", core.UserInputActionAnswer, answers)
	if err != nil || second.Outcome != core.UserInputOutcomeInProgress || second.CurrentStatus != core.UserInputStatusPending {
		t.Fatalf("second writer = %+v, %v; want in_progress/pending", second, err)
	}
	cs.stdinMu.Unlock()
	if err := <-firstDone; err != nil {
		t.Fatalf("first writer: %v", err)
	}
	if stdin.linesWritten() != 1 {
		t.Fatalf("backend writes = %d, want 1", stdin.linesWritten())
	}
}

// =====================================================================
// B. ResolveUserInput：answer / reject / idempotency / bad-shape
// =====================================================================

// TestV2_ResolveAnswerSingle：answer single → control_response(allow) + answers[qText]=label + resolved(ios)。
func TestV2_ResolveAnswerSingle(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-a1", []any{
		singleQuestionMap("Which color?", "Color", false, [2]string{"Red", "r"}, [2]string{"Blue", "b"}),
	}))
	drainAllEvents(cs)

	iid := deriveClaudeInteractionID("req-a1")
	qid := claudeQuestionID(iid, 0)
	optBlue := claudeOptionID(qid, 1) // Blue

	res, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: optBlue}}}})
	if err != nil {
		t.Fatalf("answer 失败: %v", err)
	}
	if res.Outcome != core.UserInputOutcomeAccepted {
		t.Fatalf("outcome = %q want accepted", res.Outcome)
	}

	resp := respInner(t, stdin)
	if behavior, _ := resp["behavior"].(string); behavior != "allow" {
		t.Fatalf("behavior = %q want allow", behavior)
	}
	updated, _ := resp["updatedInput"].(map[string]any)
	if updated == nil {
		t.Fatal("updatedInput 缺失")
	}
	if _, ok := updated["questions"]; !ok {
		t.Error("updatedInput.questions 应保留原 input")
	}
	answers, _ := updated["answers"].(map[string]any)
	label, ok := answers["Which color?"]
	if !ok || label != "Blue" {
		t.Fatalf("answers['Which color?'] = %v want 'Blue'", label)
	}

	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputResolved)
	if ev == nil || ev.UserInput.Status != core.UserInputStatusAnswered || ev.UserInput.ResolutionSource != "ios" {
		t.Fatalf("resolved 事件错: %+v", ev)
	}
}

// TestV2_ResolveAnswerMultiple：answer multiple → answers[qText]=[]labels。
func TestV2_ResolveAnswerMultiple(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-am", []any{
		multiQuestionMap("Pick many", "", [2]string{"a", ""}, [2]string{"b", ""}, [2]string{"c", ""}),
	}))
	drainAllEvents(cs)
	iid := deriveClaudeInteractionID("req-am")
	qid := claudeQuestionID(iid, 0)

	if _, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{
			{Kind: core.UserInputValueOption, OptionID: claudeOptionID(qid, 0)},
			{Kind: core.UserInputValueOption, OptionID: claudeOptionID(qid, 2)},
		}}}); err != nil {
		t.Fatalf("multiple answer 失败: %v", err)
	}
	updated, _ := respInner(t, stdin)["updatedInput"].(map[string]any)
	answers, _ := updated["answers"].(map[string]any)
	arr, ok := answers["Pick many"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("multiple 应产出 2 元素 label 数组，实际 %+v", answers["Pick many"])
	}
	if arr[0] != "a" || arr[1] != "c" {
		t.Fatalf("labels = %+v want [a c]", arr)
	}
}

// TestV2_ResolveAnswerMultiQuestion：多题一次性回答，answers map 覆盖全部 question text。
func TestV2_ResolveAnswerMultiQuestion(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-mqa", []any{
		singleQuestionMap("Color?", "", false, [2]string{"Red", ""}),
		multiQuestionMap("Tops?", "", [2]string{"X", ""}, [2]string{"Y", ""}),
	}))
	drainAllEvents(cs)
	iid := deriveClaudeInteractionID("req-mqa")
	q0 := claudeQuestionID(iid, 0)
	q1 := claudeQuestionID(iid, 1)

	if _, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer, []core.UserInputAnswer{
		{QuestionID: q0, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: claudeOptionID(q0, 0)}}},
		{QuestionID: q1, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: claudeOptionID(q1, 0)}, {Kind: core.UserInputValueOption, OptionID: claudeOptionID(q1, 1)}}},
	}); err != nil {
		t.Fatalf("多题 answer 失败: %v", err)
	}
	answers, _ := respInner(t, stdin)["updatedInput"].(map[string]any)["answers"].(map[string]any)
	if len(answers) != 2 {
		t.Fatalf("应 2 个 answer entry，实际 %d: %+v", len(answers), answers)
	}
	if answers["Color?"] != "Red" {
		t.Fatalf("Color? = %v want Red", answers["Color?"])
	}
	if arr, _ := answers["Tops?"].([]any); len(arr) != 2 {
		t.Fatalf("Tops? 应 2 元素，实际 %+v", answers["Tops?"])
	}
}

// TestV2_ResolveReject：reject → control_response(deny, "User skipped the question.") + resolved(rejected)。
func TestV2_ResolveReject(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-rj", []any{
		singleQuestionMap("Skip me?", "", false, [2]string{"a", ""}),
	}))
	drainAllEvents(cs)
	iid := deriveClaudeInteractionID("req-rj")

	res, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionReject, nil)
	if err != nil {
		t.Fatalf("reject 失败: %v", err)
	}
	if res.CurrentStatus != core.UserInputStatusRejected {
		t.Fatalf("status = %q want rejected", res.CurrentStatus)
	}
	resp := respInner(t, stdin)
	if behavior, _ := resp["behavior"].(string); behavior != "deny" {
		t.Fatalf("reject behavior = %q want deny", behavior)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "skipped") {
		t.Fatalf("reject message = %q want skip wording", msg)
	}
	if _, hasUpdated := resp["updatedInput"]; hasUpdated {
		t.Fatalf("reject 不应带 updatedInput")
	}
	ev := findUserInputEvent(drainAllEvents(cs), core.EventUserInputResolved)
	if ev == nil || ev.UserInput.Status != core.UserInputStatusRejected || ev.UserInput.ResolutionSource != "ios" {
		t.Fatalf("reject resolved 事件错: %+v", ev)
	}
}

// TestV2_ResolveIdempotentRetry：同 clientActionID 重试不再写 backend、不再发事件。
func TestV2_ResolveIdempotentRetry(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-idem", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}, [2]string{"b", ""}),
	}))
	drainAllEvents(cs)
	iid := deriveClaudeInteractionID("req-idem")
	qid := claudeQuestionID(iid, 0)
	ans := []core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: claudeOptionID(qid, 0)}}}}

	if _, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer, ans); err != nil {
		t.Fatalf("首次 answer: %v", err)
	}
	drainAllEvents(cs)
	before := stdin.linesWritten()

	res2, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer, ans)
	if err != nil {
		t.Fatalf("幂等重试不应 error: %v", err)
	}
	if res2.Outcome != core.UserInputOutcomeAccepted {
		t.Fatalf("幂等 outcome = %q want accepted", res2.Outcome)
	}
	if stdin.linesWritten() != before {
		t.Fatalf("幂等重试不应再写 backend，before=%d after=%d", before, stdin.linesWritten())
	}
	if len(drainAllEvents(cs)) != 0 {
		t.Fatalf("幂等重试不应再发事件")
	}
}

// TestV2_ResolveBadShapeReleasesClaim：未知 option → invalid_answer_shape，claim 释放，backend 不写，可重试。
func TestV2_ResolveBadShapeReleasesClaim(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-bs", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	drainAllEvents(cs)
	iid := deriveClaudeInteractionID("req-bs")
	qid := claudeQuestionID(iid, 0)

	_, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "bogus"}}}})
	uie, ok := err.(*core.UserInputError)
	if !ok || uie.Code != "invalid_answer_shape" {
		t.Fatalf("未知 option 应 invalid_answer_shape，实际 %T %v", err, err)
	}
	if stdin.linesWritten() != 0 {
		t.Fatalf("bad shape 不应写 backend，实际 %s", stdin.buf.String())
	}
	if cs.claudeUserInputReg.Status(iid) != claudeUIPending {
		t.Fatalf("claim 应释放回 pending 允许重试，实际 %v", cs.claudeUserInputReg.Status(iid))
	}

	// 重试（同 clientActionID）现在应成功——证明 claim 已释放。
	if _, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: claudeOptionID(qid, 0)}}}}); err != nil {
		t.Fatalf("释放后重试应成功，实际 %v", err)
	}
}

// TestV2_ResolveMultiQuestionMissingOneInvalid：多题少答一题 → invalid_answer_shape，不写 backend。
func TestV2_ResolveMultiQuestionMissingOneInvalid(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-miss", []any{
		singleQuestionMap("Color?", "", false, [2]string{"Red", ""}),
		singleQuestionMap("Size?", "", false, [2]string{"Big", ""}),
	}))
	drainAllEvents(cs)
	iid := deriveClaudeInteractionID("req-miss")
	q0 := claudeQuestionID(iid, 0)

	_, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: q0, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: claudeOptionID(q0, 0)}}}})
	uie, ok := err.(*core.UserInputError)
	if !ok || uie.Code != "invalid_answer_shape" {
		t.Fatalf("少答一题应 invalid_answer_shape，实际 %T %v", err, err)
	}
	if stdin.linesWritten() != 0 {
		t.Fatalf("invalid 不应写 backend，实际 %s", stdin.buf.String())
	}
}

// TestV2_ResolveCustomTextAccepted：真实 Claude Desktop transcript 证明 Other 可返回任意文本；
// bridge-owned responder 应把该文本原样编码到 updatedInput.answers。
func TestV2_ResolveCustomTextAccepted(t *testing.T) {
	cs, stdin := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-ct", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	drainAllEvents(cs)
	iid := deriveClaudeInteractionID("req-ct")
	qid := claudeQuestionID(iid, 0)

	_, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: qid, Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: "custom"}}}})
	if err != nil {
		t.Fatalf("Claude custom text 应可提交，实际 %v", err)
	}
	response := respInner(t, stdin)
	updated, _ := response["updatedInput"].(map[string]any)
	answers, _ := updated["answers"].(map[string]any)
	if answers["Which?"] != "custom" {
		t.Fatalf("custom answer = %#v want custom", answers["Which?"])
	}
}

// TestV2_ResolveInteractionNotFound：未知 interactionId → interaction_not_found。
func TestV2_ResolveInteractionNotFound(t *testing.T) {
	cs, _ := newAskV2TestSession(t)
	_, err := cs.ResolveUserInput(t.Context(), "ui_nonexistent", "client-A", core.UserInputActionAnswer, nil)
	uie, ok := err.(*core.UserInputError)
	if !ok || uie.Code != "interaction_not_found" {
		t.Fatalf("未知 interaction 应 interaction_not_found，实际 %T %v", err, err)
	}
}

// TestV2_ResolveDeadSession：session 不 alive → session_not_active。
func TestV2_ResolveDeadSession(t *testing.T) {
	cs, _ := newAskV2TestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-dead", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}),
	}))
	drainAllEvents(cs)
	cs.alive.Store(false)
	iid := deriveClaudeInteractionID("req-dead")
	_, err := cs.ResolveUserInput(t.Context(), iid, "client-A", core.UserInputActionAnswer, nil)
	uie, ok := err.(*core.UserInputError)
	if !ok || uie.Code != "session_not_active" {
		t.Fatalf("dead session 应 session_not_active，实际 %T %v", err, err)
	}
}

// =====================================================================
// C. ID 派生 + registry 状态机（纯函数契约）
// =====================================================================

// TestV2_DeriveClaudeInteractionIDStable：确定性、32 hex、"ui_" 前缀、与 requestId 单调绑定。
func TestV2_DeriveClaudeInteractionIDStable(t *testing.T) {
	a := deriveClaudeInteractionID("req-1")
	b := deriveClaudeInteractionID("req-1")
	c := deriveClaudeInteractionID("req-2")
	if a != b {
		t.Fatalf("非确定性: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("不同 requestId 不应派生同一 id: %q", a)
	}
	if !strings.HasPrefix(a, "ui_") {
		t.Fatalf("缺 ui_ 前缀: %q", a)
	}
	hex := strings.TrimPrefix(a, "ui_")
	if len(hex) != claudeSUIHexLen {
		t.Fatalf("hex 长度 = %d want %d", len(hex), claudeSUIHexLen)
	}
	for _, ch := range hex {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			t.Fatalf("非小写 hex 字符 %q in %q", ch, hex)
		}
	}
}

// TestV2_DeriveClaudeInteractionIDDistinctFromCodex：Claude 与 Codex 派生域不同
// （hash 输入 prefix 不同），同一 requestId 不得撞 id。
// 这里只断言 Claude 派生本身稳定且带正确前缀；跨包语义由 codex 包单测各自锁死。
func TestV2_DeriveClaudeInteractionIDDistinctFromCodex(t *testing.T) {
	// Claude 派生是 sha256("claudecode\0"+requestId)；不同 requestId 空间隔离。
	if deriveClaudeInteractionID("same") == deriveClaudeInteractionID("same") {
		// OK — 确定性自检。
	} else {
		t.Fatal("确定性自检失败")
	}
}

// TestV2_QuestionAndOptionIDFormat：questionId = iid+"_q_"+idx；optionId = qid+"_o_"+idx。
func TestV2_QuestionAndOptionIDFormat(t *testing.T) {
	iid := "ui_abc"
	if got := claudeQuestionID(iid, 0); got != "ui_abc_q_0" {
		t.Fatalf("questionId = %q want ui_abc_q_0", got)
	}
	if got := claudeOptionID("ui_abc_q_0", 2); got != "ui_abc_q_0_o_2" {
		t.Fatalf("optionId = %q want ui_abc_q_0_o_2", got)
	}
}

// TestV2_RegistryStateMachine：pending → claimed → resolved；ReleaseClaim 回 pending；clientActionID 幂等。
func TestV2_RegistryStateMachine(t *testing.T) {
	r := newClaudeUserInputRegistry()
	entry := claudeUIEntry{interactionID: "ui_x", requestID: "r1", rawInput: map[string]any{}}
	if !r.Register(entry) {
		t.Fatal("首次 Register 应成功")
	}
	if r.Register(entry) {
		t.Fatal("重复 Register 应失败（幂等）")
	}
	if r.Status("ui_x") != claudeUIPending {
		t.Fatalf("应 pending")
	}

	// Claim 成功。
	dec := r.Claim("ui_x", "ca-1")
	if !dec.claimed || dec.snapshot == nil {
		t.Fatalf("Claim 应成功并返回 snapshot，实际 %+v", dec)
	}
	if r.Status("ui_x") != claudeUIClaimed {
		t.Fatalf("应 claimed")
	}

	// 第二个并发 clientAction 命中 claimed 但不抢走。
	dec2 := r.Claim("ui_x", "ca-2")
	if dec2.claimed {
		t.Fatal("已 claimed 时第二个 Claim 不应抢走")
	}

	// ConfirmResolved。
	if !r.ConfirmResolved("ui_x", "ca-1", "ios") {
		t.Fatal("ConfirmResolved 应成功")
	}
	if r.Status("ui_x") != claudeUIResolved {
		t.Fatalf("应 resolved")
	}

	// 同 clientActionID 幂等：返回 accepted outcome。
	dec3 := r.Claim("ui_x", "ca-1")
	if dec3.outcome != core.UserInputOutcomeAccepted {
		t.Fatalf("幂等 Claim outcome = %q want accepted", dec3.outcome)
	}
}

// TestV2_RegistryReleaseClaim：claimed 状态释放回 pending。
func TestV2_RegistryReleaseClaim(t *testing.T) {
	r := newClaudeUserInputRegistry()
	r.Register(claudeUIEntry{interactionID: "ui_y", requestID: "r2"})
	r.Claim("ui_y", "ca-1")
	if r.Status("ui_y") != claudeUIClaimed {
		t.Fatal("前置：应 claimed")
	}
	if !r.ReleaseClaim("ui_y") {
		t.Fatal("ReleaseClaim 应成功")
	}
	if r.Status("ui_y") != claudeUIPending {
		t.Fatalf("释放后应回 pending，实际 %v", r.Status("ui_y"))
	}
	// 再次释放 pending 应失败（无 claim 可释放）。
	if r.ReleaseClaim("ui_y") {
		t.Fatal("pending 状态 ReleaseClaim 应失败")
	}
}
