# go-bridge 当前架构与 backend 进程模型

> 本文从原一体仓库 `go-bridge 框架现状.md`、`go_bridge_使用指南.md` 中提炼，
> 以拆分后的 `cordcode-macbridge` 源码为准。旧 Node Unified Bridge、外部 `cc-connect`
> replace、Copilot sidecar 和 FRP 默认路径均已删除出当前说明。

## 边界

```text
iPhone / iPad
  ├─ LAN ws://Mac:8777
  ├─ Tailscale wss://100.x.x.x:8778 + SPKI pin
  └─ Relay wss://relay... + HPKE
            │
            ▼
cccode-bridge-runtime
  ├─ protocol/auth/pairing/relay: go-bridge/
  ├─ agent interfaces: core/
  ├─ agent implementations: agent/{claudecode,codex,opencode}/
  └─ local configuration: config/
```

`core/`、`config/`、`agent/` 已迁入本仓库，不再依赖原一体仓库或本机绝对路径
`replace`。wire 协议适配留在 `go-bridge/`，agent 的进程、历史、模型和能力实现放在
`agent/` 与 `core/`。

## 为什么不再使用旧 Node Unified Bridge

go-bridge 的边界来自旧实现暴露出的四类问题：

1. 多层异步 event buffer 容易在交接处丢事件；
2. pending id、真实 session id 与进程生命周期缺少统一 registry；
3. shutdown 依赖外部硬杀，无法确定性回收 agent 子进程；
4. 错误分支可能吞掉完成信号，客户端长期停在 running。

当前原则不是把旧 Node 逻辑逐行翻译成 Go，而是：

- agent 数据面能力放进 `core/agent`；
- wire、auth、pairing、relay adaptation 放进 `go-bridge/`；
- 只有 OpenCode server 独有的 HTTP 语义保留 proxy；
- 真实路径失败时暴露错误，不增加假结果或 fallback backend。

## 三个网络面

1. Bridge WebSocket：`8777`，处理 `hello/hello_ack`、RPC 与事件。
2. Tailscale TLS：`8778`，使用持久自签名证书；pin 经已认证的
   `pairing_complete` 下发。
3. Management API：随机 loopback 端口，只允许 Mac app 持 token 访问。

Relay 是第四条传输路径但复用同一 Bridge RPC/event 语义。Relay server 只路由 HPKE
密文，不能读取会话内容。

## runtime 生命周期

`MacBridge/MacBridge/Services/RuntimeManager.swift` 启动
`cccode-bridge-runtime`，读取 stdout bootstrap frame，并交叉验证
`runtime.json`、PID、bridge epoch 和 Management URL。

关停顺序为：

1. 停止接受新 HTTP/WebSocket 请求；
2. `Handlers.Shutdown` 关闭活跃 agent session；
3. 关闭 direct/relay 连接；
4. 停止 relay、TLS 与 Management 服务。

Claude 与 Codex 子进程使用进程组回收。不要通过增加后台孤儿进程或忽略 shutdown 错误来
“提高可用性”。

## 事件、session 与广播设计

### 两层事件管线

```text
agent read loop
  → buffered chan core.Event
  → go-bridge relay/broadcast loop
  → mutex-protected direct WebSocket 或 per-device relay queue
```

`EventResult{Done:true}` 和 `EventError` 是 turn 收口信号。映射层发送
`turn_completed`/`error`，随后 session runtime state 回到 idle。中间 delta、tool 或
session status 不能代替确定性完成信号。

### session registry 与 pending rebind

`sessionRegistry` 保存 backend、directory、最后活动时间、running 状态和 agent session。
Codex lazy create 返回 `pending-*` 后：

1. 首个带真实 thread id 的 event 到达；
2. registry 保留 pending alias 并绑定真实 id；
3. broadcaster subscription key 同步 rebind；
4. 后续 get/resume/send 解析到同一活跃 session。

idle session 由 cleanup loop 按 backend TTL 回收；running/pending session 不得被当成普通
idle session 清理。

### 多客户端广播与离线通知

订阅 key 至少包含 `backendID + sessionID + directory`。发送方也经 broadcaster 收事件，
避免直接写连接再广播造成双份。连接关闭必须 `UnsubscribeAll`。

turn 完成时，在线订阅设备收到事件；未在线设备的通知写入 per-device pending store，iOS
回前台通过 `check_pending_notifications` 消费。

## backend 进程模型

### Claude Code

- 每个活跃 session 对应独立 Claude CLI 进程；
- iOS 发起的 turn 可通过该 session 的 stdout 实时推送；
- Mac 端另一个 Claude 进程发起的外部 turn 不共享事件总线；
- descriptor 使用 `liveEvents=session_process`，并声明
  `requiresPollingForExternalTurns=true`；
