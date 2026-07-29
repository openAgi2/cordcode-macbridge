# Bridge 配对到连接打通方案

## 目标

将已有的 Bridge 配对基础设施（SavedBridgeStore、BridgePairingClient、BridgePairingView、BridgeProvider、CCCodeBridgeTransport）接线为完整可用链路：
配对成功 → 自动连接 Bridge → Server 列表同步 → 用户可直接使用。

## 当前状态

### 已完成（不重复实现）
- BridgeProvider：管理连接状态、缓存 BackendClient
- BridgePairingClient：QR 解析、WebSocket claim、等待 Mac 批准
- BridgePairingView：配对 UI（idle/connecting/claiming/waiting/completed/failed）
- BridgePairingService：Mac 端 management API 配对服务
- BridgeClientFactory：SavedBridge → Transport → BackendClient 组装
- SavedBridgeStore：持久化 + Keychain token
- CCCodeBridgeTransport：WebSocket 传输层，认证头注入，hello 握手，自动重连
- CCCodeBridgeBackendClient：BackendClient 协议实现
- CCCodeBridgeConnectionState：连接状态管理
- BridgeMigrationDetector：迁移检测（有旧 server 无配对 bridge → 需迁移）
- BridgeConnectionIndicator：连接状态指示器 UI
- BridgeConnectionModeSection：本地/远程切换 UI
- go-bridge pairing_handler.go：配对 WebSocket 端点 + Mac 批准
- go-bridge pairing_session.go：配对状态机（Created→Claimed→Approved→Completed）
- ContentView resolveBackendClient：Bridge 缓存优先，legacy fallback

### 未接线（本次实现）
- 配对成功后无代码调用 BridgeProvider.connectBridge()
- App 启动时无自动重连已配对 Bridge
- Bridge 连接成功后无代码同步 backends 到 ServerViewModel

## Phase 1: 配对成功 → 自动连接

### 改动文件
- `OpenCodeiOS/OpenCodeiOS/App/OpenCodeiOSApp.swift`
  - 添加 `.onChange(of: pairingClient.state)` 监听 `.completed(savedBridge)`
  - 调用 `bridgeProvider.connectBridge(savedBridge)`
  - 连接成功后调用 `pairingClient.reset()` 关闭 sheet

### 验收标准
- 扫 QR 配对 → Mac 批准 → iOS 自动连接 Bridge → connectionStatus 变 .online
- 配对 sheet 自动关闭

## Phase 2: 启动自动重连

### 改动文件
- `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift`
  - 添加 `autoConnectOnLaunch()` 方法
  - 从 SavedBridgeStore 加载所有已配对 Bridge
  - 选择 lastConnectedAt 最新的、有 device token 的 Bridge
  - 调用 connectBridge()，失败静默降级
- `OpenCodeiOS/OpenCodeiOS/App/OpenCodeiOSApp.swift`
  - `.task` 中在 runLaunchAutomationIfNeeded 之前调用 autoConnectOnLaunch

### 验收标准
- 杀掉 App 重新打开 → 自动连接上次使用的 Bridge
- 无已配对 Bridge 时不报错，静默跳过
- 连接失败不阻断 App 正常使用

## Phase 3: Bridge 连接 → Server 列表同步

### 改动文件
- `OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift`
  - connectBridge 成功后发送 Notification.Name.bridgeDidConnect
  - userInfo 包含 "bridge" (SavedBridge) 和 "backends" ([CCCodeAgentDescriptor])
- `OpenCodeiOS/OpenCodeiOS/ViewModels/ServerViewModel.swift`
  - 将 upsertBridgeTargets 从 private 改为 internal
- `OpenCodeiOS/OpenCodeiOS/App/OpenCodeiOSApp.swift`
  - 添加 `.onReceive(NotificationCenter.default.publisher(for: .bridgeDidConnect))`
  - 从 notification 解析 backends 和 bridge URL
  - 将 backends 映射为 (BridgeBackendInfo, BackendKind) 列表
  - 调用 serverViewModel.upsertBridgeTargets 同步到 server 列表
  - 若当前无活跃 server，自动连接第一个 backend target

### 验收标准
- Bridge 连接成功后 server 列表自动出现对应 backend 条目
- 手动配对和启动自动重连都触发同步
- 若无活跃 server，自动选中第一个

## Phase 4: 构建验证 + 测试

### 改动
- xcodebuild build 确保 0 error
- xcodebuild test 确保 0 failure
- go test 确保 pairing 相关测试通过

### 验收标准
- BUILD SUCCEEDED
- 488+ tests passed, 0 failures
- go-bridge pairing tests passed

## Phase 5: 提交

- git commit 所有改动
- commit message 包含每个 Phase 的摘要
