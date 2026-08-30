package gobridge

// §11.8 (turn_detail_chunks_v1) contract-freeze spec tests, F1.1 closure
// (owner review 2026-08-30 night X): dedicated non-replayable overlay frame,
// full chunk/blob identity binding, ack chunk-range completeness, kernel-state
// manifest monotonicity, and encoding-aware chunk boundaries. These pin the
// wire decisions BEFORE the phase5 handlers exist.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/openAgi2/cordcode-macbridge/core"
)

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

// F1.1 P1-4: kernel-state rules — manifest monotonicity within a generation,
// loaded terminal, failed/retry must carry the retained manifest forward.
func TestTurnStateOpsV2KernelMonotonicity(t *testing.T) {
	proj := func() *SessionProjection {
		return &SessionProjection{SessionID: "s", Turns: []TurnProjection{
			{TurnID: "t1", TurnGeneration: 2, DetailLoadState: DetailStatePartial,
				DetailManifestRev: 5, DetailItemCount: 40, DetailTotalBytes: 12345},
			{TurnID: "t2", TurnGeneration: 0, DetailLoadState: DetailStateLoaded,
				DetailManifestRev: 9, DetailItemCount: 90, DetailTotalBytes: 999999},
		}}
	}

	// Advance: partial → partial with grown manifest is fine.
	if err := ApplyTurnStateOpsV2(proj(), []TurnStateOp{
		{TurnID: "t1", DetailLoadState: DetailStatePartial, TurnGeneration: 2, ManifestRev: 6, ItemCount: 44, TotalBytes: 13000},
	}); err != nil {
		t.Fatalf("manifest advance rejected: %v", err)
	}
	// Retry loading must RETAIN the current manifest (failed → loading with
	// the same-or-advanced summary).
	p := proj()
	p.Turns[0].DetailLoadState = DetailStateFailed
	p.Turns[0].DetailReasonCode = "upstream_error"
	if err := ApplyTurnStateOpsV2(p, []TurnStateOp{
		{TurnID: "t1", DetailLoadState: DetailStateLoading, TurnGeneration: 2, ManifestRev: 5, ItemCount: 40, TotalBytes: 12345},
	}); err != nil {
		t.Fatalf("retry retaining manifest rejected: %v", err)
	}

	regressions := []struct {
		name string
		ops  []TurnStateOp
	}{
		{"manifestRev decrease", []TurnStateOp{{TurnID: "t1", DetailLoadState: DetailStatePartial, TurnGeneration: 2, ManifestRev: 4, ItemCount: 44, TotalBytes: 13000}}},
		{"itemCount decrease", []TurnStateOp{{TurnID: "t1", DetailLoadState: DetailStatePartial, TurnGeneration: 2, ManifestRev: 6, ItemCount: 39, TotalBytes: 13000}}},
		{"totalBytes decrease", []TurnStateOp{{TurnID: "t1", DetailLoadState: DetailStatePartial, TurnGeneration: 2, ManifestRev: 6, ItemCount: 44, TotalBytes: 1}}},
		{"failed zeroing retained progress", []TurnStateOp{{TurnID: "t1", DetailLoadState: DetailStateFailed, ReasonCode: "timeout", TurnGeneration: 2}}},
		{"loaded -> partial", []TurnStateOp{{TurnID: "t2", DetailLoadState: DetailStatePartial, TurnGeneration: 0, ManifestRev: 9, ItemCount: 90, TotalBytes: 999999}}},
		{"loaded -> loading", []TurnStateOp{{TurnID: "t2", DetailLoadState: DetailStateLoading, TurnGeneration: 0, ManifestRev: 9, ItemCount: 90, TotalBytes: 999999}}},
		{"loaded -> failed", []TurnStateOp{{TurnID: "t2", DetailLoadState: DetailStateFailed, ReasonCode: "upstream_error", TurnGeneration: 0, ManifestRev: 10, ItemCount: 90, TotalBytes: 999999}}},
		{"loaded -> loaded advanced rev", []TurnStateOp{{TurnID: "t2", DetailLoadState: DetailStateLoaded, TurnGeneration: 0, ManifestRev: 10, ItemCount: 90, TotalBytes: 999999}}},
	}
	for _, tc := range regressions {
		if err := ApplyTurnStateOpsV2(proj(), tc.ops); err == nil {
			t.Errorf("%s: expected kernel-state rejection", tc.name)
		}
	}

	// Idempotent loaded repeat (same rev) is the ONLY accepted op on a loaded turn.
	if err := ApplyTurnStateOpsV2(proj(), []TurnStateOp{
		{TurnID: "t2", DetailLoadState: DetailStateLoaded, TurnGeneration: 0, ManifestRev: 9, ItemCount: 90, TotalBytes: 999999},
	}); err != nil {
		t.Fatalf("idempotent loaded repeat rejected: %v", err)
	}

	// Generation fence still applies and a failed apply never mutates.
	p2 := proj()
	stale := []TurnStateOp{{TurnID: "t1", DetailLoadState: DetailStateLoaded, TurnGeneration: 1, ManifestRev: 9, ItemCount: 90, TotalBytes: 999999}}
	if err := ApplyTurnStateOpsV2(p2, stale); !errors.Is(err, ErrTurnStateStale) {
		t.Fatalf("err = %v, want ErrTurnStateStale", err)
	}
	if p2.Turns[0].DetailManifestRev != 5 || p2.Turns[0].DetailLoadState != DetailStatePartial {
		t.Fatal("failed apply must not mutate the projection")
	}
}

