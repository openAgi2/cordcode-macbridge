# DeepSeek Harness (DSH) Driver 设计文档

- **日期**：2026-08-14（v9：round7 审计 —— notification session scope、at-most-once delivery、typed process death、attachments、ignorable policy）
- **Runtime**：DeepSeek Harness `dsh-jsonrpc-agent`（pinned `47f943859bef60e4160492346772ded9b24f765a`，协议 `0.0.1`，pre-release）
- **协议**：DSH SDK JSON-RPC 2.0 over stdio（**非** ACP），3 requests + 4 notifications（§3.0）
- **证据**：`scripts/dsh-gate0/`（4 次真实 run + assembled composition + sanitizer 带 peer 断言）
- **审计**：round1-4（`docs/2026-08-13-dsh-driver-design-audit-round{1,2,3,4}.md`）、round5/6（`docs/2026-08-1{3,4}-dsh-driver-design-audit-round{5,6}.md`）、round7（`docs/2026-08-14-dsh-driver-design-audit-round7.md`）

---

## 0. 协议选型

SDK 协议满足 rich timeline（reasoning/tool/todo/usage），ACP 显式丢弃。`KNOWN_SESSION_EVENT_TYPES`=44。

---

## 1-2. Runtime / 进程参数

`dsh-jsonrpc-agent <cordis.yml>`，强制 config。env：`DEEPSEEK_API_KEY`/`DEEPSEEK_BASE_URL`/`DSH_CWD`/`DSH_SESSION_ROOT`/`DSH_PERMISSION_MODE`（driver 显式注入）。stdout 是协议通道；进程组复用 grokbuild。

---

## 3. 输入输出 schema

### 3.0 Wire surface inventory（round7 P0-1 基线）

Pinned `packages/sdk/protocol/src/types.ts:50-104` 明确 3 个 request + 4 个 notification。Driver 的 §3 映射必须以这张顶层 inventory 为骨架，不能只处理 `session.event`：

| 方向 | method | shape | session scope 要求 |
|---|---|---|---|
| client→server | `initialize` | `cwd/provider/model/maxTokens?` → `{serverInfo:{name,version}}` | — |
| client→server | `session/prompt` | `sessionId/contentBlocks[]` → `{messageId}` | root scope（§3.6） |
| client→server | `shutdown` | empty → `{}` | — |
| server→client | `session.event` | `{sessionId,event}`；**runtime 中每个 session**（source: `types.ts:51-53`） | **必须按 sessionId 过滤**（§3.8） |
| server→client | `session.status` | `{sessionId,status:idle\|running}` | 只在 `sessionId==rootSessionID` 时影响 root runtime（§3.4, §3.8） |
| server→client | `subagent.started` | `{parentSessionId,childSessionId}` | 建 lineage，child event 不进 root codec（§3.8） |
| server→client | `subagent.finished` | `{provider,agentId,parentSessionId,childSessionId,status,stopReason,lastAssistantMessage?}` | 清 lineage（§3.8） |

源码证据链（round7）：
1. `types.ts:50-56`：`session.event` 覆盖"every session in the runtime"。
2. `server.ts:71-103`：server 对 `session/event`、`agent/status`、`session/created`→`subagent.started`、`subagent/end`→`subagent.finished` 注册全局广播，**没有自动限制为 SDK root session**。
3. `client.ts:354-371`：SDK client 明确说明 scoping 是 **client-side**，提供 `subscribeSessionTree` 建 lineage 过滤。
4. `sdk-client.spec.ts:129-147`：冻结测试证明 child `session.event` 会出现在 raw notification stream。

> Driver 等价于 SDK client，**必须自行做 session scoping**，不能假设 notification 只来自 root session。

### 3.1-3.2 传输 / 握手

newline-delimited JSON-RPC 2.0。**无 cancel/session-close/session-resume/load/list**。`initialize`→`{serverInfo:{name:'deepseek-harness-sdk-runtime'}}`；`session/prompt` 惰性创建。Driver 接收全部 4 类 notification，按 §3.8 session scope 路由。

### 3.3 `session.event` → core.Event 映射（自包含，证据标签分级）

🟢 runtime-dump / 🔵 source-schema / 🟡 composition-generated / ⚪ deferred。证据：`scripts/dsh-gate0/dumps/`（run1 jsonrpc-agent todo pending+completed；run2 max-tokens；run3 driver-cordis workspace-write；run4 driver-cordis danger-full-access）。

| DSH `event.type` | 标签 | core.Event | 关键字段 / 规则 |
|---|---|---|---|
| `turn/start` | 🟢 | `EventTurnStarted` | `data.turn` → TurnID（§3.6） |
| `turn/end`(completed) | 🟢 | 终态 `EventResult` | `data.reason.kind:"completed"` |
| `turn/end`(max-tokens) | 🟢 | token 限制完成 | run2 verified |
| `turn/end`(error/aborted/interrupted/blocked) | ⚪ | deferred | 无样本 |
| `step/start`·`step/end` | 🟢 | 内部边界 | `data.turn, data.step` |
| `user/message`(**source.kind=user**) | 🟢 | `EventUserMessage` | **按 source.kind 分流（§3.6 P0-1）**；`data.id`→ItemID |
| `user/message`(**source.kind=plugin**) | 🟢 | **不发 user event**（忽略/诊断） | 权限运行时上下文，禁止覆盖用户 prompt（§3.6） |
| `user/message`(未知 source) | ⚪ | fail visibly / deferred | 不默认当用户消息 |
| `assistant/chunk`(text-delta) | 🟢 | `EventText` | `data.chunk.text`；ItemID=TurnID（§3.6） |
| `assistant/chunk`(reasoning-delta) | 🟢 | `EventThinking` | 同上 |
| `assistant/chunk`(tool-call-delta) | 🟢 | **不发 tool start**（§3.7 组装 arguments） | 真实字段名 `argumentsDelta`（非 arguments） |
| `assistant/chunk`(block-start/block-end) | 🟢 | **codec 内部状态**（一期自然合并，§3.7） | core.Event 无 newPart 字段 |
| `assistant/chunk`(usage/finish) | 🟢 | usage owner（§3.7） | |
| `assistant/message` | 🟢 | **只校验不追加**（§3.7） | 组装态；`data.message.id` 不进 identity |
| `tool/call` | 🟢 | `EventToolUse` | `data.callId`→RequestID+ItemID |
| `tool/result` | 🟢 | `EventToolResult` | 双层 content；`isError` 字段 🟢，`isError:true` ⚪ |
| `tool/result.meta`(FsDiffMeta) | ⚪ | 不映射 inline diff | 实测 diffs=`[]` |
| `todo/write` | 🟢 | todo 更新 | status: pending 🟢 + completed 🟢（run1 两态）；in_progress 🔵 schema-only |
| `permission/preset`·`sandbox/mode`·`approval/policy` | 🟡 | 控制面/诊断 | composition 产物 |
| `session/title` | 🟢 | 标题 | `data.title` |
| `request/header`·`request/context` | 🟢 | context usage 输入 | `data.contextWindow`（§3.7） |
| `agent/inbox/spliced` | 🟢 | 内部 | prompt 入队 |
| 其余（compaction/approval-asked/command/tool-workflow/goal-plan-schedule/session-end-seed 等） | ⚪ | **逐项声明**（§3.10）：known+deferred → 记录诊断后跳过；unknown+无 ignorable → fail visibly | 无样本 |

