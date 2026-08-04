package codex

import (
	"context"
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
