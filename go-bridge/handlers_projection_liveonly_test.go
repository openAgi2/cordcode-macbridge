package gobridge

// Live-only projection admission tests. The legacy deepseek store-bridge branch
// (agent/dsh) that originally routed these scenarios was removed with the
// deprecated/dsh migration (2026-09-29); the admission machinery is now driven
// through the dsh-web branch (live registry session + dshw-*-t1 coverage →
// admission), mirroring TestDSHWebColdPullDuringLiveTurnKeepsKernelBaseline.
// The former dead-session not_found / dead-process-serves cases were
// deepseek-branch-specific semantics and died with the branch: dsh-web
// deliberately cold-rebuilds those cases instead (2026-09-06 matrix, see
// TestDSHWebRestartMidTurnColdPullRebuildsHistory). String-level deepseek
// guards (hydrate allowlist, source prep, observation pruning) remain in
// production code and remain covered below.
import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func newDshProjectionHandlers(t *testing.T) *Handlers {
	t.Helper()
	t.Setenv("DSH_HOME", t.TempDir())
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	return h
}

func liveOnlyPublishTurn(h *Handlers, backend, session, turnID string, deltas []string, terminal string) {
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: backend, SessionID: session, Event: "turn_started", Data: map[string]interface{}{"turnId": turnID}})
	for _, d := range deltas {
		h.eventPublisher.PublishLogical(LogicalEvent{BackendID: backend, SessionID: session, Event: "text_delta", Data: map[string]interface{}{"itemId": turnID, "delta": d}})
	}
	switch terminal {
	case "turn_completed":
		h.eventPublisher.PublishLogical(LogicalEvent{BackendID: backend, SessionID: session, Event: "turn_completed", Data: map[string]interface{}{"turnId": turnID}})
	case "turn_aborted":
		h.eventPublisher.PublishLogical(LogicalEvent{BackendID: backend, SessionID: session, Event: "turn_aborted", Data: map[string]interface{}{"turnId": turnID}})
	}
}

func liveOnlyProjectionPull(h *Handlers, sessionID string, sinceRev int) (*readFileCaptureConn, WireMessage) {
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID, "sinceRev": sinceRev})
	msg := WireMessage{RequestID: "r-liveonly-" + sessionID, BackendID: "dsh-web", Method: "get_session_projection", Params: params}
	h.handleGetSessionProjection(conn, msg, nil)
	return conn, msg
}

func liveOnlyProjectionOf(t *testing.T, conn *readFileCaptureConn) SessionProjection {
	t.Helper()
	if conn.err != nil {
		t.Fatalf("expected success, got error: code=%s msg=%s", conn.err.Code, conn.err.Message)
	}
	if conn.data == nil {
		t.Fatal("error-free response must carry data (no empty shell)")
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("served data not a map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("snapshot response missing projection: %+v", dataMap)
	}
	return proj
}

// T1+T7（C1/C5）：live 注入的 kernel 状态经 admission 成为基线——sinceRev=0 成功返回
// 完整投影，rev 连续，kernel Ready；全程无 transcript（零磁盘源）。车具 dsh-web：
// sinceRev=0 对 dsh-web 是 forceCold，须注册 live registry 会话才命中 admission
// （deepseek 分支已随 deprecated/dsh 迁移删除，2026-09-29）。
func TestLiveOnlyProjectionAdmissionServesKernelBaseline(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-live-1"
	t1 := "dshw-live1-t1"

	liveOnlyPublishTurn(h, "dsh-web", sessionID, t1, []string{"Hello", " ", "world"}, "turn_completed")

	h.mu.Lock()
	h.putSession(sessionID, &fakeAgentSession{id: sessionID, events: make(chan core.Event)})
	h.mu.Unlock()

	conn, _ := liveOnlyProjectionPull(h, sessionID, 0)
	proj := liveOnlyProjectionOf(t, conn)
	if len(proj.Turns) != 1 || proj.Turns[0].TurnID != t1 {
		t.Fatalf("turns = %+v", proj.Turns)
	}
	if got := proj.Turns[0].Status; got != "completed" {
		t.Fatalf("turn status = %q, want completed", got)
	}
	var text string
	for _, p := range proj.Turns[0].Assistant.Parts {
		if p.Type == "text" {
			text += p.Text
		}
	}
	if text != "Hello world" {
		t.Fatalf("assistant text = %q", text)
	}
	if proj.SyncRev < 3 {
		t.Fatalf("SyncRev = %d, want >= 3 (deltas+terminal must commit)", proj.SyncRev)
	}
	if st := h.projectionKernel.Status("dsh-web", sessionID); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready after admission", st.Phase)
	}
	// 第二次 pull 重复走 admission（sinceRev=0 恒 forceCold），幂等无重复提交。
	conn2, _ := liveOnlyProjectionPull(h, sessionID, 0)
	if proj2 := liveOnlyProjectionOf(t, conn2); proj2.SyncRev != proj.SyncRev {
		t.Fatalf("second pull head %d != first %d", proj2.SyncRev, proj.SyncRev)
	}
}

