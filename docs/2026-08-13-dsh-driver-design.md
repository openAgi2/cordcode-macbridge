# DeepSeek Harness (DSH) Driver 设计文档

- **日期**：2026-08-13（v5：round3 审计 —— identity 对齐 reducer、dump 语义自洽、环境切换验证）
- **Runtime**：DeepSeek Harness `dsh-jsonrpc-agent`（pinned `47f943859bef60e4160492346772ded9b24f765a`，协议 `0.0.1`，`SESSION_FORMAT_VERSION=0`，pre-release）
- **协议**：DSH SDK JSON-RPC 2.0 over stdio（**非** ACP）
- **证据**：`scripts/dsh-gate0/`（4 次真实 run + assembled composition + sanitizer 带一致性断言）
- **审计**：round1/2/3（`docs/2026-08-13-dsh-driver-design-audit-round{1,2,3}.md`）

---

## 0. 协议选型

SDK 协议满足 rich timeline（reasoning/tool/todo/usage），ACP 显式丢弃这些。DSH `KNOWN_SESSION_EVENT_TYPES` = 44 种。

---

## 1-2. Runtime / 进程参数

`dsh-jsonrpc-agent <cordis.yml>`，强制 config。env：`DEEPSEEK_API_KEY`/`DEEPSEEK_BASE_URL`/`DSH_CWD`/`DSH_SESSION_ROOT`/`DSH_PERMISSION_MODE`（driver 须显式注入子进程，§3.5）。stdout 是协议通道；进程组复用 grokbuild。

---

## 3. 输入输出 schema

### 3.1-3.2 传输 / 握手

newline-delimited JSON-RPC 2.0。**无 cancel/session-close/session-resume/load/list**。`initialize`→`{serverInfo:{name:'deepseek-harness-sdk-runtime'}}`；`session/prompt` 惰性创建 session。

### 3.3 `session.event` → core.Event（证据标签分级）

🟢 runtime-dump / 🔵 source-schema / 🟡 composition-generated / ⚪ deferred。详见 round2 §3.3 表（不变）。关键：`assistant/message` 🟢 但**只校验不追加内容**（§3.7）；`turn/end` completed+max-tokens 🟢，其余 ⚪；`isError:true`/非空 FsDiff/`todo.in_progress` ⚪。

### 3.4 turn 生命周期

`turn/start`+`turn/end`（reason 6 态）+ `session.status`（running/idle）。`session/prompt` 只回 `{messageId}`，turn 完成只认 `turn/end`。

### 3.5 权限模型（v5：环境切换 verified）

权限栈在 `packages/bundle/base/cordis.patch.yml`。assembled composition（`scripts/dsh-gate0/driver-cordis.yml`）：
- ✅ 🟢 可加载 + 激活（`permission/preset`+`sandbox/mode`+`approval/policy`）
- ✅ 🟢 **fail-closed**：unconfined `bash-local` + `permission-presets` → 拒绝加载
- ✅ 🟢 **环境切换 verified（v5 补）**：main.go 转发 `DSH_PERMISSION_MODE` 后，run3 `workspace-write`+`ask` vs run4 `danger-full-access`+`never`，runtime 真实切换 preset/mode/policy
- 组装约束：bash 用 `bash-sandbox`（confined），不重复挂 `tool-todo/tool-fs`

一期 `permission_resolve` 不声明（SDK server→client request 死能力）。

### 3.6 DSH identity → core.Event 映射矩阵（v5：对齐 reducer，round3 P0-1）

**背景**：SSV2 `ProjectionReducer` 对 text/reasoning delta **直接 `turnID := data.itemId`**（projection_reducer.go:535），且注释明确 `itemId == lifecycle turn_id == assistant message id`（L16-17）；`TurnProjection` 一个 turn 只有一个 `Assistant`。grokbuild 用同一 `promptId` 填 TurnID+ItemID 正是此因。

**v4 错误**：`ItemID="t{turn}-s{step}"` 会被 reducer 当 turnID，把一个 DSH turn 拆成 `t1`/`t1-s1`/`t1-s2` 伪 turn —— 事件不消失却形成**错误投影**（比 identityless-skip 更隐蔽）。

