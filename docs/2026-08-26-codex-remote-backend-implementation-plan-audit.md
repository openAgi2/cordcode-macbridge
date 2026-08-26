# codex-remote 实施方案评审（audit-plan 样本核验）

- 日期：2026-08-26
- 评审对象：[2026-08-26-codex-remote-backend-implementation-plan.md](2026-08-26-codex-remote-backend-implementation-plan.md)
- 评审方法：audit-plan（每条外部协议内容形状声明必须由本 session 的真实源码 dump / app.asar 提取支撑）
- 结论：**有条件通过**。无 P0 级事实错误；1 处 envelope 字段声明（environment_id）与源码不符需修正，版本基线漂移需补充为显式约束，controller 侧 wire 细节维持"待 fixture"定位。

## 0. 来源清单

```text
MacBridge=/Users/jacklee/Projects/cordcode-macbridge main 31e761f92c305553b6134c5e1e0df76558730d2c（工作区仅有评审对象本身，未跟踪）
Codex 上游=/Users/jacklee/Projects/codex main 25a6e316c81fb7600d1d75f3e63ffe26be10b7c8（2026-08-26 03:46 UTC，干净）
运行态 App=/Applications/ChatGPT.app 26.820.60940，内嵌 codex-cli 0.150.0-alpha.8（app.asar 只读提取）
iOS=未读取源码（本评审不涉及 iOS 事实声明）
```

版本关系（本评审新增事实）：本地 tag 仅有 `rust-v0.150.0-alpha.11`（**无 alpha.8 tag**）；HEAD = alpha.11 + 29 个提交；App 内嵌二进制为更老的 0.150.0-alpha.8。即：**本文核验的 remote_control 模块源码（HEAD）比出货二进制新**，`rust-v0.150.0-alpha.8` 对应的模块形状无法在本地直接 diff（见 P1-2）。

## 1. 逐内容类型核验表

