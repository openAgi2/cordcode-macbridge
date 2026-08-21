package opencodeweb

import (
	"github.com/openAgi2/cordcode-macbridge/core"
)

// WireDescriptor (design §4.1.4): opencode-web talks to the official
// `opencode serve` whose /global/event SSE stream is a SERVER-LEVEL
// BROADCAST covering every session — including turns the user starts on the
// Mac web UI — so external turns stream live through the ONE backend-instance
// global subscriber (§6.5; E3 owning replay + single-connection tests green)
// and no external-turn polling exists.
//
// StaticCapabilities is the honest positive set:
//   - external_turn_streaming: the SSE stream pushes external turns live.
//
// Everything else is interface-derived by the bridge (negative-before-
// positive): todos (core.TodoProvider — §6.9), structured_user_input_v1
// (core.UserInputResponder + StructuredUserInputProvider — §6.8), session_
// mutation (SessionRenamer+SessionArchiver — §6.10), session_delete
// (SessionDeleter), permission_resolve (ToolAuthorizer → §3.4 folding).
// Legacy question_reply is deliberately NOT advertised — structured
// questions resolve exclusively through resolve_user_input (§6.8). E2
// reasoning and all OD-3 future surfaces stay unadvertised.
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        WireKind, // "opencode-web" — iOS BackendKind.openCodeWeb
		DisplayName:                 "OpenCode Web",
		LiveEventModel:              core.LiveEventBroadcast,
		RequiresExternalTurnPolling: false,
		StaticCapabilities:          []string{"external_turn_streaming"},
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)

// SupportedAttachmentKinds implements core.AttachmentSupporter: both image
// and file attachments ride the official prompt file part
// {type:"file", mime, filename?, url:"data:<mime>;base64,…} (§6.4 verified
// transport shape; A9 persisted file/image parts). This positive declaration
// is what the bridge's attachment gate keys on.
func (a *Agent) SupportedAttachmentKinds() []string {
	return []string{"image", "file"}
}

var _ core.AttachmentSupporter = (*Agent)(nil)

// ToolAuthorizer lights up the bridge-derived permission_resolve capability:
// approvals surface through SSE permission.asked and resolve via the §3.4
// folding (permissions.go). The allowed-tools list itself is recorded and
// returned verbatim — the official API has no pre-authorization surface to
// push it to.
func (a *Agent) AddAllowedTools(tools ...string) error {
	a.mu.Lock()
	a.allowedTools = append(a.allowedTools, tools...)
	a.mu.Unlock()
	return nil
}

func (a *Agent) GetAllowedTools() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, len(a.allowedTools))
	copy(out, a.allowedTools)
	return out
}

var _ core.ToolAuthorizer = (*Agent)(nil)
