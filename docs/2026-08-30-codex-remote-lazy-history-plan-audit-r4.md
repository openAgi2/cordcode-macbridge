# codex-remote 懒加载会话历史实施方案 r4 终审报告

- 评审日期：2026-08-30
- 目标文档：`docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md`（r4，511 行）
- 目标提交：`113c18d`
- 上轮报告：`docs/2026-08-30-codex-remote-lazy-history-plan-audit-r3.md`
- 方法：`audit-plan` 样本纪律 + r3→r4 diff + Mac/iOS 现有 projection/window/checkpoint 实现核对
- 结论：**🟡 r4 已关闭 r3 的全部问题，Phase 0 可以立即执行。本轮收敛出 3 项必须冻结的 P0 和 4 项实现级 P1；其中最重要的是 older 补水合 commit 的按连接投递语义。修完这些有限条目后不再做新一轮宽泛评审，直接以 G0/G1/G2 的真实证据门推进。**

## 1. 核心结论

r4 对上一轮的 1 项 P0、3 项 P1、2 项 P2 均已实质落实，R4 不采纳理由成立：只保留首 30
回合确实是历史截断，不应作为默认范围缩减。

本轮没有再发现新的 target API 内容类型；剩余问题集中在把已确定的数据模型接入现有生产
projection 机制时的精确所有权。首要缺口是：r4 要把 older 页 prepend 进 Kernel，但当前任何普通
Kernel patch 都会广播给该 session 的全部 SSV2 连接。iOS 对普通 `upsertTurns` 中的未知 turn 是
append，且只有 `applyWindowPage` 才更新 coverage ledger。于是 iPhone A 请求 older 时，iPhone B 会
被动收到旧回合、追加到尾部，既顺序错误又没有对应的窗口 coverage。该问题必须在 T2.0 中先冻结，
不能留给实现者临场选择。

其余问题都有明确、有限的修订动作：G0 的负结果必须真正阻塞、未知 item 必须整回合失败而非部分
loaded、detail state 的 patch/ack/singleflight/restart 语义需要落到现有 wire 形状、upstream cursor
持久化与恢复必须有独立且有界的 checkpoint 规则。

## 2. r3 问题落实复核

| r3 条目 | r4 落点 | 复评 |
| --- | --- | --- |
| P0-r3-1 upstream turns ↔ projection window | §2.4、T2.0、G0-9、T3.1、真机 #8 | 🟢 目标链已建立；仍需补按连接投递语义 |
| P1-r3-1 ack/patch revision 完成条件 | §2.2、§3.2.0 五步状态机、T3.2 | 🟢 原则关闭；wire 细节见 P1-r4-1 |
| P1-r3-2 stale-write fence | §2.2、§3.2.0、T2.2/T2.5 | 🟢 原则关闭；token 需单值化 |
| P1-r3-3 禁止自动 full fallback | WARNING、§2.3、T1.1、约束 11 | 🟢 已关闭 |
| P2-r3-1 mapped variants 口径 | T2.1 | 🟢 已关闭；未知 variant 的终态仍需收口 |
| P2-r3-2 detailLoadState 层级 | turn-level、缺席默认、restore 兼容 | 🟢 层级已关闭；patch op 需精确化 |
| R4 首 30 回合替代范围 | §9 明确不采纳 | 🟢 理由充分 |

## 3. 实现交叉核对

### 3.1 older 补水合不能按普通 projection patch 广播

当前 Mac `deliverProjectionPatchLocked` 向 session 的所有 SSV2 target 广播同一 patch，没有按
window coverage 或请求连接过滤（`go-bridge/event_publisher.go:658-765`）。

当前 iOS 普通 patch 路径：

- `SessionProjection.upsertingTurns` 对未知 turn 直接 append
  （iOS `Models/SessionProjection.swift:255-265`）；
- `ProjectionReplica.applyPatch` 应用该 patch 并把 `upsertTurns != nil` 视为顺序变化
  （iOS `Models/ProjectionReplica.swift:115-144`）；
- coverage ledger 只在 RPC window page 经 `applyWindowPage` 后更新
  （iOS `Models/ProjectionStore.swift:477-507`），普通 push patch 不更新它。

因此，producer 将旧页 prepend 进 Kernel 后若走普通 `upsertTurns`：

1. 请求者可能同时收到 push patch 和 window result，违反同一页唯一归属；
2. 其他窗口客户端收到未请求的旧页，且把它 append 到尾部；
3. 其他客户端的 `hasOlder/nextOlderCursor` 仍是旧值；
4. 若简单抑制 patch，其他连接的 `appliedRev` 又会落后，下一条 live patch 将 base mismatch；
5. 非窗口/full-projection 客户端仍需要完整 truth，不能一律只发空 revision。

这证明“同一 snapshot fence / syncRev 链”还不是完整投递设计。必须按连接的 session delivery mode
区分 requester window、other window clients 和 full clients，同时保持一条 revision chain。

### 3.2 producer 状态还没有可直接复用的持久化槽位

