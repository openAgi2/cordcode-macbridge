# DeepSeek Harness (DSH) Driver 设计文档

- **日期**：2026-08-13（v2：按审计 `docs/2026-08-13-dsh-driver-design-audit.md` P0 修订）
- **Runtime**：DeepSeek Harness `dsh-jsonrpc-agent`（pinned commit `47f943859bef60e4160492346772ded9b24f765a`，协议 `0.0.1`，`SESSION_FORMAT_VERSION=0`，pre-release 无兼容承诺）
- **协议**：DSH SDK JSON-RPC 2.0（newline-delimited）over stdio —— **不是** ACP
- **入口**：spawn `dsh-jsonrpc-agent <cordis.yml>`（bundled exe，或 `node --import tsx .../bin.ts <cordis.yml>`）
- **Gate 0 证据**：`scripts/dsh-gate0/`（脚本 + 复现步骤 + pinned commit + raw frames），摘要见 §12
- **审计状态**：v1 有 3 个 P0（descriptor 误报、resume 未闭环、权限 composition 不符），v2 已修订；**当前结论仍为不进入实现，待补剩余 dump（见 §14）**

---

## 0. 协议选型：为什么不用 ACP

DSH 同时实现两条 stdio JSON-RPC 协议。逐行核对源码后的结论是 **只有 SDK 协议满足 rich timeline 需求**：

| 维度 | ACP server（`packages/acp/acp/src/index.ts`） | **SDK server（`packages/sdk/server`+`protocol`）✅ 选定** |
|---|---|---|
| assistant 文本 | ✅ 仅已提交文本 | ✅ token 级流（`assistant/chunk`）+ 组装（`assistant/message`） |
| thinking/reasoning | ❌ 显式丢弃（codec 注释 L152-154） | ✅ `reasoning-delta` chunk |
| tool calls / 结果 | ❌ 丢弃 | ✅ `tool/call` + `tool/result` |
| todos / usage / turn 状态 | ❌ 丢弃 | ✅ `todo/write` / `usage` / `turn/*` |

> **审计 verified**：DSH ACP 源码只转发 committed assistant text，显式排除 raw chunks/reasoning/tools/plans —— SDK 路径选择成立。

> **修订（审计 P2-1）**：DSH `KNOWN_SESSION_EVENT_TYPES` 实为 **44 种**（从 pinned commit `47f9438` 枚举），非 v1 所写 45。

---

## 1. Runtime 版本范围

- **pre-release**：`serverInfo.version` 恒 `0.0.1` 且客户端不校验。**无协议版本协商**。
- **检测**：spawn 后用 `initialize` 响应的 `serverInfo.name === 'deepseek-harness-sdk-runtime'` 验证；版本字段不可靠，不做 semver gate。
- **`SESSION_FORMAT_VERSION=0`**：持久化无兼容承诺。driver 历史读取路径须容忍"未来读不了"。
- **forward-compat**：未知 `event.type` 带 `ignorable:true` 可跳过，否则**拒绝重建**（known-event-types.ts L10-18）。driver 复用该语义。

---

## 2. 进程参数

```
dsh-jsonrpc-agent <path/to/cordis.yml>      # bundled exe 或 node --import tsx
```

运行时**强制要求显式 config**（`$DSH_CORDIS_CONFIG` 或 argv；无 config `exit(1)`）。config 决定**工具面 + 权限栈**（见 §3.5，这是 v1 的错误点之一）。

**关键环境变量**：

| 变量 | 用途 |
|---|---|
| `DEEPSEEK_API_KEY` / `DEEPSEEK_BASE_URL` | API 凭据 / endpoint |
| `DSH_CWD` | agent 工作目录 |
| `DSH_SESSION_ROOT` | JSONL 持久化根目录 |
| `DSH_PERMISSION_MODE` | 权限档位（仅当 cordis.yml 挂载了 `dsh-sandbox-policy`+`dsh-user-approval`+`dsh-permission-presets` 时生效，见 §3.5） |

