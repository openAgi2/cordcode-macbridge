# go-bridge 当前架构与 backend 进程模型

> 本文从原一体仓库 `go-bridge 框架现状.md`、`go_bridge_使用指南.md` 中提炼，
> 以拆分后的 `cordcode-macbridge` 源码为准。旧 Node Unified Bridge、外部 `cc-connect`
> replace、Copilot sidecar 和 FRP 默认路径均已删除出当前说明。
>
> 2026-08-17 按当前树校正：默认 drivers 含 `deepseek`/`dsh-web`；补 SSV2 投影、
> 目录 catalog、Grok `session/list`、dsh-web 官方 API 转发模型。设计细节仍以
> `docs/2026-08-16-dsh-web-backend-design.md` 为准，本文只记现行结构。
>
> 2026-09-02 按当前树校正：backend 名册扩到 9 个注册 backend（新增
> `codex-web` / `opencode-web` / `codex-remote`，见各自设计文档）；根目录
> `config/` 已不存在；默认 drivers 按「产品 lineup vs runtime fallback」双真值
> 记录；SSV2 冷加载家族、relay 空闲/turn 边界开关、capability 表与 turn detail
> 懒加载（2026-08-30 终案）同步到现行源码。

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
  ├─ transcript page index: transcriptindex/
  ├─ session pin store: pinstore/
  └─ agent implementations: agent/{claudecode,codex,codex-remote,codex-web,grokbuild,opencode,opencode-web,dsh,dsh-web}/
```

`core/`、`agent/`、`transcriptindex/` 已迁入本仓库，不再依赖原一体仓库或本机绝对路径
`replace`。wire 协议适配留在 `go-bridge/`，agent 的进程、历史、模型和能力实现放在
`agent/` 与 `core/`。根目录已无 `config/` 目录（2026-09-02 校正）。`agent/codex-appserver/`
只是 app-server JSON-RPC 客户端库（无 `RegisterAgent`），不是 backend；
`agent/providerseedtest/` 是测试辅助包。

**默认 drivers 是双真值（2026-09-02 校正）：**

- **产品 lineup**（`MacBridge/MacBridge/Services/RuntimeManager.swift` 默认传给
  runtime 的 `-drivers`）：`claude,codex-web,codex-remote,grokbuild,dsh-web,opencode-web`。
- **go-bridge fallback**（`go-bridge/main.go` `defaultDrivers`，无 Swift 传参时）：
  `claude,codex,codex-web,grokbuild,dsh-web,opencode-web`。

两者差异：产品 lineup 不含旧 `codex`（exec/app_server）与 `opencode`、`deepseek`；
runtime fallback 仍含旧 `codex` 但不含 `codex-remote`。以产品实际传入为准排查。

flag 里的 id 与 Go 包名/注册名不完全相同：`claude` → 注册名 `claudecode`，
`deepseek` → 注册名 `dsh`（别名表在 `go-bridge/main.go`），其余同名注册；
`dsh-web` 包名是 `dshweb`、wire kind 是 `deepseek-web`。旧 `deepseek` → `agent/dsh`
源码保留、仍可显式挂上，但产品 lineup 已退役（2026-08-17）。

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
- DeepSeek Web、Codex Web、OpenCode Web、Codex Desktop 都是官方服务的
  转发器 + bridge-v1 翻译器，不发明模型名、标题或工作区归组；
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

publisher 在 ordering lock 内做两件事（`go-bridge/event_publisher.go`）：为事件打
`perSessionSeq`/bridgeEpoch 时间戳，并经 `kernel.IngestLive` 摄入 SSV2 投影
kernel——revision 只在 reducer 提交变更时前进（与 transport perSessionSeq 是两个
序列）；live 路径的 patch flush 只在 `ProjectionIngestApplied` 时触发，NoChange
（重复/空转）不 flush。catalog 控制面事件与 pre-reduced timeline 事件有意不进
timeline；derived-legacy `question_asked` 永远不能成为第二个 projection writer。

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
> `relaySurvivesTurnBoundary` 当前覆盖 `claude` / `claudecode` / `dsh-web` /
> `codex-web` / `codex-remote`。Claude 是长生命周期 CLI；dsh-web 是常驻 mux 绑定，
> codex-web / codex-remote 是常驻官方连接（daemon 订阅 / Remote Control
> controller），下一轮审批/问答仍走同一 `Events()` 通道。`Close()` 必须关闭
> `Events()`，否则下一轮 send 会新建 session 对象而 `startRelayIfNotRunning`
> 空转，iOS 收不到后续审批。
> 另：`disablesRelayIdleTimeout` 对 claude / claudecode / codex / codex-web /
> codex-remote / opencode / dsh-web / opencode-web 关闭 60s 空闲收口——dsh-web
> 审批等待期间 mux 不再吐 `text_delta`，空闲超时会把还在等权限的 turn 提前封口；
> opencode-web 同理（评审 M2-2，审批等待期无 text_delta）；codex-web 的官方 turn
> 事件只发给仍订阅的 connection，idle timeout 拆掉观察连接会让 Mac Desktop 的
> 后续 delta 在补订阅完成前被丢弃（owner 2026-08-22 阶段 A）。

### Codex（`codex`，产品 lineup 已退役；runtime fallback 仍默认注册）

> 2026-09-02 定位注记：产品 lineup 已不含 `codex`——iPhone 产品面由
> `codex-web`（官方 Codex Web daemon）与 `codex-remote`（Codex Desktop /
> Remote Control）承接。本节描述的 exec/app_server 双模式仅在显式挂载或
> runtime fallback 下生效，是排查旧部署时的参考路径。

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

### Codex Web（`codex-web` → `agent/codex-web`，产品 lineup 默认）

官方 Codex Web 共享 daemon 的 JSON-RPC 客户端 backend。独立身份、独立注册
（不覆盖、不别名到旧 `codex`——2026-08-24 事故红线：iOS 映射器把两个类型当未知
类型跳过，曾导致两个后端同时从真机消失）。

- **来源**：产品默认空 URL = 复用官方 daemon（managed start），失败可见，禁止回落
  到 Desktop 无法连接的第二个 loopback app-server；显式 `-codex-web-app-server-url`
  仅用于隔离测试。它与旧 `codex` 的 `-codex-app-server-url` 是不同键、不同回退语义，
  绝不共用。
- **事件模型**：共享 daemon 上的订阅连接收到同一官方 thread 的全量 turn/item
  事件；Mac 官方客户端（默认配置 TUI）发起的外部 turn 实时旁观成立。kind
  `codex-web`，`LiveEventBroadcast`，`requiresPollingForExternalTurns=false`。
- **StaticCapabilities**：`external_turn_streaming`、
  `usage-source: rollout-tail-experimental`（冷用量读官方 thread/read 返回的 rollout
  尾部 token_count；官方提供冷用量 RPC 后退役）。
- **结构化输入**：`StructuredUserInputProvider`（request adapter + 官方 v2
  responder + canonical interaction producer 同开才宣告）。
- **冷投影**：官方 app-server `thread/read(includeTurns)`（官方 identity，无
  rollout 路径）。
- 设计与证据：`docs/2026-08-21-codex-web-backend-design.md`（Phase 0 Gate 实证）。

### Codex Desktop（`codex-remote` → `agent/codex-remote`，产品 lineup 默认）

官方 Remote Control 链路的 backend，显示名 **Codex Desktop**（Mac App 与 iOS
一致）：

```text
ChatGPT Desktop 私有 app-server
  → OpenAI Remote Control relay
  → 独立 enrollment 的 MacBridge controller
  → 普通 app-server JSON-RPC 流
