# OpenCode Web source-first convergence design（canonical）

- Date: 2026-08-20
- Canonical status: **This file is the only implementation-design authority for `opencode-web`. Gate B map, SSV2 impact, and sample inventory are evidence appendices, not alternative plans. C1 is supervisor-verified. Directive-006 commit `4a215b0` and directive-007 commit `211bb27` closed the original WP/E1–E7 queue. Owner UI acceptance on 2026-08-21 then exposed a design error in E2: the failed synthetic live capture did not justify rejecting populated reasoning already present in the official HTTP history. E2b now sample-verifies that persisted 1.18.18 shape for hydrate; direct-SSE reasoning remains separately blocked until captured. The §6 mappings below are authoritative. Capability activation, release installation, owner testing, and the completion claim remain a final gate.**
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
| E1 | selected variant (`configuration.variants`) | a prompt with a non-empty selected variant; exact request field and persisted/reload behavior | `SAMPLE-VERIFIED` with E1b | expose only live keys from the selected model's `variants` object; unknown keys fail before POST |
| E2 | live reasoning (`content.reasoning`) | populated reasoning part in direct SSE, including delta/update ordering | `BLOCKED/UNSUPPORTED` for live translation | do not guess direct-SSE ordering or advertise live reasoning until a same-version capture exists |
| E3 | external official-Web turn (`observation.external_turns`) | second client creates/sends while bridge observes global SSE through terminal/reload | `SAMPLE-VERIFIED` | global SSE is authoritative; no polling substitute |
| E4 | providers (`configuration.providers`) | real `/provider` response with connected set and model catalog | `SAMPLE-VERIFIED` with E4b | use only connected provider/catalog/default/variant facts; `env/options` values and auth stay opaque |
| E5 | configured default model (`configuration.default_model`) | configured-default source, valid/invalid/absent behavior, and relation to connected providers | `SAMPLE-VERIFIED` with E5b | follow the official picker order in §6.6 and always POST an explicit validated model |
| E6 | rename (`sessions.rename`) | PATCH path/body/response plus list/by-ID/event refresh | `SAMPLE-VERIFIED` | PATCH title; refresh metadata only; no timeline write |
| E7 | delete (`sessions.delete`) | DELETE response, subsequent list/by-ID 404, and `session.deleted` invalidation | `SAMPLE-VERIFIED-WITH-NEGATIVE-EVENT` | success is response + list/by-ID convergence; do not require or invent `session.deleted` |

For E1–E7, official source is vocabulary and a capture recipe, not shape proof. The evidence agent records raw HTTP/SSE/reload and runs independent checkers; it does **not** choose bridge mappings. After capture, the design owner updates the corresponding §6 dossier from `PENDING-SAMPLE` to either `SAMPLE-VERIFIED` or `BLOCKED/UNSUPPORTED`. Only then may an implementation directive authorize that translator.

#### 4.1.1 Final combined evidence correction (E1b/E4b/E5b) — closed

Directive-007 captured these three observations together from the isolated OpenCode 1.18.18 sandbox at commit `211bb27`:

| ID | Captured observation | Mapping consequence |
|---|---|---|
| E1b | `/provider` exposes `models.echo.variants={high,low}`; selecting `high` produces top-level prompt `variant:"high"`, HTTP 204, and persisted `user.info.model.variant`; unset omits the field | model info exposes only those live keys; selection is model-scoped; an unlisted key is rejected before POST |
| E4b | raw + sanitized `/provider` are recursively structure-equivalent; top level is `{all,default,connected}`, and provider/model key, type, and order come from raw transport | strict decoder; only declared value classes may be redacted; credentials/options remain opaque and never become product configuration |
| E5b | real `GET /config` has valid `model:"localmock/alpha"`, invalid `model:"localmock/nonexistent"`, or no `model`; `/provider.default.localmock` is `zeta`, distinct from catalog-first `alpha` | official Web selection uses the exact order in §6.6; server-side omitted-model behavior is evidence only and is not CordCode's selection algorithm |

#### 4.1.2 Owner acceptance correction (E2b persisted reasoning) — captured

