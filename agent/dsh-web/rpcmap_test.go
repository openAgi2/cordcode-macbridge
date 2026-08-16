package dshweb

// §8-2 unit tests: the RPC mapping table row by row (design §4.3 functional
// surface), including the not_supported capability-absence assertions.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// newTestAgent builds an Agent bound to the fake instance (external probe
// hit), with host.describe answered.
func newTestAgent(t *testing.T, f *fakeDSHServer) *Agent {
	t.Helper()
	f.handlers["host.describe"] = fakeRPCResponse{value: map[string]any{
		"version": "0.0.1", "cwd": "/tmp", "attachedSessions": 0, "canOpenPath": false,
	}}
	a := &Agent{workDir: "/tmp/ios-dir"}
	a.resolver = NewResolver(WithProbeURLs([]string{f.URL()}))
	if _, err := a.resolver.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return a
}

func methodCalls(f *fakeDSHServer, method string) [][]byte {
	f.requests.mu.Lock()
	defer f.requests.mu.Unlock()
	var out [][]byte
	for _, r := range f.requests.list {
		if r.method == method {
			out = append(out, r.payload)
		}
	}
	return out
}

// ── list_sessions (§4.3.1) ──────────────────────────────────────────────────

func TestListSessionsMappingAndFilters(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)

	f.handlers["session.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{
				"sessionId": "s-live", "updatedAt": 1786860018199, "running": true, "blank": false,
				"cwd":         "/Users/x/proj",
				"projections": map[string]any{"asOfSeq": 9, "values": map[string]any{"title": "标题直出"}},
			},
			{
				// subagent row: filtered
				"sessionId": "s-sub", "updatedAt": 1, "running": false, "blank": false,
				"parentSessionId": "s-live", "origin": "subagent",
			},
			{
				// blank row: filtered
				"sessionId": "s-blank", "updatedAt": 2, "running": false, "blank": true,
			},
			{
				// cold row without projections: tail-read title fallback
				"sessionId": "s-cold", "updatedAt": 1786860099999, "running": false, "blank": false,
				"cwd": "/Users/x/other",
			},
		},
	}}
	// Tail-read fallback fixture: rows carry the {event:…} envelope.
	f.hooks["session.history"] = func(payload []byte) fakeRPCResponse {
		var req sessionHistoryRequest
		_ = json.Unmarshal(payload, &req)
		if req.SessionID != "s-cold" {
			return fakeRPCResponse{err: &RPCError{Code: "session-not-found", Message: "no session", Details: json.RawMessage(`{}`)}}
		}
		return fakeRPCResponse{value: map[string]any{
			"events": []map[string]any{
				{"event": map[string]any{"type": "session/title", "seq": 9, "time": 1786860099, "data": map[string]any{"title": "尾读标题"}}},
				{"event": map[string]any{"type": "user/message", "seq": 8, "time": 1786860090, "data": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "第一句话"}}, "source": map[string]any{"kind": "user"},
				}}},
			},
			"hasMore": false,
		}}
	}

	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 rows (subagent+blank filtered), got %d: %+v", len(sessions), sessions)
	}
	live, cold := sessions[0], sessions[1]
	if live.ID != "s-live" || live.Directory != "/Users/x/proj" || live.Summary != "标题直出" {
		t.Fatalf("live row mapping: %+v", live)
	}
	if !live.ModifiedAt.Equal(time.UnixMilli(1786860018199)) {
		t.Fatalf("modifiedAt mapping: %v", live.ModifiedAt)
	}
	if cold.ID != "s-cold" || cold.Summary != "尾读标题" {
		t.Fatalf("cold row tail-read title: %+v", cold)
	}

	// Running cache feeds enrichment + (§8-3) SessionActivityProbing.
	running, err := a.GetRunningSessionIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !running["s-live"] || running["s-cold"] {
		t.Fatalf("running cache mismatch: %v", running)
	}

	// The official cursor is an unimplemented reserved seat: the request must
	// NOT page (a single session.list with an empty payload).
	listCalls := methodCalls(f, "session.list")
	if len(listCalls) != 1 {
		t.Fatalf("expected exactly one session.list call, got %d", len(listCalls))
	}
	if strings.TrimSpace(string(listCalls[0])) != "{}" {
		t.Fatalf("session.list payload should be the empty reserved form: %s", listCalls[0])
	}
}

