package gobridge

import (
	"strings"
	"sync"
	"time"
)

// ProjectionReducer maintains the authoritative in-memory SessionProjection per
// (backendId, sessionId), reduced from the EventPublisher funnel. It is the single source
// both push (projection_patch) and pull (get_session_projection) read — design §6.4 rule 4
// (pull reads the SAME state push produced).
//
// Phase 1 scope: only the Codex file-relay/rollout path feeds meaningful state. Rollout
// frames carry identity in Data — text/reasoning itemId == lifecycle turn_id (== the turn's
// turnId), user_message itemId == response_item.id (+ turnId), tools itemId == call_id — and
// bypass DeltaBatcher, so parts attribute to the correct turn. Driver/agent-event frames lack
// identity (events.go turn_started is hardcoded turnId:""; DeltaBatcher.emit strips Data to
// {"delta":text}) and are SKIPPED here until the Phase 3 turnId plumbing lands.
//
// Concurrency: Apply is called from EventPublisher.PublishLogical under the publisher lock
// (p.mu), so it takes r.mu nested under p.mu. Snapshot/FlushPatch take only r.mu — no reverse
// ordering, no deadlock.
type ProjectionReducer struct {
	mu       sync.Mutex
	sessions map[string]*projectionSession // keyed backendID + "\x00" + sessionID
	now      func() int64                  // epoch-ms clock, injectable for tests
}

// projectionSession is the per-session projection plus the pending delta accumulator used by
// FlushPatch to build coalesced patches.
type projectionSession struct {
	projection     SessionProjection
	lastAppliedRev int // highest committed input PerSessionSeq (idempotency guard)
	lastFlushedRev int // highest rev emitted in a patch (delta base for next patch)

	// pending deltas accumulated since lastFlushedRev; cleared by FlushPatch.
	textAppends map[string][]string       // assistant messageId -> delta chunks (append_text)
	thinking    map[string]string         // assistant messageId -> full accumulated reasoning (set_thinking)
	tools       map[string]ProjectionPart // tool callId -> latest tool part (upsert_tool)
	upsertTurns map[string]TurnProjection // turnId -> latest whole-turn snapshot (upsertTurns)
	execution   *ExecutionView            // pending execution change
}

// NewProjectionReducer creates an empty reducer.
func NewProjectionReducer() *ProjectionReducer {
	return &ProjectionReducer{
		sessions: make(map[string]*projectionSession),
		now:      func() int64 { return time.Now().UnixMilli() },
	}
}

func projectionSessionKey(backendID, sessionID string) string {
	return backendID + "\x00" + sessionID
}

// asDataMap extracts the Data payload as a map; returns nil for non-map payloads.
func asDataMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func dataString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (ps *projectionSession) turnByID(turnID string) *TurnProjection {
	for i := range ps.projection.Turns {
		if ps.projection.Turns[i].TurnID == turnID {
			return &ps.projection.Turns[i]
		}
	}
	return nil
}

func (ps *projectionSession) upsertTurn(turn TurnProjection) {
	if turn.TurnID == "" {
		return
	}
	if t := ps.turnByID(turn.TurnID); t != nil {
		// Merge: keep existing user/assistant if the incoming snapshot omits them.
		if turn.Status != "" {
			t.Status = turn.Status
		}
		if turn.StartedAt != 0 {
			t.StartedAt = turn.StartedAt
		}
		if turn.CompletedAt != 0 {
			t.CompletedAt = turn.CompletedAt
		}
		if turn.User != nil {
			t.User = turn.User
		}
		if turn.Assistant != nil {
			t.Assistant = turn.Assistant
		}
		if ps.upsertTurns != nil {
			ps.upsertTurns[turn.TurnID] = *t
		}
		return
	}
	ps.projection.Turns = append(ps.projection.Turns, turn)
	if ps.upsertTurns != nil {
		ps.upsertTurns[turn.TurnID] = turn
	}
}

// markRunning sets session execution to running for turnID (design §7.4). Content
// events must re-arm after a prior turn_completed left phase=idle.
func (ps *projectionSession) markRunning(turnID string) {
	if turnID == "" {
		return
	}
	exec := ExecutionView{Phase: "running", ActiveTurnID: turnID}
	ps.projection.Execution = exec
	ps.execution = &exec
	// Keep turn status running unless already settled (do not un-complete).
	if t := ps.turnByID(turnID); t != nil && t.Status != "completed" && t.Status != "aborted" && t.Status != "error" {
		t.Status = "running"
		if ps.upsertTurns != nil {
			ps.upsertTurns[turnID] = *t
		}
	}
}

