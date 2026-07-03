package providerseedtest

import (
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/openAgi2/cordcode-macbridge/core"
)

type Config struct {
	Providers []Provider `toml:"providers"`
	Projects  []Project  `toml:"projects"`
}

type Project struct {
	Name    string `toml:"name"`
	Mode    string `toml:"mode"`
	BaseDir string `toml:"base_dir"`
	Agent   Agent  `toml:"agent"`
}

type Agent struct {
	Type         string         `toml:"type"`
	Options      map[string]any `toml:"options"`
	ProviderRefs []string       `toml:"provider_refs"`
	Providers    []Provider     `toml:"providers"`
}

type ProviderModel struct {
	Model string `toml:"model"`
	Alias string `toml:"alias"`
}

type Provider struct {
	Name            string                     `toml:"name"`
	APIKey          string                     `toml:"api_key"`
	BaseURL         string                     `toml:"base_url"`
	Model           string                     `toml:"model"`
	Models          []ProviderModel            `toml:"models"`
	Thinking        string                     `toml:"thinking"`
	Env             map[string]string          `toml:"env"`
	AgentTypes      []string                   `toml:"agent_types"`
	Endpoints       map[string]string          `toml:"endpoints"`
	AgentModels     map[string]string          `toml:"agent_models"`
	AgentModelLists map[string][]ProviderModel `toml:"agent_model_lists"`
	Codex           *CodexProvider             `toml:"codex"`
}

type CodexProvider struct {
	WireAPI     string            `toml:"wire_api"`
	HTTPHeaders map[string]string `toml:"http_headers"`
}

func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parse provider seed config: %w", err)
	}
	resolveEnv(&cfg)
	resolveProviderRefs(&cfg)
	return &cfg, nil
}

func (p Provider) CoreConfig() core.ProviderConfig {
	mapped := core.ProviderConfig{
		Name:     p.Name,
		APIKey:   p.APIKey,
		BaseURL:  p.BaseURL,
		Model:    p.Model,
		Thinking: p.Thinking,
		Env:      cloneStringMap(p.Env),
	}
	for _, model := range p.Models {
		mapped.Models = append(mapped.Models, core.ModelOption{Name: model.Model, Alias: model.Alias})
	}
	if p.Codex != nil {
		mapped.CodexWireAPI = p.Codex.WireAPI
		mapped.CodexHTTPHeaders = cloneStringMap(p.Codex.HTTPHeaders)
	}
	return mapped
}

func resolveProviderRefs(cfg *Config) {
	if cfg == nil || len(cfg.Providers) == 0 {
		return
	}
	globalByName := make(map[string]Provider, len(cfg.Providers))
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
		resolved := make([]Provider, 0, len(refs))
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
				continue
			}
			resolved = append(resolved, provider.resolveForAgent(agentType))
		}
		cfg.Projects[i].Agent.Providers = append(resolved, cfg.Projects[i].Agent.Providers...)
	}
}

func (p Provider) resolveForAgent(agentType string) Provider {
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

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func resolveEnv(value any) {
	resolveEnvValue(reflect.ValueOf(value))
}

func resolveEnvValue(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			resolveEnvValue(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			resolveEnvValue(v.Field(i))
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString(resolveEnvString(v.String()))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			if elem.CanSet() {
				elem.Set(cloneEnvValue(elem))
				continue
			}
			resolveEnvValue(elem)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		iter := v.MapRange()
		for iter.Next() {
			v.SetMapIndex(iter.Key(), cloneEnvValue(iter.Value()))
		}
	case reflect.Interface:
		if !v.IsNil() && v.CanSet() {
			v.Set(cloneEnvValue(v.Elem()))
		}
	}
}

func cloneEnvValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.String:
		out := reflect.New(v.Type()).Elem()
		out.SetString(resolveEnvString(v.String()))
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(v.Elem())
		resolveEnvValue(out.Elem())
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		resolveEnvValue(out)
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneEnvValue(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneEnvValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), cloneEnvValue(iter.Value()))
		}
		return out
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(cloneEnvValue(v.Elem()))
		return out
	default:
		return v
	}
}

func resolveEnvString(value string) string {
	if !strings.Contains(value, "${") {
		return value
	}
	return envPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := envPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return os.Getenv(parts[1])
	})
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
