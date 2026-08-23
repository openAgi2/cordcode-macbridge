# Codex Web 连续性架构：同一 daemon 上的观察、执行态与 Desktop 附着

- 日期：2026-08-22（2026-08-23 按一轮、二轮评审修订；2026-08-23 晚按 §7.1 矩阵与代码状态收尾；daemon restart 回归现场记录见 §12）
- 状态：**合同已按层施工并通过 owner 真机矩阵（§7.1）。本文保留合同全文；§4/§6 内已落地项均已标注，未标注者仍为待办。**
- 适用仓库：MacBridge 本文件为权威；iOS 仓 `docs/` 下同名文件是给 iOS workspace agent 的工作镜像，冲突以本文件为准
- 前置合同：[2026-08-21-codex-web-backend-design.md](2026-08-21-codex-web-backend-design.md)（v2.0 topology-first）
- 问题来源：[2026-08-22-codex-web-desktop-bidirectional-handoff-gap.md](2026-08-22-codex-web-desktop-bidirectional-handoff-gap.md)
- 评审：一轮 [2026-08-23-codex-web-continuity-architecture-review.md](2026-08-23-codex-web-continuity-architecture-review.md)；二轮 [2026-08-23-codex-web-continuity-architecture-review-r2.md](2026-08-23-codex-web-continuity-architecture-review-r2.md)。修订处置见本文 §10 / §11
- 活体复盘：MacBridge `think.md` 2026-08-22 条目
- 不变约束：CordCode 初衷 + SSV2 十二条 + 官方 Desktop 不可改 asar

> [!IMPORTANT]
> v2.0 证明了「Desktop 与 CordCode 必须连接同一个官方 daemon」。本文不推翻那条拓扑硬门。
> owner 真机已经证明：**拓扑 PASS ≠ 连续性 PASS**。缺的是 Link 进程生命周期、Desktop
> 不可逆 stdio 锁、iOS 观察面、投影执行相位之间的整体合同。后续 agent 必须按本文四层
> 一起设计、一起验收，不得把「列表闪一下」「卡执行中」「切 session 才同步」「CPU 高」
> 拆成互不相关的 hotfix。

> [!WARNING]
> 本文授权的是整体设计与按层施工。已落地：L0/L1 座位与附着、L2 scope RPC 生效信号
> （attach 结果回传，见 §6.2.1）、L3 主合同（`turn_completed` 进入 LAN 回放白名单，
> 见 §4.4）。一轮与二轮评审纠偏后不再存在「把候选写成已证实」的缺口；§7 矩阵
> 2026-08-23 已按 #1–#5/#7 逐行确认真机归因，#6 为未测对照项（非阻断）。

## 0. 一句话

CordCode 是用户官方 Mac Codex 工作流的 iOS 延伸。官方 `codex app-server` daemon 是唯一
运行时；MacBridge / go-bridge 是第二连接和观察者，不是 runtime 主人。产品要过的不是
「曾经连上过同一 socket」，而是：

1. Link 退出或重启时，daemon 和 Desktop 的 websocket 附着都还在；
2. Link 回来后，iPhone **立刻**继续旁观当前打开的 session，不必切走再切回；
3. Desktop 上的长回合在 iPhone 上有唯一权威终态，不会正文到了还停在「执行中」；
4. 正在跑的长任务不会因为 Link 进出而被切到 Desktop 私有 stdio，更不能指望 stdio 热接那一轮。

这四条必须同时成立。只修其中一条，其余会在下一次真机里换一张皮再出现。

## 1. 产品不变量

下列条件缺一即停止产品扩面。它们是同一合同，不是可选菜单。

| ID | 不变量 | 反例（禁止当解决方案） |
|---|---|---|
| C1 | 唯一 runtime 是官方 local daemon（control socket + `daemon start` alreadyRunning） | managed-loopback、每 session stdio、抢 Desktop 子进程 |
| C2 | MacBridge 不拥有 daemon。退出 / 停后台 / 杀 runtime **不得** `daemon stop`、不得杀掉 Desktop | 把 daemon 当 go-bridge 子进程；退出 Link 当日常「重启 Desktop」 |
| C3 | Desktop 一旦 `daemon version` 失败会锁死 `kind=stdio`，进程内不可远程翻回 | 用 kill/steal stdio 子进程、SQLite 假同步、第二 app-server 继续工作 |
| C4 | 活跃 thread 的 writer 在 daemon 进程内。第二连接只能观察或在无 writer 时 resume | iOS 对 Desktop 正在写的 thread 抢锁；stdio 热接 daemon 上的 in-flight turn |
| C5 | iPhone `isGenerating` 只认投影 `execution.phase`。正文到齐不等于回合收口 | timer、输入框、Stop 本地态、字数/静默时间猜完成 |
| C6 | iPhone 正在看的 session，在 **新的 go-bridge 进程** 上必须从 t=0 就有**已生效**的观察范围和 observer attach | 只 spawn 声明不等待结果；hello 成功或 `ResultResponse{Ok:true}` 冒充已在听；靠切 session 当重连修复 |
| C7 | 长任务的执行点在 daemon，不在 Link。Link 进出不得中断 daemon 上的 turn | 把「列表闪一下」当成服务死了；用重启 Desktop 续跑 |

C3 是官方 Desktop 宿主事实，本产品改不了 asar。合同只能是：**永不制造 Desktop 的探测失败窗口**；已经锁死的 Desktop 只能完整退出一次，且这不是退出 Link 的正常步骤。

C6 说的是**生效的旁观**，不是「曾经发出过 `set_observation_scope`」、也不是 wire 上的 `Ok:true`。
已满足：scope RPC 现逐 session 同步 Subscribe + attach，并把每个 session 的
`Subscribed/Attached/Error` 放进响应（失败走 `observation_attach_failed`，见 §6.2.1）。

## 2. 官方 Desktop 不可改的宿主事实

当前 ChatGPT Desktop（`app.asar` 只读核验，bundle `26.818.31338` 一代）在本地、
`CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`、无 `FORCE_CLI` / `CODEX_CLI_PATH` 覆盖时：

1. 每次 `transport.connect()`（含 reconnect）都再跑 `codex app-server daemon version`，
   spawn timeout **2500ms**。
2. control socket 不在时该命令通常 **立刻失败**，不会等满 2.5s。
3. 失败就把 `kind` 写成 `stdio`，`supportsReconnect()` 变为 false。这个 Desktop 进程
   没有远程翻回 websocket 的 API。
4. 首次 reconnect 大约在 websocket 断开后 **1s**（`reconnectDelayMs` 初值 1000）。
5. Desktop 自己的版本门是 `appServerVersion >= 0.141.0`，**不是** `codex --version`
   字符串全等。本机曾出现 Desktop `0.149.0-alpha.4.1` vs standalone `0.149.0-alpha.4`，
   官方探测过得去，exact CLI 门却把座位安装整段 fail-closed 掉。