**stdout 是协议通道**：runtime stdout 只走 JSON-RPC，诊断走 stderr。driver 不得挂 stdout logger。

**进程组**：复用 grokbuild `prepareCmdForProcessGroup`（`Setpgid`），Close 用负 PID 信号回收进程组。

---

## 3. 输入输出 schema

### 3.1 传输层

newline-delimited JSON-RPC 2.0 over stdin/stdout。`id`+`method`=request，仅 `id`=response，仅 `method`=notification。坏 JSON 行静默忽略。

- **MacBridge → DSH**（stdin）：`initialize`、`session/prompt`、`shutdown`
- **DSH → MacBridge**（stdout）：上述 response + `session.event` / `session.status` / `subagent.*` notification

> **SDK 协议无 cancel、无 session/close、无 session/resume/load/list**（已核实 grep 空）；server→client request 是死能力。

### 3.2 初始化握手

1. MacBridge 发 `initialize`（`{cwd, provider, model, maxTokens?}`）
2. DSH 返回 `{serverInfo:{name:'deepseek-harness-sdk-runtime', version:'0.0.1'}}`
3. 无 authenticate（凭据走 `DEEPSEEK_API_KEY` env）
4. `provider:'deepseek-official'` 自动挂 `dsh-llm-deepseek`；其它未注册 provider 报错（一期绑定 DeepSeek）

握手后即可 `session/prompt`。**无显式 session/new**：`session/prompt` 带 `sessionId`，runtime 对未知 id 惰性创建（`getOrCreateSession`）。

### 3.3 `session.event` → core.Event 映射（按证据分级）

`session.event` 的 `params.event` 是完整 `SessionEvent` 信封（`{type, seq, time, data, surfaceOp?, sourceEventSeqs?, ignorable?}`）。映射表按**有无真实 dump** 分级（证据来源：Gate 0 + committed snapshots `examples/jsonrpc-agent/tests/snapshots/{text-turn,bash-tool,subagent-spawn-in-process,persistent-tools}` + 审计交叉核验）。

#### 🟢 Verified（有 dump，一期映射）

| DSH `event.type` | 真实 payload 关键字段 | core.Event |
|---|---|---|
| `turn/start` | `data.turn` | `EventTurnStarted` |
| `turn/end`（`reason.kind:"completed"`） | `data.turn, data.reason` | 终态 `EventResult{Done:true}` |
| `step/start`·`step/end` | `data.turn, data.step` | 内部 step 边界（iOS 默认不展示） |
| `user/message` | `data.content[], data.source.kind` | `EventUserMessage` |
| `assistant/chunk`（`chunk.type:"text-delta"`） | `data.chunk.text` | `EventText`（流式） |
| `assistant/chunk`（`chunk.type:"reasoning-delta"`） | `data.chunk.text` | `EventThinking` |
| `assistant/message` | `data.message.content[]`(reasoning/tool-call/text blocks), `data.usage?` | 组装态 + context usage |
| `tool/call` | `data.callId, data.name, data.arguments`(JSON string) | `EventToolUse` |
| `tool/result`（基本结果） | `data.message.content[].content[].text`（嵌套）, `data.message.source.callId` | `EventToolResult` |
| `session/title` | `data.title, data.messageSeqs` | session 标题 |
| `request/header`·`request/context` | `data.contextWindow` 等 | context usage 输入（不映射为 timeline event） |
| `agent/inbox/spliced` | `data.target, data.inserted[]` | prompt 入队（内部，可丢弃） |

**chunk discriminant**（审计 P1-1）：`assistant/chunk.data.chunk.type` 是 `text-delta`/`reasoning-delta`/`block-start`/`block-end`/`usage`/`finish`，codec 必须按它分流，不能假设只有 text。

**turn/result 嵌套**（审计 P1-1）：`tool/result` 正文在 `.data.message.content[].content[].text`（双层 content 嵌套），不是顶层 text。

#### 🔴 一期 unsupported（无 dump，不声明 capability）

以下 30 种事件**无 Gate0/snapshot 样本**，一期**不映射、不声明对应 capability**（审计 P0-4："没有样本的能力保持 unsupported/不声明"）：

