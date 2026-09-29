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

// SupportedAttachmentKinds (§3.9 mode-aware truth source): files are declared
// in both modes. Images are declared ONLY in CLI mode: the CLI path forwards
// staged image paths via --file, while the managed-server path drops staged
// image paths before building the HTTP body (server_session.go:103 discards
// them) — a server-mode image would be silently lost, so declaring it there
// would be a false capability. httpBaseURL is read under a.mu like every
// other mode consumer.
func (a *Agent) SupportedAttachmentKinds() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.httpBaseURL != "" {
		return []string{"file"}
	}
	return []string{"file", "image"}
}

var _ core.AttachmentSupporter = (*Agent)(nil)
