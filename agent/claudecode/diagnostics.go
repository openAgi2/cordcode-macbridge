package claudecode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	diagnosticStatusRunning = "running"
	diagnosticStatusPassed  = "passed"
	diagnosticStatusFailed  = "failed"
	diagnosticStatusWarning = "warning"
)

func (a *Agent) RunDiagnostics(ctx context.Context, progress func(core.DiagnosticProgress)) (*core.DiagnosticReport, error) {
	results := make([]core.DiagnosticResult, 0, 4)

	runCheck := func(id, name, severity string, fn func(context.Context) core.DiagnosticResult) {
		emitDiagnosticProgress(progress, id, diagnosticStatusRunning, name)
		result := fn(ctx)
		result.ID = id
		result.Name = name
		result.Severity = severity
		results = append(results, result)
		emitDiagnosticProgress(progress, id, result.Status, result.Message)
	}

	runCheck("cli", "Claude CLI 可用性", "required", a.runCLIDiagnostic)
	runCheck("session_start", "会话启动", "required", a.runSessionStartupDiagnostic)
	runCheck("model_query", "模型目录读取", "recommended", a.runModelQueryDiagnostic)
	runCheck("credentials", "凭证配置", "optional", a.runCredentialDiagnostic)

	return &core.DiagnosticReport{
		Results:       results,
		OverallStatus: summarizeDiagnosticOverallStatus(results),
	}, nil
}

func emitDiagnosticProgress(progress func(core.DiagnosticProgress), checkID, status, message string) {
	if progress == nil {
		return
	}
	progress(core.DiagnosticProgress{
		CheckID: checkID,
		Status:  status,
		Message: message,
	})
}

func (a *Agent) runCLIDiagnostic(context.Context) core.DiagnosticResult {
	cli := strings.TrimSpace(a.cliBin)
	if cli == "" {
		cli = "claude"
	}

	if a.spawnOpts.IsolationMode() {
		if filepath.IsAbs(cli) {
			if _, err := os.Stat(cli); err != nil {
				return core.DiagnosticResult{
					Status:        diagnosticStatusFailed,
					Message:       fmt.Sprintf("找不到 Claude CLI：%s", cli),
					FixSuggestion: "确认 cli_path 指向存在的 Claude 可执行文件，或在目标用户环境中安装 Claude CLI。",
				}
			}
		}
		return core.DiagnosticResult{
			Status:  diagnosticStatusPassed,
			Message: "run_as_user 模式下将由目标用户环境解析 Claude CLI。",
		}
	}

	if filepath.IsAbs(cli) {
		if _, err := os.Stat(cli); err != nil {
			return core.DiagnosticResult{
				Status:        diagnosticStatusFailed,
				Message:       fmt.Sprintf("找不到 Claude CLI：%s", cli),
				FixSuggestion: "确认 cli_path 配置正确，或重新安装 Claude CLI。",
			}
		}
		return core.DiagnosticResult{
			Status:  diagnosticStatusPassed,
			Message: fmt.Sprintf("Claude CLI 已找到：%s", cli),
		}
	}

	resolved, err := exec.LookPath(cli)
	if err != nil {
		return core.DiagnosticResult{
			Status:        diagnosticStatusFailed,
			Message:       fmt.Sprintf("PATH 中找不到 Claude CLI：%s", cli),
			FixSuggestion: "请先安装 Claude CLI，并确保它在 PATH 中可见。",
		}
	}
	return core.DiagnosticResult{
		Status:  diagnosticStatusPassed,
		Message: fmt.Sprintf("Claude CLI 已找到：%s", resolved),
	}
}

