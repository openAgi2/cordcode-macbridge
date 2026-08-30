package core

import "time"

// TurnDetailChunksProductionEnabled is THE single production gate for
// turn_detail_chunks_v1 (unified-bridge-protocol.md §11.8, owner final ruling
// 2026-08-30: docs/2026-08-30-codex-remote-turn-items-closed-evidence.md
// approved). It governs BOTH advertisement surfaces exactly like the v1 gate
// (TurnDetailLazyProductionEnabled):
//
//   - hello_ack echo: go-bridge main.go wires it into
//     Server.SetTurnDetailChunksEnabled (direct + relay negotiation);
//   - backend descriptor: agent/codex-remote WireDescriptor gates its
//     StaticCapabilities entry on this const.
//
// The v2 path ships client-first (iOS overlay lands before the flip), so the
// const stays false until the phase5 units are installed on both ends — the
// same ordering discipline as the Phase 3 v1 flip. v1
// (turn_detail_lazy_v1) remains advertised (deprecated) during the transition.
const TurnDetailChunksProductionEnabled = false

// Owner-frozen v2 resource parameters (§11.8 冻结参数表, 2026-08-30 终审).
// These are TRANSIENT/structural gates only — the v1 permanent per-turn
// caps (24 pages / 512KB whole-turn) are ABOLISHED by the same ruling.
const (
	// TurnDetailBlobThresholdBytes: items serialized above this are extracted
	// into the Mac blob store; the manifest/chunk carries only a preview +
	// handle (§11.8 超大 command output).
	TurnDetailBlobThresholdBytes int64 = 256 * 1024
	// TurnDetailChunkTargetBytes: per-chunk delivery target.
	TurnDetailChunkTargetBytes int64 = 128 * 1024
	// TurnDetailChunkAdvisoryCapBytes: advisory per-chunk/patch cap — split.
	TurnDetailChunkAdvisoryCapBytes int64 = 256 * 1024
	// TurnDetailPatchHardCapBytes: absolute post-encode patch cap. Never emit
	// a turn_detail_chunk whose encoded size exceeds this.
	TurnDetailPatchHardCapBytes int64 = 512 * 1024
	// TurnDetailPageRawBackstopBytes: single upstream page raw-response
	// anomaly backstop (fail page_oversize). ONLY a single-response guard —
	// it must never be re-purposed as a whole-turn cap (owner ruling).
	TurnDetailPageRawBackstopBytes int64 = 4 * 1024 * 1024
	// TurnDetailPageRPCTimeout: per-page RPC timeout.
	TurnDetailPageRPCTimeout = 30 * time.Second
	// TurnDetailBatchDeadline: per-load-attempt (batch) deadline. Reaching it
	// saves progress and resumes — it is NOT a failure state.
	TurnDetailBatchDeadline = 90 * time.Second
	// TurnDetailBlobPreviewBytes: preview length carried beside a blob handle.
	TurnDetailBlobPreviewBytes int = 2 * 1024
	// TurnDetailCacheBudgetBytes: DEFAULT budget for the ENTIRE detail cache —
	// manifests + item logs + blobs + temp transaction files, not blobs alone
	// (F1.1 P1-6: a blobs-only LRU leaves item logs unbounded). Eviction
	// granularity is a WHOLE per-turn detail cache directory, oldest
	// last-access first. INITIAL DEFAULT, not an evidence-frozen value:
	// adjust on real cache-usage data. Evicted turns re-hydrate from official
	// pagination on demand.
	TurnDetailCacheBudgetBytes int64 = 128 * 1024 * 1024
)
