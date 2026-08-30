# codex-remote 懒加载会话历史实施方案（对齐官方分页协议）

- 日期：2026-08-30（r6 收敛终版，已吸收最终评审的四项定向修订）
- 状态：**设计阶段收敛（audit-r4 为终审；r5 合规核验与 r6 最终评审均已闭环，
  后续新问题仅由 G0 fixture、定向测试或真机证据触发并在对应 gate 内修复）。Phase 0 = APPROVED，
  可立即开工**；G1 BLOCKED（G0 fixtures + identity/shape 断言 + 未知 item 原子失败规则落地）；
  G2 BLOCKED（等待 per-connection delivery / `turnStateOps` canonical wire、实现与测试落地；
  设计契约已在 §3.2.0 收口）。
- 评审报告：[r1](2026-08-30-codex-remote-lazy-history-plan-audit.md) /
  [r2](2026-08-30-codex-remote-lazy-history-plan-audit-r2.md) /
  [r3](2026-08-30-codex-remote-lazy-history-plan-audit-r3.md) /
  [r4 终审](2026-08-30-codex-remote-lazy-history-plan-audit-r4.md) /
  [r5 合规与源码可行性核验](2026-08-30-codex-remote-lazy-history-r5-compliance-source-check.md) /
  [r6 最终评审](2026-08-30-codex-remote-lazy-history-plan-final-audit-r6.md)
- 开工指令：[2026-08-30-codex-remote-lazy-history-kickoff-directive.md](2026-08-30-codex-remote-lazy-history-kickoff-directive.md)
- 母方案：[2026-08-26-codex-remote-backend-implementation-plan.md](2026-08-26-codex-remote-backend-implementation-plan.md)（其 Phase 0/1 的配对、WSS、envelope、live 投影基础设施已交付并多轮真机验证）
- 目标仓库：`cordcode-macbridge`（`agent/codex-remote` + `go-bridge`）、`cordcode-ios`（配套工作树）
- 上游官方源码（只读）：`/Users/jacklee/Projects/codex`。**目标行为基线是 tag `rust-v0.150.0-alpha.12.2`**（当前用户 Desktop 内嵌版本）；main 持续移动（r6 撰写时 `63d213884d`），只作参考不作基线。

> [!IMPORTANT]
> 本方案把"打开 Codex Desktop 会话"的数据面从重路径（`thread/read includeTurns=true` 一次拉全量回合+全量 items）迁移到官方现行懒加载路径：
> 首屏 `thread/turns/list`（默认 Summary 视图；对 `historyMode=paginated` 的会话每回合仅返回预计算的"首条用户消息/末条 agent 消息"槽位，各自可缺席；**该预计算收益仅限 paginated，legacy thread 每次请求仍重放整份 rollout**），
> 用户点开"详细过程"时再按 `thread/items/list` 分页拉取该回合全量 items，由 Mac 侧整回合原子合入投影；
> 滚动到顶加载更早历史**复用现有 `projection_window_v1` 的 `older` 请求链**——Mac producer 用内部 upstream cursor 按需把更早 Summary 页归约进 Kernel，**历史补水合 commit 按 connection delivery mode 分发**，**不新增第二套 iOS 历史分页 RPC，upstream cursor 绝不过桥**（bridge-v1.md R2）。
> **上游复用原则（本方案第一原则）**：codex-rs 已有现成实现的能力——"取什么、怎么分页、何时 EOF"——
> 直接镜像官方实现（核心锚点：`thread_processor.rs:3222-3258` 的 `paginated_turn_full_items`：
> 固定 turnId、Asc、最大页尺寸、每页严格反序列化、`nextCursor=nil` 才 EOF、cursor 原地重复立即报错），
> **不重新发明分页不变量**；本项目只实现桥接职责：投影 SoT、revision、connection delivery、iOS 状态与交互。
> 官方对 **paginated thread** 将 `includeTurns=true` 明确注释为 "slow compatibility path"；
> legacy thread 的 full read 不在此结论内（分层见 §1.2/§2.1）。
> 桥面 `session_turn_items` 的响应**只做 ack（detailLoadState + syncRev，或请求级 WireError）**；
> canonical items 的唯一内容写者是投影 snapshot/patch，RPC result 不携带明细正文。

> [!WARNING]
> 所有协议形状声明以本文引用的 codex-rs 源码行为参考（行号全部按 tag `rust-v0.150.0-alpha.12.2` checkout 生成），但**实施前必须完成 Phase 0 线上探针**，
> 用真实 Desktop app-server 的脱敏 fixture 复核（本仓 audit-plan 纪律：内容形状断言必须有真实样本，
> 不得凭记忆或类比）。G0 必补样本清单见 §3.0.5（九项）、脚本防误判约束见 §3.0.6、**负结果判定见
> §3.0.7（采到不一致结果 = G0 失败，不是通过）**。任何一项缺失或任一负结果触发，G0 不得通过。
> 性能结论必须按 `historyMode` 分层（paginated / legacy），不得混合平均；**`thread/items/list`
> 可用性按 historyMode 分层实测，legacy 不继承 paginated 结论**（§2.5）。
> **生产环境不得自动回退 `includeTurns=true`**（仅 owner 显式裁决的 historyMode/版本范围可走旧路径，
> 新 RPC 超时或形状错误一律显式报错）。
> **证据边界**：codex-rs 源码证明的是 app-server 的协议与服务端能力；ChatGPT iOS App 的默认折叠/
> 点击加载是 **owner 黑盒观察**，两者分开表述，不声称复刻 iOS 私有客户端实现。

## 0. 来源清单（r6 撰写时）

```text
Mac 仓库=/Users/jacklee/Projects/cordcode-macbridge-codex-remote
Mac 分支=codex/codex-remote-backend
Mac 提交=6ff83c8（r6 最终评审前 HEAD；最终评审报告与本次定向修订尚未提交）
iOS 配套工作树=/Users/jacklee/Projects/cordcode-ios-codex-remote
iOS 分支=codex/codex-remote-backend-ios
iOS 提交=5565a8612c0b700949ffd9761f4a47d1f3acada1
Codex 上游=/Users/jacklee/Projects/codex @ main @ 63d213884d（只读；行为基线=tag rust-v0.150.0-alpha.12.2，r4 终审/r5 核验已复核）
线上目标=ChatGPT Desktop 26.825.32147（bundle 7303）/ 内嵌 codex-cli 0.150.0-alpha.12.2
当前已安装 runtime=b45463c2ded8（2026-08-29T17:37:20Z 构建；audit-r2 已独立复核）
```

注意：两仓多会话并行开发。**实施、构建、安装前必须按 CLAUDE.md P0 门用实际构建目录重新解析
分支+HEAD 并复核工作树状态**；本文记录不替代未来复核。§1.3/§3.2 引用的本仓/iOS 行号为
audit-r2～r5 核对时点值，实施时须复核。

### 0.1 修订记录

**r2 轮（对 r1 评审）**：P0-1～P0-6、P1-1/P1-3/P1-4、P2 全部采纳；P1-2 已独立核实后采纳（§6）。

**r3 轮（对 r2 复评）**：P0-r2-1/2/3、P1-1～P1-6、P2-1～P2-4 全部采纳（audit-r3 §2 复核全部 🟢 关闭）。

**r4 轮（对 r3 复评）**：P0-r3-1、P1-r3-1/2/3、P2-r3-1/2 全部采纳（audit-r4 §2 复核全部 🟢）。

**r5 轮（对 r4 终审）**：P0-r4-1/2/3、P1-r4-1～4 全部采纳（r5 合规核验 7/7 ✅）；两项二选一定案
（failed ack 形状、per-turn generation）见 §9。

**r6 轮（对 r5 合规与官方源码可行性核验）**：

| 核验条目 | 本稿处置 |
| --- | --- |
| P0（G2 前）三字段 sparse `upsertTurns` 无法被现有 Turn schema 解码（Go `Status` 必填 `projection_types.go:96-105`；Swift 非 optional `SessionProjection.swift:226-236`） | 采纳并定案：弃 sparse upsertTurns，**新增专用 `turnStateOps` patch op**（turnId/detailLoadState/reasonCode/generation，Go/Swift 显式应用）；理由见 §9-r6 定案 3；§3.2.0 已改写 |
| P1 证据边界措辞（iOS UI 不是"源码验证"） | 采纳：§1.1 改为"owner 黑盒观察 + codex-rs 独立证明 app-server 能力"，IMPORTANT 框加证据边界声明 |
| 官方复用清单（核验 §6 表） | 采纳：T1.1 改为**镜像官方不变量**（`paginated_turn_full_items` 六项 + `paginated_thread_turns_list_response`），新增 §1.5 官方锚点表，红线 §7-13 |
| `initialTurnsPage` 作为 G0 有数据候选 | 采纳：新增 T0.6 候选路径实测（baseline vs `thread/resume(excludeTurns=true, initialTurnsPage=summary)` 对比；启用条件三选；源码锚点 `thread_processor.rs:3292-3332`、官方测试 `thread_read.rs:1048-1119`） |
| legacy 分层能力矩阵（items/list 独立必测，不继承 paginated） | 采纳：新增 §2.5 能力矩阵；T0.1/G0 增加 legacy 会话 items/list 探测；`Unsupported → method-not-found`（`thread_processor.rs:3398-3402`）记录为合法负结果探测目标 |
| "完整思考原文"承诺措辞 | 采纳：产品承诺改为"按需加载服务端实际提供的 reasoning 摘要、工具调用与执行步骤"；G0 非空 content 证据前不承诺"完整思维链"（§2.2/§4） |

**r6 最终评审定向修订（不新增版本轮次）**：

| 最终评审条目 | 本稿处置 |
| --- | --- |
| P0-final-1 `initialTurnsPage` 漏 `excludeTurns:true` | 采纳：T0.6/§1.2/§1.5/交付清单统一为完整请求；响应强制断言 `thread.turns == []`，否则候选失败 |
| P1-final-1 `turnStateOps` 未进入 change set | 采纳：§3.2.0 冻结 `changedTurnIDs` union、`orderChanged=false` 与 state-only UI 刷新测试 |
| P1-final-2 `reasonCode` 清除语义缺失 | 采纳：failed 必填非空；loading/loaded 显式清除；非法组合 fail-closed |
| P2-final-1 无 legacy 样本时 N/A | 采纳：先做 target inventory；无 legacy 时附证据标 `N/A(no target legacy thread)`，不伪造样本、不继承 paginated 结论 |

**评审收敛声明**：本稿为设计阶段终版，r6 最终评审四项已全部写回。**不再发起新一轮开放式文档评审**；
后续新问题只允许由 G0 fixture、定向测试或真机证据触发，并在对应 gate（G0/G1/G2）内修复。

