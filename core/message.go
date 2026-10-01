package core

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MergeEnv returns base env with entries from extra overriding same-key entries.
// This prevents duplicate keys (e.g. two PATH entries) which cause the override
// to be silently ignored on Linux (getenv returns the first match).
func MergeEnv(base, extra []string) []string {
	keys := make(map[string]bool, len(extra))
	for _, e := range extra {
		if k, _, ok := strings.Cut(e, "="); ok {
			keys[k] = true
		}
	}
	merged := make([]string, 0, len(base)+len(extra))
	for _, e := range base {
		if k, _, ok := strings.Cut(e, "="); ok && keys[k] {
			continue
		}
		merged = append(merged, e)
	}
	return append(merged, extra...)
}

// controlPlaneEnvDenyPrefixes are env var name prefixes/values that must NEVER
// reach an agent data-plane subprocess (Claude/Codex/OpenCode and their tool
// children). These carry go-bridge's own control-plane secrets (management
// token, relay credential/route/endpoint) and OpenCode server auth. Leaking
// them lets a remote device pivot through an agent tool into loopback control
// APIs, bypassing capability policy.
//
// NOTE: provider data-plane secrets (ANTHROPIC_API_KEY etc.) are NOT here —
// agents need those to authenticate. Only control-plane keys are rejected.
var controlPlaneEnvDenyPrefixes = []string{
	"CCCODE_",          // go-bridge control plane (management token, relay creds, ...)
	"OPENCODE_SERVER_", // OpenCode HTTP API auth (server username/password)
	"CLAUDECODE",       // nested-session detection marker (claudecode bridge)
}

// controlPlaneEnvDenyExact are full env var names that must never reach an
// agent subprocess, in addition to the prefix list above.
var controlPlaneEnvDenyExact = map[string]struct{}{
	"OPENCODE_SERVER_USERNAME": {},
	"OPENCODE_SERVER_PASSWORD": {},
}

// isControlPlaneEnv reports whether an env entry (KEY=VALUE form) is a
// control-plane secret that must be stripped from agent environments.
func isControlPlaneEnv(entry string) bool {
	k, _, ok := strings.Cut(entry, "=")
	if !ok {
		return false
	}
	if _, deny := controlPlaneEnvDenyExact[k]; deny {
		return true
	}
	for _, p := range controlPlaneEnvDenyPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

// stripControlPlaneEnv returns env with every control-plane entry removed.
func stripControlPlaneEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if isControlPlaneEnv(e) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// agentEnvRuntimeAllowlist is the minimal set of runtime-essential env var
// names that an agent CLI needs to run at all (find binaries, home dir,
// locale, temp dir). Everything else inherited from the supervisor is dropped
// so control-plane leakage can't ride along.
var agentEnvRuntimeAllowlist = []string{
	"PATH", "HOME", "USER", "LOGNAME",
	"LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES",
	"TMPDIR", "SHELL",
}

// AgentEnvRuntimeAllowlist returns a copy of the minimal runtime-essential
// env var name allowlist used to seed agent subprocess environments.
func AgentEnvRuntimeAllowlist() []string {
	out := make([]string, len(agentEnvRuntimeAllowlist))
	copy(out, agentEnvRuntimeAllowlist)
	return out
}

// FilterEnvToAllowlist returns only entries from env whose key is in allow.
func FilterEnvToAllowlist(env []string, allow []string) []string {
	set := make(map[string]struct{}, len(allow))
	for _, k := range allow {
		set[k] = struct{}{}
	}
	out := make([]string, 0, len(env))
	for _, e := range env {
		k, _, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if _, keep := set[k]; keep {
			out = append(out, e)
		}
	}
	return out
}

// BuildAgentEnv constructs the environment for an agent data-plane subprocess.
//
// base is intended to be the filtered supervisor environment (typically
// FilterEnvToAllowlist(os.Environ(), agentEnvRuntimeAllowlist)) — NEVER raw
// os.Environ(), which is the root cause of control-plane leakage. providerEnv
// carries the agent's data-plane credentials (API keys, base URLs) the agent
// must keep. sessionEnv carries per-session overrides.
//
// The control-plane deny list (CCCODE_*, OPENCODE_SERVER_*, CLAUDECODE) is
// applied unconditionally to base AND to providerEnv/sessionEnv, then applied a
// second time after merge as belt-and-braces (in case an extra layer smuggles a
// key in). Callers that still need a run_as_user isolation allowlist should run
// FilterEnvForSpawn(BuildAgentEnv(...), spawnOpts) afterwards.
func BuildAgentEnv(base, providerEnv, sessionEnv []string) []string {
	base = stripControlPlaneEnv(base)
	providerEnv = stripControlPlaneEnv(providerEnv)
	sessionEnv = stripControlPlaneEnv(sessionEnv)
	merged := MergeEnv(base, providerEnv)
	merged = MergeEnv(merged, sessionEnv)
	return stripControlPlaneEnv(merged)
}

// CheckAllowFrom logs a security warning at startup when allow_from is not
// configured (defaults to permit-all). Platforms should call this during init.
func CheckAllowFrom(platform, allowFrom string) {
	if strings.TrimSpace(allowFrom) == "" {
		slog.Warn("allow_from is not set — all users are permitted. "+
			"Set allow_from in config to restrict access.",
			"platform", platform)
	}
}

// RedactToken replaces a secret token in text with [REDACTED] to prevent
// token leakage in logs or error messages.
func RedactToken(text, token string) string {
	if token == "" || text == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "[REDACTED]")
}

