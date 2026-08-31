package gobridge

// turn_output_chunk_test.go covers the F5 §11.8 secondary lazy-load RPC:
// full binding echo, chunk walk reassembling the blob at the persisted
// offsets, the blob_evicted retryable error surface, and the eviction
// re-hydration loop (session_turn_items rebuilds the cache WITHOUT kernel
// state commits — loaded stays terminal — with chunkSeq restarting at 1
// under a fresh deliveryId and content-immutable blob handles).

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func turnOutputChunkDispatch(t *testing.T, h *Handlers, conn *olderWalkConn, params map[string]any) (*WireError, *TurnOutputChunkAck) {
	t.Helper()
	conn.mu.Lock()
	conn.err, conn.data = nil, nil
	conn.mu.Unlock()
	raw, _ := json.Marshal(params)
	msg := WireMessage{RequestID: "r-out-chunk", BackendID: "codex-remote", Method: "turn_output_chunk", Params: raw}
	h.handleTurnOutputChunk(conn, msg, nil)
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.err != nil {
		return conn.err, nil
	}
	encoded, _ := json.Marshal(conn.data)
	var ack TurnOutputChunkAck
	if err := json.Unmarshal(encoded, &ack); err != nil {
		t.Fatalf("turn_output_chunk ack decode: %v (%s)", err, encoded)
	}
	return nil, &ack
}

// A missing blob with an intact EOF manifest must invalidate that manifest;
// otherwise the client's session_turn_items retry would take the loaded
// short-circuit and remain trapped in blob_evicted forever.
func TestTurnOutputChunkMissingBlobInvalidatesCacheForRehydration(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	ref, manifestRev, _ := outputChunkFixture(t, h, conn, sessionID, agent)
	blobPath := filepath.Join(h.dataDir, "detail", "codex-remote", hashSeg(sessionID), hashSeg("T1"), "blobs", ref.Handle+".bin")
	if err := os.Remove(blobPath); err != nil {
		t.Fatal(err)
	}

	wireErr, _ := turnOutputChunkDispatch(t, h, conn, map[string]any{
		"sessionId": sessionID, "turnId": "T1", "turnGeneration": 0,
		"manifestRev": manifestRev, "itemId": ref.ItemID, "handle": ref.Handle, "chunkIndex": 0,
	})
	if wireErr == nil || wireErr.Code != "blob_evicted" {
		t.Fatalf("missing blob err = %+v", wireErr)
	}
	if _, err := h.detailStore().LoadManifest("codex-remote", sessionID, "T1"); !errors.Is(err, ErrDetailStoreNotFound) {
		t.Fatalf("missing blob must invalidate complete cache, got %v", err)
	}

	agent.pageFetches = 0
	wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil || ack.DetailLoadState != DetailStateLoaded || !ack.Progress.EOF {
		t.Fatalf("rehydration ack = %+v err = %+v", ack, wireErr)
	}
	if agent.pageFetches == 0 {
		t.Fatal("invalidated cache must re-walk official pagination")
	}
}

// outputChunkFixture loads one oversize turn via the v2 batch and returns
// (ref, manifestRev) for RPC binding plus the full content it must serve.
func outputChunkFixture(t *testing.T, h *Handlers, conn *olderWalkConn, sessionID string, agent *turnDetailAgent) (TurnDetailOversizeRef, int, string) {
	t.Helper()
	oversize := strings.Repeat("y", 300*1024)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "commandExecution", "id": "c1", "command": "cat big.log", "aggregatedOutput": oversize},
	}, "")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil || ack.DetailLoadState != DetailStateLoaded {
		t.Fatalf("batch ack = %+v err = %+v", ack, wireErr)
	}
	frames := waitChunkFrames(t, conn, 1)
	if len(frames[0].Oversize) != 1 {
		t.Fatalf("fixture needs one oversize ref: %+v", frames[0])
	}
	return frames[0].Oversize[0], frames[0].ManifestRev, oversize
}

