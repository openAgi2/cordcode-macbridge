# Claude Code Runtime / Session 源码走读

> 日期：2026-04-30
> 范围：`cc-connect` 的 Claude Code CLI 内核，以及当前仓库 `claudecode.mjs` 的对应实现。

## 1. 走读文件

- `cc-connect/agent/claudecode/claudecode.go`
- `cc-connect/agent/claudecode/session.go`
- `bridge/src/backends/claudecode.mjs`

## 2. `cc-connect` 的 Claude Code 真相层

### 2.1 Agent 层已经把运行参数边界划得很清楚

`cc-connect/agent/claudecode/claudecode.go` 负责的是：

- `work_dir`
- `cli_path`
- `model`
- `reasoning_effort`
- `permission mode`
- `allowed_tools` / `disallowed_tools`
- provider proxy / router env
- `run_as_user` 隔离

这层的价值在于：

- Claude 的运行环境、provider env、spawn 隔离已经被系统化整理过；
- 后续 bridge 若想继续支持高级模式，不应再自己零碎拼 env。

### 2.2 Session 层是真正值得借的部分

`cc-connect/agent/claudecode/session.go` 做的是一套长期存活的 stream-json session：

- 进程常驻；
- stdin 写入用户消息；
- stdout 逐行解析 stream-json；
- `control_request` 走权限回调；
- `result` 事件携带最终文本和 token usage；
- `system` 事件里可能首次给出 session id。

### 2.3 原始内容块到统一事件的映射

`session.go` 把 assistant 内容块统一成：

| Claude 原始块 | `cc-connect` 映射 |
|---|---|
| `text` | `EventText` |
| `thinking` | `EventThinking` |
| `tool_use` | `EventToolUse` |
| `result` | `EventResult` |
| `control_request(can_use_tool)` | `EventPermissionRequest` |

这再次证明：

- `cc-connect` 的关键不是外层公共 Bridge 协议；
- 而是先收口到统一 `core.Event`。

### 2.4 session id 不是创建时就稳定的

`handleSystem()` 遇到 `session_id` 时会发一个带 `SessionID` 的 `EventText` 空事件。

这说明：

- session id 绑定是运行中的一等语义；
- 不能假设 `createSession()` 的返回值已经足够当作最终 runtime session id。

### 2.5 权限请求是真正的一等内部事件

`handleControlRequest()` 遇到 `can_use_tool` 时，会发：

- `EventPermissionRequest`

其数据包含：

- `RequestID`
- `ToolName`
- `ToolInput`
- `ToolInputRaw`
- `Questions`

这里的设计对后续 Unified Bridge 很关键：

- `cc-connect` 内核的权限事件不是 pending tool step，而是专门的 permission event。

### 2.6 usage 不是后补字段，而是 result 原生携带

`handleResult()` 会直接从 raw `usage` 中提取：

- `input_tokens`
- `output_tokens`

并写入 `EventResult`。

这说明后续如果 iOS 要保留 usage UI，Claude path 应把 token usage 放进统一中间层，而不是仅靠 `getUsage()` 临时查询。

## 3. 当前仓库 `claudecode.mjs` 的状态

### 3.1 当前实现已经是最接近目标的一条 path

`bridge/src/backends/claudecode.mjs` 已经具备：

- `reasoning_delta` / `text_delta`
- `reasoning_finished`
- `tool_started` / `tool_finished`
- `text_finished`
- `session_state_changed`
- `resolvePermission()`

相比当前 OpenCode / Codex path，Claude path 更接近成熟态。

### 3.2 但它仍然存在“直接生成 wire event”的结构问题

当前实现最大的结构性问题不是缺功能，而是：

- backend 内部直接发 `reasoning_delta`
- backend 内部直接发 `text_delta`
- backend 内部直接发 `tool_started`
- backend 内部直接发 `tool_finished`

换句话说，当前 Claude backend 也没有先经过共享的内部 `NormalizedRuntimeEvent`。

### 3.3 权限路径存在一个明显的 contract 味道问题

`_createPermissionHandler()` 当前会直接 `_emitUnified(null, 'tool_started', ...)`。

这说明：

- backend 在权限阶段就已经开始生成 iOS 专用的 pending tool step；
- 并且还带有 `sessionId = null` 的临时性做法。

这在功能上也许能跑，但在架构上说明：

- 当前 backend 把“内部权限事件”和“外部 UI step”混在一起了。

### 3.4 usage 仍然只停留在 probe diagnostics

当前 `getUsage()` 主要来自：

- `_sdk.probeDiagnostics.usage`

但它没有系统接入：

- 运行中 session 的 context usage
- 每轮 `result` 自带的 token 使用
- 更细粒度的 per-session usage 累积

这与 iOS “usage 必须保留”的目标还不完全一致。

## 4. 与 `cc-connect` 的核心偏差

| 维度 | `cc-connect` Claude session | 当前 `claudecode.mjs` | 结论 |
|---|---|---|---|
| 中间事件层 | `EventText / EventThinking / EventToolUse / EventPermissionRequest / EventResult` | 直接产出 wire event | 必须补共享中间层 |
| session id | system 事件单独绑定 | backend 内部自行维护 temp / real id | 应由共享 session rebind 统一处理 |
| 权限事件 | 一等 `EventPermissionRequest` | 直接转成 pending tool step | 先保留 permission 内核，再映射 UI |
| token usage | `EventResult` 原生携带 | 部分来自 probe diagnostics | usage 仍需统一收口 |
| 工具结果 | 原始 tool_use 与 result 分开 | 最终 assistant message 中再合成 `tool_started/tool_finished` | 合成位置应上移到共享 reducer |

## 5. Claude 路径必须冻结的结论

### 5.1 结论一：Claude 不是第一个试验场，而是第三阶段收口对象

原因不是它不重要，而是：

- 当前 Claude path 已经是最接近目标的；
- 它适合在 OpenCode / Codex 把共享中间事件层打通后，再回头接入。

### 5.2 结论二：当前 Claude path 可以作为共享 reducer 的样板，而不是最终结构

它已经证明当前 iOS 强类型 UI 所需的大部分信息都能拿到，但仍有两点不能继续保留：

- backend 直接输出 wire event
- permission_request 直接伪装成 pending tool step

### 5.3 结论三：Claude token usage 应进入统一 result / usage 模型

后续统一中间层里，Claude 至少应提供：

- `result.inputTokens`
- `result.outputTokens`
- 可选 `context_usage`

而不是继续只依赖 `getUsage()` 的补充查询。

## 6. Claude 后续实现前必须补的 fixture

至少需要以下回放样本：

- `system(session_id)` 晚到样本
- `assistant(text + thinking + tool_use)` 样本
- `result(带 usage)` 样本
- `control_request(can_use_tool)` 样本
- `AskUserQuestion` 样本
- 多次 `assistant` 最终事件（先空内容、后完整内容）样本

其中最后一条非常重要，因为当前 `claudecode.mjs` 已经在用 `_finishedItemIds` 对抗重复 `assistant` final。

## 7. 本文件的直接实施价值

本文件最终要解决的问题只有一个：

> Claude 这条路径后续应该怎么改，才能不推翻现在已经比较稳定的实现？

答案是：

- 不推翻现有产品能力；
- 但必须把 backend 直接生成 wire event 的结构，替换成“先产出内部标准化事件，再统一映射”的结构。
