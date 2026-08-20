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
| A1 | create + first healthy text | `packages/app/src/utils/server-compat.ts:163-169` create; `packages/app/src/utils/server-compat.ts:200-230` `promptAsync` with client `messageID` + text part | `packages/opencode/src/server/routes/instance/httpapi/handlers/session.ts` `SessionHttpApi.create` + `promptAsync`; `packages/opencode/src/session/prompt.ts` `PromptInput`; reducer `packages/app/src/context/global-sync/event-reducer.ts` `session.created` / `message.updated` / `message.part.delta` / `session.status` | `harness/capture.py --scenario a1` | `samples/a1-first-healthy-text.sanitized.json` | captured | Invocation must send `messageID` + `agent` + `model{providerID,modelID}` + text part. Observation uses **direct** v1 payload; nested `sync` is ignored by official Web on 1.18.18. Terminal = captured idle/error, never inferred. Direct vs nested `sync` is pre-Kernel adapter normalization, not an iOS writer. Variant omitted when unset, matching official JSON omit. |
| A2 | follow-up | `packages/app/src/utils/server-compat.ts:200-230` same `promptAsync` path; optimistic echo is client-side (`promptAsync` returns synthetic `{id, sessionID, type:"user"}` then SSE reconciles) | `packages/opencode/src/session/prompt.ts` `PromptInput`; persisted user message must reuse client `messageID` | `harness/capture.py --scenario a2` | `samples/a2-follow-up.sanitized.json` | captured | Stable `messageID` correlation only. Official Web optimistic echo is client-local UI, not a server event, and must not become an iOS second writer. Nested `sync` is evidence-only. |
| A3 | provider rejection/retry | `packages/app/src/context/global-sync/event-reducer.ts` renders `session.status` retry + assistant `info.error` via `session.status` / `message.updated` | `packages/opencode/src/server/routes/instance/httpapi/handlers/session.ts:316-323` `promptAsync` fork logs `prompt_async failed` then `Session.Event.Error`; status machine in `packages/opencode/src/session/status.ts`; retry policy `packages/opencode/src/session/retry.ts` | `harness/capture.py --scenario a3` | `samples/a3-provider-error.sanitized.json` | captured | Mock: two HTTP 500 then one HTTP 400. Live seq busy → retry → busy → retry → busy → idle. Assistant persists `APIError` statusCode 400 isRetryable false. Invalid-model 400 is still not this sample. |
| A4 | abort | `packages/app/src/utils/server-compat.ts:197-198` `legacy().session.abort` via `session.interrupt` | `POST /session/:id/abort` (`packages/opencode/src/server/routes/instance/httpapi/groups/session.ts` `SessionPaths.abort`); `packages/opencode/src/session/prompt.ts` `SessionPrompt.cancel` | `harness/capture.py --scenario a4` | `samples/a4-abort.sanitized.json` | captured | Live 1.18.18: abort HTTP 200 true; assistant persists with `MessageAbortedError`/`Aborted`, finish unset, partial text; `session.error` then idle; `/session/status` no longer lists the session. Do not synthesize a healthy completed assistant. |
| A5 | SSE disconnect/reconnect | `packages/app/src/context/server-sdk.tsx:268-308` reconnect loop (`RECONNECT_DELAY_MS`) after stream error; still skips `sync` | v1 Web uses `GET /global/event`; instance encode in `packages/opencode/src/server/routes/instance/httpapi/handlers/event.ts`; no server-side replay buffer assumed until captured | `harness/capture.py --scenario a5` | `samples/a5-sse-reconnect.sanitized.json` | captured | Disconnect during busy+partial; `/session/status` still busy. Second SSE first frames: `server.connected` then live `message.part.delta` (continuation, not a snapshot replay). Terminal idle; reload assistant finish=stop. Nested sync evidence-only. |
| A6 | permission | `packages/app/src/pages/session/composer/session-permission-dock.tsx`; v1 reply `packages/app/src/utils/server-compat.ts:496-503` `permission.respond({sessionID, permissionID, response})` | Schema `packages/schema/src/v1/permission.ts` (`once` / `always` / `reject`); list `GET /permission`; deprecated session route `POST /session/:id/permissions/:permissionID`; newer `POST /permission/:requestID/reply`; events `permission.asked` / `permission.replied`; reducer cases in `packages/app/src/context/global-sync/event-reducer.ts` | `harness/capture.py --scenario a6` | `samples/a6-permission.sanitized.json` | captured | Live 1.18.18 `external_directory` request keys: id, sessionID, permission, patterns, metadata{filepath,parentDir}, always, tool. once/always/reject all asked. always then same pattern `askedAgain=false`. reject leaves assistant finish=tool-calls, not a healthy stop. |
| A7 | question | `packages/app/src/pages/session/composer/session-question-dock.tsx:226-227` `sdk().api.question.reply({ sessionID, requestID, answers })`; v1 `packages/app/src/utils/server-compat.ts:507-515` `answers: string[][]` | Tool `packages/opencode/src/tool/question.ts`; schema `packages/schema/src/v1/question.ts`; `GET /question`; `POST /question/:requestID/reply` body `{answers: string[][]}`; `POST /question/:requestID/reject`; events `question.asked/replied/rejected` | `harness/capture.py --scenario a7` | `samples/a7-question.sanitized.json` | captured | Request keys id/sessionID/questions/tool. Reply `{answers:[["red"]]}` then `question.replied`. Reject then `question.rejected`. No `question_resolved`. Distinct from permission. |
| A8 | todos | `packages/app/src/pages/session.tsx` loads `sync().session.todo(id)`; dock `packages/app/src/pages/session/composer/session-todo-dock.tsx`; reducer `todo.updated` | Tool `todowrite` `packages/opencode/src/tool/todo.ts` (asks `todowrite` permission then `Todo.update`); persist `packages/opencode/src/session/todo.ts` rows are `{content,status,priority}` **no id**; `GET /session/:id/todo`; event `todo.updated` | `harness/capture.py --scenario a8` | `samples/a8-todos.sanitized.json` | captured | Live items keys exactly content/priority/status, no id. Second todowrite replaces statuses pending/in_progress → completed. Control-plane only; stable identity is Gate B. |
| A9 | prompt parts | `packages/app/src/utils/server-compat.ts:207-228` maps text / file (mime+url+filename, optional `source` mention) / agent (`type: agent`, `name`, optional mention `source`) | `packages/opencode/src/session/prompt.ts` `PromptInput.parts` union: TextPartInput / FilePartInput / AgentPartInput / SubtaskPartInput | `harness/capture.py --scenario a9` | `samples/a9-prompt-parts.sanitized.json` | captured | prompt_async 204 for text, file, file-mention, image/png, and agent; persisted user parts keep those types (file mention and agent keep `source`). Provider mock received **text-only** OpenAI messages (`hasImage=false`, `hasFile=false`) — persist does not require a vision provider. |
| A10 | session listing | `packages/app/src/context/global-sync/session-load.ts:5-26` roots + limit; archived sessions dropped from home list by reducer `session.updated` when `time.archived` set (`packages/app/src/context/global-sync/event-reducer.ts:149-161`) | `packages/opencode/src/server/routes/instance/httpapi/groups/session.ts` `ListQuery` `roots/limit/start/search/scope/path`; `GET /session/:id`; `GET /session/:id/children`; archive via `PATCH` `time.archived` | `harness/capture.py --scenario a10` | `samples/a10-session-listing.sanitized.json` | captured | API `roots=true` omits children; archived rows remain in list and remain GET-by-id; two directories do not leak ids. Official Web UI hides archived via reducer. CordCode multi-directory aggregation and archive visibility remain Gate B. |

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
- Questions and todos now have 1.18.18 populated traces (`A7`, `A8`). Permissions have a fresh once/always/reject trace (`A6`). Product advertisement is still Gate B/C; this pack is evidence only.

