# go-bridge 框架现状

> **文档定位**：本文档记录 go-bridge 的架构设计、与 Node.js Unified Bridge 的差异、已知偏差和决策约束。
> 任何修改 go-bridge 的开发者必须先读本文档和 `转向 cc-connect 路线的原因.md`，避免重蹈覆辙。

## 1. 起源

go-bridge 是因为 Node.js Unified Bridge（`bridge/`）不稳定才创建的替代方案。Node.js bridge 的四个核心病灶（详见 `转向 cc-connect 路线的原因.md`）：

1. **没有统一 shutdown 协调** — launchctl 杀进程来不及 flush
2. **事件丢失** — 多层异步管线交接处丢事件
3. **时序/绑定问题** — tempId → realId rebind、一个 session 多个进程
4. **没有错误恢复** — catch 分支吞事件，没有统一恢复栈

go-bridge 的设计目标：用 cc-connect（Go）做后端，go-bridge 只做薄 WebSocket 适配层。

## 2. 架构总览

```
iOS App (SwiftUI)
    │ WebSocket
    ▼
go-bridge (Go, port 8777)           ← 本文档的主角
    │
    ├── Claude Code backend
    │   ├── send/resume → cc-connect claudecode AgentSession
    │   ├── list_sessions → go-bridge 扫描 ~/.claude/projects（跨项目视图）
    │   └── history/todos/memory/usage/diagnostics/mutation → cc-connect provider
    │
    ├── Codex backend
    │   └── cc-connect core.Agent → exec.Command 子进程管理
    │       exec 或 app_server session → chan Event → relayEvents → WebSocket
    │
    ├── codex app-server 被动订阅
    │   └── 连接共享 ws://localhost:4141 → core.Event → broadcaster
    │
    └── opencode backend（混合路径，proxy 已收窄）
        │
        ├── 已迁移路径（走 cc-connect generic handler）
        │   send_message → cc-connect opencodeSession → `opencode run --session`
        │   list_sessions / get_session_messages / fetch_todos / list_agents
        │   list_models / switch_model / delete_session / diagnostics 等
        │
        └── proxy-only 路径（cc-connect 尚未覆盖）
            get_session / list_projects / create_session / resume_session
            send_message / abort_generation
            → OpenCodeProxy → 直接调 opencode HTTP API (port 64667)
```

**宿主与运行集成关系：**
在产品化发布中，Go Runtime 二进制打包为 `cccode-bridge-runtime` 放在 `CCCodeBridge.app` 的 `Resources` 内。
- 由 SwiftUI 宿主 `CCCodeBridge` (`RuntimeManager.swift`) 以子进程方式调起并看护。
- 宿主生成一个唯一的 `management-token` 传递给 Go Runtime。
- Go Runtime 成功监听并就绪后，会在 Data 目录生成 `runtime.json`（写入其 `pid` 与 `managementUrl`），宿主监测到此文件后与 Go 服务的 HTTP management API 进行通信握手，从而拉取代理的状态并展示在 macOS 菜单栏。

## 3. 文件结构

| 文件 | 职责 | 行数 |
|------|------|------|
| `main.go` | 入口：flag 解析、agent 注册、passive subscription、signal handler、HTTP server 启动 | ~240 |
| `server.go` | WebSocket server：upgrade、消息路由（register/request/ping）、conn 写锁、ping/pong | ~200 |
| `handlers.go` | RPC handler 分发：generic handler、opencode proxy-only、session registry/broadcast 接入 | ~2200 |
| `events.go` | cc-connect `core.Event` → iOS wire format 映射（text_delta/tool_started/turn_completed 等） | ~125 |
| `types.go` | Wire protocol、session registry、broadcaster、订阅 key | ~390 |
| `opencode-proxy.go` | opencode HTTP API 客户端：直接调 localhost:64667 的 REST 端点 | ~600 |
| `provider_switch.go` | 从 cc-connect 配置加载 provider seed，并映射到 bridge wire payload | ~200 |

## 4. 关键设计决策

### 4.1 事件管道：两层同步链

```
cc-connect readLoop (goroutine)
    → chan core.Event (buffered 64)
        → go-bridge relayEvents (goroutine)
            → conn.SendJSON (mutex-protected WebSocket write)
```

