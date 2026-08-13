# DeepSeek Harness (DSH) Driver 设计文档

- **日期**：2026-08-13（v3：用真实 `DEEPSEEK_API_KEY` 补齐事件 dump 与 assembled composition 验证）
- **Runtime**：DeepSeek Harness `dsh-jsonrpc-agent`（pinned commit `47f943859bef60e4160492346772ded9b24f765a`，协议 `0.0.1`，`SESSION_FORMAT_VERSION=0`，pre-release 无兼容承诺）
- **协议**：DSH SDK JSON-RPC 2.0（newline-delimited）over stdio —— **不是** ACP
- **入口**：spawn `dsh-jsonrpc-agent <cordis.yml>`（bundled exe，或 `node --import tsx .../bin.ts <cordis.yml>`）
- **证据**：`scripts/dsh-gate0/`（Gate 0 脚本 + 4 次真实 run dump + assembled composition），摘要见 §12
- **审计**：v1 有 3 个 P0（v2 修订），v3 补齐 P0-3/P0-4 的真实 dump；剩余仅特殊触发条件项（见 §14）

---

## 0. 协议选型：为什么不用 ACP

DSH 同时实现两条 stdio JSON-RPC 协议，**只有 SDK 协议满足 rich timeline**：

| 维度 | ACP server（`packages/acp/acp/src/index.ts`） | **SDK server（`packages/sdk/server`+`protocol`）✅** |
|---|---|---|
| assistant 文本 | ✅ 仅已提交 | ✅ token 级流 + 组装 |
| thinking/reasoning | ❌ 显式丢弃 | ✅ `reasoning-delta` chunk（**v3 真实 verified**，deepseek-chat 返回 reasoning） |
| tool calls / 结果 | ❌ 丢弃 | ✅ `tool/call` + `tool/result`（**v3 真实 verified**） |
| todos / usage / turn 状态 | ❌ 丢弃 | ✅ `todo/write` / `usage` / `turn/*`（**v3 真实 verified**） |

> 审计 verified：DSH ACP 源码只转发 committed assistant text。DSH `KNOWN_SESSION_EVENT_TYPES` 实为 **44 种**（pinned commit 枚举）。

---

## 1. Runtime 版本范围

- **pre-release**：`serverInfo.version` 恒 `0.0.1`，无协议版本协商。检测靠 `serverInfo.name === 'deepseek-harness-sdk-runtime'`。
- **`SESSION_FORMAT_VERSION=0`**：持久化无兼容承诺。forward-compat：未知 `event.type` 带 `ignorable:true` 可跳过，否则拒绝重建。

---

## 2. 进程参数

```
dsh-jsonrpc-agent <path/to/cordis.yml>      # bundled exe 或 node --import tsx
```

运行时**强制要求显式 config**（argv 位置参数或 `$DSH_CORDIS_CONFIG`；无 config `exit(1)`）。config 决定**工具面 + 权限栈**（§3.5）。

| 变量 | 用途 |
|---|---|
| `DEEPSEEK_API_KEY` / `DEEPSEEK_BASE_URL` | API 凭据 / endpoint |
| `DSH_CWD` | agent 工作目录 |
| `DSH_SESSION_ROOT` | JSONL 持久化根 |
| `DSH_PERMISSION_MODE` | 权限档位（**仅当 cordis.yml 挂载 `dsh-sandbox-policy`+`dsh-user-approval`+`dsh-permission-presets` 时生效**，v3 verified §3.5） |

**stdout 是协议通道**；进程组复用 grokbuild `prepareCmdForProcessGroup`。

---

## 3. 输入输出 schema

### 3.1 传输层

newline-delimited JSON-RPC 2.0。`id`+`method`=request，仅 `id`=response，仅 `method`=notification。

- **MacBridge → DSH**：`initialize`、`session/prompt`、`shutdown`
- **DSH → MacBridge**：上述 response + `session.event`/`session.status`/`subagent.*` notification

> SDK 协议**无 cancel、无 session/close、无 session/resume/load/list**；server→client request 是死能力。

### 3.2 初始化握手

1. 发 `initialize`（`{cwd, provider, model, maxTokens?}`）→ `{serverInfo:{name:'deepseek-harness-sdk-runtime', version:'0.0.1'}}`（v3 真实 verified）
2. 无 authenticate；`provider:'deepseek-official'` 自动挂 `dsh-llm-deepseek`
3. `session/prompt` 带 `sessionId`，runtime 对未知 id 惰性创建（无显式 session/new）

### 3.3 `session.event` → core.Event 映射（v3：真实 dump 分级）

