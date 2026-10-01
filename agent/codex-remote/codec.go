package codexremote

// codec.go decodes the ordinary app-server notifications that arrive inside a
// Remote Control server_message envelope. The protocol identity remains the
// official (threadId, turnId, itemId) tuple; Remote envelope ids are transport
// metadata and never become projection ids. Unknown methods are counted and
// dropped, while known notifications with no bridge event are consumed
// silently so an event backlog cannot grow.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type LiveCodec struct {
	mu            sync.Mutex
	turnByThread  map[string]string
	retryByThread map[string]int
	unknown       map[string]int
	// inFlightPlan is the proposed-plan item for the current turn (official
	// ThreadItem::Plan). The completed item is authoritative; item/plan/delta
	// must not be concatenated (app-server-protocol v2 item.rs).
	inFlightPlan map[string]codexProposedPlan
	// awaitingPlanReview is a plan item waiting for the client-orchestrated
	// "Implement this plan?" action (TUI plan_implementation.rs — not a wire
	// approval request). Keyed by the official plan item id.
	awaitingPlanReview map[string]codexProposedPlan
	// Only official full settings notifications or thread/settings/get responses
	// populate this current-epoch map. Versions keep an older read response from
	// overwriting a newer Desktop notification.
	collaborationByThread map[string]versionedCollaborationSnapshot
	goalByThread          map[string]versionedGoalSnapshot
	// lastErrorParams suppresses byte-identical terminal error notifications.
	// Upstream has emitted the same transport error hundreds of thousands of times
	// per minute; the first notification is truth, exact repeats carry no state.
	lastErrorParams              []byte
	suppressedErrorNotifications uint64
	// collabFolds routes collabAgentToolCall / codex_app create_thread +
	// wait_threads operations onto cross-turn workflow cards keyed by the
	// spawn-anchored run (shared model with DeepSeek Harness' tool-workflow
	// folds; see collab_workflow.go).
	collabFolds *collabFoldRegistry
}

type versionedCollaborationSnapshot struct {
	version uint64
	state   core.SessionCollaborationMode
}

type versionedGoalSnapshot struct {
	version  uint64
	snapshot core.SessionGoalSnapshot
}

type codexProposedPlan struct {
	threadID string
	turnID   string
	itemID   string
	text     string
}

func NewLiveCodec() *LiveCodec {
	return &LiveCodec{
		turnByThread:          map[string]string{},
		retryByThread:         map[string]int{},
		unknown:               map[string]int{},
		inFlightPlan:          map[string]codexProposedPlan{},
		awaitingPlanReview:    map[string]codexProposedPlan{},
		collaborationByThread: map[string]versionedCollaborationSnapshot{},
		goalByThread:          map[string]versionedGoalSnapshot{},
		collabFolds:           newCollabFoldRegistry(),
	}
}

func (c *LiveCodec) CurrentCollaborationMode(threadID string) (core.SessionCollaborationMode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot, ok := c.collaborationByThread[threadID]
	state := snapshot.state
	state.ReasoningEffort = cloneStringPointer(state.ReasoningEffort)
	return state, ok
}

func (c *LiveCodec) CollaborationVersion(threadID string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.collaborationByThread[threadID].version
}

func (c *LiveCodec) applyCollaborationMode(threadID string, state core.SessionCollaborationMode, expectedVersion *uint64) (core.Event, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current, exists := c.collaborationByThread[threadID]
	if expectedVersion != nil && current.version != *expectedVersion {
		return core.Event{}, false
	}
	if exists && collaborationModesEqual(current.state, state) {
		return core.Event{}, false
	}
	current.version++
	current.state = cloneCollaborationMode(state)
	c.collaborationByThread[threadID] = current
	eventState := cloneCollaborationMode(state)
	return core.Event{Type: core.EventSessionCollaborationMode, SessionID: threadID, ThreadID: threadID, CollaborationMode: &eventState}, true
}