6. 私有 stdio 是另一个 app-server 进程。它不能接管 daemon 上已持有 writer 的 in-flight
   turn（`-32600 already has an active writer`）。官方也没有 stdio ↔ daemon 热迁移。
   注意：**同 daemon 的另一个客户端（如 Desktop 打开着该会话）同样独占 writer**——
   observer 的 resume 也会得到同一 -32600，这不是「Desktop 离开共享 daemon」的证据
   （2026-08-23 实测，见 §12.2/12.3）。
7. 空闲 thread 内存卸载延迟 30 分钟，那是卸载计时，不是崩溃恢复，也不是跨进程续跑。

以上数值来自 2026-08-22 对本机 asar 的只读提取，并与 `think.md`、`RuntimeManager`
注释、Desktop attach Gate README 交叉一致。2026-08-23 评审**未再反编译** asar；
Desktop 升级后必须按新 bundle 重核，不得把旧数字当跨版本常量。

推论：对 Desktop 来说，**探测失败 = 永久私有 runtime**，直到用户完整退出 App。
CordCode 能做的只有让探测不要失败，以及在已经失败时诚实告诉用户「请退出 Desktop」，
而不是再造第二服务。

## 3. 失败地图：表面现象是一层，根是合同缺口

owner 真机看到的不是六个独立 bug。它们是 C1–C7 某一层缺失时的不同皮肤。
下表「实际缺失层」写的是合同层；机制细节以 §4 为准。标「归因候选」的须用 §7 复测，
不得当成已证实根因去改错面。

| 表面现象 | 当时容易误判 | 实际缺失层 | 活体 / 代码要点 |
|---|---|---|---|
| 先重启 Link 再重启 Desktop，Mac→iOS 能跟 | 拓扑已完成 | 只证明冷启动附着，不证明 Link 进出连续性 | 两边都在 daemon 上，观察面也挂上了 |
| 退出 Link（含后台），Desktop 列表闪一下 | 共享服务被带走 | **L1 误报**。列表刷新 ≠ daemon 死 | daemon `setsid`、ppid=1；Link 退出路径不停 daemon；Desktop 仍有 UDS 打在同一 control socket |
| 再打开 Link，Mac 发、iOS 不同步；切 session 再切回才好 | Desktop 掉到 stdio；或「完全没有重连声明」 | **L2** 观察未**生效** | 新进程 scope 为空；`set_observation_scope` 已是单一充分入口但声明 fire-and-forget；无 scope 时 milestone/control 仍可能泄漏，造成「假在听」。切 session 是用户绕过，不是设计 |
| 长故事正文到了仍「执行中」；停止无效；再发短的才收口 | iOS 渲染/停止坏了；旁观 256 chan 挤掉 completed | **L3** 完成帧未进投影相位 | **归因候选（机制代码直证）**：durable `turn_completed` 走 relay mailbox，对 LAN 重连不回放；LiveFrameBuffer 回放含正文/`turn_started`（会强制拉起执行中）不含 completed。Stop 本地 aborted 会被 running 投影盖回去。iOS 有 10s stall watchdog，但 V2 下投影 executing 不收口（与 C5 一致，禁止当收口手段） |
| 长任务中途退 Link，担心任务中断、切私有后续不上 | daemon 是 Link 的孩子 | **C2+C4** | 执行点在 daemon。Link 走了只少一个观察者。stdio **不能**热接 in-flight turn |
| CordCode Link 常驻约 40% CPU | runtime 吃流 / 五个 backend 空转监视 | **观测面（已采样）** | 热点在 Swift GUI 主线程动画，不在 runtime（0.2–0.3%）。禁止为此改座位/daemon |

禁止把上表每一行开成一个互不相关的 PR。修 L2 却不处理 L3，长回合仍会卡执行中；修 L3 写路径队列却不处理 L2 和 durable 路由，重启 Link 后完成帧仍然到不了 iPhone。

## 4. 目标架构：四层连续性

四层串行。下层失败时上层必须诚实暴露，不得用更上层的 UI 技巧补。

```text
L0  登录级 daemon 座位
    LaunchAgent KeepAlive 只跑幂等 `daemon start` + attach env
    补位必须短于 Desktop 首次 reconnect（约 1s）
            │
L1  Desktop 附着
    登录域 `CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`
    永不制造 ≥1s 的 socket 空窗；CLI patch 偏差不得挡住座位
    已锁 stdio 的 Desktop = 异常恢复（完整退出），不是退出 Link 的步骤
            │
L2  观察连续性（iOS 正在看的 session）
    观察范围的权威在「当前打开的 session」
    go-bridge 侧 `set_observation_scope` 是单一充分**入口**（会触发 Subscribe + AttachLiveThread + flush）
    现状 RPC `Ok:true` 不证明 attach 生效。缺的是：声明必须可验证地生效。hello/`Ok:true` ≠ 已在听
            │
L3  执行连续性（投影相位）
    旁观路径上 `turn/completed` 跨断连窗口必须仍能进入 Kernel 相位
    iPhone 执行态 = Kernel `execution.phase`
    外部回合的 Stop 要么官方中止成功并投影 idle，要么失败可见，不得假收口
```

MacBridge 在这张图里只出现在 L0 的「探测/补位」和 L2/L3 的「第二连接」。它不是 L0 的进程主人，也不是 L3 的真相源。

### 4.1 L0 座位

已部分落地，仍属本层合同而不是「已完成可遗忘」：

- 用户 LaunchAgent `org.openagi.cordcode.codex-app-server-daemon`
- 脚本只循环官方 `daemon start`（alreadyRunning 不换 PID）+ `launchctl setenv`
- **不得** `daemon stop` / `daemon restart`（restart 是 ccswitch 的显式杠杆，见 §5）
- KeepAlive + 亚秒级补位，因为 Desktop reconnect 约 1s，60s 周期盖不住
- 只对真实用户 home 写 plist，测试假 home 不得安装
- Link `shutdownForExit` / `stop` 只停 runtime 与托管 OpenCode，不动座位、不动 daemon、不动 Desktop

官方 daemon 用 `setsid` 脱离父进程。bootout 座位 bash **不应**带走 daemon；若未来发现 process group 仍能杀掉 daemon，那是 L0 缺陷，修座位隔离，而不是让 Link 拥有 daemon。

2026-08-23 CPU 采样：座位 0.25s 循环实测 bash 0.3–0.7%，不构成 Link 40% 热点。**不为 CPU 调整补位间隔。**

### 4.2 L1 Desktop 附着

