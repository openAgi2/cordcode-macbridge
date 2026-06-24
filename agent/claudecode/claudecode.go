package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.TodoProvider = (*Agent)(nil)

func init() {
	core.RegisterAgent("claudecode", New)
}

// Agent drives Claude Code CLI using --input-format stream-json
// and --permission-prompt-tool stdio for bidirectional communication.
//
// Permission modes (maps to Claude's --permission-mode):
//   - "default":           every tool call requires user approval
//   - "acceptEdits":       auto-approve file edit tools, ask for others
//   - "plan":              plan only, no execution until approved
//   - "auto":              Claude's automatic permission classifier
//   - "bypassPermissions": auto-approve everything (alias: yolo)
type Agent struct {
	workDir          string
	cliBin           string   // CLI binary name or path (default: "claude")
	cliExtraArgs     []string // extra args parsed from cli_path (e.g. ["code", "-t", "foo"])
	cliArgsFlag      string   // if set, claude args are passed as a single string via this flag (e.g. "-a")
	model            string
	reasoningEffort  string // "low" | "medium" | "high" | "max"
	mode             string // "default" | "acceptEdits" | "plan" | "auto" | "bypassPermissions" | "dontAsk"
	allowedTools     []string
	disallowedTools  []string
	maxContextTokens int // optional: passed as --max-context-tokens when > 0
	providers        []core.ProviderConfig
	activeIdx        int // -1 = no provider set
	sessionEnv       []string
	routerURL        string // Claude Code Router URL (e.g., "http://127.0.0.1:3456")
	routerAPIKey     string // Claude Code Router API key (optional)

	providerProxy  *core.ProviderProxy // local proxy for third-party providers
	proxyLocalURL  string              // local URL of the proxy
	platformPrompt string              // platform-specific formatting instructions

	// spawnOpts controls OS-user isolation via run_as_user. Zero value
	// means legacy spawn as the supervisor user. See core/runas.go.
	spawnOpts core.SpawnOptions

	mu sync.RWMutex
}

var claudeProviderManagedEnvVars = map[string]struct{}{
	"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST":                  {},
	"CLAUDE_CODE_USE_BEDROCK":                               {},
	"CLAUDE_CODE_USE_VERTEX":                                {},
	"CLAUDE_CODE_USE_FOUNDRY":                               {},
	"ANTHROPIC_BASE_URL":                                    {},
	"ANTHROPIC_BEDROCK_BASE_URL":                            {},
	"ANTHROPIC_VERTEX_BASE_URL":                             {},
	"ANTHROPIC_FOUNDRY_BASE_URL":                            {},
	"ANTHROPIC_FOUNDRY_RESOURCE":                            {},
	"ANTHROPIC_VERTEX_PROJECT_ID":                           {},
	"CLOUD_ML_REGION":                                       {},
	"ANTHROPIC_API_KEY":                                     {},
	"ANTHROPIC_AUTH_TOKEN":                                  {},
	"CLAUDE_CODE_OAUTH_TOKEN":                               {},
	"AWS_BEARER_TOKEN_BEDROCK":                              {},
	"ANTHROPIC_FOUNDRY_API_KEY":                             {},
	"CLAUDE_CODE_SKIP_BEDROCK_AUTH":                         {},
	"CLAUDE_CODE_SKIP_VERTEX_AUTH":                          {},
	"CLAUDE_CODE_SKIP_FOUNDRY_AUTH":                         {},
	"ANTHROPIC_MODEL":                                       {},
	"ANTHROPIC_DEFAULT_HAIKU_MODEL":                         {},
	"ANTHROPIC_DEFAULT_HAIKU_MODEL_DESCRIPTION":             {},
	"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME":                    {},
	"ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES":  {},
	"ANTHROPIC_DEFAULT_OPUS_MODEL":                          {},
	"ANTHROPIC_DEFAULT_OPUS_MODEL_DESCRIPTION":              {},
	"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":                     {},
	"ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES":   {},
	"ANTHROPIC_DEFAULT_SONNET_MODEL":                        {},
	"ANTHROPIC_DEFAULT_SONNET_MODEL_DESCRIPTION":            {},
	"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME":                   {},
	"ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES": {},
	"ANTHROPIC_SMALL_FAST_MODEL":                            {},
	"ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION":                 {},
	"CLAUDE_CODE_SUBAGENT_MODEL":                            {},
}

var claudeProviderManagedEnvPrefixes = []string{
	"VERTEX_REGION_CLAUDE_",
}