// latestRunningTurnID prefers the last turn with status running/pending; else last turn id.
func (ps *projectionSession) latestRunningTurnID() string {
	for i := len(ps.projection.Turns) - 1; i >= 0; i-- {
		st := ps.projection.Turns[i].Status
		if st == "running" || st == "pending" {
			return ps.projection.Turns[i].TurnID
		}
	}
	if n := len(ps.projection.Turns); n > 0 {
		return ps.projection.Turns[n-1].TurnID
	}
	return ""
}

// ensureAssistantTextPart returns the assistant message's trailing text part, creating one if
// the last part is not text. Used by append_text accumulation.
func (m *MessageProjection) ensureTrailingTextPart() *ProjectionPart {
	if len(m.Parts) == 0 || m.Parts[len(m.Parts)-1].Type != "text" {
		m.Parts = append(m.Parts, ProjectionPart{Type: "text", Presentation: "progress"})
	}
	return &m.Parts[len(m.Parts)-1]
}

func classifyProjectionTextPresentation(message *MessageProjection, completed bool) {
	if message == nil {
		return
	}
	lastText := -1
	for index := range message.Parts {
		if message.Parts[index].Type == "text" && strings.TrimSpace(message.Parts[index].Text) != "" {
			lastText = index
		}
	}
	for index := range message.Parts {
		if message.Parts[index].Type != "text" {
			continue
		}
		message.Parts[index].Presentation = "progress"
	}
	if completed && lastText >= 0 {
		message.Parts[lastText].Presentation = "final"
	}
}