Owner UI acceptance on 2026-08-21 found that every sampled real OpenCode session failed immediately with `projection.hydrate_failed`. Read-only inspection of two failing 1.18.18 sessions proved that each history payload contained populated reasoning parts. The privacy-preserving structural sample is archived at `agent/opencode-web/testdata/official-1.18.18/samples/e2b-owner-history-reasoning.sanitized.json`; message text, credentials, owner paths, and source IDs are not retained.

| Evidence | Physical fact | Mapping decision |
|---|---|---|
| E2b | `GET /session/:id/message` returns a bare message array whose assistant `parts[]` include `{id,sessionID,messageID,type:"reasoning",text,time:{start,end}}`; two failing sessions each contained two non-empty reasoning parts, alongside text and official UI-skipped `step-start`/`step-finish` parts | persisted reasoning is a supported hydrate fact: preserve part order and map non-empty `text` to the existing canonical `reasoning_delta`/Projection reasoning part; do not fold it into answer text and do not create a second writer |

E2b corrects only HTTP history/hydrate. It does not turn the failed E2 live experiment into a direct-SSE sample and does not authorize guessed `message.part.delta`/`message.part.updated` ordering.

`check_final_provider_evidence.py` derives the provider corrections from raw HTTP/prompt/reload, proves raw/sanitized structural equivalence, and catches fourteen destructive mutations. E2 direct-SSE reasoning remains the sole blocked content transport; E2b independently closes persisted HTTP reasoning for hydrate.

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
| load/reopen message page | §6.3 / C4 | owner acceptance failed; E2b requires persisted-reasoning hydrate repair |
| create and send first/follow-up message | §6.4 / C3 | design-ready including E1/E1b selected variant |
| live stream, retry, abort, reconnect, external turns | §6.5 / C4 | design-ready from A1–A5 plus E3 |
| provider/model/agent selection | §6.6 / C5 | design-ready from E1b/E4b/E5b final mapping |
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

- **Status:** `IMPLEMENTED-AUDIT-PARTIAL`. WP-FIX evidence is now sample-verified at `4a215b0`; C2 remains open only for the strict generation-118 decoder review-fix and its negative tests.
- **User-visible behavior:** the global session list aggregates official directory-scoped root lists for registered project worktrees, hides archived sessions, deduplicates stable IDs, and sorts deterministically. A scoped list stays scoped. By-ID retrieval remains available for archived and mutation-refresh paths. Missing worktrees are hidden only by the global visibility overlay; they are not deleted from server truth.
- **Official UI source:** `packages/app/src/context/global-sync/session-load.ts:5-26` (`roots:true`, bounded `limit`); `packages/app/src/utils/server-compat.ts:170-173` by-ID get; `server-compat.ts:304` project list; `event-reducer.ts:149-161` archived removal.
- **Server/schema source:** `httpapi/groups/session.ts` `ListQuery` and `GET /session/:id`; `handlers/project.ts:15-17`; `project/project.ts:35-56,217,243-244,336`; `packages/core/src/project.ts:105-119`.
- **Same-version samples:** A10 covers root/child, limits, archived by-ID, and directory separation. Corrected WP derives three real `GET /project` responses exclusively from `http[].status/response`, including global `worktree:"/"`, two git-worktree rows, growth, and the deleted-but-still-registered row; six destructive mutations pass.
- **Verified transport shape:** session list is `GET /session?directory=<dir>&roots=true&limit=100`; failure is returned, not retried without `limit`. By-ID is `GET /session/:id` with directory scope. A10 proves archived remains API-visible and by-ID-readable while the Web hides it. `/project` is a bare array of objects; required `id` and `worktree` are non-empty strings, unknown extra fields are allowed, and `worktree:"/"` is the global pseudo-project.
- **Bridge and SSV2 mapping:** list/get/project are catalog/metadata reads only. They do not subscribe to SSE, call `EventPublisher`, ingest Kernel events, or write iOS `messages[]`. OpenCode project registry owns project facts; CordCode's missing-worktree filtering is a documented presentation overlay.
- **Error and unsupported behavior:** project registry failure, any bucket failure, malformed top-level shape, or malformed required row fails the entire operation. Empty registry returns empty catalog. There is no stale/partial/legacy fallback. The generation-118 project decoder must reject an envelope and malformed required rows unless a corrected real sample proves otherwise.
- **Owning tests:** A10 replay; corrected WP checker with destructive mutations against `http[].response`; strict decoder rejects envelope/null/scalar/non-object rows and missing/wrong/empty `id`/`worktree`; roots/limit request-count; archived hidden/by-ID retained; per-worktree aggregation; empty/failure/malformed cases; missing-worktree three-boundary; list/get zero-writer.
- **Out of scope:** pagination capability advertisement, project mutation, children/fork/share, and any timeline construction from list/detail responses.