## 4. Blockers already known (do not fill with guesses)

| Item | Why it may block | Required to unblock |
|---|---|---|
| A3 full retry ladder | Resolved in this pack: two target-session `retry` then terminal `APIError` 400 `isRetryable=false`. Longer 3/8/16s backoff is not required once the non-retryable terminal is captured. | — |
| A7 / A8 | Resolved: localmock `question` / `todowrite` tool names are accepted by 1.18.18. A8 item shape is `{content,status,priority}` with no `id`. | — |
| A9 image/file bytes | Resolved for persist: `FilePartInput` accepted without a vision model. Mock conversion is text-only (`hasImage=false`). | A vision-faithful provider round trip is not in this pack; persist evidence is sufficient for Gate A. |
| v2 | No live v2 serve. | Separate generation pack. Fail closed. |

## 5. This-round closed loop

1. Freeze this inventory (source citations).
2. Bring up isolated harness (no :4096 writes).
3. Capture A1–A10 in isolated sandboxes. Independent checkers derive from `http`/`sse`/`reload` (A5 uses `sseBefore`/`sseAfterReconnect`); `meta.captureStatus` is never evidence.
4. Any row still pending after the attempt is **blocked**, not faked. This pack: A1–A10 `captured`, none `blocked`.
5. Gate B starts only after owner review of this pack. Do not enter Gate B/S/C from this capture round.
