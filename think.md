# Claude Code 冷启动既有 session 首轮流式从头重播：跨仓排查结论

## 2026-08-04 追加复盘：Grok 外部任务 iOS 输入框卡"完成态"

### 现象
Mac 端发起的 Grok Build 任务正在执行时,iOS 端同步该 session 的过程中输入框一直停在"完成态",没有进入"执行中"。Claude/Codex/OpenCode 无此问题。

### 根因(两层,均在 MacBridge)
1. **codec 丢弃上游 durable `turn_completed`**:`convertSessionUpdate`(`agent/grokbuild/acp_codec.go`)的 default 分支把 `turn_completed` sessionUpdate 当未知类型丢弃。真实 `~/.grok/sessions/*/updates.jsonl` 证实上游在终态发 durable `turn_completed`(440 次,带 `prompt_id`+`stop_reason`,method `_x.ai/session/update`,无 isReplay → 进 leader live rail),但 codec 不映射它。
2. **relay loop 不合成 turn-start 信号**:`grokLeaderSessionRelayLoop`(`go-bridge/handlers_relay.go`)只转发内容事件,不合成 `turn_started`/`session_state_changed(running)`。上游 grok-build 不发任何 turn-start sessionUpdate(真实数据 `response_started`=0)。

iOS 进入"执行中"(`isGenerating`)唯一可靠触发是收到 `turn_started` 或 `session_state_changed(running)`。两个都收不到 → 输入框停完成态。

### 诊断方法(autonomous,不依赖 owner)
- 用本机 `~/.grok/sessions/*/updates.jsonl` 作为真实协议样本(上游持久化的通知 = live rail 同源),grep `sessionUpdate` tag 分布。替代了 audit 报告要求的"真实 leader wire 捕获"。
- 对照 codex(`handlers_relay.go:347-355`)和 claude(`:586-597`)的 file relay:它们都会在检测到活跃 turn 时合成 `turn_started`+`session_state_changed(running)` —— grok 的 leader relay 缺这步。
- audit-plan 评审(`docs/2026-08-04-grok-external-turn-completing-state-fix-audit.md`)纠正了 v1 方案的事实错误("upstream 永不发 turn_completed"是错的)。

### 修复(三层,MacBridge 单侧)
- **改动 A(codec,主收口)**:`convertSessionUpdate` 加 `case "turn_completed"`,映射成 `EventResult{Done:true, TurnID:prompt_id}`;`error` stop_reason 转 `EventError`。`sessionUpdatePayload` 加 `PromptID`/`StopReason` 字段(兼容 `prompt_id`/`promptId` 两种 key)。`mapAgentEvent` 已把它转成 wire `turn_completed`,relay loop markIdle 自然生效。
- **改动 B(relay,开始信号)**:`grokLeaderSessionRelayLoop` 在首个内容事件(`text_delta`/`reasoning_delta`/`tool_started`/`tool_finished`)前合成 `turn_started`(turnId 空)+ `session_state_changed(running)`,置 `turnArmed`。turnId 解耦(开始信号用空 ID,结束信号用 prompt_id),避免 ID 跳变。
- **改动 C(relay,兜底)**:`defer` 里若 `turnArmed` 仍为 true(leader 异常断开,未收 turn_completed),补发 `session_state_changed(idle)` + markIdle。

### 关键决策记录
- **不动 `handlers.go:1824-1839`(Bug 4 补丁)**:它针对 iOS 本地发起路径(那条路径 `session.go:380` 自己 emit turn_started),与 leader relay loop 是独立路径。动它会重新引入 2026-07-12"卡执行中"回归。
- **不动 iOS** `sessionSyncV2ProjectionBackend`(grok 被排除):grok 尚未迁移到 projection,是已知架构边界。iOS 对 wire 事件是 backend-agnostic 的,Mac 发对事件就能进入执行中。
- **不动 capability**:保持 `requiresPollingForExternalTurns=true` 的 probe 并行兜底。

### 验证
- 定向测试:`go test ./agent/grokbuild/... -count=1`(15 通过,含 5 个新 turn_completed 变体)+ `go test ./go-bridge/... -count=1`(含 6 个新 grok leader relay 测试:合成/幂等/error/plan 不触发/defer idle/subscribe error)。
- Release 构建 + 覆盖安装 `/Applications`(runtime commit `4218327f883a`,built `2026-08-04T13:43:50Z`),8777 监听者核对为正式版。
- **待 owner 真机验收**:Mac 发起 Grok 任务 → iOS 输入框变"执行中";任务完成 → 恢复"完成态"(不卡执行中)。这是诚实边界——单测和部署不等于端到端成功。

### 后续原则
- **真实数据样本胜过静态推测**:audit 用上游源码静态分析发现"upstream 发 turn_completed",但本机 `updates.jsonl` 直接证实了 440 次 + 字段形状,是最短路径。排查协议类 bug 先 grep 本机持久化样本。
- **turn 生命周期合成属 relay 层,不属 codec**:codec 是无状态 ACP→core.Event 映射;turn 开始/结束的 wire 语义合成发生在 relay loop(和 codex/claude 一致)。但 durable 终态信号的**映射**(把上游 turn_completed 转成 core.Event)属 codec——因为它是协议变体到事件的 1:1 转换。
- **leader channel close ≠ turn 结束**:close 只表示 leader 断开。turn 结束的权威信号是上游 `turn_completed`。收口逻辑必须区分两者。



### 最终状态（后续 agent 先看此表）

| 方案 | remote-web | iOS | MacBridge / Relay | 结论 |
|---|---|---|---|---|
| A. history ETag / 条件请求 | 已接入 | 早已接入 | MacBridge 早已有 `ifNoneMatchRevision` | 解决重复读取，不解决首次冷加载 |
| B. gzip 后再加密 | 已接入 | 已接入 | MacBridge 按客户端能力对 Relay 下行压缩 | 解决首次大 history 的传输体积 |
| D. 大响应分片 + 可抢占优先级队列 | **未实施** | **未实施** | 单 `readLoop` + `writeMu` 架构仍在 | 目前只有客户端编排缓解，不能写成 D 已完成 |

### A：ETag 只能复用“与 revision 配对的真实 history”

MacBridge 的 `get_session_messages` 会在完整响应中返回 `revision`；客户端下次发送
`ifNoneMatchRevision`，命中后服务端只返回紧凑的 `unchanged: true`。iOS 原本已使用该契约，
remote-web 此前漏接，导致每次切回看过的 session 仍重复传输数 MB history。

remote-web 的最终实现同时保留 per-session 内存消息 bucket 和对应 revision，并以
`sessionId + directory` 隔离 revision。收到 `unchanged: true` 时只允许恢复与其配对的真实内存
bucket；若本地 bucket 不存在则直接报错，不能用空数组、旧快照或 fallback 冒充成功。backend
切换会清空消息与 revision，完整响应没有 revision 时也会删除旧 revision。

这项优化只覆盖第二次及后续读取。第一次打开 session 没有 revision，仍必须获得完整 history；
因此不能用 A 的单测命中或切回秒开，声称首次 Relay 冷加载已优化。

### B：压缩必须发生在 padding / AEAD 之前，并由客户端显式协商

正式能力名是 `relay_gzip_v1`，只影响 MacBridge → Relay client 的在线帧：

```text
Bridge JSON -> gzip -> padding -> ChaCha20-Poly1305
ChaCha20-Poly1305 -> remove padding -> gzip decode -> Bridge JSON
```

MacBridge 只在连接是 Relay、认证后的 Bridge hello 声明能力且 hello 被接受时启用；当前 sender
只考虑至少 32 KiB 的 payload，并且只有 gzip 后确实更小时才发送 `contentEncoding: "gzip"`。
该字段属于 envelope AAD，攻击者或中间层增删、替换字段都会使认证失败。Web 只有在浏览器提供
`DecompressionStream` 时才声明能力；iOS 只在 Relay transport 声明，并在解密后使用有上限的流式
gzip decoder。旧 Web/iOS、不支持能力的客户端、Direct WebSocket、iOS → Mac 上行和 mailbox
继续使用原格式。

