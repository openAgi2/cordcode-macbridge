# 本轮任务完成情况：dsh-web Backend 设计

## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-dsh-web`
- Plan: `docs/2026-08-16-dsh-web-backend-design.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge-dsh-web/.exec-plan/state/plan-a46e4391b790.json`
- Legacy State File: `.claude/exec-plans/plan-a46e4391b790.json`（兼容路径，未作双写）
- Completion Report Verdict: `proved-complete`
- Queue Summary: `30/30 todos done, 30/30 proven；tests 多为 audit 复跑 re-verified；真机回归 self-attested`
- Related Commits: Mac `09d0089`（合 main）；iOS `627c0e9`（合 main）。中间修复含 `6cd40cc`、`841cd92`、iOS `8b92322`
- Generated At: `2026-08-18T12:00:00+08:00`

## 1. Overall Verdict (总体结论)
设计 §8 八项拆分及后续两轮 review-fix 均已落地。自动化测试在 2026-08-16 audit 复跑通过；此前卡住的真机回归（流式行 2/3/4）经 pathless/kernel 与 iOS SSV2 列入 `deepSeekWeb` 后，owner 在 2026-08-16～18 多次真机回报通过，并于 2026-08-18 明确「全部 done，且测试通过」。

本报告由执行同一条队列的 agent 撰写。单测证据标 `re-verified`（audit 当时重跑）；真机矩阵标 `self-attested`（owner 手工，不能脚本重放）。产品显示名后续改为 DeepSeek Harness，不改变本计划 wire kind `deepseek-web`。

## 2. Phase Completion Matrix (阶段完成矩阵)
| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| s8-1 客户端/生命周期 | proven-done | proven-done | proven-done | proven-done | go test dsh-web；双实例 sandbox（re-verified / self-attested） |
| s8-2 RPC 映射 | proven-done | proven-done | proven-done | proven-done | rpcmap_test + 全量 go test（re-verified） |
| s8-3 WS 双流 | proven-done | proven-done | proven-done | proven-done | streams_test 重连/三态（re-verified） |
| s8-4 审批问答 | proven-done | proven-done | proven-done | proven-done | approvals_test（re-verified） |
| s8-5 投影接线 | proven-done | proven-done | proven-done | proven-done | TestDSHWeb*（re-verified） |
| s8-6 目录/workspace | proven-done | proven-done | proven-done | proven-done | workspace.list 测；list_directory 走通用 FS（self-attested 偏离） |
| s8-7 wire+iOS 枚举 | proven-done | proven-done | proven-done | proven-done | BridgeModelsTests 45/45；真机安装（re-verified / self-attested） |
| s8-8 Release+真机 | proven-done | proven-done | proven-done | proven-done | /Applications 安装 + owner 矩阵最终 ✅（self-attested） |
| review-fix / fix2 | proven-done | proven-done | proven-done | proven-done | kernel 优先级 + SSV2 列入；owner 复测通过（self-attested） |

## 3. Key File Changes (关键文件变更)
- `agent/dsh-web/`：官方 HTTP/WS 客户端、映射、codec、审批、上下文占用、预设
- `go-bridge/handlers_projection.go`：pathless 接线 + live/kernel 基线
- `MacBridge/.../RuntimeManager.swift`：默认 drivers 含 `dsh-web`
- iOS `BackendKind.deepSeekWeb` 穷举归组、SSV2 列入、消息页/列表产品化
- `docs/protocol/` canonical + iOS mirror

## 4. Verification Evidence (验证证据)
### 4.1 Automated tests
- Commands: `go test ./agent/dsh-web/ -count=1`；`go test ./go-bridge/ -run TestDSHWeb -count=1`；`xcodebuild test -only-testing:CCCodeTests/BridgeModelsTests`
- Result: 2026-08-16 audit 复跑 PASS（dsh-web 全绿；TestDSHWeb 6/6；BridgeModels 45/45 后增至 46/46）
- Attestation: `re-verified`（audit 当日重跑）；本报告日未再全量重跑
- Main test files: `agent/dsh-web/*_test.go`；`go-bridge/handlers_projection_dshweb_test.go`
- Artifact paths: `/tmp/dsh-web-audit-20260816/`（audit 当时）

### 4.2 Regression evidence
- Device / replay / benchmark / manual validation: owner 真机。列表/标题/分组；iOS 发消息打字机；Mac web 外部 turn 旁观；iOS 新建流式；ask 审批与问答（含先答者得）；Chat/工作区归组与「查看更多」；Exec plan 长会话投影；预设芯片
- Attestation: `self-attested`
- Artifact paths: Mac main `09d0089`；iOS main `627c0e9`；owner 口头矩阵回报 2026-08-16～18
- 既有环境泄漏：`agent/claudecode TestRunModelQueryDiagnostic_WarnsWithoutProbeConfig` 与本计划无关，未当作本队列失败

### 4.3 Audit downgrade summary
- Downgraded todos: 无（本报告日按 owner 指令将 `s8-8-release-regression` / 两轮 review-fix-regression 从 blocked/in_progress 收为 proven done）
- Why they were downgraded: n/a

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)
- §8-6 `list_directory` 实际走 bridge 通用本地 FS，未映射 `host.listDirectory`（实施注记已有）
- 活会话投影走 kernel 优先，与原稿「pathless 一律 history」有产品偏差，已用测试钉住
- 旧「加州笑话」未分组会话按官方规则保持未分组
- Claude/Grok/OpenCode 上下文圈不在本计划范围；OpenCode 顶层 `tokens` 常空是另一条线

## 6. Audit Focus (建议审核重点)
1. `s8-8-*-regression` 的证明是 owner 手工回报，不是脚本重跑
2. M4 投影接线五处是否仍全在（行号已漂移，按符号核对）
3. 产品改名 DeepSeek Harness 后 wire kind 是否仍为 `deepseek-web`

## 7. Constraints (关键约束)
- 不写 `~/.dsh/sessions` 与 `workspace.json`
- 不 import `agent/dsh`
- 真机 UI 自动化仍需 owner 授权；本队列真机验收为 owner 手工
- 禁止用临时 App 测 MacBridge
