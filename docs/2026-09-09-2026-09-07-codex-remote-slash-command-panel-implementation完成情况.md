# 本轮任务完成情况：Codex Desktop（codex-remote）斜杠命令面板接入方案

## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-plan-approval`
- Plan: `docs/2026-09-07-codex-remote-slash-command-panel-implementation.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge-plan-approval/.exec-plan/state/plan-c01104525c41.json`
- Legacy State File: none
- Completion Report Verdict: `proved-complete`
- Queue Summary: 50/50 todos done, 50/50 proven (required), 其中 24 re-verified（审计期重跑命令）、26 self-attested
- Based-on Queue Hash: `d8764d1a63e0`
- Related Commits: Mac `plan/approval-layer` 一系列（最近：`8c98dbb`、`38ce43c`、`1455206`）；iOS `plan/approval-layer-ios`（`aac03958`、`153bee70`、`33a32976`、`6f71af01`）
- Generated At: 2026-09-12T19:2x UTC+8（owner 真机验收当日）

## 1. Overall Verdict (总体结论)

一期（Compact / Plan / Goal 三项原生动作 + P5.7 智能体可见性与 /goal 对齐）**完成并通过 owner 真机验收**：owner 2026-09-12 明确验收「codex desktop 模式的 goal，plan，compact 命令都正常执行✅」，此前一轮另验收「workflow 卡片展示正常」与「/goal 命令卡位置正常」。证据主体为 owner 手工矩阵（self-attested）+ 24 项重跑的自动化测试（re-verified）+ 四轮真机 goal 测试的完整取证链（rollout/日志/截图/分析文档）。审计后（audit）状态：无未证明的 required 项。

**已知交付后遗留（不属本案 required 门，均已建档跟踪）**：
1. 切走再切回后 goal 命令卡/goal bar 消失（iOS 重开渲染链；已装埋点构建 `6f71af01`，下次复现可直接定位）；
2. goal 后输入栏 ➕/⚙️/模型 面板失效（同上埋点待复现；owner 已有「···」恢复法）；
3. §6.2 collabAgentToolCall 冷路径失明（runtime 全量重建+iOS 冷开同窗才触发，另立任务）。
4. owner 矩阵第⑥行（Mac 与手机同时修改 Plan/Goal 的官方读回一致性）**未做专门双端并发真机测试**；该语义由 C5 定向测试与官方读回设计覆盖。作为已知 gap 记录，不冒充已验。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| P0 取证（C0–C5） | done | done (re-verified) | done (re-verified) | proven-done | 审计期重跑样本契约测试；C2–C4 真实 Remote 样本归档 testdata/slash-panel/ |
| P1 identity/readiness | done | done (re-verified) | done | proven-done | Go 单测重跑绿 |
| P2 Compact | done | done (re-verified) | done | proven-done | 命令卡路径 153bee70 + owner 真机 ✅（2026-09-12） |
| P3 Plan | done | done (re-verified) | done | proven-done | 冷读上游补丁（cordcode/plan-cold-verify 分支）+ owner ✅ |
| P4 Goal | done | done (re-verified) | done | proven-done | 四轮真机 goal 测试 + owner ✅ |
| P5 iOS 三项动作 | done | done | **done（owner 矩阵 2026-09-12 ✅，self-attested；⑥行未单独覆盖已注明）** | proven-done | 本轮 owner 验收 + 分析文档链 |
| P5.7 智能体可见性 | done | done (re-verified) | **done（owner 第⑦行，self-attested）** | proven-done | 18:08/18:09 截图 + 三轮修复验证（8c98dbb/38ce43c/33a32976） |
| P6 协议/交付 | done | done | **done（发布身份+装机核验，self-attested）** | proven-done | Mac 38ce43c→/Applications 运行态五项核验；iOS 33a32976/6f71af01→iPhone 16 Pro devicectl 安装确认 |

## 3. Key File Changes (关键文件变更)

