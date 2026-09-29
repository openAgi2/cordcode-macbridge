package opencode

import (
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestUsageFromOpenCodeSessionUsesInputPlusCache(t *testing.T) {
	info := opencodeSessionInfo{
		ID: "ses_0c84",
		Tokens: &opencodeSessionTokens{
			Input:     13641,
			Output:    32,
			Reasoning: 185,
			Total:     14882,
			Cache:     &struct {
				Read  int `json:"read"`
				Write int `json:"write"`
			}{Read: 1024, Write: 0},
		},
		Model: &opencodeSessionModel{ID: "mimo-v2.5-free", ProviderID: "opencode"},
	}
	usage := usageFromOpenCodeSession(info, 200000)
	if usage == nil {
		t.Fatal("usage = nil")
	}
	if usage.UsedTokens != 13641+1024 {
		t.Fatalf("UsedTokens = %d, want 14665", usage.UsedTokens)
	}
	if usage.ContextWindow != 200000 {
		t.Fatalf("ContextWindow = %d", usage.ContextWindow)
	}
	if usage.CachedInputTokens != 1024 || usage.OutputTokens != 32 {
		t.Fatalf("fields = %+v", usage)
	}
}

func TestCollectOpenCodeModelWindows(t *testing.T) {
	root := map[string]any{
		"all": []any{
			map[string]any{
				"id": "opencode",
				"models": map[string]any{
					"mimo-v2.5-free": map[string]any{
						"id":    "mimo-v2.5-free",
						"limit": map[string]any{"context": float64(200000), "output": float64(32000)},
					},
				},
			},
		},
	}
	found := map[string]int{}
	collectOpenCodeModelWindows(root, found)
	if found["mimo-v2.5-free"] != 200000 {
		t.Fatalf("found = %#v", found)
	}
}

func TestUsageFromOpenCodeInfoMap(t *testing.T) {
	usage := usageFromOpenCodeInfoMap(map[string]any{
		"id":      "ses_1",
		"modelID": "mimo-v2.5-free",
		"tokens": map[string]any{
			"input": 100,
			"cache": map[string]any{"read": 20, "write": 5},
		},
	}, 200000)
	if usage == nil || usage.UsedTokens != 125 {
		t.Fatalf("usage = %+v", usage)
	}
	_ = core.ContextUsage{}
}
