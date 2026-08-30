package gobridge

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// turn_detail_chunks_v1 (§11.8, owner final ruling 2026-08-30; F1.1 contract
// closure 2026-08-30 night X) — v2 of the lazy-detail contract. v1
// types/validation in projection_turn_state.go stay FROZEN for the deprecated
// turn_detail_lazy_v1 path; everything below is the v2 layering: kernel keeps
// a manifest SUMMARY only, full detail lives in the Mac detail store, and the
// requesting connection receives bounded turn_detail_chunk frames through a
// DEDICATED non-replayable overlay envelope (never the business
// EventMessage sequence, never the kernel patch chain).

// DetailStatePartial is the v2-only progress state: persisted progress exists
// and the next batch resumes from the saved cursor (§11.7 v1 validator
// rejects it — v2 ops are validated by ValidateTurnStateOpsV2 instead).
const DetailStatePartial = "partial"

// ReasonCodePageOversize: a single upstream page exceeded the 4MB raw
// backstop (core.TurnDetailPageRawBackstopBytes). Anomaly guard only.
const ReasonCodePageOversize = "page_oversize"

// TurnDetailChunksReasonCodes is the frozen v2 closed set. The v1-only codes
// max_pages/max_bytes are REMOVED by the owner final ruling (permanent
// per-turn caps abolished); budget exhaustion is partial+resume, never failed.
var TurnDetailChunksReasonCodes = map[string]bool{
	"upstream_error":        true,
	"timeout":               true,
	"stale_turn":            true,
	"interrupted":           true,
	"unsupported_item_type": true,
	ReasonCodePageOversize:  true,
}

// TurnDetailProgress is the §11.8 progress triple carried on acks and chunks.
type TurnDetailProgress struct {
	Pages int   `json:"pages"`
	Items int   `json:"items"`
	Bytes int64 `json:"bytes"`
	EOF   bool  `json:"eof"`
}

// TurnDetailOversizeRef describes one blob-extracted item (>256KB serialized)
// inside a turn_detail_chunk: the full content is NOT on the wire — clients
// fetch it via turn_output_chunk using the handle.
type TurnDetailOversizeRef struct {
	ItemID      string `json:"itemId"`
	Handle      string `json:"handle"`
	Type        string `json:"type"`
	TotalBytes  int64  `json:"totalBytes"`
	Preview     string `json:"preview"`
	TotalChunks int    `json:"totalChunks"`
}

// TurnDetailChunkFrame is the DEDICATED overlay envelope (F1.1 P0-1/P0-2).
// It is NOT an EventMessage: it carries no eventId, no top-level seq, no
// bridgeEpoch/perSessionSeq, never enters the event buffer, and is never
// replayed or re-emitted by recovery. Overlay delivery is connection-scoped
// and loss-tolerant: iOS completes a batch only on the contiguous
// [firstChunkSeq, lastChunkSeq] range from the batch ack and re-pulls gaps
// from the detail cache — never by trusting delivery order.
//
// Identity (P0-2): every frame binds (sessionId, turnId, turnGeneration,
// deliveryId). Clients accept content ONLY for the identity they currently
// hold; a delayed frame from an older delivery/generation is dropped by that
// comparison. chunkSeq — deliberately NOT "seq", to never alias any transport
// sequence — is the per-(session, turn) monotonic chunk index; it continues
// across batches and deliveries so global gap detection works.
type TurnDetailChunkFrame struct {
	Type           string                  `json:"type"` // always "turn_detail_chunk"
	BackendID      string                  `json:"backendId"`
	SessionID      string                  `json:"sessionId"`
	TurnID         string                  `json:"turnId"`
	TurnGeneration int                     `json:"turnGeneration"`
	DeliveryID     string                  `json:"deliveryId"`
	ManifestRev    int                     `json:"manifestRev"`
	ChunkSeq       int                     `json:"chunkSeq"`
	Items          []ProjectionPart        `json:"items,omitempty"`
	Oversize       []TurnDetailOversizeRef `json:"oversize,omitempty"`
	Progress       TurnDetailProgress      `json:"progress"`
}

// TurnDetailBatchAck is the session_turn_items v2 ack (F1.1 P0-3): the
// terminal state of THIS batch plus the chunk-sequence range it delivered.
// firstChunkSeq/lastChunkSeq are both 0 when the batch delivered no chunks
// (client then relies on manifestRev+progress only). A batch is complete on
// the client ONLY after the contiguous [firstChunkSeq, lastChunkSeq] frames
// arrived; a gap re-pulls from the detail cache (fast-path replay), never
// "pretend success".
type TurnDetailBatchAck struct {
	DetailLoadState string             `json:"detailLoadState"` // loading | partial | loaded | failed
	SyncRev         int                `json:"syncRev"`
	ReasonCode      string             `json:"reasonCode,omitempty"`
	ManifestRev     int                `json:"manifestRev"`
	DeliveryID      string             `json:"deliveryId"`
	FirstChunkSeq   int                `json:"firstChunkSeq"`
	LastChunkSeq    int                `json:"lastChunkSeq"`
	Progress        TurnDetailProgress `json:"progress"`
}

