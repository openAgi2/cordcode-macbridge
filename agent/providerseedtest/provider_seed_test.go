package providerseedtest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesProviderRefsAndAgentOverrides(t *testing.T) {
	t.Setenv("B2_PROVIDER_KEY", "sk-env")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
[[providers]]
name = "shared"
api_key = "${B2_PROVIDER_KEY}"
base_url = "https://default.example/v1"
model = "default-model"
agent_types = ["codex", "claudecode"]
endpoints = { codex = "https://codex.example/v1" }
agent_models = { codex = "gpt-codex" }
agent_model_lists = { codex = [{ model = "gpt-codex", alias = "default" }] }

[providers.env]
OPENAI_API_KEY = "${B2_PROVIDER_KEY}"

[providers.codex]
wire_api = "responses"
http_headers = { OpenAI-Organization = "org-1" }

[[providers]]
name = "claude-only"
api_key = "sk-claude"
agent_types = ["claudecode"]

[[projects]]
name = "codex-project"
base_dir = "/tmp/project"

[projects.agent]
type = "codex"
provider_refs = ["shared", "claude-only"]

[projects.agent.options]
work_dir = "/tmp/project"
codex_home = "/tmp/codex-home"
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("project count = %d, want 1", len(cfg.Projects))
	}
	providers := cfg.Projects[0].Agent.Providers
	if len(providers) != 1 {
		t.Fatalf("provider count = %d, want 1: %#v", len(providers), providers)
	}
	provider := providers[0]
	if provider.Name != "shared" {
		t.Fatalf("provider name = %q, want shared", provider.Name)
	}
	if provider.APIKey != "sk-env" || provider.Env["OPENAI_API_KEY"] != "sk-env" {
		t.Fatalf("env expansion failed: %#v", provider)
	}
	coreProvider := provider.CoreConfig()
	if coreProvider.BaseURL != "https://codex.example/v1" {
		t.Fatalf("BaseURL = %q, want codex endpoint", coreProvider.BaseURL)
	}
	if coreProvider.Model != "gpt-codex" {
		t.Fatalf("Model = %q, want gpt-codex", coreProvider.Model)
	}
	if len(coreProvider.Models) != 1 || coreProvider.Models[0].Name != "gpt-codex" || coreProvider.Models[0].Alias != "default" {
		t.Fatalf("Models = %#v, want gpt-codex/default", coreProvider.Models)
	}
	if coreProvider.CodexWireAPI != "responses" {
		t.Fatalf("CodexWireAPI = %q, want responses", coreProvider.CodexWireAPI)
	}
	if got := coreProvider.CodexHTTPHeaders["OpenAI-Organization"]; got != "org-1" {
		t.Fatalf("CodexHTTPHeaders = %#v, want org-1", coreProvider.CodexHTTPHeaders)
	}
}
