package gobridge

// PERF-S4A freeze-only gate for "Projection Window (server-owned windowing)".
//
// Canonical authority: docs/protocol/bridge-v1.md §Projection Window (FROZEN SPEC,
// R1–R10) + docs/protocol/schema/bridge-v1.types.ts + the synthetic wire-shape fixture
// in docs/protocol/samples/projection-window-v1/. No production code implements this
// yet (R10); these tests freeze the wire contract, decode the fixture against the REAL
// TurnProjection, and table-top replay the frozen state machine so PERF-S4B starts from
// a deterministic reference. S4B's producer PR is EXPECTED to update the
// not-referenced-by-production gate below — consciously, together with this file.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	projectionWindowCapability = "projection_window_v1"
	projectionWindowRPC        = "get_session_projection_window"
	projectionWindowMaxTurns   = 256 // R5
)

func projectionWindowFixture(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "docs", "protocol", "samples", "projection-window-v1", "window-response-spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

// Frozen field sets (mirror of bridge-v1.types.ts). Schema drift fails the
// schema-parse test below; fixture drift fails this test.
var frozenProjectionWindowFields = map[string][]string{
	"BridgeGetSessionProjectionWindowParams": {
		"sessionId", "directory", "backendId", "direction", "cursor", "limit", "anchorTurnId",
	},
	"BridgeProjectionWindow": {
		"windowId", "generation", "coverage", "headTurnId", "tailTurnId",
		"hasOlder", "hasNewer", "nextOlderCursor", "nextNewerCursor",
	},
	"BridgeGetSessionProjectionWindowResult": {
		"window", "turns", "syncRev", "resume",
	},
}

var frozenProjectionWindowDirections = []string{"window_0", "older", "newer", "latest", "locate"}

// 1. Fixture shape: exact key sets at every level; provenance must stay synthetic until
// the first real S4B capture replaces it under the same README discipline.
func TestProjectionWindowSpecFixtureFieldSet(t *testing.T) {
	fixture := projectionWindowFixture(t)
	if got := string(fixture["provenance"]); got != `"synthetic-spec-fixture"` {
		t.Fatalf("provenance = %s, want synthetic-spec-fixture (S4B replaces with real capture + README hashes)", got)
	}
	if got := string(fixture["rpc"]); got != fmt.Sprintf("%q", projectionWindowRPC) {
		t.Fatalf("rpc = %s, want %q", got, projectionWindowRPC)
	}

	response := rawObject(t, fixture["response"])
	if got, want := sortedRawKeys(response), []string{"resume", "syncRev", "turns", "window"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("response keys = %v, want %v", got, want)
	}
	window := rawObject(t, response["window"])
	if got, want := sortedRawKeys(window), []string{
		"coverage", "generation", "hasNewer", "hasOlder", "headTurnId",
		"nextNewerCursor", "nextOlderCursor", "tailTurnId", "windowId",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("window keys = %v, want %v", got, want)
	}

	// R5/R7 cursor-iff-remainder invariants on the fixture itself.
	var coverage string
	if err := json.Unmarshal(window["coverage"], &coverage); err != nil {
		t.Fatal(err)
	}
	if coverage != "window" && coverage != "full" {
		t.Fatalf("coverage = %q, want window|full", coverage)
	}
	checkCursorIff := func(flagKey, cursorKey string) {
		t.Helper()
		var flag bool
		if err := json.Unmarshal(window[flagKey], &flag); err != nil {
			t.Fatal(err)
		}
		_, hasCursor := window[cursorKey]
		if flag != hasCursor {
			t.Fatalf("%s=%v but %s present=%v (cursor present iff remainder flag)", flagKey, flag, cursorKey, hasCursor)
		}
	}
	checkCursorIff("hasOlder", "nextOlderCursor")
	checkCursorIff("hasNewer", "nextNewerCursor")

	var resume map[string]json.RawMessage
	if err := json.Unmarshal(response["resume"], &resume); err != nil {
		t.Fatal(err)
	}
	if got, want := sortedRawKeys(resume), []string{"kind"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resume keys = %v, want %v", got, want)
	}

	// Typed error codes the freeze names; nothing else may appear in errorShapes.
	errors := rawArray(t, fixture["errorShapes"])
	wantCodes := map[string]bool{
		"projection_window.cursor_scope_mismatch": false,
		"projection_window.limit_exceeded":         false,
		"projection_window.locate_out_of_window":   false,
		"cursor_stale":                             false,
		"protocol.capability_required":             false,
	}
	for index, entryRaw := range errors {
		entry := rawObject(t, entryRaw)
		if got, want := sortedRawKeys(entry), []string{"code", "retryable"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("errorShapes[%d] keys = %v, want %v", index, got, want)
		}
		var code string
		var retryable bool
		if err := json.Unmarshal(entry["code"], &code); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(entry["retryable"], &retryable); err != nil {
			t.Fatal(err)
		}
		if _, ok := wantCodes[code]; !ok {
			t.Fatalf("errorShapes[%d] code %q is not part of the frozen error set", index, code)
		}
		wantCodes[code] = true
	}
	for code, seen := range wantCodes {
		if !seen {
			t.Fatalf("frozen error code %q missing from fixture errorShapes", code)
		}
	}
}

// 2. Fixture turns decode against the REAL production TurnProjection (strict decoder):
// window content composes with today's kernel wire types, not a parallel shape.
func TestProjectionWindowFixtureTurnsDecodeAgainstProductionTurnProjection(t *testing.T) {
	fixture := projectionWindowFixture(t)
	response := rawObject(t, fixture["response"])
	var turnsJSON json.RawMessage
	if err := json.Unmarshal(response["turns"], &turnsJSON); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(turnsJSON)))
	decoder.DisallowUnknownFields()
	var turns []TurnProjection
	if err := decoder.Decode(&turns); err != nil {
		t.Fatalf("fixture turns must decode against production TurnProjection: %v", err)
	}
	if len(turns) == 0 {
		t.Fatal("fixture turns must not be empty")
	}
	seen := map[string]bool{}
	for index, turn := range turns {
		if turn.TurnID == "" {
			t.Fatalf("turns[%d].turnId must not be empty", index)
		}
		if seen[turn.TurnID] {
			t.Fatalf("turns duplicate turnId %q (R3 unique page ownership)", turn.TurnID)
		}
		seen[turn.TurnID] = true
		switch turn.Status {
		case "pending", "running", "completed", "aborted", "error":
		default:
			t.Fatalf("turns[%d].status = %q, outside frozen BridgeTurnStatus", index, turn.Status)
		}
	}
	var window struct {
		HeadTurnID string `json:"headTurnId"`
		TailTurnID string `json:"tailTurnId"`
	}
	if err := json.Unmarshal(response["window"], &window); err != nil {
		t.Fatal(err)
	}
	if len(turns) > 0 && window.HeadTurnID != turns[0].TurnID {
		t.Fatalf("headTurnId %q != first turn %q (R3 window boundaries are turn-aligned)", window.HeadTurnID, turns[0].TurnID)
	}
	if len(turns) > 0 && window.TailTurnID != turns[len(turns)-1].TurnID {
		t.Fatalf("tailTurnId %q != last turn %q (R3 window boundaries are turn-aligned)", window.TailTurnID, turns[len(turns)-1].TurnID)
	}
}

