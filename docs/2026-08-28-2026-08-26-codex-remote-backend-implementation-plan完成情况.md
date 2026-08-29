# 本轮任务完成情况：codex-remote Backend 实施方案（ChatGPT Desktop Remote Control 接力）

## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-codex-remote`
- Plan: `docs/2026-08-26-codex-remote-backend-implementation-plan.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge-codex-remote/.exec-plan/state/plan-bb4683ae3ec1.json`
- Legacy State File: `none`
- Completion Report Verdict: `proved-complete`
- Queue Summary: `87/87 todos done; 84 required todos proven, 3 justified n/a; 30 re-verified and 57 self-attested proof records`
- Related Commits: Mac `94000a9..ca9c223` (delivery head `ca9c223e47df`); iOS `7667a80d`
- Generated At: `2026-08-29T08:05:30Z`

## 1. Overall Verdict (总体结论)

计划队列已全部收口：87/87 todo 为 `done`，所有 required todo 均有结构化证据，三个 Phase 0 官方 iOS controller 共存项按 owner 裁决保留为具体、fail-closed 的 justified n/a。Phase 0–4 完成独立 `codex-remote` 产品链，Phase 5 在 owner 授权和稳定观察窗后只抽取了被证明重复的 transport-neutral RPC 核心。

30 条自动化/可重放证据在审计中重跑为 `re-verified`；实现、真实服务观察、owner 真机 E2E 和发布运行态等 57 条记录为 `self-attested`。这不是独立第三方审计结论。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| Phase 0 | `proven-done`（9 required + 1 justified n/a） | `proven-done`（9 required + 1 justified n/a） | `proven-done`（9 required + 1 justified n/a） | `proven-done` | 11 re-verified / 16 self-attested / 3 n/a |
| Phase 1 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 5 re-verified / 10 self-attested |
| Phase 2 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 4 re-verified / 8 self-attested |
| Phase 3 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 4 re-verified / 8 self-attested |
| Phase 4 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 3 re-verified / 6 self-attested |
| Phase 5 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 3 re-verified / 6 self-attested |

### 2.1 Upstream Anchors (上游锚点) — port/parity plans 必填

| Fix / todo | Upstream anchor (file:line) or exemption card | First divergence vs upstream |
| --- | --- | --- |
| Controller enroll/refresh/pair wire contract | `/Users/jacklee/Projects/codex/codex-rs/app-server-transport/src/transport/remote_control/protocol.rs:130`; `server_api.rs:78`; `enroll.rs:48` | 上游实现 Desktop host；本仓实现经真实 fixture 冻结的独立 controller，并保留自己的凭据与设备身份。 |
| Envelope、ACK/cursor 与 reconnect | `/Users/jacklee/Projects/codex/codex-rs/app-server-transport/src/transport/remote_control/websocket.rs:74`; `websocket.rs:704`; `websocket.rs:785`; `websocket.rs:1312` | 上游持有 host WSS；本仓持有 controller WSS，并在闭源 relay 边界按实测保持稳定 `stream_id` 与旧流容忍。 |
| 官方 reconnect backoff | `/Users/jacklee/Projects/codex/codex-rs/app-server-transport/src/transport/remote_control/websocket.rs:81`; `websocket.rs:1312` | 算法与 cap/reset 语义对齐；终止信号来自本仓 controller/stream supervisor。 |
| 分片与资源上限 | `/Users/jacklee/Projects/codex/codex-rs/app-server-transport/src/transport/remote_control/segment.rs:19` | 上游在 host transport 分片；本仓在 controller envelope 层执行同类有界重组与拒绝策略。 |
| initialize、Ping/Pong、ClientClosed 与 stream generation | `/Users/jacklee/Projects/codex/codex-rs/app-server-transport/src/transport/remote_control/client_tracker.rs:106`; `client_tracker.rs:219`; `client_tracker.rs:240`; `client_tracker.rs:861` | 上游把 controller 流接入 app-server transport；本仓把流投影为独立 backend epoch，不共享 Desktop/local-daemon 生命周期。 |
| `thread/read` history 与 item projection | `/Users/jacklee/Projects/codex/codex-rs/app-server-protocol/src/protocol/common.rs:786`; `protocol/v2/thread.rs:1628`; `protocol/v2/thread.rs:1650`; `protocol/v2/thread_data.rs:202`; `protocol/v2/thread_data.rs:355`; `protocol/v2/item.rs:233` | 官方类型进入本仓 SSV2 投影；本仓只白名单已证明 item，未知类型显式记入 `SkippedTypes`。 |
| `turn/steer` 与 `turn/interrupt` | `/Users/jacklee/Projects/codex/codex-rs/app-server-protocol/src/protocol/v2/turn.rs:273`; `turn.rs:307` | 官方请求参数保持不变；本仓从 Remote 活跃 turn/history 取得真实 id，不合成 id。 |
| Phase 5 JSON-RPC correlation/event core | `/Users/jacklee/Projects/codex/codex-rs/app-server-client/src/remote.rs:216`; `remote.rs:493`; `/Users/jacklee/Projects/codex/codex-rs/app-server-client/src/lib.rs:333` | 上游 client 直接拥有 transport；共享包只拥有相关性、分发、framing 与有界关闭，backend 继续拥有 transport、lifecycle、identity、capability 和 diagnostics。 |

