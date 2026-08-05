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
| `recovery_applied` | Client -> MacBridge | Exact per-session cut acknowledgement for an active recovery transaction. |

Server messages use:

| Type | Direction | Purpose |
| --- | --- | --- |
| `hello_ack` | MacBridge -> iOS | Preferred negotiation response. |
| `register_ack` | MacBridge -> iOS | Legacy registration response. |
| `result` | MacBridge -> iOS | RPC response. |
| `event` | MacBridge -> iOS | Backend live event. |
| `pong` | MacBridge -> iOS | Keepalive response. |
| `recovery_barrier` | MacBridge -> client | Replay input is complete; client must apply/persist and acknowledge. |
| `recovery_complete` | MacBridge -> client | Recovery is committed; pending live events follow this frame. |

## Version Negotiation

New clients must send:

```json
{
  "type": "hello",
  "client": {"app": "CordCode iOS", "version": "1.0.0", "deviceId": "dev_..."},
  "protocol": {"name": "cordcode-bridge", "version": 1, "supportedSchemaRevisions": ["2026-05-07", "2026-07-05"]}
}
```

MacBridge accepts only `protocol.version == 1` for `hello`. The server response includes
`bridge.protocol.version`, `bridge.protocol.schemaRevision`, `bridge.runtimeVersion`, current URLs,
capabilities, backend descriptors, bridge status, and running sessions.

Recovery is an optional `hello` / `hello_ack` extension. A client opts in with
`capabilities: ["recovery_v1"]` and may send `lastBridgeEpoch`, compatibility-only `lastEventId`, and
the authoritative nested `lastSeenBySession` cut map. An opted-in response may include root-level
`bridgeEpoch` and a `recovery` plan. Every recovery control frame carries the same random
`recoveryId`; cut acknowledgements are exact per-session maps and never a scalar fallback. See
`../2026-07-18-event-recovery-rfc.md` for ordering, atomic snapshot, and failure semantics.

`register` is retained as a legacy path. It carries the same `protocol` shape but only reports the
server protocol in `register_ack`; it is not the compatibility gate for new work and never starts a
recovery transaction. Without explicit `recovery_v1`, a new server preserves legacy behavior and
omits recovery fields.

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
get_session_projection
delete_session
resume_session
switch_model
resolve_permission
list_sessions
list_projects
fetch_todos
get_workspace_diff
get_turn_diff
get_full_thread_diff
get_usage
run_diagnostics
list_memory_files
read_memory_file
fetch_content_chunk
read_file
list_directory
get_git_context
checkout_git_branch
create_git_branch
create_git_worktree
rename_session
share_session
archive_session
set_session_pinned
list_pinned_sessions
compress_context
check_pending_notifications
question_reply
question_reject
resolve_user_input
get_delivery_prekey_status
upload_delivery_prekeys
get_delivery_chain_head
set_observation_scope
enable_relay_pairing
```

## RPC Scopes (§6.3)

Every backend RPC is mapped to one of seven scope tokens. The scope table is the single
source of truth in `go-bridge/rpc_scopes.go` (`rpcScopeTable`); the method-name list above
and this section are its human-readable mirror. The CI guards
`TestEveryDispatchedRPCHasScope` / `TestScopeTableCoversAllMethods` keep the table and the
dispatch surface in sync — any newly dispatched RPC MUST declare a scope or the build breaks.

Scope check lives in `CapabilityPolicy.AuthorizeRPC`, which is the single funnel inside
`HandleRPC` — it runs before `dispatchRPC` and before every out-of-switch method route
(`set_observation_scope`, the three delivery RPCs, `enable_relay_pairing`), so all of them
pass through the same gate. A denied RPC returns a stable error:

```json
{ "code": "forbidden", "message": "scope <scope> not granted" }
```

`ping` and `recovery_applied` are connection-level `type` messages (not `method` RPCs); they
never reach `HandleRPC` method routing and are unconditional by construction. The dispatch
`case "hello"` is a legacy no-op placeholder (returns `{ok:true}`); the real handshake is the
`type:"hello"` message handled separately, so `hello` is mapped to an empty (unconditional)
scope only to keep the CI guard satisfied.

| scope | RPCs | default for a paired device |
|---|---|---|
| `session.read` | `get_session`, `get_session_messages`, `get_session_projection`, `list_sessions`, `list_pinned_sessions`, `fetch_todos`, `check_pending_notifications`, `get_turn_diff`, `get_full_thread_diff` | ✅ |
| `session.write` | `create_session`, `send_message`, `abort_generation`, `resume_session`, `delete_session`, `rename_session`, `archive_session`, `set_session_pinned`, `compress_context`, `resolve_permission`, `question_reply`, `question_reject`, `resolve_user_input`, `share_session`, `set_observation_scope` | ✅ |
| `config.read` | `list_providers`, `list_models`, `list_agents`, `list_permission_modes`, `get_usage`, `list_memory_files`, `read_memory_file`, `run_diagnostics` | ✅ |
| `config.write` | `set_provider`, `switch_model`, `set_permission_mode` | ✅ |
| `workspace.read` | `get_workspace_diff`, `read_file`, `list_directory`, `get_git_context`, `fetch_content_chunk` | ✅ |
| `workspace.mutate` | `checkout_git_branch`, `create_git_branch`, `create_git_worktree`, `list_projects` | ✅ (recommend an owner per-action confirmation on top) |
| `delivery.manage` | `get_delivery_prekey_status`, `upload_delivery_prekeys`, `get_delivery_chain_head`, `enable_relay_pairing` | ✅ (own device chain only) |
| _(empty — unconditional)_ | `hello` (legacy dispatch placeholder) | ✅ (no scope required, else handshake deadlock) |

**Backward compatibility.** A paired device with no `grantedScopes` recorded (every existing
persisted record) is treated as holding all seven scopes, so this change does not alter the
current authorization semantics. The value is structural: a future restricted pairing (e.g.
read-only iPad), a hard gate forcing every new RPC through security review, and client-side
UI gating off `hello_ack.grantedScopes`.

### Hello scope fields (additive, optional)

`hello` may carry `requestedScopes` and `hello_ack` carries `grantedScopes`. Both are optional
additive fields; old clients that do not send/parse them are unaffected.

```ts
// hello (request) — additive
{
  ...,
  requestedScopes?: string[]   // client-declared intent; not enforced yet (forward compat)
}

