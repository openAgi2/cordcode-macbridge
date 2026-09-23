package dshweb

// plan_review_test.go —— 计划审批层 Phase 2c（方案 §4.1/§4.3/Phase 2c）：
// dsh plan-review intent question 改走 permission 面（plan_review 卡 + 三动作），
// 应答按 planAction 翻译（Approve label / Keep-planning+custom / reject=dismiss）。
//
// fixture 纪律：question 形状与 intent 不变量锚定官方源码（interaction/
// user-questions types.ts askUserQuestionItemSchema + plan-mode/src/index.ts
// @49a606bc：intent 按名指认 approve label，detail=plan 全文；approve label
// 必须指名自己的 option）。应答读取语义锚定 plan/plan-mode/src/index.ts
// （selected[0]===approve 且无 custom 才算批准）。waterfall 载荷锚定
// stream-protocol.ts（request 即 AskUserQuestionRequestEvent 的 JSON 投影）。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// planReviewQuestionFixture mirrors the official plan-mode ask（index.ts:300-314，
// @49a606bc）：intent 按名指认 approve label，detail=plan 全文（# 标题开头）。
func planReviewQuestionFixture(plan string) map[string]any {
	return map[string]any{
		"id":       "plan-review",
		"header":   "Plan review",
		"question": "Approve this plan and leave plan mode?",
		"detail":   plan,
		"options": []any{
			map[string]any{"label": "Approve", "description": "Leave plan mode; the plan is carried out from the next step."},
			map[string]any{"label": "Keep planning", "description": "Stay in plan mode; feedback goes back to the model."},
		},
		"intent": map[string]any{"kind": "plan-review", "approve": "Approve"},
	}
}

// planReviewWaterfall builds one user-questions/request waterfall carrying the
// given questions for sessionID (the agentId).
func planReviewWaterfall(eventID, sessionID string, questions ...map[string]any) remoteEventFrame {
	qs := make([]any, 0, len(questions))
	for _, q := range questions {
		qs = append(qs, q)
	}
	return remoteEventFrame{
		Type:    "waterfall",
		Event:   "user-questions/request",
		EventID: eventID,
		AgentID: sessionID,
		Request: mustJSON(map[string]any{"questions": qs}),
	}
}

// TestPlanReviewQuestionSurfacesPermissionCard：混合 batch（官方 spec 的
// plain+plan-review 并存用例）——plan-review question 升级 plan_review 权限卡
// （kind/三动作/detail=plan 全文），不 emit user_input/question_asked；同批
// plain question 走原 user_input 面不受影响。
func TestPlanReviewQuestionSurfacesPermissionCard(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-plan")

	plan := "# 修复登录流程\n\n1. 校验回调 state\n2. 写入 session cookie\n3. 回归测试"
	a.handleQuestionWaterfall(planReviewWaterfall("ev-plan-1", "sess-plan",
		map[string]any{"id": "plain-1", "question": "Proceed?"},
		planReviewQuestionFixture(plan),
	))

	// 发射顺序 = batch 顺序：plain 先（user_input + legacy question_asked），
	// plan-review 后（permission 面）。
	ui := drainOf(t, sess.Events(), core.EventUserInputRequested, "plain user_input")
	if ui.ItemID != "plain-1" {
		t.Fatalf("plain question = %+v", ui)
	}
	ev := drainOf(t, sess.Events(), core.EventPermissionRequest, "plan permission_request")
	if ev.RequestID != "plan-review" || ev.PermissionKind != "plan_review" {
		t.Fatalf("plan card = %+v", ev)
	}
	if len(ev.PermissionActions) != 3 || ev.PermissionActions[0] != "approve" ||
		ev.PermissionActions[1] != "requestChanges" || ev.PermissionActions[2] != "quit" {
		t.Fatalf("actions = %+v", ev.PermissionActions)
	}
	if ev.PlanReview == nil || ev.PlanReview.Content != plan {
		t.Fatalf("PlanReview = %+v, want detail as full plan", ev.PlanReview)
	}
	if ev.ToolName != "Plan review" {
		t.Fatalf("ToolName = %q, want question header", ev.ToolName)
	}

	a.approvals.mu.Lock()
	_, planRegistered := a.approvals.planReviews["plan-review"]
	a.approvals.mu.Unlock()
	if !planRegistered {
		t.Fatal("plan-review meta not registered for answer routing")
	}
}

