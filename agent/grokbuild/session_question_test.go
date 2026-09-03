package grokbuild

// Follower question answer-path tests (Phase 2): the driver-rail
// (grokSession own-turn questions) and the leader-rail write path
// (LeaderSubscriber.AnswerQuestion/CancelQuestion). Wire expectations are the
// §3.3 answer shape from the live capture: original numeric id, outcome
// accepted, answers keyed by QUESTION TEXT with option LABELS as values.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ── driver rail: grokSession own-turn questions ────────────────────────────

// startQuestionProbeAgent runs a fake stdio ACP agent over io.Pipe that
// immediately sends an ask_user_question REQUEST and records every line
// written back by the session (the responses we assert on). No handshake is
// needed: the fixture is a reverse REQUEST, handled by readLoop directly.
func startQuestionProbeAgent(t *testing.T, requestLine string) (sess *grokSession, written *[]string, wMu *sync.Mutex, stop func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	var mu sync.Mutex
	var lines []string

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = outW.Write([]byte(requestLine + "\n"))
		sc := bufio.NewScanner(inR)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			mu.Lock()
			lines = append(lines, sc.Text())
			mu.Unlock()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	sess = &grokSession{
		agent:            &Agent{workDir: t.TempDir()},
		stdin:            inW,
		stdout:           outR,
		events:           make(chan core.Event, 64),
		ctx:              ctx,
		cancel:           cancel,
		done:             make(chan struct{}),
		pendingPerms:     make(map[string][]permissionOption),
		pendingQuestions: make(map[string]*pendingAskUserQuestion),
		respChannels:     make(map[int]chan *jsonrpcResponse),
	}
	sess.alive.Store(true)
	sess.sessionID.Store("sess-1")
	go sess.readLoop()

	stop = func() {
		_ = inW.Close()
		_ = outW.Close()
		_ = inR.Close()
		cancel()
		<-done
	}
	return sess, &lines, &mu, stop
}

// driverQuestionFixture is a §3.1-shaped ask_user_question REQUEST as the
// direct stdio agent would send it (numeric id, params inlined).
const driverQuestionFixture = `{"jsonrpc":"2.0","id":5,"method":"x.ai/ask_user_question","params":{"sessionId":"sess-1","toolCallId":"call_drv1","questions":[{"question":"选一个?","options":[{"label":"A","description":"选项A"},{"label":"B","description":"选项B"}],"multiSelect":null}],"mode":"default"}}`

func awaitEvent(t *testing.T, sess *grokSession) core.Event {
	t.Helper()
	select {
	case ev := <-sess.events:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for session event")
		return core.Event{}
	}
}

// awaitEventOfType skips the sibling face of a dual-track emission (each
// question ask/resolve emits canonical user_input_* AND legacy question_*).
func awaitEventOfType(t *testing.T, sess *grokSession, typ core.EventType) core.Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-sess.events:
			if ev.Type == typ {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s event", typ)
			return core.Event{}
		}
	}
}

// lastAgentResponse parses the most recent JSON-RPC response the fake agent
// received. Returns nil when nothing was written yet.
func lastAgentResponse(t *testing.T, lines *[]string, mu *sync.Mutex) map[string]any {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if len(*lines) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte((*lines)[len(*lines)-1]), &m); err != nil {
		t.Fatalf("parse agent line %q: %v", (*lines)[len(*lines)-1], err)
	}
	return m
}

