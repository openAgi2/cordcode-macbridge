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

func TestListSessionsGroupsByWorkspaceSessionIDs(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)

	f.handlers["session.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"sessionId": "s-chat", "updatedAt": 4, "running": false, "blank": false, "cwd": "/Users/x/Chat",
				"projections": map[string]any{"values": map[string]any{"title": "讲封神榜故事"}}},
			{"sessionId": "s-stray", "updatedAt": 3, "running": false, "blank": false, "cwd": "/Users/x/Chat",
				"projections": map[string]any{"values": map[string]any{"title": "太阳能发电站的故事"}}},
			{"sessionId": "s-ios", "updatedAt": 2, "running": false, "blank": false, "cwd": "/Users/x/cordcode-ios",
				"projections": map[string]any{"values": map[string]any{"title": "西游记"}}},
		},
	}}
	f.handlers["workspace.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"workspaceId": "w-chat", "path": "/Users/x/Chat", "title": "Chat", "sessionIds": []string{"s-chat"}},
			{"workspaceId": "w-ios", "path": "/Users/x/cordcode-ios", "title": "cordcode-ios", "sessionIds": []string{"s-ios"}},
		},
		"archivedSessionIds": []string{},
	}}

	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("rows: %+v", sessions)
	}
	byID := map[string]core.AgentSessionInfo{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	if byID["s-chat"].Directory != "/Users/x/Chat" {
		t.Fatalf("attached Chat session directory: %+v", byID["s-chat"])
	}
	if byID["s-ios"].Directory != "/Users/x/cordcode-ios" {
		t.Fatalf("attached ios session directory: %+v", byID["s-ios"])
	}
	if byID["s-stray"].Directory != ungroupedDirectory {
		t.Fatalf("same-cwd stray must be 未分组, got %+v", byID["s-stray"])
	}
}

func TestListSessionsMarksArchivedFromWorkspaceList(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)

	f.handlers["session.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"sessionId": "s-live", "updatedAt": 4, "running": false, "blank": false, "cwd": "/Users/x/Chat",
				"projections": map[string]any{"values": map[string]any{"title": "在列"}}},
			{"sessionId": "s-arch", "updatedAt": 3, "running": false, "blank": false, "cwd": "/Users/x/Chat",
				"projections": map[string]any{"values": map[string]any{"title": "讲个光头笑话"}}},
		},
	}}
	f.handlers["workspace.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"workspaceId": "w-chat", "path": "/Users/x/Chat", "title": "Chat", "sessionIds": []string{"s-live"}},
		},
		"archivedSessionIds": []string{"s-arch"},
	}}

	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("rows: %+v", sessions)
	}
	var live, archived core.AgentSessionInfo
	for _, s := range sessions {
		switch s.ID {
		case "s-live":
			live = s
		case "s-arch":
			archived = s
		}
	}
	if !live.ArchivedAt.IsZero() {
		t.Fatalf("live session must not be archived: %+v", live)
	}
	if archived.ArchivedAt.IsZero() {
		t.Fatalf("official archivedSessionIds must surface ArchivedAt: %+v", archived)
	}
	if archived.Directory != ungroupedDirectory {
		t.Fatalf("archived stray directory: %+v", archived)
	}
}

func TestListSessionsMapsOfficialAgentPreset(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)

	f.handlers["session.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"sessionId": "s-ptc", "updatedAt": 4, "running": false, "blank": false, "cwd": "/Users/x/Chat",
				"agentPreset": "code",
				"projections": map[string]any{"values": map[string]any{"title": "Codex与Claude"}}},
			{"sessionId": "s-std", "updatedAt": 3, "running": false, "blank": false, "cwd": "/Users/x/Chat",
				"agentPreset": "standard",
				"projections": map[string]any{"values": map[string]any{"title": "普通会话"}}},
		},
	}}
	f.handlers["workspace.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"workspaceId": "w-chat", "path": "/Users/x/Chat", "title": "Chat", "sessionIds": []string{"s-ptc", "s-std"}},
		},
	}}

	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]core.AgentSessionInfo{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	if byID["s-ptc"].AgentPreset != "code" {
		t.Fatalf("PTC session preset: %+v", byID["s-ptc"])
	}
	if byID["s-std"].AgentPreset != "standard" {
		t.Fatalf("standard session preset: %+v", byID["s-std"])
	}
}

