# 监工审计报告 2 号（对应监工指令 2 号）

> **审计对象**：开发 agent 对监工指令 2 号的完成报告  
> **审计时间**：2026-08-20T14:58:03Z  
> **审计严格度**：严格（独立复跑 + grep 源码 + git show）  
> **Verdict**：partial

## 0. Audit Context

- 监工指令：`docs/supervisor-logs/2026-08-20-opencode-web-source-first-convergence-plan/directive-002-c1-diagnostics-hole-fill.md`
- 开发报告：`docs/supervisor-logs/2026-08-20-opencode-web-source-first-convergence-plan/report-002-c1-diagnostics-hole-fill.md`
- 设计文档 hash：`777591c8334a76987474ab6d52ee481606ce8a4c`，`design_doc_changed=false`
- 独立核实：`git show 5b8451c`、源码 grep、三个定向 diagnostics tests、真实 opencode-web descriptor test、package/bridge/core tests、vet/build。

## 1. Overall Verdict（总体结论）

diagnostics 的功能性缺口已修复：v2 现在产生 failed quarantine verdict 并在 catalog 前返回；1.18.18、unknown shape、server_unauthenticated 分类均正确，真实 descriptor 状态也为不可用。所有要求的测试与回归独立复跑通过。

但报告所称“`unsupportedGenerationDetail` 被 `InstanceStatus/clientFor/RunDiagnostics` 三处共享”与源码不符。helper 只被 `InstanceStatus` 和 `RunDiagnostics` 调用；`clientFor` 仍保留另一份硬编码错误串，并继续带旧的 `no capability` 表述。源码注释也错误声称三处共享。指令 2 的目的之一就是消除这三处漂移并修正 capability 口径，因此还需一个极小补洞后才能 verified。

## 2. 逐项核实矩阵

| 检查项 | 开发自述 | 监工核实方式 | 结论 |
|---|---|---|---|
| commit `5b8451c` | 5 文件、范围受控 | `git show --stat/name-only` | ✅ |
| v2 diagnostics failed | overall/row failed | 独立运行 focused test + 读 `diagnostics.go` | ✅ |
| v2 停在 probe | `/provider`/POST/SSE 为零 | 测试 request recorder 断言 | ✅ |
| 1.18 diagnostics | 仍 passed | `TestDiagnostics118StillPassed` | ✅ |
| unknown/no-auth 分类 | probe failure，非 quarantine | focused test | ✅ |
| descriptor 口径 | 非 available + quarantine reason | 真实 `opencodeweb.New` descriptor test | ✅ |
| 三处共享 helper | helper 覆盖三处 | `rg` + `opencodeweb.go:281-367` | ❌：`clientFor` 未调用 helper |
| capability 旧口径清理 | 不再声称数组为空 | grep 产品错误串 | ❌：`clientFor` 仍写 `no capability` |
| package tests | PASS | 独立复跑 | ✅ `2.474s` |
| bridge/core | PASS | 独立复跑 | ✅ `0.568s` / `0.030s` |
| vet/build | PASS | 独立复跑 | ✅ |
| protocol/WireDescriptor/iOS | 未改 | commit path/diff 核对 | ✅ |
| C2 | 未进入 | exec-plan/diff 核对 | ✅ |

## 3. 路线图偏离检查（八条判据）

| # | 判据 | 命中? | 证据 |
|---|---|---|---|
| 1 | 删除性测试 | 否 | 未恢复 v2 product path 或 fallback |
| 2 | 三环归属 | 否 | 仅 C1 diagnostics/control-plane |
| 3 | 待删清单特征 | 否 | 无阈值、特例裁判或双源比较 |
| 4 | 半接管最差状态 | 否 | 无 timeline writer 变化 |
| 5 | 覆盖面诚实 | **是** | 报告声明三处共享，但源码只有两处；旧 capability 口径仍存在 |
| 6 | 叙事真实性 | 否 | 未冒充 owner 真机完成 |
| 7 | 控制变量污染 | 否 | 单一 hole-fill commit，范围干净 |
| 8 | 根因锁死门 | 否 | 修复对应已复现的 diagnostics 分支 |

## 4. 驳回理由

不适用；功能行为已正确，本次为小范围 partial。

## 5. Partial 处理

- 路径：B——保持监工指令 2 号，不开新号；修正后写 `audit-002-recheck.md`。
- 唯一剩余动作：让 `clientFor` 使用同一 `unsupportedGenerationDetail` 语义，删除独立硬编码与旧 `no capability` 口径；更新依赖旧 substring 的测试，但不得弱化 fail-closed 断言。
- 重跑 agent package、focused diagnostics/descriptor、bridge/core、vet/build；仍不得进入 C2。

## 6. 给 owner 的下一步（产品语言）

- 让开发 agent 把最后一处错误文案接到同一判定函数即可；不需要真机操作。

## 7. Three-Track done 提醒

- exec-plan proven：yes（开发侧）
- 监工 verified：partial
- owner 真机矩阵 ✅：pending / 当前不要求
- 结论：diagnostics 行为已正确，但 C1 仍待同号 recheck，不能进入 C2。