## 1. 背景与已验证证据

### 1.1 现状问题

`agent/codex-remote/history.go:280` 打开会话时发送 `thread/read` 带 `includeTurns: true`，一次性拉取
全部回合的全部 items。注意：Reasoning 的 `summary[]` 与 `content[]` 字段**均存在但各自可为空数组**
（现有 schema replay 中 content 即为空）；`CommandExecution.aggregated_output` 可为 null。重路径的代价：

1. 传输与编码成本：WSS 字节、bridge JSON、投影构建全量承担（对 legacy 会话服务端仍全量重放 rollout，见 §1.2）；
2. 与用户期望的交互相反——ChatGPT iOS App 打开 Codex 会话很快、思考/步骤默认折叠、点击才加载是
   **owner 黑盒观察**；codex-rs 源码**独立证明**目标 Desktop app-server 原生提供"元数据/Summary 首屏/
   按 turn 明细分页"能力（§1.2、§1.5），因此本项目可以实现同类交互，但**不声称复刻 ChatGPT iOS 的
   私有客户端实现**。

### 1.2 官方协议证据（codex-rs，tag `rust-v0.150.0-alpha.12.2` checkout 行号）

| 事实 | 位置（tag） | 前提/边界 |
| --- | --- | --- |
| `thread/read` 的 `include_turns` serde 默认 false；doc 注释宣布全量水合已废弃 | `app-server-protocol/src/protocol/v2/thread.rs:1650-1658` | — |
| 仅 `includeTurns=true` 才水合完整 turns；paginated 走 `paginated_thread_full_turns`，legacy 加载重放 rollout | `thread_processor.rs:2981-3000` | 冷打开改用元数据 + Summary 页确实绕开旧全量应答路径 |
| `thread/turns/list` 参数含 `cursor/limit/sortDirection/itemsView`；默认 **desc + Summary** | `thread.rs:1684-1698`；服务端 `thread_processor.rs:3087,3159`（列表主实现 `3006-3089,3151-3219`） | — |
| `TurnItemsView` 三态：`NotLoaded` / `Summary` / `Full`（临时兼容） | `v2/thread_data.rs:377-385` | — |
| Summary 预计算：thread-store 每 turn 持久化 first-user / final-agent 两个 optional summary 槽位 | `thread-store/src/local/thread_history/read.rs:62-78,123-132` | **仅 `historyMode=paginated`**。legacy thread 走 `load_thread_turns_list_history` 每次请求重放整份 rollout（官方注释："it still replays the entire rollout on every request"） |
| Summary 槽位各自可缺席：0/1/2 item 均可能出现；synthetic fork boundary 会继承源回合摘要 | `read.rs:128-132` | "每回合恰为两 item"不成立 |
| **官方测试**验证正反 cursor 与三态：Summary 只留首条 user/末条 agent；NotLoaded 空 items 且 turn identity/status/timing 不变 | `app-server/tests/suite/v2/thread_read.rs:330-477` | 本仓 fixture 断言与单测场景的直接来源 |
| `thread/items/list`：`turnId` 可选过滤 + cursor 分页 + 双向；`ThreadItemEntry{turn_id,item}` | `thread_processor.rs:3365-3422`；`thread.rs:1718-1732` | — |
| ThreadStore Unsupported → items/list 返回 method-not-found | `thread_processor.rs:3398-3402` | **legacy 可用性必须独立实测**（§2.5），不继承 paginated 结论 |
| `paginated_turn_full_items`：单回合拉到 EOF 的官方算法（固定 turnId、Asc、最大页尺寸、每页严格反序列化、`nextCursor=nil` 才 EOF、cursor 原地重复立即报错） | `thread_processor.rs:3222-3258` | **T1.1 `ReadTurnItems` 的行为模板**（§1.5） |
| 官方集成测试覆盖 turn filter、items cursor、Summary/NotLoaded/Full | `thread_read.rs:1781-1934` | 同上，fixture 场景来源 |
| `Turn` 结构仅 `id/items/itemsView/status/error/time` | `v2/thread_data.rs`（Turn 定义） | **无 item count / type 集合 / has-detail 标志**——入口判定不能依赖 Summary 元数据 |
| `ThreadItem::Reasoning`：`summary[]` 与 `content[]` 均存在（serde default）、可各自为空；两字段语义不同（完整思考 vs 摘要），映射规则须样本裁决 | `v2/item.rs:268-275` | 同名 `content` 在 `UserMessage` variant 上是用户输入、在 `Reasoning` 上是思考正文——**同名异义，统计必须按 item type 分组**（§3.0.6） |
| `CommandExecution.aggregated_output` 可 null；十类 item（userMessage/reasoning/agentMessage/commandExecution/fileChange/mcpToolCall/dynamicToolCall/plan/webSearch/contextCompaction）在 full 读取中均出现 | `v2/item.rs`；`testdata/phase2/thread-read-app-server.json`（schema replay，非 live） | schema replay 只证明解码面，不证明 live 形状 |
| 实时通知轻量：`turn/started` items=NotLoaded；完成仅带 `last_agent_message` | `bespoke_event_handling.rs:170,1303-1305` | — |
| 官方注释点名 paginated thread 的旧路径为 "slow compatibility path"；`Full` 视图为临时兼容路径 | `thread_processor.rs:3165-3166,3261-3262` | **仅 paginated 语义**；legacy full read 不在此结论内 |
| `thread/resume.initialTurnsPage`：与 `excludeTurns:true` 组合后，一次往返同时取得 live resume subscription 与首个 turns 页；处理 live active turn 占位避免首屏超 limit | `thread.rs:398-409,446-449`；`thread_processor.rs:3292-3332`；官方 README `376-382,405-421`；官方测试 `thread_read.rs:1048-1119`（显式 `exclude_turns:true`、`thread.turns` 为空、initial page 与同参数 turns/list 一致） | G0 候选路径实测（T0.6），不因源码存在直接启用；漏传 `excludeTurns` 会走默认 full hydration，候选直接失败 |
| `initialTurnsPage` 引入提交 `2a1158b8e2f941afed79db95731b16c8a8db5774`、`TurnItemsView` 引入提交 `9e0c191c13` | `git merge-base --is-ancestor <commit> rust-v0.150.0-alpha.12.2`（已验证） | 内嵌版本包含性成立 |

### 1.3 与现有实现的关系（audit-r2～r5 核对的现状）

- 现有 live 流（codec/ws/pairing/backoff/重连）**全部保留不动**；目录（`thread/list`）不动。
- **共享 live tool `FlushPatch` 与 `ps.tools` 累加器不动**（audit-r2 P0-r2-3）；历史明细走专用原子操作（§3.2 T2.2）。
- agent 层现有 `text_delta.itemId` 通道已传递 agent message 官方 id；本方案要求该 id 在
  agent→bridge→projection 全链路持久化（现状：projection text part 不保存 id，修复设计见 §3.2 T2.1）。
- 现有 full decoder 已映射十类 item——切换到 `thread/items/list` 后**不得**退化为只实现 Reasoning/CommandExecution 两类。
- 现有 Reasoning 历史映射是 summary-first（`history.go:345-351`）——detail 路径**禁止原样沿用**（audit-r2 P0-r2-2）。
- **iOS 窗口机器已是生产路径**：canonical 协议定义 `get_session_projection_window` 的
  `older/newer/latest/locate`、opaque bridge-owned cursor、turn-aligned 窗口与 snapshot cut
  （`docs/protocol/bridge-v1.md:1517-1620`）；iOS `ProjectionStore.pullWindow` 消费
  `nextOlderCursor` 并按 turn id 合并页（`Models/ProjectionStore.swift:527-631`）；滚动路径已调用
  `.older`（`ViewModels/ChatViewModel+MessageSync.swift:972-990`）；冷打开优先走窗口
  （`ViewModels/ChatViewModel+SessionManagement.swift:753-808`）。**Phase 3 只验证/复用，不重造**。
- **Mac 窗口层断点**：`get_session_projection_window` 先 `ensureProjectionHydrated` 再从一次
  committed snapshot `sliceProjectionWindow`（`go-bridge/projection_window_handler.go:93-120,143-179`）；
  切片直接 `turns := proj.Turns`，`hasOlder` 只由 slice 位置决定（`projection_window.go:210-224,236-258`）。
  Kernel 无法自行到达上游第 2 页——若只把首屏 30 回合写入 Kernel，窗口会把它们误当全集返回
  `hasOlder=false`（=历史截断）。
- **canonical R2 已规定边界**：wire cursor 恒为 projection-kernel-owned；上游分页 artifact 是
  Mac producer 内部状态，不得过桥或嵌入 wire cursor（`bridge-v1.md:1594-1601`）。
- **patch 广播现状（audit-r4 核对，P0-r4-1 根源）**：Mac `deliverProjectionPatchLocked` 向 session
  的**所有** SSV2 target 广播同一 patch，无 window coverage / 请求连接过滤
  （`go-bridge/event_publisher.go:658-765`）；iOS `SessionProjection.upsertingTurns` 对未知 turn 直接
  append（`Models/SessionProjection.swift:255-265`），`ProjectionReplica.applyPatch` 把
  `upsertTurns != nil` 视为顺序变化（`Models/ProjectionReplica.swift:115-144`）；coverage ledger 只在
  RPC window page 经 `applyWindowPage` 后更新（`Models/ProjectionStore.swift:477-507`），普通 push
  patch 不更新它。**若历史补页走普通 `upsertTurns` 广播：未请求 older 的设备会把旧回合 append 到
  尾部（顺序错误）、coverage 失真；简单抑制 patch 又会让其他连接 appliedRev 落后、下一条 live
  patch base mismatch。**必须按连接投递（§2.4）。
- **checkpoint 现状**：`SessionProjection` 只有 session/revision/execution/turns；
  `ProjectionCheckpoint` 除 Projection 外只有 source checkpoints 与 `ClaudeSourceState`
  （`go-bridge/projection_types.go:113-123`、`go-bridge/projection_kernel.go:86-118`），schema v10；
  source validation 基于本地 path/cursor prefix，codex-remote 属 pathless hydrate 路径——
  producer page state 需要新的 backend-private checkpoint（T2.0），**不得**把 opaque string cursor
  塞进 `ProjectionSourceDescriptor.Cursor int64`。
- **patch 形状现状（r6 核验 §4.1 阻塞的根源）**：wire `ProjectionPatch` 只有 `UpsertTurns` 与
  `PartOps`；两端 `upsertTurns` 元素都是**完整 turn 类型**——Go `TurnProjection.Status` 必填
  （`go-bridge/projection_types.go:96-105`）、Swift `SessionTurnProjection.status` 非 optional
  （`Models/SessionProjection.swift:226-236`），`merged(with:)` 按完整 turn 合并，`ProjectionReplica`
  把任意 `upsertTurns != nil` 标 `orderChanged=true`；iOS patch 按 `execution → upsertTurns →
  partOps` 顺序应用（`Models/ProjectionStore.swift:1102-1110`）。**r5 冻结的三字段 sparse
  `upsertTurns` 无法被该 schema 解码——改为专用 `turnStateOps` op（§3.2.0 定案）**。

