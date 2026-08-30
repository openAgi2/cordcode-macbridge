# codex-remote 懒加载会话历史实施方案 r3 复评报告

- 复评日期：2026-08-30
- 目标文档：`docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md`（r3，396 行）
- 上轮报告：`docs/2026-08-30-codex-remote-lazy-history-plan-audit-r2.md`
- 方法：`audit-plan` 样本纪律 + 目标 tag 源码复核 + 当前 Mac/iOS projection-window 实现核对
- 结论：**🟡 r3 已完整关闭 r2 的三项 P0 与其余 P1/P2；Phase 0 仍可执行。但本轮发现一个新的端到端 P0：首个 30 回合之后的上游分页没有接入现有 `projection_window_v1`，按当前文字实施会让更早历史永久不可达。修订该点前不得进入 G1。**

## 1. 核心结论

r3 对 r2 的响应是实质性的，不是措辞修补：canonical item id、Reasoning 四态裁决、历史
`replace_parts` 与 live `FlushPatch` 隔离、ack-only 单写者、backend fail-closed、资源门、空明细口径
和 tag 行号均已落到具体任务与测试。r2 的三项 P0 可以关闭。

新的阻塞来自两个已实现分页层之间的断点：

1. r3 的 Mac agent 只取 `thread/turns/list` 最近 30 回合，并保留上游 cursor；
2. iOS 已通过 `projection_window_v1` 在滚动到顶时请求 `older`；
3. 但当前 Mac `get_session_projection_window` 只从已提交的 `proj.Turns` 切片，不会用 agent 保存的
   上游 cursor 补水合更早回合。

因此，如果 r3 仅把第一页写入 Kernel，窗口响应会把这 30 回合误当作 Kernel 全集，返回
`hasOlder=false`；第 31 回合以前的历史从 iOS 永久不可达。这不是可留到实现时自行决定的细节，
而是会把“全量历史改成懒加载”实施成“历史截断”的产品回归。

正确方向不是再造第二套 iOS 历史分页 RPC，而是先冻结现有窗口协议与上游分页的衔接：iOS 继续只看
bridge-owned cursor；Mac producer 在处理 `older` 前按内部 upstream cursor 把所需 Summary 页归约进
Kernel，再由既有 snapshot fence 和 `projection_window_v1` 返回窗口。若设计认为现有窗口契约不允许
producer-on-demand 扩展 Kernel，则必须先修订该 canonical 契约及测试；不能把 upstream cursor 暴露给
iOS，也不能将“未加载”当“会话起点”。

## 2. r2 问题落实复核

| r2 问题 | r3 落点 | 复评 |
| --- | --- | --- |
| P0-r2-1 Summary item id 丢失 | T1.2、T2.1：`ProjectionPart.ItemID` 扩到 mapped variants，snapshot/patch/restore 保留 | 🟢 已落实 |
| P0-r2-2 Reasoning summary-first | G0.5 四态 + owner 裁决；T1.5/T2.2 禁止沿用 summary-first | 🟢 已落实 |
| P0-r2-3 历史 merge 修改 live FlushPatch | T2.2 改用显式 turn/message 的 `replace_parts`，不进 `ps.tools` | 🟢 已落实 |
| P1-1 backend 维度 | `session_turn_items` 仅接受 `backendId=codex-remote` | 🟢 已落实 |
| P1-2 单一内容写者 | RPC ack-only，items 只经 projection snapshot/patch | 🟢 已落实 |
| P1-3 G0.5 pending 矛盾 | 无 content 时须 owner 删除产品主张；未裁决不通过 G0 | 🟢 已落实 |
| P1-4 单回合资源门 | T0.2 画像 + G0 裁决 maxPages/maxBytes/timeout | 🟢 已落实 |
| P1-5 空明细定义 | 去除 Summary 重复槽位后无 detail item | 🟢 已落实 |
| P1-6 paginated/legacy 措辞 | 预计算与 slow-path 主张均限定 paginated | 🟢 已落实 |
| P2-1～P2-4 | tag 行号、ItemsView 现状、limit、R3 分类均修正 | 🟢 已落实 |

## 3. 本轮实现交叉核对

### 3.1 现有 projection window 已是生产路径，不应重复造轮子

仓库现状已经提供 r3 所需的客户端历史窗口入口：

- canonical 协议定义 `get_session_projection_window` 的 `older/newer/latest/locate`、opaque cursor、
  turn-aligned 窗口和 snapshot cut（`docs/protocol/bridge-v1.md:1517-1620`）；
- iOS `ProjectionStore.pullWindow` 会消费 `nextOlderCursor`，以 turn id 合并页，并通过同一 replica
  应用（iOS `Models/ProjectionStore.swift:527-631`）；
- iOS 会话滚动路径已调用 `.older`（iOS
  `ViewModels/ChatViewModel+MessageSync.swift:972-990`）；
