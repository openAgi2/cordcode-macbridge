package grokbuild

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGrokSignalsUsage(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "%2Ftmp%2Fdemo", "01a00500-5bb9-7742-a894-a4ee76986c15")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(`{"current_model_id":"grok-4.6"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "signals.json"), []byte(`{
		"contextWindowUsage": 16,
		"contextTokensUsed": 83659,
		"contextWindowTokens": 500000,
		"primaryModelId": "grok-4.6"
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	usage := loadGrokSignalsUsage(home, "01a00500-5bb9-7742-a894-a4ee76986c15")
	if usage == nil {
		t.Fatal("usage = nil")
	}
	if usage.UsedTokens != 83659 {
		t.Fatalf("UsedTokens = %d, want 83659", usage.UsedTokens)
	}
	if usage.ContextWindow != 500000 {
		t.Fatalf("ContextWindow = %d, want 500000", usage.ContextWindow)
	}

	a := &Agent{grokHome: home}
	got, err := a.GetSessionContextUsage(context.Background(), "01a00500-5bb9-7742-a894-a4ee76986c15")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.UsedTokens != 83659 {
		t.Fatalf("GetSessionContextUsage = %+v", got)
	}
}

func TestContextUsageFromCompactUpdate(t *testing.T) {
	used, window := 400576, 500000
	started := sessionUpdatePayload{
		SessionUpdate: "auto_compact_started",
		TokensUsed:    &used,
		ContextWindow: &window,
	}
	usage := contextUsageFromCompactUpdate(started)
	if usage == nil || usage.UsedTokens != used || usage.ContextWindow != window {
		t.Fatalf("started = %+v", usage)
	}
	after := 16267
	completed := sessionUpdatePayload{
		SessionUpdate: "auto_compact_completed",
		TokensAfter:   &after,
	}
	usage = contextUsageFromCompactUpdate(completed)
	if usage == nil || usage.UsedTokens != after || usage.ContextWindow != grokDefaultContextWindow {
		t.Fatalf("completed = %+v", usage)
	}
}