### 6.3 Load, reopen, and hydrate the message page — C4

- **Status:** `IMPLEMENTED-AUDIT-PARTIAL / OWNER-ACCEPTANCE-FAILED`. Text/tool/file remain sample-verified by A1/A2/A4/A5/A6/A7/A8/A9. E2b sample-verifies persisted reasoning for HTTP hydrate and authorizes directive-014 repair; E2 direct-SSE reasoning remains `BLOCKED/UNSUPPORTED` until separately captured.
- **User-visible behavior:** opening or reopening a session presents the server-persisted message history once, preserves roles/parts/errors/terminal state, and converges with live events without replacing or duplicating an active projection.
- **Official UI source:** `packages/app/src/context/server-session.ts:568` calls `client.session.messages({sessionID,limit,before})`; `packages/app/src/context/global-sync/event-reducer.ts` applies message/session live facts.
- **Server/schema source:** `packages/opencode/src/server/routes/instance/httpapi/groups/session.ts:43-48,85-86,179-190` defines `MessagesQuery` and `GET /session/:sessionID/message`; message/part schemas live under `packages/opencode/src/session/`.
- **Same-version samples:** A1/A2 healthy persisted user/assistant order; A4 aborted assistant with `MessageAbortedError`; A5 partial-to-complete reconnect/reload; A6-A8 tool/interactions; A9 persisted text/file/image/file-source/agent-source parts. E2 archives two failed reasoning-stream strategies and therefore remains negative evidence only for live transport. E2b is a sanitized structural dump derived read-only from two owner sessions that both reproduced `projection.hydrate_failed` on OpenCode 1.18.18.
- **Verified transport shape:** persisted parts include text; reasoning `{id,sessionID,messageID,type,text,time:{start,end}}`; file with `mime`, `url`, optional `filename` and optional file `source`; agent with `name` and optional mention `source`; tool state; assistant error/finish facts. Official Web skips `patch`, `step-start`, and `step-finish` from its message store but retains populated reasoning as a first-class part. Direct-SSE reasoning ordering is still unverified.
- **Bridge and SSV2 mapping:** HTTP history enters only a private Kernel hydrate transaction under source cut/fence. Live events arriving during hydrate enter pending-live and commit afterward in order. Push and pull expose the same Kernel head. iOS `ProjectionStore` remains the only `messages[]` writer from t=0.
- **Error and unsupported behavior:** malformed supported parts fail the hydrate instead of being silently dropped. A populated E2b history reasoning part must no longer fail the whole session, be dropped, or be folded into answer text. Direct live reasoning remains explicitly unavailable until its own sample exists; this restriction must not poison an otherwise valid cold hydrate. Loading/empty/failed projection cannot re-enable legacy writers. Aborted/error assistants do not become healthy `finish=stop`.
- **Owning tests:** full A1/A2/A4/A5/A6/A7/A8/A9 history replay; E2b replay through adapter → rich history → private Kernel hydrate → Projection snapshot proving reasoning order/content and one ingest; destructive E2b tests for missing/wrong `text` and identity; regression proving `step-start`/`step-finish` are skipped exactly as official Web; two formerly failing owner session shapes must return a non-error projection. Retain E2 live negative proof and no live-reasoning advertisement. Also retain hydrate pending-live ordering, source cut/fence, push/pull same head, malformed-part failure, iOS writer-seal, and stale-revision rejection.
- **Out of scope:** raw history delivery to iOS, history merge fallback, local similarity dedup, direct-SSE reasoning until separately captured, and patch/snapshot/step parts marked future in Gate B.

