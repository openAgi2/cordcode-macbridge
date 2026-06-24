package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	cdxDiagStatusRunning = "running"
	cdxDiagStatusPassed  = "passed"
	cdxDiagStatusFailed  = "failed"
	cdxDiagStatusWarning = "warning"
)

func (a *Agent) RunDiagnostics(ctx context.Context, progress func(core.DiagnosticProgress)) (*core.DiagnosticReport, error) {
	var results []core.DiagnosticResult

	runCheck := func(id, name, severity string, fn func(context.Context) core.DiagnosticResult) {
		emitCdxProgress(progress, id, cdxDiagStatusRunning, name)
		r := fn(ctx)
		r.ID = id
		r.Name = name
		r.Severity = severity
		results = append(results, r)
		emitCdxProgress(progress, id, r.Status, r.Message)
	}

	runCheck("cli", "Codex CLI 可用性", "required", a.diagCLI)
	runCheck("auth", "认证配置", "required", a.diagAuth)
	runCheck("workdir", "工作目录", "required", a.diagWorkDir)

	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()

	if backend == "app_server" {
		runCheck("app_server", "app-server 可达性", "recommended", a.diagAppServer)
	}

	return &core.DiagnosticReport{
		Results:       results,
		OverallStatus: summarizeCdxOverallStatus(results),
	}, nil
}

func (a *Agent) diagCLI(_ context.Context) core.DiagnosticResult {
	a.mu.RLock()
	cli := a.cliBin
	a.mu.RUnlock()

	if cli == "" {
		cli = "codex"
	}

	resolved, err := exec.LookPath(cli)
	if err != nil {
		return core.DiagnosticResult{
			Status:        cdxDiagStatusFailed,
			Message:       fmt.Sprintf("PATH 中找不到 Codex CLI：%s", cli),
			FixSuggestion: "安装 Codex CLI：npm install -g @openai/codex",
		}
	}

	return core.DiagnosticResult{
		Status:  cdxDiagStatusPassed,
		Message: fmt.Sprintf("Codex CLI 已找到：%s", resolved),
	}
}

func (a *Agent) diagAuth(_ context.Context) core.DiagnosticResult {
	a.mu.RLock()
	home := a.codexHome
	a.mu.RUnlock()

	var path string
	if home != "" {
		path = filepath.Join(home, "auth.json")
	} else {
		var err error
		path, err = codexAuthPath()
		if err != nil {
			return core.DiagnosticResult{
				Status:        cdxDiagStatusFailed,
				Message:       fmt.Sprintf("无法解析认证文件路径：%v", err),
				FixSuggestion: "确认 CODEX_HOME 或 HOME 环境变量正确",
			}
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return core.DiagnosticResult{
			Status:        cdxDiagStatusFailed,
			Message:       "认证文件不存在或不可读",
			FixSuggestion: "运行 codex 登录以生成认证文件",
		}
	}

	var payload struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if err := parseJSON(data, &payload); err != nil || strings.TrimSpace(payload.Tokens.AccessToken) == "" {
		return core.DiagnosticResult{
			Status:        cdxDiagStatusFailed,
			Message:       "认证文件格式异常或缺少 access_token",
			FixSuggestion: "重新运行 codex 登录",
		}
	}

	return core.DiagnosticResult{
		Status:  cdxDiagStatusPassed,
		Message: "认证文件存在且包含 token",
	}
}

func (a *Agent) diagWorkDir(_ context.Context) core.DiagnosticResult {
	a.mu.RLock()
	dir := a.workDir
	a.mu.RUnlock()

	if dir == "" || dir == "." {
		return core.DiagnosticResult{
			Status:  cdxDiagStatusWarning,
			Message: "使用当前目录作为工作目录",
		}
	}

	info, err := os.Stat(dir)
	if err != nil {
		return core.DiagnosticResult{
			Status:        cdxDiagStatusFailed,
			Message:       fmt.Sprintf("工作目录不存在：%s", dir),
			FixSuggestion: "确认 -work-dir 路径正确",
		}
	}
	if !info.IsDir() {
		return core.DiagnosticResult{
			Status:        cdxDiagStatusFailed,
			Message:       fmt.Sprintf("工作目录不是目录：%s", dir),
			FixSuggestion: "确认 -work-dir 指向一个目录",
		}
	}

	return core.DiagnosticResult{
		Status:  cdxDiagStatusPassed,
		Message: fmt.Sprintf("工作目录存在：%s", dir),
	}
}

func (a *Agent) diagAppServer(ctx context.Context) core.DiagnosticResult {
	a.mu.RLock()
	url := a.appServerURL
	a.mu.RUnlock()

	if url == "" {
		return core.DiagnosticResult{
			Status:  cdxDiagStatusWarning,
			Message: "未配置 app-server URL",
		}
	}

	// app-server is WebSocket, do a simple HTTP GET to check connectivity
	httpURL := url
	if strings.HasPrefix(httpURL, "ws://") {
		httpURL = "http://" + strings.TrimPrefix(httpURL, "ws://")
	} else if strings.HasPrefix(httpURL, "wss://") {
		httpURL = "https://" + strings.TrimPrefix(httpURL, "wss://")
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, httpURL, nil)
	if err != nil {
		return core.DiagnosticResult{
			Status:        cdxDiagStatusFailed,
			Message:       fmt.Sprintf("构造请求失败：%v", err),
			FixSuggestion: "检查 -codex-app-server-url 是否正确",
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return core.DiagnosticResult{
			Status:        cdxDiagStatusFailed,
			Message:       fmt.Sprintf("无法连接 app-server (%s)", url),
			FixSuggestion: "确认 Codex app-server 正在运行",
		}
	}
	resp.Body.Close()

	return core.DiagnosticResult{
		Status:  cdxDiagStatusPassed,
		Message: fmt.Sprintf("app-server 可达：%s", url),
	}
}

func emitCdxProgress(progress func(core.DiagnosticProgress), checkID, status, message string) {
	if progress == nil {
		return
	}
	progress(core.DiagnosticProgress{
		CheckID: checkID,
		Status:  status,
		Message: message,
	})
}

func summarizeCdxOverallStatus(results []core.DiagnosticResult) string {
	hasFailed := false
	hasWarning := false
	for _, r := range results {
		if r.Status == cdxDiagStatusFailed && r.Severity == "required" {
			return cdxDiagStatusFailed
		}
		if r.Status == cdxDiagStatusFailed {
			hasFailed = true
		}
		if r.Status == cdxDiagStatusWarning {
			hasWarning = true
		}
	}
	if hasFailed {
		return cdxDiagStatusWarning
	}
	if hasWarning {
		return cdxDiagStatusWarning
	}
	return cdxDiagStatusPassed
}

func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

var _ core.DiagnosticsProvider = (*Agent)(nil)