// hello_ack — additive
{
  ...,
  grantedScopes?: string[]    // the scopes this device actually holds; client may UI-gate on it
}
```

When the device record has explicit `grantedScopes`, `hello_ack.grantedScopes` echoes that
restricted set; otherwise it echoes the full default set (`DefaultGrantedScopes`), so a normal
paired client always observes all seven scopes today.

### Claude resume ownership preflight

For `send_message`, MacBridge performs a best-effort ownership preflight only when the selected
backend is `claudecode`, the request resumes a non-empty session id, and that session is not already
backed by a live entry in the current MacBridge registry. If any matching session stub identifies a
still-live Claude process, the request fails before `StartSession(--resume)` with
`session.held_by_external_worker`. If the lister is unavailable or the check errors/times out, it
fails closed with `session.owner_check_failed`. Both errors carry `retryable: true`; clients surface
the failure and let the user retry after external state changes rather than automatically retrying.

This is not an ownership lock. Corrupt/unreadable stubs may be skipped, and another client can start
a process after the check (TOCTOU). New sessions, already registry-owned sessions, and non-Claude
backends do not use this preflight.

## Events

Event envelope:

```ts
{
  type: "event",
  eventId?: string,
  seq?: number,
  perSessionSeq?: number,
  bridgeEpoch?: string,
  backendId?: string,
  sessionId?: string,
  event?: BridgeEventName,
  data?: unknown,
  replayable?: boolean,
  timestamp?: number
}
```

`seq` is process-global. `perSessionSeq` is monotonic within one `(backendId, sessionId)`
chain and lets clients distinguish a real session gap from unrelated interleaved events.

Current event names emitted by MacBridge:

```text
text_delta
message_updated
user_message
system_message
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
user_input_requested
user_input_resolved
turn_diff_ready
projection_patch
projection_snapshot
sync_invalidate
```

`tool_started` / `tool_finished` may carry an optional `data.matches` field. It is the
single structured truth for explore/search results and has exactly one of these shapes:

```ts
type ToolMatches =
  | { kind: "count"; count: number }
  | { kind: "paths"; paths: string[] }
  | { kind: "detailed"; items: { path: string; line?: number; preview?: string }[] };
