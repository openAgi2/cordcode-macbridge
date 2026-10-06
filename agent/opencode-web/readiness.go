package opencodeweb

import (
	"context"
	"time"
)

// readiness.go — structured instance readiness seam (2026-10-06
// install-and-start plan §3). Descriptor-facing, read-only; replaces the
// boolean InstanceStatus fold for descriptor detection (InstanceStatus stays
// as the boolean view for internal callers). One discrimination sequence,
// fail-closed, never collapsing a probe failure into not_configured:
//
//  1. baseURL empty → not_configured — no endpoint was ever resolved
//     (managed_local never started, external_http without URL, or disabled).
//  2. probe auth rejected (401 with credentials) → service_not_running with
//     an auth-failure wording — the server is listening; a restart usually
//     does not fix credentials (dsh-web seam parity).
//  3. probe server_unauthenticated (no-auth 200) → service_not_running with
//     the missing-auth wording (the managed-local server always carries
//     Basic Auth; an open server is rejected by policy).
//  4. any other probe failure (unreachable, no usable route, shape) →
//     service_not_running + the probe's own error text.
//  5. detected generation != generation118 → service_not_running + the
//     shared quarantine wording (unsupportedGenerationDetail).
//  6. generation118 → available + the probe detail.
//
// 「安装中」「启动中」 are Mac-row local states; this seam keeps reporting
// the underlying status. A failed probe is retried on every read (the probe
// cache never serves an error past its timestamp), so a server started after
// the last failure flips the row on the next descriptor poll without a
// bridge restart.

// Readiness statuses — wire AgentStatus values; no new enums invented
// (not_configured / service_not_running / available already exist).
const (
	ReadinessAvailable         = "available"
	ReadinessNotConfigured     = "not_configured"
	ReadinessServiceNotRunning = "service_not_running"
)

// StructuredInstanceReadiness implements the descriptor-facing read-only
// readiness seam. It never spawns, binds, or writes; the probe is a GET-only
// sequence against the resolved endpoint.
func (a *Agent) StructuredInstanceReadiness() (string, string) {
	if a.baseURL == "" {
		return ReadinessNotConfigured, NotConfiguredDetail
	}
	a.probeMu.Lock()
	if a.probe == nil || a.probe.err != nil || time.Since(a.probe.at) > instanceStatusProbeTTL {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		a.runProbe(ctx)
		cancel()
	}
	res := a.probe
	a.probeMu.Unlock()
	if res == nil {
		return ReadinessServiceNotRunning, "probe failed: no result"
	}
	if res.err != nil {
		switch res.kind {
		case probeErrAuthRejected:
			return ReadinessServiceNotRunning, "认证失败（服务在监听但凭据不匹配或未配置；重启通常无法解决）：" + res.err.Error()
		case probeErrUnauthenticated:
			return ReadinessServiceNotRunning, "服务未启用认证（no-auth 200；OpenCode Web 需要启用 Basic Auth 的 serve）：" + res.err.Error()
		}
		return ReadinessServiceNotRunning, "probe failed: " + res.err.Error()
	}
	if res.gen != generation118 {
		return ReadinessServiceNotRunning, unsupportedGenerationDetail(res.gen, res.detail)
	}
	return ReadinessAvailable, res.detail
}
