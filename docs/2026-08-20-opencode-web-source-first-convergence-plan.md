# OpenCode Web source-first convergence design（canonical）

- Date: 2026-08-20
- Canonical status: **This file is the only implementation-design authority for `opencode-web`. Gate B map, SSV2 impact, and sample inventory are evidence appendices, not alternative plans. C1 is supervisor-verified. C2 product code exists but audit-003 is `partial`: WP HTTP-evidence ownership and the generation-118 project decoder still require the Stage-0 corrections specified below. C3–C7 product implementation is frozen. The only authorized next work is capture/checker work for E1–E7 plus the C2 evidence correction; after those samples return, this file must be revised with the observed shapes before C3–C7 code begins. Protocol, WireDescriptor, capability advertisement, and iOS timeline ownership remain unchanged.**
- Audit input: [2026-08-20-opencode-web-source-parity-audit.md](2026-08-20-opencode-web-source-parity-audit.md)
- Historical input only: [2026-08-18-opencode-web-backend-design.md](2026-08-18-opencode-web-backend-design.md) and its completion report
- Goal: make `opencode-web` a faithful, bounded adapter of the official OpenCode Web behavior, rather than a cleaned-up copy of the legacy OpenCode backend

## 0. Execution boundary

This document is the implementation contract and the **single canonical design**. An implementation agent must not reconstruct behavior by mentally joining several companion files. Every supported feature has an execution dossier in §6 containing its official source, same-version sample, transport shape, bridge mapping, SSV2 ownership, failure behavior, tests, and exclusions.

Companion files have deliberately narrower roles:

| Companion | Role | Cannot authorize |
|---|---|---|
| `2026-08-20-opencode-web-1.18.18-sample-inventory.md` + `testdata/official-1.18.18/samples/` | raw/sanitized evidence ledger and capture provenance | a translator, product decision, capability, or inferred field path |
| `2026-08-20-opencode-web-gate-b-capability-map.{md,json}` | exhaustive 60-surface disposition evidence and owner decisions OD-1/OD-2/OD-3 | implementation of a `source-only` row |
| `2026-08-20-opencode-web-ssv2-impact.{md,json}` | writer inventory, transaction domains, and acceptance-map evidence | a second writer, reducer, fallback, or raw timeline route |
| supervisor directives/reports | bounded work authorization and audit record | a change to this design's product semantics or external shape |

If a companion conflicts with this file, implementation stops. The conflict is resolved here first; an agent may not select the more convenient statement.

During an evidence-only gate, an agent may inspect official source, build isolated capture infrastructure, collect/sanitize samples, and add evidence-validation tooling. It must not modify the corresponding product translator, run writes against the owner's managed serve, use a real provider account without new authorization, install a product build, or execute the owner test matrix.

When the owner resumes implementation, work proceeds through the gates below in order. A gate cannot be marked complete by prose, endpoint reachability, or a fake fixture derived from the implementation.

## 1. Non-negotiable architecture

`opencode-web` remains:

```text
official OpenCode Web source and serve behavior
                    ↓
versioned HTTP/SSE client and real-shape fixtures
                    ↓
explicit bridge-v1 semantic translation
                    ↓
iOS capability and UI behavior
```

The following are forbidden as design evidence:

- copying request/event/history logic from `agent/opencode`;
- inferring payloads from field names in bridge-v1;
- treating OpenAPI/SDK types alone as runtime proof when a live response differs;
- treating a fake-server test authored from the same assumption as independent verification;
- adding fallback parsers for hypothetical generations;
- advertising a capability before its full request, event, reload, and error path is proven.

Legacy code may be read only as a list of historical bugs and bridge integration points. It cannot establish OpenCode semantics.

## 2. Definition of parity

“Follow the official Web” means all four layers agree:

1. **Invocation:** CordCode sends the same meaningful fields and content parts as the official UI for the selected operation.
2. **Observation:** CordCode consumes the same authoritative response/event stream, including error and resolution events.
3. **State:** create, list, reopen, mutate, archive, external turns, and reconnect converge to the same server-owned session state.
4. **Presentation:** bridge-v1 advertises only supported capabilities and preserves distinctions such as answer text versus reasoning, permission versus question, and busy versus retry versus idle.

Pixel parity with the Web UI is not required. Server-semantic parity is.

## 3. Supported runtime policy

The first convergence target is **OpenCode 1.18.18**, because the installed managed serve and audited checkout match that version. The v2 compatibility branches are not part of the first completion claim.

Every supported generation needs its own:

- exact version range;
- official source commit/tag;
- sanitized request/response/event sample pack;
- compatibility matrix;
- targeted integration tests.

If a runtime does not match a verified generation, fail with a diagnosable unsupported-version status. Do not recursively search unknown JSON for model- or event-looking nodes.

## 4. Gate A — evidence pack before implementation

Create a versioned fixture inventory for 1.18.18. Each row must include official UI source location, server/schema source location, capture command or harness scenario, sanitized raw input/output, and the bridge mapping decision.

Required P0 scenarios:

| # | Scenario | Required captured sequence |
|---|---|---|
| A1 | create + first healthy text message | create response; direct and `sync` SSE; user message; assistant text/reasoning/tool; terminal state |
| A2 | follow-up message | two prompt requests with distinct messageID plus agent/model/text; variant is **absent when unset**; persisted user IDs correlate with the two request IDs; terminal state |
| A3 | provider rejection/retry | retry statuses, final error carrier, assistant `info.error`, idle ordering |
| A4 | abort | busy turn, abort response, resulting status/events, final persisted messages |
| A5 | SSE disconnect/reconnect | last event before disconnect, status recovery, duplicate direct/`sync` behavior, terminal outcome |
| A6 | permission | pending endpoint, asked event, once/always/reject request, replied/resolved event, reload behavior |
| A7 | question | pending endpoint, asked event, reply `answers:string[][]`, reject, resolved/reload behavior |
| A8 | todos | endpoint item, update event, identity/order behavior, completion transition |
| A9 | prompt parts | text, file/image, file mention, agent mention; persisted message parts |
| A10 | session listing | root versus child sessions, limit boundary, archived session, multiple directories |

Use a deterministic local provider harness that never contacts a billable/external provider. If the official runtime has no supported local hook, document that blocker and obtain owner authorization before using a real account. Do not replace the missing capture with guessed JSON.

