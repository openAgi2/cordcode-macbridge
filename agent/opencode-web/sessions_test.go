package opencodeweb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// recordingServe is the data-plane fake: Basic Auth on everything (managed
// serve style), canned JSON per path, and per-request recording of the
// x-opencode-directory header for the M2-1/坑 5 assertions.
type recordingServe struct {
	mu        sync.Mutex
	responses map[string]string
	requests  []recordedRequest
}

type recordedRequest struct {
	Method    string
	Path      string
	Directory string
	Authed    bool
}

func (s *recordingServe) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		_, pass, ok := r.BasicAuth()
		s.requests = append(s.requests, recordedRequest{
			Method:    r.Method,
			Path:      r.URL.Path,
			Directory: r.Header.Get("x-opencode-directory"),
			Authed:    ok && pass == "pw",
		})
		if !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, found := s.responses[r.URL.Path]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

func (s *recordingServe) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

func (s *recordingServe) requestsFor(path string) []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []recordedRequest
	for _, req := range s.requests {
		if req.Path == path {
			out = append(out, req)
		}
	}
	return out
}

// newDataAgent boots an Agent against a recording serve with sensible
// health/list routes so clientFor succeeds on generation 1.18.
func newDataAgent(t *testing.T, responses map[string]string, workDir string) (*Agent, *recordingServe) {
	t.Helper()
	if _, ok := responses["/global/health"]; !ok {
		responses["/global/health"] = `{"healthy":true}`
	}
	if _, ok := responses["/session"]; !ok {
		responses["/session"] = `[]`
	}
	s := &recordingServe{responses: responses}
	base := s.start(t)
	a, err := New(map[string]any{
		"work_dir":          workDir,
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a.(*Agent), s
}

func TestListSessionsMapsFieldsAndDirectoryHeader(t *testing.T) {
	updated := float64(time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).UnixMilli())
	payload, _ := json.Marshal([]map[string]any{{
		"id":        "ses_1",
		"title":     "Fix login",
		"directory": "/tmp/proj",
		"time":      map[string]any{"created": updated - 1000, "updated": updated},
		"model":     map[string]any{"id": "glm-4.7", "providerID": "zhipuai-coding-plan"},
		"tokens":    map[string]any{"input": 0, "output": 0},
		"parentID":  "ses_parent_unknown_field",
		"unknown":   "ignored",
	}})
	agent, serve := newDataAgent(t, map[string]string{"/session": string(payload)}, "/tmp/proj")

	sessions, err := agent.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("len = %d, want 1", len(sessions))
	}
	got := sessions[0]
	if got.ID != "ses_1" || got.Summary != "Fix login" || got.Directory != "/tmp/proj" {
		t.Fatalf("mapped row %+v", got)
	}
	if got.ModelID != "glm-4.7" || got.ProviderID != "zhipuai-coding-plan" {
		t.Fatalf("model mapping %+v", got)
	}
	if !got.ModifiedAt.Equal(time.UnixMilli(int64(updated)).UTC()) {
		t.Fatalf("ModifiedAt = %v", got.ModifiedAt)
	}
	reqs := serve.requestsFor("/session")
	// The probe's shape check also GETs /session (no directory header); the
	// actual list call must carry the work dir header.
	listReq := false
	for _, req := range reqs {
		if req.Directory == "/tmp/proj" && req.Authed {
			listReq = true
		}
	}
	if !listReq {
		t.Fatalf("list request must carry work dir header + auth, got %+v", reqs)
	}
}