func TestListSessionsTitleFallsBackToUserMessage(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"sessionId": "s1", "updatedAt": 3, "running": false, "blank": false, "cwd": "/p"},
		},
	}}
	f.hooks["session.history"] = func(payload []byte) fakeRPCResponse {
		return fakeRPCResponse{value: map[string]any{
			"events": []map[string]any{
				{"event": map[string]any{"type": "user/message", "seq": 1, "time": 1, "data": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "帮我看下这个 bug"}}, "source": map[string]any{"kind": "user"},
				}}},
			},
		}}
	}
	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sessions[0].Summary != "帮我看下这个 bug" {
		t.Fatalf("fallback title: %q", sessions[0].Summary)
	}
}

// ── create / resume / prompt / cancel (§4.3.4/§4.3.6) ───────────────────────

func TestStartSessionCreateUsesCwd(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.hooks["session.create"] = func(payload []byte) fakeRPCResponse {
		return fakeRPCResponse{value: map[string]any{"sessionId": "official-1", "agentPreset": "standard"}}
	}

	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	if sess.CurrentSessionID() != "official-1" {
		t.Fatalf("session id: %s", sess.CurrentSessionID())
	}
	calls := methodCalls(f, "session.create")
	if len(calls) != 1 {
		t.Fatalf("create calls: %d", len(calls))
	}
	var req sessionCreateRequest
	if err := json.Unmarshal(calls[0], &req); err != nil {
		t.Fatal(err)
	}
	if req.Cwd != "/tmp/ios-dir" {
		t.Fatalf("create must carry the iOS-selected cwd, got %q", req.Cwd)
	}
	if req.WorkspaceID != "" {
		t.Fatal("create must not carry workspaceId (cwd path only)")
	}
}

func TestStartSessionExistingBindsWithoutCreate(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.hooks["session.history"] = func(payload []byte) fakeRPCResponse {
		var req sessionHistoryRequest
		_ = json.Unmarshal(payload, &req)
		if req.SessionID != "known-1" {
			return fakeRPCResponse{err: &RPCError{Code: "session-not-found", Message: `no session "unknown-1" in store`, Details: json.RawMessage(`{}`)}}
		}
		return fakeRPCResponse{value: map[string]any{"events": []any{}, "hasMore": false}}
	}

	sess, err := a.StartSession(context.Background(), "known-1")
	if err != nil {
		t.Fatalf("resume bind: %v", err)
	}
	sess.Close()
	if len(methodCalls(f, "session.create")) != 0 {
		t.Fatal("existing-id start must NOT create")
	}

	_, err = a.StartSession(context.Background(), "unknown-1")
	if err == nil {
		t.Fatal("unknown id must fail visibly")
	}
	// 坑 7: the official session-not-found text arrives verbatim.
	rpcErr, ok := err.(*RPCError)
	if !ok || rpcErr.Code != "session-not-found" || !strings.Contains(err.Error(), `no session "unknown-1" in store`) {
		t.Fatalf("unknown-id error must be the official RpcError: %v", err)
	}
}