```

The field is absent when the driver cannot prove the result shape. Consumers MUST NOT infer
counts or paths from `toolResult` display text. Query fields remain in `toolInput` /
`toolInputRaw`; execution results belong in `matches`.

Child-agent events may carry optional `data.streamId` and `data.parentStreamId`. The same
`streamId` is attached to that child's text, reasoning, tool, error, and completion events;
`parentStreamId` links a nested child to its owning child stream. Absence means the main flat
stream. Drivers MUST resolve this from stable transcript/tool identities, never from the most
recent Task invocation; when the relation cannot be proven, they omit both fields and emit only
sanitized diagnostics.

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

### Structured user input v2 (`structured_user_input_v1`)

`structured_user_input_v1` is the **multi-question, multi-select** successor to the
single-question `question_asked` / `question_reply` path. It is an additive, non-breaking
capability: a backend advertises it per-descriptor only when its adapter, responder, and the
Projection Kernel reducer are all ready; clients that do not see it keep the v1 path verbatim.
The v1 and v2 paths MUST NOT be mixed for the same interaction, and v2 MUST NOT fall back to
`question_reply` on failure (`.off` is an explicit legacy mode, not a runtime fallback).

- `user_input_requested` is the bridge event for one structured-input interaction. It carries
  `turnId`, `interactionId`, `status` (`pending` normal, or `failed` for malformed questions —
  both project once), `questions[]`, `canRespond`, `canReject`, `expiresAt?`, and
  `diagnosticCode?` (e.g. `invalid_backend_request`). Each question is
  `{ id, header?, prompt, answerMode: "single"|"multiple"|"text", options[], allowsCustomAnswer,
  isSecret, required }`; each option is `{ id, label, description? }`. Stable ids are derived
  (lowercase SHA-256 prefix `"ui_"`): Codex `interactionId = "ui_"+sha256("codex\0"+requestIdType+
  "\0"+requestIdValue+"\0"+threadId+"\0"+turnId+"\0"+itemId)[:32]`, Claude
  `interactionId = "ui_"+sha256("claudecode\0"+requestId)[:32]`; `questionId = interactionId+"_q_"+i`,
  `optionId = questionId+"_o_"+j`.
- `user_input_resolved` carries `turnId`, `interactionId`, `status` (`answered`|`rejected`|
  `auto_resolved`|`unavailable`|`failed`), `source` (`ios`|`mac`|`other_client`|`backend`), and
  `resolvedAt`. The projection never stores answer text (esp. for `isSecret`); the resolved event
  only carries status/source/resolvedAt.
- `resolve_user_input` is the backend-neutral bridge RPC for answering/rejecting a v2
  interaction. Payload: `{ interactionId, clientActionId, action: "answer"|"reject",
  answers?: [{ questionId, values: [{ kind: "option"|"text", optionId?, text? }] }] }`. MacBridge
  routes it to the backend-specific `UserInputResponder` (Codex app-server JSON-RPC
  `resolveUserInput`/`interrupt`, or Claude `control_response` allow with `updatedInput.answers` /
  deny). It returns `{ interactionId, outcome: "accepted"|"already_resolved"|"in_progress",
  currentStatus, headRev }` or a
  `UserInputError{ code, message }` (`interaction_not_found`, `invalid_answer_shape`,
  `backend_response_failed`, `session_not_active`). `outcome/currentStatus` acknowledges the action
  and may remain `in_progress/pending` when another writer holds the claim. Only the projected
  `user_input` part at `headRev` establishes terminal UI state; the RPC result never writes card
  state independently.
- Claude v1 invariants (adapter-enforced, design §9): `allowsCustomAnswer=true`, matching Claude's
  real Other/custom-result path; `multiSelect` maps to `single`/`multiple`; empty
  options are malformed (not normalized to text); each question is `required=true`, `isSecret=false`;
  duplicate question text within one interaction is `invalid_backend_request` (it cannot be an
  unambiguous `answers` map key). Codex has no verified reject path (`canReject=false`); Claude has
  a real deny `control_response` (`canReject=true`).
- Projection Kernel (design §10): one `user_input` part per `interactionId`, upserted in place
  (never a second "answered" card); `execution.phase=requires_action` while any active-turn
  `user_input` part is `pending`; resolved updates the part in place and reverts phase per active
  turn status. The reducer is the single consumer for both live and hydrate; identityless frames
  (missing `turnId`/`interactionId`) are dropped without committing a revision, and a `resolved`
  with no matching requested part is dropped (no fabrication, no second writer).
- One canonical backend interaction registry owns each pending responder. Legacy
  `question_asked/question_resolved` frames are derived strictly one-way from that Kernel interaction
  for legacy connections; `session_sync_v2` connections receive only projection updates. Legacy raw
  events never feed back into the Kernel and never create a second writable registry.

`schemaRevision` is bumped to `2026-08-02` for this additive set (new capability, RPC, part
variant, part op, event names); the protocol major version is unchanged and old clients ignore
the unknown names.

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

## Connection policy (control-plane)

`connectionPolicy` is a **control-plane** preference delivered over authenticated channels. Product
mental model: **Relay is the stable connection base; same-LAN direct is an opt-in performance
optimization.**

```ts
connectionPolicy?: {
  preferLocalNetwork: boolean  // default false
}
```

It appears in four authenticated payloads only:

- `hello_ack.bridge.connectionPolicy`
- Relay-first `RelayFirstResult.connectionPolicy`
- direct `pairing_complete.bridge.connectionPolicy`
- `GET /internal/remote/status` top-level `preferLocalNetwork`

`preferLocalNetwork` is `false` by default. Only when the Mac owner explicitly enables it does iOS
prefer ordinary LAN direct (`ws://<lan-ip>`, security level `lan`) on Wi-Fi/mixed networks, falling
back to Relay on failure. Cellular stays on Relay. It does **not** affect Tailscale TLS pinning, URL
security classification, or an explicit custom-remote intent.

