# CLI 2.1.234 静态提取：CCR 端点族 / 事件名 / 传输线索（2026-09-05）

来源：PATH CLI 二进制 `strings`（584091 行文本落盘
`/tmp/rc-probe/cli-2.1.234-strings.txt`，未入库；以下为提取物）。2.1.260
（Desktop 内嵌）复检：`/v1/code/sessions`（26 处）与 `/v2/ccr-sessions*`
同样存在，面一致。

## 1. CLI 内嵌的 RC 客户端实现代码（minified 上下文摘录）

CLI 自己就实现了「远程客户端」调用族（Bearer accessToken）：

```
GET  ${BASE_API_URL}/v1/code/sessions                       （列会话）
GET  ${BASE_API_URL}/v1/code/sessions/${e}                  （详情）
POST ${BASE_API_URL}/v1/code/sessions/${e}/events           （"Sending event to session ${e}"）
POST ${BASE_API_URL}/v1/code/sessions/${e}                  （"[updateSessionTitle] Updating title ..."）
POST ${BASE_API_URL}/v1/code/sessions/${e}/mark_read        （body: {event_id}）
POST ${BASE_API_URL}/v1/code/sessions/${e}/client/presence  （body: {client_id, clear}）
POST ${BASE_API_URL}/v1/code/sessions/${e}/archive
GET  ${BASE_API_URL}/v1/code/sessions/${e}/events           （"for polling" + 分页循环, 上限 50 次）
GET  ${BASE_API_URL}/v1/code/sessions/${this.sessionId}/events/stream
     ?from_sequence_num=${this.lastSequenceNum}             （SSE 游标续传）
```

## 2. v2 面（claude.ai 内部网关路径族）

```
/v2/auth
/v2/ccr-sessions/
/v2/ccr-sessions/-/chat-project
/v2/ccr-sessions/-/meta/mcp
/v2/session_ingress/shttp/mcp/
/v2/session_ingress/mcp/ws/          ← WebSocket ingress（MCP 面）
```

同一代码块出现 `new Set(["bridge.claudeusercontent.com",
"bridge-staging.claudeusercontent.com"])` 与 `wss://bridge.claudeusercontent.com`。

## 3. 事件模型（SSE 帧类型，字符串计数）

```
"tool_use" ×263
"permission_request" ×4
event 信封: "event_start" / "event_delta"（live-preview，配 event_deltas[]=agent.message）
"session_update"
agent.* 族: agent.message / agent.thinking / agent.tool_use / agent.tool_result /
  agent.mcp_tool_use / agent.mcp_tool_result / agent.custom_tool_use / agent.source /
  agent.thread_message_sent / agent.thread_message_received /
  agent.thread_context_compacted / agent.session_thread_message_sent /
  agent.session_thread_message_received / agent.name
```

## 4. `ccr-*` 内部标识符族（claude-code-remote）

```
ccr / ccr-agent-proxy[-ca] / ccr-api / ccr-byoc-* / ccr-conflict-reason /
ccr-dir-sync / ccr-gateway / ccr-launcher / ccr-mirror / ccr-proxy / ccr-relay-upstream /
ccr-seed / ccr-session / ccr-sessions / ccr-tip / ccr-triggers-
ccrBaseUrl / ccrCa / ccrClient / ccrMirrorEnabled / ccrNeedsApprovalRetry
feature flag: tengu_ccr_bridge（`claude remote-control --verbose` auth 面板实见 =true）
```

## 5. 鉴权 / 头形状

- `Authorization: Bearer ...`（多个共现）
- `anthropic-beta`（含 `managed-agent...` 值族）、`anthropic-version`、
  `anthropic-client-platform`（browser-sdk.d.ts 要求标注 host surface）、
  `anthropic-user-profile-id`
- OAuth：`/v1/oauth/token`、`/api/oauth/cri`、`claude_cli/roles`、
  `claude_cli/create_api_key`

## 6. 远程端入口语义（entrypoint 枚举，minified 代码摘录）

```
case "remote": case "remote_baku": case "remote_cowork": case "remote_desktop":
case "remote_mobile": return "claude_code_remote";
case "claude-in-teams": return "claude_code_remote";
claude-desktop / claude-desktop-3p / remote_desktop → "Claude Desktop"
remote_mobile → "Mobile"；local-agent / remote_cowork → "Cowork"
CLAUDE_CODE_ENTRYPOINT === "remote_trigger" | "remote_cowork_trigger"
```

即官方内部把 Desktop / Mobile / Cowork / Teams 全部统一为
`claude_code_remote` 客户端族——「远程客户端」是官方一等概念。

## 7. 传输结论（静态层）

- 主通道：HTTPS REST + SSE（`text/event-stream` ×45 处；`events/stream`
  路径 + `from_sequence_num` 游标 + long-lived 语义）
- WebSocket：`/v2/session_ingress/mcp/ws/`（MCP ingress）与
  `wss://bridge.claudeusercontent.com`（bridge 域，用途未定，候选=自托管
  环境隧道）；browser-sdk.d.ts 明示 WebSocket 是迁移遗留、SSE 为新标准
