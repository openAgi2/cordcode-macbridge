// Package pinstore persists MacBridge-owned session pin (置顶) metadata for backends that
// do not have a per-session MacBridge-owned sidecar (Codex, OpenCode). Claude Code uses its
// existing .cc-connect-session-meta sidecar instead, but the same store shape can serve it
// if a uniform global index is ever preferred.
//
// The store holds identity + pinnedAtMillis ONLY — never title/messageCount/modifiedAt.
// Session summaries are resolved from the real backend source by the caller (go-bridge
// handler) so pinned rows can never go stale. See docs/protocol/bridge-v1.md「Session Pinning」.
//
// On-disk layout: a single JSON document at <dir>/session-pins-v1.json:
//
//	{
//	  "version": 1,
//	  "pins": {
//	    "<backendId>|<scope>|<sessionId>": {
//	      "backendId": "codex",
//	      "sessionId": "...",
//	      "directory": "/Users/...",
//	      "pinnedAtMillis": 1783200000000
//	    }
//	  }
//	}
//
// Writes are serialized by an in-process sync.Mutex and persisted atomically via
// core.AtomicWriteFile (temp file + os.Rename), mirroring the Claude session sidecar
// discipline. Concurrent SetPinned/Remove calls never lose updates.
package pinstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Filename is the single JSON file backing the store.
const Filename = "session-pins-v1.json"

// Entry is one persisted pin record. Directory is the scope hint captured at pin time
// (used by the handler to resolve the summary, e.g. OpenCodeProxy.getSession(id, dir)).
type Entry struct {
	BackendID      string `json:"backendId"`
	SessionID      string `json:"sessionId"`
	Directory      string `json:"directory,omitempty"`
	PinnedAtMillis int64  `json:"pinnedAtMillis"`
}

// PinnedAt returns the pinned timestamp as a time.Time (zero when not pinned).
func (e Entry) PinnedAt() time.Time {
	if e.PinnedAtMillis <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(e.PinnedAtMillis).UTC()
}

// ToPin converts the persisted entry to a core.SessionPin.
func (e Entry) ToPin() core.SessionPin {
	return core.SessionPin{
		BackendID: e.BackendID,
		SessionID: e.SessionID,
		Directory: e.Directory,
		PinnedAt:  e.PinnedAt(),
	}
}

type pinFile struct {
	Version int               `json:"version"`
	Pins    map[string]*Entry `json:"pins"`
}

const fileVersion = 1

// Store is the concurrent-safe pin store backed by one JSON file under dir.
type Store struct {
	path string
	mu   sync.Mutex
}

// New returns a store backed by <dir>/session-pins-v1.json. The directory must already
// exist (callers create it as part of the bridge data dir). The JSON file is created
// lazily on first write; a missing file reads as an empty store.
func New(dir string) *Store {
	return &Store{path: filepath.Join(dir, Filename)}
}

// FromOpts extracts a *Store from an agent construction opts map (key "pin_store").
// Returns nil if absent or wrong type, so agents built without a store (e.g. unit tests
// that do not exercise pinning) degrade gracefully — their SessionPinner methods then
// return ErrStoreUnavailable. go-bridge main injects one process-wide store for production.
func FromOpts(opts map[string]any) *Store {
	if s, ok := opts["pin_store"].(*Store); ok {
		return s
	}
	return nil
}

// ErrStoreUnavailable is returned by Store-bound SessionPinner receivers when the store
// was not injected at construction (only happens in non-production paths).
var ErrStoreUnavailable = fmt.Errorf("pinstore: store not configured for this agent")

// key builds the stable, collision-free pin key. The scope disambiguates backend
// instances / project directories / CODEX_HOME values so the same session ID discovered
// under different scopes never collides. It is computed by the caller (the agent that
// knows how to resolve its own backend scope), not by the store.
func key(backendID, scope, sessionID string) string {
	return backendID + "|" + scope + "|" + sessionID
}

