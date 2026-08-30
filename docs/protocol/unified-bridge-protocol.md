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
| `session_turn_items` | `backendId`, `sessionId`, `turnId` | `turn_detail_lazy_v1`（§11.7，deprecated）或 `turn_detail_chunks_v1`（§11.8 终案）；仅 codex-remote descriptor 声明 |
| `turn_output_chunk` | `backendId`, `sessionId`, `turnId`, `itemId`, `chunkIndex` | `turn_detail_chunks_v1`（§11.8；blob 二级懒加载） |

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

- **进程内恢复（当前必测）**：loading commit 后 leader 消失（请求取消、连接断开、fetch 异常
  退出）而 bridge 存活时，下一次请求路径发现 loading 且无 in-flight leader，必须**先**原子提交
  `failed(reasonCode=interrupted)` 再重试，不得永久停在 loading。
- **restore 扫描（仅适用于持久化完整 Projection checkpoint 的拓扑）**：bridge 在 loading
  commit 后崩溃/重启、且重启会从持久化 checkpoint restore 投影时，restore 必须把**没有
  active leader** 的 orphan loading **原子恢复为 `failed(reasonCode=interrupted)`**。当前
  codex-remote 拓扑 pathless（无完整 Projection checkpoint）：重启后由上游重新 Summary
  hydrate 重建，`detailLoadState` 回到 `notRequested`——该条款不是当前真机必测项，
  `RecoverOrphanDetailLoading` 是未来引入 checkpoint 后的准入钩子。

#### 资源门（G0 owner 裁决冻结值，2026-08-30；终局裁决 2026-08-30 深夜）

- turns page limit **30**；items request limit **5**；单回合 **24 页或 512KB** 任一先到即
  **原子失败**；单 RPC **30 秒**；整个单回合拉取 **90 秒总 deadline**（不是 24 × 30s）。
- 超限分别返回 `max_pages` / `max_bytes` / `timeout`；**不截断、不提交部分明细、不以
  placeholder 代替超大 tool output**；后续只能依据真实 `resource_limit` 触发数据调整，
  不得自动扩大。
- **终局裁决（G3 真机验收触发，plan §3.0 owner 裁决记录第 5 条为准）**：上述门限为
  **临时安全门**——终案 = 官方 cursor 分页 + 有界增量提交 + 瞬时资源门（单页/单 patch/
  驻留内存/单 RPC/临时存储/取消清理），废止 512KB 作为整回合永久查看上限；取证据步骤
  = agent 层 `turn items metrics` 逐页计量日志（已落地，不记录正文）。终案落地前
  本节数值门继续生效（fail-closed），**不得单独调大 512KB**（fence/journal/relay 的
  2MB 级限制会把失败点后移而非根治）。

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

### 11.8 Session Turn Detail 增量分层加载（终案，`turn_detail_chunks_v1`，2026-08-30 owner 终审冻结）

> §11.7 的 `turn_detail_lazy_v1`（整回合原子 `replace_parts` 进 Kernel、经
> projection_patch 管道交付）在 v2 客户端上线后**过渡期保留（deprecated）**；新客户端
> 以 descriptor 声明 `turn_detail_chunks_v1` 时**必须走本节**。§11.7 的方法门控、
> `turn_not_found` Kernel 裁决、singleflight、orphan 恢复语义全部继承；**交付模型、
> 资源门、reasonCode 闭集以本节为准**。

#### 设计原则：默认轻量（owner 终审裁决 2026-08-30）

封闭取证（`docs/2026-08-30-codex-remote-turn-items-closed-evidence.md`）证明合法回合
≥128 页/5.7MB 且 raw→projection 无放大。**明细全文不得永久写入默认 Session Projection
Kernel**——否则大回合加载完成后，后续 snapshot、重连与另一台 iPhone 会重复携带全部明细，
违背"默认轻量打开"。数据分层：

