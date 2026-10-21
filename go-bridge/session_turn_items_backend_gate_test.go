package gobridge

// RC-2（评审 R1 复核，2026-10-21）：BLOCKER-2 撤回 Claude 的 turn_detail_chunks_v1
// 声明后，服务端 backend 门同步收回 codex-remote。chunks 连接上 Claude 的
// session_turn_items 必须返回 unsupported_capability——不触发任何上游拉取、不把
// failed 态写进共享投影（休眠契约的服务端兜底），与协议 pack
// unified-bridge-protocol.md §11.7 撤回注一致。修复前该请求会经 v2 批量引擎
// failTerminal(upstream_error) 并把 failed 态提交进内核（R1 BLOCKER-2 证据链）。

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionTurnItemsClaudeRejectedByBackendGate(t *testing.T) {
	h, conn, sessionID, _ := turnDetailHarness(t, detailTurnFixture())
	// v2 客户端典型形态：lazy + chunks 双 echo，handler 偏好 v2 批量引擎——但
	// backend 门在 hydrate/引擎之前，claude 到不了那里。
	h.eventPublisher.SetConnTurnDetailChunksV1(conn, true)
	// 建立已 hydrate 的投影（codex-remote 基线；backend 门断言不依赖 backend 的
	// hydrate 结果，但投影存在可同时验证「failed 态不得被写入」）。
	olderWalkDispatch(h, conn, map[string]any{
		"direction": "window_0", "backendId": "codex-remote", "sessionId": sessionID, "limit": 10,
	})
	quiesceProjectionWrites(t, h)

	raw, _ := json.Marshal(map[string]any{"sessionId": sessionID, "turnId": "T1"})
	msg := WireMessage{RequestID: "r-claude-items", BackendID: "claude", Method: "session_turn_items", Params: raw}
	h.handleSessionTurnItems(conn, msg, nil)
	quiesceProjectionWrites(t, h)

	conn.mu.Lock()
	wireErr := conn.err
	conn.mu.Unlock()
	if wireErr == nil || wireErr.Code != "unsupported_capability" {
		t.Fatalf("claude session_turn_items must be rejected by backend gate, got %+v", wireErr)
	}
	if !strings.Contains(wireErr.Message, "codex-remote") {
		t.Fatalf("error message should name the supported backend: %+v", wireErr)
	}
	// 服务端兜底的核心断言：backend 门在任何投影写入之前，既有 turn 的明细态
	// 不得被置 failed。
	projection, ok := h.projectionKernel.Snapshot("codex-remote", sessionID)
	if !ok || len(projection.Turns) == 0 {
		t.Fatalf("projection missing: ok=%v", ok)
	}
	if got := projection.Turns[0].DetailLoadState; got != DetailStateNotRequested {
		t.Fatalf("detailLoadState=%q, want not_requested (backend gate must not touch projection)", got)
	}
	// claudecode 同族（BLOCKER-2 撤回覆盖双字面量）。
	conn.mu.Lock()
	conn.err, conn.data = nil, nil
	conn.mu.Unlock()
	msg.BackendID = "claudecode"
	h.handleSessionTurnItems(conn, msg, nil)
	quiesceProjectionWrites(t, h)
	conn.mu.Lock()
	wireErr = conn.err
	conn.mu.Unlock()
	if wireErr == nil || wireErr.Code != "unsupported_capability" {
		t.Fatalf("claudecode session_turn_items must be rejected by backend gate, got %+v", wireErr)
	}
}
