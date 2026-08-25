package gobridge

// permission_closure_audit_test.go —— 审计 §3.1-A2：permission 收口对称性清算。
// codex-web（core.OfficialResolutionSource）：resolve 成功后 handler 不得本地乐观
// publish permission_resolved——收口唯一真相 = 官方 serverRequest/resolved 双泵
// per-epoch 投递（agent/codex-web interactions.go resolvedEvents，豁免卡审计
// §3.2-B1；官方 TUI 收口唯一路径 = ServerRequestResolved，app_server_events.rs:118-142）。
// 无官方 resolved 广播的 backend（dsh-web 起源 630fb8d：宿主 mux 帧到不了 SSV2
// 投影）保留本地收口行为。

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// auditClosureSession 在 fakeAgentSession 上加 OfficialResolutionSource 标记。
type auditClosureSession struct {
	*fakeAgentSession
	official bool
}

func (s *auditClosureSession) EmitsOfficialResolution() bool { return s.official }

// auditClosureAgent 实现 core.Agent + SessionPermissionResponder + 标记（无注册表
// session 的观察路径）。
type auditClosureAgent struct {
	calls int
}

func (a *auditClosureAgent) Name() string { return "codex-web" }
func (a *auditClosureAgent) StartSession(context.Context, string) (core.AgentSession, error) {
	return nil, context.Canceled
}
func (a *auditClosureAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (a *auditClosureAgent) Stop() error { return nil }
func (a *auditClosureAgent) RespondSessionPermission(_ context.Context, _, _ string, _ core.PermissionResult) error {
	a.calls++
	return nil
}
func (a *auditClosureAgent) EmitsOfficialResolution() bool { return true }

func permissionCardPending(t *testing.T, h *Handlers, backend, sessionID, requestID string) (pending, found bool) {
	t.Helper()
	proj, ok := h.projectionKernel.reducer.Snapshot(backend, sessionID)
	if !ok {
		return false, false
	}
	for _, turn := range proj.Turns {
		if turn.Assistant == nil {
			continue
		}
		for _, p := range turn.Assistant.Parts {
			if p.Type == "tool" && p.ItemID == requestID {
				return p.RequiresPermissionConfirmation, true
			}
		}
	}
	return false, false
}

func seedAuditPermissionCard(h *Handlers, backend, sessionID, requestID string) {
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: backend, SessionID: sessionID, Event: "turn_started", Data: map[string]interface{}{"turnId": "T1"}})
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: backend, SessionID: sessionID, Event: "permission_request", Data: map[string]interface{}{
		"requestId": requestID, "toolName": "Bash",
	}})
}

func waitAuditPermissionCard(t *testing.T, h *Handlers, backend, sessionID, requestID string, wantPending bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pending, found := permissionCardPending(t, h, backend, sessionID, requestID); found && pending == wantPending {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	pending, found := permissionCardPending(t, h, backend, sessionID, requestID)
	t.Fatalf("permission card state stuck: backend=%s found=%v pending=%v want_pending=%v", backend, found, pending, wantPending)
}

func resolvePermissionViaHandler(t *testing.T, h *Handlers, backend, sessionID, requestID, behavior string) *userInputCaptureConn {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"sessionId": sessionID, "requestId": requestID, "behavior": behavior})
	conn := &userInputCaptureConn{}
	h.handleResolvePermission(conn, WireMessage{Type: "request", RequestID: "req-a2", BackendID: backend, Method: "resolve_permission", Params: raw})
	return conn
}

