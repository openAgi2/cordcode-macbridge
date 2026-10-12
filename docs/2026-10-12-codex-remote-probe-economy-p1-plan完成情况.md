# 本轮任务完成情况：codex-remote 恢复循环探针经济性优化（P1）

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-native-message-timeline`
- Plan: `docs/2026-10-12-codex-remote-probe-economy-p1-plan.md`（v3，r3 APPROVED 0/0 + 元审核 META-OK；owner 2026-10-13 全量授权实施）
- Canonical State File: `.exec-plan/state/plan-92731ca149b2.json`
- Legacy State File: none
- Completion Report Verdict: `proved-complete`
- Queue Summary: 15/15 todos done，15/15 proven；3 个 `tests` 类经出口审计重跑 `re-verified`
- Related Commits: `95cbb3e0`（P1 实施 + 16 个单测）；部署产物内嵌 runtime commit `95cbb3e0bb47`
- Generated At: 2026-10-13T02:40:00+08:00

## 1. Overall Verdict (总体结论)

事故档案 §5.2 P1 已实施、测试、部署并对账闭环。核心数字（runtime pid 75543）：**48 分钟 Desktop 缺席态全程仅 1 次探针 spawn**（冷启动；修复前 ~4.6 次/分钟 ≈ 220 个/48min），刷新降为官方 Proactive 节奏（≈每 5.3 分钟一次）；Desktop 重开后 backoff 内一次 restore 收口且探针零新增（r1 F-1 冷启动场景实机闭环）；在线态 22 分钟 `token_refreshed` +2（精确 9 分钟 = 600s−lead60s）而探针 +0（缓存服务在线刷新）；P0 僵尸回归恒 0。三个 tests todo 在出口审计中重跑通过。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| S-1 auth 缓存 | proven-done | proven-done (re-verified) | n/a (justified: S-4 统一对账) | complete | git:95cbb3e0；7 单测含 -race |
| S-2 刷新门控 | proven-done | proven-done (re-verified) | n/a (justified) | complete | 四态单测（F-1 冷启动/缓存/Proactive/env-401） |
| S-3 401 恢复 | proven-done | proven-done (re-verified) | n/a (justified: 真实撤销需账号操作) | complete | 四态+R-5 单测 |
| S-4 部署对账 | proven-done | n/a (justified) | proven-done | complete | 三段运行态对账（self-attested，档案 build/p1-*.log） |
| S-5 文档 | proven-done | n/a (D0) | n/a (D0) | complete | 事故档案 §5.6、think.md、本报告 |

## 3. Key File Changes (关键文件变更)

- `agent/codex-remote/pairing.go`：`authCache` 字段 + `chatGPTAuthCached`（命中不 spawn、失败不缓存 F-6、无 exp 失败驱动 F-7）+ `invalidateAuthCache`（撤销/env-401/恢复三源）；`refreshControlToken` 401/403 位点改 `errCtrlAuthRejected` + `refreshControlTokenWithRecovery`（恢复优先，二次拒绝才撤销）。
- `agent/codex-remote/pairing_persist.go`：`ctrlTokenRequirement` 三级门控（SKEW=300s 镜像 enroll.rs）+ restoreOnce「auth 每轮获取不受门控、仅刷新受门控」（F-1）+ `ctrlFresh` Warn 字段 + runCtrlTokenRefresh 走缓存与恢复。
- `agent/codex-remote/chatgpt_auth.go`：`authJWTExpiry` 顶层 exp 解析 + `auth_probe` Info 真实 spawn 计数（D4）。
- 新增测试：`auth_cache_test.go`（7）、`restore_gate_test.go`（4）、`recovery_test.go`（4+R-5）；`chatgpt_auth_reap_test.go` 超时预算 300ms→2s（负载敏感修复）。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests

- Commands: `go test ./agent/codex-remote/ -count=1`（工具链 go1.26.6 缓存路径）
- Result: 全包 4/4 连续绿（曾出现 1 次负载相关 flake：超时用例 300ms 预算不足，已修）；定向 S-1/S-2/S-3 全过；缓存并发 -race 过
- Attestation: `re-verified`（三个 tests todo 出口审计重跑）
- 既有问题登记：stream_s3/s7_test.go 测试桩自身存在数据竞争（与本改动无关，`git diff` 证明未触碰；已挂后台任务 task_4c4a511e 待修）

### 4.2 Regression evidence

- 三段运行态对账（self-attested，owner 配合，不可重放）：缺席 48min（auth_probe 恒 1 / token_refreshed 9 次 ≈5.3min 节奏 / no_desktop_env 全速复现 / 僵尸 0）；重开收口（02:14:56 restored，探针 +0）；在线 22min（+2 刷新 9min 节奏 / +0 探针 / pid 稳定）。
- Artifact paths: `build/p1-reconcile-75543.log`、`build/p1-online-observation.log`

### 4.3 Audit downgrade summary

- Downgraded todos: 无。

## 5. Remaining Risks / Non-blocking Warnings (剩余风险)

- enrollment 撤销检测延迟 ≤ Proactive 窗口 ~300s（r1 F-5 接受的故意差异，官方同款）。
- 401 恢复路径的生产触发需真实账号撤销（单测覆盖四态；生产以在线刷新正常滚动佐证不误撤销）。
- stream 测试桩既有数据竞争（见 4.1，后台任务在队列）。

## 6. Audit Focus (建议审核重点)

1. `chatGPTAuthCached` 命中条件（hasExp 与 expAt−60s margin）与失败不缓存（F-6）是否覆盖全部返回路径。
2. `refreshControlTokenWithRecovery` 的错误分流：仅 `errCtrlAuthRejected` 走恢复，非认证错误透传给调用方契约（F-2 saved-token 回退）。
3. 对账可复算性：log 中 `auth_probe`/`token_refreshed`/`no desktop environment` 计数与采样档案时间戳。

## 7. Constraints (关键约束)

- 部署走正式路径（Release 覆盖安装 `/Applications`，无临时产物混入）；重启时机经 owner 确认（避开 iOS timeline 真机测试窗口）。
- Desktop 退出/重开由 owner 手动执行；agent 未操控用户 GUI 应用。
- CHANGELOG 不记条目（owner 2026-10-12 裁决同类内部运维修复不进）。
