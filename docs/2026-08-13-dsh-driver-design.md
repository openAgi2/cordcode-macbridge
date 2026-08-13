# DeepSeek Harness (DSH) Driver 设计文档

- **日期**：2026-08-13
- **Runtime**：DeepSeek Harness `dsh-jsonrpc-agent`（developer preview，协议 `0.0.1`，`SESSION_FORMAT_VERSION=0`，明示 pre-release 无兼容承诺）
- **协议**：DSH SDK JSON-RPC 2.0（newline-delimited）over stdio —— **不是** ACP
- **入口**：spawn `dsh-jsonrpc-agent <cordis.yml>`（bundled 单文件 exe，或 `node --import tsx .../bin.ts <cordis.yml>`）
- **Gate 0 证据**：见文末 §12（Go 连通性验证：spawn → initialize → session/prompt → 收 `session.event` 流 → shutdown）
- **参照**：`docs/2026-07-12-grok-driver-design.md`（grokbuild 是 ACP 路径；**DSH 不走 ACP，见 §0 选型**）

---

## 0. 协议选型：为什么不用 ACP

DSH 同时实现两条 stdio JSON-RPC 协议。逐行核对源码后的结论是 **只有 SDK 协议满足 rich timeline 需求**，ACP 不可用：

| 维度 | ACP server（`packages/acp/acp/src/index.ts`） | **SDK server（`packages/sdk/server` + `protocol`）✅ 选定** |
|---|---|---|
| 定位 | "Automation-only" | "Drive harness runtime from another process" |
| assistant 文本 | ✅ 仅已提交文本 | ✅ token 级流（`assistant/chunk`）+ 组装（`assistant/message`） |
| thinking/reasoning | ❌ **显式丢弃**（codec 注释 L152-154） | ✅ `assistant/chunk` 的 `reasoning-delta` |
| tool calls / 文件 diff | ❌ 丢弃 | ✅ `tool/call` + `tool/result.meta`（含 fs `FsDiffMeta.diffs`） |
| todos | ❌ 丢弃 | ✅ `todo/write` 整表快照 |
| token usage / 上下文 | ❌ 丢弃 | ✅ `assistant/message.usage` + `request/context` |
| turn 起止 / 运行态 | ❌ 只有 prompt 级 stopReason | ✅ `turn/start`·`turn/end`（6 态 reason）+ `session.status` |
| 取消 turn | ✅ `session/cancel` | ❌ 无 cancel，kill 进程 |
| session 列表 | ❌ fresh-only | ❌ 无 `session/list` |

**关键反直觉点**：grokbuild 走 ACP 是因为 **Grok CLI 的 ACP 自己往 `session/update` 塞了完整 rich events**（`acp_codec.go` 映射 thinking/tool/plan/usage）；而 **DSH 的 ACP server 主动把 rich events 过滤成纯文本**。协议名相同，内容面完全不同。照搬 grokbuild 的 ACP codec 会让 iOS timeline 降级成纯文本。

DSH 的 `SessionEvent` 词汇（`packages/core/session/src/known-event-types.ts`，45 种）与 MacBridge `core.Event` 几乎一一对应 —— 这是选 SDK 协议的根本依据。

---

## 1. Runtime 版本范围

- **状态**：DSH 处于 developer preview。`serverInfo.version` 恒 `0.0.1` 且客户端不校验（`packages/sdk/protocol/README.md` L37）。**无协议版本协商**。
- **检测方式**：spawn 后用 `initialize` 响应的 `serverInfo.name === 'deepseek-harness-sdk-runtime'` 验证是 DSH runtime；版本字段不可靠，不做 semver gate。
- **`SESSION_FORMAT_VERSION = 0`**（`packages/core/session/src/types.ts`）：持久化格式无兼容承诺，破坏性变更会直接拒读旧 log。driver 的历史读取路径必须容忍"未来某天读不了"。
- **forward-compat 策略**：未知 `event.type` 若带 `ignorable: true` 标记可跳过，否则**拒绝重建**（known-event-types.ts L10-18）。driver 应复用该语义：遇未知非 ignorable event，记录并降级，不静默丢弃导致 timeline 错乱。

