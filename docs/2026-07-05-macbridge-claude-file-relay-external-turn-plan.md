# MacBridge Claude File Relay External-Turn Plan

Date: 2026-07-05
Revision: r7 (implementation-ready cleanup after 2026-07-05 review r6; see Review Disposition at the end)

## Summary

When a Claude turn is launched **outside CordCode Link** (e.g. from the Mac's own
Claude Code app/Terminal), iOS observes it via the transcript file relay +
history polling — there is no stdout pipe to that process. iOS renders the reply
of turn N only when turn N+1 starts: a one-turn lag. The session-level
"executing" indicator reaches iOS promptly (via `list_sessions` →
`GetRunningSessionIDs`), but the relay never emits the per-turn `turn_started`
signal for the in-progress external turn, so iOS has no event to anchor a live
render on and falls back to history replay one poll late.

Root cause is one branch in `claudeSessionFileRelayLoop` (`handlers_relay.go:186`):
when the transcript's **initial snapshot** classifies as `idle`, the relay
broadcasts idle and `return`s immediately, never entering the poll loop that
already correctly emits `turn_started` on transcript growth. For an in-progress
external turn the snapshot at poll time often looks idle (last flushed complete
line is the previous turn's assistant, or the new user message has not been
flushed yet), so the relay exits every poll and the running turn is never
signalled.

The fix gates that early-exit on **Claude process liveness** — but liveness here
must be **live-only** (PID alive), not executing-only. The existing
`GetRunningSessionIDs` / `runningMapCache` are executing-only (they require
`isSessionExecuting`, so a live-but-idle process is excluded) and **cannot** be
used as the gate; this plan adds a new live-only capability
(`core.LiveSessionLister`).

This is a **separate, pre-existing issue** from the 2026-07-05 list_sessions CPU
fix (`aec16b8`). That fix is verified working in the same incident window
(`wire_mapping_ms` 4–16ms, was 9.5–11.8s). The CPU plan deliberately did not
touch the file relay. This plan owns the file-relay external-turn gap and reuses
two of the CPU fix's artifacts: the injectable `procAlive` seam and the stub-scan
helper inside `GetRunningSessionIDs`.

## Incident Evidence

Time: 2026-07-05 ~10:38–10:44 CST, during the CPU-fix acceptance test on the
installed Release runtime (PID 65487).

Setup: Mac-side Claude Code, project `-Users-jacklee-Projects-Chat`, session
`16c63341-0e57-4be2-b422-2fd663fcbeea`. User asked several turns from Mac (no iOS
send). iOS opened the session and observed.

go-bridge.log findings over the window:

```text
0   text_delta / turn_started / turn_completed from a managed stdout relay
    (the Mac Claude process is external to CordCode Link)
458 claudeSessionFileRelay "initial state is idle, broadcasting" → exited
  1 claudeSessionFileRelay "turn completed, exiting"
480 get_session_messages (iOS history polling)
857 RPC request (mostly list_sessions at 4–16ms wire_mapping_ms — CPU fix OK)
```

For session `16c63341…` every iOS `get_session_messages` poll (~10s) triggered:

```text
claudeSessionFileRelay started
claudeSessionFileRelay initial state is idle, broadcasting
claudeSessionFileRelay exited
```

i.e. start → snapshot idle → broadcast idle → exit, every poll. The relay never
reached the growth-detection loop that emits `turn_started`.

User-visible symptom on iOS:

- turn 1 reply rendered (largely via history fetch on open);
- turn 2 sent from Mac: iOS shows the executing indicator but anchored it to
  reply 1 (no `turn_started` for turn 2 arrived); reply 2 content did not render
  live; it only appeared once turn 3 was sent from Mac;
- the pattern repeats — reply N lags until turn N+1.

The executing indicator reaching iOS at all confirms `list_sessions` running-state
detection (`GetRunningSessionIDs`, now TTL-cached) works for external turns. The
gap is the relay's per-turn `turn_started` signal.

## Current Code Path

Relevant file: `go-bridge/handlers_relay.go`.

- `startClaudeSessionFileRelay` (line 43): called when iOS opens a Claude session
  (`get_session_messages`); guards on `relayRunning[sessionID]`; starts the loop.
- `claudeSessionFileRelayLoop` (line 156):
  1. `findClaudeSessionFile` → transcript path.
  2. record current file size as `offset` (only detect new content from here).
  3. `initialState := h.detectClaudeTranscriptState(sessPath)` (line 185).
  4. **`if initialState == "idle"` (line 186): broadcast `session_state_changed(idle)`,
     `h.sessions.markIdle`, log "initial state is idle, broadcasting", `return`.**
     ← THE BUG: exits without watching, even when the Claude process is alive
     mid-turn.
  5. if `initialState == "running"`: `markRunning` + broadcast running, enter poll loop.
  6. if `initialState == "unknown"`: enter poll loop, wait for growth.
- Poll loop (line 235, 3s ticker): on transcript growth, classify the last new
  entry and **already handles external turns correctly**:
  - new `user` (non-interrupt) → `turn_started` + `markRunning` (line 328);
  - `user` interrupt → `turn_completed(idle)`, keep watching (line 343);
  - `assistant` final stop_reason (`end_turn`/`stop_limit`/`stop_sequence`/`max_tokens`)
    → `turn_completed` + `session_state_changed(idle)` + **`return` (clean exit)** (line 371);
  - `assistant` non-final (e.g. tool_use) → keep watching.

So the poll loop is correct. The defect is solely the early `return` at step 4,
which prevents the loop from ever running for an external turn whose initial
snapshot looks idle.

### Why exit-on-idle exists (do not regress it)

The idle-exit was added deliberately (see `think.md` 2026-07-04 spurious-idle
sections): opening a **completed** external Claude session must not leave iOS
stuck in "executing" because the relay falsely broadcast running off a stale
snapshot. The fix must preserve "completed session → broadcast idle + exit
promptly" while adding "live process → keep watching for the turn."

## Liveness vs Executing — Why a New Live-Only Capability Is Required

The distinguishing fact for the early-exit gate is **Claude process liveness**
(PID alive), which the transcript snapshot alone cannot give. But liveness must
not be confused with executing:

- `core.RunningSessionLister` / `agent/claudecode.GetRunningSessionIDs` /
  `go-bridge.runningMapCache` are **executing-only**. Source confirms
  (`agent/claudecode/claudecode.go:625-652`): a session enters the running map
  only when `procAlive(pid)` **and** `isSessionExecutingCached(transcript) == true`.
- A live-but-idle Claude process (app/Terminal open, between turns, or in the
  brief window after the user sends but before the assistant's first token
  flushes a parseable line) is therefore **excluded** from the running map. This
  is precisely the relay's core scenario.