func TestGrokSessionRespondQuestion(t *testing.T) {
	sess, lines, mu, stop := startQuestionProbeAgent(t, driverQuestionFixture)
	defer stop()

	// Dual-track ask: canonical user_input_requested first, then legacy.
	ui := awaitEventOfType(t, sess, core.EventUserInputRequested)
	if ui.ItemID != "call_drv1" || ui.UserInput == nil || ui.UserInput.InteractionID != "call_drv1" {
		t.Fatalf("canonical ask = %+v, want interaction call_drv1", ui)
	}
	if ui.UserInput.Status != core.UserInputStatusPending || !ui.UserInput.CanRespond || !ui.UserInput.CanReject {
		t.Fatalf("canonical interaction state = %+v", ui.UserInput)
	}
	if len(ui.UserInput.Questions) != 1 {
		t.Fatalf("questions = %+v", ui.UserInput.Questions)
	}
	q := ui.UserInput.Questions[0]
	if q.ID != "call_drv1" || q.Prompt != "选一个?" || q.AnswerMode != core.UserInputAnswerModeSingle || !q.AllowsCustomAnswer {
		t.Fatalf("canonical question = %+v", q)
	}
	if len(q.Options) != 2 || q.Options[0].ID != "A" || q.Options[0].Description != "选项A" {
		t.Fatalf("canonical options = %+v", q.Options)
	}

	ev := awaitEventOfType(t, sess, core.EventQuestionAsked)
	if ev.QuestionID != "call_drv1" || ev.QuestionText != "选一个?" {
		t.Fatalf("legacy ask = %+v, want question_asked for call_drv1", ev)
	}
	if len(ev.QuestionOpts) != 2 || ev.QuestionOpts[0].ID != "A" || ev.QuestionOpts[0].Description != "选项A" {
		t.Fatalf("opts = %+v", ev.QuestionOpts)
	}

	if err := sess.RespondQuestion("call_drv1", []string{"A"}); err != nil {
		t.Fatalf("RespondQuestion: %v", err)
	}

	// Driver rail has no leader broadcast — the flush itself closes the card
	// locally on both faces (canonical user_input_resolved + legacy).
	res := awaitEventOfType(t, sess, core.EventUserInputResolved)
	if res.ItemID != "call_drv1" || res.UserInput == nil || res.UserInput.Status != core.UserInputStatusAnswered || res.UserInput.ResolutionSource != "ios" {
		t.Fatalf("canonical resolved = %+v, want call_drv1 answered/ios", res)
	}
	resolved := awaitEventOfType(t, sess, core.EventQuestionResolved)
	if resolved.QuestionID != "call_drv1" || resolved.Content != "accepted" {
		t.Fatalf("legacy resolved = %+v, want question_resolved call_drv1 accepted", resolved)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m := lastAgentResponse(t, lines, mu); m != nil {
			if fmt.Sprint(m["id"]) == "5" {
				result, _ := m["result"].(map[string]any)
				if result == nil || result["outcome"] != "accepted" {
					t.Fatalf("result = %+v, want outcome accepted", result)
				}
				answers, _ := result["answers"].(map[string]any)
				want := map[string]bool{`选一个?`: false}
				for q, v := range answers {
					labels, _ := v.([]any)
					if q == "选一个?" && len(labels) == 1 && labels[0] == "A" {
						want["选一个?"] = true
					}
				}
				if !want["选一个?"] {
					t.Fatalf("answers = %+v, want {选一个?: [A]} keyed by question text", answers)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("agent never received the id=5 answer response")
}

func TestGrokSessionRejectQuestion(t *testing.T) {
	sess, lines, mu, stop := startQuestionProbeAgent(t, driverQuestionFixture)
	defer stop()

	awaitEventOfType(t, sess, core.EventUserInputRequested)
	awaitEventOfType(t, sess, core.EventQuestionAsked)
	if err := sess.RejectQuestion("call_drv1"); err != nil {
		t.Fatalf("RejectQuestion: %v", err)
	}

	res := awaitEventOfType(t, sess, core.EventUserInputResolved)
	if res.ItemID != "call_drv1" || res.UserInput == nil || res.UserInput.Status != core.UserInputStatusRejected {
		t.Fatalf("canonical resolved = %+v, want call_drv1 rejected", res)
	}
	resolved := awaitEventOfType(t, sess, core.EventQuestionResolved)
	if resolved.QuestionID != "call_drv1" || resolved.Content != "cancelled" {
		t.Fatalf("legacy resolved = %+v, want question_resolved call_drv1 cancelled", resolved)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m := lastAgentResponse(t, lines, mu); m != nil {
			if fmt.Sprint(m["id"]) == "5" {
				result, _ := m["result"].(map[string]any)
				if result == nil || result["outcome"] != "cancelled" {
					t.Fatalf("result = %+v, want outcome cancelled", result)
				}
				if _, has := result["answers"]; has {
					t.Fatalf("cancelled response must not carry answers: %+v", result)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("agent never received the id=5 cancel response")
}

func TestGrokSessionRespondQuestionValidation(t *testing.T) {
	sess, _, _, stop := startQuestionProbeAgent(t, driverQuestionFixture)
	defer stop()
	awaitEventOfType(t, sess, core.EventUserInputRequested)

	if err := sess.RespondQuestion("call_drv1", []string{"不是选项"}); err == nil {
		t.Fatal("invalid option label must error")
	}
	if err := sess.RespondQuestion("call_drv1", nil); err == nil {
		t.Fatal("empty selection must error")
	}
	if err := sess.RespondQuestion("call_unknown", []string{"A"}); err == nil {
		t.Fatal("unknown question must error")
	}
	// Entry must still be pending after failed validations.
	sess.pendingPermsMu.Lock()
	_, ok := s_pendingQuestion(sess, "call_drv1")
	sess.pendingPermsMu.Unlock()
	if !ok {
		t.Fatal("failed validation must not consume the pending question")
	}
	if err := sess.RespondQuestion("call_drv1", []string{"B"}); err != nil {
		t.Fatalf("valid answer after failed validation: %v", err)
	}
}

func s_pendingQuestion(sess *grokSession, toolCallID string) (*pendingAskUserQuestion, bool) {
	p, ok := sess.pendingQuestions[toolCallID]
	return p, ok
}

func TestGrokSessionLateQuestionReplySilent(t *testing.T) {
	sess, lines, mu, stop := startQuestionProbeAgent(t, driverQuestionFixture)
	defer stop()
	awaitEventOfType(t, sess, core.EventUserInputRequested)

	if err := sess.RespondQuestion("call_drv1", []string{"A"}); err != nil {
		t.Fatalf("first answer: %v", err)
	}
	// Wait for the response to land, then replay the answer — must be silent.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m := lastAgentResponse(t, lines, mu); m != nil && fmt.Sprint(m["id"]) == "5" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	before := len(*lines)
	mu.Unlock()
	if err := sess.RespondQuestion("call_drv1", []string{"A"}); err != nil {
		t.Fatalf("late answer must be silent, got %v", err)
	}
	if err := sess.RejectQuestion("call_drv1"); err != nil {
		t.Fatalf("late reject must be silent, got %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	after := len(*lines)
	mu.Unlock()
	if after != before {
		t.Fatalf("late replies wrote %d extra frames, want 0", after-before)
	}
}

func TestGrokSessionMultiQuestionAccumulate(t *testing.T) {
	const twoQuestions = `{"jsonrpc":"2.0","id":9,"method":"x.ai/ask_user_question","params":{"sessionId":"sess-1","toolCallId":"call_multi","questions":[{"question":"第一问?","options":[{"label":"甲","description":""},{"label":"乙","description":""}],"multiSelect":null},{"question":"第二问?","options":[{"label":"丙","description":""},{"label":"丁","description":""}],"multiSelect":null}],"mode":"default"}}`
	sess, lines, mu, stop := startQuestionProbeAgent(t, twoQuestions)
	defer stop()

	// Each question emits canonical + legacy in that order; the canonical face
	// carries the derived per-question ids.
	ui1 := awaitEventOfType(t, sess, core.EventUserInputRequested)
	if ui1.ItemID != "call_multi" {
		t.Fatalf("canonical question 0 id = %q, want bare tool_call_id", ui1.ItemID)
	}
	l1 := awaitEventOfType(t, sess, core.EventQuestionAsked)
	if l1.QuestionID != "call_multi" {
		t.Fatalf("legacy question 0 id = %q", l1.QuestionID)
	}
	ui2 := awaitEventOfType(t, sess, core.EventUserInputRequested)
	if ui2.ItemID != "call_multi#1" {
		t.Fatalf("canonical question 1 id = %q, want call_multi#1", ui2.ItemID)
	}
	l2 := awaitEventOfType(t, sess, core.EventQuestionAsked)
	if l2.QuestionID != "call_multi#1" {
		t.Fatalf("legacy question 1 id = %q", l2.QuestionID)
	}

	if err := sess.RespondQuestion("call_multi", []string{"甲"}); err != nil {
		t.Fatalf("answer q0: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if m := lastAgentResponse(t, lines, mu); m != nil && fmt.Sprint(m["id"]) == "9" {
		t.Fatal("response flushed before all questions answered")
	}
	if err := sess.RespondQuestion("call_multi#1", []string{"丁"}); err != nil {
		t.Fatalf("answer q1: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m := lastAgentResponse(t, lines, mu); m != nil && fmt.Sprint(m["id"]) == "9" {
			result, _ := m["result"].(map[string]any)
			answers, _ := result["answers"].(map[string]any)
			if answers["第一问?"] == nil || answers["第二问?"] == nil {
				t.Fatalf("answers = %+v, want both questions keyed by text", answers)
			}
			// One resolved pair per question id, both faces, interleaved
			// canonical-then-legacy.
			got := map[string]string{}
			canon := map[string]string{}
			for i := 0; i < 2; i++ {
				cr := awaitEventOfType(t, sess, core.EventUserInputResolved)
				lr := awaitEventOfType(t, sess, core.EventQuestionResolved)
				canon[cr.ItemID] = string(cr.UserInput.Status)
				got[lr.QuestionID] = lr.Content
			}
			if got["call_multi"] != "accepted" || got["call_multi#1"] != "accepted" || len(got) != 2 {
				t.Fatalf("legacy resolved map = %v, want both accepted", got)
			}
			if canon["call_multi"] != "answered" || canon["call_multi#1"] != "answered" || len(canon) != 2 {
				t.Fatalf("canonical resolved map = %v, want both answered", canon)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("complete answer never flushed")
}

// ── leader rail: subscriber answer write path ──────────────────────────────

// runLeaderSubscriberAnswer is runLeaderSubscriber plus an act hook that runs
// in the test goroutine once the REQUEST has been registered (polls the
// registry). The script learns act finished via the answered channel.
func runLeaderSubscriberAnswer(t *testing.T, script func(c net.Conn, answered <-chan struct{}) error, act func(sub *LeaderSubscriber)) ([]core.Event, *LeaderSubscriber) {
	t.Helper()
	sock := filepath.Join("/tmp", fmt.Sprintf("cc-grok-leader-%d.sock", time.Now().UnixNano()))
	defer os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var got []core.Event
	var mu sync.Mutex
	onEvent := func(ev core.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}
	answered := make(chan struct{})
	serverErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()
		if err := leaderHandshake(c); err != nil {
			serverErr <- err
			return
		}
		serverErr <- script(c, answered)
	}()

	sub := NewLeaderSubscriber(sock, "sess-1", "/tmp")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = sub.Run(ctx, onEvent) }()

	// Wait for registration, run the act, then let the script finish.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sub.interactions != nil && sub.interactions.len() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	act(sub)
	close(answered)
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("subscriber Run did not return")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("mock leader server: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return got, sub
}

// readClientACPFrame reads one leader frame from the client and parses its
// "acp" envelope payload as generic JSON.
func readClientACPFrame(t *testing.T, c net.Conn) map[string]any {
	t.Helper()
	frame, err := readTestFrame(c)
	if err != nil {
		t.Fatalf("read client frame: %v", err)
	}
	var m leaderClientMsg
	if err := json.Unmarshal(frame, &m); err != nil || m.Type != "acp" {
		t.Fatalf("client frame = %s, want acp envelope", frame)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(m.Payload), &payload); err != nil {
		t.Fatalf("parse acp payload %q: %v", m.Payload, err)
	}
	return payload
}

// countEventTypes tallies dual-track emissions by type.
func countEventTypes(events []core.Event) map[core.EventType]int {
	m := map[core.EventType]int{}
	for _, e := range events {
		m[e.Type]++
	}
	return m
}

func TestLeaderSubscriberAnswerQuestion(t *testing.T) {
	got, _ := runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
		if err := writeACPRequestRaw(c, fixtureAskUserQuestionHalfWrapped); err != nil {
			return err
		}
		<-answered
		resp := readClientACPFrame(t, c)
		if fmt.Sprint(resp["id"]) != "0" {
			t.Errorf("answer id = %v, want original numeric id 0", resp["id"])
		}
		result, _ := resp["result"].(map[string]any)
		if result == nil || result["outcome"] != "accepted" {
			t.Errorf("result = %+v, want outcome accepted", result)
		}
		answers, _ := result["answers"].(map[string]any)
		v, _ := answers["你偏好哪种配色主题?"].([]any)
		if len(v) != 1 || v[0] != "深色主题" {
			t.Errorf("answers = %+v, want {你偏好哪种配色主题?: [深色主题]}", answers)
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	}, func(sub *LeaderSubscriber) {
		resolved, err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", []string{"深色主题"}, "")
		if err != nil || !resolved {
			t.Errorf("AnswerQuestion = (%v, %v), want (true, nil)", resolved, err)
		}
		if sub.interactions.len() != 0 {
			t.Errorf("registry len = %d after flush, want 0", sub.interactions.len())
		}
	})
	// Dual-track ask (canonical + legacy) and flush-time dual-track resolved.
	counts := countEventTypes(got)
	if counts[core.EventUserInputRequested] != 1 || counts[core.EventQuestionAsked] != 1 {
		t.Fatalf("ask counts = %v, want 1 canonical + 1 legacy", counts)
	}
	if counts[core.EventUserInputResolved] != 1 || counts[core.EventQuestionResolved] != 1 {
		t.Fatalf("resolved counts = %v, want 1 canonical + 1 legacy", counts)
	}
	for _, e := range got {
		if e.Type == core.EventUserInputResolved && (e.UserInput == nil || e.UserInput.Status != core.UserInputStatusAnswered || e.UserInput.ResolutionSource != "ios") {
			t.Fatalf("canonical resolved = %+v, want answered/ios", e)
		}
	}
}

// TestLeaderSubscriberAnswerQuestionFreeform: a typed answer submits the
// freeform wire shape — label "Other" with the text in annotations notes.
func TestLeaderSubscriberAnswerQuestionFreeform(t *testing.T) {
	got, _ := runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
		if err := writeACPRequestRaw(c, fixtureAskUserQuestionHalfWrapped); err != nil {
			return err
		}
		<-answered
		resp := readClientACPFrame(t, c)
		result, _ := resp["result"].(map[string]any)
		if result == nil || result["outcome"] != "accepted" {
			t.Errorf("result = %+v, want outcome accepted", result)
		}
		answers, _ := result["answers"].(map[string]any)
		if v, _ := answers["你偏好哪种配色主题?"].([]any); len(v) != 1 || v[0] != "Other" {
			t.Errorf("answers = %+v, want {你偏好哪种配色主题?: [Other]}", answers)
		}
		anns, _ := result["annotations"].(map[string]any)
		ann, _ := anns["你偏好哪种配色主题?"].(map[string]any)
		if ann == nil || ann["notes"] != "我要自定义" {
			t.Errorf("annotations = %+v, want {你偏好哪种配色主题?: {notes: 我要自定义}}", anns)
		}
		return nil
	}, func(sub *LeaderSubscriber) {
		resolved, err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", []string{"Other"}, "我要自定义")
		if err != nil || !resolved {
			t.Errorf("freeform AnswerQuestion = (%v, %v), want (true, nil)", resolved, err)
		}
	})
	counts := countEventTypes(got)
	if counts[core.EventUserInputResolved] != 1 {
		t.Fatalf("resolved counts = %v, want flush", counts)
	}
}

// "Other" without notes stays invalid (the TUI never submits an empty
// freeform selection).
func TestLeaderSubscriberOtherWithoutNotesRejected(t *testing.T) {
	runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
		if err := writeACPRequestRaw(c, fixtureAskUserQuestionHalfWrapped); err != nil {
			return err
		}
		<-answered
		time.Sleep(100 * time.Millisecond)
		return nil
	}, func(sub *LeaderSubscriber) {
		if _, err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", []string{"Other"}, ""); err == nil {
			t.Error("Other without notes must be rejected")
		}
		if sub.interactions.len() != 1 {
			t.Errorf("rejected freeform must not consume the entry; len = %d", sub.interactions.len())
		}
	})
}

func TestLeaderSubscriberCancelQuestion(t *testing.T) {
	got, _ := runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
		if err := writeACPRequestRaw(c, fixtureAskUserQuestionHalfWrapped); err != nil {
			return err
		}
		<-answered
		resp := readClientACPFrame(t, c)
		if fmt.Sprint(resp["id"]) != "0" {
			t.Errorf("cancel id = %v, want 0", resp["id"])
		}
		result, _ := resp["result"].(map[string]any)
		if result == nil || result["outcome"] != "cancelled" {
			t.Errorf("result = %+v, want outcome cancelled", result)
		}
		if _, has := result["answers"]; has {
			t.Errorf("cancelled must not carry answers: %+v", result)
		}
		return nil
	}, func(sub *LeaderSubscriber) {
		resolved, err := sub.CancelQuestion("call_410dc27a15f64707b7f36ca2")
		if err != nil || !resolved {
			t.Errorf("CancelQuestion = (%v, %v), want (true, nil)", resolved, err)
		}
		if sub.interactions.len() != 0 {
			t.Errorf("registry len = %d after cancel, want 0", sub.interactions.len())
		}
	})
	counts := countEventTypes(got)
	if counts[core.EventUserInputResolved] != 1 || counts[core.EventQuestionResolved] != 1 {
		t.Fatalf("resolved counts = %v, want 1 canonical + 1 legacy", counts)
	}
	for _, e := range got {
		if e.Type == core.EventUserInputResolved && (e.UserInput == nil || e.UserInput.Status != core.UserInputStatusRejected) {
			t.Fatalf("canonical resolved = %+v, want rejected", e)
		}
	}
}

func TestLeaderSubscriberAnswerValidationAndSilent(t *testing.T) {
	got, _ := runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
		if err := writeACPRequestRaw(c, fixtureAskUserQuestionHalfWrapped); err != nil {
			return err
		}
		<-answered
		// The act path answers with a valid label; give the write a moment.
		time.Sleep(150 * time.Millisecond)
		return nil
	}, func(sub *LeaderSubscriber) {
		if _, err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", []string{"不存在的选项"}, ""); err == nil {
			t.Error("invalid label must error")
		}
		if _, err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", nil, ""); err == nil {
			t.Error("empty selection must error")
		}
		if _, err := sub.AnswerQuestion("call_never_seen", []string{"深色主题"}, ""); err == nil {
			t.Error("never-registered id must error")
		}
		if sub.interactions.len() != 1 {
			t.Errorf("failed validations must not consume the entry; len = %d", sub.interactions.len())
		}
		resolved, err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", []string{"浅色主题"}, "")
		if err != nil || !resolved {
			t.Errorf("valid answer = (%v, %v), want (true, nil)", resolved, err)
		}
		// Duplicate (already flushed) — silent per §3.5.
		resolved, err = sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", []string{"浅色主题"}, "")
		if err != nil || !resolved {
			t.Errorf("late answer = (%v, %v), want silent (true, nil)", resolved, err)
		}
	})
	counts := countEventTypes(got)
	if counts[core.EventQuestionAsked] != 1 || counts[core.EventUserInputRequested] != 1 {
		t.Fatalf("ask counts = %v, want exactly 1 each", counts)
	}
	if counts[core.EventQuestionResolved] != 1 || counts[core.EventUserInputResolved] != 1 {
		t.Fatalf("resolved counts = %v, want exactly 1 flush (failed validations silent)", counts)
	}
}

// TestAgentRespondSessionQuestionRouting: agent-level responder routes to the
// registered live subscriber and errors cleanly without one.
func TestAgentRespondSessionQuestionRouting(t *testing.T) {
	a := &Agent{liveSubs: make(map[string]*LeaderSubscriber)}
	if err := a.RespondSessionQuestion(context.Background(), "sess-x", "q1", []string{"A"}); err == nil {
		t.Fatal("no live subscriber must error")
	}

	sub := NewLeaderSubscriber("", "sess-x", "/tmp")
	sub.interactions.put(leaderInteraction{
		wireID:     3,
		toolCallID: "call_route",
		params: askUserQuestionParams{
			SessionID:  "sess-x",
			ToolCallID: "call_route",
			Questions: []askUserQuestionItem{{
				Question: "路由?",
				Options:  []askUserQuestionOption{{Label: "A"}},
			}},
			Mode: "default",
		},
	})
	a.liveSubsMu.Lock()
	a.liveSubs["sess-x"] = sub
	a.liveSubsMu.Unlock()

	// Consumed tombstone path: answer after eviction is silent (no socket
	// needed — it must NOT attempt a write).
	sub.interactions.take("call_route")
	if err := a.RespondSessionQuestion(context.Background(), "sess-x", "call_route", []string{"A"}); err != nil {
		t.Fatalf("routed late answer must be silent, got %v", err)
	}
}

// ── resolve_user_input (v2 structured face) ────────────────────────────────

func TestGrokSessionResolveUserInputDriverRail(t *testing.T) {
	sess, lines, mu, stop := startQuestionProbeAgent(t, driverQuestionFixture)
	defer stop()
	awaitEventOfType(t, sess, core.EventUserInputRequested)
	awaitEventOfType(t, sess, core.EventQuestionAsked)

	// Valid option answer: accepted/answered, canonical resolved emitted.
	res, err := sess.ResolveUserInput(context.Background(), "call_drv1", "11111111-1111-4111-8111-111111111111", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: "call_drv1", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "A"}}}})
	if err != nil || res.Outcome != core.UserInputOutcomeAccepted || res.CurrentStatus != core.UserInputStatusAnswered {
		t.Fatalf("answer resolution = (%+v, %v), want accepted/answered", res, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m := lastAgentResponse(t, lines, mu); m != nil && fmt.Sprint(m["id"]) == "5" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Repeat submission: consumed → already_resolved, no extra agent frames.
	res, err = sess.ResolveUserInput(context.Background(), "call_drv1", "22222222-2222-4222-8222-222222222222", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: "call_drv1", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "B"}}}})
	if err != nil || res.Outcome != core.UserInputOutcomeAlreadyResolved {
		t.Fatalf("repeat resolution = (%+v, %v), want already_resolved", res, err)
	}

	// Reject on the same interaction stays already_resolved (consumed).
	res, err = sess.ResolveUserInput(context.Background(), "call_drv1", "33333333-3333-4333-8333-333333333333", core.UserInputActionReject, nil)
	if err != nil || res.Outcome != core.UserInputOutcomeAlreadyResolved {
		t.Fatalf("late reject resolution = (%+v, %v), want already_resolved", res, err)
	}
}

