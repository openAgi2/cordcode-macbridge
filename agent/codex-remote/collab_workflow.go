package codexremote

// Codex collab subagent operations (collabAgentToolCall spawn/wait/close items
// and the codex_app create_thread / wait_threads MCP tools) do not carry a
// runId: their item ids identify single OPERATIONS, not one user-visible
// workflow. Officially one logical run also spans MULTIPLE turns — the spawn
// turn creates the children and later turns keep polling them. Fold the
// operations of one run into a single keyed workflow snapshot anchored at the
// SPAWN turn: runID = codex-collab:<spawnTurnID> stays stable across turns, so
// the projection reducer's runId-keyed in-place upsert converges wait/activity
// updates from later turns onto the one card the first spawn anchored
// (official workflow-run parity: "first spawn anchors the card, later
// operations update the same members in place"). runIds deliberately derive
// from anchor TURNS, never from registry generations, so replaying the same
// official items through any registry state converges to the same runId.

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const codexCollabRunPrefix = "codex-collab:"

type codexCollabMemberState struct {
	seq     int
	label   string
	childID string
	status  string
}

type codexCollabWorkflowFold struct {
	runID        string
	anchorTurnID string
	name         string
	members      []codexCollabMemberState
	byChild      map[string]int
}

// newCodexCollabWorkflowFold anchors a run at the turn that first spawns it.
func newCodexCollabWorkflowFold(anchorTurnID string) *codexCollabWorkflowFold {
	return &codexCollabWorkflowFold{
		runID:        codexCollabRunPrefix + anchorTurnID,
		anchorTurnID: anchorTurnID,
		name:         "Subagents",
		byChild:      map[string]int{},
	}
}

func normalizeCollabTool(tool string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(tool)), "_", "")
}

func collabMemberLabel(prompt string, seq int) string {
	label := strings.TrimSpace(prompt)
	if runes := []rune(label); len(runes) > 80 {
		label = string(runes[:80])
	}
	if label == "" {
		return "Agent-" + strconv.Itoa(seq)
	}
	return label
}

// observe returns false until a real spawned child exists. wait/close calls
// for agents created by older turns therefore cannot pollute the current card.
func (f *codexCollabWorkflowFold) observe(item remoteThreadItem) bool {
	if f == nil || f.runID == "" {
		return false
	}
	if item.Type == "mcpToolCall" {
		return f.observeAppTool(item)
	}
	tool := normalizeCollabTool(item.CollabTool)
	spawn := tool == "spawnagent"
	changed := false
	if spawn {
		for _, receiver := range item.CollabReceivers {
			childID := strings.TrimSpace(receiver)
			if childID == "" {
				continue
			}
			if _, exists := f.byChild[childID]; exists {
				continue
			}
			seq := len(f.members) + 1
			initialStatus := core.WorkflowStatusRunning
			if item.CollabStatus == "failed" {
				initialStatus = core.WorkflowStatusFailed
			}
			f.byChild[childID] = len(f.members)
			f.members = append(f.members, codexCollabMemberState{
				seq: seq, label: collabMemberLabel(item.CollabPrompt, seq),
				childID: childID, status: initialStatus,
			})
			changed = true
		}
	}

	for childID, state := range item.CollabAgentStates {
		idx, exists := f.byChild[strings.TrimSpace(childID)]
		if !exists {
			// Only spawn establishes membership. This is important for Goal
			// turns that first close stale agents left by an earlier run.
			continue
		}
		status := remoteCollabMemberStatus(state.Status, f.members[idx].status)
		if status != f.members[idx].status {
			f.members[idx].status = status
			changed = true
		}
	}
	return changed
}

func (f *codexCollabWorkflowFold) upsertChild(childID, label, status string) bool {
	childID = strings.TrimSpace(childID)
	if childID == "" {
		return false
	}
	if idx, exists := f.byChild[childID]; exists {
		changed := false
		if status != "" && status != f.members[idx].status {
			f.members[idx].status = status
			changed = true
		}
		if label != "" && strings.HasPrefix(f.members[idx].label, "Agent-") {
			f.members[idx].label = label
			changed = true
		}
		return changed
	}
	seq := len(f.members) + 1
	if strings.TrimSpace(label) == "" {
		label = collabMemberLabel("", seq)
	}
	if status == "" {
		status = core.WorkflowStatusRunning
	}
	f.byChild[childID] = len(f.members)
	f.members = append(f.members, codexCollabMemberState{
		seq: seq, label: label, childID: childID, status: status,
	})
	return true
}

