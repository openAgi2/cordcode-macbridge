# go-bridge Claude Code 能力补齐方案

> **前提**：Claude Code 是纯 CLI 进程模型，没有 HTTP API。所有能力必须通过 cc-connect agent 实现，不存在 opencode 那种 HTTP proxy 路径。
>
> **原则**：cc-connect 是核心，go-bridge 只做薄 WS 适配。新能力在 cc-connect 层实现接口，go-bridge 只做路由和映射。
>
> **修订记录**：v4（通过）— 三轮评审通过。补充 archive 接口契约命名提醒。

## 0. 现状

go-bridge 的 Claude Code 已通过 cc-connect `agent/claudecode/` 实现了基础能力：

| 方法 | 状态 | cc-connect 接口 |
|------|------|-----------------|
| create_session | ✅ | `agent.StartSession("")` |
| send_message | ✅ | `sess.Send()` → `relayEvents` |
| abort_generation | ✅ | `sess.Close()` |
| get_session_messages | ⚠️ 只有纯文本 | `HistoryProvider.GetSessionHistory()` |
| resolve_permission | ✅ | `sess.RespondPermission()` |
| list_models / switch_model | ✅ | `ModelSwitcher` |
| list_sessions | ✅ | `agent.ListSessions()` |
| delete_session | ✅ | `SessionDeleter` |
| rename / archive / share | ❌ **假成功** | 返回 ok 但不生效 |

## 1. 缺失能力清单

按修订后的优先级排序：

| 优先级 | 能力 | 说明 |
|--------|------|------|
| **P0** | RichHistoryProvider | get_session_messages 只返回纯文本，丢失 tool_use/tool_result/thinking。iOS MessageWeb 无法渲染工具步骤和推理块 |
| **P0** | 消除 session mutation 假成功 | rename/archive 当前返回 ok 但不生效，是 correctness 问题，不是功能缺失。share 永久不支持 |
| **P1** | memory file 读取 | list_memory_files / read_memory_file |
| **P1** | run_diagnostics | 远程排障能力 |
| **P2** | session transcript token usage | 从 JSONL transcript 累积的历史 token 消耗统计。不覆盖 live context window / remaining context |
| **P2** | 实现 rename/archive | 消除假成功后补上真实实现 |
| **P3** | fetch_content_chunk | 大工具输出分块读取 |
| **P3** | model catalog invalidation | 模型列表刷新事件 |
| **待定** | 账户额度查询 (get_usage) | 需先确定 OAuth/router/provider-managed 模式下的数据来源 |

## 2. Phase 1A：RichHistoryProvider（P0）

### 2.1 目标

让 `get_session_messages` 返回带 parts/steps/thinking 的结构化历史，与 opencode 的 payload schema 对齐。

### 2.2 Claude Code JSONL 结构

Claude Code 的 session 文件（`~/.claude/projects/<path-hash>/<session-id>.jsonl`）每行一个 JSON 对象：

```
type=user        → message.content = string 或 [{type:"text",...}]
type=assistant   → message.content = [{type:"text",...}, {type:"thinking",...}, {type:"tool_use",...}]
type=user        → message.content = [{type:"tool_result", tool_use_id:"...", content:"..."}]
```

关键差异：
- **assistant 消息**的 content 是数组，包含 text、thinking、tool_use 混合块
- **紧跟 tool_use 的 user 消息**包含对应的 tool_result（由 Claude Code 运行时注入，不是真正的用户输入）
- tool_use 块有 `id`、`name`（工具名）、`input`（工具参数）
- tool_result 块有 `tool_use_id`（关联 tool_use）、`content`（输出，可能是纯字符串或 block 数组）
- thinking 块有 `thinking`（推理内容，可能有 signature）

### 2.3 实现计划

**cc-connect 层**（`agent/claudecode/claudecode.go`）：

