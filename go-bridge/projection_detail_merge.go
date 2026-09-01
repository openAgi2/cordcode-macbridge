package gobridge

// T2.2 dedicated historical detail merge (lazy-history plan §2.2/§3.2, frozen in
// unified-bridge-protocol.md §11.7 + bridge-v1.md turn_detail_lazy_v1). This is
// deliberately NOT the live reducer lifecycle: no markRunning, no ExecutionView
// change, no ps.tools accumulator, no fake reasoning_delta/text_delta. One
// completed turn's fetched detail is committed atomically as a replace_parts
// PartOp + terminal loaded turnStateOp in ONE projection patch; the projection
// stays the single content writer. The commit shares flushLocked with the live
// FlushPatch path (audit P0-1): staged live deltas drain into the head of the
// returned patch chain so the commit never strands them behind lastFlushedRev.
//
// Admission is the per-turn generation fence (audit-r4 P1-r4-2): the caller
// carries the turn's TurnGeneration observed at fetch start. Any drift (turn
// missing, turn re-activated, generation bumped) is a typed stale — the new
// truth is kept and the request must not overwrite it. A global baseRev is
// intentionally NOT used: a live append on ANOTHER turn must never kill an
// in-flight detail load of this turn.
//
// Failure paths never reach this primitive: an unsupported item / resource gate
// / upstream error commits a state-only failed op via ApplyTurnStateOps (with
// its reasonCode) and keeps the Summary parts untouched — no partial
// replace_parts, never loaded.

import (
	"errors"
	"fmt"
)

var (
	// ErrDetailTargetMissing: the target turn is not in the committed kernel
	// (deleted or never hydrated). The handler adjudicates the request-level
	// turn_not_found WireError (§11.7).
	ErrDetailTargetMissing = errors.New("projection: detail merge target turn missing")
	// ErrDetailTargetRunning: the target turn is no longer a completed turn
	// (re-activated live). Typed stale — keep the new truth.
	ErrDetailTargetRunning = errors.New("projection: detail merge target turn not completed")
)

// MergeHistoricalTurnDetail atomically replaces one completed turn's assistant
// parts with the fetched detail (ordered by official item id, canonical part
// item ids already stamped by the mapper) and sets the terminal loaded state.
// The committed patch carries BOTH the replace_parts PartOp and the loaded
// turnStateOp so replicas apply content and state in one frozen-order
// transaction; iOS completion is replica appliedRev >= patch.SyncRev. The
// mutation bumps the turn's TurnGeneration (the state op in the patch reports
// the POST-bump generation). The user slot is never touched — the Summary user
// part already carries the canonical user item (dedup by persisted itemId).
//
// Audit P0-1 serialization: any live delta staged since the last flush is
// drained FIRST, inside the same reducer critical section, so the commit bases
// at the drained head instead of jumping lastFlushedRev past unflushed content
// (which later produced a zero-span patch delivery drops). The return is the
// atomic consecutive patch CHAIN: [staged-live patch?] + [detail commit patch];
// the caller publishes them in order (live path, then detail routing).
func (r *ProjectionReducer) MergeHistoricalTurnDetail(
	backendID, sessionID, turnID string,
	generation int,
	detailParts []ProjectionPart,
) (SessionProjection, []ProjectionPatch, error) {
	if r == nil {
		return SessionProjection{}, nil, errors.New("projection reducer nil")
	}
	if backendID == "" || sessionID == "" || turnID == "" {
		return SessionProjection{}, nil, fmt.Errorf(
			"%w: empty backend/session/turn id", ErrProjectionPrependInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil {
		return SessionProjection{}, nil, fmt.Errorf(
			"%w: session %s/%s not present", ErrProjectionPrependInvalid, backendID, sessionID)
	}
	at := -1
	for i := range ps.projection.Turns {
		if ps.projection.Turns[i].TurnID == turnID {
			at = i
			break
		}
	}
	if at < 0 {
		return SessionProjection{}, nil, fmt.Errorf("%w: turn %s", ErrDetailTargetMissing, turnID)
	}
	target := &ps.projection.Turns[at]
	if target.Status != "completed" {
		return SessionProjection{}, nil, fmt.Errorf(
			"%w: turn %s status %q", ErrDetailTargetRunning, turnID, target.Status)
	}
	if target.TurnGeneration != generation {
		return SessionProjection{}, nil, fmt.Errorf(
			"%w: turn %s merge generation %d != kernel %d",
			ErrTurnStateStale, turnID, generation, target.TurnGeneration)
	}

	messageID := turnID
	if target.Assistant != nil && target.Assistant.ID != "" {
		messageID = target.Assistant.ID
	}
	if target.Assistant == nil && len(detailParts) > 0 {
		target.Assistant = &MessageProjection{ID: messageID, Role: "assistant"}
	}
	// 空明细也是 loaded（§11.7 明细口径）：detailParts 为空 = 去重后没有任何
	// 明细 item —— 不触碰既有 Summary parts（上游 items 为空时绝不能抹掉
	// Summary 的 final-agent 正文），patch 只携带 turnStateOps。
	if len(detailParts) > 0 && target.Assistant != nil {
		target.Assistant.Parts = cloneParts(detailParts)
	}
	target.DetailLoadState = DetailStateLoaded
	target.DetailReasonCode = ""
	target.TurnGeneration++
	// F3/§11.8: the generation bump is the manifest-baseline reset point —
	// the old generation's manifest summary describes pre-bump detail truth;
	// the new generation starts from zero (a stale V2 op then fails the
	// generation fence, and a fresh V2 load rebuilds the manifest).
	target.DetailManifestRev = 0
	target.DetailItemCount = 0
	target.DetailTotalBytes = 0

	// P0-1 drain-first: flush any staged live delta (other turns) into its own
	// patch BEFORE this commit consumes its rev, so the commit patch bases at
	// the drained head and the chain stays gapless with no stranded content.
	patches := make([]ProjectionPatch, 0, 2)
	if live, ok := r.flushLocked(ps); ok {
		patches = append(patches, live)
	}
	base := ps.projection.SyncRev
	ps.projection.SyncRev++
	ps.lastFlushedRev = ps.projection.SyncRev

	commit := ProjectionPatch{
		BaseRev: base,
		SyncRev: ps.projection.SyncRev,
		TurnStateOps: []TurnStateOp{{
			TurnID:          turnID,
			DetailLoadState: DetailStateLoaded,
			TurnGeneration:  target.TurnGeneration,
		}},
	}
	if len(detailParts) > 0 && target.Assistant != nil {
		commit.PartOps = []PartOp{{
			TurnID:    turnID,
			MessageID: messageID,
			Op:        "replace_parts",
			Parts:     cloneParts(detailParts),
		}}
	}
	patches = append(patches, commit)
	return cloneSessionProjection(ps.projection), patches, nil
}

func cloneParts(parts []ProjectionPart) []ProjectionPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]ProjectionPart, len(parts))
	for i := range parts {
		out[i] = cloneProjectionPart(parts[i])
	}
	return out
}

