# CLI 2.1.234 二进制内嵌 Sessions/Threads API 表（strings 提取，2026-09-05）

来源：`/opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe`
（Mach-O arm64）`strings` 提取，模式匹配内嵌 Markdown 路由表。这是 CLI
自带的 API 参考字符串，非网络样本；与 SDK 0.3.260 `.d.ts`（bridge.d.ts /
browser-sdk.d.ts）相互印证。

## sessions / threads 面（与 RC 直接相关）

| 方法 | 路径 | 名字 | 说明（原文摘录） |
| --- | --- | --- | --- |
| POST | `/v1/sessions` | CreateSession | Create a new session |
| GET | `/v1/sessions` | ListSessions | List sessions (paginated) |
| GET | `/v1/sessions/{session_id}` | GetSession | Get session details |
| POST | `/v1/sessions/{session_id}` | UpdateSession | Update session `metadata`/`title`, `agent.tools`/`agent.mcp_servers` (session-local override; session must be `idle`), or `budget` |
| DELETE | `/v1/sessions/{session_id}` | DeleteSession | Delete a session |
| POST | `/v1/sessions/{session_id}/archive` | ArchiveSession | Archive a session |
| GET | `/v1/sessions/{session_id}/events` | ListEvents | List events (polling, paginated) |
| POST | `/v1/sessions/{session_id}/events` | SendEvents | Send events (user message, tool result) |
| GET | `/v1/sessions/{session_id}/events/stream` | StreamEvents | Stream events via SSE. Optional `event_deltas[]=agent.message` / `agent.thinking` opts in to live-preview `event_start`/`event_delta` events |
| GET | `/v1/sessions/{session_id}/threads` | ListThreads | List threads (paginated) |
| GET | `/v1/sessions/{session_id}/threads/{thread_id}` | GetThread | carries `agent` snapshot, `status`, `parent_thread_id`, `stats`, `usage` |
| POST | `/v1/sessions/{session_id}/threads/{thread_id}/archive` | ArchiveThread | |
| GET | `/v1/sessions/{session_id}/threads/{thread_id}/events` | ListThreadEvents | List past events for one thread (paginated) |
| GET | `/v1/sessions/{session_id}/threads/{thread_id}/stream` | StreamThreadEvents | Stream one thread via SSE (SDK: `threads.events.stream`) |
| GET/POST/DELETE | `/v1/sessions/{session_id}/resources[/{resource_id}]` | List/Add/Get/Update/DeleteResource | Attach `file` or `github_repository` resource |

## 同表相邻（Agent Platform 云端面，非 RC 专属，供分辨）

files / skills / memory_stores / vaults / environments / deployments /
agents 同族路由（完整表见报告附录引用；此处不重复）。

## 内嵌文档中的流语义原文

- `Streaming (SSE): GET /v1/sessions/{id}/events/stream — real-time
  Server-Sent Events. **Long-lived** — the server sends pe...`
- `You've likely hit GET /v1/sessions/{id}/events/stream by mistake or a
  server-side stall — report it; don't treat it as a client...`

## 分辨说明

`/v1/sessions*` 是 Agent Platform 会话模型（云端执行）；RC（CCR）专用面为
`/v1/code/sessions*`（见 b3 样本的代码上下文）与 `/v2/ccr-sessions*`。
两者共享事件模型（`agent.*` / `event_start` / `event_delta`）。
