package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// questions.go implements the C6 §6.8 structured-question surface from the
// A7 evidence (1.18.18):
//
//	GET  /question                         → pending requests (same shape as asked)
//	SSE  question.asked                    → {id, sessionID, questions[{question, header, options[{label, description}], multiple}], tool}
//	POST /question/{requestID}/reply       → body {"answers": string[][]} (labels per question)
//	POST /question/{requestID}/reject      → body {}
//	SSE  question.replied / question.rejected → terminal (no question_resolved exists)
//
// Bridge mapping (canonical): asked translates ONCE into the canonical
// user_input_requested payload (Event.UserInput) through the normal live
// route; replied/rejected translate into user_input_resolved with
// resolutionSource=other_client when the SERVER says another client answered.
// The legacy single-question presentation (RespondQuestion) stays
// not_supported — it is not the reply route.

// ocwQuestionOption is one official option row (labels only — the official
// protocol has no option ids).
type ocwQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ocwQuestion is one official question row.
type ocwQuestion struct {
	Question string               `json:"question"`
	Header   string               `json:"header"`
	Options  []ocwQuestionOption `json:"options"`
	Multiple bool                 `json:"multiple"`
}

// ocwQuestionTool is the official tool correlation block (A7): messageID =
// the assistant message of the running turn, callID = the tool item.
type ocwQuestionTool struct {
	MessageID string `json:"messageID"`
	CallID    string `json:"callID"`
}

// ocwQuestionRequest is the official pending/asked request shape.
type ocwQuestionRequest struct {
	ID        string        `json:"id"`
	SessionID string        `json:"sessionID"`
	Questions []ocwQuestion `json:"questions"`
	Tool      ocwQuestionTool `json:"tool"`
}

func knownToolCallID(req *ocwQuestionRequest) string {
	if req == nil {
		return ""
	}
	return req.Tool.CallID
}

// notePendingShape records one official row's reply mapping — the shape
// ResolveUserInput maps canonical option ids back to official labels with.
// Directive-012 seal: it consults the lifecycle under the same lock first, so
// a row the gate has already settled terminal in this process never re-opens
// its mapping (absent/creation is fine — a later recovery may still project
// the dock). This keeps the two tables consistent in one admission order:
// terminal ⇒ no mapping entry.
func (a *Agent) notePendingShape(sessionID string, req ocwQuestionRequest) {
	if req.ID == "" {
		return
	}
	a.questionMu.Lock()
	defer a.questionMu.Unlock()
	if lc, ok := a.questions[questionLifecycleKey(sessionID, req.ID)]; ok && lc != nil && lc.status != core.UserInputStatusPending {
		return
	}
	if a.pendingQuestions == nil {
		a.pendingQuestions = make(map[string]ocwQuestionRequest)
	}
	a.pendingQuestions[req.ID] = req
}

// forgetQuestion drops the reply-mapping entry ONLY (directive-011): the
// terminal projection belongs to the server broadcast / reconciliation, never
// to the RPC's own bookkeeping.
func (a *Agent) forgetQuestion(id string) {
	a.questionMu.Lock()
	defer a.questionMu.Unlock()
	delete(a.pendingQuestions, id)
}

// ── directive-011 lifecycle gate ─────────────────────────────────────────────
//
// questionLifecycle is the per-(session, interaction) state. All question
// facts — live question.asked, live question.replied/rejected broadcasts,
// recovery requested, recovery terminal — are admitted through ONE mutex, and
// an admitted fact is emitted while still holding it. The route channel then
// carries the facts in admission order, which is the provable serial
// reduction order the audit demanded: a terminal admitted before a stale
// requested means the requested is fenced out at the gate (it can never be
// enqueued ahead of the terminal), and duplicate facts never re-emit.
type questionLifecycle struct {
	sessionID     string
	interactionID string
	toolMessageID string
	toolCallID    string
	turnID        string
	status        core.UserInputStatus
}

