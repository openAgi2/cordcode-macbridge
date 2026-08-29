package codexremote

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor keeps Remote-specific static capability claims empty. The
// transport now delivers live turn/item events and history, but no target
// Remote payload has yet frozen model mutation or server-request interaction
// shapes, so those capabilities remain fail-closed in bridge derivation.
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
