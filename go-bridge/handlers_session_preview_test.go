package gobridge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// Session preview（Phase 4 §6.3）定向测试：响应预算、item-boundary 裁剪、
// capability gate、参数校验与 Kernel 快照只读路径。
//
// projection.hydrating / not_found 的错误映射是 handlers_projection.go 既有
// 映射的子集（1:1 复用错误 sentinel），其语义由 handlers_projection_test.go
// 锁定，此处不重建 hydrate 状态机。

// TestSessionPreviewTrim_BudgetAndItemBoundary：超预算 turn 按 part 边界从较早
// 内容丢弃（user → system → assistant），结果 ≤128KiB 且 truncated=true；尾部
// （最新）part 保留；原 turn 不被写穿（deep copy）。
func TestSessionPreviewTrim_BudgetAndItemBoundary(t *testing.T) {
	big := strings.Repeat("x", 64*1024) // 单 part 64KiB：两个即超预算
	turn := TurnProjection{
		TurnID:  "turn-1",
		Status:  "completed",
		User:    &MessageProjection{ID: "u", Role: "user", Parts: []ProjectionPart{{Type: "text", Text: big}}},
		System:  &MessageProjection{ID: "s", Role: "system", Parts: []ProjectionPart{{Type: "text", Text: big}}},
		Assistant: &MessageProjection{ID: "a", Role: "assistant", Parts: []ProjectionPart{
			{Type: "text", Text: big},
			{Type: "text", Text: "final answer"}, // 尾部 part：必须保留
		}},
	}

	trimmed, truncated := trimTurnToPreviewBudget(turn)
	if !truncated {
		t.Fatal("over-budget turn must be truncated")
	}
	b, err := json.Marshal(trimmed)
	if err != nil {
		t.Fatalf("marshal trimmed: %v", err)
	}
	if len(b) > sessionPreviewMaxBytes {
		t.Fatalf("trimmed turn = %d bytes, budget %d", len(b), sessionPreviewMaxBytes)
	}
	// 尾部（最新）Assistant part 保留。
	kept := false
	for _, m := range []*MessageProjection{trimmed.User, trimmed.System, trimmed.Assistant} {
		if m == nil {
			continue
		}
		for _, p := range m.Parts {
			if p.Text == "final answer" {
				kept = true
			}
		}
	}
	if !kept {
		t.Fatal("newest assistant part must survive trimming")
	}
	// 原 turn 不被写穿：裁剪只作用于副本。
	if len(turn.User.Parts) != 1 || len(turn.System.Parts) != 1 || len(turn.Assistant.Parts) != 2 {
		t.Fatalf("source turn mutated by trimming: user=%d system=%d assistant=%d",
			len(turn.User.Parts), len(turn.System.Parts), len(turn.Assistant.Parts))
	}
}

// TestSessionPreviewTrim_FitsNoTruncation：预算内的 turn 原样返回、truncated=false。
func TestSessionPreviewTrim_FitsNoTruncation(t *testing.T) {
	turn := TurnProjection{
		TurnID: "turn-1",
		Status: "completed",
		Assistant: &MessageProjection{ID: "a", Role: "assistant", Parts: []ProjectionPart{
			{Type: "text", Text: "hello"},
		}},
	}
	trimmed, truncated := trimTurnToPreviewBudget(turn)
	if truncated {
		t.Fatal("in-budget turn must not be truncated")
	}
	if trimmed.Assistant == nil || len(trimmed.Assistant.Parts) != 1 || trimmed.Assistant.Parts[0].Text != "hello" {
		t.Fatalf("in-budget turn must round-trip unchanged, got %+v", trimmed.Assistant)
	}
}

