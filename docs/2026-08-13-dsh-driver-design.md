# DeepSeek Harness (DSH) Driver 设计文档

- **日期**：2026-08-14（v13；round12 **APPROVE** —— 设计放行，见 §16 交接验收门槛）
- **Runtime**：DeepSeek Harness `dsh-jsonrpc-agent`（pinned `47f943859bef60e4160492346772ded9b24f765a`，协议 `0.0.1`，pre-release）
- **协议**：DSH SDK JSON-RPC 2.0 over stdio（**非** ACP），3 requests + 4 notifications（§3.0）
- **证据**：`scripts/dsh-gate0/`（4 次真实 run + assembled composition + sanitizer 带 peer 断言）
- **审计**：round1-12 共 12 份，全部存于**相邻 iOS 仓** `../cordcode-ios/docs/`（本仓 docs/ 无副本；跨仓阅读时路径相对本仓根目录解析）：round1=`2026-08-13-dsh-driver-design-audit.md`、round2-5=`2026-08-13-dsh-driver-design-audit-round{2,3,4,5}.md`、round6-11=`2026-08-14-dsh-driver-design-audit-round{6,…,11}.md`、round12 **APPROVE**=`2026-08-14-dsh-driver-design-audit-round12.md`
- **复核**（2026-08-15，定稿后两仓代码更新后重验）：round12（`2edb59b`）之后 MacBridge 的 8 个 commit（grokbuild/opencode 修复、F-7/F-8、冷 hydrate seal、turn_error/turn_aborted producer 入册 canonical——与本文终态设计同向）及 iOS 侧 remote-web/渲染器/PWA 收尾均未触碰本文设计锚点；接口/seam/registry·CAS/reducer identity/core 事件面/六条 driver 附件路径全部重验成立；DSH 本地 checkout 仍在 pin `47f9438`，`gen-known-event-types.py` verify exit 0。**行号漂移**：正文少量引用行号已位移（`attachments.go` isImage 41→32、`clearRelayKindIf` 定义 →`handlers_relay.go:2427`、`errors.Is(ErrNotSupported)` 先例 →`:1433/:1607`、`handleSendMessage` →`:1952` 起、`core/message.go:527` title 注释位次变化），语义全部完好——**实现时按符号 grep 定位，行号以 round12 定稿快照为准**

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
| server→client | `subagent.finished` | `{provider,agentId,parentSessionId,childSessionId,status,stopReason,lastAssistantMessage?}` | 维护 lineage tombstone（§3.8：finished **不删** edge） |

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
| `user/message`(未知 source) | ⚪ | **fail visibly**（§3.10.2；不默认当用户消息，不 deferred） | required unknown |
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
| `session/title` | ⚪ | **driver 内部诊断**（不发 core.Event、不更新 iOS） | `core.EventType` 无 title（`core/message.go:527` 明确不携带 title）；与 `session.status` 同（§3.4）；§3.10.2 ② ignore |
| `request/header`·`request/context` | 🟢 | context usage 输入 | `data.contextWindow`（§3.7） |
| `agent/inbox/spliced` | 🟢 | 内部 | prompt 入队 |
| 其余（compaction/approval-asked/command/tool-workflow/goal-plan-schedule/session-end-seed 等） | ⚪ | **按 §3.10.2 四级**：仅 `ignorable:true` 跳过；known 但未实现 required 语义 → **fail visibly**（无样本≠safe-ignore） | 无样本 |

### 3.4 turn 生命周期 + session.status 消费规则（round7 P1-3 + round8 P1-2）

**turn 完成只认 `turn/end`**。`session/prompt` 只回 `{messageId}`，不表示 turn 完成。

**`session.status` core seam（round8 P1-2）**：`core.EventType`（`core/message.go:318-335`）**没有** session-status 类型；`AgentSession.Events() <-chan core.Event` 不承载 status。故 root `session.status` **一期只作 driver/relay 内部 liveness 诊断**（与 `turn/end`/`EventResult` 交叉校验、检测悬挂 running），**不**发 `core.Event`、**不**投影到 iOS。iOS 执行态 UI 继续由既有 bridge admission（`handleSendMessage` 前置 `session_state_changed:running`）+ turn terminal（`EventResult`/`EventError` → idle）驱动——不新增 core seam、不新增 wire。若未来要让 iOS 直接消费 status，须先新增 `core.EventSessionStateChanged` + mapAgentEvent/wire + 与现有 running/idle owner 去重（一期不做）。

**消费规则**：

| 规则 | 说明 |
|---|---|
| **session scope** | 只在 `sessionId == rootSessionID` 时影响 root 内部诊断（§3.8） |
| **driver-internal liveness** | root `running`/`idle` 是 driver 内部辅助信号，**不跨 core.Event、不进 iOS UI** |
| **不替代 turn/end** | `turn/end` 是 turn completion truth；`session.status=idle` **不**单独收口 turn、**不**生成第二个 turn terminal |
| **重复/迟到** | 重复或迟到的 `session.status` 不改变已完成 turn 的状态 |
| **descendant** | descendant session 的 `status` 过滤，不影响 root（§3.8） |
| **foreign** | foreign session 的 `status` 与 foreign event 同：**fail visibly + 终止 process**（§3.8 三行路由矩阵） |

**durable fixture**：gate helper 应把全部 4 类 notification 以带 `method/params` 的 sanitized JSONL 独立保存，至少冻结 root `running`/`idle`。当前证据为 `.stdout` / `run-output.txt`（非 committed durable fixture），committed JSONL dumps 只保存 `session.event`。durable notification dump 作为 driver 测试基建 deferred（§15）。

### 3.5 权限模型

权限栈在 `bundle/base`。assembled composition（`driver-cordis.yml`）：🟢 可加载+激活+fail-closed（unconfined executor 拒绝）；🟢 **环境切换 verified**（run3 `workspace-write`+`ask` vs run4 `danger-full-access`+`never`）。一期 `permission_resolve` 不声明。

### 3.6 identity + source.kind 分流 + process nonce（v9：round5/6/7 P0）

#### 3.6.1 identity（对齐 reducer + active-turn 状态机 + source-proven 校验）

SSV2 reducer：text/reasoning delta `turnID := data.itemId`、`itemId == lifecycle turn_id`（`projection_reducer.go:16-20` 注释 + reducer itemId 赋值）；一个 turn 一个 `Assistant`。nonce 与 TurnID 拼装规则见 §3.6.3。

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
| 未知 | — | **fail visibly**（§3.10.2；不 deferred） |

run3/run4 冻结样本：每 turn seq(N)=`user` + seq(N+1)=`plugin`。两者都发会让 reducer 同 turn 再次 upsert，plugin 覆盖真实 prompt。

#### 3.6.3 process nonce + 交付语义 + 进程生命周期（round5 P0-2 + round6 P0-1/P1-2 + round7 P0-2/P0-3/P1-4）

**v6 错误**：`processGen` 存 driver 内存，go-bridge/Agent 重启归零；原 sessionId 再传入 `StartSession` → `g0-t1` 复用。

**v7 错误（round6 P0-1）**：process nonce 只证明「新 spawn 已生成不冲突 TurnID」，没证明「crash 后必触发新 spawn」。crash 路径未闭环。

