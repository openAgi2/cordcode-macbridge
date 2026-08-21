package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// T-wiring for opencode-web (design §4.1.5 / §4.3.2 表): every projection and
// dispatch membership, the two deliberate NON-memberships (S7 catalog gate,
// deepseek-only no-external-event-source pruning), M2-1 switchDir behavior,
// M2-2 relay idle timeout, M3 observation re-attach (constant form).
func TestOpenCodeWebProjectionWiring(t *testing.T) {
	if !backendSupportsProjectionHydrate("opencode-web") {
		t.Fatal("opencode-web must be a projection hydrate backend (pathless family, design §4.3.2)")
	}
	if !pathlessRichHistoryBackend("opencode-web") {
		t.Fatal("opencode-web must be a pathless rich-history backend")
	}
	// 不进 deepseek-only 剪枝：SSE /global/event 是服务端级广播外部事件源。
	if backendHasNoExternalEventSource("opencode-web") {
		t.Fatal("opencode-web must NOT be in the no-external-event-source pruning list")
	}
	// S7（二轮亲核修正维持）：catalog 门控的是 list_sessions 对未协商 v2 的旧
	// 客户端（显式 wire 错误）；dsh-web 不在列且正常——opencode-web 同判不加入。
	if catalogCapabilityRequiredFor("opencode-web") {
		t.Fatal("opencode-web must NOT require the catalog capability gate (S7)")
	}
	// M2-2：审批等待期无 text_delta，60s 空闲超时不得收口权限卡。
	if !disablesRelayIdleTimeout("opencode-web") {
		t.Fatal("opencode-web must disable the relay idle timeout (M2-2)")
	}
	// M3：relay 重连 re-attach 名单（提为可测常量形态，二轮提示 2 的选择）。
	m3Found := false
	for _, b := range observationResubscribeBackends {
		if b == "opencode-web" {
			m3Found = true
		}
	}
	if !m3Found {
		t.Fatalf("observation re-attach list must contain opencode-web: %v", observationResubscribeBackends)
	}
	// backendKindForAgent 显式 case。
	if backendKindForAgent(&fakeAgent{name: "opencode-web"}) != "opencode-web" {
		t.Fatal("backendKindForAgent must map opencode-web explicitly")
	}
	// SSV2 能力广告（id/kind 双形态）。
	backends := []AgentProviderDescriptor{
		{ID: "opencode-web", Kind: "opencode-web"},
		{ID: "x", Kind: "opencode-web"},
	}
	advertiseSessionSyncV2Backend(backends)
	for i, b := range backends {
		found := false
		for _, c := range b.Capabilities {
			if c == "session_sync_v2" {
				found = true
			}
		}
		if !found {
			t.Fatalf("descriptor[%d] must advertise session_sync_v2: %+v", i, b)
		}
	}
	// 源准备：注册 agent → pathless source（Identity=sessionID, Path 空）。
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	agent := &fakeAgent{name: "opencode-web"}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()
	src, err := h.prepareProjectionHydrateSource(context.Background(), "opencode-web", "ses_ocw", "")
	if err != nil {
		t.Fatalf("registered opencode-web agent must prepare a pathless source: %v", err)
	}
	if src.Path != "" || src.Identity != "ses_ocw" {
		t.Fatalf("source must be pathless identity-only, got %+v", src)
	}
}

// M2-1 行为级断言：带 directory 的读方法 RPC 必须切换 opencode-web 的
// workDir（四读方法在 shouldSwitchWorkDirForMethod 之外，全靠 Name 特判），
// 否则 x-opencode-directory 恒为启动值，坑 5 复发。
func TestDispatchRPCSwitchDirCoversOpenCodeWebReads(t *testing.T) {
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	for _, method := range []string{"list_sessions", "get_session", "get_session_messages", "get_session_projection"} {
		agent := &fakeAgent{name: "opencode-web"}
		params, _ := json.Marshal(map[string]any{"directory": "/tmp/proj-ocw", "sessionId": "ses_x"})
		h.dispatchRPC(&scopeTestConn{}, WireMessage{Method: method, Params: params, RequestID: "r-ocw-" + method}, agent)
		if got := agent.GetWorkDir(); got != "/tmp/proj-ocw" {
			t.Fatalf("%s must switch opencode-web workDir to the request directory (got %q)", method, got)
		}
	}
}

