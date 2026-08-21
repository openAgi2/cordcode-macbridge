# opencode-web 新会话首回合「完成态闪烁」根因（独立取证）

- 日期：2026-08-20（同日修订：§4 按 SSV2 护栏撤回 iOS 完成态裁判，改为只修 Mac Kernel；同日落地：拆掉 R2 0 条重拉，Kernel 冷提交 running + Restore 不得 idle 盖 running）
- 任务书：`docs/2026-08-20-firstturn-flicker-analyst-brief.md`
- 方法：先取证后归因。产品代码已按 §4 落地（见 CHANGELOG Unreleased）。方案相对护栏第 4/7/9 条。
- 环境核验（第 0 步）：
  - `/Applications/CordCodeLink.app` 内嵌 runtime 字符串含 `4e421859bac1`；进程 `71467` 听 `*:8777`，argv 含 `-opencode-web-url http://127.0.0.1:4096`，无临时路径残留。
  - 启动：`2026-08-20T11:18:03`，`go-bridge: listening` 11:18:04。
  - iOS 仓 HEAD `79c1a12`。真机 iPhone 16 Pro `connected`（本轮未从设备捞回 11:24 的 Console 行，见 §5 缺口）。

## 1. 实测时间线（ses_fe2c，2026-08-20 11:24）

桥日志 `~/Library/Application Support/CordCode Link/logs/go-bridge.log`，设备 `dev_c5ad42a3` / `192.168.1.2`。

| 时刻 | 证据 | 含义 |
|---|---|---|
| 11:24:07.294 | `RPC create_session` opencode-web `req_21` | 懒建 |
| 11:24:07.460 | `RPC send_message`；`handleSendMessage sessionID=pending-35a2da09` | 发送；iOS 此时已乐观 `waitingForAssistant`（代码路径，见 §3） |
| 11:24:07.534 | `pending→real rebind` `pending-35a2da09` → `ses_fe2cd7232ffeYq3TdjLtZBuVYw` | R2 early rebind **确实发生** |
| 11:24:07.553 | `[K4Patch] flush event=user_message syncRev=1` `executionPresent=true` **delivered** | live 补丁已下发 |
| 11:24:07.561 | 同 `user_message syncRev=2` delivered | live 头到 rev=2 |
| 11:24:07.713 | iOS `get_session_projection` `sinceRev=0` `req_24` | **冷拉整本快照** |
| **11:24:07.725** | `hydrate_commit headRev=2 pendingLive=0`；`responseKind=snapshot` `executionBytes=16` `turnCount=1` `textBytes=30` `partCount=1` | **翻转信号**（见下） |
| 11:24:07.738 / 11:24:08.699 | 两次 pull `sinceRev=2 headRev=2 outcome=delta_at_head` | 头没动，没有新投影 |
| 11:24:11.350 | 第一条 `text_delta`；随后 `[K4Patch] executionPresent=true` | 流式开始，执行中应在此刻翻回 |

发送 → 快照：**265ms**（对齐 owner「立即 &lt;0.5s 完成态」）。  
快照 → 第一条 `text_delta`：**~3.6s**（这次 TTFT；owner 记的「~1s 翻回」是同一窗口的量级描述，本捕获更接近 4s）。

`executionBytes=16` 与 Go `encoding/json` 紧凑编码 **`{"phase":"idle"}` 恰好 16 字节** 对得上；`{"phase":"running"}`=19、`requires_action`=27。该快照 **没有 `activeTurnId`**（带上会更长）。

**把输入框翻成完成态的信号：`get_session_projection` 冷快照（req_24）里的 `execution.phase=idle`，在 send 后 265ms 作为 snapshot 交给 iOS。**

不是 raw `turn_completed` / `session_state_changed`（deny-list 仍在；本窗口第一条 `turn_completed` 要到 11:25:50，是回合真正结束）。也不是 500ms finalize debounce（onset 265ms，比 debounce 更早）。

## 2. 唯一根因

**首回合冷投影把「只有用户消息、助手还没 delta」的会话提交成 `execution.phase=idle` 的 snapshot；iOS SSV2 把 phase 当输入框权威，乐观执行中被清掉。live `text_delta` 到了再变成 running。**

链：