func TestSendQueuesTextPrompt(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session.create"] = fakeRPCResponse{value: map[string]any{"sessionId": "s9"}}
	f.hooks["session.history"] = func(_ []byte) fakeRPCResponse {
		return fakeRPCResponse{value: map[string]any{"events": []any{}, "hasMore": false}}
	}
	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	f.handlers["session.prompt"] = fakeRPCResponse{value: map[string]any{"accepted": true}}
	if err := sess.Send("你好，帮我修个 bug", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	calls := methodCalls(f, "session.prompt")
	if len(calls) != 1 {
		t.Fatalf("prompt calls: %d", len(calls))
	}
	var req sessionPromptRequest
	if err := json.Unmarshal(calls[0], &req); err != nil {
		t.Fatal(err)
	}
	if req.SessionID != "s9" || req.Mode != "queue" {
		t.Fatalf("prompt request: %+v", req)
	}
	if len(req.Content) != 1 || req.Content[0].Type != "text" || req.Content[0].Text != "你好，帮我修个 bug" {
		t.Fatalf("prompt content: %+v", req.Content)
	}

	// Prompt business failure → official error text verbatim (fail visibly).
	f.handlers["session.prompt"] = fakeRPCResponse{err: &RPCError{
		Code: "model-unavailable", Message: `model "default/deepseek-chat" is not routable`,
		Details: json.RawMessage(`{}`),
	}}
	err = sess.Send("again", nil, nil)
	if err == nil || !strings.Contains(err.Error(), `default/deepseek-chat`) {
		t.Fatalf("prompt error must carry official text: %v", err)
	}
}

func TestCancelTurnMapsToSessionCancel(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session.create"] = fakeRPCResponse{value: map[string]any{"sessionId": "s10"}}
	f.hooks["session.history"] = func(_ []byte) fakeRPCResponse {
		return fakeRPCResponse{value: map[string]any{"events": []any{}, "hasMore": false}}
	}
	sess, _ := a.StartSession(context.Background(), "")
	defer sess.Close()
	f.handlers["session.cancel"] = fakeRPCResponse{value: map[string]any{"accepted": true}}
	if err := sess.(core.TurnCanceler).CancelTurn(context.Background()); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	calls := methodCalls(f, "session.cancel")
	if len(calls) != 1 || !strings.Contains(string(calls[0]), `"s10"`) {
		t.Fatalf("cancel calls: %s", calls)
	}
}

// ── rename (§4.3.6) ─────────────────────────────────────────────────────────

func TestRenameReturnsAcceptedTitle(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session.rename"] = fakeRPCResponse{value: map[string]any{"title": "规范化后的标题", "seq": 12}}
	info, err := a.RenameSession(context.Background(), "s1", "  规范化后的标题  ")
	if err != nil {
		t.Fatal(err)
	}
	if info.Summary != "规范化后的标题" {
		t.Fatalf("accepted title: %+v", info)
	}
	calls := methodCalls(f, "session.rename")
	var req sessionRenameRequest
	_ = json.Unmarshal(calls[0], &req)
	if req.SessionID != "s1" || req.Title != "  规范化后的标题  " {
		t.Fatalf("rename payload: %+v", req)
	}
}

// ── history → RichHistoryEntry (§4.3.2) ─────────────────────────────────────

func TestRichHistoryMapsTurnsToolsAndReasoning(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)

	// Two pages. Official page order is OLDEST-FIRST within a page (source
	// paginate(): page = window.filter(seq>=cut) preserves log order); pages
	// walk backwards via beforeSeq. Rows carry the {event:…} envelope.
	pages := map[int64][]map[string]any{}
	mkUser := func(seq int64, text string) map[string]any {
		return map[string]any{"type": "user/message", "seq": seq, "time": seq * 1000, "data": map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}}, "source": map[string]any{"kind": "user"},
		}}
	}
	pages[0] = []map[string]any{ // newest window, ascending seq
		{"event": map[string]any{"type": "turn/start", "seq": 5, "time": 5, "data": map[string]any{"turn": 1}}},
		{"event": map[string]any{"type": "assistant/message", "seq": 6, "time": 6, "data": map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "reasoning", "text": "先思考"},
					{"type": "tool-call", "id": "call-1", "name": "bash", "arguments": `{"command":"ls -la"}`},
				},
				"source": map[string]any{"kind": "model", "provider": "deepseek", "model": "deepseek-v4-pro"},
			},
		}}},
		{"event": map[string]any{"type": "tool/result", "seq": 7, "time": 7, "data": map[string]any{
			"turn": 1, "step": 2,
			"message": map[string]any{
				"source":  map[string]any{"kind": "tool", "callId": "call-1"},
				"content": []map[string]any{{"type": "tool-result", "toolCallId": "call-1", "content": []map[string]any{{"type": "text", "text": "ls 输出"}}}},
			},
		}}},
		{"event": map[string]any{"type": "assistant/message", "seq": 8, "time": 8, "data": map[string]any{
			"turn": 1, "step": 2,
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": "修好了"}},
				"source":  map[string]any{"kind": "model", "provider": "deepseek", "model": "deepseek-v4-pro"},
			},
		}}},
		{"event": map[string]any{"type": "turn/end", "seq": 9, "time": 9, "data": map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}}},
	}
	pages[5] = []map[string]any{ // older window (beforeSeq=5), ascending seq
		{"event": map[string]any{"type": "request/context", "seq": 3, "time": 3, "data": map[string]any{"provider": "deepseek", "model": "deepseek-v4-pro", "contextWindow": 128000}}},
		{"event": mkUser(4, "帮我看下目录")},
	}

	f.hooks["session.history"] = func(payload []byte) fakeRPCResponse {
		var req sessionHistoryRequest
		_ = json.Unmarshal(payload, &req)
		if req.BeforeSeq != nil {
			return fakeRPCResponse{value: map[string]any{"events": pages[*req.BeforeSeq], "hasMore": false}}
		}
		return fakeRPCResponse{value: map[string]any{"events": pages[0], "hasMore": true}}
	}

	entries, err := a.GetRichSessionHistory(context.Background(), "s-hist", 50)
	if err != nil {
		t.Fatalf("GetRichSessionHistory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected user+assistant entries, got %d: %+v", len(entries), entries)
	}
	user, asst := entries[0], entries[1]
	if user.Role != "user" || user.Content != "帮我看下目录" {
		t.Fatalf("user entry: %+v", user)
	}
	if asst.Role != "assistant" || asst.Content != "修好了" || asst.Thinking != "先思考" {
		t.Fatalf("assistant entry: %+v", asst)
	}
	if asst.ModelID != "deepseek-v4-pro" || asst.ProviderID != "deepseek" {
		t.Fatalf("model attribution: %+v", asst)
	}
	if len(asst.Steps) != 1 || asst.Steps[0]["toolName"] != "bash" {
		t.Fatalf("steps: %+v", asst.Steps)
	}
	if asst.Steps[0]["title"] != "ls -la" {
		t.Fatalf("step title from arguments: %+v", asst.Steps[0])
	}
	if asst.Parts == nil {
		t.Fatal("parts must be present")
	}
	// Tool output correlated by callId.
	step := asst.Steps[0]
	output := step["output"].(map[string]any)
	if output["text"] != "ls 输出" {
		t.Fatalf("tool output correlation: %+v", output)
	}
	// Entry ids are deterministic sessionID:seq.
	if user.ID != "s-hist:4" || asst.ID != "s-hist:5" {
		t.Fatalf("entry ids: %s / %s", user.ID, asst.ID)
	}
	// Paging walked both pages (two history calls).
	if len(methodCalls(f, "session.history")) != 2 {
		t.Fatalf("expected 2 history pages, got %d", len(methodCalls(f, "session.history")))
	}
}