func (c *LiveCodec) ResetNativeSessionState() {
	c.mu.Lock()
	c.lastErrorParams = nil
	c.suppressedErrorNotifications = 0
	c.collaborationByThread = map[string]versionedCollaborationSnapshot{}
	c.goalByThread = map[string]versionedGoalSnapshot{}
	// collabFolds intentionally SURVIVE the rebind: official subagent runs
	// span turns and connection epochs, runIds derive from anchor turns and
	// re-observed items are idempotent, so keeping the folds lets post-reconnect
	// wait/close states settle the card instead of freezing it at the
	// spawn-time snapshot (2026-09-12 真机: 重连清空后「已中断 4/4」冻结).
	// Collaboration/goal maps reset because the new epoch re-announces them;
	// collab fold events are not re-announced.
	c.mu.Unlock()
}

func (c *LiveCodec) GoalVersion(threadID string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.goalByThread[threadID].version
}

func (c *LiveCodec) CurrentGoal(threadID string) (core.SessionGoalSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.goalByThread[threadID]
	return cloneGoalSnapshot(state.snapshot), ok
}

func (c *LiveCodec) applyGoalSnapshot(threadID string, snapshot core.SessionGoalSnapshot, expectedVersion *uint64) (core.Event, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current, exists := c.goalByThread[threadID]
	if expectedVersion != nil && current.version != *expectedVersion {
		return core.Event{}, false
	}
	if exists && goalSnapshotsEqual(current.snapshot, snapshot) {
		return core.Event{}, false
	}
	current.version++
	current.snapshot = cloneGoalSnapshot(snapshot)
	c.goalByThread[threadID] = current
	eventSnapshot := cloneGoalSnapshot(snapshot)
	return core.Event{Type: core.EventSessionGoalRecord, SessionID: threadID, ThreadID: threadID, GoalRecord: &eventSnapshot}, true
}

func (c *LiveCodec) ActiveTurn(threadID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnByThread[threadID]
}

func (c *LiveCodec) setActiveTurn(threadID, turnID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if turnID == "" {
		delete(c.turnByThread, threadID)
		return
	}
	c.turnByThread[threadID] = turnID
}

func (c *LiveCodec) UnknownMethods() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.unknown))
	for method, count := range c.unknown {
		out[method] = count
	}
	return out
}

func (c *LiveCodec) Decode(n Notification) []core.Event {
	switch n.Method {
	case "turn/started":
		return c.decodeTurnStarted(n)
	case "turn/completed":
		return c.decodeTurnCompleted(n)
	case "item/agentMessage/delta":
		c.resetRetry(n)
		return decodeRemoteAgentMessageDelta(n)
	case "item/plan/delta":
		// Official PlanDelta is streaming text for a proposed-plan item.
		// Completed item content is authoritative and may not match the
		// concatenated deltas — consume so it is not counted unknown, wait
		// for item/completed.
		c.resetRetry(n)
		return nil
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		c.resetRetry(n)
		return decodeRemoteReasoningDelta(n)
	case "item/started":
		return c.decodeItemStarted(n)
	case "item/completed":
		return c.decodeItemCompleted(n)
	case "thread/tokenUsage/updated":
		return decodeRemoteTokenUsage(n)
	case "turn/plan/updated":
		return decodeRemotePlanUpdated(n)
	case "thread/settings/updated":
		return c.decodeThreadSettingsUpdated(n)
	case "thread/status/changed":
		return c.decodeThreadStatusChanged(n)
	case "thread/goal/updated":
		return c.decodeThreadGoalUpdated(n)
	case "thread/goal/cleared":
		return c.decodeThreadGoalCleared(n)
	case "error":
		return c.decodeErrorNotification(n)
	case "warning", "thread/started", "thread/name/updated",
		"thread/archived", "thread/unarchived", "thread/deleted", "account/rateLimits/updated",
		"remoteControl/status/changed", "serverRequest/resolved",
		"turn/diff/updated":
		// These are official notifications whose state is either fetched through
		// catalog/history or has no core.Event representation yet.
		return nil
	default:
		c.mu.Lock()
		c.unknown[n.Method]++
		c.mu.Unlock()
		return nil
	}
}

