package gobridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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

// defaultColdHydrateTimeout is the budget a cold pull waits for the FIRST hydrate segment to
// produce a non-empty partial (design §10.5.6 scheme A). The first turn-bounded segment of even
// a huge rollout reduces in well under this bound, so a cold pull returns a non-empty partial
// fast; the bound is a worst-case backstop for a stuck/corrupt rollout that cannot produce even
// one content turn. Distinct from the removed 750ms "serve empty head" path: on expiry we return
// an explicit RPC error, never an empty success shell.
const defaultColdHydrateTimeout = 30 * time.Second

// coldHydrateTimeout is the live first-segment budget. Tests may lower it; production stays at
// the default. Scheme A does NOT raise this to "wait longer" — it returns a partial FAST instead.
var coldHydrateTimeout = defaultColdHydrateTimeout

// defaultColdHydrateBackgroundBudget bounds the background goroutine that finishes the remaining
// segments AFTER a partial has been served. It is intentionally much larger than
// coldHydrateTimeout so a multi-second full reduce of a huge rollout can complete and stream the
// rest as projection_patch deltas, while still bounding a stuck scan so it cannot occupy a
// goroutine + single-flight slot forever.
const defaultColdHydrateBackgroundBudget = 5 * time.Minute

// coldHydrateBackgroundBudget is the live background budget. Tests may lower it.
var coldHydrateBackgroundBudget = defaultColdHydrateBackgroundBudget

// errColdHydrateTimeout is returned when the first hydrate segment cannot be produced within
// coldHydrateTimeout (the cold pull then fails honestly instead of serving an empty shell).
var errColdHydrateTimeout = errors.New("projection cold-hydrate timed out")

// coldHydrateFlight coalesces concurrent first pulls for the same session and tracks the
// segmented cold-hydrate lifecycle (design §10.5.6 scheme A). partialReady is closed once the
// reducer holds a turn with real user/assistant content after a segment — the point at which a
// cold pull returns a non-empty partial without waiting for the full scan (or, for an honest
// empty session, once the scan completes with zero content turns). done is closed when the
// background scan goroutine exits. On a terminal outcome the goroutine closes partialReady with
// a recorded error so a waiter never blocks forever.
type coldHydrateFlight struct {
	partialReady chan struct{}
	done         chan struct{}
	partialOnce  sync.Once
	finish       sync.Once

	mu         sync.Mutex
	partialErr error // error at the partialReady point (nil = partial ok to serve)
	finalN     int   // events applied when the goroutine exits
	finalErr   error // terminal error after the background scan completes
}

func newColdHydrateFlight() *coldHydrateFlight {
	return &coldHydrateFlight{
		partialReady: make(chan struct{}),
		done:         make(chan struct{}),
	}
}

// signalPartial marks the flight partial-ready (idempotent). err is recorded so a waiter unblocks
// with the failure instead of hanging when the goroutine could not produce a partial.
func (f *coldHydrateFlight) signalPartial(err error) {
	if f == nil {
		return
	}
	f.partialOnce.Do(func() {
		if err != nil {
			f.mu.Lock()
			f.partialErr = err
			f.mu.Unlock()
		}
		close(f.partialReady)
	})
}

func (f *coldHydrateFlight) partialError() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.partialErr
}

// finishFlight marks the background scan as exited; also unblocks any partialReady waiter that
// never observed a partial. Idempotent.
func (f *coldHydrateFlight) finishFlight(n int, err error) {
	if f == nil {
		return
	}
	f.finish.Do(func() {
		f.mu.Lock()
		f.finalN = n
		f.finalErr = err
		f.mu.Unlock()
		f.partialOnce.Do(func() { close(f.partialReady) })
		close(f.done)
	})
}

