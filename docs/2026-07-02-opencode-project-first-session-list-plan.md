# OpenCode Project-First Session List Redesign

Date: 2026-07-02

Status: draft for review

Scope: `../cordcode-ios/` session list UX + `cordcode-macbridge/go-bridge` OpenCode proxy/API shape. This plan does not require modifying OpenCode upstream.

## 0. Summary

The current OpenCode session list should be redesigned around projects as the primary catalog. OpenCode stable `opencode serve` does not expose a reliable "all projects, all sessions" catalog through bare `GET /session`; when no directory is supplied, the server returns the `global` project page. That is why iOS cold start can show a large `jacklee`/global group while real Desktop projects such as `Chat` and `opencode-cc-connect` appear empty.

The desired product model is:

- Cold start fetches the project list first.
- Each project owns its own session bucket.
- Each bucket loads at most 10 sessions initially.
- "Load more" fetches the next 10 for that project only.
- The `global` project is treated as a low-priority "Global History / Unassigned" group, not as the main default project.

This replaces both bad extremes:

- Do not fan out `list_sessions` for every project at cold start.
- Do not rely on a single bare global `list_sessions` call as if it were all sessions.

Important implementation correction after source review: CordCode already has a generic `list_sessions` pagination response envelope (`sessions`, `nextCursor`, `hasMore`) in `go-bridge/pagination.go` and generic `handleListSessions`. OpenCode is unpaginated today because `handleOpenCodeRPC` routes `list_sessions` to the OpenCode HTTP proxy path, bypassing that generic handler. This plan must therefore reuse the existing response contract, not invent a new one. OpenCode still needs a separate implementation because it must page at the upstream OpenCode server, not fetch a global dump and slice it in bridge memory.

Implementation gate: before writing Swift or Go business logic, verify whether the stable OpenCode shared server exposes usable cursor pagination for directory-scoped sessions. If it does, implement the full "load more" flow. If it does not, ship only the project-first first-page model (`limit=10`, no load-more) and defer load-more until the server track exposes a verified cursor.

## 1. Evidence

Observed on the local shared OpenCode server at `http://127.0.0.1:4096`:

- `GET /project` returns 29 project records with `id` and `worktree`.
- Bare `GET /session` returns 100 sessions, all with `projectID = "global"`.
- `GET /session` with `x-opencode-directory: /Users/jacklee/Projects/Chat` returns Chat sessions.
- `GET /session` with `x-opencode-directory: /Users/jacklee/Projects/opencode-cc-connect` returns 57 sessions.
- `GET /session?limit=10` honors `limit`.

OpenCode source evidence:

- `packages/server/src/handlers/session.ts` defines `DefaultSessionsLimit = 50`, accepts `limit`, and returns `{ data, cursor }` in the v2 handler shape.
- `packages/core/src/session.ts` applies `limit` at query level.

CordCode currently sees a legacy/flattened response shape through `go-bridge/opencode-proxy.go`, so MacBridge must normalize both upstream shapes:

- raw array response
- `{ data: [...], cursor: { next, previous } }`

CordCode source evidence:

- Generic `list_sessions` already supports `limit`/`cursor` and returns `{ sessions, nextCursor, hasMore }` through `paginateSessionList`.
- Existing tests such as `TestListSessionsPagination` cover the generic bridge-owned cursor path.
- OpenCode bypasses that path via `handleOpenCodeRPC -> ocHandleListSessions`, which currently calls `ocProxy.listSessions(directory)` and returns all fetched sessions without list pagination metadata.

## 2. Current Failure Mode

The visible screenshot has four signals:

1. OpenCode Desktop project directories are not synced into iOS as first-class groups.
2. Manually added `Chat` and `opencode-cc-connect` groups show no sessions even though server-side directory-scoped calls return many sessions.
3. `jacklee` appears as a large group because bare `/session` returns the `global` project page, not all OpenCode projects.
4. A single global 100-session response is the wrong product unit; it can be consumed by one noisy group before real projects appear.

The previous attempted iOS change, "OpenCode = one global session catalog", should be superseded by this plan.