Candidate discovery is independent of the preference: when `preferLocalNetwork` is `false` the Mac
still publishes the full LAN candidate set (`currentURLs.locals`, `RelayFirstResult.localUrls`), so
toggling the switch on later requires no re-pairing and DHCP/address changes still refresh. The
presence of candidates alone never changes policy.

The field is optional everywhere. A new client decoding an old payload treats a missing
`connectionPolicy` as `preferLocalNetwork: false`; an old client ignores the new field.

**Session Sync v2 red line:** `connectionPolicy` is control-plane only. It MUST NOT appear in any
`EventMessage.data`, MUST NOT enter the timeline via publish/ingest, and MUST NOT be added to
`SessionProjection` or matched in the projection reducer. Path migration (LAN ↔ Relay) only swaps
transport — it bumps connection generation, invalidates stale in-flight frames, and re-aligns the
same `SessionProjection` via the unified reconcile path. The preference, URL candidates, and actual
transport kind do not participate in projection ownership, completion, or the timeline reducer.

`GET /internal/remote/status` also exposes a real `relay.connected` boolean (independent of
`relay.configured`): the Mac only renders "connected to relay" when the encrypted channel's status
provider reports connected AND relay is enabled and configured. `configured == true` alone MUST NOT
be displayed as connected.

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

### Capability: `external_turn_streaming`

A backend advertises `external_turn_streaming` in `capabilities` when MacBridge pushes external-turn
content events over the live stream, so clients can treat push as the primary source and demote
discovery polling to a reconcile/watchdog. The `multi-client-streaming-sync` refactor Phase 1
implements file-relay content streaming for **codex** (rollout) and **claude**/`claudecode`
(transcript): MacBridge parses transcript/rollout growth and emits `text_delta` / `reasoning_delta`
/ `tool_started` / `context_usage_updated` during the turn — not only at `turn_completed`. Codex
also emits `user_message` with `{ itemId, turnId, text }` when the rollout persists the external
prompt; `itemId` is the response-item message ID and is reused by rich history reconciliation.
**opencode** is push-native via its SSE firehose (separate path, not this capability); **grokbuild**
streams external-turn content through the projection pipeline (updates.jsonl file-tailer fallback,
`session_sync_v2`) and does not advertise this capability yet. The external prompt
(`user_message_chunk`) is caught up at relay attach when its turn is still in flight and emitted as
`user_message` attributed to that turn's source-proven `promptId`; prompts of completed turns come
from cold hydrate (`chat_history.jsonl`). Clients seeing this capability SHOULD NOT start
discovery/active external-turn probes and SHOULD keep only a `turn_completed` reconcile + a
low-frequency watchdog; clients on backends without it fall back to current polling. Adding the
string is non-breaking (extensible `capabilities` array); no protocol major-version bump.

### Capability: `supports_checkpoint` / `supports_conversation_rollback`

§6.1 checkpoint 只读 diff. A backend advertises `supports_checkpoint` in `capabilities`
when its agent driver implements the `core.CheckpointProvider` opt-in interface. MacBridge
then captures a HIDDEN git ref snapshot of the agent workspace after each completed turn, so
the client can later fetch a per-turn or full-thread file diff (read-only). The snapshot is a
workspace FILE snapshot only — it is NOT a session truth source; session truth always stays in
the official CLI. Capture honestly no-ops on a non-git workspace (`workspace_not_git`); no
mock/placeholder snapshot is ever written.

`supports_conversation_rollback` is advertised when the driver implements the forward-compat
`core.ConversationRollbackProvider` interface. No current driver implements it, so the
capability is absent everywhere today and the (currently hidden) revert entry stays disabled
until one does.

