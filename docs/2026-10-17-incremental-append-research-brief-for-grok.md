# 调研任务：CordCode iOS 会话打开时的「增量追加」可行性分析

## 0. 你要做什么

对 CordCode（macOS 桥接 + iOS 客户端）做一个**纯只读的技术调研**，回答一个问题：

> **iOS 能否在本地缓存「上次打开到哪」，下次打开同一 session 时只从 Mac 拉取「上次之后的新增消息」，append 到 timeline 尾部？**

产出：一份调研报告，包含可行性结论、方案候选、每个方案的风险与工作量、以及明确的推荐。

**不要写代码，不要改任何文件（除你自己的报告）。**

---

## 1. 背景（必读，否则会误判）

### 1.1 产品架构

- **Mac 侧**：`cordcode-bridge-runtime`（Go），把 AI agent（Claude Code / Codex / OpenCode 等）的会话转成 `SessionProjection` 推给 iOS。
- **iOS 侧**：通过 WebSocket（直连或 Relay）收 projection，渲染成 timeline。
- **关键概念**：
  - `SessionProjection` = 一棵 projection 树，含 `turns[]`（每 turn 是一轮对话）+ `execution` 等标量状态
  - `syncRev` = projection 的版本号，单调递增，**Mac 重启后归零重数**
  - `turnId` = 单个 turn 的稳定身份，跨重启有效
  - `session_sync_v2`（SSV2）= 当前主用的投影同步模式

### 1.2 事故背景（为什么现在要调研这个）

一个 119 turns / 97MB transcript 的 Claude session，打开时 Mac 返回 34MB 完整 projection，iOS 15 秒超时，重试 8 次。

已有方案（`docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`）的对策是：**首开只拉尾部 N 轮（window 机制），不拉全量**。

但该方案**没有做增量**——每次打开都重新拉尾部 N 轮，省了流量但没省"重复拉同样的内容"。

### 1.3 已经明确排除的方案（不要再提）

**上滑加载更早历史（prepend older page）已被明确放弃**，原因见 iOS 仓 `think.md` 约 2699-2724 行：

> 旧页 prepend 会改变已渲染 timeline 的 item 集合和高度……MessageWeb/Virtuoso 的分组、异步内容高度与重渲染时序不能可靠恢复同一可视位置；结果是滚动位置被重算后漂移。
>
> 当前产品决定：`supportsSessionPagination` 强制为 `false`。

**本次调研只关心「向尾部追加新消息」，不关心「向头部插入旧消息」。** 这两件事在渲染层风险完全不同，不要混淆。

---

## 2. 已知的代码事实（我已核实，可直接引用）

### 2.1 Mac 侧（`cordcode-macbridge-native-message-timeline` 仓）

**文件：`go-bridge/projection_window.go`**

- `GetSessionProjectionWindowParams` 字段（第 52-60 行）：
  ```go
  SessionID, Directory, BackendID, Direction, Cursor, Limit, AnchorTurnID
  ```
  **注意：没有 `sinceRev`，也没有「已知 turnId」类参数。**

- `Direction` 取值：`window_0` / `older` / `newer` / `latest` / `locate`

- **`window_0` 与 `latest` 的 `hasNewer` 恒为 `false`**（第 252-257 行）：
  ```go
  case projectionWindowDirectionWindow0, projectionWindowDirectionLatest:
      slice.start, slice.end = growWindowRange(sizes, limit, len(turns), true)
      hasOlder = slice.start > 0
      hasNewer = false   // ← 尾锚定，不会产生 newer cursor
  ```
  推论：**走 `window_0` 拿到的响应里没有 `nextNewerCursor`**，因此无法从这次响应直接续接 `newer` 请求。

- `newer` 分支（第 278-296 行）：
  ```go
  cursor, err := decodeProjectionWindowCursor(params.Cursor)
  validateProjectionWindowCursorScope(cursor, backendID, sessionID, bridgeEpoch)
  boundary := indexOfTurn(turns, cursor.AnchorTurnID)
  if boundary < 0 { return errProjectionWindowCursorStale }
  slice.start, slice.end = growWindowRange(sizes, limit, boundary+1, false)
  ```

- cursor 结构（第 92-99 行）：
  ```go
  type projectionWindowCursor struct {
      V int; BridgeEpoch string; BackendID string; SessionID string
      AnchorTurnID string; Side string   // "o" older | "n" newer
  }
  ```
  **`BridgeEpoch` 是 cursor 的一部分** → Mac 重启后 epoch 变 → 旧 cursor 必然 `cursor_stale`。

**文件：`go-bridge/projection_revision_journal.go`**

- 第 8-12 行：
  ```go
  defaultProjectionJournalMaxPatches = 128
  defaultProjectionJournalMaxBytes   = 2 << 20     // 2 MiB
  defaultProjectionJournalMaxAge     = 30 * time.Minute
  ```
- 第 34-37 行注释：
  > `ProjectionRevisionJournal` is **process/bridge-epoch scoped**……retains only a bounded suffix of already-committed patch values

  推论：**基于 `sinceRev` 的增量只对「Mac 未重启 且 30 分钟内」有效。**

