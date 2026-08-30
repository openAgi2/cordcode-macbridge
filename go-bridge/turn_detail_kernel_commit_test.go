package gobridge

// turn_detail_kernel_commit_test.go — F3 (§11.8 kernel manifest commit path):
// the V2 commit chain (validation-first, drain-first, one-rev state-only
// patch carrying the manifest summary), the generation-bump manifest-baseline
// reset at the detail-merge site, and the V2 restore orphan recovery carrying
// the retained manifest. Checkpoint v12 round-trip/rejection lives in
// projection_turn_state_test.go beside the v11 originals.

import (
	"errors"
	"testing"
)

// v2CompletedTurnReducer builds a reducer holding one completed turn T1
// (generation 0) via the live lifecycle, the only sanctioned path to a
// completed turn at reducer level.
func v2CompletedTurnReducer(t *testing.T) *ProjectionReducer {
	t.Helper()
	r := NewProjectionReducer()
	r.Apply(projectionReducerEvent("codex-remote", "sess", "turn_started",
		map[string]interface{}{"turnId": "T1"}, 1, "epoch"))
	r.Apply(projectionReducerEvent("codex-remote", "sess", "turn_completed",
		map[string]interface{}{"turnId": "T1"}, 2, "epoch"))
	return r
}

// The V2 commit chain: one rev per batch, ops carried verbatim on the wire
// (manifest summary included), kernel turn fields stamped, consecutive revs,
// loaded terminal + idempotent repeat, monotonicity enforced against the
// CURRENT kernel state.
func TestCommitTurnStateOpsV2ManifestChain(t *testing.T) {
	r := v2CompletedTurnReducer(t)
	// Admission first (realistic batch-engine order). Its chain legitimately
	// drains the staged turn_completed upsert — the assertions below start
	// from the steady state after it.
	if _, _, err := r.CommitTurnStateOpsV2("codex-remote", "sess", []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStateLoading, TurnGeneration: 0,
	}}); err != nil {
		t.Fatal(err)
	}

	p1, patches, err := r.CommitTurnStateOpsV2("codex-remote", "sess", []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStatePartial, TurnGeneration: 0,
		ManifestRev: 3, ItemCount: 15, TotalBytes: 4096,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 1 {
		t.Fatalf("steady-state chain = %+v, want exactly the commit patch", patches)
	}
	patch := patches[0]
	if patch.SyncRev != patch.BaseRev+1 || len(patch.TurnStateOps) != 1 {
		t.Fatalf("patch = %+v", patch)
	}
	op := patch.TurnStateOps[0]
	if op.ManifestRev != 3 || op.ItemCount != 15 || op.TotalBytes != 4096 || op.DetailLoadState != DetailStatePartial {
		t.Fatalf("ops must carry the manifest summary verbatim: %+v", op)
	}
	if len(patch.PartOps) != 0 || len(patch.UpsertTurns) != 0 {
		t.Fatalf("V2 commit must be state-only: %+v", patch)
	}
	if p1.Turns[0].DetailManifestRev != 3 || p1.Turns[0].DetailItemCount != 15 ||
		p1.Turns[0].DetailTotalBytes != 4096 || p1.Turns[0].DetailLoadState != DetailStatePartial {
		t.Fatalf("kernel manifest summary not stamped: %+v", p1.Turns[0])
	}

	// loaded advances; the idempotent same-rev repeat stays legal (and, as a
	// successful commit, consumes its rev like any other); any further
	// advance past loaded is terminal-rejected; a manifest regression is
	// rejected with NO rev consumed.
	p2, _, err := r.CommitTurnStateOpsV2("codex-remote", "sess", []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStateLoaded, TurnGeneration: 0,
		ManifestRev: 4, ItemCount: 20, TotalBytes: 8192,
	}})
	if err != nil || p2.SyncRev != p1.SyncRev+1 {
		t.Fatalf("loaded advance: %v, rev %d→%d", err, p1.SyncRev, p2.SyncRev)
	}
	repeat, _, err := r.CommitTurnStateOpsV2("codex-remote", "sess", []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStateLoaded, TurnGeneration: 0,
		ManifestRev: 4, ItemCount: 20, TotalBytes: 8192,
	}})
	if err != nil {
		t.Fatalf("idempotent loaded repeat must pass: %v", err)
	}
	before := repeat.SyncRev
	for name, bad := range map[string][]TurnStateOp{
		"loaded then advanced rev": {{TurnID: "T1", DetailLoadState: DetailStateLoaded, TurnGeneration: 0, ManifestRev: 5, ItemCount: 21, TotalBytes: 8192}},
		"loaded then partial":      {{TurnID: "T1", DetailLoadState: DetailStatePartial, TurnGeneration: 0, ManifestRev: 4, ItemCount: 20, TotalBytes: 8192}},
		"manifest regression":      {{TurnID: "T1", DetailLoadState: DetailStatePartial, TurnGeneration: 0, ManifestRev: 3, ItemCount: 20, TotalBytes: 8192}},
	} {
		after, _, err := r.CommitTurnStateOpsV2("codex-remote", "sess", bad)
		if err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
		if after.SyncRev != 0 || after.Turns != nil {
			t.Fatalf("%s: rejected commit must return no projection", name)
		}
	}
	fin, _ := r.Snapshot("codex-remote", "sess")
	if fin.SyncRev != before {
		t.Fatalf("rejected commits consumed revs: %d → %d", before, fin.SyncRev)
	}
	if fin.Turns[0].DetailManifestRev != 4 || fin.Turns[0].DetailLoadState != DetailStateLoaded {
		t.Fatalf("kernel state changed by rejected commits: %+v", fin.Turns[0])
	}
}