---

## 2. 进程参数

```
dsh-jsonrpc-agent <path/to/cordis.yml>
```

或 dev/源码模式：
```
node --import tsx <repo>/packages/examples/jsonrpc-demo/src/bin.ts <cordis.yml>
```

**运行时强制要求显式 config**（`$DSH_CORDIS_CONFIG` 或 argv 位置参数；无 config `exit(1)`）。config 即 `cordis.yml`，决定**工具面 = 你能拿到的 timeline 内容面**（bash/fs/subagent/todo 等工具由 config 组合）。

**关键环境变量**（`python/sdk-runtime/README.md`）：

| 变量 | 用途 |
|---|---|
| `DEEPSEEK_API_KEY` | DeepSeek API 凭据 |
| `DEEPSEEK_BASE_URL` | API endpoint（可指向 mock/网关） |
| `DSH_CWD` | agent 工作目录（bash/fs 工具的 cwd） |
| `DSH_SESSION_ROOT` | JSONL session 持久化根目录 |

**stdout 是协议通道**：runtime stdout 只走 JSON-RPC 帧，诊断走 stderr。driver **不得**在子进程环境里挂 stdout logger（会污染协议通道）。

**进程组管理**：复用 grokbuild 的 `prepareCmdForProcessGroup`（`Setpgid`），Close 时用负 PID 信号回收整个进程组（`proc_unix.go`），避免 DSH 的 bash 子进程残留。

---

## 3. 输入输出 schema

### 3.1 传输层

newline-delimited JSON-RPC 2.0 over stdin/stdout（`packages/sdk/protocol/src/transport.ts`）。每行一个紧凑 JSON 帧：`id`+`method` = request，仅 `id` = response，仅 `method` = notification。坏 JSON 行静默忽略。

- **MacBridge → DSH**（stdin）：`initialize`、`session/prompt`、`shutdown`
- **DSH → MacBridge**（stdout）：`initialize` response、`session/prompt` response、`shutdown` response、`session.event` notification、`session.status` notification、`subagent.started/finished` notification

**注意**：SDK 协议 **无 cancel、无 session/close、无 session/list**；server→client request 是死能力（transport 支持但 runtime 从不发）。

### 3.2 初始化握手

1. MacBridge 发 `initialize`（`{cwd, provider, model, maxTokens?}`）
2. DSH 返回 `{serverInfo: {name: 'deepseek-harness-sdk-runtime', version: '0.0.1'}}`
3. **没有 authenticate 步骤**（DSH SDK 协议无 auth 握手；凭据走 `DEEPSEEK_API_KEY` env）
4. `provider: 'deepseek-official'` 且无预注册 adapter 时，runtime 自动挂 `dsh-llm-deepseek`；其它未注册 provider 直接报错（一期绑定 DeepSeek 模型）

握手后即可发 `session/prompt`。**没有显式 session/new**：`session/prompt` 带 `sessionId`，runtime 对未知 sessionId 惰性创建 agent+session（`server.ts` `getOrCreateSession`）。

### 3.3 `session.event` → core.Event 映射

`session.event` notification 的 `params.event` 字段是完整 `SessionEvent` 信封（`{type, seq, time, data, surfaceOp?, sourceEventSeqs?, ignorable?}`）。`seq` 是 session 内单调递增序号 —— **hydrate/live 对齐靠它**（与 MacBridge SSV2 的 `baseRev→syncRev` 同族）。

