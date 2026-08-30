# codex-remote 懒加载会话历史实施方案（对齐官方分页协议）

- 日期：2026-08-30（r3，按 r2 复评修订）
- 状态：**Phase 0 已通过计划评审（audit-r2 判定 APPROVED），可执行**；Phase 1/2 仍 BLOCKED——
  G0 八项真实 fixture 未采集，且 r2 复评的三项实现级 P0 已在 r3 落入设计（canonical item id
  持久化、Reasoning 映射裁决、历史明细与 live FlushPatch 隔离），进入 G1 前不得再削减这些约束。
- 评审报告：[r1](2026-08-30-codex-remote-lazy-history-plan-audit.md) /
  [r2 复评](2026-08-30-codex-remote-lazy-history-plan-audit-r2.md)
- 母方案：[2026-08-26-codex-remote-backend-implementation-plan.md](2026-08-26-codex-remote-backend-implementation-plan.md)（其 Phase 0/1 的配对、WSS、envelope、live 投影基础设施已交付并多轮真机验证）
- 目标仓库：`cordcode-macbridge`（`agent/codex-remote` + `go-bridge`）、`cordcode-ios`（配套工作树）
- 上游官方源码（只读）：`/Users/jacklee/Projects/codex`。**目标行为基线是 tag `rust-v0.150.0-alpha.12.2`**（当前用户 Desktop 内嵌版本）；main 持续移动（r3 撰写时 `63d213884d`），只作参考不作基线。

> [!IMPORTANT]
> 本方案把"打开 Codex Desktop 会话"的数据面从重路径（`thread/read includeTurns=true` 一次拉全量回合+全量 items）迁移到官方现行懒加载路径：
> 首屏 `thread/turns/list`（默认 Summary 视图；对 `historyMode=paginated` 的会话每回合仅返回预计算的"首条用户消息/末条 agent 消息"槽位，各自可缺席；**该预计算收益仅限 paginated，legacy thread 每次请求仍重放整份 rollout**），
> 用户点开"详细过程"时再按 `thread/items/list` 分页拉取该回合全量 items，由 Mac 侧整回合原子合入投影。
> 官方对 **paginated thread** 将 `includeTurns=true` 明确注释为 "slow compatibility path"；
> legacy thread 的 full read 不在此结论内（分层见 §1.2/§2.1）。
> 桥面 `session_turn_items` 的响应**只做 ack（detailLoadState + syncRev 或错误）**；
> canonical items 的唯一内容写者是投影 snapshot/patch，RPC result 不携带明细正文。

> [!WARNING]
> 所有协议形状声明以本文引用的 codex-rs 源码行为参考（行号全部按 tag `rust-v0.150.0-alpha.12.2` checkout 生成），但**实施前必须完成 Phase 0 线上探针**，
> 用真实 Desktop app-server 的脱敏 fixture 复核（本仓 audit-plan 纪律：内容形状断言必须有真实样本，
> 不得凭记忆或类比）。G0 必补样本清单见 §3.0.5（八项）与 §3.0.6（脚本防误判约束），其中任何一项缺失 G0 不得通过。
> 性能结论必须按 `historyMode` 分层（paginated / legacy），不得混合平均。

## 0. 来源清单（r3 撰写时）

```text
Mac 仓库=/Users/jacklee/Projects/cordcode-macbridge-codex-remote
Mac 分支=codex/codex-remote-backend
Mac 提交=f64801680e2610dcd884c783765a30be118af9a2（r3 修订时已复核未变）
iOS 配套工作树=/Users/jacklee/Projects/cordcode-ios-codex-remote
iOS 分支=codex/codex-remote-backend-ios
iOS 提交=5565a8612c0b700949ffd9761f4a47d1f3acada1
Codex 上游=/Users/jacklee/Projects/codex @ main @ 63d213884d（只读；行为基线=tag rust-v0.150.0-alpha.12.2，r3 已复核）
线上目标=ChatGPT Desktop 26.825.32147（bundle 7303）/ 内嵌 codex-cli 0.150.0-alpha.12.2
当前已安装 runtime=b45463c2ded8（2026-08-29T17:37:20Z 构建；audit-r2 已独立复核）
```

注意：两仓多会话并行开发。**实施、构建、安装前必须按 CLAUDE.md P0 门用实际构建目录重新解析
分支+HEAD 并复核工作树状态**；本文记录不替代未来复核。

### 0.1 修订记录

**r2 轮（对 r1 评审）**：P0-1～P0-6、P1-1/P1-3/P1-4、P2 全部采纳（落点见 §9）；
P1-2 已独立核实后采纳（§6 重写为已修复）。audit-r2 §2 复核均为 🟢，两项 🟡（FlushPatch 条款、
tag 行号漂移）已在 r3 收口。

**r3 轮（对 r2 复评）**：