func TestListAgentsMapsOfficialPresets(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["agentPreset.list"] = fakeRPCResponse{value: map[string]any{
		"presets": []any{
			map[string]any{"id": "standard", "name": "标准模式", "description": "完整", "isDefault": true, "trust": "system"},
			map[string]any{"id": "minimal", "name": "极简模式", "description": "双工具", "trust": "system"},
			map[string]any{"id": "broken", "name": "坏的", "broken": "missing plugin", "trust": "user"},
		},
	}}
	got, err := a.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (broken omitted): %+v", len(got), got)
	}
	if got[0].Name != "standard" || got[0].DisplayName != "标准模式" || !got[0].IsDefault {
		t.Fatalf("standard = %+v", got[0])
	}
	if got[1].Name != "minimal" || got[1].DisplayName != "极简模式" {
		t.Fatalf("minimal = %+v", got[1])
	}
}

func TestStartSessionCreateSendsPendingAgentPreset(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	a.SetPendingAgentPreset("minimal")
	f.hooks["session.create"] = func(payload []byte) fakeRPCResponse {
		return fakeRPCResponse{value: map[string]any{"sessionId": "official-preset", "agentPreset": "minimal"}}
	}
	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	calls := methodCalls(f, "session.create")
	if len(calls) != 1 {
		t.Fatalf("create calls: %d", len(calls))
	}
	var req sessionCreateRequest
	if err := json.Unmarshal(calls[0], &req); err != nil {
		t.Fatal(err)
	}
	if req.AgentPreset != "minimal" {
		t.Fatalf("agentPreset = %q, want minimal", req.AgentPreset)
	}
}

func TestStartSessionOmitsUngroupedCwd(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	a.workDir = ungroupedDirectory
	f.hooks["session.create"] = func(payload []byte) fakeRPCResponse {
		return fakeRPCResponse{value: map[string]any{"sessionId": "official-ungrouped", "agentPreset": "standard"}}
	}

	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	calls := methodCalls(f, "session.create")
	if len(calls) != 1 {
		t.Fatalf("create calls: %d", len(calls))
	}
	var req sessionCreateRequest
	if err := json.Unmarshal(calls[0], &req); err != nil {
		t.Fatal(err)
	}
	if req.Cwd != "" {
		t.Fatalf("未分组 must not be sent as cwd, got %q", req.Cwd)
	}
	if req.WorkspaceID != "" {
		t.Fatal("ungrouped create must not invent a workspaceId")
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
		t.Fatal("unmatched cwd must not invent a workspaceId")
	}
}

