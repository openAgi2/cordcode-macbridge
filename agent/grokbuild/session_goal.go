package grokbuild

import (
	"context"
	"fmt"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.SessionGoalController = (*Agent)(nil)

func grokGoalActionLine(action, objective string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case core.SessionGoalActionPause:
		return "/goal pause", nil
	case core.SessionGoalActionResume:
		return "/goal resume", nil
	case core.SessionGoalActionClear:
		return "/goal clear", nil
	case core.SessionGoalActionEdit:
		objective = strings.TrimSpace(objective)
		if objective == "" {
			return "", fmt.Errorf("grokbuild: goal edit requires objective")
		}
		return "/goal edit " + objective, nil
	default:
		return "", fmt.Errorf("grokbuild: unsupported goal action %q", action)
	}
}

// MutateSessionGoal routes the shared goal banner actions through Grok's own
// official /goal host command. The goal projection remains authoritative; no
// local phase is fabricated while the command settles.
func (a *Agent) MutateSessionGoal(ctx context.Context, sessionID, action, objective string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("grokbuild: goal action requires sessionId")
	}
	line, err := grokGoalActionLine(action, objective)
	if err != nil {
		return err
	}
	s, ok := a.liveSessionForCommand(sessionID)
	if !ok {
		return fmt.Errorf("grokbuild: goal action: no live session actor")
	}
	return s.executeGoalMutation(ctx, strings.ToLower(strings.TrimSpace(action)), line)
}

// cancelGoalTurnAndWait uses Grok's native session/cancel control rail, then
// waits for both the operation lease and the authoritative goal state. Slash
// commands cannot be dispatched into the actor while its long goal turn owns
// that lease; attempting to do so used to return errTurnBusy and made the
// GoalBar pause/clear controls appear inert.
func (s *grokSession) cancelGoalTurnAndWait(ctx context.Context, requireInactiveGoal bool) error {
	settled, active := s.turn.activeSettledSignal()
	if !active {
		return nil
	}
	if s.updateState == nil {
		return fmt.Errorf("grokbuild: goal action: goal state unavailable")
	}
	goalChanged := s.updateState.goalUpdateSignal()
	if err := s.CancelTurn(ctx); err != nil {
		return err
	}
	settledDone := false
	for {
		phase := s.updateState.goalPhase()
		goalInactive := !requireInactiveGoal || (phase != "" && phase != "active")
		if settledDone && goalInactive {
			return nil
		}
		select {
		case <-settled:
			settledDone = true
			settled = nil
		case <-goalChanged:
			goalChanged = s.updateState.goalUpdateSignal()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *grokSession) executeGoalMutation(ctx context.Context, action, line string) error {
	if _, active := s.turn.activeSettledSignal(); active {
		switch action {
		case core.SessionGoalActionPause:
			// Cancelling an active Grok goal is its official pause path; the
			// resulting goal_paused update is the acknowledgement.
			return s.cancelGoalTurnAndWait(ctx, true)
		case core.SessionGoalActionClear:
			// Clear is a stop-and-remove operation. First release the active goal
			// turn and observe its automatic pause, then execute /goal clear on
			// the same actor.
			if err := s.cancelGoalTurnAndWait(ctx, true); err != nil {
				return err
			}
		}
	}
	if s.updateState == nil {
		return fmt.Errorf("grokbuild: goal action: goal state unavailable")
	}
	updated := s.updateState.goalUpdateSignal()
	wait, err := s.dispatchTurn([]contentBlock{{Type: "text", Text: line}})
	if err != nil {
		return err
	}
	select {
	case <-updated:
		// Resume can re-enter a long-running goal loop. The durable goal update
		// acknowledges the mutation; keep the dispatcher lease so the actor
		// continues owning the eventual turn terminal.
		return nil
	case out := <-wait:
		if out.Err != nil {
			return out.Err
		}
		switch out.StopReason {
		case stopReasonEndTurn:
			return nil
		case stopReasonCancelled:
			return fmt.Errorf("grokbuild: goal action cancelled")
		default:
			return fmt.Errorf("grokbuild: goal action ended with %q", out.StopReason)
		}
	case <-ctx.Done():
		_ = s.CancelTurn(ctx)
		return ctx.Err()
	}
}
