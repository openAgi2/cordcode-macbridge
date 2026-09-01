package gobridge

// turn_detail_batch_engine_test.go covers the F4 §11.8 v2 batch engine:
// multi-page EOF batch with kernel/store manifest lockstep, deadline partial
// + resume with chunkSeq continuity, reconnect fast-path replay, cursor
// invalidation re-walk (and the second-anomaly failure), the 4MB single-page
// backstop, typed page failures, singleflight follower fan-out, orphan
// loading recovery, idempotent loaded acks, and generation-rotation cache
// drop.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/codex-remote"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// ---------- fake pager surface on turnDetailAgent ----------

// ReadTurnItemsPage serves the §11.8 v2 scripted pages (cursor → page;
// unknown cursor = clean EOF). pageGate blocks every fetch while open,
// pageDelay simulates slow upstream pages for the deadline tests.
func (a *turnDetailAgent) ReadTurnItemsPage(ctx context.Context, sessionID, turnID, cursor string) (*core.TurnItemsPage, error) {
	atomic.AddInt64(&a.pageFetches, 1)
	if a.pageGate != nil {
		<-a.pageGate
	}
	if a.pageDelay > 0 && cursor != "" {
		time.Sleep(a.pageDelay)
	}
	if a.pageErr != nil {
		return nil, a.pageErr
	}
	if page, ok := a.itemPages[cursor]; ok {
		return page, nil
	}
	return &core.TurnItemsPage{EOF: true}, nil
}

// MapTurnItemsPage mirrors mapRemoteHistoryItem's OUTPUT discipline for the
// item shapes these tests script (first-user absorption into the slot,
// official itemId on every mapped part). The real mapper's wire decoding has
// its own agent-side tests.
func (a *turnDetailAgent) MapTurnItemsPage(turn *core.TurnScopedHistoryTurn, page *core.TurnItemsPage) error {
	for _, entry := range page.Entries {
		id, _ := entry.Item["id"].(string)
		switch entry.Item["type"] {
		case "userMessage":
			text, _ := entry.Item["text"].(string)
			if turn.UserItemID == "" {
				turn.UserItemID, turn.UserText = id, text
			} else if text != "" {
				turn.Parts = append(turn.Parts, map[string]any{"type": "text", "content": text, "itemId": id})
			}
		case "agentMessage":
			text, _ := entry.Item["text"].(string)
			part := map[string]any{"type": "text", "content": text, "itemId": id}
			switch entry.Item["phase"] {
			case "commentary":
				part["presentation"] = "progress"
			case "final_answer":
				part["presentation"] = "final"
			}
			turn.Parts = append(turn.Parts, part)
		case "commandExecution":
			command, _ := entry.Item["command"].(string)
			step := map[string]any{
				"id": id, "toolName": "Bash", "status": "completed",
				"toolInput": map[string]any{"command": command, "cwd": "/tmp"},
				"title":     command,
			}
			if output, ok := entry.Item["aggregatedOutput"]; ok {
				step["output"] = output
			}
			turn.Parts = append(turn.Parts, map[string]any{"type": "tool", "step": step, "itemId": id})
		default:
			return fmt.Errorf("%w: %v", codexremote.ErrUnknownThreadItem, entry.Item["type"])
		}
	}
	return nil
}

// ---------- v2 harness ----------

func turnDetailV2Harness(t *testing.T) (*Handlers, *olderWalkConn, string, *turnDetailAgent) {
	t.Helper()
	h, conn, sessionID, agent := turnDetailHarness(t, core.TurnScopedHistoryTurn{})
	h.eventPublisher.SetConnTurnDetailChunksV1(conn, true)
	agent.itemPages = map[string]*core.TurnItemsPage{}
	return h, conn, sessionID, agent
}

func itemsPage(items []map[string]any, nextCursor string) *core.TurnItemsPage {
	page := &core.TurnItemsPage{}
	for _, item := range items {
		page.Entries = append(page.Entries, core.TurnItemsEntry{TurnID: "T1", Item: item})
	}
	if nextCursor == "" {
		page.EOF = true
		return page
	}
	page.NextCursor = nextCursor
	return page
}

func turnItemsDispatchV2(t *testing.T, h *Handlers, conn *olderWalkConn, sessionID, turnID string, extra map[string]any) (*WireError, *TurnDetailBatchAck) {
	t.Helper()
	conn.mu.Lock()
	conn.err, conn.data = nil, nil
	conn.mu.Unlock()
	params := map[string]any{"sessionId": sessionID, "turnId": turnID}
	for key, value := range extra {
		params[key] = value
	}
	raw, _ := json.Marshal(params)
	msg := WireMessage{RequestID: "r-items-v2", BackendID: "codex-remote", Method: "session_turn_items", Params: raw}
	h.handleSessionTurnItems(conn, msg, nil)
	quiesceProjectionWrites(t, h)
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.err != nil {
		return conn.err, nil
	}
	encoded, _ := json.Marshal(conn.data)
	var ack TurnDetailBatchAck
	if err := json.Unmarshal(encoded, &ack); err != nil {
		t.Fatalf("v2 ack decode: %v (%s)", err, encoded)
	}
	return nil, &ack
}