- 附着入口只有官方 local daemon UDS，禁止再引入 loopback URL 产品路径
- Desktop 是否附着由 **它自己的** `daemon version` + `appServerVersion >= 0.141.0` 决定
- CordCode 的 exact `codex --version` 字符串门已经证明有害：挡住座位，又挡不住 Desktop 自己去附着或锁 stdio
- 退出 Link 时 Desktop 列表闪一下，应视为「少了一个 client 的队列通知」，直到活体证明 daemon PID 变了或 Desktop 出现私有 `codex app-server` 子进程（且父进程是 ChatGPT，不是 `cordcode-bridge-runtime`）
- 旧 `codex` driver 在 runtime 下拉起的 stdio `codex app-server` **不是** Desktop 私有服务，取证时必须看 PPID

### 4.3 L2 观察连续性

背景：2026-08-22–23 真机「重启 Link 后不同步、切 session 才好」的合同缺口。2026-08-23
已按 §6.2 收口并归因（§7.1 #2/#5）；本节保留机制描述（取证与回归对照仍有用），
已落地项分别标注。

**go-bridge 侧入口与生效信号均已建（单一充分入口，单一来源验证）：**
`handleSetObservationScope` 一个 RPC **会触发**三件事：
(1) 同步 `broadcaster.Subscribe` 当前连接（单独即可过 Kernel-ingest 订阅者门，不必先 `get_session`）、
(2) 逐 session 同步 `AttachLiveThread`（observer `thread/resume`），
(3) `FlushLiveFrameBufferForDevice`。然后构造逐 session 结果：`ObservationScopeRPCResult{Ok, Sessions}`，
任一 session 未 Subscribe 或 attach 失败时返回 wire error `observation_attach_failed`（§6.2.1）。
hello 不触发 attach。订阅按 conn 键，换连接必须重发；经 Relay 送达的 scope 走同一 `HandleRPC`。

因此：入口不必从零再造；**不得**再把 `Ok:true` 当 C6「已生效」——Subscribe 同步成功只证明
ingest 门打开，observer resume 结果现在逐 session 出现在响应里。

**2026-08-23 前成立的断裂点（现均已收口；保留为取证/回归对照）：**

1. 观察范围活在 **go-bridge 进程内存**（`relay_observation.go` 纯 map）。进程一死，范围清空。
   → 收口：iOS 恢复事务在 `bridgeTransportReconcileReady` 后重发 scope（§6.2.2），
   新进程上 scope 非空时 Subscribe+attach+flush 全程可见。
2. `resubscribeObservationSessions` 在 `scope == nil` 时是无声空操作：无错误、无成功信号。
   → 收口：语义改为「诚实沉默」——无 scope 则不参战，不再有 `Ok:true` 假绿；首次生效
   scope 的 RPC 结果可见（§6.2.3）。
3. `AttachLiveThread` 的唯一非测试调用点是 `set_observation_scope`；hello 只做能力协商。
   → 仍为事实（不让 watcher 变成第二观察源，见 §5.10）。
4. **iOS 重连声明链**（transport 重连 → 重建 client → 前台恢复事务
   `setObservationForeground(true)` 按当前 `currentSessionId` 发 scope）已修：恢复事务等待
   可验证的声明（attach 失败不再当成功），租约循环每次迭代解析**当前** backendClient
   （iOS 8f3ad3e）。「切 session 才好」应视为用户绕过，不是重连步骤。
5. **go-bridge 侧假验证**：`AttachLiveThread` 以 `go` 发射、RPC 不等待；`observeThread`
   对 transport 错误、`-32600` ownership、「官方 rpcErr」全部 `slog.Warn` 后返回；
   `obsClient == nil`（观察连接还在 backoff）时 attach **静默 no-op**。
   → 收口：attach 现在同步等待并逐 session 回传结果；失败让该 RPC 失败（§6.2.1 F1）。

必须写进合同的结构性事实（代码直证，不是立刻要建的第二套观察）：

- **无** `codex-web` 的 `StartCodexRelayWatcher`（该 watcher 只覆盖旧 backend `"codex"`）。
  不为了「有个 watcher」去复制一套观察源；L2 完成标准仍是 scope RPC 生效。若将来加安全网，
  不得变成第二真相。
- live target rebind 是惰性事件驱动，并以 observation scope 为准：新进程 scope 空 → 永远 rebind 0。
- 重连窗口会把 full_stream **软降级**为 milestones_only。这是**反症状**（可能无正文但会收口），
  取证时用来排除，**不**作为「先拆租约降级」的施工许可。
- 新进程启动时 `startPassiveSubscription` 会 resume 全部 loaded threads；无 scope 时
  durable milestone + control-plane 仍可经 `ShouldSendEvent` 白名单放行。**「部分事件在流」
  不等于「正在旁观」。** 回归对照若只看到里程碑，会假通过；验收必须断言 delta 流式。

目标形态（2026-08-23 已达成，依据见 §7.1 #2/#5 与 §6.2）：

- **权威**：iPhone 当前打开的 `(backendId, sessionId)` 是观察意图的唯一来源。
- **t=0**：新 go-bridge 进程上，hello 成功后恢复事务重发当前 scope；生效 = scope 非空 +
  当前 conn 已 Subscribe + observer 已 resume 该 thread——**逐 session 结果在 RPC 响应里**，
  任何一个失败即 wire error `observation_attach_failed`（不再靠 `Ok:true`）。
- 租约循环每次迭代解析**当前** `backendClient`，不捕获旧 client（iOS 8f3ad3e）。
- **hello 成功、Relay 已连、列表能拉、甚至收到 milestone，一律不算「正在旁观」。**
- 切走再切回只作回归对照，**不得**当作正常重连步骤写进验收。

冷校准（thread/read、projection hydrate）补的是缺口历史，不能替代 live 观察。没有 L2，L3 的完成帧根本不会到。

### 4.4 L3 执行连续性

iPhone 输入框「执行中」= `projection.execution.phase.isExecuting`。SSV2 不允许用本地 Stop、
静默时间或「正文看起来写完了」收口。上升沿除投影外还有本地 send 占位（`waitingForAssistant`）
与 `turn_started` **强制**拉起 `isGenerating`——后者是跨断连回放时「正文到了仍执行中」的
iOS 侧配合条件。

iOS 另有 10s stall watchdog：只有显式 idle 才收口，V2 下投影 executing 则不收口。与 C5
一致。禁止删掉当「卡执行中」的快捷修复，也禁止把它升级成完成判定。

**旁观链路与写路径是两条通道，溢出行为相反。禁止再把它们写成一条。**

纯旁观（Desktop 写、iPhone 看）：

