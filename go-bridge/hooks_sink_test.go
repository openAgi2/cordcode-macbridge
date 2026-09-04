package gobridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// hooks_sink_test.go —— Phase 3 hooks 事件层定向测试。
// fixture：go-bridge/testdata/claude_hooks/stop_post.json 来自 Phase 0 dump
// hooks-posts.jsonl 的真实 Stop POST 原文（CLI 2.1.234，脱路径后保留原始字段形状）。

func TestClaudeHookEndpoint_TokenInPath(t *testing.T) {
	s := &ManagementServer{cfg: ManagementConfig{Token: "tok-1"}}
	// 正确 token 的 Heartbeat
	req := httptest.NewRequest(http.MethodPost, claudeHookEndpointPath+"tok-1",
		strings.NewReader(`{"hook_event_name":"Heartbeat"}`))
	w := httptest.NewRecorder()
	s.serveClaudeHook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("probe = %d, want 200", w.Code)
	}
	if !s.claudeHookHealth.probeOK.Load() {
		t.Fatalf("probeOK must flip on Heartbeat")
	}
	// 错 token
	req = httptest.NewRequest(http.MethodPost, claudeHookEndpointPath+"wrong",
		strings.NewReader(`{"hook_event_name":"Stop"}`))
	w = httptest.NewRecorder()
	s.serveClaudeHook(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", w.Code)
	}
}

func TestClaudeHookEndpoint_RealStopFixtureDispatch(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "claude_hooks", "stop_post.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var received []ClaudeHookEvent
	s := &ManagementServer{cfg: ManagementConfig{Token: "tok-1", Handlers: nil}}
	// 直接测解析+派发语义：用 Handlers 的 nudge/无效化路径分别有专测；这里验证
	// 真实 fixture 能完整解析进 ClaudeHookEvent。
	req := httptest.NewRequest(http.MethodPost, claudeHookEndpointPath+"tok-1", strings.NewReader(string(data)))
	w := httptest.NewRecorder()
	s.serveClaudeHook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("fixture POST = %d", w.Code)
	}
	_ = received

	// 再验证字段解析（直接 unmarshal 同一 fixture）
	var ev ClaudeHookEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("fixture parse: %v", err)
	}
	if ev.Event != "Stop" || ev.SessionID == "" || ev.LastAssistantMessage != "pong" {
		t.Fatalf("fixture fields: event=%q session=%q lastAssistant=%q", ev.Event, ev.SessionID, ev.LastAssistantMessage)
	}
	if s.claudeHookHealth.receiptCount.Load() != 1 {
		t.Fatalf("receipt count = %d", s.claudeHookHealth.receiptCount.Load())
	}
}

func TestClaudeHookEndpoint_UnknownShapeIgnored(t *testing.T) {
	s := &ManagementServer{cfg: ManagementConfig{Token: "tok-1"}}
	req := httptest.NewRequest(http.MethodPost, claudeHookEndpointPath+"tok-1",
		strings.NewReader(`{"something":"else"}`))
	w := httptest.NewRecorder()
	s.serveClaudeHook(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("unknown shape = %d, want 200 (fail closed, non-blocking)", w.Code)
	}
	if s.claudeHookHealth.receiptCount.Load() != 0 {
		t.Fatalf("unknown shape must not count as a receipt")
	}
}

// 订阅集：5 事件（含 StopFailure），PermissionRequest 与 SessionStart 故意缺席。
func TestClaudeHookSettingsJSON_SubscriptionSet(t *testing.T) {
	payload := claudeHookSettingsJSON("http://127.0.0.1:1/internal/hooks/claude/x")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	hooks := parsed["hooks"].(map[string]any)
	want := []string{"Stop", "StopFailure", "UserPromptSubmit", "ConfigChange", "SessionEnd"}
	if len(hooks) != len(want) {
		t.Fatalf("events = %v", hooks)
	}
	for _, ev := range want {
		if _, ok := hooks[ev]; !ok {
			t.Errorf("event %q missing", ev)
		}
	}
	for _, banned := range []string{"PermissionRequest", "SessionStart"} {
		if _, ok := hooks[banned]; ok {
			t.Errorf("event %q must NOT be subscribed", banned)
		}
	}
}