### 1.4 内嵌版本包含性（已验证）

见 §1.2 末行：`TurnItemsView`（`9e0c191c13`）与 `initial_turns_page`
（`2a1158b8e2f941afed79db95731b16c8a8db5774`）均包含于 tag `rust-v0.150.0-alpha.12.2`。
Phase 0 仍需线上实证真实应答形状。

### 1.5 官方复用锚点表（r6 新增；"直接抄官方实现"原则的任务落点）

| 本项目任务 | 官方锚点（tag `rust-v0.150.0-alpha.12.2`） | 复用方式 |
| --- | --- | --- |
| metadata-only read | `apply_thread_read_store_fields`（仅 includeTurns 才水合，`thread_processor.rs:2981-3000`） | 行为对齐：不含 includeTurns 的 read 只取元数据 |
| Summary/NotLoaded 首屏 | `thread_turns_list_response_inner` + `paginated_thread_turns_list_response`（`3006-3089,3151-3219`） | 参数/默认值/三态语义直接采用 |
| 单回合 items 请求 | `thread_items_list_response_inner`（`3365-3422`） | 参数与响应形状直接采用 |
| 单回合拉到 EOF | `paginated_turn_full_items`（`3222-3258`）：固定 turnId、Asc、最大页尺寸、每页严格反序列化、`nextCursor=nil` 才 EOF、cursor 原地重复立即报错 | **逐项镜像其不变量**，再叠加 G0 裁决的 maxPages/maxBytes/timeout |
| 全量路径只作兼容基线 | `paginated_thread_full_turns` 的 "slow compatibility path"（`3261-3290`） | 仅探针/基线/owner 裁决范围 |
| resume 一次往返候选 | `paginated_resume_initial_turns_page(_with_active_slot)`（`3292-3332`）+ 官方请求形状 `excludeTurns:true + initialTurnsPage`（README `405-421`、测试 `1089-1111`） | T0.6 实测候选；必须断言 `thread.turns == []`，裁决后才可能启用 |
| 行为测试基线 | `app-server/tests/suite/v2/thread_read.rs:330-477,1048-1119,1781-1934` | 本仓 fixture 断言与单测场景的直接来源 |

上游"取什么、怎么分页、何时 EOF"遵循 codex-rs；本项目只实现它不得不承担的桥接职责——投影 SoT、
revision、connection delivery、iOS 状态与交互。**不要复制或重写上游已有分页语义，也不要把 bridge
内部 cursor 暴露成上游 cursor。**

## 2. 目标行为（验收口径）

### 2.1 首屏

iOS 打开任意 Codex Desktop 会话 → Mac agent 以 Summary 视图取最近页（`sortDirection=desc` 首页，
页大小常量 `CODEX_REMOTE_TURNS_PAGE_SIZE = 30`，以服务端实际 clamp 为准并记录生效值）→
**desc 网络页在投影前反转为 oldest→newest**；首屏即写入 Kernel（此后更早历史按 §2.4 按需补入）。
每回合呈现 first-user / final-agent 槽位内容（各自可缺席，允许 0/1/2 分布与空文本）。

**historyMode 分层验收**：G0 须按 `thread.read` 元数据中的 `historyMode` 分组记录
（paginated / legacy 两种都存在——codex-web 0.149 真实 fixture 已证明现实中两态并存）。
性能主张分两层表述：传输/编码收益（两种模式都有）与服务端查询收益（仅 paginated）。
legacy 会话的产品策略由 T0.5 显式裁决，**禁止静默回退 full 或宣称 legacy 获得预计算收益**。

### 2.2 展开详细过程

- **入口判定**：所有"可分页且已完成"的回合统一显示"加载详细过程"入口（r1 评审 P0-2 方案 A）。
  不从 Summary 反推"是否有工具/思考"——`Turn` 元数据不含该信息，反推即编造。
- **加载状态机**：`detailLoadState = notRequested | loading | loaded | failed`，**turn-level 字段**，
  经**专用 `turnStateOps` patch op** 承载（形状与应用顺序冻结见 §3.2.0；缺席解码为 notRequested）。
  **空明细也是 loaded**："空明细"定义为**去除与 Summary 槽位重复的 user/final-agent 后，没有
  reasoning/tool/fileChange 等明细 item**（上游 items 数组为空只是其子集；fixture 与 UI 测试用同一口径）。
  不引入未经证明的 `hasDetail` 字段。
- **产品承诺口径（r6 收紧）**：可承诺"按需加载服务端实际提供的 reasoning 摘要、工具调用与执行
  步骤"；**在 G0 取得非空 Reasoning content 证据（或 owner 裁决删除该主张）之前，不承诺"展示完整
  思维链/思考原文"**。
- **拉取与提交**：iOS 发起 `session_turn_items`（桥面方法，§3.2.0 冻结）→ **Mac 侧负责把该回合
  items/list 拉到 EOF 并原子合入投影**（singleflight、幂等），不向 iOS 暴露分页 cursor；
  重复请求返回已合入结果。整回合原子渲染；逐页渐进渲染**不进入本版**（见 §9-R2）。
  **响应只做 ack**：canonical items 只随投影 snapshot/patch 下发，投影是唯一内容写者。
  **完成条件顺序无关**：受理后 Mac 先把 `loading` 提交进投影 SoT（singleflight follower 观察同一
  状态）；成功时在同一 Kernel transaction 提交 `replace_parts + loaded` 得 `syncRev=N`，ack 仅在
  commit 成功后返回；**iOS 以 replica `appliedRev >= N` 为完成条件**，patch 先于或后于 ack 到达
  均可，不得因先收到 ack 就从 RPC result 渲染内容；失败时提交 `failed + reasonCode` 再返回
  failed ack；重试从 `failed` 回到 `loading`。
- **未知/不支持 item 整回合原子失败（audit-r4 P0-r4-3）**：`SkippedTypes` 是**诊断字段**，
  不是"丢弃后继续成功"的开关。items/list 出现无法无损映射的 type/shape 时：**中止本回合 commit**、
  保留原 Summary parts 不变、提交 `detailLoadState=failed + reasonCode=unsupported_item_type`；
  **不得执行部分 `replace_parts`、不得标 `loaded`**；修复/升级后允许重试。
  已映射但只有 schema replay、无 live 样本的类型可以实现并测试，但交付报告不得标 live proven。
- **合并语义**：专用历史明细 merge（§3.2 T2.2），通过显式 `(turnId, messageId)` 的原子
  `replace_parts` + 同 patch 的 `turnStateOps`（置终态）提交；按官方 item id 构造/替换该回合
  assistant parts；不伪装 live delta、不改变 `ExecutionView`/不 markRunning、不把历史工具塞进
  `ps.tools`、**不修改共享 live FlushPatch**；不重复 Summary 已有的 user/final-agent 正文
  （去重依据 = canonical 持久化的官方 item id，G0.7 证据 + §3.2 T2.1 存储）。
- **stale-write fence（token 定案，audit-r4 P1-r4-2）**：新增**持久化 per-turn generation**
  （投影 turn 内单调递增计数器，该回合 completion 后任何 mutation 都会 bump；随 Kernel
  snapshot 持久化，schema bump 一并冻结）。`replace_parts` admission token =
  `(backendId, sessionId, turnId, turnGeneration)`。提交前验证目标仍是同一 completed turn；
  目标删除/回合被修正/generation 改变 → typed stale 并保留新 truth。**不用全局 baseRev**
  （其他 turn 的 live append 会误杀本回合 detail）——测试须证明：目标 turn 改变 → stale；
  其他 turn 更新 → 不受影响。
- **Reasoning 明细口径**：映射规则由 G0.5 四态样本 + owner 裁决冻结（候选：A. content 非空优先
  展示完整 content、summary 为缺席时替代；B. summary 与 content 是两个独立 part）。
  **禁止沿用现 summary-first 映射后宣称"完整思考"**（audit-r2 P0-r2-2）。
- **整回合资源门**：Mac 拉到 EOF 的 maxPages / maxBytes / timeout 及超限 fail-closed 行为
  （超限 → `failed` + 原因码，不截断、不以 placeholder 代替超大 tool output）由 G0 按 T0.2
  资源画像裁决，Phase 2 按裁决值实现。

### 2.3 实时与回退

- 实时回合照旧走 live 通知；live 回合完成后的历史补齐走 Summary 语义，不触发全量。
- 冷水合与 live 事件并发、detail 加载与新 turn 并发必须有 fence 测试（§3.2 T2.5）。
- **回退纪律**：Phase 0 探针若证明某方法不可用/形状不符 → 停工上报 owner 裁决；
  兼容路径（`includeTurns=true`）**默认仅探针/基线使用**；生产路径失败一律显式报错，
  **不得因新 RPC 超时或形状错误自动 full read**；只有 owner 显式裁决的 historyMode/版本范围
  允许进入旧路径，且裁决须记录可移除条件。

### 2.4 更早历史的按需加载（upstream 分页 ↔ `projection_window_v1` 接线）

**目标**：首屏 30 回合之后，更早历史经现有窗口链可达；不新增第二套 iOS 历史分页 RPC。

- **iOS 侧零新协议**：继续只用 `get_session_projection_window` 的 `older/newer/latest/locate` 与
  bridge-owned opaque cursor（现有 `pullWindow`/`nextOlderCursor`/coverage ledger/视口锚定/
  cursor-stale 恢复实现原样复用）。
- **Mac producer on-demand 补水合**：处理 `older` 请求时，若请求范围超出已提交 Kernel 且 producer
  持有"仍有更早上游历史"事实（upstream `nextCursor` 非空），先以内部 upstream cursor 经 agent
  `ReadThreadSummary(ctx, threadID, cursor)` 拉 `thread/turns/list` 下一页，按 desc→asc 反转 +
  inclusive cursor 去重 + **prepend 归约进同一 Kernel truth**（同一 snapshot fence / syncRev 链），
  再由既有切片逻辑返回窗口。**每次窗口请求至多触发一页上游拉取（有界，受资源门约束）**；
  仍不足时由后续 `older` 请求继续，不循环拉全量。