Both are extensible `capabilities` strings; adding them is non-breaking (no major version bump).
No backend-id hard-branching; derivation is by type assertion on the driver.

### Event: `turn_diff_ready`

Optional control-plane push (§6.1). After MacBridge successfully writes a turn's checkpoint
git ref, it emits `turn_diff_ready` so clients can surface a per-turn `+/-` summary without
polling. It carries NO full patch (plan §6.1). Clients that miss it fall back to the
`get_turn_diff` / `get_full_thread_diff` RPCs. The event is control-plane only: it never
mutates the message projection (SSV2 guardrail 8 enumerated exception — not a second timeline
writer).

```ts
{
  checkpointRef: string,            // refs/cordcode/checkpoints/<backendId>/turn/<N>/r<short>
  turnNumber: number,               // 1-based count of the just-completed turn
  files: Array<{ path: string; additions: number; deletions: number }>,  // capped (~50)
  truncated: boolean                // true when the file list hit the event cap; use RPC for full
}
```

### RPC: `get_turn_diff`

Returns the per-file diff for a single completed turn (between the `turnNumber-1` and
`turnNumber` checkpoint refs). For `turnNumber == 1` the baseline is git's empty tree, so the
turn's files appear as additions. Gated on `supports_checkpoint`: returns `checkpoint_unsupported`
for backends that do not implement it, and `workspace_not_git` when the resolved workspace is
not a git repository. Both RPCs are scoped to `session.read` (see the scope table in
**RPC Scopes (§6.3)**; the scope gate is live, and a paired device holds all scopes by default).

Request:

```ts
{
  sessionId: string,
  turnNumber: number,            // 1-based
  directory?: string             // optional; falls back to the session registry + WorkDirSwitcher
}
```

Response:

```ts
{
  files: Array<{ path: string; additions: number; deletions: number }>,
  additions: number,             // totals across files
  deletions: number,
  truncated: boolean,            // true when files exceed the server cap (~500)
  checkpointRef: string,         // the turn's ref (for debugging / client-side caching)
  fromRef?: string               // the previous turn's ref, or empty when turnNumber == 1
}
```

Stable error codes: `checkpoint_unsupported`, `workspace_not_git`, `workspace_missing`,
`checkpoint_not_found` (no ref for the given turn), `invalid_directory`, `invalid_params`.

### RPC: `get_full_thread_diff`

Returns the aggregate per-file diff from the EARLIEST captured turn to the LATEST for a session.
When only one turn is captured, or when comparing against the session's first captured state,
the baseline falls back to git's empty tree so the turn's files appear as additions. Same
capability gating and error codes as `get_turn_diff`.

Request:

```ts
{
  sessionId: string,
  directory?: string
}
```

Response shape is identical to `get_turn_diff` (`files`, `additions`, `deletions`,
`truncated`, `checkpointRef`, `fromRef`), with `checkpointRef` = the latest turn's ref and
`fromRef` = the earliest turn's ref (or empty when the empty-tree baseline is used).

### Event: `sessions_changed`

Optional push (multi-client-streaming-sync §6). MacBridge periodically lists each backend's
sessions; when a NEW session appears (e.g. a turn opened in a native app while the client sits on
the session list), it broadcasts `sessions_changed` with `{backendId}`. Clients refresh
`list_sessions` on receipt. The event carries no `sessionId` and relies on the broadcaster's
all-backend/all-connections fallback to reach list-viewing clients. Non-breaking/optional: clients
also refresh on reconnect/foreground/turn-activity, so this is a latency win, not a correctness gate.

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

## Session Projection Stream (session_sync_v2)

Single-source multi-device sync. MacBridge reduces its `EventPublisher` output into ONE
authoritative `SessionProjection` per `(backendId, sessionId)`; clients only apply projection
patches/snapshots and MUST NOT dual-source merge content against `get_session_messages` or raw
`text_delta`. Design: `docs/2026-07-24-single-source-multidevice-sync-design.md`.

- **Authority** lives on MacBridge. `syncRev` is the projection-owned mutation revision; ignored
  raw events do not advance it. Push and pull read the SAME committed Kernel head.
- **Single outbound funnel.** Projection frames leave MacBridge only through the existing
  `EventPublisher` per-connection dispatch (they reuse `broadcaster` + observation target
  resolution). There is no parallel projection websocket / SSE pipe.
- **Production scope.** Codex, Claude, OpenCode and Grok Build project through the same Kernel
  contract; iOS and remote-web consume the same SPS ownership semantics.

### Capability: `session_sync_v2`

