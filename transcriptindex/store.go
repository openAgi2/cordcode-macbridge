package transcriptindex

import (
	"context"
	"fmt"
	"os"
	"sync"
)

const (
	defaultMaxConcurrentBuilds = 2
	defaultMaxInFlightBytes    = 64 << 20 // 64 MiB
)

// IndexRequest identifies one transcript to index.
type IndexRequest struct {
	Backend  Backend
	FilePath string
}

// StoreOption configures a Store.
type StoreOption func(*Store)

// WithMaxConcurrentBuilds caps the number of index builds/refreshes that may
// run at once across all sessions (global I/O/CPU budget, design §6.4).
func WithMaxConcurrentBuilds(n int) StoreOption {
	return func(s *Store) {
		if n > 0 {
			s.maxConcurrent = n
		}
	}
}

// WithMaxInFlightBytes caps the approximate bytes held by in-flight index work
// across all sessions (global memory budget, design §6.4).
func WithMaxInFlightBytes(n int64) StoreOption {
	return func(s *Store) {
		if n > 0 {
			s.maxInFlightBytes = n
		}
	}
}

// Store manages transcript indexes with per-session singleflight and global
// concurrency / in-flight-byte budgets. It is safe for concurrent use.
//
// Per the design (§6.3): concurrent get_session_messages for the same session
// reuse one in-flight index task; different sessions proceed in parallel within
// the global budgets, and no two tasks ever scan and overwrite the same index
// file simultaneously.
type Store struct {
	baseDir string

	maxConcurrent    int
	maxInFlightBytes int64

	mu            sync.Mutex
	inFlight      map[string]*indexTask
	buildSem      chan struct{}
	inFlightMu    sync.Mutex
	inFlightBytes int64
}

type indexTask struct {
	done chan struct{}
	idx  *PageIndex
	err  error
}

// NewStore returns a Store rooted at baseDir. The index directory is created
// lazily on first Save.
func NewStore(baseDir string, opts ...StoreOption) *Store {
	s := &Store{
		baseDir:          baseDir,
		inFlight:         make(map[string]*indexTask),
		maxConcurrent:    defaultMaxConcurrentBuilds,
		maxInFlightBytes: defaultMaxInFlightBytes,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.buildSem = make(chan struct{}, s.maxConcurrent)
	return s
}

func sessionKey(backend Backend, filePath string) string {
	return string(backend) + "|" + filePath
}

// Ensure builds or refreshes the index for req, reusing in-flight work for the
// same session identity and honoring global budgets. The result is persisted
// best-effort. Callers may then resolve pages with PageIndex.Replay.
func (s *Store) Ensure(ctx context.Context, req IndexRequest) (*PageIndex, error) {
	key := sessionKey(req.Backend, req.FilePath)

	s.mu.Lock()
	if t, ok := s.inFlight[key]; ok {
		s.mu.Unlock()
		select {
		case <-t.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return t.idx, t.err
	}
	task := &indexTask{done: make(chan struct{})}
	s.inFlight[key] = task
	s.mu.Unlock()

	idx, err := s.buildWithBudgets(ctx, req)
	// Publish before closing so waiters observe the result.
	task.idx = idx
	task.err = err
	close(task.done)

	s.mu.Lock()
	if cur, ok := s.inFlight[key]; ok && cur == task {
		delete(s.inFlight, key)
	}
	s.mu.Unlock()
	return idx, err
}

// buildWithBudgets loads+refreshes a persisted index when usable, else builds
// fresh, under the global concurrency and byte budgets.
func (s *Store) buildWithBudgets(ctx context.Context, req IndexRequest) (*PageIndex, error) {
	stat, err := os.Stat(req.FilePath)
	if err != nil {
		return nil, err
	}
	existing, _ := Load(s.baseDir, req.Backend, req.FilePath)

	select {
	case s.buildSem <- struct{}{}:
		defer func() { <-s.buildSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Budget the full file size (conservative; a refresh re-parses less).
	if !s.acquireBytes(stat.Size()) {
		return nil, fmt.Errorf("transcriptindex: global in-flight byte budget exceeded (%d bytes for %s)", stat.Size(), req.FilePath)
	}
	defer s.releaseBytes(stat.Size())

	if existing != nil {
		if refreshed, rerr := Refresh(existing); rerr == nil {
			_ = Save(s.baseDir, refreshed)
			return refreshed, nil
		}
	}
	idx, berr := Build(req.Backend, req.FilePath)
	if berr != nil {
		return nil, berr
	}
	_ = Save(s.baseDir, idx)
	return idx, nil
}

func (s *Store) acquireBytes(n int64) bool {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	if s.inFlightBytes+n > s.maxInFlightBytes {
		return false
	}
	s.inFlightBytes += n
	return true
}

func (s *Store) releaseBytes(n int64) {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	s.inFlightBytes -= n
}
