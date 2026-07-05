package claudecode

// procAlive is the PID-liveness hook used by GetRunningSessionIDs. It is a
// package-level variable so tests can inject a deterministic fake (returning
// true/false for chosen PIDs) instead of relying on os.Getpid() being alive or
// spawning real, timing-sensitive child processes. Production code uses the
// platform isProcessRunning implementation assigned here.
//
// The external-turn detection path (a Claude turn launched outside MacBridge,
// where there is no owned registry entry) depends on this liveness check, so
// making it injectable is what lets the fixture assert that such a turn is
// surfaced as running after a running-map cache refresh.
var procAlive = isProcessRunning

// procIdentityAlive is the PID-identity-aware liveness seam. It reports whether
// pid is alive AND the live process still matches the recorded session identity
// (the executable is a Claude process, and its working directory equals
// expectCwd when expectCwd is non-empty). This is the defence against PID reuse:
// a session stub whose original claude exited, after the OS reuses its PID for
// an unrelated process, must NOT be reported live.
//
// The default implementation composes procAlive (liveness) with
// verifyClaudeProcessIdentity (identity). Tests override this to a deterministic
// fake to avoid real process introspection.
//
// Callers that iterate session stubs (LiveSessionProcess, GetRunningSessionIDs)
// use this seam rather than procAlive/IsProcessAlive directly, so a reused PID
// cannot resurrect a stale session as "running". The file-relay per-tick
// recheck of a cached PID (IsProcessAlive) intentionally stays liveness-only:
// by then identity was confirmed once at relay start, and a reused PID only
// prolongs silent watching (bounded by the live-idle TTL) without emitting
// false events.
var procIdentityAlive = func(pid int, expectCwd string) bool {
	if !procAlive(pid) {
		return false
	}
	return verifyClaudeProcessIdentity(pid, expectCwd)
}