**v8 错误（round7 P0-2/P0-3）**：① idle crash 后「对这条新 prompt 重试一次」违反 at-most-once（DSH server `prompt()` 先 `followup(message)` 入队再返回 `messageId`；transport error 不能证明请求未送达）。② 把任意 `EventError` 当进程死亡并 delete registry，但 `EventError` 同时表示 turn/model/protocol error，进程仍健康；`sessionRegistry.delete()`（`types.go:360-374`）只删 map 不 `Close`，会制造活进程孤儿。

**v9 方案（process nonce + at-most-once delivery + typed process death）**：

##### ① nonce（round7 P1-4 措辞修正）

每次 spawn 用 `crypto/rand` 生成 **16 字节（128-bit）** 随机 hex。`rand.Read` 失败 → **spawn fail-closed**（不退时间戳/零值 fallback）。nonce 无需持久化，但必须在启动子进程**前**成功生成并固定到该 process lifetime。`TurnID = "p{nonce}-t{turn}"`。

> **措辞修正（round7 P1-4 + round8 P2）**：128-bit CSPRNG nonce，**工程上碰撞概率可忽略**（不是数学上的确定性"必新"；多次 spawn 的 birthday collision 在工程尺度为零——单次与固定值碰撞 2^-128，n 个 nonce 近似 n(n-1)/2^129）。

##### ② 错误分类 + turn terminal vs process terminal（round7 P0-3 + round9 P1-3）

**核心区分**：不是所有 `EventError` 都「进程健康」。必须二分——**application error**（模型/提供商/turn 级，流仍合法）只收口 turn、保留 process；**protocol/codec violation**（framing/JSON/envelope/seq/scope/invariant 损坏）必须先发可见 terminal 再淘汰 process，**不在已污染的 decoder 上继续下一 turn**。

| 错误类别 | 判定 | turn terminal？ | process terminal？ | 动作 |
|---|---|---|---|---|
| **application error（recoverable）** | 作为**合法 `session.event`** 到达的 `EventError`：model 拒绝 / provider 5xx / 内容策略 abort / turn 级失败，**流与 decoder 仍合法** | ✅ `turn_error` 收口 | ❌ 进程健康 | 收口 turn，**保留 session**，下一 turn 可继续（验收 #8） |
| **protocol/codec violation（fatal）** | JSON-RPC framing 错 / 非 JSON 行 / envelope schema 违例 / `seq` gap·倒退·conflicting-duplicate（§3.10.1）/ session-scope foreign 污染（§3.8）/ active-turn·step invariant 违例 / unknown required event 无 `ignorable`（§3.10.2） | ✅ `turn_error`（若 turn active） | ✅ | 发可见 terminal + **CAS 淘汰 + Close/reap**；不继续 |
| events channel close（unexpected EOF） | — | ✅ `turn_error`（如 in-flight） | ✅ process death | CAS 淘汰 + Close/reap |
| events channel close（normal `shutdown`/abort） | — | ✅ 正常完成 | ✅ 但不合成额外 error | 淘汰 + Close |
| `!sess.Alive()`（pre-send health check） | — | — | ✅ | CAS 淘汰 + Close，下一条请求 respawn |

> round9 P1-3：v10 把「protocol error」与 model/turn error 同列「保留 session」是错的——framing/envelope/seq/scope 损坏意味着 decoder/流已被污染，下一 turn 不可信。删除笼统「protocol error 健康」分类，改为上表二分：合法 `EventError` event = application（保留）；流/schema/seq/scope 损坏 = protocol violation（淘汰）。

**CAS delete + Close ownership**：
- `sessionRegistry.delete()`（`types.go:360-374`）只从 map 删除并返回 session 对象，**不 Close**。
- CAS helper 按 **session 对象身份** compare-and-delete（镜像 `clearRelayKindIf` 的 compare-and-delete 模式 `handlers_relay.go:619`），返回被删的 exact old session。
- **赢得 CAS 的 owner** 在锁外幂等 `Close()`/reap 该 session；**输家 no-op**。
- 保证：同一 session 的 `Close` **只调用一次**；registry 无 orphan（活进程不在 registry 中）。

**健康 application error 后继续发送**：driver 收到合法 `EventError`（application）→ `turn_error` 收口 → relay return → session 保留在 registry → 下一条 `handleSendMessage` 走 `sess.Send` 正常路径（不 respawn）。验收矩阵 #8。

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

**实现约束（round8 P1-1 Go contract）**：`AgentSession.Send(prompt, images, files) error`（`core/interfaces.go:39`）返回 plain `error`——driver 必须返回 **typed** delivery error，go-bridge 用 `errors.As` 分类（不能靠字符串猜测）：

```go
type DeliveryError struct { Stage DeliveryStage; Cause error }
type DeliveryStage uint8
const (
    StagePreWrite        DeliveryStage = iota // !Alive() 或 write 0 bytes（未送达）
    StagePartialWrite                          // 写了部分字节后 error（可能已送达）
    StageAwaitingResponse                      // 完整写出 request，response 未收到（可能已 followup 入队）
    StageAcceptedUnknown                       // 是否被 server 接收不确定
)
```

| Stage | 允许重建发送？ | go-bridge 动作 |
|---|---|---|
| `StagePreWrite` | ✅ 一次 | CAS 淘汰死亡 session + Close + respawn + 发送**一次**（pre-send repair，非 retry） |
| `StagePartialWrite` / `StageAwaitingResponse` / `StageAcceptedUnknown` | ❌ | 本请求 fail visibly（`send_failed`）；淘汰死亡 session 供**下一条不同请求**重建；**不得重放本 prompt** |

只有 `StagePreWrite`（能证明 zero bytes / not accepted）才可重建后发送一次；其余 fail visibly 不重放。若产品坚持自动 retry，必须先扩展协议把 bridge request identity 作为 DSH 幂等键并由 server durable dedup；当前协议 `0.0.1` 没有该能力。

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

**4 种情况**：正常流 🟢 / 无delta-assembled ⚪ 防御性 / **重复** = exact-replay（同 seq+canonical 相同）幂等跳过（§3.10.1）/ **gap·乱序** = fail visibly（§3.10.1，不 buffer/flush）。**去重 ≠ 乱序重排**，分开。

### 3.8 Notification session scope & 路由矩阵（round7 P0-1）

**背景**：DSH SDK server 对 `session.event`、`session.status`、`subagent.started`、`subagent.finished` 做进程级全局广播（`server.ts:71-103`），**不自动限制为 root session**。SDK client 自行通过 `subscribeSessionTree` 做 client-side scoping（`client.ts:354-371`）。Driver 等价于 SDK client，**必须自行做 session scoping**，否则 child/foreign session 的 event/status 会破坏 root codec 的单一 `activeTurn/activeStep` 状态机。

**root session identity（round8 P0-1 修正）**：rootSessionID 是 driver **发送给 `session/prompt.params.sessionId` 的 exact id**（source `types.ts:34-38`「unknown id lazily creates」+ `server.ts:132-142` `getOrCreateSession(params.sessionId)`）——**SDK 不分配它**（v9「SDK 分配的 sessionId」与源码不符）。driver 生成并持久化该 id；`session.event`/`session.status` 回显同一 id（`String(session.id)`）。root 过滤 = `notification.params.sessionId == driver 持有的 rootSessionID`。

