package gobridge

// T2.0 producer-on-demand hydration for the projection window `older` walk
// (lazy-history §2.4 / bridge-v1.md R11a/R11b/R11d, frozen 2026-08-30).
//
// One bounded upstream page per older request: when the walk reaches the kernel
// front and the backend-private producer checkpoint still holds an unexhausted
// upstream cursor, the page is reduced (already ascending, inclusive-dedup
// asserted by the reducer) into the SAME kernel truth as one revision bump, and
// per-connection frames are routed by delivery mode (no-op revision patch /
// sync_invalidate; the requester sees the page inside its window result).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var (
	// ErrUpstreamCursorStale is the R11d typed recovery error: a page fetched
	// with the persisted internal cursor overlaps already-known turns (upstream
	// data changed under us). The client discards its cursor chain (cursor_stale)
	// and the producer state falls back to a re-walk from the upstream head.
	ErrUpstreamCursorStale = errors.New("projection: upstream cursor stale")
)

type olderHydrateFlight struct {
	done    chan struct{}
	hasMore bool
	err     error
}

// upstreamSummaryTurnsToProjection maps ONE summary page (ascending) to kernel
// turns by REUSING the cold-baseline mapper (turnScopedHistoryTurnToProjectionEvents)
// through a scratch reducer: identity stays fully official (turnId=turn.id,
// itemId=item.id) and part semantics stay identical between the cold baseline and
// prepended pages — one mapper, one identity discipline. Summary turns carry no
// detail, so they hydrate with detailLoadState=notRequested (zero value); detail
// parts arrive later via session_turn_items (T2.2).
func upstreamSummaryTurnsToProjection(turns []core.TurnScopedHistoryTurn) []TurnProjection {
	events := turnScopedHistoryTurnToProjectionEvents(turns)
	if len(events) == 0 {
		return nil
	}
	scratch := NewProjectionReducer()
	for i, ev := range events {
		// projectionReducerEvent, never a direct EventMessage literal: the
		// no-production-bypass guard distinguishes isolated reducer transactions
		// from business-event egress by this constructor (see projection_kernel.go).
		scratch.Apply(projectionReducerEvent("scratch", "scratch", ev.Event, ev.Data, i+1, ""))
	}
	proj, ok := scratch.Snapshot("scratch", "scratch")
	if !ok {
		return nil
	}
	return proj.Turns
}

// producerHasOlderUpstreamFact reports the backend-private producer fact for
// window_0/latest honesty (R11d): true only when a persisted producer state
// claims an unexhausted upstream cursor. Until the summary-consumption hydrate
// (T2.1) seeds the state at cold open, this stays false and window behavior is
// exactly today's.
func (h *Handlers) producerHasOlderUpstreamFact(backendID, sessionID string) bool {
	state := h.loadProducerState(backendID, sessionID)
	return state != nil && state.HasOlderUpstream
}

func (h *Handlers) loadProducerState(backendID, sessionID string) *CodexProducerState {
	if h == nil || h.projectionKernel == nil {
		return nil
	}
	state, err := h.projectionKernel.LoadCodexProducerState(backendID, sessionID)
	if err != nil || state == nil {
		return nil
	}
	return state
}

// hydrateOlderFromUpstream runs the bounded one-page producer hydration for an
// `older` request whose cursor anchors at the kernel front. Returns the honest
// hasOlderUpstream fact AFTER hydration. Concurrent older walks on the same
// session share one flight (singleflight): followers observe the leader's
// result and re-slice at the new head instead of fetching a second page.
func (h *Handlers) hydrateOlderFromUpstream(
	conn Connection,
	backendID, sessionID, cursor string,
	agent core.Agent,
) (bool, error) {
	proj, ok := h.projectionKernel.Snapshot(backendID, sessionID)
	if !ok || len(proj.Turns) == 0 {
		return h.producerHasOlderUpstreamFact(backendID, sessionID), nil
	}
	decoded, err := decodeProjectionWindowCursor(cursor)
	if err != nil {
		return false, err
	}
	if indexOfTurn(proj.Turns, decoded.AnchorTurnID) != 0 {
		// Anchor is not the kernel front: the slice serves from committed turns;
		// no upstream fetch on this request (R11a — one page only when needed).
		return h.producerHasOlderUpstreamFact(backendID, sessionID), nil
	}

	if agent == nil {
		agent, _ = h.getFirstAgentByName(backendID)
	}

	key := backendID + "|" + sessionID
	candidate := &olderHydrateFlight{done: make(chan struct{})}
	for {
		actual, loaded := h.olderHydrateFlights.LoadOrStore(key, candidate)
		flight := actual.(*olderHydrateFlight)
		if loaded {
			<-flight.done
			if flight.err != nil {
				return false, flight.err
			}
			// The leader's page may not reach this follower's anchor; loop so the
			// follower re-evaluates at the new head (bounded by upstream EOF).
			if fresh, ok2 := h.projectionKernel.Snapshot(backendID, sessionID); ok2 {
				proj = fresh
			}
			if indexOfTurn(proj.Turns, decoded.AnchorTurnID) != 0 {
				return h.producerHasOlderUpstreamFact(backendID, sessionID), nil
			}
			continue
		}
		hasMore, err := h.runOlderHydrationLocked(conn, backendID, sessionID, agent)
		flight.hasMore, flight.err = hasMore, err
		close(flight.done)
		h.olderHydrateFlights.Delete(key)
		return hasMore, err
	}
}

