package codexweb

// session_ssv2_test.go —— p2-ssv2 Agent 面测试：provider 方法（turn-scoped/flat/
// activity）与 ListSessions 映射，全部走 scripted transport（官方帧形状）。

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// fakeEndpoint 把 scripted Client 注入 Agent（包内可见未导出字段）。
func agentWithScript(t *testing.T, respond func(method string) any) *Agent {
	t.Helper()
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "0.149.0-alpha.4"}
	ep.client = cl
	s.mu.Lock()
	s.onSend = func(payload []byte) {
		var req struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.Unmarshal(payload, &req)
		result := respond(req.Method)
		if result == nil {
			return
		}
		frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		s.push(string(frame))
	}
	s.mu.Unlock()
	a := New(nil)
	a.endpoint = ep
	a.lastStatus = &ProbeSnapshot{Available: true, Source: ep.Source, CLIVersion: ep.CLIVersion}
	return a
}

func TestAgentListSessionsMapping(t *testing.T) {
	name := "official-name"
	branch := "main"
	a := agentWithScript(t, func(method string) any {
		if method != "thread/list" {
			return nil
		}
		return ThreadListPage{Data: []ThreadInfo{{
			ID: "th-1", Preview: "preview text", Name: &name, Cwd: "/ws",
			ModelProvider: "mockpi", UpdatedAt: 1787330474,
			Status:  ThreadStatus{Type: ThreadStatusIdle},
			GitInfo: &GitInfo{Branch: &branch},
		}}}
	})
	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("应 1 条：%d", len(sessions))
	}
	si := sessions[0]
	if si.ID != "th-1" || si.Summary != "official-name" || si.Directory != "/ws" ||
		si.ProviderID != "mockpi" || si.GitBranch != "main" {
		t.Fatalf("映射错误：%+v", si)
	}
	if si.ModelID != "" || !si.ArchivedAt.IsZero() {
		t.Fatalf("官方未提供的字段必须留空（不编造）：%+v", si)
	}
	if si.ModifiedAt.Unix() != 1787330474 {
		t.Fatalf("ModifiedAt=%v", si.ModifiedAt)
	}
}

func TestAgentWorkDirSwitcherFeedsOfficialThreadStartCwd(t *testing.T) {
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)

	started := ThreadInfo{ID: "thread-chat", Cwd: "/Users/developer/Projects/Chat"}
	captured := captureParams(s, "thread/start", map[string]any{
		"thread": started, "model": "gpt-5.6-luna", "modelProvider": "openai",
	})
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "0.149.0-alpha.4"}
	ep.client = cl
	a := New(map[string]any{"work_dir": "/Users/developer"})
	a.endpoint = ep

	a.SetWorkDir("/Users/developer/Projects/Chat")
	if got := a.GetWorkDir(); got != "/Users/developer/Projects/Chat" {
		t.Fatalf("GetWorkDir()=%q", got)
	}
	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if sess.CurrentSessionID() != "thread-chat" {
		t.Fatalf("session id=%q", sess.CurrentSessionID())
	}
	if len(*captured) != 1 {
		t.Fatalf("thread/start calls=%d", len(*captured))
	}
	expectParams(t, (*captured)[0], map[string]any{"cwd": "/Users/developer/Projects/Chat"})
}

func TestAgentCatalogRefreshSignalCoalesces(t *testing.T) {
	a := New(nil)
	signals := a.CatalogRefreshSignals()
	a.signalCatalogRefresh()
	a.signalCatalogRefresh()
	select {
	case <-signals:
	case <-time.After(time.Second):
		t.Fatal("missing catalog refresh signal")
	}
	select {
	case <-signals:
		t.Fatal("catalog refresh burst must coalesce")
	default:
	}
}

func TestAgentRenameSessionUsesOfficialNameAndMetadata(t *testing.T) {
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)

	officialName := "official normalized title"
	branch := "feature/chat"
	setCalls := captureParams(s, "thread/name/set", map[string]any{})
	readCalls := captureParams(s, "thread/read", map[string]any{"thread": ThreadInfo{
		ID: "thread-rename", Name: &officialName, Preview: "old preview",
		Cwd: "/Users/developer/Projects/Chat", ModelProvider: "openai",
		UpdatedAt: 1787333760, GitInfo: &GitInfo{Branch: &branch},
	}})
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "0.149.0-alpha.4"}
	ep.client = cl
	a := New(nil)
	a.endpoint = ep
	signals := a.CatalogRefreshSignals()

	got, err := a.RenameSession(context.Background(), "thread-rename", "  user typed title  ")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "thread-rename" || got.Summary != officialName ||
		got.Directory != "/Users/developer/Projects/Chat" || got.ProviderID != "openai" ||
		got.GitBranch != branch || got.ModifiedAt.Unix() != 1787333760 {
		t.Fatalf("rename 必须返回官方 thread/read 元数据：%+v", got)
	}
	if len(*setCalls) != 1 || len(*readCalls) != 1 {
		t.Fatalf("calls set/read=%d/%d", len(*setCalls), len(*readCalls))
	}
	expectParams(t, (*setCalls)[0], map[string]any{
		"threadId": "thread-rename", "name": "user typed title",
	})
	expectParams(t, (*readCalls)[0], map[string]any{"threadId": "thread-rename"})
	select {
	case <-signals:
	case <-time.After(time.Second):
		t.Fatal("confirmed rename must wake authoritative catalog refresh")
	}
}