// TestGrokSessionResolveUserInputFreeformText: a typed answer (iOS "type your
// answer here" equivalent) maps to grok's freeform wire shape — label "Other"
// with the text in annotations notes (types.rs AskUserQuestionExtResponse).
func TestGrokSessionResolveUserInputFreeformText(t *testing.T) {
	sess, lines, mu, stop := startQuestionProbeAgent(t, driverQuestionFixture)
	defer stop()
	awaitEventOfType(t, sess, core.EventUserInputRequested)

	res, err := sess.ResolveUserInput(context.Background(), "call_drv1", "88888888-8888-4888-8888-888888888888", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: "call_drv1", Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: " 我要自定义答案 "}}}})
	if err != nil || res.Outcome != core.UserInputOutcomeAccepted || res.CurrentStatus != core.UserInputStatusAnswered {
		t.Fatalf("freeform resolution = (%+v, %v), want accepted/answered", res, err)
	}
	var resp map[string]any
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m := lastAgentResponse(t, lines, mu); m != nil && fmt.Sprint(m["id"]) == "5" {
			resp = m
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if resp == nil {
		t.Fatal("agent never received the freeform response")
	}
	result, _ := resp["result"].(map[string]any)
	if result == nil || result["outcome"] != "accepted" {
		t.Fatalf("result = %+v, want outcome accepted", result)
	}
	answers, _ := result["answers"].(map[string]any)
	if v, _ := answers["选一个?"].([]any); len(v) != 1 || v[0] != "Other" {
		t.Fatalf("answers = %+v, want {选一个?: [Other]}", answers)
	}
	anns, _ := result["annotations"].(map[string]any)
	ann, _ := anns["选一个?"].(map[string]any)
	if ann == nil || ann["notes"] != "我要自定义答案" {
		t.Fatalf("annotations = %+v, want {选一个?: {notes: 我要自定义答案}} (text trimmed)", anns)
	}
}

