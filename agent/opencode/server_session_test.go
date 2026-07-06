package opencode

import (
	"context"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func drain(ch <-chan core.Event) []core.Event {
	var got []core.Event
	for {
		select {
		case ev := <-ch:
			got = append(got, ev)
		default:
			return got
		}
	}
}

// TestSSESubscriberSessionFilter covers the active-mode filter that
// opencodeServerSession relies on: pending state drops all, a set filter
// passes only the matching session, and passive mode (filterActive=false used
// by the global passive subscriber) passes everything.
func TestSSESubscriberSessionFilter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &sseSubscriber{events: make(chan core.Event, 16), ctx: ctx}

	// Pending: filterActive=true, filter="" → must drop ALL events so a
	// not-yet-resolved active session doesn't leak other sessions' events to iOS.
	s.sessionFilter.Store("")
	s.filterActive.Store(true)
	s.emit(core.Event{Type: core.EventText, SessionID: "ses_a", Content: "x"})
	if got := drain(s.events); len(got) != 0 {
		t.Fatalf("pending filter must drop all, got %d events", len(got))
	}

	// Filter locked to ses_a: only ses_a passes, others dropped.
	s.setSessionFilter("ses_a")
	s.emit(core.Event{Type: core.EventText, SessionID: "ses_other", Content: "other"})
	s.emit(core.Event{Type: core.EventText, SessionID: "ses_a", Content: "mine"})
	s.emit(core.Event{Type: core.EventResult, SessionID: "ses_a", Done: true})
	got := drain(s.events)
	if len(got) != 2 {
		t.Fatalf("expected 2 events for ses_a, got %d", len(got))
	}
	for _, ev := range got {
		if ev.SessionID != "ses_a" {
			t.Fatalf("non-matching session leaked: %q", ev.SessionID)
		}
	}

	// Passive mode (filterActive=false): passes all (global observation).
	s.filterActive.Store(false)
	s.emit(core.Event{Type: core.EventText, SessionID: "ses_x"})
	s.emit(core.Event{Type: core.EventText, SessionID: "ses_y"})
	if got := drain(s.events); len(got) != 2 {
		t.Fatalf("passive mode should pass all, got %d", len(got))
	}
}

// TestSetSessionFilterEmptyIsNoOp ensures a "" id never overwrites a real
// filter (the pending guard in setSessionFilter).
func TestSetSessionFilterEmptyIsNoOp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &sseSubscriber{events: make(chan core.Event, 4), ctx: ctx}
	s.sessionFilter.Store("ses_real")
	s.filterActive.Store(true)
	s.setSessionFilter("") // must not clobber the real filter
	s.emit(core.Event{Type: core.EventText, SessionID: "ses_real", Content: "x"})
	if got := drain(s.events); len(got) != 1 {
		t.Fatalf("empty setSessionFilter must not clobber existing filter; got %d", len(got))
	}
}

func TestResolveOpencodeModelLocked(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		a := &Agent{}
		if p, m := resolveOpencodeModelLocked(a); p != "" || m != "" {
			t.Fatalf("expected empty, got %q/%q", p, m)
		}
	})
	t.Run("provider slash model string", func(t *testing.T) {
		a := &Agent{model: "opencode/mimo-v2.5-free"}
		p, m := resolveOpencodeModelLocked(a)
		if p != "opencode" || m != "mimo-v2.5-free" {
			t.Fatalf("expected opencode/mimo-v2.5-free, got %q/%q", p, m)
		}
	})
	t.Run("active provider overrides", func(t *testing.T) {
		a := &Agent{
			model:      "opencode/mimo-v2.5-free",
			activeIdx:  0,
			providers:  []core.ProviderConfig{{Name: "zhipuai-coding-plan", Model: "glm-5.1"}},
		}
		p, m := resolveOpencodeModelLocked(a)
		if p != "zhipuai-coding-plan" || m != "glm-5.1" {
			t.Fatalf("expected active provider override, got %q/%q", p, m)
		}
	})
}
