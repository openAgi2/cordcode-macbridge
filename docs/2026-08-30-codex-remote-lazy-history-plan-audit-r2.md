# codex-remote 懒加载会话历史实施方案 r2 复评报告

- 复评日期：2026-08-30
- 目标文档：`docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md`（r2，298 行）
- 上轮报告：`docs/2026-08-30-codex-remote-lazy-history-plan-audit.md`
- 方法：`audit-plan` 样本纪律 + 目标 tag 源码复核 + 当前 Mac/iOS projection 实现核对
- 结论：**🟡 r2 已实质落实上轮六项 P0，可执行 Phase 0；Phase 1 仍受 G0 阻塞，且在进入
  Phase 2 前还需补三项实现级 P0 约束。**

## 1. 核心结论

r2 不是只改措辞：`historyMode` 分层、统一 detail 入口、Mac 拉到 EOF 后原子提交、专用
detail merge、desc 正序化、十类 item 回归面、并发 fence、CPU 现状和 tag 基线均已落入
任务与门禁。R1/R2/R3 的处置也合理，没有以 fallback、假数据或未证实 capability 掩盖问题。

上轮结论可更新为：

- **Phase 0：通过计划评审，可以执行。** 它仍必须产生真实 target fixtures；截至本次复评，
  新协议面仍没有 live fixture，所以不能越过 G0。
- **Phase 1：不得开始。** 除 G0 外，r2 还需明确 Summary item identity 怎样进入 projection，
  以及 Reasoning detail 的 content/summary 选择规则。
- **Phase 2：不得按当前 T2.2 原文直接施工。** “不改 live 流”与“修改共享 FlushPatch 的
  tool PartOp”互相冲突；历史 detail 应走显式 turnId 的专用 `replace_parts`/等价原子操作，
  不应改造 live tool 累加器来兼容历史回填。

## 2. r1 问题落实复核

| 上轮问题 | r2 落点 | 复评 |
| --- | --- | --- |
| P0-1 historyMode 分层 | §1.2、§2.1、T0.2、T0.5 | 🟢 已落实 |
| P0-2 hasDetail 不可推断 | §2.2 统一入口 + detailLoadState | 🟢 已落实；方案 A 合理 |
| P0-3 禁用 live reducer 合明细 | T2.2 专用 merge | 🟡 原则落实，但 FlushPatch 条款仍与 live 零改动冲突 |
| P0-4 wire contract 未冻结 | §3.2.0 协议先行 | 🟢 已落实；仍需补 backend key/result 单写者约束 |
| P0-5 desc/prepend/去重 | §2.1、T1.2、T2.1 | 🟢 已落实 |
| P0-6 十类 item 回归 | T0.4、T1.4、T2.2 | 🟢 已落实，proven/非 proven 边界写清 |
| P1-1 并发 fence | T2.5 九场景 | 🟢 已落实 |
| P1-2 CPU 状态陈旧 | §6 | 🟢 已落实并独立复核 |
| P1-3 可空字段 | §1.1、T0.3 | 🟢 已落实 |
| P1-4 race 范围 | G2 定向 race | 🟢 已落实 |
| P2 编辑项 | 方法名、阶段数、常量、commit、tag | 🟡 主体落实，tag 行号仍有漂移 |

## 3. 事实复核

### 3.1 已修复 CPU 链与安装版本

本次重新读取仓库与安装包：

```text
71ef328 fix(codex-remote): back off failed catalog probes
9c30ca2 perf(codex-remote): drive catalog refreshes from events
c0e26fe perf(codex-remote): avoid idle projection patch churn
7171083 perf(codex-remote): reduce remaining runtime hot paths
b45463c perf(projection): gate response diagnostics behind debug logging

/Applications/CordCodeLink.app/.../cordcode-bridge-runtime --version
= cordcode-bridge-runtime 0.1.0
  (commit: b45463c2ded8, built: 2026-08-29T17:37:20Z)
```

