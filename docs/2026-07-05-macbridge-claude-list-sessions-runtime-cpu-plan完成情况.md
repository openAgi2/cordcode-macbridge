# MacBridge Claude list_sessions Runtime CPU — 完成情况

Date: 2026-07-05
Plan: `../cordcode-ios/docs/2026-07-05-macbridge-claude-list-sessions-runtime-cpu-plan.md`
Canonical State: `.exec-plan/state/plan-c7cc43114fd6.json`
Commit: `aec16b8` (main, local; not pushed)
Queue hash (final): `8bfdaa9e12afbf8bd73c7ed1f4770204b3f251ce2d8374bdc6076d2a4e439ac6`
Outcome: **completed** — 16/16 todos proven done; all 4 owner real-device acceptance items pass.

> Attestation legend: each evidence row is labeled **re-verified** (the command was
> re-run during the exit audit and the fresh result recorded) or **self-attested**
> (claimed by the implementing agent; not independently re-run). This report is
> authored by the same agent that did the work — labels are honest but not
> independent verification.

## Outcome Summary

The Claude `list_sessions` runtime CPU peg is fixed and verified end-to-end on
the installed Release runtime. In the 2026-07-05 ~02:11 incident,
`cordcode-bridge-runtime` sat near one core (≈100%) with `wire_mapping_ms`
9.5–11.8s over 144 sessions (~116MB), because the list path parsed every Claude
transcript per row to infer running state. After the fix, the same workload runs
at `wire_mapping_ms` 4–16ms with the runtime at **0.0% CPU** (owner-measured),
and per-row transcript opens are provably zero.

A separate, pre-existing issue surfaced during acceptance: Mac-originated
external Claude turns lag by one turn on iOS. That is **not** a regression from
this plan and is tracked in its own (r2) plan
`docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan.md`.

## What Shipped (`commit aec16b8`)

- **Step 0** — removed the `/tmp/bridge-sessions.json` debug dump from
  `handleListSessions` (it sat inside the `wire_mapping_ms` timing window).
- **Fix 1 + 2** — list-safe batch enrichment. Added `getRunningMap(ctx, agent)`
  (hoisted, one `GetRunningSessionIDs` per request) and
  `enrichSessionStatesForList(sessions, agent, runningMap)`; migrated all three
  list call sites (Claude / non-Claude / OpenCode). The list path now opens zero
  transcripts per row, never calls `markIdle`, never writes `/tmp`. Detail paths
  keep richer `enrichSessionStateWithAgent`. `reasoningEffort` injection
  preserved (`injectClaudeReasoningEffort`).
- **Fix 3** — 2s TTL `runningMapCache` for Claude `GetRunningSessionIDs`;
  MacBridge-tracked turn transitions invalidate via a `sessionRegistry`
  `onStateChange` callback (`markRunning`/`markIdle`). External turns still
  detected within one TTL window via the bounded live-PID path; no background
  scanner added.
- **Fix 5** — `isSessionExecuting` results cached by
  `sessionID+path+size+mtime` (size and mtime compared together), bounding
  cold-cache cost to changed transcripts rather than K.
- Testability seams: `agent/claudecode/proc_seam.go` (`procAlive` injectable),
  `go-bridge/transcript_probe.go` (test-only transcript-call counter).
- Docs: `GO_BRIDGE_ARCHITECTURE.md` (list-path boundary + new invariants) and
  `CHANGELOG.md` `[Unreleased]`.

## Per-Todo Evidence