// TestResolvePermissionCodexWebWaitsOfficialResolved（session 路径）：resolve 写官方
// 成功后卡片必须保持 pending——本地零乐观收口；官方 permission_resolved
// （per-pump resolvedEvents 映射产物）到达后卡片立即收口。收口事件源 = 官方
// serverRequest/resolved 驱动。
func TestResolvePermissionCodexWebWaitsOfficialResolved(t *testing.T) {
	h := newTestHandlers(t)
	sess := &auditClosureSession{fakeAgentSession: &fakeAgentSession{id: "th-a2", events: make(chan core.Event, 8)}, official: true}
	h.putSessionWithMeta("th-a2", "codex-web", "", sess)
	seedAuditPermissionCard(h, "codex-web", "th-a2", "th-a2:cmd_1")
	waitAuditPermissionCard(t, h, "codex-web", "th-a2", "th-a2:cmd_1", true)

	if conn := resolvePermissionViaHandler(t, h, "codex-web", "th-a2", "th-a2:cmd_1", "allow"); conn.wireErr != nil {
		t.Fatalf("resolve err = %+v", conn.wireErr)
	}
	// 留足 publisher 异步批次窗口：本地乐观收口若存在必已落投影。
	time.Sleep(400 * time.Millisecond)
	if pending, found := permissionCardPending(t, h, "codex-web", "th-a2", "th-a2:cmd_1"); !found || !pending {
		t.Fatalf("codex-web 卡片在官方 resolved 前必须保持 pending（本地乐观收口违规）: found=%v pending=%v", found, pending)
	}

	// 官方路径收口：per-pump resolvedEvents → go-bridge/events.go → permission_resolved。
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: "codex-web", SessionID: "th-a2", Event: "permission_resolved",
		Data: map[string]interface{}{"requestId": "th-a2:cmd_1", "behavior": "allow"}})
	waitAuditPermissionCard(t, h, "codex-web", "th-a2", "th-a2:cmd_1", false)
}

// TestResolvePermissionCodexWebAgentPathSkipsOptimisticClosure（观察路径，无注册表
// session）：标记 agent 承载应答时同样不做本地乐观收口。
func TestResolvePermissionCodexWebAgentPathSkipsOptimisticClosure(t *testing.T) {
	h := newTestHandlers(t)
	agent := &auditClosureAgent{}
	h.RegisterAgent("codex-web", agent)
	seedAuditPermissionCard(h, "codex-web", "th-a2-ag", "th-a2-ag:cmd_2")
	waitAuditPermissionCard(t, h, "codex-web", "th-a2-ag", "th-a2-ag:cmd_2", true)

	if conn := resolvePermissionViaHandler(t, h, "codex-web", "th-a2-ag", "th-a2-ag:cmd_2", "deny"); conn.wireErr != nil {
		t.Fatalf("resolve err = %+v", conn.wireErr)
	}
	if agent.calls != 1 {
		t.Fatalf("agent RespondSessionPermission calls = %d, want 1", agent.calls)
	}
	time.Sleep(400 * time.Millisecond)
	if pending, found := permissionCardPending(t, h, "codex-web", "th-a2-ag", "th-a2-ag:cmd_2"); !found || !pending {
		t.Fatalf("观察路径同样必须等待官方 resolved: found=%v pending=%v", found, pending)
	}
}

// TestResolvePermissionNonOfficialBackendKeepsLocalClosure：无官方 resolved 广播的
// backend（dsh-web 起源 630fb8d——宿主 mux 帧到不了 SSV2 投影）保留本地乐观收口，
// 卡片在 resolve 成功后立即关闭。
func TestResolvePermissionNonOfficialBackendKeepsLocalClosure(t *testing.T) {
	h := newTestHandlers(t)
	sess := &auditClosureSession{fakeAgentSession: &fakeAgentSession{id: "ses-dsh", events: make(chan core.Event, 8)}, official: false}
	h.putSessionWithMeta("ses-dsh", "dsh-web", "", sess)
	seedAuditPermissionCard(h, "dsh-web", "ses-dsh", "appr-dsh")
	waitAuditPermissionCard(t, h, "dsh-web", "ses-dsh", "appr-dsh", true)

	if conn := resolvePermissionViaHandler(t, h, "dsh-web", "ses-dsh", "appr-dsh", "allow"); conn.wireErr != nil {
		t.Fatalf("resolve err = %+v", conn.wireErr)
	}
	waitAuditPermissionCard(t, h, "dsh-web", "ses-dsh", "appr-dsh", false)
}
