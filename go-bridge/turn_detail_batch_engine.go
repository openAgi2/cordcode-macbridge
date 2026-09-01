package gobridge

// turn_detail_batch_engine.go — F4: the §11.8 v2 session_turn_items batch
// engine. One request = one 90s BATCH (deadline reached → partial + resume,
// never failed) driving the official items pagination page by page:
//
//	page → classify (inline | oversize slim card + blob) → store.AcceptPage
//	     → kernel CommitTurnStateOpsV2 (manifest summary in lockstep with the
//	       store commit point) → turn_detail_chunk frames to the REQUESTING
//	       connection and any singleflight followers (§11.8 per-connection
//	       overlay; frames never enter the event buffer).
//
// Resume: each accepted page persists nextCursor/boundary in the store; the
// next batch continues from it. Reconnect fast-path: an optional
// replaySinceChunkSeq request param replays the committed chunk range from
// the detail cache (no upstream) before continuing — absent param = plain
// continuation, matching the frozen ack examples (firstChunkSeq = this
// batch's first NEW chunk). Cursor invalidation (empty page with a cursor,
// or — outside a re-walk — a page entirely of already-accepted ids) switches
// to a head re-walk that skips committed content by canonical item id; a
// second anomaly fails upstream_error. The 4MB single-page raw backstop
// fails page_oversize (progress retained). No page/byte caps exist anywhere
// on this path (owner final ruling abolished them).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/codex-remote"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// turnDetailBatchDeadline is the per-batch deadline seam (production value =
// core.TurnDetailBatchDeadline; tests shrink it). Reaching it commits partial
// with the fully-accepted progress — it is NOT a failure state.
var turnDetailBatchDeadline = core.TurnDetailBatchDeadline

// turnDetailChunksFlight is the v2 singleflight entry: followers mirror the
// leader's terminal ack and register their connection so the leader's chunk
// frames fan out to them too (§11.8: the follower observes the same batch's
// event stream).
type turnDetailChunksFlight struct {
	done chan struct{}
	ack  *TurnDetailBatchAck

	mu    sync.Mutex
	conns map[Connection]bool
}

func (f *turnDetailChunksFlight) addConn(c Connection) {
	f.mu.Lock()
	f.conns[c] = true
	f.mu.Unlock()
}

func (f *turnDetailChunksFlight) receivers() []Connection {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Connection, 0, len(f.conns))
	for c := range f.conns {
		out = append(out, c)
	}
	return out
}

// newDeliveryID mints a per-batch delivery id (bound on every frame + ack).
func newDeliveryID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("d-%d", time.Now().UnixNano())
	}
	return "d-" + hex.EncodeToString(b[:])
}

// detailStore lazily constructs the process-wide detail store (rooted at
// <dataDir>/detail) and runs the startup sweep exactly once. Empty dataDir =
// no persistence: the v2 path fails honestly instead of silently dropping
// chunks.
func (h *Handlers) detailStore() *TurnDetailStore {
	h.turnDetailStoreOnce.Do(func() {
		if h.dataDir == "" {
			return
		}
		store := NewTurnDetailStore(filepath.Join(h.dataDir, "detail"))
		if err := store.SweepUncommitted(); err != nil {
			slog.Warn("go-bridge: detail store startup sweep failed", "err", err)
		}
		h.turnDetailStore = store
	})
	return h.turnDetailStore
}

