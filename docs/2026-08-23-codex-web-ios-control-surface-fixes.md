# Codex Web iOS 控制面修复：目录同源 / todo 真值 / 停止与续接

- 日期：2026-08-23
- 计划：`.zcode/plans/plan-sess_e2e6d0da-5f42-44a9-b25c-d8c54ffd0197.md`（P0-1…P0-6 + 真机验证门，已批准）
- 问题来源：真机日志 2026-08-23 18:10–18:34 窗口，三个问题：Mac 新建 session 后 iOS 列表不刷新；长任务 todo dock 不出现/停在第 1 步且「继续」指令基于过期缓存；iOS「停止」对 Mac 发起的 turn 不生效
- 参照：opencode-web（ocProxy 有真实 fetch_todos + iOS 8s 轮询推进，todo dock 正常）
- 状态：**P0-1…P0-6 全部落地并通过双仓单测；真机验证门尚未执行（需 owner 协助），见文末清单**

## 结论

三个问题的根因都不是「事件没送达」，而是分派/数据源断点。本轮在 Mac 侧补齐 codex-web 的能力（真实 todo 数据源、thread/list 同源、观察 turn 直达中断），iOS 侧激活轮询值守与列表自刷新，并让续接指令基于最新计划生成。codex-web 仍保持观察/被动型定位：不进 go-bridge 会话 registry，但桥侧按 threadID 直达官方 `turn/interrupt`，终态以官方 `turn_completed(aborted)` 回填。

## 交付内容

| # | 单元 | 文件（未提交） | 内容 |
|---|---|---|---|
| P0-1 | codex-web 真实 todo 数据源 | `agent/codex-web/session.go`、`agent/codex-web/events.go` | `Agent` 实现 `core.TodoProvider`：`planCache map[string][]core.Todo` 缓存观察到的官方 `EventPlan`（`dispatchEvent` 与订阅解码路径都写）；`FetchTodos` 返回副本、无缓存返回空（不再 `not_supported`）；`DeleteSession` 清缓存；缓存上限 1024，超限重置 |
| P0-3 | 观察 turn 早日中断 | `agent/codex-web/events.go`、`core/interfaces.go`、`go-bridge/handlers.go` | `core.ThreadTurnCanceler` 新接口；`CancelTurnForThread` 用 `liveCodec.ActiveTurn(threadID)` 取观察侧 turnID 发官方 `turn/interrupt`；`handleAbortGeneration` 注册表未命中时经 `abortObservedThread` 按 BackendID 直达（6s 超时），成功发 `turn_completed(aborted)` + `session_state_changed(idle)`，不重复 pending 通知 |
| P0-4 | 目录同源 | `agent/codex-web/catalog_thread_list.go`（新）、`go-bridge/handlers.go`、`go-bridge/session_discovery.go`、`go-bridge/catalog_native_membership.go`、`go-bridge/handlers_codex_catalog.go` | codex-web 实现 `FetchThreadList`/`FetchThreadListHead`（能力断言取代 `agent.Name()=="codex"` 字符串分派）：全局形状无 cwd、目录 shape 带 `{"cwd":[dir]}`、head 上限 25、去重、硬顶 1000；catalog 同源（directory 与 list_sessions 同一 seam）；3s discovery hint 覆盖 codex-web；发现日志同时打印 raw/filter 两个会话数 |
| P0-4 | 观察状态归因（同源扩展） | `go-bridge/catalog_native_membership.go` | `codexVisibleMembershipCounts` 一次拉取返回过滤后 wire 会话 + raw 数，会话快照/变更日志带 rawCount，便于核对「index 438 vs thread/list 430」类差异 |

## 验证证据

### Mac Go 全量（本轮复跑确认，全部命中缓存判定 ok）

`go test ./...` 全绿（19 包 ok，无失败）。

### 新增单测

- `agent/codex-web/todo_test.go`：`TestAgentTodosRoundTripCopy`、`TestAgentTodosAbsentReturnsEmpty`（无缓存=空）、`TestAgentTodoPlanCacheCapReset`、`TestAgentPlanEventCachedViaLiveCodec`（live codec 解码→缓存→FetchTodos）、`TestAgentDeleteSessionDropsPlanMirror`
- `agent/codex-web/abort_test.go`：`TestAgentCancelTurnForThreadObservedTurn`（观察 turn→turn/interrupt 带观察侧 turnID）、`TestAgentCancelTurnForThreadNoActiveTurn`（无活动 turn→报错且零 RPC）
- `agent/codex-web/catalog_thread_list_test.go`：全局/目录/head 请求形状、head 上限 25/透传 3、去重与硬顶
- `go-bridge/abort_observed_test.go`：`TestAbortObservedThreadDirectCancel`、`CancelFailure`、`NoSupport`、`UnknownBackend`
- `go-bridge/handlers_codex_catalog_test.go`：`TestCodexCatalog_V2Declared_RoutesCodexWebToThreadFetch`
- `go-bridge/session_discovery_test.go`：`TestSessionDiscoveryHintArmsForCodexWebBackend`；`catalog_native_membership_test.go` 更新 `discoveryFingerprint` 调用点

### iOS 全量（1343 tests，2026-08-23 20:29–20:43）