```text
官方通知 → 观察连接 readLoop（chan 256，阻塞投递）
        → Subscribe 泵（chan 256，阻塞 send 背压，不丢）
        → startPassiveSubscription（含 HasSessionSubscriber 门）
        → DeltaBatcher（控制帧先 flush 同 key）
        → PublishLogical → kernel.IngestLive
                          → per-conn ShouldSendEvent → sink tryEnqueue
```

`dispatchEvent` 的 256 满即丢（`agent/codex-web/events.go` 中央泵 → session listener）
**不在纯旁观链上**。它服务 iPhone 自己发 turn 的写路径 → `relayEvents`。把它当 L3
主断点去加「终态优先 / 独立完成通道」，会修错面；写路径隐患仍要记，但不得冒充旁观根因。

注脚：若 bridge 在同一 thread 上**已经持有写 session**（iPhone 早前对它发过消息），
同一外部回合也会经写连接进 `dispatchEvent`，与旁观泵两路并行、投影层去重。取证时在
日志里看到 `dispatchEvent` **不等于**「这次是写路径根因、旁观合同修错了」。纯旁观
（iPhone 从未对该 thread Send）仍只走观察泵。

2026-08-23 已确诊并收口的旁观根因（§7.1 #7 强变体确认；原为归因候选）：

**durable/live 路由不对称（已修）**

- `turn_completed` 曾分类为 durable：零目标窗口走 offlineQueue →
  `routeRelayOfflineStampedEvent`，**只投 RelayEnabled 设备的 relay mailbox，对 LAN 重连永不回放**。
- `turn_started` / `text_delta` 是 live 可缓冲帧，进 LiveFrameBuffer（60s / 200 帧 / 1MB），
  重连或 `set_observation_scope` 后 `FlushLiveFrameBufferForDevice` 回放——白名单**曾不含**
  `turn_completed`。
- iOS：`turn_started` 会 `activateGenerationIfNeeded` 强制拉起执行中。

合成（修复前）：长回合收尾时 iPhone 零目标（Link 重启、锁屏/退后台）→ 正文 + `turn_started` 进
LiveFrameBuffer，completed 进 mailbox 或被丢 → 重连 flush 回放全文并拉起执行态，
**completed 永不到** → 卡执行中；切 session / 再发短消息引入新事件才收口。

收口（2026-08-23，§6.3.1 选型 a）：`turn_completed` / `turn_error` / `turn_aborted`
已进入 LiveFrameBuffer 回放白名单（`go-bridge/live_frame_buffer.go`），LAN 重连后可对
Kernel 相位回放终态；结合 epoch-change 全量快照强制采纳（iOS 3ba4a35，快照数据源溯官方
`thread/read`，见 §6.3.1 注），§7.1 #7 强变体（12:29:52 长任务运行中重启 Link）实测收口。

其余真实丢失点（代码直证，与本次收口无关的独立面；2026-08-23 底仍未处理，定向测试不得假装没看见）：

1. offlineQueue（2048）溢出时 `default` 只 append nil，后续 `if conn != nil` 跳过——durable 帧无声消失。
2. v2 连接 raw `turn_completed`/`text_delta` 被抑制，全靠 `projection_patch`；patch 溢出只标
   `projectionInvalidated` 不重发（`K4Patch drop reason=sink_overflow`）。
3. per-conn sink `tryEnqueue` 溢出 → `conn.Close()`：非静默丢，表现为断连。
4. iOS：旧 client `handler=nil` 时 patch/snapshot **静默丢**（2026-08-23 未在 iOS 源码复现到旧实现，
   待专门核查）；入站缓冲 2048 满即断连并清空已缓冲帧。

目标形态（2026-08-23 主合同已达成，选型 a）：

- 旁观路径上跨断连窗口的 **durable 终态已进入 Kernel 相位**：`turn_completed/turn_error/
  turn_aborted` 已可对 LAN 回放（§4.4 上）。(b) 权威 pull 收口相位仍保留为兜底选项，
  其前提不变：零目标窗口内事件在 `HasSessionSubscriber` 门（`main.go` 被动泵）就被丢掉，
  **进不了 go-bridge 内存 Kernel**，因此 pull 的终态数据源必须能溯到官方 `thread/read`
  （codex-web `history.go` 一期基线，`thread/read{includeTurns:true}`）——epoch-change
  全量快照目前即按此基线构建；选 (b) 前同样只准读官方数据源，不得读未被 ingest 的内存 Kernel。
- 写路径 256 满丢是另一面，允许单独测试，不得当作旁观验收。
- Kernel 必须能从官方 `turn/completed`（以及失败/中断语义）落到 `execution.phase=idle|failed`，
  iPhone 只跟这一相位。
- 外部回合点 Stop：要么官方中止成功且投影 idle，要么明确「无法中止另一端的回合」。禁止假完成。
  现有代码对外部回合仍发真实 abort RPC、失败提示；禁止改成只改本地态。
- 禁止用「再发一条短消息」当收口手段。

`codex-web` 已禁用 relay idle timeout 合成假 `turn_completed`。§5「禁止假完成」在这一点已有代码；
不得再引入 timer 收口。

## 5. 明确不做什么

1. 不改官方 Desktop asar，不注入、不 hook、不杀它的 stdio 子进程来「翻回」websocket。
2. 不把「退出 Link 后重启 Desktop」写成正常工作流。
3. 不为了 ccswitch 而停掉登录 daemon。ccswitch 写的是 `~/.codex/config.toml`；跑着的 daemon 持有内存配置。正确杠杆是用户显式 `codex app-server daemon restart`，不得写进座位脚本。
   **2026-08-23 实测补充（决定产品入口文案）：** 官方 restart 执行后控制 socket 约 1s 内重建，但
   30s 内 **Desktop 没有自动重连**（Desktop bundle 151.0.7922.170 / standalone 0.149.0-alpha.4；
   实测时 Desktop 未附着 control socket）。"必须快于 Desktop 的 1s reconnect 探测"的旧前提在本代
   Desktop 上不成立——入口已按「重启成功 + 提示完全退出并重开 Desktop 兜底」落地（Homepage
   codex-web 行按钮 + config.toml 变更提示，5fea386）；seat 脚本仍只跑幂等 `daemon start`。
