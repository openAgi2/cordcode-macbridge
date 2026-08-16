package dshweb

// Official dsh web context meter: session projections contextPressure
// (occupancy numerator + contextWindow) and contextBreakdown (system /
// tools / conversation). The official composer ring stays hidden until
// both pressure and capacity exist — we do the same.

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
		var breakdown apiContextBreakdown
		if json.Unmarshal(rawBreakdown, &breakdown) == nil {
			usage.SystemTokens = breakdown.SystemTokens
			usage.ToolsTokens = breakdown.ToolsTokens
			usage.MessageTokens = breakdown.MessageTokens
			usage.BaselineTokens = breakdown.SystemTokens + breakdown.ToolsTokens
			usage.InputTokens = breakdown.MessageTokens
		}
	}
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
