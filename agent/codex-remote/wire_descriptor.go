package codexremote

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor self-describes the Remote backend (§6.2 A-class). Live turn/item
// events and history ride the transport; dynamic interface capabilities (including
// the official model/list adapter) are derived by the bridge; unsupported
// server-request interactions remain fail-closed.
//
// StaticCapabilities declares turn_detail_lazy_v1 (unified-bridge-protocol §11.7):
// paginated remote sessions expose session_turn_items, so the iOS "加载详细过程"
// entry is gated on THIS descriptor claim — not on a global echo. The declaration
// rides the SAME production gate as the hello echo
// (core.TurnDetailLazyProductionEnabled): flipping that one const withdraws both
// surfaces and iOS hides the entry before any request (true byte-identical
// rollback).
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	var staticCapabilities []string
	if core.TurnDetailLazyProductionEnabled {
		staticCapabilities = []string{"turn_detail_lazy_v1"}
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
