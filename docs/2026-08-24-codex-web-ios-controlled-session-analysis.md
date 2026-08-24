# codex-web iOS 控制会话分析与 Session Sync v2 收口

- 日期：2026-08-24
- 状态：**DONE（代码与 owner 真机回归已收口）**；H5 目录列表取证不属于本轮修复，作为独立 backlog 保留
- 涉及仓库：
  - Mac：`cordcode-macbridge-codex-web`
  - iOS：`cordcode-ios-codex-web-backend`
- 证据：Mac `go-bridge.log`、iOS `/tmp/cordcode-fgtrace/fg-trace.log`、双端当前源码

## 0. 结论

> 2026-08-24 晚间收尾：owner 已验证已有 session 与新建 session 的首条发送均能立即显示，并验证 Mac 发起的长任务终态可正常收口到 iOS。相关修复已提交、部署，本文档不再作为开发队列。§6.4/H5 只是尚未补齐的 catalog 取证，不得为它回退 SSV2 单 writer、首发即时占位或终态修复。
>
> 本文 §1.2/§4.1 对“不创建任何 optimistic row”的旧表述已被后续 cold-send fence 设计取代：当前允许 client-authored 首发即时占位，但它不是 server timeline 真相 writer，最终仍由 projection 确认/替换。当前契约与回归证据见根目录 `think.md` 的“codex-web cold send 首发即时显示”章节。

本轮日志不能支持“Mac 没有投递”或“iOS 60 秒没有进入渲染映射”这两个判断。

1. 场景 3（iOS 发任务）中，Mac 侧官方 runtime 观察、Projection Kernel 推进和 K4Patch 投递均正常；iOS 也持续 apply projection。文本不可见的剩余断点已经缩小到 `ChatViewModel.messages` 之后的 MessageWeb 调度、snapshot/apply/ack 或 viewport 层，禁止通过 raw/history writer 补洞。
2. todo dock 的直接问题是：旧完成计划被清空后，8 秒 control-plane 轮询没有重启；同时 raw `todos_updated` 虽进入客户端 mapper，但没有证据证明它最终进入 ChatViewModel。轮询停表已修复；raw routing 仍需分层取证。
3. 场景 1（Mac 新建 session 后 iOS 列表不出现）只证明 iOS 刷新机制在运行，尚未取得 `list_sessions` payload 和目标 session 的 catalog 判定，不能把 workspace filter 写成根因。
4. 代码审计确认过的 Session Sync v2 违规包括：乐观 timeline 写入、revision-only consumer referee、raw permission/question timeline 回退、客户端 abort 收口、发送 RPC 直返消息写入。上述入口现已封死。

## 1. Session Sync v2 架构契约

本专项受 iOS 仓库 `CLAUDE.md` 的“Session Sync v2 架构路线护栏”约束。实现与后续排障必须显式满足以下边界。

| 审计项 | 本专项约束 |
| --- | --- |
| timeline 真相 owner | Mac Projection Kernel 中每个 `(backendId, sessionId)` 的唯一 `SessionProjection` |
| active timeline 唯一 writer | iOS `ProjectionStore` apply 后的 projection → `messages[]` / execution mapping |
| control-plane owner | todo 暂由官方 plan mirror + `fetch_todos` / `todos_updated` 提供；它不拥有 timeline |
| 事务域 | 官方 daemon observation → Kernel live；todo raw/fetch control-plane；MessageWeb presentation 三域分开 |
| 新数据路径 | 本次不新增 timeline 路径；只恢复已有 todo 8 秒 control-plane 轮询的生命周期 |
| 失败呈现 | 发送、取消、todo fetch 失败显式显示；不得自动切 legacy、history、raw 或 stale cache |
| 防双写证据 | Mac delivery seal + iOS raw permission seal、send writer seal、abort authority、静态架构守卫 |

### 1.1 正确拓扑