| r2 复评条目 | 本稿处置 |
| --- | --- |
| P0-r2-1 Summary final-agent item id 在 reducer 中丢失 | 采纳：§3.2 T2.1 冻结 canonical `ProjectionPart.ItemID` 扩展与全链路保留；T1.2 冻结 agent→bridge 边界不丢 id |
| P0-r2-2 Reasoning decoder summary-first | 采纳：G0.5 改四态统计 + 阻塞语义统一；T1.5/T2.2 禁止沿用 summary-first，映射由 G0 数据+owner 裁决冻结 |
| P0-r2-3 不得修改共享 live FlushPatch | 采纳：T2.2 改为专用 `replace_parts`（显式 turnId/messageId）+ 同 patch detailLoadState upsert；删除 r2 稿"改造 FlushPatch tool PartOp"条款；live 零改动边界保留 |
| P1-1 singleflight 键补 backend 维度 | 采纳：方法 fail-closed 限定 `backendId=codex-remote`（§3.2.0） |
| P1-2 result 只做 ack、单一内容写者 | 采纳：§3.2.0 响应改为 `{detailLoadState, syncRev}` / 错误 |
| P1-3 G0.5 阻塞/pending 语义矛盾 | 采纳（选择"owner 裁决删主张"分支）：无法产生非空 content 时 owner 须裁决从本版验收与实现主张中删除"完整思考"，不保留 pending 形状声明 |
| P1-4 整回合资源门 | 采纳：T0.2 增单 turn 资源画像；G0 裁决 maxPages/maxBytes/timeout 的 fail-closed 行为，Phase 2 按裁决实现，不截断合法历史 |
| P1-5 "空明细"定义 | 采纳：§2.2 定义为"去除 Summary 重复槽位后无 reasoning/tool/file 等明细 item" |
| P1-6 deprecation 措辞按 historyMode 限定 | 采纳：IMPORTANT 框与 §1.2 已限定 paginated |
| P2-1 tag 行号重算 | 采纳：§1.2 行号全部换为 tag checkout 值 |
| P2-2 ItemsView 已存在 | 采纳：T1.2 改"保留/传播"（`remoteTurn.ItemsView` 现存于 `history.go:81`） |
| P2-3 页大小表述 | 采纳：T1.1 改"定义请求 limit 常量、接受服务端 clamp、按实际 nextCursor 拉到 EOF" |
| P2-4 §9-R3 重分类 | 采纳：R3 不再列为"不采纳"，改为"维持评审结论"（见 §9） |

## 1. 背景与已验证证据

### 1.1 现状问题

`agent/codex-remote/history.go:280` 打开会话时发送 `thread/read` 带 `includeTurns: true`，一次性拉取
全部回合的全部 items。注意：Reasoning 的 `summary[]` 与 `content[]` 字段**均存在但各自可为空数组**
（现有 schema replay 中 content 即为空）；`CommandExecution.aggregated_output` 可为 null。重路径的代价：

1. 传输与编码成本：WSS 字节、bridge JSON、投影构建全量承担（对 legacy 会话服务端仍全量重放 rollout，见 §1.2）；
2. 与官方客户端行为相反——ChatGPT iOS App 打开 Codex 会话很快，思考过程/执行步骤默认不展示、点击才加载（owner 观察 + 源码验证）。

### 1.2 官方协议证据（codex-rs，tag `rust-v0.150.0-alpha.12.2` checkout 行号）

