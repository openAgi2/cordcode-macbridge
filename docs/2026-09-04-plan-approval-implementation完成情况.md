# 本轮任务完成情况：计划审批层第一批实施方案（grok / claude / dsh）

## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-plan-approval`
- Plan: `docs/2026-09-04-plan-approval-implementation.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge-plan-approval/.exec-plan/state/plan-dfb27dce3681.json`
- Legacy State File: none
- Completion Report Verdict: `proved-complete`
- Queue Summary: `25/25 todos done, 20/20 required proven (13 re-verified; 5 required=false N/A regressions justified)`
- Related Commits: Mac `plan/approval-layer` HEAD `1d607601540be755658b7444144af0c2f797e136`（代码落地锚 `390ed6efe18b793ef5ccd886c007c93ba010e964`）；iOS `plan/approval-layer-ios` HEAD `7468a54d9b7444278fe015f28ceeb3521f988959`，**工作树未提交**（8 改 + 2 新文件）
- Generated At: `2026-09-04T18:07:53+08:00`
- Queue hash: `07405d59a14b`

来源清单（报告生成时）：
```text
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-plan-approval
分支=plan/approval-layer
提交=1d607601540be755658b7444144af0c2f797e136
未提交状态=M .exec-plan/state/plan-dfb27dce3681.json；另有无关 claudecode 设计文档改动未纳入本项
任务预期分支=plan/approval-layer
配套仓库路径=/Users/jacklee/Projects/cordcode-ios-plan-approval
配套分支=plan/approval-layer-ios
配套提交=7468a54d9b7444278fe015f28ceeb3521f988959
配套未提交=BackendModels/CCCodeBridgeClient/CCCodeBridgeBackendClient/SelectionSheets/ChatUIKitContainerView/ChatInputAccessoryView/UITestIdentifiers/project.pbxproj/WorkspaceBrowserViewModelTests + 新 PlanModeEntryActionTests.swift、PlanApprovalUITests.swift
预期产品特性=permissionKind=plan_review 计划审批卡（grok/claude/dsh 第一批）
```

## 1. Overall Verdict (总体结论)
队列全部 required todo 已 proof-carrying `done`。第一批**代码与单测**已落地；**真机产品验收**按 owner 口述表收口，不是「五条 backend × iOS/Mac 双向全绿」。

证据多数 `tests` 为历史 `re-verified`；本轮真机回归与 owner 矩阵为 `self-attested`（半人工点击 + 日志，无法脚本重放）。

已证明可用：Claude **iOS 发起**计划卡三键审批；Grok **Mac TUI 发起 → iOS 审批**。明确不在第一批完成面内或本轮未跑通的项见 §5。

## 2. Phase Completion Matrix (阶段完成矩阵)
| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| Phase 1 wire | proven-done | proven-done | n/a (justified) | proven-done | impl self-attested；tests re-verified |
| Phase 2a grok | proven-done | proven-done | n/a (justified) | proven-done | impl/tests re-verified |
| Phase 2b claude | proven-done | proven-done | n/a (justified) | proven-done | impl/tests re-verified |
| Phase 2c dsh | proven-done | proven-done | n/a (justified) | proven-done | impl/tests re-verified；真机 `/plan` 入口未跑通（§5） |
| Phase 3 iOS wire | proven-done | proven-done | n/a (justified) | proven-done | impl self-attested；tests re-verified |
| Phase 3 iOS 计划卡 | proven-done | proven-done | proven-done | proven-done | tests/regression re-verified（安装/hello）；卡面真机见 Phase 4 |
| Phase 4 | proven-done (changelog) | n/a | proven-done (runtime + owner 矩阵) | proven-done | runtime re-verified；owner 矩阵 self-attested |
| planentry-fix | proven-done | proven-done | proven-done | proven-done | tests re-verified；regression self-attested（Claude iOS 半人工） |

## 3. Key File Changes (关键文件变更)
- `core/message.go` / `core/interfaces.go`：`PlanPayload`、`PermissionResult.PlanAction`
- `go-bridge/events.go` / `types.go` / `handlers.go`：`permission_request.plan`、`resolve_permission.planAction/feedback`、`sessionActive`
- `agent/grokbuild/`：`exit_plan_mode` → `plan_review` 三动作翻译
- `agent/claudecode/`：`ExitPlanMode` → `plan_review`
- `agent/dsh-web/`：`intent.kind==plan-review` question 改走权限面
- iOS（未提交）：`BackendModels` `PlanModeEntryAction`、计划卡、`chat.input.permissionMode` identifier、`PlanApprovalUITests.swift`

