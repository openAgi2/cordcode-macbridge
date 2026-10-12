//go:build unix

package dshweb

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// prepareCmdForProcessGroup puts the managed dsh web server in its own process
// group so Stop() reaps the node server and any child processes with one
// negative-PID signal (same posture as agent/dsh and grokbuild).
func prepareCmdForProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroupNoWait sends KILL to the process group (and direct child)
// WITHOUT reaping — for callers whose child's single Wait belongs to another
// goroutine (the npm install timeout path, and execManagedStarter's reaper —
// incidents/2026-10-12). ESRCH on an already-dead group is fine.
func killProcessGroupNoWait(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// processCommandLine returns the full command line of a pid via ps (unix).
func processCommandLine(pid int) (string, bool) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil || len(out) == 0 {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// processStartTime returns the pid's start time via ps (design S9 —
// host.describe has no such field).
func processStartTime(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
