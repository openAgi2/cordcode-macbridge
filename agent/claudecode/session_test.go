package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestProductionSessionStructuredInputHelperProcess(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resumeID string
		wantSID  string
	}{
		{name: "new", wantSID: "fixture-session"},
		{name: "resume", resumeID: "fixture-session", wantSID: "fixture-session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cs, err := newClaudeSession(ctx, t.TempDir(), os.Args[0],
				[]string{"-test.run=TestHelperProcess", "--", "structured-input-fixture"}, "",
				"", "", tc.resumeID, "default", nil, nil,
				[]string{"GO_WANT_HELPER_PROCESS=1"}, "", false, core.SpawnOptions{}, 0, "")
			if err != nil {
				t.Fatalf("newClaudeSession: %v", err)
			}
			defer func() { _ = cs.Close() }()

			var requested core.Event
			for requested.Type != core.EventUserInputRequested {
				select {
				case ev, ok := <-cs.Events():
					if !ok {
						t.Fatal("session exited before structured request")
					}
					requested = ev
				case <-ctx.Done():
					t.Fatal("timed out waiting for structured request")
				}
			}
			if requested.SessionID != tc.wantSID || requested.TurnID != "assistant-tool-only" {
				t.Fatalf("request identity = session %q turn %q", requested.SessionID, requested.TurnID)
			}
			// Destroy live identity before answering: resolution must use the turn captured in registry.
			cs.activeMsgID.Store("")
			cs.streamState.reset()
			ui := requested.UserInput
			answer := []core.UserInputAnswer{{QuestionID: ui.Questions[0].ID, Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: ui.Questions[0].Options[0].ID}}}}
			if _, err := cs.ResolveUserInput(ctx, ui.InteractionID, "f40f8934-8f3d-4e5f-a9b5-883b6a8f5147", core.UserInputActionAnswer, answer); err != nil {
				t.Fatalf("ResolveUserInput: %v", err)
			}
			var resolved core.Event
			for resolved.Type != core.EventUserInputResolved {
				select {
				case ev, ok := <-cs.Events():
					if !ok {
						t.Fatal("session exited before resolved event")
					}
					resolved = ev
				case <-ctx.Done():
					t.Fatal("timed out waiting for resolved event")
				}
			}
			if resolved.TurnID != requested.TurnID {
				t.Fatalf("resolved turn %q != owning turn %q", resolved.TurnID, requested.TurnID)
			}
		})
	}
}

func TestHandleResultParsesUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs := &claudeSession{
		events: make(chan core.Event, 8),
		ctx:    ctx,
	}
	cs.sessionID.Store("test-session")
	cs.alive.Store(true)

	raw := map[string]any{
		"type":       "result",
		"result":     "done",
		"session_id": "test-session",
		"usage": map[string]any{
			"input_tokens":  float64(150000),
			"output_tokens": float64(2000),
		},
	}

	cs.handleResult(raw)

	evt := <-cs.events
	if evt.InputTokens != 150000 {
		t.Errorf("InputTokens = %d, want 150000", evt.InputTokens)
	}
	if evt.OutputTokens != 2000 {
		t.Errorf("OutputTokens = %d, want 2000", evt.OutputTokens)
	}
}

func TestHandleResultNoUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cs := &claudeSession{
		events: make(chan core.Event, 8),
		ctx:    ctx,
	}
	cs.sessionID.Store("test-session")
	cs.alive.Store(true)

	raw := map[string]any{
		"type":   "result",
		"result": "done",
	}

	cs.handleResult(raw)

	evt := <-cs.events
	if evt.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", evt.InputTokens)
	}
	if evt.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0", evt.OutputTokens)
	}
}

func TestReadLoop_ChildHoldsStdoutPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
	})

	writeDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(pw, `{"type":"system","session_id":"test-pipe"}`+"\n")
		writeDone <- err
	}()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	cs := &claudeSession{
		cmd:    cmd,
		events: make(chan core.Event, 64),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	cs.alive.Store(true)
	go cs.readLoop(pr, &stderrBuf)

	timeout := time.After(5 * time.Second)
	gotEvent := false
	for {
		select {
		case err := <-writeDone:
			if err != nil {
				t.Fatal(err)
			}
			writeDone = nil
		case evt, ok := <-cs.events:
			if !ok {
				if !gotEvent {
					t.Fatal("events closed but system event lost")
				}
				return
			}
			if evt.SessionID == "test-pipe" {
				gotEvent = true
			}
		case <-timeout:
			t.Fatal("HANG: events not closed within 5s - readLoop stuck in scanner.Scan()")
		}
	}
}