| Todo | Status | Method | Attestation | Evidence |
|---|---|---|---|---|
| phase0-dump-impl | done | impl | self-attested | `handlers.go` dump deleted; `rg bridge-sessions.json go-bridge/` → gone; `go build` ok |
| phase0-dump-tests | done | test | **re-verified** | `TestClaudeListSessionsDoesNotWriteTmpDump` fires 3 list requests, asserts no `/tmp` file; `go test ./go-bridge -run TestClaudeListSessionsDoesNotWriteTmpDump` → PASS |
| phase0-dump-regression | done (required:false) | na | self-attested | `na_reason`: behavioral coverage in phase0-dump-tests; perf re-baseline folded into phase1-listsafe-regression |
| phase1-listsafe-impl | done | impl | self-attested | `enrichSessionStatesForList`/`getRunningMap`/`applyListRuntimeState`/`injectClaudeReasoningEffort` added; 3 list call sites migrated; detail paths unchanged; `go build`/`go vet` clean |
| phase1-listsafe-tests | done | test | **re-verified** | 5 tests incl. zero per-row transcript opens (idle+running+stale-running), `getRunningMap` once/request, no `markIdle`; exit-audit full suite re-run → ok |
| phase1-listsafe-regression | done | test | **re-verified** | `TestListSessionsClaude_144SessionPerfFixture`: 144 sessions × ~700KB sparse (~100MB); cold=56ms / cache-hit=42ms / transcript_opens=0; <200ms bound scoped to cache-hit path |
| phase2-ttlcache-impl | done | impl | self-attested | `runningMapCache` (2s TTL, `onStateChange` invalidation, `procAlive` seam); `getRunningMap` routes Claude through cache; `go build`/`vet` clean |
| phase2-ttlcache-tests | done | test | **re-verified** | burst collapse, TTL expiry, invalidate, nil fallback; external-turn via injectable `procAlive`; state-change-via-registry; all PASS |
| phase2-ttlcache-regression | done | test | **re-verified** | external-turn regression IS the seam-based fixture (`TestGetRunningSessionIDs_ExternalTurnViaInjectableSeam` + `TestGetRunningMap_CacheSurfacesExternalSession`); plan explicitly chose injected PID-liveness |
| phase3-transcript-impl | done | impl | self-attested | `agent/claudecode/transcript_exec_cache.go`; `GetRunningSessionIDs` call site switched to `isSessionExecutingCached`; size+mtime key |
| phase3-transcript-tests | done | test | **re-verified** | 6 tests incl. `MtimeAloneForbidden` (same-size/diff-mtime & diff-size/same-mtime both invalidate), `LargeKGuardrail`; all PASS |
| phase3-transcript-regression | done | test | **re-verified** | `TestTranscriptExecCache_LargeKGuardrail`: K=5, round2 all-hit, round3 only-1-miss; cold cost bounded by changed-transcript count |
| phase4-doc-sync-impl | done | impl | self-attested | `GO_BRIDGE_ARCHITECTURE.md` list-boundary bullet + invariant; `CHANGELOG.md` `[Unreleased]` 2026-07-05 entry; documented symbols cross-checked present |
| phase4-doc-sync-tests | done (required:false) | na | self-attested | `na_reason`: doc-only; validated by render + symbol cross-reference |
| phase4-build-install-regression | done | build+install | self-attested (build re-verifiable; under-load observation owner-confirmed) | `./scripts/build-unsigned-release.sh` → BUILD SUCCEEDED; embedded binary contains new symbols + lacks `/tmp` dump string; reinstalled; port 8777 = new embedded runtime PID 65487 (old gone); clean startup log; `/tmp/bridge-sessions.json` absent |
| phase5-user-validation-regression | done | manual-device | **owner-validated (self-attested)** | Owner real-device: list refresh responsive; runtime CPU **0.0%** (was ~100%); external-turn executing-state reaches iOS; `wire_mapping_ms` 4–16ms (was 9.5–11.8s). All 4 CPU-plan acceptance items pass. |

## Exit Audit

- Internal audit: **passed**. Full suites re-run during the exit audit
  (`go test ./go-bridge/... ./agent/claudecode/... ./core/... -count=1`) → all ok;
  `go vet ./go-bridge ./agent/claudecode` clean.
- Failure classification: none. No `review-fix` triplets created. No handoff.
- Start outcome: **completed**.

## Scope Boundary — Separate Issue Found

During phase5, the owner found that Mac-originated external Claude turns lag by
one turn on iOS (reply N appears when turn N+1 starts). Investigation
(`go-bridge.log` + `handlers_relay.go` + `think.md`) confirmed this is a
**pre-existing file-relay issue**, not a regression from this plan:

- The Claude file relay broadcasts idle + exits on each `get_session_messages`
  poll when the transcript's initial snapshot looks idle, never entering the poll
  loop that emits `turn_started`. For an in-progress external turn the snapshot
  usually looks idle → no per-turn `turn_started` → iOS lags.
- This plan's changes do not touch the file relay (`git show aec16b8 --
  go-bridge/handlers_relay.go` shows only a no-op `transcriptStateProbe()` line).

Tracked separately: `docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan.md`
(r2, review-adopted). It reuses this plan's `procAlive` seam and stub-scan
helper.

## Deferred Per Plan §Not Adopted

- Fix 6 (prefer MacBridge-owned runtime state for turns) — long-term architecture;
  short-term CPU win overlaps Fix 1.
- Fix 7 (list-level request coalescing) — follow-up guardrail only; catalog
  already has snapshot-level in-flight de-dup.
- Removing transcript inference entirely — cold recovery and external-turn
  detection still need bounded transcript/file inference.

## Reproduction / Re-verification Commands

```bash
cd ../cordcode-macbridge

# Full suites (the exit-audit re-verification):
go test ./go-bridge/... ./agent/claudecode/... ./core/... -count=1

# Targeted:
go test ./go-bridge -run 'EnrichSessionStatesForList|GetRunningMap|ApplyListRuntimeState|ListSessionsClaude_144SessionPerfFixture|ListSessionsClaude_RunningMapComputedOncePerRequest|ListSessionsClaude_StateChangeInvalidatesRunningMap|RunningMapCache|ListSessionsOpenCode_NoTranscript|TestClaudeListSessionsDoesNotWriteTmpDump' -count=1
go test ./agent/claudecode -run 'TranscriptExecCache|TestGetRunningSessionIDs_ExternalTurnViaInjectableSeam' -count=1

# Live runtime CPU + metric under iOS refresh:
ps -axo pid,pcpu,command | grep '[c]ordcode-bridge-runtime'
rg -n 'method=list_sessions' "$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log" | tail -5
ls /tmp/bridge-sessions.json   # expected: No such file
```

Author: same agent that implemented the plan. Re-verified rows were re-run during
the exit audit; self-attested rows were not independently verified.
