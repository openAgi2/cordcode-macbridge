package gobridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	ccopencode "github.com/openAgi2/cordcode-macbridge/agent/opencode"
	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestOpencodeAgentGetRichSessionHistoryUsesConfiguredHTTPRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Basic dTpw" {
			t.Fatalf("authorization = %q, want Basic dTpw", got)
		}
		if got := r.Header.Get("x-opencode-directory"); got != "/tmp/opencode-project" {
			t.Fatalf("x-opencode-directory = %q, want /tmp/opencode-project", got)
		}
		if r.URL.Path != "/session/ses_1/message" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"info":{"id":"msg_1","role":"assistant","agent":"build","modelID":"gpt-5-mini","providerID":"github-copilot","time":{"created":1710000000000}},"parts":[
				{"type":"reasoning","text":"think first"},
				{"type":"text","text":"final answer"},
				{"type":"tool","tool":{"id":"tool-1","toolName":"bash","state":{"status":"completed","output":"done","durationMs":12}}}
			]}
		]`))
	}))
	defer server.Close()

	agent, err := ccopencode.New(map[string]any{
		"cmd":           os.Args[0],
		"work_dir":      "/tmp/opencode-project",
		"opencode_url":  server.URL,
		"opencode_user": "u",
		"opencode_pass": "p",
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	rhp, ok := agent.(core.RichHistoryProvider)
	if !ok {
		t.Fatal("agent does not implement RichHistoryProvider")
	}

	entries, err := rhp.GetRichSessionHistory(context.Background(), "ses_1", 10)
	if err != nil {
		t.Fatalf("GetRichSessionHistory failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Content != "final answer" {
		t.Fatalf("content = %q, want final answer", entry.Content)
	}
	if entry.Thinking != "think first" {
		t.Fatalf("thinking = %q, want think first", entry.Thinking)
	}
	if len(entry.Parts) != 3 {
		t.Fatalf("parts length = %d, want 3", len(entry.Parts))
	}
	if len(entry.Steps) != 1 {
		t.Fatalf("steps length = %d, want 1", len(entry.Steps))
	}
	if entry.AgentName != "build" {
		t.Fatalf("agentName = %q, want build", entry.AgentName)
	}
	if entry.ModelID != "gpt-5-mini" {
		t.Fatalf("modelID = %q, want gpt-5-mini", entry.ModelID)
	}
}

func TestOpencodeAgentFetchTodosUsesConfiguredHTTPRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/ses_1/todo" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"content":"implement provider","status":"in_progress","priority":"high"},
			{"text":"regression proof","status":"pending"}
		]`))
	}))
	defer server.Close()

	agent, err := ccopencode.New(map[string]any{
		"cmd":          os.Args[0],
		"work_dir":     "/tmp/opencode-project",
		"opencode_url": server.URL,
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	provider, ok := agent.(core.TodoProvider)
	if !ok {
		t.Fatal("agent does not implement TodoProvider")
	}

	todos, err := provider.FetchTodos(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("FetchTodos failed: %v", err)
	}
	if len(todos) != 2 {
		t.Fatalf("todo count = %d, want 2", len(todos))
	}
	if todos[0].Content != "implement provider" || todos[0].Priority != "high" {
		t.Fatalf("first todo = %#v, want content/priority mapped", todos[0])
	}
	if todos[1].Content != "regression proof" || todos[1].Priority != "normal" {
		t.Fatalf("second todo = %#v, want text fallback and default priority", todos[1])
	}
}

func TestOpencodeAgentListAgentsUsesConfiguredHTTPRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[
			{"name":"build","mode":"primary","description":"Build agent"},
			{"id":"review","hidden":true}
		]`))
	}))
	defer server.Close()

	agent, err := ccopencode.New(map[string]any{
		"cmd":          os.Args[0],
		"work_dir":     "/tmp/opencode-project",
		"opencode_url": server.URL,
	})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	lister, ok := agent.(core.AgentLister)
	if !ok {
		t.Fatal("agent does not implement AgentLister")
	}

	agents, err := lister.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("agent count = %d, want 2", len(agents))
	}
	if agents[0].Name != "build" || agents[0].Mode != "primary" {
		t.Fatalf("first agent = %#v, want build/primary", agents[0])
	}
	if agents[1].Name != "review" || agents[1].Mode != "primary" || !agents[1].Hidden {
		t.Fatalf("second agent = %#v, want id fallback and default mode", agents[1])
	}
}
