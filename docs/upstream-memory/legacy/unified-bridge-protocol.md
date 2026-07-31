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
  ],
  "lastBridgeEpoch": "epoch-20260427-001",
  "lastEventId": "epoch-20260427-001:12345",
  "lastSeenBySession": {
    "claude:sess-1": { "eventId": "epoch-20260427-001:12340", "seq": 12340 },
    "opencode:sess-2": { "eventId": "epoch-20260427-001:12345", "seq": 12345 }
  }
}
```

**字段说明：**
- `client.id`: 跨重连稳定的 UUID
- `lastBridgeEpoch`: 上次连接的 bridge epoch；缺失或无效视为全新连接
- `lastEventId`: 含 epoch 的事件 ID，格式 `${bridgeEpoch}:${seq}`
- `lastSeenBySession`: `<backendId>:<sessionId>` 复合 key，避免跨 backend 串线

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
    }
  ],
  "recovery": null
}
```

**`backends[].capabilities`** 只暴露当前 build 中可实际调用的方法能力；未来 phase 的预留能力在 feature gate 打开前不得提前 advertise。

**`backends[].kind`** 使用冻结枚举：`claude_code` | `opencode` | `codex` | `copilot` | `unified_bridge`

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

---

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

所有事件带 `eventId`（含 epoch）、`seq`、`backendId`，支持断线重连。

### 5.1 事件信封格式

```jsonc
{
  "type": "event",
  "eventId": "epoch-20260427-001:12346",
  "seq": 12346,
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
}
```

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
  supportedReasoningEfforts?: ('minimal'|'low'|'medium'|'high'|'xhigh')[]
  defaultReasoningEffort?: 'minimal'|'low'|'medium'|'high'|'xhigh'
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
  | { type: 'text', content: string }
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
2. 发送 `register`，带 `lastBridgeEpoch` / `lastEventId` / `lastSeenBySession`
3. 服务端判断：

| 条件 | 行为 | register_ack.recovery |
|------|------|-----------------------|
| `lastBridgeEpoch` == 当前 && `lastEventId` 在 buffer 内 | replay 缓存事件，继续实时推送 | `null` |
| `lastBridgeEpoch` == 当前 && `lastEventId` 不在 buffer 内 | 返回受影响 session 列表 | `{ "type": "snapshot_required", "affectedSessions": [{ backendId, sessionId }] }` |
| `lastBridgeEpoch` != 当前（Bridge 重启了） | 全量刷新 | `{ "type": "full_resync" }` |
| `lastBridgeEpoch` 缺失或无效 | 当作全新连接 | `{ "type": "full_resync" }` |

### 8.3 Epoch 安全保障

- `eventId` 格式 `${bridgeEpoch}:${seq}`，重启后 epoch 变化保证全局不重复
- `lastSeenBySession` 使用 `${backendId}:${sessionId}` 复合 key
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

---

## 12. BackendKind 枚举

统一协议固定使用以下枚举值：

| Wire 值 | 说明 | 当前状态 |
|---------|------|---------|
| `claude_code` | Claude Code / ThinBridge | Phase 1b 实现 |
| `opencode` | OpenCode HTTP server | Phase 1c 实现 |
| `codex` | Codex app-server | Phase 1d 实现 |
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
