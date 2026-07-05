package codex

import (
	"context"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

// SessionPinner receivers for the Codex driver.
//
// Codex sessions live as rollout jsonl files under $CODEX_HOME/sessions/ and are
// identified by session ID. The same session ID could in principle exist under different
// CODEX_HOME values, so the pinstore key is scoped by the effective codex home. Summary
// fields are resolved by the go-bridge handler from agent/codex/sessionListCache; the
// store holds identity + pinnedAt only.

const codexPinBackendID = "codex"

func (a *Agent) pinScope() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.codexHome
}

// SetSessionPinned implements core.SessionPinner.
func (a *Agent) SetSessionPinned(_ context.Context, sessionID, directory string, pinned bool, pinnedAt time.Time) (*core.SessionPin, error) {
	if a.pinStore == nil {
		return nil, pinstore.ErrStoreUnavailable
	}
	if pinned && pinnedAt.IsZero() {
		pinnedAt = time.Now().UTC()
	}
	scope := a.pinScope()
	if err := a.pinStore.SetPinned(codexPinBackendID, scope, sessionID, directory, pinned, pinnedAt); err != nil {
		return nil, err
	}
	if !pinned {
		return nil, nil
	}
	return &core.SessionPin{
		BackendID: codexPinBackendID,
		SessionID: sessionID,
		Directory: directory,
		PinnedAt:  pinnedAt.UTC(),
	}, nil
}

// ListPinnedSessions implements core.SessionPinner. Returns identity-only pins across
// all CODEX_HOME scopes for the go-bridge handler to enrich from sessionListCache.
func (a *Agent) ListPinnedSessions(_ context.Context) ([]core.SessionPin, error) {
	if a.pinStore == nil {
		return nil, pinstore.ErrStoreUnavailable
	}
	entries := a.pinStore.List(codexPinBackendID)
	out := make([]core.SessionPin, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ToPin())
	}
	return out, nil
}

// cleanupPin is invoked by DeleteSession to drop pin metadata. The pin may have been
// recorded under the current CODEX_HOME scope; RemoveAll sweeps by session ID across all
// scopes to be safe.
func (a *Agent) cleanupPin(sessionID string) {
	if a.pinStore == nil || sessionID == "" {
		return
	}
	a.pinStore.RemoveAll(codexPinBackendID, sessionID)
}

var _ core.SessionPinner = (*Agent)(nil)