当前 `SessionProjection` 只有 session/revision/execution/turns；`ProjectionCheckpoint` 除 Projection
外只有 source checkpoints 和 `ClaudeSourceState`（`go-bridge/projection_types.go:113-123`，
`go-bridge/projection_kernel.go:86-118`）。checkpoint schema 当前为 v10，source validation 又主要基于
本地 path/cursor prefix；codex-remote 属 pathless hydrate 路径。

所以“`hasOlderUpstream + cursor` 随 Kernel snapshot 持久化”需要新增明确的 backend-private
checkpoint state、schema bump、clone/save/restore/validation 规则，不能把 opaque string cursor 塞进
现有 `ProjectionSourceDescriptor.Cursor int64`。这属于实现范围内的必要 schema 工作。

### 3.3 detailLoadState 仍缺具体 patch operation

当前 wire `ProjectionPatch` 只有 `UpsertTurns` 与 `PartOps`；`replace_parts` 的 `PartOp` 也只有
`turnId/messageId/parts`，没有 turn-level state（`go-bridge/projection_types.go:125-143`）。iOS patch
按 `execution → upsertTurns → partOps` 顺序应用
（iOS `Models/ProjectionStore.swift:1102-1110`）。

r4 已确定 `detailLoadState` 属于 turn，但“同 patch upsert”仍需指定使用 sparse `upsertTurns`、新增
turn op，还是扩展现有 part op；并冻结应用顺序和 backward decoding。否则 Mac 与 iOS 都可能按文档
写出合理但互不兼容的实现。

## 4. 内容形状复核

本轮仍没有 target `thread/turns/list` / `thread/items/list` live fixture。当前 live 目录只有既有
controller/stream 探针记录，因此以下评级不变；这正是 Phase 0 的工作，而不是继续文档推理的理由。

| 内容面 | r4 主张/门 | 本轮样本结果 | 评级 |
| --- | --- | --- | --- |
| `thread/read.historyMode` | 按 paginated/legacy 分层 | target 无样本 | 🟡 G0 |
| turns Summary | 0/1/2 slots、desc/cursor | target 无 response dump | 🔴 G0 |
| turns NotLoaded | 空 items、同 turn identity | target 无 response dump | 🔴 G0 |
| items/list | turnId filter、asc pages、EOF | target 无 response dump | 🔴 G0 |
| Summary↔items identity | user/final-agent item id 完全一致才可去重 | target 无 side-by-side dump | 🔴 G0 |
| Reasoning 四态 | 按 variant 分组并裁决语义 | schema replay 仅“summary 非空/content 空” | 🔴 G0 |
| Command output | null/非空/跨页 | schema replay 不覆盖完整矩阵 | 🔴 G0 |
| 十类 mapped item | decoder 回归面 | schema replay 十类各 1，均有 id；非 live | 🟡 仅 schema replay |
| >30 turns chain | page→EOF、无重复/无缺口 | target 无长会话 dump | 🔴 G0 |
| initialTurnsPage | 可选、不宣称 proven | tag 源码支持；无 live response | 🟡 不阻塞 |

## 5. 脚本交叉验证

对 `agent/codex-remote/testdata/phase2/thread-read-app-server.json` 本轮重新使用两种提取策略：

```text
严格按 type：
  total=10；十类各 1
  reasoning records=1
  reasoning non-empty content=0
  reasoning non-empty summary=1

按 key-presence：
  non-empty id=10
  objects with content key=2
  non-empty content arrays=1

逐 variant 归因：
  userMessage item_user      contentLength=1
  reasoning   item_reasoning contentLength=0 summaryLength=1
```

两个计数策略的表面差异仍由 tagged-union 中 `content` 同名异义解释，逐 item 映射已复核。没有发现
新的 schema replay 类型；也没有证据可把任何 target API shape 升级为 live proven。

## 6. 必须修订的问题

### P0-r4-1：冻结 historical-hydration commit 的按连接投递与 revision 语义

T2.0 在实现前必须明确一个符合现有 single-writer 的方案，至少覆盖：

1. 请求 older 的 window connection 如何只通过该 window result 获得这一页，并在 `syncRev=N` 接续；
2. 其他 window connections 如何不收到未请求的 historical turns，同时把 applied revision 从
   `N-1` 推进到 `N`（例如使用现有 shape 的 connection-specific no-op patch，具体方案由协议冻结）；
3. full-projection connections 如何收到完整历史 mutation；
4. 每个 `(conn, backend, session)` 必须记录 window/full delivery mode，不能只看连接是否声明 capability；
5. requester 断线、sink overflow、两设备并发 older、随后 live patch 的 baseRev 链；
6. R3“一个 turn 在同一 page chain 只归属一个 window response”不得被 push patch 绕过。

若这需要 connection-specific patch selection，它仍可复用现有 `projection_patch` shape 和 ordered sink，
不等于新增第二条 pipe；但必须先修订 `bridge-v1.md` R3/R4/R10 及对应 Mac/iOS 测试。

### P0-r4-2：G0 必须规定负结果，而不只是“九项有记录”