**对比 Node.js bridge**：Node.js 有 SDK raw → `_runStreamLoop` → `_emitNormalized` → `eventBuffer` → WebSocket 五层，每层都是异步的。go-bridge 只有两层，且 `range chan` 是同步消费。

### 4.2 Session 管理：registry + pending rebind

```go
sessionRegistry  // sessionID/pendingID → trackedSession
```

- Codex lazy create 会先返回 `pending-*`，真实 thread id 出现在后续事件里
- `relayEvents` 识别真实 id 后，会同时 rebind session registry 和 broadcaster subscription key
- `ensureOpenCodeSession` 在 model/dir 变化时 close 旧 session + 创建新 session
- abort 后清理 registry，下次 send 自动重建
- idle session 会按 backend TTL 清理，pending/running session 不会被 cleanup 误关

**对比 Node.js bridge**：Node.js 的 `_sessions` Map 存 state，session 和进程关系松耦合，resumeSession 可能创建多余进程。

### 4.3 Shutdown：Go 标准信号处理

```go
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
go func() {
    sig := <-sigCh
    cancel()
    httpServer.Shutdown(context.Background())
}()
```

**对比 Node.js bridge**：Node.js bridge 的 shutdown 函数注册了但 launchctl 不给执行机会。

### 4.4 错误恢复

`relayEvents` 在 `EventResult.Done` 或 `EventError` 时：
1. 发 `session_state_changed: idle`
2. break 退出循环

`ocHandleAbortGeneration`：
1. HTTP abort（通知 opencode 服务端停止）
2. cc-connect session Close（清理本地进程）
3. 从 registry 删除
4. 发 turn_completed + idle

**对比 Node.js bridge**：`_runStreamLoop` 的 catch 分支可能吞事件，没有统一恢复栈。

### 4.5 多客户端广播

go-bridge 现在维护 `backendID + sessionID + directory` 维度的订阅表：

- `send_message`、`get_session`、`get_session_messages`、`resume_session` 都会把当前连接订阅到对应 session
- 同一 session 的多个 iPhone/iPad/macOS 客户端可以同时收到 stream、tool、todos、state 事件
- 发送方只通过 broadcaster 收事件，避免“直接写 conn + 广播”造成双份事件
- directory-scoped 和 directory-less subscription 做兼容匹配，广播目标用 `map[*Conn]struct{}` 去重
- WebSocket 连接关闭时会 `UnsubscribeAll`，避免断线客户端继续占订阅表

### 4.6 Codex app-server 被动订阅

Codex app-server 是共享服务模型。go-bridge 在 `codex app_server` 模式下会通过 `core.EventSubscriber` 连接已配置的共享 app-server URL，而不是再启动一个竞争性的 `codex app-server` 进程。

- 被动订阅使用 bridge lifecycle context，SIGTERM/SIGINT 会随 HTTP server 一起退出
- initialize 会等待 JSON-RPC response，握手失败会明确返回错误
- app-server 尚未启动或重启断线时，bridge 会带 backoff 自动重连
- macOS 端发起的 Codex turn 可通过 broadcaster 推给已订阅同一 thread 的 iOS 客户端

### 4.7 Claude/Codex 进入 go-bridge 后的能力边界

这两天 Claude Code 和 Codex 已经不再只是“能 send_message 的 backend”，而是通过 cc-connect optional interfaces 暴露出更完整的 bridge 能力。go-bridge 的原则是：能力由 agent 是否实现接口决定，`BackendList()` 只按接口暴露 capability，不做假阳性。

**Claude Code 当前路径**：

- `create_session` / `send_message` 通过 cc-connect `claudecode.Agent` 创建 CLI session
- `resume_session` 不立即启动 Claude 进程，只建立 watch 订阅；真正恢复延迟到下一次 `send_message`
- `list_sessions` 在 go-bridge 侧扫描 `~/.claude/projects`，支持无 directory 时跨项目聚合，并过滤隐藏/系统项目
- `get_session_messages` 优先走 cc-connect `RichHistoryProvider`，保留 tool step、file、large output content ref
- `fetch_todos` 读取 JSONL 中最后一条 TodoWrite，作为 todo dock 权威状态
- `list_memory_files` / `read_memory_file` 暴露 project/global `CLAUDE.md`
- `get_usage` 聚合 Claude JSONL token usage
- `run_diagnostics` 流式返回 CLI、session_start、model_query、credentials 检查
- `rename_session` / `archive_session` 写入 sidecar，`delete_session` 删除 JSONL 和 sidecar
- provider switch 已接入 cc-connect provider config，新 session 生效

