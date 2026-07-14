package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openAgi2/cordcode-macbridge/agent/grokbuild"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// newTestHandlers builds a Handlers bound to a cancellable context and arranges
// Shutdown on test cleanup. This avoids leaking the cleanup/observation
// goroutines that NewHandlers() (context.Background()) would leave running
// across the test binary — required by T09 (tests must not depend on global
// default instances and must not leak background goroutines).
func newTestHandlers(t *testing.T) *Handlers {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := NewHandlersWithContext(ctx)
	h.Start(ctx) // T09: 显式启动 observation lease loop（构造函数不再自动起）
	t.Cleanup(func() {
		cancel()
		shutdownCtx, sc := context.WithTimeout(context.Background(), 2*time.Second)
		defer sc()
		_ = h.Shutdown(shutdownCtx)
	})
	return h
}

type fakeAgent struct {
	name               string
	startErr           error
	startCalls         []string
	sessions           []*fakeAgentSession
	sessionInfos       []core.AgentSessionInfo
	sessionListErr     error
	model              string
	reasoningEffort    string
	workDir            string
	allowed            []string
	sendHook           func(*fakeAgentSession, string)
	history            []core.HistoryEntry
	historyErr         error
	richHistory        []core.RichHistoryEntry
	richHistoryErr     error
	todos              []core.Todo
	todosErr           error
	agents             []core.AgentDescriptor
	agentsErr          error
	memoryFiles        []core.MemoryFile
	memoryByID         map[string]core.MemoryFile
	memoryErr          error
	diagnosticReport   *core.DiagnosticReport
	diagnosticErr      error
	diagnosticProgress []core.DiagnosticProgress
	usageReport        *core.TokenUsageReport
	usageErr           error
	mode               string
	permissionModes    []core.PermissionModeInfo
	renameResult       *core.AgentSessionInfo
	renameErr          error
	archiveResult      *core.AgentSessionInfo
	archiveErr         error
	providers          []core.ProviderConfig
	activeProvider     string
	generateSessionID  bool
	nextSessionIndex   int
	startedProviders   map[string]string
	runningSessionIDs  map[string]bool
	runningCalls       int
	liveProcesses      map[string]core.LiveSessionProcess
	alivePIDs          map[int]bool
	liveProcessCalls   int
	processAliveCalls  int
	lastProcessAliveID int
}

type unsupportedMutationAgent struct {
	name string
}

func (u *unsupportedMutationAgent) Name() string { return u.name }

func (u *unsupportedMutationAgent) StartSession(context.Context, string) (core.AgentSession, error) {
	return &fakeAgentSession{id: "unsupported", events: make(chan core.Event)}, nil
}

func (u *unsupportedMutationAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}

func (u *unsupportedMutationAgent) Stop() error { return nil }

func (f *fakeAgent) Name() string { return f.name }

func (f *fakeAgent) GetRunningSessionIDs(ctx context.Context) (map[string]bool, error) {
	f.runningCalls++
	return f.runningSessionIDs, nil
}

func (f *fakeAgent) LiveSessionProcess(ctx context.Context, sessionID string) (core.LiveSessionProcess, error) {
	f.liveProcessCalls++
	if f.liveProcesses == nil {
		return core.LiveSessionProcess{SessionID: sessionID}, nil
	}
	proc := f.liveProcesses[sessionID]
	if proc.SessionID == "" {
		proc.SessionID = sessionID
	}
	return proc, nil
}

func (f *fakeAgent) IsProcessAlive(ctx context.Context, pid int) bool {
	f.processAliveCalls++
	f.lastProcessAliveID = pid
	if f.alivePIDs == nil {
		return false
	}
	return f.alivePIDs[pid]
}

func (f *fakeAgent) StartSession(_ context.Context, sessionID string) (core.AgentSession, error) {
	if f.startErr != nil {
		return nil, f.startErr
	}
	if sessionID == "" && f.generateSessionID {
		f.nextSessionIndex++
		sessionID = fmt.Sprintf("generated-%d", f.nextSessionIndex)
	}
	sess := &fakeAgentSession{
		id:       sessionID,
		events:   make(chan core.Event, 8),
		sendHook: f.sendHook,
	}
	if f.startedProviders == nil {
		f.startedProviders = make(map[string]string)
	}
	f.startedProviders[sessionID] = f.activeProvider
	f.startCalls = append(f.startCalls, sessionID)
	f.sessions = append(f.sessions, sess)
	return sess, nil
}

func (f *fakeAgent) ListSessions(context.Context) ([]core.AgentSessionInfo, error) {
	if f.sessionListErr != nil {
		return nil, f.sessionListErr
	}
	return append([]core.AgentSessionInfo(nil), f.sessionInfos...), nil
}

func (f *fakeAgent) GetSessionHistory(context.Context, string, int) ([]core.HistoryEntry, error) {
	if f.historyErr != nil {
		return nil, f.historyErr
	}
	return append([]core.HistoryEntry(nil), f.history...), nil
}

func (f *fakeAgent) GetRichSessionHistory(context.Context, string, int) ([]core.RichHistoryEntry, error) {
	if f.richHistoryErr != nil {
		return nil, f.richHistoryErr
	}
	return append([]core.RichHistoryEntry(nil), f.richHistory...), nil
}

func (f *fakeAgent) FetchTodos(context.Context, string) ([]core.Todo, error) {
	if f.todosErr != nil {
		return nil, f.todosErr
	}
	return append([]core.Todo(nil), f.todos...), nil
}

func (f *fakeAgent) ListAgents(context.Context) ([]core.AgentDescriptor, error) {
	if f.agentsErr != nil {
		return nil, f.agentsErr
	}
	return append([]core.AgentDescriptor(nil), f.agents...), nil
}

func (f *fakeAgent) ListMemoryFiles(context.Context) ([]core.MemoryFile, error) {
	if f.memoryErr != nil {
		return nil, f.memoryErr
	}
	return append([]core.MemoryFile(nil), f.memoryFiles...), nil
}

func (f *fakeAgent) ReadMemoryFile(_ context.Context, fileID string) (*core.MemoryFile, error) {
	if f.memoryErr != nil {
		return nil, f.memoryErr
	}
	if f.memoryByID == nil {
		return nil, nil
	}
	file, ok := f.memoryByID[fileID]
	if !ok {
		return nil, nil
	}
	copyFile := file
	return &copyFile, nil
}

func TestScanSessionsFromProjectDirUsesJSONLTimestampNotFileMTime(t *testing.T) {
	projectDir := t.TempDir()
	sessionPath := filepath.Join(projectDir, "session-1.jsonl")
	content := strings.Join([]string{
		`{"type":"user","timestamp":"2026-05-19T14:02:30.585Z","cwd":"/tmp/cccode-project","message":{"role":"user","content":[{"type":"text","text":"handoff"}]}}`,
		`{"type":"assistant","timestamp":"2026-05-19T14:36:04.567Z","cwd":"/tmp/cccode-project","message":{"role":"assistant","content":[{"type":"text","text":"✅ 已接管任务。\n\n项目根目录"}]}}`,
		`{"type":"ai-title","aiTitle":"old session","sessionId":"session-1"}`,
	}, "\n")
	if err := os.WriteFile(sessionPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	touchedAt := time.Date(2026, 5, 20, 9, 51, 18, 0, time.Local)
	if err := os.Chtimes(sessionPath, touchedAt, touchedAt); err != nil {
		t.Fatal(err)
	}

	sessions := scanSessionsFromProjectDir(projectDir, "-tmp-cccode-project")
	if len(sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(sessions))
	}

	wantUpdated := time.Date(2026, 5, 19, 14, 36, 4, 567_000_000, time.UTC).UnixMilli()
	if got := sessions[0]["updatedAtMillis"]; got != wantUpdated {
		t.Fatalf("updatedAtMillis = %#v, want %d", got, wantUpdated)
	}
	wantCreated := time.Date(2026, 5, 19, 14, 2, 30, 585_000_000, time.UTC).UnixMilli()
	if got := sessions[0]["createdAtMillis"]; got != wantCreated {
		t.Fatalf("createdAtMillis = %#v, want %d", got, wantCreated)
	}
	if got := sessions[0]["title"]; got != "✅ 已接管任务。" {
		t.Fatalf("title = %#v", got)
	}
}

func (f *fakeAgent) RunDiagnostics(ctx context.Context, progress func(core.DiagnosticProgress)) (*core.DiagnosticReport, error) {
	for _, update := range f.diagnosticProgress {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if progress != nil {
			progress(update)
		}
	}
	if f.diagnosticErr != nil {
		return nil, f.diagnosticErr
	}
	if f.diagnosticReport == nil {
		return &core.DiagnosticReport{}, nil
	}
	copyReport := *f.diagnosticReport
	copyReport.Results = append([]core.DiagnosticResult(nil), f.diagnosticReport.Results...)
	return &copyReport, nil
}

func (f *fakeAgent) GetTokenUsage(context.Context) (*core.TokenUsageReport, error) {
	if f.usageErr != nil {
		return nil, f.usageErr
	}
	if f.usageReport == nil {
		return &core.TokenUsageReport{}, nil
	}
	copyReport := *f.usageReport
	copyReport.PerSessionBreakdown = append([]core.SessionTokenUsage(nil), f.usageReport.PerSessionBreakdown...)
	return &copyReport, nil
}

func (f *fakeAgent) RenameSession(_ context.Context, sessionID, title string) (*core.AgentSessionInfo, error) {
	if f.renameErr != nil {
		return nil, f.renameErr
	}
	if f.renameResult != nil {
		copySession := *f.renameResult
		return &copySession, nil
	}
	return &core.AgentSessionInfo{ID: sessionID, Summary: title}, nil
}

func (f *fakeAgent) ArchiveSession(_ context.Context, sessionID string, archivedAt time.Time) (*core.AgentSessionInfo, error) {
	if f.archiveErr != nil {
		return nil, f.archiveErr
	}
	if f.archiveResult != nil {
		copySession := *f.archiveResult
		return &copySession, nil
	}
	return &core.AgentSessionInfo{ID: sessionID, ArchivedAt: archivedAt}, nil
}

func (f *fakeAgent) Stop() error { return nil }

func (f *fakeAgent) SetProviders(providers []core.ProviderConfig) {
	f.providers = append([]core.ProviderConfig(nil), providers...)
	if f.activeProvider == "" {
		return
	}
	for _, provider := range f.providers {
		if provider.Name == f.activeProvider {
			return
		}
	}
	f.activeProvider = ""
}

func (f *fakeAgent) SetActiveProvider(name string) bool {
	if name == "" {
		f.activeProvider = ""
		return true
	}
	for _, provider := range f.providers {
		if provider.Name == name {
			f.activeProvider = name
			return true
		}
	}
	return false
}

func (f *fakeAgent) GetActiveProvider() *core.ProviderConfig {
	for _, provider := range f.providers {
		if provider.Name != f.activeProvider {
			continue
		}
		copyProvider := provider
		return &copyProvider
	}
	return nil
}

func (f *fakeAgent) ListProviders() []core.ProviderConfig {
	return append([]core.ProviderConfig(nil), f.providers...)
}

func (f *fakeAgent) SetModel(model string) { f.model = model }

func (f *fakeAgent) GetModel() string { return f.model }

func (f *fakeAgent) AvailableModels(context.Context) []core.ModelOption { return nil }

func (f *fakeAgent) SetReasoningEffort(effort string) { f.reasoningEffort = effort }

func (f *fakeAgent) GetReasoningEffort() string { return f.reasoningEffort }

func (f *fakeAgent) AvailableReasoningEfforts() []string {
	return []string{"low", "medium", "high", "xhigh", "max", "ultra"}
}

func (f *fakeAgent) SetMode(mode string) { f.mode = mode }

func (f *fakeAgent) GetMode() string { return f.mode }

func (f *fakeAgent) PermissionModes() []core.PermissionModeInfo {
	if f.permissionModes != nil {
		return append([]core.PermissionModeInfo(nil), f.permissionModes...)
	}
	return []core.PermissionModeInfo{
		{Key: "default", Name: "Default permissions", NameZh: "默认权限", Desc: "Run commands in a sandbox", DescZh: "在沙盒中运行命令"},
		{Key: "full-access", Name: "Full access", NameZh: "完全访问权限", Desc: "Full computer access", DescZh: "完全访问计算机"},
	}
}

func (f *fakeAgent) SetWorkDir(dir string) { f.workDir = dir }

func (f *fakeAgent) GetWorkDir() string { return f.workDir }

func (f *fakeAgent) AddAllowedTools(tools ...string) error {
	f.allowed = append(f.allowed, tools...)
	return nil
}

func (f *fakeAgent) GetAllowedTools() []string { return f.allowed }

func mustJSONRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	return raw
}

func readJSONMaps(t *testing.T, clientConn *websocket.Conn, count int) []map[string]any {
	t.Helper()
	messages := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		if err := clientConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("set read deadline failed: %v", err)
		}
		var payload map[string]any
		if err := clientConn.ReadJSON(&payload); err != nil {
			t.Fatalf("read json failed at message %d/%d: %v", i+1, count, err)
		}
		messages = append(messages, payload)
	}
	return messages
}

func hasString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, value := range a {
		seen[value]++
	}
	for _, value := range b {
		seen[value]--
		if seen[value] < 0 {
			return false
		}
	}
	return true
}

func TestBackendListSkipsPermissionResolveForOpenCode(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{name: "opencode"})

	backends := handlers.BackendList()
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}
	for _, cap := range backends[0].Capabilities {
		if cap == "permission_resolve" {
			t.Fatal("opencode advertised permission_resolve, want capability removed")
		}
	}
}

func TestBackendListSkipsPermissionResolveForCodex(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex"})

	backends := handlers.BackendList()
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}
	for _, cap := range backends[0].Capabilities {
		if cap == "permission_resolve" {
			t.Fatal("codex advertised permission_resolve, want capability removed")
		}
	}
}