// TestHandleGetSessionPreview_NotSupported：非 projection backend fail-closed。
func TestHandleGetSessionPreview_NotSupported(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("mystery", &fakeAgent{name: "mystery"})
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "mystery", Method: "get_session_preview", RequestID: "p1",
		Params: mustJSONRaw(t, map[string]any{"sessionId": "s1"}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if msgs[0]["ok"] != false {
		t.Fatalf("preview on non-projection backend ok = %#v, want false", msgs[0]["ok"])
	}
	errObj, _ := msgs[0]["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "not_supported" {
		t.Fatalf("code = %q, want not_supported", code)
	}
}

// TestHandleGetSessionPreview_MissingParam：无 sessionId 拒绝。
func TestHandleGetSessionPreview_MissingParam(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode"})
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode", Method: "get_session_preview", RequestID: "p1",
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if msgs[0]["ok"] != false {
		t.Fatalf("missing sessionId ok = %#v, want false", msgs[0]["ok"])
	}
	errObj, _ := msgs[0]["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "missing_param" {
		t.Fatalf("code = %q, want missing_param", code)
	}
}

// seedPreviewKernel 在 Kernel 播种已提交投影并置 Ready（handler 的 Ready 快速
// 路径 + CommittedSnapshot 只读路径）。
func seedPreviewKernel(h *Handlers, backendID, sessionID string, turns []TurnProjection, syncRev int) {
	h.projectionKernel.reducer.Restore(backendID, sessionID, SessionProjection{
		SessionID: sessionID,
		SyncRev:   syncRev,
		Execution: ExecutionView{Phase: "idle"},
		Turns:     turns,
	})
	h.projectionKernel.MarkReady(backendID, sessionID)
}

// TestHandleGetSessionPreview_ReturnsLatestTurnAndHeadRev：headRev ==
// sourceProjection.syncRev（协议冻结项），latestTurn 是最后一个真实 turn。
func TestHandleGetSessionPreview_ReturnsLatestTurnAndHeadRev(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode"})
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	seedPreviewKernel(handlers, "claudecode", "ses_prev", []TurnProjection{
		{TurnID: "turn-old", Status: "completed",
			Assistant: &MessageProjection{ID: "a1", Role: "assistant", Parts: []ProjectionPart{{Type: "text", Text: "old"}}}},
		{TurnID: "turn-new", Status: "completed",
			Assistant: &MessageProjection{ID: "a2", Role: "assistant", Parts: []ProjectionPart{{Type: "text", Text: "new"}}}},
	}, 42)

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode", Method: "get_session_preview", RequestID: "p1",
		Params: mustJSONRaw(t, map[string]any{"sessionId": "ses_prev"}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if msgs[0]["ok"] != true {
		t.Fatalf("preview ok = %#v, want true", msgs[0]["ok"])
	}
	data, _ := msgs[0]["data"].(map[string]any)
	if got, _ := data["headRev"].(float64); got != 42 {
		t.Fatalf("headRev = %#v, want 42 (== sourceProjection.syncRev)", got)
	}
	turn, _ := data["latestTurn"].(map[string]any)
	if turn == nil {
		t.Fatal("latestTurn missing")
	}
	if turn["turnId"] != "turn-new" {
		t.Fatalf("latestTurn.turnId = %#v, want turn-new (last real turn)", turn["turnId"])
	}
	assistant, _ := turn["assistant"].(map[string]any)
	parts, _ := assistant["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("latestTurn assistant parts = %d, want 1", len(parts))
	}
	if truncated, _ := data["truncated"].(bool); truncated {
		t.Fatal("small turn must not be truncated")
	}
}

// TestHandleGetSessionPreview_EmptyTurnsOmitsLatestTurn：无 turn 时 latestTurn
// key 缺省，不伪造摘要。
func TestHandleGetSessionPreview_EmptyTurnsOmitsLatestTurn(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode"})
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	seedPreviewKernel(handlers, "claudecode", "ses_empty", nil, 7)

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode", Method: "get_session_preview", RequestID: "p1",
		Params: mustJSONRaw(t, map[string]any{"sessionId": "ses_empty"}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	data, _ := msgs[0]["data"].(map[string]any)
	if _, has := data["latestTurn"]; has {
		t.Fatal("latestTurn must be absent for a projection with no turns")
	}
	if got, _ := data["headRev"].(float64); got != 7 {
		t.Fatalf("headRev = %#v, want 7", got)
	}
}

// TestBackendCapabilitiesAdvertiseSessionPreview：projection-backed backend 广告
// session_preview_v1；非 projection backend 不广告（fail-closed 对应）。
func TestBackendCapabilitiesAdvertiseSessionPreview(t *testing.T) {
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode"})
	handlers.RegisterAgent("mystery", &fakeAgent{name: "mystery"})

	for _, be := range handlers.BackendList() {
		has := false
		for _, c := range be.Capabilities {
			if c == "session_preview_v1" {
				has = true
			}
		}
		if be.ID == "claudecode" && !has {
			t.Fatalf("claudecode capabilities missing session_preview_v1: %v", be.Capabilities)
		}
		if be.ID == "mystery" && has {
			t.Fatalf("mystery must not advertise session_preview_v1: %v", be.Capabilities)
		}
	}
}

var _ core.Agent = (*fakeAgent)(nil)