func (f *codexCollabWorkflowFold) observeAppTool(item remoteThreadItem) bool {
	if item.Server != "codex_app" {
		return false
	}
	switch item.Tool {
	case "create_thread":
		var args struct {
			Title  string `json:"title"`
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(item.Arguments, &args)
		label := strings.TrimSpace(args.Title)
		if label == "" {
			label = strings.TrimSpace(args.Prompt)
		}
		label = truncateRemoteWorkflowName(label, "")
		if label != "" && (f.name == "" || f.name == "Subagents") {
			f.name = label
		}
		status := core.WorkflowStatusRunning
		if item.ToolStatus == "failed" || item.ToolStatus == "error" {
			status = core.WorkflowStatusFailed
		}
		return f.upsertChild(remoteCreateThreadID(item.Result), label, status)
	case "wait_threads":
		var args struct {
			Targets []struct {
				ThreadID string `json:"threadId"`
			} `json:"targets"`
		}
		_ = json.Unmarshal(item.Arguments, &args)
		statuses := remoteWaitThreadStatuses(item.Result)
		changed := false
		for _, target := range args.Targets {
			childID := strings.TrimSpace(target.ThreadID)
			if childID == "" {
				continue
			}
			if status, ok := statuses[childID]; ok {
				if f.upsertChild(childID, "", status) {
					changed = true
				}
				continue
			}
			// Known member absent from this poll: official polls are
			// cumulative, so absence is not evidence of reset. Keep the
			// member's current status — only a poll that NAMES the child can
			// settle it (e.g. overwrite a stale activity-derived interrupted
			// back to completed); an unknown child still joins the card.
			if _, member := f.byChild[childID]; !member {
				if f.upsertChild(childID, "", core.WorkflowStatusRunning) {
					changed = true
				}
			}
		}
		return changed
	default:
		return false
	}
}

// adoptStates establishes members from a WAIT item's complete agentsStates on
// a fold that lost (or never saw) its spawns — e.g. the runtime started or the
// registry was cleared mid-run. Official wait items carry the full per-agent
// state map, so the card can rebuild from them instead of freezing at whatever
// snapshot preceded the gap (2026-09-12 真机: 重连后 wait/close 全部失效).
// Only a WAIT may adopt, and only onto a memberless fold: close of stale
// agents from an earlier run must never create membership (the spawn-only
// rule stays intact for folds that did see their spawns).
func (f *codexCollabWorkflowFold) adoptStates(item remoteThreadItem) bool {
	if f == nil || len(f.members) != 0 {
		return false
	}
	tool := normalizeCollabTool(item.CollabTool)
	if tool != "wait" && tool != "waitthreads" {
		return false
	}
	changed := false
	for childID, state := range item.CollabAgentStates {
		if f.upsertChild(childID, "", remoteCollabMemberStatus(state.Status, core.WorkflowStatusRunning)) {
			changed = true
		}
	}
	return changed
}

func (f *codexCollabWorkflowFold) observeActivity(item remoteThreadItem) bool {
	if f == nil || f.runID == "" {
		return false
	}
	childID := strings.TrimSpace(item.CollabAgentThreadID)
	idx, exists := f.byChild[childID]
	if !exists || childID == "" {
		return false
	}
	var status string
	switch strings.ToLower(strings.TrimSpace(item.CollabActivityKind)) {
	case "started", "interacted":
		status = core.WorkflowStatusRunning
	case "completed":
		status = core.WorkflowStatusCompleted
	case "interrupted":
		status = core.WorkflowStatusInterrupted
	default:
		return false
	}
	if status == f.members[idx].status {
		return false
	}
	f.members[idx].status = status
	return true
}

func (f *codexCollabWorkflowFold) snapshot() (core.WorkflowRunEvent, bool) {
	if f == nil || len(f.members) == 0 {
		return core.WorkflowRunEvent{}, false
	}
	status := core.WorkflowStatusCompleted
	hasFailed, hasCancelled, hasInterrupted := false, false, false
	members := make([]core.WorkflowRunMember, 0, len(f.members))
	for _, member := range f.members {
		memberStatus := member.status
		if memberStatus == "" {
			memberStatus = core.WorkflowStatusRunning
		}
		switch memberStatus {
		case core.WorkflowStatusRunning, "pending":
			status = core.WorkflowStatusRunning
		case core.WorkflowStatusFailed:
			hasFailed = true
		case core.WorkflowStatusCancelled:
			hasCancelled = true
		case core.WorkflowStatusInterrupted:
			hasInterrupted = true
		}
		members = append(members, core.WorkflowRunMember{
			Seq: member.seq, Label: member.label,
			ChildSessionID: member.childID, Status: memberStatus,
		})
	}
	if status != core.WorkflowStatusRunning {
		switch {
		case hasFailed:
			status = core.WorkflowStatusFailed
		case hasCancelled:
			status = core.WorkflowStatusCancelled
		case hasInterrupted:
			status = core.WorkflowStatusInterrupted
		}
	}
	name := strings.TrimSpace(f.name)
	if name == "" {
		name = "Subagents"
	}
	return core.WorkflowRunEvent{
		RunID: f.runID, Name: name, Status: status,
		Phases: []core.WorkflowRunPhase{{Phase: nil, Members: members}},
	}, true
}

// settled reports whether every member reached a terminal status. A spawn
// arriving after the thread's fold settled opens the NEXT run instead of
// appending to the finished card.
func (f *codexCollabWorkflowFold) settled() bool {
	if f == nil || len(f.members) == 0 {
		return false
	}
	snapshot, ok := f.snapshot()
	return ok && snapshot.Status != core.WorkflowStatusRunning
}

// collabFoldRegistry routes operation items onto cross-turn workflow folds.
// One registry serves one surface (the live codec, or one session's cold
// history mapping); it is NOT shared between them — both derive identical
// runIds from anchor turns, so their parts coalesce downstream.
type collabFoldRegistry struct {
	current map[string]*codexCollabWorkflowFold
	byChild map[string]*codexCollabWorkflowFold
}

func newCollabFoldRegistry() *collabFoldRegistry {
	return &collabFoldRegistry{
		current: map[string]*codexCollabWorkflowFold{},
		byChild: map[string]*codexCollabWorkflowFold{},
	}
}

// route resolves the fold an item belongs to. Only a spawn establishes a new
// run: re-observing a known child is idempotent, a spawn joins the thread's
// still-running fold (later operations update the same members in place), and
// a spawn after that fold settled opens the next run. Wait-class items whose
// children are all unknown anchor a fallback run at the arrival turn (codec
// restart mid-run, detail fetched before the spawn turn) without detaching
// routing for the thread's live run; activity/close and other classes only
// update folds they can resolve and never open runs.
func (r *collabFoldRegistry) route(threadID, turnID string, children []string, spawn, waitFallback bool) *codexCollabWorkflowFold {
	for _, child := range children {
		if fold, ok := r.byChild[child]; ok {
			return fold
		}
	}
	if spawn {
		if current := r.current[threadID]; current != nil && !current.settled() {
			return current
		}
		return r.open(threadID, turnID)
	}
	if waitFallback {
		return newCodexCollabWorkflowFold(turnID)
	}
	return nil
}

// open anchors a new run for the thread, releasing the previous fold's child
// routing so a late operation for the superseded batch cannot reopen it.
func (r *collabFoldRegistry) open(threadID, anchorTurnID string) *codexCollabWorkflowFold {
	if old := r.current[threadID]; old != nil {
		for child := range old.byChild {
			if r.byChild[child] == old {
				delete(r.byChild, child)
			}
		}
	}
	fold := newCodexCollabWorkflowFold(anchorTurnID)
	r.current[threadID] = fold
	return fold
}

// index publishes the children an observation added to the fold so operations
// from OTHER turns route to the same run. Children already routed to a
// different fold are never stolen (mixed-batch waits may upsert foreign
// members without taking over their routing).
func (r *collabFoldRegistry) index(fold *codexCollabWorkflowFold, children []string) {
	if fold == nil {
		return
	}
	for _, child := range children {
		child = strings.TrimSpace(child)
		if child == "" {
			continue
		}
		if _, member := fold.byChild[child]; !member {
			continue
		}
		if existing, routed := r.byChild[child]; routed && existing != fold {
			continue
		}
		r.byChild[child] = fold
	}
}

// collabRoute classifies a collab item for fold routing: the child thread ids
// it references (receivers ∪ agent states, the created thread, poll targets,
// the activity child), whether it is spawn-class, and whether it is a wait
// that may anchor a fallback run when its children are unknown. ok=false for
// non-collab items.
func collabRoute(item remoteThreadItem) (children []string, spawn, waitFallback, ok bool) {
	seen := map[string]bool{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			children = append(children, id)
		}
	}
	switch item.Type {
	case "collabAgentToolCall":
		for _, receiver := range item.CollabReceivers {
			add(receiver)
		}
		for child := range item.CollabAgentStates {
			add(child)
		}
		tool := normalizeCollabTool(item.CollabTool)
		return children, tool == "spawnagent", tool == "wait" || tool == "waitthreads", true
	case "mcpToolCall":
		if item.Server != "codex_app" {
			return nil, false, false, false
		}
		switch item.Tool {
		case "create_thread":
			add(remoteCreateThreadID(item.Result))
			return children, true, false, true
		case "wait_threads":
			var args struct {
				Targets []struct {
					ThreadID string `json:"threadId"`
				} `json:"targets"`
			}
			_ = json.Unmarshal(item.Arguments, &args)
			for _, target := range args.Targets {
				add(target.ThreadID)
			}
			return children, false, true, true
		}
		return nil, false, false, false
	case "subAgentActivity":
		add(item.CollabAgentThreadID)
		return children, false, false, true
	}
	return nil, false, false, false
}

