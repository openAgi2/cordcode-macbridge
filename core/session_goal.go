package core

import "context"

// SessionGoalController is an optional agent interface for backends that
// expose the official goal projection verbs (dsh-web goals/pause|resume|
// clear|edit, CAS on ref revision). The banner actions mirror the official
// GoalBar: pause (active only), resume (paused/blocked), edit (objective),
// clear — each returns the seat's own error message verbatim when the
// transition is rejected (e.g. "cannot pause goal … from phase \"complete\"").
// Goal creation is NOT a verb here: the official flow creates goals through
// the /goal command (SessionCommandCatalog.ExecuteSessionCommand).
type SessionGoalController interface {
	MutateSessionGoal(ctx context.Context, sessionID, action, objective string) error
}

// Session goal mutation actions (official GoalBarActions verbs).
const (
	SessionGoalActionPause  = "pause"
	SessionGoalActionResume = "resume"
	SessionGoalActionEdit   = "edit"
	SessionGoalActionClear  = "clear"
)
