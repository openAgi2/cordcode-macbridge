# 架构健康第四轮（最终轮）开发交接文档

日期：2026-07-04

输入来源：
- `docs/2026-07-04-architecture-health-third-round-development-brief完成情况.md`
- `docs/2026-07-04-architecture-health-third-round-completion-audit.md`
- `docs/2026-07-04-architecture-health-goal-gap-analysis.md`
- `docs/2026-07-04-architecture-health-second-round-gap-analysis.md`
- `../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+Generation.swift`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+MessageSync.swift`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+CodexStreaming.swift`
- `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift`
- `scripts/hygiene-baseline.json`
- `docs/2026-07-04-architecture-health-fourth-final-round-brief-评审.md`

本文定位：给第四轮开发 agent 的直接施工输入。owner 已明确要求“把第四轮当成最后一轮，否则会无限派生下一轮”。本文据此把第四轮定义为本次架构健康专项的收口轮：它不承诺清空所有历史债务，而是把第三轮后剩余的最高风险 iOS turn-sync gap 做成可测试、可门禁、可交付的闭环。第四轮完成后，本次专项关闭；未来若出现新的系统性 gap，可另立新专项，但不得把本文未做事项继续包装成“第五轮架构健康”。

---

## 0. 核心判断

前三轮已经完成：

- 动作 1：杀双写，完成；
- 动作 2：删除 config 死重，完成；
- 动作 4：web 共享包 5/5，完成；
- 动作 3：`handlers.go` 物理分发 + `BridgeProvider` transport creation 子域提取，部分完成；
- 动作 5：工程宪法 + 1 条 strict gate，部分完成。

第三轮后仍然最大的真实 gap 不是继续机械拆文件，而是**MacBridge 与 iOS 的 turn/session/history/live event 状态模型没有显式边界**。2026-07-04 的 Claude Code 冷启动流式输出反复从头刷新问题已经由 iOS `e018cb5f Fix Claude local stream history overwrite` 单点修复，并经 owner 真机复测通过；日志与 think.md 共同反证：Mac runtime 没有重复 `send_message`，真正症状主因是 iOS 在本地 live stream 中途执行普通 `loadMessages/get_session_messages`，把权威历史覆盖到了本地正在流式增长的 timeline。

因此第四轮不是修一个仍活跃的 bug，而是把这个已修问题背后的 Claude-only guard 重构并泛化为 backend-agnostic policy：避免同类竞争在 Codex/OpenCode、session switch、running-session polling 或未来入口上回涨。

第四轮主轴：

> **Chat turn sync state-model hardening：把 iOS 的本地发送、live event、history sync、running-session polling、session switch 的互斥/优先级规则提取成显式 policy/coordinator，并用定向测试和 strict gate 防回涨。**

一句话范围：

> 第四轮只收敛 iOS ChatViewModel 中与 turn 生命周期、history sync、local/live ownership 相关的状态模型边界；同步更新跨仓活文档与 hygiene gate。它不继续扩张到 `ChatUIKitContainerView`、`agent/claudecode`、`appserver_session` 或新的 BridgeProvider 子域。

第四轮完成后，本次架构健康专项关闭。剩余大文件作为普通维护债记录，不再派生“第五轮架构健康”；但这不禁止未来针对新证据、新事故或新系统性 gap 另立独立专项。

---

## 1. 当前真值与最终轮取舍

### 1.1 当前 god-object 度量

2026-07-04 当前实测：

| 文件 | 当前行数 | 第四轮处理 |
|---|---:|---|
| `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift` | 1629 | 不继续拆；保留第三轮 strict gate |
| `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+Generation.swift` | 2308 | 本轮重点：抽出 turn sync / local send policy |
| `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+MessageSync.swift` | 1480 | 本轮重点：history sync gate 规则显式化 |
| `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+CodexStreaming.swift` | 1426 | 本轮触碰：live event 对 policy 的调用点 |
| `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift` | 1021 | 本轮触碰：session switch / loadMessages 入口统一规则 |
| `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift` | 4371 | 不做 |
| `go-bridge/handlers.go` | 3272 | 不做 |
| `agent/claudecode/claudecode.go` | 1911 | 不做 |
| `agent/codex/appserver_session.go` | 1805 | 不做 |

取舍理由：

