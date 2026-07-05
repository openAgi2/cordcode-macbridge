package claudecode

import (
	"os"
	"sync"
	"sync/atomic"
)

// transcriptExecKey fingerprints a transcript file by sessionID + path + size +
// mtime. Size and mtime MUST be compared together (both are part of the key):
// mtime resolution can be too coarse under rapid writes or non-APFS storage, so
// a same-mtime/different-size change — and a same-size/different-mtime change —
// must both invalidate. Using only mtime is forbidden.
type transcriptExecKey struct {
	sessionID     string
	path          string
	size          int64
	mtimeUnixNano int64
}

// transcriptExecCache memoises isSessionExecuting results. It bounds the
// large-K cold-cache case: when GetRunningSessionIDs inspects K live Claude
// processes, an unchanged live-PID transcript (same size+mtime) is a cache hit
// and skips a full JSONL reparse, so cold cost is bounded by the number of
// *changed* live transcripts rather than by K itself.
//
// For actively-running sessions the transcript size/mtime changes per write, so
// those keys miss and re-evaluate every refresh — the cache mainly helps idle
// and repeated-list-refresh paths, which is exactly where the reparsing cost
// was pathological.
type transcriptExecCache struct {
	mu      sync.Mutex
	entries map[transcriptExecKey]bool
	hits    atomic.Int64
	misses  atomic.Int64
}

var transcriptExec = &transcriptExecCache{entries: make(map[transcriptExecKey]bool)}

func (c *transcriptExecCache) reset() {
	c.mu.Lock()
	c.entries = make(map[transcriptExecKey]bool)
	c.mu.Unlock()
	c.hits.Store(0)
	c.misses.Store(0)
}

// isSessionExecutingCached returns the executing state of sessionPath, memoised
// by (sessionID, path, size, mtime). An unchanged fingerprint is a cache hit and
// skips the JSONL reparse. A changed fingerprint (size OR mtime) re-evaluates.
// On stat error the cache is bypassed and isSessionExecuting runs uncached
// (preserving the original fail-open-idle behaviour).
func isSessionExecutingCached(sessionID, sessionPath string) bool {
	info, err := os.Stat(sessionPath)
	if err != nil {
		return isSessionExecuting(sessionPath)
	}
	key := transcriptExecKey{
		sessionID:     sessionID,
		path:          sessionPath,
		size:          info.Size(),
		mtimeUnixNano: info.ModTime().UnixNano(),
	}
	c := transcriptExec
	c.mu.Lock()
	if v, ok := c.entries[key]; ok {
		c.mu.Unlock()
		c.hits.Add(1)
		return v
	}
	c.mu.Unlock()
	v := isSessionExecuting(sessionPath)
	c.mu.Lock()
	c.entries[key] = v
	c.mu.Unlock()
	c.misses.Add(1)
	return v
}
