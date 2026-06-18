# CCCode Bridge v1

Direct bridge protocol between iOS and MacBridge over WebSocket.

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
  "client": {"app": "CCCode iOS", "version": "1.0.0", "deviceId": "dev_..."},
  "protocol": {"name": "cccode-bridge", "version": 1, "supportedSchemaRevisions": ["2026-05-07"]}
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

## Session Pagination

`list_sessions` and `get_session_messages` support backwards cursor pagination so a session with a
large history never has to be sent as a single WebSocket frame. This is the fix for iOS WebSocket
close 1009 ("Message too long") on large Codex/Claude transcripts.

### Capability

A backend advertises `session_pagination` in `capabilities` (per-backend, not server-level) when it
can expose a stable transcript file path. Currently `codex` and `claudecode` advertise it. Clients
MUST only send cursor fields to a backend that advertises this capability; otherwise the legacy
full-parse path is used.

### `list_sessions` paging

Request params (additive; `cursor` is new):

```ts
{
  "directory"?: string,
  "limit"?: number,
  "cursor"?: string  // opaque, from a previous response's nextCursor
}
```

Response data (additive; `nextCursor` and `hasMore` are new):

```ts
{
  "sessions": SessionInfo[],
  "nextCursor"?: string,  // present when hasMore is true
  "hasMore": boolean
}
```

The cursor is an opaque, versioned encoding of the backend's stable composite sort key
(`updatedAtMillis` DESC, `sessionId` ASC). It is never just a timestamp. A malformed or stale cursor
degrades to the first page rather than failing the request, because the list is cheap to re-fetch.

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
- The page is bounded by BOTH `limit` and a per-page wire-byte budget (~4 MiB). If the page would
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
