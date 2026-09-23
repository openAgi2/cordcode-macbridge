package dshweb

// Approval/question pipeline (design §4.3.4, M2 一期必接 — without it an
// iOS-initiated turn hangs forever at the first ask-policy tool, violating
// fail-visibly).
//
// Typert-gateway shape (2026-09-23 migration): approvals and questions are
// $events WATERFALLS — {event:"approval/request"|"user-questions/request",
// eventId, agentId, request}. The agentId IS the session id (Agent.id is the
// session-backed identity, core/agent/src/types.ts). The surfaced request id
// is the eventId (the only correlation the wire offers; the journal's
// approval/asked ids live in a different, uncorrelated id space). Answers go
// via POST /api/$events/result {clientId, eventId, outcome} where the value
// is the official answer payload: approval → the outcome string
// ("allowed-once"|"rejected"), question → {answers:[{id,selected,custom?}]}.
//
// Approval flow: waterfall → core permission_request. Bound sessions emit on
// the session channel; unbound (Mac-initiated) sessions emit on the agent
// passive channel so an observing iPhone can also approve. First writer
// wins: the Host cancels the losing waterfall (cancel frame) and a late
// $events/result fails with "no active event stream" — both close the pending
// entry and emit permission_resolved so the projection drops
// requiresPermissionConfirmation.
//
// Question flow (R2-1/R3-1/S-1/S-2/S-3): dsh asks WHOLE BATCHES (one ask,
// many questions, one answer). Each question carries its own dsh id —
// per-question ids ride the bridge wire so iOS's replace-by-id upsert keeps
// every question visible and answerable. The waterfall's eventId is dshweb-
// internal batch state only, never on the wire. Answers accumulate per
// question id (later answer overwrites — S-3); when the batch is complete
// ONE $events/result posts {answers:[…]} keyed by question id. The batch's
// per-question resolution is emitted locally once the respond is accepted
// (the gateway generation has no question/resolved frame); a cancel frame
// (withdrawn/claimed elsewhere) or a generation loss settles the batch as
// cancelled. Reconnect replays still-pending waterfalls under NEW eventIds:
// re-emitting question events is idempotent on iOS (same question ids), and
// a missing batch is rebuilt from the replay (S-2). Batches answered on the
// web during a disconnect window are NOT replayed — those iOS pending steps
// settle via the session's cold reload, same as every other transient
// question backend.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.SessionPermissionResponder = (*Agent)(nil)
var _ core.SessionQuestionResponder = (*Agent)(nil)
var _ core.UserInputResponder = (*Agent)(nil)
var _ core.StructuredUserInputProvider = (*Agent)(nil)
var _ core.UserInputResponder = (*dshSession)(nil)

// approvalsState is the agent-level pending registry (waterfalls are
// agent-scoped).
type approvalsState struct {
	mu sync.Mutex
	// approvals: eventId → pending approval (surfaced ones only).
	approvals map[string]*pendingApproval
	// batches: waterfall eventId → pending question batch.
	batches map[string]*pendingQuestionBatch
	// questionOwner: question id → batch eventId (answer routing).
	questionOwner map[string]string
	// planReviews: question id → derived plan-review answer labels (plan approval
	// layer, 2026-09-04). Present only for questions surfaced as the plan_review
	// permission card; cleaned up with questionOwner on resolution.
	planReviews map[string]planReviewMeta
}

// planReviewMeta mirrors what the official intent needs for answering
// (plan-mode/src/index.ts @49a606bc): intent.approve NAMES the approve label —
// never hardcode "Approve" — and the keep-planning label is the question's
// single other option.
type planReviewMeta struct {
	approveLabel      string
	keepPlanningLabel string
}

// pendingApproval is one surfaced approval waterfall. clientID is the $events
// generation that delivered it — the respond correlation; a generation loss
// orphans it (dropAllPendingInteractions settles those cards).
type pendingApproval struct {
	clientID  string
	eventID   string
	sessionID string
	toolName  string
}