| DSH `event.type` | core.Event | 映射说明 |
|---|---|---|
| `turn/start`（`{turn}`） | `EventTurnStarted` | turn 编号 |
| `turn/end`（`{turn, reason}`） | 终态 `EventResult` / `EventError` | reason 6 态见 §3.4 |
| `step/start`·`step/end` | （内部 step 边界） | 可选映射为子进度，iOS 默认不展示 step |
| `user/message` | `EventUserMessage` | 含 `source`（direct/synthetic） |
| `assistant/chunk`（`text-delta`） | `EventText`（流式增量） | token 级，比 grok ACP 更细 |
| `assistant/chunk`（`reasoning-delta`） | `EventThinking` | thinking/reasoning 流 |
| `assistant/message`（`{message, usage?}`） | （组装态）+ context usage | `usage` 携带该 step token 计费 |
| `tool/call`（`{callId, name, arguments}`） | `EventToolUse` | `arguments` 是原始 JSON 字符串 |
| `tool/result`（`{message, error?, meta?}`） | `EventToolResult` | `meta` 可能含 fs `FsDiffMeta.diffs`（**注意 iOS 不做消息内 diff，见 §7**） |
| `todo/write`（`{todos[]}`） | todo 更新 | 整表快照，latest-wins |
| `compaction/start`·`end`·`summary`·`prune` | 上下文压缩状态 | 对应 iOS 压缩指示 |
| `approval/asked`·`decided`·`policy` | 权限请求（见 §3.5） | approval 事件驱动，**非 RPC 回调** |
| `goal/change`·`plan/mode`·`schedule/change` | goal/plan/schedule | 控制面 |
| `session/title` | session 标题 | |
| `subagent/descriptor`·`tool-workflow/*` | 子 agent / workflow | |
| `command/run`·`command/done` | bash 命令执行 | |
| `request/header`·`request/context` | （内部） | 不映射为 timeline event |
| `session/end-seed` | （seed 边界标记） | 仅 hydrate/冷启动相关 |
| 未知 type + `ignorable:true` | 跳过 | forward-compat |
| 未知 type 无 ignorable | 记录 + 降级 | 不静默丢，防 timeline 错乱 |

### 3.4 turn 生命周期（双通道）

DSH 用 **两个独立通道** 表达 turn 状态，driver 需协调：

1. **`session.event` 里的 `turn/start` + `turn/end`**：精确的 turn 边界。`turn/end` 的 `reason` 是 `{kind: 'completed'|'aborted'|'blocked'|'error'|'max-tokens'|'interrupted'}`（`types.ts` L155-174 `TurnEndReasonMap`）。
2. **`session.status` notification**（`{sessionId, status: 'running'|'idle'}`）：whole-agent 生命周期转换（跨 turn）。

**turn 结束映射**：
- `completed` → 正常 `EventResult{Done:true}`
- `error` → `EventError{Done:true}`
- `interrupted` / `aborted` → 取消态 `EventResult`
- `max-tokens` → token 限制完成（非错误）
- `blocked` → 阻塞态

**与 grok 的差异**：grok 的 turn 边界靠 `session/prompt` 的 response（stopReason）；DSH 的 `session/prompt` **只回 `{messageId}` 入队回执，不关联 turn 结果**。turn 完成只能靠 `turn/end` event 判定。driver 不得用"prompt response 返回 = turn 完成"的错误推断。

### 3.5 权限审批

DSH 的权限走 `approval/asked` / `approval/decided` 事件（`packages/interaction`）。但 **SDK 协议的 server→client request 是死能力** —— runtime 当前**不会**主动向 client 发权限 RPC。

- **一期现实**：DSH 的 approval 流程在 runtime 内部消化（受 `DSH_PERMISSION_MODE` / cordis.yml 的 approval policy 控制），**不通过协议暴露给 MacBridge**。
- **影响**：iOS 端的工具授权 UX 对 DSH backend 会降级 —— 工具调用直接按 cordis.yml 配置的 policy 执行（`workspace-write` / `danger-full-access`），无法 per-call 在 iPhone 上 allow/deny。
- **未来**：若 DSH 把 approval 暴露成 server→client request（transport 已支持），driver 再实现 `core.ToolAuthorizer` 走 `approval/asked` 事件。

---

## 4. session 生命周期

| 操作 | SDK 方法 | 说明 |
|---|---|---|
| 创建 session | `session/prompt`（带新 sessionId） | 惰性创建，无显式 new |
| 发送消息 | `session/prompt`（`{sessionId, contentBlocks}`） | 返回 `{messageId}` 入队回执 |
| 取消 turn | **无**（kill 进程，见 §5） | |
| 列出 session | **无**（扫 JSONL 持久化，见下） | |
| 关闭 session | **无**（SDK-created agent 活到 process shutdown） | |

