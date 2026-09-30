package gobridge

// session_last_outcome_test.go（docs/2026-09-30-session-list-status-badges-plan.md §7）：
// registry「上次执行结局」的词表归类、settle 路由（R1 send 层 / R3 relay 同步块 /
// R6 reconcile 闭包形状 / R7 coverage dead）、列表与单 session 叠加点下发。
// 有意不接的生产者（R5 重放/push-only、R8 兜底合成）无 settle 断言义务（负证据
// 收口不产生 settle 代码路径）。

import (
	"testing"
	"time"
)

func outcomeOf(t *testing.T, h *Handlers, sessionID string) (string, time.Time) {
	t.Helper()
	outcome, at := h.sessions.lastOutcomeFor(sessionID)
	return outcome, at
}

func assertOutcome(t *testing.T, h *Handlers, sessionID, want string) {
	t.Helper()
	got, _ := outcomeOf(t, h, sessionID)
	if got != want {
		t.Fatalf("session %s outcome = %q, want %q", sessionID, got, want)
	}
}

// TestMarkSettledOutcomes：词表逐值 + markRunning 清空 + markIdle 不清（§3.3 核心
// 不变量——大量 markIdle 调用点是执行态收口，不得抹掉已观测结局）。
func TestMarkSettledOutcomes(t *testing.T) {
	cases := []struct {
		event  string
		reason string
		want   string
	}{
		{"turn_completed", "task_complete", "completed"},
		{"turn_completed", "official_turn_status", "completed"},
		{"turn_completed", "user_interrupt", "completed"},
		{"turn_completed", "aborted", "completed"},
		{"turn_completed", "", "completed"},
		{"turn_error", "", "failed"},
		{"error", "provider_error", "failed"},
		{"turn_aborted", "user_interrupt", "completed"},
		{"turn_aborted", "turn_aborted", "completed"},
		{"turn_aborted", "official_turn_status", "completed"},
		{"turn_aborted", "leader_disconnect", "failed"},
		{"turn_aborted", "process_death", "failed"},
		{"turn_aborted", "some_future_reason", "failed"}, // fail-closed
		{"turn_aborted", "", "failed"},                    // 无 reason 未知 → failed
	}
	for i, tc := range cases {
		h := NewHandlers()
		sid := "sess-" + string(rune('a'+i))
		data := map[string]interface{}{"turnId": "t1"}
		if tc.reason != "" || tc.event == "turn_aborted" {
			// turn_aborted 的空 reason 是「字段缺席」与「空串」并存的两形态，都归 failed。
			if tc.reason != "" {
				data["reason"] = tc.reason
			}
		}
		h.settleTurnOutcomeFromEvent(sid, tc.event, data)
		assertOutcome(t, h, sid, tc.want)
	}

	// 非终态事件名与空 sessionID 不 settle。
	h := NewHandlers()
	h.settleTurnOutcomeFromEvent("s-nonterminal", "text_delta", map[string]interface{}{})
	h.settleTurnOutcomeFromEvent("s-nonterminal", "session_state_changed", map[string]interface{}{"state": "idle"})
	if got, _ := outcomeOf(t, h, "s-nonterminal"); got != "" {
		t.Fatalf("non-terminal event settled outcome = %q, want empty", got)
	}
	h.settleTurnOutcomeFromEvent("", "turn_error", nil)
	// markSettled 对非法 outcome 直接拒绝（session 从未被合法 settle 过）。
	h.sessions.markSettled("s-bad-outcome", "bogus", time.Now())
	if got, _ := outcomeOf(t, h, "s-bad-outcome"); got != "" {
		t.Fatalf("invalid outcome accepted: %q", got)
	}

	// markRunning 清空；markIdle 保留。
	h2 := NewHandlers()
	h2.settleTurnOutcomeFromEvent("s-lifecycle", "turn_error", nil)
	assertOutcome(t, h2, "s-lifecycle", "failed")
	h2.sessions.markIdle("s-lifecycle")
	assertOutcome(t, h2, "s-lifecycle", "failed") // markIdle 不清 outcome
	h2.sessions.markRunning("s-lifecycle")
	if got, _ := outcomeOf(t, h2, "s-lifecycle"); got != "" {
		t.Fatalf("markRunning did not clear outcome: %q", got)
	}
	// markSettled 在条目缺席时创建（与 markIdle 同纪律）。
	h3 := NewHandlers()
	h3.sessions.markSettled("s-fresh", "failed", time.Now())
	assertOutcome(t, h3, "s-fresh", "failed")
}

