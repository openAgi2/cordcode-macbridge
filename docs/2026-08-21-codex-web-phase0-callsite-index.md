# codex-web Phase 0 官方 call-site / server / protocol / test 逐能力索引

- 日期：2026-08-21
- pinned source：`/Users/jacklee/Projects/codex` @ `536f86e5cc9ec1ff38457d099bf320b9d08eeeba`（下文相对路径均以此仓为根，`codex-rs/` 省略）
- 用途：设计 §3.0 证据优先级第 1/2 层的落地索引。`codex-web` 每个能力的实现必须能回溯到本索引的
  官方调用点 + server/protocol 位置 + 官方 v2 test；样本组见 §12 采样任务产物。
- 行号核对：本文件所有 `file:line` 由 `p0-callsite-index-tests` 的脚本逐项验证（符号存在于该行或
  邻域），源码漂移时以符号名为准。

## A. 官方客户端调用链（谁在调用 app-server）

### A1. daemon 自动启动（仅 `codex agents`）

- `cli/src/main.rs:2588-2601`：`interactive.agents_overview && remote.is_none()` 时（unix）先校验
  stdin/stdout 是 terminal，再 `codex_app_server_daemon::run(AppServerLifecycleCommand::Start)`。
  ——设计 §3.3 引用吻合：只有 agents overview 入口自动启动 daemon。

### A2. 普通 TUI 的 runtime 选择（核心 Gate 依据）

- `tui/src/lib.rs:275`：`enum AppServerTarget { Embedded, LocalDaemon{endpoint}, Remote{endpoint} }`。
- `tui/src/lib.rs:912-925`：`can_reuse_implicit_local_daemon(cli_kv_overrides, loader_overrides,
  strict_config, has_non_replayable_launch_overrides)` = `cli_kv_overrides.is_empty() &&
  loader_overrides_are_default() && !strict_config && !has_non_replayable_launch_overrides`。
  ——带 `-c`/strict/非默认 loader 覆盖 → 不复用 daemon。
- `tui/src/lib.rs:436-459`：`maybe_probe_default_daemon_socket(codex_home)` 用
  `app_server_control_socket_path(codex_home)` + `UnixStream::connect` + 超时探测默认 control
  socket（非 unix 平台恒 None）。
- `tui/src/lib.rs:851-876`：`app_server_target_for_launch(explicit_remote, default_socket,
  can_reuse, workload_identity_selected)`：workload identity → Embedded；显式 remote → Remote；
  `can_reuse && socket 存活` → **LocalDaemon(UnixSocket)**；否则 Embedded。
- `tui/src/startup_orchestration.rs:135-190`：`reuse_implicit_local_daemon = !workload_identity &&
  (cli.agents_overview || can_reuse_implicit_local_daemon(...))`；随后在 startup draft 中探测
  default daemon socket 并调 `app_server_target_for_launch`。
  ——设计 §3.3 的四条边界（自动启动/默认复用/覆盖隔离/多连接订阅）全部有源码落点。

### A3. 官方 client facade（`codex-web` rpc/transport 移植母本）

- 进程内：`app-server-client/src/lib.rs`：`InProcessAppServerClient`(:300)、`start`(:327)、
  `request`(:439/615)、`request_typed`(:467/637)、`notify`(:490/706)、
  `resolve_server_request`(:516/713)、`reject_server_request`(:544/724)、`next_event`(:574/735，
  ordered event 消费)、`shutdown`(:582/742)。
- 远端：`app-server-client/src/remote.rs`：`RemoteAppServerEndpoint`(:72)、
  `RemoteAppServerConnectArgs`(:83)、`RemoteAppServerClient::connect`(:166)、
  `server_version`(:187)、request/request_typed/notify(:493-519)、
  `resolve/reject_server_request`(:541/:568)、`next_event`(:595)、`shutdown`(:602)。
- daemon 侧 client：`app-server-daemon/src/client.rs`。

### A4. TUI session 生命周期与事件分发

- `tui/src/app/session_lifecycle.rs`：`attach_live_thread_for_selection`(:365)、
  `select_agent_thread`(:462)、`handle_startup_thread_started`(:617)、
  `start_fresh_session_with_summary_hint`(:697)、`replace_chat_widget_with_app_server_thread`(:796)、
  `is_terminal_thread_read_error`(:225)（thread/read 终态错误分类）、
  `can_fallback_from_include_turns_error`(:237)。
