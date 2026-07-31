# 2026-05-11 CCCodeBridge stop 断线感知联合分析

## 背景与目的

本文档用于沉淀当前针对以下问题的联合分析结论，供多个 agent 并行复核：

- iOS 端为 Codex 模式。
- Mac 端 Bridge 运行正常时，iOS 冷启动可以正常连接、查看 session。
- Mac 端点击 Stop Bridge 后，iOS 前台**没有及时出现“连接中 / 离线模式”横幅**，小圆点也可能仍保持绿色。
- 但此时打开部分未缓存 session，会出现“加载消息失败 / 未连接”等错误。
- 重启 iOS App 后，离线/未就绪状态表现反而更符合预期。
- Mac 端重新 Start Bridge 后，iOS 运行中恢复能力已有改善，但仍存在 stop 无感知、session list / session 打开状态不一致等问题。

本文档只记录**分析结论**，不包含新的代码修改。

---

## 现象摘要

根据当前真机反馈，问题呈现出如下稳定模式：

1. **运行中 stop Bridge 时，iOS 前台无明显全局断线提示**
   - 没有及时出现顶部橙色/红色横幅。
   - 小圆点可能仍保持绿色。

2. **页面级请求可能已经失败**
   - 打开其他 session 时，会出现“加载消息失败”“未连接”等错误。
   - 这说明系统并非完全正常，只是**全局连接态没有及时同步到 UI**。

3. **重启 iOS App 后，离线/未就绪状态更符合预期**
   - 表明问题更像是“运行中状态迁移失败”，而不是“冷启动状态判定错误”。

4. **Start Bridge 后的恢复链已经比之前明显改善**
   - 部分场景无需重启 iOS 即可恢复连接或查看 session。
   - 说明“重连成功后 backend client 恢复”这条链已经不是当前最主要矛盾。

---

## 已核对的关键事实

以下事实已经基于当前仓库代码核对。

### 1. Mac App 的 Stop 按钮实际走 `process.terminate()`

产品态下，用户点击 Stop Bridge，实际调用路径是：

- `MacBridge/MacBridge/App/MacBridgeApp.swift`
- `onStop: { dependencies.runtimeManager.stop() }`
- `MacBridge/MacBridge/Services/RuntimeManager.swift`
- `RuntimeManager.stop()`

`RuntimeManager.stop()` 的行为是：

- 设置 `userStopped = true`
- 调用 `terminateProcess()`
- `terminateProcess()` 中执行 `process.terminate()`
- 最多等待 2 秒，然后将 `bridgeProcess = nil`

**结论**：用户点击 Stop 时，首先是**终止 go-bridge 子进程**，而不是先通过 management API 走 `/internal/shutdown`。

### 2. go-bridge 的统一 shutdown 路径只做 `httpServer.Shutdown(...)`

在 `go-bridge/main.go` 中：

- `SIGTERM` / `SIGINT` 和管理 API `/internal/shutdown` 最终共用同一个 `shutdown()`
- `shutdown()` 内部主要执行：
  - `cancel()`
  - `httpServer.Shutdown(shutdownCtx)`，超时 5 秒

**当前没有看到 shutdown 阶段主动遍历并关闭所有活跃业务 WebSocket 连接的逻辑。**

### 3. go-bridge 目前没有“用于 stop/shutdown 的全局业务 WS 连接关闭机制”

在 `go-bridge/server.go` 中：

- WebSocket 升级后进入：`for { ws.ReadMessage() }`
- 连接对象 `Conn` 会在当前 handler 生命周期内存在
- 当前连接没有被统一注册到“shutdown 可遍历关闭”的全局池中

仓库中确实存在连接注册表：

- `go-bridge/device_conn_registry.go`

但它只用于：

- **device revoke** 后主动断开某个设备的连接

而不是用于：

- **Bridge stop / shutdown** 时主动关闭所有连接

**结论**：当前 stop/shutdown 并不会确定性地主动给现有 iOS WebSocket 发送 close frame。

#### 补充：现有基础设施可以复用，但覆盖范围各有盲区

当前仓库并不是“完全没有连接管理基础设施”，至少已有两套可复用信息源：

