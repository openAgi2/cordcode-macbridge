package opencodeweb

import (
	"context"
	"fmt"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// diagnostics.go implements core.DiagnosticsProvider (design §4.3.8): the
// settings-page diagnostics report the endpoint source, the detected API
// generation, the health probe, whether the selected/default model is in the
// runtime catalog, loopback-only enforcement, and the §3.4 permission
// folding probe outcomes (实施期挂账：双代探针结果与 1.18 权限字面量先探结果进诊断).

func (a *Agent) RunDiagnostics(ctx context.Context, progress func(core.DiagnosticProgress)) (*core.DiagnosticReport, error) {
	report := &core.DiagnosticReport{Results: []core.DiagnosticResult{}}
	emit := func(id, name, status, message, severity, fix string) {
		report.Results = append(report.Results, core.DiagnosticResult{
			ID: id, Name: name, Status: status, Message: message,
			Severity: severity, FixSuggestion: fix,
		})
		if progress != nil {
			progress(core.DiagnosticProgress{CheckID: id, Status: status, Message: message})
		}
	}

	// 1. Endpoint configuration.
	if a.baseURL == "" {
		emit("ocw_endpoint", "Endpoint", "failed", NotConfiguredDetail, "error",
			"MacBridge 并存期会传入与旧 OpenCode 相同的已解析 serve URL；确认 CordCode Link 托管 serve 已就绪")
		report.OverallStatus = "failed"
		return report, nil
	}
	emit("ocw_endpoint", "Endpoint", "passed", fmt.Sprintf("configured url=%s", a.baseURL), "", "")

	// 2. Loopback-only enforcement (design §4.4).
	if isLoopbackURL(a.baseURL) {
		emit("ocw_loopback", "Loopback-only", "passed", "endpoint is loopback", "", "")
	} else {
		emit("ocw_loopback", "Loopback-only", "warning",
			fmt.Sprintf("endpoint %s is not loopback", a.baseURL), "warning",
			"官方 serve 凭据模型仅覆盖 loopback；非 loopback 端点不在托管范围内")
	}

	// 3. Generation + health probe (fresh, not the cached mirror). The verdict
	// reuses the C1 gate semantics: a successfully DETECTED non-1.18.18
	// generation is unsupported-generation (quarantined) — a failed check, and
	// the report stops here (no catalog/model probing on a quarantined
	// endpoint; no POST, no SSE, no session, no Kernel).
	c := newClient(a.baseURL, a.user, a.pass)
	probe := probeInstance(ctx, c)
	if probe.err != nil {
		emit("ocw_probe", "API generation", "failed", "probe failed: "+probe.err.Error(), "error",
			"确认 serve 进程存活与凭据；探针按序尝试 /global/health 与 /api/health")
		report.OverallStatus = "failed"
	} else if probe.gen != generation118 {
		emit("ocw_probe", "API generation", "failed", unsupportedGenerationDetail(probe.gen, probe.detail), "error",
			"OpenCode 1.18.18 是唯一 verified 产品代；v2/未知代端点已隔离（无 prompt、无 SSE ingest、无 Kernel、无新增 capability）。请改用 1.18.18 serve")
		report.OverallStatus = "failed"
		return report, nil
	} else {
		c.setGeneration(probe.gen)
		emit("ocw_probe", "API generation", "passed", probe.detail, "", "")
	}

	// 4. Catalog + selected model membership.
	if probe.err == nil {
		if catalog, err := a.fetchModelCatalog(ctx, c); err != nil {
			emit("ocw_catalog", "Model catalog", "warning", "provider catalog fetch failed: "+err.Error(), "warning",
				"发送前 catalog 校验会再次拉取；持续失败说明 /provider 不可用")
		} else {
			msg := fmt.Sprintf("%d models in runtime catalog", len(catalog.Models))
			if pending := a.GetModel(); pending != "" {
				providerID, modelID := parseQualifiedModel(pending)
				if _, ok := a.modelInCatalog(ctx, c, providerID, modelID); ok {
					msg += fmt.Sprintf("; selected model %s present", pending)
				} else {
					msg += fmt.Sprintf("; selected model %s NOT in catalog (send will fail fast)", pending)
					emit("ocw_catalog", "Model catalog", "warning", msg, "warning",
						"从 list_models 选择目录内模型；下次发送生效（1.18 pending 语义）")
					emit("ocw_probe_extra", "Generation detail", "passed", foldDiagnostics(), "", "")
					report.OverallStatus = "warning"
					return report, nil
				}
			}
			emit("ocw_catalog", "Model catalog", "passed", msg, "", "")
		}
	}

	// 5. Permission folding state (实施期挂账).
	emit("ocw_permission_fold", "Permission folding", "passed", foldDiagnostics(), "", "")

	if report.OverallStatus == "" {
		report.OverallStatus = "passed"
	}
	return report, nil
}

var _ core.DiagnosticsProvider = (*Agent)(nil)

// isLoopbackURL reports whether the URL host is loopback (127.0.0.0/8 or
// localhost / [::1]).
func isLoopbackURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		return false
	}
	host := raw
	if idx := strings.Index(raw, "://"); idx >= 0 {
		host = raw[idx+3:]
	}
	if idx := strings.IndexAny(host, "/:"); idx >= 0 {
		host = host[:idx]
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || host == "::1" {
		return true
	}
	return strings.HasPrefix(host, "127.")
}
