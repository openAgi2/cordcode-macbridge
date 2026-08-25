# usage dumps —— rollout token_count 契约 fixture（审计 §3.3-C1-1）

- 来源：真实 rollout 尾部 token_count 记录脱敏冻结（仅 token 数值，无用户内容）；
  首次内联入 context_usage_test.go（6f765fc），2026-08-25 审计 C1 加固时落盘为契约 fixture。
- 对照 pin `536f86e5cc9ec1ff38457d099bf320b9d08eeeba` 源码结构：
  - `protocol/src/protocol.rs:2094-2100` `TokenUsageInfo { total_token_usage, last_token_usage, model_context_window: Option<i64> }`
  - `protocol/src/protocol.rs:2072-2088` `TokenUsage` 字段族（input/cached_input/cache_write/output/reasoning_output/total_tokens）
  - `protocol/src/protocol.rs:2160-2164` `TokenCountEvent { info: Option<TokenUsageInfo>, rate_limits }`（EventMsg::TokenCount :1346）
  - `history/src/rollout_payload.rs:49-51` `RolloutItemWire::EventMsg`（rollout 行 `{"type":"event_msg","payload":{...}}`）
- 契约规则（context_usage.go readPersistedContextUsage）：
  1. 最新一条 token_count 记录 Info 缺失 / model_context_window null·≤0 / 负用量 → 弃用文件路径 + warn 诊断（不静默回退 cache）；
  2. CLI 版本不在 `persistedUsageVerifiedCLIFamilies`（当前 0.149.x 族）→ 不走文件路径（版本门控）；
  3. 官方无冷用量 RPC（仅 thread/tokenUsage/updated live 通知）——本路径为 owner 2026-08-25 裁决的记录在案豁免，官方提供 RPC 后退役。