WebSocket `permessage-deflate` 作用在已经加密的高熵 envelope 上，几乎不能压缩，不能替代 B。
解压失败、未知编码、超出解压上限都必须暴露真实 transport 错误，禁止把压缩帧当普通 JSON
重试或加入静默 fallback。

### D：当前只有“优先发送小 RPC”的缓解，核心架构没有改

MacBridge Relay 路径仍是单 readLoop 同步处理 RPC，所有出站写共享 `writeMu`。一个大 history
响应写 socket 时，后到的模型、权限等小 RPC 仍可能在接收缓冲区等待。remote-web 当前在切换
session 前预取模型和权限，并用 promise dedup 让 Composer 复用结果；soft reconnect 期间还会等待
新 transport ready 后才允许 session history 请求。这能避免常见的“小 RPC 排在巨帧后面超时”，
但它不是分片，也不是可抢占队列。iOS 本轮接入 A/B，没有新增 D 协议或队列。

未来真正实施 D 时，行为拥有者主要在 MacBridge / Relay transport，而不是在 Web 或 iOS UI 层。
至少要同时定义：分片 envelope 与 AAD、每片和整消息大小上限、counter/顺序与重组、取消和超时、
有界 backpressure、控制帧优先级、公平性，以及旧客户端协商。Relay 服务器现有 per-device queue
也不等于“大 RPC 已分片且可被小 RPC 抢占”。在这些完成并有跨端测试前，任何 agent 都不得把
prefetch、gzip 或现有 queue 记为“D 已完成”。

### 诊断与验收经验

- 先看 MacBridge `session loading metrics`：`request_total_ms` 接近 `socket_send_ms` 表示瓶颈在
  Relay 写入；history parse/encode 很短时不要先改 handler。
- A 的验收要分别测首次打开与切回：首次仍传全量，切回应命中 `unchanged` 且保留真实消息。
- B 的验收要确认 hello/ack 能力、Mac 日志里的压缩前后字节数，以及 envelope 在解密后才能解压。
- Web owner 实测 B 后长 session 明显加快；iOS 已完成定向单测、真机构建安装，但 Relay 长 session
  的最终加载速度仍需真实 Relay 路径人工验收，不能用 LAN 或单元测试替代。
- 即使 A/B 已显著缩短时间，单 readLoop + `writeMu` 的 head-of-line blocking 仍是已知架构债；
  若以后仍出现巨帧阻塞小 RPC，再评估 D，不要回退整页 history 或降低正常大帧写超时。

---

日期：2026-07-04
结论：本次 owner 真机复现的主因不在 MacBridge 重复生成，也不在 Claude CLI stdout 中断，而在
iOS 本地 Claude live stream 期间仍执行普通历史同步并覆盖 timeline。MacBridge 日志用于排除
重复执行，并暴露 iOS 高频 `get_session_messages` 是关键证据。

## 现象

iOS App 冷启动后，Claude Code 模式打开一个已存在 session，发送“讲个狐狸笑话”。发送后出现
runtime status strip“正在思考中”，开始流式输出。回复较长时，输出一段后页面闪一下，status strip
重新出现，回答不是从上次半截继续，而是从头重新流式输出。重复 3 到 4 次后才完整收口。随后
输入框还会短暂再次进入执行中状态，几十秒后恢复。留在同一个 session 再发第二问时正常。

## MacBridge 侧排查结论

日志窗口内没有看到重复 `send_message`，也没有 Claude CLI 断连重启导致同一 prompt 被重新执行。
相反，MacBridge 持续收到 iOS 发来的 `get_session_messages`，并返回同一 session 的 persisted
history；同时夹杂 `fetch_todos`。这说明服务端只是按请求返回 transcript 历史，视觉上的“从头重播”
来自客户端把历史片段重新应用到当前 live stream。

排查期间曾修过一个真实但非主因的 MacBridge 风险：Claude 既有 session 的 transcript file relay
和真实 AgentSession stdout relay 共用 `relayRunning` 布尔位。若 file relay 抢先占位，
`send_message` 可能无法启动真实 stdout relay。修复后改为记录 relay kind（agent / claude_file），
并允许真实 agent relay 接管 file relay；该修复有回归测试保护，但 owner 复测确认问题依旧，
因此它不是这次“从头重播”的主因。

## 最终根因

iOS 本地发送 Claude turn 时，本地 user/assistant 使用本地 UUID；MacBridge 返回的 Claude
transcript 历史使用服务端 id。生成中如果普通 `loadMessages` 把服务端历史套回 UI，就会把当前
live stream 中的 assistant 替换/合并成服务端较旧或不同 id 的片段。长回复期间这些历史同步多次发生，
所以用户看到 status strip 闪烁、回答半截消失并从头输出。

最初只挡住 iOS `startRunningSessionPolling` 中的一处 `loadMessages`，但冷启动既有 session 时仍有
resident probe、后台刷新、session 切换后续刷新等路径会直接调用 `loadMessages`。因此正确边界不是
“某个轮询入口跳过历史”，而是 iOS 历史同步入口本身必须识别 Claude 本地 live turn ownership。

## 最终修复方案（iOS 仓）

1. `ChatViewModel+MessageSync.loadMessages` 增加入口级保护：Claude Code 本地 turn 进行中时，
   普通历史同步直接返回，不 fetch、不 apply、不写 cache。

2. `recoverAfterSendCompletion` 显式传 `allowDuringClaudeLocalSend: true`，允许 turn 完成后做一次
   权威历史同步和快照写入。也就是生成中禁止历史覆盖，完成后仍以服务端历史对账。

3. `startRunningSessionPolling` 在 Claude 本地 turn 看到远端 idle 时进入
   `recoverAfterSendCompletion`，而不是直接清理执行态。

4. iOS 增加回归：
   `RemoteRunningSessionTests.testClaudeCodeLocalSendLoadMessagesDoesNotApplyHistoryMidStream`
   和 `testClaudeCodeLocalSendRunningPollingDoesNotFetchHistoryMidStream`。

## 验证

- iOS 定向测试 3 条通过：
  `testClaudeCodeLocalSendLoadMessagesDoesNotApplyHistoryMidStream`、
  `testClaudeCodeLocalSendRunningPollingDoesNotFetchHistoryMidStream`、
  `testClaudeCodeTurnCompletion_transitionsToIdle`。
- iOS Debug build 已安装到连接的 iPhone 16 Pro。
- owner 真机复测确认：同一路径冷启动既有 Claude session 后，首轮长回复不再半截闪烁和从头重播。

## 后续原则

- MacBridge 日志出现高频 `get_session_messages` 但没有重复 `send_message` 时，优先怀疑 iOS
  timeline 同步覆盖，而不是 Claude CLI 重跑。
- Claude 本地 live turn 期间，普通历史同步不能作为生成中刷新源，只能在完成后做权威对账。
- MacBridge 的 file relay / agent relay 状态拆分保留为正确的风险修复，但不要把它当作本次
  现象的根因。

---

# OpenCode session 列表加载方案（实际实现）

本文记录 CordCode iOS OpenCode 模式 session 列表加载的真实修复路径。
设计文档 `docs/2026-07-02-opencode-project-first-session-list-plan.md` 的部分判断
（array-only/no-cursor、保守 limit=5）在真机验证后被推翻；本文以最终真机验证通过
的实现为准。

## 问题

iOS OpenCode 模式 session 列表存在三个叠加缺陷：

1. 每个项目只显示 1~3 条 session，远少于 OpenCode Desktop 的真实数量。
2. 没有「加载更多」入口，无法翻页。
3. 冷启动只加载字母序前 3 个项目，其余项目标题出现但 session 为空。

## 根因（三个独立缺陷叠加）

### 缺陷 1：MacBridge hasMore 逻辑对小项目是错的

