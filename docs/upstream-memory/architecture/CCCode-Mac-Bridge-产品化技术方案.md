# CCCode Mac Bridge 产品化技术方案

> 版本：v0
> 日期：2026-05-07
> 前置文档：
> - 《CCCode 产品化与 Mac 端 Bridge 产品设计》
> - 《CCCode 产品定义 v1.0 决策确认记录》
> - 《CCCode 低保真体验原型 v0.1》
> - 《CCCode 视觉设计 Brief v0》
> - 《CCCode 高保真视觉稿 v0.1》

## 1. 技术方案结论

CCCode 产品化第一版应交付两个用户可理解的产品：

1. **CCCode for iOS**：用户在手机上配对 Mac、查看 Workspace 和 Session、旁观或继续操作 agent。
2. **CCCode Bridge for macOS**：用户下载并打开的 Mac App，内置完整 go-bridge runtime，提供图形化配对、agent 检测、远程访问配置和诊断能力。

技术上第一版不再把现有 `go-bridge` CLI、定制 `cc-connect`、iOS 手动服务器配置当成用户需要理解的三个部分。**go-bridge 是唯一 backend/runtime 基础**，定制 `cc-connect` 必须被收敛进 go-bridge 的可发布工程，并随 CCCode Bridge for macOS 一起交付；iOS 连接层则迁移到围绕 go-bridge 的 CCCode Bridge v1 命名。

```text
CCCode Bridge.app
  ├── macOS SwiftUI / menu bar 管理壳
  ├── embedded go-bridge runtime 可执行文件
  ├── agent runtime / provider adapters
  ├── pairing / device auth / diagnostics
  └── local config / logs / trusted devices

CCCode iOS app
  ├── Bridge pairing
  ├── Saved Mac store
  ├── CCCodeBridgeClient
  ├── offline snapshots
  └── session / workspace UI
```

最重要的架构决定是：**新产品链路定义为 go-bridge 承载的 CCCode Bridge v1；不恢复、不兼容扩展旧 Node.js UnifiedBridge 服务。**

同时冻结一条交付硬约束：**CCCode Bridge for macOS 必须包含 go-bridge 访问层和定制 cc-connect 组件。用户下载并打开 Mac Desktop App 后，就拥有完整 Bridge 能力，不需要再下载、编译、配置 cc-connect、go-bridge CLI 或其他额外 Bridge 服务。**

## 2. 前置更正：go-bridge 是唯一 backend

当前产品化方向里，**go-bridge 是唯一 backend**。已经废弃的是早期 Node.js `UnifiedBridge` 服务；iOS 代码里仍存在大量 `UnifiedBridgeTransport`、`UnifiedBridgeAdapter`、`UnifiedBridgeModels`、`thinBridge`、`unified-bridge` 等命名，这是因为 backend 切换到 go-bridge 后尚未完成重命名。它们不代表当前功能废弃，也不代表仍有一个 Node.js UnifiedBridge backend 需要维护。

因此技术方案里的判断应按下面这条线收敛：

```text
iOS CCCode
  -> CCCode Bridge v1 client
  -> go-bridge runtime
  -> embedded cc-connect agent runtime
  -> Claude Code / OpenCode / Codex / Copilot
```

本方案对旧链路的定位如下：

| 对象 | 新定位 |
|---|---|
| Node.js `UnifiedBridge` 服务 | 已废弃，不进入产品化方案 |
| `go-bridge` | 唯一 backend/runtime 基础 |
| iOS `UnifiedBridgeTransport` | 当前仍可承载 go-bridge 连接的历史命名代码；功能不因命名废弃，产品化时迁移命名与协议模型 |
| iOS `UnifiedBridgeAdapter` | 当前仍可承载 go-bridge `BackendClient` 适配的历史命名代码；短期可继续作为能力映射参考，长期替换或重命名为 go-bridge/CCCode Bridge v1 适配层 |
| iOS `UnifiedBridgeModels` | 当前 go-bridge wire schema 的历史命名载体；新 schema 不再沿用 `UnifiedBridge` 命名 |
| `thinBridge` / `unified-bridge` 命名 | 产品化后迁移为 `cccode-bridge` / `goBridge` 语义 |
| `docs/unified-bridge-protocol.md` | 历史协议文档，不再作为新协议权威来源 |

新实现不是再造一个 go-bridge 之外的新 backend，而是给 iOS 建立新的命名与模块边界，例如：

```text
OpenCodeiOS/OpenCodeiOS/Services/Bridge/
  ├── CCCodeBridgeClient.swift
  ├── CCCodeBridgeTransport.swift
  ├── CCCodeBridgeModels.swift
  ├── BridgePairingService.swift
  ├── SavedBridgeStore.swift
  ├── BridgeCredentialStore.swift
  └── BridgeOfflineSnapshotStore.swift
```

`BackendClient` 仍可作为上层 Chat / Session 功能的统一抽象继续保留，但产品化 Bridge 连接不应再复用旧 `UnifiedBridgeAdapter` 的命名、注册协议和未认证连接模型。正确方向是新增 `CCCodeBridgeBackendClient` 或等价适配层，在内部连接 go-bridge 并使用 CCCode Bridge v1 协议。

## 3. 范围与非目标

### 3.1 v1 范围

- Mac App 内置完整 go-bridge runtime。
- `go-bridge` 与定制 `cc-connect` 收敛到同一个可发布 runtime，并作为 Mac Desktop App 的内置组件交付。
- 用户不需要单独下载、安装、编译或配置 `cc-connect`、go-bridge CLI、launchctl 服务或其他 Bridge 依赖。
- iOS 通过扫码或手动配对码完成首次配对。
- iOS 保存 Mac 授权信息，后续自动连接。
- Mac 端提供 agent/provider 检测、设备管理、Bridge 状态、日志、远程访问配置入口。
- 支持 Claude Code、OpenCode、Codex，Copilot 可按现有能力延后或标记实验。
- 支持局域网连接。
- 支持用户配置远程访问 URL，用于 FRP、Tailscale、Cloudflare Tunnel、反向代理等自带方案。
- 支持离线可读体验：最近 Workspace、Session 摘要和消息快照。
- 支持 Bridge 生命周期：开机启动、用户停止、崩溃恢复、睡眠/唤醒、升级兼容。

### 3.2 v1 非目标

- 不做官方云 relay。
- 不做跨 Apple ID 的云同步。
- 不承诺多 macOS 用户账户共享同一个 Bridge 配置。
- 不支持无 Mac 物理确认的远程首次配对。
- 不把完整 agent runtime 搬到 iOS。
- 不恢复旧 Node.js `UnifiedBridge` 服务，也不继续把 iOS 残留 `UnifiedBridge` 命名作为新功能承载层。

## 4. 总体架构

```mermaid
flowchart LR
    iOS["CCCode iOS"]
    Pair["Pairing / Saved Mac / Keychain"]
    Client["CCCodeBridgeClient"]
    MacApp["CCCode Bridge.app"]
    Runtime["go-bridge runtime"]
    Auth["Pairing + Device Auth"]
    Agents["cc-connect agent runtime"]
    Claude["Claude Code"]
    OpenCode["OpenCode"]
    Codex["Codex"]

    iOS --> Pair
    Pair --> Client
    Client <-->|"cccode-bridge-v1 over WebSocket"| Runtime
    MacApp --> Runtime
    Runtime --> Auth
    Runtime --> Agents
    Agents --> Claude
    Agents --> OpenCode
    Agents --> Codex
```

分层原则：

- **Mac SwiftUI App 不承载 agent runtime 逻辑**。它负责进程管理、配置 UI、权限提示、状态展示、日志入口。
- **go-bridge runtime 承载所有本地服务能力**。它负责协议、认证、agent 适配、事件流、诊断，是唯一 backend。
- **agent/provider runtime 保持 Go 生态**。优先复用当前 `go-bridge` 与定制 `cc-connect` 的成熟代码。
- **iOS 只连接 go-bridge 上的 CCCode Bridge v1**。上层 Chat/Session 可继续使用 `BackendClient` 抽象，但底层传输和认证必须替换为新 Bridge 客户端。

