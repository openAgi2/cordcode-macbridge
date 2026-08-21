package codexweb

// session_ssv2_test.go —— p2-ssv2 Agent 面测试：provider 方法（turn-scoped/flat/
// activity）与 ListSessions 映射，全部走 scripted transport（官方帧形状）。

import (
	"context"
	"encoding/json"
	"testing"
	"time"
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
			Status:        ThreadStatus{Type: ThreadStatusIdle},
			GitInfo:       &GitInfo{Branch: &branch},
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

func TestAgentStartSessionHonestError(t *testing.T) {
	a := New(nil)
	if _, err := a.StartSession(context.Background(), "s"); err == nil {
		t.Fatal("Phase 3 前 StartSession 必须显式报错（fail closed），不得返回假会话")
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
