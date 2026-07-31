# CCCode Mac Bridge 三期开发实施方案

> 文档性质：工程实施方案（评审修订版）
> 对应产品定义：[CCCode产品化与Mac端Bridge产品设计.md](CCCode产品化与Mac端Bridge产品设计.md)
> 前序方案：[CCCode-Mac-Bridge-产品化技术方案.md](CCCode-Mac-Bridge-产品化技术方案.md)（Phase 0-4 路线图）
> 评审报告：[CCCode-Mac-Bridge-三期开发实施方案-评审报告.md](CCCode-Mac-Bridge-三期开发实施方案-评审报告.md)
> 当前状态：Phase 0-2 已完成，Phase 3 部分完成，Phase 4 未开始
> 生成日期：2026-05-10 | 修订日期：2026-05-10 (R3 · 通过)

---

## 评审修订说明

**R1 修订**：第一轮评审发现基础设施分析有 3 处重要事实错误（P3-1 agent 检测空白、P3-2 离线存储已存在、P3-4 远程 URL 数据通路不存在），工作量从 7-10 天上调至 11.5-16 天。

**R2 修订**：第二轮评审确认 4 处事实错误全部修正，新发现 5 个描述精度问题（NetworkStateManager 状态机已存在、检测函数缺超时、Xcode build phase 缺配置、Mac 命名路径模糊、通知去重 key 过细）。逐一修正，工作量不变。

**R3 评审结论：方案通过，达到可执行状态。** 三轮评审累计 9 个问题全部修正。R3 做了跨项一致性检查（management API 端点、go-bridge flag、SetBridgeIdentity 签名、Xcode project 变更），确认 7 个实施项之间无冲突。工作量 11.5-16 天三轮不变。

---

## 背景

### 已完成（Phase 0-2 + Phase 3 部分）

Phase 0（协议收敛）、Phase 1（Mac App 管理 runtime）、Phase 2（iOS 新配对与连接）已全部完成并通过真机回归验证。Phase 3 的部分能力也已落地：

- Mac sleep/wake 重连（RuntimeManager.observeSleepWake）
- bridgeBackendReady 通知（iOS 冷启动竞态修复）
- 连接失败诊断（3 种场景：Mac 离线/无效地址/bridge 未运行）
- go-bridge pending notification 队列（checkPendingNotifications RPC）
- running session polling（Claude Code 外部 turn 感知）
- CCCodeBridgeTodo.id 修复（OpenCode Todo Dock 正常）

### 差距分析

对照产品定义 v1.0，以下能力尚未实现：

| # | 产品章节 | 能力 | 当前状态 |
|---|---------|------|---------|
| 1 | 7.3 | Mac 端后端管理页 | SettingsView 只有 OpenCode 认证；无后端检测状态 UI |
| 2 | 8.6 | iOS 离线只读快照 | 断连后无任何缓存内容可查看 |
| 3 | 8.7 | 本地推送通知 | go-bridge 有 pending queue，但未接入 UNUserNotificationCenter |
| 4 | 5.4/7.4 | 远程访问配置 | Mac 端无 Remote URL 入口，iOS 无远程连接路径 |
| 5 | 12.5 | 版本兼容提示 | hello_ack 含 runtimeVersion 但 iOS 未检查，无「需要更新」提示 |
| 6 | — | 多 Mac 管理 | SavedBridgeStore 支持多 bridge 存储，但无产品化切换体验 |
| 7 | — | 产品文案 | Mac 端日志和部分状态文本偏技术表述 |

---

## 实施清单

### P3-1：Mac 端后端管理页

**目标**：用户在 Mac App 中看到每个后端的检测状态和修复建议。

#### 现有基础设施

| 组件 | 状态 | 文件位置 |
|------|------|---------|
| Agent 状态枚举 | 已定义 7 种状态（`available`/`not_detected`/`not_logged_in` 等） | `go-bridge/agent_descriptor.go` |
| `BuildAgentDescriptor` | **硬编码 `Status: AgentStatusAvailable`**，从未执行检测 | `agent_descriptor.go:118` |
| `deriveCapabilities` | 已实现，从 `core.Agent` 接口断言推导能力列表 | `agent_descriptor.go:87-120` |
| `/internal/agents` management API | 已实现，返回 `BuildAllAgentDescriptors` 结果 | `management_api.go:93,156-159` |
| RuntimeManager.pollManagementAPI | 已周期性调用此 API | `RuntimeManager.swift:314` |

**评审修正**：原方案声称「go-bridge 启动时通过 `BuildAllAgentDescriptors` 检测 CLI 可用性」——事实上 `BuildAgentDescriptor` 硬编码 `AgentStatusAvailable`，reason 从未被赋值。Agent 检测逻辑完全空白，需要从零实现。