func TestStartSessionCreateUsesWorkspaceIDWhenCwdMatches(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	a.workDir = "/Users/x/Chat/"
	f.handlers["workspace.list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"workspaceId": "w-chat", "path": "/Users/x/Chat", "title": "Chat", "sessionIds": []string{"s-old"}},
			{"workspaceId": "w-ios", "path": "/Users/x/cordcode-ios", "title": "cordcode-ios", "sessionIds": []string{}},
		},
		"archivedSessionIds": []string{},
	}}
	f.hooks["session.create"] = func(payload []byte) fakeRPCResponse {
		return fakeRPCResponse{value: map[string]any{"sessionId": "official-chat", "agentPreset": "standard"}}
	}

	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	calls := methodCalls(f, "session.create")
	if len(calls) != 1 {
		t.Fatalf("create calls: %d", len(calls))
	}
	var req sessionCreateRequest
	if err := json.Unmarshal(calls[0], &req); err != nil {
		t.Fatal(err)
	}
	if req.WorkspaceID != "w-chat" {
		t.Fatalf("matching Chat folder must create with workspaceId, got %+v", req)
	}
	if req.Cwd != "" {
		t.Fatalf("schema is workspaceId XOR cwd, got cwd %q", req.Cwd)
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

// 回归（2026-09-06「只剩最后一条回复」事故）：turn 中段的 chunk 页（数千条
// assistant/chunk、零 turn/end / user/message / command/done）必须按 0 计入
// 预算——旧 len/8 兜底把它估成 ~500 条幻影条目，budget 秒爆、走一页就停，
// 冷打开只投影出最后一个 turn。修复后 walk 必须穿过 chunk 页拿到更早轮次。
func TestRichHistoryWalksThroughMidTurnChunkPages(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)

	chunkPage := []map[string]any{{
		"event": map[string]any{"type": "turn/start", "seq": 200, "time": 200, "data": map[string]any{"turn": 2}},
	}}
	// 4001 条 chunk：旧兜底 4002/8≈500 ≥ budget(500)，一页即停。
	for i := int64(0); i < 4001; i++ {
		chunkPage = append(chunkPage, map[string]any{
			"event": map[string]any{"type": "assistant/chunk", "seq": 201 + i, "time": 201 + i,
				"data": map[string]any{"turn": 2, "step": 1, "chunk": map[string]any{"type": "text-delta", "text": "x"}}},
		})
	}
	chunkPage = append(chunkPage,
		map[string]any{"event": map[string]any{"type": "assistant/message", "seq": 4602, "time": 4602, "data": map[string]any{
			"turn": 2, "step": 1,
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": "最后一轮回复"}},
				"source":  map[string]any{"kind": "model", "provider": "deepseek", "model": "deepseek-v4-pro"},
			},
		}}},
	)
	boundaryPage := []map[string]any{
		{"event": map[string]any{"type": "user/message", "seq": 150, "time": 150, "data": map[string]any{
			"content": []map[string]any{{"type": "text", "text": "中间的用户消息"}},
			"source":  map[string]any{"kind": "user"},
		}}},
		{"event": map[string]any{"type": "assistant/message", "seq": 160, "time": 160, "data": map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": "第一轮回复"}},
				"source":  map[string]any{"kind": "model", "provider": "deepseek", "model": "deepseek-v4-pro"},
			},
		}}},
		{"event": map[string]any{"type": "turn/end", "seq": 199, "time": 199, "data": map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}}}},
	}
	oldestPage := []map[string]any{
		{"event": map[string]any{"type": "turn/start", "seq": 100, "time": 100, "data": map[string]any{"turn": 1}}},
	}
	f.hooks["session.history"] = func(payload []byte) fakeRPCResponse {
		var req sessionHistoryRequest
		_ = json.Unmarshal(payload, &req)
		switch {
		case req.BeforeSeq == nil:
			return fakeRPCResponse{value: map[string]any{"events": chunkPage, "hasMore": true}}
		case *req.BeforeSeq == 200:
			return fakeRPCResponse{value: map[string]any{"events": boundaryPage, "hasMore": true}}
		default: // beforeSeq=150
			return fakeRPCResponse{value: map[string]any{"events": oldestPage, "hasMore": false}}
		}
	}

	entries, err := a.GetRichSessionHistory(context.Background(), "s-chunk", 0)
	if err != nil {
		t.Fatalf("GetRichSessionHistory: %v", err)
	}
	// walk 必须走完 3 页（chunk 页 0 边界不能提前终止）。
	if calls := len(methodCalls(f, "session.history")); calls != 3 {
		t.Fatalf("expected 3 history pages (walk through the chunk page), got %d", calls)
	}
	// user 气泡 + 两个 assistant turn + plan/goal 快照。
	if len(entries) != 5 {
		t.Fatalf("expected user+2 assistant turns+plan/goal snapshots, got %d: %+v", len(entries), entries)
	}
	if entries[0].Role != "user" || entries[0].Content != "中间的用户消息" {
		t.Fatalf("user entry missing: %+v", entries[0])
	}
	if entries[1].Role != "assistant" || entries[1].Content != "第一轮回复" {
		t.Fatalf("first assistant entry: %+v", entries[1])
	}
	if entries[2].Role != "assistant" || entries[2].Content != "最后一轮回复" {
		t.Fatalf("tail assistant entry: %+v", entries[2])
	}
	// 单元口径：零边界 chunk 页计 0（不再 len/8 兜底）。
	var chunkOnly []apiHistoryEntry
	for _, row := range chunkPage {
		b, _ := json.Marshal(row)
		var e apiHistoryEntry
		_ = json.Unmarshal(b, &e)
		chunkOnly = append(chunkOnly, e)
	}
	if n := countMappableEntries(chunkOnly); n != 0 {
		t.Fatalf("chunk-only page mappable = %d, want 0", n)
	}
}

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
	// user + assistant + 尾随 plan-mode/goal 快照（无 plan/goal 事件的会话折叠为
	// inactive / none）。
	if len(entries) != 4 {
		t.Fatalf("expected user+assistant+plan/goal-snapshot entries, got %d: %+v", len(entries), entries)
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
	// Entry ids：assistant turn 与 live 同源（dshw-<prefix>-t<N>，turn/start
	// 官方 turn 号）；本 fixture 的 user 行落在 turn/start 之前（attach 前残留
	// 形）→ 保持 sessionID:seq fallback。
	if user.ID != "s-hist:4" || asst.ID != "dshw-s-hist-t1" {
		t.Fatalf("entry ids: %s / %s", user.ID, asst.ID)
	}
	// 尾随 plan-mode 快照：system 角色 + plan_mode part（官方无 plan 事件 → inactive）。
	snap := entries[2]
	if snap.ID != "s-hist:plan-mode" || snap.Role != "system" || len(snap.Parts) != 1 {
		t.Fatalf("plan snapshot entry: %+v", snap)
	}
	if p := snap.Parts[0]; p["type"] != "plan_mode" || p["active"] != false || p["pending"] != false {
		t.Fatalf("plan snapshot part: %+v", p)
	}
	// Paging walked both pages (two history calls).
	if len(methodCalls(f, "session.history")) != 2 {
		t.Fatalf("expected 2 history pages, got %d", len(methodCalls(f, "session.history")))
	}
	var first sessionHistoryRequest
	_ = json.Unmarshal(methodCalls(f, "session.history")[0], &first)
	if first.MaxMessages == nil || *first.MaxMessages != historyPageMessages {
		t.Fatalf("first page maxMessages=%v want %d", first.MaxMessages, historyPageMessages)
	}
}

