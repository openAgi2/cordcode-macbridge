package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ── OpenCode (ocProxy) 全量路由 ──────────────────────────────────────────────

func (h *Handlers) handleOpenCodeRPC(conn Connection, msg WireMessage) {
	dir := extractDir(msg)

	// cc-connect 已覆盖的能力走 generic dispatch
	switch msg.Method {
	case "hello":
		conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)

	case "list_providers", "set_provider", "list_agents",
		"fetch_todos", "get_usage", "run_diagnostics",
		"get_workspace_diff",
		"list_memory_files", "read_memory_file", "fetch_content_chunk", "read_file",
		"list_directory", "get_git_context", "checkout_git_branch",
		"create_git_branch", "create_git_worktree",
		"rename_session", "archive_session", "compress_context",
		"delete_session", "list_models", "switch_model",
		"get_session_messages":
		agent, ok := h.getAgent(msg.BackendID)
		if !ok {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "backend_not_found", Message: "opencode agent not registered"})
			return
		}
		h.dispatchRPC(conn, msg, agent)
		return

	case "list_permission_modes", "set_permission_mode":
		agent, ok := h.getAgent(msg.BackendID)
		if !ok {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "backend_not_found", Message: "opencode agent not registered"})
			return
		}
		if msg.Method == "list_permission_modes" {
			h.handleListPermissionModes(conn, msg, agent)
		} else {
			h.handleSetPermissionMode(conn, msg, agent)
		}
		return

	case "resolve_permission":
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "opencode does not support permission resolution via cc-connect"})

	case "share_session":
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "not_supported", Message: "session share is not supported"})
	}

	// 以下 case 仍需 ocProxy（cc-connect 尚未实现）
	switch msg.Method {
	case "get_session":
		h.ocHandleGetSession(conn, msg, dir)

	case "list_sessions":
		h.ocHandleListSessions(conn, msg, dir)

	case "list_projects":
		h.ocHandleListProjects(conn, msg)

	case "create_session":
		h.ocHandleCreateSession(conn, msg, dir)

	case "resume_session":
		h.ocHandleResumeSession(conn, msg, dir)

	case "send_message":
		agent, ok := h.getAgent(msg.BackendID)
		if !ok {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "backend_not_found", Message: "opencode agent not registered"})
			return
		}
		h.ocHandleSendMessage(conn, msg, dir, agent)

	case "abort_generation":
		h.ocHandleAbortGeneration(conn, msg, dir)

	case "set_session_pinned", "list_pinned_sessions":
		// Session pinning is backend-neutral: route to the generic handlers (which type-assert
		// core.SessionPinner on the opencode agent). OpenCode summaries resolve via ocProxy.
		agent, ok := h.getAgent(msg.BackendID)
		if !ok {
			conn.SendResult(msg.RequestID, nil, &WireError{Code: "backend_not_found", Message: "opencode agent not registered"})
			return
		}
		if msg.Method == "set_session_pinned" {
			h.handleSetSessionPinned(conn, msg, agent)
		} else {
			h.handleListPinnedSessions(conn, msg, agent)
		}

	default:
		conn.SendResult(msg.RequestID, nil, &WireError{
			Code:    "method_not_found",
			Message: fmt.Sprintf("method %q not implemented", msg.Method),
		})
	}
}

func (h *Handlers) enrichSessionState(mapped map[string]interface{}) map[string]interface{} {
	return h.enrichSessionStateWithAgent(mapped, nil)
}

func (h *Handlers) enrichSessionStateWithAgent(mapped map[string]interface{}, agent core.Agent) map[string]interface{} {
	if mapped == nil {
		return nil
	}
	sessionID, _ := mapped["id"].(string)
	if sessionID != "" {
		state := "idle"
		if ts, ok := h.sessions.get(sessionID); ok {
			state = string(ts.state)
		}

		// 对 claudecode：优先用 GetRunningSessionIDs（检查有活跃进程的 session）。
		// 若 session 不在结果中（进程已退出、session 文件已清理），回退到直接读取
		// transcript 文件判定。这修复了进程退出后 registry 旧 "running" 状态泄漏的问题。
		if agent != nil && agent.Name() == "claudecode" {
			h.injectClaudeReasoningEffort(mapped, agent)
			usedTranscriptFallback := false
			if lister, ok := agent.(core.RunningSessionLister); ok {
				runningMap, err := lister.GetRunningSessionIDs(context.TODO())
				if err == nil {
					if runningMap[sessionID] {
						state = "running"
					} else {
						// 不在 runningMap 中——进程可能已退出。
						// 回退到直接读取 transcript 文件判定。
						_, sessPath := findClaudeSessionFile(sessionID, "")
						if sessPath != "" {
							state = h.detectClaudeTranscriptState(sessPath)
							if state == "unknown" {
								state = "idle"
							}
						} else {
							state = "idle"
						}
						h.sessions.markIdle(sessionID)
						usedTranscriptFallback = true
					}
				}
			}
			if !usedTranscriptFallback && state == "running" {
				// registry 说 running 但 GetRunningSessionIDs 出错，
				// 也用 transcript 校验。
				_, sessPath := findClaudeSessionFile(sessionID, "")
				if sessPath != "" {
					fileState := h.detectClaudeTranscriptState(sessPath)
					if fileState == "idle" {
						state = "idle"
						h.sessions.markIdle(sessionID)
					}
				}
			}
		} else if agent != nil {
			if lister, ok := agent.(core.RunningSessionLister); ok {
				runningMap, err := lister.GetRunningSessionIDs(context.TODO())
				if err == nil {
					if runningMap[sessionID] {
						state = "running"
					} else {
						state = "idle"
						h.sessions.markIdle(sessionID)
					}
				}
			}
		}
		mapped["runtimeState"] = state
	}
	return mapped
}

