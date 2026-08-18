package opencodeweb

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// context_usage.go implements the official web context formula (design §3.3,
// copied from packages/app/src/components/session/session-context-metrics.ts):
//
//  1. scan messages backward for the last assistant with tokenTotal > 0;
//  2. total = input + output + reasoning + cache.read + cache.write;
//  3. usage% = round(total / model.limit.context) — no window, no percent.
//
// Red lines (design §3.3 红线):
//   - the numerator ALWAYS comes from message-level info.tokens. Top-level
//     session.tokens is never the derivation source (both shapes differ —
//     S1 — and the official web reads messages);
//   - a missing window returns nil (iOS shows 暂无), never a fabricated 200k;
//   - the model id comes from the last assistant's info.providerID+modelID,
//     not the session top-level model (常 null);
//   - v2 GET …/context is NOT an occupancy source (it lists post-compact
//     in-context messages) and is never fetched here.

// GetSessionContextUsage is the duck-typed surface the go-bridge generic
// handler asserts (handlers.go getSessionContextUsage). Returns (nil, nil)
// when no window exists — the honest "no ring value" verdict.
func (a *Agent) GetSessionContextUsage(ctx context.Context, sessionID string) (*core.ContextUsage, error) {
	if sessionID == "" {
		return nil, nil
	}
	c, err := a.clientFor(ctx)
	if err != nil {
		if cached := a.cachedContextUsage(sessionID); cached != nil {
			return cached, nil
		}
		return nil, err
	}
	usage, err := a.computeContextUsage(ctx, c, sessionID)
	if err != nil {
		if cached := a.cachedContextUsage(sessionID); cached != nil {
			return cached, nil
		}
		return nil, err
	}
	if usage != nil {
		a.rememberContextUsage(sessionID, usage)
	}
	return usage, nil
}

func (a *Agent) computeContextUsage(ctx context.Context, c *Client, sessionID string) (*core.ContextUsage, error) {
	messages, err := a.fetchMessageMaps(ctx, c, sessionID)
	if err != nil {
		return nil, err
	}
	return usageFromMessages(ctx, a, c, messages), nil
}

// usageFromMessages applies the formula over already-fetched messages (shared
// with the SSE session.updated recompute path in events.go).
func usageFromMessages(ctx context.Context, a *Agent, c *Client, messages []map[string]any) *core.ContextUsage {
	info, ok := lastAssistantWithTokens(messages)
	if !ok {
		return nil
	}
	tokens, _ := info["tokens"].(map[string]any)
	if tokens == nil {
		return nil
	}
	cacheRead, cacheWrite := 0, 0
	if cache, ok := tokens["cache"].(map[string]any); ok {
		cacheRead = anyInt(cache["read"])
		cacheWrite = anyInt(cache["write"])
	}
	input := anyInt(tokens["input"])
	output := anyInt(tokens["output"])
	reasoning := anyInt(tokens["reasoning"])
	total := input + output + reasoning + cacheRead + cacheWrite
	if total == 0 {
		total = anyInt(tokens["total"])
	}
	if total <= 0 {
		return nil
	}

	// Model id from the last assistant's info (not session.model).
	modelID, _ := info["modelID"].(string)
	providerID, _ := info["providerID"].(string)

	window := a.contextWindowForModel(ctx, c, providerID, modelID)
	if window <= 0 {
		// 无窗口 → nil（圆环「暂无」），禁止谎报固定窗口。
		return nil
	}
	return &core.ContextUsage{
		UsedTokens:            total,
		TotalTokens:           total,
		InputTokens:           input,
		OutputTokens:          output,
		ReasoningOutputTokens: reasoning,
		CachedInputTokens:     cacheRead,
		CacheReadTokens:       cacheRead,
		CacheWriteTokens:      cacheWrite,
		ContextWindow:         window,
	}
}

// modelWindowCacheTTL bounds the /provider window map freshness.
const modelWindowCacheTTL = 5 * time.Minute

// contextWindowForModel resolves limit.context from the runtime provider
// catalog (design §3.6: recursive collection of any node carrying both id and
// limit.context; keys record both bare id and providerID/id).
func (a *Agent) contextWindowForModel(ctx context.Context, c *Client, providerID, modelID string) int {
	if modelID == "" {
		return 0
	}
	windows, ok := a.cachedModelWindows()
	if !ok {
		windows = a.refreshModelWindows(ctx, c)
	}
	if window := lookupModelWindow(windows, providerID, modelID); window > 0 {
		return window
	}
	// One refresh per miss when the cache is stale.
	windows = a.refreshModelWindows(ctx, c)
	return lookupModelWindow(windows, providerID, modelID)
}

func lookupModelWindow(windows map[string]int, providerID, modelID string) int {
	if windows == nil || modelID == "" {
		return 0
	}
	if providerID != "" {
		if window := windows[providerID+"/"+modelID]; window > 0 {
			return window
		}
	}
	return windows[modelID]
}

func (a *Agent) cachedModelWindows() (map[string]int, bool) {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	if a.modelWindows == nil || time.Since(a.modelWindowsAt) > modelWindowCacheTTL {
		return nil, false
	}
	return a.modelWindows, true
}

func (a *Agent) refreshModelWindows(ctx context.Context, c *Client) map[string]int {
	raw, err := c.fetchJSON(ctx, c.apiPath("/provider"), a.GetWorkDir())
	if err != nil {
		slog.Debug("opencode-web: provider fetch for windows failed", "error", err)
		return nil
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil
	}
	found := map[string]int{}
	collectModelWindows(root, found)
	if len(found) == 0 {
		return nil
	}
	a.usageMu.Lock()
	a.modelWindows = found
	a.modelWindowsAt = time.Now()
	a.usageMu.Unlock()
	return found
}

// collectModelWindows recursively records every node carrying both id and
// limit.context, keyed by bare id and (when derivable) providerID/id. The
// provider-qualified key is attached while descending a provider object.
func collectModelWindows(node any, into map[string]int, providerIDs ...string) {
	switch typed := node.(type) {
	case map[string]any:
		id, _ := typed["id"].(string)
		limit, _ := typed["limit"].(map[string]any)
		if id != "" && limit != nil {
			if window := anyInt(limit["context"]); window > 0 {
				into[id] = window
				if len(providerIDs) > 0 {
					into[providerIDs[0]+"/"+id] = window
				}
			}
		}
		// A node with models + an id (but maybe no limit of its own) scopes
		// its children's qualified keys.
		scope := providerIDs
		if id != "" && typed["models"] != nil {
			scope = []string{id}
		}
		for key, child := range typed {
			if key == "limit" {
				continue
			}
			collectModelWindows(child, into, scope...)
		}
	case []any:
		for _, child := range typed {
			collectModelWindows(child, into, providerIDs...)
		}
	}
}

// rememberContextUsage / cachedContextUsage cache the last computed usage per
// session so the live ContextUsageReporter surface can serve it without a
// refetch and transient failures fall back to the last known value.
func (a *Agent) rememberContextUsage(sessionID string, usage *core.ContextUsage) {
	if sessionID == "" || usage == nil {
		return
	}
	copyUsage := *usage
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	if a.usageBySession == nil {
		a.usageBySession = map[string]*core.ContextUsage{}
	}
	a.usageBySession[sessionID] = &copyUsage
}

func (a *Agent) cachedContextUsage(sessionID string) *core.ContextUsage {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	usage := a.usageBySession[sessionID]
	if usage == nil {
		return nil
	}
	copyUsage := *usage
	return &copyUsage
}
