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

	// ALL backends go through hydrate (design §10.5.7 修法 1 — no codex hardcode). A backend not
	// yet migrated to projection returns an honest error; it must NEVER fall through to an empty
	// head-0 shell (§10.5.1).
	if err := h.ensureProjectionHydrated(msg.BackendID, params.SessionID); err != nil {
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
	return backendID == "codex"
}

// ensureProjectionHydrated waits for a full committed baseline within the pull budget. Concurrent
// pulls join the Kernel single-flight. Budget expiry returns projection.hydrating without
// cancelling a healthy transaction.
func (h *Handlers) ensureProjectionHydrated(backendID, sessionID string) error {
	if h == nil || h.eventPublisher == nil || h.projectionKernel == nil || sessionID == "" {
		return nil
	}
	if h.projectionKernel.Status(backendID, sessionID).Phase == ProjectionHydrateReady {
		return nil
	}
	if !backendSupportsProjectionHydrate(backendID) {
		return errProjectionBackendNotMigrated
	}
	source, err := h.prepareProjectionHydrateSource(context.Background(), backendID, sessionID)
	if err != nil {
		h.projectionKernel.MarkFailed(
			backendID, sessionID, "projection.source_inspection_failed", err.Error(), true,
		)
		return err
	}
	admission, err := h.projectionKernel.BeginHydrateTransaction(
		backendID, sessionID, source, false, false,
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
	backendID, sessionID string,
) (ProjectionSourceDescriptor, error) {
	agentName := ""
	switch backendID {
	case "codex":
		agentName = "codex"
	case "claude", "claudecode":
		agentName = "claudecode"
	default:
		return ProjectionSourceDescriptor{}, errProjectionBackendNotMigrated
	}
	agent, ok := h.getFirstAgentByName(agentName)
	if !ok {
		if h.eventPublisher.ProjectionTurnCount(backendID, sessionID) > 0 {
			return ProjectionSourceDescriptor{Identity: sessionID}, nil
		}
		return ProjectionSourceDescriptor{}, errProjectionSourceUnavailable
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
	err := h.produceProjectionHydrateRange(
		ctx,
		backendID,
		sessionID,
		source.Path,
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
	if source.Path != "" {
		source.Cursor = admission.StartCut
		sourceCheckpoint, checkpointErr := BuildProjectionSourceCheckpoint(source)
		if checkpointErr != nil {
			slog.Error("projection checkpoint source build failed",
				"backendID", backendID, "sessionID", sessionID, "error", checkpointErr)
			return
		}
		checkpoint := NewReadyProjectionCheckpoint(
			backendID, sessionID, sourceCheckpoint, commit.Projection, time.Now(),
		)
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
				"sourceCursor", checkpoint.Source.Cursor,
				"settled", settled,
			)
		}
	}
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
	if path == "" || startOffset == endOffset {
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
				TurnDone: ev.kind == "task_complete",
			})
		})
	case "claude", "claudecode":
		return streamClaudeTranscriptProjectionEventsRangeSeed(
			ctx, path, startOffset, endOffset, currentTurnID, emit,
		)
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
	Event    string
	Data     map[string]interface{}
	TurnDone bool
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
