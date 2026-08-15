//go:build unix

package dsh

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// prepareCmdForProcessGroup puts the spawned runtime in its own process group
// so Close() reaps the runtime and any tool child processes with a
// negative-PID signal (process-group reuse mirrors grokbuild, design §1-2).
func prepareCmdForProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

const (
	sigTERM = syscall.SIGTERM
	sigKILL = syscall.SIGKILL
)

func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
