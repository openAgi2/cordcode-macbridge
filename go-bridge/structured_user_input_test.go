package gobridge

// structured_user_input_test.go 覆盖 go-bridge resolve_user_input handler（设计 §7/§10.1）：
//
//   - 成功：调用 core.UserInputResponder.ResolveUserInput → 回 outcome/status。
//   - *core.UserInputError → WireError 保留稳定 code；其它 error → resolve_user_input_failed。
//   - 非 responder（未声明 structured_user_input_v1 能力）→ response_not_supported（fail-closed）。
//   - 缺 interactionId / 非法 action → invalid_params；session 不存在 → session_not_found。
//   - dispatchRPC 把 resolve_user_input 路由到 handler（params 无 directory，不触碰 agent）。

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// userInputMockSession 嵌入 mockSession 满足 core.AgentSession，并实现 core.UserInputResponder。
type userInputMockSession struct {
	mockSession
	resolveFunc func(ctx context.Context, interactionID, clientActionID string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error)
}

func (m *userInputMockSession) ResolveUserInput(ctx context.Context, iid, clientActionID string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	if m.resolveFunc == nil {
		return core.UserInputResolution{}, &core.UserInputError{Code: "test_no_func", Message: "resolveFunc not configured"}
	}
	return m.resolveFunc(ctx, iid, clientActionID, action, answers)
}

// userInputCaptureConn 捕获 SendResult 的 data 与 error。
type userInputCaptureConn struct {
	data    any
	wireErr *WireError
}

func (c *userInputCaptureConn) SendJSON(any) {}
func (c *userInputCaptureConn) SendResult(_ string, data any, err *WireError) {
	c.data = data
	c.wireErr = err
}
func (c *userInputCaptureConn) SendEvent(string, string, string, any) {}
func (c *userInputCaptureConn) AuthedDevice() *TrustedDeviceRecord    { return nil }
func (c *userInputCaptureConn) RemoteAddr() string                    { return "test" }
func (c *userInputCaptureConn) Close() error                          { return nil }

func resolveMsg(t *testing.T, params map[string]any) WireMessage {
	t.Helper()
	if _, ok := params["clientActionId"]; !ok {
		params["clientActionId"] = "f40f8934-8f3d-4e5f-a9b5-883b6a8f5147"
	}
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return WireMessage{RequestID: "req-1", BackendID: "claude", Method: "resolve_user_input", Params: b}
}

func seedUserInputProjection(h *Handlers, backendID, sessionID, interactionID string, canReject bool) {
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: backendID, SessionID: sessionID, Event: "turn_started", Data: map[string]interface{}{"turnId": "turn-1"}})
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: backendID, SessionID: sessionID, Event: "user_input_requested", Data: map[string]interface{}{
		"turnId": "turn-1", "interactionId": interactionID, "status": "pending", "questions": uiQuestionsWire(), "canRespond": true, "canReject": canReject,
	}})
}

func resolveProjection(h *Handlers, backendID, sessionID, interactionID, status string) {
	h.eventPublisher.PublishLogical(LogicalEvent{BackendID: backendID, SessionID: sessionID, Event: "user_input_resolved", Data: map[string]interface{}{
		"turnId": "turn-1", "interactionId": interactionID, "status": status, "source": "ios",
	}})
}

func TestResolveUserInput_SuccessOutcome(t *testing.T) {
	h := newTestHandlers(t)
	var seenIID string
	var seenAction core.UserInputAction
	var seenAnswers int
	sess := &userInputMockSession{resolveFunc: func(_ context.Context, iid, _ string, action core.UserInputAction, ans []core.UserInputAnswer) (core.UserInputResolution, error) {
		seenIID = iid
		seenAction = action
		seenAnswers = len(ans)
		resolveProjection(h, "claude", "ses_1", iid, "answered")
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
	}}
	h.putSessionWithMeta("ses_1", "claude", "", sess)
	seedUserInputProjection(h, "claude", "ses_1", "ui_abc", true)

	conn := &userInputCaptureConn{}
	h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{
		"sessionId": "ses_1", "interactionId": "ui_abc", "clientActionId": "f40f8934-8f3d-4e5f-a9b5-883b6a8f5147", "action": "answer",
		"answers": []map[string]any{{"questionId": "ui_abc_q_0", "values": []map[string]any{{"kind": "option", "optionId": "ui_abc_q_0_o_0"}}}},
	}), nil)

	if conn.wireErr != nil {
		t.Fatalf("应成功，实际 wireErr=%+v", conn.wireErr)
	}
	if seenIID != "ui_abc" || seenAction != core.UserInputActionAnswer || seenAnswers != 1 {
		t.Fatalf("resolver 收到 iid=%q action=%q answers=%d want ui_abc/answer/1", seenIID, seenAction, seenAnswers)
	}
	m, _ := conn.data.(map[string]any)
	if m["interactionId"] != "ui_abc" || m["outcome"] != core.UserInputOutcomeAccepted || m["currentStatus"] != core.UserInputStatusAnswered || m["headRev"] != 2 {
		t.Fatalf("result = %+v want canonical four-field result", conn.data)
	}
}