**四类 notification 路由矩阵**：

| notification | `sessionId` 归属 | 一期处理 |
|---|---|---|
| `session.event` | `params.sessionId == rootSessionID` | **进入 root codec**（§3.3 映射表 + §3.6 identity） |
| `session.event` | descendant（在 lineage 中，见 `subagent.started`） | **显式过滤**，不进 root codec；可记录诊断日志 |
| `session.event` | unknown foreign（不在 lineage、≠ root） | **唯一策略**：fail visibly + **终止该 process**（round8 P0-1：不留「或 drop」二选一——选 fail 会误杀迟到合法 child，选 drop 会静默吞真污染）。driver 每 process 单 root tree，foreign = 协议污染，必须停。 |
| `session.status` | `== rootSessionID` | driver-internal liveness 诊断（§3.4）；**不替代 turn/end 作为完成真相** |
| `session.status` | descendant | **过滤**；不影响 root state，child `idle` 不得收口 parent turn |
| `session.status` | foreign | **与 foreign `session.event` 相同：fail visibly + 终止 process**（round9 P1-2：driver 单 root tree，foreign status 同样证明不变量已破坏） |
| `subagent.started` | — | 建 lineage 边 `parentSessionId → childSessionId`；验证 parent 非空、拒绝 self-loop/循环 lineage |
| `subagent.finished` | — | **维护 tombstone（不删 edge，§3.8）**；验证 parent/child 非空；`lastAssistantMessage` 可缺失 |

**lineage 管理（round8 P0-1）**：
- `subagent.started` 的 `childSessionId` 加入 **descendant tombstone set**——对齐 SDK `client.ts:403-415` `recordSessionRelationship`：**只在 started 记 `sessionParents`，finished 不删 edge**。
- **`subagent.finished` 不从 set 移除**：finished 只表示 subagent run ended，**不是**「该 child 的所有 notification 已到齐并形成不可跨越的流屏障」。迟到 child event/status 若在 finish 后到达，删 set 会把它降级成 foreign 误杀（v9 错误）。
- tombstone **至少保留到 process teardown**；active/finished 可另设状态，但 finished child 仍按 descendant 过滤。
- `started` edge 校验：`parentSessionId` 必须是 root 或已知 descendant；foreign parent 不能把任意 session 注入 root tree；拒绝 self-loop / 循环 lineage。
- **session-scope 路由先于 seq 校验**：只有进 root codec 的 event 才跑 §3.10.1 root `expectedSeq`；descendant/foreign 在路由层即被过滤，其 `seq` 不推进 root。
- process 退出时清空全部 lineage + tombstone。
- 一期不展示 subagent timeline；`subagent.started/finished` 完整 shape（`SubagentFinishedNotification`）标 🔵 source-schema，一期仅做诊断过滤 + lineage 维护，不映射为 core.Event。

**`StreamID/ParentStreamID` 映射**：一期 root 为 main stream（空 StreamID）。descendant event 被过滤，不分配 StreamID。若未来要展示 subagent timeline，需扩展 codec 支持 per-session stream、lineage-aware StreamID 和 parent/child 关联。

**冻结测试要求（验收矩阵 #2, #3）**：
1. parent turn 内启动 child 完整 turn → parent user/assistant/usage/turn 结果**完全不含 child 内容**，parent active state 不被 child 改写。
2. child `session.status=idle` 先于 parent `turn/end` → parent turn 不被收口。
3. 两级 descendant → 全部过滤。
4. foreign session notification → **fail visibly + 终止 process**（不并入 root）。
5. self-loop / 空 lineage `subagent.started` → 拒绝。
6. child `subagent.finished` 缺 `lastAssistantMessage` → 正常维护 lineage，不 fail。
7. **finish → 迟到 child event/status（round8）**：finished 后到达的 child notification 仍按 descendant 过滤，**不降级为 foreign 误杀**。
8. **finished child 的 grandchild（round8）**：tombstone 保留期间，grandchild 仍被过滤。
9. **foreign-parent `started`（round8）**：parent 非 root/descendant → 拒绝注入 root tree。
10. **重复 `started`（round8）**：同 child id 幂等，不重建/不破坏 lineage。
11. **child id reuse（round8）**：finished child 的 id 被「新 started」重用 → tombstone 按 started 边更新，不残留陈旧 parent。

> **测试 deferred（§15）**：使用 pinned SDK subagent fixture（`sdk-client.spec.ts:129-147`）+ synthetic wire fixture 构造上述场景。

### 3.9 Attachment 输入策略（round7 P1-1 + round8 P0-4 + round9 P0-1）

**真实数据链**：`send_message.attachments[] → go-bridge splitAttachments()（attachments.go:23-47）→ AgentSession.Send(prompt, images, files)`。`AttachmentInput{Kind,Mime,Filename,Base64}`（**无 `sizeBytes`**）。`splitAttachments` 对空/invalid/decoded-empty base64 **直接 `continue` 静默丢弃**，不返回 error。

**capability 真相源（round9 P0-1 + round10 P0-1，两级修正）**：**不能**把「descriptor 未声明 `image`/`file`」当作「不支持」（round9：负推断会拒绝一切）；**也不能**把「`Send()` 函数签名接收/调用了 helper」当作「语义支持」（round10：签名 ≠ bytes 真正进入请求）。真相必须逐 driver×mode 从源码证明：

| Backend | `file` | `image` | source 证据 |
|---|---|---|---|
| claudecode | ✅ | ✅ | image bytes→base64 block 进请求 parts（`session.go:902-925` `type:image,source:{base64}`）；file→`SaveFilesToDisk`+路径引用（`:927+`） |
| codex | ✅ | ✅ | `stageImages` 保留 path（`session.go:105`）；实现测试须覆盖 CLI 与 app-server 两模式（round10 矩阵要求） |
| opencode | ✅ | **mode-dependent** | CLI：`stageOpencodeImages` 保留 path 传 `--file`（`session.go:80,178-183`）✅；managed server：`server_session.go:103` `prompt, _, err :=` **丢弃** staged image path，HTTP body 只含 prompt（图像静默落盘丢失）❌；模式选择 `opencode.go:513-517`（baseURL 有→server，无→CLI） |
| grokbuild | ✅ | ❌ | file 路径拼进 text（`session.go:351-356`）✅；image block 只填 `Name`、无 bytes/MIME/URI（`session.go:363-368` + `acp_types.go:286-291` contentBlock 仅 text/uri/name）；冻结样本 `promptCapabilities:{"image":false}`（`acp_codec_test.go:531`，Grok CLI 0.2.93） |
| DSH | ❌ | ❌ | 一期 text-only（SDK 无 upload RPC / 无 file block） |

**OpenCode 唯一规则（round10 二选一，选 mode-aware）**：实现 **mode-aware attachment capability**——CLI 模式声明 `image`，managed server 模式**不声明**（server 路径图像本就静默丢失，声明即虚假能力）；`file` 两模式都声明。seam 先例：`deriveBackendCapabilities(id, agent, codexBackendMode)` 已按模式派生 capability（`backend_capabilities.go:5`），opencode 以 managed-server URL 有无作同等 mode 输入。**保守替代（不采纳）**：全模式不声明 OpenCode `image`——会拒绝 CLI fallback 现有可用的 image 输入，属产品行为收缩，须 owner 决策并记 regression，不能称「无回归」。

