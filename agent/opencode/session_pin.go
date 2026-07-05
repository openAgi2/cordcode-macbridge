package opencode

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
	"github.com/openAgi2/cordcode-macbridge/pinstore"
)

// SessionPinner receivers for the OpenCode driver.
//
// OpenCode sessions are served by the upstream opencode server, scoped per directory
// (x-opencode-directory header). A pinned session's directory is essential for summary
// resolution (OpenCodeProxy.getSession(id, dir)), so it is captured in the pin entry and
// used as the pinstore scope (normalized). This is what lets list_pinned_sessions surface
// pinned sessions from project buckets that the iOS sidebar has not yet loaded.
//
// Summary fields are resolved by the go-bridge handler via OpenCodeProxy.getSession; the
// store holds identity + pinnedAt only.

const opencodePinBackendID = "opencode"

// pinScope normalizes a directory into a stable scope key. Empty directory maps to ""
// (sessions pinned without a directory hint are keyed without scope; acceptable but rare).
func pinScope(directory string) string {
	d := strings.TrimSpace(directory)
	if d == "" {
		return ""
	}
	abs, err := filepath.Abs(d)
	if err != nil {
		return filepath.Clean(d)
	}
	return abs
}

// SetSessionPinned implements core.SessionPinner.
func (a *Agent) SetSessionPinned(_ context.Context, sessionID, directory string, pinned bool, pinnedAt time.Time) (*core.SessionPin, error) {
	if a.pinStore == nil {
		return nil, pinstore.ErrStoreUnavailable
	}
	if pinned && pinnedAt.IsZero() {
		pinnedAt = time.Now().UTC()
	}
	scope := pinScope(directory)
	if err := a.pinStore.SetPinned(opencodePinBackendID, scope, sessionID, directory, pinned, pinnedAt); err != nil {
		return nil, err
	}
	if !pinned {
		return nil, nil
	}
	return &core.SessionPin{
		BackendID: opencodePinBackendID,
		SessionID: sessionID,
		Directory: directory,
		PinnedAt:  pinnedAt.UTC(),
	}, nil
}

// ListPinnedSessions implements core.SessionPinner. Returns identity-only pins across
// all directories for the go-bridge handler to enrich via OpenCodeProxy.getSession.
func (a *Agent) ListPinnedSessions(_ context.Context) ([]core.SessionPin, error) {
	if a.pinStore == nil {
		return nil, pinstore.ErrStoreUnavailable
	}
	entries := a.pinStore.List(opencodePinBackendID)
	out := make([]core.SessionPin, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ToPin())
	}
	return out, nil
}

// cleanupPin is invoked by DeleteSession to drop pin metadata when an OpenCode session is
// deleted. directory is not known at delete time, so RemoveAll sweeps by session ID.
func (a *Agent) cleanupPin(sessionID string) {
	if a.pinStore == nil || sessionID == "" {
		return
	}
	a.pinStore.RemoveAll(opencodePinBackendID, sessionID)
}

var _ core.SessionPinner = (*Agent)(nil)
