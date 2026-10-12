package codexremote

// openBrowser zombie-reaping regression (incidents/2026-10-12-bridge-zombie-
// process-exhaustion.md §3.5/§5.1-2): Start() left one /usr/bin/open zombie
// per OAuth pairing click; Run() waits internally. Same Wait-contract
// discriminator as chatgpt_auth_reap_test.go — ESRCH means reaped, a zombie
// keeps answering signal 0.

import (
	"path/filepath"
	"testing"
)

func TestOpenBrowserReapsLauncherChild(t *testing.T) {
	bin := writeReapStub(t, "open-stub", "")
	pidFile := filepath.Join(t.TempDir(), "pid")
	prev := openBrowserBin
	openBrowserBin = bin
	t.Cleanup(func() { openBrowserBin = prev })
	t.Setenv("FAKE_PROBE_PIDFILE", pidFile)

	if err := openBrowser("https://example.invalid/oauth/callback"); err != nil {
		t.Fatalf("openBrowser: %v", err)
	}
	assertStubReaped(t, pidFile)
}