```
todo/write                  → 不声明 todos（需 FetchTodos 持久化读路径样本）
compaction/start|end|summary|prune → 不映射（core.Event 只有 compressing/compressed 两态，summary/prune 处理未定义）
approval/asked|decided|policy → 不声明 permission_resolve（见 §3.5，当前 composition 无样本）
tool/result.error / .meta(FsDiffMeta.diffs) → 不映射文件 diff（无实样；且 iOS 不做消息内 diff）
turn/end 非 completed 终态（error/aborted/interrupted/blocked/max-tokens）→ 六态测试待补
goal/change, plan/mode, schedule/change → 不进 control plane（无样本）
command/run, command/done, tool-workflow/*, tool/code-dispatch* → ignored
session/end-seed, subagent/descriptor 细节 → hydrate/subagent 映射待补
其余（agent-preset/selected, feedback/record, hook/*, llm/retry*, permission/preset, sandbox/mode, session/title-llm-request, web/*）→ ignored
```

> **诚实边界**：`todo/write`、`compaction/*`、`approval/*`、`FsDiffMeta`、非 completed 终态的 runtime dump 需要 `DEEPSEEK_API_KEY`（真实模型 turn）或 assembled approval composition。无 key 环境下无法补，标 unsupported。

### 3.4 turn 生命周期（双通道，seq 不跨 process 对齐）

1. **`session.event` 的 `turn/start` + `turn/end`**：精确 turn 边界。`turn/end.reason.kind` ∈ {completed, aborted, blocked, error, max-tokens, interrupted}（types.ts L155-174）—— 但**仅 completed 有 dump**，其余 5 态待补。
2. **`session.status`**（`{sessionId, status:'running'|'idle'}`）：whole-agent 转换。

**`session/prompt` 只回 `{messageId}` 入队回执，不关联 turn 结果**（审计 verified）。turn 完成只认 `turn/end`，不得用 prompt response 推断。

**seq 不跨 process 对齐（审计 P0-2）**：`seq` 是单 session 单 process 内单调序号。runtime 重启后 fresh-create 同 id 会从新 seq 开始，**不能天然对应 MacBridge `baseRev→syncRev`**。一期 live-only（§4），不存在跨 process seq 对齐；若未来支持 resume，seq 对齐需显式 fence。

### 3.5 权限模型（v2 重写，审计 P0-3）

**v1 错误**：写"受 `DSH_PERMISSION_MODE` / cordis.yml policy 控制"——但 Gate 0 用的 `examples/jsonrpc-agent/cordis.yml` 是 **unattended composition**，未挂载任何 sandbox/approval plugin，权限边界=runtime 进程主机权限。

**核实**：`DSH_PERMISSION_MODE` 与完整权限栈存在于 **`packages/bundle/base/cordis.patch.yml`**（非 jsonrpc-agent）：

```yaml
- dsh-sandbox-local          # sandbox provider
- dsh-sandbox-policy         # mode: DSH_PERMISSION_MODE ?? 'workspace-write'
- dsh-bash-sandbox / dsh-pwsh-sandbox   # 沙箱化 shell
- dsh-user-approval          # policy: DSH_PERMISSION_MODE==='danger-full-access' ? 'never' : 'ask'
- dsh-permission-presets     # read-only / workspace-write / danger-full-access 三档
```

**v2 方案**：driver **必须自建专用 cordis.yml**（§10 `config/`），从 bundle/base 借这套权限栈替代裸 `bash-local`/`fs-local`，否则 DSH 无审批地以主机权限读写。`DSH_PERMISSION_MODE` 默认 `workspace-write`（sandbox 限 cwd + approval policy `ask`）。

**一期 `permission_resolve` 仍不声明**：SDK 协议的 server→client request 是死能力，approval 在 runtime 内部消化（按 cordis.yml policy），不经协议暴露给 MacBridge/iPhone per-call 授权。

