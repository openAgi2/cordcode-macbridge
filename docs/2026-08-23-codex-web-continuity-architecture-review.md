# Codex Web 连续性架构评审：双仓源码逐条核验

- 日期：2026-08-23
- 状态：**评审报告。只评不改。权威合同已于 2026-08-23 按 §6 清单修订，采纳与否见合同 §10；本文保持核验原文，不回写为第二合同。**
- 评审对象：[2026-08-22-codex-web-continuity-architecture.md](2026-08-22-codex-web-continuity-architecture.md)（MacBridge 权威版）
- 核验基线：MacBridge 本仓 + iOS 镜像仓 `../cordcode-ios-codex-web-backend/` 当前工作树
- 方法：文档中每一条代码事实主张逐条对照源码（下文所有 `file:line` 均为核验时实际命中处），三个并行只读探查 + 两轮定向追问交叉确认
- 置信约定：标注「代码直证」的为源码可复核事实；标注「归因候选」的为能解释真机症状的机制，是否即该次成因需按权威合同 §7 矩阵复测确认
- 附录 A（§8）：CPU 归因实测与 backend 空转分析，2026-08-23 同日补充；完成权威合同 §6.4 规定的采样义务

> [!IMPORTANT]
> 总评：四层归因框架、C1–C7 不变量、禁令清单、§9「已落地补丁只是局部实现」的定性，都与双仓代码现实对得上，
> 作为「禁止按现象单修」的总合同成立且必要。但 §4.4 对 L3 根因的指认指向一条**不在旁观路径上**的通道，
> §4.3 断裂点 4 相对当前代码**已过时**，且两者都漏了一个能同时解释 owner 两条真机症状的更强机制
> （durable/live 路由不对称，见 §2）。按合同自己的纪律（§8「归不进去先改本文再改代码」），
> 应先修订 §4.3 / §4.4 / §6.3 / §7，再按层开工。

## 0. 结论一览

| 评审维度 | 结论 |
|---|---|
| 文档基建（镜像同步、v2.0 回链） | ✅ 通过（§1.1） |
| L0/L1 座位与附着主张（7 条） | ✅ 全部属实，含测试守护（§1.2） |
| L2 断裂点 1–3（scope 内存、空重绑、attach 挂载点） | ✅ 属实（§1.3） |
| L2 断裂点 4（「只能等切 session」） | ❌ 与当前代码不符，重连声明链已存在（§3） |
| L3 断裂点 1（「256 chan 丢完成帧」） | ⚠️ 通道定位错误：该丢帧面不在旁观链路上；真实丢帧点另在四处（§2） |
| L3 断裂点 2–3（Stop 回拉、短消息收口解释） | ✅ 属实（§1.3） |
| Desktop asar 宿主事实 | ✅ 仓内多源锚点一致（asar 内部数值本评审无法直接复核）（§1.2） |

## 1. 核验通过项

### 1.1 文档基建

- iOS 镜像（`cordcode-ios-codex-web-backend/docs/` 同名文件）与权威版 diff 仅 3 处预期内本地化差异（适用仓库说明、前置合同/问题来源改指 MacBridge、阅读顺序第 3 条），无内容漂移，冲突规则可用。
- v2.0 拓扑合同顶部第 11 行确有「连续性合同（v2.0 拓扑之后、禁止按现象单修）」回链（`docs/2026-08-21-codex-web-backend-design.md`）。

### 1.2 L0/L1（座位 + 附着）：7 条全部属实