// handleSessionTurnItemsV2 is the v2 dispatch (conn negotiated
// turn_detail_chunks_v1): owns the idempotent-loaded ack, orphan recovery,
// and the singleflight; the leader runs runTurnDetailBatch.
func (h *Handlers) handleSessionTurnItemsV2(
	conn Connection,
	msg WireMessage,
	sessionID, turnID string,
	target *TurnProjection,
	currentSyncRev int,
) {
	sendAck := func(ack *TurnDetailBatchAck) {
		slog.Info("go-bridge: session_turn_items v2 ack",
			"requestId", msg.RequestID, "backendId", msg.BackendID,
			"sessionId", sessionID, "turnId", turnID,
			"detailLoadState", ack.DetailLoadState, "syncRev", ack.SyncRev,
			"manifestRev", ack.ManifestRev, "deliveryId", ack.DeliveryID,
			"chunks", fmt.Sprintf("[%d,%d]", ack.FirstChunkSeq, ack.LastChunkSeq),
			"reasonCode", ack.ReasonCode)
		conn.SendResult(msg.RequestID, ack, nil)
	}
	sendErr := func(code, message string, retryable bool) {
		slog.Info("go-bridge: session_turn_items v2 error",
			"requestId", msg.RequestID, "backendId", msg.BackendID,
			"sessionId", sessionID, "turnId", turnID,
			"code", code, "message", message)
		r := retryable
		conn.SendResult(msg.RequestID, nil, &WireError{Code: code, Message: message, Retryable: &r})
	}

	// Optional reconnect fast-path param: replay committed chunks after this
	// chunkSeq from the detail cache before continuing upstream. Absent =
	// plain continuation (frozen ack examples carry only this batch's range).
	replaySince, hasReplay := -1, false
	if len(msg.Params) > 0 {
		var extra struct {
			ReplaySinceChunkSeq *int `json:"replaySinceChunkSeq"`
		}
		if err := json.Unmarshal(msg.Params, &extra); err == nil && extra.ReplaySinceChunkSeq != nil {
			replaySince, hasReplay = *extra.ReplaySinceChunkSeq, true
			if replaySince < 0 {
				sendErr("invalid_params", "replaySinceChunkSeq must be >= 0", false)
				return
			}
		}
	}

	store := h.detailStore()
	if store == nil {
		sendErr("invalid_params", "turn detail store unavailable (no data dir)", false)
		return
	}

	// Loaded terminal (§11.8): with a COMPLETE same-generation cache this is
	// the idempotent repeat — ack from kernel truth + store progress, no
	// refetch. An UNUSABLE cache (LRU eviction wiped the turn dir, runtime
	// corruption, generation rotation, or a re-hydration interrupted
	// mid-rebuild) takes the re-hydration batch instead (F5): rebuild the
	// detail cache from official pagination WITHOUT kernel state commits
	// (loaded is terminal within a generation) and deliver the rebuilt
	// chunks under a fresh deliveryId with chunkSeq restarting at 1 — the
	// evicted sequence is unrecoverable, so a contiguous [1..last] under
	// one delivery means full overlay replacement for the client.
	rehydrate := false
	if target.DetailLoadState == DetailStateLoaded {
		manifest, mErr := store.LoadManifest(msg.BackendID, sessionID, turnID)
		if mErr == nil && manifest != nil &&
			manifest.MappingVersion == turnDetailMappingVersion &&
			manifest.Generation == target.TurnGeneration && manifest.Resume.EOF {
			sendAck(&TurnDetailBatchAck{
				DetailLoadState: DetailStateLoaded,
				SyncRev:         h.loadedDetailWatermark(msg.BackendID, sessionID, turnID, currentSyncRev),
				TurnGeneration:  target.TurnGeneration,
				ManifestRev:     manifest.ManifestRev,
				DeliveryID:      newDeliveryID(),
				Progress:        h.detailStoreProgress(store, msg.BackendID, sessionID, turnID, target),
			})
			return
		}
		rehydrate = true
	}

	flightKey := projectionDeliveryKey(msg.BackendID, sessionID) + "|" + turnID

	// Lazy orphan recovery (§11.8): loading with no leader (v2 or v1) is a
	// crashed batch's leftover — recover to failed(interrupted) CARRYING the
	// retained manifest so the retry resumes from the store cursor.
	if target.DetailLoadState == DetailStateLoading {
		_, v2InFlight := h.turnDetailChunksFlights.Load(flightKey)
		_, v1InFlight := h.turnDetailFlights.Load(flightKey)
		if !v2InFlight && !v1InFlight {
			h.recoverOrphanLoadingTurnV2(msg.BackendID, sessionID, turnID, target)
		}
	}

	candidate := &turnDetailChunksFlight{done: make(chan struct{}), conns: map[Connection]bool{conn: true}}
	for {
		actual, loaded := h.turnDetailChunksFlights.LoadOrStore(flightKey, candidate)
		flight := actual.(*turnDetailChunksFlight)
		if !loaded {
			ack := h.runTurnDetailBatch(conn, msg.BackendID, sessionID, turnID, replaySince, hasReplay, rehydrate, flight)
			flight.ack = ack
			close(flight.done)
			h.turnDetailChunksFlights.Delete(flightKey)
			sendAck(ack)
			return
		}
		flight.addConn(conn)
		<-flight.done
		sendAck(flight.ack)
		return
	}
}

// detailStoreProgress snapshots the §11.8 progress triple from the store
// manifest; the kernel summary fields are the floor (the kernel never lags
// the store's committed point by more than the in-flight commit).
func (h *Handlers) detailStoreProgress(store *TurnDetailStore, backendID, sessionID, turnID string, target *TurnProjection) TurnDetailProgress {
	progress := TurnDetailProgress{Items: target.DetailItemCount, Bytes: target.DetailTotalBytes}
	if manifest, err := store.LoadManifest(backendID, sessionID, turnID); err == nil && manifest != nil {
		progress = TurnDetailProgress{
			Pages: manifest.Resume.Pages, Items: manifest.ItemCount,
			Bytes: manifest.TotalBytes, EOF: manifest.Resume.EOF,
		}
	}
	return progress
}