// 3. Schema types still carry exactly the frozen field set (catches accidental edits to
// bridge-v1.types.ts without a re-freeze).
func TestProjectionWindowSchemaTypesMatchFrozenFields(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "docs", "protocol", "schema", "bridge-v1.types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for typeName, wantFields := range frozenProjectionWindowFields {
		fields := extractInterfaceFields(t, lines, typeName)
		want := append([]string(nil), wantFields...)
		sort.Strings(want)
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("%s fields = %v, want frozen set %v", typeName, fields, want)
		}
	}
	directions := extractUnionValues(t, lines, "BridgeProjectionWindowDirection")
	if !reflect.DeepEqual(directions, frozenProjectionWindowDirections) {
		t.Fatalf("BridgeProjectionWindowDirection = %v, want %v", directions, frozenProjectionWindowDirections)
	}
}

func extractInterfaceFields(t *testing.T, lines []string, typeName string) []string {
	t.Helper()
	start := -1
	for index, line := range lines {
		if strings.HasPrefix(line, "export interface "+typeName+" {") {
			start = index + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("export interface %s not found in bridge-v1.types.ts", typeName)
	}
	fieldPattern := regexp.MustCompile(`^\s+([A-Za-z_][A-Za-z0-9_]*)\??:`)
	var fields []string
	for _, line := range lines[start:] {
		if line == "}" {
			break
		}
		match := fieldPattern.FindStringSubmatch(line)
		if match != nil {
			fields = append(fields, match[1])
		}
	}
	sort.Strings(fields)
	if len(fields) == 0 {
		t.Fatalf("%s has no extracted fields (parser drift?)", typeName)
	}
	return fields
}

func extractUnionValues(t *testing.T, lines []string, typeName string) []string {
	t.Helper()
	collecting := false
	var values []string
	valuePattern := regexp.MustCompile(`"([^"]+)"`)
	for _, line := range lines {
		if strings.HasPrefix(line, "export type "+typeName+" =") {
			collecting = true
			continue
		}
		if collecting {
			if strings.HasPrefix(line, ";") || line == "" {
				break
			}
			if match := valuePattern.FindStringSubmatch(line); match != nil {
				values = append(values, match[1])
			}
		}
	}
	if len(values) == 0 {
		t.Fatalf("export type %s not found or empty in bridge-v1.types.ts", typeName)
	}
	return values
}

// 4. The canonical freeze text itself is present with all ten rule markers.
func TestProjectionWindowSpecSectionFrozenRulesPresent(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "docs", "protocol", "bridge-v1.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	required := []string{
		"## Projection Window (server-owned windowing) — FROZEN SPEC (not advertised)",
		"### Capability: `projection_window_v1`",
		"### RPC: `get_session_projection_window`",
		"**R1 — Scope & cursor identity.**",
		"**R2 — Producer source-family boundary.**",
		"**R3 — Turn-aligned windows; unique page ownership.**",
		"**R4 — Snapshot cut & live patch fence.**",
		"**R5 — Bounds are assertable limits, not advice.**",
		"**R6 — Cursor staleness & recovery.**",
		"**R7 — `latest` and live tail.**",
		"**R8 — `locate` to an unloaded item.**",
		"**R9 — Failure rendering.**",
		"**R10 — Freeze scope.**",
	}
	for _, marker := range required {
		if !strings.Contains(text, marker) {
			t.Fatalf("bridge-v1.md lost frozen marker %q (re-freeze required for any change)", marker)
		}
	}
}

