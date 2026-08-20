# OpenCode 1.18.18 sample inventory (Gate A)

- Date: 2026-08-20
- Runtime: installed `/opt/homebrew/bin/opencode` = **1.18.18**
- Official source: `/Users/jacklee/Projects/opencode` commit **2cba7e227d** (`packages/opencode/package.json` = 1.18.18)
- Capture target: isolated XDG/`HOME` `opencode serve --pure` + local openai-compatible mock
- Forbidden: owner managed serve write (`127.0.0.1:4096`), real provider accounts, guessed JSON, product-code patches in this gate
- Sample pack: `agent/opencode-web/testdata/official-1.18.18/`

Status values:

- **pending**: source cited; live capture not yet written
- **captured**: sanitized request/response/SSE present and replayable
- **blocked**: cannot be captured safely without owner authorization or an official local hook; no guessed fixture
- **out-of-scope**: owner removed the capability from advertisement (none yet)

Bridge mapping in this document is a **decision slot**, not an implementation. Product code stays frozen until Gates A, B, and S exit.

## 0. Official Web transport (shared by all rows)

| Layer | 1.18.18 source |
|---|---|
| v1 Web event subscribe | `packages/app/src/context/server-sdk.tsx` `kind === "v1"` → `eventSdk.global.event()` (legacy envelope has `payload` + `directory`) |
| v1 Web **skips nested sync** | `server-sdk.tsx:284` `if (legacy && event.payload.type === "sync") continue` |
| v1 prompt | `packages/app/src/utils/server-compat.ts:200-230` `legacy().session.promptAsync({ messageID, agent, model, variant, parts })` |
| v1 create | `server-compat.ts:163-169` create reduces to `session.create({ directory })` — no model/agent in body |
| v1 list roots | `packages/app/src/context/global-sync/session-load.ts:19-26` `session.list({ directory, roots: true, limit })` |
| event reducer | `packages/app/src/context/global-sync/event-reducer.ts` `applyDirectoryEvent` / `SESSION_CONTENT_EVENTS` |
| model fallback | `packages/app/src/pages/session/composer/prompt-model-selection.ts:39-41` current → agent model → configured default → recent → connected fallback |
| HTTP prompt schema | `packages/opencode/src/session/prompt.ts:1499-1520` `PromptInput`; route `POST /session/:id/prompt_async` in `httpapi/groups/session.ts:96,329-341` |
| HTTP list schema | `httpapi/groups/session.ts:30-38` `ListQuery` (`roots`, `limit`, `directory` via workspace routing) |
| SSE encode | `httpapi/handlers/event.ts` instance `/event`; v1 Web uses **global** `/global/event` |

## 1. P0 scenario matrix

