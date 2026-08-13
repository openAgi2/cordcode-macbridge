# DeepSeek Harness (DSH) Driver 设计文档

- **日期**：2026-08-13（v4：落实 round2 审计 —— identity 矩阵、双写去重、dump 净化、计数修正）
- **Runtime**：DeepSeek Harness `dsh-jsonrpc-agent`（pinned commit `47f943859bef60e4160492346772ded9b24f765a`，协议 `0.0.1`，`SESSION_FORMAT_VERSION=0`，pre-release 无兼容承诺）
- **协议**：DSH SDK JSON-RPC 2.0（newline-delimited）over stdio —— **不是** ACP
- **入口**：spawn `dsh-jsonrpc-agent <cordis.yml>`
- **证据**：`scripts/dsh-gate0/`（脚本 + 4 次真实 run dump + assembled composition + sanitizer）
- **审计**：round1（3 P0，v2/v3 修订）、round2（`docs/2026-08-13-dsh-driver-design-audit-round2.md`，REQUEST CHANGES → v4）

---

## 0. 协议选型：为什么不用 ACP

DSH 同时实现两条 stdio JSON-RPC 协议，**只有 SDK 协议满足 rich timeline**：

| 维度 | ACP server | **SDK server ✅** |
|---|---|---|
| thinking/reasoning | ❌ 显式丢弃 | ✅ `reasoning-delta` chunk（runtime-dump verified） |
| tool calls / 结果 | ❌ 丢弃 | ✅ `tool/call` + `tool/result`（runtime-dump verified） |
| todos / usage / turn | ❌ 丢弃 | ✅（runtime-dump verified） |

DSH `KNOWN_SESSION_EVENT_TYPES` = **44 种**（pinned commit 枚举）。

---

## 1. Runtime 版本范围

pre-release，`serverInfo.version` 恒 `0.0.1`，无协议版本协商。检测靠 `serverInfo.name === 'deepseek-harness-sdk-runtime'`。`SESSION_FORMAT_VERSION=0` 无兼容承诺。forward-compat：未知 `event.type` 带 `ignorable:true` 可跳过，否则拒绝重建。

---

## 2. 进程参数

```
dsh-jsonrpc-agent <path/to/cordis.yml>
```

强制要求显式 config（argv 或 `$DSH_CORDIS_CONFIG`）。config 决定**工具面 + 权限栈**（§3.5）。

| 变量 | 用途 |
|---|---|
| `DEEPSEEK_API_KEY` / `DEEPSEEK_BASE_URL` | API 凭据 / endpoint |
| `DSH_CWD` | agent 工作目录 |
| `DSH_SESSION_ROOT` | JSONL 持久化根 |
| `DSH_PERMISSION_MODE` | 权限档位（**driver 须显式注入子进程，§3.5**；仅当 cordis.yml 挂载权限栈时生效） |

stdout 是协议通道；进程组复用 grokbuild `prepareCmdForProcessGroup`。

---

## 3. 输入输出 schema

### 3.1 传输层

newline-delimited JSON-RPC 2.0。**SDK 协议无 cancel、无 session/close、无 session/resume/load/list**；server→client request 是死能力。

### 3.2 初始化握手

`initialize`（`{cwd, provider, model, maxTokens?}`）→ `{serverInfo:{name:'deepseek-harness-sdk-runtime', version:'0.0.1'}}`（runtime-dump verified）。无 authenticate。`session/prompt` 带 `sessionId`，runtime 对未知 id 惰性创建。

### 3.3 `session.event` → core.Event 映射（按证据标签分级）

`params.event` = 完整 `SessionEvent` 信封（`{type, seq, time, data, surfaceOp?, sourceEventSeqs?, ignorable?}`）。**证据标签**（round2 P1-5）：🟢 runtime-dump verified / 🔵 source-schema verified / 🟡 composition-generated / ⚪ deferred。