OpenCode 路径原来没有全量列表，靠「返回数 >= limit」猜 hasMore。小项目返回 2~3 条
（< limit 5），hasMore=false，iOS 据此判定「已到末页」，「加载更多」入口永不出现。

对比 Codex/Claude 路径用 `paginateSessionList`：内存里有全量列表，用
`len(sessions) > limit` 算 hasMore 并返回可翻页的 nextCursor。

### 缺陷 2：rootsOnly 客户端丢弃把子 session 全砍了

MacBridge 原来在 Go 侧 `continue` 掉 parent_id 非空的 session。OpenCode 重度项目的
子 session（subagent、fork、compaction）比例高，砍完只剩 1~3 条 root。

### 缺陷 3：冷启动 .prefix(3) 只给前 3 个项目发请求

`loadSessions` 中 `.prefix(3)` 硬编码只加载前 3 个项目 bucket，其余靠 LazyVStack
视口懒加载，截图时多数项目没滚到。

## 最终修复方案

核心思路：让 OpenCode 路径走和 Codex/Claude 完全相同的 `paginateSessionList` 分页。

### MacBridge 侧（go-bridge）

`ocHandleListSessions`（handlers.go）重写：

- 对每个项目一次性从 OpenCode server 拉取 100 条 root session（常量
  `openCodeSessionFetchLimit = 100`），而不是之前的 limit=5。100 匹配 server 默认上限。
- `rootsOnly` 不再在 Go 侧客户端 `continue` 丢弃，而是作为 `roots=true` 查询参数
  发给 server，由 server 做 SQL `isNull(parent_id)` 过滤（和 OpenCode 源码一致）。
- 拉回后在内存按 `updatedAtMillis DESC` 排序，然后调用 `paginateSessionList(mapped,
  cursor, limit)`——与 Codex/Claude 走同一个函数。
- `hasMore` 和 `nextCursor` 由真实剩余数据量计算，不再瞎猜。
- `rootsOnly + cursor` 不再被拒绝。

`OpenCodeProxy.listSessions`（opencode-proxy.go）增加 `Roots bool` 字段，发 `roots=true`。

### iOS 侧（cordcode-ios）

`loadMoreOpenCodeSessions`（SessionsView.swift）改为 cursor 追加分页：

- 不再用「limit 加 5 重取」的旧方式。
- 用 bucket 已存的 `nextCursor` 发下一页请求，`append: true` 追加到已有 session 列表。
- 守卫条件改为 `bucket.hasMore && !bucket.isLoading && bucket.nextCursor 非空`。

侧栏（SidebarView.swift）上一轮已补的改动保持：项目区块进入视口自动触发
`loadOpenCodeBucketIfNeeded`；未加载项目显示「加载中」，加载完为空显示「暂无会话」。

### 为什么不直接用 OpenCode server 的 cursor

OpenCode server 的 `/api/session` 有 cursor（`packages/protocol/src/groups/session.ts`），
但 stable 1.17.13 的 `/session`（instance httpapi）是 array-only。MacBridge 连的是
instance httpapi。所以一次性拉 100 条再在内存分页是当前最正确的做法；未来上游
instance httpapi 支持 cursor 后可零改动切换到 server-side cursor。

## 验证

- `go test ./go-bridge/... -count=1` 全通过。
- `TestOpenCodeListSessionsFetchesLargePageAndPaginatesInMemory`：验证上游拉 100、
  roots=true、limit=2 切片返回 2 条且 hasMore=true，第二页 cursor 翻页返回剩余。
- iOS `SessionLoadOwnershipTests` 通过。
- Mac Release build + /Applications 覆盖安装 + runtime 8777 确认。
- iOS Debug build 安装到 iPhone。
- owner 真机验收通过（2026-07-03）：项目标题 basename、Chat 项目首页 5 条可翻页、
  小项目显示真实数量、go-bridge.log 无 ERROR。

## 设计文档中过时的部分

设计文档判断 OpenCode 为 array-only/no-cursor，因此采用保守 limit=5 + 客户端 rootsOnly
丢弃。实际读源码后发现 server 默认 limit 50/100 且支持 roots SQL 过滤，改为一口气拉 100
再内存分页更正确。完成情况文档 §4 的 Known Limits 中「无服务端加载更多是正确行为」
已被本方案推翻。

## 2026-07-04 追加复盘：冷启动既有 Claude session 的 spurious session_state_changed(idle)

iOS 侧「首轮流式从头重播」再次复现后，跨仓联调定位到一条 Mac 侧的已知 artifact。

### 现象（iOS 侧表现，根因在 Mac）

iPhone 冷启动既有 Claude Code session 并发送消息后，回复输出一段后闪一下、从头重播，重复 3~4 次。
Mac 日志：单个 turn 内 `get_session_messages` 被调 336 次，但 `send_message` 仅 2 次、`text_delta` 正常生成 ——
说明 Mac 没有重复执行 prompt，问题在 iOS 反复拉历史覆盖 live timeline（iOS 侧诊断与修复见 ../cordcode-ios/think.md 同节）。

### 真正根因（Mac 侧）

既有 Claude session 的 transcript file relay 与真实 AgentSession stdout relay 共用 `relayRunning` 状态位。
冷启动既有 session 时，**file relay 抢先基于上一轮已完成的 transcript** 广播 `session_state_changed(idle)`，
几乎与 iOS 的 `send_message` 同时到达（实测 T+0ms）；真实 agent stdout relay 要等 CLI 首个 stdout 才报
`session_state_changed(running)`（实测 T+10s）。对 Claude Code 的长 thinking 阶段（首 token 30s+）来说，
这个 spurious idle 是**假的** —— CLI 正在跑、只是还没出 token。

`7c1d97d "Harden Claude cold-start relay handling"` 的 relay-kind 拆分（agent / claude_file）曾试图修这个窗口，
让真实 agent relay 能接管 file relay。但实测 Mac 仍会在冷启动时发 spurious idle —— relay-kind 拆分修的是
「file relay 占位导致 send_message 起不来真实 stdout relay」，没修「file relay 仍会广播基于旧 transcript 的 idle」。

### 本次处理

iOS 侧兜底（已实现）：Claude local turn 首 text_delta 前收到的 `session_state_changed(idle)` 一律忽略，
ownership 稳住 `.localSend` 直到真实 `turnCompleted`。详见 ../cordcode-ios/think.md「首 token 前 spurious idle 收口」节。

Mac 侧**未**在本轮改：spurious idle 仍会发出，但 iOS 不再据此收口。Mac 侧的正确修法（后续独立清债）应是：
file relay 不得在「真实 agent relay 未确认 idle」前单方面广播 idle；或 file-relay 的初始状态读取不得用上一轮已完成
transcript 的终态当作当前 turn 的初态。

### 关键诊断信号（Mac 日志）

- 正常：`send_message` → `relayEvents forwarding event=text_delta` → `turn_completed`（一条 turn 内 send=1, turn_completed=1）。
- 异常（本次 bug 间接证据）：`get_session_messages` 在单个 turn 内被调数百次（iOS 反复拉历史）。
- Mac 是否发了 spurious idle：搜 `session_state_changed` / relay-kind 日志，看 send 后是否有先 idle 后 running 的翻转。

### 后续原则

- relay 状态位（file vs agent）的拆分要彻底：file relay 不得在 agent relay 未确认前广播 session 状态翻转。
- iOS 侧对 Claude local turn 首 token 前的 idle 不信任是必要防御；Mac 侧的根因修复不能让 iOS 撤掉这层兜底
  （冷启动 / 重连等场景仍可能再次出现 spurious 状态）。
- 跨仓「流式异常」排查：先看 Mac `relayEvents forwarding` 是否正常生成 text_delta（排除 Mac/CLI 重跑），
  再看 `get_session_messages` 频率（iOS 是否在 turn 内反复拉历史），最后用 `devicectl --console` 抓 iOS 端 NSLog
  定位 ownership 翻转时机。

# Claude 斜杠命令 / skill 文档泄漏：Mac 已干净、iOS 仍脏 = iOS 本地缓存陈旧

