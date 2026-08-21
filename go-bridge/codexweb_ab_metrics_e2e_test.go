package gobridge

// codexweb_ab_metrics_e2e_test.go —— p3-stream-regression：§13.2 帧级 A/B 对照。
//
// 同机、同 mock provider（上游恒定，排除 provider 单帧干扰）、同模型（mock-model）、
// 同 prompt（MOCK:STREAM×N）下分别测量：
//   A. codex-web（官方 daemon，agent/codex-web）
//   B. 旧 codex（app_server 模式，独立 CODEX_HOME 的官方 app-server ws 实例）
// 指标：send→turn/started、send→首 delta、delta 数/平均/最大字符、最大相邻间隔、
// 完成延迟。报告落盘 docs/2026-08-21-codex-web-ab-frame-metrics.md。
//
// 门控 CODEXWEB_AB=1（依赖真实 codex 二进制与网络栈）。

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/agent/codex"
	codexweb "github.com/openAgi2/cordcode-macbridge/agent/codex-web"
	"github.com/openAgi2/cordcode-macbridge/core"
)

const abTurns = 5

type abTurnRecord struct {
	SendToStartedMs  int64
	SendToFirstMs    int64
	DeltaCount       int
	AvgDeltaChars    int
	MaxDeltaChars    int
	MaxGapMs         int64
	CompleteLatencyMs int64
}