func TestAgentTurnScopedAndFlatHistory(t *testing.T) {
	a := agentWithScript(t, func(method string) any {
		if method != "thread/read" {
			return nil
		}
		return map[string]any{"thread": ThreadInfo{ID: "th-1", Turns: []TurnInfo{{
			ID: "turn-1", Status: TurnStatusCompleted,
			Items: []json.RawMessage{
				jraw(`{"type":"userMessage","id":"u1","content":[{"type":"text","text":"hi"}]}`),
				jraw(`{"type":"agentMessage","id":"a1","text":"answer"}`),
			},
		}}}}
	})
	ctx := context.Background()
	turns, err := a.GetTurnScopedRichHistory(ctx, "th-1", 0)
	if err != nil || len(turns) != 1 || turns[0].TurnID != "turn-1" {
		t.Fatalf("turn-scoped：%v %+v", err, turns)
	}
	flat, err := a.GetRichSessionHistory(ctx, "th-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 2 || flat[0].Role != "user" || flat[0].ID != "u1" || flat[1].Role != "assistant" {
		t.Fatalf("flat：%+v", flat)
	}
	legacy, err := a.GetSessionHistory(ctx, "th-1", 0)
	if err != nil || len(legacy) != 2 {
		t.Fatalf("legacy：%v %+v", err, legacy)
	}
}

func TestAgentIsSessionActiveOfficialStatus(t *testing.T) {
	status := ThreadStatus{Type: ThreadStatusActive}
	a := agentWithScript(t, func(method string) any {
		if method != "thread/read" {
			return nil
		}
		return map[string]any{"thread": ThreadInfo{ID: "th-1", Status: status}}
	})
	ctx := context.Background()
	if !a.IsSessionActive(ctx, "th-1") {
		t.Fatal("官方 active 必须报告 active")
	}
	status = ThreadStatus{Type: ThreadStatusIdle}
	if a.IsSessionActive(ctx, "th-1") {
		t.Fatal("官方 idle 应报告非 active")
	}
}

func TestAgentIsSessionActiveConservativeOnError(t *testing.T) {
	// read 失败（官方错误）→ 保守按 active（接口契约 unknown/error ⇒ active）
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)
	s.mu.Lock()
	s.onSend = func(payload []byte) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(payload, &req)
		if req.Method != "thread/read" {
			return
		}
		frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID,
			"error": map[string]any{"code": -32000, "message": "boom"}})
		s.push(string(frame))
	}
	s.mu.Unlock()
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "x"}
	ep.client = cl
	a := New(nil)
	a.endpoint = ep
	if !a.IsSessionActive(context.Background(), "th-1") {
		t.Fatal("读失败必须保守按 active")
	}
}

func TestAgentInteractionSurfacesFailClosedBeforePhase4(t *testing.T) {
	// Phase 3 前（现已实现 turn 面）：审批/提问应答仍 fail closed
	s := &agentSession{agent: New(nil), threadID: "th", events: make(chan core.Event, 1)}
	if err := s.RespondPermission("r", core.PermissionResult{}); err == nil {
		t.Fatal("RespondPermission 在 Phase 4 前必须 fail closed")
	}
	if err := s.RespondQuestion("q", nil); err == nil {
		t.Fatal("RespondQuestion 在 Phase 4 前必须 fail closed")
	}
	// 未采样输入 kind / 不支持的 turn 选项 fail closed
	if err := s.SendWithOptions("hi", []core.ImageAttachment{{}}, nil, core.PromptOptions{}); err == nil {
		t.Fatal("image 输入未采样必须显式拒绝")
	}
	if err := s.SendWithOptions("hi", nil, nil, core.PromptOptions{Agent: "planner"}); err == nil {
		t.Fatal("turn 级 agent 覆盖官方不支持，必须显式拒绝（§7）")
	}
}

func TestAgentInstanceStatusMirror(t *testing.T) {
	a := New(nil)
	if ok, detail := a.InstanceStatus(); ok || detail == "" {
		t.Fatalf("未探测时应不可用且带说明：%v %q", ok, detail)
	}
	a2 := agentWithScript(t, func(string) any { return nil })
	if ok, detail := a2.InstanceStatus(); !ok || detail == "" {
		t.Fatalf("已探测应可用且带来源/版本：%v %q", ok, detail)
	}
	_ = time.Now
}