```text
Codex Desktop 正在使用的官方共享 app-server daemon
        ▲
        │ WebSocket over UDS（两端 FD/peer 指向同一 daemon control socket）
        │ thread/observe、thread/read、turn/start、plan 等官方协议
        ▼
codex-web adapter（共享 daemon 的客户端，不启动 stdio 独占 runtime）
        ▼
Mac Projection Kernel（timeline 唯一真相）
        │ projection snapshot / patch
        ▼
iOS ProjectionStore（active timeline 唯一 writer）
        ▼
ChatViewModel mapping → MessageWeb snapshot/apply/ack → viewport

官方 plan mirror ── fetch_todos / todos_updated ──► iOS todo dock
                         （显式 control-plane 例外，不写 messages[]）
```

旧文档把官方 app-server 写成 `work_dir=/Users/jacklee` 的 stdio 子进程，这是错误拓扑。codex-web 的目标不是另起一个 adapter-owned runtime，而是加入 Codex Desktop 已使用的共享官方 runtime。

### 1.2 active 下全部写入口

修复后的 writer 清单如下：

- projection snapshot/patch：允许，经 `ProjectionStore` revision/fence 规则写入 timeline。
- raw text/reasoning/tool/session-state：禁止写 active timeline。
- raw permission/question：Mac 不再向 SSV2 客户端投递为 timeline 事件；iOS 即使收到旧帧也只做通知或非 timeline 控制动作。
- 本地发送：不 append user row，不创建 assistant placeholder，不创建本地 generation turn。
- 发送 RPC 返回值：只表示提交成功，即使携带 `Message` 也不得写 timeline 或收口。
- abort RPC：只是取消请求；客户端不得提前置 aborted/idle，必须等待 projection。
- history merge、external poll/probe、recovery snapshot：active SSV2 继续 hard gate。
- todo fetch/raw：只更新 todo dock，不写 `messages[]`，属于护栏第 8 条允许的显式 control-plane 例外。

## 2. 测试场景

| # | 场景 | 真机观察 | 当前判断 |
| --- | --- | --- | --- |
| 1 | Mac Codex App 新建 session | iOS 列表没有出现 | 未定因；需 list payload + catalog 判定证据 |
| 2 | Mac 发多 todo 长任务 | iOS 文本与 dock 可同步 | 通过；证明共享 observation/projection 主链可工作 |
| 3 | iOS 发多 todo 长任务 | Mac 正常；iOS 无 dock，文本视觉上只出现第一句，结束时全部出现 | Mac 投递正常；dock 停表已定位，文本剩余断点在 iOS presentation/viewport |

场景 3 的 iOS 发送时间为 10:09:51.4（UTC 日志 02:09:51.4Z）。

## 3. 证据纠正

### 3.1 Mac 投影链没有缺帧证据

场景 3 中 Mac 侧被动订阅解码到 721 个 `text_delta`、6 个 `todos_updated` 更新组，Projection Kernel 在约 60 秒内由 rev 311 推进到 589，316 帧 K4Patch 均记录为 delivered。原先的“事件缺失”来自 grep 截断，不是系统事实。

这只能证明 Mac observation、Kernel 与 bridge delivery 正常，不能直接证明 iOS viewport 已显示。

### 3.2 `[Render] enter = 0` 已证伪

对 `/tmp/cordcode-fgtrace/fg-trace.log` 在 `02:10:13Z–02:11:12Z` 精确重算：

```text
render_enter=74
msgCount=48  1
msgCount=49 14
msgCount=50 11
msgCount=51 13
msgCount=52 17
msgCount=53 18
```

因此不能再提出“消息集合不变导致 60 秒不进入 Render”这一 H3。ChatViewModel 的 projection mapping 在执行期持续进入且消息数逐步增长。真正需要检查的是：

```text
$messages 发布
  → scheduleRender/coalescing
  → MessageWeb snapshot 序列化
  → WebView apply/ack
  → 当前 viewport 可见性与自动滚动策略
```

该层问题只能在 presentation/viewport 修复，不得恢复 optimistic、raw 或 history timeline writer。

### 3.3 todo 事件与停表

执行期 `todos_updated` 的准确记录是 6 组、每组重复 2 帧：

- 02:09:57Z
- 02:10:09Z
- 02:10:24Z
- 02:10:37Z
- 02:10:51Z
- 02:11:07Z