### 3.4 turn 生命周期 + session.status 消费规则（round7 P1-3）

**turn 完成只认 `turn/end`**。`session/prompt` 只回 `{messageId}`，不表示 turn 完成。

**`session.status`（round7 P1-3）**：SDK server 广播 `session.status {sessionId, status: idle|running}`（`types.ts:59-64`）。四次 `.stdout` 和 `run-output.txt` 都出现 root `running→idle`。消费规则：

| 规则 | 说明 |
|---|---|
| **session scope** | 只在 `sessionId == rootSessionID` 时影响 root runtime state（§3.8） |
| **liveness 辅助信号** | root `running`/`idle` 是 runtime-state 辅助信号，用于 iOS 执行态 UI 提示 |
| **不替代 turn/end** | `turn/end` 是 turn completion truth；`session.status=idle` **不**单独收口 turn、**不**生成第二个 turn terminal |
| **重复/迟到** | 重复或迟到的 `session.status` 不改变已完成 turn 的状态 |
| **child/foreign** | descendant 或 foreign session 的 `status` 不影响 root state（§3.8） |

**durable fixture**：gate helper 应把全部 4 类 notification 以带 `method/params` 的 sanitized JSONL 独立保存，至少冻结 root `running`/`idle`。当前证据为 `.stdout` 和 `run-output.txt`（非 committed durable fixture），committed JSONL dumps 只保存 `session.event`。durable notification dump 作为 driver 测试基建 deferred（§15）。

### 3.5 权限模型

权限栈在 `bundle/base`。assembled composition（`driver-cordis.yml`）：🟢 可加载+激活+fail-closed（unconfined executor 拒绝）；🟢 **环境切换 verified**（run3 `workspace-write`+`ask` vs run4 `danger-full-access`+`never`）。一期 `permission_resolve` 不声明。

### 3.6 identity + source.kind 分流 + process nonce（v9：round5/6/7 P0）

#### 3.6.1 identity（对齐 reducer + active-turn 状态机 + source-proven 校验）

SSV2 reducer：text/reasoning delta `turnID := data.itemId`（projection_reducer.go:535），`itemId == lifecycle turn_id`（L16-17）；一个 turn 一个 `Assistant`。nonce 与 TurnID 拼装规则见 §3.6.3。

**identity 作用域（round6 P1-1）**：TurnID 只作用于 **turn-scoped core.Event**。四份 dump 双策略复核证实 949 条 turn-scoped 记录（`assistant/chunk`·`assistant/message`·`tool/call`·`tool/result`·`step/start`·`step/end`·`turn/end`）**全部自带 `data.turn` 且与 active 一致（mismatch=0）**；另有 34 条 control-plane/session/internal 记录（`permission/preset`·`sandbox/mode`·`approval/policy`·`agent/inbox/spliced`·`user/message`·`session/title`·`request/header`·`request/context`·`todo/write`）**无 `data.turn`**，不强行赋 TurnID。`user/message(source.kind=user)` 是唯一「无 source turn 但需进 timeline」的 shape，从 active 派生；其余 turnless 记录按 control-plane/session/internal 处理。

**active-turn 状态机（round5 P0-1 + round6 P0-2）**：真实 dump 证实 `user/message` 6/6 **无 `data.turn/step`**（仅 `turn/start` 有 turn）。除 user 外的 turn-scoped frame 必须 **validate-then-map**（source turn/step 先校验，不匹配 fail visibly，再用 activeTurnID），不能静默覆盖来源身份——否则迟到帧 / 跨 turn 帧 / 协议漂移会被静默挂到当前 turn，污染 projection：

| codec 状态转移 | 触发 event | 动作 |
|---|---|---|
| → `activeTurnID` | `turn/start(data.turn=N)` | 设 `activeTurn=N`、`activeTurnID="p{nonce}-t{N}"`；已有 active（嵌套 turn）→ **fail visibly** |
| 绑定 active | `user/message(source.kind=user)` | **唯一例外**（无 source turn 可校验）：`TurnID=activeTurnID`、`ItemID=data.id`；**无 active turn → fail visibly**（不回退 UUID，否则 user 落 UUID turn 与 assistant 分裂） |
| 丢弃 | `user/message(source.kind=plugin)` | 不发 event（§3.6.2） |
| **validate+map** | `assistant/chunk`·`assistant/message`·`tool/call`·`tool/result` | **校验 `data.turn==activeTurn` 且 `data.step==activeStep`**，不匹配 → **fail visibly**；通过后 assistant `ItemID==TurnID=activeTurnID`、tool `RequestID/ItemID=callId` |
| 校验 | `step/start` | 校验 `data.turn==activeTurn`；**已有未关闭 step（嵌套 step）→ fail visibly**；设 `activeStep` |
| 校验+清空 | `step/end` | 校验 `data.turn/step==active`；清空 `activeStep` |
| 校验+清空 | `turn/end` | 校验 `data.turn==activeTurn`；**仍有未关闭 step → fail visibly**；清空 `activeTurn/activeStep`；终态后再到 user → **fail visibly** |
| 不赋 TurnID | control-plane / session / internal（permission·sandbox·approval-policy·agent-inbox·session-title·request·todo 等） | 不进 active-turn 校验；按各自语义映射或忽略 |