r2 §6 的事实主张成立。Codex 上游当前 HEAD 也确为 `63d213884d...`，行为基线改锚
`rust-v0.150.0-alpha.12.2` 正确。

### 3.2 target tag 行号

行为本身成立，但 r2 §1.2 仍混用了 main/旧稿行号。目标 tag 的实际位置是：

| 事实 | r2 引用 | target tag 实际位置 |
| --- | --- | --- |
| `TurnItemsView` | `thread_data.rs:380-388` | `377-385` |
| Reasoning | `item.rs:277-283` | `268-275` |
| turn started NotLoaded | `bespoke_event_handling.rs:172` | `170` |
| turn completed last-agent Summary | `1352-1353` | `1303-1305` |
| Full temporary compatibility | `thread_processor.rs:3222-3223` | `3165-3166` |
| slow compatibility path | `3261-3262` | `3261-3262` |

既然文档声明 tag 是可复跑证据基线，行号也应全部从 tag 生成，不能把移动 main 的行号留在表中。

## 4. 内容形状复核表

本次没有发现新的 target live fixture。以下评级评的是“当前证据”，不是 r2 的任务设计质量。

| 内容面 | r2 主张/门禁 | 本次 dump 结果 | 评级 |
| --- | --- | --- | --- |
| `thread/read.historyMode` | G0 必采，按模式分层 | target Remote 无样本；旧 codex-web 真实 fixture 有 legacy/paginated 两态 | 🟡 待 G0 |
| turns Summary | 0/1/2 optional slots、desc/cursor | tag 源码支持；target 无响应 dump | 🟡 待 G0 |
| turns NotLoaded | 同 turn identity、空 items | tag 源码支持；target 无响应 dump | 🟡 待 G0 |
| items/list entry | `turnId + item`、asc 两页 | tag 源码支持；target 无响应 dump | 🔴 G0 阻塞 |
| cursor 关系 | next/backwards、inclusive anchor | tag 文档支持；无真实往返 | 🔴 G0 阻塞 |
| Summary↔items item-id 等价 | 用于正文去重 | 没有 side-by-side target dump | 🔴 G0 阻塞 |
| Reasoning summary/content | content 非空才宣称完整思考 | schema replay：summary 长度 1、reasoning content 长度 0 | 🔴 G0 阻塞 |
| Command output | null/非空/跨页 | schema replay 仅非空 1 条；无 null/跨页 target 样本 | 🔴 G0 阻塞 |
| 其他八类 item | 沿用现有 decoder，缺样本不宣称 proven | schema replay 有十类各 1；不是 live | 🟡 边界表述合格 |
| initialTurnsPage | 可选观察，不进主路径 | tag/ancestor 成立；无 live response | 🟡 不阻塞 |

此处没有任何 target 新读取面可标为 🟢。这与 r2 的“G0 任一缺失不得进入下一阶段”一致，
所以不否定 Phase 0 可执行，但明确否定现在进入 G1。

## 5. 脚本交叉验证与归因

对现有 schema replay 再跑两种提取策略：

```text
strict type-match:
  total items=10
  reasoning with non-empty content=0

key-presence:
  ids present=10
  objects with content key=2
  non-empty content arrays=1
```

两个策略表面出现“Reasoning content 0 vs content 非空 1”的分歧。进一步输出
`type → contentLength` 后确认：

```text
userMessage item_user      contentLength=1
reasoning   item_reasoning contentLength=0, summaryLength=1
```

差异不是 Reasoning 边缘形状，而是 `content` 字段在不同 tagged-union variant 上同名异义。
因此 G0.5 必须使用 `type==reasoning` 的类型限定提取，不能用全局 `has("content")` 计数。
这项归因已经用 per-item 映射复核，不是对计数差异的直觉解释。

## 6. r2 新发现的 P0

