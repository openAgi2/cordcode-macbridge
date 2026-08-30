# Unified Bridge Protocol

> **版本**: 1
> **Schema Revision**: 2026-04-27-r1
> **协议名**: `opencode-unified-bridge`
> **状态**: Phase 0 冻结版 — 开发 agent 的权威输入
> **来源**: 从 `docs/unified-bridge-plan.md` sections 5.1–5.4 提取并一致性化

本文档是统一 Bridge 协议的**唯一权威来源**。方法表、事件表、详细 schema、Driver 接口、Swift API 五处字段以本文档为准。

---

## 1. 版本与连接

### 1.1 协议标识

```jsonc
{
  "name": "opencode-unified-bridge",
  "version": 1,
  "schemaRevision": "2026-04-27-r1"
}
```

### 1.2 客户端注册 (Client → Server)

```jsonc
{
  "type": "register",
  "protocol": { "name": "opencode-unified-bridge", "version": 1, "schemaRevision": "2026-04-27-r1" },
  "client": { "id": "client-uuid-stable-across-reconnects", "name": "OpenCodeiOS", "version": "2.4.0" },
  "capabilities": [
    "text", "image", "file", "permission", "model_switch",
    "reasoning_effort", "todos", "session_state",
    "catalog_invalidation", "tool_output_delta",
    "session_mutation", "usage_reporting",
    "turns", "agent_selection"
  ]
}
```

**字段说明：**
- `client.id`: 跨重连稳定的 UUID
- `register` 是旧客户端兼容入口，不承载事件恢复；恢复只通过 `hello/hello_ack` 的
  `recovery_v1` 可选扩展协商

### 1.3 注册确认 (Server → Client)

```jsonc
{
  "type": "register_ack",
  "ok": true,
  "protocol": { "name": "opencode-unified-bridge", "version": 1, "schemaRevision": "2026-04-27-r1" },
  "serverCapabilities": [
    "text", "image", "file", "permission", "model_switch",
    "reasoning_effort", "todos", "session_state",
    "catalog_invalidation", "tool_output_delta",
    "session_mutation", "turns", "agent_selection"
  ],
  "bridgeEpoch": "epoch-20260427-001",
  "backends": [
    {
      "id": "claude",
      "kind": "claude_code",
      "displayName": "Claude Code",
      "capabilities": ["text", "image", "permission", "model_switch", "session_state", "catalog_invalidation", "memory_read", "usage_reporting"],
      "descriptor": {
        "runtimeStatus": "available",
        "sdkAPIStability": "stable",
        "configFingerprint": "fp-abc123"
      },
      "permissionMode": { "mode": "default" }
    },
    {
      "id": "opencode",
      "kind": "opencode",
      "displayName": "OpenCode",
      "capabilities": ["text", "image", "session_mutation", "todos", "agent_selection", "reasoning_effort", "memory_read", "diagnostics"],
      "permissionMode": { "mode": "default" }
    },
    {
      "id": "codex",
      "kind": "codex",
      "displayName": "Codex",
      "capabilities": ["text", "permission", "model_switch", "turns", "tool_output_delta"]
    },
    {
      "id": "codex-web",
      "kind": "codex-web",
      "displayName": "Codex Web",
      "capabilities": ["model_switch", "session_history", "external_turn_streaming", "permission_resolve", "structured_user_input_v1"]
    }
  ],
  "recovery": null
}
```

**`backends[].capabilities`** 只暴露当前 build 中可实际调用的方法能力；未来 phase 的预留能力在 feature gate 打开前不得提前 advertise。

**`backends[].kind`** 使用冻结枚举：`claude_code` | `opencode` | `codex` | `codex-web` | `codex-remote` | `copilot` | `unified_bridge`

---

## 2. 请求/响应模式

所有请求带 `backendId`、`requestId`，服务端回复对应 result。

### 2.1 请求格式

```jsonc
{
  "type": "request",
  "requestId": "uuid-1",
  "backendId": "claude",
  "method": "send_message",
  "params": { /* 方法特定参数 */ }
}
```

### 2.2 成功响应

```jsonc
{
  "type": "result",
  "requestId": "uuid-1",
  "backendId": "claude",
  "ok": true,
  "data": { /* 方法特定数据 */ }
}
```

### 2.3 错误响应

```jsonc
{
  "type": "result",
  "requestId": "uuid-1",
  "backendId": "claude",
  "ok": false,
  "error": {
    "code": "session_not_found",
    "message": "Session sess-123 not found",
    "retryable": true,
    "recoverBy": "resume_session",
    "backendId": "claude"
  }
}
```

---

## 3. 错误模型

```ts
type UnifiedError = {
  code: string
  message: string
  retryable?: boolean
  recoverBy?: 'resume_session' | 'fetch_snapshot' | 'refresh_catalog' | 'reconfigure_backend' | 'switch_backend'
  backendId?: string
  underlying?: {
    protocol?: string
    code?: string | number
    message?: string
  }
}
```

### 3.1 错误码枚举

| code | 含义 | recoverBy |
|------|------|-----------|
| `session_not_found` | Session 不存在或已过期 | `resume_session` |
| `turn_not_found` | 目标 turn 不在该 session 的已提交投影 Kernel 中（`session_turn_items`，§11.7） | — |
| `session_busy` | Session 正在处理另一请求 | — (retryable) |
| `backend_unavailable` | Backend 进程不可达 | `reconfigure_backend` |
| `unsupported_capability` | Backend 不支持该功能 | — |
| `unsupported_attachment` | 不支持该附件类型 | — |
| `catalog_outdated` | 模型目录过期 | `refresh_catalog` |
| `rate_limited` | 后端限流 | — (retryable) |
| `permission_expired` | 权限请求已过期/被取消 | `fetch_snapshot` |
| `auth_failure` | 认证失败 | `reconfigure_backend` |
| `unknown_method` | 未知方法 | — |
| `unknown_backend` | 未知 backendId | — |
| `attachment_too_large` | 附件超大小限制 | — |
| `invalid_params` | 参数校验失败 | — |

