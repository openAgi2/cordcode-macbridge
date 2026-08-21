# OpenCode Web 1.18.18 Gate B capability map

- Date: 2026-08-20
- Runtime: OpenCode **1.18.18** source `2cba7e227d`
- Gate A pack: commit `aad4b24`; A1–A10 independently captured
- Plan: `docs/2026-08-20-opencode-web-source-first-convergence-plan.md §5`
- Machine-readable source: `docs/2026-08-20-opencode-web-gate-b-capability-map.json`
- Completeness checker: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_b_map.py`
- Product code, WireDescriptor, bridge runtime, and iOS capability: **frozen**
- Gate B exited: **true**
- Gate B exit blockers: none

## Meaning of `supported now`

**supported now 只表示 Gate C 将完成完整 bridge+iOS 路径，不表示当前代码已经支持。**

**supported now + source-only 只是产品范围承诺，不是实施授权。** Source citations alone do not authorize writing event/response translators.

Gate C will implement the complete bridge+iOS path. It does not mean current product code already supports the surface. supported now + source-only is a product-scope commitment only, not implementation authorization.

## Gate S sample gate (input to Gate S, not authorization to start Gate S)

For every translated behavior whose Gate B row is source-only (no same-version captured sample), the Gate S C-slice impact record and S4 test map MUST register a prerequisite gate of 实现前补样本. Do not start that slice's event/response translation from source citations alone.

Each `supported now` row whose Gate A sample is `source-only` carries `gateSPreSampleGate: 实现前补样本`. The corresponding C-slice S3 impact record and S4 test map must list that prerequisite. Do not begin translators from source citations alone.

This document is a product decision map. It must not be used as authorization to change `WireDescriptor`, hello capabilities, or implementation.

## Counts

| Disposition | Count |
|---|---|
| supported now | 34 |
| deliberately unsupported | 5 |
| not applicable | 6 |
| future | 15 |
| **total** | **60** |

## Owner decisions (resolved 2026-08-20)

| ID | Topic | Status | Resolved decision |
|---|---|---|---|
| OD-1 | Archived session default visibility | **resolved** | `hide-in-default-list-keep-by-id` |
| OD-2 | Multi-directory session aggregation | **resolved** | `aggregate-global-list-keep-scoped-list` |
| OD-3 | C7 extras in the first convergence slice | **resolved** | `keep-mapped-future-or-unsupported` |

### Resolved statements
#### OD-1 — Archived session default visibility

- **Status:** resolved by owner on 2026-08-20
- **Resolved decision:** `hide-in-default-list-keep-by-id`
- **Resolved summary:** Default home/global list hides sessions with time.archived; GET-by-id still returns archived sessions.
- **Evidence:** A10: API roots=true still includes archived; GET /session/:id returns time.archived; official event-reducer.ts:149-161 splices archived out of the home list. Current CordCode maps ArchivedAt and comments say clients hide them.
- **Impact:** iOS session home list, get_session, archive mutation refresh. Choosing 'show archived in default list' would diverge from official Web.
- **Historical recommendation (now superseded as the decision):** Match official Web reducer: default home/global list hides rows with time.archived; GET-by-id still returns archived sessions; do not treat API list membership as UI visibility.

#### OD-2 — Multi-directory session aggregation

- **Status:** resolved by owner on 2026-08-20
- **Resolved decision:** `aggregate-global-list-keep-scoped-list`
- **Resolved summary:** iOS global list aggregates one official directory-scoped GET /session per project worktree; per-directory requests stay scoped.
- **Evidence:** A10: two directories do not leak ids. Official session-load.ts lists one directory. Current ListSessions in sessions.go already fans out per projectDirectories.
- **Impact:** iOS global session catalog versus per-workspace filter. Dropping aggregation would hide sessions outside the current work dir.
- **Historical recommendation (now superseded as the decision):** Keep CordCode product overlay: iOS global list aggregates one official directory-scoped GET /session per project worktree. Per-directory requests use the official scoped list only. Do not treat the official Web single-directory home list as a ban on CordCode aggregation.

#### OD-3 — C7 extras in the first convergence slice

- **Status:** resolved by owner on 2026-08-20
- **Resolved decision:** `keep-mapped-future-or-unsupported`
- **Resolved summary:** First C slice does not promote mapped future or deliberately unsupported C7 extras.
- **Evidence:** Plan §6 C7 says select only from this map. None of these have a Gate A P0 sample except children/list facts inside A10.
- **Impact:** Advertised iOS session/workspace commands. Promoting any item to supported now expands C7 scope.
- **Historical recommendation (now superseded as the decision):** Leave all of these as mapped (future or deliberately unsupported). Do not pull them into the first Gate C slice without a new sample pack.

## Semantic bounds already encoded (not owner-optional)

- **A6 once / always / reject** are separate rows. always is only “same isolated serve, same pattern not asked again”, never a grant that survives serve restart. reject finish is `tool-calls`, not a healthy completion.
- **A7** question is not permission. Official events are asked/replied/rejected. Canonical SSV2 path is `user_input_requested` / `user_input_resolved`. Do not invent `question_resolved`.
- **A8** todo is control-plane, not SessionProjection timeline. Live item keys are `content`/`status`/`priority` with **no id**. UnifiedTodo also has no id, so this is not silently adapted with a fake id.
- **A9** persist and provider understanding are separate. Vision/file-understanding cannot be `supported now` from the text-only mock conversion.
- **A10 / OD-1:** API list still returns archived; official Web reducer hides them; by-id GET still works. CordCode default list **hides** archived; by-id remains.
- **OD-2:** global list aggregates per project worktree; directory requests stay official scoped lists.
- **OD-3:** mapped future / deliberately unsupported C7 extras stay out of the first slice.
- **Observation:** direct SSE is the 1.18.18 ingest. Nested `sync` is evidence-only and skipped at pre-Kernel normalization. Dual ingest is forbidden.

## Surfaces by group

### sessions

| id | disposition | Gate A | current CordCode path |
|---|---|---|---|
| `sessions.list` | supported now | A10 | agent/opencode-web/sessions.go ListSessions / ListSessionsInDirectory — directory-scope... |
| `sessions.get` | supported now | A10 | agent/opencode-web/session_mutation.go FetchSessionInfo |
| `sessions.create` | supported now | A1 | agent/opencode-web/session.go ensureServerSession POST /session {} |
| `sessions.rename` | supported now | source-only | none — opencode-web does not implement core.SessionRenamer |
| `sessions.archive` | supported now | A10 | agent/opencode-web/session_mutation.go ArchiveSession |
| `sessions.delete` | supported now | source-only | agent/opencode-web/session_mutation.go DeleteSession |
| `sessions.children` | future | A10 | none as a product UI; A10 capture only |
| `sessions.fork` | future | source-only | none |
| `sessions.share` | deliberately unsupported | source-only | none |
| `sessions.unshare` | deliberately unsupported | source-only | none |
| `sessions.messages` | supported now | A1,A2,A4,A5 | agent/opencode-web/history.go GetRichSessionHistory |
| `sessions.init` | deliberately unsupported | source-only | none |

<details>
<summary>Full fields</summary>

#### `sessions.list`

- **Disposition:** supported now
- **Official UI:** packages/app/src/context/global-sync/session-load.ts:5-26 loadRootSessionsV1 session.list({directory, roots:true, limit})
- **Server/schema/reducer:** packages/opencode/src/server/routes/instance/httpapi/groups/session.ts ListQuery roots/limit/start/search/scope/path; GET /session
- **Gate A sample:** A10
- **Current CordCode path:** agent/opencode-web/sessions.go ListSessions / ListSessionsInDirectory — directory-scoped GET /session without roots/limit
- **Target product behavior:** Official roots+limit semantics per directory. Default home/global list hides rows with time.archived; GET-by-id still returns archived (OD-1 hide-in-default-list-keep-by-id). iOS global list aggregates one official directory-scoped GET /session per project worktree; directory-scoped requests stay scoped (OD-2 aggregate-global-list-keep-scoped-list).
- **SSV2 ownership:** OpenCode serve owns session catalog facts. iOS ProjectionStore does not write the catalog from raw HTTP.
- **SSV2 domain:** request/control (catalog). Timeline effects none.
- **WireDescriptor/capability future impact:** C2 changes list semantics; no new capability flag. Do not advertise session_pagination unless limit/cursor is actually implemented.
- **Rationale:** P0 listing is captured. Owner resolved OD-1 and OD-2. supported now means C2 will complete roots/limit plus these product rules, not that today's ListSessions is official-parity.
- **Dependency / evidence gap:** Current code missing roots=true and limit. Archive-hide and aggregation rules are owner-resolved, not remaining blockers.
- **Owner decision:** OD-1,OD-2
- **Gate S pre-sample gate:** —

#### `sessions.get`

- **Disposition:** supported now
- **Official UI:** packages/app/src/utils/server-compat.ts:170-173 legacy().session.get; packages/app/src/pages/session/use-session-commands.tsx sync().session.get
- **Server/schema/reducer:** GET /session/:sessionID SessionHttpApi.get
- **Gate A sample:** A10
- **Current CordCode path:** agent/opencode-web/session_mutation.go FetchSessionInfo
- **Target product behavior:** By-id GET remains valid for archived and mutated sessions; used to refresh catalog/metadata, not to manufacture timeline turns.
- **SSV2 ownership:** OpenCode serve session object. Kernel hydrate may read messages via a separate surface; get is metadata.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** none; existing get_session path
- **Rationale:** A10 proves archived-by-id 200 with time.archived. C2 retains by-id refresh.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `sessions.create`

- **Disposition:** supported now
- **Official UI:** packages/app/src/utils/server-compat.ts:163-169 create reduces to session.create({directory}); no model/agent in body
- **Server/schema/reducer:** POST /session Session.CreateInput; handlers/session.ts SessionHttpApi.create
- **Gate A sample:** A1
- **Current CordCode path:** agent/opencode-web/session.go ensureServerSession POST /session {}
- **Target product behavior:** Create with directory only; model/agent ride prompt_async, not create body.
- **SSV2 ownership:** OpenCode serve creates the session id. CordCode Kernel starts empty until hydrate/live.
- **SSV2 domain:** request/control then live/hydrate
- **WireDescriptor/capability future impact:** none
- **Rationale:** A1 captured create 200 and subsequent prompt. Current create body already matches v1.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `sessions.rename`

- **Disposition:** supported now
- **Official UI:** packages/app/src/utils/server-compat.ts:183-185 session.update({title}); packages/app/src/pages/layout.tsx session.update title
- **Server/schema/reducer:** PATCH /session/:id UpdatePayload.title
- **Gate A sample:** source-only
- **Current CordCode path:** none — opencode-web does not implement core.SessionRenamer
- **Target product behavior:** Rename via official PATCH title; catalog refresh after success. HTTP 2xx updates metadata only, not a timeline turn.
- **SSV2 ownership:** OpenCode session title. Catalog/control-plane.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** session_mutation already derived from SessionRenamer+Archiver+Deleter; adding RenameSession is C7, still no new flag name
- **Rationale:** Official Web rename is live. Audit 2026-08-20 sandbox renamed via PATCH. Gate A P0 did not recapture rename as its own scenario; C7 still implements it from source + audit sample. supported now = C7 will add SessionRenamer, not that it exists today.
- **Dependency / evidence gap:** No dedicated Gate A sanitized fixture named a-rename; use source + audit PATCH evidence until C7 adds a replay fixture. supported now + source-only is scope only. Gate S C-slice impact/test map must register 实现前补样本 before writing translators.
- **Owner decision:** —
- **Gate S pre-sample gate:** 实现前补样本

#### `sessions.archive`

- **Disposition:** supported now
- **Official UI:** packages/app/src/pages/layout.tsx command session.archive; packages/app/src/pages/home/home-sessions.tsx onArchiveSession; v1 server-compat archive helper is commented, but layout still archives
- **Server/schema/reducer:** PATCH /session/:id {time:{archived}}; reducer event-reducer.ts:149-161 hides archived from home list
- **Gate A sample:** A10
- **Current CordCode path:** agent/opencode-web/session_mutation.go ArchiveSession
- **Target product behavior:** Archive via official PATCH. Default CordCode home/global list hides archived (OD-1 hide-in-default-list-keep-by-id). By-id GET remains valid for archived sessions. API list membership is not UI visibility.
- **SSV2 ownership:** OpenCode session.time.archived. Catalog/control-plane, not messages[].
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** existing SessionArchiver; C2 must hide archived in the default list and keep by-id GET
- **Rationale:** A10 captured archive PATCH, list still containing archived, by-id GET. Owner resolved OD-1: hide in default list, keep by-id. Not leftover module behavior.
- **Dependency / evidence gap:** —
- **Owner decision:** OD-1
- **Gate S pre-sample gate:** —

#### `sessions.delete`

- **Disposition:** supported now
- **Official UI:** packages/app/src/utils/server-compat.ts:189-191 legacy().session.delete
- **Server/schema/reducer:** DELETE /session/:id success boolean
- **Gate A sample:** source-only
- **Current CordCode path:** agent/opencode-web/session_mutation.go DeleteSession
- **Target product behavior:** Delete via official DELETE; subsequent GET 404; catalog refresh. Do not keep a local ghost session.
- **SSV2 ownership:** OpenCode serve. Catalog invalidation only.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** existing SessionDeleter
- **Rationale:** Audit sandbox DELETE 200 true then GET 404. Current DeleteSession exists. C7 still needs targeted replay; supported now is the Gate C commitment.
- **Dependency / evidence gap:** No dedicated Gate A delete fixture in the A1-A10 pack. supported now + source-only is scope only. Gate S C-slice impact/test map must register 实现前补样本 before writing translators.
- **Owner decision:** —
- **Gate S pre-sample gate:** 实现前补样本

#### `sessions.children`

- **Disposition:** future
- **Official UI:** packages/app/src/pages/session/use-session-commands.tsx session.fork creates children; list uses roots=true so home omits children
- **Server/schema/reducer:** GET /session/:id/children; create with parentID
- **Gate A sample:** A10
- **Current CordCode path:** none as a product UI; A10 capture only
- **Target product behavior:** First slice: roots list omits children (covered by sessions.list). Child-session navigation stays future with fork (OD-3 keep-mapped-future-or-unsupported).
- **SSV2 ownership:** OpenCode parent/child ids. Not a timeline writer.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** none until a child-session UI is advertised
- **Rationale:** A10 proves children endpoint and roots omission. CordCode has no child-session product yet. Do not silently flatten children into the home list.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice. Depends on sessions.fork product () and a child-session UX.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `sessions.fork`

- **Disposition:** future
- **Official UI:** packages/app/src/utils/server-compat.ts:192-196; packages/app/src/pages/session/use-session-commands.tsx command.session.fork
- **Server/schema/reducer:** POST /session/:id/fork ForkPayload
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Out of first C slice Would create a new session id at a message point.
- **SSV2 ownership:** OpenCode new session. New Kernel instance per new session id.
- **SSV2 domain:** request/control then hydrate
- **WireDescriptor/capability future impact:** would need a new or extended session_mutation/fork capability if advertised
- **Rationale:** Official Web has fork. No Gate A sample. C7 optional. Do not implement from legacy adapter memory.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice. Need a live fork sample pack before any C-gate work.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `sessions.share`

- **Disposition:** deliberately unsupported
- **Official UI:** packages/app/src/pages/session/use-session-commands.tsx session.share copies a URL
- **Server/schema/reducer:** POST /session/:id/share
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** CordCode iOS/Link will not advertise OpenCode share links. Capability absent and UI unavailable.
- **SSV2 ownership:** n/a
- **SSV2 domain:** none
- **WireDescriptor/capability future impact:** must remain absent from hello capabilities
- **Rationale:** Shareable public URLs are an official Web desktop feature with no CordCode client equivalent. Advertising them would be a lie.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice. default keep unsupported. A product share-link design would be a new gate.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `sessions.unshare`

- **Disposition:** deliberately unsupported
- **Official UI:** packages/app/src/pages/session/use-session-commands.tsx session.unshare
- **Server/schema/reducer:** DELETE /session/:id/share
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Absent with share.
- **SSV2 ownership:** n/a
- **SSV2 domain:** none
- **WireDescriptor/capability future impact:** must remain absent
- **Rationale:** Paired with share. No CordCode unshare UI.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `sessions.messages`

- **Disposition:** supported now
- **Official UI:** session page loads messages via sync/message GET after create and on reopen
- **Server/schema/reducer:** GET /session/:id/message MessagesQuery limit/before
- **Gate A sample:** A1,A2,A4,A5
- **Current CordCode path:** agent/opencode-web/history.go GetRichSessionHistory
- **Target product behavior:** Cold hydrate reads official messages into Kernel private hydrate, never into iOS messages[] directly.
- **SSV2 ownership:** OpenCode messages are hydrate facts. Mac Kernel is the only timeline. iOS ProjectionStore applies the projection.
- **SSV2 domain:** hydrate
- **WireDescriptor/capability future impact:** existing rich history; C4/C3 must not let HTTP history bypass Kernel
- **Rationale:** A1-A5 reload GET /message. Current GetRichSessionHistory exists. supported now is the hydrate path, not an iOS second writer.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `sessions.init`

- **Disposition:** deliberately unsupported
- **Official UI:** session init command in official Web (AGENTS.md generator)
- **Server/schema/reducer:** POST /session/:id/init
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** CordCode will not expose OpenCode AGENTS.md init from iOS.
- **SSV2 ownership:** n/a
- **SSV2 domain:** none
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Desktop repo-bootstrap feature. No CordCode iOS equivalent. Capability absent and UI unavailable.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

</details>

### turns

| id | disposition | Gate A | current CordCode path |
|---|---|---|---|
| `turns.prompt` | not applicable | source-only | none for v1; v2 generation currently posts /prompt (session.go) which is a separate unv... |
| `turns.prompt_async` | supported now | A1,A2,A9 | agent/opencode-web/session.go Send — body currently {parts:[text], model} only; missing... |
| `turns.command` | future | source-only | none |
| `turns.shell` | future | source-only | none |
| `turns.abort` | supported now | A4 | agent/opencode-web session abort via existing AgentSession cancel path (must be re-pinn... |
| `turns.summarize` | future | source-only | none |
| `turns.revert` | future | source-only | none |
| `turns.unrevert` | future | source-only | none |

<details>
<summary>Full fields</summary>

#### `turns.prompt`

- **Disposition:** not applicable
- **Official UI:** v1 Web uses promptAsync only (server-compat.ts:200-230). Sync POST /session/:id/message is not the v1 Web composer path
- **Server/schema/reducer:** POST /session/:id/message session.prompt
- **Gate A sample:** source-only
- **Current CordCode path:** none for v1; v2 generation currently posts /prompt (session.go) which is a separate unverified generation
- **Target product behavior:** 1.18.18 CordCode path is prompt_async. Do not dual-call sync prompt.
- **SSV2 ownership:** n/a for v1
- **SSV2 domain:** none
- **WireDescriptor/capability future impact:** none; v2 remains fail-closed until its own pack
- **Rationale:** Official 1.18.18 Web composer does not use the blocking prompt route. CordCode first generation is v1 prompt_async.
- **Dependency / evidence gap:** v2 prompt is a different generation pack.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `turns.prompt_async`

- **Disposition:** supported now
- **Official UI:** packages/app/src/utils/server-compat.ts:200-230 promptAsync({messageID, agent, model, variant, parts})
- **Server/schema/reducer:** POST /session/:id/prompt_async PromptInput; httpapi/groups/session.ts:329-341
- **Gate A sample:** A1,A2,A9
- **Current CordCode path:** agent/opencode-web/session.go Send — body currently {parts:[text], model} only; missing messageID/agent/variant/file/agent parts
- **Target product behavior:** Send official fields. Correlate one user action to one persisted user message via stable messageID. Local composer echo is presentation-only.
- **SSV2 ownership:** OpenCode persist+SSE. Mac Kernel is the only CordCode timeline. iOS ProjectionStore is the only SSV2 writer.
- **SSV2 domain:** request/control admits the turn; live observation is the Kernel ingest
- **WireDescriptor/capability future impact:** C3 will require messageID/agent/model/parts; attachment kinds only when those parts are actually advertised
- **Rationale:** A1/A2/A9 captured 204 + persist. Current Send is incomplete. supported now is the C3 commitment.
- **Dependency / evidence gap:** Implementation must not ship until C3 replay of A1/A2/A9.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `turns.command`

- **Disposition:** future
- **Official UI:** packages/app/src/utils/server-compat.ts:241-265 session.command slash commands
- **Server/schema/reducer:** POST /session/:id/command
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Out of first slice. Slash-command composer is official Web, not current CordCode iOS composer.
- **SSV2 ownership:** Would be a request that yields live Kernel events.
- **SSV2 domain:** request/control then live
- **WireDescriptor/capability future impact:** do not advertise command/slash until sampled and implemented
- **Rationale:** No P0 sample. C7 optional. Owner resolved OD-3: keep current future/unsupported mapping.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice. Need command sample pack.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `turns.shell`

- **Disposition:** future
- **Official UI:** packages/app/src/utils/server-compat.ts:267-273 session.shell
- **Server/schema/reducer:** POST /session/:id/shell
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Out of first slice. Not an iOS first-party shell host.
- **SSV2 ownership:** Would be live Kernel if ever implemented.
- **SSV2 domain:** request/control then live
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Official Web shell is desktop-session scoped. No sample. Owner resolved OD-3: keep current future/unsupported mapping.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice. plus a live shell sample.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `turns.abort`

- **Disposition:** supported now
- **Official UI:** packages/app/src/utils/server-compat.ts:197-198 session.interrupt → abort
- **Server/schema/reducer:** POST /session/:id/abort returns boolean
- **Gate A sample:** A4
- **Current CordCode path:** agent/opencode-web session abort via existing AgentSession cancel path (must be re-pinned to A4 MessageAbortedError semantics in C4)
- **Target product behavior:** Abort converges to captured non-running state. Do not synthesize healthy finish=stop/completed.
- **SSV2 ownership:** OpenCode cancel+session.error/idle. Kernel terminal from authoritative error/idle, not a client timer.
- **SSV2 domain:** request/control then live
- **WireDescriptor/capability future impact:** existing abort/interrupt; C4 must match A4 error shape
- **Rationale:** A4 captured abort 200 true, MessageAbortedError/Aborted, finish unset, idle, status map without session.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `turns.summarize`

- **Disposition:** future
- **Official UI:** packages/app/src/utils/server-compat.ts:275-288 compact → session.summarize; use-session-commands.tsx session.compact
- **Server/schema/reducer:** POST /session/:id/summarize
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Out of first slice. Compaction is official Web, not current CordCode OpenCode-web advertisement.
- **SSV2 ownership:** Would mutate session messages; Kernel hydrate/live required, never raw iOS rewrite.
- **SSV2 domain:** request/control then hydrate/live
- **WireDescriptor/capability future impact:** do not advertise context compression for opencode-web until sampled
- **Rationale:** C7 optional. No sample. Owner resolved OD-3: keep current future/unsupported mapping.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice. Need summarize/compaction SSE sample.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `turns.revert`

- **Disposition:** future
- **Official UI:** packages/app/src/utils/server-compat.ts:290-293 revert.stage → session.revert
- **Server/schema/reducer:** POST /session/:id/revert
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Out of first slice.
- **SSV2 ownership:** OpenCode session.revert snapshot. Kernel rehydrate after revert; no iOS local undo writer.
- **SSV2 domain:** request/control then hydrate
- **WireDescriptor/capability future impact:** absent
- **Rationale:** No P0 sample. Owner resolved OD-3: keep current future/unsupported mapping.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice. Need revert sample including snapshot/patch.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `turns.unrevert`

- **Disposition:** future
- **Official UI:** packages/app/src/utils/server-compat.ts:295-297 revert.clear → session.unrevert
- **Server/schema/reducer:** POST /session/:id/unrevert
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Paired with revert; out of first slice.
- **SSV2 ownership:** OpenCode. Kernel rehydrate.
- **SSV2 domain:** request/control then hydrate
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Paired with revert. Owner resolved OD-3: keep current future/unsupported mapping.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

</details>

### content

| id | disposition | Gate A | current CordCode path |
|---|---|---|---|
| `content.text` | supported now | A1,A9 | agent/opencode-web/session.go Send text part; events.go text deltas |
| `content.reasoning` | supported now | source-only | agent/opencode-web/events.go reasoning cases |
| `content.tool` | supported now | A6,A7,A8 | agent/opencode-web/events.go tool cases |
| `content.file.persist` | supported now | A9 | session.go Send currently rejects images/files in phase 1 — incomplete versus this disp... |
| `content.file.vision` | future | A9 | none |
| `content.image.persist` | supported now | A9 | session.go Send rejects images in phase 1 |
| `content.image.vision` | future | A9 | none |
| `content.file_mention` | supported now | A9 | none |
| `content.agent_mention` | supported now | A9 | none in Send; agents.go ListAgents exists as catalog |
| `content.patch` | not applicable | source-only | none |
| `content.snapshot` | future | source-only | none |
| `content.step_markers` | not applicable | source-only | none as product UI |

<details>
<summary>Full fields</summary>

#### `content.text`

- **Disposition:** supported now
- **Official UI:** server-compat.ts:207-208 text part
- **Server/schema/reducer:** PromptInput TextPartInput; message.part.delta field=text
- **Gate A sample:** A1,A9
- **Current CordCode path:** agent/opencode-web/session.go Send text part; events.go text deltas
- **Target product behavior:** Text is the baseline user/assistant content. Distinct from reasoning/tool.
- **SSV2 ownership:** OpenCode parts. Kernel timeline. ProjectionStore applies.
- **SSV2 domain:** live / hydrate
- **WireDescriptor/capability future impact:** text is baseline, not a special flag
- **Rationale:** A1/A9 captured text persist and deltas.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `content.reasoning`

- **Disposition:** supported now
- **Official UI:** packages/app/src/context/global-sync/event-reducer.ts SESSION_CONTENT_EVENTS; Web renders assistant reasoning separately
- **Server/schema/reducer:** message parts type=reasoning; part.delta
- **Gate A sample:** source-only
- **Current CordCode path:** agent/opencode-web/events.go reasoning cases
- **Target product behavior:** Preserve reasoning versus answer text. Do not fold reasoning into assistant text.
- **SSV2 ownership:** OpenCode reasoning parts. Kernel distinct reasoning fields.
- **SSV2 domain:** live / hydrate
- **WireDescriptor/capability future impact:** existing reasoning events; do not drop the distinction in C4
- **Rationale:** Localmock healthy text turns did not emit reasoning. Mapping is source-backed and already in events.go. C4 replay must keep the distinction when a future sample includes it.
- **Dependency / evidence gap:** No populated reasoning sample in A1-A10. Fail closed if shape unknown; do not invent. supported now + source-only is scope only. Gate S C-slice impact/test map must register 实现前补样本 before writing translators.
- **Owner decision:** —
- **Gate S pre-sample gate:** 实现前补样本

#### `content.tool`

- **Disposition:** supported now
- **Official UI:** event-reducer SESSION_CONTENT_EVENTS; session tool parts
- **Server/schema/reducer:** message parts type=tool with state
- **Gate A sample:** A6,A7,A8
- **Current CordCode path:** agent/opencode-web/events.go tool cases
- **Target product behavior:** Tool parts stay tool parts. Permission/question/todo are separate interaction surfaces, not renamed tools.
- **SSV2 ownership:** OpenCode tool parts. Kernel tool timeline. Permission/question canonical events are not tool text.
- **SSV2 domain:** live / hydrate
- **WireDescriptor/capability future impact:** existing tool events
- **Rationale:** A6-A8 persisted tool-calls. Observe-only mapping is in scope for C4.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `content.file.persist`

- **Disposition:** supported now
- **Official UI:** server-compat.ts:209-221 file part mime+url+filename
- **Server/schema/reducer:** PromptInput FilePartInput
- **Gate A sample:** A9
- **Current CordCode path:** session.go Send currently rejects images/files in phase 1 — incomplete versus this disposition
- **Target product behavior:** C3 will accept official file parts and persist them. Attachment advertisement only after C3 is real.
- **SSV2 ownership:** Persisted user parts from OpenCode GET messages, never from provider conversion.
- **SSV2 domain:** request/control then live/hydrate
- **WireDescriptor/capability future impact:** must not advertise file attachment until C3 actually sends FilePartInput; current WireDescriptor correctly omits attachment kinds
- **Rationale:** A9: prompt_async 204 and persisted type=file. Current product rejects attachments. supported now is C3 work, not today's behavior.
- **Dependency / evidence gap:** C3 implementation + capability advertisement in the same slice.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `content.file.vision`

- **Disposition:** future
- **Official UI:** same file part path; provider request conversion is server-side
- **Server/schema/reducer:** session/llm request builder converts parts for the model
- **Gate A sample:** A9
- **Current CordCode path:** none
- **Target product behavior:** Do not claim the model understood a file. A9 mock received text-only (hasFile=false).
- **SSV2 ownership:** n/a until a vision-capable provider sample exists
- **SSV2 domain:** none
- **WireDescriptor/capability future impact:** must not advertise file-understanding / vision from A9 persist alone
- **Rationale:** A9 split: persist captured; provider mock conversion is text-only. Cannot mark vision supported now.
- **Dependency / evidence gap:** Need a vision-capable provider round trip sample.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `content.image.persist`

- **Disposition:** supported now
- **Official UI:** server-compat.ts file part with image mime
- **Server/schema/reducer:** FilePartInput mime image/png
- **Gate A sample:** A9
- **Current CordCode path:** session.go Send rejects images in phase 1
- **Target product behavior:** C3 persist image file parts. Do not claim visual understanding.
- **SSV2 ownership:** Persisted user file parts from OpenCode messages.
- **SSV2 domain:** request/control then live/hydrate
- **WireDescriptor/capability future impact:** image attachment flag only when C3 sends the part; not from persist evidence alone for vision
- **Rationale:** A9 persisted image/png file part after 204.
- **Dependency / evidence gap:** C3. Vision is a separate surface.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `content.image.vision`

- **Disposition:** future
- **Official UI:** same image file part; provider payload is server conversion
- **Server/schema/reducer:** LLM request conversion
- **Gate A sample:** A9
- **Current CordCode path:** none
- **Target product behavior:** Not in first slice. A9 hasImage=false on the mock.
- **SSV2 ownership:** n/a
- **SSV2 domain:** none
- **WireDescriptor/capability future impact:** do not advertise vision
- **Rationale:** A9 explicitly proves persist without vision. Cannot list visual understanding as supported now.
- **Dependency / evidence gap:** Real vision provider sample required.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `content.file_mention`

- **Disposition:** supported now
- **Official UI:** server-compat.ts:214-220 source mention on file part
- **Server/schema/reducer:** FilePartInput.source
- **Gate A sample:** A9
- **Current CordCode path:** none
- **Target product behavior:** C3 will send file parts with source mention when iOS composer mentions a file. Persist keeps source.
- **SSV2 ownership:** Persisted user parts.
- **SSV2 domain:** request/control then live/hydrate
- **WireDescriptor/capability future impact:** same attachment/mention advertisement as file persist; no extra flag unless C3 adds one
- **Rationale:** A9 persisted file + source.
- **Dependency / evidence gap:** C3 composer mention mapping.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `content.agent_mention`

- **Disposition:** supported now
- **Official UI:** server-compat.ts:222-228 agent part name + optional source
- **Server/schema/reducer:** PromptInput AgentPartInput
- **Gate A sample:** A9
- **Current CordCode path:** none in Send; agents.go ListAgents exists as catalog
- **Target product behavior:** C3 will send agent parts with source when mentioning an agent. Agent catalog is a separate configuration surface.
- **SSV2 ownership:** Persisted user parts. Agent selection also rides prompt_async.agent.
- **SSV2 domain:** request/control then live/hydrate
- **WireDescriptor/capability future impact:** agent_selection already exists as a possible capability; mention parts need C3
- **Rationale:** A9 persisted type=agent name=plan with source.
- **Dependency / evidence gap:** C3. ListAgents today is catalog only.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `content.patch`

- **Disposition:** not applicable
- **Official UI:** packages/app/src/context/global-sync/event-reducer.ts SKIP_PARTS includes patch
- **Server/schema/reducer:** part type patch may exist on the wire; Web content renderer skips it
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Do not project patch parts into SessionProjection timeline, matching official Web skip.
- **SSV2 ownership:** n/a as timeline content
- **SSV2 domain:** none (skip at pre-Kernel content filter, same class as Web SKIP_PARTS)
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Official Web does not treat patch as user-visible content. CordCode has no product equivalent as a message bubble.
- **Dependency / evidence gap:** Revert/diff may consume snapshots separately (future).
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `content.snapshot`

- **Disposition:** future
- **Official UI:** tied to revert snapshot in sessionInfo.revert
- **Server/schema/reducer:** Session.revert.snapshot; Snapshot file diffs
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Only if revert is promoted. Not a chat bubble.
- **SSV2 ownership:** OpenCode revert metadata. Not messages[].
- **SSV2 domain:** request/control / hydrate if revert lands
- **WireDescriptor/capability future impact:** absent until revert
- **Rationale:** Depends on turns.revert. OD-3.
- **Dependency / evidence gap:** OD-3
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `content.step_markers`

- **Disposition:** not applicable
- **Official UI:** event-reducer.ts SKIP_PARTS step-start and step-finish
- **Server/schema/reducer:** part types step-start / step-finish
- **Gate A sample:** source-only
- **Current CordCode path:** none as product UI
- **Target product behavior:** Skip as timeline content, matching official Web.
- **SSV2 ownership:** n/a
- **SSV2 domain:** none (skip)
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Official Web skips these parts. No CordCode step-marker bubble.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

</details>

### interaction

| id | disposition | Gate A | current CordCode path |
|---|---|---|---|
| `interaction.permission.once` | supported now | A6 | agent/opencode-web/permissions.go RespondSessionPermission folds allow→once; ToolAuthor... |
| `interaction.permission.always` | supported now | A6 | permissions.go allow/always fold → always |
| `interaction.permission.reject` | supported now | A6 | permissions.go deny → reject |
| `interaction.question.reply` | supported now | A7 | none (question answering ⛔ phase 1 per wire_descriptor.go) |
| `interaction.question.reject` | supported now | A7 | none |
| `interaction.todo` | supported now | A8 | events.go ignores todo.updated in phase 1; WireDescriptor does not advertise todos; no ... |

<details>
<summary>Full fields</summary>

#### `interaction.permission.once`

- **Disposition:** supported now
- **Official UI:** packages/app/src/pages/session/composer/session-permission-dock.tsx; server-compat.ts:496-503 permission.respond
- **Server/schema/reducer:** GET /permission; POST /session/:id/permissions/:id {response:once}; events permission.asked/replied; schema v1/permission.ts
- **Gate A sample:** A6
- **Current CordCode path:** agent/opencode-web/permissions.go RespondSessionPermission folds allow→once; ToolAuthorizer exists; WireDescriptor does not hand-write permission_resolve
- **Target product behavior:** once is this-request only. C6 implements from A6. Permission is control-plane plus Kernel-canonical permission state; raw path must not write messages[].
- **SSV2 ownership:** OpenCode permission request. Kernel canonical permission_request/resolved. ProjectionStore presents; no second writer.
- **SSV2 domain:** request/control + live canonical permission events
- **WireDescriptor/capability future impact:** C6 may advertise permission_resolve only when once/always/reject are all real
- **Rationale:** A6 captured asked, pending GET, session-scoped {response:once}, replied, pending cleared, turn continued.
- **Dependency / evidence gap:** C6. Do not advertise until all three replies work.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `interaction.permission.always`

- **Disposition:** supported now
- **Official UI:** session-permission-dock always action; server-compat permission.respond
- **Server/schema/reducer:** POST {response:always}; official always[] patterns persist inside that isolated serve
- **Gate A sample:** A6
- **Current CordCode path:** permissions.go allow/always fold → always
- **Target product behavior:** always means: in the same isolated serve, the same pattern was not asked again (A6 askedAgain=false). It is not a grant that survives serve restart or applies across devices.
- **SSV2 ownership:** OpenCode serve pattern store for that instance. Not CordCode durable ACL.
- **SSV2 domain:** request/control + live
- **WireDescriptor/capability future impact:** same permission_resolve flag; copy must not say permanent
- **Rationale:** A6 always then same pattern askedAgain=false on that isolated serve only. Do not enlarge this to a restart-surviving or cross-device authorization.
- **Dependency / evidence gap:** No sample of serve restart wiping or keeping always rules. Do not document restart-surviving authorization.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `interaction.permission.reject`

- **Disposition:** supported now
- **Official UI:** session-permission-dock reject
- **Server/schema/reducer:** POST {response:reject}; permission.replied; pending cleared
- **Gate A sample:** A6
- **Current CordCode path:** permissions.go deny → reject
- **Target product behavior:** Reject then observe actual turn: A6 assistant finish=tool-calls, idle, not a healthy stop/completed. Do not forge success.
- **SSV2 ownership:** OpenCode. Kernel terminal from real idle/error, not inferred completion.
- **SSV2 domain:** request/control + live
- **WireDescriptor/capability future impact:** same permission_resolve
- **Rationale:** A6 reject captured replied, pending cleared, idle, finish=tool-calls. Not a healthy completed assistant.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `interaction.question.reply`

- **Disposition:** supported now
- **Official UI:** packages/app/src/pages/session/composer/session-question-dock.tsx answers string[][]; server-compat.ts:507-515
- **Server/schema/reducer:** GET /question; POST /question/:id/reply {answers:string[][]}; events question.asked/replied — not question_resolved
- **Gate A sample:** A7
- **Current CordCode path:** none (question answering ⛔ phase 1 per wire_descriptor.go)
- **Target product behavior:** Distinct from permission. Reply uses 2D answers in question order. SSV2 canonical path is user_input_requested/resolved. Do not invent question_resolved. Do not deliver raw question_* to an SSV2 client.
- **SSV2 ownership:** OpenCode question request. Kernel canonical user_input_requested/resolved. ProjectionStore only SSV2 writer.
- **SSV2 domain:** request/control + live canonical user_input_*
- **WireDescriptor/capability future impact:** C6 may advertise structured_user_input_v1 only after reply+reject are real; never advertise question_resolved
- **Rationale:** A7 captured asked, pending questions[], POST answers:[['red']], question.replied, pending cleared, turn continued. Permission is a different surface.
- **Dependency / evidence gap:** C6. Current WireDescriptor correctly omits the flag.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `interaction.question.reject`

- **Disposition:** supported now
- **Official UI:** session-question-dock reject; server-compat.ts:513-515
- **Server/schema/reducer:** POST /question/:id/reject; event question.rejected
- **Gate A sample:** A7
- **Current CordCode path:** none
- **Target product behavior:** Reject then actual idle/error. Not a healthy completion. Canonical user_input_resolved with rejection, not question_resolved.
- **SSV2 ownership:** OpenCode. Kernel user_input_resolved.
- **SSV2 domain:** request/control + live
- **WireDescriptor/capability future impact:** paired with question.reply under structured_user_input_v1
- **Rationale:** A7 captured reject HTTP 200, question.rejected, pending cleared, idle, assistant not healthy stop.
- **Dependency / evidence gap:** C6
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `interaction.todo`

- **Disposition:** supported now
- **Official UI:** packages/app/src/pages/session.tsx sync().session.todo; session-todo-dock.tsx; reducer todo.updated
- **Server/schema/reducer:** GET /session/:id/todo; Todo.update; items {content,status,priority} with no id
- **Gate A sample:** A8
- **Current CordCode path:** events.go ignores todo.updated in phase 1; WireDescriptor does not advertise todos; no TodoProvider
- **Target product behavior:** Control-plane only. Do not enter SessionProjection timeline. Do not invent hash/content/position ids. UnifiedTodo in docs/protocol/unified-bridge-protocol.md §6.7 is content/activeForm?/status — no id required, so protocol does not force a fake id.
- **SSV2 ownership:** OpenCode todo list. Explicit control-plane exception. Not Kernel timeline. Not iOS messages[].
- **SSV2 domain:** request/control (allowed control plane)
- **WireDescriptor/capability future impact:** C6 may advertise todos only as control-plane; must not imply timeline parts or stable synthetic ids
- **Rationale:** A8 items keys exactly content/priority/status, two snapshots replace pending/in_progress with completed, no id. UnifiedTodo has no id so this is not blocked on protocol. If a future protocol revision required id, this row would move to future rather than silently adapt.
- **Dependency / evidence gap:** C6 control-plane mapping. Identity remains the official (content,status,priority) row replacement semantics.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

</details>

### workspace

| id | disposition | Gate A | current CordCode path |
|---|---|---|---|
| `workspace.project` | supported now | source-only | agent/opencode-web/projects.go ListProjectSuggestions / projectDirectories |
| `workspace.path` | not applicable | source-only | none |
| `workspace.file_list` | future | source-only | none |
| `workspace.file_read` | future | source-only | none as a workspace API; tool read may appear as content.tool |
| `workspace.file_search` | future | source-only | none |
| `workspace.session_diff` | future | source-only | events.go currently ignores session.diff |
| `workspace.vcs` | future | source-only | none |
| `workspace.pty` | not applicable | source-only | none |
| `workspace.mcp` | deliberately unsupported | source-only | none |

<details>
<summary>Full fields</summary>

#### `workspace.project`

- **Disposition:** supported now
- **Official UI:** server-compat.ts:301-309 project.list / project.current
- **Server/schema/reducer:** GET /project; worktree list
- **Gate A sample:** source-only
- **Current CordCode path:** agent/opencode-web/projects.go ListProjectSuggestions / projectDirectories
- **Target product behavior:** Project/worktree registry feeds OD-2: global list aggregates one scoped GET /session per project worktree; directory requests remain official scoped lists. Catalog only, not a timeline writer.
- **SSV2 ownership:** OpenCode project registry. Catalog.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** none
- **Rationale:** Audit verified project list shape. Owner resolved OD-2 aggregate-global-list-keep-scoped-list. C2 must keep /project as catalog, not filesystem session truth.
- **Dependency / evidence gap:** source-only catalog shape. Gate S C2 impact/test map must register 实现前补样本 before new project-list translation work. Current fan-out code is not an implementation grant.
- **Owner decision:** OD-2
- **Gate S pre-sample gate:** 实现前补样本

#### `workspace.path`

- **Disposition:** not applicable
- **Official UI:** server-compat.ts path.get is commented out for v1
- **Server/schema/reducer:** path routes exist on the server; v1 Web does not call them
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** No CordCode path.get product. Directory is session.directory / worktree.
- **SSV2 ownership:** n/a
- **SSV2 domain:** none
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Official v1 Web does not use path.get. No CordCode equivalent.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `workspace.file_list`

- **Disposition:** future
- **Official UI:** server-compat.ts:360-365 file.list
- **Server/schema/reducer:** file list routes in httpapi/groups/file.ts
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Out of first slice. iOS is not an OpenCode file explorer in this convergence.
- **SSV2 ownership:** Would be control/catalog if added.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** absent
- **Rationale:** C7 optional workspace. Owner resolved OD-3: keep current future/unsupported mapping.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice. + samples
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `workspace.file_read`

- **Disposition:** future
- **Official UI:** file content routes; distinct from the read tool inside a turn
- **Server/schema/reducer:** file content endpoints in file.ts
- **Gate A sample:** source-only
- **Current CordCode path:** none as a workspace API; tool read may appear as content.tool
- **Target product behavior:** Workspace file viewer out of first slice. Tool-read inside a turn is content.tool.
- **SSV2 ownership:** n/a as workspace UI
- **SSV2 domain:** none for first slice
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Do not confuse official file API with in-turn read tools. Owner resolved OD-3: keep current future/unsupported mapping.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `workspace.file_search`

- **Disposition:** future
- **Official UI:** server-compat.ts:366-376 file.find
- **Server/schema/reducer:** find files/text in file.ts
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Out of first slice.
- **SSV2 ownership:** n/a
- **SSV2 domain:** request/control if added
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Owner resolved OD-3: keep current future/unsupported mapping.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `workspace.session_diff`

- **Disposition:** future
- **Official UI:** event-reducer session.diff; diffs helpers
- **Server/schema/reducer:** GET /session/:id/diff
- **Gate A sample:** source-only
- **Current CordCode path:** events.go currently ignores session.diff
- **Target product behavior:** Out of first slice. Not a timeline message.
- **SSV2 ownership:** OpenCode file diffs. Control/workspace, not messages[].
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** do not advertise workspace diff for opencode-web until implemented
- **Rationale:** Official Web shows session file changes. No Gate A populated diff fixture. Owner resolved OD-3: keep current future/unsupported mapping.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice. Need diff sample.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `workspace.vcs`

- **Disposition:** future
- **Official UI:** server-compat.ts:333-358 vcs.status / vcs.diff
- **Server/schema/reducer:** vcs routes
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** Out of first slice.
- **SSV2 ownership:** OpenCode vcs. Control-plane.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Owner resolved OD-3: keep current future/unsupported mapping.
- **Dependency / evidence gap:** Owner resolved OD-3 keep-mapped-future-or-unsupported. Do not pull into the first Gate C slice.
- **Owner decision:** OD-3
- **Gate S pre-sample gate:** —

#### `workspace.pty`

- **Disposition:** not applicable
- **Official UI:** server-compat.ts:452-492 pty.list/create/get/update/remove
- **Server/schema/reducer:** pty routes
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** CordCode iOS is not an OpenCode terminal host.
- **SSV2 ownership:** n/a
- **SSV2 domain:** none
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Official Web desktop terminals have no CordCode phone equivalent.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `workspace.mcp`

- **Disposition:** deliberately unsupported
- **Official UI:** OpenCode desktop MCP configuration surfaces (not in v1 session composer path)
- **Server/schema/reducer:** mcp httpapi group
- **Gate A sample:** source-only
- **Current CordCode path:** none
- **Target product behavior:** MCP server management stays on the Mac OpenCode config. iOS will not advertise MCP setup.
- **SSV2 ownership:** n/a
- **SSV2 domain:** none
- **WireDescriptor/capability future impact:** absent
- **Rationale:** MCP is a desktop/serve configuration surface. CordCode iOS has no MCP admin UI in this convergence.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

</details>

### configuration

| id | disposition | Gate A | current CordCode path |
|---|---|---|---|
| `configuration.providers` | supported now | source-only | agent/opencode-web/models.go fetchModelCatalog connected-only |
| `configuration.models` | supported now | A1 | models.go AvailableModels / resolveSendModel — implements a shorter fallback than offic... |
| `configuration.default_model` | supported now | source-only | session.go resolveSendModel session-adopted then first connected default |
| `configuration.agents` | supported now | A1 | agents.go ListAgents; Send currently omits agent field |
| `configuration.variants` | supported now | A1 | none — Send never sends variant |
| `configuration.provider_auth` | deliberately unsupported | source-only | none on iOS; Mac OpenCode Desktop/Link owns credentials |

<details>
<summary>Full fields</summary>

#### `configuration.providers`

- **Disposition:** supported now
- **Official UI:** packages/app/src/hooks/use-providers.ts connected set; prompt-model-selection.ts
- **Server/schema/reducer:** GET /provider list all/connected/default
- **Gate A sample:** source-only
- **Current CordCode path:** agent/opencode-web/models.go fetchModelCatalog connected-only
- **Target product behavior:** Use connected providers only. Fail closed if catalog missing. iOS does not configure provider keys (see provider_auth).
- **SSV2 ownership:** OpenCode provider catalog. Control-plane.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** existing model catalog; C5 removes leftover recursive parsers
- **Rationale:** Audit verified provider list shape. Current catalog is connected-only, matching official picker validity.
- **Dependency / evidence gap:** supported now + source-only is scope only. Gate S C-slice impact/test map must register 实现前补样本 before writing translators.
- **Owner decision:** —
- **Gate S pre-sample gate:** 实现前补样本

#### `configuration.models`

- **Disposition:** supported now
- **Official UI:** prompt-model-selection.ts current/agent/configured/recent/connected fallback
- **Server/schema/reducer:** provider models in GET /provider
- **Gate A sample:** A1
- **Current CordCode path:** models.go AvailableModels / resolveSendModel — implements a shorter fallback than official 5-level chain
- **Target product behavior:** C5 implements or explicitly excludes each official fallback level with distinct fixtures.
- **SSV2 ownership:** OpenCode catalog + selected model on the prompt. Not a timeline writer.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** model_switch already possible via ModelSwitcher; C5 must match real fallback
- **Rationale:** A1 sends model{providerID,modelID}. Official picker has five levels; current resolveSendModel is not full parity. supported now = C5 will finish it.
- **Dependency / evidence gap:** C5 fixtures per fallback level. No sample of variant-selected prompt.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `configuration.default_model`

- **Disposition:** supported now
- **Official UI:** prompt-model-selection.ts configured() via resolveDefaultModel
- **Server/schema/reducer:** provider.list default plus config.model
- **Gate A sample:** source-only
- **Current CordCode path:** session.go resolveSendModel session-adopted then first connected default
- **Target product behavior:** Honor official configured default when valid and connected. Never invent a model id.
- **SSV2 ownership:** OpenCode config/catalog.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** C5
- **Rationale:** Part of official picker. Current code has a subset. C5 must not POST a model absent from connected catalog.
- **Dependency / evidence gap:** C5 distinct fixture for configured default. supported now + source-only is scope only. Gate S C-slice impact/test map must register 实现前补样本 before writing translators.
- **Owner decision:** —
- **Gate S pre-sample gate:** 实现前补样本

#### `configuration.agents`

- **Disposition:** supported now
- **Official UI:** composer agent selection; promptAsync.agent
- **Server/schema/reducer:** GET /agent; PromptInput.agent
- **Gate A sample:** A1
- **Current CordCode path:** agents.go ListAgents; Send currently omits agent field
- **Target product behavior:** Carry selected agent on prompt_async. Catalog from GET /agent.
- **SSV2 ownership:** OpenCode agent catalog + prompt field.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** agent_selection must only be advertised when Send actually forwards agent
- **Rationale:** A1/A2 captured agent=build. ListAgents exists. C3 must add the field to Send.
- **Dependency / evidence gap:** C3 prompt body.agent.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `configuration.variants`

- **Disposition:** supported now
- **Official UI:** prompt-model-selection cycleModelVariant; promptAsync.variant omitted when unset
- **Server/schema/reducer:** PromptInput.variant optional
- **Gate A sample:** A1
- **Current CordCode path:** none — Send never sends variant
- **Target product behavior:** Omit variant when unset (already the official omit). When iOS selects a variant, C3/C5 must send it. No sample yet of a set variant.
- **SSV2 ownership:** OpenCode prompt field.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** no extra flag; do not advertise variant UI until a set-variant sample exists
- **Rationale:** A1 proves omit-when-unset. Sending a selected variant is still source-only. Disposition is supported now for omit parity; selected-variant UI stays unimplemented until sampled — documented in dependencyOrGap, not as a second implicit surface.
- **Dependency / evidence gap:** Need a live sample with variant set before advertising variant cycling in iOS.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `configuration.provider_auth`

- **Disposition:** deliberately unsupported
- **Official UI:** server-compat.ts:378-449 integration.get / connect.key / oauth
- **Server/schema/reducer:** GET /provider/auth; POST oauth authorize/callback; auth.set
- **Gate A sample:** source-only
- **Current CordCode path:** none on iOS; Mac OpenCode Desktop/Link owns credentials
- **Target product behavior:** iOS must not collect provider API keys or complete OAuth against the serve. Auth remains Mac-side.
- **SSV2 ownership:** n/a
- **SSV2 domain:** none
- **WireDescriptor/capability future impact:** absent
- **Rationale:** Official Web can connect providers. CordCode product keeps provider credentials on the Mac. Advertising iOS provider-auth would be false.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

</details>

### observation

| id | disposition | Gate A | current CordCode path |
|---|---|---|---|
| `observation.status` | supported now | A3,A4,A5 | agent/opencode-web/events.go session.status/error/idle; activity.go IsSessionActive |
| `observation.global_events` | supported now | A1,A5 | agent/opencode-web/events.go Subscribe /global/event |
| `observation.direct_sse` | supported now | A1,A2,A3,A4,A5,A6,A7,A8,A10 | events.go unwraps payload and ignores sync |
| `observation.nested_sync` | not applicable | A1 | events.go ignores sync — matches official v1 skip, but C4 must pin this as the single p... |
| `observation.external_turns` | supported now | source-only | wire_descriptor.go LiveEventBroadcast, RequiresExternalTurnPolling=false, StaticCapabil... |
| `observation.reconnect` | supported now | A5 | events.go reconnect/subscribe; must be re-specified in C4 to Kernel validate/invalidate... |
| `observation.catalog_refresh` | supported now | A1,A10 | events.go session.created/deleted → signalCatalogRefresh |

<details>
<summary>Full fields</summary>

#### `observation.status`

- **Disposition:** supported now
- **Official UI:** server-compat.ts:175-181 session.status mapped to active(); event-reducer session.status
- **Server/schema/reducer:** GET /session/status; SSE session.status busy/retry/idle; session.idle; session.error
- **Gate A sample:** A3,A4,A5
- **Current CordCode path:** agent/opencode-web/events.go session.status/error/idle; activity.go IsSessionActive
- **Target product behavior:** busy vs retry vs idle vs error are distinct. Terminal is captured, never inferred. Retry is not idle.
- **SSV2 ownership:** OpenCode status. Kernel execution state from these facts only.
- **SSV2 domain:** live / reconnect
- **WireDescriptor/capability future impact:** C4 reducer must use captured ordering
- **Rationale:** A3 retry+APIError; A4 abort error; A5 busy-at-disconnect then idle on second SSE.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `observation.global_events`

- **Disposition:** supported now
- **Official UI:** packages/app/src/context/server-sdk.tsx kind===v1 → eventSdk.global.event()
- **Server/schema/reducer:** GET /global/event; instance encode in handlers/event.ts
- **Gate A sample:** A1,A5
- **Current CordCode path:** agent/opencode-web/events.go Subscribe /global/event
- **Target product behavior:** v1 Web uses global SSE. One upstream event → at most one Kernel ingest after pre-Kernel normalization.
- **SSV2 ownership:** OpenCode SSE. Mac EventPublisher/Kernel live ingest. Not iOS raw.
- **SSV2 domain:** live
- **WireDescriptor/capability future impact:** LiveEventBroadcast already declared; keep RequiresExternalTurnPolling false only if this path stays real
- **Rationale:** All Gate A scenarios used /global/event.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `observation.direct_sse`

- **Disposition:** supported now
- **Official UI:** server-sdk.tsx unwraps payload; skips sync then handles direct types
- **Server/schema/reducer:** SSE payload.type is the v1 event name
- **Gate A sample:** A1,A2,A3,A4,A5,A6,A7,A8,A10
- **Current CordCode path:** events.go unwraps payload and ignores sync
- **Target product behavior:** Direct payloads are the 1.18.18 valid ingest input. Nested sync is a separate surface that must not also ingest.
- **SSV2 ownership:** OpenCode direct payload → adapter normalize → Kernel.
- **SSV2 domain:** live
- **WireDescriptor/capability future impact:** C4 names the single pre-Kernel skip point
- **Rationale:** A1-A5 prove direct busy/delta/idle/error/retry. Official Web also uses direct after skipping sync.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `observation.nested_sync`

- **Disposition:** not applicable
- **Official UI:** server-sdk.tsx:284 if (legacy && event.payload.type === "sync") continue
- **Server/schema/reducer:** nested payload.type==sync still emitted on the wire
- **Gate A sample:** A1
- **Current CordCode path:** events.go ignores sync — matches official v1 skip, but C4 must pin this as the single pre-Kernel skip, not a consumer referee
- **Target product behavior:** Retain in evidence capture. Skip before canonical ingest. Never dual-ingest direct and nested sync for the same semantic event.
- **SSV2 ownership:** Not a CordCode truth. Skipping is adapter normalization.
- **SSV2 domain:** none (explicit skip)
- **WireDescriptor/capability future impact:** none; forbidding dual ingest is a C4/Gate S test, not a capability flag
- **Rationale:** Official v1 Web skips nested sync. Gate A kept frames as evidence only. Dual ingest is forbidden.
- **Dependency / evidence gap:** C4 + Gate S anti-double-ingest tests after this map.
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `observation.external_turns`

- **Disposition:** supported now
- **Official UI:** same global SSE; Web UI is another client on the same serve
- **Server/schema/reducer:** broadcast /global/event includes other clients' sessions
- **Gate A sample:** source-only
- **Current CordCode path:** wire_descriptor.go LiveEventBroadcast, RequiresExternalTurnPolling=false, StaticCapabilities external_turn_streaming; events.go server-level SSE
- **Target product behavior:** External official Web turns stream through the same Kernel live path. No second iOS writer. No polling substitute while SSE is bound.
- **SSV2 ownership:** OpenCode SSE. Kernel live. ProjectionStore apply.
- **SSV2 domain:** live
- **WireDescriptor/capability future impact:** external_turn_streaming already declared; C4 must keep it honest (SSE actually delivers other-session turns into Kernel, not iOS raw)
- **Rationale:** Transport is the same global SSE proven in A1/A5. A dedicated two-client fixture was not in A1-A10; the broadcast property is source-backed and already the WireDescriptor claim. First C slice still uses this path rather than polling.
- **Dependency / evidence gap:** A two-client external-turn fixture is recommended in C4 but not required to classify the surface; owner matrix row 5 is later acceptance. supported now + source-only is scope only. Gate S C-slice impact/test map must register 实现前补样本 before writing translators.
- **Owner decision:** —
- **Gate S pre-sample gate:** 实现前补样本

#### `observation.reconnect`

- **Disposition:** supported now
- **Official UI:** server-sdk.tsx:268-308 reconnect loop; still skips sync
- **Server/schema/reducer:** GET /global/event; first frame server.connected; no assumed replay buffer
- **Gate A sample:** A5
- **Current CordCode path:** events.go reconnect/subscribe; must be re-specified in C4 to Kernel validate/invalidate/rehydrate
- **Target product behavior:** Reconnect may read server messages/status then rehydrate the same Kernel. No history merge, raw catch-up writer, or inferred healthy terminal. A5: live deltas after server.connected; terminal idle on second SSE.
- **SSV2 ownership:** OpenCode facts. Same Kernel. ProjectionStore resumes via checkpoint/fence.
- **SSV2 domain:** reconnect
- **WireDescriptor/capability future impact:** C4 reconnect tests; no extra flag
- **Rationale:** A5 captured disconnect during busy+partial, status still busy, reconnect first direct server.connected then live deltas, terminal idle, full text, finish=stop.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

#### `observation.catalog_refresh`

- **Disposition:** supported now
- **Official UI:** event-reducer project.updated / session.created/deleted refresh home
- **Server/schema/reducer:** SSE session.created, session.deleted, project.updated, catalog.updated
- **Gate A sample:** A1,A10
- **Current CordCode path:** events.go session.created/deleted → signalCatalogRefresh
- **Target product behavior:** Catalog invalidation is control-plane. It must not write messages[].
- **SSV2 ownership:** OpenCode catalog events. iOS catalog via existing catalog_invalidation, not ProjectionStore timeline.
- **SSV2 domain:** request/control
- **WireDescriptor/capability future impact:** existing CatalogRefreshSignaler
- **Rationale:** Current path already treats created/deleted as catalog signals. Keep that boundary.
- **Dependency / evidence gap:** —
- **Owner decision:** —
- **Gate S pre-sample gate:** —

</details>

## What this map does not do

- It does not change `agent/opencode-web/wire_descriptor.go`.
- It does not add `todos`, `structured_user_input_v1`, or attachment kinds to hello capabilities.
- It does not implement C1–C7.
- It does not start Gate S. SSV2 columns and the 实现前补样本 gate are inputs for a later Gate S session after independent audit.