**session ID 管理**：
- `session/prompt` 的 `sessionId` 由 MacBridge 指定（driver 生成 UUID 或用 iOS 传入的 id）。
- runtime 对未知 sessionId 惰性创建。**没有 `session/load`/resume**：重连后无法 attach 到旧 runtime 进程的 session（进程已死）。Resume = 重启 runtime + 重新 hydrate（见下）。

**ListSessions（一期：扫 JSONL 持久化）**：
- SDK 协议无 `session/list`。driver 实现 `core.HistoryProvider` / `RichHistoryProvider`，扫 `DSH_SESSION_ROOT` 下的 JSONL 文件。
- **默认物理格式是 zstd 帧 + packed chunk 行**（连续 `assistant/chunk` delta 合并为 `text-chunks`/`reasoning-chunks`/`tool-call-chunks` 行，`packages/core/session/src/chunk-rows.ts`）。Go 读取需 zstd 解码 + packed 行展开。
- **建议**：driver 专用 cordis.yml 显式配 `compression: 'none'` + `packChunks: false`（`examples/jsonrpc-agent/cordis.yml` L44 演示了 `DSH_SNAPSHOT` 切 none），让磁盘产物退化为纯 JSONL，Go 直接按行解析。
- 路径编码（`projectKey(cwd)` / `encodeSegment(id)`）是 DSH 内部约定，需在 driver 复刻或扫描时容忍。
- **一期可降级**：若实现成本高，先不支持"历史会话列表"，只做 live session（打开即新建）。`session_state` capability 仍声明，`session_history` 暂不声明。

**冷启动 hydrate（SSV2 对齐）**：
- DSH 自身有 **session-projection kernel**（`packages/session/session-projection`），哲学与 MacBridge SSV2 Projection Kernel 一致（单源、whole-value event、单调 seq）。
- 按 SSV2 护栏：**iOS timeline 真相永远只认 MacBridge ProjectionStore**。DSH 只是被消费的 event 生产者。driver 把 DSH 的 `session.event` 流映射成 core.Event 喂给 ProjectionStore；**不**让两套 projection 互写。

---

## 5. 取消与关闭

### 取消当前 turn

**SDK 协议无 cancel**。取消 = 终止 runtime 进程 + 重启：
1. driver 收到 iOS cancel → 立即开始三阶段 Close（下）
2. 重启 runtime 进程
3. 新进程 `initialize` + 用原 sessionId 重发？—— **不能**（runtime 进程死了，session 丢失）。实际：cancel 后该 session 在 runtime 侧已不可用，需新建 session（或接受 turn 中断后 session 结束）。

**这是 DSH backend 相对 Claude/Codex 的明确降级点**，需在 iOS UI 诚实表达（中断 = 该 session 结束 / 新开 session）。后续若 DSH 加 cancel 方法（transport 已支持 request），再实现 `core.TurnCanceler`。

### 关闭 session（Close）

三阶段优雅关闭（复用 grokbuild `proc_unix.go` 的进程组模式）：
1. **Phase 1 — stdin close**：发 `shutdown` RPC（flush response 后 runtime 自行 dispose + exit 0），关 stdin，等进程退出（8s 超时）
2. **Phase 2 — SIGTERM**：向进程组发 SIGTERM（5s 超时）
3. **Phase 3 — SIGKILL**：`cancel()` context + `forceKillProcessGroup`

环境变量用 `core.BuildAgentEnv` 过滤 control-plane secret（禁止继承 `CCCODE_*` 等）。

---

## 6. 错误分类