#### 改动范围

**go-bridge 侧：CLI 可用性检测（新增）**

在 `agent_descriptor.go` 中新增检测函数，替代当前的硬编码：

```go
// detectAgentStatus 检测单个 agent 的可用性状态。
func detectAgentStatus(id string) (AgentStatus, string) {
    switch id {
    case "claude":
        return detectClaudeCLI()
    case "opencode":
        return detectOpenCodeService()
    case "codex":
        return detectCodexService()
    default:
        return AgentStatusAvailable, ""
    }
}
```

检测策略（所有检测设置超时，避免阻塞 go-bridge 启动）：
- **Claude Code**：`exec.LookPath("claude")` 查找 CLI，找到后 `exec.CommandContext(ctx, "claude", "--version")` 验证可执行（3 秒超时）。登录状态无法从外部检测，CLI 存在即标记 `available`
- **OpenCode**：`http.Client{Timeout: 5s}` GET `http://localhost:64667/health`（或配置的 URL），200 = `available`，连接拒绝 = `service_not_running`，超时/DNS 失败 = `not_detected`
- **Codex**：WebSocket dial 5 秒超时连接 `ws://localhost:4141`（app-server 模式）或 `exec.CommandContext` + `exec.LookPath("codex")`（exec 模式，3 秒超时）
- 超时结果标记为 `not_detected`，reason 为 "detection timed out" 

`BuildAgentDescriptor` 改为调用 `detectAgentStatus` 设置 Status 和 Reason：

```go
func BuildAgentDescriptor(id string, agent core.Agent, codexBackendMode string) AgentProviderDescriptor {
    status, reason := detectAgentStatus(id)
    return AgentProviderDescriptor{
        // ...
        Status:   status,
        Reason:   reason,
        // ...
    }
}
```

**go-bridge 侧：Management API 增强**

- `/internal/agents/refresh`（新增）：手动触发重新检测，Mac App 刷新按钮调用
- `/internal/agents/{id}/test`（新增）：测试指定后端的连通性

**MacBridge/MacBridge/Views/BackendStatusView.swift**（新建）

后端状态卡片列表页，展示每个检测到的后端：

| 后端 | 可能状态 | 用户文案 |
|------|---------|---------|
| Claude Code | `available` / `not_detected` | 已检测，可用 / 未检测到此工具，请先安装 |
| Codex | `available` / `not_detected` / `service_not_running` | 已检测，可用 / 未检测到此工具 / 服务未运行，请先启动 |
| OpenCode | `available` / `not_detected` / `service_not_running` | 已检测，可用 / 未检测到此工具 / 服务未运行 |
| Copilot | `available` / `not_detected` | 已检测，可用 / 未检测到此工具 |

每个卡片展示：
- 后端名称 + 图标
- 状态标签（绿色已检测 / 黄色需配置）
- reason 文案映射（在 Mac App 端做，go-bridge 保持技术 reason）
- 操作入口：测试连接（调用 `/internal/agents/{id}/test`）、刷新（调用 `/internal/agents/refresh`）

**MacBridge/MacBridge/Views/ContentView.swift**

在现有 tab/导航中增加后端管理入口。将 OpenCode 认证配置从 SettingsView 移入后端管理页的 OpenCode 卡片子功能。

**MacBridge/MacBridge/ViewModels/BackendStatusViewModel.swift**（新建）

管理后端状态列表，调用 ManagementAPIClient。

#### 特殊场景：全后端未检测

当所有后端 status 都不是 `available` 时，展示引导文案（产品定义 7.3）：

> CCCode Bridge 已就绪。下一步，安装或登录一个你想使用的 AI 编程工具。

提供三个入口：推荐入口（选择一个后端开始配置）、说明入口（查看各后端需要准备什么）、跳过入口（暂时只完成 iPhone 配对）。

#### 验证标准

- Mac App 后端管理页正确展示各后端检测状态
- 未安装任何后端时展示引导文案
- 点击刷新按钮触发重新检测，页面更新
- reason 文案不暴露 CLI 路径、PID、端口等技术细节
- go-bridge 启动时日志输出每个 agent 的检测结果

#### 工作量：3-4 天

go-bridge 侧 CLI 检测逻辑 1.5-2 天 + Mac App UI 1-1.5 天 + 测试 0.5 天。

---

### P3-2：iOS 离线只读快照

**目标**：断连后用户能看到最后同步的 session 列表和消息内容。

#### 现有基础设施