### `send_message` attachments：结构校验与支持矩阵（pre-StartSession）

attachments 在进入任何 session 副作用（admission / `StartSession` / `markRunning` /
split）**之前**按两级校验，任一级失败整条消息拒绝（不部分处理、不静默丢弃）：

1. **raw 结构校验** → `invalid_params`：每个附件 `kind ∈ {image, file}`；`mime` 非空且
   为裸 `type/subtype`（trim+lowercase 后匹配 `^[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+$`，
   不接受 `;` 参数，`image/*` 这类通配不是合法字面值）；`base64` 可标准解码且解码后
   非空。valid+invalid 混合同样整条拒绝。
2. **支持矩阵** → `unsupported_attachment`：附件的 **effectiveKind**（`kind == "image"`
   或 normalized mime 以 `image/` 开头 → image，否则 file——与 split 分类共用同一规则，
   不是两套判断）必须被该 backend 在 `hello_ack.backends[].capabilities` 中 **positive
   声明**（`image` / `file`）。缺席即不支持；「未声明」不得反向理解为「全支持」。

各 backend 正向声明（按 driver×mode，语义支持而非签名支持）：

| backend | `file` | `image` |
|---|---|---|
| claude / codex | ✅ | ✅（两种 codex 模式均验） |
| opencode（无 managed server / CLI） | ✅ | ✅（`--file` 路径保留） |
| opencode（managed server） | ✅ | ❌（该路径图像本就静默丢失——拒绝是现状语义化） |
| grokbuild | ✅ | ❌（ACP `promptCapabilities.image=false`） |
| deepseek（DSH） | ❌ | ❌（text-only；能力集仅声明 `text`） |

`attachment_too_large` 当前不产生（本期无 handler 级大小上限；未来引入时上限须
per-backend 正向声明，不得用统一常量回归既有 backend）。两条错误码均为既有 canonical
code，capability 字符串为 extensible addition，无 major version bump。


## 4. 方法表

所有 method 通过 `request` 消息发送，`result` 消息返回。`backendId` 标识路由目标。

### 4.1 必需方法（所有 driver 实现）

| Method | 必需参数 | 说明 |
|--------|---------|------|
| `hello` | `backendId` | 健康检查 + backend descriptor |
| `list_models` | `backendId`, `directory?`, `sessionId?` | 列出可用模型 |
| `list_agents` | `backendId` | 列出可用 agent |
| `create_session` | `backendId`, `title`, `directory?`, `model?`, `agent?` | 创建会话 |
| `resume_session` | `backendId`, `sessionId`, `directory?` | 恢复会话 |
| `list_sessions` | `backendId`, `directory?`, `rootsOnly?` | 列出会话 |
| `get_session` | `backendId`, `sessionId` | 获取会话信息 |
| `get_session_messages` | `backendId`, `sessionId`, `cursor?`, `limit?`, `includeParts?` | 获取消息（cursor 分页） |
| `fetch_content_chunk` | `backendId`, `sessionId`, `contentId`, `offset?`, `limit?` | 获取大 tool output 分块 |
| `send_message` | `backendId`, `sessionId`, `content`, `agent?`, `model?`, `reasoningEffort?`, `attachments?`, `directory?` | 发送消息 |
| `abort_generation` | `backendId`, `sessionId` | 中断生成 |
| `list_projects` | `backendId` | 列出项目目录 |
| `list_directory` | `backendId`, `path` | 浏览 Mac 上的远程文件夹 |
| `get_git_context` | `backendId`, `directory` | 读取仓库根目录、当前分支、工作树与本地分支 |
| `checkout_git_branch` | `backendId`, `directory`, `branch` | 在指定工作树切换已有本地分支 |
| `create_git_branch` | `backendId`, `directory`, `branch` | 创建并切换到新分支 |
| `create_git_worktree` | `backendId`, `directory`, `path`, `branch` | 在明确的绝对路径创建新分支工作树 |

### 4.2 可选方法（由 driver capabilities 声明）

| Method | 必需参数 | 所需 Capability |
|--------|---------|----------------|
| `switch_model` | `backendId`, `sessionId`, `modelId` | `model_switch` |
| `resolve_permission` | `backendId`, `sessionId`, `permissionId`, `selectedOptionId`, `message?` | `permission` |
| `rename_session` | `backendId`, `sessionId`, `title` | `session_mutation` |
| `archive_session` | `backendId`, `sessionId`, `archivedAtMillis` | `session_mutation` |
| `share_session` | `backendId`, `sessionId` | `session_mutation` |
| `delete_session` | `backendId`, `sessionId` | `session_mutation` |
| `fetch_todos` | `backendId`, `sessionId` | `todos` |
| `get_workspace_diff` | `backendId`, `directory?` | `workspace_diff` |
| `get_turn_diff` | `backendId`, `sessionId`, `turnNumber`, `directory?` | `supports_checkpoint`（§6.1；非 git workspace → `workspace_not_git`） |
| `get_full_thread_diff` | `backendId`, `sessionId`, `directory?` | `supports_checkpoint`（§6.1） |
| `check_pull_request_support` | `backendId`, `directory` | `workspace.read`（§7.1） |
| `get_usage` | `backendId` | `usage_reporting` |
| `set_directory` | `backendId`, `sessionId`, `directory` | (可选) |
| `subscribe_sessions` | `backendId` | (可选) |
| `unsubscribe_sessions` | `backendId` | (可选) |
| `list_providers` | `backendId` | (Phase 5B) |
| `set_provider` | `backendId`, `providerId`, `scope`, `sessionId?` | (Phase 5B) |
| `set_permission_mode` | `backendId`, `mode`, `scope`, `sessionId?`, `expiresAt?`, `allowedToolPatterns?`, `deniedToolPatterns?`, `confirmToken?` | (Phase 5C) |
| `compress_context` | `backendId`, `sessionId` | (Phase 5A) |
| `list_memory_files` | `backendId`, `directory?` | (Phase 2b+) |
| `read_memory_file` | `backendId`, `fileId` | (Phase 2b+) |
| `update_memory_file` | `backendId`, `fileId`, `content`, `expectedVersion`, `dryRun?` | (Phase 5D) |
| `run_diagnostics` | `backendId` | (Phase 2b+) |
| `session_turn_items` | `backendId`, `sessionId`, `turnId` | `turn_detail_lazy_v1`（§11.7；v1 仅 codex-remote descriptor 声明） |

