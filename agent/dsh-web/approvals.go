package dshweb

// Approval/question pipeline (design §4.3.4, M2 一期必接 — without it an
// iOS-initiated turn hangs forever at the first ask-policy tool, violating
// fail-visibly).
//
// Approval flow: approval/requested → core permission_request (surface rule:
// bridge registry hit = a live binding for that session — the ONLY judging
// criterion, R2-3; observation subscription alone is NOT a surface) → iOS
// answers → /api/respond {sessionId, approvalId, outcome} where
// allow→allowed-once / deny→rejected (the official outcome set is binary;
// iOS's always-variants already collapse to allow/deny on the wire, R3-2).
// approval/resolved closes the pending entry (first-writer-wins: web answering
// first settles the batch and the turn continuing closes iOS's prompt through
// its toolUseID lifecycle — no synthetic permission-resolved event exists).
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
		// Surface rule (R2-3): registry hit only. External sessions keep their
		// approvals on the web UI — "whoever is watching answers".
		sess, ok := a.bindings.get(f.SessionID)
		if !ok || sess == nil {
			slog.Debug("dsh-web: approval not surfaced (no bridge binding)",
				"sessionPrefix", shortLog(f.SessionID), "tool", f.ToolName)
			return
		}
		a.approvals.mu.Lock()
		a.approvals.approvals[f.ApprovalID] = &pendingApproval{
			rpcID: rpcID, sessionID: f.SessionID, approvalID: f.ApprovalID, toolName: f.ToolName,
		}
		a.approvals.mu.Unlock()
		// The dsh approval frame carries no tool input (events.schema.ts) —
		// the request surfaces with the tool name; nothing is invented.
		sess.emitControlCritical(core.Event{
			Type:      core.EventPermissionRequest,
			SessionID: f.SessionID,
			RequestID: f.ApprovalID,
			ToolName:  f.ToolName,
		})
		slog.Info("dsh-web: approval surfaced", "sessionPrefix", shortLog(f.SessionID), "tool", f.ToolName)

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
		_, ours := a.approvals.approvals[f.ApprovalID]
		delete(a.approvals.approvals, f.ApprovalID)
		a.approvals.mu.Unlock()
		if ours {
			// First-writer-wins close: if the web answered, the pending entry
			// settles here; iOS's prompt closes through the toolUseID
			// lifecycle as the turn continues (no permission_resolved wire
			// event exists — none is invented).
			slog.Info("dsh-web: approval resolved", "sessionPrefix", shortLog(f.SessionID), "outcome", f.Outcome)
		}
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
		// Surface rule (R2-3): registry hit only.
		sess, ok := a.bindings.get(f.SessionID)
		if !ok || sess == nil {
			slog.Debug("dsh-web: question batch not surfaced (no bridge binding)",
				"sessionPrefix", shortLog(f.SessionID))
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

		// Per-question events, each with its own dsh id (R3-1) — iOS's
		// replace-by-id upsert keeps every question visible and answerable.
		for _, q := range f.Questions {
			opts := make([]core.QuestionOption, 0, len(q.Options))
			for _, o := range q.Options {
				// dsh options have no ids: the label IS the identifier, echoed
				// verbatim in the answer's selected[] (user-questions types).
				opts = append(opts, core.QuestionOption{ID: o.Label, Label: o.Label, Description: o.Description})
			}
			text := q.Question
			if q.Header != "" {
				text = q.Header + "：" + q.Question
			}
			sess.emitControlCritical(core.Event{
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
			"sessionPrefix", shortLog(f.SessionID), "questions", len(f.Questions), "batch", shortLog(rpcID))

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
			return // resolved for a batch never surfaced here (web-only ask)
		}
		// S-1: the frame has no per-question content — the batch state expands
		// into N per-id resolved events so each iOS pending step closes.
		sess, ok := a.bindings.get(f.SessionID)
		if !ok || sess == nil {
			return
		}
		for _, qid := range batch.questionIDs {
			sess.emit(core.Event{
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