| 事实 | 位置（tag） | 前提/边界 |
| --- | --- | --- |
| `thread/read` 的 `include_turns` serde 默认 false；doc 注释宣布全量水合已废弃 | `app-server-protocol/src/protocol/v2/thread.rs:1650-1658` | — |
| `thread/turns/list` 参数含 `cursor/limit/sortDirection/itemsView`；默认 **Summary** | `thread.rs:1684-1698`；服务端默认 `thread_processor.rs:3087,3159` | — |
| `TurnItemsView` 三态：`NotLoaded` / `Summary` / `Full`（临时兼容） | `v2/thread_data.rs:377-385` | — |
| Summary 预计算：thread-store 每 turn 持久化 first-user / final-agent 两个 optional summary 槽位 | `thread-store/src/local/thread_history/read.rs:62-78,123-132` | **仅 `historyMode=paginated`**。legacy thread 走 `load_thread_turns_list_history` 每次请求重放整份 rollout（官方注释："it still replays the entire rollout on every request"） |
| Summary 槽位各自可缺席：0/1/2 item 均可能出现；synthetic fork boundary 会继承源回合摘要 | `read.rs:128-132` | "每回合恰为两 item"不成立 |
| `thread/items/list`：`turnId` 可选过滤 + cursor 分页 + 双向 | `thread.rs:1718-1732` | ThreadItemEntry 含 `turnId`+`item` |
| `Turn` 结构仅 `id/items/itemsView/status/error/time` | `v2/thread_data.rs`（Turn 定义） | **无 item count / type 集合 / has-detail 标志**——入口判定不能依赖 Summary 元数据 |
| `ThreadItem::Reasoning`：`summary[]` 与 `content[]` 均存在（serde default）、可各自为空；两字段语义不同（完整思考 vs 摘要），映射规则须样本裁决 | `v2/item.rs:268-275` | 同名 `content` 在 `UserMessage` variant 上是用户输入、在 `Reasoning` 上是思考正文——**同名异义，统计必须按 item type 分组**（§3.0.6） |
| `CommandExecution.aggregated_output` 可 null；十类 item（userMessage/reasoning/agentMessage/commandExecution/fileChange/mcpToolCall/dynamicToolCall/plan/webSearch/contextCompaction）在 full 读取中均出现 | `v2/item.rs`；`testdata/phase2/thread-read-app-server.json`（schema replay，非 live） | schema replay 只证明解码面，不证明 live 形状 |
| 实时通知轻量：`turn/started` items=NotLoaded；完成仅带 `last_agent_message` | `bespoke_event_handling.rs:170,1303-1305` | — |
| 官方注释点名 paginated thread 的旧路径为 "slow compatibility path"；`Full` 视图为临时兼容路径 | `thread_processor.rs:3165-3166,3261-3262` | **仅 paginated 语义**；legacy full read 不在此结论内 |
| `thread/resume.initialTurnsPage` 可带首个 turns 页 | `thread.rs:407-409,446-449`；引入提交 `2a1158b8e2f941afed79db95731b16c8a8db5774`（ancestor 检查可复跑） | 保持可选，不进产品主路径 |

### 1.3 与现有实现的关系

- 现有 live 流（codec/ws/pairing/backoff/重连）**全部保留不动**；目录（`thread/list`）不动。
- **共享 live tool `FlushPatch` 与 `ps.tools` 累加器不动**（audit-r2 P0-r2-3：改它们会波及所有 backend 的
  live patch 热路径，违反本方案 live 零改动边界）；历史明细走专用原子操作（§3.2 T2.2）。
- agent 层现有 `text_delta.itemId` 通道已传递 agent message 官方 id；本方案要求该 id 在
  agent→bridge→projection 全链路持久化（现状：projection text part 不保存 id，audit-r2 已核对
  `handlers_projection.go:1362-1376` / `projection_reducer.go:673-717` / `projection_types.go:19-20`，
  修复设计见 §3.2 T2.1）。
- 现有 full decoder 已映射十类 item——切换到 `thread/items/list` 后**不得**退化为只实现 Reasoning/CommandExecution 两类。
- 现有 Reasoning 历史映射是 summary-first（`history.go:345-351`：先 `summary[]`，空才读 `content[]`）——
  detail 路径**禁止原样沿用**（audit-r2 P0-r2-2：summary 与 content 同时非空时将永久隐藏完整思考）。

### 1.4 内嵌版本包含性（已验证）

`TurnItemsView` 引入提交 `9e0c191c13` 与 `initial_turns_page` 引入提交 `2a1158b8e2f941afed79db95731b16c8a8db5774`
均已通过 `git merge-base --is-ancestor <commit> rust-v0.150.0-alpha.12.2` 验证包含在用户当前 Desktop 内嵌版本中。
Phase 0 仍需线上实证真实应答形状。

## 2. 目标行为（验收口径）

### 2.1 首屏

iOS 打开任意 Codex Desktop 会话 → Mac agent 以 Summary 视图取最近页（`sortDirection=desc` 首页，
页大小常量 `CODEX_REMOTE_TURNS_PAGE_SIZE = 30`，以服务端实际 clamp 为准并记录生效值）→
**desc 网络页在投影前反转为 oldest→newest**；继续加载更旧页只能 **prepend**，按 turn id 去重，
反向 anchor cursor 为 inclusive（需 fixture 测试）。每回合呈现 first-user / final-agent 槽位内容
（各自可缺席，允许 0/1/2 分布与空文本）。

**historyMode 分层验收**：G0 须按 `thread.read` 元数据中的 `historyMode` 分组记录
（paginated / legacy 两种都存在——codex-web 0.149 真实 fixture 已证明现实中两态并存）。
性能主张分两层表述：传输/编码收益（两种模式都有）与服务端查询收益（仅 paginated）。
legacy 会话的产品策略由 T0.5 显式裁决，**禁止静默回退 full 或宣称 legacy 获得预计算收益**。

### 2.2 展开详细过程

- **入口判定**：所有"可分页且已完成"的回合统一显示"加载详细过程"入口（r1 评审 P0-2 方案 A）。
  不从 Summary 反推"是否有工具/思考"——`Turn` 元数据不含该信息，反推即编造。
