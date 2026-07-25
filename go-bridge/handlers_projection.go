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

// defaultColdHydrateTimeout is the far-horizon budget for disk→reducer hydrate on a cold
// pull (design §10.5 ring 1 close-out). Normal huge rollouts finish in hundreds of ms; this
// bound exists so a stuck/corrupt rollout cannot permanently occupy a single-flight slot or
// leave the RPC hanging. Distinct from the removed 750ms "serve empty head" path: on expiry
// we return an explicit RPC error, never an empty success shell.
const defaultColdHydrateTimeout = 30 * time.Second

// coldHydrateTimeout is the live budget. Tests may lower it; production stays at the default.
var coldHydrateTimeout = defaultColdHydrateTimeout

// errColdHydrateTimeout is returned when hydrate does not finish within coldHydrateTimeout.
var errColdHydrateTimeout = errors.New("projection cold-hydrate timed out")

// coldHydrateFlight coalesces concurrent first pulls for the same session so disk→reducer
// hydrate runs once. Design §10.5 ring 1: pull waits for hydrate completion; never races a
// short timeout against an in-flight scan and serves an empty head-0 shell.
type coldHydrateFlight struct {
	done   chan struct{}
	finish sync.Once
	n      int
	err    error
}

func (f *coldHydrateFlight) complete(n int, err error) {
	if f == nil {
		return
	}
	f.finish.Do(func() {
		f.n = n
		f.err = err
		close(f.done)
	})
}

// coldHydrateTestHook, when non-nil, runs at the start of hydrateCodexProjectionFromDisk.
// It receives the hydrate context so tests can block until cancel (hard-timeout path) or
// inject bounded delay (still-within-budget wait path).
var coldHydrateTestHook func(context.Context)

// handleGetSessionProjection returns the authoritative SessionProjection for a session, read
// from the ProjectionReducer's in-memory state — the SAME state push reads (design §6.4 rule 4:
// pull == push state). It is NOT a wrapper around get_session_messages: it returns projection
// turns, never raw history bodies for the client to merge.
//
// The reducer is fed by the active Codex file-relay. After Mac restart the in-memory projection
// is empty even when the rollout on disk is complete (file-relay starts at EOF). Cold pulls
// therefore one-shot hydrate from disk **and wait until hydrate finishes** before answering
// (design §5.3 hydrate-on-cold-start; §10.5 ring 1: never serve empty head-0 while hydrate is
// incomplete). Clients already carry an 8s pull cap; a multi-second reduce for a huge rollout
// is acceptable and far cheaper than concurrent full-history fallback.
//
// If hydrate cannot finish within coldHydrateTimeout (default 30s), the RPC returns an explicit
// error — not an empty projection — and the single-flight slot is released so a later pull can
// retry.
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