**v5 修正（对齐当前 reducer 模型，不改 wire/reducer）**：

| core.Event 字段 | DSH 来源 | 生成规则 | 说明 |
|---|---|---|---|
| `TurnID`（所有 event） | `data.turn` | `"t"+turn` | session 内唯一 |
| `ItemID`（assistant text/reasoning） | `data.turn` | **`"t"+turn`（== TurnID）** | reducer 要求 itemId==turnId；一个 turn 的所有 step 的 assistant content 归同一 turn 的同一 Assistant |
| `ItemID`（user message） | `user/message.data.id` | 透传 | 用户消息权威 id |
| `RequestID`（tool） | `tool/call.data.callId` | 透传 | tool_use/tool_result 共用 |
| `ItemID`（tool part） | `tool/call.data.callId` | == RequestID | tool 依 active turn（t{turn}）归属 |
| `StreamID`/`ParentStreamID` | `subagent/descriptor` | 一期 main stream（空） | deferred |

**`(turn,step)` 仅作 codec 内部 chunk/assembled 校验 key（§3.7），不进 projection identity。**

**多 step / 多 block 处理（reducer 模型约束）**：reducer 一个 turn 一个 `Assistant`。DSH 一个 turn 可含多 step（每 step 一个 assistant message）；按当前模型，同 turn 的所有 step 的 text/reasoning 用同一 `ItemID=t{turn}`，reducer 拼接到同一 turn 的 Assistant。多 content block 用 reducer 已有的 part 边界（block-start/block-end）表达，**不伪造多 turn**。

> **若产品需要 turn 内 step 级独立 assistant message**：必须把 turnId 与 itemId 分离送到 wire 并扩展 `TurnProjection`/patch/reducer —— 这是**协议 + 消费者改动**，不再是"无 bridge-v1 change"。一期不做（接受 reducer 模型约束）。

**process generation 不变量（round3 P0-1 第二点）**：`data.turn` 在新 DSH process 从 1 开始。**driver 生产不变量：cancel/runtime 重启必须换新外层 sessionId**（live-only，§4，session 终止即不复用），否则 `t1` 会在同一 projection key 复用。driver 实现须在 Close 后强制新 sessionId，不得复用。

> **reducer 级测试 deferred**（§15）：identity 矩阵须配 ProjectionReducer 测试证明 frame 不被 skip 且不形成伪 turn，属 driver 测试代码。

### 3.7 chunk 与 assembled message 双写：唯一 owner 与去重（v5：证据自洽）

**证据（v5）**：4 份真实 dump（无路径 prompt 重新生成）经 `sanitize.py` 净化后，**机器断言**：每 `(turn,step)` chunk text/reasoning 拼接 == assembled（run1 0/2、run2 0/1、run3 0/2、run4 0/4 mismatch），usage 双源（chunk usage == assembled usage）相等，无路径残留。证明 `assistant/message` 是 chunks 组装态。

**唯一 owner 规则**：

| 逻辑字段 | 唯一 owner | 另一方 |
|---|---|---|
| live text | `assistant/chunk`(text-delta) → `EventText` | `assistant/message` 只校验 |
| live reasoning | `assistant/chunk`(reasoning-delta) → `EventThinking` | 同上 |
| usage | **`assistant/chunk`(usage) 一处写** | `assistant/message.usage` 不重复 |
| tool start | `tool/call` → `EventToolUse` | `tool-call-delta` 只组装 arguments |
| tool result | `tool/result` → `EventToolResult` | — |
| assembled 校验 | `assistant/message` 到达校验 拼接==assembled | 不发新 Event |

**usage 字段映射（v5 补，round3 P1-3）**：DSH usage = `{inputTokens, outputTokens, cacheReadTokens, reasoningTokens}` → `core.ContextUsage`：
- `InputTokens` ← inputTokens；`OutputTokens` ← outputTokens
- `CachedInputTokens` ← cacheReadTokens；`ReasoningOutputTokens` ← reasoningTokens
- `UsedTokens` ← inputTokens+outputTokens；`TotalTokens`/`ContextWindow` ← `request/context.contextWindow`
- 唯一 owner = chunk usage（`EventContextUsageUpdated`），assembled usage 不重复写