- **按连接投递与 revision 语义（audit-r4 P0-r4-1）**：历史补水合 commit **不得**按普通
  `upsertTurns` patch 广播给该 session 全部连接。按每个 `(conn, backend, session)` 注册的
  **delivery mode**（window / full；bridge 在窗口 RPC 首次命中时登记，不能只看连接声明的
  capability）分发同一 snapshot 的 mutation，保持**一条连续 revision 链**：
  1. **请求者 window 连接**：该页只经本次 window result 交付（`syncRev=N` 接续）——
     "一个 turn 在同一 page chain 只归属一个 window response"，不被 push patch 绕过；
  2. **其他 window 连接**：**不收到**未请求的 historical turns；用现有 `projection_patch` shape 的
     connection-specific no-op revision patch 把 applied revision 从 N-1 推进到 N
     （不携带新 turn、不改变本地 coverage）——抑制 patch 会导致后续 live patch base mismatch，
     此方式维持单链；
  3. **full-projection 连接**：收到完整历史 mutation（完整 truth 义务不减）；
  4. 请求者断线 / sink overflow / 两设备并发 older / 随后 live patch 的 baseRev 链为测试矩阵
     固定场景（T2.5）；
  5. 若需 connection-specific patch selection，仍复用现有 `projection_patch` shape 与 ordered
     sink（**不是第二条 pipe**），但须先修订 `bridge-v1.md` R3/R4/R10 及对应 Mac/iOS 测试
     （T2.0 契约复核）。
- **诚实 hasOlder**：`hasOlder=false` 仅当 Kernel 无更旧**且**上游 EOF；"尚未水合"必须表现为
  `hasOlder=true`，**绝不把"未加载"报告成"会话起点"**。wire cursor 仍由 kernel turn ids +
  admission cut 派生，**upstream cursor 绝不过桥、不嵌入 wire cursor**（bridge-v1.md R2）。
- **producer 状态持久化（有界且可验证）**：`hasOlderUpstream` + 内部 upstream cursor 存入**新的
  backend-private producer checkpoint**（具体字段、schema version bump、clone/save/restore 由
  T2.0 冻结；不复用 `ProjectionSourceDescriptor.Cursor int64`，也不用本地文件 prefix digest 假装
  已验证远端 cursor）。**恢复**：restart 后先以 target RPC 验证/续接 cursor（如以持久化边界
  turn id 对照上游翻页首结果），错误则进入 typed recovery；"从 latest 重翻到 overlap"的恢复任务
  是**有界**的——每次任务 maxPages/maxBytes/timeout 与**持久化 continuation**，达到上限返回显式
  retryable failure，**不得一次循环扫完全历史**。
- **并发裁决**：多 iPhone 并发 `older` 按会话串行化/singleflight（同一补水合只拉一次）；
  live tail 同时推进时 prepend 不得影响 live append（snapshot cut 不回退、syncRev 链不断）；
  upstream cursor stale（服务端数据变化）→ typed error + 走恢复路径，不得以旧页覆盖新 truth。
- **契约先行**：实施前复核 bridge-v1.md 现行窗口契约（含 R3/R4/R10）是否允许 producer-on-demand
  扩展 Kernel 与按连接投递；不允许则先修订 canonical 契约与测试，再写代码。

### 2.5 historyMode 能力矩阵（r6 新增，核验 §5）

| historyMode | turns Summary 首屏 | turn-filtered items/list | 首屏收益 | 点击明细策略 |
| --- | --- | --- | --- | --- |
| **paginated** | 必测（G0） | 必测（G0） | 传输/编码 + 服务端查询 | 官方 items/list 主路径（§1.5 锚点） |
| **legacy** | inventory 存在则必测，否则有证据 N/A | **存在则独立必测，不继承 paginated 结论**（源码不能保证 legacy 有 ThreadStore items 索引；Unsupported → method-not-found，`thread_processor.rs:3398-3402`）；不存在则 inventory-backed N/A | 至少传输/编码；服务端仍重放 rollout | owner 明确裁决：保留旧 full 兼容路径 **或** 明确不支持该类会话的明细展开；**禁止自动回退** |

G0 先对目标 thread 做 `historyMode` inventory：若存在 legacy，会话的 `thread/items/list` 探测样本
与 T0.5 裁决均为必需；若 inventory 证明目标环境没有 legacy，则该能力格记为
`N/A(no target legacy thread)` 并保存 inventory 证据，**不得凭空制造 legacy 样本，也不得把 paginated
结论外推给未来出现的 legacy 会话**。若 legacy 存在但不支持 items/list，"打开快"仍成立（首屏
Summary），明细展开按 owner 裁决执行并记录入 fixture 元数据。

## 3. 实施阶段（五个阶段、五道门 G0–G4）

每阶段沿用本仓 impl/tests/regression 三段交付纪律；引用门未过不得进入下一阶段。
**当前判定（r6 最终评审四项已写回）：Phase 0 = APPROVED 可立即开工；G1 BLOCKED；G2 BLOCKED
（等待 canonical wire、实现与测试，不再有文档评审前置项）。**

### Phase 0 — 线上证据探针（阻塞门 G0）

- T0.1 复用 `testdata/phase0` 探针设施与 owner 手册流程（localhost 表单、配对码不进聊天），
  先枚举目标 thread 并保存 `threadId → historyMode` 脱敏 inventory，再对 live Desktop app-server
  调用并捕获**脱敏 fixture**：
  - `thread/turns/list`（`itemsView:"summary"` / `"notLoaded"`、desc 首页、翻页 cursor 正反往返、
    **>30 回合会话翻页全链到 EOF**）；
  - `thread/items/list`（按 turnId 过滤、asc 两页、空页、非法 turnId；paginated 必测；**inventory
    存在 legacy 时 legacy 另测一组**，不存在则附 inventory 证据标
    `N/A(no target legacy thread)`——§2.5 能力矩阵）；
  - `thread/read`（元数据，**必须记录 `thread.historyMode`**；同会话 **includeTurns=true 的
    control inventory 仅限探针使用**，供 §3.0.7 对照）。
- T0.2 体积与耗时基线，**按 historyMode 分组**：同一会话的 full read / Summary 首页 / items-list
  的响应字节数、RPC wall time、bridge 投影字节数、冷打开 TTI；CPU 结论分写成
  server work 与 transport/encoding work 两栏。**单回合资源画像**：每采样 turn 的 items 总字节、
  页数、最大单 item 字节——供 G0 裁决 maxPages/maxBytes/timeout。
- T0.3 Summary 页断言：只允许 first-user / final-agent 两类 item、**各自可缺席**；
  记录 0/1/2 分布、空文本、interrupted synthetic boundary 继承行为；核对 `itemsView/status/time/error`。
- T0.4 十类 item 覆盖：按 owner 真实会话尽可能捕获每种出现的 item 类型；缺样本类型明确标为
  未验证并保持 fail-closed / SkippedTypes 可观测（仅诊断，终态规则见 §2.2）。
- T0.5 **legacy 裁决 / N/A**：若 inventory 含 `historyMode=legacy`，向 owner 呈现 T0.2 分层数据
  **+ §2.5 能力矩阵实测结果**并裁决产品策略（接受"仅传输收益"或对该类会话保留旧路径；
  items/list 不可用时裁决明细展开策略），裁决记录入 fixture 元数据（该裁决同时是 §2.3 唯一允许
  生产走旧路径的入口）；若 inventory 无 legacy，记录 `N/A(no target legacy thread)` + inventory
  证据后关闭本项，但未来发现 legacy 时仍 fail-closed，不继承 paginated 能力结论。
- T0.6 **`initialTurnsPage` 候选路径实测（r6 新增，final-audit 修正请求形状）**：对比
  baseline（`thread/read(includeTurns=false)` + `thread/turns/list(summary)`）与
  candidate（`thread/resume(excludeTurns=true, initialTurnsPage={limit:30, sortDirection:desc,
  itemsView:summary})`，源码锚点 `thread_processor.rs:3292-3332`、官方 README `376-382,405-421`、
  官方测试 `thread_read.rs:1048-1119`）的往返数、响应总字节、耗时与 live subscription 重复性；
  **响应必须断言 `thread.turns == []`**，非空即证明发生默认 full hydration，候选直接失败且全量字节
  不得排除在性能统计之外；仅在
  **目标 Desktop 实测支持 + 不重复现有 live subscription + 耗时数据支持** 三条件齐备时才裁决
  启用候选路径，否则维持 baseline；裁决记录入 fixture 元数据。**不因源码存在直接启用 experimental
  参数**。
- 采样脚本先落实 §3.0.7 的 pass/fail 断言与 control inventory 对照（不依赖 Phase 2 架构）。
- **G0 通过条件 = §3.0.5 九项样本齐全（或仅 legacy 格按 inventory 规则提供有证据 N/A；其他任一
  缺失不得通过，含 G0.5 owner 裁决闭环）
  且 §3.0.7 零负结果触发** + T0.2 分层基线与资源画像 + 资源门裁决值 + T0.5 裁决或有证据 N/A
  + T0.6 候选路径裁决记录。

**2026-08-30 G0 owner 裁决记录（四项全部批准，含约束；证据报告
`docs/2026-08-30-codex-remote-lazy-history-g0-evidence-report.md`）**：

1. **T0.5 legacy**：同意——仅在明确 `historyMode=legacy` 时保留旧全读路径；**不得作为 paginated
   失败后的自动 fallback**；全读超时必须显式报错。
2. **T0.6 resume 候选**：同意——仅对**已验证支持的 paginated 版本**启用
   `resume(excludeTurns:true + initialTurnsPage)`；每个连接/session **只 attach 一次**；
   未验证版本必须**预先选择**官方 metadata + turns/list baseline，不得先失败再静默 full-read。
3. **G0.5 reasoning content**：同意——删除"完整思考"承诺，产品统一称**"思考摘要"**；
   summary 为空时不补 placeholder；未来发现非空 content 须重新采样取证后再增加能力。
4. **资源门（冻结层级）**：turns page limit 30；items request limit 5；单回合 **24 页或
   512KB 任一先到即原子失败**；单 RPC 30s；**整个单回合拉取 90s 总 deadline**（不是
   24×30s）；超限分别返回明确 `max_pages` / `max_bytes` / `timeout` reasonCode，不截断、
   不提交部分明细。后续仅可依据真实 `resource_limit` 触发数据调整，不得自动扩大。

**G0 记分方式（owner 指定）**：G0 记为 **owner 接受两项证据替代后的 PASS**——
(a) paginated control inventory 不可获得（替代证据：官方源码/测试、cursor 链完整性、EOF、
backwards round-trip、legacy 同通道对照）；(b) 账号无 >30 回合线程（替代证据：多页 items
fixture + 官方分页不变量）。**不得宣称"九项实测达成"，亦不得宣称已取得 paginated includeTurns
control 或 >30-turn live fixture。**

#### 3.0.5 G0 必补九项（未验证内容清单）

