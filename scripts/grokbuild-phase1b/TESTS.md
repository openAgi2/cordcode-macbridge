# Phase 1b（共用 turn dispatcher·Mac）实现与测试证据

- 工作树：`/Users/jacklee/Projects/cordcode-macbridge-plan-approval`（plan/approval-layer）
- 基线：48895c5（p0b P6 + D1 准入终表）+ 本单元变更
- 日期：2026-09-07
- 实施前取源码地图：grok-build 1.0.16 @ `72a6125`，`crates/codegen/xai-grok-shell/src/session/acp_session_impl/`（owner 指令：先读源码找现成实现，不自造轮子）

## 源码镜像对照（Go 实现 ← grok-build Rust 源码）

| Go 结构 | 镜像源码 | 不变量 |
|---|---|---|
| `turnDispatch.promote`（epoch 递增 + reqID 占位，busy 拒绝） | TurnReportSlot `start_next_turn`（turn_report_slot.rs:121-131）+ FinalizationGate 单租约（turn_task.rs:113-137） | 单 actor 单在飞 turn（operation lease）；每 promote 前进 epoch |
| `turnDispatch.settle(epoch)`（旧 epoch 拒绝） | TurnReportSlot `try_claim`（:90-103）：Free→Held→Reported | settle-once：response/cancel/EOF 三轨谁先到谁结算，后到者丢弃 |
| `settleForRequest(reqID)` | `claim_task_finalization` 身份匹配（turn_end.rs:191-208） | 陈旧 prompt 响应不得结算新 turn 的槽位 |
| `settleActive`（cancel 通知/EOF/Close 路径） | cancel.rs:643-650 注释不变量：front 的 waiter 永远被显式 resolve，绝不悬挂 | cancel rail 不等 turn task 自行了结 |
| `abandonTurn`（readLoop 退出，`errTurnActorDead`） | app.rs stdio EOF 路径：未结算 turn 报错误，绝不静默成功 | **EOF 不成功** |
| `Close` settle-without-emit | 同上（消费者已移除时不发射） | 逐出会话不悬挂 Execute waiter |
| `collectHostText`（agent_message_chunk `update._meta.hostTurn=true`） | send_host_turn_slash_command_output（acp_session.rs） | §7 正文组：官方反馈正文=Execute resultText；正文同时走正常事件轨 |
| `emitTurnTerminal`（EventResult/EventError 带 StopReason/CancellationCategory/Input/OutputTokens） | prompt_complete_fields（sampling/error.rs:375-398）：两轨终态字段同源 | **stopReason 保留**；硬错误=RPC reject 无 stopReason（ADMISSION.md §二.1） |
| handleNotification `session/cancel` → settleActive(cancelled) | cancel.rs:762-766/911-957 | agent 自取消也走单次结算 |
| `executeHostCommand` ctx 到期 → best-effort CancelTurn | cancel-then-cleanup | 取消与清理 |
| `liveSessions`（指针身份 CAS 注册/撤销） | bridge sessionRegistry deleteSessionIfSame 纪律 | sessionId 贯通：Execute 路由到会话自己的活 actor |

**不镜像的取舍**（记录）：grok-build 的 prompt_queue（并发排队）→ 本 driver rail 采用 lease 快速失败（errTurnBusy）：调用方串行 turn，交错 turn 会搅乱投影 execution 状态。1.0.13 wire 形状（hostTurn 戳在 update 级 _meta、两层 meta）以 p6-turns.json 实证为准，非 1.0.16 源码推论。

## 交付物（文件）

