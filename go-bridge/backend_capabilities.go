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
	// §6.2: A-class static positive capabilities (content_chunking for claude,
	// claude question_reply, external_turn_streaming, opencode todos 兜底) migrated
	// out of id-keyed checks into the driver's WireDescriptor.StaticCapabilities
	// (self-description). They are appended below from resolveStaticDescriptor(agent).
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
	// D class (mixed): TodoProvider interface half stays wire; opencode id-half migrated
	// into opencode's WireDescriptor.StaticCapabilities (§6.2 A-class), appended below.
	if _, ok := agent.(core.TodoProvider); ok {
		caps = append(caps, "todos")
	}
	if id == "codex" && codexBackendMode == "app_server" {
		caps = append(caps, "compression")
		caps = append(caps, "question_reply")
	}
	// claude question_reply now flows from the driver's WireDescriptor.StaticCapabilities
	// (§6.2 A-class self-description), not an id check here. OpenCode does not resolve
	// questions over the bridge and must NOT advertise it (its StaticCapabilities omits it).

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

	// external_turn_streaming (refactor `multi-client-streaming-sync` Phase 1/2): declared
	// for backends whose external turns MacBridge now streams via push (file-relay content
	// deltas). §6.2 migrated this out of an id check into each driver's
	// WireDescriptor.StaticCapabilities: codex (rollout stream) and claude/claudecode
	// (transcript stream) self-describe it; opencode is already SSE push-native (no
	// file-relay); grokbuild waits on the leader-socket subscriber. The capability is an
	// extensible string, a non-breaking addition with no protocol major-version bump.
	if wd := resolveStaticDescriptor(agent); wd != nil {
		caps = append(caps, wd.StaticCapabilities...)
	}

	// §6.1 checkpoint 只读 diff: capability 派生自 CheckpointProvider opt-in 接口（与
	// SessionPinner 同模板）。snapshot 是 workspace 文件快照（非 session 真相源）；iOS 据此
	// capability 决定是否展示 turn diff UI。capture 在 non-git workspace 诚实地不写 ref
	// （workspace_not_git），无 mock/placeholder fallback。ConversationRollbackProvider 当前
	// 无 driver 实现 → capability 缺省隐藏，revert 入口据此禁用。两条都是 extensible string，
	// 非破坏性新增，无 major version bump；不做 backend-id 硬分支。
	if cp, ok := agent.(core.CheckpointProvider); ok && cp.SupportsCheckpoint() {
		caps = append(caps, "supports_checkpoint")
	}
	if _, ok := agent.(core.ConversationRollbackProvider); ok {
		caps = append(caps, "supports_conversation_rollback")
	}

	// §3.9 attachment truth source: positively declared attachment kinds from
	// core.AttachmentSupporter (per-driver×mode semantic support — the same
	// source the send_message pre-check gates on). Absence means NOT
	// supported; "absence = unsupported" must never be read the other way.
	if sup, ok := agent.(core.AttachmentSupporter); ok {
		caps = append(caps, sup.SupportedAttachmentKinds()...)
	}

	// §6.5 工作区文件浏览器: 当 backend 有 WorkDirSwitcher 接口时,声明 workspace-browse
	// 能力。所有三个 agent (claudecode/codex/opencode) 均实现此接口,因此浏览器入口普遍可用。
	// 为 extensible string,additive,无 major version bump。
	if _, ok := agent.(core.WorkDirSwitcher); ok {
		caps = append(caps, "supports_workspace_browse")
	}

	// §7.1 PR 集成（GitHub-only）：当 backend 有 WorkDirSwitcher + 工作区是 GitHub remote
	// + gh CLI 已安装且认证 + driver 实现了非交互式 PR 内容生成器时，声明 supports_pull_requests。
	if ws, ok := agent.(core.WorkDirSwitcher); ok && ws.GetWorkDir() != "" {
		if _, ok := agent.(core.PrContentGenerator); ok && supportsPullRequests(ws.GetWorkDir()) {
			caps = append(caps, "supports_pull_requests")
		}
	}

	// Phase 1 §4.1 B commit_and_push：当 driver 实现了非交互式 commit message 生成器时，
	// 声明 supports_commit_message（iOS 据此决定是否显示 commit message 编辑/生成 UI）。
	// 现网仅 claudecode/codex 实现（opencode/grokbuild 无），未实现的 backend 不显示 commit UI。
	if _, ok := agent.(core.CommitMessageGenerator); ok {
		caps = append(caps, "supports_commit_message")
	}

	// Phase 4 后台任务只读中心（roadmap §3.2/§3.3）：`background_tasks` = list 面
	// （dsh-web 经 core.BackgroundTaskProvider；claudecode 由 go-bridge sidechain
	// registry 服务——与 B4 同源派生，C1）；`background_task_details` = detail 面
	// （BackgroundTaskDetailReader 或 claude registry detail）。未声明的 backend
	// 完全无任务面，iOS 不显示入口。
	if _, ok := agent.(core.BackgroundTaskProvider); ok {
		caps = append(caps, "background_tasks")
	}
	if id == "claudecode" {
		caps = append(caps, "background_tasks")
	}
	if _, ok := agent.(core.BackgroundTaskDetailReader); ok {
		caps = append(caps, "background_task_details")
	}
	if id == "claudecode" {
		caps = append(caps, "background_task_details")
	}

	return dedupCapabilities(caps)
}

// dedupCapabilities preserves first-occurrence order while removing duplicates. §6.2
// moved A-class capabilities into WireDescriptor.StaticCapabilities, which a driver
// could in principle also derive via an interface (e.g. an opencode-like backend that
// both implements TodoProvider and lists "todos" in its StaticCapabilities). Dedup keeps
// the advertised set clean without relying on every driver to avoid overlap.
func dedupCapabilities(caps []string) []string {
	seen := make(map[string]bool, len(caps))
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}
