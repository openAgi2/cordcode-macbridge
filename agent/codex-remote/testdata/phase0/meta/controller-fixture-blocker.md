# Controller fixture blocker

Status: **EVIDENCE-ONLY / FAIL-BLOCKED**

Static preparation for the frozen target still passes. Live redacted observations now exist through attempt-001…005. The remaining Gate P0 hole is not missing enrollment, pairing, initialize, revoke or cleanup; it is the absence of a real `x-codex-subscribe-cursor` value.

Frozen target still matches:

- ChatGPT Desktop `26.825.32147` / bundle `7303`
- embedded Codex `codex-cli 0.150.0-alpha.12.2`
- controller protocol v3

What is proven on the official path:

- independent enroll start/finish and refresh
- Computer-tab pairing via localhost one-time form
- current Mac ChatGPT Desktop environment binding (`CODEX_DESKTOP_APP`, online)
- WSS challenge/proof and ordinary app-server initialize
- `initialized` notification and `thread/list` (5 items) on the same live stream (attempt-006)
- a ChatGPT Desktop turn while the probe stayed connected produced live `thread/status/changed` (attempt-007); no envelope cursor; `turn/started` / `turn/completed` not observed as separate methods
- three bounded active pongs with no envelope cursor, including after thread/list and live Desktop-turn traffic
- probe-only revoke HTTP 204 and post-revoke refresh_start HTTP 403
- probe-key deletion and unaided process exit

What remains blocked:

- `reconnect_handshake` / `x-codex-subscribe-cursor` (absent on initialize, thread/list and pongs)
- `thread/read`, Desktop live turn, interrupt, and official iOS coexistence / single-owner semantics

`--require-live` is expected to fail only on `reconnect_handshake`. Do not synthesize a cursor. Do not start product backend registration, `agent/codex-web` / `agent/codex` edits, or iOS Phase 3 wiring while Gate P0 is `FAIL-BLOCKED`.
