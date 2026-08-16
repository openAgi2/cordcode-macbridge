package dshweb

import (
	"context"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// WireDescriptor (§6.2 self-description): dsh-web talks to the official dsh
// web instance whose mux stream is an AGENT-LEVEL BROADCAST covering every
// session — including turns the user starts on the Mac web UI — so external
// turns stream live and no external-turn polling is required.
//
// StaticCapabilities is the honest positive set:
//   - external_turn_streaming: the mux stream pushes external turns live
//     (对齐 claudecode 外部旁观, design §4.3.3);
//   - question_reply: ask batches surface per-question and resolve through
//     question_reply/question_reject (design §4.3.4).
//
// Attachment kinds are deliberately NOT declared: phase 1 is text-only
// (official session.attachment lands in phase 2) — a declared kind is a
// semantic claim, and AttachmentSupporter stays unimplemented so the bridge's
// attachment gate rejects image/file uploads pre-StartSession.
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        WireKind, // "deepseek-web" — iOS BackendKind.deepSeekWeb
		DisplayName:                 "DeepSeek Web",
		LiveEventModel:              core.LiveEventBroadcast,
		RequiresExternalTurnPolling: false,
		StaticCapabilities:          []string{"external_turn_streaming", "question_reply"},
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)

// ToolAuthorizer (grokbuild precedent): the bridge derives the
// permission_resolve capability from this interface — dsh-web resolves
// runtime approvals through /api/respond (§4.3.4), so the iOS permission
// actions must light up. The allowed-tools list itself is recorded and
// returned verbatim; dsh v1 has no pre-authorization surface to push it to.
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

// GetSessionHistory implements the legacy HistoryProvider surface over the
// same session.history source (rich entries folded to plain turns), so the
// session_history capability and any legacy consumer keep working.
func (a *Agent) GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	rich, err := a.GetRichSessionHistory(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.HistoryEntry, 0, len(rich))
	for _, e := range rich {
		out = append(out, core.HistoryEntry{
			Role:      e.Role,
			Content:   e.Content,
			Timestamp: e.Timestamp,
		})
	}
	return out, nil
}

var _ core.HistoryProvider = (*Agent)(nil)