Gate A exit: all P0 rows are green, or an explicit owner decision removes the corresponding capability from scope and advertisement.

### 4.1 Post-Gate-A source-only evidence queue

Gate A proved A1–A10, but Gate B later promoted seven `source-only` surfaces to `supported now`. They are not implementation-ready. These IDs are the only remaining pre-implementation capture queue:

| ID | Surface | Required real observation | Current status | Product rule before capture |
|---|---|---|---|---|
| E1 | selected variant (`configuration.variants`) | a prompt with a non-empty selected variant; exact request field and persisted/reload behavior | `PENDING-SAMPLE` | omit variant when unset; do not implement variant selection |
| E2 | reasoning (`content.reasoning`) | populated reasoning part in HTTP reload and direct SSE, including delta/update ordering | `PENDING-SAMPLE` | do not map an assumed reasoning shape or fold it into answer text |
| E3 | external official-Web turn (`observation.external_turns`) | second client creates/sends while bridge observes global SSE through terminal/reload | `PENDING-SAMPLE` | do not claim two-client completeness from the capability string or polling |
| E4 | providers (`configuration.providers`) | real `/provider` response with connected set and model catalog | `PENDING-SAMPLE` | do not recursively search JSON or advertise provider selection |
| E5 | configured default model (`configuration.default_model`) | configured-default source, valid/invalid/absent behavior, and relation to connected providers | `PENDING-SAMPLE` | first connected model is not a configured default |
| E6 | rename (`sessions.rename`) | PATCH path/body/response plus list/by-ID/event refresh | `PENDING-SAMPLE` | no `SessionRenamer` implementation/advertisement |
| E7 | delete (`sessions.delete`) | DELETE response, subsequent list/by-ID 404, and `session.deleted` invalidation | `PENDING-SAMPLE` | existing code is not parity authorization; do not advertise completion |

For E1–E7, official source is vocabulary and a capture recipe, not shape proof. The evidence agent records raw HTTP/SSE/reload and runs independent checkers; it does **not** choose bridge mappings. After capture, the design owner updates the corresponding §6 dossier from `PENDING-SAMPLE` to either `SAMPLE-VERIFIED` or `BLOCKED/UNSUPPORTED`. Only then may an implementation directive authorize that translator.

### 4.2 Mandatory stop lines and method reset

The following are not review suggestions. Each signal **stops feature implementation immediately**. While stopped, the agent may preserve logs, add isolated capture tooling, sanitize real samples, and update the evidence ledger; it may not stack another product-code hypothesis on top of the failed or unverified one.

| Danger signal | Why the evidence is invalid | Mandatory action | Work may resume only when |
|---|---|---|---|
| A product-code patch is prepared or committed before Gate A is complete | implementation has started defining the external contract it is supposed to discover | stop the patch series; mark the affected A-row unverified; return to official UI/server source and real capture | the affected row has source citations, a same-version real sample, and a bridge mapping decision; all Gate A exit conditions still hold |
| The sandbox invalid-model HTTP 400 is cited as a healthy first-turn sample | it proves schema rejection and field spelling only; it proves no admitted turn, user echo, assistant stream, or terminal ordering | label it `negative-schema-sample`; do not use it for A1/A2/A3/A4/A5 acceptance | a healthy deterministic provider trace covers create/prompt through persisted messages and terminal state |
| A fake-server response or SSE sequence is used to fill a missing real sample | a fixture authored from the design or implementation creates circular proof | remove the parity claim; retain the fake only as an internal unit-test aid, clearly labelled with its real-sample provenance or lack thereof | the fixture is regenerated or reviewed against an archived same-version real sample and official source |
| Capture difficulty leads to restoring a legacy parser, recursive search, speculative generation branch, or silent fallback | uncertainty is being hidden as compatibility and can produce plausible but false success | fail closed with a diagnosable unsupported/unverified state; record the missing sample and version | the exact generation/shape has its own source commit, sample pack, mapping, and contract test |
| One targeted fix produces no observable change in the reported symptom | the working causal model has been falsified or the edited path is not active | do not attempt a second fix in the same state-machine model; preserve the failed result and capture the real request/event/bridge/iOS timeline, then locate the first divergence from official Web | a revised causal statement names the first observed divergence and a new minimal experiment can falsify it |
| A report says “tests pass” without naming sample provenance and official source locations | internal consistency is being presented as external semantic proof | reject the completion claim; classify each test as fixture replay, HTTP contract, SSE replay, sandbox integration, or bridge/iOS regression | the report links the archived sample or explicitly states that the test is internal-only, cites official call sites/schemas, and includes the exact targeted command/result |

For every translated behavior, the required proof tuple is:

```text
official UI call site + official server/schema source
                    + same-version real sample
                    + explicit bridge-v1 mapping
                    + targeted replay/integration result
```

Missing any member means “not yet proven,” not “probably compatible.” A test can prove internal correctness without proving OpenCode parity; reports must state which claim each test actually supports.

### 4.3 First-failed-fix escalation record

If a targeted fix leaves the owner-visible symptom unchanged, the next progress report must contain, before any new product patch:

1. the original causal hypothesis and the exact observation that falsified it;
2. the real timeline across OpenCode request/response/SSE, MacBridge event translation, bridge-v1/SSV2, and iOS projection where available;
3. the official Web behavior for the same operation, with source and sample references;
4. the first observed divergence, or an honest statement that it is not yet located;
5. one minimal discriminating experiment, not a bundle of additional fixes.

Owner prompting is not required to trigger this escalation. “The previous patch did not change the symptom” is itself the trigger.

## 5. Gate B — product capability map

Before code changes, map every official user-facing surface to exactly one disposition:

- **supported now**: complete bridge and iOS path will be implemented;
- **deliberately unsupported**: capability absent and UI unavailable;
- **not applicable**: official Web behavior has no CordCode product equivalent, with rationale;
- **future**: capability absent; dependency and evidence gap named.

Minimum surfaces to classify:

| Group | Surfaces |
|---|---|
| sessions | list/get/create/rename/archive/delete/children/fork/share/unshare |
| turns | prompt/prompt_async/command/shell/abort/summarize/revert/unrevert |
| content | text/reasoning/tool/file/image/agent mention/patch/snapshot/step markers |
| interaction | permission/question/todo |
| workspace | project/path/file status/read/search/diff/VCS |
| configuration | providers/models/default model/agents/variants |
| observation | status, global events, external turns, reconnect, catalog refresh |

