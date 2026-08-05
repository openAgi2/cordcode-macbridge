package codex

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor (§6.2 provider 零跨层抽象): codex self-describes its static wire
// attributes. The codex transcript relay emits authoritative turn_started /
// turn_completed events for both implicit and shared app-server sessions, so external
// turn polling is NOT required (polling mistakes transcript rewrites for new turns and
// can force an idle timeline back to its bottom). The base live-event model is
// session_process; the app_server runtime override to broadcast (when a shared
// app-server URL is configured) is applied in go-bridge on top of this static base —
// it is a mode-conditional (B-class) attribute, not a static one.
//
// StaticCapabilities carries only external_turn_streaming (MacBridge file-relays codex
// rollout growth as push deltas). codex app_server compression / question_reply /
// structured_user_input_v1 are mode-conditional and stay id-keyed in wire, guarded by
// TestCodexNonAppServerNoModeCaps.
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        "codex",
		DisplayName:                 "Codex",
		LiveEventModel:              core.LiveEventSessionProcess,
		RequiresExternalTurnPolling: false,
		StaticCapabilities: []string{
			"external_turn_streaming",
		},
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)
