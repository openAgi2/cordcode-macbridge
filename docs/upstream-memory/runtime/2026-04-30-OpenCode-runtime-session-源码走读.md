# OpenCode Runtime / Session 源码走读

> 日期：2026-04-30
> 范围：`cc-connect` 的 OpenCode CLI 内核，以及当前仓库 `opencode-http.mjs` / `opencode-cli.mjs` 的偏差。

## 1. 走读文件

本文件基于以下源码整理：

- `cc-connect/agent/opencode/opencode.go`
- `cc-connect/agent/opencode/session.go`
- `bridge/src/backends/opencode-cli.mjs`
- `bridge/src/backends/opencode-http.mjs`

## 2. `cc-connect` 的 OpenCode 真相层

### 2.1 Agent 层负责 provider / model / env，而不是 session 事件

`cc-connect/agent/opencode/opencode.go` 主要负责：

- `work_dir` / `model` / `mode` / `cmd` 配置；
- provider env 合并；
- model cache 持久化；
- 可用模型发现。

这层说明一个重要事实：

- **session 行为真相不在 agent.go，而在 session.go。**
- 后续如果只参考 `opencode models` 或 `opencode session list` 之类命令面，是不够的。

### 2.2 Session 生命周期

`cc-connect/agent/opencode/session.go` 的核心行为：

- 每次 `Send()` 都启动一次 `opencode run --format json` 子进程；
- 如果已有 session id，则通过 `--session <id>` 续接；
- 文件会先落盘，再把路径附加到 prompt；
- 图片会先写入 `.cc-connect/images`，再通过 `--file` 传给 CLI；
- stdout 按 NDJSON 逐行解析；
- 进程结束后统一发出 `EventResult{Done: true}`。

这层给出的设计结论是：

- OpenCode 的“完整一轮回答结束”并不是某个单独的 `text` 事件，而是**子进程生命周期 + stdout 读完**共同定义的。

### 2.3 原始事件到统一事件的映射

`session.go` 明确把 OpenCode NDJSON 事件收口成 `core.Event`：

| OpenCode 原始事件 | `cc-connect` 映射 | 说明 |
|---|---|---|
| `text` | `EventText` | 文本 chunk |
| `reasoning` | `EventThinking` | thinking chunk |
| `tool_use` | `EventToolUse`，必要时再补 `EventToolResult` | OpenCode 某些 tool event 会把 call + result 打在一个事件里 |
| `step_start` | 更新 session id | 不直接当文本事件 |
| `step_finish` | 仅更新内部状态 / 日志 | 不直接发最终文本 |
| `error` | `EventError` | 明确终止 |
| 读循环结束 | `EventResult{Done: true}` | 统一“本轮结束”语义 |

这里最值得借鉴的不是某一条映射，而是：

- **先统一到 `EventText / EventThinking / EventToolUse / EventToolResult / EventResult`，再交给上层消费。**

### 2.4 session id 的真实来源

`session.go` 不是在 `createSession()` 时就拿到最终 session id，而是在：

- `handleStepStart()` 中，从 `part.sessionID` 更新 `chatID`

这意味着：

- OpenCode 的 session id 是运行时晚到信息；
- 后续 bridge 不应该假设 `sendMessage()` 之前 session id 已经稳定。

### 2.5 权限与 usage

`cc-connect` 的 OpenCode CLI path 明确有两个边界：

- `RespondPermission()` 是 no-op，说明 CLI path 下权限由 OpenCode 自己处理；
- 没有专用 usage reporter。

这两个事实都要原样保留，不要在 bridge 层再伪造一套不存在的 CLI 权限或 usage API。

## 3. 当前仓库的 OpenCode 两条路径

### 3.1 `opencode-cli.mjs`

当前 `bridge/src/backends/opencode-cli.mjs` 已具备：

- `session list`
- `export`
- `models`
- `agent list`
- `run --format json`

但它的 `sendMessage()` 事件解析非常薄，只处理了：

- `step_start`
- `text`
- `step_finish`
- `error`

它**没有**真正处理：

- reasoning
- tool use
- tool result
- result done 之前的统一状态机

这意味着当前 CLI driver 只借到了命令面，没有借到 `cc-connect` 的 session 内核。

### 3.2 `opencode-http.mjs`

当前 `bridge/src/backends/opencode-http.mjs` 反而已经在本地长出了更复杂的逻辑：

