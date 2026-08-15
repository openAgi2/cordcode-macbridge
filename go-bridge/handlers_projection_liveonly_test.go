package gobridge

// Live-only projection admission tests (spec docs/2026-08-16-dsh-live-only-projection-spec.md).
// C1 基线=kernel 状态；C2 死会话诚实 not_found；C5 独立 admission 路径（无磁盘源）；
// 附带修复 A：观察心跳剪枝。这些测试不注册任何 agent、不落任何 transcript——admission
// 的数据源只有 live ingestion 累积的 kernel reducer 状态。
import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

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
	msg := WireMessage{RequestID: "r-liveonly-" + sessionID, BackendID: "deepseek", Method: "get_session_projection", Params: params}
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
// 完整投影，rev 连续，kernel Ready；全程无 agent、无 transcript（零磁盘源）。
func TestLiveOnlyProjectionAdmissionServesKernelBaseline(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	liveOnlyPublishTurn(h, "deepseek", "dsh-live-1", "T1", []string{"Hello", " ", "world"}, "turn_completed")

	conn, _ := liveOnlyProjectionPull(h, "dsh-live-1", 0)
	proj := liveOnlyProjectionOf(t, conn)
	if len(proj.Turns) != 1 || proj.Turns[0].TurnID != "T1" {
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
	if st := h.projectionKernel.Status("deepseek", "dsh-live-1"); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready after admission", st.Phase)
	}
	// 第二次 pull 走 Ready 快路径，依旧成功。
	conn2, _ := liveOnlyProjectionPull(h, "dsh-live-1", 0)
	if proj2 := liveOnlyProjectionOf(t, conn2); proj2.SyncRev != proj.SyncRev {
		t.Fatalf("second pull head %d != first %d", proj2.SyncRev, proj.SyncRev)
	}
}

// T2+T3（C1）：复刻真机时序——patch rev1-6 先于 sinceRev=0 到达。基线 snapshot 的
// cutRev 覆盖全部已提交事件；随后 rev7 正常续接（delta base=cut 无 gap）；再与
// head 之间的响应为空补丁集。fence 的「post-cut patch 在 RPC 结果后同 sink 释放」
// 是 backend 无关的既有契约（projection_snapshot_fence_test.go），此处锁端到端 rev 连续。
func TestLiveOnlyProjectionPatchesBeforeBaselineAndContinuity(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	liveOnlyPublishTurn(h, "deepseek", "dsh-live-2", "T1", []string{"a", "b", "c"}, "turn_completed")
	// 第二个 turn 保持 running：turn_started 不提交，两个 text_delta 提交 → head=6。
	liveOnlyPublishTurn(h, "deepseek", "dsh-live-2", "T2", []string{"d", "e"}, "")

	conn, _ := liveOnlyProjectionPull(h, "dsh-live-2", 0)
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
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: "deepseek", SessionID: "dsh-live-2", Event: "text_delta", Data: map[string]interface{}{"itemId": "T2", "delta": "f"}})
	connDelta, _ := liveOnlyProjectionPull(h, "dsh-live-2", 6)
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
	connHead, _ := liveOnlyProjectionPull(h, "dsh-live-2", 7)
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

// T4+T9（C2/C4）：死会话（kernel 无痕 + registry 无会话，如 bridge 重启后重开）→
// 诚实 projection.not_found、retryable=false、不携带 data（禁止空壳）。
func TestLiveOnlyProjectionDeadSessionIsNotFound(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	conn, _ := liveOnlyProjectionPull(h, "dsh-ghost", 0)
	if conn.err == nil {
		t.Fatalf("dead live-only session must fail honestly, got success data=%T", conn.data)
	}
	if conn.data != nil {
		t.Fatalf("error must not pair with data (no empty shell): %T", conn.data)
	}
	if conn.err.Code != "projection.not_found" {
		t.Fatalf("code = %q, want projection.not_found", conn.err.Code)
	}
	if conn.err.Retryable == nil || *conn.err.Retryable {
		t.Fatalf("not_found must be explicitly nonretryable: %+v", conn.err)
	}
	if conn.err.Message == "" {
		t.Fatal("not_found must carry a message")
	}
}

// T5（C2）：kernel 有状态、live 进程已死（registry 无会话）→ 照常服务最后已知状态
// （含终态 execution），不报错、不空壳。
func TestLiveOnlyProjectionDeadProcessWithStateStillServes(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

	liveOnlyPublishTurn(h, "deepseek", "dsh-dead-1", "T1", []string{"last", "words"}, "turn_aborted")

	conn, _ := liveOnlyProjectionPull(h, "dsh-dead-1", 0)
	proj := liveOnlyProjectionOf(t, conn)
	if len(proj.Turns) != 1 {
		t.Fatalf("turns = %+v, want the dead session's last-known turn", proj.Turns)
	}
	if got := proj.Turns[0].Status; got != "aborted" {
		t.Fatalf("terminal status = %q, want aborted", got)
	}
	if proj.Execution.Phase == "running" {
		t.Fatalf("execution phase = running after terminal event, want settled: %+v", proj.Execution)
	}
}

// T6（C5）：deepseek 不进磁盘 hydrate 允许清单；source 准备路径对其独立拒绝；
// live-only 判定只覆盖 deepseek。
func TestLiveOnlyProjectionPathGuards(t *testing.T) {
	if backendSupportsProjectionHydrate("deepseek") {
		t.Fatal("deepseek must NOT be added to the disk hydrate allowlist (spec C5)")
	}
	if !backendUsesLiveOnlyProjection("deepseek") {
		t.Fatal("backendUsesLiveOnlyProjection(deepseek) must be true")
	}
	for _, b := range []string{"codex", "claude", "claudecode", "opencode", "grokbuild"} {
		if backendUsesLiveOnlyProjection(b) {
			t.Fatalf("backendUsesLiveOnlyProjection(%q) must be false", b)
		}
	}
	h := NewHandlers()
	defer h.Shutdown(context.Background())
	if _, err := h.prepareProjectionHydrateSource(context.Background(), "deepseek", "s", ""); !errors.Is(err, errProjectionBackendNotMigrated) {
		t.Fatalf("prepareProjectionHydrateSource(deepseek) = %v, want errProjectionBackendNotMigrated", err)
	}
}

// T8（附带修复 A）：live-only backend 的死会话（无 live registry 会话、无 kernel 状态）
// 从观察集剪枝；其他 backend 的未知会话观察不受影响（外部 turn 不是 registry 会话）。
func TestObservationPrunesDeadLiveOnlySessions(t *testing.T) {
	h := NewHandlers()
	defer h.Shutdown(context.Background())

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
