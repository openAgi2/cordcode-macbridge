//go:build unix

package gobridge

// catalog_proc_unix.go 是 catalog client 子进程回收的进程组 kill 助手（设计 §4.3 进程管理红线）。
// catalog stdio 单例子进程（Phase 2-5 Codex/Grok）必须照 codexSession 的进程组模式回收：
// Setpgid 建独立进程组 → Kill(-pgid, SIGKILL) 杀整个组（含 fork 的孙子进程），不得照搬
// appServerSession 的 Process.Kill()（只杀直属子进程，漏孙子）。本文件是 agent/codex/proc_unix.go
// 的 catalog 专用镜像（agent/codex 的同名 helper 是 unexported，go-bridge 须自有副本）。

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// catalogPrepareCmdForKill 配置 cmd 在独立进程组启动（Setpgid），以便后续进程组 kill 回收整组。
// 必须在 cmd.Start() 前调用。nil cmd 安全返回。
func catalogPrepareCmdForKill(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// catalogForceKillProcess 经进程组 SIGKILL 杀掉 cmd 的整个进程组（含孙子进程）。已退出/不存在
// 视为成功（ESRCH / ErrProcessDone 吞掉）。nil cmd 或无 Process 安全返回。
func catalogForceKillProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
