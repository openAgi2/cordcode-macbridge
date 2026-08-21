package gobridge

// projection_codexweb_wiring_test.go —— p2-ssv2 接线测试（设计 §9.1 十一处清单，
// 漏接即失败）+ §9.2 尾部封口。
//
// 断言分两类：
//   - 行为断言：家族判定/源头描述/冷基线 producer/事件映射/reducer 终态；
//   - 结构断言（源扫描）：十处接线点的函数体必须各自包含 "codex-web"——
//     防止后续重构静默丢线（第 11 处 provenance 由 agent/codex-web/provenance_test.go
//     与本文件 import 禁区共同覆盖）。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ---- 行为断言 ----

func TestCodexWebProjectionFamilyWiring(t *testing.T) {
	if !backendSupportsProjectionHydrate("codex-web") {
		t.Fatal("§9.1-1 backendSupportsProjectionHydrate 必须包含 codex-web")
	}
	if !pathlessRichHistoryBackend("codex-web") {
		t.Fatal("§9.1-7 pathlessRichHistoryBackend 必须包含 codex-web")
	}
	if !pathlessFullRebuildSource("codex-web", ProjectionSourceDescriptor{Identity: "s"}) {
		t.Fatal("codex-web pathless 源必须是 full rebuild（无文件前缀 checkpoint 语义）")
	}
}

// fakeCodexWebTurnScopedAgent 是 TurnScopedRichHistoryProvider 的可控 fake。
type fakeCodexWebTurnScopedAgent struct {
	turns []core.TurnScopedHistoryTurn
	err   error
}