The map, not accidental interface availability, determines `WireDescriptor` capabilities.

Working map (documentation only; **Gate B exited** 2026-08-20 after owner resolutions OD-1/OD-2/OD-3; independent audit still required before Gate S):

- [2026-08-20-opencode-web-gate-b-capability-map.md](2026-08-20-opencode-web-gate-b-capability-map.md)
- [2026-08-20-opencode-web-gate-b-capability-map.json](2026-08-20-opencode-web-gate-b-capability-map.json)
- checker: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_b_map.py`

Owner resolutions:

- OD-1 `hide-in-default-list-keep-by-id`
- OD-2 `aggregate-global-list-keep-scoped-list`
- OD-3 `keep-mapped-future-or-unsupported`

`supported now` + `source-only` is a product-scope commitment, **not** implementation authorization. Gate S C-slice impact/test maps must register **实现前补样本** for every translated behavior that still lacks a same-version captured sample. Do not write event/response translators from source citations alone.

Gate B exit: there is no endpoint in the official Web inventory whose CordCode disposition is implicit, and owner has judged OD-1/OD-2/OD-3. Stop for independent audit. Do not enter Gate S from this wrap-up.

## 5.1 Gate S — Session Sync v2 architecture proof

Gate S is mandatory after Gate B and before any Gate C product-code change. OpenCode Web parity does not replace CordCode's Session Sync v2 architecture. The official serve defines upstream facts; it is **not** a second active timeline writer for CordCode clients.

The continuing architecture authority is the iOS repository `CLAUDE.md` section “Session Sync v2 架构路线护栏”, together with:

- `docs/2026-07-24-single-source-multidevice-sync-design.md` in the iOS repository;
- `docs/2026-07-26-session-sync-v2-cold-start-kernel-restart-plan.md` in the iOS repository;
- the MacBridge canonical protocol pack under `docs/protocol/`.

S1–S4 working proof (documentation only; Gate C not started at exit; product code frozen at exit):

- [2026-08-20-opencode-web-ssv2-impact.md](2026-08-20-opencode-web-ssv2-impact.md)
- [2026-08-20-opencode-web-ssv2-impact.json](2026-08-20-opencode-web-ssv2-impact.json)
- S1/S2 checker: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_s_s1_s2.py`
- S3 checker: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_s_s3.py`
- S4 checker: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_s_s4.py`
- **Gate S exit 2026-08-20**: `gateSExited=true`, `gateCStarted=false`, `productCodeFrozen=true` (exit snapshot). S4 checker enforces exit ordering — `gateSExited=true` fails if any exit condition (9 layers / s4Completed / pre-sample-order=8 / zero other problems) is unmet.

### S1. Truth owners and single writers

| Concern | Authority / only writer | Explicitly forbidden |
|---|---|---|
| OpenCode session/message/status facts | the verified 1.18.18 `opencode serve` API/SSE is the upstream source; the adapter may observe and translate it | treating local guesses, legacy adapter state, or iOS state as OpenCode server truth |
| CordCode active timeline and execution | one Mac `ProjectionKernel` / `SessionProjection` per `(backendId, sessionId)`; push and pull read the same committed Kernel head | sending OpenCode history or raw SSE directly into the active iOS timeline; maintaining a second reducer beside the Kernel |
| iOS `messages[]`, turn state, and generation | `ProjectionStore` is the single client-side projection writer from t=0 whenever the selected backend advertises negotiated `session_sync_v2` | optimistic/history/raw/legacy writers, delayed raw flush, content comparison, or a timeout-based completion writer |
| protocol and projection shapes | MacBridge `docs/protocol/` is canonical; iOS mirror, Swift models, and web types are synchronized consumers | changing only one repository or using an implementation-private field as an undeclared wire contract |

“Server-semantic parity” in §2 therefore means that the adapter feeds correct canonical facts into the existing Kernel. It never means that the official Web reducer, raw HTTP history, or raw SSE may bypass the Kernel and become a client writer.

### S2. Transaction domains and allowed data paths

| Domain | Required path | Boundary |
|---|---|---|
| cold hydrate/reopen | verified OpenCode message/history source → existing pathless rich-history mapper → Kernel private hydrate transaction under one source cut/fence → committed projection | hydrate does not enter ordinary live seq, EventBuffer, offline queue, mailbox, or raw client fanout |
| live OpenCode events | verified v1 direct SSE payload → `opencode-web` source adapter normalization → shared `EventPublisher`/Kernel live ingest → projection patch/snapshot → iOS `ProjectionStore` | one upstream event produces at most one canonical Kernel ingest; no direct raw timeline delivery to an SSV2 client |
| v1 nested `sync` events | preserve in evidence capture, but follow the verified official v1 rule and skip them before canonical ingest unless later same-version evidence changes the contract | direct and nested forms must never both advance `syncRev` for the same semantic event |
| reconnect/recovery | reconnect observation may read verified server messages/status, then validate/invalidate/rehydrate the same Kernel and resume via checkpoint/fence/full-or-delta projection | no history merge, raw catch-up writer, local similarity dedup, or inferred healthy terminal on iOS |
| requests/mutations/catalog | prompt/abort/rename/archive/delete/list/model calls remain control/request paths; their resulting timeline effects re-enter through the authoritative observation/Kernel path | an HTTP 2xx may refresh metadata but cannot directly manufacture a confirmed timeline turn or completion |
| explicitly allowed control plane | session catalog, model/agent catalog, diagnostics, context usage, and todos remain outside `messages[]` unless a separately approved projection design moves them | a control-plane exception cannot carry text/reasoning/tool/turn state or mutate projection execution |

Permissions and structured questions require special handling:

- `permission_request` / `permission_resolved` may continue through the existing explicit control-plane presentation while their canonical state is also reduced by the Kernel; the raw control path must not write `messages[]` or execution.
- structured questions use canonical `user_input_requested` / `user_input_resolved` into the Kernel. `question_asked` / `question_resolved` are one-way legacy presentation only and must not be delivered raw to an SSV2 client or ingested back into the Kernel.
- todos stay an explicit raw control-plane exception in this convergence unless Gate B and a separate projection-shape decision deliberately migrate them. Todo events must not be smuggled into timeline parts.