func TestCodexWebABFrameMetrics(t *testing.T) {
	if os.Getenv("CODEXWEB_AB") != "1" {
		t.Skip("set CODEXWEB_AB=1 to run the A/B frame-metrics comparison")
		return
	}
	ctx := context.Background()
	providerURL := startABMockProvider(t)
	bin := resolveABCodexBin(t)

	// A: codex-web
	webRecords := runCodexWebSide(t, ctx, bin, providerURL)
	// B: 旧 codex（app_server 模式，独立 ws app-server）
	legacyRecords := runLegacyCodexSide(t, ctx, bin, providerURL)

	if len(webRecords) != abTurns || len(legacyRecords) != abTurns {
		t.Fatalf("turn 数不足：web=%d legacy=%d", len(webRecords), len(legacyRecords))
	}
	report := renderABReport(webRecords, legacyRecords)
	outPath := filepath.Join("..", "docs", "2026-08-21-codex-web-ab-frame-metrics.md")
	if err := os.WriteFile(outPath, []byte(report), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	t.Logf("A/B 报告已落盘 %s", outPath)
}

func resolveABCodexBin(t *testing.T) string {
	t.Helper()
	bin, err := codexweb.ResolveCodexBinary()
	if err != nil {
		t.Fatalf("codex binary: %v", err)
	}
	return bin
}

func startABMockProvider(t *testing.T) string {
	t.Helper()
	script := findRepoPath(t, filepath.Join("scripts", "codex-web-phase0", "mock_provider.py"))
	out := &strings.Builder{}
	pc := exec.Command("python3", "-u", script, "0")
	pc.Stdout = out
	pc.Stderr = out
	if err := pc.Start(); err != nil {
		t.Fatalf("mock provider: %v", err)
	}
	t.Cleanup(func() { _ = pc.Process.Kill() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, f := range strings.Fields(out.String()) {
			var port int
			if _, err := fmt.Sscanf(f, "%d", &port); err == nil && port > 0 {
				return fmt.Sprintf("http://127.0.0.1:%d/v1", port)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("mock provider 未就绪: %s", out.String())
	return ""
}

func abSeedHome(t *testing.T, bin, name, providerURL string) (home, ws string) {
	t.Helper()
	home = fmt.Sprintf("/tmp/cw-ab-%s-home-%d", name, os.Getpid())
	ws = fmt.Sprintf("/tmp/cw-ab-%s-ws-%d", name, os.Getpid())
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
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Cleanup(func() { _ = os.RemoveAll(ws) })
	return home, ws
}

// runCodexWebSide：A 侧（codex-web + 官方 daemon），使用 §13.2 内建指标。
func runCodexWebSide(t *testing.T, ctx context.Context, bin, providerURL string) []abTurnRecord {
	home, ws := abSeedHome(t, bin, "web", providerURL)
	stop := startABDaemon(t, bin, home) // 显式 daemon（与产品 §6.1 路径 2/3 一致）
	t.Cleanup(stop)

	agent := codexweb.New(map[string]any{"codex_web_codex_home": home, "work_dir": ws})
	defer func() { _ = agent.Stop() }()
	sess, err := agent.StartSession(ctx, "")
	if err != nil {
		t.Fatalf("web StartSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	// 单读者：waitABTerminal 独占消费事件通道（metrics 由中央泵侧记录）
	for i := 0; i < abTurns; i++ {
		if err := sess.Send(fmt.Sprintf("MOCK:STREAM ab web turn %d", i), nil, nil); err != nil {
			t.Fatalf("web Send: %v", err)
		}
		if err := waitABTerminal(sess.Events()); err != nil {
			t.Fatalf("web turn %d: %v", i, err)
		}
	}
	_ = sess.Close()

	snap := agent.MetricsSnapshot()
	out := make([]abTurnRecord, 0, len(snap))
	for _, m := range snap {
		if m.DeltaCount == 0 || m.SendAt.IsZero() || m.CompletedAt.IsZero() {
			continue
		}
		out = append(out, abTurnRecord{
			SendToStartedMs:   m.SendToStarted().Milliseconds(),
			SendToFirstMs:     m.SendToFirstDelta().Milliseconds(),
			DeltaCount:        m.DeltaCount,
			AvgDeltaChars:     m.DeltaChars / m.DeltaCount,
			MaxDeltaChars:     m.MaxDeltaChars,
			MaxGapMs:          m.MaxInterDelta.Milliseconds(),
			CompleteLatencyMs: m.TurnLatency().Milliseconds(),
		})
	}
	return out
}

// runLegacyCodexSide：B 侧（旧 codex app_server 模式，独立官方 ws app-server）。
func runLegacyCodexSide(t *testing.T, ctx context.Context, bin, providerURL string) []abTurnRecord {
	home, ws := abSeedHome(t, bin, "legacy", providerURL)
	// 独立 ws app-server（不与 A 侧共享实例——两 backend 并存规则 §10.1）
	port := freeABPort(t)
	srv := exec.Command(bin, "app-server", "--listen", fmt.Sprintf("ws://127.0.0.1:%d", port))
	srv.Env = append(os.Environ(), "CODEX_HOME="+home)
	if err := srv.Start(); err != nil {
		t.Fatalf("legacy app-server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill() })
	waitABPort(t, port)

	agent, err := codex.New(map[string]any{
		"work_dir":        ws,
		"mode":            "custom",
		"backend":         "app_server",
		"app_server_url":  fmt.Sprintf("ws://127.0.0.1:%d", port),
		"app_server_url_set": true,
		"codex_home":      home,
	})
	if err != nil {
		t.Fatalf("legacy New: %v", err)
	}
	defer func() { _ = agent.Stop() }()
	sess, err := agent.StartSession(ctx, "")
	if err != nil {
		t.Fatalf("legacy StartSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	var out []abTurnRecord
	for i := 0; i < abTurns; i++ {
		rec := measureLegacyTurn(t, sess, fmt.Sprintf("MOCK:STREAM ab legacy turn %d", i))
		out = append(out, rec)
	}
	return out
}

// measureLegacyTurn 用 core.Event 时间线测量旧 backend 一轮（与 A 侧同口径）。
func measureLegacyTurn(t *testing.T, sess core.AgentSession, prompt string) abTurnRecord {
	t.Helper()
	sendAt := time.Now()
	var startedAt, firstDelta, completedAt time.Time
	var count, chars, maxChars int
	var lastDelta time.Time
	var maxGap time.Duration

	if err := sess.Send(prompt, nil, nil); err != nil {
		t.Fatalf("legacy Send: %v", err)
	}
	deadline := time.After(60 * time.Second)
	for {
		select {
		case ev, ok := <-sess.Events():
			if !ok {
				t.Fatal("legacy 事件通道提前关闭")
			}
			now := time.Now()
			switch {
			case ev.Type == core.EventTurnStarted && startedAt.IsZero():
				startedAt = now
			case ev.Type == core.EventText:
				count++
				chars += len(ev.Content)
				if len(ev.Content) > maxChars {
					maxChars = len(ev.Content)
				}
				if firstDelta.IsZero() {
					firstDelta = now
				}
				if !lastDelta.IsZero() {
					if g := now.Sub(lastDelta); g > maxGap {
						maxGap = g
					}
				}
				lastDelta = now
			case (ev.Type == core.EventResult || ev.Type == core.EventError) && ev.Done:
				completedAt = now
				rec := abTurnRecord{DeltaCount: count, AvgDeltaChars: 0, MaxDeltaChars: maxChars, MaxGapMs: maxGap.Milliseconds()}
				if !startedAt.IsZero() {
					rec.SendToStartedMs = startedAt.Sub(sendAt).Milliseconds()
				}
				if !firstDelta.IsZero() {
					rec.SendToFirstMs = firstDelta.Sub(sendAt).Milliseconds()
				}
				if count > 0 {
					rec.AvgDeltaChars = chars / count
				}
				rec.CompleteLatencyMs = completedAt.Sub(sendAt).Milliseconds()
				return rec
			}
		case <-deadline:
			t.Fatal("legacy turn 60s 未完成")
		}
	}
}

func waitABTerminal(ch <-chan core.Event) error {
	deadline := time.After(60 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return fmt.Errorf("事件通道关闭")
			}
			if (ev.Type == core.EventResult || ev.Type == core.EventError) && ev.Done {
				return nil
			}
		case <-deadline:
			return fmt.Errorf("60s 未完成")
		}
	}
}

func startABDaemon(t *testing.T, bin, home string) func() {
	t.Helper()
	cmd := exec.Command(bin, "app-server", "daemon", "start")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("daemon start: %v (%s)", err, out)
	}
	return func() {
		stop := exec.Command(bin, "app-server", "daemon", "stop")
		stop.Env = append(os.Environ(), "CODEX_HOME="+home)
		_ = stop.Run()
	}
}

func freeABPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func waitABPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("legacy app-server 端口未就绪")
}

func renderABReport(web, legacy []abTurnRecord) string {
	avg := func(rs []abTurnRecord, f func(abTurnRecord) int64) int64 {
		var sum int64
		for _, r := range rs {
			sum += f(r)
		}
		if len(rs) == 0 {
			return 0
		}
		return sum / int64(len(rs))
	}
	var b strings.Builder
	b.WriteString("# codex-web vs 旧 codex 帧级 A/B 对照（§13.2）\n\n")
	b.WriteString("采集时间：" + time.Now().Format("2006-01-02 15:04:05 MST") + "\n\n")
	b.WriteString("条件：同机；上游 mock Responses provider（排除 provider 单帧差异）；模型 mock-model；")
	b.WriteString(fmt.Sprintf("每侧 %d 轮同型 prompt（MOCK:STREAM）；A=官方 daemon 路径，B=旧 backend app_server 模式（独立官方 ws app-server 实例，§10.1 并存规则）。\n\n", abTurns))
	b.WriteString("| 指标 | codex-web 均值 | 旧 codex 均值 |\n|---|---:|---:|\n")
	rows := []struct {
		name string
		f    func(abTurnRecord) int64
	}{
		{"send → turn/started (ms)", func(r abTurnRecord) int64 { return r.SendToStartedMs }},
		{"send → 首 delta (ms)", func(r abTurnRecord) int64 { return r.SendToFirstMs }},
		{"每轮 delta 数", func(r abTurnRecord) int64 { return int64(r.DeltaCount) }},
		{"delta 平均字符", func(r abTurnRecord) int64 { return int64(r.AvgDeltaChars) }},
		{"delta 最大字符", func(r abTurnRecord) int64 { return int64(r.MaxDeltaChars) }},
		{"相邻 delta 最大间隔 (ms)", func(r abTurnRecord) int64 { return r.MaxGapMs }},
		{"完成延迟 (ms)", func(r abTurnRecord) int64 { return r.CompleteLatencyMs }},
	}
	for _, row := range rows {
		b.WriteString(fmt.Sprintf("| %s | %d | %d |\n", row.name, avg(web, row.f), avg(legacy, row.f)))
	}
	b.WriteString("\n逐轮明细（A=codex-web / B=旧 codex）：\n\n")
	b.WriteString("| # | A started | A first | A deltas | A gap | A done | B started | B first | B deltas | B gap | B done |\n")
	b.WriteString("|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for i := 0; i < len(web) && i < len(legacy); i++ {
		a, bb := web[i], legacy[i]
		b.WriteString(fmt.Sprintf("| %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n",
			i+1, a.SendToStartedMs, a.SendToFirstMs, a.DeltaCount, a.MaxGapMs, a.CompleteLatencyMs,
			bb.SendToStartedMs, bb.SendToFirstMs, bb.DeltaCount, bb.MaxGapMs, bb.CompleteLatencyMs))
	}
	b.WriteString("\n结论边界：上游恒定（mock 单响应多 delta），两侧差异反映 transport/adapter 路径；")
	b.WriteString("Bridge 33ms batching 与 iOS render cadence 不在本测量内（§8.1 分层）。\n")
	return b.String()
}
