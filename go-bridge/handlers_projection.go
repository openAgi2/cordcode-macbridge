package gobridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// GetSessionProjectionParams — get_session_projection request params. Additive; sits beside
// GetSessionMessagesParams. See docs/protocol/bridge-v1.md「Session Projection Stream」.
type GetSessionProjectionParams struct {
	SessionID  string `json:"sessionId"`
	Directory  string `json:"directory,omitempty"`
	SinceRev   int    `json:"sinceRev,omitempty"`
	LimitTurns int    `json:"limitTurns,omitempty"`
}

// defaultColdHydrateTimeout is the RPC budget for a complete committed baseline. If the
// transaction remains healthy past this budget, the RPC returns projection.hydrating while the
// single-flight continues; it never serves a partial or empty success shell.
const defaultColdHydrateTimeout = 15 * time.Second

// coldHydrateTimeout is the live pull budget. Tests may lower it.
var coldHydrateTimeout = defaultColdHydrateTimeout

// defaultColdHydrateBackgroundBudget bounds the full baseline transaction after an RPC has
// returned projection.hydrating.
const defaultColdHydrateBackgroundBudget = 5 * time.Minute

// coldHydrateBackgroundBudget is the live background budget. Tests may lower it.
var coldHydrateBackgroundBudget = defaultColdHydrateBackgroundBudget

var errProjectionHydrating = errors.New("projection hydration is still in progress")

const projectionHydratingRetryAfter = 250 * time.Millisecond

// coldHydrateTestHook, when non-nil, runs once at the start of the hydrate goroutine.
var coldHydrateTestHook func(context.Context)

// coldHydrateSegmentTestHook observes the isolated transaction reducer at turn boundaries.
var coldHydrateSegmentTestHook func(ctx context.Context, segmentIdx, contentTurns int)

// handleGetSessionProjection returns the authoritative SessionProjection for a session, read
// from the ProjectionReducer's in-memory state — the SAME state push reads (design §6.4 rule 4:
// pull == push state). It is NOT a wrapper around get_session_messages: it returns projection
// turns, never raw history bodies for the client to merge.
//
// After restart, the Kernel restores a validated checkpoint or reduces exactly
// [checkpointCursor,startCut) in an isolated transaction. Post-cut live events queue until the
// baseline commits. A completed source inspection may commit a true empty session; a bare turn
// shell remains hydrating.
func (h *Handlers) handleGetSessionProjection(conn Connection, msg WireMessage, agent core.Agent) {
	var params GetSessionProjectionParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	params.SessionID = h.resolveSessionIDForActiveSession(params.SessionID)
	logProjectionRPCTrace("mac_receive", msg, params.SessionID, params.SinceRev, -1, "", nil)

	// Subscribe so the conn receives subsequent projection_patch push frames (WP5 emission).
	h.subscribeConnToSession(conn, msg, params.SessionID)
	// Projection-only clients never call get_session_messages, so subscribing alone is not
	// enough for file-backed external sessions: nothing would be watching Claude/Codex JSONL
	// growth and the reducer would never receive live events. Start the same read-only relay
	// ownership used by the legacy history-open path.
	if msg.BackendID != "claude" && msg.BackendID != "claudecode" {
		h.startProjectionLiveRelay(params.SessionID, conn, msg.BackendID, agent, params.Directory)
	}

	// ALL backends go through hydrate (design §10.5.7 修法 1 — no codex hardcode). A backend not
	// yet migrated to projection returns an honest error; it must NEVER fall through to an empty
	// head-0 shell (§10.5.1).
	// A cold open inspects sources even when the Kernel is Ready. OpenCode uses
	// this to heal its pathless HTTP baseline; Claude uses it to detect a new
	// compact continuation or advanced segment cut.
	forceColdInspection := params.SinceRev == 0 &&
		(msg.BackendID == "opencode" || msg.BackendID == "grokbuild" ||
			msg.BackendID == "claude" || msg.BackendID == "claudecode")
	if err := h.ensureProjectionHydrated(
		msg.BackendID,
		params.SessionID,
		params.Directory,
		forceColdInspection,
	); err != nil {
		code := "projection.hydrate_failed"
		retryable := false
		var retryAfterMillis *int64
		attempts := 0
		switch {
		case errors.Is(err, errProjectionHydrating):
			code = "projection.hydrating"
			retryable = true
			value := projectionHydratingRetryAfter.Milliseconds()
			retryAfterMillis = &value
		case errors.Is(err, errProjectionBackendNotMigrated):
			code = "projection.not_migrated"
		default:
			status := h.projectionKernel.Status(msg.BackendID, params.SessionID)
			if failure := status.Failure; failure != nil {
				retryable = failure.Retryable
				attempts = failure.Attempts
				if failure.Retryable {
					value := time.Until(failure.RetryAt).Milliseconds()
					if value < 0 {
						value = 0
					}
					retryAfterMillis = &value
				}
			}
		}
		logProjectionRPCTrace("response_enqueue", msg, params.SessionID, params.SinceRev, -1, code, nil)
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:             code,
			Message:          err.Error(),
			Retryable:        &retryable,
			RetryAfterMillis: retryAfterMillis,
			Attempts:         attempts,
		})
		return
	}
	if msg.BackendID == "claude" || msg.BackendID == "claudecode" {
		// Hydrate owns the Claude source first. The live reader then inherits the committed
		// complete-record cursor, making the baseline/live ranges disjoint without resampling
		// file size and without adding a second projection writer.
		h.startProjectionLiveRelay(params.SessionID, conn, msg.BackendID, agent, params.Directory)
	}

	proj, admission, snapshotErr := h.eventPublisher.BeginProjectionSnapshot(
		conn,
		msg.BackendID,
		params.SessionID,
	)
	if snapshotErr != nil {
		logProjectionRPCTrace("response_enqueue", msg, params.SessionID, params.SinceRev, -1, "projection.not_ready", nil)
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "projection.not_ready",
			Message: snapshotErr.Error(),
		})
		return
	}
	headRev := proj.SyncRev
	logProjectionRPCTrace("hydrate_ready", msg, params.SessionID, params.SinceRev, headRev, "", &admission)

	// Cheap delta response when the client is already at head: empty patch set.
	if params.SinceRev != 0 && params.SinceRev == headRev {
		logProjectionRPCTrace("response_enqueue", msg, params.SessionID, params.SinceRev, headRev, "delta_at_head", &admission)
		if err := h.eventPublisher.CompleteProjectionSnapshot(conn, admission, msg.RequestID, map[string]interface{}{
			"patches": []ProjectionPatch{},
			"headRev": headRev,
		}); err != nil {
			slog.Warn("go-bridge: projection snapshot response enqueue failed", "requestId", msg.RequestID, "error", err)
		}
		return
	}

	logProjectionRPCTrace("response_enqueue", msg, params.SessionID, params.SinceRev, proj.SyncRev, "snapshot", &admission)
	if err := h.eventPublisher.CompleteProjectionSnapshot(
		conn,
		admission,
		msg.RequestID,
		map[string]interface{}{"projection": proj},
	); err != nil {
		slog.Warn("go-bridge: projection snapshot response enqueue failed", "requestId", msg.RequestID, "error", err)
	}
}