- `tui/src/app/app_server_events.rs`：`handle_app_server_event`(:54)（TUI 事件总入口）。
- `tui/src/app/thread_events.rs`（710 行）、`tui/src/app/thread_routing.rs`（1877 行）：官方 UI
  如何分发/缓存/路由 thread/turn/item 事件。

## B. server / protocol 位置（server 承诺了什么）

### B1. 协议类型与注册表

- 类型：`app-server-protocol/src/protocol/v2/`（`thread.rs`、`turn.rs`、`item.rs`、`model.rs`、
  `config.rs`、`permissions.rs`、`mcp.rs`、`thread_data.rs`（`Turn`/`TurnItemsView`）、`shared.rs`）。
- 方法注册 + stable/experimental 门控：`app-server-protocol/src/protocol/common.rs`
  （`#[experimental]` 属性驱动；`PlanDelta` 仅 doc 注释；capability
  `mcpServerOpenaiFormElicitation` 于 common.rs:2668/2700）。

### B2. 多连接订阅 / unsubscribe / 卸载

- `app-server/src/thread_state.rs:533-581`：`try_ensure_connection_subscribed` /
  `try_add_connection_to_thread`——同一 thread 的 `connection_ids` 集合，多连接订阅是官方模型。
- `app-server/src/request_processors/thread_processor.rs:521/960-984`：
  `thread_unsubscribe` 只移除当前连接订阅，返回 `Unsubscribed/NotSubscribed/NotLoaded`，不卸载
  thread。
- `app-server/src/request_processors/thread_lifecycle.rs:7`：
  `THREAD_UNLOADING_DELAY = Duration::from_secs(30*60)`；README.md:201——最后订阅者离开后
  30 分钟无订阅且无活动才卸载并 `thread/closed`。

### B3. 写所有权

- 写锁：`thread-store/src/local/writer_lock.rs:39`（`<thread_id>.lock` 文件锁 + stale 清理）、
  `thread-store/src/local/mod.rs:302`（`acquire_writer_locks`；live_recorders 已持有则跳过）、
  `archive_thread.rs:24-41`（archive 前 lifecycle+writer 锁）。
- 冲突错误码：`app-server/src/error_code.rs:3` `INVALID_REQUEST_ERROR_CODE = -32600`。
- resume：`app-server/src/request_processors/thread_processor.rs:531 thread_resume` /
  `:3479 thread_resume_inner`。

### B4. 审批 / 问答 server 构造

- `app-server/src/bespoke_event_handling.rs`：`ExecApprovalRequest` →
  `CommandExecutionRequestApprovalParams` 构造（:609-:714，`available_decisions` 无条件
  `Some`）；同文件其余 ServerRequest 构造（file/permission/user input/elicitation/dynamic tool）。
- 出站 experimental 剥除：`app-server/src/transport.rs:175-198`
  `filter_outgoing_message_for_connection`（未开 experimentalApi 时仅
  `strip_experimental_fields` → 只置空 `additional_permissions`；`item.rs:1512-1518` 的 TODO
  承认 generic strip 未完成）。

### B5. WebSocket transport / daemon

- `app-server-transport/src/transport/websocket.rs`：`/healthz` `/readyz`(:61-64,:149-150)、
  loopback 判定(:70)、非 loopback 无 auth 拒绝启动(:135-139，`is_unauthenticated_non_loopback_listener`)。
- `app-server-daemon/src/lib.rs:191 run(LifecycleCommand)`；CLI 面：
  `codex app-server daemon {bootstrap,start,restart,enable-remote-control,disable-remote-control,stop,version}`；
  proxy：`codex app-server proxy --sock <path>`（stdio↔control socket 字节转发）。
- control socket 路径：`app-server_control_socket_path(codex_home)`（app-server-client crate）。

## C. 官方 v2 可执行契约（`app-server/tests/suite/v2/`）

与 §7 一期能力对应的测试文件：

