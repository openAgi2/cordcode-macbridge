package dshweb

// steer_mode_test.go — OD-2a=A 发送路径（A7 活体证据修正版）：官方 composer
// 默认 busy-Enter=queue（submission-policy.ts:29-38 + DEFAULT_BUSY_ENTER_
// BEHAVIOR='queue'），steer 是显式手势（agent.steer → next-step splice，
// agent-loop/agent.ts:166；空闲时唤醒成下一回合——实测 accepted，非源码
// 预映射的 agent-busy 拒绝）。桥镜像：默认/queue → mode:"queue"；
// "steer" → mode:"steer"；未知 mode 拒绝；无 per-request mode 的 backend
// 收到 steer 在 sendPrompt 可见失败，不静默降级。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func promptModeOf(t *testing.T, f *fakeDSHServer) string {
	t.Helper()
	calls := methodCalls(f, "session/prompt")
	if len(calls) == 0 {
		t.Fatal("session/prompt call missing")
	}
	var payload struct {
		Args struct {
			Request struct {
				Mode string `json:"mode"`
			} `json:"request"`
		} `json:"args"`
	}
	if err := json.Unmarshal(calls[len(calls)-1], &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Args.Request.Mode
}

func TestSendWithOptionsModeMapping(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	sess := boundTestSession(t, f, a, "sess-mode")
	f.handlers["session/prompt"] = fakeRPCResponse{value: map[string]any{"accepted": true}}

	// 默认（空 mode）与显式 queue 都落官方默认 mode:"queue"。
	if err := sess.SendWithOptions("plain send", nil, nil, core.PromptOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := promptModeOf(t, f); got != "queue" {
		t.Fatalf("default mode = %q, want queue (official composer default)", got)
	}
	if err := sess.SendWithOptions("explicit queue", nil, nil, core.PromptOptions{Mode: "queue"}); err != nil {
		t.Fatal(err)
	}
	if got := promptModeOf(t, f); got != "queue" {
		t.Fatalf("explicit queue mode = %q", got)
	}

	// steer 手势 → mode:"steer"（A7 direct 路径：next-step splice）。
	if err := sess.SendWithOptions("steer this", nil, nil, core.PromptOptions{Mode: "steer"}); err != nil {
		t.Fatal(err)
	}
	if got := promptModeOf(t, f); got != "steer" {
		t.Fatalf("steer mode = %q, want steer", got)
	}

	// 未知 mode 可见拒绝。
	err := sess.SendWithOptions("bogus", nil, nil, core.PromptOptions{Mode: "turbo"})
	if err == nil || !strings.Contains(err.Error(), "unsupported prompt mode") {
		t.Fatalf("unknown mode must fail visibly, got %v", err)
	}

	// Send（无 options 面）保持 queue 默认。
	if err := sess.Send("legacy send", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := promptModeOf(t, f); got != "queue" {
		t.Fatalf("legacy Send mode = %q, want queue", got)
	}
}