- `BridgeProvider` 已有第三轮 extract-and-test 证明闭环，继续拆它会变成“永远还有下一个子域”；
- `ChatUIKitContainerView` 体量最大，但今天暴露的真实产品风险不在 UI 结构，而在 generation/history/live 状态覆盖；
- Mac `claudecode.go` 和 `appserver_session.go` 仍大，但这次事故已由日志证明 Mac 没有重复发送，优先级低于 iOS 状态模型；
- `ChatViewModel` 不应在第四轮做全量大拆，只抽与 turn sync 直接相关的 policy/coordinator，避免把最终轮膨胀成另一个无限项目。

### 1.2 第四轮的完成定义

本轮不以“所有大文件低于某个理想行数”为完成标准。本轮以以下事实为完成标准：

1. iOS 的 local send / live event / history sync / polling / session switch 之间有一个显式状态模型入口；
2. 中途 `loadMessages` 是否允许覆盖本地 live timeline 不再散落在多个 extension 的 ad hoc 条件里；
3. 07-04 Claude 冷启动重复从头输出回归测试在 policy 接管后仍通过，并新增 ownership 并发交错用例；
4. `ChatViewModel+Generation.swift` 和 `ChatViewModel+MessageSync.swift` 的关键指标建立 strict net-growth gate；
5. `IOS_MAC_INTERACTION_FLOW.md` 和 MacBridge 文档同步写清状态模型边界；
6. 定向 tests/build/真机安装按仓库规则完成；
7. `isClaudeCodeLocalSendInProgress` / `allowDuringClaudeLocalSend` 在生产路径被 policy 取代，或完成报告逐项解释无法移除的原因；
8. 既有 07-04 CHANGELOG 条目被修订到与 think.md 根因口径一致；
9. 完成报告明确宣布本次架构健康专项 closed，不再提出第五轮；未来新系统性 gap 可另立专项。

---

## 2. 必读文件与硬约束

开发前必须读：

1. 本仓 `AGENTS.md` 中 Build & test、Backend runtime model、CHANGELOG 规则。
2. `GO_BRIDGE_ARCHITECTURE.md` 中 Claude/Codex/OpenCode 的事件与 polling 边界。
3. `../cordcode-ios/CLAUDE.md`。
4. `../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md` 第 3-7 节。
5. `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel.swift`。
6. `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+Generation.swift`。
7. `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+MessageSync.swift`。
8. `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+CodexStreaming.swift`。
9. `../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift`。
10. 已有定向测试：`RemoteRunningSessionTests.swift`、`CodexSeamTests.swift`、`MessageDeduplicationTests.swift`、`SessionSwitchCancelTests.swift`、`LoadingMaskLoopTests.swift`、`ExecutionStateSemanticsTests.swift`、`ClaudeProbeBaselineTests.swift`。

硬约束：

- 未经 owner 当前任务明确允许，不运行 UI tests、snapshot tests、simulator automation 或自动操作真机 UI。
- iOS Swift 代码改动后，若检测到 connected physical iPhone，交付前必须按 iOS 仓规则自动执行真机 Debug 构建、安装、启动；这不授权 UI 操作。
- 不在生产路径添加 fallback、mock、placeholder、假数据或缓存快照来掩盖真实失败。
- 不改变 Bridge wire protocol、pairing、Relay HPKE/mailbox、Tailscale pin、backend capability 字面契约。
- 不修改 Mac backend 的 send/stream 语义来掩盖 iOS 状态模型问题。
- 不把 `ChatUIKitContainerView`、`BridgeProvider` 下一子域、`claudecode.go`、`appserver_session.go` 加入第四轮。
- 不把“未来可继续拆”写成第五轮建议；完成报告只能记录普通维护债，不得把本文未做事项包装成新一轮架构健康专项。

---

## 3. 目标状态模型

第四轮必须把下列规则落实到代码与文档。命名可按现有风格调整，但语义不能降级。

### 3.1 Turn ownership

每个当前聊天 session 在 iOS 侧最多有一个 turn ownership：

