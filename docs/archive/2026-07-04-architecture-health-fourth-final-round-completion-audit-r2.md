# 架构健康第四轮（最终轮）完成情况 — 复核审计（Re-audit r2）

日期：2026-07-04
被审计报告：`docs/2026-07-04-architecture-health-fourth-final-round-development-brief完成情况.md`
前序审计：`docs/2026-07-04-architecture-health-fourth-final-round-completion-audit.md`（初轮，结论 conditional pass / 5 项 Finding）
复核范围：初轮 5 项 Finding（P1/P2/P3/L1/L2）的修复落地，由 iOS `4d51a7f9` + MacBridge `5de5672` 承载。
审计性质：独立复核修复 commit、strict gate 复跑、policy 迁移真实性、报告一致性。未运行 UI/snapshot/simulator automation/真机 UI 操作。

---

## 结论

**通过（pass）。初轮 5 项 Finding 全部闭环，第四轮架构健康专项正式可标为 proved-complete / Closed。**

strict gate 复跑 exit 0；`shouldDeferIdleTeardown` 是真正的纯函数（7 显式入参，零 `ChatViewModel` 访问），brief §4.4 禁止的 Claude-only ad-hoc pattern 在生产路径消除；报告 verdict 自相矛盾已统一；exec-plan state 与报告 Plan Status 对齐。专项技术交付 + P0 真机回归闭环 + audit Finding 闭环三层齐备。

---

## 5 项 Finding 逐项闭环

### 🔴 P1（gate FAIL）— RESOLVED

**初轮发现**：`d93d36a5` 把 Generation 顶到 2371/57，超过 baseline 2336/56，strict gate exit 1；报告 §6 称 passed。

**修复（iOS `4d51a7f9` Migrate spurious-idle guard logic into ChatTurnSyncPolicy）**：

- `ChatTurnSyncPolicy.shouldDeferIdleTeardown` 是 `static func` 纯函数，7 个显式入参：

  ```swift
  static func shouldDeferIdleTeardown(
      ownership: Ownership,
      backendKind: BackendKind?,
      isGenerating: Bool,
      hasReceivedAssistantText: Bool,
      activeTurnOriginIsLocalSend: Bool,
      sessionMatches: Bool,
      turnDidComplete: Bool
  ) -> Bool
  ```

  grep `ChatViewModel|@MainActor|async|await|self\.` 仅文档注释命中——无代码访问、无副作用、无网络。原 `shouldKeepClaudeLocalSendAliveBeforeFirstContent` 散落的 5 个 `ChatViewModel` 状态读取（`baseConfig?.backendKind` / `isGenerating` / `lastAssistantTextEventAt == nil` / `activeGenerationTurn` / `turn.origin == .localSend`）全部转为显式入参。brief §4.4 禁止形态在生产路径真正消除。

- `Generation.swift` 实测 **2360/57**，与 `hygiene-baseline.json` 校准值一致。
- baseline `_comment` 详尽说明：spurious-idle 由来、三处 idle 收口守卫位置（CodexStreaming sessionStateChanged + Generation runtimeStateStore-guard + remote-idle）、policy 迁移、本次上调 +24 lines/+1 func 的来源。
- 4 条 pure-func 新测试覆盖判决矩阵：`test_shouldDeferIdleTeardown_claudeLocalSendBeforeFirstText`（正例）/ `_falseAfterFirstText` / `_falseForOtherBackends`（backend-agnostic 边界）/ `_falseWhenNotLocalSend`（ownership 边界）。`ChatTurnSyncPolicyTests` 由 25 → 29 条。
- **strict gate 复跑**：`Result: STRICT passed — no net growth across all baseline files.`，**exit 0**（三条 baseline：bridgeprovider 1629/71/27、generation 2360/57、messagesync 1577/46）。

### 🟠 P2（verdict 自相矛盾）— RESOLVED

**初轮发现**：§0/§0.1-top/§8 行 167 称 Closed，§0.1-bottom/§1/§10 称「暂不 Closed」。

**修复**：

- `grep -nE '暂不 Closed|audit-invalidated'` → **零命中**。
- §0 头部（verdict + Plan Status）、§1 第 53 行（「P0 spurious-idle 真机回归已闭环，专项 Closed」）、§8 第 169 行、§10 标题与正文全部统一为 `proved-complete` + `Closed`。
- §0.1 保留下半段的降级叙事现在被明确框为「历史记录；已被上方 RESOLVED 块取代」——作为 audit 轨迹保留而非活口径，与 RESOLVED 块不再冲突。

### P3（§9 commit 漏）— RESOLVED

**初轮发现**：§9 只列 `9ba4e1d3`+`cd9a178`+`da06183`，漏 P0 修复与 restoration commit。

**修复**：§9 现列 `9ba4e1d3` / `d93d36a5`（P0 修复）/ `4d51a7f9`（audit P1 policy 迁移）/ `cd9a178` / `da06183` / `96d6406`（restoration），每条带改动说明；并新增 §10「审计更正（2026-07-04）」段，诚实记录「P0 修复首版一度把 Generation 顶到 2371/57 而未同步 baseline，导致 strict gate FAIL、本节失真」，以及修复方式。