// Drain-first (P0-1) for the V2 path: a staged live delta on ANOTHER turn
// rides the head of the commit chain; a FAILED validation drains nothing —
// the staged delta must survive for the next successful commit.
func TestCommitTurnStateOpsV2DrainsStagedLiveDelta(t *testing.T) {
	r := v2CompletedTurnReducer(t)
	// Stage live content on T2 (Apply only — no flush yet).
	r.Apply(projectionReducerEvent("codex-remote", "sess", "turn_started",
		map[string]interface{}{"turnId": "T2"}, 10, "epoch"))
	r.Apply(projectionReducerEvent("codex-remote", "sess", "text_delta",
		map[string]interface{}{"turnId": "T2", "itemId": "i2", "delta": "live staged"}, 11, "epoch"))

	// A rejected batch must not drain: manifest regression against a kernel
	// that has no manifest yet is impossible, so use the reasonCode rule.
	if _, _, err := r.CommitTurnStateOpsV2("codex-remote", "sess", []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStatePartial, TurnGeneration: 0, ReasonCode: "timeout",
	}}); !errors.Is(err, ErrTurnStateInvalid) {
		t.Fatalf("err = %v", err)
	}

	_, patches, err := r.CommitTurnStateOpsV2("codex-remote", "sess", []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStatePartial, TurnGeneration: 0,
		ManifestRev: 1, ItemCount: 2, TotalBytes: 128,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 2 {
		t.Fatalf("chain = %d patches, want [staged-live, commit]", len(patches))
	}
	if len(patches[0].PartOps) == 0 {
		t.Fatalf("chain head must be the staged live delta: %+v", patches[0])
	}
	if len(patches[1].TurnStateOps) != 1 {
		t.Fatalf("chain tail must be the V2 commit: %+v", patches[1])
	}
	if patches[0].SyncRev != patches[1].BaseRev || patches[1].SyncRev != patches[1].BaseRev+1 {
		t.Fatalf("chain must be contiguous and one-rev for the commit: %+v", patches)
	}
}

// The Kernel gate: READY session required; on the ready harness the V2 commit
// lands through the gate with the same shape as the reducer path.
func TestCommitTurnStateOpsV2KernelGate(t *testing.T) {
	if _, _, err := NewProjectionKernel(NewProjectionReducer(), nil).CommitTurnStateOpsV2(
		"codex-remote", "nope", []TurnStateOp{{TurnID: "T1", DetailLoadState: DetailStateLoading, TurnGeneration: 0}},
	); err == nil {
		t.Fatal("absent session must be gated (not ready)")
	}

	h, conn, sessionID, _ := turnDetailHarness(t, detailTurnFixture())
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)
	proj, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)

	_, patches, err := h.projectionKernel.CommitTurnStateOpsV2("codex-remote", sessionID, []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStateLoading, TurnGeneration: 0,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) != 1 || patches[0].TurnStateOps[0].DetailLoadState != DetailStateLoading {
		t.Fatalf("gate commit = %+v", patches)
	}
	after, _ := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if after.SyncRev != proj.SyncRev+1 {
		t.Fatalf("gate commit consumed %d→%d", proj.SyncRev, after.SyncRev)
	}
}