1. 懒建 + 首条 Send 才有 real id。iOS 在 pending 上 `armLocalSendProjectionPaintFence`（`ChatViewModel+Generation.swift:360`）时 `appliedRev(pending)` 为 **0**。
2. 栅栏只比 revision：`shouldHoldLocalSendOptimisticPaint` 在 `syncRev <= baseline` 时才挡住（`:1159-1171`）。注释写了「不要信滞后的 projection.idle」（`:982`），但 **rev=2 > baseline=0，栅栏立刻放开**。
3. 桥在 live `user_message` 补丁（07.553/07.561）之后，iOS `sinceRev=0` 触发 **hydrate Restore**：`CommitHydrateTransaction` 用冷历史 baseline 覆盖 kernel（`projection_kernel.go:1037`），本捕获 `pendingLive=0`——hydrate 窗口里没有把刚才的 live 行再喂回去。
4. R2 重拉只在 **历史 0 条** 时触发（`handlers_projection.go:1298`）。这次 `turnCount=1 textBytes=30`，重拉 **不跑**。registry-live 只阻止把 turn **seal 成 error**（测试 `TestOpenCodeWebLiveSessionColdPullSkipsSealAndCommitsRunningPartial` 只断言没有 `"status":"error"`，**没有断言 `phase=running`**）。
5. iOS 收到 snapshot 后 `applyPresentationChangeSet` / `renderProjectionFromStore`：`shouldBeGenerating = phase.isExecuting` 为 false → `isGenerating=false` + `setGenerationState(.idle, "projection.execution.idle")`（`:1038-1048` / `:1118-1129`）。`isSessionExecuting` 只看 phase ∥ isGenerating（`:2159`），于是输入框完成态。
6. 11:24:11.350 `text_delta` 补丁 `executionPresent=true` 再把 phase 拉回 running。

### 对照

| 面 | 为什么不闪 |
|---|---|
| **第二条起** | 不再 `sinceRev=0` 冷覆盖一个「仅用户行 + idle」的新 session；栅栏 baseline 已是高 rev，live running 补丁接着走 |
| **dsh-web 首回合** | create 即 real id，首拉在发送**前**（空 idle 时还没有乐观执行中）；发送后没有 pending 窗口里的冷 idle 快照 |

### 候选裁决

| 候选 | 裁决 |
|---|---|
| 1 raw 穿透 → finalize debounce | **排除（本捕获）**。onset 265ms ≠ 500ms debounce；本窗口 deny-list 事件的 raw 完成帧在 11:25:50 才出现 |
| 2 纯本地占位回退 | **部分参与，不是信号源**。栅栏本应挡住 idle，但 baseline=0 让 snapshot 合法「超车」。信号仍是桥上下来的 idle snapshot |
| 3 桥残余 idle | **成立**。就是 req_24 的 snapshot，不是 404 错误帧 |
| 4 dsh-web 无 pending 窗口 | **成立，作对照** |
| 5 跑了旧实例 | **排除**。runtime `4e42185`，无临时 app |

## 3. 为什么 R1/R2 单测全绿但闪烁为零变化

| 修复 | 实际打中的 | 为什么打不中本症状 |
|---|---|---|
| R1 `58a1261` emitResultOnce + sourceIsLive | 假终态 EventResult、冷检攒到回合结束 | 流式恢复（owner 已确认）。hydrate 仍可提交 **idle + 1 条用户消息** |
| R2 `4e42185` early rebind + 0 条重拉 | 本捕获 07.534 rebind 已发生；历史不是 0 条 | 空基线重拉条件过窄；seal 测试不锁 `phase` |

R2 注释自己写过「empty idle baseline → iOS flips completed for ~1s」（`handlers_projection.go:1292-1293`），但实现只防了 **零条目**。真机这条是 **一条用户消息 + idle**。

## 4. 合规方案（相对上一版：撤回 iOS 完成态裁判）

上一版第 3 点「`localSend` 未完成时 idle 投影不得清 `isGenerating`」**作废**。那是用客户端本地 send 否决已提交的 `execution.phase=idle`，违反 iOS `CLAUDE.md` Session Sync v2 护栏第 4 条（consumer referee）和第 7 条（完成态只认权威投影）。composer 闪烁的根因在 Mac 把进行中的首回合写成 idle；按第 9 条 **producer 问题修 Mac**。