// 主路径：注册 opencode-web agent（RichHistoryProvider）→ 冷 pull pathless
// 全量重建（消息 API 基线），kernel Ready —— 对照 TestDSHWebProjectionHydrateFromRichHistory。
func TestOpenCodeWebProjectionHydrateFromRichHistory(t *testing.T) {
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	sessionID := "ocw-hist-1"
	agent := &fakeAgent{
		name: "opencode-web",
		richHistory: []core.RichHistoryEntry{
			{ID: "u1", Role: "user", Content: "网页建的会话，iOS 续聊"},
			{
				ID: "a1", Role: "assistant", Content: "好的，接上上下文",
				Thinking: "先读历史",
				Parts: []map[string]any{
					{"type": "reasoning", "content": "先读历史"},
					{"type": "text", "content": "好的，接上上下文"},
				},
			},
		},
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]any{"sessionId": sessionID, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-ocw-hist", BackendID: "opencode-web", Method: "get_session_projection", Params: params}
	h.handleGetSessionProjection(conn, msg, nil)
	if conn.err != nil {
		t.Fatalf("pathless hydrate error: %+v", conn.err)
	}
	raw, _ := json.Marshal(conn.data)
	for _, want := range []string{"网页建的会话，iOS 续聊", "好的，接上上下文", "先读历史"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("projection missing %q: %s", want, string(raw))
		}
	}
	if st := h.projectionKernel.Status("opencode-web", sessionID); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready", st.Phase)
	}
}

// Official HTTP tool parts hydrate as tool_started/tool_finished. The
// previous mapper skipped them (tool is a string, not a nested object), so
// iPhone cold-open of an OpenCode Web session showed text but no 已读取/已编辑.
func TestOpenCodeWebProjectionHydrateOfficialToolSteps(t *testing.T) {
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	sessionID := "ocw-tools-1"
	readStep := map[string]any{
		"id": "prt_read1", "toolName": "read", "status": "completed",
		"title": "story.txt",
		"toolInput": map[string]any{"filePath": "/tmp/story.txt"},
		"output":    map[string]any{"kind": "inline", "text": "file contents"},
	}
	editStep := map[string]any{
		"id": "prt_edit1", "toolName": "edit", "status": "completed",
		"title": "story.txt",
		"fileChanges": []map[string]any{{
			"path": "/tmp/story.txt", "kind": "edit", "diff": "+scene",
		}},
		"output": map[string]any{"kind": "inline", "text": "Edit applied successfully."},
	}
	agent := &fakeAgent{
		name: "opencode-web",
		richHistory: []core.RichHistoryEntry{
			{ID: "u1", Role: "user", Content: "再增加情节"},
			{
				ID: "a1", Role: "assistant", Content: "先看当前文件结构：",
				Thinking: "Let me read the file.",
				Parts: []map[string]any{
					{"type": "reasoning", "content": "Let me read the file."},
					{"type": "text", "content": "先看当前文件结构："},
					{"type": "tool", "step": readStep},
				},
				Steps: []map[string]any{readStep},
			},
			{
				ID: "a2", Role: "assistant", Content: "已写入。",
				Thinking: "I'll insert a scene.",
				Parts: []map[string]any{
					{"type": "reasoning", "content": "I'll insert a scene."},
					{"type": "text", "content": "已写入。"},
					{"type": "tool", "step": editStep},
				},
				Steps: []map[string]any{editStep},
			},
		},
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]any{"sessionId": sessionID, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-ocw-tools", BackendID: "opencode-web", Method: "get_session_projection", Params: params}
	h.handleGetSessionProjection(conn, msg, nil)
	if conn.err != nil {
		t.Fatalf("pathless hydrate error: %+v", conn.err)
	}
	raw, _ := json.Marshal(conn.data)
	for _, want := range []string{
		"再增加情节",
		"先看当前文件结构：",
		"Let me read the file.",
		"已写入。",
		`"toolName":"read"`,
		`"toolName":"edit"`,
		"story.txt",
		"prt_read1",
		"prt_edit1",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("projection missing %q: %s", want, string(raw))
		}
	}
}

