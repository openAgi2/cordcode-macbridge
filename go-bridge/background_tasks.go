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
	type workflowLocated struct {
		agent claudeWorkflowLocatedAgent
	}
	var workflowFound []workflowLocated
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
		// Workflow 布局（方案 §3.2.1）：subagents/workflows/wf_*/ 一层枚举——上游
		// 契约两层，未知更深结构 fail-open 跳过。
		workflowRuns, err := os.ReadDir(filepath.Join(path, "workflows"))
		if err != nil {
			return nil
		}
		for _, run := range workflowRuns {
			if !run.IsDir() || !strings.HasPrefix(run.Name(), "wf_") {
				continue
			}
			runDir := filepath.Join(path, "workflows", run.Name())
			for _, agent := range scanClaudeWorkflowRunAgents(runDir) {
				workflowFound = append(workflowFound, workflowLocated{agent: agent})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	tasks := make([]core.BackgroundTask, 0, len(found)+len(workflowFound))
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
		// StartedAt=meta mtime（spawn 时刻）、UpdatedAt=agent jsonl mtime（activity
		// 时刻；jsonl 缺席回落 meta mtime）——r1 F-6。
		updatedAt := f.modTime
		if info, err := os.Stat(f.jsonlPath); err == nil {
			updatedAt = info.ModTime()
		}
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
			UpdatedAt:           updatedAt,
			ToolUseCount:        toolUses,
			TranscriptAvailable: jsonlErr == nil,
		})
	}
	journalCache := map[string]map[string]claudeWorkflowAgentJournal{}
	for _, wf := range workflowFound {
		a := wf.agent
		journalStates, cached := journalCache[a.RunDir]
		if !cached {
			_, journalStates = parseClaudeWorkflowJournal(a.RunDir)
			journalCache[a.RunDir] = journalStates
		}
		reducerStatus, reducerHasTurns := claudeSidechainReducerEvidence(a.JsonlPath)
		status := claudeWorkflowTaskStatus(journalStates, a.Meta.agentID, reducerStatus, reducerHasTurns)
		toolUses := claudeSidechainToolUseCount(a.JsonlPath)
		rootSession := claudeRootSessionFromAgentPath(a.MetaPath)
		_, jsonlErr := os.Stat(a.JsonlPath)
		// 与旧布局同一字段语义（r1 F-6）；Title 按 OD-W1 拼接 phase。
		updatedAt := a.MetaModTime
		if info, err := os.Stat(a.JsonlPath); err == nil {
			updatedAt = info.ModTime()
		}
		tasks = append(tasks, core.BackgroundTask{
			TaskID:              a.Meta.agentID,
			BackendID:           "claudecode",
			RootSessionID:       rootSession,
			ParentTaskID:        "", // workflow meta 无 parentAgentId——置空，不猜（§3.2.5）
			AgentID:             a.Meta.agentID,
			Title:               claudeWorkflowTaskTitle(a.Meta),
			AgentName:           strings.TrimSpace(a.Meta.AgentType),
			Status:              status,
			StartedAt:           a.MetaModTime,
			UpdatedAt:           updatedAt,
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
	}
	if t.DurationMillis > 0 {
		// Explicit work wall time (DSH official sessionStats llmMs+toolMs) wins:
		// list rows carry no startedAt/finishedAt pair, and clock-span duration
		// would bill idle time the task never spent working.
		wire["durationMillis"] = t.DurationMillis
	} else if !t.FinishedAt.IsZero() && !t.StartedAt.IsZero() {
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

// projectionBackgroundTasks reuses workflow truth already committed for the
// open session. Codex's prior list path re-read every historical turn and every
// item page, which made opening the task center take 10+ seconds even while the
// same child ids/statuses were already visible in the message projection.
func projectionBackgroundTasks(backendID, sessionID string, projection SessionProjection) ([]core.BackgroundTask, bool) {
	byID := make(map[string]core.BackgroundTask)
	var order []string
	hasWorkflow := false
	for _, turn := range projection.Turns {
		if turn.Assistant == nil {
			continue
		}
		startedAt := time.Time{}
		if turn.StartedAt > 0 {
			startedAt = time.UnixMilli(turn.StartedAt).UTC()
		} else if turn.CompletedAt > 0 {
			startedAt = time.UnixMilli(turn.CompletedAt).UTC()
		}
		finishedAt := time.Time{}
		if turn.CompletedAt > 0 {
			finishedAt = time.UnixMilli(turn.CompletedAt).UTC()
		} else {
			finishedAt = startedAt
		}
		for _, part := range turn.Assistant.Parts {
			if part.Type != "workflow" {
				continue
			}
			for _, phase := range part.WorkflowPhases {
				for _, member := range phase.Members {
					childID := strings.TrimSpace(member.ChildSessionID)
					if childID == "" {
						continue
					}
					hasWorkflow = true
					status := backgroundTaskStatusFromProjection(member.Status)
					current, exists := byID[childID]
					if !exists {
						title := strings.TrimSpace(member.Label)
						if title == "" || strings.HasPrefix(title, "Agent-") {
							title = strings.TrimSpace(part.WorkflowName)
						}
						current = core.BackgroundTask{
							TaskID: childID, BackendID: backendID, RootSessionID: sessionID,
							AgentID: childID, Title: title, Status: status,
							StartedAt: startedAt, UpdatedAt: startedAt, TranscriptAvailable: true,
						}
						order = append(order, childID)
					} else {
						current.Status = status
						if !finishedAt.IsZero() {
							current.UpdatedAt = finishedAt
						}
					}
					if status != "running" && status != "queued" && !finishedAt.IsZero() {
						current.FinishedAt = finishedAt
					}
					byID[childID] = current
				}
			}
		}
	}
	tasks := make([]core.BackgroundTask, 0, len(order))
	for _, childID := range order {
		tasks = append(tasks, byID[childID])
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt) })
	return tasks, hasWorkflow
}

func backgroundTaskStatusFromProjection(status string) string {
	switch status {
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "cancelled", "interrupted":
		return "cancelled"
	default:
		return "running"
	}
}

func (h *Handlers) handleBackgroundTasksList(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		Directory string `json:"directory"`
		SessionID string `json:"sessionId"`
	}
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}
	params.SessionID = strings.TrimSpace(params.SessionID)
	finishBackgroundMetrics := h.runtimeDiagnostics.beginBackgroundTask(msg.BackendID)
	outcome := "success"
	defer func() {
		finishBackgroundMetrics(outcome)
	}()
	if params.SessionID == "" {
		outcome = "missing_param"
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}
	var tasks []core.BackgroundTask
	switch {
	case agent.Name() == "claudecode":
		dir := strings.TrimSpace(params.Directory)
		projectsDir := h.claudeProjectsRootForBackgroundTasks(dir)
		var err error
		tasks, err = claudeBackgroundTasks(projectsDir)
		if err != nil {
			outcome = "list_failed"
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
			return
		}
	default:
		// Codex workflow cards and the task center share committed projection truth.
		// Summary-only projection cannot prove "no workflow"; that case runs one
		// full scan under the revision cache below instead of inferring an empty list.
		if msg.BackendID == "codex-remote" && h.projectionKernel != nil {
			if projected, resolved := h.codexRemoteBackgroundTasks(msg.BackendID, params.SessionID); resolved {
				tasks = projected
				break
			}
			if status := h.projectionKernel.Status(msg.BackendID, params.SessionID).Phase; status != ProjectionHydrateReady {
				outcome = "projection_not_ready"
				retryAfter := int64(250)
				retryable := true
				conn.SendResult(msg.RequestID, nil, &WireError{
					Code:             "background_tasks.projection_not_ready",
					Message:          "session projection is not ready; retry after it commits",
					Retryable:        &retryable,
					RetryAfterMillis: &retryAfter,
				})
				return
			}
		}
		if provider, ok := agent.(core.SessionBackgroundTaskProvider); ok {
			var err error
			if msg.BackendID == "codex-remote" {
				tasks, err = h.scanCodexRemoteBackgroundTasks(params.SessionID, provider)
			} else {
				tasks, err = provider.ListSessionBackgroundTasks(context.Background(), params.SessionID)
			}
			if err != nil {
				outcome = "list_failed"
				conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
				return
			}
		} else if provider, ok := agent.(core.BackgroundTaskProvider); ok {
			var err error
			tasks, err = provider.ListBackgroundTasks(context.Background())
			if err != nil {
				outcome = "list_failed"
				conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
				return
			}
		} else {
			outcome = "not_supported"
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "backend does not expose background tasks"})
			return
		}
	}
	wire := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		if strings.TrimSpace(t.RootSessionID) != params.SessionID {
			continue
		}
		wire = append(wire, backgroundTaskToWire(t))
	}
	conn.SendResult(msg.RequestID, map[string]any{"tasks": wire}, nil)
}

