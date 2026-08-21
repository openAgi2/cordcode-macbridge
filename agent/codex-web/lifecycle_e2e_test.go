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
	cfg := `model = "mock-model"
model_provider = "mockpi"

[model_providers.mockpi]
name = "Mock"
base_url = "http://127.0.0.1:1/v1"
wire_api = "responses"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(bin, "app-server", "daemon", "stop").Run()
		if daemonRunning(t, bin, home) {
			// stop 失败兜底：按 home 路径定位本测试拉起的进程回收（仅限 cw-e2e 前缀目录）
			out, _ := exec.Command("pgrep", "-f", home).Output()
			for _, id := range strings.Fields(string(out)) {
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