// TestTurnOutputChunkFullBindingEchoAndChunkWalk pins the wire contract:
// the ack echoes the COMPLETE request binding, and walking chunkIndex
// 0..totalChunks-1 reassembles the exact blob content at the persisted
// offsets (never recomputed).
func TestTurnOutputChunkFullBindingEchoAndChunkWalk(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	ref, manifestRev, content := outputChunkFixture(t, h, conn, sessionID, agent)

	var reassembled strings.Builder
	for index := 0; index < ref.TotalChunks; index++ {
		wireErr, ack := turnOutputChunkDispatch(t, h, conn, map[string]any{
			"sessionId": sessionID, "turnId": "T1", "turnGeneration": 0,
			"manifestRev": manifestRev, "itemId": ref.ItemID, "handle": ref.Handle,
			"chunkIndex": index,
		})
		if wireErr != nil {
			t.Fatalf("chunk %d err = %+v", index, wireErr)
		}
		// Full binding echo, verbatim.
		if ack.TurnGeneration != 0 || ack.ManifestRev != manifestRev ||
			ack.ItemID != ref.ItemID || ack.Handle != ref.Handle || ack.ChunkIndex != index {
			t.Fatalf("echo = %+v (want binding of chunk %d)", ack, index)
		}
		if ack.Encoding != "utf-8" || ack.TotalChunks != ref.TotalChunks || ack.TotalBytes != int64(len(content)) {
			t.Fatalf("chunk %d totals = %+v", index, ack)
		}
		reassembled.WriteString(ack.Data)
	}
	if reassembled.String() != content {
		t.Fatalf("reassembled %d bytes, want %d (chunk boundaries must be the persisted offsets)",
			reassembled.Len(), len(content))
	}
}

// TestTurnOutputChunkErrorSurface pins the request-level errors: capability
// gate, Kernel-adjudicated turn_not_found, stale generation, rebuilt
// manifestRev, unknown handle, chunkIndex bounds.
func TestTurnOutputChunkErrorSurface(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	ref, manifestRev, _ := outputChunkFixture(t, h, conn, sessionID, agent)

	cases := []struct {
		name     string
		mutate   func(params map[string]any)
		code     string
		retry    bool
		unmarkFn func() // optional pre-call conn mutator
	}{
		{
			name:   "capability not negotiated",
			mutate: func(p map[string]any) {},
			code:   "protocol.capability_required", retry: false,
			unmarkFn: func() { h.eventPublisher.SetConnTurnDetailChunksV1(conn, false) },
		},
		{
			name:   "turn_not_found",
			mutate: func(p map[string]any) { p["turnId"] = "nope" },
			code:   "turn_not_found", retry: false,
		},
		{
			name:   "stale generation",
			mutate: func(p map[string]any) { p["turnGeneration"] = 5 },
			code:   "invalid_params", retry: false,
		},
		{
			name:   "rebuilt manifestRev",
			mutate: func(p map[string]any) { p["manifestRev"] = manifestRev + 9 },
			code:   "blob_evicted", retry: true,
		},
		{
			name:   "unknown handle",
			mutate: func(p map[string]any) { p["handle"] = "deadbeef" },
			code:   "blob_evicted", retry: true,
		},
		{
			name:   "handle bound to a different item",
			mutate: func(p map[string]any) { p["itemId"] = "someone-else" },
			code:   "blob_evicted", retry: true,
		},
		{
			name:   "chunkIndex outside the persisted table",
			mutate: func(p map[string]any) { p["chunkIndex"] = ref.TotalChunks + 3 },
			code:   "invalid_params", retry: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{
				"sessionId": sessionID, "turnId": "T1", "turnGeneration": 0,
				"manifestRev": manifestRev, "itemId": ref.ItemID, "handle": ref.Handle,
				"chunkIndex": 0,
			}
			tc.mutate(params)
			if tc.unmarkFn != nil {
				tc.unmarkFn()
				defer h.eventPublisher.SetConnTurnDetailChunksV1(conn, true)
			}
			wireErr, _ := turnOutputChunkDispatch(t, h, conn, params)
			if wireErr == nil || wireErr.Code != tc.code {
				t.Fatalf("err = %+v, want %s", wireErr, tc.code)
			}
			if wireErr.Retryable == nil || *wireErr.Retryable != tc.retry {
				t.Fatalf("retryable = %v, want %v", wireErr.Retryable, tc.retry)
			}
		})
	}
}

