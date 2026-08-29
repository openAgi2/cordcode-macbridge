# 本轮任务完成情况：codex-remote Backend 实施方案（ChatGPT Desktop Remote Control 接力）

## 0. Audit Context (审核上下文)
- Project Root: `/Users/jacklee/Projects/cordcode-macbridge-codex-remote`
- Plan: `docs/2026-08-26-codex-remote-backend-implementation-plan.md`
- Canonical State File: `/Users/jacklee/Projects/cordcode-macbridge-codex-remote/.exec-plan/state/plan-bb4683ae3ec1.json`
- Legacy State File: `none`
- Completion Report Verdict: `audit-invalidated`
- Queue Summary: `90/93 todos done; phase5-gate-regression blocked; session-open review-fix regression in progress; model-catalog review-fix regression pending`
- Related Commits: Mac `2de5189a8bfd`; iOS `ed60f99`
- Generated At: `2026-08-29T09:48:00Z`

## 1. Overall Verdict (总体结论)

先前的 `proved-complete` 结论已失效。owner 真机回归证明两个产品缺口：只打开现有 session 时，Bridge 虽完成 projection hydrate，却没有用官方 `thread/resume` 把 Remote app-server connection 原子订阅到后续 thread 更新；同时 `codex-remote` 没有实现官方 `model/list` 与 per-turn model selection。

本轮已完成两个 review-fix 的实现与自动化证明：projection-only open 会创建真实 listener，重连按新 connection epoch 重新 `thread/resume`，模型目录按官方 `model/list` 分页读取并把选择透传到 `turn/start`。`phase5-gate-regression` 仍为 blocked；在 owner 真机回归和 iOS 签名安装完成前，不恢复完成结论。

## 2. Phase Completion Matrix (阶段完成矩阵)

| Phase | Impl | Tests | Regression | Verdict | Evidence (attestation) |
| --- | --- | --- | --- | --- | --- |
| Phase 0 | `proven-done`（9 required + 1 justified n/a） | `proven-done`（9 required + 1 justified n/a） | `proven-done`（9 required + 1 justified n/a） | `proven-done` | 11 re-verified / 16 self-attested / 3 n/a |
| Phase 1 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 5 re-verified / 10 self-attested |
| Phase 2 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 4 re-verified / 8 self-attested |
| Phase 3 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 4 re-verified / 8 self-attested |
| Phase 4 | `proven-done` | `proven-done` | `proven-done` | `proven-done` | 3 re-verified / 6 self-attested |
| Phase 5 | `pending`（review-fix impl/tests done） | `pending` | `blocked` | `blocked` | implementation/tests pass; owner device regression still required |

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

- `agent/codex-remote/`: 新增独立 Remote controller、pairing、envelope/stream、RPC、session/history/event、交互、重连与诊断实现；新增 projection live attachment、官方 model/list catalog、per-turn model/effort framing。
- `go-bridge/`: 注册 `codex-remote`，接入管理 API、session projection、拓扑/健康度和独立 capability 描述。
- `MacBridge/CordCodeLink/`: 新增 Codex Desktop 配对与状态产品入口。
- `/Users/jacklee/Projects/cordcode-ios-codex-remote/OpenCodeiOS/`: 接入独立 `codex-remote` kind、会话/流式投影、输入归属与 Codex runtime presentation；Codex Desktop 选择的 reasoning effort 现在随发送请求透传。
- `agent/codex-appserver/rpc/`: Phase 5 新增 transport-neutral RPC 相关性、事件分发、framing、取消清理与有界关闭核心。
- `agent/codex-web/rpc.go`、`agent/codex-remote/rpc.go`: 保留 backend API 和不同的 close/error policy，改为共享核心的薄适配器。
- `agent/codex-remote/testdata/phase0..phase5/`: 保存真实 fixture、来源、gate、回归与发布证据。

## 4. Verification Evidence (验证证据)

### 4.1 Automated tests
- Commands: `go test ./... -count=1 -timeout 300s` (PASS); `go test -race ./agent/codex-remote -count=1` (PASS); focused `go-bridge` projection/capability tests (PASS); `go vet ./...` (PASS); `git diff --check` (PASS)。
- Release: `./scripts/build-unsigned-release.sh` (PASS, `** BUILD SUCCEEDED **`, runtime commit `2de5189a8bfd`).
- Additional check: `go test -race ./go-bridge -count=1` remains blocked by a pre-existing global fast-relay poll-interval race in `claude_file_relay_test.go`/`handlers_relay.go`; it is unrelated to this Remote change and was not modified.
- Attestation: `re-verified`
- Main test files: `agent/codex-appserver/rpc/client_test.go`, `agent/codex-remote/*_test.go`, `agent/codex-web/*_test.go`, `go-bridge/*codex_remote*_test.go`, `go-bridge/*projection*_test.go`
- Artifact paths: `agent/codex-remote/testdata/phase5/validation.txt`, `agent/codex-remote/testdata/phase2/validation.txt`, `agent/codex-remote/testdata/phase4/validation.txt`