## 5. 仓库与交付物收敛

### 5.1 当前问题

当前 `go-bridge/go.mod` 通过本地 `replace` 依赖 `/Users/jacklee/Projects/cc-connect`，这适合开发阶段，但不能作为对外分发形态：

- 用户无法获得同样的本地目录结构。
- 版本不可追踪，发布构建不可复现。
- Mac App 难以签名、打包和升级。
- go-bridge 与 cc-connect 的责任边界对用户没有意义。

产品化约束是：`cc-connect` 不能以“请用户另行下载/配置”的方式存在。它可以在工程内部保持组件边界，但发布形态上必须成为 go-bridge runtime 的一部分，并随 Mac Desktop App 签名、打包、升级。

### 5.2 目标结构

产品化前应把定制 `cc-connect` 收敛进 go-bridge 的发布工程。推荐目标是 monorepo 内部模块化，而不是继续跨本地仓库引用。目录名可以继续叫 `go-bridge`，也可以在发布阶段改为 `bridge-runtime`；无论采用哪个路径，技术含义都是同一个 backend，不是新增第二套服务。

验收标准很简单：一台干净 Mac 上，用户只安装 `CCCode Bridge.app`，不安装 `cc-connect` 仓库、不运行 go-bridge CLI、不写 shell 配置，也能完成扫码配对和 agent/provider 检测。

```text
opencode-cc-connect/
  go-bridge/ 或 bridge-runtime/
    cmd/cccode-bridge-runtime/
    internal/server/
    internal/auth/
    internal/pairing/
    internal/devices/
    internal/diagnostics/
    internal/agents/
    internal/config/
  MacBridge/
    CCCodeBridge.xcodeproj 或 project.yml
    Sources/
    Resources/
  OpenCodeiOS/
  docs/
```

### 5.3 cc-connect 收敛路径

Phase 0 推荐采用 **cc-connect 发布 tagged version，go-bridge 通过远程 Go module 引用该版本** 的路径，而不是继续使用本地 `replace`。

目标形态：

```go
require github.com/chenhg5/cc-connect v0.1.0-cccode.1
```

发布构建中不允许出现指向开发者本机路径的 `replace`：

```go
replace github.com/chenhg5/cc-connect => /Users/jacklee/Projects/cc-connect
```

执行步骤：

1. 在定制 `cc-connect` 仓库整理当前 go-bridge 依赖的 agent/core 能力。
2. 给该状态打 tag，例如 `v0.1.0-cccode.1`。
3. go-bridge 的 `go.mod` 改为引用该 tag。
4. go-bridge 发布构建必须在干净 checkout 中通过，不依赖 `/Users/.../cc-connect`。
5. 后续 `cc-connect` 修改先发新 tag，再由 go-bridge 升级依赖版本。

这个路径保留 `cc-connect` 的独立维护能力，也避免 Telegram/Slack 等其他消费端被迫和 CCCode Bridge 的发布节奏绑定。Mac Desktop App 的交付形态仍然是单 App：tagged module 是构建期依赖，不是用户侧依赖。

不推荐第一阶段直接把 `cc-connect` 代码复制进 go-bridge 的 `internal/` 目录。原因是复制会立刻制造版本分叉：同一份 agent/core 修复需要在两个地方维护，后续很容易出现 go-bridge 可用但 cc-connect 主线不可用，或反过来的情况。只有当上游模块边界无法满足签名、授权或发布合规要求时，才重新评估 vendoring 或 internal copy。

可以保留 Go 内部包边界：

| 模块 | 职责 |
|---|---|
| `server` | WebSocket / local HTTP / routing / event broadcast |
| `auth` | device token 校验、token hash、权限上下文 |
| `pairing` | QR / manual code / pending pairing lifecycle |
| `devices` | trusted devices store、撤销、lastSeen |
| `agents` | Claude/OpenCode/Codex/Copilot 适配入口 |
| `diagnostics` | agent/provider 检测、端口检查、日志摘要 |
| `config` | runtime config、远程访问地址、启动策略 |

### 5.4 不建议直接把 Go 逻辑改写进 Swift

Mac App 第一版应当内嵌并管理 go-bridge runtime，而不是重写 Go runtime：

- 现有 agent 适配和事件链路已经在 Go 里成型。
- Claude Code、OpenCode、Codex 的进程模型差异已经被 Go runtime 吸收了一部分。
- SwiftUI 适合做用户配置与系统集成，不适合在第一版承接所有 agent runtime 风险。

## 6. Mac App 技术方案

### 6.1 进程模型

```text
CCCode Bridge.app
  ├── 主 App 进程：SwiftUI + menu bar
  └── 子进程：cccode-bridge-runtime（go-bridge 产品化可执行文件）
```

Mac App 负责：

- 启动 runtime。
- 停止 runtime。
- 检测 runtime health。
- 在 runtime 崩溃时按策略重启。
- 展示 runtime 状态和日志。
- 通过本地 loopback 管理接口读取状态。
- 发起 pairing session。
- 确认或拒绝 iPhone 配对请求。
- 写入用户配置，如远程访问 URL、开机启动、agent/provider 路径。

go-bridge runtime 负责：

- 对 iOS 提供 Bridge v1 协议。
- 管理已授权设备。
- 连接 Claude Code / OpenCode / Codex 等 agent/provider。
- 转发 session/message/tool/todo/memory/model 等能力。
- 推送运行事件。
- 暴露本地管理 API 给 Mac App。

### 6.2 本地管理接口

Mac App 与 runtime 不应复用面向 iOS 的公网/局域网 API。建议 runtime 提供只监听 `127.0.0.1` 的管理接口，使用启动时生成的一次性 management secret。

```text
Mac App -> runtime local management API
  GET  /internal/status
  GET  /internal/agents
  POST /internal/runtime/start-agent-scan
  POST /internal/pairing/create
  POST /internal/pairing/:id/approve
  POST /internal/pairing/:id/reject
  GET  /internal/devices
  POST /internal/devices/:id/revoke
  GET  /internal/logs/recent
  POST /internal/config/remote-access
```

这个接口不对 iOS 暴露，主要收益是：

- Mac UI 不需要直接读写 runtime 内部文件。
- iOS 协议可以保持稳定，不被 Mac UI 需求污染。
- 后续可以把 runtime 独立做命令行诊断，但不暴露给普通用户。

### 6.3 数据目录

Mac App 和 go-bridge runtime 必须共享一套明确的数据目录，避免状态散落在工作目录、临时目录或开发机路径里。

推荐位置：

```text
~/Library/Application Support/CCCode Bridge/
  identity.json
  devices.json
  config.json
  pairing/
  logs/
  runtime/
```

文件职责：

| 文件/目录 | 职责 |
|---|---|
| `identity.json` | Bridge identity、创建时间、当前 schema version |
| `devices.json` | trusted devices、token hash、撤销状态、lastSeen |
| `config.json` | 端口、远程 URL、开机启动偏好、agent/provider 配置 |
| `pairing/` | 短期 pairing session 状态；启动时清理过期项 |
| `logs/` | runtime 和 Mac App 可读日志；按大小或日期轮转 |
| `runtime/` | runtime pid、management port、临时 secret 等运行态文件 |

Mac App 启动 runtime 时通过 `--data-dir` 显式传入该目录。runtime 不应默认把产品状态写进当前工作目录。

### 6.4 启动与通信契约

Mac App 启动 go-bridge runtime 时必须使用显式参数，而不是依赖环境偶然状态。

建议启动参数：

```bash
cccode-bridge-runtime \
  --ios-port 8777 \
  --management-host 127.0.0.1 \
  --management-port 0 \
  --management-token <random-256-bit-token> \
  --data-dir "$HOME/Library/Application Support/CCCode Bridge" \
  --log-dir "$HOME/Library/Application Support/CCCode Bridge/logs" \
  --log-level info
```

参数规则：