| 组件 | 状态 | 文件位置 |
|------|------|---------|
| `SessionSnapshotStore` | **已存在**。Actor，磁盘持久化，schema v2，200MB 目录上限 / 10MB 单文件上限，`provisional`/`authoritative` 状态区分 | `Services/Storage/SessionSnapshotStore.swift` (279 行) |
| `BridgeOfflineSnapshotAdapter` | **已存在**。封装 Bridge 场景下的 snapshot 读写 | `Services/Bridge/BridgeOfflineSnapshotAdapter.swift` (62 行) |
| `SnapshotContainer` | 已实现 `durabilityStatus`、`reconcileFailureReason`、`lastAuthoritativeSyncAt`、`integrityWarnings` | `SessionSnapshotStore.swift:12-60` |
| `MessageCacheManager` | Actor，`[String: [Message]]` 字典（内存级） | — |
| `NetworkStateManager` | 跟踪 WebSocket 连接状态 | — |

**评审修正**：原方案声称需「新建 OfflineSnapshotStore 磁盘持久化」——事实上 `SessionSnapshotStore` + `BridgeOfflineSnapshotAdapter` 已完整实现了磁盘快照系统。P3-2 的实际工作是在已有基础设施上加离线 UI 模式、session 列表快照和草稿逻辑。

#### 改动范围

**扩展 SessionSnapshotStore：session 列表快照**

当前只存消息，不存 session 元数据（标题、backendId、时间戳）。离线时需要展示 session 列表，需新增：

```swift
// SessionSnapshotStore 新增方法
func saveSessionListSnapshot(baseConfig: ServerConfig?, sessions: [BackendThread]) async
func loadSessionListSnapshot(baseConfig: ServerConfig?) async -> [BackendThread]?
```

存储格式：独立的 `session-list-{scopeHash}.json` 文件，与消息快照共享同一目录。容量计入 200MB 总限制。

写入时机：每次 `fetchSessions` 成功后异步写入。

**离线判定逻辑**

`NetworkStateManager` 已有完整 5 态状态机（`connecting` → `online` → `degraded` → `offline`），自带 `reconnectAttempts` 计数器和 `didGoOffline(reason:)` 方法。不需要新增状态。

实际改动：在 `BridgeProvider` 的重连循环中接入现有离线触发：
- 当 `reconnectAttempts >= 3` 或距最后成功连接超过 30 秒时，调用 `NetworkStateManager.shared.didGoOffline(reason:)`
- 不在 WebSocket 断连立即进入离线模式（频繁重连会造成闪烁）
- UI 层已订阅 `NetworkStateManager.state`，进入 `.offline` 后自动展示离线 UI

**离线 UI**

- Session 列表页：从 `SessionSnapshotStore.loadSessionListSnapshot` 读取，顶部显示「离线模式 · 上次同步于 HH:mm」
- 消息页：从 `SessionSnapshotStore.loadSnapshot` 读取，顶部显示「离线模式 · 内容可能已变化」
- 根据 `SnapshotContainer.durabilityStatus` 展示不同提示：
  - `authoritative`：「内容为上次同步时的状态」
  - `provisional`：「内容可能不完整（同步未确认）」
- 输入框隐藏或禁用，提示「重新连接后可发送消息」
- 运行中任务显示「状态可能已变化」，避免把旧 `isGenerating` 状态伪装成实时

**草稿**

- 离线时允许在输入框输入文本，保存为 per-session 草稿（UserDefaults 或 snapshot store）
- 重连后提示：「你有未发送的草稿，是否发送？」
- 如果目标 session 在草稿期间已被其他客户端结束（turn completed），提示「此会话已结束，草稿未发送」，提供复制到新会话或丢弃

**重连后数据 reconciliation**

- 利用 `durabilityStatus` 机制：重连后首次 `loadMessages` 成功时标记快照为 `authoritative`
- 离线快照写入和网络恢复的竞态：如果重连成功时有 pending 的快照写入，优先使用服务端数据

**数据流**

```
在线：
  loadMessages → SessionSnapshotStore 写入快照（authoritative）
  fetchSessions → SessionSnapshotStore 写入 session 列表快照

离线（判定阈值满足后）：
  fetchSessions 失败 → SessionSnapshotStore 读取 session 列表 → 显示「离线模式」
  switchSession → SessionSnapshotStore 读取消息快照 → 显示 durabilityStatus 提示
  输入框 → 草稿存储 → 重连后确认发送
```

#### 验证标准

- 正常在线使用后杀掉 go-bridge → 重连失败 3 次 → iOS 进入离线模式 → 能查看 session 列表和消息
- authoritative 快照显示「上次同步时的状态」，provisional 快照显示「可能不完整」
- 离线模式下输入框禁用，草稿保存
- 重连后草稿提示发送或丢弃
- App 重启后离线快照仍在
- 设置页可以清除离线缓存

#### 工作量：2-3 天

不需要新建存储层（节省约 1 天），但 session 列表快照 + 离线 UI + 草稿 + durabilityStatus 展示 + reconciliation 边界情况处理总量相当。

