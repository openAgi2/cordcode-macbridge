package gobridge

import (
	"context"
	"errors"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// §11.8 (turn_detail_chunks_v1) contract-freeze spec tests. These pin the
// owner-final-ruling wire decisions BEFORE the phase5 handlers exist:
// v2 reasonCode closed set (max_pages/max_bytes REMOVED), the manifest-op
// validation/apply semantics, the chunk payload + blob ack shapes, the
// hello negotiation, and the shared production gate surfaces.

func TestTurnDetailChunksReasonCodeSetV2(t *testing.T) {
	for _, code := range []string{"upstream_error", "timeout", "stale_turn", "interrupted", "unsupported_item_type", "page_oversize"} {
		if !TurnDetailChunksReasonCodes[code] {
			t.Errorf("v2 closed set must contain %q", code)
		}
	}
	// Owner final ruling 2026-08-30: permanent per-turn caps abolished — the
	// v1-only codes must NOT be emittable on the v2 path.
	for _, banned := range []string{"max_pages", "max_bytes"} {
		if TurnDetailChunksReasonCodes[banned] {
			t.Errorf("v2 closed set must NOT contain %q (permanent gates abolished)", banned)
		}
	}
	if core.TurnDetailLazyProductionEnabled && len(TurnStateReasonCodes) < len(TurnDetailChunksReasonCodes) {
		// sanity only: both sets exist independently; v1 keeps its own frozen set.
		t.Logf("v1 set %d entries, v2 set %d entries", len(TurnStateReasonCodes), len(TurnDetailChunksReasonCodes))
	}
}

func TestTurnStateOpsV2Validation(t *testing.T) {
	valid := []TurnStateOp{
		{TurnID: "t1", DetailLoadState: DetailStateLoading, TurnGeneration: 0},
		{TurnID: "t1", DetailLoadState: DetailStatePartial, TurnGeneration: 0, ManifestRev: 3, ItemCount: 15, TotalBytes: 4096},
		{TurnID: "t1", DetailLoadState: DetailStateLoaded, TurnGeneration: 0, ManifestRev: 4, ItemCount: 20, TotalBytes: 8192},
		{TurnID: "t1", DetailLoadState: DetailStateFailed, ReasonCode: "upstream_error", TurnGeneration: 0, ManifestRev: 2, ItemCount: 9},
		{TurnID: "t1", DetailLoadState: DetailStateFailed, ReasonCode: ReasonCodePageOversize, TurnGeneration: 0},
	}
	if err := ValidateTurnStateOpsV2(valid); err != nil {
		t.Fatalf("valid v2 batch rejected: %v", err)
	}
	invalid := []struct {
		name string
		ops  []TurnStateOp
	}{
		{"partial with reasonCode", []TurnStateOp{{TurnID: "t", DetailLoadState: DetailStatePartial, ReasonCode: "timeout", TurnGeneration: 1}}},
		{"failed with v1-only code max_bytes", []TurnStateOp{{TurnID: "t", DetailLoadState: DetailStateFailed, ReasonCode: "max_bytes", TurnGeneration: 1}}},
		{"failed with v1-only code max_pages", []TurnStateOp{{TurnID: "t", DetailLoadState: DetailStateFailed, ReasonCode: "max_pages", TurnGeneration: 1}}},
		{"failed without reasonCode", []TurnStateOp{{TurnID: "t", DetailLoadState: DetailStateFailed, TurnGeneration: 1}}},
		{"v1-style loaded with reasonCode", []TurnStateOp{{TurnID: "t", DetailLoadState: DetailStateLoaded, ReasonCode: "timeout", TurnGeneration: 1}}},
		{"unknown state", []TurnStateOp{{TurnID: "t", DetailLoadState: "paused", TurnGeneration: 1}}},
		{"itemCount without manifestRev", []TurnStateOp{{TurnID: "t", DetailLoadState: DetailStatePartial, TurnGeneration: 1, ItemCount: 5}}},
		{"negative manifest summary", []TurnStateOp{{TurnID: "t", DetailLoadState: DetailStatePartial, TurnGeneration: 1, ManifestRev: -1}}},
		{"empty turnId", []TurnStateOp{{TurnID: "", DetailLoadState: DetailStateLoading, TurnGeneration: 1}}},
		{"negative generation", []TurnStateOp{{TurnID: "t", DetailLoadState: DetailStateLoading, TurnGeneration: -1}}},
	}
	for _, tc := range invalid {
		if err := ValidateTurnStateOpsV2(tc.ops); err == nil {
			t.Errorf("%s: expected rejection", tc.name)
		}
	}
}

func TestTurnStateOpsV2ApplyStampsManifestAndFences(t *testing.T) {
	proj := &SessionProjection{SessionID: "s", Turns: []TurnProjection{
		{TurnID: "t1", TurnGeneration: 2, DetailLoadState: DetailStateFailed, DetailReasonCode: "timeout"},
		{TurnID: "t2", TurnGeneration: 0},
	}}
	ops := []TurnStateOp{
		{TurnID: "t1", DetailLoadState: DetailStatePartial, TurnGeneration: 2, ManifestRev: 5, ItemCount: 40, TotalBytes: 12345},
	}
	if err := ApplyTurnStateOpsV2(proj, ops); err != nil {
		t.Fatal(err)
	}
	turn := proj.Turns[0]
	if turn.DetailLoadState != DetailStatePartial || turn.DetailReasonCode != "" {
		t.Fatalf("state/reason = %q/%q, want partial/\"\" (loading-family clears reason)", turn.DetailLoadState, turn.DetailReasonCode)
	}
	if turn.DetailManifestRev != 5 || turn.DetailItemCount != 40 || turn.DetailTotalBytes != 12345 {
		t.Fatalf("manifest summary not stamped: %+v", turn)
	}
	// Generation fence: stale op must fail typed and leave the projection untouched.
	stale := []TurnStateOp{{TurnID: "t1", DetailLoadState: DetailStateLoaded, TurnGeneration: 1, ManifestRev: 6}}
	before := proj.Turns[0]
	if err := ApplyTurnStateOpsV2(proj, stale); err == nil {
		t.Fatal("stale generation op must fail")
	} else if !errors.Is(err, ErrTurnStateStale) {
		t.Fatalf("err = %v, want ErrTurnStateStale", err)
	}
	if proj.Turns[0] != before {
		t.Fatal("failed apply must not mutate the projection")
	}
	// Unknown turn fails closed.
	if err := ApplyTurnStateOpsV2(proj, []TurnStateOp{{TurnID: "nope", DetailLoadState: DetailStateLoading, TurnGeneration: 0}}); err == nil {
		t.Fatal("unknown turn must fail")
	}
}

func TestTurnDetailChunkPayloadWireShape(t *testing.T) {
	payload := TurnDetailChunkPayload{
		TurnID: "turn-42", ManifestRev: 7, Seq: 12,
		Items: []ProjectionPart{{Type: "text", Text: "step"}},
		Oversize: []TurnDetailOversizeRef{{
			ItemID: "item-9", Handle: "blob-abc", Type: "commandExecution",
			TotalBytes: 1057417, Preview: "head…", TotalChunks: 9,
		}},
		Progress: TurnDetailProgress{Pages: 46, Items: 227, Bytes: 937241, EOF: false},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"turnId", "manifestRev", "seq", "items", "oversize", "progress"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("chunk payload missing wire key %q in %s", key, raw)
		}
	}
	prog := decoded["progress"].(map[string]any)
	for _, key := range []string{"pages", "items", "bytes", "eof"} {
		if _, ok := prog[key]; !ok {
			t.Errorf("progress missing wire key %q", key)
		}
	}
	over := decoded["oversize"].([]any)[0].(map[string]any)
	for _, key := range []string{"itemId", "handle", "type", "totalBytes", "preview", "totalChunks"} {
		if _, ok := over[key]; !ok {
			t.Errorf("oversize ref missing wire key %q", key)
		}
	}
}