func chunkFramesOf(conn *olderWalkConn) []TurnDetailChunkFrame {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	var out []TurnDetailChunkFrame
	for _, frame := range conn.frames {
		if chunk, ok := frame.(TurnDetailChunkFrame); ok {
			out = append(out, chunk)
		}
	}
	return out
}

func waitChunkFrames(t *testing.T, conn *olderWalkConn, want int) []TurnDetailChunkFrame {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := chunkFramesOf(conn); len(got) >= want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d turn_detail_chunk frames, got %d", want, len(chunkFramesOf(conn)))
	return nil
}

func v2KernelTurn(t *testing.T, h *Handlers, sessionID, turnID string) TurnProjection {
	t.Helper()
	proj, ok := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if !ok {
		t.Fatal("projection missing")
	}
	for _, turn := range proj.Turns {
		if turn.TurnID == turnID {
			return turn
		}
	}
	t.Fatalf("turn %s missing", turnID)
	return TurnProjection{}
}

// ---------- happy path ----------

// TestSessionTurnItemsV2HappyMultiPageEOF pins the full batch: pages flow
// page-by-page through store accept + V2 kernel commit (manifest summary in
// lockstep), chunks ride the per-connection overlay with contiguous chunkSeq
// and the ack's identity, the oversize command lands as a slim card + blob
// ref, and the kernel's Assistant parts stay the SUMMARY (the §11.8
// layering guarantee — detail content never enters the kernel).
func TestSessionTurnItemsV2HappyMultiPageEOF(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	oversize := strings.Repeat("x", 300*1024)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "userMessage", "id": "u1", "text": "q1"},
		{"type": "agentMessage", "id": "a1", "text": "answer part 1"},
		{"type": "commandExecution", "id": "c1", "command": "echo hi", "aggregatedOutput": "hi\n"},
	}, "c2")
	agent.itemPages["c2"] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a2", "text": "answer part 2"},
		{"type": "commandExecution", "id": "c2big", "command": "cat big.log", "aggregatedOutput": oversize},
	}, "c3")
	agent.itemPages["c3"] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a3", "text": "final"},
	}, "")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("ack err = %+v", wireErr)
	}
	if ack.DetailLoadState != DetailStateLoaded || ack.ReasonCode != "" {
		t.Fatalf("ack = %+v", ack)
	}
	if ack.FirstChunkSeq != 1 || ack.LastChunkSeq != 3 {
		t.Fatalf("ack chunk range = [%d,%d], want [1,3]", ack.FirstChunkSeq, ack.LastChunkSeq)
	}
	if ack.ManifestRev != 3 || !ack.Progress.EOF || ack.Progress.Pages != 3 || ack.Progress.Items != 5 {
		t.Fatalf("ack manifest/progress = %+v", ack)
	}
	if ack.DeliveryID == "" || ack.SyncRev <= 0 {
		t.Fatalf("ack identity = %+v", ack)
	}

	// Kernel ↔ store lockstep, loaded terminal, SUMMARY untouched.
	turn := v2KernelTurn(t, h, sessionID, "T1")
	if turn.DetailLoadState != DetailStateLoaded || turn.DetailManifestRev != 3 ||
		turn.DetailItemCount != 5 || turn.DetailTotalBytes <= 300*1024 {
		t.Fatalf("kernel manifest summary = %+v", turn)
	}
	if len(turn.Assistant.Parts) != 1 || turn.Assistant.Parts[0].Text != "summary answer" {
		t.Fatalf("kernel must keep the Summary parts: %+v", turn.Assistant.Parts)
	}
	store := h.detailStore()
	manifest, err := store.LoadManifest("codex-remote", sessionID, "T1")
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Resume.EOF || manifest.Resume.Pages != 3 || manifest.Generation != turn.TurnGeneration {
		t.Fatalf("store manifest = %+v", manifest.Resume)
	}
	if manifest.State != DetailStateLoaded {
		t.Fatalf("terminal state must mirror into the store: %q", manifest.State)
	}

	// Overlay frames: contiguous 1..3, ack identity, per-page manifestRev.
	frames := waitChunkFrames(t, conn, 3)
	if len(frames) != 3 {
		t.Fatalf("frames = %d", len(frames))
	}
	for i, frame := range frames {
		if frame.ChunkSeq != i+1 || frame.DeliveryID != ack.DeliveryID ||
			frame.TurnID != "T1" || frame.TurnGeneration != turn.TurnGeneration ||
			frame.ManifestRev != i+1 || frame.Type != "turn_detail_chunk" {
			t.Fatalf("frame[%d] = %+v", i, frame)
		}
		if i < len(frames)-1 && frame.Progress.EOF {
			t.Fatalf("frame[%d] must not claim EOF before the final commit", i)
		}
	}
	// Page-1 chunk: inline tool card WITH its small output.
	if got := frames[0].Items; len(got) != 2 || got[0].ItemID != "a1" || got[1].ItemID != "c1" ||
		got[1].ToolResult != "hi\n" {
		t.Fatalf("page-1 chunk items = %+v", got)
	}
	// Page-2 chunk: inline text + SLIM oversize card (output stripped) + ref.
	if got := frames[1].Items; len(got) != 2 || got[1].ItemID != "c2big" || got[1].ToolResult != nil ||
		got[1].ToolName != "Bash" {
		t.Fatalf("page-2 slim card = %+v", got)
	}
	if len(frames[1].Oversize) != 1 {
		t.Fatalf("page-2 oversize refs = %+v", frames[1].Oversize)
	}
	ref := frames[1].Oversize[0]
	if ref.ItemID != "c2big" || ref.Handle == "" || ref.TotalBytes != int64(len(oversize)) ||
		ref.TotalChunks < 1 || len(ref.Preview) == 0 || !strings.HasPrefix(ref.Preview, "xxxx") {
		t.Fatalf("oversize ref = %+v", ref)
	}
	// The blob serves its committed content at the persisted offsets.
	chunk, total, totalBytes, err := store.ReadBlobChunk("codex-remote", sessionID, "T1", ref.Handle, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != ref.TotalChunks || totalBytes != int64(len(oversize)) || !strings.HasPrefix(chunk, "xxxx") {
		t.Fatalf("blob chunk = %d/%d bytes prefix %q", len(chunk), totalBytes, chunk[:8])
	}
	// Page-3 chunk is the final one and carries EOF progress.
	if !frames[2].Progress.EOF || frames[2].Items[0].ItemID != "a3" {
		t.Fatalf("final chunk = %+v", frames[2])
	}
}