### 4.3 方法参数详细 Schema

#### `send_message`

```jsonc
{
  "method": "send_message",
  "backendId": "claude",
  "params": {
    "sessionId": "sess-123",
    "content": "Hello",
    "agent": { "name": "claude" },
    "model": { "id": "claude-sonnet-4", "providerId": "anthropic" },
    "reasoningEffort": "high",
    "attachments": [{
      "kind": "image",
      "mime": "image/png",
      "filename": "screenshot.png",
      "base64": "iVBOR...",
      "sizeBytes": 102400
    }],
    "directory": "/path/to/project"
  }
}
```

**`AttachmentInput` 类型：**

```ts
type AttachmentInput = {
  kind: 'image' | 'file'
  mime: string
  filename?: string
  uri?: string       // 远端路径（支持远端 path 的 driver 用）
  base64?: string    // base64 编码（不支持远端 path 的 driver 用）
  sizeBytes?: number
  sha256?: string
}
```

#### `get_session_messages`

```jsonc
// 请求
{ "method": "get_session_messages", "backendId": "claude",
  "params": { "sessionId": "sess-123", "cursor": null, "limit": 50, "includeParts": true } }

// 响应 data
{
  "messages": [ /* UnifiedMessage[] */ ],
  "nextCursor": "cursor-abc",
  "snapshotVersion": "v1",
  "truncated": false,
  "lastModelId": "claude-sonnet-4",
  "lastProviderId": "anthropic"
}
```

单条超大 tool output：返回 `contentRef` 代替完整内容，iOS 端通过 `fetch_content_chunk` 按需加载。

#### `resolve_permission`

```jsonc
{
  "method": "resolve_permission",
  "backendId": "copilot",
  "params": {
    "sessionId": "sess-1",
    "permissionId": "tool-1",
    "selectedOptionId": "opt-approve",
    "message": null
  }
}
```

---

## 5. 事件推送

所有 session 事件带 `eventId`（含 epoch）、全局 `seq`、session 内单调
`perSessionSeq`、`backendId`，支持断线重连与真实 session gap 检测。

### 5.1 事件信封格式

```jsonc
{
  "type": "event",
  "eventId": "epoch-20260427-001:12346",
  "seq": 12346,
  "perSessionSeq": 87,
  "bridgeEpoch": "epoch-20260427-001",
  "backendId": "claude",
  "sessionId": "sess-123",
  "turnId": "turn-1",
  "timestamp": 1745750400000,
  "event": "text_delta",
  "replayable": true,
  "data": { /* 事件特定数据 */ }
}
```

### 5.2 事件类型列表

| 统一事件名 | data 字段 | 对应 Swift `BackendLiveEvent` |
|-----------|----------|---------------------------|
| `session_created` | `{ session: UnifiedSession }` | `.sessionCreated` |
| `session_status_changed` | `{ isIdle: Bool }` | `.sessionStatusChanged` |
| `session_state_changed` | `{ state, effectiveModelId?, effectiveProviderId?, providerId? }` | `.sessionStateChanged` |
| `turn_started` | `{ turnId }` | `.turnStarted` |
| `turn_completed` | `{ turnId, reason? }` | `.turnCompleted` |
| `user_message` | `{ itemId, turnId, text }` | `.userMessage` |
| `assistant_started` | `{ itemId, agentName? }` | `.assistantMessageStarted` |
| `text_delta` | `{ itemId, delta, agentName? }` | `.assistantMessageDelta` |
| `text_updated` | `{ itemId, content, agentName?, modelId?, providerId? }` | `.assistantMessageUpdated` |
| `text_finished` | `{ itemId, content, agentName?, modelId?, providerId?, modelName? }` | `.assistantMessageFinished` |
| `reasoning_started` | `{ itemId }` | `.reasoningStarted` |
| `reasoning_delta` | `{ itemId, delta }` | `.reasoningDelta` |
| `reasoning_updated` | `{ itemId, content }` | `.reasoningUpdated` |
| `reasoning_finished` | `{ itemId, content }` | `.reasoningFinished` |
| `tool_started` | `{ itemId, step: UnifiedToolStep }` | `.toolStarted` |
| `tool_output_delta` | `{ itemId, delta }` | `.toolOutputDelta` |
| `tool_finished` | `{ itemId, step: UnifiedToolStep }` | `.toolFinished` |
| `error` | `{ message }` | `.error` |
| `model_catalog_invalidated` | `{ configFingerprint, scope, backendId, sessionId?, providerId? }` | `.modelCatalogInvalidated` |
| `model_changed` | `{ modelId, providerId? }` | `.sessionModelChanged` |
| `todos_updated` | `{ todos: UnifiedTodo[] }` | (新增) |
| `usage_reported` | `{ usage: UnifiedUsageReport }` | (新增，Phase 2b) |
| `context_compressing` | `{ sessionId }` | (新增，Phase 5A) |
| `context_compressed` | `{ sessionId, tokensBefore, tokensAfter }` | (新增，Phase 5A) |
| `permission_mode_changed` | `{ mode, scope, sessionId?, expiresAt?, allowedToolPatterns?, deniedToolPatterns?, confirmToken? }` | (新增，Phase 5C) |
| `diagnostic_progress` | `{ diagnosticRunId, checkId, status, message }` | (新增，Phase 2b) |
| `diagnostic_completed` | `{ diagnosticRunId, results: DiagnosticCheck[], overallStatus }` | (新增，Phase 2b) |

