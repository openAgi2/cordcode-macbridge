# codex-remote Phase 0 evidence probe

Status: **Phase 0 only.** Live turn stream proven after `thread/resume` (attempt-008). Cursor reconnect still unproven. **product backend not registered.** Original cursor **Gate P0 not passed.** Do not continue Phase 1 until the owner explicitly reopens it.

Stop / resume document: `docs/2026-08-28-codex-remote-phase0-fail-blocked.md`.

This directory is the sole implementation location for the codex-remote plan. It currently contains only bounded evidence probes, redacted fixtures, metadata and validators. Official Remote Control accepts an independent controller. After `thread/resume` on in-memory Desktop threads, a real Desktop turn delivered `turn/started` / item deltas / `turn/completed`. It still does not deliver a controller reconnect cursor. Future agents must not expose a bridge backend, copy `agent/codex-web`, modify ChatGPT Desktop, or connect the iOS product surface unless the owner explicitly starts Phase 1.

## Required real chain

The only acceptable product topology is:

```text
ChatGPT Desktop private app-server
  → OpenAI Remote Control relay
  → independently enrolled MacBridge controller
  → ordinary app-server JSON-RPC stream
```

Standalone app-server, fake relay, rollout/JSONL/SQLite/file tail, same-account history polling and cached snapshots cannot prove this chain.

## Gate P0 ledger

| # | Requirement | Current state |
|---|---|---|
| 1 | Freeze current Desktop/App/embedded Codex/upstream source and version drift | STATIC SOURCE BASELINE VERIFIED; binary behavior fixture still pending |
| 2 | Index host/server and both `/codex` plus `/wham` controller call-site families | STATIC CALL SITES VERIFIED; resolved origins and live shapes pending |
| 3 | Real redacted enroll/refresh, WSS challenge/envelope, environment binding and cursor fixtures | LIVE PARTIAL: enroll/refresh/WSS/env binding proven; reconnect cursor still absent after resume live stream |
| 4 | Temporary device key and independently revocable controller enrollment | LIVE: probe key create/sign/delete + enroll; self-revoke 204 |
| 5 | List paired environments and explicitly select the current Desktop | LIVE: Computer-tab pair then select `CODEX_DESKTOP_APP` |
| 6 | Prove a uniquely marked thread/turn belongs to that Desktop's private app-server | LIVE PARTIAL: attempt-008 delivered a real turn/item stream on the selected env/stream after resuming 4 loaded threads; the probe did not stamp a unique marker |
| 7 | Test coexistence with the official ChatGPT iOS controller, including 409/kick/recovery | UNVERIFIED; probe never auto-revoked the iOS controller |
| 8 | WSS app-server `initialize` / `initialized` | LIVE: attempt-006/007 |
| 9 | Real `thread/list` / `thread/read` | LIVE PARTIAL: `thread/list` 5 items and `thread/loaded/list` 4 ids; `thread/resume(excludeTurns)` ×4; `thread/read` unverified |
| 10 | Real live `turn/started` + multiple deltas + one completion | LIVE: attempt-008 `turn/started` ×1, `item/agentMessage/delta` ×36, `item/completed` ×2, `turn/completed` ×1 |
| 11 | Interrupt the same active turn and observe one official terminal state | UNVERIFIED |
| 12 | Network loss/reconnect with seq/ACK/cursor and cold reconciliation | FAIL-BLOCKED: no controller reconnect cursor on any observed envelope |
| 13 | Revoke this controller; old identity fails and official pairings remain intact | LIVE PARTIAL: probe-only DELETE 204 then refresh/start 403; Desktop “Unknown computer” disappears after revoke as designed |
| 14 | Secret scan all fixtures and logs | LIVE artifacts scanned; `gitleaks` PASS |

Owner-authorized resume-live-stream question is **PASS**. Original cursor-reconnect Gate remains **FAIL-BLOCKED**. Do not treat remaining UNVERIFIED rows, or this live stream, as a reason to start the product backend without an explicit Phase 1 go-ahead.

The following requirements are `BLOCKED`:

- persisting raw Desktop or Codex tokens;
- modifying or resigning the ChatGPT App;
- intercepting system TLS or DNS;
- injecting or hijacking a process;
- proxying all ChatGPT traffic.

## Directory contract

```text
probe/                  bounded non-product probe source and operator entry points
testdata/phase0/        redacted fixtures, immutable metadata and evidence logs
validate/               offline/static validators and fixture replay checks
```

No Phase 0 file may call `core.RegisterAgent`, add a driver to runtime defaults, import or wrap `agent/codex` or `agent/codex-web`, or share their lifecycle/ownership state.

## Safety contract for every live probe

Before a live network or credential action exists, its operator path must enforce all of the following:

1. explicit target App/version and expected official HTTPS/WSS origins;
2. a bounded overall timeout and bounded queues;
3. an ephemeral controller identity created for this probe only;
4. no token, pairing code, MFA/step-up payload, private key, raw app-server payload or user content in stdout/stderr;
5. redaction before writing any fixture or timeline;
6. an explicit revoke/cleanup path that removes only the probe's controller identity;
7. fail-closed handling for unknown schema, account/workspace mismatch, target mismatch, 409/single-owner, revoke and protocol drift;
8. no automatic disconnect, takeover or revocation of the official ChatGPT iOS controller.

If cleanup cannot be proven, stop and preserve the failure state; do not manufacture a success path.

## Frozen-directory regression

Every Phase 0–Phase 4 unit runs:

```bash
git diff --exit-code -- agent/codex-web agent/codex
```

The delivery diff is also checked from the frozen start commit `224a632e032aea913c78223b7d2231ffa78f39db`. Any change under either directory blocks the unit.

## Current evidence

- `testdata/phase0/meta/source-baseline.md`: human-readable source and binary freeze.
- `testdata/phase0/meta/source-baseline.json`: machine-readable immutable metadata.
- `validate/source-baseline.mjs`: replays the static/source checks against the installed target.
- `testdata/phase0/static-26.825.32147-alpha.12.2/controller-call-sites.json`: exact target controller call-site contract; static only.
- `testdata/phase0/live-fixture-contract.json`: fail-closed redaction and observation requirements for the missing real fixture.
- `probe/preflight.mjs`: non-mutating target/auth-status/addon/callback-port readiness check.
- `testdata/phase0/meta/controller-fixture-blocker.md`: current external authorization boundary.

Static evidence never substitutes for a real relay/controller fixture or owner-required ChatGPT iOS interaction.