// TestSessionTurnItemsV2IdempotentLoadedAck pins the v2 loaded repeat: ack
// from kernel truth + store progress, a fresh deliveryId, and NO upstream.
func TestSessionTurnItemsV2IdempotentLoadedAck(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a1", "text": "only answer"},
	}, "")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	_, first := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if first.DetailLoadState != DetailStateLoaded {
		t.Fatalf("first ack = %+v", first)
	}
	fetches := atomic.LoadInt64(&agent.pageFetches)

	_, repeat := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if repeat.DetailLoadState != DetailStateLoaded || repeat.ManifestRev != first.ManifestRev ||
		repeat.FirstChunkSeq != 0 || repeat.LastChunkSeq != 0 {
		t.Fatalf("repeat ack = %+v (no new chunks on an idempotent repeat)", repeat)
	}
	if !repeat.Progress.EOF || repeat.Progress.Items != first.Progress.Items {
		t.Fatalf("repeat progress = %+v", repeat.Progress)
	}
	if repeat.DeliveryID == first.DeliveryID {
		t.Fatal("repeat must mint a fresh deliveryId")
	}
	if got := atomic.LoadInt64(&agent.pageFetches); got != fetches {
		t.Fatalf("idempotent repeat refetched upstream: %d → %d", fetches, got)
	}
}

// A cache created by the pre-phase mapper must never remain the loaded
// fast-path forever. The runtime rebuilds it from official pagination and the
// replacement chunks preserve commentary/final provenance.
func TestSessionTurnItemsV2RebuildsPrePhaseMappingCache(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "commentary-1", "text": "working", "phase": "commentary"},
		{"type": "agentMessage", "id": "final-1", "text": "done", "phase": "final_answer"},
	}, "")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	_, first := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if first.DetailLoadState != DetailStateLoaded {
		t.Fatalf("first ack = %+v", first)
	}

	store := h.detailStore()
	store.mu.Lock()
	dir, err := store.turnDir("codex-remote", sessionID, "T1")
	if err != nil {
		store.mu.Unlock()
		t.Fatal(err)
	}
	manifest, err := store.loadManifestLocked(dir)
	if err == nil {
		manifest.MappingVersion = 0
		err = store.persistManifestLocked(dir, manifest)
	}
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	conn.mu.Lock()
	conn.frames = nil
	conn.mu.Unlock()
	fetches := atomic.LoadInt64(&agent.pageFetches)
	_, rebuilt := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if rebuilt.DetailLoadState != DetailStateLoaded || atomic.LoadInt64(&agent.pageFetches) <= fetches {
		t.Fatalf("stale mapping cache was not rebuilt: ack=%+v fetches=%d->%d",
			rebuilt, fetches, atomic.LoadInt64(&agent.pageFetches))
	}
	frames := waitChunkFrames(t, conn, 1)
	if got := frames[0].Items; len(got) != 2 || got[0].Presentation != "progress" || got[1].Presentation != "final" {
		t.Fatalf("rebuilt phase mapping = %+v", got)
	}
	manifest, err = store.LoadManifest("codex-remote", sessionID, "T1")
	if err != nil || manifest.MappingVersion != turnDetailMappingVersion {
		t.Fatalf("rebuilt manifest = %+v err=%v", manifest, err)
	}
}