`params.event` 是完整 `SessionEvent` 信封（`{type, seq, time, data, surfaceOp?, sourceEventSeqs?, ignorable?}`）。证据来源：4 次真实 run（`scripts/dsh-gate0/dumps/`）+ committed snapshots。

#### 🟢 Verified（真实 dump 支撑，一期映射）

| DSH `event.type` | 真实 payload 关键字段（v3 dump） | core.Event |
|---|---|---|
| `turn/start` | `data.turn` | `EventTurnStarted` |
| `turn/end`（`completed`） | `data.turn, data.reason.kind:"completed"` | `EventResult{Done:true}` |
| `turn/end`（`max-tokens`） | `data.reason.kind:"max-tokens"`（run2 verified） | token 限制完成（非错误） |
| `step/start`·`step/end` | `data.turn, data.step` | 内部 step 边界 |
| `user/message` | `data.content[], data.source.kind` | `EventUserMessage` |
| `assistant/chunk`（**7 种 discriminant**） | `data.chunk.type` ∈ {`text-delta`,`reasoning-delta`,`tool-call-delta`,`block-start`,`block-end`,`usage`,`finish`} | text-delta→`EventText`；reasoning-delta→`EventThinking`；**codec 必须按 discriminant 分流** |
| `assistant/message` | `data.message.content[]`(reasoning/tool-call/text blocks), `data.usage?`=`{inputTokens,outputTokens,cacheReadTokens,reasoningTokens}` | 组装态 + context usage |
| `tool/call` | `data.{turn,step,callId,name,arguments}`（arguments=JSON string） | `EventToolUse` |
| `tool/result` | `data.message.content[].content[].text`（**双层 content 嵌套**）, `data.message.content[].isError`, `data.message.source.callId`, `sourceEventSeqs`, `surfaceOp:"append"` | `EventToolResult`（isError 区分成败） |
| `todo/write` | `data.todos[]`=`{content, status:"pending"|"in_progress"|"completed"}`（整表快照） | todo 更新（latest-wins） |
| `session/title` | `data.title, data.messageSeqs` | session 标题 |
| `request/header`·`request/context` | `data.contextWindow` 等 | context usage 输入（不映射 timeline） |
| `permission/preset`·`sandbox/mode`·`approval/policy` | `data.preset/data.mode/data.policy`（assembled composition 产物，run3 verified） | 控制面（runtime 权限栈激活信号，可丢弃或诊断） |
| `agent/inbox/spliced` | `data.target, data.inserted[]` | prompt 入队（内部） |

#### 🔴 一期 unsupported（无 dump，不声明）

```
compaction/start|end|summary|prune   → 需超长对话触发压缩（成本高，待补）
approval/asked|decided               → workspace-write 下 cwd/临时区操作不触发；SDK 协议 approval resolve 行为待验证
turn/end 的 error/aborted/interrupted/blocked → 模型失败/中断，难触发（待补）
tool/result.meta 的非空 FsDiffMeta.diffs → write 工具 diffs 实测为空 []（FsDiff 无实样）
goal/change, plan/mode, schedule/change → 默认 composition 无 goal/plan 工具，不触发
command/run, command/done            → bash 工具不产生（需 dsh-shell，未挂载）
tool-workflow/*, tool/code-dispatch* → 无样本
session/end-seed                     → 无 resume（live-only，§4）
其余（agent-preset/selected, feedback/record, hook/*, llm/retry*, session/title-llm-request, web/*）→ ignored
```

**chunk discriminant（审计 P1-1，v3 verified）**：`assistant/chunk` 的 `data.chunk.type` 有 7 种，codec 必须按它分流 —— text-delta/reasoning-delta/tool-call-delta 是内容增量，block-start/block-end/usage/finish 是结构/计费信号。

**turn/result 嵌套（v3 verified）**：`tool/result` 正文在 `.data.message.content[].content[].text`（双层 content），`isError` 在 `.data.message.content[].isError`。

### 3.4 turn 生命周期

1. `session.event` 的 `turn/start`+`turn/end`（reason 6 态，completed + max-tokens 已 verified）
2. `session.status`（`running`/`idle`，whole-agent 转换）

`session/prompt` 只回 `{messageId}` 入队回执（v3 verified），turn 完成只认 `turn/end`。**seq 不跨 process 对齐**（runtime 重启 fresh-create 从新 seq 开始）。

### 3.5 权限模型（v3：assembled composition 验证完成）

**v1 错误**：误称 jsonrpc composition 有 `DSH_PERMISSION_MODE` 策略。核实：权限栈在 `packages/bundle/base/cordis.patch.yml`（`dsh-sandbox-local`+`dsh-sandbox-policy`+`dsh-bash-sandbox`+`dsh-user-approval`+`dsh-permission-presets`），jsonrpc-agent 裸 composition 无此栈。

