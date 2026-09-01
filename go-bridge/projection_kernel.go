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
// v6: ProjectionPart tool retains optional title + fileChanges (guardrail 12 / ChatGPT-style
// activity rows). Bumping invalidates v5 checkpoints so cold hydrate re-reduces with the new
// fields instead of restoring pre-title baselines (which left iOS activity rows as bare
// 「已编辑文件」 even after the reducer fix).
// v7: Claude AskUserQuestion transcript tool_use/tool_result rows are projected as structured
// user-input events instead of ordinary tool activity. Bumping invalidates v6 checkpoints so
// sessions already hydrated before this mapper change are rebuilt from the canonical transcript.
// v8: Claude pathless rich-history hydrate now preserves the same structured user-input
// semantics. v7 checkpoints may still contain AskUserQuestion as ordinary tool parts.
// v9: an interrupt marker following a resolved AskUserQuestion no longer hides the owning
// prompt and reattributes the structured part to an older turn.
// v10: Claude AskUserQuestion now advertises its evidence-backed custom-answer capability,
// and a pending transcript-owned question keeps execution in requires_action. v9 checkpoints
// can contain allowsCustomAnswer=false and execution.phase=idle, so they must be rebuilt from
// the canonical transcript instead of being restored as a permanently stale observe-only card.
// v11: turn_detail_lazy_v1 (frozen 2026-08-30) — turns gain detailLoadState/detailReasonCode/
// generation (turnStateOps patch op), and the checkpoint gains the backend-private
// CodexProducerState (lazy-history producer checkpoint, bridge-v1.md R11d). v10 checkpoints
// lack the turn fence/state fields and must be rebuilt so cold hydrate re-reduces with them.
// v12: turn_detail_chunks_v1 §11.8 (F3) — turns gain the detail manifest SUMMARY
// (detailManifestRev/detailItemCount/detailTotalBytes) and the v2-only partial
// detailLoadState, produced exclusively by the V2 manifest commit path
// (CommitTurnStateOpsV2) and the generation-bump baseline reset. v11 checkpoints
// predate that bookkeeping — restoring them would crown zero-manifest turns as
// authoritative under v2 semantics — so they rebuild from the canonical source.
// v13: legacy full-read Codex turns carry detailInline + loaded provenance.
// Rebuild v12 checkpoints so old sessions never probe paginated detail and
// remove every disclosure row after unsupported_capability.
const projectionCheckpointSchemaVersion = 13

var (
	ErrProjectionCheckpointInvalid  = errors.New("projection checkpoint invalid")
	ErrProjectionCheckpointDisabled = errors.New("projection checkpoint persistence disabled")
)

// projectionReducerEvent constructs reducer-only input. Keeping this constructor in the
// projection kernel makes the no-production-bypass guard mechanically distinguish isolated
// reducer transactions from business-event egress, which must go through EventPublisher.
func projectionReducerEvent(
	backendID, sessionID, event string,
	data interface{},
	perSessionSeq int,
	bridgeEpoch string,
) EventMessage {
	return EventMessage{
		BackendID: backendID, SessionID: sessionID,
		Event: event, Data: data,
		PerSessionSeq: perSessionSeq, BridgeEpoch: bridgeEpoch,
	}
}

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
	SchemaVersion      int                          `json:"schemaVersion"`
	BackendID          string                       `json:"backendId"`
	SessionID          string                       `json:"sessionId"`
	Source             ProjectionSourceCheckpoint   `json:"source"`
	Sources            []ProjectionSourceCheckpoint `json:"sources,omitempty"`
	Projection         SessionProjection            `json:"projection"`
	ProjectionRev      int                          `json:"projectionRev"`
	HydrateState       ProjectionHydratePhase       `json:"hydrateState"`
	UpdatedAt          time.Time                    `json:"updatedAt"`
	ClaudeSourceState  *ClaudeSourceState           `json:"claudeSourceState,omitempty"`
	CodexProducerState *CodexProducerState          `json:"codexProducerState,omitempty"`
}

