package codexremote

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// P5.7: collabAgentToolCall items (upstream item.rs:362) must fold into the
// shared workflow-card channel instead of the pre-P5.7 silent default drop.
// Wire shapes mirror the official v2 serialization: tag=type, camelCase fields.

func collabStartedParams(receivers string, states string) json.RawMessage {
	return json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"collabAgentToolCall","id":"call-1","tool":"spawnAgent","status":"inProgress","senderThreadId":"th","receiverThreadIds":[` + receivers + `],"prompt":"写贝索斯约300字故事","agentsStates":{` + states + `}}}`)
}

func TestCollabItemStartedFoldsWorkflowCard(t *testing.T) {
	codec := NewLiveCodec()
	events := codec.Decode(Notification{Method: "item/started", Params: collabStartedParams(`"agent-a"`, `"agent-a":{"status":"running"}`)})
	if len(events) != 1 || events[0].Type != core.EventWorkflowRun || events[0].WorkflowRun == nil {
		t.Fatalf("started events = %+v", events)
	}
	wr := events[0].WorkflowRun
	if wr.RunID != "codex-collab:turn" || wr.Status != "running" {
		t.Fatalf("card header = %+v", wr)
	}
	if wr.Name != "Subagents" {
		t.Fatalf("card name = %q", wr.Name)
	}
	if len(wr.Phases) != 1 || wr.Phases[0].Phase != nil {
		t.Fatalf("phases = %+v", wr.Phases)
	}
	members := wr.Phases[0].Members
	if len(members) != 1 || members[0].Label != "写贝索斯约300字故事" || members[0].ChildSessionID != "agent-a" || members[0].Status != "running" || members[0].Seq != 1 {
		t.Fatalf("members = %+v", members)
	}
	if events[0].SessionID != "th" || events[0].TurnID != "turn" {
		t.Fatalf("identity = %+v", events[0])
	}
}

func TestCollabItemCompletedRefreshesMemberStates(t *testing.T) {
	codec := NewLiveCodec()
	if events := codec.Decode(Notification{Method: "item/started", Params: collabStartedParams(`"agent-a","agent-b"`, `"agent-a":{"status":"running"},"agent-b":{"status":"running"}`)}); len(events) != 1 {
		t.Fatalf("started = %+v", events)
	}
	events := codec.Decode(Notification{Method: "item/completed", Params: collabStartedParams(`"agent-a","agent-b"`, `"agent-a":{"status":"completed","message":"done"},"agent-b":{"status":"running"}`)})
	if len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("completed = %+v", events)
	}
	wr := events[0].WorkflowRun
	if wr.Status != "running" { // item/started and completed both carry inProgress here; call still live
		t.Fatalf("card status = %q", wr.Status)
	}
	members := wr.Phases[0].Members
	if len(members) != 2 {
		t.Fatalf("members = %+v", members)
	}
	if members[0].Status != "completed" || members[1].Status != "running" {
		t.Fatalf("member statuses = %+v", members)
	}
}

func TestCollabStatusMappingVocabulary(t *testing.T) {
	codec := NewLiveCodec()
	events := codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"collabAgentToolCall","id":"call-2","tool":"spawnAgent","status":"completed","senderThreadId":"th","receiverThreadIds":["p","r","e","i","s","u","x"],"agentsStates":{"p":{"status":"pendingInit"},"r":{"status":"running"},"e":{"status":"completed"},"i":{"status":"errored"},"s":{"status":"shutdown"},"u":{"status":"interrupted"},"x":{"status":"quantum"}}}}`)})
	if len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("events = %+v", events)
	}
	members := events[0].WorkflowRun.Phases[0].Members
	want := []string{"running", "running", "completed", "failed", "completed", "interrupted", "quantum"}
	for i, m := range members {
		if m.Status != want[i] {
			t.Fatalf("member %d status = %q want %q", i, m.Status, want[i])
		}
	}
}