**v3 assembled 验证（`scripts/dsh-gate0/driver-cordis.yml`，真实 key）**：
- ✅ **可加载并跑通**：组装 sandbox/sandbox-policy/bash-sandbox/approval/permission-presets 栈，跑真实 turn（bash echo）成功
- ✅ **权限栈激活事件 verified**：turn 前 emit `permission/preset:"workspace-write"` + `sandbox/mode:"workspace-write"` + `approval/policy:"ask"`（run3）
- ✅ **fail-closed verified**：用 unconfined 的 `bash-local` + `permission-presets` 时，runtime **拒绝加载**（`the mounted bash executor does not confine — composing this plugin over an unconfined executor is a misconfiguration`）—— 证明 DSH 权限栈有严格一致性校验
- ✅ **关键组装约束**：`dsh-permission-presets` 要求 bash executor 是 **confined**（`bash-sandbox`），不能用 `bash-local`；`agent-spine-demo` 内置 todo/fs 工具，**不要重复挂** `dsh-tool-todo`/`dsh-tool-fs`（否则 `allowParallelInProgress` config 校验失败）
- ℹ️ workspace-write 下 bash 写 `/tmp` 成功 —— 因 DSH sandbox 把平台临时区列为共享可写 scratch space（设计如此，非漏洞）；精确拒绝边界是 driver 实现时细化项

**driver 必须用 §10 的专用 cordis.yml**（bash-sandbox confined + 权限栈），不能裸跑 jsonrpc-agent。一期 `permission_resolve` 仍不声明（SDK server→client request 死能力，approval 在 runtime 内消化；但 `approval/policy` 事件证实 policy 已设置）。

---

## 4. session 生命周期（live-only）

| 操作 | 一期 | 说明 |
|---|---|---|
| 创建/发送 | ✅ `session/prompt`（惰性创建） | 返回 `{messageId}` |
| 取消 | ❌ kill 进程（§5） | session 随之终止 |
| 列出/resume | ❌ **一期不支持** | SDK 无 session/list/resume；扫 JSONL 只恢复 Mac 投影，不恢复 DSH 模型上下文 |

**resume 闭环（审计 P0-2）**：runtime 重启 fresh-create，不 seed 旧对话。一期不声明 `session_history`，不展示/resume 历史，runtime 死即 session 终止。

---

## 5. 取消与关闭

**SDK 无 cancel**：取消 = kill 进程 + session 终止（live-only）。Close 三阶段（shutdown RPC → SIGTERM 进程组 → SIGKILL），复用 grokbuild `proc_unix.go`。

> v3：正常 `shutdown→{}` verified；SIGTERM/SIGKILL 进程组回收路径仍待实测（审计 🟡）。

---

## 6. 错误分类

JSON-RPC error response / stdout 解码失败 / 进程意外退出 / 未知非 ignorable event → `EventError` + 降级。`initialize` 失败 → descriptor `not_detected`。

> v3 🔴：JSON-RPC error、坏 JSON、超长行**无样本**（难触发），driver 实现时补。

---

## 7. 敏感信息脱敏

- `tool/call.arguments`/`tool/result` 正文不全广播，只提取 title/kind/status/locations。
- **iOS 不做消息内文件级 diff**：`tool/result.meta` 的 `FsDiffMeta.diffs`（实测为空 `[]`，无非空实样）不映射 inline diff；文件变更走 session 级 diff bar。
- `DEEPSEEK_API_KEY` 不进日志/dump（v3 dumps 已确认无 key）。

---

## 8. capability 与 WireDescriptor

### WireDescriptor（v2 修正）

```go
&core.WireDescriptor{
    Kind: "deepseek", DisplayName: "DeepSeek",
    LiveEventModel: core.LiveEventSessionProcess,   // owned-process，非 broadcast
    RequiresExternalTurnPolling: false,              // = 不支持外部 turn 观察
    StaticCapabilities: nil,                          // 不声明 external_turn_streaming
}
```

### capability → interface（v3：按证据调整）

