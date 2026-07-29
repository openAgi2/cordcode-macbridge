# CCCode iOS 端和 Mac 端交互流程

本文档描述 iOS app (CCCode) 与 Mac 端 MacBridge (CCCodeBridge.app + go-bridge) 之间的完整交互链路。
涵盖配对、连接、数据加载、断连重连、设备撤销等核心场景。

## 架构概览

| 层 | iOS (Swift) | Mac (Go) |
|----|-------------|----------|
| 传输 | BridgeProvider + Transport (actor) + URLSessionWebSocketTask | go-bridge (gorilla/websocket) port 8777 |
| 认证 | device token (SecureEnclave 存储) 通过 Authorization: Bearer header 发送 | authMiddleware 校验 token，查 FileDeviceStore |
| 配对 | BridgePairingClient (WebSocket claim) | pairing_handler.go + PairingSession 状态机 |
| 业务 | ServerViewModel + SessionsViewModel + ChatViewModel | handlers.go RPC dispatcher -> cc-connect AgentSession |
| 事件 | CCCodeBridgeTransport event stream | events.go core.Event -> wire event 推送 |

**关键文件索引：**

| 文件 | 角色 |
|------|------|
| `OpenCodeiOS/OpenCodeiOS/App/OpenCodeiOSApp.swift` | App 入口，配对/连接/撤销协调 |
| `OpenCodeiOS/OpenCodeiOS/App/ContentView.swift` | 主界面，server 选择，ChatViewModel 创建 |
| `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift` | 桥接生命周期管理，cachedClients，连接状态 |
| `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeTransport.swift` | WebSocket transport (actor)，握手、重连、事件流 |
| `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgePairingClient.swift` | QR 解析，claim WebSocket，等待 approve |
| `OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift` | 桥接 backend client，RPC 代理 |
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ServerViewModel.swift` | Server 管理，connectToServer，upsertBridgeTargets |
| `OpenCodeiOS/OpenCodeiOS/Views/Session/SessionsView.swift` | Session 列表 ViewModel，backendClientFactory |
| `OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel.swift` | 聊天核心 ViewModel，backendClientFactory |
| `OpenCodeiOS/OpenCodeiOS/Views/Components/BridgeReconnectingBanner.swift` | 顶部连接状态横幅 |
| `OpenCodeiOS/OpenCodeiOS/Services/Network/NetworkStateManager.swift` | 统一网络状态机 |
| `go-bridge/handlers.go` | RPC 分发 + 会话管理 |
| `go-bridge/hello_handler.go` | hello 握手，返回 backends 列表 |
| `go-bridge/events.go` | core.Event -> iOS wire event 映射 |
| `go-bridge/server.go` | WebSocket server，认证 middleware |
| `go-bridge/pairing_handler.go` | 配对 WebSocket 处理 |
| `go-bridge/device_conn_registry.go` | 设备连接注册表，DisconnectDevice |
| `go-bridge/pairing_session.go` | 配对会话状态机 |
| `go-bridge/management_api.go` | 管理 API (创建/审批/撤销配对) |
| `go-bridge/trusted_device_store.go` | 信任设备持久化 |
| `macBridge/MacBridge/Services/RuntimeManager.swift` | go-bridge 进程生命周期管理 |
| `macBridge/MacBridge/Views/PairingView.swift` | 配对页面 UI |

## 场景一：扫码配对 (QR Code Pairing)

### 1a. Mac 端创建配对会话

1. 用户在 MacBridge Pairing tab 点击 Pair New Device
2. MacBridge 调用 `POST /internal/pairing/create` (go-bridge management API)
3. go-bridge 创建 `PairingSession`，生成 `manualCode` 和 `qrPayload`。
   当前二维码使用 `cccode://pair?id=...&code=...&local=...&remote=...&remote=...&name=...`：
   - `local` 是局域网配对端点，如 `ws://172.16.10.211:8777/pairing`
   - `remote` 可出现多次，分别表示 Tailscale、VPS/FRP 等远程候选，如 `wss://47.236.182.45:9090/pairing`
   - MacBridge 远程访问页里填写的是 bridge 地址，如 `wss://47.236.182.45:9090/bridge`；生成配对二维码时会转换为 `/pairing`
