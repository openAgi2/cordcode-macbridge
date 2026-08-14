package gobridge

import (
	"encoding/json"
	"time"
)

const (
	defaultProjectionJournalMaxPatches = 128
	defaultProjectionJournalMaxBytes   = 2 << 20
	defaultProjectionJournalMaxAge     = 30 * time.Minute
)

type ProjectionResumeFallbackReason string

const (
	ProjectionResumeJournalGap ProjectionResumeFallbackReason = "journal_gap"
	ProjectionResumeLimit      ProjectionResumeFallbackReason = "limit"
)

type projectionJournalKey struct {
	backendID string
	sessionID string
}

// projectionJournalEntry keeps transport metadata outside the canonical patch payload. Callers
// return only patch, preserving the six wire fields byte-for-byte after typed encode.
type projectionJournalEntry struct {
	patch        ProjectionPatch
	encodedBytes int
	committedAt  time.Time
}

// ProjectionRevisionJournal is process/bridge-epoch scoped by its EventPublisher owner. It is
// intentionally not a session store: it retains only a bounded suffix of already-committed patch
// values and cannot construct projections or substitute for an authoritative full snapshot.
// All methods are called while EventPublisher.mu is held; the journal has no independent lock.
type ProjectionRevisionJournal struct {
	maxPatches int
	maxBytes   int
	maxAge     time.Duration
	entries    map[projectionJournalKey][]projectionJournalEntry
	bytes      map[projectionJournalKey]int
	// retainedAfter is the greatest revision removed by an explicit retention boundary. It is
	// envelope metadata only; it never enters or alters the canonical six-field patch payload.
	retainedAfter map[projectionJournalKey]int
}

func NewProjectionRevisionJournal(maxPatches, maxBytes int, maxAge ...time.Duration) *ProjectionRevisionJournal {
	if maxPatches <= 0 {
		maxPatches = defaultProjectionJournalMaxPatches
	}
	if maxBytes <= 0 {
		maxBytes = defaultProjectionJournalMaxBytes
	}
	age := defaultProjectionJournalMaxAge
	if len(maxAge) > 0 && maxAge[0] > 0 {
		age = maxAge[0]
	}
	return &ProjectionRevisionJournal{
		maxPatches:    maxPatches,
		maxBytes:      maxBytes,
		maxAge:        age,
		entries:       make(map[projectionJournalKey][]projectionJournalEntry),
		bytes:         make(map[projectionJournalKey]int),
		retainedAfter: make(map[projectionJournalKey]int),
	}
}

func (j *ProjectionRevisionJournal) Record(backendID, sessionID string, patch ProjectionPatch, recordedAt ...time.Time) bool {
	if j == nil || backendID == "" || sessionID == "" || patch.SyncRev <= patch.BaseRev {
		return false
	}
	cloned := cloneProjectionPatch(patch)
	now := time.Now()
	if len(recordedAt) > 0 && !recordedAt[0].IsZero() {
		now = recordedAt[0]
	}
	encoded, err := json.Marshal(cloned)
	if err != nil || len(encoded) > j.maxBytes {
		j.clearForLimit(backendID, sessionID, patch.SyncRev)
		return false
	}
	key := projectionJournalKey{backendID: backendID, sessionID: sessionID}
	j.purgeExpired(key, now)
	entries := j.entries[key]
	if len(entries) > 0 {
		last := entries[len(entries)-1].patch
		switch {
		case patch.SyncRev <= last.SyncRev:
			return false
		case patch.BaseRev != last.SyncRev:
			entries = nil
			j.bytes[key] = 0
		}
	}
	entry := projectionJournalEntry{patch: cloned, encodedBytes: len(encoded), committedAt: now}
	entries = append(entries, entry)
	j.bytes[key] += entry.encodedBytes
	for len(entries) > j.maxPatches || j.bytes[key] > j.maxBytes {
		j.noteRetention(key, entries[0].patch.SyncRev)
		j.bytes[key] -= entries[0].encodedBytes
		entries = entries[1:]
	}
	j.entries[key] = entries
	return true
}