// ---------- deadline partial + resume ----------

// TestSessionTurnItemsV2DeadlinePartialAndResume pins the 90s batch
// semantics at the test seam: reaching the deadline commits partial with the
// accepted progress (NOT failed), and the next batch resumes from the store
// cursor with chunkSeq continuing across batches.
func TestSessionTurnItemsV2DeadlinePartialAndResume(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a1", "text": "part one"},
	}, "c2")
	agent.itemPages["c2"] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a2", "text": "part two"},
	}, "c3")
	agent.itemPages["c3"] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a3", "text": "part three"},
	}, "")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	prev := turnDetailBatchDeadline
	turnDetailBatchDeadline = 250 * time.Millisecond
	defer func() { turnDetailBatchDeadline = prev }()
	// The slow page-2 fetch pushes the batch past the deadline AFTER page 2
	// is accepted — the loop-top check trips before page 3.
	agent.pageDelay = 300 * time.Millisecond

	wireErr, partial := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("ack err = %+v", wireErr)
	}
	if partial.DetailLoadState != DetailStatePartial || partial.ReasonCode != "" {
		t.Fatalf("deadline must commit partial, got %+v", partial)
	}
	if partial.Progress.Pages != 2 || partial.Progress.Items != 2 || partial.Progress.EOF {
		t.Fatalf("partial progress = %+v", partial.Progress)
	}
	if partial.FirstChunkSeq != 1 || partial.LastChunkSeq != 2 {
		t.Fatalf("partial chunk range = [%d,%d], want [1,2]", partial.FirstChunkSeq, partial.LastChunkSeq)
	}
	turn := v2KernelTurn(t, h, sessionID, "T1")
	if turn.DetailLoadState != DetailStatePartial || turn.DetailManifestRev != 2 || turn.DetailItemCount != 2 {
		t.Fatalf("kernel after partial = %+v", turn)
	}

	// Resume batch: plain continuation from the store cursor.
	agent.pageDelay = 0
	wireErr, resumed := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("resume err = %+v", wireErr)
	}
	if resumed.DetailLoadState != DetailStateLoaded {
		t.Fatalf("resume ack = %+v", resumed)
	}
	if resumed.FirstChunkSeq != 3 || resumed.LastChunkSeq != 3 {
		t.Fatalf("resume chunk range = [%d,%d], want [3,3] (chunkSeq continues across batches)",
			resumed.FirstChunkSeq, resumed.LastChunkSeq)
	}
	if resumed.ManifestRev != 3 || !resumed.Progress.EOF || resumed.Progress.Items != 3 {
		t.Fatalf("resume ack = %+v", resumed)
	}
	// The resumed batch only walked the remaining page.
	if got := atomic.LoadInt64(&agent.pageFetches); got != 3 {
		t.Fatalf("page fetches = %d, want 2 (partial) + 1 (resume)", got)
	}
	// Resumed chunks did NOT redeliver committed content.
	frames := waitChunkFrames(t, conn, 3)
	seen := map[int]bool{}
	for _, frame := range frames {
		if seen[frame.ChunkSeq] {
			t.Fatalf("chunkSeq %d delivered twice", frame.ChunkSeq)
		}
		seen[frame.ChunkSeq] = true
	}
}

