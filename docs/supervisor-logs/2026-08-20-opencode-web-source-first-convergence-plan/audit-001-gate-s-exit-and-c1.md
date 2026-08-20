# 监工审计报告 1 号（对应监工指令 1 号）

> **审计对象**：开发 agent 对监工指令 1 号的完成报告  
> **审计时间**：2026-08-20T14:45:12Z  
> **审计严格度**：严格（独立复跑 + grep 源码 + git show + 最小复现）  
> **Verdict**：partial

## 0. Audit Context

- 监工指令：`docs/supervisor-logs/2026-08-20-opencode-web-source-first-convergence-plan/directive-001-gate-s-exit-and-c1.md`
- 开发报告：`docs/supervisor-logs/2026-08-20-opencode-web-source-first-convergence-plan/report-001-gate-s-exit-and-c1.md`
- 设计文档：`docs/2026-08-20-opencode-web-source-first-convergence-plan.md` §6 C1
- 设计文档 hash：`777591c8334a76987474ab6d52ee481606ce8a4c`，`design_doc_changed=false`
- 独立核实：`git show`、源码/测试 grep、Gate B/S checkers、Go 定向测试/build/vet，以及隔离 `httptest` v2 diagnostics 最小复现。

## 1. Overall Verdict（总体结论）

Gate S exit 提交可验证，C1 的核心数据面闸口也成立：1.18.18 是 `clientFor` 唯一发放的 generation；v2 无 prompt、SSE 或 Kernel 输入；递归 catalog fallback 与 v2 写路径已删除；定向测试和回归独立复跑通过。

但 C1 尚不能判为 supervisor verified。S3 C1 impact record 明确要求 “unsupported generation status in diagnostics”，实际 `RunDiagnostics` 面对 v2 返回 `overall=passed`，并把 `ocw_probe` 标成 `passed generation=v2`，随后继续尝试 catalog。这违反计划 §6 C1 的错误状态区分验收。另一个报告口径问题是“零 capability”：真实 descriptor 在 `status=not_configured` 时仍携带现有 capability 列表；C1 的 capability advertisement 又被 S3 明确冻结，因此这里必须修正为可验证的“不新增 v2 capability、不可用状态阻止选择”，不能用当前测试声称 capability 数组为空。

## 2. 逐项核实矩阵

| 检查项 | 开发自述 | 监工核实方式 | 结论 |
|---|---|---|---|
| Gate S exit `bc4fcdd` | 独立 docs/checker 状态提交 | `git show --stat/name-only` + Gate B/S1–S4 checker/self-test | ✅ |
| C1 commit `5c82ecc` | 13 文件，产品范围仅 adapter | `git show --stat/name-only` | ✅ |
| 1.18.18 唯一数据面闸口 | `clientFor` 只发 generation118 | 读 `opencodeweb.go`，grep 20 个调用方，复跑 C1 tests | ✅ |
| v2 零 prompt/SSE/Kernel | fail closed | 读 `session.go/events.go`，复跑负向测试 | ✅ |
| 删除 v2 写路径与递归 fallback | 已删除 | diff `session.go/events.go/models.go/context_usage.go` | ✅ |
| transport 不制造 timeline | reconnect-only | 独立复跑 `TestSSEReconnectIsTransportOnly` 所在 package | ✅ |
| diagnostics 区分 unsupported | 已完成 | 读 `diagnostics.go:47-87` + v2 最小复现 | ❌：`overall=passed`, `ocw_probe=passed generation=v2` |
| capability claim | “零 capability” | 构建真实 `BuildAgentDescriptor` | ⚠️：`status=not_configured`，但 capability 数组非空；报告/测试口径过度 |
| agent package tests | PASS | `go test ./agent/opencode-web/ -count=1 -timeout 180s` | ✅ `2.484s` |
| bridge/core regression | PASS | 两条定向 `go test ... -timeout 180s` | ✅ `4.060s` / `0.028s` |
| build/vet | PASS | `go build ./...`; `go vet ./agent/opencode-web/` | ✅ |
| 工作树 | clean | `git status --short`（审计记录提交前） | ✅ |
| C2 | 未开始 | exec-plan C2 pending + diff inspection | ✅ |

## 3. 路线图偏离检查（八条判据）

| # | 判据 | 命中? | 证据 |
|---|---|---|---|
| 1 | 删除性测试 | 否 | v2 prompt/model/interrupt 与递归 parser 确实删除/封死 |
| 2 | 三环归属 | 否 | 改动属于计划 §6 C1 version/transport boundary |
| 3 | 待删清单特征 | 否 | 未新增阈值、裁判、双源比较或 fallback |
| 4 | 半接管最差状态 | 否 | 未新增 consumer referee 或第二 timeline writer |
| 5 | 覆盖面诚实 | **是** | 报告称 C1 完成，但 diagnostics 明确验收项未完成；“零 capability”也未被真实 descriptor 支持 |
| 6 | 叙事真实性 | 否 | 报告没有冒充 owner 真机完成，并明确停止等待审计 |
| 7 | 控制变量污染 | 否 | Gate S/C1 分离提交，tip clean，无跨阶段产品改动 |
| 8 | 根因锁死门 | 否 | C1 基于官方 commit、A1/A5 与 Gate A/B/S 证据实施 |

## 4. 驳回理由

不适用；本次为 partial，不是否定 C1 核心实现。

## 5. Partial 处理

- 未覆盖边界：v2/unsupported diagnostics 必须 fail closed；不得继续 catalog；需补 owning regression。
- 口径补洞：验证 unavailable backend 的 descriptor/选择门；不得把“status 不可用”写成“capability 数组为空”。S3 冻结 capability advertisement，本 hole-fill 不擅自改 WireDescriptor/capability 产品规则。
- 路径：A——新开监工指令 2 号 `hole-fill`；C2 继续禁止。

## 6. 给 owner 的下一步（产品语言）

- 先让开发 agent 补齐“不支持版本在诊断页必须明确报失败”这一处，再重新审计；当前不需要真机操作，也不能进入 C2。

## 7. Three-Track done 提醒

- exec-plan proven：yes（开发侧）
- 监工 verified：partial
- owner 真机矩阵 ✅：pending / 本阶段尚未要求
- 结论：Gate S 已过；C1 核心路径成立但仍有明确补洞，不能称 C1 done。