The root cause is therefore two-part:

1. iOS temporarily treated bare OpenCode `list_sessions` as an all-project catalog.
2. MacBridge's OpenCode proxy path does not yet expose project-scoped upstream pagination through the existing bridge response envelope.

## 3. Product Principles

1. Project list is the navigation skeleton.
   The session list should look useful after `list_projects` returns, even before every project has loaded sessions.

2. Session loading is per project.
   A project with many sessions should not starve other projects.

3. Cold start has a request budget.
   Initial OpenCode mode should issue:
   - one `list_projects`
   - session pages only for visible/high-priority projects, each capped at 10

4. Manual directories are pins, not a separate universe.
   If a manual directory matches an OpenCode project `worktree`, merge them.

5. `global` is special.
   It should be a collapsed/low-priority "Global History" group unless the user explicitly opens it.

6. No fake totals.
   Do not display "查看更多 (48)" unless the backend returns an actual total. Prefer "加载更多" or "再加载 10 条".

## 4. Proposed Data Model

### 4.1 Project Identity

Use `ProjectRoot` as the stable project list item, extended or accompanied by runtime state:

```swift
struct ProjectSessionBucket {
    let projectID: String
    let directory: String
    let displayName: String
    var sessions: [Session]
    var nextCursor: String?
    var hasMore: Bool
    var isLoading: Bool
    var errorMessage: String?
    var lastLoadedAt: Date?
}
```

For OpenCode:

- `projectID` comes from `/project[].id`.
- `directory` comes from `/project[].worktree` mapped to CordCode `ProjectRoot.directory`.
- `global` uses `projectID = "global"` and `directory = "/"` or a synthetic key; it is not merged with `/Users/jacklee`.

### 4.2 Session Directory Normalization

When a session has `projectID` and the project is known:

- display/group directory should be the project root directory
- raw session directory can be retained separately later if needed for diagnostics or sandbox labels

This prevents OpenCode sandbox/worktree sessions from splitting a project into many groups.

### 4.3 Manual Directory Merge

Merge order:

1. OpenCode `/project` result
2. manual pinned directories
3. inferred directories from cached sessions

If directory paths normalize to the same canonical path, keep the OpenCode project identity and mark it as pinned if the user had manually added it.

Normalize path aliases:

- `/private/tmp` and `/tmp`
- trailing slashes
- `~`
- case-insensitive compare only where the platform/filesystem makes that safe; otherwise preserve original path

Implementation note: path equivalence should use real filesystem normalization where available (`filepath.Abs` + `filepath.EvalSymlinks` on the Mac side, and the iOS-side equivalent only for strings already known to be local Mac paths as reported by Bridge). String-only comparison is not enough for `/tmp` versus `/private/tmp`.

## 5. MacBridge API Design

### 5.0 Existing Pagination Contract

Do not create a second response envelope for `list_sessions`. The bridge already uses:

```json
{
  "sessions": [],
  "nextCursor": "...",
  "hasMore": true
}
```

For Codex/Claude/generic backends, `cursor` is a bridge-owned composite cursor over an already fetched in-memory list. For OpenCode, `cursor` must be treated as an opaque upstream cursor returned by OpenCode, because fetching all sessions first would recreate the global-dump problem.

iOS must treat all `cursor` values as opaque and scoped to:

- backend kind
- bridge/backend identity
- project/directory bucket
- current process lifetime, unless a future protocol explicitly marks cursors as durable

This plan introduces **session list pagination**, not message-history pagination. It is unrelated to the currently disabled `session_pagination` capability in `agent_descriptor.go`, which refers to `get_session_messages` history paging.

### 5.0.1 OpenCode Must Not Be Merged Into Generic Pagination

Reuse only the response envelope shape. Do not merge OpenCode into the generic handler or generic cursor mechanism.

