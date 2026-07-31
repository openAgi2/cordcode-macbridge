# BridgeConnectionCoordinator 最小迁移计划

> 日期：2026-05-16  
> 来源：`docs/2026-05-16-长连接稳定性优化报告.md` Task 5  
> 状态：patch plan。当前轮次只建立迁移边界；尚未做结构性迁移。

## 触发条件

进入结构性迁移前，必须先用 Task 1 telemetry 证明仍存在多 owner 竞争，例如：

- 同一次配对或前台恢复中出现多个 active transport 创建。
- 同一 bridge 同时存在多轮 probe。
- 旧 transport 的 receive/ping/reconnect 回调在新 active transport 建立后继续驱动 provider 状态。

如果没有这类日志证据，本计划保持为设计输入，不直接重写连接生命周期。

## 迁移目标

第一阶段只接管连接创建和销毁：

- 同一时刻最多一个 active transport。
- 同一时刻最多一轮 probe。
- 所有旧 generation 回调被丢弃并记录日志。

第一阶段不同时修改：

- pairing protocol。
- local/remote 候选 URL 策略。
- UI 文案。
- `CCCodeBridgeTransport` 的 `.unlimited` 内部重连。

`.unlimited` 只有在 coordinator 完整接管 `.bridgeTransportDidReconnect` 语义后才能移除。

## Intent 映射

| 现有入口 | Coordinator intent | 第一阶段行为 |
|---|---|---|
| `connectBridge(_:mode:)` | `connect(bridge:mode:reason:)` | 分配新 generation，取消旧 probe，创建候选 transport。 |
| `disconnectBridge()` | `disconnect(reason:)` | 递增 generation，取消 active/probe/reconnect 任务，关闭 active transport。 |
| `autoConnectOnLaunch()` | `connectSavedBridge(reason:autoLaunch)` | pairing guard 为 true 时跳过；否则选择最近 bridge 后投递 connect。 |
| foreground recovery | `recoverIfNeeded(reason:foreground)` | 仅在无 active bridge 或 active transport 不健康时投递 connect。 |
| missing backend recovery | `recoverIfNeeded(reason:missingBackend)` | 健康 transport 只 coalesce `bridgeBackendReady`；不健康才投递 connect。 |
| transport callback | `transportEvent(generation:event:)` | generation 不匹配时丢弃并记录旧回调。 |

## 第一阶段结构

建议新增 `@MainActor final class BridgeConnectionCoordinator`，由 `BridgeProvider` 持有。Provider 仍负责公开现有 API 和发布 UI 状态，Coordinator 负责串行化连接生命周期。

核心状态：

```swift
private var generation: UInt64 = 0
private var active: CCCodeBridgeTransport?
private var activeBridge: SavedBridge?
private var probeTasks: [Task<Void, Never>] = []
private var connectTask: Task<Void, Never>?
```

关键规则：

- 每次 connect/disconnect 先递增 `generation`。
- 所有异步回调捕获 generation，回到 MainActor 后先比较；不匹配则只打日志并返回。
- probe transport 永远使用 `.none` reconnect policy。
- 胜出的 active transport 才使用 `.unlimited` reconnect policy。
- disconnect 必须关闭 active transport 并取消所有 probe/connect task。

## `.bridgeTransportDidReconnect` 接管条件

移除 transport 内部 `.unlimited` 前，coordinator 必须覆盖现有 `BridgeProvider.handleTransportReconnect` 的三类行为：

- 根据新的 `hello_ack` 重建 `cachedClients`。
- 发送 `.bridgeDidConnect` 通知。
- 正确处理 `bridgeBackendsChanged`，让新增/移除 backend 的 UI 与 client cache 保持一致。

在这些行为没有对应测试前，不移除 transport 内部重连。

## 验证计划

第一阶段代码迁移完成后，补以下单元测试：

- connect 时旧 generation 的成功回调不会覆盖新 active transport。
- disconnect 后旧 transport callback 不会重新发布 online。
- 同一轮 connect 只保留一个 active transport。
- 同一时刻只有一组 probe task。
- transport reconnect 后仍重建 `cachedClients` 并发送 `.bridgeDidConnect`。
- backend 列表变化时仍发送 `.bridgeBackendsChanged`。

允许的验证命令：

- `xcodebuild -project OpenCodeiOS/CCCode.xcodeproj -scheme CCCode -destination 'generic/platform=iOS' build`
- `xcodebuild -project OpenCodeiOS/CCCode.xcodeproj -scheme CCCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' test -only-testing:CCCodeTests/<CoordinatorTests>`
- `cd go-bridge && go test ./... -count=1`

不经用户确认，不运行 UI tests、snapshot tests、simulator automation 或真机自动化。

## 当前结论

本轮 Task 1-4 已先完成 telemetry、UI 状态 debounce、`bridgeBackendReady` coalesce 和 pairing/foreground intent guard。由于当前没有新的 telemetry 证明仍存在多 owner 竞争，本轮不进入 coordinator 代码迁移；后续应先采集日志，再按本计划实施第一阶段。