### 5.3 新增事件 iOS ViewModel 影响矩阵

| 新增事件 | foreground handler | background handler | snapshot write | 非归属 session 路由 |
|---------|-------------------|-------------------|---------------|-------------------|
| `todos_updated` | 更新 todo list | 忽略 | 不需要 | 按 sessionId 路由 |
| `usage_reported` | 更新用量 UI | 忽略 | 不需要 | 全局事件，不按 session 路由 |
| `context_compressing` | 显示进度 | 忽略 | 不需要 | 按 sessionId 路由 |
| `context_compressed` | 刷新消息列表 | 忽略 | 需要刷新 | 按 sessionId 路由 |
| `permission_mode_changed` | 更新 UI toggle | 忽略 | 不需要 | 按 sessionId 路由 |
| `diagnostic_progress` | 更新诊断 UI | 忽略 | 不需要 | 按 diagnosticRunId 路由 |
| `diagnostic_completed` | 显示诊断结果 | 忽略 | 不需要 | 按 diagnosticRunId 路由 |

---

## 6. 统一 Schema 定义

### 6.1 UnifiedSession

对应 Swift `Session`。

```ts
type UnifiedSession = {
  id: string
  backendId: string
  title: string
  createdAtMillis: number
  updatedAtMillis: number
  archivedAtMillis?: number
  messageCount?: number
  directory?: string
  projectId?: string
  parentId?: string
  share?: { url: string }
  availability: 'resumable' | 'history_only' | 'active_only'
  isReadOnlyHistory: boolean
  effectiveModelId?: string
  effectiveProviderId?: string
  agentName?: string
  runtimeState?: 'idle' | 'running' | 'requiresAction' | 'compactingHint' | 'requestingHint' | 'unknown'
}
```

**`runtimeState`（2026-08-15 F-8 补记，go-bridge 既有 de facto 字段正式入册）：**
session 运行态徽标值域。`unknown` = Mac 侧确实查不到该 session 的状态（进程
探测不可用且无 registry 记录、或 claude transcript 尾部无可判定条目）——**客户端
必须不渲染状态徽标**（「不知道就不亮灯」），不得把 unknown 当 idle/running 处理。
已知状态语义不变：`running` 执行中、`idle` 空闲、`requiresAction` 待用户动作、
`compactingHint`/`requestingHint` 上下文整理中。客户端实现提示：不识别的值一律
按 unknown 处理（不亮徽标）。

**字段映射（wire → Swift）：**

| Wire 字段 | Swift 字段 | 说明 |
|-----------|-----------|------|
| `id` | `id` | 直接映射 |
| `backendId` | (adapter 层路由用，不存入 Session) | 多 backend 区分 |
| `title` | `title` | 直接映射 |
| `createdAtMillis` | `createdAt` | `Date(timeIntervalSince1970: ms / 1000)` |
| `updatedAtMillis` | `updatedAt` | `Date(timeIntervalSince1970: ms / 1000)` |
| `archivedAtMillis` | `archivedAt` | `Date?`, 可选 |
| `messageCount` | `messageCount` | 直接映射 |
| `directory` | `directory` | 直接映射 |
| `projectId` | `projectID` | wire `projectId` → Swift `projectID` |
| `parentId` | `parentID` | wire `parentId` → Swift `parentID` |
| `share` | `share` | 直接映射 |
| `isReadOnlyHistory` | `isReadOnlyHistory` | 直接映射 |
| `effectiveModelId` | `effectiveModelID` | wire `Id` → Swift `ID` |
| `effectiveProviderId` | `effectiveProviderID` | wire `Id` → Swift `ID` |
| `agentName` | (无直接字段，adapter 层处理) | |
| `runtimeState` | `runtimeState` | 可选；值域与 unknown 语义见上方补记；Swift 侧为 `String?` 原样保存 |

Swift 派生属性：`isPrimarySession` = `parentId` 为空，`isArchived` = `archivedAt != nil`，`isChildSession` = `!isPrimarySession`。

### 6.2 UnifiedModel

对应 Swift `ModelInfo`。

```ts
type UnifiedModel = {
  id: string
  name: string
  provider: string
  providerId: string
  reasoning?: boolean
  limit?: { context: number, output: number }
  supportedReasoningEfforts?: ('minimal'|'low'|'medium'|'high'|'xhigh'|'max'|'ultra')[]
  defaultReasoningEffort?: 'minimal'|'low'|'medium'|'high'|'xhigh'|'max'|'ultra'
  isDefault?: boolean
}
```

**字段映射（wire → Swift）：**

| Wire 字段 | Swift 字段 |
|-----------|-----------|
| `id` | `id` |
| `name` | `name` |
| `provider` | `provider` |
| `providerId` | `providerID` |
| `reasoning` | `reasoning` |
| `limit` | `limit` (TokenLimit) |
| `supportedReasoningEfforts` | `supportedReasoningEfforts` |
| `defaultReasoningEffort` | `defaultReasoningEffort` |
| `isDefault` | `isDefault` |

### 6.3 UnifiedToolStep

对应 Swift `ToolStep`。

```ts
type UnifiedToolStep = {
  id: string
  toolName: string
  status: string  // pending/running/completed/failed/rejected/cancelled/approved/always_approved
  title?: string
  output?: ToolOutput
  duration?: number  // 秒
  requiresPermissionConfirmation: boolean
  resolutionSource?: 'user' | 'policy'
  availablePermissionOptions: UnifiedPermissionOption[]
  todoItems?: UnifiedTodo[]
  fileChanges?: UnifiedFileChange[]
}

type ToolOutput =
  | { kind: 'inline', text: string }
  | { kind: 'content_ref', contentId: string, sizeBytes?: number, preview?: string }

type ContentChunk = {
  contentId: string
  offset: number
  data: string
  nextOffset?: number
  complete: boolean
}
```

