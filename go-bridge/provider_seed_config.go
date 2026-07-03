package gobridge

import (
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

type providerSeedConfig struct {
	Providers []providerSeedProvider `toml:"providers"`
	Projects  []providerSeedProject  `toml:"projects"`
}

type providerSeedProject struct {
	Name    string            `toml:"name"`
	Mode    string            `toml:"mode"`
	BaseDir string            `toml:"base_dir"`
	Agent   providerSeedAgent `toml:"agent"`
}

type providerSeedAgent struct {
	Type         string                 `toml:"type"`
	Options      map[string]any         `toml:"options"`
	ProviderRefs []string               `toml:"provider_refs"`
	Providers    []providerSeedProvider `toml:"providers"`
}

type providerSeedProviderModel struct {
	Model string `toml:"model"`
	Alias string `toml:"alias"`
}

type providerSeedProvider struct {
	Name            string                                 `toml:"name"`
	APIKey          string                                 `toml:"api_key"`
	BaseURL         string                                 `toml:"base_url"`
	Model           string                                 `toml:"model"`
	Models          []providerSeedProviderModel            `toml:"models"`
	Thinking        string                                 `toml:"thinking"`
	Env             map[string]string                      `toml:"env"`
	AgentTypes      []string                               `toml:"agent_types"`
	Endpoints       map[string]string                      `toml:"endpoints"`
	AgentModels     map[string]string                      `toml:"agent_models"`
	AgentModelLists map[string][]providerSeedProviderModel `toml:"agent_model_lists"`
	Codex           *providerSeedCodexProvider             `toml:"codex"`
}

type providerSeedCodexProvider struct {
	WireAPI     string            `toml:"wire_api"`
	HTTPHeaders map[string]string `toml:"http_headers"`
}

func loadProviderSeedConfig(path string) (*providerSeedConfig, error) {
	var cfg providerSeedConfig
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parse provider seed config: %w", err)
	}
	resolveProviderSeedEnv(&cfg)
	resolveProviderSeedRefs(&cfg)
	return &cfg, nil
}

func resolveProviderSeedRefs(cfg *providerSeedConfig) {
	if cfg == nil || len(cfg.Providers) == 0 {
		return
	}
	globalByName := make(map[string]providerSeedProvider, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		globalByName[provider.Name] = provider
	}
	for i := range cfg.Projects {
		refs := cfg.Projects[i].Agent.ProviderRefs
		if len(refs) == 0 {
			continue
		}
		agentType := cfg.Projects[i].Agent.Type
		inlineNames := make(map[string]bool, len(cfg.Projects[i].Agent.Providers))
		for _, provider := range cfg.Projects[i].Agent.Providers {
			inlineNames[provider.Name] = true
		}
		resolved := make([]providerSeedProvider, 0, len(refs))
		for _, name := range refs {
			if inlineNames[name] {
				continue
			}
			provider, ok := globalByName[name]
			if !ok {
				slog.Warn("provider ref not found in global [[providers]]", "project", cfg.Projects[i].Name, "ref", name)
				continue
			}
			if len(provider.AgentTypes) > 0 && !stringSliceContains(provider.AgentTypes, agentType) {
				slog.Debug("skipping provider: agent type mismatch", "provider", name, "project", cfg.Projects[i].Name, "provider_agents", provider.AgentTypes, "project_agent", agentType)
				continue
			}
			resolved = append(resolved, provider.resolveForAgent(agentType))
		}
		cfg.Projects[i].Agent.Providers = append(resolved, cfg.Projects[i].Agent.Providers...)
	}
}

func (p providerSeedProvider) resolveForAgent(agentType string) providerSeedProvider {
	if endpoint := strings.TrimSpace(p.Endpoints[agentType]); endpoint != "" {
		p.BaseURL = endpoint
	}
	if model := strings.TrimSpace(p.AgentModels[agentType]); model != "" {
		p.Model = model
	}
	if models := p.AgentModelLists[agentType]; len(models) > 0 {
		p.Models = models
	}
	return p
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var providerSeedEnvPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func resolveProviderSeedEnv(value any) {
	resolveProviderSeedEnvValue(reflect.ValueOf(value))
}

func resolveProviderSeedEnvValue(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			resolveProviderSeedEnvValue(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			resolveProviderSeedEnvValue(v.Field(i))
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString(resolveProviderSeedEnvString(v.String()))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			if elem.CanSet() {
				elem.Set(cloneProviderSeedEnvValue(elem))
				continue
			}
			resolveProviderSeedEnvValue(elem)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		iter := v.MapRange()
		for iter.Next() {
			v.SetMapIndex(iter.Key(), cloneProviderSeedEnvValue(iter.Value()))
		}
	case reflect.Interface:
		if !v.IsNil() && v.CanSet() {
			v.Set(cloneProviderSeedEnvValue(v.Elem()))
		}
	}
}

func cloneProviderSeedEnvValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.String:
		out := reflect.New(v.Type()).Elem()
		out.SetString(resolveProviderSeedEnvString(v.String()))
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(v.Elem())
		resolveProviderSeedEnvValue(out.Elem())
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		resolveProviderSeedEnvValue(out)
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneProviderSeedEnvValue(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneProviderSeedEnvValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), cloneProviderSeedEnvValue(iter.Value()))
		}
		return out
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(cloneProviderSeedEnvValue(v.Elem()))
		return out
	default:
		return v
	}
}

func resolveProviderSeedEnvString(value string) string {
	if !strings.Contains(value, "${") {
		return value
	}
	return providerSeedEnvPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := providerSeedEnvPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return os.Getenv(parts[1])
	})
}