func TestCollabReceiverMissingFromStatesUsesSpawnOutcome(t *testing.T) {
	codec := NewLiveCodec()
	// A completed spawn means the child was created, not that the child itself
	// completed. Without a child state it therefore remains running.
	events := codec.Decode(Notification{Method: "item/completed", Params: collabStartedParams(`"agent-a"`, ``)})
	if len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("events = %+v", events)
	}
	members := events[0].WorkflowRun.Phases[0].Members
	if len(members) != 1 || members[0].Status != "running" {
		t.Fatalf("members = %+v", members)
	}
	// Failed call: fallback becomes failed.
	codec = NewLiveCodec()
	events = codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"collabAgentToolCall","id":"call-3","tool":"spawnAgent","status":"failed","senderThreadId":"th","receiverThreadIds":["agent-z"],"agentsStates":{}}}`)})
	if len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("failed events = %+v", events)
	}
	if members := events[0].WorkflowRun.Phases[0].Members; len(members) != 1 || members[0].Status != "failed" {
		t.Fatalf("failed members = %+v", members)
	}
}

func TestCollabOperationsFoldIntoOneTurnWorkflow(t *testing.T) {
	codec := NewLiveCodec()
	first := codec.Decode(Notification{Method: "item/completed", Params: collabStartedParams(`"agent-a"`, `"agent-a":{"status":"running"}`)})
	second := codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"collabAgentToolCall","id":"spawn-2","tool":"spawn_agent","status":"completed","receiverThreadIds":["agent-b"],"prompt":"写乔丹故事","agentsStates":{"agent-b":{"status":"pendingInit"}}}}`)})
	staleClose := codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"collabAgentToolCall","id":"close-old","tool":"close_agent","status":"completed","receiverThreadIds":["old-agent"],"agentsStates":{"old-agent":{"status":"completed"}}}}`)})
	wait := codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"collabAgentToolCall","id":"wait-1","tool":"wait","status":"completed","receiverThreadIds":["agent-a"],"agentsStates":{"agent-a":{"status":"completed"}}}}`)})
	for name, events := range map[string][]core.Event{"first": first, "second": second, "wait": wait} {
		if len(events) != 1 || events[0].WorkflowRun == nil || events[0].WorkflowRun.RunID != "codex-collab:turn" {
			t.Fatalf("%s events = %+v", name, events)
		}
	}
	if len(staleClose) != 0 {
		t.Fatalf("stale close must not refresh the workflow: %+v", staleClose)
	}
	members := wait[0].WorkflowRun.Phases[0].Members
	if len(members) != 2 || members[0].ChildSessionID != "agent-a" || members[0].Status != "completed" ||
		members[1].ChildSessionID != "agent-b" || members[1].Status != "running" {
		t.Fatalf("folded members = %+v", members)
	}
	if members[0].Label != "写贝索斯约300字故事" || members[1].Label != "写乔丹故事" {
		t.Fatalf("member labels = %+v", members)
	}
}

func TestCollabCoreAgentStatusTaggedUnionSettlesMembers(t *testing.T) {
	codec := NewLiveCodec()
	spawn := json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"collabAgentToolCall","id":"spawn-1","tool":"spawn_agent","status":"completed","sender_thread_id":"th","receiver_thread_ids":["01a08f7b-cd6a-7562-b8a1-a84b1382674f"],"prompt":"请创作一篇关于哈兰德的中文原创文学故事","agents_states":{"01a08f7b-cd6a-7562-b8a1-a84b1382674f":"pending_init"}}}`)
	if events := codec.Decode(Notification{Method: "item/completed", Params: spawn}); len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("spawn = %+v", events)
	}
	wait := json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"collabAgentToolCall","id":"wait-1","tool":"wait","status":"completed","sender_thread_id":"th","receiver_thread_ids":["01a08f7b-cd6a-7562-b8a1-a84b1382674f"],"agents_states":{"01a08f7b-cd6a-7562-b8a1-a84b1382674f":{"completed":"【文学虚构】哈兰德进球了。"}}}}`)
	events := codec.Decode(Notification{Method: "item/completed", Params: wait})
	if len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("wait = %+v", events)
	}
	members := events[0].WorkflowRun.Phases[0].Members
	if len(members) != 1 || members[0].Status != "completed" || events[0].WorkflowRun.Status != "completed" {
		t.Fatalf("settled members = %+v card=%s", members, events[0].WorkflowRun.Status)
	}
	closeAgent := json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"collabAgentToolCall","id":"close-1","tool":"close_agent","status":"completed","receiver_thread_ids":["01a08f7b-cd6a-7562-b8a1-a84b1382674f"],"agents_states":{"01a08f7b-cd6a-7562-b8a1-a84b1382674f":"shutdown"}}}`)
	if events := codec.Decode(Notification{Method: "item/completed", Params: closeAgent}); len(events) != 0 {
		t.Fatalf("close after completed must not reopen the card: %+v", events)
	}
	codec = NewLiveCodec()
	if events := codec.Decode(Notification{Method: "item/completed", Params: spawn}); len(events) != 1 {
		t.Fatalf("respawn = %+v", events)
	}
	events = codec.Decode(Notification{Method: "item/completed", Params: closeAgent})
	if len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("shutdown close = %+v", events)
	}
	if got := events[0].WorkflowRun.Phases[0].Members[0].Status; got != "completed" {
		t.Fatalf("shutdown after success = %q, want completed not cancelled", got)
	}
}

func TestCollabSubAgentActivityRefreshesExistingMember(t *testing.T) {
	codec := NewLiveCodec()
	if events := codec.Decode(Notification{Method: "item/started", Params: collabStartedParams(`"agent-a"`, `"agent-a":{"status":"running"}`)}); len(events) != 1 {
		t.Fatalf("started = %+v", events)
	}
	activity := json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"subAgentActivity","id":"act-1","kind":"completed","agentThreadId":"agent-a","agentPath":"/tmp"}}`)
	events := codec.Decode(Notification{Method: "item/completed", Params: activity})
	if len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("activity = %+v", events)
	}
	if members := events[0].WorkflowRun.Phases[0].Members; len(members) != 1 || members[0].Status != "completed" {
		t.Fatalf("activity members = %+v", events[0].WorkflowRun.Phases[0].Members)
	}
	orphan := json.RawMessage(`{"threadId":"th","turnId":"other","item":{"type":"subAgentActivity","id":"act-2","kind":"completed","agentThreadId":"missing","agentPath":"/tmp"}}`)
	if events := codec.Decode(Notification{Method: "item/completed", Params: orphan}); len(events) != 0 {
		t.Fatalf("orphan activity must not create a card: %+v", events)
	}
}