// TurnOutputChunkParams is the turn_output_chunk request (F1.1 P0-2): the
// caller binds the FULL blob identity it clicked — generation, manifestRev,
// itemId AND handle — so a response can be proven to belong to that blob.
type TurnOutputChunkParams struct {
	SessionID      string `json:"sessionId"`
	TurnID         string `json:"turnId"`
	TurnGeneration int    `json:"turnGeneration"`
	ManifestRev    int    `json:"manifestRev"`
	ItemID         string `json:"itemId"`
	Handle         string `json:"handle"`
	ChunkIndex     int    `json:"chunkIndex"`
}

// TurnOutputChunkAck echoes the full binding (F1.1 P0-2): the client rejects
// any response whose echoed identity does not match its request. Data is a
// UTF-8 text chunk cut at the frozen boundary table (DetailChunkOffsets).
type TurnOutputChunkAck struct {
	TurnGeneration int    `json:"turnGeneration"`
	ManifestRev    int    `json:"manifestRev"`
	ItemID         string `json:"itemId"`
	Handle         string `json:"handle"`
	ChunkIndex     int    `json:"chunkIndex"`
	TotalChunks    int    `json:"totalChunks"`
	TotalBytes     int64  `json:"totalBytes"`
	Encoding       string `json:"encoding"` // "utf-8"
	Data           string `json:"data"`
}

// ValidateTurnStateOpsV2 enforces the v2 fail-closed field-level invariants:
// state ∈ {loading, partial, loaded, failed}; failed ⇒ reasonCode from the v2
// closed set; loading/partial/loaded ⇒ reasonCode absent; manifest summary
// fields non-negative and mutually consistent (items counted ⇒ a manifest
// revision exists). Kernel-state monotonicity rules live in
// ApplyTurnStateOpsV2 (they need the current projection).
func ValidateTurnStateOpsV2(ops []TurnStateOp) error {
	for i, op := range ops {
		if op.TurnID == "" {
			return fmt.Errorf("%w: op[%d] empty turnId", ErrTurnStateInvalid, i)
		}
		if op.TurnGeneration < 0 {
			return fmt.Errorf("%w: op[%d] negative generation", ErrTurnStateInvalid, i)
		}
		if op.ManifestRev < 0 || op.ItemCount < 0 || op.TotalBytes < 0 {
			return fmt.Errorf("%w: op[%d] negative manifest summary", ErrTurnStateInvalid, i)
		}
		if op.ItemCount > 0 && op.ManifestRev <= 0 {
			return fmt.Errorf("%w: op[%d] itemCount without manifestRev", ErrTurnStateInvalid, i)
		}
		switch op.DetailLoadState {
		case DetailStateFailed:
			if op.ReasonCode == "" {
				return fmt.Errorf("%w: op[%d] failed without reasonCode", ErrTurnStateInvalid, i)
			}
			if !TurnDetailChunksReasonCodes[op.ReasonCode] {
				return fmt.Errorf("%w: op[%d] reasonCode %q outside v2 set", ErrTurnStateInvalid, i, op.ReasonCode)
			}
		case DetailStateLoading, DetailStatePartial, DetailStateLoaded:
			if op.ReasonCode != "" {
				return fmt.Errorf("%w: op[%d] %s carries reasonCode", ErrTurnStateInvalid, i, op.DetailLoadState)
			}
		default:
			return fmt.Errorf("%w: op[%d] state %q", ErrTurnStateInvalid, i, op.DetailLoadState)
		}
	}
	return nil
}