**identity 矩阵**：

| core.Event 字段 | 生成规则 |
|---|---|
| `TurnID`（**turn-scoped core.Event**） | `activeTurnID="p{nonce}-t{activeTurn}"`（active-turn 状态机；turn-scoped frame 先校验 source turn/step） |
| `ItemID`（assistant text/reasoning） | **== TurnID**（reducer 要求） |
| `ItemID`（user, source.kind=user） | `data.id`（TurnID 来自 active-turn） |
| `RequestID`/`ItemID`（tool） | `callId` |
| `StreamID` | 一期 main stream（空） |
| control-plane / session / internal 事件 | **无 TurnID**（不强行赋值） |

`(turn,step)` 同时是 codec 内部 chunk/assembled 校验 key 与 source-proven 校验来源。**多 step / 多 block**：同 turn 所有 step 归同一 turn 同一 `Assistant`；block-start/end 一期**自然合并**（core.Event 无 newPart，不声称"已有 part 边界"）。若要 step 级独立 message，须扩展 core.Event+wire+reducer（协议改动，一期不做）。

> **reducer 级测试 deferred（§15）**：冻结样本测 codec→mapAgentEvent→reducer，证 user/assistant 同 turn、plugin 不入 timeline、source turn/step mismatch fail visibly、abort/crash/restart 三类 nonce 不冲突。

#### 3.6.2 source.kind 分流（round4 P0-1）

assembled composition 两种 `user/message`（同 `role:"user"` + UUID `data.id`，不能按 role/id 区分）：

| `data.source.kind` | 内容 | 处理 |
|---|---|---|
| `user` | 真实用户 prompt | → `EventUserMessage`（TurnID=activeTurnID） |
| `plugin` | 权限运行时上下文 | **不发 event**（禁止覆盖用户 prompt） |
| 未知 | — | fail visibly / deferred |

run3/run4 冻结样本：每 turn seq(N)=`user` + seq(N+1)=`plugin`。两者都发会让 reducer 同 turn 再次 upsert，plugin 覆盖真实 prompt。

#### 3.6.3 process nonce + 交付语义 + 进程生命周期（round5 P0-2 + round6 P0-1/P1-2 + round7 P0-2/P0-3/P1-4）

**v6 错误**：`processGen` 存 driver 内存，go-bridge/Agent 重启归零；原 sessionId 再传入 `StartSession` → `g0-t1` 复用。

**v7 错误（round6 P0-1）**：process nonce 只证明「新 spawn 已生成不冲突 TurnID」，没证明「crash 后必触发新 spawn」。crash 路径未闭环。

**v8 错误（round7 P0-2/P0-3）**：① idle crash 后「对这条新 prompt 重试一次」违反 at-most-once（DSH server `prompt()` 先 `followup(message)` 入队再返回 `messageId`；transport error 不能证明请求未送达）。② 把任意 `EventError` 当进程死亡并 delete registry，但 `EventError` 同时表示 turn/model/protocol error，进程仍健康；`sessionRegistry.delete()`（`types.go:360-374`）只删 map 不 `Close`，会制造活进程孤儿。

**v9 方案（process nonce + at-most-once delivery + typed process death）**：

##### ① nonce（round7 P1-4 措辞修正）

每次 spawn 用 `crypto/rand` 生成 **16 字节（128-bit）** 随机 hex。`rand.Read` 失败 → **spawn fail-closed**（不退时间戳/零值 fallback）。nonce 无需持久化，但必须在启动子进程**前**成功生成并固定到该 process lifetime。`TurnID = "p{nonce}-t{turn}"`。

> **措辞修正（round7 P1-4）**：128-bit CSPRNG nonce 提供**碰撞概率可忽略**的唯一性（2^-128 birthday bound），不是数学上的"必新"。"均 `t1` 不复用"应理解为概率保证，不是确定性不变量。

##### ② turn terminal vs process terminal（round7 P0-3）

**核心区分**：`EventError` 先只收口当前 turn；仅 **typed process-exit** 或 **channel-closed** 或 **`!sess.Alive()`** 才触发 registry 淘汰。

| 事件 | turn terminal？ | process terminal？ | 动作 |
|---|---|---|---|
| `EventError`（model/turn/protocol error） | ✅ `turn_error` 收口 | ❌ 进程仍健康 | 收口 turn，**保留 session**，下一 turn 可继续发送 |
| events channel close（unexpected） | ✅ `turn_error`（如 in-flight） | ✅ process death | CAS 淘汰 + Close/reap |
| events channel close（normal shutdown/abort） | ✅ 正常完成 | ✅ 但不合成额外 error | 淘汰 + Close |
| `!sess.Alive()`（pre-send health check） | — | ✅ | CAS 淘汰 + Close，下一条请求 respawn |

**CAS delete + Close ownership**：
- `sessionRegistry.delete()`（`types.go:360-374`）只从 map 删除并返回 session 对象，**不 Close**。
- CAS helper 按 **session 对象身份** compare-and-delete（镜像 `clearRelayKindIf` 的 compare-and-delete 模式 `handlers_relay.go:619`），返回被删的 exact old session。
- **赢得 CAS 的 owner** 在锁外幂等 `Close()`/reap 该 session；**输家 no-op**。
- 保证：同一 session 的 `Close` **只调用一次**；registry 无 orphan（活进程不在 registry 中）。

**健康 turn/model error 后继续发送**：driver 收到 `EventError` → `turn_error` 收口 → relay return → session 保留在 registry → 下一条 `handleSendMessage` 走 `sess.Send` 正常路径（不 respawn）。验收矩阵 #8。

##### ③ at-most-once delivery（round7 P0-2）

