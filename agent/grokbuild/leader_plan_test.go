package grokbuild

// Follower exit_plan_mode (plan approval) tests for the leader subscriber
// (design §24 / §25 — owner ruling 2026-09-03 opened the observe-only gate;
// plan approval layer 2026-09-04 added the plan_review card + 3-action
// vocabulary).
//
// Wire shapes frozen from grok-build exit_plan_mode/types.rs @72a61251 and its
// official round-trip tests: request camelCase {sessionId, toolCallId,
// planContent?} on the half-wrapped _x.ai/ method; response {outcome:
// "approved"|"cancelled"|"abandoned", feedback?} with feedback only on
// cancelled-with-typed-feedback.

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const fixtureExitPlanMode = `{"jsonrpc":"2.0","id":9,"method":"_x.ai/exit_plan_mode","params":{"sessionId":"sess-1","toolCallId":"call_plan_9","planContent":"# 重构方案\n\n## 步骤\n1. 先做 A\n2. 再做 B"}}`

// TestLeaderSubscriberExitPlanModeSurfaced: the REQUEST registers on the wire
// axis and emits exactly one permission_request whose title carries the plan's
// first heading line — the iOS permission card renders it with zero changes.
func TestLeaderSubscriberExitPlanModeSurfaced(t *testing.T) {
	got, sub := runLeaderSubscriber(t, func(c net.Conn) error {
		if err := writeACPRequestRaw(c, fixtureExitPlanMode); err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
		return nil
	})
	perms := filterEvents(got, core.EventPermissionRequest)
	if len(perms) != 1 || len(got) != 1 {
		t.Fatalf("events = %+v, want exactly one permission_request", got)
	}
	if perms[0].RequestID != "9" || perms[0].ToolName != "计划审批: 重构方案" {
		t.Fatalf("plan card = %+v, want RequestID 9 / heading-derived title", perms[0])
	}
	// plan_review 卡面（方案 §4.1）：kind 命中、三动作全集、plan 全文与标题。
	if perms[0].PermissionKind != "plan_review" {
		t.Fatalf("PermissionKind = %q, want plan_review", perms[0].PermissionKind)
	}
	actions := perms[0].PermissionActions
	if len(actions) != 3 || actions[0] != "approve" || actions[1] != "requestChanges" || actions[2] != "quit" {
		t.Fatalf("PermissionActions = %+v, want [approve requestChanges quit]", actions)
	}
	if perms[0].PlanReview == nil || perms[0].PlanReview.Content != "# 重构方案\n\n## 步骤\n1. 先做 A\n2. 再做 B" {
		t.Fatalf("PlanReview = %+v, want full planContent", perms[0].PlanReview)
	}
	if perms[0].PlanReview.Title != "计划审批: 重构方案" {
		t.Fatalf("PlanReview.Title = %q, want heading-derived title", perms[0].PlanReview.Title)
	}
	if sub.interactions.len() != 1 {
		t.Fatalf("registry len = %d, want 1", sub.interactions.len())
	}
	entry, ok := sub.interactions.getByWire(9)
	if !ok || entry.kind != leaderKindPlan || entry.plan == nil || entry.plan.PlanContent == "" {
		t.Fatalf("byWire(9) = %+v ok=%v, want plan-kind entry with planContent", entry, ok)
	}
}

// TestLeaderSubscriberAnswerPlanApproved: iPhone 允许 answers {outcome:
// "approved"} with the original numeric id; the registry flushes and no
// permission_resolved emits (the bridge-level optimistic close owns it).
func TestLeaderSubscriberAnswerPlanApproved(t *testing.T) {
	got, _ := runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
		if err := writeACPRequestRaw(c, fixtureExitPlanMode); err != nil {
			return err
		}
		<-answered
		resp := readClientACPFrame(t, c)
		if fmt.Sprint(resp["id"]) != "9" {
			t.Errorf("answer id = %v, want original numeric id 9", resp["id"])
		}
		result, _ := resp["result"].(map[string]any)
		if result["outcome"] != "approved" {
			t.Errorf("result = %+v, want outcome approved", result)
		}
		if _, has := result["feedback"]; has {
			t.Errorf("result = %+v, approved must omit feedback", result)
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	}, func(sub *LeaderSubscriber) {
		resolved, err := sub.AnswerPermission("9", core.PermissionResult{Behavior: "allow"})
		if err != nil || !resolved {
			t.Errorf("AnswerPermission allow = (%v, %v), want (true, nil)", resolved, err)
		}
		if sub.interactions.len() != 0 {
			t.Errorf("registry len = %d after answer, want 0", sub.interactions.len())
		}
	})
	counts := countEventTypes(got)
	if counts[core.EventPermissionResolved] != 0 {
		t.Fatalf("permission_resolved count = %d, want 0 (optimistic close owns it)", counts[core.EventPermissionResolved])
	}
}

