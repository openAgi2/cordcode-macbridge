package codexweb

// control_race_test.go —— 审计 §3.1-A3：steer/interrupt 失配 resync-retry（官方
// 移植）。解析器锚点 tui/src/app.rs:643-692；重试语义锚点
// tui/src/app/thread_routing.rs:604-627（interrupt）、:683-727（steer）。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestParseSteerRaceOfficialMessages(t *testing.T) {
	// Missing 分支（app.rs:656-657）。
	if r := parseSteerRace("no active turn to steer"); !r.missing || r.actualTurnID != "" {
		t.Fatalf("missing race = %+v", r)
	}
	// 失配变体带反引号（app.rs:659-674）。
	if r := parseSteerRace("expected active turn id `turn-old` but found `turn-new`"); r.missing || r.actualTurnID != "turn-new" {
		t.Fatalf("mismatch race = %+v", r)
	}
	// interrupt 变体（无反引号）不得误匹配 steer 解析。
	if r := parseSteerRace("expected active turn id turn-old but found turn-new"); r.missing || r.actualTurnID != "" {
		t.Fatalf("bare variant must not match steer parser: %+v", r)
	}
	if r := parseSteerRace("some other error"); r.missing || r.actualTurnID != "" {
		t.Fatalf("unrelated = %+v", r)
	}
}

func TestParseInterruptMismatchOfficialMessages(t *testing.T) {
	// interrupt 失配无反引号（app.rs:676-692）。
	if got := parseInterruptMismatch("expected active turn id turn-old but found turn-new"); got != "turn-new" {
		t.Fatalf("mismatch = %q", got)
	}
	if got := parseInterruptMismatch("no active turn to interrupt"); got != "" {
		t.Fatalf("unrelated = %q", got)
	}
}

// interruptRaceTransport 对 turnId=stale-turn 回官方失配错误、对 actual-turn 回成功
//（官方 -32600 文案：interrupt 无反引号变体）。
type interruptRaceTransport struct {
	*scriptedTransport
}

func (s *interruptRaceTransport) hook() func(payload []byte) {
	return func(payload []byte) {
		var req struct {
			ID     json.Number `json:"id"`
			Method string      `json:"method"`
			Params struct {
				TurnID string `json:"turnId"`
			} `json:"params"`
		}
		dec := json.NewDecoder(strings.NewReader(string(payload)))
		dec.UseNumber()
		if dec.Decode(&req) != nil || req.Method != "turn/interrupt" {
			return
		}
		switch req.Params.TurnID {
		case "stale-turn":
			s.push(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"error":{"code":-32600,"message":"expected active turn id stale-turn but found actual-turn"}}`)
		case "actual-turn":
			s.push(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"result":{}}`)
		}
	}
}

func TestCancelTurnMismatchResyncsAndRetriesOnce(t *testing.T) {
	a, transport, _ := interactionTestAgent(t, 4)
	transport.onSend = (&interruptRaceTransport{transport}).hook()
	// liveCodec 持过期观测（如外部客户端取代 turn 后本地尚未收到广播）。
	a.mu.Lock()
	a.liveCodec.setActiveTurn("th-race", "stale-turn")
	a.mu.Unlock()

	if err := a.CancelTurnForThread(context.Background(), "th-race"); err != nil {
		t.Fatalf("resync retry must succeed, got %v", err)
	}
	// 权威纠正生效：观测已重同步为服务器报告的 actual。
	a.mu.Lock()
	observed := a.liveCodec.ActiveTurn("th-race")
	a.mu.Unlock()
	if observed != "actual-turn" {
		t.Fatalf("liveCodec observed = %q, want actual-turn", observed)
	}
	interrupts := 0
	for _, frame := range transport.sentFrames() {
		if strings.Contains(string(frame), `"turn/interrupt"`) {
			interrupts++
		}
	}
	if interrupts != 2 {
		t.Fatalf("interrupt frames = %d, want 2 (stale then resynced)", interrupts)
	}
}

