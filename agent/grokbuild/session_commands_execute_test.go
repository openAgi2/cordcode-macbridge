package grokbuild

// session_commands_execute_test.go — p1b 共用 turn dispatcher 的定向测试
// （方案 §8：单次结算、terminal future、stopReason 保留、取消与清理、
// EOF 不成功、operation lease、sessionId 贯通）。
//
// 层次：turnDispatch 单元 → liveSessions 注册表 → grokSession e2e（脚本化
// ACP peer，session/prompt 走真实 writeRequest/readLoop/handleResponse 路径）。
// 官方 wire 形状由 phase0 p6-turns.json + validate_samples.py 守护；这里的
// peer 只复刻那些已取证的形状。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// --- turnDispatch 单元（镜像 TurnReportSlot/FinalizationGate 不变量） ---

func TestTurnDispatchSettleOnce(t *testing.T) {
	var d turnDispatch
	epoch, wait, err := d.promote(7)
	if err != nil {
		t.Fatal(err)
	}
	if !d.settle(epoch, turnOutcome{StopReason: stopReasonEndTurn}) {
		t.Fatal("first settle must win")
	}
	if d.settle(epoch, turnOutcome{StopReason: stopReasonCancelled}) {
		t.Fatal("second settle for the same turn must be dropped")
	}
	select {
	case out := <-wait:
		if out.StopReason != stopReasonEndTurn {
			t.Fatalf("outcome = %+v", out)
		}
	default:
		t.Fatal("waiter must receive the settled outcome")
	}
	if d.activeReqID() != 0 {
		t.Fatal("slot must be free after settle")
	}

	// Re-promote bumps the epoch: a stale settle from the previous turn loses.
	epoch2, _, err := d.promote(8)
	if err != nil {
		t.Fatal(err)
	}
	if epoch2 == epoch {
		t.Fatal("epoch must advance per promote")
	}
	if d.settle(epoch, turnOutcome{StopReason: stopReasonEndTurn}) {
		t.Fatal("stale-epoch settle must be dropped")
	}
	if !d.settle(epoch2, turnOutcome{StopReason: stopReasonCancelled}) {
		t.Fatal("live-epoch settle must win")
	}
}

func TestTurnDispatchSettleForRequestStaleID(t *testing.T) {
	var d turnDispatch
	if _, _, err := d.promote(5); err != nil {
		t.Fatal(err)
	}
	if d.settleForRequest(4, turnOutcome{StopReason: stopReasonEndTurn}) {
		t.Fatal("response for a different request id must not settle the live turn")
	}
	if d.activeReqID() != 5 {
		t.Fatal("turn must still be in flight")
	}
	if !d.settleForRequest(5, turnOutcome{StopReason: stopReasonEndTurn}) {
		t.Fatal("matching id must settle")
	}
}

