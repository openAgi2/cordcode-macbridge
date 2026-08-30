# codex-remote 懒加载历史实施方案 r6 最终评审

- 日期：2026-08-30
- 被评审文档：`2026-08-30-codex-remote-lazy-history-implementation-plan.md` r6
- 被评审提交：`6ff83c8af0fe2aa922a46decbc88fdb8ebf310c9`
- 官方基线：`/Users/jacklee/Projects/codex` tag `rust-v0.150.0-alpha.12.2`
- 性质：最后一次收口评审；不是新一轮开放式设计

## 1. 核心结论

r6 已完整兑现 r5 合规核验的主要要求：`turnStateOps` 定案合理，官方复用锚点已进入原则、任务、测试与
红线，legacy 能力分层和证据边界也已闭合。**总体设计批准，不再需要新一轮评审。**

但在执行 G0 前必须改正 1 项 P0：T0.6 的 `initialTurnsPage` 候选调用漏写 `excludeTurns:true`。目标 tag
明确规定 `thread/resume` 默认会把完整历史放进 `thread.turns`；按 r6 当前写法采样，会同时取得全量
历史和 initial page，性能对比失真。另有 2 项 `turnStateOps` 契约细节应在 G2 wire 冻结时补齐，以及
1 项 legacy 样本缺席时的 N/A 规则。

最终门判定：

- **设计：APPROVED。**
- **Phase 0：修正 T0.6 请求形状后可立即执行。**
- **G0：仍必须由目标 Desktop live fixtures 通过，当前没有被文档评审替代。**
- **G1/G2：维持 BLOCKED；G2 在写 handler 前补齐 §4.2/§4.3。**
- **本报告之后停止文档评审，余下问题只由 G0 fixture、定向测试或真机证据触发。**

## 2. r5 核验条目落实情况

| 条目 | r6 实质落点 | 判定 |
| --- | --- | --- |
| sparse upsert 不可解码 | 改为独立 `turnStateOps`；Go/Swift 显式应用；旧 patch 忽略 | 🟢 |
| ChatGPT iOS 证据边界 | owner 黑盒观察与 codex-rs app-server 能力分开表述 | 🟢 |
| 官方实现复用 | §1.5 锚点、T1.1 六项不变量、T1.4 官方测试、红线 13 | 🟢 |
| `initialTurnsPage` 数据化候选 | T0.6 加入三条件裁决 | 🟡 请求缺 `excludeTurns:true`，见 P0 |
| legacy 能力矩阵 | §2.5、T0.1/T0.5、G0 分组、真机 #10 | 🟢；补 N/A 规则更完整 |
| 不承诺完整思维链 | 产品承诺限定为服务端实际提供的 reasoning/工具/执行步骤 | 🟢 |

## 3. 外部内容形状与证据状态

### 3.1 官方源码已证明的能力

| 内容/行为 | tag 源码或官方测试 | 评级 |
| --- | --- | --- |
| metadata-only read | `thread_processor.rs:2981-3000` | 🟢 源码实现 |
| Summary/NotLoaded/Full 与 turn cursor | `thread_processor.rs:3006-3089,3151-3219`；`thread_read.rs:330-477` | 🟢 源码+官方测试 |
| turn-filtered items/list | `thread_processor.rs:3365-3422`；`thread_read.rs:1781-1934` | 🟢 源码+官方测试 |
| 单回合 items 拉到 EOF | `paginated_turn_full_items`，`thread_processor.rs:3222-3258` | 🟢 源码实现 |
| default resume 包含完整 turns | app-server README `376-378`；`ThreadResumeParams.exclude_turns` serde default false | 🟢 源码+官方文档 |
| initial page 正确调用形状 | README 示例 `excludeTurns:true + initialTurnsPage`；`thread_read.rs:1089-1111` | 🟢 源码+官方测试 |
| legacy items/list Unsupported 映射 | `thread_processor.rs:3398-3402` | 🟢 错误映射已证；目标会话是否触发仍需 live 样本 |

### 3.2 仍必须由 G0 提供的 target live 样本

以下内容仍无目标 Desktop live response dump，r6 正确地把它们留在 G0，没有虚报 proven：

| 内容类型/关系 | 当前证据 | 评级 |
| --- | --- | --- |
| `thread.historyMode` 的目标实际分布 | 无 target live dump | 🔴 G0 |
| Summary 0/1/2 槽位、真实 id | 仅源码/官方测试 | 🔴 G0 |
| NotLoaded 实际应答 | 仅源码/官方测试 | 🔴 G0 |
| items/list 的目标 turn filter/cursor/EOF | 仅源码/官方测试 | 🔴 G0 |
| Summary↔items first-user/final-agent id 一致性 | 无 side-by-side target dump | 🔴 G0 |
| Reasoning summary/content 四态 | schema replay 仅 summary 非空/content 空 | 🔴 G0 |
| CommandExecution null/非空/跨页 | live 矩阵缺失 | 🔴 G0 |
| >30 turns 到 EOF 且无缺口 | 无 target 长会话 dump | 🔴 G0 |
| legacy items/list 可用性 | 无 target legacy response | 🔴 G0（若 inventory 存在 legacy） |
| initialTurnsPage 与现有 subscription 是否重复 | 无 target runtime trace | 🔴 G0 候选裁决 |

