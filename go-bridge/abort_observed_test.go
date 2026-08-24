package gobridge

// abort_observed_test.go —— P0-3 handleAbortGeneration 观察/被动会话直达
// CancelTurnForThread：registry 缺失不再静默 Ok，按 threadID 直达官方
// turn/interrupt（codex-web 共享 daemon 上 Mac 发起的 turn 由 iOS 停止）。

import (
	"context"
	"errors"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// abortObservedStub 实现 core.ThreadTurnCanceler 的假后端，只记录调用。
type abortObservedStub struct {
	fakeAgent
	cancelCalls int
	lastThread  string
	cancelErr   error
}

func (s *abortObservedStub) CancelTurnForThread(ctx context.Context, threadID string) error {
	s.cancelCalls++
	s.lastThread = threadID
	return s.cancelErr
}

func (s *abortObservedStub) Name() string { return "codex-web" }

// TestAbortObservedThreadDirectCancel registry 缺失 + agent 支持线程直达 →
// 回执 Ok + CancelTurnForThread 恰好一次（threadID 原样）。中断 ACK 不是
// turn/completed；Projection Kernel 必须保持 running，直到官方观察帧到达。
func TestAbortObservedThreadDirectCancel(t *testing.T) {
	h := newTestHandlers(t)
	agent := &abortObservedStub{}
	h.RegisterAgent("codex-web", agent)
	h.projectionKernel.IngestLive(ev(
		1,
		"codex-web",
		"th-observed",
		"turn_started",
		map[string]interface{}{"turnId": "official-turn"},
	))
	before, ok := h.projectionKernel.reducer.Snapshot("codex-web", "th-observed")
	if !ok || before.Execution.Phase != "running" {
		t.Fatalf("precondition: projection = %+v ok=%v, want running", before, ok)
	}

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleAbortGeneration(serverConn, WireMessage{
		BackendID: "codex-web", Method: "abort_generation", RequestID: "r1",
		Params: []byte(`{"sessionId":"th-observed"}`),
	})

	frames := readJSONMaps(t, clientConn, 1)
	if frames[0]["requestId"] != "r1" || frames[0]["ok"] != true {
		t.Fatalf("registry 缺失也须先回 Ok：%v", frames[0])
	}
	if agent.cancelCalls != 1 || agent.lastThread != "th-observed" {
		t.Fatalf("直达 CancelTurnForThread 调用次数/threadID = %d/%q，want 1/th-observed",
			agent.cancelCalls, agent.lastThread)
	}
	after, ok := h.projectionKernel.reducer.Snapshot("codex-web", "th-observed")
	if !ok {
		t.Fatal("interrupt ACK 后 projection 不应消失")
	}
	if after.SyncRev != before.SyncRev || after.Execution.Phase != "running" ||
		after.Execution.ActiveTurnID != "official-turn" {
		t.Fatalf("interrupt ACK 提前改写 projection: before=%+v after=%+v", before, after)
	}
}

// TestAbortObservedThreadCancelFailure 取消失败：Ok 已回执，不 panic、不伪造终态事件。
func TestAbortObservedThreadCancelFailure(t *testing.T) {
	h := newTestHandlers(t)
	agent := &abortObservedStub{cancelErr: context.DeadlineExceeded}
	h.RegisterAgent("codex-web", agent)

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleAbortGeneration(serverConn, WireMessage{
		BackendID: "codex-web", Method: "abort_generation", RequestID: "r1",
		Params: []byte(`{"sessionId":"th-observe-fail"}`),
	})

	frames := readJSONMaps(t, clientConn, 1)
	if frames[0]["ok"] != true {
		t.Fatalf("取消失败仍须回执 Ok：%v", frames[0])
	}
	if agent.cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want 1", agent.cancelCalls)
	}
}

// TestAbortObservedThreadNoSupport agent 不支持 ThreadTurnCanceler：Ok 保持、
// 无取消调用（老后端行为不变），不 panic。
func TestAbortObservedThreadNoSupport(t *testing.T) {
	h := newTestHandlers(t)
	agent := &fakeAgent{name: "opencode-web"}
	h.RegisterAgent("opencode-web", agent)

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleAbortGeneration(serverConn, WireMessage{
		BackendID: "opencode-web", Method: "abort_generation", RequestID: "r1",
		Params: []byte(`{"sessionId":"th-other"}`),
	})

	frames := readJSONMaps(t, clientConn, 1)
	if frames[0]["ok"] != true {
		t.Fatalf("不支持直达取消也须先回 Ok：%v", frames[0])
	}
}

// TestAbortObservedThreadUnknownBackend backend 不存在：Ok 保持，不 panic。
func TestAbortObservedThreadUnknownBackend(t *testing.T) {
	h := newTestHandlers(t)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleAbortGeneration(serverConn, WireMessage{
		BackendID: "missing-backend", Method: "abort_generation", RequestID: "r1",
		Params: []byte(`{"sessionId":"th-unknown"}`),
	})

	frames := readJSONMaps(t, clientConn, 1)
	if frames[0]["ok"] != true {
		t.Fatalf("backend 缺失仍须回执 Ok：%v", frames[0])
	}
}