- `go-bridge/device_conn_registry.go`
   - 以 `deviceID -> []*Conn` 维护**已认证连接**
   - 当前用于 device revoke 后的定向断连
- `go-bridge/types.go` 中的 `Broadcaster.connSubs`
   - 以 `conn -> SubscriptionKey` 维护**已订阅连接**
   - 当前用于广播订阅管理与 `UnsubscribeAll`

这意味着，实现“shutdown 时主动 close 所有连接”并不一定要从零开始，至少存在两条低侵入路径：

- 方案 A：给 `DeviceConnRegistry` 增加 `CloseAll()`，shutdown 前调用
- 方案 B：从 `Broadcaster.connSubs` 提取活跃 `*Conn`，逐个 close

但这两条路径都各自有覆盖盲区：

- `DeviceConnRegistry` 不覆盖**未认证连接**
- `Broadcaster.connSubs` 不覆盖**已连接但尚未 subscribe 的连接**

因此，从长期干净度与覆盖完整性看，最稳妥的方案仍是：

> 在 `ServeHTTP` 建立连接时注册、连接退出时注销，增加一个独立的“活跃业务连接注册表”，shutdown 时统一遍历 close。

不过从工程落地角度看，`DeviceConnRegistry` 完全可以作为第一步试验性修复基础，避免从零摸索。

### 4. iOS 端 `receiveLoop()` 的断线感知主要依赖底层 `receive()` 抛错

在 `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeTransport.swift` 中：

- `receiveLoop()` 通过 `try await task.receive()` 循环接收消息
- 只有当 `task.receive()` 抛错时，才会走到 receiveLoop 退出后的清理逻辑
- 退出后才会进入：
  - `updateState(.failed(...))`
  - `triggerReconnect()`
  - 进一步触发 `BridgeProvider.applyTransportState(...)`

**结论**：如果底层 socket 在 stop 后没有及时让 `receive()` 返回错误，iOS 全局断线感知就不会立即发生。

### 5. iOS 端 idle 场景还依赖 ping 心跳兜底

在同一文件中：

- `startPingTimer()` 每 10 秒尝试一次 ping
- 5 秒 pong timeout
- 超时后调用 `recoverFromTransportOperationFailure(...)`
- 理论上会推进 reconnect / failed 状态

**结论**：如果 `receive()` 不退出，系统仍希望靠 ping 来兜底发现断线。

补充一点：当前 `startPingTimer()` 实现只有失败日志，缺少以下确认性日志：

- ping timer 启动日志
- 每次发送 ping 的日志

因此在现有日志条件下，如果真机上没有看到 `"[Transport] ping 失败"`，我们还不能区分：

- ping 已经启动，但没有检测到断线
- 还是 ping timer 根本没有按预期跑起来

#### 补充：`recoverFromTransportOperationFailure` 在可重连分支不会先推 `.failed`

这里还有一个容易被忽略、但会影响时序判断的事实：

- 当 `recoverFromTransportOperationFailure(...)` 发现当前 transport **可重连**时
- 它不会先执行 `updateState(.failed)`
- 而是直接 `cancel` 当前 socket 并调用 `triggerReconnect()`

`triggerReconnect()` 本身也不推状态，它只是：

- 置 `isReconnecting = true`
- 启动一个异步 `Task` 进入 `reconnectLoop(...)`

而真正第一次推进 transport 状态的是 `reconnectLoop(...)` 内的：

- `updateState(.reconnecting(attempt: 1))`

根据当前代码顺序，这个 `updateState(.reconnecting(...))` 是在**第一次退避 sleep 之前**执行的，而不是等 1 秒退避后才执行。

因此更准确的说法是：

> ping timeout 触发后，存在一个 **Task 调度空窗**，但不是“必须等完整第一次退避后才会把 reconnecting 推到 NetworkStateManager”。

这不改变核心结论——如果真机上连橙色横幅都没有，仍更像是 ping/receive 根本没有把 transport 状态推进起来——但能让时序推理更精确。

### 6. 横幅和小圆点都只认 `NetworkStateManager`

- `BridgeReconnectingBanner.swift`
- `NetworkStateIndicator.swift`

它们只消费：

- `NetworkStateManager.state`

而 bridge-native 路径下，`NetworkStateManager` 主要由：

