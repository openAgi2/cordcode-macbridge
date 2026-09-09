package core

import "context"

// SessionCommand is one host command from a backend's per-session command
// catalog. It is the mapped form of the official command descriptor (dsh-web
// commands/list): Hint comes from the official nested input.hint — commands
// without an input key (compact/export) carry an empty Hint. The official
// input.images flag is intentionally dropped in phase 1 (no image-command UI).
type SessionCommand struct {
	Name        string // command name without the leading '/', e.g. "plan"
	Description string
	Hint        string // mapped from official input.hint; "" when the command has no input
}

// SessionCommandResult is the official settle of one host-command execution
// (dsh-web commands/execute). The official registry normalizes result.kind to
// exactly "success" | "error" and lets commands carry human-readable result
// text ("Plan mode on. …", "No compactable history yet.", the /goal usage …).
// Bridging that text onward is what makes a successful execution visible on
// clients — without it a green RPC is indistinguishable from "nothing
// happened" (owner 2026-09-05 18:2x report: taps executed fine, phone showed
// no reaction).
type SessionCommandResult struct {
	CommandID  string // lifecycle identity: official commandId when exposed, otherwise bridge projection identity
	ResultKind string // official "success" (errors return as Go error instead)
	ResultText string // official result.text; "" when the command settles silently
}

// SessionCommandCatalog is an optional agent interface for backends that
// expose the official per-session host-command catalog (dsh-web commands/list
// + commands/execute). Executing a command is a host-side action — it must
// never be routed through SendMessage (a slash line sent as a user message is
// exactly the "model replies to /plan" bug this interface exists to avoid).
// Line must be the complete official slash line (leading '/', single line).
// This is deliberately NOT the legacy zero-consumer CommandProvider.
type SessionCommandCatalog interface {
	ListSessionCommands(ctx context.Context, sessionID string) ([]SessionCommand, error)
	ExecuteSessionCommand(ctx context.Context, sessionID, line string) (SessionCommandResult, error)
}

// SessionCommandReadiness is an optional gate for backends whose command
// surface depends on evidence gates beyond mere code presence (grok-build:
// P6 feedback-type evidence + turn dispatcher). When an agent implements this
// interface, session_commands is advertised only while SessionCommandsReady
// reports true — a type assertion alone must never advertise an unproven
// surface (grok-build 方案 §5.1: 能力不能仅靠 Go 类型断言).
type SessionCommandReadiness interface {
	SessionCommandsReady() bool
}

// SessionCommandsAdvertise reports whether the agent's command surface may be
// advertised: implements the catalog AND (when it opts into the readiness
// gate) reports ready. Backends without the readiness interface (dsh-web)
// keep the pure type-assertion behavior.
func SessionCommandsAdvertise(agent Agent) bool {
	if _, ok := agent.(SessionCommandCatalog); !ok {
		return false
	}
	if r, ok := agent.(SessionCommandReadiness); ok {
		return r.SessionCommandsReady()
	}
	return true
}