| 错误类型 | 处理 | core.Event |
|---|---|---|
| JSON-RPC error response（`-32601` method not found / `-32603` internal） | 解析 code，映射用户可读消息 | `EventError` |
| stdout JSON 解码失败 | 记录原始行（脱敏），继续读下一行 | `EventError`（可诊断） |
| 进程意外退出（非零 exit） | 发射终态 `EventError`，标记 session not alive | `EventError{Done:true}` |
| `initialize` 超时 / `serverInfo.name` 不符 | driver fail-closed，descriptor `not_detected` | — |
| 未知非 ignorable event | 记录 + 降级，不静默丢 | — |
| zstd/packed JSONL 解析失败（历史读取） | 该 session 历史不可用，诚实标注 | — |

---

## 7. 敏感信息脱敏

- `tool/call` 的 `arguments` / `tool/result` 的 `message`：原始参数和输出**不全文广播给 iOS**，只提取 `title`、`kind`、`status`、`locations`（路径）。
- **iOS 不做消息内文件级 diff（2026-08-01 owner 决策）**：DSH `tool/result.meta` 的 `FsDiffMeta.diffs` **不**映射成消息内 inline diff；文件变更只走 session 级 diff bar（`workspace_diff` capability）。activity 行可显示文件名 + `+/-` 摘要。
- `assistant/chunk`（reasoning-delta）：转发为 `EventThinking`，是 DSH 自产 reasoning，非用户私有数据。
- 环境变量：`core.BuildAgentEnv` 过滤；`DEEPSEEK_API_KEY` 不进日志。
- 日志：不记录 prompt 全文、tool 参数、文件内容、token、账户信息。

---

## 8. capability 证据 → 实现对照表

| capability | core interface | 实现条件 | 依据 |
|---|---|---|---|
| `session_state` | `core.Agent` (baseline) | 总是声明 | session/prompt + session.event 流 |
| `workspace_diff` | `core.WorkDirSwitcher` | 实现后声明 | `DSH_CWD` env / initialize.cwd |
| `model_switch` | `core.ModelSwitcher` | 实现后声明 | initialize.model；DSH 无 listModels，`AvailableModels` 返回固定 DeepSeek 模型表 |
| `diagnostics` | `core.DiagnosticsProvider` | 总是实现 | `dsh-jsonrpc-agent` 可执行性 + initialize 握手 |
| `session_history` | `core.HistoryProvider`/`RichHistoryProvider` | 扫 JSONL 实现后声明 | 一期可先不声明 |
| `external_turn_streaming`（A 类静态） | — | **声明**（WireDescriptor.StaticCapabilities） | session.event 是 push 流，非 polling |
| `usage_reporting` | `core.TokenUsageReporter` | 实现后声明 | `assistant/message.usage` |
| `permission_resolve` | `core.ToolAuthorizer` | **暂不声明** | SDK 协议 server→client request 是死能力（§3.5） |
| `todos` | `core.TodoProvider` | 实现后声明 | `todo/write` 事件 |
| `session_pin` | `core.SessionPinner` | 实现后声明 | `pinstore.FromOpts` |
| `supports_checkpoint` | `core.CheckpointProvider` | 评估后声明 | DSH JSONL 可作 checkpoint 源 |
| `question_reply` | 不声明 | DSH 无 question 协议 | — |
| `content_chunking` | 不声明 | 仅 Claude 声明 | — |

### `WireDescriptor` 自描述（§6.2）

```go
&core.WireDescriptor{
    Kind:        "deepseek",          // 不转 snake_case，与 iOS fromWireKind 对应
    DisplayName: "DeepSeek",
    LiveEventModel: core.LiveEventBroadcast,  // session.event 是 push 流（非 session_process polling）
    RequiresExternalTurnPolling: false,        // event 直接从 driver 自己 spawn 的进程 stdout 出
    StaticCapabilities: []string{"external_turn_streaming"},
}
```

**注意 `LiveEventModel`**：grokbuild 是 `session_process`（需 tail 文件观察外部 turn）；DSH 是 `broadcast`（更接近 opencode —— event 流直接从 driver 持有的进程出来，driver 既是发送方又是观察方）。

### `deriveBackendCapabilities` 硬编码分支审查

DSH driver id 不匹配 claudecode/opencode/codex 任一特判分支，capability 全靠 interface 实现自动声明，**无需修改 `backend_capabilities.go` 的 id-keyed 分支**（与 grokbuild 结论一致）。