- `BridgeProvider.applyTransportState(_:)`

来驱动。

**结论**：如果 transport 没有进入 `.reconnecting` / `.failed`，那么横幅和小圆点就不会变。

### 7. 当前并不是“要等到第 5 次重连才会变红”

另一个常见误读需要澄清：

在 `BridgeProvider.applyTransportState(_:)` 中：

- `.reconnecting(let attempt)` 时会先调用 `NetworkStateManager.shared.didDisconnect(...)`
- 当 `NetworkStateManager.shared.reconnectAttempts >= 3` 时，就会进一步转为 `.offline`
- `BridgeReconnectingBanner` 对所有非 `.online` 状态都会显示

因此：

- 第 1~2 次重连时，理论上就应该看到橙色“重新连接中...”横幅
- 第 3 次后，理论上可进入红色“离线模式”

**结论**：如果真机上“连橙色横幅都没出现”，更像是 transport 状态根本没有被推进，而不是已经在 reconnect 但 UI 被吃掉。

### 8. 客户端存在 20s 业务超时 vs 30s transport timeout 的分层错位

在：

- `OpenCodeiOS/OpenCodeiOS/Services/MessageCacheManager.swift`
- `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeTransport.swift`

当前行为是：

- `fetchMessagesWithTimeout(...)`：20 秒业务超时
- `transport.sendRequest(...)`：30 秒 transport request timeout

20 秒业务超时后抛出的是：

- `OpenCodeError.serverError("请求超时")`

**结论**：页面层可能先在 20 秒报“加载失败/请求超时”，但 transport 侧还没推进到 `.reconnecting/.failed`，从而造成“页面先报错、横幅和绿点后知后觉甚至不变”的分裂体验。

---

## 对另一位 agent 分析的认同与修正

## 认同的部分

以下判断我认为方向是对的：

1. **服务端 stop/shutdown 当前不具备确定性主动关闭现有业务 WebSocket 的能力。**
2. **iOS 端当前确实把断线感知很大程度押在 `receive()` 或 ping 上。**
3. **如果真实 stop 场景中 `receive()` 长时间不报错，而 ping 也没有被观测到生效，就会直接表现为前台无横幅。**
4. **服务端 stop 时主动 close 所有活跃 WS，是最具确定性的修复方向。**

## 需要修正的部分

### 修正 1：用户点击 Stop 的实际入口不是 management API shutdown

另一个 agent 重点分析了 `/internal/shutdown`，但产品态用户点击 Stop 的真实路径是：

- `RuntimeManager.stop()`
- `process.terminate()`

不过这里更准确的表述应该是：

> **入口不同，但终态一致。**

也就是说：

- 产品态用户点击 Stop，入口是 `RuntimeManager.stop()` → `SIGTERM`
- 管理 API 路径，入口是 `/internal/shutdown`
- 但两者最终都会落到 `main.go` 的同一个 `shutdown()`

因此，这一节不是要否定另一位 agent 对 shutdown 终态的分析，而是为了澄清：

- **产品态真实入口** 是 `process.terminate()`
- **分析的最终落点** 仍然是同一个 `httpServer.Shutdown(...)`

### 修正 2：主因不太像 `isReconnecting` guard 把 UI 挡掉

如果 ping timeout 已经真正触发到：

- `recoverFromTransportOperationFailure(...)`
- `triggerReconnect()`
- `updateState(.reconnecting(...))`

那么：

- `BridgeProvider.applyTransportState(_:)` 理论上就会被调用
- 顶部至少应该出现橙色“重新连接中...”横幅
- 小圆点也应从绿变橙

因此，当真机现象是：

- 连橙色横幅都没有
- 小圆点仍然是绿色

更合理的解释是：

> transport 根本还没有进入 `.reconnecting/.failed`，而不是进入了但被 UI 层挡住。

### 修正 3：`sendPingAndAwaitPong` 的 continuation/cancellation 问题值得修，但不像主因

当前 ping 实现中确实存在 continuation 在取消下可能悬挂的风险，这属于真实的健壮性问题。

但只要 timeout 分支真正赢了，按现有代码仍应进入：

- `recoverFromTransportOperationFailure(...)`

因此它更像：

- **应该修的并发/资源问题**
- 而不是本次“前台完全无横幅”的第一根因