// startProjectionLiveRelay attaches the live producer required after a projection-only open.
// It deliberately mirrors handleGetSessionMessages without reading or merging legacy history:
// the resulting logical events reduce into SessionProjection, and v2 clients consume only
// projection_patch.
func (h *Handlers) startProjectionLiveRelay(
	sessionID string,
	conn Connection,
	backendID string,
	agent core.Agent,
	directory string,
) {
	h.mu.Lock()
	sess, hasSess := h.getSession(sessionID)
	h.mu.Unlock()
	if hasSess && sess != nil {
		h.startRelayIfNotRunning(sessionID, sess, conn, backendID)
	} else {
		if cursor, ok := h.projectionKernel.CommittedSourceCursor(backendID, sessionID); ok {
			h.startClaudeSessionFileRelayAt(sessionID, conn, backendID, &cursor)
		} else {
			h.startClaudeSessionFileRelay(sessionID, conn, backendID)
		}
	}
	h.startCodexSessionFileRelay(sessionID, conn, backendID, agent)
	h.startGrokLeaderSessionRelay(sessionID, backendID, agent, directory)
}

// logProjectionRPCTrace emits the minimal cross-process evidence needed to align one projection
// pull from iOS send through Mac receive/hydrate/response enqueue. requestId is the correlation
// key; session/backend/revision fields identify the projection head without logging content.
// This is observability only: it deliberately does not alter hydrate, reducer, or delivery order.
func logProjectionRPCTrace(
	stage string,
	msg WireMessage,
	sessionID string,
	sinceRev, headRev int,
	outcome string,
	admission *ProjectionSnapshotAdmission,
) {
	attrs := []any{
		"stage", stage,
		"requestId", msg.RequestID,
		"backendID", msg.BackendID,
		"sessionID", sessionID,
		"sinceRev", sinceRev,
	}
	if headRev >= 0 {
		attrs = append(attrs, "headRev", headRev)
	}
	if outcome != "" {
		attrs = append(attrs, "outcome", outcome)
	}
	if admission != nil {
		attrs = append(
			attrs,
			"bridgeEpoch", admission.BridgeEpoch,
			"connectionGeneration", admission.ConnectionGeneration,
			"cutRev", admission.CutRev,
		)
	}
	slog.Info("go-bridge: projection_rpc", attrs...)
}

// errProjectionBackendNotMigrated is returned for a backend that has no projection cold-hydrate
// producer yet (e.g. opencode, which is HTTP/SQLite-backed with no transcript file). The RPC
// fails honestly with code projection.not_migrated instead of serving an empty head-0 shell
// (§10.5.1 / §10.5.7 修法 1).
var errProjectionBackendNotMigrated = errors.New("backend not yet migrated to session projection")
var errProjectionSourceUnavailable = errors.New("projection source is not available for inspection")

func backendSupportsProjectionHydrate(backendID string) bool {
	switch backendID {
	case "codex", "claude", "claudecode", "opencode", "grokbuild":
		// K5: Codex/Claude use JSONL transcript hydrate; OpenCode uses HTTP rich-history
		// full rebuild (no transcript file / no file-prefix checkpoint); grokbuild uses the
		// same pathless rich-history rebuild from local chat_history.jsonl.
		return true
	default:
		return false
	}
}

// ensureProjectionHydrated waits for a full committed baseline within the pull budget. Concurrent
// pulls join the Kernel single-flight. Budget expiry returns projection.hydrating without
// cancelling a healthy transaction.
func (h *Handlers) ensureProjectionHydrated(
	backendID, sessionID, directory string,
	forceColdInspection bool,
) error {
	if h == nil || h.eventPublisher == nil || h.projectionKernel == nil || sessionID == "" {
		return nil
	}
	ready := h.projectionKernel.Status(backendID, sessionID).Phase == ProjectionHydrateReady
	// Cheap Ready hit for file-backed backends, and for OpenCode incremental pulls.
	// Cold OpenCode pulls may force a pathless rich-history rebuild below.
	if ready && !forceColdInspection {
		return nil
	}
	if !backendSupportsProjectionHydrate(backendID) {
		return errProjectionBackendNotMigrated
	}
	source, err := h.prepareProjectionHydrateSource(
		context.Background(),
		backendID,
		sessionID,
		directory,
	)
	if err != nil {
		h.projectionKernel.MarkFailed(
			backendID, sessionID, "projection.source_inspection_failed", err.Error(), true,
		)
		return err
	}
	// Pathless re-open: force full GetRichSessionHistory rebuild when already Ready.
	sourceChanged := forceColdInspection && ready &&
		(backendID == "opencode" || backendID == "grokbuild") && source.Path == ""
	admission, err := h.projectionKernel.BeginHydrateTransaction(
		backendID, sessionID, source, false, sourceChanged,
	)
	if err != nil {
		return err
	}
	if admission.AlreadyReady {
		return nil
	}
	if admission.Leader {
		go h.runProjectionHydrateTransaction(backendID, sessionID, admission)
	}
	if admission.Done == nil {
		status := h.projectionKernel.Status(backendID, sessionID)
		if status.Phase == ProjectionHydrateFailed && status.Failure != nil {
			return errors.New(status.Failure.Message)
		}
		return errProjectionHydrating
	}
	budget := coldHydrateTimeout
	if budget <= 0 {
		budget = defaultColdHydrateTimeout
	}
	select {
	case <-admission.Done:
		status := h.projectionKernel.Status(backendID, sessionID)
		if status.Phase == ProjectionHydrateReady {
			return nil
		}
		if status.Failure != nil {
			return errors.New(status.Failure.Message)
		}
		return errors.New("projection hydrate ended without a committed state")
	case <-time.After(budget):
		return fmt.Errorf("%w; retry after hydrate completes", errProjectionHydrating)
	}
}