// CodexProducerState is the backend-private lazy-history producer checkpoint for
// codex-remote (bridge-v1.md R11d; unified-bridge-protocol.md §11.7). It records the
// "more upstream history exists" fact and the INTERNAL upstream cursor so an `older`
// window request can hydrate one page at a time across restarts. It never crosses the
// bridge and is never embedded in a wire cursor (R2). Restart validation is by target
// RPC (T2.0), not file digest.
type CodexProducerState struct {
	HasOlderUpstream   bool   `json:"hasOlderUpstream"`
	UpstreamNextCursor string `json:"upstreamNextCursor,omitempty"`
	BoundaryTurnID     string `json:"boundaryTurnId,omitempty"`
	// HistoryMode records the upstream thread's declared mode at cold open
	// ("paginated" | "legacy"; empty = pre-T2.3 files). It gates the
	// turn_detail_lazy_v1 capability per session (§11.7: legacy never exposes
	// session_turn_items) — bridge-private, not a wire shape.
	HistoryMode string    `json:"historyMode,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ValidateCodexProducerState enforces the checkpoint-side invariants: claiming
// older upstream requires the kernel boundary turn the fact was observed at.
// UpstreamNextCursor may be empty ONLY as the explicit head re-walk state written
// by typed stale-cursor recovery (R11d): the next older request fetches upstream
// page 1 and walks back down to the boundary.
func ValidateCodexProducerState(state CodexProducerState) error {
	if !state.HasOlderUpstream {
		return nil
	}
	if state.BoundaryTurnID == "" {
		return fmt.Errorf("%w: codex producer state claims older upstream without boundary turn", ErrProjectionCheckpointInvalid)
	}
	return nil
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
	if err := validateTurnDetailCheckpointFields(checkpoint.Projection.Turns); err != nil {
		return ProjectionCheckpoint{}, err
	}
	if checkpoint.CodexProducerState != nil {
		if err := ValidateCodexProducerState(*checkpoint.CodexProducerState); err != nil {
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

// validateTurnDetailCheckpointFields enforces the §11.8 fail-closed rules on
// restored turns (v12 checkpoints): the detail state stays inside the frozen
// closed set (v2 adds partial), reasonCode exists iff failed, and the manifest
// summary is internally consistent (items counted ⇒ a manifest revision
// exists — mirrors ValidateTurnStateOpsV2). Reason-code SET membership is
// deliberately not checked here: a restored turn may predate a set revision.
func validateTurnDetailCheckpointFields(turns []TurnProjection) error {
	for i := range turns {
		turn := &turns[i]
		switch turn.DetailLoadState {
		case DetailStateNotRequested, DetailStateLoading, DetailStatePartial, DetailStateLoaded, DetailStateFailed:
		default:
			return fmt.Errorf("%w: turn %s detailLoadState %q",
				ErrProjectionCheckpointInvalid, turn.TurnID, turn.DetailLoadState)
		}
		if turn.DetailLoadState == DetailStateFailed {
			if turn.DetailReasonCode == "" {
				return fmt.Errorf("%w: turn %s failed without reasonCode",
					ErrProjectionCheckpointInvalid, turn.TurnID)
			}
		} else if turn.DetailReasonCode != "" {
			return fmt.Errorf("%w: turn %s state %q carries reasonCode",
				ErrProjectionCheckpointInvalid, turn.TurnID, turn.DetailLoadState)
		}
		if turn.DetailInline && turn.DetailLoadState != DetailStateLoaded {
			return fmt.Errorf("%w: turn %s inline detail state %q",
				ErrProjectionCheckpointInvalid, turn.TurnID, turn.DetailLoadState)
		}
		if turn.DetailManifestRev < 0 || turn.DetailItemCount < 0 || turn.DetailTotalBytes < 0 {
			return fmt.Errorf("%w: turn %s negative manifest summary",
				ErrProjectionCheckpointInvalid, turn.TurnID)
		}
		if (turn.DetailItemCount > 0 || turn.DetailTotalBytes > 0) && turn.DetailManifestRev <= 0 {
			return fmt.Errorf("%w: turn %s item summary without manifestRev",
				ErrProjectionCheckpointInvalid, turn.TurnID)
		}
	}
	return nil
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

// LoadCodexProducerState reads the backend-private lazy-history producer
// checkpoint side file (R11d). Missing file is (nil, nil) — first use. The state
// is validated; an invalid file is a typed error, never a silent zero state.
func (s *ProjectionCheckpointStore) LoadCodexProducerState(
	backendID, sessionID string,
) (*CodexProducerState, error) {
	path, err := s.producerStatePath(backendID, sessionID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var envelope struct {
		SchemaVersion int                 `json:"schemaVersion"`
		State         *CodexProducerState `json:"state"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: producer state decode: %v", ErrProjectionCheckpointInvalid, err)
	}
	if envelope.SchemaVersion != 1 {
		return nil, fmt.Errorf("%w: producer state schema %d", ErrProjectionCheckpointInvalid, envelope.SchemaVersion)
	}
	if envelope.State == nil {
		return nil, fmt.Errorf("%w: producer state envelope without state", ErrProjectionCheckpointInvalid)
	}
	if err := ValidateCodexProducerState(*envelope.State); err != nil {
		return nil, err
	}
	return envelope.State, nil
}

