package gobridge

// projection_detail_concurrency_test.go pins the audit P0-1/P0-0 concurrency
// invariants the owner called out when reopening Phase 2:
//   - P0-1: a live delta staged (Apply landed, publisher flush not yet run)
//     when the detail commit runs must REACH THE CLIENT — the commit drains the
//     staged delta into the head of its patch chain instead of jumping
//     lastFlushedRev past it into a zero-span patch delivery drops. Asserting
//     delivery, not merely "detail not staled", is the point.
//   - P0-2 (handler wiring): a window connection's held-turn registration rides
//     its window completion, so a later detail commit from ANOTHER connection
//     routes the full content patch to it (rule 3), never a bare no-op.

import (
	"encoding/json"
	"sort"
	"testing"
	"time"
)

// connPatches decodes the projection_patch frames a captured olderWalkConn
// received, optionally only those appended after frameIndex.
func connPatches(t *testing.T, conn *olderWalkConn, after int) []ProjectionPatch {
	t.Helper()
	conn.mu.Lock()
	frames := append([]interface{}(nil), conn.frames...)
	conn.mu.Unlock()
	if after > len(frames) {
		after = len(frames)
	}
	frames = frames[after:]
	patches := make([]ProjectionPatch, 0, 4)
	for _, frame := range frames {
		msg, ok := frame.(EventMessage)
		if !ok || msg.Event != "projection_patch" {
			continue
		}
		encoded, err := json.Marshal(msg.Data)
		if err != nil {
			t.Fatalf("patch encode: %v", err)
		}
		var patch ProjectionPatch
		if err := json.Unmarshal(encoded, &patch); err != nil {
			t.Fatalf("patch decode: %v", err)
		}
		patches = append(patches, patch)
	}
	return patches
}

func connFrameCount(conn *olderWalkConn) int {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	return len(conn.frames)
}