| 参数 | 规则 |
|---|---|
| `--ios-port` | 面向 iOS 的 Bridge v1 端口，默认 `8777` |
| `--management-host` | 固定为 `127.0.0.1` |
| `--management-port` | `0` 表示 runtime 自动选择空闲端口 |
| `--management-token` | Mac App 生成的一次性随机值，只用于本次 runtime 生命周期 |
| `--data-dir` | 产品状态目录 |
| `--log-dir` | 日志目录 |
| `--log-level` | 默认 `info`，诊断模式可临时提升 |

就绪检测：

1. Mac App 启动子进程并读取 stdout。
2. runtime 绑定管理端口后，向 stdout 输出一行 JSON ready frame。
3. Mac App 读取到 ready frame 后，使用 management token 调用 `/internal/status`。
4. `/internal/status` 返回 `ready` 后，Mac UI 才进入 Bridge ready 或 no agents 状态。

ready frame 示例：

```json
{
  "type": "runtime_ready",
  "managementURL": "http://127.0.0.1:49152",
  "iosURL": "ws://0.0.0.0:8777/bridge",
  "pid": 12345
}
```

启动失败时，runtime 应在退出前向 stdout 输出一行 JSON error frame，便于 Mac App 映射到具体 UI 状态，而不是只看到子进程退出。

error frame 示例：

```json
{
  "type": "runtime_error",
  "error": "port_bind_failed",
  "message": "Port 8777 is already in use by another process.",
  "exitCode": 1
}
```

常见启动错误：

| error | Mac App 处理 |
|---|---|
| `port_bind_failed` | 显示端口冲突和旧 go-bridge 迁移入口 |
| `data_dir_unavailable` | 显示数据目录不可用诊断 |
| `config_invalid` | 显示配置损坏诊断和重置入口 |
| `management_bind_failed` | 显示 runtime 内部管理接口启动失败 |
| `agent_init_failed` | Bridge 可继续启动，但 agent/provider 状态显示异常 |

崩溃检测：

- Mac App 使用 `Process`/`TerminationHandler` 或 `DispatchSourceProcess` 监听子进程退出。
- 非零退出且不是用户显式停止时，标记为 `crashed` 或 `restarting`。
- 崩溃重启必须有上限，例如 5 分钟内最多 3 次。
- 每次异常退出都记录 exit code、signal、最后 200 行 runtime 日志。

日志契约：

- runtime stdout 只输出结构化 lifecycle frame 和必要启动错误。
- 运行日志写入 `logs/runtime.log` 并轮转。
- Mac App 通过 `/internal/logs/recent` 拉取用户可见日志摘要，不直接解析大日志文件作为主要路径。

优雅停止契约：

1. 用户在 Mac App 中停止 Bridge，或 App 准备升级 runtime。
2. Mac App 优先调用 `/internal/shutdown`，并同时保留向子进程发送 `SIGTERM` 的能力。
3. runtime 收到 shutdown 后停止接受新 iOS 连接和新 request。
4. runtime 等待飞行中的非副作用读请求结束；对新的写请求返回 `bridge.shutting_down`。
5. runtime 刷写 trusted devices、config、logs 后退出。
6. Mac App 最多等待 5 秒；超时后发送 `SIGTERM`。
7. 再等待 3 秒仍未退出时，Mac App 才强杀并记录 `forced_kill` 诊断。

### 6.5 macOS 架构与打包

第一版建议只做 **Apple Silicon 原生**：

- 降低签名、打包、依赖和 QA 成本。
- 当前目标用户大概率使用 Apple Silicon Mac。
- Intel 版本可在 v1 稳定后按需求追加 Universal build。

发布要求：

- Mac App 需要 Developer ID 签名。
- App 和内嵌 runtime 都需要签名。
- 发布包需要 notarization。
- runtime 放入 App bundle 的 `Contents/Resources` 或 `Contents/Library` 下，由 App 启动。
- runtime 版本号随 Mac App 版本锁定，不允许用户单独替换。

签名和公证顺序：

1. 构建 Apple Silicon Go binary：`GOOS=darwin GOARCH=arm64`。
2. 对 Go Mach-O 可执行文件单独 `codesign`。
3. 将已签名 runtime 放入 App bundle。
4. 对完整 App bundle `codesign --deep --strict`。
5. 使用 `notarytool submit` 提交公证。
6. stapler 绑定公证结果后，再生成最终下载包。

Phase 1 的 Mac App runtime manager spike 必须验证这个顺序，特别是“内嵌 Go binary 已签名后再签 App bundle”的路径。

### 6.6 启动与生命周期

Bridge 状态应分为用户可理解的状态，而不是直接暴露进程状态：

| 状态 | 技术含义 | 用户表达 |
|---|---|---|
| `ready` | runtime 运行，至少管理接口正常 | Bridge is ready |
| `ready_no_agents` | runtime 运行，但所有 agent/provider 未检测 | Set up an agent to begin |
| `stopped_by_user` | 用户显式停止 runtime | Bridge stopped |
| `starting` | App 正在启动 runtime | Starting Bridge |
| `restarting` | 崩溃后恢复或升级后重启 | Restarting Bridge |
| `sleeping_or_unreachable` | Mac 睡眠或网络断开 | Mac may be asleep |
| `crashed` | runtime 异常退出且重启失败 | Bridge crashed |
| `needs_update` | iOS/Bridge 协议不兼容 | Update required |

生命周期规则：

- 用户显式停止后，不自动拉起 runtime。
- 用户开启开机启动后，Mac 登录时恢复上次运行状态。
- runtime 异常退出时，Mac App 最多执行有限次数重启，并保留崩溃日志。
- Mac 合盖或睡眠导致 iOS 断连时，不显示为 fatal error。
- Mac 唤醒后，runtime 应恢复监听，iOS 进入自动重连。
- App 升级时先停止旧 runtime，再启动新 runtime，并保持 trusted devices store 不变。

睡眠/唤醒实现：

- Mac App 监听 `NSWorkspace.willSleepNotification` 和 `NSWorkspace.didWakeNotification`。
- will sleep 时，Mac App 将本地状态标记为 `sleeping_or_unreachable`，runtime 不需要伪造 session 失败事件。
- did wake 后，Mac App 调用 `/internal/status` 和 `/internal/agents` 刷新状态。
- runtime 如果仍在运行但 iOS 监听端口失效，应重建 listener。
- iOS 端重连成功后通过 `hello_ack.runningSessions` 和 `session.messages` 恢复当前页面。

### 6.7 菜单栏常驻

菜单栏不是第二套产品入口，而是 Bridge 状态和常用动作入口：

- Open CCCode Bridge
- Pair iPhone / iPad
- Bridge status
- Recent issue / last error
- Quit

菜单栏图标使用单色 SF Symbol 风格。状态变化通过菜单内容和主窗口表达，不通过彩色复杂图标承载主要信息。

## 7. go-bridge runtime 技术方案

### 7.1 从开发态 go-bridge 到产品态 go-bridge

当前 `go-bridge` 已具备核心雏形：

- WebSocket 服务。
- register/request/result/event envelope。
- Claude/OpenCode/Codex agent 注册。
- OpenCode proxy。
- 事件广播。
- session registry。

产品化不是替换 `go-bridge`，而是把它从开发态 CLI 服务升级为 Mac App 内置的产品态 runtime。`cc-connect` 不是用户侧依赖，而是 go-bridge runtime 内部组件。需要在现有基础上补齐：

- 认证前置。
- 配对流程。
- trusted devices store。
- Bridge identity。
- version/capability handshake。
- Workspace / Session 的用户侧映射。
- Mac lifecycle/diagnostics 管理接口。
- 可发布构建，不依赖本地 `replace`。
- 移除旧 Node.js UnifiedBridge 遗留协议命名。

### 7.2 监听端口

默认策略：

- iOS 连接端口继续可使用 `8777`，但必须由 Mac App 管理。
- 管理端口使用随机 loopback 端口，写入 App/runtime 之间的启动握手，不固定暴露。
- 如果 `8777` 被旧服务占用，Mac App 显示迁移/停止旧服务建议，不静默换端口造成用户无法配对。

### 7.3 Bridge identity