func TestBackendListAdvertisesPermissionMode(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex"})

	backends := handlers.BackendList()
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}
	for _, cap := range backends[0].Capabilities {
		if cap == "permission_mode" {
			return
		}
	}
	t.Fatalf("capabilities = %#v, want permission_mode", backends[0].Capabilities)
}

func TestBackendListMatchesBuildAgentDescriptorCapabilities(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.SetCodexBackendMode("app_server")
	agent := &fakeAgent{name: "codex"}
	handlers.RegisterAgent("codex", agent)

	backends := handlers.BackendList()
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}

	descriptor := BuildAgentDescriptor("codex", agent, "app_server", nil)
	if !sameStringSet(backends[0].Capabilities, descriptor.Capabilities) {
		t.Fatalf("BackendList capabilities = %v, BuildAgentDescriptor capabilities = %v", backends[0].Capabilities, descriptor.Capabilities)
	}
	for _, cap := range []string{"compression", "question_reply"} {
		if !hasString(backends[0].Capabilities, cap) {
			t.Fatalf("BackendList missing %s: %v", cap, backends[0].Capabilities)
		}
		if !hasString(descriptor.Capabilities, cap) {
			t.Fatalf("BuildAgentDescriptor missing %s: %v", cap, descriptor.Capabilities)
		}
	}
}

func TestListPermissionModesReturnsCurrentMode(t *testing.T) {
	handlers := newTestHandlers(t)
	agent := &fakeAgent{
		name: "codex",
		mode: "default",
		permissionModes: []core.PermissionModeInfo{
			{Key: "default", Name: "Default permissions", NameZh: "默认权限", Desc: "Run commands in a sandbox", DescZh: "在沙盒中运行命令"},
			{Key: "full-access", Name: "Full access", NameZh: "完全访问权限", Desc: "Full computer access", DescZh: "完全访问计算机"},
		},
	}
	conn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleListPermissionModes(conn, WireMessage{RequestID: "req_modes", BackendID: "codex"}, agent)

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if got := data["currentMode"]; got != "default" {
		t.Fatalf("currentMode = %#v, want default", got)
	}
	modes, _ := data["modes"].([]any)
	if len(modes) != 2 {
		t.Fatalf("modes len = %d, want 2", len(modes))
	}
	first, _ := modes[0].(map[string]any)
	if first["id"] != "default" || first["localizedName"] != "默认权限" {
		t.Fatalf("first mode = %#v", first)
	}
}

func TestSetPermissionModeAppliesToLiveSessionWhenSupported(t *testing.T) {
	handlers := newTestHandlers(t)
	agent := &fakeAgent{name: "codex", mode: "default"}
	session := &fakeAgentSession{id: "ses_1", events: make(chan core.Event, 1), liveModeOK: true}
	handlers.putSessionWithMeta("ses_1", "codex", "/tmp/project", session)
	conn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleSetPermissionMode(conn, WireMessage{
		RequestID: "req_set_mode",
		BackendID: "codex",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_1",
			"mode":      "full-access",
		}),
	}, agent)

	messages := readJSONMaps(t, clientConn, 2)
	if got := messages[0]["event"]; got != "permission_mode_changed" {
		t.Fatalf("first event = %#v, want permission_mode_changed", got)
	}
	data, _ := messages[1]["data"].(map[string]any)
	if got := data["mode"]; got != "full-access" {
		t.Fatalf("mode = %#v, want full-access", got)
	}
	if got := data["appliesTo"]; got != "current_session" {
		t.Fatalf("appliesTo = %#v, want current_session", got)
	}
	if session.liveMode != "full-access" {
		t.Fatalf("session liveMode = %q, want full-access", session.liveMode)
	}
}

type readFileCaptureConn struct {
	data interface{}
	err  *WireError
}

func (c *readFileCaptureConn) SendJSON(any) {}
func (c *readFileCaptureConn) SendResult(_ string, data interface{}, err *WireError) {
	c.data = data
	c.err = err
}
func (c *readFileCaptureConn) SendEvent(string, string, string, interface{}) {}
func (c *readFileCaptureConn) AuthedDevice() *TrustedDeviceRecord            { return nil }
func (c *readFileCaptureConn) RemoteAddr() string                            { return "test:read-file" }
func (c *readFileCaptureConn) Close() error                                  { return nil }

func TestReadFileEnforcesAuthorizedWorkspaceBoundary(t *testing.T) {
	workspace := t.TempDir()
	secretDir := t.TempDir()
	allowedPath := filepath.Join(workspace, "main.go")
	secretPath := filepath.Join(secretDir, "management-token")
	envPath := filepath.Join(workspace, ".env")
	linkPath := filepath.Join(workspace, "linked-secret")
	if err := os.WriteFile(allowedPath, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretPath, []byte("do-not-leak"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("TOKEN=do-not-leak"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secretPath, linkPath); err != nil {
		t.Fatal(err)
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", workDir: workspace})

	tests := []struct {
		name     string
		path     string
		wantCode string
	}{
		{name: "absolute outside", path: secretPath, wantCode: "file.outside_authorized_root"},
		{name: "relative traversal", path: filepath.Join("..", filepath.Base(secretDir), "management-token"), wantCode: "file.outside_authorized_root"},
		{name: "symlink escape", path: linkPath, wantCode: "file.symlink_escape"},
		{name: "sensitive workspace file", path: envPath, wantCode: "file.sensitive_path_denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &readFileCaptureConn{}
			handlers.handleReadFile(conn, WireMessage{
				RequestID: "req_" + strings.ReplaceAll(tt.name, " ", "_"),
				BackendID: "codex",
				Params: mustJSONRaw(t, map[string]any{
					"path":      tt.path,
					"directory": workspace,
				}),
			})
			if conn.err == nil || conn.err.Code != tt.wantCode {
				t.Fatalf("error = %#v, want code %q", conn.err, tt.wantCode)
			}
			encoded, err := json.Marshal(struct {
				Data interface{}
				Err  *WireError
			}{Data: conn.data, Err: conn.err})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "do-not-leak") {
				t.Fatalf("response leaked secret content: %s", encoded)
			}
		})
	}

	conn := &readFileCaptureConn{}
	handlers.handleReadFile(conn, WireMessage{
		RequestID: "req_allowed",
		BackendID: "codex",
		Params: mustJSONRaw(t, map[string]any{
			"path":      allowedPath,
			"directory": workspace,
		}),
	})
	if conn.err != nil {
		t.Fatalf("allowed read error = %#v", conn.err)
	}
	data, _ := conn.data.(map[string]interface{})
	if got := data["content"]; got != "package main\n" {
		t.Fatalf("content = %#v, want allowed file content; data=%#v", got, conn.data)
	}
}

func TestReadFileFailsClosedWithoutServerAuthorizedWorkspace(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &unsupportedMutationAgent{name: "codex"})
	conn := &readFileCaptureConn{}

	handlers.handleReadFile(conn, WireMessage{
		RequestID: "req_no_root",
		BackendID: "codex",
		Params: mustJSONRaw(t, map[string]any{
			"path":      path,
			"directory": workspace,
		}),
	})
	if conn.err == nil || conn.err.Code != "file.outside_authorized_root" {
		t.Fatalf("error = %#v, want file.outside_authorized_root", conn.err)
	}
}

func TestBackendListAdvertisesMemoryDiagnosticsAndUsageCapabilities(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", &fakeAgent{
		name:             "claudecode",
		memoryFiles:      []core.MemoryFile{{ID: "project:claude", Name: "CLAUDE.md"}},
		diagnosticReport: &core.DiagnosticReport{},
		usageReport:      &core.TokenUsageReport{},
	})

	backends := handlers.BackendList()
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}

	capSet := make(map[string]bool)
	for _, cap := range backends[0].Capabilities {
		capSet[cap] = true
	}
	for _, required := range []string{"memory_read", "diagnostics", "usage_reporting"} {
		if !capSet[required] {
			t.Fatalf("capability %q missing", required)
		}
	}
	if !capSet["content_chunking"] {
		t.Fatal("capability \"content_chunking\" missing")
	}
}

func TestBackendListAdvertisesMemoryReadForCodexProvider(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{
		name:        "codex",
		memoryFiles: []core.MemoryFile{{ID: "project:agents", Name: "AGENTS.md", Scope: "project"}},
	})

	backends := handlers.BackendList()
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}

	found := false
	for _, cap := range backends[0].Capabilities {
		if cap == "memory_read" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("memory_read capability missing for codex backend with MemoryFileReader")
	}
}

func TestBackendListAdvertisesProviderSwitchForCodex(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{
		name: "codex",
		providers: []core.ProviderConfig{{
			Name:    "openai",
			BaseURL: "https://api.openai.com/v1",
		}},
	})

	backends := handlers.BackendList()
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}

	found := false
	for _, cap := range backends[0].Capabilities {
		if cap == "provider_switch" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("provider_switch capability missing for codex backend with ProviderSwitcher")
	}
}

func TestBackendListAdvertisesSessionMutationCapabilityWhenRenameAndArchiveExist(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", &fakeAgent{
		name:          "claudecode",
		renameResult:  &core.AgentSessionInfo{ID: "ses_1"},
		archiveResult: &core.AgentSessionInfo{ID: "ses_1"},
	})

	backends := handlers.BackendList()
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}

	found := false
	for _, cap := range backends[0].Capabilities {
		if cap == "session_mutation" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("session_mutation capability missing")
	}
}

func TestOpenCodeSendMessageUsesAgentSessionAndReusesSameConfig(t *testing.T) {
	var getSessionCount int
	var postMessageCount int
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_1":
			getSessionCount++
			_, _ = w.Write([]byte(`{"id":"ses_1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_1/message":
			postMessageCount++
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer proxyServer.Close()

	agent := &fakeAgent{name: "opencode"}
	agent.sendHook = func(sess *fakeAgentSession, _ string) {
		sess.events <- core.Event{Type: core.EventResult, Done: true}
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", agent)
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxyServer.URL, "", ""))

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	params := map[string]any{
		"sessionId": "ses_1",
		"content":   "hello",
		"directory": "/tmp/project",
		"model": map[string]any{
			"id":         "github-copilot/gpt-5-mini",
			"providerId": "github-copilot",
		},
	}

	for i := 0; i < 2; i++ {
		handlers.handleOpenCodeRPC(serverConn, WireMessage{
			BackendID: "opencode",
			Method:    "send_message",
			RequestID: "req",
			Params:    mustJSONRaw(t, params),
		})
		messages := readJSONMaps(t, clientConn, 4)
		if got := messages[0]["event"]; got != "session_state_changed" {
			t.Fatalf("first payload event = %#v, want session_state_changed(running)", got)
		}
		if got := messages[1]["type"]; got != "result" {
			t.Fatalf("second payload type = %#v, want result", got)
		}
		if got := messages[2]["event"]; got != "turn_completed" {
			t.Fatalf("third payload event = %#v, want turn_completed", got)
		}
		if got := messages[3]["event"]; got != "session_state_changed" {
			t.Fatalf("fourth payload event = %#v, want session_state_changed", got)
		}
	}

	if len(agent.startCalls) != 1 {
		t.Fatalf("start session calls = %d, want 1", len(agent.startCalls))
	}
	if getSessionCount != 2 {
		t.Fatalf("get session count = %d, want 2", getSessionCount)
	}
	if postMessageCount != 0 {
		t.Fatalf("HTTP message posts = %d, want 0", postMessageCount)
	}
	if agent.model != "github-copilot/gpt-5-mini" {
		t.Fatalf("model = %q, want github-copilot/gpt-5-mini", agent.model)
	}
	if agent.workDir != "/tmp/project" {
		t.Fatalf("workDir = %q, want /tmp/project", agent.workDir)
	}
	if got := len(agent.sessions[0].sentPrompts); got != 2 {
		t.Fatalf("prompt sends = %d, want 2", got)
	}
}

func TestOpenCodeSendMessageRecreatesSessionWhenConfigChanges(t *testing.T) {
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/session/ses_1" {
			_, _ = w.Write([]byte(`{"id":"ses_1"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer proxyServer.Close()

	agent := &fakeAgent{name: "opencode"}
	agent.sendHook = func(sess *fakeAgentSession, _ string) {
		sess.events <- core.Event{Type: core.EventResult, Done: true}
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", agent)
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxyServer.URL, "", ""))

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "send_message",
		RequestID: "req-1",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_1",
			"content":   "hello",
			"directory": "/tmp/project-a",
			"model": map[string]any{
				"id": "github-copilot/gpt-5-mini",
			},
		}),
	})
	_ = readJSONMaps(t, clientConn, 4)

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "send_message",
		RequestID: "req-2",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_1",
			"content":   "world",
			"directory": "/tmp/project-b",
			"model": map[string]any{
				"id": "github-copilot/gpt-5.1",
			},
		}),
	})
	_ = readJSONMaps(t, clientConn, 4)

	if len(agent.startCalls) != 2 {
		t.Fatalf("start session calls = %d, want 2", len(agent.startCalls))
	}
	if !agent.sessions[0].closed {
		t.Fatal("first session was not closed on config change")
	}
	if agent.model != "github-copilot/gpt-5.1" {
		t.Fatalf("model = %q, want github-copilot/gpt-5.1", agent.model)
	}
	if agent.workDir != "/tmp/project-b" {
		t.Fatalf("workDir = %q, want /tmp/project-b", agent.workDir)
	}
}