---

## 当前最可靠的联合根因判断

综合代码核对与现象，我认为当前最可靠的联合根因是三件事叠加：

### 根因 A：服务端 stop 时没有确定性地主动关闭现有业务 WebSocket

这意味着：

- iOS 不一定能第一时间感知连接已断
- `URLSessionWebSocketTask.receive()` 可能持续挂起
- `receiveLoop()` 不退出，就不会推进 transport 状态迁移

### 根因 B：iOS 端断线感知过度依赖 `receive()` / ping

如果 `receive()` 没及时报错，系统只能依赖 ping 心跳。

但当前真机反馈尚未证明：

- stop Bridge 后，ping timeout 真的如预期发生并推进了状态机

### 根因 C：20s 页面级业务超时早于 30s transport timeout

这会制造出一个很容易误导排查的现象：

- 页面已经报“加载失败 / 请求超时 / 未连接”
- 但全局 `NetworkStateManager` 还没变
- 因为 transport 还没被判定为 reconnecting / failed

这正好解释了：

- 打开别的 session 会失败
- 但顶部横幅仍不变
- 小圆点仍可能保持绿色

### 补充观察：服务端与客户端的 keepalive 是两套独立机制，且参数不对称

当前系统两端都做了 keepalive，但它们是彼此独立的：

- 服务端 `go-bridge/server.go`
   - 每连接 30 秒发一次 ping
   - 90 秒无 pong 则关闭连接
- iOS 客户端 `CCCodeBridgeTransport.swift`
   - 每 10 秒尝试一次 ping
   - 5 秒 pong timeout

当 Bridge stop 后，服务端的 ping goroutine 会随进程终止而一起消失，因此这条检测链不再起作用；真正仍在运行的是 iOS 侧自己的 ping timer。

这带来一个额外但很现实的时间窗口：

> 即使 iOS 侧 ping 机制完全正常，最坏也需要约 10s + 5s = 15s 才能把 stop 感知为断线。

如果用户恰好在这 15 秒窗口内尝试打开 session，就可能先体验到“页面失败”，而全局横幅尚未切换。这和“20s 页面超时 vs 30s transport timeout 错位”属于同类体验裂缝，只是时间尺度更短。

这里还有一个边界条件需要明确：

- 这个 **15 秒窗口** 主要适用于 **Bridge 进程已停、iOS 发出的 ping 无法获得 pong** 的场景
- 如果 Bridge 进程其实还活着，只是 backend runtime 本身出了问题，那么 iOS 侧 ping 仍可能收到正常 pong

在后一种场景下：

- iOS 侧 ping 不会超时
- 15 秒窗口分析就不成立
- 问题会更偏向“backend 不可用，但 transport 连接仍然健康”的另一类故障

这个边界条件对未来排查类似“Bridge 活着但后端挂了”的问题有参考价值。

---

## 为什么“重启 iOS App 后表现更符合预期”

重启 App 后，会重新走：

- 自动连接
- hello 握手
- backend 列表构建
- 初始 session 加载

这相当于系统重新进行一次**冷启动状态判定**。

如果此时 Bridge 确实已经停了：

- 连不上 / hello 失败 / backend 不存在
- 那么离线、未就绪等 UI 就会稳定出现

因此：

> 重启后表现更正常，并不说明运行时链路没问题；反而恰恰说明问题集中在“运行中状态迁移”。

---

## 当前优先级最高的验证建议

在继续修改代码前，建议先补充一轮真机证据采集，重点确认下面几件事。

### 验证 1：stop 后 iOS 端是否真的出现了 ping timeout / reconnect 日志

重点观察 iOS 侧日志中是否出现：

- `[Transport] receiveLoop started`
- `[Transport] ping 失败`
- `[Transport] updateState: reconnecting(...)`
- `[BridgeProvider] applyTransportState: ...`
- `[Network] transition ...`

如果后续补充日志，建议再明确观察：

- `[Transport] ping timer started, interval=10s timeout=5s`
- `[Transport] sending ping`

如果 stop 后只有：

- 页面加载失败
- `fetchMessagesWithTimeout 超时`

但没有上述 transport/network 日志，则基本坐实：

