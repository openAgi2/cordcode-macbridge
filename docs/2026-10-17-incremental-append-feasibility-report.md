# CordCode 增量追加可行性调研

日期：2026-10-17
报告版本：**v3**——v2（去锚定重析：restore-then-increment 主架构）+ 本日 owner 三问讨论定案：① 首次打开（无缓存）的拉取路径；② 缓存的双层形态（磁盘保底 + 内存加速）与保留策略；③ **Mac 对客户端进度完全无状态**升级为显式架构不变量。版本沿革：v1 按 brief 初答（存在「空 replica 直接 apply 增量页会丢历史」的流程缺陷，已被 v2 修正）；v2 按 owner 纠正去锚定重排（增量从二阶优化升为重开主机制，S-4 需重设计）。
调研对象：`docs/2026-10-17-incremental-append-research-brief-for-grok.md`（Q1–Q6）
调研纪律：纯只读（本报告为唯一写入文件）；所有代码事实经本轮亲核。
关联文档：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md` v9（R9 agent 评审 APPROVED——作为参照物与依赖清单引用，**不作为设计约束**；owner 已提出增量方向，方案应以 owner 驱动的 v10 修订重新入评审，本报告是其输入）。

## 1. 结论摘要

**owner 的想法成立，且是重开路径的主机制。** 推荐架构 = **「iOS 端缓存恢复 + Mac 端 turnId 锚定增量」（restore-then-increment）**，与 Mac 侧 S-2「checkpoint 恢复 + transcript suffix 折叠」结构对称——**两端各自对自己的持久化状态做增量恢复，互不存储对方的状态**：

- **分工（讨论定案）**：Mac 存**投影真相**（kernel + checkpoint/materialization，S-2 的职责）；iOS 存**进度**（水位线 `tailTurnId` + 已加载范围）；协议里流动的只有 turnId 锚定的有界请求与页。**Mac 不存任何客户端进度状态**（无 per-device offset/记忆）——多设备天然正确，任何一端重启都不需要另一端配合恢复。
- iOS 持久化一份**有界滑动尾窗缓存**（双层：磁盘保底 + 内存加速）；重开同一 session 时先恢复基底，再请求「`tailTurnId` 之后（含锚点自身）」的增量页 append；**首次打开（按设备计）与一切回退场景走 `window_0`**——它是冷启动种子与 fail-closed 兜底，不可替代但不再是重开主路径。
- 收益集中在最高频场景（重开活跃 session）：权威 paint 从「重新拉 ≤4MiB 尾窗（秒级）」降到「本地恢复 + KB 级增量（亚秒级）」，带宽降两个数量级。
- 对已批方案的影响：S-1/S-2/S-5 不变且是依赖；**S-4 须在实施前重设计**（display-only preview → 恢复基底缓存）；协议增量（anchor-newer）独立成 additive capability，**不等 S-3 v2 / OD-2**，走 R10 re-freeze 批次。

## 2. 事实核对

brief §2 列出的事实**全部经本轮亲核成立**（行号基本无偏移）：

| brief 声明 | 实测 | 判定 |
| --- | --- | --- |
| window params 无 `sinceRev`/已知 turnId 参数 | `GetSessionProjectionWindowParams` = SessionID/Directory/BackendID/Direction/Cursor/Limit/AnchorTurnID | ✓ |
| `window_0`/`latest` 的 `hasNewer` 恒 false（尾锚定） | 切片 switch 中两方向 `hasNewer = false`，注释 "Tail-anchored" | ✓ |
| `newer` 需要 cursor 且 cursor 绑 epoch | `newer` 分支先 `decodeProjectionWindowCursor` + scope 校验（元组含 `BridgeEpoch`）；R1（`bridge-v1.md:2266-2272`）"MUST NOT parse cursor contents, persist cursors across bridge epochs" | ✓ |
| journal 128 patches / 2MiB / 30min、process/epoch-scoped | `projection_revision_journal.go:8-12` 常量 + `:34-37` 注释逐字命中 | ✓ |
| full pull 的 sinceRev 三态（delta_at_head / delta / snapshot） | `handlers_projection.go:211-230` 亲核；另有 `resume`（`journal_gap`/`limit`）与 `EpochChanged` 处理 | ✓ |
| iOS `appliedRev` 内存态不落盘；`clear` 归零；`.newer` 无 cursor 直接 `.skipped`；`pullDeltaOrFull` 仅稳态 loop | `ProjectionStore.swift` 各处逐点命中 | ✓ |
| 切 session 清的是离开的 session（:232）；首开 :851 `pullOpenWindowed` | 逐字命中 | ✓ |
| `applyingWindowPage`：空态采用整页 / 按 turnId upsert / 按 anchor 插入 / 不可链式返回 nil；Replica `guard page.syncRev >= appliedRev` | 逐处命中（`ProjectionReplica` 另有 `.windowPageUnchainable`） | ✓ |
| `SessionTimelineStore` 不存 syncRev/appliedRev（continuity metadata，maxSessionCount 12）；`SessionSnapshotStore` 只存 Message 域；SSV2 下 `loadMessages` 被 hard-gate 拦截 | 逐处命中 | ✓ |
| 上滑 prepend 已放弃（iOS think.md 分页复盘；`supportsSessionPagination` 强制 false） | 复盘亲核；本报告遵守：不提恢复 prepend | ✓ |

**brief 未列、对结论关键的补充事实（本轮新核）**：

1. **`locate` 方向已存在「裸 turnId 锚定、免 cursor」先例**（`direction:"locate"` + `anchorTurnId`，无 cursor；anchor 失效 → typed `locate_out_of_window`）——turnId 锚定是冻结协议已有能力。
2. **`newer` 的服务端切片逻辑就是「锚点之后」**：`growWindowRange(sizes, limit, boundary+1, false)` + R7 严格链序（不跳过未加载 turn）——增量页所需服务端语义全部现成，缺的只是入口。
3. **older 已有空页先例**（anchor == front 时诚实空页）——「零新增」空增量页可对称定义。
4. **常规 live patch（decide=false）在 fence 期间与外都对所有 syncV2 连接完整投递、零清空**（R5 评审五条断言之三）——打开后，缓存时刻之后发生的旧 turn 突变由 patch 流自愈。
5. 主方案 v9 已建好 R10 re-freeze 批次机制（协议章节/schema/冻结样本两仓副本/两侧 spec 断言/schemaRevision 六项 +「更新冻结物不删除断言」）——本调研的协议增量直接复用。
6. **wire cursor 被冻结协议禁止跨 epoch 持久化（R1）**——iOS 磁盘上存的水位线必须是**裸 `turnId`**（数据身份），不是 opaque cursor、也不是数字序号（数字 offset 在 compaction/重排后错位）。请求时由 turnId 现场构造锚定，合规且稳健。

## 3. 架构不变量（本日讨论定案，入册 v10 时逐条落进方案）

1. **Mac 对客户端进度完全无状态**：不存 per-device offset / 已读位置 / 客户端记忆。anchor-newer 请求自带 `anchorTurnId`，Mac 对 kernel turn 列表现场 `indexOfTurn` 定位切片（与 `locate` 同构；类比 HTTP Range/ETag、IMAP UID——服务端无状态，客户端持游标）。多设备各水位线各请求，天然不串扰。**禁止**将来以任何理由往 Mac 加 per-device 状态（会引入多设备同步、重启恢复、断连重连三重复杂度）。
2. **Mac 存的是投影真相，不是客户端缓存**：kernel（内存）+ checkpoint/materialization（磁盘，S-2）是 Mac 作为唯一真相 writer 的自身基底；它的直接受益者是 **Mac 自己的重启速度**（不必从 97MB transcript 全量重建，样本全量重建实测约 1.8s）。与 iOS 的「缓存恢复 + 增量」形成对称：**两端各自对自己的持久化状态做增量，互不存对方的状态**。
3. **水位线 = `tailTurnId`（裸 turnId）**：跨重启、跨 epoch 的数据身份；磁盘持久化的就是这个值（持久化 wire cursor 被 R1 禁止）。
4. **双层缓存形态**：磁盘 = 保底权威基底（Application Support，原子写）；内存 = 加速层（活跃 session 的 replica 本身，可能比磁盘新）。磁盘落后内存是安全的——从旧水位线拉增量只是多拉几轮，turnId upsert 幂等。
5. **有界滑动 + LRU，而非「永不清除」**：单 session 滑动上限（头部淘汰，`hasOlder` 提示覆盖）+ 跨 session 总量预算 + LRU 淘汰最久未打开者。淘汰的代价只是该 session 下次打开退回 `window_0`，无正确性损失。
6. **首开与回退统一走 `window_0`**（fail-closed，不假成功）；增量只负责「重开且有有效水位线」的路径。

## 4. Q1–Q6 逐项回答（brief 原问题）

### Q1 水位线：`tailTurnId`（B），以 C 的形态落地；`syncRev` 只作诊断

- **A（syncRev）否决**：journal 是 process/epoch-scoped 有界后缀（128 patch/2MiB/30min），gap 的 fallback 是 full `snapshot`——即 34MB 事故形态；快速路径收益与失败模式不成比例。
- **B（tailTurnId）**：见不变量 3。**C 是实现形态**：持久化 `{backendId, bridgeId, sessionId, headTurnId, tailTurnId, syncRev, bridgeEpoch, savedAt}`；`tailTurnId` 是身份判据，`syncRev`/`bridgeEpoch` 只作诊断与 stale 显示。
- **协议形态**：独立 additive capability（如 `projection_window_anchor_newer_v1`）：`newer` 方向允许 `anchorTurnId` 直接锚定（无 cursor），返回**含锚点自身在内**的后续页，受 limit/4MiB 约束，空页合法，anchor 失效 typed stale。不等 OD-2/v2；走 R10 re-freeze 批次。

### Q2 Mac 重启后：不是特殊场景；「window_0 + 本地 diff」否决

- `tailTurnId` 水位线跨重启有效；仅 anchor 离开保留区（compaction/截断）才 typed stale → `window_0`。
- 「window_0 + diff」否决：网络量不变、要自写 diff 语义，且 restore-then-increment 下它连 UI 收益都没有——恢复基底本身就是权威显示。
- **实现约束**：跨 epoch 续链时 `guard page.syncRev >= appliedRev` 会误判（新 epoch 小 syncRev vs 恢复的旧大值）——restore 必须**重置 appliedRev/coverage 后再链增量页**，链合法性由 turn 链（anchor 邻接 + R7 序）判定。

### Q3 增量超一页：分页 append（A）+ 硬上限；渲染层安全

- 尾部 append 与被禁的头部 prepend 是不同风险面（append 不改变已渲染 item 集合与高度；`applyingWindowPage` 的链式合并就是为此设计）。分页 append 安全；缓解：页间串行 + 视口不在尾部时静默追加。
- **上限**：增量 >2 页或 >2×4MiB → 退 `window_0` 整体重建。**空页**（anchor == tail、零新增）为合法响应。**锚点刷新**：增量页含锚点 turn 自身——缓存保存时锚点可能仍在流式中，重开需要其终态；更早 turn 的滞后突变由打开后的 patch 流自愈（事实 4）。

### Q4 缓存与水位线存储：双层恢复基底缓存（S-4 重设计）

- **磁盘层（保底，owner 定案：重装 App 才会真正失去）**：每 session 单文件 + 原子写（temp + rename），内容 = 已加载 turns（有界范围）+ scalar 状态（execution 等，对齐主方案 F-11/W1）+ 水位线 metadata。**存放位置用 Application Support 而非 Caches**——iOS 系统在存储压力下会清除 Caches 目录，而 Application Support 仅在卸载重装时消失，精确匹配「硬盘保底、重装才清」的预期。
- **内存层（加速）**：活跃 session 的 replica 即最新态（live patch 持续 apply）。App 未杀、切走再切回：内存态若在直接用；不在（现有 `switchSession` 会 clear 离开的 session）则从磁盘 restore + 增量补齐。**写盘时机**：权威 page / 增量页 apply 后、App 退后台时。磁盘落后内存安全（幂等 upsert，见不变量 4）。
- **保留策略（对「永不清除」的工程化修正，owner 已认可方向）**：单 session 滑动上限（拟议 4MiB/256 turns 量级，超限头部淘汰 + `hasOlder` 提示）+ 跨 session 总量预算（拟议 64–128MB）+ LRU 淘汰最久未打开的 session。日常感知上「缓存一直在」，只有极冷门 session 被挤掉，代价 = 下次 `window_0`。schema 版本不兼容 → 整体失效重建。
- 否决：A（内存-only，丢跨启动收益）；B（`SessionSnapshotStore`，Message 域 + hard-gate）；独立小水位线 store（与缓存分裂身份权威）。

### Q5 截断提示：基于权威 `hasOlder` × 本地 coverage ledger

- 增量 append 后本地加载范围 = [缓存起点 … 新 tail]，`hasOlder` 仍为 true，提示语义不变；滑动淘汰与回退都由同一条提示覆盖。刷新时机 = 权威增量页（或回退的 window_0 页）apply 之后。

### Q6 总体推荐

**架构定案：restore-then-increment 为主、`window_0` 为首开/冷启动/回退。** 实施次序按依赖排列（增量是目标，S-1 是依赖）：

1. **S-1（不变，最先）**：恢复 window 链路（含 scalar 交付、re-freeze、trace）。首开与回退都需要它。
2. **协议增量（新增切片，不等 OD-2/v2）**：anchor-newer additive capability + R10 re-freeze 批次。
3. **S-4 重设计后实施（v10 修订先行）**：双层恢复基底缓存（§3.Q4）+ restore 路径（经 replica，重置 appliedRev 后链增量页）+ 滑动淘汰/LRU。
4. S-2 / S-3 / S-5 / S-6 维持主方案定位；**S-6 验收矩阵新增增量场景**：首开种子写入 / 重开零新增 / 少量新增 / 超限回退 / Mac 重启后增量 / 水位线失效回退 / 双设备各自水位线。

**改动清单**：

| 仓库/文件 | 改动 | 性质 |
| --- | --- | --- |
| Mac `docs/protocol/`（bridge-v1.md 章节 + schema + 冻结样本两仓副本）+ `bridge_v1_schema.go` | anchor-newer capability 冻结（参数、含锚点语义、空页、typed stale）+ re-freeze 批次六项 | **协议** |
| Mac `go-bridge/projection_window.go` / `projection_window_handler.go` / `server.go`+`main.go`（协商复用 `negotiateProjectionWindowV1`，对齐主方案 F-26 定案） | anchor-newer 入口分支（复用 `growWindowRange(boundary+1)`）+ capability echo + `projection_rpc` trace（对齐 F-25） | 服务端 |
| Mac `projection_window_spec_test.go` / iOS `ProjectionWindowSpecTests.swift` | 冻结断言/样本更新（更新冻结物不删除断言；对齐 F-21/F-24） | 测试 |
| iOS `Models/`（window 请求/响应 mirror） | anchor-newer 请求模型 | 客户端 |
| iOS `ProjectionStore` | restore 路径（磁盘缓存→replica 基底，重置 appliedRev/coverage）+ 打开路径改造（水位线有效→anchor-newer；否则 window_0） | 客户端 |
| iOS S-4 缓存 store（重设计） | 双层缓存：磁盘（Application Support，原子写，turns+scalar+水位线）+ 内存加速；滑动上限 + 总量 LRU | 客户端 |
| iOS coverage ledger / 截断提示 | 增量页接入 hasOlder×coverage | 客户端 |

**风险清单**：

| 风险 | 触发条件 | 后果 | 缓解 |
| --- | --- | --- | --- |
| 恢复基底与 Mac 真值漂移 | 缓存后旧 turn 被突变/compaction | 显示滞后 | 锚点含入增量页刷新末 turn；更早突变由 patch 流自愈；compaction → anchor 失效 typed stale 回退 |
| 跨 epoch syncRev 误判 | restore 后直接链新页 | `windowPageUnchainable` | restore 重置 appliedRev，链合法性由 turn 链判定 |
| 缓存无界增长 | 多次打开持续 append | 内存/磁盘膨胀 | 滑动上限 + 总量 LRU（§3.Q4） |
| iOS 系统存储压力清缓存 | Caches 目录被系统清除 | 缓存意外丢失 | 存 Application Support（不变量 4 / Q4） |
| 多页 append 布局跳动 | 大增量连续到页 | 渲染抖动 | 串行拉取 + ≤2 页上限，超限 window_0 |
| 平行协议面 | 绕过 re-freeze 私改 v1 | 冻结断言失败 | 只走 additive capability + re-freeze 批次 |
| 增量失败重试口径 | anchor-newer 网络失败 | 与 §7.3 指标冲突 | 纳入同口径（网络/hydrating 失败计入；capability 回退不计） |
| 假成功 | 缓存被当权威而不校验 | 显示错误历史 | restore 必过身份/schema 校验；增量页是权威对账点；失败显式 |
| Mac 侧将来被加 per-device 状态 | 实现走样 | 多设备串扰/重启恢复复杂化 | 不变量 1 入册 v10，评审把关 |

**工作量估计（粗粒度）**：Mac 2–3 人日（含 re-freeze 批次与 spec 测试）；iOS 4–6 人日（restore 路径 + 双层缓存与淘汰 + 打开状态机改造 + 定向测试）；owner 真机矩阵新增 7 个增量场景。**前置**：主方案 v10 修订（S-4 重设计 + anchor-newer 协议切片 + 架构不变量入册）过评审。

**若不可行的替代**：退回主方案 v9 原设计（S-4 display-only preview + window_0 权威重拉）——可用但重开体验与带宽收益全失。

## 5. 推荐方案详述（数据流与回退）

```text
打开 session S
  ├─ 内存态还在？（App 未杀且未被 clear）→ 直接用，进入稳态 patch 流
  ├─ 否 → 读磁盘基底缓存（Application Support；backendId+bridgeId+sessionId 身份命中；schema 校验）
  │    ├─ 命中 → restore：turns+scalar 经 ProjectionStore/Replica 恢复为基底
  │    │        （重置 appliedRev/coverage；epoch≠当前 → stale 标记升级，内容照常显示）
  │    │        → 请求 anchor-newer(anchorTurnId=tailTurnId, 含锚点)
  │    │            ├─ 成功（含空页=零新增）→ append/刷新锚点 → 更新水位线 → 滑动淘汰 → 写盘
  │    │            ├─ hasNewer 且 ≤2 页 → 串行续拉 → append
  │    │            └─ typed stale（anchor 离开保留区）→ ↓回退
  │    └─ 未命中 / schema 失效 / bridgeId 变 → ↓首开/回退
  ├─ 首开/回退：window_0（尾锚定有界窗口 + scalar）→ replica 整体重建
  │            → 写缓存与水位线（首开的产物 = 增量链的种子，按设备各一份）
  └─ 打开后稳态：现有 live patch 流（decide=false 完整投递）——旧 turn 突变自愈

