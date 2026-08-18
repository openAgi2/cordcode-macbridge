# go-bridge 当前架构与 backend 进程模型

> 本文从原一体仓库 `go-bridge 框架现状.md`、`go_bridge_使用指南.md` 中提炼，
> 以拆分后的 `cordcode-macbridge` 源码为准。旧 Node Unified Bridge、外部 `cc-connect`
> replace、Copilot sidecar 和 FRP 默认路径均已删除出当前说明。
>
> 2026-08-17 按当前树校正：默认 drivers 含 `deepseek`/`dsh-web`；补 SSV2 投影、
> 目录 catalog、Grok `session/list`、dsh-web 官方 API 转发模型。设计细节仍以
> `docs/2026-08-16-dsh-web-backend-design.md` 为准，本文只记现行结构。

## 边界

```text
iPhone / iPad
  ├─ LAN ws://Mac:8777
  ├─ Tailscale wss://100.x.x.x:8778 + SPKI pin
  └─ Relay wss://relay... + HPKE
            │
            ▼
cordcode-bridge-runtime
  ├─ protocol/auth/pairing/relay/projection: go-bridge/
  ├─ agent interfaces: core/
  ├─ agent implementations: agent/{claudecode,codex,grokbuild,opencode,dsh,dsh-web}/
  └─ local configuration: config/
```

`core/`、`config/`、`agent/` 已迁入本仓库，不再依赖原一体仓库或本机绝对路径
`replace`。wire 协议适配留在 `go-bridge/`，agent 的进程、历史、模型和能力实现放在
`agent/` 与 `core/`。

产品 runtime 默认 `-drivers` 为
`claude,opencode,codex,grokbuild,dsh-web`。flag 里的 id 与 Go 包名
不完全相同：`claude` → `agent/claudecode`，`dsh-web` 包名是 `dshweb`、
wire kind 是 `deepseek-web`。旧 `deepseek` → `agent/dsh` 仍可显式挂上，
但默认不再注册（2026-08-17 产品入口退役，源码保留）。

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
- DeepSeek Web 是官方 `dsh web` HTTP/WebSocket 的转发器 + bridge-v1 翻译器，
  不发明模型名、标题或工作区归组；
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
`cordcode-bridge-runtime`，读取 stdout bootstrap frame，并交叉验证
`runtime.json`、PID、bridge epoch 和 Management URL。

关停顺序为：

1. 停止接受新 HTTP/WebSocket 请求；
2. `Handlers.Shutdown` 关闭活跃 agent session；
3. 关闭 direct/relay 连接；
4. 停止 relay、TLS 与 Management 服务。

Claude / Codex / Grok catalog 与 dsh 子进程使用进程组回收；dsh-web 只杀自己
spawn 的 managed 实例，从不碰用户自己的 3080。不要通过增加后台孤儿进程或忽略
shutdown 错误来“提高可用性”。

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
- runtime 以 `--include-partial-messages` 启动 Claude CLI，流式 partial 进入 `text_delta` /
  `message_updated` 路径；
- Mac 端另一个 Claude 进程发起的外部 turn 不共享事件总线；
- descriptor 使用 `liveEvents=session_process`，并声明
  `requiresPollingForExternalTurns=true`；
- 历史来自 `~/.claude/projects` JSONL；resume 时用 `historyDraining` 抑制历史重放伪装成新
  live delta；支持 rich history、todos、memory、usage、diagnostics、session mutation、
  content chunking 和 permission resolve，具体能力由接口断言决定。
- 模型/effort 真值优先来自 `~/.claude/settings.json`（`CLAUDE_CONFIG_DIR` 可覆盖）中的
  `ANTHROPIC_DEFAULT_{HAIKU,SONNET,OPUS}_MODEL`、`*_MODEL_NAME` 与 effort 设置，mtime 懒重载；
  iOS 显示名和发送给 Claude 的别名必须按该映射处理。
