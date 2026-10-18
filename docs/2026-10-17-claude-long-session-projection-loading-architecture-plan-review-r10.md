# 评审报告 R10：Claude 长 session 打开架构优化方案（v10 第十轮）

日期：2026-10-17
评审员：plan-reviewer（fresh 模式——先独立形成风险清单并完成全部亲核，后以 R1–R9 作回归清单核对闭合；历轮「已闭合/已定案」判定一律不作结论来源，逐条亲自复验）
方案：`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`，版本 **v10**（owner 方向变更修订，非评审意见修订）
方案 SHA-256（全文，开工实测）：`e90f17a50e5c13103b8f4287c60ae01d779477a158de69ccd5e658477e251ddc`——与派发记录一致
方案 SHA-256（收工复测）：`e90f17a50e5c13103b8f4287c60ae01d779477a158de69ccd5e658477e251ddc`——**评审期间未被改动，身份有效**
交接块自报正文哈希：`cf8d3d45ddbeffa8269364224f4d60f6da014f5bf3626264f89bb85ede59e551`，按文档自述切分规则（`raw[:idx].rstrip('\n')`，`idx = "\n---\n\n设计师交接块"`，实测 idx=72717）**精确命中**，记账自洽
范围：full（全量第十轮复审）

## 1. 结论

- **verdict: REVISION_REQUIRED**
- blockers: **0**
- advisories: **2**（F-29、F-30）
- **v10 方向变更判定：架构成立，可实施。** restore-then-increment 的方向、双层缓存形态、裸 `tailTurnId` 水位线、Mac 无客户端状态不变量，经本轮独立亲核**全部成立**，且与既有冻结协议（R1 cursor 禁令、R6 retention miss、R10 re-freeze）自洽。§3.4.3 的两处实施细节（`applyFull(forceReplace:)` 注入入口 + `appliedRev` 重置时序）**证据链逐字精确**，是本方案历轮中质量最高的技术论证之一。
- **未判 APPROVED 的原因**：本轮自选新核验面发现两处**建议级**缺陷——① restore 路径漏了「`applyFull` 之后的 `commitIfObserved` 提交步骤」（不写会导致 lifecycle 不置 `.ready`、UI 不渲染，即「恢复了但屏幕空白」）；② `MemoryResident` 状态在实践中几乎不可达（`switchSession` 必清离开的 session，切回必走 `BaseCacheHit`）。两者都不影响架构方向，但都会让实施者踩坑，且方案 §3.4.2 的状态机描述因此与实际可达路径不符。
- **OD-7 pending 处理：符合先例**（与 OD-1/2/3 同级，不阻塞评审）。
- **整体回退面：完整**（停用 S-4/S-7 即回 v9 行为，§8 三层边界清晰）。
- **历轮意见：R1–R9 全部闭合项经抽核维持**；v10 未改动 S-1/S-2/S-3/S-5 的定位与内容，OD-6=W1、存储选型、re-freeze 批次机制均维持。

**本轮自选新核验面（按指令要求声明）**：除复核 §10.15 六点外，本轮新增三个此前十轮均未覆盖的核验面：

1. **restore 路径的 writer 提交完整性**（产出 F-29）——`applyFull` 是 replica 层方法，不设 lifecycle、不通知 UI；store 层所有既有调用点都紧跟 `commitIfObserved`，v10 未写这一步；
2. **状态机各状态的实际可达性**（产出 F-30）——`MemoryResident` 依赖「内存态未被 clear」，但 `switchSession` 无条件 `clear` 离开的 session，切回必是 `BaseCacheHit`；
3. **水位线在滑动淘汰与 compaction 下的有效性**（正面结论，无缺陷）——淘汰从头部删 turns，`tailTurnId` 是最新轮不受影响；compaction 导致 anchor 失效由 typed stale → `window_0` 覆盖，链路闭合。

## 2. 来源与覆盖

### 2.1 来源身份（本轮实测，P0 门重生成）

