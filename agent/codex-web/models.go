package codexweb

// models.go —— model/list、permissionProfile/list、config/read 只读映射（§7）。
//
// Phase 0 实测：typed Model 无 provider；custom provider 未实现 /v1/models 时 codex 回落
// 内置目录并 warning；config/read typed model_provider + flatten additional 实测为空
// （不含 model_providers）。禁止递归提取 provider 目录、禁止写 config.toml（⛔ 行）。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const sampledModelsCLIVersion = "0.149.0-alpha.4"

type codexWebModel struct {
	ID                       string `json:"id"`
	Model                    string `json:"model"`
	DisplayName              string `json:"displayName"`
	Description              string `json:"description"`
	Hidden                   bool   `json:"hidden"`
	IsDefault                bool   `json:"isDefault"`
	DefaultReasoningEffort   string `json:"defaultReasoningEffort"`
	SupportedReasoningEffort []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

type effectiveConfig struct {
	Model         string
	ModelProvider string
}

type PermissionProfile struct {
	ID          string  `json:"id"`
	Description *string `json:"description"`
	Allowed     bool    `json:"allowed"`
}

func listOfficialModels(ctx context.Context, cl *Client) ([]codexWebModel, error) {
	var all []codexWebModel
	seen := map[string]bool{}
	var cursor string
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, rpcErr, err := cl.RequestContext(ctx, "model/list", params)
		if err != nil {
			return nil, err
		}
		if rpcErr != nil {
			return nil, rpcErr
		}
		var page struct {
			Data       []codexWebModel `json:"data"`
			NextCursor *string         `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("codexweb: model/list decode: %w", err)
		}
		all = append(all, page.Data...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			return all, nil
		}
		if seen[*page.NextCursor] {
			return nil, fmt.Errorf("codexweb: model/list repeated cursor %q", *page.NextCursor)
		}
		seen[*page.NextCursor] = true
		cursor = *page.NextCursor
	}
}

func readEffectiveConfig(ctx context.Context, cl *Client) (effectiveConfig, error) {
	raw, rpcErr, err := cl.RequestContext(ctx, "config/read", map[string]any{})
	if err != nil {
		return effectiveConfig{}, err
	}
	if rpcErr != nil {
		return effectiveConfig{}, rpcErr
	}
	var response struct {
		Config struct {
			Model         *string `json:"model"`
			ModelProvider *string `json:"model_provider"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return effectiveConfig{}, fmt.Errorf("codexweb: config/read decode: %w", err)
	}
	var out effectiveConfig
	if response.Config.Model != nil {
		out.Model = strings.TrimSpace(*response.Config.Model)
	}
	if response.Config.ModelProvider != nil {
		out.ModelProvider = strings.TrimSpace(*response.Config.ModelProvider)
	}
	return out, nil
}

// AvailableModels 实现 core.ModelSwitcher。目录只来自官方 model/list；provider
// 只来自 typed config.model_provider。任何读取失败返回空并记录真实错误，不使用内置
// fallback、config.toml 解析或缓存快照伪装成功。
func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	var models []codexWebModel
	var cfg effectiveConfig
	err := a.withClient(ctx, func(cl *Client) error {
		if err := a.requireSampledModelsVersion(); err != nil {
			return err
		}
		var err error
		models, err = listOfficialModels(ctx, cl)
		if err != nil {
			return err
		}
		cfg, err = readEffectiveConfig(ctx, cl)
		return err
	})
	if err != nil {
		slog.Warn("codexweb: official model catalog unavailable", "error", err)
		return nil
	}

	options := make([]core.ModelOption, 0, len(models))
	efforts := make(map[string][]string, len(models))
	defaults := make(map[string]string, len(models))
	known := make(map[string]string, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			id = strings.TrimSpace(model.Model)
		}
		if id == "" || model.Hidden {
			continue
		}
		qualified := qualifyModel(cfg.ModelProvider, id)
		displayName := strings.TrimSpace(model.DisplayName)
		if displayName == "" {
			displayName = id
		}
		options = append(options, core.ModelOption{
			Name:        qualified,
			Desc:        displayName,
			Description: strings.TrimSpace(model.Description),
		})
		known[qualified] = id
		for _, effort := range model.SupportedReasoningEffort {
			if value := strings.TrimSpace(effort.ReasoningEffort); value != "" {
				efforts[qualified] = append(efforts[qualified], value)
			}
		}
		defaults[qualified] = strings.TrimSpace(model.DefaultReasoningEffort)
	}

	a.mu.Lock()
	selectedKey := qualifyModel(a.modelProvider, a.selectedModel)
	if a.modelExplicit {
		if _, stillValid := known[selectedKey]; !stillValid {
			a.selectedModel = ""
			a.modelExplicit = false
		}
	}
	a.modelProvider = cfg.ModelProvider
	a.effectiveModel = cfg.Model
	a.modelKnown = known
	a.modelEfforts = efforts
	a.modelDefaultEffort = defaults
	a.mu.Unlock()
	return options
}

