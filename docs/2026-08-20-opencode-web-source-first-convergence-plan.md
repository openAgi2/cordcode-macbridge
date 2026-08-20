# OpenCode Web source-first convergence plan

- Date: 2026-08-20
- Status: **Gate A exited. Gate B exited. Gate S S1/S2 independently audited. S3 documentation in this round. Do not start S4 or Gate C until independent S3 audit. Product-code work remains forbidden.**
- Audit input: [2026-08-20-opencode-web-source-parity-audit.md](2026-08-20-opencode-web-source-parity-audit.md)
- Historical input only: [2026-08-18-opencode-web-backend-design.md](2026-08-18-opencode-web-backend-design.md) and its completion report
- Goal: make `opencode-web` a faithful, bounded adapter of the official OpenCode Web behavior, rather than a cleaned-up copy of the legacy OpenCode backend

## 0. Execution boundary

This document is the implementation contract. The owner has authorized work to resume, beginning with evidence and architecture gates. During Gate A, Gate B, and Gate S, an agent may inspect source, build isolated capture infrastructure, collect/sanitize samples, write capability/SSV2 mappings, and add evidence-validation tooling; it must not modify product code, run write operations against the owner's managed serve, use a real provider account without new authorization, install a product build, or execute the owner test matrix.

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
| A2 | follow-up message | prompt request including messageID/agent/model/variant; optimistic echo reconciliation; terminal state |
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

### 4.1 Mandatory stop lines and method reset

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

### 4.2 First-failed-fix escalation record

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

S1/S2/S3 working proof (documentation only; S4/Gate C not started; product code frozen):

- [2026-08-20-opencode-web-ssv2-impact.md](2026-08-20-opencode-web-ssv2-impact.md)
- [2026-08-20-opencode-web-ssv2-impact.json](2026-08-20-opencode-web-ssv2-impact.json)
- S1/S2 checker: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_s_s1_s2.py`
- S3 checker: `python3 agent/opencode-web/testdata/official-1.18.18/harness/check_gate_s_s3.py`

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

Any violation discovered during Gate C triggers the §4.1 stop line and §4.2 method reset. It may not be “temporarily” bypassed with `.off`, history fallback, raw delivery, local completion timers, or content-similarity reconciliation.

## 6. Gate C — implementation order

After Gate A, Gate B, and Gate S exit, implement in this order.

### C1. Version and transport boundary

- make 1.18.18 the explicit verified adapter;
- quarantine unverified v2 behavior from normal selection and capability claims;
- remove unknown-shape recursive fallbacks;
- preserve Basic Auth, directory scoping, timeouts, SSE no-lifetime timeout, and bounded reconnect.

Acceptance: probe and error states distinguish supported, unauthorized, unreachable, and unsupported version.

### C2. Official list/get semantics

- reproduce root-session and limit semantics from official Web;
- define multi-directory aggregation and archive filtering as a bridge product rule;
- retain by-ID get for mutation refresh and archived sessions;
- do not use filesystem existence as a silent substitute for server/project truth without a documented product rule.

Acceptance: boundary fixtures cover root/child, exactly-at-limit, over-limit, archived, missing worktree, and direct by-ID retrieval.

### C3. First message and follow-up submission

- carry a stable message ID;
- carry selected agent, `{providerID,modelID}`, and variant;
- translate all supported request parts from iOS through bridge-v1;
- make unsupported parts fail before network I/O with a capability-consistent error;
- correlate the request and authoritative projection with a stable message ID so one user action produces one persisted user message and one turn; any local composer placeholder remains presentation-only and may not write or arbitrate the active timeline.

Acceptance: A1/A2/A9 replay tests plus a real sandbox create/send/reopen cycle. “HTTP 204” alone is not success.

### C4. Event reducer

- derive the event inventory from official reducer/schema source;
- apply the verified v1 direct-versus-`sync` rule at the source-adapter normalization boundary so one semantic event enters the Kernel at most once; do not create a consumer-side referee;
- replace inferred lifecycle comments with captured ordering;
- preserve text/reasoning/tool distinctions and server errors;
- recover active turns by validating/invalidating/rehydrating the same Kernel and resuming projection delivery; server reads may supply hydrate facts but must not bypass the Kernel or synthesize a healthy terminal.

Acceptance: deterministic replay of A1–A5, including duplicates, reconnect, provider error, and abort.

### C5. Model and agent semantics

- implement or explicitly exclude each official fallback level: current choice, agent model, configured default, recent, connected fallback;
- carry agent selection end to end instead of dropping it at the bridge boundary;
- use only connected providers and real catalog windows;
- remove legacy recursive catalog parsing.

Acceptance: each fallback level has a distinct fixture and selected request assertion; unavailable choices cause zero prompt POSTs.

### C6. Permissions, questions, and todos

- implement only from A6–A8 samples;
- keep permission and question as separate bridge semantics;
- answer multi-question batches without collapsing IDs or inventing resolution events;
- resolve the missing todo ID problem explicitly before publishing stable control-plane todo items; do not place todos into timeline projection without a separate approved projection-shape decision.

Acceptance: request, answer/reject, external answer, reconnect, and cold-reload tests pass; capability flags match actual support.

### C7. Session mutations and selected secondary features

- add verified rename, archive, and delete behavior with by-ID refresh/list invalidation;
- select fork/share/summarize/revert/diff/file/VCS work only from the Gate B product map;
- each feature repeats the source → sample → bridge mapping → test chain.

Acceptance: no generic “API coverage” milestone; each capability has its own evidence packet and truthful wire advertisement.

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
