# Unified Bridge 接入指南

iOS app 通过 Unified Bridge（Node.js）统一对接 Claude、OpenCode、Codex、Copilot 四个后端。iOS 端只添加一个 Bridge endpoint；可用 backend 由 `register_ack.backends[]` 发现并生成 target，聊天页再在同一个 Bridge 下切换 backend。

## 架构

```
iOS App  ←── WebSocket ──→  Bridge (Node.js)  ←──→  OpenCode / Codex / Copilot / Claude
           ws://host:8777                       各后端独立端口或进程
```

- iOS 不直连任何后端，全部经 Bridge 中转
- Bridge 运行在 Mac 上，由 `launchctl submit` 管理
- 每个后端 driver 支持 lazy activation（首次请求时才激活）

### Bridge 内部架构

```
bridge/src/
├── start-unified.mjs           # 启动入口，解析环境变量，注册 driver
├── unified-server.mjs          # WebSocket 服务器，RPC 路由，method dispatch
├── live.mjs                    # LiveSessionManager（后台 session 管理）
├── bridge.mjs                  # Claude Agent SDK facade
├── blocks.mjs                  # Claude message block 拆分与重装
├── merge.mjs                   # Sidecar metadata 合并
├── config.mjs                  # 配置读写（model、permission、effort）
│
├── core/                       # 共享核心模块
│   ├── normalized-runtime.mjs  # NormalizedRuntimeEvent → wire event 适配
│   ├── streaming-reducer.mjs   # 共享流式文本 reducer（累积/去重/rollback）
│   ├── session-binding.mjs     # 共享 session rebind（tempId → realId）
│   ├── runtime-transcript-replayer.mjs  # transcript raw → normalized → wire 回放
│   ├── event-buffer.mjs        # 事件缓冲与序号管理
│   ├── permission-store.mjs    # 权限状态存储
│   └── session-store.mjs       # session 状态存储
│
├── backends/                   # 后端 driver 实现
│   ├── base.mjs               # BaseDriver / BaseBackendDriver 基类
│   ├── opencode.mjs           # OpenCode 入口（选 HTTP 或 CLI）
│   ├── opencode-http.mjs      # OpenCode HTTP driver（SSE 事件流）
│   ├── opencode-cli.mjs       # OpenCode CLI driver
│   ├── claudecode.mjs         # Claude Code driver（Agent SDK）
│   ├── codex.mjs              # Codex 入口（选 AppServer 或 CLI）
│   ├── codex-appserver.mjs    # Codex AppServer driver
│   ├── codex-appserver-runtime.mjs  # Codex 运行时通知解析
│   ├── codex-cli.mjs          # Codex CLI driver
│   └── copilot.mjs            # Copilot ACP driver
│
└── protocol/                   # 协议层
    ├── router.mjs             # RPC 路由 + capability 门禁
    ├── schemas.mjs            # 消息 JSON schema
    ├── types.mjs              # TypeScript 风格类型定义
    ├── events.mjs             # 事件总线（emit / buffer / dispatch）
    └── errors.mjs             # 错误类型定义
```

### 事件处理管线

```
后端原始事件（各 driver 格式不同）
  → driver._emitNormalized({ kind, mode, text, ... })     # driver 内部
  → core/normalized-runtime.mjs                            # 映射为 wire event name
  → protocol/events.mjs EventBuffer.emit()                 # 加序号、缓冲
  → unified-server.mjs → client.ws.sendJson()              # 推送到 iOS
```

wire event 名称由 `kind + mode` 组合：
- `text_delta` / `text_updated` / `text_finished`
- `reasoning_delta` / `reasoning_updated` / `reasoning_finished`
- `tool_step_started` / `tool_step_updated` / `tool_step_finished`
- `session_identified` / `session_state_updated`
- `turn_started` / `turn_completed`

## 快速开始

### 1. 启动 Bridge