4. MacBridge 展示 QR 码和手动码

### 1b. iOS 端扫码并 claim

1. iPhone 扫描 QR 码或用户输入手动码
2. iOS 解析 `ccode://pair?` URL -> `ParsedPairingRequest` (bridgeURL, pairingID, manualCode)
3. `BridgePairingClient.claim(request:)` 先尝试 `local`，如果局域网不可达，再并行尝试所有 `remote` 配对端点
4. 发送 `{type: pairing_claim, pairingId: ..., manualCode: ..., device: {deviceId, displayName, platform: ios}}`
5. go-bridge `handlePairingWebSocket` 接收 claim，调用 `session.Claim()` 将状态从 `created` 变为 `claimed`
6. go-bridge 返回 `{type: pairing_result, ok: true}`
7. iOS 状态变为 `waitingForApproval`，挂起等待 `pairing_complete` 消息 (timeout 120s)

### 1c. Mac 端审批

1. MacBridge Pairing 页面显示 claimed 设备信息 (deviceName, platform)
2. 用户点击 Approve
3. MacBridge 调用 `POST /internal/pairing/{id}/approve`
4. go-bridge `Approve()` 将状态从 `claimed` 变为 `approved`，生成 `deviceToken` 和 `deviceID`
5. go-bridge 通过 `PairingPendingRegistry.NotifyComplete()` 向 iOS 的 WebSocket 发送 `pairing_complete` 消息 (含 device token 和 bridge 信息)
6. iOS 收到 `pairing_complete`，创建 `SavedBridge` 对象，保存到 `SavedBridgeStore` (含 localURL, remoteURL/remoteURLs, deviceId, deviceToken)
7. iOS `BridgePairingClient.state` 变为 `.completed(savedBridge)`

**冷启动带配对 URL 的安全保护：**
当用户扫码冷启动 app (而非 app 已在运行中扫描)，系统会调用 `onOpenURL` 或 `application(open:)`。
iOS 的 `OpenCodeiOSApp.body.task` 中 `consumePendingURL` 优先检查 `isPairingURL`，若是配对 URL 则直接进入配对流，跳过 `autoConnectOnLaunch`。
这防止了旧 SavedBridge 先自动连上，制造未批准也可访问的假象。

## 场景二：正常连接与数据加载 (Cold Start)

### 2a. 初始连接 (iOS 冷启动，MacBridge 已在运行)

1. `OpenCodeiOSApp.body.task` 执行：
   - 调用 `bridgeProvider.autoConnectOnLaunch()`
   - `autoConnectOnLaunch` 从 `SavedBridgeStore` 加载配对过的 bridges
   - 选择 `lastConnectedAt` 最新的 bridge
   - 检查 device token 存在后调用 `connectBridge`
2. `connectBridge()` 内部：
   - `await disconnectBridge()` 清理旧连接
   - 创建 `CCCodeBridgeTransport` (注入 deviceToken, deviceId, reconnectPolicy: .unlimited)
   - 注册 `onStateChange` 回调 (跨 actor，将 transport 状态同步到 BridgeProvider + NetworkStateManager)
   - `transport.connect(to: url)` - WebSocket 连接到 `ws://<ip>:8777/bridge`
     - 设置 `Authorization: Bearer <deviceToken>` header
     - 设置 `X-CCCode-Device-ID: <deviceId>` header
   - 等待 `hello_ack` 响应 (go-bridge `HandleHello` 函数)
     - go-bridge 校验协议版本 (BridgeProtocolVersion = 1)
     - go-bridge 返回 backends 列表 (claude_code, opencode, codex)
     - go-bridge 返回 running sessions 列表
   - transport 层：`isRegistered = true`, `reconnectAttempt = 0`, `updateState(.connected)`