func TestCollabPromptTruncationAndEmptyPromptFallback(t *testing.T) {
	codec := NewLiveCodec()
	long := make([]rune, 120)
	for i := range long {
		long[i] = '写'
	}
	params := json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"collabAgentToolCall","id":"call-4","tool":"spawnAgent","status":"inProgress","senderThreadId":"th","receiverThreadIds":["agent-a"],"prompt":"` + string(long) + `"}}`)
	events := codec.Decode(Notification{Method: "item/started", Params: params})
	if len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("events = %+v", events)
	}
	if got := events[0].WorkflowRun.Phases[0].Members[0].Label; len([]rune(got)) != 80 {
		t.Fatalf("member label rune length = %d", len([]rune(got)))
	}
	emptyCodec := NewLiveCodec()
	empty := emptyCodec.Decode(Notification{Method: "item/started", Params: json.RawMessage(`{"threadId":"th","turnId":"empty-turn","item":{"type":"collabAgentToolCall","id":"call-5","tool":"spawnAgent","status":"inProgress","senderThreadId":"th","receiverThreadIds":["agent-a"],"prompt":"  "}}`)})
	if len(empty) != 1 || empty[0].WorkflowRun == nil || empty[0].WorkflowRun.Phases[0].Members[0].Label != "Agent-1" {
		t.Fatalf("empty-prompt events = %+v", empty)
	}
}

func TestCollabColdHistoryProducesWorkflowPart(t *testing.T) {
	turn := core.TurnScopedHistoryTurn{TurnID: "turn", Status: "completed"}
	item := decodeRemoteThreadItem(json.RawMessage(`{"type":"collabAgentToolCall","id":"call-1","tool":"spawnAgent","status":"completed","senderThreadId":"th","receiverThreadIds":["agent-a"],"prompt":"写故事","agentsStates":{"agent-a":{"status":"completed","message":"done"}}}`))
	folds := newRemoteCollabHistoryFolds("th")
	holders := map[string]*core.TurnScopedHistoryTurn{"turn": &turn}
	folds.mapItem(&turn, item, holders)
	if len(turn.Parts) != 1 {
		t.Fatalf("parts = %+v", turn.Parts)
	}
	part := turn.Parts[0]
	if part["type"] != "workflow" || part["workflowId"] != "codex-collab:turn" || part["workflowName"] != "Subagents" || part["workflowStatus"] != "completed" {
		t.Fatalf("part = %+v", part)
	}
	phases, ok := part["workflowPhases"].([]map[string]any)
	if !ok || len(phases) != 1 {
		t.Fatalf("phases = %+v", part["workflowPhases"])
	}
	members, ok := phases[0]["members"].([]map[string]any)
	if !ok || len(members) != 1 {
		t.Fatalf("members = %+v", phases[0]["members"])
	}
	if members[0]["label"] != "写故事" || members[0]["childSessionId"] != "agent-a" || members[0]["status"] != "completed" {
		t.Fatalf("member = %+v", members[0])
	}
	// The go-bridge cold conversion (hydrateWorkflowEventsFromPart + reducer
	// workflow_run case) is covered by TestCollabColdPartHydratesInBridge in
	// the go-bridge package — same part map, asserted end to end there.
}

func TestCollabColdHistoryFoldsSpawnWaitCloseIntoOneCard(t *testing.T) {
	turn := core.TurnScopedHistoryTurn{TurnID: "turn", Status: "completed"}
	items := []string{
		`{"type":"collabAgentToolCall","id":"spawn-a","tool":"spawnAgent","status":"completed","receiverThreadIds":["agent-a"],"prompt":"写科比故事","agentsStates":{"agent-a":{"status":"running"}}}`,
		`{"type":"collabAgentToolCall","id":"spawn-b","tool":"spawnAgent","status":"completed","receiverThreadIds":["agent-b"],"prompt":"写乔丹故事","agentsStates":{"agent-b":{"status":"pendingInit"}}}`,
		`{"type":"collabAgentToolCall","id":"close-old","tool":"closeAgent","status":"completed","receiverThreadIds":["old-agent"],"agentsStates":{"old-agent":{"status":"completed"}}}`,
		`{"type":"collabAgentToolCall","id":"wait-a","tool":"wait","status":"completed","receiverThreadIds":["agent-a"],"agentsStates":{"agent-a":{"status":"completed"}}}`,
	}
	folds := newRemoteCollabHistoryFolds("th")
	holders := map[string]*core.TurnScopedHistoryTurn{"turn": &turn}
	for _, raw := range items {
		folds.mapItem(&turn, decodeRemoteThreadItem(json.RawMessage(raw)), holders)
	}
	if len(turn.Parts) != 1 {
		t.Fatalf("cold collab calls produced %d parts: %+v", len(turn.Parts), turn.Parts)
	}
	phases := workflowPhaseMaps(turn.Parts[0]["workflowPhases"])
	members := workflowMemberMaps(phases[0]["members"])
	if len(members) != 2 || members[0]["childSessionId"] != "agent-a" || members[0]["status"] != "completed" ||
		members[1]["childSessionId"] != "agent-b" || members[1]["status"] != "running" {
		t.Fatalf("cold folded members = %+v", members)
	}
}

func TestSubAgentActivityRemainsDeliberateNoCard(t *testing.T) {
	codec := NewLiveCodec()
	for _, method := range []string{"item/started", "item/completed"} {
		if events := codec.Decode(Notification{Method: method, Params: json.RawMessage(`{"threadId":"th","turnId":"turn","item":{"type":"subAgentActivity","id":"act-1","kind":"started","agentThreadId":"agent-a","agentPath":"/tmp"}}`)}); len(events) != 0 {
			t.Fatalf("%s subAgentActivity events = %+v", method, events)
		}
	}
	turn := core.TurnScopedHistoryTurn{TurnID: "turn", Status: "completed"}
	coldFolds := newRemoteCollabHistoryFolds("th")
	coldHolders := map[string]*core.TurnScopedHistoryTurn{"turn": &turn}
	coldFolds.mapItem(&turn, decodeRemoteThreadItem(json.RawMessage(`{"type":"subAgentActivity","id":"act-1","kind":"completed","agentThreadId":"agent-a","agentPath":"/tmp"}`)), coldHolders)
	if len(turn.Parts) != 0 {
		t.Fatalf("cold parts = %+v", turn.Parts)
	}
}

func TestCodexAppCreateThreadAndWaitThreadsProduceWorkflowCards(t *testing.T) {
	codec := NewLiveCodec()
	create := json.RawMessage(`{"threadId":"root","turnId":"turn","item":{"type":"mcpToolCall","id":"create-1","server":"codex_app","tool":"create_thread","arguments":{"title":"创作王母娘娘故事","prompt":"写故事"},"status":"completed","result":{"content":[{"type":"text","text":"{\"threadId\":\"child-1\",\"hostId\":\"local\"}"}],"isError":false}}}`)
	events := codec.Decode(Notification{Method: "item/completed", Params: create})
	if len(events) != 2 || events[1].Type != core.EventWorkflowRun || events[1].WorkflowRun == nil {
		t.Fatalf("create events = %+v", events)
	}
	createRun := events[1].WorkflowRun
	if createRun.RunID != "codex-collab:turn" || createRun.Name != "创作王母娘娘故事" || createRun.Status != "running" || len(createRun.Phases[0].Members) != 1 {
		t.Fatalf("create workflow = %+v", createRun)
	}
	if member := createRun.Phases[0].Members[0]; member.ChildSessionID != "child-1" || member.Status != "running" || member.Label != "创作王母娘娘故事" {
		t.Fatalf("create member = %+v", member)
	}

	wait := json.RawMessage(`{"threadId":"root","turnId":"turn","item":{"type":"mcpToolCall","id":"wait-1","server":"codex_app","tool":"wait_threads","arguments":{"targets":[{"threadId":"child-1"},{"threadId":"child-2"}],"timeoutMs":120000},"status":"completed","result":{"content":[{"type":"text","text":"{\"timedOut\":false,\"polls\":[{\"thread\":{\"id\":\"child-1\",\"status\":{\"type\":\"idle\"}},\"latestTurn\":{\"status\":\"completed\"}}]}"}],"isError":false}}}`)
	events = codec.Decode(Notification{Method: "item/completed", Params: wait})
	if len(events) != 2 || events[1].WorkflowRun == nil {
		t.Fatalf("wait events = %+v", events)
	}
	if events[1].WorkflowRun.RunID != "codex-collab:turn" || events[1].ItemID != "codex-collab:turn" {
		t.Fatalf("wait must reuse the turn-keyed card: %+v", events[1].WorkflowRun)
	}
	members := events[1].WorkflowRun.Phases[0].Members
	if len(members) != 2 || members[0].Status != "completed" || members[1].Status != "running" {
		t.Fatalf("wait members = %+v", members)
	}
}

func TestWaitThreadsStatusUpdatesReuseOneCard(t *testing.T) {
	codec := NewLiveCodec()
	first := json.RawMessage(`{"threadId":"root","turnId":"turn","item":{"type":"mcpToolCall","id":"wait-1","server":"codex_app","tool":"wait_threads","arguments":{"targets":[{"threadId":"a"},{"threadId":"b"}]},"status":"completed","result":{"content":[{"type":"text","text":"{\"polls\":[{\"thread\":{\"id\":\"a\",\"status\":{\"type\":\"idle\"}},\"latestTurn\":{\"status\":\"completed\"}},{\"thread\":{\"id\":\"b\",\"status\":{\"type\":\"active\"}},\"latestTurn\":{\"status\":\"inProgress\"}}]}"}]}}}`)
	second := json.RawMessage(`{"threadId":"root","turnId":"turn","item":{"type":"mcpToolCall","id":"wait-2","server":"codex_app","tool":"wait_threads","arguments":{"targets":[{"threadId":"b"}]},"status":"completed","result":{"content":[{"type":"text","text":"{\"polls\":[{\"thread\":{\"id\":\"b\",\"status\":{\"type\":\"idle\"}},\"latestTurn\":{\"status\":\"completed\"}}]}"}]}}}`)
	third := json.RawMessage(`{"threadId":"root","turnId":"turn","item":{"type":"mcpToolCall","id":"wait-3","server":"codex_app","tool":"wait_threads","arguments":{"targets":[{"threadId":"a"},{"threadId":"b"}]},"status":"completed","result":{"content":[{"type":"text","text":"{\"polls\":[{\"thread\":{\"id\":\"a\",\"status\":{\"type\":\"idle\"}},\"latestTurn\":{\"status\":\"completed\"}},{\"thread\":{\"id\":\"b\",\"status\":{\"type\":\"idle\"}},\"latestTurn\":{\"status\":\"completed\"}}]}"}]}}}`)
	var last *core.WorkflowRunEvent
	for i, raw := range []json.RawMessage{first, second} {
		events := codec.Decode(Notification{Method: "item/completed", Params: raw})
		if len(events) != 2 || events[1].WorkflowRun == nil || events[1].WorkflowRun.RunID != "codex-collab:turn" {
			t.Fatalf("wait %d events = %+v", i, events)
		}
		last = events[1].WorkflowRun
	}
	if last == nil || last.Status != "completed" || len(last.Phases[0].Members) != 2 {
		t.Fatalf("final card = %+v", last)
	}
	if events := codec.Decode(Notification{Method: "item/completed", Params: third}); len(events) != 1 {
		t.Fatalf("unchanged wait must not open another card: %+v", events)
	}
}

func TestCodexAppWorkflowColdPartsDriveBackgroundTasks(t *testing.T) {	turn := core.TurnScopedHistoryTurn{
		TurnID: "turn", Status: "completed",
		StartedAt: time.Unix(100, 0), CompletedAt: time.Unix(140, 0),
	}
	create := decodeRemoteThreadItem(json.RawMessage(`{"type":"mcpToolCall","id":"create-1","server":"codex_app","tool":"create_thread","arguments":{"title":"故事任务"},"status":"completed","result":{"content":[{"type":"text","text":"{\"threadId\":\"child-1\"}"}],"isError":false}}`))
	wait := decodeRemoteThreadItem(json.RawMessage(`{"type":"mcpToolCall","id":"wait-1","server":"codex_app","tool":"wait_threads","arguments":{"targets":[{"threadId":"child-1"}]},"status":"completed","result":{"content":[{"type":"text","text":"{\"polls\":[{\"thread\":{\"id\":\"child-1\",\"status\":{\"type\":\"idle\"}},\"latestTurn\":{\"status\":\"completed\"}}]}"}],"isError":false}}`))
	tasksFolds := newRemoteCollabHistoryFolds("root")
	tasksHolders := map[string]*core.TurnScopedHistoryTurn{"turn": &turn}
	tasksFolds.mapItem(&turn, create, tasksHolders)
	tasksFolds.mapItem(&turn, wait, tasksHolders)
	tasks := backgroundTasksFromTurns("root", []core.TurnScopedHistoryTurn{turn})
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v parts=%+v", tasks, turn.Parts)
	}
	task := tasks[0]
	if task.TaskID != "child-1" || task.RootSessionID != "root" || task.Title != "故事任务" || task.Status != "completed" || !task.TranscriptAvailable {
		t.Fatalf("task = %+v", task)
	}
}

// 2026-09-12 goal-run fix: one logical subagent run officially spans turns —
// the spawn turn creates the children and later turns keep polling. The run
// must stay anchored at the spawn turn instead of becoming one card per turn
// (docs/2026-09-12-codex-goal-duplicate-thinking-and-dual-workflow-card-analysis.md).

func collabCreateRaw(turnID, childID, prompt string) json.RawMessage {
	return json.RawMessage(`{"threadId":"root","turnId":"` + turnID + `","item":{"type":"mcpToolCall","id":"create-` + childID + `","server":"codex_app","tool":"create_thread","arguments":{"prompt":"` + prompt + `"},"status":"completed","result":{"content":[{"type":"text","text":"{\"threadId\":\"` + childID + `\"}"}],"isError":false}}}`)
}

func collabWaitRaw(turnID, pollJSON string, targets ...string) json.RawMessage {
	targetObjs := make([]map[string]string, 0, len(targets))
	for _, target := range targets {
		targetObjs = append(targetObjs, map[string]string{"threadId": target})
	}
	// The {"polls":[…]} payload rides as a string inside the MCP result's
	// text field; marshal the whole notification so escaping is Go's job
	// (byte shape matches the official wire samples in the fixtures above).
	notification := map[string]any{
		"threadId": "root",
		"turnId":   turnID,
		"item": map[string]any{
			"type": "mcpToolCall", "id": "wait-" + turnID, "server": "codex_app",
			"tool":       "wait_threads",
			"arguments":  map[string]any{"targets": targetObjs},
			"status":     "completed",
			"result": map[string]any{
				"content": []map[string]string{{"type": "text", "text": `{"polls":[` + pollJSON + `]}`}},
				"isError": false,
			},
		},
	}
	raw, err := json.Marshal(notification)
	if err != nil {
		panic(err)
	}
	return raw
}

const collabPollCompleted = `{"thread":{"id":"%s","status":{"type":"idle"}},"latestTurn":{"status":"completed"}}`

func TestCollabRunSurvivesTurnBoundaryIntoWaitTurn(t *testing.T) {
	codec := NewLiveCodec()
	events := codec.Decode(Notification{Method: "item/completed", Params: collabCreateRaw("spawn-turn", "child-lin", "请为《红楼梦》中的林黛玉创作一段小故事")})
	if len(events) != 2 || events[1].Type != core.EventWorkflowRun || events[1].WorkflowRun == nil {
		t.Fatalf("create events = %+v", events)
	}
	if got := events[1].WorkflowRun.RunID; got != "codex-collab:spawn-turn" {
		t.Fatalf("create runID = %q", got)
	}
	// The wait lives in a LATER turn: it must reuse the spawn-anchored run and
	// report under the anchor turn, never anchor a second card of its own.
	poll := `{"thread":{"id":"child-lin","status":{"type":"idle"}},"latestTurn":{"status":"completed"}}`
	events = codec.Decode(Notification{Method: "item/completed", Params: collabWaitRaw("wait-turn", poll, "child-lin")})
	if len(events) != 2 || events[1].WorkflowRun == nil {
		t.Fatalf("wait events = %+v", events)
	}
	if events[1].WorkflowRun.RunID != "codex-collab:spawn-turn" || events[1].TurnID != "spawn-turn" {
		t.Fatalf("cross-turn wait must reuse the spawn-anchored run: run=%q turn=%q",
			events[1].WorkflowRun.RunID, events[1].TurnID)
	}
	wr := events[1].WorkflowRun
	if wr.Status != core.WorkflowStatusCompleted || len(wr.Phases[0].Members) != 1 {
		t.Fatalf("final card = %+v", wr)
	}
	if member := wr.Phases[0].Members[0]; member.ChildSessionID != "child-lin" ||
		member.Status != core.WorkflowStatusCompleted ||
		member.Label != "请为《红楼梦》中的林黛玉创作一段小故事" {
		t.Fatalf("member = %+v", member)
	}
}

func TestCollabPollNamingChildOverwritesActivityInterrupted(t *testing.T) {
	codec := NewLiveCodec()
	codec.Decode(Notification{Method: "item/completed", Params: collabCreateRaw("spawn-turn", "child-bao", "贾宝玉故事")})
	flip := codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"root","turnId":"spawn-turn","item":{"type":"subAgentActivity","id":"act-1","kind":"interrupted","agentThreadId":"child-bao","agentPath":"/tmp"}}`)})
	if len(flip) != 1 || flip[0].WorkflowRun.Phases[0].Members[0].Status != core.WorkflowStatusInterrupted {
		t.Fatalf("activity flip = %+v", flip)
	}
	// A later poll that does NOT name the child must keep the member's current
	// status (official polls are cumulative; absence is not a reset) while an
	// unknown child still joins the card running.
	partial := codec.Decode(Notification{Method: "item/completed", Params: collabWaitRaw("wait-turn", "", "child-bao", "child-feng")})
	if len(partial) != 2 || partial[1].WorkflowRun == nil {
		t.Fatalf("partial poll events = %+v", partial)
	}
	members := partial[1].WorkflowRun.Phases[0].Members
	if len(members) != 2 || members[0].Status != core.WorkflowStatusInterrupted || members[1].Status != core.WorkflowStatusRunning {
		t.Fatalf("after unnamed poll = %+v", members)
	}
	// Only a poll that NAMES the child settles it back to completed.
	polls := `{"thread":{"id":"child-bao","status":{"type":"idle"}},"latestTurn":{"status":"completed"}},{"thread":{"id":"child-feng","status":{"type":"idle"}},"latestTurn":{"status":"completed"}}`
	final := codec.Decode(Notification{Method: "item/completed", Params: collabWaitRaw("wait-turn-2", polls, "child-bao", "child-feng")})
	wr := final[len(final)-1].WorkflowRun
	if wr.Status != core.WorkflowStatusCompleted {
		t.Fatalf("final card status = %q", wr.Status)
	}
	for _, member := range wr.Phases[0].Members {
		if member.Status != core.WorkflowStatusCompleted {
			t.Fatalf("final members = %+v", wr.Phases[0].Members)
		}
	}
}