### 6.4 Create session and send first/follow-up messages — C3

- **Status:** `SAMPLE-VERIFIED-DESIGN-READY` for create, text, model, agent, selected variant, and existing bridge attachment inputs. E1+E1b close both catalog selection and prompt/persistence.
- **User-visible behavior:** create yields one server session; each send yields one persisted user message with the client-stable ID and one authoritative assistant turn. First and follow-up sends use the same official request semantics. Composer placeholders are visual only.
- **Official UI source:** `server-compat.ts:163-169` create and `server-compat.ts:200-230` `promptAsync`.
- **Server/schema source:** `POST /session`; `POST /session/:id/prompt_async`; `PromptInput` in `session/prompt.ts:1499-1520`, including text/file/agent part unions.
- **Same-version samples:** A1 create + first send; A2 two follow-ups with distinct IDs; A9 supported prompt parts. E1 proves a top-level selected variant is admitted and persisted; E1b proves live catalog keys `high/low`, selected `high`, and omission when unset. Variant is stored per user message; an unset later message does not erase an earlier message's own persisted value.
- **Verified transport shape:** create body is `{}` with directory routing. Prompt body contains a Mac-generated-once stable `messageID`, selected `agent`, validated `model:{providerID,modelID}`, optional top-level `variant`, and parts. Existing bridge attachment `{kind,mime,filename?,base64}` maps to official file part `{type:"file",mime,filename?,url:"data:<mime>;base64,<base64>"}`. A9's file-mention `source` and agent-mention part are captured but not expressible by the current iOS composer and remain excluded rather than conflated with attachments/selected agent.
- **Bridge and SSV2 mapping:** `messageID` is correlation-only. A successful HTTP admission does not write timeline or synthesize a user/assistant message. Authoritative direct SSE enters the single pre-Kernel normalizer and Kernel; iOS composer state never arbitrates projection state.
- **Error and unsupported behavior:** unsupported parts or unavailable selected model/agent/variant fail before any POST. A variant is accepted only when it is one of the selected model's live catalog keys; otherwise zero POST. HTTP 204 means admission only. No local success, placeholder persistence, retry fallback, or legacy send path.
- **Owning tests:** exact method/path/query/body tests for A1/A2/A9/E1/E1b; two-ID persistence correlation; zero-writer on admission; attachment data-URL conversion; unsupported/file-mention/agent-mention zero-network; unavailable model/agent/variant zero-POST; catalog-to-request variant replay; sandbox create/send/reopen.
- **Out of scope:** command/shell/subtask, file mention and agent mention until their source-span input has an approved wire/UI design, vision-provider interpretation beyond A9 persistence, and reasoning output covered by §6.3.

### 6.5 Live stream, terminal state, abort, reconnect, and external turns — C4

