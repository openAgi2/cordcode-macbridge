package dshweb

// Official dsh web context meter + StatsLine: session projections
// contextPressure (occupancy numerator + contextWindow),
// contextBreakdown (system / tools / conversation), sessionStats
// (turns/steps/wall times), and tokenUsage (billed buckets). The
// official composer ring stays hidden until both pressure and
// capacity exist — we do the same. Stats ride that same occupancy
// object so iOS can show them in the existing ⭕ sheet.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type apiContextPressure struct {
	PressureTokens  *int `json:"pressureTokens"`
	ProjectedTokens *int `json:"projectedTokens"`
	ContextWindow   *int `json:"contextWindow"`
}

type apiContextBreakdown struct {
	SystemTokens  int `json:"systemTokens"`
	ToolsTokens   int `json:"toolsTokens"`
	MessageTokens int `json:"messageTokens"`
}

type apiSessionStats struct {
	Turns        int     `json:"turns"`
	Steps        int     `json:"steps"`
	LlmMs        float64 `json:"llmMs"`
	ToolMs       float64 `json:"toolMs"`
	TtftMs       float64 `json:"ttftMs"`
	TtftSteps    int     `json:"ttftSteps"`
	DecodeMs     float64 `json:"decodeMs"`
	DecodeTokens float64 `json:"decodeTokens"`
}

type apiTokenUsage struct {
	UncachedInputTokens int `json:"uncachedInputTokens"`
	OutputTokens        int `json:"outputTokens"`
	CacheReadTokens     int `json:"cacheReadTokens"`
	CacheWriteTokens    int `json:"cacheWriteTokens"`
}

func usageFromProjections(block *apiSessionProjectionsBlock) *core.ContextUsage {
	if block == nil || block.Values == nil {
		return nil
	}
	rawPressure, ok := block.Values["contextPressure"]
	if !ok || len(rawPressure) == 0 {
		return nil
	}
	var pressure apiContextPressure
	if json.Unmarshal(rawPressure, &pressure) != nil {
		return nil
	}
	used := pressure.ProjectedTokens
	if used == nil {
		used = pressure.PressureTokens
	}
	if used == nil || pressure.ContextWindow == nil || *pressure.ContextWindow <= 0 {
		return nil
	}
	usage := &core.ContextUsage{
		UsedTokens:    *used,
		TotalTokens:   *used,
		ContextWindow: *pressure.ContextWindow,
	}
	if rawBreakdown, ok := block.Values["contextBreakdown"]; ok && len(rawBreakdown) > 0 {
		applyContextBreakdown(usage, rawBreakdown)
	}
	if rawStats, ok := block.Values["sessionStats"]; ok && len(rawStats) > 0 {
		applySessionStats(usage, rawStats)
	}
	if rawTokens, ok := block.Values["tokenUsage"]; ok && len(rawTokens) > 0 {
		applyTokenUsage(usage, rawTokens)
	}
	return usage
}

func applyContextBreakdown(usage *core.ContextUsage, raw json.RawMessage) {
	if usage == nil {
		return
	}
	var breakdown apiContextBreakdown
	if json.Unmarshal(raw, &breakdown) != nil {
		return
	}
	usage.SystemTokens = breakdown.SystemTokens
	usage.ToolsTokens = breakdown.ToolsTokens
	usage.MessageTokens = breakdown.MessageTokens
	usage.BaselineTokens = breakdown.SystemTokens + breakdown.ToolsTokens
	usage.InputTokens = breakdown.MessageTokens
}

func applySessionStats(usage *core.ContextUsage, raw json.RawMessage) {
	if usage == nil {
		return
	}
	var stats apiSessionStats
	if json.Unmarshal(raw, &stats) != nil {
		return
	}
	usage.SessionTurns = stats.Turns
	usage.SessionSteps = stats.Steps
	usage.SessionLlmMs = int(stats.LlmMs)
	usage.SessionToolMs = int(stats.ToolMs)
	usage.SessionTtftMs = int(stats.TtftMs)
	usage.SessionTtftSteps = stats.TtftSteps
	usage.SessionDecodeMs = int(stats.DecodeMs)
	usage.SessionDecodeTokens = int(stats.DecodeTokens)
}

