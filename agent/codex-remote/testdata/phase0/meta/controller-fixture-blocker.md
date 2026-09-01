# Controller fixture blocker

Status: **FAIL-BLOCKED (2026-08-28). Stop product implementation.**

Owner parked this path under the original Gate P0. See
`docs/2026-08-28-codex-remote-phase0-fail-blocked.md`.

Do not register a backend. Do not copy `agent/codex-web`. Do not start iOS Phase 3.
Do not synthesize a cursor. Repeating ping/pong live runs will not pass the Gate.

Frozen target still matches:

- ChatGPT Desktop `26.825.32147` / bundle `7303`
- embedded Codex `codex-cli 0.150.0-alpha.12.2`
- controller protocol v3

Proven on the official path (attempt-001…007):

- independent enroll start/finish and refresh
- Computer-tab pairing via localhost one-time form
- current Mac ChatGPT Desktop environment binding (`CODEX_DESKTOP_APP`, online)
- WSS challenge/proof, `initialize`, `initialized`, `thread/list` (5 items)
- Desktop turn while the probe stayed connected: live `thread/status/changed` (8), env/stream match
- probe-only revoke HTTP 204 and post-revoke refresh_start HTTP 403
- unaided probe process exit after cleanup

Blocked:

- `reconnect_handshake` / `x-codex-subscribe-cursor` — absent on initialize, thread/list, active pongs **and** Desktop-turn live envelopes
- `turn/started` / item delta / `turn/completed` as separate methods
- official iOS controller post-connect coexistence / single-owner / HTTP 409

`--require-live` is expected to fail only on `reconnect_handshake`. REST pagination
`cursor` / `nextCursor` / `backwardsCursor` are not reconnect evidence.

Resume only if the official target starts delivering a controller envelope cursor,
or the owner explicitly rewrites Gate P0.