> 页面失败先发生了，但 transport 并没有真正感知到底层断线。

进一步地，如果连 `receiveLoop started` 之后都完全看不到任何 ping 相关日志，那么排查优先级应提升为：

> `startPingTimer()` 是否根本没有跑起来，或 pingTask 是否被过早取消。

补充一点：如果后续已经确认出现了 `ping timeout` / `recoverFromTransportOperationFailure(...)`，但仍然没有看到 `updateState(.reconnecting(...))`，那要优先检查的就不再是“退避太长”，而是：

> `triggerReconnect()` 的 guard 条件是否阻止了重连任务创建，或相关竞态是否让我们误以为“没有进入 reconnectLoop()”。

之所以这样表述更准确，是因为当前 `triggerReconnect()` 的保护条件本来就会拦截重复启动：

- 如果 `isReconnecting == true`，后续触发的 `triggerReconnect()` 会直接 return
- 这通常只是为了避免重复创建重连任务，本身并不代表 bug

因此，排查重点应先放在：

- 是否**有一个** `triggerReconnect()` 成功越过 guard 并创建了任务
- 以及后续是否真的看到了第一次 `updateState(.reconnecting(...))`

而不是优先假设 Swift 运行时层面的“Task 创建了但完全没有执行”。

### 验证 2：stop 时服务端是否有任何主动 close 活跃连接的动作

当前代码上看没有；真机/本地日志上应进一步确认：

- stop 前活跃连接数是多少
- stop 时是否逐个 close 了连接
- iOS 侧是否收到 close / abnormal closure

### 验证 3：页面失败时间点 vs transport 状态切换时间点

如果能在日志中对齐下列事件的时间戳，将非常有帮助：

- 第一次 session 打开失败时间
- 20s 业务超时触发时间
- transport request timeout 30s 触发时间
- 第一次 `.reconnecting` / `.offline` 出现时间

如果页面失败显著早于 transport 状态变化，则“20s vs 30s timeout 错位”会被进一步坐实。

---

## 当前优先级最高的修复建议

### 修复方向 1（最高优先级）：服务端 stop/shutdown 时主动关闭所有业务 WebSocket

建议在 `go-bridge` 中引入一个**全局业务连接注册表**，在 shutdown 阶段按如下顺序处理：

1. 遍历所有活跃业务连接
2. 显式发送 WebSocket close frame
3. 主动关闭底层连接
4. 再执行 `httpServer.Shutdown(...)`

之所以强调这个顺序，是因为当前 `httpServer.Shutdown(5s)` 是**阻塞等待 handler 自然退出**：

- 它会先关闭 listener
- 然后等待活跃 handler 结束
- 最多等待 5 秒

如果 `ServeHTTP` 内部的 `ws.ReadMessage()` 还在阻塞，单纯调用 `Shutdown` 并不会主动把这些 goroutine 打断；而如果先 close 活跃 WS，`ReadMessage()` 会更快返回错误，handler goroutine 才能尽快退出，`Shutdown` 也不必傻等满 5 秒。

实现路径上，当前有三种可选方案：

- 低侵入试验版：给 `DeviceConnRegistry` 增加 `CloseAll()`
- 低侵入广播版：从 `Broadcaster.connSubs` 提取活跃连接并 close
- 覆盖最完整版：新增独立的“全局活跃业务连接注册表”

综合覆盖面与长期维护成本，推荐第三种；但如果要快速验证思路，第一种也具备较高复用价值。

这是当前最确定、最可解释、最不依赖底层网络偶然性的修复。

### 修复方向 2：加强 iOS 端 transport/ping 日志

建议在以下节点增加真机可读日志：

- ping timer 启动（例如：`[Transport] ping timer started, interval=10s timeout=5s`）
- 每次发送 ping（例如：`[Transport] sending ping`）
- ping timeout
- `recoverFromTransportOperationFailure(...)`
- `triggerReconnect()`
- `updateState(...)`
- `applyTransportState(...)`

目的不是长期保留所有日志，而是为了下一轮真机测试能快速确认：

- 到底是 receive 不退出
- 还是 ping 根本没启动
- 还是 ping 触发了但没检测到断线
- 还是状态变了但 UI 没跟上

### 修复方向 3：收敛 20s 业务超时与 30s transport timeout 的错位

