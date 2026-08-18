package gobridge

// mapSession 的 session 级模型下发（路线图 Phase 1 / 开工清单 6）：OpenCode
// 上游 session 对象把绑定的模型携带为嵌套 `model:{id,providerID,variant}`（活体
// 取证 2026-08-18，managed server GET /session；think.md 2026-07-06：模型必须
// session 级绑定，prompt body 的模型字段被忽略）。mapSession 必须提取它；
// 未绑定模型的 session（无 model 键）保持空——不造数。

import "testing"

func TestMapSessionExtractsNestedUpstreamModel(t *testing.T) {
	raw := map[string]interface{}{
		"id":    "ses_fixture",
		"title": "Fixture session",
		"time":  map[string]interface{}{"created": float64(1710000000000), "updated": float64(1710000500000)},
		"model": map[string]interface{}{"id": "mimo-v2.5-free", "providerID": "opencode", "variant": "default"},
	}
	mapped := mapSession(raw)
	if got := mapped["effectiveModelId"]; got != "mimo-v2.5-free" {
		t.Fatalf("effectiveModelId = %#v, want mimo-v2.5-free (nested upstream model.id)", got)
	}
	if got := mapped["effectiveProviderId"]; got != "opencode" {
		t.Fatalf("effectiveProviderId = %#v, want opencode (nested upstream model.providerID)", got)
	}
}

func TestMapSessionWithoutBoundModelStaysUnmodeled(t *testing.T) {
	raw := map[string]interface{}{
		"id":    "ses_nomodel",
		"title": "No bound model",
		"time":  map[string]interface{}{"created": float64(1710000000000), "updated": float64(1710000500000)},
	}
	mapped := mapSession(raw)
	if got := mapped["effectiveModelId"]; got != "" {
		t.Fatalf("effectiveModelId = %#v, want empty (no fabricated model)", got)
	}
	if got := mapped["effectiveProviderId"]; got != "" {
		t.Fatalf("effectiveProviderId = %#v, want empty", got)
	}
}

func TestMapSessionNestedModelWinsOverFlatAliases(t *testing.T) {
	raw := map[string]interface{}{
		"id":               "ses_both",
		"title":            "Both sources",
		"time":             map[string]interface{}{"created": float64(1710000000000), "updated": float64(1710000500000)},
		"effectiveModelId": "stale-alias",
		"model":            map[string]interface{}{"id": "real-model", "providerID": "real-provider"},
	}
	mapped := mapSession(raw)
	if got := mapped["effectiveModelId"]; got != "real-model" {
		t.Fatalf("effectiveModelId = %#v, want real-model (nested model object is authoritative)", got)
	}
	if got := mapped["effectiveProviderId"]; got != "real-provider" {
		t.Fatalf("effectiveProviderId = %#v, want real-provider", got)
	}
}
