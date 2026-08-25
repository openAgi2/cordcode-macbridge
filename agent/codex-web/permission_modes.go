package codexweb

import (
	"encoding/json"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.ModeSwitcher = (*Agent)(nil)

func normalizePermissionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "default", "ask", "on-request", "on_request":
		return "default"
	case "auto-review", "auto_review", "autoreview":
		return "auto-review"
	case "full-access", "full_access", "fullaccess", "danger-full-access":
		return "full-access"
	default:
		return "custom"
	}
}

func (a *Agent) SetMode(mode string) {
	a.mu.Lock()
	a.mode = normalizePermissionMode(mode)
	a.mu.Unlock()
}

func (a *Agent) GetMode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mode == "" {
		return "custom"
	}
	return a.mode
}

func (a *Agent) PermissionModes() []core.PermissionModeInfo {
	return []core.PermissionModeInfo{
		{Key: "default", Name: "Request approval", NameZh: "请求批准", Desc: "Ask before edits outside the workspace or network access", DescZh: "编辑外部文件或访问互联网时请求批准"},
		{Key: "auto-review", Name: "Agent review", NameZh: "替我批准", Desc: "Let Codex review detected risky operations", DescZh: "仅对检测到的风险操作请求批准"},
		{Key: "full-access", Name: "Full access", NameZh: "完全访问", Desc: "Unrestricted file and network access", DescZh: "不受限制地访问文件和互联网"},
		{Key: "custom", Name: "Custom configuration", NameZh: "自定义", Desc: "Use the effective Codex configuration", DescZh: "使用 Codex 当前有效配置"},
	}
}

// permissionModeParams 把权限档映射为 thread/start / thread/settings/update 的官方
// 稳定字段组合。禁止发 `permissions`（命名权限档）——它是 experimental 字段
// （protocol/v2/thread.rs:93 `#[experimental("thread/start.permissions")]`），本连接
// 未声明 experimentalApi 时被 server 以 -32600 拒绝（2026-08-26 真机）。官方模式
// （tui/src/app_server_session.rs:1694-1713 thread_start_params_from_config）：无命名
// 档时发 sandbox + approvalPolicy + approvalsReviewer（sandbox 仅在 permissions 为
// None 时携带）。SandboxMode/AskForApproval 均为 kebab-case wire
// （protocol/src/config_types.rs:103 / protocol.rs:914）。
func permissionModeParams(mode string) map[string]any {
	switch normalizePermissionMode(mode) {
	case "default":
		return map[string]any{"sandbox": "workspace-write", "approvalPolicy": "on-request", "approvalsReviewer": "user"}
	case "auto-review":
		return map[string]any{"sandbox": "workspace-write", "approvalPolicy": "on-request", "approvalsReviewer": "auto_review"}
	case "full-access":
		return map[string]any{"sandbox": "danger-full-access", "approvalPolicy": "never", "approvalsReviewer": "user"}
	default:
		return nil
	}
}

func (a *Agent) rememberPermissionMode(settings *ThreadStartResult) {
	if settings == nil {
		return
	}
	mode := "custom"
	profile := strings.ToLower(strings.TrimSpace(settings.ActivePermissionProfile.ID))
	reviewer := strings.ToLower(strings.TrimSpace(settings.ApprovalsReviewer))
	policy := strings.ToLower(approvalPolicyName(settings.ApprovalPolicy))
	switch {
	case profile == ":danger-full-access" || policy == "never":
		mode = "full-access"
	case reviewer == "auto_review" || reviewer == "autoreview" || reviewer == "auto-review":
		mode = "auto-review"
	case profile == ":workspace" || policy == "on-request":
		mode = "default"
	}
	a.SetMode(mode)
}

// AskForApproval can be either a kebab-case string or the official granular
// object. Only the string variants map to the compact iOS mode catalog; a
// granular policy remains custom instead of being misreported.
func approvalPolicyName(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}