func New(opts map[string]any) (core.Agent, error) {
	workDir, _ := opts["work_dir"].(string)
	if workDir == "" {
		workDir = "."
	}
	cliBin := "claude"
	var cliExtraArgs []string
	if cliPath, _ := opts["cli_path"].(string); cliPath != "" {
		// NOTE: paths containing spaces are not supported because Fields
		// splits on whitespace. Use a symlink or wrapper script instead.
		parts := strings.Fields(cliPath)
		cliBin = parts[0]
		if len(parts) > 1 {
			cliExtraArgs = parts[1:]
		}
	}
	cliArgsFlag, _ := opts["cli_args_flag"].(string)
	model, _ := opts["model"].(string)
	reasoningEffort, _ := opts["reasoning_effort"].(string)
	mode, _ := opts["mode"].(string)
	mode = normalizePermissionMode(mode)

	var allowedTools []string
	if tools, ok := opts["allowed_tools"].([]any); ok {
		for _, t := range tools {
			if s, ok := t.(string); ok {
				allowedTools = append(allowedTools, s)
			}
		}
	}

	var disallowedTools []string
	if tools, ok := opts["disallowed_tools"].([]any); ok {
		for _, t := range tools {
			if s, ok := t.(string); ok {
				disallowedTools = append(disallowedTools, s)
			}
		}
	}

	maxContextTokens := 0
	switch v := opts["max_context_tokens"].(type) {
	case int:
		if v > 0 {
			maxContextTokens = v
		}
	case int64:
		if v > 0 {
			maxContextTokens = int(v)
		}
	case float64:
		if v > 0 {
			maxContextTokens = int(v)
		}
	}

	// Claude Code Router support
	routerURL, _ := opts["router_url"].(string)
	routerAPIKey, _ := opts["router_api_key"].(string)

	// run_as_user: optional OS-user isolation. Injected into opts from
	// the project-level config field by cmd/cc-connect/main.go.
	spawnOpts := core.SpawnOptions{}
	spawnOpts.RunAsUser, _ = opts["run_as_user"].(string)
	if env, ok := opts["run_as_env"].([]any); ok {
		for _, v := range env {
			if s, ok := v.(string); ok {
				spawnOpts.EnvAllowlist = append(spawnOpts.EnvAllowlist, s)
			}
		}
	} else if env, ok := opts["run_as_env"].([]string); ok {
		spawnOpts.EnvAllowlist = append(spawnOpts.EnvAllowlist, env...)
	}

	// When run_as_user is set, the target user's PATH is what matters;
	// skip the supervisor-side LookPath check and let spawn fail loudly
	// at runtime if the target doesn't have claude installed.
	if !spawnOpts.IsolationMode() {
		if _, err := exec.LookPath(cliBin); err != nil {
			return nil, fmt.Errorf("claudecode: %q CLI not found in PATH, please install it first", cliBin)
		}
	}

	return &Agent{
		workDir:          workDir,
		cliBin:           cliBin,
		cliExtraArgs:     cliExtraArgs,
		cliArgsFlag:      cliArgsFlag,
		model:            model,
		reasoningEffort:  normalizeEffort(reasoningEffort),
		mode:             mode,
		allowedTools:     allowedTools,
		disallowedTools:  disallowedTools,
		maxContextTokens: maxContextTokens,
		activeIdx:        -1,
		routerURL:        routerURL,
		routerAPIKey:     routerAPIKey,
		spawnOpts:        spawnOpts,
	}, nil
}

// normalizeEffort maps user-friendly aliases to Claude CLI --effort values.
func normalizeEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "low":
		return "low"
	case "medium", "med":
		return "medium"
	case "high":
		return "high"
	case "max", "xhigh", "extra-high", "extra_high":
		return "max"
	default:
		return ""
	}
}

// normalizePermissionMode maps user-friendly aliases to Claude CLI values.
func normalizePermissionMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "acceptedits", "accept-edits", "accept_edits", "edit":
		return "acceptEdits"
	case "plan":
		return "plan"
	case "auto":
		return "auto"
	case "bypasspermissions", "bypass-permissions", "bypass_permissions",
		"yolo":
		return "bypassPermissions"
	case "dontask", "dont-ask", "dont_ask":
		return "dontAsk"
	default:
		return "default"
	}
}

func (a *Agent) Name() string           { return "claudecode" }
func (a *Agent) CLIBinaryName() string  { return a.cliBin }
func (a *Agent) CLIDisplayName() string { return "Claude" }

func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workDir = dir
	slog.Info("claudecode: work_dir changed", "work_dir", dir)
}

func (a *Agent) GetWorkDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workDir
}

func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
	slog.Info("claudecode: model changed", "model", model)
}

func (a *Agent) GetModel() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return core.GetProviderModel(a.providers, a.activeIdx, a.model)
}

func (a *Agent) SetReasoningEffort(effort string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reasoningEffort = normalizeEffort(effort)
	slog.Info("claudecode: reasoning effort changed", "effort", a.reasoningEffort)
}

func (a *Agent) GetReasoningEffort() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reasoningEffort
}

func (a *Agent) AvailableReasoningEfforts() []string {
	return []string{"low", "medium", "high", "max"}
}

func (a *Agent) configuredModels() []core.ModelOption {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return core.GetProviderModels(a.providers, a.activeIdx)
}

func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	if models := a.configuredModels(); len(models) > 0 {
		return models
	}
	if models := a.fetchModelsFromAPI(ctx); len(models) > 0 {
		return models
	}
	return []core.ModelOption{
		{Name: "sonnet", Desc: "Claude Sonnet (balanced)"},
		{Name: "opus", Desc: "Claude Opus (most capable)"},
		{Name: "opus[1m]", Desc: "Claude Opus (1M context)"},
		{Name: "haiku", Desc: "Claude Haiku (fastest)"},
	}
}

func (a *Agent) fetchModelsFromAPI(ctx context.Context) []core.ModelOption {
	a.mu.Lock()
	apiKey := ""
	baseURL := ""
	if a.activeIdx >= 0 && a.activeIdx < len(a.providers) {
		apiKey = a.providers[a.activeIdx].APIKey
		baseURL = a.providers[a.activeIdx].BaseURL
	}
	routerURL := a.routerURL
	routerAPIKey := a.routerAPIKey
	a.mu.Unlock()

	if apiKey == "" {
		if routerAPIKey != "" {
			apiKey = routerAPIKey
		}
	}
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil
	}
	if baseURL == "" {
		if routerURL != "" {
			baseURL = routerURL
		}
	}
	if baseURL == "" {
		baseURL = os.Getenv("ANTHROPIC_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/v1/models", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Debug("claudecode: failed to fetch models", "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var result struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}

	var models []core.ModelOption
	for _, m := range result.Data {
		models = append(models, core.ModelOption{Name: m.ID, Desc: m.DisplayName})
	}
	return models
}

func (a *Agent) SetSessionEnv(env []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionEnv = env
}

func (a *Agent) SetPlatformPrompt(prompt string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.platformPrompt = prompt
}

// StartSession creates a persistent interactive Claude Code session.
func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.Lock()
	tools := make([]string, len(a.allowedTools))
	copy(tools, a.allowedTools)
	disTools := make([]string, len(a.disallowedTools))
	copy(disTools, a.disallowedTools)
	maxTok := a.maxContextTokens
	model := a.model
	effort := a.reasoningEffort
	workDir := a.workDir
	cliBin := a.cliBin
	cliExtraArgs := append([]string(nil), a.cliExtraArgs...)
	cliArgsFlag := a.cliArgsFlag
	mode := a.mode
	spawnOpts := a.spawnOpts
	extraEnv := a.runtimeEnvLocked()

	activeIdx := a.activeIdx
	var activeProviderName string
	if activeIdx >= 0 && activeIdx < len(a.providers) {
		activeProviderName = a.providers[activeIdx].Name
		if m := a.providers[activeIdx].Model; m != "" {
			model = m
		}
	}
	slog.Debug("claudecode: StartSession provider state",
		"activeIdx", activeIdx,
		"activeProvider", activeProviderName,
		"model", model,
		"sessionID", sessionID,
		"providerCount", len(a.providers))
	platformPrompt := a.platformPrompt
	// When router_url is set, --verbose conflicts with --output-format stream-json
	// (verbose emits non-JSON text to stdout that corrupts the JSON stream).
	disableVerbose := a.routerURL != ""
	a.mu.Unlock()

	return newClaudeSession(ctx, workDir, cliBin, cliExtraArgs, cliArgsFlag, model, effort, sessionID, mode, tools, disTools, extraEnv, platformPrompt, disableVerbose, spawnOpts, maxTok)
}

func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	projectDir, err := a.resolveClaudeProjectDir()
	if err != nil {
		if strings.Contains(err.Error(), "project dir not found") {
			return nil, nil
		}
		return nil, err
	}

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("claudecode: read project dir: %w", err)
	}

	var sessions []core.AgentSessionInfo
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		sessionID := strings.TrimSuffix(name, ".jsonl")
		info, err := entry.Info()
		if err != nil {
			continue
		}

		sessionInfo, err := a.buildClaudeSessionInfo(projectDir, sessionID, filepath.Join(projectDir, name), info.ModTime())
		if err != nil {
			continue
		}
		sessions = append(sessions, sessionInfo)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModifiedAt.After(sessions[j].ModifiedAt)
	})

	return sessions, nil
}

