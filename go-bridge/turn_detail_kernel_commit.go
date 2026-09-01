package gobridge

// turn_detail_kernel_commit.go — F3 kernel manifest commit path for
// turn_detail_chunks_v1 (§11.8, plan §3F-F3; F2.1 unblocked 2026-08-31).
// This is the ONLY sanctioned producer of turn manifest-summary mutations:
// the F4 batch engine commits one V2 batch per accepted detail-store page
// (or per batch terminal), keeping the kernel's per-turn manifest summary
// (detailManifestRev/itemCount/totalBytes) in lockstep with the detail
// store's manifest commit point. Detail CONTENT never flows through here —
// the §11.8 layering guarantee.
//
// CommitTurnStateOpsV2 mirrors the frozen v1 CommitTurnStateOps structure:
//   - validation FIRST (ApplyTurnStateOpsV2 is atomic — field-level rules,
//     per-turn generation fence, manifest monotonicity within a generation,
//     loaded-terminal idempotency): a failed batch mutates nothing and must
//     not drain the staged live delta either (the drain advances
//     lastFlushedRev; an unpublished drain would strand live content);
//   - drain-first serialization (P0-1): staged live deltas flush into the
//     head of the returned chain before the commit consumes its rev;
//   - one rev, one patch carrying only turnStateOps (with the manifest
//     summary — additive fields; v1 conns never receive v2 ops).

import (
	"errors"
	"fmt"
)

// CommitTurnStateOpsV2 (reducer half) commits a validated V2 state+manifest
// batch atomically: one rev, one state-only patch.
func (r *ProjectionReducer) CommitTurnStateOpsV2(
	backendID, sessionID string,
	ops []TurnStateOp,
) (SessionProjection, []ProjectionPatch, error) {
	if r == nil {
		return SessionProjection{}, nil, errors.New("projection reducer nil")
	}
	if len(ops) == 0 {
		return SessionProjection{}, nil, fmt.Errorf("%w: empty batch", ErrTurnStateInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil {
		return SessionProjection{}, nil, fmt.Errorf(
			"%w: session %s/%s not present", ErrProjectionPrependInvalid, backendID, sessionID)
	}
	// Validation first, for the same reason as the v1 path: a failed batch
	// must not consume a rev nor advance lastFlushedRev via the drain.
	if err := ApplyTurnStateOpsV2(&ps.projection, ops); err != nil {
		return SessionProjection{}, nil, err
	}
	patches := make([]ProjectionPatch, 0, 2)
	if live, ok := r.flushLocked(ps); ok {
		patches = append(patches, live)
	}
	base := ps.projection.SyncRev
	ps.projection.SyncRev++
	ps.lastFlushedRev = ps.projection.SyncRev
	patches = append(patches, ProjectionPatch{
		BaseRev:      base,
		SyncRev:      ps.projection.SyncRev,
		TurnStateOps: append([]TurnStateOp(nil), ops...),
	})
	return cloneSessionProjection(ps.projection), patches, nil
}

// CommitTurnStateOpsV2 (Kernel gate): READY session required, then the
// reducer transaction. The gate mirrors the v1 gate exactly — the v2 batch
// engine (F4) runs on hydrated sessions only.
func (k *ProjectionKernel) CommitTurnStateOpsV2(
	backendID, sessionID string,
	ops []TurnStateOp,
) (SessionProjection, []ProjectionPatch, error) {
	if k == nil {
		return SessionProjection{}, nil, errors.New("projection kernel nil")
	}
	if len(ops) == 0 {
		return SessionProjection{}, nil, nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.status.Phase != ProjectionHydrateReady {
		return SessionProjection{}, nil, fmt.Errorf(
			"projection: turn state commit requires ready session, %s/%s is %s",
			backendID, sessionID, session.status.Phase,
		)
	}
	return k.reducer.CommitTurnStateOpsV2(backendID, sessionID, ops)
}

// RecoverOrphanDetailLoadingV2 is the §11.8 restore-path orphan recovery: a
// detailLoadState=loading turn whose batch leader died with the previous
// bridge epoch is atomically recovered to failed(interrupted) CARRYING THE
// RETAINED MANIFEST (the v2 rule — a failed op must restate the current
// manifest summary, never zero it; the store keeps the accepted pages so a
// retry resumes from the persisted cursor). partial is NOT an orphan: it is
// the stable between-batches state whose progress lives in the detail store.
// The rev bump journals as a gap exactly like the v1 recovery.
func RecoverOrphanDetailLoadingV2(projection *SessionProjection) bool {
	if projection == nil || len(projection.Turns) == 0 {
		return false
	}
	ops := make([]TurnStateOp, 0, 1)
	for i := range projection.Turns {
		if projection.Turns[i].DetailLoadState != DetailStateLoading {
			continue
		}
		ops = append(ops, TurnStateOp{
			TurnID:          projection.Turns[i].TurnID,
			DetailLoadState: DetailStateFailed,
			ReasonCode:      "interrupted",
			TurnGeneration:  projection.Turns[i].TurnGeneration,
			ManifestRev:     projection.Turns[i].DetailManifestRev,
			ItemCount:       projection.Turns[i].DetailItemCount,
			TotalBytes:      projection.Turns[i].DetailTotalBytes,
		})
	}
	if len(ops) == 0 {
		return false
	}
	if err := ApplyTurnStateOpsV2(projection, ops); err != nil {
		// Inconsistent with its own counters — leave untouched and let the
		// request path's lazy orphan recovery retry honestly.
		return false
	}
	projection.SyncRev++
	return true
}