候选做法包括：

- 当 20s 业务超时发生在 **bridge-managed client** 场景下，先做一次轻量级 transport 健康检查
   - 例如检查底层 `transportState` 是否仍然声称自己是 `.connected`
   - 如果 transport 自认为 connected，但实际请求已经超时，这是一个很强的“状态过时”信号
   - 这条路径的优点是：只在真正超时时才额外做检查，不需要全局缩短所有 transport 请求超时
- 让 transport timeout 早于或不晚于页面级 timeout
- 或者当页面级 timeout 发生时，对 bridge-managed client 更积极地推进网络状态
- 或者至少保证 timeout 语义不会被完全包装成普通 `serverError("请求超时")`

否则即使 stop 感知链改善，仍可能持续出现“页面失败先于横幅变化”的体验裂缝。

---

## 当前阶段的总结结论

一句话总结：

> 当前问题最像是“服务端 stop 未确定性 close 现有 WebSocket + iOS 端断线感知过度依赖 receive/ping + 20s 页面超时早于 30s transport timeout”三者叠加，而不是单一 UI bug 或单一 reconnect 状态机 bug。

更具体地说：

- 我认同另一位 agent 把重点放在“服务端 shutdown 行为”和“URLSessionWebSocketTask 真机表现”上。
- 但我不认同把主因归到 `isReconnecting` guard 或 `[weak self]`；这些更像次级问题。
- 我认为他漏掉的关键一层，是**客户端 timeout 分层错位**，这非常符合当前“页面失败但全局状态没掉”的现象。

---

## 适合转给其他 agent 的短摘要

可以直接转发以下摘要：

> 已核对当前代码：Mac App Stop 实际走 `RuntimeManager.stop()` → `process.terminate()`；go-bridge 收到终止信号后只执行 `httpServer.Shutdown(5s)`，当前没有用于 bridge stop/shutdown 的“全局业务 WebSocket 主动 close”机制，只有 device revoke 的定向断连。iOS 端 `CCCodeBridgeTransport.receiveLoop()` 主要依赖 `task.receive()` 抛错退出，idle 场景再由 ping 兜底；但真机现象下，这两条链路都**尚未被日志充分证实或证伪**。这里的不确定性很大程度上来自当前 ping 日志粒度不足：现有日志还无法区分“ping 根本没跑”和“ping 跑了但没检测到断线”。另一个关键点是客户端存在 20s 页面级超时与 30s transport timeout 的错位，这会造成“页面先报错，但 NetworkStateManager 还没进入 reconnecting/offline”的分裂现象。当前最靠谱的联合根因不是单一 ping bug，而是：服务端 stop 不主动 close WS、iOS 依赖 receive/ping 感知断线、以及 timeout 分层错位三者叠加。最高优先级修复仍是：服务端 stop/shutdown 时确定性主动关闭所有活跃业务 WebSocket。
>
> 补充：现有基础设施并非零基础。`DeviceConnRegistry` 已掌握所有已认证连接，`Broadcaster.connSubs` 已掌握所有已订阅连接；如果要快速验证思路，可先复用它们做 `CloseAll()`，再视覆盖盲区决定是否升级为独立的全局活跃连接注册表。就产品模式而言，所有业务连接都经过认证，因此 `DeviceConnRegistry.CloseAll()` 的覆盖率会接近 100%；但在开发模式（无 `authMiddleware`）下，它会漏掉未认证连接，这个风险需要明确。

---

## 实施结果补充（2026-05-11）

基于上面的联合分析，当前仓库已完成第一轮实现，具体如下：

1. **go-bridge stop/shutdown 主动断链已落地**
    - `go-bridge/server.go` 新增 `ActiveConnRegistry`
    - `Server.ServeHTTP(...)` 在 WebSocket 建立后注册连接、清理时注销连接
    - `Conn.CloseWithControl(...)` 在关闭底层连接前会先发送 close control frame
    - `go-bridge/main.go` 的统一 `shutdown()` 已调整为：
       - 先 `server.CloseAllConnections("bridge shutting down")`
       - 再执行 `httpServer.Shutdown(...)`

