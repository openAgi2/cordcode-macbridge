package gobridge

import "github.com/openAgi2/cordcode-macbridge/core"

func deriveBackendCapabilities(id string, agent core.Agent, codexBackendMode string) []string {
	caps := []string{"model_switch", "session_state"}

	if _, ok := agent.(core.ProviderSwitcher); ok {
		caps = append(caps, "provider_switch")
	}
	if _, ok := agent.(core.HistoryProvider); ok {
		caps = append(caps, "session_history")
	}
	if _, ok := agent.(core.WorkDirSwitcher); ok {
		caps = append(caps, "workspace_diff")
	}
	// session_pagination capability disabled: 去分页方案。backward paging 在长 session 上
	// 造成 newest↔backward 自维持振荡（WebView 渲染抖动→顶部哨兵→loadOlder→再渲染→再抖动）。
	// iOS 在此 capability 缺失时有完整 fallback：fetchMessages 走 getSessionMessagesResult
	// 全量返回（不带 paginate/cursor），一次性读完整个 session。配合 relay MaxFrameBytes=8MB
	// + 写 deadline 60s，全量响应（实测 3-6MB 帧）可单帧传输不超限。重新启用需：relay 帧上限
	// 足够大 + 或改用 content_chunking 分片策略承载超大 session。
	// if _, ok := agent.(core.TranscriptLocator); ok {
	// 	caps = append(caps, "session_pagination")
	// }
	if _, ok := agent.(core.MemoryFileReader); ok {
		caps = append(caps, "memory_read")
	}
	if _, ok := agent.(core.DiagnosticsProvider); ok {
		caps = append(caps, "diagnostics")
	}
	if _, ok := agent.(core.TokenUsageReporter); ok {
		caps = append(caps, "usage_reporting")
	}
	if _, ok := agent.(core.ModeSwitcher); ok {
		caps = append(caps, "permission_mode")
	}
	if _, ok := agent.(core.SessionRenamer); ok {
		if _, ok := agent.(core.SessionArchiver); ok {
			caps = append(caps, "session_mutation")
		}
	}
	if id == "claudecode" {
		caps = append(caps, "content_chunking")
	}
	if _, ok := agent.(core.SessionDeleter); ok {
		caps = append(caps, "session_delete")
	}
	// session_pin is advertised INDEPENDENT of session_mutation (rename+archive). Codex and
	// OpenCode do not implement rename/archive but do implement SessionPinner, so folding pin
	// into session_mutation would silently hide pinning for them. See docs/protocol/bridge-v1.md
	// 「Session Pinning」.
	if _, ok := agent.(core.SessionPinner); ok {
		caps = append(caps, "session_pin")
	}
	if id != "opencode" && id != "codex" {
		if _, ok := agent.(core.ToolAuthorizer); ok {
			caps = append(caps, "permission_resolve")
		}
	}
	if _, ok := agent.(core.TodoProvider); ok || id == "opencode" {
		caps = append(caps, "todos")
	}
	if id == "codex" && codexBackendMode == "app_server" {
		caps = append(caps, "compression")
		caps = append(caps, "question_reply")
	}
	// claudecode now answers AskUserQuestion via the verified control_response
	// path (RespondQuestion/RejectQuestion in session.go), so it advertises the
	// backend-neutral question_reply capability. OpenCode does not resolve
	// questions over the bridge and must NOT advertise it.
	if id == "claudecode" {
		caps = append(caps, "question_reply")
	}

	return caps
}