**Codex 当前路径**：

- 支持 `exec` 和 `app_server` 两种 backend mode
- `app_server` 模式下 `create_session` 是 lazy create：先返回 `pending-*`，第一次 send 后 rebind 到真实 thread id
- `list_sessions` / `get_session_messages` / `delete_session` 读取 `codex_home` 下 rollout 文件
- `fetch_todos` 从 Codex rollout 的最新 plan snapshot 读取 todo
- `list_memory_files` / `read_memory_file` 暴露 project/global `AGENTS.md`
- `run_diagnostics` 覆盖 CLI、auth、workdir、app-server connectivity，并尊重 `codex_home`
- provider switch 会把 cc-connect provider config 写入 Codex config/auth，新 session 生效
- `app_server` session 支持 context compression，bridge 只在 Codex app-server mode 暴露 `compression`

## 5. opencode 混合路径详解

### 5.1 走 cc-connect generic handler 的操作（含 Claude/Codex/OpenCode）

| 方法 | 实现 | Claude | OpenCode | Codex |
|------|------|--------|----------|-------|
| `send_message` | `agent.StartSession(sessionID)` → `sess.Send(content)` → `go relayEvents(...)` | ✅ | ✅ | ✅ |
| `list_sessions` | `agent.ListSessions()` | ✅ | ✅ | ✅ |
| `get_session_messages` | `RichHistoryProvider` 优先，降级 `HistoryProvider` | ✅ | ✅ | ✅ |
| `fetch_todos` | `agent.(TodoProvider).FetchTodos()` | ✅ | ✅ | ✅ |
| `list_agents` | `agent.(AgentLister).ListAgents()` | - | ✅ | - |
| `list_models` / `switch_model` | `ModelSwitcher` / reasoning effort switcher | ✅ | ✅ | ✅ |
| `list_providers` / `set_provider` | `ProviderSwitcher` | ✅ | ✅ | ✅ |
| `delete_session` | `agent.(SessionDeleter).DeleteSession()` | ✅ | ✅ | ✅ |
| `run_diagnostics` | `agent.(DiagnosticsProvider).RunDiagnostics()` | ✅ | ✅ | ✅ |
| `list_memory_files` / `read_memory_file` | `MemoryFileReader` | ✅ | - | ✅ |
| `get_usage` | `TokenUsageReporter` | ✅ | backend-dependent | - |
| `rename_session` / `archive_session` | `SessionRenamer` / `SessionArchiver` | ✅ | - | - |
| `compress_context` | active session `ContextCompactingSession` | active-session dependent | ✅ | app-server ✅ |

### 5.2 仍走 HTTP proxy 的操作（proxy-only）

| 方法 | 实现 |
|------|------|
| `create_session` | `ocProxy.createSession()` → POST /session |
| `resume_session` | `ocProxy.getSession()` → GET /session/{id} |
| `get_session` | `ocProxy.getSession()` → GET /session/{id} |
| `list_projects` | `ocProxy.listProjects()` → GET /project |
| `send_message` | 仍需要 proxy 协助处理 OpenCode server session 语义 |
| `abort_generation` | HTTP abort + cc-connect session Close |

### 5.3 为什么读路径绕过 cc-connect

cc-connect 的 opencode agent 已覆盖大多数读路径，但仍不是 OpenCode HTTP server 本身。以下能力仍留在 proxy：

- **project/session create/resume/get 的 OpenCode server 语义** — 仍由 OpenCode HTTP API 持有
- **abort 的 server-side 取消语义** — 需要直接通知 OpenCode HTTP server
- **send_message 的 server session 协调** — 仍处于 hybrid 状态，后续应在 cc-connect 明确职责边界后再下沉

### 5.4 偏差的影响

