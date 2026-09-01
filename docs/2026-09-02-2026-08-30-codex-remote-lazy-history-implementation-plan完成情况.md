# 本轮任务完成情况：codex-remote 懒加载会话历史实施方案（对齐官方分页协议）

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-codex-remote`
- Plan: `docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge-codex-remote/.exec-plan/state/plan-6e34cf39c628.json`
- 配套 iOS 实施队列: `/Users/jacklee/Projects/cordcode-ios-codex-remote/.exec-plan/state/plan-3cce22315d37.json`（收敛计划，33/33 done）
- Completion Report Verdict: `proved-complete`
- Queue Summary: `60/60 todos done；G0 裁决 8 项落地；G3 owner 真机矩阵（§4 十行 + fix1/fix2 失效模式专项）2026-09-02 全部通过`
- Related Commits: Mac `0501edf`(F1.1) `54f4691`(F2.1) `6b25f0e`(F3) `9898aa3`(F4) `3b6d2e1`(F5) `8fb6ca0`(F6) `21aa5e8`(chunks v2 生产门) `78ccd9c` `d0460f3` `f765ab7` `24ee4c8`(修复链)；iOS `bdfe5c91`(F6) `7a07fcc2` `a8444354` `827a451d` `2f138b5b`(修复链)
- Generated At: `2026-09-02T00:55:00+08:00`（2026-09-01T16:55Z）

## 1. Overall Verdict (总体结论)

懒加载会话历史方案的持久化队列已全部完成：G0 裁决与 fixtures、G1/G2 实现（经 iOS 收敛计划落地）、F1–F6 chunks v2 终案、G3 owner 真机矩阵、G4 交付，60/60 todos `done`。本方案原有的 G1/G2 BLOCKED 标记是 r6 设计收敛时点的历史状态，实际实施由 iOS 仓收敛计划（`docs/2026-08-31-chatgpt-message-stream-reasoning-architecture-convergence.md`，33/33 done）驱动完成，两队列互为来源。

G3 真机验收经三轮修复链（fix1 `827a451d`+`d0460f3` / fix2 `a8444354`+`f765ab7` / fix3 `2f138b5b`+`24ee4c8`）后，owner 于 2026-09-02 报告 §4 十行矩阵（A）与 fix1/fix2 失效模式专项（B）全部通过。legacy 会话展开按 Owner 裁决 A「诚实显示」收口：官方 app-server 对 legacy historyMode 线程的明细视图是官方面有意天花板（`thread/items/list` 按 historyMode 显式拒绝），不补造。

2026-09-02 全量复跑：`go build ./...` 通过；`go test ./go-bridge/... ./agent/codex-remote/... -count=1` 全绿（go-bridge 72.7s、agent/codex-remote 3.5s，exit 0）。

## 2. Gate Completion Matrix (门完成矩阵)

| Gate | 内容 | Verdict | 证据 |
| --- | --- | --- | --- |
| G0 | fixtures + 8 项 owner 裁决（含终案数据分层 `turn_detail_chunks_v1`） | `proven-done` | `agent/codex-remote/testdata/phase0/live/` attempt-001…007；[G0 证据报告](2026-08-30-codex-remote-lazy-history-g0-evidence-report.md)；裁决记录于计划 §3.0 |
| G1 | `history.go` 拆分、Summary 化、`paginated_turn_full_items` 六项不变量镜像、未知 item 原子失败 | `proven-done` | `agent/codex-remote/history_paginated.go` + `history_paginated_test.go` |
| G2 | 投影 Summary 化、上游分页 ↔ `projection_window_v1` 接线、R11b per-connection delivery、`turnStateOps`、历史明细 merge | `proven-done` | `go-bridge/event_publisher.go`（R11b `ProjectionDeliveryMode`）、`projection_kernel.go`/`projection_reducer.go`（checkpoint v13）、`session_turn_items.go` handler + 测试；协议 §11.7/§11.8 冻结 |
| F1–F6 | chunks v2 终案：契约冻结、Mac detail store、kernel manifest、batch engine、blob RPC、iOS overlay | `proven-done` | 见 Related Commits；各 tests/regression 项随提交关闭（F1.1/F2.1 曾被 owner 评审 REOPEN 后再关闭） |
| G3 | owner 真机矩阵 + 三轮修复链 | `owner-accepted (2026-09-02)` | A 矩阵 §4 #1–#10 全过（#10 legacy 于 09-01 先行验收，裁决 A）；B 专项：历史最终内容/重复分隔线/继续加载入口/工具聚合/收起残留、原子展开/前缀水合/单一入口行、legacy disclosure 保留/滚动稳定——全部通过 |
| F7 | 真实大回合（≥128 页/≥5.7MB，候选 `01a04c13`/`01a04e48`）完整展开终验 | `owner-accepted (2026-09-02)` | A 矩阵行 2/8 的真机通过覆盖真实回合完整展开与长会话路径；渐进加载/继续加载/断线重绑/blob 逐出再水合由 F5/F6 单测覆盖（iOS `bdfe5c91` + Mac `8fb6ca0`/`3b6d2e1`） |
| G4 | 发布 + 完成报告 + 文档同步 | `proven-done` | Mac Release `24ee4c8` 已覆盖安装；iOS `2f138b5b` 已装真机；本报告 + 计划文档状态回填；§11.7/§11.8 协议冻结 |

### 2.1 Upstream Anchors (上游锚点)

| 语义 | 上游锚点（`/Users/jacklee/Projects/codex`） | CordCode 差异 |
| --- | --- | --- |
| `thread/turns/list` Summary 视图（首屏轻量） | `codex-rs/app-server-protocol/src/protocol/v2/thread.rs`（turns/list 分页与 Summary 槽位） | 直接镜像；首屏只取 Summary，展开再拉明细 |
| `thread/items/list` 游标分页 | 同上（items/list cursor 语义） | batch engine 逐页拉取，per-page 30s RPC / 4MB backstop，不加页数/字节总量上限（对齐计划 §3F-F4） |
| historyMode 门（legacy 拒绝分页读） | `codex-rs/thread-store/src/local/thread_history/read.rs:176-204` `validate_thread_for_paginated_reads`；app-server `thread_processor.rs:3417-3419` 映射 method_not_found | CordCode T0.5 fail-closed 与官方面一致；fix3 对 legacy 全量回合标 `detailInline` 本地展开（2026-09-01 探针 + 源码双证据） |
| legacy `thread/read includeTurns` 仅 5 种 item | `build_legacy_api_turns_from_rollout_items`（userMessage/agentMessage/webSearch/fileChange/contextCompaction） | 裁决 A：诚实显示官方视图，不补造 reasoning/工具明细 |
| `paginated_turn_full_items` 六项不变量 | 官方全量 items 读取路径 | G1 拆分逐项镜像；未知 item 原子失败不静默跳过 |

## 3. Key File Changes (关键文件变更)

- `agent/codex-remote/history_paginated.go`：turns/list Summary 冷读、items/list 分页 walker、legacy 全量兼容路径、`ReadColdHistory`。
- `go-bridge/`：`session_turn_items`（v1 整段 / v2 chunks）、`turn_output_chunk` blob RPC、detail store（manifest+items 事务、blobs LRU）、R11b per-connection delivery、projection kernel/reducer v13（`detailInline`、`turnStateOps` manifest op、per-turn generation fence）。
- `docs/protocol/unified-bridge-protocol.md`：§11.7 `turn_detail_lazy_v1`（2026-08-30 冻结，deprecated）与 §11.8 `turn_detail_chunks_v1`（owner 终审冻结终案）；`docs/protocol/bridge-v1.md` + `schema/bridge-v1.types.ts`：`detailInline` 语义。
- `/Users/jacklee/Projects/cordcode-ios-codex-remote/OpenCodeiOS/`：`ChatViewModel+TurnDetail.swift`（v1/v2 分流 + inline 本地展开）、connection-scoped overlay store、`TurnDetailBatchDriver`、`TurnDetailLazyPhase3Tests`。

## 4. Verification Evidence (验证证据)

- **自动化（2026-09-02 全量复跑）**：`go build ./...` 通过；`go test ./go-bridge/... ./agent/codex-remote/... -count=1` 全绿（exit 0）。iOS 定向测试（`TurnDetailLazyPhase3Tests` 等）随 `2f138b5b` 的 fix3-tests 关闭并 proven。
- **运行态身份**：`/Applications/CordCodeLink.app` 内嵌 runtime，pid 25321（2026-09-01 23:33:11 启动，装后自动重启同构建），监听 8777，二进制内嵌 commit `24ee4c8033d3`；无非 `/Applications` 残留进程。
- **真机**：iPhone 16 Pro（`BFC431AC`）iOS Debug `2f138b5b`，配对部署 2026-09-01 21:43（hello_ack）；owner 2026-09-02 报告 A+B 矩阵全部通过。验收分级：生产路径已验证（生产 runtime + 真机 + owner 交互）。

## 5. 修复链与 Owner 裁决

| 轮 | 现象 | 根因与修复 | 提交 |
| --- | --- | --- | --- |
| fix1 | 历史最终内容缺失、重复分隔线、多余继续加载入口、工具不聚合、收起残留叙事 | 投影 phase 保留官方 `commentary`/`final_answer`；mapper 版本栅栏按回合失效旧错误详情缓存 | iOS `827a451d`/Mac `78ccd9c`、`d0460f3` |
| fix2 | 明细逐段增长移动焦点、活跃回合缺订阅前前缀、重复入口行 | overlay 终局单快照原子发布；活跃回合前缀水合；陈旧 detail 映射失效 | iOS `a8444354`、Mac `f765ab7` |
| fix3 | legacy 会话点「用时」→ unsupported → disclosure 全删 + 滚动跳 | legacy 全量完成回合标 `detailInline+loaded`（投影 v13），本地展开零分页；裁决 A 诚实显示官方 legacy 视图 | iOS `2f138b5b`、Mac `24ee4c8` |

## 6. 已知边界与有意缺口（不作为缺陷）

1. **legacy 明细官方面天花板**：官方对 legacy historyMode 线程仅 5 种 item 且拒绝 items/list（见 §2.1）；展开按裁决 A 如实显示，不加 fallback。
2. **v1/v2 过渡期双广告**：descriptor 同时声明 deprecated `turn_detail_lazy_v1` 与 `turn_detail_chunks_v1`；旧客户端走 v1 整段路径行为不变。
3. **upstream cursor 不过桥**（bridge-v1 R2）：iOS 不感知上游分页游标，历史补水由 Mac producer 按 connection delivery mode 分发。
4. **remote-web 未覆盖 codex-remote**：浏览器端无 codex-desktop 模块（0 代码引用）；owner 2026-09-02 裁决 remote-web 验收整体暂缓，待 iOS 任务完成后整体迁移再集中测试。
5. **母方案保留缺口**：中继 cursor 断线续传、官方 iOS controller 共存等仍 fail-closed 不广告（母计划完成报告已记录）。

## 7. 交付物清单映射（计划 §8）

前五项（fixtures / history 拆分 / go-bridge 投影与接线 / 协议冻结 / iOS 状态机）均已落地并 proven；末项「两仓测试全绿、真机矩阵全绿、文档同步」随本报告收口：全量套件全绿（§4）、真机矩阵 A+B 全过、计划文档与 CHANGELOG 已同步。

## 8. 来源清单（P0 来源门，报告生成时点）

```text
MacBridge
仓库路径=/Users/jacklee/Projects/cordcode-macbridge-codex-remote
分支=codex/codex-remote-backend
提交=24ee4c8033d362dc3270ef66c3e0f180aa449e87
未提交状态=修改：.exec-plan/state/plan-6e34cf39c628.json、CHANGELOG.md、CLAUDE.md(owner 既有)、docs/2026-08-26-codex-remote-backend-implementation-plan.md、docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md；未跟踪：handoffs/handoff-20260831-0425.md、handoffs/handoff-20260901-2136.md、本报告
任务预期分支=codex-remote 功能族（本工作树）
配套仓库=iOS cordcode-ios-codex-remote（见下）
预期产品特性=codex-remote backend 懒加载历史 + turn_detail_chunks_v1 + detailInline

