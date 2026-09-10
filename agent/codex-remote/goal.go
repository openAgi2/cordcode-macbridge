package codexremote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func (a *Agent) GetSessionGoal(ctx context.Context, sessionID string) (core.SessionGoalSnapshot, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return core.SessionGoalSnapshot{}, fmt.Errorf("codex-remote: goal get: empty session id")
	}
	cl, codec, err := a.goalClient()
	if err != nil {
		return core.SessionGoalSnapshot{}, err
	}
	return a.getSessionGoalOn(ctx, cl, codec, sessionID)
}

// getSessionGoalOn binds the read and its generation fence to one connection
// epoch. Attach/reconnect hydration uses this path so a response from a
// replaced Remote stream can never seed the new epoch's projection.
func (a *Agent) getSessionGoalOn(ctx context.Context, cl *Client, codec *LiveCodec, sessionID string) (core.SessionGoalSnapshot, error) {
	baseVersion := codec.GoalVersion(sessionID)
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/goal/get", map[string]any{"threadId": sessionID})
	if err != nil {
		return core.SessionGoalSnapshot{}, err
	}
	if rpcErr != nil {
		return core.SessionGoalSnapshot{}, rpcErr
	}
	snapshot, err := decodeGoalResponse(raw, sessionID)
	if err != nil {
		return core.SessionGoalSnapshot{}, fmt.Errorf("codex-remote: thread/goal/get decode: %w", err)
	}
	current, currentEpoch := a.applyGoalResponse(cl, codec, sessionID, snapshot, baseVersion)
	if !currentEpoch {
		return core.SessionGoalSnapshot{}, fmt.Errorf("codex-remote: connection changed during thread/goal/get")
	}
	return current, nil
}

func (a *Agent) SetSessionGoal(ctx context.Context, sessionID string, update core.SessionGoalUpdate) (core.SessionGoalRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return core.SessionGoalRecord{}, fmt.Errorf("codex-remote: goal set: empty session id")
	}
	params := map[string]any{"threadId": sessionID}
	if update.Objective != nil {
		objective := strings.TrimSpace(*update.Objective)
		if objective == "" {
			return core.SessionGoalRecord{}, fmt.Errorf("codex-remote: goal set: objective must not be empty")
		}
		params["objective"] = objective
	}
	if update.Status != nil {
		status := strings.TrimSpace(*update.Status)
		if !validGoalStatus(status) {
			return core.SessionGoalRecord{}, fmt.Errorf("codex-remote: goal set: unsupported status %q", status)
		}
		params["status"] = status
	}
	if update.TokenBudgetSet {
		params["tokenBudget"] = update.TokenBudget
	}
	if len(params) == 1 {
		return core.SessionGoalRecord{}, fmt.Errorf("codex-remote: goal set: no fields to update")
	}
	cl, codec, err := a.goalClient()
	if err != nil {
		return core.SessionGoalRecord{}, err
	}
	baseVersion := codec.GoalVersion(sessionID)
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/goal/set", params)
	if err != nil {
		return core.SessionGoalRecord{}, err
	}
	if rpcErr != nil {
		return core.SessionGoalRecord{}, rpcErr
	}
	snapshot, err := decodeGoalResponse(raw, sessionID)
	if err != nil || snapshot.Goal == nil {
		if err == nil {
			err = fmt.Errorf("response omitted goal")
		}
		return core.SessionGoalRecord{}, fmt.Errorf("codex-remote: thread/goal/set decode: %w", err)
	}
	responseGoal := *cloneGoalSnapshot(snapshot).Goal
	current, currentEpoch := a.applyGoalResponse(cl, codec, sessionID, snapshot, baseVersion)
	if !currentEpoch {
		return core.SessionGoalRecord{}, fmt.Errorf("codex-remote: connection changed during thread/goal/set")
	}
	if current.Goal != nil {
		return *current.Goal, nil
	}
	return responseGoal, nil
}

