package gobridge

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

const passiveErrorLogInterval = 30 * time.Second

// passiveEventLogMaxStates bounds diagnostic memory under high-cardinality changes.
const passiveEventLogMaxStates = 4096

// passiveEventLogState is bounded diagnostic metadata. It never retains the raw
// error text or stable session id; only short one-way fingerprints and counts.
type passiveEventLogState struct {
	count            uint64
	errorFingerprint string
	firstSeen        time.Time
	lastLogged       time.Time
}

type passiveEventAggregator struct {
	mu     sync.Mutex
	now    func() time.Time
	states map[string]passiveEventLogState
}

func newPassiveEventAggregator() *passiveEventAggregator {
	return &passiveEventAggregator{
		now:    time.Now,
		states: make(map[string]passiveEventLogState),
	}
}

// observe records one event and reports whether its next log record is due.
// First occurrence and every error-identity change are due immediately;
// otherwise repeats are summarized on a bounded cadence.
func (a *passiveEventAggregator) observe(backendID, sessionID, eventName string, eventErr error) (bool, []any) {
	if a == nil {
		return true, nil
	}
	now := a.now()
	if now.IsZero() {
		now = time.Now()
	}
	sessionFP := passiveFingerprint(sessionID)
	errorFP := ""
	if eventErr != nil {
		errorFP = passiveFingerprint(eventErr.Error())
	}
	key := backendID + "|" + sessionFP + "|" + eventName

	a.mu.Lock()
	defer a.mu.Unlock()
	state, exists := a.states[key]
	identityChanged := exists && state.errorFingerprint != errorFP
	if !exists || identityChanged {
		if len(a.states) >= passiveEventLogMaxStates {
			// A high-cardinality identity change must not grow an unbounded diagnostic
			// map. Reset summaries; the new state still logs immediately.
			a.states = make(map[string]passiveEventLogState)
		}
		state = passiveEventLogState{
			count:            1,
			errorFingerprint: errorFP,
			firstSeen:        now,
			lastLogged:       now,
		}
		a.states[key] = state
		reason := "first"
		if identityChanged {
			reason = "error_identity_changed"
		}
		return true, passiveEventLogAttrs(reason, state, now, sessionFP, errorFP)
	}

	state.count++
	state.lastLogged = state.lastLoggedInFuture(now)
	a.states[key] = state
	if now.Sub(state.lastLogged) < passiveErrorLogInterval {
		return false, nil
	}
	state.lastLogged = now
	a.states[key] = state
	return true, passiveEventLogAttrs("repeat", state, now, sessionFP, errorFP)
}

// lastLoggedInFuture guards against a regressing clock. It reports the effective
// last-log time: zero lets the next event log rather than suppressing forever.
func (s passiveEventLogState) lastLoggedInFuture(now time.Time) time.Time {
	if s.lastLogged.After(now) {
		return time.Time{}
	}
	return s.lastLogged
}

func passiveEventLogAttrs(reason string, state passiveEventLogState, now time.Time, sessionFP, errorFP string) []any {
	elapsed := now.Sub(state.firstSeen)
	ratePerSecond := 0.0
	if elapsed > 0 {
		ratePerSecond = float64(state.count) / elapsed.Seconds()
	}
	return []any{
		"reason", reason,
		"count", state.count,
		"ratePerSecond", ratePerSecond,
		"sessionFingerprint", sessionFP,
		"errorFingerprint", errorFP,
		"firstSeenUnixMilli", state.firstSeen.UnixMilli(),
	}
}

func passiveFingerprint(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
