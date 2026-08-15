//go:build unix

package gobridge

// catalog_process_registry_test.go 验证 Phase 1 chunk 1E：ProcessRegistry + 进程组 kill + shutdown hook
//（设计 §4.3 进程管理红线 / §11 / P0-4）。用真实 sleep 子进程证明：
//  1. Register/AlivePIDs/Unregister 确定性跟踪（不调 ps/pgrep）；
//  2. Shutdown 经进程组 SIGKILL 回收注册子进程 + WaitAllDrained 确认；
//  3. 进程组 kill 回收孙子进程（§4.3 红线：appServerSession 的 Process.Kill() 会漏孙子，catalog 不得）；
//  4. handlers.Shutdown 的 catalog hook 独立于 sessionRegistry.drain，端到端回收 catalog 子进程。
//
// 仅 unix（darwin 开发 + linux CI）；catalog_proc_windows.go 的等价路径由 go build 覆盖（同 codex 先例）。

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// spawnSleepCmd 启动 `sleep dur` 并设 Setpgid（catalog 进程组模式），返回已 start 的 cmd。
func spawnSleepCmd(t *testing.T, dur string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", dur)
	catalogPrepareCmdForKill(cmd) // Setpgid
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep %s: %v", dur, err)
	}
	return cmd
}

// pgidGone 报告进程组 pgid 是否已不存在（signal 0 探测；TEST-ONLY，registry 自身用 AlivePIDs）。
func pgidGone(pgid int) bool {
	return syscall.Kill(-pgid, syscall.Signal(0)) != nil
}

