package gobridge

// handlers_session_commands_test.go — DSH 命令面板 RPC（docs/2026-09-04 §5.2）：
// 无接口 not_supported、sessionId/line 空 invalid_params、成功 {commands}/{ok}、
// execute 失败透传；capability 仅 SessionCommandCatalog 实现者广告。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type sessionCommandCatalogAgent struct {
	*fakeAgent
	commands      []core.SessionCommand
	executeCalls  []string
	executeResult core.SessionCommandResult
	executeErr    error
}

func (a *sessionCommandCatalogAgent) ListSessionCommands(ctx context.Context, sessionID string) ([]core.SessionCommand, error) {
	return a.commands, nil
}

func (a *sessionCommandCatalogAgent) ExecuteSessionCommand(ctx context.Context, sessionID, line string) (core.SessionCommandResult, error) {
	a.executeCalls = append(a.executeCalls, sessionID+"|"+line)
	return a.executeResult, a.executeErr
}

func TestSessionCommandsCapabilityOnlyForCatalogAgents(t *testing.T) {
	catalog := &sessionCommandCatalogAgent{fakeAgent: &fakeAgent{name: "dsh-web"}}
	found := false
	for _, c := range deriveBackendCapabilities("dsh-web", catalog, "") {
		if c == "session_commands" {
			found = true
		}
	}
	if !found {
		t.Fatal("dsh-web (SessionCommandCatalog) must advertise session_commands")
	}
	// 其他 backend 不实现接口 → 不广告（iOS 据此不画 / 按钮）。
	for _, id := range []string{"grok", "claude", "codex-remote", "opencode-web"} {
		for _, c := range deriveBackendCapabilities(id, &fakeAgent{name: id}, "") {
			if c == "session_commands" {
				t.Fatalf("%s must NOT advertise session_commands (no SessionCommandCatalog)", id)
			}
		}
	}
}

func TestListSessionCommandsHandler(t *testing.T) {
	agent := &sessionCommandCatalogAgent{
		fakeAgent: &fakeAgent{name: "dsh-web"},
		commands: []core.SessionCommand{
			{Name: "compact", Description: "Compact older conversation history"},
			{Name: "export", Description: "Download this Session log as a ZIP archive"},
			{Name: "feedback", Description: "record feedback about this session", Hint: "<text>"},
			{Name: "goal", Description: "set or view the goal for a long-running task", Hint: "[<objective>|clear|edit <objective>|pause|resume]"},
			{Name: "permission", Description: "Switch the permission preset (sandbox mode + approval policy)", Hint: "<preset>"},
			{Name: "plan", Description: "Enter or leave plan mode", Hint: "[off|message]"},
		},
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("dsh-web", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web",
		Method:    "list_session_commands",
		RequestID: "sc-1",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "sess-1"}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	commands, _ := data["commands"].([]any)
	if len(commands) != 6 {
		t.Fatalf("commands len = %d, want 6", len(commands))
	}
	first, _ := commands[0].(map[string]any)
	if first["name"] != "compact" || first["hint"] != "" {
		t.Fatalf("compact row = %#v (hint must be present-and-empty, not omitted)", first)
	}
	plan, _ := commands[5].(map[string]any)
	if plan["name"] != "plan" || plan["hint"] != "[off|message]" {
		t.Fatalf("plan row = %#v", plan)
	}
}

func TestListSessionCommandsHandlerRejections(t *testing.T) {
	// 无接口 → not_supported（旧客户端/其他 backend 的诚实拒绝）。
	plain := &fakeAgent{name: "codex"}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", plain)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "list_session_commands",
		RequestID: "sc-2",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "s"}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	if code, _ := messages[0]["error"].(map[string]any)["code"].(string); code != "not_supported" {
		t.Fatalf("code = %v, want not_supported", messages[0]["error"])
	}

	// sessionId 空 → invalid_params（session 域强依赖）。
	catalog := &sessionCommandCatalogAgent{fakeAgent: &fakeAgent{name: "dsh-web"}}
	handlers.RegisterAgent("dsh-web", catalog)
	serverConn2, clientConn2, cleanup2 := openTestConn(t)
	defer cleanup2()
	handlers.HandleRPC(serverConn2, WireMessage{
		BackendID: "dsh-web",
		Method:    "list_session_commands",
		RequestID: "sc-3",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": ""}),
	})
	messages = readJSONMaps(t, clientConn2, 1)
	if code, _ := messages[0]["error"].(map[string]any)["code"].(string); code != "invalid_params" {
		t.Fatalf("code = %v, want invalid_params", messages[0]["error"])
	}
}

