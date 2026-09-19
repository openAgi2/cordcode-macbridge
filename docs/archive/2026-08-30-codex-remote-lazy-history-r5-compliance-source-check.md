# codex-remote 懒加载历史 r5 合规与官方源码可行性核验

- 日期：2026-08-30
- 被核验方案：`2026-08-30-codex-remote-lazy-history-implementation-plan.md` r5，提交
  `45841908a075d25e16747fcda110e4801dec2963`
- 对照评审：`2026-08-30-codex-remote-lazy-history-plan-audit-r4.md`
- 官方行为基线：`/Users/jacklee/Projects/codex` tag `rust-v0.150.0-alpha.12.2`
- 核验性质：r4 闭环确认 + 官方源码可行性检查；**不是第五轮开放式评审**

## 1. 结论

1. **r5 对 r4 的 3 项 P0、4 项 P1 均有实质落点，7/7 合规。**没有发现漏采纳、把建议只写进
   修订记录却未进入任务/门/测试矩阵的情况。
2. 用户期望的产品目标——打开会话只加载最近回合摘要，默认隐藏思考/执行步骤，点击某回合后才取
   该回合 items——对 `historyMode=paginated` **可以实现，而且正是目标 tag 已提供的官方数据路径**；
   不需要自行设计上游分页协议。
3. `/Users/jacklee/Projects/codex` **不能证明 ChatGPT iOS App 的具体 UI 实现**。它能证明 app-server
   为“元数据/摘要首屏/按回合明细”提供了原生协议和服务端实现；“ChatGPT iOS 默认折叠、点击加载”
   仍是 owner 的黑盒观察。方案应把两种证据分开表述。
4. r5 仍有 **1 个 G2 前的实现级阻塞**：§3.2.0 冻结的三字段 sparse `upsertTurns` 与现有 Go/Swift
   wire 类型不兼容，不能按文档直接实现。该问题不推翻 r4 的总体设计，也不阻塞 Phase 0/G0，
   但必须在写 handler 前修正 canonical wire contract。
5. legacy 会话不能从源码推定拥有同等的按回合明细能力。r5 已用 T0.5/owner 裁决隔离该风险，方向
   正确；G0 必须把“`thread/items/list` 是否可用”按 `historyMode` 分组，而不能只测一个 paginated
   会话后把结论外推到 legacy。

因此门判定应保持：**Phase 0 = APPROVED；G1 仍由 live fixture 阻塞；G2 额外由本文 §4.1 的 wire
形状修正阻塞。**不需要再发起开放式文档评审。

## 2. r4 → r5 逐项合规核验

| r4 要求 | r5 落点 | 判定 |
| --- | --- | --- |
| P0-r4-1 按连接投递 historical hydration | §2.4 三类连接；§3.2.0 delivery mode；T2.0 A/B/C、no-op revision patch、后续 live baseRev | ✅ |
| P0-r4-2 G0 负结果必须使 gate 失败 | §3.0.7 六类 fail；control inventory 集合+顺序对照；backwards round-trip | ✅ |
| P0-r4-3 unknown item 整回合原子失败 | §2.2、T2.2、红线 12：保留 Summary，不 partial replace，不标 loaded | ✅ |
| P1-r4-1 detail RPC 状态机补齐 | §3.2.0 明确 success-shaped failed ack、terminal follower syncRev、orphan loading 恢复及 patch 顺序 | ✅，但所选 sparse shape 有实现断点，见 §4.1 |
| P1-r4-2 stale token 单值化 | 持久化 per-turn generation；目标 turn 变化与其他 turn 变化的正反测试 | ✅ |
| P1-r4-3 producer checkpoint 有界且可验证 | backend-private checkpoint、schema bump、target RPC 验证、bounded recovery + continuation | ✅ |
| P1-r4-4 恢复/多客户端测试 | T2.5 覆盖 A/B/C、live baseRev、crash orphan、leader/follower、unknown item retry | ✅ |

r5 的采纳不是表面文字：要求同时进入了目标行为、wire contract、实施任务、验收矩阵和红线，符合 r4
要求的可实施闭环。

## 3. 官方源码证明了什么

### 3.1 首屏不拉全量：官方原生支持

目标 tag 的 `thread_processor.rs:2981-3000` 显示：只有 `thread/read(includeTurns=true)` 才水合完整
turns；paginated thread 会调用 `paginated_thread_full_turns`，legacy 则加载并重放 rollout。也就是说，
冷打开改用元数据 + Summary 页确实绕开了旧的全量应答路径。

`thread_processor.rs:3006-3089` 与 `3151-3219` 进一步证明：