- 历史来自 `~/.claude/projects` JSONL；支持 rich history、todos、memory、
  usage、diagnostics 和 session mutation，具体能力由接口断言决定。

因此 iOS 不能把“Bridge 有 liveEventStream”误解为“Claude 的所有外部 turn 都会广播”。

> [!NOTE]
> 为了支持 Claude Code 长生命周期的 CLI 进程交互与多轮会话，MacBridge 的 `relayEvents` 转发协程在遇到完成/空闲等退出事件时对 `"claude"` 后端特判继续运行（通过 `continue` 绕过退出）。
> 这意味着每一个通过 iOS 发起过消息的 Claude 会话都会长驻一个转发协程和底层会话对象，其生命周期的终结完全依赖于该会话被显式关闭或删除（届时 events channel 关闭，协程才会自然退出）。在排查协程或内存泄露时需要注意此常驻设计。

### Codex

支持两种模式：

- `exec`：由 runtime 启动 Codex CLI session；
- `app_server`：通过 Codex app-server 协议运行 session。产品 `RuntimeConfig` 默认不显式
  提供 URL，因此 agent session 通过 stdio 启动自己的 app-server；只有显式配置
  `-codex-app-server-url` 时，才连接共享 TCP service 并通过 passive subscriber 接收外部
  thread/turn 事件。

`app_server` 的 create 是 lazy：可能先返回 `pending-*`，首次 send 后再绑定真实
thread id。session registry、订阅 key 与 iOS 当前 session 都必须随 rebind 更新。

默认 stdio app-server 模式下，descriptor 对 Codex 使用 `session_process` 模型，且
`requiresPollingForExternalTurns=true`，iOS 通过历史变化探测旁观外部 turn。显式共享 URL
模式下才使用 broadcast/passive event，并可关闭外部 turn 轮询。

共享 app-server 模式检查：

```bash
command -v codex
lsof -nP -iTCP:4141 -sTCP:LISTEN
ps aux | grep '[c]odex app-server'
```