### S3. Impact declaration required before each Gate C slice

Before implementing C1–C7, the agent must add an impact record to its evidence/completion log with all fields below. “No change” is a valid value only with a source/code citation.

| Required field | What must be stated |
|---|---|
| truth owner | upstream OpenCode fact owner and CordCode timeline owner |
| only writer | Mac Kernel ingest site and iOS ProjectionStore apply site |
| transaction domain | request/control, hydrate, live, reconnect, or projection delivery |
| new data path | every new or changed producer → mapper → Kernel/control → consumer edge |
| active write inventory | every code path that could touch timeline content, execution, or `messages[]`, including paths intentionally sealed |
| failure presentation | exact failed/running/unsupported behavior; no inferred success or automatic legacy fallback |
| anti-double-write proof | targeted producer, reducer/fence/delivery, and client writer-seal tests |
| 实现前补样本 | if the Gate B row is `supported now` with `gateASample=source-only`, the slice **must not** write event/response translators until a same-version captured sample exists; record this as a prerequisite gate. Source citations are not implementation authorization |

If the slice cannot prove that it adds no second truth, writer, consumer referee, or automatic fallback, it does not enter implementation.

### S4. Mandatory SSV2 acceptance matrix

Gate S must produce a concrete test map before Gate C. As implementation proceeds, the owning slice must satisfy the applicable rows:

| Layer | Required proof |
|---|---|
| OpenCode adapter | direct + nested `sync` evidence cannot double-ingest; stable IDs survive translation; unsupported shapes fail closed; every source-only translated behavior lists 实现前补样本 before mapper work |
| Kernel live | each canonical event advances the one Kernel chain at most once; execution completes only from authoritative terminal evidence |
| Kernel hydrate | cold history enters private hydrate under source cut/fence; live append during hydrate is caught up in order; push and pull expose the same head |
| reconnect | epoch/generation mismatch rejects stale frames; gap/invalidate recovers via `get_session_projection`, never raw/history merge |
| delivery | SSV2 connections do not receive raw timeline writers; control-plane allowlist remains explicit and carries no timeline payload |
| iOS ownership | negotiated per-backend capability selects projection ownership at t=0; loading/empty/failed projection never re-enables legacy writers |
| iOS application | only `ProjectionStore` applies full/delta/push; `baseRev → syncRev`, fence, and old-generation rejection remain enforced |
| interaction | permission raw control does not mutate timeline; canonical question projects once; legacy question frames are suppressed for SSV2 |
| cross-repository | canonical protocol, iOS mirror/Swift/web types, capability advertisement, and guard tests change coherently |

Gate S exits only when:

1. S1–S3 are reflected in the Gate B capability map and per-slice impact records;
2. the S4 test map names the existing or planned owning test for every affected row;
3. C3 optimistic correlation is based on an authoritative stable message ID and does not create an iOS timeline writer;
4. C4 direct/`sync` handling and reconnect recovery identify the single pre-Kernel normalization point and the single Kernel ingest path;
5. C6 classifies permission, canonical question, legacy question presentation, and todo ownership exactly as above;
6. protocol/projection changes, if any, have a canonical-first cross-repository change plan.

Any violation discovered during Gate C triggers the §4.2 stop line and §4.3 method reset. It may not be “temporarily” bypassed with `.off`, history fallback, raw delivery, local completion timers, or content-similarity reconciliation.

## 6. Canonical execution dossiers

This section replaces the old seven-bullet implementation sketch. It is the only place from which product translators may be implemented. Gate B and Gate S remain auditable evidence, but an implementation agent is not expected or permitted to invent a missing conclusion by joining them.

Every dossier uses these fixed fields:

- **Status:** `IMPLEMENTED-VERIFIED`, `IMPLEMENTED-AUDIT-PARTIAL`, `SAMPLE-VERIFIED-DESIGN-READY`, `PENDING-SAMPLE`, or `FUTURE/UNSUPPORTED`.
- **User-visible behavior:** the CordCode promise, not an endpoint list.
- **Official UI source:** the 1.18.18 call site/reducer that establishes intent.
- **Server/schema source:** the 1.18.18 route/type that establishes vocabulary.
- **Same-version samples:** archived raw/sanitized evidence that establishes physical shape and ordering.
- **Verified transport shape:** exact request/response/event facts derived from those samples. A pending field says `UNKNOWN UNTIL E#`; source types do not fill it.
- **Bridge and SSV2 mapping:** the decided translation, truth owner, transaction domain, and only writer.
- **Error and unsupported behavior:** fail-closed behavior and capability effect.
- **Owning tests:** positive, negative, replay/integration, and regression proof required for this dossier.
- **Out of scope:** nearby surfaces that this dossier must not opportunistically implement.

Implementation authorization is per dossier, not per file and not per endpoint. Independent dossiers may be developed in one batch after all of their sample gates are green, but a `PENDING-SAMPLE` dossier remains frozen even if another dossier in that batch is ready.

| Product surface | Dossier | Current authorization |
|---|---|---|
| runtime selection and transport | §6.1 / C1 | closed and verified |
| session list, detail, project buckets | §6.2 / C2 | product exists; evidence/decoder audit correction required |
| load/reopen message page | §6.3 / C4 | design-ready for captured shapes; reasoning waits for E2 |
| create and send first/follow-up message | §6.4 / C3 | base send design-ready; selected variant waits for E1 |
| live stream, retry, abort, reconnect, external turns | §6.5 / C4 | local flows design-ready; external turns wait for E3 |
| provider/model/agent selection | §6.6 / C5 | provider/default-model waits for E4/E5 |
| permission dock | §6.7 / C6 | design-ready from A6 |
| question dock | §6.8 / C6 | design-ready from A7 |
| Todo Dock | §6.9 / C6 | design-ready from A8 |
| rename/archive/delete | §6.10 / C7 | archive design-ready; rename/delete wait for E6/E7 |
| advertisement and excluded surfaces | §6.11 | activate only after owning dossier passes |

### 6.1 Runtime version and transport boundary — C1

