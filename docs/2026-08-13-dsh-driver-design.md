# DeepSeek Harness (DSH) Driver 设计文档

- **日期**：2026-08-13（v8：round6 审计 —— source-proven turn/step 校验、crash→respawn 闭环、nonce 128-bit）
- **Runtime**：DeepSeek Harness `dsh-jsonrpc-agent`（pinned `47f943859bef60e4160492346772ded9b24f765a`，协议 `0.0.1`，pre-release）
- **协议**：DSH SDK JSON-RPC 2.0 over stdio（**非** ACP）
- **证据**：`scripts/dsh-gate0/`（4 次真实 run + assembled composition + sanitizer 带 peer 断言）
- **审计**：round1-4（`docs/2026-08-13-dsh-driver-design-audit-round{1,2,3,4}.md`）、round5/6（`docs/2026-08-1{3,4}-dsh-driver-design-audit-round{5,6}.md`）

---

## 0. 协议选型

SDK 协议满足 rich timeline（reasoning/tool/todo/usage），ACP 显式丢弃。`KNOWN_SESSION_EVENT_TYPES`=44。

---

## 1-2. Runtime / 进程参数

`dsh-jsonrpc-agent <cordis.yml>`，强制 config。env：`DEEPSEEK_API_KEY`/`DEEPSEEK_BASE_URL`/`DSH_CWD`/`DSH_SESSION_ROOT`/`DSH_PERMISSION_MODE`（driver 显式注入）。stdout 是协议通道；进程组复用 grokbuild。

---

## 3. 输入输出 schema

### 3.1-3.2 传输 / 握手

newline-delimited JSON-RPC 2.0。**无 cancel/session-close/session-resume/load/list**。`initialize`→`{serverInfo:{name:'deepseek-harness-sdk-runtime'}}`；`session/prompt` 惰性创建。

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
| 其余（compaction/approval-asked/command/tool-workflow/goal-plan-schedule/session-end-seed 等） | ⚪ | ignored/deferred | 无样本 |

### 3.4 turn 生命周期

`turn/start`+`turn/end`（reason 6 态）+ `session.status`。`session/prompt` 只回 `{messageId}`，turn 完成只认 `turn/end`。

### 3.5 权限模型

权限栈在 `bundle/base`。assembled composition（`driver-cordis.yml`）：🟢 可加载+激活+fail-closed（unconfined executor 拒绝）；🟢 **环境切换 verified**（run3 `workspace-write`+`ask` vs run4 `danger-full-access`+`never`）。一期 `permission_resolve` 不声明。

### 3.6 identity + source.kind 分流 + process nonce（v8：round5/6 P0）

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

#### 3.6.3 process nonce + crash→respawn 生命周期（round5 P0-2 + round6 P0-1/P1-2）

**v6 错误**：`processGen` 存 driver 内存，go-bridge/Agent 重启归零；原 sessionId 再传入 `StartSession` → `g0-t1` 复用（v6 "重启换 sessionId"断言无源码支撑：`handleAbortGeneration` 只返回 `{ok:true}`，`handleSendMessage` 把原 id 当 resumeID 传 `StartSession`）。

**v7 错误（round6 P0-1）**：process nonce 只证明「新 spawn 已生成不冲突 TurnID」，没证明「crash 后必触发新 spawn」。当前 go-bridge `relayEvents` 的 defer（`handlers_relay.go:2451-2455`）只清 `agentRelayRunning`/`relayRunningKind`，**不删 session registry**；`handleSendMessage`（`handlers.go:1975-1976`）对 `ok=true && sess!=nil` 直接 `sess.Send`（`handlers.go:2070`），错误只映射成 wire `send_failed`，不删除 session、不 respawn、不重试。子进程 crash → 死亡 session 留在 registry → 下一次发送复用同一对象 → 不进入 `StartSession` → 不生成新 nonce。abort 与完整 runtime 重启能到 `StartSession`，**crash 路径未闭环**。

**v8 方案（process nonce + crash-respawn 闭环）**：

1. **nonce**：每次 spawn 用 `crypto/rand` 生成 **16 字节（128-bit）** 随机 hex（round6 P1-2：8 字节仅概率唯一，无法支撑「必新」承诺；16 字节成本为零且消除 birthday 隐患）。`rand.Read` 失败 → **spawn fail-closed**（不退时间戳/零值 fallback）。nonce 无需持久化，但必须在启动子进程**前**成功生成并固定到该 process lifetime。`TurnID = "p{nonce}-t{turn}"`。