// TestSessionTurnItemsV2FastPathReplay pins the reconnect fast-path:
// replaySinceChunkSeq rebuilds the committed chunk frames from the detail
// cache (deterministic re-split, no upstream refetch of replayed pages) and
// the batch then continues upstream.
func TestSessionTurnItemsV2FastPathReplay(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a1", "text": "part one"},
	}, "c2")
	agent.itemPages["c2"] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a2", "text": "part two"},
	}, "c3")
	agent.itemPages["c3"] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a3", "text": "part three"},
	}, "")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	prev := turnDetailBatchDeadline
	turnDetailBatchDeadline = 250 * time.Millisecond
	agent.pageDelay = 300 * time.Millisecond
	_, partial := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if partial.DetailLoadState != DetailStatePartial || partial.Progress.Pages != 2 {
		t.Fatalf("partial ack = %+v", partial)
	}
	turnDetailBatchDeadline = prev
	agent.pageDelay = 0
	fetches := atomic.LoadInt64(&agent.pageFetches)
	conn.mu.Lock()
	conn.frames = nil
	conn.mu.Unlock()

	wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", map[string]any{"replaySinceChunkSeq": 0})
	if wireErr != nil {
		t.Fatalf("replay err = %+v", wireErr)
	}
	if ack.DetailLoadState != DetailStateLoaded {
		t.Fatalf("replay ack = %+v", ack)
	}
	// The replay delivered chunks 1..2 BEFORE the new chunk 3 — the ack's
	// range spans the whole delivery.
	if ack.FirstChunkSeq != 1 || ack.LastChunkSeq != 3 {
		t.Fatalf("replay ack range = [%d,%d], want [1,3]", ack.FirstChunkSeq, ack.LastChunkSeq)
	}
	frames := waitChunkFrames(t, conn, 3)
	wantTexts := []string{"part one", "part two", "part three"}
	for i, frame := range frames {
		if frame.ChunkSeq != i+1 || len(frame.Items) != 1 || frame.Items[0].Text != wantTexts[i] {
			t.Fatalf("replayed frame[%d] = %+v (want %q)", i, frame, wantTexts[i])
		}
	}
	// Only the continuation page hit upstream.
	if got := atomic.LoadInt64(&agent.pageFetches); got != fetches+1 {
		t.Fatalf("replay refetched committed pages: %d → %d", fetches, got)
	}
}

// ---------- cursor invalidation ----------

// TestSessionTurnItemsV2CursorInvalidationRewalk pins the owner resume
// rules: a page entirely of already-accepted ids (stale upstream cursor)
// triggers ONE head re-walk that skips committed ids by canonical item id —
// all-known pages inside the re-walk are the expected prefix, not an anomaly
// — and the walk still reaches a clean EOF with new content accepted.
func TestSessionTurnItemsV2CursorInvalidationRewalk(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "userMessage", "id": "u1", "text": "q1"},
		{"type": "agentMessage", "id": "a1", "text": "one"},
	}, "c2")
	agent.itemPages["c2"] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a2", "text": "two"},
	}, "c3")
	// The stale cursor returns ALREADY-ACCEPTED items — invalidation signal.
	agent.itemPages["c3"] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a1", "text": "one"},
		{"type": "agentMessage", "id": "a2", "text": "two"},
	}, "c4")
	// Head re-walk: c4 serves the NEW tail and EOF.
	agent.itemPages["c4"] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a3", "text": "three"},
	}, "")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("ack err = %+v", wireErr)
	}
	if ack.DetailLoadState != DetailStateLoaded {
		t.Fatalf("re-walk must still reach loaded: %+v", ack)
	}
	store := h.detailStore()
	manifest, err := store.LoadManifest("codex-remote", sessionID, "T1")
	if err != nil {
		t.Fatal(err)
	}
	// Accepted: a1,a2 in normal mode; the re-walk skipped them (no
	// duplicates) and accepted a3; the user message is not a detail item.
	if manifest.ItemCount != 3 || !manifest.Resume.EOF {
		t.Fatalf("manifest = %+v (items %d, resume %+v)", manifest.Items, manifest.ItemCount, manifest.Resume)
	}
	seen := map[string]int{}
	for _, item := range manifest.Items {
		seen[item.ItemID]++
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("item %s accepted %d times — re-walk must skip committed ids", id, count)
		}
	}
	turn := v2KernelTurn(t, h, sessionID, "T1")
	if turn.DetailLoadState != DetailStateLoaded || turn.DetailItemCount != 3 {
		t.Fatalf("kernel = %+v", turn)
	}
}

// TestSessionTurnItemsV2SecondAnomalyFailsUpstream pins the second-anomaly
// rule: an empty page with a cursor DURING the re-walk fails the batch
// upstream_error with the committed progress retained.
func TestSessionTurnItemsV2SecondAnomalyFailsUpstream(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a1", "text": "one"},
	}, "c2")
	// First anomaly: empty page that still advances.
	agent.itemPages["c2"] = itemsPage(nil, "c3")
	// Second anomaly inside the re-walk: another empty advancing page.
	agent.itemPages["c3"] = itemsPage(nil, "c4")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("ack err = %+v", wireErr)
	}
	if ack.DetailLoadState != DetailStateFailed || ack.ReasonCode != "upstream_error" {
		t.Fatalf("ack = %+v (want failed upstream_error)", ack)
	}
	if ack.Progress.Items != 1 || ack.FirstChunkSeq != 1 || ack.LastChunkSeq != 1 {
		t.Fatalf("progress must be retained: %+v", ack)
	}
	turn := v2KernelTurn(t, h, sessionID, "T1")
	if turn.DetailLoadState != DetailStateFailed || turn.DetailReasonCode != "upstream_error" ||
		turn.DetailItemCount != 1 || turn.DetailManifestRev != 2 {
		t.Fatalf("kernel failed state must carry the retained manifest (rev 2 = 1 item page + 1 empty re-walk page): %+v", turn)
	}
}