// T2+T3（C1）：复刻真机时序——patch rev1-6 先于 sinceRev=0 到达。基线 snapshot 的
// cutRev 覆盖全部已提交事件；随后 rev7 正常续接（delta base=cut 无 gap）；再与
// head 之间的响应为空补丁集。fence 的「post-cut patch 在 RPC 结果后同 sink 释放」
// 是 backend 无关的既有契约（projection_snapshot_fence_test.go），此处锁端到端 rev 连续。
func TestLiveOnlyProjectionPatchesBeforeBaselineAndContinuity(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-live-2"
	t1, t2 := "dshw-live2-t1", "dshw-live2-t2"

	liveOnlyPublishTurn(h, "dsh-web", sessionID, t1, []string{"a", "b", "c"}, "turn_completed")
	// 第二个 turn 保持 running：turn_started 不提交，两个 text_delta 提交 → head=6。
	liveOnlyPublishTurn(h, "dsh-web", sessionID, t2, []string{"d", "e"}, "")

	h.mu.Lock()
	h.putSession(sessionID, &fakeAgentSession{id: sessionID, events: make(chan core.Event)})
	h.mu.Unlock()

	conn, _ := liveOnlyProjectionPull(h, sessionID, 0)
	proj := liveOnlyProjectionOf(t, conn)
	if proj.SyncRev != 6 {
		t.Fatalf("head = %d, want 6 (T1: 3 deltas + terminal = 4; T2: 2 deltas = 2)", proj.SyncRev)
	}
	if len(proj.Turns) != 2 {
		t.Fatalf("turns = %+v, want T1 completed + T2 running", proj.Turns)
	}
	if proj.Turns[1].Status == "completed" {
		t.Fatalf("T2 must still be running in baseline: %+v", proj.Turns[1])
	}

	// rev7 续接：delta 请求 base=6。
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: "dsh-web", SessionID: sessionID, Event: "text_delta", Data: map[string]interface{}{"itemId": t2, "delta": "f"}})
	connDelta, _ := liveOnlyProjectionPull(h, sessionID, 6)
	if connDelta.err != nil {
		t.Fatalf("delta pull failed: %+v", connDelta.err)
	}
	deltaMap, ok := connDelta.data.(map[string]interface{})
	if !ok {
		t.Fatalf("delta data not a map: %T", connDelta.data)
	}
	headRev, ok := deltaMap["headRev"].(int)
	if !ok || headRev != 7 {
		t.Fatalf("delta headRev = %#v, want 7", deltaMap["headRev"])
	}
	patches, _ := deltaMap["patches"].([]ProjectionPatch)
	if len(patches) == 0 {
		t.Fatalf("delta at 6 must carry the rev7 patch: %+v", deltaMap["patches"])
	}
	if patches[0].BaseRev != 6 || patches[0].SyncRev != 7 {
		t.Fatalf("patch rev range = %d→%d, want 6→7 (no gap, no dup)", patches[0].BaseRev, patches[0].SyncRev)
	}

	// at-head：再次 delta 请求 base=7 → 空补丁集。
	connHead, _ := liveOnlyProjectionPull(h, sessionID, 7)
	if connHead.err != nil {
		t.Fatalf("at-head pull failed: %+v", connHead.err)
	}
	headMap, _ := connHead.data.(map[string]interface{})
	if headRev, _ := headMap["headRev"].(int); headRev != 7 {
		t.Fatalf("at-head headRev = %#v, want 7", headMap["headRev"])
	}
	if patches, _ := headMap["patches"].([]ProjectionPatch); len(patches) != 0 {
		t.Fatalf("at-head must return empty patch set, got %+v", patches)
	}
}