func TestOpenCodeAbortGenerationCallsHTTPAbortAndCleansSession(t *testing.T) {
	var abortCount int
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/session/ses_1/abort" {
			abortCount++
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer proxyServer.Close()

	handlers := newTestHandlers(t)
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxyServer.URL, "", ""))
	session := &fakeAgentSession{id: "ses_1", events: make(chan core.Event, 1)}
	handlers.putSession("ses_1", session)
	handlers.opencodeSessionOptions["ses_1"] = opencodeSessionOptions{model: "github-copilot/gpt-5-mini", directory: "/tmp/project"}

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "abort_generation",
		RequestID: "abort-1",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_1",
		}),
	})

	messages := readJSONMaps(t, clientConn, 3)
	if got := messages[0]["type"]; got != "result" {
		t.Fatalf("first payload type = %#v, want result", got)
	}
	if got := messages[1]["event"]; got != "turn_completed" {
		t.Fatalf("second payload event = %#v, want turn_completed", got)
	}
	if got := messages[2]["event"]; got != "session_state_changed" {
		t.Fatalf("third payload event = %#v, want session_state_changed", got)
	}
	if abortCount != 1 {
		t.Fatalf("abort count = %d, want 1", abortCount)
	}
	if !session.closed {
		t.Fatal("session was not closed during abort")
	}
	if _, ok := handlers.getSession("ses_1"); ok {
		t.Fatal("session entry still present after abort")
	}
	if _, ok := handlers.opencodeSessionOptions["ses_1"]; ok {
		t.Fatal("session config still present after abort")
	}
}

func TestOpenCodeListProjectsMapsWorktreeToDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/project" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"proj_1","worktree":"/Users/test/Project","vcs":"git"}]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer proxyServer.Close()

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{name: "opencode"})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxyServer.URL, "", ""))
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "list_projects",
		RequestID: "oc-projects-1",
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	projects, _ := data["projects"].([]any)
	if len(projects) != 1 {
		t.Fatalf("project count = %d, want 1", len(projects))
	}
	project, _ := projects[0].(map[string]any)
	if got := project["directory"]; got != "/Users/test/Project" {
		t.Fatalf("directory = %#v, want worktree path", got)
	}
	if got := project["name"]; got != "Project" {
		t.Fatalf("name = %#v, want basename when upstream name is absent", got)
	}
}

func TestOpenCodeListProjectsUsesDesktopVisibleProjectOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appSupport := filepath.Join(home, "Library", "Application Support", "ai.opencode.desktop")
	if err := os.MkdirAll(appSupport, 0o755); err != nil {
		t.Fatal(err)
	}
	expandedFalse := false
	serverState := map[string]any{
		"projects": map[string]any{
			"local": []map[string]any{
				{"worktree": "/Users/test/Open", "expanded": true},
				{"worktree": "/Users/test/Closed", "expanded": expandedFalse},
				{"worktree": "/Users/test/Second", "expanded": true},
			},
		},
	}
	serverRaw, _ := json.Marshal(serverState)
	storeRaw, _ := json.Marshal(map[string]string{"server": string(serverRaw)})
	if err := os.WriteFile(filepath.Join(appSupport, "opencode.global.dat"), storeRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/project" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"id":"closed","worktree":"/Users/test/Closed","vcs":"git"},
				{"id":"second","worktree":"/Users/test/Second","vcs":"git"},
				{"id":"open","worktree":"/Users/test/Open","vcs":"git"},
				{"id":"other","worktree":"/Users/test/Other","vcs":"git"}
			]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer proxyServer.Close()

	projects, err := NewOpenCodeProxy(proxyServer.URL, "", "").listProjects()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(projects))
	for _, project := range projects {
		got = append(got, project["directory"].(string))
	}
	want := []string{"/Users/test/Open", "/Users/test/Closed", "/Users/test/Second"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visible project order = %#v, want %#v", got, want)
	}
}

func TestOpenCodeListProjectsPrefersManagedURLScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appSupport := filepath.Join(home, "Library", "Application Support", "ai.opencode.desktop")
	if err := os.MkdirAll(appSupport, 0o755); err != nil {
		t.Fatal(err)
	}
	serverState := map[string]any{
		"currentSidecarUrl": "http://127.0.0.1:4096",
		"projects": map[string]any{
			"local": []map[string]any{
				{"worktree": "/Users/test/Local"},
			},
			"http://127.0.0.1:4096": []map[string]any{
				{"worktree": "/Users/test/ManagedA"},
				{"worktree": "/Users/test/ManagedB"},
			},
		},
	}
	serverRaw, _ := json.Marshal(serverState)
	storeRaw, _ := json.Marshal(map[string]string{"server": string(serverRaw)})
	if err := os.WriteFile(filepath.Join(appSupport, "opencode.global.dat"), storeRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	visible := openCodeDesktopVisibleProjects("http://127.0.0.1:4096/", []map[string]any{
		{"id": "local", "directory": "/Users/test/Local"},
		{"id": "a", "directory": "/Users/test/ManagedA"},
		{"id": "b", "directory": "/Users/test/ManagedB"},
	})
	got := []string{}
	for _, project := range visible {
		got = append(got, project["directory"].(string))
	}
	want := []string{"/Users/test/ManagedA", "/Users/test/ManagedB"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visible managed projects = %#v, want %#v", got, want)
	}
}

func TestOpenCodeListProjectsSlashScopeFallsBackLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appSupport := filepath.Join(home, "Library", "Application Support", "ai.opencode.desktop")
	if err := os.MkdirAll(appSupport, 0o755); err != nil {
		t.Fatal(err)
	}
	serverState := map[string]any{
		"projects": map[string]any{
			"local": []map[string]any{
				{"worktree": "/Users/test/Local"},
			},
			"http://127.0.0.1:4096/": []map[string]any{
				{"worktree": "/Users/test/SlashOnly"},
			},
		},
	}
	serverRaw, _ := json.Marshal(serverState)
	storeRaw, _ := json.Marshal(map[string]string{"server": string(serverRaw)})
	if err := os.WriteFile(filepath.Join(appSupport, "opencode.global.dat"), storeRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	visible := openCodeDesktopVisibleProjects("http://127.0.0.1:4096", []map[string]any{
		{"id": "local", "directory": "/Users/test/Local"},
		{"id": "slash", "directory": "/Users/test/SlashOnly"},
	})
	if len(visible) != 1 || visible[0]["directory"] != "/Users/test/Local" {
		t.Fatalf("visible = %#v, want local fallback when only slash scope exists", visible)
	}
}

func TestOpenCodeListSessionsFetchesLargePageAndPaginatesInMemory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var gotDirectory string
	var gotLimit string
	var gotRoots string
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/session" {
			http.NotFound(w, r)
			return
		}
		gotDirectory = r.Header.Get("x-opencode-directory")
		gotLimit = r.URL.Query().Get("limit")
		gotRoots = r.URL.Query().Get("roots")
		// Return 3 root sessions; client asks limit=2, so hasMore must be true.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"ses_1","title":"One","time":{"created":1000,"updated":3000}},
			{"id":"ses_2","title":"Two","time":{"created":1000,"updated":2000}},
			{"id":"ses_3","title":"Three","time":{"created":1000,"updated":1000}}
		]`))
	}))
	defer proxyServer.Close()

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{name: "opencode"})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxyServer.URL, "", ""))
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "list_sessions",
		RequestID: "oc-sessions-1",
		Params: mustJSONRaw(t, map[string]any{
			"directory": "/tmp/project",
			"rootsOnly": true,
			"limit":     2,
		}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	sessions, _ := data["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("session count = %d, want 2 (client limit sliced in-memory)", len(sessions))
	}
	// Upstream must be fetched with the large fetch budget, not the client limit.
	if gotLimit != "100" {
		t.Fatalf("upstream limit = %q, want 100", gotLimit)
	}
	if gotRoots != "true" {
		t.Fatalf("upstream roots = %q, want true", gotRoots)
	}
	if gotDirectory != "/tmp/project" {
		t.Fatalf("x-opencode-directory = %q, want /tmp/project", gotDirectory)
	}
	if got := data["hasMore"]; got != true {
		t.Fatalf("hasMore = %#v, want true (3 total > limit 2)", got)
	}
	nextCursor, _ := data["nextCursor"].(string)
	if nextCursor == "" {
		t.Fatalf("nextCursor must be present when hasMore is true")
	}

	// Page 2 with the cursor returns the remaining session and hasMore=false.
	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "list_sessions",
		RequestID: "oc-sessions-2",
		Params: mustJSONRaw(t, map[string]any{
			"directory": "/tmp/project",
			"rootsOnly": true,
			"limit":     2,
			"cursor":    nextCursor,
		}),
	})
	messages2 := readJSONMaps(t, clientConn, 1)
	data2, _ := messages2[0]["data"].(map[string]any)
	sessions2, _ := data2["sessions"].([]any)
	if len(sessions2) != 1 {
		t.Fatalf("page 2 session count = %d, want 1", len(sessions2))
	}
	if got := data2["hasMore"]; got != false {
		t.Fatalf("page 2 hasMore = %#v, want false", got)
	}

	logText := logs.String()
	for _, want := range []string{`msg="opencode list_sessions"`, "directory=/tmp/project", "limit=2", "result_count=2"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("diagnostic log missing %q in %s", want, logText)
		}
	}
}

func TestOpenCodeListSessionsRootsOnlyWithCursorNowSupported(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"ses_1","title":"One","time":{"created":1000,"updated":1000}}]`))
	}))
	defer proxyServer.Close()

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{name: "opencode"})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxyServer.URL, "", ""))
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	// rootsOnly + cursor is no longer rejected; it pages the in-memory list.
	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "list_sessions",
		RequestID: "oc-sessions-roots-cursor",
		Params: mustJSONRaw(t, map[string]any{
			"directory": "/tmp/project",
			"rootsOnly": true,
			"limit":     10,
			"cursor":    "opaque-cursor",
		}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["ok"]; got != true {
		t.Fatalf("ok = %#v, want true (rootsOnly+cursor now supported)", got)
	}
}

func TestOpenCodeListDirectoryUsesGenericHandler(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "child"), 0755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{name: "opencode"})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy("http://127.0.0.1:1", "", ""))
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "list_directory",
		RequestID: "oc-list-directory-1",
		Params:    mustJSONRaw(t, map[string]any{"path": root}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["ok"]; got != true {
		t.Fatalf("ok = %#v, want true; message=%#v", got, messages[0])
	}
	data, _ := messages[0]["data"].(map[string]any)
	if got := data["currentPath"]; got != root {
		t.Fatalf("currentPath = %#v, want %q", got, root)
	}
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("item count = %d, want 1", len(items))
	}
	item, _ := items[0].(map[string]any)
	if got := item["name"]; got != "child" {
		t.Fatalf("item name = %#v, want child", got)
	}
	if got := item["isDirectory"]; got != true {
		t.Fatalf("isDirectory = %#v, want true", got)
	}
}

func TestOpenCodeResolvePermissionReturnsUnsupported(t *testing.T) {
	handlers := newTestHandlers(t)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "resolve_permission",
		RequestID: "perm-1",
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["type"]; got != "result" {
		t.Fatalf("payload type = %#v, want result", got)
	}
	if got := messages[0]["ok"]; got != false {
		t.Fatalf("ok = %#v, want false", got)
	}
	errorPayload, _ := messages[0]["error"].(map[string]any)
	if got := errorPayload["code"]; got != "not_supported" {
		t.Fatalf("error code = %#v, want not_supported", got)
	}
}

func TestHandleSessionMutationsReturnNotSupported(t *testing.T) {
	agent := &unsupportedMutationAgent{name: "codex"}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)

	tests := []struct {
		method  string
		message string
	}{
		{method: "rename_session", message: "session rename not yet supported"},
		{method: "archive_session", message: "session archive not yet supported"},
		{method: "share_session", message: "session share is not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			serverConn, clientConn, cleanup := openTestConn(t)
			defer cleanup()

			handlers.HandleRPC(serverConn, WireMessage{
				BackendID: "codex",
				Method:    tt.method,
				RequestID: "mutation-1",
			})

			messages := readJSONMaps(t, clientConn, 1)
			if got := messages[0]["ok"]; got != false {
				t.Fatalf("ok = %#v, want false", got)
			}
			errorPayload, _ := messages[0]["error"].(map[string]any)
			if got := errorPayload["code"]; got != "not_supported" {
				t.Fatalf("error code = %#v, want not_supported", got)
			}
			if got := errorPayload["message"]; got != tt.message {
				t.Fatalf("error message = %#v, want %q", got, tt.message)
			}
		})
	}
}

func TestReadOnlySessionRequestsDoNotSwitchWorkDir(t *testing.T) {
	// list_sessions / get_session 不切换 workDir；但 get_session_messages 现在
	// 会使用 session 自带的 directory 切换 workDir（跨项目 session 历史加载需要）。
	tests := []struct {
		name        string
		method      string
		params      map[string]any
		wantWorkDir string
	}{
		{
			name:        "list sessions",
			method:      "list_sessions",
			params:      map[string]any{"directory": "/tmp/from-list"},
			wantWorkDir: "/tmp/original",
		},
		{
			name:        "get session",
			method:      "get_session",
			params:      map[string]any{"sessionId": "session-1", "directory": "/tmp/from-get"},
			wantWorkDir: "/tmp/original",
		},
		{
			name:        "get session messages with directory",
			method:      "get_session_messages",
			params:      map[string]any{"sessionId": "session-1", "directory": "/tmp/from-history"},
			wantWorkDir: "/tmp/from-history",
		},
		{
			name:        "get session messages without directory",
			method:      "get_session_messages",
			params:      map[string]any{"sessionId": "session-1"},
			wantWorkDir: "/tmp/original",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &fakeAgent{
				name:    "codex",
				workDir: "/tmp/original",
				sessionInfos: []core.AgentSessionInfo{{
					ID:        "session-1",
					Summary:   "Session 1",
					Directory: "/tmp/original",
				}},
				history: []core.HistoryEntry{{
					Role:      "user",
					Content:   "hello",
					Timestamp: time.Unix(1, 0).UTC(),
				}},
			}
			handlers := newTestHandlers(t)
			handlers.RegisterAgent("codex", agent)
			serverConn, clientConn, cleanup := openTestConn(t)
			defer cleanup()

			handlers.HandleRPC(serverConn, WireMessage{
				BackendID: "codex",
				Method:    tt.method,
				RequestID: "readonly-1",
				Params:    mustJSONRaw(t, tt.params),
			})

			_ = readJSONMaps(t, clientConn, 1)
			if got := agent.workDir; got != tt.wantWorkDir {
				t.Fatalf("workDir = %q, want %q", got, tt.wantWorkDir)
			}
		})
	}
}

