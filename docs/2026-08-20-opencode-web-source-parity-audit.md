# OpenCode Web source-parity audit

- Date: 2026-08-20
- Scope: `agent/opencode-web`, the official OpenCode Web call chain, and the bridge/iOS surfaces exposed by this backend
- Audited runtime: installed `opencode 1.18.18`
- Audited source: `/Users/jacklee/Projects/opencode` at `2cba7e227d` (`packages/opencode/package.json` = `1.18.18`)
- Status: **audit complete; product-code implementation remains paused by the owner**
- Supersedes as current truth: [2026-08-18-opencode-web-backend-design.md](2026-08-18-opencode-web-backend-design.md)
- Next implementation contract: [2026-08-20-opencode-web-source-first-convergence-plan.md](2026-08-20-opencode-web-source-first-convergence-plan.md)

## 1. Core conclusion

The original architectural sentence was right:

> `opencode-web` is an HTTP/SSE client for the official `opencode serve`, plus a bridge-v1 translator.

The original implementation method was not strict enough. It allowed endpoint code and event/history mapping to be copied from the legacy `agent/opencode` adapter, then treated endpoint reachability and selected happy-path tests as proof of official Web parity. That inverted the required evidence order:

1. official Web UI call chain and official server schemas;
2. a real request/response/event sample from the supported runtime;
3. bridge-v1 product mapping;
4. implementation and tests.

The current backend does use official HTTP/SSE endpoints; it is not secretly falling back to CLI, SQLite, or the old driver. The remaining problem is **semantic parity and coverage**, not transport purity. The largest gaps are:

- first-message submission omits official fields and content types;
- model choice implements only part of the official fallback chain;
- session listing does not reproduce the official root/limit behavior;
- SSE decoding was copied from the old subscriber and ignores the `sync` envelope;
- questions and todos are absent; several official session/file/diff operations are not mapped;
- some comments and tests assert event ordering that a fresh sandbox did not reproduce;
- the v2 path is claimed but has no current live sample set.

Source parity does not authorize a new client data path. OpenCode serve is the upstream fact source, while the Mac `ProjectionKernel` remains CordCode's only active timeline truth and iOS `ProjectionStore` remains the only active client writer under negotiated `session_sync_v2`. Raw SSE, HTTP history, reconnect reads, and the official Web reducer may inform the source adapter or Kernel hydrate; none may write the active iOS timeline directly. The mandatory ownership, transaction-domain, control-plane, and anti-double-write proof is Gate S of the convergence plan.

This explains why basic create-and-send required repeated repair during `opencode-web`, while `dsh-web` was more stable: the latter plan made the official Web protocol the implementation boundary before coding; the former plan used the legacy adapter as a shortcut for several content shapes.

## 2. Evidence rules used in this audit

No request, response, or event content shape is marked verified from memory or analogy. A green shape below has a fresh 2026-08-20 sample from either:

- the owner's managed serve at `127.0.0.1:4096` using read-only requests; or
- an isolated `opencode serve --pure` sandbox on port 4397 using its own XDG directories and Basic Auth.

The sandbox was stopped and its temporary directory moved to Trash after capture. Credentials, real project paths, session IDs, message text, and provider secrets are not reproduced here. Sanitized samples retain all shape-relevant keys.

For nested message and provider data, two independent extraction strategies were used. Differences are recorded in §5 rather than normalized away.

## 3. Fresh sample ledger

### 3.1 Managed serve, read-only

