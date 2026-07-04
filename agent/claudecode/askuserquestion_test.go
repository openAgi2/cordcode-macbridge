package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// captureStdin is a thread-safe WriteCloser that records everything written to
// the Claude stdin so tests can assert the exact control_response payload shape
// produced by RespondPermission (the verified AskUserQuestion answer path).
type captureStdin struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captureStdin) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *captureStdin) Close() error { return nil }

// lastJSONLine parses the last non-empty newline-delimited JSON message written
// to stdin. Fails the test if no line is parseable.
func (c *captureStdin) lastJSONLine(t *testing.T) map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var last []byte
	for _, line := range bytes.Split(c.buf.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			last = line
		}
	}
	if last == nil {
		t.Fatal("captureStdin: no stdin line written")
	}
	var out map[string]any
	if err := json.Unmarshal(last, &out); err != nil {
		t.Fatalf("captureStdin: unmarshal stdin line failed: %v (line=%q)", err, string(last))
	}
	return out
}

func (c *captureStdin) linesWritten() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, line := range bytes.Split(c.buf.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

func newAskTestSession(t *testing.T) (*claudeSession, *captureStdin) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stdin := &captureStdin{}
	cs := &claudeSession{
		events: make(chan core.Event, 16),
		ctx:    ctx,
		stdin:  stdin,
	}
	cs.sessionID.Store("test-session")
	cs.alive.Store(true)
	return cs, stdin
}

// makeAskControlRequest builds a can_use_tool control request for AskUserQuestion
// with the given parsed question payloads (each a map[string]any with
// question/header/multiSelect/options).
func makeAskControlRequest(requestID string, questions []any) map[string]any {
	return map[string]any{
		"request_id": requestID,
		"request": map[string]any{
			"subtype":   "can_use_tool",
			"tool_name": "AskUserQuestion",
			"input": map[string]any{
				"questions": questions,
			},
		},
	}
}

func singleQuestionMap(question, header string, multiSelect bool, options ...[2]string) map[string]any {
	opts := make([]any, 0, len(options))
	for _, o := range options {
		opts = append(opts, map[string]any{"label": o[0], "description": o[1]})
	}
	return map[string]any{
		"question":    question,
		"header":      header,
		"multiSelect": multiSelect,
		"options":     opts,
	}
}

// drainEvent returns the next event within a short window, or nil if none arrive.
func drainEvent(t *testing.T, cs *claudeSession) *core.Event {
	t.Helper()
	select {
	case ev := <-cs.events:
		return &ev
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

// ============================================================
// S4: AskUserQuestion emission as question_asked
// ============================================================

func TestAskUserQuestion_ValidSingleQuestion_EmitsQuestionAsked(t *testing.T) {
	cs, _ := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-1", []any{
		singleQuestionMap("Pick a color", "Color", false, [2]string{"red", "r"}, [2]string{"blue", "b"}),
	}))

	ev := drainEvent(t, cs)
	if ev == nil {
		t.Fatal("expected question_asked event, got none")
	}
	if ev.Type != core.EventQuestionAsked {
		t.Fatalf("event type = %s, want EventQuestionAsked", ev.Type)
	}
	if ev.QuestionID != "req-1" {
		t.Errorf("QuestionID = %q, want req-1", ev.QuestionID)
	}
	if ev.QuestionText != "Color: Pick a color" {
		t.Errorf("QuestionText = %q, want header folded in", ev.QuestionText)
	}
	if !ev.Required {
		t.Errorf("Required = false, want true (AskUserQuestion is blocking)")
	}
	if ev.ThreadID != "" {
		t.Errorf("ThreadID = %q, want empty for Claude", ev.ThreadID)
	}
	if len(ev.QuestionOpts) != 2 {
		t.Fatalf("options count = %d, want 2", len(ev.QuestionOpts))
	}
	wantIDs := []string{"req-1:option-1", "req-1:option-2"}
	for i, o := range ev.QuestionOpts {
		if o.ID != wantIDs[i] {
			t.Errorf("option[%d].ID = %q, want %q", i, o.ID, wantIDs[i])
		}
	}
	if ev.QuestionOpts[0].Label != "red" || ev.QuestionOpts[1].Label != "blue" {
		t.Errorf("option labels = %q/%q, want red/blue", ev.QuestionOpts[0].Label, ev.QuestionOpts[1].Label)
	}
}

func TestAskUserQuestion_NoHeader_UsesQuestionOnly(t *testing.T) {
	cs, _ := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-h", []any{
		singleQuestionMap("Plain question", "", false, [2]string{"yes", ""}, [2]string{"no", ""}),
	}))
	ev := drainEvent(t, cs)
	if ev == nil || ev.Type != core.EventQuestionAsked {
		t.Fatalf("expected question_asked, got %v", ev)
	}
	if ev.QuestionText != "Plain question" {
		t.Errorf("QuestionText = %q, want no header prefix when header empty", ev.QuestionText)
	}
}