3. `connectBridge` 成功后的关键步骤：
   - 设置 `self.transport = transport` (只在 connect 成功后才赋值)
   - 遍历 backends，为每个 backend 创建 `CCCodeBridgeBackendClient`，存入 `cachedClients[backendKind.rawValue]`
   - 设置 `activeBridge = bridge`, `connectionStatus = .online`
   - 调用 `NetworkStateManager.shared.didConnect()` (安全网)
   - post `bridgeDidConnect` 通知 (含 bridge 和 backends)

### 2b. Server 激活与 Session 列表加载

1. `OpenCodeiOSApp.onReceive(.bridgeDidConnect)` 触发 `syncBridgeTargets(bridge:backends:)`
2. `syncBridgeTargets` 流程：
   - 调用 `serverViewModel.upsertBridgeTargets()` 创建/更新 bridge server 条目
   - 查找匹配的 server (优先 currentServer，否则 matching.first)
   - 创建 Task 调用 `connectToServer(server, backendClient: client)`
3. `connectToServer` bridge 路径：
   - 设置 `isConnecting = true`, `connectingServerID = server.id`
   - 执行 health check (读 transport.isConnected + isRegistered，失败后等 500ms 重试一次)
   - health check 通过后激活 server：`servers[index].isActive = true`, `currentServer = servers[index]`
   - post `bridgeBackendReady` 通知
   - 设置 `isConnecting = false`
4. **SessionsViewModel** 收到 `bridgeBackendReady` 通知：
   - 重新调用 `initialize(config:)`, 用 `backendClientFactory` 重新 resolve backend client
   - 此时 `bridgeProvider.cachedClients` 已填充，resolveBackendClient 返回真正的 CCCodeBridgeBackendClient
   - 调用 `loadSessions()` -> `backendClient.fetchSessions()` -> 通过 bridge WebSocket 发送 RPC 到 go-bridge
5. go-bridge 处理 RPC：
   - 接收 `{type: request, requestId: req_1, method: list_sessions, backendId: claude}`
   - 转发到 cc-connect Claude Code agent
   - 返回 session 列表

### 2c. ChatViewModel 初始化与消息加载

1. `ContentView` 检测到 `currentServer != nil`，渲染 `ChatUIKitContainerView`
2. `ChatUIKitContainerView` 创建 `ChatViewModel` (注入 `backendClientFactory` 闭包)
3. `ChatViewModel.swift` 的 `SessionManagement.swift` 中 `switchToSession`：
   - `backendClient = backendClientFactory(config)` (第一次 resolve)
4. **关键时序问题：** 如果 step 3 发生在 `autoConnectOnLaunch` 完成前，则 `resolveBackendClient` 返回 `BridgeUnavailableBackendClient`
5. **修复：** `ChatViewModel` 监听 `bridgeBackendReady` 通知，收到后用 `backendClientFactory` 重新创建 `backendClient`，并重新加载 models 和 agents
6. 修复后，用户打开 session 时 ChatViewModel 使用真正的 bridge client，RPC 请求正常到达 go-bridge

### 连接时序关键点

**app 冷启动时的竞态条件：**
`ServerViewModel` 在 init 时从 storage 加载 servers（含上次激活的 server），`currentServer` 可能非 nil。
`ContentView.body` 立即渲染 chat 界面，`SessionsView.task` 和 `ChatViewModel` init 
在 `autoConnectOnLaunch` 完成前就创建了 backend client。此时 `resolveBackendClient` 返回 
`BridgeUnavailableBackendClient`，因为 `cachedClients` 为空。

`bridgeBackendReady` 通知机制解决了这个问题：bridge 连接成功后通知所有消费者重新 resolve backend client。

## 场景三：MacBridge Stop -> Restart

### 3a. MacBridge Stop

1. 用户在 MacBridge 菜单栏点击 Stop
2. `RuntimeManager.stop()` 执行：
   - `userStopped = true`
   - `terminateProcess()` - 杀 go-bridge 子进程，最多等 2 秒
   - `resetRuntimeState()` - 清空 managementURL, managementToken, apiClient, agents
   - `setStatus(.stopped)`
3. go-bridge 进程死亡，iOS 端 WebSocket 连接断开

### 3b. iOS 端检测断连并重连