- 冷打开也已优先走窗口（iOS
  `ViewModels/ChatViewModel+SessionManagement.swift:753-808`）。

这意味着 Phase 3 不应只写“按 Summary items 渲染”，还必须验证/复用现有
`projection_window_v1` 的 load-older 入口、coverage ledger、视口锚定和 cursor-stale 恢复。

### 3.2 当前 Mac 窗口只能切已水合 Kernel，无法自行到达第 2 个上游页

Mac handler 先调用 `ensureProjectionHydrated`，随后从一次 committed snapshot 调
`sliceProjectionWindow`（`go-bridge/projection_window_handler.go:93-120,143-179`）。切片函数直接令
`turns := proj.Turns`（`go-bridge/projection_window.go:210-224`），`hasOlder` 也只由该 slice 在
`proj.Turns` 中的位置决定（同文件 `236-258`）。

canonical R2 还明确规定 upstream cursor 不能过桥；producer 若依赖源 API 分页，必须先归约进
Kernel（`docs/protocol/bridge-v1.md:1594-1600`）。所以 r3 的 “Mac 保存 next/backwards cursor” 只是
必要条件，不是端到端方案；还缺 producer-on-demand 的触发、归约和 fence。

### 3.3 ack-only 方向正确，但顺序语义尚未冻结

r3 说 `syncRev` 用于“ack 与随后 patch 对齐”，却未定义两帧谁先到、何时结束 loading。RPC result
与 projection event 即使共用连接，也必须在 contract 中规定可观察完成条件，不能靠 iOS 时序猜测。

建议冻结为：

- 请求受理后，Mac 先把该 turn 的 `detailLoadState=loading` 提交到 projection SoT；singleflight
  follower 观察同一状态；
- 成功时，在同一 Kernel transaction 中提交 `replace_parts + loaded`，得到 `syncRev=N`；ack 只在
  该 commit 成功后返回；
- iOS 以 replica `appliedRev >= N` 为完成条件，允许 patch 先于或后于 ack 到达；不得因先收到 ack
  就从 RPC result 渲染内容；
- 失败时提交 `failed + reasonCode`，再返回 typed error/failed ack；重试从 `failed` 回到 `loading`；
- session 删除、archive、同 turn generation 改变时，旧请求必须由 fence 丢弃或返回 typed stale，
  不得用较旧的全量 `replace_parts` 覆盖较新的 turn。

## 4. 内容形状复核表

本轮仍没有新增 target live fixture。评级只表示现有证据等级，不否定 Phase 0 的采样设计。

| 内容面 | r3 约束 | 当前证据 | 评级 |
| --- | --- | --- | --- |
| `thread/read.historyMode` | G0 分层、legacy owner 裁决 | target 无样本；旧 codex-web fixture 仅证明两态现实存在 | 🟡 待 G0 |
| turns Summary | 0/1/2 optional slots、desc/cursor | tag 源码支持；target 无 response dump | 🟡 待 G0 |
| turns NotLoaded | 空 items、同 turn identity | tag 源码支持；target 无 response dump | 🟡 待 G0 |
| items/list entry | `turnId + item`、asc 分页 | tag 源码支持；target 无 response dump | 🔴 G0 阻塞 |
| cursor 关系 | next/backwards、inclusive anchor | tag 行为支持；无 target 往返 dump | 🔴 G0 阻塞 |
| Summary↔items id | first-user/final-agent 分别比对 | 无 side-by-side target dump | 🔴 G0 阻塞 |
| Reasoning 四态 | 按 type 统计，owner 裁决语义 | schema replay 为仅 summary；其他三态无 live 样本 | 🔴 G0 阻塞 |
| Command output | null/非空/跨页 | schema replay 不覆盖完整目标矩阵 | 🔴 G0 阻塞 |
| 十类 mapped item | 缺 live 样本时 SkippedTypes 可观测 | schema replay 十类各一，不是 live | 🟡 边界合格 |
| initialTurnsPage | 可选、不得宣称 live proven | tag/ancestor 支撑；无 live response | 🟡 不阻塞 |

G0 的八项清单与防误判规则可以保留；本轮不应把 schema replay 提升为 live proven。

## 5. 新发现的问题

### P0-r3-1：冻结“上游 turns 页 → Kernel → projection_window older”的端到端所有权

在进入 G1 前，计划必须明确：

1. 最近 30 回合进入 Kernel 后，Kernel/producer 在何处保存“仍有更早上游历史”的事实与内部 cursor；
2. iOS 现有 `older` 请求怎样触发 agent 拉 `thread/turns/list` 第 2 页；
3. 新页如何按 desc→asc、inclusive cursor 去重后 prepend 到同一 Kernel truth；
4. `get_session_projection_window` 如何在同一 snapshot fence 下返回新窗口并给出诚实的
   `hasOlder/nextOlderCursor`；