func TestGrokSessionResolveUserInputReject(t *testing.T) {
	sess, lines, mu, stop := startQuestionProbeAgent(t, driverQuestionFixture)
	defer stop()
	awaitEventOfType(t, sess, core.EventUserInputRequested)

	res, err := sess.ResolveUserInput(context.Background(), "call_drv1", "44444444-4444-4444-8444-444444444444", core.UserInputActionReject, nil)
	if err != nil || res.Outcome != core.UserInputOutcomeAccepted || res.CurrentStatus != core.UserInputStatusRejected {
		t.Fatalf("reject resolution = (%+v, %v), want accepted/rejected", res, err)
	}
	resEv := awaitEventOfType(t, sess, core.EventUserInputResolved)
	if resEv.UserInput == nil || resEv.UserInput.Status != core.UserInputStatusRejected {
		t.Fatalf("canonical resolved = %+v, want rejected", resEv)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m := lastAgentResponse(t, lines, mu); m != nil && fmt.Sprint(m["id"]) == "5" {
			result, _ := m["result"].(map[string]any)
			if result == nil || result["outcome"] != "cancelled" {
				t.Fatalf("result = %+v, want cancelled", result)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("agent never received the cancel response")
}

func TestGrokSessionResolveUserInputMissFallsBackToAgent(t *testing.T) {
	sess, _, _, stop := startQuestionProbeAgent(t, driverQuestionFixture)
	defer stop()
	awaitEventOfType(t, sess, core.EventUserInputRequested)

	// Unknown interaction: driver rail misses → agent-level fallback → no
	// registered owner → interaction_not_found (stable code).
	_, err := sess.ResolveUserInput(context.Background(), "call_unknown", "55555555-5555-4555-8555-555555555555", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: "call_unknown", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "A"}}}})
	var uie *core.UserInputError
	if !errorsAs(err, &uie) || uie.Code != "interaction_not_found" {
		t.Fatalf("miss err = %v, want interaction_not_found", err)
	}
}

