package gobridge

// dsh-web projection wiring tests (dsh-web design §4.3.2/§8-5): the backend
// joins the pathless hydrate family (HTTP rich-history baseline via
// session.history), the two forceCold sets include it, the deepseek
// store-file/live-only branch does NOT apply (no store semantics for this
// backend), it stays out of the no-external-event-source pruning list (mux
// IS the external source), and it advertises session_sync_v2.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// dshWebPull performs one get_session_projection for the dsh-web backend.
func dshWebPull(h *Handlers, sessionID string, sinceRev int) (*readFileCaptureConn, WireMessage) {
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID, "sinceRev": sinceRev})
	msg := WireMessage{RequestID: "r-dshweb-" + sessionID, BackendID: "dsh-web", Method: "get_session_projection", Params: params}
	h.handleGetSessionProjection(conn, msg, nil)
	return conn, msg
}

// dshWebProbeAgent lets a test force the SessionActivityProbing verdict for
// the trailing-unanswered seal test.
type dshWebProbeAgent struct {
	*fakeAgent
	active bool
}

func (a *dshWebProbeAgent) IsSessionActive(context.Context, string) bool { return a.active }

// T-wiring：五处接线点 + 两个不进清单。
func TestDSHWebProjectionWiringPoints(t *testing.T) {
	if !backendSupportsProjectionHydrate("dsh-web") {
		t.Fatal("dsh-web must be a projection hydrate backend (pathless family, design §4.3.2)")
	}
	// 不进 deepseek 的 store-file/live-only 分支：无 store 语义（会话在服务端常驻）。
	h := newDshProjectionHandlers(t)
	agent := &fakeAgent{name: "dsh-web", richHistory: nil}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": agent}
	h.mu.Unlock()
	// dsh-web 不依赖 DSH_HOME store fixture：源准备仅要求注册 agent（与
	// opencode/grokbuild 同构），此处不断言错误即通过路径选择。
	if _, err := h.prepareProjectionHydrateSource(context.Background(), "dsh-web", "s", ""); err != nil {
		t.Fatalf("registered dsh-web agent must prepare a pathless source: %v", err)
	}
	// 不进 backendHasNoExternalEventSource 剪枝（mux 即外部事件源）。
	if backendHasNoExternalEventSource("dsh-web") {
		t.Fatal("dsh-web must NOT be in the no-external-event-source pruning list (mux covers all sessions)")
	}
	// SSV2 能力广告（id/kind 双形态）。
	backends := []AgentProviderDescriptor{
		{ID: "dsh-web", Kind: "dsh-web"},
		{ID: "dsh-web", Kind: "deepseek-web"},
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
}

// 主路径：注册 dsh-web agent（RichHistoryProvider）→ 冷 pull pathless 全量
// 重建（session.history 基线），kernel Ready（grokbuild/deepseek 同款断言）。
func TestDSHWebProjectionHydrateFromRichHistory(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-hist-1"
	agent := &fakeAgent{
		name: "dsh-web",
		richHistory: []core.RichHistoryEntry{
			{ID: "u1", Role: "user", Content: "web 建的会话，iOS 续聊"},
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
	h.agents = map[string]core.Agent{"dsh-web": agent}
	h.mu.Unlock()

	conn, _ := dshWebPull(h, sessionID, 0)
	if conn.err != nil {
		t.Fatalf("pathless hydrate error: %+v", conn.err)
	}
	raw, _ := json.Marshal(conn.data)
	for _, want := range []string{"web 建的会话，iOS 续聊", "好的，接上上下文", "先读历史"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("projection missing %q: %s", want, string(raw))
		}
	}
	if st := h.projectionKernel.Status("dsh-web", sessionID); st.Phase != ProjectionHydrateReady {
		t.Fatalf("kernel phase = %q, want ready", st.Phase)
	}
}

// 死会话尾封口（M1 commit gate）：尾部未答 user turn，SessionActivityProbing
// 确认 idle → 如实封口；探活失败/active → 保持等待（保守）。
func TestDSHWebProjectionTrailingUnansweredSettlesWhenIdle(t *testing.T) {
	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-dead-1"
	agent := &dshWebProbeAgent{
		fakeAgent: &fakeAgent{
			name: "dsh-web",
			richHistory: []core.RichHistoryEntry{
				{ID: "u1", Role: "user", Content: "最后一问"}, // 尾部未答：死会话
			},
		},
		active: false,
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": agent}
	h.mu.Unlock()

	conn, _ := dshWebPull(h, sessionID, 0)
	if conn.err != nil {
		t.Fatalf("dead-session hydrate error: %+v", conn.err)
	}
	if st := h.projectionKernel.Status("dsh-web", sessionID); st.Phase != ProjectionHydrateReady {
		t.Fatalf("idle trailing turn must settle (phase=%q)", st.Phase)
	}
	raw, _ := json.Marshal(conn.data)
	if !strings.Contains(string(raw), "最后一问") {
		t.Fatalf("sealed trailing turn content missing: %s", string(raw))
	}
}

// 活会话不封口：探活 active → commit gate 等待（不猜完成）。
func TestDSHWebProjectionTrailingUnansweredWaitsWhenActive(t *testing.T) {
	prevTimeout := coldHydrateTimeout
	coldHydrateTimeout = 200 * time.Millisecond
	t.Cleanup(func() { coldHydrateTimeout = prevTimeout })

	h := newDshProjectionHandlers(t)
	sessionID := "dshweb-live-1"
	agent := &dshWebProbeAgent{
		fakeAgent: &fakeAgent{
			name: "dsh-web",
			richHistory: []core.RichHistoryEntry{
				{ID: "u1", Role: "user", Content: "正在跑的 turn"},
			},
		},
		active: true,
	}
	h.mu.Lock()
	h.agents = map[string]core.Agent{"dsh-web": agent}
	h.mu.Unlock()

	conn, _ := dshWebPull(h, sessionID, 0)
	if conn.err == nil {
		// 活跃尾部要么 hydrating（budget 内等待）——绝不能以完成态成功。
		raw, _ := json.Marshal(conn.data)
		if strings.Contains(string(raw), "aborted") || strings.Contains(string(raw), "error") {
			t.Fatalf("active session must not settle as terminal: %s", string(raw))
		}
	}
	// kernel 不得 Ready（活跃尾 turn 无终态可提交）。
	if st := h.projectionKernel.Status("dsh-web", sessionID); st.Phase == ProjectionHydrateReady {
		t.Fatal("active trailing turn must NOT commit as ready")
	}
}