- **加载状态机**：`detailLoadState = notRequested | loading | loaded | failed`。**空明细也是 loaded**：
  "空明细"定义为**去除与 Summary 槽位重复的 user/final-agent 后，没有 reasoning/tool/fileChange 等
  明细 item**（上游 items 数组为空只是其子集；fixture 与 UI 测试用同一口径，audit-r2 P1-5）。
  不引入未经证明的 `hasDetail` 字段。
- **拉取与提交**：iOS 发起 `session_turn_items`（桥面方法，§3.2.0 冻结）→ **Mac 侧负责把该回合
  items/list 拉到 EOF 并原子合入投影**（singleflight、幂等），不向 iOS 暴露分页 cursor；
  重复请求返回已合入结果。整回合原子渲染；逐页渐进渲染**不进入本版**（搁置理由见 §9-R2）。
  **响应只做 ack**：`{detailLoadState, syncRev}` 或错误——canonical items 只随投影
  snapshot/patch 下发，投影是唯一内容写者（audit-r2 P1-2：避免 result 与 patch 双倍大载荷及竞态）。
- **合并语义**：专用历史明细 merge（§3.2 T2.2），通过显式 `(turnId, messageId)` 的原子
  `replace_parts`（或语义等价专用 op）+ 同 patch 的 detailLoadState upsert 提交；按官方 item id
  构造/替换该回合 assistant parts；不伪装 live delta、不改变 `ExecutionView`/不 markRunning、
  不把历史工具塞进 `ps.tools`、**不修改共享 live FlushPatch**；不重复 Summary 已有的
  user/final-agent 正文（去重依据 = canonical 持久化的官方 item id，G0.7 证据 + §3.2 T2.1 存储）。
- **Reasoning 明细口径**：映射规则由 G0.5 四态样本 + owner 裁决冻结（候选：A. content 非空优先
  展示完整 content、summary 为缺席时替代；B. summary 与 content 是两个独立 part）。
  **禁止沿用现 summary-first 映射后宣称"完整思考"**（audit-r2 P0-r2-2）。
- **整回合资源门**：Mac 拉到 EOF 的 maxPages / maxBytes / timeout 及超限 fail-closed 行为
  （超限 → `failed` + 原因码，不截断、不以 placeholder 代替超大 tool output）由 G0 按 T0.2
  资源画像裁决，Phase 2 按裁决值实现（audit-r2 P1-4）。

### 2.3 实时与回退

- 实时回合照旧走 live 通知；live 回合完成后的历史补齐走 Summary 语义，不触发全量。
- 冷水合与 live 事件并发、detail 加载与新 turn 并发必须有 fence 测试（§3.2 T2.5）。
- 回退（fail-closed）：Phase 0 探针若证明某方法不可用/形状不符 → 停工上报 owner 裁决；
  可回退保留 `includeTurns=true` 旧路径并记录证据；**禁止静默回退**。

## 3. 实施阶段（五个阶段、五道门 G0–G4）

每阶段沿用本仓 impl/tests/regression 三段交付纪律；引用门未过不得进入下一阶段。
**当前判定（audit-r2）：Phase 0 = APPROVED 可执行；G1/G2 = BLOCKED（G0 fixture 未采 + 本节设计约束不得削减）。**

### Phase 0 — 线上证据探针（阻塞门 G0）

- T0.1 复用 `testdata/phase0` 探针设施与 owner 手册流程（localhost 表单、配对码不进聊天），
  对 live Desktop app-server 调用并捕获**脱敏 fixture**：
  - `thread/turns/list`（`itemsView:"summary"` / `"notLoaded"`、desc 首页、翻页 cursor 正反往返）；
  - `thread/items/list`（按 turnId 过滤、asc 两页、空页、非法 turnId）；
  - `thread/read`（元数据，**必须记录 `thread.historyMode`**）。
- T0.2 体积与耗时基线，**按 historyMode 分组**：同一会话的 full read / Summary 首页 / items-list
  的响应字节数、RPC wall time、bridge 投影字节数、冷打开 TTI；CPU 结论分写成
  server work 与 transport/encoding work 两栏。**单回合资源画像**：每采样 turn 的 items 总字节、
  页数、最大单 item 字节——供 G0 裁决 maxPages/maxBytes/timeout。
- T0.3 Summary 页断言：只允许 first-user / final-agent 两类 item、**各自可缺席**；
  记录 0/1/2 分布、空文本、interrupted synthetic boundary 继承行为；核对 `itemsView/status/time/error`。
- T0.4 十类 item 覆盖：按 owner 真实会话尽可能捕获每种出现的 item 类型；缺样本类型明确标为
  未验证并保持 fail-closed / SkippedTypes 可观测。
- T0.5 **legacy 裁决**：若 owner 现存长会话含 `historyMode=legacy`，向 owner 呈现 T0.2 分层数据并
  裁决产品策略（接受"仅传输收益"或对该类会话保留旧路径），裁决记录入 fixture 元数据。