// TestAbortObservedThreadRegistryStubRoutesToObservedCancel：被动事件泵 markRunning
// 建的 stub 条目（backendID=""、session==nil）曾被 handleAbortGeneration 当作
// 「注册表命中」走删除路径——删掉的只有 stub，daemon 上的 turn 继续跑（2026-08-23
// 真机：Mac 发起的 turn，iOS 停止无效）。stub 必须路由到观察流的直达中断。
func TestAbortObservedThreadRegistryStubRoutesToObservedCancel(t *testing.T) {
	h := newTestHandlers(t)
	agent := &abortObservedStub{}
	h.RegisterAgent("codex-web", agent)

	// 模拟 main.go 被动事件泵：观察会话 turn_started → markRunning 建裸 stub。
	h.sessions.markRunning("th-stubbed")
	if stub, ok := h.sessions.get("th-stubbed"); !ok || stub.session != nil {
		t.Fatalf("precondition: markRunning stub 应存在且无真实 session（got ok=%v session=%v）", ok, stub.session)
	}

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	h.handleAbortGeneration(serverConn, WireMessage{
		BackendID: "codex-web", Method: "abort_generation", RequestID: "r1",
		Params: []byte(`{"sessionId":"th-stubbed"}`),
	})

	frames := readJSONMaps(t, clientConn, 1)
	if frames[0]["requestId"] != "r1" || frames[0]["ok"] != true {
		t.Fatalf("stub 路径也须先回 Ok：%v", frames[0])
	}
	if agent.cancelCalls != 1 || agent.lastThread != "th-stubbed" {
		t.Fatalf("stub 必须路由到 CancelTurnForThread：calls=%d thread=%q want 1/th-stubbed",
			agent.cancelCalls, agent.lastThread)
	}
	// 观察路径不动 registry：stub 保留（被动泵继续拥有该会话的运行态簿记）。
	if stub, ok := h.sessions.get("th-stubbed"); !ok || stub.session != nil {
		t.Fatalf("abortObservedThread 不应删除 stub registry 条目（ok=%v session=%v）", ok, stub.session)
	}
}

// ── 注册表路径（iOS 发起、bridge 持有 AgentSession 的会话）─────────────────

// abortRegistryStubSession 带 TurnCanceler 的注册表假 session：关闭本地会话
// 不会终止共享 daemon 上的 turn，与 codex-web 真实代理一致。
type abortRegistryStubSession struct {
	*fakeAgentSession
	cancelErr   error
	cancelCalls int
}

func (s *abortRegistryStubSession) CancelTurn(ctx context.Context) error {
	s.cancelCalls++
	return s.cancelErr
}

// TestAbortRegistrySharedDaemonCancelFailureKeepsProjectionRunning 共享 daemon
// 会话取消失败（2026-08-24 真机 12:02:38 首次停止）：不得合成
// turn_completed/idle——官方的 turn 仍在继续，投影必须保持 running 等官方收口。
// 2026-08-24 12:55:49 真机后补充：注册表会话与事件 relay 必须保留（不删除、
// 不 Close）——删除会 removeListener，relay 读不到官方收口帧且 agentRelayRunning
// 残留挡住被动泵，官方 turn/completed 无人摄入（12:55:55 真机：停止生效但 iOS
// 永久「执行中」、Mac 成「待执行」stub）。
func TestAbortRegistrySharedDaemonCancelFailureKeepsProjectionRunning(t *testing.T) {
	h := newTestHandlers(t)
	sess := &abortRegistryStubSession{
		fakeAgentSession: &fakeAgentSession{id: "th-reg-cw", events: make(chan core.Event, 8)},
		cancelErr:        errors.New("turn/interrupt rejected"),
	}
	h.putSessionWithMeta("th-reg-cw", "codex-web", "", sess)
	h.projectionKernel.IngestLive(ev(1, "codex-web", "th-reg-cw", "turn_started",
		map[string]interface{}{"turnId": "official-turn"}))
	before, ok := h.projectionKernel.reducer.Snapshot("codex-web", "th-reg-cw")
	if !ok || before.Execution.Phase != "running" {
		t.Fatalf("precondition: projection = %+v ok=%v, want running", before, ok)
	}

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	h.handleAbortGeneration(serverConn, WireMessage{
		BackendID: "codex-web", Method: "abort_generation", RequestID: "r1",
		Params: []byte(`{"sessionId":"th-reg-cw"}`),
	})
	if frames := readJSONMaps(t, clientConn, 1); frames[0]["ok"] != true {
		t.Fatalf("abort 须先回执 Ok：%v", frames[0])
	}
	if sess.cancelCalls != 1 {
		t.Fatalf("CancelTurn calls = %d, want 1", sess.cancelCalls)
	}
	if got, ok := h.getSession("th-reg-cw"); !ok || got == nil || got != sess {
		t.Fatal("共享 daemon abort 必须保留注册表会话（relay 靠它摄入官方 turn/completed）")
	}
	if sess.closed {
		t.Fatal("共享 daemon abort 不得 Close 会话（Close 会 removeListener，relay 变僵尸）")
	}
	after, ok := h.projectionKernel.reducer.Snapshot("codex-web", "th-reg-cw")
	if !ok {
		t.Fatal("projection 不应消失")
	}
	if after.SyncRev != before.SyncRev || after.Execution.Phase != "running" ||
		after.Execution.ActiveTurnID != "official-turn" {
		t.Fatalf("取消失败时不得合成终态: before=%+v after=%+v", before, after)
	}
}