- **短期**：功能正常，iOS 端可以正常使用
- **长期**：仍有 6 个 proxy-only 方法，需要继续收敛到 cc-connect 或明确保留边界
- **正确方向**：先在 cc-connect 层补齐 project/session create/resume/abort 等职责，再删除对应 proxy 路径

## 6. 2026-05-06 优化后的真机可见提升

这一轮优化后，真机测试能直接感知到的变化如下：

1. **WebSocket 长连接更稳**
   - `Conn.SendJSON` 使用写锁，stream、result、broadcast 并发写不会互相打断
   - server 会定时 ping，90 秒无 pong 自动关闭连接
   - 连接关闭时清理 broadcaster 订阅，减少僵尸连接和重复推送

2. **同 session 多设备旁观**
   - 同一个 backend/session 可以被多个连接订阅
   - macOS/iPhone/iPad 打开同一 session 时，事件会广播给所有订阅者
   - 广播目标去重，directory-less 和 directory-scoped 订阅并存时不会收到双份事件

3. **Codex app-server 旁观 macOS turn**
   - go-bridge 可被动连接共享 Codex app-server
   - macOS 端发起的 Codex turn，会经 passive subscription 转成 bridge event
   - iOS 打开对应 Codex thread 后，可以收到 text/tool/todos/state 更新
   - app-server 晚启动或重启时，bridge 会自动重连

4. **pending session rebind 更可靠**
   - Codex `create_session` 返回的 `pending-*` 会在真实 thread id 出现后自动 rebind
   - broadcaster subscription key 同步更新
   - `get_session`、`get_session_messages`、`resume_session` 都会先解析 pending id，再建立 watch 订阅

5. **Session idle cleanup**
   - idle session 会按 backend TTL 定期清理
   - running/pending session 不会被误清理
   - Codex TTL 更长，给 context compression、pending rebind、passive subscription 留出恢复窗口

6. **OpenCode 已迁移能力更多**
   - `list_sessions`、`get_session_messages`、`fetch_todos`、`list_agents`、`list_models`、`delete_session`、`run_diagnostics` 等已走 cc-connect generic handler
   - OpenCode directory context 会在 generic dispatch 前切换，降低多项目串读风险
   - proxy 只保留 cc-connect 尚未覆盖的 OpenCode server 专属职责

7. **Diagnostics 更适合真机排查**
   - Claude Code/OpenCode/Codex 均有 diagnostics
   - Codex auth 检查尊重配置的 `codex_home`
   - 可以区分 CLI/auth/workdir/app-server 连接问题和 iOS UI/transport 问题

8. **Claude/Codex 管理能力补齐**
   - Claude Code 现在能在 go-bridge 下暴露 memory、usage、diagnostics、rename/archive/delete、todos、rich history
   - Codex 现在能暴露 provider switch、memory、diagnostics、todos、session history/delete，以及 app-server compression/passive watch
   - iOS 端不需要再把 Claude/Codex 当成“只能发送消息”的简化 backend

## 7. go-bridge 不继承的 Node.js Bridge 缺陷

| 病灶 | Node.js Bridge | go-bridge |
|------|---------------|-----------|
| shutdown 协调 | 无：launchctl SIGTERM 硬杀 | 有：signal.Notify + httpServer.Shutdown() |
| 事件丢失 | 五层异步管线，交接处丢事件 | 两层同步链，chan + mutex write |
| session 绑定 | 松耦合：tempId rebind，多进程 | registry：pending/real ID rebind 明确建模 |
| 错误恢复 | catch 吞事件，无恢复栈 | EventResult/EventError → idle + break |
| 能力暴露 | 前端/bridge 手写矩阵，容易过期 | 按 cc-connect optional interface 推导 capability |

## 8. go-bridge 自身的风险点

### 8.1 WebSocket 连接管理层

cc-connect 本身不需要 WebSocket（它是后端，平台是消费者）。go-bridge 多了一层 WebSocket 管理（upgrade、ping/pong、conn 写锁）。这层比 Node.js bridge 简单得多（没有多 backend 抽象、没有 eventBuffer、没有 subscriber 竞争），但仍然是额外的失败面。