- **G0 通过条件 = §3.0.5 八项样本齐全**（任一缺失不得通过，含 G0.5 的 owner 裁决闭环）
  + T0.2 分层基线与资源画像 + 资源门裁决值 + T0.5 裁决（如适用）。

#### 3.0.5 G0 必补八项（未验证内容清单）

1. `thread/read` 元数据中的 `historyMode`（至少一个 owner 长会话；两态并存则分组）；
2. Summary 的 0/1/2 item 分布、`itemsView/status/time/error` 与正反 cursor；
3. NotLoaded 空 items 与同一 turn identity；
4. items/list 的 `ThreadItemEntry.turnId`、asc 两页、next/backwards cursor、空页与非法 turnId；
5. Reasoning **summary/content 四态分布**（均空 / 仅 summary / 仅 content / 双非空）+ 至少一个
   非空 `content[]` 真实样本；**若目标环境无法产生非空 content，owner 必须裁决从本版验收与实现
   主张中删除"完整思考"，不保留 pending 形状声明**（audit-r2 P1-3：阻塞与 pending 二选一，本稿选
   owner 裁决删主张，未裁决前 G0 不通过）；
6. CommandExecution output 为 null / 非空 / 跨页边界，至少一个真实输出体积；
7. Summary 与 items/list 中重复的 user/final-agent **官方 item id 是否完全一致**（去重策略依据）；
8. 同会话 full / Summary / items-list 的字节与 wall time，按 historyMode 分层。

`initialTurnsPage` 保持可选：不进产品主路径则不阻塞 G0，但其源码 round-trip 测试不得写成线上已 proven。

#### 3.0.6 G0 脚本防误判约束（audit-r2 §5/§8）

- **所有 `content`/`summary` 统计必须按 ThreadItem `type` 分组**：`content` 在 `userMessage` 与
  `reasoning` 上同名异义（schema replay 已实证全局计数会产生 0-vs-1 的假分歧）；
- **Summary↔items 去重必须输出 `turnId → summary item ids → full item ids` 映射**：first-user 与
  final-agent 分别验证，不得把两个槽位合并成集合后只看交集或只比文本/总数。

### Phase 1 — Mac agent：`agent/codex-remote`（门 G1）

- T1.1 `history.go` 拆分：
  - `ReadThreadSummary(ctx, threadID, cursor)` → `thread/read`（元数据）+ `thread/turns/list`（Summary）；
  - `ReadTurnItems(ctx, threadID, turnID)` → `thread/items/list` 循环拉到 EOF：**定义请求 limit 常量
    （沿用 `CODEX_REMOTE_TURNS_PAGE_SIZE` 或独立 `CODEX_REMOTE_ITEMS_PAGE_SIZE`），接受服务端 clamp，
    按实际 `nextCursor` 判 EOF**（客户端没有协商出的上限值，不写"用服务端上限"）；
  - 删除打开路径上的 `includeTurns=true`（保留为显式命名的兼容函数，仅供 T0.2 基线与降级路径）。
- T1.2 类型与页元数据：**保留/传播** `remoteTurn.ItemsView`（已存在，`history.go:81`）与页元数据
  （next/backwards cursor、网络顺序）供 G2 正序化；Summary 槽位映射为现有 `EventUserMessage`/`EventText`
  语义时标注 "summary 槽位"来源，**且必须携带官方 item id 走现有 `text_delta.itemId` 通道**——
  agent→bridge 边界不得丢弃 id（canonical 持久化见 T2.1；现状：agent 已传 id，projection 侧丢失）。
- T1.3 live 路径零改动；`catalog.go` 零改动。
- T1.4 单测（fixtures 驱动）：Summary 首页/翻页/空页、turnId 过滤、itemsView 透传、
  **十类 item 解码（fixture 下限 = schema replay 已覆盖十类；proven 主张仅限 live 样本中出现的类型）**、
  Reasoning content 为空数组的行为、SkippedTypes 计数可观测。
- T1.5 **Reasoning detail 映射按 G0.5 裁决实现**：不得复用 `history.go:345-351` 的 summary-first
  规则；四态样本驱动单测（均空/仅 summary/仅 content/双非空各一 case，样本缺失的 case 标
  unverified 且不宣称 proven）。
- **G1**：`go test ./agent/codex-remote/` 全绿（含 race）；形状断言全部来自 G0 fixture。

### Phase 2 — go-bridge：投影、专用明细 merge 与桥面 RPC（门 G2）

#### 3.2.0 wire contract 先行（冻结后才能写 handler）

在写任何 handler 前，先在 `docs/protocol/unified-bridge-protocol.md` 冻结：

- **方法归属**：`session_turn_items` 仅注册于 `backendId=codex-remote`，其他 backend 调用
  fail-closed 返回 unsupported（audit-r2 P1-1）；幂等/singleflight 键 = `(sessionId, turnId)`
  （在方法归属前提下已含 backend 维度）；