1. LaunchAgent `org.openagi.cordcode.codex-app-server-daemon`：脚本只循环幂等 `daemon start` + `launchctl setenv CODEX_APP_SERVER_USE_LOCAL_DAEMON 1`，循环周期由 `sleep 0.25` 决定（`MacBridge/MacBridge/Services/RuntimeManager.swift:100-115`；plist `RunAtLoad`/`KeepAlive`/`ThrottleInterval 1` 在 `:127-132`）。
2. 无 `daemon stop`/`daemon restart`，且有测试断言守护（`MacBridge/MacBridgeTests/MacBridgeBehaviorTests.swift:302-303`）；只对真实用户 home 安装（`RuntimeManager.swift:1147-1157`），fake home 有间接测试。
3. `shutdownForExit`/`stop` 只停 runtime 与托管 OpenCode，不 bootout 座位、不 `daemon stop`、不杀 Desktop（`RuntimeManager.swift:420-433, 356-364`）；bootout 仅出现在安装时替换 LaunchAgent 作业（`:1192-1193`）与清理旧 go-bridge 8777 agent（`:929`）。
4. exact `codex --version` 门已取消：版本偏差只记日志「continuing official daemon seat」，照常 `daemon start` + setenv + 装座位（`RuntimeManager.swift:1099-1127`）。
5. dsh-web `--no-open`（`agent/dsh-web/resolver.go:157`，`lifecycle_test.go:475` 断言）。
6. ownership 文案为「请完全退出并重新打开 Codex Desktop…不要关闭另一端来抢锁」（`agent/codex-web/sessions.go:262-270`）。
7. 产品路径无 managed-loopback / 第二 app-server：`agent/codex-web/lifecycle.go:5-12` 选择顺序为显式测试 URL → 复用 daemon socket → `daemon start` → fail closed；`-codex-web-app-server-url` 在 Swift 产品 argv 零出现，仅测试/env 注入。
8. Desktop asar 宿主事实的证据锚点：think.md 2026-08-22 条目（`think.md:3-11`，2500ms / 1s reconnect / patch skew / `>=0.141.0`）、`RuntimeManager.swift:91-96, 1099-1105` 代码注释、`scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md:4`（bundle `26.818.31338`）、`docs/2026-08-21-codex-web-backend-design-review.md:141`（`THREAD_UNLOADING_DELAY = 30*60s`）。多源一致；asar 内部数值本评审未直接反编译复核。

### 1.3 L2/L3 中站得住的主张

| 合同主张 | 核验结果 |
|---|---|
| 观察 scope 存 go-bridge 进程内存，重启即清空 | ✅ `go-bridge/relay_observation.go:41-54`，纯内存 map，无持久化对应物 |
| `resubscribeObservationSessions` 空 scope 时是无声空操作 | ✅ `go-bridge/handlers.go:403-426`，`scope == nil { continue }`，无错误、无成功信号 |
| `AttachLiveThread` 只挂在 `set_observation_scope`；hello 不触发 attach | ✅ 唯一非测试调用点 `handlers.go:1136-1144`；hello（`server.go:530-619`）只做能力协商 |
| codex-web relay 跨回合常驻 | ✅ `handlers_relay.go:2441-2448`（且 `:2421-2435` 禁用 idle timeout 合成假 completed） |
| `-32600 already has an active writer` 语义 | ✅ `agent/codex-web/sessions.go:246-292`；观察路径冲突只 warn 且不标记 obsSubscribed，可重试 |
| iOS `isGenerating` 对齐投影相位 | ✅ `ChatViewModel.swift:987, 1067`；legacy「todo 全完成即收口」显式关闭（`ChatViewModel+Todos.swift:250`） |
| Stop 本地 aborted 被投影回拉 | ✅ `ChatViewModel+Generation.swift:934-987` + `ChatViewModel.swift:1067-1072`；外部回合 Stop 仍发真实 abort RPC，失败仅提示 |
| 相位唯一权威是服务端投影 | ✅ `ProjectionStore.swift:872-875, 270-271`；turn_completed 只触发权威拉取（`ChatViewModel+CodexStreaming.swift:421-429`） |
| 「再发短消息才收口」的代码解释成立 | ✅ 下一回合事件少、完成帧进 Kernel、phase=idle，本地无强制收口手段 |

## 2. 发现 1（最重要）：L3 根因指错了通道，「delta 挤掉 completed」不在旁观路径上

### 2.1 代码直证：旁观链路与写路径是两条不同的通道，溢出行为相反

纯旁观（Desktop 写、iPhone 看）的完整链路（代码直证）：

