# DSH live-only 投影基线修复 spec（2026-08-16）

owner 方向放行（2026-08-16）：**live-only 会话以 kernel 状态为投影基线**（= DSH 正式迁移
SSV2，live-only 语义）。本文为修复 spec + 测试清单，先于代码提交评审。

## 0. 事故回顾（真机 2026-08-15 23:55，日志 go-bridge.log:1528-1857）

iPhone 切 DeepSeek 模式 → 列表空态（§8 修复验收通过）→ 选 cordcode-ios 目录发送
「讲个笑话」→ 输入框「执行中」永不收口、无任何回复。

Mac 侧事实：turn **完整跑完**——`turn_started seq=1`（23:55:24.2）→ user_message →
reasoning/text deltas → `turn_completed seq=201`（23:55:26.98），K4Patch 逐条 flush 并经
relay delivered 到设备。**后端与真实 key 均正常。**

iPhone 侧事实：ProjectionStore 收到 patch rev1-6 时无基线 → 按既有契约不渲染、触发
`patch_without_snapshot` recovery pull（`ProjectionStore.applyFrame` guard）→ pull
`get_session_projection sinceRev=0` → Mac 回 **`projection.not_migrated`**（23:55:25.1
至 28.0 共 5 次）→ `markFailed` → 死寂：消息页无数据、执行态无终态。

## 1. 根因

`go-bridge/handlers_projection.go:backendSupportsProjectionHydrate` 允许清单仅
`codex/claude/claudecode/opencode/grokbuild`——deepSeek 的驱动事件已进 kernel 投影
（live ingestion → K4Patch 正常产出），但 **kernel 会话永远到不了
`ProjectionHydrateReady`**（`ProjectionKernel.Snapshot` 要求 Ready），
`get_session_projection` 落到 backend 门 → `errProjectionBackendNotMigrated`。
iOS 消息页以 SSV2 投影为唯一数据源，基线拉取被拒 → 无渲染、无终态。

## 2. 修复设计（五条硬约束逐条落实）

### C1 基线 = kernel 权威状态，走既有 snapshot/fence 串行化

**Mac `ensureProjectionHydrated` 增 live-only admission 分支**（在
`backendSupportsProjectionHydrate` 门之前；Ready 快路径天然在前不动）：

- 触发条件：`backendID == "deepseek"`（wire 别名 dsh→deepseek 已在 main.go 收敛）。
- **不读任何磁盘/历史源**（DSH 无 transcript、无 RichHistory，§4 live-only）。
- admission 流程（全部复用既有 kernel 事务原语，不新增并行写路径）：
  1. `BeginHydrateTransaction(source=live-only descriptor, sourceIsLive=true)`：
     descriptor = `{Identity: "deepseek-live:"+sessionID, Kind:"live-only", Path:"",
     Cursor:0}`。`Path=="" ∧ 无 Segments ∧ !sourceChanged` 命中 kernel 既有
     「pathless 保留 carried live baseline」分支——tx reducer 直接
     `Restore(kernel reducer snapshot)`，即 **基线 = kernel 权威状态**。
  2. `AlreadyReady` → 直接返回 nil（幂等）。
  3. Leader → 立即 `CommitHydrateTransaction`：原子发布基线 + pendingLive
     （admission 窗口内到达的 live 事件按序补入）→ Ready。commit 返回的
     `PendingPatch` 按 `runProjectionHydrateTransaction` 尾部同款方式经
     eventPublisher 发布（若非 nil）。Follower → await `admission.Done`。
- rev 连续性：reducer 快照携带 SyncRev；journal（rev 1..N）不受影响；
  `BeginProjectionSnapshotWithResume` 的 `cutRev` 取自 kernel Snapshot（Ready 后可用），
  post-cut patch 由既有 fence 在 RPC 结果入队后同 sink 释放——**零新串行化逻辑**。
- 会话刚建、kernel 零事件但 live registry 有会话：commit rev-0 空投影是真相
  （live-only 无历史可掩蔽，非 §10.5.7 禁止的「空壳掩蔽 hydrate 失败」）。

