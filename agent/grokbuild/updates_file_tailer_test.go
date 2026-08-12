package grokbuild

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// updatesLine builds a JSON-RPC session/update line as grok writes it to updates.jsonl.
func updatesLine(method, sessionID string, update, meta map[string]any) []byte {
	params := map[string]any{
		"sessionId": sessionID,
		"update":    update,
	}
	if meta != nil {
		params["_meta"] = meta
	}
	obj := map[string]any{
		"timestamp": 1785861000,
		"method":    method,
		"params":    params,
	}
	b, _ := json.Marshal(obj)
	return append(b, '\n')
}

// setupUpdatesSession creates a grokHome with sessions/<encoded-cwd>/<sessionID>/
// and an EMPTY updates.jsonl, returning the file path. The empty file lets the
// tailer attach at offset 0; callers append lines to simulate live growth.
func setupUpdatesSession(t *testing.T, sessionID string) (grokHome, updatesPath string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "encoded-cwd", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("create empty updates.jsonl: %v", err)
	}
	return home, path
}

func appendUpdates(t *testing.T, path string, lines ...[]byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.Write(l); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

// fastTailKobs shrinks the tailer's timing knobs so tests run in milliseconds.
func fastTailKnobs() {
	grokUpdatesRelayPollInterval = 10 * time.Millisecond
	grokUpdatesRelayLateBindCap = 400 * time.Millisecond
	grokUpdatesRelayPostTurnGrace = 80 * time.Millisecond
	grokUpdatesRelayHardCap = 1500 * time.Millisecond
}

// startTailer launches the tailer and returns a stop() that cancels ctx and waits.
func startTailer(home, sessionID string, onEvent func(core.Event)) (stop func(wait time.Duration)) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		tail := newUpdatesFileTailSubscriber(home, sessionID)
		_ = tail.Run(ctx, onEvent)
		close(done)
	}()
	return func(wait time.Duration) {
		cancel()
		select {
		case <-done:
		case <-time.After(wait):
		}
	}
}

// TestUpdatesFileTailSkipsHistory: lines already present when the tailer attaches
// are NOT replayed (iOS loaded them via get_session_messages history).
func TestUpdatesFileTailSkipsHistory(t *testing.T) {
	fastTailKnobs()
	home, path := setupUpdatesSession(t, "ses-history")
	// Pre-fill BEFORE the tailer attaches.
	appendUpdates(t, path, updatesLine("_x.ai/session/update", "ses-history",
		map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "OLD"}}, nil))

	var got []core.Event
	var mu sync.Mutex
	stop := startTailer(home, "ses-history", func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	time.Sleep(250 * time.Millisecond) // no new growth → should stay quiet
	stop(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("history was replayed: got %d events, want 0: %+v", len(got), got)
	}
}

// TestUpdatesFileTailForwardsLive: appended session/update lines flow through
// convertSessionUpdate (both _x.ai/session/update and session/update methods).
func TestUpdatesFileTailForwardsLive(t *testing.T) {
	fastTailKnobs()
	home, path := setupUpdatesSession(t, "ses-live")

	var got []core.Event
	var mu sync.Mutex
	stop := startTailer(home, "ses-live", func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	time.Sleep(60 * time.Millisecond) // let the tailer attach at offset 0
	appendUpdates(t, path,
		updatesLine("_x.ai/session/update", "ses-live",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "hello file"}}, nil),
		updatesLine("session/update", "ses-live",
			map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "planning"}}, nil),
	)
	time.Sleep(150 * time.Millisecond) // let the ticker drain
	stop(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	if got[0].Type != core.EventText || got[0].Content != "hello file" {
		t.Fatalf("got[0] = %+v, want EventText 'hello file'", got[0])
	}
	if got[1].Type != core.EventThinking || got[1].Content != "planning" {
		t.Fatalf("got[1] = %+v, want EventThinking 'planning'", got[1])
	}
}