---

### P3-3：本地推送通知

**目标**：用户不在 App 内时，能收到任务完成、权限确认、错误等关键事件的通知。

#### 现有基础设施

| 组件 | 状态 | 文件位置 |
|------|------|---------|
| go-bridge `PendingNotificationStore` | 已实现 per-device 排队 | `go-bridge/types.go:438` |
| iOS `checkPendingNotifications` RPC | 已接入 | `CCCodeBridgeClient.swift:184` |
| iOS 回前台拉取 | ChatViewModel 已调用 | — |

#### 改动范围

**通知权限请求**

- 时机：首次收到 `turn_completed` 事件时（而非首次配对成功后，评审建议采纳——上下文更自然）
- 调用 `UNUserNotificationCenter.requestAuthorization(options: [.alert, .sound, .badge])`
- 用户拒绝后不阻塞使用
- 后续不再重复请求

**通知触发**

iOS 端实时事件流收到以下事件时，发送本地推送通知：

| 事件 | 通知内容 | 优先级 |
|------|---------|--------|
| `turn_completed` | 「任务已完成」+ session 标题 | 普通 |
| `permission_request` | 「需要确认操作」+ 工具名 | 高优先级 |
| `error` | 「任务出错」+ 错误摘要 | 普通 |

实现方式：
- `ChatViewModel+CodexStreaming.swift` 的事件处理路径中，在收到上述事件时调用 `UNUserNotificationCenter.add(request)`
- 通知 payload 包含 `sessionId`、`backendId`，用于点击跳转

**通知去重（评审补充）**

如果 iOS 实时收到了 `turn_completed` 事件并展示了前台横幅，回前台时 `checkPendingNotifications` 可能返回同一条已完成 turn。需要基于 `sessionId + eventType` 做去重（不包含 timestamp——实时事件流和 pending notification 拉取的 timestamp 可能有纳秒级差异）：

```swift
// 发送通知前检查去重
private var recentlyNotifiedEvents: Set<String> = []  // "sessionId:eventType"

func shouldSendNotification(sessionId: String, eventType: String) -> Bool {
    let key = "\(sessionId):\(eventType)"
    guard !recentlyNotifiedEvents.contains(key) else { return false }
    recentlyNotifiedEvents.insert(key)
    // 保留最近 100 条记录，防止内存泄漏
    if recentlyNotifiedEvents.count > 100 {
        recentlyNotifiedEvents = Set(recentlyNotifiedEvents.suffix(50))
    }
    return true
}

// session 切换时清除对应去重记录，允许同一 session 的后续事件再次通知
func clearNotificationDedup(for sessionId: String) {
    recentlyNotifiedEvents = recentlyNotifiedEvents.filter { !$0.hasPrefix("\(sessionId):") }
}
```

**通知聚合（评审补充）**

按 `threadIdentifier = backendId` 分组。短时间内多个 session 完成时，iOS 自动聚合同一 backend 的通知。

**通知点击跳转**

- 注册 `UNNotificationResponse` 处理
- 有 sessionId 时 → 打开对应 session
- 有 permission request 时 → 打开对应 session 并滚动到确认卡片
- 无具体目标时 → 打开 session 列表

**前台通知**

- App 在前台时使用 `UNUserNotificationCenterDelegate.willPresent` 展示横幅

**App 回前台拉取**

保持现有 `checkPendingNotifications` 调用不变。

#### 验证标准

- 首次收到 turn_completed 事件时弹出通知权限请求
- 权限授予后，任务完成时收到本地通知
- 权限确认请求时收到高优先级通知
- 同一事件不产生重复通知
- 点击通知跳转到对应 session
- 用户拒绝通知权限后 App 功能不受影响
- App 在前台时收到横幅通知

#### 工作量：1-1.5 天

补充去重和聚合逻辑增加 0.5 天。

---

### P3-4：远程访问配置

**目标**：Mac 端支持配置远程 URL，iOS 端支持通过远程地址连接已授权 Mac。

#### 现有基础设施

| 组件 | 状态 | 文件位置 |
|------|------|---------|
| go-bridge `HelloURLs.Remote` | **字段已定义但始终为空**，HandleHello 只设 Local 不设 Remote | `hello_handler.go:55,88-90` |
| go-bridge `-remote-url` flag | **不存在**，需要新增 | `main.go` |
| Mac App `RuntimeConfig` | **没有 remoteURL 字段**，需要新增 | `RuntimeManager.swift:21` |
| iOS `SavedBridge.remoteURL` | **已存在** | `CCCodeBridgeModels.swift:227` |
| iOS `CCCodeHelloAckURLs.remote` | **已存在** | `CCCodeBridgeModels.swift:38` |
| iOS `BridgeRemoteSecurityNotice` | **已存在**，首次使用新 remote URL 需用户确认安全 | `BridgeRemoteSecurityNotice.swift` |