This is a deliberate architecture boundary, not a historical oversight. The 2026-06-13 session-loading redesign explicitly scoped file-backed backends and the OpenCode proxy path separately: "后端隔离：文件型后端与 OpenCode 代理路径分别处理"; it also notes that `ocProxy` list/history capability must be evaluated separately and records "OpenCode 路径不覆盖" as risk M5. In today's code, `isOC()` routes OpenCode HTTP-server mode through `handleOpenCodeRPC`, so generic `handleListSessions` is intentionally bypassed.

Do:

- Reuse `{ sessions, nextCursor, hasMore }` as the wire response shape.
- Fill that shape inside `ocHandleListSessions` from OpenCode's directory-scoped upstream response.
- Keep OpenCode session mapping on the proxy `mapSession` path so `projectId`, `parentId`, timestamps, model/provider, and message-count metadata remain intact.

Do not:

- Do not route OpenCode HTTP mode into generic `handleListSessions`.
- Do not reuse `paginateSessionList` for OpenCode; it requires a complete in-memory sorted list and would recreate the global-dump bug.
- Do not replace the proxy path with `agent/opencode.Agent.ListSessions()`; that CLI fallback lacks the HTTP directory scope and OpenCode project metadata needed here.
- Do not change the `isOC()` routing boundary as part of this feature.

### 5.1 Extend `list_sessions` Request

Existing request:

```json
{
  "method": "list_sessions",
  "backendId": "opencode",
  "params": {
    "directory": "/Users/jacklee/Projects/Chat",
    "rootsOnly": true
  }
}
```

Add optional pagination fields:

```json
{
  "directory": "/Users/jacklee/Projects/Chat",
  "limit": 10,
  "cursor": "opaque-upstream-cursor"
}
```

Response:

```json
{
  "sessions": [],
  "nextCursor": "opaque-upstream-cursor-or-null",
  "hasMore": true
}
```

Rules:

- `limit` is capped server-side, e.g. `1...50`.
- `cursor` is opaque and backend-specific.
- For OpenCode, `hasMore` should be true only when upstream returns a next cursor. Do not infer `hasMore` merely because a page contains exactly `limit` items; that creates an extra empty tail request and hides cursor-shape bugs.
- `rootsOnly` remains supported for existing non-OpenCode backends.
- For OpenCode directory-scoped pagination, `rootsOnly` must be either ignored with a documented reason or rejected with a clear error until OpenCode can filter parent/child sessions before pagination. Client-side filtering after a server-limited page breaks pagination math and can create short/empty pages or missed sessions.

### 5.2 OpenCode Proxy Changes

`ocHandleListSessions` must newly extract `limit` via `extractPositiveInt(msg, "limit")` and `cursor` via `extractStringParam(msg, "cursor")`, then pass both through to `ocProxy.listSessions(options)`. Today it extracts only `rootsOnly`; missing this handler wiring would silently drop iOS pagination parameters even if the proxy layer is extended correctly.

`OpenCodeProxy.listSessions` should accept:

```go
type OpenCodeSessionListOptions struct {
    Directory string
    Limit     int
    Cursor    string
}
```

It should call:

```text
GET /session?limit=<limit>&cursor=<cursor>
x-opencode-directory: <directory>
```

Omit the `cursor` query parameter entirely when the extracted cursor is empty; do not send `cursor=` with an empty value, since some OpenCode server versions reject an empty cursor. Likewise omit `limit` when it is zero or unset.

Then decode either:

```json
[ ...sessions ]
```

or:

```json
{
  "data": [ ...sessions ],
  "cursor": {
    "next": "...",
    "previous": "..."
  }
}
```

These are not just defensive version variants. They reflect OpenCode's stable server shape versus the newer source/v2 handler shape seen in the checked-out upstream source. The MacBridge proxy must support both intentionally and tests must cover both.

Acceptance:

- Directory-scoped `Chat` returns Chat sessions, capped by `limit`.
- Directory-scoped `opencode-cc-connect` returns its sessions, capped by `limit`.
- Bare/global request is not used for project groups at cold start.
- If upstream returns a cursor envelope, MacBridge preserves `cursor.next` as `nextCursor`.
- If upstream returns only an array, MacBridge returns no `nextCursor`; product pagination for that server track may be limited to the first page unless a verified cursor parameter is available.
- Refreshing a project bucket means refetching the first page without cursor. The first version only needs older-page load-more; it does not need bidirectional cursor UI.