2026-07-04。Mac 源头过滤已上线，用户仍反馈 iOS 消息页显示 skill 全文；最终根因是 iOS
本地缓存未自愈，冷启动后恢复。记此条防止后续在 Mac 侧空转。

## 现象

iOS Claude Code 模式下，含 `/handoff-doc`、`/takeover` 等 skill 调用的 session 在消息页
显示 skill 文档全文（`Base directory for this skill: ... # Mission ...`）和 CLI 内部协议标签
（`<command-name>` / `<command-message>` / `<command-args>` / `<local-command-stdout>` /
`<local-command-caveat>`）。Mac 侧 `agent/claudecode` 已实现源头过滤并对全机 141 个真实
transcript 回归 0 泄漏，但 iPhone 仍显示旧内容 —— 表面矛盾。

## 根因（纯 iOS 侧，Mac 已干净）

Mac 源头过滤（`agent/claudecode/claudecode.go` 的 `normalizeClaudeUserText` +
内容驱动 `isClaudeSkillInstructionText`，`extractTextContent` 同步清洗预览/标题）**已生效**：
对 iPhone 此刻正在轮询的 `d8bff4fb-8275-4659-b6fe-082559c63d92`（最初泄漏的 9 个之一）
把真实 transcript 喂给线上同一 parser，输出全部泄漏 marker = 0；`/Applications` 内嵌
runtime（22:59 构建、pid 38490、8777 监听者）含修复符号；wire 映射 `richHistoryEntryToWire`
是干净数据的纯变换，不会回污。

iOS 显示的是**修复前持久化的本地缓存**，两层：

- 内存缓存（`MessageCacheManager.getCachedMessages`）：命中时 `ChatViewModel+MessageSync.swift`
  的 `loadMessages` 用缓存 `replaceMessagesFromServer` 后通常仍会继续 fetch（Phase 4 后
  `usesBackendLiveEventStream` 全部为 true、Claude 走分页，line 169-173 的早返回不触发），
  但缓存首屏先显示。
- 磁盘快照 `~/Library/Application Support/SessionSnapshots/snapshot-<scopeHash>-<sessionHash>.json`
  （`SessionSnapshotStore.swift`）：**键只含 `(backend identity, sessionId)`，无内容版本号**；
  `currentSnapshotSchemaVersion=3` 只做「比 app 新才删」的前向校验，**不剔旧**。修复前写入的
  脏快照在普通重开/冷启动后仍会被 `loadSnapshot` 读出并先渲染。

冷启动清掉内存缓存后，磁盘快照路径 fetch 到 Mac 干净数据、`reconcileServerMessagesAgainstDisplayedSnapshot`
按内容 diff 自愈，并 `persistSnapshot` 写回干净快照。用户冷启动后所有 skill session 恢复正常。

## 验证

- 字节级取证（临时测试，已删）：对真实 `d8bff4fb….jsonl` 跑 `LoadClaudeRichHistoryFromReader`，
  118 条 / 1.28MB 输出中 `Base directory for this skill` ×0、五类命令标签 ×0、`## Mission` ×0，
  仅保留 2 个合法 `Launching skill` tool_result。
- 线上二进制 = 修复版：`strings` 命中 `isClaudeSkillInstructionText` / `normalizeClaudeUserText`；
  pid 38490 即 `/Applications/CordCodeLink.app` 内嵌 runtime。
- 设备验证（owner）：冷启动 iOS App 后打开原 session，skill 全文消失；其他 skill 命令 session
  亦符合预期。

## 后续原则

- **「Mac 干净但 iOS 脏」排查优先级**：先在 Mac 侧对 iPhone 实际请求的那个 sessionID 做字节级
  取证（runtime 日志里 `get_session_messages` 的 sessionID + response_bytes），再动 iOS。
  Mac 干净则问题在 iOS 缓存或传输，不要回头改 Mac。
- **Mac 内容侧修复对 iOS 不是即时生效**：iOS 有内存缓存 + 磁盘快照两层，磁盘快照键无内容版本。
  一次冷启动可触发 reconcile 自愈；若需强制清旧脏快照，`SessionSnapshotStore` 没有 min-schema
  删除逻辑，得 `clearAllData` / 删 SessionSnapshots 目录 / 重装 App。
- iOS 源头治理（可选清债，本轮未做）：给 `SessionSnapshotStore` 加 min-schema（低于即删）或内容
  hash 版本键，让 Mac 侧的内容级修复能确定性失效旧快照，而不依赖冷启动 + reconcile 时序。
- 排查工具：`tail -f ~/Library/Application\ Support/CordCode\ Link/logs/go-bridge.log` 看
  `get_session_messages` 的 sessionID/response_bytes/result_count；对目标 session 可写临时 Go 测试
  调 `LoadClaudeRichHistoryFromReader` 对真实 JSONL 取证。

## 2026-07-05 Claude session PID 复用 latent bug（已修）

- **现象假设**：某 Claude session 的 stub（`~/.claude/sessions/<pid>.json`，含 `sessionId/pid/cwd`）
  因 claude 异常退出未清理而残留；OS 把该 PID 复用给无关进程。`GetRunningSessionIDs` / `LiveSessionProcess`
  原本只用 `kill(pid, 0)` 判活 → stale session 被误判 running → `enrichSessionStateWithAgent` 在
  `resume_session`/`list_sessions` 响应里报 `runtimeState=running` → iOS 进入 phantom executing
  （输入框锁"执行中"、status strip 不消失）。
- **07-05 复现未触发**：那次是 external turn（用户在 Mac Claude 窗口打字），且目标 session `16c63341`
  的 stub 当时正确缺失（`~/.claude/sessions/` 只有其它 PID），所以 `GetRunningSessionIDs` 正确报 idle。
  但代码审查发现 `agent/claudecode/proc_unix.go:49 isProcessRunning` 是纯 `kill(pid,0)`，不校验进程身份，
  是真实 latent bug。
- **修复**：`agent/claudecode/proc_seam.go` 新增可注入 seam `procIdentityAlive(pid, expectCwd)`，在
  `procAlive`（liveness）之上叠加 `verifyClaudeProcessIdentity`：`ps -p <pid> -o comm=` 校验可执行名含
  `claude`，`/proc/<pid>/cwd` readlink（Linux）/ `lsof -a -p <pid> -d cwd -Fn`（macOS）校验 cwd 与 stub 一致；
  任一强不匹配 → 非 live；平台探测失败 fail-open。`LiveSessionProcess` 和 `GetRunningSessionIDs` 改用该 seam。
  `IsProcessAlive`（公共契约，`go-bridge/handlers_relay.go:263` file-relay 每 tick 复查 cached PID 用）保留
  纯活性不动——那时身份已在 relay 启动时确认过一次，复用 PID 顶多多 silent watch 一个 live-idle TTL（90s），
  不发伪事件。回归测试 `TestGetRunningSessionIDs_PIDReuseNotRunning`。
- **排查要点**：报告"iOS phantom executing"时，先查 `~/.claude/sessions/*.json` 里目标 sessionId 的 stub
  是否残留 + 该 PID 现在是否仍是 claude（`ps -p <pid> -o comm,cwd`）。Mac 日志看 `enrichSessionStateWithAgent`
  返回的 runtimeState 需对照 transcript 是否真在跑（`isSessionExecutingCached`）。


# OpenCode active turn 流式：批处理 CLI → managed server + SSE（2026-07-06）

## 现象
iOS OpenCode 模式发消息无流式（等整段答完才出现）；Claude Code 模式正常。owner 测试矩阵：OpenCode（mimo V2.5 free）Mac 流式 / iOS 非流式；Codex 经官方/cliproxyapi 两端都流式；Codex 经 cligate 两端都非流式。