| Surface | Fresh observed shape | Result |
|---|---|---|
| health | `{ "healthy": true, "version": "1.18.18" }` | verified |
| providers | top-level `all`, `connected`, `default`; provider keys include `id`, `models`, `name`, `source`; model keys include `id`, `providerID`, `limit.context`, `limit.output` | verified |
| projects | array; item keys include `id`, `worktree`, `sandboxes`, `time`, `vcs` | verified |
| agents | array; item keys include `name`, `mode`, `native`, `description`, `options`, `permission`; `model` was absent/null in the sampled entries | verified for the observed catalog |
| session status | object keyed by session ID; sample was empty | container verified; non-idle member shape not freshly populated |
| session list | directory query and `x-opencode-directory` returned the same 100 IDs; item keys include `id`, `projectID`, `directory`, `title`, `slug`, `summary`, `time`, `cost`, `tokens`, `version` | item and directory-scoping shape verified; completeness not verified |
| session get | same session object shape as the list item | verified |
| messages | array of `{info, parts}`; user/assistant key sets and observed part types recorded below | verified for observed types |
| todos | array item keys were exactly `content`, `priority`, `status` in the sample | verified; contradicts the generated SDK type that requires `id` |
| pending permissions | HTTP 200 with `[]` | empty container verified; populated request not freshly sampled |

Observed message skeletons:

```json
{
  "info": {
    "id": "<message-id>",
    "sessionID": "<session-id>",
    "role": "user",
    "agent": "<agent>",
    "model": { "providerID": "<provider>", "modelID": "<model>" },
    "summary": {},
    "time": { "created": 0 }
  },
  "parts": [{ "type": "text", "text": "<redacted>" }]
}
```

```json
{
  "info": {
    "id": "<message-id>",
    "sessionID": "<session-id>",
    "role": "assistant",
    "parentID": "<message-id>",
    "providerID": "<provider>",
    "modelID": "<model>",
    "agent": "<agent>",
    "mode": "<mode>",
    "path": {},
    "finish": "<finish>",
    "cost": 0,
    "tokens": {
      "total": 0,
      "input": 0,
      "output": 0,
      "reasoning": 0,
      "cache": { "read": 0, "write": 0 }
    },
    "time": {}
  },
  "parts": [{ "type": "reasoning|step-start|tool|step-finish" }]
}
```

Observed tool part skeleton:

```json
{
  "type": "tool",
  "id": "<part-id>",
  "messageID": "<message-id>",
  "sessionID": "<session-id>",
  "callID": "<call-id>",
  "tool": "<tool-name>",
  "state": {
    "status": "completed",
    "input": {},
    "output": "<redacted>",
    "metadata": {},
    "title": "<redacted>",
    "time": {}
  }
}
```

Only `completed` tool state was present in the sampled history. Other tool states remain source-backed but not freshly content-sampled.

### 3.2 Isolated sandbox, write operations

| Operation | Request actually exercised | Fresh result |
|---|---|---|
| create | `POST /session?directory=<directory>` with `{}` | 200 session object; no model/agent was needed in the create body |
| invalid prompt | `POST /session/<id>/prompt_async` with text part and `model:{providerID,modelID}` pointing to a nonexistent provider/model | HTTP 400; no messages or busy state created |
| rename | `PATCH /session/<id>?directory=<directory>` with `{"title":"audit-renamed"}` | 200 session; title changed |
| archive | same PATCH with `{"time":{"archived":1787180000123}}` | 200 session; `time.archived` matched |
| list after archive | directory-scoped session list | archived session remained present in this sample |
| delete | `DELETE /session/<id>?directory=<directory>` | HTTP 200 boolean `true`; subsequent get was 404 |

The invalid-model request proves the accepted field names at schema validation, but it does **not** prove the healthy first-turn event sequence. No external provider request was made because this task did not authorize use of a real provider account.

Fresh SSE frames captured while creating the sandbox session:

```json
{
  "directory": "global",
  "project": "global",
  "payload": {
    "type": "session.created",
    "properties": {
      "sessionID": "<session-id>",
      "info": { "id": "<session-id>", "directory": "<directory>" }
    }
  }
}
```

The same creation also appeared inside a separate `payload.type = "sync"` frame whose nested `syncEvent.type` was `session.created.1`. `server.connected`, `server.heartbeat`, and `project.updated` were observed. No creation-time `session.status idle` or `session.idle` was observed during the 30-second capture. Therefore the current `events.go` comment attributing a bare idle race to `POST /session` is not accepted as current truth until a healthy first-turn capture reproduces it.