func TestOpenCodeSessionMutationsReturnNotSupported(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &unsupportedMutationAgent{name: "opencode"})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy("http://127.0.0.1:1", "", ""))

	tests := []struct {
		method  string
		message string
	}{
		{method: "rename_session", message: "session rename not yet supported"},
		{method: "archive_session", message: "session archive not yet supported"},
		{method: "share_session", message: "session share is not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			serverConn, clientConn, cleanup := openTestConn(t)
			defer cleanup()

			handlers.handleOpenCodeRPC(serverConn, WireMessage{
				BackendID: "opencode",
				Method:    tt.method,
				RequestID: "oc-mutation-1",
			})

			messages := readJSONMaps(t, clientConn, 1)
			if got := messages[0]["ok"]; got != false {
				t.Fatalf("ok = %#v, want false", got)
			}
			errorPayload, _ := messages[0]["error"].(map[string]any)
			if got := errorPayload["code"]; got != "not_supported" {
				t.Fatalf("error code = %#v, want not_supported", got)
			}
			if got := errorPayload["message"]; got != tt.message {
				t.Fatalf("error message = %#v, want %q", got, tt.message)
			}
		})
	}
}

func TestHandleGetSessionMessagesPrefersRichHistoryProvider(t *testing.T) {
	startedAt := time.Unix(1710000000, 0).UTC()
	completedAt := startedAt.Add(95 * time.Second)
	agent := &fakeAgent{
		name: "codex",
		richHistory: []core.RichHistoryEntry{{
			ID:              "msg-1",
			Role:            "assistant",
			Content:         "final answer",
			Thinking:        "chain of thought summary",
			Timestamp:       time.Unix(1710000000, 0).UTC(),
			TurnStartedAt:   &startedAt,
			TurnCompletedAt: &completedAt,
			AgentName:       "build",
			ModelID:         "gpt-5-mini",
			ProviderID:      "github-copilot",
			Parts: []map[string]any{{
				"type":         "text",
				"content":      "final answer",
				"presentation": "final",
			}},
			Steps: []map[string]any{{
				"toolName": "bash",
				"status":   "completed",
			}},
		}},
		history: []core.HistoryEntry{{
			Role:      "assistant",
			Content:   "legacy fallback",
			Timestamp: time.Unix(1, 0).UTC(),
		}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "get_session_messages",
		RequestID: "hist-1",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_1",
		}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["type"]; got != "result" {
		t.Fatalf("payload type = %#v, want result", got)
	}
	data, _ := messages[0]["data"].(map[string]any)
	entries, _ := data["messages"].([]any)
	if len(entries) != 1 {
		t.Fatalf("message count = %d, want 1", len(entries))
	}
	entry, _ := entries[0].(map[string]any)
	if got := entry["content"]; got != "final answer" {
		t.Fatalf("content = %#v, want final answer", got)
	}
	if got := entry["thinking"]; got != "chain of thought summary" {
		t.Fatalf("thinking = %#v, want chain of thought summary", got)
	}
	parts, _ := entry["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts length = %d, want 1", len(parts))
	}
	steps, _ := entry["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("steps length = %d, want 1", len(steps))
	}
	if _, ok := entry["timestampMillis"].(float64); !ok {
		t.Fatalf("timestampMillis missing or wrong type: %#v", entry["timestampMillis"])
	}
	if got := entry["turnStartedAtMillis"]; got != float64(startedAt.UnixMilli()) {
		t.Fatalf("turnStartedAtMillis = %#v, want %d", got, startedAt.UnixMilli())
	}
	if got := entry["turnCompletedAtMillis"]; got != float64(completedAt.UnixMilli()) {
		t.Fatalf("turnCompletedAtMillis = %#v, want %d", got, completedAt.UnixMilli())
	}
	part, _ := parts[0].(map[string]any)
	if got := part["presentation"]; got != "final" {
		t.Fatalf("part presentation = %#v, want final", got)
	}
	if got := entry["agentName"]; got != "build" {
		t.Fatalf("agentName = %#v, want build", got)
	}
}

func TestHandleGetSessionMessagesIfNoneMatchShortCircuits(t *testing.T) {
	agent := &fakeAgent{
		name: "codex",
		richHistory: []core.RichHistoryEntry{{
			ID:        "msg-1",
			Role:      "assistant",
			Content:   "stable answer",
			Timestamp: time.Unix(1710000000, 0).UTC(),
		}},
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	// 1st call: no ifNoneMatch → full payload + revision.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "get_session_messages",
		RequestID: "r1",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_1",
		}),
	})
	resp1 := readJSONMaps(t, clientConn, 1)[0]
	data1, _ := resp1["data"].(map[string]any)
	rev, ok := data1["revision"].(string)
	if !ok || rev == "" {
		t.Fatalf("first call missing revision: %#v", data1)
	}
	if _, hasMsgs := data1["messages"]; !hasMsgs {
		t.Fatalf("first call should return full messages")
	}

	// 2nd call: same revision → unchanged, no messages body.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "get_session_messages",
		RequestID: "r2",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId":           "ses_1",
			"ifNoneMatchRevision": rev,
		}),
	})
	resp2 := readJSONMaps(t, clientConn, 1)[0]
	data2, _ := resp2["data"].(map[string]any)
	if data2["unchanged"] != true {
		t.Fatalf("second call (matching revision) should be unchanged: %#v", data2)
	}
	if data2["revision"] != rev {
		t.Fatalf("unchanged revision mismatch: got %#v want %#v", data2["revision"], rev)
	}
	if _, hasMsgs := data2["messages"]; hasMsgs {
		t.Fatalf("unchanged response must omit messages body")
	}

	// 3rd call: stale revision → full payload again.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "get_session_messages",
		RequestID: "r3",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId":           "ses_1",
			"ifNoneMatchRevision": "stale-rev",
		}),
	})
	resp3 := readJSONMaps(t, clientConn, 1)[0]
	data3, _ := resp3["data"].(map[string]any)
	if data3["unchanged"] == true {
		t.Fatalf("stale revision must not be unchanged: %#v", data3)
	}
	if _, hasMsgs := data3["messages"]; !hasMsgs {
		t.Fatalf("stale revision should return full messages")
	}
}

func TestHandleGetSessionMessagesSynthesizesMissingRichHistoryIDs(t *testing.T) {
	agent := &fakeAgent{
		name: "codex",
		richHistory: []core.RichHistoryEntry{
			{
				Role:      "user",
				Content:   "first",
				Timestamp: time.Unix(1710000000, 0).UTC(),
			},
			{
				Role:      "assistant",
				Content:   "second",
				Timestamp: time.Unix(1710000001, 0).UTC(),
			},
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "get_session_messages",
		RequestID: "hist-empty-id",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_empty_id"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	entries, _ := data["messages"].([]any)
	if len(entries) != 2 {
		t.Fatalf("message count = %d, want 2", len(entries))
	}

	first, _ := entries[0].(map[string]any)
	second, _ := entries[1].(map[string]any)
	firstID, _ := first["id"].(string)
	secondID, _ := second["id"].(string)
	if firstID == "" || secondID == "" {
		t.Fatalf("generated ids must be non-empty: first=%q second=%q", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatalf("generated ids must be unique: %q", firstID)
	}
}

func TestHandleGetSessionMessagesFallsBackToLegacyHistoryWhenRichHistoryUnsupported(t *testing.T) {
	agent := &fakeAgent{
		name:           "codex",
		richHistoryErr: core.ErrNotSupported,
		history: []core.HistoryEntry{{
			Role:      "assistant",
			Content:   "legacy content",
			Timestamp: time.Unix(1710000100, 0).UTC(),
		}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "get_session_messages",
		RequestID: "hist-2",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_2",
		}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	entries, _ := data["messages"].([]any)
	if len(entries) != 1 {
		t.Fatalf("message count = %d, want 1", len(entries))
	}
	entry, _ := entries[0].(map[string]any)
	if got := entry["content"]; got != "legacy content" {
		t.Fatalf("content = %#v, want legacy content", got)
	}
	parts, _ := entry["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts length = %d, want 1", len(parts))
	}
	if got := entry["thinking"]; got != nil {
		t.Fatalf("thinking = %#v, want nil", got)
	}
	if _, ok := entry["timestamp"].(string); !ok {
		t.Fatalf("timestamp missing or wrong type: %#v", entry["timestamp"])
	}
	if _, ok := entry["timestampMillis"].(float64); !ok {
		t.Fatalf("timestampMillis missing or wrong type: %#v", entry["timestampMillis"])
	}
}

// TestGrokBuildGetSessionMessagesStableWireIDs is the wire-level regression
// guard for the "执行中" stuck state.  A real grokbuild.Agent reads
// chat_history.jsonl from a temp grok_home.  Two consecutive get_session_messages
// calls must return identical wire "id" values — otherwise the iOS
// external-turn probe falsely detects "new" messages and activates generation.
//
// This test exercises the full path: Grok RichHistoryProvider →
// richHistoryEntryToWire → wire JSON, proving that stable IDs survive the
// wire mapping (not just the Go struct).
func TestGrokBuildGetSessionMessagesStableWireIDs(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions", "tmp")
	sesDir := filepath.Join(sessionsDir, "grok-stable-wire")
	if err := os.MkdirAll(sesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write chat_history.jsonl with system/synthetic lines (filtered) + real messages.
	history := []string{
		`{"type":"system","content":"You are Grok"}`,
		`{"type":"user","synthetic_reason":"system_reminder","content":"bloat"}`,
		`{"type":"user","content":[{"type":"text","text":"<user_query>\nhello\n</user_query>"}]}`,
		`{"type":"assistant","content":"hi there"}`,
		`{"type":"user","content":[{"type":"text","text":"<user_query>\nbye\n</user_query>"}]}`,
		`{"type":"assistant","content":"goodbye"}`,
	}
	historyBytes := strings.Join(history, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(sesDir, "chat_history.jsonl"), []byte(historyBytes), 0o644); err != nil {
		t.Fatal(err)
	}

	agent, err := grokbuild.New(map[string]any{"grok_home": home, "cli_path": "true"})
	if err != nil {
		t.Fatalf("grokbuild.New: %v", err)
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("grokbuild", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	// First request.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "grokbuild",
		Method:    "get_session_messages",
		RequestID: "r1",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "grok-stable-wire"}),
	})
	resp1 := readJSONMaps(t, clientConn, 1)
	if got := resp1[0]["type"]; got != "result" {
		t.Fatalf("first response type = %#v, want result", got)
	}
	data1, _ := resp1[0]["data"].(map[string]any)
	entries1, _ := data1["messages"].([]any)
	if len(entries1) != 4 {
		t.Fatalf("first: message count = %d, want 4 (2 user + 2 assistant)", len(entries1))
	}

	// Second request — same session, same file.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "grokbuild",
		Method:    "get_session_messages",
		RequestID: "r2",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "grok-stable-wire"}),
	})
	resp2 := readJSONMaps(t, clientConn, 1)
	if got := resp2[0]["type"]; got != "result" {
		t.Fatalf("second response type = %#v, want result", got)
	}
	data2, _ := resp2[0]["data"].(map[string]any)
	entries2, _ := data2["messages"].([]any)
	if len(entries2) != len(entries1) {
		t.Fatalf("count drift: first=%d second=%d", len(entries1), len(entries2))
	}

	for i := range entries1 {
		e1, _ := entries1[i].(map[string]any)
		e2, _ := entries2[i].(map[string]any)
		id1, _ := e1["id"].(string)
		id2, _ := e2["id"].(string)
		if id1 == "" {
			t.Errorf("entry %d: empty wire id", i)
		}
		if id1 != id2 {
			t.Errorf("wire id drift at index %d: first=%q second=%q", i, id1, id2)
		}
	}
}

func TestBackendListAdvertisesTodosWhenProviderExists(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", todos: []core.Todo{{Content: "ship it", Status: "pending"}}})

	backends := handlers.BackendList()
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}
	found := false
	for _, cap := range backends[0].Capabilities {
		if cap == "todos" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("todos capability missing for backend with TodoProvider")
	}
}