// ── providers / models / selectModel (§4.3.5) ──────────────────────────────

func TestProvidersFilteredToActive(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["llm.providers"] = fakeRPCResponse{value: map[string]any{
		"providers": []map[string]any{
			{"provider": "deepseek", "displayName": "DeepSeek", "settingsNs": "llm.deepseek", "settingsPath": []string{}, "active": true},
			{"provider": "anthropic", "displayName": "Anthropic", "settingsNs": "llm.anthropic", "settingsPath": []string{}, "active": false, "declared": false},
			{"provider": "amazon-bedrock", "displayName": "Bedrock", "settingsNs": "llm.bedrock", "settingsPath": []string{}, "active": false},
		},
	}}
	f.handlers["llm.models"] = fakeRPCResponse{value: map[string]any{
		"groups": []map[string]any{
			{"id": "deepseek", "name": "DeepSeek", "models": []map[string]any{
				{"id": "deepseek-v4-pro", "name": "DeepSeek V4 Pro"},
				{"id": "deepseek-v4-flash", "name": "DeepSeek V4 Flash"},
			}},
		},
	}}
	providers := a.ListProviders()
	if len(providers) != 1 {
		t.Fatalf("dormant providers must not reach list_providers: %+v", providers)
	}
	if providers[0].Name != "deepseek" || len(providers[0].Models) != 2 {
		t.Fatalf("provider models: %+v", providers[0])
	}
	if providers[0].Models[0].Name != "deepseek/deepseek-v4-pro" {
		t.Fatalf("provider-qualified model id: %+v", providers[0].Models[0])
	}

	models := a.AvailableModels(context.Background())
	if len(models) != 2 || models[1].Name != "deepseek/deepseek-v4-flash" {
		t.Fatalf("available models: %+v", models)
	}
}