- `thread/turns/list` 默认 `sortDirection=desc`、`itemsView=summary`；
- paginated thread 的 Summary/NotLoaded 直接走 ThreadStore；
- `Full` 被官方注释为临时兼容路径；
- legacy 虽能分页传输，但服务端每次仍重放完整 rollout。

官方测试 `app-server/tests/suite/v2/thread_read.rs:330-477` 验证了正反 cursor，以及
Full/Summary/NotLoaded 三态：Summary 只保留首条 user 与末条 agent，NotLoaded items 为空且 turn
identity/status/timing 不变。

### 3.2 点击后按回合拉明细：官方原生支持

`thread_processor.rs:3365-3422` 的 `thread_items_list_response_inner` 原生接受：

- `thread_id`；
- 可选 `turn_id` 过滤；
- `cursor/limit/sort_direction`；
- 返回 `ThreadItemEntry { turn_id, item }` 及双向 cursor。

更关键的是，官方已经实现了本方案 Mac 侧需要的“单回合拉到 EOF”算法：
`thread_processor.rs:3222-3258` 的 `paginated_turn_full_items`。其行为是：

1. 固定 `turn_id`；
2. `sort=Asc`；
3. 使用 items 最大页尺寸请求；
4. 每页严格反序列化；
5. `nextCursor=nil` 才算 EOF；
6. cursor 原地重复立即报错，禁止死循环。

T1.1 不应只“参考概念”，而应把该函数当作行为模板，逐项镜像其排序、EOF 和 repeated-cursor guard，
再叠加本项目经 G0 裁决的 maxPages/maxBytes/timeout。这样复用的是官方经过测试的分页不变量，而不是
重新发明一套结束条件。

官方集成测试 `thread_read.rs:1781-1934` 已覆盖 turn filter、items cursor、Summary/NotLoaded/Full；
这些测试应作为本仓 fixture 断言与单测场景的直接来源。

### 3.3 `initialTurnsPage` 是官方的一次往返优化候选

目标 tag 的 app-server README 明确说明：希望在一次往返中同时获得 live resume subscription 与首个
turns 页的客户端，可给 `thread/resume` 传 `initialTurnsPage`；响应包含首屏 page 和后续 cursor。
`thread_processor.rs:3292-3332` 还处理了 live active turn 占位，避免首屏超过请求 limit；官方测试
`thread_read.rs:1048-1119` 验证 initial page 与同参数 `thread/turns/list` 一致。

r5 把它保持为可选项不违反 r4，也不应现在未经 live probe 强行改为主路径。但为最大化复用官方实现，
G0 应把它作为**有数据的候选路径**比较：

- 基线：`thread/read(includeTurns=false)` + `thread/turns/list(summary)`；
- 候选：`thread/resume(excludeTurns=true, initialTurnsPage=summary)`。

若目标 Desktop 实测支持、且不会重复现有 live subscription，候选路径可少一次 RPC，并复用官方的
active-turn overlay/cursor 处理；否则保留当前基线。这个裁决必须来自 G0 fixture 与耗时数据，不能只凭
源码存在就启用 experimental 参数。

### 3.4 不能承诺“完整思考原文”

官方协议有 Reasoning `summary[]` 与 `content[]`，但当前本仓 schema replay 的唯一 reasoning 样本是
`summary` 非空、`content` 为空。本文重新用严格 variant 提取和 key-presence/逐 variant 提取交叉验证：

```text
total items=10；十类各 1
reasoning=1；content_nonempty=0；summary_nonempty=1
userMessage content_len=1；reasoning content_len=0 summary_len=1
```

因此产品可承诺“按需加载服务端实际提供的 reasoning 摘要、工具调用和执行步骤”，不能在 G0 获得非空
Reasoning content 之前承诺“展示完整思维链”。r5 的 G0.5 四态统计和 owner 删除主张门是正确的。

## 4. 开工前必须收口的两处文档问题

### 4.1 P0（G2 前）：三字段 sparse `upsertTurns` 不能被现有 schema 解码

r5 §3.2.0 写明 sparse `upsertTurns` 只含：

```json
{"turnId":"…","detailLoadState":"loading|loaded|failed","generation":1}
```

但当前两端的 `upsertTurns` 元素都是**完整 turn 类型**：

- Go `go-bridge/projection_types.go:96-105`：`TurnProjection.Status` 是必填 `json:"status"`；
- Swift `Models/SessionProjection.swift:226-236`：`SessionTurnProjection.status` 是非 optional；
- Swift `merged(with:)` 也按完整 turn 合并；`ProjectionReplica` 还会把任意 `upsertTurns != nil`
  标为 `orderChanged=true`。

