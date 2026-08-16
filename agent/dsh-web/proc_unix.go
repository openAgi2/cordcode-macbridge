//go:build unix

package dshweb

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// Managed-server shutdown pacing: TERM grace window before group KILL.
const (
	graceTermSeconds = 5
	killPollInterval = 100 * time.Millisecond
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

// terminateProcessGroup escalates TERM → KILL against the process group.
func terminateProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pgid := cmd.Process.Pid
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		// Group TERM failed; fall back to direct TERM before escalation.
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	deadline := time.Now().Add(graceTermSeconds * time.Second)
	for {
		if err := syscall.Kill(-pgid, 0); errors.Is(err, syscall.ESRCH) {
			_, _ = cmd.Process.Wait()
			return nil
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
			return nil
		}
		time.Sleep(killPollInterval)
	}
}
