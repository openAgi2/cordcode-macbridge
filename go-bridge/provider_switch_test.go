package gobridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func writeProviderSeedConfig(t *testing.T, text string) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte(text), 0o644); err != nil {
		t.Fatalf("WriteFile(config.toml): %v", err)
	}
	return configPath
}

func TestLoadProviderSeedForAgent_ResolvesProviderRefsForMatchingWorkDir(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}

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
	t.Setenv("CC_CONNECT_CONFIG", writeProviderSeedConfig(t, configText))

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
	t.Setenv("CC_CONNECT_CONFIG", writeProviderSeedConfig(t, configText))

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

func TestLoadProviderSeedForAgent_MissingConfigReturnsEmpty(t *testing.T) {
	t.Setenv("CC_CONNECT_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))

	providers, activeProvider, err := loadProviderSeedForAgent("codex", t.TempDir())
	if err != nil {
		t.Fatalf("loadProviderSeedForAgent() error = %v", err)
	}
	if len(providers) != 0 {
		t.Fatalf("provider count = %d, want 0", len(providers))
	}
	if activeProvider != "" {
		t.Fatalf("activeProvider = %q, want empty", activeProvider)
	}
}

func TestLoadProviderSeedForAgent_ExplicitEnvPathOverridesDefault(t *testing.T) {
	homeDir := t.TempDir()
	defaultProject := filepath.Join(homeDir, "default-project")
	explicitProject := filepath.Join(homeDir, "explicit-project")
	if err := os.MkdirAll(filepath.Join(homeDir, ".cc-connect"), 0o755); err != nil {
		t.Fatalf("MkdirAll(default config dir): %v", err)
	}
	if err := os.MkdirAll(defaultProject, 0o755); err != nil {
		t.Fatalf("MkdirAll(defaultProject): %v", err)
	}
	if err := os.MkdirAll(explicitProject, 0o755); err != nil {
		t.Fatalf("MkdirAll(explicitProject): %v", err)
	}

	defaultConfigPath := filepath.Join(homeDir, ".cc-connect", "config.toml")
	if err := os.WriteFile(defaultConfigPath, []byte(providerSeedFixture("default", defaultProject, "default-provider")), 0o644); err != nil {
		t.Fatalf("WriteFile(default config): %v", err)
	}
	explicitConfigPath := writeProviderSeedConfig(t, providerSeedFixture("explicit", explicitProject, "explicit-provider"))

	t.Setenv("HOME", homeDir)
	t.Setenv("CC_CONNECT_CONFIG", explicitConfigPath)

	providers, activeProvider, err := loadProviderSeedForAgent("codex", explicitProject)
	if err != nil {
		t.Fatalf("loadProviderSeedForAgent() error = %v", err)
	}
	if len(providers) != 1 || providers[0].Name != "explicit-provider" {
		t.Fatalf("providers = %#v, want explicit-provider", providers)
	}
	if activeProvider != "explicit-provider" {
		t.Fatalf("activeProvider = %q, want explicit-provider", activeProvider)
	}
}

func TestFindProviderProject_ExactWorkDirPreferredOverBaseDirPrefix(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}

	cfg := &providerSeedConfig{Projects: []providerSeedProject{
		{
			Name:    "prefix",
			Mode:    "multi-workspace",
			BaseDir: root,
			Agent: providerSeedAgent{
				Type:      "codex",
				Providers: []providerSeedProvider{{Name: "prefix-provider"}},
			},
		},
		{
			Name: "exact",
			Agent: providerSeedAgent{
				Type: "codex",
				Options: map[string]any{
					"work_dir": workDir,
				},
				Providers: []providerSeedProvider{{Name: "exact-provider"}},
			},
		},
	}}

	project := findProviderProject(cfg, "codex", workDir)
	if project == nil || project.Name != "exact" {
		t.Fatalf("project = %#v, want exact", project)
	}
}

func TestFindProviderProject_MultiWorkspaceBaseDirPrefixMatches(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}

	cfg := &providerSeedConfig{Projects: []providerSeedProject{{
		Name:    "prefix",
		Mode:    "multi-workspace",
		BaseDir: root,
		Agent: providerSeedAgent{
			Type:      "codex",
			Providers: []providerSeedProvider{{Name: "prefix-provider"}},
		},
	}}}

	project := findProviderProject(cfg, "codex", workDir)
	if project == nil || project.Name != "prefix" {
		t.Fatalf("project = %#v, want prefix", project)
	}
}

func TestFindProviderProject_SkipsMismatchedAgentType(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}

	configText := "[[projects]]\n" +
		"name = \"claude-project\"\n" +
		"\n" +
		"[projects.agent]\n" +
		"type = \"claude\"\n" +
		"\n" +
		"[projects.agent.options]\n" +
		"work_dir = \"" + workDir + "\"\n" +
		"provider = \"anthropic\"\n" +
		"\n" +
		"[[projects.agent.providers]]\n" +
		"name = \"anthropic\"\n"
	t.Setenv("CC_CONNECT_CONFIG", writeProviderSeedConfig(t, configText))

	providers, activeProvider, err := loadProviderSeedForAgent("codex", workDir)
	if err != nil {
		t.Fatalf("loadProviderSeedForAgent() error = %v", err)
	}
	if len(providers) != 0 || activeProvider != "" {
		t.Fatalf("providers = %#v activeProvider = %q, want no match", providers, activeProvider)
	}
}