func TestResolveUserInput_UserInputErrorCodeMapped(t *testing.T) {
	h := newTestHandlers(t)
	sess := &userInputMockSession{resolveFunc: func(context.Context, string, string, core.UserInputAction, []core.UserInputAnswer) (core.UserInputResolution, error) {
		return core.UserInputResolution{}, &core.UserInputError{Code: "response_not_supported", Message: "codex reject not supported"}
	}}
	h.putSessionWithMeta("ses_1", "claude", "", sess)
	seedUserInputProjection(h, "claude", "ses_1", "ui_abc", true)

	conn := &userInputCaptureConn{}
	h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{"sessionId": "ses_1", "interactionId": "ui_abc", "action": "reject"}), nil)

	if conn.wireErr == nil || conn.wireErr.Code != "response_not_supported" {
		t.Fatalf("应把 *UserInputError.code 透传到 WireError.code，实际 %+v", conn.wireErr)
	}
	if conn.wireErr.Message != "codex reject not supported" {
		t.Fatalf("message = %q", conn.wireErr.Message)
	}
}

func TestResolveUserInput_GenericErrorMapped(t *testing.T) {
	h := newTestHandlers(t)
	sess := &userInputMockSession{resolveFunc: func(context.Context, string, string, core.UserInputAction, []core.UserInputAnswer) (core.UserInputResolution, error) {
		return core.UserInputResolution{}, errors.New("boom")
	}}
	h.putSessionWithMeta("ses_1", "claude", "", sess)
	conn := &userInputCaptureConn{}
	h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{"sessionId": "ses_1", "interactionId": "ui_abc", "action": "answer", "answers": []map[string]any{{"questionId": "q", "values": []map[string]any{{"kind": "text", "text": "x"}}}}}), nil)
	if conn.wireErr == nil || conn.wireErr.Code != "resolve_user_input_failed" {
		t.Fatalf("非 UserInputError 应 resolve_user_input_failed，实际 %+v", conn.wireErr)
	}
}

func TestResolveUserInput_NonResponderNotSupported(t *testing.T) {
	h := newTestHandlers(t)
	h.putSessionWithMeta("ses_1", "claude", "", &mockSession{}) // 仅 core.AgentSession，未实现 UserInputResponder
	conn := &userInputCaptureConn{}
	h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{"sessionId": "ses_1", "interactionId": "ui_abc", "action": "answer", "answers": []map[string]any{{"questionId": "q", "values": []map[string]any{{"kind": "text", "text": "x"}}}}}), nil)
	if conn.wireErr == nil || conn.wireErr.Code != "response_not_supported" {
		t.Fatalf("非 responder（未声明能力）应 response_not_supported，实际 %+v", conn.wireErr)
	}
}

func TestResolveUserInput_SessionNotFound(t *testing.T) {
	h := newTestHandlers(t)
	conn := &userInputCaptureConn{}
	h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{"sessionId": "missing", "interactionId": "ui_abc", "action": "answer", "answers": []map[string]any{{"questionId": "q", "values": []map[string]any{{"kind": "text", "text": "x"}}}}}), nil)
	if conn.wireErr == nil || conn.wireErr.Code != "session_not_found" {
		t.Fatalf("应 session_not_found，实际 %+v", conn.wireErr)
	}
}

func TestResolveUserInput_InvalidParamsMissingInteractionID(t *testing.T) {
	h := newTestHandlers(t)
	conn := &userInputCaptureConn{}
	h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{"sessionId": "ses_1", "action": "answer"}), nil)
	if conn.wireErr == nil || conn.wireErr.Code != "invalid_params" {
		t.Fatalf("缺 interactionId 应 invalid_params，实际 %+v", conn.wireErr)
	}
}

func TestResolveUserInput_InvalidParamsBadAction(t *testing.T) {
	h := newTestHandlers(t)
	conn := &userInputCaptureConn{}
	h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{"sessionId": "ses_1", "interactionId": "ui_abc", "action": "bogus"}), nil)
	if conn.wireErr == nil || conn.wireErr.Code != "invalid_params" {
		t.Fatalf("非法 action 应 invalid_params，实际 %+v", conn.wireErr)
	}
}

