# codex-web iOS 控制会话分析与 Session Sync v2 收口

- 日期：2026-08-24
- 状态：已纠正原始分析；已完成已知 Session Sync v2 违规路径的代码收口，待 owner 真机回归
- 涉及仓库：
  - Mac：`cordcode-macbridge-codex-web`
  - iOS：`cordcode-ios-codex-web-backend`
- 证据：Mac `go-bridge.log`、iOS `/tmp/cordcode-fgtrace/fg-trace.log`、双端当前源码

## 0. 结论

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
| H3 | 文本已进入 ChatViewModel，但 MessageWeb/presentation/viewport 没有逐次可见提交 | 中高 | 每个 syncRev 对齐 schedule、snapshot id、WebView ack、visible bottom/scroll state |
| H4 | 6 组 todo 每组重复 2 帧来自双通道发布，并可能触发 duplicate gate | 中；尚无因果证据 | 为两帧记录 source/seq/event id，确认去重发生层级 |
| H5 | 新 session 在 catalog/list/iOS filter 某层被丢弃 | 中；未证实 | 目标 session id/workdir + Mac filter decision + wire payload + iOS final list |

## 6. 后续实施顺序

1. owner 真机复测本轮 writer seal 与 todo poll：iOS 发送长任务时，用户消息只能由 projection 回显；dock 应在下一次 8 秒 fetch 或有效 `todos_updated` 后出现；取消后必须等 projection 才变为 settled。
2. 若文本仍“结束时一次出现”，只在 MessageWeb presentation 链加同一 `syncRev/snapshotID` 的 schedule/apply/ack/viewport 桩，定位 H3；不修改 Kernel writer 结构。
3. 为 todo raw 帧加入 mapper → acceptance → router → VM 的单次关联标识，定位 H2/H4。poll 是保留的 control-plane 路径，不是 timeline fallback。
4. 新建一个带唯一标题的 Mac session，记录 id/workdir，抓 Mac catalog decision、`list_sessions` 响应和 iOS 接收后的列表，完成 H5 闭环。
5. 若未来决定把 plan 纳入 projection，必须先改 Mac canonical protocol pack，再同步 iOS mirror、Swift/web types、reducer/fence/delivery 测试，并在新路径证明后删除 raw/poll。不能把它作为本轮临时补丁单端加入。

## 7. 验证记录

| 验证 | 结果 |
| --- | --- |
| Mac 定向 projection delivery tests | 通过 |
| Mac `go test ./go-bridge -count=1` | 通过（65.267s） |
| iOS 产品 target 真机签名 build | 通过 |
| iOS `build-for-testing`（generic iOS，禁签名） | 通过（`TEST BUILD SUCCEEDED`） |
| iOS 定向单测执行 | 未执行：`CCCodeTests` 现有 target 未配置 development team，测试 host 安装被签名配置阻塞；不得写成 tests passed |
| iPhone 安装 | 已安装 `org.openagi.cordcode`，未自动启动或执行 UI test |

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

本报告的排障边界是：先证明 owner 层，再修 owner 层。后续任何方案若需要恢复 raw/history/optimistic writer 才能“看起来正常”，即视为违反 Session Sync v2，不进入实现。