**评审修正**：原方案声称 go-bridge 已有 `-remote-url` flag 且 Mac App 启动时传递远程地址——事实上整条数据通路（Mac App → go-bridge flag → hello_ack → iOS）尚不存在，需要从零搭建。

#### 改动范围

**go-bridge 侧（从零搭建）**

1. 新增 `-remote-url` flag（`main.go`）：
   ```go
   remoteURL := flag.String("remote-url", "", "外部可达的 Bridge WebSocket URL（如 ws://my-tailscale:8777/bridge）")
   ```

2. `SetBridgeIdentity` 增加 `remoteURL` 参数，传递到 Server：
   ```go
   func (s *Server) SetBridgeIdentity(bridgeID, displayName, runtimeVersion, localURL, remoteURL string) {
       // ...
       s.remoteURL = remoteURL
   }
   ```

3. `HandleHello` 中填充 `HelloURLs.Remote`：
   ```go
   CurrentURLs: HelloURLs{
       Local:  localURL,
       Remote: remoteURL,  // 从空字符串变为实际值
   },
   ```

**Mac App 侧**

1. `RuntimeConfig` 新增 `remoteURL` 字段

2. SettingsView 增加「Remote Access」区块：
   - 模式选择：仅本地网络（默认） / 自带隧道地址
   - URL 输入框
   - 测试连接按钮
   - 保存后 go-bridge 自动重启（通过 RuntimeManager.restart + 新 flag）

3. 安全提示（首次启用时）：
   - 「远程访问会扩大你的 Mac 暴露面」
   - 「确保你信任使用的网络通道」
   - 「建议使用 Tailscale、WireGuard 或 Cloudflare Tunnel」

**iOS 侧（已有基础设施，补充连接逻辑）**

- `SavedBridgeStore` 保存 hello_ack 中的 remoteURL（写入逻辑可能需补充）
- 当本地连接失败时，如果该 bridge 有 remoteURL，自动尝试远程连接
- 连接成功后顶部显示：「通过远程地址连接 · 延迟可能较高」
- `BridgeRemoteSecurityNotice` 已处理首次远程连接的安全确认

**安全考量**

- 远程连接应**强制 `wss://`**（评审补充）。`ws://` 在远程场景下 device token 明文传输不可接受。iOS 端 `BridgeProvider.connectBridge` 遇到远程 `ws://`（非 localhost/局域网 IP）应拒绝连接并提示使用 `wss://`
- 设备 token 验证不受网络来源影响（Bearer token 在 hello 时传递）
- 不提供自动端口映射、VPS 购买、relay 配置

#### 验证标准

- Mac 端填入远程 URL → go-bridge 重启 → hello_ack 的 currentURLs.remote 有值
- iOS 本地不可达时自动尝试远程 URL → 连接成功
- 远程连接后 iOS 显示「通过远程地址连接」提示
- 远程连接走同一 device auth
- 远程场景强制 wss://，ws:// 被拒绝

#### 工作量：3-4 天

go-bridge 数据通路 1-1.5 天 + Mac App UI 1-1.5 天 + iOS 连接逻辑 0.5 天 + wss:// 验证 0.5 天。

---

### P3-5：版本兼容提示

**目标**：iOS 和 Mac 版本不一致时给用户明确的升级提示。

#### 现有基础设施

| 组件 | 状态 | 文件位置 |
|------|------|---------|
| go-bridge `HandleHello` 协议版本校验 | 已实现，不匹配返回 `protocol.unsupported_version` | `hello_handler.go:72` |
| hello_ack `bridge.runtimeVersion` | 已返回，当前为 `"0.1.0-dev"` | `runtime_version.go:8` |
| hello_ack `bridge.protocol` | 已返回 `{name, version, schemaRevision}` | `hello_handler.go:77-81` |
| iOS `CCCodeHelloAckMessage` | 已解析 `bridge.runtimeVersion` 和 `protocol` | `CCCodeBridgeModels.swift:42-50` |

#### 改动范围

**iOS 端版本检查**

`BridgeProvider.connectBridge` 收到 hello_ack 后：

1. `ack.ok == false && ack.error?.code == "protocol.unsupported_version"`
   - 弹出提示：「需要更新 CCCode Bridge 才能连接此 iPhone」
   - 提供 Mac 端下载链接或更新说明

2. 协议版本匹配但 runtimeVersion 过旧（定义最低支持版本常量）
   - 非阻塞警告：「CCCode Bridge 版本较旧，部分功能可能不可用」
   - 建议更新但不阻塞连接

**go-bridge build 时注入 git commit（评审补充）**