4. 不恢复 `managed-loopback-ws` 或第二 app-server 来回避 stdio 锁。
5. 不用 history / raw event / 字数比较补洞（SSV2 第 4 条）。
6. 不把 CordCode Link 的 CPU 爆发当成改 daemon 生命周期或座位间隔的理由。采样已指向 Swift GUI 动画；未做「减弱动态效果」对照前也不要把动画修复并进 L0–L3。
7. 不把旧 `codex` driver 的 stdio 子进程当成 Desktop 私有服务。
8. 不在 L2 未生效时用冷投影或 milestone 泄漏冒充「已经在 live 旁观」。
9. 不把旁观路径的完成帧问题修到写路径 `dispatchEvent` 256 chan 上。
10. 不为「有个 watcher」给 `codex-web` 复制 `StartCodexRelayWatcher` 当 L2 完成证明。
11. 不把 `set_observation_scope` 的 `ResultResponse{Ok:true}` 当 attach 生效；不把「等到 Ok」写成 L2 完成。
12. 不把零目标窗口之后的内存 Kernel pull 当权威收口，除非已证明该 pull 溯源官方 `thread/read`。

## 6. 施工切面（按层，禁止按 bug 开 PR）

后续 agent 只允许按层提交。每一层必须带：不变量、唯一 writer/观察者、失败怎么暴露、定向测试、owner 矩阵行。层与层可以并行 **设计**，但 L2 的产品验收依赖 L0/L1 仍成立。

### 6.1 L0/L1 收口（座位 + 附着）

目标：Link 退出/重启不是 Desktop 锁 stdio 的原因；座位不被 CLI patch 偏差挡住。

必须留下的证据：

- Link `stop` / `shutdownForExit` 源码路径无 `daemon stop`、无座位 bootout（安装时的 bootout 只替换 LaunchAgent 作业，不得杀死 daemon）
- 退出 Link 后 daemon PID 不变、`daemon version` 仍 running、Desktop 若原先 websocket 则仍无 ChatGPT 子进程 `codex app-server`
- Desktop 列表闪一下可以发生，但 **不得**伴随 daemon PID 更换或 Desktop 私有 stdio 子进程

未完成项（2026-08-23 状态）：

- ccswitch → 官方 `daemon restart` 的产品入口与空窗预算 —— **已做**（5fea386：Homepage codex-web
  行「重启共享 Codex 服务」按钮 + config.toml mtime 变更提示；30s socket 等待；空窗实测见 §5.3）。
- 座位 process group 与 daemon `setsid` 的隔离测试 —— **未做**（排期）。
- 已锁 stdio 的诚实文案（现有 ownership 错误已部分覆盖）—— 部分覆盖，未再加新形态。

### 6.2 L2 收口（观察在新进程上必须可验证地生效）

目标：iPhone 开着 session 时重启 Link，重连成功后 **不必切 session**，Mac 再发就能进执行态并 **delta 流式**。
**2026-08-23 已达成**（§7.1 #2/#5 真机确认）。

go-bridge 侧 `set_observation_scope` 会触发三件事，**不要从零再造入口**。按整体处理、缺一不算 L2 完成：

1. **声明可验证（已落地）：** 现有重连声明链保留，恢复事务等到 scope RPC 按下面成功定义成功，
   失败即失败可见（wire error），不再 fire-and-forget。RPC 成功的定义**包含逐 session 的
   Subscribe + attach 结果**：`handleSetObservationScope` 逐 session 同步 Subscribe +
   `AttachLiveThread`，把 `ObservationSessionAttach{Subscribed, Attached, Error}` 放进
   `ObservationScopeRPCResult{Ok, Sessions}`；任一失败返回 `observation_attach_failed`。
   （`go-bridge/handlers.go`；禁止回退到无条件 `Ok:true`。）
2. **循环解析当前 client（已落地）：** 租约循环每次迭代取当前 `backendClient`，不捕获创建时的
   旧 client（iOS `8f3ad3e`）。
3. **空 scope 诚实（已落地）：** `resubscribeObservationSessions` 在 new scope == nil 时跳过
   订阅（诚实沉默，不再假装)；第一次生效的 scope 必须 Subscribe + `AttachLiveThread` + flush，
   且 **attach 结果可见**（本条随 1/4 验收）。
4. 验收断言 **delta 流式**（§7.1 #2/#5：逐段流式已确认），不能只看到 `turn_started` /
   `sessions_changed` 里程碑，也不能只看到 scope RPC `ok: true`。

定向测试覆盖（2026-08-23 已覆盖或随 #1 落地）：
- go-bridge 进程重启、observation 内存为空后发 scope → attach 结果可见（#5 真机链路）；
- observer 尚未就绪（`obsClient == nil`）时发 scope 不再报成功（attach 同步调用，失败即 RPC 失败）；
- iOS 重连、当前 session 未 switch、随后外部 `turn/started` 与 `text_delta` 进 Kernel（§7.1 #2）；
- Desktop 持有私有 stdio writer 时 attach 失败出现在 RPC 结果里（`-32600` → 诚实失败，不是绿旁观）。

### 6.3 L3 收口（旁观终态跨断连仍达相位）

目标：Desktop 长回合结束，iPhone 相位变为 idle；Stop 对外部回合诚实。
**2026-08-23 主合同已达成（选型 a）**，§7.1 #3/#4/#7 真机确认。

已整体处理的项：

1. **主合同（已落地）**：durable `turn_completed` 在 LAN 重连后进入 Kernel 相位——
   `turn_completed/turn_error/turn_aborted` 已进 `live_frame_buffer.go` 回放白名单（选型 a）；
   不再只靠 relay mailbox。(b) 权威 pull 收口相位保留为兜底，前提不变：pull 数据源必须溯官方
   `thread/read`（codex-web `history.go` 一期基线）；epoch-change 全量快照目前即按此基线构建。
   禁止只读内存 Kernel 当权威。
2. 其余丢失点（与本层验收直接相关但**未处理**，排期）：offlineQueue nil 静默丢、K4Patch 溢出不重发、
   iOS `handler=nil` 丢帧（旧实现未复现，待专门核查）。sink 溢出断连按 L2 重声明闭环处理，
   不单独发明假完成。
3. iPhone Stop：writer 是 Desktop 时失败可见；投影 running 时不得被本地 aborted 骗成完成
   （现状保留：真实 abort RPC + 失败提示）。
4. **写路径** 256 满丢：允许保留/补测试，但必须标注「只覆盖 iPhone 自己发 turn 的 listener」，
   **不得**当作旁观 L3 通过条件。

定向测试（2026-08-23 状态）：
- （旁观，主）回合中段断连或重启 Link，重连后正文经 live 回放补齐，**且** `execution.phase`
  在 Desktop 已完成后变为 idle——§7.1 #3（12:05:55 completed rev 483 收口）/ #7（12:29:52
  重启 Link 强变体收口）确认。
- （写路径，辅）合成超过 listener 容量的 delta + 最后一帧 completed，**写路径**投影必须 idle。
  此测试通过 ≠ 旁观 L3 完成（未新增专属测试，保留为后续项）。

禁止用「再发短消息」当测试通过条件。禁止用修 `dispatchEvent` 256 来关闭旁观 L3。