| Ownership | 含义 | 允许的写入来源 |
|---|---|---|
| `.none` | 当前 session 没有 iOS 认定的活跃 turn | `loadMessages` 可权威覆盖，session switch 可重建 |
| `.localSend` | iOS 当前进程发起的 sendMessage 仍在进行或等待最终 reconcile | live delta / send response / completion recovery 可写；普通 history sync 不可覆盖 timeline |
| `.remoteLive` | Mac 侧广播或 runningSessions 表明外部 turn 正在运行 | live event / running polling 可写；history sync 只能 merge，不可误判完成 |
| `.reconciling` | turn 已结束，正在用权威历史做最终收敛 | 允许一次显式 allow 的 `loadMessages`，完成后回 `.none` |

现有 `isGenerating` 只能是 UI 派生状态，不能继续承担完整 lifecycle 判断。

### 3.2 History sync gate

所有 `loadMessages` 入口必须先经过同一个 policy 判断。policy 至少返回：

- `.allowAuthoritativeLoad`：可拉历史并按现有规则替换/merge；
- `.allowFinalReconcile`：仅由 send completion / turn completion / explicit recovery 触发；
- `.deferBecauseLocalLiveTurn`：本地发送流式中，普通 history sync 直接跳过，不触发网络拉取；
- `.mergeOnlyBecauseRemoteRunning`：外部运行中，只能做幂等 merge，不能清空/完成当前 live timeline；
- `.rejectStaleSession`：session 已切换或 initializationID 过期，丢弃结果。

`LoadTrigger` 合法值必须显式枚举，至少包括：

| Trigger | 来源 |
|---|---|
| `.userOpenedSession` | 用户显式打开或切换 session 后的首次权威加载 |
| `.manualRefresh` | 用户或开发入口显式刷新 |
| `.runningSessionPolling` | `startRunningSessionPolling` 或等价 running 状态轮询 |
| `.todoRefresh` | todo refresh 间接触发的消息同步 |
| `.sendCompletionRecovery` | `sendMessage` response / failure / completion 后的最终收敛 |
| `.liveTurnCompleted` | live event `turnCompleted` / session idle 后的最终收敛 |
| `.reconnectReconcile` | transport reconnect / hello_ack 后的权威 reconcile |
| `.snapshotRecovery` | 冷启动 snapshot miss / fallback 后的权威加载 |

任何新增 `loadMessages` 入口都必须选择一个 trigger 并经过 policy；完成报告要列出 `loadMessages(` 调用点盘点结果，说明哪些是 public wrapper，哪些是真正触发网络/应用历史的入口。

注意：第四轮可以在迁移过程中临时保留 `allowDuringClaudeLocalSend` 这类参数作为兼容壳，但 Phase B 完成时，生产路径不得继续依赖 `isClaudeCodeLocalSendInProgress` / `allowDuringClaudeLocalSend` 作为独立真值。它们必须被移除、退化为 policy wrapper，或只留在测试 fake 中；若无法移除，完成报告必须逐项解释原因与后续删除条件。

### 3.3 Backend-specific input, backend-agnostic policy

Claude Code、Codex、OpenCode 的差异只能作为 policy 的输入：

- Claude Code：本地 CLI 子进程 live stream + 历史 polling 并存；
- Codex：app-server live event 优先，运行中 session 可用 history 恢复；
- OpenCode：SSE live event 优先，descriptor 决定 polling 兜底。

policy 不应硬编码“Claude 就跳过所有 loadMessages”这类粗规则；它应根据 ownership、session、turn source、trigger reason、backend capability/running state 决定。

### 3.4 Session switch boundary

`switchSession` 是 ownership 的强边界：

- 离开旧 session 必须取消旧 session 的 local/live ownership、polling task、pending load task；
- 进入新 session 时，只有新 session 的 initializationID 可以应用 history/load/live result；
- 旧 session 迟到的 live event、history load、todo refresh 不得重新激活当前 session 的 generation 状态。

---

## 4. 推荐实现形态

### 4.1 新增 iOS 类型

推荐新增：

```text
../cordcode-ios/OpenCodeiOS/OpenCodeiOS/ViewModels/ChatTurnSyncPolicy.swift
```

或如果现有 ViewModel 目录更适合，也可命名为：

```text
ChatViewModel+TurnSyncPolicy.swift
```

优先使用独立类型，而不是继续堆 extension。建议形态：

```swift
@MainActor
struct ChatTurnSyncPolicy {
    enum Ownership: Equatable { ... }
    enum LoadTrigger: Equatable { ... }
    enum LoadDecision: Equatable { ... }

    func decideLoadMessages(
        sessionId: String,
        currentSessionId: String?,
        initializationID: UUID?,
        currentInitializationID: UUID?,
        ownership: Ownership,
        trigger: LoadTrigger,
        backendKind: BackendKind?,
        runningState: RunningSessionState?
    ) -> LoadDecision
}
```