func TestTurnDispatchLeaseBusy(t *testing.T) {
	var d turnDispatch
	if _, _, err := d.promote(1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.promote(2); !errors.Is(err, errTurnBusy) {
		t.Fatalf("second promote must be lease-rejected, got %v", err)
	}
}

func TestTurnDispatchHostTextCollection(t *testing.T) {
	var d turnDispatch
	// No turn in flight: collection is a no-op.
	d.collectHostText("orphan")
	epoch, wait, err := d.promote(3)
	if err != nil {
		t.Fatal(err)
	}
	d.collectHostText("Loaded hooks (9):\n")
	d.collectHostText("  global/settings:…")
	d.settle(epoch, turnOutcome{StopReason: stopReasonEndTurn})
	out := <-wait
	if !strings.Contains(out.HostText, "Loaded hooks (9)") || !strings.Contains(out.HostText, "global/settings") {
		t.Fatalf("host text not carried on settle: %q", out.HostText)
	}
	// After settle the collector is closed to stragglers.
	d.collectHostText("late chunk")
	if d.activeReqID() != 0 {
		t.Fatal("slot must be free")
	}
}

func TestParseHostTurnChunkShapes(t *testing.T) {
	// 1.0.13 实测：hostTurn 戳在 update 级 _meta（p6 A 样本）。
	host := `{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Loaded hooks (9)"},"_meta":{"hostTurn":true}},"_meta":{"promptId":"p1","totalTokens":0}}`
	if text, ok := parseHostTurnChunk([]byte(host)); !ok || text != "Loaded hooks (9)" {
		t.Fatalf("hostTurn chunk parse = %q,%v", text, ok)
	}
	// 模型正文（无 hostTurn 戳）：不是 host 反馈。
	model := `{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"model text"}},"_meta":{"promptId":"p1"}}`
	if _, ok := parseHostTurnChunk([]byte(model)); ok {
		t.Fatal("model chunk must not be treated as host feedback")
	}
	// transport 级 _meta 带 hostTurn 也不算（1.0.13 戳在 update 级）。
	wrongLayer := `{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}},"_meta":{"hostTurn":true}}`
	if _, ok := parseHostTurnChunk([]byte(wrongLayer)); ok {
		t.Fatal("transport-level hostTurn must not count")
	}
}

// --- liveSessions 注册表（sessionId 贯通 / CAS 撤销） ---

func TestLiveSessionsRegistry(t *testing.T) {
	l := newLiveSessions()
	s1 := &grokSession{}
	s1.alive.Store(true)
	if !l.register("sid", s1) {
		t.Fatal("register must succeed on empty registry")
	}
	s2 := &grokSession{}
	s2.alive.Store(true)
	if l.register("sid", s2) {
		t.Fatal("a live actor must not be displaced")
	}
	if got, ok := l.get("sid"); !ok || got != s1 {
		t.Fatal("get must return the live actor")
	}
	// Dead actor: get refuses, replacement register wins.
	s1.alive.Store(false)
	if _, ok := l.get("sid"); ok {
		t.Fatal("get must refuse a dead actor")
	}
	if !l.register("sid", s2) {
		t.Fatal("register must replace a dead actor")
	}
	// CAS unregister: stale actor cannot evict the replacement.
	l.unregister("sid", s1)
	if got, _ := l.get("sid"); got != s2 {
		t.Fatal("stale unregister must not evict the replacement")
	}
	l.unregister("sid", s2)
	if _, ok := l.get("sid"); ok {
		t.Fatal("unregister must remove the current actor")
	}
}

// --- grokSession e2e：脚本化 ACP peer ---

// turnScriptPeer answers session/prompt with the P6-evidenced frame sequence.
type turnScriptPeer struct {
	hostText       string // hostTurn agent_message_chunk body ("" = none)
	promptID       string // _meta.promptId (chunk + response)
	stopReason     string // response result.stopReason
	cancelCategory string // response _meta.cancellationCategory
	selfCancel     bool   // emit session/cancel notification before responding
	holdResponse   bool   // never respond (EOF test closes the pipe instead)
	goalObjective  string // emit a durable goal_created update before any response
}

// startTurnScriptPeer spawns the peer; stop closes both ends and waits.
func startTurnScriptPeer(t *testing.T, script turnScriptPeer) (stdinW io.WriteCloser, stdoutR io.ReadCloser, stop func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer close(done)
		defer wg.Done()
		defer outW.Close()
		sc := bufio.NewScanner(inR)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil || req.Method != "session/prompt" {
				continue
			}
			if script.hostText != "" {
				chunk, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": "sess-1",
						"update": map[string]any{
							"sessionUpdate": "agent_message_chunk",
							"content":       map[string]any{"type": "text", "text": script.hostText},
							"_meta":         map[string]any{"hostTurn": true},
						},
						"_meta": map[string]any{"promptId": script.promptID},
					},
				})
				_, _ = outW.Write(append(chunk, '\n'))
			}
			if script.goalObjective != "" {
				goal, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "method": "_x.ai/session/update",
					"params": map[string]any{"sessionId": "sess-1", "update": map[string]any{
						"sessionUpdate": "goal_updated", "goal_id": "goal-1", "objective": script.goalObjective,
						"status": "active", "last_event": "goal_created",
					}, "_meta": map[string]any{"agentTimestampMs": int64(1234)}},
				})
				_, _ = outW.Write(append(goal, '\n'))
			}
			if script.selfCancel {
				cancel, _ := json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/cancel",
					"params":  map[string]any{"sessionId": "sess-1"},
				})
				_, _ = outW.Write(append(cancel, '\n'))
			}
			if script.holdResponse {
				continue
			}
			meta := map[string]any{"promptId": script.promptID, "totalTokens": 12, "inputTokens": 10, "outputTokens": 2}
			if script.cancelCategory != "" {
				meta["cancellationCategory"] = script.cancelCategory
			}
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"result":  map[string]any{"stopReason": script.stopReason, "_meta": meta},
			})
			_, _ = outW.Write(append(resp, '\n'))
		}
	}()
	stop = func() {
		_ = inW.Close()
		_ = inR.Close()
		<-done
	}
	return inW, outR, stop
}

