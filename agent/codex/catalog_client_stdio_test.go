//go:build unix

package codex

// catalog_client_stdio_test.go 验证 catalog client 的 stdio 子进程生命周期 glue
// （§4.3 进程管理红线）。仅 unix（syscall 进程组探测）；Windows 等价路径由 go build 覆盖
// （catalog_proc_windows.go 编译 + Phase 1E ProcessRegistry 测试覆盖进程组回收语义）。

import (
	"context"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type stubRegistrar struct {
	onRegister func(cmd *exec.Cmd) func()
}

func (s stubRegistrar) Register(cmd *exec.Cmd) (unregister func()) {
	if s.onRegister == nil {
		return func() {}
	}
	return s.onRegister(cmd)
}

// pgidGone 报告进程组 pgid 是否已不存在（signal 0 探测；test-only）。
func pgidGone(pgid int) bool {
	return syscall.Kill(-pgid, syscall.Signal(0)) != nil
}

// waitPgidGone 轮询直到进程组 pgid 消失或超时。
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

// TestCatalogClient_StdioRegistersAndCloseReaps 锁定 §4.3 进程红线：stdio 子进程注册到
// registrar，Close 经进程组 SIGKILL 回收整组 + 注销 registrar。用 sleep 子进程模拟 app-server
// （不需要真实握手——只验证 Register/Close 的进程生命周期 glue）。
func TestCatalogClient_StdioRegistersAndCloseReaps(t *testing.T) {
	if testing.Short() {
		t.Skip("stdio lifecycle test spawns subprocess")
	}
	var registered int64
	var unregisterCalled int64
	fakeRegistrar := stubRegistrar{
		onRegister: func(cmd *exec.Cmd) func() {
			atomic.StoreInt64(&registered, 1)
			return func() { atomic.StoreInt64(&unregisterCalled, 1) }
		},
	}

	// 直接构造 client（绕过 newCatalogClient 的 initialize——sleep 不回握手），手工注册一个
	// sleep 子进程，以验证 Close 的进程组回收 glue。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &catalogClient{
		cfg:       catalogClientConfig{registrar: fakeRegistrar, cliBin: "sleep"},
		transport: appServerTransportStdio,
		ctx:       ctx,
		cancel:    cancel,
		pending:   make(map[int64]chan rpcResponseEnvelope),
	}
	c.alive.Store(true)

	cmd := exec.CommandContext(ctx, "sleep", "30")
	prepareCmdForKill(cmd) // §4.3：进程组模式（Setpgid）
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pgid := cmd.Process.Pid
	c.cmd = cmd
	c.unregister = c.cfg.registrar.Register(cmd)
	if atomic.LoadInt64(&registered) != 1 {
		t.Fatal("registrar.Register 未被调用")
	}

	c.Close()

	// 注销回调被调用。
	if atomic.LoadInt64(&unregisterCalled) != 1 {
		t.Fatal("unregister 未在 Close 时调用")
	}
	// 进程组被 SIGKILL 回收（§4.3 红线：含孙子，不得 Process.Kill()）。
	if !waitPgidGone(t, pgid, 3*time.Second) {
		t.Fatalf("进程组 %d 在 Close 后仍存活（进程组 kill 未生效）", pgid)
	}
}
