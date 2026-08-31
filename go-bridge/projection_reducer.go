package gobridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
// identity for content deltas (DeltaBatcher.emit strips Data to {"delta":text}) still lacks
// itemId and is SKIPPED; lifecycle turn_started/turn_completed now carry TurnID from the driver
// when source-proven (Phase 3 turnId plumbing for execution.phase).
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
	// largePatchObserved is a monotonic hint for the current active turn. Once a
	// whole-turn upsert crossed the no-observer threshold, subsequent tool/input
	// events can skip another full-turn walk until a new turn starts.
	largePatchObserved bool
	// publishedTurnShells records turn IDs whose current authoritative shell has
	// already been published in a snapshot or an upsertTurns patch. Tool and
	// user-input events can then use their compact PartOps on subsequent events;
	// replaying the complete growing turn for every tool transition made an active
	// stream quadratic on the wire.
	publishedTurnShells map[string]struct{}

	// pending deltas accumulated since lastFlushedRev; cleared by FlushPatch.
	textAppends map[string][]string         // assistant messageId -> delta chunks (append_text)
	thinking    map[string]string           // assistant messageId -> full accumulated reasoning (set_thinking)
	tools       map[string]ProjectionPart   // tool callId -> latest tool part (upsert_tool)
	upsertTurns map[string]TurnProjection   // turnId -> latest whole-turn snapshot (upsertTurns)
	userInputs  map[string]userInputPending // interactionId -> latest user_input part + owning turn (upsert_user_input)
	execution   *ExecutionView              // pending execution change
}

// userInputPending captures a pending upsert_user_input PartOp: the owning assistant turn/message
// and the latest user_input part. Keyed by interactionId so repeated requested/resolved events for
// the same interaction coalesce into one in-place upsert (design §6.1: no second "answered" card).
type userInputPending struct {
	turnID string
	part   ProjectionPart
}

func cloneProjectionSessionState(source *projectionSession) *projectionSession {
	if source == nil {
		return nil
	}
	cloned := &projectionSession{
		projection:          cloneSessionProjection(source.projection),
		lastAppliedRev:      source.lastAppliedRev,
		lastFlushedRev:      source.lastFlushedRev,
		largePatchObserved:  source.largePatchObserved,
		publishedTurnShells: make(map[string]struct{}, len(source.publishedTurnShells)),
		textAppends:         make(map[string][]string, len(source.textAppends)),
		thinking:            make(map[string]string, len(source.thinking)),
		tools:               make(map[string]ProjectionPart, len(source.tools)),
		upsertTurns:         make(map[string]TurnProjection, len(source.upsertTurns)),
		userInputs:          make(map[string]userInputPending, len(source.userInputs)),
	}
	for turnID := range source.publishedTurnShells {
		cloned.publishedTurnShells[turnID] = struct{}{}
	}
	for key, chunks := range source.textAppends {
		cloned.textAppends[key] = append([]string(nil), chunks...)
	}
	for key, text := range source.thinking {
		cloned.thinking[key] = text
	}
	for key, part := range source.tools {
		cloned.tools[key] = cloneProjectionPart(part)
	}
	for key, turn := range source.upsertTurns {
		cloned.upsertTurns[key] = cloneTurn(turn)
	}
	for key, pending := range source.userInputs {
		pending.part = cloneProjectionPart(pending.part)
		cloned.userInputs[key] = pending
	}
	if source.execution != nil {
		execution := *source.execution
		cloned.execution = &execution
	}
	return cloned
}

func (r *ProjectionReducer) cloneSessionReducer(backendID, sessionID string) *ProjectionReducer {
	cloned := NewProjectionReducer()
	cloned.now = r.now
	r.mu.Lock()
	defer r.mu.Unlock()
	if session := r.sessions[projectionSessionKey(backendID, sessionID)]; session != nil {
		cloned.sessions[projectionSessionKey(backendID, sessionID)] = cloneProjectionSessionState(session)
	}
	return cloned
}