**正解：positive 声明 + 单一校验路径。**

1. **真相源同步（实现 change-set，随 driver/go-bridge 实现提交 + contract tests 一起交付——round10 A/B 决策选 B）**：claudecode/codex 加 `image`+`file`；opencode 加 `file` + mode-aware `image`；**grokbuild 只加 `file`（不加 image）**；**DSH 只声明 `text`**。同一 truth source（capability 派生，经 `deriveBackendCapabilities`），不得把「旧 driver 未声明」默认解释为「不支持」。
**`classifyAttachment` 单一分类（round11 P0-1）**：现有 `splitAttachments` 的真实分类是 `isImage := a.Kind == "image" || strings.HasPrefix(strings.ToLower(a.Mime), "image/")`（`attachments.go:41`）——**不按 raw `kind`**。若 pre-check 按 raw kind、split 按 kind∨mime 各判一套，`{kind:"file", mime:"image/png"}` 会先过 file-only gate（GrokBuild/DSH），再被 split 转成 `ImageAttachment` 绕过 v12 矩阵（Grok 进不可用 image block、OpenCode server 进静默丢图路径、DSH 无法稳定产出 `unsupported_attachment`）。**唯一规则（pre-check 与 `splitAttachments` 共用同一 classifier，不得复制第二套判断）**：

```text
effectiveKind = image,  若 kind == "image" 或 normalized mime 以 "image/" 开头
             = file,   否则
```

保留现有兼容语义（kind∨mime，round11 推荐项；「kind 为唯一权威」的 wire 收紧方案不采纳，见 §15）。

2. **单一校验路径**（`handleSendMessage` decode 后、`admitBridgeTurn`/`switchDir`/`getSession`/`StartSession`/`markRunning`/`splitAttachments` **之前**，无 session 副作用）：
   - a. **raw 结构校验**（遍历全部附件）：`kind ∈ {image,file}`；`mime` **非空**（canonical `AttachmentInput.mime: string` 必填）且符合 `type/subtype` 语法（trim+lowercase 后匹配 `^[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+$`，**不接受参数**如 `; charset=`）；base64 可解码且 decoded 非空。任一不过 → `invalid_params`（**整条消息拒绝**，不部分处理、不静默丢）。malformed MIME（`"not-a-mime"`/`"image"`/`"image/png; charset=utf-8"`）→ `invalid_params`（**有意收紧**：此前此类垃圾输入静默流入 driver，非有效路径回归）。
   - b. **support matrix**（按 backend positive 声明）：附件 **`effectiveKind`**（绝非 raw `kind`）必须被该 backend capability set **positively 包含**；否则 → `unsupported_attachment`。
   - c. 全过才进 StartSession/Send。
3. **`splitAttachments`（实现 change-set）**：改用**同一个 `classifyAttachment`**（分类行为等价于现 `isImage` 规则），并对空/invalid/decoded-empty base64 **返回 validation error**（不再 `continue` 静默跳过），由调用方在 (a) 映射 `invalid_params`。
4. **DSH driver `Send`（defense-in-depth）**：收到非空 slices 仍返 typed error（不伪造 attachmentId）。

**大小策略（round9 P0-1）**：`AttachmentInput` 无 `sizeBytes`，`splitAttachments` 现无任何大小限制，既有 backend 也没有 handler 级上限。**本期不引入 handler 级大小上限**（避免给 claudecode/codex/opencode/grokbuild 新增可能拒绝其现可接受大附件的限制）；故 **`attachment_too_large` 本期不在 acceptance 矩阵**。未来引入大小策略时，上限须 **per-backend positive 声明**（与 support 同源），不得用统一常量回归既有 backend。

**canonical 错误码**（`unified-bridge-protocol.md:171/179`）：本期仅 `unsupported_attachment`（support 不匹配）与 `invalid_params`（raw 结构非法）。pre-check 直接发 `WireError` 才产出 canonical code（send 路径无 `errors.Is` 分支，经 `Send` 只会变 `send_failed`）。capability 名 `image`/`file`（§8）。

**非回归 + 拒绝矩阵（round10：按 backend×mode 拆开，不写「四家一律不回归」）**：

| 场景 | claude / codex | opencode CLI | opencode server | grokbuild | DSH |
|---|---|---|---|---|---|
| valid file | ✅ 进 `Send` | ✅ 进 `Send` | ✅ 进 `Send` | ✅ 进 `Send` | `unsupported_attachment` |
| valid image | ✅ 进 `Send` | ✅ 进 `Send` | `unsupported_attachment`（mode-aware，图像不再静默丢） | `unsupported_attachment`（image=false 冻结样本） | `unsupported_attachment` |
| **`kind:"file", mime:"image/*"`（round11 fixture，effectiveKind=image）** | ✅ 走 image path 进 `Send` | ✅ 走 image path | `unsupported_attachment` | `unsupported_attachment` | `unsupported_attachment` |
| malformed MIME（`"not-a-mime"`/`"image"`/带 `;` 参数） | `invalid_params` | `invalid_params` | `invalid_params` | `invalid_params` | `invalid_params` |
| invalid base64 / 空 kind | `invalid_params` | `invalid_params` | `invalid_params` | `invalid_params` | `invalid_params` |
| mixed valid+invalid | `invalid_params`（整条拒绝） | 同左 | 同左 | 同左 | 同左 |

> opencode server 模式拒 image 是**现状语义化**（该路径图像本就静默丢失），不是新增回归；grokbuild 同理（ACP 声明 image=false）。既有 backend 的 **file 路径全部不回归**；claude/codex 的 image 不回归。

**contract test**：上表全部场景（含 opencode 双模式、grokbuild image 拒绝、**`kind:"file", mime:"image/*"` mismatch fixture 逐 backend**、malformed MIME 三例）+ 全部在 **pre-StartSession** 拒绝；真实 key 只验 provider 能消费 image，拒绝路径用 source-owned wire fixture。**fixture 实例化注意（round12）**：`image/*` 是 MIME family 示意写法、**不是合法字面值**（v13 MIME 正则会拒绝它）——contract fixture 必须实例化为具体 subtype（`image/png`、`image/jpeg` 等），不得按字面构造。

### 3.10 seq integrity + ignorable fail-closed（round7 P1-2 + round8 P0-2/P0-3）

#### 3.10.1 seq 完整性（round8 P0-2）

`SessionEvent` envelope 真实 shape（4 份 dump 987 条 + source `core/session/src/types.ts`）：顶层 `{type, seq, time, data}`，**`seq` 在 envelope 顶层**（`type`/`data` 的 sibling），**不在 `data` 内**。四份 dump 987/987 命中 `event.seq`，0 命中 `event.data.seq`（jq + awk 双策略复核；run1 另验 count=unique=233、0…232 连续）。

seq 是 **per-session 单调**（source：「Monotonic sequence number within the session」）。driver **只在 root session scope 内**维护 `expectedSeq`——**先做 §3.8 session-scope 路由过滤，再对 root event 校验**（child/foreign 的 seq 不推进 root expectedSeq）：

