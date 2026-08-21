package dshweb

// §12 item 3 / §12.1-3: the seat-loss terminal producer must close every
// running session exactly once per alive→dark edge — grace entry and stream
// 1006 funnel through one transition, the edge-sequence guard makes
// double-firing structurally impossible, and a later edge re-arms.

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func newBoundSession(id string) *dshSession {
	s := &dshSession{agent: nil, events: make(chan core.Event, 4)}
	s.idValue.Store(id)
	return s
}

func TestSeatLossTerminalOncePerEdgePerRunningSession(t *testing.T) {
	r, starter, _, _ := holdSeat(t, time.Second)
	a := &Agent{resolver: r}
	r.SetLostCallback(a.handleSeatLost) // holdSeat's resolver; wire explicitly

	// Three bound sessions: running-known, idle-known, unknown.
	run := newBoundSession("s-running")
	idle := newBoundSession("s-idle")
	unknown := newBoundSession("s-unknown")
	a.bindings.put("s-running", run)
	a.bindings.put("s-idle", idle)
	a.bindings.put("s-unknown", unknown)
	a.running.setOne("s-running", true)
	a.running.setOne("s-idle", false)

	// Edge 1: instance dies, loss detected via Resolve.
	if err := starter.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background()); err == nil {
		t.Fatal("expected loss error")
	}
	a.handleSeatLost() // synchronous invocation (production rides the callback)

	// Only the running session got exactly one terminal event.
	got := drainEvents(run.events)
	if len(got) != 1 {
		t.Fatalf("running session must get exactly one terminal, got %d", len(got))
	}
	if got[0].Type != core.EventError || got[0].SessionID != "s-running" || got[0].Error == nil {
		t.Fatalf("terminal event shape wrong: %+v", got[0])
	}
	if n := len(drainEvents(idle.events)); n != 0 {
		t.Fatalf("idle session must not get a terminal, got %d", n)
	}
	if n := len(drainEvents(unknown.events)); n != 0 {
		t.Fatalf("unknown-running session must not get a terminal, got %d", n)
	}

	// Same edge again (any path re-noticing the death): no double fire.
	a.handleSeatLost()
	if n := len(drainEvents(run.events)); n != 0 {
		t.Fatalf("double-fired on same edge: %d", n)
	}

	// Edge 2 (seat recovered, then lost again): re-armed — one more terminal.
	r.mu.Lock()
	r.rebindLocked(&ResolvedInstance{BaseURL: r.seatURL(), Port: 1, Source: SourceExternal}, "test-edge2")
	r.loseSeatLocked(&ResolvedInstance{BaseURL: r.seatURL(), Port: 1, Source: SourceExternal})
	r.mu.Unlock()
	a.handleSeatLost()
	if n := len(drainEvents(run.events)); n != 1 {
		t.Fatalf("next edge must re-arm exactly one terminal, got %d", n)
	}
}

func drainEvents(ch <-chan core.Event) []core.Event {
	var out []core.Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}