// Apply reduces one stamped EventMessage into the session projection and records pending
// deltas for the next FlushPatch. Safe to call for any event; non-attributable events
// (no identity) are ignored so Phase 1 stays scoped to the rollout path.
func (r *ProjectionReducer) Apply(msg EventMessage) {
	if r == nil || msg.BackendID == "" || msg.SessionID == "" || msg.PerSessionSeq == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	key := projectionSessionKey(msg.BackendID, msg.SessionID)
	ps := r.sessions[key]
	if ps == nil {
		ps = &projectionSession{
			projection: SessionProjection{
				SessionID: msg.SessionID,
				Execution: ExecutionView{Phase: "idle"},
			},
			textAppends: make(map[string][]string),
			thinking:    make(map[string]string),
			tools:       make(map[string]ProjectionPart),
			upsertTurns: make(map[string]TurnProjection),
		}
		r.sessions[key] = ps
	}

	// Idempotency is tracked against the input event sequence, while SyncRev is a
	// projection-owned revision. Ignored/unattributable live events must not create
	// holes or false commits in the projection revision domain.
	if ps.lastAppliedRev > 0 && msg.PerSessionSeq <= ps.lastAppliedRev {
		return
	}
	commit := func() {
		ps.lastAppliedRev = msg.PerSessionSeq
		ps.projection.SyncRev++
		if msg.BridgeEpoch != "" {
			ps.projection.BridgeEpoch = msg.BridgeEpoch
		}
		ps.projection.UpdatedAt = r.now()
	}

	data := asDataMap(msg.Data)
	switch msg.Event {
	case "turn_started":
		turnID := dataString(data, "turnId")
		if turnID == "" {
			return // driver path (events.go hardcodes turnId:""); skip until Phase 3
		}
		commit()
		ps.upsertTurn(TurnProjection{TurnID: turnID, Status: "running", StartedAt: ps.projection.UpdatedAt})
		ps.markRunning(turnID)

	case "user_message":
		turnID := dataString(data, "turnId")
		itemID := dataString(data, "itemId")
		text := dataString(data, "text")
		if turnID == "" {
			turnID = itemID // fallback: attribute to the message id if no explicit turnId
		}
		if turnID == "" {
			return
		}
		commit()
		ps.upsertTurn(TurnProjection{
			TurnID: turnID,
			Status: "running",
			User:   &MessageProjection{ID: itemID, Role: "user", Parts: []ProjectionPart{{Type: "text", Text: text}}},
		})
		// Design §7.4: any in-flight content must keep execution.running. After cold
		// hydrate the last completed turn leaves phase=idle; a new user_message without
		// a re-emitted turn_started must still arm the UI (owner 2026-07-25: reopen app
		// → prompt+thinking then sticky 完成态 because phase stayed idle).
		ps.markRunning(turnID)

	case "text_delta":
		// itemId == lifecycle turn_id == the turn's turnId == the assistant message id.
		turnID := dataString(data, "itemId")
		delta := dataString(data, "delta")
		if turnID == "" {
			return // driver path lacks itemId; skip
		}
		commit()
		t := ps.turnByID(turnID)
		if t == nil {
			ps.upsertTurn(TurnProjection{TurnID: turnID, Status: "running"})
			t = ps.turnByID(turnID)
		}
		if t.Assistant == nil {
			t.Assistant = &MessageProjection{ID: turnID, Role: "assistant"}
		}
		var tp *ProjectionPart
		newPart, _ := data["newPart"].(bool)
		if newPart {
			t.Assistant.Parts = append(t.Assistant.Parts, ProjectionPart{Type: "text", Presentation: "progress"})
			tp = &t.Assistant.Parts[len(t.Assistant.Parts)-1]
		} else {
			tp = t.Assistant.ensureTrailingTextPart()
		}
		tp.Text += delta
		if newPart {
			ps.upsertTurns[turnID] = *t
		} else {
			ps.textAppends[turnID] = append(ps.textAppends[turnID], delta)
		}
		ps.markRunning(turnID)

	case "reasoning_delta":
		turnID := dataString(data, "itemId")
		delta := dataString(data, "delta")
		if turnID == "" {
			return
		}
		commit()
		t := ps.turnByID(turnID)
		if t == nil {
			ps.upsertTurn(TurnProjection{TurnID: turnID, Status: "running"})
			t = ps.turnByID(turnID)
		}
		if t.Assistant == nil {
			t.Assistant = &MessageProjection{ID: turnID, Role: "assistant"}
		}
		// Single reasoning part; set_thinking carries the full accumulated text (monotonic).
		var rpart *ProjectionPart
		for i := range t.Assistant.Parts {
			if t.Assistant.Parts[i].Type == "reasoning" {
				rpart = &t.Assistant.Parts[i]
				break
			}
		}
		if rpart == nil {
			t.Assistant.Parts = append(t.Assistant.Parts, ProjectionPart{Type: "reasoning"})
			rpart = &t.Assistant.Parts[len(t.Assistant.Parts)-1]
		}
		rpart.Text += delta
		ps.thinking[turnID] = rpart.Text
		ps.markRunning(turnID)

	case "tool_started", "tool_finished":
		callID := dataString(data, "itemId")
		if callID == "" {
			return // cannot attribute/update without a stable tool id
		}
		activeTurnID := ps.projection.Execution.ActiveTurnID
		if activeTurnID == "" {
			// Infer: last non-settled turn, else last turn — tools often arrive after
			// content without a fresh turn_started in the reduce window.
			activeTurnID = ps.latestRunningTurnID()
		}
		if activeTurnID == "" {
			return // no active turn to attach the tool to
		}
		t := ps.turnByID(activeTurnID)
		if t == nil {
			return
		}
		if msg.Event == "tool_finished" {
			hasMatchingTool := false
			if t.Assistant != nil {
				for index := range t.Assistant.Parts {
					if t.Assistant.Parts[index].Type == "tool" &&
						t.Assistant.Parts[index].ItemID == callID {
						hasMatchingTool = true
						break
					}
				}
			}
			// Canonical rich history ignores orphan tool results. Materializing an
			// output-only step would create a tool that never existed in the transcript UI.
			if !hasMatchingTool {
				return
			}
		}
		commit()
		if t.Assistant == nil {
			t.Assistant = &MessageProjection{ID: activeTurnID, Role: "assistant"}
		}
		ps.markRunning(activeTurnID)
		part := ProjectionPart{Type: "tool", ItemID: callID}
		if name := dataString(data, "toolName"); name != "" {
			part.ToolName = name
		}
		if v, ok := data["toolInput"]; ok {
			part.ToolInput = v
		}
		if v, ok := data["toolResult"]; ok {
			part.ToolResult = v
		}
		if status := dataString(data, "toolStatus"); status != "" {
			part.ToolStatus = status
		} else if msg.Event == "tool_started" {
			part.ToolStatus = "running"
		} else {
			part.ToolStatus = "completed"
		}
		// Upsert the tool part within the assistant message (by itemId).
		found := false
		for i := range t.Assistant.Parts {
			if t.Assistant.Parts[i].Type == "tool" && t.Assistant.Parts[i].ItemID == callID {
				mergeToolPart(&t.Assistant.Parts[i], part)
				found = true
				break
			}
		}
		if !found {
			t.Assistant.Parts = append(t.Assistant.Parts, part)
		}
		// Record the latest merged part for the pending upsert_tool op.
		for i := range t.Assistant.Parts {
			if t.Assistant.Parts[i].Type == "tool" && t.Assistant.Parts[i].ItemID == callID {
				ps.tools[callID] = t.Assistant.Parts[i]
				break
			}
		}

	case "turn_completed":
		turnID := dataString(data, "turnId")
		if turnID == "" {
			return
		}
		commit()
		ps.upsertTurn(TurnProjection{TurnID: turnID, Status: "completed", CompletedAt: ps.projection.UpdatedAt})
		if turn := ps.turnByID(turnID); turn != nil {
			classifyProjectionTextPresentation(turn.Assistant, true)
			ps.upsertTurns[turnID] = *turn
		}
		exec := ExecutionView{Phase: "idle"}
		ps.projection.Execution = exec
		ps.execution = &exec
	}
}

