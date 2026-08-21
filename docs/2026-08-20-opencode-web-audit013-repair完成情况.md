# 监工指令 13 号完成情况（Question lookup terminal gate 最终封口）

> **指令**：directive-013-question-lookup-terminal-gate.md（hole-fill，依据 audit-012 partial）
> **实现提交**：`ac3c268`（产品封口 + 红灯 owning negative）、本报告提交
> **声明**：exec-plan proven ≠ supervisor verified ≠ owner 真机 UI done。提交本报告后停止，等待 audit-013；不进入 owner UI 矩阵。

## 1. 红灯 before/after

`TestAudit013_StaleCurrentGetRowSecondResolveZeroPOST`（`go-bridge/audit013_fullpath_test.go`）在修改产品代码前落盘并在旧实现确认红。场景精确复刻 audit-012 判定的边界：**terminal 已落、内存 mapping 已清、当前 `GET /question` 在整个测试期间持续返回同一 stale row**（测试从不把端点改空；POST fixture 应答普通 200 `true`，无 404/409 掩盖 adapter 旁路）。resolve 穿过真实 `resolve_user_input` handler → 具体 opencode-web responder。

| 子用例 | 修复前（红） | 修复后（绿） |
|---|---|---|
| answered terminal + 二次 answer | `outcome:accepted` + **发出官方 POST**（旁路：`notePendingShape` 拒写但 `lookupQuestion` 无条件返回 row） | 权威 `interaction_not_found`，**零官方 POST**；投影保持同一 terminal part（que_a7/msg_u1） |
| rejected terminal + 二次 reject | 同上（accepted + POST） | 同上（零 POST、单一 terminal part） |

## 2. 实现（指令 §2 逐条）

- **admission 结果回传**：`notePendingShape` 返回 lifecycle 裁定；被 terminal 拒绝的 row 既不写 mapping，也不能作为 `lookupQuestion` 的成功返回值——GET fallback 循环 `continue` 跳过被拒 row，扫描结束后返回权威 `interaction_not_found`，不发 POST。
- **冷重启 pending 保持可提交**：尚无 lifecycle 的真实 pending row（进程重启后 iOS 持有 interactionID 的 resolve）仍被放行为 reply shape——`TestQuestionReplySendsOfficialAnswersBody`（无 lifecycle 的 GET 回退 POST）与真实 serve `question-reload` 均证 directive-010 pending reload 未破坏。
- **同一 admission 次序**：判定与写入仍在同一把 `questionMu` 内；不变式扩展为 terminal ⇒ 无 mapping 项 **且** 无 lookup 成功。未新增第三表/referee、sleep、fallback、协议字段或 Kernel writer；未改 A7 mapping、canonical、Projection、能力、iOS。

## 3. 回归输出（引号正则，默认 zsh 可直接复现）

- `go test ./go-bridge -run 'TestAudit01[123]' -count=20 -timeout 6m`：**PASS（201.9s）**。
- `go test -race ./agent/opencode-web -run 'TestAudit01[0123]|TestQuestion' -count=20 -timeout 5m`：**PASS（13.0s，无 race）**。
- `go test ./... -count=1 -timeout 10m`：全 PASS；`go vet ./agent/opencode-web ./go-bridge ./core`、`go build ./...`：PASS。
- 真实隔离 1.18.18：`TestSandboxC6C7` 全绿（question 官方 reply、question-reload pending 冷恢复、question-reopen-after-answer/reject terminal reload），`TestSandboxEndToEnd` 绿。4398/4399 用后回收；4096 owner-managed 全程未触碰（PID 48742 不变）。
- Mac 产品代码有变 → Release 重建（runtime commit `ac3c268`）并按规定流程重装：**8777 = PID 10968，`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`**（只读确认）。
- iOS 按指令未重复。双仓本报告提交后 clean。

## 4. 三轨

- exec-plan proven：`audit012-lookup-terminal-gate-review-fix-{impl,tests,regression}` 三元组 done（self-attested；测试项可独立复跑）。
- supervisor verified：**待 audit-013**。
- owner 真机 UI 矩阵：未进入。