// pendingQuestionBatch is one dsh ask batch awaiting its complete answer.
type pendingQuestionBatch struct {
	eventID   string // waterfall eventId — the $events/result correlation key
	clientID  string // $events generation that delivered the waterfall
	sessionID string
	// questionIDs preserves the batch's own order.
	questionIDs []string
	// answers accumulates per question id (overwrite semantics, S-3).
	answers map[string]questionAnswer
	// responded marks a terminal respond already sent (answered or cancelled).
	responded bool
}

type questionAnswer struct {
	selected []string
	custom   string
}

func (a *Agent) approvalsInit() {
	a.approvalsMu.Lock()
	defer a.approvalsMu.Unlock()
	if a.approvals == nil {
		a.approvals = &approvalsState{
			approvals:     map[string]*pendingApproval{},
			batches:       map[string]*pendingQuestionBatch{},
			questionOwner: map[string]string{},
			planReviews:   map[string]planReviewMeta{},
		}
	}
}

// emitPermissionEvent delivers a permission asked/resolved event to the bound
// session when one exists, otherwise to the agent passive channel (Mac-initiated
// turns that iOS is only observing).
func (a *Agent) sessionTurnID(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if c := a.codecs[sessionID]; c != nil {
		return c.activeTurnID
	}
	return ""
}

func (a *Agent) emitQuestionResolved(sessionID string, sess *dshSession, questionID, outcome string) {
	status := core.UserInputStatusAnswered
	if outcome == "cancelled" || outcome == "rejected" {
		status = core.UserInputStatusRejected
	}
	a.emitPermissionEvent(sessionID, sess, core.Event{
		Type:      core.EventUserInputResolved,
		SessionID: sessionID,
		TurnID:    a.sessionTurnID(sessionID),
		ItemID:    questionID,
		UserInput: &core.UserInputInteraction{
			InteractionID:    questionID,
			Status:           status,
			ResolutionSource: "ios",
		},
	})
	a.emitPermissionEvent(sessionID, sess, core.Event{
		Type:       core.EventQuestionResolved,
		SessionID:  sessionID,
		QuestionID: questionID,
		Content:    outcome,
		ThreadID:   sessionID,
	})
}

func (a *Agent) emitPermissionEvent(sessionID string, sess *dshSession, ev core.Event) {
	if ev.SessionID == "" {
		ev.SessionID = sessionID
	}
	if sess != nil {
		sess.emitControlCritical(ev)
		return
	}
	ch := a.passiveEvents()
	select {
	case ch <- ev:
	case <-time.After(5 * time.Second):
		slog.Error("dsh-web: passive permission event dropped after wait",
			"sessionPrefix", shortLog(sessionID), "type", string(ev.Type))
	}
}

// RespondSessionPermission implements core.SessionPermissionResponder so
// resolve_permission works without a go-bridge registry session (observe-only).
func (a *Agent) RespondSessionPermission(ctx context.Context, sessionID, requestID string, result core.PermissionResult) error {
	return a.respondPermissionRouted(ctx, sessionID, requestID, result)
}

// respondPermissionRouted dispatches a resolve_permission to the plan-review
// question machinery when the request id belongs to a surfaced plan card, else
// to the approval responder (plan approval layer, 2026-09-04).
func (a *Agent) respondPermissionRouted(ctx context.Context, sessionID, requestID string, result core.PermissionResult) error {
	a.approvalsInit()
	a.approvals.mu.Lock()
	meta, isPlan := a.approvals.planReviews[requestID]
	a.approvals.mu.Unlock()
	if isPlan {
		return a.respondPlanReview(ctx, sessionID, requestID, meta, result)
	}
	return a.respondApproval(ctx, sessionID, requestID, result)
}