| 场景 | 判定 | 处理 |
|---|---|---|
| root 首 event（**必须 `seq==0`**） | DSH 不变量 `seq=log.length` 从 0 连续（`core/session/src/index.ts:564-566` `get seq(){return this.log.length}`、`:508-527` seed 必须 `seq==index` 从 0 连续）；SDK wire **无** replay/resume handshake 暴露可信 `firstLiveSeq`。首帧非 0 只能表示前缀已丢（丢 `turn/start`/request context），四份 dump 首 seq 均 0 | `seq==0` → 接受，`expectedSeq=1`；**`seq!=0` → fail visibly + 淘汰 process**（missing prefix，round9 P0-2） |
| `seq == expectedSeq` | 顺序 | 接受，`expectedSeq++` |
| `seq == expectedSeq-1` 且 **canonical JSON 与已见完全相同** | exact replay duplicate（重传同一 event） | **幂等跳过**，不推进 |
| `seq == expectedSeq-1` 但 payload 不同 | **conflicting duplicate**（协议损坏） | **fail visibly**：收口 turn + 淘汰 process |
| `seq > expectedSeq`（gap） | 可能丢了 turn/start / chunk / tool/result / compaction boundary | **fail visibly**：收口 turn + 淘汰 process |
| `seq < expectedSeq-1`（倒退） | 协议损坏 | **fail visibly**：收口 turn + 淘汰 process |

> round8 P0-2：v9「gap 仅记日志后继续」「same-seq 直接去重」会在丢掉 canonical record 后继续生成不完整但看似成功的 projection——不满足 fail-closed。只有 **exact-replay（同 seq + canonical 相同）** 可幂等跳过；gap / 倒退 / conflicting duplicate 必须 fail visibly + 淘汰 process。一期**不实现乱序重排**（不 buffer/flush gap），但必须 **detect-and-fail**，不能 detect-and-continue。

#### 3.10.2 ignorable fail-closed（round8 P0-3）

envelope `ignorable?: true`（`core/session/src/types.ts`）是 writer 给 reader 的**唯一**「未知语义也可安全跳过」承诺；**缺席 = required**。source 明确：reader 遇到无 marker 的未知 type **必须拒绝重建**（MUST refuse to reconstruct）。`KNOWN_SESSION_EVENT_TYPES`（44）只证明 pinned build **认识 name + schema**，**不等于**「可安全忽略」。

**四级分类**（修正 v9 的三级；round8 P0-2/P2）：

| 类别 | 判定 | 处理 |
|---|---|---|
| ① 已知 + 已映射 | §3.3 🟢 行 | 按 mapping rule 映射 |
| ② 已知 + **source-proven product-safe control/log** | 出现在真实 dump 且证明是控制面/诊断（`permission/preset`·`sandbox/mode`·`approval/policy`·`agent/inbox/spliced`·`session/title`），不影响 timeline 重建 | **逐项显式 ignore**，附 dump 证据；不进 timeline |
| ③ 已知但 **未实现 required 语义** | 在 44-name set 中但 driver 未实现，且 envelope 无 `ignorable:true`（`compaction/*` 会 surface replacement、`approval/asked`·`decided`、`command/*`、`tool-workflow/*`、`goal/change`·`plan/mode`·`schedule/change`·`session/end-seed`·`hook/*`·`llm/retry*`·`feedback/record`·`agent-preset/selected`·`session/title-llm-request`·`tool/code-dispatch*`·`web/*`·`subagent/descriptor`） | **fail visibly**：收口 active turn（`turn_error`），**不跳过**。「无样本」≠ safe-ignore（audit-plan：无样本只能标 unsupported，真正出现时拒绝解释） |
| ④ 任意 type + 有效 `ignorable:true` | envelope 显式标 `ignorable:true`（已知或未知均可） | 记录诊断后跳过 |

**额外校验**：`ignorable:false` 或非布尔 → 按 required 处理（fail visibly），不当作 safe-skip。一期 4 份 dump 中 `ignorable` 出现 **0 次**——实践中所有 observed event 都是 required；safe-skip 通道仅 ④（marker=true）。

**frozen inventory**：`scripts/dsh-gate0/known-event-types.txt`（44 种，从 pinned `packages/core/session/src/known-event-types.ts` 快照，**已 committed**；DSH 每次 re-pin 用 `pnpm run gen-persistence-catalog` 重新生成并比对无漂移）。44-name 集合**只**用于「认识 vs 不认识」判定；safe-ignore **只认** `ignorable:true` marker（类别 ④）。

> **测试 deferred（§15）**：pinned source schema 的 wire fixture 构造 exact-replay / gap / conflicting-dup / ignorable-required / ignorable-true / known-unimplemented-required 场景（验收矩阵 #4, #5）；不需要真实 key。

---

## 4. session 生命周期（live-only）

| 操作 | 一期 | 说明 |
|---|---|---|
| 创建/发送 | ✅ `session/prompt`（text-only，§3.9） | `{messageId}`；at-most-once delivery（§3.6.3 ③） |
| 取消 | ❌ kill 进程 | 触发 §3.6.3 ② typed process death（terminal → CAS 淘汰 → Close → 再发新 nonce），sessionId 不变 |
| 列出/resume | ❌ | live-only |
| 图片/文件附件 | ❌ | 一期 text-only；**raw 校验优先**：坏结构 → `invalid_params`，结构有效但 DSH 不支持 → `unsupported_attachment`（§3.9），StartSession 前 |

**`ListSessions`（round4 P1-4 澄清）**：driver 返回 `nil, core.ErrNotSupported`。**当前 go-bridge generic handler（handlers.go:2628）把任何 error（含 ErrNotSupported）映射为 wire `list_failed`**，不是 `not_supported`。文档如实：一期 wire 表现为 `list_failed`（不伪造空成功）；若要 `not_supported` code，driver 主体合入时在 handler 加 `errors.Is(ErrNotSupported)` 分支（handlers.go:1431/1605 已有此模式的先例）。

---

## 5-7. 取消关闭 / 错误 / 脱敏

Close 三阶段；正常 `shutdown→{}` 🟢，SIGTERM/SIGKILL ⚪。错误 → `EventError` **先收口 turn**（§3.6.3 ② typed process death）；仅 typed process-exit/channel-close 触发 registry 淘汰。dumps 经 `sanitize.py` 递归 scrub + **peer 存在断言**（round4 P1）。

---

## 8. capability

`LiveEventSessionProcess`，无 `external_turn_streaming`。🟢 session_state/workspace_diff/diagnostics；⚠️ todos（须 FetchTodos 持久化读）/usage_reporting（须跨 turn 聚合）；❌ session_history/permission_resolve/supports_checkpoint。一期 text-only：DSH descriptor 只声明 `text`、**不声明** `image`/`file`（canonical 名，非 `attachments`）。attachment gate 走 **positive 声明**（§3.9，非「缺席=不支持」负推断）：DSH 缺 `image`/`file` 声明 → **结构有效**的 image/file pre-check 返 `unsupported_attachment`；**raw 结构非法**（空 kind/坏 mime/坏 base64/mixed）返 `invalid_params`（§3.9 单一路径 a→b）。notification session scope 由 driver client-side 过滤（§3.8）。

