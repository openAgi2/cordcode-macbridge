package dshweb

// Diagnostics (design §4.3.8 + 2026-09-22 plan §5 read-only contract):
// instance state discrimination is READ-ONLY (never spawns — only the 启动
// button and the install back-half do), capability probe (session/list +
// session/modelCatalog + llm/listConfigurableProviders with declared bits),
// and the honest boundary disclosure (S11 successor: dsh ≥0.1.6 gates /api
// behind browser-session auth; the bridge mints/exchanges the cookie —
// auth.go — and loopback binding + Bridge-fronting remain the transport
// defense).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// RunDiagnostics implements core.DiagnosticsProvider.
func (a *Agent) RunDiagnostics(ctx context.Context, progress func(core.DiagnosticProgress)) (*core.DiagnosticReport, error) {
	results := make([]core.DiagnosticResult, 0, 3)

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

	runCheck("instance", "dsh web 实例", "required", a.diagInstance)
	runCheck("api", "官方 API 能力探活", "required", a.diagAPI)
	runCheck("security", "托管安全边界", "optional", a.diagSecurity)

	status := "healthy"
	for _, r := range results {
		if r.Status == diagStatusFailed && r.Severity == "required" {
			status = "unhealthy"
			break
		}
	}
	if status == "healthy" {
		for _, r := range results {
			if r.Status != diagStatusPassed {
				status = "degraded"
				break
			}
		}
	}
	return &core.DiagnosticReport{Results: results, OverallStatus: status}, nil
}

// diagInstance reports the instance state READ-ONLY (2026-09-22 plan §5:
// diagnostics and 重新检查 must not become a third spawn path — only the
// 启动 button and the install back-half spawn). It never calls Resolve; the
// discrimination mirrors StructuredInstanceReadiness.
func (a *Agent) diagInstance(ctx context.Context) core.DiagnosticResult {
	// Grace window first: the seat is down but expected back — report the
	// window, not a hard failure.
	if inGrace, until := a.resolver.GraceState(); inGrace {
		return core.DiagnosticResult{
			Status:  diagStatusFailed,
			Message: fmt.Sprintf("座位 %s 失联，宽限重连中（至 %s）；期间 RPC 返回 backend_unavailable，不收养不补拉", a.resolver.seatURL(), until.Format(time.RFC3339)),
		}
	}
	if inst := a.resolver.Current(); inst != nil {
		switch inst.Source {
		case SourceExternal:
			return core.DiagnosticResult{
				Status:  diagStatusPassed,
				Message: fmt.Sprintf("复用权威端口上的实例 %s（探测命中，未另起进程；谁拉起的即归谁，端口即身份）", inst.BaseURL),
			}
		case SourceManaged:
			start := processStartTime(inst.PID)
			extra := ""
			if start != "" {
				extra = fmt.Sprintf("，启动于 %s", start)
			}
			return core.DiagnosticResult{
				Status:  diagStatusPassed,
				Message: fmt.Sprintf("托管实例 %s（本 Bridge 在权威端口拉起，pid %d%s；Link 退出不杀，下次经座位收养）", inst.BaseURL, inst.PID, extra),
			}
		}
	}
	// Cold discrimination, read-only (no spawn, no adoption).
	status, detail := a.StructuredInstanceReadiness()
	switch status {
	case ReadinessAvailable:
		return core.DiagnosticResult{
			Status:  diagStatusPassed,
			Message: fmt.Sprintf("座位 %s 在听（session/list 应答；本进程尚未持有，下一次 RPC 收养）", a.resolver.seatURL()),
		}
	case ReadinessNotDetected:
		return core.DiagnosticResult{
			Status:        diagStatusFailed,
			Message:       "未找到 dsh 二进制（PATH、nvm、安装记录均无）",
			FixSuggestion: "在工作站 DeepSeek Harness 行点「安装」，或自行安装 Node.js 后运行 npm install -g @deepseek-ai/dsh",
		}
	case ReadinessPortConflict:
		return core.DiagnosticResult{
			Status:        diagStatusFailed,
			Message:       fmt.Sprintf("权威端口被非 dsh 进程占用：%s", detail),
			FixSuggestion: "释放该端口后重试（lsof 看到的占用者如上）",
		}
	default: // service_not_running
		return core.DiagnosticResult{
			Status:        diagStatusFailed,
			Message:       fmt.Sprintf("dsh web 未运行：%s", detail),
			FixSuggestion: "在工作站 DeepSeek Harness 行点「启动」拉起 3080，或自行运行 dsh web",
		}
	}
}