// TestUpdatesFileTailCatchUpPendingUserMessage: attach 时文件里已有一条“终态之后、
// 尚未完成”的 user_message_chunk —— iOS 是在 Mac 已发出 prompt 后才打开的会话。
// tailer 必须补扫出这条 pending prompt (身份延迟, 不合成), 已完成 turn 的 prompt
// 不重放。
func TestUpdatesFileTailCatchUpPendingUserMessage(t *testing.T) {
	fastTailKnobs()
	home, path := setupUpdatesSession(t, "ses-pending")
	appendUpdates(t, path,
		updatesLine("session/update", "ses-pending",
			map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "OLD DONE"}}, nil),
		updatesLine("session/update", "ses-pending",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "old reply"}}, nil),
		updatesLine("session/update", "ses-pending",
			map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p-old", "stop_reason": "end_turn"}, nil),
		updatesLine("session/update", "ses-pending",
			map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "讲个法国笑话"}}, nil),
	)

	var got []core.Event
	var mu sync.Mutex
	stop := startTailer(home, "ses-pending", func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	time.Sleep(120 * time.Millisecond) // attach 补扫 + 首个 live poll
	stop(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1 pending user message: %+v", len(got), got)
	}
	if got[0].Type != core.EventUserMessage || got[0].Content != "讲个法国笑话" {
		t.Fatalf("pending event = %+v, want EventUserMessage '讲个法国笑话'", got[0])
	}
	// 身份必须延迟到 relay 用同 turn 的 promptId 补齐, tailer 不得合成。
	if got[0].ItemID != "" || got[0].TurnID != "" {
		t.Fatalf("pending identity must stay deferred, got itemId=%q turnId=%q", got[0].ItemID, got[0].TurnID)
	}
}

// TestUpdatesFileTailNoPendingAfterCompletedTurn: 文件末尾已是 turn_completed 的
// 会话 (全部 turn 完成) —— 历史由冷 hydrate 提供, tailer 不得重放任何 prompt。
func TestUpdatesFileTailNoPendingAfterCompletedTurn(t *testing.T) {
	fastTailKnobs()
	home, path := setupUpdatesSession(t, "ses-settled")
	appendUpdates(t, path,
		updatesLine("session/update", "ses-settled",
			map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "old prompt"}}, nil),
		updatesLine("session/update", "ses-settled",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "old reply"}}, nil),
		updatesLine("session/update", "ses-settled",
			map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p-old", "stop_reason": "end_turn"}, nil),
	)

	var got []core.Event
	var mu sync.Mutex
	stop := startTailer(home, "ses-settled", func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	time.Sleep(150 * time.Millisecond)
	stop(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("completed history replayed as pending: %+v", got)
	}
}

// TestUpdatesFileTailDropsReplay: _meta.isReplay==true lines are dropped (parity
// with the leader subscriber: iOS already has authoritative history).
func TestUpdatesFileTailDropsReplay(t *testing.T) {
	fastTailKnobs()
	home, path := setupUpdatesSession(t, "ses-replay")

	var got []core.Event
	var mu sync.Mutex
	stop := startTailer(home, "ses-replay", func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	time.Sleep(60 * time.Millisecond)
	appendUpdates(t, path,
		updatesLine("_x.ai/session/update", "ses-replay",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "REPLAY"}},
			map[string]any{"isReplay": true}),
		updatesLine("_x.ai/session/update", "ses-replay",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "live"}}, nil),
	)
	time.Sleep(150 * time.Millisecond)
	stop(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1 (replay dropped): %+v", len(got), got)
	}
	if got[0].Content != "live" {
		t.Fatalf("got[0] = %+v, want 'live'", got[0])
	}
}