// TestLeaderSubscriberAnswerPlanCancelled: iPhone 拒绝 answers {outcome:
// "cancelled"} — "always" degrades to approved like the permission rail (there
// is no grok equivalent of always for plan approval).
func TestLeaderSubscriberAnswerPlanCancelled(t *testing.T) {
	_, _ = runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
		if err := writeACPRequestRaw(c, fixtureExitPlanMode); err != nil {
			return err
		}
		if err := writeACPRequestRaw(c, `{"jsonrpc":"2.0","id":10,"method":"_x.ai/exit_plan_mode","params":{"sessionId":"sess-1","toolCallId":"call_plan_10","planContent":"# 第二个计划"}}`); err != nil {
			return err
		}
		<-answered
		frames := []map[string]any{readClientACPFrame(t, c), readClientACPFrame(t, c)}
		for i, want := range []struct {
			id, outcome string
		}{{"9", "cancelled"}, {"10", "approved"}} {
			if fmt.Sprint(frames[i]["id"]) != want.id {
				t.Errorf("frame[%d] id = %v, want %s", i, frames[i]["id"], want.id)
			}
			result, _ := frames[i]["result"].(map[string]any)
			if result["outcome"] != want.outcome {
				t.Errorf("frame[%d] result = %+v, want outcome %s", i, result, want.outcome)
			}
		}
		return nil
	}, func(sub *LeaderSubscriber) {
		if resolved, err := sub.AnswerPermission("9", core.PermissionResult{Behavior: "reject"}); err != nil || !resolved {
			t.Errorf("reject = (%v, %v), want (true, nil)", resolved, err)
		}
		if resolved, err := sub.AnswerPermission("10", core.PermissionResult{Behavior: "always"}); err != nil || !resolved {
			t.Errorf("always degrade = (%v, %v), want (true, nil)", resolved, err)
		}
	})
}