| 层 | 内容 | 生命周期 |
|----|------|----------|
| 主 Session Projection（Kernel） | Summary parts + `detailLoadState` + `generation` + **manifest 摘要**（manifestRev/itemCount/totalBytes/进度） | 随会话投影 |
| Mac detail cache | 分页明细全文（items 追加日志）+ 超大 output blob | 磁盘持久，LRU 淘汰可重建 |
| iOS detail overlay | 请求连接按 chunk 接收的明细内容 | **connection-scoped**，断线即弃，重连按需续拉 |

非请求连接**不接收**明细 chunk；默认 `projection_snapshot`/`projection_patch` **永不携带**
明细全文。

#### 冻结参数（owner 终审，2026-08-30）

| 参数 | 值 | 说明 |
|------|-----|------|
| item/blob 分界 | 序列化后 **256KB** | 超过的 item 提取为 blob，主明细只留摘要+预览+handle |
| detail chunk 目标 | **128KB** | 单 chunk 交付体量 |
| 单 chunk/patch 建议上限 | **256KB** | 超过即拆分 |
| 单 patch 编码后硬顶 | **512KB** | 绝对上限，不得突破 |
| 单 upstream page raw 兜底 | **4MB** | **仅单响应异常保护，绝不得用作整回合上限** |
| 单页 RPC timeout | **30s** | |
| 单次加载批次 deadline | **90s** | 到期保存进度续传，**不是失败** |
| 页数 / 整回合累计字节 | **均不设永久上限** | 废止 24 页/512KB 永久门 |
| blob 预览 | 2KB（const 可调） | 折叠行展示 |
| **detail cache 总预算** | 128MB（**初始默认值，非证据冻结**） | 覆盖 manifests + item logs + blobs + 临时事务文件**全部**；**淘汰粒度 = 整个回合的 detail cache 目录**（TTL/LRU 按整目录 last-access）；被淘汰回合可从官方分页按需重建 |

达到时间/内存/批次预算 → **保存进度续传**，不得标记 `failed(max_bytes/max_pages)`
（这两个 reasonCode 从 v2 闭集**移除**）。

#### 分块编码规则（F1.1 冻结）

- chunk 边界**绝不落在 UTF-8 rune 中间**（切断点回退到 rune 起始）；
- 每个 chunk 的尺寸按 **JSON 转义后的 wire 形态**复核：超过 256KB 建议上限即继续
  rune 对齐二分，直至转义后 ≤ 256KB（转义密集文本得到更短的原始 chunk，而不是
  超限帧）；
- 任何编码后 envelope 绝对 ≤ 512KB 硬顶（帧组装层整体复核）；
- `totalChunks` 由**实际 chunk 偏移表**计算（`len(offsets)-1`），**禁止**
  `ceil(totalBytes/128KB)` 近似——rune 对齐与转义复核都会改变切点。

#### 状态机（v2）

`notRequested → loading(含进度) → partial → loaded`；任一步可入
`failed(reasonCode)`（可重试，已加载内容保留）。partial = 已有持久进度、批次结束待续。
上游停摆时**已加载明细继续保留**，UI 显示"已加载 X 项，继续重试"，不得清空回到统一
"加载失败"。Kernel turn 上的 manifest 摘要字段（`detailLoadState`/`manifestRev`/
`itemCount`/`totalBytes`）随 `turnStateOps` 的 manifest op 原子提交。

**Kernel 状态规则（F1.1 P1-4，依当前 Kernel 状态校验，非字段级）**：同一 generation 内
manifestRev/itemCount/totalBytes **单调不倒退**（failed/重试 op 必须携带保留中的
manifest 全量，零值清空即拒绝）；`loaded` 在同一 generation 内是**终态**（仅接受同
manifestRev 的幂等重复；partial/loading/failed 回退全部拒绝）。generation 变更
（回合重新激活）在 bump 提交点重置 manifest 基线（新 truth = 全新明细状态），故上述
规则天然按 generation 生效。*实现锚点（F3，6b25f0e）*：以上规则的唯一提交通道 =
`CommitTurnStateOpsV2`（校验先行 + P0-1 drain-first + 单 rev state-only patch）；
bump 重置位 = detail merge 的 `TurnGeneration++`；checkpoint 升 **v12**（restore
fail-closed 校验 turn detail 字段一致性，v11 重建）；restore 孤儿恢复 = `loading →
failed(interrupted)` **携带保留 manifest**（partial 不是孤儿——进度在 detail store，
续传即可）。

