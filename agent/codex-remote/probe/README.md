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

Run only after explicit owner authorization:

```bash
node agent/codex-remote/probe/live_controller.mjs
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