- `message.part.delta` / `message.part.updated` / `message.updated` 多源归一化；
- `messageTextByKey` / `partOrderByMessageKey` 文本聚合；
- `text_updated` / `reasoning_updated` snapshot 收口；
- tool step、permission、todo、session state 映射。

换句话说：

- OpenCode HTTP path 已经自己重写出了一部分“中间语义层”；
- 这也是为什么 OpenCode streaming 最后修好时，本质上是在**补一个缺失的中间层**。

## 4. 与 `cc-connect` 的核心偏差

| 维度 | `cc-connect` OpenCode CLI | 当前 `opencode-cli.mjs` | 当前 `opencode-http.mjs` | 结论 |
|---|---|---|---|---|
| 文本 chunk | `EventText` | `text_delta` | `text_updated` / `text_finished` | 不应直接在 backend 里决定对外 delta / snapshot |
| reasoning | `EventThinking` | 缺失 | `reasoning_delta` / `reasoning_updated` / `reasoning_finished` | reasoning 必须进入统一中间层 |
| tool use / result | `EventToolUse` + `EventToolResult` | 缺失 | 仅 HTTP path 有部分 step 语义 | CLI path 明显语义不足 |
| result 完成 | 读循环结束后 `EventResult` | 直接拼 `finalText` 发 `text_finished` | message completed 时发 `text_finished` | “本轮完成”必须统一抽象 |
| session id 绑定 | `step_start` 更新 session id | 临时捕获 | HTTP path 依赖多处 props | session rebind 应抽到共享层 |
| 权限 | CLI no-op | 无 | HTTP path 有 permission event | runtime mode 差异必须 capability 化 |

## 5. OpenCode 路径必须冻结的结论

### 5.1 结论一：不要再让 OpenCode backend 直接生成当前 iOS wire event

后续无论是 CLI 还是 HTTP path，都应该先产出内部 `NormalizedRuntimeEvent`，例如：

- `thinking`
- `text`
- `tool_use`
- `tool_result`
- `result`
- `error`
- `session_identified`

然后再由共享 reducer 决定是否对外发：

- `text_delta`
- `text_updated`
- `text_finished`
- `reasoning_delta`
- `reasoning_updated`
- `reasoning_finished`

### 5.2 结论二：CLI path 的真相应以 `cc-connect/agent/opencode/session.go` 为准

如果后续还要保留 OpenCode CLI path，必须至少补齐 `session.go` 已有的语义：

- reasoning
- tool use
- tool result
- step_start session id 绑定
- result done

否则 CLI path 继续保留只是“命令能跑”，不是“语义可用”。

### 5.3 结论三：HTTP path 的 snapshot contract 不能再是 OpenCode 独有逻辑

当前 `opencode-http.mjs` 的 snapshot contract 已经被验证是有效的，但它不应该继续停留为：

- OpenCode backend 独有的特殊补丁

而应该被抽象成：

- 共享的文本聚合与 regression guard 能力

这样后续其他 backend 一旦也需要 snapshot / delta 幂等处理，就不需要重新发明一次。

### 5.4 结论四：OpenCode CLI 与 HTTP path 不应共享“对外事件生成逻辑”，只应共享“内部标准化事件”

OpenCode 未来即使继续存在 CLI / HTTP 两种 runtime mode，也只能共享到这里：

- 统一 `NormalizedRuntimeEvent`

不能共享到这里：

- 直接对外发 `text_delta` / `tool_started` / `reasoning_finished`

因为那会再次把 runtime 特性泄漏到 iOS 协议层。

## 6. OpenCode 后续实现前必须补的测试资产

至少需要准备以下 transcript / fixture：

- OpenCode CLI `text -> reasoning -> tool_use -> text -> done` 样本
- OpenCode CLI `tool_use(status=completed)` 单事件样本
- OpenCode CLI `step_start` 晚到 session id 样本
- OpenCode HTTP `message.part.delta + message.part.updated` 混合样本
- OpenCode HTTP 旧快照回退样本
- OpenCode HTTP 多 part 聚合样本

没有这些 fixture，就不应该再动 OpenCode streaming 路径。

## 7. 本文件的直接实施价值

本文件最终要解决的问题只有一个：

> 后续再动 OpenCode 时，开发者应先去哪里找真相？

答案是：

- CLI runtime 语义先看 `cc-connect/agent/opencode/session.go`
- HTTP/SSE 多源收口先看当前 `opencode-http.mjs`
- 真正的共享目标是内部统一事件层，而不是现有任何一个 backend 的 wire event 输出
