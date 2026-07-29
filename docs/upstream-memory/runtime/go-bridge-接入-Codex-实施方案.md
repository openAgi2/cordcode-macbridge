# go-bridge 接入 Codex 实施方案

路线约束见 `docs/go-bridge-接入-Codex-现状及建议.md`，本文档只写实施细节。

## 目标

分阶段补齐 go-bridge 的 Codex 能力，每阶段有独立验收标准：

| 阶段 | 目标 | 依赖 |
|---|---|---|
| 0 | 收敛 app-server mode/permission/listen 风险 | 无 |
| 1 | Todo/Plan 双通道：`fetch_todos` + `todos_updated` | 阶段 0 |
| 2 | Memory Files | 阶段 0 |
| 3 | Provider 切换 | 阶段 0 |
| 4 | 评估 app-server 默认启用 | 阶段 0 + 1 |
| 5 | Context 压缩 | cc-connect 有真实能力后 |

第一轮交付：阶段 0 + 阶段 1。Memory/provider/compression 不混入。

## 阶段 0：收敛 app-server 基础风险

目的：让 app-server 模式可以安全启用（不一定默认启用），消除会卡住 turn 的阻塞点。

### 0.1 收敛 Codex app-server mode 映射

问题：go-bridge 当前给所有 agent 传 `"mode": "bypassPermissions"`，cc-connect Codex `normalizeMode` 不认识，回落到 `"suggest"`，导致 app-server 进入 `approvalPolicy=on-request`。

不能把 `"bypassPermissions"` 在 Codex agent 中全局映射成 `"yolo"`。这会影响当前默认 exec 模式，使 `codex exec` 带上 `--dangerously-bypass-approvals-and-sandbox`，超出本阶段目标。

修改位置：
- `/Users/jacklee/Projects/opencode-cc-connect/go-bridge/main.go`
- 如需新增显式 Codex mode alias，再改 `/Users/jacklee/Projects/cc-connect/agent/codex/codex.go`

推荐实现：
1. go-bridge 保持当前 exec 默认路径不变。
2. 只有在显式启用 Codex app-server 时，go-bridge 对 Codex 传入 `mode: "full-auto"` 或另一个 cc-connect Codex 已识别的安全值。
3. 如果需要新增 alias，必须使用 Codex 专用且语义清晰的值，不能复用 Claude Code 的 `"bypassPermissions"` 去隐式触发 Codex `yolo`。

可接受的最小实现示例：

```go
if id == "codex" && enableCodexAppServer {
    agentOpts["backend"] = "app_server"
    agentOpts["mode"] = "full-auto"
}
```

验收：
- 当前默认 exec 模式下，Codex 不会因为 go-bridge 的 `"bypassPermissions"` 隐式进入 `yolo`
- 显式 app-server 模式下，Codex 不会回落到 `"suggest"` / `approvalPolicy=on-request`
- 新增测试覆盖 go-bridge Codex app-server opts 或 cc-connect Codex mode alias
- `go test ./agent/codex` 通过

### 0.2 处理 app-server listen 地址

问题：默认 `appServerURL = "ws://127.0.0.1:3845"` 导致每个 app-server session 都带 `--listen ws://127.0.0.1:3845`，多 session 并发端口冲突。go-bridge/cc-connect 使用 stdio JSON-RPC，不需要固定 listen。

修改位置：`/Users/jacklee/Projects/cc-connect/agent/codex/codex.go` + `appserver_session.go`

实现：
1. `appServerURL` 默认改为空字符串
2. `appserver_session.go` 的 `connect()` 中，只在 `url` 非空时追加 `--listen`
3. 保留 `WorkspaceAgentOptions()` 中对显式 `app_server_url` 的透传

验收：
- 默认启动的 app-server session 不带 `--listen`
- 两个 session 并发启动无端口冲突
- 显式传 `app_server_url` 时仍正常工作

### 0.3 明确 permission 策略

问题：`RespondPermission()` 是 no-op，但 `suggest` 模式会触发 permission request。

处理方式（第一轮）：
- Codex app-server 在 go-bridge 默认路径下必须使用 `approvalPolicy=never`（即 mode 必须是 `yolo` 或 `full-auto`）
- 不对 Codex 暴露 `permission_resolve` capability
- 不实现 permission request/response 链路，不留下 no-op 但有请求的中间状态

后续如果要支持 `on-request`，必须在 cc-connect 中实现完整的 permission notification 映射和 `RespondPermission`。