- **Status:** `SAMPLE-VERIFIED-DESIGN-READY` for local direct SSE/status/retry/abort/reconnect from A1-A5 and external official-Web turns from E3.
- **User-visible behavior:** text/tool/status stream once; retries and real errors remain visible; abort ends non-successfully; reconnect continues the same turn without duplication; a second official Web client is observed through the same global SSE/Kernel route proven by E3.
- **Official UI source:** `server-sdk.tsx:268-308` global SSE reconnect and `:284` v1 nested-`sync` skip; `event-reducer.ts` session/message/part reducers; `server-compat.ts:197-198` abort.
- **Server/schema source:** `GET /global/event`; `POST /session/:id/abort`; session status/error and message part event schemas.
- **Same-version samples:** A1/A2 healthy busy→idle; A3 retry→terminal API error; A4 abort→`MessageAbortedError`; A5 disconnect→`server.connected`→live delta→idle. E3 proves a second client create/send is visible on global SSE with ten live deltas, terminal idle, and by-ID/message reload convergence; the observer performs no list polling.
- **Verified transport shape:** v1 direct payload is authoritative; nested `sync` is retained as evidence but skipped exactly once before Kernel normalization. A5 proves reconnect is live continuation, not assumed replay. A3 final non-retryable 400 persists assistant error; A4 abort returns `true` but does not imply healthy completion. E3 proves `/global/event` carries other-client session/message/status facts with directory/session identity.
- **Bridge and SSV2 mapping:** replace per-session dedicated SSE ownership with one backend-instance global subscriber. It normalizes each direct event once and routes by `(directory,sessionID)` into the existing registered/subscribed session's one `EventPublisher`/Kernel. If no Kernel/subscription exists, only catalog metadata is refreshed; opening later hydrates server truth—do not create a second hidden timeline or broadcast raw content. Reconnect resumes this same subscriber/Kernel route.
- **Error and unsupported behavior:** unknown event/part shapes fail or remain explicitly unsupported; they are not recursively decoded. A first attempted fix with no symptom change triggers §4.3, not another state-machine guess. External-turn capability remains inactive until the E3 owning replay, subscriber-count, and same-Kernel tests pass in product code.
- **Owning tests:** complete-sequence A1-A5/E3 replays; exactly one global SSE connection per backend instance; external subscribed session streams while unopened session is catalog-only then hydrates; direct+sync anti-double-ingest; retry/error/abort negative terminal; reconnect same-Kernel/epoch/stale rejection; kernel-nil seal. `external_turn_streaming` is removed at the first product commit if these tests are not yet green and restored only in final activation.
- **Out of scope:** raw SSE to iOS, assumed server replay buffer, polling as external-turn parity, inferred idle, and v2 events.

### 6.6 Provider, model, agent, and selected variant — C5

