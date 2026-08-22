package codexweb

// models_e2e_test.go —— Phase 4 model/config/profile 真实服务回归。
// 隔离 CODEX_HOME 的 config 指向 mockpi；只读官方 RPC，不写用户或测试 config。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestE2EModelsCustomProviderReadOnly(t *testing.T) {
	if !e2eEnabled(t) {
		return
	}
	providerURL := e2eMockProvider(t)
	_, home, workDir := e2eSetupBase(t, providerURL, nil)
	configPath := filepath.Join(home, "config.toml")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	a := New(map[string]any{"codex_web_codex_home": home, "work_dir": workDir})
	defer func() { _ = a.Stop() }()
	models := a.AvailableModels(context.Background())
	if len(models) == 0 || models[0].Name != "mockpi/gpt-5.6-sol" {
		t.Fatalf("custom-provider qualified catalog = %+v", models)
	}
	if got := a.GetModel(); got != "mockpi/mock-model" {
		t.Fatalf("effective config selection = %q", got)
	}
	profiles, err := a.ListPermissionProfiles(context.Background())
	if err != nil || len(profiles) != 3 {
		t.Fatalf("permission profiles = %+v / %v", profiles, err)
	}

	a.SetModel(models[0].Name)
	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	events, _ := collectEvents(sess)
	options := core.PromptOptions{
		ModelID:         models[0].Name,
		ProviderID:      "mockpi",
		ReasoningEffort: "low",
	}
	if err := sess.(core.PromptOptionsSender).SendWithOptions("MOCK:STREAM model inheritance", nil, nil, options); err != nil {
		t.Fatal(err)
	}
	turnID := sess.(*agentSession).activeTurnSnapshot()
	result := waitForTurnEvent(t, events, core.EventResult, turnID, 30*time.Second)
	if !result.Done {
		t.Fatalf("turn result = %+v", result)
	}
	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var provider string
	for _, info := range sessions {
		if info.ID == sess.CurrentSessionID() {
			provider = info.ProviderID
			break
		}
	}
	if provider != "mockpi" {
		t.Fatalf("thread effective provider = %q, want mockpi", provider)
	}

	resumed, err := a.StartSession(context.Background(), sess.CurrentSessionID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	resumedEvents, _ := collectEvents(resumed)
	if err := resumed.(core.PromptOptionsSender).SendWithOptions("MOCK:STREAM resumed model inheritance", nil, nil, options); err != nil {
		t.Fatal(err)
	}
	resumedTurnID := resumed.(*agentSession).activeTurnSnapshot()
	resumedResult := waitForTurnEvent(t, resumedEvents, core.EventResult, resumedTurnID, 30*time.Second)
	if !resumedResult.Done {
		t.Fatalf("resumed turn result = %+v", resumedResult)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("model/config read path modified isolated config.toml")
	}
}
