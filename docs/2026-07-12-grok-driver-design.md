# Grok Driver 设计文档

- **日期**：2026-07-12
- **CLI**：Grok Build `grok 0.2.93`（Rust binary）
- **协议**：Agent Client Protocol (ACP) v1，JSON-RPC 2.0 over stdio
- **入口**：`grok agent stdio`
- **Gate 0 证据**：`docs/2026-07-12-grok-cli-compatibility-evidence.md`（verdict: pass）

---

## 1. CLI 版本范围

- **最低版本**：`0.2.93`（当前本机已验证版本）
- **检测方式**：`grok --version` 解析输出中的 semver
- **版本不兼容**：descriptor 返回 `not_detected` + reason，fail-closed

---

## 2. 进程参数

```
grok agent stdio [--debug] [--debug-file <FILE>] [--leader-socket <PATH>]
```

`grok agent stdio` 启动 ACP JSON-RPC agent over stdin/stdout。stderr 输出日志。

**启动选项**（通过 `grok agent` 父命令传递）：
- `-m/--model <MODEL>`：模型 ID
- `--reasoning-effort <EFFORT>`：reasoning effort
- `--always-approve`：自动批准所有工具（不推荐用于 bridge）
- `--agent-profile <PATH>`：agent profile 文件
- `--no-leader`：不使用共享 leader 进程（每个 session 独立进程）
- `--leader`：连接到共享 leader 进程

**Bridge 默认配置**：
- 使用 `--no-leader`（每个 MacBridge session 对应独立 Grok 进程，避免跨 session 干扰）
- 不使用 `--always-approve`（保留真实审批流）
- `--model` 和 `--reasoning-effort` 从 MacBridge agent config 传入

**workspace 参数**：通过 ACP `session/new` 的 `cwd` 字段传递，不是 CLI flag。工作目录必须是请求所选 workspace，不能回退到 home/当前目录。

---

## 3. 输入输出 schema

### 3.1 传输层

JSON-RPC 2.0 over stdin/stdout。每行一个 JSON-RPC 消息（newline-delimited）。

- **MacBridge → Grok**（stdin）：`initialize`、`authenticate`、`session/new`、`session/load`、`session/prompt`、`session/cancel`、`session/request_permission` response
- **Grok → MacBridge**（stdout）：`initialize` response、`authenticate` response、`session/new` response、`session/prompt` response（stopReason）、`session/update` notification、`session/request_permission` request

### 3.2 初始化握手

1. MacBridge 发送 `initialize`（`protocolVersion: 1`，`clientCapabilities` 声明 `session` 支持）
2. Grok 返回 `InitializeResponse`，含 `agentCapabilities`（`loadSession`、`sessionCapabilities.list/resume/close/delete`、`promptCapabilities`、`authMethods`）
3. 若 `authMethods` 非空，MacBridge 发送 `authenticate`（使用第一个 auth method）
4. Grok 返回 `authenticate` response（成功）

### 3.3 session/update 事件

ACP `session/update` notification 的 `update` 字段是 `SessionUpdate` tagged union，discriminator 字段为 `sessionUpdate`：

| ACP `sessionUpdate` 值 | core.Event | 映射说明 |
| :--- | :--- | :--- |
| `agent_message_chunk` (content.type=text) | `EventText` | 文本增量 |
| `agent_message_chunk` (content.type=resource_link) | `EventText` | 资源链接作为文本呈现 |
| `agent_thought_chunk` (content.type=text) | `EventThinking` | 思考/reasoning 内容 |
| `user_message_chunk` | 不映射 | 用户消息回显，不需要转发给 iOS |
| `tool_call` (status=pending/in_progress) | `EventToolUse` | 工具开始 |
| `tool_call` (status=completed) | `EventToolResult` (success=true) | 工具成功 |
| `tool_call` (status=failed) | `EventToolResult` (success=false) | 工具失败 |
| `tool_call_update` | `EventToolUse` 或 `EventToolResult` | 按 status 更新映射 |
| `plan` | `EventPlan` | 完整计划列表替换 |
| `usage_update` | `EventContextUsageUpdated` | token 使用量 + context window |
| `current_mode_update` | 不映射为 Event | 内部状态更新 |
| `session_info_update` | 不映射为 Event | 内部状态更新 |
| `available_commands_update` | 不映射 | iOS 不使用命令列表 |
| `config_option_update` | 不映射 | iOS 不使用配置选项 |

### 3.4 turn 生命周期

**ACP v1 没有 `turn` 字段。** Turn 边界由 `session/prompt` 的请求/响应关联决定：

- **Turn 开始**：MacBridge 发送 `session/prompt` request → 发射 `EventTurnStarted`
- **Turn 结束**：Grok 返回 `session/prompt` response（含 `stopReason`）→ 发射 `EventResult`
  - `stopReason: "end_turn"` → 正常完成
  - `stopReason: "cancelled"` → 被取消
  - `stopReason: "max_tokens"` / `"max_turn_tokens"` → token 限制
  - `stopReason: "refusal"` → 拒绝

