package dshweb

// Official dsh goal projection verbs (goals/pause|resume|clear|edit — Typert
// remotes on the GoalService host face 'goals'; goal/goal/src/index.ts
// @Remote('pause'|'resume'|'clear'|'edit')). CordCode only bridges: request
// shapes mirror the live-probed wire (gateway single `args` object with
// agentId + CAS ref {id, revision}; edit additionally carries
// request.objective). Two-generation live evidence: alpha.1
// (alpha1-commands-goals-wire.json, 2026-09-23 — shapes accepted, business
// "no current goal" on a goal-less session) and alpha.2
// (alpha2-commands-goals-wire.json, 2026-09-23 — full success path with state
// convergence). DRIFT note: the alpha.2 TypeScript source renames the param
// `agent`, but the gateway wire name stays `agentId` on BOTH generations
// (`{agent}` is rejected verbatim with `missing "agentId"; unexpected "agent"`
// — alpha.2 live sample); the source param name never reaches the wire.
//
// The CAS ref is fetched fresh from session.list projections.values.goal
// (authoritative whole snapshot) right before each mutation — never from the
// live codec state, which may lag or have been reset. Goal creation is
// intentionally NOT here: the official flow creates goals via the /goal
// command (commands/execute), not a goal verb.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	goalsPauseMethod  = "goals/pause"
	goalsResumeMethod = "goals/resume"
	goalsClearMethod  = "goals/clear"
	goalsEditMethod   = "goals/edit"
)

type goalsMutateArgs struct {
	AgentID string        `json:"agentId"`
	Ref     goalsRefWire  `json:"ref"`
	Request *goalsEditReq `json:"request,omitempty"`
}

type goalsRefWire struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

type goalsEditReq struct {
	Objective string `json:"objective"`
}

// goalsFoldedWire mirrors the official goal projection whole value
// (dsh-goal fold.ts FoldedGoal: {goal?, roundsStarted, createdAt?…}); only the
// current snapshot is needed to build the CAS ref.
type goalsFoldedWire struct {
	Goal *core.GoalEvent `json:"goal"`
}

var _ core.SessionGoalController = (*Agent)(nil)

// MutateSessionGoal runs one official goal verb host-side. action ∈
// pause|resume|clear|edit (edit requires a non-empty objective). Mutations are
// whole-snapshot host actions — the matching goal/change event updates every
// projection subscriber, so the return carries no echo, only errors.
func (a *Agent) MutateSessionGoal(ctx context.Context, sessionID, action, objective string) error {
	if sessionID == "" {
		return fmt.Errorf("dsh-web: mutate session goal: empty session id")
	}
	var method string
	var request *goalsEditReq
	switch action {
	case core.SessionGoalActionPause:
		method = goalsPauseMethod
	case core.SessionGoalActionResume:
		method = goalsResumeMethod
	case core.SessionGoalActionClear:
		method = goalsClearMethod
	case core.SessionGoalActionEdit:
		method = goalsEditMethod
		objective = strings.TrimSpace(objective)
		if objective == "" {
			return fmt.Errorf("dsh-web: mutate session goal: edit requires an objective")
		}
		request = &goalsEditReq{Objective: objective}
	default:
		return fmt.Errorf("dsh-web: mutate session goal: unknown action %q", action)
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	ref, err := a.currentGoalRef(ctx, client, sessionID)
	if err != nil {
		return err
	}
	return client.Call(ctx, method, goalsMutateArgs{AgentID: sessionID, Ref: ref, Request: request}, nil)
}

// currentGoalRef fetches the authoritative goal snapshot (session.list
// projections.values.goal) and returns its CAS ref. A missing projection
// block, absent goal unit, or null goal is "no goal" — every verb is
// meaningless without one (official GOAL_NOT_FOUND).
func (a *Agent) currentGoalRef(ctx context.Context, client *Client, sessionID string) (goalsRefWire, error) {
	var val sessionListValue
	if err := client.Call(ctx, "session/list", listArgs(), &val); err != nil {
		return goalsRefWire{}, err
	}
	for _, item := range val.Items {
		if item.SessionID != sessionID || item.Projections == nil {
			continue
		}
		raw, ok := item.Projections.Values["goal"]
		if !ok || len(raw) == 0 {
			break
		}
		var folded goalsFoldedWire
		if err := json.Unmarshal(raw, &folded); err != nil || folded.Goal == nil {
			break
		}
		if folded.Goal.ID == "" || folded.Goal.Revision <= 0 {
			break
		}
		return goalsRefWire{ID: folded.Goal.ID, Revision: folded.Goal.Revision}, nil
	}
	return goalsRefWire{}, fmt.Errorf("dsh-web: mutate session goal: session has no current goal")
}