// ---------- page oversize + typed failures ----------

// TestSessionTurnItemsV2PageOversizeRetainsProgress pins the 4MB
// single-page raw backstop: page_oversize fails the batch, the earlier
// pages' committed progress and summary survive.
func TestSessionTurnItemsV2PageOversizeRetainsProgress(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a1", "text": "one"},
	}, "c2")
	huge := &core.TurnItemsPage{Entries: []core.TurnItemsEntry{
		{TurnID: "T1", Item: map[string]any{"type": "agentMessage", "id": "big1", "text": "x"}},
	}, NextCursor: "c3", RawBytes: 5 * 1024 * 1024}
	agent.itemPages["c2"] = huge
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("ack err = %+v", wireErr)
	}
	if ack.DetailLoadState != DetailStateFailed || ack.ReasonCode != "page_oversize" {
		t.Fatalf("ack = %+v", ack)
	}
	if ack.Progress.Items != 1 || ack.ManifestRev != 1 {
		t.Fatalf("progress must be retained: %+v", ack)
	}
	turn := v2KernelTurn(t, h, sessionID, "T1")
	if turn.DetailReasonCode != "page_oversize" || turn.DetailItemCount != 1 {
		t.Fatalf("kernel = %+v", turn)
	}
}

// TestSessionTurnItemsV2TypedPageFailures pins the reasonCode mapping and
// atomicity: the FIRST page fails ⇒ nothing reaches the store, the kernel
// lands failed with the mapped code, and the Summary parts survive.
func TestSessionTurnItemsV2TypedPageFailures(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		reasonCode string
	}{
		{"unknown item", fmt.Errorf("%w: futureItem", codexremote.ErrUnknownThreadItem), "unsupported_item_type"},
		{"foreign turn item", codexremote.ErrForeignTurnItem, "unsupported_item_type"},
		{"timeout", codexremote.ErrTurnItemsTimeout, "timeout"},
		{"generic upstream", errors.New("wss broke"), "upstream_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, conn, sessionID, agent := turnDetailV2Harness(t)
			agent.pageErr = tc.err
			olderWalkDispatch(h, conn, map[string]any{
				"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
			})
			quiesceProjectionWrites(t, h)
			wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
			if wireErr != nil {
				t.Fatalf("process failures are success-shaped: %+v", wireErr)
			}
			if ack.DetailLoadState != DetailStateFailed || ack.ReasonCode != tc.reasonCode {
				t.Fatalf("ack = %+v", ack)
			}
			if _, err := h.detailStore().LoadManifest("codex-remote", sessionID, "T1"); !errors.Is(err, ErrDetailStoreNotFound) {
				t.Fatalf("failed first page must leave no store manifest: %v", err)
			}
			turn := v2KernelTurn(t, h, sessionID, "T1")
			if turn.DetailLoadState != DetailStateFailed || turn.DetailReasonCode != tc.reasonCode {
				t.Fatalf("kernel = %+v", turn)
			}
			if turn.DetailManifestRev != 0 || turn.DetailItemCount != 0 {
				t.Fatalf("nothing was accepted — manifest summary must stay zero: %+v", turn)
			}
			if len(turn.Assistant.Parts) != 1 || turn.Assistant.Parts[0].Text != "summary answer" {
				t.Fatalf("summary parts disturbed: %+v", turn.Assistant.Parts)
			}
		})
	}
}

// ---------- singleflight ----------