// SaveCodexProducerState atomically persists the producer checkpoint side file.
func (s *ProjectionCheckpointStore) SaveCodexProducerState(
	backendID, sessionID string,
	state CodexProducerState,
) error {
	if err := ValidateCodexProducerState(state); err != nil {
		return err
	}
	path, err := s.producerStatePath(backendID, sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		SchemaVersion int                `json:"schemaVersion"`
		State         CodexProducerState `json:"state"`
	}{SchemaVersion: 1, State: state})
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func (s *ProjectionCheckpointStore) producerStatePath(backendID, sessionID string) (string, error) {
	path, err := s.checkpointPath(backendID, sessionID)
	if err != nil {
		return "", err
	}
	return path + ".producer.json", nil
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
	// sourceIsLive is sampled once at hydrate admission (design §3.1 of the SSV2 running-session
	// cold-open fix): a live in-progress process is an explicit lifecycle signal that lets the
	// commit gate release a cold-armed running turn as an honest running partial. The value is
	// fixed for the transaction; process death during hydrate is closed by the live side
	// (relay-before-hydrate + synthesized turn_aborted, §3.2/§3.3), never by re-polling liveness.
	sourceIsLive bool
	// sourceIngestComplete is set true once cold-source ingest finishes (design §3.3 rule #2 /
	// D6 / K1 of the cold-start plan, guardrail #6). WaitHydrateCommitReady will not commit until
	// this is true, so readiness is decided from authoritative source-EOF + turn terminal state
	// rather than content shape or turn-count guessing.
	sourceIngestComplete bool
	// coldArmedTurnIDs records every turn ID the cold-source ingest referenced (turnId or
	// itemId). The commit gate checks terminal state only for these turns — turns carried
	// from a committed/live baseline are authoritative live truth, not cold-source half-seen
	// guesses, so a live in-progress session cold-pulled commits its current state instead of
	// blocking until the in-flight turn completes.
	coldArmedTurnIDs map[string]struct{}
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
	// AppliedPendingEventIDs lists the EventIDs of pendingLive rows that actually advanced
	// the committed reducer inside this atomic commit (web push §8.1: callers release the
	// deferred candidates for exactly these ids outside the Kernel lock). Rows that were
	// no-ops carry no push side effect.
	AppliedPendingEventIDs []string
}

