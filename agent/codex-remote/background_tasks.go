package codexremote

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ListSessionBackgroundTasks derives the task center from the parent thread's
// real workflow records. create_thread contributes child identity/title and
// later wait_threads polls refresh that child's status in transcript order.
func (a *Agent) ListSessionBackgroundTasks(ctx context.Context, sessionID string) ([]core.BackgroundTask, error) {
	turns, err := a.GetTurnScopedRichHistory(ctx, sessionID, 0)
	if err != nil {
		return nil, err
	}
	return backgroundTasksFromTurns(sessionID, turns), nil
}

func backgroundTasksFromTurns(sessionID string, turns []core.TurnScopedHistoryTurn) []core.BackgroundTask {
	byID := make(map[string]core.BackgroundTask)
	var order []string
	for _, turn := range turns {
		observedAt := turn.CompletedAt
		if observedAt.IsZero() {
			observedAt = turn.StartedAt
		}
		for _, part := range turn.Parts {
			if stringValue(part["type"]) != "workflow" {
				continue
			}
			workflowName := strings.TrimSpace(stringValue(part["workflowName"]))
			for _, phase := range workflowPhaseMaps(part["workflowPhases"]) {
				for _, member := range workflowMemberMaps(phase["members"]) {
					childID := strings.TrimSpace(stringValue(member["childSessionId"]))
					if childID == "" {
						continue
					}
					status := backgroundStatusFromWorkflow(stringValue(member["status"]))
					current, exists := byID[childID]
					if !exists {
						label := strings.TrimSpace(stringValue(member["label"]))
						if strings.HasPrefix(label, "Agent-") || label == "" {
							label = workflowName
						}
						current = core.BackgroundTask{
							TaskID: childID, BackendID: BackendID, RootSessionID: sessionID,
							AgentID: childID, Title: label, Status: status,
							StartedAt: observedAt, UpdatedAt: observedAt, TranscriptAvailable: true,
						}
						order = append(order, childID)
					} else {
						current.Status = status
						if !observedAt.IsZero() {
							current.UpdatedAt = observedAt
						}
					}
					if status != "running" && status != "queued" && !observedAt.IsZero() {
						current.FinishedAt = observedAt
					}
					byID[childID] = current
				}
			}
		}
	}
	out := make([]core.BackgroundTask, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// GetBackgroundTaskDetail reads the child thread itself. Parent linkage and
// instruction remain list-derived; the detail surface never invents them when
// the independent child thread metadata does not carry that relation.
func (a *Agent) GetBackgroundTaskDetail(ctx context.Context, taskID string) (*core.BackgroundTaskDetail, error) {
	thread, err := a.readThreadMeta(ctx, taskID)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(thread.Preview)
	if thread.Name != nil && strings.TrimSpace(*thread.Name) != "" {
		title = strings.TrimSpace(*thread.Name)
	}
	status := "completed"
	switch thread.Status.Type {
	case remoteThreadStatusActive:
		status = "running"
	case remoteThreadStatusSystemError:
		status = "failed"
	}
	startedAt := unixRemoteTime(thread.CreatedAt)
	updatedAt := unixRemoteTime(thread.UpdatedAt)
	task := core.BackgroundTask{
		TaskID: taskID, BackendID: BackendID, AgentID: taskID, Title: title,
		Status: status, StartedAt: startedAt, UpdatedAt: updatedAt, TranscriptAvailable: true,
	}
	if status != "running" {
		task.FinishedAt = updatedAt
	}
	return &core.BackgroundTaskDetail{Task: task, Instruction: thread.Preview}, nil
}

func unixRemoteTime(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

func backgroundStatusFromWorkflow(status string) string {
	switch status {
	case core.WorkflowStatusCompleted:
		return "completed"
	case core.WorkflowStatusFailed:
		return "failed"
	case core.WorkflowStatusCancelled, core.WorkflowStatusInterrupted:
		return "cancelled"
	default:
		return "running"
	}
}

func workflowPhaseMaps(value any) []map[string]any {
	switch phases := value.(type) {
	case []map[string]any:
		return phases
	case []any:
		out := make([]map[string]any, 0, len(phases))
		for _, phase := range phases {
			if mapped, ok := phase.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func workflowMemberMaps(value any) []map[string]any { return workflowPhaseMaps(value) }

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

var (
	_ core.SessionBackgroundTaskProvider = (*Agent)(nil)
	_ core.BackgroundTaskDetailReader    = (*Agent)(nil)
)