// F3 core rule: the detail-merge generation bump is the manifest-baseline
// reset point — the pre-bump manifest is zeroed with the generation, and a
// fresh V2 sequence under the new generation starts from zero.
func TestGenerationBumpResetsManifestBaseline(t *testing.T) {
	r := v2CompletedTurnReducer(t)
	if _, _, err := r.CommitTurnStateOpsV2("codex-remote", "sess", []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStatePartial, TurnGeneration: 0,
		ManifestRev: 7, ItemCount: 50, TotalBytes: 900000,
	}}); err != nil {
		t.Fatal(err)
	}

	// Post-completion content mutation (v1 merge admission) bumps generation
	// and must zero the manifest summary of the superseded generation.
	parts := []ProjectionPart{{Type: "text", Text: "merged detail", ItemID: "d1"}}
	merged, _, err := r.MergeHistoricalTurnDetail("codex-remote", "sess", "T1", 0, parts)
	if err != nil {
		t.Fatal(err)
	}
	turn := merged.Turns[0]
	if turn.TurnGeneration != 1 || turn.DetailLoadState != DetailStateLoaded {
		t.Fatalf("post-merge turn = %+v", turn)
	}
	if turn.DetailManifestRev != 0 || turn.DetailItemCount != 0 || turn.DetailTotalBytes != 0 {
		t.Fatalf("bump must reset the manifest baseline: %+v", turn)
	}

	// A V2 op from the OLD generation is fenced stale by the bump.
	if _, _, err := r.CommitTurnStateOpsV2("codex-remote", "sess", []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStatePartial, TurnGeneration: 0,
		ManifestRev: 8, ItemCount: 51, TotalBytes: 900001,
	}}); !errors.Is(err, ErrTurnStateStale) {
		t.Fatalf("old-generation op err = %v, want ErrTurnStateStale", err)
	}

	// Loaded (set by the merge — content now lives in the kernel) is terminal:
	// further V2 commits on this turn are rejected by design. The zero
	// baseline itself is a kernel-state property — a fresh generation admits
	// manifest 1/3/512 as a legal FIRST advance (no pre-bump summary to carry):
	fresh := SessionProjection{SessionID: "sess", Turns: []TurnProjection{
		{TurnID: "T1", Status: "completed", TurnGeneration: 1},
	}}
	if err := ApplyTurnStateOpsV2(&fresh, []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStatePartial, TurnGeneration: 1,
		ManifestRev: 1, ItemCount: 3, TotalBytes: 512,
	}}); err != nil {
		t.Fatalf("fresh-generation zero baseline rejected: %v", err)
	}
	if fresh.Turns[0].DetailManifestRev != 1 || fresh.Turns[0].DetailItemCount != 3 {
		t.Fatalf("fresh-generation manifest = %+v", fresh.Turns[0])
	}
	if _, _, err := r.CommitTurnStateOpsV2("codex-remote", "sess", []TurnStateOp{{
		TurnID: "T1", DetailLoadState: DetailStatePartial, TurnGeneration: 1,
		ManifestRev: 1, ItemCount: 3, TotalBytes: 512,
	}}); !errors.Is(err, ErrTurnStateInvalid) {
		t.Fatalf("post-merge loaded is terminal for V2 commits, err = %v", err)
	}
}

// §11.8 restore recovery: loading → failed(interrupted) CARRYING the retained
// manifest (never zeroed); partial is NOT an orphan (stable between-batches
// state whose progress lives in the detail store); nothing to do → false.
func TestRecoverOrphanDetailLoadingV2(t *testing.T) {
	proj := SessionProjection{SessionID: "s", SyncRev: 5, Turns: []TurnProjection{
		{TurnID: "T1", Status: "completed", DetailLoadState: DetailStateLoading, TurnGeneration: 2,
			DetailManifestRev: 5, DetailItemCount: 40, DetailTotalBytes: 12345},
		{TurnID: "T2", Status: "completed", DetailLoadState: DetailStatePartial, TurnGeneration: 1,
			DetailManifestRev: 3, DetailItemCount: 12, DetailTotalBytes: 3000},
		{TurnID: "T3", Status: "completed", DetailLoadState: DetailStateLoaded, TurnGeneration: 0,
			DetailManifestRev: 9, DetailItemCount: 90, DetailTotalBytes: 999999},
	}}
	if !RecoverOrphanDetailLoadingV2(&proj) {
		t.Fatal("loading turn must be recovered")
	}
	if proj.SyncRev != 6 {
		t.Fatalf("recovery must journal as a rev gap: %d", proj.SyncRev)
	}
	t1 := proj.Turns[0]
	if t1.DetailLoadState != DetailStateFailed || t1.DetailReasonCode != "interrupted" {
		t.Fatalf("recovered turn = %+v", t1)
	}
	if t1.DetailManifestRev != 5 || t1.DetailItemCount != 40 || t1.DetailTotalBytes != 12345 {
		t.Fatalf("recovery must CARRY the retained manifest, not zero it: %+v", t1)
	}
	if proj.Turns[1].DetailLoadState != DetailStatePartial || proj.Turns[1].DetailManifestRev != 3 {
		t.Fatalf("partial is not an orphan: %+v", proj.Turns[1])
	}
	if proj.Turns[2].DetailLoadState != DetailStateLoaded {
		t.Fatalf("loaded untouched: %+v", proj.Turns[2])
	}
	if RecoverOrphanDetailLoadingV2(&proj) {
		t.Fatal("no loading turns → false")
	}
	if RecoverOrphanDetailLoadingV2(nil) {
		t.Fatal("nil projection → false")
	}
}