验收：
- `go-bridge BackendList()` 对 Codex 不含 `permission_resolve`
- app-server turn 不会因审批卡住

### 0.4 阶段 0 验收总表

```bash
cd /Users/jacklee/Projects/cc-connect && go test ./agent/codex
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge && go test ./...
```

- 默认 exec 模式不会把 `"bypassPermissions"` 解释为 Codex `yolo`
- 显式 app-server 模式不会进入 Codex `suggest`
- 默认 app-server session 不带 `--listen`
- Codex 不暴露 `permission_resolve`
- 现有测试全部通过

---

## 阶段 1：Todo/Plan 双通道

目的：iOS 能在 session 切换时 `fetch_todos`，在 app-server 模式下实时收到 `todos_updated`。

### 1.1 cc-connect core 新增 `EventPlan`

修改位置：`/Users/jacklee/Projects/cc-connect/core/message.go`

```go
const (
    // 已有: EventText, EventToolUse, EventToolResult, EventResult, EventError, EventPermissionRequest, EventThinking
    EventPlan EventType = "plan"
)

type Event struct {
    // 已有字段 ...
    Plan []Todo `json:"plan,omitempty"`
}
```

规则：
- 使用现有 `core.Todo`，不新增 plan item 类型
- `Status` 统一为 iOS 能处理的值：`pending`、`in_progress`、`completed`、`cancelled`
- `Priority` 没有真实来源时用空字符串

验收：
- `go test ./core` 通过
- 不影响现有 Event JSON / engine 逻辑

### 1.2 cc-connect Codex 处理 `turn/plan/updated`

修改位置：`/Users/jacklee/Projects/cc-connect/agent/codex/appserver_session.go`

在 `handleNotification` 中增加 `turn/plan/updated` 分支：

```go
case "turn/plan/updated":
    var n planUpdatedNotification
    if err := json.Unmarshal(params, &n); err != nil {
        return
    }
    todos := mapAppServerPlanTodos(n.Plan)
    s.emit(core.Event{Type: core.EventPlan, SessionID: s.CurrentSessionID(), Plan: todos})
```

状态映射：

| Codex status | → wire status |
|---|---|
| `pending` | `pending` |
| `in_progress` / `running` / `active` | `in_progress` |
| `completed` / `complete` / `done` | `completed` |
| `cancelled` / `canceled` | `cancelled` |
| 其他 | `pending` |

验收：
- 新增 unit test：喂入 `turn/plan/updated` JSON-RPC notification，断言输出 `core.Event{Type: EventPlan, Plan: ...}`
- `go test ./agent/codex` 通过

### 1.3 cc-connect Codex 实现 `TodoProvider`

修改位置：`/Users/jacklee/Projects/cc-connect/agent/codex/codex.go`（或新文件 `todos.go`）

```go
func (a *Agent) FetchTodos(ctx context.Context, sessionID string) ([]core.Todo, error)
```

实现路径：
- 首选读 Codex session 持久化数据中的 plan snapshot
- 如果当前 session 有效但无 plan snapshot，返回空列表
- 只有 Codex agent 全局不具备读取 todo/plan 的能力时，才返回 `core.ErrNotSupported`
- 不在 go-bridge 缓存最近一次实时事件冒充权威状态

验收：
- `var _ core.TodoProvider = (*Agent)(nil)` 编译通过
- 对有效但无 plan 的 session 返回空列表
- 对不存在 session 返回明确错误，不假装成功
- 只有全局能力不可用时才返回 `core.ErrNotSupported`
- `go test ./agent/codex` 通过

### 1.4 go-bridge 映射 `EventPlan` → `todos_updated`

修改位置：`/Users/jacklee/Projects/opencode-cc-connect/go-bridge/events.go`

```go
case core.EventPlan:
    return "todos_updated", map[string]interface{}{
        "todos": todosToWire(ev.Plan),
    }, false
```

`todosToWire` 可复用 `handlers.go` 中 `handleFetchTodos` 的 todo 映射逻辑。

验收：
- 新增 `TestMapAgentEventPlanTodosUpdated`：event name、payload、done=false
- 现有 event tests 通过

### 1.5 go-bridge capability 自动暴露

`handlers.go` 的 `BackendList()` 已动态检查 `TodoProvider` 接口。Codex agent 实现 `TodoProvider` 后：
- `BackendList()` 自动对 Codex 暴露 `todos` capability
- `fetch_todos` handler 对 `core.ErrNotSupported` 返回 `not_supported`
- 不在 go-bridge 直接读 Codex session 文件