// recoverOrphanLoadingTurnV2 is the in-place §11.8 orphan recovery (the
// checkpoint-restore complement is RecoverOrphanDetailLoadingV2 in F3).
func (h *Handlers) recoverOrphanLoadingTurnV2(backendID, sessionID, turnID string, target *TurnProjection) {
	_, patches, err := h.projectionKernel.CommitTurnStateOpsV2(backendID, sessionID, []TurnStateOp{{
		TurnID:          turnID,
		DetailLoadState: DetailStateFailed,
		ReasonCode:      "interrupted",
		TurnGeneration:  target.TurnGeneration,
		ManifestRev:     target.DetailManifestRev,
		ItemCount:       target.DetailItemCount,
		TotalBytes:      target.DetailTotalBytes,
	}})
	if err != nil {
		return
	}
	h.eventPublisher.PublishProjectionDetail(backendID, sessionID, patches, nil)
}

// mergeTurnSummary takes the per-field max of the kernel's manifest summary
// and the store's committed manifest — the eviction re-hydration guard: a
// re-built store legitimately restarts at rev 1 while the kernel keeps the
// high-water summary; V2 ops must never regress either side.
func mergeTurnSummary(turn *TurnProjection, manifest *TurnDetailManifest) (int, int, int64) {
	rev, items, bytes := turn.DetailManifestRev, turn.DetailItemCount, turn.DetailTotalBytes
	if manifest != nil {
		if manifest.ManifestRev > rev {
			rev = manifest.ManifestRev
		}
		if manifest.ItemCount > items {
			items = manifest.ItemCount
		}
		if manifest.TotalBytes > bytes {
			bytes = manifest.TotalBytes
		}
	}
	return rev, items, bytes
}

