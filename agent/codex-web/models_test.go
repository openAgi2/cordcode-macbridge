package codexweb

// models_test.go —— Phase 4 model/list + config/read + permissionProfile/list
// contract tests。响应来自 0.149 官方 models-config raw fixture。

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func officialModelsResult(t *testing.T, id json.Number) json.RawMessage {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "official-0.149.0-alpha.4", "dumps", "models-config", "raw.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024), 2<<20)
	for scanner.Scan() {
		var row struct {
			Dir string `json:"dir"`
			Msg struct {
				ID     json.Number     `json:"id"`
				Result json.RawMessage `json:"result"`
			} `json:"msg"`
		}
		dec := json.NewDecoder(strings.NewReader(scanner.Text()))
		dec.UseNumber()
		if dec.Decode(&row) == nil && row.Dir == "server" && row.Msg.ID == id && len(row.Msg.Result) > 0 {
			return row.Msg.Result
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatalf("official models result id=%s not found", id)
	return nil
}

func modelsTestAgent(t *testing.T) (*Agent, *scriptedTransport) {
	t.Helper()
	s := newScripted()
	cl := NewClient(s, 31)
	t.Cleanup(func() { _ = cl.Close() })
	autoResponder(s, "model/list", officialModelsResult(t, "2"), 0)
	autoResponder(s, "config/read", officialModelsResult(t, "3"), 0)
	autoResponder(s, "permissionProfile/list", officialModelsResult(t, "4"), 0)
	a := New(nil)
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "0.149.0-alpha.4"}
	ep.client = cl
	a.endpoint = ep
	return a, s
}

func TestModelsOfficialCatalogEffectiveProviderAndEfforts(t *testing.T) {
	a, _ := modelsTestAgent(t)
	models := a.AvailableModels(context.Background())
	if len(models) != 5 {
		t.Fatalf("model count = %d, want 5: %+v", len(models), models)
	}
	if models[0].Name != "mockpi/gpt-5.6-sol" || models[0].Desc != "GPT-5.6-Sol" {
		t.Fatalf("first model = %+v", models[0])
	}
	// config.model=mock-model 不在官方目录中：保持真实 effective selection，
	// 不把 catalog isDefault 的内置模型冒充当前 custom-provider model。
	if got := a.GetModel(); got != "mockpi/mock-model" {
		t.Fatalf("effective model = %q", got)
	}
	efforts, defaultEffort, ok := a.EffortsForModel(context.Background(), models[0].Name)
	if !ok || defaultEffort != "low" || !reflect.DeepEqual(efforts, []string{"low", "medium", "high", "xhigh", "max", "ultra"}) {
		t.Fatalf("efforts = %v default=%q ok=%v", efforts, defaultEffort, ok)
	}

	a.SetModel("mockpi/not-in-catalog")
	if got := a.GetModel(); got != "mockpi/mock-model" {
		t.Fatalf("invalid selection changed model to %q", got)
	}
	a.SetModel("mockpi/gpt-5.6-terra")
	if got := a.GetModel(); got != "mockpi/gpt-5.6-terra" {
		t.Fatalf("selected model = %q", got)
	}
}

func TestModelsValidatedSelectionShapesThreadStart(t *testing.T) {
	a, transport := modelsTestAgent(t)
	models := a.AvailableModels(context.Background())
	a.SetModel(models[1].Name)
	autoResponder(transport, "thread/start", map[string]any{
		"thread": map[string]any{"id": "th-model"},
		"model":  "gpt-5.6-terra", "modelProvider": "mockpi", "cwd": "/tmp/ws",
	}, 0)
	a.workDir = "/tmp/ws"
	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()

	var found bool
	for _, frame := range transport.sentFrames() {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Cwd           string `json:"cwd"`
				Model         string `json:"model"`
				ModelProvider string `json:"modelProvider"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(frame), &req) == nil && req.Method == "thread/start" {
			found = true
			if req.Params.Cwd != "/tmp/ws" || req.Params.Model != "gpt-5.6-terra" || req.Params.ModelProvider != "mockpi" {
				t.Fatalf("thread/start params = %+v", req.Params)
			}
		}
	}
	if !found {
		t.Fatalf("thread/start frame missing: %v", transport.sentFrames())
	}
}

func TestModelsBadShapeHasNoFallback(t *testing.T) {
	s := newScripted()
	cl := NewClient(s, 32)
	defer cl.Close()
	autoResponder(s, "model/list", map[string]any{"data": "not-an-array"}, 0)
	a := New(nil)
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: sampledModelsCLIVersion}
	ep.client = cl
	a.endpoint = ep
	if got := a.AvailableModels(context.Background()); got != nil {
		t.Fatalf("bad official shape must not fall back: %+v", got)
	}
	if got := a.GetModel(); got != "" {
		t.Fatalf("bad shape must not invent current model: %q", got)
	}
}

func TestModelsUnknownVersionFailsClosed(t *testing.T) {
	s := newScripted()
	cl := NewClient(s, 33)
	defer cl.Close()
	a := New(nil)
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "0.150.0-unknown"}
	ep.client = cl
	a.endpoint = ep
	if got := a.AvailableModels(context.Background()); got != nil {
		t.Fatalf("unknown version catalog = %+v", got)
	}
	if len(s.sentFrames()) != 0 {
		t.Fatalf("unknown version must fail before model/config calls: %v", s.sentFrames())
	}
	if _, err := a.ListPermissionProfiles(context.Background()); err == nil || !strings.Contains(err.Error(), "not sampled") {
		t.Fatalf("unknown version permission profiles error = %v", err)
	}
}

func TestModelsPermissionProfilesOfficialFixture(t *testing.T) {
	a, _ := modelsTestAgent(t)
	profiles, err := a.ListPermissionProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 || profiles[0].ID != ":read-only" || profiles[1].ID != ":workspace" || profiles[2].ID != ":danger-full-access" {
		t.Fatalf("profiles = %+v", profiles)
	}
	for _, profile := range profiles {
		if !profile.Allowed {
			t.Fatalf("official allowed profile marked false: %+v", profile)
		}
	}
}

func TestModelsImplementsReadOnlyCatalogInterfaces(t *testing.T) {
	var agent any = New(nil)
	if _, ok := agent.(core.ModelSwitcher); !ok {
		t.Fatal("Agent must implement core.ModelSwitcher")
	}
	if _, ok := agent.(core.ModelEffortCatalog); !ok {
		t.Fatal("Agent must implement core.ModelEffortCatalog")
	}
	src, err := os.ReadFile("models.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"os.WriteFile", "os.Create", "config.toml\")"} {
		if strings.Contains(string(src), forbidden) {
			t.Fatalf("models.go contains forbidden config write path %q", forbidden)
		}
	}
}
