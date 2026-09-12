package gobridge

import (
	"context"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestReplayFreePreviewCacheIsTurnScopedAndConsumed(t *testing.T) {
	cache := newReplayFreePreviewCache()
	cache.Observe("codex-remote", "session", "turn-1", "hello ")
	cache.Observe("codex-remote", "session", "turn-2", "other")
	if got := cache.Take("codex-remote", "session", "turn-1"); got != "hello" {
		t.Fatalf("preview = %q", got)
	}
	if got := cache.Take("codex-remote", "session", "turn-1"); got != "" {
		t.Fatalf("consumed preview returned %q", got)
	}
	if got := cache.Take("codex-remote", "session", "turn-2"); got != "other" {
		t.Fatalf("turn-2 preview = %q", got)
	}
}

type replayFreeSubscriber struct {
	events chan core.Event
}

func (s replayFreeSubscriber) Subscribe(ctx context.Context) (<-chan core.Event, error) {
	go func() {
		<-ctx.Done()
		close(s.events)
	}()
	return s.events, nil
}

func TestPassiveReplayFreePreviewUsesLiveTextDeltas(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	h := NewHandlers()
	store, err := LoadWebPushStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline := NewWebPushCandidatePipeline(store)
	pipeline.SetBridgeID("brg_replay_preview")
	h.SetWebPushStore(store)
	h.SetWebPushPipeline(pipeline)

	events := make(chan core.Event, 3)
	events <- core.Event{
		Type: core.EventText, SessionID: "codex-unopened", ThreadID: "codex-unopened",
		TurnID: "turn-live", ItemID: "turn-live", Content: "真实回复 ",
	}
	events <- core.Event{
		Type: core.EventText, SessionID: "codex-unopened", ThreadID: "codex-unopened",
		TurnID: "turn-live", ItemID: "turn-live", Content: "预览",
	}
	events <- core.Event{
		Type: core.EventResult, SessionID: "codex-unopened", ThreadID: "codex-unopened",
		TurnID: "turn-live", Done: true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go startPassiveSubscription(ctx, h, "codex-remote", replayFreeSubscriber{events: events}.Subscribe, true, nil)

	deadline := time.After(5 * time.Second)
	for {
		got := pipeline.Drain()
		if len(got) > 1 {
			t.Fatalf("candidates = %+v", got)
		}
		if len(got) == 1 {
			if got[0].ContentPreview != "真实回复 预览" {
				t.Fatalf("preview = %q", got[0].ContentPreview)
			}
			if h.projectionKernel.HasReducerState("codex-remote", "codex-unopened") {
				t.Fatal("replay-free preview created hidden projection state")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("completion candidate did not arrive")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