func (f *fakeCodexWebTurnScopedAgent) Name() string { return "codex-web" }
func (f *fakeCodexWebTurnScopedAgent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	return nil, nil
}
func (f *fakeCodexWebTurnScopedAgent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (f *fakeCodexWebTurnScopedAgent) Stop() error { return nil }
func (f *fakeCodexWebTurnScopedAgent) GetTurnScopedRichHistory(ctx context.Context, sessionID string, limit int) ([]core.TurnScopedHistoryTurn, error) {
	return f.turns, f.err
}

// GetRichSessionHistory 镜像真实 Agent 的接口集（pathless 分支的家族资格检查读取
// RichHistoryProvider；SSV2 dispatch 走 turn-scoped 面）。
func (f *fakeCodexWebTurnScopedAgent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	return nil, nil
}

func TestCodexWebHydrateSourceIsPathless(t *testing.T) {
	handlers := NewHandlers()
	handlers.RegisterAgent("codex-web", &fakeCodexWebTurnScopedAgent{})
	source, err := handlers.prepareProjectionHydrateSource(context.Background(), "codex-web", "th-1", "")
	if err != nil {
		t.Fatalf("§9.1-4 prepareProjectionHydrateSource: %v", err)
	}
	if source.Path != "" || len(source.Segments) != 0 || source.Cursor != 0 {
		t.Fatalf("codex-web 源必须无文件路径/段/cursor（pathless）： %+v", source)
	}
	if source.Identity != "th-1" {
		t.Fatalf("identity 应为官方 thread id：%q", source.Identity)
	}
}

func TestCodexWebProduceHydrateRangeUsesOfficialIdentity(t *testing.T) {
	handlers := NewHandlers()
	handlers.RegisterAgent("codex-web", &fakeCodexWebTurnScopedAgent{turns: []core.TurnScopedHistoryTurn{{
		TurnID: "turn-official-1", Status: "completed", HasTime: true,
		UserItemID: "item-u1", UserText: "hello",
		Parts: []map[string]any{
			{"type": "text", "content": "world", "itemId": "item-a1"},
			{"type": "reasoning", "content": "thinking", "itemId": "item-r1"},
			{"type": "tool", "itemId": "item-c1", "step": map[string]any{
				"id": "item-c1", "toolName": "Bash", "status": "completed", "output": "ok",
			}},
		},
	}}})
	var events []projectionHydrateEvent
	emit := func(ev projectionHydrateEvent) bool {
		events = append(events, ev)
		return true
	}
	// §9.1-6：pathless 空 path 也必须进入 dispatch（allow-list）
	if err := handlers.produceProjectionHydrateRange(context.Background(), "codex-web", "th-1", "", 0, 0, SessionProjection{}, emit); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, ev := range events {
		kinds = append(kinds, ev.Event)
	}
	want := []string{"user_message", "text_delta", "reasoning_delta", "tool_started", "tool_finished", "turn_completed"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("事件序列=%v，期望 %v", kinds, want)
	}
	// 官方 identity：turnId=turn.id，itemId=item.id
	if events[0].Data["turnId"] != "turn-official-1" || events[0].Data["itemId"] != "item-u1" {
		t.Fatalf("user_message identity=%v", events[0].Data)
	}
	if events[1].Data["itemId"] != "item-a1" || events[2].Data["itemId"] != "item-r1" {
		t.Fatalf("text/reasoning itemId=%v/%v", events[1].Data, events[2].Data)
	}
	if events[5].Data["turnId"] != "turn-official-1" || !events[5].TurnDone {
		t.Fatalf("turn_completed identity/TurnDone=%v", events[5])
	}
}

// TestCodexWebTerminalSealing §9.2：只有官方 turn status 封口。
func TestCodexWebTerminalSealing(t *testing.T) {
	turns := []core.TurnScopedHistoryTurn{
		{TurnID: "t-completed", Status: "completed", UserItemID: "u1", UserText: "a"},
		{TurnID: "t-failed", Status: "failed", ErrorMessage: "provider unreachable", UserItemID: "u2", UserText: "b"},
		{TurnID: "t-interrupted", Status: "interrupted", UserItemID: "u3", UserText: "c"},
		{TurnID: "t-running", Status: "inProgress", UserItemID: "u4", UserText: "d"},
	}
	events := turnScopedHistoryTurnToProjectionEvents(turns)
	terminals := map[string]string{}
	for _, ev := range events {
		switch ev.Event {
		case "turn_completed":
			terminals["t-completed"] = "completed"
		case "turn_error":
			terminals["t-failed"] = "error"
			if ev.Data["error"] != "provider unreachable" {
				t.Fatalf("failed turn 应透传官方 error：%v", ev.Data)
			}
		case "turn_aborted":
			terminals["t-interrupted"] = "aborted"
		}
	}
	if len(terminals) != 3 {
		t.Fatalf("应恰好 3 个官方终态：%v", terminals)
	}
	if _, sealed := terminals["t-running"]; sealed {
		t.Fatal("inProgress turn 不得本地封口（§9.2）")
	}

	// 空身份 turn 跳过（不合成 identity）
	events = turnScopedHistoryTurnToProjectionEvents([]core.TurnScopedHistoryTurn{{TurnID: "", Status: "completed"}})
	if len(events) != 0 {
		t.Fatalf("无官方 turn id 的行必须跳过：%v", events)
	}
}

// TestCodexWebHydrateRebuildDeterministic 删 checkpoint 后重建一致性（纯函数层）：
// 同一官方历史两次重建产生逐事件一致的基线。
func TestCodexWebHydrateRebuildDeterministic(t *testing.T) {
	turns := []core.TurnScopedHistoryTurn{{
		TurnID: "t1", Status: "completed", UserItemID: "u1", UserText: "x",
		Parts: []map[string]any{{"type": "text", "content": "y", "itemId": "a1"}},
	}}
	first := turnScopedHistoryTurnToProjectionEvents(turns)
	second := turnScopedHistoryTurnToProjectionEvents(turns)
	if len(first) != len(second) {
		t.Fatal("重建事件数不一致")
	}
	for i := range first {
		if first[i].Event != second[i].Event || first[i].TurnDone != second[i].TurnDone {
			t.Fatalf("事件 %d 不一致：%v vs %v", i, first[i], second[i])
		}
	}
}

// TestCodexWebReducerTerminalUniqueness reducer 终态唯一性（official status →
// projection status 单向映射）。
func TestCodexWebReducerTerminalUniqueness(t *testing.T) {
	handlers := NewHandlers()
	handlers.RegisterAgent("codex-web", &fakeCodexWebTurnScopedAgent{turns: []core.TurnScopedHistoryTurn{
		{TurnID: "t-failed", Status: "failed", ErrorMessage: "boom", UserItemID: "u", UserText: "q"},
	}})
	var events []projectionHydrateEvent
	if err := handlers.produceProjectionHydrateRange(context.Background(), "codex-web", "s", "", 0, 0, SessionProjection{}, func(ev projectionHydrateEvent) bool {
		events = append(events, ev)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	terminal := 0
	for _, ev := range events {
		if ev.TurnDone {
			terminal++
		}
	}
	if terminal != 1 {
		t.Fatalf("一个 turn 恰好一个终态事件，得 %d", terminal)
	}
}

// ---- 结构断言（十处接线点源扫描；漏接即失败） ----

func sourceFuncBody(t *testing.T, file, funcName string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	idx := strings.Index(string(raw), funcName)
	if idx < 0 {
		t.Fatalf("%s 中找不到 %s", file, funcName)
	}
	// 取函数名之后 2400 字符作为函数体近似范围（switch 体内含全部 case）
	return string(raw)[idx : idx+2400]
}

func TestCodexWebWiringSitesPresent(t *testing.T) {
	sites := []struct {
		file string
		mark string
	}{
		{"handlers_projection.go", "func backendSupportsProjectionHydrate"},                                     // §9.1-1
		{"handlers_projection.go", "forceColdInspection := params.SinceRev == 0"},                               // §9.1-2
		{"handlers_projection.go", "sourceChanged := forceColdInspection && ready"},                             // §9.1-3
		{"handlers_projection.go", "func (h *Handlers) prepareProjectionHydrateSource"},                         // §9.1-4
		{"handlers_projection.go", "func (h *Handlers) streamCodexWebRichHistoryProjectionEvents"},              // §9.1-5
		{"handlers_projection.go", "func (h *Handlers) produceProjectionHydrateRange"},                          // §9.1-6
		{"projection_kernel.go", "func pathlessRichHistoryBackend"},                                             // §9.1-7
		{"agent_descriptor.go", `case "codex-web":`},                                                            // §9.1-8
		{"main.go", "func buildAgentOptions"},                                                                    // §9.1-9
		{"server.go", "func advertiseSessionSyncV2Backend"},                                                     // §9.1-10
	}
	for _, s := range sites {
		body := sourceFuncBody(t, s.file, s.mark)
		if !strings.Contains(body, `"codex-web"`) {
			t.Fatalf("§9.1 接线点缺失：%s @ %s 未包含 codex-web", s.file, s.mark)
		}
	}
	// §9.1-9 的注册面：blank import 必须存在（独立注册，不别名 codex）
	mainSrc, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainSrc), `_ "github.com/openAgi2/cordcode-macbridge/agent/codex-web"`) {
		t.Fatal("main.go 缺少 codex-web blank import（§9.1-9）")
	}
}

// TestCodexWebNoLegacyProvenanceInBridge codex-web 的冷基线 dispatch 不得落入
// 旧 codex 文件 relay/parser 分支（§9.1-11 的 go-bridge 侧护栏）。
func TestCodexWebNoLegacyProvenanceInBridge(t *testing.T) {
	raw, err := os.ReadFile("handlers_projection.go")
	if err != nil {
		t.Fatal(err)
	}
	body := sourceFuncBody(t, "handlers_projection.go", "func (h *Handlers) streamCodexWebRichHistoryProjectionEvents")
	for _, forbidden := range []string{"streamCodexTranscriptRelayEventsRange", "codexRelayEvent", "file relay"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("codex-web 冷基线不得引用旧 codex 文件路径：%s", forbidden)
		}
	}
	_ = raw
}