func (h *Handlers) prepareProjectionHydrateSource(
	ctx context.Context,
	backendID, sessionID, directory string,
) (ProjectionSourceDescriptor, error) {
	agentName := ""
	switch backendID {
	case "codex":
		agentName = "codex"
	case "claude", "claudecode":
		agentName = "claudecode"
	case "opencode":
		agentName = "opencode"
	case "grokbuild":
		agentName = "grokbuild"
	default:
		return ProjectionSourceDescriptor{}, errProjectionBackendNotMigrated
	}
	// Claude session lists are global across ~/.claude/projects and each row carries its real
	// directory. Resolve the transcript from that session identity instead of the agent's mutable
	// workDir. A projection pull is read-only and multiple observers may cold-open different
	// projects concurrently, so changing shared agent workDir here would create a cross-device
	// directory race.
	if backendID == "claude" || backendID == "claudecode" {
		agent, ok := h.getFirstAgentByName("claudecode")
		if ok {
			if provider, composite := agent.(core.CompositeRichHistoryProvider); composite {
				physical, err := provider.RichHistoryTranscriptSegments(ctx, sessionID)
				if err != nil {
					return ProjectionSourceDescriptor{}, err
				}
				if len(physical) == 0 {
					return ProjectionSourceDescriptor{}, errProjectionSourceUnavailable
				}
				segments := make([]ProjectionSourceSegment, 0, len(physical))
				var totalCut int64
				for _, physicalSegment := range physical {
					path := strings.TrimSpace(physicalSegment.Path)
					identity := strings.TrimSpace(physicalSegment.Identity)
					if path == "" || identity == "" {
						return ProjectionSourceDescriptor{}, errProjectionSourceUnavailable
					}
					cut, cutErr := projectionJSONLStartCut(path)
					if cutErr != nil {
						return ProjectionSourceDescriptor{}, cutErr
					}
					segments = append(segments, ProjectionSourceSegment{
						Identity: identity,
						Path:     path,
						Cursor:   cut,
					})
					totalCut += cut
				}
				confirmed, confirmErr := provider.RichHistoryTranscriptSegments(ctx, sessionID)
				if confirmErr != nil {
					return ProjectionSourceDescriptor{}, confirmErr
				}
				if !sameTranscriptSegmentMembership(physical, confirmed) {
					return ProjectionSourceDescriptor{}, fmt.Errorf(
						"%w: Claude continuation chain changed during hydrate admission",
						ErrProjectionCheckpointInvalid,
					)
				}
				return ProjectionSourceDescriptor{
					Identity: sessionID,
					Cursor:   totalCut,
					Segments: segments,
				}, nil
			}
		}
		// Test/custom providers without the explicit stitching guarantee retain
		// the normal single-transcript path and its checkpoint semantics.
		_, path := findClaudeSessionFile(sessionID, directory)
		if path != "" {
			cut, err := projectionJSONLStartCut(path)
			if err != nil {
				return ProjectionSourceDescriptor{}, err
			}
			return ProjectionSourceDescriptor{Identity: sessionID, Path: path, Cursor: cut}, nil
		}
	}
	agent, ok := h.getFirstAgentByName(agentName)
	if !ok {
		if h.eventPublisher.ProjectionTurnCount(backendID, sessionID) > 0 {
			return ProjectionSourceDescriptor{Identity: sessionID}, nil
		}
		return ProjectionSourceDescriptor{}, errProjectionSourceUnavailable
	}
	// OpenCode has no JSONL transcript path; grokbuild's chat_history.jsonl is a structured
	// turn snapshot, not a raw transcript with stable byte cursors. Both cold-hydrate as a full
	// rich-history rebuild keyed by session identity only (Cursor=0, Path empty). Checkpoint
	// file-prefix validation does not apply; re-open always rebuilds from GetRichSessionHistory.
	if backendID == "opencode" || backendID == "grokbuild" {
		if _, ok := agent.(core.RichHistoryProvider); !ok {
			if h.eventPublisher.ProjectionTurnCount(backendID, sessionID) > 0 {
				return ProjectionSourceDescriptor{Identity: sessionID}, nil
			}
			return ProjectionSourceDescriptor{}, errProjectionSourceUnavailable
		}
		return ProjectionSourceDescriptor{
			Identity: sessionID,
			Path:     "",
			Cursor:   0,
		}, nil
	}
	locator, ok := agent.(core.TranscriptLocator)
	if !ok {
		if h.eventPublisher.ProjectionTurnCount(backendID, sessionID) > 0 {
			return ProjectionSourceDescriptor{Identity: sessionID}, nil
		}
		return ProjectionSourceDescriptor{}, errProjectionSourceUnavailable
	}
	path, err := locator.TranscriptPath(ctx, sessionID)
	if err != nil {
		return ProjectionSourceDescriptor{}, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return ProjectionSourceDescriptor{}, errProjectionSourceUnavailable
	}
	cut, err := projectionJSONLStartCut(path)
	if err != nil {
		return ProjectionSourceDescriptor{}, err
	}
	return ProjectionSourceDescriptor{
		Identity: sessionID,
		Path:     path,
		Cursor:   cut,
	}, nil
}