// waitPgidGone 轮询直到进程组 pgid 消失或超时，返回是否已消失。
func waitPgidGone(t *testing.T, pgid int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pgidGone(pgid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return pgidGone(pgid)
}

// TestProcessRegistry_EmptyNoop：空 registry 的 Shutdown/AlivePIDs 不 panic、返回空。
func TestProcessRegistry_EmptyNoop(t *testing.T) {
	r := NewProcessRegistry()
	if pids := r.AlivePIDs(); len(pids) != 0 {
		t.Fatalf("empty AlivePIDs = %v, want empty", pids)
	}
	if rem := r.Shutdown(2 * time.Second); len(rem) != 0 {
		t.Fatalf("empty Shutdown = %v, want empty", rem)
	}
}

// TestProcessRegistry_ShutdownKillsAndDrains：注册 sleep 30 → AlivePIDs 含其 pid → Shutdown 进程组
// SIGKILL 回收 → 残留空 + 进程组确已消失。
func TestProcessRegistry_ShutdownKillsAndDrains(t *testing.T) {
	r := NewProcessRegistry()
	cmd := spawnSleepCmd(t, "30")
	pgid := cmd.Process.Pid
	r.Register(cmd)

	if pids := r.AlivePIDs(); len(pids) != 1 || pids[0] != pgid {
		t.Fatalf("AlivePIDs = %v, want [%d]", pids, pgid)
	}
	if rem := r.Shutdown(2 * time.Second); len(rem) != 0 {
		t.Fatalf("Shutdown 残留 = %v, want empty（须 drain）", rem)
	}
	if !waitPgidGone(t, pgid, 3*time.Second) {
		t.Fatalf("进程组 %d 在 Shutdown 后仍存活（进程组 kill 未生效）", pgid)
	}
}

// TestProcessRegistry_ShutdownIdempotent：重复 Shutdown 安全（第二次无副作用、返回空）。
func TestProcessRegistry_ShutdownIdempotent(t *testing.T) {
	r := NewProcessRegistry()
	cmd := spawnSleepCmd(t, "30")
	pgid := cmd.Process.Pid
	r.Register(cmd)
	r.Shutdown(2 * time.Second)
	if rem := r.Shutdown(2 * time.Second); len(rem) != 0 {
		t.Fatalf("第二次 Shutdown 残留 = %v, want empty", rem)
	}
	if !waitPgidGone(t, pgid, 3*time.Second) {
		t.Fatalf("进程组 %d 仍存活", pgid)
	}
}

// TestProcessRegistry_WaitAllDrainedNaturalExit：注册短命 sleep 1，不调 Shutdown，WaitAllDrained 等其
// 自然退出 → 返回 nil（reap goroutine 自动 Unregister）。
func TestProcessRegistry_WaitAllDrainedNaturalExit(t *testing.T) {
	r := NewProcessRegistry()
	cmd := spawnSleepCmd(t, "1")
	pgid := cmd.Process.Pid
	r.Register(cmd)
	remaining, err := r.WaitAllDrained(3 * time.Second)
	if err != nil {
		t.Fatalf("WaitAllDrained err = %v, want nil", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining = %v, want empty（自然退出须 drain）", remaining)
	}
	if !waitPgidGone(t, pgid, 2*time.Second) {
		t.Fatalf("进程组 %d 自然退出后仍存活", pgid)
	}
}

// TestProcessRegistry_ShutdownReclaimsGrandchild（§4.3 进程管理红线）：注册的 sh 子进程 fork 一个
// 后台 sleep 孙子进程；Shutdown 的进程组 SIGKILL 必须把孙子也回收（appServerSession 的
// Process.Kill() 只杀直属子进程会漏孙子，catalog 不得照搬）。断言孙子 PID 在 Shutdown 后消失。
func TestProcessRegistry_ShutdownReclaimsGrandchild(t *testing.T) {
	r := NewProcessRegistry()
	// sh 启动后台 sleep（孙子），打印其 PID，然后 wait（保持 sh 存活直到被 kill）。
	cmd := exec.Command("sh", "-c", "sleep 30 & echo $!; wait")
	catalogPrepareCmdForKill(cmd) // Setpgid：sh 为组长，孙子继承同 pgid
	// 用 StdoutPipe（不用 bytes.Buffer——它非 goroutine-safe，与 os/exec 的 copy goroutine 并发会 race）。
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdoutpipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sh: %v", err)
	}
	pgid := cmd.Process.Pid

	// 读一行孙子 PID（带 2s 上限，避免 sh 异常时无限阻塞）。
	type readResult struct {
		line string
		err  error
	}
	rc := make(chan readResult, 1)
	reader := bufio.NewReader(stdout)
	go func() {
		line, err := reader.ReadString('\n')
		rc <- readResult{line, err}
	}()
	var grandchild int
	select {
	case res := <-rc:
		if res.err != nil {
			_ = catalogForceKillProcess(cmd)
			t.Fatalf("读孙子 PID 失败: %v", res.err)
		}
		v, perr := strconv.Atoi(strings.TrimSpace(res.line))
		if perr != nil {
			_ = catalogForceKillProcess(cmd)
			t.Fatalf("孙子 PID 解析失败: %q (%v)", res.line, perr)
		}
		grandchild = v
	case <-time.After(2 * time.Second):
		_ = catalogForceKillProcess(cmd)
		t.Fatalf("2s 内未读到孙子 PID")
	}
	r.Register(cmd)

	if rem := r.Shutdown(2 * time.Second); len(rem) != 0 {
		t.Fatalf("Shutdown 残留 = %v, want empty", rem)
	}
	// 组长进程组消失。
	if !waitPgidGone(t, pgid, 3*time.Second) {
		t.Fatalf("组长进程组 %d 仍存活", pgid)
	}
	// 孙子进程（独立 PID）也必须被进程组 kill 回收（§4.3 红线）。
	if !waitPgidGone(t, grandchild, 3*time.Second) {
		t.Fatalf("孙子进程 %d 仍存活（进程组 kill 须回收孙子，不得照搬 Process.Kill()）", grandchild)
	}
}

// TestHandlers_Shutdown_ReapsCatalogSubprocess（§4.3/§11 端到端 hook）：catalog 子进程注册到
// h.catalogProcessRegistry()（不经 sessionRegistry），handlers.Shutdown 必须经独立 catalog hook
// （catalogProcRegistry.Shutdown）回收它，与 sessionRegistry.drain() 解耦。
func TestHandlers_Shutdown_ReapsCatalogSubprocess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewHandlersWithContext(ctx)

	reg := h.catalogProcessRegistry() // lazy init（Phase 1：Phase 2-5 catalog client 经此 Register）
	cmd := spawnSleepCmd(t, "30")
	pgid := cmd.Process.Pid
	reg.Register(cmd)
	if pids := reg.AlivePIDs(); len(pids) != 1 || pids[0] != pgid {
		t.Fatalf("AlivePIDs = %v, want [%d]", pids, pgid)
	}

	shutdownCtx, sc := context.WithTimeout(context.Background(), 5*time.Second)
	defer sc()
	if err := h.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown error = %v", err)
	}

	// catalog 子进程组经 hook 回收（独立于 session close 路径）。
	if !waitPgidGone(t, pgid, 3*time.Second) {
		t.Fatalf("catalog 子进程组 %d 在 handlers.Shutdown 后仍存活（catalog hook 未生效）", pgid)
	}
	if pids := reg.AlivePIDs(); len(pids) != 0 {
		t.Fatalf("registry AlivePIDs = %v after Shutdown, want empty", pids)
	}
}