// ensureCodexProjectionHydrated blocks until the session has reducer state or disk hydrate has
// finished once (success, honest empty, or hard-timeout error). Concurrent callers for the same
// session share a single flight. On hard timeout the flight slot is cleared so a later pull can
// retry; callers receive errColdHydrateTimeout rather than an empty success shell.
func (h *Handlers) ensureCodexProjectionHydrated(backendID, sessionID string) error {
	if h == nil || h.eventPublisher == nil || sessionID == "" {
		return nil
	}
	if h.eventPublisher.ProjectionHeadRev(backendID, sessionID) > 0 {
		return nil
	}

	h.mu.Lock()
	if h.coldHydrateFlights == nil {
		h.coldHydrateFlights = make(map[string]*coldHydrateFlight)
	}
	if h.eventPublisher.ProjectionHeadRev(backendID, sessionID) > 0 {
		h.mu.Unlock()
		return nil
	}
	key := backendID + "\x00" + sessionID
	flight, exists := h.coldHydrateFlights[key]
	if !exists {
		flight = &coldHydrateFlight{done: make(chan struct{})}
		h.coldHydrateFlights[key] = flight
		h.mu.Unlock()

		budget := coldHydrateTimeout
		if budget <= 0 {
			budget = defaultColdHydrateTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		start := time.Now()

		type hydrateOutcome struct {
			n   int
			err error
		}
		outCh := make(chan hydrateOutcome, 1)
		go func() {
			n, err := h.hydrateCodexProjectionFromDisk(ctx, backendID, sessionID)
			outCh <- hydrateOutcome{n: n, err: err}
		}()

		var (
			n   int
			err error
		)
		select {
		case out := <-outCh:
			n, err = out.n, out.err
			cancel()
		case <-ctx.Done():
			// Hard timeout: do not serve empty head. Release the single-flight slot so a
			// later pull can retry. The hydrate goroutine observes ctx cancel and exits
			// (hook / apply loop); cancel is not deferred so joiners can still read flight.
			err = fmt.Errorf("%w after %s: %v", errColdHydrateTimeout, budget, ctx.Err())
			n = 0
			cancel()
			// Drain outcome without blocking forever so the worker is not stranded on send
			// if it finishes shortly after timeout.
			go func() { <-outCh }()
		}

		flight.complete(n, err)
		if err != nil {
			// Free the slot on failure/timeout so the next pull is not joined into a dead flight.
			h.clearColdHydrateFlight(key)
		}

		headRev := h.eventPublisher.ProjectionHeadRev(backendID, sessionID)
		if err != nil {
			slog.Warn("go-bridge: get_session_projection cold-hydrate failed",
				"sessionID", sessionID,
				"events", n,
				"headRev", headRev,
				"elapsed", time.Since(start).String(),
				"error", err.Error(),
			)
		} else {
			slog.Info("go-bridge: get_session_projection cold-hydrate",
				"sessionID", sessionID,
				"events", n,
				"headRev", headRev,
				"elapsed", time.Since(start).String(),
				"waited", true,
			)
		}
		return err
	}
	h.mu.Unlock()

	<-flight.done
	slog.Info("go-bridge: get_session_projection cold-hydrate joined in-flight",
		"sessionID", sessionID,
		"events", flight.n,
		"headRev", h.eventPublisher.ProjectionHeadRev(backendID, sessionID),
		"error", errorString(flight.err),
	)
	return flight.err
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

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// hydrateCodexProjectionFromDisk one-shot feeds the rollout JSONL into ProjectionReducer
// so get_session_projection can answer after Mac restart without waiting for a new turn.
// Events are published offline (no live delivery requirement); Apply still runs.
// Callers must wait for this to return before serving a cold pull (design §10.5).
// ctx cancel aborts between scan/apply steps so a hard-timeout does not leave work running
// indefinitely after the RPC has already failed.
func (h *Handlers) hydrateCodexProjectionFromDisk(ctx context.Context, backendID, sessionID string) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if coldHydrateTestHook != nil {
		coldHydrateTestHook(ctx)
		if err := ctx.Err(); err != nil {
			return 0, err
		}
	}
	if h == nil || h.eventPublisher == nil || sessionID == "" {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	agent, ok := h.getFirstAgentByName("codex")
	if !ok {
		return 0, nil
	}
	locator, ok := agent.(core.TranscriptLocator)
	if !ok {
		return 0, nil
	}
	sessPath, err := locator.TranscriptPath(ctx, sessionID)
	if err != nil || strings.TrimSpace(sessPath) == "" {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	// Scan can be large; run it so ctx cancel can abandon waiting for the result. The scanner
	// itself is not mid-cancelable without a full rewrite; abandoning the wait prevents the
	// RPC path from blocking, and the scan goroutine exits when the file read finishes.
	type scanOut struct {
		events []codexRelayEvent
	}
	scanCh := make(chan scanOut, 1)
	go func() {
		scanCh <- scanOut{events: scanCodexTranscriptRelayEvents(sessPath, 0)}
	}()
	var events []codexRelayEvent
	select {
	case out := <-scanCh:
		events = out.events
	case <-ctx.Done():
		go func() { <-scanCh }()
		return 0, ctx.Err()
	}
	if len(events) == 0 {
		return 0, nil
	}

	currentTurnID := ""
	toolNames := make(map[string]string)
	applied := 0
	for _, ev := range events {
		if err := ctx.Err(); err != nil {
			return applied, err
		}
		var eventName string
		var data map[string]interface{}
		switch ev.kind {
		case "task_started":
			currentTurnID = ev.turnID
			eventName = "turn_started"
			data = map[string]interface{}{"turnId": currentTurnID}
		case "task_complete":
			tid := ev.turnID
			if tid == "" {
				tid = currentTurnID
			}
			eventName = "turn_completed"
			data = map[string]interface{}{"turnId": tid, "done": true, "reason": "task_complete"}
			currentTurnID = ""
		case "text":
			eventName = "text_delta"
			data = map[string]interface{}{"itemId": currentTurnID, "delta": ev.text}
		case "reasoning":
			eventName = "reasoning_delta"
			data = map[string]interface{}{"itemId": currentTurnID, "delta": ev.text}
		case "user_message":
			eventName = "user_message"
			data = map[string]interface{}{"itemId": ev.itemId, "turnId": currentTurnID, "text": ev.text}
		case "tool_started":
			if ev.itemId != "" {
				toolNames[ev.itemId] = ev.toolName
			}
			eventName = "tool_started"
			data = map[string]interface{}{"toolName": ev.toolName, "toolInput": ev.toolInput}
			if ev.itemId != "" {
				data["itemId"] = ev.itemId
			}
		case "tool_finished":
			eventName = "tool_finished"
			data = map[string]interface{}{"toolResult": ev.toolResult, "toolStatus": "completed"}
			if name, ok := toolNames[ev.itemId]; ok && name != "" {
				data["toolName"] = name
			}
			if ev.itemId != "" {
				data["itemId"] = ev.itemId
			}
		default:
			continue
		}
		h.publishEvent(LogicalEvent{
			SessionID: sessionID,
			BackendID: backendID,
			Event:     eventName,
			Data:      data,
			Broadcast: false, // hydrate only — do not fan out mid-pull
			Offline:   true,
		})
		applied++
	}
	if err := ctx.Err(); err != nil {
		return applied, err
	}
	// Completed transcript should settle execution to idle.
	h.publishEvent(LogicalEvent{
		SessionID: sessionID,
		BackendID: backendID,
		Event:     "session_state_changed",
		Data:      map[string]interface{}{"state": "idle"},
		Broadcast: false,
		Offline:   true,
	})
	return applied, nil
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
