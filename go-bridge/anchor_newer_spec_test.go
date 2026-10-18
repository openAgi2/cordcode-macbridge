package gobridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// anchor-newer spec tests（S-7，方案 v30 §3.8；canonical =
// docs/protocol/bridge-v1.md §Anchor-Newer Incremental Reconciliation — FROZEN SPEC
// + 冻结样本 samples/projection-window-v1/anchor-newer-response-spec.json +
// schema docs/protocol/schema/bridge-v1.types.ts 的三个新接口）。
//
// 纪律（F-33）：本文件是 anchor 样本的**独立读取点**——既有 window-response-spec.json
// 及其精确 key 断言零改动；新类型冻结集独立于 v1 的 frozenProjectionWindowFields，
// 不触碰 frozen v1 字段集。

const anchorNewerRPC = "get_session_projection_anchor_newer"

// Frozen field sets for the anchor-newer types (mirror of bridge-v1.types.ts).
// Schema drift fails the schema-parse test below; fixture drift fails the fixture test.
var frozenAnchorNewerFields = map[string][]string{
	"BridgeGetSessionProjectionAnchorNewerParams": {
		"sessionId", "backendId", "anchorTurnId", "baseDigests", "limit", "correctionCursor",
	},
	"BridgeAnchorNewerScalarState": {
		"execution", "planMode", "sessionMode", "collaborationMode", "goal", "codexGoal",
	},
	"BridgeGetSessionProjectionAnchorNewerResult": {
		"turns", "anchorRefresh", "changedTurns", "removedTurnIds",
		"scalarState", "correctionsPending", "turnTokens", "syncRev",
	},
}

// 两仓字节一致副本的 SHA-256（README 登记值；改样本必须同步 README 与 iOS 仓副本）。
const anchorNewerFixtureSHA256 = "191cc8710262b807d560faa15ae11ee8b4961190068cb3865e1885c0d59c7b71"