**问题**：DSH server `prompt()`（`server.ts:132-142`）先 `agent.followup(message)` 把带唯一 id 的用户消息入队，**然后**才返回 `{messageId}`。进程可能在完整接收 request、执行 `followup` 之后，于 response flush 前退出。driver 此时只看到 EOF/broken pipe。若自动重发，会生成第二个 DSH messageId，两个 prompt 都可能执行，工具调用副作用也可能重复。现有 bridge request ID 没有传进 DSH，也没有 SDK idempotency key，无法靠下游去重。

**at-most-once 规则**：

| 场景 | 请求是否可能已送达 | 允许重发？ |
|---|---|---|
| **pre-send 已知死亡**（`!sess.Alive()` / registry miss） | ❌ 未发送任何字节 | ✅ CAS 淘汰 + respawn + 发送**一次**（pre-send repair，不是 retry） |
| **zero-byte write**（连接建立后写 0 字节即 error） | ❌ 未写入 | ✅ respawn + 发送一次 |
| **partial write**（写了部分字节后 error） | ⚠️ 可能已送达 | ❌ **fail visibly**；淘汰死亡 session 供**下一条不同请求**重建 |
| **full request + response lost**（请求完整发出，response 未收到） | ⚠️ 可能已入队执行 | ❌ **fail visibly**；不重放本 prompt |
| **accepted unknown**（不确定是否被 server 接收） | ⚠️ 不确定 | ❌ **fail visibly** |

**实现约束**：
- `sess.Send` error 必须是 **typed/classified delivery outcome**，不是 generic error。
- 只有能证明 **zero bytes written / not accepted** 的错误才可重建后发送。
- partial write、response lost、accepted unknown 一律将本请求 fail visibly（返回 wire `send_failed`），淘汰死亡 session 供**下一条不同请求**重建，**不得重放本 prompt**。
- 若产品坚持自动 retry，必须先扩展协议把 bridge request identity 作为 DSH 幂等键并由 server durable dedup；当前协议（`0.0.1`）没有该能力。

> **不采纳：v8「idle crash 后对这条新 prompt 重试一次」**。该方案无法区分 "pre-send 已知死亡"（安全重建 + 发送）和 "send 后响应丢失"（不安全重放）。v9 以 delivery classification 替代：只有 pre-send death 和 zero-byte write 允许一次发送，其余 fail visibly。

##### ④ race 边界

abort 与 crash 并发 → 对象身份 CAS 使淘汰幂等（首触发淘汰，次为 no-op）；并发 send 已有 double-checked locking（`handlers.go:2048-2059`），新 session 不会被 stale relay defer 误清。eager 与 lazy 不互斥：eager 已淘汰的 session lazy 不再命中（对象已变）。abort+relay close、stale relay close 新 session、Close 只调用一次、registry 无 orphan（验收矩阵 #12）。

##### ⑤ 五场景全覆盖

1. abort → kill → terminal → 淘汰 → Close → 再发 `StartSession`（新 nonce）。
2. crash（turn 中） → typed process death → 淘汰 → Close → 再发（新 nonce）。
3. crash（idle） → 下一条 `handleSendMessage` pre-send health check 发现 `!sess.Alive()` → CAS 淘汰 → Close → respawn → 发送一次（新 nonce）。
4. 健康 turn/model error → `turn_error` 收口 → **session 保留** → 下一条正常发送。
5. go-bridge 重启 → registry 空 → `StartSession`（新 nonce）。

##### ⑥ 不改 session 协议

原 sessionId，不碰 rebind；projection key=TurnID（含 nonce）。

> **respawn 实现测试 deferred（§15）**：driver + go-bridge 代码；验收矩阵 #6-#9, #12 的 fault-injection 场景（pre-send dead、zero-byte/partial/full send、response lost、健康 error 后继续、process exit、abort/crash/stale relay 并发）。

> **替代方案（不采纳，§15）**：replacement session（abort 返回新 sessionId + iOS 切换，跨仓协议改动）/ 持久化 `(backend,outerSessionID)→generation`（需原子递增/恢复定义）/ 自动 retry（需 SDK idempotency key，当前协议不支持）—— process-nonce + typed process death + at-most-once delivery 最局部、无需持久化、不碰 session 流/rebind。

### 3.7 chunk/assembled 双写 + usage（round4 P0-2；peer 边界 round6 P1-3）

**唯一 owner**（🟢 证据：sanitizer peer 断言 ALL PASS —— chunk==assembled text/reasoning、usage 双源相等、**非空 peer 缺失即失败**；空内容 peer 的严格存在性检查 deferred，见 §15 P1-3）：

| 字段 | owner |
|---|---|
| live text/reasoning | chunk delta（text-delta/reasoning-delta）；assembled 只校验 |
| tool start | `tool/call`；`tool-call-delta`（字段名 `argumentsDelta`）只组装 |
| tool result | `tool/result` |

**usage 公式修正（round4 P0-2）**：DSH `inputTokens` **不含 cache hit**（adapter 从 prompt_tokens 减 cache）；DSH context-pressure projection 用 `inputTokens + cacheReadTokens`。

| core.ContextUsage | 来源 | 说明 |
|---|---|---|
| `InputTokens` | DSH inputTokens | 不含 cache |
| `CachedInputTokens` | cacheReadTokens | cache 占用 |
| `OutputTokens` | outputTokens | |
| `ReasoningOutputTokens` | reasoningTokens | output 子分，**不加回总量** |
| `UsedTokens` | **inputTokens + cacheReadTokens** | 与 ContextWindow 比较的当前压力 |
| `TotalTokens` | **= UsedTokens**（不填 contextWindow） | round4 修正：填 contextWindow 会伪造"已满" |
| `ContextWindow` | request/context.contextWindow | 模型容量 |

`EventContextUsageUpdated` 是**当前会话压力**（最近一次 request 的 pressure），**不累计各 step**；`usage_reporting` 跨 turn 计费聚合是另一条能力（一期不实现）。

**4 种情况**：正常流 🟢 / 无delta-assembled ⚪ 防御性 / 重复 ⚪（processed-seq 去重）/ 乱序-gap ⚪（buffer/flush 未定义）。**去重 ≠ 乱序重排**，分开。

### 3.8 Notification session scope & 路由矩阵（round7 P0-1）

