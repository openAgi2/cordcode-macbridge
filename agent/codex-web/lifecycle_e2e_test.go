package codexweb

// lifecycle_e2e_test.go —— 真实官方二进制 e2e（p1-lifecycle-regression 证据）。
//
// 门控：CODEXWEB_E2E=1（无 codex 环境的 CI 不跑）。复用 Phase 0 的方法：
// 短路径 CODEX_HOME（SUN_LEN）+ standalone 符号链种子（daemon start 前置）。
// 断言：
//   1. daemon 缺失时 Probe 走 cordcode-started-daemon 且六步就绪全过（真实 initialize/
//      thread/list /model/list 响应）；
//   2. §6.3：Close 不停共享 daemon（socket 仍在、daemon version=running）；
//   3. daemon start 不可用时落托管 loopback WS（真实 app-server + healthz + 就绪），
//      Close 独占回收。

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func e2eEnabled(t *testing.T) bool {
	if os.Getenv("CODEXWEB_E2E") != "1" {
		t.Skip("set CODEXWEB_E2E=1 to run real-binary e2e")
		return false
	}
	return true
}

func e2eSetup(t *testing.T) (bin, home, workDir string) {
	t.Helper()
	return e2eSetupBase(t, "http://127.0.0.1:1/v1", nil)
}

// e2eMockProvider 启动 Phase 0 的本地 mock Responses provider（stdlib 脚本，
// 只控制上游模型行为），返回其 base_url。
func e2eMockProvider(t *testing.T) string {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "codex-web-phase0", "mock_provider.py"))
	if err != nil {
		t.Fatal(err)
	}
	var outBuf strings.Builder
	cmd := exec.Command("python3", "-u", script, "0")
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mock provider: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		// 脚本打印实际监听端口（纯数字一行；取首个纯数字 token，容忍 stderr 交错）
		for _, field := range strings.Fields(outBuf.String()) {
			if _, err := strconv.Atoi(field); err == nil {
				return "http://127.0.0.1:" + field + "/v1"
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("mock provider 未就绪：%s", outBuf.String())
	return ""
}

// e2eSetupBase 是 e2e 环境组装：短路径 CODEX_HOME + standalone 种子 + mockpi 配置。
// providerBaseURL 指向不可达端口（lifecycle 用）或本地 mock provider（history/turn 用）。
func e2eSetupBase(t *testing.T, providerBaseURL string, _ []string) (bin, home, workDir string) {
	t.Helper()
	bin, err := ResolveCodexBinary()
	if err != nil {
		t.Fatalf("resolve codex: %v", err)
	}
	home = fmt.Sprintf("/tmp/cw-e2e-home-%d", os.Getpid())
	workDir = fmt.Sprintf("/tmp/cw-e2e-ws-%d", os.Getpid())
	for _, d := range []string{home, workDir} {
		if err := os.RemoveAll(d); err != nil {
			t.Fatalf("clean %s: %v", d, err)
		}
	}
	cur := filepath.Join(home, "packages", "standalone", "current")
	if err := os.MkdirAll(cur, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(cur, "codex")); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`model = "mock-model"
model_provider = "mockpi"

[model_providers.mockpi]
name = "Mock"
base_url = %q
wire_api = "responses"
`, providerBaseURL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// daemon stop 必须带 CODEX_HOME：stop 面向的是该 home 的 control socket，
		// 不带 env 会打到用户默认 ~/.codex daemon（绝不触碰）。
		stop := exec.Command(bin, "app-server", "daemon", "stop")
		stop.Env = append(os.Environ(), "CODEX_HOME="+home)
		_ = stop.Run()
		if daemonRunning(t, bin, home) {
			// stop 失败兜底：回收持有本测试 home 文件的残留 daemon（仅限 cw-e2e 前缀；
			// 用 lsof 判归属，绝不按进程名误杀用户 daemon）
			out, _ := exec.Command("pgrep", "-f", "app-server --listen unix://").Output()
			for _, id := range strings.Fields(string(out)) {
				files, _ := exec.Command("lsof", "-p", id).Output()
				if !strings.Contains(string(files), home) {
					continue
				}
				_ = exec.Command("kill", id).Run()
			}
		}
		_ = os.RemoveAll(home)
		_ = os.RemoveAll(workDir)
	})
	_ = exec.Command(bin, "app-server", "daemon", "stop").Run()
	return bin, home, workDir
}

func daemonRunning(t *testing.T, bin, home string) bool {
	t.Helper()
	cmd := exec.Command(bin, "app-server", "daemon", "version")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	out, _ := cmd.CombinedOutput()
	return strings.Contains(string(out), `"status":"running"`)
}

func TestE2ECordCodeStartedDaemon(t *testing.T) {
	if !e2eEnabled(t) {
		return
	}
	bin, home, workDir := e2eSetup(t)

	ep, err := Probe(ProbeOptions{CodexHome: home, WorkDir: workDir})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if ep.Source != SourceCordCodeStartedDaemon || !ep.StartedByCordCode {
		t.Fatalf("source=%s started=%v，期望 cordcode-started-daemon（daemon 缺失时自启）", ep.Source, ep.StartedByCordCode)
	}
	if ep.CLIVersion == "" || ep.CodexHome == "" {
		t.Fatalf("initialize 信息缺失：%+v", ep)
	}
	if ep.Client() == nil {
		t.Fatal("六步就绪后必须持有 Client")
	}

	// §6.3：Close 不停共享 daemon（即使由 CordCode 拉起）
	if err := ep.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	socket, _ := ControlSocketPath(home)
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("Close 后 control socket 不应消失（共享 daemon 不停）: %v", err)
	}
	if !daemonRunning(t, bin, home) {
		t.Fatal("Close 后 daemon 应仍在运行（§6.3 不 stop/restart 共享 daemon）")
	}
}

func TestE2EManagedLoopbackFallback(t *testing.T) {
	if !e2eEnabled(t) {
		return
	}
	_, home, workDir := e2eSetup(t)

	deps := DefaultDeps()
	deps.RunDaemonStart = func(string, string) (string, error) {
		return "", fmt.Errorf("simulated: managed standalone install not found")
	}
	ep, err := ProbeWith(deps, ProbeOptions{CodexHome: home, WorkDir: workDir})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if ep.Source != SourceManagedLoopbackWS {
		t.Fatalf("source=%s，期望 managed-loopback-ws", ep.Source)
	}
	healthURL := strings.Replace(ep.TCPEndpoint, "ws://", "http://", 1) + "/healthz"
	if resp, err := http.Get(healthURL); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("托管 WS healthz 应为 200：%v", err)
	} else {
		resp.Body.Close()
	}

	// 托管进程独占回收
	if err := ep.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := http.Get(healthURL); err != nil {
			return // 端口已关 → 回收完成
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("Close 后托管 app-server 端口应关闭（独占回收）")
}
