package grokbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func runColdLeaderGoalClear(t *testing.T, publishClear bool) (error, []core.Event, map[string]any) {
	t.Helper()
	sock := filepath.Join("/tmp", fmt.Sprintf("cc-grok-goal-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	release := make(chan struct{})
	serverErr := make(chan error, 1)
	prompt := make(chan map[string]any, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()
		if err := leaderHandshake(c); err != nil {
			serverErr <- err
			return
		}
		msg, err := readClientMsg(c)
		if err != nil {
			serverErr <- err
			return
		}
		var request map[string]any
		if msg.Type != "acp" || json.Unmarshal([]byte(msg.Payload), &request) != nil {
			serverErr <- fmt.Errorf("goal clear frame = %+v", msg)
			return
		}
		prompt <- request
		if publishClear {
			if err := writeACPNotification(c, "session/update", map[string]any{
				"sessionId": "sess-1",
				"update": map[string]any{
					"sessionUpdate": "goal_updated",
					"goal_id":       "",
					"objective":     "",
					"status":        "cleared",
					"phase":         "idle",
				},
			}); err != nil {
				serverErr <- err
				return
			}
		}
		if err := writeACPResponse(c, acpPayloadID(msg.Payload), promptResult{StopReason: stopReasonEndTurn}); err != nil {
			serverErr <- err
			return
		}
		<-release
		serverErr <- nil
	}()

	var events []core.Event
	var eventsMu sync.Mutex
	sub := NewLeaderSubscriber(sock, "sess-1", "/tmp")
	runCtx, cancelRun := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRun()
	runDone := make(chan error, 1)
	go func() {
		runDone <- sub.Run(runCtx, func(ev core.Event) {
			eventsMu.Lock()
			events = append(events, ev)
			eventsMu.Unlock()
		})
	}()
	select {
	case <-sub.ready:
	case <-time.After(time.Second):
		t.Fatal("leader subscriber did not become ready")
	}

	a := &Agent{liveSubs: map[string]*LeaderSubscriber{"sess-1": sub}}
	mutationTimeout := time.Second
	if !publishClear {
		mutationTimeout = 100 * time.Millisecond
	}
	mutationCtx, cancelMutation := context.WithTimeout(context.Background(), mutationTimeout)
	mutationErr := a.MutateSessionGoal(mutationCtx, "sess-1", "clear", "")
	cancelMutation()
	request := <-prompt
	close(release)
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("leader subscriber did not stop")
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	return mutationErr, append([]core.Event(nil), events...), request
}

func TestGrokColdClearUsesLoadedLeaderConnection(t *testing.T) {
	err, events, request := runColdLeaderGoalClear(t, true)
	if err != nil {
		t.Fatalf("cold clear: %v", err)
	}
	if request["method"] != "session/prompt" || int(request["id"].(float64)) <= 2 {
		t.Fatalf("cold clear request = %+v", request)
	}
	params, ok := request["params"].(map[string]any)
	if !ok || params["sessionId"] != "sess-1" {
		t.Fatalf("cold clear params = %+v", params)
	}
	prompt, ok := params["prompt"].([]any)
	if !ok || len(prompt) != 1 || prompt[0].(map[string]any)["text"] != "/goal clear" {
		t.Fatalf("cold clear prompt = %+v", params["prompt"])
	}
	if len(events) != 1 || events[0].Type != core.EventSessionGoal || events[0].Goal != nil {
		t.Fatalf("cold clear events = %+v", events)
	}
}

func TestGrokColdClearRejectsPromptTerminalWithoutGoalClear(t *testing.T) {
	err, _, _ := runColdLeaderGoalClear(t, false)
	if err == nil || !strings.Contains(err.Error(), "without an authoritative cleared update") {
		t.Fatalf("cold clear error = %v", err)
	}
}