// getRunningMap returns the once-per-request snapshot of running session IDs for
// agent, or nil when agent is not a core.RunningSessionLister or the lookup
// errored. List handlers call this once and pass the result to
// enrichSessionStatesForList so GetRunningSessionIDs is never invoked once per
// listed session row.
//
// For the Claude agent the lookup is served by the short-TTL runningMapCache
// (Fix 3): within a TTL window a burst of list_sessions requests reuses one
// GetRunningSessionIDs call, and MacBridge-tracked turn transitions invalidate
// the cache immediately via the sessionRegistry state-change callback. For any
// other lister the thin uncached lookup is used (only Claude implements
// RunningSessionLister in production today).
func (h *Handlers) getRunningMap(ctx context.Context, agent core.Agent) map[string]bool {
	if agent == nil {
		return nil
	}
	if agent.Name() == "claudecode" {
		return h.runningMap.get(ctx)
	}
	lister, ok := agent.(core.RunningSessionLister)
	if !ok {
		return nil
	}
	running, err := lister.GetRunningSessionIDs(ctx)
	if err != nil || running == nil {
		return nil
	}
	return running
}

// enrichSessionStatesForList is the list-safe batch enrichment used by every
// list_sessions handler. It is read-only and side-effect free with respect to
// both the filesystem and the session registry:
//   - it never opens/parses transcript files for any listed row (no
//     findClaudeSessionFile / detectClaudeTranscriptState / isSessionExecuting);
//   - it never mutates the registry (no h.sessions.markIdle);
//   - it never writes debug files.
//
// Precise running state comes from the precomputed runningMap (itself bounded by
// live Claude PID count via GetRunningSessionIDs). When runningMap is non-nil it
// is authoritative: a session absent from it is reported idle even if a stale
// registry entry claims running, which keeps completed sessions from appearing
// stuck. When runningMap is nil (non-lister agent, e.g. Codex/OpenCode today, or
// lookup error) the registry's last-known state is used as a fallback. Deeper
// per-session inspection belongs to single-session detail paths
// (enrichSessionStateWithAgent) and to GetRunningSessionIDs — never to per-row
// list enrichment.
func (h *Handlers) enrichSessionStatesForList(sessions []map[string]interface{}, agent core.Agent, runningMap map[string]bool) []map[string]interface{} {
	claude := agent != nil && agent.Name() == "claudecode"
	for i, s := range sessions {
		if s == nil {
			continue
		}
		if claude {
			h.injectClaudeReasoningEffort(s, agent)
		}
		sessions[i] = h.applyListRuntimeState(s, runningMap)
	}
	return sessions
}

// applyListRuntimeState sets runtimeState on mapped from the registry and the
// precomputed runningMap only. It does not touch the filesystem and does not
// mutate the registry. When runningMap is non-nil it is authoritative.
func (h *Handlers) applyListRuntimeState(mapped map[string]interface{}, runningMap map[string]bool) map[string]interface{} {
	if mapped == nil {
		return nil
	}
	state := "idle"
	if sessionID, _ := mapped["id"].(string); sessionID != "" {
		if ts, ok := h.sessions.get(sessionID); ok && string(ts.state) != "" {
			state = string(ts.state)
		}
		if runningMap != nil {
			if runningMap[sessionID] {
				state = "running"
			} else {
				state = "idle"
			}
		}
	}
	mapped["runtimeState"] = state
	return mapped
}

// injectClaudeReasoningEffort fills reasoningEffort from the agent's in-memory
// switcher when the session metadata did not carry one. Cheap getter only; safe
// for the list hot path. Mutates mapped in place when non-nil.
func (h *Handlers) injectClaudeReasoningEffort(mapped map[string]interface{}, agent core.Agent) {
	if mapped == nil || agent == nil {
		return
	}
	if effort, _ := mapped["reasoningEffort"].(string); strings.TrimSpace(effort) == "" {
		if re, ok := agent.(core.ReasoningEffortSwitcher); ok {
			if effort := normalizeClaudeRuntimeEffort(re.GetReasoningEffort()); effort != "" {
				mapped["reasoningEffort"] = effort
			}
		}
	}
}

