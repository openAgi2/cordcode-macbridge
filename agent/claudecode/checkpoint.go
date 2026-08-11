package claudecode

import "github.com/openAgi2/cordcode-macbridge/core"

// CheckpointProvider opt-in (§6.1 read-only checkpoint diff). Claude Code sessions
// run in a workspace directory that MacBridge may snapshot into hidden git refs
// after each completed turn. The snapshot is a workspace FILE snapshot only — it is
// NOT a session truth source; session truth always stays in the official `claude`
// CLI (plan §3 防呆, SSV2 guardrail 1).
//
// Per-session workspace resolution lives in go-bridge (sessionRegistry.directoryForSession,
// populated when create_session/send_message carry a directory); this driver only
// declares the opt-in. Capture no-ops honestly (workspace_not_git, no ref, no event)
// when the resolved workspace is not a git repo, and when a purely external Claude
// session (started in another Terminal, observed only via file-relay) has no tracked
// directory — no mock/placeholder snapshot is ever written.
func (a *Agent) SupportsCheckpoint() bool { return true }

var _ core.CheckpointProvider = (*Agent)(nil)