### 4.2 Regression evidence
- Mac Release 已覆盖安装：`/Applications/CordCodeLink.app` 当前 runtime PID `39406`，监听 TCP `8777`；management status 为 runtime `ready`、Remote `ready/online`，relay connected。旧 app 已可恢复地移至 `/tmp/CordCodeLink.previous.FlCjMj`。
- iOS 修改已提交 `ed60f99`；已按真机要求执行 `./scripts/run.sh device --device BFC431AC-C205-56B2-BB4D-9EC0C57A0C05`，真实安装被 Xcode `No Accounts`/缺少 `org.openagi.cordcode` 与 `org.openagi.cordcode.share` provisioning profiles 阻断；随后 `CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO xcodebuild build ...` 通过（`BUILD SUCCEEDED`）。
- Owner device verification is still required: open-only session must receive a Desktop-originated turn, and the model menu must show the live catalog and honor a selected model/effort.
- Attestation: `self-attested` for local build/install; owner device behavior remains unverified.
- Artifact paths: `agent/codex-remote/testdata/phase5/authorization-audit.md`, `agent/codex-remote/testdata/phase5/gate-p5.md`, `agent/codex-remote/testdata/phase5/validation.txt`, `agent/codex-remote/models_test.go`, `go-bridge/projection_live_session_attach_test.go`.

### 4.3 Audit downgrade summary
- Downgraded todos: `phase5-gate-regression` → `blocked`; added review-fix regression todos remain unproven until owner verification.
- Why they were downgraded: owner 真机复现“仅打开 session 后 Desktop 新回合不推送到 iOS”；源码与日志同时证明 projection-open 未建立官方 thread listener。另有 `list_models` 请求，但 backend 不实现 `ModelSwitcher`，因此模型选择器为空。修复已按官方 `thread/resume`、`model/list`、`turn/start` 路径落地；Remote envelope 没有可复用的 replay cursor，不是该缺口的直接根因。官方锚点为 `app-server/src/thread_state.rs:61`、`app-server-protocol/src/protocol/v2/model.rs:53` 与 `protocol/v2/turn.rs:152`。

## 5. Remaining Risks / Non-blocking Warnings (剩余风险 / 非阻塞警告)

- 官方 ChatGPT iOS controller 与 MacBridge controller 的 concurrent-owner/HTTP 409/kick-out 真实矩阵仍未单独执行；产品保持不自动 revoke、不宣称该项已证明。
- 本轮未运行 UI test、snapshot test、simulator automation 或真机自动点击；owner 报告的双向 E2E 属 self-attested。
- 最终安装后的本地观察窗仍较短；若绑定后约 2 秒死亡，应按 `stream_id` 风暴复发处理。
- 当前 runtime 每分钟仍记录一次 `codex-remote: request thread/list canceled: context deadline exceeded` 的 discovery warning；不影响 pairing status，但需在 owner session-list 回归中确认是否为 Desktop app-server 的实际响应边界。
- iOS 工作树已在 `ed60f99` 清洁；真机安装待 Xcode 账号与 provisioning profiles 恢复后重跑。

## 6. Audit Focus (建议审核重点)

1. 审核 `agent/codex-appserver/rpc` 是否始终不反向依赖 backend，以及两个 wrapper 是否继续保留不同的 `IsClosed` 与 error policy。
2. 用真实 relay/设备复核稳定 `stream_id`、旧流容忍和 server-request backlog 不阻塞 RPC response 的组合行为。
3. 独立检查 Phase 0 controller coexistence 的 justified n/a，不要把 owner 的双向同步验收误写成 concurrent-controller 证明。

## 7. Constraints (关键约束)

- `codex-remote`、`codex-web`、`codex` 的 wire/session/cache identity 与 lifecycle 必须独立。
- 不得用 fallback、缓存快照、mock 或 placeholder 掩盖真实 Remote/path failure。
- 未经 owner 明确允许，不运行 UI/snapshot/simulator/真机自动化。
- 外部协议形状以真实 fixture 和 `/Users/jacklee/Projects/codex` 官方源码为准；闭源 controller/relay 行为不得从 host 实现类推。
