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
	"sync"
	"sync/atomic"
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
	listHook           func() // optional; lets a test inject a panic on ListSessions
	listSessionsCalls  atomic.Int64
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
	processMu          sync.Mutex
	liveProcessCalls   int
	processAliveCalls  int
	lastProcessAliveID int
	// transcriptPath 让 fakeAgent 满足 core.TranscriptLocator（Codex file relay 需要）。
	transcriptPath string
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

// WireDescriptor (§6.2) makes fakeAgent self-describe by name, mirroring the real
// driver values in agent/<drv>/wire_descriptor.go. A fakeAgent stands in for a real
// driver, so after §6.2 it must self-describe the same A-class static capabilities
// (content_chunking/question_reply/external_turn_streaming for claude, external_turn_streaming
// for codex, todos 兜底 for opencode) instead of relying on the removed id-keyed checks.
// "claude" and "claudecode" map to the same descriptor (production alias); unknown names
// return nil → legacy fallback. Keep these values in sync with the driver files.
func (f *fakeAgent) WireDescriptor() *core.WireDescriptor {
	switch f.name {
	case "claude", "claudecode":
		return &core.WireDescriptor{
			Kind: "claude_code", DisplayName: "Claude Code",
			LiveEventModel: core.LiveEventSessionProcess, RequiresExternalTurnPolling: true,
			StaticCapabilities: []string{"content_chunking", "question_reply", "external_turn_streaming"},
		}
	case "codex":
		return &core.WireDescriptor{
			Kind: "codex", DisplayName: "Codex",
			LiveEventModel: core.LiveEventSessionProcess, RequiresExternalTurnPolling: false,
			StaticCapabilities: []string{"external_turn_streaming"},
		}
	case "opencode":
		return &core.WireDescriptor{
			Kind: "opencode", DisplayName: "OpenCode",
			LiveEventModel: core.LiveEventBroadcast, RequiresExternalTurnPolling: true,
			StaticCapabilities: []string{"todos"},
		}
	case "grokbuild":
		return &core.WireDescriptor{
			Kind: "grokbuild", DisplayName: "Grok Build",
			LiveEventModel: core.LiveEventSessionProcess, RequiresExternalTurnPolling: true,
		}
	default:
		return nil
	}
}

func (f *fakeAgent) GetRunningSessionIDs(ctx context.Context) (map[string]bool, error) {
	f.runningCalls++
	return f.runningSessionIDs, nil
}

func (f *fakeAgent) LiveSessionProcess(ctx context.Context, sessionID string) (core.LiveSessionProcess, error) {
	f.processMu.Lock()
	defer f.processMu.Unlock()
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

// LiveProcessCallCount / ProcessAliveStats expose the relay-inspection counters under processMu so
// tests can read them without racing the relay goroutine's writes (the fakeAgent is touched by the
// relay goroutine via IsProcessAlive/LiveSessionProcess and by the test goroutine).
func (f *fakeAgent) LiveProcessCallCount() int {
	f.processMu.Lock()
	defer f.processMu.Unlock()
	return f.liveProcessCalls
}
func (f *fakeAgent) ProcessAliveStats() (calls int, lastPID int) {
	f.processMu.Lock()
	defer f.processMu.Unlock()
	return f.processAliveCalls, f.lastProcessAliveID
}

func (f *fakeAgent) IsProcessAlive(ctx context.Context, pid int) bool {
	f.processMu.Lock()
	defer f.processMu.Unlock()
	f.processAliveCalls++
	f.lastProcessAliveID = pid
	if f.alivePIDs == nil {
		return false
	}
	return f.alivePIDs[pid]
}

// SetLiveProcess sets a liveProcesses entry under processMu so tests can mutate it without racing
// the relay goroutine's LiveSessionProcess reads.
func (f *fakeAgent) SetLiveProcess(sessionID string, proc core.LiveSessionProcess) {
	f.processMu.Lock()
	defer f.processMu.Unlock()
	if f.liveProcesses == nil {
		f.liveProcesses = map[string]core.LiveSessionProcess{}
	}
	f.liveProcesses[sessionID] = proc
}

// TranscriptPath 让 fakeAgent 满足 core.TranscriptLocator（Codex file relay 启动需要）。
func (f *fakeAgent) TranscriptPath(ctx context.Context, sessionID string) (string, error) {
	return f.transcriptPath, nil
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
	f.listSessionsCalls.Add(1)
	if f.listHook != nil {
		f.listHook()
	}
	if f.sessionListErr != nil {
		return nil, f.sessionListErr
	}
	return append([]core.AgentSessionInfo(nil), f.sessionInfos...), nil
}

func (f *fakeAgent) ListSessionsCallCount() int64 { return f.listSessionsCalls.Load() }

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
	// done 由 SendResult 关闭一次，供 async 路径（read_file_v2 经 file pool）等待结果。
	// nil（裸字面量构造）时 SendResult 不触碰它，保持 legacy 同步测试行为。
	done chan struct{}
	once sync.Once
}