当前 `runtimeVersion` 为硬编码 `"0.1.0-dev"`，没有实际参考价值。改为 build 时注入：

```go
// runtime_version.go
// -ldflags "-X main.runtimeVersion=$(git describe --tags --always --dirty)"
var runtimeVersion = "0.1.0-dev (dev build)"
```

Mac App 的 Xcode build phase 新增 script phase：
1. 编译 go-bridge 前执行 `git describe --tags --always --dirty` 获取版本字符串
2. 通过 `-ldflags "-X main.runtimeVersion=..."` 传入 `go build`
3. 编译产物 `cccode-bridge-runtime` 内嵌版本信息

**采用协议版本整数作为唯一兼容判断依据**（原方案推荐，评审确认）。不引入语义版本比较的复杂度。

#### 验证标准

- 协议版本不匹配时 iOS 显示「需要更新 CCCode Bridge」
- 协议版本匹配但 runtime 过旧时 iOS 显示非阻塞警告
- runtimeVersion 包含 git commit 信息

#### 工作量：0.5-1 天

补充 git commit 注入增加 0.5 天。

---

### P3-6：多 Mac 管理产品化

**目标**：iOS 端支持保存和切换多个 Mac Bridge 连接。

#### 现有基础设施

| 组件 | 状态 | 文件位置 |
|------|------|---------|
| `SavedBridgeStore` | 已支持多 bridge 存储，`saveBridge`/`deleteBridge` | `SavedBridgeStore.swift:61,94` |
| `SavedBridge` | 已有 `id`/`displayName`/`localURL`/`remoteURL`/`lastConnectedAt` | `CCCodeBridgeModels.swift:223-230` |
| `BridgeProvider.autoConnectOnLaunch` | 自动连接最近使用的 bridge | `BridgeProvider.swift` |
| go-bridge displayName | **硬编码为 "CCCode Bridge"**（`main.go`），多 Mac 无法区分 | `main.go:170` |

#### 改动范围

**Mac 端：自定义 Bridge 显示名称**

- go-bridge 首次启动时自动检测 `hostname`，存入 data dir 作为默认 displayName（不新增 flag）
- Mac App 通过 management API `/internal/settings/display-name`（新增端点）读取和修改名称
- 修改后立即生效（hello_ack 后续返回新名称），无需重启 go-bridge
- Mac App SettingsView 增加名称编辑入口
- iOS 端 `SavedBridge.displayName` 在每次 hello_ack 时更新

**iOS 端连接管理页**

在设置或 sidebar 增加「已连接的 Mac」管理页：

- 已保存的 Mac 列表（名称、最近连接时间、当前状态、连接地址）
- 点击切换到对应 Mac
- 左滑删除（只删 iOS 本地凭证，不撤销 Mac 端授权）
- 当前连接的 Mac 高亮显示

**切换体验**

- 切换 Mac 时断开当前连接 → 连接新 Mac → 刷新 session 列表
- 切换过程中显示 loading 状态
- 切换失败时回到原连接

**多 Mac 连接策略**

- 只维持当前活跃的一台 Mac 连接，不同时连接多台（评审补充）
- 切换时如果当前 Mac 有正在执行的 turn，弹出确认：「当前有任务正在执行，切换后任务将继续在 Mac 上运行」
- 不中断远端任务，只是 iOS 端不再接收实时事件

#### 验证标准

- 保存两个 Mac Bridge → 能在列表中看到 → 能切换
- 切换后 session 列表正确刷新
- 切换时如有 running session 弹出确认
- 删除只影响 iOS 本地
- 不同 Mac 有不同的显示名称

#### 工作量：1-2 天

补充 Mac 命名 + 连接策略定义 + running session 切换处理。

---

### P3-7：产品文案优化

**目标**：Mac 端 UI 文案面向普通用户，不暴露内部工程术语。

#### 改动范围

**MacBridge 全局文案审计**

逐屏检查并替换：

| 当前文案 | 替换为 |
|---------|--------|
| 「Bridge 运行中」 | 「CCCode Bridge 运行中」 |
| 「Bridge 已停止」 | 「CCCode Bridge 已停止」 |
| 「go-bridge 已启动」 | 「Bridge 服务已启动」 |
| 「go-bridge 意外退出」 | 「Bridge 服务意外退出」 |
| 「PID=12345」 | 不在 UI 显示 |
| 「端口 8777」 | 不在主界面显示（高级诊断可见） |
| 「agent」 | 「AI 工具」或具体名称 |
| 「driver」 | 不在 UI 显示 |

**错误消息产品化**

- RuntimeManager 内部做文案映射（评审补充），而非分散在 View 层
- 确保所有错误路径都经过映射
- 技术日志保留在 NSLog，不暴露给 UI