// respondPlanReview maps the plan-card vocabulary onto the official question
// answer (方案 §4.3 翻译表；plan-mode/src/index.ts answer reading @49a606bc):
// approve → select the intent-named approve label; requestChanges → select the
// keep-planning label with custom=feedback (empty feedback omits custom);
// quit → reject the batch (respond error branch = ASK_CANCELLED, "the user
// dismissed the plan review" — D3). Legacy two-button replies (no planAction)
// map allow→approve label, anything else→keep-planning label.
func (a *Agent) respondPlanReview(ctx context.Context, sessionID, questionID string, meta planReviewMeta, result core.PermissionResult) error {
	switch result.PlanAction {
	case "approve":
		return a.respondQuestion(ctx, sessionID, questionID, []string{meta.approveLabel}, "")
	case "requestChanges":
		return a.respondQuestion(ctx, sessionID, questionID, []string{meta.keepPlanningLabel}, result.Message)
	case "quit":
		return a.rejectQuestion(ctx, sessionID, questionID)
	default:
		if result.Behavior == "allow" || result.Behavior == "always" {
			return a.respondQuestion(ctx, sessionID, questionID, []string{meta.approveLabel}, "")
		}
		return a.respondQuestion(ctx, sessionID, questionID, []string{meta.keepPlanningLabel}, "")
	}
}

func (a *Agent) StructuredUserInputReady() bool { return true }

func (a *Agent) RespondSessionQuestion(ctx context.Context, sessionID, questionID string, optionIDs []string) error {
	return a.respondQuestion(ctx, sessionID, questionID, optionIDs, "")
}

func (a *Agent) RejectSessionQuestion(ctx context.Context, sessionID, questionID string) error {
	return a.rejectQuestion(ctx, sessionID, questionID)
}

func (a *Agent) ResolveUserInput(ctx context.Context, interactionID string, _ string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	a.approvalsInit()
	a.approvals.mu.Lock()
	rpcID := a.approvals.questionOwner[interactionID]
	batch := a.approvals.batches[rpcID]
	sessionID := ""
	if batch != nil {
		sessionID = batch.sessionID
	}
	a.approvals.mu.Unlock()
	if sessionID == "" {
		return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "question is not pending"}
	}
	sess, _ := a.bindings.get(sessionID)
	if action == core.UserInputActionReject {
		if err := a.rejectQuestion(ctx, sessionID, interactionID); err != nil {
			return core.UserInputResolution{}, err
		}
		a.emitQuestionResolved(sessionID, sess, interactionID, "cancelled")
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusRejected}, nil
	}
	for _, ans := range answers {
		var selected []string
		var custom string
		for _, v := range ans.Values {
			if v.Kind == core.UserInputValueOption && v.OptionID != "" {
				selected = append(selected, v.OptionID)
			}
			if v.Kind == core.UserInputValueText && strings.TrimSpace(v.Text) != "" {
				custom = v.Text
			}
		}
		qid := ans.QuestionID
		if qid == "" {
			qid = interactionID
		}
		if err := a.respondQuestion(ctx, sessionID, qid, selected, custom); err != nil {
			return core.UserInputResolution{}, err
		}
		a.emitQuestionResolved(sessionID, sess, qid, "answered")
	}
	return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
}

func (s *dshSession) ResolveUserInput(ctx context.Context, interactionID string, clientActionID string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	return s.agent.ResolveUserInput(ctx, interactionID, clientActionID, action, answers)
}

// emitControlCritical posts a control-critical event (permission/question) to
// the bound session with a bounded wait — unlike live deltas these may not be
// silently dropped (a lost permission_request hangs the turn, 坑 8).
func (s *dshSession) emitControlCritical(ev core.Event) {
	if s.closed.Load() {
		return
	}
	if ev.SessionID == "" {
		ev.SessionID = s.CurrentSessionID()
	}
	defer func() { _ = recover() }()
	select {
	case s.events <- ev:
	case <-time.After(5 * time.Second):
		slog.Error("dsh-web: control-critical event dropped after wait",
			"sessionPrefix", shortLog(ev.SessionID), "type", string(ev.Type))
	case <-s.ctx.Done():
	}
}

// ── $events waterfall entries ──────────────────────────────────────────────

// dispatchWaterfall routes one approval/user-questions waterfall frame. The
// agentId IS the session id (Agent.id is the session-backed identity).
func (a *Agent) dispatchWaterfall(client *Client, f remoteEventFrame) {
	switch f.Event {
	case "approval/request":
		a.handleApprovalWaterfall(f)
	case "user-questions/request":
		a.handleQuestionWaterfall(f)
	default:
		slog.Debug("dsh-web: unknown waterfall", "event", f.Event)
	}
}