// newReadFileCaptureConn 构造带 done 信号的 conn，用于 read_file_v2 异步路径测试。
func newReadFileCaptureConn() *readFileCaptureConn {
	return &readFileCaptureConn{done: make(chan struct{})}
}

// waitForResult 阻塞直到 SendResult 被调用一次或超时。
func (c *readFileCaptureConn) waitForResult(t *testing.T) {
	t.Helper()
	if c.done == nil {
		return // 裸字面量（同步路径），无需等待
	}
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		t.Fatal("readFileCaptureConn: SendResult 未在 2s 内调用")
	}
}

func (c *readFileCaptureConn) SendJSON(any) {}
func (c *readFileCaptureConn) SendResult(_ string, data interface{}, err *WireError) {
	c.data = data
	c.err = err
	c.once.Do(func() {
		if c.done != nil {
			close(c.done)
		}
	})
}
func (c *readFileCaptureConn) SendEvent(string, string, string, interface{}) {}
func (c *readFileCaptureConn) AuthedDevice() *TrustedDeviceRecord            { return nil }
func (c *readFileCaptureConn) RemoteAddr() string                            { return "test:read-file" }
func (c *readFileCaptureConn) Close() error                                  { return nil }

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
	for _, want := range []string{`msg="opencode list_sessions"`, "directory=project", "limit=2", "result_count=2"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("diagnostic log missing %q in %s", want, logText)
		}
	}
	// Phase 7 §444：catalog 日志的 directory 已脱敏为 basename（project）。绝对路径 /tmp/project
	// 不得出现在日志中——此负向断言锁定脱敏契约，防止回归到直接打 workDir/cwd。
	if strings.Contains(logText, "/tmp/project") {
		t.Fatalf("diagnostic log leaks absolute directory path (§444 redaction): %s", logText)
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

func TestHandleGetSessionMessagesBoundsPaginatedRichHistoryFallback(t *testing.T) {
	large := strings.Repeat("x", 180<<10)
	agent := &fakeAgent{
		name: "claude",
		richHistory: []core.RichHistoryEntry{
			{ID: "old", Role: "assistant", Content: large, Timestamp: time.Unix(1, 0).UTC()},
			{ID: "new", Role: "assistant", Content: large, Timestamp: time.Unix(2, 0).UTC()},
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claude", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claude",
		Method:    "get_session_messages",
		RequestID: "hist-bounded",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_large",
			"limit":     50,
			"paginate":  true,
		}),
	})

	response := readJSONMaps(t, clientConn, 1)[0]
	data, _ := response["data"].(map[string]any)
	entries, _ := data["messages"].([]any)
	if len(entries) != 1 {
		t.Fatalf("message count = %d, want newest message only", len(entries))
	}
	entry, _ := entries[0].(map[string]any)
	if got := entry["id"]; got != "new" {
		t.Fatalf("message id = %#v, want new", got)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxPageResponseBytes {
		t.Fatalf("fallback page encoded %d bytes > budget %d", len(encoded), maxPageResponseBytes)
	}
}

