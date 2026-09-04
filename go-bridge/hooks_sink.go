package gobridge

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// hooks_sink.go —— Claude Code 官方 hooks 事件层（设计 §6 Phase 3，M4 收缩后）。
//
// 范围：只服务 CordCode 自 spawn 会话（agent/claudecode 在 spawn 时以 --settings
// 内联 HTTP hooks 指向本端点；hooks 数组跨层 merge，不打掉 cc-switch 等用户
// hooks——Phase 0 实证）。外部会话维持轮询为默认，本层不取代。
//
// 不订阅 PermissionRequest（S2：避免与 stdio can_use_tool 双应答；本机 7823 死
// 端点为前车样本）。SessionStart 不订阅（Phase 0 实证：--settings 层不触发）。
//
// 鉴权：token 走 URL 路径段（Phase 0 实证 HTTP hook 配置无 headers 字段）。
//
// 活性检测（S3，验收硬条件）：端点心跳自检（probe）+ 收据统计如实上报；
// 失活时行为=纯轮询（现状），不伪装事件驱动。

// claudeHookEndpointPath is the management route (token appended as path segment).
const claudeHookEndpointPath = "/internal/hooks/claude/"

// ClaudeHookEvent is one parsed hooks POST (field names per Phase 0 dump
// hooks-posts.jsonl, CLI 2.1.234).
type ClaudeHookEvent struct {
	Event         string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD           string `json:"cwd"`
	PromptID      string `json:"prompt_id"`
	// Stop-only (Phase 0 dump): definitive final assistant text — transcript_path
	// 可能滞后（设计 §3.5），正文以此为准。
	LastAssistantMessage string `json:"last_assistant_message"`
	// ConfigChange-only (Phase 0 dump): which settings layer changed.
	Source  string `json:"source"`
	FilePath string `json:"file_path"`
	// SessionEnd-only.
	Reason string `json:"reason"`
}

// ClaudeHookSink consumes parsed hook events. Implemented by *Handlers.
type ClaudeHookSink interface {
	HandleClaudeHook(ev ClaudeHookEvent)
}

// claudeHookHealth is the honest source-status state (S3).
type claudeHookHealth struct {
	probeOK      atomic.Bool
	lastReceipt  atomic.Value // stores time.Time
	receiptCount atomic.Int64
}

func (h *claudeHookHealth) snapshot() map[string]any {
	last, _ := h.lastReceipt.Load().(time.Time)
	out := map[string]any{
		"endpointProbeOk": h.probeOK.Load(),
		"receipts":        h.receiptCount.Load(),
	}
	if !last.IsZero() {
		out["lastReceiptAt"] = last.UTC().Format(time.RFC3339)
	}
	return out
}