func TestSwitchModelAppliesSelectModelToActiveSession(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session.create"] = fakeRPCResponse{value: map[string]any{"sessionId": "s-model"}}
	f.hooks["session.history"] = func(_ []byte) fakeRPCResponse {
		return fakeRPCResponse{value: map[string]any{"events": []any{}, "hasMore": false}}
	}
	f.handlers["llm.providers"] = fakeRPCResponse{value: map[string]any{"providers": []any{}}}
	f.handlers["llm.models"] = fakeRPCResponse{value: map[string]any{"groups": []any{}}}
	f.handlers["session.selectModel"] = fakeRPCResponse{value: map[string]any{
		"selected": map[string]any{"provider": "deepseek", "model": "deepseek-v4-pro", "reasoningEffort": "high"},
	}}

	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	a.SetModel("deepseek/deepseek-v4-pro")
	a.SetReasoningEffort("high")
	if a.GetModel() != "deepseek/deepseek-v4-pro" {
		t.Fatalf("GetModel: %s", a.GetModel())
	}
	calls := methodCalls(f, "session.selectModel")
	if len(calls) == 0 {
		t.Fatal("switch_model must reach session.selectModel on the active session")
	}
	var req sessionSelectModelRequest
	_ = json.Unmarshal(calls[len(calls)-1], &req)
	if req.SessionID != "s-model" || req.Provider != "deepseek" || req.Model != "deepseek-v4-pro" || req.ReasoningEffort != "high" {
		t.Fatalf("selectModel payload: %+v", req)
	}
}

// ── ⛔ not_supported: capability absence is the driver-side truth ───────────

func TestUnsupportedCapabilitiesAreAbsent(t *testing.T) {
	a := &Agent{}
	// ⛔ rows of the §4.3 table whose semantics is "this driver does not
	// implement the optional interface" — the bridge handlers then answer
	// not_supported generically (fa371a3 惯例).
	if _, ok := interface{}(a).(core.SessionDeleter); ok {
		t.Fatal("delete_session ⛔: must not implement SessionDeleter")
	}
	if _, ok := interface{}(a).(core.SessionArchiver); ok {
		t.Fatal("archive_session 2️⃣: must not implement SessionArchiver in phase 1")
	}
	if _, ok := interface{}(a).(core.TranscriptLocator); ok {
		t.Fatal("pathless backend must not implement TranscriptLocator")
	}
	if _, ok := interface{}(a).(core.MemoryFileReader); ok {
		t.Fatal("list_memory_files/read_memory_file ⛔: must not implement MemoryFileReader")
	}
	if _, ok := interface{}(a).(core.MemoryFileProvider); ok {
		t.Fatal("must not implement MemoryFileProvider")
	}
	if _, ok := interface{}(a).(core.TodoProvider); ok {
		t.Fatal("fetch_todos ⛔ phase 1: must not implement TodoProvider")
	}
	if _, ok := interface{}(a).(core.UsageReporter); ok {
		t.Fatal("get_usage ⛔ phase 1: must not implement UsageReporter")
	}
	if _, ok := interface{}(a).(core.TokenUsageReporter); ok {
		t.Fatal("must not implement TokenUsageReporter")
	}
	if _, ok := interface{}(a).(core.AgentLister); ok {
		t.Fatal("list_agents ⛔ phase 1: must not implement AgentLister")
	}
	if _, ok := interface{}(a).(core.ModeSwitcher); ok {
		t.Fatal("list_permission_modes/set_permission_mode ⛔ phase 1: must not implement ModeSwitcher")
	}
	if _, ok := interface{}(a).(core.AttachmentSupporter); ok {
		t.Fatal("text-only phase 1: must NOT implement AttachmentSupporter (a declared kind is a semantic claim)")
	}
	if _, ok := interface{}(a).(core.ContextCompressor); ok {
		t.Fatal("compress_context ⛔: must not implement ContextCompressor")
	}
	if _, ok := interface{}(a).(core.CompositeRichHistoryProvider); ok {
		t.Fatal("pathless backend has no transcript segments")
	}
}