2. **crash→respawn owner（round6 P0-1，唯一 owner = go-bridge，primitive = `deleteSession` `handlers.go:637`）**：
   - **eager 淘汰**：`relayEvents` 对 DSH backend 收到 terminal（非 claude `EventError` / events channel close）时，按 **session 对象身份 CAS** 删除 registry 项——仅当 registry 仍持有本次 relay 读的同一 `sess` 对象才删（镜像 `clearRelayKindIf` 的 compare-and-delete 模式 `handlers_relay.go:619`），避免旧 relay defer 清掉并发新 spawn 的新 session。**失败 turn 必须先以原 nonce 的 `turn_error` 收口**：现有 `EventError → turn_error` 路径（`handlers_relay.go:2558-2584`）已满足，**不重放失败 prompt**。
   - **lazy 淘汰（idle crash）**：turn 正常完成后、idle 期 crash 时无活跃 reader，relay 早已退出，eager 不触发。下一次 `handleSendMessage` 的 `sess.Send` 返回 transport 错误（broken pipe）；该错误必须触发同样的 CAS 淘汰 + 重入 `StartSession`（driver spawn 新进程 + 新 nonce + 新 codec）+ **对这条新 prompt 重试一次**（重试的是崩溃后新发的 prompt，不是崩溃 turn）。
   - 淘汰后 `getSession` 返回 `ok=false` → `handleSendMessage` 进入既有 `StartSession` 分支（`handlers.go:1979-2042`）→ driver spawn 新进程 + 新 nonce。

3. **race 边界**：abort 与 crash 并发 → 对象身份 CAS 使淘汰幂等（首触发淘汰，次为 no-op）；并发 send 已有 double-checked locking（`handlers.go:2048-2059`），新 session 不会被 stale relay defer 误清。eager 与 lazy 不互斥：eager 已淘汰的 session lazy 不再命中（对象已变）。

4. **三场景全覆盖**：abort → kill → terminal → 淘汰 → 再发 `StartSession`（新 nonce）；crash → terminal → 淘汰 → 再发（新 nonce）；go-bridge 重启 → registry 空 → `StartSession`（新 nonce）。**均 `t1` 不复用**。

5. 不改 session 协议（原 sessionId，不碰 rebind）；projection key=TurnID（含 nonce）。

> **respawn 实现测试 deferred（§15）**：driver + go-bridge 代码；冻结样本至少覆盖 turn 中 crash、turn 后 idle crash、crash+abort 并发、crash 后首次新 send 四态。

> **替代方案（不采纳，§15）**：replacement session（abort 返回新 sessionId + iOS 切换，跨仓协议改动）/ 持久化 `(backend,outerSessionID)→generation`（需原子递增/恢复定义）—— process-nonce + go-bridge 对象身份淘汰最局部、无需持久化、不碰 session 流/rebind。

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

---

## 4. session 生命周期（live-only）

| 操作 | 一期 | 说明 |
|---|---|---|
| 创建/发送 | ✅ `session/prompt` | `{messageId}` |
| 取消 | ❌ kill 进程 | 触发 §3.6.3 crash-respawn（terminal → 淘汰 → 再发新 nonce），sessionId 不变 |
| 列出/resume | ❌ | live-only |

**`ListSessions`（round4 P1-4 澄清）**：driver 返回 `nil, core.ErrNotSupported`。**当前 go-bridge generic handler（handlers.go:2628）把任何 error（含 ErrNotSupported）映射为 wire `list_failed`**，不是 `not_supported`。文档如实：一期 wire 表现为 `list_failed`（不伪造空成功）；若要 `not_supported` code，driver 主体合入时在 handler 加 `errors.Is(ErrNotSupported)` 分支（handlers.go:1431/1605 已有此模式的先例）。

---

## 5-7. 取消关闭 / 错误 / 脱敏

Close 三阶段；正常 `shutdown→{}` 🟢，SIGTERM/SIGKILL ⚪。错误→`EventError`+fail visibly。dumps 经 `sanitize.py` 递归 scrub + **peer 存在断言**（round4 P1）。

---

## 8. capability

`LiveEventSessionProcess`，无 `external_turn_streaming`。🟢 session_state/workspace_diff/diagnostics；⚠️ todos（须 FetchTodos 持久化读）/usage_reporting（须跨 turn 聚合）；❌ session_history/permission_resolve/supports_checkpoint。

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