- **请求/响应**：请求 `{ sessionId, turnId }`；响应 **只做 ack**：`{ detailLoadState, syncRev }`
  或 `{ error }`——**canonical items 不进 result**，投影 snapshot/patch 是唯一内容写者；
  `syncRev` 用于 iOS 将 ack 与随后 patch 对齐；
- **分页所有权**：**Mac 拉到 EOF 后原子提交**（`replace_parts` + 同 patch `detailLoadState` upsert）；
  iOS 不见 cursor、无 partial 状态；
- **资源门**：maxPages / maxBytes / timeout 用 G0 裁决值；超限 → `failed` + 原因码，
  不截断合法历史、不以 placeholder 代替超大 tool output；
- **幂等/singleflight**：同一 `(sessionId, turnId)` 并发请求（双击、两台 iPhone）合并为一次拉取；
  重复请求返回当前 ack；
- **fence**：live turn 未落库、archive/delete、重连窗口与 detail 加载并发时的裁决顺序（投影 SoT 串行化）；
- `detailLoadState` 进入 Mac 投影 snapshot 并随 patch 下发；`failed` 可重试；
- **错误语义**：上游 RPC 错误 → `failed + 原因码`；空明细（§2.2 口径）→ `loaded`。

#### 实施任务

- T2.1 投影消费 Summary：历史路径改为消费 agent 的 Summary 历史；**desc 网络页先反转为
  oldest→newest 再入 reducer；加载更旧页只 prepend；turn id 去重；inclusive backwards cursor
  有 fixture 测试**。
  **Canonical item id 持久化（audit-r2 P0-r2-1）**：把 `ProjectionPart.ItemID` 从 tool-only 扩展为
  所有官方 item variant（text/reasoning 等），同步 Go schema 注释与 Swift 端
  `SessionProjectionPart.itemId`（iOS 字段已存在，`makeText/makeReasoning` 默认写 nil 需接通）；
  snapshot、patch、restore 全链路保留，不得只放本次 handler 的临时 map。
  新增测试：Summary snapshot 中 final-agent part 的 itemId 非空；重连 restore 后 detail merge
  仍按同一 id 去重。
- T2.2 **专用历史明细 merge**（不复用 live reducer 生命周期语义，不改共享 FlushPatch）：
  - 输入：`turnId + 有序全量 items + 终态 detailLoadState`；
  - 提交通道：**现有 wire 的 `replace_parts`（显式 `turnId, messageId`）+ 同 patch 的
    `detailLoadState` upsert**，直接针对请求的回合原子提交（iOS 已实现 `replace_parts`）；
    **不把历史工具塞进 `ps.tools`、不修改 live tool FlushPatch**（audit-r2 P0-r2-3：改共享
    FlushPatch 会改变所有 backend 的 live patch 热路径、扩大回归面并违反 live 零改动边界；
    若未来确需重构 FlushPatch，必须另立方案并扩充全部 backend 的 live 回归矩阵，不在本方案内）；
  - 按**官方 item id** 构造/替换该回合 assistant parts（依赖 T2.1 持久化的 id），不伪装
    `reasoning_delta/text_delta`、不 `markRunning`、不改变 `ExecutionView`；
  - 去重：Summary 已有的 user/final-agent 按 canonical item id 对齐（G0.7 证据 + §3.0.6 映射脚本），
    不重复 append；
  - Reasoning 映射按 G0.5 裁决（§2.2），禁止 summary-first；
  - 可复用 item decoder；十类 item 中缺 live 样本的类型按 SkippedTypes 观测，不静默丢弃。
- T2.3 桥面方法实现（按 §3.2.0 冻结稿）；管理 API/relay 测试复用 `fakeAgentSession` 模式。
- T2.4 协议文档更新与 handler 同提交入库。
- T2.5 **并发与归属测试矩阵**：已完成回合、非末回合、当前另有 live turn、
  重复请求（幂等）、两 turn 并发展开（singleflight）、空明细（§2.2 口径）、冷水合与 live 输出
  同时进行、Summary 返回瞬间 live turn 完成、detail 加载时新 turn 开始、**replace_parts 重复提交
  幂等**、**重连 restore 后 detail merge 去重**。
- **G2**：`go test ./go-bridge/` 全绿 + **detail merge 与投影并发路径的定向 race 测试通过**
  （不笼统主张全包 race 已证明——仓内存在与本方案无关的测试 helper race）；桥面协议文档变更入同一提交。

### Phase 3 — iOS：懒加载 UX（门 G3）

- T3.1 会话页按 Summary items 渲染（现有 UI 主要即渲染用户消息与 agent 回复，预期改动小）。
- T3.2 所有可分页已完成回合统一"加载详细过程"入口：点击 → `session_turn_items` →
  收到 ack（`detailLoadState/syncRev`）后**从投影 patch 渲染明细**（投影是唯一内容写者，
  iOS 不从 RPC result 取 items）；按 `detailLoadState` 呈现
  loading/loaded（含 §2.2 口径的"无详细过程"文案）/failed 可重试；
  `loaded` 后入口变为直接展开/收起，不再发请求（防重复拉取）。