1. `CCCodeBridgeTransport.receiveLoop` 检测到 WebSocket 断开 (URLSessionWebSocketTask.receive 抛错)
2. `receiveLoop` 执行清理：
   - `clearHelloAckContinuation()` - 清理未完成的 hello 等待
   - `isConnected = false`, `isRegistered = false`
   - `updateState(.failed(reason:))` -> `onStateChange` 回调 -> `NetworkStateManager.didGoOffline()`
   - `triggerReconnect()` - 启动 reconnectLoop
3. `reconnectLoop` 中：
   - `reconnectAttempt += 1`
   - 检查 `reconnectPolicy.canRetry` (.unlimited 策略永不放弃)
   - `updateState(.reconnecting(attempt:))` -> `NetworkStateManager.didDisconnect()`
   - 等待指数退避延迟
   - 调用 `performConnect(to: urlString)` 重新连接 WebSocket
   - 连接失败时检查是否为认证错误 (401 -> 停止重连，提示重新配对)
   - 连接失败时如果是非认证错误，继续循环重试

**此时 iOS 用户看到的：** `BridgeReconnectingBanner` 顶部橙色横幅显示连接中，`NetworkStateManager.state` 为 degraded/offline

### 3c. MacBridge Start (go-bridge 恢复)

1. 用户在 MacBridge 菜单栏点击 Start
2. `RuntimeManager.start()` 执行：
   - `userStopped = false`, `crashCount = 0`
   - `launchBridgeProcess()` -> 启动新的 go-bridge 子进程
3. 新 go-bridge 进程启动并监听 port 8777
4. iOS reconnectLoop 中的下次重试：
   - `performConnect` 成功，收到 hello_ack (backends=3, ok=true)
   - `isReconnecting = false`
   - post `bridgeTransportDidReconnect` 通知 (含 helloAck)

### 3d. iOS 端重连成功后的恢复

1. `BridgeProvider.handleTransportReconnect` 收到通知：
   - 验证 ack.backends 非空
   - 验证 `self.transport` 和 `self.activeBridge` 非 nil
   - 重建 `cachedClients` (为每个 backend 重新创建 CCCodeBridgeBackendClient)
   - 检查 backend 列表变化，通知移除已消失的 server
   - 设置 `connectionStatus = .online`
   - 调用 `NetworkStateManager.shared.didConnect()` (安全网)
   - post `bridgeDidConnect` 通知
2. `.onReceive(.bridgeDidConnect)` 触发 `syncBridgeTargets`：
   - `upsertBridgeTargets` 更新/创建 server 条目
   - `connectToServer` 执行 health check，激活 server
   - post `bridgeBackendReady` 通知
3. `SessionsViewModel` 收到 `bridgeBackendReady`，重新 initialize 并加载 session 列表
4. `ChatViewModel` 收到 `bridgeBackendReady`，重新创建 backend client 并重载 models/agents

### 3e. 前台恢复安全网

`OpenCodeiOSApp.onChange(of: scenePhase)`：当 app 从后台回到前台时，检查 bridge 状态一致性：
- 如果 `bridgeProvider.activeBridge != nil` 但 `NetworkStateManager.state` 不是 online，则检查 `bridgeProvider.connectionStatus`，如果为 online 则调用 `NetworkStateManager.didConnect()`
- 如果 bridge 已连接但 `currentServer` 为 nil，重新调用 `syncBridgeTargets` 激活 server

### 3f. 崩溃误报修复 (重要)

macBridge `restart()` 和 stop->start 曾经会误判为 go-bridge 连续崩溃 3 次。修复包含三层防护：

1. **userStopped 延迟重置**: `restart()` 不再立即 `userStopped = false`，而是延迟到 `launchBridgeProcess()` 前才重置。
   这样 terminationHandler 的异步 Task 执行时看到 userStopped=true，不会增加 crashCount。
2. **PID 校验**: `handleProcessTermination` 比对退出 PID 和 `lastLaunchedPID`。如果 PID 不匹配说明是旧进程的延迟退出通知，直接忽略。
   解决 stop->快速 start 时旧进程退出被误判为新进程崩溃的问题。
3. **shutdownForExit** 设置 userStopped=true，防止 app 退出时触发 crash 逻辑。