### L1（§0.1 prekey 红鲱鱼）— RESOLVED

**初轮发现**：§0.1 保留下半段强调 prekey 耗尽（availableCount: 0），与 RESOLVED 块的 spurious-idle 真因冲突，且与 `p0-claude-no-streaming-prekey-redherring` 记忆矛盾。

**修复**：§0.1 保留下半段开头加标注：

> 历史记录；已被上方 RESOLVED 块取代。其中 prekey 耗尽 / live event 投递链路的归因**后被证伪为红鲱鱼**——`text_delta` 不在 `durableMilestoneWhitelist` 内、不经 relay offline 路径，prekey 与流式无关；真实根因是 Mac file relay 的 spurious idle，见 RESOLVED 块。保留原文只为留痕 audit 当时的判断路径与 re-verification 证据。

证伪理由具体（`text_delta` 不在 `durableMilestoneWhitelist` 内、不经 relay offline 路径），可复现；原文保留作 audit 轨迹。

### L2（state 无 closure）— RESOLVED

**初轮发现**：exec-plan state `plan-8146dd664595.json` 无 closure 段、status `current`，与报告「Plan Status: closed」不一致。

**修复**：state 新增 `closure` 段：

```json
{
  "plan_status": "closed",
  "closed_at": "2026-07-04T13:30:00Z",
  "summary": "20/20 todos proven-done. P0 root-caused to spurious idle, fixed via
              shouldKeepClaudeLocalSendAliveBeforeFirstContent (logic migrated to
              ChatTurnSyncPolicy.shouldDeferIdleTeardown pure function per brief §4.4).
              Strict hygiene gate re-passed at chatviewmodel_generation 2360/57.",
  "audit_trail": [
    "2026-07-04T11:35Z: auto-report proved-complete (queue_hash 30b16baf1447)",
    "2026-07-04T12:10Z: audit-invalidated (owner real-device acceptance P0 — prekey framing was initial hypothesis)",
    "2026-07-04T13:10Z: P0 resolved (real root cause: spurious idle; prekey red herring); report restored to proved-complete",
    "2026-07-04T13:30Z: closure recorded; plan_status closed"
  ]
}
```

audit_trail 完整记录初稿 → 降级 → P0 resolve → closure 四步，与 §0.1 文字轨迹一致。

---

## 提交边界

- iOS `4d51a7f9 Migrate spurious-idle guard logic into ChatTurnSyncPolicy (audit fix)`：3 files, +102/-24。`ChatTurnSyncPolicy.swift`(+39) / `ChatViewModel+Generation.swift`(净 +13) / `ChatTurnSyncPolicyTests.swift`(+50)。边界干净，只动 policy 迁移相关。
- MacBridge `5de5672 Resolve fourth-round completion audit findings (P1/P2/P3/L1/L2)`：4 files, +214/-14。`hygiene-baseline.json`(+6) / 完成报告(+27/-14) / exec-plan state(+13) / 初轮审计报告归档(+182)。边界干净。
- 两仓工作树剩余修改均为非第四轮（iOS `CLAUDE.md`/`project.pbxproj`；MacBridge 第三轮 closure 段、`CLAUDE.md`、第三轮报告、3 个 handoff），不影响第四轮闭环判定。

---

## 残余观察（非阻塞）

- **初轮审计报告作为历史档案保留**：`docs/2026-07-04-architecture-health-fourth-final-round-completion-audit.md`（初轮，conditional pass 结论）由 `5de5672` 提交归档，记录的是修复前的发现状态。本文（r2）记录修复后的闭环状态。两者构成完整 audit 轨迹：发现 → 修复 → 复核。未来读者应按 r2 为准判断当前 verdict。
- **pre-existing failing test**（`testClaudeCodeAssistantFinishedCompletesWithoutIdleEvent`）：初轮 §5.3 已诚实披露，归 `e018cb5f` 维护债，非第四轮引入，非本次 5 项 Finding 范围。建议进日常 backlog 跟踪。
- **iOS 52/75 用例未独立复跑**：与历轮审计同范围。验证依靠测试方法静态存在 + 4 条新 pure-func 测试核对 + strict gate 复跑 + policy 纯函数性 grep。
- **真机 policy-migration build 已装**：owner 自述「行为与 P0 修复等价，逻辑更干净」。审计接受 owner 自述（与 `d93d36a5` commit message 内真机验证证据口径一致），未独立复跑真机操作（brief 硬约束需 owner 授权）。

---

## 审计判定

**通过（pass）。** 第四轮架构健康专项技术交付 + P0 真机回归闭环 + audit 5 项 Finding 闭环三层齐备，strict gate 复跑 exit 0，policy 纯函数性、报告 verdict 一致性、exec-plan state closure 全部经独立复核落地。

本次架构健康专项到第四轮正式 **Closed**，不派生第五轮；剩余大文件（`ChatUIKitContainerView.swift` / `claudecode.go` / `appserver_session.go` / `handlers.go`）作为普通维护债进日常 backlog。未来若出现新系统性 gap，按 brief §8 另立独立专项。