// TestLeaderSubscriberAnswerPlanVocabulary: the plan-card 3-action vocabulary
// (方案 §4.3 翻译表，锚点 types.rs @72a61251 round-trip tests)——quit →
// {outcome:"abandoned"}（修正旧把一切非 allow 都归 cancelled 的语义偏差）；
// requestChanges → {outcome:"cancelled", feedback}，空反馈时无 feedback 字段。
func TestLeaderSubscriberAnswerPlanVocabulary(t *testing.T) {
	_, _ = runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
		for i, plan := range []string{"# 计划甲", "# 计划乙", "# 计划丙"} {
			if err := writeACPRequestRaw(c, fmt.Sprintf(`{"jsonrpc":"2.0","id":2%d,"method":"_x.ai/exit_plan_mode","params":{"sessionId":"sess-1","toolCallId":"call_plan_2%d","planContent":%q}}`, i, i, plan)); err != nil {
				return err
			}
		}
		<-answered
		frames := []map[string]any{readClientACPFrame(t, c), readClientACPFrame(t, c), readClientACPFrame(t, c)}
		byID := map[string]map[string]any{}
		for _, f := range frames {
			byID[fmt.Sprint(f["id"])] = f
		}
		// quit → abandoned（abandoned 无 feedback，官方类型）。
		r, _ := byID["20"]["result"].(map[string]any)
		if r["outcome"] != "abandoned" {
			t.Errorf("quit result = %+v, want outcome abandoned", r)
		}
		if _, has := r["feedback"]; has {
			t.Errorf("quit result = %+v, abandoned must omit feedback", r)
		}
		// requestChanges + 反馈 → cancelled + feedback。
		r, _ = byID["21"]["result"].(map[string]any)
		if r["outcome"] != "cancelled" || r["feedback"] != "第二步改成并行" {
			t.Errorf("requestChanges result = %+v, want cancelled + feedback", r)
		}
		// requestChanges 空反馈 → cancelled，无 feedback 字段（omitempty 语义）。
		r, _ = byID["22"]["result"].(map[string]any)
		if r["outcome"] != "cancelled" {
			t.Errorf("requestChanges(empty) result = %+v, want outcome cancelled", r)
		}
		if _, has := r["feedback"]; has {
			t.Errorf("requestChanges(empty) result = %+v, empty feedback must omit the field", r)
		}
		return nil
	}, func(sub *LeaderSubscriber) {
		if resolved, err := sub.AnswerPermission("20", core.PermissionResult{Behavior: "deny", PlanAction: "quit"}); err != nil || !resolved {
			t.Errorf("quit = (%v, %v), want (true, nil)", resolved, err)
		}
		if resolved, err := sub.AnswerPermission("21", core.PermissionResult{Behavior: "deny", PlanAction: "requestChanges", Message: "第二步改成并行"}); err != nil || !resolved {
			t.Errorf("requestChanges = (%v, %v), want (true, nil)", resolved, err)
		}
		if resolved, err := sub.AnswerPermission("22", core.PermissionResult{Behavior: "deny", PlanAction: "requestChanges"}); err != nil || !resolved {
			t.Errorf("requestChanges(empty) = (%v, %v), want (true, nil)", resolved, err)
		}
	})
}

// TestLeaderSubscriberPlanResolvedBroadcast: the TUI answers its plan modal
// first — the official interaction_resolved broadcast evicts the entry, emits
// permission_resolved (RequestID = the wire id), and the late iOS answer is a
// silent no-op.
func TestLeaderSubscriberPlanResolvedBroadcast(t *testing.T) {
	got, sub := runLeaderSubscriber(t, func(c net.Conn) error {
		if err := writeACPRequestRaw(c, fixtureExitPlanMode); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		return writeACPNotification(c, "_x.ai/session_notification", map[string]any{
			"sessionId": "sess-1",
			"update":    map[string]any{"sessionUpdate": "interaction_resolved", "tool_call_id": "call_plan_9"},
		})
	})
	resolvedEvents := filterEvents(got, core.EventPermissionResolved)
	if len(resolvedEvents) != 1 || resolvedEvents[0].RequestID != "9" || resolvedEvents[0].Content != "resolved" {
		t.Fatalf("permission_resolved = %+v, want one RequestID 9 / resolved", resolvedEvents)
	}
	if sub.interactions.len() != 0 {
		t.Fatalf("registry len = %d after resolution, want 0", sub.interactions.len())
	}
	if resolved, err := sub.AnswerPermission("9", core.PermissionResult{Behavior: "allow"}); err != nil || !resolved {
		t.Errorf("late AnswerPermission = (%v, %v), want (true, nil) silent", resolved, err)
	}
}

// TestPlanApprovalTitle: heading-stripped first line, 80-rune truncation, and
// the empty-plan fallback.
func TestPlanApprovalTitle(t *testing.T) {
	if got := planApprovalTitle("# 重构方案\n\n正文"); got != "计划审批: 重构方案" {
		t.Errorf("heading = %q", got)
	}
	if got := planApprovalTitle("\n\n   \n## 第二行才是标题\n正文"); got != "计划审批: 第二行才是标题" {
		t.Errorf("skip blank lines = %q", got)
	}
	long := strings.Repeat("长", 200)
	if got := planApprovalTitle(long); len([]rune(got)) != len([]rune("计划审批: "))+80 {
		t.Errorf("truncation length = %d", len([]rune(got)))
	}
	if got := planApprovalTitle(""); got != "计划审批 (Exit plan mode)" {
		t.Errorf("empty fallback = %q", got)
	}
}