// newTurnTestSession wires a grokSession onto a script peer with the real
// readLoop running. Returns the session, the RAW peer stop (EOF tests call
// this mid-test so the readLoop-exit abandon is what settles), and shut (the
// full teardown for defers).
func newTurnTestSession(t *testing.T, script turnScriptPeer) (*grokSession, func(), func()) {
	t.Helper()
	stdinW, stdoutR, stop := startTurnScriptPeer(t, script)
	ctx, cancel := context.WithCancel(context.Background())
	s := &grokSession{
		agent:        &Agent{},
		stdin:        stdinW,
		stdout:       stdoutR,
		events:       make(chan core.Event, 64),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	s.alive.Store(true)
	s.sessionID.Store("sess-1")
	go s.readLoop()
	// Test teardown: literal sessions have no cmd.Wait watcher, so Close()
	// would block forever on s.done. Settle without emitting (consumers are
	// gone), cancel (unblocks any emit), then drop the peer.
	shut := func() {
		s.turn.settleActive(turnOutcome{Err: errTurnActorDead})
		s.cancel()
		stop()
	}
	return s, stop, shut
}

// nextTerminal drains events until the single Done terminal arrives.
func nextTerminal(t *testing.T, events <-chan core.Event) core.Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Done {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for the terminal event")
			return core.Event{}
		}
	}
}