func TestHandleGetSessionMessagesWholeHistoryPreservesRowsAndTruncatesOversizedMessage(t *testing.T) {
	agent := &fakeAgent{
		name: "codex",
		richHistory: []core.RichHistoryEntry{
			{ID: "old", Role: "user", Content: "keep old", Timestamp: time.Unix(1, 0).UTC()},
			{ID: "huge", Role: "assistant", Content: strings.Repeat("x", 1<<20), Timestamp: time.Unix(2, 0).UTC()},
			{ID: "new", Role: "assistant", Content: "keep new", Timestamp: time.Unix(3, 0).UTC()},
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex",
		Method:    "get_session_messages",
		RequestID: "hist-whole",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "ses_whole",
			"limit":     0,
		}),
	})

	response := readJSONMaps(t, clientConn, 1)[0]
	data, _ := response["data"].(map[string]any)
	entries, _ := data["messages"].([]any)
	if len(entries) != 3 {
		t.Fatalf("message count = %d, want complete 3-row history", len(entries))
	}
	if entries[0].(map[string]any)["id"] != "old" || entries[2].(map[string]any)["id"] != "new" {
		t.Fatalf("whole-history order changed: %#v", entries)
	}
	huge := entries[1].(map[string]any)
	if marked, _ := huge["truncated"].(bool); !marked {
		t.Fatalf("oversized whole-history message missing truncated marker: %#v", huge)
	}
	encoded, err := json.Marshal(huge)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxPageResponseBytes {
		t.Fatalf("oversized whole-history message encoded %d bytes > budget %d", len(encoded), maxPageResponseBytes)
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

// TestModelProviderForAgentUsesActiveProviderForGrokbuild 锁死 §5.1 缺口 1（C6 后半）：
// grokbuild 实现 ProviderSwitcher 后，无前缀模型（如 grok-4.5）在有 active provider 时
// 标到 active name，而非回落 "default"（修复前 grokbuild 不实现 ProviderSwitcher，100% 走 default）。
func TestModelProviderForAgentUsesActiveProviderForGrokbuild(t *testing.T) {
	agent := &fakeAgent{
		name:           "grokbuild",
		providers:      []core.ProviderConfig{{Name: "glm"}},
		activeProvider: "glm",
	}

	id, provider, providerID := modelProviderForAgent(agent, "grok-4.5")

	if id != "grok-4.5" || provider != "glm" || providerID != "glm" {
		t.Fatalf("model provider = (%q, %q, %q), want (grok-4.5, glm, glm)", id, provider, providerID)
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

func TestClaudeSendMessageKeepsFileRelayAlongsideAgentRelay(t *testing.T) {
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
	// 留下 transcript file relay 标记（kind=claude_file）。send_message 必须启动真实
	// AgentSession stdout relay，但**不能**把 file relay supersede 掉——file relay 是
	// 本地 turn 唯一的 UUID-keyed 内容来源（agent relay 事件缺 itemId，会被 reducer 跳过）。
	// 因此两者应并行：agent relay 作 sidecar 投递控制面/带 itemId 事件，file relay 保留为内容来源。
	const sessionID = "b36c6286-2222-4eec-b542-8cdc8a382573"
	handlers.mu.Lock()
	handlers.relayRunning[sessionID] = true
	handlers.relayRunningKind[sessionID] = relayKindClaudeFile
	handlers.mu.Unlock()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "send_message",
		RequestID: "send-keeps-file-relay",
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
		t.Fatalf("agent relay 未投递事件：sawText=%v sawCompleted=%v messages=%v", sawText, sawCompleted, messages)
	}
	// 核心断言：file relay (kind=claude_file) 没有被 send_message supersede，agent relay 作为
	// sidecar 并行运行（agentRelayRunning=true）。
	handlers.mu.Lock()
	fileRelayKept := handlers.relayRunningKind[sessionID] == relayKindClaudeFile
	agentRelaySidecar := handlers.agentRelayRunning[sessionID]
	handlers.mu.Unlock()
	if !fileRelayKept {
		t.Fatalf("send_message 把 file relay superseded 了：relayRunningKind=%q，应保持 claude_file", handlers.relayRunningKind[sessionID])
	}
	if !agentRelaySidecar {
		t.Fatalf("send_message 未启动 agent relay sidecar：agentRelayRunning=false")
	}
}

// readEventOrTimeout reads one JSON event from the websocket; returns nil on timeout or any read
// error so callers can drain non-fatally within a deadline.
func readEventOrTimeout(t *testing.T, c *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil
	}
	var m map[string]any
	if err := c.ReadJSON(&m); err != nil {
		return nil
	}
	return m
}

// TestClaudeFileRelayAndAgentRelayRunConcurrentlyRaceFree closes the evidence gap flagged in the
// Issue 3 review: "data-race-free is a lock-design argument, not a concurrent proof." It runs the
// REAL claudeSessionFileRelayLoop (file relay) AND the REAL relayEvents (agent relay sidecar)
// concurrently on the SAME session — both touching the shared relayRunning / relayRunningKind /
// agentRelayRunning maps and the projection reducer. Run under `go test -race` it proves there is
// no data race; the assertions prove the file relay is never superseded (kind stays claude_file)
// and both relays deliver their events while overlapping.
func TestClaudeFileRelayAndAgentRelayRunConcurrentlyRaceFree(t *testing.T) {
	withFastClaudeFileRelay(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	const sessionID = "concurrent-both-relay"
	path := writeClaudeFileRelayTranscript(t, home, sessionID,
		`{"type":"user","uuid":"both-user","message":{"role":"user","content":"local turn"}}`)

	agent := &fakeAgent{
		name:              "claudecode",
		generateSessionID: true,
		liveProcesses: map[string]core.LiveSessionProcess{
			sessionID: {SessionID: sessionID, PID: 4242, Live: true},
		},
		alivePIDs: map[int]bool{4242: true},
		// Agent-relay sidecar events. EventText carries no itemId (driver path) — its delta is
		// distinctive from file-relay text (which carries the transcript UUID as itemId).
		sendHook: func(sess *fakeAgentSession, _ string) {
			sess.events <- core.Event{Type: core.EventText, SessionID: sess.id, Content: "agent partial"}
			sess.events <- core.Event{Type: core.EventResult, SessionID: sess.id, Done: true}
		},
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", agent)
	// This test models a MacBridge-owned session with both its stdout relay and the transcript
	// watcher active. Install the real session object up front so send_message does not enter the
	// external-owner resume preflight (a live external-only stub is now intentionally rejected).
	ownedSession, err := agent.StartSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	handlers.mu.Lock()
	handlers.putSession(sessionID, ownedSession)
	handlers.mu.Unlock()
	serverConn, clientConn, cleanup := openTestConn(t)
	t.Cleanup(cleanup)
	handlers.broadcaster.Subscribe(serverConn, SubscriptionKey{BackendID: "claudecode", SessionID: sessionID})

	// 1. Start the FILE relay → claims the global slot with kind=claude_file.
	handlers.startClaudeSessionFileRelay(sessionID, serverConn, "claudecode")

	// Drain the file relay's warm-start turn_started for both-user.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m := readEventOrTimeout(t, clientConn, 300*time.Millisecond); m != nil && m["event"] == "turn_started" {
			break
		}
	}

	// 2. Local turn → AGENT relay starts as sidecar. Must NOT supersede the file relay.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "send_message",
		RequestID: "send-concurrent",
		Params:    mustJSONRaw(t, map[string]any{"sessionId": sessionID, "content": "hi"}),
	})

	handlers.mu.Lock()
	kindAfterSend := handlers.relayRunningKind[sessionID]
	handlers.mu.Unlock()
	if kindAfterSend != relayKindClaudeFile {
		t.Fatalf("send_message superseded file relay: kind=%q, want claude_file", kindAfterSend)
	}

	// 3. File relay concurrently emits its assistant completion for the same session/reducer.
	appendClaudeFileRelayTranscript(t, path,
		`{"type":"assistant","uuid":"both-asst","message":{"id":"msg_both","role":"assistant","content":[{"type":"text","text":"file done"}],"stop_reason":"end_turn"}}`)

	// 4. Overlap window: collect events until output from BOTH relays is observed.
	deadline = time.Now().Add(2 * time.Second)
	var sawAgentText, sawFileText bool
	for time.Now().Before(deadline) && !(sawAgentText && sawFileText) {
		m := readEventOrTimeout(t, clientConn, 300*time.Millisecond)
		if m == nil {
			continue
		}
		if m["event"] == "text_delta" {
			if d, ok := m["data"].(map[string]any); ok {
				switch d["delta"] {
				case "agent partial":
					sawAgentText = true
				case "file done":
					sawFileText = true
				}
			}
		}
	}
	if !sawAgentText {
		t.Fatal("agent relay sidecar did not deliver its event concurrently with the file relay")
	}
	if !sawFileText {
		t.Fatal("file relay did not deliver its text concurrently with the agent relay")
	}

	handlers.mu.Lock()
	kindEnd := handlers.relayRunningKind[sessionID]
	agentSidecar := handlers.agentRelayRunning[sessionID]
	handlers.mu.Unlock()
	if kindEnd != relayKindClaudeFile {
		t.Fatalf("file relay kind changed during overlap: %q, want claude_file", kindEnd)
	}
	if !agentSidecar {
		t.Fatal("agent relay sidecar not running alongside file relay")
	}

	// Cleanly terminate the agent relay so it exits before teardown (its events channel stays
	// open otherwise); closing it makes relayEvents hit the channel-closed path and return.
	if n := len(agent.sessions); n > 0 {
		_ = agent.sessions[n-1].Close()
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
	ws := catalogFixtureWorkspace(t, projectsDir, "claude-project")
	projectDir := filepath.Join(projectsDir, "-tmp-claude-project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "ses_1.jsonl")
	// cwd must be a real non-temp workspace so claudeWorkspaceVisibleForCatalog keeps it.
	if err := os.WriteFile(sessionPath, []byte(`{"cwd":"`+ws+`"}`+"\n"), 0644); err != nil {
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
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

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
	ws := catalogFixtureWorkspace(t, projectsDir, "claude-dump")
	projectDir := filepath.Join(projectsDir, "-tmp-claude-project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "ses_dump.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"cwd":"`+ws+`"}`+"\n"), 0644); err != nil {
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
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

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

// TestAuthorizedReadFileRootAcceptsSubdirectoryWithinWorkspace 验证授权根解析：
// requestedDir 是授权 workspace 的子目录时应被接受（不误拒合法子目录调用），
// workspace 外的目录仍被拒绝。
func TestAuthorizedReadFileRootAcceptsSubdirectoryWithinWorkspace(t *testing.T) {
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

func TestClaudeResumeSessionUsesRegistryStateWithoutTranscriptScan(t *testing.T) {
	agent := &fakeAgent{
		name:              "claudecode",
		runningSessionIDs: map[string]bool{"large-session": true},
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claude", agent)
	handlers.sessions.markRunning("large-session")

	probeCalls := 0
	handlers.transcriptStateProbe = func() { probeCalls++ }

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claude",
		Method:    "resume_session",
		RequestID: "req-large-resume",
		Params: mustJSONRaw(t, map[string]any{
			"sessionId": "large-session",
			"directory": "/tmp",
		}),
	})

	messages := readJSONMaps(t, clientConn, 1)
	data, _ := messages[0]["data"].(map[string]any)
	if got := data["runtimeState"]; got != "running" {
		t.Fatalf("resume_session runtimeState = %#v, want registry running", got)
	}
	if agent.runningCalls != 0 {
		t.Fatalf("resume_session called GetRunningSessionIDs %d time(s), want 0", agent.runningCalls)
	}
	if probeCalls != 0 {
		t.Fatalf("resume_session scanned transcript %d time(s), want 0", probeCalls)
	}
}

func TestShouldListClaudeProjectsAllowlist(t *testing.T) {
	t.Parallel()
	if shouldListClaudeProjects(nil) {
		t.Fatal("nil agent must not scan Claude projects")
	}
	if !shouldListClaudeProjects(&fakeAgent{name: "claudecode"}) {
		t.Fatal("claudecode must scan Claude projects")
	}
	if !shouldListClaudeProjects(&fakeAgent{name: "claude"}) {
		t.Fatal("claude alias must scan Claude projects")
	}
	for _, name := range []string{"codex", "grokbuild", "opencode"} {
		if shouldListClaudeProjects(&fakeAgent{name: name}) {
			t.Fatalf("%s must not scan Claude projects", name)
		}
	}
}

func TestHandleListProjectsCodexReturnsEmptyEvenIfClaudeProjectsExist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeProjects := filepath.Join(home, ".claude", "projects", "-Users-jacklee-Projects-fake")
	if err := os.MkdirAll(claudeProjects, 0o755); err != nil {
		t.Fatal(err)
	}

	handlers := newTestHandlers(t)
	conn := &readFileCaptureConn{}
	handlers.handleListProjects(conn, WireMessage{RequestID: "req-codex-projects"}, &fakeAgent{name: "codex"})

	if conn.err != nil {
		t.Fatalf("unexpected wire error: %+v", conn.err)
	}
	data, _ := conn.data.(map[string]interface{})
	projects, _ := data["projects"].([]interface{})
	if len(projects) != 0 {
		t.Fatalf("codex list_projects = %#v, want empty (must not inherit ~/.claude/projects)", projects)
	}
}

// TestHandleListProjectsClaudeSkipsEmptyShells：Claude list_projects 不得返回无 .jsonl 的
// 空 project 壳（owner 2026-08-10：iOS 侧栏出现一堆「暂无会话」幽灵目录）。
func TestHandleListProjectsClaudeSkipsEmptyShells(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectsRoot := filepath.Join(home, ".claude", "projects")
	emptyDir := filepath.Join(projectsRoot, "-Users-jacklee-Projects-empty-shell")
	liveDir := filepath.Join(projectsRoot, "-Users-jacklee-Projects-live")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Real workspace on disk (visibility filter requires existing non-temp cwd).
	liveWS := filepath.Join(home, "Projects", "live")
	if err := os.MkdirAll(liveWS, 0o755); err != nil {
		t.Fatal(err)
	}
	// Live project: one transcript with cwd so resolveProjectRealDirectory works.
	liveJSONL := filepath.Join(liveDir, "sess-live.jsonl")
	if err := os.WriteFile(liveJSONL, []byte(`{"cwd":"`+liveWS+`","type":"session_meta"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	handlers := newTestHandlers(t)
	conn := &readFileCaptureConn{}
	handlers.handleListProjects(conn, WireMessage{RequestID: "req-claude-projects"}, &fakeAgent{name: "claudecode"})

	if conn.err != nil {
		t.Fatalf("unexpected wire error: %+v", conn.err)
	}
	data, _ := conn.data.(map[string]interface{})
	// handleListProjects sends []map[string]interface{}, not []interface{}.
	projects, ok := data["projects"].([]map[string]interface{})
	if !ok {
		t.Fatalf("projects type = %T (%#v), want []map[string]interface{}", data["projects"], data["projects"])
	}
	if len(projects) != 1 {
		t.Fatalf("claude list_projects count = %d (%#v), want 1 (empty shell filtered)", len(projects), projects)
	}
	if projects[0]["directory"] != liveWS {
		t.Fatalf("directory = %#v, want %s", projects[0]["directory"], liveWS)
	}
	if projects[0]["name"] != "live" {
		t.Fatalf("name = %#v, want live", projects[0]["name"])
	}
}

// TestClaudeWorkspaceVisibleForCatalog_HidesTempAndWorktrees：Desktop 不显示的
// /private/tmp 抓取目录与 .claude/worktrees 不得进入 public catalog。
func TestClaudeWorkspaceVisibleForCatalog_HidesTempAndWorktrees(t *testing.T) {
	// Real existing non-temp path (control).
	okDir := t.TempDir()
	if !claudeWorkspaceVisibleForCatalog(okDir) {
		t.Fatalf("visible fixture dir %q should pass", okDir)
	}
	// System temp.
	if claudeWorkspaceVisibleForCatalog("/private/tmp/claude_aq_capture") {
		t.Fatal("/private/tmp/... must be hidden")
	}
	if claudeWorkspaceVisibleForCatalog("/tmp/scratch") {
		t.Fatal("/tmp/... must be hidden")
	}
	// Worktree path shape (existence irrelevant — shape alone hides).
	wt := filepath.Join(okDir, ".claude", "worktrees", "quirky-blackburn-3f30d4")
	if claudeWorkspaceVisibleForCatalog(wt) {
		t.Fatalf("worktree path %q must be hidden", wt)
	}
	// Missing absolute path.
	missing := filepath.Join(okDir, "does-not-exist-workspace")
	if claudeWorkspaceVisibleForCatalog(missing) {
		t.Fatalf("missing path %q must be hidden", missing)
	}
	// Encoded project key fallback.
	if claudeWorkspaceVisibleForCatalog("-Users-jacklee-Projects-foo") {
		t.Fatal("encoded project key must be hidden")
	}
}

// ── §6.5: list_directory workspace-bound + symlink + pagination + depth ────────────────

func TestListDirectoryWorkspaceBound_RejectsTraversal(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	subDir := filepath.Join(workspace, "src")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspaceFile := filepath.Join(workspace, "ok.txt")
	if err := os.WriteFile(workspaceFile, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := newTestHandlers(t)

	tests := []struct {
		name          string
		path          string
		workspaceRoot string
		wantCode      string
	}{
		{
			name:          "absolute outside workspace",
			path:          outside,
			workspaceRoot: workspace,
			wantCode:      "file.outside_authorized_root",
		},
		{
			name:          "traversal relative",
			path:          filepath.Join(workspace, "..", filepath.Base(outside)),
			workspaceRoot: workspace,
			wantCode:      "file.outside_authorized_root", // canonicalExistingDirectory resolves '..' → outside; pathIsWithinRoot catches it
		},
		{
			name:          "valid subdirectory within workspace",
			path:          subDir,
			workspaceRoot: workspace,
			wantCode:      "", // success
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &readFileCaptureConn{}
			h.handleListDirectory(conn, WireMessage{
				RequestID: "req_" + tt.name,
				Params: mustJSONRaw(t, map[string]any{
					"path":           tt.path,
					"workspace_root": tt.workspaceRoot,
				}),
			})
			if tt.wantCode == "" {
				if conn.err != nil {
					t.Fatalf("expected success, got error: %+v", conn.err)
				}
				return
			}
			if conn.err == nil || conn.err.Code != tt.wantCode {
				t.Fatalf("error = %#v, want code %q", conn.err, tt.wantCode)
			}
		})
	}

	// 允许：workspace 内的子目录。
	conn := &readFileCaptureConn{}
	h.handleListDirectory(conn, WireMessage{
		RequestID: "req_allowed_subdir",
		Params: mustJSONRaw(t, map[string]any{
			"path":           subDir,
			"workspace_root": workspace,
		}),
	})
	if conn.err != nil {
		t.Fatalf("allowed subdir should succeed, got error: %+v", conn.err)
	}
}

func TestListDirectoryWorkspaceBound_SymlinkLeaf(t *testing.T) {
	workspace := t.TempDir()
	subDir := filepath.Join(workspace, "src")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nestedFile := filepath.Join(subDir, "util.go")
	if err := os.WriteFile(nestedFile, []byte("package util"), 0o600); err != nil {
		t.Fatal(err)
	}
	// symlink 在 workspace 内，指向子目录
	linkPath := filepath.Join(workspace, "src-link")
	if err := os.Symlink(subDir, linkPath); err != nil {
		t.Fatal(err)
	}

	h := newTestHandlers(t)
	conn := &readFileCaptureConn{}
	h.handleListDirectory(conn, WireMessage{
		RequestID: "req_symlink_leaf",
		Params: mustJSONRaw(t, map[string]any{
			"path":           workspace,
			"workspace_root": workspace,
			"depth":          2,
		}),
	})
	if conn.err != nil {
		t.Fatalf("expected nil error, got %+v", conn.err)
	}
	resMap, _ := conn.data.(map[string]interface{})
	itemsRaw, _ := json.Marshal(resMap["items"])

	type item struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		IsDirectory bool   `json:"isDirectory"`
		IsSymlink   bool   `json:"isSymlink,omitempty"`
	}
	var items []item
	json.Unmarshal(itemsRaw, &items)

	if len(items) < 2 {
		t.Fatalf("expected >=2 items (src + src-link + files under src), got %d: %v", len(items), items)
	}

	// 找 src-link（symlink 叶节点，不应有子条目）。
	hasLink := false
	hasUtilGo := false
	for _, it := range items {
		if it.Name == "src-link" {
			hasLink = true
			if !it.IsSymlink {
				t.Error("symlink entry should have isSymlink=true")
			}
		}
		if it.Name == "util.go" {
			hasUtilGo = true
		}
		// symlink 不应有子条目跟随——但 symlink 是 leaf，children 只出现在 real dir 下。
	}
	if !hasLink {
		t.Error("missing symlink entry 'src-link'")
	}
	if !hasUtilGo {
		t.Error("missing nested file 'util.go' under real dir 'src'")
	}
}

func TestListDirectoryWorkspaceBound_SymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "leak.txt")
	if err := os.WriteFile(outsideFile, []byte("leaked"), 0o600); err != nil {
		t.Fatal(err)
	}
	// symlink 在 workspace 内，指向 workspace 外的目录
	escLink := filepath.Join(workspace, "escape-link")
	if err := os.Symlink(outside, escLink); err != nil {
		t.Fatal(err)
	}

	h := newTestHandlers(t)

	// workspace-bound 模式：列出 workspace，symlink 应作为叶节点出现（不跟随，不暴露逃逸）。
	conn := &readFileCaptureConn{}
	h.handleListDirectory(conn, WireMessage{
		RequestID: "req_escape_leaf",
		Params: mustJSONRaw(t, map[string]any{
			"path":           workspace,
			"workspace_root": workspace,
			"depth":          2,
		}),
	})
	if conn.err != nil {
		t.Fatalf("expected nil error, got %+v", conn.err)
	}
	resMap, _ := conn.data.(map[string]interface{})
	itemsRaw, _ := json.Marshal(resMap["items"])

	type item struct {
		Name      string `json:"name"`
		IsSymlink bool   `json:"isSymlink,omitempty"`
	}
	var items []item
	json.Unmarshal(itemsRaw, &items)

	for _, it := range items {
		if it.Name == "leak.txt" {
			t.Fatalf("symlink escape: leaked file from outside dir should NOT appear in listing")
		}
		if it.Name == "escape-link" && !it.IsSymlink {
			t.Error("escape-link should be marked as symlink")
		}
	}
}