当前 G0-7 写“item id 是否完全一致”。只记录“不一致”也形式上满足“九项齐全”，但 T2.2 的唯一去重
依据随即失效。应冻结：

- first-user 与 final-agent 的 Summary id 必须分别在 items/list 找到同 id；任何一侧不成立，G0
  直接失败并回到 owner 重新裁决 identity/merge 方案；禁止退回文本相似度；
- Summary 出现预期两类以外的 item、turn filter 返回其他 turn、分页游标产生重复/缺页、非法 turnId
  返回 success-shaped 外来内容，均是 G0 fail，不是“样本已采所以通过”；
- “无缺口”不能从 opaque turn id 自身判断。G0-9 应将分页拼接后的 turn-id 序列与同会话仅用于探针的
  `includeTurns=true` control inventory 做集合和顺序对照，并做 backwards round-trip；否则只能证明
  “无重复”，不能证明“无缺口”。

### P0-r4-3：未知/不支持 item 必须整回合原子失败，不能 `loaded` 后少内容

r4 同时写“unknown variant fail-closed”和“SkippedTypes 可观测”，但没冻结二者如何共同决定终态。
应明确：

- `SkippedTypes` 是诊断字段，不是允许丢弃后继续成功的开关；
- items/list 出现无法无损映射的 type/shape 时，中止本回合 commit，保留原 Summary parts，提交
  `detailLoadState=failed + reasonCode=unsupported_item_type`；
- 不得执行部分 `replace_parts`，不得标 `loaded`，修复/升级后允许重试；
- 已映射但只有 schema replay、没有 live 样本的类型可以实现并测试，但交付报告不得标 live proven。

这使“fail-closed”具有可测试含义，也避免“可观测的数据丢失”。

## 7. 实现级 P1

### P1-r4-1：把 detail RPC 的 wire 状态机补到可独立实现

在 `unified-bridge-protocol.md` 一次冻结以下四点：

1. `detailLoadState` 在 patch 中的确切 operation 与同 patch 应用顺序；
2. 失败到底返回 WireError，还是 success-shaped
   `{detailLoadState:"failed", syncRev, reasonCode}`。若返回 WireError，必须定义 iOS 如何获得 failed
   commit 的 `syncRev`；不能同时写“typed error/failed ack”而不选；
3. singleflight follower 在 leader loading 时必须等待同一 terminal commit，再全部返回相同 terminal
   `syncRev`；若立即返回 loading ack，则不能把 `appliedRev >= loadingRev` 当 detail 完成；
4. bridge 进程在 loading commit 后崩溃/重启时，restore 必须把没有 active leader 的 orphan loading
   原子恢复为 `failed(reasonCode=interrupted)`（或明确的可重试终态），不能永久停在 loading。

### P1-r4-2：把 stale token 从“turnGeneration/baseRev”改成唯一算法

斜杠不是协议。全局 baseRev 会因其他 turn 的 live append 改变而误杀本回合 detail；当前 schema 又没有
turnGeneration。协议必须二选一并定义生成/比较规则，例如新增持久化的 per-turn generation，或使用
完成回合 canonical identity/fingerprint。测试应同时证明：目标 turn 改变会 stale，其他 turn 更新不会。

### P1-r4-3：producer checkpoint 与恢复必须有界且可验证

T2.0 增加：

- backend-private producer page state 的具体 checkpoint 字段、schema version bump、clone/save/restore；
- pathless codex-remote 的有效性检查，不能复用本地文件 prefix digest 假装已验证远端 cursor；
- restart 后先以 target RPC 验证/续接 cursor，错误则进入 typed recovery；
- “从 latest 重翻到 overlap”每次请求/任务的 maxPages/maxBytes/timeout 与持久化 continuation；达到上限
  返回显式 retryable failure，不能一次循环扫完全历史、重新制造 CPU/传输尖峰。

### P1-r4-4：补齐 historical delivery 与 detail state 的恢复测试

在现有 T2.5 矩阵之外增加：

- A 请求 older、B 停在首屏、C 使用 full projection：三者收到的内容与 revision 都正确；
- B 随后收到 live turn，无 base mismatch/全量 recovery；
- loading 后 bridge crash，重启不保留永久 loading；
- leader/follower 断线与取消不互相取消 authoritative fetch；
- unknown item 失败后 Summary 不变、修复后重试只提交一次完整 parts。

## 8. 收敛后的门判定

- **Phase 0：APPROVED，可立即开工。** 采样脚本先补 P0-r4-2 的明确 pass/fail 断言和 control
  inventory 对照；不需要等待 Phase 2 架构实现。
- **G1：BLOCKED。** 等 G0 的 target fixtures 与 identity/shape 断言通过，并落实 P0-r4-3 的原子失败规则。
- **G2：BLOCKED。** 先冻结 P0-r4-1 的 per-connection delivery/revision 契约，以及 P1-r4-1～4。

本报告是设计阶段的收敛终点：上述条目修订后，**不建议再发起第五轮开放式文档评审**。后续新问题
只允许由 G0 fixture、定向测试或真机证据触发，并在对应 gate 内修复；不能继续凭假设扩展评审范围。