### 8.2 opencode create/resume 不经过 cc-connect 进程管理

Claude Code 的 create/send 由 cc-connect 进程管理；Codex lazy create 会先返回 `pending-*`，到 send 时再进入 cc-connect session。OpenCode 的 create/resume 仍走 HTTP proxy，这意味着 OpenCode session 创建不受 cc-connect 的进程生命周期管理。

### 8.3 OPENCODE_SERVER_USERNAME/PASSWORD 的清理与动态重载

`main.go` 在 flag 解析后立即 unset 这两个环境变量，防止子进程 `opencode run` 继承。这是刻意的设计。
在 `CCCodeBridge.app` 托管模式下，当在设置中修改凭据或重启 Bridge 时，Swift 宿主主程序会重新从持久化的 `credentials.json` 中读取内容，通过新建 `Process` 并重新注入环境变量与 CLI 参数的方式启动 `cccode-bridge-runtime` 子进程，从而自然地满足了动态重载与注入的需要。

### 8.4 Claude Code TodoProvider（2026-05-05 新增）

Claude Code 此前不支持 `fetch_todos`，因为 cc-connect 的 Claude Code agent 没有实现 `core.TodoProvider` 接口。iOS 端打开 macOS Claude Code 执行的会话时，看不到 todo dock，也无法自动刷新。

**修复**：

- **cc-connect 层**：在 `agent/claudecode/claudecode.go` 中实现 `FetchTodos` 方法，读取 session JSONL 文件，倒序查找最后一条 TodoWrite 工具调用，提取完整 todo 列表返回
- **Node.js bridge 层**：在 `bridge/src/backends/claudecode.mjs` 中新增 `fetchTodos` 方法（从 `getSessionMessages` 中提取 TodoWrite 步骤的 todoItems），并在 capabilities 中声明 `todos`；同时在 `_handleFinal` 中检测 TodoWrite 步骤时主动 emit `todos_updated` 事件，使 iOS 自己发起的 turn 也能实时更新 todo dock
- **设计依据**：Claude Code 的 TodoWrite 每次发送完整列表，因此最后一条 TodoWrite 即为权威状态。cc-connect 实现直接读 JSONL 文件，不依赖 SDK 或进程间通信

## 9. 约束（必读）

1. **不能把 Node.js bridge 逻辑翻译成 Go** — 任何在 go-bridge 里"绕过 cc-connect 直接调 HTTP"的做法本质上是重走老路
2. **新增能力优先在 cc-connect 层实现** — 只有 cc-connect 确实无法提供的纯查询能力才用 HTTP proxy 补充
3. **opencode 的 resolve_permission 和 reasoning-effort 明确不支持** — 不能假阳性暴露能力
4. **OpenCode proxy-only 方法必须持续收窄** — 当前保留的是 `get_session`、`list_projects`、`create_session`、`resume_session`、`send_message`、`abort_generation`
5. **修改事件管道或 session 管理前必须理解 cc-connect 的进程模型差异** — 见 CLAUDE.md「四个后端的进程模型差异」

## 10. 测试覆盖

| 测试位置 | 覆盖范围 |
|----------|----------|
| `go-bridge/*_test.go` | handler 路由、事件映射、session 生命周期、provider-backed 读路径、环境变量清理 |
| `/Users/jacklee/Projects/cc-connect/agent/opencode/session_test.go` | opencode agent 的 token 累积、session entry 解析、图片暂存 |
| iOS XCTest (CCCodeTests) | bridge 消息解码、MessageWeb 渲染、todo 提取、timeline 构建 |
| Live smoke | WS 连接 → create_session → send_message → verify text_delta + turn_completed + idle |

## 11. 参考

- `转向 cc-connect 路线的原因.md` — Node.js bridge 的四个核心病灶详细分析
- `docs/go-bridge-opencode-cc-connect路线方案.md` — opencode 迁移到 cc-connect 路线的完整方案
- `docs/2026-05-04-go-bridge-opencode-cc-connect路线方案完成情况.md` — 方案完成报告
- `CLAUDE.md` — 项目级架构说明和命令参考
- `/Users/jacklee/Projects/cc-connect` — cc-connect 源码（go-bridge 通过 go.mod replace 引用）