// TestUpdatesFileTailTurnCompleted: turn_completed maps to EventResult{Done:true},
// and the tailer exits within grace once growth stops (does not wait out the ctx).
func TestUpdatesFileTailTurnCompleted(t *testing.T) {
	fastTailKnobs()
	home, path := setupUpdatesSession(t, "ses-term")

	var got []core.Event
	var mu sync.Mutex
	start := time.Now()
	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		tail := newUpdatesFileTailSubscriber(home, "ses-term")
		_ = tail.Run(ctx, func(ev core.Event) {
			mu.Lock()
			got = append(got, ev)
			mu.Unlock()
		})
		close(done)
	}()
	time.Sleep(60 * time.Millisecond)
	appendUpdates(t, path,
		updatesLine("_x.ai/session/update", "ses-term",
			map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "finishing"}}, nil),
		updatesLine("_x.ai/session/update", "ses-term",
			map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p-abc", "stop_reason": "end_turn"}, nil),
	)
	<-done // tailer should exit on its own after grace
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	last := got[len(got)-1]
	if last.Type != core.EventResult || !last.Done || last.TurnID != "p-abc" {
		t.Fatalf("terminal event = %+v, want EventResult{Done,TurnID=p-abc}", last)
	}
	// Should exit ~grace (80ms) after turn_completed + last growth, not wait the full ctx.
	if elapsed > 900*time.Millisecond {
		t.Fatalf("tailer did not exit promptly after turn_completed: elapsed=%v", elapsed)
	}
}