// T6（store bridge 后语义）：deepseek 已入投影 hydrate 允许清单（file-backed
// pathless 重建）；source 准备在无注册 agent 且无已提交 turn 时诚实拒绝
// errProjectionSourceUnavailable（不是 not_migrated——那是未迁移后端）。
// 字符串层守卫：deepseek 分支删除后 allowlist/source-prep 仍在生产代码中。
func TestLiveOnlyProjectionPathGuards(t *testing.T) {
	h := newDshProjectionHandlers(t)
	if !backendSupportsProjectionHydrate("deepseek") {
		t.Fatal("deepseek must be a projection hydrate backend (store bridge, design §4.4)")
	}
	for _, b := range []string{"grok", "cursor", "unknown-backend"} {
		if backendSupportsProjectionHydrate(b) {
			t.Fatalf("backendSupportsProjectionHydrate(%q) must be false", b)
		}
	}
	if _, err := h.prepareProjectionHydrateSource(context.Background(), "deepseek", "s", ""); !errors.Is(err, errProjectionSourceUnavailable) {
		t.Fatalf("prepareProjectionHydrateSource(deepseek) = %v, want errProjectionSourceUnavailable", err)
	}
}

// T8（附带修复 A）：live-only backend 的死会话（无 live registry 会话、无 kernel 状态）
// 从观察集剪枝；其他 backend 的未知会话观察不受影响（外部 turn 不是 registry 会话）。
// deepseek 仍是 backendHasNoExternalEventSource 的唯一成员（字符串层，随生产代码保留）。
func TestObservationPrunesDeadLiveOnlySessions(t *testing.T) {
	h := newDshProjectionHandlers(t)

	liveOnlyPublishTurn(h, "deepseek", "dsh-obs-alive", "T1", []string{"x"}, "turn_completed")

	conn := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "dev-prune"}}
	params := json.RawMessage(`{"backendId":"deepseek","sessionIds":["dsh-obs-alive","dsh-obs-ghost"],"deliveryMode":"full_stream","includeRunningSessionSignals":true,"leaseSeconds":90}`)
	h.handleSetObservationScope(conn, WireMessage{RequestID: "req-prune", BackendID: "deepseek", Params: params})

	scope := h.observation.GetScope("dev-prune", "deepseek")
	if scope == nil || len(scope.SessionIDs) != 1 || scope.SessionIDs[0] != "dsh-obs-alive" {
		t.Fatalf("scope after prune = %#v, want only dsh-obs-alive", scope)
	}
	// 剪枝的权威判定在 observation manager：死会话不再命中 full_stream 投递。
	// （broadcaster.Targets 有 backend 级 fallback，不能作为 session 级剪枝证据。）
	if h.observation.ShouldSendEvent("dev-prune", "deepseek", "dsh-obs-ghost", "projection_patch") {
		t.Fatal("pruned dead session must no longer match full_stream delivery")
	}
	if !h.observation.ShouldSendEvent("dev-prune", "deepseek", "dsh-obs-alive", "projection_patch") {
		t.Fatal("live kernel-backed session must still match full_stream delivery")
	}

	// 回归护栏：claude 的未知会话（外部 turn）仍被观察与订阅。
	connClaude := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "dev-prune"}}
	paramsClaude := json.RawMessage(`{"backendId":"claude","sessionIds":["ext-unknown"],"deliveryMode":"full_stream","includeRunningSessionSignals":true,"leaseSeconds":90}`)
	h.handleSetObservationScope(connClaude, WireMessage{RequestID: "req-claude", BackendID: "claude", Params: paramsClaude})
	scopeClaude := h.observation.GetScope("dev-prune", "claude")
	if scopeClaude == nil || len(scopeClaude.SessionIDs) != 1 || scopeClaude.SessionIDs[0] != "ext-unknown" {
		t.Fatalf("claude unknown-session observation must be preserved: %#v", scopeClaude)
	}
	if targets := h.broadcaster.Targets("claude", "ext-unknown", ""); len(targets) != 1 {
		t.Fatalf("claude unknown session must stay subscribed, targets=%d", len(targets))
	}
}
