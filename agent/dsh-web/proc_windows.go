//go:build windows

package dshweb

import (
	"os/exec"
	"syscall"
	"time"
)

const graceTermSeconds = 5

// prepareCmdForProcessGroup is the windows no-op (mirrors agent/dsh posture:
// process groups are a unix concept; windows relies on Process.Kill).
func prepareCmdForProcessGroup(cmd *exec.Cmd) {}

// terminateProcessGroup kills the spawned process directly on windows.
func terminateProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	return nil
}

var _ = time.Second // keep time imported for future pacing parity
var _ = syscall.SIGTERM

// processCommandLine is the windows placeholder: tasklist-based matching is
// not wired; the legacy cleanup falls back to warn-and-remove (never a wrong
// kill).
func processCommandLine(pid int) (string, bool) {
	return "", false
}

// processStartTime is the windows placeholder (S9 best-effort surface).
func processStartTime(pid int) string { return "" }