- **Status:** `IMPLEMENTED-VERIFIED` at product commit `5c82ecc`, diagnostics correction `5b8451c`, shared-message correction `257f0df`, and closeout `40452cc`.
- **User-visible behavior:** only a verified OpenCode 1.18.18 serve is selectable. Unreachable, unauthorized, unknown-shape, and recognized-but-unverified generation states remain distinguishable and diagnosable.
- **Official UI source:** `packages/app/src/context/server-sdk.tsx:268-308` for v1 global SSE reconnect; `packages/app/src/utils/server-compat.ts` for v1 HTTP calls.
- **Server/schema source:** 1.18.18 HTTP groups under `packages/opencode/src/server/routes/instance/httpapi/`; global event route and health/probe shapes at official commit `2cba7e227d`.
- **Same-version samples:** A1 establishes healthy v1 HTTP/SSE; A5 establishes disconnect/reconnect. No v2 sample exists.
- **Verified transport shape:** data-plane access passes the single generation-118 gate; Basic Auth and directory query/header are control metadata; HTTP has a 30-second request timeout; SSE has no lifetime timeout and reconnects with bounded 1-to-15-second backoff. v2/unknown never reaches prompt, SSE ingest, or Kernel.
- **Bridge and SSV2 mapping:** transport/probe/catalog do not write timeline. The only timeline entry remains normalized v1 SSE through shared `EventPublisher` into the one Kernel.
- **Error and unsupported behavior:** `unsupported-generation (quarantined)` is shared by selection, diagnostics, and client acquisition. Unknown/unreachable/unauthorized are not mislabeled as quarantine. No recursive shape search or v2 fallback.
- **Owning tests:** `TestVerified118Only`, `TestV2FailClosedQuarantine`, `TestGenerationV2QuarantineZeroPromptAndZeroKernelIngest`, transport-control negative tests, diagnostics quarantine tests, and go-bridge descriptor availability test.
- **Out of scope:** v2 compatibility, speculative generations, protocol or capability additions.

### 6.2 Session list, session detail, and project buckets — C2

- **Status:** `IMPLEMENTED-AUDIT-PARTIAL`. C2 code is present at `ef21db1`, but it is not closed until the WP capture/checker and decoder strictness correction described here pass independent audit.
- **User-visible behavior:** the global session list aggregates official directory-scoped root lists for registered project worktrees, hides archived sessions, deduplicates stable IDs, and sorts deterministically. A scoped list stays scoped. By-ID retrieval remains available for archived and mutation-refresh paths. Missing worktrees are hidden only by the global visibility overlay; they are not deleted from server truth.
- **Official UI source:** `packages/app/src/context/global-sync/session-load.ts:5-26` (`roots:true`, bounded `limit`); `packages/app/src/utils/server-compat.ts:170-173` by-ID get; `server-compat.ts:304` project list; `event-reducer.ts:149-161` archived removal.
- **Server/schema source:** `httpapi/groups/session.ts` `ListQuery` and `GET /session/:id`; `handlers/project.ts:15-17`; `project/project.ts:35-56,217,243-244,336`; `packages/core/src/project.ts:105-119`.
- **Same-version samples:** A10 covers root/child, limits, archived by-ID, and directory separation. WP covers `/project`, but its checker currently derives from a duplicate summary field instead of the archived `http[].response`; therefore WP is evidence-pending until recaptured or normalized so the raw HTTP response is the sole proof.
- **Verified transport shape:** session list is `GET /session?directory=<dir>&roots=true&limit=100`; failure is returned, not retried without `limit`. By-ID is `GET /session/:id` with directory scope. A10 proves archived remains API-visible and by-ID-readable while the Web hides it. WP's exact `/project` top-level/row shape is **not authorized for decoder work until the corrected checker proves it from `http[].response`**.
- **Bridge and SSV2 mapping:** list/get/project are catalog/metadata reads only. They do not subscribe to SSE, call `EventPublisher`, ingest Kernel events, or write iOS `messages[]`. OpenCode project registry owns project facts; CordCode's missing-worktree filtering is a documented presentation overlay.
- **Error and unsupported behavior:** project registry failure, any bucket failure, malformed top-level shape, or malformed required row fails the entire operation. Empty registry returns empty catalog. There is no stale/partial/legacy fallback. The generation-118 project decoder must reject an envelope and malformed required rows unless a corrected real sample proves otherwise.
- **Owning tests:** A10 replay; corrected WP checker with destructive mutations against `http[].response`; roots/limit request-count test; archived hidden/by-ID retained; per-worktree aggregation; empty/failure/malformed cases; missing-worktree three-boundary test; list/get zero-writer test.
- **Out of scope:** pagination capability advertisement, project mutation, children/fork/share, and any timeline construction from list/detail responses.

### 6.3 Load, reopen, and hydrate the message page — C4

- **Status:** `SAMPLE-VERIFIED-DESIGN-READY` for text/tool/file facts captured by A1/A2/A4/A5/A6/A7/A8/A9. Reasoning translation is `PENDING-SAMPLE` on E2.
- **User-visible behavior:** opening or reopening a session presents the server-persisted message history once, preserves roles/parts/errors/terminal state, and converges with live events without replacing or duplicating an active projection.
- **Official UI source:** `packages/app/src/context/server-session.ts:568` calls `client.session.messages({sessionID,limit,before})`; `packages/app/src/context/global-sync/event-reducer.ts` applies message/session live facts.
- **Server/schema source:** `packages/opencode/src/server/routes/instance/httpapi/groups/session.ts:43-48,85-86,179-190` defines `MessagesQuery` and `GET /session/:sessionID/message`; message/part schemas live under `packages/opencode/src/session/`.
- **Same-version samples:** A1/A2 healthy persisted user/assistant order; A4 aborted assistant with `MessageAbortedError`; A5 partial-to-complete reconnect/reload; A6-A8 tool/interactions; A9 persisted text/file/image/file-source/agent-source parts. E2 is required for populated reasoning in reload and SSE.
- **Verified transport shape:** persisted parts proven today are text; file with `mime`, `url`, optional `filename` and optional file `source`; agent with `name` and optional mention `source`; tool state; assistant error/finish facts. The populated reasoning part shape and delta/update ordering are `UNKNOWN UNTIL E2`.
- **Bridge and SSV2 mapping:** HTTP history enters only a private Kernel hydrate transaction under source cut/fence. Live events arriving during hydrate enter pending-live and commit afterward in order. Push and pull expose the same Kernel head. iOS `ProjectionStore` remains the only `messages[]` writer from t=0.
- **Error and unsupported behavior:** malformed supported parts fail the hydrate instead of being silently dropped. An unsupported part cannot be coerced to text. Loading/empty/failed projection cannot re-enable `loadMessages` or `replaceMessagesFromServer`. Aborted/error assistants do not become healthy `finish=stop`.
- **Owning tests:** full A1/A2/A4/A5/A6/A7/A8/A9 history replay; E2 replay before reasoning code; hydrate pending-live ordering; source cut/fence; push/pull same head; malformed-part failure; iOS writer-seal and stale-revision rejection.
- **Out of scope:** raw history delivery to iOS, history merge fallback, local similarity dedup, reasoning before E2, patch/snapshot/step parts marked future in Gate B.