| DSH `event.type` | 标签 | core.Event | 真实 payload 关键字段 |
|---|---|---|---|
| `turn/start` | 🟢 | `EventTurnStarted` | `data.turn` |
| `turn/end`(completed) | 🟢 | 终态 `EventResult` | `data.reason.kind:"completed"` |
| `turn/end`(max-tokens) | 🟢 | token 限制完成 | `data.reason.kind:"max-tokens"` |
| `turn/end`(error/aborted/interrupted/blocked) | ⚪ | 终态 | 无样本，deferred |
| `step/start`·`step/end` | 🟢 | 内部边界 | `data.turn, data.step` |
| `user/message` | 🟢 | `EventUserMessage` | `data.content[], data.id, data.source.kind` |
| `assistant/chunk`(text-delta) | 🟢 | `EventText` | `data.chunk.text` |
| `assistant/chunk`(reasoning-delta) | 🟢 | `EventThinking` | `data.chunk.text` |
| `assistant/chunk`(tool-call-delta) | 🟢 | **不发 tool start**（§3.7 仅组装参数） | `data.chunk` |
| `assistant/chunk`(block-start/end/usage/finish) | 🟢 | codec 状态/usage（§3.7） | `data.chunk.type` |
| `assistant/message` | 🟢 | **只校验，不追加内容**（§3.7） | `data.message.content[], data.message.id, data.usage?` |
| `tool/call` | 🟢 | `EventToolUse` | `data.callId, name, arguments` |
| `tool/result` | 🟢 | `EventToolResult` | 双层 content 嵌套, `isError`（🟢 字段存在；⚪ `isError:true` 无样本 deferred） |
| `tool/result.meta`(FsDiffMeta.diffs) | ⚪ | 不映射 inline diff | 实测 diffs=`[]`，非空样本 deferred |
| `todo/write` | 🟢 | todo 更新 | `data.todos[]`（status: pending/completed 🟢；in_progress 🔵 schema-only） |
| `permission/preset`·`sandbox/mode`·`approval/policy` | 🟡 | 控制面/诊断 | assembled composition 产物（§3.5） |
| `approval/asked`·`decided` | ⚪ | deferred | workspace-write 不触发 |
| `compaction/*` | ⚪ | deferred | 需长对话 |
| `session/title` | 🟢 | 标题 | `data.title` |
| `request/header`·`request/context` | 🟢 | context usage 输入 | `data.contextWindow` |
| `agent/inbox/spliced` | 🟢 | 内部 | prompt 入队 |
| `command/*`·`tool-workflow/*`·`goal/plan/schedule`·`session/end-seed` | ⚪ | ignored/deferred | 无样本 |

### 3.4 turn 生命周期

1. `session.event` 的 `turn/start`+`turn/end`（reason 6 态：completed+max-tokens 🟢，其余 ⚪）
2. `session.status`（running/idle 🟢）

`session/prompt` 只回 `{messageId}` 入队回执（🟢），turn 完成只认 `turn/end`。**seq 不跨 process 对齐**（runtime 重启 fresh-create）。

### 3.5 权限模型

**权限栈在 `packages/bundle/base/cordis.patch.yml`**（`dsh-sandbox-local`+`dsh-sandbox-policy`+`dsh-bash-sandbox`+`dsh-user-approval`+`dsh-permission-presets`），jsonrpc-agent 裸 composition 无此栈。

**assembled 验证（`scripts/dsh-gate0/driver-cordis.yml`，真实 key）**：
- ✅ 🟢 可加载并跑通；权限栈激活（`permission/preset:"workspace-write"`+`sandbox/mode`+`approval/policy`）
- ✅ 🟢 **fail-closed**：unconfined `bash-local` + `permission-presets` → runtime 拒绝加载（`does not confine ... misconfiguration`）
- ⚠️ round2 修正：**`DSH_PERMISSION_MODE` 当前 evidence 只证明 YAML 默认值（workspace-write），未证明环境切换**（Gate helper 未把该变量转发子进程）。driver 必须在 `BuildAgentEnv` 显式注入 `DSH_PERMISSION_MODE`（不依赖基础 allowlist），切换 read-only/workspace-write/danger-full-access；环境切换的 dump 待补
- 组装约束：bash 用 `bash-sandbox`（confined），不重复挂 `tool-todo/tool-fs`（agent-spine 内置）

一期 `permission_resolve` 不声明（SDK server→client request 死能力）。

### 3.6 DSH identity → core.Event 映射矩阵（v4：round2 P0-1）

**背景**：SSV2 ProjectionReducer 对 identity 有硬门槛 —— 空 `TurnID` 的 turn_started、空 `ItemID` 的 text/reasoning delta、空 call/item identity 的 tool event **被直接 skip**（projection_reducer.go:465/537/568/598；grokbuild acp_codec.go:194-200 注释佐证）。grokbuild 用 ACP `_meta.promptId` 同时填 TurnID+ItemID；**DSH 没有 promptId**，按下表生成稳定 identity。

