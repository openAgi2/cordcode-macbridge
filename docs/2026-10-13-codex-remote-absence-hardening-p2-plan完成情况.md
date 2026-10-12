# 本轮任务完成情况：codex-remote 缺席语义加固（P2）

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`
- Plan: `docs/2026-10-13-codex-remote-absence-hardening-p2-plan.md`（v4，r4 定向验证 APPROVED 0/0 + 元审核 META-OK；owner 预授权过审即实施）
- Canonical State File: `.exec-plan/state/plan-aadb9fabf264.json`
- Legacy State File: none
- Completion Report Verdict: `proved-complete`
- Queue Summary: 15/15 todos done，15/15 proven；3 个 `tests` 类经出口审计重跑 `re-verified`
- Related Commits: `e51fbc3f`（P2 实施 + 13 个单测）；部署产物内嵌 runtime commit `e51fbc3fefd4`（运行中 pid 5318）
- Generated At: 2026-10-13T04:45:00+08:00

## 1. Overall Verdict (总体结论)

事故档案 §5.3 P2 三项全部实施、测试、部署并运行态核验，P0/P1/P2 三层修复至此闭环。运行态证据：watchdog armed 特征行启动即出（`pid=5318 period=5m0s childrenThreshold=64`）且健康静默（12 分钟零误报）；Management API 实测 codex-remote `status: available`（五态经 descriptor 全链路生效）；E-8 P1 口径复测通过（冷启动 1 探针 + 5s restored + ctrl 新鲜零刷新；12min 在线一次 lead 窗口刷新而探针恒 1；僵尸恒 0）。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| S-1 五态就绪 | proven-done | proven-done (re-verified) | n/a (justified: S-4 运行态) | complete | git:e51fbc3f；5 单测（分类 6 输入/五态映射含 fail-closed/失败即反映/成功清除/超时 transient） |
| S-2 重试预算 | proven-done | proven-done (re-verified) | n/a (justified: 未安装态本机 N/A) | complete | 4 单测（固定 120s 不推进/推进/翻转续接/var 可缩） |
| S-3 watchdog | proven-done | proven-done (re-verified) | n/a (justified: armed 行在 S-4) | complete | 4 单测（边界 7 组/确定性僵尸构造/armed+超标+失败存活/健康静默） |
| S-4 部署核验 | proven-done | n/a | proven-done | complete | 五项核验 + armed 行 + mgmt available + E-8（self-attested，档案 build/p2-online-watch.log） |
| S-5 文档 | proven-done | n/a (D0) | n/a (D0) | complete | 事故档案 §5.7、think.md、CHANGELOG、本报告 |

## 3. Key File Changes (关键文件变更)

- `agent/codex-remote/pairing_persist.go`：`classifyRestoreError`（四类缺席分类，文本匹配沿用 offlineMessageForRestoreError 既有模式）+ `restoreOnce` 包装器（restoreOnceLocked 承载原体；出口单一写入 lastRestoreClass：失败写/成功清）+ `restoreRetryWait`（notInstalled→固定 120s 不推进 backoff，双循环共用）+ 两处等待 Warn 带 `absence` 字段。
- `agent/codex-remote/pairing.go`：`lastRestoreClass` 字段。
- `agent/codex-remote/diagnostics.go`：五态映射（not_installed→not_detected、not_logged_in→not_logged_in、其余 fail-closed→service_not_running）+ `restoreAbsenceClass` 读取。
- `go-bridge/zombie_watchdog.go`（新）：armed 一次性特征行 + 5min ticker + `sampleChildProcesses`（ps 同步 Output）+ `zombieWatchdogAssess` 纯判定（任一僵尸或 children>64）+ 超标 WARN 不节流；`main.go` 接线。
- 新增测试：`readiness_states_test.go`（5）、`retry_budget_test.go`（4）、`zombie_watchdog_test.go`（4）。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests

- Commands: `go test ./agent/codex-remote/... -count=1`（ok 9.0s）；`go test ./go-bridge/ -count=1 -skip TestRelayEventsForwardsUserInputRequested`（ok 173s）
- Result: 全绿。**既有红测试披露**：`TestRelayEventsForwardsUserInputRequested` 在基线 535f447a / bc311b6c / e6a37c88 均红（多基线临时 worktree bisect 证明，引入于 2026-09-14 至 10-11 之间，与本轮无关）——已挂独立任务 task_5b253f6c；本轮以 -skip 验证其余全绿，不掩盖不冒充。
- Attestation: `re-verified`（出口审计重跑）
- 另：codex-remote stream_s3/s7 测试桩数据竞争为既有问题（task_4c4a511e，与本轮无关）。

### 4.2 Regression evidence

- 运行态（self-attested，不可重放）：五项核验（pid 5318 lstart 04:21:54 晚于构建 20:21:12Z=04:21:12 本地、8777=5318、无 /Applications 之外实例、-version=e51fbc3fefd4、runtime.json epoch dc58615b）；watchdog armed 行启动即出 + 12min 健康静默（0 WARN）；mgmt API codex-remote available；E-8：冷启动 1 auth_probe/5s restored/零刷新（ctrl 新鲜门控）+ 12min 在线 token_refreshed×1（04:29:20，lead 窗口节奏）而 auth_probe 恒 1 + 僵尸恒 0。未安装态本机不可构造（ChatGPT.app 在装）——五态缺席分支以单测为准，运行态 N/A 如实记录。
- Artifact paths: `build/p2-online-watch.log`

### 4.3 Audit downgrade summary

- Downgraded todos: 无。

## 5. Remaining Risks / Non-blocking Warnings (剩余风险)

- 卸载掩蔽窗口（方案 §6）：卸载落在 auth 缓存有效期内时 notInstalled 检测延迟到缓存失效；本机 autoRestartEnabled=0（owner 关闭兜底重启）下实践上界 = token exp（~96h 量级）；窗口内行为=现状非回归。
- not_logged_in 态维持 1–30s 探针节奏（每轮短命探针、P0 后均收尸；方案钉死的接受行为）。
- 两个既有测试问题挂独立任务：stream 桩数据竞争（task_4c4a511e）、relay 事件顺序红测试（task_5b253f6c）。

## 6. Audit Focus (建议审核重点)

1. `restoreOnce` 包装器的类别写入是否覆盖全部出口（含 connected 早退、revoked、bind 失败）；`restoreOnceLocked` 是否完整承载原体（无逻辑漂移）。
2. 双循环分流是否真的同规则（restorePersistedPairing 与 watchBinding 的失败分支对照）。
3. CHANGELOG 前提勘误（见下）：P1 收口时「不记 CHANGELOG」基于误归因删除（实际删除者非 owner、已 revert），本轮补记 P1+P2 一节——核对 2026-10-13 节内容与 §5.6/§5.7 对账数字一致。

## 7. Constraints (关键约束)

- 部署走正式路径；重启时机经确认（iOS 会话全 idle，深夜时段直接执行，符合预授权口径）。
- 五态全部复用既有 wire 枚举，零 iOS 代码改动（iOS 只读源码核验 BackendModels.swift:25-38）；protocol pack 未改。

## 附：CHANGELOG 前提勘误（2026-10-13）

P1 完成报告与事故档案 §5.5 曾记「owner 已于 2026-10-12 将 P0 同类条目从 CHANGELOG 移除，P1 不记」——该删除经 owner 否认且已被 revert（commit 7d147cdc→ff990f73），P0 条目实际在册；「不记」决定基于误归因前提。本轮按 P2 方案 r1 F-5（用户可见变化应记）补记 P1+P2 合并一节，勘误记录于此，不回改已归档的 P1 完成报告原文。
