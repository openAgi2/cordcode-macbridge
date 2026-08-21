# 监工指令 11 号完成情况（Question terminal reconciliation 最终收口）

> **指令**：directive-011-question-terminal-reconciliation.md（hole-fill，依据 audit-010 partial）
> **实现提交**：`1506628`（产品修复 + barrier/full-path 测试）、`d6ba0b4`（真实 serve terminal reload 回归）、本报告提交
> **前次报告更正**：指令 10 的实现提交是 **`4a2afb6`**（非 `1e9a714`——该对象不存在，系上一份报告的事实错误）
> **声明**：exec-plan proven ≠ supervisor verified ≠ owner 真机 UI done。提交本报告后停止，等待 audit-011；不进入 owner UI 矩阵。

## 1. 四个 barrier 交错的红/绿（before/after）

所有测试在改产品代码前先落盘并对**修复前代码**复跑确认红灯（真实 adapter + Handlers relay + deltaBatcher + EventPublisher + Kernel 全链，serve 响应闸门控制交错，非顺序冒充）：

| # | 交错 | 修复前（红） | 修复后（绿） |
|---|---|---|---|
| 1 | pending 已投影；recovery 在途（GET 闸门停靠）；live `question.replied` 先落；释放 recovery | `recovery re-projected a stale requested over the terminal: {…Status:pending}`（part 翻回 pending） | 陈旧 requested 被门挡出；part 保持 answered，恰 1 part |
| 2 | recovery 取得旧空 snapshot；新 live asked 随后到达；释放 recovery | （护栏用例：当前代码本就不清除；对任何"absence 即 resolved"的补丁为红） | 新 pending 完整保留（fence：reconciliation 集合在 GET 发出前固定） |
| 3 | pending 已投影；SSE 断开；server 在 gap 内 answer/reject；重连 GET 空 + A7 history terminal | answered 与 rejected 两个子用例均 8s 超时（**产品缺口本体**：pending Dock 永久残留） | 同一 part 原位 settled（answered/rejected），identity（que_a7/msg_u1/call_a7）不漂移 |
| 4 | resolved 后重复/迟到 asked | `late asked must not re-arm a terminal interaction, got [{…Status:pending}]` | 零再投影；terminal 不回 pending |

## 2. Lifecycle 与 source fence 实现

- **裸 claim 删除**：`projectedQuestions map[string]bool` 整体移除，取代为 per-`(sessionID, interactionID)` 的 `questionLifecycle{toolMessageID, toolCallID, turnID, status}`。没有叠加时间窗口、sleep 或第二 referee。
- **串行归约顺序**：每个 question fact——live asked、live terminal 广播、recovery requested、recovery terminal——在**同一把 `questionMu` 内完成 admission 与 emit**。route channel 按 admission 顺序收到事实，reducer 按序应用：terminal 永远不会被更晚入队的陈旧 requested 覆盖；重复 fact 幂等跳过；每个 source fact 至多一次 Kernel ingest；未新增 reducer/writer/协议字段。
- **终态对账**：成功 decode 的 GET `/question` 是 pending set truth，但"缺席"本身不判定状态。对本地已知 pending、server 已无该 ID 的项，从**同一次**权威 `GET /session/{id}/message` 事务按 `messageID + callID` 匹配 question tool：
  - `state.status=completed` 且 `state.metadata.answers` 存在 → answered；
  - `state.status=error` 且 `state.error="The user dismissed this question"`（官方 RejectedError 文案）→ rejected；
  - 其他形状（含 completed 无 answers、非 dismissed 的 error）→ **fail closed**，记诊断日志、保持 pending 等下一周期，绝不凭空选择状态。
  - 同一事务同时供 turn 证明（assistant `parentID` 且 parent 为真实 user 行）。
