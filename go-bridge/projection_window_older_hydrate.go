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
	"log/slog"
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
	// The cold-open seed may still be inside its post-commit persist window:
	// CommitHydrateTransaction releases Done-waiters BEFORE the runner's
	// persistCodexProducerSeed hook runs, so a same-moment older walk can read
	// here between the two. The in-memory seed is the freshest page-1 fact and
	// wins over any on-disk claim from a previous epoch; it is peeked, never
	// consumed (the persist hook owns the LoadAndDelete).
	if raw, ok := h.hydrateProducerSeeds.Load(projectionDeliveryKey(backendID, sessionID)); ok {
		if seed, ok := raw.(*CodexProducerState); ok {
			return seed
		}
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
	h.producerWritesMu.Lock()
	defer h.producerWritesMu.Unlock()
	return h.saveProducerStateLocked(backendID, sessionID, state)
}

// saveProducerStateLocked is the lock-free writer; the caller holds
// producerWritesMu (persistCodexProducerSeed's guard+write critical section).
func (h *Handlers) saveProducerStateLocked(backendID, sessionID string, state CodexProducerState) error {
	if h.projectionKernel == nil {
		return nil
	}
	return h.projectionKernel.SaveCodexProducerState(backendID, sessionID, state)
}

// streamCodexRemoteSummaryProjectionEvents is the T2.1 cold-hydrate source for
// codex-remote: ONE Summary page (plan §2.1 — desc network page 1, page size =
// the agent's frozen turns/list limit 30, already reversed ascending by the
// pager). This is where includeTurns=true finally leaves the production path;
// the agent's compat full read remains for probe/baseline use only. The
// upstream cursor fact is recorded as a producer seed and persisted AFTER the
// hydrate commits (persistCodexProducerSeed) so a failed hydrate never leaves a
// stale "older upstream" claim behind.
func (h *Handlers) streamCodexRemoteSummaryProjectionEvents(
	ctx context.Context,
	pager core.UpstreamHistoryPager,
	backendID, sessionID string,
	emit func(projectionHydrateEvent) bool,
) error {
	page, err := pager.ReadUpstreamHistoryPage(ctx, sessionID, "")
	if err != nil {
		return err
	}
	for _, ev := range turnScopedHistoryTurnToProjectionEvents(page.Turns) {
		if !emit(ev) {
			return nil
		}
	}
	seed := CodexProducerState{
		HasOlderUpstream:   page.NextCursor != "",
		UpstreamNextCursor: page.NextCursor,
		UpdatedAt:          time.Now().UTC(),
	}
	// Boundary = the oldest MAPPED turn (the kernel front after commit).
	for _, turn := range page.Turns {
		if turn.TurnID != "" {
			seed.BoundaryTurnID = turn.TurnID
			break
		}
	}
	if seed.HasOlderUpstream && seed.BoundaryTurnID == "" {
		// Pathological page (cursor claims more upstream but nothing mappable
		// landed): an empty kernel cannot serve older walks anyway — record EOF
		// rather than an invalid boundary-less claim.
		seed.HasOlderUpstream = false
		seed.UpstreamNextCursor = ""
	}
	h.hydrateProducerSeeds.Store(projectionDeliveryKey(backendID, sessionID), &seed)
	return nil
}

// persistCodexProducerSeed runs AFTER a successful codex-remote hydrate commit:
// it installs the page-1 producer fact, anchored at the committed kernel front
// (re-asserted — the commit gate may have merged live appends at the tail, but
// the front must still be the seeded page's oldest turn).
func (h *Handlers) persistCodexProducerSeed(backendID, sessionID string, committed SessionProjection) {
	key := projectionDeliveryKey(backendID, sessionID)
	raw, ok := h.hydrateProducerSeeds.Load(key)
	if !ok {
		return
	}
	seed := *raw.(*CodexProducerState)
	// Lost-update guard: an older walk (or its stale recovery) may have advanced
	// the persisted fact while this seed sat in its post-commit persist window —
	// those saves all stamp UpdatedAt=now, i.e. strictly after the seed. The
	// newer persisted state wins; the seed is consumed either way.
	h.producerWritesMu.Lock()
	defer h.producerWritesMu.Unlock()
	if disk, derr := h.projectionKernel.LoadCodexProducerState(backendID, sessionID); derr == nil && disk != nil {
		if disk.UpdatedAt.After(seed.UpdatedAt) {
			h.hydrateProducerSeeds.CompareAndDelete(key, raw)
			return
		}
	}
	if len(committed.Turns) > 0 {
		if seed.BoundaryTurnID != "" && committed.Turns[0].TurnID != seed.BoundaryTurnID {
			// Commit reordered/pruned the front (unexpected): anchor at the real
			// front only when the seeded boundary survived; otherwise drop the
			// claim — the older walk re-derives honestly on demand.
			found := false
			for _, turn := range committed.Turns {
				if turn.TurnID == seed.BoundaryTurnID {
					found = true
					break
				}
			}
			if !found {
				h.hydrateProducerSeeds.CompareAndDelete(key, raw)
				return
			}
		}
		seed.BoundaryTurnID = committed.Turns[0].TurnID
	} else if seed.HasOlderUpstream {
		h.hydrateProducerSeeds.CompareAndDelete(key, raw)
		return
	}
	if err := h.saveProducerStateLocked(backendID, sessionID, seed); err != nil {
		slog.Warn("go-bridge: codex producer seed persist failed",
			"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID), "error", err)
	}
	// Consume only AFTER the disk write: readers peek the map before falling
	// back to disk (loadProducerState), so write-then-delete keeps the fact
	// continuously visible across the Done-release/persist window. CompareAndDelete
	// leaves a newer hydrate's seed intact if one raced in.
	h.hydrateProducerSeeds.CompareAndDelete(key, raw)
}
