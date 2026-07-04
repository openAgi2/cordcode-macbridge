# CordCode Bridge v1

Direct bridge protocol between iOS and MacBridge over WebSocket.

> **命名说明：** 本协议的 wire 名称固定为 `cordcode-bridge`（见下文协议常量），不随
> 产品名 CCCode→CordCode 变更。本文标题与说明用新品牌名 CordCode。

## Envelope

Client messages use one of these top-level `type` values:

| Type | Direction | Purpose |
| --- | --- | --- |
| `hello` | iOS -> MacBridge | Preferred capability and protocol negotiation. |
| `register` | iOS -> MacBridge | Legacy registration path. |
| `request` | iOS -> MacBridge | Backend RPC call. |
| `ping` | iOS -> MacBridge | Keepalive. |

Server messages use:

| Type | Direction | Purpose |
| --- | --- | --- |
| `hello_ack` | MacBridge -> iOS | Preferred negotiation response. |
| `register_ack` | MacBridge -> iOS | Legacy registration response. |
| `result` | MacBridge -> iOS | RPC response. |
| `event` | MacBridge -> iOS | Backend live event. |
| `pong` | MacBridge -> iOS | Keepalive response. |

## Version Negotiation

New clients must send:

```json
{
  "type": "hello",
  "client": {"app": "CordCode iOS", "version": "1.0.0", "deviceId": "dev_..."},
  "protocol": {"name": "cordcode-bridge", "version": 1, "supportedSchemaRevisions": ["2026-05-07"]}
}
```

MacBridge accepts only `protocol.version == 1` for `hello`. The server response includes
`bridge.protocol.version`, `bridge.protocol.schemaRevision`, `bridge.runtimeVersion`, current URLs,
capabilities, backend descriptors, bridge status, and running sessions.

`register` is retained as a legacy path. It carries the same `protocol` shape but only reports the
server protocol in `register_ack`; it is not the compatibility gate for new work.

## RPC

Request envelope:

```ts
{
  type: "request",
  requestId: string,
  backendId: string,
  method: BridgeRPCMethod,
  params?: object
}
```

Response envelope:

```ts
{
  type?: "result",
  requestId?: string,
  backendId?: string,
  ok?: boolean,
  data?: unknown,
  error?: BridgeWireError
}
```

Supported backend RPC method names in the current MacBridge runtime:

```text
hello
list_providers
set_provider
list_models
list_agents
list_permission_modes
set_permission_mode
create_session
send_message
abort_generation
get_session
get_session_messages
delete_session
resume_session
switch_model
resolve_permission
list_sessions
list_projects
fetch_todos
get_workspace_diff
get_usage
run_diagnostics
list_memory_files
read_memory_file
fetch_content_chunk
read_file
rename_session
share_session
archive_session
compress_context
check_pending_notifications
question_reply
question_reject
get_delivery_prekey_status
upload_delivery_prekeys
get_delivery_chain_head
```

## Events

Event envelope:

```ts
{
  type: "event",
  eventId?: string,
  seq?: number,
  bridgeEpoch?: string,
  backendId?: string,
  sessionId?: string,
  event?: BridgeEventName,
  data?: unknown,
  replayable?: boolean,
  timestamp?: number
}
```

Current event names emitted by MacBridge:

```text
text_delta
message_updated
reasoning_delta
tool_started
tool_finished
todos_updated
turn_started
turn_completed
error
permission_request
context_compressing
context_compressed
context_usage_updated
question_asked
question_resolved
```

## Semantic Notes — questions vs. permissions

These notes clarify the meaning of method/event names that the name registries
above do not make explicit. They are non-breaking semantic documentation; no
field, type, or wire value was changed.

- `question_asked` is the single bridge event for a **structured user-choice
  prompt**. It carries `questionId`, `questionText`, `options[]` (each
  `{ id, label, description }`), `required`, and `threadId?`. It is emitted by:
  - Codex app-server `turn/question` notifications, and
  - Claude Code `AskUserQuestion` tool requests (MacBridge parses the
    `can_use_tool` control request, emits `question_asked`, and registers the
    pending question so a later `question_reply` can build the verified
    `control_response` answer).
  Once emitted, iOS does not care which backend produced it.
- `question_reply` / `question_reject` are **backend-neutral bridge RPCs** for
  answering or cancelling a structured question. `question_reply` carries
  `optionIds: string[]`; iOS sends exactly one option id (single-select v1).
  `question_reject` cancels the question. Both are routed by MacBridge to the
  backend-specific responder (Codex app-server JSON-RPC, or Claude
  `control_response`).
- `permission_request` is for **tool authorization** (e.g. Bash/Write approval),
  NOT structured user-choice prompts. A Claude `AskUserQuestion` that parses to
  a valid structured question is emitted as `question_asked`, not
  `permission_request`. `permission_request` is only used for AskUserQuestion as
  a fallback when parsing yields zero valid questions, so malformed input still
  produces a visible block.
- `resolve_permission.behavior` wire values are exactly `"allow"` / `"deny"`.
  This is the MacBridge/agent wire contract (`core.PermissionResult.behavior`).
  Claude's permission responder treats ONLY `behavior == "allow"` as allow; any
  other value (including legacy `approve`/`approve_always`/`reject`/
  `reject_always`) is deny.