验收：
- 新增 handler test：Codex fakeAgent 有 todos 时暴露 `todos` capability
- `fetch_todos` payload 与 iOS 现有 `SessionTodo` 解码兼容

### 1.6 阶段 1 验收总表

```bash
cd /Users/jacklee/Projects/cc-connect && go test ./core ./agent/codex
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge && go test ./...
```

- `EventPlan` 事件类型已添加
- `handleNotification` 处理 `turn/plan/updated`
- `FetchTodos` 编译通过且行为正确
- `EventPlan` → `todos_updated` 映射正确
- Codex `todos` capability 自动暴露
- 所有现有测试通过

---

## 阶段 2：Memory Files

目标：Codex memory read 走 cc-connect agent，不走 go-bridge 直接读文件。

修改位置：
- `/Users/jacklee/Projects/cc-connect/agent/codex/codex.go` 或新文件 `memory_files.go`
- go-bridge 无需改动（handler 已存在）

实现：
1. Codex agent 实现 `core.MemoryFileReader`
2. 复用 `ProjectMemoryFile()` / `GlobalMemoryFile()` 路径
3. 稳定 file IDs：`project:agents`、`global:agents`
4. 只返回真实存在且可读的文件

验收：
- `go test ./agent/codex` 通过
- go-bridge `BackendList()` 对 Codex 暴露 `memory_read`

---

## 阶段 3：Provider 切换

目标：provider 能从配置进入 Codex agent，并通过 go-bridge 切换。

前置：Codex agent 已实现 `ProviderSwitcher`，但 go-bridge 没有 provider 配置加载和 handler。

实现顺序：
1. cc-connect 层确认 provider config 来源，复用 config/presets 结构
2. go-bridge 启动时把 providers 传入 Codex agent
3. go-bridge 增加 `provider_switch` capability
4. 实现 `list_providers` / `set_provider`
5. 切换 provider 后只保证新 session 生效

验收：
- providers 为空时返回明确空列表
- 切换不存在 provider 返回 `not_found`
- 切换成功后 `GetActiveProvider()` 与后续 `StartSession()` 一致

---

## 阶段 4：评估 app-server 默认启用

阶段 0 + 1 通过后才评估。评估条件：

- app-server create/send/turn_completed/idle smoke 通过
- 两个 Codex session 并发无端口冲突
- 需要审批的路径不会静默卡住
- Todo/Plan 实时更新可见，`fetch_todos` 能恢复初始状态

不满足时，保留 exec 默认，允许通过显式配置启用 app-server。

---

## 阶段 5：Context 压缩

当前不做。`CompressCommand()` 返回空字符串 = 不可用。只有 cc-connect Codex agent 有真实压缩入口后才补 go-bridge handler。

---

## 测试

### cc-connect

```bash
cd /Users/jacklee/Projects/cc-connect
go test ./core ./agent/codex
```

需覆盖：
- 默认 exec 模式不会把 `"bypassPermissions"` 解释为 Codex `yolo`
- 显式 app-server 模式不会进入 Codex `suggest`
- 默认 app-server session 不带 `--listen`
- `turn/plan/updated` → `EventPlan` 映射
- `FetchTodos` 成功、有效空 plan、session 不存在、ErrNotSupported
- MemoryFileReader（阶段 2）
- ProviderSwitcher 配置注入（阶段 3）

### go-bridge

```bash
cd /Users/jacklee/Projects/opencode-cc-connect/go-bridge
go test ./...
```

需覆盖：
- `EventPlan` → `todos_updated`
- Codex `todos` capability 暴露
- `fetch_todos` 成功和 `not_supported`
- memory capability（阶段 2）
- provider switch handler（阶段 3）

### live smoke

默认不跑 UI/真机验证。阶段 4 前可做最小 live smoke：
1. go-bridge 只启用 codex
2. WebSocket register → create_session → send_message
3. 等待 text_delta → turn_completed → session_state_changed: idle
4. Todo/Plan 场景：确认 `todos_updated` 事件

---

## 禁止事项

- 禁止从 `bridge/src/backends/codex*.mjs` 搬实现
- 禁止在 go-bridge 新增 Codex app-server client/proxy 绕过 cc-connect
- 禁止用实时事件缓存冒充 `FetchTodos` 权威状态
- 禁止在 unsupported 场景返回空成功
- 禁止默认开启 UI/真机验证