另有一条任务开始前的单帧 02:09:00Z。旧文档中的 02:10:47Z 不存在。

每帧都有 `[WS-RX]` 和 `🔍EVT-RECV`，但全天没有 `[TODO] sse-case`。需要注意：`🔍EVT-RECV` 位于 `CCCodeBridgeBackendClient.mapBridgeEvent` 入口，它只证明 mapper 被调用，不能证明 mapping 成功、acceptance 通过、路由到当前 ChatViewModel 或 `.todosUpdated` switch 被执行。

同时，02:09:51.4Z 发送前执行 `discardStaleCompletedTodoPlanForNewGeneration`，旧完成计划被清空，但旧实现没有重挂 8 秒轮询。于是这一轮任务期间 dock 同时失去 poll 数据源，而 raw 事件又没有到达 VM 分支。停表是已证实缺陷；raw routing 的具体断点仍未证实。

Mac 侧首次带计划的 fetch 是 10:07:49，`count=5 active=5 completed=0`；10:08:13 是后续状态，`count=5 active=4 completed=1`。旧文档已纠正数据归属。

### 3.4 session 列表仍未定因

iOS 在测试窗口内每分钟自驱刷新，并在 `sessions_changed` 后执行防抖刷新。这排除了“刷新 task 完全没有运行”，但不能证明服务端 payload 包含目标 session，也不能证明 iOS 接收后没有再次过滤。

Mac 日志中的 `raw=433 / kept=189 / codex_roots=12` 只能说明 catalog filter 存在，不能说明目标 session 就是被它丢弃。没有目标 session id、workdir、filter decision 和最终 `list_sessions` payload 前，H5 只能保持未证实。

## 4. Session Sync v2 违规审计与代码修复

### 4.1 iOS active timeline writer 收口

涉及文件：

- `OpenCodeiOS/ViewModels/ChatViewModel.swift`
- `OpenCodeiOS/ViewModels/ChatViewModel+Generation.swift`
- `OpenCodeiOS/ViewModels/ChatViewModel+CodexStreaming.swift`
- `OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift`
- `OpenCodeiOS/ViewModels/ChatViewModel+Todos.swift`

已修复：

1. 删除 `localSendProjectionBaselineRev` / `localSendOptimisticPaint` 及 hold/restore 逻辑。该逻辑用 revision 猜测“何时保留本地消息”，属于护栏禁止的 consumer referee。
2. active SSV2 发送不再写本地 user message、assistant placeholder、generation turn 或本地 running 状态。
3. 发送 RPC 成功仅表示命令被接受；直返 assistant message 在 active SSV2 下被硬拒绝，不得写 timeline 或触发本地 finalize。
4. raw permission/question 旧帧不再落入 legacy `messages[]` 写分支；只保留通知与明确的非 timeline 控制动作。
5. abort 不再先调用 `settleLocalAbortState`；RPC 成功或失败都不能冒充权威完成态，等待 projection 发布 aborted/idle。
6. SSV2 resume 不再使用固定 2 秒 timer 猜 ready/settled。
7. todo fetch 失败与空结果分离；“继续”不再用 stale todo cache 制造成功。清空旧完成计划后立即恢复已有 8 秒 control-plane poll。

### 4.2 Mac raw delivery seal

涉及文件：

- `go-bridge/projection_delivery.go`
- `go-bridge/projection_delivery_test.go`

`permission_request`、`permission_resolved`、`permission_asked` 已加入 SSV2 projection-owned timeline deny-list。legacy 客户端仍保持既有投递，SSV2 客户端不会再收到它们作为 raw timeline 帧。

该改动没有把 todo 误封：todo 仍是当前明确登记的 control-plane 例外。

Mac 的 observed-thread 取消路径也遵守同一权威边界：官方 `turn/interrupt`
返回 ACK 后不再合成 `turn_completed{reason: aborted}` 或 `session_state_changed: idle`。
ACK 只证明取消请求被接受，不是完成证据；Projection Kernel 保持原 rev、active turn
和 running phase，直到共享 daemon 的官方 `turn/completed` 经观察流到达。该路径是控制
请求，不是 control-plane 终态例外。

