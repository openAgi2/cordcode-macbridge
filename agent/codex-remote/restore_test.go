package codexremote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestWaitForRestoreMissingIdentityIsNotConfigured(t *testing.T) {
	agent := New(nil)
	if err := agent.WaitForRestore(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitForRestoreBoundedPersistedRestore(t *testing.T) {
	previous := remoteRestoreWaitTimeout
	remoteRestoreWaitTimeout = 10 * time.Millisecond
	t.Cleanup(func() { remoteRestoreWaitTimeout = previous })

	agent := New(nil)
	path := filepath.Join(t.TempDir(), "pairing.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	agent.pairing.storePath = path
	agent.pairing.mu.Lock()
	agent.pairing.state.phase = PairPhaseAuthorizing
	agent.pairing.mu.Unlock()

	if err := agent.WaitForRestore(context.Background()); !errors.Is(err, core.ErrRestoreInProgress) {
		t.Fatalf("err = %v, want restore in progress", err)
	}

	agent.pairing.mu.Lock()
	agent.pairing.state.phase = PairPhaseFailed
	agent.pairing.mu.Unlock()
	if err := agent.WaitForRestore(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("terminal failure err = %v", err)
	}
}