// activityProbingFakeAgent adds core.SessionActivityProbing to fakeAgent for
// the cold-source seal tests without changing fakeAgent's interface set (every
// existing fakeAgent user would suddenly consult the probe). richHistoryFn,
// when set, serves per-call dynamic entries (persist-race simulation) and
// counts fetches through fetchCalls.
type activityProbingFakeAgent struct {
	*fakeAgent
	active        bool
	richHistoryFn func(call int32) []core.RichHistoryEntry
	fetchCalls    *atomic.Int32
	fetchInfo     func() (*core.AgentSessionInfo, error)
}

func (a *activityProbingFakeAgent) FetchSessionInfo(ctx context.Context, sessionID string) (*core.AgentSessionInfo, error) {
	if a.fetchInfo == nil {
		return nil, fmt.Errorf("no fetchInfo stub")
	}
	return a.fetchInfo()
}

func (a *activityProbingFakeAgent) IsSessionActive(context.Context, string) bool {
	return a.active
}

func (a *activityProbingFakeAgent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	if a.richHistoryFn == nil {
		return a.fakeAgent.GetRichSessionHistory(ctx, sessionID, limit)
	}
	call := a.fetchCalls.Add(1)
	return a.richHistoryFn(call), nil
}

// ocwLiveColdPullAgent builds the first-turn topology: one sent user message
// still unanswered (assistant streaming), an activity probe with the given
// busy-map verdict, and a session the bridge registry holds live unless
// registerLive is false.
func ocwLiveColdPullHarness(t *testing.T, busyMapActive, registerLive bool) (*Handlers, *readFileCaptureConn) {
	t.Helper()
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	// A blocked commit gate must fail fast, not after the 15s default budget.
	prevTimeout := coldHydrateTimeout
	coldHydrateTimeout = 200 * time.Millisecond
	t.Cleanup(func() { coldHydrateTimeout = prevTimeout })

	sessionID := "ocw-live-1"
	agent := &activityProbingFakeAgent{
		fakeAgent: &fakeAgent{
			name: "opencode-web",
			richHistory: []core.RichHistoryEntry{
				{ID: "u1", Role: "user", Content: "讲个猴哥语录100字左右"},
			},
		},
		active: busyMapActive,
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()
	if registerLive {
		h.putSessionWithMeta(sessionID, "opencode-web", "/tmp/proj",
			&fakeAgentSession{id: sessionID, events: make(chan core.Event, 4)})
	}

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]any{"sessionId": sessionID, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-ocw-live", BackendID: "opencode-web", Method: "get_session_projection", Params: params}
	h.handleGetSessionProjection(conn, msg, agent)
	return h, conn
}

// Real-device 2026-08-20 first-turn regression (busy-map race): the iOS pull
// lands in the window where prompt_async is queued but the serve's busy map
// has not registered the session yet — 1.18 answers a missing key as
// definitive idle. The bridge registry holds the session live, so the cold
// source must NOT seal the just-sent turn as rich_history_unanswered, and the
// hydrate must commit a running partial instead of blocking on the turn's
// terminal event.
func TestOpenCodeWebLiveSessionColdPullSkipsSealAndCommitsRunningPartial(t *testing.T) {
	h, conn := ocwLiveColdPullHarness(t, false /* busy-map miss: the race */, true /* registry live */)
	if conn.err != nil {
		t.Fatalf("live mid-turn cold pull must commit a running partial, got error: %+v", conn.err)
	}
	proj := projectionFromCapture(t, conn)
	if proj.Execution.Phase != "running" {
		t.Fatalf("execution phase = %q, want running (SSV2 completion is projection phase)", proj.Execution.Phase)
	}
	if proj.Execution.ActiveTurnID == "" {
		t.Fatalf("running partial must arm activeTurnId: %+v", proj.Execution)
	}
	raw, _ := json.Marshal(conn.data)
	if strings.Contains(string(raw), `"status":"error"`) {
		t.Fatalf("registry-live session must not seal the just-sent turn as terminal: %s", string(raw))
	}
	if !strings.Contains(string(raw), "讲个猴哥语录100字左右") {
		t.Fatalf("projection missing the sent user message: %s", string(raw))
	}
	if st := h.projectionKernel.Status("opencode-web", "ocw-live-1"); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready (running partial commit)", st.Phase)
	}
}