func TestReadLoop_CtxCancelClosesChannels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
	})

	// "err-then-sleep" emits stderr before sleeping so that ctx cancel
	// produces a non-empty stderrBuf in readLoop's defer — exercising the
	// `case <-cs.ctx.Done()` select branch in finishReadLoop.
	cmd := helperCommand(ctx, "err-then-sleep")
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	cs := &claudeSession{
		cmd:    cmd,
		events: make(chan core.Event, 64),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	cs.alive.Store(true)
	go cs.readLoop(pr, &stderrBuf)

	time.Sleep(200 * time.Millisecond)
	cancel()

	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-cs.events:
			if !ok {
				goto closed
			}
		case <-timeout:
			t.Fatal("HANG: events not closed within 5s after ctx cancel")
		}
	}
closed:
	select {
	case <-cs.done:
	case <-timeout:
		t.Fatal("HANG: done not closed within 5s after ctx cancel")
	}
}

func TestClaudeSessionClose_IdempotentNoPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := helperCommand(ctx, "stdin-eof-exit")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	cs := &claudeSession{
		cmd:                 cmd,
		stdin:               stdin,
		ctx:                 ctx,
		cancel:              cancel,
		done:                done,
		gracefulStopTimeout: 200 * time.Millisecond,
	}
	cs.alive.Store(true)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Close panicked: %v", r)
		}
	}()

	if err := cs.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := cs.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestShellJoinArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"empty", nil, ""},
		{"single_plain", []string{"--verbose"}, "--verbose"},
		{"multiple_plain", []string{"--verbose", "--model", "opus"}, "--verbose --model opus"},
		{"arg_with_space", []string{"--prompt", "hello world"}, "--prompt 'hello world'"},
		{"arg_with_tab", []string{"a\tb"}, "'a\tb'"},
		{"arg_with_newline", []string{"line1\nline2"}, "'line1\nline2'"},
		{"arg_with_single_quote", []string{"it's"}, "'it'\\''s'"},
		{"arg_with_double_quote", []string{`say "hi"`}, `'say "hi"'`},
		{"arg_with_backslash", []string{`path\to`}, `'path\to'`},
		{"mixed", []string{"--flag", "has space", "plain", "it's here"}, "--flag 'has space' plain 'it'\\''s here'"},
		{"empty_string_arg", []string{""}, ""},
		{"long_prompt", []string{"--append-system-prompt", "You are a helpful assistant.\nBe concise."}, "--append-system-prompt 'You are a helpful assistant.\nBe concise.'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellJoinArgs(tt.args)
			if got != tt.want {
				t.Errorf("shellJoinArgs(%v)\n  got  = %q\n  want = %q", tt.args, got, tt.want)
			}
		})
	}
}

func helperCommand(ctx context.Context, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess", "--", mode)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return cmd
}

// TestHelperProcess lets this test binary act as a tiny external command for
// cases that need a process with controlled lifetime semantics.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	mode := os.Args[len(os.Args)-1]
	for _, arg := range os.Args {
		if arg == "structured-input-fixture" {
			mode = arg
			break
		}
	}
	switch mode {
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "err-then-sleep":
		_, _ = os.Stderr.WriteString("helper: starting up\n")
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "stdin-eof-exit":
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	case "structured-input-fixture":
		_, _ = os.Stdout.WriteString(`{"type":"system","session_id":"fixture-session"}` + "\n")
		for i, arg := range os.Args {
			if arg == "--resume" && i+1 < len(os.Args) {
				_, _ = os.Stdout.WriteString(`{"type":"result","result":"history drained","session_id":"fixture-session"}` + "\n")
				break
			}
		}
		_, _ = os.Stdout.WriteString(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"assistant-tool-only"}}}` + "\n")
		_, _ = os.Stdout.WriteString(`{"type":"control_request","request_id":"fixture-request","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[{"question":"Retry?","header":"Build","multiSelect":false,"options":[{"label":"Retry","description":"Try again"},{"label":"Fail","description":"Stop"}]}]}}}` + "\n")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			os.Exit(3)
		}
		_, _ = os.Stdout.WriteString(`{"type":"result","result":"done","session_id":"fixture-session"}` + "\n")
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