func (c *LiveCodec) foldCollabWorkflow(params remoteItemNotification, item remoteThreadItem) []core.Event {
	if params.ThreadID == "" || params.TurnID == "" || item.ID == "" {
		return nil
	}
	children, spawn, waitFallback, applicable := collabRoute(item)
	if !applicable {
		return nil
	}
	c.mu.Lock()
	fold := c.collabFolds.route(params.ThreadID, params.TurnID, children, spawn, waitFallback)
	var events []core.Event
	if fold != nil {
		changed := fold.adoptStates(item)
		if item.Type == "subAgentActivity" {
			changed = fold.observeActivity(item) || changed
		} else {
			changed = fold.observe(item) || changed
		}
		c.collabFolds.index(fold, children)
		if snapshot, valid := fold.snapshot(); changed && valid {
			// TurnID is the run's ANCHOR turn, not the observing turn: later
			// turns' wait/activity updates must land on the card the first
			// spawn anchored (the reducer keys workflow parts by runId and
			// keeps the first owning turn).
			events = []core.Event{{
				Type: core.EventWorkflowRun, SessionID: params.ThreadID, ThreadID: params.ThreadID,
				TurnID: fold.anchorTurnID, ItemID: snapshot.RunID, WorkflowRun: &snapshot,
			}}
		}
	}
	c.mu.Unlock()
	return events
}

