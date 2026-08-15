package grokbuild

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor (§6.2 provider 零跨层抽象): grokbuild self-describes its static wire
// attributes. grok agent stdio is a stdin/stdout pipe (LiveEventSessionProcess, same
// process model as claudecode), so external-turn polling is required until the
// leader-socket subscriber ships push deltas.
//
// StaticCapabilities is empty: grokbuild has no A-class static positive capabilities
// today (no content_chunking, no question_reply, no external_turn_streaming). Leaving
// the slice nil rather than placeholder is the honest self-description; adding a future
// capability means appending to this slice, not adding an id branch in wire.
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        "grokbuild", // 不转 snake_case，与 iOS fromWireKind 的 case "grokbuild" 对应
		DisplayName:                 "Grok Build",
		LiveEventModel:              core.LiveEventSessionProcess,
		RequiresExternalTurnPolling: true,
		StaticCapabilities:          nil,
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)

// SupportedAttachmentKinds (§3.9 truth source): file paths are appended to
// the prompt text, but the ACP image block cannot carry bytes/MIME/URI and
// Grok CLI freezes promptCapabilities.image=false — image is NOT declared
// (declaring it would be a false capability).
func (a *Agent) SupportedAttachmentKinds() []string { return []string{"file"} }

var _ core.AttachmentSupporter = (*Agent)(nil)
