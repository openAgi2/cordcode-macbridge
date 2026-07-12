package grokbuild

// Diagnostics implementation for the Grok Build driver.
// Reports CLI availability, version compatibility, and session startup.

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	diagStatusRunning = "running"
	diagStatusPassed  = "passed"
	diagStatusFailed  = "failed"
	diagStatusWarning = "warning"
)

// RunDiagnostics implements core.DiagnosticsProvider.
func (a *Agent) RunDiagnostics(ctx context.Context, progress func(core.DiagnosticProgress)) (*core.DiagnosticReport, error) {
	results := make([]core.DiagnosticResult, 0, 2)

	runCheck := func(id, name, severity string, fn func(context.Context) core.DiagnosticResult) {
		if progress != nil {
			progress(core.DiagnosticProgress{CheckID: id, Status: diagStatusRunning})
		}
		r := fn(ctx)
		r.ID = id
		r.Name = name
		r.Severity = severity
		results = append(results, r)
		if progress != nil {
			progress(core.DiagnosticProgress{CheckID: id, Status: r.Status, Message: r.Message})
		}
	}

	runCheck("cli", "Grok CLI 可用性", "required", a.runCLIDiagnostic)
	runCheck("version", "CLI 版本兼容性", "required", a.runVersionDiagnostic)

	return &core.DiagnosticReport{
		Results:       results,
		OverallStatus: summarizeDiagStatus(results),
	}, nil
}

func (a *Agent) runCLIDiagnostic(ctx context.Context) core.DiagnosticResult {
	bin := a.cliBin
	if bin == "" {
		bin = "grok"
	}

	if _, err := exec.LookPath(bin); err != nil {
		return core.DiagnosticResult{
			Status:        diagStatusFailed,
			Message:       fmt.Sprintf("未找到 Grok CLI (%s)。请安装 Grok Build CLI。", bin),
			FixSuggestion: "运行 grok 安装脚本或参考 https://x.ai/cli",
		}
	}

	return core.DiagnosticResult{
		Status:  diagStatusPassed,
		Message: fmt.Sprintf("Grok CLI (%s) 已安装", bin),
	}
}

var versionRegex = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func (a *Agent) runVersionDiagnostic(ctx context.Context) core.DiagnosticResult {
	bin := a.cliBin
	if bin == "" {
		bin = "grok"
	}

	cmd := exec.CommandContext(ctx, bin, "--version")
	output, err := cmd.Output()
	if err != nil {
		return core.DiagnosticResult{
			Status:        diagStatusFailed,
			Message:       fmt.Sprintf("无法获取 Grok CLI 版本: %v", err),
			FixSuggestion: "确认 grok --version 可正常执行",
		}
	}

	versionStr := strings.TrimSpace(string(output))
	matches := versionRegex.FindStringSubmatch(versionStr)
	if matches == nil {
		return core.DiagnosticResult{
			Status:  diagStatusWarning,
			Message: fmt.Sprintf("无法解析版本号: %s", versionStr),
		}
	}

	// Check minimum version: 0.2.93
	major, minor, patch := parseInt(matches[1]), parseInt(matches[2]), parseInt(matches[3])
	if major < 0 || (major == 0 && (minor < 2 || (minor == 2 && patch < 93))) {
		return core.DiagnosticResult{
			Status:        diagStatusFailed,
			Message:       fmt.Sprintf("Grok CLI 版本 %s 低于最低要求 0.2.93", versionStr),
			FixSuggestion: "升级 Grok Build CLI",
		}
	}

	return core.DiagnosticResult{
		Status:  diagStatusPassed,
		Message: fmt.Sprintf("Grok CLI 版本: %s", versionStr),
	}
}

func summarizeDiagStatus(results []core.DiagnosticResult) string {
	for _, r := range results {
		if r.Status == diagStatusFailed && r.Severity == "required" {
			return "unhealthy"
		}
	}
	for _, r := range results {
		if r.Status != diagStatusPassed {
			return "degraded"
		}
	}
	return "healthy"
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
