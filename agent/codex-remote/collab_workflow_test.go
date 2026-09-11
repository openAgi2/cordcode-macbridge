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
	mapRemoteHistoryItem(&turn, item)
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
	for _, raw := range items {
		mapRemoteHistoryItem(&turn, decodeRemoteThreadItem(json.RawMessage(raw)))
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
	mapRemoteHistoryItem(&turn, decodeRemoteThreadItem(json.RawMessage(`{"type":"subAgentActivity","id":"act-1","kind":"completed","agentThreadId":"agent-a","agentPath":"/tmp"}`)))
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

func TestCodexAppWorkflowColdPartsDriveBackgroundTasks(t *testing.T) {
	turn := core.TurnScopedHistoryTurn{
		TurnID: "turn", Status: "completed",
		StartedAt: time.Unix(100, 0), CompletedAt: time.Unix(140, 0),
	}
	create := decodeRemoteThreadItem(json.RawMessage(`{"type":"mcpToolCall","id":"create-1","server":"codex_app","tool":"create_thread","arguments":{"title":"故事任务"},"status":"completed","result":{"content":[{"type":"text","text":"{\"threadId\":\"child-1\"}"}],"isError":false}}`))
	wait := decodeRemoteThreadItem(json.RawMessage(`{"type":"mcpToolCall","id":"wait-1","server":"codex_app","tool":"wait_threads","arguments":{"targets":[{"threadId":"child-1"}]},"status":"completed","result":{"content":[{"type":"text","text":"{\"polls\":[{\"thread\":{\"id\":\"child-1\",\"status\":{\"type\":\"idle\"}},\"latestTurn\":{\"status\":\"completed\"}}]}"}],"isError":false}}`))
	mapRemoteHistoryItem(&turn, create)
	mapRemoteHistoryItem(&turn, wait)
	tasks := backgroundTasksFromTurns("root", []core.TurnScopedHistoryTurn{turn})
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v parts=%+v", tasks, turn.Parts)
	}
	task := tasks[0]
	if task.TaskID != "child-1" || task.RootSessionID != "root" || task.Title != "故事任务" || task.Status != "completed" || !task.TranscriptAvailable {
		t.Fatalf("task = %+v", task)
	}
}