```text
官方通知 → 观察连接 readLoop（chan 256，阻塞投递，rpc.go:93, 172-177）
        → Subscribe 泵（chan 256，阻塞 send 背压，不丢，events.go:507-513）
        → startPassiveSubscription（main.go:745-805，含 HasSessionSubscriber 门 :796）
        → DeltaBatcher（控制帧先 flush 同 key，保序，delta_batcher.go:101-108）
        → PublishLogical → kernel.IngestLive（event_publisher.go:829）
                          → per-conn ShouldSendEvent 滤 → sink tryEnqueue（:891）
```

`dispatchEvent` 的 256 满即丢（`agent/codex-web/events.go:116-131`，ch 在 `:160`）**不在这条链上**：它只服务中央写连接 → session listener → `relayEvents`（iPhone 经 bridge 自己发 turn 的写路径）。合同 §4.4「观察/session listener 通道容量 256」把两个溢出行为相反的通道混写成一条——实施 agent 照此修_chan，会修错面。

### 2.2 能同时解释「正文到了 + 相位卡执行中」的真实路径（归因候选，机制代码直证）

**durable/live 路由不对称**：

- `turn_completed` 被分类为 durable（`main.go:803` `Offline: IsDurableMilestone`），零目标窗口走 offlineQueue → `routeRelayOfflineStampedEvent`，**只投 RelayEnabled 设备的 relay mailbox，对 LAN 重连永不回放**（`handlers_relay.go:2664-2697`，投递点 `:2683`）。
- `turn_started` / `text_delta` 是 live 可缓冲帧，零目标时进 LiveFrameBuffer（60s/200 帧/1MB，`live_frame_buffer.go:17-22`），重连后 `FlushLiveFrameBufferForDevice` 回放（`handlers.go:364-366`；`set_observation_scope` 也在 `handlers.go:1163` flush）——回放白名单**不含 turn_completed**（`live_frame_buffer.go:104-116`）。
- iOS 侧配合条件：turn_started 会 `activateGenerationIfNeeded` 强制拉起 isGenerating（`ChatViewModel+Generation.swift:2633-2641`）。

时序合成：Desktop 长回合收尾时 iPhone 连接断开（典型触发：go-bridge 进程重启，或 iPhone 锁屏/退后台）→ 缺口期零目标：全文 + turn_started 进 LiveFrameBuffer，completed 落 mailbox 或被丢 → 重连 + scope 重声明 → flush 回放全文 + turn_started（拉起执行态），**completed 永不到** → 卡执行中；切 session / 再发短消息引入新事件才收口。该机制同时吻合合同 §3 中 L2（「重启 Link 后切走切回才同步」场景）与 L3（「再发短的才收口」）两行真机症状。

其余真实丢失点（代码直证，均应写进合同 §4.4）：

1. offlineQueue（容量 2048，`event_publisher.go:12, 266`）溢出时 `default:` 分支只 append nil，溢出处理 `if conn != nil` 跳过 nil 项——durable 帧**无声消失**（`event_publisher.go:1018-1041`）。
2. v2 连接 raw `turn_completed`/`text_delta` 被抑制（`projection_delivery.go:111, 58-77`），全靠 projection_patch；patch 溢出只标记 `projectionInvalidated` 不重发（`event_publisher.go:720-726` "K4Patch drop reason=sink_overflow"）。
3. per-conn sink `tryEnqueue` 溢出 → `conn.Close()`（`event_publisher.go:891-892, 1038-1042`）——非静默丢但更暴力，表现为断连。
4. iOS 侧：旧 client `handler=nil` 时 patch/snapshot 帧**静默丢弃**（`CCCodeBridgeBackendClient.swift:1161-1175`）；入站缓冲 2048 满即断连并清空已缓冲帧（`CCCodeBridgeTransport.swift:252, 1081-1093, 686`）。

### 2.3 影响

按 §6.3 现在的写法修 256 chan（终态优先/背压/独立完成通道），修的是**写路径**隐患，真机症状可能原样保留；§6.3 的定向测试（「合成超过队列容量的 delta + 最后一帧 completed，投影必须 idle」）会在错误的通道上通过而设备仍失败。终态不可丢的合同必须覆盖：跨断连窗口的 durable 路由（completed 对 LAN 重连不可达）、offlineQueue nil 跳过、K4Patch 溢出恢复、iOS handler=nil 丢帧。