---

## 9. protocol 决策

**无 bridge-v1 protocol change。** DSH SDK 协议的全部事件都能用现有 `bridge-v1.md` schema 表达：
- `session.event`（SessionEvent）→ 现有 bridge event（text/thinking/tool/usage/todo/turn）
- `turn/start`·`turn/end` → 现有 turn 语义
- 不需要新 bridge event type，不需要提高 protocol version

**driver ↔ runtime** 的 DSH SDK JSON-RPC 是 driver 内部实现细节，**不进入 bridge wire**（iOS 永远只看到 core.Event / bridge event）。

---

## 10. 文件结构

```
agent/deepseek/
├── deepseek.go        # init() + New(opts) + Agent struct + core.Agent 方法 + 能力子接口
├── session.go         # deepseekSession struct + core.AgentSession 实现（spawn/readLoop/Close）
├── sdk_codec.go       # DSH SDK JSON-RPC 编解码 + SessionEvent → core.Event 转换
├── sdk_types.go       # DSH SDK wire types（SessionEvent union 子集、Initialize/Prompt 等）
├── history.go         # core.HistoryProvider/RichHistoryProvider（扫 JSONL，含 zstd/packed 展开或 none 配置）
├── wire_descriptor.go # core.WireDescriptorProvider 自描述
├── diagnostics.go     # core.DiagnosticsProvider
├── session_pin.go     # core.SessionPinner（pinstore.FromOpts）
├── proc_unix.go       # 进程组管理（从 grokbuild 复制）
├── proc_windows.go
├── config/            # driver 专用 cordis.yml 模板（compression:none + packChunks:false 便于历史读取）
└── *_test.go          # 单元测试（fake stdio + fixture session.event 回放）
```

**与 grokbuild 的复用关系**：
- `proc_unix.go` / `proc_windows.go` / Close 三阶段 / `core.BuildAgentEnv` —— **直接复制**（通用 stdio 进程模型）
- `acp_codec.go` / `acp_types.go` —— **不复用**（那是 ACP sessionUpdate 映射；DSH 是 SessionEvent union，形态完全不同，需新写 `sdk_codec.go`）
- catalog 子进程单例 —— **不需要**（DSH 无外部 turn 观察渠道，event 直接从 driver 进程出；session list 靠扫文件而非 RPC 子进程）

**编译时断言**：
```go
var _ core.Agent = (*Agent)(nil)
var _ core.AgentSession = (*deepseekSession)(nil)
var _ core.WireDescriptorProvider = (*Agent)(nil)
var _ core.DiagnosticsProvider = (*Agent)(nil)
```

---

## 11. 三仓改动面（参照 grokbuild 接入 = go-bridge 侧约 23 文件）

### A. MacBridge driver（新包 `agent/deepseek/`）
见 §10。

### B. go-bridge 协调层
- `main.go`：blank import `_ ".../agent/deepseek"`；`-drivers` 默认列表 + `agentAliases`；`buildAgentOptions` 加 DSH 专属 opts（exe 路径、cordis.yml 路径、session root）
- `agent_descriptor.go`：`agentKind`/`agentDisplayName` switch 加 `deepseek` case；`detectAgentStatus` 加 `detectDshRuntime`（LookPath `dsh-jsonrpc-agent` + initialize 握手探测）
- `backend_capabilities.go`：无需改 id 分支（§8）
- `handlers.go`：`handleListSessions`/`handleSendMessage`/`handleResumeSession` dispatch 加 `deepseek`；DSH 无外部 turn relay（不需要 grokbuild 的 `handlers_grok_catalog.go` + leader relay）
- `session_discovery.go`：DSH 是 driver-holding 进程，live session 由 driver 进程 liveness决定
- `server.go` `advertiseSessionSyncV2Backend`：若走 SSV2 hydrate 加 id/kind

