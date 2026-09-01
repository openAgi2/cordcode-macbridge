package codexremote

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor self-describes the Remote backend (§6.2 A-class). Live turn/item
// events and history ride the transport; dynamic interface capabilities (including
// the official model/list adapter) are derived by the bridge; unsupported
// server-request interactions remain fail-closed.
//
// StaticCapabilities declares turn_detail_lazy_v1 (unified-bridge-protocol §11.7)
// and, once the phase5 client-first rollout completes,
// turn_detail_chunks_v1 (§11.8, owner final ruling 2026-08-30 — incremental
// layered loading). Paginated remote sessions expose session_turn_items, so
// the iOS "加载详细过程" entry gates on THIS descriptor claim — not on a
// global echo. Each declaration rides its own production const shared with
// the hello echo (core.TurnDetailLazyProductionEnabled /
// core.TurnDetailChunksProductionEnabled): flipping a const withdraws both of
// its surfaces at once. v1 stays listed (deprecated) during the v2
// transition.
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	var staticCapabilities []string
	if core.TurnDetailLazyProductionEnabled {
		staticCapabilities = []string{"turn_detail_lazy_v1"}
	}
	if core.TurnDetailChunksProductionEnabled {
		staticCapabilities = append(staticCapabilities, "turn_detail_chunks_v1")
	}
	return &core.WireDescriptor{
		Kind:                        WireKind,
		DisplayName:                 DisplayName,
		LiveEventModel:              core.LiveEventBroadcast,
		RequiresExternalTurnPolling: false,
		StaticCapabilities:          staticCapabilities,
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)
