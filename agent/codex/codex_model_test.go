package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestReadCodexConfigModels_ReturnsConfiguredModel(t *testing.T) {
	writeCodexConfig(t, `model = "deepseek-v4-flash"
model_provider = "custom"

[model_providers.custom]
name = "deepseek"
base_url = "https://api.deepseek.com"
`)

	models := readCodexConfigModels()
	if len(models) != 1 {
		t.Fatalf("readCodexConfigModels() = %v, want exactly 1 model", models)
	}
	if models[0].Name != "deepseek-v4-flash" {
		t.Fatalf("model name = %q, want deepseek-v4-flash", models[0].Name)
	}
	if !strings.Contains(models[0].Desc, "deepseek") {
		t.Fatalf("model desc = %q, want provider name", models[0].Desc)
	}
}

func TestReadCodexConfigModels_EmptyOrMissingModel(t *testing.T) {
	t.Run("no config file", func(t *testing.T) {
		t.Setenv("CODEX_HOME", t.TempDir())
		if models := readCodexConfigModels(); models != nil {
			t.Fatalf("readCodexConfigModels() = %v, want nil for missing file", models)
		}
	})

	t.Run("empty model field", func(t *testing.T) {
		writeCodexConfig(t, "model = \"\"\n")
		if models := readCodexConfigModels(); models != nil {
			t.Fatalf("readCodexConfigModels() = %v, want nil for empty model", models)
		}
	})

	t.Run("corrupt file", func(t *testing.T) {
		writeCodexConfig(t, "this is not toml [[[")
		if models := readCodexConfigModels(); models != nil {
			t.Fatalf("readCodexConfigModels() = %v, want nil for corrupt file", models)
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

func TestAvailableModels_ConfigTierShortCircuits(t *testing.T) {
	writeCodexConfig(t, "model = \"deepseek-v4-flash\"\n")
	a := &Agent{}

	models := a.AvailableModels(context.Background())
	if len(models) != 1 || models[0].Name != "deepseek-v4-flash" {
		t.Fatalf("AvailableModels() = %v, want exactly [deepseek-v4-flash] (tier 1.5 short-circuit)", models)
	}
}