## 3. 发现 2：L2 断裂点 4 已过时，剩余缺口是「重声明不可验证」

合同 §4.3 断裂点 4 称续约循环捕获旧 client 后「一直等到用户切 session 才 `setObservationForeground`」。当前代码里**重连声明链已经存在**（代码直证）：

```text
transport 重连 → BridgeProvider.handleTransportReconnect 重建 client，
  post .bridgeDidConnect / .bridgeBackendReady（BridgeProvider.swift:1075-1188）
→ ChatViewModel 重建 backendClient + attachProjectionStore
  （ChatViewModel.swift:1767-1776）
→ 前台恢复事务 setObservationForeground(true)
  （ChatViewModel.swift:1510-1511）
→ 以新 client + 当前 currentSessionId 重发 set_observation_scope(full_stream)
```

connectionGeneration 递增进入恢复事务 key，保证每次重连事务不被 `.ignoredHealthy` 去重短路（`ChatViewModel.swift:1463`、`ForegroundRecoveryCoordinator.swift:109-115`）；事件流循环每周期重新解析 `backendClient` 并 rebind（`ChatViewModel+SessionManagement.swift:1228-1240`）。**§6.2.1「iOS 在重连时立即按当前 currentSessionId 声明观察」按字面已经实现。**

真正残留的缺口（应替换合同断裂点 4 的表述）：

1. 声明是 fire-and-forget：`setObservationForeground` 只 spawn 租约 Task 即返回，RPC 失败不会把恢复事务标失败（`ChatViewModel.swift:1511`）。
2. 失败只打日志，20s 盲重试，且**循环内从不重新解析 client**（`ChatViewModel+SessionManagement.swift:19-36`，`bridgeClient` 在 `:19` 一次性捕获）。
3. wire 上只有 `set_observation_scope` 一个方法（`CCCodeBridgeClient.swift:186`），无租约回读，无「观察已生效」校验。
4. 事件流 rebind 有 15-strike 退避（0.5s→30s），停机后要等下一次事务/前台/发送才拉起（`SessionManagement.swift:1328-1335, 1210-1218`）。

go-bridge 刚重启时，重声明 RPC 与对端 hello/agent ready 之间存在竞态，失败即静默——这大概率才是 owner 真机「切 session 才好」的现存机制（归因候选），而不是「没有重连声明链」。

合同 §4.3 另漏三条结构性事实（代码直证，建议补入断裂点列表）：

- 无 codex-web 的 relay 安全网 watcher：`StartCodexRelayWatcher` 只覆盖 backend `"codex"`（`handlers_relay.go:602-606`）。
- live target rebind 是惰性事件驱动且以 observation scope 为准：新进程 scope 空 → 永远 rebind 0（`handlers.go:433-462`、`event_publisher.go:903-911`）。
- 重连窗口本身触发软租约降级 full_stream → milestones_only（`relay_observation.go:246-259`、`handlers.go:369-373` 注释）——注意该降级是**反症状**的（无正文但会收口），取证时可用于排除。

## 4. 发现 3（利好，建议合同写明）：`set_observation_scope` 是 L2 的单一充分入口

追问直证：`handleSetObservationScope` 一个 RPC 同时完成三件事（`handlers.go:1127-1163`）：

1. `broadcaster.Subscribe` 逐 session 绑定当前连接（`:1127-1135`）——**单独即可满足** `main.go:796` 的 Kernel-ingest 订阅者门，不需要先拉 `get_session`；
2. `AttachLiveThread`（`:1136-1144`）；
3. `FlushLiveFrameBufferForDevice`（`:1163`）。

即合同 §4.3「t=0 三件事」在 go-bridge 侧**已建待验**，不是待建；L2 剩余工作几乎全在 iOS 触发可靠性（§3 的四条缺口）。§6.2 现行措辞（「新 go-bridge 在第一条 scope 上 Subscribe + AttachLiveThread」）会被实施 agent 误读为要从零建造。边界：订阅按 conn 键，换连接必须重发；经 relay 送达的 scope 走同一 `HandleRPC` 分发（`handlers.go:1016-1018`），同样生效。