func TestAgentResolveUserInputLeaderRouting(t *testing.T) {
	a := &Agent{liveSubs: make(map[string]*LeaderSubscriber)}

	// No registered owner → interaction_not_found.
	_, err := a.ResolveUserInput(context.Background(), "call_route", "66666666-6666-4666-8666-666666666666", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: "call_route", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "A"}}}})
	var uie *core.UserInputError
	if !errorsAs(err, &uie) || uie.Code != "interaction_not_found" {
		t.Fatalf("no-owner err = %v, want interaction_not_found", err)
	}

	// Owner registered (leader rail surfaced it) but subscriber gone → same
	// stable code.
	a.trackQuestionOwner("call_route", "sess-x")
	_, err = a.ResolveUserInput(context.Background(), "call_route", "66666666-6666-4666-8666-666666666666", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: "call_route", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "A"}}}})
	if !errorsAs(err, &uie) || uie.Code != "interaction_not_found" {
		t.Fatalf("no-subscriber err = %v, want interaction_not_found", err)
	}

	// Live subscriber with a two-question interaction: one answer →
	// in_progress (wire response not flushed yet).
	sub := NewLeaderSubscriber("", "sess-x", "/tmp")
	sub.interactions.put(leaderInteraction{
		wireID:     3,
		toolCallID: "call_route",
		params: askUserQuestionParams{
			SessionID:  "sess-x",
			ToolCallID: "call_route",
			Questions: []askUserQuestionItem{{
				Question: "第一问?",
				Options:  []askUserQuestionOption{{Label: "A"}},
			}, {
				Question: "第二问?",
				Options:  []askUserQuestionOption{{Label: "B"}},
			}},
			Mode: "default",
		},
	})
	a.liveSubsMu.Lock()
	a.liveSubs["sess-x"] = sub
	a.liveSubsMu.Unlock()

	res, err := a.ResolveUserInput(context.Background(), "call_route", "66666666-6666-4666-8666-666666666666", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: "call_route", Values: []core.UserInputValue{{Kind: core.UserInputValueOption, OptionID: "A"}}}})
	if err != nil || res.Outcome != core.UserInputOutcomeInProgress || res.CurrentStatus != core.UserInputStatusPending {
		t.Fatalf("partial resolution = (%+v, %v), want in_progress/pending", res, err)
	}

	// Freeform text on the leader rail is now a legal answer (TUI "type your
	// answer here" shape): the second question completes the interaction, so
	// the flush proceeds — this sub has no live connection, so the failure is
	// the connection error, NOT invalid_answer_shape.
	_, err = a.ResolveUserInput(context.Background(), "call_route", "77777777-7777-4777-8777-777777777777", core.UserInputActionAnswer,
		[]core.UserInputAnswer{{QuestionID: "call_route#1", Values: []core.UserInputValue{{Kind: core.UserInputValueText, Text: "自定义"}}}})
	if errorsAs(err, &uie) && uie.Code == "invalid_answer_shape" {
		t.Fatalf("freeform text err = %v, text answers are legal now", err)
	}
	if err == nil {
		t.Fatal("freeform flush without a live connection must still error")
	}
}

// errorsAs is errors.As with the pointer-to-pointer pattern kept local so the
// test file reads cleanly.
func errorsAs(err error, target **core.UserInputError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*core.UserInputError); ok {
		*target = e
		return true
	}
	return false
}
