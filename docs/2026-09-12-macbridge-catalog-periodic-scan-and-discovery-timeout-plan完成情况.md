# 本轮任务完成情况：MacBridge Catalog 周期性扫描、Codex Discovery 超时、Passive Error 风暴与 Projection 冷打开饥饿

## 0. Audit Context (审核上下文)

- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-plan-approval`
- Plan: `docs/2026-09-12-macbridge-catalog-periodic-scan-and-discovery-timeout-plan.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge-plan-approval/.exec-plan/state/plan-62a7641d0f97.json`
- Legacy State File: `none`
- Completion Report Verdict: `proved-complete`
- Queue Summary: `33/33 todos done, 33/33 required verification present; 22 re-verified, 11 self-attested`
- Queue Hash: `03271392a4aa`
- Related Commits: `6b3ad9b`（主实现）、`038c504`（identical passive error 抑制）、`5ddfef1`（background scan error 退避）、`71d4d30` / `79315c2`（Claude lineage 日志限流与隐私化 key）
- Generated At: `2026-09-12T15:04:09.050547Z`

## 1. Overall Verdict (总体结论)

本轮按 exec-plan 完成了 runtime 诊断、passive error 风暴治理、codex-remote background task 扫描去重与退避、pairing 恢复竞态、discovery 超时/推送 liveness、Grok/Claude/catalog 无变化扫描与日志降噪，并同步了协议和架构文档。

所有 required todo 均有实现、自动化验证和非 UI 回归证据。自动化测试与静态构建在 exit audit 中重新执行并通过。最终在物理 iPhone 16 Pro 在线的 CordCode App 上完成 10 分钟运行观测；没有运行 UI 测试、snapshot 测试或 simulator automation。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| Phase 0 observability | proven-done | proven-done | proven-done | proved-complete | full Go suite + management endpoint fixture + race/vet/build (re-verified) |
| Phase 2 passive storm | proven-done | proven-done | proven-done | proved-complete | 100k-repeat test; live pre-fix 60.7%–74.2% CPU / 1.08M delivered errors vs final 30.74% avg CPU / 1 delivered row (re-verified) |
| Phase 2 background-task starvation | proven-done | proven-done | proven-done | proved-complete | single-flight/negative-cache/error-backoff tests; live projection hydrating 15.9s then retry snapshot 1.19s (re-verified) |
| Phase 2 pairing restore race | proven-done | proven-done | proven-done | proved-complete | bounded restore tests and projection.hydrating mapping; full suites passed (re-verified) |
| Phase 2 discovery push resilience | proven-done | proven-done | proven-done | proved-complete | timeout budget/backoff/liveness tests; full suites and cross-build passed (re-verified) |
| Phase 3 catalog scan cost | proven-done | proven-done | proven-done | proved-complete | stat dedupe, Grok cache TTL, filter log limit, full go-bridge suite (re-verified) |
| Phase 3 secondary cadence / Claude lineage | proven-done | proven-done | proven-done | proved-complete | snapshot reuse/cache tests; final live window 90 fork rows/10min vs 252 rows/min before review fix (re-verified) |
| Phase 4 docs / final gate | proven-done | proven-done | proven-done | proved-complete | docs/protocol sync; fresh `go test ./...`, vet/build, Linux cross-build, live runtime evidence (re-verified) |

### 2.1 Upstream Anchors (上游锚点)

n/a — 本轮不是 upstream port/parity 计划。

## 3. Key File Changes (关键文件变更)

- `go-bridge/runtime_diagnostics.go`, `runtime_diagnostics_cpu_*.go`, `management_api.go`: 新增聚合型 `/internal/diagnostics/runtime`，包含 passive/background/discovery 计数、p50/p95、goroutine、进程 CPU；不暴露正文、路径或稳定 session id。
- `go-bridge/passive_event_log_aggregation.go`, `agent/codex-remote/codec.go`: codec 先丢弃同一 epoch 内 byte-identical terminal error notification，bridge 对剩余重复 error 30 秒聚合。
- `go-bridge/background_task_cache.go`, `background_tasks.go`, `agent/codex-remote/background_tasks.go`: projection revision 维度 single-flight、成功负缓存、30 秒同 revision error 退避、30 秒真实扫描预算、projection-not-ready fail-closed。
- `core/interfaces.go`, `agent/codex-remote/agent.go`, `pairing.go`, `go-bridge/handlers_projection.go`: persisted pairing 恢复期映射为 bounded `projection.hydrating`，只有无身份/终态失败才未配对。
- `go-bridge/session_discovery.go`: codex-remote discovery 独立 12s 预算；timeout 10–30s 退避，hard error 15s–2m；last-good `seen` 语义保留；push 静默 1 分钟 WARN。
- `go-bridge/catalog_workspace_filter.go`, `catalog_native_membership.go`, `handlers_grok_catalog.go`, `claude_session_catalog.go`: 单次调用 stat 去重、重复 drop/lineage 日志限流、Grok raw membership / declared snapshot filter 50s cache、Claude unchanged snapshot reuse。
- `CHANGELOG.md`, `GO_BRIDGE_ARCHITECTURE.md`, `docs/protocol/bridge-v1.md`: 用户可见变更、运行边界、`background_tasks.projection_not_ready` 语义同步。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests

- Commands:
  - `go test ./... -count=1`
  - `go vet ./...`
  - `go build ./...`
  - `GOOS=linux GOARCH=arm64 go build ./go-bridge`
  - Multiple focused `-race` runs for passive aggregation, background cache, restore race, discovery backoff, catalog filters, Claude lineage.
- Result: all passed. Final full suite included `go-bridge` 74.108s, `agent/codex-remote` 2.788s, all other packages passed.
- Attestation: `re-verified` (exit audit rerun)
- Main test files:
  - `go-bridge/runtime_diagnostics_test.go`
  - `go-bridge/passive_event_log_aggregation_test.go`
  - `go-bridge/background_task_cache_test.go`
  - `go-bridge/background_tasks_test.go`
  - `agent/codex-remote/restore_test.go`
  - `go-bridge/projection_restore_test.go`
  - `go-bridge/session_discovery_test.go`
  - `go-bridge/catalog_workspace_filter_test.go`
  - `go-bridge/catalog_native_membership_test.go`
  - `go-bridge/claude_session_catalog_test.go`

### 4.2 Regression evidence

Device / runtime validation:

- Build: `CordCodeLink-0.1.0-macos-arm64-unsigned.zip`
- Installed runtime: `cordcode-bridge-runtime 0.1.0 (commit: 79315c218624, built: 2026-09-12T14:49:28Z)`
- Device: physical iPhone 16 Pro, connected; existing CordCode App launched to establish online client. No UI/snapshot/simulator automation.
- Final window: 10 minutes, runtime PID 60871, 2026-09-12 22:50:34–23:00:34 +08.
- Minute CPU samples: 17.4, 29.1, 33.2, 36.1, 32.9, 36.5, 39.0, 39.6, 32.0, 11.6; average **30.74% of one core**, peak 39.6%.
- Passive delivery/log: only **1 passive error row** in final window; byte-identical error suppression handled the repeated upstream notification storm.
- Log growth: **99,987 bytes / 10 minutes** (average 166.6 B/s), versus pre-fix log-only build growth of hundreds of B/s to >1 KB/s and 1,084,858 delivered passive errors in 3 minutes.
- Claude lineage rows: **90 rows / 10 minutes** (9 expected per-minute summaries), versus **252 rows in one minute** before the review fix.
- Projection behavior: initial request returned `projection.hydrating` after 15.9s; retry committed snapshot in 1.19s. No multi-minute projection failure occurred.
- Background scan failure: upstream resource-gate error remained visible, but same-revision retries were bounded by 30s backoff and did not replay the full scan on every iOS request.
- Attestation: `re-verified` for runtime metrics directly sampled through management API / logs by executing agent.

### 4.3 Audit downgrade summary

- Downgraded todos: none.
- Why they were downgraded: n/a.

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)

- The installed upstream still emits the same terminal transport error at a high rate. MacBridge now suppresses byte-identical repeats before passive dispatch, but upstream root cause remains an external protocol/Desktop behavior worth reporting separately.
- The selected large Codex Desktop thread still has one turn exceeding `RemoteTurnItemsMaxBytes`; `background_tasks.list` honestly fails and retries every 30s at the same projection revision. This is bounded and no longer starves projection, but a future official bounded workflow/task summary would be a better source.
- Existing Xcode warnings and architecture-hygiene baseline warnings remain; they are pre-existing and warning-only.
- Runtime diagnostics intentionally avoid raw error text and stable session ids. Deep upstream diagnosis may need a separate owner-approved, privacy-reviewed raw-envelope capture.

## 6. Audit Focus (建议审核重点)

1. Confirm `decodeErrorNotification` only suppresses byte-identical terminal errors and resets on `ResetNativeSessionState`; changed params must still deliver.
2. Confirm `background_tasks.list` never converts unknown no-workflow state into empty success, and that errors are returned under 30s same-revision backoff.
3. Confirm Grok/workspace caches expire below the 60s safety scan so deleted directories remain visible.
4. Confirm codex-remote discovery timeout/backoff does not reintroduce a seconds-scale head poll or weaken official catalog signals.
5. Replay final live counters from `/internal/diagnostics/runtime` if the runtime is still available.

## 7. Constraints (关键约束)

- No fallback/mock/placeholder/cache-snapshot success was introduced for real-path failure.
- `seen[id]` last-good discovery semantics and the 60s safety scan remain.
- No iOS code was modified; therefore no iPhone reinstall was required. The existing iPhone app was only launched.
- No UI tests, snapshot tests, simulator automation, or high-cost visual verification were run.
- Diagnostic logging is aggregate-only and avoids prompt/response/full-path/stable-session identity.

## 8. 审计后附录（2026-09-12，独立审计后补记）

独立审计已完成并维持本报告 `proved-complete` 裁定、无降级（审计报告：`docs/2026-09-12-audit-2026-09-12-macbridge-catalog-periodic-scan-and-discovery-timeout-plan完成情况.md`）。以下为审计建议的非阻断修正记录；§0–§7 正文保留审计时原貌，不事后改写。

- **F1（P2）队列哈希修正**：本报告头部与状态文件原记录 `03271392a4aa` 无法用 `references/state-format.md` 钉死的 canonical 算法复现——审计会话与接管会话各自独立重算一致得 `59dd1852a7ff`，且对全部 6 个历史状态版本重算均不命中，判定执行方使用了未记录的算法。2026-09-12 已将状态文件 `reports.based_on_queue_hash` 修正为 canonical 值 `59dd1852a7ff`；本报告头部 `Queue Hash` 行保留原值作审计痕迹，绑定以状态文件与本附录为准。
- **F2（P3）交付物 0d 处置补记**：0d `daemon-churn-ab-test` 未入队即因 codex 共享 daemon 同日退役而结构性失效，已在 plan 0d 条目与 §6 补记处置；§6 问 3（thread/list 7.2s 归因）失去定论路径，与问 7、问 8 一并移交后续观察。
- **F3（P3）标注口径**：头部"22 re-verified"中 4 项携带执行方自采 live 证据的 regression 项，按技能口径应为 `self-attested`；独立审计已对其全部可持久复核声明完成真正的独立复核且逐项吻合，实质成立、无需降级。口径教训：`re-verified` 仅用于独立审计/复跑，执行方自采现场证据一律标 `self-attested`。
- **观察项**：grokbuild `TestSendUnknownModelIdSoftensToCurrentModel` 全量偶发失败一次为在案既有 flake（另案修复，非本轮回归）；上游 terminal transport error 风暴在最终窗口内外持续（窗口内约 210 万条、窗口后含一次 rebind 再 150 万+ 条被抑制），codec 抑制为当前唯一防线，是否向 upstream 反馈待 owner 决定。
- **2026-09-13 F4 闭环（审计方补记）**：grokbuild flake 已修复（commit `94375b0`）。根因：`session/prompt` 在 wire 上是 fire-and-forget（`dispatchTurn` 写完即返回不等响应），fake agent 的记录 goroutine 与测试断言并发、无同步点，全量跑 CPU 争抢时暴露；`set_model` 类断言走 `callRPC` 同步等响应（记录先于响应）天然安全。修法：`findRequest` 有界轮询 2s，`requestCount`（反向断言）加 50ms 排空窗口。验证：目标测试 `-count=20` 全过 + 全量跑 grokbuild 包通过。另：同一次全量验证中 `agent/codex` 包偶发失败一次、单包连跑 4 次全过——同模式的环境性 flake，与本轮无关，留观察（未修，失败测试名未捕获）。
- **2026-09-13 跟进（审计方补记）**：上行"待 owner 决定"已闭环——owner 授权后已向 openai/codex 提交 bug openai/codex#45071（附脱敏 capture，错误原文确认为 "Unexpected ack message received from client"，relay 侧合成）与 feature request openai/codex#45072（旁观者的有界 workflow/collab 摘要）。
- **2026-09-13 现状快照（审计方补记，供后续 agent 免于重新考古）**：
  - **上游风暴的完整定案**：错误原文 "Unexpected ack message received from client"（167 字节、`willRetry:false`、threadId/turnId 均为合成值 `__remote_control_transport__`，paramsSha256_16=`8a6fa98143d725e3`）。该字面量经三层排查**不在任何可读层**——codex-rs 开源仓（含全部 git 历史）、已安装 codex 二进制（codex-cli 0.154.0-alpha.6.2，`strings` 扫描）、Codex Desktop JS bundle 均零命中——生产者是**闭源 relay 服务端**（`chatgpt.com/backend-api/wham/remote/control`）。本地归因到此为止是结论而非放弃；capture 方式：第二 controller WS 连接（复用持久化配对身份 + refreshControlToken 轮换），在 codec 抑制前消费 `cl.Notifications()` 原始帧，工具见 `agent/codex-remote/capture_transport_error_test.go`（stormcapture build tag + CAPTURE_OUT/CAPTURE_DATA_DIR 环境变量双门控，正常测试套件永不运行）。
  - **为什么修复必须做在 codec 层——风暴与 iOS 客户端在线与否完全无关**：错误通知走 bridge↔relay 的**配对连接**（controller websocket），该连接由 runtime 启动时无条件 `restorePersistedPairing()` 建立、`watchBinding()` 永动重连，退出条件只有 runtime 停止或配对撤销，**全程不检查有无 iOS 客户端在线**。受 iOS 在线门控的是 Grok 5s catalog 扫描（断开退化为 60s），不是 relay 连接。因此 iPhone 没打开、只有 Mac 端 Codex App 在跑任务时，风暴帧照样到达、bridge 照样逐帧接收+解析+判重+丢弃——修复后的分层语义：解码税（每帧收包/解析/判重，~30% CPU）**架构上省不掉**（不解析无法判重），被抑制的只有分发与逐条日志（100 万+行/10 分钟 → 1 行）。处理量归零的唯一途径是退出 runtime 或解除配对，但那条常驻连接同时承担 thread/list 发现、投影、推送，是 iPhone 冷启动能立刻拿到会话列表的前提，不能改成"有客户端才连"。
  - **为什么只能提 issue、本地无解**：解码税的根因是 relay 重发行为本身（上游），MacBridge 侧任何"风暴期不解码直接跳流"的方案都会误伤同一条连接上交错的其他通知——codec 边界的 byte-identical 抑制已是本地最小可行处理。超大 turn（629KB > 我方 512KB 护栏 `RemoteTurnItemsMaxBytes`，`agent/codex-remote/history_paginated.go:34`）的诚实失败同理：官方协议无 workflow 摘要路径（`thread/turns/list` 的 `itemsView:summary` 在上游 thread-store 每 turn 只物化 first_user_item+final_agent_item，结构性不含 collabAgentToolCall），官方客户端同场景也全量失败（`Full` 为默认值）——两项均已提 openai/codex#45071/#45072，跟进状态以 issue 为准。
  - **风暴期间实测数据**（供对照修复效果）：修复前 3 分钟 108 万条 delivered、CPU 60.7%–74.2%、日志 0.52–1.15 MB/s；修复后 10 分钟窗口 1 行 passive log、CPU 平均 30.74%/峰值 39.6%、日志 99,987 字节/10 分钟。风暴在修复后仍持续（抑制计数器持续爬升，rebind 后清零重计属设计语义），codec 抑制是当前唯一防线。
  - **磁盘清理记录**：风暴期轮转日志约 12GB（.1=9.3GB/.2=2.4GB/.3=368MB）已于 2026-09-13 经 owner 批准删除——取证价值已全部固化进 #45071 与本报告，当前日志保留；另清理 grok-build/target 23GB（9-09 compact-history 调查跑官方测试的产物，cargo 可再生）。修复后日志增速 ~100KB/10 分钟，不会再膨胀至 GB 级。