// TestSendLayerSettlesRegistryOutcome（R1）：走真实 sendSessionEvent /
// sendSessionEventWithPushIntentPreview 生产者路径，防「单测全绿而真机无❗」。
func TestSendLayerSettlesRegistryOutcome(t *testing.T) {
	h := NewHandlers()

	h.sendSessionEvent("s-r1a", "claude", "turn_completed", map[string]interface{}{"turnId": "t1", "reason": "task_complete"})
	assertOutcome(t, h, "s-r1a", "completed")

	h.sendSessionEventWithPushIntent("s-r1b", "claude", "turn_error", map[string]interface{}{"turnId": "t2", "message": "boom"})
	assertOutcome(t, h, "s-r1b", "failed")

	h.sendSessionEventWithPushIntentPreview("s-r1c", "codex", "turn_aborted", map[string]interface{}{"turnId": "t3", "reason": "process_death"}, "")
	assertOutcome(t, h, "s-r1c", "failed")

	// 非终态事件经 send 层不 settle；空 sessionID 不 settle。
	h.sendSessionEvent("s-r1d", "claude", "text_delta", map[string]interface{}{"text": "x"})
	if got, _ := outcomeOf(t, h, "s-r1d"); got != "" {
		t.Fatalf("text_delta settled outcome: %q", got)
	}
	h.sendSessionEvent("", "claude", "turn_error", nil)
}

// TestReconcileClosureSettlesOutcome（R6）：reconcile 提交循环把闭包 (event, data)
// 原样传 settle helper——此处按闭包构造处的三种上游终态形状（projection_reconcile
// 状态 switch：completed/failed/interrupted，reason 恒 official_turn_status）直驱，
// 断言归类与 D2 取舍。
func TestReconcileClosureSettlesOutcome(t *testing.T) {
	h := NewHandlers()
	h.sessions.markRunning("s-r6")
	h.settleTurnOutcomeFromEvent("s-r6", "turn_completed", map[string]interface{}{"turnId": "t1", "done": true, "reason": "official_turn_status"})
	assertOutcome(t, h, "s-r6", "completed")

	h.sessions.markRunning("s-r6b")
	h.settleTurnOutcomeFromEvent("s-r6b", "turn_error", map[string]interface{}{"turnId": "t2", "error": "x", "reason": "official_turn_status"})
	assertOutcome(t, h, "s-r6b", "failed")

	h.sessions.markRunning("s-r6c")
	h.settleTurnOutcomeFromEvent("s-r6c", "turn_aborted", map[string]interface{}{"turnId": "t3", "reason": "official_turn_status"})
	assertOutcome(t, h, "s-r6c", "completed") // D2：官方 interrupted＝刻意收口
}

// TestClaudeCoverageDeadSettlesOutcome（R7）：直驱 dead 分支函数（kernel 无打开的
// hydrate 事务时 ApplyHydrateEvent 安全 false，settle 照常发生），断言 registry 记
// failed（(turn_aborted, process_death) 词表）。
func TestClaudeCoverageDeadSettlesOutcome(t *testing.T) {
	h := NewHandlers()
	h.settleClaudeCoverageDeadTurns("claude", "s-r7", []string{"t1", "t2"})
	assertOutcome(t, h, "s-r7", "failed")
	if got, _ := outcomeOf(t, h, "s-r7"); got != "failed" {
		t.Fatalf("coverage dead settle = %q", got)
	}
}

