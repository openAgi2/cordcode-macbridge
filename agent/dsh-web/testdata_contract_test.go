package dshweb

// testdata 夹具契约门：session.list / llm.models 的脱敏活体捕获（dsh web
// 0.0.1，2026-08-18 实跑；audit P1 / 路线图 Phase 1 测试资产项）。codex/grok/
// opencode 均有 sanitized 捕获夹具，dsh-web 此前缺失——runtime 升级破坏契约时
// 无对照。schema 漂移时这里先红，由设计阶段重新取证，而不是在 client 现场
// 猜格式（与 grokbuild catalog_session_list_test.go 的格式冻结门同构）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const (
	sessionListFixture = "testdata/session_list_sanitized.json"
	llmModelsFixture   = "testdata/llm_models_sanitized.json"
)

// loadFixtureValue reads a sanitized capture and unmarshals response.result.value
// into the pinned apitypes struct — the decode itself is the freeze gate.
func loadFixtureValue(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("", path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Response struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s envelope: %v", path, err)
	}
	if len(doc.Response.Result.Value) == 0 {
		t.Fatalf("%s: empty result.value", path)
	}
	if err := json.Unmarshal(doc.Response.Result.Value, into); err != nil {
		t.Fatalf("decode %s value into %T: %v", path, into, err)
	}
}

func TestSessionListFixtureDecodesAgainstPinnedSchema(t *testing.T) {
	var val sessionListValue
	loadFixtureValue(t, sessionListFixture, &val)

	roots, subs := 0, 0
	for _, it := range val.Items {
		if it.SessionID == "" {
			t.Fatal("session.list row without sessionId")
		}
		if it.UpdatedAt <= 0 {
			t.Fatalf("row %s: updatedAt must stay a ms epoch", it.SessionID)
		}
		// ListSessions 过滤条件的两个键必须留在夹具里。
		if it.Origin == "subagent" || it.ParentSessionID != "" {
			subs++
			if it.Origin != "subagent" || it.ParentSessionID == "" {
				t.Fatalf("subagent row %s must carry BOTH origin=subagent and parentSessionId", it.SessionID)
			}
			continue
		}
		roots++
		if it.Projections != nil && it.Projections.Values != nil {
			if _, ok := it.Projections.Values["title"]; !ok {
				t.Fatalf("root row %s projections lost the title key", it.SessionID)
			}
		}
	}
	if roots == 0 || subs == 0 {
		t.Fatalf("fixture must cover root AND subagent rows; roots=%d subs=%d", roots, subs)
	}
}

func TestLLMModelsFixtureDecodesAgainstPinnedSchema(t *testing.T) {
	var val llmModelsValue
	loadFixtureValue(t, llmModelsFixture, &val)

	if len(val.Groups) == 0 {
		t.Fatal("llm.models fixture without provider groups")
	}
	models, sawOff, sawDefaultEffort := 0, false, false
	for _, g := range val.Groups {
		for _, m := range g.Models {
			models++
			if m.ID == "" || m.Name == "" {
				t.Fatalf("model without id/name in group %s", g.ID)
			}
			if m.Reasoning == nil {
				continue
			}
			if m.Reasoning.DefaultEffort != "" {
				sawDefaultEffort = true
			}
			for _, e := range m.Reasoning.Efforts {
				if e.ID == "" || e.Name == "" {
					t.Fatalf("effort without id/name on %s/%s", g.ID, m.ID)
				}
				if e.ID == "off" {
					sawOff = true
				}
			}
		}
	}
	if models < 5 {
		t.Fatalf("llm.models fixture too thin: %d models", models)
	}
	// off 档是审计 N2 的 wire 证据（DSH 真实词表含 off）——夹具丢失它即失效。
	if !sawOff {
		t.Fatal("llm.models fixture lost the `off` effort tier (N2 wire evidence)")
	}
	if !sawDefaultEffort {
		t.Fatal("llm.models fixture lost defaultEffort (ModelEffortCatalog reads it)")
	}
}
