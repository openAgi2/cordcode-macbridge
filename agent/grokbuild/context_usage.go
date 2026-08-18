package grokbuild

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// grokDefaultContextWindow is the initialize-reported Grok Build window
// (docs/2026-07-12-grok-cli-compatibility-evidence.md: 500K). Real
// signals.json on this machine always carries the same value.
const grokDefaultContextWindow = 500000

type grokSignalsFile struct {
	ContextTokensUsed   int `json:"contextTokensUsed"`
	ContextWindowTokens int `json:"contextWindowTokens"`
}

func loadGrokSignalsUsage(grokHome, sessionID string) *core.ContextUsage {
	home := resolveGrokHome(grokHome)
	if home == "" || sessionID == "" {
		return nil
	}
	dir := findSessionDir(home, sessionID)
	if dir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "signals.json"))
	if err != nil {
		return nil
	}
	var sig grokSignalsFile
	if err := json.Unmarshal(raw, &sig); err != nil {
		return nil
	}
	return grokOccupancy(sig.ContextTokensUsed, sig.ContextWindowTokens)
}

func grokOccupancy(used, window int) *core.ContextUsage {
	if used <= 0 {
		return nil
	}
	if window <= 0 {
		window = grokDefaultContextWindow
	}
	return &core.ContextUsage{
		UsedTokens:    used,
		TotalTokens:   used,
		ContextWindow: window,
	}
}

func contextUsageFromCompactUpdate(p sessionUpdatePayload) *core.ContextUsage {
	switch p.SessionUpdate {
	case "auto_compact_started":
		if p.TokensUsed != nil {
			window := 0
			if p.ContextWindow != nil {
				window = *p.ContextWindow
			}
			return grokOccupancy(*p.TokensUsed, window)
		}
	case "auto_compact_completed":
		if p.TokensAfter != nil {
			return grokOccupancy(*p.TokensAfter, grokDefaultContextWindow)
		}
	}
	return nil
}

func (a *Agent) GetSessionContextUsage(_ context.Context, sessionID string) (*core.ContextUsage, error) {
	usage := loadGrokSignalsUsage(a.grokHome, sessionID)
	return usage, nil
}

func (s *grokSession) GetContextUsage() *core.ContextUsage {
	if s == nil || s.agent == nil {
		return nil
	}
	return loadGrokSignalsUsage(s.agent.grokHome, s.CurrentSessionID())
}

var _ core.ContextUsageReporter = (*grokSession)(nil)
