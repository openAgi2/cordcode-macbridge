package dshweb

// EffortsForModel（core.ModelEffortCatalog）：per-model effort 真值来自
// llm.models 的 reasoning{efforts,defaultEffort}，不经 agent 级抹平。
// 路线图 §5.2 第三条（审计 N3/P0）/ Phase 1。

import (
	"context"
	"reflect"
	"testing"
)

func TestEffortsForModelPerModelTruth(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)

	f.handlers["llm.providers"] = fakeRPCResponse{value: map[string]any{
		"providers": []any{map[string]any{
			"provider": "deepseek", "displayName": "DeepSeek",
			"settingsNs": "", "settingsPath": []any{}, "active": true,
		}},
	}}
	f.handlers["llm.models"] = fakeRPCResponse{value: map[string]any{
		"groups": []any{map[string]any{
			"id": "deepseek", "name": "DeepSeek",
			"models": []any{
				map[string]any{
					"id": "deepseek-v4-flash", "name": "V4 Flash",
					"reasoning": map[string]any{
						"efforts": []any{
							map[string]any{"id": "off", "name": "Off"},
							map[string]any{"id": "low", "name": "Low"},
							map[string]any{"id": "high", "name": "High"},
							map[string]any{"id": "max", "name": "Max"},
						},
						"defaultEffort": "high",
					},
				},
				map[string]any{"id": "deepseek-v4", "name": "V4"},
			},
		}},
		"failures": []any{},
	}}

	if models := a.AvailableModels(context.Background()); len(models) != 2 {
		t.Fatalf("AvailableModels len = %d, want 2 (also primes the catalog cache)", len(models))
	}

	efforts, def, ok := a.EffortsForModel(context.Background(), "deepseek/deepseek-v4-flash")
	if !ok {
		t.Fatal("expected ok=true for a model with runtime-declared efforts")
	}
	if def != "high" {
		t.Fatalf("defaultEffort = %q, want high", def)
	}
	if want := []string{"off", "low", "high", "max"}; !reflect.DeepEqual(efforts, want) {
		t.Fatalf("efforts = %#v, want %#v (wire order preserved, off included)", efforts, want)
	}

	// bare id（无 provider 前缀）同样接受。
	if _, _, ok := a.EffortsForModel(context.Background(), "deepseek-v4-flash"); !ok {
		t.Fatal("expected ok=true for bare model id")
	}

	// 无 reasoning 声明的模型：ok=false —— 不猜测、不回退 agent 级词表。
	if _, _, ok := a.EffortsForModel(context.Background(), "deepseek/deepseek-v4"); ok {
		t.Fatal("expected ok=false for a model without runtime-declared efforts")
	}

	// 未知模型：ok=false。
	if _, _, ok := a.EffortsForModel(context.Background(), "deepseek/no-such-model"); ok {
		t.Fatal("expected ok=false for an unknown model")
	}
}
