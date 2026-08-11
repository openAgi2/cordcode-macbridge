package opencode

import "github.com/openAgi2/cordcode-macbridge/core"

// CheckpointProvider opt-in (§6.1 read-only checkpoint diff). OpenCode sessions run
// in a workspace directory that MacBridge may snapshot into hidden git refs after
// each completed turn. The snapshot is a workspace FILE snapshot only — NOT a session
// truth source; session truth stays in the official OpenCode server (plan §3 防呆,
// SSV2 guardrail 1).
//
// Per-session workspace resolution lives in go-bridge (sessionRegistry.directoryForSession);
// this driver only declares the opt-in. Capture no-ops honestly (workspace_not_git)
// when the resolved workspace is not a git repo — no mock/placeholder snapshot.
func (a *Agent) SupportsCheckpoint() bool { return true }

var _ core.CheckpointProvider = (*Agent)(nil)