#### `session_turn_items`（v2 语义）

请求形状不变。ack = **本批次的终态 + 本批交付的 chunk 序列范围**（批次期间的增量内容
经 `turn_detail_chunk` 帧流式交付，不在 ack 里）：

```jsonc
// 批次因 deadline 暂停（续传，不是失败）
{ "detailLoadState": "partial", "syncRev": 130, "manifestRev": 7,
  "deliveryId": "d-17", "firstChunkSeq": 20, "lastChunkSeq": 27,
  "progress": { "pages": 46, "items": 227, "bytes": 937241, "eof": false } }
// EOF
{ "detailLoadState": "loaded", "syncRev": 131, "manifestRev": 8,
  "deliveryId": "d-17", "firstChunkSeq": 20, "lastChunkSeq": 31,
  "progress": { "pages": 52, "items": 260, "bytes": 1020000, "eof": true } }
// 可重试失败（进度保留在 detail cache 与 manifest；本批未交付 chunk 时范围两端为 0）
{ "detailLoadState": "failed", "syncRev": 132, "reasonCode": "upstream_error",
  "deliveryId": "d-17", "firstChunkSeq": 0, "lastChunkSeq": 0,
  "progress": { "pages": 19, "items": 95, "bytes": 412906, "eof": false } }
```

- `deliveryId`：本次加载尝试（批次）的 bridge 侧不透明标识，跨批次各不相同；客户端
  只接受**当前** `(session, turn, generation, deliveryId)` 的 chunk 帧；
- `firstChunkSeq`/`lastChunkSeq`：本批交付的 chunkSeq 闭区间；本批没有交付任何 chunk
  时两端为 0（此时客户端只依赖 manifestRev+progress）；
- **完成条件（F1.1 P0-3）**：客户端只有在收到连续的
  `[firstChunkSeq, lastChunkSeq]` 帧后才认为本批交付完成；**发现缺口不得假装成功**，
  必须从 detail cache 重放补齐（重发 `session_turn_items` 走 fast-path）；
- 客户端收到 `partial` 自动再次调用（每次调用 = 一个新 90s 批次，从持久 cursor 续传）；
- 可选加法参数 `replaySinceChunkSeq`（int，≥0）：重连/补缺 fast-path 的触发器——存在时
  批次先从 detail cache **确定性重放** chunkSeq > 该值的全部已提交 chunk（重放走存储
  读路径的同源 re-split，不重拉上游），再从持久 cursor 续传剩余部分；缺省 = 纯续传
  （ack 的 `[firstChunkSeq, lastChunkSeq]` 只含本批**新** chunk，与上方冻结示例一致）；
- 请求级错误（`unknown_backend`/`session_not_found`/`turn_not_found`/`invalid_params`）
  与 §11.7 相同。

*实现锚点（F4）*：批次引擎 = `go-bridge/turn_detail_batch_engine.go`；单页原语 =
agent `ReadTurnItemsPage`/`MapTurnItemsPage`（无内部 deadline/无页数字节门，逐页
foreign-turn/unknown-item 原子失败，repeated-cursor 立即失败）；v2 分派在 §11.7
能力门/legacy 门之后按 `turn_detail_chunks_v1` 连接标记优先接管（v1/v2 标记可并存，
v2 优先）。singleflight follower 经 flight 连接表与 leader **同批收帧**、逐字镜像
终态 ack。cursor 失效（空页带 cursor / 全已知页，重扫之外）→ 一次从头重扫，按
canonical itemId 跳过已提交内容（重扫中的全已知页 = 正常前缀跳过，非异常）；同一
批次内第二次异常 → `upstream_error`（进度保留）。generation 轮换 → `DropTurn`
丢弃跨代缓存、从官方分页重建。淘汰重水化守卫 = kernel 与 store 的 manifest 摘要
逐字段取 max 后提交（`mergeTurnSummary`，store 重建从 rev 1 起步时 kernel 高水位
不回退）。

