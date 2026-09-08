package grokbuild

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type goalMutationWriteRecorder struct {
	writes chan []byte
}

func (w *goalMutationWriteRecorder) Write(p []byte) (int, error) {
	copyOfP := append([]byte(nil), p...)
	w.writes <- copyOfP
	return len(p), nil
}

func (*goalMutationWriteRecorder) Close() error { return nil }

func newGoalMutationTestSession(t *testing.T) (*Agent, *grokSession, *goalMutationWriteRecorder) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := &goalMutationWriteRecorder{writes: make(chan []byte, 4)}
	s := &grokSession{
		stdin: w, events: make(chan core.Event, 8), ctx: ctx, cancel: cancel,
		updateState: newGrokUpdateState(), pendingQuestions: make(map[string]*pendingAskUserQuestion),
		pendingPerms: make(map[string][]permissionOption),
	}
	s.alive.Store(true)
	s.sessionID.Store("sess-1")
	s.updateState.observeGoal(core.GoalEvent{ID: "goal-1", Objective: "ship it", Phase: "active"})
	a := &Agent{}
	a.registerLiveSession("sess-1", s)
	t.Cleanup(func() {
		s.turn.settleActive(turnOutcome{Err: errTurnActorDead})
		a.unregisterLiveSession("sess-1", s)
	})
	return a, s, w
}

func readGoalMutationWrite(t *testing.T, w *goalMutationWriteRecorder) string {
	t.Helper()
	select {
	case raw := <-w.writes:
		return string(raw)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Grok goal control write")
		return ""
	}
}

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

func TestGrokPauseCancelsActiveGoalAndWaitsForAuthoritativePause(t *testing.T) {
	a, s, writes := newGoalMutationTestSession(t)
	if _, _, err := s.turn.promote(7); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- a.MutateSessionGoal(context.Background(), "sess-1", "pause", "") }()
	if wire := readGoalMutationWrite(t, writes); !strings.Contains(wire, `"method":"session/cancel"`) {
		t.Fatalf("pause wire = %s, want native session/cancel", wire)
	}
	if !s.turn.settleForRequest(7, turnOutcome{StopReason: stopReasonCancelled}) {
		t.Fatal("active goal turn did not settle")
	}
	s.updateState.observeGoal(core.GoalEvent{ID: "goal-1", Objective: "ship it", Phase: "paused"})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("pause failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pause did not acknowledge the authoritative paused state")
	}
	select {
	case extra := <-writes.writes:
		t.Fatalf("pause must not enqueue a busy /goal turn: %s", extra)
	default:
	}
}

func TestGrokClearStopsActiveGoalThenDispatchesOfficialClear(t *testing.T) {
	a, s, writes := newGoalMutationTestSession(t)
	if _, _, err := s.turn.promote(9); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- a.MutateSessionGoal(context.Background(), "sess-1", "clear", "") }()
	if wire := readGoalMutationWrite(t, writes); !strings.Contains(wire, `"method":"session/cancel"`) {
		t.Fatalf("clear first wire = %s, want native session/cancel", wire)
	}
	if !s.turn.settleForRequest(9, turnOutcome{StopReason: stopReasonCancelled}) {
		t.Fatal("active goal turn did not settle")
	}
	select {
	case premature := <-writes.writes:
		t.Fatalf("clear dispatched before authoritative pause: %s", premature)
	default:
	}
	s.updateState.observeGoal(core.GoalEvent{ID: "goal-1", Objective: "ship it", Phase: "paused"})

	clearWire := readGoalMutationWrite(t, writes)
	var req struct {
		Method string `json:"method"`
		Params struct {
			Prompt []contentBlock `json:"prompt"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(clearWire), &req); err != nil {
		t.Fatalf("decode clear wire: %v (%s)", err, clearWire)
	}
	if req.Method != "session/prompt" || len(req.Params.Prompt) != 1 || req.Params.Prompt[0].Text != "/goal clear" {
		t.Fatalf("clear second wire = %s, want official /goal clear prompt", clearWire)
	}
	s.updateState.observeGoal(core.GoalEvent{ID: "", Objective: "", Phase: "none"})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("clear failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("clear did not acknowledge the authoritative cleared state")
	}
}
