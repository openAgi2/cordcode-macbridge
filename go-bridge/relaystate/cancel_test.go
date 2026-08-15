package relaystate

import "testing"

// table-driven：每个 (state, writerCommitted) -> (outcome, newState) 唯一确定。
func TestTryCancel_TableDriven(t *testing.T) {
	cases := []struct {
		name            string
		state           CancelState
		writerCommitted bool
		wantOutcome     CancelOutcome
		wantState       CancelState
	}{
		{"queued -> cancelled", StateQueued, false, CancelCancelled, StateCancelled},
		{"reading -> cancelled", StateReading, false, CancelCancelled, StateCancelled},
		{"serializing -> cancelled", StateSerializing, false, CancelCancelled, StateCancelled},
		// outboundQueued 是唯一受 writer 原子撤回裁决的边界
		{"outboundQueued + notCommitted -> cancelled", StateOutboundQueued, false, CancelCancelled, StateCancelled},
		{"outboundQueued + committed -> too_late (state 不变)", StateOutboundQueued, true, CancelTooLate, StateOutboundQueued},
		// committedToWriter 之后一律 too_late
		{"committedToWriter -> too_late", StateCommittedToWriter, false, CancelTooLate, StateCommittedToWriter},
		{"complete -> too_late", StateComplete, false, CancelTooLate, StateComplete},
		// cancelled 幂等
		{"cancelled -> idempotent cancelled", StateCancelled, false, CancelCancelled, StateCancelled},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			outcome, newState := TryCancel(tc.state, tc.writerCommitted)
			if outcome != tc.wantOutcome {
				t.Errorf("outcome=%s want %s", outcome, tc.wantOutcome)
			}
			if newState != tc.wantState {
				t.Errorf("newState=%s want %s", newState, tc.wantState)
			}
		})
	}
}

func TestIsTooLateBoundary(t *testing.T) {
	tooLate := []CancelState{StateCommittedToWriter, StateComplete}
	notTooLate := []CancelState{StateQueued, StateReading, StateSerializing, StateOutboundQueued}
	for _, s := range tooLate {
		if !IsTooLateBoundary(s) {
			t.Errorf("%s must be too-late boundary", s)
		}
	}
	for _, s := range notTooLate {
		if IsTooLateBoundary(s) {
			t.Errorf("%s must NOT be too-late boundary", s)
		}
	}
}

func TestAdvance_OnlyForward(t *testing.T) {
	// 合法向前序列
	seq := []CancelState{StateQueued, StateReading, StateSerializing, StateOutboundQueued, StateCommittedToWriter, StateComplete}
	for i := 0; i < len(seq)-1; i++ {
		if !Advance(seq[i], seq[i+1]) {
			t.Errorf("Advance(%s,%s) should be allowed", seq[i], seq[i+1])
		}
	}
	// 非法：跳步 / 回退
	if Advance(StateQueued, StateSerializing) {
		t.Error("skip-step queued->serializing must be illegal")
	}
	if Advance(StateCommittedToWriter, StateReading) {
		t.Error("backward transition must be illegal")
	}
	if Advance(StateComplete, StateQueued) {
		t.Error("complete -> anything must be illegal")
	}
}