### 5.3 `list_projects`

MacBridge already maps OpenCode `worktree` to CordCode `directory`. Keep that behavior.

Recommended response addition:

```json
{
  "projects": [
    {
      "id": "...",
      "directory": "/Users/jacklee/Projects/Chat",
      "name": "Chat",
      "backendProjectId": "..."
    }
  ]
}
```

If wire compatibility argues against a new field, reuse `id` as the OpenCode project id and keep `directory` as the worktree.

## 6. iOS Design

### 6.1 SessionsViewModel Ownership

Current single `sessions: [Session]` is too flat for OpenCode. Add project bucket state while preserving existing backend behavior.

Suggested approach:

- Keep existing `[Session]` for non-OpenCode until a broader refactor is justified.
- Add OpenCode-specific bucket aggregation path inside `SessionsViewModel`.
- Expose `groupedSessions` from buckets rather than from flat sessions when `backendKind == .openCode`.

This OpenCode-only branch is an intentional first step because OpenCode has project-first HTTP semantics. If session-list pagination is later generalized to Codex/Claude, the UI gate should move to a real `session_list_pagination` capability instead of hard-coding backend names.

### 6.2 Cold Start Flow

OpenCode mode:

1. Restore cached project buckets if available.
2. Fetch `list_projects`.
3. Merge projects + manual pins.
4. Render project sections immediately.
5. Load session page for:
   - current selected project, if any
   - preferred/pinned recent projects
   - visible first-screen projects up to a small cap, e.g. 3
6. Each session page uses `limit = 10`.

No cold-start fan-out over all projects.

Initial project-page loading should be bounded, for example at most 2 concurrent directory-scoped session requests. This avoids replacing "29 requests at once" with a smaller but still spiky burst.

### 6.3 Load More

Current sidebar `查看更多 (%d)` opens a sheet with already loaded sessions. Replace for OpenCode with per-project pagination:

- If bucket has `hasMore`, button label: `加载更多`
- Button action: `loadMoreSessions(projectID)`
- It appends next page to the bucket.
- The full project sheet can show loaded sessions plus a bottom loading/more control.

Do not show fake remaining counts unless the backend provides total counts.

Load-more visibility is driven by the bucket's `hasMore` flag, not by a compile-time product switch. On a no-cursor server track the bridge never sets `hasMore=true`, so the button simply does not appear and each project shows only its first page (`limit=10`); no pre-flight track decision is plumbed into iOS.

### 6.4 Empty State

Project group empty states should distinguish:

- not loaded yet: small spinner or "加载中"
- loaded and truly empty: "暂无会话"
- load failed: compact retry row

The screenshot's `Chat -> 暂无会话` is misleading if the bucket was never directory-loaded.

### 6.5 Global Group Policy

OpenCode `global` should not dominate default UI.

Suggested display:

- title: `全局历史`
- placement: below real projects
- initial state: collapsed or unloaded
- load only on tap

Do not silently reassign `projectID = "global"` sessions by default. Longest-prefix matching may be offered later as an explicit heuristic, for example "疑似属于 Chat", with a visible rule and a way to undo or ignore it. The first implementation should keep true `global` sessions in `全局历史` to avoid surprising moves.

## 7. Cache Design

Cache key should include:

- bridge/backend scope
- project id
- project directory

Persist:

- project list
- first page sessions per recently used project
- do not persist OpenCode cursors initially; treat them as memory-only opaque tokens

Stale cache behavior:

- Show cached projects immediately.
- Show cached first pages with stale banner.
- Refresh visible buckets in the background, not all buckets.

## 8. Migration From Current State

1. Revert/supersede the iOS OpenCode "single global catalog" special case.
2. Keep MacBridge fixes already proven necessary:
   - `/global/health`
   - `worktree -> directory`
   - OpenCode branch routes `list_directory`