### 4.3 防回归守卫

新增或调整的定向守卫覆盖：

- Mac：SSV2 raw permission 三类事件 0 投递、legacy 仍投递。
- Mac：observed-thread interrupt ACK 不改变 projection rev/active turn/running phase。
- iOS：raw permission 不写 timeline。
- iOS：SSV2 send 不创建 optimistic row / placeholder / local turn。
- iOS：projection 到达时直接替换任何未确认的 client-authored row，不做 referee。
- iOS：abort 请求后在 projection 确认前仍保持 running。
- iOS 静态架构测试：被删除的 optimistic fence 标识不得回归，发送与 raw permission writer seal 必须存在。

## 5. 当前根因假设

| # | 假设 | 状态/置信度 | 下一证据 |
| --- | --- | --- | --- |
| H1 | dock 无数据源 = cleared 分支停 poll + raw todo 未进入 VM | 停 poll：已证实并修复；raw routing：中高但未定点 | mapper output、acceptance、router、VM switch 四段同一 event id 日志 |
| H2 | `EVT-RECV → VM` 中间存在 mapping/routing/acceptance 丢弃 | 中高 | 记录 event 名、session id、current session、mapping result、acceptance result |
| H3 | 文本已进入 ChatViewModel，但 MessageWeb/presentation/viewport 没有逐次可见提交 | ~~中高~~ **已关闭（§8.2）**：iOS 全程 0 条 text 类路由日志，正文唯一通道是投影链；双路发生在 kernel 摄入（relay+passive），不在渲染层 | — |
| H4 | 6 组 todo 每组重复 2 帧来自双通道发布，并可能触发 duplicate gate | ~~中~~ **已定案为文本双路变体（§8.2）**：todo 双帧在 gate 按 eventID 正常 duplicate；正文双收来自 relay+passive 两个 `deltaBatcher.Send` 合并 | — |
| H5 | 新 session 在 catalog/list/iOS filter 某层被丢弃 | 中；未证实 | 目标 session id/workdir + Mac filter decision + wire payload + iOS final list |

## 6. 后续实施顺序

1. owner 真机复测本轮 writer seal 与 todo poll：iOS 发送长任务时，用户消息只能由 projection 回显；dock 应在下一次 8 秒 fetch 或有效 `todos_updated` 后出现；取消后必须等 projection 才变为 settled。
2. ~~若文本仍“结束时一次出现”，只在 MessageWeb presentation 链加同一 `syncRev/snapshotID` 的 schedule/apply/ack/viewport 桩，定位 H3~~ —— **已实施（2026-08-24）**：`[PIPE]` 桩（sink/schedule/snapshot/enqueue/ack/scroll 六步，贯通 `syncRev + snapshot.revision`），见 §8 新命令；仍不修改 Kernel writer 结构。
3. ~~为 todo raw 帧加入 mapper → acceptance → router → VM 的单次关联标识，定位 H2/H4~~ —— **已实施（2026-08-24）**：`EVT-RECV` 与 `[ROUTE]`（accept/duplicate/gap/non-current）与 `[TODO] sse-case`/`handleTodoUpdate` 全部携带同一 `eventID`（bridge `epoch:seq`）；gate 的 `.accept/.duplicate` 从此可审计。poll 仍是保留的 control-plane 路径，不是 timeline fallback。
4. 新建一个带唯一标题的 Mac session，记录 id/workdir，抓 Mac catalog decision、`list_sessions` 响应和 iOS 接收后的列表，完成 H5 闭环。
5. 若未来决定把 plan 纳入 projection，必须先改 Mac canonical protocol pack，再同步 iOS mirror、Swift/web types、reducer/fence/delivery 测试，并在新路径证明后删除 raw/poll。不能把它作为本轮临时补丁单端加入。

## 7. 验证记录