## 3. Key File Changes (关键文件变更)

- `agent/codex-remote/`: 新增独立 Remote controller、pairing、envelope/stream、RPC、session/history/event、交互、重连与诊断实现。
- `go-bridge/`: 注册 `codex-remote`，接入管理 API、session projection、拓扑/健康度和独立 capability 描述。
- `MacBridge/CordCodeLink/`: 新增 Codex Desktop 配对与状态产品入口。
- `/Users/jacklee/Projects/cordcode-ios-codex-remote/OpenCodeiOS/`: 接入独立 `codex-remote` kind、会话/流式投影、输入归属与 Codex runtime presentation。
- `agent/codex-appserver/rpc/`: Phase 5 新增 transport-neutral RPC 相关性、事件分发、framing、取消清理与有界关闭核心。
- `agent/codex-web/rpc.go`、`agent/codex-remote/rpc.go`: 保留 backend API 和不同的 close/error policy，改为共享核心的薄适配器。
- `agent/codex-remote/testdata/phase0..phase5/`: 保存真实 fixture、来源、gate、回归与发布证据。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests
- Commands: `go test -race ./agent/codex-appserver/... ./agent/codex-web ./agent/codex-remote -count=1 -timeout 240s`; focused `go-bridge` Remote/topology/projection race suite; `go test ./... -count=1 -timeout 300s`; Phase 5 authorization/boundary/iOS source validators; repeated RPC/reconnect/server-request stress suites。
- Result: 全部 PASS；Release `xcodebuild` 通过并输出 `** BUILD SUCCEEDED **`。
- Attestation: `re-verified`
- Main test files: `agent/codex-appserver/rpc/client_test.go`, `agent/codex-remote/*_test.go`, `agent/codex-web/*_test.go`, `go-bridge/*codex_remote*_test.go`, `go-bridge/*projection*_test.go`
- Artifact paths: `agent/codex-remote/testdata/phase5/validation.txt`, `agent/codex-remote/testdata/phase2/validation.txt`, `agent/codex-remote/testdata/phase4/validation.txt`

### 4.2 Regression evidence
- Device / replay / benchmark / manual validation: owner 确认 iPhone ↔ Codex Desktop 双向会话投影和发消息/回复同步；最终安装前有约 53 分钟稳定观察窗；提交 `ca9c223e47df` 的 Release 覆盖安装后，PID 41727 从 `/Applications/CordCodeLink.app` 监听 8777，management 状态为 runtime `ready`、Remote `ready/online`，51.8 秒启动窗无 2 秒死亡或重连风暴。
- Attestation: `self-attested`（owner 真机结果与执行 agent 的真实运行态观察）
- Artifact paths: `agent/codex-remote/testdata/phase5/authorization-audit.md`, `agent/codex-remote/testdata/phase5/gate-p5.md`, `agent/codex-remote/testdata/phase5/validation.txt`, `agent/codex-remote/testdata/phase0/live/`

### 4.3 Audit downgrade summary
- Downgraded todos: `none`（本轮最终内部审计无 unresolved downgrade）。
- Why they were downgraded: `n/a`。Phase 0 的三条 controller coexistence 记录不是 downgrade，而是 owner 已接受且带明确 fail-closed 条件的 `required:false` justified n/a。

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)

- 官方 ChatGPT iOS controller 与 MacBridge controller 的 concurrent-owner/HTTP 409/kick-out 真实矩阵仍未单独执行；产品保持不自动 revoke、不宣称该项已证明。
- 本轮未运行 UI test、snapshot test、simulator automation 或真机自动点击；owner 报告的双向 E2E 属 self-attested。
- 最终安装后的观察窗约 52 秒；更长稳定性由安装前同一修复链的约 53 分钟窗口支撑。若绑定后约 2 秒死亡，应按 `stream_id` 风暴复发处理。
- iOS Phase 5 无源码改动，因此未触发新的 iPhone 安装；iOS 工作树保持干净，当前交付提交为 `7667a80d`。

## 6. Audit Focus (建议审核重点)

1. 审核 `agent/codex-appserver/rpc` 是否始终不反向依赖 backend，以及两个 wrapper 是否继续保留不同的 `IsClosed` 与 error policy。
2. 用真实 relay/设备复核稳定 `stream_id`、旧流容忍和 server-request backlog 不阻塞 RPC response 的组合行为。
3. 独立检查 Phase 0 controller coexistence 的 justified n/a，不要把 owner 的双向同步验收误写成 concurrent-controller 证明。

## 7. Constraints (关键约束)

- `codex-remote`、`codex-web`、`codex` 的 wire/session/cache identity 与 lifecycle 必须独立。
- 不得用 fallback、缓存快照、mock 或 placeholder 掩盖真实 Remote/path failure。
- 未经 owner 明确允许，不运行 UI/snapshot/simulator/真机自动化。
- 外部协议形状以真实 fixture 和 `/Users/jacklee/Projects/codex` 官方源码为准；闭源 controller/relay 行为不得从 host 实现类推。
