# Codex Runtime / Session 源码走读

> 日期：2026-04-30
> 范围：`cc-connect` 的 Codex `exec` / `app_server` 两套 runtime 内核，以及当前仓库 `codex-cli.mjs` / `codex-appserver.mjs` 的偏差。

## 1. 走读文件

- `cc-connect/agent/codex/codex.go`
- `cc-connect/agent/codex/session.go`
- `cc-connect/agent/codex/appserver_session.go`
- `bridge/src/backends/codex-cli.mjs`
- `bridge/src/backends/codex-appserver.mjs`

## 2. `cc-connect` 的 Codex 真相层

### 2.1 Agent 层已经把 backend mode 明确成 `exec` / `app_server`

`cc-connect/agent/codex/codex.go` 不是把 Codex 当单一路径处理，而是明确支持：

- `backend = exec`
- `backend = app_server`

这点非常重要，因为当前仓库也同时存在：

- `codex-cli.mjs`
- `codex-appserver.mjs`

也就是说：

- **`cc-connect` 已经证明了“两种 Codex runtime 共存但统一收口”是可行的。**

### 2.2 `exec` path 的关键不是 JSON 解析，而是 turn 状态机

`cc-connect/agent/codex/session.go` 最关键的不是 `JSON.parse`，而是以下状态机：

- `thread.started` -> 记录 thread id
- `turn.started` -> 清空 `pendingMsgs` 与 context usage
- `item.started` -> 工具开始前先 `flushPendingAsThinking()`
- `item.completed` -> 把 reasoning / agent message / tool result 分流
- `turn.completed` -> `flushPendingAsText()`，再发 `EventResult{Done: true}`

这里的关键设计是：

- agent message 不是一收到就直接当最终文本，而是先缓存在 `pendingMsgs`
- 只有在 turn 结束时，或者在 tool 前被明确转成 thinking 时，才决定它们属于哪类 UI 语义

这就是 Codex 路径最容易被低估的地方。

### 2.3 `app_server` path 已经是一套成熟的一等实现

`cc-connect/agent/codex/appserver_session.go` 不是临时兼容层，而是完整 session 内核。它处理了：

- `turn/start`
- `item/started`
- `item/completed`
- `turn/completed`
- `account/rateLimits/updated`
- `thread/tokenUsage/updated`
- runtime usage cache
- context usage cache

它同样把原始通知统一折叠成：

- `EventThinking`
- `EventText`
- `EventToolUse`
- `EventToolResult`
- `EventResult`
- `EventError`

换句话说：

- `exec` 和 `app_server` 在 `cc-connect` 里虽然底层不同，但最终都统一到同一层 `core.Event` 语义。

### 2.4 Codex 的 usage / context usage 不是附属功能

`appserver_session.go` 还额外实现了：

- `GetUsage(ctx)`
- `GetContextUsage()`

并且通过 app-server notification 实时更新：

- usage snapshot
- 当前 thread token usage

这说明在 `cc-connect` 的真实设计里：

- usage / context usage 是 runtime 内核的一部分，不是上层临时拼装的数据。

## 3. 当前仓库的 Codex 两条路径

### 3.1 `codex-cli.mjs`

当前 `bridge/src/backends/codex-cli.mjs` 的主要特点：

- `createSession()` 只是本地生成 session 记录；
- `resumeSession()` 只是把 `threadId = sessionId` 填回内存；
- `sendMessage()` 直接 `codex exec`，并把 `item.completed` 的文本直接发 `text_delta`；
- `listSessions()` / `getSessionMessages()` 明确声明不支持；
- 缺少 thinking / pendingMsgs / tool result 的成熟收口。

它的问题不是“功能少”，而是：

- **没有把 `cc-connect` 已经验证过的 turn 状态机带过来。**

### 3.2 `codex-appserver.mjs`

当前 `bridge/src/backends/codex-appserver.mjs` 比 CLI path 更完整，已经支持：

- `thread/list`
- `thread/read`
- `thread/start`
- `thread/resume`
- `turn/start`
- `turn/interrupt`
- item started / completed / delta

而且它也能输出：

