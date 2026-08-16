package dshweb

// Approval/question pipeline (design §4.3.4, M2 一期必接 — without it an
// iOS-initiated turn hangs forever at the first ask-policy tool, violating
// fail-visibly).
//
// Approval flow: approval/requested → core permission_request. Bound sessions
// emit on the session channel; unbound (Mac-initiated) sessions emit on the
// agent passive channel so an observing iPhone can also approve. First writer
// wins: either side's allow/deny closes both UIs via approval/resolved.
// iOS answers → /api/respond {sessionId, approvalId, outcome} where
// allow→allowed-once / deny→rejected (the official outcome set is binary;
// iOS's always-variants already collapse to allow/deny on the wire, R3-2).
// approval/resolved closes the pending entry (first-writer-wins) and emits
// permission_resolved so the projection drops requiresPermissionConfirmation.
//
// Question flow (R2-1/R3-1/S-1/S-2/S-3): dsh asks WHOLE BATCHES (one ask,
// many questions, one answer). Each question carries its own dsh id
// (events.schema.ts) — per-question ids ride the bridge wire so iOS's
// replace-by-id upsert keeps every question visible and answerable. The mux
// frame's rpcId is dshweb-internal batch state only, never on the wire.
// Answers accumulate per question id (later answer overwrites — S-3); when
// the batch is complete ONE /api/respond posts {answers:[{id,selected,custom?}]}
// keyed by question id. NOTHING is synthesized as resolved before the host's
// batch question/resolved frame arrives (中间态如实 — a per-question synthetic
// resolution would be a lie if another question gets rejected); that frame
// carries no per-question content, so the batch state expands into N
// question_resolved events (S-1). Reject cancels the WHOLE batch through the
// respond ERROR branch (ok:false, code "cancelled") — asymmetric with
// approvals by design. Reconnect replays still-pending frames (same rpcId):
// re-emitting question events is idempotent on iOS (same ids), and a missing
// batch is rebuilt from the replay (S-2). Batches answered on the web during
// a disconnect window are NOT replayed — those iOS pending steps settle via
// the session's cold reload, same as every other transient question backend.

