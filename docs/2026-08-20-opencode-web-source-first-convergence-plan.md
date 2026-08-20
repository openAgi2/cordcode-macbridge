# OpenCode Web source-first convergence plan

- Date: 2026-08-20
- Status: **documentation ready; all product-code implementation is paused until the owner explicitly resumes it**
- Audit input: [2026-08-20-opencode-web-source-parity-audit.md](2026-08-20-opencode-web-source-parity-audit.md)
- Historical input only: [2026-08-18-opencode-web-backend-design.md](2026-08-18-opencode-web-backend-design.md) and its completion report
- Goal: make `opencode-web` a faithful, bounded adapter of the official OpenCode Web behavior, rather than a cleaned-up copy of the legacy OpenCode backend

## 0. Execution boundary

This document is the next implementation contract, but **not an instruction to start coding now**. While paused, an agent may read and audit; it must not modify product code, run write operations against the owner's managed serve, use a real provider account, install a build, or execute the owner test matrix.

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

Gate B exit: there is no endpoint in the official Web inventory whose CordCode disposition is implicit.

## 6. Gate C — implementation order

After the owner resumes coding, implement in this order.

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
- map optimistic echo and SSE reconciliation so one user action produces one persisted user message and one turn.

Acceptance: A1/A2/A9 replay tests plus a real sandbox create/send/reopen cycle. “HTTP 204” alone is not success.

### C4. Event reducer

- derive the event inventory from official reducer/schema source;
- decide how direct payload and nested `sync` events relate and deduplicate by stable IDs/revisions;
- replace inferred lifecycle comments with captured ordering;
- preserve text/reasoning/tool distinctions and server errors;
- recover active turns after reconnect from server state without synthesizing a healthy terminal.

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
- resolve the missing todo ID problem explicitly before projecting stable items.

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
5. MacBridge and iOS targeted tests cover only changed bridge capabilities and projections;
6. owner real-device matrix runs once at the feature gate, not after every small edit.

The test suite must prove negative behavior too: unsupported content fails before POST, unknown versions fail closed, duplicate events do not duplicate messages, and a diagnostic timeout cannot masquerade as an active turn.

No UI tests or simulator automation are authorized by this plan. No unbounded build/test process is permitted. Repository timeout and cleanup rules remain mandatory.

## 8. Release gates

No release or owner matrix begins until:

- Gates A and B are complete;
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
- a completion report links evidence rather than restating claims;
- no mandatory stop-line event remains unresolved, and every failed-fix escalation names its final first divergence;
- no v2 parity is claimed without a separate v2 evidence pack.

Until then, the honest product status is: **OpenCode Web transport exists and several flows work, but source-level parity is incomplete.**