func TestRegisterAckAdvertisesTodosCapabilityForCodexProvider(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", todos: []core.Todo{{Content: "ship it", Status: "pending"}}})
	server := NewServer(handlers)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	server.handleRegister(serverConn, &WireMessage{
		Type:     "register",
		Client:   mustJSONRaw(t, map[string]any{"name": "test-client"}),
		Protocol: mustJSONRaw(t, map[string]any{"name": "cordcode-bridge", "version": 1}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["type"]; got != "register_ack" {
		t.Fatalf("payload type = %#v, want register_ack", got)
	}
	backends, ok := messages[0]["backends"].([]any)
	if !ok {
		t.Fatalf("backends type = %T, want []any", messages[0]["backends"])
	}
	for _, backend := range backends {
		backendMap, _ := backend.(map[string]any)
		if backendMap["id"] != "codex" {
			continue
		}
		caps, _ := backendMap["capabilities"].([]any)
		for _, cap := range caps {
			if cap == "todos" {
				return
			}
		}
		t.Fatalf("codex capabilities = %#v, want todos", caps)
	}
	t.Fatal("codex backend missing from register_ack")
}

func TestRegisterAckAdvertisesProviderSwitchCapabilityForCodex(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{
		name: "codex",
		providers: []core.ProviderConfig{{
			Name: "openai",
		}},
	})
	server := NewServer(handlers)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	server.handleRegister(serverConn, &WireMessage{
		Type:     "register",
		Client:   mustJSONRaw(t, map[string]any{"name": "test-client"}),
		Protocol: mustJSONRaw(t, map[string]any{"name": "cordcode-bridge", "version": 1}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	backends, ok := messages[0]["backends"].([]any)
	if !ok {
		t.Fatalf("backends type = %T, want []any", messages[0]["backends"])
	}
	for _, backend := range backends {
		backendMap, _ := backend.(map[string]any)
		if backendMap["id"] != "codex" {
			continue
		}
		caps, _ := backendMap["capabilities"].([]any)
		for _, cap := range caps {
			if cap == "provider_switch" {
				return
			}
		}
		t.Fatalf("codex capabilities = %#v, want provider_switch", caps)
	}
	t.Fatal("codex backend missing from register_ack")
}

func TestHandleListProvidersReturnsEmptyListForCodex(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex"})
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "list_providers",
		RequestID: "providers-empty-1",
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["type"]; got != "result" {
		t.Fatalf("payload type = %#v, want result", got)
	}
	data, _ := messages[0]["data"].(map[string]any)
	providers, _ := data["providers"].([]any)
	if len(providers) != 0 {
		t.Fatalf("provider count = %d, want 0", len(providers))
	}
	if got := data["activeProvider"]; got != "" {
		t.Fatalf("activeProvider = %#v, want empty string", got)
	}
}

func TestHandleSetProviderSwitchesCodexActiveProvider(t *testing.T) {
	agent := &fakeAgent{
		name: "codex",
		providers: []core.ProviderConfig{
			{Name: "openai", BaseURL: "https://api.openai.com/v1"},
			{Name: "azure", BaseURL: "https://azure.example.com/v1"},
		},
		activeProvider: "openai",
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "set_provider",
		RequestID: "provider-switch-1",
		Params:    mustJSONRaw(t, map[string]any{"provider": "azure"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if got := data["provider"]; got != "azure" {
		t.Fatalf("provider = %#v, want azure", got)
	}
	if got := data["appliesTo"]; got != "new_sessions" {
		t.Fatalf("appliesTo = %#v, want new_sessions", got)
	}
	if active := agent.GetActiveProvider(); active == nil || active.Name != "azure" {
		t.Fatalf("active provider = %#v, want azure", active)
	}

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "list_providers",
		RequestID: "provider-list-1",
	})

	messages = readJSONMaps(t, clientConn, 1)
	data, _ = messages[0]["data"].(map[string]any)
	if got := data["activeProvider"]; got != "azure" {
		t.Fatalf("activeProvider = %#v, want azure", got)
	}
	providers, _ := data["providers"].([]any)
	if len(providers) != 2 {
		t.Fatalf("provider count = %d, want 2", len(providers))
	}
	second, _ := providers[1].(map[string]any)
	if got := second["name"]; got != "azure" {
		t.Fatalf("providers[1].name = %#v, want azure", got)
	}
	if got := second["isActive"]; got != true {
		t.Fatalf("providers[1].isActive = %#v, want true", got)
	}
}

func TestHandleSetProviderReturnsNotFoundForCodex(t *testing.T) {
	agent := &fakeAgent{
		name:      "codex",
		providers: []core.ProviderConfig{{Name: "openai"}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "set_provider",
		RequestID: "provider-missing-1",
		Params:    mustJSONRaw(t, map[string]any{"provider": "missing"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["type"]; got != "result" {
		t.Fatalf("payload type = %#v, want result", got)
	}
	errorMap, _ := messages[0]["error"].(map[string]any)
	if got := errorMap["code"]; got != "not_found" {
		t.Fatalf("error.code = %#v, want not_found", got)
	}
}

func TestModelProviderForAgentUsesOpenAIForCodexModels(t *testing.T) {
	agent := &fakeAgent{name: "codex"}

	id, provider, providerID := modelProviderForAgent(agent, "gpt-5.3-codex")

	if id != "gpt-5.3-codex" || provider != "openai" || providerID != "openai" {
		t.Fatalf("model provider = (%q, %q, %q), want (gpt-5.3-codex, openai, openai)", id, provider, providerID)
	}
}

func TestModelProviderForAgentUsesActiveProviderForUnprefixedModels(t *testing.T) {
	agent := &fakeAgent{
		name:           "codex",
		providers:      []core.ProviderConfig{{Name: "local"}},
		activeProvider: "local",
	}

	id, provider, providerID := modelProviderForAgent(agent, "qwen3-coder")

	if id != "qwen3-coder" || provider != "local" || providerID != "local" {
		t.Fatalf("model provider = (%q, %q, %q), want (qwen3-coder, local, local)", id, provider, providerID)
	}
}

func TestModelProviderForAgentKeepsPrefixedProvider(t *testing.T) {
	agent := &fakeAgent{name: "codex"}

	id, provider, providerID := modelProviderForAgent(agent, "openrouter/anthropic/claude-sonnet-4.5")

	if id != "anthropic/claude-sonnet-4.5" || provider != "openrouter" || providerID != "openrouter" {
		t.Fatalf("model provider = (%q, %q, %q), want (anthropic/claude-sonnet-4.5, openrouter, openrouter)", id, provider, providerID)
	}
}

func TestCodexProviderSwitchOnlyAffectsNewSessions(t *testing.T) {
	agent := &fakeAgent{
		name:              "codex",
		generateSessionID: true,
		providers: []core.ProviderConfig{
			{Name: "openai", BaseURL: "https://api.openai.com/v1"},
			{Name: "azure", BaseURL: "https://azure.example.com/v1"},
		},
		activeProvider: "openai",
	}
	agent.sendHook = func(sess *fakeAgentSession, _ string) {
		sess.events <- core.Event{Type: core.EventResult, Done: true}
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	createAndSend := func(requestID string) string {
		t.Helper()
		handlers.HandleRPC(serverConn, WireMessage{
			BackendID: "codex",
			Method:    "create_session",
			RequestID: requestID,
		})
		messages := readJSONMaps(t, clientConn, 2)
		if got := messages[0]["event"]; got != "session_state_changed" {
			t.Fatalf("first message event = %#v, want session_state_changed", got)
		}
		data, _ := messages[1]["data"].(map[string]any)
		sessionID, _ := data["id"].(string)
		if sessionID == "" {
			t.Fatal("create_session returned empty session id")
		}
		if !strings.HasPrefix(sessionID, "pending-") {
			t.Fatalf("created session id = %q, want pending id", sessionID)
		}

		handlers.HandleRPC(serverConn, WireMessage{
			BackendID: "codex",
			Method:    "send_message",
			RequestID: requestID + "-send",
			Params: mustJSONRaw(t, map[string]any{
				"sessionId": sessionID,
				"content":   "hello",
			}),
		})
		_ = readJSONMaps(t, clientConn, 4)
		return agent.sessions[len(agent.sessions)-1].id
	}

	firstSessionID := createAndSend("create-provider-openai")
	if got := agent.startedProviders[firstSessionID]; got != "openai" {
		t.Fatalf("first session provider = %q, want openai", got)
	}

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "set_provider",
		RequestID: "provider-switch-runtime",
		Params:    mustJSONRaw(t, map[string]any{"provider": "azure"}),
	})
	_ = readJSONMaps(t, clientConn, 1)

	secondSessionID := createAndSend("create-provider-azure")
	if got := agent.startedProviders[secondSessionID]; got != "azure" {
		t.Fatalf("second session provider = %q, want azure", got)
	}
	if got := agent.startedProviders[firstSessionID]; got != "openai" {
		t.Fatalf("first session provider mutated to %q, want openai", got)
	}
}

func TestCodexCreateSessionIsLazyAndSendAppliesSelectedModel(t *testing.T) {
	agent := &fakeAgent{name: "codex", generateSessionID: true}
	agent.sendHook = func(sess *fakeAgentSession, _ string) {
		sess.events <- core.Event{Type: core.EventResult, Done: true}
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "create_session",
		RequestID: "create-codex-lazy",
	})

	messages := readJSONMaps(t, clientConn, 2)
	data, _ := messages[1]["data"].(map[string]any)
	sessionID, _ := data["id"].(string)
	if !strings.HasPrefix(sessionID, "pending-") {
		t.Fatalf("created session id = %q, want pending id", sessionID)
	}
	if len(agent.startCalls) != 0 {
		t.Fatalf("start calls after create_session = %d, want 0", len(agent.startCalls))
	}

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "send_message",
		RequestID: "send-codex-model",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": sessionID,
			"content":   "hello",
			"model": map[string]any{
				"id":         "gpt-5.4-mini",
				"providerId": "openai",
			},
		}),
	})
	_ = readJSONMaps(t, clientConn, 4)

	if agent.model != "gpt-5.4-mini" {
		t.Fatalf("agent model = %q, want gpt-5.4-mini", agent.model)
	}
	if len(agent.startCalls) != 1 {
		t.Fatalf("start calls after send_message = %d, want 1", len(agent.startCalls))
	}
}

func TestClaudeCreateSessionIsLazyAndSendAppliesSelectedModelAndEffort(t *testing.T) {
	agent := &fakeAgent{name: "claudecode", generateSessionID: true}
	agent.sendHook = func(sess *fakeAgentSession, _ string) {
		sess.events <- core.Event{Type: core.EventResult, SessionID: sess.id, Done: true}
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "create_session",
		RequestID: "create-claude-lazy",
	})

	messages := readJSONMaps(t, clientConn, 2)
	data, _ := messages[1]["data"].(map[string]any)
	sessionID, _ := data["id"].(string)
	if !strings.HasPrefix(sessionID, "pending-") {
		t.Fatalf("created session id = %q, want pending id", sessionID)
	}
	if len(agent.startCalls) != 0 {
		t.Fatalf("start calls after create_session = %d, want 0", len(agent.startCalls))
	}

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "send_message",
		RequestID: "send-claude-model-effort",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId":       sessionID,
			"content":         "hello",
			"reasoningEffort": "ultra",
			"model": map[string]any{
				"id":         "glm-5.2",
				"providerId": "default",
			},
		}),
	})
	_ = readJSONMaps(t, clientConn, 4)

	if agent.model != "glm-5.2" {
		t.Fatalf("agent model = %q, want glm-5.2", agent.model)
	}
	if agent.reasoningEffort != "ultra" {
		t.Fatalf("agent reasoning effort = %q, want ultra", agent.reasoningEffort)
	}
	if len(agent.startCalls) != 1 {
		t.Fatalf("start calls after send_message = %d, want 1", len(agent.startCalls))
	}
}

// TestClaudeSendMessageWithNilStubDoesNotPanic 回归 2026-06-30 真机崩溃：
// iOS 打开一个已存在的 Mac 会话时，file-relay/session-state 事件会先对该 sessionID
// 调 markRunning，在 registry 里留下一个 session==nil 的占位 trackedSession。
// getSession 对它返回 (nil, true)；旧 handleSendMessage 只判 ok 就调用 sess.Send，
// 对 nil 接口派发导致 panic，send_message RPC 不返回结果、消息丢失。
// 修复后：handleSendMessage 把 nil session 当"未持有真实会话"，回落到 StartSession（即 --resume）。
func TestClaudeSendMessageWithNilStubDoesNotPanic(t *testing.T) {
	agent := &fakeAgent{name: "claudecode", generateSessionID: true}
	agent.sendHook = func(sess *fakeAgentSession, _ string) {
		sess.events <- core.Event{Type: core.EventResult, SessionID: sess.id, Done: true}
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	// 模拟 iOS 已打开该会话、file-relay 把它标记为 running（session 字段为 nil 的占位 stub）。
	const realSessionID = "b36c6286-1116-4eec-b542-8cdc8a382573"
	handlers.sessions.markRunning(realSessionID)
	// 自检前提：占位 stub 确实是 nil session（getSession 返回 ok=true 但 session=nil）。
	if sess, ok := handlers.getSession(realSessionID); !ok || sess != nil {
		t.Fatalf("前提不成立：期望 markRunning 占位返回 (nil,true)，got sess=%v ok=%v", sess, ok)
	}

	// 修复前：下一行会 panic（nil 接口派发 sess.Send）。修复后：回落到 StartSession 续接。
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "send_message",
		RequestID: "send-nil-stub",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": realSessionID,
			"content":   "hello",
		}),
	})
	_ = readJSONMaps(t, clientConn, 4)

	if len(agent.startCalls) != 1 {
		t.Fatalf("nil-stub 应回落 StartSession：startCalls=%d want 1 (ids=%v)", len(agent.startCalls), agent.startCalls)
	}
	if agent.startCalls[0] != realSessionID {
		t.Fatalf("应以其真实 id resume：got %q want %q", agent.startCalls[0], realSessionID)
	}
}

func TestClaudeSendMessageReplacesFileRelayWithAgentRelay(t *testing.T) {
	agent := &fakeAgent{name: "claudecode", generateSessionID: true}
	agent.sendHook = func(sess *fakeAgentSession, _ string) {
		sess.events <- core.Event{Type: core.EventText, SessionID: sess.id, Content: "partial"}
		sess.events <- core.Event{Type: core.EventResult, SessionID: sess.id, Done: true}
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	// 冷启动打开既有 Claude session 时，get_session_messages/resume_session 可能先
	// 留下 transcript file relay 标记。send_message 必须用真实 AgentSession stdout
	// relay 接管，否则 iOS 只能靠历史轮询看到从头刷新的 assistant 内容。
	const sessionID = "b36c6286-2222-4eec-b542-8cdc8a382573"
	handlers.mu.Lock()
	handlers.relayRunning[sessionID] = true
	handlers.relayRunningKind[sessionID] = relayKindClaudeFile
	handlers.mu.Unlock()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "send_message",
		RequestID: "send-replaces-file-relay",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": sessionID,
			"content":   "hello",
		}),
	})

	messages := readJSONMaps(t, clientConn, 5)
	var sawText, sawCompleted bool
	for _, message := range messages {
		switch message["event"] {
		case "text_delta":
			data, _ := message["data"].(map[string]any)
			sawText = data["delta"] == "partial"
		case "turn_completed":
			sawCompleted = true
		}
	}
	if !sawText || !sawCompleted {
		t.Fatalf("agent relay 未接管 file relay：sawText=%v sawCompleted=%v messages=%v", sawText, sawCompleted, messages)
	}
}