**4 种情况（标签修正）**：
1. **正常流**（chunk→…→assembled→turn/end）：🟢 有样本，chunk 发 text/reasoning/usage，assembled 校验。
2. **无 delta 但有 assembled**（模型一次性返回）：⚪ **无样本，防御性设计** —— chunk 缺失时 assembled 降级为 owner（用 §3.6 ItemID），实现时须有测试。
3. **重复 frame**：⚪ 无样本 —— codec 用 `processed-seq` set 去重（重复 seq 丢弃）；**去重 ≠ 乱序重排**，二者分开。
4. **乱序/gap**：⚪ 无样本 —— gap/buffer/flush 顺序规则**未定义**，实现时须设计 + 测试。

> §3.7 不再宣称 assembled-only/重复/乱序为"已观测模式"，均标 ⚪ deferred。

---

## 4. session 生命周期（live-only）

| 操作 | 一期 | 说明 |
|---|---|---|
| 创建/发送 | ✅ `session/prompt` | `{messageId}` |
| 取消 | ❌ kill 进程 | session 终止，sessionId 不复用（§3.6 不变量） |
| 列出/resume | ❌ | live-only |

**`Agent.ListSessions` Go 契约（v5 固定，round3 P1-1）**：返回 `nil, core.ErrNotSupported`（wrapped，非二选一）。wire 层映射为确定的 unsupported error（不伪造空成功，不返回空 slice 假装成功）。iOS 侧不展示 DSH 历史列表。

---

## 5-7. 取消关闭 / 错误 / 脱敏

Close 三阶段（shutdown→SIGTERM→SIGKILL）；正常 `shutdown→{}` 🟢，SIGTERM/SIGKILL 回收 ⚪。错误 → `EventError` + fail visibly。脱敏：dumps 经 `sanitize.py` 净化（递归 scrub + 一致性断言），无 key/无本机路径。

---

## 8. capability

`LiveEventSessionProcess`，无 `external_turn_streaming`。声明：`session_state`/`workspace_diff`/`diagnostics` 🟢；`todos`/`usage_reporting` ⚠️ 须先实现持久化读/聚合；`session_history`/`permission_resolve`/`supports_checkpoint` ❌。

---

## 9-11. protocol / 文件 / 三仓

无 bridge-v1 change（**除非**产品要 turn 内多 assistant message，则协议改动，§3.6）。driver `config/cordis.yml` 见 `scripts/dsh-gate0/driver-cordis.yml`。go-bridge：`BuildAgentEnv` 显式注入 `DSH_PERMISSION_MODE`；iOS：`BackendKind.deepSeek`，不展示历史。

---

## 12. 证据（v5：新 dumps + 环境切换 + 一致性断言）

pinned `47f943859bef60e4160492346772ded9b24f765a`，node v25.9.0，go1.26.2。

| Run | composition | env | distinct | 关键 |
|---|---|---|---|---|
| Gate0(mock) | jsonrpc-agent | — | 11 | 连通 |
| run1 | jsonrpc-agent | — | 14 | todo/write, tool/call, tool/result, reasoning-delta |
| run2 | jsonrpc-agent | maxTokens=24 | 11 | turn/end(max-tokens), usage |
| run3 | driver-cordis.yml | **workspace-write** | 16 | §10 可加载；preset=workspace-write, mode=workspace-write, policy=ask |
| run4 | driver-cordis.yml | **danger-full-access** | 16 | **环境切换**：preset=danger-full-access, mode=danger-full-access, policy=**never** |
| union | — | — | 17 | runtime-dump verified |

