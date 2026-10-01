package gobridge

import (
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// outcome_hint_test.go —— 红❗持久化（2026-10-01）定向测试：官方 ThreadStatus
// systemError → AgentSessionInfo.OutcomeHint → 列表 wire lastOutcome=failed
// （跨 bridge 重启的持久官方真相；registry outcome 侧存是内存态）。
// 方案：iOS 仓 docs/2026-10-01-codex-system-error-outcome-plan.md（r1 APPROVED）。

func TestPlantRuntimeStateHintsPlantsOutcomeHint(t *testing.T) {
	wire := []map[string]interface{}{{"id": "s1"}, {"id": "s2"}}
	infos := []core.AgentSessionInfo{
		{ID: "s1", RuntimeStateHint: "running", OutcomeHint: "failed"},
		{ID: "s2"},
	}
	plantRuntimeStateHints(wire, infos)
	if v, _ := wire[0]["runtimeStateHint"].(string); v != "running" {
		t.Fatalf("s1 runtimeStateHint = %q, want running", v)
	}
	if v, _ := wire[0]["outcomeHint"].(string); v != "failed" {
		t.Fatalf("s1 outcomeHint = %q, want failed", v)
	}
	if _, ok := wire[1]["outcomeHint"]; ok {
		t.Fatal("s2 must not carry outcomeHint when empty")
	}
}

func TestApplyListRuntimeStateOutcomeHintWins(t *testing.T) {
	h := NewHandlers()
	// registry 旧 completed（bridge 漏收 error 通知的分歧行）——catalog systemError
	// 是更新真相，hint 在场即胜。
	h.sessions.markSettled("s-hint", "completed", time.Unix(1000, 0))
	mapped := h.applyListRuntimeState(map[string]interface{}{
		"id":             "s-hint",
		"updatedAtMillis": int64(1790846818000),
		"outcomeHint":    "failed",
	}, nil)
	if got, _ := mapped["lastOutcome"].(string); got != "failed" {
		t.Fatalf("lastOutcome = %v, want failed (catalog hint wins over stale registry)", got)
	}
	if got, _ := mapped["lastOutcomeAtMillis"].(int64); got != 1790846818000 {
		t.Fatalf("lastOutcomeAtMillis = %v, want row updatedAtMillis", got)
	}
	if _, ok := mapped["outcomeHint"]; ok {
		t.Fatal("outcomeHint temp key must be stripped (never leaks to wire)")
	}
}

func TestApplyListRuntimeStateOutcomeHintFloatMillis(t *testing.T) {
	// recent 视图快照经 JSON round-trip 后 updatedAtMillis 是 float64。
	h := NewHandlers()
	mapped := h.applyListRuntimeState(map[string]interface{}{
		"id":             "s-float",
		"updatedAtMillis": float64(1790846818000),
		"outcomeHint":    "failed",
	}, nil)
	if got, _ := mapped["lastOutcomeAtMillis"].(int64); got != 1790846818000 {
		t.Fatalf("lastOutcomeAtMillis = %v, want 1790846818000 (float64 tolerated)", got)
	}
}

func TestApplyListRuntimeStateRegistryOutcomeUnchanged(t *testing.T) {
	h := NewHandlers()
	// hint 缺席 → 既有 registry lastOutcomeFor 路径原样。
	settled := time.Unix(2000, 0)
	h.sessions.markSettled("s-reg", "failed", settled)
	mapped := h.applyListRuntimeState(map[string]interface{}{"id": "s-reg"}, nil)
	if got, _ := mapped["lastOutcome"].(string); got != "failed" {
		t.Fatalf("lastOutcome = %v, want failed (registry path)", got)
	}
	if got, _ := mapped["lastOutcomeAtMillis"].(int64); got != settled.UnixMilli() {
		t.Fatalf("lastOutcomeAtMillis = %v, want registry settle time", got)
	}
	// 两者皆缺席 → 无 lastOutcome 字段。
	mapped = h.applyListRuntimeState(map[string]interface{}{"id": "s-none"}, nil)
	if _, ok := mapped["lastOutcome"]; ok {
		t.Fatal("no outcome source → lastOutcome must be absent")
	}
}

func TestApplyOutcomeHintStripsKeyAndRequiresMillis(t *testing.T) {
	// 非 failed 词：键存在即删（不泄漏），不命中。
	mapped := map[string]interface{}{"outcomeHint": "somethingElse", "updatedAtMillis": int64(1)}
	if applyOutcomeHint(mapped) {
		t.Fatal("non-failed word must not hit")
	}
	if _, ok := mapped["outcomeHint"]; ok {
		t.Fatal("key must be deleted even for non-failed words")
	}
	// failed 但行缺 updatedAtMillis：不命中（不造时间），键仍删。
	mapped = map[string]interface{}{"outcomeHint": "failed"}
	if applyOutcomeHint(mapped) {
		t.Fatal("missing updatedAtMillis must not hit")
	}
	if _, ok := mapped["outcomeHint"]; ok {
		t.Fatal("key must be deleted even when not hit")
	}
	if _, ok := mapped["lastOutcome"]; ok {
		t.Fatal("no fabricated lastOutcome without a timestamp")
	}
}

func TestEnrichSessionStateWithAgentOutcomeHintDefensive(t *testing.T) {
	// 单 session 路径（get_session/resume）不经 plantRuntimeStateHints 种键——
	// 消费分支为防御性对称（同 RuntimeStateHint 现状）；手动种键驱动验证。
	h := NewHandlers()
	mapped := h.enrichSessionStateWithAgent(map[string]interface{}{
		"id":             "s-single",
		"updatedAtMillis": int64(1790846818000),
		"outcomeHint":    "failed",
	}, nil)
	if got, _ := mapped["lastOutcome"].(string); got != "failed" {
		t.Fatalf("lastOutcome = %v, want failed", got)
	}
	if _, ok := mapped["outcomeHint"]; ok {
		t.Fatal("outcomeHint temp key must be stripped")
	}
}