| core.Event 字段 | DSH 来源 | 生成规则 | 稳定性 / 作用域 |
|---|---|---|---|
| `TurnID` | `data.turn`（session 内递增数字） | `"t"+turn`（如 `"t1"`） | session 内唯一；live-only（§4）不跨 process，重启=新 session，turn 复用不冲突 |
| `ItemID`（assistant text/reasoning） | `data.turn, data.step` | `"t{turn}-s{step}"`（如 `"t1-s1"`） | 一个 (turn,step) 的所有 assistant content（含多 block）属同一 item；chunk 与 assembled message **共用此 id**（§3.7） |
| `ItemID`（user message） | `user/message.data.id`（UUID） | 直接透传 | DSH 用户消息权威 id |
| `RequestID`（tool） | `tool/call.data.callId` | 直接透传 | per-tool 稳定；tool_use 与 tool_result 共用 |
| `ItemID`（tool part） | `tool/call.data.callId` | 同 RequestID | 与 grokbuild toolCallID 先例一致 |
| `StreamID`/`ParentStreamID` | `subagent/descriptor`（待补） | 一期 main stream（空） | deferred |

**关键决策**：
1. **TurnID=`"t{turn}"`**：加前缀避免与纯数字 id 冲突。live-only 下 turn 重启从 1 不冲突（新 session）。
2. **assistant ItemID=`"t{turn}-s{step}"`，不用 `assistant/message.data.message.id`**：chunk（无 message.id）先于 assembled message（有 message.id）到达；若 chunk 用临时 id、assembled 用 message.id，reducer 当两个 item。统一 (turn,step) 使二者落同一 item，双写去重靠 §3.7。`message.id` 仅 DSH 内部，不进 core identity。
3. **block-start/block-end**：codec 内部状态（标记 text↔reasoning block 切换），不单独发 core.Event；多 block 合并到同一 (turn,step) item。
4. **callId → RequestID + tool ItemID**：与 grokbuild 先例一致。

> **reducer 级测试 deferred**（见 §15 不采纳）：identity 矩阵须配 ProjectionReducer 测试证明 frame 不被 skip，但该项属 driver 测试代码，文档阶段不写。

### 3.7 chunk 与 assembled message 双写：唯一 owner 与去重（v4：round2 P0-2）

真实 dump 证实 `assistant/message` 是 chunks 的组装态（8/8 step：拼接 text-delta=assembled text、reasoning-delta=assembled reasoning、chunk usage=assembled usage）。若两者都写正文/reasoning/usage，确定性重复。`tool-call-delta` 后又有权威 `tool/call`，同类风险。

**唯一 owner 规则**：

| 逻辑字段 | 唯一 owner（发 core.Event 的一方） | 另一方用途 |
|---|---|---|
| live text | `assistant/chunk`(text-delta) → `EventText` | `assistant/message` 只校验，**不追加 text** |
| live reasoning | `assistant/chunk`(reasoning-delta) → `EventThinking` | `assistant/message` 只校验 |
| usage（token） | **`assistant/chunk`(usage) 一处写**（`InputTokens`/`OutputTokens`） | `assistant/message.usage` 不重复写（选 chunk 作 owner：更早到达，支持 live 计费） |
| tool start | `tool/call` → `EventToolUse`（权威） | `tool-call-delta` **只组装 arguments**，不发 tool start |
| tool result | `tool/result` → `EventToolResult` | — |
| assembled 校验 | `assistant/message` 到达时校验 拼接==assembled | 一致→确认 item 完成；不一致→记日志，以 chunk 为准（不发新 Event） |

**4 种情况覆盖**：
1. **正常流**（chunk→…→assistant/message→turn/end）：chunk 发 text/reasoning/usage，assembled 校验，turn/end 收口。
2. **无 delta 但有 assembled message**（模型一次性返回）：chunk 缺失时，`assistant/message` **降级为 owner**（发 text/reasoning/usage），用 §3.6 ItemID。
3. **重复 frame**（重放/乱序）：按 `(sessionId, seq)` 去重 —— codec 记录已处理 seq，重复丢弃；同 (turn,step) chunk 按 seq 单调追加。
4. **缺失 assembled message**（turn/end 前断连）：chunk 已发的 text/reasoning 保留（item=running），turn/end 收口；assembled 校验跳过。

---

## 4. session 生命周期（live-only）

| 操作 | 一期 | 说明 |
|---|---|---|
| 创建/发送 | ✅ `session/prompt`（惰性创建） | 返回 `{messageId}` |
| 取消 | ❌ kill 进程（§5） | session 终止 |
| 列出/resume | ❌ **一期不支持** | SDK 无 session/list/resume；扫 JSONL 只恢复 Mac 投影，不恢复 DSH 模型上下文 |