| # | 内容类型（方案声明） | 证据 dump（file:line） | 评级 |
|---|---|---|---|
| 1 | URL 族：`config.chatgpt_base_url` → `/wham/remote/control/server/*` + host WSS（§2.2） | `app-server/src/lib.rs:784` `remote_control_url: config.chatgpt_base_url.clone()` → `remote_control/mod.rs:962` `normalize_remote_control_url(&config.remote_control_url)`；`protocol.rs:298-370` 测试实证 `https://chatgpt.com/backend-api` → `wss://chatgpt.com/backend-api/wham/remote/control/server` + `/enroll` `/refresh` `/pair` `/pair/status` | 🟢 |
| 2 | 生产 URL 仅允许 HTTPS chatgpt.com/子域 + staging；localhost 供开发（§2.2） | `protocol.rs:193-200` `is_allowed_remote_control_chatgpt_host`（`chatgpt.com`、`chatgpt-staging.com` 及子域）；`:203-215` loopback；`:283-286` HTTPS 强制（chatgpt 域）/HTTP 仅 localhost；错误文案逐字含 "expected HTTPS URL for chatgpt.com or chatgpt-staging.com, or HTTP/HTTPS URL for localhost" | 🟢 |
| 3 | host enrollment 必须 ChatGPT 认证 + account id，API key 不支持（§2.2） | `auth.rs:56-58` `"remote control requires ChatGPT authentication; API key auth is not supported"`（PermissionDenied）；`auth.rs:12` header `chatgpt-account-id`；`server_api.rs:104` `account_id: auth.account_id.clone()`；`server_api.rs:30` `x-codex-installation-id` | 🟢 |
| 4 | enroll/refresh/pair 返回 server/environment identity 与短期 host token（§3 行2） | `protocol.rs:25-71`：`EnrollRemoteServerResponse{server_id, environment_id, remote_control_token, expires_at}`、`RefreshRemoteServerRequest{server_id, installation_id}`、`StartRemoteControlPairingResponse{pairing_code, manual_pairing_code, server_id, environment_id, expires_at}`、`RemoteControlPairingStatusResponse{claimed}`。QR/manual 双码 → §4.1 "QR 或 manual pairing code" 成立 | 🟢 |
| 5 | envelope：JSON-RPC 外包 client/stream/seq（§3 行4，§6.1） | `protocol.rs:85-127` `ClientEvent::{ClientMessage{message: JSONRPCMessage}, ClientMessageChunk{segment_id, segment_count, message_size_bytes, message_chunk_base64}, Ack{segment_id?}, Ping, ClientClosed}`；`:130-142` `ClientEnvelope{event(serde flatten), client_id, stream_id?, seq_id?, cursor?}`；`:156-178` `ServerEvent::{ServerMessage, ServerMessageChunk, Ack, Pong{status}}`；`:182-190` `ServerEnvelope{client_id, stream_id, seq_id}` | 🟢（除 environment_id，见 #13） |
| 6 | ACK / 未确认缓冲 / reconnect cursor / 分片上限 / ping-pong / 空闲回收（§3 行3，§6.2） | `websocket.rs:74` header `x-codex-subscribe-cursor`（cursor 经 HTTP header 在重连时递交）；`:126-132` acked cursor `(seq_id, segment_id)` 比较；`:209-210` 订阅 cursor 存储；`:264-265,516-523` reconnect 循环；`:2718` 测试 `run_server_writer_inner_assigns_contiguous_seq_ids_per_stream`（per-stream 单调 seq）；`segment.rs:19-23` **TARGET 100KB / MAX 150KB / 重组上限 100MB / 1024 段 / 128 并发组装**；`client_tracker.rs:27-28` **空闲 10min + 30s sweep** | 🟢 |
| 7 | host token 到期前刷新（§6.2 类比项） | `server_api.rs:20-22` 刷新退避 24–36s；`:127,165-188` `server_token_refresh_requirement_at` 主动调度 + 失败降级日志 | 🟢（host 侧）|
| 8 | Remote → 普通 app-server connection：`ConnectionOrigin::RemoteControl` + `IncomingMessage`（§3 行5） | `client_tracker.rs:106-109` initialize 识别；`:151` `TransportEvent::IncomingMessage`；`:170` `origin: ConnectionOrigin::RemoteControl`；`:433` `method == "initialize"` | 🟢 |
| 9 | runtime control API：enable/disable/status/pair/client list/revoke（§3 行6） | `remote_control_processor.rs:32-113`：`enable`/`disable`/`status_read`/`pairing_start`/`pairing_status`/`clients_list`/`clients_revoke` 全部存在；错误文案实证方法名前缀 `remoteControl/pairing/status`（camelCase 参数 `pairingCode`/`manualPairingCode`） | 🟢 |
| 10 | CLI/daemon：实验性 start/stop/pair + remote-control daemon（§3 行7） | `cli/src/remote_control_cmd.rs:45-47` 子命令 `remote-control start/stop/pair`；`:54,60,80` "Start the app-server (daemon) with remote control enabled"；`:91` `start_remote_control_pairing`；`:127` `RemoteControlStartupMode::EnabledEphemeral` | 🟢 |
| 11 | stdio 私有 app-server 默认按持久化设置启动 Remote Control（§3 行1） | `app-server/src/lib.rs:442,451` 默认 `RemoteControlStartupMode::ResolvePersisted`；`:797-805` `resolve_persisted_preference`；`remote_control/mod.rs:368-372` SQLite state db `set_remote_control_enabled(websocket_url, account_id, client_name)` | 🟢 |
| 12 | controller 客户端（asar）：`/codex/remote/control/client`、enroll/refresh、device-key challenge/proof、短期 scope token（§3 行8，§4.1） | asar 提取：`expectedPath:'/codex/remote/control/client/enroll/finish'`、`.../enroll/start`、`.../refresh/start'`、`.../refresh/finish'`、`.../pair'`、`/codex/remote/control/clients'`（**两步 enroll/refresh = start/finish**，finish 带 `requireDeviceI...` 校验）；scopes 数组 `['remote_control_controller_websocket']` 且 `scopes.length!==1‖scopes[0]!=='remote_control_controller_websocket'` 抛错、audience 文案 "Remote control device-key connection audience"、`tokenSha256Base64url` 绑定；遥测名 `remote_control_websocket_device_key_challenge[_signed\|_mismatch]`；step-up token 错误文案；另有 `wham/remote/control/{client/pair,clients,mfa}` 路径族 | 🟢（字符串级；wire 全形状仍需 Phase 0 fixture，方案已如此定位） |
| 13 | **envelope 复用层含 `environment_id`（§6.1 图）** | **不成立/未证实**：host 侧 `ClientEnvelope`/`ServerEnvelope` 字段全集（#5）**无 environment_id**；`environment_id` 仅出现在 enroll/pairing 响应（`protocol.rs:31,52`）与 websocket.rs 的 enrollment 状态跟踪（`:350-383,713,768`）。asar 侧 `environment_id` 命中全部为 `local_remote_control_environment_id` 持久化状态/查询键，未见 envelope 字段。controller→relay 如何按 environment 寻址（REST 订阅、WSS 控制消息或连接绑定）在 OSS 源码中不可见（relay 服务端闭源） | 🔴→修订为"绑定机制待 fixture 证明" |
| 14 | app-server JSON-RPC 方法/事件名（§6.1、Phase 0 任务 6–9、§13.6） | `app-server-protocol/src/protocol/common.rs:497` `initialize`、`:786` `thread/read`、`:700` `thread/list`、`:985` `turn/interrupt`、`:1839` `thread/started`、`:1863` `turn/started`、`:1865` `turn/completed`、`:1870` `item/started`、`:1685-1710` `item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、`item/tool/requestUserInput`、`item/permissions/requestApproval`；`:2796` `"initialized"`；`v2/thread.rs` `include_turns` 字段（wire camelCase `includeTurns`，与 processor 错误文案一致）；`v2/turn.rs` `turn/start`/`turn/steer`；`v2/thread_data.rs:266` "Only populated on thread/resume, rollback, fork, and thread/read" | 🟢 |
| 15 | §5.3/§5.4 复制白名单/黑名单引用的 codex-web 文件存在性 | `agent/codex-web/` 实测：`rpc.go`、`codec.go`、`events.go`、`catalog_thread_list.go`、`history.go`、`sessions.go`、`session.go`、`interactions.go`、`models.go`、`permission_modes.go`、`context_usage.go`、`lifecycle.go`、`lifecycle_managed.go` 及对应 `*_test.go` 全部存在 | 🟢 |

## 2. 未证实内容类型（Phase 0 的 P0 候选）

1. **controller WSS 完整 wire**（envelope 字段全集、environment 绑定方式、连接 challenge 的握手时序）：仅有 asar 字符串级证据；OSS 仓只含 host 侧。方案的 "experimental/private implementation detail + fixture 冻结" 定位正确，评审只要求 §6.1 图先按 #13 修正。
2. **controller token 的到期前主动刷新调度**：refresh/start+finish 端点已证（#12），"到期前"的调度细节在 asar 内部，未见 dump；列为 assumption pending fixture（P2-1）。
3. **步骤升级/MFA 的完整流程**：`wham/remote/control/mfa` 端点与 step-up token 错误文案已证（#12），交互序列未取样。

## 3. 脚本交叉核验记录

1. **`rg -r` 污染**：本评审两次误用 `rg -rn pattern`（`-r` 是 replace，把命中替换成 "n"），产出 `protocol::n`、`pub n: bool` 等假象；全部用干净 `rg -n`/`sed` 重跑后才采信。教训与 skill 的"dump 脚本本身可能错"一致：被替换的输出**形状可疑**（标识符单字母化）是识别线索。
2. **chatgpt_base_url 归因反转（典型 attribution verification）**：第一轮 grep 命中 `config.remote_control_url`（mod.rs:962），若止步于此会误判方案 §2.2 错误；追构造点发现 `app-server/src/lib.rs:784` 用 `config.chatgpt_base_url` 填充 `RemoteControlStartConfig.remote_control_url`——方案声明正确，链路是两跳。**正确的下一步（找构造点）而不是重数命中次数**，才避免了把正确文档改错。
3. **environment_id 双策略**：策略 A（struct 字段全集 dump，protocol.rs 130-190）与策略 B（asar 上下文 grep）一致得出"非 envelope 字段"，两个独立来源同向，#13 判定成立。
4. **URL 校验双证据**：校验函数实现（protocol.rs:193-286）与测试期望值（:298-370）一致。

## 4. 修订优先级

### P1（施工前修正文档）

1. **§6.1 envelope 图去掉 `+ environment_id` 或改为"environment 绑定机制待 Phase 0 fixture"**。现图把它画成复用层字段，与 host 侧 envelope 字段全集（client_id/stream_id/seq_id/cursor + event）不符；controller 侧亦无证据。绑定机制（REST 订阅 vs WSS 控制消息 vs 连接级）属于闭源 relay 契约，必须由 fixture 冻结后再写入设计。
2. **版本基线显式化**：§3 表各"最新上游位置"核验于 HEAD `25a6e316`（今日 main），出货二进制是 0.150.0-alpha.8（更老；本地无该 tag，无法 diff remote_control 模块差异；HEAD 已领先 alpha.11 29 提交，模块处于活跃演进）。Phase 0 任务 1（冻结版本）应补充：a) 尝试 fetch `rust-v0.150.0-alpha.8` tag 并 diff `codex-rs/app-server-transport/src/transport/remote_control/` 与 HEAD 的差异清单；b) 所有"源码核验于 HEAD"的结论在 asar/binary fixture 面前以 binary 为准。

### P2（施工时补强）

1. §6.2 "controller token 到期前刷新"：标注 controller 侧调度为 assumption pending fixture（host 侧主动刷新已由 `server_token_refresh_requirement_at` 证实）。
2. §6.2/§9 可落入的精确常量（来自 segment.rs/client_tracker.rs，直接进测试断言）：段 TARGET 100KB / MAX 150KB / 重组 100MB / 1024 段 / 128 并发组装；空闲 10min / sweep 30s；host 刷新退避 24–36s；reconnect cursor 经 header `x-codex-subscribe-cursor` 递交。
3. §3 行8 的 controller 端点族建议同时索引 `/codex/remote/control/client/*`（expectedPath 校验用）与 `wham/remote/control/*`（client/pair、clients、mfa）两个路径族，Phase 0 fixture 记录各自 base 与版本归属。
4. §5.2 建议补 `remote_protocol.go` 需覆盖 `ServerEvent::Pong{status: Active\|Unknown}` 与 `ClientClosed`（客户端可主动上报关闭，重连语义不同于连接断开）。

### 无 P0

方案的其余全部内容形状声明（URL 族与校验、认证红线、REST 请求/响应、envelope、ACK/cursor/chunk/seq/空闲回收、client_tracker origin、runtime API 方法、CLI 子命令、app-server 方法/事件名、复制白名单文件存在性）均与上游源码/asar 一致；"先证据探针后产品接线"、fail-closed、版本门、禁止 MITM/改包/偷凭据的边界与源码事实（API key 显式拒绝、URL 白名单）互相印证。

## 5. 对 Phase 0 Gate 的补充建议

- Gate 判据 1–12 保持；建议在第 3 项（fixture 冻结）里明确**必须包含 environment 绑定证据**（controller 如何指定/切换目标 environment），否则 §6.1 的两级连接设计缺少关键一环的实证。
- 建议在 dumps 里同时保留 host 侧 `x-codex-subscribe-cursor` 递交样本（重连时序），它是 §6.2 "reconnect cursor" 的直接实现位置。