## 场景四：设备撤销 (Revoke)

### Mac 端 (go-bridge) 处理

1. 用户在 MacBridge Devices tab 点击 Revoke
2. MacBridge 调用 `DELETE /internal/devices/{deviceId}` (go-bridge management API)
3. go-bridge `management_api.go` 中的 revoke handler：
   - 调用 `FileDeviceStore.RevokeDevice(deviceID)` 从持久化存储中删除设备
   - 调用 `DeviceConnRegistry.DisconnectDevice(deviceID)` 断开该设备的所有活跃 WebSocket 连接
4. `DisconnectDevice` 对每个连接发送 `{type: event, event: device_revoked, message: ...}` 然后设置 `conn.revoked = true`
5. 如果设备不在活跃连接中 (如 iOS 已断开)，下一次该设备连接时，authMiddleware 会检查 token 是否仍有效：
   - token 已被撤销 -> 拒绝连接，返回 HTTP 401
   - iOS 收到 401 -> `isAuthError` 检测 -> 停止重连，post `bridgeDeviceRevoked` 通知

### iOS 端处理

1. transport 收到 `device_revoked` 事件 (JSON: `{type: event, event: device_revoked}`)
2. `handleEvent` 检测到 `device_revoked`：
   - `Task { await disconnect() }` - 主动断开 WebSocket，设置 `isExplicitDisconnect = true` (停止重连)
   - post `bridgeDeviceRevoked` 通知
3. 如果通过 RPC 返回 auth.device_revoked 错误：
   - `handleResult` 检测到 `auth.device_revoked` 错误码
   - 同样呼叫 `disconnect()` 和 post `bridgeDeviceRevoked`
4. `OpenCodeiOSApp.onReceive(.bridgeDeviceRevoked)` 处理：
   - 调用 `store.deleteBridge(id:)` 删除 SavedBridge (移除配对凭证)
   - 调用 `bridgeProvider.removeKnownBridge(id:)` 移除已知 bridge 记录
   - 调用 `bridgeProvider.disconnectBridge()` 断开连接
   - 调用 `serverViewModel.disconnectCurrentServer()` 清空当前 server
5. `disconnectCurrentServer` 将所有 server 的 isActive 设为 false，`currentServer = nil`
6. `ContentView.body` 检测到 `currentServer == nil`，切换显示到 No server connected 页面
7. 用户需要重新扫码配对才能使用

**两条撤销路径：**
- **WebSocket 主动推送** (设备在线时): go-bridge 直接发送 `device_revoked` 事件，iOS 立即断开
- **连接时拒绝** (设备离线后恢复时): transport 重连收到 401 / auth.device_revoked，停止重连并通知上层

## 场景五：Backend Unavailable 错误的根因与修复

错误消息：`Paired MacBridge did not provide backend Claude Code. Reconnect MacBridge and try again.`

### 根因

1. `BridgeProvider.resolveBackendClient` 在 `cachedClients` 为空时返回 `BridgeUnavailableBackendClient`
2. app 冷启动时调用链：
   - `ServerViewModel` init 从 storage 加载 servers，`currentServer` 非 nil
   - `ContentView` 渲染 chat 界面 -> `SessionsView.task { initialize }`
   - `initialize` 调用 `backendClientFactory(config)` -> `resolveBackendClient`
   - 此时 `autoConnectOnLaunch` 还在执行中，`cachedClients` 为空
   - 返回 `BridgeUnavailableBackendClient`
3. bridge 连接成功后 `connectToServer` 激活了 server
4. 但 server config 没变 (host/port/kind 相同)，SessionsView 和 ChatViewModel 的 `onChange(of: serverConfig)` 不触发
5. 两个 ViewModel 永远持有 `BridgeUnavailableBackendClient`

### 修复

引入 `bridgeBackendReady` 通知：
- `connectToServer` bridge 路径成功激活 server 后 post
- `SessionsViewModel` 监听并重新 `initialize` (重建 backend client)
- `ChatViewModel` 监听并调用 `backendClientFactory` 重建 client，重载 models/agents
