package dshweb

// testdata 夹具契约门：session/list / session/modelCatalog / agentPresets/list
// 的脱敏活体捕获（typert 网关代，dsh 0.1.7-alpha.1，2026-09-23 实跑；上一代
// 点分隔捕获已于 wire 迁移时退役）。runtime 升级破坏契约时这里先红，由设计
// 阶段重新取证，而不是在 client 现场猜格式（与 grokbuild catalog_session_
// list_test.go 的格式冻结门同构）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const (
	sessionListFixture  = "testdata/session_list_sanitized.json"
	modelCatalogFixture = "testdata/model_catalog_sanitized.json"
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

func TestModelCatalogFixtureDecodesAgainstPinnedSchema(t *testing.T) {
	var val modelCatalogValue
	loadFixtureValue(t, modelCatalogFixture, &val)

	if len(val.Groups) == 0 {
		t.Fatal("session/modelCatalog fixture without provider groups")
	}
	if val.Default.Provider == "" || val.Default.Model == "" {
		t.Fatalf("fixture lost the default selection: %+v", val.Default)
	}
	if len(val.RoutableProviders) == 0 {
		t.Fatal("fixture lost routableProviders")
	}
	models, sawOff := 0, false
	for _, g := range val.Groups {
		for _, m := range g.Models {
			models++
			if m.ID == "" || m.Name == "" {
				t.Fatalf("model without id/name in group %s", g.ID)
			}
			if m.Reasoning == nil {
				continue
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
		t.Fatalf("modelCatalog fixture too thin: %d models", models)
	}
	// off 档是审计 N2 的 wire 证据（DSH 真实词表含 off）——夹具丢失它即失效。
	if !sawOff {
		t.Fatal("modelCatalog fixture lost the `off` effort tier (N2 wire evidence)")
	}
	// defaultEffort：上一代捕获携带它；typert 网关代的活体目录（2026-09-23
	// 捕获）不再携带 —— EffortsForModel 对缺失返回空默认档，不伪造。
}

// ---- G4 sample-gate fixtures (2026-08-28 live captures; docs G4 深对齐 evidence) ----
//
// agentPreset.list / host.describe / session.list(non-null contextBreakdown+permissions,
// zero-pressure anomaly) / permission preset transcript chain。整批问答（question batch）
// 在全部 24 个活体 transcript 的事件类型普查中零实例（11053 assistant/chunk、684
// assistant/message、0 question-batch）——按样本门保持不实施，不猜形状。

const (
	agentPresetListFixture     = "testdata/agentpreset_list_sanitized.json"
	sessionListG4Fixture       = "testdata/session_list_g4_sanitized.json"
	permissionPresetEventsFixt = "testdata/permission_preset_events_sanitized.json"
)

func TestAgentPresetListFixtureDecodesAgainstPinnedSchema(t *testing.T) {
	var val agentPresetListValue
	loadFixtureValue(t, agentPresetListFixture, &val)

	if len(val.Presets) < 4 {
		t.Fatalf("agentPresets/list fixture lost presets: %d", len(val.Presets))
	}
	byID := map[string]apiAgentPresetEntry{}
	for _, p := range val.Presets {
		if p.ID == "" {
			t.Fatalf("preset without id: %+v", p)
		}
		byID[p.ID] = p
	}
	for _, id := range []string{"standard", "ptc", "minimal", "cordis"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("fixture lost official preset %q", id)
		}
	}
	if !byID["standard"].IsDefault {
		t.Fatal("standard must stay the default preset (live capture 2026-09-23)")
	}
}

func TestSessionListG4FixtureCarriesNonNullBreakdownAndPermissions(t *testing.T) {
	var val sessionListValue
	loadFixtureValue(t, sessionListG4Fixture, &val)

	sawBreakdown, sawPermissions, sawPressure := false, false, false
	for _, it := range val.Items {
		vals := it.Projections.Values
		if vals == nil {
			continue
		}
		var cp apiContextPressure
		if raw, ok := vals["contextPressure"]; ok && raw != nil {
			if err := json.Unmarshal(raw, &cp); err != nil {
				t.Fatalf("row %s contextPressure decode: %v", it.SessionID, err)
			}
		}
		if raw, ok := vals["contextBreakdown"]; ok && raw != nil {
			var br apiContextBreakdown
			if err := json.Unmarshal(raw, &br); err != nil {
				t.Fatalf("row %s contextBreakdown decode: %v", it.SessionID, err)
			}
			if br.SystemTokens > 0 || br.ToolsTokens > 0 || br.MessageTokens > 0 {
				sawBreakdown = true
			}
		}
		if raw, ok := vals["permissions"]; ok && raw != nil {
			var perm struct {
				CurrentValue string `json:"currentValue"`
			}
			if err := json.Unmarshal(raw, &perm); err != nil {
				t.Fatalf("row %s permissions decode: %v", it.SessionID, err)
			}
			if perm.CurrentValue != "" {
				sawPermissions = true
			}
		}
		// 真实压力行（projected>0 且 window>0）：新代捕获的活体证据，decode 不得漂移。
		if cp.ProjectedTokens != nil && cp.ContextWindow != nil &&
			*cp.ProjectedTokens > 0 && *cp.ContextWindow > 0 {
			sawPressure = true
		}
	}
	if !sawBreakdown {
		t.Fatal("fixture lost the non-null contextBreakdown rows (G4 official composition evidence)")
	}
	if !sawPermissions {
		t.Fatal("fixture lost the non-null permissions projection (G4 preset forcing evidence)")
	}
	if !sawPressure {
		t.Fatal("fixture lost the live pressure row (projected>0, window>0)")
	}
}

func TestPermissionPresetEventsFixtureChain(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("", permissionPresetEventsFixt))
	if err != nil {
		t.Fatalf("read %s: %v", permissionPresetEventsFixt, err)
	}
	var doc struct {
		Events []map[string]json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", permissionPresetEventsFixt, err)
	}
	var seq []string
	for _, e := range doc.Events {
		var typ string
		if err := json.Unmarshal(e["type"], &typ); err != nil {
			t.Fatalf("event without type: %v", err)
		}
		seq = append(seq, typ)
	}
	want := []string{"session", "permission/preset", "sandbox/mode", "approval/policy",
		"session/end-seed", "command/run", "permission/preset", "sandbox/mode", "approval/policy"}
	if len(seq) != len(want) {
		t.Fatalf("event chain length = %d (%v), want %d", len(seq), seq, len(want))
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("event[%d] = %q, want %q", i, seq[i], want[i])
		}
	}
	// slash-channel switch 的 command/run 必须带 name=permission + source.kind=user。
	sawSwitch := false
	for _, e := range doc.Events {
		var typ string
		json.Unmarshal(e["type"], &typ)
		if typ != "command/run" {
			continue
		}
		var data struct {
			CommandID string `json:"commandId"`
			Name      string `json:"name"`
			Args      string `json:"args"`
			Source    struct {
				Kind string `json:"kind"`
			} `json:"source"`
		}
		if err := json.Unmarshal(e["data"], &data); err != nil {
			t.Fatalf("command/run data decode: %v", err)
		}
		if data.Name == "permission" && data.Source.Kind == "user" && data.Args != "" {
			sawSwitch = true
		}
	}
	if !sawSwitch {
		t.Fatal("fixture lost the slash-channel permission switch command/run")
	}
}

// host.describe 已随 typert 网关退役（claimsEndpoint 无 host 命名空间）；
// 其夹具与版本钉子一并退役，API 身份以 npm 包版本 + 上游 commit 锚定。