func isSessionExecuting(sessionPath string) bool {
	f, err := os.Open(sessionPath)
	if err != nil {
		slog.Debug("isSessionExecuting: failed to open transcript, assuming idle",
			"path", sessionPath, "error", err)
		return false
	}
	defer f.Close()

	var lastMsg struct {
		Role       string
		StopReason string
		Text       string
	}
	hasMsg := false

	scanner := bufio.NewScanner(f)
	// Claude session files can have very long lines (e.g. tool outputs)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024*16) // Up to 16MB per line

	for scanner.Scan() {
		var entry struct {
			Type    string                    `json:"type"`
			Message *transcriptHistoryMessage `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Message == nil {
			continue
		}
		if entry.Type == "user" {
			lastMsg.Role = "user"
			lastMsg.StopReason = ""
			lastMsg.Text = extractTextContent(entry.Message.Content)
			hasMsg = true
		} else if entry.Type == "assistant" {
			lastMsg.Role = "assistant"
			lastMsg.StopReason = entry.Message.StopReason
			lastMsg.Text = ""
			hasMsg = true
		}
	}

	if !hasMsg {
		return false
	}

	if lastMsg.Role == "user" {
		trimmed := strings.TrimSpace(lastMsg.Text)
		if strings.HasPrefix(trimmed, "[Request interrupted by user") {
			slog.Debug("isSessionExecuting: last message is user interrupt → idle",
				"path", sessionPath)
			return false
		}
		// Last message is a user message without an assistant response yet →
		// session may still be executing (Claude is generating a response).
		slog.Debug("isSessionExecuting: last message is user (not interrupted) → executing",
			"path", sessionPath)
		return true
	}

	if lastMsg.Role == "assistant" {
		sr := lastMsg.StopReason
		isFinal := sr == "end_turn" || sr == "stop_limit" || sr == "stop_sequence" || sr == "max_tokens"
		if !isFinal {
			slog.Debug("isSessionExecuting: assistant without stop_reason → executing",
				"path", sessionPath)
		}
		return !isFinal
	}

	return false
}

func (a *Agent) GetRunningSessionIDs(ctx context.Context) (map[string]bool, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	sessionsDir := filepath.Join(homeDir, ".claude", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	running := make(map[string]bool)
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(sessionsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var state struct {
			Pid       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
		}
		if err := json.Unmarshal(data, &state); err != nil {
			continue
		}
		if state.SessionID != "" && isProcessRunning(state.Pid) {
			// Default to false: if we cannot locate and inspect the transcript file,
			// we cannot prove the session is still executing.  The old default of
			// true caused sessions whose .jsonl was inaccessible (wrong project dir,
			// missing file, etc.) to be permanently locked as "running".
			isExecuting := false
			if state.Cwd != "" {
				projectDir := findProjectDir(homeDir, state.Cwd)
				if projectDir != "" {
					sessionPath := filepath.Join(projectDir, state.SessionID+".jsonl")
					if _, err := os.Stat(sessionPath); err == nil {
						isExecuting = isSessionExecuting(sessionPath)
					} else {
						slog.Info("GetRunningSessionIDs: transcript file not found, defaulting to idle",
							"sessionID", state.SessionID,
							"cwd", state.Cwd,
							"projectDir", projectDir,
							"sessionPath", sessionPath,
							"statError", err)
					}
				} else {
					slog.Warn("GetRunningSessionIDs: project dir not found for cwd, defaulting to idle",
						"sessionID", state.SessionID,
						"cwd", state.Cwd)
				}
			}
			if isExecuting {
				running[state.SessionID] = true
			}
		}
	}
	return running, nil
}

func (a *Agent) DeleteSession(_ context.Context, sessionID string) error {
	projectDir, path, err := a.resolveClaudeSessionPath(sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return removeClaudeSessionSidecar(projectDir, sessionID)
}

func scanSessionMeta(path string) (string, int) {
	projectDir := filepath.Dir(path)
	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	meta, err := scanClaudeSessionMeta(path, projectDir, sessionID)
	if err != nil {
		return "", 0
	}
	return meta.Title, meta.MessageCount
}

var xmlTagRe = regexp.MustCompile(`<[^>]+>`)

func stripXMLTags(s string) string {
	return xmlTagRe.ReplaceAllString(s, "")
}

const transcriptScannerMaxBytes = 16 * 1024 * 1024

type transcriptHistoryEnvelope struct {
	Type      string                    `json:"type"`
	Timestamp string                    `json:"timestamp"`
	Message   *transcriptHistoryMessage `json:"message"`
}

type transcriptHistoryMessage struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Model      string          `json:"model"`
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
}

type transcriptContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Raw       json.RawMessage `json:"-"`
}

type transcriptToolResult struct {
	Output  any
	IsError bool
}

type richHistoryMessageBuilder struct {
	ID               string
	Role             string
	Timestamp        time.Time
	ContentSegments  []string
	ThinkingSegments []string
	Parts            []richHistoryPartBuilder
	Steps            map[string]map[string]any
	StepOrder        []string
	ModelID          string
	AgentName        string
	ProviderID       string
	ModelName        string
}

type richHistoryPartBuilder struct {
	Value  map[string]any
	StepID string
}

func newRichHistoryMessageBuilder(id, role string, timestamp time.Time) *richHistoryMessageBuilder {
	return &richHistoryMessageBuilder{
		ID:        id,
		Role:      role,
		Timestamp: timestamp,
		Steps:     make(map[string]map[string]any),
	}
}

func (b *richHistoryMessageBuilder) addText(text string) {
	if text == "" {
		return
	}
	b.ContentSegments = append(b.ContentSegments, text)
	b.Parts = append(b.Parts, richHistoryPartBuilder{
		Value: map[string]any{
			"type":    "text",
			"content": text,
		},
	})
}

func (b *richHistoryMessageBuilder) addThinking(thinking string) {
	if thinking == "" {
		return
	}
	b.ThinkingSegments = append(b.ThinkingSegments, thinking)
	b.Parts = append(b.Parts, richHistoryPartBuilder{
		Value: map[string]any{
			"type":    "reasoning",
			"content": thinking,
		},
	})
}

func (b *richHistoryMessageBuilder) addToolUse(toolID, toolName string) string {
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		toolID = fmt.Sprintf("tool-%d", len(b.StepOrder)+1)
	}
	if _, exists := b.Steps[toolID]; exists {
		return toolID
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		toolName = "unknown"
	}
	step := map[string]any{
		"id":                             toolID,
		"toolName":                       toolName,
		"title":                          toolName,
		"status":                         "unknown",
		"requiresPermissionConfirmation": false,
		"availablePermissionOptions":     []any{},
	}
	b.Steps[toolID] = step
	b.StepOrder = append(b.StepOrder, toolID)
	b.Parts = append(b.Parts, richHistoryPartBuilder{StepID: toolID})
	return toolID
}

func (b *richHistoryMessageBuilder) applyToolResult(toolID string, result transcriptToolResult) bool {
	step, ok := b.Steps[toolID]
	if !ok {
		return false
	}
	if result.IsError {
		step["status"] = "failed"
	} else {
		step["status"] = "completed"
	}
	if result.Output != nil {
		step["output"] = result.Output
	}
	return true
}

func (b *richHistoryMessageBuilder) build() core.RichHistoryEntry {
	steps := make([]map[string]any, 0, len(b.StepOrder))
	stepRefs := make(map[string]map[string]any, len(b.StepOrder))
	for _, stepID := range b.StepOrder {
		step, ok := b.Steps[stepID]
		if !ok {
			continue
		}
		steps = append(steps, step)
		stepRefs[stepID] = step
	}

	parts := make([]map[string]any, 0, len(b.Parts))
	for _, part := range b.Parts {
		if part.StepID != "" {
			if step, ok := stepRefs[part.StepID]; ok {
				parts = append(parts, map[string]any{
					"type": "tool",
					"step": step,
				})
			}
			continue
		}
		parts = append(parts, part.Value)
	}

	entry := core.RichHistoryEntry{
		ID:         b.ID,
		Role:       b.Role,
		Content:    strings.Join(b.ContentSegments, "\n\n"),
		Thinking:   strings.Join(b.ThinkingSegments, "\n\n"),
		Parts:      parts,
		Steps:      steps,
		Timestamp:  b.Timestamp,
		AgentName:  b.AgentName,
		ModelID:    b.ModelID,
		ProviderID: b.ProviderID,
		ModelName:  b.ModelName,
	}
	if entry.Parts == nil {
		entry.Parts = []map[string]any{}
	}
	if entry.Steps == nil {
		entry.Steps = []map[string]any{}
	}
	if entry.Files == nil {
		entry.Files = []map[string]any{}
	}
	return entry
}

func newTranscriptScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 256*1024), transcriptScannerMaxBytes)
	return scanner
}

func decodeTranscriptContentBlocks(raw json.RawMessage) []transcriptContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return []transcriptContentBlock{{Type: "text", Text: plain, Raw: raw}}
	}

	var rawBlocks []json.RawMessage
	if json.Unmarshal(raw, &rawBlocks) != nil {
		return nil
	}

	blocks := make([]transcriptContentBlock, 0, len(rawBlocks))
	for _, rawBlock := range rawBlocks {
		var block transcriptContentBlock
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			continue
		}
		block.Raw = rawBlock
		blocks = append(blocks, block)
	}
	return blocks
}

func normalizeToolResultOutput(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var text string
	if json.Unmarshal(raw, &text) == nil {
		return map[string]any{"kind": "inline", "text": text}
	}

	var array []any
	if json.Unmarshal(raw, &array) == nil {
		joined := extractTextFromJSONArray(array)
		if joined != "" {
			return map[string]any{"kind": "inline", "text": joined}
		}
		return array
	}

	var object map[string]any
	if json.Unmarshal(raw, &object) == nil {
		return object
	}

	var scalar any
	if json.Unmarshal(raw, &scalar) == nil {
		return scalar
	}

	return map[string]any{"kind": "inline", "text": string(raw)}
}

func extractTextFromJSONArray(array []any) string {
	segments := make([]string, 0, len(array))
	for _, item := range array {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, _ := obj["text"].(string); text != "" {
			segments = append(segments, text)
		}
	}
	return strings.Join(segments, "\n")
}

func parseTranscriptTimestamp(raw string) time.Time {
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return ts
}

func loadClaudeRichHistory(path string) ([]core.RichHistoryEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("claudecode: open session file: %w", err)
	}
	defer f.Close()
	return LoadClaudeRichHistoryFromReader(f, path)
}

// LoadClaudeRichHistoryFromReader runs the Claude logical-message grouping
// builder over r (a transcript or a byte-range section of one) and returns the
// logical messages in file order. path is used only for debug logging. Exported
// so the bridge can replay a transcript page index byte range (design §6.3); the
// boundaries match the claude span extractor in package transcriptindex.
func LoadClaudeRichHistoryFromReader(r io.Reader, path string) ([]core.RichHistoryEntry, error) {
	scanner := newTranscriptScanner(r)
	builders := make([]*richHistoryMessageBuilder, 0)
	assistantByMessageID := make(map[string]*richHistoryMessageBuilder)
	toolOwners := make(map[string]*richHistoryMessageBuilder)
	pendingToolResults := make(map[string]transcriptToolResult)

	lineNo := 0
	for scanner.Scan() {
		lineNo++

		var raw transcriptHistoryEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			slog.Debug("claudecode: skip invalid transcript line", "path", path, "line", lineNo, "error", err)
			continue
		}
		if raw.Type != "user" && raw.Type != "assistant" {
			continue
		}
		if raw.Message == nil {
			continue
		}

		blocks := decodeTranscriptContentBlocks(raw.Message.Content)
		timestamp := parseTranscriptTimestamp(raw.Timestamp)

		switch raw.Type {
		case "assistant":
			messageID := strings.TrimSpace(raw.Message.ID)
			if messageID == "" {
				messageID = fmt.Sprintf("assistant-line-%d", lineNo)
			}
			builder, ok := assistantByMessageID[messageID]
			if !ok {
				builder = newRichHistoryMessageBuilder(messageID, "assistant", timestamp)
				assistantByMessageID[messageID] = builder
				builders = append(builders, builder)
			}
			if builder.Timestamp.IsZero() && !timestamp.IsZero() {
				builder.Timestamp = timestamp
			}
			if builder.ModelID == "" {
				builder.ModelID = raw.Message.Model
			}

			for _, block := range blocks {
				switch block.Type {
				case "text":
					builder.addText(block.Text)
				case "thinking":
					builder.addThinking(block.Thinking)
				case "tool_use":
					toolID := builder.addToolUse(block.ID, block.Name)
					toolOwners[toolID] = builder
					if pending, ok := pendingToolResults[toolID]; ok {
						builder.applyToolResult(toolID, pending)
						delete(pendingToolResults, toolID)
					}
				case "":
					continue
				default:
					slog.Debug("claudecode: skip unsupported assistant block", "path", path, "line", lineNo, "type", block.Type)
				}
			}

		case "user":
			builder := newRichHistoryMessageBuilder(strings.TrimSpace(raw.Message.ID), "user", timestamp)
			if builder.ID == "" {
				builder.ID = fmt.Sprintf("user-line-%d", lineNo)
			}
			hasVisibleContent := false

			for _, block := range blocks {
				switch block.Type {
				case "tool_result":
					if block.ToolUseID == "" {
						continue
					}
					result := transcriptToolResult{
						Output:  normalizeToolResultOutput(block.Content),
						IsError: block.IsError,
					}
					if owner, ok := toolOwners[block.ToolUseID]; ok {
						owner.applyToolResult(block.ToolUseID, result)
					} else {
						pendingToolResults[block.ToolUseID] = result
					}
				case "text":
					hasVisibleContent = true
					builder.addText(block.Text)
				case "":
					continue
				default:
					slog.Debug("claudecode: skip unsupported user block", "path", path, "line", lineNo, "type", block.Type)
				}
			}

			if hasVisibleContent {
				builders = append(builders, builder)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("claudecode: scan session file: %w", err)
	}

	entries := make([]core.RichHistoryEntry, 0, len(builders))
	for _, builder := range builders {
		entries = append(entries, builder.build())
	}
	return entries, nil
}

// GetSessionHistory reads the Claude Code JSONL transcript and returns user/assistant messages.
func (a *Agent) GetSessionHistory(_ context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	richEntries, err := a.GetRichSessionHistory(context.Background(), sessionID, 0)
	if err != nil {
		return nil, err
	}

	entries := make([]core.HistoryEntry, 0, len(richEntries))
	for _, entry := range richEntries {
		if strings.TrimSpace(entry.Content) == "" {
			continue
		}
		entries = append(entries, core.HistoryEntry{
			Role:      entry.Role,
			Content:   entry.Content,
			Timestamp: entry.Timestamp,
		})
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// TranscriptPath resolves the on-disk JSONL transcript path for a Claude Code
// session under the agent's current work directory, implementing
// core.TranscriptLocator so the bridge can index and page it.
func (a *Agent) TranscriptPath(_ context.Context, sessionID string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	absWorkDir, _ := filepath.Abs(a.workDir)
	projectDir := findProjectDir(homeDir, absWorkDir)
	if projectDir == "" {
		return "", fmt.Errorf("claudecode: project dir not found")
	}
	return filepath.Join(projectDir, sessionID+".jsonl"), nil
}

func (a *Agent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	absWorkDir, _ := filepath.Abs(a.workDir)
	projectDir := findProjectDir(homeDir, absWorkDir)
	if projectDir == "" {
		return nil, fmt.Errorf("claudecode: project dir not found")
	}

	path := filepath.Join(projectDir, sessionID+".jsonl")
	started := time.Now()
	entries, err := loadClaudeRichHistory(path)
	var fileBytes int64
	if stat, statErr := os.Stat(path); statErr == nil {
		fileBytes = stat.Size()
	}
	core.SessionLoadMetricsFromContext(ctx).AddHistoryParse(time.Since(started), fileBytes)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// FetchTodos implements core.TodoProvider. It reads the session transcript to find
// the most recent TodoWrite tool_use block and returns the full todo list.
// Claude Code's TodoWrite always sends the complete list, so the last block is authoritative.
func (a *Agent) FetchTodos(_ context.Context, sessionID string) ([]core.Todo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	absWorkDir, _ := filepath.Abs(a.workDir)
	projectDir := findProjectDir(homeDir, absWorkDir)
	if projectDir == "" {
		return nil, fmt.Errorf("claudecode: project dir not found")
	}

	sessionPath := filepath.Join(projectDir, sessionID+".jsonl")
	f, err := os.Open(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("claudecode: open session file: %w", err)
	}
	defer f.Close()

	var latestTodos []core.Todo
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var raw transcriptHistoryEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			continue
		}
		if raw.Type != "assistant" || raw.Message == nil {
			continue
		}
		blocks := decodeTranscriptContentBlocks(raw.Message.Content)
		for _, block := range blocks {
			if block.Type != "tool_use" || !strings.EqualFold(block.Name, "TodoWrite") {
				continue
			}
			todos := extractTodosFromToolInput(block.Input)
			if len(todos) > 0 {
				latestTodos = todos
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("claudecode: scan session file: %w", err)
	}
	return latestTodos, nil
}

// extractTodosFromToolInput parses a TodoWrite tool_use input into []core.Todo.
// Claude Code's TodoWrite input can be a flat array of todo objects or
// an object with a "todos" array.
func extractTodosFromToolInput(input json.RawMessage) []core.Todo {
	if len(input) == 0 {
		return nil
	}

	type rawTodo struct {
		Content    string `json:"content"`
		Status     string `json:"status"`
		Priority   string `json:"priority"`
		ActiveForm string `json:"activeForm"`
	}

	// Try object form: { "todos": [...] }
	var wrapper struct {
		Todos []rawTodo `json:"todos"`
	}
	if json.Unmarshal(input, &wrapper) == nil && len(wrapper.Todos) > 0 {
		todos := make([]core.Todo, 0, len(wrapper.Todos))
		for _, t := range wrapper.Todos {
			if t.Content != "" {
				todos = append(todos, core.Todo{
					Content:  t.Content,
					Status:   defaultString(t.Status, "pending"),
					Priority: defaultString(t.Priority, "medium"),
				})
			}
		}
		return todos
	}

	// Try flat array form
	var flat []rawTodo
	if json.Unmarshal(input, &flat) == nil && len(flat) > 0 {
		todos := make([]core.Todo, 0, len(flat))
		for _, t := range flat {
			if t.Content != "" {
				todos = append(todos, core.Todo{
					Content:  t.Content,
					Status:   defaultString(t.Status, "pending"),
					Priority: defaultString(t.Priority, "medium"),
				})
			}
		}
		return todos
	}

	return nil
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// extractTextContent extracts readable text from Claude Code message content.
// Content can be a plain string or an array of content blocks.
func extractTextContent(raw json.RawMessage) string {
	blocks := decodeTranscriptContentBlocks(raw)
	segments := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			segments = append(segments, block.Text)
		}
	}
	return strings.Join(segments, "\n\n")
}

// Stop is a no-op for now. Per-session Close() already reaps each CLI's process
// group on shutdown (Handlers.Shutdown → AgentSession.Close). Agent-wide stop
// (stopping background usage probes / passive subscriptions) is not yet wired.
// TODO: process-group stop at the Agent level.
func (a *Agent) Stop() error { return nil }

// SetMode changes the permission mode for future sessions.
func (a *Agent) SetMode(mode string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mode = normalizePermissionMode(mode)
	slog.Info("claudecode: permission mode changed", "mode", a.mode)
}

// GetMode returns the current permission mode.
func (a *Agent) GetMode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

// GetRunAsUser returns the target user for OS-isolation spawning, or ""
// if no isolation is configured. Set at construction from the project-level
// run_as_user field (injected into opts by cmd/cc-connect/main.go).
//
// This accessor exists specifically so multi-workspace mode can propagate
// run_as_user from the parent (project-level) agent into per-workspace
// agent instances created lazily by core.Engine.getOrCreateWorkspaceAgent.
// Without this, workspace agents are constructed with a fresh opts map
// that never contained run_as_user, silently dropping back to the legacy
// supervisor-user spawn path — which is exactly the leak cc-connect#496
// is designed to prevent.
func (a *Agent) GetRunAsUser() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.spawnOpts.RunAsUser
}

// GetRunAsEnv returns the user-configured env allowlist extension (the
// run_as_env project field), which is merged with core.DefaultEnvAllowlist
// at spawn time. Returns nil if no extension is configured.
//
// Used by the multi-workspace propagation path alongside GetRunAsUser.
func (a *Agent) GetRunAsEnv() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.spawnOpts.EnvAllowlist) == 0 {
		return nil
	}
	out := make([]string, len(a.spawnOpts.EnvAllowlist))
	copy(out, a.spawnOpts.EnvAllowlist)
	return out
}

// PermissionModes returns all supported permission modes.
func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	return []core.PermissionModeInfo{
		{Key: "default", Name: "Default", NameZh: "默认", Desc: "Ask permission for every tool call", DescZh: "每次工具调用都需确认"},
		{Key: "acceptEdits", Name: "Accept Edits", NameZh: "接受编辑", Desc: "Auto-approve file edits, ask for others", DescZh: "自动允许文件编辑，其他需确认"},
		{Key: "plan", Name: "Plan Mode", NameZh: "计划模式", Desc: "Plan only, no execution until approved", DescZh: "只做规划不执行，审批后再执行"},
		{Key: "auto", Name: "Auto", NameZh: "自动模式", Desc: "Claude decides when to ask for permission", DescZh: "由 Claude 自动判断何时需要确认"},
		{Key: "bypassPermissions", Name: "YOLO", NameZh: "YOLO 模式", Desc: "Auto-approve everything", DescZh: "全部自动通过"},
		{Key: "dontAsk", Name: "Don't Ask", NameZh: "静默拒绝", Desc: "Auto-deny tools unless pre-approved via allowed_tools or settings.json allow rules", DescZh: "未预授权的工具自动拒绝，不弹确认"},
	}
}

// AddAllowedTools adds tools to the pre-allowed list (takes effect on next session).
func (a *Agent) AddAllowedTools(tools ...string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	existing := make(map[string]bool)
	for _, t := range a.allowedTools {
		existing[t] = true
	}
	for _, tool := range tools {
		if !existing[tool] {
			a.allowedTools = append(a.allowedTools, tool)
			existing[tool] = true
		}
	}
	slog.Info("claudecode: updated allowed tools", "tools", tools, "total", len(a.allowedTools))
	return nil
}

// GetAllowedTools returns the current list of pre-allowed tools.
func (a *Agent) GetAllowedTools() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]string, len(a.allowedTools))
	copy(result, a.allowedTools)
	return result
}

// GetDisallowedTools returns the current list of disallowed tools.
func (a *Agent) GetDisallowedTools() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]string, len(a.disallowedTools))
	copy(result, a.disallowedTools)
	return result
}

// ── CommandProvider implementation ────────────────────────────

func (a *Agent) CommandDirs() []string {
	absDir, err := filepath.Abs(a.workDir)
	if err != nil {
		absDir = a.workDir
	}
	dirs := []string{filepath.Join(absDir, ".claude", "commands")}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".claude", "commands"))
	}
	return dirs
}

// ── SkillProvider implementation ──────────────────────────────

func (a *Agent) SkillDirs() []string {
	absDir, err := filepath.Abs(a.workDir)
	if err != nil {
		absDir = a.workDir
	}
	return appendProjectClaudeSkillDirs(absDir, claudeConfigHomeDir())
}

// ── ContextCompressor implementation ──────────────────────────

func (a *Agent) CompressCommand() string { return "/compact" }

func claudeConfigHomeDir() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

func appendProjectClaudeSkillDirs(workDir, configHome string) []string {
	home, _ := os.UserHomeDir()
	projectDirs := walkUpClaudeSkillDirs(workDir, home)
	if configHome == "" {
		return projectDirs
	}
	return uniqueSkillDirs(append(projectDirs, filepath.Join(configHome, "skills")))
}

func walkUpClaudeSkillDirs(workDir, home string) []string {
	current := filepath.Clean(workDir)
	home = filepath.Clean(home)
	stopAt := findGitRoot(current)

	var dirs []string
	for {
		if home != "" && samePath(current, home) {
			break
		}
		dirs = append(dirs, filepath.Join(current, ".claude", "skills"))
		if stopAt != "" && samePath(current, stopAt) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return uniqueSkillDirs(dirs)
}

func findGitRoot(start string) string {
	current := filepath.Clean(start)
	for {
		gitPath := filepath.Join(current, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func uniqueSkillDirs(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

// ── MemoryFileProvider implementation ─────────────────────────

func (a *Agent) ProjectMemoryFile() string {
	absDir, err := filepath.Abs(a.workDir)
	if err != nil {
		absDir = a.workDir
	}
	return filepath.Join(absDir, "CLAUDE.md")
}

func (a *Agent) GlobalMemoryFile() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".claude", "CLAUDE.md")
}

func (a *Agent) HasSystemPromptSupport() bool { return true }

// ── ProviderSwitcher implementation ──────────────────────────

func (a *Agent) SetProviders(providers []core.ProviderConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.providers = providers
}

func (a *Agent) SetActiveProvider(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopProviderProxyLocked()
	if name == "" {
		a.activeIdx = -1
		slog.Info("claudecode: provider cleared")
		return true
	}
	for i, p := range a.providers {
		if p.Name == name {
			a.activeIdx = i
			slog.Info("claudecode: provider switched", "provider", name)
			return true
		}
	}
	return false
}

func (a *Agent) GetActiveProvider() *core.ProviderConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeIdx < 0 || a.activeIdx >= len(a.providers) {
		return nil
	}
	p := a.providers[a.activeIdx]
	return &p
}

func (a *Agent) ListProviders() []core.ProviderConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]core.ProviderConfig, len(a.providers))
	copy(result, a.providers)
	return result
}

// providerEnvLocked returns env vars for the active provider. Caller must hold mu.
//
// When a custom base_url is configured:
//  1. We use ANTHROPIC_AUTH_TOKEN (Bearer) instead of ANTHROPIC_API_KEY
//     (x-api-key). Claude Code validates API keys against api.anthropic.com
//     which hangs for third-party endpoints; Bearer auth skips that check.
//  2. If the provider sets thinking (e.g. "disabled"), a local reverse proxy
//     rewrites the thinking parameter for compatibility with providers that
//     don't support adaptive thinking.
func (a *Agent) providerEnvLocked() []string {
	if a.activeIdx < 0 || a.activeIdx >= len(a.providers) {
		a.stopProviderProxyLocked()
		return nil
	}
	p := a.providers[a.activeIdx]
	var env []string

	if p.BaseURL != "" {
		if p.Thinking != "" {
			if err := a.ensureProviderProxyLocked(p.BaseURL, p.Thinking); err != nil {
				slog.Error("providerproxy: failed to start", "error", err)
				env = append(env, "ANTHROPIC_BASE_URL="+p.BaseURL)
			} else {
				env = append(env, "ANTHROPIC_BASE_URL="+a.proxyLocalURL)
				env = append(env, "NO_PROXY=127.0.0.1")
			}
		} else {
			a.stopProviderProxyLocked()
			env = append(env, "ANTHROPIC_BASE_URL="+p.BaseURL)
		}
		if p.APIKey != "" {
			env = append(env, "ANTHROPIC_AUTH_TOKEN="+p.APIKey)
			env = append(env, "ANTHROPIC_API_KEY=")
		}
		if p.Model != "" {
			env = append(env, "ANTHROPIC_MODEL="+p.Model)
		}
	} else {
		a.stopProviderProxyLocked()
		if p.APIKey != "" {
			env = append(env, "ANTHROPIC_API_KEY="+p.APIKey)
		}
	}

	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	slog.Debug("claudecode: providerEnv",
		"provider", p.Name,
		"model", p.Model,
		"env", core.RedactEnv(env))
	return env
}

func (a *Agent) runtimeEnvLocked() []string {
	env := append([]string(nil), a.providerEnvLocked()...)
	env = append(env, a.sessionEnv...)

	if a.routerURL != "" {
		env = append(env, "ANTHROPIC_BASE_URL="+a.routerURL)
		env = append(env, "NO_PROXY=127.0.0.1")
		env = append(env, "DISABLE_TELEMETRY=true")
		env = append(env, "DISABLE_COST_WARNINGS=true")
	}
	if a.routerAPIKey != "" {
		env = append(env, "ANTHROPIC_API_KEY="+a.routerAPIKey)
	}

	if !claudeEnvManagesProviderRouting(env) {
		return env
	}
	return core.MergeEnv(env, []string{"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1"})
}

func claudeEnvManagesProviderRouting(env []string) bool {
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(key))
		if _, ok := claudeProviderManagedEnvVars[upper]; ok {
			return true
		}
		for _, prefix := range claudeProviderManagedEnvPrefixes {
			if strings.HasPrefix(upper, prefix) {
				return true
			}
		}
	}
	return false
}

func (a *Agent) ensureProviderProxyLocked(targetURL, thinkingOverride string) error {
	if a.providerProxy != nil && a.proxyLocalURL != "" {
		return nil
	}
	a.stopProviderProxyLocked()
	proxy, localURL, err := core.NewProviderProxy(targetURL, thinkingOverride)
	if err != nil {
		return err
	}
	a.providerProxy = proxy
	a.proxyLocalURL = localURL
	return nil
}

func (a *Agent) stopProviderProxyLocked() {
	if a.providerProxy != nil {
		a.providerProxy.Close()
		a.providerProxy = nil
		a.proxyLocalURL = ""
	}
}

// summarizeInput produces a short human-readable description of tool input.
func summarizeInput(tool string, input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}

	switch tool {
	case "Read", "Edit", "Write":
		if fp, ok := m["file_path"].(string); ok {
			return fp
		}
	case "Bash":
		if cmd, ok := m["command"].(string); ok {
			return cmd
		}
	case "Grep":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	case "Glob":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
		if p, ok := m["glob_pattern"].(string); ok {
			return p
		}
	}

	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// parseUserQuestions extracts structured questions from AskUserQuestion input.
func parseUserQuestions(input map[string]any) []core.UserQuestion {
	questionsRaw, ok := input["questions"].([]any)
	if !ok || len(questionsRaw) == 0 {
		return nil
	}
	var questions []core.UserQuestion
	for _, qRaw := range questionsRaw {
		qMap, ok := qRaw.(map[string]any)
		if !ok {
			continue
		}
		q := core.UserQuestion{
			Question:    strVal(qMap, "question"),
			Header:      strVal(qMap, "header"),
			MultiSelect: boolVal(qMap, "multiSelect"),
		}
		if optsRaw, ok := qMap["options"].([]any); ok {
			for _, oRaw := range optsRaw {
				oMap, ok := oRaw.(map[string]any)
				if !ok {
					continue
				}
				q.Options = append(q.Options, core.UserQuestionOption{
					Label:       strVal(oMap, "label"),
					Description: strVal(oMap, "description"),
				})
			}
		}
		if q.Question != "" {
			questions = append(questions, q)
		}
	}
	return questions
}

func strVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func boolVal(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

// encodeClaudeProjectKey converts an absolute path to Claude Code's project key format.
// Claude Code encodes paths by:
//  1. Replacing path separators (/ or \) with "-"
//  2. Replacing colons (:) with "-" (Windows drive letters)
//  3. Replacing underscores (_) with "-"
//  4. Replacing spaces and tildes (~) with "-" (common in macOS iCloud paths like
//     "/Users/x/Library/Mobile Documents/com~apple~CloudDocs/...")
//  5. Replacing all non-ASCII characters with "-"
func encodeClaudeProjectKey(absPath string) string {
	// First, normalize to forward slashes for consistent processing
	normalized := strings.ReplaceAll(absPath, "\\", "/")

	// Build the encoded key character by character
	var result strings.Builder
	for _, r := range normalized {
		if r == '/' || r == ':' || r == '_' || r == ' ' || r == '~' {
			result.WriteRune('-')
		} else if r < 128 { // ASCII range (0-127)
			result.WriteRune(r)
		} else {
			// Non-ASCII characters become hyphens
			result.WriteRune('-')
		}
	}
	return result.String()
}

// findProjectDir locates the Claude Code session directory for a given work dir.
// Claude Code stores sessions at ~/.claude/projects/{projectKey}/ where projectKey
// is derived from the absolute path. On Windows, the key format may vary (colon
// handling, slash direction), so we try multiple key candidates and fall back to
// scanning the projects directory.
func findProjectDir(homeDir, absWorkDir string) string {
	projectsBase := filepath.Join(homeDir, ".claude", "projects")

	// Build candidate keys: different ways Claude Code might encode the path.
	// Primary encoding: Claude Code's actual algorithm (non-ASCII → "-")
	candidates := []string{
		encodeClaudeProjectKey(absWorkDir),
		// Legacy candidates for backward compatibility
		strings.ReplaceAll(absWorkDir, string(filepath.Separator), "-"),
		strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(absWorkDir),
		strings.NewReplacer("/", "-", "\\", "-", ":", "-", "_", "-").Replace(absWorkDir),
	}
	// Also try with forward slashes (config might use forward slashes on Windows)
	fwd := strings.ReplaceAll(absWorkDir, "\\", "/")
	candidates = append(candidates, strings.ReplaceAll(fwd, "/", "-"))

	for _, key := range candidates {
		dir := filepath.Join(projectsBase, key)
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}

	// Fallback: scan the projects directory and find a match by
	// comparing the encoded path (handles variations in encoding).
	entries, err := os.ReadDir(projectsBase)
	if err != nil {
		return ""
	}

	// Use the primary encoding for comparison
	encodedWorkDir := encodeClaudeProjectKey(absWorkDir)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Direct match with encoded key
		if entry.Name() == encodedWorkDir {
			return filepath.Join(projectsBase, entry.Name())
		}
		// Case-insensitive match for Windows compatibility
		if strings.EqualFold(entry.Name(), encodedWorkDir) {
			return filepath.Join(projectsBase, entry.Name())
		}
	}

	return ""
}
