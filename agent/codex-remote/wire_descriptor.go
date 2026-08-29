package codexremote

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor keeps Remote-specific static capability claims empty. The
// transport delivers live turn/item events and history. Dynamic interface
// capabilities (including the official model/list adapter) are derived by the
// bridge; unsupported server-request interactions remain fail-closed.
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
