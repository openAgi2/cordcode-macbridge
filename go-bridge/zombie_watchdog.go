package gobridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// zombie_watchdog.go — runtime 级子进程健康自检（P2 缺席语义加固 §3 D3，
// incidents/2026-10-12-bridge-zombie-process-exhaustion.md §5.3-3）：该事故
// 2142 个僵尸积压 7.7 小时无任何告警。watchdog 只观测不处置：周期统计本进程
// 直接子进程与僵尸数，超阈值 WARN（报警 + 长期回归证据）；启动时打一条一次性
// armed 特征行作为部署后运行态核验锚点（CLAUDE.md 部署后运行态验证第 2 条
// ——提交独有文案，不依赖首周期 tick，启动即出）。

var (
	// zombieWatchdogPeriod 是采样周期（var 以便测试缩短）。
	zombieWatchdogPeriod = 5 * time.Minute
	// zombieWatchdogChildrenThreshold 是子进程数告警阈值——宽松防误报：
	// 正常驱动拓扑 = grok catalog 单例 1 + 每 session CLI 若干。
	zombieWatchdogChildrenThreshold = 64
)

// childProcessSampler counts the direct children and zombies of pid. Seam
// for tests; the real implementation reads the process table via ps
// （同步 Output() 收尾，合规于事故 §3.5 收尸表——不引入新僵尸）。
type childProcessSampler func(pid int) (children int, zombies int, err error)

// sampleChildProcesses scans `ps -axo ppid=,stat=` for the given pid's
// direct children and how many of them are zombies.
func sampleChildProcesses(pid int) (int, int, error) {
	out, err := exec.Command("ps", "-axo", "ppid=,stat=").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("ps sample: %w", err)
	}
	children, zombies := 0, 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		ppid, convErr := strconv.Atoi(fields[0])
		if convErr != nil || ppid != pid {
			continue
		}
		children++
		if strings.HasPrefix(fields[1], "Z") {
			zombies++
		}
	}
	return children, zombies, nil
}

// zombieWatchdogAssess is the pure judgment: any zombie is abnormal (the
// P0 reaping fixes hold them at zero — one is already a regression), and
// children beyond the threshold suggests a live-process leak.
func zombieWatchdogAssess(children, zombies, childrenThreshold int) bool {
	return zombies > 0 || children > childrenThreshold
}

// runZombieWatchdog emits the one-time armed feature line, then samples
// every period. Normal samples log nothing (no standing noise); threshold
// breaches WARN unthrottled (regression-evidence density wins); a failed
// sample only WARNs and keeps the loop alive. ctx 取消后退出。
func runZombieWatchdog(ctx context.Context, sample childProcessSampler) {
	if sample == nil {
		sample = sampleChildProcesses
	}
	pid := os.Getpid()
	slog.Info("go-bridge zombie watchdog armed", "pid", pid,
		"period", zombieWatchdogPeriod.String(),
		"childrenThreshold", zombieWatchdogChildrenThreshold)
	ticker := time.NewTicker(zombieWatchdogPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			children, zombies, err := sample(pid)
			if err != nil {
				slog.Warn("go-bridge zombie watchdog sample failed", "error", err)
				continue
			}
			if zombieWatchdogAssess(children, zombies, zombieWatchdogChildrenThreshold) {
				slog.Warn("go-bridge zombie watchdog", "children", children,
					"zombies", zombies, "pid", pid)
			}
		}
	}
}
