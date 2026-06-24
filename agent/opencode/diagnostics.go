package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	ocDiagStatusRunning = "running"
	ocDiagStatusPassed  = "passed"
	ocDiagStatusFailed  = "failed"
	ocDiagStatusWarning = "warning"
)

func (a *Agent) RunDiagnostics(ctx context.Context, progress func(core.DiagnosticProgress)) (*core.DiagnosticReport, error) {
	var results []core.DiagnosticResult

	runCheck := func(id, name, severity string, fn func(context.Context) core.DiagnosticResult) {
		emitDiagProgress(progress, id, ocDiagStatusRunning, name)
		r := fn(ctx)
		r.ID = id
		r.Name = name
		r.Severity = severity
		results = append(results, r)
		emitDiagProgress(progress, id, r.Status, r.Message)
	}

	runCheck("server", "OpenCode server 可达性", "required", a.diagServerConnectivity)
	runCheck("workdir", "工作目录", "required", a.diagWorkDir)
	runCheck("models", "模型端点", "recommended", a.diagModelsEndpoint)
	runCheck("cli", "OpenCode CLI 可用性", "optional", a.diagCLI)

	return &core.DiagnosticReport{
		Results:       results,
		OverallStatus: summarizeDiagOverallStatus(results),
	}, nil
}

func (a *Agent) diagServerConnectivity(ctx context.Context) core.DiagnosticResult {
	a.mu.RLock()
	baseURL := a.httpBaseURL
	authHeader := a.httpAuthHeader
	a.mu.RUnlock()

	if baseURL == "" {
		return core.DiagnosticResult{
			Status:        ocDiagStatusFailed,
			Message:       "未配置 OpenCode server URL",
			FixSuggestion: "设置 -opencode-url 参数或 OPENCODE_BASE_URL 环境变量",
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, baseURL+"/agent", nil)
	if err != nil {
		return core.DiagnosticResult{
			Status:        ocDiagStatusFailed,
			Message:       fmt.Sprintf("构造请求失败：%v", err),
			FixSuggestion: "检查 OpenCode server URL 是否正确",
		}
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return core.DiagnosticResult{
			Status:        ocDiagStatusFailed,
			Message:       fmt.Sprintf("无法连接 OpenCode server (%s)", baseURL),
			FixSuggestion: "确认 OpenCode 正在运行：执行 opencode server 或检查端口监听",
		}
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return core.DiagnosticResult{
			Status:        ocDiagStatusFailed,
			Message:       "认证失败：server 返回 401/403",
			FixSuggestion: "检查 -opencode-user / -opencode-pass 是否正确",
		}
	}

	if resp.StatusCode >= 400 {
		return core.DiagnosticResult{
			Status:  ocDiagStatusWarning,
			Message: fmt.Sprintf("server 返回 HTTP %d，但连接成功", resp.StatusCode),
		}
	}

	return core.DiagnosticResult{
		Status:  ocDiagStatusPassed,
		Message: fmt.Sprintf("OpenCode server 可达：%s", baseURL),
	}
}

func (a *Agent) diagWorkDir(_ context.Context) core.DiagnosticResult {
	a.mu.RLock()
	dir := a.workDir
	a.mu.RUnlock()

	if dir == "" || dir == "." {
		return core.DiagnosticResult{
			Status:  ocDiagStatusWarning,
			Message: "使用当前目录作为工作目录",
		}
	}

	info, err := os.Stat(dir)
	if err != nil {
		return core.DiagnosticResult{
			Status:        ocDiagStatusFailed,
			Message:       fmt.Sprintf("工作目录不存在：%s", dir),
			FixSuggestion: "确认 -work-dir 路径正确",
		}
	}
	if !info.IsDir() {
		return core.DiagnosticResult{
			Status:        ocDiagStatusFailed,
			Message:       fmt.Sprintf("工作目录不是目录：%s", dir),
			FixSuggestion: "确认 -work-dir 指向一个目录",
		}
	}

	return core.DiagnosticResult{
		Status:  ocDiagStatusPassed,
		Message: fmt.Sprintf("工作目录存在：%s", dir),
	}
}

func (a *Agent) diagModelsEndpoint(ctx context.Context) core.DiagnosticResult {
	raw, err := a.fetchJSON(ctx, "/global/config")
	if err != nil {
		return core.DiagnosticResult{
			Status:        ocDiagStatusFailed,
			Message:       fmt.Sprintf("模型端点不可用：%v", err),
			FixSuggestion: "确认 OpenCode server 正在运行且有模型配置",
		}
	}

	var data map[string]interface{}
	if err := parseJSONRaw(raw, &data); err != nil {
		return core.DiagnosticResult{
			Status:  ocDiagStatusWarning,
			Message: "模型配置响应格式异常",
		}
	}

	providers, _ := data["providers"].(map[string]interface{})
	if providers == nil {
		providers, _ = data["provider"].(map[string]interface{})
	}
	if len(providers) == 0 {
		return core.DiagnosticResult{
			Status:  ocDiagStatusWarning,
			Message: "未发现可用模型配置",
		}
	}

	count := 0
	for _, provVal := range providers {
		provData, _ := provVal.(map[string]interface{})
		if provData == nil {
			continue
		}
		models, _ := provData["models"].(map[string]interface{})
		count += len(models)
	}

	return core.DiagnosticResult{
		Status:  ocDiagStatusPassed,
		Message: fmt.Sprintf("模型端点正常，发现 %d 个模型", count),
	}
}

func (a *Agent) diagCLI(_ context.Context) core.DiagnosticResult {
	a.mu.RLock()
	cmd := a.cmd
	a.mu.RUnlock()

	if cmd == "" {
		cmd = "opencode"
	}

	resolved, err := exec.LookPath(cmd)
	if err != nil {
		return core.DiagnosticResult{
			Status:        ocDiagStatusWarning,
			Message:       fmt.Sprintf("PATH 中找不到 OpenCode CLI：%s", cmd),
			FixSuggestion: "OpenCode CLI 非必需（server 模式下），但可用于调试",
		}
	}

	return core.DiagnosticResult{
		Status:  ocDiagStatusPassed,
		Message: fmt.Sprintf("OpenCode CLI 已找到：%s", resolved),
	}
}

func emitDiagProgress(progress func(core.DiagnosticProgress), checkID, status, message string) {
	if progress == nil {
		return
	}
	progress(core.DiagnosticProgress{
		CheckID: checkID,
		Status:  status,
		Message: message,
	})
}

func summarizeDiagOverallStatus(results []core.DiagnosticResult) string {
	hasFailed := false
	hasWarning := false
	for _, r := range results {
		if r.Status == ocDiagStatusFailed && r.Severity == "required" {
			return ocDiagStatusFailed
		}
		if r.Status == ocDiagStatusFailed {
			hasFailed = true
		}
		if r.Status == ocDiagStatusWarning {
			hasWarning = true
		}
	}
	if hasFailed {
		return ocDiagStatusWarning
	}
	if hasWarning {
		return ocDiagStatusWarning
	}
	return ocDiagStatusPassed
}

func parseJSONRaw(raw json.RawMessage, v interface{}) error {
	return json.Unmarshal(raw, v)
}

var _ core.DiagnosticsProvider = (*Agent)(nil)