**实施前必须补**：对组装后的 driver composition 做真实拒绝/允许样本（需 `DEEPSEEK_API_KEY` 触发工具调用，或 mock 触发 + 验证 sandbox 边界）。当前无 key，标待补。

---

## 4. session 生命周期（v2：收敛为 live-only，审计 P0-2）

**一期 live-only**：

| 操作 | 一期支持 | 说明 |
|---|---|---|
| 创建 session | ✅ `session/prompt`（新 sessionId 惰性创建） | |
| 发送消息 | ✅ `session/prompt` | 返回 `{messageId}` |
| 取消 turn | ❌ 无 cancel（kill 进程，§5） | session 随之终止 |
| 列出 session | ❌ **一期不支持** | SDK 无 session/list；扫 JSONL 只能恢复 Mac 投影，不能恢复 DSH 模型上下文 |
| resume 历史 session | ❌ **一期不支持** | 见下 |

**resume 闭环问题（审计 P0-2）**：
- SDK 无 `session/resume/load`。runtime 重启后 `getOrCreateSession` 是 **fresh-create**，不 seed 旧对话上下文（DSH 内部恢复入口 `ctx.agents.resume` 未暴露到 SDK wire）。
- 扫 JSONL 只能 hydrate **Mac Projection Kernel**（timeline 展示），**不能把旧对话 seed 回 DSH agent**。因此即使列表可见，也无法在 runtime 重启后携带原上下文继续对话。

**一期决策**：**不声明 `session_history`，不展示可 resume 的历史 session，runtime 死/cancel 即 session 终止**。删除 v1 的 "handleResumeSession dispatch" 与 "扫 JSONL 实现 history" 承诺。

**未来 resume 路径（二选一，需真实 dump 证明）**：
1. 维持 live-only（长期，简单）；或
2. 先给 DSH runtime 加真实 resume wire（内部调 `ctx.agents.resume`），用"首轮→shutdown→同 id resume→第二轮含首轮上下文"的 dump 证明 —— 仅扫 JSONL 不能替代。

---

## 5. 取消与关闭

### 取消 turn

**SDK 无 cancel**。取消 = kill runtime 进程 + session 终止（一期 live-only，§4）。**这是 DSH 相对 Claude/Codex 的明确降级**，iOS 须诚实表达。后续若 DSH 加 cancel 方法（transport 已支持 request），再实现 `core.TurnCanceler`。

### Close（三阶段，复用 grokbuild `proc_unix.go`）

1. **shutdown RPC**（runtime flush 后自行 dispose + exit 0）+ stdin close，等退出（8s）
2. **SIGTERM** 进程组（5s）
3. **SIGKILL** 进程组

> 审计 🟡 needs-refinement：三阶段超时与进程组子进程回收**未实测**（仅实测正常 `shutdown→{}`）。实施时补 SIGTERM/SIGKILL 路径样本。

环境变量用 `core.BuildAgentEnv` 过滤 control-plane secret。

---

## 6. 错误分类

| 错误类型 | 处理 |
|---|---|
| JSON-RPC error response（`-32601`/`-32603`） | 解析 code，`EventError` |
| stdout JSON 解码失败 | 记录原始行（脱敏），继续读 |
| 进程意外退出 | 终态 `EventError{Done:true}` |
| `initialize` 超时 / `serverInfo.name` 不符 | fail-closed，descriptor `not_detected` |
| 未知非 ignorable event | 记录 + 降级（不静默丢） |

> 审计 🔴 unverified：JSON-RPC error response、坏 JSON、超长行**无样本**。实施时补。

---

## 7. 敏感信息脱敏

- `tool/call.arguments` / `tool/result` 正文：不全文广播，只提取 title/kind/status/locations。
- **iOS 不做消息内文件级 diff（2026-08-01 owner 决策）**：`tool/result.meta` 的 `FsDiffMeta.diffs`（无实样）不映射 inline diff；文件变更只走 session 级 diff bar。
- `DEEPSEEK_API_KEY` 不进日志；`core.BuildAgentEnv` 过滤。

---

## 8. capability 与 WireDescriptor（v2 修正，审计 P0-1/P0-4）