func (f *coldHydrateFlight) finalOutcome() (int, error) {
	if f == nil {
		return 0, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finalN, f.finalErr
}

// coldHydrateTestHook, when non-nil, runs once at the start of the hydrate goroutine with the
// first-segment ctx. Tests block until cancel (hard-timeout path) or inject bounded delay.
var coldHydrateTestHook func(context.Context)

// coldHydrateSegmentTestHook, when non-nil, runs after each turn-bounded segment (task_complete)
// is applied to the reducer. Tests use it to observe incremental reducer state mid-scan, proving
// earlier turns enter the reducer before the full scan completes (design §10.5.6 scheme A).
var coldHydrateSegmentTestHook func(ctx context.Context, segmentIdx, contentTurns int)

// handleGetSessionProjection returns the authoritative SessionProjection for a session, read
// from the ProjectionReducer's in-memory state — the SAME state push reads (design §6.4 rule 4:
// pull == push state). It is NOT a wrapper around get_session_messages: it returns projection
// turns, never raw history bodies for the client to merge.
//
// The reducer is fed by the active Codex file-relay. After Mac restart the in-memory projection
// is empty even when the rollout on disk is complete (file-relay starts at EOF). Cold pulls
// therefore hydrate from disk in turn-bounded segments (design §10.5.6 scheme A): the FIRST
// segment that yields a content turn is served as a non-empty partial WITHOUT waiting for the
// full scan, and the remaining segments stream to subscribed clients as projection_patch deltas
// from a background goroutine. An honest empty session (no rollout) still returns an empty
// projection at head 0.
//
// If hydrate cannot produce even one content turn within coldHydrateTimeout (default 30s — a
// worst-case backstop for a stuck/corrupt rollout), the RPC returns an explicit error, never an
// empty success shell.
func (h *Handlers) handleGetSessionProjection(conn Connection, msg WireMessage, agent core.Agent) {
	var params GetSessionProjectionParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	params.SessionID = h.resolveSessionIDForActiveSession(params.SessionID)
	slog.Info("go-bridge: get_session_projection", "backendID", msg.BackendID, "sessionID", params.SessionID, "sinceRev", params.SinceRev)

	// Subscribe so the conn receives subsequent projection_patch push frames (WP5 emission).
	h.subscribeConnToSession(conn, msg, params.SessionID)

	if msg.BackendID == "codex" {
		if err := h.ensureCodexProjectionHydrated(msg.BackendID, params.SessionID); err != nil {
			code := "projection.hydrate_failed"
			if errors.Is(err, errColdHydrateTimeout) {
				code = "projection.hydrate_timeout"
			}
			conn.SendResult(msg.RequestID, nil, &WireError{
				Code:    code,
				Message: err.Error(),
			})
			return
		}
	}

	headRev := h.eventPublisher.ProjectionHeadRev(msg.BackendID, params.SessionID)

	// Cheap delta response when the client is already at head: empty patch set.
	if params.SinceRev != 0 && params.SinceRev == headRev {
		conn.SendResult(msg.RequestID, map[string]interface{}{
			"patches": []ProjectionPatch{},
			"headRev": headRev,
		}, nil)
		return
	}

	proj, ok := h.eventPublisher.ProjectionSnapshot(msg.BackendID, params.SessionID)
	if !ok {
		// Hydrate completed (or was not needed) and still no reducer state — honest empty
		// session (no rollout / brand-new). This is NOT the forbidden "timeout, serve empty
		// while hydrate continues" path (design §10.5).
		proj = SessionProjection{
			SessionID: params.SessionID,
			SyncRev:   0,
			Execution: ExecutionView{Phase: "idle"},
			Turns:     []TurnProjection{},
		}
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"projection": proj}, nil)
}

// ensureCodexProjectionHydrated blocks until the session reducer holds a non-empty partial
// projection (a turn with real user/assistant content) that get_session_projection may serve,
// or until the first-segment budget elapses (design §10.5.6 scheme A). On success it returns
// nil EVEN IF the full disk scan is still running in the background — the caller snapshots the
// reducer (a real partial), and remaining turns stream to subscribed clients as projection_patch
// deltas. On a stuck rollout that cannot produce even one content turn within coldHydrateTimeout
// it returns errColdHydrateTimeout so the RPC fails honestly instead of serving an empty shell.
//
// Concurrent cold pulls share a single flight; joiners wait on the leader's partialReady.
func (h *Handlers) ensureCodexProjectionHydrated(backendID, sessionID string) error {
	if h == nil || h.eventPublisher == nil || sessionID == "" {
		return nil
	}
	// Fast path: reducer already holds a turn (prior hydrate done, or a partial from an
	// in-flight background scan). Serve immediately without touching the flight.
	if h.eventPublisher.ProjectionTurnCount(backendID, sessionID) > 0 {
		return nil
	}

	h.mu.Lock()
	if h.coldHydrateFlights == nil {
		h.coldHydrateFlights = make(map[string]*coldHydrateFlight)
	}
	// Re-check under the lock: a background scan may have just produced a turn.
	if h.eventPublisher.ProjectionTurnCount(backendID, sessionID) > 0 {
		h.mu.Unlock()
		return nil
	}
	key := backendID + "\x00" + sessionID
	flight, exists := h.coldHydrateFlights[key]
	if !exists {
		flight = newColdHydrateFlight()
		h.coldHydrateFlights[key] = flight
	}
	h.mu.Unlock()

	if exists {
		return h.joinColdHydrateFlight(backendID, sessionID, flight)
	}
	return h.leadColdHydrateFlight(backendID, sessionID, key, flight)
}