// The sourceIsLive half alone: the busy map already shows the session (pull
// landed mid-generation), so the seal path is inert — but without registry
// liveness feeding sourceIsLive the commit gate would block on the in-flight
// turn's terminal event until the budget expired.
func TestOpenCodeWebLiveSessionColdPullWithBusyProbeCommitsRunningPartial(t *testing.T) {
	h, conn := ocwLiveColdPullHarness(t, true /* busy map shows the session */, true /* registry live */)
	if conn.err != nil {
		t.Fatalf("live mid-turn cold pull must commit a running partial, got error: %+v", conn.err)
	}
	proj := projectionFromCapture(t, conn)
	if proj.Execution.Phase != "running" {
		t.Fatalf("execution phase = %q, want running", proj.Execution.Phase)
	}
	raw, _ := json.Marshal(conn.data)
	if strings.Contains(string(raw), `"status":"error"`) {
		t.Fatalf("busy session must not seal the in-flight turn as terminal: %s", string(raw))
	}
	if st := h.projectionKernel.Status("opencode-web", "ocw-live-1"); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready (running partial commit)", st.Phase)
	}
}

func projectionFromCapture(t *testing.T, conn *readFileCaptureConn) SessionProjection {
	t.Helper()
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data not map: %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("expected committed projection, got %T %+v", dataMap["projection"], dataMap)
	}
	return proj
}