| # | Scenario | Official UI | Official server/schema/reducer | Capture command | Sanitized sample | Capture status | Bridge mapping (decision only) |
|---|---|---|---|---|---|---|---|
| A1 | create + first healthy text | `packages/app/src/utils/server-compat.ts:163-169` create; `packages/app/src/utils/server-compat.ts:200-230` `promptAsync` with client `messageID` + text part | `packages/opencode/src/server/routes/instance/httpapi/handlers/session.ts` `SessionHttpApi.create` + `promptAsync`; `packages/opencode/src/session/prompt.ts` `PromptInput`; reducer `packages/app/src/context/global-sync/event-reducer.ts` `session.created` / `message.updated` / `message.part.delta` / `session.status` | `harness/capture.py --scenario a1` | `samples/a1-first-healthy-text.sanitized.json` | pending | Invocation must send `messageID` + `agent` + `model{providerID,modelID}` + text part. Observation uses **direct** v1 payload; nested `sync` is ignored by official Web on 1.18.18. Terminal = captured idle/error, never inferred. Direct vs nested `sync` is pre-Kernel adapter normalization, not an iOS writer. |
| A2 | follow-up | `packages/app/src/utils/server-compat.ts:200-230` same `promptAsync` path; optimistic echo is client-side (`promptAsync` returns synthetic `{id, sessionID, type:"user"}` then SSE reconciles) | `packages/opencode/src/session/prompt.ts` `PromptInput`; persisted user message must reuse client `messageID` | `harness/capture.py --scenario a2` | `samples/a2-follow-up.sanitized.json` | pending | One user action → one persisted user message. Follow-up must include the same agent/model/variant fields as Web. Stable `messageID` is authoritative correlation only; it must not create an iOS optimistic timeline writer. |
| A3 | provider rejection/retry | `packages/app/src/context/global-sync/event-reducer.ts` renders `session.status` retry + assistant `info.error` via `session.status` / `message.updated` | `packages/opencode/src/server/routes/instance/httpapi/handlers/session.ts:316-323` `promptAsync` fork logs `prompt_async failed` then `Session.Event.Error`; status machine in `packages/opencode/src/session/status.ts` | `harness/capture.py --scenario a3` | `samples/a3-provider-error.sanitized.json` | pending | Retry is not idle. Final error carrier is whatever the live sequence shows (`session.error` and/or assistant `info.error`). Do not treat empty idle as healthy. Invalid-model HTTP 400 is `negative-schema-sample` only. |
| A4 | abort | `packages/app/src/utils/server-compat.ts:197-198` `legacy().session.abort` via `session.interrupt` | `POST /session/:id/abort` (`packages/opencode/src/server/routes/instance/httpapi/groups/session.ts` `SessionPaths.abort`); `packages/opencode/src/session/prompt.ts` `SessionPrompt.cancel` | `harness/capture.py --scenario a4` | `samples/a4-abort.sanitized.json` | pending | Abort must record busy → abort HTTP → resulting status/events → persisted messages. Do not synthesize a completed assistant. |
| A5 | SSE disconnect/reconnect | `packages/app/src/context/server-sdk.tsx:268-308` reconnect loop (`RECONNECT_DELAY_MS`) after stream error; still skips `sync` | v1 Web uses `GET /global/event`; instance encode in `packages/opencode/src/server/routes/instance/httpapi/handlers/event.ts`; no server-side replay buffer assumed until captured | `harness/capture.py --scenario a5` | `samples/a5-sse-reconnect.sanitized.json` | pending | After reconnect, recover from server messages/status then validate/invalidate/rehydrate the same Kernel. Duplicate direct vs `sync` must follow the captured official Web rule (v1: skip `sync`). No history merge or local timer. |
| A6 | permission | `packages/app/src/pages/session/composer/session-permission-dock.tsx`; v1 reply `packages/app/src/utils/server-compat.ts:496-503` `permission.respond({sessionID, permissionID, response})` | Schema `packages/schema/src/v1/permission.ts` (`once` / `always` / `reject`); list `GET /permission`; deprecated session route `POST /session/:id/permissions/:permissionID`; newer `POST /permission/:requestID/reply`; events `permission.asked` / `permission.replied`; reducer cases in `packages/app/src/context/global-sync/event-reducer.ts` | `harness/capture.py --scenario a6` | `samples/a6-permission.sanitized.json` | pending | Drive the **v1 Web path** (session-scoped `respond`) because that is what 1.18.18 Web actually calls. Capture pending GET + asked + reply + resolved + reload. Raw permission control must not write iOS `messages[]`. |
| A7 | question | `packages/app/src/pages/session/composer/session-question-dock.tsx:226-227` `sdk().api.question.reply({ sessionID, requestID, answers })`; v1 `packages/app/src/utils/server-compat.ts:507-515` `answers: string[][]` | Tool `packages/opencode/src/tool/question.ts`; schema `packages/schema/src/v1/question.ts`; `GET /question`; `POST /question/:requestID/reply` body `{answers: string[][]}`; `POST /question/:requestID/reject`; events `question.asked/replied/rejected` | `harness/capture.py --scenario a7` | `samples/a7-question.sanitized.json` | pending | Keep question distinct from permission. Reply must preserve `answers: string[][]` order. No invented resolved event. Canonical path is `user_input_requested/resolved`; legacy question frames must not be delivered to SSV2. |
| A8 | todos | `packages/app/src/pages/session.tsx` loads `sync().session.todo(id)`; dock `packages/app/src/pages/session/composer/session-todo-dock.tsx`; reducer `todo.updated` | Tool `todowrite` `packages/opencode/src/tool/todo.ts` (asks `todowrite` permission then `Todo.update`); persist `packages/opencode/src/session/todo.ts` rows are `{content,status,priority}` **no id**; `GET /session/:id/todo`; event `todo.updated` | `harness/capture.py --scenario a8` | `samples/a8-todos.sanitized.json` | pending | Do not invent a todo `id`. Identity/order must follow captured live items (content+position). SDK-required `id` is **not** live 1.18.18 truth. Todos stay explicit control-plane; do not smuggle into timeline parts. |
| A9 | prompt parts | `packages/app/src/utils/server-compat.ts:207-228` maps text / file (mime+url+filename, optional `source` mention) / agent (`type: agent`, `name`, optional mention `source`) | `packages/opencode/src/session/prompt.ts` `PromptInput.parts` union: TextPartInput / FilePartInput / AgentPartInput / SubtaskPartInput | `harness/capture.py --scenario a9` | `samples/a9-prompt-parts.sanitized.json` | pending | Supported parts only after a live round-trip. Unsupported parts fail closed before POST. Image vs file-mention are different official shapes. |
| A10 | session listing | `packages/app/src/context/global-sync/session-load.ts:5-26` roots + limit; archived sessions dropped from home list by reducer `session.updated` when `time.archived` set (`packages/app/src/context/global-sync/event-reducer.ts:149-161`) | `packages/opencode/src/server/routes/instance/httpapi/groups/session.ts` `ListQuery` `roots/limit/start/search/scope/path`; `GET /session/:id`; `GET /session/:id/children`; archive via `PATCH` `time.archived` | `harness/capture.py --scenario a10` | `samples/a10-session-listing.sanitized.json` | pending | Bridge product rule (not official Web): how to aggregate multiple directories and whether archived rows appear in CordCode list. Must be written, not implied. List/get is catalog/control, not a timeline writer. |

