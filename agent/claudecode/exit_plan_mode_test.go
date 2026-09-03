package claudecode

// exit_plan_mode_test.go —— 计划审批层 Phase 2b（方案 §4.1/§4.3/Phase 2b）：
// Claude ExitPlanMode 经 can_use_tool 管道升级 plan_review 专用卡 + deny.message
// 按 planAction 区分文案。
//
// fixture 纪律：testdata/exit_plan_mode_control_request.json 取自本机
// ~/.claude/projects 真实 transcript 的 ExitPlanMode tool_use（调研档 §3.2 实测
// 样本，7.5KB 级），脱敏（家目录→/Users/dev、仓内提交哈希→占位、plan 文件名→
// example-plan.md）后入仓；结构（三字段 input、markdown 全文、allowedPrompts）
// 与真实样本一致。

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func loadExitPlanModeFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/exit_plan_mode_control_request.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var frame map[string]any
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return frame
}

// TestExitPlanModeEmitsPlanReviewCard：ExitPlanMode control_request 升级为
// plan_review 卡——kind/三动作/plan 全文与 planFilePath 落载荷；卡面摘要 =
// planFilePath（不再是整份 input 的 JSON 序列化把全文挤进 toolInput）。
func TestExitPlanModeEmitsPlanReviewCard(t *testing.T) {
	cs, _ := newAskTestSession(t)
	cs.handleControlRequest(loadExitPlanModeFixture(t))

	ev := drainLegacyEvent(t, cs)
	if ev == nil || ev.Type != core.EventPermissionRequest {
		t.Fatalf("event = %+v, want permission_request", ev)
	}
	if ev.RequestID != "req_plan_fixture_1" || ev.ToolName != "ExitPlanMode" {
		t.Fatalf("card ids = %+v", ev)
	}
	if ev.PermissionKind != "plan_review" {
		t.Fatalf("PermissionKind = %q, want plan_review", ev.PermissionKind)
	}
	if len(ev.PermissionActions) != 3 || ev.PermissionActions[0] != "approve" ||
		ev.PermissionActions[1] != "requestChanges" || ev.PermissionActions[2] != "quit" {
		t.Fatalf("PermissionActions = %+v, want [approve requestChanges quit]", ev.PermissionActions)
	}
	if ev.PlanReview == nil {
		t.Fatal("PlanReview payload missing")
	}
	if len(ev.PlanReview.Content) < 7*1024 {
		t.Fatalf("plan content = %d bytes, want multi-KB real-scale", len(ev.PlanReview.Content))
	}
	if ev.PlanReview.PlanFilePath != "/Users/dev/.claude/plans/example-plan.md" {
		t.Fatalf("PlanFilePath = %q", ev.PlanReview.PlanFilePath)
	}
	// 卡面摘要走 summarizeInput 的 ExitPlanMode 分支（planFilePath，非全文 JSON）。
	if ev.ToolInput != "/Users/dev/.claude/plans/example-plan.md" {
		t.Fatalf("ToolInput = %q, want planFilePath summary", ev.ToolInput)
	}
	// 旧客户端面：ToolInputRaw 仍带完整 input（含 plan 原文），数据不丢。
	rawPlan, _ := ev.ToolInputRaw["plan"].(string)
	if rawPlan != ev.PlanReview.Content {
		t.Fatalf("ToolInputRaw.plan diverged from PlanReview.Content (%d vs %d bytes)", len(rawPlan), len(ev.PlanReview.Content))
	}
}

// TestRespondPermissionPlanVocabulary：plan 审批应答按 planAction 区分——D5 纯
// allow（无 updatedPermissions）；requestChanges 带反馈→deny+message=反馈；空反馈
// 与 quit 各用固定文案（claude wire 上两动作同为 deny，差异只在 message，调研档
// §3.5）；无 planAction 的 legacy deny 保持原默认文案。
func TestRespondPermissionPlanVocabulary(t *testing.T) {
	cs, stdin := newAskTestSession(t)

	// approve（D5 纯 allow）。
	if err := cs.RespondPermission("req-p1", core.PermissionResult{Behavior: "allow", PlanAction: "approve"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	resp := stdin.lastJSONLine(t)
	inner := resp["response"].(map[string]any)["response"].(map[string]any)
	if inner["behavior"] != "allow" {
		t.Fatalf("approve inner = %+v", inner)
	}
	if _, has := inner["updatedPermissions"]; has {
		t.Fatalf("D5 violation: approve must not carry updatedPermissions: %+v", inner)
	}

	// requestChanges + 反馈 → deny + message=反馈（官方反馈回模型通道）。
	if err := cs.RespondPermission("req-p2", core.PermissionResult{Behavior: "deny", PlanAction: "requestChanges", Message: "第二步改成并行"}); err != nil {
		t.Fatalf("requestChanges: %v", err)
	}
	resp = stdin.lastJSONLine(t)
	inner = resp["response"].(map[string]any)["response"].(map[string]any)
	if inner["behavior"] != "deny" || inner["message"] != "第二步改成并行" {
		t.Fatalf("requestChanges inner = %+v, want deny + feedback message", inner)
	}

	// requestChanges 空反馈 → 固定文案（SDK deny.message 必填）。
	if err := cs.RespondPermission("req-p3", core.PermissionResult{Behavior: "deny", PlanAction: "requestChanges"}); err != nil {
		t.Fatalf("requestChanges(empty): %v", err)
	}
	resp = stdin.lastJSONLine(t)
	inner = resp["response"].(map[string]any)["response"].(map[string]any)
	if inner["behavior"] != "deny" || inner["message"] != "The user rejected the plan and asked to keep planning. No specific feedback was provided." {
		t.Fatalf("requestChanges(empty) inner = %+v", inner)
	}

	// quit → deny + 固定 dismissed 文案。
	if err := cs.RespondPermission("req-p4", core.PermissionResult{Behavior: "deny", PlanAction: "quit"}); err != nil {
		t.Fatalf("quit: %v", err)
	}
	resp = stdin.lastJSONLine(t)
	inner = resp["response"].(map[string]any)["response"].(map[string]any)
	if inner["behavior"] != "deny" || inner["message"] != "The user dismissed the plan review." {
		t.Fatalf("quit inner = %+v", inner)
	}

	// legacy deny（无 planAction）→ 原默认文案不变。
	if err := cs.RespondPermission("req-p5", core.PermissionResult{Behavior: "deny"}); err != nil {
		t.Fatalf("legacy deny: %v", err)
	}
	resp = stdin.lastJSONLine(t)
	inner = resp["response"].(map[string]any)["response"].(map[string]any)
	if inner["message"] != "The user denied this tool use. Stop and wait for the user's instructions." {
		t.Fatalf("legacy deny inner = %+v, default copy drifted", inner)
	}
}

// TestSummarizeInputExitPlanMode：ExitPlanMode 摘要分支——planFilePath 优先，
// 缺失时回落固定短文案；不再序列化整份 input。
func TestSummarizeInputExitPlanMode(t *testing.T) {
	if got := summarizeInput("ExitPlanMode", map[string]any{
		"plan":         "# Plan\n正文",
		"planFilePath": "/Users/dev/.claude/plans/x.md",
	}); got != "/Users/dev/.claude/plans/x.md" {
		t.Fatalf("summarize with path = %q", got)
	}
	if got := summarizeInput("ExitPlanMode", map[string]any{"plan": "# Plan"}); got != "plan approval" {
		t.Fatalf("summarize without path = %q", got)
	}
}