// TestTurnOutputChunkEvictedThenRehydrated pins the full F5 recovery loop:
// eviction answers blob_evicted; the next session_turn_items runs the
// re-hydration batch — kernel state and syncRev untouched (loaded is
// terminal), store rebuilt at the same generation, chunks re-delivered
// under a fresh deliveryId with chunkSeq restarting at 1, and the blob
// handle IDENTICAL (content-hash immutability) — after which the RPC with
// the rebuilt manifestRev serves the same bytes.
func TestTurnOutputChunkEvictedThenRehydrated(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	ref, _, content := outputChunkFixture(t, h, conn, sessionID, agent)
	turnBefore := v2KernelTurn(t, h, sessionID, "T1")
	projBefore, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)

	// LRU eviction wipes the whole per-turn dir.
	if err := h.detailStore().DropTurn("codex-remote", sessionID, "T1"); err != nil {
		t.Fatal(err)
	}
	agent.pageFetches = 0
	conn.mu.Lock()
	conn.frames = nil
	conn.mu.Unlock()

	wireErr, _ := turnOutputChunkDispatch(t, h, conn, map[string]any{
		"sessionId": sessionID, "turnId": "T1", "turnGeneration": 0,
		"manifestRev": 1, "itemId": ref.ItemID, "handle": ref.Handle, "chunkIndex": 0,
	})
	if wireErr == nil || wireErr.Code != "blob_evicted" {
		t.Fatalf("evicted blob must answer blob_evicted: %+v", wireErr)
	}
	if wireErr.Retryable == nil || !*wireErr.Retryable {
		t.Fatal("blob_evicted must be retryable")
	}

	// The client re-pulls: session_turn_items on the loaded turn re-hydrates.
	wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("re-hydration err = %+v", wireErr)
	}
	if ack.DetailLoadState != DetailStateLoaded || !ack.Progress.EOF {
		t.Fatalf("re-hydration ack = %+v", ack)
	}
	if ack.FirstChunkSeq != 1 {
		t.Fatalf("re-hydration delivery restarts chunkSeq at 1: [%d,%d]",
			ack.FirstChunkSeq, ack.LastChunkSeq)
	}
	// Kernel untouched: loaded terminal, no state commits, no syncRev move.
	turnAfter := v2KernelTurn(t, h, sessionID, "T1")
	projAfter, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if turnAfter.DetailLoadState != DetailStateLoaded ||
		turnAfter.DetailManifestRev != turnBefore.DetailManifestRev ||
		turnAfter.DetailItemCount != turnBefore.DetailItemCount ||
		projAfter.SyncRev != projBefore.SyncRev {
		t.Fatalf("re-hydration must not touch the kernel: turn %+v syncRev %d→%d",
			turnAfter, projBefore.SyncRev, projAfter.SyncRev)
	}
	if got := agent.pageFetches; got == 0 {
		t.Fatal("re-hydration must re-walk official pagination")
	}
	// Re-delivered frames carry the SAME handle (content-immutable) under a
	// fresh deliveryId.
	frames := waitChunkFrames(t, conn, 1)
	if len(frames[0].Oversize) != 1 || frames[0].Oversize[0].Handle != ref.Handle {
		t.Fatalf("rebuilt ref = %+v (handle must be content-immutable %s)", frames[0].Oversize, ref.Handle)
	}
	if frames[0].DeliveryID == ack.DeliveryID && frames[0].ChunkSeq != 1 {
		t.Fatalf("rebuilt frames = %+v", frames[0])
	}

	// The RPC now succeeds against the REBUILT identity, same bytes.
	wireErr, chunkAck := turnOutputChunkDispatch(t, h, conn, map[string]any{
		"sessionId": sessionID, "turnId": "T1", "turnGeneration": 0,
		"manifestRev": frames[0].ManifestRev, "itemId": ref.ItemID, "handle": ref.Handle,
		"chunkIndex": 0,
	})
	if wireErr != nil {
		t.Fatalf("post-re-hydration chunk err = %+v", wireErr)
	}
	if chunkAck.TotalBytes != int64(len(content)) || !strings.HasPrefix(chunkAck.Data, "yyy") {
		t.Fatalf("rebuilt chunk = %+v", chunkAck)
	}

	// And a further idempotent call short-circuits (cache complete at EOF).
	fetches := agent.pageFetches
	_, repeat := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if repeat.DetailLoadState != DetailStateLoaded || agent.pageFetches != fetches {
		t.Fatalf("complete cache must short-circuit: %+v fetches %d→%d",
			repeat, fetches, agent.pageFetches)
	}
}