## 根因（OpenCode）
`agent/opencode/opencode.go` `StartSession` → `newOpencodeSession` 永远 spawn `opencode run --format json`（批处理，一轮 turn 只发 1 帧 `text_delta`），**完全绕过** Swift 已托管的 `opencode serve`（`-opencode-url` 传入的 `httpBaseURL`/`httpAuthHeader` 在 active turn 路径是死字段，只被被动 SSE subscriber + 诊断用）。managed server 本身流式正常（mimo 实测一轮 turn 49~80 帧 `message.part.delta` 分布在数秒内）。整个 opencode agent 改造前没有任何 POST 发消息代码（`providers.go` 只有 GET `fetchJSON`）。

## 修复（commit `330de91`）
- 新增 `opencodeServerSession`（`agent/opencode/server_session.go`，实现 `core.AgentSession`）：`Send` 时 `POST /session/:id/prompt_async`（204 非阻塞），消费一条 dedicated、按 sessionID client-side 过滤的 `/global/event` SSE。
- **复用** `sseSubscriber` 全套解析 + dedup + 生命周期翻译（`message.part.delta`→`EventText`、`session.status idle`→`EventResult`、`message.updated`→snapshot diff），只新增 `sessionFilter`（atomic；pending 态 chatID 未定时全丢，避免把别的 session 事件串到当前 iOS turn）。
- `StartSession` 按 `httpBaseURL` 分流：server 在 → `newOpencodeServerSession`；否则回退 `newOpencodeSession`（批处理 CLI 兜底，不中途切换）。
- 模型经 `resolveOpencodeModelLocked`（active provider 的 Name/Model）解析，建 session 时用 `{model:{id,providerID}}` 绑定。
- `providers.go` 加 POST-capable `doRequest`，`fetchJSON` 复用。
- live 集成测试（`server_session_live_test.go`，env-gated `OPENCODE_LIVE=1`）：80 帧流式 vs 批处理 1 帧。owner iOS mimo 真机验收逐字流式。

## 关键实证（防下次重新摸黑）
- **prompt body 的 `providerID/modelID` 不生效**（实测 Quotio body 被忽略，仍用 session 默认）。模型必须 **session 级**设定：`POST /session {model:{id,providerID}}`（字段是 `id` 不是 `modelID`）。
- managed server 上 **xirang 报 ProviderModelNotFoundError、zhipuai-coding-plan retry 5 次失败**（疑似 auth 绑 Mac opencode App 而非 managed server），只有 **opencode/mimo-v2.5-free** 实测跑通。owner 决定只管 mimo，zhipu/xirang 不查。
- `message.part.delta` = 流式 token 事件（sst/opencode#33397）；`/global/event` server 端**不支持** sessionID 过滤（sst/opencode#9650），**必须 client-side 过滤**（`sse_subscriber.go` 的 `extractSSESessionID` 已这么做）。
- `opencode serve` 的 SSE 有已知可靠性 bug（静默丢 #28729、不转发 #26866）—— server_session 不能假设 SSE 永远可靠，依赖 `deltaForPartSnapshot` 兜底。
- `x-opencode-directory` header 在 CJK workDir 有 bug（#13167/#13256），本仓库中文 owner 需留意。

## Codex 流式 —— 甄别 cligate 供应商，不要错查到 appserver_session.go
codex app-server（`agent/codex/appserver_session.go`）本身发 `item/agentMessage/delta` 完全正常（实测 31~38 帧/turn，通知名经二进制 strings 验证正确，handler/optOut/transport 全对，stdio 和 ws 两种传输都验证过）。**经官方/cliproxyapi 供应商 iOS 本就流式**。**经 cligate 供应商两端都不流式**（Mac codex + cligate 也一样），根因是 cligate 上游：`src/routes/responses-route.js:972` 和 `:1192`（`_responsesToChatBody` / `sendViaNativeResponsesProvider`）把 Responses→Chat Completions 时**硬编码 `stream:false`**，攒满整段再 `sendResponsesSSE()` 假装流式；同文件 `:1423-1441` 的 ChatGPT 账号池路径已是真流式 pipe，可参考修。**排查 codex 流式先确认供应商**，别一头扎进 appserver_session.go（那里没 bug）。

## 诊断 trick
- 数一轮 turn 的 `text_delta` 帧（go-bridge.log `relayEvents forwarding`）：1=批处理，多=流式。Claude ~1495 帧/turn 是参照。
- codex app-server 通知名可用 `strings /Applications/Codex.app/Contents/Resources/codex | grep -i agentMessage` 核实（不需 runtime 抓包）。
- opencode managed server 状态：`cat "$HOME/Library/Application Support/CordCode Link/opencode-managed-server.json"`（url/user/password），curl `http://127.0.0.1:<port>/global/config` 看 providers。
- codex app-server WS daemon：`codex app-server --listen ws://127.0.0.1:<port>`（CLI 原生支持，加 `--listen` flag）。

## Grok Build 真机疑难排查（2026-07-12，5 个 bug 全部修复）

### Bug 1: StartSession RWMutex read→write 升级死锁 → 30s RPC 超时
- **现象**：Grok session 发消息后 30s 超时。MacBridge 日志能看到 `initialize_done` 但没有 `session_loaded`。
- **根因**：`Agent.StartSession` 持 `a.mu.RLock()`，内部 `newGrokSession → loadSession → SetWorkDir` 请求 `a.mu.Lock()`。Go `sync.RWMutex` **不支持 read→write 升级**——永久死锁。
- **教训**：Go RWMutex 的 RLock→Lock 是永久死锁（不 panic）。StartSession 这类入口方法不要持锁——子方法自己加锁。
- **修法**：移除 `grokbuild.go` StartSession 的 RLock。

### Bug 2: convertSessionUpdate 未知类型 → EventError → iOS "unknown error"
- **现象**：turn 完成后弹 "unknown error"。
- **根因**：`acp_codec.go` `convertSessionUpdate` default 分支把未知 sessionUpdate type 转成 `EventError{Done:true}`。iOS 映射为 `("error", {message:"unknown error"}, done=true)`。
- **修法**：default → `return nil`（静默跳过未知类型）。

### Bug 3: legacy HistoryProvider 无 ID → iOS probe 误激活 generation
- **现象**：打开 Grok 历史 session 输入框卡"执行中"。
- **根因**：Grok 只实现 `core.HistoryProvider`，`core.HistoryEntry` 没有 `ID` 字段。iOS 为缺失 ID 生成随机 UUID → external-turn probe 误判新 turn。
- **教训**：历史消息 ID 必须稳定。修复路径是 `RichHistoryProvider`（`core.RichHistoryEntry` 有 ID）。ID 从 JSONL **物理行号**（不是过滤后数组索引）+ 原始行 hash 派生。
- **修法**：`session_catalog.go` 新增 `GetRichSessionHistory` + `deriveStableMessageID`。`grokbuild.go` 加 `var _ core.RichHistoryProvider` 断言。
- **关键约束**：`legacyHistoryEntryToWire` 是包级函数，没有 sessionID/行号参数——**不能在 legacy 路径补建 ID**。必须走 rich 路径。handler 已优先走 rich（`handlers.go:2442`）。

### Bug 4: handlers.go grokbuild 跳过 session_state_changed(running)
- **现象/根因**：grokbuild 的 `turn_started` 事件已通过 `syncRuntimeStateStore` 激活 iOS 执行态。额外发 `session_state_changed(running)` 会让 `isGenerating` 过早激活；如果 turn_completed 的 debounce 在 session 切换时被取消，isGenerating 永久残留。
- **修法**：`handlers.go:1481` `if agent.Name() != "grokbuild"` 跳过 running 广播。

### Bug 5: relayEvents idle timeout 后事件投递正常（非 MacBridge 问题）
- **排查结论**：5G relay 冷启动发消息卡住的问题，MacBridge 侧完全正常（日志确认 `send_message` → `turn_started` → `turn_completed` 全链路转发，`RelayDeviceConn.SendJSON` 无 dropped）。根因在 iOS 侧的 stale background generation marker（详见 iOS 仓 `think.md`）。
- **教训**：排查 relay 卡住时，先确认 MacBridge 日志有完整的 `relayEvents forwarding` 链——如果有，问题在 iOS 侧，不要改 MacBridge。