1. `thread/read` 元数据中的 `historyMode`（至少一个 owner 长会话；两态并存则分组）；
2. Summary 的 0/1/2 item 分布、`itemsView/status/time/error` 与正反 cursor；
3. NotLoaded 空 items 与同一 turn identity；
4. items/list 的 `ThreadItemEntry.turnId`、asc 两页、next/backwards cursor、空页与非法 turnId
   （paginated 必测；legacy 按 inventory 分组实测或附证据标 `N/A(no target legacy thread)`）；
5. Reasoning **summary/content 四态分布**（均空 / 仅 summary / 仅 content / 双非空）+ 至少一个
   非空 `content[]` 真实样本；**若目标环境无法产生非空 content，owner 必须裁决从本版验收与实现
   主张中删除"完整思考"，不保留 pending 形状声明**（未裁决前 G0 不通过）；
6. CommandExecution output 为 null / 非空 / 跨页边界，至少一个真实输出体积；
7. Summary 与 items/list 中重复的 user/final-agent **官方 item id 是否完全一致**（first-user 与
   final-agent **分别**比对）；
8. 同会话 full / Summary / items-list 的字节与 wall time，按 historyMode 分层；
9. **>30 回合真实会话的 turns/list 翻页全链**：逐页 cursor、翻到 EOF、turn id 无重叠
   （无缺口的判定见 §3.0.7 control inventory 对照）、desc 首页与后续页衔接。
   **2026-08-30 实测修订**：目标账号 150 线程（22 paginated + 128 legacy）中发现扫描
   24 线程（最近 8 + 全量跨步 16），最深仅 25 回合——**采样范围内不存在 >30 回合线程**，
   多页 turns 链无法在真实账号上行使。cursor 链机制（逐页衔接/重复检测/EOF/往返）由
   同机制的 items/list 多页链（16 页，attempt-010）+ 单页 turns EOF + 官方源码不变量
   （§1.5 锚点）覆盖；此为账号覆盖度限制的诚实记录，非协议缺口。G0 裁决确认。

`initialTurnsPage` 经 T0.6 实测后已由 owner 批准进入产品主路径（G0 裁决 #2；落实见 §5 非目标
修订与 `attachLiveThreadOn`）。其源码 round-trip 测试仍不得写成线上已 proven——线上 proven 的
边界以 T0.6 探针实测（1 RPC 921ms/78611B、`thread.turns==[]` 两次成立、单次 attach）为准。

#### 3.0.6 G0 脚本防误判约束

- **所有 `content`/`summary` 统计必须按 ThreadItem `type` 分组**：`content` 在 `userMessage` 与
  `reasoning` 上同名异义（schema replay 已实证全局计数会产生 0-vs-1 的假分歧）；
- **Summary↔items 去重必须输出 `turnId → summary item ids → full item ids` 映射**：first-user 与
  final-agent 分别验证，不得把两个槽位合并成集合后只看交集或只比文本/总数。

#### 3.0.7 G0 负结果判定（负结果 = 失败，不是"样本已采"）

以下任一发生，**G0 直接失败**，回到 owner 重新裁决 identity/merge/分页方案；**禁止**降级为
文本相似度去重或"继续实施并容错"：

1. **Summary↔items id 不一致**：first-user 或 final-agent 任一侧在 items/list 中找不到同 id
   （G0.7 断言失败即失败）；
2. Summary 出现 first-user / final-agent 以外的 item type；
3. `turnId` 过滤的 items/list 返回了其他 turn 的 item；
4. 分页往返产生重复 turn/item 或**缺页**；
5. 非法 turnId（格式合法但不存在）返回了**非空 items**（turn 过滤泄漏/编造）或返回 rpc 错误
   （**2026-08-30 修订**：原假设"应为错误"与官方源码不符——tag `rust-v0.150.0-alpha.12.2`
   `thread_processor.rs:3365-3425` 中 `turn_id` 直接作为 store 过滤器下推，错误映射只覆盖
   thread 级失败（InvalidRequest/Unsupported/ThreadNotFound），**不存在未知 turnId 报错路径**；
   线上实测（attempt-009）同样返回 200 + 空页。因此正确断言为：未知合法格式 turnId →
   `error == null` 且 `data` 为空；任一偏离（返回错误、返回非空 items）才是负结果）；
6. G0.9 的**无缺口判定**：分页拼接后的 turn-id 序列与同会话 `includeTurns=true`
   **control inventory**（仅探针使用，不得进产品路径）做**集合与顺序双重对照**，并完成
   backwards round-trip——跳过对照只能证明"无重复"，不能证明"无缺口"，视为未通过。
   **2026-08-30 实测修订**：paginated 线程上 `includeTurns=true` 经 WSS **冷/暖态均无响应**
   （240s 超时 ×3，attempt-009/010；同一传输上 legacy 线程 768ms 成功返回，证明是
   paginated 模式行为而非传输问题；与 installed `codex-cli 0.151.0-alpha.7.1` 的
   alpha 版本特征一致的嫌疑记录在 drift_assessment）。control 对照在该版本+该传输上
   **不可获得**，无缺口判定退位于链内证据：无重复 + cursor 链完整（逐页 requestCursor
   = 前页 nextCursor）+ EOF（nextCursor=nil）+ backwards 往返 + notLoaded 同一性 +
   官方不变量（§1.5 锚点）；control 对照保留定义，legacy 线程与未来版本可复测。

（`thread/items/list` 对 legacy 会话返回 method-not-found **不属于负结果**——它是 §2.5 能力矩阵的
合法探测目标，走 T0.5 owner 裁决。）

**2026-08-30 G0 实测补充注记（attempt-001~009，两次完整 live 运行）**：冷态（线程未加载）
`thread/read includeTurns=true` 在 paginated 线程上经 WSS **240 秒内无响应**（两次运行行为
一致；此时空闲看门狗与 RPC 超时的交互曾导致探针挂死，已修复并记录于探针代码注释）。
control inventory 改为**链路后暖态重试**（turns/items 分页链已把线程加载进 app-server），
冷态行为本身作为观察记录保留。该行为与 paginated 模式"turns 必须走 `turns/list`"的设计
假设一致，不构成负结果。

### Phase 1 — Mac agent：`agent/codex-remote`（门 G1）

- T1.1 `history.go` 拆分（**镜像官方不变量，见 §1.5 锚点表**）：
  - `ReadThreadSummary(ctx, threadID, cursor)` → `thread/read`（元数据）+ `thread/turns/list`
    （Summary，支持任意 cursor 翻页——它同时是 §2.4 producer 补水合的取页原语；行为对齐
    `paginated_thread_turns_list_response` 的默认值与三态语义）；
  - `ReadTurnItems(ctx, threadID, turnID)` → `thread/items/list` 循环拉到 EOF，**逐项镜像官方
    `paginated_turn_full_items`（`thread_processor.rs:3222-3258`）的不变量**：固定 turnId、
    `sortDirection=Asc`、最大页尺寸请求（limit 常量，接受服务端 clamp）、每页严格反序列化、
    `nextCursor=nil` 才算 EOF、**cursor 原地重复立即报错（防死循环）**；再叠加本项目 G0 裁决的
    maxPages/maxBytes/timeout；
  - 删除打开路径上的 `includeTurns=true`（保留为显式命名的兼容函数，**默认仅供 T0.2 探针/基线与
    owner 裁决范围使用；生产路径失败显式报错，禁止自动回退**）。
- T1.2 类型与页元数据：**保留/传播** `remoteTurn.ItemsView`（已存在，`history.go:81`）与页元数据
  （**upstream `nextCursor`/`backwardsCursor`、网络顺序、EOF 事实**）供 G2 正序化与 §2.4 producer
  状态使用；Summary 槽位映射为现有 `EventUserMessage`/`EventText` 语义时标注 "summary 槽位"来源，
  **且必须携带官方 item id 走现有 `text_delta.itemId` 通道**——agent→bridge 边界不得丢弃 id。
- T1.3 live 路径零改动；`catalog.go` 零改动。
- T1.4 单测（fixtures 驱动；**场景直接取自官方 `thread_read.rs` 测试基线**，§1.5）：Summary
  首页/翻页/空页/EOF、turnId 过滤、itemsView 透传、**十类 item 解码（fixture 下限 = schema replay
  已覆盖十类；proven 主张仅限 live 样本中出现的类型）**、Reasoning content 为空数组的行为、
  repeated-cursor guard、SkippedTypes 计数可观测（仅诊断字段）。
- T1.5 **Reasoning detail 映射按 G0.5 裁决实现**：不得复用 `history.go:345-351` 的 summary-first
  规则；四态样本驱动单测（样本缺失的 case 标 unverified 且不宣称 proven）。
- **G1**：`go test ./agent/codex-remote/` 全绿（含 race）；形状断言全部来自 G0 fixture；
  **未知 item 原子失败规则（§2.2）已在 decoder/mapper 层实现并有测试**。

### Phase 2 — go-bridge：投影、窗口接线、专用明细 merge 与桥面 RPC（门 G2）

#### 3.2.0 wire contract 先行（冻结后才能写 handler）

在写任何 handler 前，先在 `docs/protocol/unified-bridge-protocol.md` 冻结（窗口链语义按
`docs/protocol/bridge-v1.md` 现行契约执行，不允许时先修契约，见 T2.0）：

- **方法归属**：`session_turn_items` 仅注册于 `backendId=codex-remote`，其他 backend 调用
  fail-closed 返回 unsupported；幂等/singleflight 键 = `(sessionId, turnId)`；
- **请求/响应**：请求 `{ sessionId, turnId }`；响应**成功形状 ack**：
  `{ detailLoadState: "loaded"|"failed", syncRev, reasonCode? }`——**canonical items 不进 result**，
  投影 snapshot/patch 是唯一内容写者。**失败 ack 形状定案**：上游 RPC 错误、资源门超限、未知 item、
  中断等**过程性失败走 success-shaped failed ack**（携带该失败 commit 的 `syncRev` 与
  `reasonCode`）；仅**请求级错误**（未知 backend/session/turn、malformed 请求）返回 WireError；
- **状态机与顺序无关完成条件**：
  1. 请求受理后，Mac 先把该 turn 的 `detailLoadState=loading` 提交进投影 SoT（singleflight
     follower 观察同一状态，不发第二次拉取）；
  2. 成功时在**同一 Kernel transaction** 提交 `replace_parts + loaded`，得到 `syncRev=N`；
     ack 只在该 commit 成功后返回；
  3. iOS 完成条件 = replica `appliedRev >= N`，patch 先于/后于 ack 均可；**不得从 RPC result
     渲染内容**；断线后只收到 snapshot 也能恢复（`detailLoadState` 随 snapshot 下发）；
  4. 失败时提交 `failed + reasonCode`（同样得到 commit syncRev），再返回 failed ack；
     重试迁移 `failed → loading`；
  5. 会话删除、archive、同 turn generation 改变时，旧请求由 fence 丢弃或返回 typed stale；