- T3.3 live 回合不受影响；断线重连后已加载明细不丢（投影 SoT 在 Mac，`detailLoadState` 与
  canonical itemId 随 snapshot 恢复）。
- T3.4 单测（ChatViewModel/projection store）+ 真机构建安装（`scripts/run.sh device`，
  遵循 REAL_DEVICE_DEBUGGING.md）。
- **G3**：真机回归 + owner 验收矩阵（§4）。

### Phase 4 — 回归与交付（门 G4）

- 全量定向测试（两仓）、Release 构建 + 覆盖安装（BUILD_INSTALL_AND_RUNTIME.md 交付条件）、
  真机矩阵全绿、CHANGELOG/Owner 手册同步、G0 基线复核（打开会话字节/TTI 对比数据入交付说明）。

## 4. Owner 真机验收矩阵

| # | 步骤 | 期望 |
| --- | --- | --- |
| 1 | iPhone 打开一个长历史 Codex Desktop 会话 | 首屏可交互时间明显缩短（对照 G0 基线）；最近回合正常显示 |
| 2 | 点开某回合"加载详细过程" | 展示该回合思考（按 G0.5 裁决口径；若 owner 已裁决删除"完整思考"主张，则按裁决后口径验收）/工具/命令/文件变更等完整明细 |
| 3 | 点开一个无工具纯对话回合 | 明细加载完成，按 §2.2 口径显示"无详细过程"或仅思考摘要——**不得报错** |
| 4 | 再次点开同一回合 | 即时展开，无网络拉取（detailLoadState=loaded） |
| 5 | Mac 端同会话发起新回合 | iOS live 同步照旧（live 零回归） |
| 6 | 断网/代理切换后重连，重复 1-4 | 行为一致；无重复拉取、无风暴日志；执行状态不被误标 running；已加载明细不丢 |
| 7 | 加载明细同时 Mac 端开始新回合 | 明细与新 turn 互不串扰（fence 生效） |

（矩阵 #3 覆盖"空明细也是 loaded"；#6 覆盖"不重复正文/不误标执行中/restore 保留"三条红线。）

## 5. 非目标

- 中继 cursor 断线续传、出站 seq/ack 重放缓冲：既有 fail-closed 缺口，维持不动。
- `codex-web` 后端任何改动。
- `initialTurnsPage` 进产品主路径（保持可选观察）。
- 逐页渐进渲染（见 §9-R2 搁置理由）。
- 重构共享 live tool FlushPatch（audit-r2 P0-r2-3：如确需，另立方案并扩充全部 backend 的 live 回归矩阵）。

## 6. 关联已修复问题（上下文更新）

2026-08-29 晚诊断的 CPU 高占用（会话发现轮询自激循环）**已由并行会话修复并部署**：
`71ef328`（失败目录探针退避）、`9c30ca2`（事件驱动目录刷新）、`c0e26fe`（空闲投影补丁消除）、
`7171083`（剩余热路径收缩）、`b45463c`（诊断日志降级），已安装 runtime `b45463c2ded8`
（audit-r2 已独立复核）。
本方案的懒加载价值因此定位为**独立的传输/编码与体验收益**（更小的打开载荷、官方路径对齐），
不再是轮询风暴的待办修复。

## 7. 约束（实施期间持续有效）

1. 双仓 P0 来源门：Mac 仅 `cordcode-macbridge-codex-remote` 工作树，iOS 仅配套工作树；构建/安装前
   在实际构建目录复核分支+HEAD。
2. 不做 asar/resign/MITM/进程劫持；配对码只进 localhost 表单；探针 fixture 必须 gitleaks 脱敏通过。
3. 两仓不 push、不合 main，除非 owner 明确要求。
4. 协议形状断言必须有 live fixture 或 tag 源码行号双重支撑；**目标行为基线是
   `rust-v0.150.0-alpha.12.2`，不是移动的 main**（引用行号一律按 tag checkout 生成）；版本偏差
   fail-closed 并上报。
5. 改 `agent/`/`go-bridge/` 后的交付条件：定向测试 + `./scripts/build-unsigned-release.sh` +
   覆盖安装 + 启动验证；遵守 D 级成本纪律。
6. live 流（codec/ws/pairing/backoff）零回归：各阶段回归必须包含"发送消息 live 同步"检查。
7. 历史明细 merge 红线：**不改变 execution、不重复 Summary 正文、工具按显式 turnId 归属**。
8. **live 隔离红线（audit-r2 P0-r2-3）**：不修改共享 `FlushPatch`/`ps.tools` live 热路径；
   历史明细只经显式 `(turnId, messageId)` 的专用原子 op（`replace_parts` 等价）提交。
