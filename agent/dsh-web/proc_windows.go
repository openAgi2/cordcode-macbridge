//go:build windows

package dshweb

import (
	"os"
	"os/exec"
)

// prepareCmdForProcessGroup is the windows no-op (mirrors agent/dsh posture:
// process groups are a unix concept; windows relies on Process.Kill).
func prepareCmdForProcessGroup(cmd *exec.Cmd) {}

// killProcessGroupNoWait kills the direct child WITHOUT waiting — windows has
// no process groups, and the single Wait belongs to the starter's reaper
// goroutine (incidents/2026-10-12).
func killProcessGroupNoWait(pid int) {
	if pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// processCommandLine is the windows placeholder: tasklist-based matching is
// not wired; the legacy cleanup falls back to warn-and-remove (never a wrong
// kill).
func processCommandLine(pid int) (string, bool) {
	return "", false
}

// processStartTime is the windows placeholder (S9 best-effort surface).
func processStartTime(pid int) string { return "" }
