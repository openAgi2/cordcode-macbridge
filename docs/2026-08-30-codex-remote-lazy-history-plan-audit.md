# codex-remote 懒加载会话历史实施方案评审报告

- 评审日期：2026-08-30
- 目标文档：`docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md`
- 评审方式：`audit-plan`（协议形状必须由真实样本证明）+ 当前实现静态审计 +
  `/Users/jacklee/Projects/codex` 的 tag/source 复核
- 结论：**🔴 暂不具备直接进入 Phase 1 的条件；Phase 0 的方向正确，但 G0 与
  Phase 1～3 之间仍缺少四项架构决策。**

## 1. 核心结论

官方源码与版本包含性主链成立：`rust-v0.150.0-alpha.12.2` 确实包含
`TurnItemsView`（`9e0c191c13`）和 `initialTurnsPage`（`2a1158b8e2f9`），
`thread/turns/list` 默认 Summary，`thread/items/list` 是官方明细分页通道，
`includeTurns=true` 对 paginated thread 是慢兼容路径。

但计划把以下四件尚未证明或与现有实现冲突的事写成了可直接实施的路径：

1. “Summary 一定是预计算轻查询”只对 `historyMode=paginated` 成立；legacy thread
   每次 `thread/turns/list` 仍会重放整份 rollout 后再分页。
2. Summary turn 没有 `hasDetail`、item count 或 item type 集合，无法知道该回合是否含
   Reasoning/CommandExecution。
3. 当前 projection reducer 不能把 detail 安全增量合入一个已完成回合：tool 依赖
   `Execution.ActiveTurnID`，reasoning/text 会走 live reducer 并改变 execution/重复正文。
4. `session_turn_items` 没有冻结请求/响应、cursor、幂等、并发和 `detailLoaded` 的
   Mac-side SoT 语义。

因此 G0 不能只验证 JSON 形状和体积；它还必须产出 `historyMode` 分层证据，并在进入
G1 前修订 detail merge 协议。否则即使所有新 RPC 都能调用，Phase 2/3 仍会出现“展开
入口无法判定、工具明细丢失、正文重复、会话被误标执行中”等确定性故障。

## 2. 样本与源码复核

### 2.1 本次实际可得样本

本仓搜索结果：`agent/codex-remote/testdata/` 中没有任何包含
`thread/turns/list`、`thread/items/list` 或 `initialTurnsPage` 响应的真实 fixture。

现有 `agent/codex-remote/testdata/phase2/thread-read-app-server.json` 是 schema replay，
其 provenance 明确声明“positive decoder tests intentionally use the schema fixture only”，
不能当 live Desktop 样本。它本次 dump 的结果是：

```text
turnCount=1
itemsView=[full]
itemCount=10
itemTypes=userMessage,reasoning,agentMessage,commandExecution,fileChange,
          mcpToolCall,dynamicToolCall,plan,webSearch,contextCompaction
reasoning.summary 非空=1
reasoning.content 非空=0
commandExecution.aggregatedOutput 非 null=1
```

`attempt-008-thread-resume-live-turn-stream.json` 是真实 Remote capture，但 payload 已
脱敏，只证明 `thread/resume` 和 live turn/item 通知到达；它不包含上述三个新读取面的
响应形状。

另有 `codex-web` 0.149.0-alpha.4 的真实 fixture 能证明 `Thread.historyMode` 在现实中
同时出现 `legacy` 与 `paginated`，但它不是本次目标 Desktop 0.150.0-alpha.12.2 的
Remote 样本，不能替代 G0。

### 2.2 两种提取策略交叉验证

对唯一可用的 full schema replay 分别执行了 strict type-match 与 key-presence 提取：

| 检查 | strict type-match | key-presence | 结果 |
| --- | ---: | ---: | --- |
| item 总数 | 10 | 10 个非空 `id` | 一致 |
| Reasoning 有内容记录 | 1（summary 或 content） | 1 个非空 summary array | 一致；仅 summary，未证明 content |
| Command output | 1 个非 null output | 1 个 `aggregatedOutput` key 且非 null | 一致 |
| item type 重复 | 0 | 10 个 type / 10 个 distinct type | 一致 |

没有出现脚本计数分歧。真正的证据缺口不是提取错误，而是样本本身不是 live、且没有
Summary/items-list/pagination 数据。

### 2.3 官方源码复核中的关键条件

tag `rust-v0.150.0-alpha.12.2` 中，`thread_turns_list_response_inner` 明确分支：

