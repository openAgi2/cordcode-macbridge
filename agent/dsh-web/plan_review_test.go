package dshweb

// plan_review_test.go —— 计划审批层 Phase 2c（方案 §4.1/§4.3/Phase 2c）：
// dsh plan-review intent question 改走 permission 面（plan_review 卡 + 三动作），
// 应答按 planAction 翻译（Approve label / Keep-planning+custom / reject=dismiss）。
//
// fixture 纪律：question 形状与 intent 不变量锚定安装版 dsh-v0.1.1-rc.2 官方源码
// —— apiproxy/src/api/events.schema.ts askUserQuestionItemSchema（intent 是
// discriminatedUnion，仅 plan-review 一种，approve 必填 string；未知 kind 整帧拒绝）
// 与 user-questions.spec.ts（approve label 必须指名自己的 option；detail 即 plan
// 全文；混合 batch 中 plain+plan-review 并存）。应答读取语义锚定
// plan/plan-mode/src/index.ts（selected[0]===approve 且无 custom 才算批准）。

import (
	"context"
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

func questionFrame(sessionID string, questions ...map[string]any) json.RawMessage {
	qs := make([]any, 0, len(questions))
	for _, q := range questions {
		qs = append(qs, q)
	}
	return mustJSON(map[string]any{"sessionId": sessionID, "questions": qs})
}

// TestPlanReviewQuestionSurfacesPermissionCard：混合 batch（官方 spec 的
// plain+plan-review 并存用例）——plan-review question 升级 plan_review 权限卡
// （kind/三动作/detail=plan 全文），不 emit user_input/question_asked；同批
// plain question 走原 user_input 面不受影响。
func TestPlanReviewQuestionSurfacesPermissionCard(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-plan")

	plan := "# 修复登录流程\n\n1. 校验回调 state\n2. 写入 session cookie\n3. 回归测试"
	a.handleQuestionFrame(context.Background(), "rpc-plan-1", "question/requested", questionFrame("sess-plan",
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
// 反馈无 custom 字段）；quit 走 reject 错误分支（=dismiss/ASK_CANCELLED，D3）；
// legacy 二值 allow 也落 Approve label。
func TestPlanReviewAnswerVocabulary(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-plan-v")
	f.handlers["/api/respond"] = fakeRPCResponse{}

	plan := "# 计划\n\n1. 步骤一\n2. 步骤二"
	answerAndDrain := func(rpcID string, result core.PermissionResult) map[string]any {
		t.Helper()
		a.handleQuestionFrame(context.Background(), rpcID, "question/requested",
			questionFrame("sess-plan-v", planReviewQuestionFixture(plan)))
		drainOf(t, sess.Events(), core.EventPermissionRequest, "plan card")
		if err := sess.RespondPermission("plan-review", result); err != nil {
			t.Fatalf("RespondPermission(%+v): %v", result, err)
		}
		f.lastRespond.mu.Lock()
		body := f.lastRespond.body
		f.lastRespond.mu.Unlock()
		var sent struct {
			Type   string        `json:"type"`
			RPCID  string        `json:"rpcId"`
			Result rpcResultBody `json:"result"`
		}
		if err := json.Unmarshal(body, &sent); err != nil {
			t.Fatal(err)
		}
		if sent.Type != "client-response" || sent.RPCID != rpcID {
			t.Fatalf("respond envelope: %+v", sent)
		}
		if !sent.Result.OK {
			t.Fatalf("vocabulary answer must ride the value branch: %+v", sent.Result)
		}
		var val struct {
			Answer struct {
				Answers []struct {
					ID       string   `json:"id"`
					Selected []string `json:"selected"`
					Custom   string   `json:"custom"`
				} `json:"answers"`
			} `json:"answer"`
		}
		if err := json.Unmarshal(sent.Result.Value, &val); err != nil {
			t.Fatal(err)
		}
		if len(val.Answer.Answers) != 1 || val.Answer.Answers[0].ID != "plan-review" {
			t.Fatalf("answers = %+v", val.Answer.Answers)
		}
		return map[string]any{
			"selected": val.Answer.Answers[0].Selected,
			"custom":   val.Answer.Answers[0].Custom,
			"rawValue": sent.Result.Value,
		}
	}

	// approve → selected=[Approve]，无 custom。
	got := answerAndDrain("rpc-pv-approve", core.PermissionResult{Behavior: "allow", PlanAction: "approve"})
	if got["selected"].([]string)[0] != "Approve" || strings.Contains(string(got["rawValue"].(json.RawMessage)), `"custom"`) {
		t.Fatalf("approve = %+v", got)
	}

	// requestChanges + 反馈 → selected=[Keep planning] + custom=反馈。
	got = answerAndDrain("rpc-pv-rc", core.PermissionResult{Behavior: "deny", PlanAction: "requestChanges", Message: "第二步改成并行"})
	if got["selected"].([]string)[0] != "Keep planning" || got["custom"].(string) != "第二步改成并行" {
		t.Fatalf("requestChanges = %+v", got)
	}

	// requestChanges 空反馈 → Keep planning，无 custom 字段。
	got = answerAndDrain("rpc-pv-rc-empty", core.PermissionResult{Behavior: "deny", PlanAction: "requestChanges"})
	if got["selected"].([]string)[0] != "Keep planning" || strings.Contains(string(got["rawValue"].(json.RawMessage)), `"custom"`) {
		t.Fatalf("requestChanges(empty) = %+v", got)
	}

	// legacy allow（旧客户端二值卡）→ Approve label。
	got = answerAndDrain("rpc-pv-legacy", core.PermissionResult{Behavior: "allow"})
	if got["selected"].([]string)[0] != "Approve" {
		t.Fatalf("legacy allow = %+v", got)
	}

	// quit → reject 错误分支（batch cancelled，非 value 分支）。
	a.handleQuestionFrame(context.Background(), "rpc-pv-quit", "question/requested",
		questionFrame("sess-plan-v", planReviewQuestionFixture(plan)))
	drainOf(t, sess.Events(), core.EventPermissionRequest, "plan card (quit)")
	if err := sess.RespondPermission("plan-review", core.PermissionResult{Behavior: "deny", PlanAction: "quit"}); err != nil {
		t.Fatalf("RespondPermission(quit): %v", err)
	}
	f.lastRespond.mu.Lock()
	body := f.lastRespond.body
	f.lastRespond.mu.Unlock()
	var sent struct {
		Result rpcResultBody `json:"result"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Result.OK {
		t.Fatalf("quit must ride the reject error branch: %+v", sent.Result)
	}
}

// TestPlanReviewResolvedClosesCard：question/resolved（web 先答）对 plan 卡发
// permission_resolved 而非 user_input resolution；注册表清理。
func TestPlanReviewResolvedClosesCard(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-plan-r")

	a.handleQuestionFrame(context.Background(), "rpc-plan-r", "question/requested",
		questionFrame("sess-plan-r", planReviewQuestionFixture("# 计划\n1. 步骤")))
	drainOf(t, sess.Events(), core.EventPermissionRequest, "plan card")

	a.handleQuestionFrame(context.Background(), "rpc-plan-r", "question/resolved",
		mustJSON(map[string]any{"sessionId": "sess-plan-r", "questionRpcId": "rpc-plan-r", "outcome": "answered"}))

	ev := drainOf(t, sess.Events(), core.EventPermissionResolved, "permission_resolved")
	if ev.RequestID != "plan-review" || ev.Content != "answered" {
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
	sess := boundTestSession(t, f, a, "sess-plan-bad")

	bad := planReviewQuestionFixture("# Plan")
	bad["intent"] = map[string]any{"kind": "plan-review", "approve": "Ship it"} // 不在 options 中
	a.handleQuestionFrame(context.Background(), "rpc-plan-bad", "question/requested",
		questionFrame("sess-plan-bad", bad))

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