| capability | interface | v3 决策 | 依据 |
|---|---|---|---|
| `session_state` | `core.Agent` | ✅ | session/prompt + session.event |
| `workspace_diff` | `core.WorkDirSwitcher` | ✅ | DSH_CWD |
| `diagnostics` | `core.DiagnosticsProvider` | ✅ | exe 探测 + initialize |
| `external_turn_streaming` | — | ❌ | owned-process |
| `todos` | `core.TodoProvider` | ⚠️ **可声明，但须先实现 `FetchTodos` 持久化读路径** | `todo/write` event verified；审计 P1-5：仅 live 映射不满足接口 |
| `usage_reporting` | `core.TokenUsageReporter` | ⚠️ **可声明，但须先实现跨 session 聚合/去重** | `assistant/message.usage` 结构 verified（含 reasoningTokens）；审计 P1-4 |
| `session_history` | HistoryProvider | ❌ 一期不声明 | live-only（§4） |
| `permission_resolve` | ToolAuthorizer | ❌ | SDK server→client request 死能力 |
| `model_switch` | ModelSwitcher | ⚠️ 待定 | DSH 无 listModels |
| `session_pin` | SessionPinner | ⚠️ 可选 | pinstore.FromOpts |
| `supports_checkpoint` | CheckpointProvider | ❌ | live-only |

`deriveBackendCapabilities` 硬编码分支：DSH id 不匹配任一特判，**无需改**。

---

## 9. protocol 决策

**无 bridge-v1 protocol change。** DSH SDK 事件全部能用现有 bridge event 表达；driver↔runtime 的 SDK JSON-RPC 不进 bridge wire。仍须按双仓规则更新 canonical protocol compatibility pack + iOS mirror（审计 P1-6）。

---

## 10. 文件结构（v3：cordis.yml 已 assembled 验证）

```
agent/deepseek/
├── deepseek.go, session.go, sdk_codec.go, sdk_types.go
├── wire_descriptor.go, diagnostics.go, session_pin.go
├── proc_unix.go / proc_windows.go   # 从 grokbuild 复制
├── config/cordis.yml                # 见下（已 assembled 验证）
└── *_test.go
```

### driver `config/cordis.yml`（v3 verified，见 `scripts/dsh-gate0/driver-cordis.yml`）

```yaml
- id: sdk-jsonrpc-server
  name: '@deepseek-ai/dsh-sdk-jsonrpc-server'
- id: llm-deepseek
  name: '@deepseek-ai/dsh-llm-deepseek'
  config: { thinking: enabled, reasoningEffort: max }
- id: subprocess
  name: '@deepseek-ai/dsh-subprocess-local'
# --- permission stack (bundle/base) ---
- id: sandbox
  name: '@deepseek-ai/dsh-sandbox-local'
- id: sandbox-policy
  name: '@deepseek-ai/dsh-sandbox-policy'
  config:
    mode: !!js process.env.DSH_PERMISSION_MODE ?? 'workspace-write'
    workspaceRoot: !!js process.env.DSH_CWD ?? process.cwd()
- id: bash-sandbox              # MUST be confined (bash-local 被 permission-presets 拒绝)
  name: '@deepseek-ai/dsh-bash-sandbox'
  config: { timeoutMs: 60000 }
- id: shell-env
  name: '@deepseek-ai/dsh-shell-env'
- id: tool-bash
  name: '@deepseek-ai/dsh-tool-bash'
- id: approval
  name: '@deepseek-ai/dsh-user-approval'
  config:
    policy: !!js "(process.env.DSH_PERMISSION_MODE ?? 'workspace-write') === 'danger-full-access' ? 'never' : 'ask'"
- id: permission
  name: '@deepseek-ai/dsh-permission-presets'
  config:
    presets:
      read-only: { sandbox: read-only, approval: ask }
      workspace-write: { sandbox: workspace-write, approval: ask }
      danger-full-access: { sandbox: danger-full-access, approval: never }
# --- spine + persistence（不要重复挂 tool-todo/tool-fs，agent-spine 内置）---
- id: agent-spine
  name: '@deepseek-ai/dsh-agent-spine-demo'
  config: { persona: !!js process.env.DSH_SYSTEM_PROMPT ?? 'You are a coding agent.', workspaceContext: false, skills: { enabled: false }, toolBash: { enableRunInBackground: false }, toolJobs: false }
- id: sessions
  name: '@deepseek-ai/dsh-session-persistence-jsonl'
  config: { root: !!js process.env.DSH_SESSION_ROOT ?? './.sessions', compression: none }
- id: session-checkpoints
  name: '@deepseek-ai/dsh-session-checkpoint-policy'
- id: subagent
  name: '@deepseek-ai/dsh-subagent'
- id: subagent-spawn-in-process
  name: '@deepseek-ai/dsh-subagent-spawn-in-process'
  config: { providerName: spawn }
- id: tool-subagent
  name: '@deepseek-ai/dsh-tool-subagent'
  config: { provider: spawn }
```