每个 Mac 用户账户下生成一个稳定的 `bridgeId`：

```json
{
  "bridgeId": "brg_01HY...",
  "displayName": "Jack's MacBook Pro",
  "createdAt": "2026-05-07T10:00:00Z",
  "runtimeVersion": "1.0.0",
  "protocol": {
    "name": "cccode-bridge",
    "version": 1,
    "schemaRevision": "2026-05-07"
  }
}
```

`bridgeId` 绑定的是当前 macOS 用户账户下的 Bridge 配置，不跨 macOS 用户共享。

### 7.4 agent/provider 检测

go-bridge runtime 应输出 agent/provider 检测结果，而不是只返回可用目标列表。这样 Mac 和 iOS 都能表达 “Bridge ready + no agents detected”。这里的 `backends` 如果继续出现在协议字段里，只表示 go-bridge 管理的 agent target，不表示有多个网络 backend。

```json
{
  "backends": [
    {
      "id": "claude",
      "kind": "claude_code",
      "displayName": "Claude Code",
      "status": "available",
      "capabilities": ["sessions", "messages", "permissions", "models"]
    },
    {
      "id": "opencode",
      "kind": "opencode",
      "displayName": "OpenCode",
      "status": "not_detected",
      "reason": "server_not_running"
    }
  ]
}
```

检测结果要区分：

- 未安装。
- 未登录。
- 本地服务未启动。
- 端口冲突。
- 版本不支持。
- 权限不足。
- 可用。

### 7.5 Agent 进程模型

现有四类 agent/provider 的进程模型差异必须保留为技术事实：

| Agent/provider | 模型 | iOS 体验影响 |
|---|---|---|
| Codex app-server | 共享服务 + WebSocket 广播 | 可实时旁观 |
| OpenCode | 共享 HTTP/SSE 服务 | 可实时旁观 |
| Claude Code | 独立 CLI 进程 | 旁观 Mac 端任务需要读取历史或轮询 |
| Copilot | 独立进程/ACP | 实时性能力受限 |

Bridge v1 不应向 iOS 假装所有 agent/provider 都有同样实时能力。descriptor 里要声明：

```json
{
  "liveEvents": "broadcast",
  "requiresPollingForExternalTurns": false
}
```

Claude Code 应声明：

```json
{
  "liveEvents": "session_process",
  "requiresPollingForExternalTurns": true
}
```

这样 iOS 的刷新策略由能力驱动，而不是写死在旧 `BackendKind.thinBridge` 或历史命名的 `UnifiedBridgeAdapter` 里。

### 7.6 Mac 发现与地址更新

二维码里的 `localURL` 只能作为首次连接地址，不能假设 Mac 的局域网 IP 永远不变。Phase 0/1 先采用低复杂度策略，Phase 2 再接入 iOS 自动发现。

v1 最小策略：

- QR payload 包含当前 `localURL`。
- iOS 每次成功 `hello` 后，用 runtime 返回的 `bridge.currentURLs.local` 更新 Saved Mac。
- 如果保存的 `localURL` 连接失败，iOS 进入 reconnect/diagnostics，不做静默局域网扫描。
- Mac App 首页显示当前 local URL，方便用户判断网络变化。

runtime 在 `hello_ack` 中返回：

```json
{
  "bridge": {
    "bridgeId": "brg_01HY...",
    "displayName": "Jack's MacBook Pro",
    "currentURLs": {
      "local": "ws://192.168.1.25:8777/bridge",
      "remote": null
    }
  }
}
```

Bonjour/mDNS 作为 v1.1 候选增强：

- runtime 广播 `_cccode-bridge._tcp`。
- TXT record 只包含 `bridgeId`、协议版本和端口，不包含 token。
- iOS 用 Bonjour 找到同一 `bridgeId` 后更新 local URL。

第一版不采用局域网 IP 段扫描。原因是体验不可控、权限提示更重、容易被网络设备拦截，也更难解释失败原因。

## 8. 配对与认证

### 8.1 安全原则

- 未配对设备不能访问任何 session、workspace、agent/provider 状态或日志。
- 二维码和手动码只代表短期配对会话，不包含长期 token。
- 长期 device token 只在配对确认后签发。
- iOS 端 token 存 Keychain。
- Mac 端只保存 token hash，不保存明文 token。
- Mac 端可以随时撤销设备。
- 远程访问不会绕过配对和 Mac 端确认。

### 8.2 配对流程

```mermaid
sequenceDiagram
    participant Mac as CCCode Bridge.app
    participant Runtime as go-bridge runtime
    participant iOS as CCCode iOS

    Mac->>Runtime: create pairing session
    Runtime-->>Mac: pairingId, qrPayload, manualCode, expiresAt
    iOS->>Runtime: pairing/claim(pairingId or manualCode)
    Runtime-->>Mac: pending device request
    Mac->>Runtime: approve pairing
    Runtime-->>iOS: device token + bridge profile
    iOS->>iOS: save token in Keychain
    Runtime->>Runtime: save token hash in trusted devices
```

### 8.3 QR payload

二维码只包含连接和配对启动信息：

```json
{
  "type": "cccode.bridge.pairing",
  "version": 1,
  "bridgeId": "brg_01HY...",
  "displayName": "Jack's MacBook Pro",
  "localURL": "ws://192.168.1.25:8777/bridge",
  "pairingId": "pair_01HY...",
  "expiresAt": "2026-05-07T10:05:00Z"
}
```

不把长期 token 放进二维码，原因是二维码可能被拍照、截屏或远程转发。

### 8.4 手动配对码

手动码用于扫码不可用或相机权限被拒绝的场景：

- 6 位数字。
- 与二维码共享同一个 pairing session。
- M4 与 M4a 切换不重置倒计时。
- 过期后必须重新生成。
- 输入错误不暴露具体 pairing session 是否存在。

### 8.5 设备授权模型

Mac 端 trusted device 记录：

```json
{
  "deviceId": "dev_01HY...",
  "displayName": "Jack's iPhone",
  "platform": "iOS",
  "tokenHash": "sha256:...",
  "createdAt": "2026-05-07T10:05:30Z",
  "lastSeenAt": "2026-05-07T11:20:00Z",
  "lastRemoteAddress": "192.168.1.30",
  "revokedAt": null
}
```

iOS 保存：

```json
{
  "bridgeId": "brg_01HY...",
  "displayName": "Jack's MacBook Pro",
  "localURL": "ws://192.168.1.25:8777/bridge",
  "remoteURL": null,
  "deviceId": "dev_01HY...",
  "tokenKeychainRef": "...",
  "lastConnectedAt": "2026-05-07T11:20:00Z",
  "lastCapabilities": {}
}
```

### 8.6 Device token 格式

Device token 是配对成功后签发的长期凭证，不使用 JWT。第一版采用 opaque random token，减少服务端解析和撤销复杂度。

推荐格式：

```text
ccb1_<base64url-encoded-32-random-bytes>
```

规则：

| 项 | 规则 |
|---|---|
| 前缀 | `ccb1_`，表示 CCCode Bridge token v1 |
| 随机长度 | 32 bytes cryptographically secure random |
| 编码 | base64url，无 padding |
| 明文存储 | 只存 iOS Keychain |
| Mac 存储 | 只保存 `sha256(token)`，格式 `sha256:<hex>` |
| 传输 | 只在 Mac approve 后的 pairing response 中返回一次 |
| 撤销 | Mac 将 device 标记 `revokedAt`，后续 token 校验失败 |

pairing approve 后返回给 iOS：

```json
{
  "type": "pairing_result",
  "ok": true,
  "device": {
    "deviceId": "dev_01HY...",
    "token": "ccb1_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
  },
  "bridge": {
    "bridgeId": "brg_01HY...",
    "displayName": "Jack's MacBook Pro",
    "localURL": "ws://192.168.1.25:8777/bridge"
  }
}
```

iOS 后续连接优先通过 WebSocket header 携带：

```text
Authorization: Bearer ccb1_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
X-CCCode-Device-ID: dev_01HY...
```