func TestResolveUserInput_EnvelopeAndBackendScopeValidation(t *testing.T) {
	t.Run("invalid UUID", func(t *testing.T) {
		h := newTestHandlers(t)
		conn := &userInputCaptureConn{}
		h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{
			"sessionId": "ses_1", "interactionId": "ui_abc", "clientActionId": "not-a-uuid", "action": "answer",
			"answers": []map[string]any{{"questionId": "q", "values": []map[string]any{{"kind": "text", "text": "x"}}}},
		}), nil)
		if conn.wireErr == nil || conn.wireErr.Code != "invalid_params" {
			t.Fatalf("invalid UUID accepted: %+v", conn.wireErr)
		}
	})
	t.Run("answer requires answers", func(t *testing.T) {
		h := newTestHandlers(t)
		conn := &userInputCaptureConn{}
		h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{"sessionId": "ses_1", "interactionId": "ui_abc", "action": "answer"}), nil)
		if conn.wireErr == nil || conn.wireErr.Code != "invalid_params" {
			t.Fatalf("missing answers accepted: %+v", conn.wireErr)
		}
	})
	t.Run("reject omits answers", func(t *testing.T) {
		h := newTestHandlers(t)
		conn := &userInputCaptureConn{}
		h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{"sessionId": "ses_1", "interactionId": "ui_abc", "action": "reject", "answers": []any{}}), nil)
		if conn.wireErr == nil || conn.wireErr.Code != "invalid_params" {
			t.Fatalf("reject answers accepted: %+v", conn.wireErr)
		}
	})
	t.Run("backend mismatch", func(t *testing.T) {
		h := newTestHandlers(t)
		h.putSessionWithMeta("shared-id", "codex", "", &userInputMockSession{})
		conn := &userInputCaptureConn{}
		h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{
			"sessionId": "shared-id", "interactionId": "ui_abc", "action": "answer",
			"answers": []map[string]any{{"questionId": "q", "values": []map[string]any{{"kind": "text", "text": "x"}}}},
		}), nil)
		if conn.wireErr == nil || conn.wireErr.Code != "session_not_found" {
			t.Fatalf("cross-backend route accepted: %+v", conn.wireErr)
		}
	})
}

func TestResolveUserInput_ClaimedReturnsInProgressWithoutFakeTerminalState(t *testing.T) {
	h := newTestHandlers(t)
	sess := &userInputMockSession{resolveFunc: func(context.Context, string, string, core.UserInputAction, []core.UserInputAnswer) (core.UserInputResolution, error) {
		return core.UserInputResolution{Outcome: core.UserInputOutcomeInProgress, CurrentStatus: core.UserInputStatusPending}, nil
	}}
	h.putSessionWithMeta("ses_1", "claude", "", sess)
	seedUserInputProjection(h, "claude", "ses_1", "ui_abc", false)
	conn := &userInputCaptureConn{}
	h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{
		"sessionId": "ses_1", "interactionId": "ui_abc", "action": "answer",
		"answers": []map[string]any{{"questionId": "q", "values": []map[string]any{{"kind": "text", "text": "x"}}}},
	}), nil)
	if conn.wireErr != nil {
		t.Fatal(conn.wireErr)
	}
	result := conn.data.(map[string]any)
	if result["outcome"] != core.UserInputOutcomeInProgress || result["currentStatus"] != core.UserInputStatusPending || result["headRev"] != 1 {
		t.Fatalf("claimed result = %+v", result)
	}
}

func TestResolveUserInput_RejectRequiresProjectedCanReject(t *testing.T) {
	h := newTestHandlers(t)
	called := false
	sess := &userInputMockSession{resolveFunc: func(context.Context, string, string, core.UserInputAction, []core.UserInputAnswer) (core.UserInputResolution, error) {
		called = true
		return core.UserInputResolution{}, nil
	}}
	h.putSessionWithMeta("ses_1", "claude", "", sess)
	seedUserInputProjection(h, "claude", "ses_1", "ui_abc", false)
	conn := &userInputCaptureConn{}
	h.handleResolveUserInput(conn, resolveMsg(t, map[string]any{"sessionId": "ses_1", "interactionId": "ui_abc", "action": "reject"}), nil)
	if conn.wireErr == nil || conn.wireErr.Code != "response_not_supported" || called {
		t.Fatalf("canReject=false result=%+v called=%v", conn.wireErr, called)
	}
}

// dispatch 路由：resolve_user_input 经 dispatchRPC 命中 handler（params 无 directory → 不触碰 nil agent）。
func TestResolveUserInput_DispatchRouting(t *testing.T) {
	h := newTestHandlers(t)
	called := false
	sess := &userInputMockSession{resolveFunc: func(context.Context, string, string, core.UserInputAction, []core.UserInputAnswer) (core.UserInputResolution, error) {
		called = true
		resolveProjection(h, "claude", "ses_1", "ui_abc", "answered")
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
	}}
	h.putSessionWithMeta("ses_1", "claude", "", sess)
	seedUserInputProjection(h, "claude", "ses_1", "ui_abc", false)
	conn := &userInputCaptureConn{}
	h.dispatchRPC(conn, resolveMsg(t, map[string]any{"sessionId": "ses_1", "interactionId": "ui_abc", "action": "answer", "answers": []map[string]any{{"questionId": "q", "values": []map[string]any{{"kind": "text", "text": "x"}}}}}), nil)
	if !called {
		t.Fatalf("dispatchRPC 应把 resolve_user_input 路由到 handler 并调用 responder")
	}
	m, _ := conn.data.(map[string]any)
	if m["outcome"] != core.UserInputOutcomeAlreadyResolved {
		t.Fatalf("result outcome = %v want already_resolved", m["outcome"])
	}
}