### 6.4 CPU（独立观测，不并入 L0–L3）

2026-08-23 只读采样（评审附录 A）已完成合同原先的采样义务：

- CordCode Link 主线程约 40%，栈为 SwiftUI 常驻动画（`UpdateCycle` → layout → 动态 shadow）
- 代码对应：主窗口 `PairDeviceButton` 的 `repeatForever` 呼吸 + 扫光；`ApproveDeviceButton` 同款
- runtime 0.2–0.3%；座位循环不是热点；六个 driver 在零 iOS 连接时的被动订阅在订阅者门前丢弃，不是 40% 来源
- iOS 离线或看其他 backend 时，codex 被动事件默认不进 Kernel；例外是仍存活的 per-session relay mailbox（durable 里程碑）以及无 scope 时的 milestone/control 泄漏

2026-08-23 处理与剩余：
- `PairDeviceButton`（WorkspaceView）与 `ApproveDeviceButton`（PairingView）的呼吸+扫光
  均已移除（静态蓝底白字，保留悬停反馈）——修复面正是「仅首次引导播放」之外的持续动画；
  该改动作为独立 UI 提交，不并入 L0–L3。
- **未完成（owner 对照）**：用活动监视器确认减动画后 CordCode Link 主线程 CPU 是否骤降；
  坐实后（如有残留）再查主线程栈。此条目独立于连续性施工。

## 7. owner 验收矩阵

后续任何「连续性完成」报告必须一次性给出下表，不得跑完一行再要下一行。owner 只回报步骤号 + ✅/❌ + 现象。

| # | 前提 | 动作 | 应看到 |
|---|---|---|---|
| 1 | Desktop 已附着共享 daemon（无 ChatGPT 子进程 `codex app-server`） | 只退出 Link（含后台），不要动 Desktop | 列表最多闪一下；daemon PID 不变；Desktop 长任务若在跑应继续出字 |
| 2 | 仍是 1 | 再打开 Link，iPhone **不要**切 session | 重连成功后，Mac 再发，iPhone 立刻执行中 + **逐字/逐段流式**（不能只看到回合开始/列表刷新），不必切走切回 |
| 3 | 两边都在 daemon | Mac 发「讲个故事 1000 字」，等 Desktop 完成 | iPhone 正文跟上后输入框变为完成态；点停止若回合已结束应保持完成，不得假执行中 |
| 4 | 3 刚结束 | 不要再靠「发一条短的」救状态；若已完成则直接停在完成态 | 后续 Mac 短消息正常开跑、正常收口 |
| 5 | iPhone 开着该 session | 重启 Link，Mac 在重连成功后发 | 与 #2 相同；切 session 只作为对照，不是必做步骤 |
| 6 | 对照：任务中途 **完整退出 Codex App** | 再打开 Desktop | 允许换 runtime；正在飞的那一轮接不上，只能看到已写下的历史。这不是退出 Link 的对照失败 |
| 7 | 两边都在 daemon，Mac 正在打长回合 | **必测强变体：** 回合收尾前后 **重启 Link**（真零目标，durable 路由不对称的强形式）。补充变体：iPhone 锁屏/退后台再回前台（连接可能未断，只走 per-conn 缓冲，durable 未修时也可能过） | 强变体：正文可补齐，**且 Desktop 完成后 iPhone 必须收口**；若只有正文回放、输入框一直执行中，即 durable 路由未收口（L3）。锁屏变体不得单独当作 L3 通过 |

#1 失败才去查 L0/L1。#2/#5 失败是 L2（须看到 delta，不能只看到里程碑或 `Ok:true`）。#3/#4/#7 失败是 L3；#7 以重启 Link 为准。#6 是官方宿主边界，用来防止把「退出 Desktop」误写成产品主路径。

### 7.1 owner 真机执行记录（2026-08-23，iPhone 16 Pro；MacBridge bd6dc29 runtime + iOS 8f3ad3e→3ba4a35）

| # | 结果 | 证据 |
|---|---|---|
| 1 | ✅（由 #5/#7 场景覆盖） | 两次重启 Link 均在 Desktop 附着的长任务运行中执行；官方 daemon 与任务全程存活，仅 MacBridge 重建被动订阅 |
| 2 | ✅ | 12:01:28 / 12:03 Mac 发「小猫笑话」「蛤蟆笑话」：iOS 实时渲染（用户气泡+逐段流式），未切 session |
| 3 | ✅ | 12:05:55 `turn_completed`（rev 483）交付；1000 字正文在 iOS 完整收口，输入框无假执行中 |
| 4 | ✅ | 12:31-12:33 Todo 5/6 步骤依次开跑并收口（各 20s+笑文本体），后续步骤无卡执行中 |
| 5 | ✅ | 12:05:00 重启 Link（纪元 8b66706f→cc0416ec）→ 12:05:23 Mac 首发（rev 127，base 126 接续快照）即时流式 |
| 6 | 未测（对照项，非阻断） | 完整退出 Codex Desktop 的宿主边界，本轮未纳入 |
| 7 | ✅ | 12:29:52 重启 Link（纪元→2946234b）发生在长任务运行中；iOS 3s 重连，`sinceRev=54`→`headRev=19` 按 `full(epoch_change)` 快照强制采纳；turn_completed rev 148 / 194 均交付（write_err 空）且 12:33 截图输入框收口 |

补充记录（2026-08-23 夜间，非矩阵行，属 §6.1/§6.4 未完成项收口）：
- §6.1 ccswitch 入口已交付（MacBridge 5fea386/1cc075d）：Homepage「AI 工具」Codex Web 行
  「重启共享 Codex 服务」按钮（官方 `app-server daemon restart` + 30s socket 等待 +
  活跃 turn/pending 计数禁用与点击前预检）+ `~/.codex/config.toml` mtime 变更提示。
  实测（§5.3）Desktop 未自动重连，按钮结果按 fallback 文案提示「完全退出并重新打开 Codex 桌面」。
- §6.4 PairDeviceButton / ApproveDeviceButton 呼吸+扫光动画已移除（静态样式 + 悬停反馈）；
  owner CPU 对照（活动监视器确认骤降）仍未做。

归因补充：本轮全部行均在 **iOS `3ba4a35`（epoch-change 全量恢复强制采纳）+ MacBridge `67f920a` / `bd6dc29`** 下通过。修复前的差异对照：2026-08-23 10:33 美国笑话轮在旧 iOS 二进制下整段丢失——桥重启后新纪元 rev 106-147（均 ≤ 旧纪元 appliedRev 147）被 `applyFull` 单调门全部 STALE-DROP，直到 rev 148 起恢复；修复后同一轮数据在新纪元全量快照下恢复可见。

## 8. 给后续 agent 的阅读顺序与禁令

