package opencode

import (
	"context"
	"encoding/json"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type opencodeSessionTokens struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	Total     int `json:"total"`
	Cache     *struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	} `json:"cache"`
}

type opencodeSessionModel struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
}

type opencodeSessionInfo struct {
	ID     string                `json:"id"`
	Tokens *opencodeSessionTokens `json:"tokens"`
	Model  *opencodeSessionModel  `json:"model"`
}

func usageFromOpenCodeSession(info opencodeSessionInfo, window int) *core.ContextUsage {
	if info.Tokens == nil {
		return nil
	}
	tok := info.Tokens
	cacheRead, cacheWrite := 0, 0
	if tok.Cache != nil {
		cacheRead = tok.Cache.Read
		cacheWrite = tok.Cache.Write
	}
	used := tok.Input + cacheRead + cacheWrite
	if used <= 0 {
		used = tok.Total
	}
	if used <= 0 || window <= 0 {
		return nil
	}
	return &core.ContextUsage{
		UsedTokens:            used,
		TotalTokens:           used,
		InputTokens:           tok.Input,
		CachedInputTokens:     cacheRead,
		OutputTokens:          tok.Output,
		ReasoningOutputTokens: tok.Reasoning,
		ContextWindow:         window,
		CacheReadTokens:       cacheRead,
		CacheWriteTokens:      cacheWrite,
	}
}

func (a *Agent) rememberContextUsage(sessionID string, usage *core.ContextUsage) {
	if sessionID == "" || usage == nil {
		return
	}
	copy := *usage
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	if a.usageBySession == nil {
		a.usageBySession = map[string]*core.ContextUsage{}
	}
	a.usageBySession[sessionID] = &copy
}

func (a *Agent) cachedContextUsage(sessionID string) *core.ContextUsage {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	usage := a.usageBySession[sessionID]
	if usage == nil {
		return nil
	}
	copy := *usage
	return &copy
}

func (a *Agent) GetSessionContextUsage(ctx context.Context, sessionID string) (*core.ContextUsage, error) {
	if sessionID == "" {
		return nil, nil
	}
	raw, err := a.fetchJSON(ctx, "/session/"+sessionID)
	if err != nil {
		if cached := a.cachedContextUsage(sessionID); cached != nil {
			return cached, nil
		}
		return nil, err
	}
	var info opencodeSessionInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	window := a.contextWindowForModel(ctx, info.Model)
	usage := usageFromOpenCodeSession(info, window)
	if usage != nil {
		a.rememberContextUsage(sessionID, usage)
	}
	return usage, nil
}

func (a *Agent) contextWindowForModel(ctx context.Context, model *opencodeSessionModel) int {
	if model == nil {
		return a.lookupCachedModelWindow("", "")
	}
	if window := a.lookupCachedModelWindow(model.ProviderID, model.ID); window > 0 {
		return window
	}
	a.refreshModelWindows(ctx)
	return a.lookupCachedModelWindow(model.ProviderID, model.ID)
}

func (a *Agent) lookupCachedModelWindow(providerID, modelID string) int {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	if a.modelWindows == nil {
		return 0
	}
	if modelID != "" {
		if window := a.modelWindows[modelID]; window > 0 {
			return window
		}
		if providerID != "" {
			if window := a.modelWindows[providerID+"/"+modelID]; window > 0 {
				return window
			}
		}
	}
	return 0
}

func (a *Agent) refreshModelWindows(ctx context.Context) {
	raw, err := a.fetchJSON(ctx, "/provider")
	if err != nil {
		return
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return
	}
	found := map[string]int{}
	collectOpenCodeModelWindows(root, found)
	if len(found) == 0 {
		return
	}
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	a.modelWindows = found
}

func collectOpenCodeModelWindows(node any, into map[string]int) {
	switch typed := node.(type) {
	case map[string]any:
		id, _ := typed["id"].(string)
		if limit, ok := typed["limit"].(map[string]any); ok {
			window := anyInt(limit["context"])
			if id != "" && window > 0 {
				into[id] = window
			}
		}
		for _, child := range typed {
			collectOpenCodeModelWindows(child, into)
		}
	case []any:
		for _, child := range typed {
			collectOpenCodeModelWindows(child, into)
		}
	}
}

func anyInt(v any) int {
	switch typed := v.(type) {
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		return 0
	}
}

func usageFromOpenCodeInfoMap(info map[string]any, window int) *core.ContextUsage {
	if info == nil {
		return nil
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return nil
	}
	var parsed opencodeSessionInfo
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	if parsed.Model == nil {
		if modelID, _ := info["modelID"].(string); modelID != "" {
			providerID, _ := info["providerID"].(string)
			parsed.Model = &opencodeSessionModel{ID: modelID, ProviderID: providerID}
		}
	}
	if parsed.Tokens == nil {
		if tokens, ok := info["tokens"].(map[string]any); ok {
			parsed.Tokens = mapOpenCodeTokens(tokens)
		}
	}
	return usageFromOpenCodeSession(parsed, window)
}

func mapOpenCodeTokens(raw map[string]any) *opencodeSessionTokens {
	tok := &opencodeSessionTokens{
		Input:     anyInt(raw["input"]),
		Output:    anyInt(raw["output"]),
		Reasoning: anyInt(raw["reasoning"]),
		Total:     anyInt(raw["total"]),
	}
	if cache, ok := raw["cache"].(map[string]any); ok {
		tok.Cache = &struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		}{Read: anyInt(cache["read"]), Write: anyInt(cache["write"])}
	}
	return tok
}

func (s *opencodeServerSession) GetContextUsage() *core.ContextUsage {
	if s == nil || s.a == nil {
		return nil
	}
	return s.a.cachedContextUsage(s.CurrentSessionID())
}

var _ core.ContextUsageReporter = (*opencodeServerSession)(nil)

func modelFromOpenCodeInfo(info map[string]any) *opencodeSessionModel {
	if info == nil {
		return nil
	}
	if model, ok := info["model"].(map[string]any); ok {
		id, _ := model["id"].(string)
		provider, _ := model["providerID"].(string)
		if id != "" {
			return &opencodeSessionModel{ID: id, ProviderID: provider}
		}
	}
	id, _ := info["modelID"].(string)
	if id == "" {
		return nil
	}
	provider, _ := info["providerID"].(string)
	return &opencodeSessionModel{ID: id, ProviderID: provider}
}