### 6.4 Create session and send first/follow-up messages — C3

- **Status:** `SAMPLE-VERIFIED-DESIGN-READY` for create, text, model, agent, file/image persist, file mention, and agent mention. Selected variant is `PENDING-SAMPLE` on E1.
- **User-visible behavior:** create yields one server session; each send yields one persisted user message with the client-stable ID and one authoritative assistant turn. First and follow-up sends use the same official request semantics. Composer placeholders are visual only.
- **Official UI source:** `server-compat.ts:163-169` create and `server-compat.ts:200-230` `promptAsync`.
- **Server/schema source:** `POST /session`; `POST /session/:id/prompt_async`; `PromptInput` in `session/prompt.ts:1499-1520`, including text/file/agent part unions.
- **Same-version samples:** A1 create + first send; A2 two follow-ups with distinct IDs; A9 supported prompt parts. E1 must capture a genuinely non-empty selected variant.
- **Verified transport shape:** create body is `{}` with directory routing. A1/A2 prompt body contains a fresh `messageID`, `agent:"build"`, `model:{providerID:"localmock",modelID:"echo"}`, and text part; unset variant is omitted. A9 proves file `{type,mime,url,filename?}`, optional file mention `source`, image as file part with image MIME, and agent `{type:"agent",name,source?}`. The selected-variant field's accepted value and persistence behavior are `UNKNOWN UNTIL E1`.
- **Bridge and SSV2 mapping:** `messageID` is correlation-only. A successful HTTP admission does not write timeline or synthesize a user/assistant message. Authoritative direct SSE enters the single pre-Kernel normalizer and Kernel; iOS composer state never arbitrates projection state.
- **Error and unsupported behavior:** unsupported parts or unavailable selected model/agent/variant fail before any POST. HTTP 204 means admission only. No local success, placeholder persistence, retry fallback, or legacy send path.
- **Owning tests:** exact method/path/query/body tests for A1/A2/A9; two-ID persistence correlation; zero-writer on admission; unsupported-part zero-network; unavailable selection zero-POST; sandbox create/send/reopen; E1 capture/checker and replay before variant implementation.
- **Out of scope:** command/shell/subtask, variant before E1, vision-provider interpretation beyond A9 persistence, and any protocol expansion not separately approved canonical-first.

### 6.5 Live stream, terminal state, abort, reconnect, and external turns — C4

- **Status:** `SAMPLE-VERIFIED-DESIGN-READY` for local direct SSE/status/retry/abort/reconnect from A1-A5. External official-Web turns are `PENDING-SAMPLE` on E3.
- **User-visible behavior:** text/tool/status stream once; retries and real errors remain visible; abort ends non-successfully; reconnect continues the same turn without duplication; a second official Web client will be observed only after E3 proves that path.
- **Official UI source:** `server-sdk.tsx:268-308` global SSE reconnect and `:284` v1 nested-`sync` skip; `event-reducer.ts` session/message/part reducers; `server-compat.ts:197-198` abort.
- **Server/schema source:** `GET /global/event`; `POST /session/:id/abort`; session status/error and message part event schemas.
- **Same-version samples:** A1/A2 healthy busy→idle; A3 retry→terminal API error; A4 abort→`MessageAbortedError`; A5 disconnect→`server.connected`→live delta→idle. E3 must capture a second client from request through reload while the bridge observes global SSE.
- **Verified transport shape:** v1 direct payload is authoritative; nested `sync` is retained as evidence but skipped exactly once before Kernel normalization. A5 proves reconnect is live continuation, not assumed replay. A3 final non-retryable 400 persists assistant error; A4 abort returns `true` but does not imply healthy completion. External-turn directory/session/event coverage is `UNKNOWN UNTIL E3`.
- **Bridge and SSV2 mapping:** one source adapter normalizes direct SSE, then one `EventPublisher`/Kernel ingest. Reconnect validates/invalidate/rehydrates the same Kernel and resumes projection; no second reducer, consumer referee, raw delivery, or iOS timer completion.
- **Error and unsupported behavior:** unknown event/part shapes fail or remain explicitly unsupported; they are not recursively decoded. A first attempted fix with no symptom change triggers §4.3, not another state-machine guess. External-turn capability remains unproven until E3.
- **Owning tests:** complete-sequence A1-A5 replays; direct+sync anti-double-ingest; retry/error/abort negative terminal tests; reconnect same-Kernel/epoch/stale rejection; kernel-nil seal; E3 second-client capture/checker/replay before external-turn completion claim.
- **Out of scope:** raw SSE to iOS, assumed server replay buffer, polling as external-turn parity, inferred idle, and v2 events.

### 6.6 Provider, model, agent, and selected variant — C5

