# 本轮任务完成情况：codex-remote Backend 实施方案（ChatGPT Desktop Remote Control 接力）

## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-codex-remote`
- Plan: `docs/2026-08-26-codex-remote-backend-implementation-plan.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge-codex-remote/.exec-plan/state/plan-bb4683ae3ec1.json`
- Legacy State File: `none`
- Completion Report Verdict: `proved-complete`
- Queue Summary: `111/111 todos done, 111/111 proven; 31 test/script records re-verified, 80 records self-attested`
- Related Commits: Mac `ebdcd68`, `71ef328`, `9c30ca2`, `b0ac765`, `c0e26fe`; iOS `5565a86`
- Generated At: `2026-08-29T16:57:13Z`

## 1. Overall Verdict (总体结论)

本计划的持久化队列已完成：所有 required `impl + tests + regression` triplet 均为 `done` 且 verification 为 `present`。此前因 Remote 目录自激、iOS 变更可见延迟、项目目录/标题不一致而失效的 Gate P5 已由对应 review-fix 链修复并重新取证。

Mac Release runtime 已从 `c0e26fe` 构建并覆盖安装；Remote 目录改为官方事件触发、60 秒安全扫描，且无在线 v2 观察者时不再构造大整回合 patch。当前证据中 31 项可重跑测试/脚本为 `re-verified`，其余为明确标注的 self-attested 设备/历史证据；这不是把手工设备观察冒充独立复核。

## 2. Phase Completion Matrix (阶段完成矩阵)
| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| Phase 0 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 11 re-verified / 19 self-attested |
| Phase 1 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 5 re-verified / 10 self-attested |
| Phase 2 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 4 re-verified / 8 self-attested |
| Phase 3 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 4 re-verified / 8 self-attested |
| Phase 4 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 3 re-verified / 6 self-attested |
| Phase 5 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 4 re-verified / 29 self-attested |

### 2.1 Upstream Anchors (上游锚点)
| Fix / todo | Upstream anchor (file:line) or exemption card | First divergence vs upstream |
| --- | --- | --- |
| Controller enroll/refresh/pair wire contract | `/Users/jacklee/Projects/codex/codex-rs/app-server-transport/src/transport/remote_control/protocol.rs:130`; `enroll.rs:48` | 上游实现 Desktop host；本仓实现经 fixture 冻结的独立 controller，并保留自己的凭据与设备身份。 |
| Envelope、ACK/cursor 与 reconnect | `/Users/jacklee/Projects/codex/codex-rs/app-server-transport/src/transport/remote_control/websocket.rs:74`; `websocket.rs:704`; `websocket.rs:1312` | 上游持有 host WSS；本仓持有 controller WSS，并按实测保持稳定 `stream_id` 与旧流容忍。 |
| 官方 reconnect backoff | `/Users/jacklee/Projects/codex/codex-rs/app-server-transport/src/transport/remote_control/websocket.rs:81`; `websocket.rs:1312` | 算法与 cap/reset 语义对齐；终止信号来自本仓 controller/stream supervisor。 |
| `thread/read` history 与 item projection | `/Users/jacklee/Projects/codex/codex-rs/app-server-protocol/src/protocol/common.rs:786`; `protocol/v2/thread.rs:1628`; `protocol/v2/item.rs:233` | 官方类型进入本仓 SSV2 投影；未知 item 显式跳过，不伪造内容。 |
| `thread/name/set`、archive/delete、`thread/start.cwd` | `/Users/jacklee/Projects/codex/codex-rs/app-server-protocol/src/protocol/common.rs:532`; `common.rs:537`; `common.rs:566`; `protocol/v2/thread.rs:62` | 本仓复用既有 Bridge mutation/workdir seam，写后以官方 readback 为唯一标题/目录真相。 |
| `thread/list` page/fingerprint lifecycle | `/Users/jacklee/Projects/codex/codex-rs/app-server-protocol/src/protocol/v2/thread.rs:62`; `/Users/jacklee/Projects/codex/codex-rs/app-server/src/thread_state.rs:61` | Remote 使用官方事件触发目录刷新；60 秒安全扫描仅作边界修复，不把 recency churn 当目录变化。 |
| Session projection / pull-resume | `/Users/jacklee/Projects/codex/codex-rs/app-server-protocol/src/protocol/v2/thread_data.rs:202`; `thread_data.rs:355` | reducer 快照是唯一 SoT；无观察者的大 patch 断点后由现有完整快照恢复，未造 mock/fallback 数据。 |