// AllowList checks whether a user ID is permitted based on a comma-separated
// allow_from string. Returns true if allowFrom is empty or "*" (allow all),
// or if the userID is in the list. Comparison is case-insensitive.
func AllowList(allowFrom, userID string) bool {
	allowFrom = strings.TrimSpace(allowFrom)
	if allowFrom == "" || allowFrom == "*" {
		return true
	}
	for _, id := range strings.Split(allowFrom, ",") {
		if strings.EqualFold(strings.TrimSpace(id), userID) {
			return true
		}
	}
	return false
}

// ImageAttachment represents an image sent by the user.
type ImageAttachment struct {
	MimeType string // e.g. "image/png", "image/jpeg"
	Data     []byte // raw image bytes
	FileName string // original filename (optional)
}

// EventAttachment describes one RECEIVED attachment inside a user message
// (dsh-web S4: journal image/file blocks, official admitPromptContent shape).
// Image bytes are never inlined on the event — clients fetch them lazily via
// the backend's attachment read path keyed by AttachmentID (official
// session/attachment; referencedImage journal-proof + readImage).
type EventAttachment struct {
	Kind         string `json:"kind"`                    // "image" | "file"
	AttachmentID string `json:"attachmentId,omitempty"` // official durable id ("sha256:<hex>")
	MediaType    string `json:"mediaType,omitempty"`    // image only
	Name         string `json:"name,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	Width        int    `json:"width,omitempty"`  // image only
	Height       int    `json:"height,omitempty"` // image only
}

// AttachmentData is one durable image read: the official reference verbatim
// plus the decoded bytes (AttachmentReader / dsh-web session/attachment).
type AttachmentData struct {
	Ref  EventAttachment // kind=image; official ImageAttachmentRef fields
	Data []byte         // decoded image bytes
}

// 附件文件名清理用的常量（P2-3），避免在字符串字面量中混用转义。
const (
	backslash      = "\\"
	forwardSlash   = "/"
	pathSeparators = "/\\:"
)

// FileAttachment represents a file (PDF, doc, spreadsheet, etc.) sent by the user.
type FileAttachment struct {
	MimeType string // e.g. "application/pdf", "text/plain"
	Data     []byte // raw file bytes
	FileName string // original filename
}

// SaveFilesToDisk saves file attachments to workDir/.cccode-macbridge/attachments/
// and returns the list of absolute file paths. Agents can reference these paths
// in their prompts so the CLI can read them with built-in tools.
func SaveFilesToDisk(workDir string, files []FileAttachment) []string {
	if len(files) == 0 {
		return nil
	}
	attachDir := filepath.Join(workDir, ".cccode-macbridge", "attachments")
	if err := os.MkdirAll(attachDir, 0o755); err != nil {
		slog.Warn("SaveFilesToDisk: mkdir failed", "dir", attachDir, "error", err)
	}

	var paths []string
	for i, f := range files {
		fname := safeAttachmentBaseName(f.FileName, i)
		fpath := filepath.Join(attachDir, fname)
		// P2-3: basename 化后再校验最终路径仍在 attachDir 内（防御 symlink/eval 场景）。
		if !isWithinDir(attachDir, fpath) {
			slog.Error("SaveFilesToDisk: rejected path escaping attachment dir", "name", f.FileName, "resolved", fpath)
			continue
		}
		if err := os.WriteFile(fpath, f.Data, 0o644); err != nil {
			slog.Error("SaveFilesToDisk: write failed", "error", err)
			continue
		}
		paths = append(paths, fpath)
		slog.Debug("SaveFilesToDisk: file saved", "path", fpath, "name", f.FileName, "mime", f.MimeType, "size", len(f.Data))
	}
	return paths
}

// safeAttachmentBaseName 将客户端提供的文件名收敛为安全的 basename（P2-3）。
// 拒绝绝对路径与 ../ 逃逸：只取 Base，并对 Windows 风格分隔符与盘符做兜底处理。
// 空名或纯分隔符名回退为时间戳+索引的合成名。
func safeAttachmentBaseName(name string, index int) string {
	// 规范化 Windows 分隔符，避免 filepath.Base 在 unix 上漏判 "C:\\evil"。
	cleaned := strings.ReplaceAll(name, backslash, forwardSlash)
	base := filepath.Base(cleaned)
	// filepath.Base("/") == "/"，filepath.Base("C:") == "C:" 等：回退。
	if base == "" || base == "." || base == "/" || base == string(filepath.Separator) {
		return fmt.Sprintf("file_%d_%d", time.Now().UnixMilli(), index)
	}
	// 再防御一层：若 base 仍含路径分隔符或盘符冒号，回退。
	if strings.ContainsAny(base, pathSeparators) {
		return fmt.Sprintf("file_%d_%d", time.Now().UnixMilli(), index)
	}
	return base
}

// isWithinDir 判断 target（已 Clean）是否位于 dir（已 Clean）之下。
func isWithinDir(dir, target string) bool {
	absDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absTarget)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// AppendFileRefs appends file path references to a prompt string.
func AppendFileRefs(prompt string, filePaths []string) string {
	if len(filePaths) == 0 {
		return prompt
	}
	if prompt == "" {
		prompt = "Please analyze the attached file(s)."
	}
	return prompt + "\n\n(Files saved locally, please read them: " + strings.Join(filePaths, ", ") + ")"
}

// AudioAttachment represents a voice/audio message sent by the user.
type AudioAttachment struct {
	MimeType string // e.g. "audio/amr", "audio/ogg", "audio/mp4"
	Data     []byte // raw audio bytes
	Format   string // short format hint: "amr", "ogg", "m4a", "mp3", "wav", etc.
	Duration int    // duration in seconds (if known)
}

// LocationAttachment represents a geographical location sent by the user.
type LocationAttachment struct {
	Latitude             float64 // latitude coordinate
	Longitude            float64 // longitude coordinate
	HorizontalAccuracy   float64 // accuracy radius in meters (optional)
	LivePeriod           int     // time period for live location updates in seconds (optional)
	Heading              int     // direction of movement in degrees (optional)
	ProximityAlertRadius int     // maximum distance for proximity alerts in meters (optional)
}

// Message represents a unified incoming message from any platform.
type Message struct {
	SessionKey   string // unique key for user context, e.g. "feishu:{chatID}:{userID}"
	Platform     string
	MessageID    string // platform message ID for tracing
	UserID       string
	UserName     string
	ChatName     string // human-readable chat/group name (optional)
	Content      string
	Images       []ImageAttachment   // attached images (if any)
	Files        []FileAttachment    // attached files (if any)
	Audio        *AudioAttachment    // voice message (if any)
	Location     *LocationAttachment // geographical location (if any)
	ExtraContent string              // platform-enriched content (e.g. location text, reply quote) prepended for the agent
	ChannelKey   string              // platform-provided channel identifier for workspace binding (optional)
	ReplyCtx     any                 // platform-specific context needed for replying
	FromVoice    bool                // true if message originated from voice transcription
	ModeOverride string              // if set, temporarily override agent permission mode for this message
}

// EventType distinguishes different kinds of agent output.
type EventType string

const (
	EventText                     EventType = "text"                       // intermediate or final text
	EventTextReplace              EventType = "text_replace"               // full text replacement (non-incremental update)
	EventToolUse                  EventType = "tool_use"                   // tool invocation info
	EventToolResult               EventType = "tool_result"                // tool execution result
	EventPlan                     EventType = "plan"                       // todo/plan update
	EventResult                   EventType = "result"                     // final aggregated result
	EventError                    EventType = "error"                      // error occurred
	EventPermissionRequest        EventType = "permission_request"         // agent requests permission via stdio protocol
	EventPermissionResolved       EventType = "permission_resolved"        // permission was allowed or denied (projection SoT close)
	EventThinking                 EventType = "thinking"                   // thinking/processing status
	EventTurnStarted              EventType = "turn_started"               // new turn started (for passive broadcast)
	EventUserMessage              EventType = "user_message"               // user prompt attributed to a turn (projection SoT)
	EventUserMessageQueued        EventType = "user_message_queued"        // dsh-web inbox splice insert: queued placeholder row keyed by UserMessage.id (S3; wire user_message with pending:true)
	EventUserMessageRemoved       EventType = "user_message_removed"       // dsh-web inbox splice removal: retract the queued placeholder row by UserMessage.id (S3; wire user_message_removed)
	EventTurnFileChanges          EventType = "turn_file_changes"          // turn-level official net file diffs (opencode user-message summary.diffs; upserts the owning turn)
	EventContextCompressing       EventType = "context_compressing"        // context compression started
	EventContextCompressed        EventType = "context_compressed"         // context compression completed
	EventContextUsageUpdated      EventType = "context_usage_updated"      // runtime context usage changed
	EventQuestionAsked            EventType = "question_asked"             // agent asks user a question (Codex)
	EventQuestionResolved         EventType = "question_resolved"          // question was answered or cancelled
	EventUserInputRequested       EventType = "user_input_requested"       // 结构化用户输入交互产生（pending/failed），权威 payload 在 Event.UserInput（设计 §10.1）
	EventUserInputSubmitted       EventType = "user_input_submitted"       // 结构化用户输入已被提交（control response 写成功；Kernel 控制事实，非耐久 resolved；设计 v6 §4.7）
	EventUserInputResolved        EventType = "user_input_resolved"        // 结构化用户输入交互被解决（answered/rejected/auto_resolved/unavailable）
	EventRetryStatus              EventType = "retry_status"               // transient provider-retry notice (serve keeps the turn alive; wire session_retry_status)
	EventSessionCommand           EventType = "session_command"            // host 斜杠命令生命周期（各 backend 按 commandId 折叠；权威 payload 在 Event.SessionCommand）
	EventSessionPlanMode          EventType = "session_plan_mode"          // dsh-web 计划模式投影 {active, pending}（官方 plan projection view；权威 payload 在 Event.PlanMode）
	EventSessionMode              EventType = "session_mode"               // typed 模式状态投影 {status, mode?, canSet, reason?}（Grok Build 方案 §5.1；权威 payload 在 Event.SessionMode）
	EventSessionCollaborationMode EventType = "session_collaboration_mode" // Codex per-thread collaboration settings（官方 thread/settings/updated；权威 payload 在 Event.CollaborationMode）
	EventSessionGoal              EventType = "session_goal"               // dsh-web 目标投影整值快照（官方 goal projection view；权威 payload 在 Event.Goal，nil = 已清除）
	EventSessionGoalRecord        EventType = "session_goal_record"        // Codex thread/goal 官方记录（权威 payload 在 Event.GoalRecord；Goal nil = cleared）
	EventContextInjection         EventType = "context_injection"          // dsh-web 上下文注入行（user/message source.kind!="user"，当前仅 subagent-settled；权威 payload 在 Event.ContextInjection）
	EventWorkflowRun              EventType = "workflow_run"               // dsh-web 并行子代理 workflow 卡整值快照（tool-workflow/* 四事件按 runId 折叠；权威 payload 在 Event.WorkflowRun）
	EventSessionState             EventType = "session_state"              // 控制面执行态投影（codex-remote 官方 thread/status/changed；权威 payload 在 Event.SessionState；词表 running|requiresAction|idle——session-badges 上游对齐方案 §5.2）
)

// UserQuestion represents a structured question from AskUserQuestion.
type UserQuestion struct {
	Question    string               `json:"question"`
	Header      string               `json:"header"`
	Options     []UserQuestionOption `json:"options"`
	MultiSelect bool                 `json:"multiSelect"`
}

// UserQuestionOption is one choice in a UserQuestion.
type UserQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// QuestionOption is one selectable option in a Codex question ask event.
type QuestionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// ── 结构化用户输入 v2 domain model（设计 §6/§10.1）─────────────────────────────
// 这是 user_input projection part 与 EventUserInputRequested/Resolved 的权威 payload。
// projection 不保存答案正文；isSecret=true 时答案不得进入 projection/日志/诊断/snapshot。

// UserInputStatus 是一次结构化用户输入交互的生命周期状态。
type UserInputStatus string

const (
	UserInputStatusPending      UserInputStatus = "pending"
	UserInputStatusSubmitted    UserInputStatus = "submitted" // control response 已写成功（Kernel 控制事实；transcript tool_result 才是耐久 resolved）
	UserInputStatusAnswered     UserInputStatus = "answered"
	UserInputStatusRejected     UserInputStatus = "rejected"
	UserInputStatusAutoResolved UserInputStatus = "auto_resolved"
	UserInputStatusUnavailable  UserInputStatus = "unavailable"
	UserInputStatusFailed       UserInputStatus = "failed"
)

// UserInputAnswerMode 表达单题的回答形态。
type UserInputAnswerMode string

const (
	UserInputAnswerModeSingle   UserInputAnswerMode = "single"
	UserInputAnswerModeMultiple UserInputAnswerMode = "multiple"
	UserInputAnswerModeText     UserInputAnswerMode = "text"
)

// UserInputOption 是一个可选项；ID 由 interactionId/questionId/optionIndex 稳定派生（§6.1）。
type UserInputOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// UserInputQuestion 是一道规范化后的结构化提问。
type UserInputQuestion struct {
	ID                 string              `json:"id"`
	Header             string              `json:"header,omitempty"`
	Prompt             string              `json:"prompt"`
	AnswerMode         UserInputAnswerMode `json:"answerMode"`
	Options            []UserInputOption   `json:"options"`
	AllowsCustomAnswer bool                `json:"allowsCustomAnswer"`
	IsSecret           bool                `json:"isSecret"`
	Required           bool                `json:"required"`
}

// UserInputInteraction 是一次完整的结构化用户输入交互，对应一个 user_input projection part。
// ResolutionSource 取值 ios|mac|other_client|backend。ExpiresAt/ResolvedAt 为 epoch-ms，仅显示。
type UserInputInteraction struct {
	InteractionID    string              `json:"interactionId"`
	Status           UserInputStatus     `json:"status"`
	Questions        []UserInputQuestion `json:"questions"`
	CanRespond       bool                `json:"canRespond"`
	CanReject        bool                `json:"canReject"`
	ExpiresAt        int64               `json:"expiresAt,omitempty"`
	ResolvedAt       int64               `json:"resolvedAt,omitempty"`
	ResolutionSource string              `json:"resolutionSource,omitempty"`
	DiagnosticCode   string              `json:"diagnosticCode,omitempty"`
}

// SessionCommandEvent 是一次 host 斜杠命令生命周期的权威 payload（dsh-web
// 映射官方 command/run + command/done；Grok Build 映射官方 pager 的本地
// manual-compact lifecycle）。各 backend 均按 commandId 折叠。Kind:
// running（run 已见、done 未到）| success | error。Name/Args 来自 run 帧；仅见
// done 时 Name 为空（官方 CommandNode.name 可空，客户端按官方 locale 回退）。
// Text 是官方 settle 文案逐字，或对官方 pager 本地 lifecycle 文案的等值镜像
// （done 无文案时为空，客户端按官方 locale 回退「已完成/指令失败」；绝不从
// 静默时长或消息正文推测命令状态）。
// InputLine 是官方 goal 命令输入行回显（ui-goal goal-command-input.ts
// goalCommandText："/goal" + args.TrimRight；只有 goal 注册 command-input 节点，
// plan/compact 官方无用户气泡）。空 = 非 goal 命令或无 run 帧。
type SessionCommandEvent struct {
	CommandID string `json:"commandId"`
	Name      string `json:"name,omitempty"`
	Args      string `json:"args,omitempty"`
	Kind      string `json:"kind"` // running | success | error
	Text      string `json:"text,omitempty"`
	InputLine string `json:"inputLine,omitempty"`
}

// PlanModeEvent 是 dsh-web 计划模式投影的 {active, pending} 快照（官方 plan
// projection 的 wire view：pending = wanted 非 null 且 wanted != active，即一次
// 尚未在下个 accepted pre-step 落地的模式选择）。客户端 chip 公式与官方一致：
// target = pending ? !active : active；target 为假不显示。
type PlanModeEvent struct {
	Active  bool `json:"active"`
	Pending bool `json:"pending"`
}

// SessionModeEvent 是 typed 模式状态的权威 payload（Grok Build 面板方案
// 2026-09-07 §5.1）。status: confirmed|pending|unknown；mode 仅 confirmed 必须
// 有（plan|default）；canSet=false 表示技术门/归属/状态任一不满足（当前
// Grok 1.0.13 因官方恢复语义阻断——P7）；reason 为稳定原因码。
type SessionModeEvent struct {
	Status string  `json:"status"`
	Mode   *string `json:"mode,omitempty"`
	CanSet bool    `json:"canSet"`
	Reason string  `json:"reason,omitempty"`
}

// SessionStateEvent 是控制面执行态投影的权威 payload（session-badges 上游对齐
// 方案 §5.2）：State ∈ running|requiresAction|idle，源自官方 thread/status/changed
// 的 ThreadStatus 词表映射（Active 无 flags→running、Active 含 flags→requiresAction、
// Idle→idle；SystemError/NotLoaded 不映射——诚实不冒充）。
type SessionStateEvent struct {
	State string `json:"state"`
}

// ContextInjectionEvent 是 dsh-web 上下文注入行的权威 payload（官方
// continuation.ts 注入父会话的 user/message source 逐字段映射；官方 UI 渲染为
// ContextInjectionRow「上下文注入 · <kind> · <summary>」）。当前唯一生产者是
// subagent-settled settle 通知（form="notice"）。Summary 为官方 source.summary
// 逐字（折叠行的一行结算，notice 语义通常不展开即可读）；Text 是 model-facing
// 全文（结算摘要 + 结语，展开体）。SenderSessionID 是 settle 的子会话 id。
// Summary 空则 codec/历史两路都静默丢（fail-open，不造行）。
type ContextInjectionEvent struct {
	ItemID          string `json:"itemId"`
	Kind            string `json:"kind"`
	Form            string `json:"form,omitempty"`
	Summary         string `json:"summary,omitempty"`
	Text            string `json:"text,omitempty"`
	SenderSessionID string `json:"senderSessionId,omitempty"`
}

// WorkflowRunStatus 枚举（官方 WorkflowRunStatus 对位；interrupted 仅由投影
// reducer turn 终态注入，折叠层不产）。
const (
	WorkflowStatusRunning     = "running"
	WorkflowStatusCompleted   = "completed"
	WorkflowStatusFailed      = "failed"
	WorkflowStatusCancelled   = "cancelled"
	WorkflowStatusInterrupted = "interrupted"
)

// WorkflowRunMember 是 workflow 卡一个成员的投影数据（官方 WorkflowRunMemberData
// 对位）。Status: running（agent-start 已见、agent-end 未到）| completed | failed |
// cancelled（agent-end outcome）| interrupted（turn 终态注入，官方 locationClosed）。
type WorkflowRunMember struct {
	Seq            int    `json:"seq"`
	Label          string `json:"label"`
	ChildSessionID string `json:"childSessionId,omitempty"`
	Status         string `json:"status"`
}

// WorkflowRunPhase 是按 phase 身份分组的成员表（官方 WorkflowRunPhaseData 对位）。
// Phase 三态身份：nil = 未分阶段（官方 phase undefined → null → key "missing"）；
// 非 nil 空串 = 空阶段名（官方 value:0: 独立身份）；非空 = 阶段名。分组按首现顺序。
type WorkflowRunPhase struct {
	Phase   *string             `json:"phase"`
	Members []WorkflowRunMember `json:"members"`
}

// WorkflowRunEvent 是 dsh-web 并行子代理 workflow 卡的整值快照（官方
// ui-workflow-run workflow-definition.ts 折叠 + projectWorkflow 视图投影的
// CordCode wire view；live codec 与 history 冷拉经 workflow_fold.go 单一折叠真值
// 产出）。Name 是 run-start 的官方 run 名；Status: running（run-end 未到）|
// completed | cancelled | failed（run-end stopReason 映射）| interrupted（reducer
// turn 终态注入——折叠层不知 turn 闭合，官方 locationClosed 语义由投影层补）。
type WorkflowRunEvent struct {
	RunID  string             `json:"runId"`
	Name   string             `json:"name"`
	Status string             `json:"status"`
	Phases []WorkflowRunPhase `json:"phases"`
}

// GoalBlockedReason 是目标受阻时的官方规范化解释（goal/change 全量快照内字段）。
type GoalBlockedReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// GoalEvent 是 dsh-web 目标投影的整值快照（官方 goal projection 的 wire view：
// goal/change 为全量快照替换语义，无折叠）。渲染契约镜像官方 GoalBar：
// phase==complete 或无目标不渲染横条；active/paused/blocked 显示相位标签 +
// objective + 动作（active→pause、paused→resume、恒有 edit/clear）。
// Event.Goal 为 nil 表示目标已被清除（clear 墓碑）。
type GoalEvent struct {
	ID            string             `json:"id"`
	Revision      int64              `json:"revision"`
	Objective     string             `json:"objective"`
	Phase         string             `json:"phase"` // active | paused | blocked | complete
	BlockedReason *GoalBlockedReason `json:"blockedReason,omitempty"`
	MaxGoalRounds int                `json:"maxGoalRounds,omitempty"`
	// VerifyingCompletion is Grok's authoritative live boundary between the
	// user-visible implementation round and its hidden completion evaluator.
	VerifyingCompletion bool `json:"verifyingCompletion,omitempty"`
}

// FileChange describes one structured file mutation emitted by an agent.
type FileChange struct {
	Path     string
	Kind     string
	Diff     string
	MovePath string
	// Additions/Deletions carry the backend's official per-file edit counts when
	// the payload provides them (opencode-web filediff / apply_patch metadata.files,
	// Claude structuredPatch hunk counting). Pointer semantics keep a legitimate
	// 0 distinct from "no official number" (nil): wire encoding writes a present
	// value verbatim and omits nil, so clients never see 0 fabricated for missing
	// data. A complete pair (both non-nil) is the only official statistic; consumers
	// must not mix one official number with a diff-derived one.
	Additions *int
	Deletions *int
}

// ToolMatchItem is one structured search/explore result. Preview is optional
// agent-provided evidence; callers must not synthesize it from display text.
type ToolMatchItem struct {
	Path    string `json:"path"`
	Line    *int   `json:"line,omitempty"`
	Preview string `json:"preview,omitempty"`
}

// ToolMatches is the single structured truth for explore results.
// Kind is count, paths, or detailed; fields not selected by Kind stay empty.
type ToolMatches struct {
	Kind  string          `json:"kind"`
	Count *int            `json:"count,omitempty"`
	Paths []string        `json:"paths,omitempty"`
	Items []ToolMatchItem `json:"items,omitempty"`
}

// PlanPayload is the plan document attached to a plan-review permission
// request (PermissionKind == "plan_review"; grok exit_plan_mode / claude
// ExitPlanMode / dsh plan-review question). Content is the full plan text
// (markdown; may be empty when the backend broadcast a plan approval with no
// plan content). Title and PlanFilePath are source-proven only — never
// synthesized by the bridge.
type PlanPayload struct {
	Content       string `json:"content"`
	ContentFormat string `json:"contentFormat,omitempty"` // "markdown" (only format today)
	Title         string `json:"title,omitempty"`
	PlanFilePath  string `json:"planFilePath,omitempty"`
}

// Event represents a single piece of agent output streamed back to the engine.
type Event struct {
	Type         EventType
	Content      string
	ToolName     string         // populated for EventToolUse, EventPermissionRequest
	ToolInput    string         // human-readable summary of tool input
	ToolInputRaw map[string]any // raw tool input (for EventPermissionRequest, used in allow response)
	ToolResult   string         // populated for EventToolResult
	ToolStatus   string         // optional status for EventToolResult (e.g. completed/failed)
	ToolExitCode *int           // optional exit code for EventToolResult
	ToolSuccess  *bool          // optional success flag for EventToolResult
	SessionID    string         // agent-managed session ID for conversation continuity
	RequestID    string         // unique request ID for EventPermissionRequest
	// Official permission.asked payload (opencode-web v1.18, live-pinned):
	// permission kind + patterns are what the official desktop renders
	// (category line + pattern rows); "always" replies persist these.
	PermissionKind     string   // e.g. "external_directory"; empty for backends without official payload
	PermissionPatterns []string // e.g. ["/Users/x/Projects/Chat/*"]
	// PermissionActions is the exact UI action vocabulary supported by this
	// pending request. Empty preserves the legacy client default; Codex Web sets
	// approve/reject because its official wire has no persistent "always" reply.
	PermissionActions []string // approve | approveAlways | reject | rejectAlways | plan actions (requestChanges | quit)
	// PlanReview carries the plan document for permission requests whose
	// PermissionKind == "plan_review" (plan approval layer). nil for every other
	// permission request. It rides the existing permission_request control-plane
	// event only — never the messages timeline, never a second projection writer.
	PlanReview *PlanPayload
	TurnID     string // source-proven turn identity (Codex/Claude/OpenCode turn id; projection lifecycle)
	ItemID     string // source-proven item identity (assistant text/reasoning/tool part id)
	// DurationMs mirrors the official Turn.durationMs ("Duration between turn start and
	// completion in milliseconds, if known" — codex app-server-protocol v2/Turn.ts). 0 =
	// unknown; sources that do not provide it leave 0 and consumers fall back to timestamps.
	DurationMs int64
	// StopReason preserves the agent's own turn terminal (grok-build
	// session/prompt result.stopReason: end_turn/cancelled/max_tokens/refusal)
	// on the terminal EventResult. Empty for backends without a stop reason —
	// Done alone remains the settle signal, never the reason.
	StopReason string
	// CancellationCategory is the official _meta.cancellationCategory wire
	// value (MidTurnAbort/HookDenied/PermissionRejected/PermissionCancelled)
	// riding the same terminal event; "" when the turn was not cancelled.
	CancellationCategory string
	Questions            []UserQuestion // populated when ToolName == "AskUserQuestion"
	Plan                 []Todo         `json:",omitempty"`
	Done                 bool
	Error                error
	InputTokens          int // token usage from agent result events
	OutputTokens         int
	ContextUsage         *ContextUsage
	FileChanges          []FileChange
	ToolMatches          *ToolMatches
	StreamID             string // stable child stream identity; empty means the main stream
	// Attachments carries received user-message attachments (dsh-web S4:
	// journal image/file blocks). Populated for EventUserMessage and
	// EventUserMessageQueued; nil for every other event type.
	Attachments []EventAttachment
	ParentStreamID       string // optional parent child-stream identity
	RetryAttempt         int    // populated for EventRetryStatus (1-based serve retry attempt)
	RetryNext            int64  // populated for EventRetryStatus (serve epoch-ms when the next attempt fires)
	// question 相关字段
	QuestionID   string           // question 唯一标识 (Codex ask)
	QuestionText string           // question prompt 文本
	QuestionOpts []QuestionOption // 可选项
	Required     bool             // 是否必须回答
	ThreadID     string           // Codex thread id
	// user_input v2 结构化交互（EventUserInputRequested/Resolved 的权威 payload）。
	// 旧单题 QuestionID/QuestionText/QuestionOpts 字段只服务 legacy `.off` 路径；
	// v2 adapter 只填充 UserInput（设计 §10.1）。projection 不保存答案正文。
	UserInput *UserInputInteraction
	// host 斜杠命令生命周期（EventSessionCommand 的权威 payload）。
	// 折叠语义镜像官方 conversation-nodes/command.ts（run→running 行，done→settle）。
	SessionCommand *SessionCommandEvent
	// dsh-web 计划模式投影快照（EventSessionPlanMode 的权威 payload）。
	PlanMode *PlanModeEvent
	// typed 模式状态投影（EventSessionMode 的权威 payload；Grok 方案 §5.1）。
	SessionMode *SessionModeEvent
	// 控制面执行态投影（EventSessionState 的权威 payload；codex-remote 官方
	// thread/status/changed 按 §5.2 词表映射——running|requiresAction|idle）。
	// 纯控制面：消费面是 registry/runtimeStateStore，绝不写 timeline。
	SessionState *SessionStateEvent
	// Codex collaboration mode is an official per-thread settings snapshot.
	// It never aliases permission mode or the Grok/dsh mode projections.
	CollaborationMode *SessionCollaborationMode
	// dsh-web 目标投影整值快照（EventSessionGoal 的权威 payload；nil = 已清除）。
	Goal *GoalEvent
	// Codex goal record uses the official thread-goal shape and remains
	// separate from dsh GoalEvent's id/revision/phase contract.
	GoalRecord *SessionGoalSnapshot
	// dsh-web 上下文注入行（EventContextInjection 的权威 payload；官方
	// ContextMessageNode 对位——user/message source.kind!="user" 的注入上下文，
	// 当前只 subagent-settled settle 通知）。
	ContextInjection *ContextInjectionEvent
	// dsh-web 并行子代理 workflow 卡整值快照（EventWorkflowRun 的权威 payload；
	// 官方 ui-workflow-run WorkflowRunChatData 对位，折叠真值在
	// agent/dsh-web/workflow_fold.go）。
	WorkflowRun *WorkflowRunEvent
}

// HistoryEntry is one turn in a conversation.
type HistoryEntry struct {
	Role      string    `json:"role"` // "user" or "assistant"
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// RichHistoryEntry is a backward-compatible superset for backends that can
// return structured message history with parts, steps, and thinking blocks.
// Callers should continue to honor Role/Content/Timestamp as the minimal
// compatibility surface and treat the richer fields as optional enhancements.
type RichHistoryEntry struct {
	ID        string           `json:"id,omitempty"`
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Thinking  string           `json:"thinking,omitempty"`
	Parts     []map[string]any `json:"parts,omitempty"`
	Steps     []map[string]any `json:"steps,omitempty"`
	Files     []map[string]any `json:"files,omitempty"`
	Timestamp time.Time        `json:"timestamp"`
	// TurnStartedAt / TurnCompletedAt are optional, source-proven wall-clock
	// boundaries. They are never synthesized from tool durations.
	TurnStartedAt   *time.Time `json:"turnStartedAt,omitempty"`
	TurnCompletedAt *time.Time `json:"turnCompletedAt,omitempty"`
	AgentName       string     `json:"agentName,omitempty"`
	ModelID         string     `json:"modelId,omitempty"`
	ProviderID      string     `json:"providerId,omitempty"`
	ModelName       string     `json:"modelName,omitempty"`
	// ContextInjection 非空 = Role "context_injection" 行（dsh subagent-settled
	// settle 通知的冷拉载体；Content 同 Text）。其他 role 恒 nil。
	ContextInjection *ContextInjectionEvent `json:"contextInjection,omitempty"`
	// TurnFileChanges 是回合级官方净diff（opencode user-message
	// info.summary.diffs：{file, patch, additions, deletions, status}）。非空时
	// hydrate 把它作为该回合 user_message 事件的 fileChanges 附带——reducer 写
	// TurnProjection.FileChanges（turn 级权威，客户端文件盒优先消费）。逐工具
	// per-call fileChanges 仍是 L2/L3 工具行数据源，两者不混写。
	TurnFileChanges []map[string]any `json:"turnFileChanges,omitempty"`
	// Attachments 是该 user 行收到的附件描述符（dsh-web S4：journal image/file
	// 块）。图片字节不内联——客户端按 AttachmentID 经 get_attachment 懒取。
	Attachments []EventAttachment `json:"attachments,omitempty"`
}

// Todo represents one backend-managed todo item for a session.
type Todo struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

// AgentDescriptor describes an available agent profile exposed by a backend.
type AgentDescriptor struct {
	Name        string `json:"name"`
	Mode        string `json:"mode,omitempty"`
	Hidden      bool   `json:"hidden,omitempty"`
	Native      bool   `json:"native,omitempty"`
	Description string `json:"description,omitempty"`
	// DisplayName is the official UI label (e.g. 极简模式). Name stays the
	// stable id used on create/select (e.g. minimal).
	DisplayName string `json:"displayName,omitempty"`
	IsDefault   bool   `json:"isDefault,omitempty"`
}

// SessionPin is the identity-only pin (置顶) record persisted by a SessionPinner driver.
// It deliberately carries NO summary fields (title/messageCount/modifiedAt): those remain
// backend-owned and are resolved on demand by the go-bridge handler when building
// AgentSessionInfo for list_pinned_sessions / set_session_pinned responses. Keeping the
// pin store limited to identity + pinnedAt avoids stale pinned-row summaries.
//
// Directory is the scope hint the pin was recorded with (the request directory for
// OpenCode, the resolved project dir for Claude, empty/unused where not meaningful). It
// is also the input the handler uses to resolve the summary (e.g. OpenCodeProxy.getSession).
type SessionPin struct {
	BackendID string
	SessionID string
	Directory string
	PinnedAt  time.Time
}

// AgentSessionInfo describes one session as reported by the agent backend.
type AgentSessionInfo struct {
	ID           string
	Summary      string
	MessageCount int
	ModifiedAt   time.Time `json:"modified_at"`
	ArchivedAt   time.Time `json:"archived_at,omitempty"`
	// PinnedAt is non-zero when the user pinned (置顶) this session. It is MacBridge-owned
	// metadata (NOT agent-local state): Claude stores it in the .cc-connect-session-meta
	// sidecar; Codex/OpenCode store it in the bridge-owned pin index. The wire field is
	// pinnedAtMillis (emitted by sessionsToWire / mapSession); pin/unpin MUST NOT alter
	// ModifiedAt. See docs/protocol/bridge-v1.md「Session Pinning」.
	PinnedAt        time.Time `json:"pinned_at,omitempty"`
	GitBranch       string
	Directory       string
	ModelID         string
	ProviderID      string
	ReasoningEffort string
	// AgentPreset is the official dsh-web agent preset id (standard/code/minimal/cordis).
	// Empty for backends that do not have presets. Wire field: agentPreset.
	AgentPreset string
	// RuntimeStateHint is a per-fetch catalog-derived execution hint ("running" |
	// "requiresAction"; empty = none). Filled only by backends whose catalog response
	// carries authoritative per-session status (codex-remote official thread/list
	// ThreadStatus). Consumed by the list/single-session runtime-state overlay points
	// as an UPGRADE-only signal (never downgrades registry live state); never emitted
	// on the wire (planted as a temp key, stripped at the overlay).
	RuntimeStateHint string `json:"-"`
	// OutcomeHint is a per-fetch catalog-derived settle-outcome hint ("failed";
	// empty = none). Filled only by codex-remote: official thread/list ThreadStatus
	// "systemError" (upstream thread_status.rs — last turn ended in a system error,
	// persists until the next turn starts; the ChatGPT desktop renders it as the
	// thread's ❗). Consumed by the list outcome overlay as the persistent official
	// truth (survives bridge restarts, unlike the in-memory registry outcome
	// side-store); never emitted on the wire (planted as a temp key, stripped at
	// the overlay). Fresh per fetch on the list/membership paths; the recent view
	// serves a TTL snapshot so it may lag there (same accepted staleness as
	// RuntimeStateHint).
	OutcomeHint string `json:"-"`
}

// BackgroundTask is the backend-neutral read-only background-task summary
// (docs/protocol/bridge-v1.md「Background Tasks」; roadmap §3.2). Sources must be
// real agent state — Claude sidechain meta/JSONL (the same files B4 hydrates,
// single-derivation rule C1) or DSH official session.list subagent rows. Fields
// the backend does not know stay zero/empty (wire omits them; iOS never renders
// unknown as 0).
type BackgroundTask struct {
	TaskID              string
	BackendID           string
	RootSessionID       string
	ParentTaskID        string // nested parent (Claude parentAgentId / DSH subagent-of-subagent); "" for depth-1
	DurationMillis      int64  // explicit work wall time (DSH sessionStats llmMs+toolMs); 0 = unknown, wire omits
	AgentID             string // Claude sidechain agent id / DSH sub-session id
	Title               string // task instruction/description (real text, not invented)
	AgentName           string // general-purpose 等
	Status              string // queued|running|completed|failed|cancelled
	StartedAt           time.Time
	FinishedAt          time.Time
	TokenCount          int64 // 0 = unknown
	ToolUseCount        int64 // 0 = unknown
	Error               string
	TranscriptAvailable bool // detail/read path exists
	UpdatedAt           time.Time
}

// BackgroundTaskDetail is the read-only detail (background_tasks.get).
// Phase 4 is read-only: CanCancel/CanRetry stay false until the capability-gated
// operations land (roadmap Phase D).
type BackgroundTaskDetail struct {
	Task        BackgroundTask
	Instruction string
	NestedTasks []BackgroundTask
	CanCancel   bool
	CanRetry    bool
}

// SessionModelSelection is a session's authoritative current model selection,
// reported by a SessionModelSelectionReader (e.g. dsh-web session.models →
// current{provider, model, reasoningEffort}). Fields are empty when the
// backend did not provide them; readers must never invent values.
type SessionModelSelection struct {
	Provider        string
	Model           string
	ReasoningEffort string
}
