package gobridge

// backend_action_capabilities_test.go locks the atomic action capability matrix
// (session-list parity plan §6.2 / Phase 3): session_rename / session_archive
// are advertised independently of the legacy session_mutation AND-combination,
// so Grok (rename+delete, no archive) and DSH Web (rename+pin, no archive/delete)
// can express their real surfaces. Covers the four single-interface states and
// the real production drivers.

import (
	"context"
	"testing"
	"time"

	dshweb "github.com/openAgi2/cordcode-macbridge/agent/dsh-web"
	"github.com/openAgi2/cordcode-macbridge/agent/grokbuild"
	"github.com/openAgi2/cordcode-macbridge/core"
)

// actionMatrixBase is a MINIMAL agent (it must NOT embed the shared fakeAgent — that
// base implements the full mutation method set, which would silently satisfy
// every interface assertion). Each test variant adds exactly one mutation
// interface, proving each capability derives from its own assertion.
type actionMatrixBase struct {
	name string
}

func (a *actionMatrixBase) Name() string { return a.name }
func (a *actionMatrixBase) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	return nil, context.Canceled
}
func (a *actionMatrixBase) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (a *actionMatrixBase) Stop() error { return nil }

type renamerOnlyAgent struct{ *actionMatrixBase }

func (a *renamerOnlyAgent) RenameSession(ctx context.Context, sessionID, title string) (*core.AgentSessionInfo, error) {
	return &core.AgentSessionInfo{ID: sessionID, Summary: title}, nil
}

type archiverOnlyAgent struct{ *actionMatrixBase }

func (a *archiverOnlyAgent) ArchiveSession(ctx context.Context, sessionID string, archivedAt time.Time) (*core.AgentSessionInfo, error) {
	return &core.AgentSessionInfo{ID: sessionID, ArchivedAt: archivedAt}, nil
}

type deleterOnlyAgent struct{ *actionMatrixBase }

func (a *deleterOnlyAgent) DeleteSession(ctx context.Context, sessionID string) error { return nil }

type pinnerOnlyAgent struct{ *actionMatrixBase }

func (a *pinnerOnlyAgent) SetSessionPinned(ctx context.Context, sessionID, directory string, pinned bool, pinnedAt time.Time) (*core.SessionPin, error) {
	return nil, nil
}

func (a *pinnerOnlyAgent) ListPinnedSessions(ctx context.Context) ([]core.SessionPin, error) {
	return nil, nil
}

func hasCap(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// TestAtomicActionCapabilities_IndependentDerivation: each atomic capability
// derives from its own interface; session_mutation still requires BOTH renamer
// AND archiver (legacy AND semantics preserved).
func TestAtomicActionCapabilities_IndependentDerivation(t *testing.T) {
	cases := []struct {
		name         string
		agent        core.Agent
		wantCaps     []string
		wantAbsent   []string
		wantMutation bool
	}{
		{
			name:         "renamer-only advertises session_rename only",
			agent:        &renamerOnlyAgent{&actionMatrixBase{name: "matrix-renamer"}},
			wantCaps:     []string{"session_rename"},
			wantAbsent:   []string{"session_archive", "session_delete", "session_pin", "session_mutation"},
			wantMutation: false,
		},
		{
			name:         "archiver-only advertises session_archive only",
			agent:        &archiverOnlyAgent{&actionMatrixBase{name: "matrix-archiver"}},
			wantCaps:     []string{"session_archive"},
			wantAbsent:   []string{"session_rename", "session_delete", "session_pin", "session_mutation"},
			wantMutation: false,
		},
		{
			name:         "deleter-only advertises session_delete only",
			agent:        &deleterOnlyAgent{&actionMatrixBase{name: "matrix-deleter"}},
			wantCaps:     []string{"session_delete"},
			wantAbsent:   []string{"session_rename", "session_archive", "session_pin", "session_mutation"},
			wantMutation: false,
		},
		{
			name:         "pinner-only advertises session_pin only",
			agent:        &pinnerOnlyAgent{&actionMatrixBase{name: "matrix-pinner"}},
			wantCaps:     []string{"session_pin"},
			wantAbsent:   []string{"session_rename", "session_archive", "session_delete", "session_mutation"},
			wantMutation: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caps := deriveBackendCapabilities("matrix", tc.agent, "")
			for _, want := range tc.wantCaps {
				if !hasCap(caps, want) {
					t.Errorf("missing capability %q in %v", want, caps)
				}
			}
			for _, absent := range tc.wantAbsent {
				if hasCap(caps, absent) {
					t.Errorf("unexpected capability %q in %v", absent, caps)
				}
			}
			if hasCap(caps, "session_mutation") != tc.wantMutation {
				t.Errorf("session_mutation = %v, want %v", hasCap(caps, "session_mutation"), tc.wantMutation)
			}
		})
	}
}

// TestRealDriverActionMatrix: production drivers must express their real
// surfaces — Grok (rename+delete, no archive) and DSH Web (rename+pin, no
// archive/delete).
func TestRealDriverActionMatrix(t *testing.T) {
	grok, err := grokbuild.New(nil)
	if err != nil {
		t.Fatalf("grokbuild.New: %v", err)
	}
	caps := deriveBackendCapabilities("grokbuild", grok, "")
	if !hasCap(caps, "session_rename") || !hasCap(caps, "session_delete") {
		t.Errorf("grokbuild missing rename/delete: %v", caps)
	}
	if hasCap(caps, "session_archive") {
		t.Errorf("grokbuild must NOT advertise session_archive (no SessionArchiver): %v", caps)
	}
	if hasCap(caps, "session_mutation") {
		t.Errorf("grokbuild must NOT advertise legacy session_mutation (no archiver): %v", caps)
	}

	dsh, err := dshweb.New(nil)
	if err != nil {
		t.Fatalf("dshweb.New: %v", err)
	}
	caps = deriveBackendCapabilities("dsh-web", dsh, "")
	if !hasCap(caps, "session_rename") || !hasCap(caps, "session_pin") {
		t.Errorf("dsh-web missing rename/pin: %v", caps)
	}
	if hasCap(caps, "session_archive") {
		t.Errorf("dsh-web must NOT advertise session_archive: %v", caps)
	}
	if hasCap(caps, "session_delete") {
		t.Errorf("dsh-web must NOT advertise session_delete: %v", caps)
	}
	if hasCap(caps, "session_mutation") {
		t.Errorf("dsh-web must NOT advertise legacy session_mutation: %v", caps)
	}
}