**背景**：DSH SDK server 对 `session.event`、`session.status`、`subagent.started`、`subagent.finished` 做进程级全局广播（`server.ts:71-103`），**不自动限制为 root session**。SDK client 自行通过 `subscribeSessionTree` 做 client-side scoping（`client.ts:354-371`）。Driver 等价于 SDK client，**必须自行做 session scoping**，否则 child/foreign session 的 event/status 会破坏 root codec 的单一 `activeTurn/activeStep` 状态机。

**root session identity**：driver 在 `initialize` + 首次 `session/prompt` 后，记录 SDK 分配的 sessionId 作为 **rootSessionID**。该 id 在该 process lifetime 内不变。

**四类 notification 路由矩阵**：

| notification | `sessionId` 归属 | 一期处理 |
|---|---|---|
| `session.event` | `params.sessionId == rootSessionID` | **进入 root codec**（§3.3 映射表 + §3.6 identity） |
| `session.event` | descendant（在 lineage 中，见 `subagent.started`） | **显式过滤**，不进 root codec；可记录诊断日志 |
| `session.event` | unknown foreign（不在 lineage、≠ root） | **不并入 root**；fail-visible protocol diagnostic 或明确丢弃并记录 |
| `session.status` | `== rootSessionID` | 更新 root runtime state（§3.4）；**不替代 turn/end 作为完成真相** |
| `session.status` | descendant / foreign | **不影响 root state**；child `idle` 不得收口 parent turn |
| `subagent.started` | — | 建 lineage 边 `parentSessionId → childSessionId`；验证 parent 非空、拒绝 self-loop/循环 lineage |
| `subagent.finished` | — | 清 lineage；验证 parent/child 非空；`lastAssistantMessage` 可缺失 |

**lineage 管理**：
- `subagent.started` 的 `childSessionId` 加入 active descendant set；后续该 id 的 `session.event`/`session.status` 一律过滤。
- `subagent.finished` 从 descendant set 移除。
- process 退出时清空全部 lineage。
- 一期不展示 subagent timeline；`subagent.started/finished` 完整 shape（`SubagentFinishedNotification`）标 🔵 source-schema，一期仅做诊断过滤，不映射为 core.Event。

**`StreamID/ParentStreamID` 映射**：一期 root 为 main stream（空 StreamID）。descendant event 被过滤，不分配 StreamID。若未来要展示 subagent timeline，需扩展 codec 支持 per-session stream、lineage-aware StreamID 和 parent/child 关联。

**冻结测试要求（验收矩阵 #2, #3）**：
1. parent turn 内启动 child 完整 turn → parent user/assistant/usage/turn 结果**完全不含 child 内容**，parent active state 不被 child 改写。
2. child `session.status=idle` 先于 parent `turn/end` → parent turn 不被收口。
3. 两级 descendant → 全部过滤。
4. foreign session notification → 不并入 root；按明确策略诊断/拒绝。
5. self-loop/空 lineage `subagent.started` → 拒绝。
6. child `subagent.finished` 缺 `lastAssistantMessage` → 正常清 lineage，不 fail。

> **测试 deferred（§15）**：使用 pinned SDK subagent fixture（`sdk-client.spec.ts:129-147`）+ synthetic wire fixture 构造上述场景。

### 3.9 Attachment 输入策略（round7 P1-1）

MacBridge `core.AgentSession.Send(prompt, images, files)` 接收 `images []ImageAttachment` 和 `files []FileAttachment`；go-bridge `handlers.go:2069-2070` 无条件传给 driver。v9 必须定义非空 images/files 的处理策略。

**DSH SDK 限制**（🔵 source）：
- `session/prompt.contentBlocks` 支持 `image`，但 wire image 不是 base64，而是 `ImageAttachmentRef {attachmentId,mediaType,bytes,width,height,name?}`，引用指向 DSH attachment service。
- SDK protocol **没有 upload RPC**；`attachmentId` 由 DSH attachment service 内部分配。
- DSH `ContentBlock` **没有通用 file block**。

**一期策略**：text-only prompt。

| 附件类型 | 一期策略 | 理由 |
|---|---|---|
| **image** | 不支持 → 返回稳定 `not_supported` | DSH attachment-local 写入 path（bytes→ref）需要 driver 实现 DSH attachment service 对接，一期不在范围。不能自行伪造 `attachmentId`。 |
| **file** | 不支持 → 返回稳定 `not_supported` | DSH `ContentBlock` 无通用 file block；现有 `SaveFilesToDisk + prompt path` 语义需要 driver 端文件落地逻辑，一期不做。 |
| **text-only** | ✅ 正常 `session/prompt` | 已 verified（4 次 real run） |

**实现约束**：
- 非空 images/files 必须同步返回 wire `not_supported`（或 `send_failed`），**不得静默丢弃**。
- iOS backend descriptor 不得暗示 DSH backend 支持图片/文件上传（§8 capability 不声明 attachment 相关能力）。
- 若未来支持 image：需定义 bytes→ref 路径（通过 attachment-local 安全写入）、验证 mime/尺寸/限额/清理/`DSH_HOME` 隔离；不能用伪造 `attachmentId` 绕过 attachment service。

**contract test 要求**：text-only ✅、单 image → `not_supported`、坏 mime/超限 → `not_supported`、单 file → `not_supported`、混合附件 → `not_supported`。真实 key 只需验证 provider 能消费 image；格式/拒绝路径用 source-owned fixture。

### 3.10 Unknown event ignorable fail-closed 策略（round7 P1-2）

`SessionEvent` envelope（`packages/core/session/src/types.ts:404-422`）明确规定：未知 `type` 只有显式标注 `ignorable:true` 才能跳过；未知且未标 ignorable 的事件**必须拒绝解释**（MUST refuse to reconstruct），否则可能静默重建错误 timeline。

**三级分类**：

| 类别 | 判定 | 处理 |
|---|---|---|
| **已知 + 已映射** | 在 §3.3 映射表中 | 按 mapping rule 映射 |
| **已知 + deferred** | 在 `KNOWN_SESSION_EVENT_TYPES`（44 种）中，但一期不映射 | 逐项声明 ignore（如 compaction/approval-asked/command/tool-workflow/goal-plan-schedule/session-end-seed）；记录诊断日志后跳过 |
| **未知 + `ignorable:true`** | 不在 frozen inventory 中，但 envelope 标 `ignorable:true` | 记录诊断后跳过 |
| **未知 + 无 `ignorable` 标记** | 不在 frozen inventory 中，envelope 无 `ignorable` | **fail visibly**：收口 active turn（`turn_error`），不得静默跳过 |

