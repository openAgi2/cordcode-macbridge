package gobridge

import (
	"fmt"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// turn_detail_chunks_v1 (§11.8, owner final ruling 2026-08-30) — v2 of the
// lazy-detail contract. v1 types/validation in projection_turn_state.go stay
// FROZEN for the deprecated turn_detail_lazy_v1 path; everything below is the
// v2 layering: kernel keeps a manifest SUMMARY only, full detail lives in the
// Mac detail store, and the requesting connection receives bounded
// turn_detail_chunk events (never kernel patches carrying detail content).

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

// TurnDetailChunkPayload is the Data payload of the turn_detail_chunk event.
// Encoded size discipline: advisory cap core.TurnDetailChunkAdvisoryCapBytes,
// absolute hard cap core.TurnDetailPatchHardCapBytes (post-encode).
type TurnDetailChunkPayload struct {
	TurnID      string                  `json:"turnId"`
	ManifestRev int                     `json:"manifestRev"`
	Seq         int                     `json:"seq"`
	Items       []ProjectionPart        `json:"items,omitempty"`
	Oversize    []TurnDetailOversizeRef `json:"oversize,omitempty"`
	Progress    TurnDetailProgress      `json:"progress"`
}

// TurnOutputChunkAck is the success data envelope of the turn_output_chunk
// RPC (§11.8). Data is UTF-8 text chunked at core.TurnDetailChunkTargetBytes.
type TurnOutputChunkAck struct {
	ChunkIndex  int    `json:"chunkIndex"`
	TotalChunks int    `json:"totalChunks"`
	TotalBytes  int64  `json:"totalBytes"`
	Encoding    string `json:"encoding"`
	Data        string `json:"data"`
}

// ValidateTurnStateOpsV2 enforces the v2 fail-closed invariants: state ∈
// {loading, partial, loaded, failed}; failed ⇒ reasonCode from the v2 closed
// set; loading/partial/loaded ⇒ reasonCode absent; manifest summary fields
// non-negative and mutually consistent (items counted ⇒ a manifest revision
// exists). v2 ops ride the same turnStateOps wire array as v1 (additive
// fields); a v1 connection never receives them (gated delivery).
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
		index        int
		state        string
		reason       string
		manifestRev  int
		itemCount    int
		totalBytes   int64
	}
	mutations := make([]turnMutation, 0, len(ops))
	for i, op := range ops {
		at, ok := index[op.TurnID]
		if !ok {
			return fmt.Errorf("%w: op[%d] unknown turn %q", ErrTurnStateInvalid, i, op.TurnID)
		}
		if projection.Turns[at].TurnGeneration != op.TurnGeneration {
			return fmt.Errorf("%w: op[%d] turn %s op generation %d != kernel %d",
				ErrTurnStateStale, i, op.TurnID, op.TurnGeneration, projection.Turns[at].TurnGeneration)
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

// turnDetailChunkEvent wraps a payload into the pushed EventMessage envelope
// (constructor lives in event_publisher.go — the only blessed business-event
// egress site, enforced by TestBusinessEventConstructionHasNoProductionBypass).

// ChunkTotalBytesFor returns the total chunk count for a blob of size bytes
// (target-size chunks; the tail chunk may be short).
func ChunkTotalCountFor(totalBytes int64) int {
	if totalBytes <= 0 {
		return 0
	}
	target := core.TurnDetailChunkTargetBytes
	n := totalBytes / target
	if totalBytes%target != 0 {
		n++
	}
	return int(n)
}
