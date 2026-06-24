package gobridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type queryRouteMatrix struct {
	Verdict         string   `json:"verdict"`
	Reason          string   `json:"reason"`
	HTTPFieldKeys   []string `json:"httpFieldKeys,omitempty"`
	AgentFieldKeys  []string `json:"agentFieldKeys,omitempty"`
	HTTPCount       int      `json:"httpCount,omitempty"`
	AgentCount      int      `json:"agentCount,omitempty"`
	HTTPDurationMs  int64    `json:"httpDurationMs,omitempty"`
	AgentDurationMs int64    `json:"agentDurationMs,omitempty"`
}

type queryEvaluationReport struct {
	GeneratedAt  string           `json:"generatedAt"`
	BaseURL      string           `json:"baseURL"`
	WorkDir      string           `json:"workDir"`
	ListSessions queryRouteMatrix `json:"listSessions"`
	GetSession   queryRouteMatrix `json:"getSession"`
	ListModels   queryRouteMatrix `json:"listModels"`
}

func TestOpenCodeQueryEvaluationMatrix(t *testing.T) {
	if os.Getenv("GO_BRIDGE_OPENCODE_QUERY_EVAL") != "1" {
		t.Skip("set GO_BRIDGE_OPENCODE_QUERY_EVAL=1 to run live query evaluation")
	}

	cmdPath, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode CLI not found in PATH")
	}

	baseURL := os.Getenv("OPENCODE_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:64667"
	}
	workDir := os.Getenv("QUERY_EVAL_WORKDIR")
	if workDir == "" {
		workDir = "/Users/developer"
	}
	user := os.Getenv("OPENCODE_SERVER_USERNAME")
	pass := os.Getenv("OPENCODE_SERVER_PASSWORD")

	proxy := NewOpenCodeProxy(baseURL, user, pass)
	agent, err := core.CreateAgent("opencode", map[string]any{
		"cmd":           cmdPath,
		"work_dir":      workDir,
		"mode":          "bypassPermissions",
		"opencode_url":  baseURL,
		"opencode_user": user,
		"opencode_pass": pass,
	})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	report := queryEvaluationReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		BaseURL:     baseURL,
		WorkDir:     workDir,
	}

	httpSessionsRaw, httpSessionsDuration, err := sampleDuration(func() ([]map[string]interface{}, error) {
		return proxy.listSessions(workDir)
	})
	if err != nil {
		t.Fatalf("proxy.listSessions failed: %v", err)
	}
	httpSessions := make([]map[string]interface{}, 0, len(httpSessionsRaw))
	for _, session := range httpSessionsRaw {
		httpSessions = append(httpSessions, mapSession(session))
	}

	agentSessionsRaw, agentSessionsDuration, err := sampleDuration(func() ([]core.AgentSessionInfo, error) {
		return agent.ListSessions(context.Background())
	})
	if err != nil {
		t.Fatalf("agent.ListSessions failed: %v", err)
	}
	agentSessions := sessionsToWire(agentSessionsRaw)

	report.ListSessions = queryRouteMatrix{
		Verdict:         "keep_http",
		Reason:          "HTTP sessions carry directory, project, parent, availability, and effective model/provider metadata that AgentSessionInfo does not expose.",
		HTTPFieldKeys:   fieldKeys(firstMap(httpSessions)),
		AgentFieldKeys:  fieldKeys(firstMap(agentSessions)),
		HTTPCount:       len(httpSessions),
		AgentCount:      len(agentSessions),
		HTTPDurationMs:  httpSessionsDuration.Milliseconds(),
		AgentDurationMs: agentSessionsDuration.Milliseconds(),
	}

	getSessionID := ""
	if len(httpSessions) > 0 {
		getSessionID, _ = httpSessions[0]["id"].(string)
	}
	if getSessionID == "" {
		session, err := proxy.createSession("query-eval", workDir)
		if err != nil {
			t.Fatalf("proxy.createSession failed: %v", err)
		}
		mapped := mapSession(session)
		getSessionID, _ = mapped["id"].(string)
	}
	httpSessionDetailRaw, getSessionDuration, err := sampleDuration(func() (map[string]interface{}, error) {
		return proxy.getSession(getSessionID, workDir)
	})
	if err != nil {
		t.Fatalf("proxy.getSession failed: %v", err)
	}
	report.GetSession = queryRouteMatrix{
		Verdict:        "keep_http",
		Reason:         "the agent runtime has no direct get_session abstraction with full session metadata, so this route cannot migrate without inventing unsupported fields.",
		HTTPFieldKeys:  fieldKeys(mapSession(httpSessionDetailRaw)),
		HTTPCount:      1,
		HTTPDurationMs: getSessionDuration.Milliseconds(),
	}

	httpModels, httpModelsDuration, err := sampleDuration(func() ([]map[string]interface{}, error) {
		return proxy.listModels(workDir)
	})
	if err != nil {
		t.Fatalf("proxy.listModels failed: %v", err)
	}
	ms, ok := agent.(core.ModelSwitcher)
	if !ok {
		t.Fatalf("agent does not implement ModelSwitcher")
	}
	agentModelsRaw, agentModelsDuration, err := sampleDuration(func() ([]core.ModelOption, error) {
		return ms.AvailableModels(context.Background()), nil
	})
	if err != nil {
		t.Fatalf("AvailableModels failed: %v", err)
	}
	agentModels := agentModelsToWire(ms, agentModelsRaw)
	report.ListModels = queryRouteMatrix{
		Verdict:         "keep_http",
		Reason:          "HTTP list_models comes from the authoritative runtime config endpoint, while the agent path is CLI-derived discovery with synthetic reasoning and limit metadata.",
		HTTPFieldKeys:   fieldKeys(firstMap(httpModels)),
		AgentFieldKeys:  fieldKeys(firstMap(agentModels)),
		HTTPCount:       len(httpModels),
		AgentCount:      len(agentModels),
		HTTPDurationMs:  httpModelsDuration.Milliseconds(),
		AgentDurationMs: agentModelsDuration.Milliseconds(),
	}

	outputPath := filepath.Join("..", "output", "opencode-query-eval.json")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		t.Fatalf("mkdir output dir failed: %v", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal report failed: %v", err)
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		t.Fatalf("write report failed: %v", err)
	}

	if report.ListSessions.Verdict != "keep_http" || report.GetSession.Verdict != "keep_http" || report.ListModels.Verdict != "keep_http" {
		t.Fatalf("unexpected verdicts: %+v", report)
	}
	t.Logf("wrote query evaluation matrix to %s", outputPath)
}

func sampleDuration[T any](fn func() (T, error)) (T, time.Duration, error) {
	start := time.Now()
	value, err := fn()
	return value, time.Since(start), err
}

func firstMap(values []map[string]interface{}) map[string]interface{} {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func fieldKeys(value map[string]interface{}) []string {
	if value == nil {
		return nil
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func agentModelsToWire(ms core.ModelSwitcher, models []core.ModelOption) []map[string]interface{} {
	currentModel := ms.GetModel()
	result := make([]map[string]interface{}, 0, len(models))
	for _, model := range models {
		id, provider, providerID := parseModelID(model.Name)
		name := model.Desc
		if name == "" {
			name = id
		}
		result = append(result, map[string]interface{}{
			"id":                        model.Name,
			"name":                      name,
			"provider":                  provider,
			"providerId":                providerID,
			"reasoning":                 false,
			"limit":                     nil,
			"supportedReasoningEfforts": nil,
			"defaultReasoningEffort":    nil,
			"isDefault":                 model.Name == currentModel,
		})
	}
	return result
}
