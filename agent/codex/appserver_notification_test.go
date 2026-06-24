package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// testdataDir 返回 testdata 目录路径。
func testdataDir() string {
	return filepath.Join("testdata")
}

// loadFixture 加载 testdata 中的 JSON fixture 文件，反序列化为 notification envelope。
func loadFixture(t *testing.T, name string) (method string, params json.RawMessage) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir(), name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var envelope rpcNotificationEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return envelope.Method, envelope.Params
}

// collectEvents 收集 handleNotification 产生的事件。
type mockCollector struct {
	events []core.Event
}

func newMockCollector() *mockCollector { return &mockCollector{} }

func (m *mockCollector) collect(ev core.Event) { m.events = append(m.events, ev) }

// runNotification 在测试 session 上运行 handleNotification 并收集事件。
func runNotification(t *testing.T, fixture string) *mockCollector {
	t.Helper()
	s := &appServerSession{
		events:  make(chan core.Event, 128),
		pending: make(map[int64]chan rpcResponseEnvelope),
	}
	s.alive.Store(true)
	s.threadID.Store("thread_abc123")

	method, params := loadFixture(t, fixture)

	done := make(chan struct{})
	coll := newMockCollector()
	go func() {
		defer close(done)
		for ev := range s.events {
			coll.collect(ev)
		}
	}()

	s.handleNotification(method, params)
	close(s.events)
	<-done
	return coll
}

// ── turn/started ──────────────────────────────────────────────────────