## 3. Key File Changes (关键文件变更)
- `agent/codex-remote/`: 独立 Remote controller、pairing、WSS envelope/stream、RPC、thread/history/event、mutation、model/effort、重连和诊断。
- `go-bridge/session_discovery.go`: Remote 目录事件触发、60 秒安全扫描、失败退避、recency 稳定指纹。
- `go-bridge/event_publisher.go`、`go-bridge/projection_reducer.go`: v2 观察者判定、无观察者大整回合 patch 抑制、权威快照保留。
- `go-bridge/handlers.go`: 无 live connection 时跳过逐 token 设备重绑定扫描并降低无目标日志等级。
- `go-bridge/projection_*_test.go`: 大 patch 丢弃、快照保留、小增量 journal 连续性和 v2 交付回归。
- `CHANGELOG.md`: 记录 Remote catalog、标题/目录/mutation 和 CPU 修复。
- `/Users/jacklee/Projects/cordcode-ios-codex-remote/OpenCodeiOS/`: 独立 `codex-remote` 接线、模型/effort、双向投影和 authoritative mutation 映射（iOS commit `5565a86`）。

## 4. Verification Evidence (验证证据)
### 4.1 Automated tests
- Commands: `go test ./... -count=1 -timeout 300s` (PASS); targeted `go test -race ./go-bridge ...` for projection/rebind paths (PASS); focused iOS mutation suite (PASS); `gitleaks detect --no-banner --redact --source .` (no leaks).
- Result: complete Go module suite passed, including `go-bridge`, `agent/codex-remote`, app-server RPC, and all supporting packages; build script succeeded.
- Attestation: `re-verified` for commands recorded in the current state; individual historical test triplets retain their honest state attestation.
- Main test files: `go-bridge/projection_coalesce_test.go`, `go-bridge/projection_delivery_test.go`, `go-bridge/session_discovery_test.go`, `agent/codex-remote/*_test.go`, `/Users/jacklee/Projects/cordcode-ios-codex-remote/OpenCodeiOS/OpenCodeiOSTests/CCCodeBridgeMutationRefetchTests.swift`.
- Artifact paths: `.exec-plan/state/plan-bb4683ae3ec1.json`, `CHANGELOG.md`, build output under `build/unsigned-release/`.

### 4.2 Regression evidence
- Owner physical-device acceptance (self-attested): iOS↔Mac bidirectional send/sync and turn handoff; model list and effort; rename/archive/delete; selected project directory; generated title stability across message page, iOS list, and Codex Desktop.
- Mac runtime (self-attested local deployment): `/Applications/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime` reports commit `c0e26fe901ca`; it is the sole TCP `8777` listener. Startup reports `interval=1m0s codexRemoteHintInterval=0s`.
- CPU benchmark (self-attested local process sample): 12 samples over 60 seconds, average `0.48%`, min `0.20%`, max `1.70%`; no reconnect storm. The only Remote catalog request in that window was the expected 60-second safety poll.
- Attestation: device observations and local process benchmark are `self-attested`; re-runnable Go/script commands are `re-verified` where recorded.
- Artifact paths: `/Users/jacklee/Library/Application Support/CordCode Link/logs/go-bridge.log`, `build/unsigned-release/`, `.exec-plan/state/plan-bb4683ae3ec1.json`.

### 4.3 Audit downgrade summary
- Downgraded todos: none in the final queue; the prior `phase5-gate-regression` failure is resolved by the recorded review-fix triplets.
- Why: the former `not_supported`, wrong-project, stale-title, delayed-visibility, catalog self-excitation, and no-observer patch-churn findings each have an implementation, automated-test, and regression record.

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)
- 当前 Remote 没有可复用的 host replay cursor；重连连续性仍依赖稳定 `stream_id`、旧流容忍和重拉 authoritative state，不把缺失 cursor 伪称已实现。
- 官方 ChatGPT iOS controller 与本 controller 的并发 owner 矩阵未单独做 UI 自动化；产品保持 fail-closed，不自动 revoke。
- `thread/list` 的 60 秒安全扫描仍可能在 Desktop 忙时记录一次取消/超时，但它不再形成自激刷新循环，且 CPU 基线已恢复低位。
- 未运行 UI/snapshot/simulator automation；本轮遵守项目约束，采用静态检查、定向单测、Release 部署和真实进程采样。
- Mac 工作树仅保留用户已有未跟踪 handoff 文件；iOS 工作树在已记录 commit 后保持干净。

## 6. Audit Focus (建议审核重点)
1. 复核 `go-bridge/event_publisher.go` 的 observer 判定与 `ProjectionReducer.DropPendingPatch`：快照是否始终先于 live patch 恢复。
2. 用真实 Desktop 回合复核 `stream_id` 稳定性，以及绑定后约 2 秒内是否出现 `stream lost`/重连风暴。
3. 复核 Remote 官方 `thread/list` 事件触发与 60 秒安全扫描的边界，确认标题/目录变更仍即时可见。

## 7. Constraints (关键约束)
- `codex-remote`、`codex-web`、`codex` 的 wire/session/cache identity 与 lifecycle 独立。
- 不用 fallback、缓存快照、mock 或 placeholder 掩盖真实 Remote/path failure；无观察者路径只使用现有 authoritative full projection snapshot。
- 未经 owner 明确允许，不运行 UI/snapshot/simulator/真机自动化。
- 外部协议形状以真实 fixture 和 `/Users/jacklee/Projects/codex` 官方源码为准；闭源 controller/relay 行为不从 host 实现类推。