func TestAskUserQuestion_MultiQuestion_DeniedAndNoEvent(t *testing.T) {
	cs, stdin := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-multi", []any{
		singleQuestionMap("Q1", "", false, [2]string{"a", ""}),
		singleQuestionMap("Q2", "", false, [2]string{"b", ""}),
	}))

	// No question_asked / permission_request event should be emitted.
	if ev := drainEvent(t, cs); ev != nil {
		t.Fatalf("expected no event for multi-question deny, got %+v", ev)
	}
	line := stdin.lastJSONLine(t)
	resp := nested(t, line, "response", "response")
	if behavior, _ := resp["behavior"].(string); behavior != "deny" {
		t.Errorf("multi-question behavior = %q, want deny", behavior)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "multiple or multi-select") {
		t.Errorf("multi-question deny message = %q, want unsupported-shape wording", msg)
	}
}

func TestAskUserQuestion_MultiSelect_DeniedAndNoEvent(t *testing.T) {
	cs, stdin := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-ms", []any{
		singleQuestionMap("Pick many", "", true, [2]string{"a", ""}, [2]string{"b", ""}, [2]string{"c", ""}),
	}))
	if ev := drainEvent(t, cs); ev != nil {
		t.Fatalf("expected no event for multi-select deny, got %+v", ev)
	}
	resp := nested(t, stdin.lastJSONLine(t), "response", "response")
	if behavior, _ := resp["behavior"].(string); behavior != "deny" {
		t.Errorf("multi-select behavior = %q, want deny", behavior)
	}
}

func TestAskUserQuestion_ZeroValidQuestions_FallsBackToPermissionRequest(t *testing.T) {
	cs, _ := newAskTestSession(t)
	// A question with empty question text parses to zero valid (parseUserQuestions skips empty).
	cs.handleControlRequest(makeAskControlRequest("req-bad", []any{
		map[string]any{"question": "", "header": "", "options": []any{}},
	}))
	ev := drainEvent(t, cs)
	if ev == nil || ev.Type != core.EventPermissionRequest {
		t.Fatalf("expected fallback permission_request for malformed input, got %v", ev)
	}
	if ev.ToolName != "AskUserQuestion" {
		t.Errorf("fallback toolName = %q, want AskUserQuestion", ev.ToolName)
	}
}

// ============================================================
// S3: pending-question registry lifecycle
// ============================================================

func TestPendingRegistry_ReplyConsumesAndClears(t *testing.T) {
	cs, _ := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-r1", []any{
		singleQuestionMap("Which?", "", false, [2]string{"x", ""}, [2]string{"y", ""}),
	}))
	if ev := drainEvent(t, cs); ev == nil || ev.Type != core.EventQuestionAsked {
		t.Fatalf("setup: expected question_asked, got %v", ev)
	}
	if err := cs.RespondQuestion("req-r1", []string{"req-r1:option-2"}); err != nil {
		t.Fatalf("RespondQuestion: %v", err)
	}
	// Second reply must fail visibly (entry consumed).
	if err := cs.RespondQuestion("req-r1", []string{"req-r1:option-2"}); err == nil {
		t.Fatal("second RespondQuestion should fail (no pending), got nil")
	}
}

func TestPendingRegistry_RejectConsumesAndClears(t *testing.T) {
	cs, _ := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-rj", []any{
		singleQuestionMap("Which?", "", false, [2]string{"x", ""}),
	}))
	drainEvent(t, cs)
	if err := cs.RejectQuestion("req-rj"); err != nil {
		t.Fatalf("RejectQuestion: %v", err)
	}
	if err := cs.RespondQuestion("req-rj", []string{"req-rj:option-1"}); err == nil {
		t.Fatal("reply after reject should fail (entry consumed)")
	}
}

func TestPendingRegistry_LateReplyAfterCloseFails(t *testing.T) {
	cs, _ := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-late", []any{
		singleQuestionMap("Which?", "", false, [2]string{"x", ""}),
	}))
	drainEvent(t, cs)
	// Clear pending state as Close() does.
	cs.pendingQuestions.Range(func(k, _ any) bool {
		cs.pendingQuestions.Delete(k)
		return true
	})
	if err := cs.RespondQuestion("req-late", []string{"req-late:option-1"}); err == nil {
		t.Fatal("late reply after cleanup should fail visibly")
	}
}

func TestPendingRegistry_TwoPendingDoNotCollide(t *testing.T) {
	cs, _ := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-a", []any{
		singleQuestionMap("A?", "", false, [2]string{"a1", ""}),
	}))
	drainEvent(t, cs)
	cs.handleControlRequest(makeAskControlRequest("req-b", []any{
		singleQuestionMap("B?", "", false, [2]string{"b1", ""}),
	}))
	drainEvent(t, cs)

	// Answering req-b first must not affect req-a.
	if err := cs.RespondQuestion("req-b", []string{"req-b:option-1"}); err != nil {
		t.Fatalf("RespondQuestion req-b: %v", err)
	}
	if err := cs.RespondQuestion("req-a", []string{"req-a:option-1"}); err != nil {
		t.Fatalf("RespondQuestion req-a after req-b: %v", err)
	}
}

// ============================================================
// S5: question_reply / question_reject responder wire shape
// ============================================================