**iOS 端诊断页**

连接失败诊断信息的产品化表述微调。

**国际化准备（评审补充）**

文案映射使用 `String(localized:)` 或 `NSLocalizedString`，即使当前只支持英文/中文。为后续多语言做准备。

#### 验证标准

- Mac App 主界面不出现 go-bridge、PID、端口、driver、WebSocket 等术语
- 错误提示说人话
- 高级诊断页保留技术细节
- 文案通过 String(localized:) 管理

#### 工作量：0.5 天

不变。

---

## 优先级与依赖关系

```
P3-7 产品文案优化 ───────── 独立，随时可做
P3-5 版本兼容提示 ───────── 独立，改动小
P3-3 本地推送通知 ───────── 独立，核心场景价值高
P3-1 后端管理页 ──────────── 独立，go-bridge 检测 + Mac UI
P3-2 离线只读快照 ────────── 独立，工程量最大
P3-4 远程访问配置 ────────── 数据通路从零搭建
P3-6 多 Mac 管理 ─────────── 依赖 P3-4（remoteURL 体系）+ P3-1（Mac 命名）
```

建议实施顺序：

1. **P3-7 产品文案**（改动小、影响广、无风险）
2. **P3-5 版本兼容**（改动小，利用已有协议版本字段）
3. **P3-3 本地推送通知**（核心场景价值高）
4. **P3-1 后端管理页**（go-bridge CLI 检测 + Mac UI）
5. **P3-2 离线只读快照**（工程量最大，利用已有 SessionSnapshotStore）
6. **P3-4 远程访问**（数据通路从零搭建）
7. **P3-6 多 Mac 管理**（依赖 P3-4 和 P3-1）

P3-1、P3-2、P3-3 三者无依赖关系，可以并行。

---

## 工作量估计

| 编号 | 任务 | 估计 | 说明 |
|------|------|------|------|
| P3-1 | Mac 端后端管理页 | **3-4 天** | go-bridge CLI 检测 1.5-2 天 + Mac UI 1-1.5 天 + 测试 0.5 天 |
| P3-2 | iOS 离线只读快照 | **2-3 天** | 利用已有 SessionSnapshotStore，session 列表快照 + 离线 UI + 草稿 + 边界情况 |
| P3-3 | 本地推送通知 | **1-1.5 天** | UNUserNotificationCenter + 去重 + 聚合 |
| P3-4 | 远程访问配置 | **3-4 天** | go-bridge flag + hello_ack + Mac UI + iOS 回退 + wss:// 验证 |
| P3-5 | 版本兼容提示 | **0.5-1 天** | iOS 端检查 + go-bridge git commit 注入 |
| P3-6 | 多 Mac 管理 | **1-2 天** | Mac 命名 + 连接策略 + running session 切换处理 |
| P3-7 | 产品文案优化 | **0.5 天** | 全局文案审计和替换 |

**总计：11.5-16 个工作日**

---

## 测试策略（评审补充）

| 编号 | 单元测试 | 集成测试 | 真机回归 |
|------|---------|---------|---------|
| P3-1 | go-bridge `detectAgentStatus` 各分支测试 | Mac App → go-bridge `/internal/agents` 端到端 | 多后端环境验证检测准确性 |
| P3-2 | 离线判定阈值、草稿保存/恢复、durabilityStatus 读取 | SessionSnapshotStore 读写 + 离线 UI 触发 | 杀 go-bridge → 离线查看 → 重连恢复 |
| P3-3 | 通知去重逻辑 | 前台/后台通知展示 | 真机通知权限 + 点击跳转 |
| P3-4 | go-bridge remote-url flag 解析 + hello_ack 填充 | Mac App → go-bridge → iOS 端到端 | Tailscale/局域网双环境验证 |
| P3-5 | 协议版本匹配/不匹配分支 | hello 握手版本协商 | — |
| P3-6 | 切换流程（断开 → 连接 → 刷新） | 多 Mac 列表管理 | 双 Mac 环境切换 |
| P3-7 | 文案映射覆盖 | — | 逐屏目视检查 |

---

## 回滚方案（评审补充）

| 编号 | 回滚策略 |
|------|---------|
| P3-1 | go-bridge 检测失败时 fallback 到 `AgentStatusAvailable`（当前行为），Mac App 不展示检测失败卡片 |
| P3-2 | 离线 UI 通过 feature flag 控制，回滚后恢复当前行为（断连即空白） |
| P3-3 | 通知功能完全增量，关闭权限即回滚，无数据影响 |
| P3-4 | Mac App 不填 remote URL = 回滚到仅本地模式，go-bridge flag 默认空 |
| P3-5 | 版本检查失败不影响连接（非阻塞），可静默关闭 |
| P3-6 | 多 Mac 列表只读展示，切换功能可通过 feature flag 关闭 |
| P3-7 | 文案替换是纯 UI 变更，git revert 即可 |

