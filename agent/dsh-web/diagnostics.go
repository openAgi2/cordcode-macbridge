package dshweb

// Diagnostics (design §4.3.8): instance lifecycle resolution (external probe /
// managed spawn — this IS the make-it-work surface), host.describe (its
// `version` is an API-level identifier, NOT the npm package version — S6, do
// not pass it off as one), capability probe (empty session.list + llm.providers
// with full state bits), and the honest unauthenticated-loopback disclosure
// (S11: dsh v1 has no auth layer; loopback binding + Bridge-fronting is the
// entire defense).

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

// diagInstance resolves and reports the instance source. NOTE: Resolve is a
// MUTATING call (canonical-3080 design §0.1/S2 — it may spawn on the seat);
// read-only discrimination is lsof + the state file + InstanceStatus.
func (a *Agent) diagInstance(ctx context.Context) core.DiagnosticResult {
	// Grace window first: the seat is down but expected back — report the
	// window, not a hard failure.
	if inGrace, until := a.resolver.GraceState(); inGrace {
		return core.DiagnosticResult{
			Status:  diagStatusFailed,
			Message: fmt.Sprintf("座位 %s 失联，宽限重连中（至 %s）；期间 RPC 返回 backend_unavailable，不收养不补拉", a.resolver.seatURL(), until.Format(time.RFC3339)),
		}
	}
	inst, err := a.resolver.Resolve(ctx)
	if err != nil {
		msg := fmt.Sprintf("未找到可用的 dsh web 实例：%v", err)
		fix := "安装 dsh（npm i -g @deepseek-ai/dsh）；若权威端口被非 dsh 进程占用，请释放端口后重试"
		if sp := a.resolver.LastSpawnErr(); sp != nil && strings.Contains(sp.Error(), "non-dsh") {
			msg = fmt.Sprintf("权威端口被非 dsh 进程占用：%v", sp)
		}
		return core.DiagnosticResult{
			Status:        diagStatusFailed,
			Message:       msg,
			FixSuggestion: fix,
		}
	}
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
	return core.DiagnosticResult{Status: diagStatusFailed, Message: "unknown instance source"}
}

// diagAPI probes the capability surface (host.describe + empty session.list +
// llm.providers full set with state bits — §3.4 应对).
func (a *Agent) diagAPI(ctx context.Context) core.DiagnosticResult {
	client, err := a.clientFor(ctx)
	if err != nil {
		return core.DiagnosticResult{Status: diagStatusFailed, Message: fmt.Sprintf("实例不可达: %v", err)}
	}
	var desc describeValue
	if err := client.Call(ctx, "host.describe", map[string]any{}, &desc); err != nil {
		return core.DiagnosticResult{
			Status:  diagStatusFailed,
			Message: fmt.Sprintf("host.describe 失败: %v", err),
		}
	}
	if err := client.Call(ctx, "session.list", sessionListRequest{}, nil); err != nil {
		return core.DiagnosticResult{
			Status:  diagStatusFailed,
			Message: fmt.Sprintf("session.list 探活失败: %v", err),
		}
	}
	var provs llmProvidersValue
	if err := client.Call(ctx, "llm.providers", map[string]any{}, &provs); err != nil {
		return core.DiagnosticResult{
			Status:  diagStatusFailed,
			Message: fmt.Sprintf("llm.providers 失败: %v", err),
		}
	}
	active, dormant := 0, 0
	for _, p := range provs.Providers {
		if p.Active {
			active++
		} else {
			dormant++
		}
	}
	lines := []string{
		fmt.Sprintf("API 版本标识 %s（host.describe version；非 npm 包版本）", desc.Version),
		fmt.Sprintf("providers: %d 活跃 / %d 休眠（休眠项不进入 list_providers）", active, dormant),
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