func TestCollabNewSpawnBatchAfterSettledRunOpensNextRun(t *testing.T) {
	codec := NewLiveCodec()
	codec.Decode(Notification{Method: "item/completed", Params: collabCreateRaw("batch1-turn", "child-one", "第一批故事")})
	codec.Decode(Notification{Method: "item/completed", Params: collabWaitRaw("batch1-turn", `{"thread":{"id":"child-one","status":{"type":"idle"}},"latestTurn":{"status":"completed"}}`, "child-one")})
	// Batch 1 settled; a new spawn in a later turn opens the NEXT run anchored
	// at its own spawn turn (one card per logical run, not per thread).
	events := codec.Decode(Notification{Method: "item/completed", Params: collabCreateRaw("batch2-turn", "child-two", "第二批故事")})
	if len(events) != 2 || events[1].WorkflowRun == nil {
		t.Fatalf("batch2 events = %+v", events)
	}
	wr := events[1].WorkflowRun
	if wr.RunID != "codex-collab:batch2-turn" || len(wr.Phases[0].Members) != 1 || wr.Phases[0].Members[0].ChildSessionID != "child-two" {
		t.Fatalf("batch2 card = %+v", wr)
	}
}

func workflowParts(parts []map[string]any) []map[string]any {
	var out []map[string]any
	for _, part := range parts {
		if stringValue(part["type"]) == "workflow" {
			out = append(out, part)
		}
	}
	return out
}