`hello` 首包仍包含 `deviceId` 和 protocol 信息，但不把 token 放进 JSON body，除非后续诊断模式明确需要兼容 header 不可用的环境。

### 8.7 配对会话状态机

pairing session 是短期状态，默认 5 分钟过期。M4 QR 和 M4a 手动码共享同一个 session。

```mermaid
stateDiagram-v2
    [*] --> Created
    Created --> Claimed: iOS pairing.claim
    Created --> Expired: timeout
    Claimed --> Approved: Mac approve
    Claimed --> Rejected: Mac reject
    Claimed --> Expired: timeout
    Approved --> Completed: iOS receives token
    Approved --> Expired: token pickup timeout
    Rejected --> [*]
    Expired --> [*]
    Completed --> [*]
```

状态规则：

- `Created`：Mac 生成 QR/manual code，等待 iOS claim。
- `Claimed`：iOS 已提交设备信息，Mac UI 显示 pending device。
- `Approved`：Mac 已批准，runtime 生成 device token，短期等待 iOS 拉取。
- `Completed`：iOS 已收到 token，runtime 写入 trusted devices。
- `Rejected`：Mac 拒绝，iOS 显示被拒绝。
- `Expired`：超时后 QR/manual code 失效。

边界处理：

- iOS claim 后 Mac 长时间不确认：进入 `Expired`，iOS 显示 pairing expired。
- Mac approve 后 iOS 断连：token 在 pairing session 内短期保留，超过 pickup timeout 后销毁；Mac 端不写 trusted device。
- 同一个 pairing session 被第二台设备 claim：默认拒绝第二次 claim，要求 Mac 重新生成 pairing session。
- Mac App 退出或 runtime 重启：所有未完成 pairing session 作废。

## 9. CCCode Bridge v1 协议

### 9.1 命名

协议名冻结为：

```json
{
  "name": "cccode-bridge",
  "version": 1,
  "schemaRevision": "2026-05-07"
}
```

不继续使用 `unified-bridge` 作为协议名。这里不是否定 go-bridge，而是切断旧 Node.js UnifiedBridge 的历史命名。新 iOS 不应主动发起旧 `unified-bridge` 注册；go-bridge 如需短期识别旧字段，只服务于迁移和兼容诊断。

### 9.2 WebSocket endpoint

建议路径：

```text
ws://<mac-host>:8777/bridge
wss://<remote-host>/bridge
```

所有非 pairing 请求必须携带 device auth。可以使用首包 `hello` 携带 token，也可以使用 WebSocket header。iOS 原生 WebSocket 设置 header 可控，优先 header；同时保留首包校验以便诊断。

### 9.3 envelope

```json
{
  "type": "request",
  "requestId": "req_01HY...",
  "method": "session.list",
  "params": {}
}
```

响应：

```json
{
  "type": "result",
  "requestId": "req_01HY...",
  "ok": true,
  "data": {}
}
```

事件：

```json
{
  "type": "event",
  "seq": 42,
  "backendId": "codex",
  "workspaceId": "wsp_...",
  "sessionId": "ses_...",
  "event": "message.delta",
  "data": {}
}
```

错误：

```json
{
  "type": "result",
  "requestId": "req_01HY...",
  "ok": false,
  "error": {
    "code": "auth.revoked",
    "message": "This device is no longer trusted by the Mac."
  }
}
```

### 9.4 最小方法集

| 方法 | 用途 | 是否需要认证 |
|---|---|---|
| `pairing.claim` | iOS 认领二维码/手动码 | 否 |
| `pairing.poll` | iOS 等待 Mac 确认 | 否，需 pairing secret |
| `hello` | 认证、版本握手、能力获取 | 是 |
| `bridge.status` | Bridge 状态 | 是 |
| `backend.list` | agent/provider 检测与能力；字段名可迁移为 `agent.list` | 是 |
| `workspace.list` | Workspace 列表 | 是 |
| `session.list` | Session 列表 | 是 |
| `session.get` | Session 元信息 | 是 |
| `session.messages` | 消息历史 | 是 |
| `session.create` | 创建 Session | 是 |
| `session.send` | 发送消息 | 是 |
| `session.abort` | 中断生成 | 是 |
| `model.list` | 模型列表 | 是 |
| `model.set` | 设置模型 | 是 |
| `permission.resolve` | Claude Code 权限确认 | 是 |
| `diagnostics.summary` | 用户可见诊断摘要 | 是 |

### 9.5 version/capability handshake

iOS 连接成功后的第一步：

```json
{
  "type": "hello",
  "client": {
    "app": "CCCode iOS",
    "version": "1.0.0",
    "deviceId": "dev_01HY..."
  },
  "protocol": {
    "name": "cccode-bridge",
    "version": 1,
    "supportedSchemaRevisions": ["2026-05-07"]
  }
}
```

runtime 返回：

```json
{
  "type": "hello_ack",
  "ok": true,
  "bridge": {
    "bridgeId": "brg_01HY...",
    "displayName": "Jack's MacBook Pro",
    "runtimeVersion": "1.0.0"
  },
  "capabilities": {
    "remoteAccessConfig": true,
    "trustedDevices": true,
    "offlineSnapshots": true,
    "workspaceList": true,
    "sessionMutation": true
  },
  "backends": []
}
```

如果版本不兼容：

```json
{
  "type": "hello_ack",
  "ok": false,
  "error": {
    "code": "protocol.unsupported_version",
    "message": "Update CCCode Bridge on your Mac to connect this iPhone."
  }
}
```

### 9.6 重连与恢复

iOS 每次 WebSocket 重连后必须重新发送 `hello`。runtime 不假设旧连接状态仍然有效。

keepalive 规则：

- iOS 每 30 秒发送 WebSocket ping。
- runtime 使用标准 WebSocket pong 响应。
- 连续 3 次 ping 无响应后，iOS 主动关闭连接并触发重连。
- 如果正在旁观长任务，keepalive 失败不立刻显示任务失败，而是进入 reconnecting 状态。
- keepalive 使用 WebSocket 控制帧，不新增 Bridge v1 JSON message。

重连规则：

- WebSocket 断开后，飞行中的 request 标记为 failed。
- iOS 上层决定是否重试，transport 不自动重放 `session.send` 这类有副作用请求。
- 重连成功后，runtime 返回 Bridge 状态、agent/provider 状态、当前 running sessions 摘要。
- iOS 根据摘要刷新 UI，并为当前打开的 session 调用 `session.messages` 补齐消息。
- 如果 token 被撤销，runtime 返回 `auth.revoked`，iOS 停止重试并进入重新配对路径。

`hello_ack` 可包含恢复摘要：

```json
{
  "type": "hello_ack",
  "ok": true,
  "bridgeStatus": "ready",
  "runningSessions": [
    {
      "backendId": "codex",
      "sessionId": "thread_...",
      "workspaceId": "wsp_...",
      "status": "running"
    }
  ]
}
```

### 9.7 错误码命名空间

Bridge v1 错误码使用点分命名，iOS 以错误码做稳定映射，`message` 只作为诊断补充。

| 前缀 | 范围 | 示例 |
|---|---|---|
| `auth.*` | 认证、授权、设备撤销 | `auth.missing_token`, `auth.invalid_token`, `auth.revoked` |
| `protocol.*` | 协议版本、schema、未知方法 | `protocol.unsupported_version`, `protocol.unknown_method` |
| `pairing.*` | 配对流程 | `pairing.expired`, `pairing.rejected`, `pairing.already_claimed` |
| `bridge.*` | go-bridge runtime 状态 | `bridge.not_ready`, `bridge.shutting_down`, `bridge.needs_update` |
| `agent.*` | agent/provider 检测与运行 | `agent.not_detected`, `agent.unavailable`, `agent.version_unsupported` |
| `workspace.*` | Workspace 解析 | `workspace.not_found`, `workspace.access_denied` |
| `session.*` | Session 操作 | `session.not_found`, `session.not_running`, `session.conflict` |
| `permission.*` | 工具权限确认 | `permission.not_found`, `permission.already_resolved` |
| `network.*` | runtime 对外部本地服务访问 | `network.timeout`, `network.connection_refused` |

