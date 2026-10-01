package gobridge

import (
	"testing"
	"time"
)

// registry_outcome_test.go —— 2026-10-01 owner 矩阵 r1 根因修复的定向测试：
// settle 结局必须在 idle 槽位清理（cleanupIdleSessions→registry.delete 整条
// 删 trackedSession）后存活；新 turn 取代旧结局时同步清除侧存；侧存超限逐旧；
// plain "error" 加入终态 sessions_changed invalidate 词表。

func TestOutcomeSurvivesRegistryDelete(t *testing.T) {
	r := newSessionRegistry()
	settled := time.Now()
	r.markSettled("s-evict", "failed", settled)
	if _, ok := r.get("s-evict"); !ok {
		t.Fatal("markSettled should keep the tracked entry present")
	}
	// cleanupIdleSessions 的逐出路径：整条 tracked 记录被删。
	if _, ok := r.delete("s-evict"); !ok {
		t.Fatal("delete should remove the tracked entry")
	}
	if _, ok := r.get("s-evict"); ok {
		t.Fatal("tracked entry must be gone after idle eviction")
	}
	outcome, at := r.lastOutcomeFor("s-evict")
	if outcome != "failed" || !at.Equal(settled) {
		t.Fatalf("outcome must survive idle eviction, got %q@%v", outcome, at)
	}
}

func TestOutcomeClearedOnNewTurn(t *testing.T) {
	r := newSessionRegistry()
	r.markSettled("s-turn", "completed", time.Now())
	r.claimRunning("s-turn")
	if outcome, _ := r.lastOutcomeFor("s-turn"); outcome != "" {
		t.Fatalf("new turn must clear the stale outcome, got %q", outcome)
	}
	// settle-after-delete：tracked 条目缺席时侧存仍要记录结局（晚到 settle）。
	late := time.Now()
	r.markSettled("s-late", "failed", late)
	r.delete("s-late")
	r.markSettled("s-late", "failed", late) // delete 之后的 settle 不被丢
	if outcome, _ := r.lastOutcomeFor("s-late"); outcome != "failed" {
		t.Fatalf("settle after eviction must record the outcome, got %q", outcome)
	}
}

func TestOutcomeSideStorePrune(t *testing.T) {
	orig := registryOutcomeCap
	registryOutcomeCap = 3
	t.Cleanup(func() { registryOutcomeCap = orig })

	r := newSessionRegistry()
	base := time.Now()
	for i := 0; i < 5; i++ {
		r.markSettled("s-prune-"+string(rune('a'+i)), "completed", base.Add(time.Duration(i)*time.Second))
	}
	if outcome, _ := r.lastOutcomeFor("s-prune-a"); outcome != "" {
		t.Fatal("oldest entries must be pruned once over cap")
	}
	if outcome, _ := r.lastOutcomeFor("s-prune-e"); outcome != "completed" {
		t.Fatal("newest entries must be kept")
	}
	if len(r.outcomes) > registryOutcomeCap {
		t.Fatalf("side store must respect cap, got %d", len(r.outcomes))
	}
}

func TestTerminalInvalidateEventVocabulary(t *testing.T) {
	for _, name := range []string{"turn_completed", "turn_error", "turn_aborted", "error"} {
		if !isTerminalInvalidateEvent(name) {
			t.Fatalf("%q must be a terminal invalidate word", name)
		}
	}
	for _, name := range []string{"text_delta", "session_state_changed", "question_asked", "", "errors"} {
		if isTerminalInvalidateEvent(name) {
			t.Fatalf("%q must NOT be a terminal invalidate word", name)
		}
	}
}