func questionLifecycleKey(sessionID, interactionID string) string {
	return sessionID + "\x00" + interactionID
}

// gateAdmitRequested admits a pending fact. Terminal precedence: an
// interaction that is already pending (idempotent) or terminal (late/duplicate
// asked, stale recovery row) never re-emits requested — and, directive-012,
// never re-opens the reply mapping: the lifecycle verdict happens BEFORE any
// pendingQuestions write, so a settled interaction cannot be submitted again
// from this process.
func (a *Agent) gateAdmitRequested(sub *sseSubscriber, sessionID string, req ocwQuestionRequest, turnID string) bool {
	if sub == nil || sessionID == "" || req.ID == "" || turnID == "" {
		return false
	}
	a.questionMu.Lock()
	defer a.questionMu.Unlock()
	key := questionLifecycleKey(sessionID, req.ID)
	if lc, ok := a.questions[key]; ok && lc != nil && lc.status != "" {
		return false
	}
	if a.pendingQuestions == nil {
		a.pendingQuestions = make(map[string]ocwQuestionRequest)
	}
	a.pendingQuestions[req.ID] = req
	if a.questions == nil {
		a.questions = make(map[string]*questionLifecycle)
	}
	a.questions[key] = &questionLifecycle{
		sessionID:     sessionID,
		interactionID: req.ID,
		toolMessageID: req.Tool.MessageID,
		toolCallID:    req.Tool.CallID,
		turnID:        turnID,
		status:        core.UserInputStatusPending,
	}
	sub.emit(core.Event{
		Type:      core.EventUserInputRequested,
		SessionID: sessionID,
		TurnID:    turnID,
		ItemID:    req.Tool.CallID,
		UserInput: questionInteraction(req),
	})
	return true
}

// gateAdmitResolved admits a server-terminal fact. A projected pending part
// settles in place (the reducer keys the update on the stored interaction
// identity); a terminal stays (first server resolution wins); an interaction
// never projected here is recorded but NOT emitted — the reducer drops
// identity-less terminals, so emitting one would be a phantom.
func (a *Agent) gateAdmitResolved(sub *sseSubscriber, sessionID, interactionID string, status core.UserInputStatus) {
	if sub == nil || sessionID == "" || interactionID == "" {
		return
	}
	if status != core.UserInputStatusAnswered && status != core.UserInputStatusRejected {
		return
	}
	a.questionMu.Lock()
	defer a.questionMu.Unlock()
	delete(a.pendingQuestions, interactionID)
	key := questionLifecycleKey(sessionID, interactionID)
	lc, ok := a.questions[key]
	if !ok || lc == nil {
		if a.questions == nil {
			a.questions = make(map[string]*questionLifecycle)
		}
		a.questions[key] = &questionLifecycle{sessionID: sessionID, interactionID: interactionID, status: status}
		return
	}
	if lc.status != core.UserInputStatusPending {
		if lc.status != status {
			slog.Warn("opencode-web: question terminal conflict — first server resolution wins",
				"session", sessionID, "interaction", interactionID, "recorded", lc.status, "incoming", status)
		}
		return
	}
	lc.status = status
	sub.emit(core.Event{
		Type:      core.EventUserInputResolved,
		SessionID: sessionID,
		TurnID:    lc.turnID,
		ItemID:    lc.toolCallID,
		UserInput: &core.UserInputInteraction{
			InteractionID:    interactionID,
			Status:           status,
			CanRespond:       false,
			CanReject:        false,
			ResolutionSource: "other_client",
		},
	})
}

// gatePendingSnapshot copies the pending lifecycle entries of one session —
// taken BEFORE the recovery's GET /question is issued. Reconciliation only
// considers this pre-snapshot set, so a live ask landing after the snapshot
// can never be cleared by the (older) empty result.
func (a *Agent) gatePendingSnapshot(sessionID string) map[string]questionLifecycle {
	a.questionMu.Lock()
	defer a.questionMu.Unlock()
	out := make(map[string]questionLifecycle)
	for _, lc := range a.questions {
		if lc == nil || lc.sessionID != sessionID || lc.status != core.UserInputStatusPending {
			continue
		}
		out[lc.interactionID] = *lc
	}
	return out
}