- `historyMode=paginated`：走 ThreadStore 的 `list_turns`，Summary 可读取预计算的
  first-user/final-agent 列；
- 其他模式：`load_thread_turns_list_history` 重放整份 rollout，源码注释明确说
  “it still replays the entire rollout on every request”。

所以计划 §1.2 的“Summary 是预计算的、读取不触碰 rollout 全量 items”需要加上
`historyMode=paginated` 前提。目标 Desktop 中 owner 现有长 session 属于哪一种模式，
目前没有 target fixture 证明。

## 3. 按内容类型验证表

| 内容面 | 计划断言 | 本次样本/源码结果 | 评级 |
| --- | --- | --- | --- |
| `thread/read` metadata | omit `includeTurns` 得到元数据 | 源码成立；旧真实 fixture 有 `historyMode`，目标 Remote fixture 未采 | 🟡 需补 target sample |
| `thread/turns/list` Summary | 默认 Summary；每 turn 为 first-user + final-agent | 源码证明默认值与两个 optional summary slot；无 target response；0/1 item、synthetic fork boundary 均可能，不保证“恰为两个” | 🔴 未验证且表述过强 |
| `thread/turns/list` NotLoaded | items 为空、保留 turn shell | 源码成立；无 target response | 🟡 待 G0 |
| `thread/turns/list` Full | 仅兼容/体积基线 | 源码成立；当前只有 `thread/read full` schema replay | 🟡 待 G0，不应进入产品路径 |
| turn cursor 正/反向 | desc 首页、next/backwards cursor 往返 | 源码定义 cursor 为 opaque，反向 anchor 为 inclusive；无真实往返样本 | 🔴 待 G0 |
| `thread/items/list` | `turnId` 过滤、asc、分页 | 源码成立；无 target response；legacy/paginated 两模式可用性未分层 | 🔴 待 G0 |
| `ThreadItemEntry` | 每项含 `turnId` + `item` | 源码成立；无 target response | 🔴 待 G0 |
| Reasoning | `summary[]` + `content[]`，展开显示完整思考 | schema replay 只看到 summary 非空、content 为空；没有真实非空 content 样本 | 🔴 验收口径无证据 |
| CommandExecution | command/cwd/status/output | schema replay 字段齐全但非 live；无 null/大输出/分页边界样本 | 🟡 需补边界样本 |
| FileChange/MCP/Dynamic/Plan/WebSearch | 现有 full hydrate 已映射，懒加载不得回归 | schema replay 各 1 条；计划的 G0/G1/G3 未覆盖这些类型 | 🔴 回归面遗漏 |
| `initialTurnsPage` | resume 可带首个 turns page | tag/ancestor 与源码成立；无 target response；计划标为可选、不作为主路径 | 🟡 可保持可选 |
| `hasDetail` | 可由 Summary metadata/capability 推断 | `Turn` 只有 id/items/itemsView/status/error/time；Summary 不含 detail count/type bitmap | 🔴 断言不成立 |
| `detailLoaded` | 重连不丢，Mac projection 为 SoT | 当前 `TurnProjection` 无该字段，计划未定义 cursor/load state | 🔴 协议未设计 |

## 4. 主要问题

### P0-1：性能收益必须按 `historyMode` 分层，当前 G0 会误判

计划 §1.2、§2.1 和 T0.2 把 Summary 响应体变小等同于服务端查询变轻。对 legacy thread，
官方实现仍全量 replay rollout，只是最后裁剪网络响应。它可以减少 WSS 字节和 bridge JSON
成本，但不保证降低 Desktop app-server CPU 或首包延迟。

修订要求：

1. T0 fixture 必须记录 `thread.historyMode`。
2. 至少选择一个 paginated 长 session；若 owner 现存长 session 有 legacy，再加一个 legacy
   对照，不得把两种结果混成一组平均数。
3. T0.2 除 response bytes 外记录 RPC wall time、bridge projection bytes 和冷打开 TTI；
   CPU 结论必须分别写成 server work 与 transport/encoding work。
4. G0 明确裁决 legacy thread 的产品策略。不能暗中切回 full，也不能声称 legacy 已获得
   预计算查询收益。

### P0-2：Summary 无法支持“仅有明细时展示入口”

计划 §2.2/T2.3 允许从 Summary metadata 推断 `hasDetail`，但官方 `Turn` 没有 item count、
item types 或 has-detail flag；Summary 的两个 item 本身也恰好排除了 Reasoning/Tool。
capability 只能说明方法可调用，不能说明某个 turn 有明细。

