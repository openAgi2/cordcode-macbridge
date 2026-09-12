package grokbuild

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestSubscribeLiveSkipsExistingHistoryAndEmitsNewTurn(t *testing.T) {
	fastTailKnobs()
	home, path := setupUpdatesSession(t, "ses-global-live")
	appendUpdates(t, path,
		updatesLine("_x.ai/session/update", "ses-global-live",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "OLD"}},
			map[string]any{"promptId": "p-old", "isReplay": true}),
		updatesLine("_x.ai/session/update", "ses-global-live",
			map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p-old", "stop_reason": "end_turn"}, nil),
	)

	agent := &Agent{grokHome: home}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := agent.SubscribeLive(ctx)
	if err != nil {
		t.Fatalf("SubscribeLive: %v", err)
	}
	time.Sleep(40 * time.Millisecond) // initial sweep must baseline existing EOF
	appendUpdates(t, path,
		updatesLine("_x.ai/session/update", "ses-global-live",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "真实回复"}},
			map[string]any{"promptId": "p-live"}),
		updatesLine("_x.ai/session/update", "ses-global-live",
			map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p-live", "stop_reason": "end_turn"}, nil),
	)

	textSeen := false
	resultSeen := false
	deadline := time.After(3 * time.Second)
	for !textSeen || !resultSeen {
		select {
		case ev := <-events:
			switch {
			case ev.Type == core.EventText && ev.SessionID == "ses-global-live":
				if ev.Content != "真实回复" || ev.TurnID != "p-live" {
					t.Fatalf("text event = %+v", ev)
				}
				textSeen = true
			case ev.Type == core.EventResult && ev.SessionID == "ses-global-live":
				if !ev.Done || ev.TurnID != "p-live" {
					t.Fatalf("result event = %+v", ev)
				}
				resultSeen = true
			}
		case <-deadline:
			t.Fatalf("live events incomplete: text=%v result=%v", textSeen, resultSeen)
		}
	}
}

func TestSubscribeLiveObservesNewSessionJournal(t *testing.T) {
	fastTailKnobs()
	home, _ := setupUpdatesSession(t, "ses-existing")
	agent := &Agent{grokHome: home}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := agent.SubscribeLive(ctx)
	if err != nil {
		t.Fatalf("SubscribeLive: %v", err)
	}
	time.Sleep(40 * time.Millisecond)

	dir := filepath.Join(home, "sessions", "encoded-cwd", "ses-new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	appendUpdates(t, path,
		updatesLine("_x.ai/session/update", "ses-new",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "new session"}},
			map[string]any{"promptId": "p-new"}),
		updatesLine("_x.ai/session/update", "ses-new",
			map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p-new", "stop_reason": "end_turn"}, nil),
	)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.SessionID == "ses-new" && ev.Type == core.EventResult && ev.Done && ev.TurnID == "p-new" {
				return
			}
		case <-deadline:
			t.Fatal("new session journal completion was not observed")
		}
	}
}

func TestSubscribeLiveRetainsInProgressTurnAtStartup(t *testing.T) {
	fastTailKnobs()
	home, path := setupUpdatesSession(t, "ses-midturn")
	appendUpdates(t, path,
		updatesLine("_x.ai/session/update", "ses-midturn",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "旧完成回复"}},
			map[string]any{"promptId": "p-old"}),
		updatesLine("_x.ai/session/update", "ses-midturn",
			map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p-old", "stop_reason": "end_turn"}, nil),
		updatesLine("_x.ai/session/update", "ses-midturn",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "启动前已生成"}},
			map[string]any{"promptId": "p-mid"}),
	)
	agent := &Agent{grokHome: home}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := agent.SubscribeLive(ctx)
	if err != nil {
		t.Fatalf("SubscribeLive: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	appendUpdates(t, path,
		updatesLine("_x.ai/session/update", "ses-midturn",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": " 启动后完成"}},
			map[string]any{"promptId": "p-mid"}),
		updatesLine("_x.ai/session/update", "ses-midturn",
			map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p-mid", "stop_reason": "end_turn"}, nil),
	)
	deadline := time.After(3 * time.Second)
	var text string
	for {
		select {
		case ev := <-events:
			if ev.Type == core.EventText && ev.SessionID == "ses-midturn" && ev.TurnID == "p-mid" {
				text += ev.Content
			}
			if ev.Type == core.EventResult && ev.SessionID == "ses-midturn" && ev.TurnID == "p-mid" {
				if text != "启动前已生成 启动后完成" {
					t.Fatalf("mid-turn preview text = %q", text)
				}
				return
			}
		case <-deadline:
			t.Fatalf("mid-turn completion was not observed, text=%q", text)
		}
	}
}
