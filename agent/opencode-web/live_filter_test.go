package opencodeweb

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestOpenCodeLiveFilterConsumesObservedTurnOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan core.Event, 16)
	output := make(chan core.Event, 16)
	go filterOpenCodeLiveEvents(ctx, input, output)

	live := []core.Event{
		{Type: core.EventUserMessage, SessionID: "ses-live", TurnID: "msg_u", Content: "prompt"},
		{Type: core.EventTurnStarted, SessionID: "ses-live", TurnID: "msg_u"},
		{Type: core.EventText, SessionID: "ses-live", TurnID: "msg_u", Content: "真实回复"},
		{Type: core.EventResult, SessionID: "ses-live", TurnID: "msg_u", Done: true},
	}
	for _, ev := range live {
		input <- ev
	}
	for i, want := range []core.EventType{core.EventUserMessage, core.EventTurnStarted, core.EventText, core.EventResult} {
		select {
		case got := <-output:
			if got.Type != want || got.SessionID != "ses-live" || got.TurnID != "msg_u" {
				t.Fatalf("event %d = %+v, want type %v", i, got, want)
			}
			if want == core.EventText && got.Content != "真实回复" {
				t.Fatalf("text event = %+v", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event %d (%v) missing", i, want)
		}
	}

	for _, ev := range live {
		input <- ev
	}
	select {
	case got := <-output:
		t.Fatalf("replayed turn leaked: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestOpenCodeLiveFilterRejectsUnobservedTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := make(chan core.Event, 1)
	output := make(chan core.Event, 1)
	go filterOpenCodeLiveEvents(ctx, input, output)
	input <- core.Event{Type: core.EventResult, SessionID: "ses-cold", TurnID: "msg-old", Done: true}
	select {
	case got := <-output:
		t.Fatalf("unobserved terminal leaked: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}