这些红项不是继续评审的理由，而是 Phase 0 的明确工作队列。

## 4. 最终修订项

### P0-final-1：T0.6 必须使用 `excludeTurns:true`

r6 T0.6 当前候选写为：

```text
thread/resume(initialTurnsPage=summary)
```

目标 tag 的官方 README 明确写明：默认 resume 会重建完整 `thread.turns`；只有
`excludeTurns:true` 才保持 turns 为空。官方 initial page 测试同样显式设置
`exclude_turns: true`，并断言 `thread.turns.is_empty()`。

候选必须改成：

```json
{
  "method": "thread/resume",
  "params": {
    "threadId": "…",
    "excludeTurns": true,
    "initialTurnsPage": {
      "limit": 30,
      "sortDirection": "desc",
      "itemsView": "summary"
    }
  }
}
```

T0.6 三条件再增加一个响应断言：`thread.turns == []`。若非空，候选直接判失败，不能把全量历史字节
排除在性能统计之外。§0.1、§1.5 与交付清单中的候选简称也应至少写一次完整的
`excludeTurns=true` 约束，防止实施者照简写调用。

### P1-final-1：`turnStateOps` 必须进入 change-set 的 changedTurnIDs

现有 iOS `ProjectionReplica.changedTurnIDs(from:)` 只汇总 `upsertTurns` 与 `partOps`。如果 loading 或
failed patch 只携带 `turnStateOps`，即使 replica 内部状态已经更新，生成的 change set 仍可能没有目标
turn，展示层不会收到该回合需要刷新的事实。

G2 wire/Swift 实施任务应明确：

- `changedTurnIDs(from:)` 必须 union `turnStateOps.map(\.turnId)`；
- 单独的 loading/failed state patch 产生 `orderChanged=false`、`changedTurns` 含目标 turn；
- 同 patch 的 `turnStateOps + replace_parts` 只产生一个目标 turn change；
- T3.4 增加 state-only patch 能驱动 UI 状态刷新的测试，而不只是解码/最终模型值测试。

### P1-final-2：冻结 `reasonCode` 的赋值/清除不变量

`reasonCode?` 目前只有字段形状，没有状态约束。若一个 turn 从 failed 重试到 loading/loaded，而新 op
省略 reasonCode，合并实现可能保留旧错误，出现“已加载但仍显示 unsupported/interrupted”。协议应冻结：

- `failed`：`reasonCode` 必填且非空；
- `loading`、`loaded`：应用 op 时**显式清除**旧 reasonCode；
- `notRequested` 不通过普通网络 op 回写，只是缺席字段/旧 snapshot 的解码默认；
- 非法组合 fail-closed，并有 failed→loading→loaded 清除旧错误的单测。

### P2-final-1：legacy 样本不存在时允许有证据的 N/A

T0.1/G0.4 目前写 paginated 与 legacy“各测一组”，但 T0.5 又以“若 owner 现存长会话含 legacy”为前提。
应统一为：先做目标 thread inventory；若确有 legacy，必须完成 legacy items/list 探测与 T0.5；若 inventory
证明没有 legacy，则将该格记为 `N/A(no target legacy thread)`，附 inventory 证据，不因无法凭空制造 legacy
会话而阻塞 G0。以后出现 legacy 会话时能力仍保持 fail-closed，不能继承 paginated 结论。

## 5. 脚本交叉验证

本轮重新对 `agent/codex-remote/testdata/phase2/thread-read-app-server.json` 使用两种策略：

```text
严格按 item.type：
  total=10；十类各 1
  reasoning records=1
  reasoning content_nonempty=0
  reasoning summary_nonempty=1

按 key-presence + 逐 variant：
  content-key objects=3（包含嵌套 result content）
  non-empty content arrays=2
  userMessage item_user: content=1 summary=0
  reasoning item_reasoning: content=0 summary=1
```

全局 key-presence 数量大于顶层 ThreadItem 数量，是因为递归扫描包含嵌套 MCP result content；逐 variant
映射确认 Reasoning 结论不变。该 fixture 仍只是 schema replay，不升级为 target live proven。

## 6. 最终批准条件

本次不要求再生成 r7 或再次送审。对 r6 做以下定向修订即可结束设计阶段：

1. T0.6 候选改为 `excludeTurns:true + initialTurnsPage`，并断言返回 `thread.turns=[]`；
2. G2 明确 `turnStateOps` 进入 changedTurnIDs/change set；
3. G2 冻结 reasonCode 的 failed 必填与 loading/loaded 清除语义；
4. G0 增 legacy inventory-backed N/A 规则。

完成上述文字收口后，直接执行 G0 探针。此后只接受 fixture、定向测试或真机结果驱动的修正，不再做
开放式方案评审。