func TestFixture_TurnStarted(t *testing.T) {
	coll := runNotification(t, "turn_started.json")
	if len(coll.events) != 1 {
		t.Fatalf("turn/started should emit 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventTurnStarted {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventTurnStarted)
	}
}

// ── item/agentMessage/delta → EventText ────────────────────────────────

func TestFixture_AgentMessageDelta(t *testing.T) {
	coll := runNotification(t, "item_agentMessage_delta.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventText {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventText)
	}
	if coll.events[0].Content != "Hello, " {
		t.Errorf("content = %q, want %q", coll.events[0].Content, "Hello, ")
	}
}

func TestFixture_AgentMessageDelta_Second(t *testing.T) {
	coll := runNotification(t, "item_agentMessage_delta_second.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Content != "world!" {
		t.Errorf("content = %q, want %q", coll.events[0].Content, "world!")
	}
}

// ── item/agentMessage/completed (no delta seen) → queued in pendingMsgs ─

func TestFixture_AgentMessageCompleted_NoDelta(t *testing.T) {
	// agentMessage completed 不直接 emit，而是缓存到 pendingMsgs，
	// 在 turn/completed 时才 flush 为 EventText。
	// 所以单独处理 item/completed 时不会产生事件。
	coll := runNotification(t, "item_agentMessage_completed_empty_delta.json")
	if len(coll.events) != 0 {
		t.Errorf("agentMessage completed alone should not emit, got %d events", len(coll.events))
	}
}

// ── item/reasoning/textDelta → EventThinking ───────────────────────────

func TestFixture_ReasoningTextDelta(t *testing.T) {
	coll := runNotification(t, "item_reasoning_textDelta.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventThinking {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventThinking)
	}
	if coll.events[0].Content != "Let me analyze this..." {
		t.Errorf("content = %q, want %q", coll.events[0].Content, "Let me analyze this...")
	}
}

// ── item/reasoning/completed (no delta seen) → EventThinking ───────────

func TestFixture_ReasoningCompleted_NoDelta(t *testing.T) {
	coll := runNotification(t, "item_reasoning_completed_no_delta.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventThinking {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventThinking)
	}
}

// ── turn/plan/updated → EventPlan ─────────────────────────────────────

func TestFixture_TurnPlanUpdated(t *testing.T) {
	coll := runNotification(t, "turn_plan_updated.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventPlan {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventPlan)
	}
	if len(coll.events[0].Plan) != 3 {
		t.Errorf("plan items = %d, want 3", len(coll.events[0].Plan))
	}
}

// ── turn/completed → EventResult(Done:true) ───────────────────────────

func TestFixture_TurnCompleted(t *testing.T) {
	coll := runNotification(t, "turn_completed.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventResult {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventResult)
	}
	if !coll.events[0].Done {
		t.Error("Done should be true")
	}
}

// ── turn/question → EventQuestionAsked ────────────────────────────────

func TestFixture_TurnQuestion(t *testing.T) {
	coll := runNotification(t, "turn_question.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	ev := coll.events[0]
	if ev.Type != core.EventQuestionAsked {
		t.Errorf("event type = %q, want %q", ev.Type, core.EventQuestionAsked)
	}
	if ev.QuestionID != "q_001" {
		t.Errorf("question id = %q, want %q", ev.QuestionID, "q_001")
	}
	if ev.QuestionText != "Which file should I modify?" {
		t.Errorf("question text = %q, want %q", ev.QuestionText, "Which file should I modify?")
	}
	if len(ev.QuestionOpts) != 2 {
		t.Fatalf("options = %d, want 2", len(ev.QuestionOpts))
	}
	if ev.QuestionOpts[0].ID != "opt_a" {
		t.Errorf("option[0].ID = %q, want %q", ev.QuestionOpts[0].ID, "opt_a")
	}
	if !ev.Required {
		t.Error("Required should be true")
	}
}

// ── error notification → EventError ───────────────────────────────────

func TestFixture_ErrorNotification(t *testing.T) {
	coll := runNotification(t, "error_notification.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventError {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventError)
	}
}

// ── item/commandExecution → EventToolUse + EventToolResult ────────────

func TestFixture_CommandExecution(t *testing.T) {
	// started
	coll := runNotification(t, "item_commandExecution_started.json")
	if len(coll.events) != 1 {
		t.Fatalf("started: expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventToolUse {
		t.Errorf("started: event type = %q, want %q", coll.events[0].Type, core.EventToolUse)
	}
	if coll.events[0].ToolName != "Bash" {
		t.Errorf("started: tool name = %q, want Bash", coll.events[0].ToolName)
	}

	// completed
	coll = runNotification(t, "item_commandExecution_completed.json")
	if len(coll.events) != 1 {
		t.Fatalf("completed: expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventToolResult {
		t.Errorf("completed: event type = %q, want %q", coll.events[0].Type, core.EventToolResult)
	}
	if coll.events[0].ToolName != "Bash" {
		t.Errorf("completed: tool name = %q, want Bash", coll.events[0].ToolName)
	}
	if coll.events[0].ToolExitCode == nil || *coll.events[0].ToolExitCode != 0 {
		t.Errorf("completed: exit code = %v, want 0", coll.events[0].ToolExitCode)
	}
	if coll.events[0].ToolSuccess == nil || !*coll.events[0].ToolSuccess {
		t.Error("completed: ToolSuccess should be true")
	}
}

func TestFixture_CommandExecution_Failed(t *testing.T) {
	coll := runNotification(t, "item_commandExecution_failed.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].ToolExitCode == nil || *coll.events[0].ToolExitCode != 1 {
		t.Errorf("exit code = %v, want 1", coll.events[0].ToolExitCode)
	}
	if coll.events[0].ToolSuccess == nil || *coll.events[0].ToolSuccess {
		t.Error("ToolSuccess should be false for failed command")
	}
}

// ── item/fileChange → EventToolUse ────────────────────────────────────

func TestFixture_FileChange(t *testing.T) {
	coll := runNotification(t, "item_fileChange_started.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventToolUse {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventToolUse)
	}
	if coll.events[0].ToolName != "Patch" {
		t.Errorf("tool name = %q, want Patch", coll.events[0].ToolName)
	}
}

// ── item/contextCompaction/completed → EventContextCompressed ─────────

func TestFixture_ContextCompaction(t *testing.T) {
	coll := runNotification(t, "item_contextCompaction_completed.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventContextCompressed {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventContextCompressed)
	}
}

// ── item/mcpToolCall → EventToolResult ────────────────────────────────

func TestFixture_MCPToolCall(t *testing.T) {
	coll := runNotification(t, "item_mcpToolCall_completed.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventToolResult {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventToolResult)
	}
	if coll.events[0].ToolName != "search_repositories" {
		t.Errorf("tool name = %q, want search_repositories", coll.events[0].ToolName)
	}
}

// ── item/dynamicToolCall → EventToolResult ────────────────────────────

func TestFixture_DynamicToolCall(t *testing.T) {
	coll := runNotification(t, "item_dynamicToolCall_completed.json")
	if len(coll.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(coll.events))
	}
	if coll.events[0].Type != core.EventToolResult {
		t.Errorf("event type = %q, want %q", coll.events[0].Type, core.EventToolResult)
	}
	if coll.events[0].ToolName != "Read" {
		t.Errorf("tool name = %q, want Read", coll.events[0].ToolName)
	}
}

// ── unknown method → no events, no crash ──────────────────────────────

func TestFixture_UnknownMethod(t *testing.T) {
	coll := runNotification(t, "unknown_method.json")
	if len(coll.events) != 0 {
		t.Errorf("unknown method should not emit events, got %d", len(coll.events))
	}
}

// ── malformed params → no events, no crash ────────────────────────────

func TestFixture_MalformedParams(t *testing.T) {
	method, params := loadFixture(t, "malformed_params.json")
	s := &appServerSession{
		events:  make(chan core.Event, 128),
		pending: make(map[int64]chan rpcResponseEnvelope),
	}
	s.alive.Store(true)
	// Should not panic on malformed params
	s.handleNotification(method, params)
}