// TestUpdatesFileTailTruncateReset: a rewritten (shorter) file resets offset to head
// and re-reads the new content. Pre-fills a long line, rewrites with a short one.
func TestUpdatesFileTailTruncateReset(t *testing.T) {
	fastTailKnobs()
	home, path := setupUpdatesSession(t, "ses-trunc")
	// Pre-fill a LONG line (tailer attaches at offset = its length).
	longPad := strings.Repeat("x", 400)
	appendUpdates(t, path, updatesLine("_x.ai/session/update", "ses-trunc",
		map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": longPad}}, nil))

	var got []core.Event
	var mu sync.Mutex
	stop := startTailer(home, "ses-trunc", func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	time.Sleep(80 * time.Millisecond)
	// Rewrite with a SHORT line → newSize < offset → reset to head → re-read.
	if err := os.WriteFile(path, updatesLine("_x.ai/session/update", "ses-trunc",
		map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "AFTER-TRUNC"}}, nil), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	stop(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	for _, ev := range got {
		if ev.Type == core.EventText && ev.Content == "AFTER-TRUNC" {
			return // success
		}
	}
	t.Fatalf("truncate reset did not re-read new content; events=%+v", got)
}

// TestUpdatesFileTailLateBind: the tailer waits for the session dir/file to appear
// (grok may create them after iOS opens the session) instead of failing fast, then
// catches growth once the file exists.
func TestUpdatesFileTailLateBind(t *testing.T) {
	fastTailKnobs()
	grokUpdatesRelayLateBindCap = 3 * time.Second // give late-bind room
	home := t.TempDir()                           // NO session dir yet

	var got []core.Event
	var mu sync.Mutex
	stop := startTailer(home, "ses-late", func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	time.Sleep(80 * time.Millisecond) // tailer is late-binding (file absent)
	// Create dir + empty file mid-run.
	dir := filepath.Join(home, "sessions", "encoded-cwd", "ses-late")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("create empty: %v", err)
	}
	time.Sleep(80 * time.Millisecond) // let waitForFile attach at offset 0
	appendUpdates(t, path, updatesLine("_x.ai/session/update", "ses-late",
		map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "late-arrived"}}, nil))
	time.Sleep(150 * time.Millisecond)
	stop(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Content != "late-arrived" {
		t.Fatalf("late-bind did not pick up file once created: got=%+v", got)
	}
}

// TestUpdatesFileTailMissingSessionErrors: if the session dir never appears within
// the late-bind cap, Run returns an error (honest failure, not a hang).
func TestUpdatesFileTailMissingSessionErrors(t *testing.T) {
	fastTailKnobs()
	home := t.TempDir()
	tail := newUpdatesFileTailSubscriber(home, "ses-missing")
	err := tail.Run(context.Background(), func(core.Event) {})
	if err == nil {
		t.Fatal("want error for missing session file, got nil")
	}
}

// chatHistoryLine builds a chat_history.jsonl row in grok's on-disk shape.
func chatHistoryLine(rowType, content string) []byte {
	obj := map[string]any{"type": rowType}
	if rowType == "user" {
		obj["content"] = []any{map[string]any{"type": "text", "text": content}}
	} else {
		obj["content"] = content
	}
	b, _ := json.Marshal(obj)
	return append(b, '\n')
}

// setupSessionWithHistory creates a session dir containing chat_history.jsonl
// with the given rows and an EMPTY updates.jsonl, returning both paths.
func setupSessionWithHistory(t *testing.T, sessionID string, rows ...[]byte) (grokHome, historyPath, updatesPath string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "encoded-cwd", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	historyPath = filepath.Join(dir, "chat_history.jsonl")
	if err := os.WriteFile(historyPath, bytesJoin(rows), 0o644); err != nil {
		t.Fatalf("write chat_history.jsonl: %v", err)
	}
	updatesPath = filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(updatesPath, nil, 0o644); err != nil {
		t.Fatalf("create empty updates.jsonl: %v", err)
	}
	return home, historyPath, updatesPath
}

func bytesJoin(rows [][]byte) []byte {
	var out []byte
	for _, r := range rows {
		out = append(out, r...)
	}
	return out
}

// TestUpdatesFileTailPendingSuppressedWhenInHistory: running-session cold-open
// repro. chat_history.jsonl already carries the in-flight turn's user prompt +
// partial assistant reply (grok appends them while the turn executes), and
// updates.jsonl has the SAME user_message_chunk after the last turn_completed
// (no terminal for this turn). The cold hydrate baseline already served the
// prompt, so the tailer must NOT replay it at attach — otherwise iOS shows the
// question twice and the reply restarting from the half.
func TestUpdatesFileTailPendingSuppressedWhenInHistory(t *testing.T) {
	fastTailKnobs()
	home, _, path := setupSessionWithHistory(t, "ses-dup",
		chatHistoryLine("user", "<user_query>\n这是完成情况：docs/2026-08-12-plan.md\n</user_query>"),
		chatHistoryLine("assistant", "对照完成情况与三张截图，只做效果分析，不动代码。"),
	)
	appendUpdates(t, path,
		updatesLine("session/update", "ses-dup",
			map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p-old", "stop_reason": "end_turn"}, nil),
		updatesLine("session/update", "ses-dup",
			map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "这是完成情况：docs/2026-08-12-plan.md"}}, nil),
	)

	var got []core.Event
	var mu sync.Mutex
	stop := startTailer(home, "ses-dup", func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	time.Sleep(120 * time.Millisecond) // attach 补扫 + 首个 live poll
	stop(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("attach replayed prompt already present in chat_history: got %d events, want 0: %+v", len(got), got)
	}
}

// TestUpdatesFileTailPendingEmittedWhenNotInHistory: the race window the
// attach-scan exists for. chat_history.jsonl exists but does NOT yet contain the
// in-flight prompt (Mac just sent it, grok hasn't flushed the user row) — the
// tailer must still replay the pending prompt so iOS doesn't miss the question.
func TestUpdatesFileTailPendingEmittedWhenNotInHistory(t *testing.T) {
	fastTailKnobs()
	home, _, path := setupSessionWithHistory(t, "ses-race",
		chatHistoryLine("user", "<user_query>\n上一个已完成的问题\n</user_query>"),
	)
	appendUpdates(t, path,
		updatesLine("session/update", "ses-race",
			map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "p-old", "stop_reason": "end_turn"}, nil),
		updatesLine("session/update", "ses-race",
			map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "刚发出的新问题"}}, nil),
	)

	var got []core.Event
	var mu sync.Mutex
	stop := startTailer(home, "ses-race", func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	time.Sleep(120 * time.Millisecond)
	stop(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1 pending user message: %+v", len(got), got)
	}
	if got[0].Type != core.EventUserMessage || got[0].Content != "刚发出的新问题" {
		t.Fatalf("pending event = %+v, want EventUserMessage '刚发出的新问题'", got[0])
	}
}