// holder 门控：端点未配置/自检未过 → provider false（纯轮询=现状）。
func TestClaudeHookHolder_ProbeGating(t *testing.T) {
	h := &claudeHookConfigHolder{}
	provider := h.SettingsProvider()
	if _, ok := provider(); ok {
		t.Fatalf("empty holder must not provide settings")
	}
	h.set("http://127.0.0.1:45678", "tok")
	if _, ok := provider(); ok {
		t.Fatalf("before probe pass, provider must stay off")
	}
	h.probeOK.Store(true)
	payload, ok := provider()
	if !ok || !strings.Contains(payload, "/internal/hooks/claude/tok") {
		t.Fatalf("after probe: ok=%v payload=%v", ok, payload)
	}
}

// Stop → nudge 注册通道立即触发（事件驱动定向刷新，不等 3s tick）。
func TestHandleClaudeHook_StopNudgesRelay(t *testing.T) {
	h := newTestHandlers(t)
	ch := make(chan struct{}, 1)
	h.registerClaudeRelayNudge("sess-1", ch)
	h.HandleClaudeHook(ClaudeHookEvent{Event: "Stop", SessionID: "sess-1"})
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("Stop hook must nudge the registered relay channel")
	}
	// 未注册会话：no-op 不 panic
	h.HandleClaudeHook(ClaudeHookEvent{Event: "Stop", SessionID: "unknown"})
	// unregister 幂等
	h.unregisterClaudeRelayNudge("sess-1", ch)
	h.HandleClaudeHook(ClaudeHookEvent{Event: "Stop", SessionID: "sess-1"})
	select {
	case <-ch:
		t.Fatalf("nudge after unregister must not fire")
	default:
	}
}

// ConfigChange → 已注册的 claudecode agent 收到失效调用。
func TestHandleClaudeHook_ConfigChangeInvalidates(t *testing.T) {
	h := newTestHandlers(t)
	fake := &fakeConfigInvalidator{name: "claudecode"}
	h.agents["claudecode"] = fake
	h.HandleClaudeHook(ClaudeHookEvent{Event: "ConfigChange", SessionID: "s", Source: "project_settings"})
	if fake.calls != 1 {
		t.Fatalf("invalidator calls = %d, want 1", fake.calls)
	}
	// 未注册 claudecode agent：no-op 不 panic
	delete(h.agents, "claudecode")
	h.HandleClaudeHook(ClaudeHookEvent{Event: "ConfigChange", SessionID: "s"})
}

type fakeConfigInvalidator struct {
	name  string
	calls int
}

func (f *fakeConfigInvalidator) Name() string { return f.name }
func (f *fakeConfigInvalidator) StartSession(context.Context, string) (core.AgentSession, error) {
	return nil, nil
}
func (f *fakeConfigInvalidator) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (f *fakeConfigInvalidator) Stop() error { return nil }
func (f *fakeConfigInvalidator) InvalidateSettingsModels(context.Context) {
	f.calls++
}

// exit-127 fixture（S3 静默失效）语义：端点收不到 POST 时行为=纯轮询——
// 由 holder 门控与 relay 兜底保证；这里验证 status 快照如实反映零接收。
func TestClaudeHookHealth_SnapshotHonest(t *testing.T) {
	var hh claudeHookHealth
	snap := hh.snapshot()
	if snap["receipts"] != int64(0) || snap["endpointProbeOk"] != false {
		t.Fatalf("initial snapshot must be honest zeros: %v", snap)
	}
	hh.probeOK.Store(true)
	hh.receiptCount.Add(2)
	snap = hh.snapshot()
	if snap["receipts"] != int64(2) || snap["endpointProbeOk"] != true {
		t.Fatalf("snapshot = %v", snap)
	}
}