`CCCodeTests` 全量结果：**14 failures 全部命中已知基线**（改前 HEAD 已复现）：`SessionsViewModelServerSwitchTests` 4 组（bridge-ready/初始化重试类确定性失败 ×13）+ `testSessionRefreshNotification_isCoalescedForCodex` + `testSessionCreationUpdateStillRendersPendingPermissionStep`（已知偶发，HEAD 单跑通过）。本轮触及的套件全部通过：`RemoteRunningSessionTests`（含新建 todo 轮询/续接/abort 测试）、`SessionsViewModelAutoRefreshTests`（2 个新 sessions_changed 测试）、`ServerViewModelBridgeActivationTests`、`ChatViewModel+Todos` 相关。

### iOS 新增单测

- `RemoteRunningSessionTests.swift`（`TodoRefreshGenerationTests`）：`testAuthoritativeEmptyTodoFetchClearsStaleCacheAndDock`、`testEmptyTodoFetchArmsRefreshWatcherFromColdCache`、`testVagueResumeMessageRefetchesLatestTodosBeforePrompt`、`testAbortGenerationSendsRPCAndResetsState`、`testSessionStateChangedRunningRestartsTodoRefreshChain`、`testActiveProbeReloadsEmptyTodoCacheAndRestoresDockAndResumeChip`
- `SessionsViewModelAutoRefreshTests.swift`：`testSessionsChangedEventDirectlyRefreshesSessionList`、`testSessionsChangedEventDefersWhileSidebarHiddenAndFlushesOnOpen`、`testSessionListRefreshDefersWhileSidebarHiddenThenRunsWhenOpened`

## 真机验证门（未执行，需 owner）

1. 挂 console 复现：Mac 跑 3 步短任务 → iOS console 确认 `fetch_todos` 应答与 8s 轮询命中，dock 按步更新。
2. Mac 新建 session → iOS 列表 ≤60s（通常 ≤3s hint）刷新，无需重启 App。
3. 多步长任务（Mac 端发起）→ iOS dock 逐步推进；Mac 到第 N 步时 iOS 不再停在第 1 步。
4. iOS 点「停止」→ daemon 侧 turn 终态 interrupted（桥日志 `turn_completed(aborted)`），任务实际停下。
5. iOS 发「继续」→ 指令文本基于最新 plan（从未完成步骤续跑，不再「从步骤 1 开始」）。
6. 回归：打开会话/投影同步/消息同步/断连重连不回归。

附：若 console 复现时 `todos_updated` 未稳定驱动 dock，8s 轮询即为本轮的兜底设计（用户场景可接受）；若事件到达但 dock 仍不更新，再做「Mac 把 EventPlan reduce 进投影 + iOS 投影解码映射」的协议级升级（独立提交）。

## 第二轮（2026-08-23 晚间真机复测）——三个断点补修

第一轮真机复测结果：todo dock 已通（P0-1/P0-2 ✅），但 ①Mac 新建 session 列表仍不及时；②Mac 发起的 turn iOS「停止」无效；③桥日志出现 codex-web `sessions_changed` generation 1→108 风暴（全量刷新每 ~3s 一次）。桥日志 + 设备 fg-trace 定位出三个此前未覆盖的断点：

| # | 根因（证据） | 修复（文件） |
|---|---|---|
| B1 | iOS 端 `scheduleSessionListRefresh` 以 `isSidebarVisible` 门控：聊天页期间 108 次 sessions_changed 全部 `hasDeferredSessionListRefresh` 后不再 flush；设备日志 18 分钟 0 次 `list_sessions`（bridge 侧仅 22:25:40 一次）。Drawer 返回列表不必然触发 `setSidebarVisible(true)` → 列表停在使用旧 catalog | `SessionsView.swift`：事件驱动刷新去除 `isSidebarVisible` 门控，只保留 3s 最小间隔节流 + fgTrace 证据行（`[Sessions-EVT]`） |
| A1 | `handleAbortGeneration` 检查 `h.sessions.get()` 只区分「未命中/空」：被动事件泵（main.go）`markRunning` 给观察会话建的 stub（`session==nil`、`backendID=""`）被当作命中→删 stub+关闭 no-op→daemon 的 Mac turn 继续跑（22:33:22/22:39:17 两次 abort 后 text_delta 接着流式；`abortObservedThread` 日志零行——根本没走到） | `handlers.go`：`t.session == nil` 的 stub 与未命中同样路由到 `abortObservedThread`（官方 `turn/interrupt` 直达）；新增回归测试 `TestAbortObservedThreadRegistryStubRoutesToObservedCancel` |
| C1 | 3s head 提示用与权威全量相同的语义指纹（含 `updatedAtMillis`）：流式 turn 每个 delta 都改写 updatedAt → 每次探测「head changed」→ 全量刷新 → fingerprint 又变 → sessions_changed 风暴（generation 每 3s +1，22:29–22:43 达 108） | `catalog_native_membership.go` 新增 `listOrderFingerprint`（顺序+id）；`session_discovery.go` `codexDiscoveryHintFingerprint` 改用它：新增/删除/recency 变化仍触发（id/顺序体现），流式 updatedAt churn 不再触发；回归测试 `TestCodexDiscoveryHintIgnoresUpdatedAtChurn`、`TestListOrderFingerprintIgnoresUpdatedAtChurn` |

验证：Mac `go test ./go-bridge ./agent/codex-web -count=1` 全绿；iOS 专项（`SessionsViewModelAutoRefreshTests` + `RemoteRunningSessionTests`）全绿，其中 defer 语义测试改写为「侧栏隐藏也刷新 + 3s 节流」（`testSessionListNeedsRefreshFiresWhileSidebarHidden`、`testSessionsChangedEventRefreshesWhileSidebarHiddenAndThrottles`）。真机复测见上表对应行。
