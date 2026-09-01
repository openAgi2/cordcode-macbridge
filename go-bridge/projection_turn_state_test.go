package gobridge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// turnStateTestProjection builds a two-turn Kernel projection with generations set.
func turnStateTestProjection() SessionProjection {
	return SessionProjection{
		SessionID: "sess-ts",
		SyncRev:   7,
		Execution: ExecutionView{Phase: "idle"},
		Turns: []TurnProjection{
			{TurnID: "t_old", Status: "completed", TurnGeneration: 3},
			{TurnID: "t_live", Status: "completed", TurnGeneration: 1},
		},
	}
}

// Old patches (pre turn_detail_lazy_v1) carry no turnStateOps field: decode must
// leave it nil and the field must round-trip absent (exact-shape absence rule).
func TestTurnStateOpsOldPatchDecodeIgnored(t *testing.T) {
	raw := `{"baseRev":6,"syncRev":7,"execution":{"phase":"idle","activeTurnId":""}}`
	var patch ProjectionPatch
	if err := json.Unmarshal([]byte(raw), &patch); err != nil {
		t.Fatal(err)
	}
	if patch.TurnStateOps != nil {
		t.Fatalf("turnStateOps = %v, want nil", patch.TurnStateOps)
	}
	out, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "turnStateOps") {
		t.Fatalf("absence not preserved: %s", out)
	}
}

// Old snapshot turns without detailLoadState decode to the notRequested default.
func TestTurnStateOldSnapshotTurnDecodesNotRequested(t *testing.T) {
	raw := `{"turnId":"t1","status":"completed"}`
	var turn TurnProjection
	if err := json.Unmarshal([]byte(raw), &turn); err != nil {
		t.Fatal(err)
	}
	if got := turn.EffectiveDetailLoadState(); got != "notRequested" {
		t.Fatalf("effective state = %q", got)
	}
	if turn.DetailReasonCode != "" || turn.TurnGeneration != 0 {
		t.Fatalf("unexpected defaults: %+v", turn)
	}
}

// Illegal state/reasonCode combinations are fail-closed: the whole batch is
// rejected and the projection untouched.
func TestTurnStateOpsIllegalCombinationsFailClosed(t *testing.T) {
	cases := []struct {
		name string
		ops  []TurnStateOp
	}{
		{"failed without reasonCode", []TurnStateOp{{TurnID: "t_old", DetailLoadState: DetailStateFailed, TurnGeneration: 3}}},
		{"loading with reasonCode", []TurnStateOp{{TurnID: "t_old", DetailLoadState: DetailStateLoading, ReasonCode: "timeout", TurnGeneration: 3}}},
		{"loaded with reasonCode", []TurnStateOp{{TurnID: "t_old", DetailLoadState: DetailStateLoaded, ReasonCode: "max_pages", TurnGeneration: 3}}},
		{"notRequested on wire", []TurnStateOp{{TurnID: "t_old", DetailLoadState: DetailStateNotRequested, TurnGeneration: 3}}},
		{"unknown state", []TurnStateOp{{TurnID: "t_old", DetailLoadState: "queued", TurnGeneration: 3}}},
		{"reasonCode outside frozen set", []TurnStateOp{{TurnID: "t_old", DetailLoadState: DetailStateFailed, ReasonCode: "mystery", TurnGeneration: 3}}},
		{"empty turnId", []TurnStateOp{{TurnID: "", DetailLoadState: DetailStateLoading, TurnGeneration: 3}}},
		{"negative generation", []TurnStateOp{{TurnID: "t_old", DetailLoadState: DetailStateLoading, TurnGeneration: -1}}},
		{"good op followed by bad op", []TurnStateOp{
			{TurnID: "t_old", DetailLoadState: DetailStateLoading, TurnGeneration: 3},
			{TurnID: "t_live", DetailLoadState: DetailStateFailed, TurnGeneration: 1},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projection := turnStateTestProjection()
			err := ApplyTurnStateOps(&projection, tc.ops)
			if !errors.Is(err, ErrTurnStateInvalid) {
				t.Fatalf("err = %v, want ErrTurnStateInvalid", err)
			}
			for _, turn := range projection.Turns {
				if turn.DetailLoadState != DetailStateNotRequested {
					t.Fatalf("partial application leaked: %+v", turn)
				}
			}
		})
	}
}

