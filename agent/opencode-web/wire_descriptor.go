package opencodeweb

import (
	"github.com/openAgi2/cordcode-macbridge/core"
)

// WireDescriptor (design §4.1.4): opencode-web talks to the official
// `opencode serve` whose /global/event (or /api/event) SSE stream is a
// SERVER-LEVEL BROADCAST covering every session — including turns the user
// starts on the Mac web UI — so external turns stream live and no
// external-turn polling is required (unlike the legacy hybrid whose watchdog
// kept polling).
//
// StaticCapabilities is the honest positive set:
//   - external_turn_streaming: the SSE stream pushes external turns live.
//
// Not declared (design §4.1.3/§4.1.4): todos (interface unimplemented),
// question_reply / structured_user_input_v1 (question answering is ⛔ phase 1),
// attachment kinds (text-only phase 1 — declared kinds are semantic claims and
// AttachmentSupporter stays unimplemented so the bridge's attachment gate
// rejects image/file uploads instead of silently dropping them).
// permission_resolve is derived by the bridge from the ToolAuthorizer type
// assertion (lands with the approvals phase) — it must never be hand-written
// here without the implementation.
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        WireKind, // "opencode-web" — iOS BackendKind.openCodeWeb
		DisplayName:                 "OpenCode Web",
		LiveEventModel:              core.LiveEventBroadcast,
		RequiresExternalTurnPolling: false,
		StaticCapabilities:          []string{"external_turn_streaming"},
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)

// ToolAuthorizer lights up the bridge-derived permission_resolve capability:
// approvals surface through SSE permission.asked and resolve via the §3.4
// folding (permissions.go). The allowed-tools list itself is recorded and
// returned verbatim — the official API has no pre-authorization surface to
// push it to.
func (a *Agent) AddAllowedTools(tools ...string) error {
	a.mu.Lock()
	a.allowedTools = append(a.allowedTools, tools...)
	a.mu.Unlock()
	return nil
}

func (a *Agent) GetAllowedTools() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, len(a.allowedTools))
	copy(out, a.allowedTools)
	return out
}

var _ core.ToolAuthorizer = (*Agent)(nil)
