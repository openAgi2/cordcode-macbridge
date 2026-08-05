package opencode

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor (§6.2 provider 零跨层抽象): opencode self-describes its static wire
// attributes. opencode exposes a service-level SSE event stream that fans out
// external-turn events (LiveEventBroadcast), and clients reconcile on event, so
// external-turn polling stays enabled as a watchdog.
//
// StaticCapabilities carries the todos 兜底: opencode does NOT implement TodoProvider,
// yet it must advertise todos so iOS renders the todo surface. The pre-§6.2 code
// handled this with `id=="opencode"` in backend_capabilities.go; that id-half migrates
// here as a self-described static capability. opencode does NOT receive
// external_turn_streaming (it is already SSE push-native; MacBridge does not
// file-relay it) and does NOT resolve questions over the bridge (no question_reply).
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        "opencode",
		DisplayName:                 "OpenCode",
		LiveEventModel:              core.LiveEventBroadcast,
		RequiresExternalTurnPolling: true,
		StaticCapabilities: []string{
			"todos",
		},
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)