func TestCancelTurnMismatchStillFailingReturnsOfficialError(t *testing.T) {
	a, transport, _ := interactionTestAgent(t, 4)
	transport.onSend = func(payload []byte) {
		var req struct {
			ID     json.Number `json:"id"`
			Method string      `json:"method"`
		}
		dec := json.NewDecoder(strings.NewReader(string(payload)))
		dec.UseNumber()
		if dec.Decode(&req) != nil || req.Method != "turn/interrupt" {
			return
		}
		// 重试后仍失配（服务器 active turn 又被取代）：重试一次后原样返回。
		transport.push(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"error":{"code":-32600,"message":"expected active turn id a but found b"}}`)
	}
	a.mu.Lock()
	a.liveCodec.setActiveTurn("th-race2", "a")
	a.mu.Unlock()

	err := a.CancelTurnForThread(context.Background(), "th-race2")
	if err == nil {
		t.Fatal("persisting mismatch must surface official error")
	}
	if !strings.Contains(err.Error(), "expected active turn id") {
		t.Fatalf("official error must pass through verbatim: %v", err)
	}
	interrupts := 0
	for _, frame := range transport.sentFrames() {
		if strings.Contains(string(frame), `"turn/interrupt"`) {
			interrupts++
		}
	}
	if interrupts != 2 {
		t.Fatalf("interrupt frames = %d, want exactly 2 (no infinite retry)", interrupts)
	}
}

func TestSteerMismatchResyncsAndRetriesOnce(t *testing.T) {
	a, transport, _ := interactionTestAgent(t, 4)
	transport.onSend = func(payload []byte) {
		var req struct {
			ID     json.Number `json:"id"`
			Method string      `json:"method"`
			Params struct {
				ExpectedTurnID string `json:"expectedTurnId"`
			} `json:"params"`
		}
		dec := json.NewDecoder(strings.NewReader(string(payload)))
		dec.UseNumber()
		if dec.Decode(&req) != nil || req.Method != "turn/steer" {
			return
		}
		switch req.Params.ExpectedTurnID {
		case "stale-turn":
			// steer 失配带反引号（app.rs:659-674）。
			transport.push(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"error":{"code":-32600,"message":"expected active turn id ` + "`stale-turn`" + ` but found ` + "`actual-turn`" + `"}}`)
		case "actual-turn":
			transport.push(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"result":{"turnId":"turn-queued"}}`)
		}
	}
	sess := &agentSession{agent: a, threadID: "th-steer"}
	a.mu.Lock()
	a.liveCodec.setActiveTurn("th-steer", "stale-turn")
	a.mu.Unlock()

	steered, err := sess.Steer(context.Background(), "追加输入")
	if err != nil {
		t.Fatalf("steer resync retry must succeed: %v", err)
	}
	if steered != "turn-queued" {
		t.Fatalf("steered = %q", steered)
	}
	a.mu.Lock()
	observed := a.liveCodec.ActiveTurn("th-steer")
	a.mu.Unlock()
	if observed != "actual-turn" {
		t.Fatalf("liveCodec observed = %q, want actual-turn", observed)
	}
	steers := 0
	for _, frame := range transport.sentFrames() {
		if strings.Contains(string(frame), `"turn/steer"`) {
			steers++
		}
	}
	if steers != 2 {
		t.Fatalf("steer frames = %d, want 2", steers)
	}
}

func TestSteerMissingFallsBackToFreshSend(t *testing.T) {
	a, transport, _ := interactionTestAgent(t, 4)
	var steerSeen, startSeen bool
	transport.onSend = func(payload []byte) {
		var req struct {
			ID     json.Number `json:"id"`
			Method string      `json:"method"`
		}
		dec := json.NewDecoder(strings.NewReader(string(payload)))
		dec.UseNumber()
		if dec.Decode(&req) != nil {
			return
		}
		switch req.Method {
		case "turn/steer":
			steerSeen = true
			transport.push(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"error":{"code":-32600,"message":"no active turn to steer"}}`)
		case "turn/start":
			startSeen = true
			transport.push(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"result":{"turn":{"id":"turn-fresh","items":[],"status":"inProgress"}}}`)
		}
	}
	sess := &agentSession{agent: a, threadID: "th-steer2"}
	a.mu.Lock()
	a.liveCodec.setActiveTurn("th-steer2", "ghost-turn")
	a.mu.Unlock()

	if _, err := sess.Steer(context.Background(), "新输入"); err != nil {
		t.Fatalf("missing 分支应转普通 Send 成功: %v", err)
	}
	if !steerSeen || !startSeen {
		t.Fatalf("steer=%v start=%v, want both", steerSeen, startSeen)
	}
	// Missing 分支清除过期观测（官方 clear_active_turn_id，thread_routing.rs:691-698）。
	a.mu.Lock()
	observed := a.liveCodec.ActiveTurn("th-steer2")
	a.mu.Unlock()
	if observed != "" {
		t.Fatalf("ghost observation must be cleared, got %q", observed)
	}
}

// 保证编译期引用 core（send 路径携带 core.ImageAttachment/nil 形参）。
var _ = core.Event{}
var _ = http.StatusOK
