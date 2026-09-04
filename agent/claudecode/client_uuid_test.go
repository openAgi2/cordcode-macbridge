package claudecode

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// client_uuid_test.go —— client uuid turn 身份（owner 2026-09-05 复盘，官方 SDK
// user_message_uuid 契约）。证据锚点：CLI 2.1.234 真实探针
// （testdata/client-uuid/turn-stream.jsonl，--include-partial-messages + 输入 user
// 帧自带 uuid）——transcript 采纳该 uuid、result 帧回盖 user_message_uuid。

// probeFixtureUUID 与 fixture turn-stream.jsonl 中 result.user_message_uuid 一致
// （探针提交 user 帧时自带的 uuid，被 CLI 原样回盖）。
const probeFixtureUUID = "deadbeef-0000-4000-8000-2584000"

// Send 必须在输入 user 帧上自带 client uuid，并把它登记为 active turn。
func TestSendStampsClientUUIDOnUserFrame(t *testing.T) {
	cs := newTestClaudeSession(t)
	// os.Pipe（内核缓冲）：io.Pipe 是同步写，Send 的 stdin.Write 会阻塞等读端，
	// 与「Send 返回后再读」的断言顺序死锁。
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cs.stdin = pw
	t.Cleanup(func() { pw.Close(); pr.Close() })

	if err := cs.Send("讲个笑话", nil, nil); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var frame map[string]any
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		t.Fatal(err)
	}
	uuid, _ := frame["uuid"].(string)
	if uuid == "" {
		t.Fatalf("user frame missing client uuid: %s", line)
	}
	if !strings.HasSuffix(uuid[:14], "-0000-4000") && !strings.Contains(uuid[14:], "-") {
		// v4 形态粗检：8-4-4-4-12 分段
		t.Fatalf("client uuid not uuid-shaped: %q", uuid)
	}
	if got := cs.currentClientTurnID(); got != uuid {
		t.Fatalf("activeClientUUID = %q, want sent uuid %q", got, uuid)
	}
}

// 流式增量（stream_event → EventText/EventThinking）携带 active client uuid 作
// TurnID——mapAgentEvent 将其发为 text_delta.turnId，SSV2 reducer 不再跳过。
func TestStreamEventsCarryClientTurnID(t *testing.T) {
	cs := newTestClaudeSession(t)
	active := cs.registerClientTurn()

	feedFixture(t, cs, "testdata/client-uuid/turn-stream.jsonl")
	evts := collectAllEvents(cs)
	var sawText, sawThinking bool
	for _, evt := range evts {
		// system/init 的空内容通知帧（handleSystem 既有行为）不携带身份；
		// reducer 对空 delta 本就跳过，身份契约只约束有内容的增量。
		if evt.Type == core.EventText && evt.Content != "" {
			sawText = sawText || evt.TurnID == active && strings.Contains(evt.Content, "收到")
			if evt.TurnID == "" {
				t.Fatalf("identity-less text delta emitted: %+v", evt)
			}
		}
		if evt.Type == core.EventThinking && evt.TurnID == active {
			sawThinking = true
		}
	}
	if !sawText {
		t.Fatalf("streamed text delta missing TurnID=%q in %d events", active, len(evts))
	}
	if !sawThinking {
		t.Fatalf("thinking delta missing TurnID=%q in %d events", active, len(evts))
	}
}

// result 帧按官方 user_message_uuid 契约收口：EventResult 绑定该 turn 的 uuid，
// 队列消费到匹配位置。fixture 是真 2.1.234 样本（result 恒带 stamp）。
func TestResultSettlesClientTurnByOfficialStamp(t *testing.T) {
	cs := newTestClaudeSession(t)
	cs.pendingClientUUIDs = []string{"queued-uuid-2"}
	cs.activeClientUUID = probeFixtureUUID

	feedFixture(t, cs, "testdata/client-uuid/turn-stream.jsonl")
	evts := collectAllEvents(cs)
	var result *core.Event
	for i := range evts {
		if evts[i].Type == core.EventResult {
			result = &evts[i]
		}
	}
	if result == nil {
		t.Fatal("no EventResult emitted")
	}
	if result.TurnID != probeFixtureUUID {
		t.Fatalf("EventResult.TurnID = %q, want %q", result.TurnID, probeFixtureUUID)
	}
	if got := cs.currentClientTurnID(); got != "queued-uuid-2" {
		t.Fatalf("after settle active = %q, want queued-uuid-2", got)
	}
}

// turn 进行中再次 Send 进 FIFO 队列（CLI queue 语义），首个 result 只消费 active。
func TestSecondSendQueuesWhileTurnActive(t *testing.T) {
	cs := newTestClaudeSession(t)
	first := cs.registerClientTurn()
	second := cs.registerClientTurn()
	if first == second {
		t.Fatal("register returned same uuid twice")
	}
	if cs.currentClientTurnID() != first {
		t.Fatalf("active = %q, want first %q", cs.currentClientTurnID(), first)
	}

	cs.settleClientTurn(first)
	if got := cs.currentClientTurnID(); got != second {
		t.Fatalf("after settle active = %q, want second %q", got, second)
	}
}

// 无 stamp 的老 producer result：保守清空 active（防下一个 turn 串位），不 panic。
func TestResultWithoutStampClearsActive(t *testing.T) {
	cs := newTestClaudeSession(t)
	cs.registerClientTurn()
	cs.registerClientTurn() // 队列

	settled := cs.settleClientTurn("")
	if settled == "" {
		t.Fatal("settled should return the previously active uuid")
	}
	if got := cs.currentClientTurnID(); got == "" {
		t.Fatal("queued uuid should take over after clear")
	}
}

// 事件驱动 drain（owner 2026-09-05 复盘）：首条 stream_event 到达即关闭 drain
// 窗口，本帧事件不再被丢弃（原实现整窗口 return，流式头几帧靠 12s watchdog
// 侥幸早关才没丢）。
func TestDrainClosesOnFirstStreamEvent(t *testing.T) {
	cs := newTestClaudeSession(t)
	cs.registerClientTurn()
	cs.historyDraining.Store(true)

	feedFixture(t, cs, "testdata/client-uuid/turn-stream.jsonl")
	if cs.historyDraining.Load() {
		t.Fatal("drain still open after first stream_event")
	}
	evts := collectAllEvents(cs)
	sawText := false
	for _, evt := range evts {
		if evt.Type == core.EventText {
			sawText = true
		}
	}
	if !sawText {
		t.Fatalf("streamed text dropped while drain window was open: %d events", len(evts))
	}
}