// TestTurnItemsDetailCommitFlushesPendingLiveText is the P0-1 regression: the
// owner's exact scenario — live Apply on T3 sits in the pending accumulator
// when T1's detail commit runs. Old code pushed lastFlushedRev past it, the
// follow-up FlushPatch produced BaseRev==SyncRev, and delivery dropped it: the
// kernel was right, the online client never saw the live text. The commit must
// now drain the staged delta first (patch chain head) and the client must
// actually receive both the live text and the detail content on a contiguous,
// strictly-advancing chain.
func TestTurnItemsDetailCommitFlushesPendingLiveText(t *testing.T) {
	h, conn, sessionID, _ := twoTurnDetailHarness(t)
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	cut := proj.SyncRev
	framesBefore := connFrameCount(conn)

	// Win the race window deterministically: live Apply staged, no flush yet.
	h.projectionKernel.reducer.Apply(projectionReducerEvent(
		"codex-remote", sessionID, "turn_started",
		map[string]interface{}{"turnId": "T3"}, 500, "epoch"))
	h.projectionKernel.reducer.Apply(projectionReducerEvent(
		"codex-remote", sessionID, "text_delta",
		map[string]interface{}{"turnId": "T3", "itemId": "i3", "delta": "live text A"}, 501, "epoch"))

	wireErr, ack := turnItemsDispatch(t, h, conn, sessionID, "T1")
	if wireErr != nil || ack["detailLoadState"] != "loaded" {
		t.Fatalf("ack = %+v err = %+v", ack, wireErr)
	}

	deadline := time.Now().Add(2 * time.Second)
	var patches []ProjectionPatch
	for time.Now().Before(deadline) {
		patches = connPatches(t, conn, framesBefore)
		if len(patches) >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(patches) < 3 {
		t.Fatalf("client received %d patches, want live drain + loading + merge: %+v", len(patches), patches)
	}

	// DELIVERY assertions (not just kernel truth):
	liveSeen, detailSeen := false, false
	for _, patch := range patches {
		if patch.SyncRev <= patch.BaseRev {
			t.Fatalf("zero-span patch delivered: %d→%d %+v", patch.BaseRev, patch.SyncRev, patch)
		}
		for _, op := range patch.PartOps {
			if op.Op == "append_text" && op.TurnID == "T3" && op.Text == "live text A" {
				liveSeen = true
			}
			if op.Op == "replace_parts" && op.TurnID == "T1" {
				detailSeen = true
			}
		}
	}
	if !liveSeen {
		t.Fatalf("staged live text never reached the client: %+v", patches)
	}
	if !detailSeen {
		t.Fatalf("detail replace_parts never reached the client: %+v", patches)
	}

	// Contiguous strictly-advancing chain from the client's cut.
	sort.Slice(patches, func(i, j int) bool { return patches[i].BaseRev < patches[j].BaseRev })
	if patches[0].BaseRev != cut {
		t.Fatalf("chain does not start at the client cut %d: %d→%d", cut, patches[0].BaseRev, patches[0].SyncRev)
	}
	for i := 1; i < len(patches); i++ {
		if patches[i].BaseRev != patches[i-1].SyncRev {
			t.Fatalf("chain gap at %d: %d→%d after %d→%d",
				i, patches[i].BaseRev, patches[i].SyncRev, patches[i-1].BaseRev, patches[i-1].SyncRev)
		}
	}
	if ackSyncRev := int(ack["syncRev"].(float64)); ackSyncRev != patches[len(patches)-1].SyncRev {
		t.Fatalf("ack syncRev %d != final patch %d", ackSyncRev, patches[len(patches)-1].SyncRev)
	}

	// Nothing stranded: the accumulator fence caught up with the kernel head.
	if _, ok := h.projectionKernel.reducer.FlushPatch("codex-remote", sessionID); ok {
		t.Fatal("detail commit stranded staged live content behind lastFlushedRev")
	}
}

// TestWindowConnReceivesDetailCommitForHeldTurn is the P0-2 handler wiring:
// B's window pull registers its held turns inside its completion transaction;
// A's later detail commit for a held turn must route the FULL content patch to
// B (rule 3), not the no-op revision patch a never-registered (or
// post-hoc-registered) connection would get.
func TestWindowConnReceivesDetailCommitForHeldTurn(t *testing.T) {
	h, connB, sessionID, _ := twoTurnDetailHarness(t)
	if _, bFirst := olderWalkDispatch(h, connB, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	}); bFirst["turns"] == nil {
		t.Fatalf("B window_0 = %+v", bFirst)
	}
	quiesceProjectionWrites(t, h)
	framesBeforeB := connFrameCount(connB)

	// A: a second, full-projection connection that requests the detail.
	connA := &olderWalkConn{}
	h.eventPublisher.broadcaster.RegisterConn(connA)
	h.eventPublisher.broadcaster.Subscribe(connA, SubscriptionKey{BackendID: "codex-remote", SessionID: sessionID})
	h.eventPublisher.SetConnSyncV2(connA, true)
	h.eventPublisher.SetConnTurnDetailV1(connA, true)

	wireErr, ack := turnItemsDispatch(t, h, connA, sessionID, "T1")
	if wireErr != nil || ack["detailLoadState"] != "loaded" {
		t.Fatalf("A ack = %+v err = %+v", ack, wireErr)
	}

	// B (window mode, holds T1 from its window response) must have received the
	// commit CONTENT, not a no-op.
	var bContent *ProjectionPatch
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && bContent == nil {
		for _, patch := range connPatches(t, connB, framesBeforeB) {
			for _, op := range patch.PartOps {
				if op.Op == "replace_parts" && op.TurnID == "T1" {
					captured := patch
					bContent = &captured
				}
			}
		}
		if bContent == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if bContent == nil {
		t.Fatalf("window conn never received the detail content patch: %+v", connPatches(t, connB, framesBeforeB))
	}
	found := false
	for _, op := range bContent.TurnStateOps {
		if op.TurnID == "T1" && op.DetailLoadState == DetailStateLoaded {
			found = true
		}
	}
	if !found {
		t.Fatalf("held-turn patch missing the loaded state op: %+v", bContent)
	}
}