**fail-closed** 🟢：`bash-local`(unconfined)+`permission-presets` → 拒绝加载。
**环境切换** 🟢（v5）：run3 vs run4 preset/mode/policy 真实切换（main.go 转发 `DSH_PERMISSION_MODE`）。
**dump 自洽** 🟢（v5）：`sanitize.py` 净化后机器断言 chunk==assembled（text/reasoning）、usage 双源相等、无路径 —— ALL PASS。
**计数**：裸 mock=11，union=17。

**剩余 ⚪**：compaction/approval-asked/error 终态/非空 FsDiff/command/tool-workflow/goal-plan-schedule/assembled-only/乱序/SIGTERM 回收/JSON-RPC error。

---

## 13. 风险

1. pre-release 漂移；2. 取消即 session 终止；3. 无 list/resume；4. approval 不经协议；5. DeepSeek 绑定；6. reducer 模型约束（turn 内多 step 拼接到一个 Assistant）；7. ⚪ 证据缺口。

---

## 14. 修订记录

### Round3（v4→v5）
| 项 | v4 问题 | v5 修订 | 状态 |
|---|---|---|---|
| P0-1 | ItemID=t{turn}-s{step} 与 reducer 冲突（拆伪 turn） | **ItemID==TurnID==t{turn}**；(turn,step) 仅 codec 内部；多 step 用 part 边界；process generation 不变量 | ✅ 设计对齐 reducer；reducer 测试 deferred |
| P0-2 | sanitizer 破坏 chunk==assembled（4/8 mismatch） | 无路径 prompt 重生成 dumps + sanitizer 递归 scrub + 一致性断言（ALL PASS） | ✅ |
| P1-1 | §15 谎称已改 env/覆盖写 | main.go 实际修复（见下）+ §15 如实 | ✅ |
| P1-2 | ListSessions 二选一 | 固定 `nil, core.ErrNotSupported` | ✅ |
| P1-3 | usage cache/reasoning 映射未定义 | §3.7 ContextUsage 字段映射 | ✅ |
| P1-4 | assembled-only/重复/乱序混淆 | 分别标 ⚪ deferred；去重≠重排 | ✅ |
| P0-3(环境切换) | 只证明 YAML 默认 | run3/run4 真实切换 workspace-write↔danger-full-access | ✅ |

### Round1/2 闭环：见前版（P0-1 descriptor / P0-2 resume / P0-3 权限 / P0-4 计数 / P0-5 Gate0 / round2 identity雏形 / 双写 owner）。

---

## 15. 不采纳 / deferred 说明

| 审计建议 | 处理 | 原因 |
|---|---|---|
| 配 `ProjectionReducer` 级测试 | deferred | driver 测试代码；本次限定"优化文档不写 driver"。identity 已对齐 reducer 源码语义（projection_reducer.go:535 + 注释 L16-17），测试实现时补 |
| Gate helper 完整 CI gate（malformed 断言、dispatcher join） | 部分采纳 | v5 已修关键：`DSH_PERMISSION_MODE`/`DSH_SYSTEM_PROMPT` 转发、dump `O_TRUNC` 覆盖、`sendAndWait` 先注册后写（消除竞态）、JSON-RPC error 捕获、PARTIAL 不再静默成功。完整 malformed/join 属 driver 测试基建 |
| fail-closed durable fixture + 机器断言 | deferred | 行为 🟢 真实验证；durable fixture 属测试基建 |
| round1 P1-6 protocol pack + iOS mirror 同步 | deferred | 跨仓 protocol 改动，driver 实现时做（**除非**要 turn 内多 assistant message，§3.6） |

**main.go v5 实际修复（P1-1）**：① `DSH_PERMISSION_MODE`/`DSH_SYSTEM_PROMPT` 转发子进程；② dump `O_TRUNC` 覆盖（不再 append）；③ `sendAndWait` 先 `regPending` 再写 stdin（消除快速响应竞态）；④ JSON-RPC error response 捕获 + 影响 VERDICT；⑤ PARTIAL 不静默成功。

**当前结论**：协议选型、事件映射、**identity（对齐 reducer）**、双写去重（证据自洽）、权限 composition（含环境切换）五块设计完整且有真实证据。剩余为 driver 实现代码（测试/fixture/protocol 同步）与特殊触发 dump。
