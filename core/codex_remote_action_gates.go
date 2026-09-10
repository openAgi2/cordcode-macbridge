package core

// Codex Remote native actions are advertised only after their matching live
// Remote Control evidence gates pass. Each capability flips independently:
// Compact and Goal are proven on Desktop 26.903.61454 / Codex 0.153.4. Plan
// is proven by the isolated patched runtime's live update -> restart ->
// thread/settings/get lifecycle; the signed 0.153.4 runtime keeps attachment
// compatibility but remains fail-closed until a complete settings notification.
const (
	CodexRemoteContextCompactionProductionEnabled = true
	CodexRemoteCollaborationModeProductionEnabled = true
	CodexRemoteSessionGoalProductionEnabled       = true
)

// ContextCompactionReadinessProvider is the agent-level source of truth for
// advertising and accepting a native context-compaction action.
type ContextCompactionReadinessProvider interface {
	ContextCompactionReady() bool
}

// CollaborationModeReadinessProvider gates the native per-thread collaboration
// mode surface independently of generic permission or session modes.
type CollaborationModeReadinessProvider interface {
	CollaborationModeReady() bool
}

// SessionGoalReadinessProvider lets a goal-capable agent keep its public goal
// surface closed until the backend-specific live contract has been proven.
type SessionGoalReadinessProvider interface {
	SessionGoalReady() bool
}