如果现有类型名不完全匹配，可用本地已有 model 替代；不要为了该 policy 新造生产 mock backend。

### 4.2 ViewModel state holder

在 `ChatViewModel.swift` 或窄 extension 中集中保存当前 turn ownership，建议新增一个小的 state holder：

```swift
@MainActor
final class ChatTurnSyncState {
    var ownershipBySession: [String: Ownership]
    var lastAcceptedInitializationIDBySession: [String: UUID]
    ...
}
```

如果引入 class 过重，可以先用 `ChatViewModel` 内的私有属性，但必须通过少数 helper 读写，不允许每个 extension 直接拼条件。

ownership 的读写必须是 `@MainActor` 上的同步状态机操作：

- live event、sendMessage、send completion、session switch、history load apply 对 ownership 的 set/clear 必须在同一 MainActor 隔离域内完成；
- `loadMessages` 入口读取 ownership 并做 policy 判决时，不得在读 ownership 与记录本次 load token 之间跨 `await`；
- history fetch 可以跨 `await`，但 apply 前必须再次用同一个 policy/token 复核 ownership、sessionId、initializationID；
- 不允许把 ownership 快照传到后台 task 后无复核地应用结果；
- 如果某条路径必须跨 actor 或跨 task，完成报告必须说明复核点与测试覆盖。

这条是硬约束。07-04 问题本质是 live stream append 与 async history load apply 的竞争；policy 如果没有 MainActor 原子读写和 apply 前复核，只是把 race 换了名字。

建议实现骨架如下，实际命名可按现有代码调整，但流程不能省略。`turnSyncState` 是本节的 state holder，其 `decideLoad` / `beginLoadIfAllowed` 是对纯 policy `decideLoadMessages` 的 MainActor 包装：state holder 内部读取自身 ownership / currentSessionId / currentInitializationID 后调用 policy 纯函数，避免每个调用点重复拼参数；policy 自身仍是 §4.1 那个不访问 `ChatViewModel`、无副作用的纯函数。

```swift
let decision = turnSyncState.decideLoad(sessionId, trigger, initializationID)
guard let token = turnSyncState.beginLoadIfAllowed(decision, sessionId, initializationID) else {
    return  // .deferBecauseLocalLiveTurn / .rejectStaleSession 等判决必须在网络请求前直接返回
}
let result = try await backendClient.getSessionMessages(...)
guard turnSyncState.canApply(token, sessionId, initializationID) else { return }
applyLoadedMessages(result, decision: decision)
turnSyncState.finishLoad(token)
```

重点是 `decideLoad` / `beginLoadIfAllowed` 与 ownership 读取在同一 MainActor 同步段内完成且不跨 `await`；`.defer*` / `.reject*` 类判决经 `beginLoadIfAllowed` 返回 nil，在网络请求前短路；`canApply` 在 fetch 返回后再次复核 ownership、sessionId、initializationID。

### 4.3 调用点收敛

必须改到同一个 policy 入口的调用点：

- `ChatViewModel+MessageSync.loadMessages(...)`；
- `ChatViewModel+Generation.sendMessage(...)` 中创建 local send / placeholder / completion recovery 的位置；
- `ChatViewModel+Generation.recoverAfterSendCompletion(...)`；
- `ChatViewModel+Generation.startRunningSessionPolling` 或等价 polling 循环；
- `ChatViewModel+CodexStreaming.handleCodexLiveEvent(_:)` 中 turn started/completed/sessionStateChanged/message delta 相关分支；
- `ChatViewModel+SessionManagement.switchSession(...)` 与其中的 load task 启动/取消点。

必须盘存并复用现有 staleness guard，例如 `isInitializationCurrent(initializationID)`、`currentSessionId == sessionId`、已有 load task cancellation。policy 不得另起一套与这些 guard 并行的重复判决；正确形态是把它们作为 policy 输入或 apply 前复核的一部分。

允许保留现有 public 方法签名，内部转发到 policy；不要求一次改动所有调用者 API。

### 4.4 禁止形态

第四轮不得交付以下形态：