**`Agent.ListSessions` live-only 返回（round2 P1-4）**：driver 实现 `core.Agent.ListSessions` 时，**不得伪造空成功**。一期返回**确定的空列表 + 明确 reason**（`live-only backend, no persistent catalog`），或对调用方标注 unsupported；iOS 侧不展示 DSH 历史列表。这保证不伪造数据（`SecurityAndTruthfulnessTests` 护栏）。

---

## 5. 取消与关闭

**SDK 无 cancel**：取消 = kill 进程 + session 终止。Close 三阶段（shutdown RPC → SIGTERM 进程组 → SIGKILL），复用 grokbuild `proc_unix.go`。正常 `shutdown→{}` 🟢；SIGTERM/SIGKILL 回收路径 ⚪（driver 实现时补）。

---

## 6. 错误分类

JSON-RPC error / stdout 解码失败 / 进程意外退出 / 未知非 ignorable event → `EventError` + 降级。`initialize` 失败 → descriptor `not_detected`。⚪ JSON-RPC error、坏 JSON、超长行、未知 discriminant 无样本，codec 必须 **fail visibly**（不得静默伪成功）。

---

## 7. 敏感信息脱敏

`tool/call.arguments`/`tool/result` 正文不全广播。**iOS 不做消息内 diff**：FsDiffMeta（实测 diffs=`[]`）不映射 inline diff，走 session 级 diff bar。`DEEPSEEK_API_KEY` 不进日志/dump（已确认 dumps 无 key）。**已提交 dumps 经 `scripts/dsh-gate0/sanitize.py` 净化**（cwd/临时目录→`<CWD>`/`<TMPFILE>` 占位符，round2 P0-3）。

---

## 8. capability 与 WireDescriptor

```go
&core.WireDescriptor{
    Kind: "deepseek", DisplayName: "DeepSeek",
    LiveEventModel: core.LiveEventSessionProcess,   // owned-process
    RequiresExternalTurnPolling: false,              // 不支持外部 turn 观察
    StaticCapabilities: nil,
}
```

| capability | 决策 | 标签 |
|---|---|---|
| `session_state` | ✅ | 🟢 |
| `workspace_diff` | ✅ | 🟢 |
| `diagnostics` | ✅ | 🟢 |
| `todos` | ⚠️ 须先实现 `FetchTodos` 持久化读路径 | `todo/write` 🟢，FetchTodos ⚪ |
| `usage_reporting` | ⚠️ 须先实现跨 session 聚合/去重 | usage shape 🟢，聚合 ⚪ |
| `session_history`/`list`/`resume` | ❌ live-only | — |
| `permission_resolve` | ❌ | SDK 死能力 |
| `external_turn_streaming` | ❌ | owned-process |
| `supports_checkpoint` | ❌ | live-only |

`deriveBackendCapabilities` 硬编码分支：DSH id 不匹配任一特判，无需改。

---

## 9. protocol 决策

无 bridge-v1 protocol change。仍须按双仓规则更新 canonical protocol compatibility pack + iOS mirror（round1 P1-6）。

---

## 10. 文件结构 + driver `config/cordis.yml`

见 `scripts/dsh-gate0/driver-cordis.yml`（assembled 验证通过）。要点：bash 用 `bash-sandbox`（confined），不重复挂 `tool-todo/tool-fs`（agent-spine 内置），`compression:none` 便于读 JSONL。与 grokbuild 复用：`proc_unix.go`/Close/`BuildAgentEnv`；`acp_codec.go` 不复用；不需 catalog。

---

## 11. 三仓改动面

A. MacBridge driver（`agent/deepseek/`，§10）；B. go-bridge（`main.go` import+alias+opts、`agent_descriptor.go`、`handlers.go` handleSendMessage；**无** list/resume dispatch；**`BuildAgentEnv` 显式注入 `DSH_PERMISSION_MODE`**）；C. iOS + remote-web（`BackendKind.deepSeek`、不展示历史/resume）。

---

## 12. 证据（round2 修正计数 + 标签分级）

脚本 + dump：`scripts/dsh-gate0/`。pinned：DSH `47f943859bef60e4160492346772ded9b24f765a`，node v25.9.0，go1.26.2。

| Run | composition | 捕获 distinct types | 关键 verified |
|---|---|---|---|
| Gate0(mock) | jsonrpc-agent | **11**（round2 修正：v3 误写 14） | 协议连通 |
| run1 | jsonrpc-agent | 14 | todo/write, tool/call, tool/result, reasoning-delta, chunk 7 discriminant |
| run2 | jsonrpc-agent(maxTokens=24) | 11 | turn/end(max-tokens), usage |
| run3 | **driver-cordis.yml** | 16 | §10 可加载；permission/sandbox/approval 激活 |
| run4 | driver-cordis.yml | 16 | bash 写临时区（sandbox 允许） |
| **union** | — | **17** | runtime-dump verified |