// TestAbortRegistrySharedDaemonCancelSuccessKeepsProjectionRunning 取消失败
// 以外的常态：CancelTurn 成功后同样不得合成终态——官方 turn/completed
// （interrupted）是唯一收口（9cf9287 (b) 规则扫到注册表路径）。
func TestAbortRegistrySharedDaemonCancelSuccessKeepsProjectionRunning(t *testing.T) {
	h := newTestHandlers(t)
	sess := &abortRegistryStubSession{
		fakeAgentSession: &fakeAgentSession{id: "th-reg-cw-ok", events: make(chan core.Event, 8)},
	}
	h.putSessionWithMeta("th-reg-cw-ok", "codex-web", "", sess)
	h.projectionKernel.IngestLive(ev(1, "codex-web", "th-reg-cw-ok", "turn_started",
		map[string]interface{}{"turnId": "official-turn"}))
	before, ok := h.projectionKernel.reducer.Snapshot("codex-web", "th-reg-cw-ok")
	if !ok {
		t.Fatal("precondition: projection missing")
	}

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	h.handleAbortGeneration(serverConn, WireMessage{
		BackendID: "codex-web", Method: "abort_generation", RequestID: "r1",
		Params: []byte(`{"sessionId":"th-reg-cw-ok"}`),
	})
	_ = readJSONMaps(t, clientConn, 1)
	if sess.cancelCalls != 1 {
		t.Fatalf("CancelTurn calls = %d, want 1", sess.cancelCalls)
	}
	if got, ok := h.getSession("th-reg-cw-ok"); !ok || got != sess {
		t.Fatal("共享 daemon abort（成功路径）同样必须保留注册表会话")
	}
	if sess.closed {
		t.Fatal("共享 daemon abort（成功路径）不得 Close 会话")
	}
	after, ok := h.projectionKernel.reducer.Snapshot("codex-web", "th-reg-cw-ok")
	if !ok || after.SyncRev != before.SyncRev || after.Execution.Phase != "running" {
		t.Fatalf("CancelTurn 成功也不得合成终态: before=%+v after=%+v", before, after)
	}
}

// TestAbortRegistryPrivateBackendSyntheticIdlePreserved 私有进程后端（不存在
// 独立官方收口的 daemon）保持既有合成终态：Close 即真实终止，turn_completed →
// idle 由本层收口（回归护栏，防止误伤非共享后端）。
func TestAbortRegistryPrivateBackendSyntheticIdlePreserved(t *testing.T) {
	h := newTestHandlers(t)
	sess := &abortRegistryStubSession{
		fakeAgentSession: &fakeAgentSession{id: "th-reg-ds", events: make(chan core.Event, 8)},
	}
	h.putSessionWithMeta("th-reg-ds", "deepseek", "", sess)
	h.projectionKernel.IngestLive(ev(1, "deepseek", "th-reg-ds", "turn_started",
		map[string]interface{}{"turnId": "official-turn"}))
	before, ok := h.projectionKernel.reducer.Snapshot("deepseek", "th-reg-ds")
	if !ok {
		t.Fatal("precondition: projection missing")
	}

	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	h.handleAbortGeneration(serverConn, WireMessage{
		BackendID: "deepseek", Method: "abort_generation", RequestID: "r1",
		Params: []byte(`{"sessionId":"th-reg-ds"}`),
	})
	_ = readJSONMaps(t, clientConn, 1)
	if got, ok := h.getSession("th-reg-ds"); ok && got == sess {
		t.Fatal("私有后端 abort 必须删除会话（Close 是真实终止）")
	}
	if !sess.closed {
		t.Fatal("私有后端 abort 必须 Close 会话")
	}
	after, ok := h.projectionKernel.reducer.Snapshot("deepseek", "th-reg-ds")
	if !ok || after.SyncRev <= before.SyncRev {
		t.Fatalf("私有后端 abort 必须合成终态并推进 syncRev: before=%+v after=%+v", before, after)
	}
	if after.Execution.Phase != "idle" || after.Execution.ActiveTurnID != "" {
		t.Fatalf("私有后端合成 idle 缺失: after=%+v", after)
	}
}
