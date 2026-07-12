# Grok CLI 兼容性证据表（Gate 0）

- **日期**：2026-07-12
- **CLI 版本**：`grok 0.2.93 (f00f96316d4b)`
- **CLI 路径**：`/Users/jacklee/.local/bin/grok`
- **协议**：Agent Client Protocol (ACP) v1，JSON-RPC 2.0 over stdio
- **调查方法**：官方文档（`docs.x.ai/build/`）、ACP 规范（`agentclientprotocol.com/protocol/v1/`）、本机 `--version`/`--help`/`agent --help`/`agent stdio --help` 输出、本机文件系统检查

---

## 1. CLI 存在性与版本

| 检查项 | 结论 | 来源 |
| :--- | :--- | :--- |
| CLI 是否安装 | ✅ 是 | `which grok` → `/Users/jacklee/.local/bin/grok` |
| 版本可输出 | ✅ 是 | `grok --version` → `grok 0.2.93 (f00f96316d4b)` |
| 帮助可输出 | ✅ 是 | `grok --help` 输出完整用法 |
| 会话存储存在 | ✅ 是 | `~/.grok/sessions/` 存在，含按 workspace 路径命名的子目录和 `session_search.sqlite` |
| 配置文件存在 | ✅ 是 | `~/.grok/config.toml` 存在 |

**复现命令**：
```bash
which grok
grok --version
grok --help
ls ~/.grok/sessions/
cat ~/.grok/config.toml
```

---

## 2. 启动方式与 workspace 参数

| 检查项 | 结论 | 来源 |
| :--- | :--- | :--- |
| 交互 TUI 启动 | ✅ `grok [PROMPT]` | `grok --help` |
| Headless 单轮 | ✅ `grok -p/--single <PROMPT>` | `grok --help` |
| Headless 多轮 JSON | ✅ `--output-format json\|streaming-json` | `grok --help` |
| ACP stdio 模式 | ✅ `grok agent stdio` | `grok agent --help` |
| ACP WebSocket serve | ✅ `grok agent serve` | `grok agent --help` |
| ACP headless relay | ✅ `grok agent headless` | `grok agent --help` |
| workspace 参数 | ✅ `--cwd <CWD>` | `grok --help` |
| 模型选择 | ✅ `-m/--model <MODEL>` | `grok --help` |
| 权限模式 | ✅ `--permission-mode <MODE>`，值：`default`, `acceptEdits`, `auto`, `dontAsk`, `bypassPermissions`, `plan` | `grok --help` |
| reasoning effort | ✅ `--reasoning-effort <EFFORT>` | `grok --help` |
| 工具控制 | ✅ `--tools`, `--disallowed-tools`, `--allow`, `--deny` | `grok --help` |
| Leader 共享进程 | ✅ `--leader` / `--no-leader` / `--leader-socket <PATH>` | `grok agent --help` |

**Bridge 推荐入口**：`grok agent stdio`（ACP JSON-RPC over stdin/stdout），与 Zed 编辑器等 IDE 集成方式一致。

---

## 3. 机器可读的流式输出协议

### 3.1 ACP stdio（推荐 bridge 使用）