**fail-closed** 🟢：`bash-local`(unconfined)+`permission-presets` → 拒绝加载。

**计数澄清（round2 P0-4）**：裸 Gate 0 mock = **11 类**（不是 14）；assembled composition run = 16 类；四次 run **union = 17 类**。单次覆盖 ≠ union。

**dump 净化（round2 P0-3）**：已提交 dumps 经 `sanitize.py` 处理，`/private/var/folders/.../dsh-conn-cwd-NNN`→`<CWD>`、`/tmp/dsh-*.txt`→`<TMPFILE>`，保留字段形状；净化后广 grep 验证无本机路径。

**剩余 27 类**：compaction/approval-asked/error 终态/非空 FsDiff/command/tool-workflow/goal-plan-schedule 等，需特殊触发（长对话/审批操作/模型失败/专用工具），deferred（§3.3 ⚪）。

---

## 13. 风险

1. pre-release 漂移；2. 取消即 session 终止；3. 无 list/resume（live-only）；4. approval 不经协议（iPhone 无 per-call 授权）；5. DeepSeek 绑定；6. ⚪ 证据缺口（特殊触发项）。

---

## 14. 修订记录

### Round1（v1→v2→v3）
| 项 | v1 问题 | 闭环 |
|---|---|---|
| P0-1 | descriptor 误报 | ✅ v2 LiveEventSessionProcess |
| P0-2 | resume 未闭环 | ✅ v2 live-only |
| P0-3 | 权限 composition 不符 | ✅ v3 assembled 验证（⚠️ 环境切换待补） |
| P0-4 | 映射过度承诺 | ✅ v3 分级（v4 标签化） |
| P0-5 | Gate 0 在 /tmp | ✅ v2 纳入仓库 |

### Round2（v3→v4）
| 项 | 问题 | v4 修订 | 状态 |
|---|---|---|---|
| P0-1 | 缺 identity 映射，reducer 丢弃 | §3.6 identity 矩阵（TurnID/ItemID/RequestID 生成规则+稳定性+作用域） | ✅ 设计完成；reducer 测试 deferred §15 |
| P0-2 | chunk/assembled 双写 | §3.7 唯一 owner + 去重 + 4 种情况 | ✅ |
| P0-3 | dump 含本机路径 | `sanitize.py` 净化 + README 修正 | ✅ |
| P0-4 | Gate 0 计数 14≠11 | §12 修正 + union 澄清 | ✅ |
| P1-2 | DSH_PERMISSION_MODE 未注入 | §3.5/§11 说明 driver 显式注入；环境切换 dump 待补 | ⚠️ 设计完成，dump 待补 |
| P1-4 | ListSessions 返回 | §4 live-only 确定空+reason | ✅ |
| P1-5 | 标签分级 | §3.3 四类标签 | ✅ |

---

## 15. 不采纳 / 部分采纳说明

| 审计建议 | 处理 | 原因 |
|---|---|---|
| P0-1 配 `ProjectionReducer` 级测试证明 frame 不被 skip | **deferred** | 属 driver 测试代码；本次任务限定"优化设计文档、不写 driver 代码"。identity 矩阵已定义到可实现，测试在 driver 实现阶段补 |
| P1-1 Gate helper 全面修复（响应竞态 send/waitResp、RPC error 判定、非零失败退出、覆盖写、dispatcher join、malformed 不静默） | **部分采纳** | 本次修关键项（`DSH_PERMISSION_MODE` 转发、dump 覆盖写、净化）；完整竞态/join/error 断言属 driver 测试基建，实现时补。helper 现状足以产出 dump 证据，但尚非严格 CI gate |
| P1-3 提交 fail-closed fixture + 机器可断言期望错误 | **deferred** | fail-closed 行为已 🟢 真实验证（错误信息记录在 §3.5）；durable fixture + 断言属 driver 测试基建 |
| round1 P1-6 更新 canonical protocol pack + iOS mirror | **deferred** | 属 driver 实现的跨仓同步（protocol pack 改动），文档阶段不动 protocol |

**当前结论**：协议选型、事件映射、identity 矩阵、双写去重、权限 composition 五块设计已完整且有真实证据支撑。剩余为 driver 实现代码（测试/fixture/protocol 同步）与特殊触发 dump，不阻塞设计评审进入实现。
