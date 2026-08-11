package gobridge

// rpc_scopes.go — §6.3 RPC 按方法鉴权 scope 表。
//
// 每个 RPC 方法映射到一个 scope（空串 = 无条件放行，仅用于不能要求 scope 的控制面占位）。
// 默认配对设备拥有全部 7 个 scope（见 DefaultGrantedScopes / deviceHasScope 的向后兼容语义），
// 所以这张表当前不改变现有授权语义——它的价值是：
//  1. 为新 RPC 提供硬门槛（CI guard TestEveryDispatchedRPCHasScope 强制每个 dispatchRPC
//     case 都在此声明 scope）；
//  2. 为未来「受限配对」（例如只读 iPad）提供按方法收回的能力；
//  3. 让客户端据 hello_ack.grantedScopes 做 UI gating。
//
// 校验落点：CapabilityPolicy.AuthorizeRPC（HandleRPC 的单一漏斗，先于 dispatchRPC 与所有
// switch 外方法路由），而非字面「dispatchRPC 入口」——详见计划 §6.3「实现核实」。这样
// dispatchRPC 的 44 个 case 和 switch 外的 set_observation_scope / delivery ×3 /
// enable_relay_pairing 都经过同一道 scope 门。
//
// ping / recovery_applied 是 Type 级连接消息（msg.Type == "ping"），不进 HandleRPC 的 method
// 路由，天然无条件，不在此表。

const (
	ScopeSessionRead     = "session.read"
	ScopeSessionWrite    = "session.write"
	ScopeConfigRead      = "config.read"
	ScopeConfigWrite     = "config.write"
	ScopeWorkspaceRead   = "workspace.read"
	ScopeWorkspaceMutate = "workspace.mutate"
	ScopeDeliveryManage  = "delivery.manage"
)

// rpcScopeTable 把每个 RPC 方法名映射到它所属的 scope。
// 空串 = 无条件放行（bridge 控制面占位，不能要求 scope 否则握手/探活死锁）。
//
// 任何新增 RPC 必须在此声明 scope——TestEveryDispatchedRPCHasScope 会强制 dispatchRPC 的
// 每个 case 都有条目；TestScopeTableCoversAllMethods 反向保证无僵尸条目（每个 key 都是
// 真实分发的方法）。
var rpcScopeTable = map[string]string{
	// bridge.control（无条件）。dispatchRPC 里的 case "hello" 是遗留占位（返回 Ok:true）；
	// 真正握手走 Type 级 hello 消息，不进此表。给空串只是让 CI guard 通过且明确语义。
	"hello": "",

	// session.read
	"get_session":                 ScopeSessionRead,
	"get_session_messages":        ScopeSessionRead,
	"get_session_projection":      ScopeSessionRead,
	"list_sessions":               ScopeSessionRead,
	"list_pinned_sessions":        ScopeSessionRead,
	"fetch_todos":                 ScopeSessionRead,
	"check_pending_notifications": ScopeSessionRead,
	"get_turn_diff":               ScopeSessionRead,
	"get_full_thread_diff":        ScopeSessionRead,

	// session.write
	"create_session":        ScopeSessionWrite,
	"send_message":          ScopeSessionWrite,
	"abort_generation":      ScopeSessionWrite,
	"resume_session":        ScopeSessionWrite,
	"delete_session":        ScopeSessionWrite,
	"rename_session":        ScopeSessionWrite,
	"archive_session":       ScopeSessionWrite,
	"set_session_pinned":    ScopeSessionWrite,
	"compress_context":      ScopeSessionWrite,
	"resolve_permission":    ScopeSessionWrite,
	"question_reply":        ScopeSessionWrite,
	"question_reject":       ScopeSessionWrite,
	"resolve_user_input":    ScopeSessionWrite,
	"cancel_request_v1":     ScopeSessionWrite, // R1.5：read_file_v2 bulk cancel control RPC（control-plane）
	"share_session":         ScopeSessionWrite, // dispatchRPC 内 not_supported 占位 case
	"set_observation_scope": ScopeSessionWrite, // switch 外方法（handlers.go:837）

	// config.read
	"list_providers":        ScopeConfigRead,
	"list_models":           ScopeConfigRead,
	"list_agents":           ScopeConfigRead,
	"list_permission_modes": ScopeConfigRead,
	"get_usage":             ScopeConfigRead,
	"list_memory_files":     ScopeConfigRead,
	"read_memory_file":      ScopeConfigRead,
	"run_diagnostics":       ScopeConfigRead,

	// config.write
	"set_provider":        ScopeConfigWrite,
	"switch_model":        ScopeConfigWrite,
	"set_permission_mode": ScopeConfigWrite,

	// workspace.read
	"get_workspace_diff":         ScopeWorkspaceRead,
	"read_file_v2":               ScopeWorkspaceRead, // tagged text/unsupported/binary + segments + identity (R1.1)
	"list_directory":             ScopeWorkspaceRead, // path 校验在 §6.5 接入 workspace-bound
	"get_git_context":            ScopeWorkspaceRead,
	"fetch_content_chunk":        ScopeWorkspaceRead,
	"check_pull_request_support": ScopeWorkspaceRead,

	// workspace.mutate
	"checkout_git_branch": ScopeWorkspaceMutate,
	"create_git_branch":   ScopeWorkspaceMutate,
	"create_git_worktree": ScopeWorkspaceMutate,
	"create_pull_request": ScopeWorkspaceMutate, // §7.1 GitHub-only PR 集成
	"list_projects":       ScopeWorkspaceMutate,

	// delivery.manage
	"get_delivery_prekey_status": ScopeDeliveryManage,
	"upload_delivery_prekeys":    ScopeDeliveryManage,
	"get_delivery_chain_head":    ScopeDeliveryManage,
	"enable_relay_pairing":       ScopeDeliveryManage, // switch 外方法（relay_upgrade.go:67）
}

