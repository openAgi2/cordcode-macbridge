package codexremote

// Plan-mode follow-through for Codex Desktop (codex-remote).
//
// Official source @ /Users/jacklee/Projects/codex 50fffd5ed367aa99491d9ec58575626fce4e9dd4:
//   - Plan body = ThreadItem::Plan {type:"plan", id, text} plus item/plan/delta
//     (app-server-protocol v2/item.rs). Completed item is authoritative.
//   - "Implement this plan?" is TUI-only client orchestration
//     (tui/src/chatwidget/plan_implementation.rs). There is no wire approval
//     request. Yes → submit user message "Implement the plan." with Default
//     collaboration mode (plan_implementation.rs:13,36-41; input_flow.rs:258-293).
//   - Stay in Plan → no-op. Clear-context-and-implement starts a new thread
//     and is deliberately unsupported here.
//
// CordCode synthesizes a plan_review permission card at turn/completed when
// this turn produced a Plan item (mirroring maybe_prompt_plan_implementation
// in turn_runtime.rs:226-251). Answering the card performs the same client
// orchestration. Mac Desktop may still show its own dialog; the two are not
// first-answer-wins.

import (
	"context"
	"fmt"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// PLAN_IMPLEMENTATION_CODING_MESSAGE is the official TUI submit text
// (plan_implementation.rs:13). Do not localize or paraphrase.
const planImplementationCodingMessage = "Implement the plan."

const planKeepPlanningEmptyFeedback = "The user rejected the plan and asked to keep planning. No specific feedback was provided."

func (c *LiveCodec) rememberInFlightPlan(threadID, turnID, itemID, text string) {
	if threadID == "" || turnID == "" || itemID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inFlightPlan[threadID] = codexProposedPlan{
		threadID: threadID,
		turnID:   turnID,
		itemID:   itemID,
		text:     text,
	}
}

func (c *LiveCodec) takeAwaitingPlanLocked(threadID string) codexProposedPlan {
	for id, plan := range c.awaitingPlanReview {
		if plan.threadID == threadID {
			delete(c.awaitingPlanReview, id)
			return plan
		}
	}
	return codexProposedPlan{}
}

func (c *LiveCodec) takeAwaitingPlan(requestID string) (codexProposedPlan, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	plan, ok := c.awaitingPlanReview[requestID]
	if ok {
		delete(c.awaitingPlanReview, requestID)
	}
	return plan, ok
}

func codexPlanReviewEvent(plan codexProposedPlan) core.Event {
	title := planReviewTitle(plan.text)
	return core.Event{
		Type:              core.EventPermissionRequest,
		SessionID:         plan.threadID,
		ThreadID:          plan.threadID,
		TurnID:            plan.turnID,
		ItemID:            plan.itemID,
		RequestID:         plan.itemID,
		ToolName:          title,
		PermissionKind:    "plan_review",
		PermissionActions: []string{"approve", "requestChanges", "quit"},
		PlanReview: &core.PlanPayload{
			Content:       plan.text,
			ContentFormat: "markdown",
			Title:         title,
		},
	}
}

func planReviewTitle(planContent string) string {
	for _, line := range strings.Split(planContent, "\n") {
		t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if t == "" {
			continue
		}
		runes := []rune(t)
		if len(runes) > 80 {
			t = string(runes[:80])
		}
		return t
	}
	return "计划审批"
}

func (s *remoteSession) RespondPermission(requestID string, result core.PermissionResult) error {
	if s.agent == nil {
		return core.ErrNotSupported
	}
	return s.agent.RespondSessionPermission(context.Background(), s.threadID, requestID, result)
}

func (a *Agent) RespondSessionPermission(ctx context.Context, sessionID, requestID string, result core.PermissionResult) error {
	a.mu.Lock()
	codec := a.codec
	a.mu.Unlock()
	if codec == nil {
		return core.ErrNotSupported
	}
	plan, ok := codec.takeAwaitingPlan(requestID)
	if !ok {
		return core.ErrNotSupported
	}
	restore := func() {
		codec.mu.Lock()
		codec.awaitingPlanReview[requestID] = plan
		codec.mu.Unlock()
	}
	if plan.threadID != "" && sessionID != "" && plan.threadID != sessionID {
		restore()
		return fmt.Errorf("codex-remote: plan review %s is not for thread %s", requestID, sessionID)
	}
	action := result.PlanAction
	if action == "" {
		if result.Behavior == "allow" || result.Behavior == "always" {
			action = "approve"
		} else {
			action = "quit"
		}
	}
	var err error
	switch action {
	case "approve":
		err = a.startTurnWithCollaborationMode(ctx, plan.threadID, planImplementationCodingMessage, "default")
	case "requestChanges":
		text := strings.TrimSpace(result.Message)
		if text == "" {
			text = planKeepPlanningEmptyFeedback
		}
		err = a.startTurnWithCollaborationMode(ctx, plan.threadID, text, "plan")
	case "quit":
		err = a.updateThreadCollaborationMode(ctx, plan.threadID, "default")
	default:
		err = fmt.Errorf("codex-remote: unknown planAction %q", action)
	}
	if err != nil {
		restore()
	}
	return err
}

var _ core.SessionPermissionResponder = (*Agent)(nil)

func (a *Agent) startTurnWithCollaborationMode(ctx context.Context, threadID, text, modeKind string) error {
	a.mu.Lock()
	cl := a.client
	model := a.selectedModel
	if model == "" {
		model = a.defaultModel
	}
	a.mu.Unlock()
	if cl == nil {
		return ErrNotConfigured
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("codex-remote: empty turn input")
	}
	if model == "" {
		return fmt.Errorf("codex-remote: no official model for collaborationMode settings")
	}
	params := map[string]any{
		"threadId":          threadID,
		"input":             []map[string]any{{"type": "text", "text": text}},
		"collaborationMode": officialCollaborationMode(modeKind, model),
	}
	_, rpcErr, err := cl.RequestContext(ctx, "turn/start", params)
	if err != nil {
		return err
	}
	if rpcErr != nil {
		return rpcErr
	}
	return nil
}

func (a *Agent) updateThreadCollaborationMode(ctx context.Context, threadID, modeKind string) error {
	a.mu.Lock()
	cl := a.client
	model := a.selectedModel
	if model == "" {
		model = a.defaultModel
	}
	a.mu.Unlock()
	if cl == nil {
		return ErrNotConfigured
	}
	if model == "" {
		return fmt.Errorf("codex-remote: no official model for collaborationMode settings")
	}
	params := map[string]any{
		"threadId":          threadID,
		"collaborationMode": officialCollaborationMode(modeKind, model),
	}
	_, rpcErr, err := cl.RequestContext(ctx, "thread/settings/update", params)
	if err != nil {
		return err
	}
	if rpcErr != nil {
		return rpcErr
	}
	return nil
}

// officialCollaborationMode is the experimental turn/start and
// thread/settings/update payload (app-server-protocol v2 CollaborationMode).
// ModeKind serializes as "plan" / "default". Settings.model is required;
// developer_instructions null means "use built-in instructions for the mode"
// (app-server README collaborationMode/list).
func officialCollaborationMode(modeKind, model string) map[string]any {
	return map[string]any{
		"mode": modeKind,
		"settings": map[string]any{
			"model":                  model,
			"developer_instructions": nil,
		},
	}
}