---

## 9-11. protocol / 文件 / 三仓

无 bridge-v1 change（除非 turn 内多 message，§3.6）。driver `config/cordis.yml`=`scripts/dsh-gate0/driver-cordis.yml`。`BuildAgentEnv`（`core/message.go:134`，core 包、非 go-bridge 目录）注入 `DSH_PERMISSION_MODE`；iOS 侧为**待实施项**：`BackendKind`（iOS 仓 `OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift`）现有 case 为 openCode/codex/claudeCode/copilot/thinBridge/grokBuild，**不含且历史上从未有过** `deepSeek`——实现时需**新增** `case deepSeek`（含 wire kind 字符串映射）并保持不展示历史。

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

1. pre-release 漂移；2. 取消即触发 typed process death（kill → terminal → CAS 淘汰 → Close → 再发新 nonce；同 turn 不重放，at-most-once §3.6.3 ③）；3. 无 list/resume；4. approval 不经协议；5. DeepSeek 绑定；6. reducer 模型约束（turn 内多 step 拼接）；7. ⚪ 证据缺口；8. child/foreign session notification 必须由 driver 过滤（§3.8），lineage tombstone 保留到 teardown（不因 finished 删）；9. 一期 text-only：坏结构附件 → `invalid_params`、结构有效但 DSH 不支持 → `unsupported_attachment`（§3.9），不声明 `image`/`file`；10. unknown / known-unimplemented event 必须 fail-closed（§3.10.2，只认 `ignorable:true`）；11. seq gap/倒退/conflicting-duplicate 必须 fail visibly + 淘汰（§3.10.1，不 detect-and-continue）；12. **本版为设计修订 + 2 个 committed artifact（known-event-types.txt + gen-known-event-types.py），未写 driver/go-bridge 代码（含 descriptor truth-source，A/B 选 B 随实现交付）、无 fault-injection 证据——非"已落地"**。

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

### Round8（v9→v10）
| 项 | v9 问题 | v10 修订 | 状态 |
|---|---|---|---|
| P0-1 | `subagent.finished` 删 lineage → 迟到 child 降级 foreign；foreign 留「或 drop」二选一；rootSessionID 称「SDK 分配」 | §3.8 lineage tombstone 保留到 teardown（对齐 client.ts:403-415 只 started 记录）；foreign 唯一策略=fail+terminate；rootSessionID=driver 发送的 session/prompt id（SDK 不分配，types.ts:34-38/server.ts:132-142） | ✅ 设计；fixture 测试 deferred |
| P0-2 | seq 写 `data.seq`（实为 envelope `event.seq`）；gap 记日志、dup 直接去重非 fail-closed | §3.10.1 envelope `event.seq`（987/987 实证）；per-root expectedSeq；exact-replay 幂等跳过；gap/倒退/conflicting-dup fail visibly+淘汰；scope 过滤先于 seq | ✅ 设计；wire fixture 测试 deferred |
| P0-3 | known+deferred 一律跳过（把「认识名」当「safe-ignore」） | §3.10.2 四级分类；只 `ignorable:true` 可跳过；known-unimplemented required（compaction 等）fail visibly；无样本≠safe-ignore | ✅ 设计；wire fixture 测试 deferred |
| P0-4 | attachment 拒绝放 driver 层，splitAttachments 已静默丢；错码 `not_supported`、capability `attachments` | §3.9 拒绝移到 go-bridge pre-check（StartSession 前，raw inventory，capability image/file）；canonical `unsupported_attachment`；splitAttachments 返 validation error；§8 capability=image/file | ✅ 设计；contract test deferred |
| P1-1 | delivery classification 缺 Go contract | §3.6.3 ③ `DeliveryError{Stage}` + go-bridge `errors.As` 矩阵（PreWrite 可重建；Partial/Awaiting/Unknown fail visibly） | ✅ 设计；fault-injection deferred |
| P1-2 | session.status 无 core.Event 载体（core.EventType 无 status 类型） | §3.4 一期 driver-internal 诊断 only，不发 core.Event/不投影 iOS；UI 由 bridge admission+turn terminal 驱动 | ✅ |
| P1-3 | 引用不存在的 known-event-types.txt | 生成 + commit `scripts/dsh-gate0/known-event-types.txt`（44 种，pinned source 快照） | ✅ artifact |
| P1-4 | v9 commit 只改 MD 却称「落地」 | §13/§15 如实标注：设计修订 + 1 artifact，**非 landed**；fault/contract tests 仍 deferred | ✅ 如实 |
| P2 | nonce「2^-128 birthday」不准；§3.9 `not_supported`/`send_failed` 不一致；§3.10「三级」实为四类 | nonce 改「工程上碰撞可忽略」；§3.9 统一 `unsupported_attachment`；§3.10 改四级 | ✅ |

### Round9（v10→v11）
| 项 | v10 问题 | v11 修订 | 状态 |
|---|---|---|---|
| P0-1 | capability 负推断（缺席=不支持）会回归 claude/codex/opencode/grokbuild（其 `Send()` 实已处理 image+file，但 `StaticCapabilities` 未声明）；validation 顺序无法产出 `invalid_params`；`attachment_too_large` 无字段/上限 | §3.9 改 **positive 声明**（4 既有 driver 加 `image`/`file`，DSH text-only，共用 `StaticCapabilities` 真相源）；单一校验路径 raw 结构（`invalid_params`）→ support（`unsupported_attachment`）；本期不引入大小上限，`attachment_too_large` 移出 acceptance | ✅ 设计；contract test deferred |
| P0-2 | root 首 event 任意 seq 建基线会掩盖前缀丢失 | §3.10.1 强制首 event `seq==0`（DSH `index.ts:564-566`/`:508-527` 从 0 连续，无 replay handshake）；非 0 → fail visibly+淘汰 | ✅ 设计；fixture deferred |
| P1-1 | `session/title` 同时「映射标题」+「ignore」；core 无 title 载体 | §3.3 `session/title` 改 driver 内部诊断（与 session.status 同；`core/message.go:527` 无 title） | ✅ |
| P1-2 | foreign `session.status` 与「任何 foreign 终止」冲突 | §3.8 路由矩阵拆 root/descendant/foreign 三行；foreign status=fail+terminate | ✅ |
| P1-3 | recoverable turn error 与 fatal protocol violation 混一类 | §3.6.3 ② 二分：application error（合法 `EventError`）保留 process；protocol/codec violation（framing/seq/scope/invariant）淘汰 | ✅ |
| P1-4 | 正文 6 处 stale/二选一（清 lineage×2、known+deferred、processed-seq/gap、source 二选一×2） | §3.0/§3.3/§3.6.2/§3.7/§3.8 逐项统一为 tombstone/fail-closed/marker 规则 | ✅ |
| P2 | `known-event-types.txt` 再生成命令带省略号不可执行 | 新增可执行 `gen-known-event-types.py`（verify+`--write`，已测 drift 检出）；`.txt` 注释改指脚本 | ✅ artifact |