```bash
cd bridge
npm install

# 前置：将认证信息存入 ~/.zshenv（一次性）
cat >> ~/.zshenv << 'EOF'
export OPENCODE_SERVER_USERNAME=opencode
export OPENCODE_SERVER_PASSWORD=<your-password>
EOF

# 启动全部 4 个后端（推荐，认证从环境变量读取）
UNIFIED_BRIDGE_DRIVERS=claude,opencode,codex,copilot \
node src/start-unified.mjs

# 只启动部分后端
UNIFIED_BRIDGE_DRIVERS=opencode,codex \
node src/start-unified.mjs

UNIFIED_BRIDGE_DRIVERS=claude node src/start-unified.mjs
```

> **注意**: 如果不设 `UNIFIED_BRIDGE_DRIVERS`，默认只启动 `claude` 一个 driver。

启动成功后应看到：

```
[unified-bridge] Registered driver: opencode (opencode)
[unified-bridge] Registered driver: codex (codex)
[unified-bridge] Registered driver: claude (claude_code)
[unified-bridge] Registered driver: copilot (copilot)
[unified-bridge] Driver started: opencode
[unified-bridge] Driver started: codex
[unified-bridge] Driver started: claude
[unified-bridge] Driver started: copilot
[unified-bridge] Listening on ws://0.0.0.0:8777
```

### 2. iOS 端添加 Bridge

在 iOS app 中打开 Settings，选择 **Add Bridge Manually**。

| 字段 | 值 |
|------|----|
| Host | Mac 局域网 IP（`ipconfig getifaddr en0`） |
| Port | `8777` |
| Username / Password | 如 Bridge 启用了认证，填写对应凭据 |

点击 **Add Bridge** 后，iOS 自动发现所有 backend target：

```
MacBook Bridge                    192.168.1.100:8777 / jack / 4 backend target(s)
  ├── OpenCode
  ├── Codex
  ├── Claude Code  (active)
  └── Copilot
```

### 3. 连接与切换 Backend

点击任一 target 连接。聊天页右上角出现 backend 切换按钮，可在同一 Bridge 下的不同 backend 之间切换。

### 4. Bridge 管理（macOS launchctl）

真机长期调试建议用 `launchctl submit` 常驻：

```bash
# 前置：将认证信息存入 ~/.zshenv（一次性）
cat >> ~/.zshenv << 'EOF'
export OPENCODE_SERVER_USERNAME=opencode
export OPENCODE_SERVER_PASSWORD=<your-password>
EOF

# 停止旧实例
launchctl remove com.opencode.unifiedbridge 2>/dev/null || true
: > /tmp/unified-bridge.log

# 启动（zsh -lc 自动 source ~/.zshenv，无需在命令中指定密码）
launchctl submit -l com.opencode.unifiedbridge \
  -o /tmp/unified-bridge.log \
  -e /tmp/unified-bridge.log \
  -- /bin/zsh -lc 'cd /Users/jacklee/Projects/opencodeIosNew/bridge && \
     UNIFIED_BRIDGE_DRIVERS=claude,opencode,codex,copilot \
     exec /opt/homebrew/bin/node src/start-unified.mjs'
```

> **注意**: 密码存放在 `~/.zshenv` 中，launchctl 的 `zsh -lc` 会自动加载。不要将密码提交到仓库。

常用命令：

| 操作 | 命令 |
|------|------|
| **启动** | 上面的 `launchctl submit` 命令 |
| **重启** | `launchctl remove com.opencode.unifiedbridge`，再执行启动命令 |
| **停止** | `launchctl remove com.opencode.unifiedbridge` |
| **查看状态** | `lsof -nP -iTCP:8777 -sTCP:LISTEN` |
| **查看日志** | `tail -f /tmp/unified-bridge.log` |

注意事项：

- `launchctl submit` 创建的是临时任务，Mac 重启后需重新执行
- Bridge 默认监听 `ws://0.0.0.0:8777`，iPhone 应填 Mac 局域网 IP
- 修改 `UNIFIED_BRIDGE_DRIVERS`、auth、端口后需重启 Bridge，并在 iOS 删除旧 Bridge 重新 Add Bridge
- `/tmp/unified-bridge.log` 同时接收 stdout/stderr

#### iOS 端管理