import (
	"context"
	"encoding/json"
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

// approvalsState is the agent-level pending registry (mux is agent-scoped).
type approvalsState struct {
	mu sync.Mutex
	// approvals: approvalId → pending approval (surfaced ones only).
	approvals map[string]*pendingApproval
	// batches: frame rpcId → pending question batch.
	batches map[string]*pendingQuestionBatch
	// questionOwner: question id → batch rpcId (answer routing).
	questionOwner map[string]string
}

type pendingApproval struct {
	rpcID      string
	sessionID  string
	approvalID string
	toolName   string
}

// pendingQuestionBatch is one dsh ask batch awaiting its complete answer.
type pendingQuestionBatch struct {
	rpcID     string // mux frame envelope rpcId — the respond echo key
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
	return a.respondApproval(ctx, sessionID, requestID, result)
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

// ── mux frame entries ───────────────────────────────────────────────────────

// handleApprovalFrame dispatches approval/requested|resolved.
func (a *Agent) handleApprovalFrame(ctx context.Context, rpcID, method string, payload json.RawMessage) {
	a.approvalsInit()
	switch method {
	case "approval/requested":
		var f struct {
			SessionID  string `json:"sessionId"`
			ApprovalID string `json:"approvalId"`
			ToolName   string `json:"toolName"`
			CallID     string `json:"callId"`
			Reason     string `json:"reason"`
		}
		if err := json.Unmarshal(payload, &f); err != nil || f.ApprovalID == "" {
			slog.Warn("dsh-web: approval/requested unparsable", "error", err)
			return
		}
		a.approvals.mu.Lock()
		a.approvals.approvals[f.ApprovalID] = &pendingApproval{
			rpcID: rpcID, sessionID: f.SessionID, approvalID: f.ApprovalID, toolName: f.ToolName,
		}
		a.approvals.mu.Unlock()
		sess, _ := a.bindings.get(f.SessionID)
		// The dsh approval frame carries no tool input (events.schema.ts) —
		// the request surfaces with the tool name; nothing is invented.
		raw := map[string]any{}
		if f.Reason != "" {
			raw["reason"] = f.Reason
		}
		if f.CallID != "" {
			raw["callId"] = f.CallID
		}
		var toolInputRaw map[string]any
		if len(raw) > 0 {
			toolInputRaw = raw
		}
		a.emitPermissionEvent(f.SessionID, sess, core.Event{
			Type:         core.EventPermissionRequest,
			SessionID:    f.SessionID,
			RequestID:    f.ApprovalID,
			ToolName:     f.ToolName,
			Content:      f.Reason,
			ToolInput:    f.Reason,
			ToolInputRaw: toolInputRaw,
		})
		slog.Info("dsh-web: approval surfaced", "sessionPrefix", shortLog(f.SessionID), "tool", f.ToolName, "bound", sess != nil)

	case "approval/resolved":
		var f struct {
			SessionID  string `json:"sessionId"`
			ApprovalID string `json:"approvalId"`
			Outcome    string `json:"outcome"` // allowed-once|rejected|cancelled|unavailable
		}
		if err := json.Unmarshal(payload, &f); err != nil {
			return
		}
		a.approvals.mu.Lock()
		delete(a.approvals.approvals, f.ApprovalID)
		a.approvals.mu.Unlock()
		// Projection SoT now owns the permission card. Closing the pending
		// entry is not enough — SSV2 remaps from the projection, so the
		// host-resolved outcome must clear requiresPermissionConfirmation.
		// Idempotent with go-bridge resolve_permission → permission_resolved.
		behavior := "deny"
		if f.Outcome == "allowed-once" {
			behavior = "allow"
		}
		sess, _ := a.bindings.get(f.SessionID)
		a.emitPermissionEvent(f.SessionID, sess, core.Event{
			Type:      core.EventPermissionResolved,
			SessionID: f.SessionID,
			RequestID: f.ApprovalID,
			Content:   behavior,
		})
		slog.Info("dsh-web: approval resolved", "sessionPrefix", shortLog(f.SessionID), "outcome", f.Outcome)
	}
}

// handleQuestionFrame dispatches question/requested|resolved.
func (a *Agent) handleQuestionFrame(ctx context.Context, rpcID, method string, payload json.RawMessage) {
	a.approvalsInit()
	switch method {
	case "question/requested":
		var f struct {
			SessionID string `json:"sessionId"`
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
			} `json:"questions"`
		}
		if err := json.Unmarshal(payload, &f); err != nil || len(f.Questions) == 0 {
			slog.Warn("dsh-web: question/requested unparsable or empty", "error", err)
			return
		}
		a.approvals.mu.Lock()
		if _, exists := a.approvals.batches[rpcID]; !exists {
			fresh := &pendingQuestionBatch{
				rpcID:       rpcID,
				sessionID:   f.SessionID,
				answers:     map[string]questionAnswer{},
				questionIDs: make([]string, 0, len(f.Questions)),
			}
			for _, q := range f.Questions {
				fresh.questionIDs = append(fresh.questionIDs, q.ID)
				a.approvals.questionOwner[q.ID] = rpcID
			}
			a.approvals.batches[rpcID] = fresh
		} // existing batch = reconnect replay (S-2): re-emit only
		a.approvals.mu.Unlock()

		sess, _ := a.bindings.get(f.SessionID)
		// Per-question events, each with its own dsh id (R3-1). Bound sessions
		// use the session channel; Mac-initiated (unbound) use the passive
		// channel so an observing iPhone can answer too.
		//
		// Canonical writer is user_input_requested (SSV2 projection / UserInputDock).
		// question_asked is the one-way legacy presentation only — EventPublisher
		// will not ingest it, so emitting it alone leaves iPhone with no card
		// (owner 2026-08-16: Mac 多选框出现，iPhone 没有).
		for _, q := range f.Questions {
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
			a.emitPermissionEvent(f.SessionID, sess, core.Event{
				Type:      core.EventUserInputRequested,
				SessionID: f.SessionID,
				TurnID:    a.sessionTurnID(f.SessionID),
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
			a.emitPermissionEvent(f.SessionID, sess, core.Event{
				Type:         core.EventQuestionAsked,
				SessionID:    f.SessionID,
				QuestionID:   q.ID,
				QuestionText: text,
				QuestionOpts: opts,
				Required:     true,
				ThreadID:     f.SessionID,
			})
		}
		slog.Info("dsh-web: question batch surfaced",
			"sessionPrefix", shortLog(f.SessionID), "questions", len(f.Questions), "batch", shortLog(rpcID), "bound", sess != nil)

	case "question/resolved":
		var f struct {
			SessionID     string `json:"sessionId"`
			QuestionRPCID string `json:"questionRpcId"`
			Outcome       string `json:"outcome"` // answered|cancelled
		}
		if err := json.Unmarshal(payload, &f); err != nil {
			return
		}
		a.approvals.mu.Lock()
		batch := a.approvals.batches[f.QuestionRPCID]
		if batch != nil {
			delete(a.approvals.batches, f.QuestionRPCID)
			for _, qid := range batch.questionIDs {
				delete(a.approvals.questionOwner, qid)
			}
		}
		a.approvals.mu.Unlock()
		if batch == nil {
			return // resolved for a batch never surfaced here
		}
		// S-1: the frame has no per-question content — the batch state expands
		// into N per-id resolved events so each iOS pending card closes.
		sess, _ := a.bindings.get(f.SessionID)
		status := core.UserInputStatusAnswered
		if strings.EqualFold(f.Outcome, "cancelled") || strings.EqualFold(f.Outcome, "rejected") {
			status = core.UserInputStatusRejected
		}
		turnID := a.sessionTurnID(f.SessionID)
		for _, qid := range batch.questionIDs {
			a.emitPermissionEvent(f.SessionID, sess, core.Event{
				Type:      core.EventUserInputResolved,
				SessionID: f.SessionID,
				TurnID:    turnID,
				ItemID:    qid,
				UserInput: &core.UserInputInteraction{
					InteractionID:    qid,
					Status:           status,
					ResolutionSource: "backend",
				},
			})
			a.emitPermissionEvent(f.SessionID, sess, core.Event{
				Type:       core.EventQuestionResolved,
				SessionID:  f.SessionID,
				QuestionID: qid,
				Content:    f.Outcome,
				ThreadID:   f.SessionID,
			})
		}
		slog.Info("dsh-web: question batch resolved",
			"sessionPrefix", shortLog(f.SessionID), "questions", len(batch.questionIDs), "outcome", f.Outcome)
	}
}

// ── responders (bridge handler entry) ──────────────────────────────────────

// respondApproval maps the iOS permission decision onto the official payload.
func (a *Agent) respondApproval(ctx context.Context, sessionID, requestID string, result core.PermissionResult) error {
	client, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	outcome := "rejected"
	if result.Behavior == "allow" {
		outcome = "allowed-once"
	}
	// The respond echoes the FRAME envelope's rpcId (the host's pending table
	// keys by it, api-proxy.ts:1410) — looked up from the surfaced entry.
	a.approvalsInit()
	a.approvals.mu.Lock()
	echoKey := requestID
	if pending := a.approvals.approvals[requestID]; pending != nil {
		echoKey = pending.rpcID
		delete(a.approvals.approvals, requestID)
	}
	a.approvals.mu.Unlock()

	value := map[string]any{
		"sessionId":  sessionID,
		"approvalId": requestID,
		"outcome":    outcome,
	}
	accepted, err := client.Respond(ctx, echoKey, true, value, nil)
	if err != nil {
		return err
	}
	if !accepted {
		// First-writer-wins: the web already answered; the resolved frame (or
		// its prior arrival) settles the state. The turn's continuation is
		// the visible outcome — not an error for the iOS submit.
		slog.Info("dsh-web: approval respond not-pending (answered elsewhere)", "approval", shortLog(requestID))
	}
	return nil
}

// respondQuestion accumulates one per-question answer; the batch answers ONCE
// when every question carries an answer (R3-1/S-3).
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
	value := map[string]any{
		"sessionId": batch.sessionID,
		"answer":    map[string]any{"answers": answers},
	}
	accepted, err := client.Respond(ctx, batch.rpcID, true, value, nil)
	if err != nil {
		return err
	}
	if !accepted {
		slog.Info("dsh-web: question respond not-pending (answered/cancelled elsewhere)",
			"batch", shortLog(batch.rpcID))
	}
	// 中间态如实: the batch stays registered until the host's
	// question/resolved frame expands per-question resolutions (S-1).
	return nil
}

// rejectQuestion cancels the WHOLE batch via the respond error branch
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
	if _, err := client.Respond(ctx, batch.rpcID, false, nil, nil); err != nil {
		return err
	}
	slog.Info("dsh-web: question batch cancelled by reject", "batch", shortLog(rpcID),
		"viaQuestion", shortLog(questionID), "questions", len(batch.questionIDs))
	return nil
}