### Round10（v11→v12）
| 项 | v11 问题 | v12 修订 | 状态 |
|---|---|---|---|
| P0-1 | 「4 家 `Send()` 已处理 image+file」前提不成立：Grok 冻结样本 `promptCapabilities.image=false`（image block 只填 Name，无 bytes/MIME/URI）；OpenCode 双路径分裂（CLI `--file` ✅ / managed server 丢弃 staged path ❌）——「调用了 helper」≠「语义支持」 | §3.9 矩阵改 **source-proven 逐 driver×mode**：claude/codex `image+file`；opencode `file`+**mode-aware image**（唯一规则，CLI 声明、server 不声明——server 拒绝是现状语义化非回归；保守全拒方案不采纳：会收缩 CLI 现有可用 image，属产品决策须 owner 批）；grokbuild 只 `file`；DSH text-only。非回归矩阵按 backend×mode 拆开。A/B 决策：**选 B**（descriptor 物理修改随 driver/go-bridge 实现提交 + contract tests 交付，不做 v11 的方案 A） | ✅ 设计；truth-source 同步为实现 change-set |
| P1-1 | §3.4 残留「child/foreign 不影响 root」与 §3.8 foreign fail+terminate 冲突 | §3.4 拆 descendant（过滤）/foreign（fail+terminate）两行 | ✅ |
| P1-2 | §8/§4/§13 残留「非空附件一律 `unsupported_attachment`」与 §3.9 raw 校验优先冲突 | §4 表/§8/§13 风险 9/§15 行统一为：坏结构 → `invalid_params`；结构有效但不支持 → `unsupported_attachment` | ✅ |
| P2 | `gen-known-event-types.py --write` 生成降级 header（丢 pinned SHA 与 ignorable 语义）；`str \| None`/`list[str]` 需 Py≥3.10 | `--write` 保留 Semantics block + usage lines，SHA 从 `DSH_ROOT` `git rev-parse HEAD` 读取（fail-closed）；`from __future__ import annotations` 兼容 3.9；已实测 verify/drift/`--write` 三模式 + artifact 用新脚本再生成（byte-stable） | ✅ artifact |

### Round11（v12→v13）
| 项 | v12 问题 | v13 修订 | 状态 |
|---|---|---|---|
| P0-1 | capability gate 按 raw `kind` 判断，`splitAttachments` 实际按 `kind=="image" ∨ mime 前缀 image/`（attachments.go:41）重分类；`{kind:"file", mime:"image/png"}` 绕过 file-only gate（Grok 进不可用 image block / OC server 进静默丢图 / DSH 错误码不稳定） | §3.9 新增 **`classifyAttachment` 单一规则**（effectiveKind=kind∨mime，pre-check 与 split 共用、禁止第二套判断）；support gate 改查 effectiveKind；raw 校验补 MIME `type/subtype` 语法（malformed 含 `;` 参数 → `invalid_params`，有意收紧）；矩阵新增 `kind:"file", mime:"image/*"` fixture 行（逐 backend）。「kind 唯一权威」备选不采纳（§15） | ✅ 设计；contract test deferred |
| P2 | generator `--write` 读 DSH **working tree** 却 stamp HEAD SHA：dirty worktree + 新增唯一事件会生成 45 类 artifact 且标为 clean HEAD（审计员负向复现） | `--write` 改经 `git show HEAD:<path>` 读 **exact committed blob**（内容与 SHA 同源，dirty 不再泄漏——已复现审计员场景验证 2 类非 3 类）；verify 新增 **provenance drift** 检查（artifact stamp ≠ DSH HEAD 即 exit≠0，同 set 也报）；missing artifact 改干净报错；已测 8 场景（clean verify/3.9 二进制/verify、dirty 不泄漏、set drift、provenance-only drift、full match、真实 repo） | ✅ artifact |

### Round12（v13）：**APPROVE —— 设计放行**

两项 round11 阻断实证关闭（effectiveKind 唯一规则 + 对抗输入逐 backend 推导一致；generator HEAD-blob/provenance/Py3.9 全场景通过）。两条非阻断实现注意已采纳：① contract fixture 中 `image/*` 须实例化为具体 subtype（正则会拒绝字面 `image/*`），已注 §3.9；② artifact 已用新脚本 `--write` 重生成对齐 header（event set/SHA 本就正确）。

**设计层停止循环审计；下一阶段 = driver/go-bridge 实现 + §16 验收门槛。**

### Round1-4 闭环：见前版（descriptor/resume/权限/计数/Gate0/identity/双写owner/环境切换/source.kind/usage/block/ListSessions/todo/自包含）。

---

## 15. 不采纳 / deferred

| 审计建议 | 处理 | 原因 |
|---|---|---|
| replacement session（abort 返回新 sessionId + iOS 切换） | 不采纳（选 process-nonce 方案） | replacement 需改 go-bridge handleAbortGeneration + iOS session 流（跨仓协议改动）；process-nonce + typed process death 更局部，不改 session 流/rebind |
| 自动 retry（transport error 后重发同一 prompt） | 不采纳（违反 at-most-once） | DSH server 先 `followup` 入队再返回 `messageId`；transport error 不能证明未送达。需 SDK idempotency key 才能安全 retry，当前协议不支持。v9 以 delivery classification 替代（§3.6.3 ③） |
| 图片/文件附件上传 | 不采纳（一期 text-only） | DSH SDK 无 upload RPC；`ImageAttachmentRef` 需要 attachment service 对接；`ContentBlock` 无通用 file block。**拒绝放 go-bridge pre-check**（splitAttachments 之前），坏结构 → `invalid_params`、有效但不支持（按 `effectiveKind`）→ `unsupported_attachment`（§3.9），不在 driver 层 |
| 「`kind` 为唯一分类权威」（`kind:"file", mime:"image/*"` 一律按 file，round11 备选） | 不采纳（保留 kind∨mime 兼容规则） | 现链路（splitAttachments `isImage`）对 `kind:"file", mime:"image/*"` **已按 image 处理**；改为 kind 权威会把这类输入从 image 路径改到 file 路径，是对现存输入类别的行为改写（wire 语义收紧），须协议兼容说明 + 确认无发送方依赖——超出本 driver 任务。兼容规则（effectiveKind=kind∨mime）保持现网行为，仅让 gate 与 split 用同一判断并新增拒绝矩阵，不改写任何允许路径 |
| subagent timeline 展示 | 不采纳（一期过滤） | child event 显式过滤不进 root codec（§3.8）。若未来支持需扩展 codec：per-session stream、lineage-aware StreamID、parent/child 关联 |
| `ProjectionReducer` 级测试 | deferred | driver 测试代码。identity 已对齐 reducer 源码（L535+L16-17）+ source.kind 分流 + process nonce + typed process death + source-proven 校验 |
| sanitizer seen-flags / cardinality / 负向 fixture（round6 P1-3） | deferred | sanitizer 现用内容 truthiness（`s['text']`）兼任「见过」与「内容」，单侧空 peer 仍通过；doc 已如实收紧为「非空 peer 缺失即失败」。改 seen-flags + assembled cardinality + 负向 fixture 属 driver 测试基建 |
| crash→respawn / at-most-once / typed death 实现测试（round6 P0-1 + round7 P0-2/P0-3） | deferred | driver + go-bridge 代码：验收矩阵 #6-#9, #12 的 fault-injection 场景（pre-send dead、zero-byte/partial/full send、response lost、健康 error 后继续、process exit、abort/crash/stale relay 并发）。设计闭环见 §3.6.3（已对齐 `deleteSession`/`relayEvents` defer/`sess.Send`/`prompt()` 源码） |
| notification session scope 实现测试（round7 P0-1） | deferred | driver + go-bridge 代码：验收矩阵 #2-#5 的 subagent/foreign/ignorable 场景。设计闭环见 §3.8（已对齐 `types.ts:50-104`/`server.ts:71-103`/`client.ts:354-371`） |
| attachment contract test（round7-11 P0 链） | deferred | go-bridge pre-check + driver 代码 + descriptor truth-source 同步 + `classifyAttachment` 共用重构（随实现提交）：text-only ✅ / DSH valid image·file → `unsupported_attachment` / 坏 base64·malformed MIME（`not-a-mime`/`image`/带 `;`）/ 空 kind → `invalid_params` / mixed → `invalid_params`（整条拒绝）/ **`kind:"file", mime:"image/*"` → effectiveKind=image：DSH·grok·OC-server → `unsupported_attachment`，claude·codex·OC-CLI → image path** / claude·codex valid image+file → 进 `Send` / opencode CLI image ✅、server image → `unsupported_attachment`（mode-aware）/ grokbuild image → `unsupported_attachment`（全部 pre-StartSession）。设计见 §3.9 |
| durable notification dump（round7 P1-3） | deferred | gate helper 把全部 4 类 notification 以带 method/params 的 sanitized JSONL 保存；当前证据为 `.stdout` / `run-output.txt` |
| `block-start/end → newPart` 扩展 core.Event | 一期不扩展 | core.Event 无 newPart；一期自然合并（同 turn 多 block text 拼接）。若要 block 级独立 part，扩展 core.Event+wire（协议改动） |
| Gate helper 完整 CI gate（malformed/join） | 部分采纳 | 已修：env 转发/O_TRUNC/sendAndWait 竞态/rpc-error/PARTIAL exit 1/peer 断言。完整 malformed/join 属 driver 测试基建 |
| fail-closed durable fixture / protocol pack 同步 | deferred | 测试基建/跨仓协议 |

