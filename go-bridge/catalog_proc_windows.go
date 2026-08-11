//go:build windows

package gobridge

// catalog_proc_windows.go 是 catalog client 子进程回收的进程组 kill 助手（Windows 版，设计 §4.3）。
// catalog stdio 单例子进程（Phase 2-5）必须走进程组 kill，不得照搬 appServerSession 的 Process.Kill()。
// 本文件是 agent/codex/proc_windows.go 的 catalog 专用镜像（unexported，go-bridge 须自有副本）。

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func catalogPrepareCmdForKill(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

func catalogForceKillProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	killCmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	output, err := killCmd.CombinedOutput()
	if err == nil {
		return nil
	}
	lower := bytes.ToLower(output)
	if bytes.Contains(lower, []byte("there is no running instance")) || bytes.Contains(lower, []byte("not found")) {
		return nil
	}
	if killErr := cmd.Process.Kill(); killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	}
	return fmt.Errorf("taskkill failed: %w: %s; process kill fallback failed: %w", err, catalogKillOutput(output), killErr)
}

func catalogKillOutput(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return "(empty output)"
	}
	return trimmed
}