func (a *Agent) ClearSessionGoal(ctx context.Context, sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, fmt.Errorf("codex-remote: goal clear: empty session id")
	}
	cl, codec, err := a.goalClient()
	if err != nil {
		return false, err
	}
	baseVersion := codec.GoalVersion(sessionID)
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/goal/clear", map[string]any{"threadId": sessionID})
	if err != nil {
		return false, err
	}
	if rpcErr != nil {
		return false, rpcErr
	}
	var response struct {
		Cleared *bool `json:"cleared"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || response.Cleared == nil {
		if err == nil {
			err = fmt.Errorf("response omitted cleared")
		}
		return false, fmt.Errorf("codex-remote: thread/goal/clear decode: %w", err)
	}
	// A false response does not authorize removing a locally known record. The
	// projection clear contract is deliberately narrower: only cleared:true or
	// the official cleared notification removes the goal. Either response is
	// still protected by the generation fence.
	if *response.Cleared {
		if _, currentEpoch := a.applyGoalResponse(cl, codec, sessionID, core.SessionGoalSnapshot{}, baseVersion); !currentEpoch {
			return false, fmt.Errorf("codex-remote: connection changed during thread/goal/clear")
		}
	} else if !a.goalClientCurrent(cl, codec) {
		return false, fmt.Errorf("codex-remote: connection changed during thread/goal/clear")
	}
	return *response.Cleared, nil
}

func (a *Agent) refreshSessionGoalAfterAttach(ctx context.Context, cl *Client, threadID string) error {
	if !a.SessionGoalReady() {
		return nil
	}
	a.mu.Lock()
	codec := a.codec
	current := a.client == cl
	a.mu.Unlock()
	if !current || codec == nil {
		return ErrNotConfigured
	}
	_, err := a.getSessionGoalOn(ctx, cl, codec, threadID)
	return err
}

func (a *Agent) goalClient() (*Client, *LiveCodec, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || a.codec == nil {
		return nil, nil, ErrNotConfigured
	}
	return a.client, a.codec, nil
}

func (a *Agent) goalClientCurrent(cl *Client, codec *LiveCodec) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client == cl && a.codec == codec
}

func (a *Agent) applyGoalResponse(cl *Client, codec *LiveCodec, threadID string, snapshot core.SessionGoalSnapshot, baseVersion uint64) (core.SessionGoalSnapshot, bool) {
	a.mu.Lock()
	if a.client != cl || a.codec != codec {
		a.mu.Unlock()
		return core.SessionGoalSnapshot{}, false
	}
	event, applied := codec.applyGoalSnapshot(threadID, snapshot, &baseVersion)
	current, exists := codec.CurrentGoal(threadID)
	a.mu.Unlock()
	if !exists {
		return core.SessionGoalSnapshot{}, false
	}
	if applied {
		a.dispatchForClient(cl, event)
	}
	return current, true
}

func (c *LiveCodec) decodeThreadGoalUpdated(n Notification) []core.Event {
	var params struct {
		ThreadID string          `json:"threadId"`
		Goal     json.RawMessage `json:"goal"`
	}
	if json.Unmarshal(n.Params, &params) != nil || strings.TrimSpace(params.ThreadID) == "" {
		return nil
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	goal, err := decodeGoal(params.Goal)
	if err != nil || goal.ThreadID != params.ThreadID {
		return nil
	}
	event, applied := c.applyGoalSnapshot(params.ThreadID, core.SessionGoalSnapshot{Goal: &goal}, nil)
	if !applied {
		return nil
	}
	return []core.Event{event}
}

func (c *LiveCodec) decodeThreadGoalCleared(n Notification) []core.Event {
	var params struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(n.Params, &params) != nil || strings.TrimSpace(params.ThreadID) == "" {
		return nil
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	event, applied := c.applyGoalSnapshot(params.ThreadID, core.SessionGoalSnapshot{}, nil)
	if !applied {
		return nil
	}
	return []core.Event{event}
}

func decodeGoalResponse(raw json.RawMessage, expectedThreadID string) (core.SessionGoalSnapshot, error) {
	var response struct {
		Goal json.RawMessage `json:"goal"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return core.SessionGoalSnapshot{}, err
	}
	if len(response.Goal) == 0 {
		return core.SessionGoalSnapshot{}, fmt.Errorf("response omitted goal")
	}
	if bytes.Equal(bytes.TrimSpace(response.Goal), []byte("null")) {
		return core.SessionGoalSnapshot{}, nil
	}
	goal, err := decodeGoal(response.Goal)
	if err != nil {
		return core.SessionGoalSnapshot{}, err
	}
	if goal.ThreadID != expectedThreadID {
		return core.SessionGoalSnapshot{}, fmt.Errorf("thread identity mismatch: requested %q, received %q", expectedThreadID, goal.ThreadID)
	}
	return core.SessionGoalSnapshot{Goal: &goal}, nil
}

func decodeGoal(raw json.RawMessage) (core.SessionGoalRecord, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("goal is not an object")
		}
		return core.SessionGoalRecord{}, err
	}
	for _, field := range []string{"threadId", "objective", "status", "tokenBudget", "tokensUsed", "timeUsedSeconds", "createdAt", "updatedAt"} {
		if _, ok := fields[field]; !ok {
			return core.SessionGoalRecord{}, fmt.Errorf("goal omitted %s", field)
		}
	}
	for _, field := range []string{"tokensUsed", "timeUsedSeconds", "createdAt", "updatedAt"} {
		if bytes.Equal(bytes.TrimSpace(fields[field]), []byte("null")) {
			return core.SessionGoalRecord{}, fmt.Errorf("goal field %s must be an integer", field)
		}
	}
	var goal core.SessionGoalRecord
	if err := json.Unmarshal(raw, &goal); err != nil {
		return core.SessionGoalRecord{}, err
	}
	goal.ThreadID = strings.TrimSpace(goal.ThreadID)
	if goal.ThreadID == "" || strings.TrimSpace(goal.Objective) == "" || !validGoalStatus(goal.Status) {
		return core.SessionGoalRecord{}, fmt.Errorf("invalid thread goal identity or status")
	}
	return goal, nil
}

func validGoalStatus(status string) bool {
	switch status {
	case "active", "paused", "blocked", "usageLimited", "budgetLimited", "complete":
		return true
	default:
		return false
	}
}

var _ core.SessionGoalRecordController = (*Agent)(nil)
