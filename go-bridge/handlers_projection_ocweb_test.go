package gobridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