// SetPinned records (pinned=true) or clears (pinned=false) a pin. pinnedAt is stored only
// when pinned=true; callers should pass the user-pinned time, not the session's updatedAt.
// directory is captured as the scope hint for later summary resolution.
func (s *Store) SetPinned(backendID, scope, sessionID, directory string, pinned bool, pinnedAt time.Time) error {
	if backendID == "" || sessionID == "" {
		return fmt.Errorf("pinstore: backendId and sessionId are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	pf, err := s.load()
	if err != nil {
		return err
	}
	k := key(backendID, scope, sessionID)
	if !pinned {
		delete(pf.Pins, k)
		return s.save(pf)
	}
	if pinnedAt.IsZero() {
		pinnedAt = time.Now().UTC()
	}
	pf.Pins[k] = &Entry{
		BackendID:      backendID,
		SessionID:      sessionID,
		Directory:      directory,
		PinnedAtMillis: pinnedAt.UTC().UnixMilli(),
	}
	return s.save(pf)
}

// Get returns the pin entry for the given scope, or (zero, false) when not pinned.
func (s *Store) Get(backendID, scope, sessionID string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return Entry{}, false
	}
	if e, ok := pf.Pins[key(backendID, scope, sessionID)]; ok {
		return *e, true
	}
	return Entry{}, false
}

// GetAnyScope returns the pin entry for sessionID under any scope/directory for the backend,
// or (zero, false) when not pinned anywhere. Used by list_sessions overlay when the caller
// (go-bridge) cannot compute the driver-internal scope (e.g. Codex CODEX_HOME). If multiple
// scopes match, returns the most-recently-pinned one.
func (s *Store) GetAnyScope(backendID, sessionID string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return Entry{}, false
	}
	var best Entry
	found := false
	for _, e := range pf.Pins {
		if e.BackendID == backendID && e.SessionID == sessionID {
			if !found || e.PinnedAtMillis > best.PinnedAtMillis {
				best = *e
				found = true
			}
		}
	}
	return best, found
}

// List returns all pin entries for the given backendID, in a stable order (by pinnedAt
// descending, then session ID). Used to serve list_pinned_sessions before enrichment.
func (s *Store) List(backendID string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return nil
	}
	var out []Entry
	for _, e := range pf.Pins {
		if e.BackendID == backendID {
			out = append(out, *e)
		}
	}
	// Sort: pinnedAt desc; tiebreak by sessionId for determinism.
	sortEntriesDesc(out)
	return out
}

// Remove deletes a single pin by scope. Used by driver DeleteSession cleanup. Missing keys
// are a no-op (delete cleanup must not fail when the session was never pinned).
func (s *Store) Remove(backendID, scope, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return err
	}
	delete(pf.Pins, key(backendID, scope, sessionID))
	return s.save(pf)
}

// RemoveEntry deletes a pin by matching backendID + sessionID + directory. Used by the
// list_pinned_sessions handler to prune definitively-missing sessions (e.g. OpenCode 404)
// without needing to recompute the scope string. It removes the first matching entry.
func (s *Store) RemoveEntry(backendID, sessionID, directory string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return false
	}
	for k, e := range pf.Pins {
		if e.BackendID == backendID && e.SessionID == sessionID && e.Directory == directory {
			delete(pf.Pins, k)
			if err := s.save(pf); err != nil {
				return false
			}
			return true
		}
	}
	return false
}

// RemoveAll deletes every pin for the given backendID + sessionID, regardless of scope or
// directory. Used by driver DeleteSession cleanup when the directory/scope is not known at
// delete time (e.g. OpenCode delete carries only sessionID). Returns the count removed.
// Missing keys are a no-op.
func (s *Store) RemoveAll(backendID, sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return 0
	}
	n := 0
	for k, e := range pf.Pins {
		if e.BackendID == backendID && e.SessionID == sessionID {
			delete(pf.Pins, k)
			n++
		}
	}
	if n > 0 {
		_ = s.save(pf)
	}
	return n
}

// load reads the file. A missing file is an empty store (not an error). A corrupt file is
// an error so we never silently lose real pin data. Caller holds s.mu.
func (s *Store) load() (*pinFile, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &pinFile{Version: fileVersion, Pins: map[string]*Entry{}}, nil
		}
		return nil, fmt.Errorf("pinstore: read %s: %w", s.path, err)
	}
	var pf pinFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("pinstore: decode %s: %w", s.path, err)
	}
	if pf.Pins == nil {
		pf.Pins = map[string]*Entry{}
	}
	if pf.Version == 0 {
		pf.Version = fileVersion
	}
	return &pf, nil
}

// save writes the file atomically. Caller holds s.mu.
func (s *Store) save(pf *pinFile) error {
	if pf.Version == 0 {
		pf.Version = fileVersion
	}
	if pf.Pins == nil {
		pf.Pins = map[string]*Entry{}
	}
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("pinstore: encode: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("pinstore: mkdir: %w", err)
	}
	return core.AtomicWriteFile(s.path, data, 0o600)
}

// ScopeKey is exported for callers/tests that need to predict the on-disk key shape.
func ScopeKey(backendID, scope, sessionID string) string {
	return key(backendID, scope, sessionID)
}

// sortEntriesDesc sorts by pinnedAtMillis desc, then sessionId asc. Stable for tests.
func sortEntriesDesc(es []Entry) {
	// Simple insertion sort: pin lists are tiny (bounded by user pin count).
	for i := 1; i < len(es); i++ {
		j := i
		for j > 0 && before(es[j], es[j-1]) {
			es[j], es[j-1] = es[j-1], es[j]
			j--
		}
	}
}

func before(a, b Entry) bool {
	if a.PinnedAtMillis != b.PinnedAtMillis {
		return a.PinnedAtMillis > b.PinnedAtMillis
	}
	return strings.Compare(a.SessionID, b.SessionID) < 0
}