- **singleflight follower 终态**：leader 处于 loading 时，follower **等待同一 terminal commit**
  并返回**相同 terminal syncRev**（loaded 或 failed）；**不得**返回中间 loading ack；
- **orphan loading 恢复（pathless 裁决三分，2026-08-30 复核修正——当前产品不要求真实
  crash/restore 覆盖）**：
  1. **当前拓扑 N/A（restart restore）**：go-bridge 现无完整 Projection checkpoint（仅
     CodexProducerState side-file），重启后投影由上游重新 Summary hydrate 重建，
     `detailLoadState` 随之回到 `notRequested`——不存在可恢复的持久 loading 态；
  2. **当前必须覆盖（进程内 orphan recovery）**：loading commit 后 leader 消失（请求取消、
     连接断开、fetch 异常退出）而 bridge 存活时，下一次请求路径发现 loading 且无 in-flight
     leader，必须**先**原子提交 `failed(reasonCode=interrupted)` 再重试
     （`recoverOrphanLoadingTurn`），不得永久停在 loading；
  3. **future hook（checkpoint 准入门槛）**：未来引入完整 Projection checkpoint（重启
     restore）后，restore 扫描必须把无 active leader 的 orphan loading 原子恢复为
     `failed(interrupted)`——`RecoverOrphanDetailLoading` 及其单测已冻结为该准入钩子；
- **`turnStateOps` 专用 patch op（r6 定案，替代 r5 的 sparse upsertTurns——核验 §4.1）**：
  - **弃用理由**：现有 Go `TurnProjection.Status` 必填（`projection_types.go:96-105`）、Swift
    `SessionTurnProjection.status` 非 optional（`SessionProjection.swift:226-236`），三字段 sparse
    JSON 无法被完整 Turn schema 解码；若改发完整 turn upsert，`ProjectionReplica` 会把
    `upsertTurns != nil` 标 `orderChanged=true`、`merged(with:)` 按完整 turn 合并——触碰共享
    merge/顺序语义，违反 live 零改动红线；
  - **定案形状**：`ProjectionPatch` 新增 `turnStateOps: [ { turnId, detailLoadState, reasonCode?,
    generation } ]`（与 `partOps` 对称的专用 op；Go/Swift 显式应用，不伪装完整 turn upsert）；
  - **状态字段不变量**：`failed` op 的 `reasonCode` **必填且非空**；`loading` / `loaded` op 应用时
    **无条件清除**该 turn 旧 `reasonCode`（不是“字段缺席就保留旧值”）；`notRequested` 不通过普通
    网络 op 回写，只是旧 snapshot / 缺席字段的解码默认；任何非法 state/reasonCode 组合 fail-closed；
  - **应用顺序冻结**：iOS patch 应用顺序改为 `execution → upsertTurns → turnStateOps → partOps`
    （状态先行、parts 后至，同 patch 内原子）；`replace_parts + loaded` 同 patch：partOps 替换
    明细、turnStateOps 置终态；`loading`/`failed` 单独以 turnStateOps patch 提交；
  - **change-set 可见性**：`ProjectionReplica.changedTurnIDs(from:)` 必须 union
    `turnStateOps.map(\.turnId)`；state-only loading/failed patch 产生 `orderChanged=false` 且
    `changedTurns` 含目标 turn；同 patch 的 `turnStateOps + replace_parts` 对同一 turn 只产生一个
    changedTurnID，确保状态写入 replica 后展示层必定刷新、又不触发顺序重排；
  - **向后兼容**：旧 patch 无 `turnStateOps` 字段 → 忽略；旧 snapshot turn 缺 detailLoadState →
    `notRequested`；bridge 旧版本发出的 patch 不含该字段，iOS 行为不变（live 零改动）；
  - schema bump 与 Go/Swift 类型、解码测试同批冻结；
- **stale-write fence（per-turn generation）**：投影 turn 新增**持久化 per-turn generation**
  （completion 后任何 mutation bump；随 snapshot 持久化，schema bump 同步）；`replace_parts`
  admission token = `(backendId, sessionId, turnId, turnGeneration)`；提交前验证目标仍是同一
  completed turn；变化 → typed stale 并保留新 truth。**不用全局 baseRev**；测试证明目标 turn
  改变 → stale、其他 turn 更新 → 不受影响；
- **detailLoadState schema 层级**：**turn-level 字段**（挂在投影 turn 对象上，Go/Swift schema
  同步）；缺席解码为 `notRequested`；restore 向后兼容；
- **delivery mode 注册**：bridge 为每个 `(conn, backend, session)` 记录 **window / full** delivery
  mode（窗口 RPC 首次命中时登记，非 capability 声明）；历史补水合 commit 按 §2.4 三类规则分发；
  契约文字（bridge-v1.md R3/R4/R10）如不兼容先修订；
- **分页所有权**：**Mac 拉到 EOF 后原子提交**；iOS 不见 cursor、无 partial 状态；
- **资源门**：maxPages / maxBytes / timeout 用 G0 裁决值；超限 → failed ack + 原因码，
  不截断合法历史、不以 placeholder 代替超大 tool output；
- **fence**：live turn 未落库、archive/delete、重连窗口与 detail 加载并发时的裁决顺序（投影 SoT
  串行化）；
- **错误语义**：过程性失败 → success-shaped failed ack；请求级错误 → WireError；
  空明细（§2.2 口径）→ `loaded`。

#### 实施任务

- T2.0 **上游分页 ↔ 窗口接线（先于其余 Phase 2 任务设计冻结）**：
  - **契约复核**：确认 bridge-v1.md 现行 `projection_window_v1` 契约（snapshot cut、admission、
    bridge-owned cursor、R2 producer 边界、**R3/R4/R10 单 funnel 与 patch 语义**）允许
    producer-on-demand 扩展 Kernel 与 §2.4 按连接投递；不允许则先修订 canonical 契约与测试再实施；
  - **producer admission 扩展**：`older` 请求且 Kernel 不足时，按 §2.4 语义以内部 upstream cursor
    经 `ReadThreadSummary` 补一页（有界）→ desc→asc 反转 + inclusive 去重 + prepend 进 Kernel
    truth（同一 snapshot fence / syncRev 链）→ 按 delivery mode 分发 → 返回诚实
    `hasOlder/nextOlderCursor`；
  - **per-connection delivery**：实现 `(conn, backend, session)` delivery mode 注册表 + 三类分发
    规则 + connection-specific no-op revision patch（复用现有 `projection_patch` shape 与 ordered
    sink，非第二条 pipe）；
  - **backend-private producer checkpoint**：字段定义、schema version bump（v10 → v11+）、
    clone/save/restore/validation；pathless codex-remote 的 cursor 有效性检查（restart 后先以
    target RPC 验证/续接，错误进 typed recovery）；恢复任务有界（maxPages/maxBytes/timeout +
    持久化 continuation，超限显式 retryable failure）；
  - **测试矩阵**：>30 回合 fixture 的 `window_0 → older → … → EOF` 可达性（第 31 回合以前可达、
    turn id 无重叠/无缺口）；hasOlder 诚实性；wire cursor 不含 upstream artifact；多 iPhone 并发
    older；live tail 同时推进时 syncRev 链不断；bridge restart/restore 后继续 older；upstream
    cursor stale → typed error + 恢复路径；**A 请求 older / B 停在首屏 / C full projection 三类
    连接各自内容与 revision 均正确；B 随后收 live turn 无 base mismatch/全量 recovery**。
    **2026-08-30 owner 裁决修正**：G0 live 采集证明目标账号**不存在** >30 回合线程
    （24/150 采样最深 25，attempt-010），故本矩阵的 ">30 回合 fixture" **不得引用
    attempt-010 或任何 live fixture 冒充**；到 G2 前必须通过**真实 app-server 测试环境生成
    确定性 >30 回合 fixture**（本地 app-server + 脚本造回合），或**复用官方分页测试基线**
    （`thread_read.rs` 分页/EOF 用例形状）。此修正不阻塞 Phase 1 API 拆分。
- T2.1 投影消费 Summary：历史路径改为消费 agent 的 Summary 历史；**desc 网络页先反转为
  oldest→newest 再入 reducer；加载更旧页只 prepend；turn id 去重；inclusive backwards cursor
  有 fixture 测试**。
  **Canonical item id 持久化**：把 `ProjectionPart.ItemID` 从 tool-only 扩展为**所有已映射且
  schema 支持的官方 item variant**（text/reasoning 等）——未知/未映射 variant 按 §2.2 原子失败
  （非静默丢弃）；同步 Go schema 注释与 Swift 端 `SessionProjectionPart.itemId`（iOS 字段已存在，
  `makeText/makeReasoning` 默认写 nil 需接通）；snapshot、patch、restore 全链路保留。
  新增测试：Summary snapshot 中 final-agent part 的 itemId 非空；重连 restore 后 detail merge
  仍按同一 id 去重。
- T2.2 **专用历史明细 merge**（不复用 live reducer 生命周期语义，不改共享 FlushPatch）：
  - 输入：`turnId + 有序全量 items + 终态 detailLoadState + admission token(per-turn generation)`；
  - 提交通道：**现有 wire 的 `replace_parts`（显式 `turnId, messageId`）+ 同 patch 的
    `turnStateOps` 置终态**（§3.2.0 定案形状）；**不把历史工具塞进 `ps.tools`、不修改 live tool
    FlushPatch**（若未来确需重构 FlushPatch，必须另立方案并扩充全部 backend 的 live 回归矩阵，
    不在本方案内）；
  - **未知/无法无损映射的 item type/shape → 整回合原子失败**（中止 commit、保留 Summary、
    `failed + unsupported_item_type`；不执行部分 replace_parts、不标 loaded）；
  - 按**官方 item id** 构造/替换该回合 assistant parts（依赖 T2.1 持久化的 id），不伪装
    `reasoning_delta/text_delta`、不 `markRunning`、不改变 `ExecutionView`；
  - 去重：Summary 已有的 user/final-agent 按 canonical item id 对齐（G0.7 证据 + §3.0.6 映射脚本），
    不重复 append；
  - Reasoning 映射按 G0.5 裁决（§2.2），禁止 summary-first；
  - 可复用 item decoder；SkippedTypes 仅作诊断观测。
- T2.3 桥面方法实现（按 §3.2.0 冻结稿）；管理 API/relay 测试复用 `fakeAgentSession` 模式。
- T2.4 协议文档更新与 handler 同提交入库（含 T2.0 若引发的 bridge-v1.md 契约修订、
  `turnStateOps` 的 unified-bridge-protocol.md/schema/Go/Swift 类型同步）。