// Real-device 2026-08-20 shape: registry-live, busy-map miss, 1 user row plus
// an empty in-flight assistant shell. Completing that shell is what produced
// {"phase":"idle"} (executionBytes=16, headRev=2). The cold commit must stay
// running — do not wait/re-poll history, do not seal.
func TestOpenCodeWebLiveSessionColdPullEmptyAssistantCommitsRunning(t *testing.T) {
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	prevTimeout := coldHydrateTimeout
	coldHydrateTimeout = 200 * time.Millisecond
	t.Cleanup(func() { coldHydrateTimeout = prevTimeout })

	sessionID := "ocw-live-empty-asst"
	agent := &activityProbingFakeAgent{
		fakeAgent: &fakeAgent{
			name: "opencode-web",
			richHistory: []core.RichHistoryEntry{
				{ID: "u1", Role: "user", Content: "讲个猴哥语录100字左右"},
				{ID: "a1", Role: "assistant", Content: ""},
			},
		},
		active: false,
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()
	h.putSessionWithMeta(sessionID, "opencode-web", "/tmp/proj",
		&fakeAgentSession{id: sessionID, events: make(chan core.Event, 4)})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]any{"sessionId": sessionID, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-ocw-empty-asst", BackendID: "opencode-web", Method: "get_session_projection", Params: params}
	h.handleGetSessionProjection(conn, msg, agent)
	if conn.err != nil {
		t.Fatalf("live empty-assistant cold pull must commit, got error: %+v", conn.err)
	}
	proj := projectionFromCapture(t, conn)
	if proj.Execution.Phase != "running" {
		t.Fatalf("execution phase = %q, want running (empty assistant is in-flight, not idle)", proj.Execution.Phase)
	}
	if proj.Execution.ActiveTurnID != "u1" {
		t.Fatalf("activeTurnId = %q, want u1", proj.Execution.ActiveTurnID)
	}
	raw, _ := json.Marshal(conn.data)
	if strings.Contains(string(raw), `"status":"error"`) {
		t.Fatalf("must not seal the in-flight turn: %s", string(raw))
	}
}

// Control: a dead session (not in the bridge registry) whose backend confirms
// idle still seals trailing unanswered turns — the 2026-08-14 empty-turn fix
// must survive the registry-liveness override.
func TestOpenCodeWebDeadSessionColdPullStillSealsUnansweredTurn(t *testing.T) {
	h, conn := ocwLiveColdPullHarness(t, false /* idle verdict */, false /* not in registry */)
	if conn.err != nil {
		t.Fatalf("dead-session cold pull must commit, got error: %+v", conn.err)
	}
	raw, _ := json.Marshal(conn.data)
	// The seal settles the trailing turn as terminal turn_error (the
	// rich_history_unanswered reason rides the event, not the projection).
	if !strings.Contains(string(raw), `"status":"error"`) {
		t.Fatalf("dead session must still seal the trailing unanswered turn: %s", string(raw))
	}
	if st := h.projectionKernel.Status("opencode-web", "ocw-live-1"); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready", st.Phase)
	}
}

// Real-device 2026-08-20 residual (~1s completed flicker): with the relay's
// per-event rebind alone, the registry keeps the session under the pending id
// until the first SSE event — a first-turn pull in that window resolves the
// real id but finds no live registry entry, so the seal override and the
// sourceIsLive sampling both miss. Send resolves the real id synchronously
// (opencode-web creates the server session inside Send); the rebind must not
// wait for the first event.
func TestOpenCodeWebSendRebindsPendingToRealBeforeFirstEvent(t *testing.T) {
	h := newTestHandlers(t)
	agent := &fakeAgent{
		name: "opencode-web",
		sendHook: func(s *fakeAgentSession, _ string) {
			// ensureServerSession resolves the real server id inside Send.
			s.id = "ses_ocw_real"
		},
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()

	serverConn, _, cleanup := openTestConn(t)
	defer cleanup()
	h.handleSendMessage(serverConn, WireMessage{
		BackendID: "opencode-web", Method: "send_message", RequestID: "r-ocw-rebind",
		Params: []byte(`{"sessionId":"pending-ocw1","content":"讲个猴哥语录"}`),
	}, agent)

	real, ok := h.getSession("ses_ocw_real")
	if !ok || real == nil {
		t.Fatal("registry must hold the real id immediately after Send, before any SSE event")
	}
	// rebind keeps the pending alias (resolveSessionIDForActiveSession depends
	// on it) — both keys must map to the SAME live session object.
	if pending, ok := h.getSession("pending-ocw1"); !ok || pending != real {
		t.Fatal("pending alias must map to the same session object as the real id")
	}
	if real.CurrentSessionID() != "ses_ocw_real" {
		t.Fatalf("real id = %q, want ses_ocw_real", real.CurrentSessionID())
	}
}

// SSV2 rules 4/6: a live session with 0 history rows must not wait or guess
// from count. One fetch, then commit the honest empty source. In-flight
// execution is preserved by the kernel merge when live already armed running.
func TestOpenCodeWebLiveEmptyColdSourceDoesNotRepoll(t *testing.T) {
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	sessionID := "ocw-live-empty"
	fetchCalls := &atomic.Int32{}
	agent := &activityProbingFakeAgent{
		fakeAgent: &fakeAgent{name: "opencode-web"},
		active:    false,
		richHistoryFn: func(int32) []core.RichHistoryEntry {
			return nil
		},
		fetchCalls: fetchCalls,
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()
	h.putSessionWithMeta(sessionID, "opencode-web", "/tmp/proj",
		&fakeAgentSession{id: sessionID, events: make(chan core.Event, 4)})

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]any{"sessionId": sessionID, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-ocw-empty", BackendID: "opencode-web", Method: "get_session_projection", Params: params}
	h.handleGetSessionProjection(conn, msg, agent)
	if conn.err != nil {
		t.Fatalf("live empty cold pull must commit, got error: %+v", conn.err)
	}
	if got := fetchCalls.Load(); got != 1 {
		t.Fatalf("bridge-live empty session must fetch exactly once, got %d", got)
	}
	if st := h.projectionKernel.Status("opencode-web", sessionID); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready (honest empty commit, no history wait)", st.Phase)
	}
}

// Control: dead sessions (not in the registry) keep the single-fetch behavior
// — an honestly empty session cold-opens as empty without re-poll delay.
func TestOpenCodeWebDeadEmptyColdSourceSingleFetch(t *testing.T) {
	h := NewHandlers()
	t.Cleanup(func() { h.Shutdown(context.Background()) })
	sessionID := "ocw-dead-empty"
	fetchCalls := &atomic.Int32{}
	agent := &activityProbingFakeAgent{
		fakeAgent: &fakeAgent{name: "opencode-web"},
		active:    false,
		richHistoryFn: func(int32) []core.RichHistoryEntry {
			return nil // honestly empty
		},
		fetchCalls: fetchCalls,
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()

	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]any{"sessionId": sessionID, "sinceRev": 0})
	msg := WireMessage{RequestID: "r-ocw-dead-empty", BackendID: "opencode-web", Method: "get_session_projection", Params: params}
	h.handleGetSessionProjection(conn, msg, agent)
	if conn.err != nil {
		t.Fatalf("dead empty-session cold pull must commit empty, got error: %+v", conn.err)
	}
	if got := fetchCalls.Load(); got != 1 {
		t.Fatalf("dead session must fetch exactly once, got %d", got)
	}
	if st := h.projectionKernel.Status("opencode-web", sessionID); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready (honest empty commit)", st.Phase)
	}
}