#### `turn_detail_chunk` 帧（专用非 replayable overlay envelope，仅请求连接）

**不是**业务 EventMessage：不携带 `eventId`/顶层 `seq`/`bridgeEpoch`/`perSessionSeq`，
**不进事件缓冲、不参与业务 event sequence、recovery 不重放**。overlay 交付是
连接作用域、可丢失的——丢失由 ack 的 `[firstChunkSeq, lastChunkSeq]` 范围 + detail
cache 重放修复，而不是靠重放语义。

```jsonc
{ "type": "turn_detail_chunk", "backendId": "codex-remote",
  "sessionId": "sess-1", "turnId": "turn-42", "turnGeneration": 3,
  "deliveryId": "d-17", "manifestRev": 7, "chunkSeq": 12,
  "items": [ /* ProjectionPart[]（普通明细 item，转义后 ≤256KB 建议/512KB 硬顶内可多 item） */ ],
  "oversize": [ { "itemId": "…", "handle": "…", "type": "commandExecution",
                  "totalBytes": 1057417, "preview": "…", "totalChunks": 9 } ],
  "progress": { "pages": 46, "items": 227, "bytes": 937241, "eof": false } }
```

- **身份绑定（F1.1 P0-2）**：每帧携带 `(sessionId, turnId, turnGeneration, deliveryId,
  manifestRev)`。客户端只接受与当前持有身份完全一致的帧——回合重新激活
  （generation 变更）或重试（新 deliveryId）后，旧请求的迟到帧被身份比较丢弃；
- `chunkSeq`（刻意不叫 `seq`，避免与任何传输层序号混淆）：per-`(session, turn)` 单调
  递增的 chunk 序号，**跨批次/跨 delivery 连续编号**（全局缺口检测）；
- 客户端按 `chunkSeq` 去重 + 检测缺口；
- 每个 chunk 对应一个成功接纳的上游页（或页的拆分）；
- **只发给请求了该 turn 明细的连接**（per-connection registry）；singleflight follower
  观察同一批次的帧流；
- 重连后重新调用 `session_turn_items`：manifest 已有持久进度时走 **fast-path**——从
  detail cache 重放 chunk（不重拉上游），再从持久 cursor 续传剩余部分；
- chunk 帧不进 revision journal、不进 snapshot——overlay 是唯一客户端载体。

#### `turn_output_chunk` RPC（超大输出二级懒加载）

请求与响应**绑定完整 blob 身份**（F1.1 P0-2：generation + manifestRev + handle +
itemId + chunkIndex）——客户端拒绝任何回显身份与请求不符的响应：

```jsonc
// 请求
{ "method": "turn_output_chunk", "backendId": "codex-remote",
  "params": { "sessionId": "sess-1", "turnId": "turn-42", "turnGeneration": 3,
              "manifestRev": 7, "itemId": "…", "handle": "…", "chunkIndex": 0 } }
// 成功（data envelope，回显完整绑定）
{ "turnGeneration": 3, "manifestRev": 7, "itemId": "…", "handle": "…",
  "chunkIndex": 0, "totalChunks": 9, "totalBytes": 1057417,
  "encoding": "utf-8", "data": "…" }
```

- chunk 按冻结偏移表（见「分块编码规则」）切分；`totalChunks` 来自实际偏移表；
- 按 itemId+chunkIndex 顺序读取 Mac blob cache；
- blob 已被 LRU 淘汰 → UnifiedError `blob_evicted`（可重试）：客户端重新调用
  `session_turn_items`，Mac 从官方分页重建 blob 后再取 chunk；
- **完整内容可达，永不静默截断**（面向模型上下文的官方 output truncation 语义
  不适用于历史 UI）。