### WireDescriptor（v2 修正）

```go
&core.WireDescriptor{
    Kind:        "deepseek",
    DisplayName: "DeepSeek",
    LiveEventModel:              core.LiveEventSessionProcess, // owned-process，非 broadcast
    RequiresExternalTurnPolling: false,                        // = 不支持外部 turn 观察（非"push 已覆盖"）
    StaticCapabilities:          nil,                          // 不声明 external_turn_streaming
}
```

**v1 错误**：声明了 `LiveEventBroadcast` + `external_turn_streaming`。核实 `external_turn_streaming` 是 MacBridge "push 外部进程产生的 turn"的能力（agent_descriptor_test.go L412，grok/codex external turn 用）；`LiveEventBroadcast` 是 opencode 那种 service-level fan-out 给多观察者。**DSH 是 driver 自己 spawn 持有的进程，event 从它的 stdout 出，只能证明 CordCode 自己发起的 turn，看不到另一个 Terminal/另一个 DSH process 的 turn** —— 是 `LiveEventSessionProcess`，不声明 `external_turn_streaming`。`RequiresExternalTurnPolling:false` 须解释为"不支持外部观察"，非"push 已覆盖外部 turn"。

### capability → interface（v2 收缩到有证据项）

| capability | interface | v2 决策 | 依据 |
|---|---|---|---|
| `session_state` | `core.Agent` | ✅ 声明 | session/prompt + session.event |
| `workspace_diff` | `core.WorkDirSwitcher` | ✅ 声明 | DSH_CWD |
| `diagnostics` | `core.DiagnosticsProvider` | ✅ 声明 | exe 探测 + initialize 握手 |
| `external_turn_streaming` | — | ❌ **不声明**（v1 错误） | owned-process，无外部 turn 观察 |
| `session_history` | HistoryProvider | ❌ **一期不声明** | live-only（§4） |
| `usage_reporting` | TokenUsageReporter | ❌ **不声明** | 需跨 transcript 聚合/去重，未定义（审计 P1-4） |
| `todos` | TodoProvider | ❌ **不声明** | 无 todo/write dump + 需 FetchTodos 持久化读路径 |
| `permission_resolve` | ToolAuthorizer | ❌ **不声明** | SDK server→client request 死能力（§3.5） |
| `model_switch` | ModelSwitcher | ⚠️ 待定 | DSH 无 listModels；AvailableModels 返回固定表待定 |
| `session_pin` | SessionPinner | ⚠️ 可选 | pinstore.FromOpts（与 history 解耦） |
| `supports_checkpoint` | CheckpointProvider | ❌ 不声明 | 一期 live-only |

**`deriveBackendCapabilities` 硬编码分支**：DSH id 不匹配 claudecode/opencode/codex 任一特判，**无需改**。

---

## 9. protocol 决策

**无 bridge-v1 protocol change。** DSH SDK 事件全部能用现有 bridge event 表达；driver↔runtime 的 SDK JSON-RPC 是 driver 内部细节，不进 bridge wire（iOS 只见 core.Event）。

> 即便 bridge-v1 无需升 major，按双仓规则仍须更新 MacBridge canonical protocol compatibility pack 并同步 iOS mirror/backend kind（审计 P1-6）。

---

## 10. 文件结构（v2：加专用 cordis.yml）

```
agent/deepseek/
├── deepseek.go        # init() + New + core.Agent + 能力子接口（仅 v2 收缩后的）
├── session.go         # core.AgentSession（spawn/readLoop/Close）
├── sdk_codec.go       # SDK JSON-RPC 编解码 + SessionEvent → core.Event（仅 §3.3 verified 项）
├── sdk_types.go       # SDK wire types 子集
├── wire_descriptor.go # §8 WireDescriptor
├── diagnostics.go     # DiagnosticsProvider
├── session_pin.go     # SessionPinner（pinstore.FromOpts，可选）
├── proc_unix.go / proc_windows.go   # 从 grokbuild 复制
├── config/
│   └── cordis.yml     # driver 专用 composition（挂 §3.5 权限栈，非裸 bash-local/fs-local）
└── *_test.go
```