// TestPlanReviewAnswerVocabulary：三动作翻译（方案 §4.3 dsh 列）——approve 选中
// intent 指名的 Approve label；requestChanges 选 Keep planning + custom=反馈（空
// 反馈无 custom 字段）；quit 走 rejected outcome（=dismiss/ASK_CANCELLED，D3）；
// legacy 二值 allow 也落 Approve label。
func TestPlanReviewAnswerVocabulary(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-plan-v")

	plan := "# 计划\n\n1. 步骤一\n2. 步骤二"
	answerAndDrain := func(eventID string, result core.PermissionResult) map[string]any {
		t.Helper()
		a.handleQuestionWaterfall(planReviewWaterfall(eventID, "sess-plan-v", planReviewQuestionFixture(plan)))
		drainOf(t, sess.Events(), core.EventPermissionRequest, "plan card")
		if err := sess.RespondPermission("plan-review", result); err != nil {
			t.Fatalf("RespondPermission(%+v): %v", result, err)
		}
		f.lastEventResult.mu.Lock()
		args := f.lastEventResult.args
		f.lastEventResult.mu.Unlock()
		if args.EventID != eventID || args.ClientID != "fake-client-1" {
			t.Fatalf("result correlation: %+v", args)
		}
		if args.Outcome.Kind != "result" {
			t.Fatalf("vocabulary answer must ride the result branch: %+v", args.Outcome)
		}
		var val struct {
			Answers []struct {
				ID       string   `json:"id"`
				Selected []string `json:"selected"`
				Custom   string   `json:"custom"`
			} `json:"answers"`
		}
		if err := jsonUnmarshal(args.Outcome.Value, &val); err != nil {
			t.Fatal(err)
		}
		if len(val.Answers) != 1 || val.Answers[0].ID != "plan-review" {
			t.Fatalf("answers = %+v", val.Answers)
		}
		return map[string]any{
			"selected": val.Answers[0].Selected,
			"custom":   val.Answers[0].Custom,
			"rawValue": args.Outcome.Value,
		}
	}

	// approve → selected=[Approve]，无 custom。
	got := answerAndDrain("ev-pv-approve", core.PermissionResult{Behavior: "allow", PlanAction: "approve"})
	if got["selected"].([]string)[0] != "Approve" || strings.Contains(string(got["rawValue"].(json.RawMessage)), `"custom"`) {
		t.Fatalf("approve = %+v", got)
	}

	// requestChanges + 反馈 → selected=[Keep planning] + custom=反馈。
	got = answerAndDrain("ev-pv-rc", core.PermissionResult{Behavior: "deny", PlanAction: "requestChanges", Message: "第二步改成并行"})
	if got["selected"].([]string)[0] != "Keep planning" || got["custom"].(string) != "第二步改成并行" {
		t.Fatalf("requestChanges = %+v", got)
	}

	// requestChanges 空反馈 → Keep planning，无 custom 字段。
	got = answerAndDrain("ev-pv-rc-empty", core.PermissionResult{Behavior: "deny", PlanAction: "requestChanges"})
	if got["selected"].([]string)[0] != "Keep planning" || strings.Contains(string(got["rawValue"].(json.RawMessage)), `"custom"`) {
		t.Fatalf("requestChanges(empty) = %+v", got)
	}

	// legacy allow（旧客户端二值卡）→ Approve label。
	got = answerAndDrain("ev-pv-legacy", core.PermissionResult{Behavior: "allow"})
	if got["selected"].([]string)[0] != "Approve" {
		t.Fatalf("legacy allow = %+v", got)
	}

	// quit → rejected outcome（batch dismissed，非 value 分支）。
	a.handleQuestionWaterfall(planReviewWaterfall("ev-pv-quit", "sess-plan-v", planReviewQuestionFixture(plan)))
	drainOf(t, sess.Events(), core.EventPermissionRequest, "plan card (quit)")
	if err := sess.RespondPermission("plan-review", core.PermissionResult{Behavior: "deny", PlanAction: "quit"}); err != nil {
		t.Fatalf("RespondPermission(quit): %v", err)
	}
	f.lastEventResult.mu.Lock()
	args := f.lastEventResult.args
	f.lastEventResult.mu.Unlock()
	if args.Outcome.Kind != "rejected" {
		t.Fatalf("quit must ride the rejected outcome: %+v", args.Outcome)
	}
}

// TestPlanReviewSettledClosesCard：settle（web 先答的 cancel 帧 / 本地应答）
// 对 plan 卡发 permission_resolved 而非 user_input resolution；注册表清理。
func TestPlanReviewSettledClosesCard(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-plan-r")

	a.handleQuestionWaterfall(planReviewWaterfall("ev-plan-r", "sess-plan-r", planReviewQuestionFixture("# 计划\n1. 步骤")))
	drainOf(t, sess.Events(), core.EventPermissionRequest, "plan card")

	// The web answers first: the Host cancels our waterfall.
	a.closePendingInteraction("ev-plan-r", "cancelled")

	ev := drainOf(t, sess.Events(), core.EventPermissionResolved, "permission_resolved")
	if ev.RequestID != "plan-review" {
		t.Fatalf("permission_resolved = %+v", ev)
	}
	assertNoEvent(t, sess.Events(), "user_input resolution for plan question")

	a.approvals.mu.Lock()
	_, planLeft := a.approvals.planReviews["plan-review"]
	_, ownerLeft := a.approvals.questionOwner["plan-review"]
	a.approvals.mu.Unlock()
	if planLeft || ownerLeft {
		t.Fatalf("registry not cleaned: plan=%v owner=%v", planLeft, ownerLeft)
	}
}

// TestPlanReviewDegradesOnBadIntent：intent.approve 未指名任何 option（官方
// BAD_INTENT 情形的纵深防御）→ 退化通用 question 卡，不注册 plan 路由。
func TestPlanReviewDegradesOnBadIntent(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	setMuxClientID(a, "fake-client-1")
	sess := boundTestSession(t, f, a, "sess-plan-bad")

	bad := planReviewQuestionFixture("# Plan")
	bad["intent"] = map[string]any{"kind": "plan-review", "approve": "Ship it"} // 不在 options 中
	a.handleQuestionWaterfall(planReviewWaterfall("ev-plan-bad", "sess-plan-bad", bad))

	ui := drainOf(t, sess.Events(), core.EventUserInputRequested, "degraded user_input card")
	if ui.ItemID != "plan-review" {
		t.Fatalf("degraded card = %+v", ui)
	}
	// 通用卡路径还带 legacy question_asked；两条都不是 permission 面。
	if ev := drainOf(t, sess.Events(), core.EventQuestionAsked, "legacy question_asked"); ev.QuestionID != "plan-review" {
		t.Fatalf("legacy card = %+v", ev)
	}
	assertNoEvent(t, sess.Events(), "permission_request for bad intent")

	a.approvals.mu.Lock()
	_, registered := a.approvals.planReviews["plan-review"]
	a.approvals.mu.Unlock()
	if registered {
		t.Fatal("bad intent must not register plan routing")
	}
}