- **Status:** `SAMPLE-VERIFIED-DESIGN-READY`. E1b/E4b/E5b close catalog variants, raw provenance, `/config`, and the provider-default/configured/catalog-first distinction.
- **User-visible behavior:** choices reflect connected providers and real models/agents; send preserves the selected model and agent; fallback distinguishes current choice, agent model, configured default, recent, and connected fallback rather than collapsing them.
- **Official UI source:** `packages/app/src/context/global-sync/bootstrap.ts:229-242` loads providers and `:266-269` agents; `packages/app/src/pages/session/composer/prompt-model-selection.ts:16-40` establishes connected validation and fallback order, while `:79-123` selects variants; `packages/app/src/utils/server-compat.ts:200-230` sends model/agent/variant.
- **Server/schema source:** the 1.18.18 provider/agent HTTP groups (`GET /provider`, `GET /agent`), configuration model source, and `packages/opencode/src/session/prompt.ts:1499-1520` `PromptInput` model/agent/variant fields at commit `2cba7e227d`.
- **Same-version samples:** A1 proves selected model/agent in an admitted prompt. E1/E1b prove selected variant and non-empty model-specific choices. E4/E4b prove raw `/provider` `{all,default,connected}`, connected `localmock`, model rows `alpha/echo/zeta`, and raw/sanitized structural equivalence. E5/E5b prove real `/config.model` valid/invalid/absent inputs, `/provider.default.localmock="zeta"`, catalog-first `alpha`, and the distinct server behavior when a prompt omits model. The latter is not reused as the Web picker's algorithm.
- **Verified transport shape:** `GET /provider` returns `{all:Provider[],default:{[providerID]:modelID},connected:string[]}`. A provider row has `{id,name,source,env,options,models}`; each model is keyed by model ID and may contain `variants:{[variantKey]:object}`. `GET /config` is an object whose optional `model` is a `providerID/modelID` string. Expose only providers named in `connected`; model IDs/context facts and variant keys come from that provider's live model map. Ignore `env`, `options`, credentials, and variant values. E4b's observed catalog order is `alpha,echo,zeta`; provider default is independently `zeta`.
- **Bridge and SSV2 mapping:** catalogs and choices are control plane and never write timeline. Resolve a model in the official Web order: (1) explicit current iOS selection when still connected/catalog-valid; (2) selected agent's configured model when valid; (3) `resolveDefaultModel(providerDefault, config.model)`, where a defined `/provider.default` wins before legacy `/config.model`; (4) recent session model when valid; (5) the default model of the first connected provider, otherwise that provider's first catalog model. E5b therefore resolves `zeta` in valid/invalid/absent modes while provider default exists; `alpha`/`nonexistent` are server omitted-model outcomes, not picker outcomes. Every prompt carries the resolved explicit `{providerID,modelID}`. Variant is an optional model-specific key and is not `reasoningEffort`.
- **Error and unsupported behavior:** strict provider/config decode; no recursive JSON search, fake catalog, cached legacy snapshot, silent server-side selection, or first-connected-as-configured-default shortcut. Each candidate is validated before use and an unavailable candidate advances only to the next documented level; if no connected valid model exists, zero prompt POST. An unavailable agent or unlisted variant also yields zero POST. Capability/UI activation waits for every owning positive and negative test.
- **Owning tests:** E1b/E4b/E5b raw/sanitized checkers and fourteen destructive mutations; strict provider/config decoders; distinct tests for current, agent, provider-default-over-config, config when provider default is absent, recent, provider-default fallback, catalog-first fallback, and no-valid-model zero POST; exact selected model/agent/variant request; catalog refresh control-only; unknown-shape fail-closed.
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
- **Bridge and SSV2 mapping:** translate once to canonical `user_input_requested` / `user_input_resolved` through Kernel. Pending cold reload uses `GET /question` for the original request ID plus the same history transaction for source-proven `messageID` / `callID` / owning turn. A missed terminal is reconciled in place only when this process already owns that interaction lifecycle and A7-shaped history proves answered or rejected. Legacy question presentation is one-way and suppressed for SSV2; it cannot be ingested back or write `messages[]`.
- **Resolved cold-history boundary:** after resolution, `GET /question` is empty and A7 history retains `messageID`, `callID`, and terminal tool state but not the original `que_…` interaction ID. A fresh bridge process therefore must not invent an interaction ID or synthesize a resolved Dock part. It hydrates the evidence-backed tool activity and creates no phantom pending Dock. In-process route/reconnect recovery keeps an already projected interaction resolved in place. Restoring the original structured resolved Dock across a bridge-process restart would require a separately approved durable canonical identity source/protocol change and is not part of C6.
- **Error and unsupported behavior:** do not invent `question_resolved`, an interaction ID, collapse answer groups, reuse permission RPCs, or claim support when reply/reject is unavailable. Terminal lifecycle state must also close the reply mapping: a late/replayed `question.asked` or stale recovery row cannot make an already resolved interaction answerable again.
- **Owning tests:** A7 replay; multi-group body preservation; reply/reject/external resolution/reload; pending fresh-process reload; in-process missed-terminal reconciliation; fresh-process resolved history produces tool activity with zero phantom Dock; late asked/stale recovery after terminal causes zero re-projection and zero second resolve POST; canonical single-ingest; legacy-frame suppression; zero `messages[]` writer; capability truth test.
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

- **Status:** `SAMPLE-VERIFIED-DESIGN-READY` for archive (A10), rename (E6), and delete (E7).
- **User-visible behavior:** rename updates server-owned title; archive disappears from default lists but remains by-ID retrievable; delete disappears and becomes by-ID not found. Metadata refreshes do not manufacture timeline events.
- **Official UI source:** `server-compat.ts:183-185` rename, archive/home reducer behavior, and `server-compat.ts:189-191` delete.
- **Server/schema source:** `PATCH /session/:id` `UpdatePayload.title`; `PATCH /session/:id` `time.archived`; `DELETE /session/:id`; session updated/deleted events.
- **Same-version samples:** A10 proves archive API/list/by-ID behavior. E6 proves `PATCH /session/:id` body `{title}` returns 200 Session.Info with the new title, list/by-ID converge, and missing ID returns 404 `NotFoundError`. E7 proves `DELETE /session/:id` returns 200 `true`, subsequent list excludes it, by-ID and second delete return 404 `NotFoundError`, and no `session.deleted` was observed in the window.
- **Verified transport shape:** archive uses PATCH with `time.archived` and remains present in API list/by-ID while CordCode hides it by OD-1. Rename accepts only non-empty title and consumes the returned Session.Info. Delete success is boolean `true`; completion is confirmed by list absence/by-ID 404, not by an assumed SSE deletion event.
- **Bridge and SSV2 mapping:** HTTP success refreshes catalog metadata only. Rename/archive use the returned/by-ID metadata. Caller-initiated delete removes/invalidate the catalog/session handle only after successful response and confirmed server absence; it does not wait for or invent `session.deleted` and never manufactures a terminal timeline event. External deletion is observed on the next authoritative catalog/detail refresh unless a future real SSE sample proves a live event.
- **Error and unsupported behavior:** failed/malformed mutation surfaces a real error and leaves prior metadata; no optimistic permanent mutation, list-only success, legacy fallback, or capability advertisement before replay and integration proof.
- **Owning tests:** A10/E6/E7 replay; exact method/path/directory/body/response; rename returned/list/by-ID convergence; archive default-list/by-ID rules; delete boolean/list/by-ID/second-delete convergence; explicit assertion that missing `session.deleted` cannot block success or be synthesized; failure preservation; zero timeline writer; truthful capability activation.
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