func TestTurnOutputChunkAckWireShape(t *testing.T) {
	raw, err := json.Marshal(TurnOutputChunkAck{ChunkIndex: 0, TotalChunks: 9, TotalBytes: 1057417, Encoding: "utf-8", Data: "…"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"chunkIndex", "totalChunks", "totalBytes", "encoding", "data"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("turn_output_chunk ack missing wire key %q in %s", key, raw)
		}
	}
}

func TestTurnDetailChunkEventEnvelope(t *testing.T) {
	msg := turnDetailChunkEvent("codex-remote", "sess-1", TurnDetailChunkPayload{TurnID: "t", ManifestRev: 1, Seq: 1, Progress: TurnDetailProgress{Items: 1}}, 1700000000000)
	if msg.Type != "event" || msg.Event != "turn_detail_chunk" {
		t.Fatalf("envelope = %q/%q", msg.Type, msg.Event)
	}
	if msg.BackendID != "codex-remote" || msg.SessionID != "sess-1" {
		t.Fatalf("routing = %q/%q", msg.BackendID, msg.SessionID)
	}
}

func TestChunkTotalCountMath(t *testing.T) {
	target := core.TurnDetailChunkTargetBytes
	cases := []struct {
		total int64
		want  int
	}{
		{0, 0},
		{1, 1},
		{target, 1},
		{target + 1, 2},
		{9 * target, 9},
		{9*target - 1, 9},
		{1057417, 9}, // 1.06MB evidence item at 128KB target
	}
	for _, tc := range cases {
		if got := ChunkTotalCountFor(tc.total); got != tc.want {
			t.Errorf("ChunkTotalCountFor(%d) = %d, want %d", tc.total, got, tc.want)
		}
	}
}

