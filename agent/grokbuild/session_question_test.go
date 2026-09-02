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

	ev := awaitEvent(t, sess)
	if ev.Type != core.EventQuestionAsked || ev.QuestionID != "call_drv1" || ev.QuestionText != "选一个?" {
		t.Fatalf("event = %+v, want question_asked for call_drv1", ev)
	}
	if len(ev.QuestionOpts) != 2 || ev.QuestionOpts[0].ID != "A" || ev.QuestionOpts[0].Description != "选项A" {
		t.Fatalf("opts = %+v", ev.QuestionOpts)
	}

	if err := sess.RespondQuestion("call_drv1", []string{"A"}); err != nil {
		t.Fatalf("RespondQuestion: %v", err)
	}

	// Driver rail has no leader broadcast — the flush itself closes the card
	// locally via question_resolved (Content = wire outcome).
	resolved := awaitEvent(t, sess)
	if resolved.Type != core.EventQuestionResolved || resolved.QuestionID != "call_drv1" || resolved.Content != "accepted" {
		t.Fatalf("resolved event = %+v, want question_resolved call_drv1 accepted", resolved)
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

	awaitEvent(t, sess)
	if err := sess.RejectQuestion("call_drv1"); err != nil {
		t.Fatalf("RejectQuestion: %v", err)
	}

	resolved := awaitEvent(t, sess)
	if resolved.Type != core.EventQuestionResolved || resolved.QuestionID != "call_drv1" || resolved.Content != "cancelled" {
		t.Fatalf("resolved event = %+v, want question_resolved call_drv1 cancelled", resolved)
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
	awaitEvent(t, sess)

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
	awaitEvent(t, sess)

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

	ev1 := awaitEvent(t, sess)
	if ev1.QuestionID != "call_multi" {
		t.Fatalf("question 0 id = %q, want bare tool_call_id", ev1.QuestionID)
	}
	ev2 := awaitEvent(t, sess)
	if ev2.QuestionID != "call_multi#1" {
		t.Fatalf("question 1 id = %q, want call_multi#1", ev2.QuestionID)
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
			// One resolved event per question id, both accepted.
			r1 := awaitEvent(t, sess)
			r2 := awaitEvent(t, sess)
			got := map[string]string{r1.QuestionID: r1.Content, r2.QuestionID: r2.Content}
			if r1.Type != core.EventQuestionResolved || r2.Type != core.EventQuestionResolved {
				t.Fatalf("resolved types = %+v / %+v", r1, r2)
			}
			if got["call_multi"] != "accepted" || got["call_multi#1"] != "accepted" || len(got) != 2 {
				t.Fatalf("resolved map = %v, want both call_multi and call_multi#1 accepted", got)
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
		if err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", []string{"深色主题"}); err != nil {
			t.Errorf("AnswerQuestion: %v", err)
		}
		if sub.interactions.len() != 0 {
			t.Errorf("registry len = %d after flush, want 0", sub.interactions.len())
		}
	})
	if len(got) != 1 || got[0].Type != core.EventQuestionAsked {
		t.Fatalf("events = %+v, want 1 question_asked", got)
	}
}

func TestLeaderSubscriberCancelQuestion(t *testing.T) {
	_, _ = runLeaderSubscriberAnswer(t, func(c net.Conn, answered <-chan struct{}) error {
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
		if err := sub.CancelQuestion("call_410dc27a15f64707b7f36ca2"); err != nil {
			t.Errorf("CancelQuestion: %v", err)
		}
		if sub.interactions.len() != 0 {
			t.Errorf("registry len = %d after cancel, want 0", sub.interactions.len())
		}
	})
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
		if err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", []string{"不存在的选项"}); err == nil {
			t.Error("invalid label must error")
		}
		if err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", nil); err == nil {
			t.Error("empty selection must error")
		}
		if err := sub.AnswerQuestion("call_never_seen", []string{"深色主题"}); err == nil {
			t.Error("never-registered id must error")
		}
		if sub.interactions.len() != 1 {
			t.Errorf("failed validations must not consume the entry; len = %d", sub.interactions.len())
		}
		if err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", []string{"浅色主题"}); err != nil {
			t.Errorf("valid answer: %v", err)
		}
		// Duplicate (already flushed) — silent per §3.5.
		if err := sub.AnswerQuestion("call_410dc27a15f64707b7f36ca2", []string{"浅色主题"}); err != nil {
			t.Errorf("late answer must be silent, got %v", err)
		}
	})
	if len(got) != 1 {
		t.Fatalf("events = %+v, want exactly 1 question_asked", got)
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