func TestColdHistoryCrossTurnRunFoldsIntoSpawnTurn(t *testing.T) {
	thread := &remoteThread{ID: "root", Turns: []remoteTurn{
		{ID: "spawn-turn", Status: remoteTurnStatusCompleted, Items: []json.RawMessage{
			json.RawMessage(`{"type":"mcpToolCall","id":"create-1","server":"codex_app","tool":"create_thread","arguments":{"prompt":"薛宝钗故事"},"status":"completed","result":{"content":[{"type":"text","text":"{\"threadId\":\"child-xue\"}"}],"isError":false}}`),
		}},
		{ID: "wait-turn", Status: remoteTurnStatusCompleted, Items: []json.RawMessage{
			json.RawMessage(`{"type":"mcpToolCall","id":"wait-1","server":"codex_app","tool":"wait_threads","arguments":{"targets":[{"threadId":"child-xue"}]},"status":"completed","result":{"content":[{"type":"text","text":"{\"polls\":[{\"thread\":{\"id\":\"child-xue\",\"status\":{\"type\":\"idle\"}},\"latestTurn\":{\"status\":\"completed\"}}]}"}],"isError":false}}`),
		}},
	}}
	turns := mapRemoteHistoryTurns(thread, 0, newRemoteCollabHistoryFolds("root"))
	if len(turns) != 2 {
		t.Fatalf("turns = %+v", turns)
	}
	if parts := workflowParts(turns[0].Parts); len(parts) != 1 ||
		parts[0]["workflowId"] != "codex-collab:spawn-turn" || parts[0]["workflowStatus"] != "completed" {
		t.Fatalf("spawn-turn workflow parts = %+v", turns[0].Parts)
	}
	if parts := workflowParts(turns[1].Parts); len(parts) != 0 {
		t.Fatalf("wait turn must not carry its own card: %+v", parts)
	}
	members := workflowMemberMaps(workflowPhaseMaps(workflowParts(turns[0].Parts)[0]["workflowPhases"])[0]["members"])
	if len(members) != 1 || members[0]["childSessionId"] != "child-xue" ||
		members[0]["status"] != "completed" || members[0]["label"] != "薛宝钗故事" {
		t.Fatalf("cold folded members = %+v", members)
	}
}