---

## 2026-07-13 Codex 既有 session 的思考过程历史重放

### 现象

Codex Desktop 的旧 session 在 iOS 首次重放时会缺少工具步骤，或只显示「已执行 N 个工具」。即使 iOS
清除了本地缓存，展示仍不变。

### 根因

Codex transcript 的 `custom_tool_call` 常以 `name=exec` 记录，真实 `tools.exec_command` / `tools.apply_patch`
嵌在 JavaScript 输入；`custom_tool_call_output` 则可能是结构化 content array。旧 parser 只把
`apply_patch` 视为有效 custom call，并把 output 当 JSON string 解码，导致数组解码失败后 tool completion
被丢弃。另一个常见误判是只编译 Debug Mac app：iPhone 的 bridge 实际运行 `/Applications/CordCodeLink.app`
内嵌 runtime，Debug 产物不会替换它。

### 修复与原则

1. `Output` 使用 `json.RawMessage`，兼容 JSON string 和带 `text` 字段的数组，保留真实 tool output。
2. 对 `exec` 包装只在其中包含**单一且可解析**的真实操作时还原：`exec_command` 提取 `cmd`，
   `apply_patch` 显示 patch 目标。多操作/混合包装必须保留 generic，不能杜撰为某一个操作。
3. parser 改动需要 `go test ./agent/codex -run TestGetRichSessionHistory -count=1`；交付到 iPhone 前还必须
   Release 构建、覆盖安装 `/Applications/CordCodeLink.app`、重启 app，并比对内嵌 runtime。只重装 iOS App
   无法部署 Go parser 改动。
4. 当「清 iOS 缓存后仍旧」时，先核对当前运行 PID 是否来自 `/Applications/CordCodeLink.app`，再检查
   installed runtime 是否与新 Release 一致；确认这一点前不要把问题归因为 iOS cache 或 renderer。

## 2026-07-14 Codex idle 历史 session 误触发执行态并导致 iOS 滚底

### 现象与证据

iOS 打开已经完成的长 Codex history 时，会周期性拉回底部。MacBridge 日志中该 session 没有 active runtime，
请求也始终是 `get_session_messages paginate=false limit=0`，所以这不是分页；但 idle 状态反复传输完整 history，单次约 5 MB。

### 根因与修复

Codex transcript file relay 已经是外部 turn 的权威来源，会发送真实的 `turn_started` / `turn_completed`。但
`agentDescriptor.RequiresPollingForExternalTurns` 仍把 Codex 标为需要 polling，促使 iOS 对已打开 session 做 full-history resident probe。transcript 补全或重写随即被客户端投影为新 turn，进而触发 follow-output 滚动。

因此 Codex descriptor 现在明确返回 `false`，由 transcript relay 负责真实 turn 生命周期；iOS 同时删除自己的 Codex 无条件 fallback。该标记是跨端状态机契约，不能把它当作可随意保留的兼容开关。

### 可复用教训

1. 已支持可靠 `turn_started` / `turn_completed` 的 driver 必须关闭 history polling；两条路径并存会让历史重写伪装成 live turn。
2. 排查“iOS 自动滚底”先查 `get_session_messages` 参数、active runtime 与 relay event 链路。滚动层通常只是正确响应了错误的 generating 状态。
3. 对长 transcript，full-history polling 既放大流量也放大误判窗口；完成态 session 不能以 history diff 作为活跃任务证据。

### 验证

`go test ./go-bridge -run TestAgentDescriptorCodex -count=1` 通过，Release runtime 已替换并配合 iOS 重装。owner 于 2026-07-14 手动打开并上滑已完成 Codex history，确认不再跳到底部；未运行 UI 自动化。

---

## 2026-07-14 Grok rich history 过程相位不能被 accumulator 重排

### 现象

Grok 已完成 history 已经能提供结构化 `parts[]`，但 iOS 展示仍不像真实执行过程：一轮中多个 reasoning
被集中在最前，工具调用集中在其后。前端即使按 parts 顺序渲染，也只能得到错误的相位排布。

### 根因

`turnAccumulator` 为了形成一条 assistant turn，将每一段 reasoning 汇总到独立字段，并在 build 时无条件
prepend 为第一个 `reasoning` part；工具和正文则另存 parts。于是原始
`reasoning → tool → reasoning → tool → text` 被不可逆地改写为
`reasoning(合并) → tool → tool → text`。

### 修复与约束

1. reasoning 只允许与**紧邻**的 reasoning 合并；遇到 tool 或 assistant text 必须 flush 到 `parts[]`。
2. assistant text 也必须作为真实 text part 写入，既是正文又是下一段 reasoning 的相位边界；不能仅留在
顶层 `Content`。
3. `build()` 只能 flush pending reasoning 后直接使用累积 parts，禁止再 prepend 一个汇总 thinking part。
4. 以合成 fixture 断言完整序列 `reasoning, tool, reasoning, tool, text`；只断言“含有所有 part”不足以防回归。

### 可复用教训

- `parts[]` 的顺序是 presentation contract，不是可自由规范化的缓存。任何跨类型重排都会让消费端失去
  恢复真实时间线的能力。
- 当视觉问题呈现为“所有思考在前、所有工具在后”时，应先检查 producer 是否丢失相位边界，而不是先改 iOS
  group/CSS。
- Go driver 变更只有 Release 构建、替换 `/Applications/CordCodeLink.app` 并重启 runtime 后才会作用于真机；
  iOS 重装本身不会更新 MacBridge parser。

## 2026-07-15 MacBridge 国际化（L10n）首选语言脏缓存与超时误判为配对失效

### 现象

1.  **系统语言检测被脏缓存覆盖**：Mac 系统的真实语言是中文，但安装最新版 MacBridge 启动后却依然默认显示为英文。
2.  **配对通道误报错失效**：用户打开「配对新设备」弹窗仅几秒钟，就突然弹出报错「配对通道已失效，配对二维码在 5 分钟内未被扫描会自动过期。请重新生成。」

### 根因

1.  **首选语言脏缓存机制**：
    *   SwiftUI 的 `@AppStorage("appLanguage")` 被用来持久化语言。如果之前在测试或历史版本中，由于默认策略或手动点击，导致本地 `UserDefaults` 中已经存入了 `"en"` 的键值，之后的启动将永远读取这个 `"en"`，这就产生了“脏缓存”的现象。
    *   在 macOS 沙盒下，如果没有在 Xcode 工程中显式完成中文 `.lproj` 资源束绑定，macOS 会对 `Locale.preferredLanguages` 进行自动截断和过滤（由于 App 本身在系统看来不支持中文，所以强制过滤为 App 所支持的 `en`），这就导致 `Locale.preferredLanguages.first` 永远返回 `en`，默认初始化的 fallback 完全失效。
2.  **网络超时被误判为 5 分钟到期**：
    *   在 `PairingView.swift` 的 `errorStateView` 判定逻辑中，错误地将包含 `"request timed out"`（即网络请求超时）的所有网络底层错误（例如因为向公网 Relay 轮询 claim 发生超时或连接被取消等），全部误判归类为了 `isTimeout`，进而渲染成了「配对通道已失效，二维码在 5 分钟内未扫描会自动过期」。
    *   事实上，配对通道真正的“5 分钟倒计时到期”有专门的 `.expired` 状态机做权威处理，完全不需要在普通的异常信息中通过包含 `"timed out"` 这种字符串来进行模糊映射。这直接导致网络波动时的临时超时报错被严重扭曲为了“二维码过期”。

### 修复方案

1.  **引入 `didUserSetLanguage` 用户主动切换标志**：
    *   在 `UserDefaults` 引入 `didUserSetLanguage` 偏好门控标志。如果用户从未在右上角菜单中手动设置过语言（标志为 `false`），则强制忽略本地任何 `appLanguage` 缓存，直接以系统当前的真实偏好为准。
    *   **获取真实系统语言**：改用 `UserDefaults.standard.stringArray(forKey: "AppleLanguages")` 这条系统最原始的全局首选语言链，这能 100% 绕过 App 自身本地化包带来的截断过滤，真实拿取 Mac 当前的语言环境（中文系统必定为 `"zh-Hans"` 或 `"zh-Hans-CN"`）。