func sameTranscriptSegmentMembership(
	lhs, rhs []core.TranscriptSourceSegment,
) bool {
	if len(lhs) != len(rhs) {
		return false
	}
	for index := range lhs {
		if strings.TrimSpace(lhs[index].Identity) != strings.TrimSpace(rhs[index].Identity) ||
			strings.TrimSpace(lhs[index].Path) != strings.TrimSpace(rhs[index].Path) {
			return false
		}
	}
	return true
}

func projectionJSONLStartCut(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size == 0 {
		return 0, nil
	}
	var one [1]byte
	if _, err := f.ReadAt(one[:], size-1); err != nil {
		return 0, err
	}
	if one[0] == '\n' {
		return size, nil
	}
	const blockSize = int64(4096)
	for end := size; end > 0; {
		start := end - blockSize
		if start < 0 {
			start = 0
		}
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil {
			return 0, err
		}
		for i := len(buf) - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				return start + int64(i) + 1, nil
			}
		}
		end = start
	}
	return 0, nil
}

// ensureClaudeSourceStateInstalled installs a fresh Mac-private Claude source ledger when one is
// not already present (hydrate commit or checkpoint restore owns the first install; the live file
// relay also calls this at startup using its inherited cursor). Cursor identity is taken from
// claudeSourceCorrelation.Observe at the given admission cut, matching the live reader's Observe at
// the same cut, so the first live source batch clears the Kernel cursor/gap/generation fence.
// Idempotent; Mac-private; never enters a wire payload (guardrails #1/#3/#5).
func (h *Handlers) ensureClaudeSourceStateInstalled(backendID, sessionID, sourcePath string, admissionCut int64) {
	if backendID != "claude" && backendID != "claudecode" {
		return
	}
	if sourcePath == "" || admissionCut < 0 {
		return
	}
	if _, ok := h.projectionKernel.ClaudeSourceStateSnapshot(backendID, sessionID); ok {
		return
	}
	correlation, err := h.claudeSourceCorrelation.Observe(backendID, sessionID, sessionID, sourcePath, admissionCut)
	if err != nil {
		slog.Warn("go-bridge: Claude source-state install correlation unavailable",
			"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID), "error", err)
		return
	}
	state, err := newInitialClaudeSourceState(correlation.SegmentStableKey, correlation.SegmentGeneration, admissionCut)
	if err != nil {
		slog.Warn("go-bridge: Claude source-state install build failed",
			"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID), "error", err)
		return
	}
	if err := h.projectionKernel.InstallClaudeSourceState(backendID, sessionID, state); err != nil {
		slog.Warn("go-bridge: Claude source-state install failed",
			"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID), "error", err)
	}
}

func (h *Handlers) runProjectionHydrateTransaction(
	backendID, sessionID string,
	admission ProjectionHydrateAdmission,
) {
	startedAt := time.Now()
	budget := coldHydrateBackgroundBudget
	if budget <= 0 {
		budget = defaultColdHydrateBackgroundBudget
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	select {
	case h.projectionHydrateSlots <- struct{}{}:
		defer func() { <-h.projectionHydrateSlots }()
	case <-ctx.Done():
		h.projectionKernel.MarkFailed(
			backendID,
			sessionID,
			"projection.hydrate_queue_timeout",
			ctx.Err().Error(),
			true,
		)
		return
	}
	if coldHydrateTestHook != nil {
		coldHydrateTestHook(ctx)
	}
	source, _ := h.projectionKernel.HydrateSource(backendID, sessionID)
	base, _ := h.projectionKernel.HydrateSnapshot(backendID, sessionID)
	segmentIdx := 0
	err := h.produceProjectionHydrateSource(
		ctx,
		backendID,
		sessionID,
		source,
		admission.StartCursor,
		admission.StartCut,
		base,
		func(event projectionHydrateEvent) bool {
			h.projectionKernel.ApplyHydrateEvent(
				backendID,
				sessionID,
				h.eventPublisher.BridgeEpoch(),
				event.Event,
				event.Data,
			)
			if event.TurnDone {
				segmentIdx++
				if coldHydrateSegmentTestHook != nil {
					hydrating, _ := h.projectionKernel.HydrateSnapshot(backendID, sessionID)
					coldHydrateSegmentTestHook(ctx, segmentIdx, len(hydrating.Turns))
				}
			}
			return ctx.Err() == nil
		},
	)
	if err != nil {
		h.projectionKernel.MarkFailed(
			backendID, sessionID, "projection.source_read_failed", err.Error(), true,
		)
		return
	}
	// B4 child-stream (sync-only hydrate): after the mainstream Claude transcript is reduced
	// into this transaction, read sibling sidechain files (subagents/agent-*.jsonl + .meta.json)
	// and emit subagent_part events through the same ApplyHydrateEvent transaction. Claude only —
	// Codex/OpenCode have no sidechain files. The mainstream anchor map (Agent/Task tool_use id →
	// owning turnId) is derived from the in-transaction snapshot so depth-1 subagents attach to
	// the exact turns just scanned, with no re-scan. Fail-open: any read/build error is logged
	// and the hydrate proceeds without subagent parts (current state preserved, no fabrication,
	// guardrail §10). See claude_sidechain_subagents.go.
	if backendID == "claude" || backendID == "claudecode" {
		if subagentsDir := claudeSubagentsDir(source); subagentsDir != "" {
			hydrated, _ := h.projectionKernel.HydrateSnapshot(backendID, sessionID)
			mainstreamToolUseTurn := map[string]string{}
			for _, turn := range hydrated.Turns {
				if turn.Assistant == nil {
					continue
				}
				for _, part := range turn.Assistant.Parts {
					if part.Type == "tool" && (part.ToolName == "Agent" || part.ToolName == "Task") && part.ItemID != "" {
						mainstreamToolUseTurn[part.ItemID] = turn.TurnID
					}
				}
			}
			sidechainEmit := func(event projectionHydrateEvent) bool {
				h.projectionKernel.ApplyHydrateEvent(
					backendID, sessionID, h.eventPublisher.BridgeEpoch(),
					event.Event, event.Data,
				)
				return ctx.Err() == nil
			}
			if err := produceClaudeSidechainSubagentEvents(ctx, subagentsDir, mainstreamToolUseTurn, sidechainEmit); err != nil {
				slog.Warn("go-bridge: Claude sidechain subagent source-read failed; failing open",
					"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID), "error", err)
			}
		}
	}
	// §5.1 #7: cold-source ingest (mainstream + Claude sidechain) is now complete — no more
	// ApplyHydrateEvent calls will be made from the cold source. Arm the commit gate so
	// WaitHydrateCommitReady decides readiness from authoritative source-EOF + turn terminal
	// state instead of content-shape/turn-count guessing (guardrail #6).
	h.projectionKernel.MarkHydrateSourceIngestComplete(backendID, sessionID)
	if err := h.projectionKernel.WaitHydrateCommitReady(ctx, backendID, sessionID); err != nil {
		h.projectionKernel.MarkFailed(
			backendID, sessionID, "projection.bare_source_wait_failed", err.Error(), true,
		)
		return
	}
	commit, err := h.projectionKernel.CommitHydrateTransaction(backendID, sessionID)
	if err != nil {
		h.projectionKernel.MarkFailed(
			backendID, sessionID, "projection.commit_failed", err.Error(), true,
		)
		return
	}
	slog.Info(
		"go-bridge: projection_shadow",
		"stage", "hydrate_commit",
		"policyVersion", SessionSyncV2PolicyVersion,
		"backendID", backendID,
		"sessionPrefix", projectionSessionLogPrefix(sessionID),
		"checkpointHit", admission.CheckpointHit,
		"startCursor", admission.StartCursor,
		"startCut", admission.StartCut,
		"headRev", commit.Projection.SyncRev,
		"pendingLive", commit.PendingLive,
		"elapsedMillis", time.Since(startedAt).Milliseconds(),
	)
	if commit.PendingPatch != nil {
		h.eventPublisher.PublishProjectionPatch(backendID, sessionID, *commit.PendingPatch)
	}
	// Install a fresh Mac-private Claude source ledger now that hydrate owns the source cut, so
	// the live file relay can route content through the source-batch transaction. No-op when a
	// checkpoint already restored one. Must run before the live relay's startup correlation.
	if cursor, ok := h.projectionKernel.CommittedSourceCursor(backendID, sessionID); ok {
		h.ensureClaudeSourceStateInstalled(backendID, sessionID, source.Path, cursor)
	}
	if source.Path != "" || len(source.Segments) > 0 {
		source.Cursor = admission.StartCut
		sourceCheckpoints, checkpointErr := BuildProjectionSourceCheckpoints(source)
		if checkpointErr != nil {
			slog.Error("projection checkpoint source build failed",
				"backendID", backendID, "sessionID", sessionID, "error", checkpointErr)
			return
		}
		var checkpoint ProjectionCheckpoint
		if len(source.Segments) > 0 {
			checkpoint = NewReadyCompositeProjectionCheckpoint(
				backendID, sessionID, sourceCheckpoints, commit.Projection, time.Now(),
			)
		} else {
			checkpoint = NewReadyProjectionCheckpoint(
				backendID, sessionID, sourceCheckpoints[0], commit.Projection, time.Now(),
			)
		}
		settled := commit.Projection.Execution.Phase != "running"
		if checkpointErr := h.projectionKernel.StageCheckpoint(checkpoint, settled); checkpointErr != nil &&
			!errors.Is(checkpointErr, ErrProjectionCheckpointDisabled) {
			slog.Error("projection checkpoint stage failed",
				"backendID", backendID, "sessionID", sessionID, "error", checkpointErr)
		} else if checkpointErr == nil {
			slog.Info(
				"go-bridge: projection_shadow",
				"stage", "checkpoint_stage",
				"policyVersion", SessionSyncV2PolicyVersion,
				"backendID", backendID,
				"sessionPrefix", projectionSessionLogPrefix(sessionID),
				"headRev", checkpoint.ProjectionRev,
				"sourceCursor", source.Cursor,
				"settled", settled,
			)
		}
	}
}

func (h *Handlers) produceProjectionHydrateSource(
	ctx context.Context,
	backendID, sessionID string,
	source ProjectionSourceDescriptor,
	startOffset, endOffset int64,
	base SessionProjection,
	emit func(projectionHydrateEvent) bool,
) error {
	if len(source.Segments) == 0 {
		return h.produceProjectionHydrateRange(
			ctx, backendID, sessionID, source.Path, startOffset, endOffset, base, emit,
		)
	}
	if startOffset == endOffset {
		return nil
	}
	if backendID != "claude" && backendID != "claudecode" {
		return errProjectionBackendNotMigrated
	}
	agent, ok := h.getFirstAgentByName("claudecode")
	if !ok {
		return errProjectionSourceUnavailable
	}
	provider, ok := agent.(core.CompositeRichHistoryProvider)
	if !ok {
		return errProjectionSourceUnavailable
	}
	if claudeSourceTraceEnabled() {
		traceTurnID := ""
		for _, segment := range source.Segments {
			if err := h.traceClaudeHydrateRange(
				ctx, backendID, sessionID, segment.Identity, segment.Path,
				0, segment.Cursor, &traceTurnID,
			); err != nil {
				return err
			}
		}
	}
	segments := make([]core.TranscriptSourceSegment, 0, len(source.Segments))
	for _, segment := range source.Segments {
		segments = append(segments, core.TranscriptSourceSegment{
			Identity: segment.Identity,
			Path:     segment.Path,
			Cursor:   segment.Cursor,
		})
	}
	entries, err := provider.GetRichSessionHistoryAtSegments(ctx, sessionID, segments)
	if err != nil {
		return err
	}
	return streamRichHistoryProjectionEntries(ctx, entries, emit)
}

func projectionSessionLogPrefix(sessionID string) string {
	if len(sessionID) <= 8 {
		return sessionID
	}
	return sessionID[:8]
}

func (h *Handlers) produceProjectionHydrateRange(
	ctx context.Context,
	backendID, sessionID, path string,
	startOffset, endOffset int64,
	base SessionProjection,
	emit func(projectionHydrateEvent) bool,
) error {
	if backendID != "opencode" && backendID != "grokbuild" &&
		backendID != "claude" && backendID != "claudecode" &&
		(path == "" || startOffset == endOffset) {
		return nil
	}
	currentTurnID := base.Execution.ActiveTurnID
	if currentTurnID == "" {
		for i := len(base.Turns) - 1; i >= 0; i-- {
			if base.Turns[i].Status == "running" || base.Turns[i].Status == "pending" {
				currentTurnID = base.Turns[i].TurnID
				break
			}
		}
	}
	switch backendID {
	case "codex":
		toolNames := make(map[string]string)
		for i := range base.Turns {
			if base.Turns[i].Assistant == nil {
				continue
			}
			for _, part := range base.Turns[i].Assistant.Parts {
				if part.Type == "tool" && part.ItemID != "" && part.ToolName != "" {
					toolNames[part.ItemID] = part.ToolName
				}
			}
		}
		return streamCodexTranscriptRelayEventsRange(ctx, path, startOffset, endOffset, func(ev codexRelayEvent) bool {
			eventName, data, ok := codexRelayEventToProjectionEvent(ev, &currentTurnID, toolNames)
			if !ok {
				return true
			}
			return emit(projectionHydrateEvent{
				Event:    eventName,
				Data:     data,
				TurnDone: ev.kind == "task_complete" || ev.kind == "turn_aborted",
			})
		})
	case "claude", "claudecode":
		if path == "" {
			return h.streamClaudeRichHistoryProjectionEvents(ctx, sessionID, emit)
		}
		traceTurnID := currentTurnID
		if err := h.traceClaudeHydrateRange(
			ctx, backendID, sessionID, sessionID, path,
			startOffset, endOffset, &traceTurnID,
		); err != nil {
			return err
		}
		return streamClaudeTranscriptProjectionEventsRangeSeed(
			ctx, path, startOffset, endOffset, currentTurnID, emit,
		)
	case "opencode":
		return h.streamOpenCodeRichHistoryProjectionEvents(ctx, sessionID, emit)
	case "grokbuild":
		return h.streamBackendRichHistoryProjectionEvents(ctx, "grokbuild", sessionID, emit)
	default:
		return errProjectionBackendNotMigrated
	}
}

// projectionHydrateEvent is one logical projection event emitted by a backend's cold-hydrate
// parser (codex rollout / claude .jsonl). It mirrors the (Event, Data) carried by LogicalEvent
// minus delivery metadata, so the generic hydrate loop can publish across backends uniformly
// (design §10.5.7 修法 1 — full-backend reducer). TurnDone marks a turn-completion boundary
// (codex task_complete / claude final stop_reason): the segment-hook checkpoint and the point at
// which partial-readiness is re-evaluated.
type projectionHydrateEvent struct {
	Event              string
	Data               map[string]interface{}
	TurnDone           bool
	SourceBlockOrdinal *int
}

// hydrateToolEventsFromStep emits the tool_started + tool_finished projection-hydrate events
// for one rich-history tool step, preserving the structured fields iOS needs for friendly
// activity rows (fileChanges / title / toolInput). Previously hydration only copied
// itemId/toolName/toolStatus/toolResult, which dropped Codex structured fileChanges and
// Claude path-bearing title/toolInput, leaving iOS cold-start with no file path (R2/R5).
//
// For Claude cold-start this is necessary but NOT sufficient on its own: the upstream
// rich-history builder (richHistoryMessageBuilder.addToolUse) must first populate a valid
// title/toolInput (L-α) — otherwise this passthrough just forwards toolName as the title.
func hydrateToolEventsFromStep(step map[string]any) []projectionHydrateEvent {
	toolID := strings.TrimSpace(fmt.Sprint(step["id"]))
	toolName := strings.TrimSpace(fmt.Sprint(step["toolName"]))
	status := strings.TrimSpace(fmt.Sprint(step["status"]))
	if status == "" || status == "<nil>" {
		status = "completed"
	}
	if toolID == "" || toolID == "<nil>" {
		return nil
	}
	started := map[string]interface{}{"itemId": toolID}
	if toolName != "" && toolName != "<nil>" {
		started["toolName"] = toolName
	}
	// Preserve path-bearing fields on tool_started so iOS can render a friendly title
	// before completion (e.g. "正在编辑 <file>").
	copyOptionalStepField(started, step, "title")
	copyOptionalStepField(started, step, "toolInput")
	copyOptionalStepField(started, step, "fileChanges")

	finished := map[string]interface{}{
		"itemId":     toolID,
		"toolStatus": status,
	}
	if toolName != "" && toolName != "<nil>" {
		finished["toolName"] = toolName
	}
	if output := step["output"]; output != nil {
		finished["toolResult"] = output
	}
	// Same structured fields on tool_finished so the completed activity row has path/diff.
	copyOptionalStepField(finished, step, "title")
	copyOptionalStepField(finished, step, "toolInput")
	copyOptionalStepField(finished, step, "fileChanges")

	return []projectionHydrateEvent{
		{Event: "tool_started", Data: started},
		{Event: "tool_finished", Data: finished},
	}
}

func hydrateUserInputEventsFromPart(part map[string]any, turnID string) []projectionHydrateEvent {
	interactionID := dataString(part, "interactionId")
	if interactionID == "" || turnID == "" {
		return nil
	}
	status := dataString(part, "status")
	if status == "" {
		status = "pending"
	}
	requestStatus := status
	if status != "pending" && status != "failed" {
		requestStatus = "pending"
	}
	request := map[string]interface{}{
		"turnId":         turnID,
		"interactionId":  interactionID,
		"status":         requestStatus,
		"questions":      part["questions"],
		"canRespond":     dataBool(part, "canRespond"),
		"canReject":      dataBool(part, "canReject"),
		"diagnosticCode": dataString(part, "diagnosticCode"),
	}
	if itemID := dataString(part, "itemId"); itemID != "" {
		request["itemId"] = itemID
	}
	out := []projectionHydrateEvent{{Event: "user_input_requested", Data: request}}
	if status == "pending" || status == "failed" {
		return out
	}
	resolved := map[string]interface{}{
		"turnId":        turnID,
		"interactionId": interactionID,
		"status":        status,
		"source":        dataString(part, "resolutionSource"),
	}
	if itemID := dataString(part, "itemId"); itemID != "" {
		resolved["itemId"] = itemID
	}
	if resolvedAt := dataInt64(part, "resolvedAt"); resolvedAt != 0 {
		resolved["resolvedAt"] = resolvedAt
	}
	out = append(out, projectionHydrateEvent{Event: "user_input_resolved", Data: resolved})
	return out
}

// copyOptionalStepField copies a non-nil, non-empty step field into the target hydration
// event data under the same key. It deliberately forwards whatever the upstream builder
// produced (including structured fileChanges []any / toolInput string / title string)
// without inventing or transforming values.
func copyOptionalStepField(target map[string]interface{}, step map[string]any, key string) {
	if v, ok := step[key]; ok && v != nil {
		// Skip fmt "<nil>" stringified placeholders the upstream may emit.
		if s, isStr := v.(string); isStr {
			if strings.TrimSpace(s) == "" || s == "<nil>" {
				return
			}
		}
		target[key] = v
	}
}

// streamOpenCodeRichHistoryProjectionEvents rebuilds a full projection baseline from
// OpenCode HTTP rich history (GET /session/{id}/message). There is no JSONL cursor:
// every cold hydrate is a complete ordered rebuild. Turn identity follows the Claude
// convention — the owning user message id is turnId/itemId for the whole turn.
func (h *Handlers) streamOpenCodeRichHistoryProjectionEvents(
	ctx context.Context,
	sessionID string,
	emit func(projectionHydrateEvent) bool,
) error {
	return h.streamBackendRichHistoryProjectionEvents(ctx, "opencode", sessionID, emit)
}

func (h *Handlers) streamClaudeRichHistoryProjectionEvents(
	ctx context.Context,
	sessionID string,
	emit func(projectionHydrateEvent) bool,
) error {
	return h.streamBackendRichHistoryProjectionEvents(ctx, "claudecode", sessionID, emit)
}

func (h *Handlers) streamBackendRichHistoryProjectionEvents(
	ctx context.Context,
	agentName, sessionID string,
	emit func(projectionHydrateEvent) bool,
) error {
	if h == nil {
		return errProjectionSourceUnavailable
	}
	agent, ok := h.getFirstAgentByName(agentName)
	if !ok {
		return errProjectionSourceUnavailable
	}
	provider, ok := agent.(core.RichHistoryProvider)
	if !ok {
		return errProjectionSourceUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	entries, err := provider.GetRichSessionHistory(ctx, sessionID, 0)
	if err != nil {
		return err
	}
	return streamRichHistoryProjectionEntries(ctx, entries, emit)
}

func streamRichHistoryProjectionEntries(
	ctx context.Context,
	entries []core.RichHistoryEntry,
	emit func(projectionHydrateEvent) bool,
) error {
	currentTurnID := ""
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, ev := range openCodeRichHistoryEntryToProjectionEvents(entry, &currentTurnID) {
			if !emit(ev) {
				return ctx.Err()
			}
		}
	}
	return ctx.Err()
}

func openCodeRichHistoryEntryToProjectionEvents(
	entry core.RichHistoryEntry,
	currentTurnID *string,
) []projectionHydrateEvent {
	role := strings.ToLower(strings.TrimSpace(entry.Role))
	identity := strings.TrimSpace(entry.ID)
	if identity == "" {
		// Honest identity only: without a source message id the reducer cannot attribute
		// the turn. Skip rather than inventing a synthetic id.
		return nil
	}
	var out []projectionHydrateEvent
	switch role {
	case "user":
		text := strings.TrimSpace(entry.Content)
		if text == "" {
			// Prefer explicit text parts if content was empty.
			var b strings.Builder
			for _, part := range entry.Parts {
				if strings.TrimSpace(fmt.Sprint(part["type"])) != "text" {
					continue
				}
				chunk := strings.TrimSpace(fmt.Sprint(part["content"]))
				if chunk == "" || chunk == "<nil>" {
					chunk = strings.TrimSpace(fmt.Sprint(part["text"]))
				}
				if chunk == "" || chunk == "<nil>" {
					continue
				}
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(chunk)
			}
			text = b.String()
		}
		if text == "" {
			return nil
		}
		*currentTurnID = identity
		out = append(out, projectionHydrateEvent{
			Event: "user_message",
			Data: map[string]interface{}{
				"itemId": identity,
				"turnId": identity,
				"text":   text,
			},
		})
		return out
	case "assistant":
		turnID := *currentTurnID
		if turnID == "" {
			turnID = identity
			*currentTurnID = turnID
		}
		emittedContent := false
		hasPendingUserInput := false
		// Prefer structured parts when present.
		if len(entry.Parts) > 0 {
			for _, part := range entry.Parts {
				ptype := strings.TrimSpace(fmt.Sprint(part["type"]))
				switch ptype {
				case "text":
					chunk := strings.TrimSpace(fmt.Sprint(part["content"]))
					if chunk == "" || chunk == "<nil>" {
						chunk = strings.TrimSpace(fmt.Sprint(part["text"]))
					}
					if chunk == "" || chunk == "<nil>" {
						continue
					}
					out = append(out, projectionHydrateEvent{
						Event: "text_delta",
						Data:  map[string]interface{}{"itemId": turnID, "delta": chunk},
					})
					emittedContent = true
				case "reasoning":
					chunk := strings.TrimSpace(fmt.Sprint(part["content"]))
					if chunk == "" || chunk == "<nil>" {
						chunk = strings.TrimSpace(fmt.Sprint(part["text"]))
					}
					if chunk == "" || chunk == "<nil>" {
						continue
					}
					out = append(out, projectionHydrateEvent{
						Event: "reasoning_delta",
						Data:  map[string]interface{}{"itemId": turnID, "delta": chunk},
					})
					emittedContent = true
				case "tool":
					step, _ := part["step"].(map[string]any)
					if step == nil {
						continue
					}
					out = append(out, hydrateToolEventsFromStep(step)...)
					emittedContent = true
				case "user_input":
					events := hydrateUserInputEventsFromPart(part, turnID)
					if len(events) == 0 {
						continue
					}
					out = append(out, events...)
					if strings.TrimSpace(fmt.Sprint(part["status"])) == "pending" {
						hasPendingUserInput = true
					}
					emittedContent = true
				}
			}
		}
		if !emittedContent {
			if thinking := strings.TrimSpace(entry.Thinking); thinking != "" {
				out = append(out, projectionHydrateEvent{
					Event: "reasoning_delta",
					Data:  map[string]interface{}{"itemId": turnID, "delta": thinking},
				})
				emittedContent = true
			}
			if content := strings.TrimSpace(entry.Content); content != "" {
				out = append(out, projectionHydrateEvent{
					Event: "text_delta",
					Data:  map[string]interface{}{"itemId": turnID, "delta": content},
				})
				emittedContent = true
			}
			for _, step := range entry.Steps {
				out = append(out, hydrateToolEventsFromStep(step)...)
				emittedContent = true
			}
		}
		// AskUserQuestion with no result is a blocking boundary, not a completed turn. Keeping
		// it open lets the reducer preserve execution.phase=requires_action so every composer
		// stays in the waiting state while the owning Claude Desktop session awaits an answer.
		if hasPendingUserInput {
			return out
		}
		// Other rich history rows are complete snapshots; seal the turn.
		out = append(out, projectionHydrateEvent{
			Event:    "turn_completed",
			Data:     map[string]interface{}{"turnId": turnID, "done": true, "reason": "rich_history"},
			TurnDone: true,
		})
		return out
	case "system":
		text := strings.TrimSpace(entry.Content)
		if text == "" {
			return nil
		}
		// Compaction can happen in the middle of one long tool-running response.
		// Seal its attribution boundary so assistant records after compact form a
		// new continuation turn instead of being merged back before this system row.
		*currentTurnID = ""
		data := map[string]interface{}{
			"itemId": identity,
			"turnId": identity,
			"text":   text,
		}
		if !entry.Timestamp.IsZero() {
			data["timestampMillis"] = entry.Timestamp.UnixMilli()
		}
		return []projectionHydrateEvent{{Event: "system_message", Data: data}}
	default:
		return nil
	}
}

// codexRelayEventToProjectionEvent maps a scanned codexRelayEvent to its projection LogicalEvent
// name + data, threading per-turn state (currentTurnID, toolNames). Returns ok=false for
// unhandled kinds (caller skips them). Extracted from the former inline hydrate switch so the
// segmented path preserves the exact mapping (no behavior fork).
func codexRelayEventToProjectionEvent(ev codexRelayEvent, currentTurnID *string, toolNames map[string]string) (string, map[string]interface{}, bool) {
	switch ev.kind {
	case "task_started":
		*currentTurnID = ev.turnID
		return "turn_started", map[string]interface{}{"turnId": ev.turnID}, true
	case "task_complete":
		tid := ev.turnID
		if tid == "" {
			tid = *currentTurnID
		}
		*currentTurnID = ""
		return "turn_completed", map[string]interface{}{"turnId": tid, "done": true, "reason": "task_complete"}, true
	case "turn_aborted":
		// §5.1 #7 producer layer 3：cold rollout 的 turn_aborted（真实形态 019f5453）映射到
		// reducer 的 turn_aborted 终态 case。turnID 回退同 task_complete（driver 可能省略）。
		// 清空 *currentTurnID——turn 已终态，后续 content 不再挂到它。
		tid := ev.turnID
		if tid == "" {
			tid = *currentTurnID
		}
		*currentTurnID = ""
		return "turn_aborted", map[string]interface{}{"turnId": tid, "reason": "turn_aborted"}, true
	case "text":
		if !ev.canonical {
			return "", nil, false
		}
		return "text_delta", map[string]interface{}{"itemId": *currentTurnID, "delta": ev.text, "newPart": ev.newPart}, true
	case "reasoning":
		if !ev.canonical {
			return "", nil, false
		}
		return "reasoning_delta", map[string]interface{}{"itemId": *currentTurnID, "delta": ev.text}, true
	case "user_message":
		return "user_message", map[string]interface{}{"itemId": ev.itemId, "turnId": *currentTurnID, "text": ev.text}, true
	case "tool_started":
		if ev.itemId != "" {
			toolNames[ev.itemId] = ev.toolName
		}
		data := map[string]interface{}{"toolName": ev.toolName, "toolInput": ev.toolInput}
		if ev.itemId != "" {
			data["itemId"] = ev.itemId
		}
		return "tool_started", data, true
	case "tool_finished":
		status := ev.toolStatus
		if status == "" {
			status = "completed"
		}
		data := map[string]interface{}{"toolResult": ev.toolResult, "toolStatus": status}
		if name, ok := toolNames[ev.itemId]; ok && name != "" {
			data["toolName"] = name
		}
		if ev.itemId != "" {
			data["itemId"] = ev.itemId
		}
		return "tool_finished", data, true
	default:
		return "", nil, false
	}
}

// ProjectionSnapshot returns a deep copy of the session projection (pull path accessor).
func (p *EventPublisher) ProjectionSnapshot(backendID, sessionID string) (SessionProjection, bool) {
	if p == nil || p.projection == nil {
		return SessionProjection{}, false
	}
	return p.projection.Snapshot(backendID, sessionID)
}

// ProjectionHeadRev returns the current head syncRev for the session.
func (p *EventPublisher) ProjectionHeadRev(backendID, sessionID string) int {
	if p == nil || p.projection == nil {
		return 0
	}
	return p.projection.LastAppliedRev(backendID, sessionID)
}

// ProjectionTurnCount returns the number of turns in the committed reducer.
func (p *EventPublisher) ProjectionTurnCount(backendID, sessionID string) int {
	if p == nil || p.projection == nil {
		return 0
	}
	return p.projection.TurnCount(backendID, sessionID)
}

// ProjectionHasContentTurn reports whether the committed reducer holds real message content.
func (p *EventPublisher) ProjectionHasContentTurn(backendID, sessionID string) bool {
	if p == nil || p.projection == nil {
		return false
	}
	return p.projection.HasContentTurn(backendID, sessionID)
}