func cloneGoalSnapshot(snapshot core.SessionGoalSnapshot) core.SessionGoalSnapshot {
	out := snapshot
	if snapshot.Goal != nil {
		goal := *snapshot.Goal
		if snapshot.Goal.TokenBudget != nil {
			budget := *snapshot.Goal.TokenBudget
			goal.TokenBudget = &budget
		}
		out.Goal = &goal
	}
	return out
}

func goalSnapshotsEqual(a, b core.SessionGoalSnapshot) bool {
	if a.Goal == nil || b.Goal == nil {
		return a.Goal == nil && b.Goal == nil
	}
	left, right := a.Goal, b.Goal
	if left.ThreadID != right.ThreadID || left.Objective != right.Objective || left.Status != right.Status ||
		left.TokensUsed != right.TokensUsed || left.TimeUsedSeconds != right.TimeUsedSeconds ||
		left.CreatedAt != right.CreatedAt || left.UpdatedAt != right.UpdatedAt {
		return false
	}
	if left.TokenBudget == nil || right.TokenBudget == nil {
		return left.TokenBudget == nil && right.TokenBudget == nil
	}
	return *left.TokenBudget == *right.TokenBudget
}

func cloneCollaborationMode(state core.SessionCollaborationMode) core.SessionCollaborationMode {
	state.ReasoningEffort = cloneStringPointer(state.ReasoningEffort)
	return state
}

func collaborationModesEqual(a, b core.SessionCollaborationMode) bool {
	if a.Mode != b.Mode || a.Model != b.Model {
		return false
	}
	if a.ReasoningEffort == nil || b.ReasoningEffort == nil {
		return a.ReasoningEffort == nil && b.ReasoningEffort == nil
	}
	return *a.ReasoningEffort == *b.ReasoningEffort
}

// decodeThreadStatusChanged 消费官方 thread/status/changed（app-server
// ThreadStatusChangedNotification，进程级广播——官方本地客户端转圈/黄点的同源通道）。
// 词表（session-badges 上游对齐方案 §5.2）：Active 无 flags→running、Active 含
// waitingOnApproval/waitingOnUserInput→requiresAction、Idle→idle；SystemError/
// NotLoaded 不映射（线程级系统态/卸载，诚实不冒充 idle/running）。缺 threadId→nil。
func (c *LiveCodec) decodeThreadStatusChanged(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		Status   *struct {
			Type        string   `json:"type"`
			ActiveFlags []string `json:"activeFlags"`
		} `json:"status"`
	}
	if json.Unmarshal(n.Params, &params) != nil {
		return nil
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	if params.ThreadID == "" || params.Status == nil {
		return nil
	}
	var state string
	switch params.Status.Type {
	case "active":
		state = "running"
		if len(params.Status.ActiveFlags) > 0 {
			state = "requiresAction"
		}
	case "idle":
		state = "idle"
	case "systemError":
		// 官方 ThreadStatus.SystemError（红❗持久化，2026-10-01）＝非 active（无在飞
		// turn/挂起）＋上一 turn 系统错误收尾——执行态分解为 idle（防 error 通知丢失时
		// registry 卡 running，status/changed 是权威转移信号）；失败 outcome 由 error
		// 通知实时 settle＋catalog systemError 拉路径持久承担（mapCatalogThread）。
		state = "idle"
	default:
		return nil
	}
	return []core.Event{{
		Type:        core.EventSessionState,
		SessionID:   params.ThreadID,
		SessionState: &core.SessionStateEvent{State: state},
	}}
}