- **旧 snapshot 不清新 ask**：reconciliation 的输入是 `gatePendingSnapshot`——在 GET `/question` 发出**之前**从门里复制的 pending 集合；此后 live 新建的 pending 不在本周期清算范围。
- **pending 行的 terminal 优先**：恢复时若 history 已带该行的 evidence-proven terminal（reply 落在两次 fetch 之间），直接 settle 而非 arm pending。
- **resolve RPC 边界**：`ResolveUserInput` 成功/404/409 只清除 reply-mapping（`forgetQuestion`）；terminal 投影始终由 server 广播或对账拥有——RPC 丢失广播的场景由 reconciliation 兜底。

### 2.1 resolved cold reopen 的语义边界（证据）

- **同进程 reopen（bridge/投影仍在）**：`TestAudit011_ReopenAfterResolveKeepsInPlace` + 真实 serve `question-reopen-after-*`——route 重立 + GET 空 + terminal history 时零再投影，已 resolved 的 part 原位不动（同一 interaction/turn 语义）。
- **跨进程 cold reopen（全新 adapter）**：A7 raw 样本证明权威 history 的 question tool part 只携带 `messageID/callID/state`，**没有任何 `que_` interaction id**（asked/replied/rejected 帧与 GET 行之外不存在该 id）。因此跨进程重建结构化 part 必须凭空发明 interactionID——被"不得凭空/无 phantom"规则禁止。诚实行为 = 官方 Web 对齐：resolved question 以 tool activity 行呈现（hydrate 既有行为），且**绝不**复活 pending Dock。此边界以 raw 样本逐字段核查为证（`a7-question.raw.json` 全部 tool part 的 key 集合）。

## 3. resolve RPC 全链（owning）

`TestAudit011_ResolveUserInputFullChainAnswer/Reject`：真实 `handleResolveUserInput` → 具体 opencode-web responder → 官方 `POST /question/que_a7/reply`（body `{"answers":[["red"]]}` 逐字校验）/ `POST /question/que_a7/reject`（body `{}`）→ serve 按 true 应答并广播 `question.replied/rejected` → adapter → EventPublisher/Kernel → handler 返回**权威** `headRev>0` 与 `currentStatus=answered/rejected`，outcome=accepted；part 原位迁移、恰 1 part、turn/call identity 不漂移。

## 4. 回归与真实 1.18.18

- `go test ./go-bridge -run TestAudit011 -count=20`：PASS（103.9s）。`go test -race ./agent/opencode-web -run 'TestAudit011|TestQuestion|TestAudit010' -count=20`：PASS。`go test ./...` 全绿；`go vet` / `go build` PASS。
- 真实隔离 1.18.18（4398/4399，用后回收）：`TestSandboxC6C7` 全绿——question、question-reload（pending 冷恢复）、**question-reopen-after-answer / question-reopen-after-reject**（进程内 reopen 零再投影；全新进程对 resolved history + GET empty 零 phantom Dock；未伪造 TCP 掐断，gap ordering 由 barrier 测试证明）、todo、permission、mutations；C4/C2/prompt-options/e2e/archive-delete 家族全过。
- `TestQuestionResolutionEventsFromServer` 更新为新语义：identity-less terminal（从未投影的 que_2）记录但不发射（reducer 本会丢弃）；并新增 late-asked 不再 arm 的断言。

## 5. 安装与状态

- Mac 产品代码有变 → Release 重建（runtime commit `d6ba0b4`）并按规定流程重装：8777 = PID 81262，`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`。
- 4096 全程 owner-managed、harness 未触碰：本轮 PID 48742 保持不变（规定的 killall 重装后其父进程退出成为 PPID 1 的孤儿 serve，新 app 未另行绑定 4096）。
- 4398/4399 已回收。iOS 按指令未重复（10 号安装证据继续有效）。Mac 仓本报告提交后 clean；iOS 仓 clean @ `71007a4`。

## 6. 三轨

- exec-plan proven：`audit010-resolution-race-review-fix-{impl,tests,regression}` 三元组 done（self-attested；测试项可独立复跑）。
- supervisor verified：**待 audit-011**。
- owner 真机 UI 矩阵：未进入。