**iOS reconcile（本次日志 rev1-6 先于 sinceRev=0 到达）**：现状即契约——patch 无基线
不渲染、触发 recovery pull；基线 snapshot（cutRev≥6）整建；后续 patch 按基线续接。
**测试锁定**此行为（T11），不改同步管线（C3）。

### C2 会话已死/不存在 → 诚实 not_found，禁止空壳快照

- 判定：kernel 无该会话状态 **且** live registry 无该会话 → 返回新 wire 错误码
  **`projection.not_found`**，`retryable=false`。场景：bridge 重启后重开本地留存的
  deepSeek 会话；session 被淘汰且 kernel 无痕。
- kernel 有状态但 live 进程已死（进程死亡时 driver 已合成终态事件）→ **照常服务**
  最后已知状态（含终态 execution phase），不报错、不空壳。
- iOS 侧 `not_found` 映射诚实文案（见附带修复 B）。

### C3 禁止 iOS 侧裁判

不改 iOS 同步/渲染管线：不超时收口、不无 ownership 渲染补丁、不加 legacy fallback。
iOS 侧改动仅限：错误呈现（附带修复 B）与既有行为的测试锁定（T11/T12）。

### C4 按声明记账（顺序：canonical → mirror → 代码）

1. canonical `docs/protocol/bridge-v1.md`（MacBridge）：v2 projection 错误表增
   `projection.not_found` 行（live-only backend 会话本 epoch 无 kernel 状态且无 live
   会话；retryable=false）。
2. iOS mirror `../cordcode-ios/docs/protocol/bridge-v1.md` 同步。
3. 设计文档 `docs/2026-08-13-dsh-driver-design.md` §8 增 `session_sync_v2` 条目：
   🟢（live-only 基线 admission；无 history backfill；dead → not_found）。
4. 完成报告 `docs/2026-08-13-dsh-driver-design完成情况.md` 增补章节；双仓 CHANGELOG。

### C5 不许只改允许清单：独立 live-only admission 路径

- `backendSupportsProjectionHydrate` **不加** deepseek（guard 测试 T6 锁定返回 false）；
  deepseek 不进 `forceColdInspection` 名单；`prepareProjectionHydrateSource` 的 backend
  switch 不加 deepseek（admission 分支在调用它之前返回）。
- 独立判定函数 `backendUsesLiveOnlyProjection(backendID) bool`（现仅 "deepseek"），
  带注释说明 live-only 语义与禁止项（无磁盘 hydrate 源、不代装、不读 ~/.dsh）。

## 3. 附带修复

### A. 观察心跳在会话被清后停止（Mac 侧）

现状：`set_observation_scope` 每 ~20s 续租（lease 90s），会话已死后仍带
`sessions=[死id]` 续租 → Mac 每次为其重启 relayEvents → 10s idle timeout（日志
23:55:37 起）空转。
修法（Mac 权威侧剪枝）：`set_observation_scope` 处理时，对 **live-only backend** 的
session，若既无 live registry 会话又无 kernel 状态 → 从 observed set 剔除（该 session
按定义已无任何可观察事件源）。**其他 backend 不动**（claude/codex 的外部 turn 观察
依赖非 registry 会话，剪枝会破坏外部观察）。
iOS 续租循环每轮重读 `currentSessionId`（已核实），无需改动。

### B. iOS not_migrated/not_found 死寂 → 诚实状态

现状：recovery pull（patch 触发）的终态失败只 `markFailed` 进 store，未走
ChatViewModel 的 `K4FailureState` errorMessage 路径 → 用户看到永久「执行中」而非错误。
修法：**所有** session_sync_v2 pull 终态失败（初始 pull 与 recovery pull 同路径）经
既有 `K4FailureState` 呈现；`not_found`（及 `not_migrated`）用专门文案
「会话已结束或不存在（实时模式会话不保留历史）」，其余码维持现有文案。
不新增裁判逻辑：只是把 store 的 failed lifecycle 观测接到已存在的错误呈现面。

