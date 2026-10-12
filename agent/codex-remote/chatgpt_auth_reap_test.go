package codexremote

// Zombie-reaping regression (incidents/2026-10-12-bridge-zombie-process-
// exhaustion.md §5.1): loadChatGPTAuth spawns a ChatGPT-embedded codex
// app-server probe on every call — the Desktop-installed-but-closed restore
// retry loop ran it ≈4.6×/min, and Kill without Wait left one zombie per
// probe until kern.maxprocperuid was exhausted. These tests pin the os/exec
// Wait contract on the success, not-logged-in and timeout paths: when
// loadChatGPTAuth returns, the probe must be fully reaped. A zombie still
// answers signal 0 with nil, so ESRCH is the discriminator; ps system-state
// polling is deliberately NOT a CI assertion (plan r1 F-4). The stub
// binaries stand in for the real probe via the chatGPTCodexBin seam.

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const reapTestAccountID = "acct_reap_test"

// reapTestJWT builds a minimally shaped ChatGPT token: three dot-separated
// segments with a payload accountIDFromJWT can decode.
func reapTestJWT(t *testing.T) string {
	t.Helper()
	payload := base64.URLEncoding.EncodeToString([]byte(
		`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + reapTestAccountID + `"}}`))
	return "h." + payload + ".s"
}

// writeReapStub writes an executable /bin/sh stub that reports its pid to
// $FAKE_PROBE_PIDFILE and then runs body.
func writeReapStub(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	script := "#!/bin/sh\n" +
		"echo $$ > \"$FAKE_PROBE_PIDFILE\"\n" +
		body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// useChatGPTCodexStub points loadChatGPTAuth at the stub binary for this test.
func useChatGPTCodexStub(t *testing.T, bin, pidFile string) {
	t.Helper()
	prev := chatGPTCodexBin
	chatGPTCodexBin = func() string { return bin }
	t.Cleanup(func() { chatGPTCodexBin = prev })
	t.Setenv("FAKE_PROBE_PIDFILE", pidFile)
}

// assertStubReaped fails unless the stub's pid is fully gone from the
// process table. When loadChatGPTAuth (or openBrowser) has returned, its
// deferred Wait already completed, so ESRCH is expected immediately; the
// short retry only absorbs kernel teardown latency. A zombie (Kill without
// Wait, the pre-fix behavior) keeps answering signal 0 and fails here.
func assertStubReaped(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("stub never reported its pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("stub pid %q not numeric: %v", raw, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stub pid %d still in the process table — zombie left behind (Kill-without-Wait regression)", pid)
}

// probeStubBody answers the app-server handshake: initialize → result, then
// getAuthStatus → the auth shape given by authLine (a bare JSON object).
// Reading continues so the child stays alive until the parent kills it (the
// success path returns while the probe is still running).
func probeStubBody(authLine string) string {
	return "while IFS= read -r line; do\n" +
		"  case \"$line\" in\n" +
		"    *'\"initialize\"'*) printf '%s\\n' '{\"id\":1,\"result\":{}}' ;;\n" +
		"    *'\"getAuthStatus\"'*) printf '%s\\n' '{\"id\":2,\"result\":" + authLine + "}' ;;\n" +
		"  esac\n" +
		"done\n"
}

func TestLoadChatGPTAuthReapsProbeOnSuccess(t *testing.T) {
	token := reapTestJWT(t)
	authLine := `{"authMethod":"chatgpt","authToken":"` + token + `"}`
	bin := writeReapStub(t, "codex-probe-ok", probeStubBody(authLine))
	pidFile := filepath.Join(t.TempDir(), "pid")
	useChatGPTCodexStub(t, bin, pidFile)

	gotToken, gotAccount, err := loadChatGPTAuth(context.Background())
	if err != nil {
		t.Fatalf("loadChatGPTAuth: %v", err)
	}
	if gotToken != token {
		t.Fatalf("token mismatch: %q", gotToken)
	}
	if gotAccount != reapTestAccountID {
		t.Fatalf("account id mismatch: %q", gotAccount)
	}
	// The probe is killed at return; it must also be reaped (Wait contract).
	assertStubReaped(t, pidFile)
}

func TestLoadChatGPTAuthReapsProbeOnNotLoggedIn(t *testing.T) {
	authLine := `{"authMethod":"apikey"}`
	bin := writeReapStub(t, "codex-probe-apikey", probeStubBody(authLine))
	pidFile := filepath.Join(t.TempDir(), "pid")
	useChatGPTCodexStub(t, bin, pidFile)

	if _, _, err := loadChatGPTAuth(context.Background()); err == nil {
		t.Fatal("expected not-logged-in error")
	}
	assertStubReaped(t, pidFile)
}

func TestLoadChatGPTAuthReapsProbeOnTimeout(t *testing.T) {
	// The incident's production shape: Desktop installed but unusable, the
	// probe never answers, the deadline (here bounded by the parent ctx)
	// kills it. `exec` keeps the killed pid identical to the reported $$
	// (and lets the kill close the stdout pipe — no grandchild holds it).
	bin := writeReapStub(t, "codex-probe-mute", "exec sleep 30\n")
	pidFile := filepath.Join(t.TempDir(), "pid")
	useChatGPTCodexStub(t, bin, pidFile)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, _, err := loadChatGPTAuth(ctx); err == nil {
		t.Fatal("expected timeout error")
	}
	assertStubReaped(t, pidFile)
}