| 检查项 | 结论 | 来源 |
| :--- | :--- | :--- |
| 协议规范 | ✅ ACP v1，JSON-RPC 2.0 over stdio | [ACP Overview](https://agentclientprotocol.com/protocol/v1/overview)、[xAI Headless & Scripting](https://docs.x.ai/build/cli/headless-scripting) |
| 能力协商 | ✅ `initialize` 方法 | xAI docs + ACP spec |
| 认证 | ✅ `authenticate` 方法 | xAI docs |
| 创建 session | ✅ `session/new` | ACP spec（MUST support） |
| 发送消息 | ✅ `session/prompt` | ACP spec（MUST support） |
| 流式更新 | ✅ `session/update` notification（agent→client） | ACP spec（MUST support） |
| 取消 turn | ✅ `session/cancel` | ACP spec（MUST support） |
| 加载已有 session | ✅ `session/load` | xAI docs |
| 列出 session | ⚠️ `session/list`（MAY support，可选） | ACP spec + xAI docs |

### 3.2 Headless streaming-json（备选）

`--output-format streaming-json` 提供行分隔 JSON 事件流，适合简单脚本，但不支持外部 permission response 或 session resume。

**结论**：ACP stdio 是 bridge 的正确入口。它是结构化的 JSON-RPC 协议，不是终端文本解析。

---

## 4. session/update 事件 schema

ACP v1 `session/update` notification 包含以下更新类型（来自 [ACP Prompt Turn](https://agentclientprotocol.com/protocol/v1/prompt-turn)）：

| ACP update 类型 | 对应 `core.Event` | 说明 |
| :--- | :--- | :--- |
| 文本增量（content text delta） | `EventText` / `EventTextReplace` | 按 delta 语义映射 |
| 思考增量（thinking delta） | `EventThinking` | reasoning 内容 |
| 计划更新（plan update） | `EventPlan` | plan mode 的计划内容 |
| turn 开始 | `EventTurnStarted` | `session/update` 的 `turn` 字段 = `start` |
| turn 结束 | `EventResult` | `session/update` 的 `turn` 字段 = `end` |
| tool_call created/in_progress | `EventToolUse` | 工具开始 |
| tool_call completed/failed | `EventToolResult` | 工具结束，含成功/失败 |
| tool_call pending_permission | `EventPermissionRequest` | 需要用户授权 |
| 错误 | `EventError` | turn 级别错误 |
| context 压缩 | `EventContextCompressing` / `EventContextCompressed` | 需确认 Grok 是否产生 |
| context usage | `EventContextUsageUpdated` | 需确认 Grok 是否产生 |

**turn 生命周期**：`session/prompt` 发起 → 多个 `session/update` 流式推送 → `session/prompt` 的 JSON-RPC response（含 `stopReason`）结束。

**实测验证（2026-07-12）**：
- `initialize` ✅ 返回 `agentCapabilities.loadSession: true`、`authMethods: [{id: "cached_token"}]`、`modelState.currentModelId: "grok-4.5"`、context 500K tokens
- `authenticate` ✅ `methodId: "cached_token"` 成功（注意字段名是 `methodId` 不是 `method`）
- `session/new` ✅ 需要 `mcpServers: []`（空数组），返回 `sessionId` (UUID)
- `session/update` ✅ 收到 `available_commands_update` notification——流式推送正常工作
- `session/prompt` ⚠️ 协议正确但本机 xAI 免费额度已用尽（"reached your free Grok Build usage limit"）；需 SuperGrok 订阅或等待额度重置后验证完整 turn

---

## 5. 工具调用与权限审批

| 检查项 | 结论 | 来源 |
| :--- | :--- | :--- |
| tool_call 状态生命周期 | ✅ `created`, `in_progress`, `completed`, `failed`, `cancellable`, `pending_permission` | [ACP Tool Calls](https://agentclientprotocol.com/protocol/v1/tool-calls) |
| 外部权限请求 | ✅ `session/request_permission`（client→agent RPC） | ACP spec + xAI docs |
| 权限可回复 | ✅ client 通过 `session/request_permission` 回复 allow/deny | ACP spec |
| 权限模式 | ✅ `default`, `acceptEdits`, `auto`, `dontAsk`, `bypassPermissions`, `plan` | `grok --help` |
| 结构化提问 | ❌ 无独立 question/choice 协议 | xAI docs：「No external question/choice protocol beyond tool permissions」 |

**映射**：
- `pending_permission` → `EventPermissionRequest` → iOS 显示审批入口
- iOS `resolve_permission` (allow/deny) → `session/request_permission` RPC → Grok 执行或跳过工具
- **不声明 `question_reply` capability**：ACP 没有独立的结构化提问协议

---

## 6. 会话标识与恢复

| 检查项 | 结论 | 来源 |
| :--- | :--- | :--- |
| 稳定 session ID | ✅ UUID | `grok --help`：`--session-id <UUID>` |
| resume session | ✅ `--resume <SESSION_ID>` 或 `--continue` | `grok --help` |
| ACP session/load | ✅ 加载已有 session | xAI docs |
| ACP session/list | ⚠️ 可选（MAY support） | ACP spec |
| fork session | ✅ `--fork-session` | `grok --help` |
| session 导出 | ✅ `grok export <SESSION_ID>` → Markdown | `grok export --help` |
| 本地存储 | ✅ `~/.grok/sessions/` 按 workspace 路径组织 + `session_search.sqlite` | 本机检查 |

**注意**：`session/list` 在 ACP spec 中是 MAY（可选）。**2026-07-12 实测**：`session/list` → `Method not found`（code -32601）；`initialize` 未声明 `sessionCapabilities.list`。  
**v1 决策（方案 1）**：MacBridge `ListSessions` 读本地 `~/.grok/sessions/`（summary.json + 可选 session_search.sqlite），并实现 `HistoryProvider` 读 `chat_history.jsonl`，以便 iOS 列出并续接 Mac 端 session。不再因缺少 ACP list 而返回 `ErrNotSupported`。

---

## 7. 其他能力

| 能力 | 是否存在 | 来源 |
| :--- | :--- | :--- |
| 模型选择 | ✅ `-m/--model` | `grok --help` |
| permission mode 切换 | ✅ `--permission-mode` | `grok --help` |
| reasoning effort | ✅ `--reasoning-effort` | `grok --help` |
| structured output | ✅ `--json-schema` + `--output-format json` | `grok --help`（headless 模式） |
| subagents | ✅ `--no-subagents` 可关闭，默认开启 | `grok --help` |
| memory | ✅ `--experimental-memory` / `--no-memory` | `grok --help` |
| worktree | ✅ `--worktree` / `--worktree-ref` | `grok --help` |
| plan mode | ✅ `--no-plan` 可关闭 | `grok --help` |
| sandbox | ✅ `--sandbox <PROFILE>` | `grok --help` |
| web search | ✅ `--disable-web-search` 可关闭 | `grok --help` |
| todos | ❓ 未在 CLI help 中发现独立 todo 协议 | 需在 ACP session/update 中确认 |
| usage/token reporting | ❓ 未在 CLI help 中发现 | 需在 ACP session/update 中确认 |
| workspace diff | ❓ 未在 CLI help 中发现 | 需在 ACP session/update 中确认 |

---

## 8. 许可与隐私

| 检查项 | 结论 | 来源 |
| :--- | :--- | :--- |
| 本地运行 | ✅ CLI 在用户 Mac 本地运行 | CLI 架构 |
| 会话本地存储 | ✅ `~/.grok/sessions/` | 本机检查 |
| ACP stdio 本地通信 | ✅ JSON-RPC over stdin/stdout，无网络 | ACP spec |
| xAI API 调用 | ⚠️ CLI 调用 xAI API 进行模型推理 | CLI 架构——prompt 内容经 xAI 云端处理 |
| 桥接许可 | ⚠️ 需确认 xAI ToS 是否允许第三方 bridge 通过 ACP 控制 CLI | 需阅读 xAI Terms of Service |

**隐私边界**：ACP stdio 传输的 session/prompt 内容、tool_call 参数和 permission 请求会经 MacBridge→iOS Relay 传输。这些内容本身也会经 Grok CLI 发送到 xAI API。Bridge 传输不增加额外的数据暴露面——prompt 已经要发给 xAI 云端。但 Relay 传输必须使用端到端加密（现有 HPKE 机制）。

---

## 9. Grok → core.Event 映射表

| Grok ACP 事件/字段 | core.Event | 映射确定性 |
| :--- | :--- | :--- |
| `session/update` content text delta | `EventText` | 确定 |
| `session/update` content text replace | `EventTextReplace` | 确定（若 ACP 支持 replace 语义） |
| `session/update` thinking delta | `EventThinking` | 确定 |
| `session/update` plan update | `EventPlan` | 确定 |
| `session/update` turn=start | `EventTurnStarted` | 确定 |
| `session/update` turn=end | `EventResult` | 确定 |
| `session/update` tool_call created/in_progress | `EventToolUse` | 确定 |
| `session/update` tool_call completed | `EventToolResult`（成功） | 确定 |
| `session/update` tool_call failed | `EventToolResult`（失败） | 确定 |
| `session/update` tool_call pending_permission | `EventPermissionRequest` | 确定 |
| 错误 / CLI exit 非零 | `EventError` | 确定 |
| `session/cancel` 响应 | `EventResult`（取消） | 确定 |
| context compressing | `EventContextCompressing` | **未支持**（需确认 Grok 是否产生） |
| context compressed | `EventContextCompressed` | **未支持**（需确认 Grok 是否产生） |
| context usage updated | `EventContextUsageUpdated` | **未支持**（需确认 Grok 是否产生） |
| 结构化提问 | `EventQuestionAsked` | **未支持**（ACP 无独立 question 协议） |
| 提问解决 | `EventQuestionResolved` | **未支持**（ACP 无独立 question 协议） |

---

## 10. Gate Verdict

### **pass**

理由：

1. ✅ **结构化流式输出**：Grok Build CLI 实现 ACP v1（JSON-RPC 2.0 over stdio），提供 `session/update` 流式事件，可区分文本、思考、计划、工具调用、完成、错误和取消。不是终端文本解析。
2. ✅ **稳定会话策略**：UUID 标识 session，`session/load` 可恢复，`--resume`/`--continue` 可从 CLI 恢复，`~/.grok/sessions/` 本地持久化。`session/list` 可选但可能支持。
3. ✅ **取消方式**：`session/cancel` RPC 可取消当前 turn。
4. ✅ **可回复审批接口**：`session/request_permission` 提供外部可回复的 allow/deny 闭环，`pending_permission` 状态标识需要授权的工具调用。
5. ✅ **许可与隐私**：CLI 本地运行，ACP stdio 本地通信，会话本地存储。Bridge 不增加额外数据暴露面（prompt 本身已发给 xAI API）。需确认 xAI ToS 桥接条款，但这不阻塞技术实现。

### 前置条件

- Driver 使用 `grok agent stdio` 作为唯一入口，不解析 TUI 或 headless 文本输出。
- `session/list` 需在 Driver 实现时实测；不支持则返回 `core.ErrNotSupported`。
- 不声明 `question_reply` capability（ACP 无独立 question 协议）。
- `permission_resolve` 仅在 Driver 实现 `core.ToolAuthorizer`（封装 `session/request_permission`）后声明。
- `todos`、`usage_reporting`、`workspace_diff` 需在 ACP session/update 实测中确认对应字段后才声明。
- xAI ToS 桥接条款需在发布前由 owner 确认。

### 未验证项（不阻塞 Gate pass，需在 Phase 2/5 补充）

- ACP `session/update` 的精确 JSON schema（需在 Phase 2 用 `grok agent stdio` + `--debug` 实测捕获）
- `session/list` 是否实际支持
- `todos`/`usage`/`workspace_diff` 是否在 `session/update` 中有对应字段
- 真实 prompt 的端到端流（需 owner 授权账户后验证）
