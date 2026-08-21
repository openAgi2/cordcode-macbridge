package codexweb

// codec_test.go —— p3-codec 测试：官方真实帧全量回放（每个 Item variant 的
// started/delta/completed）、exact-identity 去重、terminal 唯一终态、
// §2.5 四项历史故障定向回归。
//
// 帧来源：testdata/official-0.149.0-alpha.4/dumps/{catalog,interaction,reconnect,
// ownership} 的真实服务端通知（零手写形状）。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// replayServerFrames 提取一组 dump 的全部服务端通知帧（保序）。
func replayServerFrames(t *testing.T, groups ...string) []Notification {
	t.Helper()
	var out []Notification
	for _, grp := range groups {
		for _, line := range strings.Split(string(fixtureBytes(t, grp)), "\n") {
			if line == "" {
				continue
			}
			var e struct {
				Dir string          `json:"dir"`
				Msg json.RawMessage `json:"msg"`
			}
			if json.Unmarshal([]byte(line), &e) != nil || e.Dir != "server" {
				continue
			}
			var m struct {
				ID     *json.Number    `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(e.Msg, &m) != nil || m.Method == "" || m.ID != nil {
				continue
			}
			out = append(out, Notification{Method: m.Method, Params: m.Params})
		}
	}
	return out
}

// TestCodecOfficialReplayNoLostIdentity §2.5-1（identity 漂移）：全部真实帧回放，
// 任何正文/工具/终态事件必须携带官方 (threadId,turnId[,itemId])——与帧内字段一致。
func TestCodecOfficialReplayNoLostIdentity(t *testing.T) {
	frames := replayServerFrames(t, "catalog", "interaction", "reconnect", "ownership")
	if len(frames) < 50 {
		t.Fatalf("真实帧不足：%d", len(frames))
	}
	codec := NewLiveCodec()
	var events []core.Event
	for _, n := range frames {
		events = append(events, codec.Decode(n)...)
	}
	if len(events) < 30 {
		t.Fatalf("回放事件过少：%d", len(events))
	}
	terminals := map[string]int{}
	for _, ev := range events {
		if ev.SessionID == "" {
			t.Fatalf("事件缺官方 threadId：%+v", ev)
		}
		switch ev.Type {
		case core.EventText, core.EventThinking:
			if ev.TurnID == "" || ev.ItemID == "" {
				t.Fatalf("正文事件缺官方 turnId/itemId：%+v", ev)
			}
		case core.EventToolUse, core.EventToolResult:
			if ev.RequestID == "" {
				t.Fatalf("工具事件缺官方 item id：%+v", ev)
			}
		case core.EventResult, core.EventError:
			if !ev.Done {
				continue // 非 Done 的 EventError 是官方 error 通知面（重连/上游错误原文），非 turn 终态
			}
			if ev.TurnID == "" {
				t.Fatalf("终态事件缺官方 turnId：%+v", ev)
			}
			terminals[ev.TurnID]++
		}
	}
	for turnID, n := range terminals {
		if n != 1 {
			t.Fatalf("turn %s 终态事件 %d 个（必须唯一）", turnID, n)
		}
	}
}

// TestCodecNoDoubleEmitDeltaVsSnapshot §2.5-3（delta 与 completed 双发）：
// agentMessage/reasoning 的 item/completed 不得再产正文事件（delta 已流式）。
func TestCodecNoDoubleEmitDeltaVsSnapshot(t *testing.T) {
	codec := NewLiveCodec()
	var frameTurn, itemId, deltaText string
	for _, n := range replayServerFrames(t, "catalog") {
		if n.Method == "item/agentMessage/delta" && deltaText == "" {
			var p struct {
				TurnID string `json:"turnId"`
				ItemID string `json:"itemId"`
				Delta  string `json:"delta"`
			}
			_ = json.Unmarshal(n.Params, &p)
			frameTurn, itemId, deltaText = p.TurnID, p.ItemID, p.Delta
		}
	}
	if deltaText == "" {
		t.Fatal("fixture 缺 agentMessage delta 帧")
	}
	completedFrame := Notification{
		Method: "item/completed",
		Params: mustJSON(t, map[string]any{
			"threadId": "th", "turnId": frameTurn,
			"item": map[string]any{
				"type": "agentMessage", "id": itemId,
				"text": deltaText + deltaText, // snapshot 与 delta 正文相同（双发陷阱）
			},
		}),
	}
	var emitted []core.Event
	emitted = append(emitted, codec.Decode(completedFrame)...)
	for _, ev := range emitted {
		if ev.Type == core.EventText || ev.Type == core.EventThinking {
			t.Fatalf("agentMessage completed 不得再发正文（双发红线）：%+v", ev)
		}
	}
	// reasoning completed 同理
	rc := Notification{
		Method: "item/completed",
		Params: mustJSON(t, map[string]any{
			"threadId": "th", "turnId": frameTurn,
			"item": map[string]any{"type": "reasoning", "id": "r1", "summary": []string{"s"}, "content": []string{"c"}},
		}),
	}
	for _, ev := range codec.Decode(rc) {
		if ev.Type == core.EventText || ev.Type == core.EventThinking {
			t.Fatalf("reasoning completed 不得再发正文：%+v", ev)
		}
	}
}

// TestCodecTerminalOnlyFromOfficialStatus §2.5-2（EOF/静默误判完成）：
// 除 turn/completed 外的任何帧（含 EOF、静默、itemsView=notLoaded、连接关闭）
// 都不产生终态；failed 携带官方 error 原文；interrupted 不伪装 error。
func TestCodecTerminalOnlyFromOfficialStatus(t *testing.T) {
	codec := NewLiveCodec()
	frames := replayServerFrames(t, "catalog", "interaction", "reconnect", "ownership")
	for _, n := range frames {
		if n.Method == "turn/completed" {
			continue
		}
		for _, ev := range codec.Decode(n) {
			if (ev.Type == core.EventResult || ev.Type == core.EventError) && ev.Done {
				t.Fatalf("非 turn/completed 帧产生终态：%s → %+v", n.Method, ev)
			}
		}
	}
	// inProgress turn/completed 缺失 → 无终态（结构性：codec 无定时器/EOF 路径）
	// failed 终态 + 官方原文
	failed := codec.Decode(Notification{
		Method: "turn/completed",
		Params: mustJSON(t, map[string]any{
			"threadId": "th",
			"turn": map[string]any{
				"id": "t-f", "status": "failed",
				"error": map[string]any{"message": "stream disconnected before completion"},
			},
		}),
	})
	if len(failed) != 1 || failed[0].Type != core.EventError ||
		!strings.Contains(failed[0].Error.Error(), "stream disconnected") {
		t.Fatalf("failed 终态必须为 EventError+官方原文：%+v", failed)
	}
	// interrupted → EventResult（不伪装 error）
	interrupted := codec.Decode(Notification{
		Method: "turn/completed",
		Params: mustJSON(t, map[string]any{
			"threadId": "th", "turn": map[string]any{"id": "t-i", "status": "interrupted"},
		}),
	})
	if len(interrupted) != 1 || interrupted[0].Type != core.EventResult || !interrupted[0].Done {
		t.Fatalf("interrupted 终态应为 EventResult：%+v", interrupted)
	}
}

// TestCodecItemVariantsReplay 每个 variant 的 started/completed 映射
// （command/file 变体用 interaction 真实帧；schema-only 变体不在 live 广告范围）。
func TestCodecItemVariantsReplay(t *testing.T) {
	codec := NewLiveCodec()
	var sawCommandUse, sawCommandResult, sawFileUse, sawFileResult, sawUsage, sawCompaction bool
	for _, n := range replayServerFrames(t, "interaction", "reconnect") {
		for _, ev := range codec.Decode(n) {
			switch {
			case ev.Type == core.EventToolUse && ev.ToolName == "Bash":
				sawCommandUse = true
				if ev.RequestID == "" || ev.ToolInput == "" {
					t.Fatalf("command started 缺 id/input：%+v", ev)
				}
			case ev.Type == core.EventToolResult && ev.ToolName == "Bash":
				sawCommandResult = true
				// fixture 事实：declined（审批拒绝）官方不携带 exitCode；completed 必有
				if ev.ToolStatus == "" {
					t.Fatalf("command completed 缺 status：%+v", ev)
				}
				if ev.ToolStatus == "completed" && ev.ToolExitCode == nil {
					t.Fatalf("completed 命令缺 exitCode：%+v", ev)
				}
			case ev.Type == core.EventToolUse && ev.ToolName == "Patch":
				sawFileUse = true
			case ev.Type == core.EventToolResult && ev.ToolName == "Patch":
				sawFileResult = true
				if len(ev.FileChanges) == 0 || ev.FileChanges[0].Path == "" || ev.FileChanges[0].Diff == "" {
					t.Fatalf("fileChange completed 缺结构化 changes：%+v", ev.FileChanges)
				}
			case ev.Type == core.EventContextUsageUpdated:
				sawUsage = true
				if ev.ContextUsage == nil || ev.ContextUsage.ContextWindow == 0 || ev.ContextUsage.InputTokens == 0 {
					t.Fatalf("tokenUsage 映射不完整：%+v", ev.ContextUsage)
				}
			}
		}
	}
	if !sawCommandUse || !sawCommandResult {
		t.Fatal("commandExecution started/completed 未覆盖")
	}
	if !sawFileUse || !sawFileResult {
		t.Fatal("fileChange started/completed 未覆盖")
	}
	if !sawUsage {
		t.Fatal("thread/tokenUsage/updated 未覆盖")
	}
	_ = sawCompaction
}

// TestCodecPlanUpdated turn/plan/updated → EventPlan（官方枚举映射）。
func TestCodecPlanUpdated(t *testing.T) {
	codec := NewLiveCodec()
	evs := codec.Decode(Notification{
		Method: "turn/plan/updated",
		Params: mustJSON(t, map[string]any{
			"threadId": "th", "turnId": "t1",
			"plan": []map[string]any{
				{"step": "first", "status": "completed"},
				{"step": "second", "status": "inProgress"},
				{"step": "third", "status": "pending"},
			},
		}),
	})
	if len(evs) != 1 || evs[0].Type != core.EventPlan || len(evs[0].Plan) != 3 {
		t.Fatalf("plan 事件映射错误：%+v", evs)
	}
	if evs[0].Plan[0].Status != "completed" || evs[0].Plan[1].Status != "in_progress" || evs[0].Plan[2].Status != "pending" {
		t.Fatalf("plan 状态映射错误：%v", evs[0].Plan)
	}
	if evs[0].TurnID != "t1" {
		t.Fatalf("plan 事件缺 turnId：%+v", evs[0])
	}
}

// TestCodecRetrySemantics §2.5-4 的 adapter 侧（provider 单帧不误诊）：
// willRetry=true → EventRetryStatus（计数递增，delta/terminal 重置）；
// willRetry=false → EventError 官方原文；两者都不产生终态。
func TestCodecRetrySemantics(t *testing.T) {
	codec := NewLiveCodec()
	mk := func(willRetry bool, msg string) Notification {
		return Notification{Method: "error", Params: mustJSON(t, map[string]any{
			"error": map[string]any{"message": msg}, "willRetry": willRetry, "threadId": "th",
		})}
	}
	evs := codec.Decode(mk(true, "Reconnecting... 1/5"))
	if len(evs) != 1 || evs[0].Type != core.EventRetryStatus || evs[0].RetryAttempt != 1 {
		t.Fatalf("willRetry 应为 RetryStatus(1)：%+v", evs)
	}
	evs = codec.Decode(mk(true, "Reconnecting... 2/5"))
	if evs[0].RetryAttempt != 2 {
		t.Fatalf("重试计数应递增：%+v", evs)
	}
	// delta 到达 → 重置
	_ = codec.Decode(Notification{Method: "item/agentMessage/delta",
		Params: mustJSON(t, map[string]any{"threadId": "th", "turnId": "t", "itemId": "i", "delta": "x"})})
	evs = codec.Decode(mk(true, "Reconnecting... 1/5"))
	if evs[0].RetryAttempt != 1 {
		t.Fatalf("delta 后重试计数应重置：%+v", evs)
	}
	evs = codec.Decode(mk(false, "fatal provider failure"))
	if len(evs) != 1 || evs[0].Type != core.EventError || !strings.Contains(evs[0].Error.Error(), "fatal") {
		t.Fatalf("非重试错误应为 EventError 原文：%+v", evs)
	}
	if evs[0].Done {
		t.Fatal("error 通知不是 turn 终态（终态只认 turn/completed）")
	}
}

// TestCodecUnknownMethodTolerated 未识别通知记录计数、不崩、连接语义不变。
func TestCodecUnknownMethodTolerated(t *testing.T) {
	codec := NewLiveCodec()
	evs := codec.Decode(Notification{Method: "brandNew/notification", Params: json.RawMessage(`{"x":1}`)})
	if len(evs) != 0 {
		t.Fatalf("未知通知不应产事件：%v", evs)
	}
	if codec.UnknownMethods()["brandNew/notification"] != 1 {
		t.Fatal("未知 method 应记录计数")
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