func TestListSessionsV2EnvelopeAndStableOrder(t *testing.T) {
	older := float64(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC).UnixMilli())
	newer := float64(time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC).UnixMilli())
	s := &recordingServe{responses: map[string]string{
		"/global/health": `{"healthy":true}`,
		"/api/health":    `{"healthy":true}`,
		"/api/session": `{"data":[
			{"id":"ses_old","title":"old","time":{"updated":` + jsonNumber(older) + `}},
			{"id":"ses_new","title":"new","time":{"updated":` + jsonNumber(newer) + `}}
		],"cursor":null}`,
	}}
	base := s.start(t)
	a, _ := New(map[string]any{
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	agent := a.(*Agent)
	// Shape arbiter: /session missing (404) + /api/session envelope → v2.
	if _, err := agent.clientFor(context.Background()); err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	sessions, err := agent.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 || sessions[0].ID != "ses_new" || sessions[1].ID != "ses_old" {
		t.Fatalf("order/rows = %+v", sessions)
	}
}

func jsonNumber(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestGetRichSessionHistoryMapsParts(t *testing.T) {
	messages := `[
	{"info":{"id":"msg_1","role":"user","time":{"created":1000}},
	 "parts":[{"type":"text","text":"hello"}]},
	{"info":{"id":"msg_2","role":"assistant","modelID":"glm-4.7","providerID":"zhipuai-coding-plan","agent":"build","time":{"created":2000}},
	 "parts":[
		{"type":"reasoning","text":"thinking…"},
		{"type":"text","text":"answer"},
		{"type":"tool","tool":{"id":"pt_1","toolName":"read","state":{"status":"completed","output":"file contents","durationMs":12}}},
		{"type":"file","id":"f1","mime":null,"url":"u","filename":"a.txt"}
	 ]}]`
	agent, serve := newDataAgent(t, map[string]string{
		"/session/ses_x/message": messages,
	}, "/tmp/proj")

	rich, err := agent.GetRichSessionHistory(context.Background(), "ses_x", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rich) != 2 {
		t.Fatalf("len = %d", len(rich))
	}
	user, assistant := rich[0], rich[1]
	if user.Role != "user" || user.Content != "hello" {
		t.Fatalf("user entry %+v", user)
	}
	if assistant.Role != "assistant" || assistant.Content != "answer" || assistant.Thinking != "thinking…" {
		t.Fatalf("assistant entry %+v", assistant)
	}
	if assistant.ModelID != "glm-4.7" || assistant.ProviderID != "zhipuai-coding-plan" || assistant.AgentName != "build" {
		t.Fatalf("assistant info mapping %+v", assistant)
	}
	if len(assistant.Steps) != 1 {
		t.Fatalf("steps = %+v", assistant.Steps)
	}
	step := assistant.Steps[0]
	if step["id"] != "pt_1" || step["toolName"] != "read" || step["status"] != "completed" {
		t.Fatalf("step = %+v", step)
	}
	out := step["output"].(map[string]any)
	if out["kind"] != "inline" || out["text"] != "file contents" {
		t.Fatalf("tool output = %+v", out)
	}
	reqs := serve.requestsFor("/session/ses_x/message")
	if len(reqs) == 0 || reqs[0].Directory != "/tmp/proj" {
		t.Fatalf("message read must carry directory header, got %+v", reqs)
	}
	// HistoryProvider folding.
	plain, err := agent.GetSessionHistory(context.Background(), "ses_x", 0)
	if err != nil || len(plain) != 2 || plain[1].Content != "answer" {
		t.Fatalf("plain history = %+v err=%v", plain, err)
	}
}

func TestGetRichSessionHistoryLimitTrimsTail(t *testing.T) {
	messages := `[{"info":{"role":"user"},"parts":[]},{"info":{"role":"assistant"},"parts":[]},{"info":{"role":"user"},"parts":[]}]`
	agent, _ := newDataAgent(t, map[string]string{"/session/ses_x/message": messages}, "/tmp")
	rich, err := agent.GetRichSessionHistory(context.Background(), "ses_x", 2)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rich) != 2 || rich[0].Role != "assistant" {
		t.Fatalf("limit must keep the tail, got %+v", rich)
	}
}

// lastAssistantPayload mirrors the live verified shape (design §3.6/S1):
// message-level info.tokens carries `total`; the session top level does not.
func lastAssistantPayload() string {
	return `[
	{"info":{"id":"msg_1","role":"user","time":{"created":1000}},"parts":[{"type":"text","text":"hi"}]},
	{"info":{"id":"msg_2","role":"assistant","modelID":"glm-4.7","providerID":"zhipuai-coding-plan","time":{"created":2000},
		"tokens":{"input":10000,"output":2000,"reasoning":500,"total":18457,"cache":{"read":5000,"write":957}}},
		"parts":[{"type":"text","text":"done"}]}
	]`
}

func TestUsageZeroTopLevelStillComputesFromMessages(t *testing.T) {
	// The session detail route answers with all-zero top-level tokens (the
	// live 99/100 shape) — usage must still resolve from messages.
	agent, _ := newDataAgent(t, map[string]string{
		"/session/ses_x":         `{"id":"ses_x","tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"model":null}`,
		"/session/ses_x/message": lastAssistantPayload(),
		"/provider":              `{"zhipuai-coding-plan":{"npm":{"id":"zhipuai-coding-plan","models":{"glm-4.7":{"id":"glm-4.7","limit":{"context":128000}}}}}}`,
	}, "/tmp/proj")

	usage, err := agent.GetSessionContextUsage(context.Background(), "ses_x")
	if err != nil {
		t.Fatalf("usage err: %v", err)
	}
	if usage == nil {
		t.Fatal("usage must resolve from message-level tokens")
	}
	// total = 10000+2000+500+5000+957 = 18457
	if usage.UsedTokens != 18457 || usage.TotalTokens != 18457 {
		t.Fatalf("used = %d, want 18457", usage.UsedTokens)
	}
	if usage.InputTokens != 10000 || usage.OutputTokens != 2000 || usage.ReasoningOutputTokens != 500 {
		t.Fatalf("parts mapping %+v", usage)
	}
	if usage.CacheReadTokens != 5000 || usage.CachedInputTokens != 5000 || usage.CacheWriteTokens != 957 {
		t.Fatalf("cache mapping %+v", usage)
	}
	if usage.ContextWindow != 128000 {
		t.Fatalf("window = %d, want 128000", usage.ContextWindow)
	}
}

func TestUsageNoWindowReturnsNilNotFabricated(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{
		"/session/ses_x/message": lastAssistantPayload(),
		"/provider":              `{"someprov":{"models":{"glm-4.7":{"id":"glm-4.7"}}}}`,
	}, "/tmp/proj")
	usage, err := agent.GetSessionContextUsage(context.Background(), "ses_x")
	if err != nil {
		t.Fatalf("usage err: %v", err)
	}
	if usage != nil {
		t.Fatalf("no window must yield nil, got %+v", usage)
	}
}