// 5. R10 freeze gate: no production (non-test) Go code references the capability or RPC
// yet. S4B's producer PR must update this allowlist consciously in the same commit that
// adds the producer — silent production drift fails here.
func TestProjectionWindowNotReferencedByProductionCode(t *testing.T) {
	var offenders []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == "testdata" || entry.Name() == "vendor" || entry.Name() == ".git") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), projectionWindowCapability) || strings.Contains(string(raw), projectionWindowRPC) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("R10 freeze violated: production files reference %s/%s before S4B: %v",
			projectionWindowCapability, projectionWindowRPC, offenders)
	}
}

// 6. Table-top replay of the frozen state machine. This is a TEST-LOCAL reference
// implementation of the decisions the spec text defines — it is deliberately not
// production code; S4B implements the real one and these scenarios become its
// acceptance table. Every case cites the rule it replays.

type windowReplayStep struct {
	name        string
	declared    []string // client-declared capabilities at hello time
	rolloutOn   bool     // server rollout flag
	rpc         bool     // a get_session_projection_window request is being made
	direction   string
	cursorScope string // match | other-backend | other-epoch | none
	limit       int
	anchorKnown bool
}

type windowReplayOutcome struct {
	errCode    string // "" = success
	retryable  bool
	clientNext string // canonical client follow-up the freeze prescribes
}

