# 监工审计报告 2 号 Recheck（对应监工指令 2 号）

> **审计对象**：commit `257f0df` 对 audit-002 partial 的同号补洞  
> **审计时间**：2026-08-20T15:02:47Z  
> **审计严格度**：严格（独立复跑 + grep 源码 + git show）  
> **Verdict**：verified

## 0. Audit Context

- 原审计：`audit-002-c1-diagnostics-hole-fill.md`（partial）
- recheck commit：`257f0df`
- 设计文档 hash：`777591c8334a76987474ab6d52ee481606ce8a4c`，`design_doc_changed=false`
- 范围：`agent/opencode-web/opencodeweb.go` 加五个既有 test 文件，共 6 文件，`+15/-15`。

## 1. Overall Verdict

指令 2 的最后缺口已闭合。`clientFor`、`InstanceStatus`、`RunDiagnostics` 现在真实调用同一个 `unsupportedGenerationDetail`；旧 `unsupported/unverified generation` 和产品错误串中的 `no capability` 已删除。测试断言统一到更强的 `unsupported-generation (quarantined)` 标记，没有删除零写、零 SSE、零 Kernel 或 unknown 非 quarantine 的断言。

C1 的 supervisor 轨现可判 verified。此结论不等于 owner 真机 done；本阶段没有要求真机 UI 验收。

## 2. 逐项核实矩阵

| 检查项 | 核实方式 | 结论 |
|---|---|---|
| commit/path 范围 | `git show --stat/name-only 257f0df` | ✅ 6 文件，仅 adapter/tests |
| 三处共享 helper | `rg unsupportedGenerationDetail` | ✅ helper + 3 callers |
| 旧错误串删除 | `rg 'unsupported/unverified generation|no capability'` | ✅ 产品/测试旧串消失；仅架构注释保留 “no capability claim” 语义 |
| 14 处断言升级 | diff 逐项阅读 | ✅ 未弱化零副作用与分类断言 |
| agent package | 独立 `go test` | ✅ `2.467s` |
| focused C1 | 独立 `go test -run ...` | ✅ `0.015s` |
| go-bridge descriptor | 独立 `go test` | ✅ `0.628s` |
| core | 独立 `go test` | ✅ `0.036s` |
| vet/build | 独立复跑 | ✅ |
| protocol/WireDescriptor/iOS | commit diff + iOS status | ✅ 无改动 |
| C2 停止线 | exec-plan | ✅ 三项 pending/missing |
| 工作树 | `git status --short`（审计落盘前） | ✅ clean |

## 3. 路线图偏离检查（八条判据）

| # | 判据 | 命中? | 证据 |
|---|---|---|---|
| 1 | 删除性测试 | 否 | 未恢复 v2 写路径或 fallback |
| 2 | 三环归属 | 否 | C1 version/transport error boundary |
| 3 | 待删清单特征 | 否 | 无阈值、裁判或双源比较 |
| 4 | 半接管最差状态 | 否 | 无 timeline writer 变化 |
| 5 | 覆盖面诚实 | 否 | recheck 报告与源码、测试一致 |
| 6 | 叙事真实性 | 否 | 明确等待监工审计，未冒充真机 done |
| 7 | 控制变量污染 | 否 | 单一 6 文件措辞/断言 commit |
| 8 | 根因锁死门 | 否 | 对应 audit-002 已定位的单一硬编码 |

## 4. 驳回理由

不适用。

## 5. Partial 处理

原 partial 已通过同号路径 B recheck 升级为 verified，无剩余 hole。

## 6. 给 owner 的下一步

- C1 的监工轨已通过；可以按路线图另行下发 C2 指令。当前无需真机操作。

## 7. Three-Track done

- exec-plan proven：yes
- 监工 verified：verified
- owner 真机矩阵 ✅：pending / 本阶段未要求
- 结论：C1 的代码与证据轨完成；产品整体仍须后续 C2–C7 与最终 owner 验收。