func TestRichHistoryShrinksOversizedPage(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)

	old := unaryResponseLimit
	unaryResponseLimit = 900
	t.Cleanup(func() { unaryResponseLimit = old })

	f.hooks["session.history"] = func(payload []byte) fakeRPCResponse {
		var req sessionHistoryRequest
		_ = json.Unmarshal(payload, &req)
		max := 0
		if req.MaxMessages != nil {
			max = *req.MaxMessages
		}
		if max >= historyPageMessages {
			return fakeRPCResponse{value: map[string]any{
				"events": []map[string]any{
					{"event": map[string]any{"type": "user/message", "seq": 1, "time": 1, "data": map[string]any{
						"content": []map[string]any{{"type": "text", "text": strings.Repeat("X", 4000)}},
						"source":  map[string]any{"kind": "user"},
					}}},
				},
				"hasMore": false,
			}}
		}
		return fakeRPCResponse{value: map[string]any{
			"events": []map[string]any{
				{"event": map[string]any{"type": "user/message", "seq": 1, "time": 1, "data": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "短页"}},
					"source":  map[string]any{"kind": "user"},
				}}},
			},
			"hasMore": false,
		}}
	}

	entries, err := a.GetRichSessionHistory(context.Background(), "s-big", 0)
	if err != nil {
		t.Fatalf("GetRichSessionHistory: %v", err)
	}
	// 短页 user + 尾随 plan-mode/goal 快照。
	if len(entries) != 3 || entries[0].Content != "短页" {
		t.Fatalf("entries after shrink: %+v", entries)
	}
	var sizes []int
	for _, raw := range methodCalls(f, "session.history") {
		var req sessionHistoryRequest
		_ = json.Unmarshal(raw, &req)
		if req.MaxMessages != nil {
			sizes = append(sizes, *req.MaxMessages)
		}
	}
	if len(sizes) < 2 || sizes[0] != historyPageMessages || sizes[len(sizes)-1] >= historyPageMessages {
		t.Fatalf("page sizes did not shrink: %v", sizes)
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
	// AgentLister ⛔ 守卫已随产品化预设功能（09d0089）移除：agent_presets.go 的
	// ListAgents 把官方 agent preset 映射为 core.AgentDescriptor，是已交付能力。
	if _, ok := interface{}(a).(core.ModeSwitcher); !ok {
		t.Fatal("list_permission_modes/set_permission_mode: dsh-web must implement ModeSwitcher")
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

// ── wire descriptor (§8-7) ──────────────────────────────────────────────────

func TestWireDescriptorDeepSeekWeb(t *testing.T) {
	a := &Agent{}
	wd := a.WireDescriptor()
	if wd == nil {
		t.Fatal("descriptor must self-describe")
	}
	if wd.Kind != "deepseek-web" || wd.DisplayName != "DeepSeek Harness" {
		t.Fatalf("kind/displayName: %+v", wd)
	}
	if wd.LiveEventModel != "broadcast" {
		t.Fatalf("mux is agent-level broadcast: %s", wd.LiveEventModel)
	}
	if wd.RequiresExternalTurnPolling {
		t.Fatal("mux pushes external turns — polling not required")
	}
	for _, want := range []string{"external_turn_streaming", "question_reply", "structured_user_input_v1"} {
		found := false
		for _, c := range wd.StaticCapabilities {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("StaticCapabilities missing %q: %+v", want, wd.StaticCapabilities)
		}
	}
	// ToolAuthorizer present (permission_resolve derivation for iOS actions).
	if _, ok := interface{}(a).(interface {
		AddAllowedTools(...string) error
		GetAllowedTools() []string
	}); !ok {
		t.Fatal("ToolAuthorizer must be implemented (permission_resolve)")
	}
	if _, ok := interface{}(a).(core.HistoryProvider); !ok {
		t.Fatal("legacy HistoryProvider must be implemented (session_history)")
	}
}

// TestMapHistoryEventsCommandFoldAndPlanSnapshot：冷拉折叠出官方命令行 +
// 计划快照（与 live codec 同一官方折叠）：run→done 按 commandId 续接、
// done-only 续空 name、torn-tail run 保留 running 行、末尾一条 plan 快照。
func TestMapHistoryEventsCommandFoldAndPlanSnapshot(t *testing.T) {
	evs := []apiHistoryEntry{
		{Event: env("user/message", 1, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "压缩一下"}},
			"source":  map[string]any{"kind": "user"},
		})},
		{Event: env("command/run", 2, map[string]any{"commandId": "cmd-a", "name": "compact", "source": map[string]any{"kind": "user"}})},
		{Event: env("command/done", 3, map[string]any{"commandId": "cmd-a", "kind": "success", "text": "Compacted 20 history items (~11695 tokens)."})},
		{Event: env("command/run", 4, map[string]any{"commandId": "cmd-b", "name": "plan", "args": "on"})},
		{Event: env("command/done", 5, map[string]any{"commandId": "cmd-b", "kind": "error", "text": "Attachments cannot accompany /plan off."})},
		{Event: env("command/run", 6, map[string]any{"commandId": "cmd-c", "name": "goal"})},
		// torn tail：cmd-c 无 done → 保留 running 行。
	}
	entries := mapHistoryEvents("s-fold", evs)
	if len(entries) != 6 {
		t.Fatalf("entries = %d (user + 3 command rows + plan/goal snapshot), got %+v", len(entries), entries)
	}
	if entries[0].Role != "user" {
		t.Fatalf("first entry = %+v, want user", entries[0])
	}
	rowA := entries[1]
	if rowA.ID != "s-fold:cmd:cmd-a" || rowA.Role != "system" || len(rowA.Parts) != 1 {
		t.Fatalf("rowA = %+v", rowA)
	}
	if p := rowA.Parts[0]; p["type"] != "command" || p["commandId"] != "cmd-a" ||
		p["name"] != "compact" || p["kind"] != "success" ||
		p["text"] != "Compacted 20 history items (~11695 tokens)." {
		t.Fatalf("rowA part = %+v", p)
	}
	// /plan on 失败：done kind=error + 官方文案；折叠落回 inactive。
	rowB := entries[2]
	if p := rowB.Parts[0]; p["commandId"] != "cmd-b" || p["name"] != "plan" || p["args"] != "on" ||
		p["kind"] != "error" {
		t.Fatalf("rowB part = %+v", p)
	}
	// torn tail：running 行带 name，无 text。
	rowC := entries[3]
	if p := rowC.Parts[0]; p["commandId"] != "cmd-c" || p["name"] != "goal" || p["kind"] != "running" {
		t.Fatalf("rowC part = %+v", p)
	} else if _, hasText := p["text"]; hasText {
		t.Fatalf("running row must carry no text: %+v", p)
	}
	// 末尾 plan 快照：error done 不保留 wanted → {active:false, pending:false}。
	snap := entries[4]
	if snap.ID != "s-fold:plan-mode" || len(snap.Parts) != 1 {
		t.Fatalf("plan snapshot = %+v", snap)
	}
	if p := snap.Parts[0]; p["type"] != "plan_mode" || p["active"] != false || p["pending"] != false {
		t.Fatalf("plan snapshot part = %+v", p)
	}
	// 末尾 goal 快照：无 goal/change → phase "none"（远端清除残留横条）。
	gsnap := entries[5]
	if gsnap.ID != "s-fold:goal" || gsnap.Role != "system" || len(gsnap.Parts) != 1 {
		t.Fatalf("goal snapshot = %+v", gsnap)
	}
	if p := gsnap.Parts[0]; p["type"] != "goal" || p["phase"] != "none" {
		t.Fatalf("empty goal snapshot part = %+v", p)
	}

	// /plan on 成功 + plan/mode 整值：快照迁到 active。
	evs2 := []apiHistoryEntry{
		{Event: env("command/run", 1, map[string]any{"commandId": "cmd-p", "name": "plan", "args": "on"})},
		{Event: env("command/done", 2, map[string]any{"commandId": "cmd-p", "kind": "success"})},
		{Event: env("plan/mode", 3, map[string]any{"active": true})},
	}
	entries = mapHistoryEvents("s-fold2", evs2)
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	if p := entries[1].Parts[0]; p["type"] != "plan_mode" || p["active"] != true || p["pending"] != false {
		t.Fatalf("active plan snapshot = %+v", p)
	}
	if p := entries[2].Parts[0]; p["type"] != "goal" || p["phase"] != "none" {
		t.Fatalf("goal snapshot part = %+v", p)
	}
}