// CommitTurnStateOps (T2.3) commits a validated state-only batch atomically:
// one rev, one patch carrying only turnStateOps. The session_turn_items state
// machine uses it for the loading admission and every failed terminal commit
// (resource gates, upstream errors, stale fences, orphan recovery). On any
// validation/fence error the projection is untouched (ApplyTurnStateOps is
// atomic) and NO rev is consumed. Same P0-1 drain-first serialization as
// MergeHistoricalTurnDetail: staged live deltas flush into the head of the
// returned chain before the state commit consumes its rev.
func (r *ProjectionReducer) CommitTurnStateOps(
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
	// Validation first: a failed batch must not drain the staged live delta
	// either (the drain advances lastFlushedRev; an unpublished drain would
	// strand the live content exactly like the bug it exists to fix).
	if err := ApplyTurnStateOps(&ps.projection, ops); err != nil {
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

// RecoverOrphanDetailLoading implements §11.7 orphan recovery for the checkpoint
// restore path: a detailLoadState=loading turn with no in-flight leader (the
// bridge crashed/restarted between the loading commit and the terminal commit)
// is atomically recovered to failed(interrupted) — never a permanent loading.
// The mutation bumps the restored projection's rev as a journal gap (conns from
// the dead epoch are gone; new conns realign via snapshot), so a live leader
// concurrently re-admitting the same turn fails its generation fence honestly.
func RecoverOrphanDetailLoading(projection *SessionProjection) bool {
	if projection == nil || len(projection.Turns) == 0 {
		return false
	}
	ops := make([]TurnStateOp, 0, 1)
	for i := range projection.Turns {
		if projection.Turns[i].DetailLoadState == DetailStateLoading {
			ops = append(ops, TurnStateOp{
				TurnID:          projection.Turns[i].TurnID,
				DetailLoadState: DetailStateFailed,
				ReasonCode:      "interrupted",
				TurnGeneration:  projection.Turns[i].TurnGeneration,
			})
		}
	}
	if len(ops) == 0 {
		return false
	}
	if err := ApplyTurnStateOps(projection, ops); err != nil {
		// A fence error here means the projection itself is inconsistent with
		// its own generation counters — leave it untouched and let the request
		// path's lazy orphan recovery retry honestly.
		return false
	}
	projection.SyncRev++
	return true
}

// CommitTurnStateOps is the Kernel gate for state-only commits (loading
// admission, failed terminals, orphan recovery): READY session required.
func (k *ProjectionKernel) CommitTurnStateOps(
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
	return k.reducer.CommitTurnStateOps(backendID, sessionID, ops)
}