第一版不把底层 Go error string 直接透传给 iOS UI。runtime 需要把错误归一到上述命名空间。

### 9.8 事件类型

Bridge v1 事件类型保持稳定命名，避免 iOS 直接依赖不同 agent/provider 的原始事件名。

| 事件 | 用途 |
|---|---|
| `session.started` | session 或 turn 开始运行 |
| `session.completed` | session 或 turn 正常结束 |
| `session.failed` | agent/provider 返回失败 |
| `session.aborted` | 用户或系统中断 |
| `message.started` | assistant message 容器开始；一次 turn 中可能出现多条 message |
| `message.delta` | 文本增量 |
| `message.completed` | 一条 assistant message 完成 |
| `tool.started` | 工具调用开始 |
| `tool.delta` | 工具输出增量 |
| `tool.completed` | 工具调用完成 |
| `permission.requested` | 需要用户批准 |
| `permission.resolved` | 权限已处理 |
| `todos.updated` | todo 列表变化 |
| `model.changed` | 当前模型变化 |
| `agent.status_changed` | agent/provider 可用性变化 |
| `bridge.status_changed` | go-bridge 状态变化 |

Claude Code、OpenCode、Codex 的原始事件可以在 runtime 内部保留，但发给 iOS 前必须映射到 Bridge v1 事件集合。

## 10. iOS 技术方案

### 10.1 新模块边界

iOS 应新增 Bridge 产品化模块，目标是把历史 `UnifiedBridge` 命名从产品化连接层里剥离出来。这里的重点不是废掉当前功能，也不是否定现有 go-bridge 连接能力，而是承认当前 iOS 代码里很多 `Unified` 描述没有随 Node.js UnifiedBridge 废弃和 backend 切换到 go-bridge 后及时改名。

```text
Services/Bridge/
  CCCodeBridgeClient.swift
  CCCodeBridgeTransport.swift
  CCCodeBridgeModels.swift
  BridgePairingService.swift
  SavedBridgeStore.swift
  BridgeCredentialStore.swift
  BridgeConnectionStateStore.swift
  BridgeOfflineSnapshotStore.swift
```

职责：

| 模块 | 职责 |
|---|---|
| `BridgePairingService` | 扫码、手动码、等待 Mac 确认、保存授权 |
| `SavedBridgeStore` | 保存已配对 Mac 的非敏感元数据 |
| `BridgeCredentialStore` | Keychain 中保存 device token |
| `CCCodeBridgeTransport` | WebSocket、request/response、event stream、重连 |
| `CCCodeBridgeClient` | Bridge v1 方法封装 |
| `CCCodeBridgeBackendClient` | 适配现有 `BackendClient` 上层调用 |
| `BridgeOfflineSnapshotStore` | 离线 Workspace/Session/Message 快照 |

### 10.2 与现有 BackendClient 的关系

短期不建议重写 ChatViewModel 全链路。更稳妥的迁移是：

```text
Chat / Session UI
  -> BackendClient
    -> CCCodeBridgeBackendClient
      -> CCCodeBridgeClient
        -> CCCodeBridgeTransport
```

这样可以复用现有 Chat / Session / MessageWeb 的大量逻辑。历史命名的 `UnifiedBridgeAdapter` 如果当前仍能正常连接 go-bridge，短期不应被当作废弃功能删除；产品化迁移应把它替换或重命名为 go-bridge/CCCode Bridge v1 适配层，并补齐配对认证和 Saved Mac 模型。

迁移完成后：

- `BackendClientFactory` 不再默认返回带旧 UnifiedBridge 命名的 adapter；如果复用现有实现，先完成语义重命名和认证改造。
- `.thinBridge` 不再作为新产品 backend kind。
- 用户配置不再是 host/port/backend 三元组，而是 Saved Mac + agent/provider target。
- 旧手动服务器配置仅进入迁移流程。

### 10.3 Saved Mac 模型

新 iOS 数据模型应围绕 Mac，而不是围绕 server endpoint：

```swift
struct SavedBridge: Identifiable, Codable, Sendable {
    var id: String              // bridgeId
    var displayName: String
    var localURL: URL
    var remoteURL: URL?
    var deviceId: String
    var lastConnectedAt: Date?
    var lastKnownStatus: BridgeStatusSnapshot?
    var lastCapabilities: BridgeCapabilities?
}
```

agent/provider target 是 Saved Mac 下的二级对象：

```swift
struct BridgeBackendTarget: Identifiable, Codable, Sendable {
    var id: String              // claude / opencode / codex
    var kind: String
    var displayName: String
    var status: BackendRuntimeStatus
    var capabilities: [String]
}
```

这个模型能直接支持产品定义里的 Sidebar / Workspace / Session 表达。

### 10.4 Workspace 与 Session 映射

Workspace 是用户理解的“项目入口”，不是完整本地路径。runtime 可以返回：

```json
{
  "id": "wsp_...",
  "displayName": "opencode-cc-connect",
  "shortPath": "~/Projects/opencode-cc-connect",
  "backendIds": ["claude", "codex"],
  "lastActiveAt": "2026-05-07T11:00:00Z"
}
```

iOS 列表默认显示 `displayName + shortPath`，不显示完整 `/Users/...`。

Session 是某个 agent/provider 在某个 workspace 下的一段 agent 对话或任务上下文。不同 provider 的 native session id 不需要全局一致，go-bridge 对 iOS 提供统一 envelope：

```json
{
  "id": "codex:thread_...",
  "nativeSessionId": "thread_...",
  "backendId": "codex",
  "workspaceId": "wsp_...",
  "title": "Implement bridge pairing",
  "status": "running",
  "updatedAt": "2026-05-07T11:00:00Z"
}
```

### 10.5 离线体验

iOS 需要保存最近一次成功连接时的快照：

- Saved Mac 列表。
- Bridge 状态摘要。
- Backend 状态摘要。
- Workspace 列表。
- Session 列表。
- 最近打开 Session 的消息快照。

离线时：

- Workspace/Session 可以只读浏览。
- 输入框可保存本地草稿，但不能发送。
- 如果重新连接后发现 Session 已结束，草稿保留，并提示用户选择新建发送或丢弃。
- 如果 device token 被撤销，离线快照仍可按本机策略短期只读展示，但连接操作必须进入重新配对。

### 10.6 本地通知

iOS 通知在配对成功后请求授权，不在首次打开 App 时打断用户。通知只服务“旁观长任务”和“需要用户动作”的场景，不把普通流式输出做成通知噪音。

事件到通知的最小映射：

| Bridge 事件/状态 | 通知行为 |
|---|---|
| `permission.requested` | 发送本地通知，点击进入对应 session 的权限确认 |
| `session.completed` | 如果用户正在旁观该 session 且 App 不在前台，发送完成通知 |
| `session.failed` | 发送异常通知，点击进入 session 错误状态 |
| `bridge.status_changed` -> `sleeping_or_unreachable` | 运行中 session 断连时发送可见通知 |
| `auth.revoked` | 不发普通通知；下次打开 App 进入重新配对 |

通知 payload 应包含：

```json
{
  "bridgeId": "brg_...",
  "workspaceId": "wsp_...",
  "backendId": "codex",
  "sessionId": "thread_...",
  "route": "session"
}
```

点击通知后，iOS 使用 `bridgeId/workspaceId/backendId/sessionId` deep link 到对应 Session。如果当前离线，则打开离线快照和连接诊断，而不是空白页。

### 10.7 本地网络权限

iOS 首次连接局域网 go-bridge 时可能触发系统 Local Network 权限。产品和技术都要把它当成正常路径：

- 在扫码前用页面文案解释需要连接同一 Wi-Fi 下的 Mac。
- 系统权限弹出后，如果用户拒绝，I2/I3 进入本地网络权限诊断。
- 本地网络权限被拒绝时，不应提示 token 错误或 pairing 失败。
- 如果用户配置了 remoteURL，可以允许走远程连接，但仍必须完成 Mac 端配对确认。

