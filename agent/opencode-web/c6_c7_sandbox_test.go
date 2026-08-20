package opencodeweb

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// c6_c7_sandbox_test.go is the C6/C7 regression against the REAL isolated
// 1.18.18 serve (mock scenarios A6_READ_OUTSIDE / A7_QUESTION / A8_TODOWRITE
// plus the real mutation routes): first server resolution wins, docks stay
// on the control plane, and rename/archive/delete converge through HTTP +
// catalog truth — never through invented events.
//
//	OCW_SANDBOX_URL=http://127.0.0.1:4398 OCW_SANDBOX_USER=gatea OCW_SANDBOX_PASS=gatea-pass \
//	  go test ./agent/opencode-web -run TestSandboxC6C7 -count=1 -v
func TestSandboxC6C7(t *testing.T) {
	base := strings.TrimSpace(os.Getenv("OCW_SANDBOX_URL"))
	if base == "" {
		t.Skip("set OCW_SANDBOX_URL (and OCW_SANDBOX_USER/PASS) to run the C6/C7 sandbox regression")
	}
	root := envOrDefault("OCW_SANDBOX_ROOT", "/tmp/ocw-gate-a-20260820")
	ws := envOrDefault("OCW_SANDBOX_WORKSPACE", filepath.Join(root, "workspace"))

	a, err := New(map[string]any{
		"work_dir":          ws,
		"opencode_web_url":  base,
		"opencode_web_user": os.Getenv("OCW_SANDBOX_USER"),
		"opencode_web_pass": os.Getenv("OCW_SANDBOX_PASS"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	agent := a.(*Agent)
	t.Cleanup(func() { _ = agent.Stop() })
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	c, err := agent.clientFor(ctx)
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	newSession := func() string {
		code, raw, err := c.doRequest(ctx, http.MethodPost, c.endpoint(c.apiPath("/session")+"?directory="+ws), map[string]any{}, ws, true)
		if err != nil || code >= 400 {
			t.Fatalf("create: code=%d err=%v body=%s", code, err, truncateForError(string(raw)))
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
			t.Fatalf("create decode: %v %s", err, truncateForError(string(raw)))
		}
		return created.ID
	}
	sendAndWait := func(t *testing.T, sid, text string) {
		t.Helper()
		sess, err := agent.StartSession(ctx, sid)
		if err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		defer sess.Close()
		if err := sess.(core.PromptOptionsSender).SendWithOptions(text, nil, nil, core.PromptOptions{Agent: "build"}); err != nil {
			t.Fatalf("send: %v", err)
		}
		waitTurnTerminal(t, sess.Events(), 90*time.Second)
	}

	// ── §6.8 question: asked → answer red via ResolveUserInput → replied ──
	t.Run("question", func(t *testing.T) {
		sid := newSession()
		sess, err := agent.StartSession(ctx, sid)
		if err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		defer sess.Close()
		if err := sess.(core.PromptOptionsSender).SendWithOptions("A7_QUESTION pick red", nil, nil, core.PromptOptions{Agent: "build"}); err != nil {
			t.Fatalf("send: %v", err)
		}
		var interactionID string
		deadline := time.After(60 * time.Second)
		for interactionID == "" {
			select {
			case ev := <-sess.Events():
				if ev.Type == core.EventUserInputRequested && ev.UserInput != nil {
					interactionID = ev.UserInput.InteractionID
				}
			case <-deadline:
				t.Fatal("question.asked never surfaced as user_input_requested")
			}
		}
		res, err := agent.ResolveUserInput(ctx, interactionID, "act_sb_1", core.UserInputActionAnswer, []core.UserInputAnswer{{
			QuestionID: interactionID + "/q0",
			Values:     []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: interactionID + "/q0/o0"}},
		}})
		if err != nil {
			t.Fatalf("ResolveUserInput: %v", err)
		}
		if res.Outcome != core.UserInputOutcomeAccepted {
			t.Fatalf("outcome = %+v", res)
		}
		// The server broadcast resolves the dock (first server resolution
		// wins) and the turn then converges to a terminal.
		waitTurnTerminal(t, sess.Events(), 90*time.Second)
		t.Logf("question %s answered through the official reply route", interactionID)
	})

	// ── §6.9 todo: replacement list via endpoint, order/fields verbatim ────
	t.Run("todo", func(t *testing.T) {
		sid := newSession()
		sendAndWait(t, sid, "A8_TODOWRITE two items")
		todos, err := agent.FetchTodos(ctx, sid)
		if err != nil {
			t.Fatalf("FetchTodos: %v", err)
		}
		if len(todos) == 0 {
			t.Fatal("the todowrite scenario must leave items on GET /session/:id/todo")
		}
		for _, todo := range todos {
			if todo.Content == "" || todo.Status == "" {
				t.Fatalf("todo items must preserve server fields, got %+v", todos)
			}
		}
		t.Logf("todos endpoint returned %d ordered items", len(todos))

		// Completion transition: the update scenario flips items to completed.
		sendAndWait(t, sid, "A8_TODOWRITE_UPDATE finish them")
		todos, err = agent.FetchTodos(ctx, sid)
		if err != nil {
			t.Fatalf("FetchTodos after update: %v", err)
		}
		for _, todo := range todos {
			if todo.Status != "completed" {
				t.Fatalf("second write must transition items to completed, got %+v", todos)
			}
		}
	})

	// ── §6.7 permission: asked → allow(once) → server resolution ──────────
	t.Run("permission", func(t *testing.T) {
		sid := newSession()
		sess, err := agent.StartSession(ctx, sid)
		if err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		defer sess.Close()
		if err := sess.(core.PromptOptionsSender).SendWithOptions("A6_READ_OUTSIDE read the file", nil, nil, core.PromptOptions{Agent: "build"}); err != nil {
			t.Fatalf("send: %v", err)
		}
		var requestID string
		deadline := time.After(60 * time.Second)
		for requestID == "" {
			select {
			case ev := <-sess.Events():
				if ev.Type == core.EventPermissionRequest && ev.RequestID != "" {
					requestID = ev.RequestID
				}
			case <-deadline:
				t.Fatal("permission.asked never surfaced")
			}
		}
		if err := agent.RespondSessionPermission(ctx, sid, requestID, core.PermissionResult{Behavior: "allow"}); err != nil {
			t.Fatalf("respond: %v", err)
		}
		waitTurnTerminal(t, sess.Events(), 90*time.Second)
		t.Logf("permission %s answered once; turn converged", requestID)
	})

	// ── §6.10 mutations: rename → archive(OD-1) → delete convergence ──────
	t.Run("mutations", func(t *testing.T) {
		sid := newSession()
		info, err := agent.RenameSession(ctx, sid, "c7-regression-title")
		if err != nil {
			t.Fatalf("rename: %v", err)
		}
		if info.Summary != "c7-regression-title" {
			t.Fatalf("rename echo title = %q", info.Summary)
		}
		byID, err := agent.FetchSessionInfo(ctx, sid)
		if err != nil || byID.Summary != "c7-regression-title" {
			t.Fatalf("by-ID must converge to the new title, got %+v err=%v", byID, err)
		}

		archived, err := agent.ArchiveSession(ctx, sid, time.Now().UTC().Add(-time.Second))
		if err != nil || archived.ArchivedAt.IsZero() {
			t.Fatalf("archive: %+v err=%v", archived, err)
		}
		sessions, err := agent.ListSessionsInDirectory(ctx, ws)
		if err != nil {
			t.Fatalf("list after archive: %v", err)
		}
		for _, s := range sessions {
			if s.ID == sid {
				t.Fatalf("archived %s must be hidden from the default list (OD-1)", sid)
			}
		}

		if err := agent.DeleteSession(ctx, sid); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := agent.FetchSessionInfo(ctx, sid); err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("by-ID after delete must 404, got %v", err)
		}
		t.Log("mutations converged: rename → archive(hidden/by-ID kept) → delete(404)")
	})
}