func TestLoadProviderSeedForAgent_OptionsStringDecodingIgnoresNonStrings(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}

	configText := "[[projects]]\n" +
		"name = \"mixed-options\"\n" +
		"mode = \"multi-workspace\"\n" +
		"base_dir = \"" + root + "\"\n" +
		"\n" +
		"[projects.agent]\n" +
		"type = \"codex\"\n" +
		"\n" +
		"[projects.agent.options]\n" +
		"provider = 123\n" +
		"work_dir = true\n" +
		"some_flag = false\n" +
		"\n" +
		"[[projects.agent.providers]]\n" +
		"name = \"openai\"\n"
	t.Setenv("CC_CONNECT_CONFIG", writeProviderSeedConfig(t, configText))

	providers, activeProvider, err := loadProviderSeedForAgent("codex", workDir)
	if err != nil {
		t.Fatalf("loadProviderSeedForAgent() error = %v", err)
	}
	if len(providers) != 1 || providers[0].Name != "openai" {
		t.Fatalf("providers = %#v, want openai from base_dir match", providers)
	}
	if activeProvider != "" {
		t.Fatalf("activeProvider = %q, want empty for non-string provider option", activeProvider)
	}
}

func TestLoadProviderSeedForAgent_MapsProviderFields(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(workDir): %v", err)
	}

	configText := "[[projects]]\n" +
		"name = \"codex-project\"\n" +
		"\n" +
		"[projects.agent]\n" +
		"type = \"codex\"\n" +
		"\n" +
		"[projects.agent.options]\n" +
		"work_dir = \"" + workDir + "\"\n" +
		"provider = \"openai\"\n" +
		"\n" +
		"[[projects.agent.providers]]\n" +
		"name = \"openai\"\n" +
		"api_key = \"sk-openai\"\n" +
		"base_url = \"https://api.openai.com/v1\"\n" +
		"model = \"gpt-5\"\n" +
		"thinking = \"medium\"\n" +
		"\n" +
		"[projects.agent.providers.env]\n" +
		"OPENAI_API_KEY = \"sk-env\"\n" +
		"\n" +
		"[[projects.agent.providers.models]]\n" +
		"model = \"gpt-5\"\n" +
		"alias = \"default\"\n" +
		"\n" +
		"[projects.agent.providers.codex]\n" +
		"wire_api = \"responses\"\n" +
		"\n" +
		"[projects.agent.providers.codex.http_headers]\n" +
		"OpenAI-Organization = \"org-1\"\n"
	t.Setenv("CC_CONNECT_CONFIG", writeProviderSeedConfig(t, configText))

	providers, activeProvider, err := loadProviderSeedForAgent("codex", workDir)
	if err != nil {
		t.Fatalf("loadProviderSeedForAgent() error = %v", err)
	}
	if activeProvider != "openai" {
		t.Fatalf("activeProvider = %q, want openai", activeProvider)
	}
	if len(providers) != 1 {
		t.Fatalf("provider count = %d, want 1", len(providers))
	}
	provider := providers[0]
	if provider.Name != "openai" || provider.APIKey != "sk-openai" || provider.BaseURL != "https://api.openai.com/v1" || provider.Model != "gpt-5" || provider.Thinking != "medium" {
		t.Fatalf("provider scalar mapping = %#v", provider)
	}
	if got := provider.Env["OPENAI_API_KEY"]; got != "sk-env" {
		t.Fatalf("provider env OPENAI_API_KEY = %q, want sk-env", got)
	}
	if len(provider.Models) != 1 || provider.Models[0].Name != "gpt-5" || provider.Models[0].Alias != "default" {
		t.Fatalf("provider models = %#v, want gpt-5/default", provider.Models)
	}
	if provider.CodexWireAPI != "responses" {
		t.Fatalf("provider CodexWireAPI = %q, want responses", provider.CodexWireAPI)
	}
	if got := provider.CodexHTTPHeaders["OpenAI-Organization"]; got != "org-1" {
		t.Fatalf("provider CodexHTTPHeaders = %#v, want org-1", provider.CodexHTTPHeaders)
	}
}

func TestProviderSeedDoesNotImportLegacyConfigPackage(t *testing.T) {
	for _, path := range []string{
		"agent/claudecode/provider_integration_test.go",
		"agent/codex/provider_switch_test.go",
		"agent/providerseedtest/provider_seed.go",
		"go-bridge/provider_switch.go",
		"go-bridge/provider_seed_config.go",
	} {
		data, err := os.ReadFile(filepath.Join("..", path))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		if strings.Contains(string(data), "github.com/openAgi2/cordcode-macbridge/config") {
			t.Fatalf("%s imports legacy config package", path)
		}
	}
}

func providerSeedFixture(projectName, workDir, providerName string) string {
	return "[[projects]]\n" +
		"name = \"" + projectName + "\"\n" +
		"\n" +
		"[projects.agent]\n" +
		"type = \"codex\"\n" +
		"\n" +
		"[projects.agent.options]\n" +
		"work_dir = \"" + workDir + "\"\n" +
		"provider = \"" + providerName + "\"\n" +
		"\n" +
		"[[projects.agent.providers]]\n" +
		"name = \"" + providerName + "\"\n"
}
