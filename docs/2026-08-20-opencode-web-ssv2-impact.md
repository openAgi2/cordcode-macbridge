# OpenCode Web Gate S S1/S2/S3/S4 SSV2 impact

- Date: 2026-08-20
- OpenCode: 1.18.18 `2cba7e227d`
- Gate A: `aad4b24` · Gate B: `883513b` · S3 audit + S4 授权: `c8d732d`
- **S1–S4 documented. Gate S exit pending independent audit. Gate C: not started.** Product code, protocol models, WireDescriptor, and capability advertisement: **frozen.** `s4Started=true`, `s4Completed=true`, `gateSExited=false`, `gateCStarted=false`, `productCodeFrozen=true`.
- Machine-readable companion: `docs/2026-08-20-opencode-web-ssv2-impact.json`
- Checkers:
  - S1/S2: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_s_s1_s2.py`
  - S3: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_s_s3.py`
  - S4: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_s_s4.py`

## Authorities read

- `../cordcode-ios/CLAUDE.md Session Sync v2 架构路线护栏`
- `../cordcode-ios/docs/2026-07-24-single-source-multidevice-sync-design.md`
- `../cordcode-ios/docs/2026-07-26-session-sync-v2-cold-start-kernel-restart-plan.md`
- `docs/protocol/README.md`
- `docs/protocol/bridge-v1.md`
- `GO_BRIDGE_ARCHITECTURE.md`
- `docs/2026-08-20-opencode-web-gate-b-capability-map.json`
- `docs/2026-08-20-opencode-web-source-first-convergence-plan.md §5.1 S1/S2/S3`

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

## S3 — C1–C7 impact records

S3 is documentation only. `gateCStarted=false`. `productCodeFrozen=true`. Each slice records target path vs current code; a satisfied-as-architecture row is not Gate C implementation. (S4 has since been documented; see the S4 section.)

| id | name | protocol | 实现前补样本 |
|---|---|---|---|
| C1 | version/transport | no protocol change | none |
| C2 | list/get | no protocol change | `workspace.project` |
| C3 | submit | no protocol change (adapter-generated messageID; send-RPC field would be canonical-first) | selected variant (not omit-when-unset) |
| C4 | event/reconnect | no protocol change | `content.reasoning`, `observation.external_turns` |
| C5 | model/agent | no protocol change | `configuration.providers`, `configuration.default_model` |
| C6 | interaction | no protocol change | none of the seven; advertise only after full path |
| C7 | mutation/secondary | no protocol change | `sessions.rename`, `sessions.delete` |

### C1 — version/transport

- **truth owner:** OpenCode 1.18.18 serve is the only verified generation fact owner for health/session-shape/SSE transport. CordCode timeline remains the Mac ProjectionKernel; transport never writes `messages[]`.
- **only writer:** none on timeline. `probeInstance`/`Client` are control-plane. Kernel `IngestLive`/hydrate and iOS `ProjectionStore.applyFrame` stay the only timeline writers (C4/S1).
- **transaction domain:** request/control (transport/auth/timeouts). Not hydrate, not live ingest, not reconnect Kernel recovery.
- **new data path:** `GET /global/health` (authed Basic Auth) plus `GET /session` bare-array shape → `probe.go probeInstance` → `Client.gen` must be `generation118`. SSE uses `client.go streamClient` with no body timeout and `events.go Subscribe` bounded reconnect. Directory scope remains a request header. Unverified v2 is quarantined and does not enter Kernel.
- **active write inventory:** `probe.go probeInstance/probeHealth`: no timeline write, allowed control. `client.go doRequest/streamClient`: transport only. `events.go Subscribe` reconnect: transport only, must not manufacture turns. `session.go generationV2 POST /prompt` and `/interrupt`: sealed/quarantine. Recursive JSON search: forbidden.
- **failure presentation:** Unsupported/unverified generation (`generationV2` or unknown) fails closed with diagnosable unsupported-generation / quarantine; zero Kernel ingest. 401 unauthorized. Unreachable `healthFailed`. No-auth 200 is `server_unauthenticated`. Missing health is not inferred 1.18 success. No legacy parser fallback.
- **anti-double-write proof:** Existing `probe_test.go`. Planned: `TestGenerationV2QuarantineZeroPromptAndZeroKernelIngest`; `TestSSEReconnectIsTransportOnly`. Health 200 is not a confirmed turn.
- **实现前补样本:** none — C1 has no Gate B supported-now source-only surface.
- **Gate B surfaces:** `observation.global_events`, `turns.prompt` (v1 not applicable; v2 fail closed).
- **Gate A samples:** A1, A5.
- **Current paths:** `probe.go probeHealth/probeInstance`; `client.go generation118/generationV2`, Basic Auth, `streamClient` no Timeout; `events.go Subscribe` bounded reconnect; `session.go` still contains `generationV2 POST /prompt` (gap to quarantine).
- **Planned add:** explicit verified-generation gate, 1.18.18 only.
- **Planned modify:** `probeInstance` fail-closed/quarantine on v2 for the product adapter.
- **Planned seal:** v2 prompt/interrupt; unknown-shape recursive search.
- **Planned delete:** unverified v2 as a normal selection; do not restore legacy parsers.
- **canonical protocol:** no protocol change. Cite `docs/protocol/bridge-v1.md` hello_ack backends[] and `GO_BRIDGE_ARCHITECTURE.md` OpenCode server probe.
- **Planned tests Mac:** existing `probe_test.go`; planned `TestVerified118Only`, `TestV2FailClosedQuarantine`.
- **Planned tests iOS:** none for transport; `ArchitectureGuardrailTests.swift` remains sealed.
- **Out of scope:** C4 Kernel ingest/reconnect; C5 catalog; v2 parity pack; capability advertisement.

1.18.18 is the only verified generation. Unverified v2 must fail closed/quarantine. No recursive JSON search, legacy parser fallback, or unknown-shape guessing. Basic Auth, directory scope, timeouts, and bounded SSE reconnect remain transport/control and do not write timeline.

### C2 — list/get

- **truth owner:** OpenCode serve is the catalog fact owner. Mac Kernel is not a catalog store. iOS ProjectionStore does not write the session list from raw HTTP.
- **only writer:** catalog metadata via `ListSessions`/`FetchSessionInfo`/`signalCatalogRefresh`. Timeline `messages[]` remain Kernel + ProjectionStore. list/get must not manufacture turns.
- **transaction domain:** request/control (catalog). Timeline effects none.
- **new data path:** official directory-scoped `GET /session` with roots/limit → `sessions.go ListSessions` / `ListSessionsInDirectory`. OD-1: default home/global list hides `time.archived`; GET-by-id keeps archived. OD-2: iOS global list aggregates one official scoped GET per project worktree; directory requests stay scoped. `GET /session/:id` refreshes metadata only.
- **active write inventory:** `ListSessions`/`ListSessionsInDirectory`: catalog, allowed. `FetchSessionInfo`: metadata, allowed. `ensureServerSession POST /session {}`: session id fact, allowed control. `session.created/deleted → signalCatalogRefresh`: catalog invalidation. HTTP list/get writing `messages[]`: forbidden. filesystem existence as session truth: forbidden.
- **failure presentation:** list/get HTTP errors are catalog failures. Missing worktree is a documented product rule, not a silent filesystem substitute. Archived hidden is not deleted. Empty catalog is empty. No history fallback into `messages[]`.
- **anti-double-write proof:** existing `sessions_test.go`, `session_mutation_test.go`. Planned: `TestListRootsLimitArchiveHideByIdKeep`; `TestGlobalAggregatePerWorktree`; `TestListGetDoesNotWriteMessages`.
- **实现前补样本:** `workspace.project` is supported now + source-only: 实现前补样本 before any project-registry translator/parity claim.
- **Gate B surfaces:** `sessions.list`, `sessions.get`, `sessions.create`, `workspace.project`.
- **Gate A samples:** A1, A10.
- **Current paths:** `sessions.go ListSessions` / `ListSessionsInDirectory` without roots/limit; `session_mutation.go FetchSessionInfo`; `session.go ensureServerSession`; `projects.go ListProjectSuggestions`.
- **Planned add:** official `roots=true` and `limit`; missing-worktree rule; default-list archive hide.
- **Planned modify:** ListSessions to official roots/limit per directory; keep by-id get.
- **Planned seal:** filesystem existence as session truth; list HTTP as `messages[]`.
- **Planned delete:** unscoped list; flattening children into home list.
- **canonical protocol:** no protocol change. Existing get_session / session list in `docs/protocol/bridge-v1.md`. Do not advertise `session_pagination` unless implemented.
- **Planned tests Mac:** `TestOfficialRootsLimit`; `TestArchivedHiddenInDefaultListKeptById`; `TestMissingWorktreeRule`.
- **Planned tests iOS:** OD-2 global aggregate vs scoped directory request; no `messages[]` writes from list/get.
- **Out of scope:** `sessions.children`/`fork` (OD-3 future); C7 mutations; workspace.file_*; message hydrate (C4).

### C3 — submit

- **truth owner:** OpenCode serve persists the user message and emits SSE. Mac ProjectionKernel is the only CordCode timeline. iOS ProjectionStore is the only SSV2 `messages[]` writer. HTTP 204 is admission only.
- **only writer:** Kernel for CordCode timeline; ProjectionStore for iOS `messages[]`. Authoritative stable messageID is **correlation-only** for request ↔ persisted user message ↔ projection. iOS local composer placeholder is **presentation-only**. **no iOS writer** on the active timeline.
- **transaction domain:** request/control admits the turn; live observation (C4) is the Kernel ingest. Submit must not write `SessionProjection` itself.
- **new data path:** iOS send RPC presentation-only placeholder → `handleSendMessage` → `session.go Send` `POST /session/:id/prompt_async` `{messageID, agent, model{providerID,modelID}, variant?, parts}`. 204/200 is admission. Persisted user message and assistant stream re-enter via C4 unique Kernel ingest. Unsupported parts fail before network I/O.
- **active write inventory:** `Send` HTTP 204 return: admission only. `handleSendMessage`: request/control. iOS composer placeholder: presentation-only, sealed from `messages[]`. Optimistic/history/raw second writer: forbidden.
- **failure presentation:** HTTP 204 is not a confirmed turn or assistant completion. Unsupported part fails before POST (zero prompt). Unavailable model: zero prompt POST. Missing terminal stays failed/running until C4 evidence. No inferred success, no timeout completion.
- **anti-double-write proof:** existing `session_test.go` / `official_shapes_test.go`. Planned: `TestPromptAsyncMessageIDAgentModelPartsFromA1A2A9`; `TestHTTP204IsAdmissionNotTurn`; `TestUnsupportedPartFailsBeforeNetwork`; `TestIOSComposerPlaceholderDoesNotWriteMessages`.
- **实现前补样本:** selected variant is 实现前补样本. A1 omit-when-unset is not a selected-variant sample and must not derive variant cycling. Vision remains unsupported (A9 text-only mock).
- **Gate B surfaces:** `turns.prompt_async`, `turns.abort`, `content.text`, `content.file.persist`, `content.image.persist`, `content.file_mention`, `content.agent_mention`, `configuration.agents`, `configuration.variants`, `sessions.create`.
- **Gate A samples:** A1, A2, A4, A9.
- **Current paths:** `session.go Send` body `{parts:[text], model}` only; `ensureServerSession`; `CancelTurn`; `handleSendMessage` / `handleAbortGeneration`.
- **Planned add:** prompt_async `messageID`/`agent`/supported parts; adapter-generated authoritative messageID as correlation-only; fail-before-network for unsupported parts.
- **Planned modify:** Send to official PromptInput.
- **Planned seal:** HTTP 204 as `turn_completed`; iOS optimistic writer; history/raw second writer; omit-when-unset as selected-variant proof.
- **Planned delete:** dual-call sync `POST /session/:id/message`.
- **canonical protocol:** no protocol change if messageID is generated in the Mac adapter and correlated to existing `SessionProjectionTurn.messageId` (`go-bridge/projection_types.go`; `docs/protocol/bridge-v1.md` Session Projection Stream). If a new send-RPC client field is later required, that is canonical-first only: Mac `docs/protocol/schema` → Go types → iOS mirror/Swift/web types → dual-repo tests. This round does not change protocol.
- **Planned tests Mac:** A1/A2/A9 replay; `TestZeroPromptOnUnsupportedPart`.
- **Planned tests iOS:** composer placeholder presentation-only; send 204 does not append `messages[]`; no iOS writer.
- **Out of scope:** `turns.prompt` sync; vision; selected variant UI until sample; C4 part mapping; C5 fallback chain; C6.

### C4 — event/reconnect

- **truth owner:** OpenCode 1.18.18 direct SSE and `GET /session/:id/message` are upstream facts. One Mac ProjectionKernel per `(backendId, sessionId)` is CordCode timeline truth. iOS ProjectionStore is the only SSV2 client writer.
- **only writer:** Hydrate: `ProjectionKernel.BeginHydrateTransaction` / `ApplyHydrateEvent` / `CommitHydrateTransaction`. Live: **unique Kernel ingest** `EventPublisher.publish` → `ProjectionKernel.IngestLive`. iOS: `ProjectionStore.applyFrame`. `kernel==nil → projection.Apply` is a sealed second reducer, not a writer.
- **transaction domain:** hydrate, live, reconnect, and projection delivery. Nested sync is an explicit skip domain, not an ingest domain.
- **new data path:** **unique pre-Kernel normalization point:** `events.go sseSubscriber.handleRawEvent` unwraps payload then `sseSubscriber.handleServerEvent` is the exclusive type switch. **nested sync skip:** `payload.type==sync` is explicitly skipped at that unique point before any `core.Event` mapping (official `server-sdk.tsx:284`). Direct SSE maps to `core.Event` → `mapAgentEvent` → unique Kernel ingest. **reconnect same Kernel:** validate/invalidate/rehydrate the same `(backendId, sessionId)` Kernel; history/status supply hydrate facts only. Direct+sync dual ingest is forbidden.
- **active write inventory:** `handleRawEvent`/`handleServerEvent`: mapper only. `publish → IngestLive`: allowed unique live writer. hydrate transaction: allowed. `kernel==nil → projection.Apply`: sealed second reducer. History/status bypass of Kernel: forbidden. Consumer-side dedup/referee: forbidden. iOS history merge: sealed.
- **failure presentation:** A3: APIError 400 `isRetryable=false`, retry then idle; not healthy completion. A4: abort → `MessageAbortedError`, idle, not `finish=stop` success. A5: busy-at-disconnect; reconnect first direct `server.connected` then live deltas; terminal idle on second SSE — never synthesize `finish=stop` from empty reload status. Missing terminal stays running/failed. No inferred success.
- **anti-double-write proof:** existing `events_test.go`, `handlers_projection_ocweb_test.go`, `session_sync_v2_test.go`. Planned: `TestNestedSyncSkipAtHandleServerEventOnly`; `TestDirectAndSyncDoNotBothAdvanceSyncRev`; `TestKernelNilProjectionApplySealed`; `TestReconnectSameKernelA5Sequence`; `TestHistoryStatusCannotBypassKernel`.
- **实现前补样本:** `content.reasoning` and `observation.external_turns` are supported now + source-only: 实现前补样本 before reasoning/external-turn translator parity claims. Current `WireDescriptor` `external_turn_streaming` is frozen this round and is not proof.
- **Gate B surfaces:** `observation.direct_sse`, `observation.nested_sync`, `observation.reconnect`, `observation.status`, `observation.global_events`, `observation.external_turns`, `sessions.messages`, `content.reasoning`, `content.tool`, `content.text`.
- **Gate A samples:** A1, A2, A3, A4, A5.
- **Current paths:** `handleServerEvent` implicit default-drop of `sync` (gap: not a named exclusive skip); `event_publisher.go` kernel==nil fallback; hydrate via `streamBackendRichHistoryProjectionEvents`; iOS `ProjectionStore.applyFrame` / `scheduleRecoveryPull`.
- **Planned add:** named exclusive nested sync skip; A1–A5 Kernel replay; reconnect same-Kernel tests.
- **Planned modify:** replace implicit default-drop with explicit skip at the unique pre-Kernel normalization point; use captured error/abort/retry/reconnect sequences.
- **Planned seal:** `kernel==nil → projection.Apply`; consumer referee; dual ingest; history/status Kernel bypass; iOS history merge.
- **Planned delete:** second reducer; raw OpenCode history into iOS `messages[]`.
- **canonical protocol:** no protocol change. Existing `projection_snapshot` / `projection_patch` / `sync_invalidate` in `docs/protocol/bridge-v1.md` Session Projection Stream.
- **Planned tests Mac:** A1–A5 replay; `TestSinglePreKernelSyncSkip`; `TestSealKernelNilFallback`.
- **Planned tests iOS:** reconnect does not call `loadMessages`; gap/invalidate pulls `get_session_projection` only.
- **Out of scope:** C3 prompt fields; C6 ownership implementation; C5 catalog; selected variant; protocol additions.

### C5 — model/agent

- **truth owner:** OpenCode serve provider/agent catalogs are upstream facts. Selected model/agent on prompt_async is a request field, not timeline content.
- **only writer:** control-plane catalog (`AvailableModels`, `ListAgents`). Timeline remains Kernel + ProjectionStore. Unavailable choice must produce zero prompt POST.
- **transaction domain:** request/control (catalog). Prompt field carry is C3; C5 owns fallback levels and connected-only catalog.
- **new data path:** `GET /provider` connected-only and `GET /agent` → catalogs. Fallback levels kept distinct: current choice, agent model, configured default, recent, connected fallback. Only connected providers. No recursive catalog search.
- **active write inventory:** `models.go AvailableModels/fetchModelCatalog`: catalog, allowed. `resolveSendModel`: request-time choice, must not invent ids. `ListAgents`: catalog. Recursive catalog JSON search: forbidden. Catalog rows into `messages[]`: forbidden. Unconnected provider POST: forbidden (zero prompt).
- **failure presentation:** unavailable/not-connected choice → zero prompt POST and diagnosable model error. Missing catalog fails closed. Collapsing fallback levels is a failure, not success.
- **anti-double-write proof:** planned distinct fixtures per fallback level; `TestUnavailableChoiceZeroPrompt`; `TestNoRecursiveCatalogSearch`.
- **实现前补样本:** `configuration.providers` and `configuration.default_model` are supported now + source-only: 实现前补样本. Do not derive configured-default from omit-when-unset or from first connected model.
- **Gate B surfaces:** `configuration.models`, `configuration.providers`, `configuration.default_model`, `configuration.agents`.
- **Gate A samples:** A1, A2.
- **Current paths:** `models.go` connected-only catalog; `resolveSendModel` shorter than official 5-level chain; `ListAgents`; Send omits agent (C3).
- **Planned add:** distinct fixture per official fallback level, or explicit exclusion of a level with a recorded product rule.
- **Planned modify:** `resolveSendModel` to named levels.
- **Planned seal:** recursive catalog parsing; inventing model ids; first connected model as configured default without sample.
- **Planned delete:** legacy recursive catalog search; v2 `postModel` as product path (C1 quarantine).
- **canonical protocol:** no protocol change. Existing list_models / model_switch in `docs/protocol/bridge-v1.md`. `agent_selection` advertisement is WireDescriptor, frozen this round.
- **Planned tests Mac:** five-level fallback fixtures; zero-prompt unavailable; connected-only.
- **Planned tests iOS:** picker shows connected catalog only; selecting unavailable model does not send.
- **Out of scope:** selected variant (C3 pre-sample); `provider_auth`; C3 parts; timeline.

### C6 — interaction

- **truth owner:** OpenCode serve owns permission/question/todo facts. Canonical permission and question state are reduced by Mac ProjectionKernel. Todos remain control-plane, not SessionProjection timeline. ProjectionStore is the only SSV2 writer for projected cards.
- **only writer:** Permission: Kernel reduces canonical `permission_request`/`permission_resolved`; raw control must not write `messages[]`. Question: Kernel reduces canonical `user_input_requested`/`user_input_resolved` only. Todo: no timeline writer; control-plane publisher only if advertised.
- **transaction domain:** live (canonical Kernel ingest for permission/question) plus explicit control-plane (permission raw presentation; todo).
- **new data path:** `permission.asked` + `GET /permission` → `handlePermissionAsked` → unique Kernel ingest. Reply `POST /session/:id/permissions/:id {response:once|always|reject}` is control; reject is not a healthy completion. Official `question.asked`/`replied`/`rejected` → canonical `user_input_requested`/`user_input_resolved`; do not invent official `question_resolved`; legacy question presentation must not enter Kernel or SSV2 raw. `todo.updated` + `GET /todo` `{content,status,priority}` no id → control-plane only; must not enter SessionProjection; do not create hash/content/position IDs.
- **active write inventory:** `RespondSessionPermission`: control reply. `handlePermissionAsked`: mapper then Kernel. `isDerivedLegacyQuestionEvent` skip IngestLive: sealed legacy. `RespondQuestion`/`RejectQuestion` currently `ErrNotSupported`: absence. `todo.updated` ignored: absence. Todo into SessionProjection: forbidden. Raw permission into `messages[]`: forbidden.
- **failure presentation:** A6 reject: `finish=tool-calls`, idle, pending cleared — not healthy stop. A7 reject: idle, pending cleared. Reply HTTP errors stay pending/failed. Missing path is unsupported/absent, not inferred success. Capability only after the full real path exists.
- **anti-double-write proof:** existing `projection_reducer_permission_test.go`. Planned: A6 once/always/reject; A7 `answers:string[][]`; A8 todo control-plane with no id; `TestPermissionRawDoesNotWriteMessages`; `TestLegacyQuestionNotIngestedAndNotSentToSSV2`; `TestTodoNotInSessionProjection`.
- **实现前补样本:** none of the seven source-only supported-now items belong to C6. A6/A7/A8 already captured; still do not advertise until the full path exists.
- **Gate B surfaces:** `interaction.permission.once`, `interaction.permission.always`, `interaction.permission.reject`, `interaction.question.reply`, `interaction.question.reject`, `interaction.todo`.
- **Gate A samples:** A6, A7, A8.
- **Current paths:** `handlePermissionAsked`; `RespondSessionPermission`; question handlers `ErrNotSupported`; todo ignored; `EventUserInputRequested`; WireDescriptor still only `external_turn_streaming` (frozen).
- **Planned add:** question mapper to user_input_*; todo control-plane without ids; A6–A8 replay.
- **Planned modify:** permission once/always/reject to A6 (always `askedAgain=false` on same isolated serve only).
- **Planned seal:** raw permission writing `messages[]`; legacy question into Kernel or SSV2; todo as timeline parts; inventing `question_resolved`; synthetic todo ids; advertising before the full path.
- **Planned delete:** permission-as-question folding as a product path.
- **canonical protocol:** no protocol change. Existing permission and `user_input_requested`/`user_input_resolved` plus `todos_updated` in `docs/protocol/bridge-v1.md`. Capability advertisement frozen this round.
- **Planned tests Mac:** A6/A7/A8 replay; `TestRejectIsNotHealthyCompletion`; `TestTodoControlPlaneNoTimeline`.
- **Planned tests iOS:** existing `StructuredUserInputIOSRegressionTests.swift`; SSV2 never receives raw `question_*`; permission card from projection; todo dock control-plane only.
- **Out of scope:** C4 text/tool/reasoning; C7 mutations; advertising capabilities this round; moving todos onto SessionProjection without a separate approved projection-shape decision.

### C7 — mutation/secondary

- **truth owner:** OpenCode serve owns session title/archived/deleted facts. Catalog metadata may refresh on HTTP success. Timeline changes still re-enter the same Kernel.
- **only writer:** catalog/metadata for rename/archive/delete. Kernel for any resulting timeline. ProjectionStore for iOS timeline. Mutation HTTP success is not a turn factory.
- **transaction domain:** request/control (mutation/catalog). Timeline effects re-enter observation/Kernel (C4).
- **new data path:** first round only: rename PATCH title, archive PATCH `time.archived`, delete `DELETE /session/:id`. Archive uses A10. Rename and delete are source-only and must not implement translators until 实现前补样本. HTTP success → `FetchSessionInfo` / list invalidation only. OD-3 future/unsupported extras stay out of this slice.
- **active write inventory:** `ArchiveSession`: catalog metadata, allowed. `DeleteSession`: exists but delete remains 实现前补样本 before translator/parity. `SessionRenamer`: currently absent, not a silent success. HTTP mutation writing `messages[]`: forbidden. OD-3 extras in this slice: forbidden.
- **failure presentation:** mutation HTTP errors are mutation failures. Missing `SessionRenamer` is absence, not a no-op success. Delete 404 after success is expected. Do not keep a local ghost session. No inferred timeline rewrite.
- **anti-double-write proof:** existing `session_mutation_test.go`. Planned after samples: `TestArchiveA10MetadataOnly`; `TestRenameAfterSampleCatalogOnly`; `TestDeleteAfterSampleCatalogOnly`; `TestOD3ExtrasNotImplemented`.
- **实现前补样本:** `sessions.rename` and `sessions.delete` are supported now + source-only: 实现前补样本. Archive is A10, not source-only.
- **Gate B surfaces:** `sessions.rename`, `sessions.archive`, `sessions.delete`.
- **Gate A samples:** A10.
- **Current paths:** `session_mutation.go ArchiveSession` / `DeleteSession` / `FetchSessionInfo`. No `SessionRenamer`.
- **Planned add:** `SessionRenamer` only after rename sample; targeted delete replay after delete sample.
- **Planned modify:** archive/delete catalog refresh to by-id + list invalidation.
- **Planned seal:** generic API coverage milestone; mutation HTTP as confirmed turn; implementing OD-3 extras in this slice.
- **Planned delete:** do not pull OD-3 extras into C7 first round.
- **canonical protocol:** no protocol change. Existing session mutation RPCs in `docs/protocol/bridge-v1.md` when the agent implements the interfaces. WireDescriptor frozen.
- **Planned tests Mac:** A10 archive replay; rename/delete tests only after 实现前补样本 fixtures.
- **Planned tests iOS:** list/detail match server after archive; by-id still opens archived; rename/delete UI only after samples.
- **Out of scope:** OD-3 keep-mapped-future-or-unsupported: `sessions.fork`, `sessions.share`, `sessions.unshare`, `sessions.children`, `turns.command`, `turns.shell`, `turns.summarize`, `turns.revert`, `turns.unrevert`, `workspace.session_diff`, `workspace.vcs`, `workspace.file_list`, `workspace.file_read`, `workspace.file_search`. Each remains future/unsupported and needs its own source → sample → mapping → test chain later. No generic API coverage.

## S4 — acceptance/test map

S4 is documentation only (test map; no test code written this round). `gateSExited=false` — Gate S exit awaits independent audit. `gateCStarted=false`. `productCodeFrozen=true`. Planned tests are **planned**, not passing; existing tests were verified to exist with the named symbol in the named file.

| id | layer | affected slices | status | existing | planned |
|---|---|---|---|---|---|
| `s4.adapter` | OpenCode adapter | C1, C3, C4 | partial | 12 | 5 |
| `s4.kernel-live` | Kernel live | C1, C4 | partial | 5 | 3 |
| `s4.kernel-hydrate` | Kernel hydrate | C2, C4 | partial | 6 | 3 |
| `s4.reconnect` | reconnect | C4 | partial | 6 | 3 |
| `s4.delivery` | delivery | C4, C6 | partial | 7 | 1 |
| `s4.ios-ownership` | iOS ownership | C1, C4 | partial | 7 | 2 |
| `s4.ios-application` | iOS application | C3, C4 | partial | 7 | 3 |
| `s4.interaction` | interaction | C6 | partial | 12 | 7 |
| `s4.cross-repository` | cross-repository | C1–C7 | partial | 5 | 2 |

affectedSlices 合集覆盖 C1–C7。iOS 文件路径相对 `../cordcode-ios`。

### `s4.adapter` — OpenCode adapter（C1/C3/C4）

- **invariant:** direct SSE 与 nested sync 不能双 ingest；nested sync 在唯一 pre-Kernel normalization point（single-normalization）显式跳过（nested-sync-skip）；stable messageID 在 request、persisted user message、projection 间一致（correlation-only）；unsupported version/shape fail closed；source-only 行必须 **capture-before-translator**（先补同版本样本，再写 translator 测试）。
- **producer proof:** handleRawEvent 解 envelope 后 handleServerEvent 是唯一类型开关；unknown type log-and-drop 不当成功。
- **reducer/fence proof:** 一个上游语义事件最多推进一次 syncRev（planned `TestDirectAndSyncDoNotBothAdvanceSyncRev`）；v2/unknown generation 零 Kernel ingest（planned `TestV2FailClosedQuarantine`）。
- **delivery/client proof:** adapter 不向 SSV2 直投 raw；catalog/todo 信号不进 chat stream（existing `TestSSECatalogSignalsDoNotEnterChatStream` / `TestSSETodoUpdatedIgnoredInPhase1`）。
- **existing tests:** `agent/opencode-web/events_test.go TestSubscribeStreamsLiveSSEFrames / TestSSEStreamReconnectsAndHealsAfterDrop / TestSSECatalogSignalsDoNotEnterChatStream / TestSSETodoUpdatedIgnoredInPhase1 / TestSSEUserMessageArmsTurnOnce`; `probe_test.go TestProbeShapeArbiterFlipsGeneration / TestProbeRejectsUnauthenticatedServer / TestProbeFailsWhenNoRouteAnswers / TestProbeSelectsV2GenerationWhenLegacyRouteMissing`; `official_shapes_test.go TestOfficialShape_SessionListCarriesDirectoryQuery`; `sessions_test.go TestGetRichSessionHistoryMapsParts`; `opencodeweb_test.go TestInstanceStatusSuccessCarriesGeneration`.
- **planned tests (planned, not passing):** `events_test.go TestNestedSyncSkipAtHandleServerEventOnly`; `go-bridge/events_test.go TestDirectAndSyncDoNotBothAdvanceSyncRev`; `probe_test.go TestVerified118Only / TestV2FailClosedQuarantine`; `session_test.go TestPromptAsyncMessageIDAgentModelPartsFromA1A2A9`.
- **sample/evidence dependency:** A1–A10 replay 归属：A1/A2/A3/A4/A5→C4；A6/A7/A8→C6；A9→C3；A10→C2/C7。
- **negative assertion:** unknown SSE type drop 不算成功；v2/unknown 零 ingest；unsupported part 零 POST；禁止 recursive JSON search 与 legacy parser fallback。
- **exit condition:** A1–A10 每个 replay 有命名 owner；nested sync 显式跳过有 owning test；每个 source-only translator 行先登记 capture gate。
- **current status:** partial。
- **未覆盖边界:** named exclusive sync skip 仍是隐式 default-drop（C4 实现项）；selected variant 无同版本样本；vision remains future，不进入 supported acceptance tests。

### `s4.kernel-live` — Kernel live（C1/C4）

- **invariant:** 一个 canonical event 最多推进一次 Kernel chain（single-ingest）；execution 只从 authoritative status/error/idle 终结；`kernel==nil → projection.Apply` 是要封死的第二 reducer（kernel-nil-seal）；HTTP 204 与 timeout 不得制造 completion。
- **producer proof:** publisher lock 下 `publish → kernel.IngestLive`；每个 logical event 恰好一条记录（existing `TestEventPublisherAppendsExactlyOneRecordPerLogicalEvent`）。
- **reducer/fence proof:** revision 只在 projection commit 推进（existing `TestProjectionRevisionAdvancesOnlyOnProjectionCommit`）；kernel-nil fallback 待 seal（planned `TestKernelNilProjectionApplySealed`）。
- **delivery/client proof:** 零输出 idle 暴露 turn error（existing `TestSSEZeroOutputIdleSurfacesTurnError`）；catalog gate 零 POST（existing `TestSendCatalogGateIsZeroPOST`）。
- **existing tests:** `go-bridge/event_buffer_test.go TestEventPublisherAppendsExactlyOneRecordPerLogicalEvent`; `go-bridge/projection_kernel_test.go TestProjectionRevisionAdvancesOnlyOnProjectionCommit / TestProjectionSnapshotDeeplyImmutable`; `agent/opencode-web/events_test.go TestSSEZeroOutputIdleSurfacesTurnError`; `agent/opencode-web/session_test.go TestSendCatalogGateIsZeroPOST`.
- **planned tests (planned, not passing):** `go-bridge/event_publisher_test.go TestKernelNilProjectionApplySealed`; `go-bridge/handlers_projection_test.go TestHistoryStatusCannotBypassKernel`; `agent/opencode-web/session_test.go TestHTTP204IsAdmissionNotTurn`.
- **sample/evidence dependency:** A3（retry/error 终态）、A4（abort→MessageAbortedError→idle）、A1/A2（authoritative idle）。
- **negative assertion:** 无 timeout-completion；204 仅 admission；缺 terminal 不推断成功；history/status 不得绕过 Kernel。
- **exit condition:** kernel-nil seal 与不可绕过各有 owning test；A3/A4 回放证明 terminal 只来自 authoritative 证据。
- **current status:** partial。
- **未覆盖边界:** kernel==nil fallback 代码仍在（sealed 待 C4）。

### `s4.kernel-hydrate` — Kernel hydrate（C2/C4）

- **invariant:** cold history 只进 private hydrate transaction（source cut/fence）；hydrate 期间 pending live 按序补追；commit 后 push/pull 同一个 head；不进 EventBuffer、offline queue、mailbox、raw fanout。
- **producer proof:** opencode-web rich history 只走 ApplyHydrateEvent（existing `TestOpenCodeWebProjectionHydrateFromRichHistory`）。
- **reducer/fence proof:** Begin/Apply/Commit 原子；checkpoint admission/invalidation（existing `TestProjectionCheckpointAdmissionAppendAndInvalidation`）；restore schema 校验（existing `TestProjectionCheckpointRestoreValidAndEmpty`）。
- **delivery/client proof:** by-id 优先（existing `TestOpenCodeWebGetSessionPrefersByIDFetcher`）；cold pull 只提交 running partial（existing `TestOpenCodeWebLiveSessionColdPullSkipsSealAndCommitsRunningPartial`）。
- **existing tests:** `go-bridge/handlers_projection_ocweb_test.go TestOpenCodeWebProjectionHydrateFromRichHistory / TestOpenCodeWebGetSessionPrefersByIDFetcher / TestOpenCodeWebLiveSessionColdPullSkipsSealAndCommitsRunningPartial`; `go-bridge/projection_kernel_test.go TestProjectionHydrateRetryPolicy / TestProjectionCheckpointAdmissionAppendAndInvalidation / TestProjectionCheckpointRestoreValidAndEmpty`.
- **planned tests (planned, not passing):** `TestHydratePendingLiveCaughtUpInOrder`; `TestPushPullSameHeadAfterCommit`; `TestColdHistoryNeverEntersEventBuffer`（均在 `go-bridge/projection_kernel_test.go`）。
- **sample/evidence dependency:** A1/A2 reload persisted messages；A5 hydrate facts；A10 by-id archived。
- **negative assertion:** hydrate 不进 ordinary live seq/EventBuffer/live buffer/offline queue/mailbox/raw fanout；客户端不得自行 merge。
- **exit condition:** pending-live 顺序、push/pull 同 head、buffer 隔离各有 owning test。
- **current status:** partial。
- **未覆盖边界:** 三项 planned test 未落地。

### `s4.reconnect` — reconnect（C4）

- **invariant:** generation/epoch mismatch 拒绝 stale frame；gap/invalidate 只拉 `get_session_projection`；恢复用同一个 Kernel（reconnect-same-Kernel）；iOS 不得 history merge / content similarity / completion timer；A5 busy→server.connected→live delta→idle 有 owning test。
- **producer proof:** 进程 epoch 注入 EventPublisher 且跨 hello/register 一致（existing `TestServerInjectsProcessEpochIntoEventPublisher` / `TestConcurrentHelloConnectionsShareProcessEpoch` / `TestServerUsesOneInjectedEpochAcrossHelloAndRegister`）。
- **reducer/fence proof:** checkpoint schema 不匹配即拒绝（existing `TestProjectionCheckpointRejectsPreviousSemanticSchema`）；validate/invalidate/rehydrate 同一 Kernel。
- **delivery/client proof:** 旧 session projection 响应不改当前 timeline（existing iOS `testOldSessionProjectionResponseDoesNotMutateCurrentTimeline`）；SSE drop 后 heal（existing `TestSSEStreamReconnectsAndHealsAfterDrop`）。
- **existing tests:** `go-bridge/bridge_epoch_test.go` 三项；`go-bridge/projection_kernel_test.go TestProjectionCheckpointRejectsPreviousSemanticSchema`; `go-bridge/handlers_projection_ocweb_test.go TestOpenCodeWebLiveSessionColdPullWithBusyProbeCommitsRunningPartial`; iOS `ChatViewModelSessionSyncV2Tests.swift testOldSessionProjectionResponseDoesNotMutateCurrentTimeline`.
- **planned tests (planned, not passing):** `agent/opencode-web/events_test.go TestReconnectSameKernelA5Sequence`; `go-bridge/event_publisher_test.go TestEpochMismatchRejectsStaleFrame`; iOS `testReconnectDoesNotCallLoadMessagesWhenSyncV2Active`.
- **sample/evidence dependency:** A5 真实序列（busy-at-disconnect；server.connected → live delta；terminal idle；reload finish=stop）。
- **negative assertion:** 不得从空 reload 合成 finish=stop；无 iOS history merge / similarity / timer；gap 只走 get_session_projection。
- **exit condition:** A5 序列、epoch 拒绝、iOS 不回调 loadMessages 各有 owning test。
- **current status:** partial。
- **未覆盖边界:** 三项 planned（C4 实现项）。

### `s4.delivery` — delivery（C4/C6）

- **invariant:** SSV2 connection 不接收 raw timeline writers；control-plane allowlist 明确且不携带 timeline payload；projection snapshot/patch/invalidate 只走现有这一条 websocket delivery path；push 丢失后 pull 返回同一 Kernel head。
- **producer proof:** shouldDeliverRawEventLocked 对 SSV2 封印 raw timeline；分类固定（existing `TestSessionSyncV2RawTimelineClassification`）。
- **reducer/fence proof:** patch 只投 V2 连接（existing `TestProjectionPatchDeliveredToV2ConnOnly`）；canonical user_input projection-only、legacy 单向派生（existing `TestCanonicalUserInputIsProjectionOnlyAndLegacyIsOneWayDerived`）。
- **delivery/client proof:** control-plane raw 仍可达（existing `TestProjectionOnlyConnStillReceivesControlPlaneRawEvents`）；无订阅不 crash（existing `TestProjectionPatchNoSubscriberNoCrash`）；iOS push 旁路 cursor（existing `testProjectionPatchPublishesDespiteRecoveryCursorAhead`）。
- **existing tests:** `go-bridge/projection_delivery_test.go TestProjectionPatchDeliveredToV2ConnOnly / TestSessionSyncV2RawTimelineClassification / TestCanonicalUserInputIsProjectionOnlyAndLegacyIsOneWayDerived / TestProjectionOnlyConnStillReceivesControlPlaneRawEvents / TestProjectionPatchNoSubscriberNoCrash`; `go-bridge/session_sync_v2_test.go TestSessionSyncV2DirectTransportResultPrecedesLiveProjectionPatch`; iOS `K4ProjectionPushBypassTests.swift testProjectionPatchPublishesDespiteRecoveryCursorAhead`.
- **planned tests (planned, not passing):** `go-bridge/projection_delivery_test.go TestPushLostPullReturnsSameKernelHead`.
- **sample/evidence dependency:** A1/A5 delivery envelope 与现有 websocket 一致。
- **negative assertion:** raw text_delta / question_* 不发 SSV2；无第二 projection 管道；control-plane 不夹带 timeline payload。
- **exit condition:** push-lost→pull-same-head 有 owning test。
- **current status:** partial。
- **未覆盖边界:** push-lost 直接断言 planned。

### `s4.ios-ownership` — iOS ownership（C1/C4）

- **invariant:** ownership 在 t=0 由 mode + selected backend capability 决定；loading/empty/failed/invalidated 不恢复 legacy writer；loadMessages gate 在 cache/snapshot/network 读取之前；OpenCode Web 属于 projection backend family。
- **producer proof:** active mode 选择时即持有（existing `testActiveModeOwnsAtSelectionBeforeProjectionArrives`）；off 保留 legacy（existing `testOffModeKeepsLegacyOnly`）。
- **reducer/fence proof:** capability 按 backend 收敛（existing `TestSessionSyncV2CapabilityScopedToMigratedBackend`；server.go advertiseSessionSyncV2Backend 含 opencode-web）。
- **delivery/client proof:** loading 只启动有界 disaster retry（existing `testLoadingProjectionStartsOnlyBoundedDisasterRetryTask`）；honest empty 也武装（existing `testActiveModeArmsOnHonestEmptyAndExecutingShell`）；history fallback 复用原事务策略（existing `testHistoryFallbackAndRetryReuseOriginalTransactionPolicy`）。
- **existing tests:** iOS `ChatViewModelSessionSyncV2Tests.swift testActiveModeOwnsAtSelectionBeforeProjectionArrives / testOffModeKeepsLegacyOnly / testLoadingProjectionStartsOnlyBoundedDisasterRetryTask / testOffModeTransientBridgeDisconnectKeepsEmptyHistoryLoadingWithoutBanner / testActiveModeArmsOnHonestEmptyAndExecutingShell`; iOS `ArchitectureGuardrailTests.swift testHistoryFallbackAndRetryReuseOriginalTransactionPolicy`; mac `go-bridge/session_sync_v2_test.go TestSessionSyncV2CapabilityScopedToMigratedBackend`.
- **planned tests (planned, not passing):** iOS `testOpenCodeWebSelectedBackendProjectsAtT0`; `testLoadMessagesGateRunsBeforeCacheSnapshotNetworkReads`.
- **sample/evidence dependency:** A1 首开；A5 断连状态机。
- **negative assertion:** loading/empty/failed/invalidated 不触发 loadMessages / replaceMessagesFromServer / get_session_messages 兜底 / delayed raw flush。
- **exit condition:** opencode-web t=0 与 gate 顺序各有 owning test。
- **current status:** partial。
- **未覆盖边界:** 两条 planned（C4/C1 实现项）。

### `s4.ios-application` — iOS application（C3/C4）

- **invariant:** 只有 ProjectionStore 应用 full/delta/push；baseRev→syncRev、fence、generation/epoch、stale frame rejection 持续生效；submit path / composer placeholder / HTTP 204 不写 `messages[]`（**submit-zero-writer**；placeholder 仅 presentation-only），只有后续 projection apply 才写。
- **producer proof:** guard 封印外部双 writer 并固定 ProjectionStore 持有/泵模型（existing `testSessionSyncV2GuardsExternalDualWriters`）。
- **reducer/fence proof:** 低 rev snapshot 不重开已解决卡片（existing `testStaleLowerRevSnapshotDoesNotReopenResolvedCard`）；gap 对齐权威 snapshot（existing `testGapRecoveryRealignsUserInputCardToAuthoritativeSnapshot`）。
- **delivery/client proof:** baseline 前 patch 不渲染后 reconcile（existing `testPatchesBeforeBaselineDoNotRenderThenBaselineReconciles`）；无 baseline 不裁决（existing `testNoAdjudicationWhileUnresolved`）；snapshot/invalidate 旁路 cursor（existing `testProjectionSnapshotAndInvalidateAlsoBypassCursorGate`）；send 重绑 pending 到 real（existing `TestOpenCodeWebSendRebindsPendingToRealBeforeFirstEvent`）。
- **existing tests:** iOS `ArchitectureGuardrailTests.swift testSessionSyncV2GuardsExternalDualWriters`; iOS `StructuredUserInputIOSRegressionTests.swift testStaleLowerRevSnapshotDoesNotReopenResolvedCard / testGapRecoveryRealignsUserInputCardToAuthoritativeSnapshot`; iOS `LiveOnlyProjectionStateTests.swift testPatchesBeforeBaselineDoNotRenderThenBaselineReconciles / testNoAdjudicationWhileUnresolved`; iOS `K4ProjectionPushBypassTests.swift testProjectionSnapshotAndInvalidateAlsoBypassCursorGate`; mac `go-bridge/handlers_projection_ocweb_test.go TestOpenCodeWebSendRebindsPendingToRealBeforeFirstEvent`.
- **planned tests (planned, not passing):** iOS `testComposerPlaceholderDoesNotWriteMessages`; `testSubmit204DoesNotAppendMessagesUntilProjectionApply`; `ProjectionStoreTests.swift testBaseRevSyncRevFenceRejectsOldGeneration`（文件按 Gate C 落位，须先建 owning 文件）。
- **sample/evidence dependency:** A1/A2 persisted user message ↔ projection 对齐；A9 parts。
- **negative assertion:** placeholder 不写/确认/覆盖/裁判 timeline；204 不是 turn_completed；无 optimistic/history/raw 第二 writer。
- **exit condition:** submit-zero-writer、presentation-only、fence 各有 owning test。
- **current status:** partial。
- **未覆盖边界:** 三条 planned（C3 实现项）。

### `s4.interaction` — interaction（C6）

- **invariant:** permission raw control 不写 timeline、canonical permission 只归约一次、reject 不是健康完成；question 只走 canonical `user_input_requested`/`user_input_resolved`，legacy question frame 不进 Kernel 不发 SSV2；todo 只在 control-plane、绝不进 SessionProjection、不造 hash/content/position ID；A6/A7/A8 request/reply/reject/reload 都有 owning test；capability 只在完整真实路径后广告。
- **producer proof:** `handlePermissionAsked` 只产 canonical 事件；todo.updated phase-1 显式忽略（existing `TestSSETodoUpdatedIgnoredInPhase1`）。
- **reducer/fence proof:** pending tool + requires action（existing `TestReducerPermissionRequestProjectsPendingToolAndRequiresAction`）；resolved 清 pending 保 running（existing `TestReducerPermissionResolvedClearsPendingAndLeavesRunning`）；deny 拒绝 tool（existing `TestReducerPermissionResolvedDenyRejectsTool`）；active turn 归属（existing `TestReducerUserInputRequestedWithoutTurnIDUsesActiveTurn`）。
- **delivery/client proof:** canonical user_input projection-only、legacy 单向（existing `TestCanonicalUserInputIsProjectionOnlyAndLegacyIsOneWayDerived`）；iOS 五状态一卡（existing `testSnapshotRoundTrip_AllFiveStatusesMapToExactlyOneCard`）；question 面诚实 ErrNotSupported（existing `TestQuestionsNotSupported`）；WireDescriptor 无 todos/question_reply（existing `TestWireDescriptorMatchesDesign`）。
- **existing tests:** `go-bridge/projection_reducer_permission_test.go` 五项；`go-bridge/projection_delivery_test.go TestCanonicalUserInputIsProjectionOnlyAndLegacyIsOneWayDerived`; `agent/opencode-web/events_test.go TestSSEPermissionAsked / TestSSETodoUpdatedIgnoredInPhase1`; `agent/opencode-web/session_test.go TestQuestionsNotSupported`; `agent/opencode-web/opencodeweb_test.go TestWireDescriptorMatchesDesign`; iOS `StructuredUserInputIOSRegressionTests.swift testSnapshotRoundTrip_AllFiveStatusesMapToExactlyOneCard / testPatchUpsertUserInput_ResolvesPendingToAnsweredInPlace`.
- **planned tests (planned, not passing):** `TestPermissionA6OnceAlwaysRejectReplay`; `question_test.go TestQuestionA7AnswersStringMatrixAndReject`; `projection_reducer_todo_test.go TestTodoA8ControlPlaneNoIdNoTimeline / TestTodoNotInSessionProjection`; `TestPermissionRawDoesNotWriteMessages`; `TestLegacyQuestionNotIngestedAndNotSentToSSV2`; `TestRejectIsNotHealthyCompletion`.
- **sample/evidence dependency:** A6/A7/A8 全路径（request/reply/reject/reload）。
- **negative assertion:** reject ≠ 健康完成；不发明官方 question_resolved；permission 不折叠为 question；todo 不进 SessionProjection 不合成 ID；完整路径前不广告。
- **exit condition:** A6/A7/A8 全路径 owning test + 三类所有权 negative test。
- **current status:** partial。
- **未覆盖边界:** question/todo 当前诚实缺席；全部 planned 为 C6 实现项。

### `s4.cross-repository` — cross-repository（C1–C7）

- **invariant:** 协议顺序 **canonical-first**：Mac `docs/protocol/schema` → Go types → iOS mirror/Swift/web types → capability advertisement；C1–C7 判定 no protocol change 的 slice 必须有 guard test 证明没有私自新增 wire field；若 Gate C 后来需要协议变更，先完成 canonical-first 双仓 tests 再广告；capability flag 只能在完整 Mac+iOS 路径与相关测试通过后开启。
- **producer proof:** hello/hello_ack fixture 走 schema 解码/往返（existing `TestBridgeV1HelloFixtureDecodes` / `TestBridgeV1HelloAckFixtureRoundTrips`）。
- **reducer/fence proof:** capability 按 opt-in 广告/省略（existing `TestSessionSyncV2CapabilityAdvertisedOnOptIn` / `TestSessionSyncV2CapabilityOmittedWithoutOptIn`）。
- **delivery/client proof:** opencode-web WireDescriptor 恰为 `external_turn_streaming`、无 todos/question_reply（existing `TestWireDescriptorMatchesDesign`）——证明 OD-3 extras 未实施且未广告。
- **existing tests:** `go-bridge/bridge_v1_schema_test.go TestBridgeV1HelloFixtureDecodes / TestBridgeV1HelloAckFixtureRoundTrips`; `go-bridge/session_sync_v2_test.go TestSessionSyncV2CapabilityAdvertisedOnOptIn / TestSessionSyncV2CapabilityOmittedWithoutOptIn`; `agent/opencode-web/opencodeweb_test.go TestWireDescriptorMatchesDesign`.
- **planned tests (planned, not passing):** `TestNoUndeclaredWireFieldAdded`; `TestOD3ExtrasNotImplementedAndNotAdvertised`.
- **sample/evidence dependency:** None（结构守卫行）：证据为 schema fixture 与 WireDescriptor 测试本体。
- **negative assertion:** OD-3 十四项与 vision 保持 future/unsupported：未实施、未广告、无 owning implementation acceptance；不私加 wire field；完整路径前不开 capability flag。
- **exit condition:** no-undeclared-wire-field guard 与 OD-3 未实施未广告断言有 owning test。
- **current status:** partial。
- **未覆盖边界:** 双仓协议变更流程仅在 Gate C 真正改协议时触发（当前仅 C3 条件性 send-RPC 字段）。

### S4 source-only capture-before-translator 顺序

先 capture/checker，后 translator test；不得直接列 translator test：

| id | C slice | 顺序 |
|---|---|---|
| `workspace.project` | C2 | capture/checker → then translator test |
| `content.reasoning` | C4 | capture/checker → then translator test |
| `observation.external_turns` | C4 | capture/checker → then translator test |
| `configuration.providers` | C5 | capture/checker → then translator test |
| `configuration.default_model` | C5 | capture/checker → then translator test |
| `sessions.rename` | C7 | capture/checker → then translator test |
| `sessions.delete` | C7 | capture/checker → then translator test |
| `selected_variant` | C3 | capture/checker → then translator test（A1 omit-when-unset 不算 selected-variant 样本） |

vision remains future：不进入 supported acceptance tests。

## S3 protocol and freeze

Every C slice above states **no protocol change** except C3's conditional canonical-first plan if a new send-RPC field is later required. The S3/S4 rounds do not change `docs/protocol/`, Go types, iOS mirror, WireDescriptor, or capability advertisement.

`s4Started=true`. `s4Completed=true`. `gateSExited=false` — Gate S exit awaits independent audit. `gateCStarted=false`. `productCodeFrozen=true`. Do not start Gate C from this document.