| 操作 | 说明 |
|------|------|
| **删除 Bridge** | Settings → Bridge 分组 → Delete Bridge。仅删除 iOS 本地数据 |
| **重新发现** | Bridge 重启或 driver 变更后，删除旧 Bridge 再重新 Add Bridge |
| **断连重连** | 聊天页自动按指数退避重连，重连后重新 register 并刷新 backend target |

### 内部流程

```
用户添加 Bridge
  → ServerViewModel.addBridge()
  → BridgeDiscoveryService.discover()
  → Transport 建立 WebSocket
  → 发送 register（含 Authorization header）
  → Bridge 返回 register_ack（含 backend 列表）
  → iOS 按 backend kind/id 创建 target
  → Settings 按 Bridge 分组展示
  → 用户连接 target
  → BackendClientFactory 创建 UnifiedBridgeAdapter
  → 后续请求经 Bridge 路由到目标后端
```

## 后端映射

| iOS BackendKind | Bridge kind / backendId | 说明 |
|-----------------|-------------------------|------|
| `.openCode` | `opencode` / `opencode` | OpenCode HTTP 或 CLI |
| `.codex` | `codex` / `codex` | Codex AppServer 或 CLI |
| `.claudeCode` | `claude_code` / `claude` | Claude Agent SDK |
| `.copilot` | `copilot` / `copilot` | GitHub Copilot ACP |
| `.thinBridge` | `claude_code` / `claude` | 兼容旧配置；新建 Bridge 优先生成 `.claudeCode` |

## 环境变量

### Bridge 服务器

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `UNIFIED_BRIDGE_PORT` | `8777` | Bridge WebSocket 监听端口 |
| `UNIFIED_BRIDGE_DRIVERS` | `claude` | 启用的 driver，逗号分隔 |
| `UNIFIED_BRIDGE_BACKEND_IDLE_TIMEOUT_MS` | `300000` | 后端空闲超时（毫秒） |

### OpenCode

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `UNIFIED_BRIDGE_OPENCODE_RUNTIME` | `http` | 运行模式：`http` 或 `cli` |
| `OPENCODE_BASE_URL` | `http://localhost:64667` | OpenCode HTTP 服务地址 |
| `OPENCODE_SERVER_USERNAME` | — | HTTP Basic auth 用户名 |
| `OPENCODE_SERVER_PASSWORD` | — | HTTP Basic auth 密码 |

- **HTTP 模式**：功能完整（sessions、models、todos、permissions、SSE 事件流）。需 OpenCode 服务先行启动。Session 列表已自动过滤子 agent session（有 `parentID` 的不返回）。
- **CLI 模式**：功能子集（send、list sessions、models、agents）。

### Codex

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `UNIFIED_BRIDGE_CODEX_RUNTIME` | `app_server` | 运行模式：`app_server` 或 `cli` |
| `CODEX_WS_URL` | `ws://localhost:4141` | Codex AppServer WebSocket 地址 |

- **AppServer 模式**：功能完整（sessions、threads、model switching、images）。时间戳自动从秒转换为毫秒。
- **CLI 模式**：功能子集（send、list sessions）。

### Copilot

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `UNIFIED_BRIDGE_COPILOT_RUNTIME` | `acp_lazy` | 激活模式：`acp_lazy` 或 `acp_eager` |
| `COPILOT_WS_URL` | `ws://localhost:8875` | Copilot ACP WebSocket 地址 |
| `COPILOT_REST_URL` | `http://localhost:8876` | Copilot REST API 地址 |
| `UNIFIED_BRIDGE_COPILOT_SIDECAR_CMD` | — | Copilot sidecar 启动命令（可选） |

### Claude

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `UNIFIED_BRIDGE_CLAUDE_RUNTIME` | `sdk` | 运行模式，目前仅支持 `sdk` |

Claude 使用 Anthropic Agent SDK 直接调用，无需额外服务。

## 运行模式对比

| | HTTP / AppServer | CLI |
|---|---|---|
| 前提 | 后端服务已运行 | 后端二进制在 PATH |
| 功能 | 完整 | 子集 |
| 性能 | 常驻连接，低延迟 | 每次 request 启动子进程 |
| 适用 | 长期运行 | 开发测试 / 无服务场景 |

## 已知限制与注意事项