- **Status:** models and agent request vocabulary are sample-backed by A1; providers are `PENDING-SAMPLE` on E4, configured default model on E5, and selected variant on E1. The dossier is not product-authorized until E1/E4/E5 are resolved.
- **User-visible behavior:** choices reflect connected providers and real models/agents; send preserves the selected model and agent; fallback distinguishes current choice, agent model, configured default, recent, and connected fallback rather than collapsing them.
- **Official UI source:** `packages/app/src/context/global-sync/bootstrap.ts:229-242` loads providers and `:266-269` agents; `packages/app/src/pages/session/composer/prompt-model-selection.ts:16-40` establishes connected validation and fallback order, while `:79-123` selects variants; `packages/app/src/utils/server-compat.ts:200-230` sends model/agent/variant.
- **Server/schema source:** the 1.18.18 provider/agent HTTP groups (`GET /provider`, `GET /agent`), configuration model source, and `packages/opencode/src/session/prompt.ts:1499-1520` `PromptInput` model/agent/variant fields at commit `2cba7e227d`.
- **Same-version samples:** A1 proves selected model/agent in an admitted prompt. E4 must archive the provider response and connected subset; E5 must prove configured valid/invalid/absent default; E1 must prove a non-empty selected variant.
- **Verified transport shape:** exact `/provider` top-level shape, connected-provider field, configured-default location, invalid-default behavior, and selected-variant persistence are respectively `UNKNOWN UNTIL E4/E5/E1`. A first connected model is not evidence of a configured default.
- **Bridge and SSV2 mapping:** catalogs and choice state are control plane and never write timeline. Prompt selection crosses into §6.4 only as request metadata; message/turn facts return through SSE/Kernel.
- **Error and unsupported behavior:** unknown catalog shape fails closed; no recursive JSON search, fake catalog, cached legacy snapshot, or silent model substitution. An unavailable explicit choice causes zero prompt POST. Capabilities remain unadvertised until all owning paths pass.
- **Owning tests:** E4/E5/E1 independent checkers with destructive mutations; one test per fallback level; exact selected request assertion; unavailable choice zero-POST; catalog refresh remains control-only; unknown-shape fail-closed regression.
- **Out of scope:** v2 catalogs, provider/account management, invented defaults, implicit variant support, and model fallback derived from legacy adapter behavior.

### 6.7 Permission Dock — C6

- **Status:** `SAMPLE-VERIFIED-DESIGN-READY` from A6.
- **User-visible behavior:** pending permission appears once; once/always/reject sends the official response; external resolution clears the pending request; rejection is not rendered as healthy assistant completion.
- **Official UI source:** `session-permission-dock.tsx`; v1 response mapping `server-compat.ts:496-503`.
- **Server/schema source:** v1 permission schema; `GET /permission`; `POST /session/:id/permissions/:permissionID`; `permission.asked` and `permission.replied`.
- **Same-version samples:** A6 contains once, always, reject, pending/reload, and repeated-pattern behavior.
- **Verified transport shape:** request includes `id`, `sessionID`, `permission`, `patterns`, `metadata`, `always`, and `tool`; reply body is `{response:"once"|"always"|"reject"}`. Always suppresses a repeated matching ask in the isolated serve. Reject may leave assistant `finish=tool-calls` and is not a success terminal.
- **Bridge and SSV2 mapping:** permission raw control may present the dock but cannot write `messages[]`; canonical permission state enters Kernel exactly once. Resolution uses the existing permission semantics and capability only after end-to-end support.
- **Error and unsupported behavior:** unknown response/request shape fails visibly; no automatic approval, fake resolution, question conversion, or healthy completion on reject.
- **Owning tests:** A6 full replay; exact three response bodies; pending/reload/external resolution; reject negative terminal; raw-control zero timeline write; canonical single-ingest; capability lag test.
- **Out of scope:** structured questions, todo items, permission policy editing, and synthetic resolution events.

### 6.8 Structured Question Dock — C6

- **Status:** `SAMPLE-VERIFIED-DESIGN-READY` from A7.
- **User-visible behavior:** a question batch preserves its request ID and answer groups; reply or reject resolves once; permission UI and question UI remain distinct.
- **Official UI source:** `session-question-dock.tsx:226-227`; v1 mapping `server-compat.ts:507-515`.
- **Server/schema source:** v1 question schema; `GET /question`; `POST /question/:requestID/reply`; `POST /question/:requestID/reject`; asked/replied/rejected events.
- **Same-version samples:** A7 includes asked, reply `{answers:[["red"]]}`, reject, direct events, and reload.
- **Verified transport shape:** reply body is `{answers:string[][]}` and reject has its own endpoint. 1.18.18 emits `question.replied` or `question.rejected`; no `question_resolved` event is observed.
- **Bridge and SSV2 mapping:** translate once to canonical `user_input_requested` / `user_input_resolved` through Kernel. Legacy question presentation is one-way and suppressed for SSV2; it cannot be ingested back or write `messages[]`.
- **Error and unsupported behavior:** do not invent `question_resolved`, collapse answer groups, reuse permission RPCs, or claim support when reply/reject is unavailable.
- **Owning tests:** A7 replay; multi-group body preservation; reply/reject/external resolution/reload; canonical single-ingest; legacy-frame suppression; zero `messages[]` writer; capability truth test.
- **Out of scope:** free-form UI redesign, permission conversion, guessed question event names, and protocol changes beyond an independently approved canonical-first plan.

### 6.9 Todo Dock — C6

- **Status:** `SAMPLE-VERIFIED-DESIGN-READY` from A8.
- **User-visible behavior:** the dock shows the authoritative ordered list and its pending/in-progress/completed transitions. It does not pretend each item has a server ID.
- **Official UI source:** `pages/session.tsx` todo load, `session-todo-dock.tsx`, and `event-reducer.ts` `todo.updated`.
- **Server/schema source:** `GET /session/:id/todo`; `session/todo.ts`; `todowrite` tool and `todo.updated` event.
- **Same-version samples:** A8 captures 197 frames and two replacements, proving items with exactly `content`, `status`, and `priority`, with no `id`.
- **Verified transport shape:** endpoint and event carry an ordered replacement list; the second write transitions the captured pending/in-progress items to completed. No stable server item ID is present.
- **Bridge and SSV2 mapping:** todo is an explicit control-plane advertisement, not a `SessionProjection` timeline part. Preserve server order and fields; do not synthesize IDs or use item identity as a timeline key.
- **Error and unsupported behavior:** malformed items fail the todo update; absence of IDs must not be hidden with random/hash IDs. Todo transport cannot write answer/reasoning/tool execution into timeline.
- **Owning tests:** A8 endpoint/event/reload replay; ordered replacement and completion transition; no-ID negative assertion; control-plane allowlist; zero Kernel timeline part / zero iOS `messages[]` write; capability truth test.
- **Out of scope:** editing todos independently of server tools, invented stable identity, projection migration, and question/permission reuse.

### 6.10 Rename, archive, and delete sessions — C7