| 文件 | 内容 |
|---|---|
| `agent/grokbuild/turn_dispatch.go` | turnDispatch 状态机（promote/settle/settleForRequest/settleActive/collectHostText）；`parseHostTurnChunk`（update 级 hostTurn 戳）；grokSession glue：`dispatchTurn`（Send+Execute 共用；promote 先于 write，保留 P0-2 修复）、`settlePromptResponse`（stopReason/_meta 解析）、`emitTurnTerminal`、`abandonTurn` |
| `agent/grokbuild/live_sessions.go` | Agent 活 actor 注册表（register/get/CAS unregister）+ Agent 访问器（nil-safe 懒建） |
| `agent/grokbuild/session.go` | Send 收敛到 dispatchTurn；handleResponse 走 dispatcher 仲裁并保留 stopReason；`session/cancel` 通知 settle；readLoop 退出 abandon + 撤销注册；newGrokSessionACU 握手后注册（List pull child 不注册）；Close settle-without-emit + 撤销 |
| `agent/grokbuild/session_commands.go` | `ExecuteSessionCommand` 真实现：单行 slash 校验 → D1 双门（准入表 ∩ 会话官方目录缓存）→ 活 actor → 共用 dispatcher 等待终态；end_turn→success+官方正文、cancelled→错误、ctx 到期→尽力取消；readiness 翻真（init） |
| `core/message.go` | `Event.StopReason`/`Event.CancellationCategory` 字段 |
| `go-bridge/events.go` | turn_completed payload 透传 stopReason/cancellationCategory（无值键缺省） |
| `agent/grokbuild/grokbuild.go` | Agent `live`/`liveInitMu` 字段 |

## 测试矩阵

定向（agent/grokbuild `session_commands_execute_test.go` + 既有更新，14 个）：

| 测试 | 证明 |
|---|---|
| TestTurnDispatchSettleOnce | 单次结算 + epoch 防重（第二次 settle 丢弃；re-promote 后旧 epoch 结算丢弃） |
| TestTurnDispatchSettleForRequestStaleID | 陈旧响应 id 不得结算活 turn |
| TestTurnDispatchLeaseBusy | operation lease：第二个 promote → errTurnBusy |
| TestTurnDispatchHostTextCollection | 正文收集仅在 turn 在飞时；settle 携带全文；settle 后迟到 chunk 不收 |
| TestParseHostTurnChunkShapes | update 级 hostTurn 戳（1.0.13 实证）；模型正文/transport 级误戳不判为 host 反馈 |
| TestLiveSessionsRegistry | 注册/活占位/死替换/CAS 撤销不误逐替换者 |
| TestExecuteHostCommandEndTurn | e2e：活 actor + 目录缓存 → /hooks-list → success + 官方正文 resultText；终态事件保留 end_turn + tokens；无第二终态 |
| TestSendPreservesCancelledStopReason | Send 终态事件 StopReason=cancelled + CancellationCategory=MidTurnAbort（p6 D 形状） |
| TestAgentSelfCancelSettlesExactlyOnce | agent 自取消通知先结算、迟到响应不产生第二终态（跨轨 settle-once） |
| TestEOFDoesNotSettleSuccess | peer 中途 EOF → 终态 EventError(errTurnActorDead)，槽位释放（EOF 不成功） |
| TestExecuteEOFReturnsErrorNotHang | Execute waiter 在 actor 死亡时收错误不悬挂 |
| TestTurnLeaseRejectsSecondDispatch | Send 双发 lease 拒绝；settle 后 lease 释放 |
| TestExecuteRequiresLiveActor | 冷会话（无活 actor）Execute 显式失败 |
| TestExecuteSessionCommandFailsClosed | 准入/liveness 各层拒绝文案钉死（excluded/not admitted/no catalog/非 slash 行） |
| TestSessionCommandsAdvertiseGate（更新） | p1b 门开广告；门关不广告 |

go-bridge：TestMapAgentEventTurnCompletedPreservesStopReason（turn_completed payload 带官方 stopReason/cancellationCategory；无值键缺省）。

## 运行记录

- `go build ./...` exit=0；`go vet ./agent/grokbuild/ ./core/ ./go-bridge/` exit=0
- 定向：`go test ./agent/grokbuild/ -run 'TestTurnDispatch|TestParseHostTurn|TestLiveSessions|TestExecute|TestSend_Preserves|TestAgentSelfCancel|TestEOF|TestTurnLease|TestSessionCommandsAdvertiseGate|TestExecuteSessionCommandFailsClosed' -count=1` → ok
- 回归：`go test ./agent/grokbuild/ ./go-bridge/ ./core/ -count=1` → ok（29.9s / 74.8s / 0.5s）
- go-bridge 定向：TestMapAgentEventTurnCompletedPreservesStopReason → ok
