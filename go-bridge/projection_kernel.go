package gobridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Version 4 persists every physical segment of a compact-linked Claude session.
// Older checkpoints cannot prove the complete composite source cut.
// projectionCheckpointSchemaVersion is bumped when the projection reducer's
// output shape or hydrate baseline semantics change, so stale derived state is
// invalidated and rebuilt from the canonical source. v5: pathless hydrate no
// longer carries a prior projection as baseline (see BeginHydrateTransaction),
// which changes the turn set produced for pathless backends — old checkpoints
// that mixed live row-UUID turns with builder user-line-N turns must be rebuilt.
const projectionCheckpointSchemaVersion = 5

var (
	ErrProjectionCheckpointInvalid  = errors.New("projection checkpoint invalid")
	ErrProjectionCheckpointDisabled = errors.New("projection checkpoint persistence disabled")
)

type ProjectionHydratePhase string

const (
	ProjectionHydrateAbsent    ProjectionHydratePhase = "absent"
	ProjectionHydrateHydrating ProjectionHydratePhase = "hydrating"
	ProjectionHydrateReady     ProjectionHydratePhase = "ready"
	ProjectionHydrateFailed    ProjectionHydratePhase = "failed"
)

type ProjectionHydrateFailure struct {
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
	RetryAt   time.Time `json:"retryAt,omitempty"`
	Attempts  int       `json:"attempts"`
}

type ProjectionHydrationStatus struct {
	Phase   ProjectionHydratePhase    `json:"phase"`
	Failure *ProjectionHydrateFailure `json:"failure,omitempty"`
}

type ProjectionSourceDescriptor struct {
	Identity string
	Path     string
	Cursor   int64
	Segments []ProjectionSourceSegment
}

type ProjectionSourceSegment struct {
	Identity string
	Path     string
	Cursor   int64
}

type ProjectionSourceCheckpoint struct {
	Identity       string `json:"identity"`
	Cursor         int64  `json:"cursor"`
	PrefixSHA256   string `json:"prefixSha256"`
	ObservedSize   int64  `json:"observedSize"`
	ObservedMTimeN int64  `json:"observedMTimeNanos,omitempty"`
}

type ProjectionCheckpoint struct {
	SchemaVersion     int                          `json:"schemaVersion"`
	BackendID         string                       `json:"backendId"`
	SessionID         string                       `json:"sessionId"`
	Source            ProjectionSourceCheckpoint   `json:"source"`
	Sources           []ProjectionSourceCheckpoint `json:"sources,omitempty"`
	Projection        SessionProjection            `json:"projection"`
	ProjectionRev     int                          `json:"projectionRev"`
	HydrateState      ProjectionHydratePhase       `json:"hydrateState"`
	UpdatedAt         time.Time                    `json:"updatedAt"`
	ClaudeSourceState *ClaudeSourceState           `json:"claudeSourceState,omitempty"`
}

type projectionCheckpointPersistence interface {
	LoadValidated(backendID, sessionID string, source ProjectionSourceDescriptor) (ProjectionCheckpoint, error)
	Save(ProjectionCheckpoint) error
}

type ProjectionCheckpointStore struct {
	baseDir      string
	beforeRename func(tempPath, checkpointPath string) error
}

func NewProjectionCheckpointStore(dataDir string) *ProjectionCheckpointStore {
	baseDir := ""
	if dataDir != "" {
		baseDir = filepath.Join(dataDir, "session-projection", "checkpoints")
	}
	return &ProjectionCheckpointStore{baseDir: baseDir}
}

func projectionCheckpointFilename(backendID, sessionID string) string {
	sum := sha256.Sum256([]byte(backendID + "\x00" + sessionID))
	return hex.EncodeToString(sum[:]) + ".json"
}

func (s *ProjectionCheckpointStore) checkpointPath(backendID, sessionID string) (string, error) {
	if s == nil || s.baseDir == "" {
		return "", ErrProjectionCheckpointDisabled
	}
	if backendID == "" || sessionID == "" {
		return "", fmt.Errorf("%w: empty backend/session identity", ErrProjectionCheckpointInvalid)
	}
	return filepath.Join(s.baseDir, projectionCheckpointFilename(backendID, sessionID)), nil
}