*实现锚点（F5）*：RPC = `go-bridge/turn_output_chunk_handler.go`（方法路由
`turn_output_chunk`；绑定三层校验——generation 对 Kernel turn、manifestRev +
itemId+handle 对 store manifest 行、chunkIndex 对落盘偏移表；一切缓存类失配
[目录被淘汰/损坏/换代/manifestRev 变更/handle 无引用/blob 文件缺失]统一收敛为
可重试 `blob_evicted`，客户端重调 `session_turn_items` 后用重建身份重取——
blob handle 绑 generation+内容哈希**不可变**，内容未变的 item 重建后 handle 相同）。
**淘汰重水化**（引擎 `rehydrate` 模式）：loaded 终态回合的 `session_turn_items`
在 store manifest 缺失/损坏/换代/**未到 EOF**（重建被打断）时进入重水化批次——
**不做任何 kernel 状态提交**（loaded 同代终态不可逆；syncRev/manifest 摘要不
动），仅重建 store + 交付 chunk；交付换新 deliveryId、**chunkSeq 从 1 重启**
（被淘汰的旧序号不可恢复——单一 delivery 下连续 [1..last] 即完整 overlay 替换，
F6 契约按此口径）；ack 恒 loaded、manifestRev = 重建 store 的 rev（帧/ref 同源），
重建失败仅记日志（kernel 真相不变，客户端经 blob_evicted 收敛）。tool hydrate
事件不带 turnId（靠流内已建回合归属）：引擎每页**全量重映射累计 scratch** 并
按前缀切片，锚点 = 批次吸收的 user 槽，resumed 批次回退 Kernel Summary user
身份——tool-only 页（含续批首页）不再丢件。

#### detail store 事务模型（F1.1 P1-5 冻结，F2 实现依据）

存储选型：**文件存储 + 固定提交顺序**（不引入 SQLite/WAL）。理由：go-bridge 现无任何
SQL 依赖；mattn/go-sqlite3 需 cgo（破坏纯 Go runtime 分发），modernc.org/sqlite 为
append-only 有界负载引入多 MB 依赖；官方 codex 的 SQLite 在 Rust 宿主侧，不构成 Mac
桥面必须跟随的先例；现有代码库已有原子 rename 纪律（CodexProducerState 模式）。
**固定提交顺序**（崩溃时最多回滚最后一步，启动扫描兜底）：

1. blob 临时文件写入 → fsync → rename 到最终名；
2. items 事务记录（本页 items + 事务头）追加写入 → fsync；
3. manifest 临时文件写入 → fsync → rename（提交点：manifest 指向的 cursor/计数
   只在前两步完成后推进）；
4. 启动时扫描：manifest 未指向的 items 尾部记录与无主 blob = 未提交事务，回滚/清除。

**路径安全**：sessionID/turnID **不得**直接拼磁盘路径——目录名一律
`hex(sha256(rawID))`（防路径穿越/非法字符），manifest 内保留原始 ID 供审计。
**预算**：见冻结参数表「detail cache 总预算」（整回合目录粒度淘汰，128MB 初始默认）。

#### 断点恢复（owner 终审规则）

每个成功接纳的页面**原子保存**：upstream next cursor、已接纳 item ID 集合（或连续
边界）、turn generation、manifest revision、已落盘 blob 信息。cursor 跨 stream/client
epoch 失效时（空页/内容回跳检测）：**从第一页重新翻、按 canonical item ID 跳过已提交
内容、找到重叠边界后继续**——不得假设 cursor 永远有效，也不得因 cursor 失效丢弃已
加载内容。

#### reasonCode 闭集（v2）

`upstream_error | timeout | stale_turn | interrupted | unsupported_item_type |
page_oversize`。`max_pages`/`max_bytes` 已移除（永久门废止）；`page_oversize` = 单
upstream 页 raw 超 4MB 异常兜底。iOS 对未知 code 按通用可重试失败渲染（前向兼容）。

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