3. Add paginated directory-scoped list support in the OpenCode proxy path, reusing the existing `list_sessions` response envelope.
4. Add iOS bucket state and OpenCode project-first loading.
5. Change sidebar "more" behavior from local sheet-only to per-project load-more.
6. Add cache migration or ignore old flat OpenCode list cache for the new project-bucket path.

## 9. Test Plan

### 9.1 MacBridge Go Tests

Add tests for:

- OpenCode session list passes `limit` and `cursor`.
- OpenCode session list sends `x-opencode-directory`.
- Response shape array decodes correctly.
- Response shape `{ data, cursor }` decodes correctly.
- `hasMore/nextCursor` mapping.
- OpenCode proxy path does not use generic `paginateSessionList` over a bare global dump.
- `ocHandleListSessions` extracts `limit`/`cursor` and httptest captures them in the upstream request URL.
- `rootsOnly + limit/cursor` is rejected or otherwise handled without client-side post-page filtering.
- Existing generic `TestListSessionsPagination` remains the authority for the bridge-owned cursor envelope.

### 9.2 iOS Unit Tests

Add tests for:

- OpenCode cold start calls `fetchProjects` once and does not call `fetchSessions` for every project.
- Initial visible bucket loading caps at 10 sessions per project.
- `loadMore(project)` only calls that project's `fetchSessions`.
- cursor is stored per project bucket and treated as opaque.
- Manual `Chat` merges with OpenCode project `Chat`.
- `global` sorts after real projects.
- Sandbox session with project id groups under project root.
- Empty state is `notLoaded` until the first project page returns.

### 9.3 Manual Validation

With external shared server on 4096:

1. Cold start iOS OpenCode mode.
2. Confirm project list includes Desktop projects such as `Chat` and `opencode-cc-connect`.
3. Confirm `Chat` shows up to 10 sessions initially.
4. Full cursor-capable track only: tap load more in `Chat`; confirm exactly one new directory-scoped request and up to 10 appended sessions.
5. Confirm `opencode-cc-connect` shows its real sessions.
6. Confirm `jacklee/global` no longer occupies the top as a normal project.
7. Confirm MacBridge log no longer shows a burst of project-count `list_sessions` calls on cold start.
8. Full cursor-capable track only: confirm repeated load-more calls do not produce duplicate session ids and hide the button when `hasMore=false`.
9. Array-only/no-cursor track only: confirm load-more is not shown and the first 10 sessions per loaded project still render.

## 10. Risks And Open Questions

1. Cursor shape in the current endpoint (implementation gate)
   The local curl path currently returned arrays for legacy `/session`, while source indicates v2 handlers can return `{ data, cursor }`. MacBridge must tolerate both. The actual stable server's cursor query parameter and response cursor behavior must be curl-verified before implementation. If stable has no cursor, load-more is deferred and only first-page project buckets ship.

   Decision tree:

   - If cursor parameter, cursor response, newest-first ordering, and "next means older" are all verified: ship full project bucket + load-more.
   - If cursor is absent or array-only: ship project bucket first page only; defer load-more until a server track exposes cursor pagination.
   - If ordering or cursor direction differ: adjust the first-page and load-more semantics before implementation.

2. Total counts
   OpenCode does not appear to return total counts. UI should avoid exact remaining counts.

3. Root/global project
   Need decide final UX wording and placement: `全局历史`, `未归类`, or hidden behind a filter.

4. Session moves
   If OpenCode later moves sessions between projects, iOS bucket cache must reconcile by session id and project id.

5. Non-OpenCode behavior
   Avoid dragging Claude/Codex into this refactor unless tests prove the shared bucket model is ready.

6. Cursor semantics
   The same RPC field name `cursor` will carry bridge-owned cursors for generic backends and upstream opaque cursors for OpenCode. This is acceptable only if clients never parse cursor contents and never reuse a cursor across backend/project scopes.

7. Capability naming
   If a capability is added, it must be named for session-list pagination, e.g. `session_list_pagination`, and must not be confused with the disabled message-history `session_pagination` capability.

