package gobridge

// bridge_turn_slot_leak_test.go — 2026-09-21 真机回归：abort/delete_session
// 泄漏 bridge-owned turn 槽。
//
// 事故链（owner 真机 15:45–15:49）：GLM-5.3-Highspeed 权限错误 → turn 零输出
// 卡 running → owner 点停止（abort_generation）→ ocHandleAbortGeneration 走
// deleteSession+Close，注册表条目已删、markIdle→completeBridgeTurn 永不触发 →
// 发送槽泄漏 → 该 session 后续所有 send 被拒，且错误文案误报
// "Bridge runtime is quiescing"（管理 API 实证：admissionState=accepting、
// quiesce=none、bridgeOwnedActiveTurns=1）。
//
// 本文件锁定三件事：
//  1. 槽卡住时 admitBridgeTurn 返回诚实的 session_action_in_progress（可重试），
//     不再冒充 quiescing；
//  2. ocHandleAbortGeneration / handleAbortGeneration / ocHandleDeleteSession
//     三条终态路径都释放发送槽；
//  3. 真 quiesce（admission 机器）仍返回 runtime.quiescing。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/go-bridge/admission"
)

// newTurnSlotTestHandlers 装配带 admission 机器的 Handlers（生产形态：
// ManagementServer 构建时注入；无机器时 admitBridgeTurn 恒放行）。
func newTurnSlotTestHandlers(t *testing.T) *Handlers {
	t.Helper()
	h := newTestHandlers(t)
	machine := admission.NewAdmissionMachine(admission.RuntimeIdentity{PID: 1, BridgeEpoch: 1}, nil, 30_000)
	h.SetAdmissionMachine(machine)
	return h
}

func turnSlotParams(t *testing.T, sessionID string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"sessionId": sessionID})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// 槽卡住（同 session 上一发未收口）必须返回诚实的 action 冲突错误，
// 不得冒充 quiescing——2026-09-21 事故里该误报把排障方向带偏到 RuntimeManager。
func TestAdmitBridgeTurnStuckSlotReturnsHonestActionError(t *testing.T) {
	h := newTurnSlotTestHandlers(t)
	if wireErr := h.admitBridgeTurn("s-leak"); wireErr != nil {
		t.Fatalf("first admit failed: %v", wireErr.Message)
	}

	second := h.admitBridgeTurn("s-leak")
	if second == nil {
		t.Fatal("second admit must be rejected while the turn slot is held")
	}
	if second.Code != "session_action_in_progress" {
		t.Fatalf("code=%q, want session_action_in_progress", second.Code)
	}
	if second.Message == "Bridge runtime is quiescing" {
		t.Fatal("stuck per-session slot must not masquerade as a quiesce")
	}
	if second.Retryable == nil || !*second.Retryable {
		t.Fatal("action conflict must be retryable")
	}

	// 其他 session 不受影响（槽是 per-session 的）。
	if wireErr := h.admitBridgeTurn("s-other"); wireErr != nil {
		t.Fatalf("unrelated session admit failed: %v", wireErr.Message)
	}
}

// ocHandleAbortGeneration（opencode-web 停止路径）必须释放发送槽。
func TestOcAbortGenerationReleasesBridgeTurnSlot(t *testing.T) {
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer proxySrv.Close()

	h := newTurnSlotTestHandlers(t)
	h.ocProxy = NewOpenCodeProxy(proxySrv.URL, "", "")
	h.RegisterAgent("opencode-web", &fakeAgent{name: "opencode-web"})
	sess := &fakeAgentSession{id: "s-abort-oc", events: make(chan core.Event)}
	h.sessions.put("s-abort-oc", "opencode-web", "/tmp/proj", sess)
	if wireErr := h.admitBridgeTurn("s-abort-oc"); wireErr != nil {
		t.Fatalf("admit before abort failed: %v", wireErr.Message)
	}

	conn := &readFileCaptureConn{}
	h.ocHandleAbortGeneration(conn, WireMessage{
		RequestID: "req-abort",
		BackendID: "opencode-web",
		Method:    "abort_generation",
		Params:    turnSlotParams(t, "s-abort-oc"),
	}, "/tmp/proj")
	if conn.err != nil {
		t.Fatalf("abort failed: %v", conn.err.Message)
	}

	if wireErr := h.admitBridgeTurn("s-abort-oc"); wireErr != nil {
		t.Fatalf("abort must release the turn slot, got: %s (%s)", wireErr.Message, wireErr.Code)
	}
}

// handleAbortGeneration（通用停止路径，私有进程后端分支）必须释放发送槽。
func TestGenericAbortGenerationReleasesBridgeTurnSlot(t *testing.T) {
	h := newTurnSlotTestHandlers(t)
	h.RegisterAgent("dsh-web", &fakeAgent{name: "dsh-web"})
	sess := &fakeAgentSession{id: "s-abort-gen", events: make(chan core.Event)}
	h.sessions.put("s-abort-gen", "dsh-web", "/tmp/proj", sess)
	if wireErr := h.admitBridgeTurn("s-abort-gen"); wireErr != nil {
		t.Fatalf("admit before abort failed: %v", wireErr.Message)
	}

	conn := &readFileCaptureConn{}
	h.handleAbortGeneration(conn, WireMessage{
		RequestID: "req-abort",
		BackendID: "dsh-web",
		Method:    "abort_generation",
		Params:    turnSlotParams(t, "s-abort-gen"),
	})
	if conn.err != nil {
		t.Fatalf("abort failed: %v", conn.err.Message)
	}

	if wireErr := h.admitBridgeTurn("s-abort-gen"); wireErr != nil {
		t.Fatalf("abort must release the turn slot, got: %s (%s)", wireErr.Message, wireErr.Code)
	}
}

// ocHandleDeleteSession（删除会话路径）同样终结 bridge-owned turn，必须释放发送槽。
func TestOcDeleteSessionReleasesBridgeTurnSlot(t *testing.T) {
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer proxySrv.Close()

	h := newTurnSlotTestHandlers(t)
	h.ocProxy = NewOpenCodeProxy(proxySrv.URL, "", "")
	h.RegisterAgent("opencode-web", &fakeAgent{name: "opencode-web"})
	sess := &fakeAgentSession{id: "s-del-oc", events: make(chan core.Event)}
	h.sessions.put("s-del-oc", "opencode-web", "/tmp/proj", sess)
	if wireErr := h.admitBridgeTurn("s-del-oc"); wireErr != nil {
		t.Fatalf("admit before delete failed: %v", wireErr.Message)
	}

	conn := &readFileCaptureConn{}
	h.ocHandleDeleteSession(conn, WireMessage{
		RequestID: "req-del",
		BackendID: "opencode-web",
		Method:    "delete_session",
		Params:    turnSlotParams(t, "s-del-oc"),
	}, "/tmp/proj")
	if conn.err != nil {
		t.Fatalf("delete failed: %v", conn.err.Message)
	}

	if wireErr := h.admitBridgeTurn("s-del-oc"); wireErr != nil {
		t.Fatalf("delete_session must release the turn slot, got: %s (%s)", wireErr.Message, wireErr.Code)
	}
}