func TestRespondQuestion_BuildsVerifiedAnswerShape(t *testing.T) {
	cs, stdin := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-ans", []any{
		singleQuestionMap("Which color?", "Color", false, [2]string{"red", "r"}, [2]string{"blue", "b"}),
	}))
	drainEvent(t, cs)

	if err := cs.RespondQuestion("req-ans", []string{"req-ans:option-2"}); err != nil {
		t.Fatalf("RespondQuestion: %v", err)
	}

	// Expect a control_response with behavior=allow and updatedInput.answers keyed
	// by question text -> selected option label (the verified SDK shape).
	line := stdin.lastJSONLine(t)
	if typ, _ := line["type"].(string); typ != "control_response" {
		t.Fatalf("stdin type = %q, want control_response", typ)
	}
	resp := nested(t, line, "response", "response")
	if behavior, _ := resp["behavior"].(string); behavior != "allow" {
		t.Errorf("behavior = %q, want allow", behavior)
	}
	updatedInput, _ := resp["updatedInput"].(map[string]any)
	if updatedInput == nil {
		t.Fatal("updatedInput missing")
	}
	// Original input (questions array) must be preserved.
	if _, ok := updatedInput["questions"]; !ok {
		t.Error("updatedInput.questions missing — original input must be preserved")
	}
	answers, _ := updatedInput["answers"].(map[string]any)
	if answers == nil {
		t.Fatal("updatedInput.answers missing — this is the verified answer field")
	}
	// Answers keyed by exact question text, value = option label.
	label, ok := answers["Which color?"]
	if !ok {
		t.Fatalf("answers = %+v, want key 'Which color?'", answers)
	}
	if label != "blue" {
		t.Errorf("answers['Which color?'] = %v, want 'blue' (selected option-2)", label)
	}

	// A question_resolved event should follow.
	ev := drainEvent(t, cs)
	if ev == nil || ev.Type != core.EventQuestionResolved {
		t.Fatalf("expected question_resolved (replied), got %v", ev)
	}
}

func TestRespondQuestion_RejectSendsDenyWithSkipWording(t *testing.T) {
	cs, stdin := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-rej", []any{
		singleQuestionMap("Skip me?", "", false, [2]string{"a", ""}),
	}))
	drainEvent(t, cs)

	if err := cs.RejectQuestion("req-rej"); err != nil {
		t.Fatalf("RejectQuestion: %v", err)
	}
	resp := nested(t, stdin.lastJSONLine(t), "response", "response")
	if behavior, _ := resp["behavior"].(string); behavior != "deny" {
		t.Errorf("reject behavior = %q, want deny", behavior)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "skipped") {
		t.Errorf("reject message = %q, want explicit skip wording", msg)
	}
	ev := drainEvent(t, cs)
	if ev == nil || ev.Type != core.EventQuestionResolved || ev.Content != "rejected" {
		t.Fatalf("expected question_resolved (rejected), got %v", ev)
	}
}

func TestRespondQuestion_RequiresExactlyOneOptionID(t *testing.T) {
	cs, _ := newAskTestSession(t)
	cs.handleControlRequest(makeAskControlRequest("req-cnt", []any{
		singleQuestionMap("Which?", "", false, [2]string{"a", ""}, [2]string{"b", ""}),
	}))
	drainEvent(t, cs)

	for _, n := range []int{0, 2} {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = "req-cnt:option-1"
		}
		if err := cs.RespondQuestion("req-cnt", ids); err == nil {
			t.Errorf("RespondQuestion with %d option ids should fail, got nil", n)
		}
	}
	// Malformed count must NOT consume the entry; a correct reply still works.
	if err := cs.RespondQuestion("req-cnt", []string{"req-cnt:option-1"}); err != nil {
		t.Errorf("RespondQuestion after malformed count should still succeed (entry preserved): %v", err)
	}
}

func TestRespondQuestion_NoPendingFails(t *testing.T) {
	cs, _ := newAskTestSession(t)
	if err := cs.RespondQuestion("never-asked", []string{"x"}); err == nil {
		t.Fatal("RespondQuestion with no pending question should fail")
	}
	if err := cs.RejectQuestion("never-asked"); err == nil {
		t.Fatal("RejectQuestion with no pending question should fail")
	}
}

func TestRespondQuestion_EmptyQuestionIDFails(t *testing.T) {
	cs, _ := newAskTestSession(t)
	if err := cs.RespondQuestion("", []string{"x"}); err == nil {
		t.Error("RespondQuestion('') should fail")
	}
	if err := cs.RejectQuestion(""); err == nil {
		t.Error("RejectQuestion('') should fail")
	}
}

// ============================================================
// helpers
// ============================================================

// nested walks a chain of map[string]any keys, failing the test if any step is missing.
func nested(t *testing.T, m map[string]any, keys ...string) map[string]any {
	t.Helper()
	cur := m
	for _, k := range keys {
		next, _ := cur[k].(map[string]any)
		if next == nil {
			t.Fatalf("nested key %q missing in %+v", k, cur)
		}
		cur = next
	}
	return cur
}