- 只在 `loadMessages` 再加一个 backend-specific if，未形成统一 policy；
- 用 timer、sleep、延迟几十秒规避流式覆盖；
- 用本地缓存快照冒充权威历史；
- Mac 端重复抑制 `send_message` 来掩盖 iOS 重放；
- 把所有 Claude `loadMessages` 禁掉，导致完成后无法权威 reconcile；
- 用 UI 状态文字是否显示 runtime strip 作为核心判断；测试应检查 ViewModel 数据与 network call 计数。

---

## 5. 测试计划

第四轮必须先补 policy 级小测试，再改调用点。测试应保持定向，不跑 UI/snapshot/simulator automation。

### P0：现状保护与 policy 单测

新增或扩展：

```text
../cordcode-ios/OpenCodeiOS/OpenCodeiOSTests/ChatTurnSyncPolicyTests.swift
```

覆盖最小集合：

1. local send in progress + ordinary history refresh -> `.deferBecauseLocalLiveTurn`；
2. local send completed + final reconcile trigger -> `.allowFinalReconcile`；
3. remote running + history polling -> `.mergeOnlyBecauseRemoteRunning`；
4. stale initializationID -> `.rejectStaleSession`；
5. no ownership + explicit user/session load -> `.allowAuthoritativeLoad`；
6. session switch clears old ownership and rejects late old-session load result。

P0 只测试纯 policy，不改生产调用点；这里验证的是 `.rejectStaleSession` 等纯函数判决，不替代 P3 的 ViewModel 集成测试。

P0 还必须冻结 policy 的纯函数性：`decideLoadMessages` 只能根据显式入参返回 `LoadDecision`，不得访问 `ChatViewModel`、不得读写全局状态、不得发起网络请求、不得产生副作用。ownership 的 set/clear 属于 state holder 职责，不属于 policy 纯函数职责。

### P1：Claude 冷启动流式回归测试

在 `RemoteRunningSessionTests.swift` 或新增 `ChatTurnSyncIntegrationTests.swift` 中覆盖 2026-07-04 真机 bug：

场景：

1. App 冷启动后打开已有 Claude Code session；
2. iOS local send 进入 `.localSend`；
3. 模拟 live delta 已追加一段 assistant 内容；
4. 普通 `loadMessages` / running polling / todo refresh 在中途触发；
5. 断言没有发起 `get_session_messages`，或即使 fake client 被调用也不得替换当前 assistant partial；
6. 模拟 turn completed / session idle；
7. final reconcile 显式 allow，权威历史可以应用；
8. 状态回到 idle，输入框不再恢复成执行中。

必须额外覆盖一个交错窗口：history fetch 已经发起但尚未 apply 时，ownership 翻转为 `.localSend` 或 session initializationID 变旧；此时返回的历史结果不得替换 live partial，也不得把 generation 状态错误置为 idle/running。这个用例用于证明 apply 前复核真实存在。

必须覆盖“第二次问题正常”的原因：同 session 后续 turn 不能因为第一次 local ownership 残留而永久阻止 history sync。

### P2：Codex/OpenCode live event 非回归

Phase A 必须先给出 Codex/OpenCode parity 调研结论：阅读 delta apply 路径、session id / item id 对齐规则和 history merge 入口，判断它们是否存在与 Claude 等价的 timeline 覆盖风险。

复用已有 `CodexSeamTests.swift`、`ExecutionStateSemanticsTests.swift`、`StreamingOptimizationTests.swift` 中的 live event 用例，至少新增/确认：

- Codex `assistantMessageDelta` 后，普通 history sync 不清空 partial；
- Codex `turnCompleted` 后 final reconcile 仍运行；
- OpenCode/SSE 类 live event 如果已有测试 harness，覆盖同类 ownership 转移；如果没有，不为测试造生产 mock 路径，只在现有 fake backend client 层补单测。

如果调研结论是 Codex/OpenCode 没有等价覆盖风险，本轮仍保留 backend-agnostic policy 入口，但 Codex/OpenCode 可走 `.allowAuthoritativeLoad` 直通；完成报告必须诚实写明“统一入口，Claude 实质受益，Codex/OpenCode 无需行为变更”，不得假造 parity 风险。

如果调研结论是 Codex/OpenCode 存在等价覆盖风险，Phase B/C 的调用点收敛必须显式覆盖对应 backend，不得只修 Claude；完成报告要列出新增覆盖路径与定向测试。