### 3.5 权限审批

ACP 权限通过独立的 `session/request_permission` RPC 实现（不是 tool_call 的 status）：

1. Grok 发送 `session/request_permission` request（含 `toolCallId`、`options[]`）
2. MacBridge 发射 `EventPermissionRequest`（`RequestID` = JSON-RPC request `id`，`ToolName` = toolCall title）
3. iOS 用户选择 allow/deny
4. MacBridge 收到 iOS 的 `resolve_permission` → 发送 `session/request_permission` response
   - allow → `{"outcome": "selected", "optionId": "<allow_option_id>"}`
   - deny → `{"outcome": "selected", "optionId": "<reject_option_id>"}`
   - turn 取消 → `{"outcome": "cancelled"}`

**PermissionOptionKind**：`allow_once`、`allow_always`、`reject_once`、`reject_always`

Driver 映射 `core.PermissionResult.Behavior`：
- `"allow"` → 选择第一个 `allow_*` option
- `"deny"` → 选择第一个 `reject_*` option

---

## 4. session 生命周期

| 操作 | ACP 方法 | 说明 |
| :--- | :--- | :--- |
| 创建 session | `session/new` (cwd=workspace) | 返回 `sessionId` (UUID) |
| 加载已有 session | `session/load` (sessionId) | 需 `loadSession` capability |
| 发送消息 | `session/prompt` (sessionId, prompt[]) | prompt 是 ContentBlock 数组 |
| 取消 turn | `session/cancel` (sessionId) | notification，无 response |
| 列出 session | `session/list` | 可选（MAY support） |
| 关闭 session | `session/close` | 可选 |

**session ID 管理**：
- `session/new` 返回的 `sessionId` 更新到 `core.AgentSession.CurrentSessionID()`
- 不能根据文本猜测 session ID
- Resume 通过 `session/load` 实现（若 `loadSession` capability 存在）

**ListSessions（v1 = 本地 catalog，方案 1）**：
- 实测（2026-07-12，grok 0.2.93）：ACP `session/list` 返回 `Method not found`；`initialize` 未声明 `sessionCapabilities.list`。
- v1 实现读取 `$GROK_HOME/sessions/`（默认 `~/.grok/sessions`）：
  - 主源：`**/summary.json` walk
  - 标题增强：可选 `sqlite3` CLI 读 `session_search.sqlite`；否则用 `chat_history.jsonl` 首条真实 user 文本
  - 返回 `ID` / `Directory`(cwd) / `ModifiedAt` / `MessageCount` / `ModelID` / `Summary`
- 同时实现 `core.HistoryProvider`：读对应目录 `chat_history.jsonl`，过滤 system / synthetic reminder，unwrap `<user_query>`，供 `get_session_messages` 与 iOS 续聊。
- 打开后继续用 `session/load`（`loadSession: true` 已实测）。
- 未来若官方 ACP list 可用，可优先 RPC 并降级到本地 catalog；不改 protocol。

---

## 5. 取消与关闭

### 取消当前 turn

发送 `session/cancel` notification（含 `sessionId`）。Grok 最终返回 `session/prompt` response（`stopReason: "cancelled"`）。

取消后必须对所有 pending `session/request_permission` 回复 `{"outcome": "cancelled"}`。

### 关闭 session（Close）

三阶段优雅关闭（参考 claudecode session.go 的 Close 模式）：

1. **Phase 1 — stdin close**：关闭 stdin，等待进程自然退出（8s 超时）
2. **Phase 2 — SIGTERM**：向进程组发送 SIGTERM（5s 超时）
3. **Phase 3 — SIGKILL**：`cancel()` context + `forceKillProcessGroup`

使用 `core.BuildAgentEnv` 构建子进程环境变量，禁止继承 `CCCODE_*`、`OPENCODE_SERVER_*` 等 control-plane secret。

---

## 6. 错误分类

| 错误类型 | 处理 | core.Event |
| :--- | :--- | :--- |
| JSON-RPC error response | 解析 error.code，映射为用户可读消息 | `EventError` |
| stdout JSON 解码失败 | 记录原始行（脱敏），继续读下一行 | `EventError`（可诊断） |
| 进程意外退出（非零 exit） | 发射终态 `EventError`，标记 session 不 alive | `EventError` + `Done: true` |
| `session/prompt` 返回 error | 发射 `EventError` | `EventError` |
| stdin 写入失败 | 标记 session 不 alive，发射 `EventError` | `EventError` |

**ACP error codes**：
- `-32700` Parse error
- `-32601` Method not found
- `-32002` Authentication required
- `-32001` Resource not found

---

## 7. 敏感信息脱敏

- `rawInput` / `rawOutput`（tool_call 的原始参数和输出）：不广播给 iOS，只提取 `title`、`kind`、`status`、`locations`（路径）
- `agent_thought_chunk` 内容：转发为 `EventThinking`，但这是 Grok 自己产生的 reasoning，不是用户私有数据
- 环境变量：使用 `core.BuildAgentEnv` 过滤 control-plane secret
- 日志：不记录 prompt 内容、tool 参数、文件内容、token、账户信息
- session 存储路径（`~/.grok/sessions/`）：不写入日志或报告