// TestMapHistoryEventsGoalSnapshotFold：goal/change 全量替换语义的冷拉折叠——
// 多条快照只留最后一条；clear 墓碑回到 "none"；快照字段保真。
func TestMapHistoryEventsGoalSnapshotFold(t *testing.T) {
	evs := []apiHistoryEntry{
		{Event: env("goal/change", 1, map[string]any{
			"kind": "goal/change", "version": 1, "operation": "create",
			"goal":          map[string]any{"id": "goal-h-1", "revision": 1, "objective": "目标甲", "phase": "active", "maxGoalRounds": 256},
			"roundsStarted": 0, "createdAt": 1, "updatedAt": 1,
		})},
		{Event: env("goal/change", 2, map[string]any{
			"kind": "goal/change", "version": 1, "operation": "pause",
			"goal":          map[string]any{"id": "goal-h-1", "revision": 2, "objective": "目标乙", "phase": "paused", "maxGoalRounds": 256},
			"roundsStarted": 1, "createdAt": 1, "updatedAt": 2,
		})},
	}
	entries := mapHistoryEvents("s-goal", evs)
	if len(entries) != 2 {
		t.Fatalf("goal-only log folds to plan-mode + goal trailing snapshots: %+v", entries)
	}
	p := entries[1].Parts[0]
	if p["type"] != "goal" || p["id"] != "goal-h-1" || p["revision"] != int64(2) ||
		p["objective"] != "目标乙" || p["phase"] != "paused" || p["maxGoalRounds"] != 256 {
		t.Fatalf("folded goal snapshot = %+v", p)
	}

	// clear 墓碑置空。
	evsClear := []apiHistoryEntry{
		{Event: env("goal/change", 1, map[string]any{
			"kind": "goal/change", "version": 1, "operation": "create",
			"goal": map[string]any{"id": "goal-h-1", "revision": 1, "objective": "目标甲", "phase": "active"},
		})},
		{Event: env("goal/change", 2, map[string]any{
			"kind": "goal/change", "version": 1, "operation": "clear",
			"cleared": map[string]any{"id": "goal-h-1", "revision": 2}, "clearedAt": 3,
		})},
	}
	entries = mapHistoryEvents("s-goal2", evsClear)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if p := entries[1].Parts[0]; p["type"] != "goal" || p["phase"] != "none" {
		t.Fatalf("cleared goal snapshot = %+v", p)
	}
}