5. 多 iPhone 并发 older、live tail 同时推进、上游 cursor stale、bridge restart/restore 的裁决与测试；
6. 绝不向 iOS 暴露 upstream cursor，绝不把“尚未水合”报告成 `hasOlder=false`。

优先扩展现有 `projection_window_v1` producer admission，而不是新增一套 session-history cursor。
如果仅把首次 30 回合作为本版范围，必须显式把“更早历史不可访问”写成产品回归并由 owner 接受；
本评审不建议该裁决。

### P1-r3-1：冻结 ack/patch 的 revision 完成条件与完整状态迁移

按 §3.3 把 `loading → loaded/failed` 的每次 SoT commit、ack 发送点、`appliedRev >= syncRev` 等待条件、
错误 reasonCode 和 retry 迁移写入 wire contract。当前 T3.2 的“收到 ack 后从 patch 渲染”不足以处理
patch-before-ack、ack-before-patch 和断线后只恢复 snapshot 三种顺序。

### P1-r3-2：给 `replace_parts` 增加 stale-write fence，而不只列场景名

`replace_parts` 是整组替换。任务应冻结 admission token 至少绑定
`(backendId, sessionId, turnId, turnGeneration/baseRev)`；提交前验证目标仍是同一个 completed turn。
目标删除、回合被修正或 generation 改变时返回 typed stale，并保留新 truth，不能把旧 items 猜合进去。

### P1-r3-3：明确生产环境不自动回退 full read

T1.1 的“兼容函数仅供 T0.2 基线与降级路径”与 §2.3 的 fail-closed 容易被实现为运行时 fallback。
应改成：兼容函数默认只供探针/基线；生产路径失败直接显式报错。只有 T0.5 或新 owner 裁决明确指定的
historyMode/版本范围才允许进入旧路径，并记录可移除条件；不得因新 RPC 超时或形状错误自动 full read。

### P2-r3-1：把“所有官方 item variant”收窄为“所有已映射 variant”

T2.1 写“所有官方 item variant”，但 T0.4/T2.2 同时允许未知/缺样本类型走 SkippedTypes。建议统一为
“所有已映射且 schema 支持的官方 item variant 均持久化 item id；未知 variant fail-closed/可观测”，
避免把未来新增 variant 误宣称为已覆盖。

### P2-r3-2：在协议 schema 中冻结 `detailLoadState` 的确切字段位置

除写 `detailLoadState` “进入投影”外，还应在 `unified-bridge-protocol.md` 与 Go/Swift schema 中明确它是
turn-level 还是 message-level 字段、默认缺席如何解码为 `notRequested`、`replace_parts` 同 patch 的
upsert 路径，以及 restore 的向后兼容规则。否则双方可各自正确实现却写到不同层级。

## 6. 未验证内容类型与脚本纪律

r3 §3.0.5 的八项仍是正确的 G0 阻塞门，§3.0.6 的两条防误判规则也已关闭 r2 的归因风险。
执行时还应把新 P0 的窗口链加入 fixture：至少使用超过 `CODEX_REMOTE_TURNS_PAGE_SIZE` 的真实会话，
记录 `window_0 → older → older/EOF`，证明第 31 回合以前可达、turn id 无重叠/无缺口、bridge-owned
cursor 不含 upstream cursor，且 live tail 同时推进时 `syncRev` 链不断。

当前 schema replay 的已知结果仍是：十个 item 均有 id；`userMessage.content` 非空，而
`reasoning.content=[]`、`reasoning.summary` 非空。按 variant 分组后没有计数矛盾，但它不能证明目标
Desktop 的四态分布、items/list 分页或 Summary id 等价。

## 7. 修订优先级与门判定

### P0（进入 G1 前）

1. 增加 upstream turns pagination 与现有 `projection_window_v1` 的端到端接线设计和测试矩阵。
2. G0 使用大于 30 回合的 target fixture 实证 `window_0 → older → EOF` 可达性。

### P1（wire contract 冻结、进入 G2 前）

1. 冻结 ack/patch 顺序无关的 revision 完成条件与 loading/loaded/failed 状态提交。
2. 冻结 `replace_parts` stale-write fence。
3. 禁止未经 owner 裁决的生产 full-read 自动 fallback。

### P2（编辑与 schema 精确化）

1. “所有 official variants”改成“所有 mapped variants”。
2. 明确 `detailLoadState` 的 schema 层级、缺省值和 patch/restore 规则。

**r3 当前判定：Phase 0 = APPROVED；G1/G2 = BLOCKED。** r2 的旧 P0 已全部关闭；新的阻塞不是
目标 API 内容形状，而是现有生产 window 层与新 upstream pagination 层之间缺少所有权和触发链。
完成 P0-r3-1 的文本修订后可以执行 Phase 0；G0 fixtures 仍必须全部通过后才能进入 Phase 1。