---

## 8. capability 证据 → 实现对照表

| capability | core interface | 实现条件 | Gate 0 证据 |
| :--- | :--- | :--- | :--- |
| `session_state` | `core.Agent` (baseline) | 总是声明 | ACP session/new + session/load |
| `model_switch` | `core.ModelSwitcher` | 实现后声明 | `grok --model` flag + ACP initialize |
| `permission_mode` | `core.ModeSwitcher` | 实现后声明 | `grok --permission-mode` flag |
| `permission_resolve` | `core.ToolAuthorizer` | 实现后声明 | ACP `session/request_permission` |
| `session_history` | `core.HistoryProvider` | v1 已实现（本地 `chat_history.jsonl`） | 本地 sessions 落盘 + `loadSession: true`；ACP list 仍未支持 |
| `usage_reporting` | `core.TokenUsageReporter` | 实现后声明 | ACP `usage_update` SessionUpdate |
| `workspace_diff` | `core.WorkDirSwitcher` | 实现后声明 | ACP `session/new` cwd 参数 |
| `diagnostics` | `core.DiagnosticsProvider` | 总是实现 | CLI 检测 + 版本检查 |
| `question_reply` | 不声明 | ACP 无 question 协议 | Gate 0 evidence §5 |
| `todos` | `core.TodoProvider` | 仅 ACP 有 todo 字段时 | 需实测确认 |
| `session_pin` | `core.SessionPinner` | 实现后声明 | 使用 `pinstore.FromOpts` |
| `content_chunking` | 不声明 | 仅 Claude 声明 | `deriveBackendCapabilities` id 特判 |

### deriveBackendCapabilities 硬编码分支审查

| 分支 | 当前逻辑 | grokbuild 影响 | 决策 |
| :--- | :--- | :--- | :--- |
| `id == "claudecode"` → `content_chunking` | Claude 专属 | 不触发（id 不匹配） | ✅ 无需修改 |
| `id != "opencode" && id != "codex"` → `permission_resolve`（若实现 `ToolAuthorizer`） | grokbuild 满足条件 | **会自动声明** `permission_resolve`（若实现 `ToolAuthorizer`） | ✅ 符合预期——Grok 实现了 `session/request_permission`，应该声明 |
| `TodoProvider \|\| id == "opencode"` → `todos` | grokbuild 仅在实现 `TodoProvider` 时触发 | 不无条件声明 | ✅ 无需修改 |
| `id == "codex" && app_server` → `compression`/`question_reply` | Codex 专属 | 不触发 | ✅ 无需修改 |
| `id == "claudecode"` → `question_reply` | Claude 专属 | 不触发 | ✅ 无需修改 |

**结论**：grokbuild 不需要修改 `deriveBackendCapabilities` 的任何硬编码分支。所有 capability 通过 interface 实现自动声明。

---

## 9. protocol 决策

**无 protocol change。** Grok v1 的全部事件和 RPC 都可以用现有 `bridge-v1.md` 的 schema 表达：

- ACP `session/update` → 现有 bridge event（text/tool/plan/thinking/usage）
- ACP `session/request_permission` → 现有 `permission_request` event + `resolve_permission` RPC
- ACP `session/cancel` → 现有 cancel 语义
- 不需要新的 bridge event type
- 不需要提高 protocol version

---

## 10. 文件结构

```
agent/grokbuild/
├── grokbuild.go          # init() + New() + Agent struct + core.Agent 方法
├── session.go            # grokSession struct + core.AgentSession 实现
├── acp_codec.go          # ACP JSON-RPC 编解码 + SessionUpdate → core.Event 转换
├── acp_types.go          # ACP wire types (SessionUpdate, ContentBlock, ToolCall, etc.)
├── diagnostics.go        # core.DiagnosticsProvider 实现
├── session_mutation.go   # core.SessionRenamer/SessionArchiver（若支持）
├── session_pin.go        # core.SessionPinner（使用 pinstore.FromOpts）
└── *_test.go             # 单元测试
```

**编译时断言**：
```go
var _ core.Agent = (*Agent)(nil)
var _ core.AgentSession = (*grokSession)(nil)
var _ core.DiagnosticsProvider = (*Agent)(nil)
```

---

## 11. 待实测项（Phase 2 补充）

以下项需在 Phase 2 用 `grok agent stdio --debug` + 真实或模拟 ACP 交互确认：

1. ~~`session/list` 是否实际支持~~ → **不支持**（Method not found）；v1 改本地 catalog
2. `usage_update` 是否在实际 turn 中产生
3. `plan` update 是否在 plan mode 下产生
4. `agent_thought_chunk` 是否在 reasoning model 下产生
5. ~~`loadSession` capability~~ → **已声明 true**；`session_history` 由本地 HistoryProvider 提供
6. `session/request_permission` 的 `options[]` 实际内容（optionId 值）