// ── ocProxy: list_sessions ────────────────────────────────────────────────────

// openCodeSessionFetchLimit is the single upstream fetch budget. OpenCode server
// is array-only in stable (no cursor on /session), so the only way to know the
// real total is to ask for a large page once. 100 matches the server-side default
// upper bound and keeps one request bounded; the per-client page is then sliced
// in-memory by paginateSessionList with a real cursor, exactly like Codex/Claude.
const openCodeSessionFetchLimit = 100

func (h *Handlers) ocHandleListSessions(conn Connection, msg WireMessage, dir string) {
	rootsOnly := extractBool(msg, "rootsOnly")
	limit := h.effectiveSessionListLimit(extractPositiveInt(msg, "limit"))
	cursor := extractStringParam(msg, "cursor")
	if limit > 1000 {
		limit = 1000
	}

	started := time.Now()

	// Fetch a large page from upstream so the in-memory list reflects the real
	// total. rootsOnly is forwarded as the server-side roots=true SQL filter
	// (isNull(parent_id)); we no longer discard child sessions client-side, which
	// used to make hasMore unreliable for small projects.
	page, err := h.ocProxy.listSessions(OpenCodeSessionListOptions{
		Directory: dir,
		Limit:     openCodeSessionFetchLimit,
		Roots:     rootsOnly,
	})
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}

	agent, _ := h.getAgent(msg.BackendID)
	mapped := make([]map[string]interface{}, 0, len(page.Sessions))
	for _, s := range page.Sessions {
		mapped = append(mapped, mapSession(s))
	}
	mapped = h.enrichSessionStatesForList(mapped, agent, h.getRunningMap(context.Background(), agent))
	h.overlayPinnedState(mapped, "opencode")
	sortSessionsByUpdatedAt(mapped)

	// Slice the in-memory list by cursor+limit, identical to Codex/Claude.
	// paginateSessionList emits a real nextCursor and hasMore derived from the
	// actual remaining count, so "load more" appears whenever there is more data.
	result := paginateSessionList(mapped, cursor, limit)

	if ws, ok := result["sessions"].([]map[string]interface{}); ok {
		slog.Info("opencode list_sessions",
			"directory", dir,
			"limit", limit,
			"cursor_present", cursor != "",
			"result_count", len(ws),
			"next_cursor_present", result["hasMore"] == true,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	}
	conn.SendResult(msg.RequestID, result, nil)
}

// ── ocProxy: get_session ──────────────────────────────────────────────────────

func (h *Handlers) ocHandleGetSession(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	s, err := h.ocProxy.getSession(sessionID, dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "get_failed", Message: err.Error()})
		return
	}
	agent, _ := h.getAgent(msg.BackendID)
	conn.SendResult(msg.RequestID, map[string]interface{}{"session": h.enrichSessionStateWithAgent(mapSession(s), agent)}, nil)
}

// ── ocProxy: get_session_messages ─────────────────────────────────────────────

func (h *Handlers) ocHandleGetSessionMessages(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	// 订阅连接到该 session，以便 relayEvents 转发实时事件
	h.subscribeConnToSession(conn, msg, sessionID)

	msgs, err := h.ocProxy.getSessionMessages(sessionID, dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "history_failed", Message: err.Error()})
		return
	}

	var result []map[string]interface{}
	for _, m := range msgs {
		result = append(result, mapMessage(m))
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{
		"messages":        result,
		"nextCursor":      nil,
		"snapshotVersion": "v1",
		"truncated":       false,
	}, nil)
}

// ── ocProxy: list_models ──────────────────────────────────────────────────────

func (h *Handlers) ocHandleListModels(conn Connection, msg WireMessage, dir string) {
	models, err := h.ocProxy.listModels(dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{
		"models":            models,
		"configFingerprint": nil,
		"source":            "catalog",
		"generatedAtMillis": nowMillis(),
	}, nil)
}

// ── ocProxy: list_agents ──────────────────────────────────────────────────────

func (h *Handlers) ocHandleListAgents(conn Connection, msg WireMessage) {
	agents, err := h.ocProxy.listAgents()
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"agents": agents}, nil)
}

// ── ocProxy: list_projects ────────────────────────────────────────────────────

func (h *Handlers) ocHandleListProjects(conn Connection, msg WireMessage) {
	projects, err := h.ocProxy.listProjects()
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "list_failed", Message: err.Error()})
		return
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"projects": projects}, nil)
}

// ── ocProxy: create_session ───────────────────────────────────────────────────

