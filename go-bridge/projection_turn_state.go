package gobridge

import (
	"errors"
	"fmt"
)

// turn_detail_lazy_v1 turn-level detail states (bridge-v1.md, frozen 2026-08-30).
// DetailStateNotRequested exists only as the decode default of an absent wire
// field; it is never emitted in a network op.
const (
	DetailStateNotRequested = ""
	DetailStateLoading      = "loading"
	DetailStateLoaded       = "loaded"
	DetailStateFailed       = "failed"
)

var (
	// ErrTurnStateInvalid covers every fail-closed decode/apply rule violation
	// (illegal state value, failed without reasonCode, loading/loaded with
	// reasonCode, empty turnId, negative generation, unknown target turn on
	// the Kernel side).
	ErrTurnStateInvalid = errors.New("projection: invalid turnStateOps")
	// ErrTurnStateStale is the per-turn generation fence: the op was built
	// against an older generation of the target turn (typed stale; the new
	// truth is kept, the old request must not overwrite it).
	ErrTurnStateStale = errors.New("projection: turnStateOps stale turn generation")
)

// TurnStateReasonCodes is the frozen closed set (unified-bridge-protocol.md §11.7).
var TurnStateReasonCodes = map[string]bool{
	"upstream_error":        true,
	"unsupported_item_type": true,
	"max_pages":             true,
	"max_bytes":             true,
	"timeout":               true,
	"stale_turn":            true,
	"interrupted":           true,
}

// ValidateTurnStateOps enforces the frozen fail-closed invariants on a wire
// batch: state ∈ {loading, loaded, failed}; failed ⇒ non-empty reasonCode from
// the closed set; loading/loaded ⇒ reasonCode absent; turnId non-empty;
// generation ≥ 0. The whole batch is rejected on the first violation — ops are
// never partially applied.
func ValidateTurnStateOps(ops []TurnStateOp) error {
	for i, op := range ops {
		if op.TurnID == "" {
			return fmt.Errorf("%w: op[%d] empty turnId", ErrTurnStateInvalid, i)
		}
		if op.TurnGeneration < 0 {
			return fmt.Errorf("%w: op[%d] negative generation", ErrTurnStateInvalid, i)
		}
		switch op.DetailLoadState {
		case DetailStateFailed:
			if op.ReasonCode == "" {
				return fmt.Errorf("%w: op[%d] failed without reasonCode", ErrTurnStateInvalid, i)
			}
			if !TurnStateReasonCodes[op.ReasonCode] {
				return fmt.Errorf("%w: op[%d] reasonCode %q outside frozen set", ErrTurnStateInvalid, i, op.ReasonCode)
			}
		case DetailStateLoading, DetailStateLoaded:
			if op.ReasonCode != "" {
				return fmt.Errorf("%w: op[%d] %s carries reasonCode", ErrTurnStateInvalid, i, op.DetailLoadState)
			}
		default:
			return fmt.Errorf("%w: op[%d] state %q", ErrTurnStateInvalid, i, op.DetailLoadState)
		}
	}
	return nil
}

// ApplyTurnStateOps applies a validated batch to the Kernel-side projection.
// Kernel semantics are STRICTER than the replica rule: the target turn must
// exist and the op's generation must match the turn's current TurnGeneration
// (per-turn stale-write fence — a global baseRev is intentionally NOT used, so
// a live append on another turn cannot kill an in-flight detail load). State
// ops themselves never bump TurnGeneration; the content mutation's
// replace_parts admission bumps it. On any error the projection is untouched.
func ApplyTurnStateOps(projection *SessionProjection, ops []TurnStateOp) error {
	if len(ops) == 0 {
		return nil
	}
	if err := ValidateTurnStateOps(ops); err != nil {
		return err
	}
	index := make(map[string]int, len(projection.Turns))
	for i := range projection.Turns {
		index[projection.Turns[i].TurnID] = i
	}
	type turnMutation struct {
		index  int
		state  string
		reason string
	}
	mutations := make([]turnMutation, 0, len(ops))
	for i, op := range ops {
		at, ok := index[op.TurnID]
		if !ok {
			return fmt.Errorf("%w: op[%d] unknown turn %q", ErrTurnStateInvalid, i, op.TurnID)
		}
		if projection.Turns[at].TurnGeneration != op.TurnGeneration {
			return fmt.Errorf("%w: op[%d] turn %s op generation %d != kernel %d",
				ErrTurnStateStale, i, op.TurnID, op.TurnGeneration, projection.Turns[at].TurnGeneration)
		}
		mutations = append(mutations, turnMutation{index: at, state: op.DetailLoadState, reason: op.ReasonCode})
	}
	// loading/loaded unconditionally CLEAR the stored reasonCode (not
	// "absent field keeps the old value"); failed sets state+code atomically.
	for _, m := range mutations {
		projection.Turns[m.index].DetailLoadState = m.state
		projection.Turns[m.index].DetailReasonCode = m.reason
	}
	return nil
}

// EffectiveDetailLoadState maps the zero value to the wire-decode default.
func (t TurnProjection) EffectiveDetailLoadState() string {
	if t.DetailLoadState == DetailStateNotRequested {
		return "notRequested"
	}
	return t.DetailLoadState
}