A CLIENT projection-only transport capability plus a backend-scoped ownership capability. The
client opts in with
`capabilities: ["session_sync_v2"]` in `hello`; when the server-side rollout flag is enabled,
MacBridge echoes `capabilities["session_sync_v2"] = true` and adds `session_sync_v2` only to each
migrated backend descriptor's `capabilities`. Clients MUST decide timeline ownership from the
selected backend descriptor, not the global hello echo.

Since Phase 4, advertising `session_sync_v2` is an unambiguous ownership promise, not a shadow
observation request:

- opted-in connections receive projection frames plus non-timeline control-plane events;
- MacBridge's live `EventPublisher` fanout does not send raw timeline-semantic events (`turn_*`,
  user/text/reasoning/tool content, permission/question timeline steps, completion/error state) to
  that connection. Durable recovery/mailbox storage remains legacy-compatible so a later `.off`
  client can recover; an active client therefore MUST retain its raw-content writer seal;
- a legacy client uses the explicit kill-switch by omitting `session_sync_v2`; it continues to
  receive raw/history behavior;
- clients MUST NOT advertise the capability while retaining legacy timeline ownership.

The former rollout-only shadow mode is retired. Adding these fields remains non-breaking and uses
`schemaRevision`, not the protocol major version.

### Push frames

All three are `event`-envelope frames (same `seq` / `perSessionSeq` / `bridgeEpoch` envelope as
other events). Envelope sequence numbers belong to transport/recovery; patch `syncRev` belongs to
the projection and must be compared only with `appliedRev`.

| Event | `data` shape | Client action |
| --- | --- | --- |
| `projection_patch` | `BridgeProjectionPatch` | if `baseRev == appliedRev`, apply `partOps`/`upsertTurns`/`execution` and set `appliedRev = syncRev`; else call `get_session_projection(sinceRev=appliedRev)` |
| `projection_snapshot` | `BridgeProjectionSnapshot` | if `syncRev > appliedRev`, replace the whole projection and set `appliedRev = syncRev` |
| `sync_invalidate` | `BridgeSyncInvalidate` | call `get_session_projection` (full) |

`projection_patch` is the main streaming path: MacBridge coalesces consecutive text/thinking
deltas 50–100ms and emits one patch with `partOps` so the observing client sees incremental
content during a long turn (not only at `turn_completed`). `upsertTurns` (whole-turn replace)
is for new-turn appearance, status change, or authoritative correction — not per-token updates.
Completion is authoritative only when the turn's `status ∈ {completed, aborted, error}`
(integrating the existing `turnCompletedAt` evidence); clients MUST NOT settle a v2-observed
turn on a heuristic alone.

Codex text parts may carry `presentation: "progress" | "final"`, using the same canonical
classification as rich history. On a settled turn, only the terminal `final` text contributes to
the message's final body; progress parts remain ordered timeline evidence. Older snapshots may
omit the additive field.

A turn may contain an additive `system` message (`role: "system"`) for a transcript lifecycle
milestone. Claude `compact_boundary` is projected this way as one short completion summary; the
following `isCompactSummary` / `isVisibleInTranscriptOnly` transcript payload is internal context
and MUST NOT be projected as user content. A system milestone is completed content and MUST NOT
arm `execution`.

Projection frames are reconstructable via `get_session_projection`, so they are NOT durable
mailbox milestones and are NOT live-buffered; reconnect/recovery aligns via a `get_session_projection`
pull, not via mailbox replay of patches (design §8.4 option A).

### RPC: `get_session_projection`

Sits beside `get_session_messages` (which remains for legacy clients, paging, and debugging).
MUST read the ProjectionReducer in-memory state (cold-start may hydrate once from disk into the
reducer, then serve); it MUST NOT be a thin wrapper that returns `get_session_messages` bodies for
the client to merge.

Cold start is a single-flight transaction. MacBridge restores a validated checkpoint or reduces
`[checkpointCursor,startCut)` in an isolated reducer, queues post-cut live input, then atomically
commits baseline plus pending live events. Hydrate does not publish ordinary events, consume
transport sequence numbers, enter recovery/offline/mailbox buffers, or expose partial projections.
A completed source inspection may commit an honest `ready(empty)`; a bare running shell alone
MUST remain hydrating.

RPC lifecycle is explicit:

| State | Wire result | Meaning |
| --- | --- | --- |
| `hydrating` | error `projection.hydrating`, `retryable=true`, optional `retryAfterMillis` | healthy single-flight continues; client stays loading |
| `ready` | success `{projection}` or `{patches,headRev}` | complete committed head; only this may map into the active timeline |
| `failed` | error `projection.hydrate_failed`, `retryable`, optional `retryAfterMillis`/`attempts` | hydrate terminated; retry policy is explicit |
| not migrated | error `projection.not_migrated`, `retryable=false` | selected backend has no v2 authority |