**额外校验**：
- 非法 `ignorable:false` → 按无 ignorable 处理（fail visibly）。
- 非布尔 `ignorable` 值 → 按 wire error 处理（fail visibly）。
- seq gap（`data.seq` 不连续）→ 记录诊断；一期不 buffer/flush（§3.7 ⚪），但必须有 gap detection 日志。
- seq duplicate → processed-seq 去重（§3.7 既有规则）。

**frozen inventory**：driver 使用 `KNOWN_SESSION_EVENT_TYPES`（44 种）作为 known-type set。每轮 DSH 版本 pin 后重新生成该列表并验证无漂移。当前 pinned 44 种见 `scripts/dsh-gate0/known-event-types.txt`（round1 审计生成）。

> **测试 deferred（§15）**：使用 pinned source schema 的 wire fixture 构造 unknown ignorable/required/illegal 场景（验收矩阵 #4, #5）；不需要真实 key。

---

## 4. session 生命周期（live-only）

| 操作 | 一期 | 说明 |
|---|---|---|
| 创建/发送 | ✅ `session/prompt`（text-only，§3.9） | `{messageId}`；at-most-once delivery（§3.6.3 ③） |
| 取消 | ❌ kill 进程 | 触发 §3.6.3 ② typed process death（terminal → CAS 淘汰 → Close → 再发新 nonce），sessionId 不变 |
| 列出/resume | ❌ | live-only |
| 图片/文件附件 | ❌ | 一期 text-only，非空返回 `not_supported`（§3.9） |

**`ListSessions`（round4 P1-4 澄清）**：driver 返回 `nil, core.ErrNotSupported`。**当前 go-bridge generic handler（handlers.go:2628）把任何 error（含 ErrNotSupported）映射为 wire `list_failed`**，不是 `not_supported`。文档如实：一期 wire 表现为 `list_failed`（不伪造空成功）；若要 `not_supported` code，driver 主体合入时在 handler 加 `errors.Is(ErrNotSupported)` 分支（handlers.go:1431/1605 已有此模式的先例）。

---

## 5-7. 取消关闭 / 错误 / 脱敏

Close 三阶段；正常 `shutdown→{}` 🟢，SIGTERM/SIGKILL ⚪。错误 → `EventError` **先收口 turn**（§3.6.3 ② typed process death）；仅 typed process-exit/channel-close 触发 registry 淘汰。dumps 经 `sanitize.py` 递归 scrub + **peer 存在断言**（round4 P1）。

---

## 8. capability

`LiveEventSessionProcess`，无 `external_turn_streaming`。🟢 session_state/workspace_diff/diagnostics；⚠️ todos（须 FetchTodos 持久化读）/usage_reporting（须跨 turn 聚合）；❌ session_history/permission_resolve/supports_checkpoint/attachments。一期 text-only prompt（§3.9）；notification session scope 由 driver client-side 过滤（§3.8）。

---

## 9-11. protocol / 文件 / 三仓

无 bridge-v1 change（除非 turn 内多 message，§3.6）。driver `config/cordis.yml`=`scripts/dsh-gate0/driver-cordis.yml`。go-bridge `BuildAgentEnv` 注入 `DSH_PERMISSION_MODE`；iOS `BackendKind.deepSeek`，不展示历史。

---

## 12. 证据

pinned `47f943859bef60e4160492346772ded9b24f765a`。

| Run | composition | env | distinct | 关键 |
|---|---|---|---|---|
| Gate0(mock) | jsonrpc-agent | — | 11 | 连通 |
| run1 | jsonrpc-agent | — | 14 | todo **pending+completed**, tool/call, tool/result, reasoning-delta |
| run2 | jsonrpc-agent | maxTokens=24 | 11 | turn/end(max-tokens), usage |
| run3 | driver-cordis.yml | workspace-write | 16 | §10 可加载；preset/mode/policy；**user+plugin user/message 双 shape** |
| run4 | driver-cordis.yml | danger-full-access | 16 | 环境切换；user+plugin 双 shape |
| union | — | — | 17 | runtime-dump verified |

**fail-closed** 🟢；**环境切换** 🟢（run3↔run4）；**dump 自洽** 🟢（sanitizer peer 断言：chunk==assembled、usage 双源、**非空 peer 存在**、无路径）；**计数** mock=11/union=17。**user/message 双 shape** 🟢（source.kind=user/plugin，§3.6.2）。

**剩余 ⚪**：compaction/approval-asked/error 终态/非空 FsDiff/command/tool-workflow/goal-plan-schedule/assembled-only/乱序/SIGTERM/JSON-RPC error/replacement-session 完整链路。

---

## 13. 风险

1. pre-release 漂移；2. 取消即触发 typed process death（kill → terminal → CAS 淘汰 → Close → 再发新 nonce；同 turn 不重放，at-most-once delivery §3.6.3 ③）；3. 无 list/resume；4. approval 不经协议；5. DeepSeek 绑定；6. reducer 模型约束（turn 内多 step 拼接）；7. ⚪ 证据缺口；8. child/foreign session notification 必须由 driver 过滤（§3.8）；9. 一期 text-only，不支持图片/文件附件（§3.9）；10. unknown event 必须 fail-closed（§3.10）。

---

## 14. 修订记录

