# Probe entry-point rules

Status: **attempt-008 proven live turn stream after `thread/resume`.** Cursor reconnect remains FAIL-BLOCKED. Do not turn this directory into a product backend unless the owner reopens Phase 1.

This directory contains non-product Phase 0 controller probes only. It does not register a MacBridge backend.

Every future executable added here must document:

- exact evidence question and Gate P0 row;
- target ChatGPT App and embedded Codex versions;
- required human account/pairing action, if any;
- official origin/path allowlist;
- overall and per-operation timeouts;
- redaction boundary and permitted output schema;
- temporary device-key/enrollment lifecycle;
- deterministic revoke/cleanup command;
- expected failure classifications, including HTTP 409/single-owner;
- evidence artifact paths.

A probe must stop before a network mutation unless all automatic preflight checks pass. It must never disconnect or revoke another controller to make its own connection succeed.

## Current probes

`lib/controller_session.mjs` is the shared enrollment/WSS machinery extracted from the proven `live_controller.mjs` flow (that file keeps its own inline copy and stays untouched). It performs the same enroll/start → step-up → enroll/finish → refresh round trip, opens the controller WSS with the device-key challenge flow, exposes a promise-based `rpc()` session, and provides deterministic `cleanupController` (revoke only client_probe → verify rejection → delete probe key → remove the temp helper). New probes import it instead of duplicating the flow.

`preflight.mjs` is the non-mutating entry point. It verifies the exact ASAR contract, embedded helper binaries, device-key addon ABI, ChatGPT login-status availability and the two frozen OAuth callback ports. It does not request a bearer token, create a key, open a browser, contact controller endpoints or change pairing state:

```bash
/Applications/ChatGPT.app/Contents/Resources/cua_node/bin/node \
  agent/codex-remote/probe/preflight.mjs
```

`live_controller.mjs` is the owner-authorized real-account probe for the independent controller path. It:

- obtains the current ChatGPT account credential through the embedded official app-server and retains it only in memory;
- compiles and signs `device_key_helper.swift` in a temporary directory, using an independently named nonextractable login-Keychain P-256 key;
- performs the official enroll start, fresh OAuth step-up and enroll finish flow;
- if pairing is required, opens a one-time localhost form so the owner can enter a fresh Desktop manual pairing code without exposing it to chat, stdout or disk;
- after `initialize` / `initialized` / `thread/list` / `thread/loaded/list`, resumes in-memory or recency-selected threads with `excludeTurns: true` and waits for a Desktop turn;
- does not fail closed on missing envelope cursor; cursor reconnect is skipped unless a later owner-authorized run asks for it;
- stops on any failed request, including HTTP 409, without deleting or disconnecting another controller;
- revokes only the probe controller, deletes the probe key and removes the temporary helper in `finally`.

The target is ChatGPT Desktop `26.825.32147` (bundle `7303`), embedded Codex `0.150.0-alpha.12.2`, controller protocol v3. The allowlisted service is `https://chatgpt.com` and the exact `/backend-api/codex/remote/control/*` plus `/backend-api/wham/remote/control/*` paths frozen in `../testdata/phase0/static-26.825.32147-alpha.12.2/controller-call-sites.json`.

The overall human-input timeout is ten minutes for step-up and ten minutes for pairing; network operations use 15-second timeouts and the WSS initialization uses 30 seconds. Output is restricted to status codes, structural field names, counts, pseudonyms and cleanup results. The operator must not paste an OAuth code or pairing code into chat.

`history_probe.mjs` is the owner-authorized G0 lazy-history probe (read-only against thread history; the owner never needs to send a chat message). Evidence question: plan §3.0.5 nine-item fixture set, §3.0.6 type-grouped stats + summary↔items id mapping, §3.0.7 negative-result assertions, T0.2 bytes/wall-time baselines grouped by `historyMode`, and the T0.6 `thread/resume(excludeTurns=true, initialTurnsPage)` candidate where `thread.turns == []` is mandatory. Calls, all over the same controller WSS: `thread/list` (historyMode inventory), bounded discovery paging, `thread/read` metadata + `includeTurns=true` control inventory (probe-only, never a product path), `thread/turns/list` summary/notLoaded desc chains to EOF plus the asc backwards round-trip anchored on the last desc page's `backwardsCursor`, `thread/items/list` turnId-filtered asc pages on sampled turns plus an illegal-turnId error probe, and the T0.6 resume candidate last (resume attaches live state). Human action: fresh step-up in the opened browser page; pairing code (if required) only through the one-time localhost form. Timeouts: per-RPC 30 s (control full-read 180 s, resume candidate 60 s), WSS idle 120 s, total 20 min; every pagination loop bounded by the CAPS recorded in the fixture. Redaction boundary and output schema: `../testdata/phase0/live-fixture-contract.json` `history_probe` section — pseudonyms `id-N`/`cur-N`, text/paths/error bodies as lengths only, enum values only for whitelisted keys, timestamps as relative offsets. Expected failure classifications: HTTP 409/single-owner stops the run; `thread/items/list` method-not-found on legacy threads is a legitimate §2.5 observation; an rpc error for the illegal turnId is expected; §3.0.7 negative findings are recorded and adjudicated offline. Artifacts: one `REDACTED_FIXTURE=` JSON line on stdout, saved by the operator as `../testdata/phase0/live/attempt-XXX-history-*.json` after the owner attestation and a gitleaks PASS; assertions run offline via `../validate/history-fixture-assert.mjs --fixture <file>` (its `--self-test` must stay green).

Run only after explicit owner authorization:

```bash
node agent/codex-remote/probe/live_controller.mjs
node agent/codex-remote/probe/history_probe.mjs
```

Every attempt revokes its own controller automatically. If a process is interrupted before cleanup can run, use the official Remote Connections UI to revoke the controller named for the Phase 0 probe; never revoke the ChatGPT iOS controller. Evidence artifacts live under `../testdata/phase0/live/` and must pass `../validate/controller-fixtures.mjs` plus the repository gitleaks policy.

## Gate discipline

The frozen call-site contract and live-fixture policy are available under `../testdata/phase0/`. Static preparation and a partial live enrollment observation do not pass Phase 0.

Before requesting owner action, implementation must provide one bounded command that:

1. uses an independently named, OS-protected nonextractable probe key;
2. keeps account, step-up and controller credentials in memory only;
3. allowlists the frozen official origin and exact paths;
4. writes only the redacted schema after secret scanning;
5. stops on 409/single-owner without disconnecting the official mobile controller;
6. exposes a deterministic revoke command for only the probe controller.

Static package inspection cannot establish any of these live properties.
