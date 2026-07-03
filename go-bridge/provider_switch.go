package gobridge

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func applyProviderSeed(agent core.Agent, agentType, workDir string) error {
	switcher, ok := agent.(core.ProviderSwitcher)
	if !ok {
		return nil
	}

	providers, activeProvider, err := loadProviderSeedForAgent(agentType, workDir)
	if err != nil {
		return err
	}

	switcher.SetProviders(providers)
	if activeProvider != "" && !switcher.SetActiveProvider(activeProvider) {
		slog.Warn("go-bridge: configured provider not found in loaded provider set", "agent", agentType, "provider", activeProvider)
	}
	return nil
}

func loadProviderSeedForAgent(agentType, workDir string) ([]core.ProviderConfig, string, error) {
	configPath := ccConnectConfigPath()
	if configPath == "" {
		return []core.ProviderConfig{}, "", nil
	}
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return []core.ProviderConfig{}, "", nil
		}
		return nil, "", fmt.Errorf("go-bridge: stat config %q: %w", configPath, err)
	}

	cfg, err := loadProviderSeedConfig(configPath)
	if err != nil {
		return nil, "", fmt.Errorf("go-bridge: load config %q: %w", configPath, err)
	}

	project := findProviderProject(cfg, agentType, workDir)
	if project == nil {
		return []core.ProviderConfig{}, "", nil
	}

	return providerConfigsToCore(project.Agent.Providers), optionString(project.Agent.Options["provider"]), nil
}

func ccConnectConfigPath() string {
	if path := strings.TrimSpace(os.Getenv("CC_CONNECT_CONFIG")); path != "" {
		return path
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".cc-connect", "config.toml")
}

func findProviderProject(cfg *providerSeedConfig, agentType, workDir string) *providerSeedProject {
	if cfg == nil {
		return nil
	}

	normalizedWorkDir := normalizeProviderPath(workDir)
	var prefixMatch *providerSeedProject
	for i := range cfg.Projects {
		project := &cfg.Projects[i]
		if project.Agent.Type != agentType {
			continue
		}
		matches, exact := projectMatchesWorkDir(project, normalizedWorkDir)
		if !matches {
			continue
		}
		if exact {
			return project
		}
		if prefixMatch == nil {
			prefixMatch = project
		}
	}
	return prefixMatch
}

func projectMatchesWorkDir(project *providerSeedProject, workDir string) (matches bool, exact bool) {
	if project == nil || workDir == "" {
		return false, false
	}

	if configuredWorkDir := optionString(project.Agent.Options["work_dir"]); configuredWorkDir != "" {
		normalizedConfiguredWorkDir := normalizeProviderPath(configuredWorkDir)
		if normalizedConfiguredWorkDir == workDir {
			return true, true
		}
	}

	if strings.EqualFold(strings.TrimSpace(project.Mode), "multi-workspace") {
		normalizedBaseDir := normalizeProviderPath(project.BaseDir)
		if normalizedBaseDir == "" {
			return false, false
		}
		if normalizedBaseDir == workDir {
			return true, true
		}
		if isPathWithinBase(workDir, normalizedBaseDir) {
			return true, false
		}
	}

	return false, false
}

func isPathWithinBase(path, base string) bool {
	if path == "" || base == "" {
		return false
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func normalizeProviderPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	absPath, err := filepath.Abs(trimmed)
	if err == nil {
		return filepath.Clean(absPath)
	}
	return filepath.Clean(trimmed)
}

func optionString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func providerConfigsToCore(providers []providerSeedProvider) []core.ProviderConfig {
	result := make([]core.ProviderConfig, 0, len(providers))
	for _, provider := range providers {
		mapped := core.ProviderConfig{
			Name:     provider.Name,
			APIKey:   provider.APIKey,
			BaseURL:  provider.BaseURL,
			Model:    provider.Model,
			Thinking: provider.Thinking,
			Env:      cloneStringMap(provider.Env),
		}
		for _, model := range provider.Models {
			mapped.Models = append(mapped.Models, core.ModelOption{Name: model.Model, Alias: model.Alias})
		}
		if provider.Codex != nil {
			mapped.CodexWireAPI = provider.Codex.WireAPI
			mapped.CodexHTTPHeaders = cloneStringMap(provider.Codex.HTTPHeaders)
		}
		result = append(result, mapped)
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func providerConfigsToWire(providers []core.ProviderConfig, activeProvider string) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(providers))
	for _, provider := range providers {
		item := map[string]interface{}{
			"name":     provider.Name,
			"baseUrl":  provider.BaseURL,
			"model":    provider.Model,
			"isActive": provider.Name == activeProvider,
		}
		if provider.Thinking != "" {
			item["thinking"] = provider.Thinking
		}
		if len(provider.Models) > 0 {
			models := make([]map[string]interface{}, 0, len(provider.Models))
			for _, model := range provider.Models {
				models = append(models, map[string]interface{}{
					"id":    model.Name,
					"alias": model.Alias,
				})
			}
			item["models"] = models
		}
		result = append(result, item)
	}
	return result
}