Mac App 如果后续启用 Bonjour/mDNS，也需要在 Info.plist 声明对应 service type；Phase 0/1 不依赖 Bonjour 时不强制引入。

## 11. 远程访问

### 11.1 v1 策略

第一版不做官方 relay，只提供用户自带远程访问的配置入口：

- 用户在 Mac App 里填写远程 URL。
- iOS 同一个 Saved Mac 同时保存 localURL 和 remoteURL。
- iOS 优先尝试 localURL，失败后按用户确认策略尝试 remoteURL。
- 首次启用 remoteURL 时，iOS 明确提示正在通过外网地址连接自己的 Mac。

### 11.2 TLS 与认证

远程访问必须使用认证 token。TLS 策略：

- localURL 可以支持 `ws://`，但仍必须认证。
- remoteURL 推荐并默认要求 `wss://`。
- 如果用户明确使用内网 VPN/Tailscale 的 `ws://` 地址，应在高级配置中允许，但显示安全提示。

不允许 remoteURL 绕过 Bridge v1 auth，也不允许把 pairing code 当长期远程访问密码。

### 11.3 FRP 配置边界

Mac App 可以提供 FRP 配置辅助，但不要在 v1 默认托管用户 VPS：

- 允许用户填入 VPS host、端口、token、subdomain 等配置。
- Mac App 生成本地 frp client 配置。
- Bridge UI 显示当前远程 URL 和连接状态。
- 所有配置必须由用户明确保存和启用。

如果后续做官方 relay，应作为独立产品阶段设计，不混进 v1 的用户自带 tunnel。

## 12. 迁移方案

### 12.1 Mac 端迁移

启动 CCCode Bridge.app 时检测：

- 是否已有旧 `go-bridge` 进程监听 `8777`。
- 是否存在旧 go-bridge 配置。
- 是否存在旧 cc-connect 本地配置。
- 是否存在 OpenCode/Codex/Claude Code 可用状态。

检测到旧服务时：

- 提示“检测到旧版 Bridge 服务”。
- 允许用户停止旧服务并迁移到 Mac App 管理。
- 不长期支持双 Bridge 并行。
- 迁移完成后由 Mac App 管理 runtime。

### 12.2 iOS 端迁移

旧 iOS 可能保存了 host/port/backend 配置。迁移策略：

- 识别可能的旧 Bridge endpoint。
- 如果 endpoint 是旧 Node.js UnifiedBridge 服务，提示用户在 Mac 上安装新版 CCCode Bridge。
- 如果 endpoint 实际是 go-bridge，但 iOS 侧只是旧 `UnifiedBridge` 命名，应走命名和认证模型迁移，不把它误判成废弃 backend。
- 不自动把旧 endpoint 当作已授权 Mac，因为旧链路没有新 device token。
- 允许用户重新扫码配对。
- 尽量保留本地消息缓存，但新 cache key 必须包含 `bridgeId/backendId/workspaceId/sessionId`。

### 12.3 命名迁移

用户可见文案：

- `Unified Bridge` -> `CCCode Bridge`
- `Thin Bridge` -> 不再出现
- `Server` -> `Mac`
- `Backend` 在用户界面尽量表达为 `Agent` 或具体工具名
- 代码里的 `UnifiedBridge*` 若仍承载 go-bridge 连接，应优先重命名或包裹迁移，避免后续开发者误以为 Node.js UnifiedBridge 仍是目标服务

代码迁移可以分阶段，但新文件、新协议、新文档不再引入旧命名。

## 13. 测试与验证策略

遵循本仓库约束：开发阶段默认不主动运行 UI tests、snapshot tests、simulator automation 或其他高消耗视觉验证流程。技术方案阶段的验证以静态检查、定向 build、定向 unit test 为主。

### 13.1 Go runtime

必须覆盖：

- pairing session 创建、过期、认领、批准、拒绝。
- manual code 错误和过期。
- device token 签发、hash 存储、认证成功、撤销失败。
- 未认证请求被拒绝。
- agent/provider descriptor 输出。
- version handshake。
- runtime config 读写。
- trusted devices store 迁移。
- Claude/OpenCode/Codex descriptor 能力差异。

建议命令：

```bash
cd go-bridge && go test ./... -count=1
```

如果发布态目录后续改名，再同步替换命令路径；这里的测试对象仍是 go-bridge 这一套唯一 backend。

### 13.2 iOS

必须覆盖：

- QR payload decode。
- manual code 输入校验。
- SavedBridgeStore persistence。
- BridgeCredentialStore Keychain wrapper。
- CCCodeBridgeModels Codable。
- request/response correlation。
- hello version mismatch。
- revoked token error mapping。
- offline snapshot key。
- legacy server config 迁移。

建议只跑定向 unit test，不默认跑 UI test：

```bash
xcodebuild -project OpenCodeiOS/CCCode.xcodeproj -scheme CCCode -destination 'platform=iOS Simulator,name=iPhone 17 Pro Max' test -only-testing:CCCodeTests/BridgePairingTests
```

如果后续必须跑真机或 UI 自动化，应先说明原因、范围和成本，再获得确认。

### 13.3 Mac App

必须覆盖：

- runtime 启动参数生成。
- runtime health 状态解析。
- stopped by user 不自动重启。
- crashed 后有限重启。
- sleep/wake 后状态刷新。
- pairing approve/reject 调用。
- trusted device revoke 调用。
- remote URL 配置保存。

Mac App 第一阶段可以先用 unit test 和小范围 integration test，不以 snapshot test 作为默认门槛。

## 14. 分阶段实施计划

Phase 依赖关系：

```mermaid
flowchart LR
    P0["Phase 0\n协议与仓库收敛"]
    P1["Phase 1\nMac App 管理 runtime"]
    P2["Phase 2\niOS 新配对与连接"]
    P3["Phase 3\n离线、异常与通知"]
    P4["Phase 4\n迁移与远程访问"]

    P0 --> P1
    P0 --> P2
    P1 --> P2
    P2 --> P3
    P2 --> P4
```

Phase 2 可以在 Phase 1 的 runtime 启动契约稳定后并行推进，不必等待 Mac App 所有页面完成。Phase 3 依赖 Phase 2 的 Saved Mac、事件流和 offline snapshot key。Phase 4 的远程访问依赖 Phase 2 的认证模型，但旧配置检测可以与 Phase 1 并行。

### Phase 0：协议与仓库收敛

目标：把 go-bridge 作为唯一 backend 的可发布边界定下来。

交付：

- 确定 `go-bridge` 产品态目录和可执行文件命名；可以保留目录名，也可以 rename，但不能引入第二套 backend。
- 移除对本地 `/Users/jacklee/Projects/cc-connect` 的发布依赖。
- 定义 `cccode-bridge-v1` Go/Swift schema。
- 定义 Bridge identity / trusted devices store。
- 明确旧 Node.js UnifiedBridge 已废弃，iOS `UnifiedBridge*` 只是历史命名迁移对象。

完成标准：

- `go.mod` 中没有指向本地路径的发布态 `replace` 指令。
- `cc-connect` 依赖来自明确 tagged version。
- `GOOS=darwin GOARCH=arm64 go build ./cmd/cccode-bridge-runtime` 或等价 go-bridge 产品态入口构建成功。
- `go test ./... -count=1` 通过。
- 协议名不再是 `unified-bridge`。
- Device token 格式、hash 规则、header 传输方式完成 Go/Swift schema 对齐。
- 未认证 WebSocket 连接被拒绝。
- Mac App 启动 runtime 的参数、ready frame、status 检测契约冻结。

### Phase 1：Mac App 管理 runtime

目标：用户打开 Mac App 即拥有 Bridge runtime。

交付：

- Apple Silicon Mac App shell。
- 内嵌 runtime 启动/停止。
- 管理接口。
- M1/M2/M3/M4/M4a/M5/M6 对应基础状态。
- agent/provider 检测展示。
- 菜单栏入口。

完成标准：

- 不需要用户手动启动 `go-bridge`。
- Bridge ready + no agents detected 能正确显示。
- runtime 崩溃和用户停止状态可区分。