// ProjectionIngestResult is the tri-state outcome of IngestLive (web push §8.1):
// the old bool conflated "no-op" with "hydrate deferred", which the push candidate
// path must distinguish. R2-O1 baseline: only Applied may trigger the live-path
// projection patch flush in EventPublisher; Deferred defers that to the hydrate
// commit's own flush, NoChange never flushes.
type ProjectionIngestResult int

const (
	// ProjectionIngestNoChange: the event was applied to the committed reducer but did
	// not advance the projection revision (duplicate/no-op).
	ProjectionIngestNoChange ProjectionIngestResult = iota
	// ProjectionIngestApplied: the authoritative reducer advanced.
	ProjectionIngestApplied
	// ProjectionIngestDeferred: a hydrate transaction is active; the event was queued as
	// pendingLive and will only reach the committed reducer at CommitHydrateTransaction.
	ProjectionIngestDeferred
)

type ProjectionKernel struct {
	mu               sync.Mutex
	reducer          *ProjectionReducer
	store            projectionCheckpointPersistence
	sessions         map[string]*projectionKernelSession
	retryPolicy      ProjectionRetryPolicy
	checkpointPolicy ProjectionCheckpointPolicy
	now              func() time.Time
	randomUnit       func() float64

	// stageTurnCheckpoint is the §6.1 git-checkpoint hook. When non-nil, IngestLive
	// invokes it AFTER the reducer applies a turn_completed event, passing the
	// 1-based TurnCount() of the just-completed turn. The callback ONLY stages
	// intent (under the coalescer's own mutex); it must NOT perform git I/O on
	// this stack (IngestLive runs under k.mu holding the event-delivery lock).
	// Git I/O happens later in the checkpoint coalescer goroutine (checkpoint.go).
	// Sourced from Handlers via SetTurnCheckpointStager; nil in unit tests that do
	// not exercise the checkpoint path.
	stageTurnCheckpoint func(backendID, sessionID string, turnN int)
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

// SetTurnCheckpointStager wires the §6.1 git-checkpoint hook. The stager is
// invoked from IngestLive after the reducer applies a turn_completed event; it
// must only register intent (no git I/O). Handlers passes h.checkpointCoalescer.stage.
func (k *ProjectionKernel) SetTurnCheckpointStager(fn func(backendID, sessionID string, turnN int)) {
	if k == nil {
		return
	}
	k.mu.Lock()
	k.stageTurnCheckpoint = fn
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

// HasReducerState reports whether the authoritative reducer holds any state for the
// session, i.e. live ingestion committed at least one event this bridge epoch. The
// live-only projection admission uses it to distinguish a dead session (honest
// projection.not_found) from a live session whose baseline has not been admitted yet.
func (k *ProjectionKernel) HasReducerState(backendID, sessionID string) bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	_, ok := k.reducer.Snapshot(backendID, sessionID)
	return ok
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

// MarkFailed fails the active hydrate transaction and returns its status plus the
// EventIDs of the uncommitted pendingLive rows (web push §8.1: callers discard the
// deferred candidates for exactly these ids outside the Kernel lock and emit a
// sanitized web_push.deferred_hydrate_failed diagnostic — never a fake send).
func (k *ProjectionKernel) MarkFailed(
	backendID, sessionID, code, message string,
	retryable bool,
) (ProjectionHydrationStatus, []string) {
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
	var deferredEventIDs []string
	if session.hydrate != nil {
		for _, msg := range session.hydrate.pendingLive {
			if msg.EventID != "" {
				deferredEventIDs = append(deferredEventIDs, msg.EventID)
			}
		}
	}
	session.status = ProjectionHydrationStatus{Phase: ProjectionHydrateFailed, Failure: failure}
	session.hydrate = nil
	k.finishHydrateLocked(session)
	return session.status, deferredEventIDs
}

func (k *ProjectionKernel) finishHydrateLocked(session *projectionKernelSession) {
	if session != nil && session.hydrateDone != nil {
		close(session.hydrateDone)
		session.hydrateDone = nil
	}
}

// pathlessRichHistoryBackend reports whether a backend can perform a pathless hydrate
// (source.Path == "") that replays content through a rich-history builder: Claude and
// OpenCode. The legacy "codex" driver is file-based and excluded; codex-web is pathless
// by design — its baseline is the official app-server API, and checkpoints must never
// carry a session file path (design §9.1 pathless-family row).
func pathlessRichHistoryBackend(backendID string) bool {
	switch backendID {
	case "opencode", "grokbuild", "claude", "claudecode", "opencode-web", "codex-web":
		return true
	default:
		return false
	}
}

// pathlessFullRebuildSource is true when hydrate must start from an EMPTY reducer and
// re-reduce the entire source as sole baseline (OpenCode HTTP; Claude without file/segment
// cursors). Carrying a prior projection would replay builder output on top of already-
// present turns and can duplicate content across live row-UUID vs builder turn ids
// (see docs/2026-07-31-claude-projection-pathless-hydrate-duplication-fix.md).
//
// Claude composite segments still carry per-file cursors and validated checkpoints.
// Those must NOT be treated as full rebuilds: when a checkpoint hit sets startCursor to
// the segment cut (EOF), produceProjectionHydrateSource returns early on
// startOffset==endOffset. Without restoring the checkpoint baseline the committed
// projection is empty (headRev=0) and iOS shows "还没有消息" for every Claude session.
func pathlessFullRebuildSource(backendID string, source ProjectionSourceDescriptor) bool {
	return source.Path == "" && len(source.Segments) == 0 && pathlessRichHistoryBackend(backendID)
}

// BeginHydrateTransaction creates the isolated reducer used for [checkpointCursor,startCut).
// Live events that arrive after this cut are queued by IngestLive and cannot mutate the
// authoritative reducer until CommitHydrateTransaction publishes the baseline atomically.
func (k *ProjectionKernel) BeginHydrateTransaction(
	backendID, sessionID string,
	source ProjectionSourceDescriptor,
	explicitRetry, sourceChanged, sourceIsLive bool,
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
		source:           source,
		startCut:         source.Cursor,
		reducer:          NewProjectionReducer(),
		liveArrived:      make(chan struct{}, 1),
		coldArmedTurnIDs: make(map[string]struct{}),
		sourceIsLive:     sourceIsLive,
	}
	if source.Path == "" {
		if pathlessFullRebuildSource(backendID, source) {
			// OpenCode / Claude pathless (no Path, no Segments) rebuild starts EMPTY.
			// tx.reducer is already a fresh NewProjectionReducer(); do NOT Restore.
		} else if !sourceChanged && len(source.Segments) == 0 {
			// Codex pathless degenerate no-file case: keep carried live baseline.
			// Claude composite Segments are handled via checkpoint Restore below (not here).
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
			if !pathlessFullRebuildSource(backendID, source) {
				// Carry the checkpoint projection as baseline.
				// CRITICAL: Claude composite (Path=="" + Segments) is NOT a full rebuild —
				// startCursor becomes source.Cursor (EOF cut). Skipping Restore here yields
				// an empty projection (headRev=0) and empty iOS Claude sessions.
				// Only true pathless full rebuilds (no Path, no Segments) skip Restore.
				restored := checkpoint.Projection
				if backendID == "codex-remote" {
					// §11.8 orphan recovery (V2 semantics supersede the §11.7
					// hook here): a loading turn whose batch leader died with the
					// previous bridge epoch is atomically failed(interrupted)
					// CARRYING the retained manifest summary; the rev bump
					// journals as a gap (dead-epoch conns are gone).
					RecoverOrphanDetailLoadingV2(&restored)
				}
				tx.reducer.Restore(backendID, sessionID, restored)
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
	// Record every turn the cold source references (turnId, else itemId), so the commit gate
	// checks terminal state only for cold-source-armed turns (design §3.3 rule #2 / D6 / K1). Turns carried
	// from a committed/live baseline are never referenced here and thus never gate — live
	// in-progress sessions cold-pulled commit their current state instead of blocking.
	if tid, ok := data["turnId"].(string); ok && tid != "" {
		tx.coldArmedTurnIDs[tid] = struct{}{}
	} else if iid, ok := data["itemId"].(string); ok && iid != "" {
		tx.coldArmedTurnIDs[iid] = struct{}{}
	}
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
// a deep immutable pending event (ProjectionIngestDeferred); otherwise it applies directly to
// the committed reducer and reports Applied vs NoChange by whether the revision advanced.
func (k *ProjectionKernel) IngestLive(msg EventMessage) ProjectionIngestResult {
	if k == nil {
		return ProjectionIngestNoChange
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
		return ProjectionIngestDeferred
	}
	before := k.reducer.LastAppliedRev(msg.BackendID, msg.SessionID)
	k.reducer.Apply(msg)
	projectionAdvanced := k.reducer.LastAppliedRev(msg.BackendID, msg.SessionID) != before

	// §6.1 checkpoint hook: after the reducer applies turn_completed, read the
	// 1-based TurnCount() (= the just-completed turn) and stage the git-checkpoint
	// intent. This is KERNEL-level (not reducer-level — the reducer stays pure and
	// cannot reference git or the Kernel). We DO NOT self-maintain a counter;
	// TurnCount() is projection truth. The stager only registers intent under the
	// coalescer's own mutex; git I/O runs in the coalescer goroutine, NEVER under
	// k.mu (that would block all session event delivery). Gated on
	// projectionAdvanced so a no-op/duplicate turn_completed does not capture.
	if projectionAdvanced && msg.Event == "turn_completed" && k.stageTurnCheckpoint != nil {
		if turnN := k.reducer.TurnCount(msg.BackendID, msg.SessionID); turnN > 0 {
			k.stageTurnCheckpoint(msg.BackendID, msg.SessionID, turnN)
		}
	}
	if projectionAdvanced {
		return ProjectionIngestApplied
	}
	return ProjectionIngestNoChange
}

// MarkHydrateSourceIngestComplete signals that cold-source ingest feeding this hydrate
// transaction has finished — no further ApplyHydrateEvent calls will be made from the cold
// source (mainstream transcript + Claude sidechain). WaitHydrateCommitReady will not commit
// until this is set, so readiness is decided from authoritative source-EOF + turn terminal
// state rather than content shape or turn-count guessing (design §3.3 rule #2 / D6 / K1 of
// the cold-start plan, guardrail #6).
// Also nudges liveArrived so a waiter re-evaluates immediately. Idempotent; no-op if the
// transaction is no longer active (already committed/failed/superseded).
func (k *ProjectionKernel) MarkHydrateSourceIngestComplete(backendID, sessionID string) {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.status.Phase != ProjectionHydrateHydrating || session.hydrate == nil {
		return
	}
	session.hydrate.sourceIngestComplete = true
	select {
	case session.hydrate.liveArrived <- struct{}{}:
	default:
	}
}

// WaitHydrateCommitReady blocks until the hydrate transaction is committable, then returns
// nil. Commit readiness is authoritative (design §3.3 rule #2 / D6 / K1 of the cold-start
// plan, guardrail #6): the cold source must be fully ingested
// (MarkHydrateSourceIngestComplete) AND every turn armed by the source must have reached a
// terminal state (completed/aborted/error) — unless the source is live (§3.1 of the SSV2
// running-session cold-open fix), in which case an in-flight running turn may commit as an
// honest running partial. This replaces the earlier turnCount==0 || HasContentTurn gate,
// which guessed from count/content shape and left empty/aborted/crashed sessions stuck
// hydrating forever. A bare turn_started with no terminal event stays not-ready (correct); a
// non-live in-flight turn cold-opened mid-flight waits for its terminal event instead of
// committing on partial content.
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
		// §3.3 rule #2 / D6 / K1 (cold-start plan) + §3.1 (SSV2 running-session fix): readiness
		// is authoritative, not guessed. Commit only after the cold source is fully ingested AND
		// either every turn the cold source armed has reached a terminal state
		// (completed/aborted/error), or the source is live (an explicit lifecycle signal that
		// admits an honest running partial for the in-flight turn). Scoped to cold-armed turns:
		// turns carried from a committed/live baseline are live truth and do not gate, so a live
		// in-progress session cold-pulled commits its current state instead of blocking. The old
		// `turnCount == 0 || HasContentTurn(...)` gate guessed from count/content shape; it left
		// empty/aborted/crashed sessions stuck hydrating forever and could commit a half-seen
		// in-flight turn on partial content.
		ready := tx.sourceIngestComplete &&
			(tx.sourceIsLive || preview.NonTerminalTurnCountInSet(backendID, sessionID, tx.coldArmedTurnIDs) == 0)
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
	// Live rows arriving during Claude hydrate are now expected and correlated: the live file
	// relay starts before the hydrate wait (§3.2) and routes in-flight content through the
	// EventPublisher into pendingLive, and the Claude source ledger advances past every
	// physical row during hydrate (§3.2 hydrate routing), so the post-cut live events are
	// authoritative same-owner rows, not uncorrelated overlap. CommitHydrateTransaction applies
	// them after the baseline in their stamped order.
	liveSnap, liveOK := k.reducer.Snapshot(backendID, sessionID)
	baseline, ok := tx.reducer.Snapshot(backendID, sessionID)
	if !ok {
		baseline = SessionProjection{
			SessionID: sessionID,
			Execution: ExecutionView{Phase: "idle"},
			Turns:     []TurnProjection{},
		}
	}
	// Pathless full rebuild starts from an empty tx reducer (do NOT Restore live
	// turns — live row-UUID vs builder turn ids duplicate). Events that landed
	// on the main reducer before BeginHydrate are therefore not in pendingLive.
	// Restore of a cold idle baseline must not clobber an already-live in-flight
	// execution (real device 2026-08-20: user_message patches then sinceRev=0
	// hydrate committed {"phase":"idle"}). Turns still come from the cold source;
	// execution takes the in-flight max (running/requires_action > idle).
	baseline = mergeHydrateBaselineWithLiveExecution(baseline, liveSnap, liveOK)
	k.reducer.Restore(backendID, sessionID, baseline)
	appliedPendingIDs := make([]string, 0, len(tx.pendingLive))
	for _, msg := range tx.pendingLive {
		before := k.reducer.LastAppliedRev(msg.BackendID, msg.SessionID)
		k.reducer.Apply(msg)
		if k.reducer.LastAppliedRev(msg.BackendID, msg.SessionID) != before && msg.EventID != "" {
			appliedPendingIDs = append(appliedPendingIDs, msg.EventID)
		}
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
		Projection:             committed,
		PendingLive:            len(tx.pendingLive),
		PendingPatch:           patch,
		AppliedPendingEventIDs: appliedPendingIDs,
	}, nil
}

func executionInFlight(e ExecutionView) bool {
	switch e.Phase {
	case "running", "requires_action":
		return true
	default:
		return false
	}
}

// mergeHydrateBaselineWithLiveExecution keeps an already-live in-flight execution
// when the cold baseline would otherwise Restore idle. Cold turns/content stay
// the hydrate baseline; this is not a second writer — commit is still one Restore.
func mergeHydrateBaselineWithLiveExecution(cold, live SessionProjection, liveOK bool) SessionProjection {
	if !liveOK || !executionInFlight(live.Execution) || executionInFlight(cold.Execution) {
		return cold
	}
	cold.Execution = live.Execution
	active := live.Execution.ActiveTurnID
	if active == "" {
		return cold
	}
	for i := range cold.Turns {
		if cold.Turns[i].TurnID != active {
			continue
		}
		switch cold.Turns[i].Status {
		case "completed", "aborted", "error":
			cold.Turns[i].Status = "running"
			cold.Turns[i].CompletedAt = 0
		}
	}
	return cold
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

// LoadCodexProducerState reads the backend-private producer checkpoint through
// the installed store; a nil/foreign store means no persistence (session-lifetime
// producer state only).
func (k *ProjectionKernel) LoadCodexProducerState(backendID, sessionID string) (*CodexProducerState, error) {
	if k == nil {
		return nil, nil
	}
	k.mu.Lock()
	store := k.store
	k.mu.Unlock()
	concrete, ok := store.(*ProjectionCheckpointStore)
	if !ok {
		return nil, nil
	}
	return concrete.LoadCodexProducerState(backendID, sessionID)
}

// SaveCodexProducerState persists through the installed store (no-op without one).
func (k *ProjectionKernel) SaveCodexProducerState(backendID, sessionID string, state CodexProducerState) error {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	store := k.store
	k.mu.Unlock()
	concrete, ok := store.(*ProjectionCheckpointStore)
	if !ok {
		return nil
	}
	return concrete.SaveCodexProducerState(backendID, sessionID, state)
}

// PrependHistoricalTurns commits a structural historical prepend (R11a) on a
// READY session; see ProjectionReducer.PrependHistoricalTurns for the journal-gap
// and delivery contract.
func (k *ProjectionKernel) PrependHistoricalTurns(
	backendID, sessionID string,
	turns []TurnProjection,
) (SessionProjection, error) {
	if k == nil {
		return SessionProjection{}, errors.New("projection kernel nil")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.status.Phase != ProjectionHydrateReady {
		return SessionProjection{}, fmt.Errorf(
			"projection: prepend requires ready session, %s/%s is %s",
			backendID, sessionID, session.status.Phase,
		)
	}
	return k.reducer.PrependHistoricalTurns(backendID, sessionID, turns)
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

// ActiveTurnID 返回该 session 当前 active/最近 running 的 turn id（web push
// notification key 的 identity 来源）。与 Snapshot 不同，它不要求 hydrate-ready：
// relay 循环中的 session 可能从未走过显式 hydrate 事务，但 authoritative reducer
// 已持有其 running turn。只读，不改状态。
func (k *ProjectionKernel) ActiveTurnID(backendID, sessionID string) string {
	if k == nil {
		return ""
	}
	projection, ok := k.reducer.Snapshot(backendID, sessionID)
	if !ok {
		return ""
	}
	if projection.Execution.ActiveTurnID != "" {
		return projection.Execution.ActiveTurnID
	}
	for i := len(projection.Turns) - 1; i >= 0; i-- {
		if projection.Turns[i].Status == "running" {
			return projection.Turns[i].TurnID
		}
	}
	return ""
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

// MergeHistoricalTurnDetail (T2.2) is the Kernel gate for the dedicated
// historical detail merge: requires a READY session (hydrated truth), then
// delegates to the reducer's atomic replace_parts + loaded turnStateOp commit
// (P0-1 chain: staged-live flush patch first, then the detail commit patch).
func (k *ProjectionKernel) MergeHistoricalTurnDetail(
	backendID, sessionID, turnID string,
	generation int,
	detailParts []ProjectionPart,
) (SessionProjection, []ProjectionPatch, error) {
	if k == nil {
		return SessionProjection{}, nil, errors.New("projection kernel nil")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	session := k.sessionLocked(backendID, sessionID)
	if session.status.Phase != ProjectionHydrateReady {
		return SessionProjection{}, nil, fmt.Errorf(
			"projection: detail merge requires ready session, %s/%s is %s",
			backendID, sessionID, session.status.Phase,
		)
	}
	return k.reducer.MergeHistoricalTurnDetail(backendID, sessionID, turnID, generation, detailParts)
}