阅读顺序：

1. 本文全文（合同）与 §10 / §11 评审处置
2. 一轮 [2026-08-23-codex-web-continuity-architecture-review.md](2026-08-23-codex-web-continuity-architecture-review.md)、二轮 [2026-08-23-codex-web-continuity-architecture-review-r2.md](2026-08-23-codex-web-continuity-architecture-review-r2.md)（源码行号以当时工作树为准，施工前应再核）
3. v2.0 设计 §0 与 §6.1（拓扑，不可退回 managed-loopback）
4. `think.md` 2026-08-22 Desktop stdio 锁与 CLI patch 偏差
5. 再读源码：`RuntimeManager.swift` 座位、`handlers.go` `set_observation_scope`（注意 `go AttachLiveThread` 与无条件 Ok）、`resubscribeObservationSessions`、`agent/codex-web/events.go` 观察泵 vs `dispatchEvent` 与 `obsClient==nil`、`main.go` `HasSessionSubscriber` 门、`handlers_relay.go` durable mailbox、`live_frame_buffer.go` 回放白名单、iOS `setObservationForeground` / `applyPresentationChangeSet` / `activateGenerationIfNeeded`

禁令：

- 不得只修 iOS 或只修 Mac 来掩盖另一侧缺口。L2 是双仓：iOS 可验证地声明当前 session，Mac 在那次 RPC 里 Subscribe+attach **并把 attach 结果放进响应**（入口已实现，生效信号未建）。
- 不得把「发过 set_observation_scope」写成 L2 测试通过；L2 验收以 `ObservationScopeRPCResult` 的逐 session `Subscribed/Attached` 为准（`Ok:true` 已废弃为信号，见 §6.2.1）。
- 不得把切 session、再发短消息、重启 Desktop 写进正常用户步骤。
- 不得在未跑 §7 矩阵时声称「双向 live 已完成」。自动拓扑脚本 PASS 仍然不够。
- 发现新现象时，先在 §3 表里归层；归不进四层，先改本文，再改代码。
- 取证长回合卡执行中时：先看 completed 是否在 `live_frame_buffer.go` 回放白名单、是否进了
  `routeRelayOfflineStampedEvent`（LAN）；不要先去扩写路径 256 chan。

## 9. 与已落地补丁的关系

下列改动是本架构的局部实现，不是完成证明：

- `codex-web` 列入 relay 跨回合常驻、`set_observation_scope` 会触发 Subscribe + 同步 `AttachLiveThread` + flush，逐 session 回传 attach/Subscribe 结果（L2 **入口与生效信号均已建**，§6.2.1）
- `codex-web` 禁用 relay idle timeout 合成假 `turn_completed`（禁止假完成的已有落地）
- 登录 LaunchAgent 座位、0.25s KeepAlive 循环
- 取消 exact CLI 版本 fail-closed，避免挡住座位
- dsh-web `--no-open`
- ownership 文案改为要求完整退出 Desktop，而不是抢锁
- iOS 重连后前台恢复事务会 `setObservationForeground(true)`（声明链存在且已验证：恢复事务等可验证声明 + 当前 client，8f3ad3e）

它们可以保留，但必须被 §4 四层重新解释。若与本文冲突，改代码或改本文，禁止再叠第四个特例。

## 10. 评审处置（2026-08-23）

对象：[2026-08-23-codex-web-continuity-architecture-review.md](2026-08-23-codex-web-continuity-architecture-review.md)。
「采纳」= 写入本合同。「不采纳 / 部分采纳」必须有理由，避免实施 agent 把评审原文当第二合同。

| 评审项 | 处置 | 理由 |
|---|---|---|
| R1 主断点改为 durable/live 不对称；256 chan 降为写路径 | **已落实（归因候选→确认）** | §7 #7（12:29:52 长任务中重启 Link）确认根因；`turn_completed/turn_error/turn_aborted` 已进 LiveFrameBuffer 回放白名单（§4.4、§6.3.1 选型 a）。256 chan 保持写路径面 |
| R2 补 offlineQueue / K4Patch / sink / iOS handler 与 2048 缓冲 | **采纳** | 均为旁观交付链上的真实丢失点 |
| R3 §6.3 增加断连后 completed 仍达；原超容量测试保留并标明写路径 | **采纳** | 防止修错面的测试绿 |
| R4 §7 增加锁屏/退后台或中途重启 Link 且必须相位收口 | **采纳** | 现为 #7 |
| R5 删除「只能等切 session」机制表述 | **部分采纳** | 删除「没有重连声明链」；**保留** owner 表面现象「切 session 才好」作为绕过，机制改为 fire-and-forget / 竞态。删掉现象会让后续 agent 以为真机没发生过 |
| R6 §6.2 改为可验证声明 + 循环换 client；注明声明链已存在 | **采纳** | |
| R7 无 codex-web watcher、惰性 rebind、租约降级 | **采纳为结构事实** | 写入 §4.3。**不**把补 `StartCodexRelayWatcher` 或拆租约降级列为 L2 完成项（见下） |
| R8 `set_observation_scope` 是单一充分入口 | **采纳** | |
| R9 stall watchdog；无 scope 时 milestone 假在听 | **采纳** | |
| 附录 A CPU = PairDeviceButton 常驻动画 | **采纳为 §6.4 采样结论** | **不**并入 L0–L3 施工。未做「减弱动态效果」owner 对照前，不把改动画写成连续性验收 |
| 把 durable 不对称写成已证实根因并立即按它改代码 | **不采纳** | 评审自己标「归因候选」。合同先改层描述，施工仍先用 §7 #7 日志/真机坐实或证伪 |
| L2 完成 = 给 codex-web 加 relay watcher | **不采纳** | L2 完成标准是当前 session 的 scope RPC **生效**。watcher 会变成第二观察源，违反「单一充分入口」 |
| 先拆 full_stream→milestones_only 软降级 | **不采纳** | 评审称其为反症状、用于排除。未证明它造成「切 session 才好」之前不动租约语义 |
| 用扩 `dispatchEvent` 256 / 终态优先修旁观卡执行中 | **不采纳** | 修错通道 |
| 为 CPU 放慢座位 0.25s 或停被动订阅 | **不采纳** | 采样否定这两条是 40% 来源 |
| 现在重反编译 asar 改 §2 数字 | **不采纳** | 仓内多源一致；Desktop 升级时再核，不把评审「未复核」当成数字作废 |

## 11. 二轮评审处置（2026-08-23 r2）

对象：[2026-08-23-codex-web-continuity-architecture-review-r2.md](2026-08-23-codex-web-continuity-architecture-review-r2.md)（评审对象为修订版合同 `cc61709`）。