**`ToolOutput` iOS 映射策略：**
- `inline.text` → `ToolStep.output`（直接赋值）
- `content_ref.preview` → `ToolStep.output`（降级显示 preview 或 "大输出，点击查看详情"）
- `contentId`/`sizeBytes` → 存入 `UnifiedBridgeAdapter` 私有 `contentStore`
- 用户点击详情 → `fetch_content_chunk(contentId)` 按需加载

**字段映射（wire → Swift）：**

| Wire 字段 | Swift 字段 |
|-----------|-----------|
| `id` | `id` |
| `toolName` | `toolName` |
| `status` | `status` |
| `title` | `title` |
| `output` (inline.text) | `output` |
| `duration` | `duration` |
| `requiresPermissionConfirmation` | `requiresPermissionConfirmation` |
| `availablePermissionOptions` | `availablePermissionOptions` |
| `todoItems` | `todoItems` |

### 6.4 UnifiedPermissionOption

对应 Swift `ToolPermissionOption`。

```ts
type UnifiedPermissionOption = {
  id: string
  action: 'approve' | 'approveAlways' | 'reject' | 'rejectAlways'
  title: string
  scope?: 'once' | 'always'
  isDestructive?: boolean
  backendPayload?: any
}
```

**字段映射（wire → Swift）：**

| Wire 字段 | Swift 字段 |
|-----------|-----------|
| `id` | `id` |
| `action` | `action` (ToolPermissionAction) |
| `title` | `title` |

### 6.5 UnifiedMessage

对应 Swift `Message`。

```ts
type UnifiedMessage = {
  id: string
  role: 'user' | 'assistant' | 'system'
  content: string
  thinking?: string
  thinkingDuration?: number
  steps: UnifiedToolStep[]
  files: UnifiedMessageFile[]
  parts: UnifiedMessagePart[]
  timestampMillis: number
  // Present only when the backend can prove the turn boundary.
  turnStartedAtMillis?: number
  turnCompletedAtMillis?: number
  agentName?: string
  modelId?: string
  providerId?: string
  modelName?: string
}

type UnifiedMessageFile = {
  id: string
  mime: string
  url: string
  filename?: string
}

type UnifiedMessagePart =
  // `presentation` is optional for backwards compatibility. Codex uses
  // `progress` for interim agent messages and `final` for the terminal one.
  | { type: 'text', content: string, presentation?: 'progress' | 'final' }
  | { type: 'reasoning', content: string }
  | { type: 'tool', step: UnifiedToolStep }
  | { type: 'file', file: UnifiedMessageFile }
```

**字段映射（wire → Swift）：**

| Wire 字段 | Swift 字段 |
|-----------|-----------|
| `id` | `id` |
| `role` | `role` |
| `content` | `content` |
| `thinking` | `thinking` |
| `thinkingDuration` | `thinkingDuration` |
| `steps` | `steps` |
| `files` | `files` |
| `parts` | `parts` |
| `timestampMillis` | `timestamp` (`Date(timeIntervalSince1970: ms / 1000)`) |
| `turnStartedAtMillis` | `turnStartedAt` |
| `turnCompletedAtMillis` | `turnCompletedAt` |
| `agentName` | `agentName` |
| `modelId` | `modelID` |
| `providerId` | `providerID` |
| `modelName` | `modelName` |

### 6.6 UnifiedAgent

对应 Swift `AgentInfo`。

```ts
type UnifiedAgent = {
  name: string
  mode?: string
  hidden?: boolean
  native?: boolean
  description?: string
  color?: string
}
```

### 6.7 UnifiedTodo

```ts
type UnifiedTodo = {
  content: string
  activeForm?: string
  status: string
}
```

### 6.8 UnifiedFileChange

```ts
type UnifiedFileChange = {
  path: string
  kind: 'add' | 'delete' | 'update'
  movePath?: string
  diff?: string
}
```

---

## 7. 权限协议

### 7.1 权限请求（嵌入 `tool_started` 事件）

```jsonc
{
  "event": "tool_started",
  "data": {
    "itemId": "tool-1",
    "step": {
      "id": "tool-1",
      "toolName": "bash",
      "status": "pending",
      "title": "等待权限确认",
      "output": "rm -rf /tmp/old",
      "requiresPermissionConfirmation": true,
      "availablePermissionOptions": [
        { "id": "opt-approve", "action": "approve", "title": "批准", "scope": "once" },
        { "id": "opt-approve-always", "action": "approveAlways", "title": "总是批准", "scope": "always" },
        { "id": "opt-reject", "action": "reject", "title": "拒绝", "scope": "once" },
        { "id": "opt-reject-always", "action": "rejectAlways", "title": "总是拒绝", "scope": "always" }
      ]
    }
  }
}
```

### 7.2 权限回复

iOS 端用 `permissionId` + `selectedOptionId` 回复：

```jsonc
{
  "method": "resolve_permission",
  "backendId": "copilot",
  "params": {
    "sessionId": "sess-1",
    "permissionId": "tool-1",
    "selectedOptionId": "opt-approve",
    "message": null
  }
}
```

**设计要点：**
- `permissionId` = `itemId`
- `selectedOptionId` 是实际决策字段，action 只做展示/兼容
- `backendPayload` 允许 driver 透传后端特有不透明数据
- 不支持 `rejectAlways` 的 driver 只返回 3 个 options

---

## 8. 心跳与重连

### 8.1 心跳

- 客户端每 30 秒发送 `{ "type": "ping", "ts": ... }`
- 服务端 90 秒无 ping 则断开
- 服务端回复 `{ "type": "pong", "ts": ... }`

### 8.2 重连流程