func TestExecuteSessionCommandHandler(t *testing.T) {
	agent := &sessionCommandCatalogAgent{fakeAgent: &fakeAgent{name: "dsh-web"}}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("dsh-web", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web",
		Method:    "execute_session_command",
		RequestID: "sc-4",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "sess-1", "line": "/plan"}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if ok, _ := data["ok"].(bool); !ok {
		t.Fatalf("result = %#v, want {ok:true}", messages[0])
	}
	if len(agent.executeCalls) != 1 || agent.executeCalls[0] != "sess-1|/plan" {
		t.Fatalf("executeCalls = %v, want [sess-1|/plan]", agent.executeCalls)
	}

	// line 空 → invalid_params。
	serverConn2, clientConn2, cleanup2 := openTestConn(t)
	defer cleanup2()
	handlers.HandleRPC(serverConn2, WireMessage{
		BackendID: "dsh-web",
		Method:    "execute_session_command",
		RequestID: "sc-5",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "sess-1", "line": "  "}),
	})
	messages = readJSONMaps(t, clientConn2, 1)
	if code, _ := messages[0]["error"].(map[string]any)["code"].(string); code != "invalid_params" {
		t.Fatalf("code = %v, want invalid_params", messages[0]["error"])
	}

	// 执行失败（dsh-web 把官方 result.text 织进 error message）→ execute_failed 原文透传。
	agent.executeErr = errors.New("dsh-web: /export: The Web /export command does not accept a path.")
	serverConn3, clientConn3, cleanup3 := openTestConn(t)
	defer cleanup3()
	handlers.HandleRPC(serverConn3, WireMessage{
		BackendID: "dsh-web",
		Method:    "execute_session_command",
		RequestID: "sc-6",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "sess-1", "line": "/export"}),
	})
	messages = readJSONMaps(t, clientConn3, 1)
	errObj, _ := messages[0]["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "execute_failed" {
		t.Fatalf("code = %v, want execute_failed", errObj)
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "The Web /export command does not accept a path.") {
		t.Fatalf("official result.text must ride the failure message, got %q", msg)
	}
}

func TestExecuteSessionCommandHandlerStartsColdGrokActor(t *testing.T) {
	agent := &sessionCommandCatalogAgent{fakeAgent: &fakeAgent{name: "grokbuild"}}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("grokbuild", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "grokbuild",
		Method:    "execute_session_command",
		RequestID: "sc-grok-cold",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "grok-session-1",
			"directory": "/tmp/grok-project",
			"line":      "/compact",
		}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if ok, _ := data["ok"].(bool); !ok {
		t.Fatalf("result = %#v, want {ok:true}", messages[0])
	}
	if len(agent.startCalls) != 1 || agent.startCalls[0] != "grok-session-1" {
		t.Fatalf("StartSession calls = %v, want [grok-session-1]", agent.startCalls)
	}
	tracked, found := handlers.sessions.getForBackend("grok-session-1", "grokbuild")
	if !found || tracked == nil || tracked.session == nil || tracked.directory != "/tmp/grok-project" {
		t.Fatalf("cold Grok actor not registered with metadata: found=%v tracked=%+v", found, tracked)
	}
	if len(agent.executeCalls) != 1 || agent.executeCalls[0] != "grok-session-1|/compact" {
		t.Fatalf("executeCalls = %v, want cold actor followed by execute", agent.executeCalls)
	}
}

// 官方 settle 透传（2026-09-05「点了没反应」返工）：成功响应必须携带
// commandId/resultKind/resultText 供 iPhone 显示官方反馈；零值字段省键不发。
func TestExecuteSessionCommandHandlerSettlePassthrough(t *testing.T) {
	agent := &sessionCommandCatalogAgent{
		fakeAgent:     &fakeAgent{name: "dsh-web"},
		executeResult: core.SessionCommandResult{CommandID: "cmd-x-9", ResultKind: "success", ResultText: "Plan mode on. Use /plan off to leave."},
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("dsh-web", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web",
		Method:    "execute_session_command",
		RequestID: "sc-7",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "sess-1", "line": "/plan"}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if data["commandId"] != "cmd-x-9" || data["resultKind"] != "success" || data["resultText"] != "Plan mode on. Use /plan off to leave." {
		t.Fatalf("settle passthrough = %#v, want official commandId/kind/text verbatim", data)
	}

	// 零值 settle（官方 text 为空等）→ 只回 {ok:true}，不发空串/null 键。
	agent.executeResult = core.SessionCommandResult{}
	serverConn2, clientConn2, cleanup2 := openTestConn(t)
	defer cleanup2()
	handlers.HandleRPC(serverConn2, WireMessage{
		BackendID: "dsh-web",
		Method:    "execute_session_command",
		RequestID: "sc-8",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "sess-1", "line": "/plan"}),
	})
	messages = readJSONMaps(t, clientConn2, 1)
	data, _ = messages[0]["data"].(map[string]any)
	if ok, _ := data["ok"].(bool); !ok {
		t.Fatalf("result = %#v, want {ok:true}", data)
	}
	for _, key := range []string{"commandId", "resultKind", "resultText"} {
		if _, present := data[key]; present {
			t.Fatalf("empty settle must omit %q, got %#v", key, data)
		}
	}
}