func TestTurnStateOpsApplyLifecycle(t *testing.T) {
	projection := turnStateTestProjection()

	// loading commit: state set, no code.
	if err := ApplyTurnStateOps(&projection, []TurnStateOp{
		{TurnID: "t_old", DetailLoadState: DetailStateLoading, TurnGeneration: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if projection.Turns[0].DetailLoadState != DetailStateLoading || projection.Turns[0].DetailReasonCode != "" {
		t.Fatalf("loading = %+v", projection.Turns[0])
	}

	// failed commit: code set atomically.
	if err := ApplyTurnStateOps(&projection, []TurnStateOp{
		{TurnID: "t_old", DetailLoadState: DetailStateFailed, ReasonCode: "timeout", TurnGeneration: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if projection.Turns[0].DetailLoadState != DetailStateFailed || projection.Turns[0].DetailReasonCode != "timeout" {
		t.Fatalf("failed = %+v", projection.Turns[0])
	}

	// retry moves failed → loading and UNCONDITIONALLY clears the old code.
	if err := ApplyTurnStateOps(&projection, []TurnStateOp{
		{TurnID: "t_old", DetailLoadState: DetailStateLoading, TurnGeneration: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if projection.Turns[0].DetailLoadState != DetailStateLoading || projection.Turns[0].DetailReasonCode != "" {
		t.Fatalf("retry loading = %+v", projection.Turns[0])
	}

	// loaded clears code too; multiple ops in one batch hit distinct turns.
	if err := ApplyTurnStateOps(&projection, []TurnStateOp{
		{TurnID: "t_old", DetailLoadState: DetailStateLoaded, TurnGeneration: 3},
		{TurnID: "t_live", DetailLoadState: DetailStateFailed, ReasonCode: "interrupted", TurnGeneration: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if projection.Turns[0].DetailLoadState != DetailStateLoaded || projection.Turns[0].DetailReasonCode != "" {
		t.Fatalf("loaded = %+v", projection.Turns[0])
	}
	if projection.Turns[1].DetailLoadState != DetailStateFailed || projection.Turns[1].DetailReasonCode != "interrupted" {
		t.Fatalf("live failed = %+v", projection.Turns[1])
	}

	// State ops never bump the turn generation (content replace_parts does, T2.2).
	if projection.Turns[0].TurnGeneration != 3 || projection.Turns[1].TurnGeneration != 1 {
		t.Fatalf("generation drifted: %+v", projection.Turns)
	}
}

// Kernel-side strictness: unknown turn is an error (the replica-side per-turn
// no-op rule is the iOS decoder's, not the Kernel's).
func TestTurnStateOpsKernelStrictUnknownTurn(t *testing.T) {
	projection := turnStateTestProjection()
	err := ApplyTurnStateOps(&projection, []TurnStateOp{
		{TurnID: "t_missing", DetailLoadState: DetailStateLoading, TurnGeneration: 0},
	})
	if !errors.Is(err, ErrTurnStateInvalid) {
		t.Fatalf("err = %v, want ErrTurnStateInvalid", err)
	}
}

// Per-turn generation fence: op built against an older generation is typed
// stale; a mutation on ANOTHER turn must not affect this op (no global baseRev).
func TestTurnStateOpsGenerationFence(t *testing.T) {
	projection := turnStateTestProjection()
	// t_live's generation bumped by some post-completion mutation (simulated).
	projection.Turns[1].TurnGeneration = 2

	err := ApplyTurnStateOps(&projection, []TurnStateOp{
		{TurnID: "t_live", DetailLoadState: DetailStateLoading, TurnGeneration: 1},
	})
	if !errors.Is(err, ErrTurnStateStale) {
		t.Fatalf("err = %v, want ErrTurnStateStale", err)
	}
	if projection.Turns[1].DetailLoadState != DetailStateNotRequested {
		t.Fatalf("stale op leaked: %+v", projection.Turns[1])
	}

	// The other turn's change must not fence t_old's op (per-turn, not global).
	if err := ApplyTurnStateOps(&projection, []TurnStateOp{
		{TurnID: "t_old", DetailLoadState: DetailStateLoading, TurnGeneration: 3},
	}); err != nil {
		t.Fatalf("sibling generation change fenced unrelated op: %v", err)
	}
}

// turnStateOps round-trips through the patch JSON with the frozen wire names.
func TestTurnStateOpsPatchRoundTrip(t *testing.T) {
	patch := ProjectionPatch{
		BaseRev: 7, SyncRev: 8,
		TurnStateOps: []TurnStateOp{
			{TurnID: "t_old", DetailLoadState: DetailStateLoaded, TurnGeneration: 3},
			{TurnID: "t_live", DetailLoadState: DetailStateFailed, ReasonCode: "max_bytes", TurnGeneration: 1},
		},
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"turnStateOps"`) || !strings.Contains(string(raw), `"detailLoadState"`) {
		t.Fatalf("wire names missing: %s", raw)
	}
	var back ProjectionPatch
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.TurnStateOps) != 2 || back.TurnStateOps[1].ReasonCode != "max_bytes" {
		t.Fatalf("round-trip = %+v", back.TurnStateOps)
	}
}

// Schema v13 checkpoint round-trip: turn state + manifest/inline fields and
// the codex producer checkpoint persist through save/load; an older checkpoint
// is rejected and rebuilt from the canonical source instead of being restored.
func TestCheckpointV13RoundTripTurnStateAndProducerState(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{\"a\":1}\n{\"a\":2}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := BuildProjectionSourceCheckpoint(ProjectionSourceDescriptor{
		Identity: "codex-remote:sess-ts",
		Path:     sourcePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := turnStateTestProjection()
	projection.Turns[0].DetailLoadState = DetailStateLoaded
	projection.Turns[0].DetailInline = true
	projection.Turns[0].DetailManifestRev = 7
	projection.Turns[0].DetailItemCount = 50
	projection.Turns[0].DetailTotalBytes = 900000
	projection.Turns[1].DetailLoadState = DetailStateFailed
	projection.Turns[1].DetailReasonCode = "timeout"
	// failed carrying the retained manifest is a legal v2 shape
	projection.Turns[1].DetailManifestRev = 2
	projection.Turns[1].DetailItemCount = 10
	projection.Turns[1].DetailTotalBytes = 1000
	now := time.Now().UTC().Truncate(time.Second)
	checkpoint := NewReadyProjectionCheckpoint("codex-remote", "sess-ts", source, projection, now)
	checkpoint.CodexProducerState = &CodexProducerState{
		HasOlderUpstream:   true,
		UpstreamNextCursor: "opaque-upstream-cursor",
		BoundaryTurnID:     "t_old",
		UpdatedAt:          now,
	}

	store := NewProjectionCheckpointStore(dir)
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadValidated("codex-remote", "sess-ts", ProjectionSourceDescriptor{
		Identity: "codex-remote:sess-ts",
		Path:     sourcePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != 13 {
		t.Fatalf("schema = %d", loaded.SchemaVersion)
	}
	turns := loaded.Projection.Turns
	if turns[0].DetailLoadState != DetailStateLoaded || turns[0].EffectiveDetailLoadState() != "loaded" {
		t.Fatalf("turn state lost: %+v", turns[0])
	}
	if !turns[0].DetailInline {
		t.Fatalf("inline detail provenance lost: %+v", turns[0])
	}
	if turns[0].DetailManifestRev != 7 || turns[0].DetailItemCount != 50 || turns[0].DetailTotalBytes != 900000 {
		t.Fatalf("turn manifest summary lost: %+v", turns[0])
	}
	if turns[1].DetailLoadState != DetailStateFailed || turns[1].DetailReasonCode != "timeout" {
		t.Fatalf("failed state lost: %+v", turns[1])
	}
	if turns[1].DetailManifestRev != 2 || turns[1].DetailItemCount != 10 {
		t.Fatalf("failed-turn retained manifest lost: %+v", turns[1])
	}
	if loaded.CodexProducerState == nil ||
		!loaded.CodexProducerState.HasOlderUpstream ||
		loaded.CodexProducerState.UpstreamNextCursor != "opaque-upstream-cursor" ||
		loaded.CodexProducerState.BoundaryTurnID != "t_old" {
		t.Fatalf("producer state lost: %+v", loaded.CodexProducerState)
	}
}

func TestCheckpointV11RejectedAfterSchemaBump(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{\"a\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source, err := BuildProjectionSourceCheckpoint(ProjectionSourceDescriptor{
		Identity: "codex-remote:sess-v11",
		Path:     sourcePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := turnStateTestProjection()
	projection.SessionID = "sess-v11"
	checkpoint := NewReadyProjectionCheckpoint("codex-remote", "sess-v11", source, projection, time.Now())
	checkpoint.SchemaVersion = 11 // simulate a pre-bump checkpoint on disk
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	store := NewProjectionCheckpointStore(dir)
	path, err := store.checkpointPath("codex-remote", "sess-v11")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = store.LoadValidated("codex-remote", "sess-v11", ProjectionSourceDescriptor{
		Identity: "codex-remote:sess-v11",
		Path:     sourcePath,
	})
	if !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("err = %v, want ErrProjectionCheckpointInvalid (rebuild from source)", err)
	}
}

// §11.8 checkpoint-side fail-closed validation (v12): the restored projection's
// detail fields must be internally consistent — an inconsistent checkpoint is
// invalid (rebuilt from source), never restored as-is.
func TestCheckpointTurnDetailFieldValidation(t *testing.T) {
	valid := func(mutate func(*TurnProjection)) func() ProjectionCheckpoint {
		return func() ProjectionCheckpoint {
			projection := turnStateTestProjection()
			projection.Turns[0].DetailLoadState = DetailStateLoaded
			projection.Turns[0].DetailManifestRev = 3
			projection.Turns[0].DetailItemCount = 5
			projection.Turns[0].DetailTotalBytes = 500
			mutate(&projection.Turns[0])
			return ProjectionCheckpoint{
				SchemaVersion: projectionCheckpointSchemaVersion,
				BackendID:     "codex-remote", SessionID: "sess-ts",
				Projection: projection, ProjectionRev: projection.SyncRev,
				HydrateState: ProjectionHydrateReady,
			}
		}
	}
	bad := []struct {
		name  string
		build func() ProjectionCheckpoint
	}{
		{"unknown state", valid(func(t *TurnProjection) { t.DetailLoadState = "paused" })},
		{"inline without loaded", valid(func(t *TurnProjection) {
			t.DetailInline = true
			t.DetailLoadState = DetailStatePartial
		})},
		{"reasonCode without failed", valid(func(t *TurnProjection) { t.DetailReasonCode = "timeout" })},
		{"failed without reasonCode", valid(func(t *TurnProjection) { t.DetailLoadState = DetailStateFailed })},
		{"negative manifestRev", valid(func(t *TurnProjection) { t.DetailManifestRev = -1 })},
		{"itemCount without manifestRev", valid(func(t *TurnProjection) {
			t.DetailManifestRev = 0
			t.DetailItemCount = 5
		})},
		{"totalBytes without manifestRev", valid(func(t *TurnProjection) {
			t.DetailManifestRev = 0
			t.DetailTotalBytes = 500
		})},
	}
	for _, tc := range bad {
		if err := validateTurnDetailCheckpointFields(tc.build().Projection.Turns); err == nil {
			t.Errorf("%s: expected rejection", tc.name)
		} else if !errors.Is(err, ErrProjectionCheckpointInvalid) {
			t.Errorf("%s: err = %v, want ErrProjectionCheckpointInvalid", tc.name, err)
		}
	}
	// The legal shapes pass: v2 partial with a manifest, loaded zero-manifest
	// (v1-path turn), failed carrying a retained manifest, notRequested.
	legal := turnStateTestProjection()
	legal.Turns[0].DetailLoadState = DetailStatePartial
	legal.Turns[0].DetailManifestRev = 2
	legal.Turns[0].DetailItemCount = 4
	legal.Turns[0].DetailTotalBytes = 400
	if err := validateTurnDetailCheckpointFields(legal.Turns); err != nil {
		t.Fatalf("legal v2 shapes rejected: %v", err)
	}
}

// Producer checkpoint invariants: claiming older upstream requires both the
// internal cursor and the kernel boundary turn it was observed at.
func TestCodexProducerStateValidation(t *testing.T) {
	if err := ValidateCodexProducerState(CodexProducerState{HasOlderUpstream: false}); err != nil {
		t.Fatalf("EOF state rejected: %v", err)
	}
	if err := ValidateCodexProducerState(CodexProducerState{HasOlderUpstream: true, UpstreamNextCursor: "c", BoundaryTurnID: "t_old"}); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
	// Cursor-less WITH boundary is the explicit head re-walk state (R11d typed
	// stale-cursor recovery) — valid.
	if err := ValidateCodexProducerState(CodexProducerState{HasOlderUpstream: true, BoundaryTurnID: "t_old"}); err != nil {
		t.Fatalf("head re-walk state rejected: %v", err)
	}
	if err := ValidateCodexProducerState(CodexProducerState{HasOlderUpstream: true, UpstreamNextCursor: "c"}); err == nil {
		t.Fatal("boundary-less claim accepted")
	}
}