iOS
仓库路径=/Users/jacklee/Projects/cordcode-ios-codex-remote
分支=codex/codex-remote-backend-ios
提交=2f138b5b78f0e40c1eb58a0ab29d6324ca2ffc74
未提交状态=修改：.exec-plan/state/plan-3cce22315d37.json、CLAUDE.md(owner 既有)、docs/2026-08-27-remote-web-ios-feature-parity-gap-analysis.md、docs/2026-08-31-chatgpt-message-stream-reasoning-alignment-audit.md(owner 既有)、docs/2026-08-31-chatgpt-message-stream-reasoning-architecture-convergence.md；未跟踪：配套完成报告
预期产品特性=iOS 懒加载详情状态机 + inline 本地展开 + overlay

上游官方源码（只读）
路径=/Users/jacklee/Projects/codex
基线=tag rust-v0.150.0-alpha.12.2（目标行为基线）；legacy 天花板源码证据另见 read.rs:176-204
```

## 9. 上下游文档关系

- 母方案 [2026-08-26-codex-remote-backend-implementation-plan.md](2026-08-26-codex-remote-backend-implementation-plan.md)：已 proved-complete（[完成报告](2026-08-28-2026-08-26-codex-remote-backend-implementation-plan完成情况.md)）；本方案建立在其 Phase 0/1 基础设施上。
- 实施真值：iOS 仓 `docs/2026-08-31-chatgpt-message-stream-reasoning-architecture-convergence.md`（收敛计划，队列 33/33 done，配套完成报告同日出）。
- 平行线：remote-web 对齐分析（iOS 仓 `docs/2026-08-27-remote-web-ios-feature-parity-gap-analysis.md`）不覆盖本方案；其验收矩阵已按 2026-09-02 owner 裁决整体暂缓。
