package dshweb

// commands_test.go — DSH 命令面板（docs/2026-09-04 §5.3）：官方 wire 形状
// （list 恰一个 args 键、键名 agentId、裸数组）+ 中间类型映射（compact/export
// 无 input → Hint 空；input.images 丢弃）+ execute 透传与失败语义（官方
// error 原文、undefined miss = command not matched、绝不走 session.prompt）。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// officialListFixture：rc.2 官方活体六条（name 排序、compact/export 无 input 键）。
func officialListFixture() []map[string]any {
	return []map[string]any{
		{"name": "compact", "description": "Compact older conversation history"},
		{"name": "export", "description": "Download this Session log as a ZIP archive"},
		{"name": "feedback", "description": "record feedback about this session", "input": map[string]any{"hint": "<text>"}},
		{"name": "goal", "description": "set or view the goal for a long-running task", "input": map[string]any{"hint": "[<objective>|clear|edit <objective>|pause|resume]", "images": true}},
		{"name": "permission", "description": "Switch the permission preset (sandbox mode + approval policy)", "input": map[string]any{"hint": "<preset>"}},
		{"name": "plan", "description": "Enter or leave plan mode", "input": map[string]any{"hint": "[off|message]", "images": true}},
	}
}

func TestListSessionCommandsWireAndMapping(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["commands/list"] = fakeRPCResponse{value: officialListFixture()}

	commands, err := a.ListSessionCommands(context.Background(), "sess-cmd")
	if err != nil {
		t.Fatal(err)
	}

	// 官方 wire：方法名 commands/list；payload 恰一个 args 键，args 内键名 agentId。
	calls := methodCalls(f, "commands/list")
	if len(calls) != 1 {
		t.Fatalf("commands/list calls = %d, want 1", len(calls))
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(calls[0], &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 {
		t.Fatalf("payload must be exactly {args:…} (gateway single-args rule), keys = %v", payloadKeys(payload))
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(payload["args"], &args); err != nil {
		t.Fatal(err)
	}
	if string(args["agentId"]) != `"sess-cmd"` {
		t.Fatalf("args.agentId = %s, want \"sess-cmd\"", args["agentId"])
	}
	if _, has := args["sessionId"]; has {
		t.Fatal("args key must be agentId, not sessionId (gateway rejects missing \"agentId\")")
	}

	// 裸数组映射：6 条；compact/export 无 input → Hint 空；plan hint 保真；images 丢弃。
	if len(commands) != 6 {
		t.Fatalf("commands len = %d, want 6", len(commands))
	}
	byName := map[string]struct {
		Description string
		Hint        string
	}{}
	for _, cmd := range commands {
		byName[cmd.Name] = struct {
			Description string
			Hint        string
		}{cmd.Description, cmd.Hint}
	}
	for _, noInput := range []string{"compact", "export"} {
		if byName[noInput].Hint != "" {
			t.Fatalf("%s has no official input key; Hint must stay empty, got %q", noInput, byName[noInput].Hint)
		}
	}
	if byName["plan"].Description != "Enter or leave plan mode" || byName["plan"].Hint != "[off|message]" {
		t.Fatalf("plan mapped = %+v", byName["plan"])
	}
	if byName["permission"].Hint != "<preset>" {
		t.Fatalf("permission hint = %q, want <preset>", byName["permission"].Hint)
	}
}

func payloadKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestListSessionCommandsDropsEmptyName(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["commands/list"] = fakeRPCResponse{value: []map[string]any{
		{"name": "", "description": "ghost"},
		{"name": "plan", "description": "Enter or leave plan mode"},
	}}
	commands, err := a.ListSessionCommands(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0].Name != "plan" {
		t.Fatalf("empty-name descriptors must be dropped, got %+v", commands)
	}
}

func TestListSessionCommandsRejectsEmptySession(t *testing.T) {
	a := &Agent{}
	if _, err := a.ListSessionCommands(context.Background(), ""); err == nil {
		t.Fatal("empty session id must be rejected")
	}
}

func TestExecuteSessionCommandLinePassthrough(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["commands/execute"] = fakeRPCResponse{value: map[string]any{
		"commandId": "cmd-x-9",
		"result":    map[string]any{"kind": "success", "text": "Plan mode on. Use /plan off to leave."},
	}}

	res, err := a.ExecuteSessionCommand(context.Background(), "sess-cmd", "/plan")
	if err != nil {
		t.Fatal(err)
	}
	// 官方 settle 逐字映射（2026-09-05「点了没反应」返工：成功反馈的来源）。
	if res.CommandID != "cmd-x-9" || res.ResultKind != "success" || res.ResultText != "Plan mode on. Use /plan off to leave." {
		t.Fatalf("settle = %#v, want official commandId/kind/text verbatim", res)
	}
	// 命令是 host 动作：绝不经 session.prompt（否则 /plan 变聊天）。
	if n := len(methodCalls(f, "session.prompt")); n != 0 {
		t.Fatalf("session.prompt called %d times; /plan must never become a user turn", n)
	}
	calls := methodCalls(f, "commands/execute")
	if len(calls) != 1 {
		t.Fatalf("commands/execute calls = %d, want 1", len(calls))
	}
	var req commandsExecuteRequest
	if err := json.Unmarshal(calls[0], &req); err != nil {
		t.Fatal(err)
	}
	if req.Args.AgentID != "sess-cmd" {
		t.Fatalf("agentId = %q, want sess-cmd", req.Args.AgentID)
	}
	if req.Args.Line != "/plan" {
		t.Fatalf("line = %q, want /plan (verbatim)", req.Args.Line)
	}
	// 部署座位网关按描述符强制要求 images 字段（2026-09-05 真机报障活体复核：
	// 缺 images → `missing "images"`；images:null → boundary validation 失败）。
	// 必须精确序列化为 []（非 null、非缺键）。
	var argsEnvelope struct {
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(calls[0], &argsEnvelope); err != nil {
		t.Fatal(err)
	}
	var rawArgs map[string]json.RawMessage
	if err := json.Unmarshal(argsEnvelope.Args, &rawArgs); err != nil {
		t.Fatal(err)
	}
	if string(rawArgs["images"]) != "[]" {
		t.Fatalf("args.images must serialize as exactly [] (seat gateway rejects omission and null), got %s", rawArgs["images"])
	}
}

func TestExecuteSessionCommandValidation(t *testing.T) {
	a := &Agent{}
	for _, line := range []string{"", "plan", "/pl\nan", "/plan\r"} {
		if _, err := a.ExecuteSessionCommand(context.Background(), "s", line); err == nil {
			t.Fatalf("line %q must be rejected (empty / no slash / embedded newline)", line)
		}
	}
	if _, err := a.ExecuteSessionCommand(context.Background(), "", "/plan"); err == nil {
		t.Fatal("empty session id must be rejected")
	}
}

func TestExecuteSessionCommandErrorTextPassthrough(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["commands/execute"] = fakeRPCResponse{value: map[string]any{
		"commandId": "cmd-e-1",
		"result":    map[string]any{"kind": "error", "text": "The Web /export command does not accept a path."},
	}}
	_, err := a.ExecuteSessionCommand(context.Background(), "s", "/export /tmp/x")
	if err == nil || !strings.Contains(err.Error(), "The Web /export command does not accept a path.") {
		t.Fatalf("official error text must be the failure message, got %v", err)
	}
}

func TestExecuteSessionCommandUndefinedMiss(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	// 官方未知命令返回 undefined（无 value）不写日志；解码为零值结构体——
	// commandId 与 result.kind 都空 = 未命中，必须当失败（permission 路径同判）。
	f.handlers["commands/execute"] = fakeRPCResponse{}
	_, err := a.ExecuteSessionCommand(context.Background(), "s", "/nope")
	if err == nil || !strings.Contains(err.Error(), "command not matched") {
		t.Fatalf("undefined miss must fail as command not matched, got %v", err)
	}
}

// rework ⑨ 回归（2026-09-06 owner /compact 02:17:38 报障）：wire 层共享
// client 曾带 30s http.Client.Timeout 硬顶——Go 语义里 per-request ctx 只能
// 缩短、不能延长它，handler 的长预算从未生效，POST /api/commands/execute
// 在 30s 整被掐断，官方 execute 的请求级 signal 随之把压缩中止为
// "This operation was aborted"。三条不变量：默认 client 不带总超时；
// 调用方自带期限时以调用方为准（长过默认值也能活到响应）；无期限 ctx
// 仍被默认 30s 兜底（既有短调用语义不变）。
func TestCallCallerDeadlineBeatsDefaultUnaryTimeout(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	const delay = 350 * time.Millisecond
	f.hooks["commands/execute"] = func(payload []byte) fakeRPCResponse {
		time.Sleep(delay)
		return fakeRPCResponse{value: map[string]any{"commandId": "cmd-long"}}
	}
	client := NewClient(f.URL(), nil)

	if d := client.httpClient.Timeout; d != 0 {
		t.Fatalf("default client must not carry a blanket Timeout (rework ⑨), got %v", d)
	}

	old := defaultUnaryTimeout
	defaultUnaryTimeout = 120 * time.Millisecond
	defer func() { defaultUnaryTimeout = old }()

	// 调用方期限 3s > 默认 120ms：延迟响应必须被等到（官方压缩即此形）。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out struct {
		CommandID string `json:"commandId"`
	}
	if err := client.Call(ctx, "commands/execute", map[string]any{"args": map[string]any{}}, &out); err != nil {
		t.Fatalf("caller deadline (3s) must beat the shrunken default (120ms): %v", err)
	}
	if out.CommandID != "cmd-long" {
		t.Fatalf("delayed value must be decoded, got %q", out.CommandID)
	}

	// 无期限 ctx：默认兜底仍然生效（短调用语义保持）。
	if err := client.Call(context.Background(), "commands/execute", map[string]any{}, nil); err == nil {
		t.Fatal("deadline-less call must still hit the default unary timeout")
	}
}