**文件：`go-bridge/handlers_projection.go`**

- `GetSessionProjectionParams`（第 21-26 行）有 `SinceRev` 字段
- 第 211-230 行：`sinceRev == headRev` → 返回空 patch（`delta_at_head`）；有连续 patch → `delta`；否则回 `snapshot` 全量

**`docs/protocol/bridge-v1.md` 第 2268-2270 行（frozen R1）**：
> Clients MUST NOT parse cursor contents, **persist cursors across bridge epochs**

### 2.2 iOS 侧（`cordcode-ios-native-message-timeline` 仓）

**文件：`OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift`**

- `State` 结构（约第 112-119 行）：
  ```swift
  var projection: SessionProjection? = nil
  var appliedRev: Int = 0
  var lifecycle: SessionSyncV2Lifecycle = .disabled
  var window: ProjectionWindowCoverageState? = nil
  ```
  **`appliedRev` 是内存状态，不落盘。**

- `clear(sessionId:)`（约第 343-352 行）：`states[sessionId] = nil` → `appliedRev` 归零

- `pullOpenWindowed`（约第 763-777 行）：首开走 `pullWindow(direction: .window0)`；`.unsupported/.skipped` → 回退 `pullFull`

- `pullWindow`（约第 655-760 行）：
  - `.newer` 分支取 `states[sessionId]?.window?.nextNewerCursor`，**为 nil 则直接 `return .skipped`**（第 673-675 行）
  - `.older` 同理取 `nextOlderCursor`

- `pullDeltaOrFull`（约第 779-783 行）：
  ```swift
  let rev = appliedRev(for: sessionId)
  let since: Int? = rev > 0 ? rev : nil
  return await pull(sessionId: sessionId, sinceRev: since)
  ```
  **只在「打开之后」的稳态 loop 里用，不是打开路径。**

**文件：`OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift`**

- 第 232 行：切 session 时 `await projectionStore.clear(sessionId: leavingSessionId)`（清的是**离开的那个** session）
- 第 851 行：首开 `await self.projectionStore.pullOpenWindowed(sessionId: sessionId)`

**文件：`OpenCodeiOS/OpenCodeiOS/Models/SessionProjectionWindow.swift`**

- `applyingWindowPage`（第 116-176 行）：合并一页 window 到已有 projection
  - 空状态 → 直接采用整页
  - 已有 turns → 按 `turnId` 就地 upsert；未知 turn 按 anchor 位置插入
  - **零重叠且无 anchor → 返回 nil（不可链式）**

**文件：`OpenCodeiOS/OpenCodeiOS/Models/ProjectionReplica.swift`**

- `applyWindowPage`（第 298-390 行）：
  - 首次加载（`state.projection == nil`）→ 采用该页作为初始 projection
  - 已有 → `guard page.syncRev >= state.appliedRev`，否则 `.windowPageUnchainable`

**文件：`OpenCodeiOS/OpenCodeiOS/Services/SessionTimelineStore.swift`**

- 存 `messages` / `activeTurn` / `runtimeState` / `lastAppliedSequence` / `lastAppliedEventID`
- **不存 `appliedRev`，不存 `syncRev`**
- 内存态（`maxSessionCount: 12`）

**文件：`OpenCodeiOS/OpenCodeiOS/Services/Storage/SessionSnapshotStore.swift`**

- 磁盘快照存 `messages` + `schemaVersion` + `lastMessageID`
- **不存 `syncRev` / `appliedRev`**

**文件：`OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+MessageSync.swift`**

- 第 108 行：SSV2 激活时 `loadMessages` 被 HARD-GATE 拦截 → **现有磁盘快照在 SSV2 路径下不参与 timeline 构建**

---

## 3. 需要你回答的核心问题

### Q1（最重要）：增量应该基于什么"水位线"？

候选：

- **A. `syncRev`**：直接对得上 `sinceRev` 参数，零映射。但 Mac 重启后归零、journal 30 分钟过期 → 失效频率高。
- **B. `tailTurnId`**（上次拿到的最后一轮的 turnId）：turnId 是数据身份，跨重启有效。但 Mac 现有 API 无法表达"给我 X 之后的部分"（`window_0` 只给尾部 N 轮；`newer` 需要 cursor 而 cursor 绑 epoch）。
- **C. 两者结合**：存 `tailTurnId` 做身份校验，存 `syncRev` 做快速路径，任一失效则回退。
- **D. 其他**（你有更好的想法请提出）

**请回答**：哪个最优？为什么？如果需要改协议（例如给 window 加 `sinceTurnId` 参数），请说明改动范围与风险。

### Q2：Mac 重启后的行为应该是什么？

Mac 重启 → `bridgeEpoch` 变 → 所有 cursor 失效、`syncRev` 归零。

此时 iOS 手上的缓存：
- 内容还在（`tailTurnId` 有效）
- 但无法续接增量

**请回答**：
- 应该直接回退到 `window_0`（简单，但每次都全量尾部 N 轮）？
- 还是应该有更聪明的做法（例如：先 `window_0` 拿尾部，再与本地缓存做 diff，只 append 真正新增的 turn）？

