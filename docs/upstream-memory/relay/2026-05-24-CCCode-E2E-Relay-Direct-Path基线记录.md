# CCCode E2E Relay Phase 0 Direct-Path 基线记录

**日期**: 2026-05-24  
**关联方案**: `docs/2026-05-24-CCCode-E2E-Relay与加密离线同步实施方案.md`  
**目的**: 在引入 Relay 之前冻结现有 direct-path 的真实 backend 行为基线，后续 secure channel、relay routing 和离线补投不得改变这些语义。

## 1. 基线边界

- Direct path 仍通过现有 Bridge v1 连接执行真实请求，不经过 Relay。
- Mac/go-bridge/backend 是 session、history、Todo、running state 的权威来源。
- Codex 和 OpenCode 是共享服务事件源；iOS 可旁观其他客户端在同一服务中发起的 turn。
- Claude Code 是独立 CLI 进程模型；iOS 对 Mac 端外部 turn 只能依靠历史轮询恢复，不宣称实时广播。
- 本基线不加入 fallback、假数据、假完成态或离线写排队。

## 2. 代码锚点

| 行为 | 已有实现锚点 | 基线含义 |
| --- | --- | --- |
| backend 事件模型描述 | `go-bridge/agent_descriptor.go` | Claude 标记 external-turn polling；Codex 不需 polling；OpenCode 广播链路保留保护策略。 |
| 事件映射与订阅广播 | `go-bridge/events.go`、`go-bridge/events_test.go` | `todos_updated`、`turn_completed`、passive subscriber 转发必须保持可用。 |
| OpenCode 执行与终止 | `go-bridge/handlers.go`、`go-bridge/handlers_test.go` | send 重用 session 配置，abort 真实触发 backend 终止并清理会话映射。 |
| Codex 续接与 Todo | `go-bridge/handlers.go`、`go-bridge/handlers_test.go` | pending session rebind 到真实 ID，Todo fetch 继续是权威读路径。 |
| iOS backend 选择策略 | `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift` | Codex 进入 external-turn probe；需要保护的 backend 使用历史变化探测。 |
| iOS polling 生命周期 | `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+Generation.swift`、`ChatViewModel+CodexStreaming.swift` | Claude 收到 live content 时不得提前取消 polling；无变化达到阈值后真实收口。 |

## 3. 自动化基线

本阶段允许运行代码级定向单元测试，不运行 UI test、snapshot test 或 simulator automation。

### 3.1 go-bridge

覆盖范围：

- Claude/OpenCode/Codex 的 external-turn event model descriptor。
- `todos_updated`、`turn_completed` 映射及事件 relay。
- broadcaster 对订阅连接、被动事件和 backend 隔离的行为。
- OpenCode send/abort，Codex pending-session rebind 和 Todo 权威读取。

执行命令：

```bash
cd go-bridge && go test ./... -run '^(TestClaudeDescriptorRequiresPolling|TestOpenCodeDescriptorBroadcastRequiresPollingProtection|TestCodexDescriptorBroadcastNoPolling|TestMapAgentEventPlanTodosUpdated|TestMapAgentEventTurnCompleted|TestRelayEventsSendsIdleAfterTurnCompleted|TestRelayEventsSendsIdleAfterError|TestRelayEventsSendsTurnCompletedOnChannelClose|TestBroadcasterSendsToSubscribedConn|TestTwoConnsSubscribeSameSession|TestBroadcasterPassiveEventMatchesSubscriptionWithDirectory|TestBroadcasterFallbackNoCrossBackend|TestOpenCodeSendMessageUsesAgentSessionAndReusesSameConfig|TestOpenCodeAbortGenerationCallsHTTPAbortAndCleansSession|TestCodexPendingSessionRebindsToRealSessionID|TestCodexTodosBridgeFlowKeepsFetchAuthoritativeAfterPlanEvent)$' -count=1
```

结果：`PASS`，`ok go-bridge 0.833s`。

### 3.2 iOS unit tests

覆盖范围：

- descriptor 驱动的 Claude polling 要求。
- Claude 外部 turn 的轮询存活、完成态收口。
- Codex/OpenCode 共享服务场景的 abort 发出。

执行命令：

```bash
xcodebuild -quiet -project OpenCodeiOS/CCCode.xcodeproj -scheme CCCode \
  -destination 'platform=iOS Simulator,id=96A1CCDE-52CE-4444-9F3C-EAA522CD2E8B' \
  test -only-testing:CCCodeTests/CCCodeBridgePollingTests \
  -only-testing:CCCodeTests/RemoteRunningSessionTests
```

结果：`PASS`，结果包位于
`/Users/jacklee/Library/Developer/Xcode/DerivedData/CCCode-ejsbrogigusndlbbsdhkpiidamzg/Logs/Test/Test-CCCode-2026.05.24_16-09-57-+0800.xcresult`。

## 4. 真实跨端验收门禁

以下验收必须在 Mac 的真实 backend 与已配对 iPhone 之间执行。项目约束要求未经用户明确允许不主动运行该类真机/跨端流程，因此本文先固定步骤与证据要求，不预填结果。

| Backend | 操作路径 | 预期真实行为 | 状态 | 证据要求 |
| --- | --- | --- | --- | --- |
| Codex app-server | Mac 发起 turn，iPhone 打开同一 session 旁观 | iPhone 收到 streaming/完成事件，无需以 history polling 替代实时流 | 待授权执行 | Mac 日志、iPhone 录屏或截图、session/turn ID |
| Codex app-server | iPhone 对运行中 turn 执行 stop，随后继续任务并刷新 Todo | stop 终止真实 turn；续接写入真实 session；Todo 与 Mac 一致 | 待授权执行 | abort/result 日志、续接消息、Todo 对照记录 |
| OpenCode | Mac 侧发起 turn，iPhone 旁观并发送后续消息 | SSE 驱动实时刷新与完成态；后续消息进入真实 session | 待授权执行 | SSE/bridge 日志、消息历史对照 |
| Claude Code | Mac Terminal 发起任务，iPhone 打开同一 session | iPhone 通过 polling 观察历史变化；最终消息与 Todo/状态正确，不伪装 broadcast | 待授权执行 | polling 日志、JSONL/消息历史对照、最终状态 |

## 5. Exit 标准

Phase 0 direct-path regression 仅在以下事实都有证据时才可标记完成：

1. 上述定向 Go/XCTest 通过。
2. 三种 backend 的真实跨端验收均记录可核验结果，或每个无法执行项有明确外部阻塞记录。
3. Relay 后续实现的队列不得在该门禁未完成时启动 Phase 1 开发。

当前本文建立了执行入口和真实行为期望；真机跨端结果必须由实际执行补入，不能以静态阅读或单元测试替代。

**当前门禁状态**: `BLOCKED`。自动化基线已通过；第 4 节跨端验收需要用户明确授权在真实 iPhone 与 Mac backend 上执行后才能继续 Phase 1。