// questionHistoryFacts is ONE authoritative GET /session/{id}/message
// transaction distilled for question recovery (directive-011): the
// assistant→owning-turn parentage and the A7-proven question-tool terminal
// evidence.
type questionHistoryFacts struct {
	assistantParent map[string]string            // assistant messageID → parentID
	parentIsUser    map[string]bool             // messageID → row is a user message
	terminals       map[string]questionTerminalEvidence
}

type questionTerminalEvidence struct {
	status core.UserInputStatus // answered | rejected
	turnID string
}

// questionRejectedErrorText is the official RejectedError message the serve
// records on the question tool when a client dismisses it (A7 reload/reject).
const questionRejectedErrorText = "The user dismissed this question"

// buildQuestionHistoryFacts walks the transaction rows. Terminal evidence is
// strictly A7-shaped: completed + captured metadata.answers → answered;
// error + the official dismissed text → rejected. Any other shape (or another
// session's row) contributes nothing — fail closed, never a guessed status.
func buildQuestionHistoryFacts(items []json.RawMessage, sessionID string) *questionHistoryFacts {
	facts := &questionHistoryFacts{
		assistantParent: make(map[string]string),
		parentIsUser:    make(map[string]bool),
		terminals:       make(map[string]questionTerminalEvidence),
	}
	for _, item := range items {
		var message map[string]any
		if err := json.Unmarshal(item, &message); err != nil {
			continue
		}
		info := message
		if sub, ok := message["info"].(map[string]any); ok {
			info = sub
		}
		// The endpoint is session-scoped; an explicit other-session id is a
		// provenance violation — the row contributes nothing.
		if sid := firstString(info, "sessionID"); sid != "" && sid != sessionID {
			continue
		}
		id := firstString(info, "id")
		if id == "" {
			continue
		}
		switch firstString(info, "role") {
		case "user":
			facts.parentIsUser[id] = true
		case "assistant":
			if parent := firstString(info, "parentID"); parent != "" {
				facts.assistantParent[id] = parent
			}
		}
		parts, _ := message["parts"].([]any)
		for _, partValue := range parts {
			part, _ := partValue.(map[string]any)
			if part == nil || firstString(part, "type") != "tool" {
				continue
			}
			if toolName, _ := part["tool"].(string); toolName != "question" {
				continue
			}
			messageID := firstString(part, "messageID")
			callID := firstString(part, "callID")
			if messageID == "" || callID == "" {
				continue
			}
			state, _ := part["state"].(map[string]any)
			if state == nil {
				continue
			}
			switch firstString(state, "status") {
			case "completed":
				metadata, _ := state["metadata"].(map[string]any)
				if _, ok := metadata["answers"].([]any); !ok {
					continue // completed without captured answers is not evidence
				}
				facts.terminals[messageID+"\x00"+callID] = questionTerminalEvidence{
					status: core.UserInputStatusAnswered,
					turnID: facts.assistantParent[messageID],
				}
			case "error":
				if firstString(state, "error") != questionRejectedErrorText {
					continue // unknown failure shape — fail closed
				}
				facts.terminals[messageID+"\x00"+callID] = questionTerminalEvidence{
					status: core.UserInputStatusRejected,
					turnID: facts.assistantParent[messageID],
				}
			}
		}
	}
	return facts
}

// provenTurn maps an assistant message to its owning turn from the same
// transaction: the parent must exist and be a real user row (no phantom).
func (f *questionHistoryFacts) provenTurn(messageID string) (string, bool) {
	parent, ok := f.assistantParent[messageID]
	if !ok || parent == "" || !f.parentIsUser[parent] {
		return "", false
	}
	return parent, true
}

