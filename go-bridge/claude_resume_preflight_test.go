package gobridge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type ownerCheckAgent struct {
	*unsupportedMutationAgent
	proc   core.LiveSessionProcess
	err    error
	block  bool
	starts int
}

type noListerOwnerAgent struct{ starts int }

func (a *noListerOwnerAgent) Name() string { return "claudecode" }
func (a *noListerOwnerAgent) StartSession(context.Context, string) (core.AgentSession, error) {
	a.starts++
	return &fakeAgentSession{id: "unexpected", events: make(chan core.Event)}, nil
}
func (a *noListerOwnerAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (a *noListerOwnerAgent) Stop() error { return nil }

func (a *ownerCheckAgent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.starts++
	return a.unsupportedMutationAgent.StartSession(ctx, sessionID)
}

func (a *ownerCheckAgent) LiveSessionProcess(ctx context.Context, sessionID string) (core.LiveSessionProcess, error) {
	if a.block {
		<-ctx.Done()
		return core.LiveSessionProcess{SessionID: sessionID}, ctx.Err()
	}
	if a.proc.SessionID == "" {
		a.proc.SessionID = sessionID
	}
	return a.proc, a.err
}

func (a *ownerCheckAgent) IsProcessAlive(context.Context, int) bool { return false }

func TestPreflightClaudeResume_ExecutingFailsRetryable(t *testing.T) {
	agent := &ownerCheckAgent{
		unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"},
		proc:                     core.LiveSessionProcess{SessionID: "s1", PID: 42, Live: true, Executing: true},
	}
	got := preflightClaudeResume(context.Background(), agent, "s1")
	if got == nil || got.Code != "session.held_by_external_worker" || got.Retryable == nil || !*got.Retryable {
		t.Fatalf("preflight error = %#v", got)
	}
}

// Owner 2026-08-28: Claude Desktop 打开着会话但空闲（live-but-idle）时不再拦截 —
// 只有 transcript 证明在跑任务才阻塞。
func TestPreflightClaudeResume_LiveButIdleAllowsResume(t *testing.T) {
	agent := &ownerCheckAgent{
		unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"},
		proc:                     core.LiveSessionProcess{SessionID: "s1", PID: 42, Live: true},
	}
	if got := preflightClaudeResume(context.Background(), agent, "s1"); got != nil {
		t.Fatalf("preflight error = %#v, want nil for live-but-idle", got)
	}
}

func TestPreflightClaudeResume_DeadAllowsResume(t *testing.T) {
	agent := &ownerCheckAgent{unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"}}
	if got := preflightClaudeResume(context.Background(), agent, "s1"); got != nil {
		t.Fatalf("preflight error = %#v, want nil", got)
	}
}

func TestPreflightClaudeResume_CheckFailureAndMissingListerFailClosed(t *testing.T) {
	for name, agent := range map[string]core.Agent{
		"check error": &ownerCheckAgent{
			unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"},
			err:                      errors.New("scan failed"),
		},
		"missing lister": &unsupportedMutationAgent{name: "claudecode"},
	} {
		t.Run(name, func(t *testing.T) {
			got := preflightClaudeResume(context.Background(), agent, "s1")
			if got == nil || got.Code != "session.owner_check_failed" || got.Retryable == nil || !*got.Retryable {
				t.Fatalf("preflight error = %#v", got)
			}
		})
	}
}

func TestPreflightClaudeResume_TimeoutFailsClosed(t *testing.T) {
	previous := claudeResumeOwnerCheckTimeout
	claudeResumeOwnerCheckTimeout = 10 * time.Millisecond
	t.Cleanup(func() { claudeResumeOwnerCheckTimeout = previous })
	agent := &ownerCheckAgent{
		unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"},
		block:                    true,
	}
	got := preflightClaudeResume(context.Background(), agent, "s1")
	if got == nil || got.Code != "session.owner_check_failed" {
		t.Fatalf("preflight error = %#v, want owner check failure", got)
	}
}

func TestPreflightClaudeResume_RootCancellationIsNotOwnerFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	agent := &ownerCheckAgent{unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"}}
	got := preflightClaudeResume(ctx, agent, "s1")
	if got == nil || got.Code != "request.cancelled" {
		t.Fatalf("preflight error = %#v, want request.cancelled", got)
	}
}

func TestHandleSendMessage_ClaudeResumePreflightBlocksStart(t *testing.T) {
	agent := &fakeAgent{
		name: "claudecode",
		liveProcesses: map[string]core.LiveSessionProcess{
			"owned-session": {SessionID: "owned-session", PID: 42, Live: true, Executing: true},
		},
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claude", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claude",
		Method:    "send_message",
		RequestID: "owner-check",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "owned-session",
			"content":   "must not send",
		}),
	})
	result := readJSONMaps(t, clientConn, 1)[0]
	wireErr, _ := result["error"].(map[string]any)
	if wireErr["code"] != "session.held_by_external_worker" || wireErr["retryable"] != true {
		t.Fatalf("wire error = %#v", wireErr)
	}
	if len(agent.startCalls) != 0 {
		t.Fatalf("StartSession calls = %v, want none", agent.startCalls)
	}
}

