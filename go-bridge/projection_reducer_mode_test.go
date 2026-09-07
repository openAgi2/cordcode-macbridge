package gobridge

// projection_reducer_mode_test.go — typed 模式状态投影定向测试（Grok Build
// 面板方案 2026-09-07 §5.1）：整值快照 last-wins、unknown 是值不是缺席、
// 未知 status 词汇 fail-closed 丢弃、去重不空转、patch 携带变化、snapshot/
// clone 保真。

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestReducerSessionModeStoresWholeSnapshotAndDedupes(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "grokbuild", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "grokbuild", "s1", "session_mode", map[string]interface{}{
		"status": "confirmed", "mode": "plan", "canSet": false,
		"reason": "grok_mode_switch_blocked_official_recovery",
	}))
	proj, ok := r.Snapshot("grokbuild", "s1")
	if !ok {
		t.Fatal("no projection")
	}
	sm := proj.SessionMode
	if sm == nil || sm.Status != "confirmed" || sm.Mode == nil || *sm.Mode != "plan" {
		t.Fatalf("sessionMode = %+v, want confirmed/plan", sm)
	}
	if sm.CanSet {
		t.Fatal("canSet must pass through false (P7 blocked)")
	}
	if sm.Reason == "" {
		t.Fatal("reason must pass through")
	}

	// 同值重复：去重，不 bump SyncRev。
	before := proj.SyncRev
	r.Apply(ev(3, "grokbuild", "s1", "session_mode", map[string]interface{}{
		"status": "confirmed", "mode": "plan", "canSet": false,
		"reason": "grok_mode_switch_blocked_official_recovery",
	}))
	proj, _ = r.Snapshot("grokbuild", "s1")
	if proj.SyncRev != before {
		t.Fatalf("same-value session_mode must dedupe: %d -> %d", before, proj.SyncRev)
	}

	// 值变化（外部切回 default + CMU 重读）：last-wins。
	r.Apply(ev(4, "grokbuild", "s1", "session_mode", map[string]interface{}{
		"status": "confirmed", "mode": "default", "canSet": false,
	}))
	proj, _ = r.Snapshot("grokbuild", "s1")
	if sm := proj.SessionMode; sm == nil || sm.Mode == nil || *sm.Mode != "default" {
		t.Fatalf("last-wins failed: %+v", sm)
	}
}

func TestReducerSessionModeUnknownIsAValue(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "grokbuild", "s1", "session_mode", map[string]interface{}{
		"status": "unknown", "canSet": false,
	}))
	proj, ok := r.Snapshot("grokbuild", "s1")
	if !ok || proj.SessionMode == nil {
		t.Fatal("unknown must be stored as a value, not dropped to nil")
	}
	if proj.SessionMode.Status != "unknown" || proj.SessionMode.Mode != nil {
		t.Fatalf("unknown state = %+v", proj.SessionMode)
	}
}

func TestReducerSessionModeUnknownVocabularyFailsClosed(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "grokbuild", "s1", "session_mode", map[string]interface{}{
		"status": "pretty-sure", "canSet": true,
	}))
	// 垃圾 status 不得进投影（会话壳可因事件入口而诞生，但 sessionMode 必须缺席）。
	proj, _ := r.Snapshot("grokbuild", "s1")
	if proj.SessionMode != nil {
		t.Fatalf("garbage status must be dropped, got %+v", proj.SessionMode)
	}
}

func TestReducerSessionModePatchCarriesChange(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "grokbuild", "s1", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "grokbuild", "s1", "session_mode", map[string]interface{}{
		"status": "confirmed", "mode": "default", "canSet": false,
	}))
	patch, ok := r.FlushPatch("grokbuild", "s1")
	if !ok {
		t.Fatal("patch expected")
	}
	if patch.SessionMode == nil || patch.SessionMode.Mode == nil || *patch.SessionMode.Mode != "default" {
		t.Fatalf("patch.sessionMode = %+v", patch.SessionMode)
	}
	// 无新变化：patch 不再携带。
	r.Apply(ev(3, "grokbuild", "s1", "session_mode", map[string]interface{}{
		"status": "confirmed", "mode": "default", "canSet": false,
	}))
	if patch, ok := r.FlushPatch("grokbuild", "s1"); ok && patch.SessionMode != nil {
		t.Fatalf("unchanged session_mode must not ride patches: %+v", patch.SessionMode)
	}
}

func TestSessionModeCloneFidelity(t *testing.T) {
	m := "plan"
	view := SessionModeView{Status: "confirmed", Mode: &m, CanSet: false, Reason: "r1"}
	proj := SessionProjection{SessionID: "s1", SessionMode: &view}
	cloned := cloneSessionProjection(proj)
	if cloned.SessionMode == nil || cloned.SessionMode.Mode == nil || *cloned.SessionMode.Mode != "plan" {
		t.Fatal("clone lost sessionMode")
	}
	*cloned.SessionMode.Mode = "default"
	if *proj.SessionMode.Mode != "plan" {
		t.Fatal("clone must deep-copy the mode pointer")
	}
}

// TestMapAgentEventSessionMode：core.EventSessionMode → 逻辑事件 "session_mode"
//（§5.1 live 路径；nil payload 丢弃——unknown 是值，nil 才是缺席）。
func TestMapAgentEventSessionMode(t *testing.T) {
	plan := "plan"
	logical, dataRaw, done := mapAgentEvent(core.Event{
		Type:        core.EventSessionMode,
		SessionID:   "s1",
		SessionMode: &core.SessionModeEvent{Status: "confirmed", Mode: &plan, CanSet: false, Reason: "grok_mode_switch_blocked_official_recovery"},
	})
	if logical != "session_mode" || done {
		t.Fatalf("event = %q done=%v", logical, done)
	}
	data, _ := dataRaw.(map[string]interface{})
	if data["status"] != "confirmed" || data["mode"] != "plan" || data["canSet"] != false ||
		data["reason"] != "grok_mode_switch_blocked_official_recovery" {
		t.Fatalf("data = %+v", data)
	}
	if name, _, _ := mapAgentEvent(core.Event{Type: core.EventSessionMode, SessionID: "s1"}); name != "" {
		t.Fatal("nil payload must drop")
	}
}
