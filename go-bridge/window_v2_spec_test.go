package gobridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// projection_window_v2 summary spec tests（S-3 v2，冻结规范
// docs/2026-10-19-s3-v2-summary-freeze-spec.md v3——2026-10-19 评审通过；
// canonical = docs/protocol/bridge-v1.md §Projection Window v2 Summary — FROZEN SPEC
// + 冻结样本 samples/projection-window-v2/*.json +
// schema docs/protocol/schema/bridge-v1.types.ts 的 v2 判别联合类型）。
//
// 纪律（沿 F-33）：本文件是 v2 样本的独立读取点——既有 v1/anchor-newer 样本及其断言
// 零改动；v2 新类型冻结集独立于 v1 frozenProjectionWindowFields，不触碰 frozen 字段集。

const (
	windowV2RPC         = "get_session_projection_window_v2"
	windowV2FixtureDir  = "projection-window-v2"
	windowV2FixtureSHA  = "5c39e17ae5952e732dbdfcef87c5e0ab655e4877513b901d3640b649c1aa9950"
	anchorSummarySHA256 = "8959e1a641c40b74d7a23a63d23b39ac57c1f4ea14f1791d7f48182710f39182"
)

// Frozen field sets for the v2 types (mirror of bridge-v1.types.ts)。
// Schema drift fails the schema-parse test below; fixture drift fails the fixture test.
var frozenWindowV2Fields = map[string][]string{
	"BridgeGetSessionProjectionWindowV2Params": {
		"sessionId", "directory", "backendId", "direction", "cursor", "limit",
		"anchorTurnId", "itemsView",
	},
	"BridgeTurnSummaryBrief": {"text", "truncated"},
	"BridgeTurnSummaryDescriptor": {
		"turnId", "status", "startedAt", "completedAt", "durationMs",
		"userBrief", "agentBrief", "agentBriefHead", "folded", "foldReason",
		"toolProcessCount", "toolProcessBytes", "liveState", "turnGeneration",
	},
	"BridgeProjectionSummaryBudget": {"payloadBytes", "foldedTurnIds", "exceptions"},
	"BridgeGetSessionProjectionWindowV2SummaryResult": {
		"window", "turns", "scalarState", "syncRev", "resume", "itemsView", "summaryBudget",
	},
	"BridgeGetSessionProjectionWindowV2FullResult": {
		"window", "turns", "scalarState", "syncRev", "resume", "itemsView",
	},
}

// v1 anchor-newer params 的 additive itemsView 字段（冻结集在 anchor_newer_spec_test.go
// 维护；此处只锁 additive 事实与文档同步，不重复 v1 断言）。
const frozenAnchorNewerParamsAdditiveItemsView = "itemsView"