// TestApplyListRuntimeStateEmitsOutcome（§3.4 列表叠加点）：runningMap 权威分支与
// registry 回退分支都下发；空 outcome 不发字段。
func TestApplyListRuntimeStateEmitsOutcome(t *testing.T) {
	h := NewHandlers()
	h.sessions.markSettled("s-list", "failed", time.UnixMilli(1727577000000))

	mapped := h.applyListRuntimeState(map[string]interface{}{"id": "s-list"}, nil)
	if mapped["lastOutcome"] != "failed" {
		t.Fatalf("registry-fallback branch lastOutcome = %v", mapped["lastOutcome"])
	}
	if mapped["lastOutcomeAtMillis"] != int64(1727577000000) {
		t.Fatalf("lastOutcomeAtMillis = %v", mapped["lastOutcomeAtMillis"])
	}

	mapped2 := h.applyListRuntimeState(map[string]interface{}{"id": "s-list"}, map[string]bool{"s-list": true})
	if mapped2["runtimeState"] != "running" {
		t.Fatalf("runningMap authoritative branch runtimeState = %v", mapped2["runtimeState"])
	}
	if mapped2["lastOutcome"] != "failed" {
		t.Fatalf("runningMap authoritative branch lost lastOutcome: %v", mapped2)
	}

	empty := h.applyListRuntimeState(map[string]interface{}{"id": "s-clean"}, nil)
	if _, ok := empty["lastOutcome"]; ok {
		t.Fatalf("empty outcome emitted: %v", empty)
	}
}

// TestRelaySyncBlockOutcome（R3）：直测抽出的 relay 同步块 helper——主转发路径终态
// 族（turn_completed / "error"；EventResult(Done+Error) 的 turn_error 合成绕块但由
// 同一 core.EventError 的 "error" 映射过块等价覆盖）+ markIdle 词表维持原状的回归。
func TestRelaySyncBlockOutcome(t *testing.T) {
	h := NewHandlers()

	h.applyRelayEventRegistrySync("s-r3a", "turn_completed", map[string]interface{}{"turnId": "t1"})
	assertOutcome(t, h, "s-r3a", "completed")
	if st, ok := h.sessions.get("s-r3a"); !ok || string(st.state) != string(sessionStateIdle) {
		t.Fatalf("turn_completed did not markIdle: %v", st.state)
	}

	h.applyRelayEventRegistrySync("s-r3b", "error", map[string]interface{}{"message": "boom"})
	assertOutcome(t, h, "s-r3b", "failed")

	// 同步块词表维持原状（无 turn_error/turn_aborted markIdle 分支）——settle helper
	// 覆盖词表外的名字也不得经此块改执行态。
	h.applyRelayEventRegistrySync("s-r3c", "turn_started", nil)
	if st, ok := h.sessions.get("s-r3c"); !ok || string(st.state) != string(sessionStateRunning) {
		t.Fatalf("turn_started did not markRunning")
	}
	// turn_started（markRunning）清空结局——新 turn 取代旧结局。
	h.applyRelayEventRegistrySync("s-r3a", "turn_started", nil)
	if got, _ := outcomeOf(t, h, "s-r3a"); got != "" {
		t.Fatalf("turn_started did not clear outcome: %q", got)
	}

	// session_state_changed(idle) 只收执行态，不动 outcome。
	h.applyRelayEventRegistrySync("s-r3b", "session_state_changed", map[string]interface{}{"state": "idle"})
	assertOutcome(t, h, "s-r3b", "failed")
}

