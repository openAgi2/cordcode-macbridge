package gobridge

// turn_output_chunk_handler.go — F5: the §11.8 secondary lazy-load RPC for
// oversize outputs. The request binds the FULL blob identity the client
// clicked (generation + manifestRev + itemId + handle + chunkIndex); the ack
// echoes the complete binding so the client can prove the data belongs to
// the blob it holds. Chunks are served at the offsets PERSISTED at accept
// time (128KB target, rune-aligned, escape-checked — store truth, never
// recomputed). A missing cache (eviction, stale identity, rotated
// generation) answers the retryable `blob_evicted` UnifiedError: the client
// re-calls session_turn_items, which re-hydrates the cache from official
// pagination (owner plan §3F-F5); blob handles are content-hash-immutable,
// so an unchanged item re-materializes under the SAME handle.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (h *Handlers) handleTurnOutputChunk(conn Connection, msg WireMessage, _ core.Agent) {
	sendAck := func(ack *TurnOutputChunkAck) {
		slog.Info("go-bridge: turn_output_chunk ack",
			"requestId", msg.RequestID, "backendId", msg.BackendID,
			"itemId", ack.ItemID, "chunkIndex", ack.ChunkIndex,
			"totalChunks", ack.TotalChunks, "dataBytes", len(ack.Data))
		conn.SendResult(msg.RequestID, ack, nil)
	}
	sendErr := func(code, message string, retryable bool) {
		slog.Info("go-bridge: turn_output_chunk error",
			"requestId", msg.RequestID, "backendId", msg.BackendID,
			"code", code, "message", message)
		r := retryable
		conn.SendResult(msg.RequestID, nil, &WireError{Code: code, Message: message, Retryable: &r})
	}

	// Capability gate: only turn_detail_chunks_v1 connections may call this
	// (F1.1 P0-2 — the same registry session_turn_items v2 dispatches on).
	if !h.eventPublisher.ConnTurnDetailChunksV1(conn) {
		sendErr("protocol.capability_required",
			"turn_output_chunk requires the turn_detail_chunks_v1 capability", false)
		return
	}
	if msg.BackendID != "codex-remote" {
		sendErr("unsupported_capability",
			fmt.Sprintf("turn_output_chunk is codex-remote only, not %s", msg.BackendID), false)
		return
	}

	var params TurnOutputChunkParams
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			sendErr("invalid_params", "turn_output_chunk params decode: "+err.Error(), false)
			return
		}
	}
	if params.SessionID == "" || params.TurnID == "" || params.ItemID == "" ||
		params.Handle == "" || params.ChunkIndex < 0 {
		sendErr("invalid_params",
			"turn_output_chunk requires sessionId, turnId, itemId, handle and chunkIndex >= 0", false)
		return
	}

	// Hydrate gate + Kernel adjudication, same discipline as §11.7.
	if err := h.ensureProjectionHydrated(msg.BackendID, params.SessionID, "", false); err != nil {
		code, retryable := "projection.hydrate_failed", false
		switch {
		case errors.Is(err, errProjectionHydrating):
			code, retryable = "projection.hydrating", true
		case errors.Is(err, errProjectionSessionNotFound):
			code = "session_not_found"
		}
		sendErr(code, err.Error(), retryable)
		return
	}
	proj, ok := h.projectionKernel.Snapshot(msg.BackendID, params.SessionID)
	if !ok {
		sendErr("session_not_found",
			"projection not available for "+params.SessionID, false)
		return
	}
	var target *TurnProjection
	for i := range proj.Turns {
		if proj.Turns[i].TurnID == params.TurnID {
			target = &proj.Turns[i]
			break
		}
	}
	if target == nil {
		sendErr("turn_not_found",
			fmt.Sprintf("turn %s is not in the committed projection of %s", params.TurnID, params.SessionID), false)
		return
	}
	if params.TurnGeneration != target.TurnGeneration {
		// The client holds a pre-rotation identity — its refs are void.
		sendErr("invalid_params",
			fmt.Sprintf("turn %s generation %d != kernel %d (stale identity — re-open the turn)",
				params.TurnID, params.TurnGeneration, target.TurnGeneration), false)
		return
	}

	store := h.detailStore()
	if store == nil {
		sendErr("blob_evicted", "turn detail store unavailable (no data dir)", true)
		return
	}

	// Full binding against the STORE manifest (the layer that owns blob
	// identity): every mismatch converges through the same retry — the
	// client re-pulls via session_turn_items, which rebuilds the cache.
	manifest, mErr := store.LoadManifest(msg.BackendID, params.SessionID, params.TurnID)
	if mErr != nil {
		sendErr("blob_evicted", "turn detail cache is not available: "+mErr.Error(), true)
		return
	}
	if manifest.Generation != target.TurnGeneration {
		sendErr("blob_evicted",
			fmt.Sprintf("detail cache generation %d != kernel %d (evicted/stale — re-hydrate via session_turn_items)",
				manifest.Generation, target.TurnGeneration), true)
		return
	}
	if params.ManifestRev != manifest.ManifestRev {
		sendErr("blob_evicted",
			fmt.Sprintf("manifestRev %d != cache %d (rebuilt identity — re-pull refs via session_turn_items)",
				params.ManifestRev, manifest.ManifestRev), true)
		return
	}
	var offsets []int
	for _, row := range manifest.Items {
		if row.BlobHandle == params.Handle {
			if row.ItemID != params.ItemID {
				break
			}
			offsets = row.BlobOffsets
			break
		}
	}
	if offsets == nil {
		sendErr("blob_evicted",
			fmt.Sprintf("handle %s is not referenced by the cache for item %s", params.Handle, params.ItemID), true)
		return
	}
	if params.ChunkIndex >= len(offsets)-1 {
		sendErr("invalid_params",
			fmt.Sprintf("chunkIndex %d outside the persisted table (0..%d)",
				params.ChunkIndex, len(offsets)-2), false)
		return
	}

	data, totalChunks, total, err := store.ReadBlobChunk(
		msg.BackendID, params.SessionID, params.TurnID, params.Handle, params.ChunkIndex)
	if err != nil {
		if errors.Is(err, ErrDetailBlobMissing) {
			// A complete manifest that references a missing blob is not a usable
			// loaded cache. Invalidate the whole turn so the client's documented
			// session_turn_items retry enters the real re-hydration path instead
			// of short-circuiting forever on manifest.Resume.EOF.
			if dropErr := store.DropTurn(msg.BackendID, params.SessionID, params.TurnID); dropErr != nil {
				slog.Warn("go-bridge: failed to invalidate detail cache after missing blob",
					"sessionId", params.SessionID, "turnId", params.TurnID, "err", dropErr)
			}
			sendErr("blob_evicted", "blob file is gone (evicted): "+err.Error(), true)
			return
		}
		if errors.Is(err, ErrDetailBlobUnref) {
			sendErr("blob_evicted", "blob file is gone (evicted): "+err.Error(), true)
			return
		}
		sendErr("upstream_error", "blob read failed: "+err.Error(), false)
		return
	}
	sendAck(&TurnOutputChunkAck{
		TurnGeneration: params.TurnGeneration,
		ManifestRev:    params.ManifestRev,
		ItemID:         params.ItemID,
		Handle:         params.Handle,
		ChunkIndex:     params.ChunkIndex,
		TotalChunks:    totalChunks,
		TotalBytes:     total,
		Encoding:       "utf-8",
		Data:           data,
	})
}