// TestSessionTurnItemsV2SingleflightFollowerFrames pins the v2 singleflight:
// a follower on a SECOND connection mirrors the leader's ack verbatim and
// receives the same batch's chunk frames through the fan-out.
func TestSessionTurnItemsV2SingleflightFollowerFrames(t *testing.T) {
	h, conn1, sessionID, agent := turnDetailV2Harness(t)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a1", "text": "answer"},
	}, "")
	olderWalkDispatch(h, conn1, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	conn2 := &olderWalkConn{}
	h.eventPublisher.SetConnSyncV2(conn2, true)
	h.eventPublisher.SetConnProjectionWindowV1(conn2, true)
	h.eventPublisher.SetConnTurnDetailV1(conn2, true)
	h.eventPublisher.SetConnTurnDetailChunksV1(conn2, true)

	gate := make(chan struct{})
	agent.pageGate = gate // block the leader inside its first page fetch
	var leaderAck, followerAck *TurnDetailBatchAck
	var leaderErr, followerErr *WireError
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		leaderErr, leaderAck = turnItemsDispatchV2(t, h, conn1, sessionID, "T1", nil)
	}()
	time.Sleep(150 * time.Millisecond) // leader admitted + fetching
	go func() {
		defer wg.Done()
		followerErr, followerAck = turnItemsDispatchV2(t, h, conn2, sessionID, "T1", nil)
	}()
	time.Sleep(150 * time.Millisecond) // follower joined the flight
	close(gate)
	wg.Wait()

	if leaderErr != nil || followerErr != nil {
		t.Fatalf("acks err = %+v / %+v", leaderErr, followerErr)
	}
	if leaderAck == nil || followerAck == nil ||
		leaderAck.DeliveryID != followerAck.DeliveryID ||
		leaderAck.DetailLoadState != DetailStateLoaded ||
		followerAck.DetailLoadState != DetailStateLoaded ||
		leaderAck.SyncRev != followerAck.SyncRev ||
		leaderAck.FirstChunkSeq != followerAck.FirstChunkSeq ||
		leaderAck.LastChunkSeq != followerAck.LastChunkSeq {
		t.Fatalf("follower must mirror the leader's terminal ack: %+v vs %+v", leaderAck, followerAck)
	}
	if got := atomic.LoadInt64(&agent.pageFetches); got != 1 {
		t.Fatalf("page fetches = %d — singleflight must share one batch", got)
	}
	if frames := waitChunkFrames(t, conn2, 1); len(frames) != 1 || frames[0].DeliveryID != leaderAck.DeliveryID {
		t.Fatalf("follower frames = %+v", frames)
	}
	if frames := waitChunkFrames(t, conn1, 1); len(frames) != 1 {
		t.Fatalf("leader frames = %+v", frames)
	}
}

// ---------- orphan recovery + generation rotation ----------

// TestSessionTurnItemsV2OrphanLoadingRecovered pins both halves: the direct
// V2 recovery commits failed(interrupted) CARRYING the retained manifest
// summary, and the handler-level flow (orphan detected, then a real batch)
// still finishes loaded.
func TestSessionTurnItemsV2OrphanLoadingRecovered(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a1", "text": "answer"},
	}, "")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	// Simulate a crashed batch: loading committed with progress retained.
	if _, _, err := h.projectionKernel.CommitTurnStateOpsV2("codex-remote", sessionID, []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStateLoading, TurnGeneration: 0,
		ManifestRev: 2, ItemCount: 4, TotalBytes: 4096,
	}}); err != nil {
		t.Fatal(err)
	}
	target := v2KernelTurn(t, h, sessionID, "T1")
	h.recoverOrphanLoadingTurnV2("codex-remote", sessionID, "T1", &target)
	recovered := v2KernelTurn(t, h, sessionID, "T1")
	if recovered.DetailLoadState != DetailStateFailed || recovered.DetailReasonCode != "interrupted" {
		t.Fatalf("recovered = %+v", recovered)
	}
	if recovered.DetailManifestRev != 2 || recovered.DetailItemCount != 4 || recovered.DetailTotalBytes != 4096 {
		t.Fatalf("recovery must CARRY the retained manifest: %+v", recovered)
	}

	// Handler level: a fresh request sees the failed state, runs the batch,
	// finishes loaded.
	wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("ack err = %+v", wireErr)
	}
	if ack.DetailLoadState != DetailStateLoaded {
		t.Fatalf("ack = %+v", ack)
	}
	if turn := v2KernelTurn(t, h, sessionID, "T1"); turn.DetailLoadState != DetailStateLoaded {
		t.Fatalf("final kernel state = %+v", turn)
	}
}