MacBridge Restart 只重启 Bridge runtime，不负责重启外部共享 Codex app-server。
共享服务的启动归属和本机常驻约束见
[BUILD_INSTALL_AND_RUNTIME.md](BUILD_INSTALL_AND_RUNTIME.md#codex-app-server-的启动归属)。

### OpenCode

OpenCode 不再隐式硬编码 `127.0.0.1:64667`。MacBridge 在 Swift 端解析出明确的
**Server Source**（`managed_local` / `external_http` / `legacy_64667` /
`service_discovery_future` / `disabled`）。新装默认 `managed_local`：CordCode Link 作为
supervisor 启动 loopback-only `opencode serve`，持久化 `4096...4196` 范围内的端口和随机
Basic Auth 凭据，health 通过后把 resolved loopback URL 通过 `-opencode-url` 传给
go-bridge；endpoint 未解析（disabled / external_http 未填 URL / managed server 启动失败）
时**不传** `-opencode-url`，go-bridge 把该 backend 的 descriptor 状态报为
`not_configured`，绝不 dial `64667`。

- agent session 与历史/模型等通用能力位于 `agent/opencode/`；`agent/opencode.New` 在
  无 URL 时进入 degraded（CLI 能力可用，HTTP 数据面返回 `ErrNotSupported` / 未配置诊断），
  不再 fallback `http://localhost:64667`。
- OpenCode server 专属的 create/resume/get/abort 等语义仍可走
  `go-bridge/opencode-proxy.go`（仅 URL 非空时注册）。
- `agent/opencode/sse_subscriber.go` 被动订阅 OpenCode SSE；无 URL 时
  `shouldStartPassiveSubscription` 直接返回 false，避免无意义重连退避（Subscribe 本身也会
  拒绝空 URL）。
- descriptor 当前仍声明 `requiresPollingForExternalTurns=true`，iOS 可保留低频历史
  探测兜底，但 SSE 健康时不应同时启动高频 recovery polling。

MacBridge 仍为 OpenCode 管理本地 Basic Auth：`managed_local` 的运行态写入独立
`opencode-managed-server.json`（`0600`，不复用用户配置语义的 `credentials.json`）；
既有 `credentials.json` 继续保存用户显式 source、外部 URL 和兼容凭据。Swift 端
`OpenCodeHealthValidator` / managed health probe 先发 no-auth `/global/health`，证明 server
要求认证（401）后再做 authed 校验；no-auth `200` 的 OpenCode server 判为
`server_unauthenticated` 必须拒绝（`legacy_64667` 例外，标
`legacy_insecure_unverified`）。Desktop 默认 server 配置同步到 resolved endpoint URL，并把
`local` 项目 scope 合并到 `projects[managedURL]`，不再固定写 `64667`。

### OpenCode hybrid 路由矩阵

当前 `handleOpenCodeRPC` 不是“全部 proxy”，也不是“全部 agent”：

| 路径 | 当前方法 |
| --- | --- |
| 通用 agent/interface dispatch | provider、models、agents、todos、usage、diagnostics、workspace diff、memory、content chunk、read file、rename/archive/delete、compression、permission mode、完整消息历史 |
| OpenCode HTTP proxy | get/list/create/resume session、list projects |
| 混合路径 | send：先用 proxy 校验 server session，再由 `agent/opencode` 发送并 relay events；abort：先通知 HTTP server，再关闭 registry session |
| 明确不支持 | share session、Bridge 代答 OpenCode permission |

新增 OpenCode 能力时，先判断它是通用 agent capability 还是 OpenCode server 专属资源。不要
为了省事把所有读写重新塞回 HTTP proxy，也不要删除 server-side abort/create 语义后假装
agent session 等价。

## 能力不是手写产品矩阵

`go-bridge/agent_descriptor.go` 根据 `core/interfaces.go` 的可选接口推导 capability。
调用方必须以 `hello_ack.backends[].capabilities` 为准，不要只按 backend 名称猜能力。

常见 capability：

| capability | 来源 |
| --- | --- |
| `model_switch`、`session_state` | 基础能力 |
| `provider_switch` | `ProviderSwitcher` |
| `session_history` | `HistoryProvider` |
| `workspace_diff` | `WorkDirSwitcher` |
| `memory_read` | `MemoryFileReader` |
| `diagnostics` | `DiagnosticsProvider` |
| `usage_reporting` | `TokenUsageReporter` |
| `permission_mode` | `ModeSwitcher` |
| `session_mutation` | rename + archive |
| `session_delete` | `SessionDeleter` |
| `todos` | `TodoProvider`，OpenCode 也显式暴露 |
| `compression` | Codex app-server |
| `question_reply` | Codex app-server |

`session_pagination` 当前刻意关闭：长会话分页曾造成 newest/backward 振荡；当前由完整历史
响应配合 8 MiB relay frame 与写 deadline 承载。重新启用必须先解决稳定游标与大内容分片，
不能只恢复 capability 字符串。

## 事件管线

agent 事件经 `go-bridge/events.go` 统一映射：

| core event | wire event |
| --- | --- |
| text / replacement | `text_delta` / `message_updated` |
| thinking | `reasoning_delta` |
| tool use/result | `tool_started` / `tool_finished` |
| plan | `todos_updated` |
| turn lifecycle | `turn_started` / `turn_completed` |
| permission | `permission_request` |
| context | `context_compressing` / `context_compressed` / `context_usage_updated` |
| Codex question | `question_asked` / `question_resolved` |

同一 session 的 direct 与 relay 客户端通过 broadcaster 订阅。连接关闭必须注销订阅；
发送方也走 broadcaster，避免“直接写 + 广播”产生双份事件。

## 已知风险与不可破坏约束

- WebSocket/auth/relay 是 agent core 之外的额外失败面；先分层定位，不同时改 driver 和客户端。
- OpenCode 仍是 hybrid path，职责边界需要显式维护。
- 控制面 secret 不能进入 agent subprocess；错误和 stderr 必须脱敏。
- direct 与 relay 必须共享 auth、撤销、RPC 和事件语义，不能长期形成两套协议。
- 公网明文 `ws://` 必须 fail-closed；Tailscale 自签名只允许已配对 SPKI pin。
- protocol 破坏性变更必须升级 major version 并同步 iOS protocol pack。
- capability 必须从真实接口推导，不为 UI 显示而声明假阳性。
- `session_pagination` 在重新设计游标/分片前保持关闭。

## 测试入口

```bash
go test ./go-bridge/... -count=1
go test ./agent/claudecode/... ./agent/codex/... ./agent/opencode/... -count=1
go test ./core/... ./config/... -count=1
(cd relay-server && go test ./... -count=1)
```

事件、rebind、broadcast、shutdown、relay mailbox 或协议变更应优先跑对应定向测试，再做
Release 覆盖安装。需要 iOS 真机交互验证时，按相邻仓库规则取得 owner 授权。

## 调试顺序

端到端同步异常时按边界取证：

1. MacBridge runtime 日志中是否收到 backend 原始事件；
2. `events.go` 是否映射出正确 wire event；
3. broadcaster 是否有目标订阅；
4. iOS 是否收到 envelope；
5. iOS 是被 live event、session state 还是 history polling 驱动。

只有确认事件在 MacBridge 前半段消失时才修改 driver；只有确认 wire 已到 iOS 后才修改
`ChatViewModel`。不要通过同时改两端制造无法归因的“看起来好了”。
