# codex-remote Phase 0 evidence probe

Status: **Phase 0 only — product backend not registered — Gate P0 not passed**.

This directory is the sole implementation location for the codex-remote plan. During Phase 0 it contains only bounded evidence probes, redacted fixtures, metadata and validators. It must not expose a bridge backend, modify ChatGPT Desktop, alter Codex configuration, or connect the iOS product surface.

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
| 3 | Real redacted enroll/refresh, WSS challenge/envelope, environment binding and cursor fixtures | EVIDENCE-ONLY: exact static contract/preflight pass; real fixture blocked on owner authorization |
| 4 | Temporary device key and independently revocable controller enrollment | UNVERIFIED |
| 5 | List paired environments and explicitly select the current Desktop | UNVERIFIED |
| 6 | Prove a uniquely marked thread/turn belongs to that Desktop's private app-server | UNVERIFIED |
| 7 | Test coexistence with the official ChatGPT iOS controller, including 409/kick/recovery | UNVERIFIED |
| 8 | WSS app-server `initialize` / `initialized` | LIVE: attempt-006 sent both on the selected Desktop stream |
| 9 | Real `thread/list` / `thread/read` | LIVE PARTIAL: attempt-006 `thread/list` returned 5 items; `thread/read` still unverified |
| 10 | Real live `turn/started` + multiple deltas + one completion | LIVE PARTIAL: attempt-007 saw Desktop-turn `thread/status/changed` (8) on the selected stream; `turn/started` / item delta / `turn/completed` not observed; no envelope cursor |
| 11 | Interrupt the same active turn and observe one official terminal state | UNVERIFIED |
| 12 | Network loss/reconnect with seq/ACK/cursor and cold reconciliation | UNVERIFIED |
| 13 | Revoke this controller; old identity fails and official pairings remain intact | UNVERIFIED |
| 14 | Secret scan all fixtures and logs | PARTIAL: current static artifacts pass; live artifacts pending |

Gate P0 passes only when all 14 rows have real, versioned evidence. List/read without live/interrupt is `EVIDENCE-ONLY`. Silent takeover of ChatGPT iOS is `EVIDENCE-ONLY` pending product adjudication.

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
