package gobridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cccode-macbridge/core"
)

// ── 最小 fakeAgent，仅实现 core.Agent ──

type descriptorFakeAgent struct {
	name string
}

func (f *descriptorFakeAgent) Name() string { return f.name }
func (f *descriptorFakeAgent) StartSession(_ context.Context, _ string) (core.AgentSession, error) {
	return nil, nil
}
func (f *descriptorFakeAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (f *descriptorFakeAgent) Stop() error { return nil }

// fullFakeAgent 覆盖所有可选接口，用于测试 capabilities 推导。
type fullFakeAgent struct {
	descriptorFakeAgent
}

func (f *fullFakeAgent) SetModel(string)                                    {}
func (f *fullFakeAgent) GetModel() string                                   { return "" }
func (f *fullFakeAgent) AvailableModels(context.Context) []core.ModelOption { return nil }
func (f *fullFakeAgent) SetWorkDir(string)                                  {}
func (f *fullFakeAgent) GetWorkDir() string                                 { return "" }
func (f *fullFakeAgent) AddAllowedTools(...string) error                    { return nil }
func (f *fullFakeAgent) GetAllowedTools() []string                          { return nil }
func (f *fullFakeAgent) SetProviders([]core.ProviderConfig)                 {}
func (f *fullFakeAgent) SetActiveProvider(string) bool                      { return true }
func (f *fullFakeAgent) GetActiveProvider() *core.ProviderConfig            { return nil }
func (f *fullFakeAgent) ListProviders() []core.ProviderConfig               { return nil }
func (f *fullFakeAgent) GetSessionHistory(context.Context, string, int) ([]core.HistoryEntry, error) {
	return nil, nil
}
func (f *fullFakeAgent) ListMemoryFiles(context.Context) ([]core.MemoryFile, error) { return nil, nil }
func (f *fullFakeAgent) ReadMemoryFile(context.Context, string) (*core.MemoryFile, error) {
	return nil, nil
}
func (f *fullFakeAgent) RunDiagnostics(context.Context, func(core.DiagnosticProgress)) (*core.DiagnosticReport, error) {
	return nil, nil
}
func (f *fullFakeAgent) GetTokenUsage(context.Context) (*core.TokenUsageReport, error) {
	return nil, nil
}
func (f *fullFakeAgent) SetMode(string)                             {}
func (f *fullFakeAgent) GetMode() string                            { return "" }
func (f *fullFakeAgent) PermissionModes() []core.PermissionModeInfo { return nil }
func (f *fullFakeAgent) RenameSession(context.Context, string, string) (*core.AgentSessionInfo, error) {
	return nil, nil
}
func (f *fullFakeAgent) ArchiveSession(context.Context, string, time.Time) (*core.AgentSessionInfo, error) {
	return nil, nil
}
func (f *fullFakeAgent) DeleteSession(context.Context, string) error             { return nil }
func (f *fullFakeAgent) FetchTodos(context.Context, string) ([]core.Todo, error) { return nil, nil }

// ── Tests ────────────────────────────────────────────────────────────────────

func TestClaudeDescriptorRequiresPolling(t *testing.T) {
	d := BuildAgentDescriptor("claude", &descriptorFakeAgent{name: "claudecode"}, "", nil)
	if d.LiveEvents != "session_process" {
		t.Fatalf("LiveEvents = %q, want session_process", d.LiveEvents)
	}
	if !d.RequiresPollingForExternalTurns {
		t.Fatal("RequiresPollingForExternalTurns = false, want true")
	}
	if d.Kind != "claude_code" {
		t.Fatalf("Kind = %q, want claude_code", d.Kind)
	}
	if d.DisplayName != "Claude Code" {
		t.Fatalf("DisplayName = %q, want Claude Code", d.DisplayName)
	}
}

func TestOpenCodeDescriptorBroadcastRequiresPollingProtection(t *testing.T) {
	d := BuildAgentDescriptor("opencode", &descriptorFakeAgent{name: "opencode"}, "", nil)
	if d.LiveEvents != "broadcast" {
		t.Fatalf("LiveEvents = %q, want broadcast", d.LiveEvents)
	}
	if !d.RequiresPollingForExternalTurns {
		t.Fatal("RequiresPollingForExternalTurns = false, want true")
	}
	if d.Kind != "opencode" {
		t.Fatalf("Kind = %q, want opencode", d.Kind)
	}
}

func TestCodexDescriptorBroadcastNoPolling(t *testing.T) {
	d := BuildAgentDescriptor("codex", &descriptorFakeAgent{name: "codex"}, "", nil)
	if d.LiveEvents != "broadcast" {
		t.Fatalf("LiveEvents = %q, want broadcast", d.LiveEvents)
	}
	if d.RequiresPollingForExternalTurns {
		t.Fatal("RequiresPollingForExternalTurns = true, want false")
	}
	if d.Kind != "codex" {
		t.Fatalf("Kind = %q, want codex", d.Kind)
	}
}

func TestUnknownDescriptorDefaultStatusAvailable(t *testing.T) {
	d := BuildAgentDescriptor("custom", &descriptorFakeAgent{name: "custom"}, "", nil)
	if d.Status != AgentStatusAvailable {
		t.Fatalf("Status = %q, want available", d.Status)
	}
}

func TestFullAgentCapabilities(t *testing.T) {
	agent := &fullFakeAgent{descriptorFakeAgent{name: "claudecode"}}
	d := BuildAgentDescriptor("claudecode", agent, "", nil)

	capSet := make(map[string]bool, len(d.Capabilities))
	for _, c := range d.Capabilities {
		capSet[c] = true
	}

	expected := []string{
		"model_switch", "session_state",
		"provider_switch", "session_history", "memory_read",
		"diagnostics", "usage_reporting", "permission_mode", "session_mutation",
		"content_chunking", "session_delete", "permission_resolve", "todos",
	}
	for _, e := range expected {
		if !capSet[e] {
			t.Errorf("missing capability %q in %v", e, d.Capabilities)
		}
	}
}

func TestCodexAppServerCompressionCapability(t *testing.T) {
	agent := &fullFakeAgent{descriptorFakeAgent{name: "codex"}}
	d := BuildAgentDescriptor("codex", agent, "app_server", nil)

	found := false
	for _, c := range d.Capabilities {
		if c == "compression" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("compression capability missing for codex app_server mode")
	}
}

func TestCodexExecNoCompressionCapability(t *testing.T) {
	agent := &fullFakeAgent{descriptorFakeAgent{name: "codex"}}
	d := BuildAgentDescriptor("codex", agent, "exec", nil)

	for _, c := range d.Capabilities {
		if c == "compression" {
			t.Fatal("compression should not appear in exec mode")
		}
	}
}

func TestOpenCodeNoPermissionResolve(t *testing.T) {
	agent := &fullFakeAgent{descriptorFakeAgent{name: "opencode"}}
	d := BuildAgentDescriptor("opencode", agent, "", nil)

	for _, c := range d.Capabilities {
		if c == "permission_resolve" {
			t.Fatal("opencode should not have permission_resolve")
		}
	}
}

func TestCodexNoPermissionResolve(t *testing.T) {
	agent := &fullFakeAgent{descriptorFakeAgent{name: "codex"}}
	d := BuildAgentDescriptor("codex", agent, "", nil)

	for _, c := range d.Capabilities {
		if c == "permission_resolve" {
			t.Fatal("codex should not have permission_resolve")
		}
	}
}

func TestOpenCodeAlwaysHasTodos(t *testing.T) {
	d := BuildAgentDescriptor("opencode", &descriptorFakeAgent{name: "opencode"}, "", nil)

	found := false
	for _, c := range d.Capabilities {
		if c == "todos" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("opencode always has todos capability")
	}
}

func TestBuildAllAgentDescriptors(t *testing.T) {
	agents := map[string]core.Agent{
		"codex":    &descriptorFakeAgent{name: "codex"},
		"claude":   &descriptorFakeAgent{name: "claudecode"},
		"opencode": &descriptorFakeAgent{name: "opencode"},
	}

	descs := BuildAllAgentDescriptors(agents, "exec", nil)
	if len(descs) != 3 {
		t.Fatalf("descriptor count = %d, want 3", len(descs))
	}

	if descs[0].ID != "claude" || descs[1].ID != "codex" || descs[2].ID != "opencode" {
		t.Fatalf("order = %s/%s/%s, want claude/codex/opencode",
			descs[0].ID, descs[1].ID, descs[2].ID)
	}
}

func TestBuildAllAgentDescriptorsEmpty(t *testing.T) {
	descs := BuildAllAgentDescriptors(map[string]core.Agent{}, "", nil)
	if len(descs) != 0 {
		t.Fatalf("descriptor count = %d, want 0", len(descs))
	}
}

func TestDescriptorJSONRoundTrip(t *testing.T) {
	agent := &fullFakeAgent{descriptorFakeAgent{name: "claudecode"}}
	original := BuildAgentDescriptor("claudecode", agent, "exec", nil)

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded AgentProviderDescriptor
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Kind != original.Kind {
		t.Errorf("Kind = %q, want %q", decoded.Kind, original.Kind)
	}
	if decoded.DisplayName != original.DisplayName {
		t.Errorf("DisplayName = %q, want %q", decoded.DisplayName, original.DisplayName)
	}
	if decoded.Status != original.Status {
		t.Errorf("Status = %q, want %q", decoded.Status, original.Status)
	}
	if decoded.LiveEvents != original.LiveEvents {
		t.Errorf("LiveEvents = %q, want %q", decoded.LiveEvents, original.LiveEvents)
	}
	if decoded.RequiresPollingForExternalTurns != original.RequiresPollingForExternalTurns {
		t.Errorf("RequiresPollingForExternalTurns = %v, want %v",
			decoded.RequiresPollingForExternalTurns, original.RequiresPollingForExternalTurns)
	}
	if len(decoded.Capabilities) != len(original.Capabilities) {
		t.Errorf("Capabilities length = %d, want %d",
			len(decoded.Capabilities), len(original.Capabilities))
	}
}

// ── Management API display-name tests (P3-6) ───────────────────────────────

func TestManagementAPI_GetDisplayName(t *testing.T) {
	cfg := ManagementConfig{
		Token:       "test-token",
		DisplayName: "TestMac",
	}
	srv := NewManagementServer(cfg)

	req := httptest.NewRequest("GET", "/internal/settings/display-name", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if name, _ := resp["displayName"].(string); name != "TestMac" {
		t.Errorf("displayName = %q, want TestMac", name)
	}
}

func TestManagementAPI_SetDisplayName(t *testing.T) {
	cfg := ManagementConfig{
		Token:       "test-token",
		DisplayName: "OldName",
	}
	srv := NewManagementServer(cfg)

	body := `{"displayName":"NewName"}`
	req := httptest.NewRequest("PUT", "/internal/settings/display-name", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if name, _ := resp["displayName"].(string); name != "NewName" {
		t.Errorf("displayName = %q, want NewName", name)
	}
	if srv.DisplayName() != "NewName" {
		t.Errorf("server DisplayName() = %q, want NewName", srv.DisplayName())
	}
}

func TestManagementAPI_SetDisplayName_Empty(t *testing.T) {
	cfg := ManagementConfig{
		Token:       "test-token",
		DisplayName: "OldName",
	}
	srv := NewManagementServer(cfg)

	req := httptest.NewRequest("PUT", "/internal/settings/display-name", strings.NewReader(`{"displayName":""}`))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestManagementAPI_DisplayName_AuthRequired(t *testing.T) {
	cfg := ManagementConfig{
		Token:       "test-token",
		DisplayName: "TestMac",
	}
	srv := NewManagementServer(cfg)

	req := httptest.NewRequest("GET", "/internal/settings/display-name", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no-auth status = %d, want 401", w.Code)
	}

	req = httptest.NewRequest("GET", "/internal/settings/display-name", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong-token status = %d, want 401", w.Code)
	}
}

// ── detectAgentStatus tests (P3-1) ──────────────────────────────────────────

func TestDetectAgentStatus_UnknownID(t *testing.T) {
	status, reason := detectAgentStatus("nonexistent", "exec", nil)
	if status != AgentStatusAvailable {
		t.Errorf("unknown agent status = %q, want available (passthrough)", status)
	}
	if reason != "" {
		t.Errorf("unknown agent reason = %q, want empty", reason)
	}
}

func TestDetectClaudeCLI_NotFound(t *testing.T) {
	status, reason := detectClaudeCLI()
	// CI/本地环境通常没装 claude CLI
	if status == AgentStatusAvailable && reason != "" {
		t.Errorf("unexpected: available with reason %q", reason)
	}
}

func TestDetectOpenCodeService_NotRunning(t *testing.T) {
	status, reason := detectOpenCodeService("http://localhost:64667", "", "")
	if status != AgentStatusServiceNotRunning && status != AgentStatusNotDetected {
		t.Errorf("opencode status = %q, want service_not_running or not_detected", status)
	}
	if reason == "" {
		t.Error("expected non-empty reason for unreachable opencode")
	}
}

func TestDetectCodexAppServerFallsBackToRunningProcess(t *testing.T) {
	original := detectCodexAppServerProcessFunc
	detectCodexAppServerProcessFunc = func(context.Context) bool { return true }
	defer func() { detectCodexAppServerProcessFunc = original }()

	status, reason := detectCodexAppServer("ws://127.0.0.1:1")
	if status != AgentStatusAvailable {
		t.Fatalf("codex status = %q, want available", status)
	}
	if reason != "" {
		t.Fatalf("codex reason = %q, want empty", reason)
	}
}