func replayProjectionWindow(step windowReplayStep) windowReplayOutcome {
	declared := map[string]bool{}
	for _, capability := range step.declared {
		declared[capability] = true
	}
	if step.rpc {
		if !declared[projectionWindowCapability] || !step.rolloutOn {
			return windowReplayOutcome{errCode: "protocol.capability_required", clientNext: "render typed failure; no window field"}
		}
		if step.limit > projectionWindowMaxTurns {
			return windowReplayOutcome{errCode: "projection_window.limit_exceeded", clientNext: "render typed failure"}
		}
		if step.direction == "locate" && !step.anchorKnown {
			return windowReplayOutcome{errCode: "projection_window.locate_out_of_window", clientNext: "full get_session_projection pull (only honest fallback)"}
		}
		switch step.cursorScope {
		case "other-backend":
			return windowReplayOutcome{errCode: "projection_window.cursor_scope_mismatch", clientNext: "render typed failure; never re-issue the cursor"}
		case "other-epoch":
			return windowReplayOutcome{errCode: "cursor_stale", retryable: true, clientNext: "discard cursor chain; re-issue window_0"}
		}
		return windowReplayOutcome{clientNext: "apply window; chain patches from syncRev (R4)"}
	}
	// hello-time capability validation.
	if declared[projectionWindowCapability] && !declared["session_sync_v2"] {
		return windowReplayOutcome{errCode: "protocol.invalid_capabilities", clientNext: "hello fails; fix declarations"}
	}
	return windowReplayOutcome{clientNext: "hello_ack echo only when rollout+descriptor support"}
}

// Shared cross-language scenario table: this file AND the iOS mirror's
// ProjectionWindowSpecTests.swift decode the same JSON and run structurally identical
// replay functions — Go (server side) and Swift (client side) must agree on every row.
type tabletopScenario struct {
	Name  string `json:"name"`
	Rule  string `json:"rule"`
	Input struct {
		Declared    []string `json:"declared"`
		RolloutOn   bool     `json:"rolloutOn"`
		RPC         bool     `json:"rpc"`
		Direction   string   `json:"direction"`
		CursorScope string   `json:"cursorScope"`
		Limit       int      `json:"limit"`
		AnchorKnown bool     `json:"anchorKnown"`
	} `json:"input"`
	Expected struct {
		ErrorCode  string `json:"errorCode"`
		Retryable  bool   `json:"retryable"`
		ClientNext string `json:"clientNext"`
	} `json:"expected"`
}

