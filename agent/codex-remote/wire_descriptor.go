package codexremote

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor self-describes the Remote backend (§6.2 A-class). Live turn/item
// events and history ride the transport; dynamic interface capabilities (including
// the official model/list adapter) are derived by the bridge; unsupported
// server-request interactions remain fail-closed.
//
// StaticCapabilities declares turn_detail_lazy_v1 (unified-bridge-protocol §11.7):
// paginated remote sessions expose session_turn_items, so the iOS "加载详细过程"
// entry is gated on THIS descriptor claim — not on a global echo. The hello-time
// echo (negotiateTurnDetailLazyV1) still requires the client to declare the
// capability with its session_sync_v2 prerequisite.
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        WireKind,
		DisplayName:                 DisplayName,
		LiveEventModel:              core.LiveEventBroadcast,
		RequiresExternalTurnPolling: false,
		StaticCapabilities:          []string{"turn_detail_lazy_v1"},
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)