// F1.1 P0-1: the overlay frame is a DEDICATED envelope — full identity on the
// wire, no EventMessage sequence fields.
func TestTurnDetailChunkFrameWireShape(t *testing.T) {
	frame := TurnDetailChunkFrame{
		Type: "turn_detail_chunk", BackendID: "codex-remote", SessionID: "sess-1",
		TurnID: "turn-42", TurnGeneration: 3, DeliveryID: "d-17", ManifestRev: 7, ChunkSeq: 12,
		Items: []ProjectionPart{{Type: "text", Text: "step"}},
		Oversize: []TurnDetailOversizeRef{{
			ItemID: "item-9", Handle: "blob-abc", Type: "commandExecution",
			TotalBytes: 1057417, Preview: "head…", TotalChunks: 9,
		}},
		Progress: TurnDetailProgress{Pages: 46, Items: 227, Bytes: 937241, EOF: false},
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"type", "backendId", "sessionId", "turnId", "turnGeneration", "deliveryId", "manifestRev", "chunkSeq", "items", "oversize", "progress"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("overlay frame missing wire key %q in %s", key, raw)
		}
	}
	// Dedicated non-replayable envelope: none of the business EventMessage
	// identity fields may appear — they would imply buffer/replay semantics
	// the overlay deliberately does not participate in.
	for _, banned := range []string{"eventId", "seq", "bridgeEpoch", "perSessionSeq", "event", "replayable", "timestamp"} {
		if _, ok := decoded[banned]; ok {
			t.Errorf("overlay frame must NOT carry business-event field %q", banned)
		}
	}
	if decoded["type"] != "turn_detail_chunk" {
		t.Fatalf("frame type = %v", decoded["type"])
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

// F1.1 P0-3: the batch ack carries the delivered chunk-sequence range.
func TestTurnDetailBatchAckWireShape(t *testing.T) {
	raw, err := json.Marshal(TurnDetailBatchAck{
		DetailLoadState: DetailStatePartial, SyncRev: 130, ManifestRev: 7,
		DeliveryID: "d-17", FirstChunkSeq: 20, LastChunkSeq: 27,
		Progress: TurnDetailProgress{Pages: 46, Items: 227, Bytes: 937241, EOF: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"detailLoadState", "syncRev", "manifestRev", "deliveryId", "firstChunkSeq", "lastChunkSeq", "progress"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("batch ack missing wire key %q in %s", key, raw)
		}
	}
	// A failed ack also carries reasonCode.
	rawFailed, _ := json.Marshal(TurnDetailBatchAck{
		DetailLoadState: DetailStateFailed, SyncRev: 131, ReasonCode: "upstream_error",
		ManifestRev: 7, DeliveryID: "d-17", FirstChunkSeq: 0, LastChunkSeq: 0,
		Progress: TurnDetailProgress{Pages: 19, Items: 95, Bytes: 412906},
	})
	if !strings.Contains(string(rawFailed), `"reasonCode":"upstream_error"`) {
		t.Errorf("failed ack must carry reasonCode: %s", rawFailed)
	}
}

// F1.1 P0-2: turn_output_chunk request AND ack bind the full blob identity.
func TestTurnOutputChunkBindingShapes(t *testing.T) {
	rawReq, err := json.Marshal(TurnOutputChunkParams{
		SessionID: "sess-1", TurnID: "turn-42", TurnGeneration: 3, ManifestRev: 7,
		ItemID: "item-9", Handle: "blob-abc", ChunkIndex: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(rawReq, &req); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sessionId", "turnId", "turnGeneration", "manifestRev", "itemId", "handle", "chunkIndex"} {
		if _, ok := req[key]; !ok {
			t.Errorf("turn_output_chunk request missing binding key %q in %s", key, rawReq)
		}
	}

	rawAck, err := json.Marshal(TurnOutputChunkAck{
		TurnGeneration: 3, ManifestRev: 7, ItemID: "item-9", Handle: "blob-abc",
		ChunkIndex: 0, TotalChunks: 9, TotalBytes: 1057417, Encoding: "utf-8", Data: "…",
	})
	if err != nil {
		t.Fatal(err)
	}
	var ack map[string]any
	if err := json.Unmarshal(rawAck, &ack); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"turnGeneration", "manifestRev", "itemId", "handle", "chunkIndex", "totalChunks", "totalBytes", "encoding", "data"} {
		if _, ok := ack[key]; !ok {
			t.Errorf("turn_output_chunk ack missing binding key %q in %s", key, rawAck)
		}
	}
}

// F1.1 P1-7: encoding-aware chunk boundaries — no mid-rune cuts, escaped-size
// respected, count from the actual offset table.
func TestDetailChunkOffsetsRuneSafety(t *testing.T) {
	target := int(core.TurnDetailChunkTargetBytes)
	advisory := int(core.TurnDetailChunkAdvisoryCapBytes)

	// ASCII at evidence size: 1,057,417 B / 128 KB target → 9 chunks.
	ascii := strings.Repeat("a", 1057417)
	offsets := DetailChunkOffsets(ascii)
	if count := len(offsets) - 1; count != 9 {
		t.Fatalf("ASCII evidence-size chunk count = %d, want 9", count)
	}

	// Pure 3-byte CJK runes: every boundary must sit on a multiple of 3
	// (rune start) relative to the string start.
	cjk := strings.Repeat("好", target) // target runes = 3× target bytes
	co := DetailChunkOffsets(cjk)
	for i, off := range co {
		if off%3 != 0 {
			t.Fatalf("CJK offset[%d]=%d splits mid-rune", i, off)
		}
	}
	// A greedy target cut at target bytes would land mid-rune; the aligned
	// cut is target-1 or target-2 — chunk still ≈ target, count unchanged.
	if count := len(co) - 1; count != 3 && count != 4 {
		t.Fatalf("CJK chunk count = %d, want 3-4", count)
	}

	// 4-byte emoji: boundaries on multiples of 4.
	emoji := strings.Repeat("\U0001F600", target) // target bytes of 4-byte runes
	eo := DetailChunkOffsets(emoji)
	for i, off := range eo {
		if off%4 != 0 {
			t.Fatalf("emoji offset[%d]=%d splits mid-rune", i, off)
		}
	}

	// Escaping-heavy: backslashes double on the wire. With 200 KB of raw
	// backslashes (escaped 400 KB > advisory 256 KB) each chunk must be split
	// until its ESCAPED form fits the advisory cap.
	heavy := strings.Repeat(`\`, 200*1024)
	ho := DetailChunkOffsets(heavy)
	if len(ho)-1 < 2 {
		t.Fatalf("escaping-heavy string must split beyond the raw-target cut (got %d chunks)", len(ho)-1)
	}
	for i := 1; i < len(ho); i++ {
		chunk := heavy[ho[i-1]:ho[i]]
		if esc := jsonEscapedLen(chunk); esc > advisory {
			t.Fatalf("chunk[%d] escaped size %d exceeds advisory %d", i-1, esc, advisory)
		}
	}

	// Control characters (\n, \t, quotes) escape to \uXXXX / \n forms; every
	// chunk's escaped size must fit.
	mixed := strings.Repeat("\n\t\"\\", 40*1024)
	mo := DetailChunkOffsets(mixed)
	for i := 1; i < len(mo); i++ {
		if esc := jsonEscapedLen(mixed[mo[i-1]:mo[i]]); esc > advisory {
			t.Fatalf("mixed-control chunk[%d] escaped size %d exceeds advisory", i-1, esc)
		}
	}

	// Union + monotonicity: chunks reconstruct the original, offsets strictly
	// increasing, valid rune boundaries everywhere.
	for _, tc := range []string{ascii, cjk, emoji, heavy, mixed, "", "单", "a\U0001F600b好"} {
		offs := DetailChunkOffsets(tc)
		if len(offs) == 0 || offs[0] != 0 || offs[len(offs)-1] != len(tc) {
			t.Fatalf("offsets must start at 0 and end at len(s): %v (len %d)", offs, len(tc))
		}
		for i := 1; i < len(offs); i++ {
			if offs[i] <= offs[i-1] {
				t.Fatalf("offsets must be strictly increasing: %v", offs)
			}
			if offs[i] < len(tc) && !utf8.RuneStart(tc[offs[i]]) {
				// offs[i] is a chunk START — must be a rune start.
				t.Fatalf("offset[%d]=%d starts mid-rune", i, offs[i])
			}
		}
		var rebuilt strings.Builder
		for i := 1; i < len(offs); i++ {
			rebuilt.WriteString(tc[offs[i-1]:offs[i]])
		}
		if rebuilt.String() != tc {
			t.Fatal("chunk union must reconstruct the original")
		}
	}
	if DetailChunkCount("") != 0 {
		t.Fatal("empty string must be zero chunks")
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

// PublishTurnDetailChunk sends the dedicated frame (never an EventMessage)
// and only to connections holding the v2 mark.
func TestPublishTurnDetailChunkSendsDedicatedFrame(t *testing.T) {
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	conn := &olderWalkConn{}
	h.eventPublisher.RegisterConnection(conn)

	frame := TurnDetailChunkFrame{Type: "turn_detail_chunk", BackendID: "codex-remote",
		SessionID: "s", TurnID: "t", TurnGeneration: 0, DeliveryID: "d-1",
		ManifestRev: 1, ChunkSeq: 1, Progress: TurnDetailProgress{Items: 1}}
	if err := h.eventPublisher.PublishTurnDetailChunk(conn, frame); err == nil {
		t.Fatal("unmarked connection must be refused")
	}
	h.eventPublisher.SetConnTurnDetailChunksV1(conn, true)
	if err := h.eventPublisher.PublishTurnDetailChunk(conn, frame); err != nil {
		t.Fatalf("marked connection publish: %v", err)
	}
	badType := frame
	badType.Type = "event"
	if err := h.eventPublisher.PublishTurnDetailChunk(conn, badType); err == nil {
		t.Fatal("non-overlay frame type must be refused")
	}
}