### P3：Session switch 边界测试

扩展 `SessionSwitchCancelTests.swift` 或 `LoadingMaskLoopTests.swift`：

- switch from session A to B cancels A ownership；
- A 的迟到 `loadMessages` result 不得覆盖 B；
- A 的迟到 live event 不得重新激活 `isGenerating`；
- B 的 initial authoritative load 不被 A 的 local send gate 错误阻塞。

P3 是 ViewModel 集成层测试，验证真实 `switchSession`、load task cancellation、live event handler 和 apply 前复核能共同工作；它不与 P0 的纯 policy staleness 用例重复。

### P4：测试命令

建议定向命令：

```bash
cd ../cordcode-ios
xcodebuild test -project OpenCodeiOS/CordCode.xcodeproj -scheme CordCode \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' \
  -only-testing:CCCodeTests/ChatTurnSyncPolicyTests \
  -only-testing:CCCodeTests/RemoteRunningSessionTests \
  -only-testing:CCCodeTests/CodexSeamTests \
  -only-testing:CCCodeTests/ExecutionStateSemanticsTests \
  -only-testing:CCCodeTests/SessionSwitchCancelTests \
  -only-testing:CCCodeTests/LoadingMaskLoopTests
```

如果某测试类耗时过高，可先跑新增测试和受影响类，再在完成报告中说明未跑的类与原因。不得用 UI automation 替代这些 ViewModel/unit tests。

本节的 simulator destination 只用于运行 unit test target；它不授权 UI tests、snapshot tests、simulator automation 或自动 UI 操作。禁止的是视觉/UI 自动化，不是 `xcodebuild test` 使用 simulator destination。

---

## 6. 实施阶段

### Phase A：冻结现状与补 policy 小测试

1. 记录当前行数：
   - `ChatViewModel+Generation.swift`；
   - `ChatViewModel+MessageSync.swift`；
   - `ChatViewModel+CodexStreaming.swift`；
   - `ChatViewModel+SessionManagement.swift`。
2. 盘点现有 guard：`isClaudeCodeLocalSendInProgress`、`allowDuringClaudeLocalSend`、`isInitializationCurrent(initializationID)`、`currentSessionId == sessionId`、load task cancellation、generation state transitions。
3. 盘点 `loadMessages(` 调用点，按 trigger 分类，区分 public wrapper 与真正触发网络/应用历史的入口。
4. 调研 Codex/OpenCode live event 与 history merge 是否存在等价 timeline 覆盖风险，形成一段结论写入完成报告。
5. 新增 `ChatTurnSyncPolicyTests.swift`，先让纯 policy 测试表达目标语义。
6. 若 policy 尚未接入生产代码，允许测试先驱动新类型；不允许为测试在生产路径添加假数据。

完成条件：

- 新 policy 类型存在；
- P0 policy tests 通过；
- 既有 guard 与 `loadMessages` 调用点盘点完成；
- Codex/OpenCode parity 调研完成；
- 若 parity 调研发现 Codex/OpenCode 等价覆盖风险，Phase B/C 必须显式纳入该 backend；若无风险，完成报告按 P2 要求说明直通原因；
- 尚未改调用点时，不声明 bug 已修。

### Phase B：接入 `loadMessages` 与 local send ownership

1. 在 `sendMessage` 进入真实发送前设置 `.localSend`；
2. 在 send 失败、取消、completion recovery 后清理或转入 `.reconciling`；
3. `loadMessages` 入口统一调用 policy；
4. 普通 history sync 遇到 `.deferBecauseLocalLiveTurn` 必须在网络请求前返回；
5. `recoverAfterSendCompletion` 使用 explicit final reconcile trigger，不再依赖 ad hoc `allowDuringClaudeLocalSend` 语义。

完成条件：

- 2026-07-04 Claude 冷启动回归测试在 policy 接管后仍通过；
- 新增 history fetch 已发起但 apply 前 ownership/session 变旧的交错测试，并通过；
- `loadMessages` 中不再直接散落 Claude-only local send 判断；
- `isClaudeCodeLocalSendInProgress` / `allowDuringClaudeLocalSend` 的生产调用点被 policy 取代、移除或退化为 policy wrapper；若保留，完成报告必须逐项解释原因；
- final reconcile 仍会拉权威历史。

