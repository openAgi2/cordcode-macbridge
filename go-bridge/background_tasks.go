package gobridge

// background_tasks.go — Phase 4 read-only background-task center (roadmap §3.2/§3.3,
// docs/protocol/bridge-v1.md「Background Tasks」).
//
// Two real sources, one per backend family:
//   - Claude Code: the SAME sidechain files B4 hydrates (subagents/agent-*.meta.json
//     + .jsonl). The status base (running/failed/completed) is derived by literally
//     calling buildSidechainAgentBlocks — the B4 reducer walk — so the summary and
//     the projection part share one derivation (guardrail C1). `cancelled` is a
//     summary-layer signal from the meta's stoppedByUser flag (2/159 real samples;
//     B4 has no cancelled concept, so this cannot contradict it).
//   - dsh-web (and any future backend): core.BackgroundTaskProvider on the agent —
//     official session.list subagent rows, never re-parsed by iOS.
//
// Phase 4 is read-only: no cancel/retry/clear (Phase 5), no changed events yet.

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// claudeBackgroundTasks enumerates every sidechain subagent under projectsDir as a
// read-only task summary. A missing projects dir returns an empty list (no error) —
// "no tasks" is the honest state for machines without Claude sidechains.
func claudeBackgroundTasks(projectsDir string) ([]core.BackgroundTask, error) {
	type located struct {
		meta      claudeSidechainMeta
		dir       string
		jsonlPath string
		modTime   time.Time
	}
	var found []located
	err := filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || d.Name() != "subagents" {
			return nil
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil // fail-open per sidechain §5
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(path, name))
			if err != nil {
				continue
			}
			var m claudeSidechainMeta
			if err := json.Unmarshal(raw, &m); err != nil {
				continue
			}
			m.agentID = strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
			info, err := e.Info()
			if err != nil {
				continue
			}
			jsonl := filepath.Join(path, "agent-"+m.agentID+".jsonl")
			found = append(found, located{
				meta:      m,
				dir:       path,
				jsonlPath: jsonl,
				modTime:   info.ModTime(),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	tasks := make([]core.BackgroundTask, 0, len(found))
	for _, f := range found {
		// Same reducer walk as B4 (single status derivation, C1).
		_, baseStatus := buildSidechainAgentBlocks(context.Background(), f.jsonlPath)
		status := baseStatus
		if f.meta.StoppedByUser {
			status = "cancelled"
		}
		toolUses := claudeSidechainToolUseCount(f.jsonlPath)
		rootSession := filepath.Base(filepath.Dir(f.dir))
		_, jsonlErr := os.Stat(f.jsonlPath)
		tasks = append(tasks, core.BackgroundTask{
			TaskID:              f.meta.agentID,
			BackendID:           "claudecode",
			RootSessionID:       rootSession,
			ParentTaskID:        f.meta.ParentAgentID,
			AgentID:             f.meta.agentID,
			Title:               strings.TrimSpace(f.meta.Description),
			AgentName:           strings.TrimSpace(f.meta.AgentType),
			Status:              status,
			StartedAt:           f.modTime,
			UpdatedAt:           f.modTime,
			ToolUseCount:        toolUses,
			TranscriptAvailable: jsonlErr == nil,
		})
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt) })
	return tasks, nil
}

// claudeSidechainToolUseCount counts tool_use rows in the sidechain JSONL — a real
// count from the transcript, never an estimate.
func claudeSidechainToolUseCount(jsonlPath string) int64 {
	raw, err := os.ReadFile(jsonlPath)
	if err != nil {
		return 0
	}
	return int64(strings.Count(string(raw), `"type":"tool_use"`)) +
		int64(strings.Count(string(raw), `"type": "tool_use"`))
}

