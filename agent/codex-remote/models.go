package codexremote

// models.go maps the official Codex app-server model/list response used by
// Desktop Remote Control. The field names follow
// codex-rs/app-server-protocol/src/protocol/v2/model.rs. There is deliberately
// no built-in model fallback: if the Remote app-server cannot answer, the
// bridge returns an empty catalog and logs the real failure.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type remoteModel struct {
	ID                     string `json:"id"`
	Model                  string `json:"model"`
	DisplayName            string `json:"displayName"`
	Description            string `json:"description"`
	Hidden                 bool   `json:"hidden"`
	IsDefault              bool   `json:"isDefault"`
	DefaultReasoningEffort string `json:"defaultReasoningEffort"`
	SupportedEfforts       []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

func listRemoteModels(ctx context.Context, cl *Client) ([]remoteModel, error) {
	var all []remoteModel
	seen := map[string]struct{}{}
	cursor := ""
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
			Data       []remoteModel `json:"data"`
			NextCursor *string       `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("codex-remote: model/list decode: %w", err)
		}
		all = append(all, page.Data...)
		if page.NextCursor == nil || strings.TrimSpace(*page.NextCursor) == "" {
			return all, nil
		}
		next := strings.TrimSpace(*page.NextCursor)
		if _, duplicate := seen[next]; duplicate {
			return nil, fmt.Errorf("codex-remote: model/list repeated cursor %q", next)
		}
		seen[next] = struct{}{}
		cursor = next
	}
}

func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return nil
	}
	models, err := listRemoteModels(ctx, cl)
	if err != nil {
		slog.Warn("codex-remote: official model catalog unavailable", "error", err)
		return nil
	}
	options := make([]core.ModelOption, 0, len(models))
	known := make(map[string]struct{}, len(models))
	efforts := make(map[string][]string, len(models))
	defaults := make(map[string]string, len(models))
	defaultModel := ""
	for _, row := range models {
		id := strings.TrimSpace(row.ID)
		if id == "" {
			id = strings.TrimSpace(row.Model)
		}
		if id == "" || row.Hidden {
			continue
		}
		display := strings.TrimSpace(row.DisplayName)
		if display == "" {
			display = id
		}
		options = append(options, core.ModelOption{Name: id, Desc: display, Description: strings.TrimSpace(row.Description)})
		known[id] = struct{}{}
		for _, option := range row.SupportedEfforts {
			if effort := strings.TrimSpace(option.ReasoningEffort); effort != "" {
				efforts[id] = append(efforts[id], effort)
			}
		}
		defaults[id] = strings.TrimSpace(row.DefaultReasoningEffort)
		if row.IsDefault {
			defaultModel = id
		}
	}
	a.mu.Lock()
	if _, ok := known[a.selectedModel]; a.selectedModel != "" && !ok {
		a.selectedModel = ""
	}
	a.modelKnown = known
	a.modelEfforts = efforts
	a.modelDefaultEffort = defaults
	a.defaultModel = defaultModel
	a.mu.Unlock()
	return options
}

func normalizeRemoteModel(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "openai/") {
		return strings.TrimPrefix(raw, "openai/")
	}
	return raw
}

func (a *Agent) SetModel(model string) {
	model = normalizeRemoteModel(model)
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.modelKnown[model]; !ok {
		slog.Warn("codex-remote: ignored model outside official catalog", "model", model)
		return
	}
	a.selectedModel = model
}

func (a *Agent) GetModel() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.selectedModel != "" {
		return a.selectedModel
	}
	return a.defaultModel
}

func (a *Agent) EffortsForModel(_ context.Context, model string) ([]string, string, bool) {
	model = normalizeRemoteModel(model)
	a.mu.Lock()
	defer a.mu.Unlock()
	values, ok := a.modelEfforts[model]
	if !ok || len(values) == 0 {
		return nil, "", false
	}
	return append([]string(nil), values...), a.modelDefaultEffort[model], true
}

func (a *Agent) validateTurnSelection(opts core.PromptOptions) (string, string, error) {
	provider := strings.TrimSpace(opts.ProviderID)
	if provider != "" && provider != "openai" && provider != "default" {
		return "", "", fmt.Errorf("codex-remote: provider switch to %q is not supported by official turn/start", provider)
	}
	model := normalizeRemoteModel(opts.ModelID)
	effort := strings.TrimSpace(opts.ReasoningEffort)
	a.mu.Lock()
	defer a.mu.Unlock()
	if model != "" {
		if _, ok := a.modelKnown[model]; !ok {
			return "", "", fmt.Errorf("codex-remote: model %q is outside the official catalog", model)
		}
	}
	modelForEffort := model
	if modelForEffort == "" {
		modelForEffort = a.selectedModel
	}
	if modelForEffort == "" {
		modelForEffort = a.defaultModel
	}
	if effort != "" {
		valid := false
		for _, candidate := range a.modelEfforts[modelForEffort] {
			if candidate == effort {
				valid = true
				break
			}
		}
		if !valid {
			return "", "", fmt.Errorf("codex-remote: effort %q is outside the official catalog for model %q", effort, modelForEffort)
		}
	}
	return model, effort, nil
}

func (a *Agent) GetSessionModelSelection(ctx context.Context, sessionID string) (core.SessionModelSelection, bool) {
	if err := a.AttachLiveThread(ctx, sessionID); err != nil {
		return core.SessionModelSelection{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	selection, ok := a.sessionSelections[sessionID]
	return selection, ok && selection.Model != ""
}

var _ core.ModelSwitcher = (*Agent)(nil)
var _ core.ModelEffortCatalog = (*Agent)(nil)
var _ core.SessionModelSelectionReader = (*Agent)(nil)