- **Status:** archive is `SAMPLE-VERIFIED-DESIGN-READY` from A10. Rename is `PENDING-SAMPLE` on E6; delete is `PENDING-SAMPLE` on E7. This dossier is not batch-authorized until E6/E7 are resolved.
- **User-visible behavior:** rename updates server-owned title; archive disappears from default lists but remains by-ID retrievable; delete disappears and becomes by-ID not found. Metadata refreshes do not manufacture timeline events.
- **Official UI source:** `server-compat.ts:183-185` rename, archive/home reducer behavior, and `server-compat.ts:189-191` delete.
- **Server/schema source:** `PATCH /session/:id` `UpdatePayload.title`; `PATCH /session/:id` `time.archived`; `DELETE /session/:id`; session updated/deleted events.
- **Same-version samples:** A10 proves archive API/list/by-ID behavior. E6 must prove rename request/response/list/by-ID/event. E7 must prove delete response/list/by-ID 404/`session.deleted` invalidation.
- **Verified transport shape:** archive uses PATCH with `time.archived` and remains present in API list/by-ID while CordCode hides it by OD-1. Rename and delete exact response/event ordering are `UNKNOWN UNTIL E6/E7`.
- **Bridge and SSV2 mapping:** HTTP success refreshes catalog metadata only. Timeline content changes only if an authoritative observation is normalized into the existing Kernel; delete invalidates catalog/session ownership without fabricating a healthy turn.
- **Error and unsupported behavior:** failed/malformed mutation surfaces a real error and leaves prior metadata; no optimistic permanent mutation, list-only success, legacy fallback, or capability advertisement before replay and integration proof.
- **Owning tests:** A10 archive replay and default-list/by-ID rules; E6/E7 independent capture checkers; exact method/path/body/response; list/by-ID/event convergence; failure preservation; zero timeline writer; truthful capability activation.
- **Out of scope:** fork/share/unshare/summarize/revert/unrevert/diff/file/VCS and all fourteen OD-3 future/unsupported surfaces.

### 6.11 Capability activation and explicit exclusions

- **Status:** `FUTURE/UNSUPPORTED` until each owning dossier is sample-verified, implemented, and passes its positive and negative acceptance set.
- **User-visible behavior:** the iOS UI offers only end-to-end supported operations. A Go interface method, route, fake-server test, or Gate B `supported now` row does not itself make a capability available.
- **Official UI source:** the owning dossier's official call site; there is no generic “API coverage” source.
- **Server/schema source:** the owning dossier's verified route/schema; source-only rows remain vocabulary, not physical-shape proof.
- **Same-version samples:** every advertised translated behavior must cite A1-A10, corrected WP, or E1-E7. Missing evidence means absent capability.
- **Verified transport shape:** there is no generic envelope or recursive decoder. Each capability uses only its dossier's proven shape.
- **Bridge and SSV2 mapping:** `WireDescriptor` and `backend_capabilities` change only after the owning request, observation/reload, Kernel/control ownership, and iOS consumption paths are complete. Protocol changes require Mac canonical-first plus synchronized iOS mirror/models/tests.
- **Error and unsupported behavior:** absent/unverified capability remains unavailable with an honest diagnostic; never advertise and then silently fallback, return fake data, or route through legacy.
- **Owning tests:** descriptor/capability negative-before-positive tests; no undeclared wire field; unsupported UI absence; cross-repository schema round trip when applicable; OD-3 non-advertisement guard.
- **Out of scope:** Gate B rows marked future/deliberately unsupported/not applicable, all v2 behavior, and any product surface not explicitly promoted in this canonical section.

## 7. Test strategy after implementation resumes

Testing is layered and targeted:

1. pure shape/translation tests replay sanitized real fixtures;
2. HTTP contract tests assert exact method/path/query/body and replay real response fixtures;
3. SSE reducer tests replay complete captured sequences, not hand-selected single events;
4. deterministic sandbox integration covers create/send/reopen/mutation/interaction without external accounts;
5. MacBridge and iOS targeted tests cover changed bridge capabilities/projections plus the applicable Gate S producer, reducer/fence/delivery, connection-generation, and client writer-seal rows;
6. owner real-device matrix runs once at the feature gate, not after every small edit.

The test suite must prove negative behavior too: unsupported content fails before POST, unknown versions fail closed, duplicate events do not duplicate messages, and a diagnostic timeout cannot masquerade as an active turn.

No UI tests or simulator automation are authorized by this plan. No unbounded build/test process is permitted. Repository timeout and cleanup rules remain mandatory.

## 8. Release gates

No release or owner matrix begins until:

- Gates A and B are complete;
- Gate S is complete and every implemented slice carries its S3 impact record;
- all implemented capability descriptors match reality;
- targeted Go/Swift tests and builds pass;
- the runtime is installed only under the repository's normal release rule;
- evidence distinguishes sandbox success from owner managed-serve success.

The owner matrix must cover at least:

| # | Preconditions | Action | Expected |
|---|---|---|---|
| 1 | new session, connected provider | send first text message | one user bubble, streamed response, terminal idle |
| 2 | existing session | send follow-up | same session/model/agent semantics; no duplicate prompt |
| 3 | provider returns a real error | send message | retry/error is visible and turn terminates honestly |
| 4 | active turn | abort | server and iOS converge to non-running state |
| 5 | external official Web turn | send from Web, observe iOS | one external turn streams and persists |
| 6 | permission and question available | answer/reject from iOS and Web | first server resolution wins; both clients converge |
| 7 | renamed/archived/deleted sessions | mutate then reload | list/detail match official server state |
| 8 | reconnect during active turn | interrupt network, restore | no duplicate content; final state recovered |

This table is future acceptance scope, not authorization to execute it now.

## 9. Completion definition

The convergence work is complete only when:

- the historical design is no longer used as implementation truth;
- the 1.18.18 sample pack covers every advertised capability;
- official Web call sites and server schemas are cited for every mapped operation;
- first-message, follow-up, external-turn, reconnect, error, permission, question, and selected mutation paths pass their gates;
- unsupported and future capabilities are absent from advertisement and UI;
- Mac Projection Kernel remains the only CordCode timeline truth, iOS ProjectionStore remains the only active client writer, and hydrate/live/reconnect all use that same Kernel;
- all applicable Gate S anti-double-write, revision/fence, connection-generation, delivery, and client writer-seal tests pass;
- a completion report links evidence rather than restating claims;
- no mandatory stop-line event remains unresolved, and every failed-fix escalation names its final first divergence;
- no v2 parity is claimed without a separate v2 evidence pack.

Until then, the honest product status is: **OpenCode Web transport exists and several flows work, but source-level parity is incomplete.**
