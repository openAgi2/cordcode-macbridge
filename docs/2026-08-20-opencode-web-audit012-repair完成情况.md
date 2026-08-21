# 监工指令 12 号完成情况（Question terminal reply-mapping 最终封口）

> **指令**：directive-012-question-terminal-reply-mapping-seal.md（hole-fill，依据 audit-011 partial）
> **实现提交**：`01ca143`（产品封口 + 红灯 owning negatives）、本报告提交
> **声明**：exec-plan proven ≠ supervisor verified ≠ owner 真机 UI done。提交本报告后停止，等待 audit-012；不进入 owner UI 矩阵。

## 1. 两个红灯的 before/after

两个测试均在修改产品代码前落盘，并在**修复前代码**上确认红（均穿过真实 `resolve_user_input` handler → 具体 opencode-web responder → 官方 POST 路由）：

| # | 场景 | 修复前（红） | 修复后（绿） |
|---|---|---|---|
| 1 | terminal 广播已落（answered/rejected 各一）→ 迟到同一 `question.asked` → 反复真实 resolve | `second resolve must be authoritatively refused, got [… outcome:accepted …]`——stale mapping 命中并**发出官方 POST** | 2 秒窗口内每一次尝试均返回 `interaction_not_found`，**零新增官方 POST**；投影仍是同一 terminal part（que_a7/msg_u1，identity 不漂移） |
| 2 | barrier：recovery 的 GET 响应携带 stale pending row 停靠 → live `question.replied` 先落 → 释放后再 resolve | 同上：`accepted` + POST（stale row 经 `gateAdmitRequested` 重开 mapping） | 权威拒绝 + 零 POST；投影保持 answered |

红灯采用有界窗口内**反复**尝试（迟到帧异步处理，单次尝试存在竞态假象——首版测试 1 曾因此假绿，已修正并在修复前复跑确认双红）。

## 2. 实现边界（逐条对应指令 §2）

- **`gateAdmitRequested` 先判后写**：lifecycle 判定（terminal/duplicate）移到**任何** `pendingQuestions` 写入之前；只有真正 admit 一个新 pending fact 时才在同一锁内原子写入 lifecycle + mapping。terminal 或 stale requested 不再恢复 reply mapping。
- **`gateAdmitResolved` 不变**：继续在同一锁内清除 mapping；server terminal 仍是唯一 projection terminal owner。
- **`lookupQuestion` 不绕过 lifecycle**：GET `/question` 回退改走 `notePendingShape`——同一把 `questionMu` 下先查 lifecycle，terminal 行不写 mapping。两表一致性以单一 admission 次序维持（不变式：terminal ⇒ 无 mapping 项），未增加第三个 referee、sleep、fallback 或 404 正常化。
- recovery 的 terminal-settle 分支删除了对 row shape 的预写（与漏洞同类）。
- 未改 A7 mapping、canonical、protocol、WireDescriptor、capability、Kernel/reducer、iOS。

## 3. 回归输出（指令 §3 逐项）

- `go test ./go-bridge -run TestAudit01[12] -count=20 -timeout 5m`：**PASS（200.9s）**。
- `go test -race ./agent/opencode-web -run 'TestAudit01[012]|TestQuestion' -count=20 -timeout 5m`：**PASS（13.0s，无 race）**。
- `go test ./... -count=1 -timeout 10m`：全 PASS；`go vet ./agent/opencode-web ./go-bridge ./core`、`go build ./...`：PASS。
- 真实隔离 1.18.18：`TestSandboxC6C7` 全绿——question（官方 reply 路由）、question-reload（pending 冷恢复）、question-reopen-after-answer / question-reopen-after-reject（terminal reload 双路径零再投影/零 phantom）；C4 turn terminals、e2e 亦绿。4398/4399 用后回收；4096 owner-managed 全程未触碰（PID 48742 不变）。
- Mac 产品代码有变 → Release 重建（runtime commit `01ca143`）并按规定流程重装：**8777 = PID 99662，`/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime`**（只读确认）。
- iOS 按指令未重复（11 号安装证据继续有效）。双仓本报告提交后 clean。

## 4. 三轨

- exec-plan proven：`audit011-terminal-reply-map-review-fix-{impl,tests,regression}` 三元组 done（self-attested；测试项可独立复跑）。
- supervisor verified：**待 audit-012**。
- owner 真机 UI 矩阵：未进入。