iOS 继续听投影。已有的 **revision 栅栏**（`shouldHoldLocalSendOptimisticPaint`，仅 `syncRev <= baseline`）是第 6 条允许的显式 fence，**不要**扩成「本地 send 仍活着就忽略 idle」。

**不要再扩 R2 的「0 条则 200ms×6 重拉」。** 护栏第 4 条点名禁止「空投影先等 history」；第 6 条禁止用条数猜 ready。本症状也不是 0 条（`turnCount=1`）。

### 4.1 护栏清单（实施前必填）

| 项 | 本案 |
|---|---|
| 真相 owner | Mac Projection Kernel（`(opencode-web, sessionId)` 的 `SessionProjection`） |
| 唯一 writer | 同一 Kernel reducer；hydrate 与 live 仍是两个事务域，commit 时串行进同一个 reducer |
| 受影响事务域 | hydrate commit / 冷快照 `execution`；**不**改 raw 投递、不改 iOS timeline writer |
| 是否新增数据路径 | 否。不新增 history/raw 客户端路径，不新增 RPC |
| active 下写入口 | 不变：只有 Kernel commit → `projection_patch` / `get_session_projection` snapshot。iOS `messages[]` / `isGenerating` 仍只从 ProjectionStore 映射 |
| 失败呈现 | Kernel 不得用 idle 冒充完成。hydrate 失败仍走现有 failed/retryable，不切 `.off` |
| 防双写测试 | 见 §4.4：锁 `phase=running`，并锁「live 已 running 后 hydrate 不得写回 idle」 |

无法证明「没有第二真相 / 第二 writer / 自动 fallback」则不得开工。本方案：没有。

### 4.2 该改什么（只动 Mac）

**A. 冷提交的执行态必须诚实（第 1、7 条）**

registry-live（桥 `getSession` 命中）且冷源里存在**未收口** user turn（未 seal、无 assistant 终态）时，提交的 `SessionProjection.execution` 必须是：

```json
{"phase":"running","activeTurnId":"<该 turn id>"}
```

不得是 16 字节的 `{"phase":"idle"}`。

落点（择一，优先沿现有 ingest，不要在 commit 后再「猜」）：

- 冷 ingest（`streamRichHistoryProjectionEntries` 或 opencode-web 等价路径）：未 seal 的 user 行必须带上与 live 相同的 `turn_started`（或等价 reducer 事件），让 `projection_reducer.go` 走已有 `Phase: "running"` 分支（约 `:288`），而不是只 upsert 一条没有 armed execution 的 user 消息。
- 现有 `sourceIsLive` / registry-live 不 seal（R1）保留；它们只阻止 `"status":"error"`，**不够**。必须让 phase 本身是 running。

**B. hydrate 不得用冷 idle 盖掉已经 live 的 running（第 5 条）**

11:24 捕获：`user_message` live 补丁在 07.553/07.561 已进 **主** kernel；07.713 才 `BeginHydrateTransaction`。pathless 全量重建对 opencode-web **故意从空 reducer 起**（`projection_kernel.go:780-784` `do NOT Restore`），commit 时 `Restore(cold baseline)`（`:1037`）且 `pendingLive=0`——hydrate 开始前的 live 行不在 pendingLive 里，被冷 idle 整本盖掉。

修法（串行，不靠 syncRev 碰运气）：

- `CommitHydrateTransaction`：Restore 冷 baseline 之前，读主 kernel 当前 snapshot。若其中 `execution.phase` 已是 `running`（或 `requires_action`），commit 结果 **不得回退成 idle**；冷源只补 turns/内容，执行态取「已 live 的 in-flight」与冷源的 max（running/requires_action > idle）。
- 或等价：pathless 全量重建启动时把主 kernel 已有 snapshot Restore 进 tx.reducer（与同文件 Codex pathless「keep carried live baseline」分支同构，`:784-789`），再 ingest 冷源。这样 pendingLive 之外的 pre-hydrate live 也不会丢。

两条里选能用现有测试钉死、且不把普通「死会话冷开真 idle」改坏的一条。死会话（registry 不命中）仍允许 idle + seal。

**C. 明确不改**

- 不改 iOS `isGenerating` / `isSessionExecuting` 对 `execution.phase` 的服从。
- 不把 rev 栅栏改成 phase 裁判。
- 不扩大 0 条 history 重拉。
- 不改 `agent/opencode/`，不切 `.off`。

