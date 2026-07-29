# Session Loading Phase 0 Baseline

> Date: 2026-06-13  
> Status: Phase 0 complete  
> Plan: `docs/2026-06-13-session-loading-systemic-redesign.md`

## Environment

- macOS host, Apple Silicon
- Go Bridge real local transcript data
- Codex: 536 JSONL files discovered, 534 sessions returned, 1,279,686,117 bytes total
- Claude Code: 71 JSONL files returned, 59,802,036 bytes total
- iOS build installed on connected iPhone 16 Pro
- iOS history request limit: 200 logical messages
- Five measured runs per cold/hot scenario and route

Cold list means a new agent and empty process-local list cache. Hot list means the
same agent after one warm-up request. This does not purge the macOS filesystem
cache. History currently has no transcript cache, so cold/hot labels measure
repeatability rather than a parser cache hit.

## Commands

```bash
CCCODE_SESSION_LOADING_BASELINE=1 \
CCCODE_SESSION_LOADING_BASELINE_OUTPUT=.exec-plan/artifacts/session-loading/phase0/real-dataset-metrics.jsonl \
go test ./go-bridge -run '^TestSessionLoadingRealDatasetBaseline$' -count=1 -v
```

The direct route uses a real local WebSocket pair and the production handler.
The relay route uses the production `RelayDeviceConn`, including JSON encoding,
envelope construction, encryption, and send callback. Public Relay network
latency is not included.

Raw evidence:

- `.exec-plan/artifacts/session-loading/phase0/real-dataset-metrics.jsonl`
- `.exec-plan/artifacts/session-loading/phase0/real-dataset-metrics.tsv`
- `.exec-plan/artifacts/session-loading/phase0/real-dataset-summary.tsv`
- `.exec-plan/artifacts/session-loading/phase0/real-dataset-run.log`

## Selected Sessions

| Backend | Class | File bytes | Returned messages | Response bytes |
| --- | ---: | ---: | ---: | ---: |
| Codex | small | 65,604 | 1 | 394-395 |
| Codex | large | 47,825,232 | 51 | 12,453,341-12,453,343 |
| Claude Code | small | 86,591 | 9 | 64,067-64,069 |
| Claude Code | large | 6,139,357 | 200 | 1,142,855-1,142,857 |

## Results

Values are five-run means in milliseconds.

| Route | Backend | Scenario | Cold total | Hot total | Parse | Encode | Send |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| direct | Codex | list | 727.8 | 4.0 | 723.8 cold | 0.5 | 0.4 |
| relay | Codex | list | 732.1 | 6.9 | 727.6 cold | 0.5 | 0.9 |
| direct | Claude Code | list | 117.2 | 115.6 | 114.3-115.8 | 0.1 | 0.1 |
| relay | Claude Code | list | 125.7 | 119.0 | 117.6-124.1 | 0.1 | 0.1 |
| direct | Codex | large history | 324.4 | 312.3 | 267.5-278.7 | 17.5-17.7 | 18.5-18.9 |
| relay | Codex | large history | 335.9 | 331.6 | 268.0-269.3 | 18.1-18.7 | 35.8-36.2 |
| direct | Claude Code | large history | 65.7 | 66.6 | 60.7-61.4 | 2.3-2.4 | 2.2-2.4 |
| relay | Claude Code | large history | 69.2 | 67.2 | 60.8-63.1 | 2.1-2.6 | 3.3-3.5 |

No measured Bridge request returned an error.

## Findings

1. Claude Code list requests reparse all 59.8MB of transcript data on every
   request. Hot latency is unchanged at about 116-119ms. The global metadata
   index is therefore a measured Phase 1 go.
2. Codex list caching works. Cold parsing is about 724ms; hot requests are
   4-7ms. During the run, two active files were usually marked changed, so
   `cache_hit` remained false even though only those files were reparsed.
3. A one-second fingerprint sample confirmed the current Codex rollout file was
   actively appended. This is expected invalidation, not a global cache miss.
   Evidence: `.exec-plan/artifacts/session-loading/phase0/codex-changing-files.txt`.
4. The large Codex response is 12.45MB even though only 51 logical messages are
   returned. Full transcript parsing, response materialization, and transport
   all remain proportional to the whole history representation.
5. The large Claude response is 1.14MB for the 200-message limit. The parser
   still scans the complete 6.14MB file on every request.
6. Relay encryption/send overhead grows with payload size: about 36ms for the
   12.45MB Codex response versus about 3.4ms for the 1.14MB Claude response.
7. No recent local OpenCodeiOS crash or Jetsam report was found in the host
   diagnostic directories. Absence of a synced report is not proof that a
   device crash did not occur.

## Go/No-Go

| Workstream | Decision | Evidence |
| --- | --- | --- |
| A: Claude global list index | GO | Hot list reparses 59.8MB and remains about 116-119ms. |
| B: transcript index and pagination | GO | Existing limit does not bound parsing; large Codex response is 12.45MB. |
| Codex list hardening | LIMITED GO | Keep existing cache; add deterministic ordering and mtime(ns)+size fingerprinting. No index rewrite. |
| C: batched `open_session` | NO-GO | Measured dominant costs are history parsing/materialization/transport, not an extra serial RPC above the 150ms threshold. Re-evaluate after pagination. |

## iOS Device Results

The instrumented build was installed and launched on the connected iPhone 16
Pro. A temporary unauthenticated Bridge on port 18777 was used only for this
local-network diagnostic run because the fresh app container had no saved
pairing credentials.

- Endpoint: `http://192.168.1.3:18777`
- Route: direct LAN
- Register: 117ms on the catalog run, 228ms on the large-session launch
- Codex list RPC wait: 1,159ms
- Codex list decode: 9.0ms
- Codex list mapping: 0.49ms
- Returned sessions: 534
- Large history Bridge time: 336-450ms
- Large history response: 12,453,358 bytes
- Device result: WebSocket close 1009 / `Message too long`
- UI result: repeated history timeout and empty state; the app process remained alive
- Crash/Jetsam result: none observed and no recent synced diagnostic report found

The payload was rejected before iOS decode/model mapping/application, so
`memory_before_mb`, `memory_peak_mb`, `main_actor_apply_ms`, and
`first_content_visible_ms` do not exist for the failed large-session request.
That absence is itself the measured boundary: the current single-frame
protocol cannot deliver this session to the client.

Public Relay was not configured in the fresh app container. Relay payload
encoding/encryption costs were measured through the production
`RelayDeviceConn`; public network latency remains outside this local baseline.

Evidence:

- `.exec-plan/artifacts/session-loading/phase0/ios-build.log`
- `.exec-plan/artifacts/session-loading/phase0/ios-device-build.log`
- `.exec-plan/artifacts/session-loading/phase0/ios-device-install.log`
- `.exec-plan/artifacts/session-loading/phase0/ios-device-console.log`
- `.exec-plan/artifacts/session-loading/phase0/ios-direct-console.log`
- `.exec-plan/artifacts/session-loading/phase0/ios-direct-large-session-console.log`
- `.exec-plan/artifacts/session-loading/phase0/temporary-direct-bridge.log`
- `.exec-plan/artifacts/session-loading/phase0/ios-tests.log`

Phase 0 classifies the reported failure as a transport-size/UI-state failure,
not a process crash. Phase 1 and Phase 2 are approved according to the table
above. Phase 3 remains no-go.