- Using `GetRunningSessionIDs` / `h.getRunningMap` as the gate would classify the
  very case we need to fix as "not running" and the relay would still exit on
  idle — the fix would be a no-op.

Therefore the plan adds a **live-only** capability, `core.LiveSessionLister`
(`LiveSessionProcess(ctx, sessionID)` + `IsProcessAlive(ctx, pid)`), implemented
by `agent/claudecode.Agent` by reusing the existing `~/.claude/sessions/*.json`
stub scan and `procAlive` seam but **without** reading the transcript and
**without** calling `isSessionExecuting`. The Handlers side type-asserts the
registered agent, the same way it discovers `RunningSessionLister` today.

## Implementation Prerequisite — `runningMap` closure hotfix (shipped CPU fix `aec16b8`)

The R2/R3 review's P1 surfaced a **real production bug in already-shipped code**:
the `runningMap` cache closure in `go-bridge/handlers.go` (from the 2026-07-05
list_sessions CPU fix, `aec16b8`) hardcodes `h.getAgent("claudecode")`:

```go
h.runningMap = newRunningMapCache(func(ctx context.Context) (map[string]bool, error) {
    agent, ok := h.getAgent("claudecode")   // ← resolves to nil in production
    ...
})
```

Production registers the Claude agent under the **driver id** `"claude"` (via
`handlers.RegisterAgent(id, agent)` in `main.go` with `-drivers claude,...`), and
`RegisterAgent` stores only under that one key with no alias. So in production
`h.getAgent("claudecode")` returns `(nil, false)`, the recompute closure returns
nil, and **the running-map cache is permanently empty** — `GetRunningSessionIDs`
is effectively never called from the list path. The CPU fix's primary goal still
holds (no per-row transcript parsing; `wire_mapping_ms` 4–16ms confirmed), but
Fix 3's TTL cache is a no-op in production and Claude list running-state falls
back to registry-only. All existing tests register the agent as `"claudecode"`,
which is why the bug was not caught.

**This must be hotfixed BEFORE the file-relay implementation lands** (R3 P1):
this plan's Summary and Acceptance assume "the session-level executing indicator
reaches iOS via `list_sessions`", which is currently silently broken. Shipping
the file-relay fix on top of the broken closure would mix two known faults and
weaken acceptance.