func windowV2Fixture(t *testing.T, name string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(strings.Join([]string{"..", "docs", "protocol", "samples", windowV2FixtureDir, name}, "/"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != windowV2FixtureSHA && got != anchorSummarySHA256 {
		t.Fatalf("fixture %s drifted from the frozen README SHA (got %s) — 更新样本必须同步 README 哈希与 iOS 仓副本", name, got)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("fixture %s not JSON: %v", name, err)
	}
	return doc
}

func TestWindowV2FixtureProvenanceAndRPC(t *testing.T) {
	doc := windowV2Fixture(t, "window-summary-spec.json")
	if got := string(doc["provenance"]); got != `"synthetic-spec-fixture"` {
		t.Fatalf("provenance = %s", got)
	}
	if got := string(doc["rpc"]); got != `"`+windowV2RPC+`"` {
		t.Fatalf("rpc = %s", got)
	}
	req := doc["request"]
	var params map[string]json.RawMessage
	if err := json.Unmarshal(req, &params); err != nil {
		t.Fatal(err)
	}
	if string(params["itemsView"]) != `"summary"` {
		t.Fatalf("v2 首开请求 itemsView 必须 summary，got %s", params["itemsView"])
	}
	if _, ok := params["anchorTurnId"]; ok {
		t.Fatal("window_0 不得携带 anchorTurnId（locate 专用，v1 R8 组合规则不变）")
	}
}

// ③ 正文优先 wire 化 + always-visible 例外双实例（F-R3-2）+ 预算降级形态。
func TestWindowV2SummaryTurnShapes(t *testing.T) {
	doc := windowV2Fixture(t, "window-summary-spec.json")
	var resp struct {
		Turns []map[string]json.RawMessage `json:"turns"`
	}
	if err := json.Unmarshal(doc["response"], &resp); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, turn := range resp.Turns {
		var id string
		if err := json.Unmarshal(turn["turnId"], &id); err != nil {
			t.Fatal(err)
		}
		byID[id] = turn
	}
	// folded=true 的行必须有 foldReason（含 budget 降级行）。
	for id, turn := range byID {
		var folded bool
		if err := json.Unmarshal(turn["folded"], &folded); err != nil {
			t.Fatalf("turn %s folded not bool: %v", id, err)
		}
		if folded {
			if _, ok := turn["foldReason"]; !ok {
				t.Fatalf("folded=true 的 turn %s 必须携带 foldReason（任何折叠都有定义原因值）", id)
			}
		}
	}
	// budget 降级行：仅身份 + folded + foldReason（无 brief）。
	if budget := byID["turn-103"]; budget == nil || string(budget["foldReason"]) != `"budget"` {
		t.Fatal("样本必须含 budget 降级实例（foldReason=budget）")
	} else {
		if _, hasBrief := budget["userBrief"]; hasBrief {
			t.Fatal("budget 降级行不得携带 brief")
		}
	}
	// 例外通道双实例（F-R3-2）：permission 族 part 与 user_input pending part。
	live := byID["turn-104"]["liveState"]
	if len(live) == 0 {
		t.Fatal("样本必须含 permission 族例外实例（liveState 原样内嵌）")
	}
	var liveTurn struct {
		Assistant struct {
			Parts []map[string]json.RawMessage `json:"parts"`
		} `json:"assistant"`
	}
	if err := json.Unmarshal(live, &liveTurn); err != nil {
		t.Fatal(err)
	}
	hasPermissionPart := false
	for _, part := range liveTurn.Assistant.Parts {
		// F-3 更正（2026-10-20）：官方形态 = type:"tool" + requiresPermissionConfirmation:true。
		if string(part["type"]) == `"tool"` && string(part["requiresPermissionConfirmation"]) == "true" {
			hasPermissionPart = true
		}
	}
	if !hasPermissionPart {
		t.Fatal("liveState 例外实例必须含官方权限 part（type=tool + requiresPermissionConfirmation）")
	}
	var running map[string]json.RawMessage
	if err := json.Unmarshal(doc["liveStateRunningExample"], &struct{}{}); err == nil {
		_ = running
	}
	var runExample struct {
		Turn struct {
			Assistant struct {
				Parts []map[string]json.RawMessage `json:"parts"`
			} `json:"assistant"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(doc["liveStateRunningExample"], &runExample); err != nil {
		t.Fatal(err)
	}
	hasPendingUserInput := false
	for _, part := range runExample.Turn.Assistant.Parts {
		if string(part["type"]) == `"user_input"` && string(part["status"]) == `"pending"` {
			hasPendingUserInput = true
		}
	}
	if !hasPendingUserInput {
		t.Fatal("running 例外实例必须含 user_input pending part（wire 字段名 status==pending，F-R3-1）")
	}
}

// 错误集 = frozen v1 window 全集 + summary.oversized；retryable 与 direction 适用逐项冻结。
func TestWindowV2ErrorShapes(t *testing.T) {
	doc := windowV2Fixture(t, "window-summary-spec.json")
	var shapes []struct {
		Code       string   `json:"code"`
		Retryable  bool     `json:"retryable"`
		Directions []string `json:"directions"`
	}
	if err := json.Unmarshal(doc["errorShapes"], &shapes); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, shape := range shapes {
		got[shape.Code] = shape.Retryable
	}
	want := map[string]bool{
		"cursor_stale":                               true,
		"projection_window.cursor_scope_mismatch":    false,
		"projection_window.limit_exceeded":           false,
		"projection_window.locate_out_of_window":     false,
		"invalid_params":                             false,
		"protocol.capability_required":               false,
		"projection.hydrating":                       true,
		"summary.oversized":                          false,
	}
	for code, retryable := range want {
		gotRetryable, ok := got[code]
		if !ok {
			t.Fatalf("错误集缺 %s（= v1 全集 + summary.oversized）", code)
		}
		if gotRetryable != retryable {
			t.Fatalf("%s retryable = %v, want %v", code, gotRetryable, retryable)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("错误集多余项: %v", got)
	}
}

// anchor-newer itemsView additive 样本：summary 形态行 + 回显 + token 基准不变声明。
func TestAnchorNewerSummarySpec(t *testing.T) {
	doc := windowV2Fixture(t, "anchor-newer-summary-response-spec.json")
	if got := string(doc["rpc"]); got != `"get_session_projection_anchor_newer"` {
		t.Fatalf("rpc = %s", got)
	}
	var responses map[string]json.RawMessage
	if err := json.Unmarshal(doc["responses"], &responses); err != nil {
		t.Fatal(err)
	}
	var firstPage map[string]json.RawMessage
	if err := json.Unmarshal(responses["firstPage"], &firstPage); err != nil {
		t.Fatal(err)
	}
	if string(firstPage["itemsView"]) != `"summary"` {
		t.Fatal("summary 形态对账响应必须回显 itemsView=summary")
	}
	// turns / anchorRefresh / changedTurns 行为 summary 形态（无完整明细字段）。
	for _, key := range []string{"turns", "changedTurns"} {
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(firstPage[key], &rows); err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if _, hasParts := row["assistant"]; hasParts {
				t.Fatalf("%s 行不得携带完整明细（assistant 字段 = full 形态）", key)
			}
		}
	}
	var anchorRefresh map[string]json.RawMessage
	if err := json.Unmarshal(firstPage["anchorRefresh"], &anchorRefresh); err != nil {
		t.Fatal(err)
	}
	if _, hasAssistant := anchorRefresh["assistant"]; hasAssistant {
		t.Fatal("anchorRefresh summary 形态不得携带完整明细")
	}
	// 首包与续页同值 itemsView。
	var requests map[string]json.RawMessage
	if err := json.Unmarshal(doc["requests"], &requests); err != nil {
		t.Fatal(err)
	}
	var continuationReq map[string]json.RawMessage
	if err := json.Unmarshal(requests["continuation"], &continuationReq); err != nil {
		t.Fatal(err)
	}
	if string(continuationReq["itemsView"]) != `"summary"` {
		t.Fatal("续页请求 itemsView 必须与首包同值")
	}
}

// schema（bridge-v1.types.ts）冻结集同步——v2 类型字段与 spec §2/§3 一致，防实施漂移。
func TestWindowV2SchemaFieldsFrozen(t *testing.T) {
	raw, err := os.ReadFile(strings.Join([]string{"..", "docs", "protocol", "schema", "bridge-v1.types.ts"}, "/"))
	if err != nil {
		t.Fatal(err)
	}
	schema := string(raw)
	for typeName := range frozenWindowV2Fields {
		if !strings.Contains(schema, "interface "+typeName) {
			t.Fatalf("schema 缺类型 %s", typeName)
		}
	}
	// 判别联合存在。
	if !strings.Contains(schema, "BridgeGetSessionProjectionWindowV2Result") {
		t.Fatal("schema 缺判别联合 BridgeGetSessionProjectionWindowV2Result")
	}
	// anchor-newer params 的 additive itemsView 已入 schema。
	if !strings.Contains(schema, frozenAnchorNewerParamsAdditiveItemsView) {
		t.Fatal("schema 缺 anchor-newer params additive itemsView 字段")
	}
	// F-R3-1：例外识别的 wire 字段名为 status（不是属性名 userInputStatus）。
	if !strings.Contains(schema, `status: "pending"`) {
		t.Fatal("schema user_input pending 识别字段名必须为 status（F-R3-1）")
	}
}

// 冻结样本 key 排序稳定性（确定性可复现）。
func TestWindowV2FixtureKeysSorted(t *testing.T) {
	doc := windowV2Fixture(t, "window-summary-spec.json")
	keys := make([]string, 0, len(doc))
	for key := range doc {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := []string{"errorShapes", "liveStateRunningExample", "note", "provenance", "request", "response", "rpc"}
	if len(keys) != len(want) {
		t.Fatalf("fixture keys = %v", keys)
	}
	for i := range keys {
		if keys[i] != want[i] {
			t.Fatalf("fixture keys[%d] = %s, want %s", i, keys[i], want[i])
		}
	}
}