## 4. 测试清单（约束 → 用例映射）

### Mac（go-bridge，handlers_projection / kernel）

| # | 约束 | 用例 |
|---|---|---|
| T1 | C1 | deepSeek 会话 live 注入若干事件后 `get_session_projection sinceRev=0` → 成功 snapshot；headRev=journal 头；rev 自 1 连续；kernel Ready；第二次 pull 走 Ready 快路径（日志断言无新 admission） |
| T2 | C1 | 先 flush patch rev1-6 再 pull（复刻本次日志时序）→ snapshot cutRev≥6 含全部 turn；随后 live patch rev7 正常续接，无 gap/重复 |
| T3 | C1 | pull 进行中并发 live patch burst → post-cut patch 在 RPC 结果之后同 sink 有序释放（fence 契约对 deepSeek 同样成立） |
| T4 | C2 | deepSeek 会话 kernel 无状态 + registry 无会话 → `projection.not_found`、`retryable=false`、无 data（不空壳）；模拟 bridge 重启后重开 |
| T5 | C2 | kernel 有状态、live 进程已死（driver 已发终态事件）→ pull 成功返回最后已知状态（含终态 execution）；不报错不空壳 |
| T6 | C5 | guard：`backendSupportsProjectionHydrate("deepseek")==false`；deepseek 不在 forceColdInspection；admission 分支不触达 `prepareProjectionHydrateSource`（无 provider 的 fake agent 亦可通过 T1 佐证） |
| T7 | C5 | admission 全程零磁盘读（fake agent 无 TranscriptLocator/RichHistory 接口，T1 即证） |
| T8 | 附A | set_observation_scope 续租携带已死 deepSeek session → observed set 剔除、不再为其启动 relayEvents；同请求中的 claude 未知 session 仍被观察（回归护栏） |
| T9 | C4 | not_found wire 形状（code/retryable=false/message）与 canonical 文档一致（fixture 断言） |
| T10 | 回归 | 既有 `TestProjectionNotMigratedForUnsupportedBackend`（madeup-backend）不变绿；五大 backend hydrate 行为零变化 |

### iOS（OpenCodeiOSTests）

| # | 约束 | 用例 |
|---|---|---|
| T11 | C1 | reconcile：patch rev1-6 先到（无基线）→ 不渲染 + recovery pull；基线 snapshot rev6 到达 → 完整渲染；patch rev7 续接；无重复/幽灵 turn |
| T12 | C3 | 裁判禁令锁定：.loading/.failed 期间 patch 不渲染；无 Mac 数据时不因超时合成完成态（确定性：推进时间无终态伪造） |
| T13 | 附B | recovery pull 终态失败 not_found/not_migrated → errorMessage 经 K4FailureState 呈现（专门文案）；retryable 码仍走 loading 不报错 |

## 5. 实施顺序

1. 本 spec commit（先行评审锚点）。
2. C4 文档：canonical → iOS mirror → 设计 §8 → （代码后）完成报告 + CHANGELOG。
3. Mac：admission 路径 + not_found + 观察剪枝 + T1-T10。
4. iOS：诚实状态 + T11-T13。
5. `go test ./go-bridge/... -run 'Projection' -count=1` + DSH 定向套件；iOS
   `-only-testing:CCCodeTests` 定向类。
6. MacBridge Release 构建覆盖安装 /Applications + 重启；`scripts/run.sh device` 真机重装。
7. owner 真机复验：切 DeepSeek → 发消息 → 流式回复可见、执行态收口；重开已结束会话 →
   诚实「会话已结束」提示；日志无 relayEvents 空转。

## 6. 边界与不做

- 不实现 deepSeek ListSessions/历史 backfill（§4 live-only 冻结，owner 止损指令重申）。
- 不读 `~/.dsh`（探测-复用-未启动形态只读凭据链不变）。
- 不改五大 backend 的 hydrate/observation 行为。
- iOS 同步管线（patch 应用/超时/收口）零改动。