必须在计划中二选一并冻结：

- 对所有可分页的已完成 turn 显示统一“加载详细过程”入口；点击后空页即“无详细过程”；或
- 先执行一个真实、便宜且有样本证明的 per-turn detail probe。

不得从“Summary items 只有两条”反推“没有工具”，也不得新增本地猜测字段。若选择第一种，
字段应表达 `detailLoadState=notRequested/loading/loaded/failed`，而不是未经证明的 `hasDetail`。

### P0-3：不能复用 live event reducer 增量注入已完成回合

计划 T1.2/T2.1 要求保持现有 projection 入口，并在 T2.2 把 detail “增量合入”。当前代码
不具备这个语义：

1. `hydrateToolEventsFromStep` 只写 `itemId`，不写 `turnId`；tool reducer 只从
   `Execution.ActiveTurnID`/latest running turn 归属。已完成历史回合通常没有 active turn，
   tool 会丢失或串到错误回合。
2. reasoning/text detail 走 `reasoning_delta`/`text_delta`，会调用 `markRunning`。即使 turn
   status 已 completed，session execution 仍会被设成 running，直到额外伪造一次
   `turn_completed`；这会污染 live 状态和 checkpoint。
3. items/list 会再次返回 summary 中已经存在的 user/final-agent。现有 text projection 没有
   agent-message item-id 级去重，重复映射会再次 append 最终回复；user_message 则会覆盖。
4. `FlushPatch` 的 tool PartOp 仍从全局 `Execution.ActiveTurnID` 取 turn id，即使前面给
   reducer 增加了显式 turnId，也不足以修好 flush 归属。

修订要求：Phase 2 设计一个独立的**历史明细 merge**操作，输入至少包含
`turnId + ordered full items + terminal detailLoadState`，直接按官方 item id 构造/替换该回合
assistant parts；不得伪装成 live delta，不得改变 `ExecutionView`，不得重复 user/final-agent。
可复用 item decoder，但不能原样复用 live reducer 的生命周期语义。新增测试必须覆盖：
已完成回合、非末回合、当前另有 live turn、重复请求、两个 turn 并发展开和空明细。

### P0-4：`session_turn_items` 的分页与 SoT 合约未冻结

计划只给了方法名，没有请求/响应 JSON，也没有回答：

- cursor 是由 iOS 驱动还是 bridge 内部拉到 EOF；页大小和最大页数是什么；
- `nextCursor/backwardsCursor` 是否暴露，失败后从哪一页重试；
- 同一 turn 双击、两台 iPhone 同时展开时是否 singleflight；
- live turn 尚未落库、archive/delete/reconnect 与加载并发时如何 fence；
- `detailLoaded` 怎样进入 Mac projection snapshot，如何随 patch 传给 iOS；
- 只加载一半时是 partial、failed 还是 loaded；下一次请求是否继续。

T2.2 前必须在 `unified-bridge-protocol.md` 先冻结请求、result、error、状态机和幂等键，再写
handler。建议 Mac 负责拉完一个 turn 并原子提交；否则“渐进渲染”和投影原子性会制造半页
永久状态。若确实需要逐页渐进，则必须把 cursor 和 partial 状态纳入 Mac SoT，不能只存在
iOS ViewModel。

### P0-5：desc 首页不能直接按返回顺序写入 projection

目标首屏请求是 `sortDirection=desc`，现有 full hydrate/projection 以时间正序构建 turn 数组。
计划没有规定 Summary page 在进入 reducer 前反转为 oldest→newest，也没有规定继续加载旧页
是 prepend 还是 append。若按响应顺序直接 emit，iOS 列表顺序、latest-running 推断和
“最后一回合”逻辑都会错误。

G1 类型应保留 page metadata；G2 明确：desc 网络页在 projection 中正序化，加载更旧页只能
prepend，turn id 去重，并对 inclusive reverse cursor 写 fixture 测试。

### P0-6：明细类型覆盖不能退化为 Reasoning + CommandExecution

当前 full decoder 还映射 FileChange、MCP、DynamicTool、Plan、WebSearch、ContextCompaction。
计划的探针和 iOS 验收只覆盖 Reasoning/CommandExecution。切换到 items/list 后若只实现这两类，
已工作的 Patch、MCP 和搜索活动会在冷打开时消失。

G0 应按目标真实 session 尽可能捕获每种出现的 item；缺样本类型必须明确标为未验证并保持
fail-closed/SkippedTypes 可观测，不能默默丢弃。G1/G2 fixture 至少冻结当前 schema replay 已
覆盖的十类，真实形状断言只对 live sample 中出现的类型宣称 proven。