// handleApprovalWaterfall surfaces one approval/request waterfall
// (request: {toolName, callId?, reason?} — interaction/user-approval
// ApprovalRequestEvent with the agent/signal lifetime stripped in transit).
func (a *Agent) handleApprovalWaterfall(f remoteEventFrame) {
	a.approvalsInit()
	if f.EventID == "" || f.AgentID == "" {
		slog.Warn("dsh-web: approval waterfall missing correlation", "eventId", f.EventID)
		return
	}
	var req struct {
		ToolName string `json:"toolName"`
		CallID   string `json:"callId"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(f.Request, &req); err != nil || req.ToolName == "" {
		slog.Warn("dsh-web: approval/request unparsable", "error", err)
		return
	}
	sessionID := f.AgentID
	a.approvals.mu.Lock()
	if _, exists := a.approvals.approvals[f.EventID]; !exists {
		a.approvals.approvals[f.EventID] = &pendingApproval{
			clientID: a.muxClientID, eventID: f.EventID, sessionID: sessionID, toolName: req.ToolName,
		}
	} // existing = reconnect replay under a new generation: re-emit only
	a.approvals.mu.Unlock()
	sess, _ := a.bindings.get(sessionID)
	// The official approval request carries no tool input — the request
	// surfaces with the tool name; nothing is invented.
	raw := map[string]any{}
	if req.Reason != "" {
		raw["reason"] = req.Reason
	}
	if req.CallID != "" {
		raw["callId"] = req.CallID
	}
	var toolInputRaw map[string]any
	if len(raw) > 0 {
		toolInputRaw = raw
	}
	a.emitPermissionEvent(sessionID, sess, core.Event{
		Type:         core.EventPermissionRequest,
		SessionID:    sessionID,
		RequestID:    f.EventID,
		ToolName:     req.ToolName,
		Content:      req.Reason,
		ToolInput:    req.Reason,
		ToolInputRaw: toolInputRaw,
	})
	slog.Info("dsh-web: approval surfaced", "sessionPrefix", shortLog(sessionID), "tool", req.ToolName, "bound", sess != nil)
}

// derivePlanReviewMeta validates the official intent against the offered
// options: the approve label must be named by intent.approve and present among
// the options, and exactly one other option must remain (the keep-planning
// label). Anything else is not answerable as a plan review.
func derivePlanReviewMeta(approve string, labels []string) (planReviewMeta, bool) {
	if approve == "" {
		return planReviewMeta{}, false
	}
	var keep string
	found := false
	for _, l := range labels {
		if l == approve {
			found = true
			continue
		}
		if keep != "" {
			return planReviewMeta{}, false // more than one non-approve option
		}
		keep = l
	}
	if !found || keep == "" {
		return planReviewMeta{}, false
	}
	return planReviewMeta{approveLabel: approve, keepPlanningLabel: keep}, true
}

// handleQuestionWaterfall surfaces one user-questions/request waterfall
// (request: {questions:[…]} — interaction/user-questions
// AskUserQuestionRequestEvent with the agent/signal lifetime stripped).
func (a *Agent) handleQuestionWaterfall(f remoteEventFrame) {
	a.approvalsInit()
	if f.EventID == "" || f.AgentID == "" {
		slog.Warn("dsh-web: question waterfall missing correlation", "eventId", f.EventID)
		return
	}
	var req struct {
		Questions []struct {
			ID       string `json:"id"`
			Question string `json:"question"`
			Header   string `json:"header"`
			Detail   string `json:"detail"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
			MultiSelect bool `json:"multiSelect"`
			// intent is presentation-only metadata on the official question
			// (plan-mode/src/index.ts:313): {kind:"plan-review", approve:<label>}
			// names the approve label; a capable UI renders the plan (detail)
			// as a review decision instead of a generic question.
			Intent *struct {
				Kind    string `json:"kind"`
				Approve string `json:"approve,omitempty"`
			} `json:"intent"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(f.Request, &req); err != nil || len(req.Questions) == 0 {
		slog.Warn("dsh-web: user-questions/request unparsable or empty", "error", err)
		return
	}
	sessionID := f.AgentID
	rpcID := f.EventID
	a.approvals.mu.Lock()
	if _, exists := a.approvals.batches[rpcID]; !exists {
		fresh := &pendingQuestionBatch{
			eventID:     rpcID,
			clientID:    a.muxClientID,
			sessionID:   sessionID,
			answers:     map[string]questionAnswer{},
			questionIDs: make([]string, 0, len(req.Questions)),
		}
		for _, q := range req.Questions {
			fresh.questionIDs = append(fresh.questionIDs, q.ID)
			a.approvals.questionOwner[q.ID] = rpcID
		}
		a.approvals.batches[rpcID] = fresh
	} // existing batch = reconnect replay (S-2): re-emit only
	a.approvals.mu.Unlock()

	sess, _ := a.bindings.get(sessionID)
	// Per-question events, each with its own dsh id (R3-1). Bound sessions
	// use the session channel; Mac-initiated (unbound) use the passive
	// channel so an observing iPhone can answer too.
	//
	// Canonical writer is user_input_requested (SSV2 projection / UserInputDock).
	// question_asked is the one-way legacy presentation only — EventPublisher
	// will not ingest it, so emitting it alone leaves iPhone with no card
	// (owner 2026-08-16: Mac 多选框出现，iPhone 没有).
	for _, q := range req.Questions {
		// plan approval layer (2026-09-04): a plan-review question (official
		// intent.kind=="plan-review", plan full text in detail) surfaces as the
		// plan_review permission card INSTEAD of the user_input card — the
		// answer rides resolve_permission.planAction. Non-plan questions in
		// the same batch are unaffected. Meta not derivable (intent.approve
		// not among the offered labels / option shape unexpected) fails
		// closed to the generic card, never to a broken plan card.
		if q.Intent != nil && q.Intent.Kind == "plan-review" {
			labels := make([]string, 0, len(q.Options))
			for _, o := range q.Options {
				labels = append(labels, o.Label)
			}
			if meta, ok := derivePlanReviewMeta(q.Intent.Approve, labels); ok {
				a.approvals.mu.Lock()
				a.approvals.planReviews[q.ID] = meta
				a.approvals.mu.Unlock()
				a.emitPermissionEvent(sessionID, sess, core.Event{
					Type:              core.EventPermissionRequest,
					SessionID:         sessionID,
					TurnID:            a.sessionTurnID(sessionID),
					RequestID:         q.ID,
					ToolName:          q.Header,
					PermissionKind:    "plan_review",
					PermissionActions: []string{"approve", "requestChanges", "quit"},
					PlanReview:        &core.PlanPayload{Content: q.Detail},
				})
				continue
			}
			slog.Warn("dsh-web: plan-review question without derivable labels, degrading to generic card",
				"sessionPrefix", shortLog(sessionID), "questionPrefix", shortLog(q.ID), "approve", q.Intent.Approve, "options", len(q.Options))
		}
		opts := make([]core.QuestionOption, 0, len(q.Options))
		uiOpts := make([]core.UserInputOption, 0, len(q.Options))
		for _, o := range q.Options {
			// dsh options have no ids: the label IS the identifier, echoed
			// verbatim in the answer's selected[] (user-questions types).
			opts = append(opts, core.QuestionOption{ID: o.Label, Label: o.Label, Description: o.Description})
			uiOpts = append(uiOpts, core.UserInputOption{ID: o.Label, Label: o.Label, Description: o.Description})
		}
		text := q.Question
		if q.Header != "" {
			text = q.Header + "：" + q.Question
		}
		mode := core.UserInputAnswerModeSingle
		if q.MultiSelect {
			mode = core.UserInputAnswerModeMultiple
		}
		a.emitPermissionEvent(sessionID, sess, core.Event{
			Type:      core.EventUserInputRequested,
			SessionID: sessionID,
			TurnID:    a.sessionTurnID(sessionID),
			ItemID:    q.ID,
			UserInput: &core.UserInputInteraction{
				InteractionID: q.ID,
				Status:        core.UserInputStatusPending,
				Questions: []core.UserInputQuestion{{
					ID:                 q.ID,
					Header:             q.Header,
					Prompt:             q.Question,
					AnswerMode:         mode,
					Options:            uiOpts,
					AllowsCustomAnswer: true,
					IsSecret:           false,
					Required:           true,
				}},
				CanRespond: true,
				CanReject:  true,
			},
		})
		a.emitPermissionEvent(sessionID, sess, core.Event{
			Type:         core.EventQuestionAsked,
			SessionID:    sessionID,
			QuestionID:   q.ID,
			QuestionText: text,
			QuestionOpts: opts,
			Required:     true,
			ThreadID:     sessionID,
		})
	}
	slog.Info("dsh-web: question batch surfaced",
		"sessionPrefix", shortLog(sessionID), "questions", len(req.Questions), "batch", shortLog(rpcID), "bound", sess != nil)
}

// ── responders (bridge handler entry) ──────────────────────────────────────

// isAnsweredElsewhere reports whether a $events/result failure means the
// waterfall was already claimed/cancelled (first-writer-wins) — the
// gateway's "identifies no active event stream" rejection.
func isAnsweredElsewhere(err error) bool {
	var rpcErr *RPCError
	return err != nil && errors.As(err, &rpcErr) &&
		strings.Contains(rpcErr.Message, "no active event stream")
}

// respondApproval maps the iOS permission decision onto the official
// approval outcome string (the $events/result value is the bare
// ApprovalOutcome — interaction/user-approval types.ts).
func (a *Agent) respondApproval(ctx context.Context, sessionID, requestID string, result core.PermissionResult) error {
	client, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	outcome := "rejected"
	// "always" (opencode-web official reply) has no dsh-web equivalent; degrade to allow-once.
	if result.Behavior == "allow" || result.Behavior == "always" {
		outcome = "allowed-once"
	}
	a.approvalsInit()
	a.approvals.mu.Lock()
	pending := a.approvals.approvals[requestID]
	if pending != nil {
		delete(a.approvals.approvals, requestID)
	}
	a.approvals.mu.Unlock()
	if pending == nil {
		// Unknown here: answered/cancelled elsewhere (the cancel frame or a
		// generation loss already settled the card) — not an iOS error.
		slog.Info("dsh-web: approval respond for unknown waterfall", "approval", shortLog(requestID))
		return nil
	}
	if err := client.RespondEventResult(ctx, pending.clientID, pending.eventID, outcome, false); err != nil {
		if isAnsweredElsewhere(err) {
			// First-writer-wins: the web already answered. The turn's
			// continuation is the visible outcome — not an error for iOS.
			slog.Info("dsh-web: approval answered elsewhere", "approval", shortLog(requestID))
		} else {
			return err
		}
	}
	behavior := "deny"
	if outcome == "allowed-once" {
		behavior = "allow"
	}
	sess, _ := a.bindings.get(pending.sessionID)
	a.emitPermissionEvent(pending.sessionID, sess, core.Event{
		Type:      core.EventPermissionResolved,
		SessionID: pending.sessionID,
		RequestID: requestID,
		Content:   behavior,
	})
	slog.Info("dsh-web: approval resolved", "sessionPrefix", shortLog(pending.sessionID), "outcome", outcome)
	return nil
}

// respondQuestion accumulates one per-question answer; the batch answers ONCE
// when every question carries an answer (R3-1/S-3) — the $events/result value
// is the official AskUserQuestionAnswer {answers:[…]}.
func (a *Agent) respondQuestion(ctx context.Context, sessionID, questionID string, optionIDs []string, custom string) error {
	a.approvalsInit()
	a.approvals.mu.Lock()
	rpcID := a.approvals.questionOwner[questionID]
	if rpcID == "" {
		a.approvals.mu.Unlock()
		return fmt.Errorf("dsh-web: unknown question %s (no pending batch)", shortLog(questionID))
	}
	batch := a.approvals.batches[rpcID]
	if batch == nil || batch.responded {
		a.approvals.mu.Unlock()
		return fmt.Errorf("dsh-web: question batch %s is not pending", shortLog(rpcID))
	}
	batch.answers[questionID] = questionAnswer{selected: append([]string(nil), optionIDs...), custom: custom}
	complete := len(batch.answers) == len(batch.questionIDs)
	if !complete {
		a.approvals.mu.Unlock()
		return nil // accumulated; the batch responds when complete
	}
	// Assemble under the lock: a racing duplicate submit must not double-send.
	batch.responded = true
	answers := make([]map[string]any, 0, len(batch.questionIDs))
	for _, qid := range batch.questionIDs {
		ans := batch.answers[qid]
		entry := map[string]any{"id": qid, "selected": ans.selected}
		if strings.TrimSpace(ans.custom) != "" {
			entry["custom"] = ans.custom
		}
		answers = append(answers, entry)
	}
	a.approvals.mu.Unlock()

	client, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	value := map[string]any{"answers": answers}
	if err := client.RespondEventResult(ctx, batch.clientID, batch.eventID, value, false); err != nil {
		if isAnsweredElsewhere(err) {
			slog.Info("dsh-web: question answered/cancelled elsewhere", "batch", shortLog(batch.eventID))
			a.settleBatch(batch.eventID, "cancelled")
			return nil
		}
		return err
	}
	// The gateway generation has no question/resolved frame: the accepted
	// respond settles the batch locally (S-1 expansion — one resolved event
	// per question id so each iOS pending card closes).
	a.settleBatch(batch.eventID, "answered")
	return nil
}

// rejectQuestion cancels the WHOLE batch via the rejected outcome branch
// (asymmetric with approvals, R2-1/S-3).
func (a *Agent) rejectQuestion(ctx context.Context, sessionID, questionID string) error {
	a.approvalsInit()
	a.approvals.mu.Lock()
	rpcID := a.approvals.questionOwner[questionID]
	if rpcID == "" {
		a.approvals.mu.Unlock()
		return fmt.Errorf("dsh-web: unknown question %s (no pending batch)", shortLog(questionID))
	}
	batch := a.approvals.batches[rpcID]
	if batch == nil || batch.responded {
		a.approvals.mu.Unlock()
		return fmt.Errorf("dsh-web: question batch %s is not pending", shortLog(rpcID))
	}
	batch.responded = true
	a.approvals.mu.Unlock()

	client, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	if err := client.RespondEventResult(ctx, batch.clientID, batch.eventID, nil, true); err != nil {
		if isAnsweredElsewhere(err) {
			slog.Info("dsh-web: question answered/cancelled elsewhere", "batch", shortLog(batch.eventID))
		} else {
			return err
		}
	}
	a.settleBatch(batch.eventID, "cancelled")
	slog.Info("dsh-web: question batch cancelled by reject", "batch", shortLog(batch.eventID),
		"viaQuestion", shortLog(questionID), "questions", len(batch.questionIDs))
	return nil
}

// settleBatch closes one pending question batch and expands its state into
// per-id resolved events (the S-1 expansion the retired question/resolved
// frame used to trigger). outcome is echoed verbatim (answered|cancelled) —
// no synthetic approve/deny claim. Unknown/missing batches are a no-op.
func (a *Agent) settleBatch(rpcID, outcome string) {
	a.approvalsInit()
	a.approvals.mu.Lock()
	batch := a.approvals.batches[rpcID]
	if batch != nil {
		delete(a.approvals.batches, rpcID)
		for _, qid := range batch.questionIDs {
			delete(a.approvals.questionOwner, qid)
		}
	}
	// Plan-surfaced question ids leave the plan registry too; snapshot which
	// ones were plan cards so the per-id close emits permission_resolved (the
	// card face iOS actually has) instead of the user_input resolution.
	planQIDs := make([]string, 0, len(batch.questionIDs))
	if batch != nil {
		for _, qid := range batch.questionIDs {
			if _, isPlan := a.approvals.planReviews[qid]; isPlan {
				delete(a.approvals.planReviews, qid)
				planQIDs = append(planQIDs, qid)
			}
		}
	}
	a.approvals.mu.Unlock()
	if batch == nil {
		return // settled for a batch never surfaced here
	}
	sess, _ := a.bindings.get(batch.sessionID)
	status := core.UserInputStatusAnswered
	if strings.EqualFold(outcome, "cancelled") || strings.EqualFold(outcome, "rejected") {
		status = core.UserInputStatusRejected
	}
	turnID := a.sessionTurnID(batch.sessionID)
	planSet := map[string]bool{}
	for _, qid := range planQIDs {
		planSet[qid] = true
		a.emitPermissionEvent(batch.sessionID, sess, core.Event{
			Type:      core.EventPermissionResolved,
			SessionID: batch.sessionID,
			TurnID:    turnID,
			RequestID: qid,
			Content:   outcome,
		})
	}
	for _, qid := range batch.questionIDs {
		if planSet[qid] {
			continue // plan-surfaced: no user_input face was ever emitted
		}
		a.emitPermissionEvent(batch.sessionID, sess, core.Event{
			Type:      core.EventUserInputResolved,
			SessionID: batch.sessionID,
			TurnID:    turnID,
			ItemID:    qid,
			UserInput: &core.UserInputInteraction{
				InteractionID:    qid,
				Status:           status,
				ResolutionSource: "backend",
			},
		})
		a.emitPermissionEvent(batch.sessionID, sess, core.Event{
			Type:       core.EventQuestionResolved,
			SessionID:  batch.sessionID,
			QuestionID: qid,
			Content:    outcome,
			ThreadID:   batch.sessionID,
		})
	}
	slog.Info("dsh-web: question batch settled",
		"sessionPrefix", shortLog(batch.sessionID), "questions", len(batch.questionIDs), "outcome", outcome)
}

// closePendingInteraction settles one surfaced waterfall after a Host cancel
// frame (withdrawn or claimed elsewhere — first-writer-wins).
func (a *Agent) closePendingInteraction(eventID, outcome string) {
	if eventID == "" {
		return
	}
	a.approvalsInit()
	a.approvals.mu.Lock()
	pending := a.approvals.approvals[eventID]
	if pending != nil {
		delete(a.approvals.approvals, eventID)
	}
	a.approvals.mu.Unlock()
	if pending != nil {
		sess, _ := a.bindings.get(pending.sessionID)
		a.emitPermissionEvent(pending.sessionID, sess, core.Event{
			Type:      core.EventPermissionResolved,
			SessionID: pending.sessionID,
			RequestID: eventID,
			Content:   "deny", // outcome unknown: withdrawn/claimed — never claim allow
		})
		slog.Info("dsh-web: approval withdrawn/answered elsewhere",
			"sessionPrefix", shortLog(pending.sessionID), "approval", shortLog(eventID))
		return
	}
	a.settleBatch(eventID, outcome)
}

// dropAllPendingInteractions settles every surfaced waterfall at a mux
// generation loss: the dead generation's clientId orphans their responds.
func (a *Agent) dropAllPendingInteractions() {
	a.approvalsInit()
	a.approvals.mu.Lock()
	approvalIDs := make([]string, 0, len(a.approvals.approvals))
	for id := range a.approvals.approvals {
		approvalIDs = append(approvalIDs, id)
	}
	batchIDs := make([]string, 0, len(a.approvals.batches))
	for id := range a.approvals.batches {
		batchIDs = append(batchIDs, id)
	}
	a.approvals.mu.Unlock()
	for _, id := range approvalIDs {
		a.closePendingInteraction(id, "cancelled")
	}
	for _, id := range batchIDs {
		a.settleBatch(id, "cancelled")
	}
	if len(approvalIDs)+len(batchIDs) > 0 {
		slog.Info("dsh-web: settled pending interactions after stream generation loss",
			"approvals", len(approvalIDs), "batches", len(batchIDs))
	}
}