// runOlderHydrationLocked is the leader path: load producer state → fetch ONE
// page via the internal upstream cursor → validate (R11d) → prepend → persist →
// route per-connection frames.
func (h *Handlers) runOlderHydrationLocked(
	requester Connection,
	backendID, sessionID string,
	agent core.Agent,
) (bool, error) {
	state := h.loadProducerState(backendID, sessionID)
	if state == nil || !state.HasOlderUpstream {
		return false, nil
	}
	pager, ok := agent.(core.UpstreamHistoryPager)
	if !ok {
		return false, fmt.Errorf("projection: backend %s has no upstream history pager", backendID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	page, err := pager.ReadUpstreamHistoryPage(ctx, sessionID, state.UpstreamNextCursor)
	if err != nil {
		return false, err
	}
	proj, ok := h.projectionKernel.Snapshot(backendID, sessionID)
	if !ok {
		return false, errors.New("projection: kernel snapshot vanished during older hydration")
	}
	known := make(map[string]struct{}, len(proj.Turns))
	for i := range proj.Turns {
		known[proj.Turns[i].TurnID] = struct{}{}
	}
	for _, turn := range page.Turns {
		if _, dup := known[turn.TurnID]; dup {
			// R11d stale cursor: the page overlaps known turns — never overwrite
			// newer truth. Fall the producer state back to a head re-walk and
			// surface typed staleness so the client discards its cursor chain.
			boundary := ""
			if len(proj.Turns) > 0 {
				boundary = proj.Turns[0].TurnID
			}
			_ = h.saveProducerState(backendID, sessionID, CodexProducerState{
				HasOlderUpstream:   true,
				UpstreamNextCursor: "",
				BoundaryTurnID:     boundary,
				UpdatedAt:          time.Now().UTC(),
			})
			return false, fmt.Errorf("%w: page overlapped known turn %s", ErrUpstreamCursorStale, turn.TurnID)
		}
	}
	turns := upstreamSummaryTurnsToProjection(page.Turns)
	if len(turns) == 0 {
		// Page held no completable turns (e.g. only an in-flight turn upstream):
		// consume the cursor without a commit and keep the fact honest.
		return h.saveProducerFact(backendID, sessionID, page.NextCursor)
	}
	committed, err := h.projectionKernel.PrependHistoricalTurns(backendID, sessionID, turns)
	if err != nil {
		return false, err
	}
	next := CodexProducerState{
		HasOlderUpstream:   page.NextCursor != "",
		UpstreamNextCursor: page.NextCursor,
		UpdatedAt:          time.Now().UTC(),
	}
	if len(committed.Turns) > 0 {
		next.BoundaryTurnID = committed.Turns[0].TurnID
	}
	if err := h.saveProducerState(backendID, sessionID, next); err != nil {
		return false, err
	}
	h.eventPublisher.PublishProjectionPrepend(backendID, sessionID, committed.SyncRev, requester)
	return page.NextCursor != "", nil
}

func (h *Handlers) saveProducerFact(backendID, sessionID, nextCursor string) (bool, error) {
	err := h.saveProducerState(backendID, sessionID, CodexProducerState{
		HasOlderUpstream:   nextCursor != "",
		UpstreamNextCursor: nextCursor,
		UpdatedAt:          time.Now().UTC(),
	})
	return nextCursor != "", err
}

func (h *Handlers) saveProducerState(backendID, sessionID string, state CodexProducerState) error {
	if h.projectionKernel == nil {
		return nil
	}
	return h.projectionKernel.SaveCodexProducerState(backendID, sessionID, state)
}