| 能力组 | v2 test |
|---|---|
| initialize / 连接 / 断线 | `initialize.rs`、`connection_handling_websocket.rs`、`connection_handling_websocket_unix.rs`、`session_end.rs` |
| catalog | `thread_list.rs`、`thread_loaded_list.rs` |
| history / hydrate | `thread_read.rs`、`thread_resume.rs`、`thread_sections.rs`、`rollout_migration.rs` |
| 生命周期写操作 | `thread_start.rs`、`thread_archive.rs`、`thread_unarchive.rs`、`thread_delete.rs`、`thread_fork.rs`、`thread_unsubscribe.rs`、`thread_name_websocket.rs`、`thread_status.rs` |
| turn | `turn_start.rs`、`turn_start_zsh_fork.rs`、`turn_steer.rs`、`turn_interrupt.rs`、`thread_queue.rs` |
| 交互 | `request_user_input.rs`、`mcp_server_elicitation.rs`、`experimental_api.rs` |
| 工具/压缩/上下文 | `command_exec.rs`、`dynamic_tools.rs`、`mcp_tool.rs`、`compaction.rs`、`account_thread_usage.rs` |
| 审查/其它 | `review.rs`、`config_rpc.rs`、`web_search.rs`（⚠️/🧪 面取证用） |

## D. §7 一期能力 → 三方引用映射（✅ 行）

| §7 能力 | 官方客户端 call site | server/protocol | v2 test | 样本组(§12) |
|---|---|---|---|---|
| list_sessions | TUI `session_lifecycle.rs` agent picker `upsert_agent_picker_thread`/`refresh_agent_picker_thread_liveness`(:250/:282)；官方 client `request_typed` 模式 | `thread_processor.rs` thread_list；`v2/thread.rs` ThreadListParams/Response | thread_list.rs | catalog |
| get_session / rich history | `replace_chat_widget_with_app_server_thread`(:796)、`is_terminal_thread_read_error`(:225) | thread/read（`includeTurns`）；`thread_data.rs` | thread_read.rs | history |
| create/resume | `start_fresh_session_with_summary_hint`(:697)、`handle_startup_thread_started`(:617)、`select_agent_thread`(:462) | thread/start、thread/resume(:531) + writer 锁(B3) | thread_start.rs / thread_resume.rs | turn / ownership |
| send_message | TUI composer → `turn/start`（`turn_processor.rs`） | `v2/turn.rs` TurnStartParams | turn_start.rs | turn |
| steer | TUI steer 路径（`thread_routing.rs` 内 active turn 跟踪） | TurnSteerParams `expectedTurnId` 必填 | turn_steer.rs | turn |
| stop | TUI interrupt | turn/interrupt | turn_interrupt.rs | turn |
| live status / stream | `app_server_events.rs:54` 总入口 + `thread_events.rs`/`thread_routing.rs` 分发 | `thread_state.rs:533-581` 多连接订阅；item/* 通知（common.rs） | thread_status.rs / turn_start.rs | turn / external host |
| approvals / questions | 官方 client `resolve_server_request`(:516/:541) | B4；`item.rs`/`permissions.rs`/`mcp.rs` 类型 | request_user_input.rs / mcp_server_elicitation.rs | interaction |
| models / config 只读 | TUI model picker（models_refresh_worker.rs） | `v2/model.rs`、`v2/config.rs`、models.rs | config_rpc.rs / experimental_api.rs | models/config |
| rename/archive/delete/unsubscribe | `session_lifecycle.rs` 相应入口 | thread_processor.rs:960 等；`thread_lifecycle.rs:7` | thread_name_websocket.rs 等 | catalog / ownership |
| token usage | TUI usage 显示路径 | `thread/tokenUsage/updated`（common.rs 通知） | account_thread_usage.rs | history / turn |

## E. 实施纪律提示（来自本索引的三个新事实）

1. `startup_orchestration.rs:135-137`：`agents_overview` **也**置 `reuse_implicit_local_daemon=true`
   （`cli.agents_overview || can_reuse(...)`）——即 `codex agents` 启动后，后续默认 TUI 可复用其
   daemon。Gate 场景 1 的"daemon 已运行"可以用官方 `codex agents` 拉起（与设计 §3.3 一致）。
2. `websocket.rs:135-139`：非 loopback 无 auth 直接拒绝启动——`codex-web` 托管 WS 只绑
   127.0.0.1 与官方安全边界一致，无需额外设计。
3. `thread/read` 的 `excludeTurns`+cursor（README:367）与 `thread/turns/list` 分页是
   paginated thread 的官方续聊契约；一期 `thread/read(includeTurns:true)` 基线之外的行为
   一律待样本后再决定（§7 🧪 行不变）。