所以 r5 冻结的三字段 JSON 不能直接解码成现有 Swift `SessionTurnProjection`。这不是测试能自动补齐的
小细节，而是 wire shape 自相矛盾。

实施前必须在 canonical protocol 中二选一并写死：

- **最小改动方案**：仍用现有 `upsertTurns`，但发送包含当前 canonical `status` 的合法 turn upsert，
  明确 merge 不覆盖缺席的 message/timing 字段；同时接受/修正 `orderChanged` 的现有语义；或
- **更干净方案**：新增专用的 turn-state patch DTO/op（只含 turnId/state/reason/generation），
  Swift/Go 显式应用它，不把状态更新伪装成完整 turn upsert。

不能保留“三字段 sparse `upsertTurns` + 现有完整 Turn 类型”这一组合。由于 r4 只要求在允许的方案中
选定一个，并未要求必须选 sparse upsert，修正这一点仍属于 r4 P1-r4-1 的 gate 内收口，不是重开设计。

### 4.2 P1（措辞/证据边界）：不能把 ChatGPT iOS UI 写成“源码验证”

r5 §1.1 写“ChatGPT iOS App … 点击才加载（owner 观察 + 源码验证）”。`/Users/jacklee/Projects/codex`
不包含 ChatGPT iOS App 的 UI 源码，无法验证它是否点击时才发 RPC、是否缓存或采用其他内部接口。

建议改为：

> ChatGPT iOS 的默认折叠和快速打开是 owner 黑盒观察；codex-rs 源码独立证明目标 Desktop app-server
> 提供 Summary 首屏与按 turn items 分页能力，因此本项目可以实现同类交互，但不声称复刻 ChatGPT iOS
> 的私有客户端实现。

## 5. legacy 边界与目标覆盖范围

源码能确定：legacy 的 `thread/turns/list` 是网络分页，但服务端仍每次重放完整 rollout；源码不能保证
legacy 一定有可用的 ThreadStore items 索引。`thread/items/list` 遇到 ThreadStore Unsupported 会返回
method-not-found（`thread_processor.rs:3398-3402`）。

r5 T0.5 已要求 owner 在 legacy 样本存在时裁决，这符合 fail-closed 纪律。但 G0 记录应增加一条明确的
分层能力矩阵：

| historyMode | turns Summary | turn-filtered items | 首屏收益 | 点击明细策略 |
| --- | --- | --- | --- | --- |
| paginated | 必测 | 必测 | 传输/编码 + 服务端查询 | 官方 items/list 主路径 |
| legacy | 必测 | **独立必测，不继承 paginated 结论** | 至少传输/编码；服务端仍重放 | owner 明确裁决；禁止静默 full fallback |

如果 legacy 的 items/list 不可用，仍可做到“打开快”：首屏走 Summary；只是第一次展开明细需要 owner
明确选择保留旧 full 兼容路径，或明确不支持该类会话的明细展开。不能把其中任一种偷偷写成自动回退。

## 6. 推荐的官方复用清单

实施时应把下列 tag 函数/测试列为 T1/G1 的行为锚点：

| 本项目任务 | 官方锚点 |
| --- | --- |
| metadata-only read | `apply_thread_read_store_fields`（仅 includeTurns 才水合） |
| Summary/NotLoaded 首屏 | `thread_turns_list_response_inner` + `paginated_thread_turns_list_response` |
| 单回合 items 请求 | `thread_items_list_response_inner` |
| 单回合拉到 EOF | `paginated_turn_full_items`（Asc、EOF、repeated cursor guard） |
| 全量路径只作兼容基线 | `paginated_thread_full_turns` 的 “slow compatibility path” 注释 |
| resume 一次往返候选 | `paginated_resume_initial_turns_page(_with_active_slot)` |
| 行为测试基线 | `thread_read.rs` 的 pagination/items-view/items-list/initial-page tests |

这套复用边界也说明：上游“取什么、怎么分页、何时 EOF”应遵循 codex-rs；本项目只实现它不得不承担的
桥接职责——投影 SoT、revision、connection delivery、iOS 状态与交互。不要复制或重写上游已有分页
语义，也不要把 bridge 内部 cursor 暴露成上游 cursor。

## 7. 最终判定

- **r4 合规：通过（7/7）。**
- **目标可行性：通过，但 paginated 为源码证明的完整主路径；legacy 由 G0 分层裁决。**
- **Phase 0：可立即执行。**
- **Phase 1：等待 G0 live fixtures，且实现应直接镜像官方 `paginated_turn_full_items` 不变量。**
- **Phase 2：在 handler 开工前先修正 sparse `upsertTurns` wire shape。**
- **无需第六轮开放式评审**；上述问题都可在既有 G0/G2 gate 内闭合。