```

- 独立 controller enrollment：ChatGPT「电脑」页配对（device key + JWT，可独立
  撤销），绝不修改 ChatGPT Desktop 本体；standalone app-server、fake relay、
  rollout/JSONL tail、同账号 history polling 都不能替代这条链路。
- kind `codex-remote`，`LiveEventBroadcast`，`requiresPollingForExternalTurns=false`。
- 历史/turn 明细走官方分页远程 API；turn detail 懒加载能力见
  [Session Projection（SSV2）](#session-projectionssv2) 一节。
- 设计与阶段证据：`docs/2026-08-26-codex-remote-backend-implementation-plan.md`、
  懒加载终案 `docs/2026-08-30-codex-remote-lazy-history-implementation-plan.md`。

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
- 会话列表加速：`Agent` 实现 `core.CatalogRefreshSignaler`——存活的 leader 订阅
  连接收到 machine-wide `x.ai/sessions/changed` roster 广播（上游 roster.rs
  `RosterChanged`，wire `_` 前缀与裸形态均识别）即触发 discovery watcher 立即
  权威指纹重扫 → `sessions_changed`；buffered-1 通道合并多订阅连接的重复广播。
  失效信号语义：不本地应用 roster 增量，指纹 diff 拥有 fence/seen/publish 真值；
  5s grok fast poll 与 60s safety scan 保留（无订阅连接时 roster 不可达，如纯
  侧栏浏览无打开会话）。
- 模型/effort（grok 1.0.13 实证，2026-09-02）：initialize 响应 `_meta.modelState`
  是官方目录真值（`availableModels[]` 含 `_meta.{supportsReasoningEffort,
  reasoningEfforts[]}`；用户自定义 provider 以普通条目出现）。`Agent` 采纳后
  `AvailableModels` 目录优先、实现 `core.ModelEffortCatalog`（per-model 档位，
  handleListModels 下发 `supportedReasoningEfforts`/`defaultReasoningEffort`，
  iOS 零改动）。显式选择经 `session/new` params `_meta.{modelId,reasoningEffort}`
  （仅显式时携带）；load 恢复/中途切换走 `session/set_model`（**snake-case**，
  camelCase 返回 -32601；`modelId` 服务端必填，effort-only 漂移须重发当前模型，
  真值来自 session/new|load 响应顶层 `models`）。Send 前漂移检查失败=硬失败（turn
  不发出）；loadSession 后失败=软（会话健康优先）。无效值 fail-closed。
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

**实例生命周期（权威端口/座位模型，2026-08-19 设计
`docs/2026-08-19-dsh-web-canonical-3080-instance-design.md`）：**

1. **座位 = 探测列表首位**（默认 `127.0.0.1:3080`；用户显式配置 `dsh_web_url`
   则配置端口即座位）。座位是唯一实例位子——**端口即身份**，应答即用，无论谁
   拉起（3096–3196 私有端口区间已退役，旧孤儿由一次性迁移清理按 PID 安全收尸）；
2. **失联 → 宽限 120s**（`lostAt` 置位）：不收养、不补拉，`Resolve` 返回类型化
   `ErrInstanceReconnecting`，handler 映射 wire 码 `backend_unavailable`
   （send 三处 + list 四处；**禁止** `not_configured`——hello 侧
   `InstanceStatus` 宽限内特判 `available=true` + reconnecting detail，防止
   `detectInstanceStatusProber` 把 `available=false` 一律折成 `not_configured`）；
   running 会话在边沿收一条 turn 终态错误事件（边沿序号幂等，坑 8 红线）；
3. **冷启动（本进程从未持有）或宽限到期 → 在座位上补拉**：
   `dsh --profile web --host 127.0.0.1 --port <座位端口>`（安全红线不变：仅
   loopback、永不 `--trusted-host`/`0.0.0.0`）；spawn 失败按 PID/端口占用如实
   报错并 5s 退避重试；
4. 锁纪律：`resolver.mu` 只护缓存字段，探活/spawn 锁外，single-flight +
   失联探活 ≤1s 负缓存；补拉期间并发 RPC 立即 `backend_unavailable` 不阻塞；
5. **Link 退出不杀座位实例**（不杀+下次收养）：浏览器跨 bridge 重启不断流；
   升级 dsh 后手动重启一次（诊断可见 pid + ps 启动时间）。

`run_diagnostics` 是**会突变**的判别手段（内部走 `Resolve`，可能触发补拉）；
只读判别用 `lsof` + `dsh-web-managed-server.json` + `InstanceStatus`。
`dsh-web-managed-server.json`（0600，无凭据）只写不读：诊断与迁移清理消费，
解析不读。

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

### OpenCode（`opencode`，产品 lineup 已退役；显式挂载仍可用）

> 2026-09-02 定位注记：产品 lineup 已不含 `opencode`——iPhone 产品面由
> `opencode-web`（官方 `opencode serve` Web API）承接。本节的 managed_local /
> server source / proxy 混合路径描述的是该 legacy backend，显式 `-drivers` 挂载
> 或旧部署排查时适用。

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

### OpenCode Web（`opencode-web` → `agent/opencode-web`，产品 lineup 默认）

官方 `opencode serve` Web API backend，与 legacy `opencode` 并行、独立身份。配置读
自己的键（`opencode_web_url` / `opencode_web_user` / `opencode_web_pass`），**绝不
复用** `opencode_url` 作为来源。

- **事件模型**：`/global/event` SSE 是 server 级广播，覆盖每一个 session——包括
  用户在 Mac web UI 上发起的 turn。单一 backend 实例的全局订阅者直播外部 turn：
  kind `opencode-web`，`LiveEventBroadcast`，`requiresPollingForExternalTurns=false`，
  StaticCapabilities `external_turn_streaming`。
- **能力全部接口推导（负先于正）**：todos（`TodoProvider`）、
  `structured_user_input_v1`（`UserInputResponder` + `StructuredUserInputProvider`）、
  session mutation（`SessionRenamer`+`SessionArchiver`）、session delete
  （`SessionDeleter`）、`permission_resolve`（`ToolAuthorizer`，审批经 SSE
  `permission.asked` 浮出、按 §3.4 folding 应答）。legacy `question_reply` **故意
  不宣告**——结构化问答只走 `resolve_user_input`。
- **附件**：`AttachmentSupporter` 声明 image + file，都走官方 prompt file part
  （`{type:"file", mime, filename?, url:"data:<mime>;base64,…"}`）。
- **冷投影**：官方 `GET /session/:id/message` 重建（pathless）。
- 设计与证据：`docs/2026-08-18-opencode-web-backend-design.md` 及其
  source-first convergence / gate B 系列。

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
| `permission_resolve` | `ToolAuthorizer`；dsh-web（`/api/respond`）、opencode-web（SSE `permission.asked` + folding）宣告；OpenCode/Codex 不宣告 |
| `todos` | `TodoProvider` |
| `compression` | Codex `app_server` |
| `question_reply` | Codex `app_server` 在 derive 里加；Claude / dsh-web 走 `StaticCapabilities`；opencode-web **故意不宣告**（结构化问答只走 `resolve_user_input`） |
| `structured_user_input_v1` | Codex `app_server`，或 `StructuredUserInputProvider`（codex-web / opencode-web）；dsh-web 也在 StaticCapabilities |
| `external_turn_streaming` | 各 driver `StaticCapabilities`（dsh-web mux / codex-web 共享 daemon 订阅 / opencode-web SSE 广播） |
| `turn_detail_lazy_v1` / `turn_detail_chunks_v1` | 仅 codex-remote `StaticCapabilities`；门控 `session_turn_items` / `turn_output_chunk`（unified-bridge-protocol §11.7/§11.8），见 SSV2 一节 |
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
- **Codex Web / OpenCode Web / Codex Desktop**：同属官方服务端广播 backend
  （`LiveEventBroadcast` + `requiresPollingForExternalTurns=false`），外部 turn
  直播，不需要 external-turn polling；冷基线见 SSV2 家族表。

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

冷加载家族（`backendSupportsProjectionHydrate`，当前覆盖全部注册 backend）：

| 家族 | backend | 基线 |
| --- | --- | --- |
| transcript JSONL | claude / claudecode / codex | `TranscriptLocator` + transcriptindex |
| pathless 本地日志 | grokbuild / deepseek | 本地 grok/dsh 会话日志 |
| pathless 官方 API | opencode（HTTP rich-history）/ **dsh-web** / **opencode-web** / **codex-web** / **codex-remote** | OpenCode HTTP；官方 `session.history`；官方 `GET /session/:id/message`；官方 `thread/read(includeTurns)`；官方分页远程历史 |

规则：live/kernel 已有状态的会话以 kernel 为基线，file/HTTP 重建只服务冷开或脱活
会话。forceCold 集合与 `backendSupportsProjectionHydrate` 必须同时列入新 backend，
漏一处对应机制静默失效。dsh-web **不进** deepseek 的 store-file / live-only
admission 回退（会话在官方服务端常驻，mux 即时到达）。

**turn detail 懒加载（2026-08-30 终案）**：仅 codex-remote 的分页远程会话声明
`turn_detail_lazy_v1`（§11.7，整回合原子 `replace_parts` 进 Kernel，已 deprecated）
与 `turn_detail_chunks_v1`（§11.8 终案，增量分层加载：`session_turn_items` 首层 +
`turn_output_chunk` blob 二级懒加载）。声明挂在 codex-remote descriptor 上而不是
全局 echo——iOS「加载详细过程」入口以 descriptor capability 为门。两个 production
gate（`core/turn_detail_lazy_gate.go` / `core/turn_detail_chunks_gate.go`，当前均
开）与 hello echo 共用同一 const，翻转即同时撤回两个面。

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
go test ./agent/... ./core/... ./transcriptindex/... ./pinstore/... -count=1
(cd relay-server && go test ./... -count=1)
```

事件、rebind、broadcast、shutdown、relay mailbox 或协议变更应优先跑对应定向测试，再做
Release 覆盖安装。需要 iOS 真机交互验证时，按相邻仓库规则取得 owner 授权。

## 调试顺序

端到端同步异常时按边界取证：

1. MacBridge runtime 日志中是否收到 backend 原始事件（dsh-web 还要看 mux/host 是否连上 3080；
   codex-web 看官方 daemon 订阅是否建立；codex-remote 看 controller enrollment/链路是否在线）；
2. `events.go` / codec 是否映射出正确 wire event（dsh-web 审批看 `permission_request`，问答看 `user_input_requested`）；
3. 投影 kernel 是否 ingest、SSV2 客户端是否吃 patch 而不是只等 raw live；
4. broadcaster 是否有目标订阅，`relayEvents` 是否因空闲/turn 边界提前退出；
5. iOS 是被 live event、投影、session state 还是 history polling 驱动。

只有确认事件在 MacBridge 前半段消失时才修改 driver；只有确认 wire 已到 iOS 后才修改
`ChatViewModel`。不要通过同时改两端制造无法归因的“看起来好了”。