**main.go v7 实际修复**：① `DSH_PERMISSION_MODE`/`DSH_SYSTEM_PROMPT` 转发；② dump `O_TRUNC`；③ `sendAndWait` 先注册后写 + rpc-error 返回 nil（fatal exit 1）；④ **PARTIAL→`os.Exit(1)`**；⑤ error response 不打印 OK；⑥ **gofmt 干净 + go vet 无诊断**。
**sanitize.py v7**：递归 scrub + **双向 peer 断言**（chunk→assembled 与 assembled→chunk 双向，**非空** peer 缺失即失败；负向样本 assembled-only/chunk-only 均 FAIL）+ 无路径断言。**已知边界（round6 P1-3）**：断言用内容 truthiness（`s['text']`/`s['atext']`）兼任「见过记录」与「拼接内容」，单侧空 text delta / 单侧空 assembled text 仍通过；当前真实 dump 无空块，既有结论不受影响。完整「缺任一 peer 即失败」需 seen-flags + assembled cardinality + 负向 fixture，见 §15 deferred。

**当前结论（v13）**：协议选型（SDK JSON-RPC，3 request + 4 notification）、事件映射（自包含 + source.kind 分流 + ignorable 四级 fail-closed + `session/title` 内部诊断）、identity（对齐 reducer + process nonce + source-proven turn/step 校验）、**seq 完整性（envelope `event.seq` + 首 seq=0 + per-root fail-closed）**、交付语义（at-most-once delivery + Go `DeliveryError` contract）、**错误分类（application recoverable vs protocol/codec fatal）**、进程生命周期（typed process death + CAS Close ownership）、notification session scope（root/descendant/foreign 三分 + lineage tombstone 保留到 teardown）、attachment 策略（一期 text-only + source-proven positive 矩阵 + **`classifyAttachment` 单一分类（effectiveKind，gate 与 split 共用）** + go-bridge pre-check + canonical `unsupported_attachment`/`invalid_params` + MIME 语法校验）、session.status 消费规则（driver-internal only）、双写去重（peer 断言）、usage 公式、权限 composition（环境切换）十三块设计完整且有真实/源码证据；正文无互斥规则、无二选一。

**⚠ 诚实边界（round8 P1-4 + round9 P1-4 + round10 A/B=B + round11）**：本版为**设计修订 + 2 个 committed artifact（`known-event-types.txt` + `gen-known-event-types.py`：HEAD-blob 读取、SHA 同源 stamp、provenance drift 检查，8 场景实测）**；**driver/go-bridge 代码尚未编写（含 capability truth-source descriptor 物理修改与 `classifyAttachment` 重构——按 owner A/B 决策选 B，随实现提交 + contract tests 交付），fault-injection / contract / fixture 测试尚未提交——非"已落地"**。每条设计规则已对齐其依赖的真实接口（DSH `types.ts`/`server.ts`/`client.ts`/`known-event-types.ts`/`index.ts`、MacBridge `core/interfaces.go`/`attachments.go`（含分类行 41）/`backend_capabilities.go`/`handlers.go`/`projection_reducer.go`/canonical protocol）。剩余实现：capability truth-source 同步（claude/codex `image+file`、opencode `file`+mode-aware `image`、grokbuild `file`、DSH `text`）、`classifyAttachment` 共用重构、respawn/at-most-once/typed-death fault-injection、notification scope + lineage fixture、seq=0/gap/conflicting-dup wire fixture、attachment raw-wire rejection test（含 opencode 双模式、grok image 拒绝、kind/mime mismatch fixture、malformed MIME）、`DeliveryError` errors.As 矩阵、CAS eviction/Close-once race、reducer frozen-sample、sanitizer seen-flags、durable notification dump、protocol 同步。

---

## 16. 交接验收门槛（round12 APPROVE 附带，开发 agent 必须与实现同交）

设计已放行（round12），但以下测试**必须与实现代码同批交付**，不得以「设计完成」宣称 landed：

1. **attachment matrix**：raw malformed、mixed、effectiveKind mismatch（`kind:"file", mime:"image/png"` 等，实例化具体 subtype）、DSH/Grok/OpenCode server 拒绝、Claude/Codex/OpenCode CLI 不回归，全部断言 pre-StartSession。
2. **capability truth source**：Claude/Codex `image+file`，OpenCode `file` + mode-aware image，Grok file-only，DSH text-only。
3. **seq fixture**：首帧 0、首帧非 0、exact replay、gap、倒退、conflicting duplicate。
4. **notification scope**：root/descendant/foreign event 与 status、finish 后迟到 child、grandchild、foreign parent、id reuse。
5. **delivery fault matrix**：pre-write、zero-byte、partial、response lost、accepted unknown。
6. **process lifecycle**：application error 保留 session，framing/seq/scope/invariant fatal 淘汰，CAS Close-once。
7. **reducer frozen samples**：user/assistant 同 turn、plugin 不进 timeline、turn/step mismatch fail、nonce 重启不冲突。
8. **generator/protocol sync**：repo 内 `gen-known-event-types.py` verify 保持 exit 0；protocol pack / iOS mirror 如实现引入 wire 契约变化则同步更新（canonical 先行）。