Hotfix scope (small, contained):
- Replace the closure's `h.getAgent("claudecode")` with the same backendID-aware
  lookup this plan specifies for `sessionLiveProcess` (`backendID → "claude" →
  `"claudecode"` → scan by `agent.Name()=="claudecode"`). For the `runningMap`
  closure, which has no per-call `backendID`, use the scan-by-name path
  (production has exactly one claudecode agent).
- Add a `go-bridge` test that registers the agent as `"claude"` (production
  wiring) and asserts `getRunningMap` for a claude list actually invokes
  `GetRunningSessionIDs` (i.e., the cache populates). This is the regression
  guard the existing tests missed.
- Rebuild + reinstall per CLAUDE.md.

This hotfix is tracked separately from the file-relay plan's todos but is a
hard prerequisite to starting Fix 0.

## Fix Direction

### Principle

The relay must decide exit-on-idle based on **process liveness** (live-only),
not on a single transcript snapshot and not on executing-state. A completed
session (dead PID) still exits promptly; a live process (external turn in
progress, or between turns) enters the poll loop, which already correctly emits
`turn_started` / `turn_completed`.

### Recommended MacBridge Changes

0. Add a live-only capability: `core.LiveSessionLister`

   The R2 `LiveSessionIDs(ctx) (map[string]bool, error)` shape is insufficient
   (R3 P0): it returns no PID, so the relay cannot "resolve the PID once and
   re-check only that PID on each tick"; and `agent/claudecode.procAlive` is
   package-private, so `go-bridge` cannot call the liveness seam directly. Letting
   `go-bridge` read `~/.claude/sessions/<pid>.json` itself would duplicate the
   Claude stub semantics in the wire-handler layer and bypass the `procAlive`
   test seam. The interface therefore returns per-session process info AND exposes
   a cheap PID-based recheck, both implemented behind the agent seam:

   In `core/interfaces.go`:

   ```go
   // LiveSessionProcess describes the backing process of one Claude session.
   type LiveSessionProcess struct {
       SessionID string
       PID       int
       Live      bool  // PID alive (procAlive), regardless of executing state
   }

   // LiveSessionLister is the live-only counterpart to RunningSessionLister.
   // It exposes per-session process liveness (PID alive) WITHOUT reading
   // transcripts and WITHOUT isSessionExecuting. A live-but-idle process
   // (app/Terminal open, between turns) is Live here but absent from
   // RunningSessionIDs.
   //
   // Internal wiring only: discovered by Handlers type-assertion; NOT a wire
   // capability, NOT in hello_ack, no protocol change.
   type LiveSessionLister interface {
       // LiveSessionProcess resolves the backing process for one session.
       // Called once at relay start. May scan stubs to find sessionID→PID.
       LiveSessionProcess(ctx context.Context, sessionID string) (LiveSessionProcess, error)

       // IsProcessAlive is a cheap (single PID) liveness recheck intended for
       // poll-tick use after LiveSessionProcess resolved the PID at start.
       // O(1); no stub scan, no transcript read.
       IsProcessAlive(ctx context.Context, pid int) bool
   }
   ```

   In `agent/claudecode`:
   - Refactor the stub scan (`~/.claude/sessions/*.json` → `{pid, sessionId,
     cwd}`) currently inlined in `GetRunningSessionIDs` into a shared helper
     (e.g. `readSessionStubs(homeDir)`).
   - Implement `LiveSessionProcess(ctx, sessionID)`: scan stubs for the entry
     whose `sessionId == sessionID`; return `{SessionID, PID, Live: procAlive(pid)}`.
     Not found → `Live=false` (and a sentinel/zero PID).
   - Implement `IsProcessAlive(ctx, pid)`: defer to the injectable `procAlive`
     seam. This is the ONE place the seam is exposed across the package boundary
     (via the interface), keeping `go-bridge` free of Claude process semantics.
   - `GetRunningSessionIDs` keeps its executing-only semantics, reusing the same
     stub helper. No new "live stub cache" is introduced (R3 P2): the relay's
     per-tick cost is bounded by caching the PID at start (below), not by a
     pre-existing cache.

   The cost model: `LiveSessionProcess` runs once per relay start (a stub scan);
   each 3s tick calls only `IsProcessAlive(pid)` — O(1), independent of stub count
   or session count. relay-count × stub-count per tick is forbidden.

1. Gate exit-on-idle on live-only liveness

   In `claudeSessionFileRelayLoop`, replace the unconditional idle-exit branch
   (`handlers_relay.go:186`) with:

   ```text
   if initialState == "idle":
       proc, liveLister := h.sessionLiveProcess(sessionID, backendID)   // LiveSessionProcess + LiveSessionLister, NOT runningMap
       live := proc.Live
       cachedPID := proc.PID
       // cachedPID + liveLister are captured on the relay loop state so each
       // poll tick calls liveLister.IsProcessAlive(ctx, cachedPID) (O(1)),
       // never re-resolving via LiveSessionProcess.
       if live:
           // Live process: snapshot looks idle only because the new turn's
           // first line hasn't flushed yet (or we caught it between turns).
           // Do NOT broadcast idle; fall through into the poll loop, which
           // already emits turn_started on real growth and exits cleanly on
           // turn_completed.
           slog.Info("claudeSessionFileRelay initial idle but process live; watching", ...)
       else:
           // Genuinely completed: broadcast idle + exit (preserves the
           // spurious-running fix for opening completed external sessions).
           broadcast session_state_changed(idle)
           markIdle
           return
   ```

   `h.sessionLiveProcess(sessionID, backendID)` is a thin helper whose
   **agent lookup must be backendID-aware** (this is the cautionary pattern —
   see "Implementation Prerequisite" above), and which **returns both the
   process info and the resolved lister** (R5 P2), so the relay loop can cache
   them once and never re-resolve on a tick. Signature shape:

   ```text
   func (h *Handlers) sessionLiveProcess(sessionID, backendID string) (proc core.LiveSessionProcess, lister core.LiveSessionLister, err error)
   ```

   Production registers the Claude agent under the **driver id** `"claude"` (not
   `"claudecode"`), because `main.go` calls `handlers.RegisterAgent(id, agent)`
   with `id` from `-drivers`, and the default is `claude,opencode,codex`. Lookup
   order:

   1. `h.getAgent(backendID)` — the backend ID of the request that started this
      relay (the file relay already carries `backendID`).
   2. fallback `h.getAgent("claude")`, then `h.getAgent("claudecode")` (explicit
      driver id / test wiring).
   3. last resort: scan `h.agents` for the first agent with
      `agent.Name() == "claudecode"`.

   Only after resolving the agent, type-assert it to `core.LiveSessionLister`.
   At relay start call `LiveSessionProcess(ctx, sessionID)` once; capture both
   the returned `{PID, Live}` and the lister onto the relay loop state. The gate
   decision uses `proc.Live`; subsequent ticks use `lister.IsProcessAlive(ctx,
   proc.PID)`. The helper must NOT consult `h.getRunningMap` /
   `RunningSessionLister` (executing-only — the R1 P0 point).

   If the helper cannot resolve a `LiveSessionLister` or `LiveSessionProcess`
   returns an error, log the failure with `session_id` / `backend_id` and treat
   the process as not live for this relay start. Do not fall back to
   `RunningSessionLister` / `runningMap`: an executing-only signal is the wrong
   contract here and would make the original bug intermittent.

   **Cost bound (do not scan stubs every tick — R2/R3 P2):** each 3s poll tick
   re-checks liveness via `IsProcessAlive(ctx, cachedPID)` only — O(1),
   independent of stub/session count. `LiveSessionProcess` (the stub scan) runs
   once per relay start, not per tick. relay-count × stub-count per tick is
   forbidden. No new "live stub cache" is introduced; cost is bounded by caching
   the PID at start, not by a pre-existing cache.

1b. Close the live-idle-TTL restart gap (enrich initial scan)

   The live-idle TTL exit (Fix 2) opens a timing hole (R3 P1): after the relay
   exits on TTL, an external user line may be appended before the next
   `get_session_messages` restarts the relay. The new relay sets `offset` to the
   current file size, so that user line is never seen by the poll loop; and the
   existing initial-`running` branch (`handlers_relay.go:217-230`) only broadcasts
   `session_state_changed(running)`, **not** `turn_started` — so the per-turn
   anchor the plan exists to deliver is still lost. The bug just moves from
   "initial idle early-exit" to "TTL-gap user line swallowed on restart."

   Fix: make the **initial scan** carry enough information to emit the right
   per-turn event on a warm start. Concretely, replace the coarse
   `detectClaudeTranscriptState → "idle"|"running"|"unknown"` string used by the
   relay with a richer classifier shared with the poll loop's growth logic. The
   classifier must support BOTH a full-file initial scan and an offset-based
   incremental scan (R4 P1), so the poll tick never re-reads pre-offset content:

   ```text
   type lastMeaningfulEntry struct {
       hasMeaningfulEntry bool     // false when scanned region had no user/assistant entry
       entryType          // "user" | "assistant" | ""
       interrupt          // last user entry is a "[Request interrupted by user" prefix
       finalAssistant     // last assistant has end_turn/stop_limit/stop_sequence/max_tokens
   }
   // Reader-based: caller chooses full file (initial) or io.Reader seeded via
   // Seek(offset) (incremental poll tick). Pure function; no offset state inside.
   classifyLastMeaningfulEntryFromReader(r io.Reader) (lastMeaningfulEntry, error)
   ```

   The `hasMeaningfulEntry` field is what lets the poll loop correctly ignore a
   growth that added only meta/ignored lines: when the incremental scan returns
   `hasMeaningfulEntry == false`, the tick only advances `offset` and emits
   **nothing** (no re-used old entry, no repeated `turn_started`). A full-file
   initial scan, by contrast, scans from byte 0 and so always reflects the true
   last meaningful entry.

   Initial-scan decision table (replaces the current idle/running/unknown
   branches at `handlers_relay.go:185-233`). `live` is `LiveSessionProcess.Live`
   resolved at relay start (Fix 0/1). **Every running-like branch is gated on
   `live == true`** (R4 P0) so a dead process's incomplete transcript is never
   treated as an active turn:

   - `live == false` (dead PID), regardless of last entry → broadcast
     `session_state_changed(idle)` + `markIdle` + exit. Do NOT emit
     `turn_started` or `session_state_changed(running)`. A crashed/killed
     Claude left a partial user or non-final assistant line; that is stale
     residue, not an active turn. (This is the original "completed session must
     not be stuck running" guard, extended from final-assistant snapshots to
     incomplete snapshots.)
   - `live == true` AND (final assistant OR `!hasMeaningfulEntry`) → broadcast
     `session_state_changed(idle)` + `markIdle` + enter the poll loop (live
     process; a new turn may start — Fix 1's live-idle watch). `!hasMeaningfulEntry`
     covers an empty/unknown transcript of a live process.
   - `live == true` AND non-interrupt `user` last → a turn is in progress
     (whether the user line arrived before or after relay start) → emit
     **`turn_started`** + `markRunning` + enter the poll loop. This is the
     restart-gap fix: even if the user line was swallowed by `offset`, the
     initial full-file scan still surfaces it as a `turn_started`.
   - `live == true` AND non-final `assistant` (e.g. tool_use mid-turn) → emit
     `session_state_changed(running)` + `markRunning` + enter the poll loop
     (existing behavior; no per-turn anchor yet because no new user line).
   - `live == true` AND interrupt `user` → emit `turn_completed(idle)` +
     `markIdle` per the poll loop's existing interrupt semantics, then **remain
     in the poll loop** (do NOT exit); the live process may continue, and a
     subsequent new user line will emit a fresh `turn_started`.

   The poll loop's growth classifier already tracks exactly these fields
   (`lastEntryType`, `lastUserIsInterrupt`, `lastStopReason`); refactor it into
   the shared `classifyLastMeaningfulEntryFromReader` helper so initial-scan
   (full file) and growth-scan (post-offset reader) agree. This keeps the fix
   contained and avoids a second parser.

2. Two-tier idle lifecycle in the poll loop

   Once the relay stays alive for a live process, define when it exits. Two
   distinct no-growth cases:

   - **Mid-turn no-growth** (`turn_started` already emitted, waiting for the
     assistant): **no idle timeout.** Claude may think or run tools for a long
     time without appending a `final` assistant line. Only the **process-death
     bound** (below) terminates this.
   - **Live-idle watch** (entered the loop from the live-PID + idle-snapshot path
     and has **not** yet emitted `turn_started`): apply a **no-growth watch TTL**
     (e.g. 60–120s, conservative). If no transcript growth for that window → exit
     cleanly **and** reconcile registry state before exiting: if
     `h.sessions` currently has this `sessionID` as `running` (e.g. a stale entry
     left by a previous relay that emitted `turn_started` then died, or a
     `markRunning` from another path), call `markIdle` and broadcast
     `session_state_changed(idle)` so the registry does not keep a stale running
     row. If the registry is already idle / absent (the common case for a relay
     that never emitted `turn_started`), just clear `relayRunning[sessionID]` and
     exit without broadcasting. The next iOS `get_session_messages` poll restarts
     the relay, so observation resumes.

   Add a **process-death bound** to the poll tick (covers both cases): if the PID
   has been dead for N consecutive ticks (e.g. 2) **and** there has been no growth
   for M seconds (e.g. ~6s), broadcast `session_state_changed(idle)` + `markIdle`
   and exit. This bounds resource use when an external Claude process is killed
   mid-turn (no final assistant line) and converges to idle.

   Keep the existing exit paths unchanged: agent-relay supersede, and final
   assistant → `turn_completed` + idle + `return`.

3. Do not false-broadcast running on entry

   When entering the poll loop from the "live process + idle snapshot" path, do
   **not** broadcast `session_state_changed(running)` and do **not** call
   `markRunning`. The poll loop will broadcast `turn_started` + `markRunning`
   once growth confirms a real new user message (existing line 328). This avoids
   reintroducing the "stuck running" symptom for a live-but-truly-idle process,
   and keeps the per-turn running signal anchored to a concrete `turn_started`.

### Priority Order

0. **Hotfix prerequisite (shipped CPU fix `aec16b8`)** — fix the `runningMap`
   closure's hardcoded `h.getAgent("claudecode")` to be backendID-aware, plus a
   test that registers as `"claude"`. Must land BEFORE the file-relay fix: this
   plan's Summary/Acceptance assume "session-level executing indicator reaches
   iOS via `list_sessions`", which is currently a no-op in production. See
   "Implementation Prerequisite" above.
1. **Fix 0** (live-only `core.LiveSessionLister`: `LiveSessionProcess` +
   `IsProcessAlive`) — prerequisite; refactor stub scan, reuse `procAlive` seam,
   no transcript read, PID returned for cheap tick recheck.
2. **Fix 1** (live-only gate before exit-on-idle, using `LiveSessionLister`) —
   the core fix.
3. **Fix 1b** (enrich initial scan → emit `turn_started` when last meaningful
   entry is a non-interrupt user **AND** `Live == true`; reader-based classifier
   with `hasMeaningfulEntry`; closes the live-idle-TTL restart gap and the
   dead-PID false-running gap).
4. **Fix 2** (two-tier idle lifecycle + process-death bound) — resource hygiene
   and correct convergence; needed once Fix 1 lets relays stay alive for live
   processes. Distinguish mid-turn (no timeout) from live-idle-watch (TTL).
5. **Fix 3** (no false running on entry) — invariant, easy to honor.

Fix 1 + Fix 1b + Fix 2 preserve the historical guards and the per-turn anchor:
dead → exit promptly (spurious-running); live → watch; user-last-entry →
`turn_started` (warm start or cold); completed → clean `turn_completed` exit.

### Not in scope (explicitly deferred)

- Emitting real **content** deltas (`text_delta`) for external turns from the
  relay. The relay emits state events (`turn_started` / `turn_completed` /
  `session_state_changed`), not content. Live content for external turns still
  flows through iOS `get_session_messages` polling. If — after this Mac-side fix —
  iOS still does not render in-progress content for Mac-originated turns, that is
  an iOS-side rendering decision (history application during a non-local running
  turn) and must be addressed in `../cordcode-ios/`, not here.
- Adding `session_state_changed(running)` to the poll loop's user-growth branch.
  See Acceptance — `turn_started` is the chosen per-turn running signal.
- Background scanners that periodically parse transcripts (forbidden — same rule
  as the CPU plan; the relay stays event-driven per session).
- Changing the relay's transient-per-poll start model or the `relayRunning` /
  `relayRunningKind` boundary (the agent-relay-vs-file-relay ownership split is
  out of scope; only the idle-exit branch + poll-loop lifecycle change).

## Test Seams (required for deterministic tests)

The current `claudeSessionFileRelayLoop` calls `findClaudeSessionFile`, uses a
real 3s `time.NewTicker`, broadcasts asynchronously, and `return`s in the idle
branch — all of which make naive tests slow and racy. Before implementing, the
following minimal seams must exist (production behavior unchanged):

- **Live-only lister injectable**: `core.LiveSessionLister` discovered by
  type-assertion; `go-bridge` tests inject a fake agent returning a chosen
  live-PID set. `agent/claudecode` unit-tests the real stub scan via the
  `procAlive` seam.
- **Shortenable timing**: poll interval and the live-idle-watch TTL are
  configurable (package-level vars or `Handlers` fields) so tests can run with
  e.g. 10ms poll / 50ms TTL instead of 3s / 60–120s. The process-death bound (N
  ticks, M seconds) follows the same config.
- **Transcript path via HOME temp dir**: reuse the existing fixture layout
  (`~/.claude/projects/<key>/<sid>.jsonl` + `~/.claude/sessions/<pid>.json`
  under a temp HOME) — no production-path mocking.
- **Assert on events, not logs**: tests read broadcasts via the existing test
  connection / broadcaster, not by parsing log strings (logs are diagnostics
  only).

## Test Plan

Two layers, per the review:

### Layer 1 — `agent/claudecode`: live-only lister

- `LiveSessionProcess(ctx, sessionID)` returns `{SessionID, PID, Live}` for the
  matching stub, **without** reading transcripts. A live-but-idle process (PID
  alive, transcript's last line a final assistant → `isSessionExecuting == false`)
  returns `Live=true` here but is **absent** from `GetRunningSessionIDs`
  (executing-only). No stub → `Live=false`, zero PID.
- `IsProcessAlive(ctx, pid)` defers to the injectable `procAlive` seam and is
  O(1) (used for poll-tick recheck). Test via injected `procAlive`: a chosen PID
  is alive; a dead/different PID is not.
- Dead PID → `LiveSessionProcess.Live == false`.
- The stub scan is shared with `GetRunningSessionIDs` (refactor guard: running
  semantics unchanged).
- `LiveSessionProcess` / `IsProcessAlive` are exercised via the `procAlive` seam
  (no real process spawning); they must NOT read transcript content and must NOT
  call `isSessionExecuting`.

### Layer 2 — `go-bridge`: file relay lifecycle

Using the seams above (injectable lister, short timers, HOME fixture, event
assertions):

- **Completed session, dead PID**: initial snapshot `idle` + live-lister says
  dead → relay broadcasts `session_state_changed(idle)` exactly once and exits;
  does **not** enter the poll loop. (Regression guard for the spurious-running
  fix.)
- **External turn, live PID + idle-looking snapshot**: initial snapshot `idle`
  + live-lister says live → relay does **not** broadcast idle / does **not** exit;
  enters the poll loop; on transcript growth (a new user line) emits `turn_started`
  + `markRunning`; on a final assistant line emits `turn_completed` + idle and
  exits.
- **Live-idle watch TTL**: live PID + idle snapshot + no growth for the TTL →
  relay exits cleanly without having broadcast running; a subsequent
  `get_session_messages` restarts it (and if growth then appears, `turn_started`
  fires).
- **Mid-turn no-growth**: `turn_started` already emitted, then long no-growth
  (longer than the live-idle TTL) → relay stays (no premature idle), until a
  final assistant line or the process-death bound.
- **Live PID killed mid-turn**: process death + no final assistant line → relay
  broadcasts idle + exits within the process-death bound (no goroutine leak).
- **No false running on entry**: entering the loop from the live-idle path emits
  no `session_state_changed(running)` and no `markRunning` until a real new user
  line grows.
- **Agent-relay supersede** still terminates the file relay when an agent relay
  takes over (unchanged).
- **Crucially**: none of the above uses `h.getRunningMap` / `RunningSessionLister`
  as the gate — add an explicit assertion that the relay's live-check calls the
  live-only lister, not the running map.
- **Production agent-ID wiring (R2 P1)**: register the fake Claude agent under
  `"claude"` only (matching `main.go`'s `-drivers claude`), start the relay with
  `backendID == "claude"`, and assert the live gate still resolves the agent and
  calls `LiveSessionProcess` (does not fall through to dead/idle). Repeat with the
  agent unregistered → gate returns false → idle-exit (graceful degradation).
- **Stale registry on live-idle TTL exit (R2 P2)**: before entering live-idle
  watch, force `h.sessions.markRunning(sessionID)` (simulating a stale running
  row from a prior relay/process); let the no-growth TTL elapse with a live PID
  → assert the relay `markIdle`'s the session and broadcasts
  `session_state_changed(idle)`, so `h.sessions.isIdle(sessionID)` is true
  afterward. Repeat without the stale entry → no idle broadcast, clean exit.
- **Per-tick cost is O(1) (R2 P2)**: with K stub files present and one relay
  running, assert across multiple ticks that the stub scan (`LiveSessionProcess`)
  is NOT called on every tick — liveness is checked via `IsProcessAlive(cachedPID)`
  only. Acceptable bound: at most one `LiveSessionProcess` call per relay start;
  ticks call only `IsProcessAlive`, regardless of stub count.
- **Live-idle-TTL restart gap (R3 P1)**: live-idle TTL exit → append a non-interrupt
  user line to the transcript BEFORE the next relay start → restart the relay →
  assert it emits `turn_started` + `markRunning` (not only
  `session_state_changed(running)`), even though the user line was written before
  restart and is above the new relay's `offset`.
- **Initial-scan decision table (R3 P1 + R4 P0 + R5 P2, Fix 1b)**: for each
  combination of `live ∈ {true,false}` × last-meaningful-entry shape
  (non-interrupt user / final assistant / non-final assistant / interrupt user /
  empty), assert the initial scan emits exactly the specified event set.
  **Critically include the dead-PID cases**: `live=false` + last non-interrupt
  user, and `live=false` + non-final assistant → must NOT emit `turn_started` or
  `session_state_changed(running)`; must broadcast `session_state_changed(idle)`
  + `markIdle` + exit (a crashed/killed process's incomplete transcript is not
  an active turn). **And the live interrupt-user case**: `live=true` + interrupt
  user → emits `turn_completed(idle)` + `markIdle` AND **remains in the poll
  loop** (does not exit); a subsequent new non-interrupt user line then emits a
  fresh `turn_started` (assert the watching lifecycle, not just the event).
- **Tick recheck uses the cached PID, not a re-resolve (R5 P2)**: with a relay
  watching, force several poll ticks; assert `IsProcessAlive(ctx, cachedPID)` is
  the only liveness call per tick and `LiveSessionProcess` is NOT re-invoked
  (the PID + lister are captured once at relay start).
- **Incremental scan does not re-emit on meta/ignored growth (R4 P1, Fix 1b)**:
  after the relay is watching with `turn_started` already emitted, append ONLY a
  meta/ignored line (no new user/assistant meaningful entry) → the poll tick
  must advance `offset` and emit nothing (no repeated `turn_started`, no state
  change). Confirms the classifier's `hasMeaningfulEntry == false` path and the
  reader-based (post-offset) incremental API. Also cover truncate/rewrite: when
  `newSize < offset`, the helper re-scans from 0 (full file) and the existing
  `offset = 0` reset semantics are preserved.

Suggested targeted commands:

```bash
cd ../cordcode-macbridge
go test ./agent/claudecode -run 'LiveSession|RunningSession' -count=1
go test ./go-bridge -run 'ClaudeSessionFileRelay|FileRelayExternalTurn' -count=1
```

No real Claude credentials, no real iOS UI.

## Acceptance Criteria

- The per-turn running signal is **`turn_started`**: a Mac-originated external
  turn makes the relay emit `turn_started` (on the new user-line growth) instead
  of exiting on the first idle snapshot. iOS no longer lags by one turn for the
  per-turn anchor.
- **Warm-start `turn_started` (R3 P1)**: when a relay starts after a live-idle
  TTL exit and the transcript's last meaningful entry is a non-interrupt user
  (the user line landed during the gap), the initial scan emits `turn_started` +
  `markRunning`, not only `session_state_changed(running)`. The per-turn anchor
  survives relay restarts.
- **Classifier API is split full vs incremental (R4 P1)**: the shared classifier
  is reader-based (`classifyLastMeaningfulEntryFromReader`) so the initial scan
  reads the full file and the poll tick reads only post-`offset` content; it
  returns `hasMeaningfulEntry`, so a tick whose growth added only meta/ignored
  lines advances `offset` and emits nothing (no repeated `turn_started`).
- Session-level running continues to come from `list_sessions` runtimeState
  (`GetRunningSessionIDs`, unchanged — and the prerequisite hotfix restores it in
  production). The poll loop does **not** add a new `session_state_changed(running)`
  (Option A — see Review Disposition P1b).
- A genuinely completed (or dead/crashed) external session — `Live == false`,
  regardless of whether the transcript's last meaningful entry is a final
  assistant, a non-interrupt user, or a non-final assistant — still makes the
  relay broadcast idle and exit promptly, and **never** emits `turn_started` or
  `session_state_changed(running)` for a dead PID (R4 P0). No regression of the
  "stuck running on open" symptom, including for incomplete stale snapshots.
- A live-but-idle process does not cause a false `running`/`turn_started`.
- Live-idle watch is bounded: a live process with no growth for the TTL exits
  cleanly; mid-turn no-growth (after `turn_started`) is not prematurely idled.
- The relay exits within a bounded window after an external Claude process dies
  (no perpetual poll loop / goroutine leak), even without a final assistant line.
- `claudeSessionFileRelay "initial state is idle, broadcasting" → exited` is no
  longer the dominant pattern for a session whose Mac process is actually live
  (verifiable from go-bridge.log during a real external turn).
- CPU stays bounded: at most one relay goroutine per session; 3s poll interval;
  no new background transcript scanner; `LiveSessionProcess` reuses the shared
  stub-scan helper + `procAlive` seam and reads no transcript; per-tick liveness
  is O(1) via `IsProcessAlive(cachedPID)`, not a per-tick stub scan and not a
  non-existent "live stub cache" (R3 P2).
- Agent lookup is backendID-aware: the live gate works with the production
  `-drivers claude` wiring (agent registered under `"claude"`), not only under
  the `"claudecode"` test wiring.
- Per-tick liveness cost is O(1) in stub count: a relay does not scan all
  `~/.claude/sessions/*.json` on every 3s tick; it checks only the cached PID
  via `IsProcessAlive` (no short-TTL cache involved — R3 P2).
- Live-idle TTL exit reconciles the registry: a stale `running` row is
  `markIdle`'d (and idle broadcast) on exit; it does not leave
  `h.sessions.isIdle(sessionID) == false` behind.
- `LiveSessionLister` is internal wiring only — no new `hello_ack` capability
  field, no protocol change.

## Diagnostics For Future Incidents

```bash
# During a Mac-originated Claude turn observed on iOS:
LOG="$HOME/Library/Application Support/CordCode Link/logs/go-bridge.log"
rg -n 'claudeSessionFileRelay|turn_started|turn_completed|session_state_changed|text_delta|live but process live' "$LOG" | tail -80
```

Interpretation:

- `claudeSessionFileRelay started` → `initial state is idle, broadcasting` +
  `exited` recurring every poll for a session whose Mac process is actually live →
  Fix 1 not effective (live-only gate missing, wrong, or wired to executing-only).
- `claudeSessionFileRelay initial idle but process live; watching` followed by a
  `turn_started` during the Mac turn → Mac side correct; if iOS still lags, the
  remaining issue is iOS-side rendering of Mac-originated turn content.
- Absent any `turn_started` with a persistent executing indicator anchored to the
  previous reply → confirms the one-turn-lag class.

## Relationship To Existing Known Issues

This plan directly addresses the Mac-side item that `think.md` (2026-07-04
spurious-idle sections) records as a deferred cleanup:

> Mac 侧的正确修法（后续独立清债）应是：file relay 不得在「真实 agent relay
> 未确认 idle」前单方面广播 idle；或 file-relay 的初始状态读取不得用上一轮
> 已完成 transcript。

Related but distinct prior issues:

- **Spurious idle before first token (2026-07-04)**: file relay broadcast idle
  off the previous completed transcript on cold start. iOS was hardened
  (ChatTurnSyncPolicy / `.localSend` ownership) to not collapse on it. That
  hardening was for **iOS-initiated** local turns; **Mac-originated** external
  turns are a different ownership path and still need the Mac side to emit a
  timely `turn_started` — which is what this plan delivers.
- **list_sessions CPU (2026-07-05, `aec16b8`)**: related only through the
  prerequisite backend-ID hotfix and shared implementation seams. This plan
  reuses its `procAlive` seam and stub-scan helper, but does not otherwise
  change list enrichment, catalog, or pagination semantics.

## Implementation Notes (from think.md cross-repo review)

These constraints are surfaced by the R5 review's cross-repo read of this repo's
`think.md` and `../cordcode-ios/think.md`. They do not change the plan; they are
preserved here so the implementer honors them and so triage follows the
established order.

- The Mac-side file relay broadcasting spurious idle off a stale transcript
  terminus is a **known artifact**; the `LiveSessionProcess.Live` gating in this
  plan is exactly the cleanup of that Mac-side debt.
- The iOS-side "ignore idle before first token of a Claude local turn" defense
  must **remain in place** even after this Mac fix — cold start, reconnect, and
  event reordering can still produce brief spurious state. The Mac fix and the
  iOS defense are layered, not substitutes.
- `turn_started` is only a per-turn anchor; external-turn **content** still
  renders via iOS history sync. If content still does not refresh after this Mac
  fix, investigate iOS history application / ownership first — do NOT add
  fabricated `text_delta` to the file relay.
- Incident triage order (per `think.md`): first rule out duplicate `send_message`
  / Claude CLI re-runs, then inspect file-relay state events, finally check
  whether iOS is doing high-frequency `get_session_messages` that overwrites the
  timeline.

## Out of Scope

- iOS-side rendering of in-progress Mac-originated turn content (verify after
  this fix; address in `../cordcode-ios/` if still broken).
- Real `text_delta` content streaming from the file relay.
- Adding `session_state_changed(running)` to the poll loop (Option A chosen; see
  Review Disposition).
- Changing the relay start/ownership model or `relayRunningKind` boundary.
- Any list_sessions / catalog / pagination change, **except** the explicit
  Priority 0 `runningMap` backend-ID hotfix required before this plan (that
  hotfix is in scope and mandatory; see Implementation Prerequisite).

## Review Disposition

Review source: `docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan-review.md`.

All five findings adopted. None rejected. Two findings offered alternatives; the
chosen alternative is noted (choosing an alternative is adopting the finding, not
rejecting it).

### Adopted

- **P0 — `GetRunningSessionIDs` cannot be the liveness gate (executing-only ≠
  live-only).** Adopted in full. The Summary, the new "Liveness vs Executing"
  section, Fix 0, Fix 1, the test plan's explicit "must not use runningMap as
  gate" assertion, and the acceptance now all say: add live-only
  `core.LiveSessionLister` (`LiveSessionProcess` + `IsProcessAlive`), reuse the
  stub scan + `procAlive` seam only, no transcript read, no
  `isSessionExecuting`. All prior wording about "reuse `h.getRunningMap` /
  `GetRunningSessionIDs` for liveness" is removed.
- **P1a — live-but-idle long-residency goroutine lifecycle.** Adopted. Fix 2 now
  defines a two-tier idle lifecycle: mid-turn no-growth (after `turn_started`) has
  no idle timeout (Claude can think long without file growth); live-idle watch
  (no `turn_started` yet) exits after a conservative no-growth TTL, and the next
  `get_session_messages` restarts the relay. Plus a process-death bound covers
  both cases.
- **P1b — `session_state_changed(running)` acceptance inconsistency.** Adopted,
  choosing **Option A** (keep code unchanged): `turn_started` is the per-turn
  running signal; the registry's `markRunning` updates local state; session-level
  running continues to come from `list_sessions` runtimeState. The poll loop does
  **not** add a new `session_state_changed(running)`. Acceptance rewritten to a
  single consistent口径. Option B (extend the poll loop to also broadcast
  `session_state_changed(running)`) was considered and not chosen: it adds
  ordering/dup risk for no proven iOS need — the user's symptom is a missing
  per-turn anchor, and `turn_started` already provides exactly that.
- **P2a — fix the interface shape (no "decide at impl time").** Adopted. The plan
  now mandates `core.LiveSessionLister` as a new optional interface implemented by
  `agent/claudecode.Agent`, discovered by the Handlers via the same type-assertion
  used for `RunningSessionLister`. Reason: per repo boundary, `go-bridge` must not
  reach into `agent/claudecode` package-level helpers; capability discovery is via
  `core/interfaces.go` optional interfaces.
- **P2b — test seams.** Adopted. A dedicated "Test Seams" section now requires:
  injectable live-only lister; shortenable poll interval + live-idle TTL +
  process-death bound; transcript path via HOME temp dir; event-based assertions
  (not log-parsing). The test plan is split into Layer 1 (`agent/claudecode`
  live-only lister) and Layer 2 (`go-bridge` relay lifecycle).

### Not Adopted

None. Every review finding is reflected above. The only "choices" are between
alternatives the review itself offered as legitimate (P1b Option A vs B; the
review's P1a "while iOS observes" vs "TTL" — TTL chosen). No finding was rejected.

### Considered but folded in (not separate rejections)

- Extending `RunningSessionLister` with live-only process methods instead of a
  separate `LiveSessionLister` interface: considered, not chosen — single
  responsibility (executing-only vs live-only are different contracts and the
  docstring/test assertions differ), and consistent with the repo's one-capability-
  per-interface style. This is a sub-decision of P0/P2a, not a rejection.

## Review Disposition (R2)

Review source: `docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan-review-r2.md`.

R2 confirms all five R1 findings are fixed and raises three implementation-detail
findings. All three adopted.

### Adopted

- **R2 P1 — agent lookup must be backendID-aware (production registers under
  `"claude"`, not `"claudecode"`).** Adopted. Fix 1's helper is now
  `sessionLiveProcess(sessionID, backendID)` with an explicit lookup order:
  `backendID` → `"claude"` → `"claudecode"` → scan by `agent.Name()=="claudecode"`.
  A new Layer-2 test registers the agent under `"claude"` only and asserts the
  gate still resolves + calls `LiveSessionProcess`; acceptance adds a
  backendID-aware wiring criterion. This is the cautionary pattern flagged by the
  review.
- **R2 P2 — live-liveness re-check cost + live-idle TTL exit registry cleanup.**
  Adopted. Fix 1's helper now requires per-tick cost to be O(1) in stub count:
  resolve the session's PID once at relay start and re-check that single PID via
  `IsProcessAlive(cachedPID)` each tick, never a full stub scan per tick.
  Fix 2's live-idle TTL exit now reconciles registry state: if a stale `running`
  row exists it is `markIdle`'d (+ idle broadcast); otherwise clean exit. New
  Layer-2 tests cover the per-tick cost bound and the stale-registry cleanup;
  acceptance adds both criteria.
- **R2 P3 — `LiveSessionLister` is internal wiring, not a wire capability.**
  Adopted. Fix 0 now states explicitly: discovered by Handlers type-assertion,
  NOT added to `deriveBackendCapabilities` / `hello_ack.backends[].capabilities`,
  no protocol change.

### Prerequisite Bug surfaced by R2 P1

R2 P1's cautionary example is a **real production bug in the shipped CPU fix
(`aec16b8`)**: the `runningMap` cache closure hardcodes `h.getAgent("claudecode")`,
which resolves to nil under the production `-drivers claude` wiring, so the
running-map cache never populates and `GetRunningSessionIDs` is not called from
the list path. The CPU fix's primary goal (no per-row transcript parsing) still
holds; Fix 3's cache is a no-op in production. This is documented in the new
"Implementation Prerequisite" section above as a mandatory hotfix inside this
plan's scope exception, and this plan's `sessionLiveProcess` avoids the same
pattern.

### Not Adopted (R2)

None. All three R2 findings adopted.

## Review Disposition (R3)

Review source: `docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan-review-r3.md`.

R3 confirms R2's three findings are fixed and raises four more implementation
gates. All four adopted.

### Adopted

- **R3 P0 — `LiveSessionIDs(ctx) map[string]bool` cannot support "tick checks a
  single PID via the `procAlive` seam".** Adopted. Fix 0's interface is now
  two-method: `LiveSessionProcess(ctx, sessionID) (LiveSessionProcess, error)`
  (returns PID + Live, called once at relay start) and `IsProcessAlive(ctx, pid)
  bool` (cheap PID recheck for poll ticks, the single place the `procAlive` seam
  crosses the package boundary via the interface). `go-bridge` does NOT read
  Claude stub files or call `procAlive` directly; Claude process semantics stay
  in `agent/claudecode`.
- **R3 P1 (timing hole) — live-idle TTL exit can swallow the next `turn_started`
  on relay restart.** Adopted, choosing the **enrich-initial-scan** option (R3's
  Option B): new Fix 1b introduces a shared reader-based classifier
  (`classifyLastMeaningfulEntryFromReader`, refactored out of the poll loop's
  growth logic) and an initial-scan decision table that emits `turn_started` +
  `markRunning` when the last meaningful entry is a non-interrupt user **and the
  PID is live** (the live-PID gate was added by R4 P0) — even if that user line
  landed during the TTL gap and
  is above the new relay's `offset`. R3's Option A (drop TTL, bind watch to iOS
  observation lifetime) was considered and not chosen: it requires redefining
  relay lifetime around connection/subscription state, a larger change than the
  contained classifier refactor. New Layer-2 test: append a user line during the
  TTL gap → restart relay → assert `turn_started` fires.
- **R3 P1 (hotfix prerequisite) — `runningMap` closure bug must be a prerequisite,
  not a side note.** Adopted. The bug is now documented in
  "Implementation Prerequisite" and listed as Priority item 0: the file-relay
  implementation does not start until the closure's `h.getAgent("claudecode")`
  hardcode is fixed (backendID-aware / scan-by-name) and a `"claude"`-registered
  running-map test is added. Reason: this plan's Summary/Acceptance assume
  session-level executing indicator reaches iOS via `list_sessions`; shipping
  file-relay on top of the broken closure would mix two known faults.
- **R3 P2 — "cached stub/`procAlive` path" wording is imprecise.** Adopted. There
  is no live stub cache today. Acceptance and Fix 0 now say `LiveSessionProcess`
  reuses the shared stub-scan helper + `procAlive` seam and reads no transcript;
  per-tick cost is bounded by `IsProcessAlive(cachedPID)` (O(1)), explicitly not
  a per-tick stub scan and not a non-existent cache.

### Not Adopted (R3)

None. All four R3 findings adopted. The only "choice" is R3 P1 Option A vs B
(both offered by the review); Option B chosen for the reason above.

## Review Disposition (R4)

Review source: `docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan-review-r4.md`.

R4 confirms R3's fixes and raises three more implementation gates. All three
adopted.

### Adopted

- **R4 P0 — initial-scan running branches must be gated on `Live == true`.**
  Adopted. The Fix 1b decision table now has an explicit `live` dimension. Every
  running-like branch (non-interrupt user → `turn_started`; non-final assistant
  → `session_state_changed(running)`) requires `live == true`. A new dead-PID
  branch (`live == false`, regardless of last entry) broadcasts idle + `markIdle`
  + exits and never emits `turn_started`/running — extending the original
  "completed session must not be stuck running" guard from final-assistant
  snapshots to incomplete/crashed snapshots. New tests: dead PID × {non-interrupt
  user, non-final assistant} → no `turn_started`/running.
- **R4 P1 — classifier must split full-file vs incremental (offset-based) scan.**
  Adopted. The shared classifier is now reader-based:
  `classifyLastMeaningfulEntryFromReader(r io.Reader)`, with the caller choosing
  full-file (initial scan) or `Seek(offset)`-seeded (poll tick). The return
  struct gains `hasMeaningfulEntry bool` so a tick whose growth added only
  meta/ignored lines advances `offset` and emits nothing — no re-used old entry,
  no repeated `turn_started`. New test: append-only-meta/ignored → no re-emit.
  Truncate/rewrite (`newSize < offset`) keeps the existing `offset = 0` full
  re-scan.
- **R4 P2 — Fix 2 heading was dropped.** Adopted. The `2. Two-tier idle lifecycle
  in the poll loop` heading is restored (it had been lost when Fix 1b was
  inserted); numbering no longer jumps 1b → 3.

### Not Adopted (R4)

None. All three R4 findings adopted.

## Review Disposition (R5)

Review source: `docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan-review-r5.md`.

R5 confirms r5 is implementation-ready and raises three document-level fixes
(one contradiction, two precision gaps). All three adopted. R5 also passes
through cross-repo `think.md` constraints; those are preserved verbatim in the
new "Implementation Notes (from think.md cross-repo review)" section.

### Adopted

- **R5 P1 — Out of Scope contradicts Priority 0 hotfix.** Adopted. The
  "Any list_sessions / catalog / pagination / running-map change" bullet now
  reads "…except the explicit Priority 0 `runningMap` backend-ID hotfix required
  before this plan." Implementer no longer has conflicting scope signals.
- **R5 P2 — `sessionLiveProcess` must return PID-bearing info, not bool.**
  Adopted. The pseudo-code now binds `proc, liveLister := h.sessionLiveProcess(...)`,
  caches `proc.PID` + the lister on relay state, and each tick calls
  `lister.IsProcessAlive(ctx, cachedPID)`. The helper signature is spelled out as
  `(proc core.LiveSessionProcess, lister core.LiveSessionLister, err error)`. New
  test asserts ticks re-check via `IsProcessAlive(cachedPID)`, never re-resolving
  via `LiveSessionProcess`.
- **R5 P2 — initial interrupt-user branch must state it keeps watching.**
  Adopted. The decision-table entry now reads "emit `turn_completed(idle)` +
  `markIdle` … then **remain in the poll loop** (do NOT exit)." The decision-table
  test asserts both the event and the watching lifecycle (a subsequent new
  non-interrupt user line emits a fresh `turn_started`).

### Not Adopted (R5)

None. All three R5 findings adopted.

### Cross-repo notes (preserved, not main-plan changes)

Per R5's "Cross-Repo Think Notes", four `think.md`-derived constraints are now
recorded under "Implementation Notes (from think.md cross-repo review)": (1) Mac
spurious-idle is a known artifact this plan retires; (2) the iOS "ignore idle
before first token" defense stays; (3) `turn_started` is only an anchor — content
still renders via iOS history sync, do not fabricate `text_delta`; (4) triage
order: duplicate `send_message` → file-relay state events → iOS high-frequency
`get_session_messages`.

## Review Disposition (R6)

Review source: `docs/2026-07-05-macbridge-claude-file-relay-external-turn-plan-review-r6.md`.

R6 found no remaining P0/P1/P2 blockers and recommended moving to implementation
instead of continuing plan iteration. The review did surface a few cleanup edits
for the plan document itself; all are incorporated in this r7 revision.

### Adopted

- **Mark the plan as implementation-ready.** Adopted. The revision header now
  records r7 as an implementation-ready cleanup after R6.
- **Remove stale historical wording that could confuse implementation.** Adopted.
  Earlier disposition text now refers to the final interface shape
  (`LiveSessionProcess` + `IsProcessAlive`) instead of obsolete live-ID map
  wording, except where the old shape is explicitly discussed as rejected.
- **Clarify helper error handling.** Adopted. `sessionLiveProcess` failure is now
  explicit: log it, treat the process as not live for that relay start, and do
  not fall back to executing-only `RunningSessionLister` / `runningMap`.
- **Keep the Priority 0 hotfix inside the scope exception.** Adopted. The
  `runningMap` backend-ID bug is consistently documented as a mandatory
  prerequisite, not an out-of-scope related note.

### Not Adopted (R6)

None. R6 did not raise any new blocking design issue.