1. 实现 `RichHistoryProvider` 接口
2. 新增 `GetRichSessionHistory(ctx, sessionID, limit)` 方法
3. 解析 JSONL 时不再丢弃非文本块，构建结构化 `RichHistoryEntry`：
   - 每个 `type:assistant` 的消息 → 一个 `RichHistoryEntry{role:"assistant"}`
   - 把 content 数组中的 text → parts, tool_use → steps, thinking → thinking 字段
   - 每个 `type:user` 的纯文本消息 → 一个 `RichHistoryEntry{role:"user"}`

**go-bridge 层**（`handlers.go`）：

1. `handleGetSessionMessages` 已经有 `RichHistoryProvider` 的偏好逻辑（优先使用 RichHistoryProvider，fallback 到 HistoryProvider）
2. 不需要改路由，只需 cc-connect agent 实现接口即可自动生效

### 2.4 tool_result 合并规则（关键）

**以 `tool_use_id` 为主键合并，不依赖相邻关系。**

解析流程：
1. 第一遍扫描：收集所有 assistant 消息的 content blocks，记录每个 `tool_use` 的 `id`
2. 第二遍扫描：收集所有 user 消息中的 `tool_result`，按 `tool_use_id` 建立查找表
3. 合并：对每个 `tool_use` block，用 `id` 查找对应的 `tool_result`，合并为 step 的 output

降级策略：
- `tool_result` 缺失（JSONL 截断）→ step 标记 `status:"unknown"`，output 为空
- `tool_result.content` 是 block 数组而非字符串 → 拼接所有 text block
- `tool_result.content` 是 JSON 对象 → 序列化为字符串
- 未知 block type → 跳过，不崩溃，打 debug log
- 同一消息多个 text/thinking block → 按顺序拼接

**limit 裁剪规则**：先完整解析并重建逻辑消息，再对最终 `RichHistoryEntry` 数组应用 limit。不要对原始 JSONL 行先裁剪，否则 tool_use 在窗口内但对应 tool_result 被裁掉，误报成缺失/unknown。

### 2.5 映射关系

```
JSONL content block              → go-bridge wire format
─────────────────────────────────────────────────────────
assistant.content[type=text]     → parts: [{type:"text", content:"..."}]
  (多个 text block)              → 按顺序拼接，每个成为独立 part
assistant.content[type=thinking] → parts: [{type:"reasoning", content:"..."}]
                                  + entry.thinking = 拼接所有 thinking text
assistant.content[type=tool_use] → steps: [{id, toolName, status, output}]
                                  + parts: [{type:"tool", step:{...}}]
  + 匹配 tool_result            → step.output 从 tool_result.content 提取
user.content[type=tool_result]   → 不生成独立 entry，合并到对应 tool_use step
user.content (纯文本)            → parts: [{type:"text", content:"..."}]
未知 block type                  → 跳过 + debug log
```

### 2.6 验证

**cc-connect 单测 fixture（至少覆盖以下场景）：**
1. 一个 assistant turn 内多个 `tool_use`（验证多 step 映射）
2. `tool_result` 延后出现（不紧跟 tool_use，验证 id 匹配）
3. `tool_result.content` 不是纯字符串（block array / JSON）
4. 同一消息多个 text/thinking block（验证拼接）
5. 未知 block type（验证跳过不崩溃）
6. 截断/损坏 JSONL（验证降级不崩溃）
7. limit 裁剪后 tool_result 未被误裁（验证 limit 在消息重建后生效）

**go-bridge 单测**：mock RichHistoryProvider，验证 wire payload 与 iOS XCTest 期望一致

**Live smoke**：通过 WS 调 get_session_messages，验证包含工具步骤和推理块

## 3. Phase 1B：消除 Session Mutation 假成功（P0）

### 3.1 问题

当前 go-bridge `handlers.go:252-257` 对 rename/archive/share 返回 `{ok: true}` 但什么都不做。这是 **correctness 问题**：客户端收到成功响应，但后端状态没有变化。

