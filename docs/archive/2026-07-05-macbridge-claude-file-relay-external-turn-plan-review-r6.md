# MacBridge Claude File Relay External-Turn Plan Review R6

Date: 2026-07-05
Reviewed plan: `docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan.md`
Plan revision: r6

## Verdict

Implementation-ready.

I do not see any remaining P0/P1/P2 issue in r6 that should block implementation or force another document revision before coding starts. r6 closes the three R5 pre-flight problems, preserves the earlier R1-R4 fixes, and correctly imports the cross-repo `think.md` constraints without changing the main design.

The plan now has a stable shape:

1. First hotfix the production `runningMap` backend-ID lookup bug.
2. Add `core.LiveSessionLister` with PID-bearing `LiveSessionProcess` plus O(1) `IsProcessAlive`.
3. Gate initial idle exit on live-only PID liveness, not executing-state.
4. Refactor transcript classification into a reader-based helper that supports both full-file initial scan and offset-based incremental scan.
5. Keep live-idle watches bounded, while never timing out a mid-turn no-growth state.
6. Keep iOS history sync as the content path; do not fabricate file-relay `text_delta`.

## R6 Checks

### R5 P1 — Out of Scope contradiction

Resolved.

The old contradiction was: "any list_sessions / running-map change is out of scope" while Priority 0 required a `runningMap` closure hotfix. r6 now says list/catalog/pagination work is out of scope **except** the explicit Priority 0 `runningMap` backend-ID hotfix. That gives implementers one clear boundary.

No further change needed.

### R5 P2 — `sessionLiveProcess` must return process info, not bool

Resolved.

r6 uses the correct shape:

```text
func (h *Handlers) sessionLiveProcess(sessionID, backendID string) (proc core.LiveSessionProcess, lister core.LiveSessionLister, err error)
```

The relay caches `proc.PID` and the resolved `lister`, and poll ticks call only:

```text
lister.IsProcessAlive(ctx, cachedPID)
```

This preserves the intended package boundary:

- `agent/claudecode` owns Claude stub/PID semantics.
- `go-bridge` owns relay lifecycle and event emission.
- ticks do not rescan stubs.
- no code path needs `go-bridge` to import agent internals or call `procAlive` directly.

No further change needed.

### R5 P2 — interrupt-user initial branch lifecycle

Resolved.

r6 explicitly says initial `live == true AND interrupt user` emits `turn_completed(idle)` + `markIdle`, then **remains in the poll loop**. The test plan also asserts that a subsequent non-interrupt user line emits a fresh `turn_started`.

This matters because an interrupt marker is not equivalent to a dead session. r6 now preserves the process-watch lifecycle correctly.

No further change needed.

### Cross-repo `think.md` notes

Resolved and useful.

The new "Implementation Notes (from think.md cross-repo review)" section captures the important institutional memory:

- Mac file relay spurious idle is a known artifact; this plan retires that debt with live gating.
- iOS must keep its "ignore idle before first token" defense because reconnect/cold-start/reordering can still produce transient spurious state.
- `turn_started` is an anchor, not a content stream; external-turn content still comes from iOS history sync.
- Triage order remains duplicate send / CLI rerun → file-relay events → iOS high-frequency `get_session_messages` overwrites.

This is exactly the right level: it informs implementation and debugging without expanding the Mac-side scope into iOS behavior changes.

## No New Findings

I intentionally re-checked the areas most likely to cause implementation churn:

- **Live vs executing:** still correctly separated. `GetRunningSessionIDs` remains executing-only and is not used as the relay liveness gate.
- **Dead PID safety:** initial scan running-like branches remain gated on `Live == true`, so stale partial transcripts do not resurrect dead sessions as running.
- **Restart gap:** full-file initial scan can emit `turn_started` for a user line appended while no relay was watching.
- **Incremental scan safety:** reader-based classifier plus `hasMeaningfulEntry` prevents meta-only growth from reusing old entries and re-emitting events.
- **Per-tick cost:** PID is resolved once, then `IsProcessAlive(cachedPID)` is used; no per-tick stub scan.
- **Protocol boundary:** `LiveSessionLister` remains internal type-assertion wiring only, with no `hello_ack`/capability/protocol change.
- **Scope boundary:** list path is untouched except the mandatory backend-ID hotfix.

No additional document fixes are required.

## Implementation Guardrails

These are not new findings; they are the coding checklist I would keep visible while implementing:

- Do the Priority 0 `runningMap` backend-ID hotfix first, with a production-style `"claude"` registration test.
- Keep `LiveSessionProcess` error handling explicit and observable; do not silently fall back to executing-state.
- Assert broadcaster events and registry state in tests, not logs.
- Include the r6 regression cases: dead PID with partial transcript, TTL restart gap, interrupt-user continues watching, meta-only append, and per-tick O(1) liveness recheck.

## Final Recommendation

Stop iterating the plan and move to implementation.

r6 has enough precision to code against, and another review pass is more likely to create churn than reduce risk. The remaining risk is now implementation fidelity, not plan ambiguity.