func TestExecuteHostCommandEndTurn(t *testing.T) {
	s, _, shut := newTurnTestSession(t, turnScriptPeer{
		hostText:   "Loaded hooks (9):\n  global/settings:session_start[0]…",
		promptID:   "p-uuid-1",
		stopReason: "end_turn",
	})
	defer shut()

	a := &Agent{acu: newACUSideState()}
	a.registerLiveSession("sess-1", s)
	// resolveSessionCwd falls back to workDir when no on-disk session exists.
	dir := t.TempDir()
	a.SetWorkDir(dir)
	a.acu.storeListSuccess("sess-1", dir, []core.SessionCommand{{Name: "compact"}})

	res, err := a.ExecuteSessionCommand(context.Background(), "sess-1", "/compact")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ResultKind != "success" {
		t.Fatalf("kind = %q", res.ResultKind)
	}
	if !strings.Contains(res.ResultText, "Loaded hooks (9)") {
		t.Fatalf("resultText = %q, want the official hostTurn body", res.ResultText)
	}
	// Terminal event preserved the stop reason and the tokens from _meta.
	term := nextTerminal(t, s.events)
	if term.Type != core.EventResult || term.StopReason != "end_turn" {
		t.Fatalf("terminal = %+v", term)
	}
	if term.InputTokens != 10 || term.OutputTokens != 2 {
		t.Fatalf("tokens = %d/%d", term.InputTokens, term.OutputTokens)
	}
	// Exactly one terminal: the next event read must time out (nothing else
	// is pending beyond the streamed ones already consumed by nextTerminal).
	select {
	case ev := <-s.events:
		if ev.Done {
			t.Fatalf("double terminal: %+v", ev)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

func TestExecuteGoalAcceptsMultilineObjective(t *testing.T) {
	s, _, shut := newTurnTestSession(t, turnScriptPeer{
		hostText:   "Goal updated.",
		promptID:   "p-uuid-goal",
		stopReason: "end_turn",
	})
	defer shut()

	a := &Agent{acu: newACUSideState()}
	a.registerLiveSession("sess-1", s)
	dir := t.TempDir()
	a.SetWorkDir(dir)
	a.acu.storeListSuccess("sess-1", dir, []core.SessionCommand{{Name: "goal"}})

	res, err := a.ExecuteSessionCommand(context.Background(), "sess-1", "/goal ship the fix\n/tmp/demo-plan121.txt")
	if err != nil {
		t.Fatalf("multiline /goal Execute: %v", err)
	}
	if res.ResultKind != "success" || res.ResultText != "Goal updated." {
		t.Fatalf("result = %+v, want official success", res)
	}
}

func TestExecuteGoalReturnsAtCreationAndLeavesGoalTurnRunning(t *testing.T) {
	s, _, shut := newTurnTestSession(t, turnScriptPeer{goalObjective: "ship it", promptID: "p-goal", holdResponse: true})
	defer shut()
	s.updateState = newGrokUpdateState()
	a := &Agent{acu: newACUSideState()}
	a.registerLiveSession("sess-1", s)
	dir := t.TempDir()
	a.SetWorkDir(dir)
	a.acu.storeListSuccess("sess-1", dir, []core.SessionCommand{{Name: "goal"}})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := a.ExecuteSessionCommand(ctx, "sess-1", "/goal ship it")
	if err != nil || res.ResultKind != "success" || res.ResultText != "ship it" {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	if s.turn.activeReqID() == 0 {
		t.Fatal("goal turn lease was released at acknowledgement; background execution would be orphaned")
	}
	foundCommand := false
	deadline := time.After(time.Second)
	for !foundCommand {
		select {
		case ev := <-s.events:
			if ev.Type == core.EventSessionCommand && ev.SessionCommand != nil && ev.SessionCommand.InputLine == "/goal ship it" {
				foundCommand = true
			}
		case <-deadline:
			t.Fatal("goal acknowledgement did not emit the reusable command projection")
		}
	}
}

func TestSendPreservesCancelledStopReason(t *testing.T) {
	s, _, shut := newTurnTestSession(t, turnScriptPeer{
		promptID:       "p-uuid-2",
		stopReason:     "cancelled",
		cancelCategory: "MidTurnAbort",
	})
	defer shut()

	if err := s.Send("count slowly", nil, nil); err != nil {
		t.Fatal(err)
	}
	term := nextTerminal(t, s.events)
	if term.Type != core.EventResult {
		t.Fatalf("terminal = %+v", term)
	}
	if term.StopReason != "cancelled" || term.CancellationCategory != "MidTurnAbort" {
		t.Fatalf("stopReason/category = %q/%q", term.StopReason, term.CancellationCategory)
	}
}

func TestAgentSelfCancelSettlesExactlyOnce(t *testing.T) {
	// The agent emits session/cancel BEFORE the prompt response; the
	// notification settles the slot and the late response must not produce a
	// second terminal (settle-once across rails).
	s, _, shut := newTurnTestSession(t, turnScriptPeer{
		promptID:   "p-uuid-3",
		stopReason: "cancelled",
		selfCancel: true,
	})
	defer shut()

	if err := s.Send("work", nil, nil); err != nil {
		t.Fatal(err)
	}
	term := nextTerminal(t, s.events)
	if term.StopReason != "cancelled" {
		t.Fatalf("terminal = %+v", term)
	}
	// The scripted response arrives after the notification; give it time to
	// be processed, then assert no second terminal appeared.
	time.Sleep(150 * time.Millisecond)
	select {
	case ev := <-s.events:
		if ev.Done {
			t.Fatalf("late response produced a second terminal: %+v", ev)
		}
	case <-time.After(100 * time.Millisecond):
	}
	if s.turn.activeReqID() != 0 {
		t.Fatal("slot must be free after the notification settle")
	}
}

func TestEOFDoesNotSettleSuccess(t *testing.T) {
	// EOF 不成功（§8）：the peer holds the response, then the pipe closes —
	// the in-flight turn must settle as an actor-death error, never success.
	s, stopPeer, _ := newTurnTestSession(t, turnScriptPeer{promptID: "p-uuid-4", holdResponse: true})
	defer s.cancel() // the EOF settle is the teardown; only the ctx needs closing

	if err := s.Send("hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	// Let the write land, then kill the peer (readLoop sees EOF).
	time.Sleep(100 * time.Millisecond)
	stopPeer()

	term := nextTerminal(t, s.events)
	if term.Type != core.EventError {
		t.Fatalf("EOF with in-flight turn must emit an error terminal, got %+v", term)
	}
	if !errors.Is(term.Error, errTurnActorDead) {
		t.Fatalf("terminal error = %v, want errTurnActorDead", term.Error)
	}
	// The wait channel carries the same settle (an Execute caller would see it).
	// Slot free again; a fresh turn may be dispatched on a replaced actor.
	if s.turn.activeReqID() != 0 {
		t.Fatal("slot must be free after EOF abandon")
	}
}

func TestExecuteEOFReturnsErrorNotHang(t *testing.T) {
	s, stopPeer, shut := newTurnTestSession(t, turnScriptPeer{promptID: "p-uuid-5", holdResponse: true})
	a := &Agent{acu: newACUSideState()}
	a.registerLiveSession("sess-1", s)
	dir := t.TempDir()
	a.SetWorkDir(dir)
	a.acu.storeListSuccess("sess-1", dir, []core.SessionCommand{{Name: "compact"}})

	resCh := make(chan error, 1)
	go func() {
		_, err := a.ExecuteSessionCommand(context.Background(), "sess-1", "/compact")
		resCh <- err
	}()
	time.Sleep(100 * time.Millisecond)
	stopPeer() // peer dies mid-turn

	select {
	case err := <-resCh:
		if err == nil {
			t.Fatal("Execute must fail on actor death, got success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Execute waiter hung on actor death (EOF 不成功 violated)")
	}
	shut()
}

func TestTurnLeaseRejectsSecondDispatch(t *testing.T) {
	s, _, shut := newTurnTestSession(t, turnScriptPeer{promptID: "p-uuid-6", holdResponse: true})
	defer shut()

	if err := s.Send("first", nil, nil); err != nil {
		t.Fatal(err)
	}
	err := s.Send("second", nil, nil)
	if !errors.Is(err, errTurnBusy) {
		t.Fatalf("second Send must be lease-rejected, got %v", err)
	}
	// After a settle the lease frees.
	s.turn.settleActive(turnOutcome{StopReason: stopReasonCancelled})
	if err := s.Send("third", nil, nil); err != nil {
		t.Fatalf("dispatch after settle must be accepted, got %v", err)
	}
}

func TestExecuteRequiresLiveActor(t *testing.T) {
	a := &Agent{acu: newACUSideState()}
	dir := t.TempDir()
	a.SetWorkDir(dir)
	a.acu.storeListSuccess("sess-none", dir, []core.SessionCommand{{Name: "compact"}})
	_, err := a.ExecuteSessionCommand(context.Background(), "sess-none", "/compact")
	if err == nil || !strings.Contains(err.Error(), "no live session actor") {
		t.Fatalf("cold-session execute must fail closed, got %v", err)
	}
}