### 3.2 修复方案

**第一步：把 rename/archive stub 改成显式 `not_supported`**

```go
// rename_session — 当前返回 ok 但不生效，先改成 not_supported
conn.SendResult(msg.RequestID, nil, &WireError{
    Code: "not_supported",
    Message: "session rename not yet supported",
})
```

对 archive_session 同理。这样 iOS 端可以正确显示"不支持"而不是假装成功。

**share_session：永久 `not_supported`**

Claude Code 的 share 功能依赖外部服务（Anthropic share endpoint），且 cc-connect 的进程模型不支持直接触发 share 操作。share_session 改为永久返回 `not_supported`，不进入后续实现阶段。

**第二步（P2）：实现真实 rename**

Claude Code 的 JSONL 有 `custom-title` 行：
```json
{"type":"custom-title","title":"新标题","sessionId":"...","timestamp":"..."}
```

实现方式：
1. cc-connect 新增 `SessionRenamer` 接口
2. claudecode agent 实现：在 JSONL 文件中写入 `custom-title` 行
3. 同步修改 `scanSessionMeta` 读取 custom-title 作为 title
4. go-bridge 暴露 `rename_session` RPC

**第二步（P2）：实现 archive**

需要调查 Claude Code 的归档机制（是否有标准字段或目录约定）。如果 JSONL 没有标准归档字段，需要自行定义（如在 session 目录下创建 `.archived` 标记文件）。

**注意**：`ListSessions` 和 `scanSessionMeta` 必须同步更新，否则 rename/archive 后 list_sessions 仍返回旧数据。

### 3.3 验证

- rename 后 list_sessions 能看到新标题
- archive 后 list_sessions 不再显示（或标记为 archived）
- iOS 端收到 `not_supported` 后正确显示 UI 状态

## 4. Phase 2：Memory File + Diagnostics（P1）

每个新增能力必须同时补齐四件事：cc-connect 接口 + go-bridge handler + capability 暴露 + 测试 fixture。

### 4.1 Capability 暴露机制（先做）

当前 `handlers.go:56-83` 的 `BackendList()` 只暴露少数 capability。新增 RPC 前，必须先在 capability 列表里注册。

新增 capability 位：
- `memory_read` — memory file 读取
- `diagnostics` — 远程诊断
- `session_mutation` — rename/archive（实现后启用）
- `usage_reporting` — 用量查询（实现后启用）
- `content_chunking` — 内容分块（实现后启用）

### 4.2 Memory File（P1）

**cc-connect 层**：

`MemoryFileProvider` 已有 `ProjectMemoryFile()` 和 `GlobalMemoryFile()`。需要新增带内容的读取接口：

```go
type MemoryFileReader interface {
    ListMemoryFiles(ctx context.Context, directory string) ([]MemoryFile, error)
    ReadMemoryFile(ctx context.Context, fileID string) (string, error)
}
```

**fileID 设计**（收窄，避免路径耦合）：
- `project:claude` — 项目级 CLAUDE.md
- `global:claude` — 用户级 ~/.claude/CLAUDE.md
- 后续扩展：`project:agents` 等，不改客户端主键语义

**MemoryFile 结构**：
```go
type MemoryFile struct {
    ID            string // "project:claude" / "global:claude"
    Scope         string // "project" / "global"
    FileName      string
    Path          string // 真实路径，供展示
    Exists        bool
    SizeBytes     int64
    LastModified  time.Time
    Writable      bool   // 当前 false
}
```

**go-bridge 层**：
1. 新增 `list_memory_files` RPC handler
2. 新增 `read_memory_file` RPC handler
3. 在 `BackendList()` 添加 `memory_read` capability

### 4.3 Diagnostics（P1）

**cc-connect 层**：