func TestMapTurnItemsPageForSessionFoldsWaitTurnIntoSpawnRun(t *testing.T) {
	agent := New(nil)
	spawnTurn := core.TurnScopedHistoryTurn{TurnID: "spawn-turn", Status: "completed"}
	if err := agent.MapTurnItemsPageForSession("root", &spawnTurn, collabPageOfItems("spawn-turn",
		`{"type":"mcpToolCall","id":"create-1","server":"codex_app","tool":"create_thread","arguments":{"prompt":"王熙凤故事"},"status":"completed","result":{"content":[{"type":"text","text":"{\"threadId\":\"child-feng\"}"}],"isError":false}}`)); err != nil {
		t.Fatal(err)
	}
	waitTurn := core.TurnScopedHistoryTurn{TurnID: "wait-turn", Status: "completed"}
	if err := agent.MapTurnItemsPageForSession("root", &waitTurn, collabPageOfItems("wait-turn",
		`{"type":"mcpToolCall","id":"wait-1","server":"codex_app","tool":"wait_threads","arguments":{"targets":[{"threadId":"child-feng"}]},"status":"completed","result":{"content":[{"type":"text","text":"{\"polls\":[{\"thread\":{\"id\":\"child-feng\",\"status\":{\"type\":\"idle\"}},\"latestTurn\":{\"status\":\"completed\"}}]}"}],"isError":false}}`)); err != nil {
		t.Fatal(err)
	}
	// Lazy per-turn detail fetched the wait turn AFTER the spawn turn: the
	// session registry still resolves the spawn-anchored run, so the part keeps
	// the anchored runId (the reducer coalesces it onto the spawn turn) and
	// carries the real member label instead of "Subagents"/"Agent-N" defaults.
	parts := workflowParts(waitTurn.Parts)
	if len(parts) != 1 || parts[0]["workflowId"] != "codex-collab:spawn-turn" {
		t.Fatalf("wait-turn parts = %+v", waitTurn.Parts)
	}
	members := workflowMemberMaps(workflowPhaseMaps(parts[0]["workflowPhases"])[0]["members"])
	if len(members) != 1 || members[0]["label"] != "王熙凤故事" || members[0]["status"] != "completed" {
		t.Fatalf("lazy members = %+v", members)
	}
}