func TestHandleSendMessage_ClaudeResumeCheckErrorDoesNotStart(t *testing.T) {
	agent := &ownerCheckAgent{
		unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"},
		err:                      errors.New("scan failed"),
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claude", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claude",
		Method:    "send_message",
		RequestID: "owner-check-error",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "existing", "content": "must not send"}),
	})
	result := readJSONMaps(t, clientConn, 1)[0]
	wireErr, _ := result["error"].(map[string]any)
	if wireErr["code"] != "session.owner_check_failed" {
		t.Fatalf("wire error = %#v", wireErr)
	}
	if agent.starts != 0 {
		t.Fatalf("StartSession calls = %d, want none", agent.starts)
	}
}

func TestHandleSendMessage_ClaudeResumeMissingListerDoesNotStart(t *testing.T) {
	agent := &noListerOwnerAgent{}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claude", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claude",
		Method:    "send_message",
		RequestID: "owner-check-no-lister",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "existing", "content": "must not send"}),
	})
	result := readJSONMaps(t, clientConn, 1)[0]
	wireErr, _ := result["error"].(map[string]any)
	if wireErr["code"] != "session.owner_check_failed" {
		t.Fatalf("wire error = %#v", wireErr)
	}
	if agent.starts != 0 {
		t.Fatalf("StartSession calls = %d, want none", agent.starts)
	}
}

func TestHandleSendMessage_PreflightScopeExcludesNewAndOtherBackend(t *testing.T) {
	for _, tc := range []struct {
		name, backendID, sessionID string
		agent                      *fakeAgent
	}{
		{name: "claude pending", backendID: "claude", sessionID: "pending-new", agent: &fakeAgent{name: "claudecode"}},
		{name: "codex resume", backendID: "codex", sessionID: "existing", agent: &fakeAgent{name: "codex"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handlers := newTestHandlers(t)
			handlers.RegisterAgent(tc.backendID, tc.agent)
			serverConn, _, cleanup := openTestConn(t)
			defer cleanup()
			handlers.handleSendMessage(serverConn, WireMessage{
				BackendID: tc.backendID,
				RequestID: "scope",
				Params:    mustJSONRaw(t, map[string]any{"sessionId": tc.sessionID, "content": "ok"}),
			}, tc.agent)
			if len(tc.agent.startCalls) != 1 {
				t.Fatalf("StartSession calls = %v, want one", tc.agent.startCalls)
			}
		})
	}
}

