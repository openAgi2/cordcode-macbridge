package dshweb

import (
	"context"
	"encoding/json"
	"testing"
)

func TestUsageFromProjectionsMatchesOfficialMeter(t *testing.T) {
	block := &apiSessionProjectionsBlock{
		AsOfSeq: 40,
		Values: map[string]json.RawMessage{
			"contextPressure":  json.RawMessage(`{"projectedTokens":41200,"pressureTokens":39000,"contextWindow":1000000}`),
			"contextBreakdown": json.RawMessage(`{"systemTokens":1500,"toolsTokens":8400,"messageTokens":19000}`),
		},
	}
	usage := usageFromProjections(block)
	if usage == nil {
		t.Fatal("usage = nil")
	}
	if usage.UsedTokens != 41200 || usage.ContextWindow != 1000000 {
		t.Fatalf("occupancy = %d/%d", usage.UsedTokens, usage.ContextWindow)
	}
	if usage.SystemTokens != 1500 || usage.ToolsTokens != 8400 || usage.MessageTokens != 19000 {
		t.Fatalf("breakdown = %d/%d/%d", usage.SystemTokens, usage.ToolsTokens, usage.MessageTokens)
	}
	if usage.BaselineTokens != 9900 {
		t.Fatalf("baseline = %d, want 9900", usage.BaselineTokens)
	}
}

func TestUsageFromProjectionsHiddenUntilBothSidesExist(t *testing.T) {
	if usageFromProjections(nil) != nil {
		t.Fatal("nil block should hide the meter")
	}
	onlyWindow := &apiSessionProjectionsBlock{
		Values: map[string]json.RawMessage{
			"contextPressure": json.RawMessage(`{"contextWindow":1000000}`),
		},
	}
	if usageFromProjections(onlyWindow) != nil {
		t.Fatal("window without pressure should hide the meter")
	}
	onlyPressure := &apiSessionProjectionsBlock{
		Values: map[string]json.RawMessage{
			"contextPressure": json.RawMessage(`{"projectedTokens":100}`),
		},
	}
	if usageFromProjections(onlyPressure) != nil {
		t.Fatal("pressure without window should hide the meter")
	}
}

func TestGetSessionContextUsageReadsTailProjections(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	f.handlers["session.history"] = fakeRPCResponse{value: map[string]any{
		"events":  []any{},
		"hasMore": false,
		"projections": map[string]any{
			"asOfSeq": 12,
			"values": map[string]any{
				"contextPressure":  map[string]any{"projectedTokens": 41200, "contextWindow": 1000000},
				"contextBreakdown": map[string]any{"systemTokens": 1500, "toolsTokens": 8400, "messageTokens": 19000},
			},
		},
	}}
	usage, err := a.GetSessionContextUsage(context.Background(), "sess-ctx")
	if err != nil {
		t.Fatalf("GetSessionContextUsage: %v", err)
	}
	if usage == nil || usage.UsedTokens != 41200 || usage.ContextWindow != 1000000 {
		t.Fatalf("usage = %+v", usage)
	}
	cached := a.cachedContextUsage("sess-ctx")
	if cached == nil || cached.SystemTokens != 1500 {
		t.Fatalf("cache = %+v", cached)
	}
}