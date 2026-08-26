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
background_tasks.list
background_tasks.get
background_tasks.cancel
get_workspace_diff
get_turn_diff
get_full_thread_diff
get_usage
run_diagnostics
list_memory_files
read_memory_file
fetch_content_chunk
read_file_v2
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

`set_observation_scope` result `data` (schemaRevision `2026-08-23`) is additive:
`{ok, sessions:[{sessionId, subscribed, attached, error?}]}`. `ok` requires Subscribe
plus observer attach when the backend supports it. Failure code `observation_attach_failed`.
See relay-v1 §9.2.

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
| `workspace.read` | `get_workspace_diff`, `read_file_v2`, `list_directory`, `get_git_context`, `fetch_content_chunk`, `check_pull_request_support` | ✅ |
| `workspace.mutate` | `checkout_git_branch`, `create_git_branch`, `create_git_worktree`, `create_pull_request`, `commit_and_push`, `list_projects` | ✅ (recommend an owner per-action confirmation on top) |
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

### DeepSeek dead-session resume guard

For `send_message` on the `deepseek` backend with a non-empty resume id that is not live in the
MacBridge registry but DOES exist in the user's harness store (`~/.dsh/sessions`), the request
fails before any process spawn with error `session_resume_not_supported` (`retryable: false`):
the pinned DSH SDK (0.1.0-rc.6) exposes no cross-process resume — `session/prompt` on a known id
lazily creates a new agent+session pair, and the harness persistence refuses to rematerialize an
existing log (source-verified against pin `47f9438`). Clients surface this honestly (the session
has ended; start a new session to continue) instead of retrying. History of such a session remains
fully readable via `get_session_projection`/`get_session_messages` over the store. New sessions
(empty/pending ids) and registry-live sessions are unaffected.

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
turn_error
turn_aborted
error
permission_request
permission_resolved
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
session_retry_status
```

`turn_error` / `turn_aborted` settle a turn as failed/aborted at the Kernel (turn status
`error`/`aborted`, execution → idle); the wire shape is the reducer contract defined when
the cases landed (`turnId`, optional `message`, `done: true`). Producers active as of
2026-08-14: the codex live relay (`turn_aborted` on aborted tasks), the opencode live
subscriber (zero-output turns — provider resolution failures that the server otherwise
closes silently), and the idle-verified cold-hydrate seal for trailing unanswered user
turns (`reason: rich_history_unanswered`). Before that date the events existed in the
reducer/mailbox contract only, with no producer.

`session_retry_status` (2026-08-19, producer: opencode-web) is a **transient** control-plane
notice: the serve is retrying the provider call with backoff and the turn stays alive. Shape:
`{sessionId, attempt: number, message: string, next?: number(epoch-ms of the next attempt)}`.
It must NOT settle turn state, is not a durable milestone (no mailbox persistence), and is
NOT in the session-sync-v2 raw deny-list — raw delivery is its only carrier (the projection
kernel ignores it). Clients render it as a transient row (official web parity: the serve's
`session.status {type:"retry"}` row); older clients that do not know the name ignore it.

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
- `permission_resolved` closes a pending permission card in the Session
  Projection (`requiresPermissionConfirmation` cleared; status `running` on
  allow, `rejected` on deny). Producers: `resolve_permission` RPC success, and
  dsh-web host `approval/resolved`. Payload: `{ requestId, behavior }` where
  `behavior` is `allow` or `deny`. Idempotent.
- `resolve_permission.behavior` wire values are `"allow"` / `"deny"` /
  `"always"`. `"always"` is only sent by clients for `permission_request`
  events that carry the official payload (below); backends without an official
  always concept degrade it to a one-time allow (never deny).
- The iOS UI/native action enum (`approve` / `approveAlways` / `reject` /
  `rejectAlways`) is a **different layer** from the bridge wire `behavior`. iOS
  translates the UI action to the wire value before calling `resolve_permission`
  (`approve` → `"allow"`, `approveAlways` → `"always"` for official-payload
  requests or `"allow"` otherwise, `reject`/`rejectAlways` → `"deny"`).
  Clients MUST send `allow`/`deny`/`always` on the wire; legacy snake_case
  values are a bug, not an alternate vocabulary.
- **Official permission payload (opencode-web, v1.18)** — `permission_request`
  gains two additive, optional fields mirroring the live-pinned official
  `permission.asked` SSE frame (1.18.18 `/global/event`, 2026-08-19):
  - `permissionKind?: string` — official category key, e.g.
    `"external_directory"`. Clients render the category line via the official
    i18n catalog (`settings.permissions.tool.{kind}.description`, e.g.
    「访问项目目录之外的文件」); unknown keys render no category line.
  - `patterns?: string[]` — official patterns, e.g. `["/Users/x/Projects/Chat/*"]`,
    rendered one row each (monospace, break-all) like the official desktop dock.
  - Requests carrying these fields offer the official button triple
    拒绝/始终允许/允许一次 → wire `"deny"` / `"always"` / `"allow"`. Requests
    without them keep the legacy two-button card verbatim.
  - Field names/values are shape-pinned to the official frame
    (`agent/opencode-web` permlab capture + official desktop
    session-permission-dock.tsx); absence on other backends is by design.
  - The Session Projection part carries the same two fields
    (`permissionKind` / `permissionPatterns`) — the projected part is the
    permission-card SoT for SSV2 clients, so the kernel copies them from the
    wire event on reduce (non-empty merge: a thin duplicate from a same-serve
    legacy backend must not erase them).
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
  (lowercase SHA-256 prefix `"ui_"`): legacy Codex `interactionId = "ui_"+sha256("codex\0"+requestIdType+
  "\0"+requestIdValue+"\0"+threadId+"\0"+turnId+"\0"+itemId)[:32]`, Claude
  `interactionId = "ui_"+sha256("claudecode\0"+requestId)[:32]`; `questionId = interactionId+"_q_"+i`,
  `optionId = questionId+"_o_"+j`. The independent `codex-web` backend preserves the official
  request identity as `interactionId = threadId + ":" + itemId`; its question and option ids are
  deterministic children of that interaction id.
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

`schemaRevision` was bumped to `2026-08-02` for this additive set (new capability, RPC, part
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

1. `list_sessions` session-list pagination. These fields are additive and may be used when a backend returns the standard `{ sessions, nextCursor, hasMore }` envelope. The `cursor`/`nextCursor` exposed to clients is always **bridge-owned**: MacBridge never forwards an upstream catalog cursor across the bridge boundary (upstream cursors from Codex `thread/list` or Grok ACP are used only for MacBridge's internal bounded read). Clients MUST treat `cursor` as opaque and scoped to backend, bridge/backend identity, and project or directory bucket. The bridge-owned cursor is **durable across catalog subprocess/connection restarts**: it is NOT invalidated when the backend catalog process or the bridge connection restarts, as long as the catalog data fingerprint is unchanged. Only a fingerprint change (new/updated/removed sessions) or page-0 snapshot TTL expiry invalidates it, reported via `cursor_stale` (see § Cursor invalidity below). OpenCode `/session` is array-only and exposes no upstream cursor; MacBridge synthesizes the bridge-owned cursor over the bounded in-memory fetch.
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
- For OpenCode directory-scoped lists, the upstream `/session` endpoint is array-only (no upstream cursor). MacBridge fetches a bounded upstream page (≤100, the upstream API's hard cap), then synthesizes bridge-owned cursor pagination over that in-memory result; `hasMore` reflects the remaining bridge-owned slice for the current request scope. If the workspace has more than 100 sessions, the excess is silently invisible at the upstream boundary — a known ceiling imposed by the upstream API shape, not a bridge choice.
- `rootsOnly` remains valid for legacy/non-OpenCode list calls. OpenCode forwards it as the server-side root-session filter; clients must still scope cursors to the same backend/project/directory.

### Cursor invalidity (`cursor_stale`)

`cursor_stale` is the shared, generic cursor-invalidity error code for BOTH pagination surfaces — `list_sessions` session-list paging and `get_session_messages` history paging. It is a pagination-negotiation result, NOT a catalog live/stale state and NOT a UI error: clients MUST NOT surface it as an error. On receipt, the client discards the current cursor chain and reloads from page-0 (list) / the first page (history), merging the fresh result with unchanged local state by id.

Trigger reasons differ per surface:

- `list_sessions` (bridge-owned snapshot cursor, v2): the cursor's snapshot epoch no longer matches the current catalog fingerprint (sessions added/updated/removed); the page-0 snapshot has passed TTL with no fresh snapshot rebuilt; or no snapshot exists for the scope. A v1 cursor received from a connection that declared `catalog_cursor_epoch_v2` is also treated as stale (it carries no epoch).
- `get_session_messages` (history): the indexed transcript prefix the cursor referenced was rewritten, truncated, or replaced, so ancestry can no longer be proven continuous (see § Cursor semantics below).

The client rebuild contract is identical for both: drop the cursor chain, suppress error display, reload page-0 / first page.

### Capability: `catalog_cursor_epoch_v2`

A CLIENT opt-in capability for the bridge-owned session-list snapshot cursor (design `docs/2026-08-09-cross-backend-session-catalog-parity-implementation-plan.md` §4.1.1). The client declares `capabilities: ["catalog_cursor_epoch_v2"]` in `hello`; MacBridge echoes `capabilities["catalog_cursor_epoch_v2"] = true` in `hello_ack`.

- **Declared, non-Claude backend**: MacBridge emits the v2 bridge-owned cursor (opaque; carries a snapshot epoch derived from the catalog fingerprint, plus the v1 content position) and returns `cursor_stale` (per § Cursor invalidity) when the epoch mismatches, the snapshot expired, or a stale v1 cursor arrives. The client implements the `cursor_stale → discard chain + resend page-0` contract.
- **Declared, Claude backend**: declaration proves the client satisfies the minimum-client capability contract, but Claude has no supported native catalog. MacBridge therefore continues the dedicated Claude compatibility catalog successfully; its cursor remains v1-shaped and MUST NOT be presented as an epoch-v2 cursor.
- **Undeclared**: `list_sessions` fails with a stable wire error: `code = "protocol.capability_required"`, `retryable = false`, and a message containing `catalog_cursor_epoch_v2`. The error response MUST NOT contain a success-shaped `sessions` field, including an empty array. This applies to Codex, Grok, OpenCode, and Claude; undeclared clients have no generic v1 success path.

Advertising `catalog_cursor_epoch_v2` is an explicit promise that the client has implemented `cursor_stale → resend page-0`; clients MUST NOT advertise the capability while still treating `cursor_stale` as a fatal/unexpected error. This mirrors the `session_sync_v2` MUST-NOT-advertise contract: the capability is a versioned opt-in/out, not a runtime fallback.

**Intentional same-major minimum-client retirement**: the undeclared error is an intentional breaking deprecation under protocol major 1, approved as a minimum-client retirement. It MUST NOT be described as a non-breaking additive change. A runtime failure on a declared v2 path MUST NOT fall back to v1.

**Push semantics are independent**: an undeclared authenticated connection can still receive backend-scoped `sessions_changed` according to the normal subscription/observation rules. `catalog_cursor_epoch_v2` gates the `list_sessions` RPC/cursor contract only; it MUST NOT gate EventPublisher delivery. A subsequent undeclared `list_sessions` request returns `protocol.capability_required` as specified above.

**Release ordering**: supported clients MUST implement and advertise the capability before the server begins returning the undeclared error. In particular, the production remote-web client must ship the declaration and one-shot `cursor_stale → page-0` recovery first. Canonical protocol and client readiness MUST be published before the MacBridge service flip; code retained only for rollback does not restore a supported undeclared success contract.

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
`session_sync_v2`) and does not advertise this capability yet. **dsh-web** advertises it: its
official mux WebSocket stream is an agent-level broadcast covering EVERY session (including turns
started in the Mac web UI), pushed live through the same relay path. The external prompt
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

### Backend: `codex-web` (kind `codex-web`)

The `codex-web` backend is an independent official app-server client. It coexists with the legacy
`codex` backend: `backends[].id`, `backends[].kind`, registry identity, configuration keys, session
scope, and client cache scope are all `codex-web` and MUST NOT alias or migrate through `codex`.

Wire behavior:

- `displayName = "Codex Web"`, `liveEvents = "broadcast"`, and
  `requiresPollingForExternalTurns = false`. The Phase 0 daemon gate proves external Mac-hosted
  turns arrive through official app-server notifications, so `external_turn_streaming` is
  advertised; no rollout-file relay or discovery polling is used.
- Availability is the read-only lifecycle probe snapshot. Before a real service connection is
  established, the descriptor reports `not_configured`; it never claims availability from a
  placeholder endpoint. Catalog/history use official `thread/list`, `thread/read`, and subscription
  RPCs, so `session_history` and `session_sync_v2` are truthful capabilities.
- Tool approvals use the official server-request response frame and decision vocabulary
  (`accept`, `cancel`, or official `permissions`). The bridge advertises `permission_resolve`
  because the agent implements the session-level responder used for externally originated turns.
- Official `item/tool/requestUserInput` batches project through `structured_user_input_v1`.
  Each question contains two or three official options; the official `isOther` option is the free
  text path. Answers return as the official question-id-to-answer map. The backend has no verified
  reject response, so `canReject=false` and reject attempts fail closed.
- Models and supported reasoning efforts come from the official model catalog. Provider
  configuration is read-only unless an official write surface is separately proven; legacy Codex
  provider allowlists, default `xhigh`, rollout history, and permission-mode catalogs are not
  inherited.

Adding `codex-web` is a non-breaking extension of the descriptor space. Clients that do not know
the new kind ignore that backend while legacy `codex` continues unchanged.

### Backend: `dsh-web` (kind `deepseek-web`)

The `dsh-web` backend (hello_ack `backends[].id = "dsh-web"`, `kind = "deepseek-web"`,
display name "DeepSeek Web") forwards onto the official DeepSeek Harness Web API
(`dsh web`, default `127.0.0.1:3080`; MacBridge spawns a managed loopback instance
`3096..3196` when the user's own instance is absent). All data-format conversion is
MacBridge-side; the session store (`~/.dsh/sessions`) stays written only by the official
service (MacBridge never writes it or `workspace.json`).

Wire behavior:

- `liveEvents = "broadcast"`, `requiresPollingForExternalTurns = false`: the official mux
  WebSocket stream covers every session; external turns (started in the Mac web UI) stream
  live with `external_turn_streaming` advertised. Catalog changes additionally trigger
  `sessions_changed` through the host-stream immediate layer on top of the generic
  discovery watcher.
- `session_history` / rich history / SSV2 cold hydrate all derive from the official
  `session.history` (pathless family: no transcript file, no byte-cut checkpoint;
  re-open rebuilds fully). `session_sync_v2` is advertised.
- `send_message` maps to `session.prompt` (mode `queue`). Prompting an existing session
  uses the official resume semantics — there is NO "session ended" guard and NO
  `session_resume_not_supported` for this backend. Phase 1 is text-only: no attachment
  kinds are declared, so the two-level attachment gate rejects image/file uploads.
- Approvals/questions surface through the existing `permission_request` /
  `question_asked` events, answered via `resolve_permission` / `question_reply` /
  `question_reject`. dsh asks WHOLE QUESTION BATCHES: each question carries its own
  per-question id on the wire (iOS's replace-by-id upsert keeps every question
  answerable); MacBridge accumulates answers per question id and answers the batch once;
  `question_resolved` arrives per question id after the host settles the batch. A
  rejection cancels the whole batch. Approval outcomes are the official binary set
  (`allowed-once`/`rejected`); iOS's always-variants collapse to allow/deny on the wire
  (no semantic inversion; the always-persistence face does not exist in phase 1).
  Frames for sessions without a live bridge binding (web-only sessions) are not surfaced.
- Model/provider selection maps to the official session-scoped `session.selectModel`
  (there is no backend-global write surface); `list_providers` lists only
  runtime-active providers, `list_models` comes from the runtime catalog.
- `list_projects` serves the official workspace registry (`workspace.list`) as
  quick-pick directory suggestions; `list_directory` stays on the bridge-generic local
  filesystem browser shared by every backend.
- Not supported in phase 1 (existing generic `not_supported` paths; iOS hides the
  entries): `delete_session`, git surface (`get_git_context` / PR suite /
  `commit_and_push` / branch / worktree), diff suite, `fetch_todos`, `get_usage`,
  memory files, `list_permission_modes`/`set_permission_mode`, `list_agents`,
  `compress_context`, `share_session`. `rename_session` works on the RPC level; the
  iOS rename/archive entries follow the `session_mutation` convention (hidden until
  archive lands, identical to codex/opencode today).
- `run_diagnostics` reports the instance source (external probe hit / managed spawn,
  port, loopback-only disclosure) and the full provider registry with state bits; the
  `host.describe` version is an API-level identifier, NOT the npm package version.

Adding the backend is a non-breaking extension of the descriptor space (new
`backends[].kind` value; clients that do not know it ignore the backend).

### Backend: `opencode-web` (kind `opencode-web`)

The `opencode-web` backend (hello_ack `backends[].id = "opencode-web"`,
`kind = "opencode-web"`, display name "OpenCode Web") is the official
`opencode serve` HTTP/SSE client (design
`docs/2026-08-18-opencode-web-backend-design.md`). It coexists with the legacy
hybrid `opencode` backend until retirement: the Swift-managed server
(`opencode-managed-server.json`, port range `4096..4196`) keeps serving both —
`opencode-web` is a second client of the same resolved URL, never a second
supervisor, and never binds or spawns anything itself.

Wire behavior:

- `liveEvents = "broadcast"`, `requiresPollingForExternalTurns = false`: the
  official `/global/event` SSE (v2: `/api/event`) covers every session on the
  serve; external web turns stream live with `external_turn_streaming`
  advertised. Catalog changes (`session.created`/`session.deleted`) trigger
  `sessions_changed` through the catalog refresh signal on top of the generic
  discovery watcher. Empty URL ⇒ descriptor `not_configured` (no SSE
  subscription, no implicit legacy-port dialing).
- API generation is probed at startup (`/global/health` exists ⇒ 1.18
  un-prefixed routes; otherwise `/api/health` ⇒ v2 `/api` routes); the probe
  result (`generation=… url=…`) rides the descriptor reason and
  `run_diagnostics`. Bare-array vs `{data}` envelope is the final shape
  arbiter (1.18.18 also answers `/api/*` — dual presence is not proof of v2).
- Reads carry the session's own `x-opencode-directory` header (list uses the
  request directory; the go-bridge switchDir special-case keeps it correct
  for the four read methods —坑 5 修复). `session_history` / rich history /
  SSV2 cold hydrate derive from `GET /session/:id/message` (pathless family:
  re-open rebuilds fully). `session_sync_v2` is advertised.
- Context usage follows the official web formula: the LAST assistant message
  with positive token total over the runtime catalog's `limit.context`
  (`total = input+output+reasoning+cache.read+cache.write`). A missing window
  yields no usage value (iOS shows 暂无) — never a fabricated 200k. The v2
  `…/context` route (post-compact in-context messages) is NOT an occupancy
  source. `IsSessionActive` reads 1.18 `GET /session/status` (missing key =
  definitive idle; v2 `/api/session/active` absence is NOT a global idle
  verdict).
- `send_message` maps to `POST /session/:id/prompt_async` (v2:
  `…/prompt` after a session-scoped model switch) and ALWAYS carries a
  catalog model `{id, providerID}`. A model outside the runtime provider
  catalog fails the send RPC with zero POSTs; attachments are NOT declared
  in phase 1 (image/file uploads are rejected loudly, never silently
  dropped). A turn that arms but produces zero assistant output surfaces as
  `turn_error` with the diagnosable "model produced no output" text (never a
  healthy empty completion). `abort_generation` maps to `…/abort` (v2:
  `…/interrupt`); closing the iOS view only tears the SSE binding — it never
  aborts the running turn.
- Canonical additive revision (E1b sample-verified): `send_message.params.model`
  additionally carries optional `variant: string` — a model-specific OpenCode
  variant key, NOT `reasoningEffort`. It is accepted only when it is one of the
  selected model's live `/provider.all[].models[modelID].variants` keys; an
  unlisted key fails the send RPC with zero POSTs. `send_message.params.agent`
  (already canonical) selects the official agent and rides the same prompt
  atomically; per-request options travel session-scoped through
  `core.PromptOptionsSender` (`PromptOptions{Agent, ProviderID, ModelID,
  Variant}`) — no agent-global mutable selection. `list_models` model items
  gain optional `variants: string[]` containing exactly those live keys
  (empty/absent = no variant selector for that model).
- Approvals surface through the existing `permission_request` events (SSE
  `permission.asked`) and are answered by folding bridge `allow`/`deny` onto
  the official reply literals (1.18 probes `once`/`reject` first and falls
  back to `allow`/`deny` on 4xx; v2 replies `once`/`reject` directly). The
  serve holds the single answer lock: the first answerer (web UI or iOS)
  wins. Questions are NOT supported in phase 1 (`not_supported`; no banner).
- `list_providers`/`list_models` come from `GET /provider` (recursive runtime
  catalog; qualified `providerID/modelID` ids); `switch_model` records a
  pending selection that rides the next prompt (1.18 has no dedicated switch
  endpoint — the NEXT reply uses the new model; v2 switches via
  `POST …/model`). `list_agents` maps `GET /agent`; `list_projects` maps
  `GET /project` reading the `worktree` field (v2's `/api/location` is a
  single-location parser, NOT a project list — `not_supported` there).
- Not supported in phase 1: `fetch_todos` (todos not advertised),
  `get_usage`, memory files, diff suite, git surface,
  `list_permission_modes`/`set_permission_mode`, `set_agent_preset`,
  `delete_session` (HTTP delete not live-pinned), `rename_session` (until
  live-pinned), `share_session`, `resolve_user_input`.
- `run_diagnostics` reports the endpoint source, loopback-only disclosure,
  the generation probe detail, catalog/selected-model membership, and the
  permission-literal folding state.

Adding the backend is a non-breaking extension of the descriptor space (new
`backends[].kind` value; clients that do not know it ignore the backend).

### Capability: `supports_workspace_browse` (§6.5)

A backend advertises `supports_workspace_browse` in `capabilities` when its agent driver
implements the `core.WorkDirSwitcher` interface (claudecode, codex, opencode all do). iOS
uses this to gate the workspace file browser entry (distinct from the pre-session remote
directory picker). The capability is additive (extensible string, no major version bump).

### Capability: `supports_pull_requests` (§7.1)

A backend advertises `supports_pull_requests` when all three preconditions are met:
1. the agent implements `core.WorkDirSwitcher` (has a known workdir);
2. `git remote get-url origin` returns a URL containing `github.com`;
3. `gh` CLI is installed and authenticated on the Mac.

When present, iOS may show a "Create Pull Request" entry on the session Git panel;
when absent, the entry is hidden. The capability is additive (extensible string, no major
version bump).

### Capability: `supports_commit_message` (Phase 1 §4.1 B)

A backend advertises `supports_commit_message` when the agent driver implements
`core.CommitMessageGenerator` (one-off, non-interactive commit message generation).
Current implementations: `claudecode`, `codex` (`opencode`/`grokbuild` do not implement
it). When present, iOS may show the "Commit and Push" entry with a generated-message
option on the session Git panel; when absent, that entry is hidden. Additive (extensible
string, no major version bump).

### RPC: `commit_and_push` (Phase 1 §4.1 B)

Commits all changes (tracked modifications + new untracked files, honoring `.gitignore`)
and pushes the current branch. The commit message is either the caller-provided `message`,
or — when empty — generated non-interactively by the agent (`CommitMessageGenerator`).
It never touches a chat session or the timeline (SSV2 control-plane). `create_pull_request`
semantics are unchanged (not split).

Request:

```ts
{
  directory: string,   // required; must pass validateGitDirectory
  message?: string     // optional; empty → agent generates (requires supports_commit_message)
}
```

Response:

```ts
{
  head: string,        // new HEAD commit sha after commit
  pushed: boolean,     // true after a successful push
  remote: string       // upstream ref pushed to, e.g. "origin/main" or "origin/<branch>"
}
```

Failure codes: `invalid_params` / `invalid_directory` / `not_a_git_repo` /
`nothing_to_commit` (clean working tree) / `commit_message_generation_unsupported` (agent
has no `CommitMessageGenerator`) / `commit_message_generation_failed` /
`git_status_failed` / `git_add_failed` / `git_diff_failed` / `git_commit_failed` /
`push_rejected` (no upstream + detached HEAD, or push rejected) + real git stderr summary.

### RPC: `create_pull_request` (§7.1)

Creates a GitHub pull request from the current workspace. All params except `base` are
required.

| field | type | description |
|-------|------|-------------|
| `directory` | string | Git repository root (must pass `validateGitDirectory`). |
| `base` | string? | Target branch; absent → repository default. |

`title` / `body` are deprecated and ignored by the current handler. The PR title and body are generated on the Mac side by the session's agent from the real diff and the repository `PULL_REQUEST_TEMPLATE.md` (if present).

Server-side, the handler checks the GitHub remote, generates a sanitised branch name
`cordcode/<slug>` (whitelist `^cordcode/[a-z0-9][a-z0-9-]{0,60}$`), checks out or
creates the branch, pushes to origin, and invokes `gh pr create`. The response carries
`pr_url`, `branch`, and `base` (see `handlers_git.go` `handleCreatePullRequest`; an
earlier version of this doc also listed `remote_url`, which the handler never sends).

**PR template handling (T3-style).** Before invoking `gh pr create`, the server resolves
the repo root via `git rev-parse --show-toplevel` and reads the first existing
`PULL_REQUEST_TEMPLATE.md` (repo root, then `.github/`; ≤64 KiB; read-only — fixed
relative names only). The template text (if any) is included in the prompt sent to the
session's agent together with the real diff; the agent generates the title and body.
No placeholder/append merge protocol exists. The server never writes the template back
to the workspace or git.

### RPC: `check_pull_request_support` (§7.1)

Returns whether the current workspace directory supports PR creation **right now**.
It re-runs the same checks as `supports_pull_requests`: `git remote get-url origin` must
contain `github.com`, and `gh` must be installed. Clients call this when opening the diff
sheet instead of trusting a cached hello_ack capability, because the capability is
workdir-scoped and becomes stale after switching directories.

Request:

```ts
{
  directory: string
}
```

Response:

```ts
{
  supported: boolean
}
```

### RPC: `get_git_context` (§6.5 + Phase 1 §4.1)

Returns the git context for a workspace directory: repository root, current branch,
worktrees, local branches, and (Phase 1 §4.1, all optional / additive) workspace
status fields. Old clients ignore the optional fields.

Request:

```ts
{
  directory: string
}
```

Response:

```ts
{
  repositoryRoot: string,
  // detached HEAD → currentBranch is "" (empty string), never the literal "Detached HEAD".
  currentBranch: string,
  worktrees: { path: string, branch?: string, isCurrent: boolean }[],
  branches: string[],

  // Phase 1 §4.1 status extension (all optional; omitted → client degrades gracefully).
  isRepo?: boolean,            // true when rev-parse reached here; absent → treat as unknown.
  isDirty?: boolean,           // git status --porcelain non-empty (untracked counts as dirty).
  changedFileCount?: number,   // len(workspace diff files); INCLUDES untracked (not pure numstat).
  additions?: number,          // workspace diff additions; same source as changedFileCount.
  deletions?: number,          // workspace diff deletions; untracked files contribute 0.
  hasUpstream?: boolean,       // rev-parse --abbrev-ref @{u} success; no upstream → false (NOT error).
  aheadCount?: number,         // ONLY when hasUpstream; no upstream → omitted (NOT 0).
  behindCount?: number,        // ONLY when hasUpstream; no upstream → omitted (NOT 0).
  defaultBranch?: string,      // symbolic-ref origin/HEAD; no origin → omitted (client must NOT guess "main").
  openPullRequest?: { number: number, url: string, state: string } | null
                               // gh pr view for current branch; no PR / non-GitHub / no gh → omitted/null.
}
```

**Failure semantics (Phase 1 §4.1.1, authoritative):**
- `isRepo` failure → whole RPC error (no half-status).
- `isDirty` / `changedFileCount` / `additions` / `deletions`: all three derive from one
  `loadWorkspaceDiff` call; any failure → whole RPC error (same-error, no half-status, no
  independent field omission).
- `hasUpstream` no-upstream → `false` (exit 128), NOT an error.
- `aheadCount`/`behindCount` no-upstream → omitted (NOT 0).
- `defaultBranch` no-origin → omitted (client must NOT guess `main`).
- `openPullRequest`: no-PR and cannot-query are both omitted/null — client cannot distinguish
  them but must NOT fabricate a PR object. `gh pr view` network behavior needs real GitHub
  (verify with a fixture during implementation).

### RPC: `list_directory` params (§6.5)

`list_directory` accepts optional fields that refine listing behavior. All are additive
— clients that do not send them get the previous default behavior unchanged.

| field | type | default | description |
|-------|------|---------|-------------|
| `path` | string | (required) | Directory to list; `""` or `~` → home dir |
| `limit` | number | `200` | Max top-level entries; `0` → default. Capped to `500`. |
| `offset` | number | `0` | Skip N top-level entries before listing. |
| `depth` | number | `1` | Recursion depth. `1` = immediate children only; max `3`. Symlink entries are never recursed. |
| `workspace_root` | string | (absent) | When present, the handler validates `realpath(path)` starts with `realpath(workspace_root)`, rejecting `../` / absolute-outside traversals. Symlink entries are marked `isSymlink:true` and treated as unexpandable leaf nodes. When absent, the RPC retains the existing broad (picker) behavior — `path` is resolved via `expandPath` with no workspace-bound restriction. |

Response shape (all fields except `currentPath` and `items` are additive):

```json
{
  "currentPath": "/absolute/resolved/path",
  "items": [
    { "name": "main.go",  "path": "/.../main.go",  "isDirectory": false },
    { "name": "src",      "path": "/.../src",      "isDirectory": true },
    { "name": "src-link", "path": "/.../src-link", "isDirectory": false, "isSymlink": true }
  ],
  "limit": 200,
  "offset": 0,
  "depth": 1,
  "hasMore": false
}
```

| response field | description |
|----------------|-------------|
| `currentPath` | Canonical absolute path that was actually listed. |
| `items[].name` | Entry name (hidden files prefixed with `.` are filtered out). |
| `items[].path` | Full absolute path. |
| `items[].isDirectory` | `true` for directories (non-symlink). Symlink-to-dir reports `false`. |
| `items[].isSymlink` | `true` if the entry is a symlink (always a leaf — never recursed). |
| `limit` / `offset` / `depth` | Echoed for client-side pagination logic. |
| `hasMore` | `true` when more top-level entries exist beyond `offset + limit`. |

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
  files: Array<{ path: string; additions: number; deletions: number; diff?: string }>,  // diff = unified patch (RPC only; turn_diff_ready stays patch-free)
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

### Capability: `background_tasks` / `background_task_details`（Phase 4 只读任务中心）

后台任务 = Mac 端 agent 已启动、拥有独立任务身份、可脱离当前 timeline 继续执行并可集中查询的 agent task（roadmap §3.1）。它不是 todo、不是普通 tool row、也不是 root session 列表的一员（subagent 子 session 永远不回填 root 列表）。

能力与来源（单一派生点，护栏 C1）：

| Capability | 服务方 | 数据源 |
| --- | --- | --- |
| `background_tasks` | `dsh-web`（`core.BackgroundTaskProvider`）；`claudecode`（go-bridge sidechain registry） | dsh：官方 `session.list` 子任务行（origin=subagent + parentSessionId）；claude：`subagents/agent-*.meta.json` + `.jsonl`（与 B4 hydrate 同一文件、同一 status 派生函数） |
| `background_task_details` | 同上（detail 面） | claude detail = meta description + 嵌套子任务；dsh detail = 列表行本身（官方列表面无独立 instruction 字段，不造数） |

未声明能力的 backend 完全无任务面：iOS 不显示任何任务入口（无能力 = 无入口，不显示空列表假装支持）。

Claude 状态派生：`running/failed/completed` 由 B4 同款 reducer walk 产出（`buildSidechainAgentBlocks`，同一函数）；`cancelled` 是 summary 层信号，来自 meta 的 `stoppedByUser`（真实样本 2/159；B4 投影无 cancelled 概念，互不矛盾）。dsh 状态只有 `running/completed` 两态——官方列表行无失败信号，不发明。

### RPC: `background_tasks.list`

params: `{}`（跨 session 全量；排序 updatedAt 降序，服务端计算）

```ts
{ tasks: BackgroundTaskSummary[] }
```

`BackgroundTaskSummary`（未知字段整体 OMIT，客户端不得把缺失当 0 渲染）：

```ts
{
  taskId: string,            // 稳定任务 ID（claude sidechain agent id / dsh 子 session id）
  backendId: string,
  rootSessionId: string,     // 所属父 session
  parentTaskId?: string,     // 嵌套父任务（claude depth≥2）
  agentId?: string,
  title: string,             // 真实指令/标题文本
  agentName?: string,        // general-purpose 等
  status: "queued" | "running" | "completed" | "failed" | "cancelled",
  startedAt?: string, finishedAt?: string, durationMillis?: number,  // 后端计算
  tokenCount?: number, toolUseCount?: number,                        // 后端真实统计
  error?: string,
  transcriptAvailable: boolean,
  updatedAt: string
}
```

### RPC: `background_tasks.get`

params: `{ taskId }` → `{ task, instruction, nestedTasks: BackgroundTaskSummary[], capabilities: { cancel: boolean, retry: boolean } }`。`capabilities` 反映真实可操作性：dsh-web 运行中任务 `cancel=true`（官方 `session.cancel` 面）；终态任务与无取消面的 backend 恒 false。`retry` 当前所有 backend 均 false（无真实重试面，不假装）。错误码：`task_not_found`。

### RPC: `background_tasks.cancel`（Phase 5，capability `background_task_cancel`）

params: `{ taskId }` → `{ cancelled: true }`。仅在 backend 声明 `background_task_cancel`（实现取消面：dsh-web 官方 `session.cancel`）时可调；未声明 backend 诚实返回 `not_supported`（Claude sidechain 无 bridge 侧取消路径，不提供假取消）。`clear`/`retry` 暂无协议面：无真实数据源支撑，待 runtime 提供真实表面再增补（roadmap Phase D 逐 backend 开放原则）。

### Event: `background_tasks_changed`（Phase 5）

backend 级 control-plane invalidate 通知（与 `sessions_changed` 同形：带 backendId、非 session-scoped、broadcast）。触发源：session catalog 指纹变化（DSH 事件驱动 refresh 信号 / 各 backend discovery 周期）——同一变化同时意味着任务面可能变化。事件**不携带任务数据**：客户端收到后重新 `background_tasks.list` 拿权威真值（事件不做第二真值）。Claude 的 mid-run 子代理 live 推送仍属未来增强（B4 hydrate-only 现状，roadmap §2.1）；Claude 任务列表新鲜度由同一 discovery 指纹机制覆盖。

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
  reset to the first page. `cursor_stale` is the shared generic invalidity code for both this
  history surface and the `list_sessions` snapshot cursor — see § Cursor invalidity for the full
  trigger-reason / rebuild-contract enumeration.

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
MUST remain hydrating. A cold pull of a session whose source process is live may return a
partial containing an in-flight `running` turn — that partial is an authoritative subset of the
projection, not a final state; the turn is completed by subsequent `projection_patch` frames.

RPC lifecycle is explicit:

| State | Wire result | Meaning |
| --- | --- | --- |
| `hydrating` | error `projection.hydrating`, `retryable=true`, optional `retryAfterMillis` | healthy single-flight continues; client stays loading |
| `ready` | success `{projection}` or `{patches,headRev}` | complete committed head; only this may map into the active timeline |
| `failed` | error `projection.hydrate_failed`, `retryable`, optional `retryAfterMillis`/`attempts` | hydrate terminated; retry policy is explicit |
| not migrated | error `projection.not_migrated`, `retryable=false` | selected backend has no v2 authority |
| not found | error `projection.not_found`, `retryable=false` | backend session has neither kernel state, a live session, nor a file-backed source this bridge epoch (e.g. a DeepSeek id absent from the user harness store); nothing to serve — never an empty shell |

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
| {
    projection: BridgeSessionProjection,
    resume?: { kind: "full", reason: "cold" | "journal_gap" | "epoch_change" | "limit", requestedRev?: number }
  }
| {
    patches: BridgeProjectionPatch[],
    headRev: number,
    resume?: { kind: "at_head" | "journal", fromRev: number, toRev: number }
  }
```

MacBridge keeps a process/`bridgeEpoch`-scoped bounded journal of already-committed
`BridgeProjectionPatch` values. Journal entries preserve the canonical patch payload unchanged;
retention size/byte metadata remains outside the wire patch. When the retained suffix is contiguous
from `sinceRev` through the snapshot admission cut, the RPC returns that non-empty suffix plus
`headRev`. `sinceRev == headRev` returns an empty patch list. Any gap, retention miss, oversized
entry, epoch change, or head mismatch returns the authoritative `{projection}` form instead. The
journal is not a session store and MUST NOT reconstruct a projection, call history, or substitute a
cached snapshot.

`resume` is additive for compatibility and emitted by current MacBridge builds. `at_head` and
`journal` identify the exact admitted revision interval. A full response reports one typed reason:
`cold` for no requested revision, `journal_gap` for non-contiguous revisions, `epoch_change` when
the negotiated client's prior bridge epoch differs, or `limit` when rev-count, encoded-byte, age,
or oversized-entry retention removed the requested suffix. The production journal is bounded by
128 patches, 2 MiB per session, and 30 minutes; these bounds cover the P0 observed production
window (8 patches, 416–754 bytes over about 19 minutes) without becoming durable session storage.

#### Observed paired wire samples

The canonical sanitized production capture is under
`docs/protocol/samples/session-projection-v2/`. It contains both the real
`projection_patch` envelope and a later authenticated `get_session_projection` non-empty delta
response. The push `data` object equals the first pull `patches` element. Its observed patch key
presence is `baseRev`/`syncRev`/`execution`/`upsertTurns` present and
`partOps`/`replacesClientIds` absent; absence is preserved rather than normalized to empty arrays.
See the sample README for capture provenance, sanitization boundaries, and raw hashes.

Exact field shapes (`BridgeSessionProjection`, `BridgeTurnProjection`, `BridgeMessageProjection`,
`BridgeProjectionPart`, `BridgeProjectionPatch`, `BridgePartOp`, `BridgeExecutionView`) are defined
in `docs/protocol/schema/bridge-v1.types.ts`.

#### Tool part additive fields: `title` / `fileChanges` (ChatGPT-style activity rows)

`BridgeProjectionPart` tool variant gains two **optional** fields (non-breaking, lowerCamelCase):

| Field | Purpose |
|-------|---------|
| `title?: string` | Path-bearing display title (Claude Edit/Write `file_path`, Codex patch target, etc.). Clients use it for activity-row labels and `extractPrimaryPath` when structured file path is otherwise missing. |
| `fileChanges?: { path, kind?, movePath?, diff? }[]` | Structured file mutations for this tool step (Codex Patch / apply_patch). Same shape as UnifiedFileChange. |
| `requiresPermissionConfirmation?: boolean` | Pending tool must be approved before the turn continues (`permission_request`). Clients map to the existing permission card. Absent/false on older producers. |
| `permissionKind?: string` / `permissionPatterns?: string[]` | Official permission payload (opencode-web v1.18) carried on the projected permission part: category key + pattern rows so SSV2 clients render the official card (category line + patterns + reject/always/once). Mirrors the `permission_request` extras; additive, absent on other backends. |
| `permissionActions?: string[]` | Exact actions supported by this request (`approve`, `approveAlways`, `reject`, `rejectAlways`). When present, clients must render only these actions. Codex Web emits `approve`/`reject` because its official approval response has no distinct persistent “always” decision. Absent preserves the legacy client policy. |

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

## Projection Window (server-owned windowing) — FROZEN SPEC (not advertised)

Server-owned projection windows let MacBridge serve **bounded projections** (windows) to
capable clients instead of always shipping the full `BridgeSessionProjection`, and let a
client walk history with opaque cursors while live tail continues over the existing
projection stream. This section FREEZES the wire state machine, capability, ordering and
recovery semantics BEFORE any Mac/iOS production change. Nothing in this section is
advertised or implemented by a production MacBridge yet; the rollout flag stays off until
PERF-S4B/S4C. Design: `docs/2026-08-23-message-web-gpuix-borrowing-realistic-assessment.md`
§PERF-S4A (that plan is iOS-repo-local; THIS file is the canonical authority).

### Capability: `projection_window_v1`

A CLIENT opt-in capability, mirrored as a backend-scoped server promise, following the
`session_sync_v2` declaration pattern:

- The client declares `capabilities: ["projection_window_v1"]` in `hello`. MacBridge echoes
  `capabilities["projection_window_v1"] = true` in `hello_ack` only when BOTH (a) the
  server-side rollout flag is enabled and (b) the selected backend descriptor's projection
  kernel supports window admission. Per-backend support is additionally reported on the
  backend descriptor's `capabilities` list; clients MUST gate window RPCs on the descriptor
  list, not the global echo.
- **Prerequisite**: `session_sync_v2` MUST also be declared. A connection declaring
  `projection_window_v1` without `session_sync_v2` is a protocol violation: `hello` fails
  with `code = "protocol.invalid_capabilities"` (`retryable = false`). Windows are a
  projection-surface feature; there is no raw/history window path.
- **Undeclared peer**: behavior is EXACTLY today's — full `get_session_projection`, full
  `projection_snapshot`/`projection_patch`. Window RPCs from an undeclared connection fail
  with `code = "protocol.capability_required"`, `retryable = false`, message containing
  `projection_window_v1`, and MUST NOT include any success-shaped `window` field. This is an
  opt-in additive surface; the undeclared path is the compatibility path, not a fallback a
  declared client may drift back to.
- **Release ordering** (mirrors `catalog_cursor_epoch_v2`): canonical spec + client
  implementation (window state machine, cursor-discard recovery, typed-failure rendering)
  MUST ship before the server begins accepting/enforcing window RPCs; the MacBridge service
  flip happens last. Retained rollback code does not restore a supported undeclared window
  contract.
- **Disable/rollback**: omitting the capability (client) or turning the rollout flag off
  (server) returns every session to full-projection semantics. There is no per-session mix:
  once a session has been served a window on a connection, that connection's session state
  remains projection-owned; a client that wants full projection again simply issues
  `get_session_projection` (full) — windows never remove data, they bound delivery.

### RPC: `get_session_projection_window`

Pull-only RPC; sits beside `get_session_projection` and reads the SAME committed Kernel head
(single outbound funnel rules unchanged — windowed patches ride the existing
`projection_patch` events, there is no second pipe).

Request params (additive):

```ts
{
  sessionId: string,
  directory?: string,
  backendId: string,            // REQUIRED. Backend identity is part of the request scope.
  direction: "window_0" | "older" | "newer" | "latest" | "locate",
  cursor?: string,              // opaque, bridge-owned; required for older/newer
  limit?: number,               // max TURNS requested; hard cap below
  anchorTurnId?: string         // locate only
}
```

Response data (success):

```ts
{
  window: {
    windowId: string,           // opaque; embeds scope + generation
    generation: number,         // monotonic per (backendId, sessionId) within one bridgeEpoch
    coverage: "full" | "window",
    headTurnId: string | null,  // null + hasOlder=false => absolute head of the projection
    tailTurnId: string | null,  // null + hasNewer=false => live tail is inside this window
    hasOlder: boolean,
    hasNewer: boolean,
    nextOlderCursor?: string,   // present iff hasOlder
    nextNewerCursor?: string    // present iff hasNewer
  },
  turns: BridgeTurnProjection[], // window content: turn-aligned, ordered, deduplicated by turnId
  syncRev: number,               // kernel admission cut (baseRev for subsequent patches)
  resume?: { kind: "at_head" }
}
```

### Frozen rules (each normative "MUST/MUST NOT" is testable)

**R1 — Scope & cursor identity.** A wire cursor is **bridge-owned and opaque**. Its scope is
the tuple `(backendId, bridgeEpoch, sessionId)`. Clients MUST NOT parse cursor contents,
persist cursors across bridge epochs, or send a cursor obtained under one `backendId` with a
request naming another. A scope-mismatched cursor returns the stable typed error
`projection_window.cursor_scope_mismatch` (`retryable = false`) — never a wrong window, never
a silent page-0.

**R2 — Producer source-family boundary.** The wire cursor is ALWAYS projection-kernel-owned
(derived from committed turn ids + admission cut). Upstream pagination artifacts — Codex Web
`thread/read` cursor/limit, OpenCode Web message-API pagination, Claude/OpenCode transcript
file offsets — are internal to the Mac producer and MUST NOT be forwarded across the bridge
boundary or embedded verbatim in a wire cursor. A producer that can only page its source
API must reduce pages into the kernel first; the cursor the client sees refers to kernel
turn ids, so cursor stability equals kernel stability, not upstream API stability.

**R3 — Turn-aligned windows; unique page ownership.** Window boundaries are TURN-aligned.
A turn belongs to EXACTLY ONE window response (ownership by `turnId`); a response MUST NOT
end mid-turn, and overlapping requests (`older` then `newer`) deduplicate by `turnId` on the
client via the existing projection apply path — the server never duplicates a turn into two
pages of the same chain.

**R4 — Snapshot cut & live patch fence.** A window response is admitted at one kernel cut
and reports that cut as `syncRev`. Live patches after the cut flow through the existing
`projection_patch` events with `baseRev` fencing exactly as today; a window NEVER carries
inline live deltas. A patch whose `baseRev` doesn't chain from the window's `syncRev`
triggers the client's existing `get_session_projection(sinceRev=appliedRev)` alignment —
unchanged SSV2 recovery, no window-specific repair path.

**R5 — Bounds are assertable limits, not advice.** `maxWindowTurns = 256`,
`maxWindowEncodedBytes = 4 MiB` (encoded response payload), `limit <= maxWindowTurns`.
`limit > maxWindowTurns` returns `projection_window.limit_exceeded` (`retryable = false`).
The server MUST truncate at a turn boundary — when the byte bound binds first, fewer turns
are returned and `hasOlder`/`hasNewer` + the matching `next*Cursor` express the remainder.
A response MUST NOT split a turn to satisfy a byte bound.

**R6 — Cursor staleness & recovery.** A cursor is stale when: the projection epoch changed
(kernel rebuild/rewind), MacBridge restarted (different `bridgeEpoch` — generation is
epoch-scoped and resets), or kernel retention no longer admits the cursor's anchor turn.
Stale cursors return `cursor_stale` (shared code, § Cursor invalidity): client discards the
whole cursor chain and re-issues `window_0`. Relay reconnect/mailbox semantics are
unchanged — windows are pull-only; after reconnect the client re-establishes via
`window_0`/`latest`, never via mailbox replay.

**R7 — `latest` and live tail.** `direction: "latest"` returns the window ending at the
committed live tail (`tailTurnId = null`-semantics expressed via `hasNewer = false`) — the
reader-intent jump target. `direction: "newer"` walks toward the tail and MUST NOT skip
unloaded turns (strict turn-chain order).

**R8 — `locate` to an unloaded item.** `direction: "locate"` with `anchorTurnId` targets a
specific turn. If the turn is retained by the kernel, the server returns the window
containing it (anchor anywhere inside). If the turn id is unknown or outside retention
(windowing cannot reach it), the server returns `projection_window.locate_out_of_window`
(`retryable = false`); the client's ONLY honest fallback is a full
`get_session_projection` pull — the server MUST NOT fabricate a nearest-neighbor window.

**R9 — Failure rendering.** All typed errors above are protocol results, not UI errors.
`cursor_stale` is silent-recover (§ Cursor invalidity). `cursor_scope_mismatch`,
`limit_exceeded`, `locate_out_of_window`, `capability_required` surface once as a typed
state the client renders explicitly (message/retry affordance); none may be retried
automatically, none may be answered with a fabricated or empty window.

**R10 — Freeze scope.** This section specifies wire semantics only. It does not change
`projection_snapshot`/`projection_patch` shapes, does not add a second event pipe, and is
NOT advertised by any production MacBridge until PERF-S4B/S4C implement and gate the
producer/replica sides. Open product semantics discovered later are BLOCKERS to be re-frozen
here first; implementing agents MUST NOT guess them in code.

### Observed-sample contract

`docs/protocol/samples/projection-window-v1/` carries the canonical SYNTHETIC wire-shape
fixture (clearly labeled `provenance: "synthetic-spec-fixture"` — no production capture
exists yet because no producer ships). Schema/decoder tests on both platforms MUST decode it
and assert the frozen field set; when the first real producer capture lands (S4B), it
replaces the synthetic fixture under the same README discipline as
`session-projection-v2` (raw hashes + sanitization boundary).

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