// codexRemoteBackgroundTasks resolves tasks from a Ready projection when workflow
// truth is present. It never treats a summary-only "no workflow" projection as
// authoritative emptiness; that unknown state returns resolved=false so the caller
// performs one full scan and caches the negative result by SyncRev.
func (h *Handlers) codexRemoteBackgroundTasks(backendID, sessionID string) ([]core.BackgroundTask, bool) {
	if h == nil || h.projectionKernel == nil {
		return nil, false
	}
	if h.projectionKernel.Status(backendID, sessionID).Phase != ProjectionHydrateReady {
		return nil, false
	}
	projection, ok := h.projectionKernel.Snapshot(backendID, sessionID)
	if !ok {
		return nil, false
	}
	tasks, hasWorkflow := projectionBackgroundTasks(backendID, sessionID, projection)
	if hasWorkflow {
		h.backgroundTaskFlights.pruneCompletedSession(backendID, sessionID, projection.SyncRev)
		return tasks, true
	}
	cached, hit := h.backgroundTaskFlights.lookup(backendID, sessionID, projection.SyncRev)
	if !hit {
		return nil, false
	}
	return cached, true
}

// scanCodexRemoteBackgroundTasks runs the all-turn history scan at most once per
// session/projection revision. The 30s bound prevents a pathological scan from
// owning the Remote channel indefinitely; timeout remains an error.
func (h *Handlers) scanCodexRemoteBackgroundTasks(sessionID string, provider core.SessionBackgroundTaskProvider) ([]core.BackgroundTask, error) {
	var syncRev int
	if projection, ok := h.projectionKernel.Snapshot("codex-remote", sessionID); ok {
		syncRev = projection.SyncRev
	}
	defer h.backgroundTaskFlights.pruneCompletedSession("codex-remote", sessionID, syncRev)
	return h.backgroundTaskFlights.fetch(h.ctx, "codex-remote", sessionID, syncRev, func(ctx context.Context) ([]core.BackgroundTask, error) {
		return provider.ListSessionBackgroundTasks(ctx, sessionID)
	})
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
		"task":        backgroundTaskToWire(detail.Task),
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
// task registry. Claude sidechains live under ~/.claude/projects across all
// projects, so discovery starts at that root; handleBackgroundTasksList then
// applies the required rootSessionId == request.sessionId boundary.
func (h *Handlers) claudeProjectsRootForBackgroundTasks(_ string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".claude", "projects")
}