8. Protocol pack synchronization
   Optional request/response fields are non-breaking, but the canonical `docs/protocol/` pack and the iOS mirror/models still need to be updated with the new list pagination fields.

9. OpenCode observability
   Basic request-budget counting is already possible through the common `"go-bridge: RPC request"` entry log, which includes method, backend id, and request id for OpenCode too. Rich diagnostics are still missing from `ocHandleListSessions`; add an OpenCode-specific list log for directory/project, limit, cursor-present, result count, next-cursor-present, and duration when implementing the proxy pagination path.

## 11. Implementation Sequence

Recommended order:

1. MacBridge: curl-verify stable OpenCode shared server and record:
   - cursor query parameter name
   - whether stable returns a cursor envelope or array-only
   - result ordering
   - whether next cursor means older sessions
   - observed server version
2. Decision gate:
   - full path if cursor + ordering/direction are verified
   - first-page-only path if stable is array-only or lacks cursor

   Note: both paths share **one runtime-adaptive bridge implementation**, not two code branches. The proxy dual-decodes the response shape (§5.2), omits `cursor` when empty, and derives `hasMore` from the upstream cursor at runtime. The decision gate determines only (a) which manual-validation steps apply (§9.3) and (b) what the protocol/acceptance text promises (§12). iOS load-more visibility is `hasMore`-driven, so no compile-time track flag is plumbed to the client; on an array-only server `hasMore` is never true and the button simply does not appear. Do **not** write `if cursorVerified { ... } else { ... }` branches in bridge or iOS code — when a server later gains cursor support, load-more must start working with zero code change.

3. MacBridge: extend only the OpenCode proxy path so both branches support directory-scoped `limit` and the existing `{sessions,nextCursor,hasMore}` envelope; enable upstream cursor pass-through only when verified by step 1.
4. MacBridge: add rich OpenCode list diagnostics in parallel with the proxy work; request-budget proof can already use the existing `"go-bridge: RPC request"` log.
5. MacBridge: resolve `rootsOnly` semantics for OpenCode paginated lists before exposing the path to iOS.
6. MacBridge: add Go tests for proxy limit/cursor, `x-opencode-directory`, dual response shapes, cursor preservation, observability, and `rootsOnly` behavior.
7. Protocol: update canonical `docs/protocol/` and the iOS mirror/models for optional `list_sessions.limit`, `list_sessions.cursor`, `nextCursor`, and `hasMore`; distinguish list pagination from message-history `session_pagination`.
8. iOS: introduce `ProjectSessionBucket` state and OpenCode-only project-first path.
9. iOS: update sidebar group rendering and per-project load-more behavior; hide load-more in first-page-only server tracks.
10. iOS: add unit tests for request budget, bucket pagination or first-page-only behavior, global ordering, manual merge, empty states, and opaque cursor scoping.
11. Build MacBridge Release and install.
12. Build iOS Debug and install to connected iPhone.
13. Run secret scan before release packaging if protocol/runtime files changed.
14. Owner manual validation on real external_http OpenCode shared server.

## 12. Acceptance Criteria

- Cold start no longer shows `Jacklee`/global as the dominant default group.
- OpenCode Desktop project directories are visible on iOS without manual re-adding.
- `Chat` and `opencode-cc-connect` show real sessions from the shared server.
- Each project initially renders no more than 10 sessions.
- Full cursor-capable track: loading more affects only the selected project.
- Array-only/no-cursor track: load-more is not shown; project first pages still work and older-session pagination is explicitly deferred.
- Cold start request count is bounded and explainable.
- OpenCode cold start `list_sessions` count is bounded by the selected/visible project budget, typically no more than 4, and is proven from `go-bridge.log`; the existing `"go-bridge: RPC request"` entry log is enough for request counts, while the new OpenCode list diagnostic log adds richer debugging fields.
- Full cursor-capable track: repeated project load-more calls issue exactly one directory-scoped request per tap, append without duplicate session ids, and stop when `hasMore=false`.
- No production mock/fallback data is introduced.
