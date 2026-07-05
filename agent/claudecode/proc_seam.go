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
