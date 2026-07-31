# cc-connect Runtime 源码走读总纲

> 日期：2026-04-30
> 目标：在不接入 `cc-connect` 公共 Bridge / Management API 的前提下，系统梳理哪些 `cc-connect` 内核实现必须复用，哪些层必须继续由本项目自持。

## 1. 本轮硬约束

以下约束已明确，不再讨论是否放宽：

- iOS 端必须保留当前强类型原生 UI：思考块、工具步骤、usage、权限流。
- iOS 端必须保留“动态发现 / 动态创建项目目录”的现有体验。
- iOS 端必须继续以 backend 为主视角，不能改成 `project`-first。
- 不能依赖 `cc-connect` 当前仍处于 draft / beta 的公共 Bridge / Management API 作为 iOS 主协议。

这四条约束直接决定：

- 不能把 iOS 直接改造成 `cc-connect` 客户端。
- 不能让 iOS 去消费 `reply` / `reply_stream` / `card` / `buttons` 这类适配器协议。
- 不能把 `cc-connect` 的 `project` 模型硬塞进当前产品。

## 2. 最终判断

本轮源码走读后的结论是：

1. **不复用 `cc-connect` 的公共 Bridge / Management API。**
2. **不复用 `cc-connect` 的 `project`-first 产品模型。**
3. **必须复用或对齐 `cc-connect` 的 runtime / session / event 内核。**
4. **Unified Bridge 后续实现前，必须先冻结一层内部标准化事件模型。**
5. **任何 backend 都不能再直接从原始 runtime 事件发射当前 iOS wire event。**

这意味着：

- 外层仍然是本项目自己的 `UnifiedBridgeAdapter <-> UnifiedBridgeServer` 协议。
- 中层要新增一层内部统一的 `NormalizedRuntimeEvent`。
- 内层 backend runtime 解析与状态机应尽量直接借鉴 `cc-connect`。

## 3. 走读范围

本轮实际对照了以下代码层：

- `cc-connect/core/message.go`
- `cc-connect/core/interfaces.go`
- `cc-connect/core/engine.go`
- `cc-connect/core/streaming.go`
- `cc-connect/core/bridge.go`
- `cc-connect/agent/opencode/opencode.go`
- `cc-connect/agent/opencode/session.go`
- `cc-connect/agent/codex/codex.go`
- `cc-connect/agent/codex/session.go`
- `cc-connect/agent/codex/appserver_session.go`
- `cc-connect/agent/claudecode/claudecode.go`
- `cc-connect/agent/claudecode/session.go`
- `bridge/src/backends/opencode-http.mjs`
- `bridge/src/backends/opencode-cli.mjs`
- `bridge/src/backends/codex-appserver.mjs`
- `bridge/src/backends/codex-cli.mjs`
- `bridge/src/backends/claudecode.mjs`
- `docs/unified-bridge-protocol.md`

## 4. 需要区分的五层模型

后续讨论时必须把 `cc-connect` 拆成五层看，不能再把“借鉴 `cc-connect`”混成一句空话。

| 层次 | 代表文件 | 作用 | 本项目是否复用 |
|---|---|---|---|
| L1 原始 runtime 输出 | 各 CLI / SDK / app-server 原始 JSON / NDJSON / stream-json | backend 自己的非稳定输出 | 只读，不直接暴露给 iOS |
| L2 backend session 解析器 | `agent/*/session.go` | 把原始输出折叠成稳定的中间事件和会话状态机 | **必须复用 / 对齐** |
| L3 统一中间事件模型 | `core/message.go` 的 `core.Event` | `text / thinking / tool_use / tool_result / permission_request / result / error` | **必须复用其语义** |
| L4 上层收口逻辑 | `core/engine.go`、`core/streaming.go` | preview freeze / finish、权限等待、session 持久化、usage/context usage 收口 | **必须复用关键策略** |
| L5 公共对外 API | `core/bridge.go`、`bridge-protocol`、`management-api` | 聊天平台适配器协议、project 管理 API | **不复用为 iOS 主协议** |

过去几轮 Unified Bridge 最大的问题，是主要借了 L5 和少量 L1 表象，没有真正把 L2-L4 当成真相源。

## 5. 本轮最关键的源码发现

### 5.1 `cc-connect` 真正稳定的层不是它的公共 Bridge 协议

`cc-connect` 对外 Bridge 协议更接近“适配器协议”，核心消息是：

- `reply`
- `reply_stream`
- `card`
- `buttons`
- `typing`

它并不是给“强类型原生编码客户端”设计的协议。

真正成熟、值得借的是：

- backend session 状态机
- `core.Event` 统一语义
- `engine.go` 里的事件消费策略
- `streaming.go` 里的 preview / freeze / finish 规则

### 5.2 当前 bridge 的问题不是 patch 不够，而是少了一层统一的中间事件模型

当前 bridge 存在两个问题：

- backend driver 直接从 runtime 原始事件映射到 wire event；
- 各 backend 各自实现 streaming / thinking / tool / permission 的语义边界。

结果是：

- OpenCode 的 SSE 需要自己发明 snapshot / delta 收口；
- Codex 的 CLI / app-server 语义被分裂成两套；
- Claude Code 虽然相对成熟，但 backend 内部仍直接产出 wire event，而不是先走一层统一中间事件。