func TestTurnDetailChunksDescriptorRidesOwnGate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "agent", "codex-remote", "wire_descriptor.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "core.TurnDetailChunksProductionEnabled") {
		t.Fatal("wire_descriptor.go must gate turn_detail_chunks_v1 on core.TurnDetailChunksProductionEnabled (own shared gate, mirroring the v1 discipline)")
	}
	if strings.Contains(src, `[]string{"turn_detail_lazy_v1", "turn_detail_chunks_v1"}`) {
		t.Fatal("turn_detail_chunks_v1 must not ride an ungated literal")
	}
	// main.go must wire the const exactly once (echo surface), same discipline
	// as the v1 flip gate test.
	mainSrc, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(mainSrc), "SetTurnDetailChunksEnabled(core.TurnDetailChunksProductionEnabled)"); got != 1 {
		t.Fatalf("main.go must contain exactly one SetTurnDetailChunksEnabled(core.TurnDetailChunksProductionEnabled) (found %d)", got)
	}
	// Direct + relay hello paths must both route through the negotiation (the
	// relay is the iPhone production path).
	serverSrc, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(serverSrc), "negotiateTurnDetailChunksV1"); got < 2 {
		t.Fatalf("server.go must define and call negotiateTurnDetailChunksV1 (found %d)", got)
	}
	if !strings.Contains(string(mainSrc), "negotiateTurnDetailChunksV1") {
		t.Fatal("relay hello path (main.go) must mirror the turn_detail_chunks_v1 negotiation")
	}
}

func TestHelloNegotiatesTurnDetailChunksV1(t *testing.T) {
	// Mirror of the v1 negotiation discipline: session_sync_v2 prerequisite,
	// echo only when enabled, per-conn registry set, disabled server stays ok.
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	server := NewServer(h)
	conn := &olderWalkConn{}
	server.SetTurnDetailChunksEnabled(true)

	hello := HelloMessage{Capabilities: []string{"turn_detail_chunks_v1", "session_sync_v2"}}
	ack := &HelloAckMessage{Ok: true, Capabilities: map[string]bool{}}
	if !server.negotiateTurnDetailChunksV1(ack, &hello, conn) {
		t.Fatal("negotiation must succeed with session_sync_v2 present")
	}
	if !ack.Capabilities["turn_detail_chunks_v1"] {
		t.Fatal("enabled server must echo turn_detail_chunks_v1")
	}
	if !server.eventPublisher.ConnTurnDetailChunksV1(conn) {
		t.Fatal("per-conn chunks registry must be set")
	}

	// Without session_sync_v2 the hello fails closed.
	ack2 := &HelloAckMessage{Ok: true, Capabilities: map[string]bool{}}
	if server.negotiateTurnDetailChunksV1(ack2, &HelloMessage{Capabilities: []string{"turn_detail_chunks_v1"}}, conn) {
		t.Fatal("declaration without session_sync_v2 must fail hello")
	}

	// Disabled server never echoes but keeps hello ok, and clears no registry
	// state it did not own (the earlier mark stays until UnregisterConnection).
	server.SetTurnDetailChunksEnabled(false)
	ack3 := &HelloAckMessage{Ok: true, Capabilities: map[string]bool{}}
	if !server.negotiateTurnDetailChunksV1(ack3, &hello, conn) {
		t.Fatal("disabled server must not fail the hello")
	}
	if ack3.Capabilities["turn_detail_chunks_v1"] {
		t.Fatal("disabled server must not echo turn_detail_chunks_v1")
	}
	// Unregister clears the mark (replacement connections re-negotiate).
	server.eventPublisher.UnregisterConnection(conn)
	if server.eventPublisher.ConnTurnDetailChunksV1(conn) {
		t.Fatal("UnregisterConnection must clear the chunks registry mark")
	}
}