func qualifyModel(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if provider == "" {
		return model
	}
	return provider + "/" + model
}

// SetModel 只接受最近一次官方目录中的 id。接口本身无 error 返回，非法值保持原
// selection 并记录；不能把任意字符串带入 thread/start。
func (a *Agent) SetModel(model string) {
	model = strings.TrimSpace(model)
	a.mu.Lock()
	defer a.mu.Unlock()
	bare, ok := a.modelKnown[model]
	if !ok {
		slog.Warn("codexweb: ignored model outside official catalog", "model", model)
		return
	}
	a.selectedModel = bare
	a.modelExplicit = true
}

func (a *Agent) GetModel() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	model := a.effectiveModel
	if a.modelExplicit {
		model = a.selectedModel
	}
	return qualifyModel(a.modelProvider, model)
}

func (a *Agent) EffortsForModel(_ context.Context, model string) ([]string, string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	efforts, ok := a.modelEfforts[model]
	if !ok || len(efforts) == 0 {
		return nil, "", false
	}
	return append([]string(nil), efforts...), a.modelDefaultEffort[model], true
}

func (a *Agent) selectedModelForStart() (model, provider string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.modelExplicit {
		return "", ""
	}
	return a.selectedModel, a.modelProvider
}

func (a *Agent) modelForTurn(raw string) (model, provider string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	bare, ok := a.modelKnown[raw]
	if !ok {
		return "", "", fmt.Errorf("codex-web: model %q is outside the official catalog", raw)
	}
	return bare, a.modelProvider, nil
}

func (a *Agent) effortForTurn(modelKey, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, effort := range a.modelEfforts[modelKey] {
		if effort == raw {
			return raw, nil
		}
	}
	return "", fmt.Errorf("codex-web: effort %q is outside the official catalog for model %q", raw, modelKey)
}

func (a *Agent) ListPermissionProfiles(ctx context.Context) ([]PermissionProfile, error) {
	var profiles []PermissionProfile
	err := a.withClient(ctx, func(cl *Client) error {
		if err := a.requireSampledModelsVersion(); err != nil {
			return err
		}
		raw, rpcErr, err := cl.RequestContext(ctx, "permissionProfile/list", map[string]any{})
		if err != nil {
			return err
		}
		if rpcErr != nil {
			return rpcErr
		}
		var response struct {
			Data []PermissionProfile `json:"data"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("codexweb: permissionProfile/list decode: %w", err)
		}
		profiles = response.Data
		return nil
	})
	return profiles, err
}

func (a *Agent) requireSampledModelsVersion() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.endpoint == nil || a.endpoint.CLIVersion != sampledModelsCLIVersion {
		version := "unknown"
		if a.endpoint != nil && a.endpoint.CLIVersion != "" {
			version = a.endpoint.CLIVersion
		}
		return fmt.Errorf("%w: codex-web model/config wire version %s is not sampled (want %s)", core.ErrNotSupported, version, sampledModelsCLIVersion)
	}
	return nil
}

var _ core.ModelSwitcher = (*Agent)(nil)
var _ core.ModelEffortCatalog = (*Agent)(nil)