## 2. Shared capture contract

Each captured sample file must contain:

1. `meta.opencodeVersion` = `1.18.18`
2. `meta.sourceCommit` = `2cba7e227d`
3. `meta.scenario` = `A1`…`A10`
4. `source.ui` / `source.server` citations (copied from this table)
5. `http[]` raw method/path/query/headers-names/body/status/response (secrets stripped)
6. `sse[]` raw parsed frames in arrival order, including `payload.type` (and nested `sync` if present, even if Web ignores it)
7. `reload[]` GET after terminal (messages, session, pending permission/question/todo as applicable)
8. `sanitization` note listing replaced value classes (ids, paths, timestamps)

Sanitization keeps **all keys** and part/event **types**. Values replaced: session/message/part/permission/question IDs, absolute directories, timestamps, auth headers.

## 3. Current code vs this inventory (not implementation work)

Checked at HEAD `299337c` against `agent/opencode-web`:

- `session.go` create body is `{}` + directory header — matches official v1 create.
- `session.go` prompt body is **only** `{parts:[{type:text,text}], model:{providerID,modelID}}` — missing official `messageID`, `agent`, `variant`, file/agent parts.
- `events.go` unwraps payload and **ignores `sync`**. Official 1.18.18 Web also skips `sync` (`server-sdk.tsx:284`). The missing evidence is a healthy trace proving direct payloads are sufficient, plus reconnect/duplicate behavior.
- Questions and todos are not advertised. Permissions exist from earlier permlab work but lack a fresh 1.18.18 populated trace in this pack.

## 4. Blockers already known (do not fill with guesses)

| Item | Why it may block | Required to unblock |
|---|---|---|
| A3 full retry ladder | 1.18.18 provider backoff was previously observed at 3/8/16/34/60s. A bounded capture may only get early retry frames. | Either capture the full ladder with a long but finite timeout, or record partial retry + explicit “terminal error not reached in budget”. |
| A7 / A8 | Depend on the mock being able to emit official `question` / `todowrite` tool-call names that the 1.18.18 tool layer accepts. | Live tool-call round trip. If the runtime rejects the mock tool call, mark blocked — do not hand-write the event. |
| A9 image/file bytes | Needs a local file part accepted by `FilePartInput` without a real provider vision call. | Isolated file on the sandbox worktree. If serve requires a vision-capable model, mark blocked. |
| v2 | No live v2 serve. | Separate generation pack. Fail closed. |

## 5. This-round closed loop

1. Freeze this inventory (source citations).
2. Bring up isolated harness (no :4096 writes).
3. Capture A1, then A2, A4, A10, then A6/A3/A5/A7/A8/A9 as the harness allows.
4. Any row still pending after the attempt is **blocked**, not faked.
5. Gate B starts only when every P0 row is `captured` or an owner decision removes it.
