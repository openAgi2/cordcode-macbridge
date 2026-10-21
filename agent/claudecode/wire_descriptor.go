package claudecode

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor (§6.2 provider 零跨层抽象): claudecode self-describes its static wire
// attributes instead of go-bridge branching on backend id. claudecode runs as a
// stdin/stdout pipe (LiveEventSessionProcess), requires external-turn polling, and
// carries the A-class static capabilities that were previously id-keyed in
// backend_capabilities.go:
//   - content_chunking: claude transcript deltas are chunked for transport.
//   - question_reply: claude resolves AskUserQuestion via the verified control_response
//     path (RespondQuestion/RejectQuestion in session.go).
//   - external_turn_streaming: MacBridge file-relays transcript growth as push deltas.
//
// Note on id drift: the production backend id is "claude" (alias of factory name
// "claudecode"); the pre-§6.2 id checks `id=="claudecode"` therefore never matched the
// production descriptor, so claude historically did NOT advertise content_chunking /
// question_reply. Self-description restores the intended capabilities regardless of the
// id spelling under which the driver is registered. See docs/§6.2 + CHANGELOG.
//
// Mode-conditional capabilities (codex app_server) and interface-gated capabilities do
// NOT belong here; only static attributes live in the descriptor.
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        "claude_code",
		DisplayName:                 "Claude Code",
		LiveEventModel:              core.LiveEventSessionProcess,
		RequiresExternalTurnPolling: true,
		StaticCapabilities: []string{
			"content_chunking",
			"question_reply",
			"external_turn_streaming",
			// turn_detail_chunks_v1 已于 2026-10-21 撤回（评审 BLOCKER-2）：chunks
			// 声明会解锁 iOS「加载详细过程」入口，但生产连接协商 chunks 后
			// session_turn_items 走 v2 批量引擎，该引擎要求 core.TurnItemsPager
			//（仅 codex-remote 实现）→ Claude 必定 failed(upstream_error) 并把失败
			// 态写进共享投影。S-3 v2 退役休眠契约下 Claude 不声明 chunks；v1
			// producer（session_turn_items_handler runTurnDetailFetch）代码保留
			// 休眠，待实现 TurnItemsPager 或显式产品决策再恢复声明。
		},
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)

// SupportedAttachmentKinds (§3.9 truth source): image bytes become base64
// image blocks in the request parts and files are staged to disk and passed
// as path references — both reach the real Claude request.
func (a *Agent) SupportedAttachmentKinds() []string { return []string{"image", "file"} }

var _ core.AttachmentSupporter = (*Agent)(nil)