func TestUsageNoAssistantTokensReturnsNil(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{
		"/session/ses_x/message": `[{"info":{"role":"assistant"},"parts":[]}]`,
		"/provider":              `{"p":{"models":{"m":{"id":"m","limit":{"context":1000}}}}}`,
	}, "/tmp")
	usage, err := agent.GetSessionContextUsage(context.Background(), "ses_x")
	if err != nil || usage != nil {
		t.Fatalf("usage = %+v err = %v, want nil/nil", usage, err)
	}
}

func TestAvailableModelsQualifiedNamesAndWindows(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{
		"/provider": `{"zhipuai-coding-plan":{"npm":{"id":"zhipuai-coding-plan","name":"Zhipu","models":{
			"glm-4.7":{"id":"glm-4.7","name":"GLM 4.7","limit":{"context":128000}},
			"glm-4.6":{"id":"glm-4.6","limit":{"context":64000}}}}}}`,
	}, "/tmp")
	models := agent.AvailableModels(context.Background())
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].Name != "zhipuai-coding-plan/glm-4.6" || models[1].Name != "zhipuai-coding-plan/glm-4.7" {
		t.Fatalf("qualified names = %+v", models)
	}
	if models[1].Desc != "GLM 4.7" {
		t.Fatalf("desc = %q", models[1].Desc)
	}
	// Window map shared with the usage formula (qualified + bare keys).
	windows, ok := agent.cachedModelWindows()
	if !ok {
		t.Fatal("window cache should be primed by AvailableModels")
	}
	if windows["zhipuai-coding-plan/glm-4.7"] != 128000 || windows["glm-4.7"] != 128000 {
		t.Fatalf("windows = %+v", windows)
	}
	agent.SetModel("zhipuai-coding-plan/glm-4.7")
	if agent.GetModel() != "zhipuai-coding-plan/glm-4.7" {
		t.Fatalf("GetModel = %q", agent.GetModel())
	}
}

func TestListProjectsMapsWorktree(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{
		"/project": `[{"id":"prj_1","worktree":"/Users/x/proj","vcs":{"branch":"main"},"time":{"created":1},"sandboxes":[]},{"id":"prj_2"}]`,
	}, "/tmp")
	projects, err := agent.ListProjectSuggestions(context.Background())
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("entries without worktree dropped, got %+v", projects)
	}
	if projects[0].Directory != "/Users/x/proj" || projects[0].Name != "proj" || projects[0].ID != "prj_1" {
		t.Fatalf("mapping = %+v", projects[0])
	}
}

func TestListAgentsMapsAndEmptyIsLegal(t *testing.T) {
	agent, _ := newDataAgent(t, map[string]string{
		"/agent": `[{"id":"build","mode":"primary","description":"general coding","hidden":false,"native":true},{"name":"plan","mode":"plan"}]`,
	}, "/tmp")
	agents, err := agent.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("agents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("agents = %+v", agents)
	}
	if agents[0].Name != "build" || agents[0].Mode != "primary" || !agents[0].Native {
		t.Fatalf("first = %+v", agents[0])
	}
	if agents[1].Name != "plan" || agents[1].Mode != "plan" {
		t.Fatalf("second = %+v", agents[1])
	}

	empty, serve2 := newDataAgent(t, map[string]string{"/agent": `[]`}, "/tmp")
	got, err := empty.ListAgents(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("empty array legal, got %+v err=%v", got, err)
	}
	_ = serve2
}

func TestProjectsNotSupportedOnV2(t *testing.T) {
	s := &recordingServe{responses: map[string]string{
		"/global/health": `{"healthy":true}`,
		"/api/health":    `{"healthy":true}`,
		"/api/session":   `{"data":[]}`,
	}}
	base := s.start(t)
	a, _ := New(map[string]any{
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	agent := a.(*Agent)
	if _, err := agent.clientFor(context.Background()); err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	if _, err := agent.ListProjectSuggestions(context.Background()); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("v2 project list must be not_supported (no /api/location misuse), got %v", err)
	}
}

var _ = core.ContextUsage{}