### driver 专用 `config/cordis.yml` 模板（基于 bundle/base，审计 P0-3）

```yaml
# stdout 仅供 JSON-RPC；权限栈来自 bundle/base（非 jsonrpc-agent 裸 composition）
- id: sdk-jsonrpc-server
  name: '@deepseek-ai/dsh-sdk-jsonrpc-server'
- id: llm-deepseek
  name: '@deepseek-ai/dsh-llm-deepseek'
  config: { thinking: enabled, reasoningEffort: max }
- id: subprocess
  name: '@deepseek-ai/dsh-subprocess-local'
- id: sandbox
  name: '@deepseek-ai/dsh-sandbox-local'
- id: sandbox-policy
  name: '@deepseek-ai/dsh-sandbox-policy'
  config:
    mode: !!js process.env.DSH_PERMISSION_MODE ?? 'workspace-write'
    workspaceRoot: !!js process.env.DSH_CWD ?? process.cwd()
- id: bash-sandbox
  name: '@deepseek-ai/dsh-bash-sandbox'
  config: { timeoutMs: 60000 }
- id: approval
  name: '@deepseek-ai/dsh-user-approval'
  config:
    policy: !!js "(process.env.DSH_PERMISSION_MODE ?? 'workspace-write') === 'danger-full-access' ? 'never' : 'ask'"
- id: permission
  name: '@deepseek-ai/dsh-permission-presets'
  config:
    presets:
      workspace-write: { sandbox: workspace-write, approval: ask }
      danger-full-access: { sandbox: danger-full-access, approval: never }
- id: agent-spine
  name: '@deepseek-ai/dsh-agent-spine-demo'
  config: { persona: !!js process.env.DSH_SYSTEM_PROMPT ?? 'You are a coding agent.', workspaceContext: false, skills: { enabled: false } }
- id: sessions
  name: '@deepseek-ai/dsh-session-persistence-jsonl'
  config:
    root: !!js process.env.DSH_SESSION_ROOT ?? './.sessions'
    compression: none        # 一期便于 Go 直接读 JSONL（若做 history）
    # packChunks: false      # 同理（待确认 config 字段名）
- id: session-checkpoints
  name: '@deepseek-ai/dsh-session-checkpoint-policy'
```

> **注意**：此模板**未经 assembled runtime dump 验证**（审计 🟡 needs-refinement：`compression:none`+`packChunks:false` 的目标 driver config 尚不存在）。实施前必须用真实组装验证 plugin 组合可加载 + sandbox/approval 生效 + fail-closed 拒绝样本。

**与 grokbuild 复用**：`proc_unix.go`/Close/`BuildAgentEnv` 直接复制；`acp_codec.go`/`acp_types.go` **不复用**（ACP sessionUpdate ≠ SessionEvent union）；**不需要** catalog 子进程（无外部 turn 观察，无 session/list）。

---

## 11. 三仓改动面（v2 收缩）

### A. MacBridge driver（`agent/deepseek/`）—— §10
### B. go-bridge
- `main.go`：blank import + `agentAliases` + `buildAgentOptions`（exe/cordis.yml/session root）
- `agent_descriptor.go`：`agentKind`/`agentDisplayName`/`detectAgentStatus` 加 `deepseek`（detectDshRuntime：LookPath + initialize 握手）
- `handlers.go`：`handleSendMessage` dispatch 加 `deepseek`；**v2 删除 v1 的 `handleListSessions`/`handleResumeSession` dispatch**（live-only）
- `backend_capabilities.go`：无需改 id 分支
- **不需要** `handlers_grok_catalog.go` 式的 catalog（无 session list）

### C. iOS + remote-web
- `BackendModels.swift`：`BackendKind` 加 `deepSeek` + `fromWireKind:"deepseek"` + displayName/icon
- `CCCodeBridgeBackendClient.swift`：kind→id 映射
- `remote-web`：识别 + 显示名
- **iOS 不展示 DSH 的"历史会话列表/resume"**（对应 live-only）