### 5.3 `docs/unified-bridge-protocol.md` 已经和实际实现产生漂移

协议文档当前列出的事件只有：

- `text_delta`
- `text_finished`
- `reasoning_delta`
- `reasoning_finished`
- `tool_started`
- `tool_finished`

但实际代码已经存在并依赖：

- `text_updated`
- `reasoning_updated`

例如：

- `bridge/src/backends/opencode-http.mjs` 会发 `text_updated` 与 `reasoning_updated`
- `OpenCodeiOS/.../UnifiedBridgeAdapter.swift` 已映射这两个事件

这说明下一轮实现前必须先冻结内部规范，再回写协议文档，不能继续让代码和文档各自演化。

## 6. 后续实现前必须冻结的规则

### 6.1 规则一：backend parser 只允许产出内部标准化事件

禁止再出现：

- OpenCode parser 直接发 `text_updated`
- Codex parser 直接发 `tool_started`
- Claude parser 直接发 `reasoning_delta`

正确边界应该是：

- backend parser -> `NormalizedRuntimeEvent`
- shared reducer / adapter -> Unified Bridge wire event

### 6.2 规则二：streaming 正确性必须由共享 reducer 负责

OpenCode 之前踩坑的根因之一，就是 text snapshot / delta contract 既在 backend 里处理，又在 iOS 里兜底。

后续必须保证：

- 文本累积逻辑只有一份共享实现；
- “更短旧快照回退保护”“delta/snapshot 幂等保护”只有一份共享实现；
- tool / permission / thinking 对文本 preview 的 freeze / detach / finish 只有一份共享实现。

### 6.3 规则三：权限请求先是内部 `permission_request`，再映射成 UI step

`cc-connect` 的真实内核语义是 `EventPermissionRequest`，不是“直接发一个 pending tool step”。

后续应先保留：

- `requestId`
- `toolName`
- `inputSummary`
- `inputRaw`
- `questions`

再由共享 adapter 映射成当前 iOS 需要的 `tool_started(status: pending, options: ...)`。

### 6.4 规则四：session ID 解析必须是一等事件

多个 backend 的真实 session / thread id 都可能在运行中晚到：

- OpenCode CLI：`step_start` 才拿到 `sessionID`
- Claude Code：`system` 事件里才拿到 `session_id`
- Codex：`thread.started` 才拿到 `thread_id`

这意味着 session rebind 不能继续散落在各 backend / iOS 里处理，而应该进入共享中间层。

### 6.5 规则五：usage / context usage 不能继续当附属功能

如果 iOS 必须保留 usage UI，那么：

- `UsageReport`
- `ContextUsage`
- final result token usage

都必须进入内部标准化事件层，而不是只在 Claude path 上临时拼一个 `getUsage()`。

## 7. 推荐阅读顺序

后续任何实现前，建议强制按下面顺序阅读：

1. `cc-connect/core/message.go`
2. `cc-connect/core/interfaces.go`
3. `cc-connect/core/streaming.go`
4. `cc-connect/core/engine.go`
5. `cc-connect/agent/codex/appserver_session.go`
6. `cc-connect/agent/opencode/session.go`
7. `cc-connect/agent/codex/session.go`
8. `cc-connect/agent/claudecode/session.go`
9. 当前仓库各 backend driver

原因：

- 先看 `core.Event`，才能知道 backend parser 应该收口到哪里；
- 先看 `streaming.go` 和 `engine.go`，才能知道为什么 `full_text` / `freeze` / `permission wait` 成熟；
- 最后再看当前 bridge，才看得出哪些地方是“重复发明”。

## 8. 推荐实现顺序

### Phase 0

- 冻结 `NormalizedRuntimeEvent` 文档
- 冻结 transcript 回放测试策略
- 修正 `docs/unified-bridge-protocol.md` 与实际代码的漂移

### Phase 1

- 先对齐 Codex app-server 内核

原因：

- `cc-connect` 已经把 `appserver_session.go` 做成一等实现；
- 它同时覆盖了 thinking / text / tool / usage / context usage；
- 当前 bridge 也已经在使用 app-server，不需要先改产品面。

### Phase 2

- 对齐 OpenCode runtime / session 内核

原因：

- 当前最痛的 streaming 正确性问题集中在 OpenCode；
- `opencode-session.go` 提供了 CLI 语义真相；
- 当前 `opencode-http.mjs` 里的 snapshot contract 也需要被统一 reducer 吸收，而不是继续独立演化。

### Phase 3

- 对齐 Claude Code 内核

原因：

- 当前 Claude path 已经最接近目标；
- 它更适合作为统一事件层完成后的第三个收口对象，而不是第一个试验场。

## 9. 本轮文档清单

本轮共生成 6 份文档：

- `2026-04-30-cc-connect-runtime-源码走读总纲.md`
- `2026-04-30-OpenCode-runtime-session-源码走读.md`
- `2026-04-30-Codex-runtime-session-源码走读.md`
- `2026-04-30-Claude-Code-runtime-session-源码走读.md`
- `2026-04-30-Unified-Bridge-内部事件规范与复用边界.md`
- `2026-04-30-Runtime-Transcript-回放测试策略.md`

后续任何实现工作都应把这 6 份文档视为前置阅读材料，而不是事后复盘材料。