- **list_sessions 的 enrichment 边界（CPU 关键路径）**：`handleListSessions` 用
  `getRunningMap(ctx, agent)` 一次性算出当前 running 集，再用 list-safe 批量 enricher
  `enrichSessionStatesForList` 给所有列出的 session 行打 `runtimeState`。该路径**不得**对任何
  session 行打开/解析 transcript（不调 `findClaudeSessionFile`/`detectClaudeTranscriptState`/
  `isSessionExecuting`），**不得**调 `h.sessions.markIdle`（read-only），**不得**写 `/tmp` 调试转储。
  running map 来自 `GetRunningSessionIDs`（精确语义：活但空闲的进程不算 running），经 2s TTL
  `runningMapCache` 缓存；burst 刷新只触发一次 `GetRunningSessionIDs`，MacBridge 拥有的 turn
  状态迁移（send_message/turn_completed/abort/process exit，统一走 `sessionRegistry.markRunning/
  markIdle` 的 `onStateChange` 回调）立即失效缓存。外部启动的 Claude turn（无 owned registry
  条目）在 ≤1 个 TTL 窗口后被探测到——`GetRunningSessionIDs` 的 K（live Claude PID 数，通常 1-3）
  次 `isSessionExecuting` 调用结果按 `sessionID+path+size+mtime`（**size 和 mtime 必须同时比较**）
  缓存，把冷缓存代价收敛到「变化的 live transcript 数」而非 K。`reasoningEffort` 注入是廉价内存
  getter，list-safe 路径必须保留（`injectClaudeReasoningEffort`）。详细的 single-session 检视
  （transcript fallback）只属于 `get_session`/`get_session_messages` 等 detail 路径
  （`enrichSessionStateWithAgent`），不属于 list 热路径。

因此 iOS 不能把“Bridge 有 liveEventStream”误解为“Claude 的所有外部 turn 都会广播”。

> [!NOTE]
> `relayEvents` 对会跨 turn 存活的 backend 不在 `turn_completed` / 空闲时退出：
> `relaySurvivesTurnBoundary` 当前覆盖 `claude` / `claudecode` / `dsh-web`。
> Claude 是长生命周期 CLI；dsh-web 是常驻 mux 绑定，下一轮审批/问答仍走同一
> `Events()` 通道。`Close()` 必须关闭 `Events()`，否则下一轮 send 会新建 session
> 对象而 `startRelayIfNotRunning` 空转，iOS 收不到后续审批。
> 另：`disablesRelayIdleTimeout` 对 claude / claudecode / codex / opencode /
> dsh-web 关闭 60s 空闲收口——dsh-web 审批等待期间 mux 不再吐 `text_delta`，
> 空闲超时会把还在等权限的 turn 提前封口。

### Codex

支持两种模式：

- `exec`：由 runtime 启动 Codex CLI session；
- `app_server`：通过 Codex app-server 协议运行 session。产品 `RuntimeConfig` 默认不显式
  提供 URL，因此 agent session 通过 stdio 启动自己的 app-server；只有显式配置
  `-codex-app-server-url` 时，才连接共享 TCP service 并通过 passive subscriber 接收外部
  thread/turn 事件。

`app_server` 的 create 是 lazy：可能先返回 `pending-*`，首次 send 后再绑定真实
thread id。session registry、订阅 key 与 iOS 当前 session 都必须随 rebind 更新。

默认 stdio app-server 模式下，descriptor 对 Codex 使用 `session_process` 模型，
`requiresPollingForExternalTurns=false`（`go-bridge/agent_descriptor.go:105`，commit `19250fe`
"fix(codex): stop polling idle history sessions"）——transcript relay 已发权威
`turn_started`/`turn_completed` milestone，polling 反会把 transcript rewrite 误判为新 turn。iOS 仍通过
`switchSession` + live-event 驱动 history 变化探测旁观外部 turn，**不依赖**此 flag（codex 无 claude
那样的 flag 兜底）。显式共享 URL 模式下才使用 broadcast/passive event。