- The iOS UI/native action enum (`approve` / `approveAlways` / `reject` /
  `rejectAlways`) is a **different layer** from the bridge wire `behavior`. iOS
  translates the UI action to the wire value before calling `resolve_permission`
  (`approve`/`approveAlways` → `"allow"`, `reject`/`rejectAlways` → `"deny"`).
  Clients MUST send `allow`/`deny` on the wire; legacy snake_case values are a
  bug, not an alternate vocabulary.
- v1 limitations (enforced at MacBridge parse time, never reach iOS):
  - Only single-question, single-select AskUserQuestion prompts are emitted as
    `question_asked`.
  - `AskUserQuestion` with `len(questions) > 1` or any `multiSelect == true` is
    denied via `RespondPermission(deny)` at parse time and emits no
    `question_asked`.
  - Claude `autoApprove` / `dontAsk` / `acceptEditsOnly` modes short-circuit
    `AskUserQuestion` before event emission, so the iOS question UI does not
    appear in those modes.

## Mapping Notes

iOS accepts compatible session directory fields in this priority order:

```text
directory -> worktree -> cwd
```

Message parts use `type` values:

```text
text
reasoning
tool
file
```

Tool file changes use:

```text
path
kind
diff
movePath
```

New fields should be optional and ignored by older clients. New event names should be additive and
must not reuse an existing event name with incompatible payload semantics.

## Connection URLs

`hello_ack.bridge.currentURLs` may contain:

```ts
{
  local: string,       // primary LAN ws:// candidate
  remote?: string,     // legacy single remote candidate
  remotes?: string[],  // additional remote candidates
  locals?: string[]    // additional LAN ws:// candidates
}
```

`locals` contains LAN WebSocket candidates other than `local`. It is additive and exists so
Relay-first pairing can hand iOS both Relay credentials and current LAN candidates. It MUST NOT
include Tailscale self-signed `wss://100.x` candidates, because those require a separate authenticated
SPKI pin. Clients should treat `local` as primary, race or fallback across `locals` inside the direct
phase, and keep Relay as the remote path when available.

## Session Pagination

There are two separate pagination surfaces:

1. `list_sessions` session-list pagination. These fields are additive and may be used when a backend returns the standard `{ sessions, nextCursor, hasMore }` envelope. Clients MUST treat `cursor` as opaque and scoped to backend, bridge/backend identity, project or directory bucket, and the current backend process lifetime unless a future protocol marks it durable. OpenCode may carry an upstream cursor here; file-backed backends may carry a bridge-owned cursor.
2. `get_session_messages` message-history pagination. This is gated by the existing `session_pagination` backend capability and is unrelated to OpenCode project bucket list loading.

### `list_sessions` paging

Request params (additive):

```ts
{
  "directory"?: string,
  "rootsOnly"?: boolean,
  "limit"?: number,
  "cursor"?: string  // opaque, from a previous response's nextCursor
}
```

Response data (additive):

```ts
{
  "sessions": SessionInfo[],
  "nextCursor"?: string,  // present only when hasMore is true
  "hasMore": boolean
}
```

Rules:

- Clients MUST NOT parse cursor contents or reuse a cursor across backend/project/directory scopes.
- `hasMore` is authoritative. Do not infer more pages from `sessions.length == limit`.
- For OpenCode directory-scoped lists, stable upstream servers may still be array-only and omit a cursor. MacBridge fetches a bounded upstream page, then exposes bridge-owned cursor pagination over that in-memory result; `hasMore` reflects the remaining bridge-owned slice for the current request scope.
- `rootsOnly` remains valid for legacy/non-OpenCode list calls. OpenCode forwards it as the server-side root-session filter; clients must still scope cursors to the same backend/project/directory.

### Capability

A backend advertises `session_pagination` in `capabilities` only for message-history pagination (`get_session_messages`), not for session-list pagination. Clients MUST only send `paginate`/`beforeCursor` history fields to a backend that advertises this capability; otherwise the legacy full-history path is used.

### `get_session_messages` paging

Request params (additive; `paginate`, `beforeCursor` are new):

```ts
{
  "sessionId": string,
  "directory"?: string,
  "limit"?: number,        // page size, clamped to [1, 200], default 50
  "paginate"?: boolean,    // opt in to the paginated path; omit for legacy behavior
  "beforeCursor"?: string  // opaque, from a previous response's oldestCursor
}
```

When `paginate` is true and the backend supports it, the response data is:

```ts
{
  "messages": RichHistoryEntry[],
  "oldestCursor"?: string,  // send as beforeCursor for the next (older) page
  "newestCursor"?: string,  // informational, for client merge/dedup
  "hasMore": boolean,
  "contextUsage"?: ContextUsage
}
```

- No `beforeCursor` returns the newest page.
- `beforeCursor` returns the page strictly older than the cursor's message.
- The page is bounded by BOTH `limit` and a per-page wire-byte budget (~256 KiB). If the page would
  exceed the byte budget, the oldest messages in the page are deferred to the next page, so a single
  oversized tool output can never reopen the close-1009 frame on its own.
- `beforeCursor` pins a message ordinal within a prefix generation. Tail appends to a live session
  keep old cursors valid (the generation lineage proves ancestry). If the indexed prefix was
  rewritten, truncated, or replaced, the server returns `error.code == "cursor_stale"` and the client
  MUST reload the first page instead of stitching across lineages.

### Cursor semantics

- Cursors are opaque and versioned. Clients must not introspect or construct them.
- A cursor is only valid for the session and backend it was issued for.
- `cursor_stale` means the history prefix the cursor referenced can no longer be proven continuous;
  reset to the first page.