| 验证 | 结果 |
| --- | --- |
| Mac 定向 projection delivery tests | 通过 |
| Mac `go test ./go-bridge -count=1` | 通过（62.4s，含 §8.2 新增回归） |
| iOS 产品 target 真机签名 build | 通过 |
| iOS `build-for-testing`（generic iOS，禁签名） | 通过（`TEST BUILD SUCCEEDED`） |
| iOS 定向单测执行 | 未执行：`CCCodeTests` 现有 target 未配置 development team，测试 host 安装被签名配置阻塞；不得写成 tests passed |
| iPhone 安装 | 已安装 `org.openagi.cordcode`，未自动启动或执行 UI test |
| Mac runtime 重建+重启（含 §8.2 两修复） | 通过：go-bridge 全量测试绿后重建，app 换入新 binary（codesign 校验通过），被动泵 1:1 健康（62 条 delta = 62 条 passive event） |
| Mac `go test ./go-bridge -count=1`（含 §8.2 第三根因回归） | 通过（66.2s） |
| Mac `go test ./agent/... ./core/... ./transcriptindex/... -count=1` | 通过（agent/codex-web 0.75s 等，全模块绿） |
| Mac runtime 重建+重启（含第三根因修复，13:16） | 通过：Release 构建成功（runtime 内含新代码字符串校验），app 重启后 runtime PID 32061 新进程，iOS 已连上新桥（list_models/workspace_diff/fetch_todos） |

（2026-08-24 晚间专报与此处各自独立；本条审计记录不覆盖后续 `[PIPE]`/`[ROUTE]` 桩的构建结果，见对应提交。）

## 8. 取证命令

```sh
# 精确复算执行期 Render 次数；不要用 head/tail 截断后下结论
awk '$0 >= "2026-08-24T02:10:13" && $0 <= "2026-08-24T02:11:12.999" && /\[Render\] enter/ {
  n++
  if (match($0, /msgCount=[0-9]+/)) c[substr($0, RSTART, RLENGTH)]++
} END {
  print "render_enter=" n
  for (k in c) print k, c[k]
}' /tmp/cordcode-fgtrace/fg-trace.log

# todo raw 接收与 VM 分支必须分别计数
rg 'event=todos_updated|EVT-RECV event=todos_updated|\[TODO\] sse-case' \
  /tmp/cordcode-fgtrace/fg-trace.log

# Mac 投影与 todo control-plane
rg 'K4Patch.*delivered|fetch_todos result|passive event' \
  "$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log"
```

### 8.1 H3/H2/H4 关联取证（2026-08-24 桩，iOS 仓 `scripts/forensics-todo-render-pipe.sh`）

```sh
# 一键对齐：todo 同 eventID 四段（EVT-RECV→ROUTE→sse-case→handleTodoUpdate）+
# 渲染链六步（sink→schedule→snapshot→enqueue→ack→scroll）
scripts/forensics-todo-render-pipe.sh /tmp/cordcode-fgtrace/fg-trace.log
```

关联键与语义（必须逐段取读，禁止一次 grep 后下结论）：

- **eventID**（bridge 帧 `epoch:seq`）贯穿四段：`EVT-RECV`（mapper 入口）→ `[ROUTE] accept|duplicate|gap|non-current-enqueue`（acceptance gate 判决与 current-session 路由）→ `[TODO] sse-case v2|legacy`（VM switch 执行）→ `[TODO] handleTodoUpdate ... eventID=`（应用为 dock）。**同 eventID 在 gate 缺席 = 门内丢失；在 gate 出现但 VM 缺席 = 路由/分支丢弃；`duplicate` = 双通道重复由 gate 判掉（H4 证据方向）**。
- **syncRev**：`projectionStore.appliedRev`（VM 已应用的投影 rev），出现在 `[PIPE] sink/snapshot`。
- **rev**：`WebTimelineSnapshot.revision`，每 `makeSnapshot` +1，贯穿 `[PIPE] snapshot → enqueue → ack`。`snapshot rev=X` 后无 `enqueue rev=X` = MessageWeb 未排队；`enqueue rev=X state=sent seq=S` 后无 `ack ... rev=X` = WebView 未回 snapshotApplied（H3 证据方向）。
- 断链判定顺序：`sink` 有而无 `schedule` ＝ 调度未发生；`schedule` 有而无 `snapshot` ＝ coalesce 吞（streaming 节流）；`snapshot` 有而无 `enqueue` ＝ not-ready/awaiting 挂起（行尾 `state=` 标识）；`enqueue sent` 无 `ack` ＝ WebView 卡住或 ack 超时；`ack` 有而无对应起点的 `scroll` ＝ 滚动策略问题（Web owns scroll 契约内）。