// recoverPendingQuestions is the directive-010/011 recovery: pull the official
// GET /question list, keep ONLY the target session's rows, and re-present
// still-pending interactions through the ONE Kernel route with source-proven
// identity (subscriber facts first, then the authoritative history
// transaction). Directive-011 adds terminal precedence and reconciliation: a
// pending row whose history already carries an evidence-proven terminal is
// settled instead of armed, and locally-pending interactions (snapshot BEFORE
// the GET — the source fence) that the server no longer lists are reconciled
// from the SAME history transaction. Absence alone decides NOTHING; unknown
// terminal shapes fail closed with a diagnostic and stay pending for the next
// cycle. No ActiveTurnID fallback, no phantom turn, no raw second path.
func (a *Agent) recoverPendingQuestions(ctx context.Context, c *Client, sub *sseSubscriber, sessionID, directory string) {
	if sessionID == "" || sub == nil {
		return
	}
	if strings.TrimSpace(directory) == "" {
		directory = a.GetWorkDir()
	}

	localPending := a.gatePendingSnapshot(sessionID)

	rows, err := a.fetchPendingQuestions(ctx, c)
	if err != nil {
		slog.Warn("opencode-web: pending-question recovery fetch failed (no recovery this cycle)",
			"session", sessionID, "error", err)
		return
	}
	var facts *questionHistoryFacts
	history := func() *questionHistoryFacts {
		if facts != nil {
			return facts
		}
		raw, err := c.fetchJSON(ctx, c.apiPath("/session/")+sessionID+"/message", directory)
		if err != nil {
			return nil
		}
		items, err := decodeListPayload(raw)
		if err != nil {
			return nil
		}
		facts = buildQuestionHistoryFacts(items, sessionID)
		return facts
	}

	serverPending := make(map[string]bool)
	for _, row := range rows {
		if row.SessionID != sessionID {
			continue // the official list is serve-wide; only the target session is processed
		}
		if row.ID == "" || row.Tool.MessageID == "" || row.Tool.CallID == "" {
			slog.Warn("opencode-web: recovered pending question lacks tool identity — dropped",
				"session", sessionID, "question", row.ID)
			continue
		}
		serverPending[row.ID] = true
		// Server terminal beats a stale pending row: the reply may have landed
		// between the GET snapshot and the history fetch. Terminal settle never
		// opens the reply mapping (directive-012).
		if f := history(); f != nil {
			if term, ok := f.terminals[row.Tool.MessageID+"\x00"+row.Tool.CallID]; ok {
				a.gateAdmitResolved(sub, sessionID, row.ID, term.status)
				slog.Info("opencode-web: recovered question settled terminal instead of pending",
					"session", sessionID, "question", row.ID, "status", term.status)
				continue
			}
		}
		turnID, ok := sub.provenQuestionTurn(sessionID, row.Tool)
		if !ok {
			if f := history(); f != nil {
				turnID, ok = f.provenTurn(row.Tool.MessageID)
			}
		}
		if !ok {
			slog.Warn("opencode-web: recovered pending question failed source-proven owning-turn correlation — dropped (no phantom turn)",
				"session", sessionID, "question", row.ID, "toolMessageID", row.Tool.MessageID)
			continue
		}
		if a.gateAdmitRequested(sub, sessionID, row, turnID) {
			slog.Info("opencode-web: recovered pending question through the Kernel route",
				"session", sessionID, "question", row.ID, "turn", turnID)
		}
	}

	// Terminal reconciliation (directive-011): locally-pending interactions —
	// fixed BEFORE the snapshot was requested — that the server no longer
	// lists. The same authoritative history transaction must carry an
	// evidence-proven terminal; otherwise fail closed (kept pending, retried
	// on the next recovery cycle).
	for interactionID, lc := range localPending {
		if serverPending[interactionID] {
			continue
		}
		f := history()
		if f == nil {
			slog.Warn("opencode-web: pending-question reconciliation could not read the authoritative history — keeping pending (retry next cycle)",
				"session", sessionID, "interaction", interactionID)
			continue
		}
		term, ok := f.terminals[lc.toolMessageID+"\x00"+lc.toolCallID]
		if !ok {
			slog.Warn("opencode-web: server no longer lists a locally-pending question but the history carries no evidence-proven terminal — fail closed, keeping pending",
				"session", sessionID, "interaction", interactionID, "toolMessageID", lc.toolMessageID)
			continue
		}
		a.gateAdmitResolved(sub, sessionID, interactionID, term.status)
		slog.Info("opencode-web: reconciled missed question terminal through the Kernel route",
			"session", sessionID, "interaction", interactionID, "status", term.status)
	}
}

