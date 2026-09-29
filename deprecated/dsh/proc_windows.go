//go:build windows

package dsh

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// prepareCmdForProcessGroup has no Windows process-group equivalent; the
// runtime is signalled directly through exec.Cmd's process handle.
func prepareCmdForProcessGroup(cmd *exec.Cmd) {}

const (
	sigTERM = syscall.SIGTERM
	sigKILL = syscall.SIGKILL
)

func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