## 4. Verification Evidence (验证证据)
### 4.1 Automated tests
- Commands: 队列内历史 `go test` / iOS `PlanModeEntryActionTests`（commit 390ed6e 前后及 audit 已 re-verified）
- Result: 对应 todo 记录为通过
- Attestation: `re-verified`（历史 audit；本收口轮未再跑全套单测）
- Main test files: `go-bridge/plan_review_wire_test.go`、`agent/grokbuild/leader_plan_test.go`、`agent/dsh-web/plan_review_test.go`、`OpenCodeiOSTests/PlanModeEntryActionTests.swift`
- Artifact paths: 见各 `-tests` todo `verification.artifacts`

### 4.2 Regression evidence
- Device / replay / benchmark / manual validation:
  - Claude iOS LAN 半人工：17:12:59 `mode=plan` → 17:13:43 ExitPlanMode → 17:13:54/17:14:18 批准 → `/tmp/demo-plan.txt`=`ok-manual`
  - Grok Mac TUI → iOS：18:02:10 `exit_plan_mode` 655B → 18:02:29 iOS `resolve_permission` allow
  - Mac Release 生产路径核验见 `phase4-mac-runtime-verification`
- Attestation: `self-attested`（真机点击无法重放）；Mac runtime 项历史 `re-verified`
- Artifact paths: `go-bridge.log` 上述时间窗；`handoffs/handoff-20260904-1659.md`；owner 口述验收表

### 4.3 Audit downgrade summary
- Downgraded todos: none this close-out
- Why they were downgraded: n/a

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)
- **Grok iOS 选 Plan 不生效**：`SetMode` 只写 agent 内存，`session/new` 不把 plan 交给 grok 进程，无 `LiveModeSwitcher`。iOS 发相同指令会走普通问答（本轮出现 `ask_user_question`），不是计划卡。需另案把官方 plan mode 接到会话启动。
- **Claude Mac App 外部 turn**：审批只存在于该进程，iOS 不可见（方案 §8）。
- **Codex Desktop（批准面已绿，入口挂起）**：官方仍无 wire 审批，「实施此计划？」是 Desktop TUI 本地弹窗。本轮已把 `ThreadItem::Plan` 合成 iOS 计划卡，批准代发 `"Implement the plan."` + Default。**owner 2026-09-04 真机**：Mac Codex App 计划模式发任务 → iPhone 同步计划正文并可点批准执行。**iPhone 无法自己打开 Codex 计划模式**（与 DeepSeek Harness 相同产品形状）。owner 裁决先不做 iOS 开启 Plan，调研入口 `docs/2026-09-04-codex-ios-plan-mode-entry.md`。
- **DeepSeek**：齿轮无 Plan 符合官方预设（只读/工作区可写/完全访问）。Mac 须切到「标准」套餐后输入 `/` 选 `plan`（`commands/execute`，不是用户消息）。该路径 **Mac 发起 → iOS 计划卡三键 + 批准生效** 已于 18:09–18:10 真机通过。iOS 输入栏没有 `/` 指令面板；把 `/plan` 当普通消息发出不会进入计划模式（官方 web 同样禁止用 prompt 发 slash 行）。iOS 侧 `/plan` 入口属另案，与 Codex iOS 入口一并挂在 `think.md` 后续案。
- **OpenCode**：无目标形态的 wire 计划审批；仍不在本批。
- **Relay 未测**。
- **iOS 工作树未提交**：计划卡与入口修复仍在 `plan/approval-layer-ios` 脏树。
- 本报告作者即执行 agent；真机项均为 self-attested。

## 6. Audit Focus (建议审核重点)
1. Grok iOS `set_permission_mode=plan` 是否真的启动 grok plan mode（当前结论：否）。
2. Claude iOS 计划卡三键是否与 D5（批准=纯 allow、后续 Write 仍弹权限）一致。
3. iOS 未提交改动是否与 Mac `390ed6e` `sessionActive` 协议配套，提交前是否再对一下来源清单。

## 7. Constraints (关键约束)
- 第一批范围 = grok / claude / dsh；codex / opencode 仅 §8 预留。
- 真机 UI 自动化已按 owner 裁决停止；验收为半人工。
- 禁止用临时 Mac 构建产物测试；本轮生产 runtime 为 `/Applications/CordCodeLink.app`。
- 计划测试文件只用 `/tmp` 绝对路径。
- 不把 Codex/OpenCode/Claude Mac App 外部 turn 的「iOS 无卡」记为第一批实现失败。
