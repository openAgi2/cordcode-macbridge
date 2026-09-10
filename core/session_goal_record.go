package core

import "context"

// SessionGoalRecord is the faithful Codex thread-goal record. It intentionally
// does not reuse the dsh GoalEvent id/revision/phase vocabulary.
type SessionGoalRecord struct {
	ThreadID        string `json:"threadId"`
	Objective       string `json:"objective"`
	Status          string `json:"status"` // active | paused | blocked | usageLimited | budgetLimited | complete
	TokenBudget     *int64 `json:"tokenBudget"`
	TokensUsed      int64  `json:"tokensUsed"`
	TimeUsedSeconds int64  `json:"timeUsedSeconds"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

// SessionGoalSnapshot represents an authoritative get/response/notification.
// Goal == nil means the backend authoritatively reports no goal.
type SessionGoalSnapshot struct {
	Goal *SessionGoalRecord `json:"goal"`
}

// SessionGoalUpdate mirrors thread/goal/set partial-update semantics. Nil
// Objective/Status preserve the corresponding server value. TokenBudgetSet
// distinguishes omission (preserve) from explicit null (clear).
type SessionGoalUpdate struct {
	Objective      *string
	Status         *string
	TokenBudgetSet bool
	TokenBudget    *int64
}

// SessionGoalRecordController is the typed Codex goal surface. Set returns the
// official full response goal; Clear returns the official cleared boolean.
type SessionGoalRecordController interface {
	GetSessionGoal(ctx context.Context, sessionID string) (SessionGoalSnapshot, error)
	SetSessionGoal(ctx context.Context, sessionID string, update SessionGoalUpdate) (SessionGoalRecord, error)
	ClearSessionGoal(ctx context.Context, sessionID string) (bool, error)
}