// questionInteraction maps the official asked request to the canonical
// UserInputInteraction payload. Option ids are derived deterministically
// (interactionID/qN/oN — §6.1) so iOS re-renders identically across reloads.
func questionInteraction(req ocwQuestionRequest) *core.UserInputInteraction {
	questions := make([]core.UserInputQuestion, 0, len(req.Questions))
	for qi, q := range req.Questions {
		mode := core.UserInputAnswerModeSingle
		if q.Multiple {
			mode = core.UserInputAnswerModeMultiple
		}
		options := make([]core.UserInputOption, 0, len(q.Options))
		for oi, opt := range q.Options {
			options = append(options, core.UserInputOption{
				ID:          fmt.Sprintf("%s/q%d/o%d", req.ID, qi, oi),
				Label:       opt.Label,
				Description: opt.Description,
			})
		}
		questions = append(questions, core.UserInputQuestion{
			ID:         fmt.Sprintf("%s/q%d", req.ID, qi),
			Header:     q.Header,
			Prompt:     q.Question,
			AnswerMode: mode,
			Options:    options,
			Required:   true,
		})
	}
	return &core.UserInputInteraction{
		InteractionID: req.ID,
		Status:        core.UserInputStatusPending,
		Questions:     questions,
		CanRespond:    true,
		CanReject:     true,
	}
}

// fetchPendingQuestions reads GET /question (A7-proven bare array).
func (a *Agent) fetchPendingQuestions(ctx context.Context, c *Client) ([]ocwQuestionRequest, error) {
	raw, err := c.fetchJSON(ctx, c.apiPath("/question"), a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	trimmed := trimSpaceBytes(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("opencode-web: pending questions must be a bare array (generation-118 verified shape), got: %s", truncateForError(string(raw)))
	}
	var rows []ocwQuestionRequest
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("opencode-web: pending questions malformed: %w", err)
	}
	out := make([]ocwQuestionRequest, 0, len(rows))
	for i, req := range rows {
		if req.ID == "" {
			return nil, fmt.Errorf("opencode-web: pending question row %d missing required id", i)
		}
		out = append(out, req)
	}
	return out, nil
}

// lookupQuestion returns the live request shape for an interaction id,
// recovering from GET /question when the in-memory pending map missed
// (restart, or an asked frame this process never saw).
func (a *Agent) lookupQuestion(ctx context.Context, c *Client, interactionID string) (*ocwQuestionRequest, error) {
	a.questionMu.Lock()
	req, ok := a.pendingQuestions[interactionID]
	a.questionMu.Unlock()
	if ok {
		return &req, nil
	}
	pending, err := a.fetchPendingQuestions(ctx, c)
	if err != nil {
		return nil, err
	}
	for _, p := range pending {
		a.notePendingShape(p.SessionID, p)
		if p.ID == interactionID {
			return &p, nil
		}
	}
	return nil, &core.UserInputError{
		Code:    "interaction_not_found",
		Message: "the server has no pending question with that id (it may already be answered by another client)",
	}
}