// remoteCollabHistoryFolds is the session-scoped cross-turn state for cold
// collab folding. Every cold mapping surface for a session (full-thread
// reads, paginated history, per-turn lazy detail) shares one registry so
// waits and activities observed in later turns fold onto the run the spawn
// turn anchored — the same runId discipline as the live codec's registry.
// State lives for the runtime process: re-observing the same official items
// is idempotent and runIds derive from anchor turns, so replays converge.
type remoteCollabHistoryFolds struct {
	threadID string
	mu       sync.Mutex
	reg      *collabFoldRegistry
}

func newRemoteCollabHistoryFolds(threadID string) *remoteCollabHistoryFolds {
	return &remoteCollabHistoryFolds{threadID: threadID, reg: newCollabFoldRegistry()}
}

// sessionCollabFolds returns the agent-lifetime fold context for a session
// (lazily created). Concurrent per-turn detail mappings for one session
// serialize on the context lock inside mapItem: cross-turn fold writes into
// an anchor turn's parts must not race that turn's own part appends.
func (a *Agent) sessionCollabFolds(sessionID string) *remoteCollabHistoryFolds {
	a.collabFoldMu.Lock()
	defer a.collabFoldMu.Unlock()
	if a.collabFolds == nil {
		a.collabFolds = map[string]*remoteCollabHistoryFolds{}
	}
	folds, ok := a.collabFolds[sessionID]
	if !ok {
		folds = newRemoteCollabHistoryFolds(sessionID)
		a.collabFolds[sessionID] = folds
	}
	return folds
}

// fold applies one collab item to the session registry and materializes the
// run's latest whole-value part. holders carries the CURRENT mapping call's
// turns by id: an update whose anchor turn is being mapped in the same pass
// upserts the part in the anchor turn; otherwise (lazy pages delivered
// newest-first, per-turn detail fetched before the spawn turn) the part lands
// in the observing turn while keeping the anchored runId — the projection
// reducer's runId-keyed upsert coalesces it onto the anchor turn once that
// turn hydrates. The part is written even when the observation changed
// nothing: mapping is stateless per call, so re-reads must still materialize
// the run.
func (h *remoteCollabHistoryFolds) fold(turn *core.TurnScopedHistoryTurn, item remoteThreadItem, holders map[string]*core.TurnScopedHistoryTurn) {
	if turn == nil || strings.TrimSpace(turn.TurnID) == "" {
		return
	}
	children, spawn, waitFallback, applicable := collabRoute(item)
	if !applicable {
		return
	}
	fold := h.reg.route(h.threadID, turn.TurnID, children, spawn, waitFallback)
	if fold == nil {
		return
	}
	if fold.adoptStates(item) {
		h.reg.index(fold, children)
	}
	if item.Type == "subAgentActivity" {
		fold.observeActivity(item)
	} else {
		fold.observe(item)
	}
	h.reg.index(fold, children)
	snapshot, valid := fold.snapshot()
	if !valid {
		return
	}
	part := remoteWorkflowPart(&snapshot, fold.runID)
	holder := turn
	if anchor, ok := holders[fold.anchorTurnID]; ok && anchor != nil {
		holder = anchor
	}
	for i := range holder.Parts {
		if stringValue(holder.Parts[i]["type"]) == "workflow" && stringValue(holder.Parts[i]["workflowId"]) == fold.runID {
			holder.Parts[i] = part
			return
		}
	}
	holder.Parts = append(holder.Parts, part)
}