// TestMapHistoryEventsGoalRoundInterleave：goal 轮冷拉交错真值（owner
// 2026-09-06 01:49 rework ⑧）。官方 journal 里 command/done 恒落在 turn/end
// 与下一 turn/start 之间，且 goal 轮没有 user 行（user/message kind=goal 不进
// 时间线）。折叠出的 entries 必须保持 journal 交错序：每轮 assistant 各自持有
// 与 live 同源的 dshw-<prefix>-t<N> 身份，命令行夹在轮次之间——不得把多轮
// 输出折进同一 turn、命令行聚到尾部。
func TestMapHistoryEventsGoalRoundInterleave(t *testing.T) {
	mkAsst := func(seq int64, text string) apiHistoryEntry {
		return apiHistoryEntry{Event: env("assistant/message", seq, map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": text}},
				"source":  map[string]any{"kind": "model", "provider": "deepseek", "model": "deepseek-v4-pro"},
			},
		})}
	}
	evs := []apiHistoryEntry{
		// turn 1：user 提问 + 输出1。
		{Event: env("turn/start", 1, map[string]any{"turn": 1})},
		{Event: env("user/message", 2, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "开始"}},
			"source":  map[string]any{"kind": "user"},
		})},
		mkAsst(3, "输出1"),
		{Event: env("turn/end", 4, map[string]any{"turn": 1, "reason": map[string]any{"kind": "completed"}})},
		// goal 命令1：run+change+done（真值三连），随后 goal 轮无 user 行。
		{Event: env("command/run", 5, map[string]any{"commandId": "g1", "name": "goal", "args": " 创作浩克故事1000字左右"})},
		{Event: env("goal/change", 6, map[string]any{"operation": "create", "goal": map[string]any{"id": "goal-1", "revision": 1, "objective": "创作浩克故事", "phase": "active"}})},
		{Event: env("command/done", 7, map[string]any{"commandId": "g1", "kind": "success"})},
		// goal 轮 turn 2：只有 assistant 输出2。
		{Event: env("turn/start", 8, map[string]any{"turn": 2})},
		mkAsst(9, "输出2"),
		{Event: env("turn/end", 10, map[string]any{"turn": 2, "reason": map[string]any{"kind": "completed"}})},
		// goal 命令2 + goal 轮 turn 3。
		{Event: env("command/run", 11, map[string]any{"commandId": "g2", "name": "goal", "args": " 创作雷神故事2000字左右"})},
		{Event: env("command/done", 12, map[string]any{"commandId": "g2", "kind": "success"})},
		{Event: env("turn/start", 13, map[string]any{"turn": 3})},
		mkAsst(14, "输出3"),
		{Event: env("turn/end", 15, map[string]any{"turn": 3, "reason": map[string]any{"kind": "completed"}})},
	}
	entries := mapHistoryEvents("s-goalround", evs)
	// 交错序：user(dshw-t1) + asst(dshw-t1) + cmd:g1 + asst(dshw-t2) + cmd:g2 +
	// asst(dshw-t3) + 尾随 plan/goal 快照。
	want := []struct {
		id   string
		role string
	}{
		{"dshw-s-goalround-t1", "user"},
		{"dshw-s-goalround-t1", "assistant"},
		{"s-goalround:cmd:g1", "system"},
		{"dshw-s-goalround-t2", "assistant"},
		{"s-goalround:cmd:g2", "system"},
		{"dshw-s-goalround-t3", "assistant"},
		{"s-goalround:plan-mode", "system"},
		{"s-goalround:goal", "system"},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %d, want %d: %+v", len(entries), len(want), entries)
	}
	for i, w := range want {
		if entries[i].ID != w.id || entries[i].Role != w.role {
			t.Fatalf("entries[%d] = {%s %s}, want {%s %s}（交错序被破坏，rework ⑧）",
				i, entries[i].ID, entries[i].Role, w.id, w.role)
		}
	}
	// 每轮输出独立（不折进同一 turn）：三个 assistant entry 身份互不相同。
	if entries[1].ID == entries[3].ID || entries[3].ID == entries[5].ID {
		t.Fatalf("goal rounds must hold distinct turn identities: %s / %s / %s",
			entries[1].ID, entries[3].ID, entries[5].ID)
	}
	if entries[1].Content != "输出1" || entries[3].Content != "输出2" || entries[5].Content != "输出3" {
		t.Fatalf("round contents drifted: %+v %+v %+v", entries[1], entries[3], entries[5])
	}
}