#### 6.11.1 Exact cross-repository selection mapping (final)

E1b proves non-empty catalog variants and E5b closes configured/default ordering. The concentrated product batch therefore uses this canonical-first additive mapping; the implementation agent does not redesign it:

1. Mac canonical bridge-v1 `list_models` model item gains optional `variants: string[]`, containing only keys observed in that model's live `/provider.all[].models[modelID].variants` object. Empty/absent means no variant selector.
2. Existing `send_message.params.model` gains optional `variant: string` beside `id` and `providerId`. It is not `reasoningEffort`. Unknown/unlisted variants fail before network I/O.
3. The iOS protocol mirror and `BackendModelInfo`/`BackendModelSelection` gain the same optional model-specific variants/selection. The existing model configuration UI may expose the selector only when the selected model declares non-empty variants.
4. The already-canonical `send_message.params.agent` must be passed by `CCCodeBridgeBackendClient`/`CCCodeBridgeClient`; the current drop at the Swift bridge boundary is a bug, not a new protocol field.
5. Mac uses a new optional session-scoped interface named `core.PromptOptionsSender` with `SendWithOptions(prompt, images, files, core.PromptOptions)`; `PromptOptions` contains `Agent`, `ProviderID`, `ModelID`, and `Variant`. `opencode-web` implements it. The handler calls it atomically for that request; other backends retain `AgentSession.Send`. Do not add another agent-global mutable variant/agent selection that can race concurrent sessions.
6. `opencode-web` generates one stable OpenCode `messageID` inside `SendWithOptions`, uses it for the request and correlation, and never asks iOS to become a timeline writer.

This is an additive bridge-v1 revision, not a protocol-major change. Implementation order is mandatory: Mac canonical protocol doc/schema → iOS mirror/models → handler/session implementation → targeted cross-repository tests → final capability/UI activation. No other protocol field or timeline writer is authorized by this batch.

#### 6.11.2 Concentrated implementation contract

The remaining work is one implementation batch, not a chain of owner/supervisor pauses. The developer may use reviewable internal commits and run independent feature waves, then submit one final report:

1. **Foundation wave:** close the C2 strict project decoder audit hole and land the canonical-first additive selection protocol/mirror from §6.11.1.
2. **Turn wave:** implement C3 submission/options, C4 hydrate/live/global-SSE/reconnect, and C5 provider/model/agent/variant selection. C4 and C5 may proceed independently after the shared C3 protocol/session boundary is stable.
3. **Independent surface wave:** implement C6 permission/question/todo and C7 rename/archive/delete without waiting for C4/C5, because their evidence and ownership are independent.
4. **Activation wave:** only after all owning positive/negative/regression tests pass, synchronize truthful capability advertisement, build/install the Mac release, perform required iOS build/install for changed iOS code, and stop for one supervisor audit plus one owner matrix.

Internal failures do not require a supervisor checkpoint unless they hit §4.2/§4.3: evidence contradicts this mapping, a new protocol/product decision is required, a change would add a writer/fallback, or the first fix has no observable effect. In those cases preserve evidence and stop the affected slice; unrelated slices may continue only if they do not share the disputed boundary.

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