func collabPageOfItems(turnID string, rawItems ...string) *core.TurnItemsPage {
	page := &core.TurnItemsPage{EOF: true}
	for _, raw := range rawItems {
		var item map[string]any
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			panic(err)
		}
		page.Entries = append(page.Entries, core.TurnItemsEntry{TurnID: turnID, Item: item})
	}
	return page
}

// 2026-09-12 second-test fixes: connection rebinds (BindClient →
// ResetNativeSessionState) must not orphan an in-flight collab run, and a wait
// whose spawns were never seen (runtime started mid-run) rebuilds members from
// its complete agentsStates (docs/2026-09-12-codex-goal-second-test-*-analysis.md).

func TestCollabFoldSurvivesRebind(t *testing.T) {
	codec := NewLiveCodec()
	codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"root","turnId":"spawn-turn","item":{"type":"collabAgentToolCall","id":"spawn-1","tool":"spawn_agent","status":"completed","receiverThreadIds":["child-bao"],"prompt":"贾宝玉故事","agentsStates":{"child-bao":"pending_init"}}}`)})
	// The pairing stream drops and reconnects mid-run: the rebind resets native
	// state, but the run's fold must keep its members so the post-reconnect
	// wait with authoritative states settles the SAME card.
	codec.ResetNativeSessionState()
	events := codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"root","turnId":"spawn-turn","item":{"type":"collabAgentToolCall","id":"wait-1","tool":"wait","status":"completed","receiverThreadIds":[],"agentsStates":{"child-bao":{"completed":"故事全文"}}}}`)})
	if len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("post-rebind wait events = %+v", events)
	}
	wr := events[0].WorkflowRun
	if wr.RunID != "codex-collab:spawn-turn" || wr.Status != core.WorkflowStatusCompleted {
		t.Fatalf("card = %+v", wr)
	}
	if member := wr.Phases[0].Members[0]; member.ChildSessionID != "child-bao" ||
		member.Status != core.WorkflowStatusCompleted || member.Label != "贾宝玉故事" {
		t.Fatalf("member = %+v", member)
	}
}