### Phase C：接入 live event / polling / session switch

1. `handleCodexLiveEvent` 的 turn started/completed/session state 分支更新 ownership；
2. running-session polling 在 policy 拒绝时不拉历史、不恢复 runtime strip；
3. session switch 清理旧 ownership，并用 initializationID 拦截迟到结果；
4. todo refresh 不得通过隐式 `loadMessages` 重新激活 generation。

完成条件：

- Codex/OpenCode live event 相关定向测试通过；
- session switch 迟到结果测试通过；
- `isGenerating` 的变化仍由 generation state 驱动，但不再作为唯一 truth。
- 列出受影响既有测试及调整说明，尤其是 `MessageDeduplicationTests` / `ExecutionStateSemanticsTests` / `RemoteRunningSessionTests` 中因 policy 接管而更新的断言。

### Phase D：gate 与文档收口

在 MacBridge 仓扩展 `scripts/hygiene-baseline.json` 和 `scripts/check-architecture-hygiene.sh`：

1. 保留第三轮 `BridgeProvider` strict gate；
2. 新增 `ChatViewModel+Generation.swift` net-growth baseline；
3. 新增 `ChatViewModel+MessageSync.swift` net-growth baseline；
4. 默认 warning-only inventory 可继续存在，但这两个新增 baseline 在 `CORDCODE_HYGIENE_STRICT=1` 下必须 fail on net growth；
5. baseline 以第四轮完成后的实测值为准，允许减少，不允许净增。

建议计数：

```text
lines = wc -l
funcs = grep -wo 'func' | wc -l
turnSyncAdHocMarkers = grep -E 'allowDuringClaudeLocalSend|isClaudeCodeLocalSendInProgress|loadMessages\\(' 的人工说明，不建议作为硬 gate 第一版
```

更新文档：

- `../cordcode-ios/IOS_MAC_INTERACTION_FLOW.md`：补“Turn ownership / history sync gate / final reconcile”小节；
- 本仓 `GO_BRIDGE_ARCHITECTURE.md`：补 Claude/Codex/OpenCode live event vs history polling 的 iOS 消费边界；
- 本仓 `CHANGELOG.md`：修订既有 2026-07-04 Claude 冷启动条目，把 Mac `relayRunningKind` 拆分标为 latent bug / 独立 hardening，把 iOS `loadMessages` 覆盖标为症状主因；再在 `[Unreleased]` 下记录第四轮 policy 硬化；
- `../cordcode-ios/CHANGELOG.md`：确认 07-04 iOS 单点修复与第四轮 policy 硬化口径一致；
- 新增第四轮完成报告：`docs/2026-07-04-architecture-health-fourth-final-round-development-brief完成情况.md`。

完成条件：

- strict gate 通过；
- 文档与代码语义一致；
- CHANGELOG 与 think.md 根因口径一致；
- 完成报告明确“本次架构健康专项 closed，未来新系统性 gap 另立专项，不派生第五轮”。

### Phase E：构建、安装、提交

iOS 代码修改后：

1. `xcrun devicectl list devices` 检查 connected physical iPhone；
2. 若有连接真机，运行 iOS 仓规定的真机构建安装命令；
3. 不做 UI automation / snapshot / 自动点击；
4. 完成报告写明 unit tests、build、真机安装已执行或未执行原因。

提交边界：

- iOS 仓：policy/coordinator + ViewModel 调用点 + tests + iOS docs/CHANGELOG，一条或多条清晰 commit；
- MacBridge 仓：第四轮 brief/完成报告 + hygiene gate + Mac 活文档/CHANGELOG，一条 commit；
- 不把 handoff、临时日志、DerivedData、xcresult、个人环境文件提交。

---

## 7. 验收标准

第四轮完成时必须同时满足：