func TestListDirectory_Pagination(t *testing.T) {
	workspace := t.TempDir()
	// 创建 10 个文件用于分页
	for i := 0; i < 10; i++ {
		fname := fmt.Sprintf("file-%02d.txt", i)
		if err := os.WriteFile(filepath.Join(workspace, fname), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := newTestHandlers(t)

	// 第 1 页：limit=4, offset=0
	conn := &readFileCaptureConn{}
	h.handleListDirectory(conn, WireMessage{
		RequestID: "req_page1",
		Params: mustJSONRaw(t, map[string]any{
			"path":  workspace,
			"limit": 4,
		}),
	})
	if conn.err != nil {
		t.Fatalf("unexpected error: %+v", conn.err)
	}
	resMap := conn.data.(map[string]interface{})
	page1JSON, _ := json.Marshal(resMap["items"])
	type item struct{ Name string }
	var page1 []item
	json.Unmarshal(page1JSON, &page1)
	if len(page1) != 4 {
		t.Fatalf("page1: expected 4 items, got %d", len(page1))
	}
	if resMap["hasMore"] != true {
		t.Error("expected hasMore=true after first page of 4/10")
	}

	// 第 2 页：limit=4, offset=4
	conn = &readFileCaptureConn{}
	h.handleListDirectory(conn, WireMessage{
		RequestID: "req_page2",
		Params: mustJSONRaw(t, map[string]any{
			"path":   workspace,
			"limit":  4,
			"offset": 4,
		}),
	})
	resMap = conn.data.(map[string]interface{})
	page2JSON, _ := json.Marshal(resMap["items"])
	var page2 []item
	json.Unmarshal(page2JSON, &page2)
	if len(page2) != 4 {
		t.Fatalf("page2: expected 4 items, got %d", len(page2))
	}
	if resMap["hasMore"] != true {
		t.Error("expected hasMore=true after 8/10")
	}

	// 第 3 页（最后一页）：limit=4, offset=8 → 2 items, hasMore=false
	conn = &readFileCaptureConn{}
	h.handleListDirectory(conn, WireMessage{
		RequestID: "req_page3",
		Params: mustJSONRaw(t, map[string]any{
			"path":   workspace,
			"limit":  4,
			"offset": 8,
		}),
	})
	resMap = conn.data.(map[string]interface{})
	page3JSON, _ := json.Marshal(resMap["items"])
	var page3 []item
	json.Unmarshal(page3JSON, &page3)
	if len(page3) != 2 {
		t.Fatalf("page3: expected 2 items (last page), got %d", len(page3))
	}
	if resMap["hasMore"] != false {
		t.Error("expected hasMore=false on last page (10/10)")
	}

	// 前两页不应重叠。
	page1Names := make(map[string]bool)
	for _, it := range page1 {
		page1Names[it.Name] = true
	}
	for _, it := range page2 {
		if page1Names[it.Name] {
			t.Errorf("page2 and page1 overlap on %s", it.Name)
		}
	}
}

func TestListDirectory_DepthRecursion(t *testing.T) {
	workspace := t.TempDir()
	sub := filepath.Join(workspace, "src")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(sub, "internal")
	if err := os.Mkdir(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "config.go"), []byte("package config"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "app.go"), []byte("package main"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := newTestHandlers(t)

	// depth=1：只看到 workspace 的直接子条目（src + main.go），没有孙子条目。
	conn1 := &readFileCaptureConn{}
	h.handleListDirectory(conn1, WireMessage{
		RequestID: "req_depth1",
		Params: mustJSONRaw(t, map[string]any{
			"path":           workspace,
			"workspace_root": workspace,
			"depth":          1,
		}),
	})
	resMap := conn1.data.(map[string]interface{})
	itemsJSON, _ := json.Marshal(resMap["items"])
	type item struct {
		Name        string `json:"name"`
		IsDirectory bool   `json:"isDirectory"`
	}
	var d1 []item
	json.Unmarshal(itemsJSON, &d1)
	if len(d1) != 2 {
		t.Fatalf("depth=1: expected 2 top-level items (src + main.go), got %d: %v", len(d1), d1)
	}
	for _, it := range d1 {
		if it.Name == "internal" || it.Name == "config.go" || it.Name == "app.go" {
			t.Errorf("depth=1 should NOT include %s (it is inside src/)", it.Name)
		}
	}

	// depth=2：看到 workspace + src 子目录（app.go + internal/）
	conn2 := &readFileCaptureConn{}
	h.handleListDirectory(conn2, WireMessage{
		RequestID: "req_depth2",
		Params: mustJSONRaw(t, map[string]any{
			"path":           workspace,
			"workspace_root": workspace,
			"depth":          2,
		}),
	})
	resMap2 := conn2.data.(map[string]interface{})
	itemsJSON2, _ := json.Marshal(resMap2["items"])
	var d2 []item
	json.Unmarshal(itemsJSON2, &d2)
	if len(d2) < 4 {
		t.Fatalf("depth=2: expected >=4 items (top-level 2 + src children), got %d: %v", len(d2), d2)
	}
	hasAppGo := false
	hasInternal := false
	hasConfigGo := false
	for _, it := range d2 {
		if it.Name == "app.go" {
			hasAppGo = true
		}
		if it.Name == "internal" {
			hasInternal = true
		}
		if it.Name == "config.go" {
			hasConfigGo = true
		}
	}
	if !hasAppGo {
		t.Error("depth=2 should include src/app.go")
	}
	if !hasInternal {
		t.Error("depth=2 should include src/internal/ (subdirectory)")
	}
	if hasConfigGo {
		t.Error("depth=2 should NOT include src/internal/config.go (it is depth=3)")
	}
}

func TestListDirectory_BroadModeWithHomeDir(t *testing.T) {
	// 广域模式（无 workspace_root）：保持现有行为——expandPath 展开 ~ 到家目录。
	h := newTestHandlers(t)
	conn := &readFileCaptureConn{}
	h.handleListDirectory(conn, WireMessage{
		RequestID: "req_home",
		Params:    mustJSONRaw(t, map[string]any{"path": "~"}),
	})
	if conn.err != nil {
		t.Fatalf("broad mode ~ should succeed, got error: %+v", conn.err)
	}
	resMap, _ := conn.data.(map[string]interface{})
	cp, ok := resMap["currentPath"].(string)
	if !ok || cp == "" {
		t.Fatal("broad mode should return currentPath (home dir)")
	}
	homeDir, _ := os.UserHomeDir()
	if homeDir != "" && cp != homeDir {
		t.Errorf("expected ~ to resolve to %s, got %s", homeDir, cp)
	}
	// 验证响应有 hasMore 字段（additive，picker 也带）。
	if _, ok := resMap["hasMore"]; !ok {
		t.Error("broad mode response should include hasMore field")
	}
}

// TestListDirectory_BroadMode_SymlinkIsLeaf 固化 review① 不变量：广域模式（无 workspace_root）
// 下 symlink 仍是叶子——即便 symlink 指向浏览目录之外（等价 ~/.ssh 等敏感目录），也只返回
// isSymlink:true 叶子标记，不递归展开 target 内容。picker 可浏览任意真实目录，但不穿越 symlink。
// 该守卫由 collectDirItems 的 mode-independent `!isSymlink` 递归门保证，与 workspaceBound 无关。
func TestListDirectory_BroadMode_SymlinkIsLeaf(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "leak.txt")
	if err := os.WriteFile(outsideFile, []byte("leaked"), 0o600); err != nil {
		t.Fatal(err)
	}
	// symlink 在广域浏览目录内，指向外部敏感目录（模拟经 symlink 翻到 ~/.ssh 等）。
	escLink := filepath.Join(workspace, "escape-link")
	if err := os.Symlink(outside, escLink); err != nil {
		t.Fatal(err)
	}

	h := newTestHandlers(t)
	conn := &readFileCaptureConn{}
	// 广域模式：不传 workspace_root；depth=3 给足递归空间，若 symlink 被错误展开会暴露 leak.txt。
	h.handleListDirectory(conn, WireMessage{
		RequestID: "req_broad_symlink",
		Params: mustJSONRaw(t, map[string]any{
			"path":  workspace,
			"depth": 3,
		}),
	})
	if conn.err != nil {
		t.Fatalf("broad mode symlink listing should succeed, got error: %+v", conn.err)
	}
	resMap, _ := conn.data.(map[string]interface{})
	itemsRaw, _ := json.Marshal(resMap["items"])

	type item struct {
		Name      string `json:"name"`
		IsSymlink bool   `json:"isSymlink,omitempty"`
	}
	var items []item
	json.Unmarshal(itemsRaw, &items)

	hasLink := false
	for _, it := range items {
		if it.Name == "leak.txt" {
			t.Fatalf("broad mode symlink leaf violated: target content '%s' leaked from outside dir", it.Name)
		}
		if it.Name == "escape-link" {
			hasLink = true
			if !it.IsSymlink {
				t.Error("escape-link should be marked isSymlink=true in broad mode")
			}
		}
	}
	if !hasLink {
		t.Error("missing symlink entry 'escape-link' in broad mode listing")
	}
}