**注意**：后者的收益是"UI 不闪烁"（本地已有内容不重建），但网络量不变。请评估这个收益是否值得复杂度。

### Q3：增量超过一页怎么办？

假设上次打开到 turn 100，这次打开时 Mac 已有 turn 125（新增 25 轮）。

- 如果一页上限是 100 turns / 4 MiB，25 轮通常能一页装下
- 但如果新增了 200 轮呢？

**请回答**：
- **A. 分页拉**（多次 `newer`，每次都 append 到尾部）——是否安全？会不会有"多次 append 导致渲染抖动"的风险？
- **B. 直接退回 `window_0`**（一次拿尾部 N 轮，丢弃增量）——简单，但大增量时省不了
- **C. 其他**

**关键判断**：多次"向尾部 append"是否真的比"一次 window_0 替换"更安全？请从渲染层角度分析（提示：iOS 侧 `applyingWindowPage` 的 `newer` 分支是"append"，`window_0` 首次加载是"采用整页"）。

### Q4：iOS 应该把水位线存在哪里？

候选：
- **A. 扩展现有 `SessionTimelineStore`**（内存，`maxSessionCount: 12`，App 退出丢失）
- **B. 扩展现有 `SessionSnapshotStore`**（磁盘，但当前存的是 `Message` 而非 projection）
- **C. 新建独立的 `ProjectionWatermarkStore`**（磁盘，只存 `{sessionId, backendId, tailTurnId, syncRev, bridgeEpoch}`）
- **D. 其他**

**请回答**：哪个最优？考虑：
- 现有两个 store 的语义边界（`SessionTimelineStore` 是"continuity metadata, not a second history database"）
- SSV2 模式下 `SessionSnapshotStore` 被 hard-gate 挡着
- 水位线数据量极小（几百字节/session）

### Q5：这个方案与「明确告诉用户更早消息未显示」的关系

已确定要学 Claude App 的做法：当历史被截断时，明确显示「更早的消息未显示」。

**请回答**：增量追加方案是否会影响这个提示的语义？例如：
- 如果只拉增量，用户看到的 timeline 起点可能是"上次打开的位置"，而不是"Mac 保留的最早位置"——这个区别需要提示吗？
- 截断提示应该基于什么判断？（`hasOlder`？本地已加载范围？）

### Q6：总体推荐

请给出：
1. **推荐方案**（含 Q1-Q5 的选择）
2. **最小可行改动清单**（按仓库/文件列出，标注"改协议"vs"纯客户端"）
3. **风险清单**（每条给出触发条件与后果）
4. **工作量估计**（粗粒度：天/人）
5. **如果推荐方案不可行，最接近的替代是什么**

---

## 4. 调研纪律（重要）

1. **必须读源码验证，不要凭我的描述下结论。** 我上面列的行号可能有偏移，以实际代码为准。
2. **区分「方案已定」与「待定」**：本文第 1.3 节「上滑加载已放弃」是已定结论，不要建议恢复；其余都是待定。
3. **不要提出需要 UI 自动化的验证方案**（该仓已永久停用 agent-device）。
4. **如果发现我上面的事实描述有误，请明确指出并给出正确版本。**
5. **报告用中文。**

---

## 5. 参考文件（只读）

**Mac 仓**：`/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`
- `go-bridge/projection_window.go`（window 机制核心）
- `go-bridge/projection_window_handler.go`（RPC handler）
- `go-bridge/projection_revision_journal.go`（patch 日志）
- `go-bridge/handlers_projection.go`（full pull + sinceRev 增量）
- `docs/protocol/bridge-v1.md`（协议冻结规范，搜 "Projection Window"）
- `docs/2026-10-17-claude-long-session-projection-loading-architecture-plan.md`（当前方案，已过 9 轮评审）
- `think.md`（已知问题复盘，搜 "分页" / "prepend" / "跳动"）

**iOS 仓**：`/Users/jacklee/Projects/cordcode-ios-native-message-timeline`
- `OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift`
- `OpenCodeiOS/OpenCodeiOS/Models/ProjectionReplica.swift`
- `OpenCodeiOS/OpenCodeiOS/Models/SessionProjectionWindow.swift`
- `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+SessionManagement.swift`
- `OpenCodeiOS/OpenCodeiOS/Services/SessionTimelineStore.swift`
- `OpenCodeiOS/OpenCodeiOS/Services/Storage/SessionSnapshotStore.swift`
- `think.md`（搜 "2026-07-12" 那条分页复盘）

---

## 6. 输出格式

请输出一份 markdown 报告，结构：

```
# CordCode 增量追加可行性调研

## 1. 结论摘要
（推荐方案 + 一句话理由）

## 2. 事实核对
（我提供的事实中，哪些经你验证成立、哪些有误、哪些补充）

## 3. Q1-Q6 逐项回答

## 4. 推荐方案详述
（含数据流图、失效回退路径）

## 5. 改动清单
（按仓库/文件，标注协议变更 vs 客户端变更）

## 6. 风险清单
（触发条件 → 后果 → 缓解）

## 7. 工作量估计

## 8. 未解决的问题
（需要产品决策或需要更多信息才能定的）
```