// TestRehydrationResumesInterruptedRebuild pins the anti-deadlock rule: a
// re-hydration stopped by the batch deadline leaves the store !EOF, and the
// NEXT session_turn_items CONTINUES the rebuild (the loaded short-circuit
// requires EOF) instead of stranding the client in a blob_evicted loop.
func TestRehydrationResumesInterruptedRebuild(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	oversize := strings.Repeat("z", 300*1024)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a1", "text": "one"},
	}, "c2")
	agent.itemPages["c2"] = itemsPage([]map[string]any{
		{"type": "commandExecution", "id": "c2big", "command": "cat big.log", "aggregatedOutput": oversize},
	}, "c3")
	agent.itemPages["c3"] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a3", "text": "three"},
	}, "")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	wireErr, first := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil || first.DetailLoadState != DetailStateLoaded {
		t.Fatalf("first batch = %+v err = %+v", first, wireErr)
	}
	projLoaded, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)

	if err := h.detailStore().DropTurn("codex-remote", sessionID, "T1"); err != nil {
		t.Fatal(err)
	}

	// Interrupted rebuild: slow cursor page pushes past the shrunken deadline.
	prev := turnDetailBatchDeadline
	turnDetailBatchDeadline = 250 * time.Millisecond
	agent.pageDelay = 300 * time.Millisecond
	conn.mu.Lock()
	conn.frames = nil
	conn.mu.Unlock()
	wireErr, partial := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("interrupted rebuild err = %+v", wireErr)
	}
	if partial.DetailLoadState != DetailStateLoaded || partial.Progress.EOF || partial.Progress.Pages < 1 {
		t.Fatalf("interrupted rebuild ack = %+v (loaded kernel truth, partial store)", partial)
	}
	projMid, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if projMid.SyncRev != projLoaded.SyncRev {
		t.Fatalf("re-hydration must not move syncRev: %d → %d", projLoaded.SyncRev, projMid.SyncRev)
	}

	// The retry CONTINUES the rebuild to EOF.
	turnDetailBatchDeadline = prev
	agent.pageDelay = 0
	wireErr, done := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("resumed rebuild err = %+v", wireErr)
	}
	if done.DetailLoadState != DetailStateLoaded || !done.Progress.EOF || done.Progress.Pages != 3 {
		t.Fatalf("resumed rebuild ack = %+v", done)
	}
	manifest, err := h.detailStore().LoadManifest("codex-remote", sessionID, "T1")
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Resume.EOF || manifest.ItemCount != 3 {
		t.Fatalf("rebuilt manifest = %+v", manifest.Resume)
	}
	projDone, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if projDone.SyncRev != projLoaded.SyncRev {
		t.Fatalf("re-hydration commits leaked into the kernel: %d → %d", projLoaded.SyncRev, projDone.SyncRev)
	}
}