func (c *LiveCodec) decodeThreadSettingsUpdated(n Notification) []core.Event {
	var params struct {
		ThreadID       string          `json:"threadId"`
		ThreadSettings json.RawMessage `json:"threadSettings"`
	}
	if json.Unmarshal(n.Params, &params) != nil || len(params.ThreadSettings) == 0 {
		return nil
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	state, err := decodeCollaborationModeFromThreadSettings(params.ThreadSettings)
	if err != nil || params.ThreadID == "" {
		return nil
	}
	event, applied := c.applyCollaborationMode(params.ThreadID, state, nil)
	if !applied {
		return nil
	}
	return []core.Event{event}
}

func (c *LiveCodec) resetRetry(n Notification) {
	var params struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(n.Params, &params) == nil && params.ThreadID != "" {
		c.mu.Lock()
		delete(c.retryByThread, params.ThreadID)
		c.mu.Unlock()
	}
}

func (c *LiveCodec) decodeTurnStarted(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.ThreadID == "" || params.Turn.ID == "" {
		return nil
	}
	c.mu.Lock()
	c.turnByThread[params.ThreadID] = params.Turn.ID
	delete(c.retryByThread, params.ThreadID)
	delete(c.inFlightPlan, params.ThreadID)
	superseded := c.takeAwaitingPlanLocked(params.ThreadID)
	c.mu.Unlock()
	started := core.Event{Type: core.EventTurnStarted, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.Turn.ID}
	if superseded.itemID == "" {
		return []core.Event{started}
	}
	return []core.Event{
		{Type: core.EventPermissionResolved, SessionID: params.ThreadID, ThreadID: params.ThreadID, RequestID: superseded.itemID, Content: "cancel"},
		started,
	}
}

func (c *LiveCodec) decodeTurnCompleted(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID         string           `json:"id"`
			Status     string           `json:"status"`
			Error      *remoteTurnError `json:"error"`
			DurationMs *int64           `json:"durationMs"`
		} `json:"turn"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.ThreadID == "" {
		return nil
	}
	if params.Turn.ID == "" {
		// Turn.id is required by the official TurnCompletedNotification. Never
		// infer it from a local active-turn map after a malformed wire frame.
		slog.Warn("codex-remote codec: turn/completed missing turn.id, dropping", "thread", params.ThreadID)
		return nil
	}
	c.mu.Lock()
	delete(c.turnByThread, params.ThreadID)
	delete(c.retryByThread, params.ThreadID)
	// Collab workflow folds intentionally SURVIVE turn completion: official
	// runs span turns (spawn turn + later wait turns), and the fold registry
	// itself bounds lifecycle (a new spawn batch supersedes the settled run).
	plan := c.inFlightPlan[params.ThreadID]
	delete(c.inFlightPlan, params.ThreadID)
	emitReview := params.Turn.Status == remoteTurnStatusCompleted &&
		plan.turnID == params.Turn.ID && strings.TrimSpace(plan.text) != ""
	if emitReview {
		c.awaitingPlanReview[plan.itemID] = plan
	}
	c.mu.Unlock()
	event := core.Event{SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.Turn.ID, Done: true}
	if params.Turn.DurationMs != nil {
		event.DurationMs = *params.Turn.DurationMs
	}
	if params.Turn.Status != remoteTurnStatusCompleted {
		message := "turn ended with status " + params.Turn.Status
		if params.Turn.Error != nil && params.Turn.Error.Message != "" {
			message = params.Turn.Error.Message
		}
		event.Type = core.EventError
		event.Error = &remoteOfficialError{message: message}
		return []core.Event{event}
	}
	event.Type = core.EventResult
	if !emitReview {
		return []core.Event{event}
	}
	return []core.Event{codexPlanReviewEvent(plan), event}
}

type remoteOfficialError struct{ message string }

func (e *remoteOfficialError) Error() string { return e.message }

func decodeRemoteAgentMessageDelta(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
		Delta    string `json:"delta"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.Delta == "" {
		return nil
	}
	return []core.Event{{Type: core.EventText, Content: params.Delta, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: params.ItemID}}
}

