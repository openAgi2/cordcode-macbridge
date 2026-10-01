package gobridge

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// registry_requires_action_test.go —— 黄点耐久性（2026-10-01）定向测试：
// registry requiresAction 表达（转换族/outcomes 不动/isKnownActive）＋
// applyQuestionRegistrySync 词表接线＋列表快照携带＋session_state_changed
// (requiresAction) 不再降级 markRunning（codex-remote ThreadStatus 现存路径）。
// 方案：iOS 仓 docs/2026-10-01-question-badge-registry-requires-action-plan.md（v2，r2 APPROVED）。

func TestRequiresActionRegistryTransitions(t *testing.T) {
	r := newSessionRegistry()
	// 正常序列：turn_started 先行 → 挂起。
	r.markRunning("s-qa")
	r.markRequiresAction("s-qa")
	if ts, ok := r.get("s-qa"); !ok || ts.state != sessionStateRequiresAction {
		t.Fatalf("state after markRequiresAction = %v", ts.state)
	}
	if !r.isKnownActive("s-qa") {
		t.Fatal("requiresAction must count as known-active (turn in flight)")
	}
	// 边缘：question 先于任何 turn_started 观测（缺席创建）。
	r.markRequiresAction("s-absent")
	if ts, ok := r.get("s-absent"); !ok || ts.state != sessionStateRequiresAction {
		t.Fatalf("absent-entry state = %v", ts.state)
	}
	// 解除 → running；turn 终态 → idle 收口。
	r.markRunning("s-qa")
	if ts, _ := r.get("s-qa"); ts.state != sessionStateRunning {
		t.Fatalf("state after resolved = %v", ts.state)
	}
	r.markIdle("s-qa")
	if ts, _ := r.get("s-qa"); ts.state != sessionStateIdle {
		t.Fatalf("state after terminal = %v", ts.state)
	}
}

func TestRequiresActionKeepsOutcome(t *testing.T) {
	r := newSessionRegistry()
	settled := time.Now()
	r.markSettled("s-out", "failed", settled)
	r.markRequiresAction("s-out")
	if outcome, at := r.lastOutcomeFor("s-out"); outcome != "failed" || !at.Equal(settled) {
		t.Fatalf("markRequiresAction must not touch outcomes, got %q@%v", outcome, at)
	}
	// 对照：claimRunning（新 turn）清空结局——markRequiresAction 有意不清。
	r.markRunning("s-out")
	if outcome, _ := r.lastOutcomeFor("s-out"); outcome != "" {
		t.Fatalf("markRunning must clear outcome, got %q", outcome)
	}
}

func TestApplyQuestionRegistrySyncVocabulary(t *testing.T) {
	h := NewHandlers()
	h.applyQuestionRegistrySync("s-wire", "user_input_requested")
	if ts, ok := h.sessions.get("s-wire"); !ok || ts.state != sessionStateRequiresAction {
		t.Fatalf("user_input_requested state = %v", ts.state)
	}
	h.applyQuestionRegistrySync("s-wire", "question_asked")
	if ts, _ := h.sessions.get("s-wire"); ts.state != sessionStateRequiresAction {
		t.Fatalf("question_asked state = %v", ts.state)
	}
	h.applyQuestionRegistrySync("s-wire", "user_input_resolved")
	if ts, _ := h.sessions.get("s-wire"); ts.state != sessionStateRunning {
		t.Fatalf("user_input_resolved state = %v", ts.state)
	}
	h.applyQuestionRegistrySync("s-wire", "question_resolved")
	if ts, _ := h.sessions.get("s-wire"); ts.state != sessionStateRunning {
		t.Fatalf("question_resolved state = %v", ts.state)
	}
	// 无关词 no-op；permission 词不接线（A-2 冻结）。
	for _, word := range []string{"text_delta", "turn_started", "permission_request", "permission_resolved", ""} {
		h.applyQuestionRegistrySync("s-wire", word)
		if ts, _ := h.sessions.get("s-wire"); ts.state != sessionStateRunning {
			t.Fatalf("word %q must be no-op, state = %v", word, ts.state)
		}
	}
}

func TestListRuntimeStateCarriesRequiresAction(t *testing.T) {
	h := NewHandlers()
	h.sessions.markRequiresAction("s-list")
	mapped := h.applyListRuntimeState(map[string]interface{}{"id": "s-list"}, nil)
	if got, _ := mapped["runtimeState"].(string); got != "requiresAction" {
		t.Fatalf("list runtimeState = %q, want requiresAction", got)
	}
	// 既有语义不回归：running 仍为 running。
	h.sessions.markRunning("s-run")
	mapped = h.applyListRuntimeState(map[string]interface{}{"id": "s-run"}, nil)
	if got, _ := mapped["runtimeState"].(string); got != "running" {
		t.Fatalf("list runtimeState = %q, want running", got)
	}
}

func TestRelaySyncRequiresActionNotDowngraded(t *testing.T) {
	h := NewHandlers()
	h.applyRelayEventRegistrySync("s-relay", "session_state_changed", map[string]interface{}{"state": "requiresAction"})
	if ts, ok := h.sessions.get("s-relay"); !ok || ts.state != sessionStateRequiresAction {
		t.Fatalf("relay sync must keep requiresAction (no markRunning downgrade), got %v", ts.state)
	}
	h.applyRelayEventRegistrySync("s-relay", "session_state_changed", map[string]interface{}{"state": "running"})
	if ts, _ := h.sessions.get("s-relay"); ts.state != sessionStateRunning {
		t.Fatalf("relay sync running = %v", ts.state)
	}
	h.applyRelayEventRegistrySync("s-relay", "session_state_changed", map[string]interface{}{"state": "idle"})
	if ts, _ := h.sessions.get("s-relay"); ts.state != sessionStateIdle {
		t.Fatalf("relay sync idle = %v", ts.state)
	}
}

// 被动泵端到端：question 事件经 startPassiveSubscription → agentRelayRunningFor
// 门内 applyQuestionRegistrySync → registry；EventSessionState(requiresAction)
// 经同步块 markRequiresAction（不降级）。question_asked/resolved 与
// user_input_* 在接线词表中同款（直驱 helper 测试已覆盖四词）。
func TestPassivePumpQuestionRegistrySync(t *testing.T) {
	h := NewHandlers()
	events := make(chan core.Event, 3)
	events <- core.Event{Type: core.EventQuestionAsked, SessionID: "s-passive"}
	events <- core.Event{Type: core.EventQuestionResolved, SessionID: "s-passive"}
	events <- core.Event{Type: core.EventSessionState, SessionID: "s-passive2", SessionState: &core.SessionStateEvent{State: "requiresAction"}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go startPassiveSubscription(ctx, h, "opencode-web", replayFreeSubscriber{events: events}.Subscribe, true, nil)

	deadline := time.After(5 * time.Second)
	for {
		ts1, ok1 := h.sessions.get("s-passive")
		ts2, ok2 := h.sessions.get("s-passive2")
		if ok1 && ts1.state == sessionStateRunning && ok2 && ts2.state == sessionStateRequiresAction {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("passive pump did not sync registry: s-passive=%v s-passive2=%v", regState(h, "s-passive"), regState(h, "s-passive2"))
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func regState(h *Handlers, id string) sessionState {
	ts, ok := h.sessions.get(id)
	if !ok {
		return "<absent>"
	}
	return ts.state
}