2.  **剔除误导性的超时映射**：
    *   移除 `errorStateView` 里面对 `"request timed out"` 的模糊匹配判定。让所有的网络层普通超时报错正确回归到 `exclamationmark.triangle`（配对发生错误）类型，并附带明显的「重试」按钮，使用户知晓这是网络异常而非二维码过期。

### 验证

*   `xcodebuild -project MacBridge/CordCodeLink.xcodeproj -scheme CordCodeLink -configuration Debug -destination 'platform=macOS' test` 全套单元测试通过，尤其是更新了 `LayoutConstants.connectionSheetHeight` 调整至 `740` 防止英文滚动条的 IA 契约尺寸测试。
*   Release 构建覆盖安装后，删除 App 缓存重新打开，中文系统成功首次默认加载中文界面。

### 后续原则

*   在 macOS 中进行设备检测和语言检测时，优先使用 `UserDefaults.standard.stringArray(forKey: "AppleLanguages")` 来获取真实的系统环境，防止因 App 本地化支持范围不一致而被系统沙盒强行过滤截断。
*   异常渲染不得将一般的网络状态或动作超时（Network / Request Timeout）和产品业务规则上的时间到期（Business Session Expiration）混为一谈。

## 2026-07-19 Claude 外部 turn file-relay：turn_completed 后不能 return，进程 live 时继续 watch

### 现象（owner 真机复现，iOS+Web 同时）

Mac Desktop/CLI Claude 多轮外部 turn（Mac 端发起、客户端旁观）出现：

- Web 完全收不到回复，必须靠「下一问」才把「上一答」同步出来
- iOS 历史同步也偶发掉 turn
- file-relay 日志里频繁出现 `claudeSessionFileRelay turn completed, exiting`，然后该 session 进入
  「无 relay」状态，直到下一次 `get_session_messages` 才重启

### 根因（go-bridge 侧）

`claudeSessionFileRelayLoop`（`handlers_relay.go`）在检测到 `finalAssistant` 时：

```go
if entry.finalAssistant {
    h.sendSessionEvent(sessionID, backendID, "turn_completed", ...)
    h.broadcastIdleState(sessionID, backendID)
    slog.Info("go-bridge: claudeSessionFileRelay turn completed, exiting", ...)
    return  // ← BUG
}
```

`return` 让 goroutine 退出，`relayRunning[sessionID]` 标记被清掉。Claude Desktop 在**同一 PID**
上连续多轮 turn 是常态——下一轮 user 写入 JSONL 时，**没有任何 goroutine 在 watch**，
`turn_started` 永远不会发出，直到客户端发起下一次 `get_session_messages` 触发重启 relay。
窗口期内客户端既收不到 `turn_started`，也看不到正在生成的 assistant body（Claude Desktop
只在 end_turn 才 flush JSONL），表现为「必须等下一问才能看到上一答」。

另一个相关 bug：live-idle TTL 退出条件原本是「90s 无文件增长就退出」，**不管 Claude 进程
是否仍存活**。Claude 长 thinking 阶段 transcript 静默 90s+ 是正常的，却被误判为「session 已死」。

### 修复（go-bridge）

1. **`finalAssistant` 不再 `return`**：改为 `runningObserved = false; continue`，在 Claude 进程
   仍 live 时继续 watch。下一轮 user 写入 JSONL 立刻广播 `turn_started`。
2. **live-idle TTL 仅在进程已不存活时才退出**：进程仍 live 但 transcript 静默时保持 watching，
   防止长 thinking 被误杀。原退出条件保留作为进程死亡后的清理路径。

### 测试更新

`claude_file_relay_test.go`：

- `TestClaudeFileRelayTickUsesCachedPID`：live 时保持 running，进程死后才停（断言
  `handlers.relayKindIs(sessionID, relayKindClaudeFile) == true`）。
- 其它已有用例（WarmStartUser / LiveIdleSnapshot / Interrupt 等）不依赖「turn_completed 后
  退出」，只读 2 条事件即通过，无需修改。

`go test ./go-bridge/ -run ClaudeFileRelay` 全部通过。

### 配套 Web 侧修复（详见 ../cordcode-ios/think.md 复盘 VII-IX）

Web 端也有一个叠加 bug：`applyExternalTurnHistory` 无脑把 trailing assistant 标成
`isStreaming:true`，导致安全网 `externalTurnLooksComplete` 永远 false，即使服务端已 flush 出
完整 body 也永不自动 settle。Mac 修了 file-relay 不退出 + Web 修了不强制 streaming，
两层叠加的「下一问才同步上一答」才彻底消除。

### 后续原则

- **长生命周期 watcher goroutine 不要在常规完成信号上 `return`**：完成 ≠ session 关闭。
  只要有可能产生下一轮事件（同 PID、同 socket、同 session），就应该继续 watch，把退出条件
  严格限制在「真正不可恢复」（进程死亡、socket 关闭、超长 idle + 进程不存活）。
- **跨客户端 turn 同步需要双端配合**：Mac 端负责广播边界事件，客户端负责在缺事件时也能
  从权威历史推导 settle；任何一端假设「对方一定会发事件」都会在边界条件下丢 turn。
- **file-relay 的退出语义**：「finalAssistant 写入」只是 transcript 状态变化，**不是 watcher
  生命周期事件**；这两个概念必须分开。

---

# 2026-07-21 跨仓指针：iOS 输入框执行中 / 外部 turn 收口（本仓无代码变更）

> **完整复盘在 iOS 仓** `../cordcode-ios/think.md`「2026-07-21 复盘 XI」。  
> 设计/实现/审计：`../cordcode-ios/docs/2026-07-21-ios-generation-single-authority-*.md`。

## 结论（Mac 侧只需知道）

- **根因在 iOS generation 多权威收口**（expected stale 裸 return、多 poll force-complete、load 内自 complete、Idle 下 delta activate 等），**不是**本轮 go-bridge EMIT 缺失。
- owner 真机卡住瞬间 `go-bridge.log` 常有 `codexSessionFileRelay EMIT turn_started/turn_completed` + history 增长 → 投递侧可用；排障仍用 EMIT 日志 + LAN/relay 对照。
- **本仓 2026-07-21 无业务代码 commit**；file-relay「turn_completed 后继续 watch」原则仍见上文 2026-07-19 节。
- iOS 已收敛：输入框 `isGenerating||requiresAction`、HEAL、`externalTurnLooksComplete`、load post-apply settle、Idle 不 activate；owner 三连 ✅。剩余 G1 poll 函数合一 / G6 recover 结构在 iOS 后续 PR。
# 2026-07-22 Codex rollout identity / completion boundary

Codex file-relay 与 rich history 曾把同一 rollout turn 投影成不同身份：scanner 已读取 `task_started.turn_id`，但 lifecycle payload 的 `turnId` 为空，history entry 又使用独立派生 ID。更严重的是 rich-history reader 在当前文件 EOF 就写 `TurnCompletedAt` 并把最后文本标成 final；活跃 rollout 每次增长都会被客户端观察成一次伪完成。

现在以 rollout 原生信号为唯一真值：`task_started.turn_id` 贯穿 lifecycle、delta `itemId` 与 history entry ID；EOF 保持 progress 且无完成时间，只有 `task_complete` 关闭 turn。transcript index span 同时覆盖 start/complete 记录，分页 replay 不丢这两项证据。消费端因此可以按 exact ID reducer 合并，不需要正文相似度启发式。

第五轮真机回归进一步证明，仅修 assistant 身份仍不够：Codex rollout 会在 `task_started` 后写入 `response_item(role=user)`，旧 file-relay 忽略该记录，导致 Mac 端问题只能随 history 回源迟到。现在 scanner 将它映射为 `user_message`，复用 response-item `id`，并绑定当前 source turn ID；`event_msg.user_message` 不重复解析，避免 rollout 双写造成重复。iOS 的 foreground/history reconcile 同时被约束为活跃 push turn 的 merge-only 校准者，不能凭部分 history 提前结束 turn。