### Round4（v5→v6）
| 项 | v5 问题 | v6 修订 | 状态 |
|---|---|---|---|
| P0-1 | user/message 全映射 EventUserMessage，plugin 覆盖真实 prompt | §3.6.2 source.kind 分流（user→event，plugin→忽略）+ run3/run4 冻结样本 | ✅ |
| P0-2 | usage 公式错（UsedTokens 漏 cacheRead、TotalTokens 填 contextWindow 伪造已满） | §3.7 UsedTokens=input+cacheRead、TotalTokens=UsedTokens、ContextWindow 独立 | ✅ |
| P0-3 | "重启换 sessionId"未接入生命周期 | §3.6.3 process generation in TurnID（g{gen}-t{turn}），driver 内部闭环 | ✅ 设计；实现测试 deferred |
| P1 main.go | PARTIAL exit 0 | PARTIAL→os.Exit(1)；rpc-error 不打印 OK | ✅ |
| P1 sanitizer | 缺 peer 时空通过 | peer-existence 断言（chunk↔assembled 双向） | ✅ |
| P1 block | 声称"已有 part 边界" | 一期自然合并，删除错误声明 | ✅ |
| P1 ListSessions | 称"确定 unsupported error" | 澄清当前=list_failed，not_supported 需 handler 分支 | ✅ |
| P1 todo completed | 样本被覆盖 | run1 重跑补 pending+completed | ✅ |
| P1 自包含 | 引用 round2 跨仓表 | §3.3 自包含映射表 | ✅ |

### Round5（v6→v7）
| 项 | v6 问题 | v7 修订 | 状态 |
|---|---|---|---|
| P0-1 | user/message 无 data.turn，TurnID 生成规则缺 active-turn 归属 | §3.6.1 active-turn 状态机（turn/start 设 active、user 绑定 active、无 active fail visibly） | ✅ 设计；reducer 测试 deferred |
| P0-2 | processGen 内存归零，go-bridge 重启 g0-t1 复用 | §3.6.3 **process nonce**（每子进程随机 nonce，TurnID=p{nonce}-t{turn}，跨重启不复用） | ✅ 设计；三场景测试 deferred |
| P1 sanitizer | peer 仅 chunk→assembled（assembled-only 空通过） | 双向 peer 断言 + 负向样本验证（assembled-only/chunk-only 均 FAIL） | ✅ |
| P2 gofmt | main.go gofmt 差异 | `gofmt -w` + go vet 干净 | ✅ |

### Round6（v7→v8）
| 项 | v7 问题 | v8 修订 | 状态 |
|---|---|---|---|
| P0-1 | nonce 只保证新 spawn 新 ID；crash 后死亡 session 留 registry，再发不走 `StartSession` | §3.6.3 crash→respawn 闭环：relay terminal 按 session 对象身份 CAS `deleteSession`（eager）+ `sess.Send` transport 错误触发淘汰重试（lazy，覆盖 idle crash）；失败 turn 先 `turn_error` 收口，不重放 | ✅ 设计→v9 重写（at-most-once + typed death） |
| P0-2 | assistant/tool 行只「用 active」，未校验 source turn/step | §3.6.1 validate-then-map：除 user 外 turn-scoped frame 校验 `data.turn/step==active`，mismatch fail visibly；step 嵌套/未闭合校验 | ✅ 设计；reducer 测试 deferred |
| P1-1 | identity 矩阵写「TurnID（所有 event）」与 34 条 turnless shape 冲突 | §3.6.1 矩阵限定 turn-scoped core.Event；control-plane/session/internal 不赋 TurnID | ✅ |
| P1-2 | nonce 8 字节仅概率唯一、无 `crypto/rand` 失败策略 | 16 字节（128-bit）+ `rand.Read` 失败 fail-closed（spawn 失败，不 fallback 时间戳/零值） | ✅ 设计→v9 措辞修正（概率保证） |
| P1-3 | sanitizer 用内容 truthiness，单侧空 peer 仍通过 | §3.7/§15 如实收紧为「非空 peer 缺失即失败」；seen-flags + cardinality + 负向 fixture 属 driver 测试基建 deferred | 🟡 doc 已如实；代码 deferred |
| P1-4 | §4/§12/§13/§15 仍残留 processGen/generation/v6 真值，审计索引缺 round5/6 | 统一为 nonce + crash-respawn；§12 标题去 v6；审计索引补 round5/6；header→v8 | ✅ |
| P1-5 | README 仍 round4/v6 + 仅 chunk→assembled 单向 peer | README 同步 v8 + 双向 peer + 空内容边界；durable 负向 fixture/test deferred | ✅ 文本；fixture deferred |
| P2 | evidence 目录未跟踪 `__pycache__` | 根 `.gitignore` 忽略 `__pycache__/` | ✅ |

### Round7（v8→v9）
| 项 | v8 问题 | v9 修订 | 状态 |
|---|---|---|---|
| P0-1 | 只处理 `session.event`，遗漏 `session.status`/`subagent.started`/`subagent.finished`；SDK server 全局广播，child/foreign session 会破坏 parent active-turn 状态机 | §3.0 wire inventory（3 request + 4 notification）+ §3.8 notification session scope 路由矩阵：root/descendant/foreign 分流，lineage 管理，child event 显式过滤 | ✅ 设计；frozen fixture 测试 deferred |
| P0-2 | idle crash 后「对这条新 prompt 重试一次」违反 at-most-once（server 先 `followup` 入队再返回 `messageId`，transport error 不能证明未送达） | §3.6.3 ③ at-most-once delivery：pre-send death / zero-byte write → 可重建发送一次；partial write / response lost / accepted unknown → fail visibly 不重放。替代 v8 lazy retry | ✅ 设计；fault-injection 测试 deferred |
| P0-3 | 任意 `EventError` 当进程死亡并 delete registry，但 `EventError` 同时表示 turn/model error；`delete()` 不 Close 会造孤儿 | §3.6.3 ② typed process death：`EventError` 先只收口 turn（保留 session）；仅 typed process-exit/channel-close/`!Alive()` 触发淘汰；CAS 返回 old session，winner 在锁外幂等 Close/reap，loser no-op | ✅ 设计；race test deferred |
| P1-1 | `Send(prompt, images, files)` 输入 shape 完全缺失 | §3.9 attachment 策略：一期 text-only，非空 images/files 返回稳定 `not_supported`，不静默丢弃；DSH SDK 无 upload RPC / 无 file block | ✅ 设计；contract test deferred |
| P1-2 | 未知 event type 缺 `ignorable` fail-closed 规则 | §3.10 ignorable policy：known+deferred 逐项声明 ignore；unknown+`ignorable:true` 记录诊断后跳过；unknown+无 marker → fail visibly 收口 turn | ✅ 设计；wire fixture test deferred |
| P1-3 | `session.status` 有运行证据但无 durable fixture 与消费规则 | §3.4 session.status 消费规则：root-only scope、liveness 辅助信号、不替代 turn/end、child/foreign 不影响 root；durable notification dump deferred | ✅ 设计；durable fixture deferred |
| P1-4 | nonce 写「必新」是数学绝对量，实际是概率属性 | §3.6.3 ① 措辞修正：「128-bit CSPRNG，碰撞概率可忽略（2^-128 birthday bound）」，不是数学上的必不碰撞 | ✅ |
| P2 | Round6 修订表中 P0-2 行描述与 P0-1 有交叉重复 | 检查并确保 v9 Round6 表中 P0-1/P0-2 行内容互斥清晰 | ✅ |

