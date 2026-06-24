package gobridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestLoadProviderSeedForAgent_ResolvesProviderRefsForMatchingWorkDir(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	configText := "[[providers]]\n" +
		"name = \"openai\"\n" +
		"api_key = \"sk-openai\"\n" +
		"base_url = \"https://api.openai.com/v1\"\n" +
		"model = \"gpt-5\"\n" +
		"\n" +
		"[[projects]]\n" +
		"name = \"codex-project\"\n" +
		"\n" +
		"[projects.agent]\n" +
		"type = \"codex\"\n" +
		"provider_refs = [\"openai\"]\n" +
		"\n" +
		"[projects.agent.options]\n" +
		"work_dir = \"" + workDir + "\"\n" +
		"provider = \"openai\"\n" +
		"\n" +
		"[[projects.platforms]]\n" +
		"type = \"telegram\"\n"
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml): %v", err)
	}
	t.Setenv("CC_CONNECT_CONFIG", configPath)

	providers, activeProvider, err := loadProviderSeedForAgent("codex", workDir)
	if err != nil {
		t.Fatalf("loadProviderSeedForAgent() error = %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("provider count = %d, want 1", len(providers))
	}
	if providers[0].Name != "openai" {
		t.Fatalf("providers[0].Name = %q, want openai", providers[0].Name)
	}
	if providers[0].BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("providers[0].BaseURL = %q, want openai base url", providers[0].BaseURL)
	}
	if activeProvider != "openai" {
		t.Fatalf("activeProvider = %q, want openai", activeProvider)
	}
}

func TestApplyProviderSeed_SetsProvidersAndActiveProvider(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	configText := "[[projects]]\n" +
		"name = \"codex-project\"\n" +
		"\n" +
		"[projects.agent]\n" +
		"type = \"codex\"\n" +
		"\n" +
		"[projects.agent.options]\n" +
		"work_dir = \"" + workDir + "\"\n" +
		"provider = \"azure\"\n" +
		"\n" +
		"[[projects.agent.providers]]\n" +
		"name = \"openai\"\n" +
		"api_key = \"sk-openai\"\n" +
		"\n" +
		"[[projects.agent.providers]]\n" +
		"name = \"azure\"\n" +
		"api_key = \"sk-azure\"\n" +
		"base_url = \"https://azure.example.com/v1\"\n" +
		"\n" +
		"[[projects.platforms]]\n" +
		"type = \"telegram\"\n"
	if err := os.WriteFile(configPath, []byte(configText), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml): %v", err)
	}
	t.Setenv("CC_CONNECT_CONFIG", configPath)

	agent := &fakeAgent{name: "codex"}
	if err := applyProviderSeed(agent, "codex", workDir); err != nil {
		t.Fatalf("applyProviderSeed() error = %v", err)
	}
	if len(agent.providers) != 2 {
		t.Fatalf("provider count = %d, want 2", len(agent.providers))
	}
	active := agent.GetActiveProvider()
	if active == nil || active.Name != "azure" {
		t.Fatalf("active provider = %#v, want azure", active)
	}
	if active.BaseURL != "https://azure.example.com/v1" {
		t.Fatalf("active provider baseURL = %q, want azure base url", active.BaseURL)
	}

	var _ core.ProviderSwitcher = agent
}