// TestMapHistoryEventsSubagentSettledContextInjection：settle 通知冷拉折叠——
// user/message source.kind="subagent-settled" → 独立 context_injection entry
// （id "ctxinj:<seq>" 与 live codec 同身份，冷热合并防重复行）；summary 空 /
// 其余注入 kind 维持 known-drop（fail-open，不造行）。
func TestMapHistoryEventsSubagentSettledContextInjection(t *testing.T) {
	evs := []apiHistoryEntry{
		{Event: env("user/message", 1, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "正常输入"}},
			"source":  map[string]any{"kind": "user"},
		})},
		{Event: env("user/message", 2, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "Background subagent sess-bg finished after 1 round."},
				{"type": "text", "text": "已生成封神榜第一章。"},
			},
			"source": map[string]any{
				"kind": "subagent-settled", "form": "notice",
				"summary":         "Background subagent sess-bg finished after 1 round.",
				"senderSessionId": "sess-bg",
			},
		})},
		{Event: env("user/message", 3, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "形状未知"}},
			"source": map[string]any{"kind": "subagent-settled", "form": "notice"},
		})},
		{Event: env("user/message", 4, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "<goal_round>"}},
			"source": map[string]any{"kind": "goal"},
		})},
	}
	entries := mapHistoryEvents("s-ctxinj", evs)
	// user 行 + settle 行 + plan-mode 快照 + goal 快照。
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4 (user + settle + 2 trailing snapshots): %+v", len(entries), entries)
	}
	if entries[0].Role != "user" {
		t.Fatalf("first entry = %+v, want user", entries[0])
	}
	row := entries[1]
	if row.Role != "context_injection" || row.ID != "ctxinj:2" {
		t.Fatalf("settle entry = %+v", row)
	}
	ci := row.ContextInjection
	if ci == nil {
		t.Fatalf("settle entry missing typed payload: %+v", row)
	}
	if ci.ItemID != "ctxinj:2" || ci.Kind != "subagent-settled" || ci.Form != "notice" {
		t.Fatalf("payload identity = %+v", ci)
	}
	if ci.Summary != "Background subagent sess-bg finished after 1 round." {
		t.Fatalf("summary not verbatim: %q", ci.Summary)
	}
	if ci.SenderSessionID != "sess-bg" {
		t.Fatalf("senderSessionId = %q", ci.SenderSessionID)
	}
	if ci.Text != "Background subagent sess-bg finished after 1 round.\n已生成封神榜第一章。" {
		t.Fatalf("text = %q", ci.Text)
	}
	// env() 把 time 记为 seq*1000 → 冷拉 entry 带真实 journal 时间。
	if want := time.UnixMilli(2 * 1000); !row.Timestamp.Equal(want) {
		t.Fatalf("timestamp = %v, want %v (journal time)", row.Timestamp, want)
	}
}

