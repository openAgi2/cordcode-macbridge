package gobridge

// catalog_process_registry.go 是 catalog client 的进程 supervisor（设计 §4.3 进程与连接生命周期 / §11）。
//
// 它跟踪 catalog 子进程（Phase 2-5 stdio 单例 catalog client，如 Codex app-server / Grok ACP），
// 暴露确定性回收与断言接口，使「shutdown 后无残留子进程」可被测试断言而不依赖 ps/pgrep（P0-4）。
// OpenCode 走 HTTP（既有 ocProxy），无 catalog 子进程，不经此 registry。
//
// 生命周期：
//   - catalog client 启动 stdio 子进程时调用 Register(cmd)（cmd 须已 catalogPrepareCmdForKill 即
//     Setpgid，且已 Start）；Register 转移 reap 所有权（registry 持有 cmd.Wait()，client 不得再 Wait）。
//   - 子进程自然退出 → registry 内部 reap goroutine（cmd.Wait 返回）自动 Unregister。
//   - handlers.Shutdown 调 Shutdown(timeout)：经进程组 SIGKILL 杀全部注册子进程 + WaitAllDrained 确认。
//   - AlivePIDs() 用于断言「shutdown 后残留子进程 == 空」。
//
// catalog client 不进 sessionRegistry，sessionRegistry.drain() 不覆盖它；handlers.Shutdown 经
// catalogProcessRegistry getter 取本 registry 并独立 Shutdown（类比 deltaBatcher.Stop()），带确定性
// 总超时（catalogShutdownTimeout），不与 session close 串行化。

import (
	"errors"
	"os/exec"
	"sort"
	"sync"
	"time"
)

// catalogShutdownTimeout 是 handlers.Shutdown 中 catalog 子进程回收的确定性总超时（§4.3「带确定性
// 总超时」）。独立于 caller ctx：即便 bridge shutdown ctx 紧，catalog 子进程仍有固定窗口被 SIGKILL
// 回收，避免泄漏到下一进程。
const catalogShutdownTimeout = 5 * time.Second

// errCatalogDrainTimeout 由 WaitAllDrained 在 timeout 内仍有未结束子进程时返回（Shutdown 的残留证据）。
var errCatalogDrainTimeout = errors.New("catalog ProcessRegistry: drain timeout (alive subprocesses remain)")

// ProcessRegistry 跟踪 catalog 子进程并暴露确定性回收/断言接口（§4.3/§11）。零值不可用，须 New。
type ProcessRegistry struct {
	mu      sync.Mutex
	entries map[int]*catalogTrackedProc // pid → tracked
}

// catalogTrackedProc 是一个被跟踪的 catalog 子进程。exited 在 reap goroutine cmd.Wait() 返回后 close。
type catalogTrackedProc struct {
	pid    int
	cmd    *exec.Cmd
	exited chan struct{}
}

// NewProcessRegistry 创建空 registry。
func NewProcessRegistry() *ProcessRegistry {
	return &ProcessRegistry{entries: make(map[int]*catalogTrackedProc)}
}

// catalogProcessRegistry 返回 per-Handlers 的 catalog 子进程 supervisor，首次使用懒加载（sync.Once，
// 构造路径 NewHandlers 不改）。Phase 2-5 catalog client（Codex/Grok stdio 单例）经此 Register 其子进程；
// handlers.Shutdown 经 h.catalogProcRegistry 直读（不经 getter，避免 shutdown 时无谓初始化）调 Shutdown。
func (h *Handlers) catalogProcessRegistry() *ProcessRegistry {
	h.catalogProcInitOnce.Do(func() {
		h.catalogProcRegistry = NewProcessRegistry()
	})
	return h.catalogProcRegistry
}

// Register 记录一个 catalog 子进程并启动 reap goroutine。cmd 须已 Start 且 catalogPrepareCmdForKill
// 已设 Setpgid。Register 后 registry 拥有 cmd.Wait()（client 不得再调 Wait，否则 double-Wait）。
// 返回 unregister 回调（幂等），供 client 在已知子进程结束时显式移除；reap goroutine 也会自动移除。
func (r *ProcessRegistry) Register(cmd *exec.Cmd) (unregister func()) {
	if cmd == nil || cmd.Process == nil {
		return func() {}
	}
	pid := cmd.Process.Pid
	entry := &catalogTrackedProc{pid: pid, cmd: cmd, exited: make(chan struct{})}
	r.mu.Lock()
	r.entries[pid] = entry
	r.mu.Unlock()
	go func() {
		_ = cmd.Wait() // registry 拥有 reap；子进程结束（自然 / 被 Kill）即返回
		close(entry.exited)
		r.Unregister(pid) // 防御：client 忘记调 unregister 时也能自清理
	}()
	return func() { r.Unregister(pid) }
}

// Unregister 移除一个子进程记录（幂等，pid 不存在也安全）。
func (r *ProcessRegistry) Unregister(pid int) {
	r.mu.Lock()
	delete(r.entries, pid)
	r.mu.Unlock()
}

// AlivePIDs 返回当前注册的存活 catalog 子进程 PID 升序快照。确定性断言，不调 ps/pgrep（P0-4）。
func (r *ProcessRegistry) AlivePIDs() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	pids := make([]int, 0, len(r.entries))
	for pid := range r.entries {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}

// WaitAllDrained 等待所有当前注册子进程结束，最多 timeout。返回 (remaining, err)：
//   - 全部 drain → (nil, nil)；
//   - timeout 内仍有未结束 → (残留 PID 快照, errCatalogDrainTimeout)；
//   - timeout<=0 → 立即返回当前快照（不等待）。
func (r *ProcessRegistry) WaitAllDrained(timeout time.Duration) ([]int, error) {
	r.mu.Lock()
	entries := make([]*catalogTrackedProc, 0, len(r.entries))
	for _, e := range r.entries {
		entries = append(entries, e)
	}
	r.mu.Unlock()
	if timeout <= 0 {
		return r.AlivePIDs(), nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, e := range entries {
		select {
		case <-e.exited:
		case <-timer.C:
			return r.AlivePIDs(), errCatalogDrainTimeout
		}
	}
	return nil, nil
}

// Shutdown 经进程组 SIGKILL 杀掉所有当前注册子进程（含孙子进程），然后 WaitAllDrained(timeout)
// 确认回收。返回残留 PID（空表示全部回收）。确定性总超时（§4.3）。幂等：重复调用无副作用。
func (r *ProcessRegistry) Shutdown(timeout time.Duration) []int {
	r.mu.Lock()
	toKill := make([]*catalogTrackedProc, 0, len(r.entries))
	for _, e := range r.entries {
		toKill = append(toKill, e)
	}
	r.mu.Unlock()
	for _, e := range toKill {
		_ = catalogForceKillProcess(e.cmd) // 进程组 SIGKILL（Setpgid 已在 Register 前由 client 设）
	}
	remaining, _ := r.WaitAllDrained(timeout)
	return remaining
}
