package claudecode

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor (§6.2 provider 零跨层抽象): claudecode self-describes its static wire
// attributes instead of go-bridge branching on backend id. claudecode runs as a
// stdin/stdout pipe (LiveEventSessionProcess), requires external-turn polling, and
// carries the A-class static capabilities that were previously id-keyed in
// backend_capabilities.go:
//   - content_chunking: claude transcript deltas are chunked for transport.
//   - question_reply: claude resolves AskUserQuestion via the verified control_response
//     path (RespondQuestion/RejectQuestion in session.go).
//   - external_turn_streaming: MacBridge file-relays transcript growth as push deltas.
//
// Note on id drift: the production backend id is "claude" (alias of factory name
// "claudecode"); the pre-§6.2 id checks `id=="claudecode"` therefore never matched the
// production descriptor, so claude historically did NOT advertise content_chunking /
// question_reply. Self-description restores the intended capabilities regardless of the
// id spelling under which the driver is registered. See docs/§6.2 + CHANGELOG.
//
// Mode-conditional capabilities (codex app_server) and interface-gated capabilities do
// NOT belong here; only static attributes live in the descriptor.
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        "claude_code",
		DisplayName:                 "Claude Code",
		LiveEventModel:              core.LiveEventSessionProcess,
		RequiresExternalTurnPolling: true,
		StaticCapabilities: []string{
			"content_chunking",
			"question_reply",
			"external_turn_streaming",
		},
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)