**v3 组装要点（已 verified）**：① bash 用 `bash-sandbox`（confined），非 `bash-local`；② 不重复挂 `tool-todo`/`tool-fs`（agent-spine 内置，重复挂触发 `allowParallelInProgress` 校验失败）；③ `compression: none` 便于 Go 读 JSONL。

**与 grokbuild 复用**：`proc_unix.go`/Close/`BuildAgentEnv` 复制；`acp_codec.go` 不复用；不需要 catalog 子进程。

---

## 11. 三仓改动面

### A. MacBridge driver（`agent/deepseek/`）—— §10
### B. go-bridge：`main.go`（import+alias+opts）、`agent_descriptor.go`（kind/displayName/detectDshRuntime）、`handlers.go`（handleSendMessage dispatch；**无** list/resume dispatch）；不需 catalog
### C. iOS + remote-web：`BackendModels.swift`（`BackendKind.deepSeek` + `fromWireKind:"deepseek"`）、`CCCodeBridgeBackendClient.swift`、remote-web 识别；**iOS 不展示 DSH 历史/resume**（live-only）

---

## 12. 证据（v3：4 次真实 run + Gate 0）

脚本 + dump：`scripts/dsh-gate0/`。pinned：DSH `47f943859bef60e4160492346772ded9b24f765a`，node v25.9.0，go1.26.2。

| Run | composition | prompt 意图 | 捕获（distinct types） | 关键 verified |
|---|---|---|---|---|
| Gate0 | jsonrpc-agent | mock canned turn | 14 | 协议连通（mock） |
| run1 | jsonrpc-agent | todo+bash+write | 14 | **todo/write, tool/call, tool/result(双层+isError), reasoning-delta chunk, chunk 7 discriminant** |
| run2 | jsonrpc-agent | 长文 maxTokens=24 | 11 | **turn/end(max-tokens), usage(reasoningTokens,cacheReadTokens)** |
| run3 | **driver-cordis.yml** | bash echo | 16 | **§10 可加载；permission/preset+sandbox/mode+approval/policy 激活** |
| run4 | driver-cordis.yml | 写 /tmp | 16 | bash 写临时区（sandbox 允许） |

**fail-closed 证据**：`bash-local`(unconfined) + `permission-presets` → runtime 拒绝加载（`does not confine ... misconfiguration`）。

**覆盖**：44 种事件中 **17 种真实 verified**（含权限栈 3 种）。剩余 27 种需特殊触发（compaction 需长对话、approval/asked 需审批操作、error/blocked 需模型失败、goal/plan 需专用工具）。

---

## 13. 风险与回退

1. **pre-release 漂移**：协议 `0.0.1` 无协商。缓解：forward-compat + pin runtime。
2. **取消即 session 终止**：无 cancel。
3. **无 session list/resume**：live-only。
4. **权限**：approval 不经协议暴露（iPhone 无 per-call 授权）；runtime sandbox/approval 由 §10 cordis.yml 控制（v3 verified 可加载+激活+fail-closed）。
5. **DeepSeek 绑定**。
6. **证据缺口**：compaction/approval-asked/error 终态/FsDiff 非空 等特殊触发项待补（见 §14）。

---

## 14. 修订记录

| 项 | v1 问题 | v2 修订 | v3 进展 |
|---|---|---|---|
| P0-1 | descriptor 误报 broadcast/external_turn_streaming | 改 LiveEventSessionProcess | ✅ |
| P0-2 | resume 未闭环 | live-only | ✅ |
| P0-3 | 权限 composition 不符 | 写明 bundle/base 栈 | ✅ **assembled 验证完成**（可加载+激活+fail-closed，§3.5/§10） |
| P0-4 | 映射表过度承诺 | verified/unsupported 分级 | ✅ **17 种真实 verified**（reasoning-delta/todo/max-tokens/usage/权限栈） |
| P0-5 | Gate 0 在 /tmp | pin + 纳入仓库 | ✅ 脚本+4 run dump+assembled composition 入库 |

**v3 剩余（特殊触发条件，driver 实现时补）**：`compaction/*`（长对话）、`approval/asked`（审批操作 + SDK resolve 行为）、error/aborted/interrupted/blocked 终态（模型失败）、非空 `FsDiffMeta`、`command/*`（dsh-shell）、goal/plan/schedule（专用工具）、JSON-RPC error/坏 JSON/超长行（错误路径）、SIGTERM/SIGKILL 回收。

**当前结论**：协议选型、事件映射主体、权限 composition 均已真实证据支撑；剩余为边缘事件/错误路径/特殊触发项，不阻塞进入实现的设计评审，可在 driver 实现阶段增量补证。