另注（合同 §3 可补充的诚实边界）：新 go-bridge 进程启动时 `startPassiveSubscription` 会 resume 全部 loaded threads，Mac 侧观察面自动铺满；无 scope 时 durable milestone + control-plane 事件仍可经 `ShouldSendEvent` 白名单放行（`relay_observation.go:201-205, 174-181`、`relay_mailbox.go:361-367`）。「部分事件在流」不等于「正在旁观」，回归对照（切 session）可能经里程碑路径假通过——验收必须断言 delta 流式（合同 §7 #2 已含「流式」，维持）。

## 5. 发现 4：次要

1. iOS 存在 10s stall watchdog（`ChatViewModel+Generation.swift:3106-3137`），但遵守「只有显式 idle 才收口」、V2 下投影 executing 则不收口——与 C5 不冲突；建议 §3 活体要点注明其存在与边界，避免后续 agent 误删或误判违规。上升沿除投影外还有 sendMessage 占位（waitingForAssistant）与 turn_started 强制拉起——后者正是 §2.2 机制的 iOS 侧配合条件，§4.4 可点名。
2. `codex-web` 禁用 relay idle timeout 合成假 `turn_completed`（`handlers_relay.go:2421-2435`）——合同 §5「禁止假完成」在这一点已有代码落地，§9 可点名。
3. asar 数值（2500ms、reconnectDelayMs 1000、`>=0.141.0`、30 分钟、bundle `26.818.31338`）本评审未反编译复核，仓内证据链一致（§1.2 第 8 条）；后续如有 asar 升级，§2 数值需按新 bundle 重核。

## 6. 建议修订清单（改权威合同，不改代码）

按合同 §8 自己的纪律执行——先改 `2026-08-22-codex-web-continuity-architecture.md`，同步 iOS 镜像，再按层开工：

| # | 位置 | 修订 |
|---|---|---|
| R1 | §4.4 断裂点 1 / §3 L3 行 | 根因换位：主断点改为「durable/live 路由不对称——turn_completed 走 relay mailbox，对 LAN 重连永不回放，LiveFrameBuffer 回放不含 completed」；256 chan 丢帧降级为写路径隐患并注明两个通道溢出行为相反 |
| R2 | §4.4 断裂点 | 补 offlineQueue nil 静默丢、K4Patch 溢出不重发、per-conn sink 溢出断连、iOS handler=nil 丢帧与 2048 入站缓冲清空 |
| R3 | §6.3 | 定向测试增加「回合中段断连/重连后 completed 仍达、相位收口」场景；原「超容量 delta + completed」测试保留但注明它只覆盖写路径面 |
| R4 | §7 矩阵 | 增加一行：Desktop 长回合中 iPhone 锁屏/退后台再回前台（或回合中重启 Link），应看到正文经回放补齐**且相位收口**——这是 durable 路由不对称的定向用例 |
| R5 | §4.3 断裂点 4 | 删除「只能等用户切 session」表述，替换为 §3 所列四条 iOS 缺口（fire-and-forget、盲重试不换 client、无生效校验、15-strike 停机） |
| R6 | §6.2.1 | 从「实现重连即声明」改为「声明必须可验证 + 失败升级 + 循环内重新解析 client」；注明声明链已存在 |
| R7 | §4.3 | 补三条结构事实：无 codex-web relay watcher、惰性 rebind 以 scope 为准、重连窗口租约降级（反症状，用于排除） |
| R8 | §4.3 / §6.2 | 写明 `set_observation_scope` 是单一充分入口（Subscribe + AttachLiveThread + flush 已在一个 RPC 内），go-bridge 侧 L2 为已建待验 |
| R9 | §3 活体要点 | 注明 iOS stall watchdog 的存在与边界；注明无 scope 时 milestone/control 泄漏可造成「假在听」 |

## 7. 给后续 agent 的取证建议