- T2.5 **并发与归属测试矩阵**：已完成回合、非末回合、当前另有 live turn、重复请求（幂等）、
  两 turn 并发展开（singleflight）、空明细（§2.2 口径）、冷水合与 live 输出同时进行、Summary
  返回瞬间 live turn 完成、detail 加载时新 turn 开始、replace_parts 重复提交幂等、重连 restore
  后 detail merge 去重、generation 改变/会话删除时的 typed stale、older 补水合与 detail 加载
  并发、**A/B/C 三连接投递正确性与 B 的 live baseRev 链**（T2.0）、**进程内 leader 消失 →
  orphan loading 恢复（下次请求先原子 failed(interrupted) 再重试，不永久 loading；restart
  restore 属 future hook，当前无完整 Projection checkpoint，N/A）**、**leader/follower 断线与取消不互相取消
  authoritative fetch**、**unknown item 失败后 Summary 不变、修复后重试只提交一次完整 parts**、
  **turnStateOps 向后兼容（旧 bridge patch 无该字段时 iOS 行为不变）**、**state-only loading/failed
  change-set 含目标 turn 且 orderChanged=false**、**同 turn 的 state+parts 只报告一个 changedTurnID**、
  **failed→loading→loaded 显式清除旧 reasonCode，非法组合 fail-closed**。
- **G2**：`go test ./go-bridge/` 全绿 + **detail merge、窗口补水合、per-connection delivery 与投影
  并发路径的定向 race 测试通过**（不笼统主张全包 race 已证明）；桥面协议文档变更入同一提交。

### Phase 3 — iOS：懒加载 UX（门 G3）

- T3.1 **复用现有窗口机器（已是生产路径，不重造）**：会话页按 Summary items 渲染首屏；滚动到顶
  继续走现有 `projection_window_v1` load-older 链（`pullWindow` 消费 `nextOlderCursor`、coverage
  ledger、视口锚定、cursor-stale 恢复）——本阶段只验证该链在 producer 补水合 + per-connection
  delivery 下的行为，不改其协议面。
- T3.2 所有可分页已完成回合统一"加载详细过程"入口：点击 → `session_turn_items` →
  **以 replica `appliedRev >= syncRev` 为完成条件**从投影 patch 渲染明细（顺序无关；
  failed ack 同样以 syncRev 等待后呈现失败态；断线后经 snapshot 恢复 `detailLoadState`）；
  按 `detailLoadState` 呈现 loading/loaded（含 §2.2 口径的"无详细过程"文案）/failed 可重试；
  `loaded` 后入口变为直接展开/收起，不再发请求（防重复拉取）。
- T3.3 live 回合不受影响；断线重连后已加载明细不丢（投影 SoT 在 Mac，`detailLoadState` 与
  canonical itemId 随 snapshot 恢复）。
- T3.4 单测（ChatViewModel/projection store，含 `turnStateOps` 解码与应用顺序、state-only patch
  驱动 UI 刷新、changedTurnIDs/orderChanged、failed→loading→loaded 清除 reasonCode）+ 真机构建安装
  （`scripts/run.sh device`，遵循 REAL_DEVICE_DEBUGGING.md）。
- **G3**：真机回归 + owner 验收矩阵（§4）。

### Phase 4 — 回归与交付（门 G4）

- 全量定向测试（两仓）、Release 构建 + 覆盖安装（BUILD_INSTALL_AND_RUNTIME.md 交付条件）、
  真机矩阵全绿、CHANGELOG/Owner 手册同步、G0 基线复核（打开会话字节/TTI 对比数据入交付说明）。

## 4. Owner 真机验收矩阵

| # | 步骤 | 期望 |
| --- | --- | --- |
| 1 | iPhone 打开一个长历史 Codex Desktop 会话 | 首屏可交互时间明显缩短（对照 G0 基线）；最近回合正常显示 |
| 2 | 点开某回合"加载详细过程" | 展示该回合服务端实际提供的思考摘要/完整思考（按 G0.5 裁决口径）/工具/命令/文件变更等明细 |
| 3 | 点开一个无工具纯对话回合 | 明细加载完成，按 §2.2 口径显示"无详细过程"或仅思考摘要——**不得报错** |
| 4 | 再次点开同一回合 | 即时展开，无网络拉取（detailLoadState=loaded） |
| 5 | Mac 端同会话发起新回合 | iOS live 同步照旧（live 零回归） |
| 6 | 断网/代理切换后重连，重复 1-4、8 | 行为一致；无重复拉取、无风暴日志；执行状态不被误标 running；已加载明细不丢 |
| 7 | 加载明细同时 Mac 端开始新回合 | 明细与新 turn 互不串扰（fence 生效） |
| 8 | **>30 回合会话滚动到顶继续加载更早历史** | 第 31 回合以前的历史可达；连续 older 直到会话起点；无重复/无缺口；与 live 推进互不干扰（§2.4 窗口链）。**2026-08-30 修正：owner 账号无 >30 回合线程（G0 实测），本行真机验收以测试环境生成的长会话进行，或降级为窗口链单测 + owner 接受** |
| 9 | **两台 iPhone 同会话，A 滚动加载旧历史，B 停在首屏不动** | B 列表顺序/内容不变、无旧回合插入、后续 live 消息照常到达且无 base mismatch（§2.4 per-connection delivery） |
| 10 | （如适用）legacy 会话打开与展开 | 打开速度受益照常；明细展开行为符合 T0.5 owner 裁决（含明确不支持的情形），无静默 full fallback |

（矩阵 #3 覆盖"空明细也是 loaded"；#6 覆盖"不重复正文/不误标执行中/restore 保留"红线；
#8/#9 覆盖 P0-r3-1 与 P0-r4-1 端到端；#10 覆盖 §2.5 能力矩阵。）

## 5. 非目标

- 中继 cursor 断线续传、出站 seq/ack 重放缓冲：既有 fail-closed 缺口，维持不动。
- `codex-web` 后端任何改动。
- ~~`initialTurnsPage` 进产品主路径（T0.6 三条件裁决前保持可选观察）~~
  **2026-08-30 owner 裁决后修订（P1-3 落实）**：T0.6 三条件齐备，owner 已批准启用
  （G0 裁决 #2，约束：仅已验证 paginated 版本、每连接/session 只 attach 一次、未验证版本
  预先选择 baseline，不得先失败再静默 full-read）。产品路径已落实：
  `thread/resume(excludeTurns:true + initialTurnsPage{limit:30, desc, summary})` 于
  唯一 attach 点携带，响应断言 `thread.turns == []`（违例触发 per-process breaker 回退
  官方 baseline，非静默 full-read）；缓存页仅服务 `historyMode=paginated` 的
  `ReadColdHistory`，legacy/unknown/无缓存一律预先选择官方 metadata + turns/list
  baseline（`agent/codex-remote/session.go attachLiveThreadOn` +
  `history_paginated.go ReadColdHistory`）。非目标中保留的是"未经重新裁决扩大到
  未验证版本"。
- 逐页渐进渲染（见 §9-R2 搁置理由）。
- 重构共享 live tool FlushPatch（如确需，另立方案并扩充全部 backend 的 live 回归矩阵）。
- **新增第二套 iOS 历史分页 RPC**（更早历史只走现有 `projection_window_v1` older 链）。
- 未知 item 的静默跳过/部分渲染（按 §2.2 一律整回合原子失败）。
- **重新发明 codex-rs 已有分页语义**（"取什么/怎么分页/何时 EOF"镜像官方实现，§1.5）。
- 复刻 ChatGPT iOS 私有客户端实现（仅实现同类交互；iOS 行为依据是 owner 观察 + 本方案设计，
  非 iOS 源码）。

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
8. **live 隔离红线**：不修改共享 `FlushPatch`/`ps.tools` live 热路径；历史明细只经显式
   `(turnId, messageId)` 的专用原子 op（`replace_parts` 等价）提交。
9. **单一内容写者**：`session_turn_items` result 只做 ack；canonical items 只经投影
   snapshot/patch 下发。
10. **窗口链红线**：upstream cursor 绝不过桥/不嵌入 wire cursor；`hasOlder` 必须诚实（未水合 ≠
    无更旧）；producer 补水合必须先归约进 Kernel 再切窗；**历史补水合 commit 按 delivery mode
    分发，不得全量广播旧回合**；契约不允许时先修 bridge-v1.md 再写代码。
11. **回退红线**：生产环境不得自动回退 `includeTurns=true`；仅 owner 显式裁决的 historyMode/版本
    范围可走旧路径，且须记录可移除条件；新 RPC 超时/形状错误一律显式报错。
12. **未知 item 红线**：无法无损映射的 item → 整回合原子失败（保留 Summary、
    `failed + unsupported_item_type`）；SkippedTypes 仅诊断，不作为丢弃后继续成功的开关。
13. **官方复用红线（r6 新增）**："取什么、怎么分页、何时 EOF"等上游语义**直接镜像 codex-rs 现成
    实现（§1.5 锚点表），不自行发明等价逻辑**；本项目只实现桥接职责（投影 SoT、revision、
    connection delivery、iOS 状态与交互）。镜像实现的偏差（如新增 clamp/资源门）必须在代码注释与
    测试中标注与官方锚点的差异点。

## 8. 交付物清单

- [ ] `agent/codex-remote/testdata/phase0/live/`：turns/list（**多页 turns 全链 fixture 按
      2026-08-30 owner 裁决由 app-server 测试环境生成或复用官方分页测试基线——live 采集已证明
      账号无 >30 回合线程；control inventory 对照以"不可获得（paginated 经 WSS 240s×3）+
      legacy 对照 + 链内证据"的替代记录形式交付**）、items/list（paginated 必测；legacy 按
      target inventory 实测或附证据 N/A）、thread/read(含 historyMode)
      脱敏 fixtures + 分层体积/耗时基线 + 单回合资源画像 + 资源门裁决值 + §3.0.7 负结果判定记录 +
      T0.6 `excludeTurns=true + initialTurnsPage` 候选裁决记录（含 `thread.turns == []` 断言）+
      legacy 裁决或 inventory-backed N/A 记录
- [ ] `agent/codex-remote/history.go` 拆分（**镜像 `paginated_turn_full_items` 六项不变量**）+
      兼容路径显式化（默认仅探针/裁决范围）+ 页元数据与 item id 传递保留 + Reasoning 映射按
      G0.5 裁决重写（非 summary-first）+ 未知 item 原子失败