func applyTokenUsage(usage *core.ContextUsage, raw json.RawMessage) {
	if usage == nil {
		return
	}
	var tokens apiTokenUsage
	if json.Unmarshal(raw, &tokens) != nil {
		return
	}
	usage.UncachedInputTokens = tokens.UncachedInputTokens
	usage.CacheReadTokens = tokens.CacheReadTokens
	usage.CacheWriteTokens = tokens.CacheWriteTokens
	usage.OutputTokens = tokens.OutputTokens
}

func mergeProjectionValue(base *core.ContextUsage, key string, raw json.RawMessage) *core.ContextUsage {
	if key == "" || len(raw) == 0 {
		return nil
	}
	usage := &core.ContextUsage{}
	if base != nil {
		copy := *base
		usage = &copy
	}
	switch key {
	case "contextPressure":
		var pressure apiContextPressure
		if json.Unmarshal(raw, &pressure) != nil {
			return nil
		}
		used := pressure.ProjectedTokens
		if used == nil {
			used = pressure.PressureTokens
		}
		if used == nil || pressure.ContextWindow == nil || *pressure.ContextWindow <= 0 {
			return nil
		}
		usage.UsedTokens = *used
		usage.TotalTokens = *used
		usage.ContextWindow = *pressure.ContextWindow
	case "contextBreakdown":
		applyContextBreakdown(usage, raw)
	case "sessionStats":
		applySessionStats(usage, raw)
	case "tokenUsage":
		applyTokenUsage(usage, raw)
	default:
		return nil
	}
	if usage.ContextWindow <= 0 {
		return nil
	}
	return usage
}

func (a *Agent) applyProjectionValue(sessionID, key string, raw json.RawMessage) *core.ContextUsage {
	if a == nil || sessionID == "" {
		return nil
	}
	usage := mergeProjectionValue(a.cachedContextUsage(sessionID), key, raw)
	if usage == nil {
		return nil
	}
	a.rememberContextUsage(sessionID, usage)
	return usage
}

func (a *Agent) rememberContextUsage(sessionID string, usage *core.ContextUsage) {
	if a == nil || sessionID == "" || usage == nil || usage.ContextWindow <= 0 {
		return
	}
	a.usageMu.Lock()
	if a.usageBySession == nil {
		a.usageBySession = map[string]*core.ContextUsage{}
	}
	copy := *usage
	a.usageBySession[sessionID] = &copy
	a.usageMu.Unlock()
}

func (a *Agent) cachedContextUsage(sessionID string) *core.ContextUsage {
	if a == nil || sessionID == "" {
		return nil
	}
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	usage := a.usageBySession[sessionID]
	if usage == nil {
		return nil
	}
	copy := *usage
	return &copy
}

// GetSessionContextUsage reads the official tail-page projections. iOS
// get_session / fetch_messages attach this as contextUsage.
func (a *Agent) GetSessionContextUsage(ctx context.Context, sessionID string) (*core.ContextUsage, error) {
	if sessionID == "" {
		return nil, nil
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		if cached := a.cachedContextUsage(sessionID); cached != nil {
			return cached, nil
		}
		return nil, err
	}
	usage, err := fetchContextUsage(ctx, client, sessionID)
	if err != nil {
		if cached := a.cachedContextUsage(sessionID); cached != nil {
			return cached, nil
		}
		return nil, err
	}
	a.rememberContextUsage(sessionID, usage)
	return usage, nil
}

func fetchContextUsage(ctx context.Context, client *Client, sessionID string) (*core.ContextUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	max := 1
	var val sessionHistoryValue
	if err := client.Call(ctx, "session.history", sessionHistoryRequest{
		SessionID:   sessionID,
		MaxMessages: &max,
	}, &val); err != nil {
		return nil, err
	}
	return usageFromProjections(val.Projections), nil
}

func (s *dshSession) GetContextUsage() *core.ContextUsage {
	if s == nil || s.agent == nil {
		return nil
	}
	return s.agent.cachedContextUsage(s.CurrentSessionID())
}

var _ core.ContextUsageReporter = (*dshSession)(nil)