// TestSessionTurnItemsV2GenerationRotationDropsStaleStore pins the rotation
// rule: a store manifest from a SUPERSEDED generation can never accept the
// current generation's pages — the engine drops the cache and rebuilds it
// from official pagination.
func TestSessionTurnItemsV2GenerationRotationDropsStaleStore(t *testing.T) {
	h, conn, sessionID, agent := turnDetailV2Harness(t)
	agent.itemPages[""] = itemsPage([]map[string]any{
		{"type": "agentMessage", "id": "a1", "text": "fresh truth"},
	}, "")
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	// Stale-epoch cache: a previous generation's manifest for this turn.
	store := h.detailStore()
	if _, err := store.AcceptPage(DetailPageAccept{
		BackendID: "codex-remote", SessionID: sessionID, TurnID: "T1",
		Generation: 7, Page: 1, EOF: true,
		Entries: []DetailPageEntry{{
			ItemID: "stale1",
			Inline: &ProjectionPart{Type: "text", ItemID: "stale1", Text: "stale"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
	if wireErr != nil {
		t.Fatalf("ack err = %+v", wireErr)
	}
	if ack.DetailLoadState != DetailStateLoaded {
		t.Fatalf("ack = %+v", ack)
	}
	manifest, err := store.LoadManifest("codex-remote", sessionID, "T1")
	if err != nil {
		t.Fatal(err)
	}
	turn := v2KernelTurn(t, h, sessionID, "T1")
	if manifest.Generation != turn.TurnGeneration {
		t.Fatalf("rebuilt manifest generation = %d, kernel = %d", manifest.Generation, turn.TurnGeneration)
	}
	if manifest.ItemCount != 1 || manifest.Items[0].ItemID != "a1" {
		t.Fatalf("stale cache must be dropped and rebuilt: %+v", manifest.Items)
	}
}

// TestSessionTurnItemsV2ToolOnlyPageAnchoring pins the tool-attribution
// regression: tool hydrate events carry no turnId (they attach to the turn
// established earlier in the mapped stream), so tool-only pages — mid-batch
// AND as the first page of a resumed batch (kernel Summary user anchor) —
// must still map into parts instead of silently dropping.
func TestSessionTurnItemsV2ToolOnlyPageAnchoring(t *testing.T) {
	load := func(t *testing.T) (*Handlers, *olderWalkConn, string, *turnDetailAgent) {
		h, conn, sessionID, agent := turnDetailV2Harness(t)
		agent.itemPages[""] = itemsPage([]map[string]any{
			{"type": "userMessage", "id": "u1", "text": "q1"},
			{"type": "agentMessage", "id": "a1", "text": "anchor"},
		}, "c2")
		agent.itemPages["c2"] = itemsPage([]map[string]any{
			{"type": "commandExecution", "id": "c1", "command": "ls", "aggregatedOutput": "out"},
		}, "")
		olderWalkDispatch(h, conn, map[string]any{
			"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
		})
		quiesceProjectionWrites(t, h)
		return h, conn, sessionID, agent
	}

	t.Run("mid-batch tool-only page", func(t *testing.T) {
		h, conn, sessionID, _ := load(t)
		wireErr, ack := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
		if wireErr != nil || ack.DetailLoadState != DetailStateLoaded {
			t.Fatalf("ack = %+v err = %+v", ack, wireErr)
		}
		frames := waitChunkFrames(t, conn, 2)
		last := frames[len(frames)-1]
		if len(last.Items) == 0 || last.Items[0].Type != "tool" || last.Items[0].ItemID != "c1" ||
			last.Items[0].ToolResult != "out" {
			t.Fatalf("tool-only page dropped its part: %+v", last.Items)
		}
	})

	t.Run("resumed batch opens with a tool-only page", func(t *testing.T) {
		h, conn, sessionID, _ := load(t)
		// Stage the resumed-batch precondition deterministically: page 1
		// committed in the store + partial in the kernel, next page is the
		// tool-only one.
		if _, err := h.detailStore().AcceptPage(DetailPageAccept{
			BackendID: "codex-remote", SessionID: sessionID, TurnID: "T1",
			Generation: 0, Page: 1, NextCursor: "c2",
			Entries: []DetailPageEntry{{
				ItemID: "a1",
				Inline: &ProjectionPart{Type: "text", ItemID: "a1", Text: "anchor"},
			}},
		}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := h.projectionKernel.CommitTurnStateOpsV2("codex-remote", sessionID, []TurnStateOp{{
			TurnID: "T1", DetailLoadState: DetailStatePartial, TurnGeneration: 0,
			ManifestRev: 1, ItemCount: 1, TotalBytes: 64,
		}}); err != nil {
			t.Fatal(err)
		}

		wireErr, resumed := turnItemsDispatchV2(t, h, conn, sessionID, "T1", nil)
		if wireErr != nil || resumed.DetailLoadState != DetailStateLoaded {
			t.Fatalf("resumed ack = %+v err = %+v", resumed, wireErr)
		}
		// The resumed batch's scratch is empty ("resumed" seed) — the kernel
		// Summary user slot is the only anchor for the tool-only page.
		frames := waitChunkFrames(t, conn, 1)
		if len(frames[0].Items) == 0 || frames[0].Items[0].Type != "tool" ||
			frames[0].Items[0].ItemID != "c1" || frames[0].Items[0].ToolResult != "out" {
			t.Fatalf("resumed tool-only page dropped its part: %+v", frames[0].Items)
		}
	})
}