- [ ] `go-bridge` 投影 Summary 化（正序化/prepend/去重）+ **上游分页 ↔ `projection_window_v1`
      接线（T2.0：producer 补水合、per-connection delivery、诚实 hasOlder、backend-private
      checkpoint 与有界恢复）** + canonical `ProjectionPart.ItemID` 已映射 variant 持久化 +
      **per-turn generation** stale fence + **`turnStateOps` patch op（Go/Swift schema 同步、
      changedTurnIDs/change-set、reasonCode 清除不变量）** +
      专用历史明细 merge（`replace_parts` 通道 + 未知 item 原子失败）+ 并发归属测试矩阵
- [ ] `docs/protocol/unified-bridge-protocol.md`：`session_turn_items` wire contract
      （ack-only、backend 归属、状态机/syncRev 完成条件、`turnStateOps`、delivery mode、资源门
      ——先冻结后实现）；如 T2.0 契约复核需要，同步修订 `bridge-v1.md`（R2/R3/R4/R10）
- [ ] iOS 统一入口 + detailLoadState 状态机（`turnStateOps` 解码/顺序）+ appliedRev≥syncRev 完成
      条件渲染 + 现有窗口链 load-older 验证（不重造）+ 防重复拉取
- [ ] 两仓测试全绿、真机矩阵（§4，含 #8/#9/#10）全绿、文档同步

## 9. 评审采纳记录（四轮评审 + 合规核验 + 最终定向评审，含不采纳项理由）

### r1 轮（对 [r1 评审](2026-08-30-codex-remote-lazy-history-plan-audit.md)）

| 编号 | 建议 | 处置 | 理由 |
| --- | --- | --- | --- |
| — | P0-1～P0-6、P1-1/P1-3/P1-4、P2 全部 | **采纳** | 落点见 r2 稿；后续复评确认关闭 |
| R1 | P0-2 方案 B：per-turn detail probe（先探针判断是否有明细） | **不采纳** | 每回合一次探针 RPC 会重新引入本方案要消除的请求重量（N 个回合 = N 次探针）；且评审自己标注"真实、便宜且有样本证明"为前提，当前无证据满足。方案 A（统一入口 + `detailLoadState`，空明细展示"无详细过程"）语义等价且零额外请求 |
| R2 | 逐页渐进渲染 | **搁置（部分采纳）** | 主路径定为 Mac 整回合拉 EOF 原子提交：半页状态与投影原子性冲突、partial 语义复杂。超长回合的处理走资源门（G0 裁决 maxPages/maxBytes/timeout，超限 fail-closed + owner 裁决），而不是预先设计 cursor 下放的渐进扩展；当前无数据支撑 |

### r2 轮（对 [r2 复评](2026-08-30-codex-remote-lazy-history-plan-audit-r2.md)）

| 编号 | 建议 | 处置 | 理由 |
| --- | --- | --- | --- |
| — | P0-r2-1/2/3、P1-1～P1-6、P2-1～P2-4 | **全部采纳** | audit-r3 §2 复核确认全部 🟢 关闭；两处二选一均选复评建议分支（P1-3 owner 裁决删主张；P0-r2-3 专用 replace_parts 而非重构 FlushPatch） |
| （重分类） | r2 稿 §9-R3 "initialTurnsPage 进产品路径不采纳" | **改为"维持评审结论"** | r1 评审本就裁定 initialTurnsPage 可选且不阻塞；属确认而非否决，不应列在"不采纳"下 |

### r3 轮（对 [r3 复评](2026-08-30-codex-remote-lazy-history-plan-audit-r3.md)）

| 编号 | 建议 | 处置 | 理由 |
| --- | --- | --- | --- |
| — | P0-r3-1、P1-r3-1/2/3、P2-r3-1/2 | **全部采纳** | audit-r4 §2 复核全部 🟢；落点为 §2.4 + T2.0、§3.2.0 状态机与 fence、回退红线、mapped variants、detailLoadState 层级 |
| R4 | P0-r3-1 的范围替代项："仅首 30 回合为本版范围"（更早历史不可访问，由 owner 接受为产品回归） | **不采纳** | audit-r3 自己明确不建议该裁决：(a) 把"懒加载"实施成"历史截断"，是对现有 full-read 行为的产品回归；(b) iOS 窗口机器已是生产路径，截断版不减少 iOS 侧工作；(c) 剩余成本集中在 Mac producer admission 一层，与"历史永久丢失"的代价不成比例。若 owner 在 G0 数据出来后仍要求缩减，须作为显式产品回归记录并重新过评审 |

### r4 轮（对 [r4 终审](2026-08-30-codex-remote-lazy-history-plan-audit-r4.md)）

| 编号 | 建议 | 处置 | 理由 |
| --- | --- | --- | --- |
| — | P0-r4-1/2/3、P1-r4-1～4 | **全部采纳**（r5 合规核验 7/7 ✅） | 落点见 §0.1 r5 轮 |
| （定案记录 1） | P1-r4-1-2 失败 ack 形状二选一 | **选 success-shaped failed ack** | 与 ack-only 单写者设计自洽：failed commit 本身产生 syncRev，failed ack 直接携带它，iOS 用同一 `appliedRev >= syncRev` 条件处理成败两态；WireError 路径要求 iOS 另行获取失败 commit 的 syncRev，徒增协议复杂度。WireError 仅保留给请求级错误 |
| （定案记录 2） | P1-r4-2 stale token 算法二选一 | **选持久化 per-turn generation**（备选：completed-turn fingerprint） | generation 是 O(1) 整数比较，schema bump 与 detailLoadState/per-turn 持久化工作顺路；fingerprint 需定义 canonical 序列化与哈希、比较成本高且对"回合被修正"的语义间接。两条路线都满足 audit 的"目标 turn 改变→stale、其他 turn 更新→不受影响"测试要求，取实现更简单者 |

### r5 合规核验轮（对 [r5 合规与官方源码可行性核验](2026-08-30-codex-remote-lazy-history-r5-compliance-source-check.md)）

| 编号 | 建议 | 处置 | 理由 |
| --- | --- | --- | --- |
| — | P0（sparse upsertTurns 修正）、P1（证据边界措辞）、官方复用清单、initialTurnsPage 候选、legacy 分层矩阵、完整思考承诺措辞 | **全部采纳** | 落点见 §0.1 r6 表 |
| （定案记录 3） | sparse `upsertTurns` 修正二选一（完整 turn upsert vs 专用 turn-state op） | **选新增专用 `turnStateOps` patch op** | 方案 A（发完整 turn upsert）会被现有 `ProjectionReplica` 标 `orderChanged=true`、经 `merged(with:)` 完整合并——触碰共享 merge/顺序语义，恰好违反本方案 live 零改动红线（§7-8）；方案 B 与既有 `partOps`/`replace_parts` 的专用 op 风格对称，向后兼容解码简单（旧 patch 无字段即忽略），不动任何共享 live 路径。核验自己也标注 B 为"更干净方案" |

### r6 最终定向评审（对 [最终评审](2026-08-30-codex-remote-lazy-history-plan-final-audit-r6.md)）

| 编号 | 建议 | 处置 | 落点 |
| --- | --- | --- | --- |
| P0-final-1 | initialTurnsPage 候选补 `excludeTurns:true` + 空 turns 断言 | **采纳** | §1.2/§1.5/T0.6/交付清单 |
| P1-final-1 | turnStateOps 进入 changedTurnIDs/change set | **采纳** | §3.2.0/T2.5/T3.4 |
| P1-final-2 | reasonCode failed 必填、loading/loaded 清除 | **采纳** | §3.2.0/T2.5/T3.4 |
| P2-final-1 | 无 legacy 时允许 inventory-backed N/A | **采纳** | §2.5/T0.1/T0.5/G0.4/交付清单 |

**评审收敛声明**：audit-r4 为设计阶段终审，r5 合规核验与 r6 最终定向评审均已闭环；本稿后不再进行
开放式文档评审，后续新问题仅由 G0 fixture、定向测试或真机证据触发并在对应 gate 内修复。

## 10. 变更历史

- 2026-08-30 r1：初稿。
- 2026-08-30 r2：按 r1 评审修订——historyMode 分层、统一入口 + detailLoadState、专用历史明细 merge、
  wire contract 先冻结、desc 正序化/prepend/去重、十类 item 回归面、G0 样本清单、并发 fence 矩阵、
  §6 已修复现状、P2 编辑修正。
- 2026-08-30 r3：按 r2 复评修订——canonical item id 持久化、Reasoning 四态裁决禁 summary-first、
  `replace_parts` 原子通道不改共享 FlushPatch、ack-only 单写者 + backend 归属 + 资源门、"空明细"
  口径、G0.5 阻塞语义、tag 行号重算、脚本防误判约束。
- 2026-08-30 r4：按 r3 复评修订——§2.4 + T2.0 上游分页 ↔ 窗口端到端接线、G0 增 >30 回合翻页
  fixture、§3.2.0 状态机与 stale fence、生产禁自动回退、mapped variants 收窄、detailLoadState
  层级冻结。
- 2026-08-30 r5：按 r4 终审修订——按连接投递与 revision 语义、G0 负结果判定（§3.0.7）、未知 item
  整回合原子失败、detailLoadState sparse upsertTurns patch op、失败 ack 形状/follower 终态/orphan
  loading 恢复、per-turn generation、producer checkpoint 有界恢复、多客户端/crash/重试测试。
- 2026-08-30 r6：按 r5 合规与官方源码可行性核验修订——**sparse `upsertTurns` 改为专用
  `turnStateOps` patch op**（现有完整 Turn schema 无法解码三字段 sparse JSON；定案理由见
  §9-r6 定案 3；应用顺序/向后兼容/schema bump 冻结）；**§1.5 官方复用锚点表 + 红线 §7-13**
  （"取什么/怎么分页/何时 EOF"直接镜像 `paginated_turn_full_items` 等官方实现，不造轮子）；
  **T0.6 `initialTurnsPage` 候选路径实测**（三条件裁决）；**§2.5 historyMode 能力矩阵**（legacy
  items/list 独立必测，Unsupported=合法探测目标）；**证据边界拆分**（iOS UI=owner 黑盒观察，
  codex-rs 只证明 app-server 能力）；产品承诺收紧为"服务端实际提供的明细"，G0 前不承诺完整思维链；
  G0 增 legacy items/list 分组探测、真机矩阵增 #10。本轮无整体不采纳项，定案记录 3 见 §9。
- 2026-08-30 r6-final：按最终定向评审修订（不新增版本轮次）——`initialTurnsPage` 候选强制
  `excludeTurns:true` 并断言 `thread.turns == []`；`turnStateOps` 纳入 changedTurnIDs/change set，
  冻结 reasonCode 赋值/清除不变量；legacy 改为 target inventory 驱动的必测或有证据 N/A。四项全部
  采纳，设计评审到此结束，下一步直接执行 G0。