func TestCollabWaitRebuildsMembersWhenSpawnUnseen(t *testing.T) {
	codec := NewLiveCodec()
	// Runtime restarted mid-run (or the stream dropped the spawns): the first
	// collab item seen is a wait carrying the full per-agent state map — the
	// card rebuilds from it instead of never forming.
	events := codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"root","turnId":"wait-turn","item":{"type":"collabAgentToolCall","id":"wait-1","tool":"wait","status":"completed","receiverThreadIds":[],"agentsStates":{"child-a":{"completed":"故事A"},"child-b":{"completed":"故事B"}}}}`)})
	if len(events) != 1 || events[0].WorkflowRun == nil {
		t.Fatalf("rebuild events = %+v", events)
	}
	wr := events[0].WorkflowRun
	if wr.RunID != "codex-collab:wait-turn" || wr.Status != core.WorkflowStatusCompleted || len(wr.Phases[0].Members) != 2 {
		t.Fatalf("card = %+v", wr)
	}
	for _, member := range wr.Phases[0].Members {
		if member.Status != core.WorkflowStatusCompleted {
			t.Fatalf("rebuilt members = %+v", wr.Phases[0].Members)
		}
	}
	// Close of stale agents still never creates a card (spawn-only adoption
	// rule preserved for close).
	codec = NewLiveCodec()
	if events := codec.Decode(Notification{Method: "item/completed", Params: json.RawMessage(`{"threadId":"root","turnId":"turn","item":{"type":"collabAgentToolCall","id":"close-old","tool":"close_agent","status":"completed","receiverThreadIds":["old-agent"],"agentsStates":{"old-agent":{"completed":"x"}}}}`)}); len(events) != 0 {
		t.Fatalf("stale close must not adopt members: %+v", events)
	}
}
