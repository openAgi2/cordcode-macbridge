# Claude Code 冷启动既有 session 首轮流式从头重播：跨仓排查结论

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

