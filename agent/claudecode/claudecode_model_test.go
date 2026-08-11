package claudecode

import (
	"context"
	"net/http"
	"net/http/httptest"
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
		model: "sonnet",
		providers: []core.ProviderConfig{
			{Name: "anthropic", Model: "opus"},
		},
		activeIdx: 0,
	}

	if got := a.GetModel(); got != "opus" {
		t.Fatalf("GetModel() = %q, want opus", got)
	}
}

// TestAvailableModels_GatewayMergesAliasAndAPI 覆盖 §5.1 缺口4：custom 网关（routerURL 非空）时
// 不短路 settingsModels，合并 alias（owner 3-slot）+ fetchModelsFromAPI（网关 /v1/models）。
// 修复前 settingsModels 命中即 return，GLM/DeepSeek 等网关模型永远不可见。
func TestAvailableModels_GatewayMergesAliasAndAPI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", dir)
	t.Setenv("ANTHROPIC_BASE_URL", "") // 用 routerURL 触发网关分支，避免真实 env 干扰
	writeClaudeSettings(t, dir, `{
		"env": {
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku-4-5",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME": "glm-4.7",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-6",
			"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "glm-5-turbo",
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-8",
			"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME": "glm-5.2"
		}
	}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"sonnet","display_name":"Claude Sonnet"},
			{"id":"glm-4.7","display_name":"GLM 4.7"},
			{"id":"deepseek-chat","display_name":"DeepSeek Chat"}
		]}`))
	}))
	defer srv.Close()

	a := &Agent{routerURL: srv.URL, routerAPIKey: "test-key"}
	models := a.AvailableModels(context.Background())

	got := map[string]string{}
	for _, m := range models {
		got[m.Name] = m.Desc
	}
	// alias 三条全在，Desc 保留 alias 的 glm-*（未被网关同名项覆盖）。
	if got["haiku"] != "glm-4.7" || got["sonnet"] != "glm-5-turbo" || got["opus"] != "glm-5.2" {
		t.Errorf("alias missing or Desc overwritten: %v", got)
	}
	// 网关第三方模型也在（合并而非短路）。
	if _, ok := got["glm-4.7"]; !ok {
		t.Errorf("gateway model glm-4.7 missing (merge regression?): %v", got)
	}
	if _, ok := got["deepseek-chat"]; !ok {
		t.Errorf("gateway model deepseek-chat missing: %v", got)
	}
	// sonnet 去重：alias 与网关同名，只出现一次。
	count := 0
	for _, m := range models {
		if m.Name == "sonnet" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("sonnet appears %d times, want 1 (dedup)", count)
	}
}

// TestAvailableModels_OfficialShortCircuitsOnSettingsAlias 锁死官方（非网关）场景仍短路
// settingsModels：routerURL 空 + ANTHROPIC_BASE_URL 空 → 只返 alias 3-slot，不合并 API。
func TestAvailableModels_OfficialShortCircuitsOnSettingsAlias(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", dir)
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "") // 确保即便落到 API 也不打真实端点
	writeClaudeSettings(t, dir, `{
		"env": {
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku-4-5",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME": "glm-4.7",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-6",
			"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "glm-5-turbo"
		}
	}`)

	a := &Agent{} // 无 routerURL → 非网关
	models := a.AvailableModels(context.Background())

	if len(models) != 2 {
		t.Fatalf("AvailableModels = %v, want exactly 2 alias (official short-circuit)", models)
	}
	for _, m := range models {
		if m.Name != "haiku" && m.Name != "sonnet" {
			t.Errorf("non-alias model %+v leaked into official short-circuit", m)
		}
	}
}