// ResolveUserInput implements core.UserInputResponder: answer/reject through
// the official routes. The serve holds the single answer lock — a reply that
// loses the race surfaces the serve's error honestly.
func (a *Agent) ResolveUserInput(ctx context.Context, interactionID string, clientActionID string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return core.UserInputResolution{}, err
	}
	req, err := a.lookupQuestion(ctx, c, interactionID)
	if err != nil {
		return core.UserInputResolution{}, err
	}

	if action == core.UserInputActionReject {
		path := c.apiPath("/question/") + interactionID + "/reject"
		code, raw, err := c.doRequest(ctx, http.MethodPost, c.endpoint(path), map[string]any{}, a.GetWorkDir(), true)
		if err != nil {
			return core.UserInputResolution{}, fmt.Errorf("opencode-web question reject: %w", err)
		}
		if code == 404 || code == 409 {
			a.forgetQuestion(interactionID)
			return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusRejected}, nil
		}
		if code >= 400 {
			return core.UserInputResolution{}, fmt.Errorf("opencode-web question reject HTTP %d: %s", code, truncateForError(string(raw)))
		}
		a.forgetQuestion(interactionID)
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusRejected}, nil
	}

	// Answer: map canonical answers onto the official string[][] (labels per
	// question, question order preserved; unanswered questions submit []).
	body := make([][]string, len(req.Questions))
	for i := range body {
		body[i] = []string{}
	}
	for _, ans := range answers {
		qIdx := -1
		for i := range req.Questions {
			if fmt.Sprintf("%s/q%d", interactionID, i) == ans.QuestionID {
				qIdx = i
				break
			}
		}
		if qIdx == -1 {
			return core.UserInputResolution{}, &core.UserInputError{
				Code:    "invalid_answer",
				Message: "answer references an unknown question of this interaction",
			}
		}
		values := make([]string, 0, len(ans.Values))
		for _, v := range ans.Values {
			switch v.Kind {
			case core.UserInputValueOption:
				label, ok := optionLabelFor(req.Questions[qIdx], v.OptionID)
				if !ok {
					return core.UserInputResolution{}, &core.UserInputError{
						Code:    "invalid_answer",
						Message: "answer references an unknown option of this question",
					}
				}
				values = append(values, label)
			case core.UserInputValueText:
				if strings.TrimSpace(v.Text) == "" {
					return core.UserInputResolution{}, &core.UserInputError{
						Code:    "invalid_answer",
						Message: "custom answer value is empty",
					}
				}
				values = append(values, v.Text)
			}
		}
		body[qIdx] = values
	}
	for i, slot := range body {
		if len(slot) == 0 {
			return core.UserInputResolution{}, &core.UserInputError{
				Code:    "invalid_answer",
				Message: fmt.Sprintf("question %d has no answer (the official reply requires a value per question)", i),
			}
		}
	}

	path := c.apiPath("/question/") + interactionID + "/reply"
	code, raw, err := c.doRequest(ctx, http.MethodPost, c.endpoint(path), map[string]any{"answers": body}, a.GetWorkDir(), true)
	if err != nil {
		return core.UserInputResolution{}, fmt.Errorf("opencode-web question reply: %w", err)
	}
	if code == 404 || code == 409 {
		a.forgetQuestion(interactionID)
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
	}
	if code >= 400 {
		return core.UserInputResolution{}, fmt.Errorf("opencode-web question reply HTTP %d: %s", code, truncateForError(string(raw)))
	}
	a.forgetQuestion(interactionID)
	return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
}

func optionLabelFor(q ocwQuestion, optionID string) (string, bool) {
	// Option ids are "<interactionID>/qN/oM" (questionInteraction); only the
	// trailing oM indexes this question's options.
	parts := strings.Split(optionID, "/")
	if len(parts) == 0 {
		return "", false
	}
	tail := parts[len(parts)-1]
	for oi, opt := range q.Options {
		if fmt.Sprintf("o%d", oi) == tail {
			return opt.Label, true
		}
	}
	return "", false
}

// StructuredUserInputReady implements core.StructuredUserInputProvider.
func (a *Agent) StructuredUserInputReady() bool { return true }

var _ core.UserInputResponder = (*Agent)(nil)
var _ core.StructuredUserInputProvider = (*Agent)(nil)