### Phase 2：iOS 新配对与连接

目标：iOS 不再依赖旧 UnifiedBridge 命名和未认证连接模型，实际连接目标仍是 go-bridge。

交付：

- `Services/Bridge` 新模块。
- QR pairing。
- manual code pairing。
- Keychain device token。
- Saved Mac store。
- `CCCodeBridgeBackendClient` 接入现有上层。
- I1/I2/I2a/I3/I4/I5 基础链路。

完成标准：

- 扫码后 Mac 确认，iOS 保存授权。
- 重新打开 iOS 可自动连接已保存 Mac。
- 旧 UnifiedBridge 命名不再出现在新产品入口和新连接层。

### Phase 3：离线、异常与通知

目标：旁观长任务和异常断连体验可信。

交付：

- offline snapshot store。
- running session notification policy。
- Mac sleep/wake reconnect。
- session mid-run go-bridge restart / agent crash 展示。
- Claude Code 外部 turn polling 策略能力化。
- revoked token / Mac unreachable / Bridge crashed 错误映射。

完成标准：

- Mac 睡眠时 iOS 不误判为账户或配对失败。
- 运行中断连时用户能看到最后已知状态。
- Claude Code 进程退出能被表达为可恢复或已结束状态。

### Phase 4：迁移与远程访问

目标：老用户和外网连接进入 v1 闭环。

交付：

- 开发态 go-bridge 进程检测。
- 旧 iOS server config 迁移提示。
- remoteURL 配置。
- FRP 配置辅助。
- 远程连接状态与安全提示。

完成标准：

- 老用户不会继续维护开发态 go-bridge + cc-connect 双服务心智。
- 外网连接仍走同一 device auth。
- local/remote 当前连接来源在 iOS 清楚可见。

## 15. 风险与取舍

### 15.1 最大风险：把历史命名误认为当前架构

如果继续在 `UnifiedBridgeAdapter` 上追加配对、认证、设备管理和远程访问，但不更正它和旧 Node.js UnifiedBridge 的历史关系，会得到一个名字、协议、安全模型都不匹配的旧系统增强版。更糟的是，后续开发者会误以为还有一个和 go-bridge 并列的 UnifiedBridge backend。

取舍：围绕 go-bridge 新增或重命名为 CCCode Bridge v1 模块，多做一次 iOS 命名和认证迁移，但换来清晰的产品命名、安全边界和协议演进空间。

### 15.2 Mac App + Go runtime 双进程复杂度

双进程带来启动、日志、崩溃恢复、升级协调成本。

取舍：不重写 Go runtime，保留 agent 适配成熟度。复杂度集中在 lifecycle manager，风险比重写所有后端低。

### 15.3 local ws 与 remote wss 的安全差异

局域网 `ws://` 易于调试和连接，但远程必须更谨慎。

取舍：本地允许 `ws://` 但强制 token；远程默认要求 `wss://`，高级场景才允许用户明确启用非 TLS 内网隧道。

### 15.4 Claude Code 旁观体验天然受限

Claude Code 无共享服务广播，iOS 旁观 Mac 端 Claude Code 任务无法像 Codex/OpenCode 一样实时。

取舍：协议能力显式声明实时模型，iOS 按 capability 决定轮询和状态文案，不把 agent/provider 差异藏起来。

## 16. 待确认但不阻塞 Phase 0

这些问题不阻塞技术方案进入第一轮拆分，但需要在实现前记录成决策项：

- 发布名是否最终使用 `CCCode Bridge.app`。
- 第一版最低 macOS 版本。
- 第一版是否完全不提供 Intel build。
- Copilot 是否进入 v1 默认 agent/provider 列表。
- remoteURL 是否强制 `wss://`，还是允许高级用户 `ws://`。
- pairing session 默认有效期：建议 5 分钟。
- trusted devices store 使用 Keychain、Application Support 文件加密，还是二者组合。

## 17. 下一步建议

技术方案 v0 后，下一步不是直接大规模编码，而是把 Phase 0 拆成可评审的工程任务：

1. 写 `cccode-bridge-v1` 协议草案和 Go/Swift schema 对照表。
2. 制定 `go-bridge + cc-connect` 仓库收敛方案，消除本地 `replace` 发布依赖，并确认 go-bridge 是唯一 backend。
3. 制定 iOS `UnifiedBridge` 命名迁移清单，列出哪些代码只是改名、哪些协议字段要替换、哪些旧 Node.js 兼容可以删除。
4. 制定 Mac App runtime manager 技术 spike，验证签名后的 App 启动内嵌 Go runtime。

这四项完成后，项目就可以从“技术方案”进入“工程实施拆分”。

## 18. 评审建议采纳记录

本轮评审结论是技术方案可以进入 Phase 0。评审建议处理如下：

| 建议 | 处理 | 位置 |
|---|---|---|
| 将 §2 “go-bridge 是唯一 backend” 同步为开发者共识 | 采纳；继续保留在 §2，并在 Phase 0 完成标准中要求冻结 | §2、§14 |
| 补充 cc-connect 收敛路径 | 采纳；采用 tagged Go module 路径，去掉发布态本地 `replace` | §5.3 |
| 定义 Device token 格式 | 采纳；定义 `ccb1_` opaque random token、长度、hash 和 header 传输 | §8.6 |
| 定义 Mac App 启动 runtime 与通信契约 | 采纳；补充启动参数、ready frame、崩溃检测、日志契约 | §6.4 |
| 补充配对会话状态机 | 采纳；增加 Created/Claimed/Approved/Completed/Rejected/Expired 状态 | §8.7 |
| 补充 Mac 发现机制 | 采纳；v1 采用连接成功后地址更新，Bonjour 作为 v1.1 候选 | §7.6 |
| 补充 WebSocket 重连协议 | 采纳；重连必须重新 hello，飞行请求失败，不自动重放副作用请求 | §9.6 |
| 补充数据存储位置 | 采纳；统一到 `~/Library/Application Support/CCCode Bridge/` | §6.3 |
| 补充错误码命名空间 | 采纳 | §9.7 |
| 补充事件类型命名空间 | 采纳 | §9.8 |
| 补充 iOS 本地通知实现 | 采纳 | §10.6 |
| 补充本地网络权限 | 采纳 | §10.7 |
| Phase 0 完成标准 checklist 化 | 采纳 | §14 |
| Phase 依赖关系显式声明 | 采纳 | §14 |

未采纳项：

| 建议/可选路径 | 不采纳理由 |
|---|---|
| 第一阶段直接复制 `cc-connect` 代码到 go-bridge `internal/` | 不采纳。复制会制造版本分叉，导致 agent/core 修复需要双处维护。Phase 0 先采用 `cc-connect tagged version -> go-bridge go.mod` 的收敛方式；只有 tagged module 无法满足发布、签名或合规要求时，才重新评估 vendoring/internal copy。 |
| 第一版使用局域网 IP 段扫描寻找 Mac | 不采纳。扫描体验不可控、权限提示更重、容易被网络设备拦截，失败原因也难向用户解释。v1 先用 QR localURL + 成功连接后地址更新；Bonjour/mDNS 作为 v1.1 候选增强。 |

### 18.1 修订版评审建议采纳记录

修订版评审结论是技术方案可以进入 Phase 0，无附加条件。剩余 5 个细节均已采纳：

| 建议 | 处理 | 位置 |
|---|---|---|
| Runtime 启动失败的错误帧格式 | 采纳；补充 `runtime_error` frame 和常见错误映射 | §6.4 |
| Runtime 优雅停止契约 | 采纳；补充 `/internal/shutdown`、SIGTERM、等待和强杀顺序 | §6.4 |
| 内嵌 Go binary 签名流程 | 采纳；补充 codesign binary -> codesign bundle -> notarytool submit 顺序 | §6.5 |
| WebSocket ping/pong keepalive | 采纳；补充 30 秒 ping、连续 3 次失败触发重连 | §9.6 |
| 事件类型增加 `message.started` | 采纳；补入 Bridge v1 事件类型表 | §9.8 |

本轮没有不采纳建议。