## 5. 次要修订

### P1

- 增加 cold-hydrate 与 live event 并发测试：打开 session 的同时 Mac 正在输出、Summary
  返回后 live turn 完成、detail load 时另一个新 turn 开始。验收“live 零改动”不足以证明
  source transaction/fence 没有覆盖新事件。
- 把 §6 更新为已修复现状：当前实现已经是 lifecycle signal + 60s safety scan，head/full
  refresh 有独立退避；已覆盖安装 runtime `b45463c2ded8`。懒加载仍有独立性能价值，但不再是
  轮询风暴的待办修复。
- T0.3 的“每回合恰为两个 item”改为“只允许 first-user/final-agent，分别可缺席；记录
  0/1/2 分布、空文本、interrupted synthetic boundary”。
- §1.1 “每条 Reasoning 的 summary+content 双份”改为字段均存在但可为空；当前 schema replay
  已证明 content 可以是空数组。
- `go test ./go-bridge/` 门应补充 projection detail merge 的 targeted race 测试；不要笼统声称
  全包 race 已证明（仓内曾有与本方案无关的测试 helper race）。

### P2

- 文档第 13 行把官方方法写成了 `turn/items/list`，应为 `thread/items/list`。
- “四个阶段四个门”与正文 Phase 0～4 / G0～G4 不一致，实际是五阶段五门。
- `N=bridged 页大小` 未定义，应给一个唯一常量及服务端 clamp 行为；“建议 30”不是协议。
- `initial_turns_page` 引入提交应补全为 `2a1158b8e2f941afed79db95731b16c8a8db5774`，
  让 §1.4 的 ancestor 检查可复跑。
- 上游当前 HEAD 已从计划记录的 `94311d44` 前进到 `63d21388`；计划已要求 P0 重解析，
  实施者仍应以 tag `rust-v0.150.0-alpha.12.2` 作为目标行为基线，而不是移动的 main。

## 6. G0 必须补齐的未验证内容

以下任一项没有 target Desktop 0.150.0-alpha.12.2 的脱敏 fixture，G0 不应通过：

1. `thread/read` metadata 中的 `historyMode`，至少一个 owner 长 session；若存在两种模式则分组。
2. Summary 的 0/1/2 item 分布、itemsView/status/time/error 和正反 cursor。
3. NotLoaded 的空 items 与同一 turn identity。
4. items/list 的 `ThreadItemEntry.turnId`、asc 两页、next/backwards cursor、空页和非法 turnId。
5. Reasoning 非空 `content`；若目标环境无法产生，验收“展示完整思考”必须改为 pending evidence。
6. Command output 为 null、非空和跨页边界；至少记录一个真实输出体积。
7. Summary 与 items-list 中重复的 user/final-agent item id 是否完全一致（这是去重策略的依据）。
8. 同一 session 的 full read / Summary / items-list 字节数与 wall time，且按 historyMode 分层。

`initialTurnsPage` 可以继续保持可选；若不进入产品路径，不应阻塞 G0，但不得把源码 round-trip
测试写成线上形状已 proven。

## 7. 修订优先级与通过条件

### P0（修订计划后才能实施）

1. 把 `historyMode` 分层加入 G0、性能基线与 legacy 裁决。
2. 删除无法证明的 per-turn `hasDetail` 推断，改成明确的入口/加载状态设计。
3. 设计历史 detail 专用 merge，不复用会改变 execution 的 live reducer 事件。
4. 冻结 `session_turn_items` wire contract、分页所有权、singleflight、错误和 SoT 状态机。
5. 明确 desc→chronological 排序/prepend/去重规则。
6. 把现有十类 item 的冷历史回归面纳入 G1/G2。

### P1（G0/G2 门内完成）

1. 补 live/cold/detail 并发 fence 测试与端到端 payload/TTI 指标。
2. 更新 CPU 关联问题为已修复现状。
3. 收紧 Summary/Reasoning 的可空表述和 race 证据范围。

### P2（编辑修正）

修正方法名、阶段数量、页大小常量、完整 ancestor commit 与移动 main 的引用方式。

**重新评审通过口径：** P0 设计修订完成，G0 fixture 清单包含上述八项，且计划明确
“历史 detail merge 不改变 execution、不会重复 Summary 正文、工具按显式 turnId 归属”。
在此之前，本方案可作为 Phase 0 探针计划使用，但不能作为 Phase 1～3 的直接施工单。
