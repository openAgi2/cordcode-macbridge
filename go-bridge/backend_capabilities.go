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
	// 全量返回（不带 paginate/cursor），一次性读完整个 session。配合 relay 默认
	// MaxFrameBytes=32MiB + 写 deadline 120s，全量响应（实测 3-6MB 帧）可单帧传输不超限。重新启用需：relay 帧上限
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

	// structured_user_input_v1（design docs/2026-08-01-codex-claude-structured-user-input-design.md §13）：
	// 仅当 backend adapter session 实现 core.UserInputResponder（ResolveUserInput）时广告。这是
	// per-backend descriptor capability；session_sync_v2（Projection Kernel readiness）是独立的
	// global connection capability，iOS 按 §13.1 step 8 自行 AND 两者，此处不同步耦合、不在 advert
	// 层 AND Kernel。实现者：Codex app_server（appServerSession）与 Claude Code（claudeSession）；
	// opencode 与非 app_server codex 不实现 → fail-closed（iOS 不出现提问卡）。
	// Claude capability and production control-request routing share one readiness source through
	// StructuredUserInputProvider. Descriptor aliases therefore cannot advertise a path that the
	// instantiated adapter has not enabled. No cross-backend global boolean is used.
	if id == "codex" && codexBackendMode == "app_server" {
		caps = append(caps, "structured_user_input_v1")
	}
	if ready, ok := agent.(core.StructuredUserInputProvider); ok && ready.StructuredUserInputReady() {
		caps = append(caps, "structured_user_input_v1")
	}

	// external_turn_streaming（refactor `multi-client-streaming-sync` Phase 1/2）：MacBridge 对该
	// backend 的外部 turn 已实现 push 流式——file-relay 解析 transcript/rollout 增长，发
	// text_delta/reasoning_delta/tool_started/context_usage_updated，客户端据此可关闭发现型轮询、
	// 改用 reconcile-on-event（turn_completed 一次 reconcile + 低频看门狗）。Phase 1 已为 codex
	// （rollout 流）与 claude/claudecode（transcript 流）实现；opencode 本就 SSE push-native；
	// grokbuild 待 leader-socket subscriber。capability 是 extensible string，非破坏性新增，无需
	// 协议大版本 bump。
	if id == "codex" || id == "claude" || id == "claudecode" {
		caps = append(caps, "external_turn_streaming")
	}

	return caps
}