// TestMapHistoryEventsMidTurnSettleAfterTurn：settle 注入发生在 turn 进行中时，
// 折叠 turn 模型下归属该 turn 之后（live reducer 同位），不得先于所属 turn 的
// assistant entry 入列（2026-09-06 owner 报障：注入行出现在回复开头）。
func TestMapHistoryEventsMidTurnSettleAfterTurn(t *testing.T) {
	evs := []apiHistoryEntry{
		{Event: env("turn/start", 10, map[string]any{"turn": 1})},
		{Event: env("user/message", 11, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "并行写五章"}},
			"source":  map[string]any{"kind": "user"},
		})},
		{Event: env("assistant/message", 12, map[string]any{
			"turn": 1, "step": 1,
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": "启动 5 个 subagent"}},
			},
		})},
		// settle 落在 turn 内（journal 语义：子代理中途结算，正文/Think 之间）。
		{Event: env("user/message", 13, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "Background subagent bg-1 finished after 2 rounds."}},
			"source": map[string]any{
				"kind": "subagent-settled", "form": "notice",
				"summary":         "Background subagent bg-1 finished after 2 rounds.",
				"senderSessionId": "bg-1",
			},
		})},
		{Event: env("user/message", 14, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "Background subagent bg-2 finished after 3 rounds."}},
			"source": map[string]any{
				"kind": "subagent-settled", "form": "notice",
				"summary":         "Background subagent bg-2 finished after 3 rounds.",
				"senderSessionId": "bg-2",
			},
		})},
		{Event: env("assistant/message", 15, map[string]any{
			"turn": 1, "step": 2,
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": "全部结算完成"}},
			},
		})},
		{Event: env("turn/end", 16, nil)},
	}
	entries := mapHistoryEvents("s-midturn", evs)
	want := []struct {
		id   string
		role string
	}{
		{"dshw-s-midturn-t1", "user"},
		{"dshw-s-midturn-t1", "assistant"},
		{"ctxinj:13", "context_injection"},
		{"ctxinj:14", "context_injection"},
		{"s-midturn:plan-mode", "system"},
		{"s-midturn:goal", "system"},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %d, want %d: %+v", len(entries), len(want), entries)
	}
	for i, w := range want {
		if entries[i].ID != w.id || entries[i].Role != w.role {
			t.Fatalf("entries[%d] = {%s %s}, want {%s %s}（turn 内 settle 应在所属 turn 之后）",
				i, entries[i].ID, entries[i].Role, w.id, w.role)
		}
	}
	// turn 折叠未被注入打断：两条 assistant 文本合并进同一 entry。
	if entries[1].Content != "启动 5 个 subagent\n全部结算完成" {
		t.Fatalf("turn content = %q, want merged two-step text", entries[1].Content)
	}
}