// joinColdHydrateFlight waits for a leader's partial (or terminal outcome) without starting a
// second scan. If a content turn has landed by the time it unblocks, it serves that partial.
func (h *Handlers) joinColdHydrateFlight(backendID, sessionID string, flight *coldHydrateFlight) error {
	start := time.Now()
	select {
	case <-flight.partialReady:
		if err := flight.partialError(); err != nil {
			// Leader could not produce a partial; fall back to whatever turns landed since.
			if h.eventPublisher.ProjectionHasContentTurn(backendID, sessionID) {
				return nil
			}
			return err
		}
		return nil
	case <-flight.done:
		// Background scan exited before this caller observed a partial.
		if h.eventPublisher.ProjectionHasContentTurn(backendID, sessionID) {
			return nil
		}
		if _, err := flight.finalOutcome(); err != nil {
			return err
		}
		return fmt.Errorf("%w after %s (joined)", errColdHydrateTimeout, time.Since(start))
	case <-time.After(coldHydrateTimeout):
		if h.eventPublisher.ProjectionHasContentTurn(backendID, sessionID) {
			return nil
		}
		return fmt.Errorf("%w after %s (joined)", errColdHydrateTimeout, time.Since(start))
	}
}

// leadColdHydrateFlight starts the background segmented hydrate goroutine and waits for the
// first non-empty partial within the cold-hydrate budget. The goroutine continues after this
// returns so remaining turns stream as projection_patch deltas. On first-segment timeout it
// cancels the goroutine, frees the single-flight slot, and returns errColdHydrateTimeout.
func (h *Handlers) leadColdHydrateFlight(backendID, sessionID, key string, flight *coldHydrateFlight) error {
	partialBudget := coldHydrateTimeout
	if partialBudget <= 0 {
		partialBudget = defaultColdHydrateTimeout
	}
	bgBudget := coldHydrateBackgroundBudget
	if bgBudget <= 0 {
		bgBudget = defaultColdHydrateBackgroundBudget
	}
	partialCtx, partialCancel := context.WithTimeout(context.Background(), partialBudget)
	bgCtx, bgCancel := context.WithTimeout(context.Background(), bgBudget)

	go func() {
		produce := func(ctx context.Context, emit func(projectionHydrateEvent) bool) error {
			return h.produceCodexHydrateEvents(ctx, backendID, sessionID, emit)
		}
		n, err := h.hydrateProjectionFromSource(partialCtx, bgCtx, backendID, sessionID, flight, produce)
		bgCancel()
		partialCancel()
		// Free the single-flight slot BEFORE unblocking waiters so a retry that observed the
		// timeout sees an empty map (no stranded waiter for a dead flight).
		h.clearColdHydrateFlight(key)
		flight.finishFlight(n, err)
	}()

	start := time.Now()
	select {
	case <-flight.partialReady:
		// Do NOT cancel bgCtx — the background scan continues to stream remaining turns.
		partialCancel()
		if err := flight.partialError(); err != nil && !h.eventPublisher.ProjectionHasContentTurn(backendID, sessionID) {
			return err
		}
		slog.Info("go-bridge: get_session_projection cold-hydrate partial served",
			"sessionID", sessionID,
			"turns", h.eventPublisher.ProjectionTurnCount(backendID, sessionID),
			"headRev", h.eventPublisher.ProjectionHeadRev(backendID, sessionID),
			"elapsed", time.Since(start).String(),
		)
		return nil
	case <-partialCtx.Done():
		// First segment did not land within budget. Stop the background scan and free the slot.
		bgCancel()
		partialCancel()
		<-flight.done
		if h.eventPublisher.ProjectionHasContentTurn(backendID, sessionID) {
			// A content turn landed in the race window — treat as a real partial.
			return nil
		}
		err := fmt.Errorf("%w after %s: %v", errColdHydrateTimeout, partialBudget, partialCtx.Err())
		slog.Warn("go-bridge: get_session_projection cold-hydrate first-segment timeout",
			"sessionID", sessionID,
			"turns", h.eventPublisher.ProjectionTurnCount(backendID, sessionID),
			"headRev", h.eventPublisher.ProjectionHeadRev(backendID, sessionID),
			"elapsed", time.Since(start).String(),
			"error", err.Error(),
		)
		return err
	}
}

