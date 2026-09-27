package gobridge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	codexremote "github.com/openAgi2/cordcode-macbridge/agent/codex-remote"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// S-1a（断线韧性方案 §3.1）：source_inspection_failed 的终态/瞬态分类。
// ErrNotConfigured（无持久化身份 / revoked→phase=failed）与 errProjectionSourceUnavailable
// （backend 未挂载 / RichHistoryProvider 能力缺失——静态条件，本连接周期内不会自愈）归终态
// retryable=false，iOS 保留硬错误与重新配对语义；ctx 取消等其余错误维持瞬态
// retryable=true + RetryAt 退避；mid-hydrate 落点（source_read_failed 等）不属本分类面，
// 维持 retryable=true 不变。

type notConfiguredProjectionAgent struct {
	fakeAgent
}

func (a *notConfiguredProjectionAgent) WaitForRestore(context.Context) error {
	return codexremote.ErrNotConfigured
}

type transientRestoreProjectionAgent struct {
	fakeAgent
}

func (a *transientRestoreProjectionAgent) WaitForRestore(context.Context) error {
	return context.Canceled
}

// noRichHistoryAgent 实现 core.Agent + RestoreWaiter 但刻意不实现 RichHistoryProvider，
// 驱动 codex-remote pathless 分支的 errProjectionSourceUnavailable（终态类）。
type noRichHistoryAgent struct {
	name string
}

func (a *noRichHistoryAgent) Name() string { return a.name }

func (a *noRichHistoryAgent) StartSession(context.Context, string) (core.AgentSession, error) {
	return nil, errors.New("not implemented")
}

func (a *noRichHistoryAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}

func (a *noRichHistoryAgent) Stop() error { return nil }

func (a *noRichHistoryAgent) WaitForRestore(context.Context) error { return nil }

func pullProjectionExpectFailure(t *testing.T, handlers *Handlers, agent core.Agent, sessionID string) *WireError {
	t.Helper()
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID})
	msg := WireMessage{RequestID: t.Name(), BackendID: "codex-remote", Method: "get_session_projection", Params: params}
	handlers.handleGetSessionProjection(conn, msg, agent)
	if conn.err == nil {
		t.Fatalf("expected hydrate failure, got success data=%T", conn.data)
	}
	if conn.err.Code != "projection.hydrate_failed" {
		t.Fatalf("error code = %q, want projection.hydrate_failed", conn.err.Code)
	}
	if conn.err.Retryable == nil {
		t.Fatalf("hydrate_failed must carry explicit retryability: %+v", conn.err)
	}
	return conn.err
}

func TestProjectionSourceNotConfiguredIsTerminal(t *testing.T) {
	handlers := NewHandlers()
	agent := &notConfiguredProjectionAgent{fakeAgent: fakeAgent{name: "codex-remote"}}
	handlers.RegisterAgent("codex-remote", agent)
	err := pullProjectionExpectFailure(t, handlers, agent, "s1a-not-configured")
	if *err.Retryable {
		t.Fatalf("ErrNotConfigured must be terminal (retryable=false), got %+v", err)
	}
	if err.RetryAfterMillis != nil {
		t.Fatalf("terminal failure must not carry retryAfterMillis, got %+v", err)
	}
}

func TestProjectionSourceUnavailableIsTerminal(t *testing.T) {
	handlers := NewHandlers()
	agent := &noRichHistoryAgent{name: "codex-remote"}
	handlers.RegisterAgent("codex-remote", agent)
	err := pullProjectionExpectFailure(t, handlers, agent, "s1a-source-unavailable")
	if *err.Retryable {
		t.Fatalf("errProjectionSourceUnavailable must be terminal (retryable=false), got %+v", err)
	}
	if err.RetryAfterMillis != nil {
		t.Fatalf("terminal failure must not carry retryAfterMillis, got %+v", err)
	}
}

func TestProjectionSourceTransientErrorStaysRetryable(t *testing.T) {
	handlers := NewHandlers()
	agent := &transientRestoreProjectionAgent{fakeAgent: fakeAgent{name: "codex-remote"}}
	handlers.RegisterAgent("codex-remote", agent)
	err := pullProjectionExpectFailure(t, handlers, agent, "s1a-transient")
	if !*err.Retryable {
		t.Fatalf("ctx cancel must stay retryable=true, got %+v", err)
	}
	if err.RetryAfterMillis == nil {
		t.Fatalf("retryable failure must carry retryAfterMillis, got %+v", err)
	}
}

func TestProjectionMidHydrateReadFailureStaysRetryable(t *testing.T) {
	handlers := NewHandlers()
	agent := &fakeAgent{name: "codex-remote", richHistoryErr: errors.New("transport hiccup")}
	handlers.RegisterAgent("codex-remote", agent)
	err := pullProjectionExpectFailure(t, handlers, agent, "s1a-mid-hydrate")
	if !*err.Retryable {
		t.Fatalf("mid-hydrate source_read_failed must keep retryable=true, got %+v", err)
	}
}