func (a *Agent) runSessionStartupDiagnostic(ctx context.Context) core.DiagnosticResult {
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	session, err := a.StartSession(probeCtx, "")
	if err != nil {
		return core.DiagnosticResult{
			Status:        diagnosticStatusFailed,
			Message:       fmt.Sprintf("无法启动 Claude 会话：%v", err),
			FixSuggestion: "检查 Claude CLI 登录状态、run_as_user 环境，以及项目目录是否可访问。",
		}
	}
	defer session.Close()

	timer := time.NewTimer(750 * time.Millisecond)
	defer timer.Stop()

	for {
		select {
		case <-probeCtx.Done():
			if session.Alive() {
				return core.DiagnosticResult{
					Status:  diagnosticStatusPassed,
					Message: "Claude 会话已成功启动。",
				}
			}
			return core.DiagnosticResult{
				Status:        diagnosticStatusFailed,
				Message:       "Claude 会话启动超时。",
				FixSuggestion: "确认 Claude CLI 没有卡在首次信任提示、登录提示或系统权限弹窗。",
			}
		case ev, ok := <-session.Events():
			if !ok {
				if session.Alive() {
					return core.DiagnosticResult{
						Status:  diagnosticStatusPassed,
						Message: "Claude 会话已成功启动。",
					}
				}
				return core.DiagnosticResult{
					Status:        diagnosticStatusFailed,
					Message:       "Claude 会话在启动后立即退出。",
					FixSuggestion: "检查 Claude CLI 输出、登录状态和运行目录权限。",
				}
			}
			if ev.Type == core.EventError {
				return core.DiagnosticResult{
					Status:        diagnosticStatusFailed,
					Message:       fmt.Sprintf("Claude 会话启动失败：%v", ev.Error),
					FixSuggestion: "检查 Claude CLI 登录状态、provider 配置和本地网络。",
				}
			}
			return core.DiagnosticResult{
				Status:  diagnosticStatusPassed,
				Message: "Claude 会话已成功启动。",
			}
		case <-timer.C:
			if session.Alive() {
				return core.DiagnosticResult{
					Status:  diagnosticStatusPassed,
					Message: "Claude 会话已成功启动。",
				}
			}
			return core.DiagnosticResult{
				Status:        diagnosticStatusFailed,
				Message:       "Claude 会话在启动后立即退出。",
				FixSuggestion: "检查 Claude CLI 输出、登录状态和运行目录权限。",
			}
		}
	}
}

func (a *Agent) runModelQueryDiagnostic(ctx context.Context) core.DiagnosticResult {
	if !a.hasModelCatalogProbeConfig() {
		return core.DiagnosticResult{
			Status:        diagnosticStatusWarning,
			Message:       "未配置可探测的模型目录凭证，已跳过远端模型查询。",
			FixSuggestion: "如需验证模型目录，请配置 provider API key、router API key，或设置 ANTHROPIC_API_KEY。",
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	models := a.fetchModelsFromAPI(probeCtx)
	if len(models) == 0 {
		return core.DiagnosticResult{
			Status:        diagnosticStatusWarning,
			Message:       "模型目录查询未返回结果。",
			FixSuggestion: "检查 provider/base_url/router 配置、API key，以及到模型服务的网络连通性。",
		}
	}
	return core.DiagnosticResult{
		Status:  diagnosticStatusPassed,
		Message: fmt.Sprintf("模型目录查询成功，共发现 %d 个模型。", len(models)),
	}
}

func (a *Agent) runCredentialDiagnostic(context.Context) core.DiagnosticResult {
	if a.hasCredentialHint() {
		return core.DiagnosticResult{
			Status:  diagnosticStatusPassed,
			Message: "检测到 Claude 认证或 provider/router 配置。",
		}
	}
	return core.DiagnosticResult{
		Status:        diagnosticStatusWarning,
		Message:       "未检测到明确的 Claude 凭证线索。",
		FixSuggestion: "登录 Claude CLI，或配置 provider API key / router API key / ANTHROPIC_API_KEY。",
	}
}

func (a *Agent) hasModelCatalogProbeConfig() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.routerURL != "" && a.routerAPIKey != "" {
		return true
	}
	if a.activeIdx >= 0 && a.activeIdx < len(a.providers) {
		provider := a.providers[a.activeIdx]
		if provider.APIKey != "" {
			return true
		}
	}
	return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != ""
}

func (a *Agent) hasCredentialHint() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.routerAPIKey != "" || a.routerURL != "" {
		return true
	}
	if a.activeIdx >= 0 && a.activeIdx < len(a.providers) {
		provider := a.providers[a.activeIdx]
		if provider.APIKey != "" || provider.BaseURL != "" || len(provider.Env) > 0 {
			return true
		}
	}
	if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != "" || strings.TrimSpace(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")) != "" {
		return true
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		for _, candidate := range []string{
			filepath.Join(homeDir, ".claude.json"),
			filepath.Join(homeDir, ".config", "claude", "credentials.json"),
			filepath.Join(homeDir, ".claude", "credentials.json"),
		} {
			if _, err := os.Stat(candidate); err == nil {
				return true
			}
		}
	}
	return false
}

func summarizeDiagnosticOverallStatus(results []core.DiagnosticResult) string {
	hasRequiredFailure := false
	hasNonPass := false
	for _, result := range results {
		if result.Status == diagnosticStatusFailed && result.Severity == "required" {
			hasRequiredFailure = true
		}
		if result.Status != diagnosticStatusPassed {
			hasNonPass = true
		}
	}
	if hasRequiredFailure {
		return "unhealthy"
	}
	if hasNonPass {
		return "degraded"
	}
	return "healthy"
}

var _ core.DiagnosticsProvider = (*Agent)(nil)