Codex 另有 transcript file relay：`get_session_messages` 会并行启动
`startCodexSessionFileRelay`，从 JSONL 中的 `task_complete` 等持久事件补齐外部/共享服务 session
的真实完成信号。它使用独立 relay key，不替代标准 agent session relay。

共享 app-server 模式检查：

```bash
command -v codex
lsof -nP -iTCP:4141 -sTCP:LISTEN
ps aux | grep '[c]odex app-server'
```

MacBridge Restart 只重启 Bridge runtime，不负责重启外部共享 Codex app-server。
共享服务的启动归属和本机常驻约束见
[BUILD_INSTALL_AND_RUNTIME.md](BUILD_INSTALL_AND_RUNTIME.md#codex-app-server-的启动归属)。

### Grok Build

Grok Build 由 `agent/grokbuild` ACP driver 提供，产品 runtime 默认注册名为 `grokbuild`
（界面显示为 Grok Build）。

- 每个 turn 仍是独立 `grok agent` stdio 子进程（`LiveEventSessionProcess`）。
- 会话目录走进程级单例 catalog 子进程：`grok agent --no-leader stdio` 上的
  `session/list`（握手未声明 list 能力则 fail-closed，不再静默读本地
  `~/.grok/sessions`）。上游 list **不按 cwd 过滤**；iOS「查看更多」的
  directory 范围由 go-bridge `filterWireSessionsByDirectory` 过滤。
- descriptor：`requiresPollingForExternalTurns=true`（外部 turn 仍要 polling /
  `updates.jsonl` tailer 兜底；leader-socket 订阅尚未取代这一声明）。
- 能力仍由 `core` 可选接口和 `WireDescriptor` 推导，客户端不得只按名称猜。

### DeepSeek（`deepseek` → `agent/dsh`，产品入口已退役）

旧 SDK stdio 路线。源码与 `dsh-web` **并行保留、互不 import**，但 **默认
drivers 不再注册**，iOS 将其标为 deprecated。会话数据在 `~/.dsh/sessions`，
DeepSeek Web 经官方 API 可读可续。不要在这条路上再扩功能。

每个活跃 session 一个 `dsh-jsonrpc-agent`（或用户全局 npm `dsh` 的
shadow-tree 启动）子进程，协议是 SDK JSON-RPC 2.0 over stdio，不是官方 web API。

- wire kind `deepseek`，iOS `BackendKind.deepSeek`。
- 会话日志可读用户 `~/.dsh/sessions`（zstd / 明文）；冷投影走 pathless
  rich-history 重建。
- `LiveEventSessionProcess`，`requiresPollingForExternalTurns=false`（没有
  跨进程共享事件总线，外部 Mac web turn 不会进这个 driver）。
- 与官方 web 共写同一 store 时必须对齐编码（zstd）和模型白名单；新接入应优先
  dsh-web，不要在这条路上再扩官方 web 才有的审批/问答/归组。

### DeepSeek Web（`dsh-web` → `agent/dsh-web`，包名 `dshweb`）

官方 `dsh web` 的请求转发器 + bridge-v1 翻译器。iOS 几乎零新渲染逻辑；标题、
模型、权限预设、上下文用量、工作区归组都取自官方 API，MacBridge 不发明这些事实。

**实例生命周期（探测复用优先）：**

1. 启动时后台 `host.describe` 探测用户自己的实例（默认 `127.0.0.1:3080`）；
2. 未命中则 managed spawn：`dsh --profile web --host 127.0.0.1 --port 3096…3196`，
   状态写入 data dir 的 `dsh-web-managed-server.json`（0600，无凭据——dsh v1
   无认证面）；
3. 两态都失败 → hello_ack `not_configured`，不代装。

hello_ack 的 `detectDSHWebInstance` 只镜像 driver 已解析的实例状态，自己不再
探测或 spawn。

**两条常驻 WebSocket（官方 v1 无 `since`，重连 = 重开流 + history/forceCold）：**

| 流 | 作用 |
| --- | --- |
| `GET /api/events.mux` | 全会话 `session/event`（与磁盘日志同构）。`assistant/chunk` 的 `text-delta` 即真流式打字机。绑定中的 session 走该 `dshSession.Events()`；其余走 agent 级 passive 通道（外部 Mac web turn 同样直播）。 |
| `GET /api/events.host` | `session-added/removed/status`、`workspace-changed` → `CatalogRefreshSignaler` 立即重扫 catalog，不必等 60s discovery。 |

**会话列表与归组：**

- `session.list` → `ListSessions`：滤掉 `origin=subagent` / `parentSessionId` /
  `blank`；标题优先 `projections.values.title`，否则 history 尾读。
- 分组键不是 cwd。官方侧栏按 `workspace.list` 的 `sessionIds` 名单归组；
  不在名单里的进「未分组」；`archivedSessionIds` 打 `archivedAtMillis`，iOS 隐藏。
- 在 Chat / cordcode-ios 这种已登记目录新建：`session.create` 必须带
  `workspaceId`（官方 **只有** 带 workspaceId 才会 `attachSession`）。只传 cwd
  能聊，但会话进「未分组」。cwd 命中已登记 path 时 create 改传 workspaceId。
- `list_sessions` 带 `directory`（iOS「查看更多」）时，generic 列表路径必须
  `filterWireSessionsByDirectory`，否则 sheet 会混进所有工作区。

**审批 / 问答 / 权限 / 用量：**

- 权限：官方 mux `approval/requested` → `permission_request`；应答
  `POST /api/respond`（first-writer-wins）。日志里的 `approval/asked` 只是审计，
  不进 codec。
- 问答：权威事件是 `user_input_requested`（SSV2 投影 / UserInputDock）。
  `question_asked` 是 derived-legacy，EventPublisher **不 ingest**。
- 齿轮权限模式：`ModeSwitcher` 走官方 `POST /api/commands/execute`
  `{args:{agentId,line:"/permission <preset>"}}`，**禁止**当用户消息
  `session.prompt`（否则模型会回「我无法改沙箱」）。
- Agent 预设（标准/PTC/极简/创造）：`agentPreset.list` / `session.create.agentPreset`；
  空白会话才能 `agentPreset.select`。不是权限档。`session.list` /
  `get_session` 带官方 `agentPreset`，iPhone 标题旁显示「标准模式」芯片。
- 上下文表：官方 projections 的 `contextPressure` /
  `contextBreakdown`（system/tools/messages），以及 StatsLine 的
  `sessionStats` / `tokenUsage`（轮次、耗时、缓存命中、计费
  in/out）；`get_session` 带 `contextUsage`，tail 与
  `session/projection` 增量都发 `context_usage_updated`。iOS 点 ⭕
  看同一张表，不另做输入框底下那一行。

**其它 backend 的占用（2026-08-17，仅已用/窗口，没有 Harness 拆分）：**

- Codex：`GetSessionContextUsage` 读 rollout `token_count` + `model_context_window`。
- Grok Build：真实落盘 **没有** `usage_update`。占用在 `signals.json` 的
  `contextTokensUsed` / `contextWindowTokens`（本机实测窗口 500K）。
  `auto_compact_started` 也会带同一对字段。`turn_completed.usage.inputTokens`
  是回合计费累计，不能当占用。
- OpenCode：`GET /session/:id` 的 `tokens` + `/provider` 里模型
  `limit.context`。占用 = `input + cache.read + cache.write`。
- Claude Code：JSONL 没有官方窗口。启发式占用 = 最后一条 assistant
  `input_tokens + cache_read + cache_creation`；窗口 200K，模型名含 `1m`
  则 1M；`--max-context-tokens` 优先。
- 冷投影：`session.history` 按官方 `maxMessages=50` 分页（消息边界，不是
  事件条数）。长会话一页过大时缩小再拉，避免 32MiB unary 截断。

**descriptor：**

- kind `deepseek-web`，显示名 DeepSeek Harness（iOS 切换框用这个名字；底层仍是 dsh-web）。
- `LiveEventBroadcast`，`requiresPollingForExternalTurns=false`。
- StaticCapabilities：`external_turn_streaming`、`question_reply`、
  `structured_user_input_v1`。phase 1 不声明附件。
- 投影：pathless 家族，冷基线 = 官方 `session.history`，**不进** deepseek
  的 store-file 分支。live/kernel 会话以 kernel 为基线，重建只服务冷开。

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

常见 capability（`deriveBackendCapabilities` + 各 driver `WireDescriptor.StaticCapabilities`）：

| capability | 来源 |
| --- | --- |
| `model_switch`、`session_state` | 基础能力 |
| `provider_switch` | `ProviderSwitcher` |
| `session_history` | `HistoryProvider` |
| `workspace_diff`、`supports_workspace_browse` | `WorkDirSwitcher` |
| `memory_read` | `MemoryFileReader` |
| `diagnostics` | `DiagnosticsProvider` |
| `usage_reporting` | `TokenUsageReporter` |
| `permission_mode` | `ModeSwitcher`（含 dsh-web 官方 `/permission` 预设） |
| `session_mutation` | rename + archive |
| `session_delete` | `SessionDeleter` |
| `session_pin` | `SessionPinner`（独立于 mutation；Codex/OpenCode/dsh-web 可只有 pin） |
| `content_chunking` | Claude `StaticCapabilities`，配合 `fetch_content_chunk` |
| `permission_resolve` | `ToolAuthorizer`；OpenCode/Codex 不宣告；dsh-web 宣告（`/api/respond`） |
| `todos` | `TodoProvider` |
| `compression` | Codex `app_server` |
| `question_reply` | Codex `app_server` 在 derive 里加；Claude / dsh-web 走 `StaticCapabilities` |
| `structured_user_input_v1` | Codex `app_server`，或 `StructuredUserInputProvider`；dsh-web 也在 StaticCapabilities |
| `external_turn_streaming` | 各 driver `StaticCapabilities`（Claude transcript / Codex file-relay / dsh-web mux） |
| `supports_checkpoint` / `supports_commit_message` / `supports_pull_requests` | 对应 opt-in 接口；未实现则不宣告 |

`session_sync_v2` 是 **连接级** capability（hello 里声明、hello_ack 回显），不是
backend 行上的 capability。它打开 Session Projection Stream，与上面的 per-backend
能力正交；iOS 对结构化问答自行 AND `session_sync_v2` + `structured_user_input_v1`。

`session_pagination` 当前仍不向客户端宣告：稳定游标与 transcript-index 分页实现已经存在，
但 backward paging 曾造成 newest/backward UI 振荡，产品路径仍走完整历史 fallback。重新启用
不能只恢复 capability 字符串，必须同时证明 iOS 合并/滚动、relay 帧预算和超大内容分片都稳定。

## transcriptindex 与消息分页

`transcriptindex/` 是边界安全的 transcript 页面索引层，被 Claude/Codex 文件历史读取路径使用：

- `core.TranscriptLocator` 让 agent 暴露 session 对应 JSONL 文件；
- `go-bridge/pagination.go` 用 `MessageCursor{SessionID, Ordinal, Generation}` 做稳定 cursor；
- 每页同时受 message limit 与约 256 KiB wire-byte budget 约束，避免单页过大导致移动网络或 relay
  frame 失败；
- cursor stale 只在前缀被重写/截断/替换时返回，尾部 append 不应使旧 cursor 失效。

当前实现可在客户端显式传 `paginate=true` 时服务页面，但由于 capability 未宣告，生产 iOS 不应默认
依赖该路径。

## 事件管线

agent 事件经 `go-bridge/events.go` 统一映射：

| core event | wire event |
| --- | --- |
| text / replacement | `text_delta` / `message_updated` |
| thinking | `reasoning_delta` |
| tool use/result | `tool_started` / `tool_finished` |
| plan | `todos_updated` |
| turn lifecycle | `turn_started` / `turn_completed` |
| permission | `permission_request` / `permission_resolved` |
| context | `context_compressing` / `context_compressed` / `context_usage_updated` |
| 旧问答 | `question_asked` / `question_resolved`（derived-legacy；EventPublisher 不 ingest `question_asked`） |
| 结构化问答 | `user_input_requested` / `user_input_resolved`（SSV2 / UserInputDock 权威面） |

同一 session 的 direct 与 relay 客户端通过 broadcaster 订阅。连接关闭必须注销订阅；
发送方也走 broadcaster，避免“直接写 + 广播”产生双份事件。

### iOS live event vs history polling 消费边界

第四轮架构健康专项（最终轮）后，iOS 在 `ChatViewModel` 层用一个显式 turn sync
policy（`ChatTurnSyncPolicy` + `ChatTurnSyncState`）决定 live event、history sync、
running-session polling、session switch 之间的互斥与优先级。MacBridge 不改变 wire
语义，但理解 iOS 侧的下列消费边界有助于排查“live 与 history 竞争”类问题：

- **Claude Code**：CLI 子进程的 live stream 只能被本进程 stdout 捕获，没有跨 session
  共享 live event bus。MacBridge 不会重放其他 Terminal 中 Claude turn 的事件；iOS 只能
  通过共享 JSONL 历史的 history polling 旁观外部 turn。因此 Claude 在 iOS 本地发送
  进行中（`.localSend` ownership）时，普通 history sync 会被 policy **defer**
  （`.deferBecauseLocalLiveTurn`），避免迟到权威历史覆盖正在流式增长的 live partial；
  只有显式 final reconcile（send completion / turn completion）才允许权威加载。
- **Codex**：app-server live event 是权威的；iOS 在 `.localSend` 时普通 history sync
  走 **merge-only**（`.mergeOnlyBecauseRemoteRunning`，以 baseline 为锚幂等合并），
  不清空 live partial。
- **OpenCode**：SSE live event 优先，descriptor 决定 polling 兜底；与 Codex 同样走
  merge-only 直通。
- **DeepSeek Web**：mux 是 agent 级广播，覆盖本机 web 发起的外部 turn，不需要
  external-turn polling。iOS 必须把它放进 SSV2 投影族；空 kernel 时先用官方
  history 播种，不得把 live 会话收成空基线。审批等待期无 text_delta，relay 不得
  因空闲超时封口。

ownership 的读写与 history apply 前复核均在 iOS `@MainActor` 边界内完成，并有定向
交错测试覆盖（`RemoteRunningSessionTests.testClaudeCodeInterleave_*`）。MacBridge 的
send/stream 语义不为此做 backend-specific 重复抑制——iOS 侧的 race 由 iOS policy 收敛。

流式粒度（产品手感，不是 bug）：dsh-web 的 `assistant/chunk text-delta` 与官方 web
同一条细流，iPhone 呈打字机；Claude CLI 的 `content_block_delta` 常按词/句攒批；
Codex 产品路径多见 `item.completed` 整段落地；Grok ACP `agent_message_chunk` 通常
是一小段。不要按 DeepSeek Web 的逐字效果去改其他 backend 的上游契约。

## Session Projection（SSV2）

协商到 `session_sync_v2` 的客户端以投影为消息页真相源：`get_session_projection`
拉基线，`projection_patch` 推增量。kernel + reducer 在 `go-bridge/projection_*.go`。

冷加载家族：

| 家族 | backend | 基线 |
| --- | --- | --- |
| transcript JSONL | claude / claudecode / Codex | `TranscriptLocator` + transcriptindex |
| pathless HTTP/rich-history | opencode / grokbuild / deepseek / **dsh-web** | OpenCode HTTP、本地 grok/dsh 日志、或官方 `session.history` |

规则：live/kernel 已有状态的会话以 kernel 为基线，file/HTTP 重建只服务冷开或脱活
会话。forceCold 集合与 `backendSupportsProjectionHydrate` 必须同时列入新 backend，
漏一处对应机制静默失效。dsh-web **不进** deepseek 的 store-file / live-only
admission 回退（会话在官方服务端常驻，mux 即时到达）。

## 会话目录（list_sessions）

- **全局首页**：Claude / Codex / Grok 走 fair-home（每 directory 最多 K=5 +
  `directoryTotals`），避免单项目吃光配额。iOS 侧栏「查看更多」再发
  directory-scoped 分页。
- **directory-scoped**：必须按该 directory 过滤后再分页。Claude 用 projectKey；
  Codex/Grok 从全局快照 `filterWireSessionsByDirectory`；dsh-web / deepseek 等
  generic `handleListSessions` 同样要过滤——漏掉则「查看更多」混进全部工作区。
- **sessions_changed**：`session_discovery.go` 按 catalog 指纹周期重扫（默认
  60s）。Codex 另有 3s recency-head；Grok 在有客户端连接时 5s 全量指纹；
  dsh-web 由 host 流信号立即重扫，不另开快轮询。

## 已知风险与不可破坏约束

- WebSocket/auth/relay 是 agent core 之外的额外失败面；先分层定位，不同时改 driver 和客户端。
- OpenCode 仍是 hybrid path，职责边界需要显式维护。
- `agent/dsh` 与 `agent/dsh-web` 并行、禁止互相 import；新功能接官方 web 面，不要在 SDK
  stdio 路线上重做归组/审批。
- dsh-web 不得盲写 `~/.dsh/workspace.json`（两写恢复协议，外部进程不可代写）。
  归组只走官方 `session.create{workspaceId}` / `workspace.list`。
- 控制面 secret 不能进入 agent subprocess；错误和 stderr 必须脱敏。
- direct 与 relay 必须共享 auth、撤销、RPC 和事件语义，不能长期形成两套协议。
- 公网明文 `ws://` 必须 fail-closed；Tailscale 自签名只允许已配对 SPKI pin。
- protocol 破坏性变更必须升级 major version 并同步 iOS protocol pack。
- capability 必须从真实接口推导，不为 UI 显示而声明假阳性。
- `session_pagination` 在 UI 合并/滚动、relay 帧预算和超大内容分片重新验收前保持关闭。
- `list_sessions` 是只读 UI 热路径：per-row transcript 打开数必须为 0、不得 `markIdle`、
  不得写 `/tmp`；transcript 推理只能出现在 detail 路径或 `GetRunningSessionIDs` 的 live-PID
  有界检查里，不能回到 list 热路径。transcript-state 缓存的指纹必须 size + mtime 同时比较。
- generic / dsh-web 的 directory-scoped `list_sessions` 必须过滤，不得把全量 catalog
  当成某一个工作区的「查看更多」。

## 测试入口

```bash
go test ./go-bridge/... -count=1
go test ./agent/claudecode/... ./agent/codex/... ./agent/grokbuild/... ./agent/opencode/... -count=1
go test ./agent/dsh-web/... ./agent/dsh/... -count=1
go test ./core/... ./config/... -count=1
(cd relay-server && go test ./... -count=1)
```

事件、rebind、broadcast、shutdown、relay mailbox 或协议变更应优先跑对应定向测试，再做
Release 覆盖安装。需要 iOS 真机交互验证时，按相邻仓库规则取得 owner 授权。

## 调试顺序

端到端同步异常时按边界取证：

1. MacBridge runtime 日志中是否收到 backend 原始事件（dsh-web 还要看 mux/host 是否连上 3080）；
2. `events.go` / codec 是否映射出正确 wire event（dsh-web 审批看 `permission_request`，问答看 `user_input_requested`）；
3. 投影 kernel 是否 ingest、SSV2 客户端是否吃 patch 而不是只等 raw live；
4. broadcaster 是否有目标订阅，`relayEvents` 是否因空闲/turn 边界提前退出；
5. iOS 是被 live event、投影、session state 还是 history polling 驱动。

只有确认事件在 MacBridge 前半段消失时才修改 driver；只有确认 wire 已到 iOS 后才修改
`ChatViewModel`。不要通过同时改两端制造无法归因的“看起来好了”。