1. 客户端指数退避重连（1s → 2s → 4s → ... → 60s）
2. 支持恢复的客户端先安装持久 inbound listener，再发送带 `capabilities: ["recovery_v1"]`、
   `lastBridgeEpoch` 与 `lastSeenBySession` 的 `hello`；`lastEventId` 仅为兼容提示
3. 服务端只在双方都声明 `recovery_v1` 时通过 `hello_ack.recovery` 建立 transaction；否则
   完全保持旧行为并省略 recovery
4. 同 epoch 且 replay/gap 覆盖完整时回放；任意不可回放 gap、buffer eviction 或 TTL 返回
   `snapshot_required`；epoch 不同或无效返回 `full_resync`
5. replay barrier 后客户端完成 apply/persist，发送精确 per-session cut 的
   `recovery_applied`；服务端验证后原子入队 `recovery_complete` 与 pending live events

完整的 recoveryId、cut vector、snapshot atomic cut、overflow 和超时语义以
`docs/2026-07-18-event-recovery-rfc.md` 为唯一权威定义。`register/register_ack` 不参与恢复。

### 8.3 Epoch 安全保障

- `eventId` 格式 `${bridgeEpoch}:${seq}`，重启后 epoch 变化保证全局不重复
- `lastSeenBySession` 使用 `backendId -> sessionId -> cut` 嵌套 map，避免 key 拼接歧义
- `affectedSessions` 返回 `{ backendId, sessionId }[]`

---

## 9. Driver 接口

### 9.1 接口层次

```
DriverCore                    // 所有 driver 必须实现
  └── SessionDriver           // Session 操作（必须实现）
        ├── ModelDriver       // 模型操作（可选）
        ├── PermissionDriver  // 权限操作（可选）
        ├── MutationDriver    // Session 变更操作（可选）
        ├── TodosDriver       // Todos 操作（可选）
        ├── UsageDriver       // Usage 操作（可选）
        ├── ProviderDriver    // Provider 切换（可选）
        ├── PermissionModeDriver  // 权限模式（可选）
        ├── CompressionDriver // 上下文压缩（可选）
        ├── MemoryDriver      // Memory 文件（可选）
        └── DiagnosticDriver  // 诊断（可选）
```

### 9.2 核心接口方法

```javascript
class DriverCore {
  get id() {}           // backendId
  get kind() {}         // backendKind
  get displayName() {}
  get capabilities() {} // string[]
  get descriptor() {}   // { runtimeStatus, sdkAPIStability, configFingerprint }
  async start(ctx) {}
  async stop() {}
  async healthCheck(ctx) {}
}

class SessionDriver extends DriverCore {
  async createSession(ctx, { title, directory, model, agent }) {}
  async resumeSession(ctx, { sessionId, directory }) {}
  async listSessions(ctx, { directory, rootsOnly }) {}
  async getSession(ctx, { sessionId }) {}
  async getSessionMessages(ctx, { sessionId, cursor, limit, includeParts }) {}
  async sendMessage(ctx, { sessionId, content, agent, model, reasoningEffort, attachments, directory }) {}
  async abortGeneration(ctx, { sessionId }) {}
  async listProjects(ctx) {}
}

class ModelDriver extends SessionDriver {
  async listModels(ctx, { directory, sessionId }) {}
  async setModel(ctx, { sessionId, modelId }) {}
  async listAgents(ctx) {}
}

class PermissionDriver extends SessionDriver {
  async resolvePermission(ctx, { sessionId, permissionId, selectedOptionId, message }) {}
}

class MutationDriver extends SessionDriver {
  async renameSession(ctx, { sessionId, title }) {}
  async archiveSession(ctx, { sessionId, archivedAtMillis }) {}
  async shareSession(ctx, { sessionId }) {}
  async deleteSession(ctx, { sessionId }) {}
}

class TodosDriver extends SessionDriver {
  async fetchTodos(ctx, { sessionId }) {}
}

class UsageDriver extends SessionDriver {
  async getUsage(ctx) {}
}

class ProviderDriver extends SessionDriver {
  async listProviders(ctx) {}
  async setProvider(ctx, { providerId, scope, sessionId }) {}
}

class PermissionModeDriver extends SessionDriver {
  async setPermissionMode(ctx, { mode, scope, sessionId, expiresAt, allowedToolPatterns, deniedToolPatterns, confirmToken }) {}
}

class CompressionDriver extends SessionDriver {
  async compressContext(ctx, { sessionId }) {}
}

class MemoryDriver extends SessionDriver {
  async listMemoryFiles(ctx, { directory }) {}
  async readMemoryFile(ctx, { fileId }) {}
  async updateMemoryFile(ctx, { fileId, content, expectedVersion, dryRun }) {}
}

class DiagnosticDriver extends SessionDriver {
  async runDiagnostics(ctx) {}
}
```

### 9.3 ctx（请求上下文）

```javascript
const ctx = {
  requestId: string
  clientId: string
  backendId: string
  now: number
  signal: AbortSignal
  logger: { info, warn, error }
  emit: (event) => void
}
```

---

## 10. Wire Casing 规范

### 10.1 统一协议 wire 层

统一协议 wire 只使用以下 casing：
- `providerId`, `modelId`, `effectiveModelId`, `effectiveProviderId`, `backendKind`, `backendId`, `sessionId`, `requestId`, `configFingerprint`, `createdAtMillis`, `updatedAtMillis`, `archivedAtMillis`, `timestampMillis`, `projectId`, `parentId`

### 10.2 Swift 内部层

Swift 现有代码使用 `ID` 后缀：`providerID`, `modelID`, `effectiveModelID`, `effectiveProviderID`, `projectID`, `parentID`。这些仅允许出现在 Swift 适配层内部。

### 10.3 映射规则

