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
	return s.executeGoalMutation(ctx, line)
}

func (s *grokSession) executeGoalMutation(ctx context.Context, line string) error {
	wait, err := s.dispatchTurn([]contentBlock{{Type: "text", Text: line}})
	if err != nil {
		return err
	}
	updated := s.turn.goalUpdatedSignal()
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