// diagAPI probes the capability surface (session/list + session/modelCatalog
// + llm/listConfigurableProviders with declared bits — §3.4 应对). The
// typert gateway retired host.describe; there is no API-level version
// identifier to report (the npm package version is surfaced by the install
// surface instead).
func (a *Agent) diagAPI(ctx context.Context) core.DiagnosticResult {
	client, err := a.clientFor(ctx)
	if err != nil {
		return core.DiagnosticResult{Status: diagStatusFailed, Message: fmt.Sprintf("实例不可达: %v", err)}
	}
	if err := client.Call(ctx, "session/list", listArgs(), nil); err != nil {
		return core.DiagnosticResult{
			Status:  diagStatusFailed,
			Message: fmt.Sprintf("session/list 探活失败: %v", err),
		}
	}
	var catalog modelCatalogValue
	if err := client.Call(ctx, "session/modelCatalog", map[string]any{}, &catalog); err != nil {
		return core.DiagnosticResult{
			Status:  diagStatusFailed,
			Message: fmt.Sprintf("session/modelCatalog 失败: %v", err),
		}
	}
	var provs configurableProvidersValue
	if err := client.Call(ctx, "llm/listConfigurableProviders", map[string]any{}, &provs); err != nil {
		return core.DiagnosticResult{
			Status:  diagStatusFailed,
			Message: fmt.Sprintf("llm/listConfigurableProviders 失败: %v", err),
		}
	}
	routable := make(map[string]bool, len(catalog.RoutableProviders))
	for _, id := range catalog.RoutableProviders {
		routable[id] = true
	}
	active, dormant := 0, 0
	for _, p := range provs.Providers {
		if routable[p.Provider] {
			active++
		} else {
			dormant++
		}
	}
	lines := []string{
		fmt.Sprintf("providers: %d 可路由 / %d 休眠（休眠项不进入 list_providers）", active, dormant),
	}
	return core.DiagnosticResult{Status: diagStatusPassed, Message: strings.Join(lines, "\n")}
}

// diagSecurity discloses the honest boundary (S11): the managed instance is
// an unauthenticated loopback service; loopback binding + Bridge-fronting is
// the entire defense (unlike opencode managed's generated Basic Auth).
func (a *Agent) diagSecurity(ctx context.Context) core.DiagnosticResult {
	inst := a.resolver.Current()
	if inst == nil || inst.Source != SourceManaged {
		return core.DiagnosticResult{
			Status:  diagStatusPassed,
			Message: "当前为外部实例（用户自管）；托管安全边界不适用",
		}
	}
	if !strings.Contains(inst.BaseURL, "127.0.0.1") {
		return core.DiagnosticResult{
			Status:  diagStatusFailed,
			Message: "托管实例未绑定 loopback — 违反安全红线，拒绝继续",
		}
	}
	return core.DiagnosticResult{
		Status:  diagStatusPassed,
		Message: "托管实例仅绑定 127.0.0.1（永不 0.0.0.0/--trusted-host）。dsh v1 服务本身无认证层（trust fence 非 auth）：本机其他进程可达的风险面与用户自启实例同类；loopback 绑定 + Bridge 前置是全部防线。",
	}
}

const (
	diagStatusRunning = "running"
	diagStatusPassed  = "passed"
	diagStatusFailed  = "failed"
	diagStatusWarning = "warning"
)

var _ core.DiagnosticsProvider = (*Agent)(nil)