**Mac（codex-remote / go-bridge / core）**
- `agent/codex-remote/`：collab_workflow.go（跨 turn spawn 锚定 run + 重连存活 + wait 重建成员 + poll 点名防御）、codec.go、collaboration_mode.go（thread/settings/get）、goal controller、history*.go（冷路径会话级 fold 注册表 + MapTurnItemsPageForSession）
- `core/interfaces.go`：TurnItemsSessionMapper 可选接口
- `go-bridge/`：projection_reducer.go（codex_goal→cmd turn 按 StartedAt 插入、turn_started 真实开始时刻、workflow_run 锚定）、turn_detail_batch_engine.go（会话感知映射）
- `agent/codex-remote/testdata/slash-panel/`：C0–C5 真实 Remote 脱敏样本 + plan-cold-live-2026-09-10.json
- 协议 pack / GO_BRIDGE_ARCHITECTURE.md 锚点表（含 codex 上游验证分支 `cordcode/plan-cold-verify` 登记）

**iOS（OpenCodeiOS + message-web）**
- `＋` 面板三项 typed native action（AttachMenuPlanner/ChatInputAccessoryView/CodexGoalCommandRouting）
- workflow 卡复用 dsh 槽位、goal bar 承载 running-agents 徽标、/goal claim 输入拦截
- MessageWebSnapshotBuilder：codex-remote reasoning blocks 官方对齐过滤（思考卡不渲染，详情可展开）
- compact 命令卡替代 alert；goal 卡定位/拆组合并（153bee70）
- 输入栏状态埋点（6f71af01，行为零变化）

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests
- Commands: `go test ./agent/codex-remote/ -count=1`；`go test ./go-bridge/ -count=1`（72s 全量）；collab/goal/reducer 定向套件
- Result: 全绿（50 todo 中 24 项在审计期重跑并记 re-verified）
- Attestation: `re-verified`（上述两包 2026-09-12 本会话内重跑）
- Main test files: `agent/codex-remote/collab_workflow_test.go`、`history_paginated_test.go`、`go-bridge/projection_reducer_goal_record_test.go`、`projection_reducer_workflow_test.go`、`go-bridge/*_test.go`
- Artifact paths: 本会话执行记录；`docs/2026-09-12-codex-goal-*-analysis.md`（含命令与结果摘录）

### 4.2 Regression evidence
- Device / replay / benchmark / manual validation: iPhone 16 Pro 真机四轮 goal 测试（2026-09-11~12）+ 2026-09-12 owner 最终验收（goal/plan/compact ✅；workflow 卡片正常；/goal 卡位置正常；思考行不出现 ✅）。断线韧性由 2026-09-12 17:00:38 真实 pairing 流断开重连事件实际演练。
- Attestation: `self-attested`（owner 手工观察，经会话转述记录；无 UI automation）
- Artifact paths: `docs/2026-09-11-codex-remote-goal-live-transcript-vanishes*.md`、`docs/2026-09-12-codex-goal-duplicate-thinking-and-dual-workflow-card-analysis.md`、`docs/2026-09-12-codex-goal-second-test-goalcard-position-and-frozen-workflow-analysis.md`、截图 `/Users/jacklee/Downloads/Scrollie_2026*.jpg`、`/Users/jacklee/Downloads/picture/2026-09-12 18.0*.jpg`

### 4.3 Delivery identity
- Mac: unsigned Release 脚本产物（内嵌 runtime commit `38ce43c52708`）→ `/Applications/CordCodeLink.app`；运行态五项核验（进程代际/8777 监听者/新符号/bridgeEpoch/driver lineup）。Attestation: `self-attested`（本会话执行并记录）。
- iOS: `plan/approval-layer-ios` 构建 → devicectl 安装 iPhone 16 Pro（org.openagi.cordcode）。Attestation: `self-attested`。
- 上游验证分支: `cordcode/plan-cold-verify` @ `4cb6eeae5d`（基于 9e868bd9，树与原 stash 逐字节一致；生产永不运行 patched codex）。

## 5. Deviations / Known Gaps (偏差与已知缺口)
1. owner 矩阵第⑥行未单独做双端并发测试（见 §1）。
2. 交付后遗留三项（切回 goal 卡丢失、composer 面板失效、collab 冷路径失明）已在分析文档建档，均为 follow-up，不阻断本案 required 门。
3. Review / Skills 按计划 §9 仍为「后续独立单元」，未实施、未广告——**本报告不声称全量 Codex slash 支持**。
4. 昵称缺口（P5.7 一期以序号+prompt 标注）维持计划内记录。

## 6. Rollback Semantics (回滚口径)
三项 capability（`context_compaction` / `session_collaboration_mode` / `session_goal`）可独立撤下菜单与写 handler；additive 解码保留。若任一 required 能力回滚，一期完成状态按计划 §12 恢复为未完成。