// ContiguousRange returns an immutable patch suffix covering exactly sinceRev→headRev. A gap,
// retention miss, or journal head mismatch returns ok=false so the caller serves a full snapshot.
func (j *ProjectionRevisionJournal) ContiguousRange(
	backendID, sessionID string,
	sinceRev, headRev int,
) ([]ProjectionPatch, bool) {
	patches, ok, _ := j.ContiguousRangeAt(backendID, sessionID, sinceRev, headRev, time.Now())
	return patches, ok
}

// ContiguousRangeAt additionally classifies an unavailable suffix as a retention limit or a
// true revision gap. The caller uses that typed reason only in the outer RPC resume envelope.
func (j *ProjectionRevisionJournal) ContiguousRangeAt(
	backendID, sessionID string,
	sinceRev, headRev int,
	now time.Time,
) ([]ProjectionPatch, bool, ProjectionResumeFallbackReason) {
	if j == nil || sinceRev <= 0 || headRev <= sinceRev {
		return nil, false, ProjectionResumeJournalGap
	}
	key := projectionJournalKey{backendID: backendID, sessionID: sessionID}
	j.purgeExpired(key, now)
	entries := j.entries[key]
	if sinceRev < j.retainedAfter[key] {
		return nil, false, ProjectionResumeLimit
	}
	patches := make([]ProjectionPatch, 0, len(entries))
	expected := sinceRev
	started := false
	for _, entry := range entries {
		patch := entry.patch
		if !started {
			if patch.BaseRev != expected {
				continue
			}
			started = true
		}
		if patch.BaseRev != expected || patch.SyncRev > headRev {
			return nil, false, ProjectionResumeJournalGap
		}
		patches = append(patches, cloneProjectionPatch(patch))
		expected = patch.SyncRev
		if expected == headRev {
			return patches, true, ""
		}
	}
	return nil, false, ProjectionResumeJournalGap
}

func (j *ProjectionRevisionJournal) clear(backendID, sessionID string) {
	if j == nil {
		return
	}
	key := projectionJournalKey{backendID: backendID, sessionID: sessionID}
	delete(j.entries, key)
	delete(j.bytes, key)
}

func (j *ProjectionRevisionJournal) clearForLimit(backendID, sessionID string, throughRev int) {
	if j == nil {
		return
	}
	key := projectionJournalKey{backendID: backendID, sessionID: sessionID}
	j.noteRetention(key, throughRev)
	delete(j.entries, key)
	delete(j.bytes, key)
}

func (j *ProjectionRevisionJournal) noteRetention(key projectionJournalKey, throughRev int) {
	if throughRev > j.retainedAfter[key] {
		j.retainedAfter[key] = throughRev
	}
}

func (j *ProjectionRevisionJournal) purgeExpired(key projectionJournalKey, now time.Time) {
	entries := j.entries[key]
	for len(entries) > 0 && now.Sub(entries[0].committedAt) > j.maxAge {
		j.noteRetention(key, entries[0].patch.SyncRev)
		j.bytes[key] -= entries[0].encodedBytes
		entries = entries[1:]
	}
	if len(entries) == 0 {
		delete(j.entries, key)
		delete(j.bytes, key)
		return
	}
	j.entries[key] = entries
}

func cloneProjectionPatch(patch ProjectionPatch) ProjectionPatch {
	cloned := ProjectionPatch{
		BaseRev: patch.BaseRev,
		SyncRev: patch.SyncRev,
	}
	if patch.Execution != nil {
		execution := *patch.Execution
		cloned.Execution = &execution
	}
	for _, turn := range patch.UpsertTurns {
		cloned.UpsertTurns = append(cloned.UpsertTurns, cloneTurn(turn))
	}
	for _, op := range patch.PartOps {
		clonedOp := PartOp{
			TurnID: op.TurnID, MessageID: op.MessageID, Op: op.Op, Text: op.Text,
		}
		if op.Part != nil {
			part := cloneProjectionPart(*op.Part)
			clonedOp.Part = &part
		}
		for _, part := range op.Parts {
			clonedOp.Parts = append(clonedOp.Parts, cloneProjectionPart(part))
		}
		cloned.PartOps = append(cloned.PartOps, clonedOp)
	}
	cloned.ReplacesClientIDs = append([]string(nil), patch.ReplacesClientIDs...)
	return cloned
}
