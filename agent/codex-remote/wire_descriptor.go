package codexremote

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor is honest for Phase 1 identity: the product transport is
// not up yet, so no live/interrupt/reconnect capability is advertised.
// Probe attempt-008 proved a controller *can* receive turn/item events after
// thread/resume; that is not this Agent's wired path until transport-rpc lands.
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        WireKind,
		DisplayName:                 DisplayName,
		LiveEventModel:              core.LiveEventBroadcast,
		RequiresExternalTurnPolling: false,
		StaticCapabilities:          nil,
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)