// DefaultGrantedScopes 是配对设备默认拥有的全部 scope。
// TrustedDeviceRecord.GrantedScopes 为 nil/空时视作拥有此全集（向后兼容：现有持久化记录
// 没有该字段，不能因字段缺失把已配对设备锁死）。
func DefaultGrantedScopes() []string {
	return []string{
		ScopeSessionRead,
		ScopeSessionWrite,
		ScopeConfigRead,
		ScopeConfigWrite,
		ScopeWorkspaceRead,
		ScopeWorkspaceMutate,
		ScopeDeliveryManage,
	}
}

// scopeForMethod 返回方法对应的 scope；方法不在表内返回空串（= 无条件）。
// 表外未知方法由 dispatchRPC 的 default 分支返回 method_not_found；AuthorizeRPC 对其放行。
func scopeForMethod(method string) string {
	return rpcScopeTable[method]
}

// deviceHasScope 判断设备是否拥有给定 scope。
//   - scope == "" → 永远 true（无条件方法）。
//   - device == nil（开发模式无认证）→ true：与既有 AuthorizeRPC 对 nil device 的放行语义
//     一致，下游 handler 的 workspace 锚点兜底；真实受限配对时 device 非 nil 且 GrantedScopes 非空。
//   - device.GrantedScopes 为 nil/空 → true：向后兼容现有持久化记录（无此字段 = 全权）。
//   - 否则线性查找 GrantedScopes。
func deviceHasScope(device *TrustedDeviceRecord, scope string) bool {
	if scope == "" {
		return true
	}
	if device == nil {
		return true
	}
	if len(device.GrantedScopes) == 0 {
		return true
	}
	for _, s := range device.GrantedScopes {
		if s == scope {
			return true
		}
	}
	return false
}

// grantedScopesForEcho 计算 hello_ack 应回显给客户端的 grantedScopes。
// device 显式带 GrantedScopes 时回显之（未来受限配对）；否则回显默认全集。
// 用于客户端 UI gating——客户端据此决定哪些入口可见。
func grantedScopesForEcho(device *TrustedDeviceRecord) []string {
	if device != nil && len(device.GrantedScopes) > 0 {
		out := make([]string, len(device.GrantedScopes))
		copy(out, device.GrantedScopes)
		return out
	}
	return DefaultGrantedScopes()
}