func (h *Handlers) ocHandleCreateSession(conn Connection, msg WireMessage, dir string) {
	title := extractString(msg, "title")

	s, err := h.ocProxy.createSession(title, dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "create_failed", Message: err.Error()})
		return
	}

	session := h.enrichSessionState(mapSession(s))
	conn.SendResult(msg.RequestID, session, nil)
}

// ── ocProxy: resume_session ───────────────────────────────────────────────────

func (h *Handlers) ocHandleResumeSession(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	s, err := h.ocProxy.getSession(sessionID, dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "resume_failed", Message: err.Error()})
		return
	}

	session := h.enrichSessionState(mapSession(s))
	conn.SendResult(msg.RequestID, session, nil)
}

// ── opencode hybrid: send_message ─────────────────────────────────────────────

func (h *Handlers) ocHandleSendMessage(conn Connection, msg WireMessage, dir string, agent core.Agent) {
	var params struct {
		SessionID       string                 `json:"sessionId"`
		Content         string                 `json:"content"`
		Agent           string                 `json:"agent,omitempty"`
		Model           map[string]interface{} `json:"model,omitempty"`
		ReasoningEffort string                 `json:"reasoningEffort,omitempty"`
		Attachments     []AttachmentInput      `json:"attachments,omitempty"`
	}
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}
	if params.SessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	modelID := normalizeModelParam(params.Model)
	sess, err := h.ensureOpenCodeSession(agent, params.SessionID, modelID, dir)
	if err != nil {
		code := "send_failed"
		if strings.Contains(err.Error(), "HTTP 404") {
			code = "session_not_found"
		}
		conn.SendResult(msg.RequestID, nil, &WireError{Code: code, Message: err.Error()})
		return
	}

	conn.SendEvent(params.SessionID, msg.BackendID, "session_state_changed", map[string]interface{}{"state": "running"})
	h.broadcaster.Subscribe(conn, SubscriptionKey{
		BackendID: msg.BackendID,
		SessionID: params.SessionID,
		Directory: dir,
	})

	images, files := splitAttachments(params.Attachments)
	if err := sess.Send(params.Content, images, files); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "send_failed", Message: err.Error()})
		return
	}

	h.sessions.markRunning(params.SessionID)
	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
	h.startRelayIfNotRunning(params.SessionID, sess, conn, msg.BackendID)
}

// ── opencode hybrid: abort_generation ─────────────────────────────────────────

func (h *Handlers) ocHandleAbortGeneration(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
		return
	}
	if err := h.ocProxy.abortGeneration(sessionID, dir); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "abort_failed", Message: err.Error()})
		return
	}

	h.mu.Lock()
	sess, ok := h.deleteSession(sessionID)
	delete(h.opencodeSessionOptions, sessionID)
	h.mu.Unlock()
	if ok {
		_ = sess.Close()
	}

	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
	// 只有 session 确实被删除时才发完成事件，避免伪造状态
	if ok {
		conn.SendEvent(sessionID, msg.BackendID, "turn_completed", map[string]interface{}{"done": true, "reason": "aborted"})
		conn.SendEvent(sessionID, msg.BackendID, "session_state_changed", map[string]interface{}{"state": "idle"})
	}
}

// ── ocProxy: delete_session ───────────────────────────────────────────────────

func (h *Handlers) ocHandleDeleteSession(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}
	if err := h.ocProxy.deleteSession(sessionID, dir); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "delete_failed", Message: err.Error()})
		return
	}
	h.mu.Lock()
	sess, _ := h.deleteSession(sessionID)
	delete(h.opencodeSessionOptions, sessionID)
	h.mu.Unlock()
	if sess != nil {
		_ = sess.Close()
	}
	conn.SendResult(msg.RequestID, &ResultResponse{Ok: true}, nil)
}

// ── ocProxy: fetch_todos ──────────────────────────────────────────────────────

func (h *Handlers) ocHandleFetchTodos(conn Connection, msg WireMessage, dir string) {
	sessionID := extractSessionID(msg)
	if sessionID == "" {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "missing_param", Message: "sessionId required"})
		return
	}

	todos, err := h.ocProxy.fetchTodos(sessionID, dir)
	if err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "todo_failed", Message: err.Error()})
		return
	}

	var result []map[string]interface{}
	for _, t := range todos {
		content, _ := t["content"].(string)
		if content == "" {
			content, _ = t["text"].(string)
		}
		status, _ := t["status"].(string)
		if status == "" {
			status = "pending"
		}
		priority, _ := t["priority"].(string)
		if priority == "" {
			priority = "normal"
		}
		result = append(result, map[string]interface{}{
			"content":  content,
			"status":   status,
			"priority": priority,
		})
	}
	conn.SendResult(msg.RequestID, map[string]interface{}{"todos": result}, nil)
}