---

---

# 2026-07-27 K5.2 Claude projection SoT（uuid + keep-watch + catch-up）

完整产品复盘在 iOS 仓 `../cordcode-ios/think.md`「复盘 XVIII」。本仓只记 go-bridge 契约。

## 修了什么

1. **`b787975`** — Claude transcript identity  
   - 结构加顶层 `uuid`  
   - turn/item：`message.id` 优先，否则 `uuid`  
   - live file-relay growth 复用 `claudeEntryToProjectionEvents`（带 identity 的 user_message / text_delta / turn_completed）  
   - 禁止空 `turnId` 的 turn_started / 无 itemId 的 text_delta（reducer 会 skip）

2. **`a39133e`** — multi-session live + reopen  
   - process 未 live：**继续 watch** transcript，不立即 exit；PID 晚到可 late-bind  
   - 未 live 时不根据历史 tail 武装 running  
   - `ProjectionKernel.committedSourceCursor`：Ready 后若 `source.Cursor` 更大，强制 catch-up hydrate，而不是 `AlreadyReady`

## 为何需要

Owner K5.2：A 同步正常，B 打开后 Mac 发消息 3 无 live，切回仍无。日志：B relay `process not live ... exiting`；reopen `headRev` 钉死旧 rev。根因在本仓 SoT，不在 iOS UI。

## 测试

`go test ./go-bridge -run 'TestClaudeFileRelay|TestClaudeEntryToProjectionEvents|TestProjectionCatchUpWhenSourceAdvancesPastReady'`

## 原则

- Claude user 身份以真实 JSONL 为准（常无 message.id）。  
- file-relay 生命周期 ≠ process 此刻可发现。  
- Ready projection 必须能对 source 增长 catch-up。  
- 不把这类洞交给 iOS referee。



## OpenCode K5.3 (2026-07-28)

OpenCode session_sync_v2 via rich-history hydrate + live TurnID/ItemID.

Follow-on SoT fixes same day (owner matrix green):
1. `handleOpenCodeRPC` allowlist `get_session_projection` (cold open).
2. `DeltaBatcher` preserve `itemId` on text/reasoning flush (live content).
3. SSE `noteUserPrompt` for bare user + part.delta (user bubbles).
4. Multi-step: do **not** emit EventResult on intermediate assistant `time.completed` / `step_finish`; only `session.status`/`session.updated` idle closes the turn (composer no longer flips idle on tools).
5. iOS side (sibling repo): v2 allows todos control-plane; discards stale completed plans on new generation.

Tests: `go test ./agent/opencode -run 'TestSSESubscriber_MultiStep|CompletionIsIdempotent|ToolTodoAndIdle'`.


# 2026-07-29 remote-web 首开 Claude projection 报 project dir not found

## 现象与证据

web 第一次打开 Claude session，`get_session_projection` 立即
`projection.hydrate_failed: claudecode: project dir not found`；再打开任意 session 后恢复。
同一时刻 `claudeSessionFileRelay` 能找到正确 JSONL，说明 session 与 transcript 都存在，失败只在
cold hydrate source inspection。

## 根因

Claude `prepareProjectionHydrateSource` 通过 agent `TranscriptPath` 查文件；该实现从共享
`agent.workDir` 推导 `~/.claude/projects/<key>`。首开前 workDir 还是 runtime 启动目录，尚未被其他
带 directory 的 RPC 更新。后续“自愈”只是别的请求偶然改了共享状态。

直接在 read-only projection handler 上 `SetWorkDir` 也不安全：多设备可能同时冷开不同项目，
共享 agent workDir 会产生跨 session 竞态。

## 修复

- `get_session_projection` 将 request directory 传入 hydrate source resolver；
- Claude 用已有 `findClaudeSessionFile(sessionID, directory)` 解析真实 transcript；
- 不修改 agent workDir；Codex/OpenCode source 路径不变；
- 测试以 stale agent workDir + 正确 session directory 冷拉，证明投影有内容且 workDir 未变化，
  `-count=10` 稳定通过。

## 验证边界

- 新增定向测试及相关 projection tests 通过；
- Release build 通过；
- 仓库全量 Go 仍有两个独立既有失败：
  `TestScanCodexTranscriptRelayEventsToolsAndTokens`（Codex itemId）、
  `TestRegressionR1_LeaseAutoDowngrade`（lease expiry）；单独重跑仍失败，本轮不顺手改。

## 2026-08-07：Codex archived session hydrate 失败

`findSessionFile` 只扫 `~/.codex/sessions/`，Desktop archive 物理移动到
`archived_sessions/` 后 cold hydrate 报 session file not found。
修复：active 优先，archived fallback（`7baafd8`）。

## 2026-08-12：Claude Desktop archive/delete 不消失（iOS 列表残留）

### 现象
owner 真机矩阵：Claude Code 模式下 Mac 端改名/新建/发消息都能同步，但 Mac 端
Claude Desktop archive/delete 后，iOS 列表仍显示这些 session，重启 iOS App 也一样。

### 根因（MacBridge 单侧，compatibility catalog 看不到 Desktop 私有状态）
`claude_session_catalog.go` 只扫 `~/.claude/projects/**/*.jsonl`。Claude Desktop 3P
（`claude-desktop-3p`）archive/delete **不改 JSONL 文件**：
- archive：只把 `~/Library/Application Support/Claude-3p/claude-code-sessions/<acct>/<org>/local_*.json`
  里的 `isArchived` 置 true；transcript 文件原样保留。
- delete：只删自己的 `local_*.json` 并在同目录写 `deleted_<uuid>` tombstone；
  transcript 文件也原样保留。

日志证据（`~/Library/Logs/Claude-3p/main.log`，2026-08-12 00:12–00:17）：
`LocalSessions.delete/archive` 与实际 `local_*.json` + `deleted_*` 一一对应；
对应的 Chat JSONL 仍存在。

### 修复
新增 `claude_desktop_state.go`：读 Desktop 自己的纯数据文件（不碰 app.asar /
私有 Electron API），把
- `local_*.json isArchived=true` → 给对应 CLI transcript 打 `ArchivedAt`（文件 mtime），
  catalog 照旧输出 `archivedAtMillis`，iOS/remote-web 既有过滤逻辑隐藏；
- `deleted_*` tombstone → 直接从 catalog 排除对应 JSONL；
- Desktop 文件 mtime 并入 `claudeSessionFingerprint`，archive/unarchive/delete 都能
  让缓存失效并触发 `sessions_changed`。

兼容两个 App Support 根：`Claude-3p/` 与 `Claude/`，并同时扫
`claude-code-sessions/` 与 `local-agent-mode-sessions/`。解析失败时 fail-safe：
不伪造结果，维持旧 JSONL 列表行为。

### 验证
- 定向单测：`TestClaudeSessionCatalogHonorsDesktopArchiveAndDelete`（archive 标记、
  unarchive 失效、tombstone 排除与恢复）。
- 本机真实状态校验（临时测试，未提交）：`91330136-…`（已 delete tombstone）不再列出；
  `9e5bc559-…`（已 archive）带 `archivedAtMillis`。
- 全量 `go test ./go-bridge/...` PASS；定向 race PASS。
- Release 重建 + 覆盖安装 `/Applications/CordCodeLink.app`。

### 诚实边界
Desktop 存储格式是私有且可能随版本变化；这是兼容 catalog 的 best-effort，不是
Claude 原生 catalog 同源。等 Claude 上游暴露稳定 catalog 接口后再迁移，不要把它
当成 exact parity。

### owner 复测（2026-08-12）
真机复测反馈「基本符合预期」：Desktop archive/delete 后 iOS 列表能及时消失，
unarchive 也能恢复；本轮修复闭环。
