package claudecode

import (
	"context"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

// SessionPinner receivers for the Claude Code driver.
//
// Claude session IDs are the transcript jsonl filenames under ~/.claude/projects/<key>/
// and are globally unique (timestamp-based UUIDs), so the pinstore key uses an empty
// scope — there is no cross-project collision risk within the claudecode backend.
// Pin state is persisted in the bridge-owned pinstore (NOT the .cc-connect-session-meta
// sidecar) so ListPinnedSessions can enumerate without scanning every project dir.
// Summary fields (title/messageCount/modifiedAt) are resolved by the go-bridge handler
// from the Claude session catalog; the store holds identity + pinnedAt only.

const claudePinBackendID = "claudecode"
const claudePinScope = "" // Claude session IDs are globally unique

// SetSessionPinned implements core.SessionPinner.
func (a *Agent) SetSessionPinned(_ context.Context, sessionID, directory string, pinned bool, pinnedAt time.Time) (*core.SessionPin, error) {
	if a.pinStore == nil {
		return nil, pinstore.ErrStoreUnavailable
	}
	if pinned && pinnedAt.IsZero() {
		pinnedAt = time.Now().UTC()
	}
	if err := a.pinStore.SetPinned(claudePinBackendID, claudePinScope, sessionID, directory, pinned, pinnedAt); err != nil {
		return nil, err
	}
	if !pinned {
		return nil, nil
	}
	return &core.SessionPin{
		BackendID: claudePinBackendID,
		SessionID: sessionID,
		Directory: directory,
		PinnedAt:  pinnedAt.UTC(),
	}, nil
}

// ListPinnedSessions implements core.SessionPinner. Returns identity-only pins for the
// go-bridge handler to enrich from the Claude session catalog.
func (a *Agent) ListPinnedSessions(_ context.Context) ([]core.SessionPin, error) {
	if a.pinStore == nil {
		return nil, pinstore.ErrStoreUnavailable
	}
	entries := a.pinStore.List(claudePinBackendID)
	out := make([]core.SessionPin, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ToPin())
	}
	return out, nil
}

// cleanupPin is invoked by DeleteSession to drop pin metadata when a Claude session is
// deleted. directory/scope is not known at delete time, so RemoveAll sweeps by session ID.
func (a *Agent) cleanupPin(sessionID string) {
	if a.pinStore == nil || sessionID == "" {
		return
	}
	a.pinStore.RemoveAll(claudePinBackendID, sessionID)
}

var _ core.SessionPinner = (*Agent)(nil)