// ApplyTurnStateOpsV2 mirrors ApplyTurnStateOps kernel semantics (strict
// target-turn existence + per-turn generation fence) and additionally stamps
// the manifest SUMMARY fields (manifestRev/itemCount/totalBytes). Detail
// content NEVER flows through here — that is the §11.8 layering guarantee.
//
// F1.1 P1-4 kernel-state rules (checked against the CURRENT projection, not
// just field-level):
//
//   - manifest monotonicity within a generation: every op must restate the
//     full current-or-advanced manifest (manifestRev/itemCount/totalBytes
//     each ≥ the kernel's current values). A failed/retry op that zeroes the
//     summary while progress exists is rejected — the builder must carry the
//     retained manifest forward;
//   - loaded is TERMINAL within a generation: once loaded, only the exact
//     idempotent repeat (loaded, same manifestRev) is accepted. A generation
//     bump resets the manifest baseline at the bump commit site (new truth =
//     fresh detail state), so these rules are per-generation by construction.
func ApplyTurnStateOpsV2(projection *SessionProjection, ops []TurnStateOp) error {
	if len(ops) == 0 {
		return nil
	}
	if err := ValidateTurnStateOpsV2(ops); err != nil {
		return err
	}
	index := make(map[string]int, len(projection.Turns))
	for i := range projection.Turns {
		index[projection.Turns[i].TurnID] = i
	}
	type turnMutation struct {
		index       int
		state       string
		reason      string
		manifestRev int
		itemCount   int
		totalBytes  int64
	}
	mutations := make([]turnMutation, 0, len(ops))
	for i, op := range ops {
		at, ok := index[op.TurnID]
		if !ok {
			return fmt.Errorf("%w: op[%d] unknown turn %q", ErrTurnStateInvalid, i, op.TurnID)
		}
		cur := projection.Turns[at]
		if cur.TurnGeneration != op.TurnGeneration {
			return fmt.Errorf("%w: op[%d] turn %s op generation %d != kernel %d",
				ErrTurnStateStale, i, op.TurnID, op.TurnGeneration, cur.TurnGeneration)
		}
		if op.ManifestRev < cur.DetailManifestRev || op.ItemCount < cur.DetailItemCount || op.TotalBytes < cur.DetailTotalBytes {
			return fmt.Errorf("%w: op[%d] turn %s manifest regression (op %d/%d/%d < kernel %d/%d/%d)",
				ErrTurnStateInvalid, i, op.TurnID,
				op.ManifestRev, op.ItemCount, op.TotalBytes,
				cur.DetailManifestRev, cur.DetailItemCount, cur.DetailTotalBytes)
		}
		if cur.DetailLoadState == DetailStateLoaded {
			idempotent := op.DetailLoadState == DetailStateLoaded && op.ManifestRev == cur.DetailManifestRev
			if !idempotent {
				return fmt.Errorf("%w: op[%d] turn %s loaded is terminal within a generation (op state %q)",
					ErrTurnStateInvalid, i, op.TurnID, op.DetailLoadState)
			}
		}
		mutations = append(mutations, turnMutation{
			index: at, state: op.DetailLoadState, reason: op.ReasonCode,
			manifestRev: op.ManifestRev, itemCount: op.ItemCount, totalBytes: op.TotalBytes,
		})
	}
	for _, m := range mutations {
		turn := &projection.Turns[m.index]
		turn.DetailLoadState = m.state
		turn.DetailReasonCode = m.reason
		turn.DetailManifestRev = m.manifestRev
		turn.DetailItemCount = m.itemCount
		turn.DetailTotalBytes = m.totalBytes
	}
	return nil
}

// jsonEscapedLen measures the exact JSON-escaped wire length of s (without
// the surrounding quotes) — the budget that actually matters for envelope
// size after escaping.
func jsonEscapedLen(s string) int {
	quoted, err := json.Marshal(s)
	if err != nil {
		// json.Marshal of a string cannot fail; defensive fallback.
		return len(s) * 6
	}
	return len(quoted) - 2
}

// alignBackToRuneStart moves cut backward to a UTF-8 rune boundary (never
// splits mid-rune; F1.1 P1-7).
func alignBackToRuneStart(s string, cut int) int {
	for cut > 0 && cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}

// DetailChunkOffsets computes the frozen chunk boundary table (F1.1 P1-7):
//
//  1. target-size cuts on raw bytes (core.TurnDetailChunkTargetBytes), each
//     aligned back to a rune start — a chunk NEVER begins or ends mid-rune;
//  2. every chunk's JSON-ESCAPED wire length is re-checked against the
//     advisory cap (core.TurnDetailChunkAdvisoryCapBytes) and split further
//     (rune-aligned halves) until it fits — escaping-heavy text (backslashes,
//     quotes, control chars) yields shorter raw chunks, not oversized frames;
//  3. totalChunks = len(offsets)-1 — computed from the ACTUAL offset table,
//     never from ceil(totalBytes/target).
//
// offsets[0] == 0 and offsets[len-1] == len(s); an empty string yields a
// zero-chunk table ([]int{0}). Frame-level assembly (F4) additionally
// enforces the 512KB post-encode hard cap on the WHOLE envelope.
func DetailChunkOffsets(s string) []int {
	target := int(core.TurnDetailChunkTargetBytes)
	advisory := int(core.TurnDetailChunkAdvisoryCapBytes)
	if len(s) == 0 {
		return []int{0}
	}
	offsets := []int{0}
	for start := 0; start < len(s); {
		cut := start + target
		if cut >= len(s) {
			cut = len(s)
		} else {
			cut = alignBackToRuneStart(s, cut)
			if cut <= start { // pathological: rune longer than target — keep whole rune
				cut = start + utf8.RuneLen([]rune(s[start : start+1])[0])
				if cut > len(s) {
					cut = len(s)
				}
			}
		}
		// Escaped-size re-check: split rune-aligned halves until the chunk's
		// escaped wire form fits the advisory cap. A single rune never exceeds
		// the cap (max escaped rune ≈ 12 bytes), so this terminates.
		for cut < len(s) && jsonEscapedLen(s[start:cut]) > advisory {
			half := alignBackToRuneStart(s, start+(cut-start)/2)
			if half <= start || half >= cut {
				half = cut - 1
				half = alignBackToRuneStart(s, half)
				if half <= start {
					break
				}
			}
			cut = half
		}
		offsets = append(offsets, cut)
		start = cut
	}
	return offsets
}

// DetailChunkCount returns the chunk count from the ACTUAL offset table.
func DetailChunkCount(s string) int {
	return len(DetailChunkOffsets(s)) - 1
}