func BuildProjectionSourceCheckpoint(source ProjectionSourceDescriptor) (ProjectionSourceCheckpoint, error) {
	if source.Identity == "" || source.Path == "" || source.Cursor < 0 {
		return ProjectionSourceCheckpoint{}, fmt.Errorf("%w: incomplete source descriptor", ErrProjectionCheckpointInvalid)
	}
	f, err := os.Open(source.Path)
	if err != nil {
		return ProjectionSourceCheckpoint{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ProjectionSourceCheckpoint{}, err
	}
	if info.Size() < source.Cursor {
		return ProjectionSourceCheckpoint{}, fmt.Errorf(
			"%w: source truncated size=%d cursor=%d",
			ErrProjectionCheckpointInvalid,
			info.Size(),
			source.Cursor,
		)
	}
	digest, err := digestProjectionSourcePrefix(f, source.Cursor)
	if err != nil {
		return ProjectionSourceCheckpoint{}, err
	}
	return ProjectionSourceCheckpoint{
		Identity:       source.Identity,
		Cursor:         source.Cursor,
		PrefixSHA256:   digest,
		ObservedSize:   info.Size(),
		ObservedMTimeN: info.ModTime().UnixNano(),
	}, nil
}

func buildProjectionSegmentCheckpoint(segment ProjectionSourceSegment) (ProjectionSourceCheckpoint, error) {
	return BuildProjectionSourceCheckpoint(ProjectionSourceDescriptor{
		Identity: segment.Identity,
		Path:     segment.Path,
		Cursor:   segment.Cursor,
	})
}

func BuildProjectionSourceCheckpoints(
	source ProjectionSourceDescriptor,
) ([]ProjectionSourceCheckpoint, error) {
	if len(source.Segments) == 0 {
		checkpoint, err := BuildProjectionSourceCheckpoint(source)
		if err != nil {
			return nil, err
		}
		return []ProjectionSourceCheckpoint{checkpoint}, nil
	}
	checkpoints := make([]ProjectionSourceCheckpoint, 0, len(source.Segments))
	for _, segment := range source.Segments {
		checkpoint, err := buildProjectionSegmentCheckpoint(segment)
		if err != nil {
			return nil, err
		}
		checkpoints = append(checkpoints, checkpoint)
	}
	return checkpoints, nil
}

func digestProjectionSourcePrefix(r io.Reader, cursor int64) (string, error) {
	if cursor < 0 {
		return "", fmt.Errorf("%w: negative source cursor", ErrProjectionCheckpointInvalid)
	}
	h := sha256.New()
	if cursor > 0 {
		n, err := io.CopyN(h, r, cursor)
		if err != nil {
			return "", fmt.Errorf("%w: hash prefix read %d/%d: %v", ErrProjectionCheckpointInvalid, n, cursor, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *ProjectionCheckpointStore) LoadValidated(
	backendID, sessionID string,
	source ProjectionSourceDescriptor,
) (ProjectionCheckpoint, error) {
	path, err := s.checkpointPath(backendID, sessionID)
	if err != nil {
		return ProjectionCheckpoint{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ProjectionCheckpoint{}, err
	}
	var checkpoint ProjectionCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: decode: %v", ErrProjectionCheckpointInvalid, err)
	}
	if checkpoint.SchemaVersion != projectionCheckpointSchemaVersion ||
		checkpoint.BackendID != backendID ||
		checkpoint.SessionID != sessionID ||
		checkpoint.Projection.SessionID != sessionID ||
		checkpoint.HydrateState != ProjectionHydrateReady ||
		checkpoint.ProjectionRev != checkpoint.Projection.SyncRev {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: schema/identity/state mismatch", ErrProjectionCheckpointInvalid)
	}
	if checkpoint.ClaudeSourceState != nil {
		if err := ValidateClaudeSourceState(*checkpoint.ClaudeSourceState); err != nil {
			return ProjectionCheckpoint{}, err
		}
	}
	if source.Identity == "" {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: source identity mismatch", ErrProjectionCheckpointInvalid)
	}
	if len(source.Segments) > 0 {
		if len(checkpoint.Sources) != len(source.Segments) {
			return ProjectionCheckpoint{}, fmt.Errorf("%w: composite source membership mismatch", ErrProjectionCheckpointInvalid)
		}
		for index, segment := range source.Segments {
			stored := checkpoint.Sources[index]
			if stored.Identity != segment.Identity || stored.Cursor != segment.Cursor {
				return ProjectionCheckpoint{}, fmt.Errorf("%w: composite source cut mismatch", ErrProjectionCheckpointInvalid)
			}
			if err := validateProjectionSourceCheckpoint(segment.Path, stored); err != nil {
				return ProjectionCheckpoint{}, err
			}
		}
		checkpoint.Projection = cloneSessionProjection(checkpoint.Projection)
		return checkpoint, nil
	}
	if checkpoint.Source.Identity != source.Identity {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: source identity mismatch", ErrProjectionCheckpointInvalid)
	}
	if source.Path == "" {
		return ProjectionCheckpoint{}, fmt.Errorf("%w: source path missing", ErrProjectionCheckpointInvalid)
	}
	if err := validateProjectionSourceCheckpoint(source.Path, checkpoint.Source); err != nil {
		return ProjectionCheckpoint{}, err
	}
	checkpoint.Projection = cloneSessionProjection(checkpoint.Projection)
	return checkpoint, nil
}

func validateProjectionSourceCheckpoint(path string, checkpoint ProjectionSourceCheckpoint) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < checkpoint.Cursor {
		return fmt.Errorf(
			"%w: source truncated size=%d cursor=%d",
			ErrProjectionCheckpointInvalid,
			info.Size(),
			checkpoint.Cursor,
		)
	}
	digest, err := digestProjectionSourcePrefix(f, checkpoint.Cursor)
	if err != nil {
		return err
	}
	if digest != checkpoint.PrefixSHA256 {
		return fmt.Errorf("%w: consumed prefix digest mismatch", ErrProjectionCheckpointInvalid)
	}
	return nil
}

func (s *ProjectionCheckpointStore) Save(checkpoint ProjectionCheckpoint) error {
	path, err := s.checkpointPath(checkpoint.BackendID, checkpoint.SessionID)
	if err != nil {
		return err
	}
	if checkpoint.SchemaVersion != projectionCheckpointSchemaVersion ||
		checkpoint.HydrateState != ProjectionHydrateReady ||
		checkpoint.Projection.SessionID != checkpoint.SessionID ||
		!projectionCheckpointHasCompleteSource(checkpoint) ||
		checkpoint.ProjectionRev != checkpoint.Projection.SyncRev {
		return fmt.Errorf("%w: refused incomplete checkpoint", ErrProjectionCheckpointInvalid)
	}
	if checkpoint.ClaudeSourceState != nil {
		if err := ValidateClaudeSourceState(*checkpoint.ClaudeSourceState); err != nil {
			return err
		}
	}
	checkpoint.Projection = cloneSessionProjection(checkpoint.Projection)
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".projection-checkpoint-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		_ = tmp.Close()
		if keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if s.beforeRename != nil {
		if err := s.beforeRename(tmpPath, path); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keepTemp = false
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func projectionCheckpointHasCompleteSource(checkpoint ProjectionCheckpoint) bool {
	if len(checkpoint.Sources) > 0 {
		for _, source := range checkpoint.Sources {
			if source.Identity == "" || source.Cursor < 0 || source.PrefixSHA256 == "" {
				return false
			}
		}
		return true
	}
	return checkpoint.Source.Identity != "" &&
		checkpoint.Source.Cursor >= 0 &&
		checkpoint.Source.PrefixSHA256 != ""
}

type ProjectionRetryPolicy struct {
	Initial        time.Duration
	Maximum        time.Duration
	JitterFraction float64
}

func DefaultProjectionRetryPolicy() ProjectionRetryPolicy {
	return ProjectionRetryPolicy{
		Initial:        projectionHydrateRetryInitial,
		Maximum:        projectionHydrateRetryMaximum,
		JitterFraction: projectionHydrateRetryJitterFraction,
	}
}

func (p ProjectionRetryPolicy) delay(attempts int, randomUnit float64) time.Duration {
	if p.Initial <= 0 {
		p.Initial = time.Second
	}
	if p.Maximum < p.Initial {
		p.Maximum = p.Initial
	}
	if attempts < 1 {
		attempts = 1
	}
	delay := p.Initial
	for i := 1; i < attempts && delay < p.Maximum; i++ {
		if delay > p.Maximum/2 {
			delay = p.Maximum
			break
		}
		delay *= 2
	}
	if delay > p.Maximum {
		delay = p.Maximum
	}
	if p.JitterFraction <= 0 {
		return delay
	}
	if randomUnit < 0 {
		randomUnit = 0
	}
	if randomUnit > 1 {
		randomUnit = 1
	}
	factor := 1 + ((randomUnit*2)-1)*p.JitterFraction
	jittered := time.Duration(float64(delay) * factor)
	if jittered > p.Maximum {
		return p.Maximum
	}
	return jittered
}

type ProjectionCheckpointPolicy struct {
	MaxInterval  time.Duration
	MaxRevisions int
}

func DefaultProjectionCheckpointPolicy() ProjectionCheckpointPolicy {
	return ProjectionCheckpointPolicy{
		MaxInterval:  2 * time.Second,
		MaxRevisions: 128,
	}
}

type projectionKernelSession struct {
	status                ProjectionHydrationStatus
	failureAttempts       int
	hydrate               *projectionHydrateTransaction
	hydrateDone           chan struct{}
	lastPersistedRev      int
	committedSourceCursor int64 // last transcript cut committed into SoT; catch-up when source advances
	committedSource       ProjectionSourceDescriptor
	claudeSourceState     *ClaudeSourceState
	pending               *ProjectionCheckpoint
	writeInFlight         bool
	timer                 *time.Timer
	forceWrite            bool
	waiters               []chan struct{}
	lastWriteErr          error
}

type projectionHydrateTransaction struct {
	source      ProjectionSourceDescriptor
	startCursor int64
	startCut    int64
	reducer     *ProjectionReducer
	nextInput   int
	pendingLive []EventMessage
	liveArrived chan struct{}
}

type ProjectionHydrateAdmission struct {
	Leader        bool
	AlreadyReady  bool
	StartCursor   int64
	StartCut      int64
	CheckpointHit bool
	Done          <-chan struct{}
}

type ProjectionHydrateCommit struct {
	Projection   SessionProjection
	PendingLive  int
	PendingPatch *ProjectionPatch
}

type ProjectionKernel struct {
	mu               sync.Mutex
	reducer          *ProjectionReducer
	store            projectionCheckpointPersistence
	sessions         map[string]*projectionKernelSession
	retryPolicy      ProjectionRetryPolicy
	checkpointPolicy ProjectionCheckpointPolicy
	now              func() time.Time
	randomUnit       func() float64
}

func NewProjectionKernel(reducer *ProjectionReducer, store projectionCheckpointPersistence) *ProjectionKernel {
	if reducer == nil {
		reducer = NewProjectionReducer()
	}
	return &ProjectionKernel{
		reducer:          reducer,
		store:            store,
		sessions:         make(map[string]*projectionKernelSession),
		retryPolicy:      DefaultProjectionRetryPolicy(),
		checkpointPolicy: DefaultProjectionCheckpointPolicy(),
		now:              time.Now,
		randomUnit:       rand.Float64,
	}
}

func (k *ProjectionKernel) SetCheckpointStore(store projectionCheckpointPersistence) {
	if k == nil {
		return
	}
	k.mu.Lock()
	k.store = store
	k.mu.Unlock()
}

func (k *ProjectionKernel) SetReducer(reducer *ProjectionReducer) {
	if k == nil || reducer == nil {
		return
	}
	k.mu.Lock()
	k.reducer = reducer
	k.mu.Unlock()
}

func (k *ProjectionKernel) sessionLocked(backendID, sessionID string) *projectionKernelSession {
	key := projectionSessionKey(backendID, sessionID)
	session := k.sessions[key]
	if session == nil {
		session = &projectionKernelSession{
			status: ProjectionHydrationStatus{Phase: ProjectionHydrateAbsent},
		}
		k.sessions[key] = session
	}
	return session
}

func (k *ProjectionKernel) Status(backendID, sessionID string) ProjectionHydrationStatus {
	k.mu.Lock()
	defer k.mu.Unlock()
	status := k.sessionLocked(backendID, sessionID).status
	if status.Failure != nil {
		failure := *status.Failure
		status.Failure = &failure
	}
	return status
}

func (k *ProjectionKernel) BeginHydrate(backendID, sessionID string, explicitRetry, sourceChanged bool) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	switch session.status.Phase {
	case ProjectionHydrateHydrating, ProjectionHydrateReady:
		return false
	case ProjectionHydrateFailed:
		failure := session.status.Failure
		if failure != nil {
			if !failure.Retryable && !sourceChanged {
				return false
			}
			if failure.Retryable && !explicitRetry && k.now().Before(failure.RetryAt) {
				return false
			}
		}
	}
	session.status = ProjectionHydrationStatus{Phase: ProjectionHydrateHydrating}
	return true
}

func (k *ProjectionKernel) MarkReady(backendID, sessionID string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	session.status = ProjectionHydrationStatus{Phase: ProjectionHydrateReady}
	session.hydrate = nil
	k.finishHydrateLocked(session)
	session.failureAttempts = 0
}

func (k *ProjectionKernel) MarkFailed(
	backendID, sessionID, code, message string,
	retryable bool,
) ProjectionHydrationStatus {
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	session.failureAttempts++
	attempts := session.failureAttempts
	failure := &ProjectionHydrateFailure{
		Code:      code,
		Message:   message,
		Retryable: retryable,
		Attempts:  attempts,
	}
	if retryable {
		failure.RetryAt = k.now().Add(k.retryPolicy.delay(attempts, k.randomUnit()))
	}
	session.status = ProjectionHydrationStatus{Phase: ProjectionHydrateFailed, Failure: failure}
	session.hydrate = nil
	k.finishHydrateLocked(session)
	return session.status
}

func (k *ProjectionKernel) finishHydrateLocked(session *projectionKernelSession) {
	if session != nil && session.hydrateDone != nil {
		close(session.hydrateDone)
		session.hydrateDone = nil
	}
}

// pathlessRichHistoryBackend reports whether a backend's pathless hydrate (source.Path
// == "") replays the full source through a rich-history builder: Claude (pathless or
// compact-continuation segments) and OpenCode (HTTP rich history). Such a rebuild must
// start from an empty reducer so the builder is the sole baseline; carrying a prior
// projection would replay content onto it and duplicate turns. Codex is file-based — a
// pathless Codex session is a degenerate no-file case with no builder replay, so it is
// excluded and keeps its carried live baseline.
func pathlessRichHistoryBackend(backendID string) bool {
	switch backendID {
	case "opencode", "claude", "claudecode":
		return true
	default:
		return false
	}
}

// BeginHydrateTransaction creates the isolated reducer used for [checkpointCursor,startCut).
// Live events that arrive after this cut are queued by IngestLive and cannot mutate the
// authoritative reducer until CommitHydrateTransaction publishes the baseline atomically.
func (k *ProjectionKernel) BeginHydrateTransaction(
	backendID, sessionID string,
	source ProjectionSourceDescriptor,
	explicitRetry, sourceChanged bool,
) (ProjectionHydrateAdmission, error) {
	if k == nil || backendID == "" || sessionID == "" || source.Cursor < 0 {
		return ProjectionHydrateAdmission{}, fmt.Errorf("%w: invalid hydrate admission", ErrProjectionCheckpointInvalid)
	}
	k.mu.Lock()
	session := k.sessionLocked(backendID, sessionID)
	switch session.status.Phase {
	case ProjectionHydrateReady:
		// A compact-linked transcript is one logical source with multiple
		// physical cuts. Any membership/order/cut change is rebuilt through a
		// new private transaction; it never bypasses source inspection.
		if len(source.Segments) > 0 && !projectionSourceDescriptorsEqual(source, session.committedSource) {
			break
		}
		// Source advanced past the committed cut (live relay gap / process-not-live miss).
		// Force a catch-up hydrate of [committedSourceCursor, source.Cursor) instead of
		// returning a stale AlreadyReady baseline.
		if source.Path != "" && source.Cursor > session.committedSourceCursor {
			break
		}
		// Pathless backends (OpenCode): no file cursor. sourceChanged forces a full
		// rich-history rebuild so re-open can heal live gaps (missing user prompts).
		if source.Path == "" && sourceChanged {
			break
		}
		k.mu.Unlock()
		return ProjectionHydrateAdmission{AlreadyReady: true}, nil
	case ProjectionHydrateHydrating:
		admission := ProjectionHydrateAdmission{Done: session.hydrateDone}
		if session.hydrate != nil {
			admission.StartCursor = session.hydrate.startCursor
			admission.StartCut = session.hydrate.startCut
		}
		k.mu.Unlock()
		return admission, nil
	case ProjectionHydrateFailed:
		failure := session.status.Failure
		if failure != nil {
			if !failure.Retryable && !sourceChanged {
				k.mu.Unlock()
				return ProjectionHydrateAdmission{}, nil
			}
			if failure.Retryable && !explicitRetry && k.now().Before(failure.RetryAt) {
				k.mu.Unlock()
				return ProjectionHydrateAdmission{}, nil
			}
		}
	}
	catchUpFrom := session.committedSourceCursor
	wasReadyCatchUp := session.status.Phase == ProjectionHydrateReady && source.Path != "" && source.Cursor > catchUpFrom
	tx := &projectionHydrateTransaction{
		source:      source,
		startCut:    source.Cursor,
		reducer:     NewProjectionReducer(),
		liveArrived: make(chan struct{}, 1),
	}
	if source.Path == "" {
		if pathlessRichHistoryBackend(backendID) {
			// Claude / OpenCode pathless rebuild starts EMPTY. Their rich-history builder
			// has no file cursor and re-reduces the full source every time, so the projection
			// must be derived SOLELY from that rebuild. Carrying a prior committed projection
			// (in-memory here, or from checkpoint below) would replay the builder on top of
			// already-present content — text_delta appends, never replaces — and, when the
			// prior baseline used a different turn-id scheme (live/raw row-UUID vs builder
			// user-line-N), create duplicate turns that persist in the checkpoint across
			// reopens. See docs/2026-07-31-claude-projection-pathless-hydrate-duplication-fix.md.
			// tx.reducer is already a fresh NewProjectionReducer(); do NOT Restore.
		} else if !sourceChanged {
			// Codex is file-based: a pathless Codex session is a degenerate no-file case
			// with NO builder replay, so the carried in-memory live state is the sole
			// content and must be preserved (do not drop it).
			if existing, ok := k.reducer.Snapshot(backendID, sessionID); ok {
				tx.reducer.Restore(backendID, sessionID, existing)
			}
		}
	}
	// In-memory catch-up: keep the already-committed projection as base and only
	// reduce the transcript gap after committedSourceCursor.
	if wasReadyCatchUp {
		if existing, ok := k.reducer.Snapshot(backendID, sessionID); ok {
			tx.reducer.Restore(backendID, sessionID, existing)
		}
	}
	session.status = ProjectionHydrationStatus{Phase: ProjectionHydrateHydrating}
	session.hydrate = tx
	session.hydrateDone = make(chan struct{})
	done := session.hydrateDone
	store := k.store
	k.mu.Unlock()

	checkpointHit := false
	startCursor := int64(0)
	if wasReadyCatchUp {
		startCursor = catchUpFrom
		checkpointHit = true // base already in memory; only gap remains
	} else if store != nil {
		checkpoint, err := store.LoadValidated(backendID, sessionID, source)
		switch {
		case err == nil:
			if !(source.Path == "" && pathlessRichHistoryBackend(backendID)) {
				// Carry the checkpoint projection forward as baseline — EXCEPT for a
				// Claude/OpenCode pathless rebuild, which must start empty so the rich-
				// history builder is the sole baseline (see Site A comment above).
				tx.reducer.Restore(backendID, sessionID, checkpoint.Projection)
			}
			if len(source.Segments) > 0 {
				startCursor = source.Cursor
			} else {
				startCursor = checkpoint.Source.Cursor
			}
			checkpointHit = true
		case errors.Is(err, os.ErrNotExist),
			errors.Is(err, ErrProjectionCheckpointInvalid),
			errors.Is(err, ErrProjectionCheckpointDisabled):
			// Missing or stale derived state is an honest full rebuild, not a fallback.
		default:
			k.MarkFailed(backendID, sessionID, "projection.checkpoint_read_failed", err.Error(), true)
			return ProjectionHydrateAdmission{}, err
		}
	}
	k.mu.Lock()
	current := k.sessionLocked(backendID, sessionID)
	if current.hydrate != tx || current.status.Phase != ProjectionHydrateHydrating {
		k.mu.Unlock()
		return ProjectionHydrateAdmission{}, errors.New("projection hydrate admission superseded")
	}
	tx.startCursor = startCursor
	admission := ProjectionHydrateAdmission{
		Leader:        true,
		StartCursor:   tx.startCursor,
		StartCut:      tx.startCut,
		CheckpointHit: checkpointHit,
		Done:          done,
	}
	k.mu.Unlock()
	return admission, nil
}

// ApplyHydrateEvent mutates only the transaction-local reducer. It never allocates EventPublisher
// seq, appends an EventBuffer, writes an offline queue, or emits a mailbox/frame.
func (k *ProjectionKernel) ApplyHydrateEvent(
	backendID, sessionID, bridgeEpoch string,
	event string,
	data map[string]interface{},
) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.status.Phase != ProjectionHydrateHydrating || session.hydrate == nil {
		return false
	}
	tx := session.hydrate
	tx.nextInput++
	before := tx.reducer.LastAppliedRev(backendID, sessionID)
	tx.reducer.Apply(EventMessage{
		BackendID:     backendID,
		SessionID:     sessionID,
		Event:         event,
		Data:          cloneProjectionJSONValue(data),
		PerSessionSeq: tx.nextInput,
		BridgeEpoch:   bridgeEpoch,
	})
	return tx.reducer.LastAppliedRev(backendID, sessionID) != before
}

// IngestLive is called from EventPublisher under its ordering lock. During hydrate it records
// a deep immutable pending event; otherwise it applies directly to the committed reducer.
func (k *ProjectionKernel) IngestLive(msg EventMessage) bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(msg.BackendID, msg.SessionID)
	if session.status.Phase == ProjectionHydrateHydrating && session.hydrate != nil {
		queued := msg
		queued.Data = cloneProjectionJSONValue(msg.Data)
		session.hydrate.pendingLive = append(session.hydrate.pendingLive, queued)
		select {
		case session.hydrate.liveArrived <- struct{}{}:
		default:
		}
		return false
	}
	before := k.reducer.LastAppliedRev(msg.BackendID, msg.SessionID)
	k.reducer.Apply(msg)
	return k.reducer.LastAppliedRev(msg.BackendID, msg.SessionID) != before
}

// WaitHydrateCommitReady distinguishes a truly empty inspected source from a bare turn shell.
// A bare shell remains hydrating until a post-cut live event contributes real content.
func (k *ProjectionKernel) WaitHydrateCommitReady(
	ctx context.Context,
	backendID, sessionID string,
) error {
	for {
		k.mu.Lock()
		session := k.sessionLocked(backendID, sessionID)
		if session.status.Phase != ProjectionHydrateHydrating || session.hydrate == nil {
			k.mu.Unlock()
			return errors.New("projection hydrate transaction is not active")
		}
		tx := session.hydrate
		preview := NewProjectionReducer()
		if baseline, ok := tx.reducer.Snapshot(backendID, sessionID); ok {
			preview.Restore(backendID, sessionID, baseline)
		}
		for _, msg := range tx.pendingLive {
			preview.Apply(msg)
		}
		turnCount := preview.TurnCount(backendID, sessionID)
		ready := turnCount == 0 || preview.HasContentTurn(backendID, sessionID)
		liveArrived := tx.liveArrived
		k.mu.Unlock()
		if ready {
			return nil
		}
		select {
		case <-liveArrived:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// CommitHydrateTransaction publishes one complete baseline, then applies post-cut live events
// in their original stamped order under the same Kernel lock.
func (k *ProjectionKernel) CommitHydrateTransaction(
	backendID, sessionID string,
) (ProjectionHydrateCommit, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.status.Phase != ProjectionHydrateHydrating || session.hydrate == nil {
		return ProjectionHydrateCommit{}, errors.New("projection hydrate transaction is not active")
	}
	tx := session.hydrate
	if (backendID == "claude" || backendID == "claudecode") && len(tx.pendingLive) > 0 {
		return ProjectionHydrateCommit{}, fmt.Errorf(
			"%w: Claude hydrate received uncorrelated live rows; re-inspect from one source owner",
			ErrProjectionCheckpointInvalid,
		)
	}
	baseline, ok := tx.reducer.Snapshot(backendID, sessionID)
	if !ok {
		baseline = SessionProjection{
			SessionID: sessionID,
			Execution: ExecutionView{Phase: "idle"},
			Turns:     []TurnProjection{},
		}
	}
	k.reducer.Restore(backendID, sessionID, baseline)
	for _, msg := range tx.pendingLive {
		k.reducer.Apply(msg)
	}
	committed, _ := k.reducer.Snapshot(backendID, sessionID)
	var patch *ProjectionPatch
	if pendingPatch, ok := k.reducer.FlushPatch(backendID, sessionID); ok {
		patch = &pendingPatch
	}
	session.status = ProjectionHydrationStatus{Phase: ProjectionHydrateReady}
	session.committedSourceCursor = tx.startCut
	session.committedSource = cloneProjectionSourceDescriptor(tx.source)
	session.hydrate = nil
	k.finishHydrateLocked(session)
	session.failureAttempts = 0
	return ProjectionHydrateCommit{
		Projection:   committed,
		PendingLive:  len(tx.pendingLive),
		PendingPatch: patch,
	}, nil
}

func (k *ProjectionKernel) HydrateSource(backendID, sessionID string) (ProjectionSourceDescriptor, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.hydrate == nil {
		return ProjectionSourceDescriptor{}, false
	}
	return session.hydrate.source, true
}

func (k *ProjectionKernel) CommittedSourceCursor(backendID, sessionID string) (int64, bool) {
	if k == nil {
		return 0, false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.status.Phase != ProjectionHydrateReady || session.committedSource.Identity == "" {
		return 0, false
	}
	if segments := session.committedSource.Segments; len(segments) > 0 {
		// Claude's live file relay watches the active tail segment, while the descriptor cursor
		// is the composite sum across the compact-linked chain.
		return segments[len(segments)-1].Cursor, true
	}
	return session.committedSourceCursor, true
}

// FlushProjectionPatch drains the authoritative reducer's pending deltas for one session into a
// single projection patch. Callers use it after a Kernel-private ingest (e.g.
// ApplyClaudeSourceRecordBatch) advanced the reducer under the Kernel lock, then deliver the patch
// via EventPublisher.PublishProjectionPatch. Keeps the projection stream the sole active-timeline
// writer (guardrail #3): the patch is a read-only fan-out of state the transaction already reduced.
func (k *ProjectionKernel) FlushProjectionPatch(backendID, sessionID string) (ProjectionPatch, bool) {
	if k == nil {
		return ProjectionPatch{}, false
	}
	return k.reducer.FlushPatch(backendID, sessionID)
}

func (k *ProjectionKernel) HydrateSnapshot(backendID, sessionID string) (SessionProjection, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.hydrate == nil {
		return SessionProjection{}, false
	}
	return session.hydrate.reducer.Snapshot(backendID, sessionID)
}

func (k *ProjectionKernel) RestoreCheckpoint(
	backendID, sessionID string,
	source ProjectionSourceDescriptor,
) (ProjectionCheckpoint, error) {
	k.mu.Lock()
	store := k.store
	reducer := k.reducer
	k.mu.Unlock()
	if store == nil {
		return ProjectionCheckpoint{}, ErrProjectionCheckpointDisabled
	}
	checkpoint, err := store.LoadValidated(backendID, sessionID, source)
	if err != nil {
		return ProjectionCheckpoint{}, err
	}
	var restoredClaudeSourceState *ClaudeSourceState
	if checkpoint.ClaudeSourceState != nil {
		cloned, cloneErr := cloneClaudeSourceState(*checkpoint.ClaudeSourceState)
		if cloneErr != nil {
			return ProjectionCheckpoint{}, cloneErr
		}
		restoredClaudeSourceState = &cloned
	}
	reducer.Restore(backendID, sessionID, checkpoint.Projection)
	k.mu.Lock()
	session := k.sessionLocked(backendID, sessionID)
	session.status = ProjectionHydrationStatus{Phase: ProjectionHydrateReady}
	session.failureAttempts = 0
	session.lastPersistedRev = checkpoint.ProjectionRev
	session.committedSourceCursor = checkpoint.Source.Cursor
	if len(source.Segments) > 0 {
		session.committedSourceCursor = source.Cursor
	}
	session.committedSource = cloneProjectionSourceDescriptor(source)
	session.claudeSourceState = restoredClaudeSourceState
	session.lastWriteErr = nil
	k.mu.Unlock()
	checkpoint.Projection = cloneSessionProjection(checkpoint.Projection)
	return checkpoint, nil
}

func cloneProjectionSourceDescriptor(source ProjectionSourceDescriptor) ProjectionSourceDescriptor {
	cloned := source
	cloned.Segments = append([]ProjectionSourceSegment(nil), source.Segments...)
	return cloned
}

func projectionSourceDescriptorsEqual(lhs, rhs ProjectionSourceDescriptor) bool {
	if lhs.Identity != rhs.Identity || lhs.Path != rhs.Path || lhs.Cursor != rhs.Cursor ||
		len(lhs.Segments) != len(rhs.Segments) {
		return false
	}
	for index := range lhs.Segments {
		if lhs.Segments[index] != rhs.Segments[index] {
			return false
		}
	}
	return true
}

func (k *ProjectionKernel) Snapshot(backendID, sessionID string) (SessionProjection, bool) {
	k.mu.Lock()
	status := k.sessionLocked(backendID, sessionID).status
	reducer := k.reducer
	k.mu.Unlock()
	if status.Phase != ProjectionHydrateReady {
		return SessionProjection{}, false
	}
	if projection, ok := reducer.Snapshot(backendID, sessionID); ok {
		return projection, true
	}
	return SessionProjection{
		SessionID: sessionID,
		Execution: ExecutionView{Phase: "idle"},
		Turns:     []TurnProjection{},
	}, true
}

func (k *ProjectionKernel) StageCheckpoint(checkpoint ProjectionCheckpoint, settled bool) error {
	if checkpoint.SchemaVersion == 0 {
		checkpoint.SchemaVersion = projectionCheckpointSchemaVersion
	}
	if checkpoint.HydrateState == "" {
		checkpoint.HydrateState = ProjectionHydrateReady
	}
	if checkpoint.UpdatedAt.IsZero() {
		checkpoint.UpdatedAt = k.now()
	}
	checkpoint.Projection = cloneSessionProjection(checkpoint.Projection)
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.store == nil {
		return ErrProjectionCheckpointDisabled
	}
	session := k.sessionLocked(checkpoint.BackendID, checkpoint.SessionID)
	if checkpoint.ProjectionRev <= session.lastPersistedRev && session.pending == nil {
		return nil
	}
	if session.pending == nil || checkpoint.ProjectionRev >= session.pending.ProjectionRev {
		cp := checkpoint
		session.pending = &cp
	}
	if settled {
		session.forceWrite = true
	}
	k.scheduleCheckpointLocked(checkpoint.BackendID, checkpoint.SessionID, session)
	return nil
}

func (k *ProjectionKernel) scheduleCheckpointLocked(
	backendID, sessionID string,
	session *projectionKernelSession,
) {
	if session == nil || session.pending == nil || session.writeInFlight {
		return
	}
	revGap := session.pending.ProjectionRev - session.lastPersistedRev
	immediate := session.forceWrite ||
		k.checkpointPolicy.MaxRevisions <= 0 ||
		revGap >= k.checkpointPolicy.MaxRevisions
	if immediate {
		if session.timer != nil {
			session.timer.Stop()
			session.timer = nil
		}
		k.startCheckpointWriteLocked(backendID, sessionID, session)
		return
	}
	if session.timer != nil {
		return
	}
	delay := k.checkpointPolicy.MaxInterval
	if delay <= 0 {
		delay = 2 * time.Second
	}
	session.timer = time.AfterFunc(delay, func() {
		k.mu.Lock()
		current := k.sessionLocked(backendID, sessionID)
		current.timer = nil
		current.forceWrite = true
		k.scheduleCheckpointLocked(backendID, sessionID, current)
		k.mu.Unlock()
	})
}

func (k *ProjectionKernel) startCheckpointWriteLocked(
	backendID, sessionID string,
	session *projectionKernelSession,
) {
	if session == nil || session.pending == nil || session.writeInFlight {
		return
	}
	checkpoint := *session.pending
	checkpoint.Projection = cloneSessionProjection(checkpoint.Projection)
	store := k.store
	session.pending = nil
	session.forceWrite = false
	session.writeInFlight = true
	go func() {
		var err error
		if store == nil {
			err = ErrProjectionCheckpointDisabled
		} else {
			err = store.Save(checkpoint)
		}
		k.mu.Lock()
		current := k.sessionLocked(backendID, sessionID)
		current.writeInFlight = false
		current.lastWriteErr = err
		if err == nil && checkpoint.ProjectionRev > current.lastPersistedRev {
			current.lastPersistedRev = checkpoint.ProjectionRev
		}
		if current.pending != nil && current.pending.ProjectionRev <= current.lastPersistedRev {
			current.pending = nil
		}
		if current.pending != nil {
			k.scheduleCheckpointLocked(backendID, sessionID, current)
		} else {
			for _, waiter := range current.waiters {
				close(waiter)
			}
			current.waiters = nil
		}
		k.mu.Unlock()
	}()
}

func (k *ProjectionKernel) FlushCheckpoint(
	ctx context.Context,
	backendID, sessionID string,
) error {
	k.mu.Lock()
	session := k.sessionLocked(backendID, sessionID)
	if session.pending == nil && !session.writeInFlight {
		err := session.lastWriteErr
		k.mu.Unlock()
		return err
	}
	session.forceWrite = true
	waiter := make(chan struct{})
	session.waiters = append(session.waiters, waiter)
	k.scheduleCheckpointLocked(backendID, sessionID, session)
	k.mu.Unlock()
	select {
	case <-waiter:
		k.mu.Lock()
		err := k.sessionLocked(backendID, sessionID).lastWriteErr
		k.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func NewReadyProjectionCheckpoint(
	backendID, sessionID string,
	source ProjectionSourceCheckpoint,
	projection SessionProjection,
	now time.Time,
) ProjectionCheckpoint {
	projection = cloneSessionProjection(projection)
	return ProjectionCheckpoint{
		SchemaVersion: projectionCheckpointSchemaVersion,
		BackendID:     backendID,
		SessionID:     sessionID,
		Source:        source,
		Projection:    projection,
		ProjectionRev: projection.SyncRev,
		HydrateState:  ProjectionHydrateReady,
		UpdatedAt:     now,
	}
}

func NewReadyCompositeProjectionCheckpoint(
	backendID, sessionID string,
	sources []ProjectionSourceCheckpoint,
	projection SessionProjection,
	now time.Time,
) ProjectionCheckpoint {
	projection = cloneSessionProjection(projection)
	return ProjectionCheckpoint{
		SchemaVersion: projectionCheckpointSchemaVersion,
		BackendID:     backendID,
		SessionID:     sessionID,
		Sources:       append([]ProjectionSourceCheckpoint(nil), sources...),
		Projection:    projection,
		ProjectionRev: projection.SyncRev,
		HydrateState:  ProjectionHydrateReady,
		UpdatedAt:     now,
	}
}