## 4. Source-to-implementation verification matrix

Status meanings:

- **green**: fresh shape sample and official-source call chain agree;
- **yellow**: part of the surface is verified, but semantics/coverage remain incomplete;
- **red**: current implementation is missing, contradictory, or lacks the sample needed to implement safely.

| Surface | Official source of truth | Fresh sample | Current implementation | Status / required action |
|---|---|---|---|---|
| health/generation | installed serve + `/global/health` | 1.18.18 health shape | probes v1/v2 | green for v1; v2 remains unverified |
| provider catalog | `prompt-model-selection.ts:17-42` | `all/connected/default`; two counts agree | filters `connected` | yellow: remove legacy recursive fallback; model-choice chain is incomplete |
| model selection | prompt current → agent model → config default → recent → connected fallback | provider/agent catalogs sampled | pending → session model → connected default | red: explicitly map or deliberately exclude every official level |
| root session list | `session-load.ts:5-25`; compat `server-compat.ts:140-161` | directory-scoped list capped at 100 in sample | one list per `/project` worktree; no roots/limit/order | red: current list can be incomplete and does not match official root semantics |
| project catalog | `/project` server surface | project shape sampled | treats filesystem-existing worktrees as list universe | yellow: `/project` shape is real; product grouping/filter semantics need a written mapping |
| session detail | official session get | detail sampled | by-id get implemented | green for v1 shape |
| create | official UI `submit.ts:401-433`; v1 compat reduces create to directory-only | `{}` + directory created a session | lazy create uses `{}` + directory | green for v1 request shape; surrounding first-turn orchestration is red |
| first/follow-up prompt | `submit.ts:113-207`; compat `server-compat.ts:200-230` | invalid request validated `providerID/modelID`; healthy sequence not sampled | sends one text part + model only | red: missing messageID, agent, variant, files, agent mentions, optimistic/reconciliation contract |
| history | server messages + official reducer | user/assistant and text/reasoning/step/tool sampled | mapper explicitly copied from legacy and ignores unknown parts | yellow: observed subset works; exhaustive part inventory absent |
| SSE envelope | official event reducer + live SSE | direct payload and nested sync frame both observed | unwraps payload, ignores `sync` | red: determine authoritative/dedup behavior with a healthy trace before changing |
| lifecycle | official reducer handles `session.status`, message events, errors | fresh healthy busy/retry/error/idle sequence not captured | mutable state machine contains locally inferred ordering | red: capture and replay healthy, error, abort, retry, reconnect traces |
| permission | official `permission` service/schema | only empty pending list fresh; older permlab fixture exists | asked/reply implemented | yellow: obtain a fresh populated request/reply/resolved trace before declaring parity |
| question | official `question` service/schema | no fresh populated trace | explicit not-supported | red: missing product capability and mapping |
| todo | official session todo + reducer | real items have no `id` | event ignored; capability absent | red: reconcile live shape versus generated SDK before mapping stable IDs |
| rename | official v1 compat uses session update | fresh PATCH succeeded | no rename capability | red: missing despite verified server support |
| archive | server accepts `time.archived` update | fresh PATCH succeeded; archived remains in list | implemented | yellow: list filtering/presentation semantics must be specified |
| delete | official v1 compat uses session delete | fresh DELETE true + get 404 | implemented | green for HTTP mutation; bridge refresh behavior still needs regression evidence |
| files/agent mentions | official prompt builder and compat mapper | not freshly submitted | files rejected; agent selection discarded before request | red: user-visible official Web behavior missing |
| fork/share/summarize/revert/diff/file/VCS | official SDK/OpenAPI inventory | no fresh write samples | mostly absent | unscoped: classify by CordCode product need before implementation |
| v2 generation | checkout contains v2 APIs | no live v2 serve/sample set | compatibility branches exist | red: quarantine from parity claims until a versioned fixture/sample pack exists |