func (r *ProjectionReducer) swapSessionFrom(
	backendID, sessionID string,
	source *ProjectionReducer,
) {
	key := projectionSessionKey(backendID, sessionID)
	source.mu.Lock()
	next := cloneProjectionSessionState(source.sessions[key])
	source.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if next == nil {
		delete(r.sessions, key)
		return
	}
	r.sessions[key] = next
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

// dataStringSlice reads a JSON string-array event field ([]interface{} of strings).
func dataStringSlice(m map[string]interface{}, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dataInt64(m map[string]interface{}, key string) int64 {
	if m == nil {
		return 0
	}
	switch value := m[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		n, _ := value.Int64()
		return n
	default:
		return 0
	}
}

func dataBool(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
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
		// Merge: keep existing user/assistant/system if the incoming snapshot omits them.
		if turn.Status != "" {
			t.Status = turn.Status
		}
		if turn.StartedAt != 0 {
			t.StartedAt = turn.StartedAt
		}
		if turn.CompletedAt != 0 {
			t.CompletedAt = turn.CompletedAt
		}
		if turn.DurationMs != 0 {
			t.DurationMs = turn.DurationMs
		}
		if turn.User != nil {
			t.User = turn.User
		}
		if turn.Assistant != nil {
			t.Assistant = turn.Assistant
		}
		if turn.System != nil {
			t.System = turn.System
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

// upsertTurnPersistOnly merges into projection.Turns without staging a flush delta. Used by
// turn_started so skeleton frames (running turn without content) never leave the reducer —
// publishing one breaks client local-send optimistic paint fences (owner 2026-08-04 真机).
func (ps *projectionSession) upsertTurnPersistOnly(turn TurnProjection) {
	if turn.TurnID == "" {
		return
	}
	if t := ps.turnByID(turn.TurnID); t != nil {
		if turn.Status != "" {
			t.Status = turn.Status
		}
		if turn.StartedAt != 0 {
			t.StartedAt = turn.StartedAt
		}
		if turn.CompletedAt != 0 {
			t.CompletedAt = turn.CompletedAt
		}
		if turn.DurationMs != 0 {
			t.DurationMs = turn.DurationMs
		}
		if turn.User != nil {
			t.User = turn.User
		}
		if turn.Assistant != nil {
			t.Assistant = turn.Assistant
		}
		if turn.System != nil {
			t.System = turn.System
		}
		return
	}
	ps.projection.Turns = append(ps.projection.Turns, turn)
}

// setActiveTurnPersistOnly arms Execution.ActiveTurnID without staging ps.execution. Content
// events call markRunning (full path) once they arrive; turn_started alone must not flush a
// running-execution delta for a content-less turn (see upsertTurnPersistOnly note).
func (ps *projectionSession) setActiveTurnPersistOnly(turnID string) {
	if turnID == "" {
		return
	}
	ps.projection.Execution = ExecutionView{Phase: "running", ActiveTurnID: turnID}
}

// markRunning sets session execution to running for turnID (design §7.4). Content
// events must re-arm after a prior turn_completed left phase=idle.
//
// Codex rollouts can emit a new task_started without task_complete for the previous
// turn (owner residual 2026-07-27: idle sessions still had older turns status=running,
// so iOS turnStillLive kept composer 执行中). A session has at most one live turn: when
// arming turnID, settle any other non-settled turns as completed.
func (ps *projectionSession) markRunning(turnID string) {
	if turnID == "" {
		return
	}
	exec := ExecutionView{Phase: "running", ActiveTurnID: turnID}
	ps.projection.Execution = exec
	ps.execution = &exec
	// Keep turn status running unless already settled (do not un-complete). Do not
	// stage an already-running turn on every content delta: text/reasoning events
	// carry their mutation in PartOps, so repeatedly copying the growing assistant
	// body into upsertTurns makes long streams quadratic on the wire.
	if t := ps.turnByID(turnID); t != nil && t.Status != "completed" && t.Status != "aborted" && t.Status != "error" {
		statusChanged := t.Status != "running"
		t.Status = "running"
		if statusChanged && ps.upsertTurns != nil {
			ps.upsertTurns[turnID] = *t
		}
	}
	ps.settleOtherOpenTurns(turnID, ps.projection.UpdatedAt)
}

// settleOtherOpenTurns marks every non-settled turn except activeTurnID as completed.
// Used when a newer turn becomes live or the session returns to idle, so historical
// supersession cannot leave zombie running turns in the projection SoT.
func (ps *projectionSession) settleOtherOpenTurns(activeTurnID string, completedAt int64) {
	for i := range ps.projection.Turns {
		t := &ps.projection.Turns[i]
		if t.TurnID == "" || t.TurnID == activeTurnID {
			continue
		}
		if t.Status == "completed" || t.Status == "aborted" || t.Status == "error" {
			continue
		}
		t.Status = "completed"
		if completedAt != 0 {
			t.CompletedAt = completedAt
		}
		classifyProjectionTextPresentation(t.Assistant, true)
		if ps.upsertTurns != nil {
			ps.upsertTurns[t.TurnID] = *t
		}
	}
}

// stageTurnForFlush copies the current turn snapshot into the flush buffer so the next
// projection_patch carries UpsertTurns. Persist-only turn_started never stages a shell
// (optimistic-paint fence); permission_request / question_asked / FlushPatch must do it
// or iOS applyingPartOps skips upsert_tool / upsert_user_input (no matching turnId).
func (ps *projectionSession) stageTurnForFlush(turnID string) {
	if ps == nil || turnID == "" || ps.upsertTurns == nil {
		return
	}
	if _, published := ps.publishedTurnShells[turnID]; published {
		return
	}
	if t := ps.turnByID(turnID); t != nil {
		ps.upsertTurns[turnID] = *t
	}
}

// stageOwningTurnsForPendingParts ensures every pending tool / user_input PartOp has a
// turn shell in upsertTurns. Codec reset after a Mac-side question answer emits a new
// persist-only turn_started; without this, FlushPatch ships upsert_tool alone and iOS
// drops it (owner 2026-08-16: Mac 覆盖后出现权限框，iPhone 没有).
func (ps *projectionSession) stageOwningTurnsForPendingParts() {
	if ps == nil {
		return
	}
	if len(ps.tools) > 0 {
		ps.stageTurnForFlush(ps.projection.Execution.ActiveTurnID)
	}
	for _, pending := range ps.userInputs {
		ps.stageTurnForFlush(pending.turnID)
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

// upsertUserInputPart inserts or replaces (in place, by interactionId) a user_input part in the
// assistant message. Design §6.1: the same interaction upserts in place — never appends a second
// "answered" card. Returns the index of the part.
func upsertUserInputPart(msg *MessageProjection, part ProjectionPart) int {
	if idx := findUserInputPart(msg, part.UserInputInteractionID); idx >= 0 {
		msg.Parts[idx] = part
		return idx
	}
	msg.Parts = append(msg.Parts, part)
	return len(msg.Parts) - 1
}

// findUserInputPart returns the index of the user_input part with the given interactionId, or -1.
func findUserInputPart(msg *MessageProjection, interactionID string) int {
	if msg == nil || interactionID == "" {
		return -1
	}
	for i := range msg.Parts {
		if msg.Parts[i].Type == "user_input" && msg.Parts[i].UserInputInteractionID == interactionID {
			return i
		}
	}
	return -1
}

func questionOptionsToUserInputOptions(raw interface{}) []map[string]interface{} {
	list, ok := raw.([]interface{})
	if !ok {
		if typed, ok := raw.([]map[string]interface{}); ok {
			out := make([]map[string]interface{}, 0, len(typed))
			for i, m := range typed {
				id := dataString(m, "id")
				label := dataString(m, "label")
				if id == "" {
					id = label
				}
				if id == "" {
					id = "opt-" + strconv.Itoa(i)
				}
				out = append(out, map[string]interface{}{
					"id":          id,
					"label":       label,
					"description": dataString(m, "description"),
				})
			}
			return out
		}
		return nil
	}
	out := make([]map[string]interface{}, 0, len(list))
	for i, item := range list {
		m, _ := item.(map[string]interface{})
		if m == nil {
			continue
		}
		id := dataString(m, "id")
		label := dataString(m, "label")
		if id == "" {
			id = label
		}
		if id == "" {
			id = "opt-" + strconv.Itoa(i)
		}
		out = append(out, map[string]interface{}{
			"id":          id,
			"label":       label,
			"description": dataString(m, "description"),
		})
	}
	return out
}

func hasPermissionTool(msg *MessageProjection, itemID string) bool {
	if msg == nil || itemID == "" {
		return false
	}
	for i := range msg.Parts {
		if msg.Parts[i].Type == "tool" && msg.Parts[i].ItemID == itemID {
			return true
		}
	}
	return false
}

// hasPendingUserInput reports whether the turn's assistant message has any pending user_input part.
func (ps *projectionSession) hasPendingUserInput(turnID string) bool {
	t := ps.turnByID(turnID)
	if t == nil || t.Assistant == nil {
		return false
	}
	for i := range t.Assistant.Parts {
		if t.Assistant.Parts[i].Type == "user_input" && t.Assistant.Parts[i].UserInputStatus == "pending" {
			return true
		}
		if t.Assistant.Parts[i].Type == "tool" && t.Assistant.Parts[i].RequiresPermissionConfirmation &&
			(t.Assistant.Parts[i].ToolStatus == "" || t.Assistant.Parts[i].ToolStatus == "pending") {
			return true
		}
	}
	return false
}

// applyUserInputExecution derives execution.phase from user_input state (design §6.2):
//   - active turn has a pending user_input → requires_action
//   - no pending user_input and the turn is still running/pending → running
//     (turn_completed owns the idle transition; we never preemptively idle an active turn)
//   - turn already settled (completed/aborted/error) → leave phase untouched (idle stays idle)
func (ps *projectionSession) applyUserInputExecution(turnID string) {
	if turnID == "" {
		return
	}
	if ps.hasPendingUserInput(turnID) {
		exec := ExecutionView{Phase: "requires_action", ActiveTurnID: turnID}
		ps.projection.Execution = exec
		ps.execution = &exec
		return
	}
	if t := ps.turnByID(turnID); t != nil && (t.Status == "running" || t.Status == "pending") {
		ps.markRunning(turnID)
	}
}

// ensureAssistantTextPart returns the assistant message's trailing text part, creating one if
// the last part is not text. Used by append_text accumulation.
// trailingTextPartForItem returns the trailing text part only when it belongs to
// the same canonical item (either side may lack an id — legacy live frames carry
// the turn id as itemId, cold Summary frames carry real item ids). A different
// canonical itemId forces a new part (T2.1 item-boundary rule).
func (m *MessageProjection) trailingTextPartForItem(itemID string) *ProjectionPart {
	if len(m.Parts) == 0 {
		return nil
	}
	trailing := &m.Parts[len(m.Parts)-1]
	if trailing.Type != "text" {
		return nil
	}
	if itemID != "" && trailing.ItemID != "" && trailing.ItemID != itemID {
		return nil
	}
	return trailing
}

func (m *MessageProjection) ensureTrailingTextPart() *ProjectionPart {
	if len(m.Parts) == 0 || m.Parts[len(m.Parts)-1].Type != "text" {
		m.Parts = append(m.Parts, ProjectionPart{Type: "text"})
	}
	return &m.Parts[len(m.Parts)-1]
}

func classifyProjectionTextPresentation(message *MessageProjection, completed bool) {
	if message == nil {
		return
	}
	lastText := -1
	hasExplicitPresentation := false
	for index := range message.Parts {
		if message.Parts[index].Type == "text" && strings.TrimSpace(message.Parts[index].Text) != "" {
			lastText = index
			if message.Parts[index].presentationExplicit {
				hasExplicitPresentation = true
			}
		}
	}
	if hasExplicitPresentation {
		// Official history phase is authoritative. Preserve it and only give an
		// unannotated text part the neutral progress classification; never promote
		// an official commentary item to final by array position.
		for index := range message.Parts {
			if message.Parts[index].Type == "text" && message.Parts[index].Presentation == "" {
				message.Parts[index].Presentation = "progress"
			}
		}
		return
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
			publishedTurnShells: make(map[string]struct{}),
			textAppends:         make(map[string][]string),
			thinking:            make(map[string]string),
			tools:               make(map[string]ProjectionPart),
			upsertTurns:         make(map[string]TurnProjection),
			userInputs:          make(map[string]userInputPending),
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
			return // no source-proven turnId; skip identityless lifecycle frames
		}
		if activeTurnID := ps.projection.Execution.ActiveTurnID; activeTurnID != "" && activeTurnID != turnID {
			ps.largePatchObserved = false
		}
		// No commit / no flush-buffer writes on turn_started: at this point the turn has no
		// user/assistant content yet. Publishing a skeleton projection (rev advances while the
		// user part is not in) breaks client local-send optimistic paint fences — a lagging
		// patch with rev > baseline but no user row blanks the just-sent bubble (owner
		// 2026-08-04 真机: 发送后 "问题1" 消失几秒再出现). Content events (user_message /
		// text_delta / reasoning_delta / tool_started) commit the complete frame when they arrive.
		// Persist-only updates keep Execution.ActiveTurnID armed (tool_started / text_delta attach
		// point in content-only turns that never carry a user_message) and preserve StartedAt,
		// without staging a flush delta that FlushPatch would publish.
		ps.upsertTurnPersistOnly(TurnProjection{TurnID: turnID, Status: "running", StartedAt: ps.projection.UpdatedAt})
		ps.setActiveTurnPersistOnly(turnID)

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
			// Canonical official item id (T2.1): the Summary user slot dedups against
			// detail items by this part-level id, mirroring the upstream mapper.
			User: &MessageProjection{ID: itemID, Role: "user", Parts: []ProjectionPart{
				{Type: "text", Text: text, ItemID: itemID},
			}},
		})
		// Design §7.4: any in-flight content must keep execution.running. After cold
		// hydrate the last completed turn leaves phase=idle; a new user_message without
		// a re-emitted turn_started must still arm the UI (owner 2026-07-25: reopen app
		// → prompt+thinking then sticky 完成态 because phase stayed idle).
		ps.markRunning(turnID)

	case "system_message":
		turnID := dataString(data, "turnId")
		itemID := dataString(data, "itemId")
		text := dataString(data, "text")
		if turnID == "" {
			turnID = itemID
		}
		if itemID == "" {
			itemID = turnID
		}
		if turnID == "" || strings.TrimSpace(text) == "" {
			return
		}
		commit()
		timestamp := dataInt64(data, "timestampMillis")
		ps.upsertTurn(TurnProjection{
			TurnID:      turnID,
			Status:      "completed",
			StartedAt:   timestamp,
			CompletedAt: timestamp,
			System: &MessageProjection{
				ID:   itemID,
				Role: "system",
				Parts: []ProjectionPart{{
					Type:         "text",
					Text:         text,
					Presentation: "final",
				}},
			},
		})

	case "text_delta":
		// Turn attribution: legacy/live frames carry the turn id AS itemId (no turnId);
		// codex-web turn-scoped cold frames carry BOTH (turnId=official turn id,
		// itemId=official item id) — explicit turnId wins so official turns don't
		// fragment per item (PERF-S0B fixture generation, real catalog sample: 2
		// official turns fragmented into 5 without this).
		turnID := dataString(data, "turnId")
		if turnID == "" {
			turnID = dataString(data, "itemId")
		}
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
		createdAssistant := t.Assistant == nil
		if t.Assistant == nil {
			t.Assistant = &MessageProjection{ID: turnID, Role: "assistant"}
		}
		itemID := dataString(data, "itemId")
		presentation := dataString(data, "presentation")
		if presentation != "progress" && presentation != "final" {
			presentation = ""
		}
		var tp *ProjectionPart
		newPart, _ := data["newPart"].(bool)
		if newPart {
			t.Assistant.Parts = append(t.Assistant.Parts, ProjectionPart{Type: "text", Presentation: presentation, ItemID: itemID})
			tp = &t.Assistant.Parts[len(t.Assistant.Parts)-1]
		} else if trailing := t.Assistant.trailingTextPartForItem(itemID); trailing != nil {
			tp = trailing
			if tp.ItemID == "" {
				tp.ItemID = itemID
			}
		} else if len(t.Assistant.Parts) > 0 && t.Assistant.Parts[len(t.Assistant.Parts)-1].Type == "text" {
			// Canonical official item boundary (T2.1): the trailing part belongs to
			// a DIFFERENT item — never merge across the boundary. Whole-turn upsert
			// publishes the split; append_text PartOps cannot express it.
			t.Assistant.Parts = append(t.Assistant.Parts, ProjectionPart{Type: "text", Presentation: presentation, ItemID: itemID})
			tp = &t.Assistant.Parts[len(t.Assistant.Parts)-1]
			newPart = true
		} else {
			// First text part: keep the legacy append_text path, now item-stamped.
			tp = t.Assistant.ensureTrailingTextPart()
			tp.ItemID = itemID
		}
		if presentation != "" {
			tp.Presentation = presentation
			tp.presentationExplicit = true
		}
		tp.Text += delta
		if newPart {
			ps.upsertTurns[turnID] = *t
		} else {
			ps.textAppends[turnID] = append(ps.textAppends[turnID], delta)
			// A persist-only turn_started intentionally did not publish its shell.
			// The first content-bearing frame must still mount that shell before the
			// append_text PartOp; later deltas remain PartOp-only.
			if createdAssistant {
				ps.upsertTurns[turnID] = *t
			}
		}
		ps.markRunning(turnID)

	case "reasoning_delta":
		turnID := dataString(data, "turnId")
		if turnID == "" {
			turnID = dataString(data, "itemId")
		}
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
		createdAssistant := t.Assistant == nil
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
		rItemID := dataString(data, "itemId")
		if rpart == nil {
			t.Assistant.Parts = append(t.Assistant.Parts, ProjectionPart{Type: "reasoning", ItemID: rItemID})
			rpart = &t.Assistant.Parts[len(t.Assistant.Parts)-1]
		} else if rpart.ItemID == "" {
			rpart.ItemID = rItemID
		}
		rpart.Text += delta
		ps.thinking[turnID] = rpart.Text
		if createdAssistant {
			ps.upsertTurns[turnID] = *t
		}
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
		// P0-1: structured tool matches (Glob/Grep 文件列表). 生产端 = Claude session driver
		// (session.go EventToolResult.ToolMatches → events.go 写 payload["matches"])。reducer 必须
		// 填 part.Matches，否则投影 writer 恒空、消费端喂不到（audit2 producer-first 命门）。
		// 读 map key data["matches"]（同 toolInput/toolResult 模式），不假设 ev 结构体——reducer 拿到的是
		// EventMessage.Data，不是 core.Event（audit3 双策略核验）。
		if v, ok := data["matches"]; ok {
			part.Matches = v
		}
		// Guardrail 12 / ChatGPT-style activity rows: path-bearing title + structured
		// fileChanges must survive into Snapshot parts (hydrate and live both emit them
		// on event Data; without this merge they die at the reducer).
		if title := dataString(data, "title"); title != "" {
			part.Title = title
		}
		if v, ok := data["fileChanges"]; ok {
			part.FileChanges = v
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

	case "subagent_part":
		// B4 child-stream: upsert a fully-built subagent ProjectionPart (Type=="subagent")
		// into the mainstream turn's assistant message, keyed by AgentID. The whole part
		// (including recursive SubagentBlocks) is constructed upstream by the sidechain
		// source-read pre-pass (claude_sidechain_subagents.go) during cold hydrate; the
		// reducer only anchors + upserts — it does not build the tree (guardrail §3/§4: no
		// consumer referee). Sync-only hydrate (ApplyHydrateEvent → tx.reducer); never a
		// live event. turnId is the depth-1 mainstream anchor (resolved via SpawnToolUseID
		// ↔ mainstream Agent tool_use id before emit).
		turnID := dataString(data, "turnId")
		agentID := dataString(data, "agentId")
		if turnID == "" || agentID == "" {
			return
		}
		t := ps.turnByID(turnID)
		if t == nil {
			return // mainstream owning turn must already exist (hydrate runs after mainstream scan)
		}
		commit()
		if t.Assistant == nil {
			t.Assistant = &MessageProjection{ID: turnID, Role: "assistant"}
		}
		blocks, _ := data["subagentBlocks"].([]ProjectionPart)
		part := ProjectionPart{
			Type:               "subagent",
			AgentID:            agentID,
			ParentAgentID:      dataString(data, "parentAgentId"),
			SpawnToolUseID:     dataString(data, "spawnToolUseId"),
			SpawnDepth:         int(dataInt64(data, "spawnDepth")),
			SubagentType:       dataString(data, "subagentType"),
			SubagentStatus:     dataString(data, "subagentStatus"),
			SubagentBlocks:     blocks,
			SubagentError:      dataString(data, "subagentError"),
			SubagentDiagnostic: dataString(data, "subagentDiagnostic"),
		}
		// Upsert by AgentID within the assistant message (mirrors the tool upsert pattern).
		found := false
		for i := range t.Assistant.Parts {
			if t.Assistant.Parts[i].Type == "subagent" && t.Assistant.Parts[i].AgentID == agentID {
				t.Assistant.Parts[i] = part
				found = true
				break
			}
		}
		if !found {
			t.Assistant.Parts = append(t.Assistant.Parts, part)
		}

	case "permission_request":
		// dsh-web mux approval/requested. Not reduced before 2026-08-16, so SSV2
		// clients never saw a permission card (projection overwrite wiped the live
		// toolStarted). itemId = requestId (approvalId); attach to the active turn.
		callID := dataString(data, "requestId")
		if callID == "" {
			callID = dataString(data, "itemId")
		}
		if callID == "" {
			return
		}
		activeTurnID := ps.projection.Execution.ActiveTurnID
		if activeTurnID == "" {
			activeTurnID = ps.latestRunningTurnID()
		}
		if activeTurnID == "" {
			return
		}
		commit()
		t := ps.turnByID(activeTurnID)
		if t == nil {
			ps.upsertTurn(TurnProjection{TurnID: activeTurnID, Status: "running"})
			t = ps.turnByID(activeTurnID)
		}
		if t == nil {
			return
		}
		if t.Assistant == nil {
			t.Assistant = &MessageProjection{ID: activeTurnID, Role: "assistant"}
		}
		part := ProjectionPart{
			Type:                           "tool",
			ItemID:                         callID,
			ToolStatus:                     "pending",
			RequiresPermissionConfirmation: true,
		}
		if name := dataString(data, "toolName"); name != "" {
			part.ToolName = name
		}
		if reason := dataString(data, "reason"); reason != "" {
			part.Title = reason
		}
		if v, ok := data["toolInput"]; ok {
			// 空串 toolInput（fileChange 审批官方无 command 字段，wire 恒带空键）
			// 必须跳过：mergeToolPart 按 nil 判保留旧值，"" 非 nil 会把既有 part
			// 已投影的工具内容（tool_started 的命令）清掉，SSV2 审批卡只剩按钮
			// 无文案（2026-08-26 真机：GPT fileChange 审批）。也不回退
			// toolInputRaw——那是审批请求参数，不是工具内容。
			if s, isStr := v.(string); !isStr || s != "" {
				part.ToolInput = v
			}
		} else if v, ok := data["toolInputRaw"]; ok {
			part.ToolInput = v
		}
		// Official permission payload (opencode-web v1.18): additive category/pattern
		// fields ride the projected part so SSV2 clients render the official card.
		if kind := dataString(data, "permissionKind"); kind != "" {
			part.PermissionKind = kind
		}
		if patterns := dataStringSlice(data, "patterns"); len(patterns) > 0 {
			part.PermissionPatterns = patterns
		}
		if actions := dataStringSlice(data, "permissionActions"); len(actions) > 0 {
			part.PermissionActions = actions
		}
		found := false
		for i := range t.Assistant.Parts {
			if t.Assistant.Parts[i].Type == "tool" && t.Assistant.Parts[i].ItemID == callID {
				mergeToolPart(&t.Assistant.Parts[i], part)
				t.Assistant.Parts[i].RequiresPermissionConfirmation = true
				t.Assistant.Parts[i].ToolStatus = "pending"
				found = true
				break
			}
		}
		if !found {
			t.Assistant.Parts = append(t.Assistant.Parts, part)
		}
		if ps.tools != nil {
			for i := range t.Assistant.Parts {
				if t.Assistant.Parts[i].Type == "tool" && t.Assistant.Parts[i].ItemID == callID {
					ps.tools[callID] = t.Assistant.Parts[i]
					break
				}
			}
		}
		ps.stageTurnForFlush(activeTurnID)
		ps.applyUserInputExecution(activeTurnID)

	case "permission_resolved":
		// Close the pending permission tool in place. itemId == requestId (approvalId).
		// First-writer-wins: iOS resolve_permission and host approval/resolved are
		// idempotent — a second resolve on an already-cleared part is a no-op.
		callID := dataString(data, "requestId")
		if callID == "" {
			callID = dataString(data, "itemId")
		}
		if callID == "" {
			return
		}
		behavior := strings.ToLower(dataString(data, "behavior"))
		if behavior == "" {
			behavior = strings.ToLower(dataString(data, "outcome"))
		}
		denied := behavior == "deny" || behavior == "rejected" || behavior == "cancelled" || behavior == "unavailable"
		// Search the active turn first, then any turn that still holds this pending tool.
		turnID := ps.projection.Execution.ActiveTurnID
		t := ps.turnByID(turnID)
		if t == nil || t.Assistant == nil || !hasPermissionTool(t.Assistant, callID) {
			t = nil
			for i := range ps.projection.Turns {
				if ps.projection.Turns[i].Assistant != nil && hasPermissionTool(ps.projection.Turns[i].Assistant, callID) {
					t = &ps.projection.Turns[i]
					turnID = ps.projection.Turns[i].TurnID
					break
				}
			}
		}
		if t == nil || t.Assistant == nil {
			return
		}
		commit()
		for i := range t.Assistant.Parts {
			if t.Assistant.Parts[i].Type == "tool" && t.Assistant.Parts[i].ItemID == callID {
				t.Assistant.Parts[i].RequiresPermissionConfirmation = false
				if denied {
					t.Assistant.Parts[i].ToolStatus = "rejected"
				} else if t.Assistant.Parts[i].ToolStatus == "" || t.Assistant.Parts[i].ToolStatus == "pending" {
					t.Assistant.Parts[i].ToolStatus = "running"
				}
				if ps.tools != nil {
					ps.tools[callID] = t.Assistant.Parts[i]
				}
				break
			}
		}
		ps.applyUserInputExecution(turnID)

	case "question_asked":
		// dsh-web mux question/requested. Project as user_input so SSV2 clients
		// get the composer UserInputDock (same card as Claude/Codex structured ask).
		qid := dataString(data, "questionId")
		if qid == "" {
			return
		}
		turnID := ps.projection.Execution.ActiveTurnID
		if turnID == "" {
			turnID = ps.latestRunningTurnID()
		}
		if turnID == "" {
			return
		}
		if existing, ok := ps.userInputs[qid]; ok && existing.turnID != "" {
			turnID = existing.turnID
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
		prompt := dataString(data, "questionText")
		opts := questionOptionsToUserInputOptions(data["options"])
		answerMode := dataString(data, "answerMode")
		if answerMode == "" {
			if dataBool(data, "multiSelect") {
				answerMode = "multiple"
			} else {
				answerMode = "single"
			}
		}
		questions := []map[string]interface{}{
			{
				"id":                 qid,
				"header":             nil,
				"prompt":             prompt,
				"answerMode":         answerMode,
				"options":            opts,
				"allowsCustomAnswer": true,
				"isSecret":           false,
				"required":           true,
			},
		}
		part := ProjectionPart{
			Type:                   "user_input",
			UserInputInteractionID: qid,
			UserInputStatus:        "pending",
			UserInputQuestions:     questions,
			UserInputCanRespond:    true,
			UserInputCanReject:     true,
		}
		upsertUserInputPart(t.Assistant, part)
		ps.userInputs[qid] = userInputPending{turnID: turnID, part: part}
		ps.stageTurnForFlush(turnID)
		ps.applyUserInputExecution(turnID)

	case "question_resolved":
		qid := dataString(data, "questionId")
		if qid == "" {
			return
		}
		status := "answered"
		if result := strings.ToLower(dataString(data, "result")); result == "cancelled" || result == "rejected" {
			status = "rejected"
		}
		turnID := ""
		if existing, ok := ps.userInputs[qid]; ok {
			turnID = existing.turnID
		}
		if turnID == "" {
			turnID = ps.projection.Execution.ActiveTurnID
		}
		t := ps.turnByID(turnID)
		if t == nil || t.Assistant == nil {
			return
		}
		idx := findUserInputPart(t.Assistant, qid)
		if idx < 0 {
			return
		}
		commit()
		t.Assistant.Parts[idx].UserInputStatus = status
		t.Assistant.Parts[idx].UserInputResolutionSource = "backend"
		t.Assistant.Parts[idx].UserInputResolvedAt = ps.projection.UpdatedAt
		ps.userInputs[qid] = userInputPending{turnID: turnID, part: t.Assistant.Parts[idx]}
		ps.applyUserInputExecution(turnID)

	case "user_input_requested":
		// Structured user input requested (design §10.1/§10.2). The adapter emits a proven
		// turnId + interactionId; without both the event is identityless and skipped (no
		// phantom turn, no raw second path). status may be pending (normal) or failed
		// (malformed questions) — both project once via the same upsert.
		//
		// dsh-web Mac-initiated asks may omit turnId (codec reset + unbound observe).
		// Fall back to the persist-only ActiveTurnID so iOS still gets a UserInputDock.
		interactionID := dataString(data, "interactionId")
		if interactionID == "" {
			return
		}
		turnID := dataString(data, "turnId")
		// interactionId is the cross-source identity. Claude live stream attributes the request
		// to its assistant message while transcript hydrate attributes the same tool_use to the
		// enclosing user turn. Once either source has established the interaction, keep that
		// owning turn and update in place instead of creating a phantom second card.
		if existing, ok := ps.userInputs[interactionID]; ok && existing.turnID != "" {
			turnID = existing.turnID
		}
		if turnID == "" && msg.BackendID == "dsh-web" {
			// Mac-initiated dsh-web asks after codec reset may omit turnId.
			// Claude/Codex stay fail-closed (identityless frames stay dropped).
			turnID = ps.projection.Execution.ActiveTurnID
			if turnID == "" {
				turnID = ps.latestRunningTurnID()
			}
		}
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
		part := ProjectionPart{
			Type:                    "user_input",
			UserInputInteractionID:  interactionID,
			UserInputStatus:         dataString(data, "status"),
			UserInputQuestions:      data["questions"],
			UserInputCanRespond:     dataBool(data, "canRespond"),
			UserInputCanReject:      dataBool(data, "canReject"),
			UserInputExpiresAt:      dataInt64(data, "expiresAt"),
			UserInputDiagnosticCode: dataString(data, "diagnosticCode"),
		}
		if part.UserInputStatus == "" {
			part.UserInputStatus = "pending"
		}
		upsertUserInputPart(t.Assistant, part)
		ps.userInputs[interactionID] = userInputPending{turnID: turnID, part: part}
		ps.stageTurnForFlush(turnID)
		ps.applyUserInputExecution(turnID)

	case "user_input_resolved":
		// Resolved in place: update the existing part's status/source/resolvedAt (design §10.2).
		// Projection never stores the answer text. If no matching requested part exists, the
		// resolution is stale/unattributable — do not fabricate one (no second path).
		interactionID := dataString(data, "interactionId")
		if interactionID == "" {
			return
		}
		turnID := dataString(data, "turnId")
		if existing, ok := ps.userInputs[interactionID]; ok && existing.turnID != "" {
			turnID = existing.turnID
		}
		if turnID == "" {
			turnID = ps.projection.Execution.ActiveTurnID
		}
		t := ps.turnByID(turnID)
		if t == nil || t.Assistant == nil {
			return
		}
		idx := findUserInputPart(t.Assistant, interactionID)
		if idx < 0 {
			return
		}
		commit()
		t.Assistant.Parts[idx].UserInputStatus = dataString(data, "status")
		t.Assistant.Parts[idx].UserInputResolutionSource = dataString(data, "source")
		if resolvedAt := dataInt64(data, "resolvedAt"); resolvedAt != 0 {
			t.Assistant.Parts[idx].UserInputResolvedAt = resolvedAt
		} else {
			t.Assistant.Parts[idx].UserInputResolvedAt = ps.projection.UpdatedAt
		}
		ps.userInputs[interactionID] = userInputPending{turnID: turnID, part: t.Assistant.Parts[idx]}
		ps.applyUserInputExecution(turnID)

	case "turn_completed":
		turnID := dataString(data, "turnId")
		if turnID == "" {
			// Live driver frames historically omitted turnId; if a prior turn_started / content
			// event already armed ActiveTurnID, still complete that turn (design §6.4 rule 3).
			turnID = ps.projection.Execution.ActiveTurnID
		}
		if turnID == "" {
			return
		}
		commit()
		completed := TurnProjection{TurnID: turnID, Status: "completed", CompletedAt: ps.projection.UpdatedAt}
		if durationMs := dataInt64(data, "durationMs"); durationMs > 0 {
			// Official Turn.durationMs when the source provides it; absent keeps the
			// timestamp-derived value clients compute as fallback.
			completed.DurationMs = durationMs
		}
		ps.upsertTurn(completed)
		if turn := ps.turnByID(turnID); turn != nil {
			classifyProjectionTextPresentation(turn.Assistant, true)
			ps.upsertTurns[turnID] = *turn
		}
		// Completing the active turn also settles any older zombie running/pending turns
		// left by missing task_complete boundaries in Codex rollouts.
		ps.settleOtherOpenTurns(turnID, ps.projection.UpdatedAt)
		exec := ExecutionView{Phase: "idle"}
		ps.projection.Execution = exec
		ps.execution = &exec

	case "turn_aborted", "turn_error":
		// §5.1 #7: an aborted or failed turn is a terminal state, not a forever-hydrating
		// shell. Without these cases a content-less aborted/crashed turn never satisfied the
		// old hydrate-commit gate (HasContentTurn=false, turnCount!=0 → waited indefinitely),
		// so empty/aborted/archived sessions stuck on "hydrating" forever. The turn keeps
		// whatever user/assistant content already landed (upsertTurn merges non-zero fields);
		// only status + CompletedAt change. The producer side (codex rollout abort/crash →
		// turn_aborted / turn_error events) lands in Phase 2; the reducer contract is defined
		// and unit-tested here so Phase 2 only needs to emit the events.
		turnID := dataString(data, "turnId")
		if turnID == "" {
			// Live driver frames may omit turnId; fall back to the armed active turn, mirroring
			// turn_completed (design §6.4 rule 3).
			turnID = ps.projection.Execution.ActiveTurnID
		}
		if turnID == "" {
			return
		}
		status := "aborted"
		if msg.Event == "turn_error" {
			status = "error"
		}
		commit()
		terminal := TurnProjection{TurnID: turnID, Status: status, CompletedAt: ps.projection.UpdatedAt}
		// turn_error carries the official backend failure text. Preserve it in the
		// projection as a system message so a content-less pre-turn failure remains
		// visible after the optimistic iOS row is reconciled away. This is diagnostic
		// truth, not an assistant reply.
		if msg.Event == "turn_error" {
			if errorMessage := strings.TrimSpace(dataString(data, "message")); errorMessage != "" {
				terminal.System = &MessageProjection{
					ID:   turnID + "-error",
					Role: "system",
					Parts: []ProjectionPart{{
						Type:         "text",
						Text:         errorMessage,
						Presentation: "final",
					}},
				}
			}
		}
		ps.upsertTurn(terminal)
		if turn := ps.turnByID(turnID); turn != nil {
			classifyProjectionTextPresentation(turn.Assistant, true)
			ps.upsertTurns[turnID] = *turn
		}
		// Settling other open turns mirrors turn_completed: at most one live turn, so a
		// terminal event also retires any older non-settled zombie turns.
		ps.settleOtherOpenTurns(turnID, ps.projection.UpdatedAt)
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
	// P0-1: matches 随 tool_finished（result）到达，必须经 upsert 存活到 tool_started 创建的 part 上。
	// 对齐 ToolResult 的 non-nil merge 语义（后到覆盖）。
	if src.Matches != nil {
		dst.Matches = src.Matches
	}
	if src.Title != "" {
		dst.Title = src.Title
	}
	if src.FileChanges != nil {
		dst.FileChanges = src.FileChanges
	}
	if src.ToolStatus != "" {
		dst.ToolStatus = src.ToolStatus
	}
	// Official permission fields: non-empty wins (thin duplicates from a
	// same-serve legacy backend must not erase the official payload).
	if src.PermissionKind != "" {
		dst.PermissionKind = src.PermissionKind
	}
	if len(src.PermissionPatterns) > 0 {
		dst.PermissionPatterns = src.PermissionPatterns
	}
	if len(src.PermissionActions) > 0 {
		dst.PermissionActions = src.PermissionActions
	}
	if src.RequiresPermissionConfirmation {
		dst.RequiresPermissionConfirmation = true
	} else if src.ToolStatus != "" && src.ToolStatus != "pending" {
		dst.RequiresPermissionConfirmation = false
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
	// Heal pre-settle / missing-task_complete checkpoints: composer SoT is execution.phase,
	// but zombie running turns still pollute observers and future checkpoint writes.
	healProjectionTurnConsistency(&projection)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[projectionSessionKey(backendID, sessionID)] = &projectionSession{
		projection:     projection,
		lastAppliedRev: 0,
		lastFlushedRev: projection.SyncRev,
		publishedTurnShells: func() map[string]struct{} {
			published := make(map[string]struct{}, len(projection.Turns))
			for _, turn := range projection.Turns {
				if turn.TurnID != "" {
					published[turn.TurnID] = struct{}{}
				}
			}
			return published
		}(),
		textAppends: make(map[string][]string),
		thinking:    make(map[string]string),
		tools:       make(map[string]ProjectionPart),
		upsertTurns: make(map[string]TurnProjection),
	}
}

// healProjectionTurnConsistency enforces "at most one live turn" on restored snapshots.
// Idle sessions settle every non-settled turn; executing sessions settle every turn except ActiveTurnID.
func healProjectionTurnConsistency(projection *SessionProjection) {
	if projection == nil {
		return
	}
	activeTurnID := ""
	switch projection.Execution.Phase {
	case "running", "requires_action":
		activeTurnID = projection.Execution.ActiveTurnID
	default:
		// idle / unknown: no live turn
		activeTurnID = ""
	}
	for i := range projection.Turns {
		t := &projection.Turns[i]
		if t.TurnID == "" || t.TurnID == activeTurnID {
			continue
		}
		if t.Status == "completed" || t.Status == "aborted" || t.Status == "error" {
			continue
		}
		t.Status = "completed"
		if projection.UpdatedAt != 0 && t.CompletedAt == 0 {
			t.CompletedAt = projection.UpdatedAt
		}
		classifyProjectionTextPresentation(t.Assistant, true)
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
	return r.flushLocked(ps)
}

// flushLocked drains the staged live delta into one patch and advances the
// flush fence to the head. It backs both the live publisher flush AND the
// pre-commit drain inside the detail/state commit primitives
// (projection_detail_merge.go): a commit that pushes lastFlushedRev past a
// staged delta would strand it into a later zero-span patch that journal and
// delivery drop (audit P0-1). Caller MUST hold r.mu; ps must be non-nil.
func (r *ProjectionReducer) flushLocked(ps *projectionSession) (ProjectionPatch, bool) {
	ps.stageOwningTurnsForPendingParts()
	headRev := ps.projection.SyncRev
	if headRev == ps.lastFlushedRev && len(ps.textAppends) == 0 && len(ps.thinking) == 0 &&
		len(ps.tools) == 0 && len(ps.upsertTurns) == 0 && len(ps.userInputs) == 0 && ps.execution == nil {
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
	for turnID := range ps.upsertTurns {
		ps.publishedTurnShells[turnID] = struct{}{}
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
	// user_input upserts: one PartOp per interaction (in-place upsert by interactionId). The owning
	// message id is the assistant turn/message id (Claude/Codex assistant message id == turn id).
	for _, u := range ps.userInputs {
		part := cloneProjectionPart(u.part)
		patch.PartOps = append(patch.PartOps, PartOp{TurnID: u.turnID, MessageID: u.turnID, Op: "upsert_user_input", Part: &part})
	}
	// Clear pending; next patch will be delta from this head.
	ps.textAppends = make(map[string][]string)
	ps.thinking = make(map[string]string)
	ps.tools = make(map[string]ProjectionPart)
	ps.upsertTurns = make(map[string]TurnProjection)
	ps.userInputs = make(map[string]userInputPending)
	ps.execution = nil
	ps.lastFlushedRev = headRev
	return patch, true
}

// DropPendingPatch advances the patch fence to the authoritative head without
// constructing or journaling a patch. It is used when no session_sync_v2
// observer can receive the live delta: the reducer snapshot remains the source
// of truth, and a later observer must pull that snapshot before resuming live
// patches. Clearing the pending accumulator here is important for long-running
// turns with no clients; otherwise every token would keep accumulating copies
// of patch data that can never be delivered.
func (r *ProjectionReducer) DropPendingPatch(backendID, sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil {
		return false
	}
	headRev := ps.projection.SyncRev
	if headRev == ps.lastFlushedRev && len(ps.textAppends) == 0 && len(ps.thinking) == 0 &&
		len(ps.tools) == 0 && len(ps.upsertTurns) == 0 && len(ps.userInputs) == 0 && ps.execution == nil {
		return false
	}
	ps.textAppends = make(map[string][]string)
	ps.thinking = make(map[string]string)
	ps.tools = make(map[string]ProjectionPart)
	// A dropped pending turn upsert is still represented by the authoritative
	// reducer snapshot. Future observers pull that snapshot before receiving
	// compact PartOps, so treat those shells as published and avoid rebuilding
	// them on every subsequent tool event while no observer is attached.
	for turnID := range ps.upsertTurns {
		ps.publishedTurnShells[turnID] = struct{}{}
	}
	ps.upsertTurns = make(map[string]TurnProjection)
	ps.userInputs = make(map[string]userInputPending)
	ps.execution = nil
	ps.lastFlushedRev = headRev
	return true
}

// PendingPatchExceeds reports whether the next patch would carry a large whole
// turn snapshot. It deliberately inspects only pending tool/input/turn upserts:
// text and reasoning deltas are already compact append/set operations, while
// those upserts cause FlushPatch to deep-copy the complete active turn. The
// bounded estimate lets the publisher avoid that copy when nobody can receive
// the patch; callers may then serve a full authoritative snapshot on reconnect.
func (r *ProjectionReducer) PendingPatchExceeds(backendID, sessionID string, limit int) bool {
	if r == nil || limit <= 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil || (len(ps.tools) == 0 && len(ps.userInputs) == 0 && len(ps.upsertTurns) == 0) {
		return false
	}
	if ps.largePatchObserved {
		return true
	}
	for _, turn := range ps.upsertTurns {
		if projectionTurnExceeds(&turn, limit) {
			ps.largePatchObserved = true
			return true
		}
	}
	if len(ps.tools) > 0 {
		if turn := ps.turnByID(ps.projection.Execution.ActiveTurnID); turn != nil && projectionTurnExceeds(turn, limit) {
			ps.largePatchObserved = true
			return true
		}
	}
	for _, pending := range ps.userInputs {
		if turn := ps.turnByID(pending.turnID); turn != nil && projectionTurnExceeds(turn, limit) {
			ps.largePatchObserved = true
			return true
		}
	}
	return false
}

// projectionTurnExceeds is a bounded, allocation-free size estimate for the
// JSON-like values carried by projection parts. It is intentionally conservative
// about unknown scalar types (they are not expanded), and stops as soon as the
// configured budget is exhausted.
func projectionTurnExceeds(turn *TurnProjection, limit int) bool {
	if turn == nil {
		return false
	}
	budget := limit
	consume := func(value string) bool {
		budget -= len(value)
		return budget <= 0
	}
	var consumePart func(ProjectionPart) bool
	consumePart = func(part ProjectionPart) bool {
		if consume(part.Type) || consume(part.Text) || consume(part.Presentation) ||
			consume(part.ItemID) || consume(part.ToolName) || consume(part.ToolStatus) ||
			consume(part.Title) || consume(part.Path) || consume(part.Kind) ||
			consume(part.Diff) || consume(part.MovePath) || consume(part.AgentID) ||
			consume(part.ParentAgentID) || consume(part.SpawnToolUseID) ||
			consume(part.SubagentType) || consume(part.SubagentStatus) ||
			consume(part.SubagentError) || consume(part.SubagentDiagnostic) ||
			consume(part.UserInputInteractionID) || consume(part.UserInputStatus) ||
			consume(part.UserInputResolutionSource) || consume(part.UserInputDiagnosticCode) {
			return true
		}
		for _, nested := range part.SubagentBlocks {
			if consumePart(nested) {
				return true
			}
		}
		if projectionValueExceeds(part.ToolInput, &budget) ||
			projectionValueExceeds(part.ToolResult, &budget) ||
			projectionValueExceeds(part.Matches, &budget) ||
			projectionValueExceeds(part.FileChanges, &budget) ||
			projectionValueExceeds(part.UserInputQuestions, &budget) {
			return true
		}
		for _, action := range part.PermissionActions {
			if consume(action) {
				return true
			}
		}
		for _, pattern := range part.PermissionPatterns {
			if consume(pattern) {
				return true
			}
		}
		return budget <= 0
	}
	consumeMessage := func(message *MessageProjection) bool {
		if message == nil {
			return false
		}
		if consume(message.ID) || consume(message.ClientID) || consume(message.Role) {
			return true
		}
		for _, part := range message.Parts {
			if consumePart(part) {
				return true
			}
		}
		return false
	}
	if consume(turn.TurnID) || consume(turn.Status) || consumeMessage(turn.User) ||
		consumeMessage(turn.Assistant) || consumeMessage(turn.System) {
		return true
	}
	return budget <= 0
}

// projectionValueExceeds walks the JSON-compatible containers used by tool and
// structured-input payloads without marshaling or allocating a second copy.
func projectionValueExceeds(value interface{}, budget *int) bool {
	if value == nil || budget == nil || *budget <= 0 {
		return budget != nil && *budget <= 0
	}
	switch typed := value.(type) {
	case string:
		*budget -= len(typed)
	case []byte:
		*budget -= len(typed)
	case []interface{}:
		for _, item := range typed {
			if projectionValueExceeds(item, budget) {
				return true
			}
		}
	case []string:
		for _, item := range typed {
			*budget -= len(item)
			if *budget <= 0 {
				return true
			}
		}
	case map[string]interface{}:
		for key, item := range typed {
			*budget -= len(key)
			if *budget <= 0 || projectionValueExceeds(item, budget) {
				return true
			}
		}
	case map[string]string:
		for key, item := range typed {
			*budget -= len(key) + len(item)
			if *budget <= 0 {
				return true
			}
		}
	}
	return *budget <= 0
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

func (r *ProjectionReducer) lastInputSequence(backendID, sessionID string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.sessions[projectionSessionKey(backendID, sessionID)]
	if session == nil {
		return 0
	}
	return session.lastAppliedRev
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

// HasContentTurn reports whether the reducer holds at least one turn with real user, assistant,
// or system
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
		if t.System != nil && len(t.System.Parts) > 0 {
			return true
		}
	}
	return false
}

// NonTerminalTurnCountInSet returns how many of the given turn IDs are NOT yet in a terminal
// state (status other than completed/aborted/error). This is the §5.1 #7 hydrate-commit
// readiness signal, scoped to turns armed by cold-source ingest (see projectionHydrateTransaction.
// coldArmedTurnIDs). Turns carried from a committed/live baseline are authoritative live truth,
// not cold-source half-seen guesses, so they are excluded from the gate — a live in-progress
// session cold-pulled returns its current state instead of blocking until the in-flight turn
// completes. Guardrail #6: readiness is decided from authoritative cold-source-EOF + terminal
// state, never from content shape or turn count; the live baseline is authoritative, not guessed.
func (r *ProjectionReducer) NonTerminalTurnCountInSet(backendID, sessionID string, ids map[string]struct{}) int {
	if r == nil || len(ids) == 0 {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil {
		return 0
	}
	count := 0
	for i := range ps.projection.Turns {
		t := &ps.projection.Turns[i]
		if _, ok := ids[t.TurnID]; !ok {
			continue
		}
		st := t.Status
		if st != "completed" && st != "aborted" && st != "error" {
			count++
		}
	}
	return count
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
	if t.System != nil {
		s := *t.System
		if len(t.System.Parts) > 0 {
			s.Parts = make([]ProjectionPart, len(t.System.Parts))
			for i := range t.System.Parts {
				s.Parts[i] = cloneProjectionPart(t.System.Parts[i])
			}
		}
		out.System = &s
	}
	return out
}

func cloneProjectionPart(part ProjectionPart) ProjectionPart {
	out := part
	out.ToolInput = cloneProjectionJSONValue(part.ToolInput)
	out.ToolResult = cloneProjectionJSONValue(part.ToolResult)
	out.Matches = cloneProjectionJSONValue(part.Matches)
	out.FileChanges = cloneProjectionJSONValue(part.FileChanges)
	out.UserInputQuestions = cloneProjectionJSONValue(part.UserInputQuestions)
	if len(part.PermissionPatterns) > 0 {
		out.PermissionPatterns = make([]string, len(part.PermissionPatterns))
		copy(out.PermissionPatterns, part.PermissionPatterns)
	}
	if len(part.SubagentBlocks) > 0 {
		// SubagentBlocks is a concrete []ProjectionPart (recursive); the shallow `out := part`
		// above only copies the slice header. Deep-copy each nested part so the reducer's
		// projection and the delivered patch/freeze do not alias the same subagent blocks
		// (would race once a later reduce mutates either side).
		out.SubagentBlocks = make([]ProjectionPart, len(part.SubagentBlocks))
		for i := range part.SubagentBlocks {
			out.SubagentBlocks[i] = cloneProjectionPart(part.SubagentBlocks[i])
		}
	}
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

var (
	// ErrProjectionPrependInvalid rejects a structural historical prepend: empty or
	// duplicate turn ids, or a turn already committed (inclusive-cursor dedup is the
	// caller's contract; the reducer re-asserts it).
	ErrProjectionPrependInvalid = errors.New("projection: historical prepend invalid")
)

// PrependHistoricalTurns atomically inserts strictly-older historical turns (given in
// ASCENDING order) at the front of the committed projection, advancing syncRev by
// exactly one (lazy-history §2.4 / bridge-v1.md R11a). It deliberately produces NO
// pending content patch: this rev is journaled as a GAP on purpose — journal catch-up
// conns crossing it fall back to the authoritative {projection} form, which is the only
// order-correct carrier of a structural prepend (replica upsertTurns appends unknown
// turns at the tail and cannot express a front insert). Per-connection frames are the
// caller's job (no-op revision patch for window conns, sync_invalidate for
// full-projection conns; the requester sees the page inside its window result).
func (r *ProjectionReducer) PrependHistoricalTurns(
	backendID, sessionID string,
	turns []TurnProjection,
) (SessionProjection, error) {
	if r == nil {
		return SessionProjection{}, errors.New("projection reducer nil")
	}
	if backendID == "" || sessionID == "" {
		return SessionProjection{}, fmt.Errorf("%w: empty backend/session id", ErrProjectionPrependInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.sessions[projectionSessionKey(backendID, sessionID)]
	if ps == nil {
		return SessionProjection{}, fmt.Errorf("%w: session %s/%s not present", ErrProjectionPrependInvalid, backendID, sessionID)
	}
	if len(turns) == 0 {
		return cloneSessionProjection(ps.projection), nil
	}
	existing := make(map[string]struct{}, len(ps.projection.Turns))
	for i := range ps.projection.Turns {
		if ps.projection.Turns[i].TurnID != "" {
			existing[ps.projection.Turns[i].TurnID] = struct{}{}
		}
	}
	prepared := make([]TurnProjection, 0, len(turns))
	for i := range turns {
		turn := cloneTurn(turns[i])
		if turn.TurnID == "" {
			return SessionProjection{}, fmt.Errorf("%w: empty turnId at %d", ErrProjectionPrependInvalid, i)
		}
		if _, dup := existing[turn.TurnID]; dup {
			return SessionProjection{}, fmt.Errorf("%w: turn %s already committed", ErrProjectionPrependInvalid, turn.TurnID)
		}
		if _, dup := existing[""]; dup {
			delete(existing, "")
		}
		existing[turn.TurnID] = struct{}{}
		if turn.Status == "" {
			turn.Status = "completed"
		}
		prepared = append(prepared, turn)
	}
	ps.projection.Turns = append(prepared, ps.projection.Turns...)
	ps.projection.SyncRev++
	for i := range prepared {
		ps.publishedTurnShells[prepared[i].TurnID] = struct{}{}
	}
	// No patch for this rev — journal gap by design (see doc comment).
	ps.lastFlushedRev = ps.projection.SyncRev
	return cloneSessionProjection(ps.projection), nil
}
