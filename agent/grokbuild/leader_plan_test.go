package grokbuild

// Follower exit_plan_mode (plan approval) tests for the leader subscriber
// (design §25 — owner ruling 2026-09-03 opened the observe-only gate).
//
// Wire shapes frozen from grok-build exit_plan_mode/types.rs @72a61251 and its
// official round-trip tests: request camelCase {sessionId, toolCallId,
// planContent?} on the half-wrapped _x.ai/ method; response {outcome:
// "approved"|"cancelled", feedback?} with feedback only on typed-cancel (the
// TUI freeform path, which the bridge card does not have).

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