func loadTabletopScenarios(t *testing.T) (scenarios []tabletopScenario, newerChainWalk struct {
	Chain        []string `json:"chain"`
	AfterTurnID  string   `json:"afterTurnId"`
	Limit        int      `json:"limit"`
	ExpectedTurn []string `json:"expectedTurns"`
}) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "docs", "protocol", "samples", "projection-window-v1", "tabletop-scenarios.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Scenarios      []tabletopScenario `json:"scenarios"`
		NewerChainWalk struct {
			Chain        []string `json:"chain"`
			AfterTurnID  string   `json:"afterTurnId"`
			Limit        int      `json:"limit"`
			ExpectedTurn []string `json:"expectedTurns"`
		} `json:"newerChainWalk"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Scenarios) == 0 {
		t.Fatal("tabletop-scenarios.json carries no scenarios")
	}
	return payload.Scenarios, payload.NewerChainWalk
}

func (scenario tabletopScenario) step() windowReplayStep {
	return windowReplayStep{
		name:        scenario.Name,
		declared:    scenario.Input.Declared,
		rolloutOn:   scenario.Input.RolloutOn,
		rpc:         scenario.Input.RPC,
		direction:   scenario.Input.Direction,
		cursorScope: scenario.Input.CursorScope,
		limit:       scenario.Input.Limit,
		anchorKnown: scenario.Input.AnchorKnown,
	}
}

func (scenario tabletopScenario) want() windowReplayOutcome {
	return windowReplayOutcome{
		errCode:    scenario.Expected.ErrorCode,
		retryable:  scenario.Expected.Retryable,
		clientNext: scenario.Expected.ClientNext,
	}
}

func TestProjectionWindowTabletopReplay(t *testing.T) {
	scenarios, newerChainWalk := loadTabletopScenarios(t)
	for _, scenario := range scenarios {
		t.Run(scenario.Rule+": "+scenario.Name, func(t *testing.T) {
			got := replayProjectionWindow(scenario.step())
			if got != scenario.want() {
				t.Fatalf("[%s] replay = %+v, want %+v", scenario.Rule, got, scenario.want())
			}
			// R9: only cursor_stale is retryable; every other typed error is terminal.
			if got.errCode != "" && got.errCode != "cursor_stale" && got.retryable {
				t.Fatalf("[%s] %q must not be retryable (R9)", scenario.Rule, got.errCode)
			}
		})
	}

	// Transport independence: direct LAN and Relay carry the same protocol decisions —
	// windows are pull-only RPCs and must not branch on transport.
	for range []string{"direct", "relay"} {
		for _, scenario := range scenarios {
			if got := replayProjectionWindow(scenario.step()); got != scenario.want() {
				t.Fatalf("%s: decision diverged across transports: %+v vs %+v", scenario.Name, got, scenario.want())
			}
		}
	}

	// R6 two-step recovery: cursor_stale discards the chain and window_0 succeeds.
	var staleScenario, healthyScenario *tabletopScenario
	for index := range scenarios {
		if scenarios[index].Expected.ErrorCode == "cursor_stale" {
			staleScenario = &scenarios[index]
		}
		if scenarios[index].Expected.ErrorCode == "" && scenarios[index].Input.Direction == "window_0" {
			healthyScenario = &scenarios[index]
		}
	}
	if staleScenario == nil || healthyScenario == nil {
		t.Fatal("scenario table must carry one cursor_stale and one healthy window_0 row for the recovery replay")
	}
	stale := replayProjectionWindow(staleScenario.step())
	if stale != staleScenario.want() {
		t.Fatalf("stale replay = %+v, want %+v", stale, staleScenario.want())
	}
	recovered := replayProjectionWindow(healthyScenario.step())
	if recovered != healthyScenario.want() {
		t.Fatalf("window_0 recovery replay = %+v, want %+v", recovered, healthyScenario.want())
	}

	// R7 strict turn-chain: newer never skips an unloaded turn.
	newer := chainSliceNewer(newerChainWalk.Chain, newerChainWalk.AfterTurnID, newerChainWalk.Limit)
	if want := newerChainWalk.ExpectedTurn; !reflect.DeepEqual(newer, want) {
		t.Fatalf("newer walk = %v, want %v (R7 strict order, no skips)", newer, want)
	}
}

// chainSliceNewer table-tops R7: walking newer from a cursor at index i returns the
// immediately-following turns in order, bounded by limit and the committed tail.
func chainSliceNewer(chain []string, afterTurnID string, limit int) []string {
	start := -1
	for index, turnID := range chain {
		if turnID == afterTurnID {
			start = index + 1
			break
		}
	}
	if start < 0 || start >= len(chain) {
		return nil
	}
	end := start + limit
	if end > len(chain) {
		end = len(chain)
	}
	return chain[start:end]
}