9. **单一内容写者（audit-r2 P1-2）**：`session_turn_items` result 只做 ack；canonical items
   只经投影 snapshot/patch 下发。

## 8. 交付物清单

- [ ] `agent/codex-remote/testdata/phase0/live/`：turns/list、items/list、thread/read(含 historyMode)
      脱敏 fixtures + 分层体积/耗时基线 + 单回合资源画像 + 资源门裁决值 + （如适用）legacy 裁决记录
- [ ] `agent/codex-remote/history.go` 拆分 + 兼容路径显式化 + 页元数据与 item id 传递保留 +
      Reasoning 映射按 G0.5 裁决重写（非 summary-first）
- [ ] `go-bridge` 投影 Summary 化（正序化/prepend/去重）+ canonical `ProjectionPart.ItemID`
      全 variant 持久化 + 专用历史明细 merge（`replace_parts` 通道）+ 并发归属测试矩阵
- [ ] `docs/protocol/unified-bridge-protocol.md`：`session_turn_items` wire contract
      （ack-only、backend 归属、资源门、幂等/fence——先冻结后实现）
- [ ] iOS 统一入口 + detailLoadState 状态机 + ack/patch 分离渲染 + 防重复拉取
- [ ] 两仓测试全绿、真机矩阵（§4）全绿、文档同步

## 9. 评审采纳记录（两轮，含不采纳项理由）

### r1 轮（对 [r1 评审](2026-08-30-codex-remote-lazy-history-plan-audit.md)）

| 编号 | 建议 | 处置 | 理由 |
| --- | --- | --- | --- |
| — | P0-1～P0-6、P1-1/P1-3/P1-4、P2 全部 | **采纳** | 落点见 r2 稿；audit-r2 §2 复核确认全部落实（两项 🟡 已在 r3 收口） |
| R1 | P0-2 方案 B：per-turn detail probe（先探针判断是否有明细） | **不采纳** | 每回合一次探针 RPC 会重新引入本方案要消除的请求重量（N 个回合 = N 次探针）；且评审自己标注"真实、便宜且有样本证明"为前提，当前无证据满足。方案 A（统一入口 + `detailLoadState`，空明细展示"无详细过程"）语义等价且零额外请求 |
| R2 | 逐页渐进渲染 | **搁置（部分采纳）** | 主路径定为 Mac 整回合拉 EOF 原子提交：半页状态与投影原子性冲突、partial 语义复杂。超长回合的处理走资源门（G0 裁决 maxPages/maxBytes/timeout，超限 fail-closed + owner 裁决），而不是预先设计 cursor 下放的渐进扩展；当前无数据支撑 |

### r2 轮（对 [r2 复评](2026-08-30-codex-remote-lazy-history-plan-audit-r2.md)）

| 编号 | 建议 | 处置 | 理由 |
| --- | --- | --- | --- |
| — | P0-r2-1、P0-r2-2、P0-r2-3，P1-1～P1-6，P2-1～P2-4 | **全部采纳** | 落点见 §0.1 r3 表。r2 复评**无整体不采纳项**；两处二选一均选择复评建议分支：P1-3 选"owner 裁决删主张"（消除阻塞/pending 矛盾）；P0-r2-3 选"专用 `replace_parts` 原子 op"而非重构共享 FlushPatch |
| （重分类） | r2 稿 §9-R3 "initialTurnsPage 进产品路径不采纳" | **改为"维持评审结论"**（audit-r2 P2-4） | r1 评审本就裁定 initialTurnsPage 可选且不阻塞；本方案与其一致，属确认而非否决，不应列在"不采纳"下 |

## 10. 变更历史

- 2026-08-30 r1：初稿。
- 2026-08-30 r2：按 r1 评审修订——historyMode 分层、统一入口 + detailLoadState、专用历史明细 merge
  （三红线）、wire contract 先冻结、desc 正序化/prepend/去重、十类 item 回归面、G0 八项样本清单、
  并发 fence 测试矩阵、§6 更新为已修复现状、P2 编辑修正。
- 2026-08-30 r3：按 r2 复评修订——canonical `ProjectionPart.ItemID` 全 variant 持久化与全链路
  保留（P0-r2-1）、Reasoning 四态裁决并禁 summary-first（P0-r2-2）、历史明细改走显式
  `(turnId,messageId)` 的 `replace_parts` 原子通道且不改共享 FlushPatch（P0-r2-3）、wire contract
  改 ack-only 单一内容写者 + backend 归属 + 资源门（P1-1/2/4）、"空明细"口径定义（P1-5）、
  deprecation 措辞限定 paginated（P1-6）、G0.5 阻塞语义统一（P1-3）、tag 行号重算与 ItemsView/
  页大小/§9-R3 编辑修正（P2-1～4）、新增 §3.0.6 脚本防误判约束。r2 复评无整体不采纳项，见 §9。