func decodeRemoteReasoningDelta(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
		Delta    string `json:"delta"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.Delta == "" {
		return nil
	}
	return []core.Event{{Type: core.EventThinking, Content: params.Delta, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: params.ItemID}}
}

type remoteItemNotification struct {
	Item     json.RawMessage `json:"item"`
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
}

func (c *LiveCodec) decodeItemStarted(n Notification) []core.Event {
	var params remoteItemNotification
	if json.Unmarshal(n.Params, &params) != nil {
		return nil
	}
	item := decodeRemoteThreadItem(params.Item)
	if params.ThreadID == "" || params.TurnID == "" || item.ID == "" {
		return nil
	}
	switch item.Type {
	case "userMessage":
		return []core.Event{{Type: core.EventUserMessage, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, Content: item.userText()}}
	case "contextCompaction":
		return []core.Event{{Type: core.EventContextCompressing, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID}}
	case "commandExecution":
		event := remoteToolUseEvent(params, item, "Bash", item.Command)
		event.ActivityActions = canonicalActivityActions(item.CommandActions)
		return []core.Event{event}
	case "fileChange":
		event := remoteToolUseEvent(params, item, "Patch", remoteJSONField(item.Raw, "changes"))
		event.FileChanges = remoteFileChanges(item)
		return []core.Event{event}
	case "mcpToolCall":
		title := strings.TrimSpace(item.Server + ":" + item.Tool)
		events := []core.Event{remoteToolUseEvent(params, item, "MCP", title+"\n"+string(remoteOrEmpty(item.Arguments)))}
		return append(events, c.foldCollabWorkflow(params, item)...)
	case "webSearch":
		return []core.Event{remoteToolUseEvent(params, item, "WebSearch", item.Query)}
	case "dynamicToolCall":
		return []core.Event{remoteToolUseEvent(params, item, item.Tool, string(remoteOrEmpty(item.Arguments)))}
	case "plan":
		// Proposed-plan item start: wait for item/completed (authoritative text).
		return nil
	case "collabAgentToolCall":
		return c.foldCollabWorkflow(params, item)
	case "subAgentActivity":
		return c.foldCollabWorkflow(params, item)
	default:
		return nil
	}
}

// remoteCollabMemberStatus maps the official CollabAgentStatus vocabulary to
// the shared workflow-member status vocabulary (running | completed | failed |
// cancelled | interrupted | pending). Empty maps to the call-level fallback;
// unknown future values pass through verbatim.
func remoteCollabMemberStatus(official string, fallback string) string {
	switch official {
	case "pendingInit", "pending_init":
		// Shared workflow wire has no pending member state. The child exists and
		// has not settled, so it is truthfully part of the running run.
		return "running"
	case "running":
		return "running"
	case "completed":
		return "completed"
	case "errored":
		return "failed"
	case "interrupted":
		return "interrupted"
	case "shutdown":
		// Official AgentStatus::Shutdown is "Agent has been shutdown" after the
		// child finished (protocol.rs:1836). Official iOS shows 已关闭, not a
		// user cancel. Map to completed so the card and task center settle.
		return "completed"
	case "notFound", "not_found":
		return "failed"
	case "":
		return fallback
	default:
		return official
	}
}

func (c *LiveCodec) decodeItemCompleted(n Notification) []core.Event {
	var params remoteItemNotification
	if json.Unmarshal(n.Params, &params) != nil || params.ThreadID == "" || params.TurnID == "" {
		return nil
	}
	item := decodeRemoteThreadItem(params.Item)
	if item.ID == "" {
		return nil
	}
	switch item.Type {
	case "commandExecution":
		event := core.Event{Type: core.EventToolResult, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: "Bash", RequestID: item.ID, ToolStatus: item.CommandStatus, ActivityActions: canonicalActivityActions(item.CommandActions)}
		if item.AggregatedOutput != nil {
			event.ToolResult = *item.AggregatedOutput
		}
		if item.ExitCode != nil {
			code := int(*item.ExitCode)
			event.ToolExitCode = &code
			success := code == 0
			event.ToolSuccess = &success
		}
		return []core.Event{event}
	case "fileChange":
		return []core.Event{{Type: core.EventToolResult, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: "Patch", RequestID: item.ID, ToolStatus: item.PatchStatus, ToolResult: remoteJSONField(item.Raw, "changes"), FileChanges: remoteFileChanges(item)}}
	case "mcpToolCall":
		event := core.Event{Type: core.EventToolResult, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: "MCP", RequestID: item.ID, ToolStatus: item.ToolStatus}
		if len(item.Result) > 0 {
			event.ToolResult = string(item.Result)
		} else if len(item.ToolError) > 0 {
			event.ToolResult = string(item.ToolError)
		}
		return append([]core.Event{event}, c.foldCollabWorkflow(params, item)...)
	case "contextCompaction":
		return []core.Event{{Type: core.EventContextCompressed, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID}}
	case "dynamicToolCall":
		return []core.Event{{Type: core.EventToolResult, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: item.Tool, RequestID: item.ID, ToolStatus: item.ToolStatus, ToolResult: string(remoteOrEmpty(item.Arguments))}}
	case "webSearch":
		return []core.Event{{Type: core.EventToolResult, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: "WebSearch", RequestID: item.ID}}
	case "plan":
		c.rememberInFlightPlan(params.ThreadID, params.TurnID, item.ID, item.Text)
		return nil
	case "collabAgentToolCall":
		return c.foldCollabWorkflow(params, item)
	case "subAgentActivity":
		return c.foldCollabWorkflow(params, item)
	default:
		// userMessage, agentMessage and reasoning are represented by item/started
		// or their delta notifications; completed snapshots must not duplicate.
		return nil
	}
}

func truncateRemoteWorkflowName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	runes := []rune(value)
	if len(runes) > 80 {
		return string(runes[:80])
	}
	return value
}

func remoteCreateThreadID(raw json.RawMessage) string {
	for _, text := range remoteMCPResultTexts(raw) {
		var result struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal([]byte(text), &result) == nil && result.ThreadID != "" {
			return result.ThreadID
		}
	}
	return ""
}

func remoteWaitThreadStatuses(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	for _, text := range remoteMCPResultTexts(raw) {
		var result struct {
			Polls []struct {
				Thread struct {
					ID     string `json:"id"`
					Status struct {
						Type string `json:"type"`
					} `json:"status"`
				} `json:"thread"`
				LatestTurn *struct {
					Status string `json:"status"`
				} `json:"latestTurn"`
			} `json:"polls"`
		}
		if json.Unmarshal([]byte(text), &result) != nil {
			continue
		}
		for _, poll := range result.Polls {
			status := ""
			if poll.LatestTurn != nil {
				status = remoteChildTaskStatus(poll.LatestTurn.Status)
			}
			if status == "" {
				status = remoteChildTaskStatus(poll.Thread.Status.Type)
			}
			if poll.Thread.ID != "" && status != "" {
				out[poll.Thread.ID] = status
			}
		}
	}
	return out
}

func remoteMCPResultTexts(raw json.RawMessage) []string {
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if json.Unmarshal(raw, &result) != nil || result.IsError {
		return nil
	}
	texts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			texts = append(texts, content.Text)
		}
	}
	return texts
}

func remoteChildTaskStatus(status string) string {
	switch status {
	case "completed", "idle":
		return core.WorkflowStatusCompleted
	case "failed", "error", "systemError":
		return core.WorkflowStatusFailed
	case "interrupted":
		return core.WorkflowStatusInterrupted
	case "cancelled", "canceled":
		return core.WorkflowStatusCancelled
	case "running", "inProgress", "active":
		return core.WorkflowStatusRunning
	default:
		return ""
	}
}

func remoteToolUseEvent(params remoteItemNotification, item remoteThreadItem, name, input string) core.Event {
	return core.Event{Type: core.EventToolUse, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, ItemID: item.ID, ToolName: name, ToolInput: input, RequestID: item.ID}
}

func remoteFileChanges(item remoteThreadItem) []core.FileChange {
	changes := make([]core.FileChange, 0, len(item.Changes))
	for _, change := range item.Changes {
		mapped := core.FileChange{Path: change.Path, Kind: change.changeKind(), Diff: change.Diff}
		if movePath := change.movePath(); movePath != nil {
			mapped.MovePath = *movePath
		}
		changes = append(changes, mapped)
	}
	return changes
}

func decodeRemoteTokenUsage(n Notification) []core.Event {
	var params struct {
		ThreadID   string `json:"threadId"`
		TokenUsage struct {
			Total struct {
				TotalTokens           int `json:"totalTokens"`
				InputTokens           int `json:"inputTokens"`
				CachedInputTokens     int `json:"cachedInputTokens"`
				OutputTokens          int `json:"outputTokens"`
				ReasoningOutputTokens int `json:"reasoningOutputTokens"`
			} `json:"total"`
			Last struct {
				TotalTokens           int `json:"totalTokens"`
				InputTokens           int `json:"inputTokens"`
				CachedInputTokens     int `json:"cachedInputTokens"`
				OutputTokens          int `json:"outputTokens"`
				ReasoningOutputTokens int `json:"reasoningOutputTokens"`
			} `json:"last"`
			ModelContextWindow int `json:"modelContextWindow"`
		} `json:"tokenUsage"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.ThreadID == "" {
		return nil
	}
	usage := &core.ContextUsage{
		UsedTokens: params.TokenUsage.Last.TotalTokens, TotalTokens: params.TokenUsage.Total.TotalTokens,
		InputTokens: params.TokenUsage.Last.InputTokens, CachedInputTokens: params.TokenUsage.Last.CachedInputTokens,
		OutputTokens: params.TokenUsage.Last.OutputTokens, ReasoningOutputTokens: params.TokenUsage.Last.ReasoningOutputTokens,
		ContextWindow: params.TokenUsage.ModelContextWindow,
	}
	return []core.Event{{Type: core.EventContextUsageUpdated, SessionID: params.ThreadID, ThreadID: params.ThreadID, ContextUsage: usage}}
}

