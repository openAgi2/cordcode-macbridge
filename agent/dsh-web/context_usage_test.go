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

func TestUsageFromProjectionsIncludesOfficialSessionStats(t *testing.T) {
	block := &apiSessionProjectionsBlock{
		AsOfSeq: 40,
		Values: map[string]json.RawMessage{
			"contextPressure":  json.RawMessage(`{"projectedTokens":41200,"contextWindow":1000000}`),
			"contextBreakdown": json.RawMessage(`{"systemTokens":1500,"toolsTokens":8400,"messageTokens":19000}`),
			"sessionStats":     json.RawMessage(`{"turns":3,"steps":4,"llmMs":12300,"toolMs":4100,"ttftMs":3600,"ttftSteps":3,"decodeMs":8700,"decodeTokens":392}`),
			"tokenUsage":       json.RawMessage(`{"uncachedInputTokens":1800,"outputTokens":1200,"cacheReadTokens":8200,"cacheWriteTokens":400}`),
		},
	}
	usage := usageFromProjections(block)
	if usage == nil {
		t.Fatal("usage = nil")
	}
	if usage.SessionTurns != 3 || usage.SessionSteps != 4 {
		t.Fatalf("counts = %d/%d", usage.SessionTurns, usage.SessionSteps)
	}
	if usage.SessionLlmMs != 12300 || usage.SessionToolMs != 4100 {
		t.Fatalf("durations = %d/%d", usage.SessionLlmMs, usage.SessionToolMs)
	}
	if usage.SessionTtftMs != 3600 || usage.SessionTtftSteps != 3 {
		t.Fatalf("ttft = %d/%d", usage.SessionTtftMs, usage.SessionTtftSteps)
	}
	if usage.SessionDecodeMs != 8700 || usage.SessionDecodeTokens != 392 {
		t.Fatalf("decode = %d/%d", usage.SessionDecodeMs, usage.SessionDecodeTokens)
	}
	if usage.UncachedInputTokens != 1800 || usage.CacheReadTokens != 8200 || usage.CacheWriteTokens != 400 {
		t.Fatalf("billed input = %d/%d/%d", usage.UncachedInputTokens, usage.CacheReadTokens, usage.CacheWriteTokens)
	}
	if usage.OutputTokens != 1200 {
		t.Fatalf("output = %d", usage.OutputTokens)
	}
}

func TestMergeProjectionValueKeepsOccupancyWhileUpdatingStats(t *testing.T) {
	base := usageFromProjections(&apiSessionProjectionsBlock{
		Values: map[string]json.RawMessage{
			"contextPressure": json.RawMessage(`{"projectedTokens":41200,"contextWindow":1000000}`),
		},
	})
	if base == nil {
		t.Fatal("base = nil")
	}
	merged := mergeProjectionValue(base, "sessionStats", json.RawMessage(`{"turns":2,"steps":3,"llmMs":8000,"toolMs":0,"ttftMs":1200,"ttftSteps":2,"decodeMs":5000,"decodeTokens":200}`))
	if merged == nil {
		t.Fatal("merged = nil")
	}
	if merged.UsedTokens != 41200 || merged.ContextWindow != 1000000 {
		t.Fatalf("occupancy clobbered: %d/%d", merged.UsedTokens, merged.ContextWindow)
	}
	if merged.SessionTurns != 2 || merged.SessionSteps != 3 || merged.SessionLlmMs != 8000 {
		t.Fatalf("stats = %+v", merged)
	}
	if mergeProjectionValue(base, "title", json.RawMessage(`"ignored"`)) != nil {
		t.Fatal("unknown projection key should be ignored")
	}
	if mergeProjectionValue(nil, "sessionStats", json.RawMessage(`{"turns":1,"steps":1}`)) != nil {
		t.Fatal("stats without occupancy should stay hidden")
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