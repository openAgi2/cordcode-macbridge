// Package codexweb 是 CordCode 的 codex-web backend：Codex 官方长驻 app-server 的
// API 客户端 + bridge-v1 协议翻译器（设计 docs/2026-08-21-codex-web-backend-design.md）。
//
// 建立纪律（设计 §0/§2.2/§2.4）：
//   - 本目录从空目录建立，禁止复制/包装/import 旧 agent/codex；禁止 import
//     transcriptindex 或任何旧 rollout/file-relay/session scanner 包；
//   - 架构分层参考 agent/dsh-web（职责边界、生命周期、SSV2 pathless 接线组织），
//     Codex 语义全部来自官方源码（pin 536f86e5）、目标二进制生成 schema 与
//     Phase 0 真实样本（testdata/official-0.149.0-alpha.4）；
//   - 所有 session/turn/config 写入只走官方 app-server JSON-RPC（§9）。
//
// Phase 0 关键协议事实（实测，见设计 §22）：
//   - daemon control socket 是 WebSocket over UDS（每个 JSON-RPC 消息一个 WS text 帧）；
//   - 通知分级：thread/started、thread/status/changed 全局广播；turn/*、item/* 仅发
//     已订阅连接且不重放（§7.1）。
//
// 当前状态：Phase 1 骨架。文件职责见各文件头；行为实现随 Phase 1–4 落地。
package codexweb

// BackendID 是 wire/backend identity（设计 §5.1，独立于旧 "codex"）。
const BackendID = "codex-web"

// WireKind 是 iOS 侧 backend kind（与 BackendID 一致）。
const WireKind = "codex-web"

// Agent 是 codex-web backend 的组装根（dsh-web 式依赖组装；语义实现分布于同包各文件）。
type Agent struct {
	// Phase 1：lifecycle/transport/rpc 组装；Phase 2 起 catalog/history/SSV2 接线。
}

// New 返回空骨架 Agent。注册进 main.go 默认 drivers 属 Phase 5（设计 §9.1/Phase 5）。
func New() *Agent { return &Agent{} }

// Identity 返回 (threadID, turnID, itemID) 三元组——§2.5/§9 的事务 identity 基础。
// 禁止以文件路径或本地别名代替官方 identity。
type Identity struct {
	ThreadID string
	TurnID   string
	ItemID   string
}

// ConnectionEpoch 标识一次 transport 连接代际（§7.2：断线清理旧 epoch pending）。
type ConnectionEpoch int64