### C. iOS + remote-web（双仓同步）
- `OpenCodeiOS/.../Services/Backend/BackendModels.swift`：`BackendKind` 加 `case deepSeek`（或 `dsh`）+ `fromWireKind` 映射 `"deepseek"` + displayName/iconName
- `CCCodeBridgeBackendClient.swift`：backendKind → backend id
- `remote-web`：backend 识别 + 显示名 + logo

---

## 12. Gate 0 证据（连通性验证）

**目标**：证明 Go 进程能 spawn DSH runtime、驱动 initialize + session/prompt、消费完整 `session.event` 流。

**方法**：复刻 DSH 自身的 `examples/jsonrpc-agent/tests/keyless-smoke.e2e.ts` —— 本地 HTTP server 模拟 DeepSeek API（canned SSE turn），`DEEPSEEK_BASE_URL` 指向它（无需真 key），Go spawn `node --import tsx bin.ts cordis.yml`，发 initialize → session/prompt → 捕获 `session.event` → shutdown。

验证脚本：`/tmp/dsh-connectivity/main.go`（一次性，不入库）。

**实测结果（2026-08-13，VERDICT: PASS）**：

Go（`go1.26.2 darwin/arm64`）spawn `node --import tsx .../bin.ts cordis.yml`（`node v25.9.0`），驱动一个 canned turn，**成功捕获 16 个 `session.event` 通知**，`seq` 单调递增 0→15，完整覆盖一个 turn 的生命周期：

```
agent/inbox/spliced (seq 0)   ← prompt 入队（target: next-turn）
turn/start (seq 1)
agent/inbox/spliced (seq 2)   ← turn claim
step/start (seq 3)
user/message (seq 4)          ← source.kind: user
session/title (seq 5)
request/header (seq 6)
request/context (seq 7)
assistant/chunk (seq 8-12)    ← 5 个 token 级流式 chunk
assistant/message (seq 13)    ← 组装消息
step/end (seq 14)
turn/end (seq 15)             ← reason: completed（canned turn 走 stop）
```

并确认：
- **`session.status` 双通道**：`running`（prompt 后）→ `idle`（turn/end 后）
- **event 信封结构**符合 `SessionEvent` schema（`{type, seq, time, data}`，sample frame 含 `agent/inbox/spliced.data.inserted[].content[].text`）
- **`initialize` 响应** `serverInfo.name === 'deepseek-harness-sdk-runtime'`、`version: '0.0.1'`
- **`session/prompt` 返回** `{messageId}` 入队回执（不关联 turn 结果，坐实 §3.4"turn 完成只认 turn/end"）
- **模型请求** 1 次 `POST /chat/completions`（6541 bytes，携带 `max_tokens` + tools schema），证明 cordis.yml 组合的工具面正确注入

**结论**：Go 进程能通过 stdio JSON-RPC 完整驱动 DSH runtime 并消费 rich `session.event` 流，§3.3 的 SessionEvent→core.Event 映射可基于这些真实 event.type 落地。验证脚本一次性使用，不入库。

---

## 13. 风险与回退

1. **pre-release 漂移（最大风险）**：DSH 协议 `0.0.1` 无版本协商、`SESSION_FORMAT_VERSION=0` 无兼容承诺。DSH 升级可能直接打挂 driver（未知非 ignorable event 拒读）。**缓解**：driver forward-compat 处理（ignorable 跳过 + 非 ignorable 降级而非崩溃）；锁定 runtime 版本（bundled exe pin）。
2. **取消即重启**：SDK 无 cancel，中断 turn = kill 进程 + session 结束。iOS 需诚实表达，不能伪装优雅中断。
3. **无 session list**：一期扫 JSONL（zstd/packed 需 Go 解码）或先不支持历史列表。
4. **权限降级**：approval 不经协议暴露，iPhone 无法 per-call 授权（走 cordis.yml policy）。
5. **DeepSeek 绑定**：runtime adapter 自动挂载是 DeepSeek-specific，一期基本绑定 DeepSeek 模型。
6. **回退**：若 pre-release 风险不可接受，等 DSH 首个 tagged release（其 AGENTS.md 说"Remove this section at the first tagged release"后才有兼容承诺）再接入。
