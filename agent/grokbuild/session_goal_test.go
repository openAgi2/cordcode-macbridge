package grokbuild

import (
	"context"
	"testing"
	"time"
)

func TestGrokGoalActionLineUsesOfficialHostCommands(t *testing.T) {
	tests := []struct {
		action, objective, want string
	}{
		{"pause", "ignored", "/goal pause"},
		{"resume", "", "/goal resume"},
		{"clear", "", "/goal clear"},
		{"edit", "  revised\nobjective  ", "/goal edit revised\nobjective"},
	}
	for _, tc := range tests {
		got, err := grokGoalActionLine(tc.action, tc.objective)
		if err != nil || got != tc.want {
			t.Fatalf("action %q: line=%q err=%v, want %q", tc.action, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ action, objective string }{{"edit", "  "}, {"teleport", ""}} {
		if _, err := grokGoalActionLine(tc.action, tc.objective); err == nil {
			t.Fatalf("action %q objective %q: expected validation error", tc.action, tc.objective)
		}
	}
}

func TestGrokResumeAcknowledgesGoalUpdateWithoutCancellingLongTurn(t *testing.T) {
	s, _, shut := newTurnTestSession(t, turnScriptPeer{
		goalObjective: "ship it", promptID: "p-resume", holdResponse: true,
	})
	defer shut()
	s.updateState = newGrokUpdateState()
	a := &Agent{}
	a.registerLiveSession("sess-1", s)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.MutateSessionGoal(ctx, "sess-1", "resume", ""); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if s.turn.activeReqID() == 0 {
		t.Fatal("resume acknowledgement released the goal turn lease")
	}
}