// TestApplyListRuntimeStateCatalogHint：codex-remote 官方 ThreadStatus hint 的
// 只升不降语义——外部（Mac 端发起）执行中的 session 不进 iOS 也能亮转圈（owner
// 2026-09-30 验收发现的缺口：此前列表 runtimeState 只剩 registry 回退，外部 turn
// 无事件喂入 → unknown → 永不亮）。
func TestApplyListRuntimeStateCatalogHint(t *testing.T) {
	h := NewHandlers()

	// unknown + running hint → running（主修复场景：外部 turn，registry 无记录）。
	m := h.applyListRuntimeState(map[string]interface{}{"id": "s1", "runtimeStateHint": "running"}, nil)
	if m["runtimeState"] != "running" {
		t.Fatalf("unknown+running hint = %v", m["runtimeState"])
	}
	if _, leaked := m["runtimeStateHint"]; leaked {
		t.Fatalf("hint key leaked to wire: %v", m)
	}

	// registry idle（陈旧观测）+ running hint → running（同 fetch 更新鲜）。
	h2 := NewHandlers()
	h2.sessions.markIdle("s2")
	m2 := h2.applyListRuntimeState(map[string]interface{}{"id": "s2", "runtimeStateHint": "running"}, nil)
	if m2["runtimeState"] != "running" {
		t.Fatalf("stale-idle+running hint = %v", m2["runtimeState"])
	}

	// requiresAction hint 绝对生效（active+waitingOnApproval）。
	m3 := h.applyListRuntimeState(map[string]interface{}{"id": "s3", "runtimeStateHint": "requiresAction"}, nil)
	if m3["runtimeState"] != "requiresAction" {
		t.Fatalf("requiresAction hint = %v", m3["runtimeState"])
	}

	// registry running（live relay）+ running hint → running（幂等，hint 不清 live 态）。
	h4 := NewHandlers()
	h4.sessions.markRunning("s4")
	m4 := h4.applyListRuntimeState(map[string]interface{}{"id": "s4", "runtimeStateHint": "running"}, nil)
	if m4["runtimeState"] != "running" {
		t.Fatalf("registry running + running hint = %v", m4["runtimeState"])
	}

	// runningMap authoritative idle（claude PID 权威：进程已退出）不被 hint 翻转——
	// claude 不产 hint，此为语义防回归：runningMap 分支后 hint 只对非 running 生效，
	// 但 PID 权威 idle 意味着无活进程，catalog stale 不应复活。
	// （实现上 running hint 会升级 idle——claude 无 hint 故不触发；此用例钉死
	// 「无 hint 行完全不受影响」。）
	m5 := h.applyListRuntimeState(map[string]interface{}{"id": "s5"}, map[string]bool{})
	if m5["runtimeState"] != "idle" {
		t.Fatalf("no-hint row under authoritative runningMap = %v", m5["runtimeState"])
	}

	// 未知 hint 值：消费删除但不改 state（fail-closed）。
	m6 := h.applyListRuntimeState(map[string]interface{}{"id": "s6", "runtimeStateHint": "bogus"}, nil)
	if m6["runtimeState"] != "unknown" {
		t.Fatalf("bogus hint changed state: %v", m6["runtimeState"])
	}
	if _, leaked := m6["runtimeStateHint"]; leaked {
		t.Fatalf("bogus hint key leaked: %v", m6)
	}
}

// TestEnrichSingleSessionOutcome（§3.4 单 session 叠加点）：get_session 路径同样
// 下发；agent 为 nil（非 claude 分支）时 registry 结局照样叠加。
func TestEnrichSingleSessionOutcome(t *testing.T) {
	h := NewHandlers()
	h.sessions.markSettled("s-single", "completed", time.UnixMilli(1727577001000))

	mapped := h.enrichSessionStateWithAgent(map[string]interface{}{"id": "s-single"}, nil)
	if mapped["lastOutcome"] != "completed" {
		t.Fatalf("single-session lastOutcome = %v", mapped["lastOutcome"])
	}
	if mapped["lastOutcomeAtMillis"] != int64(1727577001000) {
		t.Fatalf("single-session lastOutcomeAtMillis = %v", mapped["lastOutcomeAtMillis"])
	}

	clean := h.enrichSessionStateWithAgent(map[string]interface{}{"id": "s-none"}, nil)
	if _, ok := clean["lastOutcome"]; ok {
		t.Fatalf("empty outcome emitted on single-session path: %v", clean)
	}
}