// claudeBackgroundTaskDetail serves background_tasks.get for claudecode: the task
// row plus instruction (meta description) and nested depth≥2 children.
func claudeBackgroundTaskDetail(projectsDir, taskID string) (*core.BackgroundTaskDetail, error) {
	tasks, err := claudeBackgroundTasks(projectsDir)
	if err != nil {
		return nil, err
	}
	var task *core.BackgroundTask
	for i := range tasks {
		if tasks[i].TaskID == taskID {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		return nil, os.ErrNotExist
	}
	var nested []core.BackgroundTask
	for _, t := range tasks {
		if t.ParentTaskID == taskID {
			nested = append(nested, t)
		}
	}
	return &core.BackgroundTaskDetail{
		Task:        *task,
		Instruction: task.Title,
		NestedTasks: nested,
	}, nil
}

// backgroundTaskToWire maps a summary to the bridge shape. Unknown numerics are
// OMITTED (never 0) — iOS must not render unknown as zero.
func backgroundTaskToWire(t core.BackgroundTask) map[string]any {
	wire := map[string]any{
		"taskId":        t.TaskID,
		"backendId":     t.BackendID,
		"rootSessionId": t.RootSessionID,
		"title":         t.Title,
		"status":        t.Status,
		"updatedAt":     t.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if t.ParentTaskID != "" {
		wire["parentTaskId"] = t.ParentTaskID
	}
	if t.AgentID != "" {
		wire["agentId"] = t.AgentID
	}
	if t.AgentName != "" {
		wire["agentName"] = t.AgentName
	}
	if !t.StartedAt.IsZero() {
		wire["startedAt"] = t.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !t.FinishedAt.IsZero() {
		wire["finishedAt"] = t.FinishedAt.UTC().Format(time.RFC3339Nano)
		wire["durationMillis"] = t.FinishedAt.Sub(t.StartedAt).Milliseconds()
	}
	if t.TokenCount > 0 {
		wire["tokenCount"] = t.TokenCount
	}
	if t.ToolUseCount > 0 {
		wire["toolUseCount"] = t.ToolUseCount
	}
	if t.Error != "" {
		wire["error"] = t.Error
	}
	wire["transcriptAvailable"] = t.TranscriptAvailable
	return wire
}

func (h *Handlers) handleBackgroundTasksList(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		Directory string `json:"directory"`
	}
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	var tasks []core.BackgroundTask
	switch {
	case agent.Name() == "claudecode":
		dir := strings.TrimSpace(params.Directory)
		projectsDir := h.claudeProjectsRootForBackgroundTasks(dir)
		var err error
		tasks, err = claudeBackgroundTasks(projectsDir)
		if err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
			return
		}
	default:
		provider, ok := agent.(core.BackgroundTaskProvider)
		if !ok {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not expose background tasks"})
			return
		}
		var err error
		tasks, err = provider.ListBackgroundTasks(context.Background())
		if err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
			return
		}
	}
	wire := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		wire = append(wire, backgroundTaskToWire(t))
	}
	conn.SendResult(msg.RequestID, map[string]any{"tasks": wire}, nil)
}

func (h *Handlers) handleBackgroundTasksGet(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		TaskID string `json:"taskId"`
	}
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.TaskID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "taskId required"})
		return
	}
	var detail *core.BackgroundTaskDetail
	switch {
	case agent.Name() == "claudecode":
		var err error
		detail, err = claudeBackgroundTaskDetail(h.claudeProjectsRootForBackgroundTasks(""), params.TaskID)
		if err != nil {
			if os.IsNotExist(err) {
				conn.SendResult(msg.RequestID, nil, &WireError{Code: "task_not_found", Message: "no such background task"})
				return
			}
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "get_failed", Message: err.Error()})
			return
		}
	default:
		reader, ok := agent.(core.BackgroundTaskDetailReader)
		if !ok {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not expose background task details"})
			return
		}
		var err error
		detail, err = reader.GetBackgroundTaskDetail(context.Background(), params.TaskID)
		if err != nil {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "get_failed", Message: err.Error()})
			return
		}
	}
	nested := make([]map[string]any, 0, len(detail.NestedTasks))
	for _, n := range detail.NestedTasks {
		nested = append(nested, backgroundTaskToWire(n))
	}
	conn.SendResult(msg.RequestID, map[string]any{
		"task":       backgroundTaskToWire(detail.Task),
		"instruction": detail.Instruction,
		"nestedTasks": nested,
		"capabilities": map[string]bool{
			"cancel": detail.CanCancel,
			"retry":  detail.CanRetry,
		},
	}, nil)
}

// publishBackgroundTasksChanged emits the Phase 5 invalidate notification. The
// event carries NO task data — clients re-list from the authoritative RPC (no
// second truth source rides the event).
func (h *Handlers) publishBackgroundTasksChanged(backendID string, catalogGeneration uint64) {
	if !h.broadcaster.HasConnections() {
		return
	}
	if _, err := h.eventPublisher.PublishControlPlane(LogicalEvent{
		BackendID:         backendID,
		Event:             "background_tasks_changed",
		Data:              map[string]interface{}{"backendId": backendID},
		Broadcast:         true,
		CatalogGeneration: catalogGeneration,
	}); err != nil {
		slog.Error("go-bridge: background_tasks_changed publish rejected",
			"backend", backendID, "error", err.Error())
	}
}

// handleBackgroundTasksCancel routes the Phase 5 capability-gated cancel. Only
// backends with a REAL cancellation surface implement core.BackgroundTaskCanceller
// (dsh-web: official session.cancel). Claude sidechains have no bridge-owned
// cancel path — the capability stays absent there and the RPC answers
// not_supported instead of pretending.
func (h *Handlers) handleBackgroundTasksCancel(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		TaskID string `json:"taskId"`
	}
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if strings.TrimSpace(params.TaskID) == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "taskId required"})
		return
	}
	canceller, ok := agent.(core.BackgroundTaskCanceller)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not support background task cancel"})
		return
	}
	if err := canceller.CancelBackgroundTask(context.Background(), params.TaskID); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "cancel_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]any{"cancelled": true}, nil)
}

// claudeProjectsRootForBackgroundTasks resolves the Claude projects root for the
// task registry. Claude sidechains live under ~/.claude/projects across ALL
// projects — the task center is cross-session by design (roadmap §3.1), so the
// request directory selects nothing here; the default root is authoritative.
func (h *Handlers) claudeProjectsRootForBackgroundTasks(_ string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".claude", "projects")
}