func (h *Handlers) clearColdHydrateFlight(key string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.coldHydrateFlights != nil {
		delete(h.coldHydrateFlights, key)
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

// hydrateProjectionFromSource runs the generic segmented cold-hydrate loop over a backend-specific
// event producer (design §10.5.6 scheme A + §10.5.7 修法 1). The producer streams
// projectionHydrateEvents in source order; this loop publishes them, signals a non-empty partial
// once the reducer holds a turn with real user/assistant content, and settles execution to idle.
//
// Phase-1 events (before the first content turn) publish Offline:true (reducer-only, no mid-pull
// patch fan-out); once a non-empty partial is signalable, subsequent events flip to Offline:false
// (Broadcast:true) so they reach subscribed clients as projection_patch deltas over the served
// partial. partialCtx bounds the first-segment wait (coldHydrateTimeout); bgCtx bounds background
// completion (coldHydrateBackgroundBudget). The producer receives bgCtx and should honor ctx cancel
// between events. Returns total events applied + terminal error.
func (h *Handlers) hydrateProjectionFromSource(partialCtx, bgCtx context.Context, backendID, sessionID string, flight *coldHydrateFlight, produce func(ctx context.Context, emit func(projectionHydrateEvent) bool) error) (int, error) {
	if partialCtx == nil {
		partialCtx = context.Background()
	}
	if bgCtx == nil {
		bgCtx = partialCtx
	}
	if coldHydrateTestHook != nil {
		coldHydrateTestHook(partialCtx)
		if err := partialCtx.Err(); err != nil {
			flight.signalPartial(err)
			return 0, err
		}
	}
	if h == nil || h.eventPublisher == nil || sessionID == "" {
		flight.signalPartial(nil)
		return 0, nil
	}
	if err := partialCtx.Err(); err != nil {
		flight.signalPartial(err)
		return 0, err
	}

	applied := 0
	segmentIdx := 0
	partialSignaled := false

	emit := func(ev projectionHydrateEvent) bool {
		if err := bgCtx.Err(); err != nil {
			return false
		}
		h.publishEvent(LogicalEvent{
			SessionID: sessionID,
			BackendID: backendID,
			Event:     ev.Event,
			Data:      ev.Data,
			Broadcast: partialSignaled, // phase 2 fans patches out to subscribers
			Offline:   !partialSignaled,
		})
		applied++

		// Once the reducer holds a content turn, the partial is honest — signal the leader once.
		if !partialSignaled && h.eventPublisher.ProjectionHasContentTurn(backendID, sessionID) {
			partialSignaled = true
			flight.signalPartial(nil)
		}
		// Segment boundary (completed turn) — test hook observes incremental reducer state.
		if ev.TurnDone {
			segmentIdx++
			if coldHydrateSegmentTestHook != nil {
				coldHydrateSegmentTestHook(bgCtx, segmentIdx, h.eventPublisher.ProjectionTurnCount(backendID, sessionID))
			}
		}
		return true
	}

	produceErr := produce(bgCtx, emit)

	if !partialSignaled {
		// No content turn was ever served. Distinguish a phase-1 timeout/cancel from an honest
		// empty session: partialCtx.Err() is set when the leader's first-segment budget elapsed
		// (or the leader bgCanceled on timeout); nil partialCtx + nil produceErr means the source
		// simply produced zero content turns (brand-new / empty rollout / no transcript) — honest empty.
		cause := partialCtx.Err()
		if cause == nil {
			cause = produceErr
		}
		if cause != nil {
			flight.signalPartial(cause)
			return applied, cause
		}
		flight.signalPartial(nil) // honest empty session — RPC serves the empty idle branch
	} else if applied > 0 && bgCtx.Err() == nil {
		// Partial was served; settle execution to idle (fans out in background phase).
		h.publishEvent(LogicalEvent{
			SessionID: sessionID,
			BackendID: backendID,
			Event:     "session_state_changed",
			Data:      map[string]interface{}{"state": "idle"},
			Broadcast: true,
			Offline:   false,
		})
	}

	// Terminal outcome for finishFlight (informational — the leader already returned on partial).
	var terminal error
	if produceErr != nil {
		terminal = produceErr
	} else if err := bgCtx.Err(); err != nil {
		terminal = err
	}
	return applied, terminal
}

// produceCodexHydrateEvents is the codex cold-hydrate producer: locate the rollout JSONL via the
// codex agent's TranscriptLocator and stream it through the codex parser + event mapper. Returns
// nil (honest empty) when there is no codex agent / transcript; a read/scan error otherwise.
func (h *Handlers) produceCodexHydrateEvents(ctx context.Context, backendID, sessionID string, emit func(projectionHydrateEvent) bool) error {
	agent, ok := h.getFirstAgentByName("codex")
	if !ok {
		return nil // no codex agent → honest empty session, not a hydrate failure
	}
	locator, ok := agent.(core.TranscriptLocator)
	if !ok {
		return nil
	}
	sessPath, err := locator.TranscriptPath(ctx, sessionID)
	if err != nil || strings.TrimSpace(sessPath) == "" {
		return nil
	}
	currentTurnID := ""
	toolNames := make(map[string]string)
	return streamCodexTranscriptRelayEvents(ctx, sessPath, 0, func(ev codexRelayEvent) bool {
		eventName, data, ok := codexRelayEventToProjectionEvent(ev, &currentTurnID, toolNames)
		if !ok {
			return true // unhandled kind (e.g. context_usage) — skip, keep scanning
		}
		return emit(projectionHydrateEvent{Event: eventName, Data: data, TurnDone: ev.kind == "task_complete"})
	})
}

// produceClaudeHydrateEvents is the claude cold-hydrate producer: locate the session .jsonl via
// the claudecode agent's TranscriptLocator and stream it through the claude parser. Returns nil
// (honest empty) when there is no claude agent / transcript; a read/scan error otherwise.
// (design §10.5.7 修法 1 — claude joins the projection reducer.)
func (h *Handlers) produceClaudeHydrateEvents(ctx context.Context, backendID, sessionID string, emit func(projectionHydrateEvent) bool) error {
	agent, ok := h.getFirstAgentByName("claudecode")
	if !ok {
		return nil
	}
	locator, ok := agent.(core.TranscriptLocator)
	if !ok {
		return nil
	}
	sessPath, err := locator.TranscriptPath(ctx, sessionID)
	if err != nil || strings.TrimSpace(sessPath) == "" {
		return nil
	}
	return streamClaudeTranscriptProjectionEvents(ctx, sessPath, emit)
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
		return "text_delta", map[string]interface{}{"itemId": *currentTurnID, "delta": ev.text}, true
	case "reasoning":
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
		data := map[string]interface{}{"toolResult": ev.toolResult, "toolStatus": "completed"}
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

// ProjectionTurnCount returns the number of turns the reducer currently holds for the session
// (design §10.5.6 scheme A — the non-empty partial boundary for segmented cold-hydrate).
func (p *EventPublisher) ProjectionTurnCount(backendID, sessionID string) int {
	if p == nil || p.projection == nil {
		return 0
	}
	return p.projection.TurnCount(backendID, sessionID)
}

// ProjectionHasContentTurn reports whether the reducer holds a turn with real user/assistant
// content — the honest non-empty-partial boundary for segmented cold-hydrate (§10.5.6 scheme A).
func (p *EventPublisher) ProjectionHasContentTurn(backendID, sessionID string) bool {
	if p == nil || p.projection == nil {
		return false
	}
	return p.projection.HasContentTurn(backendID, sessionID)
}
