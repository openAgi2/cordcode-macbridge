package gobridge

// projection_inbox_test.go — S3 排队消息可见性的 reducer 侧（方案 §5.3 写入
// 所有权）：pending 占位行按 UserMessage.id upsert；落定（同 id user_message）
// 原位替换不双行；user_message_removed 只撤 pending 标记行（fail-closed，
// 绝不碰真实回合）；撤行进 patch 的 additive RemovedTurnIDs。

import (
	"context"
	"reflect"
	"testing"
)

func TestReducerQueuedPlaceholderUpsertAndSettleReplace(t *testing.T) {
	r := newTestReducer()
	// 排队占位（dsh-web inbox splice insert → wire user_message pending:true）。
	r.Apply(ev(1, "dsh-web", "s1", "user_message", map[string]interface{}{
		"itemId": "m-queued", "text": "queued while running", "pending": true,
	}))
	proj, _ := r.Snapshot("dsh-web", "s1")
	if len(proj.Turns) != 1 {
		t.Fatalf("turns = %d, want 1 placeholder", len(proj.Turns))
	}
	row := proj.Turns[0]
	if row.TurnID != "m-queued" || row.Pending == nil || !*row.Pending {
		t.Fatalf("placeholder must be keyed by itemId with pending marker, got %+v", row)
	}
	if row.User == nil || row.User.ID != "m-queued" {
		t.Fatalf("placeholder user = %+v", row.User)
	}
	// 占位行不武装 execution（排队 ≠ 回合运行）。
	if proj.Execution.Phase == "running" && proj.Execution.ActiveTurnID == "m-queued" {
		t.Fatalf("pending placeholder must not arm execution, got %+v", proj.Execution)
	}

	// 落定（claim splice 先撤、user/message 同 id 进真实回合）。
	r.Apply(ev(2, "dsh-web", "s1", "user_message_removed", map[string]interface{}{"itemId": "m-queued"}))
	r.Apply(ev(3, "dsh-web", "s1", "turn_started", map[string]interface{}{"turnId": "T9"}))
	r.Apply(ev(4, "dsh-web", "s1", "user_message", map[string]interface{}{
		"itemId": "m-queued", "turnId": "T9", "text": "queued while running",
	}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	if len(proj.Turns) != 1 {
		t.Fatalf("turns = %d, want 1 (placeholder replaced in place, no double row)", len(proj.Turns))
	}
	if proj.Turns[0].TurnID != "T9" || proj.Turns[0].Pending != nil {
		t.Fatalf("settled row must be the real turn without pending marker, got %+v", proj.Turns[0])
	}
	if proj.Turns[0].User == nil || proj.Turns[0].User.ID != "m-queued" {
		t.Fatalf("settled user id = %+v, want m-queued (id continuity)", proj.Turns[0].User)
	}
}

func TestReducerUserMessageRemovedRetractsOnlyPending(t *testing.T) {
	r := newTestReducer()
	// 一个占位行 + 一个真实回合行。
	r.Apply(ev(1, "dsh-web", "s1", "user_message", map[string]interface{}{
		"itemId": "m-pend", "text": "pending one", "pending": true,
	}))
	r.Apply(ev(2, "dsh-web", "s1", "user_message", map[string]interface{}{
		"itemId": "m-real", "turnId": "T1", "text": "real turn",
	}))
	// 撤占位行。
	r.Apply(ev(3, "dsh-web", "s1", "user_message_removed", map[string]interface{}{"itemId": "m-pend"}))
	proj, _ := r.Snapshot("dsh-web", "s1")
	if len(proj.Turns) != 1 || proj.Turns[0].TurnID != "T1" {
		t.Fatalf("only the real turn must survive, got %+v", proj.Turns)
	}
	// fail-closed：对真实回合的 id 发 removed 不得撤行。
	r.Apply(ev(4, "dsh-web", "s1", "user_message_removed", map[string]interface{}{"itemId": "m-real"}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	if len(proj.Turns) != 1 || proj.Turns[0].TurnID != "T1" {
		t.Fatalf("removal must never touch a real turn, got %+v", proj.Turns)
	}
	// 未知 id 是 no-op。
	r.Apply(ev(5, "dsh-web", "s1", "user_message_removed", map[string]interface{}{"itemId": "m-ghost"}))
	proj, _ = r.Snapshot("dsh-web", "s1")
	if len(proj.Turns) != 1 {
		t.Fatalf("unknown id removal must be a no-op, got %+v", proj.Turns)
	}
}

func TestReducerRetractionRidesPatchRemovedTurnIDs(t *testing.T) {
	r := newTestReducer()
	r.Apply(ev(1, "dsh-web", "s1", "user_message", map[string]interface{}{
		"itemId": "m-patch", "text": "queued", "pending": true,
	}))
	patch, ok := r.FlushPatch("dsh-web", "s1")
	if !ok || len(patch.UpsertTurns) != 1 {
		t.Fatalf("placeholder patch missing: ok=%v turns=%d", ok, len(patch.UpsertTurns))
	}
	r.Apply(ev(2, "dsh-web", "s1", "user_message_removed", map[string]interface{}{"itemId": "m-patch"}))
	patch, ok = r.FlushPatch("dsh-web", "s1")
	if !ok {
		t.Fatal("retraction must produce a patch")
	}
	if !reflect.DeepEqual(patch.RemovedTurnIDs, []string{"m-patch"}) {
		t.Fatalf("patch.RemovedTurnIDs = %v, want [m-patch]", patch.RemovedTurnIDs)
	}
	if len(patch.UpsertTurns) != 0 {
		t.Fatalf("retracted row must not ride upsertTurns, got %+v", patch.UpsertTurns)
	}
}

// TestSessionQueueManagementCapabilityAdvertisement：S3 管理范围（OD-2b=B）
// 的能力广告——实现 SessionQueueManager 的 backend 广告
// session_queue_management，未实现的不广告（iOS 据此不画排队项管理动作）。
func TestSessionQueueManagementCapabilityAdvertisement(t *testing.T) {
	found := false
	for _, c := range deriveBackendCapabilities("dsh-web", &queueManagerAgent{fakeAgent: &fakeAgent{name: "dsh-web"}}, "") {
		if c == "session_queue_management" {
			found = true
		}
	}
	if !found {
		t.Fatal("dsh-web (SessionQueueManager) must advertise session_queue_management")
	}
	for _, id := range []string{"grok", "claude", "codex-remote", "opencode-web"} {
		for _, c := range deriveBackendCapabilities(id, &fakeAgent{name: id}, "") {
			if c == "session_queue_management" {
				t.Fatalf("%s must NOT advertise session_queue_management (no SessionQueueManager)", id)
			}
		}
	}
}

type queueManagerAgent struct {
	*fakeAgent
}

func (a *queueManagerAgent) UpdateSessionQueue(_ context.Context, _, _, _, _ string) error {
	return nil
}