// 打开即拉活（2026-09-05）：iOS 打开 claude 会话时 best-effort spawn——
// dead/live-idle 放行并注册；registry 已有会话不重复 spawn；外部 worker
// 正在执行时放弃。
func TestStartClaudeSessionOnOpen_SpawnsAndRegisters(t *testing.T) {
	h := newTestHandlers(t)
	agent := &ownerCheckAgent{unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"}}
	h.startClaudeSessionOnOpen(agent, "open-1", WireMessage{BackendID: "claudecode", RequestID: "r1"})

	deadline := time.After(3 * time.Second)
	for agent.starts == 0 {
		select {
		case <-deadline:
			t.Fatalf("open-spawn never called StartSession")
		case <-time.After(10 * time.Millisecond):
		}
	}
	h.mu.Lock()
	sess, ok := h.getSession("open-1")
	h.mu.Unlock()
	if !ok || sess == nil {
		t.Fatalf("open-spawn session not registered")
	}

	// registry 已有会话：再次 open 不重复 spawn。
	h.startClaudeSessionOnOpen(agent, "open-1", WireMessage{BackendID: "claudecode", RequestID: "r2"})
	time.Sleep(100 * time.Millisecond)
	if agent.starts != 1 {
		t.Fatalf("starts = %d, want 1 (existing session must skip)", agent.starts)
	}
}

func TestStartClaudeSessionOnOpen_ExecutingExternalWorkerSkips(t *testing.T) {
	h := newTestHandlers(t)
	agent := &ownerCheckAgent{
		unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"},
		proc:                     core.LiveSessionProcess{SessionID: "x", PID: 42, Live: true, Executing: true},
	}
	h.startClaudeSessionOnOpen(agent, "exec-1", WireMessage{BackendID: "claudecode", RequestID: "r1"})
	time.Sleep(200 * time.Millisecond)
	if agent.starts != 0 {
		t.Fatalf("starts = %d, executing external worker must skip", agent.starts)
	}
}

func TestStartClaudeSessionOnOpen_EmptySessionIDNoop(t *testing.T) {
	h := newTestHandlers(t)
	agent := &ownerCheckAgent{unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"}}
	h.startClaudeSessionOnOpen(agent, "", WireMessage{BackendID: "claudecode"})
	time.Sleep(50 * time.Millisecond)
	if agent.starts != 0 {
		t.Fatalf("empty session id must not spawn")
	}
}

// 打开即拉活入口修正（2026-09-05 复测）：iOS 打开 claude 会话的真实链路是
// set_observation_scope(full_stream)——full_stream 触发 open-spawn，milestones_only
// 旁路观察不拉活。
func TestSetObservationScopeFullStreamTriggersOpenSpawn(t *testing.T) {
	h := newTestHandlers(t)
	agent := &ownerCheckAgent{unsupportedMutationAgent: &unsupportedMutationAgent{name: "claudecode"}}
	h.agents["claude"] = agent
	conn := &relayBroadcastCaptureConn{device: &TrustedDeviceRecord{DeviceID: "dev_open1"}}

	scope := json.RawMessage(`{"backendId":"claude","sessionIds":["open-scope-1"],"deliveryMode":"full_stream","includeRunningSessionSignals":true,"leaseSeconds":90}`)
	h.handleSetObservationScope(conn, WireMessage{RequestID: "req-os1", BackendID: "claude", Params: scope})

	deadline := time.After(3 * time.Second)
	for agent.starts == 0 {
		select {
		case <-deadline:
			t.Fatalf("full_stream scope must trigger open-spawn")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// milestones_only 旁路观察不拉活。
	agent.starts = 0
	scopeMilestones := json.RawMessage(`{"backendId":"claude","sessionIds":["open-scope-2"],"deliveryMode":"milestones_only","includeRunningSessionSignals":true,"leaseSeconds":90}`)
	h.handleSetObservationScope(conn, WireMessage{RequestID: "req-os2", BackendID: "claude", Params: scopeMilestones})
	time.Sleep(200 * time.Millisecond)
	if agent.starts != 0 {
		t.Fatalf("milestones_only must not spawn, starts = %d", agent.starts)
	}
}
