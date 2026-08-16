package dshweb

// Official dsh web permission presets (Read Only / Workspace Write / Full access).
//
// The official composer gear writes the current session through Typert remote
// `commands/execute` (`session.command("/permission <preset>")` in the web
// client). That path only appends command/run + command/done + permission/preset
// — it is never a user/message and is never sent to the model.
//
// session.prompt of the same slash line is the WRONG surface: HTTP prompt()
// always createUserMessage + followup, so the model sees "/permission …" and
// replies that it cannot change sandbox policy. Do not use it here.
//
// The default for future sessions is settings ns "permission".defaultPreset.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const commandsExecuteMethod = "commands/execute"

const (
	permissionModeReadOnly         = "read-only"
	permissionModeWorkspaceWrite   = "workspace-write"
	permissionModeDangerFullAccess = "danger-full-access"
	permissionSettingsNS           = "permission"
)

var _ core.ModeSwitcher = (*Agent)(nil)
var _ core.LiveModeSwitcher = (*dshSession)(nil)

func (a *Agent) SetMode(mode string) {
	norm := normalizePermissionMode(mode)
	a.mu.Lock()
	a.mode = norm
	sid := a.lastActiveSessionID
	a.mu.Unlock()
	a.persistPermissionDefault(norm)
	if sid != "" {
		if err := a.applySessionPermission(sid, norm); err != nil {
			slog.Warn("dsh-web: apply session permission failed",
				"sessionPrefix", shortLog(sid), "mode", norm, "error", err)
		}
	}
}

func (a *Agent) GetMode() string {
	a.mu.RLock()
	if a.mode != "" {
		mode := a.mode
		a.mu.RUnlock()
		return mode
	}
	a.mu.RUnlock()
	if mode := a.readPermissionDefault(); mode != "" {
		a.mu.Lock()
		if a.mode == "" {
			a.mode = mode
		}
		a.mu.Unlock()
		return mode
	}
	return permissionModeWorkspaceWrite
}

func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	return []core.PermissionModeInfo{
		{Key: permissionModeReadOnly, Name: "Read Only", NameZh: "只读", Desc: "Sandbox restricts operations to reads", DescZh: "沙箱限制为只读操作"},
		{Key: permissionModeWorkspaceWrite, Name: "Workspace Write", NameZh: "工作区可写", Desc: "Writes allowed under the session workspace; approvals ask", DescZh: "允许写入会话工作区；需审批时询问"},
		{Key: permissionModeDangerFullAccess, Name: "Full access", NameZh: "完全访问", Desc: "No sandbox restrictions; approvals never asked", DescZh: "无沙箱限制；不询问审批"},
	}
}

func (s *dshSession) SetLiveMode(mode string) bool {
	if s == nil || s.agent == nil {
		return false
	}
	err := s.agent.applySessionPermission(s.CurrentSessionID(), normalizePermissionMode(mode))
	if err != nil {
		slog.Warn("dsh-web: live permission switch failed",
			"sessionPrefix", shortLog(s.CurrentSessionID()), "error", err)
		return false
	}
	return true
}

type commandsExecuteRequest struct {
	Args commandsExecuteArgs `json:"args"`
}

type commandsExecuteArgs struct {
	AgentID string `json:"agentId"`
	Line    string `json:"line"`
}

type commandsExecuteValue struct {
	CommandID string `json:"commandId"`
	Result    struct {
		Kind string `json:"kind"`
		Text string `json:"text,omitempty"`
	} `json:"result"`
}

func (a *Agent) applySessionPermission(sessionID, mode string) error {
	if sessionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	// Official chrome payload: Remote args { agentId, line }. The gateway
	// rejects anything that is not exactly one plain-object `args` field.
	var out commandsExecuteValue
	err = client.Call(ctx, commandsExecuteMethod, commandsExecuteRequest{
		Args: commandsExecuteArgs{
			AgentID: sessionID,
			Line:    "/permission " + mode,
		},
	}, &out)
	if err != nil {
		return err
	}
	if strings.EqualFold(out.Result.Kind, "error") {
		return fmt.Errorf("dsh-web: /permission %s: %s", mode, out.Result.Text)
	}
	if out.CommandID == "" && out.Result.Kind == "" {
		return fmt.Errorf("dsh-web: /permission %s: command not matched", mode)
	}
	return nil
}

func (a *Agent) persistPermissionDefault(mode string) {
	if a == nil || a.resolver == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client, err := a.clientFor(ctx)
	if err != nil {
		return
	}
	payload := map[string]any{
		"ns":    permissionSettingsNS,
		"patch": map[string]any{"defaultPreset": mode},
	}
	if err := client.Call(ctx, "settings.update", payload, nil); err != nil {
		slog.Debug("dsh-web: persist permission default failed", "mode", mode, "error", err)
	}
}

func (a *Agent) readPermissionDefault() string {
	if a == nil || a.resolver == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client, err := a.clientFor(ctx)
	if err != nil {
		return ""
	}
	var out struct {
		Namespaces []struct {
			NS    string          `json:"ns"`
			Value json.RawMessage `json:"value"`
		} `json:"namespaces"`
	}
	if err := client.Call(ctx, "settings.describe", map[string]any{}, &out); err != nil {
		return ""
	}
	for _, ns := range out.Namespaces {
		if ns.NS != permissionSettingsNS {
			continue
		}
		var val struct {
			DefaultPreset string `json:"defaultPreset"`
		}
		if json.Unmarshal(ns.Value, &val) != nil {
			return ""
		}
		return normalizePermissionMode(val.DefaultPreset)
	}
	return ""
}

func normalizePermissionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "workspace-write", "workspacewrite", "workspace_write":
		return permissionModeWorkspaceWrite
	case "read-only", "readonly", "read_only":
		return permissionModeReadOnly
	case "danger-full-access", "dangerfullaccess", "danger_full_access", "full-access", "fullaccess":
		return permissionModeDangerFullAccess
	default:
		return permissionModeWorkspaceWrite
	}
}