| 评审项 | 处置 | 理由 |
|---|---|---|
| F1 `Ok:true` 假验证；attach 异步吞错；`obsClient==nil` 静默跳过 | **已落实** | 源码直证成立并已修复：`handleSetObservationScope` 逐 session 同步 Subscribe + `AttachLiveThread`，`ObservationSessionAttach{Subscribed, Attached, Error}` 逐 session 回传，任一失败返回 `observation_attach_failed`（`go-bridge/handlers.go`）。L2 不再存在按「等到 Ok」的落地风险 |
| F2 选项 b pull 必须溯源官方 `thread/read` | **采纳为选型前提** | 订阅者门在 Kernel ingest 之前。写入 §4.4 目标形态与 §6.3.1。不在此刻选定 (a) 或 (b) |
| F3 dispatchEvent 在已有写 session 时两路并行 | **采纳为注脚** | 写入 §4.4。不改变「纯旁观不走 dispatchEvent」主判断 |
| F4 #7 以重启 Link 为必测强变体，锁屏为补充 | **采纳** | 写入 §7 #7。锁屏单独通过 ≠ L3 完成 |
| 把 F1 做成新的观察协议方法 / 第二 RPC | **不采纳** | 合同要的是**现有** `set_observation_scope` 的成功定义含 attach 结果。另开方法会变成第二入口，违反单一充分入口 |
| attach 必须同步阻塞到 Desktop 释放 writer 才返回 Ok | **不采纳** | Desktop 在私有 stdio 时 writer 可能一直不放。正确行为是 **诚实失败**（结果里可见），不是挂起恢复事务 |
| 为让 (b) 变绿而拆掉 `HasSessionSubscriber` 门、无订阅也 ingest Kernel | **不采纳** | 评审只要求 (b) 的数据源前提。拆门会给未打开的 session 建隐藏时间线，与被动泵现有纪律相反 |
| 用锁屏/退后台变体单独关闭 L3 | **不采纳** | 连接未断时走 per-conn 缓冲，会假绿 |
| 因 F1 宣布「单一充分入口」作废、从零再造 L2 | **不采纳** | 入口仍是这一个 RPC；缺的是生效信号，不是再造三条触发路径 |

## 12. 现场记录（2026-08-23 下午：daemon restart 后的同步回归与两处认知修正）

背景：owner 重启 MacBridge 后，Mac 端发消息 iOS 端不同步；在 MacBridge 点击「重启共享
Codex 服务」后打开 session 仍显示「无法加载会话投影。重新打开会话可重试」。本节记录本次
诊断的根因与两处对早前判断的修正；修复随本节同提交。

### 12.1 根因：`withClient` 断线判定漏掉 `broken pipe`，daemon restart 后 RPC 泵永久死连接

实测证据链（go-bridge.log，2026-08-23 16:34–16:39）：

- 16:35:39 `daemon restart`（按钮动作）替换控制 socket。观察连接按 §8.3 backoff 在
  16:35:41 重连成功；但中央泵的主 RPC 连接进入「半死不通知」状态——整份日志
  `connection lost, starting background re-probe` 出现 **0 次**，泵从不主动感知失败。
- 16:36:02 投影命中内存 shadow（`delta_at_head`，未走 wire），随后 16:36:02.522 起
  `official model catalog unavailable: write unix ->…: write: broken pipe` 持续刷；
  16:36:06/14 `list_sessions error_code=list_failed`；16:36:11/13/15 水化
  `projection.hydrate_failed`（3ms 即失败＝写死连接）——iOS 的「无法加载会话投影」即
  16:36:15 `req_112` 该结果的面上映射。
- 根因在 `agent/codex-web/session.go isConnectionLoss`：只认 `connection closed / connection
  lost / websocket: close` 三种子串。daemon restart 的断流形状是 **`write: broken pipe`**，
  不命中 → `withClient`（唯一带自动重 Probe 的入口）永不刷新 endpoint → 水化、目录、指纹、
  模型清单全部停在死连接上，直到 MacBridge 自身重启。

修复：`isConnectionLoss` 增加 broken pipe / connection reset / connection refused /
use of closed network connection 子串，并按 `syscall.EPIPE/ECONNRESET/ECONNREFUSED/
ECONNABORTED` errno 识别包装错误；`TestIsConnectionLossCoversDeadSocketShapes` 覆盖
各形状并断言官方 `RPCError` 不误判。这同时恢复了 §8.3 冷校准路径（`thread/read` 只读
水化）在 restart 后继续工作的闭环——L2 capture（`set_observation_scope` 重订阅循环每
轮走 `withClient`）与 L3 投影都会在 restart 后数秒内自愈，不再要求 reopen session。

### 12.2 认知修正 A：同 daemon 另一客户端持有 writer 时，observer resume 同样 -32600

此前代码 WARN 与 `OwnershipConflictError` 文案把 `-32600 already has an active writer`
解释为「Desktop 已回退到私有 stdio app-server，请完全退出并重开 Desktop」。本次实测否定了
该归因：Desktop（ChatGPT 内嵌 `codex app-server`，`CODEX_APP_SERVER_USE_LOCAL_DAEMON=1`）
打开着目标 session 时，**同一共享 daemon 上** observer 的 `thread/resume` 也得到同样
错误——writer 座位按进程/客户端独占。这正是 dumps/ownership `second_resume` 的本来含义
（此前注释误读为「同 daemon 多连接 resume 无 writer 冲突」，指错了方向）。

修正：WARN 文案改为中性事实（writer 被另一 app-server 持有，只读路径仍可用）；冲突提示
改为「关闭持有该会话的 Codex 客户端窗口或等待官方卸载；只读投影（thread/read）与事件
广播不受影响」，不再给出「Desktop 离开了共享 daemon」的错误诊断。

### 12.3 认知修正 B：turn/item 事件是全局广播，观察订阅不依赖 resume 成功

dumps/ownership `raw.jsonl` 明示：连接 #2（第二 app-server）**尚未 resume、也没做任何
订阅请求**时，即收到 `thread/started`、`turn/started`、`item/started`、`item/completed`
广播（时间戳早于其 `thread/resume` 请求）。即：

- 事件流 ≠ writer 座位。`attachOk=false`（resume 冲突）不代表 iOS 收不到实时事件；
- 冲突期 `thread/read` 可用（`readonly_during_writer: true`），投影与 §8.3 冷校准依然成立；
- L2 失败语义应读作「attach（writer 地位）未获得」，不是「观察断流」。

正文 §2 宿主事实 #6 与 §4.3/§4.4 的相关表述按本节修正口径理解；§10/§11 中引用「Desktop
私有 stdio 时 writer 不放」的行保留（那是另一个真实失败模式，与本节 A 场景共存）。
