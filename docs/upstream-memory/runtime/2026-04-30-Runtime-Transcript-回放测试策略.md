# Runtime Transcript 回放测试策略

> 日期：2026-04-30
> 目的：把后续 runtime / session 重构的验证方式从“真机踩坑后再回头看 `cc-connect` 源码”改成“先用真实 transcript 回放证明 parser / reducer 正确”。

## 1. 为什么必须单独写这份文档

仅靠源码走读还不够。

如果没有回放测试，后续仍然会重演下面的过程：

1. 先改某个 backend driver
2. 本地单测通过
3. 真机出现 streaming / tool / permission 错乱
4. 再回去对照 `cc-connect`

本文件的目标就是打断这条路径。

## 2. 测试策略总原则

### 2.1 先固化原始 transcript，再实现 parser

每个 backend 都要先拿到真实原始输出：

- OpenCode：NDJSON / SSE 原始行
- Codex exec：CLI JSON lines
- Codex app-server：JSON-RPC notification 序列
- Claude Code：stream-json 行序列

后续 parser 只能对着这些 transcript 开发，不能一边写 parser 一边用手写假样本猜语义。

### 2.2 分三层断言

每个 transcript 至少要覆盖三层断言：

1. 原始 transcript -> `NormalizedRuntimeEvent`
2. `NormalizedRuntimeEvent` -> Unified Bridge wire event
3. wire event -> iOS 关键状态断言

### 2.3 共享 reducer 的回放测试必须独立于 backend

共享 reducer 的测试不应绑定某个 backend 名称。

它应该只关心：

- 一串 `NormalizedRuntimeEvent` 进来后
- 最终应该产出怎样的 `text_* / reasoning_* / tool_* / session_state_changed / usage_reported`

## 3. 建议收集的 transcript 类型

### 3.1 OpenCode

必须至少包含：

- 纯文本流式输出
- 文本 + reasoning 混合
- tool_use completed 单事件
- `step_start` 才出现 session id
- `message.part.delta + message.part.updated` 混合
- 旧 snapshot 晚到
- 多 part 聚合

### 3.2 Codex exec

必须至少包含：

- `turn.started -> agent_message -> turn.completed`
- `turn.started -> reasoning -> tool -> agent_message -> turn.completed`
- tool failed 带 exit code
- `thread.started` 晚到 thread id

### 3.3 Codex app-server

必须至少包含：

- `item/started(commandExecution)` / `item/completed(commandExecution)`
- `item/started(webSearch)` / `item/completed(webSearch)`
- `thread/tokenUsage/updated`
- `account/rateLimits/updated`
- `turn/completed`
- 中途 `error`

### 3.4 Claude Code

必须至少包含：

- `system(session_id)`
- `assistant(text)`
- `assistant(thinking)`
- `assistant(tool_use)`
- `result(带 usage)`
- `control_request(can_use_tool)`
- `AskUserQuestion`
- 多次 `assistant` 最终事件，前一次为空或仅有 thinking

## 4. 必须固定的回放断言

### 4.1 文本类断言

- 不得重复 append 相同内容
- 旧 snapshot 不得回退正文
- final text 必须和 transcript 真正完成态一致
- thinking 更新不得擦除正文

### 4.2 工具类断言

- 每个工具步骤必须只有一个稳定的 step id
- `tool_use` 与 `tool_result` 需要正确配对
- 失败结果必须保留 `status / exitCode / success`
- file change / todo item 等结构化字段不能在 adapter 层丢失

### 4.3 权限类断言

- permission request 必须先变成内部 permission event
- 再映射成 pending tool step
- timeout / resolve / cancel 路径都必须有 transcript 或手写中间事件回放测试

### 4.4 会话类断言

- session id / thread id 晚到时必须正确 rebind
- pending session 不能重复创建第二条会话
- turn complete 后必须进入 idle

### 4.5 usage 类断言

- final result token usage 不得丢失
- Codex app-server 的 account usage / context usage 更新不得丢失
- usage 为全局快照还是 session 增量，必须在断言里写死

## 5. 建议的 fixture 组织方式

建议未来代码实现时，把 transcript fixture 固定在类似结构：

```text
bridge/testdata/runtime/
  opencode/
    cli/
    http/
  codex/
    exec/
    appserver/
  claude/
```

每个样本至少包含：

- `raw.*`：原始 transcript
- `normalized.json`：期望 `NormalizedRuntimeEvent` 序列
- `wire.json`：期望 Unified Bridge wire event 序列
- `notes.md`：样本来源、关键断言、对应历史 bug

## 6. 必须优先补的回放样本

### 6.1 OpenCode streaming 重灾样本

这是第一优先级，因为它已经造成过多轮返工。

至少要补：

- 重复 delta 污染样本
- snapshot 晚到回退样本
- 多 truth source 混合样本

### 6.2 Codex pendingMsgs 分流样本

这是第二优先级，因为它决定 thinking / final text 的正确边界。

### 6.3 Claude permission / usage 样本

这是第三优先级，因为它决定当前 iOS 强类型 UI 能否保留。

## 7. 以后如何使用这些 transcript

后续任何 runtime 改动都应经过下面流程：

1. 新增或更新 transcript fixture
2. parser 回放通过
3. reducer 回放通过
4. 关键 iOS 定向测试通过
5. 最后才上真机 smoke

顺序不能再反过来。

## 8. 真机验证在这套策略中的角色

真机验证仍然重要，但角色要变：

- 真机负责验证最终产品体验
- transcript 回放负责验证 runtime 语义正确性

不能再让真机承担“发现 parser 状态机 bug”的职责。

## 9. 本文件的结论

后续如果没有 transcript 回放测试，任何“继续向 `cc-connect` runtime 内核靠拢”的实现都不应该开始。

原因不是测试洁癖，而是你们已经用真实成本证明过：

- 没有 transcript 回放，最终就一定会在 streaming / reasoning / permission / tool 边界上把 `cc-connect` 已经踩过的坑再踩一遍。