---

## 风险

| 风险 | 严重程度 | 缓解措施 |
|------|---------|---------|
| 离线快照与实时消息的 reconciliation 边界情况（快照写入和网络恢复的竞态） | 中 | 利用 `durabilityStatus` 机制，重连后首次 loadMessages 成功时标记快照为 authoritative |
| Agent 检测依赖 CLI 环境（PATH、安装路径），不同用户环境差异大 | 中 | 提供手动刷新检测按钮；检测失败时 fallback 到 `available` + 日志记录 |
| 远程访问的 TLS 证书验证在自签名/内网 CA 场景下会失败 | 低 | 文档说明仅支持公共 CA 签发的证书；自签名场景提示用户配置信任 |
| 多 Mac 场景下 bridgeId 重新生成后旧 token 成为孤儿数据 | 低 | 删除 Mac 操作时清理 Keychain（已实现） |
| 本地通知在后台被系统静音 | 低 | 前台通知兜底 + checkPendingNotifications 回前台拉取 |

---

## 评审建议采纳情况

### 已采纳

| 评审建议 | 对应改动 |
|---------|---------|
| P3-1 agent 检测逻辑空白，工作量上调至 3-4 天 | 修正基础设施分析，新增 go-bridge CLI 检测子任务 |
| P3-2 SessionSnapshotStore 已存在，不需要新建 | 删除「新建 OfflineSnapshotStore」，改为扩展已有存储 |
| P3-2 离线判定需要阈值 | 新增「重连失败 3 次或 30 秒后进入离线模式」 |
| P3-2 provisional vs authoritative 快照的 UI 差异 | 新增 durabilityStatus 展示逻辑 |
| P3-2 草稿在 session 已结束时的处理 | 新增「会话已结束，草稿未发送」处理策略 |
| P3-3 通知去重 | 新增 recentlyNotifiedEvents 去重机制 |
| P3-3 通知聚合 | 新增 threadIdentifier = backendId 分组 |
| P3-3 通知权限时机改为「首次收到 turn_completed 事件时」 | 采纳，更自然的上下文 |
| P3-4 remote-url flag 不存在，工作量上调至 3-4 天 | 修正基础设施分析，详细列举需新增的组件 |
| P3-4 远程模式强制 wss:// | 新增安全考量 |
| P3-5 go-bridge build 时注入 git commit | 新增子任务 |
| P3-6 Mac 显示名称硬编码为 "CCCode Bridge" | 新增自定义显示名称子任务 |
| P3-6 多 Mac 连接策略 | 定义只连当前活跃的一台 |
| P3-6 切换时 running session 处理 | 新增确认提示 |
| P3-7 文案映射在 RuntimeManager 内部完成 | 采纳 |
| P3-7 使用 String(localized:) 做国际化准备 | 采纳 |
| 新增测试策略 | 新增「测试策略」章节 |
| 新增回滚方案 | 新增「回滚方案」章节 |
| 依赖关系补充（P3-6 依赖 P3-4 和 P3-1） | 更新依赖关系图 |
| R2: P3-2 NetworkStateManager 已有完整状态机 | 修正为接入现有 didGoOffline 触发 |
| R2: P3-1 检测函数加超时机制 | 所有网络检测 5s 超时，CLI 执行 3s 超时 |
| R2: P3-5 Xcode build phase 需新增 script | 补充 script phase 具体步骤 |
| R2: P3-6 Mac 命名明确选 management API | 新增 /internal/settings/display-name 端点 |
| R2: P3-3 去重 key 去掉 timestamp | 改为 sessionId:eventType |

### 不采纳

| 评审建议 | 不采纳原因 |
|---------|----------|
| R1: P3-4 远程模式增加证书 pinning | 第一版用户群体是高级开发者自用，Tailscale/Cloudflare Tunnel 已经提供了加密通道。在 tunnel 之上再做 pinning 增加了配置复杂度，收益不明显。如果未来做官方 relay 再评估 |
| R1: P3-4 go-bridge 检测 remote URL 可达性并提示 | go-bridge 启动时检测可达性会引入额外的网络 I/O 和启动延迟。远程 URL 的可达性取决于用户网络环境，启动时不可达不代表运行中不可达。Mac App 的「测试连接」按钮已覆盖此需求 |
| R2: 建议在去重 key 中保留 timestamp（第二轮评审未明确建议此方向，但为完整性说明） | 保留 sessionId:eventType 已经足够——同一 session 的同一事件类型不应重复通知。加入 timestamp 会导致实时流和 pending queue 的同一事件因纳秒差异被误判为不同事件，反而引入 bug |