新增接口：
```go
type DiagnosticsProvider interface {
    RunDiagnostics(ctx context.Context) ([]DiagnosticResult, error)
}

type DiagnosticResult struct {
    ID            string // "cli" / "session" / "model_query" / "credential"
    Name          string
    Status        string // "passed" / "failed" / "warning"
    Message       string
    Severity      string // "required" / "optional"
    FixSuggestion string
}
```

**诊断分级**（不做"全有或全无"判断）：
1. CLI 可执行（`claude --version`）— required
2. 基础 session 能启动 — required
3. 模型查询可用 — optional（API 失败不代表 Claude Code 不可用）
4. 凭证/网络状态 — optional（OAuth/router/provider-managed 各不同）

**go-bridge 层**：
1. 新增 `run_diagnostics` RPC handler
2. 逐步发送 `diagnostic_progress` 事件
3. 最终返回 `diagnostic_completed` 结果
4. 在 `BackendList()` 添加 `diagnostics` capability

## 5. Phase 3：Session Transcript Token Usage（P2）

### 5.1 范围定义

本 Phase 只做 **session transcript 的历史 token 累积统计**：从 JSONL 中读取 `type:result` 行的 `usage` 字段，累加为 session 级别的总量。

**本 Phase 不覆盖**：
- live context window 占用 / remaining context（如需要，属于 `ContextUsageReporter` 语义，单列另一项）
- 账户额度查询（鉴权路径不统一，待定）

### 5.2 实现

cc-connect 新增独立接口：
```go
type SessionUsageProvider interface {
    GetSessionTokenUsage(ctx context.Context, sessionID string) (*SessionTokenUsage, error)
}

type SessionTokenUsage struct {
    InputTokens  int
    OutputTokens int
    // 如果可获取：
    CacheReadTokens       int
    CacheCreationTokens   int
}
```

数据来源：从 JSONL 中 `type:result` 行的 `usage` 字段累积。

go-bridge 新增 `get_session_usage` RPC。

**B. 账户额度查询（待定，暂不做）**

需要先确认：
- Claude Code 的 OAuth / router / provider-managed 模式下的鉴权路径
- 是否有一个统一可查的 quota API endpoint
- 如果数据来源不确定，先不做，避免引入不稳定的依赖

### 5.3 验证

- session_usage 查询返回值与 turn_completed 的 inputTokens/outputTokens 对齐
- 多 turn session 的累积值正确

## 6. Phase 4：Session Mutation 实现 + Content Chunking（P2-P3）

### 6.1 Rename 实现（P2）

cc-connect 新增统一 mutation 接口（Phase 4 实现时确定最终命名）：
- rename → `SessionRenamer.RenameSession(ctx, sessionID, newTitle) error`
- archive → `SessionArchiver.ArchiveSession(ctx, sessionID) error` 或合并为 `SessionMutator` 统一接口

当前只明确：Phase 4 实现前必须先定义接口契约，不在编码时临时补命名。

1. cc-connect 新增 `SessionRenamer` 接口
2. claudecode agent：在 JSONL 写入 `custom-title` 行
3. 同步修改 `scanSessionMeta` 读取 custom-title
4. go-bridge 把 stub 替换为真实实现
5. capability 从 not_supported 升级为 `session_mutation`

### 6.2 Archive 实现（P2）

1. 调查 Claude Code 的归档机制
2. 如果 JSONL 没有标准字段，定义本地标记方式
3. `ListSessions` 过滤已归档 session 或标记 availability

### 6.3 Content Chunking（P3）

低优先级。当前 inline 输出对大多数场景够用。如需实现：
1. go-bridge 维护 content cache
2. 新增 `fetch_content_chunk` RPC
3. 在 `BackendList()` 添加 `content_chunking` capability

## 7. 改动面清单

每个 Phase 的完整改动面（cc-connect + go-bridge + iOS）：