func TestDrainHistoryEventsWaitsForClaudeResumeDrainSignal(t *testing.T) {
	drained := make(chan struct{})
	session := &fakeAgentSession{
		id:               "claude-resume",
		events:           make(chan core.Event, 1),
		historyDrainDone: drained,
	}

	started := time.Now()
	time.AfterFunc(250*time.Millisecond, func() {
		close(drained)
	})
	drainHistoryEvents(session)

	if elapsed := time.Since(started); elapsed < 200*time.Millisecond {
		t.Fatalf("drainHistoryEvents returned too early after %s; want it to wait for drain signal", elapsed)
	}
}

func TestDetectClaudeTranscriptStateIgnoresResumeMetaContinuation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	lines := []string{
		`{"type":"user","timestamp":"2026-07-01T08:00:00Z","message":{"role":"user","content":"first real prompt"}}`,
		`{"type":"assistant","timestamp":"2026-07-01T08:00:01Z","message":{"id":"assistant-1","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`,
		`{"type":"user","isMeta":true,"timestamp":"2026-07-01T08:01:00Z","message":{"role":"user","content":[{"type":"text","text":"Continue from where you left off."}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	handlers := &Handlers{}
	if got := handlers.detectClaudeTranscriptState(path); got != "idle" {
		t.Fatalf("state with pending resume meta = %q, want idle", got)
	}

	lines = append(lines,
		`{"type":"assistant","timestamp":"2026-07-01T08:01:00Z","message":{"id":"assistant-meta","role":"assistant","content":[{"type":"text","text":"No response requested."}],"stop_reason":"end_turn"}}`,
	)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := handlers.detectClaudeTranscriptState(path); got != "idle" {
		t.Fatalf("state after resume no-response = %q, want idle", got)
	}

	lines = append(lines,
		`{"type":"user","timestamp":"2026-07-01T08:01:01Z","message":{"role":"user","content":"second real prompt"}}`,
	)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := handlers.detectClaudeTranscriptState(path); got != "running" {
		t.Fatalf("state after real user = %q, want running", got)
	}
}

func TestCodexPendingSessionRebindsToRealSessionID(t *testing.T) {
	agent := &fakeAgent{name: "codex", generateSessionID: true}
	agent.sendHook = func(sess *fakeAgentSession, _ string) {
		sess.id = "real-codex-thread"
		sess.events <- core.Event{Type: core.EventText, Content: "bonjour"}
		sess.events <- core.Event{Type: core.EventResult, SessionID: "real-codex-thread", Done: true}
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "create_session",
		RequestID: "create-codex-rebind",
	})
	messages := readJSONMaps(t, clientConn, 2)
	data, _ := messages[1]["data"].(map[string]any)
	pendingID, _ := data["id"].(string)
	if !strings.HasPrefix(pendingID, "pending-") {
		t.Fatalf("created session id = %q, want pending id", pendingID)
	}

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "send_message",
		RequestID: "send-codex-rebind",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": pendingID,
			"content":   "hello",
		}),
	})
	messages = readJSONMaps(t, clientConn, 4)
	var textSessionID any
	var completedSessionID any
	for _, message := range messages {
		switch message["event"] {
		case "text_delta":
			textSessionID = message["sessionId"]
		case "turn_completed":
			completedSessionID = message["sessionId"]
		}
	}
	if textSessionID != "real-codex-thread" {
		t.Fatalf("text event sessionId = %#v, want real-codex-thread; messages=%#v", textSessionID, messages)
	}
	if completedSessionID != "real-codex-thread" {
		t.Fatalf("turn completed sessionId = %#v, want real-codex-thread; messages=%#v", completedSessionID, messages)
	}
	if got := handlers.resolveSessionIDForActiveSession(pendingID); got != "real-codex-thread" {
		t.Fatalf("resolved session id = %q, want real-codex-thread", got)
	}
}

func TestHandleFetchTodosReturnsProviderData(t *testing.T) {
	agent := &fakeAgent{
		name: "codex",
		todos: []core.Todo{{
			Content:  "wire provider support",
			Status:   "in_progress",
			Priority: "high",
		}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "fetch_todos",
		RequestID: "todo-1",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_1",
		}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["type"]; got != "result" {
		t.Fatalf("payload type = %#v, want result", got)
	}
	data, _ := messages[0]["data"].(map[string]any)
	todos, _ := data["todos"].([]any)
	if len(todos) != 1 {
		t.Fatalf("todo count = %d, want 1", len(todos))
	}
	todo, _ := todos[0].(map[string]any)
	if got := todo["content"]; got != "wire provider support" {
		t.Fatalf("content = %#v, want wire provider support", got)
	}
	if got := todo["priority"]; got != "high" {
		t.Fatalf("priority = %#v, want high", got)
	}
}

func TestCodexTodosBridgeFlowKeepsFetchAuthoritativeAfterPlanEvent(t *testing.T) {
	agent := &fakeAgent{
		name: "codex",
		todos: []core.Todo{{
			Content:  "persisted snapshot",
			Status:   "completed",
			Priority: "high",
		}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	assertFetchTodos := func(requestID string) {
		t.Helper()
		handlers.HandleRPC(serverConn, WireMessage{
			BackendID: "codex",
			Method:    "fetch_todos",
			RequestID: requestID,
			Params: mustJSONRaw(t, map[string]any{
				"sessionId": "ses_1",
			}),
		})

		messages := readJSONMaps(t, clientConn, 1)
		if got := messages[0]["type"]; got != "result" {
			t.Fatalf("payload type = %#v, want result", got)
		}
		data, _ := messages[0]["data"].(map[string]any)
		todos, _ := data["todos"].([]any)
		if len(todos) != 1 {
			t.Fatalf("todo count = %d, want 1", len(todos))
		}
		todo, _ := todos[0].(map[string]any)
		if got := todo["content"]; got != "persisted snapshot" {
			t.Fatalf("content = %#v, want persisted snapshot", got)
		}
		if got := todo["status"]; got != "completed" {
			t.Fatalf("status = %#v, want completed", got)
		}
		if got := todo["priority"]; got != "high" {
			t.Fatalf("priority = %#v, want high", got)
		}
	}

	assertFetchTodos("todo-before")

	session := &fakeAgentSession{
		id:     "ses_1",
		events: make(chan core.Event, 2),
	}
	session.events <- core.Event{
		Type: core.EventPlan,
		Plan: []core.Todo{{Content: "live update", Status: "in_progress"}},
	}
	session.events <- core.Event{Type: core.EventResult, Done: true, Content: "done"}
	close(session.events)

	done := make(chan struct{})
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{
		BackendID: "codex",
		SessionID: "ses_1",
	})
	go func() {
		handlers.relayEvents(serverConn, session, "ses_1", "codex")
		close(done)
	}()

	messages := readJSONMaps(t, clientConn, 3)
	if got := messages[0]["event"]; got != "todos_updated" {
		t.Fatalf("first event = %#v, want todos_updated", got)
	}
	eventData, _ := messages[0]["data"].(map[string]any)
	liveTodos, _ := eventData["todos"].([]any)
	if len(liveTodos) != 1 {
		t.Fatalf("live todo count = %d, want 1", len(liveTodos))
	}
	liveTodo, _ := liveTodos[0].(map[string]any)
	if got := liveTodo["content"]; got != "live update" {
		t.Fatalf("live content = %#v, want live update", got)
	}
	if got := liveTodo["status"]; got != "in_progress" {
		t.Fatalf("live status = %#v, want in_progress", got)
	}
	if got := liveTodo["priority"]; got != "normal" {
		t.Fatalf("live priority = %#v, want normal", got)
	}
	if got := messages[1]["event"]; got != "turn_completed" {
		t.Fatalf("second event = %#v, want turn_completed", got)
	}
	if got := messages[2]["event"]; got != "session_state_changed" {
		t.Fatalf("third event = %#v, want session_state_changed", got)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relayEvents did not finish after plan event flow")
	}

	assertFetchTodos("todo-after")
}

func TestHandleListMemoryFilesForCodexProvider(t *testing.T) {
	agent := &fakeAgent{
		name: "codex",
		memoryFiles: []core.MemoryFile{
			{
				ID:           "project:agents",
				Name:         "AGENTS.md",
				Description:  "项目级 Codex 指令文件",
				SizeBytes:    42,
				LastModified: time.Unix(1710000300, 0).UTC(),
				ETag:         "etag-project",
				Scope:        "project",
			},
			{
				ID:           "global:agents",
				Name:         "AGENTS.md",
				Description:  "全局 Codex 指令文件",
				SizeBytes:    21,
				LastModified: time.Unix(1710000400, 0).UTC(),
				ETag:         "etag-global",
				Scope:        "global",
			},
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "list_memory_files",
		RequestID: "codex-memory-list-1",
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	files, _ := data["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("file count = %d, want 2", len(files))
	}
	projectFile, _ := files[0].(map[string]any)
	if got := projectFile["id"]; got != "project:agents" {
		t.Fatalf("project file id = %#v, want project:agents", got)
	}
	if got := projectFile["fileName"]; got != "AGENTS.md" {
		t.Fatalf("project fileName = %#v, want AGENTS.md", got)
	}
	if got := projectFile["scope"]; got != "project" {
		t.Fatalf("project scope = %#v, want project", got)
	}
	if got := projectFile["content"]; got != nil {
		t.Fatalf("project content = %#v, want nil for list response", got)
	}
	globalFile, _ := files[1].(map[string]any)
	if got := globalFile["id"]; got != "global:agents" {
		t.Fatalf("global file id = %#v, want global:agents", got)
	}
	if got := globalFile["scope"]; got != "global" {
		t.Fatalf("global scope = %#v, want global", got)
	}
}

func TestHandleReadMemoryFileForCodexProvider(t *testing.T) {
	agent := &fakeAgent{
		name: "codex",
		memoryByID: map[string]core.MemoryFile{
			"project:agents": {
				ID:           "project:agents",
				Name:         "AGENTS.md",
				Description:  "项目级 Codex 指令文件",
				SizeBytes:    18,
				LastModified: time.Unix(1710000400, 0).UTC(),
				ETag:         "etag-project",
				Scope:        "project",
				Content:      "# codex memory\n",
			},
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "read_memory_file",
		RequestID: "codex-memory-read-1",
		Params:    mustJSONRaw(t, map[string]any{"fileId": "project:agents"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if got := data["content"]; got != "# codex memory\n" {
		t.Fatalf("content = %#v, want markdown body", got)
	}
	if got := data["scope"]; got != "project" {
		t.Fatalf("scope = %#v, want project", got)
	}
	if got := data["fileName"]; got != "AGENTS.md" {
		t.Fatalf("fileName = %#v, want AGENTS.md", got)
	}
	if got := data["id"]; got != "project:agents" {
		t.Fatalf("id = %#v, want project:agents", got)
	}
}

func TestHandleListMemoryFilesReturnsProviderData(t *testing.T) {
	agent := &fakeAgent{
		name: "claudecode",
		memoryFiles: []core.MemoryFile{{
			ID:           "project:claude",
			Name:         "CLAUDE.md",
			Description:  "项目级 Claude 指令文件",
			SizeBytes:    42,
			LastModified: time.Unix(1710000300, 0).UTC(),
			ETag:         "etag-1",
			Scope:        "project",
		}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "list_memory_files",
		RequestID: "memory-list-1",
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	files, _ := data["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("file count = %d, want 1", len(files))
	}
	file, _ := files[0].(map[string]any)
	if got := file["id"]; got != "project:claude" {
		t.Fatalf("id = %#v, want project:claude", got)
	}
	if got := file["fileName"]; got != "CLAUDE.md" {
		t.Fatalf("fileName = %#v, want CLAUDE.md", got)
	}
	if got := file["content"]; got != nil {
		t.Fatalf("content = %#v, want nil for list response", got)
	}
}

func TestHandleReadMemoryFileReturnsProviderData(t *testing.T) {
	agent := &fakeAgent{
		name: "claudecode",
		memoryByID: map[string]core.MemoryFile{
			"project:claude": {
				ID:           "project:claude",
				Name:         "CLAUDE.md",
				Description:  "项目级 Claude 指令文件",
				SizeBytes:    18,
				LastModified: time.Unix(1710000400, 0).UTC(),
				ETag:         "etag-2",
				Scope:        "project",
				Content:      "# project memory\n",
			},
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "read_memory_file",
		RequestID: "memory-read-1",
		Params:    mustJSONRaw(t, map[string]any{"fileId": "project:claude"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if got := data["content"]; got != "# project memory\n" {
		t.Fatalf("content = %#v, want markdown body", got)
	}
	if got := data["scope"]; got != "project" {
		t.Fatalf("scope = %#v, want project", got)
	}
}

func TestHandleGetUsageReturnsProviderData(t *testing.T) {
	agent := &fakeAgent{
		name: "claudecode",
		usageReport: &core.TokenUsageReport{
			TotalTokensUsed:     33,
			InputTokens:         10,
			OutputTokens:        20,
			CacheReadTokens:     2,
			CacheCreationTokens: 1,
			PerSessionBreakdown: []core.SessionTokenUsage{{
				SessionID:           "ses_1",
				TokensUsed:          33,
				InputTokens:         10,
				OutputTokens:        20,
				CacheReadTokens:     2,
				CacheCreationTokens: 1,
			}},
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "get_usage",
		RequestID: "usage-1",
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if got := data["totalTokensUsed"]; got != float64(33) {
		t.Fatalf("totalTokensUsed = %#v, want 33", got)
	}
	breakdown, _ := data["perSessionBreakdown"].([]any)
	if len(breakdown) != 1 {
		t.Fatalf("breakdown length = %d, want 1", len(breakdown))
	}
}

func TestHandleRunDiagnosticsStreamsProgressAndCompletion(t *testing.T) {
	agent := &fakeAgent{
		name: "claudecode",
		diagnosticProgress: []core.DiagnosticProgress{{
			CheckID: "cli",
			Status:  "running",
			Message: "checking",
		}, {
			CheckID: "cli",
			Status:  "passed",
			Message: "ok",
		}},
		diagnosticReport: &core.DiagnosticReport{
			OverallStatus: "healthy",
			Results: []core.DiagnosticResult{{
				ID:       "cli",
				Name:     "Claude CLI 可用性",
				Status:   "passed",
				Message:  "ok",
				Severity: "required",
			}},
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "run_diagnostics",
		RequestID: "diag-1",
	})

	messages := readJSONMaps(t, clientConn, 4)
	if got := messages[0]["type"]; got != "result" {
		t.Fatalf("first payload type = %#v, want result", got)
	}
	data, _ := messages[0]["data"].(map[string]any)
	runID, _ := data["diagnosticRunId"].(string)
	if runID == "" {
		t.Fatal("diagnosticRunId missing")
	}
	if got := messages[1]["event"]; got != "diagnostic_progress" {
		t.Fatalf("second payload event = %#v, want diagnostic_progress", got)
	}
	if got := messages[3]["event"]; got != "diagnostic_completed" {
		t.Fatalf("fourth payload event = %#v, want diagnostic_completed", got)
	}
	completedData, _ := messages[3]["data"].(map[string]any)
	if got := completedData["diagnosticRunId"]; got != runID {
		t.Fatalf("completed diagnosticRunId = %#v, want %q", got, runID)
	}
	if got := completedData["overallStatus"]; got != "healthy" {
		t.Fatalf("overallStatus = %#v, want healthy", got)
	}
}

func TestHandleRenameSessionReturnsUpdatedSession(t *testing.T) {
	agent := &fakeAgent{
		name: "claudecode",
		renameResult: &core.AgentSessionInfo{
			ID:           "ses_rename",
			Summary:      "新的标题",
			MessageCount: 3,
			ModifiedAt:   time.Unix(1710000600, 0).UTC(),
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "rename_session",
		RequestID: "rename-1",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_rename",
			"title":     "新的标题",
		}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	session, _ := data["session"].(map[string]any)
	if got := session["title"]; got != "新的标题" {
		t.Fatalf("session title = %#v, want 新的标题", got)
	}
}

func TestHandleArchiveSessionReturnsArchivedSession(t *testing.T) {
	archivedAt := time.Unix(1710000700, 0).UTC()
	agent := &fakeAgent{
		name: "claudecode",
		archiveResult: &core.AgentSessionInfo{
			ID:         "ses_archive",
			Summary:    "待归档",
			ModifiedAt: archivedAt,
			ArchivedAt: archivedAt,
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "archive_session",
		RequestID: "archive-1",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId":        "ses_archive",
			"archivedAtMillis": float64(archivedAt.UnixMilli()),
		}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	session, _ := data["session"].(map[string]any)
	if got := session["archivedAtMillis"]; got != float64(archivedAt.UnixMilli()) {
		t.Fatalf("archivedAtMillis = %#v, want %d", got, archivedAt.UnixMilli())
	}
}

func TestHandleGetSessionReturnsSingleSessionPayload(t *testing.T) {
	agent := &fakeAgent{
		name:            "claudecode",
		reasoningEffort: "ultra",
		sessionInfos: []core.AgentSessionInfo{{
			ID:           "ses_1",
			Summary:      "Renamed session",
			MessageCount: 7,
			ModifiedAt:   time.Unix(1710000500, 0).UTC(),
		}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "get_session",
		RequestID: "session-1",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_1"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	session, _ := data["session"].(map[string]any)
	if got := session["id"]; got != "ses_1" {
		t.Fatalf("session id = %#v, want ses_1", got)
	}
	if got := session["title"]; got != "Renamed session" {
		t.Fatalf("session title = %#v, want Renamed session", got)
	}
	if got := session["reasoningEffort"]; got != "ultra" {
		t.Fatalf("session reasoningEffort = %#v, want ultra", got)
	}
}

func TestClaudeListSessionsUsesRuntimeEffortWhenMetadataMissing(t *testing.T) {
	agent := &fakeAgent{
		name:            "claudecode",
		reasoningEffort: "ultra",
	}

	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-claude-project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "ses_1.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	catalog := newClaudeSessionCatalog(projectsDir)
	catalog.parseSession = func(string, time.Time) claudeSessionScanResult {
		return claudeSessionScanResult{
			Title:      "Historical Claude session",
			ModelID:    "glm-5.2",
			ProviderID: "default",
			CreatedAt:  time.Unix(1710000000, 0).UTC(),
			UpdatedAt:  time.Unix(1710000500, 0).UTC(),
		}
	}

	handlers := newTestHandlers(t)
	handlers.claudeSessions = catalog
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "list_sessions",
		RequestID: "sessions-1",
		Params:    mustJSONRaw(t, map[string]any{}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	sessionsRaw, _ := data["sessions"].([]any)
	if len(sessionsRaw) != 1 {
		t.Fatalf("sessions count = %d, want 1", len(sessionsRaw))
	}
	first, _ := sessionsRaw[0].(map[string]any)
	if got := first["reasoningEffort"]; got != "ultra" {
		t.Fatalf("list session reasoningEffort = %#v, want ultra", got)
	}
	if got := first["modelId"]; got != "glm-5.2" {
		t.Fatalf("list session modelId = %#v, want glm-5.2", got)
	}
}

// TestClaudeListSessionsDoesNotWriteTmpDump guards the production list_sessions
// hot path: it must not write /tmp/bridge-sessions.json (or any /tmp debug dump)
// on any request. The dump previously sat inside the wire_mapping_ms timing
// window and polluted the runtime-state-enrichment metric.
func TestClaudeListSessionsDoesNotWriteTmpDump(t *testing.T) {
	const tmpDump = "/tmp/bridge-sessions.json"
	os.Remove(tmpDump)

	agent := &fakeAgent{name: "claudecode", reasoningEffort: "high"}
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-claude-project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "ses_dump.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	catalog := newClaudeSessionCatalog(projectsDir)
	catalog.parseSession = func(string, time.Time) claudeSessionScanResult {
		return claudeSessionScanResult{
			Title:     "dump guard session",
			CreatedAt: time.Unix(1710000000, 0).UTC(),
			UpdatedAt: time.Unix(1710000500, 0).UTC(),
		}
	}

	handlers := newTestHandlers(t)
	handlers.claudeSessions = catalog
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	// Fire list_sessions repeatedly; the dump previously wrote on every call.
	for i := 0; i < 3; i++ {
		handlers.HandleRPC(serverConn, WireMessage{
			BackendID: "claudecode",
			Method:    "list_sessions",
			RequestID: fmt.Sprintf("dump-%d", i),
			Params:    mustJSONRaw(t, map[string]any{}),
		})
	}
	_ = readJSONMaps(t, clientConn, 3)

	if _, err := os.Stat(tmpDump); err == nil {
		t.Fatalf("list_sessions wrote debug dump %s; production hot path must not write /tmp files", tmpDump)
	}
}

func TestHandleGetSessionMessagesStoresLargeToolOutputAsContentRef(t *testing.T) {
	largeOutput := strings.Repeat("x", 600000)
	toolStep := map[string]any{
		"id":       "tool-large",
		"toolName": "Read",
		"status":   "completed",
		"output":   largeOutput,
	}
	agent := &fakeAgent{
		name: "claudecode",
		richHistory: []core.RichHistoryEntry{{
			ID:        "msg-large",
			Role:      "assistant",
			Content:   "完成",
			Timestamp: time.Unix(1710000800, 0).UTC(),
			Parts: []map[string]any{{
				"type": "tool",
				"step": toolStep,
			}},
			Steps: []map[string]any{toolStep},
		}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "get_session_messages",
		RequestID: "hist-large-1",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_large"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	entries, _ := data["messages"].([]any)
	entry, _ := entries[0].(map[string]any)
	steps, _ := entry["steps"].([]any)
	step, _ := steps[0].(map[string]any)
	output, _ := step["output"].(map[string]any)
	if got := output["kind"]; got != "content_ref" {
		t.Fatalf("output kind = %#v, want content_ref", got)
	}
	contentID, _ := output["contentId"].(string)
	if contentID == "" {
		t.Fatal("contentId missing from content_ref output")
	}
	parts, _ := entry["parts"].([]any)
	part, _ := parts[0].(map[string]any)
	partStep, _ := part["step"].(map[string]any)
	partOutput, _ := partStep["output"].(map[string]any)
	if got := partOutput["kind"]; got != "content_ref" {
		t.Fatalf("parts step output kind = %#v, want content_ref", got)
	}
	if got := partOutput["contentId"]; got != contentID {
		t.Fatalf("parts contentId = %#v, want %#v", got, contentID)
	}
	encoded, err := json.Marshal(messages[0])
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if len(encoded) >= 512<<10 {
		t.Fatalf("response size = %d, want below relay frame limit %d", len(encoded), 512<<10)
	}

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "fetch_content_chunk",
		RequestID: "chunk-1",
		Params: mustJSONRaw(t, map[string]any{
			"contentId": contentID,
			"offset":    0,
			"limit":     10,
		}),
	})

	chunkMessages := readJSONMaps(t, clientConn, 1)
	chunkData, _ := chunkMessages[0]["data"].(map[string]any)
	if got := chunkData["data"]; got != "xxxxxxxxxx" {
		t.Fatalf("chunk data = %#v, want first 10 chars", got)
	}
	if got := chunkData["complete"]; got != false {
		t.Fatalf("complete = %#v, want false", got)
	}
}

func TestHandleFetchTodosReturnsUnsupportedWhenProviderDeclines(t *testing.T) {
	agent := &fakeAgent{name: "codex", todosErr: core.ErrNotSupported}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "fetch_todos",
		RequestID: "todo-2",
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["ok"]; got != false {
		t.Fatalf("ok = %#v, want false", got)
	}
	errorPayload, _ := messages[0]["error"].(map[string]any)
	if got := errorPayload["code"]; got != "not_supported" {
		t.Fatalf("error code = %#v, want not_supported", got)
	}
}

func TestHandleListAgentsReturnsProviderData(t *testing.T) {
	agent := &fakeAgent{
		name: "codex",
		agents: []core.AgentDescriptor{{
			Name:        "planner",
			Mode:        "primary",
			Description: "Planning agent",
		}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "list_agents",
		RequestID: "agents-1",
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	agents, _ := data["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("agent count = %d, want 1", len(agents))
	}
	agentPayload, _ := agents[0].(map[string]any)
	if got := agentPayload["name"]; got != "planner" {
		t.Fatalf("name = %#v, want planner", got)
	}
}

func TestHandleListAgentsReturnsUnsupportedWhenProviderDeclines(t *testing.T) {
	agent := &fakeAgent{name: "codex", agentsErr: core.ErrNotSupported}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "list_agents",
		RequestID: "agents-2",
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["ok"]; got != false {
		t.Fatalf("ok = %#v, want false", got)
	}
	errorPayload, _ := messages[0]["error"].(map[string]any)
	if got := errorPayload["code"]; got != "not_supported" {
		t.Fatalf("error code = %#v, want not_supported", got)
	}
}

func TestOpenCodeGetSessionMessagesUsesAgentRichHistoryProvider(t *testing.T) {
	var proxyHistoryCalls int
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session/ses_1/message" {
			proxyHistoryCalls++
		}
		http.NotFound(w, r)
	}))
	defer proxyServer.Close()

	agent := &fakeAgent{
		name: "opencode",
		richHistory: []core.RichHistoryEntry{{
			ID:        "msg-1",
			Role:      "assistant",
			Content:   "bridge rich payload",
			Thinking:  "reasoning",
			Timestamp: time.Unix(1710000200, 0).UTC(),
			Parts: []map[string]any{{
				"type":    "text",
				"content": "bridge rich payload",
			}},
			Steps: []map[string]any{{
				"toolName": "bash",
				"status":   "completed",
			}},
		}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", agent)
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxyServer.URL, "", ""))
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "get_session_messages",
		RequestID: "oc-hist-1",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_1"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	entries, _ := data["messages"].([]any)
	if len(entries) != 1 {
		t.Fatalf("message count = %d, want 1", len(entries))
	}
	entry, _ := entries[0].(map[string]any)
	if got := entry["thinking"]; got != "reasoning" {
		t.Fatalf("thinking = %#v, want reasoning", got)
	}
	if proxyHistoryCalls != 0 {
		t.Fatalf("proxy history calls = %d, want 0", proxyHistoryCalls)
	}
}

func TestCodexGetSessionMessagesDoesNotResumeSession(t *testing.T) {
	agent := &fakeAgent{
		name: "codex",
		richHistory: []core.RichHistoryEntry{{
			Role:      "assistant",
			Content:   "cached history",
			Timestamp: time.Unix(1710000200, 0).UTC(),
		}},
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "get_session_messages",
		RequestID: "codex-history-no-resume",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_1", "directory": "/tmp/project"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	if got := messages[0]["ok"]; got != true {
		t.Fatalf("ok = %#v, want true", got)
	}
	if len(agent.startCalls) != 0 {
		t.Fatalf("StartSession calls = %v, want none for history read", agent.startCalls)
	}
}

func TestOpenCodeListAgentsUsesAgentProvider(t *testing.T) {
	var proxyAgentCalls int
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent" {
			proxyAgentCalls++
		}
		http.NotFound(w, r)
	}))
	defer proxyServer.Close()

	agent := &fakeAgent{
		name: "opencode",
		agents: []core.AgentDescriptor{{
			Name:        "planner",
			Mode:        "primary",
			Description: "Planning agent",
		}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", agent)
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxyServer.URL, "", ""))
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "list_agents",
		RequestID: "oc-agents-1",
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	agentsPayload, _ := data["agents"].([]any)
	if len(agentsPayload) != 1 {
		t.Fatalf("agent count = %d, want 1", len(agentsPayload))
	}
	if proxyAgentCalls != 0 {
		t.Fatalf("proxy agent calls = %d, want 0", proxyAgentCalls)
	}
}

func TestOpenCodeFetchTodosUsesAgentProvider(t *testing.T) {
	var proxyTodoCalls int
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session/ses_1/todo" {
			proxyTodoCalls++
		}
		http.NotFound(w, r)
	}))
	defer proxyServer.Close()

	agent := &fakeAgent{
		name:  "opencode",
		todos: []core.Todo{{Content: "bridge todo", Status: "pending", Priority: "normal"}},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", agent)
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxyServer.URL, "", ""))
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "fetch_todos",
		RequestID: "oc-todos-1",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_1"}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	todosPayload, _ := data["todos"].([]any)
	if len(todosPayload) != 1 {
		t.Fatalf("todo count = %d, want 1", len(todosPayload))
	}
	if proxyTodoCalls != 0 {
		t.Fatalf("proxy todo calls = %d, want 0", proxyTodoCalls)
	}
}

// ── Phase 5: compression capability + handler ────────────────────────────────

type compactableFakeSession struct {
	*fakeAgentSession
	compactCalls int
	compactErr   error
}

func (c *compactableFakeSession) CompactContext(ctx context.Context) error {
	c.compactCalls++
	return c.compactErr
}

func TestBackendListCompressionCapabilityOnlyForCodexAppServer(t *testing.T) {
	tests := []struct {
		name           string
		backendMode    string
		agentID        string
		wantCapability bool
	}{
		{"codex app_server", "app_server", "codex", true},
		{"codex exec", "exec", "codex", false},
		{"codex empty", "", "codex", false},
		{"claudecode ignored", "app_server", "claudecode", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlers := newTestHandlers(t)
			handlers.RegisterAgent(tt.agentID, &fakeAgent{name: tt.agentID})
			if tt.agentID == "codex" {
				handlers.SetCodexBackendMode(tt.backendMode)
			}

			backends := handlers.BackendList()
			if len(backends) != 1 {
				t.Fatalf("backend count = %d, want 1", len(backends))
			}
			found := false
			for _, cap := range backends[0].Capabilities {
				if cap == "compression" {
					found = true
				}
			}
			if found != tt.wantCapability {
				t.Fatalf("compression capability = %v, want %v", found, tt.wantCapability)
			}
		})
	}
}

func TestHandleCompressContextNotSupported(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex"})
	session := &fakeAgentSession{id: "ses_1", events: make(chan core.Event, 1)}
	handlers.mu.Lock()
	handlers.putSession("ses_1", session)
	handlers.mu.Unlock()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "compress_context",
		RequestID: "req-1",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_1"}),
	})

	msgs := readJSONMaps(t, clientConn, 1)
	if msgs[0]["error"] == nil {
		t.Fatal("expected error, got nil")
	}
	errObj := msgs[0]["error"].(map[string]any)
	if errObj["code"] != "not_supported" {
		t.Fatalf("error code = %q, want not_supported", errObj["code"])
	}
}

func TestHandleCompressContextSessionNotFound(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex"})
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "compress_context",
		RequestID: "req-2",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "nonexistent"}),
	})

	msgs := readJSONMaps(t, clientConn, 1)
	errObj := msgs[0]["error"].(map[string]any)
	if errObj["code"] != "session_not_found" {
		t.Fatalf("error code = %q, want session_not_found", errObj["code"])
	}
}

func TestHandleCompressContextAccepted(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex"})
	compactSession := &compactableFakeSession{
		fakeAgentSession: &fakeAgentSession{id: "ses_1", events: make(chan core.Event, 1)},
	}
	handlers.mu.Lock()
	handlers.putSession("ses_1", compactSession)
	handlers.mu.Unlock()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "compress_context",
		RequestID: "req-3",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_1"}),
	})

	msgs := readJSONMaps(t, clientConn, 1)
	data, _ := msgs[0]["data"].(map[string]any)
	if accepted, _ := data["accepted"].(bool); !accepted {
		t.Fatalf("data.accepted = %v, want true", data["accepted"])
	}
	if compactSession.compactCalls != 1 {
		t.Fatalf("compactCalls = %d, want 1", compactSession.compactCalls)
	}
}

func TestHandleCompressContextCompactError(t *testing.T) {
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex"})
	compactSession := &compactableFakeSession{
		fakeAgentSession: &fakeAgentSession{id: "ses_1", events: make(chan core.Event, 1)},
		compactErr:       fmt.Errorf("compact failed"),
	}
	handlers.mu.Lock()
	handlers.putSession("ses_1", compactSession)
	handlers.mu.Unlock()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "compress_context",
		RequestID: "req-4",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_1"}),
	})

	msgs := readJSONMaps(t, clientConn, 1)
	errObj := msgs[0]["error"].(map[string]any)
	if errObj["code"] != "compress_failed" {
		t.Fatalf("error code = %q, want compress_failed", errObj["code"])
	}
}

func TestBackendListAdvertisesDiagnosticsForOpenCodeAndCodex(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{
		name:             "opencode",
		diagnosticReport: &core.DiagnosticReport{},
	})
	handlers.RegisterAgent("codex", &fakeAgent{
		name:             "codex",
		diagnosticReport: &core.DiagnosticReport{},
	})

	backends := handlers.BackendList()
	if len(backends) != 2 {
		t.Fatalf("backend count = %d, want 2", len(backends))
	}

	for _, b := range backends {
		found := false
		for _, cap := range b.Capabilities {
			if cap == "diagnostics" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("backend %q missing diagnostics capability", b.ID)
		}
	}
}

func TestRunDiagnosticsReturnsResultsForOpenCode(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{
		name: "opencode",
		diagnosticReport: &core.DiagnosticReport{
			Results: []core.DiagnosticResult{
				{ID: "server", Status: "passed", Message: "OK"},
			},
			OverallStatus: "passed",
		},
	})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy("http://127.0.0.1:1", "", ""))

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "run_diagnostics",
		RequestID: "diag-1",
	})

	msgs := readJSONMaps(t, clientConn, 1)
	data, _ := msgs[0]["data"].(map[string]any)
	if data == nil {
		t.Fatal("expected data in response")
	}
	if _, ok := data["diagnosticRunId"]; !ok {
		t.Fatal("expected diagnosticRunId in data")
	}
}

func TestRunDiagnosticsReturnsNotSupportedWhenNoProvider(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &unsupportedMutationAgent{name: "opencode"})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy("http://127.0.0.1:1", "", ""))

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "opencode",
		Method:    "run_diagnostics",
		RequestID: "diag-no-provider",
	})

	msgs := readJSONMaps(t, clientConn, 1)
	errObj, _ := msgs[0]["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error in response, got: %v", msgs[0])
	}
	if errObj["code"] != "not_supported" {
		t.Fatalf("error code = %q, want not_supported", errObj["code"])
	}
}

// TestReadFileAcceptsSubdirectoryWithinWorkspace 验证 P0-1 review 观察：
// requestedDir 是授权 workspace 的子目录时应被接受（不误拒合法子目录调用），
// workspace 外的目录仍被拒绝。
func TestReadFileAcceptsSubdirectoryWithinWorkspace(t *testing.T) {
	workspace := t.TempDir()
	subDir := filepath.Join(workspace, "src")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	cases := []struct {
		name      string
		requested string
		wantErr   bool
	}{
		{"empty_dir_uses_root", "", false},
		{"root_exact", workspace, false},
		{"subdir_within_root", subDir, false},
		{"outside_workspace", outside, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, err := matchAuthorizedReadFileRoot(workspace, c.requested)
			if c.wantErr {
				if err == nil {
					t.Fatalf("requestedDir=%q 应被拒绝", c.requested)
				}
				return
			}
			if err != nil {
				t.Fatalf("requestedDir=%q 不应被拒绝: %v", c.requested, err)
			}
			// 返回的授权根始终是 workspace 根，而非子目录。
			wantRoot, _ := canonicalExistingDirectory(workspace)
			if root != wantRoot {
				t.Fatalf("授权根 = %q, want %q", root, wantRoot)
			}
		})
	}
}

func TestListDirectory(t *testing.T) {
	workspace := t.TempDir()

	// Create some dirs and files
	dir1 := filepath.Join(workspace, "dir1")
	dir2 := filepath.Join(workspace, "dir2")
	hiddenDir := filepath.Join(workspace, ".hidden_dir")
	file1 := filepath.Join(workspace, "file1.txt")

	if err := os.Mkdir(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(hiddenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file1, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newTestHandlers(t)
	conn := &readFileCaptureConn{}

	// Test listing the workspace
	params, _ := json.Marshal(map[string]interface{}{"path": workspace})
	h.handleListDirectory(conn, WireMessage{
		RequestID: "req_list",
		Params:    params,
	})

	if conn.err != nil {
		t.Fatalf("expected nil error, got %v", conn.err)
	}

	resMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", conn.data)
	}

	currentPath := resMap["currentPath"].(string)
	if currentPath != workspace {
		t.Errorf("expected currentPath %s, got %s", workspace, currentPath)
	}

	itemsRaw := resMap["items"]
	itemsJSON, _ := json.Marshal(itemsRaw)

	type directoryItem struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		IsDirectory bool   `json:"isDirectory"`
	}
	var items []directoryItem
	json.Unmarshal(itemsJSON, &items)

	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d: %#v", len(items), items)
	}

	hasDir1 := false
	hasDir2 := false
	hasFile1 := false

	for _, item := range items {
		if strings.HasPrefix(item.Name, ".") {
			t.Errorf("should not contain hidden item: %s", item.Name)
		}
		switch item.Name {
		case "dir1":
			hasDir1 = true
			if !item.IsDirectory {
				t.Error("dir1 should be a directory")
			}
		case "dir2":
			hasDir2 = true
			if !item.IsDirectory {
				t.Error("dir2 should be a directory")
			}
		case "file1.txt":
			hasFile1 = true
			if item.IsDirectory {
				t.Error("file1.txt should not be a directory")
			}
		}
	}

	if !hasDir1 || !hasDir2 || !hasFile1 {
		t.Errorf("missing expected items, got: %#v", items)
	}

	// Test expandPath helper
	homeDir, _ := os.UserHomeDir()
	res, err := expandPath("~")
	if err != nil {
		t.Fatal(err)
	}
	if res != homeDir {
		t.Errorf("expected ~ to resolve to %s, got %s", homeDir, res)
	}

	res2, err := expandPath("~/foo")
	if err != nil {
		t.Fatal(err)
	}
	if res2 != filepath.Join(homeDir, "foo") {
		t.Errorf("expected ~/foo to resolve to %s, got %s", filepath.Join(homeDir, "foo"), res2)
	}
}

func TestSessionRuntimeStateEnrichment(t *testing.T) {
	agent := &fakeAgent{
		name: "mockagent",
		sessionInfos: []core.AgentSessionInfo{{
			ID:           "ses_running",
			Summary:      "Running session",
			MessageCount: 1,
			ModifiedAt:   time.Unix(1710000500, 0).UTC(),
		}},
		runningSessionIDs: map[string]bool{"ses_running": true},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("mockagent", agent)
	handlers.sessions.markRunning("ses_running")

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	// 1. Test resume_session returns runtimeState
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "mockagent",
		Method:    "resume_session",
		RequestID: "req-resume",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_running", "directory": "/tmp"}),
	})
	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if got := data["runtimeState"]; got != "running" {
		t.Fatalf("resume_session runtimeState = %#v, want running", got)
	}

	// 2. Test get_session returns runtimeState
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "mockagent",
		Method:    "get_session",
		RequestID: "req-get",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_running"}),
	})
	messages = readJSONMaps(t, clientConn, 1)
	data, _ = messages[0]["data"].(map[string]any)
	session, _ := data["session"].(map[string]any)
	if got := session["runtimeState"]; got != "running" {
		t.Fatalf("get_session runtimeState = %#v, want running", got)
	}

	// 3. Test list_sessions returns runtimeState
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "mockagent",
		Method:    "list_sessions",
		RequestID: "req-list",
		Params:    mustJSONRaw(t, map[string]any{}),
	})
	messages = readJSONMaps(t, clientConn, 1)
	data, _ = messages[0]["data"].(map[string]any)
	sessionsRaw, _ := data["sessions"].([]any)
	if len(sessionsRaw) == 0 {
		t.Fatalf("expected at least one session")
	}
	firstSession, _ := sessionsRaw[0].(map[string]any)
	if got := firstSession["runtimeState"]; got != "running" {
		t.Fatalf("list_sessions runtimeState = %#v, want running", got)
	}

	// 4. Test GetRunningSessionIDs fallback detection (not in memory, but running in agent)
	agent.sessionInfos = append(agent.sessionInfos, core.AgentSessionInfo{
		ID:           "ses_external",
		Summary:      "External session",
		MessageCount: 1,
		ModifiedAt:   time.Unix(1710000500, 0).UTC(),
	})
	agent.runningSessionIDs = map[string]bool{"ses_external": true}

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "mockagent",
		Method:    "get_session",
		RequestID: "req-get-external",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": "ses_external"}),
	})
	messages = readJSONMaps(t, clientConn, 1)
	data, _ = messages[0]["data"].(map[string]any)
	session, _ = data["session"].(map[string]any)
	if got := session["runtimeState"]; got != "running" {
		t.Fatalf("get_session (external) runtimeState = %#v, want running", got)
	}
}
