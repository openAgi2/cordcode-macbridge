package gobridge

import "log/slog"

// CapabilityPolicy 是集中式 RPC 授权层（P3 架构演进的第一步，§3.2 / §8）。
//
// 当前实现是最小可用版本：为"文件类"敏感 RPC 建立统一的、可扩展的授权钩子，
// 把"已认证设备能否执行某方法、可访问哪个 workspace"从散落 handler 收敛到一处。
// 这不是完整的 capability model（device capability/version、本机确认升级尚未引入），
// 但它确立了 policy 层的位置与签名，后续 P0-1 的 workspace 锚点、device capability
// 字段与本机确认都挂载在这里，而不是继续在 handler 内各自判断。
//
// design intent（来自 review §8 三层信任）：
//   - 未配对客户端：只能提交有界配对 claim（由 /pairing 限流覆盖，不进 RPC dispatch）。
//   - 已配对普通设备：可管理自己的会话，但只能访问显式授权 workspace。
//   - 本机管理员能力：设备管理、workspace 外文件读取、credential/config 修改。
type CapabilityPolicy struct {
	// fileScopedMethods 是需要 workspace 授权根的方法集合。
	// 这些方法默认要求"设备当前有解析出的授权 workspace"，否则拒绝。
	fileScopedMethods map[string]bool
}

// NewCapabilityPolicy 返回默认策略。
func NewCapabilityPolicy() *CapabilityPolicy {
	return &CapabilityPolicy{
		fileScopedMethods: map[string]bool{
			// read_file 已在 handleReadFile 内做 workspace 锚点校验（P0-1）。
			// 这里登记是为了将来把"是否允许该设备读文件"升级为显式 capability，
			// 并为新增的文件类方法提供一处登记点，避免遗漏。
			"read_file":           true,
			"list_directory":      true,
			"get_git_context":     true,
			"checkout_git_branch": true,
			"create_git_branch":   true,
			"create_git_worktree": true,
		},
	}
}

// AuthorizeRPC 在 RPC dispatch 前对方法做集中授权检查。
// 返回 nil 表示放行；返回 *WireError 表示拒绝（携带稳定错误码）。
//
// 当前：对 fileScopedMethods 不做额外拒绝（workspace 锚点由 handleReadFile 内执行），
// 但记录"该方法进入了 policy 层"，确立集中入口。后续 capability 字段就绪后，
// 这里会读取 device capability 并决定是否需要本机确认。
//
// device 可能为 nil（开发模式无认证）；此时放行，由下游 handler 的 workspace 锚点兜底。
func (p *CapabilityPolicy) AuthorizeRPC(conn Connection, msg WireMessage) *WireError {
	if p == nil {
		return nil
	}
	if _, scoped := p.fileScopedMethods[msg.Method]; !scoped {
		return nil
	}
	// 留作扩展点：当 device capability 字段引入后，在此判断该设备是否具备
	// 文件读取 capability；不具备则返回 file.capability_denied。
	// 当前仅记录进入 policy 层的方法，不改变现有授权语义。
	slog.Debug("go-bridge: capability policy evaluated", "method", msg.Method, "requestId", msg.RequestID)
	return nil
}
