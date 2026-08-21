package codexweb

// wire_descriptor.go —— go-bridge 侧 descriptor/capability（§9.1 第 8 条）。
//
// 能力诚实原则：StaticCapabilities 只声明有 Phase 0 真实证据的正面能力；
// history/list 由接口推导（core.Agent + RichHistoryProvider）；turn/交互能力在
// Phase 3/4 落地前不声明。Gate 未过或未取样的能力 fail closed。

import "github.com/openAgi2/cordcode-macbridge/core"

// WireDescriptor 声明：
//   - Kind "codex-web"（iOS BackendKind.codexWeb，独立于旧 "codex"）；
//   - LiveEventBroadcast：共享 daemon 上的订阅连接收到同一官方 thread 的全量
//     turn/item 事件（Phase 0 Gate scene1 实证；§8.2 PASS）；
//   - external_turn_streaming：Mac 官方客户端（默认配置 TUI，共享 daemon）发起的
//     外部 turn 实时旁观成立（Gate 核心 PASS 结论）；
//   - RequiresExternalTurnPolling=false：不靠轮询补旁路。
func (a *Agent) WireDescriptor() *core.WireDescriptor {
	return &core.WireDescriptor{
		Kind:                        WireKind,
		DisplayName:                 "Codex Web",
		LiveEventModel:              core.LiveEventBroadcast,
		RequiresExternalTurnPolling: false,
		StaticCapabilities:          []string{"external_turn_streaming"},
	}
}

var _ core.WireDescriptorProvider = (*Agent)(nil)