The RPC response budget is 15 seconds. Budget expiry while the transaction remains healthy returns
`projection.hydrating`; it never returns head-0 or a partial success. The client may keep its
independent hard cap and allow a late complete response to apply, but MUST NOT fall back to history.

Request params (additive):

```ts
{
  sessionId: string,
  directory?: string,
  sinceRev?: number,   // when set, server MAY return a delta {patches, headRev}
  limitTurns?: number
}
```

Response data — full projection, or a delta when `sinceRev` was honored:

```ts
| { projection: BridgeSessionProjection }
| { patches: BridgeProjectionPatch[], headRev: number }
```

Exact field shapes (`BridgeSessionProjection`, `BridgeTurnProjection`, `BridgeMessageProjection`,
`BridgeProjectionPart`, `BridgeProjectionPatch`, `BridgePartOp`, `BridgeExecutionView`) are defined
in `docs/protocol/schema/bridge-v1.types.ts`.

#### Tool part additive fields: `title` / `fileChanges` (ChatGPT-style activity rows)

`BridgeProjectionPart` tool variant gains two **optional** fields (non-breaking, lowerCamelCase):

| Field | Purpose |
|-------|---------|
| `title?: string` | Path-bearing display title (Claude Edit/Write `file_path`, Codex patch target, etc.). Clients use it for activity-row labels and `extractPrimaryPath` when structured file path is otherwise missing. |
| `fileChanges?: { path, kind?, movePath?, diff? }[]` | Structured file mutations for this tool step (Codex Patch / apply_patch). Same shape as UnifiedFileChange. |

Producers (live `tool_started`/`tool_finished` and cold hydrate) must pass these through the
Projection Kernel reducer so snapshot/patch parts retain them. Clients map them read-only; when
absent they fall back to `toolInput` / tool output presentation parsing — never invent paths or
`+0 −0`. Older clients ignore unknown optional fields.

#### Part vocabulary: `subagent` (B4 child-stream, sync-only)

`BridgeProjectionPart` gains an additive `type: "subagent"` variant for Claude `Agent`/`Task`
tool nested subagents. It is populated **only** by the MacBridge projection kernel during cold
hydrate (sync-only): the kernel reads sibling `subagents/agent-<id>.jsonl` + `.meta.json`
sidechain files, builds the multi-level tree, and emits one `subagent` part per depth-1 agent
through the same hydrate transaction. Clients map it **read-only** — no client-side tree
building, no second writer.

Join keys mirror the real sidechain `.meta.json` schema (sample-verified): depth-1 anchors to the
mainstream turn via `spawnToolUseId` ↔ the mainstream `Agent` tool_use `itemId`; depth≥2 nests
via `parentAgentId` ↔ the parent agent's `agentId`. `subagentBlocks` is recursive (the subagent's
own text/reasoning/tool content plus nested depth+1 subagents). `subagentDiagnostic` carries
`orphan_parent` / `cycle` / `max_depth` for defensive tree-walk outcomes; a depth-1 agent whose
mainstream anchor is absent is dropped (fail-open to the current state, never fabricated).

This is distinct from the legacy `streamId`/`parentStreamId` live child-stream fields (Events
§`child_stream_*`), which describe **live** mid-run delivery. B4 sync-only does not use those:
async mid-run live delivery is a separate, future enhancement (方案 2).

#### Part vocabulary: `user_input` (structured user input v2)

`BridgeProjectionPart` gains an additive `type: "user_input"` variant for one structured-input
interaction (design §6/§10), backend-neutral once projected. The MacBridge Projection Kernel is
the single writer: it reduces `user_input_requested`/`user_input_resolved` events into exactly one
part per `interactionId`, upserted **in place** on the owning assistant turn/message (never a
second "answered" card). Clients map it read-only into a dedicated block; they do not derive
status, do not write answers back into the projection, and do not synthesize a second card.

Part fields (all lowerCamelCase, optional unless noted):