所有 wire → Swift 的字段映射由 `UnifiedBridgeAdapter` 完成：
- wire `providerId` → Swift `providerID`
- wire `modelId` → Swift `modelID`
- wire `effectiveModelId` → Swift `effectiveModelID`
- wire `effectiveProviderId` → Swift `effectiveProviderID`
- wire `projectId` → Swift `projectID`
- wire `parentId` → Swift `parentID`
- wire `createdAtMillis` → Swift `createdAt` (`Date`)
- wire `updatedAtMillis` → Swift `updatedAt` (`Date`)
- wire `archivedAtMillis` → Swift `archivedAt` (`Date?`)
- wire `timestampMillis` → Swift `timestamp` (`Date`)

---

## 11. 高级功能协议定义

以下功能在协议层面定义完整，但实现由 feature gate 控制。

### 11.1 Usage Reporting (Phase 2b)

```ts
type UnifiedUsageReport = {
  totalTokensUsed: number
  inputTokens: number
  outputTokens: number
  cacheReadTokens?: number
  cacheCreationTokens?: number
  estimatedCost?: { amount: number, currency: string }
  period?: { since: string, until: string }
  limits?: {
    dailyTokenLimit?: number
    dailyTokensRemaining?: number
    rateLimitRemaining?: number
    rateLimitResetAt?: string
  }
  perSessionBreakdown?: Array<{ sessionId: string, tokensUsed: number, cost?: number }>
}
```

### 11.2 Provider 切换 (Phase 5B)

```ts
type UnifiedProvider = {
  id: string
  name: string
  baseURL?: string
  isDefault: boolean
  isActive: boolean
  models?: string[]
  status: 'available' | 'unavailable' | 'not_configured'
  configHint?: string
}
```

`set_provider` 只定义 `session` / `backendDefault` scope。单次 override 通过 `send_message.params.model.providerId`。

### 11.3 Permission Mode (Phase 5C)

`set_permission_mode` 支持 session-scoped yolo 模式。安全约束：二次确认、tool pattern 过滤、过期机制、deniedToolPatterns 默认排除 `bash`/`rm*`。

### 11.4 Context Compression (Phase 5A)

`compress_context` 触发 `context_compressing` / `context_compressed` 事件链。压缩期间 session 进入 `compacting` 状态。

### 11.5 Memory File (Phase 2b 只读, Phase 5D 写入)

```ts
type UnifiedMemoryFile = {
  fileId: string
  fileName: string
  description?: string
  sizeBytes: number
  lastModifiedAt: string
  etag: string
  scope: 'project' | 'user' | 'global'
  writable: boolean
  content?: string
}
```

安全边界：白名单、路径规范化、etag 乐观锁、敏感内容过滤、审计日志。

### 11.6 Doctor Diagnostics (Phase 2b)

```ts
type DiagnosticCheck = {
  checkId: string
  name: string
  status: 'passed' | 'failed' | 'warning' | 'running'
  message: string
  severity: 'required' | 'recommended' | 'optional'
  fixSuggestion?: string
}
```

所有事件通过 `diagnosticRunId` 关联，支持连续多次诊断。

### 11.7 Session Turn Detail 按需加载（`session_turn_items`，2026-08-30 冻结）

适用 backend：**v1 仅 `codex-remote`**（BackendKind `codex-remote`，上游历史为 paginated
`historyMode`）。目标：首屏只载 Summary；回合明细（reasoning 摘要、工具调用与执行步骤）按需
拉取、经投影 Kernel 原子提交、由既有 `projection_snapshot`/`projection_patch` 管道交付——
**不新增第二条内容管道**。投影侧 wire 形状（`turnStateOps` patch op、turn 级
`detailLoadState`/`detailReasonCode`/`generation`、apply 顺序、per-connection 交付规则）冻结于
`docs/protocol/bridge-v1.md`（Capability: `turn_detail_lazy_v1`）。

#### 方法归属与门控

- `session_turn_items` 仅在 backend descriptor `capabilities` 含 `turn_detail_lazy_v1` 时可
  调用（v1 只有 codex-remote 的 paginated historyMode 声明）；客户端以 descriptor 列表门控，
  不以全局 echo 门控（与 `projection_window_v1` 同规）。
- 其他 backend、或未声明该 capability 的 codex-remote（如 legacy historyMode）调用 →
  `unsupported_capability`（不可重试），不触发任何上游拉取。

#### 请求与响应

```jsonc
// 请求
{ "method": "session_turn_items", "backendId": "codex-remote",
  "params": { "sessionId": "sess-1", "turnId": "turn-42" } }

// 成功形状 ack —— loaded 与 failed 都是 success 形状
{ "detailLoadState": "loaded", "syncRev": 128 }
{ "detailLoadState": "failed", "syncRev": 129, "reasonCode": "timeout" }
```

- **canonical items 永不进入 result**：投影 snapshot/patch 是唯一内容写者。
- **过程性失败一律 success-shaped failed ack**（携带该失败 commit 的 `syncRev` 与
  `reasonCode`）：上游 RPC 错误、资源门超限、不可映射 item、fence stale、orphan 恢复。
- 仅**请求级错误**返回 UnifiedError：`unknown_backend` / `session_not_found` /
  `turn_not_found`（`turnId` 不在该 session 已提交 Kernel 中——**以 Kernel 裁决，不询问
  上游**）/ `invalid_params`（参数缺失或目标 turn 非 completed——不触发拉取）。
- **与上游过滤语义的消解记录**：G0 实测官方 `thread/items/list` 对未知 turnId 返回**空成功
  页**（`thread_processor.rs` 过滤语义，rust-v0.150.0-alpha.12.2）。bridge 的
  `turn_not_found` 以 Kernel 为准：进入 Kernel 的 turnId 均来自上游 Summary 页，"Kernel 已知
  而上游未知"正常不出现；若上游对 Kernel 已知 turn 返回空页，按空明细处理（见下），绝不当
  notFound、也不当错误。

#### 状态机与顺序无关完成条件

1. 请求受理后，Mac 先把该 turn 的 `detailLoadState=loading` 提交进投影 SoT（singleflight
   follower 观察同一状态，不发第二次拉取）；