func anchorNewerFixture(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(strings.Join([]string{"..", "docs", "protocol", "samples", "projection-window-v1", "anchor-newer-response-spec.json"}, "/"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != anchorNewerFixtureSHA256 {
		t.Fatalf("anchor-newer fixture sha256 drift: got %s want %s (two-repo byte-identical copies + README must be updated together)", got, anchorNewerFixtureSHA256)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func anchorNewerRawObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	return object
}

func anchorNewerSortedKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	object := anchorNewerRawObject(t, raw)
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// 1. Fixture wire-shape: exact key sets for both request forms and both response
// pages; provenance must stay synthetic until the first real capture replaces it.
func TestAnchorNewerSpecFixtureFieldSets(t *testing.T) {
	fixture := anchorNewerFixture(t)
	if got := string(fixture["provenance"]); got != `"synthetic-spec-fixture"` {
		t.Fatalf("provenance = %s, want synthetic-spec-fixture", got)
	}
	if got := string(fixture["rpc"]); got != `"`+anchorNewerRPC+`"` {
		t.Fatalf("rpc = %s, want %q", got, anchorNewerRPC)
	}

	requests := anchorNewerRawObject(t, fixture["requests"])

	// 首包：无 correctionCursor（续页身份标记缺席 = 首包）。
	first := anchorNewerRawObject(t, requests["firstPage"])
	if got, want := anchorNewerSortedKeys(t, requests["firstPage"]), []string{"anchorTurnId", "backendId", "baseDigests", "limit", "sessionId"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first-page request keys = %v, want %v (no correctionCursor)", got, want)
	}
	firstDigests := anchorNewerRawObject(t, first["baseDigests"])
	if len(firstDigests) != 3 {
		t.Fatalf("first-page baseDigests entries = %d, want 3", len(firstDigests))
	}
	if string(firstDigests["turn-old-b"]) != "null" {
		t.Fatalf("baseDigests['turn-old-b'] = %s, want explicit null (token-invalidated semantics)", string(firstDigests["turn-old-b"]))
	}

	// 续页：correctionCursor 存在；复查集含已发项 token（不收缩）。
	continuation := anchorNewerRawObject(t, requests["continuation"])
	if got, want := anchorNewerSortedKeys(t, requests["continuation"]), []string{"anchorTurnId", "backendId", "baseDigests", "correctionCursor", "sessionId"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("continuation request keys = %v, want %v (correctionCursor present)", got, want)
	}
	continuationDigests := anchorNewerRawObject(t, continuation["baseDigests"])
	if len(continuationDigests) != 4 {
		t.Fatalf("continuation baseDigests entries = %d, want 4 (full review set does not shrink across pages)", len(continuationDigests))
	}
	if _, ok := continuationDigests["turn-new-1"]; !ok {
		t.Fatalf("continuation review set must include already-delivered turn token (turn-new-1)")
	}

	responses := anchorNewerRawObject(t, fixture["responses"])

	// 首包响应：anchorRefresh 存在 + correctionsPending 存在。
	if got, want := anchorNewerSortedKeys(t, responses["firstPage"]), []string{"anchorRefresh", "changedTurns", "correctionsPending", "removedTurnIds", "scalarState", "syncRev", "turnTokens", "turns"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first-page response keys = %v, want %v", got, want)
	}
	firstResponse := anchorNewerRawObject(t, responses["firstPage"])
	if anchorNewerRawObject(t, firstResponse["anchorRefresh"])["turnId"] == nil {
		t.Fatalf("anchorRefresh must be a full turn terminal state")
	}
	// turnTokens 覆盖本页交付全集（turns + anchorRefresh + changedTurns），与内容同帧配对。
	firstTokens := anchorNewerRawObject(t, firstResponse["turnTokens"])
	for _, delivered := range []string{"turn-new-1", "turn-watermark", "turn-old-b"} {
		if _, ok := firstTokens[delivered]; !ok {
			t.Fatalf("turnTokens must cover delivered turn %s", delivered)
		}
	}
	scalar := anchorNewerRawObject(t, firstResponse["scalarState"])
	if scalar["execution"] == nil {
		t.Fatalf("scalarState.execution is required (running session semantics)")
	}

	// 续页响应：无 anchorRefresh（不重复）、末页无 correctionsPending、可含 turns 剩余片。
	if got, want := anchorNewerSortedKeys(t, responses["continuation"]), []string{"changedTurns", "removedTurnIds", "scalarState", "syncRev", "turnTokens", "turns"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("continuation response keys = %v, want %v (no anchorRefresh on continuation pages)", got, want)
	}

	// typed error 形状全集。
	var errorShapes []map[string]json.RawMessage
	if err := json.Unmarshal(fixture["errorShapes"], &errorShapes); err != nil {
		t.Fatal(err)
	}
	wantCodes := []string{"cursor_stale", "full.oversized", "protocol.capability_required", "reconciliation.oversized"}
	gotCodes := make([]string, 0, len(errorShapes))
	for _, shape := range errorShapes {
		gotCodes = append(gotCodes, strings.Trim(string(shape["code"]), `"`))
	}
	sort.Strings(gotCodes)
	if !reflect.DeepEqual(gotCodes, wantCodes) {
		t.Fatalf("error shape codes = %v, want %v", gotCodes, wantCodes)
	}
}

// 2. Schema types carry exactly the frozen anchor-newer field sets (catches
// accidental edits to bridge-v1.types.ts without a re-freeze; independent of the
// v1 frozen map above — the v1 interfaces stay untouched).
func TestAnchorNewerSchemaTypesMatchFrozenFields(t *testing.T) {
	raw, err := os.ReadFile(strings.Join([]string{"..", "docs", "protocol", "schema", "bridge-v1.types.ts"}, "/"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for typeName, wantFields := range frozenAnchorNewerFields {
		fields := extractInterfaceFields(t, lines, typeName)
		want := append([]string(nil), wantFields...)
		sort.Strings(want)
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("%s fields = %v, want frozen set %v", typeName, fields, want)
		}
	}
}

// 3. Freeze-boundary guard: the frozen v1 window types must stay untouched by the
// anchor-newer batch (the v1 spec test owns the exact assertions; this is the
// cross-batch boundary note — v1 params/result field sets exclude anchor-newer
// fields by construction, new types carry them).
func TestAnchorNewerDoesNotTouchFrozenV1WindowSample(t *testing.T) {
	raw, err := os.ReadFile(strings.Join([]string{"..", "docs", "protocol", "samples", "projection-window-v1", "window-response-spec.json"}, "/"))
	if err != nil {
		t.Fatal(err)
	}
	var window map[string]json.RawMessage
	if err := json.Unmarshal(raw, &window); err != nil {
		t.Fatal(err)
	}
	// 既有样本仍只描述 v1 result（window/turns/syncRev/resume）。
	response := anchorNewerRawObject(t, window["response"])
	if got, want := anchorNewerSortedKeys(t, window["response"]), []string{"resume", "syncRev", "turns", "window"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("v1 window-response-spec.json response keys drifted: %v, want %v", got, want)
	}
	if _, ok := response["anchorRefresh"]; ok {
		t.Fatalf("v1 sample must not grow anchor-newer fields (independent file discipline, F-33)")
	}
}