## 5. Cross-verification notes

### 5.1 Provider catalog

Strategy A filtered every `all[]` provider by membership in `connected`, then counted models. Strategy B iterated `connected` IDs and looked each up in `all`. Both produced 66 connected models during this session. This supports the `connected` filter and rules out the old recursive-all behavior for the current runtime.

### 5.2 Message text

Strategy A counted only parts with `type == "text"` and nonempty `text`: 45. Strategy B counted every part carrying nonempty `text`: 58. The 13 extra values were all `reasoning` parts. This is a semantic distinction, not a parser disagreement: user-visible answer text and reasoning text must remain separate bridge fields.

### 5.3 Session directory scoping

The query parameter and `x-opencode-directory` header returned the same 100 IDs for the sampled directory. This verifies equivalence for that read, not completeness. The official UI asks for roots and a limit, and explicitly tracks whether the result may be limited; current global enumeration does neither.

### 5.4 Todo schema drift

The live 1.18.18 response had no todo `id`, while the generated SDK type expects one. Neither may be silently preferred. A future implementation needs a captured event plus endpoint response and must define whether CordCode can derive a stable identity without inventing storage truth.

## 6. Unverified content types and blocked claims

These are not implementation-ready:

1. healthy first-message sequence from create through user echo, assistant stream, and terminal idle;
2. provider retry and terminal-error sequence on the current binary;
3. abort during an active healthy turn and the terminal event emitted afterward;
4. reconnect while a turn is active, including direct-versus-`sync` duplication;
5. populated permission asked/replied/resolved payloads on the current binary;
6. populated question asked/replied/rejected payloads on the current binary;
7. pending/running tool state shapes beyond the sampled completed state;
8. prompt file/image and `agent` part round trips;
9. todo update events and stable identity semantics;
10. every v2 request, response, and event shape.

The next implementation must use a local deterministic provider harness or another owner-authorized, non-billable fixture to capture items 1–9. Real provider credentials are not an acceptable implicit test dependency. Until then, tests may preserve known samples, but may not fabricate an unseen “official” shape and call it a contract.

## 7. Why the earlier review did not prevent this

The earlier review established useful facts—endpoint existence, transport separation, selected payload corrections, bridge registration, and test coverage—but it accepted three weak substitutes for parity:

- legacy `agent/opencode` code as shape evidence;
- an endpoint returning 2xx as proof that official Web uses it with the same semantics;
- fake-server fixtures written from the design as proof of the design.

Those checks are circular. A test that encodes an inferred event sequence only proves the implementation matches the inference. The replacement plan therefore makes a captured sample pack and an official-source call-chain map prerequisites, not implementation by-products.

## 8. Revision priorities

| Priority | Documentation/implementation prerequisite | Exit evidence |
|---|---|---|
| P0 | freeze the old design as historical; adopt the convergence plan | no agent can mistake v3.1 or its completion report for current truth |
| P0 | build a versioned source/sample matrix for first turn, lifecycle, permissions, questions, todos, and prompt parts | sanitized raw fixtures plus source citations; no guessed keys |
| P0 | define bridge product parity: supported, deliberately unsupported, or not applicable for every official Web surface | owner-reviewable capability matrix with no “later” ambiguity |
| P0 | prove Session Sync v2 ownership before product changes | Gate S records truth owner, only writer, hydrate/live/reconnect paths, explicit control exceptions, and anti-double-write tests |
| P1 | replace copied legacy semantics in list/send/events/history with source-derived mappings | contract tests replay real samples; targeted integration tests pass |
| P1 | repair first-message and follow-up equivalence before broad feature expansion | create/send/reopen/external-turn matrix passes in sandbox and owner environment |
| P2 | add selected mutations and user-facing Web features in dependency order | each capability has source, sample, mapping, test, and truthful advertisement |
| P3 | evaluate v2 only after a real supported v2 runtime is available | separate v2 sample pack and compatibility matrix |