func (c *LiveCodec) decodeErrorNotification(n Notification) []core.Event {
	c.mu.Lock()
	if len(c.lastErrorParams) > 0 && bytes.Equal(c.lastErrorParams, n.Params) {
		c.suppressedErrorNotifications++
		count := c.suppressedErrorNotifications
		c.mu.Unlock()
		if count == 1_000 || count%100_000 == 0 {
			slog.Warn("codex-remote suppressed byte-identical error notifications",
				"count", count)
		}
		return nil
	}
	c.mu.Unlock()

	var params struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		WillRetry bool   `json:"willRetry"`
		ThreadID  string `json:"threadId"`
		TurnID    string `json:"turnId"`
	}
	if json.Unmarshal(n.Params, &params) != nil || strings.TrimSpace(params.Error.Message) == "" {
		return nil
	}
	if strings.HasPrefix(params.ThreadID, "__remote") {
		// Transport-level diagnostic stamped with a synthetic sentinel thread id
		// (e.g. "__remote_control_transport"). It names no session/turn, so it
		// must not become a core session event; the transport-side ack circuit
		// breaker (stream.go) reacts to the same sentinel on the wire.
		return nil
	}
	if params.WillRetry {
		c.mu.Lock()
		c.lastErrorParams = nil
		c.retryByThread[params.ThreadID]++
		attempt := c.retryByThread[params.ThreadID]
		c.mu.Unlock()
		return []core.Event{{Type: core.EventRetryStatus, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, RetryAttempt: attempt, Content: params.Error.Message}}
	}
	c.mu.Lock()
	c.lastErrorParams = append([]byte(nil), n.Params...)
	c.mu.Unlock()
	return []core.Event{{Type: core.EventError, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, Error: &remoteOfficialError{message: params.Error.Message}}}
}

// SuppressedErrorNotifications reports how many byte-identical terminal error
// notifications were dropped after the first was decoded and delivered.
func (c *LiveCodec) SuppressedErrorNotifications() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.suppressedErrorNotifications
}

func decodeRemotePlanUpdated(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Plan     []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if json.Unmarshal(n.Params, &params) != nil || params.ThreadID == "" {
		return nil
	}
	todos := make([]core.Todo, 0, len(params.Plan))
	for _, entry := range params.Plan {
		if strings.TrimSpace(entry.Step) == "" {
			continue
		}
		status := entry.Status
		if status == "inProgress" {
			status = "in_progress"
		}
		todos = append(todos, core.Todo{Content: entry.Step, Status: status, Priority: "normal"})
	}
	if len(todos) == 0 {
		return nil
	}
	return []core.Event{{Type: core.EventPlan, SessionID: params.ThreadID, ThreadID: params.ThreadID, TurnID: params.TurnID, Plan: todos}}
}

func remoteJSONField(raw json.RawMessage, key string) string {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	return string(remoteOrEmpty(object[key]))
}

func remoteOrEmpty(raw []byte) []byte {
	if raw == nil {
		return []byte{}
	}
	return raw
}