### 8.2 根因定案与修复（2026-08-24 真机 12:01-12:10 波次 + 自采 daemon 流证明）

**重复文本（H3 定案 = H4 变异形态：单一摄入所有者被双路违反）。**

- iOS 全程 0 条 `DELTA-APPLY`、0 条 text 类 `EVT-RECV`：正文唯一通道是 K4Patch 投影链，渲染层无重复写入方（排除 H3）。
- 自采 daemon `item/agentMessage/delta` 流（`codex app-server proxy` + WS-over-UDS 直连 control socket，60/59/62 条分三轮）：官方 `delta` 是严格追加式；对 captured 流做「最大后缀-前缀重叠去重」差分 = 0 重叠。
- 因此 kernel 文本重复只能来自摄入侧重复：`relayEvents`（handlers_relay.go，session route 中继）对每个事件 `deltaBatcher.Send`，而被动泵门（main.go，旧判据 `HasSessionSubscriber || HasSessionInterest`）对同一官方增量 **再次** `Send`。两份拷贝落在同一个 batcher accumulator 里合并成 `D+D` 的 append_text；两次 `PublishLogical` 分配不同 perSessionSeq，kernel 的 seq 去重无法识别。真机观测：每条 delta 后文本尾部出现 1-8 字符重复（正是单条官方 delta 的体量），且随流式逐段出现。
- 修复合入（代码提交）：被动泵门改为「该会话 **无 agent relay 在跑** 且 **有观察兴趣**」才补投（`agentRelayRunningFor`）；有 relay 的中继会话由 relayEvents 单点摄入。**判据必须是 relay 在跑而不是「有订阅者」**：codex 文件 relay 只覆盖 "codex" backend，codex-web 外部 turn（Mac 发起、bridge 无 AgentSession）有订阅者但没有 relay——被动泵必须继续兜底，否则外部观察 turn 会饿死 kernel。
- 回归：`TestPassiveFeedAllowedSingleIngestOwner`（门真值表：relayed-only / relayed-and-observed / observed-only / untracked 四象限）。

**首次「停止」1 秒后回到执行中（第二次停止才生效）。**

- 12:02:38.593 `abort_generation req_137`：注册表路径命中（iOS 发起的 turn 持有 AgentSession），`CancelTurn` 失败（错误仅 DEBUG 不可见），随后按旧逻辑**合成** `turn_completed{aborted}` + `session_state_changed:idle`（flush syncRev=816，offline=true）——iOS 显示停滞 1 秒；但共享 daemon 上的官方 turn 并未停（官方流继续 → flush 817/818 tool 事件重挂 running）。
- 12:02:49.930 `req_141`：注册表会话已被第一次删除 → 走 `abortObservedThread` 直达 `turn/interrupt`（955ms 内 ACK）→ 官方 `turn/completed`（interrupted）经观察流收口 → 两侧一致。
- 修复合入：注册表路径对共享 daemon 后端（codex-web，及 app_server 模式的 codex = `sharedDaemonCodexBackend`）不再合成终态/不再 `recordPendingNotification`——与 9cf9287 (b) 规则对齐（ACK 只代表请求被接受，投影保持 running 等官方收口）；`CancelTurn` 失败升级为可见 Info。私有进程后端（Close 即真实终止）保持合成行为不变。
- 回归：`TestAbortRegistrySharedDaemonCancelFailureKeepsProjectionRunning`、`TestAbortRegistrySharedDaemonCancelSuccessKeepsProjectionRunning`（均断言 SyncRev/Phase/ActiveTurnID 不变）、`TestAbortRegistryPrivateBackendSyntheticIdlePreserved`（非共享后端合成 idle 仍推进）。

**停止生效但两端 UI 卡住（12:55:49-12:58 波次：iOS 永久「执行中」，Mac「待执行」stub）。**