2. 成功时在**同一 Kernel transaction** 提交 `replace_parts + loaded`，得 `syncRev=N`；ack 只
   在该 commit 成功后返回；
3. iOS 完成条件 = replica `appliedRev >= N`；patch 先于/后于 ack 均可；**不得从 RPC result
   渲染内容**；断线后只收到 snapshot 也能恢复（`detailLoadState` 随 turn 下发）；
4. 失败时提交 `failed + reasonCode`（同样得到 commit `syncRev`），再返回 failed ack；
   重试迁移 `failed → loading`；
5. fence：会话删除 → `session_not_found`（请求级）；archive / turn generation 改变 / 目标不再
   是同一 completed turn → failed ack `reasonCode=stale_turn`（typed stale，保留新 truth，
   旧请求不覆盖）。

#### 幂等与 singleflight

- singleflight 键 = `(sessionId, turnId)`；leader 处于 loading 时，follower **等待同一
  terminal commit** 并返回**相同 terminal `syncRev`**（loaded 或 failed）；**不得**返回中间
  loading ack。
- 已 `loaded` 的重复请求直接返回当前 loaded ack（携带原 commit 的 `syncRev`），不重新拉取。
  实现细则（T2.3 落地时定案）：原 commit rev 从进程内 revision journal 恢复；journal 因
  有界保留（128 patches/2MB/30min）淘汰该条目时，退回**当前 `syncRev`** 作为保守水位——
  `appliedRev >= 当前` 蕴含 `appliedRev >= 原 commit rev`，永不提前，只可能多等。

#### orphan loading 恢复

- bridge 在 loading commit 后崩溃/重启时，restore 必须把**没有 active leader** 的 orphan
  loading **原子恢复为 `failed(reasonCode=interrupted)`**，不得永久停在 loading。

#### 资源门（G0 owner 裁决冻结值，2026-08-30）

- turns page limit **30**；items request limit **5**；单回合 **24 页或 512KB** 任一先到即
  **原子失败**；单 RPC **30 秒**；整个单回合拉取 **90 秒总 deadline**（不是 24 × 30s）。
- 超限分别返回 `max_pages` / `max_bytes` / `timeout`；**不截断、不提交部分明细、不以
  placeholder 代替超大 tool output**；后续只能依据真实 `resource_limit` 触发数据调整，
  不得自动扩大。

#### reasonCode 冻结闭集（v1）

`upstream_error | unsupported_item_type | max_pages | max_bytes | timeout | stale_turn |
interrupted`。producer 不得发送闭集外值；iOS 对未知 code 按通用失败渲染（前向兼容）。

#### 明细口径

- **空明细也是 `loaded`**：空明细 = 去除与 Summary 槽位重复的 user/final-agent 后，没有
  reasoning/tool/fileChange 等明细 item（上游 items 数组为空只是其子集）。
  实现细则（T2.2 落地时定案）：空明细的 loaded 是 **state-only commit**（patch 只携带
  `turnStateOps`，无 `partOps`）——绝不执行会把 Summary final-agent 正文抹掉的空
  `replace_parts`。
- 未知/不可映射 item 类型 → **整回合原子失败** `unsupported_item_type`：中止本回合 commit、
  保留原 Summary parts、不执行部分 `replace_parts`、不标 `loaded`（`SkippedTypes` 仅为诊断
  字段，不是"丢弃后继续成功"的开关）；修复/升级后允许重试。
- 产品承诺口径（G0.5 裁决）：按需加载**服务端实际提供的** reasoning 摘要与工具调用；不承诺
  "完整思维链/思考原文"。

---

## 12. BackendKind 枚举

统一协议固定使用以下枚举值：

| Wire 值 | 说明 | 当前状态 |
|---------|------|---------|
| `claude_code` | Claude Code / ThinBridge | Phase 1b 实现 |
| `opencode` | OpenCode HTTP server | Phase 1c 实现 |
| `codex` | Codex app-server | Phase 1d 实现 |
| `codex-web` | Codex 官方长驻 app-server（独立 backend，与 `codex` 并存） | Phase 5 实现 |
| `copilot` | Copilot ACP | Phase 3 实现 |
| `unified_bridge` | 统一 Bridge 自身 | — |

---

## 13. 安全策略摘要

### 13.1 Memory File 安全

1. 文件名白名单：只允许 driver 声明的文件名
2. 路径规范化：禁止 `../`、绝对路径、symlink escape
3. 版本校验：写入前必须提交 `expectedVersion`
4. 敏感内容过滤：不允许读写含 API key / token 的文件
5. 审计日志：每次写入记录 who/when/what/fileId/contentSha256

### 13.2 Permission Mode 安全

1. 作用域限定：Phase 5 只实现 session-scoped
2. 粒度控制：`allowedToolPatterns` / `deniedToolPatterns`
3. 过期机制：`expiresAt` 支持限时 yolo
4. 显式确认：切换到 yolo 必须二次确认
5. Source of truth：Permission mode 状态由 Bridge 侧维护
6. 竞态处理：切换到 default 时，已 pending 的权限仍需用户处理
7. 自动批准标识：yolo 下自动批准的事件带 `resolutionSource: "policy"`

### 13.3 Provider 切换安全

1. stateful scope 与 per-request override 分离
2. running session 保护：不允许 `backendDefault` 级别 provider mutation
3. `catalog_invalidated` 必须包含完整 scope 信息
4. 多客户端隔离：不同客户端 session-scoped 切换互不影响

### 13.4 当前协议漂移说明

1. `text_updated` / `reasoning_updated` 是当前 live wire contract 的正式组成部分，用于传递权威全文快照。
2. `session_state_changed` 当前实现同时兼容 `providerId` 与 `effectiveProviderId`，iOS 侧以 `effectiveProviderId` 为准。
3. 模型目录失效事件当前以 `model_catalog_invalidated` 为 live 名称；旧文档中的 `catalog_invalidated` 仅作历史别名参考。