// serveClaudeHook handles POST /internal/hooks/claude/{token}. Auth is the
// token in the path segment (HTTP hooks cannot set headers — Phase 0).
func (s *ManagementServer) serveClaudeHook(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, claudeHookEndpointPath)
	if s.cfg.Token == "" || token == "" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
		http.Error(w, `{"error":"token mismatch"}`, http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"read body"}`, http.StatusBadRequest)
		return
	}
	var ev ClaudeHookEvent
	if err := json.Unmarshal(body, &ev); err != nil || ev.Event == "" {
		// 未知形状不假装理解（fail closed），但 200 避免 CLI 侧非阻塞报错噪音。
		slog.Debug("mgmt: claude hook payload unrecognized, ignored", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	s.claudeHookHealth.receiptCount.Add(1)
	s.claudeHookHealth.lastReceipt.Store(time.Now())

	if ev.Event == "Heartbeat" {
		// 心跳自检（main 在 wiring 后立即自测端点路由+鉴权+解析）。
		s.claudeHookHealth.probeOK.Store(true)
		if s.cfg.ClaudeHookHolder != nil {
			s.cfg.ClaudeHookHolder.probeOK.Store(true)
		}
		slog.Info("mgmt: claude hooks endpoint probe ok")
		w.WriteHeader(http.StatusOK)
		return
	}

	if s.cfg.Handlers != nil {
		s.cfg.Handlers.HandleClaudeHook(ev)
	}
	w.WriteHeader(http.StatusOK)
}

// claudeHookSettingsJSON builds the --settings inline payload for self-spawned
// sessions. Only events that actually fire from the --settings layer on this
// CLI generation are subscribed (Phase 0 证据)；PermissionRequest 故意缺席（S2），
// SessionStart 不触发（--settings 层不发射，Phase 0）。
func claudeHookSettingsJSON(url string) string {
	events := []string{"Stop", "StopFailure", "UserPromptSubmit", "ConfigChange", "SessionEnd"}
	hooks := make(map[string]any, len(events))
	for _, ev := range events {
		hooks[ev] = []map[string]any{{
			"hooks": []map[string]any{{"type": "http", "url": url, "timeout": 10}},
		}}
	}
	payload, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		return ""
	}
	return string(payload)
}

// claudeHookConfigHolder lets the claude agent resolve the hook endpoint at
// SPAWN time even though agents are constructed before the management server
// starts (main.go wiring order). probeOK flips only after main's startup
// self-probe POST succeeds (心跳自检, S3) — until then spawns get no
// --settings hooks (纯轮询=现状).
type claudeHookConfigHolder struct {
	mu      sync.RWMutex
	base    string // http://127.0.0.1:<port>
	token   string
	probeOK atomic.Bool
}

func (h *claudeHookConfigHolder) set(base, token string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.base, h.token = base, token
}

// SettingsProvider returns the --settings JSON payload when hooks are usable
// (endpoint up + probe passed), else ok=false（纯轮询=现状，不伪装）。
func (h *claudeHookConfigHolder) SettingsProvider() func() (string, bool) {
	return func() (string, bool) {
		h.mu.RLock()
		base, token := h.base, h.token
		h.mu.RUnlock()
		if base == "" || token == "" || !h.probeOK.Load() {
			return "", false
		}
		payload := claudeHookSettingsJSON(fmt.Sprintf("%s%s%s", base, claudeHookEndpointPath, token))
		if payload == "" {
			return "", false
		}
		return payload, true
	}
}

// ---- Handlers 侧分发 ------------------------------------------------------

// claudeConfigInvalidator is implemented by the claudecode Agent (settings
// cache invalidation + catalog refresh on ConfigChange).
type claudeConfigInvalidator interface {
	InvalidateSettingsModels(ctx context.Context)
}

// HandleClaudeHook implements ClaudeHookSink on *Handlers.
//
// Stop → 事件驱动定向刷新：立即 nudge 该会话的 transcript file-relay（不等
// 3s tick）——Phase 3 验收项「transcript_path 单文件定向刷新可观测」。
// ConfigChange → settings 层变化（cc-switch 整份重写等）：失效别名缓存 +
// 活会话 list_models 刷新官方目录。
// 其余事件仅观测（fail closed：不认识的事件不猜测语义）。
func (h *Handlers) HandleClaudeHook(ev ClaudeHookEvent) {
	switch ev.Event {
	case "Stop":
		h.nudgeClaudeRelay(ev.SessionID)
		slog.Info("claude hook: Stop → targeted refresh", "sessionID", ev.SessionID,
			"lastAssistantLen", len(ev.LastAssistantMessage))
	case "StopFailure":
		slog.Warn("claude hook: StopFailure", "sessionID", ev.SessionID, "cwd", ev.CWD)
	case "ConfigChange":
		slog.Info("claude hook: ConfigChange → invalidate settings models + catalog refresh",
			"sessionID", ev.SessionID, "source", ev.Source, "file", ev.FilePath)
		h.mu.Lock()
		agent := h.agents["claudecode"]
		h.mu.Unlock()
		if inv, ok := agent.(claudeConfigInvalidator); ok {
			inv.InvalidateSettingsModels(context.Background())
		}
	case "UserPromptSubmit", "SessionEnd":
		slog.Debug("claude hook observed", "event", ev.Event, "sessionID", ev.SessionID, "reason", ev.Reason)
	default:
		slog.Debug("claude hook: unhandled event ignored (fail closed)", "event", ev.Event)
	}
}

// ---- relay nudge ----------------------------------------------------------

// registerClaudeRelayNudge installs (or replaces) the nudge channel for a
// session's file-relay loop; returns the previous channel if any.
func (h *Handlers) registerClaudeRelayNudge(sessionID string, ch chan struct{}) {
	h.nudgeMu.Lock()
	defer h.nudgeMu.Unlock()
	if h.claudeRelayNudges == nil {
		h.claudeRelayNudges = make(map[string]chan struct{})
	}
	h.claudeRelayNudges[sessionID] = ch
}

// unregisterClaudeRelayNudge removes the nudge channel (relay exit path).
func (h *Handlers) unregisterClaudeRelayNudge(sessionID string, ch chan struct{}) {
	h.nudgeMu.Lock()
	defer h.nudgeMu.Unlock()
	if cur, ok := h.claudeRelayNudges[sessionID]; ok && cur == ch {
		delete(h.claudeRelayNudges, sessionID)
	}
}

// nudgeClaudeRelay triggers an immediate poll of the session's transcript
// file-relay (non-blocking; no relay = no-op，轮询兜底仍在)。
func (h *Handlers) nudgeClaudeRelay(sessionID string) {
	h.nudgeMu.Lock()
	ch := h.claudeRelayNudges[sessionID]
	h.nudgeMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// claudeHookStatusPath is the health-status GET endpoint (Bearer auth).
const claudeHookStatusPath = "/internal/hooks/claude/status"

// handleClaudeHookStatus reports the honest hook-source health (S3).
func (s *ManagementServer) handleClaudeHookStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.claudeHookHealth.snapshot())
}
