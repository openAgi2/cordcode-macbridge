package gobridge

// abort_observed_test.go —— P0-3 handleAbortGeneration 观察/被动会话直达
// CancelTurnForThread：registry 缺失不再静默 Ok，按 threadID 直达官方
// turn/interrupt（codex-web 共享 daemon 上 Mac 发起的 turn 由 iOS 停止）。

import (
	"context"
	"testing"
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
// 回执 Ok + CancelTurnForThread 恰好一次（threadID 原样）。
func TestAbortObservedThreadDirectCancel(t *testing.T) {
	h := newTestHandlers(t)
	agent := &abortObservedStub{}
	h.RegisterAgent("codex-web", agent)

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
