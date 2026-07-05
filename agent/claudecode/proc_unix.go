//go:build unix

package claudecode

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// prepareCmdForProcessGroup puts the spawned command in its own process group
// (Setpgid) so Close() can reap the whole tree (the CLI plus any wrapper /
// sudo / plugin subprocesses it forks) with a single negative-PID signal.
// Mirrors agent/codex/proc_unix.go.
func prepareCmdForProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// forceKillProcessGroup sends SIGKILL to the process group led by cmd's PID.
// Safe to call when the process is already gone (ESRCH / ErrProcessDone).
func forceKillProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// signalProcessGroup sends sig to the process group led by cmd's PID.
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errno, ok := err.(syscall.Errno); ok && errno == syscall.EPERM {
		return true
	}
	return false
}

// verifyClaudeProcessIdentity checks whether the live process at pid is
// consistent with a Claude Code session: its executable name contains "claude"
// and, when expectCwd is non-empty, its working directory equals expectCwd.
//
// It is fail-OPEN: when a platform lookup is unavailable (process gone, ps/lsof
// missing, /proc absent) it returns true, so a transient introspection failure
// does not regress into false-idle. The PID-reuse defence relies on the
// strong-mismatch branches — executable not claude, or cwd differs — which
// return false. Callers must have already confirmed liveness via procAlive.
func verifyClaudeProcessIdentity(pid int, expectCwd string) bool {
	if comm, ok := processComm(pid); ok {
		if !strings.Contains(strings.ToLower(comm), "claude") {
			return false
		}
	}
	if expectCwd != "" {
		if cwd, ok := processCwd(pid); ok && cwd != expectCwd {
			return false
		}
	}
	return true
}

// processComm returns the executable name of pid (the basename path on macOS,
// or the comm string on Linux). ok=false means the lookup was unavailable
// (process gone, or ps not available) — callers fail-open.
func processComm(pid int) (string, bool) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return "", false
	}
	comm := strings.TrimSpace(string(out))
	if comm == "" {
		return "", false
	}
	return comm, true
}

// processCwd returns the working directory of pid. ok=false means unavailable.
// Linux uses the /proc/<pid>/cwd symlink (no fork); macOS falls back to lsof.
func processCwd(pid int) (string, bool) {
	if link, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd"); err == nil && link != "" {
		return link, true
	}
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			if cwd := strings.TrimPrefix(line, "n"); cwd != "" {
				return cwd, true
			}
		}
	}
	return "", false
}