// ── diagnostics (§4.3.8) ────────────────────────────────────────────────────

func TestRunDiagnosticsReportsInstanceAndProviderStates(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session.list"] = fakeRPCResponse{value: map[string]any{"items": []any{}}}
	f.handlers["llm.providers"] = fakeRPCResponse{value: map[string]any{
		"providers": []map[string]any{
			{"provider": "deepseek", "displayName": "DeepSeek", "settingsNs": "llm.deepseek", "settingsPath": []string{}, "active": true},
			{"provider": "anthropic", "displayName": "Anthropic", "settingsNs": "llm.a", "settingsPath": []string{}, "active": false, "declared": false},
		},
	}}
	report, err := a.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.OverallStatus != "healthy" {
		t.Fatalf("overall: %s (%+v)", report.OverallStatus, report.Results)
	}
	joined := ""
	for _, r := range report.Results {
		joined += r.Message + "\n"
	}
	if !strings.Contains(joined, "API 版本标识 0.0.1") || !strings.Contains(joined, "非 npm 包版本") {
		t.Fatalf("S6: version must be labeled as an API identifier, not npm version: %s", joined)
	}
	if !strings.Contains(joined, "1 活跃 / 1 休眠") {
		t.Fatalf("S1: full provider set with state bits must reach diagnostics: %s", joined)
	}
	if !strings.Contains(joined, "127.0.0.1") {
		t.Fatalf("S11 loopback disclosure missing: %s", joined)
	}
}

func TestRunDiagnosticsFailsVisiblyWithoutInstance(t *testing.T) {
	a := &Agent{}
	// Failing managed starter: without it the resolver would really spawn the
	// user's dsh from PATH (unit tests must never do that).
	a.resolver = NewResolver(
		WithProbeURLs([]string{"http://127.0.0.1:1"}),
		withManagedStarter(&countingStarter{fail: true}),
	)
	report, err := a.RunDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.OverallStatus != "unhealthy" {
		t.Fatalf("expected unhealthy: %+v", report.Results)
	}
}

// ── list_projects (§4.3.7) ──────────────────────────────────────────────────

func TestListProjectSuggestionsFromWorkspaceList(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["workspace.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"workspaceId": "w1", "path": "/Users/x/proj", "title": "我的项目", "sessionIds": []string{"s1"}, "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-02T00:00:00Z"},
			{"workspaceId": "w2", "path": "/Users/x/other", "title": "", "sessionIds": []string{}, "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"},
		},
		"archivedSessionIds": []string{},
	}}
	suggestions, err := a.ListProjectSuggestions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(suggestions) != 2 {
		t.Fatalf("suggestions: %+v", suggestions)
	}
	if suggestions[0].ID != "w1" || suggestions[0].Directory != "/Users/x/proj" || suggestions[0].Name != "我的项目" {
		t.Fatalf("named workspace: %+v", suggestions[0])
	}
	if suggestions[1].Name != "other" {
		t.Fatalf("title-less workspace falls back to path base: %+v", suggestions[1])
	}
}

func TestListProjectSuggestionsEmptyRegistry(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["workspace.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{}, "archivedSessionIds": []string{},
	}}
	suggestions, err := a.ListProjectSuggestions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(suggestions) != 0 {
		t.Fatalf("empty registry must return empty (iOS local fallback): %+v", suggestions)
	}
}