// mergeToolPart applies non-zero fields from src onto dst (tool_started then tool_finished).
func mergeToolPart(dst *ProjectionPart, src ProjectionPart) {
	if src.ToolName != "" {
		dst.ToolName = src.ToolName
	}
	if src.ToolInput != nil {
		dst.ToolInput = src.ToolInput
	}
	if src.ToolResult != nil {
		dst.ToolResult = src.ToolResult
	}
	if src.ToolStatus != "" {
		dst.ToolStatus = src.ToolStatus
	}
}

// Snapshot returns a deep copy of the session projection (for get_session_projection pull).
// The copy is independent of later reduce activity.
func (r *ProjectionReducer) Snapshot(backendID, sessionID string) (SessionProjection, bool) {
	if r == nil {
		return SessionProjection{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil {
		return SessionProjection{}, false
	}
	return cloneSessionProjection(ps.projection), true
}

// Restore installs a committed checkpoint snapshot without producing pending patch state.
// Input event sequencing is deliberately reset: projection.SyncRev belongs to the projection
// domain and must not be reused as an EventPublisher per-session sequence after restart.
func (r *ProjectionReducer) Restore(backendID, sessionID string, projection SessionProjection) {
	if r == nil || backendID == "" || sessionID == "" {
		return
	}
	projection = cloneSessionProjection(projection)
	projection.SessionID = sessionID
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[projectionSessionKey(backendID, sessionID)] = &projectionSession{
		projection:     projection,
		lastAppliedRev: 0,
		lastFlushedRev: projection.SyncRev,
		textAppends:    make(map[string][]string),
		thinking:       make(map[string]string),
		tools:          make(map[string]ProjectionPart),
		upsertTurns:    make(map[string]TurnProjection),
	}
}

// FlushPatch builds and clears the pending delta accumulated since the last flush, returning a
// projection_patch {baseRev: lastFlushedRev, syncRev: currentSyncRev, ...}. Returns ok=false
// when there is nothing pending (no session or no deltas since last flush). Used by the
// coalesce ticker (WP3) and the emission path (WP5).
func (r *ProjectionReducer) FlushPatch(backendID, sessionID string) (ProjectionPatch, bool) {
	if r == nil {
		return ProjectionPatch{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil {
		return ProjectionPatch{}, false
	}
	headRev := ps.projection.SyncRev
	if headRev == ps.lastFlushedRev && len(ps.textAppends) == 0 && len(ps.thinking) == 0 &&
		len(ps.tools) == 0 && len(ps.upsertTurns) == 0 && ps.execution == nil {
		return ProjectionPatch{}, false
	}
	patch := ProjectionPatch{BaseRev: ps.lastFlushedRev, SyncRev: headRev}
	if ps.execution != nil {
		e := *ps.execution
		patch.Execution = &e
	}
	for _, t := range ps.upsertTurns {
		patch.UpsertTurns = append(patch.UpsertTurns, cloneTurn(t))
	}
	for msgID, chunks := range ps.textAppends {
		combined := ""
		for _, c := range chunks {
			combined += c
		}
		patch.PartOps = append(patch.PartOps, PartOp{TurnID: msgID, MessageID: msgID, Op: "append_text", Text: combined})
	}
	for msgID, text := range ps.thinking {
		patch.PartOps = append(patch.PartOps, PartOp{TurnID: msgID, MessageID: msgID, Op: "set_thinking", Text: text})
	}
	for _, p := range ps.tools {
		tool := p
		turnID := ps.projection.Execution.ActiveTurnID
		patch.PartOps = append(patch.PartOps, PartOp{TurnID: turnID, MessageID: turnID, Op: "upsert_tool", Part: &tool})
	}
	// Clear pending; next patch will be delta from this head.
	ps.textAppends = make(map[string][]string)
	ps.thinking = make(map[string]string)
	ps.tools = make(map[string]ProjectionPart)
	ps.upsertTurns = make(map[string]TurnProjection)
	ps.execution = nil
	ps.lastFlushedRev = headRev
	return patch, true
}

// LastAppliedRev exposes the current head syncRev (for diagnostics / RPC headRev).
func (r *ProjectionReducer) LastAppliedRev(backendID, sessionID string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil {
		return 0
	}
	return ps.projection.SyncRev
}

// TurnCount returns the number of turns currently held for the session (lightweight, no deep
// copy). Used by the segmented cold-hydrate path (design §10.5.6 scheme A) to detect when the
// reducer has crossed from empty into a non-empty partial that may be served to a cold pull —
// the boundary that separates an honest non-empty partial from the forbidden empty head-0 shell.
func (r *ProjectionReducer) TurnCount(backendID, sessionID string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil {
		return 0
	}
	return len(ps.projection.Turns)
}

// HasContentTurn reports whether the reducer holds at least one turn with real user or assistant
// content (a non-empty message). This is the precise non-empty-partial boundary for segmented
// cold-hydrate (design §10.5.6 scheme A): a bare task_started shell with no message content is
// not yet a partial worth serving; once user/assistant text lands the partial is honest.
func (r *ProjectionReducer) HasContentTurn(backendID, sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil {
		return false
	}
	for i := range ps.projection.Turns {
		t := &ps.projection.Turns[i]
		if t.User != nil && len(t.User.Parts) > 0 {
			return true
		}
		if t.Assistant != nil && len(t.Assistant.Parts) > 0 {
			return true
		}
	}
	return false
}

// --- deep-copy helpers (Snapshot must be independent of later reduce activity) ---

func cloneSessionProjection(s SessionProjection) SessionProjection {
	out := s
	if len(s.Turns) > 0 {
		out.Turns = make([]TurnProjection, len(s.Turns))
		for i := range s.Turns {
			out.Turns[i] = cloneTurn(s.Turns[i])
		}
	}
	return out
}

func cloneTurn(t TurnProjection) TurnProjection {
	out := t
	if t.User != nil {
		u := *t.User
		if len(t.User.Parts) > 0 {
			u.Parts = make([]ProjectionPart, len(t.User.Parts))
			for i := range t.User.Parts {
				u.Parts[i] = cloneProjectionPart(t.User.Parts[i])
			}
		}
		out.User = &u
	}
	if t.Assistant != nil {
		a := *t.Assistant
		if len(t.Assistant.Parts) > 0 {
			a.Parts = make([]ProjectionPart, len(t.Assistant.Parts))
			for i := range t.Assistant.Parts {
				a.Parts[i] = cloneProjectionPart(t.Assistant.Parts[i])
			}
		}
		out.Assistant = &a
	}
	return out
}

func cloneProjectionPart(part ProjectionPart) ProjectionPart {
	out := part
	out.ToolInput = cloneProjectionJSONValue(part.ToolInput)
	out.ToolResult = cloneProjectionJSONValue(part.ToolResult)
	out.Matches = cloneProjectionJSONValue(part.Matches)
	return out
}

func cloneProjectionJSONValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			out[key] = cloneProjectionJSONValue(item)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i := range typed {
			out[i] = cloneProjectionJSONValue(typed[i])
		}
		return out
	default:
		return value
	}
}