1. **Claude Code 长对话历史加载**：`getSessionMessages` 需从 SDK 读取完整 JSONL transcript，长 session 可能需要 30 秒以上。iOS 端 `get_session_messages` 超时设为 60 秒。
2. **子 agent session 过滤**：OpenCode HTTP/CLI driver 的 `listSessions` 已过滤有 `parentID` 的子 agent session，只返回主 agent session。
3. **Codex 时间戳**：Codex app-server 返回 Unix 秒级时间戳，Bridge `_toEpochMs()` 自动转换为毫秒。
4. **图片支持**：模型不支持图片输入时会正确报错（如 `this model does not support image input`），这是预期行为。

## 连接排错

### Bridge 启动日志

```
[unified-bridge] Listening on ws://0.0.0.0:8777
[unified-bridge] Registered driver: opencode (opencode)
[unified-bridge] Registered driver: codex (codex)
[unified-bridge] Registered driver: claude (claude_code)
[unified-bridge] Registered driver: copilot (copilot)
```

确认 driver 注册成功且端口正确。

### iOS 连接失败

1. **确认 Bridge 在运行**：`lsof -nP -iTCP:8777 -sTCP:LISTEN` 应看到 Node 监听
2. **确认 driver 列表正确**：日志中 `Registered driver` 行数应与预期一致
3. **确认 iOS 端口正确**：Add Bridge 填写的端口必须与 `UNIFIED_BRIDGE_PORT` 一致
4. **确认后端服务可用**：
   - OpenCode HTTP：`curl -u <user>:<pass> http://localhost:64667/global/health`
   - Codex AppServer：`curl http://localhost:4141`
5. **查看 Bridge 日志**：`tail -f /tmp/unified-bridge.log`

### register_ack 返回空 backends

`UNIFIED_BRIDGE_DRIVERS` 未包含任何 iOS 支持的 driver，或 driver 启动失败。

### Add Bridge 后只发现 1 个 backend

最常见原因：未设 `UNIFIED_BRIDGE_DRIVERS`，默认只有 `claude`。

**修复**：
1. 停掉 Bridge：`launchctl remove com.opencode.unifiedbridge`
2. 带完整 driver 列表重启
3. iOS Settings 中删除旧 Bridge → 重新 Add Bridge

### Bridge 断连后 selector 不可用

断连期间 backend selector 只读/禁用。重连后自动 register + 刷新 target。不再报告的 stale target 被自动清理。

### Bridge 重启后 iOS 端如何更新

Bridge 重启后 epoch 变化，iOS 下次连接时自动触发重新 register。也可手动删除旧 Bridge 再 Add Bridge。

## 相关文件

| 文件 | 作用 |
|------|------|
| `bridge/src/start-unified.mjs` | Bridge 启动入口 |
| `bridge/src/unified-server.mjs` | WebSocket 服务器，消息路由 |
| `bridge/src/core/normalized-runtime.mjs` | NormalizedRuntimeEvent → wire event |
| `bridge/src/core/streaming-reducer.mjs` | 共享流式文本 reducer |
| `bridge/src/core/session-binding.mjs` | 共享 session rebind |
| `bridge/src/core/runtime-transcript-replayer.mjs` | transcript 回放器 |
| `bridge/src/backends/opencode-http.mjs` | OpenCode HTTP driver |
| `bridge/src/backends/opencode-cli.mjs` | OpenCode CLI driver |
| `bridge/src/backends/claudecode.mjs` | Claude Code driver（Agent SDK） |
| `bridge/src/backends/codex-appserver.mjs` | Codex AppServer driver |
| `bridge/src/backends/copilot.mjs` | Copilot ACP driver |
| `OpenCodeiOS/.../UnifiedBridgeAdapter.swift` | iOS 统一适配器 |
| `OpenCodeiOS/.../UnifiedBridgeTransport.swift` | iOS WebSocket 传输层 |
| `OpenCodeiOS/.../UnifiedBridgeModels.swift` | iOS 协议数据模型 |
| `OpenCodeiOS/.../BridgeDiscoveryService.swift` | iOS Bridge register/discover 服务 |
| `OpenCodeiOS/.../ServerViewModel.swift` | iOS 连接管理 |