2. **iOS transport/ping 诊断日志已增强**
    - `CCCodeBridgeTransport.startPingTimer()` 现在会记录：
       - `ping timer started`
       - `sending ping`
    - `triggerReconnect()` / `reconnectLoop()` / `recoverFromTransportOperationFailure(...)` 现在会记录：
       - 是否真正调度了重连
       - 当前 attempt / delay
       - 失败错误与当前 transport state

3. **20s 页面超时 vs 30s transport timeout 的收敛钩子已落地**
    - `BackendClient` 新增 `BridgeManagedMessageTimeoutHandling`
    - `CCCodeBridgeBackendClient` 实现了 `handleFetchMessagesTimeout(...)`
    - `MessageCacheManager.fetchMessagesWithTimeout(...)` 在 20s 业务超时时，会对 bridge-managed client 调用该 hook
    - 当前策略是：
       - 先记录 transport state / connected / registered 诊断信息
       - 若 transport 仍表现为 connected / connecting / registered，则触发 `recoverFromTransportOperationFailure(...)`

4. **自动化回归已通过**
    - Go：`cd go-bridge && go test ./... -count=1`
    - iOS 定向测试：51 tests / 0 failures
       - `MessageCacheManagerTimeoutTests`
       - `CCCodeBridgeReconnectTests`
       - `CCCodeBridgeHandshakeRaceTests`
       - `CCCodeBridgeConnectionStateTests`
       - `RemoteRunningSessionTests`

当前状态可以进入下一轮**真机验证**，重点观察：

- stop Bridge 后，iOS 前台是否更快出现橙色/红色横幅
- iOS 控制台是否出现新的 ping / reconnect 诊断日志
- 当页面先命中 20s 超时时，是否会更快把 `NetworkStateManager` 推进到 reconnecting / offline

---

## 最终验证补充（2026-05-11，问题关闭）

在上述第一轮实现之后，后续真机复测又暴露出一个更深层的问题：

- `CCCodeBridgeTransport` 的状态回调最初是**单订阅者**模型
- `BridgeProvider` 与后创建的 `CCCodeBridgeBackendClient` 会竞争同一个回调槽位
- 结果是：冷启动离线判断可能正常，但运行中 stop/start Bridge 时，`BridgeProvider.applyTransportState(...)` 可能收不到后续状态，导致顶部横幅、小圆点和 bridge-aware 恢复链路不同步

同时还确认了两个放大器：

1. `SessionsView` / `SidebarView` 的部分能力判断仍直接走 `BackendClientFactory.makeClient(...)`，绕过了 `BridgeProvider` 的 bridge-aware client 解析
2. 配对 Bridge 重连后，如果目标 backend 短暂未出现在 hello_ack 中，旧代码可能误走 legacy 激活路径，留下“Paired MacBridge did not provide backend ...”一类卡住状态

### 第二轮修复内容

已追加完成以下修复：

1. `CCCodeBridgeTransport` 改为**多观察者 fan-out**，不再使用单一 `onStateChange` 槽位
2. `CCCodeBridgeBackendClient` 改为 observer token 订阅/释放，避免覆盖 `BridgeProvider`
3. `BridgeProvider` 增强 paired bridge reconnect 时 backend 缺失的恢复与重试逻辑
4. `OpenCodeiOSApp.syncBridgeTargets(...)` 在 backend 尚未 ready 时不再误走 legacy 激活路径
5. `SessionsView` 与 `SidebarView` 的 session mutation / capability 判断改为走 bridge-aware 注入工厂

### 自动化验证结果

第二轮修复后的定向 XCTest 已通过：

- `CCCodeBridgePhase3Tests`
- `MessageCacheManagerTimeoutTests`
- `CCCodeBridgeReconnectTests`
- `CCCodeBridgeHandshakeRaceTests`
- `CCCodeBridgeConnectionStateTests`
- `RemoteRunningSessionTests`

结果：

- `Executed 53 tests, with 0 failures`
- `** TEST SUCCEEDED **`

### 最终结论

用户随后完成了最新一轮实际验证，并确认：

> 最新的测试结果完全符合预期，这个问题可以关闭了。

因此，本问题现在可以明确标记为：

- **已修复**
- **已完成自动化回归验证**
- **已完成最终人工/真机验收**
- **可以关闭**