| 来源 | 本轮实测身份 | 方案 §2.1 声明 | 判定 |
| --- | --- | --- | --- |
| MacBridge | `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = `f868af18bbbbff6e7f47994c11fb76b49406b33e`（上一轮提交 docs 后推进 1 个提交）；`git status --porcelain` = M 方案 + ?? 调研报告 | 方案未更新提交哈希（仍记 `11caa133`） | 🟡 漂移，但见下 |
| iOS | `/Users/jacklee/Projects/cordcode-ios-native-message-timeline` @ `feat/ios-native-message-timeline`，HEAD = **`e52daa275946ee0d41749aa7146ec8bbc8b05c29`**（方案记 `dfb77ed9…`，**已推进 2 个提交**）；`git status --porcelain` 为**空**（此前 8 项未提交文件已由 `df47e0de` 提交） | `dfb77ed9…` + 8 项未提交枚举 | 🟡 **漂移较大**，但见下 |
| Codex 上游 | `/Users/jacklee/Projects/codex` @ `main`，HEAD = `ce0323f2f2d3b25e82806cd555237da3db4e1897`，`git status --porcelain` 为空 | 同 | ✓ 一致 |
| 正式 runtime | 未重测（本轮不涉及运行态结论；方案对 runtime 的告诫仍有效） | PID 8303，监听 8777 | — 未核 |

**iOS 漂移的性质判定（实测，非推断）**：

```bash
git diff --name-only dfb77ed9…..e52daa27… | grep -E "ProjectionStore|ProjectionReplica|SessionProjectionWindow|SessionProjection\.swift|SessionTimelineStore|SessionSnapshotStore|CCCodeBridgeTransport|CCCodeBridgeBackendClient|ChatViewModel|ProjectionWindowSpecTests"
# （无输出）
```

即 `dfb77ed9 → e52daa27` 的两个提交（`df47e0de` smooth-height S0 + `e52daa27` 文档）**未触及任何本方案锚点文件**。逐文件 `git status --porcelain -- <file>` 实测：`ProjectionStore.swift` / `ProjectionReplica.swift` / `SessionProjectionWindow.swift` / `SessionTimelineStore.swift` / `SessionSnapshotStore.swift` / `ProjectionWindowSpecTests.swift` **全部 clean**。故本轮全部 iOS 锚点核验对象 = 提交内容，身份成立，不污染任何结论。

**MacBridge 漂移**：HEAD 推进 1 个提交（`f868af18`，即上一轮把方案与九轮报告入库的 docs 提交），**未触及任何 go-bridge 源码**。方案 §2.1 仍记 `11caa133`，属时点快照语义（v3 起已标注），不构成缺陷，但与实测不符，并入 F-30 的同类处置建议。

### 2.2 §10.15 六点逐项核验（本轮重点）

| # | 复核点 | 实测 | 判定 |
| --- | --- | --- | --- |
| ① | §3.4 基底缓存重设计 + §3.8 anchor-newer 是否完整自洽；R-4/§3.1 数据流/不变量 2/6 改写是否准确 | 逐节读取：§3.4.1 双层存储（Application Support + 原子写）、裸 `tailTurnId` 水位线、滑动 + LRU、§3.4.2 状态机、§3.4.3 实施细节；§3.8 协议形状（含锚点/空页/typed stale/服务端实现/re-freeze/ iOS 消费）；不变量 2 改写为「缓存必须经同一 writer 注入」、不变量 6 新增「Mac 无客户端状态」 | ✓ **自洽**（除 F-29/F-30 两处缺口） |
| ② | §3.4.3 两处实施细节 + 34MB 回退论证 | **三处证据链逐字精确**：`applyFull` = `ProjectionReplica.swift:193-212`（实测签名 `(_:sessionId:forceReplace:)` 默认 false，`:199` `if !forceReplace, let current = state.projection, projection.syncRev < current.syncRev { return .stale }`）；`:208` = `state.appliedRev = projection.syncRev`；`:323` = `guard page.syncRev >= state.appliedRev else { return .windowPageUnchainable }`。34MB 论证：`sinceRev > headRev` → `ContiguousRangeAt` 返回 `(nil, false, ProjectionResumeJournalGap)`（`projection_revision_journal.go:127`，条件 `headRev <= sinceRev`）→ `resumeSelection.Available == false` → 落到 `logProjectionRPCTrace(..., "snapshot", ...)`（`handlers_projection.go:255`） | ✓ **成立，论证完整** |
| ③ | S-7 与 W1 的 re-freeze 批次合并交付是否合理 | §3.8「本切片批次可与 W1 批次**合并为同一批次交付**（同族变更，减少协议 pack 波动，由实施评审定夺）」；§6 S-7 行同；`open_gates` 记「S-7 re-freeze 批次(可与 W1 合并)」 | ✓ **合理**（同属 Projection Window 家族，合并减少一次 schemaRevision bump） |
| ④ | E-11 对账验收与 §7.4 场景 9-15 是否可执行 | E-11 定义「同一 session『restore + anchor-newer』最终 turn 集与『window_0 全新打开』在重叠范围一致」——**可执行**（两者都是确定性输入，turn 集可逐项比对）；§7.4 场景 9-15 逐条读取（首开种子/零新增/少量新增/超限回退/重启后增量/失效回退/双设备），每条都有明确动作与预期 | ✓ **可执行** |
| ⑤ | OD-7 pending 是否合先例 | §4 OD-7 行「pending，需 owner 裁决（S-4 开工前；不阻塞方案评审——沿用 OD-1/2/3 pending 先例）」；§5 Gate A 记 S-4 前置含 OD-7 | ✓ **合先例** |
| ⑥ | 整体回退面是否完整 | §8 回退边界五条：capability 回退 / anchor-newer 回退 / materialization 回退 / 基底缓存回退 / **v10 方向整体回退 = 停用 S-4 + S-7 恢复 v9**（并注明「v9 经 R9 APPROVED，回退面完整保留」） | ✓ **完整** |

### 2.3 数字与日志抽核

沿用 §1.1 声明口径抽核：checkpoint 34,537,988 B；turns 119；整棵 34,427,361 B；最大单 turn 6,347,762 B（index 1，不在尾 17）；尾 17 = 3,801,579 B；transcript 101,520,809 B = 96.8178 MiB；8 次全量投递；E-2 window RPC = 0；E-1 hydrate_failed = 0。**全部维持命中。**

### 2.4 锚点抽核（历轮既有锚点维持）

抽核 12 处：`projection_window.go:77-82/24-26/52-60/252-257/278-296/297-301`、`projection_revision_journal.go:8-12/34-37/127`、`handlers_projection.go:122/255`、`projection_snapshot_fence.go:224/227/230-235/269-276`、`event_publisher.go:771-776/1726-1749/1802-1811`、`bridge-v1.md:2266-2272/2328-2332`、iOS `ProjectionStore.swift:145/152/763-777`、`ProjectionReplica.swift:193-212/208/323`、`SessionProjectionWindow.swift:96-98/116-176`、`SessionSnapshotStore.swift:316-318`、`ProjectionWindowSpecTests.swift:48/51/129-130`——**全部维持命中**。

### 2.5 覆盖声明

- **已实测核验**：三仓身份（含 iOS/MacBridge 漂移性质判定与锚点洁净性）；方案全文哈希（开工 + 收工）；交接块正文哈希；§10.15 六点逐项；§3.4.3 三处证据链逐字符；34MB 回退论证的完整代码路径；§3.8 的 locate 先例（`projection_window.go:297-301`）与空页先例（`pageEmptyNewerAnchor` `:250/:277/:351`）；不变量 2/6 改写；OD-7/E-11/S-7 登记与交叉引用（15/11/13 处全接通）；§8 回退边界五条；数字与锚点抽核。
- **已独立推理（非命令实测，已标注）**：F-29 的「缺 `commitIfObserved` 会导致 UI 不渲染」——基于 `applyFull` 不设 lifecycle（replica `State` 无该字段）+ store 层全部既有调用点均紧跟 `commitIfObserved` 的两项实测推得，**未运行任何测试**；F-30 的「MemoryResident 不可达」——基于 `switchSession` 无条件 clear 的单点实测推得。
- **未核验及原因**：截图 heic（无视觉工具授权；日志时间线已独立闭环）；runtime 运行态（本轮不涉及，方案告诫仍有效）；transcript 逐行 type 分布（对本案无影响）；**未 build / 未 test**（硬边界）。

## 3. 意见

### F-29 [建议] restore 路径漏写 store 层提交步骤：`applyFull` 之后必须跟 `commitIfObserved`，否则 lifecycle 不置 `.ready`、UI 不渲染

- **位置**：§3.4.2 状态机 `BaseCacheHit` 分支（「restore：turns + scalar 经 `applyFull(forceReplace: true)` 注入 replica 为基底」）；§3.4.3 第 1 条（「现成入口 = `ProjectionReplica.applyFull(_:sessionId:forceReplace:)`」）。
- **实测证据**：
  1. **replica 层不持有 lifecycle**——`ProjectionReplica.swift:103-107`：
     ```swift
     private struct State {
         var projection: SessionProjection?
         var appliedRev: Int = 0
         var index: ProjectionReplicaIndex = ProjectionReplicaIndex()
     }
     ```
     `applyFull`（`:193-212`）只写 `projection` / `appliedRev` / `index`，**不设 lifecycle、不触发 UI 通知**。
  2. **store 层所有既有 `applyFull` 调用点都紧跟 `commitIfObserved`**——`ProjectionStore.swift:1044-1056`：
     ```swift
     switch await replica.applyFull(proj, sessionId: sessionId, forceReplace: forceReplace) {
     case .applied(let changeSet, let projection, let appliedRev):
         let wasActive = states[sessionId]?.projection != nil
         await commitIfObserved(
             sessionId: sessionId, projection: projection, appliedRev: appliedRev,
             lifecycle: .ready, changeSet: changeSet
         )
     ```
  3. **`applyWindowPage`（store 层）同样配 `commitIfObserved`**——`:620-626`。
  4. **`commitIfObserved` 是 lifecycle 与 UI 通知的唯一入口**（`ProjectionStore.swift:1164-1176`）：它做 observed-session 过滤、MainActor hop、`notifyChanged`。
  5. **UI 渲染依赖 lifecycle**——`ChatViewModel.renderProjectionFromStore`（`:1108-1148`）按 `projectionStore.lifecycle(for: sid)` 分支：`.loading` → 显示 loading veil 并 return；`.invalidated` → freeze 或 veil；`.failed`/`.disabled` → return；只有 `.ready` 才继续渲染。**lifecycle 停在初始 `.disabled` 时，restore 完成后屏幕不会出内容。**
- **影响**：v10 §3.4.2 只写「经 `applyFull` 注入 replica 为基底」，未写「随后 `commitIfObserved`（lifecycle `.ready` + changeSet）」。实施者若严格按文面实现，restore 成功但 timeline 空白——**而这恰好是本次方案要解决的「打开后立刻看到内容」核心收益**。缺陷在实施期容易被发现（本地调试立刻可见），故为建议级；但它会让 §3.4.2 的「显示基底 + 正在同步标记」这一步缺一条明确指令。
- **修订方向**：① §3.4.2 `BaseCacheHit` 分支把「restore」展开为两步：「`replica.applyFull(forceReplace: true)` → `commitIfObserved(lifecycle: .ready, changeSet:)`」；② §3.4.3 第 1 条补一句「`applyFull` 是 replica 层方法，不设 lifecycle；store 层必须按既有 `applyFull` 调用点模式（`ProjectionStore.swift:1044-1056`）追加 `commitIfObserved`」；③ §7.2.12 的 restore 断言补「restore 后 `lifecycle == .ready` 且 timeline 非空」。
- **闭合标准**：§3.4 的 restore 步骤含 store 层提交，且 §7.2.12 有对应可测断言。

### F-30 [建议] `MemoryResident` 状态在实践中几乎不可达：`switchSession` 无条件清离开的 session，切回必走 `BaseCacheHit`

- **位置**：§3.4.2 状态机首分支（「`MemoryResident`（App 未杀且该 session 内存态未被 clear）→ 直接进入稳态 patch 流」）；§3.4.1 内存层（「`switchSession` 的 `clear` 只清内存态、不删磁盘缓存」）。
- **实测证据**：
  1. **`switchSession` 无条件 clear 离开的 session**——`ChatViewModel+SessionManagement.swift:228-232`：
     ```swift
     if let leavingSessionId = currentSessionId {
         // Drop v2 projection for the session we leave so isActive/sessionSyncV2Active
         // cannot leak across switchSession (store is process-wide per session id).
         stopProjectionPullLoop()
         await projectionStore.clear(sessionId: leavingSessionId)
     ```
  2. **全仓 `projectionStore.clear` 只有这一个调用点**（`grep -rn "projectionStore.clear"` → 单点），即没有其他保留内存态的路径。
  3. **`clear` 把 `states[sessionId] = nil`**（`ProjectionStore.swift:343-352`）→ `appliedRev` 归零、`projection` 消失。
- **影响**：因此「切走 A → 切回 A」这一最常见的重开场景，**必然走 `BaseCacheHit`**，而不是 `MemoryResident`。`MemoryResident` 实际只在「从未切走」时成立——但那种情况下 session 本来就是打开的，不存在「打开」动作。**结论：状态机的首分支是死路径，真正的重开路径只有 `BaseCacheHit` 一条。**

  这不影响功能正确性（`BaseCacheHit` 能正确工作），但有两个后果：① §3.4.2 的状态机图与实际可达路径不符，实施者会疑惑「MemoryResident 什么时候触发」，甚至可能为此去改 `switchSession` 的 clear 语义（**这会有跨 session 泄漏风险，因为 clear 的注释明确说是为了防止 `isActive`/`sessionSyncV2Active` 跨切泄漏**）；② 方案声称的「内存层加速」在重开场景实际不生效——每次重开都要读磁盘 + 反序列化，虽然比网络快，但和「直接用内存」不是一回事。
- **修订方向**：① §3.4.2 状态机把 `MemoryResident` 重新表述为「同 session 未切走的持续驻留（非打开路径）」，或直接删掉该分支、把首分支改为 `BaseCacheHit`；② 若要保留内存加速收益，需明确「重开时是否值得保留内存态」——但那要求改 `switchSession` 的 clear 语义，**必须先论证不引入 `isActive`/`sessionSyncV2Active` 跨切泄漏**（现有注释明确该 clear 是防泄漏的），这属于设计变更，建议单独评估而非顺手改；③ 若确认不做内存加速，§3.4.1 的「内存层（加速）」表述应改为「内存态仅对当前活跃 session 有效，重开走磁盘恢复」，避免夸大收益。
- **闭合标准**：§3.4.2 的状态机与实际可达路径一致；「内存层加速」的收益表述与实现一致，或明确登记为后续优化。

### 附：本轮正面核验结论（非缺陷，供实施参考）

1. **§3.4.3 的证据链质量很高**：三处锚点（`:193-212` 签名、`:208` 赋值、`:323` guard）逐字符命中，且 34MB 回退论证的完整代码路径（`sinceRev > headRev` → `JournalGap` → `snapshot`）经实测闭合。这是本方案历轮中最扎实的一处技术论证，`appliedRev` 重置的「正确性必需，非防御性卫生」定性准确。
2. **§3.8 的「anchor-newer 不是新发明」判断成立**：`locate` 方向（`projection_window.go:297-301`）确实用裸 `anchorTurnId` 且 anchor 失效返回 typed error，证明「裸 turnId 锚定」在冻结协议中已有先例；`growWindowRange(sizes, limit, boundary+1, false)` 现成可用；空页先例 `pageEmptyNewerAnchor`（`:250/:277/:351`）确实存在。**协议增量的风险因此低于常规新 RPC。**
3. **水位线在滑动淘汰与 compaction 下的有效性**：淘汰从头部删 turns，`tailTurnId`（最新轮）不受影响；compaction 导致 anchor 失效由 typed stale → `window_0` 覆盖。**链路闭合，无缺口。**
4. **不变量 6（Mac 无客户端状态）的价值**：它同时封住了多设备串扰、任一端重启需另一端配合、Mac 侧 per-device 状态膨胀三条复杂度路径，且与 `locate` 的既有无状态语义同构。这条不变量值得在实施评审中作为硬约束把关。
5. **回退面设计完整**：三层（capability 不 echo / 基底缓存不读 / 整体停用 S-4+S-7）逐层可独立回退，且整体回退明确指向 R9 APPROVED 的 v9 行为。

## 4. 历轮意见闭合复核（本轮独立重判）

| 意见 | 级别 | 出处 | 本轮判定 |
| --- | --- | --- | --- |
| F-1 ~ F-9 | 2 阻塞 + 7 建议 | R1 | **全部闭合**（抽核维持） |
| F-10 | 建议 | R2 | **闭合** |
| F-11 / F-12 | 2 阻塞 | R3 | **闭合** |
| F-13 ~ F-16 | 4 建议 | R3 | **全部闭合** |
| F-17 | 阻塞 | R4 | **闭合** |
| F-18 / F-19 | 2 建议 | R4 | **闭合** |
| F-20 | 阻塞 | R5 | **闭合** |
| F-21 / F-22 / F-23 | 1 阻塞 + 2 建议 | R6 | **全部闭合** |
| F-24 / F-25 | 2 建议 | R7 | **闭合** |
| F-26 / F-27 | 2 建议 | R8 | **闭合** |
| F-28 | 建议 | R9 | **闭合**（v10 保留 v9 的更正内容） |

**回归检查**：v10 改动集中于 §0/§1.2/§3.1/§3.4/§3.8/§4/§5/§6/§7/§8/§9/§10，本轮重读这些章节及全部交叉引用（§3.4 ↔ §3.8 ↔ S-4 ↔ S-7 ↔ E-11 ↔ OD-7 ↔ §7.2 ↔ §7.4 ↔ §8），**未发现修订引入的新矛盾或悬空引用**（S-7/OD-7/E-11 交叉引用 15/11/13 处全接通，与交付说明一致）。**未改动项已核实**：S-1/S-2/S-3/S-5 定位与内容、OD-6=W1、fence scalar 分支、存储选型、re-freeze 六项机制均保持 v9 原样。

**一处结构修复记录**：交付说明提到「§4 标题被 §3.8 插入短暂吞掉，当场发现并修复」。本轮实测 `grep -n "^## 4\."` → 命中 `597:## 4. 决策表`，且 §3.8 在 `:581` 正常收束于 `:595`，**结构完整，修复有效**。

## 5. 已定案事项复核

### 5.1 OD-6 = W1（维持，连续第六轮）
W1 仍是唯一满足「零额外往返 + 冷开 running session 正确 + 单一真相链路」的路线。v10 未改动其定案内容，且 §3.7 的 fence 语义与 scalar 交付继续作为 §3.4 基底缓存的 scalar 来源。**维持。**

### 5.2 §3.2.2 存储选型（Mac=bbolt / iOS=文件型，维持，连续第六轮）
五项约束抽核全部成立。v10 的基底缓存沿用「iOS 文件型 + 原子写」，与 §3.2.2 定案一致（且新引 `SessionSnapshotStore.swift:316-318` 等三处 Application Support 惯例锚点，本轮抽核 `:317-318` 命中）。**维持。**

### 5.3 不变量 6（Mac 无客户端状态）——新入册，本轮独立复核
这是 v10 新增的架构不变量。本轮独立评估：它与 `locate` 的既有无状态语义同构、与 HTTP Range/IMAP UID 类比准确、且**禁止**将来加 per-device 状态的措辞明确。**成立，建议作为实施期硬约束。**

## 6. 剩余门

- **证据门**：E-3（window 未生效根因——S-1 修复前必须闭合）；E-4（wire 级验证——S-1 验收）；E-5（iOS 阶段耗时——S-5）；E-6（materialization 正确性——S-2）；E-7（preview/基底不污染权威 writer——S-4）；E-10（window 冷开 scalar 正确性——S-1 完成宣告）；**E-11**（restore + 增量打开路径正确性——S-4 完成宣告）。
- **决定门**：OD-1、OD-2、OD-3、**OD-7**（owner 裁决；OD-1/OD-3/OD-7 阻塞 S-4 开工，OD-2 阻塞 S-3 协议冻结）。
- **S-1 开工前置**：E-1/E-2 + OD-6 定案 + F-20 修订（已完成）+ F-21 re-freeze 批次。
- **S-4 开工前置**：OD-1/OD-3/OD-7 + **S-7 落地**。
- **S-7 开工前置**：anchor-newer re-freeze 批次（可与 W1 合并）。
- **授权门**：S-6 真机 owner 手动矩阵及一切 UI automation（repo 规则 + 2026-10-10 agent-device 永久停用裁决）。

## 7. 评审过程记录

- **模式**：fresh。先独立形成风险清单（三仓身份含漂移判定、方案哈希、§10.15 六点、§3.4.3 证据链、34MB 论证、§3.8 先例、状态机可达性、restore 提交完整性），全部亲核完成后才读 R4–R9 报告作回归清单；历轮「已闭合/已定案」判定未作为任何结论来源。
- **本轮与前轮差异**：R9 判 APPROVED（v9，agent 评审）；v10 是 **owner 驱动的方向变更**（增量追加成为重开主机制），按 repo 评审循环纪律重新进入评审。本轮判 REVISION_REQUIRED（0 阻塞 + 2 建议）——方向与架构成立，两处缺口均为实施细节层面。
- **命令清单（可复现）**：`shasum -a 256 <plan>`（开工 + 收工）；各仓 `git rev-parse HEAD && git branch --show-current && git status --porcelain`；`git diff --name-only dfb77ed9..e52daa27 | grep <锚点>`；逐锚点文件 `git status --porcelain --`；`python3` 交接块哈希切分；`sed -n`/`awk` 逐段读 `ProjectionReplica.swift`（`:103-107/:193-212/:208/:323`）、`ProjectionStore.swift`（`:343-352/:620-626/:1044-1056/:1164-1176`）、`ChatViewModel.swift`（`:1108-1148`）、`ChatViewModel+SessionManagement.swift`（`:228-232`）、`projection_window.go`（`:250/:277/:297-301/:351`）、`projection_revision_journal.go:127`、`handlers_projection.go:255`；`grep -rn "projectionStore.clear"` 单点确认；`grep -n "^## 4\."` 结构校验。
- **唯一写入文件**：本报告。方案、三仓源码、历轮 9 份报告 + 调研报告、iOS 工作树一律只读未触碰；未 build / 未 test / 未部署 / 未安装。
- **契约**：plan-review SKILL.md + plan-contract-v1.1 + review-guide + audit-plan 接入（内容形状专项：checkpoint dump、字节测量、日志计数、fixture key 断言实测、双副本哈希、双 hello 路径镜像比对、既有测试护栏覆盖度、writer 提交链路完整性）。

---

评审员交接块：

```text
verdict: REVISION_REQUIRED
plan_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md
plan_sha256: e90f17a50e5c13103b8f4287c60ae01d779477a158de69ccd5e658477e251ddc
report_path: /Users/jacklee/Projects/cordcode-macbridge-native-message-timeline/docs/2026-10-17-claude-long-session-projection-loading-architecture-plan-review-r10.md
scope: full
blockers: 0
advisories: 2
open_gates: E-3, E-4, E-5, E-6, E-7, E-10, E-11, OD-1, OD-2, OD-3, OD-7, F-21 re-freeze 批次(S-1 前置), S-7 re-freeze 批次(可与 W1 合并), S-6 owner 真机手动矩阵授权
round_complete: true
contract_version: plan-contract-v1.1
```