// classifyDetailParts maps one page's ProjectionParts (official order,
// canonical itemIds) to ordered store entries. A part beyond the blob
// threshold becomes an oversize entry: SLIM card (metadata intact, giant
// content stripped) + the full content as blob payload. Exactly one of
// Inline/Oversize is set, per the F2.1 store contract.
func classifyDetailParts(parts []ProjectionPart) ([]DetailPageEntry, error) {
	threshold := core.TurnDetailBlobThresholdBytes
	entries := make([]DetailPageEntry, 0, len(parts))
	for i := range parts {
		part := parts[i]
		if part.ItemID == "" {
			return nil, fmt.Errorf("detail page part without canonical itemId (type %q)", part.Type)
		}
		entry := DetailPageEntry{ItemID: part.ItemID}
		if partSize(part) <= threshold {
			entry.Inline = &part
			entries = append(entries, entry)
			continue
		}
		content := detailPartOutput(part)
		slim := part
		if slim.Type == "tool" {
			// Slim tool card (F2.1 P0-2): command/cwd/status/title stay, the
			// giant output becomes a lazy blob load via the ref.
			slim.ToolResult = nil
		} else {
			slim.Text = ""
		}
		entry.Oversize = &DetailOversizeStaged{
			Part:    slim,
			Preview: truncateRuneAligned(content, core.TurnDetailBlobPreviewBytes),
			Content: content,
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// detailPartOutput extracts the oversize blob content: the tool result for
// tool parts (stringified when structured), the text itself otherwise.
func detailPartOutput(part ProjectionPart) string {
	if part.Type == "tool" && part.ToolResult != nil {
		if s, ok := part.ToolResult.(string); ok {
			return s
		}
		if raw, err := json.Marshal(part.ToolResult); err == nil {
			return string(raw)
		}
		return fmt.Sprintf("%v", part.ToolResult)
	}
	return part.Text
}

// runTurnDetailBatch is the singleflight leader. Terminal outcomes:
// loaded(EOF) / partial(deadline — retryable continuation) / failed(reason
// from the v2 closed set, progress retained via the carried manifest).
//
// rehydrate (F5) = the §11.8 eviction re-hydration batch: the kernel turn is
// already loaded (terminal — NO state commits happen) while the detail cache
// is missing/incomplete, so the batch only rebuilds the store + delivers
// chunks; acks stay loaded with the fresh store manifest identity, and a
// mid-rebuild failure answers loaded (kernel truth) — the client converges
// via blob_evicted on the next turn_output_chunk.
func (h *Handlers) runTurnDetailBatch(
	requester Connection,
	backendID, sessionID, turnID string,
	replaySince int, hasReplay, rehydrate bool,
	flight *turnDetailChunksFlight,
) *TurnDetailBatchAck {
	deliveryID := newDeliveryID()
	generation := 0
	store := h.detailStore()
	findTurn := func(proj SessionProjection) *TurnProjection {
		for i := range proj.Turns {
			if proj.Turns[i].TurnID == turnID {
				return &proj.Turns[i]
			}
		}
		return nil
	}
	currentTurn := func() *TurnProjection {
		proj, ok := h.projectionKernel.Snapshot(backendID, sessionID)
		if !ok {
			return nil
		}
		return findTurn(proj)
	}

	// failTerminal commits the v2 failed op carrying the RETAINED summary
	// (kernel vs store max — never a zeroing) and builds the failed ack.
	failTerminal := func(reasonCode string, deliveredFirst, deliveredLast int) *TurnDetailBatchAck {
		turn := currentTurn()
		if turn == nil {
			return &TurnDetailBatchAck{DetailLoadState: DetailStateFailed, SyncRev: 0,
				ReasonCode: "stale_turn", DeliveryID: deliveryID,
				FirstChunkSeq: deliveredFirst, LastChunkSeq: deliveredLast}
		}
		generation = turn.TurnGeneration
		manifest, _ := store.LoadManifest(backendID, sessionID, turnID)
		rev, items, bytes := mergeTurnSummary(turn, manifest)
		if rehydrate {
			// Kernel truth stays loaded (terminal); the rebuild failure is
			// diagnostics-only — progress reflects whatever rebuilt, and the
			// client converges through blob_evicted on its next pull.
			slog.Warn("go-bridge: detail cache re-hydration failed",
				"sessionId", sessionID, "turnId", turnID, "reason", reasonCode,
				"pages", func() int {
					if manifest != nil {
						return manifest.Resume.Pages
					}
					return 0
				}())
			return &TurnDetailBatchAck{DetailLoadState: DetailStateLoaded,
				SyncRev:        h.loadedDetailWatermark(backendID, sessionID, turnID, kernelSyncRev(h, backendID, sessionID)),
				TurnGeneration: generation,
				ManifestRev:    rev, DeliveryID: deliveryID,
				FirstChunkSeq: deliveredFirst, LastChunkSeq: deliveredLast,
				Progress: progressOf(manifest, items, bytes)}
		}
		if turn.DetailLoadState == DetailStateLoaded {
			// A concurrent batch already finished the turn — its truth wins.
			return &TurnDetailBatchAck{DetailLoadState: DetailStateLoaded,
				SyncRev:        h.loadedDetailWatermark(backendID, sessionID, turnID, kernelSyncRev(h, backendID, sessionID)),
				TurnGeneration: generation,
				ManifestRev:    rev, DeliveryID: deliveryID,
				FirstChunkSeq: deliveredFirst, LastChunkSeq: deliveredLast,
				Progress: progressOf(manifest, items, bytes)}
		}
		_, patches, err := h.projectionKernel.CommitTurnStateOpsV2(backendID, sessionID, []TurnStateOp{{
			TurnID: turnID, DetailLoadState: DetailStateFailed, ReasonCode: reasonCode,
			TurnGeneration: turn.TurnGeneration, ManifestRev: rev, ItemCount: items, TotalBytes: bytes,
		}})
		if err != nil {
			return &TurnDetailBatchAck{DetailLoadState: DetailStateFailed,
				SyncRev:        kernelSyncRev(h, backendID, sessionID),
				TurnGeneration: generation,
				ReasonCode:     "stale_turn", ManifestRev: rev, DeliveryID: deliveryID,
				FirstChunkSeq: deliveredFirst, LastChunkSeq: deliveredLast,
				Progress: progressOf(manifest, items, bytes)}
		}
		h.eventPublisher.PublishProjectionDetail(backendID, sessionID, patches, requester)
		mirrorState(store, backendID, sessionID, turnID, turn.TurnGeneration, DetailStateFailed, reasonCode)
		return &TurnDetailBatchAck{DetailLoadState: DetailStateFailed,
			SyncRev:        patches[len(patches)-1].SyncRev,
			TurnGeneration: generation,
			ReasonCode:     reasonCode, ManifestRev: rev, DeliveryID: deliveryID,
			FirstChunkSeq: deliveredFirst, LastChunkSeq: deliveredLast,
			Progress: progressOf(manifest, items, bytes)}
	}

	turn := currentTurn()
	if turn == nil || turn.Status != "completed" {
		return failTerminal("stale_turn", 0, 0)
	}
	generation = turn.TurnGeneration

	agent, haveAgent := h.getFirstAgentByName(backendID)
	if !haveAgent {
		return failTerminal("upstream_error", 0, 0)
	}
	pager, canPage := agent.(core.TurnItemsPager)
	if !canPage {
		return failTerminal("upstream_error", 0, 0)
	}

	// 1. Loading admission (v2 op: loading carries the CURRENT summary).
	// Skipped in rehydrate mode: the kernel turn is loaded-terminal and no
	// state commit is legal — the batch rebuilds the STORE only.
	if !rehydrate {
		_, patches, err := h.projectionKernel.CommitTurnStateOpsV2(backendID, sessionID, []TurnStateOp{{
			TurnID: turnID, DetailLoadState: DetailStateLoading, TurnGeneration: generation,
			ManifestRev: turn.DetailManifestRev, ItemCount: turn.DetailItemCount, TotalBytes: turn.DetailTotalBytes,
		}})
		if err != nil {
			return failTerminal("stale_turn", 0, 0)
		}
		h.eventPublisher.PublishProjectionDetail(backendID, sessionID, patches, requester)
	}

	// 2. Resume state from the store; generation rotation drops the stale
	// cache (a superseded-generation manifest can never accept this
	// generation's pages).
	manifest, err := store.LoadManifest(backendID, sessionID, turnID)
	if err != nil && !errors.Is(err, ErrDetailStoreNotFound) && !errors.Is(err, ErrDetailStoreCorrupt) {
		return failTerminal("upstream_error", 0, 0)
	}
	if err != nil || (manifest != nil &&
		(manifest.Generation != generation || manifest.MappingVersion != turnDetailMappingVersion)) {
		// NotFound = fresh/evicted; Corrupt = quarantine (F2.1: a committed-
		// range defect re-hydrates from official pagination, never "repaired");
		// generation rotation = a superseded cache can never accept this
		// generation's pages; mapping-version rotation = persisted presentation
		// semantics changed. All cases drop the dir and rebuild.
		if err := store.DropTurn(backendID, sessionID, turnID); err != nil {
			return failTerminal("upstream_error", 0, 0)
		}
		manifest = nil
	}

	deliveredFirst, deliveredLast := 0, 0

	// 3. Reconnect fast-path: replay committed chunks after replaySince from
	// the detail cache (no upstream). Chunk frames are rebuilt by the SAME
	// deterministic re-split the store validated at read time.
	if hasReplay && manifest != nil && manifest.ChunkSeqNext > replaySince+1 {
		records, err := store.ReadRecords(backendID, sessionID, turnID)
		if err != nil {
			return failTerminal("upstream_error", 0, 0)
		}
		for _, rec := range records {
			if len(rec.Entries) == 0 {
				continue
			}
			pack := make([]DetailChunkEntry, 0, len(rec.Entries))
			for _, e := range rec.Entries {
				pack = append(pack, DetailChunkEntry{Part: e.Part, Ref: e.Ref})
			}
			chunks, err := SplitDetailChunks(pack)
			if err != nil {
				return failTerminal("upstream_error", 0, 0)
			}
			for i, chunk := range chunks {
				seq := rec.ChunkSeqFirst + i
				if seq <= replaySince {
					continue
				}
				if deliveredFirst == 0 {
					deliveredFirst = seq
				}
				deliveredLast = seq
				frame := TurnDetailChunkFrame{
					Type: "turn_detail_chunk", BackendID: backendID, SessionID: sessionID,
					TurnID: turnID, TurnGeneration: generation, DeliveryID: deliveryID,
					ManifestRev: manifest.ManifestRev, ChunkSeq: seq,
					Items: chunk.Items, Oversize: chunk.Oversize,
					Progress: TurnDetailProgress{
						Pages: rec.Page, Items: pageItemsSoFar(manifest, rec.Page),
						Bytes: pageBytesSoFar(manifest, rec.Page),
					},
				}
				for _, conn := range flight.receivers() {
					if err := h.eventPublisher.PublishTurnDetailChunk(conn, frame); err != nil {
						slog.Warn("go-bridge: turn_detail_chunk replay publish failed",
							"sessionId", sessionID, "turnId", turnID, "chunkSeq", seq, "err", err)
					}
				}
			}
		}
	}

	// 4. Upstream page loop.
	batchStart := time.Now()
	rewalked := false
	cursor := ""
	if manifest != nil && !manifest.Resume.EOF {
		cursor = manifest.Resume.NextCursor
	}
	scratch := core.TurnScopedHistoryTurn{TurnID: turnID, Status: "completed"}
	if manifest != nil && manifest.Resume.Pages > 0 {
		// Resumed batch: earlier pages (including the turn's first
		// userMessage) are committed; seed the user slot so any LATER
		// userMessage maps to a text part instead of replacing the slot the
		// Summary's canonical user part came from.
		scratch.UserItemID = "resumed"
	}
	acceptedIDs := map[string]bool{}
	if manifest != nil {
		for _, item := range manifest.Items {
			acceptedIDs[item.ItemID] = true
		}
	}
	// mappedCount tracks how many Assistant parts of the accumulated
	// scratch were already classified into PRIOR pages of this batch — each
	// page re-maps the whole scratch and slices this prefix off (below).
	mappedCount := 0

	for {
		if time.Since(batchStart) >= turnDetailBatchDeadline {
			// Deadline: partial + resume (NOT a failure). Progress = the
			// store's committed manifest; the kernel summary follows it.
			current := currentTurn()
			if current == nil {
				return failTerminal("stale_turn", deliveredFirst, deliveredLast)
			}
			m, _ := store.LoadManifest(backendID, sessionID, turnID)
			rev, items, bytes := mergeTurnSummary(current, m)
			if rehydrate {
				// Kernel stays loaded; the interrupted rebuild resumes on the
				// next session_turn_items call (store !EOF keeps re-hydration
				// eligible — the loaded short-circuit requires EOF).
				mirrorState(store, backendID, sessionID, turnID, generation, DetailStatePartial, "")
				return &TurnDetailBatchAck{
					DetailLoadState: DetailStateLoaded,
					SyncRev:         h.loadedDetailWatermark(backendID, sessionID, turnID, kernelSyncRev(h, backendID, sessionID)),
					TurnGeneration:  generation,
					ManifestRev:     rev, DeliveryID: deliveryID,
					FirstChunkSeq: deliveredFirst, LastChunkSeq: deliveredLast,
					Progress: progressOf(m, items, bytes),
				}
			}
			_, patches, err := h.projectionKernel.CommitTurnStateOpsV2(backendID, sessionID, []TurnStateOp{{
				TurnID: turnID, DetailLoadState: DetailStatePartial, TurnGeneration: generation,
				ManifestRev: rev, ItemCount: items, TotalBytes: bytes,
			}})
			if err != nil {
				return failTerminal("stale_turn", deliveredFirst, deliveredLast)
			}
			h.eventPublisher.PublishProjectionDetail(backendID, sessionID, patches, requester)
			mirrorState(store, backendID, sessionID, turnID, generation, DetailStatePartial, "")
			return &TurnDetailBatchAck{
				DetailLoadState: DetailStatePartial, SyncRev: patches[len(patches)-1].SyncRev,
				TurnGeneration: generation,
				ManifestRev:    rev, DeliveryID: deliveryID,
				FirstChunkSeq: deliveredFirst, LastChunkSeq: deliveredLast,
				Progress: progressOf(m, items, bytes),
			}
		}

		pageCtx, cancel := context.WithTimeout(context.Background(), core.TurnDetailPageRPCTimeout)
		page, err := pager.ReadTurnItemsPage(pageCtx, sessionID, turnID, cursor)
		cancel()
		if err != nil {
			return failTerminal(turnDetailPageReasonCode(err), deliveredFirst, deliveredLast)
		}
		if page.RawBytes > int(core.TurnDetailPageRawBackstopBytes) {
			return failTerminal(ReasonCodePageOversize, deliveredFirst, deliveredLast)
		}

		// Cursor-invalidation detection (owner resume rules). Outside a
		// re-walk: an empty page that still advances, or a page ENTIRELY of
		// already-accepted ids, means the cursor no longer describes the
		// walk — re-walk from the head skipping committed ids. Inside a
		// re-walk, all-known pages are the expected committed prefix (their
		// entries are filtered below); an empty page with a cursor remains
		// an anomaly — the second one fails the batch.
		allKnown := len(page.Entries) > 0
		for _, entry := range page.Entries {
			if id, _ := entry.Item["id"].(string); !acceptedIDs[id] {
				allKnown = false
				break
			}
		}
		if !page.EOF && len(page.Entries) == 0 {
			if rewalked {
				return failTerminal("upstream_error", deliveredFirst, deliveredLast)
			}
			rewalked = true
			cursor = ""
			continue
		}
		if allKnown && !rewalked {
			rewalked = true
			cursor = ""
			continue
		}

		partsBefore := len(scratch.Parts)
		if err := pager.MapTurnItemsPage(&scratch, page); err != nil {
			return failTerminal(turnDetailPageReasonCode(err), deliveredFirst, deliveredLast)
		}
		// The FIRST userMessage of the batch is consumed by the turn's user
		// slot (the Summary owns it): treat its id as accepted so a re-walk
		// never re-delivers it as a text part. (A RESUMED batch's original
		// user id is unknown here — a re-walk then emits it as a text part
		// carrying the SAME canonical itemId the Summary's user slot uses;
		// clients dedupe by id.)
		if scratch.UserItemID != "" && scratch.UserItemID != "resumed" {
			acceptedIDs[scratch.UserItemID] = true
		}
		var pageEntries []DetailPageEntry
		if newParts := scratch.Parts[partsBefore:]; len(newParts) > 0 {
			// Map the WHOLE accumulated scratch (not the page slice): tool
			// hydrate events carry no turnId — they attribute to the turn the
			// stream established earlier — so a tool-only page mapped in
			// isolation would silently drop its parts. The anchor is the
			// batch's absorbed user slot, falling back to the kernel turn's
			// Summary user identity (resumed batches, tool-only first pages).
			pageTurn := core.TurnScopedHistoryTurn{TurnID: turnID, Status: "completed", Parts: scratch.Parts}
			if scratch.UserItemID != "" && scratch.UserItemID != "resumed" && strings.TrimSpace(scratch.UserText) != "" {
				pageTurn.UserItemID, pageTurn.UserText = scratch.UserItemID, scratch.UserText
			} else if turn.User != nil && len(turn.User.Parts) > 0 {
				pageTurn.UserItemID, pageTurn.UserText = turn.User.Parts[0].ItemID, turn.User.Parts[0].Text
			}
			mapped := upstreamSummaryTurnsToProjection([]core.TurnScopedHistoryTurn{pageTurn})
			if len(mapped) == 1 && mapped[0].Assistant != nil && len(mapped[0].Assistant.Parts) > mappedCount {
				// Deterministic mapper + reducer: the prefix reproduces
				// byte-identically, the suffix is exactly this page's parts.
				if pageEntries, err = classifyDetailParts(mapped[0].Assistant.Parts[mappedCount:]); err != nil {
					return failTerminal("unsupported_item_type", deliveredFirst, deliveredLast)
				}
				mappedCount = len(mapped[0].Assistant.Parts)
			}
		}
		// Re-walk skip: only entries past the accepted boundary are new.
		if rewalked && len(pageEntries) > 0 {
			filtered := pageEntries[:0]
			for _, entry := range pageEntries {
				if !acceptedIDs[entry.ItemID] {
					filtered = append(filtered, entry)
				}
			}
			pageEntries = filtered
		}

		pageNo := 1
		if manifest != nil {
			pageNo = manifest.Resume.Pages + 1
		}
		accepted, err := store.AcceptPage(DetailPageAccept{
			BackendID: backendID, SessionID: sessionID, TurnID: turnID,
			Generation: generation, Page: pageNo, NextCursor: page.NextCursor, EOF: page.EOF,
			Entries: pageEntries,
		})
		if err != nil {
			return failTerminal("upstream_error", deliveredFirst, deliveredLast)
		}
		manifest = accepted.Manifest
		for _, entry := range pageEntries {
			acceptedIDs[entry.ItemID] = true
		}

		current := currentTurn()
		if current == nil || current.TurnGeneration != generation {
			return failTerminal("stale_turn", deliveredFirst, deliveredLast)
		}
		rev, items, bytes := mergeTurnSummary(current, manifest)
		var patches []ProjectionPatch
		if !rehydrate {
			state := DetailStatePartial
			if page.EOF {
				state = DetailStateLoaded
			}
			_, commitPatches, err := h.projectionKernel.CommitTurnStateOpsV2(backendID, sessionID, []TurnStateOp{{
				TurnID: turnID, DetailLoadState: state, TurnGeneration: generation,
				ManifestRev: rev, ItemCount: items, TotalBytes: bytes,
			}})
			if err != nil {
				return failTerminal("stale_turn", deliveredFirst, deliveredLast)
			}
			patches = commitPatches
			h.eventPublisher.PublishProjectionDetail(backendID, sessionID, patches, requester)
		}

		// Deliver this page's chunks (deterministic re-split of the accepted
		// entries — identical to the store's chunkSeq assignment).
		if len(pageEntries) > 0 {
			pack := make([]DetailChunkEntry, 0, len(pageEntries))
			for _, entry := range pageEntries {
				if entry.Inline != nil {
					pack = append(pack, DetailChunkEntry{Part: *entry.Inline})
				} else {
					pack = append(pack, DetailChunkEntry{
						Part: entry.Oversize.Part,
						Ref:  oversizeRefOf(manifest, entry.Oversize),
					})
				}
			}
			chunks, err := SplitDetailChunks(pack)
			if err != nil {
				return failTerminal("upstream_error", deliveredFirst, deliveredLast)
			}
			for i, chunk := range chunks {
				seq := accepted.ChunkSeqFirst + i
				if deliveredFirst == 0 {
					deliveredFirst = seq
				}
				deliveredLast = seq
				frame := TurnDetailChunkFrame{
					Type: "turn_detail_chunk", BackendID: backendID, SessionID: sessionID,
					TurnID: turnID, TurnGeneration: generation, DeliveryID: deliveryID,
					ManifestRev: manifest.ManifestRev, ChunkSeq: seq,
					Items: chunk.Items, Oversize: chunk.Oversize,
					Progress: progressOf(manifest, items, bytes),
				}
				for _, conn := range flight.receivers() {
					if err := h.eventPublisher.PublishTurnDetailChunk(conn, frame); err != nil {
						slog.Warn("go-bridge: turn_detail_chunk publish failed",
							"sessionId", sessionID, "turnId", turnID, "chunkSeq", seq, "err", err)
					}
				}
			}
		}

		if page.EOF {
			mirrorState(store, backendID, sessionID, turnID, generation, DetailStateLoaded, "")
			syncRev := 0
			if rehydrate {
				// No commit happened: the kernel watermark stands, and the
				// binding identity is the REBUILT store's (frames/refs carry
				// it); the kernel's high-water summary rev stays internal.
				syncRev = h.loadedDetailWatermark(backendID, sessionID, turnID, kernelSyncRev(h, backendID, sessionID))
				rev = manifest.ManifestRev
			} else {
				syncRev = patches[len(patches)-1].SyncRev
			}
			return &TurnDetailBatchAck{
				DetailLoadState: DetailStateLoaded, SyncRev: syncRev,
				TurnGeneration: generation,
				ManifestRev:    rev, DeliveryID: deliveryID,
				FirstChunkSeq: deliveredFirst, LastChunkSeq: deliveredLast,
				Progress: progressOf(manifest, items, bytes),
			}
		}
		cursor = page.NextCursor
	}
}

// mirrorState best-effort mirrors a terminal kernel state into the store
// manifest (restart fast-path); failures are logged, never fatal — the
// kernel remains the state authority.
func mirrorState(store *TurnDetailStore, backendID, sessionID, turnID string, generation int, state, reason string) {
	if err := store.UpdateState(backendID, sessionID, turnID, generation, state, reason); err != nil {
		slog.Warn("go-bridge: detail store state mirror failed",
			"sessionId", sessionID, "turnId", turnID, "state", state, "err", err)
	}
}

// oversizeRefOf rebuilds the committed oversize ref for a staged entry from
// the manifest rows the accept just wrote (handle/offsets/bytes/type) plus
// the frozen-budget preview — the ref rides the same chunk as its slim card.
func oversizeRefOf(manifest *TurnDetailManifest, staged *DetailOversizeStaged) *TurnDetailOversizeRef {
	if manifest == nil || staged == nil {
		return nil
	}
	for _, row := range manifest.Items {
		if row.ItemID == staged.Part.ItemID && row.BlobHandle != "" {
			return &TurnDetailOversizeRef{
				ItemID:      row.ItemID,
				Handle:      row.BlobHandle,
				Type:        row.Type,
				TotalBytes:  row.Bytes,
				Preview:     truncateRuneAligned(staged.Preview, core.TurnDetailBlobPreviewBytes),
				TotalChunks: len(row.BlobOffsets) - 1,
			}
		}
	}
	return nil
}

// pageItemsSoFar / pageBytesSoFar summarize progress up to a replayed
// record's page from the manifest's per-item rows.
func pageItemsSoFar(manifest *TurnDetailManifest, page int) int {
	if manifest == nil {
		return 0
	}
	count := 0
	for _, item := range manifest.Items {
		if item.Page <= page {
			count++
		}
	}
	return count
}

func pageBytesSoFar(manifest *TurnDetailManifest, page int) int64 {
	if manifest == nil {
		return 0
	}
	var total int64
	for _, item := range manifest.Items {
		if item.Page <= page {
			total += item.Bytes
		}
	}
	return total
}

// progressOf builds the §11.8 progress triple (store manifest is the truth;
// the kernel summary is the floor).
func progressOf(manifest *TurnDetailManifest, items int, bytes int64) TurnDetailProgress {
	progress := TurnDetailProgress{Items: items, Bytes: bytes}
	if manifest != nil {
		progress = TurnDetailProgress{
			Pages: manifest.Resume.Pages, Items: manifest.ItemCount,
			Bytes: manifest.TotalBytes, EOF: manifest.Resume.EOF,
		}
	}
	return progress
}

func kernelSyncRev(h *Handlers, backendID, sessionID string) int {
	proj, ok := h.projectionKernel.Snapshot(backendID, sessionID)
	if !ok {
		return 0
	}
	return proj.SyncRev
}

// turnDetailPageReasonCode maps the agent's typed page errors onto the v2
// closed set (max_pages/max_bytes do not exist on this path).
func turnDetailPageReasonCode(err error) string {
	switch {
	case errors.Is(err, codexremote.ErrUnknownThreadItem),
		errors.Is(err, codexremote.ErrForeignTurnItem):
		return "unsupported_item_type"
	case errors.Is(err, codexremote.ErrTurnItemsTimeout),
		errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "upstream_error"
	}
}