### P0-r2-1：Summary final-agent item id 目前进入 reducer 后会丢失

r2 第 118～120、200～204 行要求按官方 item id 对齐 Summary 与 full items。agent 层现在
确实把 agent message id 放进 `text_delta.itemId`，但 projection reducer 的 text 路径只用
itemId 作为缺省 turn attribution，创建的 text `ProjectionPart` 不保存 item id：

- `handlers_projection.go:1362-1376`：事件带 `itemId`；
- `projection_reducer.go:673-717`：text part 只保存 type/text/presentation；
- `projection_types.go:19-20`：`ItemID` 注释仍限定为 tool；
- iOS `SessionProjectionPart.itemId` 也存在，但 `makeText/makeReasoning` 默认写 nil。

所以即使 G0.7 证明两个接口的 item id 完全一致，Mac projection 中也没有 Summary
final-agent id 可供 detail merge 比较。仅写“标注 summary 槽位来源”不够，必须冻结存储位置。

修订要求：T1.2/T2.1 明确把官方 item id 持久化到 canonical projection part（建议把
`ProjectionPart.ItemID` 从 tool-only 扩展为所有官方 item variant，并同步 Go schema/Swift
注释与构造器），或定义等价的 Mac-side canonical summary identity 字段。snapshot、patch、
restore 后必须仍保留，不能只放在本次 handler 的临时 map。新增测试：Summary snapshot 中
final-agent part itemId 非空；重连 restore 后 detail merge 仍按同 id 去重。

### P0-r2-2：现有 Reasoning decoder 是 summary-first，不满足“加载完整思考”

`agent/codex-remote/history.go:345-351` 当前先拼 `summary[]`，只有 summary 为空才读取
`content[]`。r2 T2.2 写“可复用 item decoder”，但验收矩阵要求有非空 content 时展示完整
思考。如果真实响应同时包含非空 summary 和非空 content，原 decoder 会永远隐藏 content。

修订要求：G0.5 除“content 非空”外，必须 dump 同一 Reasoning 的 summary/content 组合并统计
四态（均空 / 仅 summary / 仅 content / 两者均非空）。T1/T2 冻结 detail 语义：明细视图优先
完整 `content[]`，summary 是 content 缺席时的明确替代，还是独立 part，必须由真实样本和产品
口径裁决。不能原样复用 summary-first mapper 后宣称已展示 full reasoning。

### P0-r2-3：专用历史 merge 不应修改共享 live `FlushPatch`

r2 §1.3/T1.3 声明 live 路径零改动，但 T2.2 又要求把 `FlushPatch` 的 tool PartOp 改成显式
turn 归属。当前 `ps.tools` 只存 `callID → part`，FlushPatch 从全局
`Execution.ActiveTurnID` 取 owner；为历史 detail 改它会改变所有 backend 的 live patch 热路径，
扩大回归面并违反本方案边界。

现有 wire 已有显式 `(turnId,messageId)` 的 `replace_parts`，iOS 也已实现它。修订要求：历史
detail merge 使用专用原子 operation（优先 `replace_parts` + 同 patch 的 detailLoadState upsert，
或语义等价的新 op），直接针对请求的 turn；不要把历史工具塞进 `ps.tools`，也不要为本功能
修改 live tool FlushPatch。若坚持重构共享 FlushPatch，就必须删除“live 路径零改动”边界，扩充
所有 backend 的 live tool 回归矩阵，不能两种说法同时保留。

## 7. P1/P2 修订

### P1

1. **singleflight key 补 backend 维度。** 标准 request envelope 有 `backendId`，projection
   SoT key 也是 `(backendId,sessionId)`；幂等键应是 `(backendId,sessionId,turnId)`，或协议明确
   此 RPC 只接受 `backendId=codex-remote` 并在 handler fail-closed 拒绝其他 backend。
