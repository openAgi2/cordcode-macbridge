# OpenCode Web Gate S S1/S2 SSV2 impact

- Date: 2026-08-20
- OpenCode: 1.18.18 `2cba7e227d`
- Gate A: `aad4b24` · Gate B: `883513b`
- **S3/S4/Gate C: not started.** Product code, protocol models, WireDescriptor, and capability advertisement: **frozen.**
- Machine-readable companion: `docs/2026-08-20-opencode-web-ssv2-impact.json`
- Checker: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_s_s1_s2.py`

## Authorities read

- `../cordcode-ios/CLAUDE.md Session Sync v2 架构路线护栏`
- `../cordcode-ios/docs/2026-07-24-single-source-multidevice-sync-design.md`
- `../cordcode-ios/docs/2026-07-26-session-sync-v2-cold-start-kernel-restart-plan.md`
- `docs/protocol/README.md`
- `docs/protocol/bridge-v1.md`
- `GO_BRIDGE_ARCHITECTURE.md`
- `docs/2026-08-20-opencode-web-gate-b-capability-map.json`
- `docs/2026-08-20-opencode-web-source-first-convergence-plan.md §5.1 S1/S2`

This document records **target architecture** vs **current code**. A satisfied-as-architecture row is not Gate C parity and is not an implementation grant. `supported now` + `source-only` remains **实现前补样本**.

## S1 — truth owners and only writers

| id | concern | only writer | current status |
|---|---|---|---|
| `s1.opencode-facts` | OpenCode session/message/status facts | opencode serve HTTP/SSE (GET/POST /session, /session/:id/message, /session/statu | satisfied-as-owner |
| `s1.kernel-timeline` | CordCode active timeline and execution | go-bridge ProjectionKernel → ProjectionReducer.Apply (hydrate transaction or Ing | satisfied-as-architecture |
| `s1.projectionstore` | iOS messages[], turn state, generation under negotiated session_sync_v2 | OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift applyFrame → ProjectionRepl | satisfied-as-architecture |
| `s1.protocol` | protocol and projection shapes | docs/protocol/bridge-v1.md + go-bridge/bridge_v1_schema.go (canonical); iOS Open | satisfied-as-architecture |

### S1 details

#### `s1.opencode-facts`

- **Concern:** OpenCode session/message/status facts
- **Target architecture:** OpenCode 1.18.18 serve is the upstream fact owner for session/message/status. The adapter may observe and translate; local guesses, legacy adapter state, and iOS state are not OpenCode server truth.
- **Only writer (target):** opencode serve HTTP/SSE (GET/POST /session, /session/:id/message, /session/status, GET /global/event)
- **Current source:** `agent/opencode-web/session.go Send/ensureServerSession; agent/opencode-web/history.go GetRichSessionHistory; agent/opencode-web/events.go handleRawEvent/handleServerEvent; agent/opencode-web/session_mutation.go FetchSessionInfo/ArchiveSession/DeleteSession; agent/opencode-web/activity.go IsSessionActive`
- **Current status:** satisfied-as-owner
- **Gap:** Translation completeness is Gate C (prompt fields, parts, list roots/limit). Ownership of facts is already the official serve, not a second store.

#### `s1.kernel-timeline`

- **Concern:** CordCode active timeline and execution
- **Target architecture:** Exactly one Mac ProjectionKernel / SessionProjection per (backendId, sessionId). Push and pull read the same committed Kernel head. OpenCode history or raw SSE must not become a second reducer.
- **Only writer (target):** go-bridge ProjectionKernel → ProjectionReducer.Apply (hydrate transaction or IngestLive)
- **Current source:** `go-bridge/projection_kernel.go BeginHydrateTransaction/ApplyHydrateEvent/IngestLive/CommitHydrateTransaction/Snapshot; go-bridge/projection_types.go SessionProjection; go-bridge/event_publisher.go publish → kernel.IngestLive; go-bridge/server.go advertiseSessionSyncV2Backend includes opencode-web`
- **Current status:** satisfied-as-architecture
- **Gap:** EventPublisher.publish still has a kernel==nil fallback to p.projection.Apply (go-bridge/event_publisher.go). Production installs a Kernel; that fallback is a sealed second-reducer path and must stay unused. C4 must keep the single ingest.

#### `s1.projectionstore`

- **Concern:** iOS messages[], turn state, generation under negotiated session_sync_v2
- **Target architecture:** ProjectionStore is the only client-side projection writer from t=0. Ownership is mode + selected backend session_sync_v2, not projection arrival. loading/empty/failed must not re-enable legacy writers.
- **Only writer (target):** OpenCodeiOS/OpenCodeiOS/Models/ProjectionStore.swift applyFrame → ProjectionReplica.applyPatch/snapshot → commitIfObserved
- **Current source:** `ProjectionStore.swift applyFrame/commitIfObserved/lifecycle; ChatViewModel+MessageSync.swift loadMessages/replaceMessagesFromServer guards on sessionSyncV2Active; ArchitectureGuardrailTests.swift seals pump/applyFrame; go-bridge/server.go advertiseSessionSyncV2Backend for opencode-web`
- **Current status:** satisfied-as-architecture
- **Gap:** loadMessages comment still says “codex+v2” in one place; the live gate is sessionSyncV2Active which follows the selected backend descriptor, including opencode-web. No code change this round.

#### `s1.protocol`

- **Concern:** protocol and projection shapes
- **Target architecture:** MacBridge docs/protocol is the only canonical pack. iOS docs/protocol, Swift models, and web types are synchronized consumers.
- **Only writer (target):** docs/protocol/bridge-v1.md + go-bridge/bridge_v1_schema.go (canonical); iOS OpenCodeiOS/.../BridgeModels.swift is consumer
- **Current source:** `docs/protocol/README.md; docs/protocol/bridge-v1.md Session Projection Stream; go-bridge/bridge_v1_schema.go; go-bridge/projection_types.go; iOS docs/protocol is a mirror per iOS CLAUDE.md`
- **Current status:** satisfied-as-architecture
- **Gap:** C-gates that change projection/capability must still land canonical-first. This S1/S2 round does not change protocol.

S1 invariants (must remain true): OpenCode 1.18.18 serve owns session/message/status facts; one Mac ProjectionKernel/SessionProjection per `(backendId, sessionId)` is CordCode timeline truth; negotiated `session_sync_v2` makes iOS ProjectionStore the only writer of `messages[]`, turn state, and generation from t=0; MacBridge `docs/protocol/` is the only protocol authority.

## S2 — transaction domains

| id | name | status | only writer |
|---|---|---|---|
| `s2.hydrate` | cold hydrate/reopen | satisfied-as-architecture | Kernel private hydrate transaction |
| `s2.live-direct-sse` | live direct SSE | gap | Kernel IngestLive for timeline; ProjectionStore for iOS messages[] |
| `s2.nested-sync` | v1 nested sync | gap | none |
| `s2.reconnect` | reconnect/recovery | gap | same Kernel + ProjectionStore apply |
| `s2.request-mutation-catalog` | prompt/abort/rename/archive/delete/list/model requests | pending-C | Kernel for timeline; catalog APIs for session list metadata |
| `s2.permission` | permission | pending-C | Kernel for canonical permission state; ProjectionStore for iOS timelin |
| `s2.question` | structured question | pending-C | Kernel for canonical question state |
| `s2.todo` | todo | pending-C | none on timeline; control-plane publisher if C6 advertises todos |
| `s2.projection-delivery` | projection delivery | satisfied-as-architecture | Kernel head is the only payload; connections do not reduce a second co |
| `s2.ios-projection-apply` | iOS projection apply | satisfied-as-architecture | ProjectionStore |

### S2 details

#### `s2.hydrate` — cold hydrate/reopen

- **Producer:** Official GET /session/:id/message via agent/opencode-web/history.go GetRichSessionHistory
- **Mapper / normalization:** go-bridge/handlers_projection.go streamBackendRichHistoryProjectionEvents (opencode-web case, not the legacy opencode helper)
- **Kernel or control-plane entry:** ProjectionKernel.BeginHydrateTransaction → ApplyHydrateEvent on a private reducer → CommitHydrateTransaction
- **Consumer:** Handlers.handleGetSessionProjection returns Kernel Snapshot; iOS ProjectionStore applies snapshot
- **Only writer:** Kernel private hydrate transaction
- **Forbidden bypass:** Must not enter ordinary live seq, EventBuffer, live buffer, offline queue, mailbox, or raw client fanout. Must not wrap get_session_messages for the client to merge.
- **Current source:** `go-bridge/handlers_projection.go handleGetSessionProjection, streamBackendRichHistoryProjectionEvents case opencode-web; go-bridge/projection_kernel.go BeginHydrateTransaction/ApplyHydrateEvent/CommitHydrateTransaction; agent/opencode-web/history.go GetRichSessionHistory`
- **Status:** satisfied-as-architecture
- **Required path:** verified OpenCode messages → pathless rich-history mapper → Kernel private hydrate under one source cut → committed projection
- **Forbidden path:** HTTP history directly into iOS messages[]; PublishLogical as cold hydrate; concurrent file-scan and live publisher mutating the committed reducer
- **Failure presentation:** handleGetSessionProjection returns honest hydrating/failed errors (projection.hydrating / timeout). iOS ProjectionStore lifecycle stays loading/failed. No inferred empty-success, no history fallback.
- **Future C slice:** C2/C4 (list/get semantics and event mapping of history parts)

#### `s2.live-direct-sse` — live direct SSE

- **Producer:** Official GET /global/event direct payload.type (message.part.delta, session.status, session.error, permission.asked, …)
- **Mapper / normalization:** agent/opencode-web/events.go handleRawEvent unwraps payload; handleServerEvent maps direct types to core.Event; go-bridge/events.go mapAgentEvent to wire names
- **Kernel or control-plane entry:** EventPublisher.publish → ProjectionKernel.IngestLive
- **Consumer:** SSV2 connections receive projection_patch/snapshot only (shouldDeliverRawEventLocked seals raw timeline). ProjectionStore.applyFrame.
- **Only writer:** Kernel IngestLive for timeline; ProjectionStore for iOS messages[]
- **Forbidden bypass:** Raw OpenCode SSE or raw bridge text_delta must not write iOS messages[]. One upstream event → at most one Kernel ingest.
- **Current source:** `agent/opencode-web/events.go handleRawEvent/handleServerEvent; go-bridge/events.go mapAgentEvent; go-bridge/event_publisher.go publish/IngestLive/shouldDeliverRawEventLocked`
- **Status:** gap
- **Required path:** verified v1 direct SSE → opencode-web source adapter normalization → EventPublisher/Kernel live ingest → projection patch → ProjectionStore
- **Forbidden path:** direct raw timeline delivery to an SSV2 client; dual ingest with nested sync; inferred terminal from HTTP 204
- **Failure presentation:** Provider/session.error maps to terminal error content + idle; abort maps to MessageAbortedError. Missing terminal stays running/failed, never a timeout-completed turn. HTTP 2xx on prompt_async is admission only.
- **Future C slice:** C4 (pin direct-vs-sync skip as the single pre-Kernel point; replay A1–A5)

#### `s2.nested-sync` — v1 nested sync

- **Producer:** Official /global/event frames with payload.type==sync (still on the wire)
- **Mapper / normalization:** Intended: one pre-Kernel normalization skip, matching official Web server-sdk.tsx:284
- **Kernel or control-plane entry:** none — skipped before canonical ingest
- **Consumer:** evidence capture only (Gate A samples retain sync frames)
- **Only writer:** none
- **Forbidden bypass:** Direct and nested forms must never both advance syncRev for the same semantic event.
- **Current source:** `agent/opencode-web/events.go handleServerEvent default + isServerEventType (sync is not a handled type, so it is dropped). Gate A A1 retains nested sync as evidence.`
- **Status:** gap
- **Required path:** preserve in capture; skip before Kernel ingest at one named pre-Kernel point
- **Forbidden path:** dual ingest of direct payload and nested sync; consumer-side referee; recursive JSON search for nested events
- **Failure presentation:** Unknown/unhandled SSE types log debug and drop. They must not be treated as success or as a second event stream.
- **Future C slice:** C4 (replace implicit default-drop with an explicit exclusive skip at the adapter boundary)

#### `s2.reconnect` — reconnect/recovery

- **Producer:** Second GET /global/event (A5: first direct frame server.connected then live deltas); GET /session/:id/message and /session/status may supply hydrate facts
- **Mapper / normalization:** Same adapter as live SSE; Kernel validate/invalidate/rehydrate
- **Kernel or control-plane entry:** ProjectionKernel RestoreCheckpoint / BeginHydrateTransaction / IngestLive on the same (backendId, sessionId) Kernel
- **Consumer:** ProjectionStore applyFrame snapshot/patch; gap → get_session_projection pull
- **Only writer:** same Kernel + ProjectionStore apply
- **Forbidden bypass:** No iOS history merge, content-similarity dedup, raw catch-up writer, or local completion guess.
- **Current source:** `agent/opencode-web/events.go SSE reconnect/subscribe; go-bridge/projection_kernel.go RestoreCheckpoint/BeginHydrateTransaction; iOS ProjectionStore.swift scheduleRecoveryPull/applyFrame; ChatViewModel+MessageSync.swift loadMessages HARD-GATE when sessionSyncV2Active`
- **Status:** gap
- **Required path:** reconnect observation may read server messages/status, then validate/invalidate/rehydrate the same Kernel, resume via checkpoint/fence/full-or-delta projection
- **Forbidden path:** history merge on iOS; inferred healthy terminal; treating session.created as replay success (A5)
- **Failure presentation:** Mismatch/gap → invalidated + pull. Failed pull stays failed/loading. Never synthesize finish=stop from empty reload status.
- **Future C slice:** C4 (replay A5: busy-at-disconnect, live delta after server.connected, terminal idle on second SSE)

#### `s2.request-mutation-catalog` — prompt/abort/rename/archive/delete/list/model requests

- **Producer:** iOS RPC → go-bridge handlers → agent/opencode-web HTTP (prompt_async, abort, PATCH, DELETE, GET /session, GET /provider)
- **Mapper / normalization:** request/control path only; timeline effects re-enter through observation/Kernel
- **Kernel or control-plane entry:** HTTP 2xx may refresh catalog/metadata (signalCatalogRefresh). Prompt/abort do not write SessionProjection themselves.
- **Consumer:** catalog invalidation; subsequent SSE/hydrate is the timeline path
- **Only writer:** Kernel for timeline; catalog APIs for session list metadata
- **Forbidden bypass:** HTTP 2xx must not manufacture a confirmed turn, assistant completion, or timeline content.
- **Current source:** `agent/opencode-web/session.go Send (returns on 204); go-bridge/handlers.go send/abort; session_mutation.go ArchiveSession/DeleteSession; sessions.go ListSessions; models.go AvailableModels; events.go session.created/deleted → signalCatalogRefresh`
- **Status:** pending-C
- **Required path:** prompt/abort/rename/archive/delete/list/model remain control/request; resulting timeline effects re-enter observation/Kernel
- **Forbidden path:** treating prompt_async 204 as turn_completed; list HTTP as messages[]; filesystem existence as session truth
- **Failure presentation:** HTTP errors surface as send/abort/mutation failures. Catalog refresh is not a fake session. Missing SessionRenamer on opencode-web is absence, not a silent no-op success.
- **Future C slice:** C2 list/get; C3 prompt fields; C5 models; C7 rename (source-only sample gate)

#### `s2.permission` — permission

- **Producer:** Official permission.asked SSE + GET /permission; reply POST /session/:id/permissions/:id {response:once|always|reject}
- **Mapper / normalization:** events.go handlePermissionAsked → core.EventPermissionRequest; go-bridge/events.go mapAgentEvent → permission_request; permissions.go RespondSessionPermission folds allow→once, always→always, deny→reject
- **Kernel or control-plane entry:** EventPublisher IngestLive reduces canonical permission_request/permission_resolved. Raw control presentation is allowed but must not write messages[].
- **Consumer:** SSV2: projected permission part. Legacy: permission_request event sealed from SSV2 raw fanout.
- **Only writer:** Kernel for canonical permission state; ProjectionStore for iOS timeline card; not ChatViewModel messages[] from raw
- **Forbidden bypass:** permission raw control must not write messages[] or execution. Do not treat reject as healthy stop.
- **Current source:** `agent/opencode-web/events.go handlePermissionAsked; agent/opencode-web/permissions.go RespondSessionPermission; go-bridge/events.go EventPermissionRequest; go-bridge/projection_reducer_permission_test.go`
- **Status:** pending-C
- **Required path:** permission_request/resolved through Kernel; raw control presentation without messages[] writes; A6 once/always/reject
- **Forbidden path:** raw permission frames writing messages[]; inferring always as restart-surviving grant; reject → finish=stop
- **Failure presentation:** A6 reject: finish=tool-calls, idle, pending cleared. Reply HTTP errors stay pending/failed. No inferred completion.
- **Future C slice:** C6

#### `s2.question` — structured question

- **Producer:** Official question.asked / GET /question; POST /question/:id/reply {answers:string[][]} or /reject
- **Mapper / normalization:** Target: core user_input_requested and user_input_resolved only. go-bridge/events.go already maps EventUserInputRequested and treats EventQuestionAsked as derived-legacy (EventPublisher must not ingest question_asked).
- **Kernel or control-plane entry:** Kernel user_input_requested/resolved. Legacy question_asked/question_resolved are one-way presentation and must not re-enter Kernel.
- **Consumer:** SSV2: user_input part via ProjectionStore. Must not deliver raw question_* to an SSV2 client.
- **Only writer:** Kernel for canonical question state
- **Forbidden bypass:** Do not invent question_resolved as an official OpenCode event. Do not map permission to question. Do not ingest legacy question frames back into Kernel.
- **Current source:** `go-bridge/events.go EventQuestionAsked/EventUserInputRequested; go-bridge/event_publisher.go isDerivedLegacyQuestionEvent skip IngestLive; agent/opencode-web has no question SSE handler yet (C6)`
- **Status:** pending-C
- **Required path:** official asked/replied/rejected → canonical user_input_requested and user_input_resolved into Kernel
- **Forbidden path:** question_resolved as official OpenCode event; permission-as-question; legacy question raw to SSV2 client; Kernel ingest of question_asked
- **Failure presentation:** A7 reject: idle, pending cleared, not healthy stop. Missing question path is unsupported/absent, not a fake resolve.
- **Future C slice:** C6

#### `s2.todo` — todo

- **Producer:** Official todo.updated SSE + GET /session/:id/todo items {content,status,priority} no id
- **Mapper / normalization:** Control-plane only. events.go currently ignores todo.updated (phase 1). Target: EventPlan/todos_updated control-plane, not timeline parts.
- **Kernel or control-plane entry:** Explicit control-plane exception. Must not enter SessionProjection timeline.
- **Consumer:** iOS todo dock / control-plane events if advertised. Not messages[].
- **Only writer:** none on timeline; control-plane publisher if C6 advertises todos
- **Forbidden bypass:** Do not invent hash/content/position ids. Do not smuggle todos into timeline parts.
- **Current source:** `agent/opencode-web/events.go todo.updated comment-ignored; go-bridge/events.go EventPlan → todos_updated; WireDescriptor does not advertise todos`
- **Status:** pending-C
- **Required path:** todo.updated + GET /todo as control-plane; A8 replacement semantics; UnifiedTodo has no id
- **Forbidden path:** todo events in SessionProjection timeline; synthetic ids; treating ignore-as-implemented
- **Failure presentation:** Until C6, todos are absent from advertisement. Absence is honest, not an empty completed plan.
- **Future C slice:** C6

#### `s2.projection-delivery` — projection delivery

- **Producer:** ProjectionKernel Snapshot / FlushProjectionPatch after hydrate or live ingest
- **Mapper / normalization:** EventPublisher per-connection dispatch of projection_snapshot / projection_patch / sync_invalidate (same envelope as other events; no parallel websocket)
- **Kernel or control-plane entry:** EventPublisher.publish with projection frames; shouldDeliverRawEventLocked omits raw timeline to SSV2 connections
- **Consumer:** iOS CCCodeBridgeTransport routes frames to ProjectionStore.applyFrame
- **Only writer:** Kernel head is the only payload; connections do not reduce a second copy
- **Forbidden bypass:** No parallel projection SSE. Raw timeline-semantic events must not fan out to session_sync_v2 connections.
- **Current source:** `go-bridge/event_publisher.go publish/shouldDeliverRawEventLocked; go-bridge/projection_kernel.go Snapshot/FlushProjectionPatch; docs/protocol/bridge-v1.md Session Projection Stream; iOS CCCodeBridgeTransport.swift applyFrame routing`
- **Status:** satisfied-as-architecture
- **Required path:** one Kernel head → EventPublisher projection frames → SSV2 client applyFrame
- **Forbidden path:** raw text_delta to SSV2; second projection pipe; mailbox raw as active timeline
- **Failure presentation:** Delivery failure stays loading/invalidated and pulls get_session_projection. No automatic legacy raw resume.
- **Future C slice:** C4 reconnect/delivery tests

#### `s2.ios-projection-apply` — iOS projection apply

- **Producer:** projection_patch / projection_snapshot / get_session_projection result
- **Mapper / normalization:** ProjectionStore.applyFrame (replica applyPatch/snapshot off MainActor; MainActor commitIfObserved)
- **Kernel or control-plane entry:** n/a — client apply only. Ownership chosen at t=0 from sessionSyncV2Active (mode + backend session_sync_v2).
- **Consumer:** ChatViewModel observes ProjectionStore; SessionProjectionMapping maps to UI. messages[] updated only via this store when v2 is active.
- **Only writer:** ProjectionStore
- **Forbidden bypass:** loading/empty/failed/invalidated must not re-enable loadMessages, replaceMessagesFromServer, history merge, delayed raw flush, or generation timeout completion.
- **Current source:** `OpenCodeiOS/Models/ProjectionStore.swift applyFrame; ChatViewModel+MessageSync.swift sessionSyncV2Active HARD-GATE; ChatViewModel+Generation.swift sessionSyncV2Active seals; ArchitectureGuardrailTests.swift`
- **Status:** satisfied-as-architecture
- **Required path:** only ProjectionStore applies full/delta/push; baseRev→syncRev; fence; old-generation rejection
- **Forbidden path:** history merge; content similarity; timeout completion; projection failure → get_session_messages fallback; empty projection waits for history
- **Failure presentation:** lifecycle failed/invalidated/loading remains visible. Honest empty ready is empty, not a history fill. .off is not a runtime fallback.
- **Future C slice:** none this round; C-gates must not weaken seals

## Permission / question / todo ownership

- **Permission:** raw control may present; canonical state is Kernel `permission_request` / `permission_resolved`; raw must not write `messages[]`. Reject is not a healthy completion.
- **Question:** canonical `user_input_requested` / `user_input_resolved` into Kernel. Official OpenCode events are asked/replied/rejected. Legacy `question_asked` / `question_resolved` are one-way presentation and must not re-enter Kernel. Do not invent `question_resolved` as an official OpenCode event.
- **Todo:** control-plane only; items `{content,status,priority}` with no id; must not enter SessionProjection timeline.

## Direct vs nested sync

Direct SSE is the 1.18.18 ingest. Nested `sync` is skipped at the adapter before Kernel ingest. Dual ingest is forbidden. Current skip is implicit default-drop in `handleServerEvent`; C4 must name the exclusive pre-Kernel skip.

Hydrate, live, and reconnect are separate Kernel domains: private hydrate transaction; IngestLive; same-Kernel validate/invalidate/rehydrate. They are not three writers.

## Source-only supported-now pre-sample gates

| id | C slice | gate |
|---|---|---|
| `sessions.rename` | C7 | 实现前补样本 |
| `sessions.delete` | C7 | 实现前补样本 |
| `content.reasoning` | C4 | 实现前补样本 |
| `workspace.project` | C2 | 实现前补样本 |
| `configuration.providers` | C5 | 实现前补样本 |
| `configuration.default_model` | C5 | 实现前补样本 |
| `observation.external_turns` | C4 | 实现前补样本 |

S1/S2 only registers these gates. This round does not sample and does not implement. Source citations do not prove parity.

## Active-writer inventory

| path | writes | allowed | domain |
|---|---|---|---|
| `go-bridge/projection_kernel.go ApplyHydrateEvent` | private hydrate reducer only | allowed | `s2.hydrate` |
| `go-bridge/projection_kernel.go IngestLive` | committed ProjectionReducer (or queues during hydrate) | allowed | `s2.live-direct-sse` |
| `go-bridge/projection_kernel.go CommitHydrateTransaction` | atomic baseline publish then pending live | allowed | `s2.hydrate` |
| `go-bridge/event_publisher.go publish → kernel.IngestLive` | Kernel live ingest under publisher lock | allowed | `s2.live-direct-sse` |
| `go-bridge/event_publisher.go publish → p.projection.Apply when kernel==nil` | direct ProjectionReducer without Kernel | sealed/forbidden | `s2.live-direct-sse` |
| `go-bridge/handlers_projection.go streamBackendRichHistoryProjectionEvents` | ApplyHydrateEvent only | allowed | `s2.hydrate` |
| `agent/opencode-web/session.go Send HTTP 204 return` | none on timeline | allowed | `s2.request-mutation-catalog` |
| `iOS ProjectionStore.swift applyFrame` | iOS SessionProjection mirror / messages[] via mapping | allowed | `s2.ios-projection-apply` |
| `iOS ChatViewModel+MessageSync.swift loadMessages / replaceMessagesFromServer` | messages[] from get_session_messages | sealed/forbidden | `s2.ios-projection-apply` |
| `iOS ChatViewModel+Generation.swift timeout/completion helpers` | generation/turn completion | sealed/forbidden | `s2.ios-projection-apply` |

Notes on sealed writers:
- `go-bridge/event_publisher.go publish → p.projection.Apply when kernel==nil`: Sealed second-reducer fallback. Must remain unused in production (Kernel is installed).
- `agent/opencode-web/session.go Send HTTP 204 return`: Admission only. Timeline comes from later SSE/Kernel.
- `iOS ChatViewModel+MessageSync.swift loadMessages / replaceMessagesFromServer`: Hard-gated when sessionSyncV2Active. Must stay sealed for opencode-web v2. Not deleted; listed as a sealed legacy writer.
- `iOS ChatViewModel+Generation.swift timeout/completion helpers`: Sealed by sessionSyncV2Active. Forbidden as inferred success.

## Current architecture gaps

- Nested sync skip is implicit default-drop in handleServerEvent, not a named exclusive pre-Kernel skip (C4).
- EventPublisher kernel==nil reducer.Apply is a second-reducer path that must stay unused (C4).
- opencode-web Send omits official messageID/agent/variant/file/agent parts (C3).
- ListSessions lacks official roots/limit (C2).
- todos ignored; not yet control-plane advertised (C6).
- question SSE/HTTP not implemented in opencode-web (C6).
- permission ToolAuthorizer exists but permission_resolve is not advertised; C6 must complete once/always/reject without forging healthy reject completion.
- Seven Gate B source-only supported-now surfaces require 实现前补样本 before translators (rename, delete, reasoning, project, providers, default_model, external_turns).

## Forbidden as allowed paths

The following must never be listed as an allowed path: raw/history fallback, a second timeline writer, inferred success, timeout completion, recursive JSON search, restoring the old opencode adapter, or re-enabling legacy writers from ProjectionStore loading/empty/failed.

HTTP 2xx does not manufacture a confirmed turn. Raw OpenCode history/SSE does not write the SSV2 iOS timeline.