// get_session prefers the by-id fetcher over the directory-scoped list scan:
// archived sessions can be absent from the default enumeration while remaining
// individually readable (live 1.18.18) — the archive refetch must not race
// that filter, and sessions outside the current work dir must resolve.
func TestOpenCodeWebGetSessionPrefersByIDFetcher(t *testing.T) {
	h := newTestHandlers(t)
	agent := &activityProbingFakeAgent{
		fakeAgent: &fakeAgent{
			name: "opencode-web",
			// List intentionally does NOT contain the session (archive filter).
			sessionInfos: []core.AgentSessionInfo{},
		},
	}
	agent.fetchInfo = func() (*core.AgentSessionInfo, error) {
		return &core.AgentSessionInfo{
			ID: "ses_fetch1", Summary: "已归档会话", Directory: "/tmp/proj",
			ModifiedAt: time.UnixMilli(2).UTC(), ArchivedAt: time.UnixMilli(3).UTC(),
		}, nil
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"opencode-web": agent}
	h.mu.Unlock()
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]any{"sessionId": "ses_fetch1"})
	h.dispatchRPC(conn, WireMessage{Method: "get_session", BackendID: "opencode-web", Params: params, RequestID: "r-fetch"}, agent)
	if conn.err != nil {
		t.Fatalf("get_session must resolve via the by-id fetcher, got %+v", conn.err)
	}
	raw, _ := json.Marshal(conn.data)
	if !strings.Contains(string(raw), `"id":"ses_fetch1"`) || !strings.Contains(string(raw), `"archivedAtMillis"`) {
		t.Fatalf("by-id fetch result must carry the session with archivedAtMillis: %s", string(raw))
	}
}