### Round1-4 闭环：见前版（descriptor/resume/权限/计数/Gate0/identity/双写owner/环境切换/source.kind/usage/block/ListSessions/todo/自包含）。

---

## 15. 不采纳 / deferred

| 审计建议 | 处理 | 原因 |
|---|---|---|
| replacement session（abort 返回新 sessionId + iOS 切换） | 不采纳（选 process-nonce 方案） | replacement 需改 go-bridge handleAbortGeneration + iOS session 流（跨仓协议改动）；process-nonce + typed process death 更局部，不改 session 流/rebind |
| 自动 retry（transport error 后重发同一 prompt） | 不采纳（违反 at-most-once） | DSH server 先 `followup` 入队再返回 `messageId`；transport error 不能证明未送达。需 SDK idempotency key 才能安全 retry，当前协议不支持。v9 以 delivery classification 替代（§3.6.3 ③） |
| 图片/文件附件上传 | 不采纳（一期 text-only） | DSH SDK 无 upload RPC；`ImageAttachmentRef` 需要 attachment service 对接；`ContentBlock` 无通用 file block。非空返回 `not_supported`（§3.9） |
| subagent timeline 展示 | 不采纳（一期过滤） | child event 显式过滤不进 root codec（§3.8）。若未来支持需扩展 codec：per-session stream、lineage-aware StreamID、parent/child 关联 |
| `ProjectionReducer` 级测试 | deferred | driver 测试代码。identity 已对齐 reducer 源码（L535+L16-17）+ source.kind 分流 + process nonce + typed process death + source-proven 校验 |
| sanitizer seen-flags / cardinality / 负向 fixture（round6 P1-3） | deferred | sanitizer 现用内容 truthiness（`s['text']`）兼任「见过」与「内容」，单侧空 peer 仍通过；doc 已如实收紧为「非空 peer 缺失即失败」。改 seen-flags + assembled cardinality + 负向 fixture 属 driver 测试基建 |
| crash→respawn / at-most-once / typed death 实现测试（round6 P0-1 + round7 P0-2/P0-3） | deferred | driver + go-bridge 代码：验收矩阵 #6-#9, #12 的 fault-injection 场景（pre-send dead、zero-byte/partial/full send、response lost、健康 error 后继续、process exit、abort/crash/stale relay 并发）。设计闭环见 §3.6.3（已对齐 `deleteSession`/`relayEvents` defer/`sess.Send`/`prompt()` 源码） |
| notification session scope 实现测试（round7 P0-1） | deferred | driver + go-bridge 代码：验收矩阵 #2-#5 的 subagent/foreign/ignorable 场景。设计闭环见 §3.8（已对齐 `types.ts:50-104`/`server.ts:71-103`/`client.ts:354-371`） |
| attachment contract test（round7 P1-1） | deferred | driver 代码：text-only ✅ / image → not_supported / file → not_supported / 混合 → not_supported。设计见 §3.9 |
| durable notification dump（round7 P1-3） | deferred | gate helper 把全部 4 类 notification 以带 method/params 的 sanitized JSONL 保存；当前证据为 `.stdout` / `run-output.txt` |
| `block-start/end → newPart` 扩展 core.Event | 一期不扩展 | core.Event 无 newPart；一期自然合并（同 turn 多 block text 拼接）。若要 block 级独立 part，扩展 core.Event+wire（协议改动） |
| Gate helper 完整 CI gate（malformed/join） | 部分采纳 | 已修：env 转发/O_TRUNC/sendAndWait 竞态/rpc-error/PARTIAL exit 1/peer 断言。完整 malformed/join 属 driver 测试基建 |
| fail-closed durable fixture / protocol pack 同步 | deferred | 测试基建/跨仓协议 |

**main.go v7 实际修复**：① `DSH_PERMISSION_MODE`/`DSH_SYSTEM_PROMPT` 转发；② dump `O_TRUNC`；③ `sendAndWait` 先注册后写 + rpc-error 返回 nil（fatal exit 1）；④ **PARTIAL→`os.Exit(1)`**；⑤ error response 不打印 OK；⑥ **gofmt 干净 + go vet 无诊断**。
**sanitize.py v7**：递归 scrub + **双向 peer 断言**（chunk→assembled 与 assembled→chunk 双向，**非空** peer 缺失即失败；负向样本 assembled-only/chunk-only 均 FAIL）+ 无路径断言。**已知边界（round6 P1-3）**：断言用内容 truthiness（`s['text']`/`s['atext']`）兼任「见过记录」与「拼接内容」，单侧空 text delta / 单侧空 assembled text 仍通过；当前真实 dump 无空块，既有结论不受影响。完整「缺任一 peer 即失败」需 seen-flags + assembled cardinality + 负向 fixture，见 §15 deferred。

**当前结论（v9）**：协议选型（SDK JSON-RPC，3 request + 4 notification）、事件映射（自包含 + source.kind 分流 + ignorable fail-closed）、identity（对齐 reducer + process nonce + source-proven turn/step 校验）、交付语义（at-most-once delivery）、进程生命周期（typed process death + CAS Close ownership）、notification session scope（root/descendant/foreign 路由矩阵）、attachment 策略（一期 text-only）、session.status 消费规则、双写去重（peer 断言）、usage 公式、权限 composition（环境切换）十一块设计完整且有真实/源码证据。剩余为 driver 实现代码（respawn/at-most-once/typed-death fault-injection 测试、notification scope fixture、attachment contract test、sanitizer seen-flags fixture、durable notification dump、protocol 同步）与特殊触发 dump。