### 4.3 实施顺序

1. 先补测试（见 §4.4），确认今日红（live 捕获那种「1 条 user + idle」必须失败）。
2. 再改 Kernel/hydrate（A+B）。
3. `go test` 定向：`handlers_projection_ocweb_test.go`、hydrate transaction、reducer execution。
4. Mac **Release 覆盖安装** `/Applications`（`./scripts/build-unsigned-release.sh`），禁止临时 app。
5. 真机：opencode-web 新会话首条；对照第二条和 dsh-web 首条。不改 iOS 包也能验——完成态应只跟 snapshot/patch 的 phase。

落地（同日）：删除 `streamBackendRichHistoryProjectionEvents` 的 0 条 200ms×6 重拉；live 空 assistant 不再 `turn_completed`；`CommitHydrateTransaction` 用 `mergeHydrateBaselineWithLiveExecution` 禁止 running→idle。`TestOpenCodeWebLiveEmptyColdSourceRepollsForFirstPromptPersist` 改为 `DoesNotRepoll`。

真机验收（同日，owner）：新建 session 发送后输入框一直执行中，可正常流式，输出完变为完成态。符合预期。

### 4.4 测试（Mac 仓，锁 phase）

在 `go-bridge/handlers_projection_ocweb_test.go`（及必要的 kernel 单测）补/收紧：

1. **现有** `TestOpenCodeWebLiveSessionColdPullSkipsSealAndCommitsRunningPartial`：在「无 error status、有 user 文案」之外，**必须** `json` 含 `"phase":"running"` 且有非空 `activeTurnId`。今日实现应红。
2. **新**：主 kernel 先 Apply live `user_message`/`turn_started`（phase=running），再跑 pathless hydrate commit（模拟 pendingLive=0）。commit 后 phase **仍为 running**，不得变 idle。
3. **负例**：registry **没有** live session、backend 报 idle → 仍可 idle，且未收口 turn 仍可 seal（现有 dead-session 测试保留）。
4. **不**加 iOS 测试去断言「投影 idle 时 isGenerating 仍为 true」。

### 4.5 可证伪预测

修好后再抓同样首条发送：

- `get_session_projection sinceRev=0` 的 snapshot **`executionBytes` ≠ 16**，应为 `{"phase":"running","activeTurnId":...}`（≥19）。
- 发送后、第一条 `text_delta` 前，不应再下发 `execution.phase=idle` 的 snapshot/patch。
- iOS 不改包：输入框在 send 乐观执行中之后应保持执行中，直到投影 phase 真变 idle（真收口）。
- 第二条、dsh-web 首条、死会话冷开空列表：不变。

若 snapshot 已是 running 而闪烁仍在：再抓 iOS `[TB-VM] scheduleCodexTurnFinishDebounce source=` / `[ProjRender] phase=`（本轮 11:24 无真机 Console，见 §6）。那是新缺口，不是把裁判加回 iOS 的理由。

## 5. 与护栏对照（本方案）

| 条 | 是否踩 | 说明 |
|---|---|---|
| 1 Kernel 唯一真相 | 否 | 只改 Kernel 提交的 phase |
| 4 禁止 referee | 否 | 撤回 iOS 否决 idle；不「空了再等 history」 |
| 5 hydrate/live 串行 | 否 | B 正是修 Restore 盖写 |
| 6 显式 rev/fence | 否 | 不靠条数/静默时间；不改 iOS fence 语义 |
| 7 完成态只认投影 | 否 | 让投影别撒谎；iOS 继续映射 phase |
| 9 按层修 | 否 | 只动 Mac producer |
| 10 失败暴露 | 否 | 不切 `.off`、不假成功 |
| 12 协议 | 否 | 不改 wire 形状，只改同一字段的合法取值时机 |

## 6. 证据缺口（标明）

- **假设（非本轮 Console 实测）**：iOS 应用该 snapshot 时走的是 `projection.execution.idle` 而不是 debounce。Mac 载荷 + iOS 源码对齐，onset 265ms 与 500ms debounce 不合。要钉死，下次复现抓 `[ProjRender]` / `[TB-VM]`。
- 本捕获完成态持续到 `text_delta` 约 3.6s；owner 口头 ~1s。**onset（&lt;0.5s）与 idle snapshot 对齐**；持续时间随 TTFT 变，不改变信号身份。