Mac 侧全程无客户端状态：每次 anchor-newer 都是对 kernel turn 列表的现场定位切片；
Mac 重启只影响它自己的真相重建速度（S-2 负责），不影响 iOS 的水位线有效性。
失效面：bridgeId 变 → 整体 miss；epoch 变 → 水位线仍有效（turnId 身份），仅 stale 标记；
        anchor 保留区失效 / 增量超限 / 首开 → window_0（fail-closed，不假成功）。
```

**首开预期管理（讨论定案）**：增量不解决首次打开——那是 window 机制（≤3s 目标）与未来 S-3 v2 summary（超大 turn 收口）的职责。增量价值区间 =「第二次及以后」，日常使用中重开远多于首开，分工合理。首开按设备计：iPhone 与 iPad 各自做各自的 `window_0`、各自持水位线（不变量 1 的直接推论）。

## 6–8. 改动清单 / 风险 / 工作量

见 §3.Q6 与 §5。

## 实施细节补充（评估方提出，本轮亲核成立——v10 S-4 设计必含）

1. **restore 的注入入口**：replica 无"注入"方法，现成入口是 `ProjectionReplica.applyFull(_:sessionId:forceReplace:)`（`ProjectionReplica.swift:193-212`）——`forceReplace: true` 恰好绕开 `projection.syncRev < current.syncRev` 的 stale 判断，可用于恢复缓存基底。
2. **`appliedRev` 重置的精确时序与必要性**：`applyFull` 内 `state.appliedRev = projection.syncRev`（`:208`）——恢复后 appliedRev = 缓存里旧 epoch 的大值；而 `applyWindowPage` 的 `guard page.syncRev >= state.appliedRev`（`:323`）在跨 epoch 新页（小 syncRev）下必然误判 `.windowPageUnchainable`。**重置必须发生在 `applyFull` 之后、增量页请求之前**（显式压回 0）。这不止是解 guard：`applyWindowPage` 成功路径返回 `appliedRev: page.syncRev`，增量页链上后 appliedRev 自动校正为新 epoch 值，同时修好稳态 `pullDeltaOrFull` 的 `sinceRev`——否则旧大值 sinceRev 打到新 epoch Mac，journal 无法服务会退 **34MB full snapshot**（增量架构自己埋的事故回退路径）。时序注意：重置到增量 apply 之间若稳态 pull 抢跑（sinceRev=nil）同样退 full snapshot——打开路径必须保证增量请求先于稳态 loop 启动（现有时序即如此，v10 写明）。
3. **Application Support 目录先例**：仓内已有三个 store 一致使用 `.applicationSupportDirectory`（`SessionSnapshotStore.swift:316-318` 的 `SessionSnapshots/`、`BackgroundGenerationMarkerStore.swift:22`、`SessionReadStateStore.swift:35`）——新缓存 store 照此模式，报告的目录建议与仓内惯例对齐。

## 未解决的问题

1. **产品：滑动上限与 LRU 预算数值**（拟议单 session 4MiB/256 turns、总量 64–128MB）与「超限回退阈值」（拟议 2 页）——建议并入 OD 系列裁决。
2. **产品：Mac 侧 turn 保留窗口**范围（决定 anchor 失效频率）——应引用 S-2 冻结后的保留规则。
3. **流程：主方案 v10 修订**（S-4 重设计 + anchor-newer 协议切片 + §3 架构不变量入册 + S-6 矩阵扩 7 场景）需按 repo 评审循环重新过审。
4. **锚点含入 vs 严格排他**：增量页含锚点自身略放大每页 payload，换取末 turn 终态刷新——实施期用真实样本定夺。
5. **零新增的 UI 表现**：预计静默（水位线更新），实施期确认。