1. pre-release 漂移；2. 取消即触发 crash-respawn（kill → terminal → 淘汰 → 再发新 nonce；turn 隔代，同 turn 不重放）；3. 无 list/resume；4. approval 不经协议；5. DeepSeek 绑定；6. reducer 模型约束（turn 内多 step 拼接）；7. ⚪ 证据缺口。

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
| P0-1 | nonce 只保证新 spawn 新 ID；crash 后死亡 session 留 registry，再发不走 `StartSession` | §3.6.3 crash→respawn 闭环：relay terminal 按 session 对象身份 CAS `deleteSession`（eager）+ `sess.Send` transport 错误触发淘汰重试（lazy，覆盖 idle crash）；失败 turn 先 `turn_error` 收口，不重放 | ✅ 设计；respawn 四态测试 deferred |
| P0-2 | assistant/tool 行只「用 active」，未校验 source turn/step | §3.6.1 validate-then-map：除 user 外 turn-scoped frame 校验 `data.turn/step==active`，mismatch fail visibly；step 嵌套/未闭合校验 | ✅ 设计；reducer 测试 deferred |
| P1-1 | identity 矩阵写「TurnID（所有 event）」与 34 条 turnless shape 冲突 | §3.6.1 矩阵限定 turn-scoped core.Event；control-plane/session/internal 不赋 TurnID | ✅ |
| P1-2 | nonce 8 字节仅概率唯一、无 `crypto/rand` 失败策略 | 16 字节（128-bit）+ `rand.Read` 失败 fail-closed（spawn 失败，不 fallback 时间戳/零值） | ✅ 设计 |
| P1-3 | sanitizer 用内容 truthiness，单侧空 peer 仍通过 | §3.7/§15 如实收紧为「非空 peer 缺失即失败」；seen-flags + cardinality + 负向 fixture 属 driver 测试基建 deferred | 🟡 doc 已如实；代码 deferred |
| P1-4 | §4/§12/§13/§15 仍残留 processGen/generation/v6 真值，审计索引缺 round5/6 | 统一为 nonce + crash-respawn；§12 标题去 v6；审计索引补 round5/6；header→v8 | ✅ |
| P1-5 | README 仍 round4/v6 + 仅 chunk→assembled 单向 peer | README 同步 v8 + 双向 peer + 空内容边界；durable 负向 fixture/test deferred | ✅ 文本；fixture deferred |
| P2 | evidence 目录未跟踪 `__pycache__` | 根 `.gitignore` 忽略 `__pycache__/` | ✅ |

### Round1-4 闭环：见前版（descriptor/resume/权限/计数/Gate0/identity/双写owner/环境切换/source.kind/usage/block/ListSessions/todo/自包含）。

---

## 15. 不采纳 / deferred

| 审计建议 | 处理 | 原因 |
|---|---|---|
| replacement session（abort 返回新 sessionId + iOS 切换） | 不采纳（选 process-nonce 方案） | replacement 需改 go-bridge handleAbortGeneration + iOS session 流（跨仓协议改动）；process-nonce + go-bridge 对象身份淘汰更局部，不改 session 流/rebind |
| `ProjectionReducer` 级测试 | deferred | driver 测试代码；本次限定"优化文档"。identity 已对齐 reducer 源码（L535+L16-17）+ source.kind 分流 + process nonce + crash-respawn 淘汰 + source-proven 校验 |
| sanitizer seen-flags / cardinality / 负向 fixture（round6 P1-3） | deferred | sanitizer 现用内容 truthiness（`s['text']`）兼任「见过」与「内容」，单侧空 peer 仍通过；doc 已如实收紧为「非空 peer 缺失即失败」。改 seen-flags + assembled cardinality + 负向 fixture（须独立 harness，因 `sanitize.py` 原地重写 dump）属 driver 测试基建，与下述 Gate helper CI gate 同类 |
| crash→respawn 四态实现测试（round6 P0-1） | deferred | driver + go-bridge 代码：turn 中 crash / turn 后 idle crash / crash+abort 并发 / crash 后首次新 send。设计闭环见 §3.6.3（已对齐 `deleteSession`/`relayEvents` defer/`sess.Send` 源码） |
| `block-start/end → newPart` 扩展 core.Event | 一期不扩展 | core.Event 无 newPart；一期自然合并（同 turn 多 block text 拼接）。若要 block 级独立 part，扩展 core.Event+wire（协议改动） |
| Gate helper 完整 CI gate（malformed/join） | 部分采纳 | 已修：env 转发/O_TRUNC/sendAndWait 竞态/rpc-error/PARTIAL exit 1/peer 断言。完整 malformed/join 属 driver 测试基建 |
| fail-closed durable fixture / protocol pack 同步 | deferred | 测试基建/跨仓协议 |

**main.go v7 实际修复**：① `DSH_PERMISSION_MODE`/`DSH_SYSTEM_PROMPT` 转发；② dump `O_TRUNC`；③ `sendAndWait` 先注册后写 + rpc-error 返回 nil（fatal exit 1）；④ **PARTIAL→`os.Exit(1)`**；⑤ error response 不打印 OK；⑥ **gofmt 干净 + go vet 无诊断**。
**sanitize.py v7**：递归 scrub + **双向 peer 断言**（chunk→assembled 与 assembled→chunk 双向，**非空** peer 缺失即失败；负向样本 assembled-only/chunk-only 均 FAIL）+ 无路径断言。**已知边界（round6 P1-3）**：断言用内容 truthiness（`s['text']`/`s['atext']`）兼任「见过记录」与「拼接内容」，单侧空 text delta / 单侧空 assembled text 仍通过；当前真实 dump 无空块，既有结论不受影响。完整「缺任一 peer 即失败」需 seen-flags + assembled cardinality + 负向 fixture，见 §15 deferred。

**当前结论**：协议选型、事件映射（自包含+source.kind 分流）、identity（对齐 reducer + process nonce + source-proven turn/step 校验）、crash→respawn 闭环（go-bridge 对象身份淘汰）、双写去重（peer 断言）、usage 公式、权限 composition（环境切换）七块设计完整且有真实证据。剩余为 driver 实现代码（respawn 四态测试/sanitizer seen-flags fixture/protocol 同步）与特殊触发 dump。