- `text_delta`
- `text_finished`
- `reasoning_delta`
- `reasoning_finished`
- `tool_started`
- `tool_finished`
- `tool_output_delta`

但它依然有一个根本问题：

- 还是直接从 app-server notification 映射到 wire event，**没有经过统一中间事件层**。

### 3.3 `codex-appserver.mjs` 当前仍缺的关键语义

相比 `cc-connect/appserver_session.go`，当前实现仍缺少：

- `pendingMsgs -> thinking / text` 的共享缓冲语义
- usage cache
- context usage cache
- 统一的 tool use / tool result 折叠层
- 可复用的 runtime transcript 回放入口

## 4. 与 `cc-connect` 的核心偏差

| 维度 | `cc-connect exec` | `cc-connect app_server` | 当前 `codex-cli.mjs` | 当前 `codex-appserver.mjs` | 结论 |
|---|---|---|---|---|---|
| thread / turn 生命周期 | 完整 | 完整 | 极简 | 部分有 | 需要共享 turn reducer |
| pendingMsgs | 有 | 有 | 无 | 无 | 这是 thinking / text 正确分流的关键 |
| 工具开始前 flush thinking | 有 | 有 | 无 | 无 | 否则工具前文本语义会漂移 |
| tool result | 完整 | 完整 | 非常弱 | 有 | 仍需统一到内部事件层 |
| usage | 依赖 runtime config / provider | 有 | 无 | 无 | iOS 要保留 usage 时必须补上 |
| context usage | 有 runtime config 读取 | 有 | 无 | 无 | 当前 bridge 缺这一层 |

## 5. Codex 路径必须冻结的结论

### 5.1 结论一：Codex 是最适合先收口到内部标准化事件层的 backend

原因：

- `cc-connect` 已经同时覆盖 `exec` 和 `app_server` 两条 runtime；
- `appserver_session.go` 已经处理了 usage / context usage；
- 当前 bridge 也已经在使用 app-server，不需要先改产品面。

### 5.2 结论二：Codex 的真正真相层是 `pendingMsgs + turn reducer`

不要被“它只是 JSON line 事件”误导。

Codex 语义稳定的关键其实是：

- 什么时候缓存 agent message
- 什么时候把缓存内容当 thinking 发出去
- 什么时候 turn 完成后再变成最终 text

如果继续跳过这层，未来一定会在以下地方反复踩坑：

- 工具前的文本显示
- thinking 和 final text 混淆
- turn 完成时遗漏最后一段文本

### 5.3 结论三：`app_server` 路径后续应优先直接对齐 `cc-connect/agent/codex/appserver_session.go`

后续如果要优先实现一条稳定路径，应首选：

- Codex app-server

理由：

- 当前产品已依赖 app-server；
- `cc-connect` 已有成熟实现；
- 它覆盖的信息面最全。

### 5.4 结论四：`codex-cli.mjs` 不应继续作为语义样板

当前 CLI driver 只适合当：

- 命令面探针
- 临时 fallback

不适合当：

- 新一轮 unified runtime 语义的样板代码

真正值得对齐的是：

- `cc-connect/agent/codex/session.go`

## 6. Codex 后续实现前必须补的 fixture

至少需要以下回放样本：

- `exec` path：`turn.started -> agent_message -> tool -> turn.completed`
- `exec` path：仅 reasoning，无 tool
- `exec` path：tool result failed，带 exit code
- `app_server` path：`item/started(commandExecution)` 与 `item/completed(commandExecution)`
- `app_server` path：`thread/tokenUsage/updated`
- `app_server` path：`account/rateLimits/updated`
- `app_server` path：中途 `error`

这些 fixture 缺一不可，因为它们共同定义了 Codex 的真实状态机边界。

## 7. 本文件的直接实施价值

本文件最终给出的行动建议很明确：

- 如果下一阶段先动 Codex，就从 `cc-connect/agent/codex/appserver_session.go` 开始；
- 不要再把 `codex-cli.mjs` 当成未来统一 runtime 的样板；
- Codex 是最适合先落地 `NormalizedRuntimeEvent` 的 backend。