发现 1/2 的归因候选需一次真机或日志确认：在 Desktop 长回合中重启 Link（或让 iPhone 断连覆盖收尾时刻），随后抓两端日志——若 iPhone 收到回放正文但 `turn_completed` 缺席、且 go-bridge 侧可见该帧进入 `routeRelayOfflineStampedEvent`（LAN 连接），即坐实 §2.2。反之若 completed 已到 iPhone 而 `projectionInvalidated`/K4Patch 丢弃出现，则候选 3 为主因。确认后按 §6 清单修订合同，再开 L2/L3 层施工。

## 8. 附录 A：CPU 归因实测与 backend 空转分析

- 触发：owner 报告 CordCodeLink 常驻 ~40% CPU（几乎持续），并追问「5 个 backend 是否被一直监视、iOS 离线或看其他 backend 时是否仍在同步」。权威合同 §6.4 要求未采样不得动生命周期；本附录完成该采样义务并回答上述问题。
- 方法（全部只读，不改代码）：`ps` 进程快照 ×2 + `ps -M` 线程级 CPU 归属 + `sample <pid> 3` 热点栈 + 座位循环 0.1s×20 密集采样 + Swift GUI / go-bridge 常驻工作静态清点。热点栈原始文件为临时产物，可用同一条 `sample` 命令复现。

### 8.1 归因结论（实测）

**40% 常驻 CPU 与「监视 backend」「iOS 在不在线 / 看哪个 backend」「任务在不在跑」均无关；是 Swift GUI 主窗口常驻动画在主线程逐帧渲染。**

进程归属（实测快照）：

| 进程 | 实测 CPU | 说明 |
|---|---|---|
| CordCodeLink（Swift GUI） | 39.9% → 42.7% | 唯一热点 |
| cordcode-bridge-runtime | 0.2–0.3% | backend 监视本体 |
| 官方 daemon（standalone codex） | 0.1% | |
| 座位脚本 bash（0.25s 循环） | 0.3–0.7% | 每秒 4 次 `daemon start` spawn，CPU 记在 codex 短命进程上，快照未构成可见热点 |
| opencode serve（GUI 托管） | 0.1–9%（波动） | macOS ps %CPU 为近期衰减均值 |

- 线程归属（`ps -M`，实测）：CPU 几乎全在**主线程**——进程运行约 87 分钟，主线程 UTIME 34m50s ≈ 40%，其余线程均为秒级。
- 热点栈（`sample` 3s，实测）：主线程活跃分支为 SwiftUI 动画驱动（`UpdateCycle`/DriverCore）→ `CA::Transaction::commit` → `NSHostingView.layout` → `DisplayList.ViewUpdater` 逐帧更新，摘要含 `CG::stroker`（路径描边）——持续逐帧的布局+渲染管线。
- 静态对应（代码直证）：GUI 常驻循环里唯一能产生该形态的，是主窗口常驻可见的 `PairDeviceButton` 两条 `repeatForever` 动画（`MacBridge/MacBridge/Views/WorkspaceView.swift:619-691`；渲染点 `:113`/`:141` 两条主窗口路径都包含）：1.8s 呼吸（**动态 shadow 半径 4↔8** + scale + stroke opacity）+ 1.5s 扫光渐变位移。SwiftUI 动态阴影每帧离屏高斯模糊，ProMotion 高刷下常驻 20–50% CPU 属常见量级。`ApproveDeviceButton` 为同款动画（`MacBridge/MacBridge/Views/PairingView.swift:626-691`），配对确认页可见时叠加。
- 其余常驻循环均可排除（代码直证）：3s 管理面轮询稳态无扇出（`/internal/agents` 缓存命中，`go-bridge/management_api.go:547-566`；全量 backend 探测仅 runtime 重启 / 手动刷新 / 120min 兜底重启时各一次，`RuntimeManager.swift:572-598`、`agent_descriptor.go:149-192`）；无事件流/WebSocket 订阅、无日志 tail 推送、无逐 backend 健康轮询（`RuntimeManager.swift:553-568, 781-871`）。
- 免改码验证：系统开启「减弱动态效果」（`startMotion` 即停，`WorkspaceView.swift:679-683`）或最小化主窗口，CPU 应骤降。坐实后修复面为纯 UI（一处按钮的动画播放策略，例如仅首次引导时播放），不触碰本合同 L0–L3 任何一层。