| Field | Purpose |
|-------|---------|
| `interactionId: string` | Stable derived id (`"ui_"+sha256…`); the upsert key. |
| `status: string` | `pending` \| `answered` \| `rejected` \| `auto_resolved` \| `unavailable` \| `failed`. |
| `questions` | Canonical question array (see Semantic Notes §Structured user input v2). Absent on `resolved`. |
| `canRespond: boolean` | Whether the client may answer (`false` for `failed`/`unavailable`). |
| `canReject: boolean` | Whether the client may reject (Claude `true`, Codex `false`). |
| `expiresAt?: number` | epoch-ms display hint; clients MUST NOT use a local timer to flip status. |
| `resolvedAt?: number` | epoch-ms when the interaction reached a terminal status. |
| `resolutionSource?: string` | `ios` \| `mac` \| `other_client` \| `backend`. |
| `diagnosticCode?: string` | e.g. `invalid_backend_request` for malformed/`failed`. |

A new part op `upsert_user_input` carries the full part in `BridgePartOp.part` (targeting the
owning `turnId`/`messageId`). `execution.phase` becomes `requires_action` while any active-turn
`user_input` part is `pending`; resolving the last pending part reverts phase per the active turn
status. Snapshot/patch round-trips preserve the part and its `questions` (deep-copied); a
checkpoint with a `pending` part whose responder handle was lost after process restart is
recovered to `unavailable` via the Kernel private recovery transaction (design §10.3), never left
as a clickable-but-unanswerable UI.

## Session Pinning

Session pinning (置顶) is a backend-neutral, MacBridge-owned session-metadata capability. iOS does
NOT read Claude/Codex/OpenCode local storage; it consumes pin state through the same `BackendClient`
path used for rename/archive/delete.

### Capability

```text
session_pin
```

A backend advertises `session_pin` in `capabilities` when its agent implements the `SessionPinner`
interface (independent of `session_mutation` / `session_delete`). It is advertised for Claude,
Codex, and OpenCode. Clients MUST gate pin/unpin on `session_pin`, not on `session_mutation`.

### Wire field

`BridgeSessionInfo.pinnedAtMillis?: number` — epoch-ms representing when the user pinned the
session, present only when pinned. Pin/unpin MUST NOT alter `updatedAtMillis`. See
`docs/protocol/schema/bridge-v1.types.ts` `BridgeSessionInfo`.

### RPC: `set_session_pinned`

Idempotent mutation. Mirrors the rename/archive shape: iOS sends the request, discards the wire
response (`requestVoid`), and re-fetches via `get_session` for the returned `BackendThread`.

Request params:

```ts
{
  sessionId: string,
  pinned: boolean,
  pinnedAtMillis?: number,   // epoch-ms; required when pinned=true, ignored when pinned=false
  directory?: string         // optional scope hint (same param iOS sends for rename/archive)
}
```

MacBridge resolves the backend/session scope from `sessionId` (+ optional `directory`) into the
same key used during list enrichment before writing pin metadata, and returns the updated session
summary for symmetry with rename/archive:

```ts
{
  session: BridgeSessionInfo   // includes pinnedAtMillis when pinned
}
```

### RPC: `list_pinned_sessions`

Backend-neutral, NOT directory-scoped. Returns the global pinned section for the active backend.
This is what surfaces pinned OpenCode sessions whose project bucket has not been loaded via
`list_sessions(directory:)` (which stays scoped to the requested directory).

Request params:

```ts
{ backendId: string }
```

Response data:

```ts
{ sessions: BridgeSessionInfo[] }   // each includes pinnedAtMillis
```

### Summary source and truthfulness

The pin store holds identity + `pinnedAtMillis` only — it does NOT cache title/messageCount/
updatedAtMillis. `list_pinned_sessions` resolves summaries from the real backend source:
- Claude: overlay on the existing Claude session catalog.
- Codex: overlay on `agent/codex/sessionListCache`.
- OpenCode: resolve each pin via `OpenCodeProxy.getSession(sessionID, directory)` with bounded
  fan-out, using the pinned entry's stored directory.

Prune-vs-fail rule: if a pinned session is definitively gone (upstream HTTP 404), MacBridge prunes
that pin and omits it from the response. If the upstream fetch fails transiently (5xx / timeout /
network), `list_pinned_sessions` fails truthfully — it does NOT return fabricated or stale partial
summaries. Distinguishing these requires a typed upstream status error from the OpenCode proxy, not
`strings.Contains(err.Error(), "HTTP 404")`.

### Identity / keying

Pin keys are scoped by backend + backend-instance-scope + session ID, never by sessionId alone, so
the same session ID discovered under different projects / CODEX_HOME values does not collide. Keys
are computed by MacBridge, not iOS. Claude pin state lives in the existing `.cc-connect-session-meta`
sidecar (delete cleanup is automatic); Codex/OpenCode pin state lives in a MacBridge-owned index and
their `DeleteSession` paths clean the pin entry on delete.
