package gobridge

// projection_codexweb_e2e_test.go —— p2-ssv2-regression：真实官方 app-server 上的
// codex-web 冷 hydrate dispatch 全链路：
//   1. 真实 daemon + mock provider（仅上游）+ 官方 thread/completed turn；
//   2. prepareProjectionHydrateSource → pathless（Identity=官方 thread id，无文件路径）；
//   3. produceProjectionHydrateRange → 官方 turn identity 的 projection 事件
//      （user_message.turnId == turn/start 返回的官方 turn id）+ 唯一终态（§9.2）；
//   4. 重建一致性：同一官方历史两次重建事件流一致（无本地 cache 参与——Agent 只持
//      官方连接，删本地 projection 后仅靠官方 API 即可重建）。
//
// 门控 CODEXWEB_E2E=1（与 agent/codex-web e2e 相同开关）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/codex-web"
)

func codexWebE2EEnabled(t *testing.T) bool {
	if os.Getenv("CODEXWEB_E2E") != "1" {
		t.Skip("set CODEXWEB_E2E=1 to run real-binary codex-web e2e")
		return false
	}
	return true
}

func TestCodexWebRealColdHydrateDispatchE2E(t *testing.T) {
	if !codexWebE2EEnabled(t) {
		return
	}
	ctx := context.Background()

	// mock provider（仅上游模型行为；app-server 全为官方二进制）
	script := findRepoPath(t, filepath.Join("scripts", "codex-web-phase0", "mock_provider.py"))
	outBuf := &strings.Builder{}
	pc := exec.Command("python3", "-u", script, "0")
	pc.Stdout = outBuf
	pc.Stderr = outBuf
	if err := pc.Start(); err != nil {
		t.Fatalf("mock provider: %v", err)
	}
	t.Cleanup(func() { _ = pc.Process.Kill() })
	providerURL := ""
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, f := range strings.Fields(outBuf.String()) {
			if _, err := fmt.Sscanf(f, "%d", new(int)); err == nil {
				providerURL = "http://127.0.0.1:" + f + "/v1"
				break
			}
		}
		if providerURL != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if providerURL == "" {
		t.Fatalf("mock provider 未就绪：%s", outBuf.String())
	}

	// 隔离 CODEX_HOME（短路径 + standalone 种子）；二进制解析复用 lifecycle 的
	// 官方顺序（env CODEX_WEB_CODEX_BIN → PATH → ChatGPT.app 内嵌）。
	bin, err := codexweb.ResolveCodexBinary()
	if err != nil {
		t.Fatalf("codex binary: %v", err)
	}
	home := fmt.Sprintf("/tmp/cw-ssv2-home-%d", os.Getpid())
	ws := fmt.Sprintf("/tmp/cw-ssv2-ws-%d", os.Getpid())
	for _, d := range []string{home, ws} {
		_ = os.RemoveAll(d)
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
`, providerURL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop := exec.Command(bin, "app-server", "daemon", "stop")
		stop.Env = append(os.Environ(), "CODEX_HOME="+home)
		_ = stop.Run()
		_ = os.RemoveAll(home)
		_ = os.RemoveAll(ws)
	})

	// 驱动连接：官方 thread + 真实完成 turn
	ep, err := codexweb.Probe(codexweb.ProbeOptions{CodexHome: home, WorkDir: ws})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer func() { _ = ep.Close() }()
	cl := ep.Client()
	// 通知通道单读者：由 waitForCodexWebTurnCompleted 独占消费。

	threadRaw, rpcErr, err := cl.RequestContext(ctx, "thread/start", map[string]any{"cwd": ws})
	if err != nil || rpcErr != nil {
		t.Fatalf("thread/start: %v / %v", err, rpcErr)
	}
	var threadResp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(threadRaw, &threadResp)
	threadID := threadResp.Thread.ID

	prompt := "MOCK:STREAM ssv2 cold hydrate dispatch"
	turnRaw, rpcErr, err := cl.RequestContext(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
	})
	if err != nil || rpcErr != nil {
		t.Fatalf("turn/start: %v / %v", err, rpcErr)
	}
	var turnResp struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(turnRaw, &turnResp)
	officialTurnID := turnResp.Turn.ID

	completed := waitForCodexWebTurnCompleted(cl, threadID, officialTurnID, 30*time.Second)
	if !completed {
		t.Fatal("30s 内未收到官方 turn/completed")
	}

	// go-bridge dispatch：真实 codex-web Agent（懒探测同一 daemon）
	agent := codexweb.New(map[string]any{"codex_web_codex_home": home, "work_dir": ws})
	defer func() { _ = agent.Stop() }()
	handlers := NewHandlers()
	handlers.RegisterAgent("codex-web", agent)

	source, err := handlers.prepareProjectionHydrateSource(ctx, "codex-web", threadID, "")
	if err != nil {
		t.Fatalf("prepareProjectionHydrateSource: %v", err)
	}
	if source.Path != "" || len(source.Segments) != 0 || source.Cursor != 0 {
		t.Fatalf("真实源必须 pathless：%+v", source)
	}
	if source.Identity != threadID {
		t.Fatalf("identity 应为官方 thread id：%q", source.Identity)
	}

	collect := func() []projectionHydrateEvent {
		var events []projectionHydrateEvent
		if err := handlers.produceProjectionHydrateRange(ctx, "codex-web", threadID, "", 0, 0, SessionProjection{}, func(ev projectionHydrateEvent) bool {
			events = append(events, ev)
			return true
		}); err != nil {
			t.Fatalf("produceProjectionHydrateRange: %v", err)
		}
		return events
	}
	first := collect()

	var sawUser, sawTerminal bool
	var terminalEvent string
	for _, ev := range first {
		if ev.Event == "user_message" {
			sawUser = true
			if ev.Data["turnId"] != officialTurnID || ev.Data["itemId"] == "" {
				t.Fatalf("user_message 必须用官方 identity：%v", ev.Data)
			}
			if ev.Data["text"] != prompt {
				t.Fatalf("user 文本不符：%v", ev.Data["text"])
			}
		}
		if ev.TurnDone {
			if sawTerminal {
				t.Fatal("一个官方 completed turn 恰好一个终态事件")
			}
			sawTerminal = true
			terminalEvent = ev.Event
		}
	}
	if !sawUser || !sawTerminal || terminalEvent != "turn_completed" {
		t.Fatalf("冷基线缺 user/官方终态：user=%v terminal=%q", sawUser, terminalEvent)
	}
	var deltaCount int
	for _, ev := range first {
		if ev.Event == "text_delta" {
			deltaCount++
		}
	}
	if deltaCount == 0 {
		t.Fatal("agentMessage 冷基线正文缺失")
	}

	// 重建一致性（删本地 projection 后仅靠官方 API 重建同一基线）
	second := collect()
	if len(first) != len(second) {
		t.Fatalf("重建事件数不一致：%d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Event != second[i].Event || first[i].TurnDone != second[i].TurnDone {
			t.Fatalf("重建事件 %d 漂移：%v vs %v", i, first[i], second[i])
		}
	}
}

func waitForCodexWebTurnCompleted(cl *codexweb.Client, threadID, turnID string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case n := <-cl.Notifications():
			if n.Method != "turn/completed" {
				continue
			}
			var p struct {
				ThreadID string `json:"threadId"`
				Turn     struct {
					ID string `json:"id"`
				} `json:"turn"`
			}
			_ = json.Unmarshal(n.Params, &p)
			if p.ThreadID == threadID && p.Turn.ID == turnID {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func findRepoPath(t *testing.T, rel string) string {
	t.Helper()
	// go-bridge 测试 cwd = go-bridge/；仓库根在上级
	abs, err := filepath.Abs(filepath.Join("..", rel))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("repo path %s: %v", abs, err)
	}
	return abs
}