2. **result 与 projection 只能有一个内容写者。** §3.2.0 的 `{items?}` 会让同一整回合既出现在
   RPC result 又出现在 projection patch，造成双倍大载荷和竞态。建议 result 只返回
   `{detailLoadState,syncRev}`/错误 ack，canonical items 只随 projection snapshot/patch 下发。
3. **G0.5 通过语义消除矛盾。** 文档一处说八项任一缺失 G0 不得通过，另一处说无法产生非空
   Reasoning content 时把验收改成 pending evidence。应二选一：严格阻塞；或记录 owner 裁决并
   从本版验收/实现主张中删除“完整思考”。不能一边通过 G0，一边保留 pending 形状声明。
4. **整回合资源门。** T0.2/G0.6 应记录单 turn 总 items 字节、页数和最大单 item；G0 在进入
   “Mac 拉到 EOF、内存原子提交”前裁决 max pages/max bytes/timeout 的 fail-closed 行为。不得
   到 Phase 2 才凭常量截断合法历史，也不得以 placeholder 代替超大 tool output。
5. **明确 detail 定义。** items/list 会包含 user/final-agent 本身。“空明细”应定义为去除
   Summary 重复槽位后没有 reasoning/tool/file/etc.，而不是上游 items 数组为空；对应空纯对话
   fixture 和 UI 测试需使用同一口径。
6. **兼容路径措辞按 historyMode 限定。** `includeTurns=true` 的 deprecation/slow path 结论
   是针对 paginated thread；top-level 不应把所有 legacy full read 都称为“官方已废弃”。

### P2

1. 把 §1.2 的源码行号统一重算为 target tag 行号（见 §3.2 表）。
2. `remoteTurn.ItemsView` 当前已经存在（`history.go:81`）；T1.2 应写“保留/传播 itemsView 与
   summary source”，不是“增加”。
3. `ReadTurnItems` 的页大小不要写“用服务端上限”：客户端没有协商出的上限值。应定义请求
   limit 常量、接受服务端 clamp，并按实际 `nextCursor` 拉到 EOF。
4. §9-R3 不是对上轮建议的“不采纳”：上轮报告本就明确 initialTurnsPage 可选且不阻塞。
   改成“维持评审结论”更准确。

## 8. 未验证内容类型与 G0 判定

G0 的八项清单总体合格，但执行脚本还需补两条防误判约束：

- 所有 `content`/`summary` 统计必须按 ThreadItem `type` 分组；同名字段不能跨 variant 汇总。
- Summary↔items 去重必须输出 `turnId → summary item ids → full item ids` 映射，而不是只比较
  两边总数或文本。要分别验证 first-user 与 final-agent，不得把两个 slot 合成集合后只看交集。

没有真实样本的类型继续保持“unverified”是正确做法。schema replay 可驱动 decoder contract
测试，但不能升级为 live proven；r2 已遵守这条边界。

## 9. 修订优先级与复评门

### P0（进入 G1 前）

1. 冻结 Summary final-agent item id 在 canonical projection 中的持久化位置。
2. 用 G0 四态样本裁决 Reasoning detail 的 summary/content 映射，禁止原样沿用 summary-first。
3. 把历史 detail merge 与 live FlushPatch 完全隔离，或显式扩大 live 改动范围与回归门。

### P1（wire contract 冻结时）

1. singleflight/幂等键加入 backendId 或限制 backend。
2. RPC result 只做 ack，projection 是 items 唯一内容 SoT。
3. 统一 G0.5 阻塞/pending 语义，冻结整回合资源失败规则和“空明细”定义。

### P2（编辑）

更新 target tag 行号、ItemsView 现状、limit 表述和 §9-R3 分类。

**r2 当前判定：Phase 0 = APPROVED；G1/G2 = BLOCKED。** 完成上述三项 P0 文本修订，且
G0 八项真实 fixture 全部满足或经 owner 明确缩减产品主张后，才可重新判定进入 Phase 1。