---

## 12. Gate 0 证据（纳入仓库，审计 P0-5）

**脚本 + 复现步骤 + pinned commit + 脱敏 raw frames**：`scripts/dsh-gate0/`（macbridge 仓库）。

**方法**：复刻 DSH `examples/jsonrpc-agent/tests/keyless-smoke.e2e.ts` —— 本地 HTTP server 模拟 DeepSeek API（canned SSE turn），`DEEPSEEK_BASE_URL` 指向它（无需真 key），Go spawn `node --import tsx bin.ts cordis.yml`，发 initialize → session/prompt → 捕获 `session.event` → shutdown。

**pinned**：DSH commit `47f943859bef60e4160492346772ded9b24f765a`；node v25.9.0；go1.26.2 darwin/arm64。

**实测结果（2026-08-13，VERDICT: PASS）**：捕获 16 个 `session.event`（seq 0→15）：`agent/inbox/spliced`×2 → `turn/start` → `step/start` → `user/message` → `session/title` → `request/header` → `request/context` → `assistant/chunk`×5 → `assistant/message` → `step/end` → `turn/end(completed)`；`session.status` running→idle 双通道；落盘 `<root>/<encoded-project>/<encoded-session>/session.jsonl.zstd`。

**覆盖边界**：仅 1 个 `completed` 纯文本 turn，覆盖 44 种事件中的 14 种（committed snapshots 交叉补证 tool/call、reasoning-delta、tool/result 基本结构）。**剩余 30 种无 runtime dump**（§3.3 unsupported 清单）。

---

## 13. 风险与回退

1. **pre-release 漂移（最大）**：协议 `0.0.1` 无协商、`SESSION_FORMAT_VERSION=0` 无兼容。缓解：forward-compat（ignorable 跳过）+ pin runtime 版本。
2. **取消即 session 终止**：无 cancel，中断=kill+session 结束（live-only）。
3. **无 session list / resume**：一期 live-only。
4. **权限降级**：approval 不经协议暴露，iPhone 无 per-call 授权；runtime 内 sandbox/approval 由 driver composition 控制（须自建，§3.5/§10）。
5. **DeepSeek 绑定**：adapter 自动挂载 DeepSeek-specific。
6. **证据缺口**：todo/compaction/approval/FsDiff/非 completed 终态/错误路径/sandbox 拒绝样本均无（需 key 或 assembled composition），是"不进入实现"的主因。
7. **回退**：等 DSH 首个 tagged release 再接入。

---

## 14. 修订记录（响应审计 P0）

| P0 | v1 问题 | v2 修订 | 状态 |
|---|---|---|---|
| P0-1 | `LiveEventBroadcast`+`external_turn_streaming` 误报 | 改 `LiveEventSessionProcess`，移除 `external_turn_streaming`（§8） | ✅ 文档已改 |
| P0-2 | resume 闭环缺失（扫 JSONL 不能恢复模型上下文） | 收敛 live-only：不声明 session_history、不展示/resume 历史（§4） | ✅ 文档已改 |
| P0-3 | `DSH_PERMISSION_MODE` 误用于 jsonrpc composition | 写明权限栈在 bundle/base；driver 自建专用 cordis.yml（§3.5/§10） | ✅ 文档已改；⚠️ assembled dump 待补 |
| P0-4 | 映射表"几乎一一对应"过度承诺 | §3.3 按 verified/unsupported 分级，无样本不声明（§8） | ✅ 文档已改；⚠️ 30 种 dump 待补 |
| P0-5 | Gate 0 依赖 /tmp 一次性证据 | pin commit + 脚本/raw frames 纳入 `scripts/dsh-gate0/`（§12） | ✅ 本次提交 |

**剩余阻塞（需 owner 提供 `DEEPSEEK_API_KEY` 或真实模型环境才能补）**：todo/compaction/approval/FsDiff dump、非 completed 五态、错误路径、sandbox 拒绝样本、assembled driver composition 验证。补齐前**不进入实现**。