### 8.2 backend 常驻监视的真实结构（代码直证 + 实测）

- 默认 6 个 driver：`claude, codex, codex-web, grokbuild, dsh-web, opencode-web`（`go-bridge/main.go:35`），全部在 runtime 进程启动即创建（`main.go:153-224`），iOS hello 只做列表下发、不触发 lazy 启动。
- 零 iOS 连接时的持续工作分三类：
  1. **web 系「读入即丢」**：codex-web 观察连接 resume 全部 loaded threads、逐事件解码（`agent/codex-web/events.go:448-525`）；dsh-web 双 WS（128 有损 tap）；opencode-web 单 SSE 引用计数。事件在 `go-bridge/main.go:796` 订阅者门前被丢弃——不进 Kernel、不进 EventBuffer、不写 relay。
  2. **60s 目录/指纹扫描**：claude 全 walk `~/.claude/projects`（`agent/claudecode/claude_session_catalog.go:159-263`）；codex / grok 走各自的单例 stdio catalog 子进程；codex 3s 头探测与 grok 5s 快扫仅在有连接时启用（`session_discovery.go:134-136`）。
  3. **默认不启用**：claude / grok 的进程级订阅、transcriptindex、file relay 均按需启动。
- 代价实测：runtime 进程 0.2–0.3%。**监视本身不是 40% 的来源。**

### 8.3 iOS 离线 / 看其他 backend 时的同步行为（代码直证）

- 订阅者门是 `(backend, session)` 粒度（`go-bridge/types.go:806-815`），在 publish 之前丢弃：iOS 离线或看其他 backend 时，codex 被动事件不进 Kernel、不写 mailbox、不占内存（零目标时 live 帧不入 LiveFrameBuffer；缓冲上限清单：EventBuffer 1 万条/16MB/5min `event_buffer.go:57-66`、LiveFrameBuffer 200 帧/1MB/60s `live_frame_buffer.go:17-23`、offlineQueue/sink 2048 `event_publisher.go:12`）。
- **例外一（同步到 relay mailbox）**：iOS 之前打开过、per-session relay 仍存活的 session——codex-web 会话约 300s TTL 无活动回收；旧 codex rollout file relay 只要文件持续增长就以 1s 轮询不退出、15min 无增长才回收（`handlers_relay.go:36-43, 443-475`）——其 durable 里程碑（白名单 `relay_mailbox.go:361-371`）持续写 relay-server mailbox，**不看观察 scope**（`handlers_relay.go:2664-2697`），24h TTL / 50MB 每设备封顶（`relay-server/internal/relay/server.go:173-177, 668`）。live 正文对离线设备不缓存。
- **例外二（里程碑到设备）**：iOS 在线但看的不是该 backend 时，仍存活 relay 的 session 无条件 publish，目标解析有「全部连接」fallback（`types.go:700-720`），设备会收到 `turn_started` / `turn_completed` / `sessions_changed`（control-plane 白名单对无 scope 设备放行，`relay_observation.go:174-181, 197-212`），`text_delta` 被观察过滤。即：里程碑会同步，正文不会。
- relay 出站 WS 常驻，与 iOS 无关（2s→60s 重连、30s ping，`relay_bridge_client.go:20, 127-173, 305-331`）。

### 8.4 对合同的增量结论

1. 合同 §3「CPU 是观测面不是拓扑失败」与 §6.4「未采样前禁止改座位 / daemon / 间隔」的判断被实测支持；§6.4 的采样义务本附录已完成一轮，建议 owner 再做一次「减弱动态效果」或最小化窗口的对照实验坐实。
2. 若坐实，CPU 修复属于纯 UI（`PairDeviceButton` / `ApproveDeviceButton` 动画播放策略），不触碰 L0–L3 任何一层，可独立于连续性施工进行。
3. 座位 0.25s 循环实测代价很小（bash 0.3–0.7%，spawn 未构成可见热点；本次快照粒度 0.1s×20 较粗，精确累计值可后续用 `top` 观测）。维持合同 §4.1 现状，无需为 CPU 调整补位间隔。
