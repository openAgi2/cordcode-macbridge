//go:build unix

package codexweb

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func inspectManagedProcess(pid int) (command, startTime string, alive bool) {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return "", "", false
	}
	commandOut, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", "", false
	}
	startOut, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", "", false
	}
	return strings.TrimSpace(string(commandOut)), strings.TrimSpace(string(startOut)), true
}

func managedProcessStartTime(pid int) string {
	for i := 0; i < 10; i++ {
		_, start, alive := inspectManagedProcess(pid)
		if alive && start != "" {
			return start
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ""
}

func managedProcessOwnsPort(pid, port int) bool {
	out, err := exec.Command("lsof", "-nP", "-a", "-p", strconv.Itoa(pid),
		"-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fp").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "p"+strconv.Itoa(pid) {
			return true
		}
	}
	return false
}

func terminateManagedProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