- `ChatTurnSyncPolicy` 或等价独立类型存在，且生产调用点接入；
- 普通 history sync 在 local live turn 中不会拉取/覆盖当前 streaming timeline；
- final reconcile 在 turn completion 后仍可拉取并应用权威历史；
- session switch 能拒绝旧 session 的迟到 load/live result；
- Codex/OpenCode live event 路径不因 Claude 修复被降级；
- 生产路径不再依赖独立 Claude-only guard 作为 history sync 真值；如保留 wrapper，完成报告解释原因；
- ownership 读写与 history apply 前复核均在 MainActor 边界内实现并有交错测试覆盖；
- 新增/更新的定向 tests 通过；
- `CORDCODE_IOS_ROOT=../cordcode-ios CORDCODE_HYGIENE_STRICT=1 scripts/check-architecture-hygiene.sh` 通过；
- iOS 代码改动后完成定向 build；如有 connected physical iPhone，完成安装启动；
- `IOS_MAC_INTERACTION_FLOW.md` 与 `GO_BRIDGE_ARCHITECTURE.md` 写清状态模型边界；
- 既有 07-04 CHANGELOG 条目已修订到与 think.md 根因一致；
- 完成报告落盘，并明确本次专项关闭，不再派生下一轮；未来新系统性 gap 可另立专项。

---

## 8. 明确不做

第四轮不做：

- 不继续拆 `BridgeProvider`；
- 不拆 `ChatUIKitContainerView.swift`；
- 不拆 `agent/claudecode/claudecode.go`；
- 不拆 `agent/codex/appserver_session.go`；
- 不继续细分 `go-bridge/handlers.go`；
- 不把所有 hygiene warning inventory 升级成 strict；
- 不改 Bridge protocol；
- 不改 Relay server；
- 不做 UI/snapshot/simulator automation；
- 不把真机肉眼验收写成自动完成。

这些不是“留给第五轮”的任务。它们作为普通维护债进入日常 backlog；本次架构健康专项到第四轮结束。若未来出现新的系统性事故或新证据，需另立专项并重新定义范围，不能把本文明确不做项自动续成下一轮。

---

## 9. 完成报告模板要求

第四轮完成报告必须包含：

1. 本轮目标与最终轮声明；
2. 关键文件变更；
3. 状态模型规则落地说明；
4. 测试矩阵与命令输出摘要；
5. strict gate 输出摘要；
6. iOS 真机构建/安装状态；
7. 未做事项和原因；
8. 两仓 commit hash；
9. `Closed` 结论：本次架构健康专项停止，不生成第五轮；未来新系统性 gap 另立专项。

推荐文件：

```text
docs/2026-07-04-architecture-health-fourth-final-round-development-brief完成情况.md
```

---

## 10. 评审处理记录

评审报告：`docs/2026-07-04-architecture-health-fourth-final-round-brief-评审.md`。

| ID | 评审意见 | 处理 | 说明 |
|---|---|---|---|
| H1 | 07-04 bug 已由 iOS `e018cb5f` 单点修复，简报不应写成活跃 bug | 采纳 | 第 0 节改为“已修问题的结构性硬化”，完成定义改为 policy 接管后回归仍通过 |
| H2 | “closed / 不生成第五轮”治理越权 | 部分采纳 | owner 已明确要求第四轮当最后一轮，因此保留本次专项收口；同时软化为不堵死未来新系统性 gap 另立专项 |
| H3 | CHANGELOG 把症状主因归到 Mac relay hardening，与 think.md 冲突 | 采纳 | Phase D 增加修订既有 07-04 CHANGELOG 条目，区分 latent bug 与症状主因 |
| H4 | ownership 并发/原子性未规定 | 采纳 | 第 4.2 节新增 MainActor 同步读写、fetch 后 apply 前复核硬约束；P1 增加交错测试 |
| H5 | Claude-only guard 退场无死线 | 采纳 | 第 3.2 / Phase B / 验收标准要求生产路径由 policy 接管，保留需解释 |
| H6 | Codex/OpenCode parity 缺证据 | 采纳 | Phase A 增加 parity 调研；P2 允许统一入口但行为直通，并要求完成报告诚实说明 |
| H7 | `ChatViewModel+MessageSync.swift` 行数低估 | 采纳 | 第 1.1 节改为 1480 |
| H8 | 未盘存既有 `isInitializationCurrent` guard | 采纳 | 第 4.3 / Phase A 要求盘存并复用既有 staleness guard |
| H9 | simulator unit test 与禁止 simulator automation 容易混淆 | 采纳 | P4 增加说明：允许 simulator destination 跑 unit test，禁止 UI/snapshot/自动 UI 操作 |

未采纳项：无。H2 未按“完全移除最终轮声明”处理，因为 owner 在提出本方案时已明确要求“把第四轮当成最后一轮”；文档采用“本次专项收口 + 未来新系统性 gap 可另立专项”的折中口径。