- 12:52:48 iOS `send_message`（长任务）→ 注册表路径 AgentSession + agent relay 启动；12:53:51 turn A 自然完成（relay 转发 seq=601），Mac Desktop 于 12:55:31 在同 thread 发起 turn B（`turn_started` 12:55:31.743）——agentSession 的 `activeTurnID` 仍停在 turn A（`observeEvent` 无调用者，只有本端 TurnStart 返回值被记录），于是 12:55:49.420 第一次 `abort_generation req_11` 用过期 turnID → 官方拒绝 `-32600 expected active turn id 01a0321d but found 01a0321f`。**该次请求仍执行了 `deleteSession` + `sess.Close()`（removeListener）**。
- 12:55:55.907 第二次 `req_13` 注册表会话已删 → 走 `abortObservedThread` 直达成功（用 liveCodec 观测到的 01a0321f）；官方 `turn/completed`（interrupted）12:55:55.929 广播——**但只到达被动泵**：relay 的监听者已被 removeListener，relay goroutine 挂在永远收不到事件也永不关闭的通道上（僵尸）；`agentRelayRunning` 残留 true 让被动泵门（单一摄入所有者）永久挡掉这一帧 → Projection Kernel 停在 syncRev=443（tool_started），Execution 永久 running → iOS 按钮「执行中」；被动泵 `markIdle`（在门之前无条件执行）给已删会话建了 `sessionStateIdle` stub → Mac 列表「待执行」。
- 修复合入（三点联动）：
  1. `agentSession` 事件流改为「监听者→转发」链（`startEventForward`）：转发前调用 `observeEvent`，`activeTurnID` 随官方 turn/started 与 turn/completed 更新（外部 turn 覆盖、完成清空）；`Close()` 关闭对外 `events` 通道——relayEvents 的 `!ok` 分支据此退出 relay 并清理 `agentRelayRunning`，官方后续帧改由被动泵摄入（不再有僵尸挡门）。`currentTurnForControl` 改为中央泵观测（liveCodec）优先、本端返回值兜底。
  2. `handleAbortGeneration` 注册表路径对共享 daemon 后端**不再 deleteSession / 不再 Close**——relay 保留直到官方 turn/completed 经它到达 Kernel（取消是否停住只能由官方帧证明）；首次失败也不破坏摄入链路。
  3. `relayEvents` `!ok`（通道关闭）分支对共享 daemon 后端不再合成 `turn_completed{events_channel_closed}`——那是本端判断，官方 turn 可能仍在跑，合成必被后续官方帧打回（9cf9287 (b) 同规则）；私有进程后端（Close 即真实终止）保持合成。
- 回归：`TestEventForwardMaintainsActiveTurn`、`TestEventForwardObservedTurnWins`、`TestSessionCloseClosesEvents`（agent pkg）；`TestAbortRegistrySharedDaemonCancelFailureKeepsProjectionRunning` / `...Success...` 断言翻转为「会话保留、未 Close、不合成」；`TestAbortRegistryPrivateBackendSyntheticIdlePreserved` 补「会话删除 + Close」断言；`TestRelayEventsSharedDaemonChannelClosedDoesNotSynthesize`。

## 9. 实施边界（2026-08-24 桩整改后补注）

第 6.2/6.3 的桩只做层间取证，任何结论都必须按护栏 9 落到 owner 层修复；桩在 H3/H2/H4 结论得出后可按边界裁剪，但 **gate 的 `[ROUTE]` 判决行建议保留**——它是当前唯一能审计「SFV2 下 raw control-plane 帧为何消失」的持久证据（此前 `.accept/.duplicate` 完全无声）。

H3/H4 已随 §8.2 根因定案关闭（不是 presentation 层、不是 todo 双通道，是 kernel 摄入双路）；H1/H2 的 todo 链四段关联桩保留，供 owner 真机复核；H5 待 §6.4 唯一标题会话流程。

本报告的排障边界是：先证明 owner 层，再修 owner 层。后续任何方案若需要恢复 raw/history/optimistic writer 才能“看起来正常”，即视为违反 Session Sync v2，不进入实现。
