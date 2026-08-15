package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestConfiguredModels_BoundaryConditions(t *testing.T) {
	a := &Agent{
		providers: []core.ProviderConfig{
			{Models: []core.ModelOption{{Name: "first"}}},
			{Models: []core.ModelOption{{Name: "second"}}},
		},
	}

	tests := []struct {
		name      string
		activeIdx int
		wantNil   bool
		wantName  string
	}{
		{name: "negative index", activeIdx: -1, wantNil: true},
		{name: "out of range", activeIdx: 2, wantNil: true},
		{name: "valid index", activeIdx: 1, wantName: "second"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.activeIdx = tt.activeIdx
			got := a.configuredModels()
			if tt.wantNil {
				if got != nil {
					t.Fatalf("configuredModels() = %v, want nil", got)
				}
				return
			}
			if len(got) != 1 || got[0].Name != tt.wantName {
				t.Fatalf("configuredModels() = %v, want %q", got, tt.wantName)
			}
		})
	}
}

func TestGetModel_PrefersActiveProviderModel(t *testing.T) {
	a := &Agent{
		model: "gpt-4.1-mini",
		providers: []core.ProviderConfig{
			{Name: "openai", Model: "gpt-5.4"},
		},
		activeIdx: 0,
	}

	if got := a.GetModel(); got != "gpt-5.4" {
		t.Fatalf("GetModel() = %q, want gpt-5.4", got)
	}
}

func writeCodexConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	t.Setenv("CODEX_HOME", dir)
	return dir
}

// writeCodexConfigWithCatalog 写 config.toml(引用 catalog)+ 一个含 pro/flash 的 catalog 文件。
func writeCodexConfigWithCatalog(t *testing.T, catalogName string) string {
	t.Helper()
	dir := writeCodexConfig(t, "model = \"deepseek-v4-flash\"\nmodel_catalog_json = \""+catalogName+"\"\n")
	catalog := `{"models": [
		{"slug": "deepseek-v4-flash", "display_name": "DeepSeek V4 Flash"},
		{"slug": "deepseek-v4-pro", "display_name": "DeepSeek V4 Pro"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, catalogName), []byte(catalog), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return dir
}

func TestReadCodexConfigModels_ReturnsCatalogModels(t *testing.T) {
	writeCodexConfigWithCatalog(t, "cc-switch-model-catalog.json")

	models := readCodexConfigModels()
	if len(models) != 2 {
		t.Fatalf("readCodexConfigModels() = %v, want exactly 2 catalog models", models)
	}
	if models[0].Name != "deepseek-v4-flash" || models[0].Desc != "DeepSeek V4 Flash" {
		t.Fatalf("models[0] = %+v, want flash with display name", models[0])
	}
	if models[1].Name != "deepseek-v4-pro" || models[1].Desc != "DeepSeek V4 Pro" {
		t.Fatalf("models[1] = %+v, want pro with display name", models[1])
	}
}

func TestReadCodexConfigModels_EmptyOrMissingModel(t *testing.T) {
	t.Run("no config file", func(t *testing.T) {
		t.Setenv("CODEX_HOME", t.TempDir())
		if models := readCodexConfigModels(); models != nil {
			t.Fatalf("readCodexConfigModels() = %v, want nil for missing file", models)
		}
	})

	t.Run("no model_catalog_json field", func(t *testing.T) {
		writeCodexConfig(t, "model = \"deepseek-v4-flash\"\n")
		if models := readCodexConfigModels(); models != nil {
			t.Fatalf("readCodexConfigModels() = %v, want nil without catalog field", models)
		}
	})

	t.Run("corrupt config file", func(t *testing.T) {
		writeCodexConfig(t, "this is not toml [[[")
		if models := readCodexConfigModels(); models != nil {
			t.Fatalf("readCodexConfigModels() = %v, want nil for corrupt config", models)
		}
	})

	t.Run("missing catalog file", func(t *testing.T) {
		writeCodexConfig(t, "model_catalog_json = \"missing-catalog.json\"\n")
		if models := readCodexConfigModels(); models != nil {
			t.Fatalf("readCodexConfigModels() = %v, want nil for missing catalog", models)
		}
	})
}

func TestGetModel_FallsBackToConfigWhenUnset(t *testing.T) {
	writeCodexConfig(t, "model = \"deepseek-v4-flash\"\n")
	a := &Agent{} // no provider, no a.model

	if got := a.GetModel(); got != "deepseek-v4-flash" {
		t.Fatalf("GetModel() = %q, want deepseek-v4-flash (config fallback)", got)
	}
}

func TestAvailableModels_ConfigCatalogTierShortCircuits(t *testing.T) {
	writeCodexConfigWithCatalog(t, "cc-switch-model-catalog.json")
	a := &Agent{}

	models := a.AvailableModels(context.Background())
	if len(models) != 2 {
		t.Fatalf("AvailableModels() = %v, want exactly 2 catalog models (tier 1.5 short-circuit)", models)
	}
	if models[0].Name != "deepseek-v4-flash" || models[1].Name != "deepseek-v4-pro" {
		t.Fatalf("AvailableModels() = %v, want [flash, pro]", models)
	}
}

// TestAvailableModels_APIFetch_NoWhitelist 覆盖 §5.1 缺口 2（C4）：删白名单后 /v1/models 返回的
// 所有 id（含 GLM/DeepSeek 第三方）都转 ModelOption，不再被 openaiChatModels 过滤。
func TestAvailableModels_APIFetch_NoWhitelist(t *testing.T) {
	// CODEX_HOME 指向空 temp dir（无 model/catalog/cache），强制落到 fetchModelsFromAPI。
	t.Setenv("CODEX_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Errorf("request missing Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"glm-4.7"},
			{"id":"deepseek-chat"},
			{"id":"gpt-4.1"}
		]}`))
	}))
	defer srv.Close()
	t.Setenv("OPENAI_BASE_URL", srv.URL)
	t.Setenv("OPENAI_API_KEY", "test-key")

	a := &Agent{}
	models := a.AvailableModels(context.Background())

	got := map[string]bool{}
	for _, m := range models {
		got[m.Name] = true
	}
	for _, want := range []string{"glm-4.7", "deepseek-chat", "gpt-4.1"} {
		if !got[want] {
			t.Errorf("AvailableModels missing %q (whitelist regression?); got %v", want, models)
		}
	}
}

// TestAvailableModels_NativeSingleModel_IsDefault 覆盖 §5.1 缺口 3（C5）：config.toml 顶层 model
// 存在、无 catalog → AvailableModels 只返该模型 + GetModel()==该模型，handleListModels isDefault 命中
// （修复前 GetModel 返 native glm-4.7 但 AvailableModels 不含 → 无 isDefault → iOS 落到 models.first）。
func TestAvailableModels_NativeSingleModel_IsDefault(t *testing.T) {
	writeCodexConfig(t, "model = \"glm-4.7\"\n") // 无 catalog 字段
	a := &Agent{}

	models := a.AvailableModels(context.Background())
	if len(models) != 1 || models[0].Name != "glm-4.7" {
		t.Fatalf("AvailableModels = %v, want single [glm-4.7] (native single-model semantics)", models)
	}
	if got := a.GetModel(); got != "glm-4.7" {
		t.Errorf("GetModel = %q, want glm-4.7 (so handleListModels isDefault matches)", got)
	}
}