| Phase | cc-connect 接口 | cc-connect 实现 | go-bridge handler | go-bridge capability | iOS feature gate |
|-------|-----------------|-----------------|-------------------|---------------------|-----------------|
| 1A | RichHistoryProvider（已有） | claudecode agent | 无改动（已有 fallback） | 无改动（已有 session_history） | 无改动 |
| 1B | 无 | 无 | rename/archive/share 改为 `not_supported` | **不暴露** session_mutation | 识别 not_supported → 显示不支持 |
| 2 | MemoryFileReader + DiagnosticsProvider | claudecode agent | list_memory_files / read_memory_file / run_diagnostics | memory_read / diagnostics | 检查 capability 显示对应 UI |
| 3 | SessionUsageProvider（新增） | claudecode agent | get_session_usage | usage_reporting | 显示 token 统计 |
| 4 | SessionRenamer（新增） | rename/archive 实现 | rename/archive 真实实现 | session_mutation | 识别 capability 后显示 UI |

## 8. 不做的事情

| 能力 | 原因 |
|------|------|
| `TodoProvider` / `AgentLister` | Claude Code 不适用，不是多 agent 系统 |
| **share_session** | Claude Code 的 share 依赖外部服务（Anthropic share endpoint），cc-connect 进程模型不支持。永久 `not_supported` |
| 账户额度查询 (UsageReporter) | 鉴权路径不统一（OAuth/router/provider-proxy），数据来源不确定，待定 |
| live context window / remaining context | 属于 `ContextUsageReporter` 语义，与 transcript token usage 是不同能力，如需要单列 |
| model catalog invalidation | 低频，手动刷新够用 |
| Provider proxy 暴露 | cc-connect 已有实现，暂不需要通过 go-bridge 暴露 |

## 9. 风险

1. **Claude Code JSONL 格式不稳定** — JSONL 是 Claude Code 内部格式，可能随版本变化。解析必须 robust：跳过未知字段，不崩溃，打 debug log
2. **tool_use / tool_result 关联** — 以 `tool_use_id` 为主键合并，不依赖相邻关系。JSONL 截断时 tool_result 可能缺失，需要降级处理
3. **thinking 内容有 signature** — Claude Code 的 thinking 块有 `signature` 字段，只透传 `thinking` 文本部分
4. **custom-title 行格式** — 需要确认 `custom-title` 的 JSONL 格式是否稳定，写入时需保留其他字段
5. **MemoryFileReader 的 fileID 设计** — 必须稳定，后续扩展不能改已有 fileID 的语义

## 10. 参考文件

| 文件 | 用途 |
|------|------|
| `go-bridge/handlers.go:903-943` | 当前 get_session_messages 实现（RichHistoryProvider fallback 逻辑） |
| `go-bridge/handlers.go:56-83` | BackendList capability 暴露（需补齐新能力位） |
| `go-bridge/handlers.go:252-257` | rename/archive/share stub（假成功，需消除） |
| `go-bridge/opencode-proxy.go:343-490` | opencode 的 mapMessage 参考（parts/steps 映射） |
| `/cc-connect/agent/claudecode/claudecode.go:535-591` | 当前 GetSessionHistory 实现（纯文本，需升级） |
| `/cc-connect/agent/claudecode/claudecode.go:595-621` | extractTextContent（只取第一个 text block，需修改） |
| `/cc-connect/agent/claudecode/claudecode.go:417-469` | ListSessions / scanSessionMeta（rename 后需同步更新） |
| `/cc-connect/core/interfaces.go:291-296` | RichHistoryProvider 接口定义 |
| `/cc-connect/core/interfaces.go:365-404` | UsageReporter 接口（注意语义，不适合 session token 查询） |
| `/cc-connect/core/message.go:213-230` | RichHistoryEntry 数据结构 |
| `go-bridge 框架现状.md` | 架构约束（cc-connect 是核心） |
| `转向 cc-connect 路线的原因.md` | Node.js bridge 病灶（避免重蹈覆辙） |
| `go-bridge-claude-code-能力补齐方案-评审报告.md` | 本次修订的依据 |
