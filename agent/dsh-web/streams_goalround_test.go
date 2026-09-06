package dshweb

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// 2026-09-05 23:25 真机 goal 轮返工⑥回放测试。
//
// Fixture：testdata/goal_round_seat_windows.jsonl —— 座位
// session-3eacd40e（dsh-v0.1.3-alpha.1 = d347e70，官方 web 同源）真实事件
// 三个窗口原样导出（seq 连续段，跨窗口以 mid-log join 采纳语义衔接）：
//
//   - 基线窗口 1-14：turn/start 1 + user/message(kind user, seq 8) +
//     agent-instructions(9) / plugin(10) / skill-catalog(11) 基线注入——
//     修复前 9/11 每次座位收编回放都触发 codec reset；
//   - goal 窗口 1469-1492：turn/end 2 → 裸 /goal(1472/1473 usage) →
//     /goal 创作钢铁侠…(1474/1476) → goal/change(1475) → inbox spliced →
//     turn/start 3 → user/message source.kind "goal"(1481，修复前 P0 reset
//     帧) → assistant/chunk 流；
//   - 权威替换窗口 16578-16596：todo_write 的 delta 流为带空格 JSON、
//     block-end(16586) 为归一化紧凑 JSON（修复前 "arguments disagree"
//     reset），assistant/message(16589) settlement 与 delta 并存（修复前
//     "text disagrees" reset），tool/call(16590) 权威参数。
//
// 官方锚点：ui-chat conversation-nodes/message.ts（kind != "user" →
// ContextMessageNode，从不 reset）、partial.ts（block-end /
// assistant/message 整值替换累积）、ui-goal goal-command-input.ts（goal
// 专属输入行 "/goal" + args 去尾空白）。

func loadSeatWindows(t *testing.T) [][]byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/goal_round_seat_windows.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var frames [][]byte
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			frames = append(frames, []byte(line))
		}
	}
	if len(frames) == 0 {
		t.Fatal("empty fixture")
	}
	return frames
}

// replayWindows feeds each contiguous window through its own fresh codec
// (mid-log join adoption, mirror of feedWithReset's reset-then-continue path)
// and returns the folded core events plus the number of codec resets.
func replayWindows(t *testing.T) ([]core.Event, int) {
	t.Helper()
	frames := loadSeatWindows(t)
	var events []core.Event
	resets := 0
	codec := newSessionCodec("session-3e")
	prevSeq := int64(-1)
	for _, raw := range frames {
		var env sessionEventWire
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("unmarshal fixture frame: %v", err)
		}
		if prevSeq >= 0 && env.Seq != prevSeq+1 {
			codec = newSessionCodec("session-3e") // new window: mid-log join
		}
		prevSeq = env.Seq
		out, err := codec.apply(&env)
		if err != nil {
			if _, ok := err.(*codecReset); ok {
				resets++
				codec = newSessionCodec("session-3e")
				continue
			}
			t.Fatalf("non-reset error at seq %d: %v", env.Seq, err)
		}
		events = append(events, out...)
	}
	return events, resets
}

// TestGoalRoundReplayNoCodecReset：三个真实窗口全程零 codec reset。
// 修复前命中五次（seq 9/11 基线注入、1481 goal 注入、16586/16589 权威替换
// 分歧），23:25:18 的一次直接拆掉 goal 轮整条 live 流（owner 真机 P0）。
func TestGoalRoundReplayNoCodecReset(t *testing.T) {
	_, resets := replayWindows(t)
	if resets != 0 {
		t.Fatalf("codec resets = %d, want 0 (real goal-round seat windows must fold without teardown)", resets)
	}
}

// TestGoalRoundReplayCommandInputLine：goal 命令折叠携带官方输入行
// （goalCommandText："/goal" + args 去尾空白），裸 usage 行与带目标行都在，
// settle 续接输入行；非 goal 命令不带。
func TestGoalRoundReplayCommandInputLine(t *testing.T) {
	events, _ := replayWindows(t)
	var bareRun, goalRun, goalSettle *core.SessionCommandEvent
	for i := range events {
		e := &events[i]
		if e.Type != core.EventSessionCommand || e.SessionCommand == nil {
			continue
		}
		sc := e.SessionCommand
		switch {
		case sc.CommandID == "cmd-89f0dffc-15" && sc.Kind == "running":
			bareRun = sc
		case sc.CommandID == "cmd-89f0dffc-16" && sc.Kind == "running":
			goalRun = sc
		case sc.CommandID == "cmd-89f0dffc-16" && sc.Kind == "success":
			goalSettle = sc
		}
	}
	if bareRun == nil || goalRun == nil || goalSettle == nil {
		t.Fatalf("missing command folds: bare=%v goalRun=%v settle=%v", bareRun, goalRun, goalSettle)
	}
	const want = "/goal 创作钢铁侠故事10000字左右，并写入 /tmp/demo-plan102.txt"
	if goalRun.InputLine != want {
		t.Fatalf("goal run InputLine = %q, want %q", goalRun.InputLine, want)
	}
	if goalSettle.InputLine != want {
		t.Fatalf("goal settle must carry the input line onward (reducer replaces the part wholesale), got %q", goalSettle.InputLine)
	}
	if bareRun.InputLine != "/goal" {
		t.Fatalf("bare goal run InputLine = %q, want %q", bareRun.InputLine, "/goal")
	}
}

// TestGoalRoundReplayStreamsIntoTurn3：goal 注入帧(1481)之后 turn 3 继续
// 流式（user/message source.kind "goal" 不再拆流）。窗口 3 的 tool/call
// (todo_write, seq 16590) 落在 turn 3 是拆流已修的直接证据——修复前 1481
// reset 后整条 turn 3 执行链丢失。turn id 为 codec 的 dshw-<prefix>-t<turn> 形。
func TestGoalRoundReplayStreamsIntoTurn3(t *testing.T) {
	events, _ := replayWindows(t)
	turn3 := "dshw-session-3e-t3"
	var sawTurn3, sawToolUse bool
	for _, e := range events {
		switch e.Type {
		case core.EventTurnStarted:
			if e.TurnID == turn3 {
				sawTurn3 = true
			}
		case core.EventToolUse:
			if e.TurnID == turn3 {
				sawToolUse = true
			}
		}
	}
	if !sawTurn3 {
		t.Fatal("turn 3 never started in replay")
	}
	if !sawToolUse {
		t.Fatal("no execution event reached turn 3 after the goal-round injection (stream was torn)")
	}
}

// TestGoalRoundReplayBaselineInjectionFolds：基线窗口的非用户注入
// （agent-instructions / skill-catalog / plugin）不再 reset，且唯一 user
// 消息（seq 8）照常成为用户气泡事件。
func TestGoalRoundReplayBaselineInjectionFolds(t *testing.T) {
	events, resets := replayWindows(t)
	if resets != 0 {
		t.Fatalf("baseline injections must not reset: resets=%d", resets)
	}
	sawUser := false
	for _, e := range events {
		if e.Type == core.EventUserMessage && strings.Contains(e.Content, "二郎神") {
			sawUser = true
		}
	}
	if !sawUser {
		t.Fatal("baseline window lost the kind=user message")
	}
}
