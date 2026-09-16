package claudecode

// user_input.go 是 Claude Code 结构化用户输入 v2 适配器（设计 §9）。
//
// AskUserQuestion always enters this canonical path before permission-mode bypass. Legacy
// question_asked/resolved frames are a one-way presentation derived from the same registry;
// legacy question_reply/question_reject and v2 resolve_user_input compete for one claim.
//
// 关键不变量（设计已冻结）：
//   - Claude AskUserQuestion 支持客户端提供的 Other/custom 文本；该文本仍按 single=string、
//     multiple=string[] 写入 updatedInput.answers；
//   - multiSelect false→single、true→multiple；options 缺失属 malformed（SDK 本应在 control_request
//     前拒绝），不归一化为 text；
//   - 每题 required=true、isSecret=false；questionId/optionId 由 requestId+index 派生；
//   - 多问题 question text 重复 → invalid_backend_request（无法作为 answers map key 无歧义表达）；
//   - answer/reject 先原子 claim 再写 control_response；写成功才 ConfirmResolved，写失败 ReleaseClaim
//     回 pending（修复 v1 LoadAndDelete 后写失败即丢请求的顺序）；
//   - reject 写同一 request_id 的 control_response（subtype=success/behavior=deny/
//     message="User skipped the question."，无 updatedInput）→ status=rejected。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ── 稳定 ID 派生（§6.1，Claude 分支）─────────────────────────────────────────
// 与 codex 包的派生语义一致（小写十六进制 SHA-256 前 32 字符、"ui_" 前缀），但 Claude 的 hash
// 输入是 "claudecode\0" + toolUseID。本地定义避免跨 adapter 包依赖；canonical 语义由本文件单测锁死。
//
// 双 identity（设计 v6 §2.1，R5-P0-1）：control envelope 外层 request_id 只用于
// control_response 配对；Ask 的 request.tool_use_id（= transcript assistant tool_use id =
// user tool_result tool_use_id）是唯一 timeline canonical seed。两者缺一不可且不可互换：
// 真实 2.1.209 配对 fixture 证明二者恒不相等，用 request_id 派生 interactionId 会与
// transcript mapper 确定性分叉成两张卡。

const (
	claudeSUIHexLen = 32
	claudeSUIPrefix = "ui_"
	// StructuredUserInputReady is the Claude adapter readiness source used by both
	// the production control-request path and backend capability advertisement.
	StructuredUserInputReady = true
)

// claudeAskAnsweringEnabled 是 iOS 作答能力的证据门开关（设计 v6 §0 交付门/§4.6）。
// Claude Code 2.1.261 answer/deny 配对样本（完整 control request/response + transcript
// claudeAskAnsweringEnabled：live Claude AskUserQuestion 可答翻转（设计 v6 §0/§4.6）。
// 2026-09-16 证据门 PASSED（CLI 2.1.261，answer envelope + deny 形状双场景 fixture
// 锁定，见 scripts/claudecode-phase0/fixtures/ask-evidence-2026-09-16/）后翻转为
// true：live requested 的 canRespond 由活控制通道证明（registry pending + session
// alive）。测试用 withClaudeAskAnswering 临时翻转（含验证 fail-closed 的场景）。
var claudeAskAnsweringEnabled = true

// withClaudeAskAnswering 在 fn 执行期间临时翻转证据门开关（仅测试使用）。
func withClaudeAskAnswering(enabled bool, fn func()) {
	prev := claudeAskAnsweringEnabled
	claudeAskAnsweringEnabled = enabled
	defer func() { claudeAskAnsweringEnabled = prev }()
	fn()
}

// ClaudeAskAnsweringEnabled reports whether the 2.1.261 evidence gate has flipped the
// iOS answering capability on. Exposed for the transcript-side answerability oracle
// (go-bridge) so live and cold paths gate on the same switch.
func ClaudeAskAnsweringEnabled() bool { return claudeAskAnsweringEnabled }

func deriveClaudeInteractionID(toolUseID string) string {
	h := sha256.New()
	h.Write([]byte("claudecode\x00"))
	h.Write([]byte(toolUseID))
	sum := h.Sum(nil)
	return claudeSUIPrefix + hex.EncodeToString(sum[:claudeSUIHexLen/2])
}

// DeriveStructuredUserInputInteractionID exposes the Claude structured-input identity
// derivation to transcript consumers. Passive transcript projection must derive the same
// interactionId as the live Claude adapter from the persisted tool_use id; duplicating the
// hash here would let cold/live paths disagree silently.
func DeriveStructuredUserInputInteractionID(toolUseID string) string {
	return deriveClaudeInteractionID(toolUseID)
}

// HasStructuredUserInputResultEnvelope recognizes the persisted Claude Desktop
// AskUserQuestion resolution envelope. Callers use presence only: answer values stay out of
// projection/history so a passive observer never duplicates private response content.
func HasStructuredUserInputResultEnvelope(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var envelope struct {
		Questions json.RawMessage `json:"questions"`
		Answers   json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false
	}
	return len(envelope.Questions) > 0 && string(envelope.Questions) != "null" &&
		len(envelope.Answers) > 0 && string(envelope.Answers) != "null"
}

// IsStructuredUserInputDeniedResult recognizes the persisted deny shape of an
// AskUserQuestion resolution (evidence gate 2026-09-16, CLI 2.1.261, scenarios
// ask-file-deny/ask-deny): frame-level tool_use_result is the plain string
// "Error: User declined to answer this question." — no questions/answers
// envelope, and the message.content tool_result block carries is_error=true
// with content "User declined to answer this question.". Callers must gate on
// the owning tool_result belonging to an AskUserQuestion (toolUseMeta) before
// treating this as a user-input rejection.
func IsStructuredUserInputDeniedResult(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(s), "Error: User declined to answer this question")
}

func claudeQuestionID(interactionID string, questionIndex int) string {
	return fmt.Sprintf("%s_q_%d", interactionID, questionIndex)
}

func claudeOptionID(questionID string, optionIndex int) string {
	return fmt.Sprintf("%s_o_%d", questionID, optionIndex)
}

// ── pending registry（first-writer-wins + clientActionID 幂等）─────────────────

type claudeUIEntryStatus int

const (
	claudeEntryPending claudeUIEntryStatus = iota
	claudeEntryClaimed
	claudeEntryResolved
)

// claudeUIStatus 是对外的离散查询结果。
type claudeUIStatus int

const (
	claudeUIAbsent claudeUIStatus = iota
	claudeUIPending
	claudeUIClaimed
	claudeUIResolved
)

type claudePendingOption struct {
	id    string // 派生 option id
	label string // Claude answers map 期望的 option label
}

// claudeUIEntry 保存回答所需的原始 identity：Claude control_request request_id（仅控制回包
// 配对）、tool_use_id（timeline canonical seed，与 transcript mapper 同键）、原始 input
// （shallowCopy 基底）、question text → mode/options 映射。
type claudeUIEntry struct {
	interactionID      string
	requestID          string
	toolUseID          string
	owningTurnID       string
	rawInput           map[string]any
	questionMode       map[string]core.UserInputAnswerMode // questionText → single|multiple
	questionOpts       map[string][]claudePendingOption    // questionText → options
	questionOrder      []string                            // questionText 原序
	status             claudeUIEntryStatus
	resolvedAt         time.Time
	resolver           string
	outcomeByAction    map[string]core.UserInputResolutionOutcome
	activeClientAction string
	// transition generation/channel（设计 v6 §4.5.1）：ConfirmCommitted/ReleaseClaim/
	// Remove/Clear 推进 generation 并关闭旧 channel 唤醒等待方。等待方醒来后循环重判。
	transitionGen uint64
	transitionCh  chan struct{}
}

// advanceTransitionLocked 推进 entry 的 transition generation 并唤醒所有等待方。
// 调用方必须持有 r.mu。
func (r *claudeUserInputRegistry) advanceTransitionLocked(e *claudeUIEntry) {
	if e.transitionCh != nil {
		close(e.transitionCh)
	}
	e.transitionGen++
	e.transitionCh = make(chan struct{})
}

// claudeClaimSnapshot 是 Claim 成功时返回的只读视图，供 session 层序列化 control_response。
type claudeClaimSnapshot struct {
	interactionID string
	requestID     string
	toolUseID     string
	owningTurnID  string
	rawInput      map[string]any
	questionMode  map[string]core.UserInputAnswerMode
	questionOpts  map[string][]claudePendingOption
	questionOrder []string
}

type claudeClaimDecision struct {
	claimed  bool
	snapshot *claudeClaimSnapshot
	outcome  core.UserInputResolutionOutcome
	status   claudeUIStatus
	// waitCh/waitGen（§4.5.1）：entry 已被其他 claimant 占用时非 nil——调用方在锁外
	// 等待该 channel 关闭（transition 发生）后循环重判。
	waitCh chan struct{}
	waitGen uint64
}

type claudeUserInputRegistry struct {
	mu        sync.Mutex
	entries   map[string]*claudeUIEntry
	byRequest map[string]string
}

func newClaudeUserInputRegistry() *claudeUserInputRegistry {
	return &claudeUserInputRegistry{
		entries:   make(map[string]*claudeUIEntry),
		byRequest: make(map[string]string),
	}
}

func (r *claudeUserInputRegistry) Register(e claudeUIEntry) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[e.interactionID]; ok {
		return false
	}
	e.status = claudeEntryPending
	e.transitionCh = make(chan struct{})
	r.entries[e.interactionID] = &e
	if e.requestID != "" {
		r.byRequest[e.requestID] = e.interactionID
	}
	return true
}

func (r *claudeUserInputRegistry) SnapshotByRequest(requestID string) (*claudeClaimSnapshot, claudeUIStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	interactionID := r.byRequest[requestID]
	e := r.entries[interactionID]
	if e == nil {
		return nil, claudeUIAbsent
	}
	status := claudeUIPending
	switch e.status {
	case claudeEntryClaimed:
		status = claudeUIClaimed
	case claudeEntryResolved:
		status = claudeUIResolved
	}
	return claudeSnapshotOf(e), status
}

// SnapshotByInteraction 按 interactionId 返回 entry 快照（submitted 重发需要
// registry 保存的 turn/item/tool-use 身份，§4.5.2）。
func (r *claudeUserInputRegistry) SnapshotByInteraction(interactionID string) (*claudeClaimSnapshot, claudeUIStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[interactionID]
	if e == nil {
		return nil, claudeUIAbsent
	}
	status := claudeUIPending
	switch e.status {
	case claudeEntryClaimed:
		status = claudeUIClaimed
	case claudeEntryResolved:
		status = claudeUIResolved
	}
	return claudeSnapshotOf(e), status
}

func (r *claudeUserInputRegistry) Status(interactionID string) claudeUIStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[interactionID]
	if !ok {
		return claudeUIAbsent
	}
	switch e.status {
	case claudeEntryPending:
		return claudeUIPending
	case claudeEntryClaimed:
		return claudeUIClaimed
	case claudeEntryResolved:
		return claudeUIResolved
	}
	return claudeUIAbsent
}

func (r *claudeUserInputRegistry) Claim(interactionID, clientActionID string) claudeClaimDecision {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[interactionID]
	if !ok {
		return claudeClaimDecision{status: claudeUIAbsent}
	}
	if clientActionID != "" {
		if out, hit := e.outcomeByAction[clientActionID]; hit {
			return claudeClaimDecision{outcome: out, status: claudeUIResolved}
		}
	}
	switch e.status {
	case claudeEntryResolved:
		return claudeClaimDecision{outcome: core.UserInputOutcomeAlreadyResolved, status: claudeUIResolved}
	case claudeEntryClaimed:
		// §4.5.1：已占用不立即成功返回——调用方拿 waiter 在锁外等待 transition，
		// 醒来后重判（committed→already_resolved / released→重 Claim）。
		return claudeClaimDecision{
			status:  claudeUIClaimed,
			waitCh:  e.transitionCh,
			waitGen: e.transitionGen,
		}
	case claudeEntryPending:
		e.status = claudeEntryClaimed
		e.activeClientAction = clientActionID
		return claudeClaimDecision{claimed: true, snapshot: claudeSnapshotOf(e), status: claudeUIClaimed}
	}
	return claudeClaimDecision{status: claudeUIAbsent}
}

// WaitTransition 在锁外等待 entry 的下一次 transition（§4.5.1）。返回等待后的最新
// status；entry 消失返回 claudeUIAbsent；ctx 结束返回 ctx.Err()。不得持 registry
// mutex 等待 channel。
func (r *claudeUserInputRegistry) WaitTransition(interactionID string, lastGen uint64, ctx context.Context) (claudeUIStatus, error) {
	r.mu.Lock()
	e, ok := r.entries[interactionID]
	if !ok {
		r.mu.Unlock()
		return claudeUIAbsent, nil
	}
	ch := e.transitionCh
	gen := e.transitionGen
	r.mu.Unlock()
	if ch == nil || gen != lastGen {
		// 已发生过 transition（或 entry 重建）：直接返回当前状态供重判。
		return r.Status(interactionID), nil
	}
	select {
	case <-ch:
		return r.Status(interactionID), nil
	case <-ctx.Done():
		return claudeUIAbsent, ctx.Err()
	}
}

func (r *claudeUserInputRegistry) ConfirmResolved(interactionID, clientActionID, resolver string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[interactionID]
	if !ok || e.status != claudeEntryClaimed {
		return false
	}
	e.status = claudeEntryResolved
	e.resolvedAt = time.Now()
	if resolver != "" {
		e.resolver = resolver
	}
	if clientActionID != "" {
		if e.outcomeByAction == nil {
			e.outcomeByAction = make(map[string]core.UserInputResolutionOutcome)
		}
		e.outcomeByAction[clientActionID] = core.UserInputOutcomeAccepted
	}
	e.activeClientAction = ""
	r.advanceTransitionLocked(e)
	return true
}

func (r *claudeUserInputRegistry) ReleaseClaim(interactionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[interactionID]
	if !ok || e.status != claudeEntryClaimed {
		return false
	}
	e.status = claudeEntryPending
	e.activeClientAction = ""
	r.advanceTransitionLocked(e)
	return true
}

// RemoveByRequest 按 control request ID 找到 entry 并移除（§4.5.1：
// control_cancel_request 必须按 request ID Remove 并唤醒等待方）。
func (r *claudeUserInputRegistry) RemoveByRequest(requestID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	interactionID := r.byRequest[requestID]
	e := r.entries[interactionID]
	if e == nil {
		return false
	}
	delete(r.byRequest, requestID)
	delete(r.entries, interactionID)
	r.advanceTransitionLocked(e)
	return true
}

func (r *claudeUserInputRegistry) Remove(interactionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[interactionID]
	if e == nil {
		return
	}
	if e.requestID != "" {
		delete(r.byRequest, e.requestID)
	}
	delete(r.entries, interactionID)
	r.advanceTransitionLocked(e)
}

func (r *claudeUserInputRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		r.advanceTransitionLocked(e)
	}
	r.entries = make(map[string]*claudeUIEntry)
	r.byRequest = make(map[string]string)
}

func claudeSnapshotOf(e *claudeUIEntry) *claudeClaimSnapshot {
	mode := make(map[string]core.UserInputAnswerMode, len(e.questionMode))
	for k, v := range e.questionMode {
		mode[k] = v
	}
	opts := make(map[string][]claudePendingOption, len(e.questionOpts))
	for k, v := range e.questionOpts {
		cp := make([]claudePendingOption, len(v))
		copy(cp, v)
		opts[k] = cp
	}
	order := make([]string, len(e.questionOrder))
	copy(order, e.questionOrder)
	return &claudeClaimSnapshot{
		interactionID: e.interactionID,
		requestID:     e.requestID,
		toolUseID:     e.toolUseID,
		owningTurnID:  e.owningTurnID,
		rawInput:      e.rawInput,
		questionMode:  mode,
		questionOpts:  opts,
		questionOrder: order,
	}
}

// ── 归一化（§9.2）──────────────────────────────────────────────────────────────

// normalizeClaudeUserQuestions 把 parseUserQuestions 的结果映射到 domain UserInputQuestion。
// multiSelect false→single、true→multiple；allowsCustomAnswer=true（Claude 客户端提供 Other）；required=true；
// isSecret=false。options 必须非空（SDK 在 control_request 前已拒绝无 options 的调用）。
// question text 重复 → error（无法作为 answers map key 无歧义表达）。
func normalizeClaudeUserQuestions(interactionID string, parsed []core.UserQuestion) ([]core.UserInputQuestion, error) {
	seen := map[string]bool{}
	out := make([]core.UserInputQuestion, 0, len(parsed))
	for i, q := range parsed {
		qText := q.Question
		if seen[qText] {
			return nil, fmt.Errorf("duplicate question text %q (cannot be unambiguous answers map key)", qText)
		}
		seen[qText] = true
		if len(q.Options) == 0 {
			return nil, fmt.Errorf("question %q has no options (SDK should reject before control_request)", qText)
		}
		qid := claudeQuestionID(interactionID, i)
		mode := core.UserInputAnswerModeSingle
		if q.MultiSelect {
			mode = core.UserInputAnswerModeMultiple
		}
		opts := make([]core.UserInputOption, 0, len(q.Options))
		for j, o := range q.Options {
			opts = append(opts, core.UserInputOption{
				ID:          claudeOptionID(qid, j),
				Label:       o.Label,
				Description: o.Description,
			})
		}
		out = append(out, core.UserInputQuestion{
			ID:                 qid,
			Header:             q.Header,
			Prompt:             q.Question,
			AnswerMode:         mode,
			Options:            opts,
			AllowsCustomAnswer: true,
			IsSecret:           false,
			Required:           true,
		})
	}
	return out, nil
}

// NormalizeStructuredUserInputQuestions parses and normalizes a Claude AskUserQuestion input
// using the same rules as the live responder path. It is intentionally exported for the
// transcript relay, which observes Claude Desktop sessions without owning their responder handle.
func NormalizeStructuredUserInputQuestions(interactionID string, input map[string]any) ([]core.UserInputQuestion, error) {
	return normalizeClaudeUserQuestions(interactionID, parseUserQuestions(input))
}

// buildClaudePendingEntry 把规范化结果组装成 registry entry。
// parsed[i] 与 normalized[i] 一一对应（normalize 成功时不跳过任何题）。
func buildClaudePendingEntry(interactionID, requestID, toolUseID, owningTurnID string, rawInput map[string]any, parsed []core.UserQuestion, normalized []core.UserInputQuestion) claudeUIEntry {
	mode := make(map[string]core.UserInputAnswerMode, len(normalized))
	opts := make(map[string][]claudePendingOption, len(normalized))
	order := make([]string, 0, len(normalized))
	for i, nq := range normalized {
		qText := parsed[i].Question
		mode[qText] = nq.AnswerMode
		order = append(order, qText)
		plist := make([]claudePendingOption, 0, len(nq.Options))
		for _, o := range nq.Options {
			plist = append(plist, claudePendingOption{id: o.ID, label: o.Label})
		}
		opts[qText] = plist
	}
	return claudeUIEntry{
		interactionID: interactionID,
		requestID:     requestID,
		toolUseID:     toolUseID,
		owningTurnID:  owningTurnID,
		rawInput:      rawInput,
		questionMode:  mode,
		questionOpts:  opts,
		questionOrder: order,
	}
}

// ── session 层：request 处理 + ResolveUserInput ───────────────────────────────

// handleAskUserQuestionV2 处理 v2 路径的 AskUserQuestion（§9.1/§9.2；identity 依设计 v6 §4.1）。
// 在 permission-mode bypass 之前由 handleControlRequest 调用。
//
// 双 identity fail closed（§2.1）：requestID 只用于 control_response 配对；toolUseID 是
// timeline canonical seed。缺 toolUseID 无法建立跨域 identity——不发 canonical requested，
// requestID 可用时回 deny（invalid_backend_request）避免 CLI 永久等待，绝不回退用另一
// identity 猜测。缺 requestID（空）时同样不发卡：无法回 control response 的 Ask 无法被
// 本 bridge 作答，只留 transcript 侧 observe-only 投影。
func (cs *claudeSession) handleAskUserQuestionV2(requestID, toolUseID string, input map[string]any) {
	if toolUseID == "" {
		slog.Error("claudeSession: AskUserQuestion missing tool_use_id; failing closed",
			"request_id", requestID)
		if requestID != "" {
			_ = cs.RespondPermission(requestID, core.PermissionResult{
				Behavior: "deny",
				Message:  "CordCode could not establish the tool_use identity for this question.",
			})
		}
		return
	}
	if requestID == "" {
		slog.Error("claudeSession: AskUserQuestion missing control request_id; failing closed",
			"tool_use_id", toolUseID)
		return
	}
	iid := deriveClaudeInteractionID(toolUseID)
	turnID := cs.currentStructuredInputTurnID()
	if turnID == "" {
		slog.Error("claudeSession: AskUserQuestion has no attributable turn", "request_id", requestID)
		_ = cs.RespondPermission(requestID, core.PermissionResult{
			Behavior: "deny",
			Message:  "CordCode could not attribute this question to the active turn.",
		})
		return
	}
	parsed := parseUserQuestions(input)
	normalized, err := normalizeClaudeUserQuestions(iid, parsed)
	if err != nil || len(normalized) == 0 {
		slog.Warn("claudeSession: AskUserQuestion v2 malformed", "request_id", requestID, "error", err)
		cs.emitUserInputEvent(core.Event{
			Type:      core.EventUserInputRequested,
			SessionID: cs.CurrentSessionID(),
			TurnID:    turnID,
			ItemID:    toolUseID,
			UserInput: &core.UserInputInteraction{
				InteractionID:  iid,
				Status:         core.UserInputStatusFailed,
				Questions:      normalized,
				CanRespond:     false,
				CanReject:      false,
				DiagnosticCode: "invalid_backend_request",
			},
		})
		return
	}

	entry := buildClaudePendingEntry(iid, requestID, toolUseID, turnID, input, parsed, normalized)
	if !cs.claudeUserInputReg.Register(entry) {
		// 重放：只在仍 pending 时重发 pending（幂等 upsert）；已 resolved 不降级。
		if cs.claudeUserInputReg.Status(iid) != claudeUIPending {
			return
		}
	}

	// 可答性（§2.2）：证据门（claudeAskAnsweringEnabled，默认 false）未翻转前维持
	// owner 2026-08-31 裁决的 observe_only 只读呈现；门翻转后桥持有活会话 + registry
	// pending 即可答可跳过。canRespond 不从 transcript 来源或 registry miss 推断。
	canRespond := claudeAskAnsweringEnabled && cs.alive.Load()
	diagnosticCode := ""
	if !canRespond {
		diagnosticCode = "observe_only"
	}

	cs.emitUserInputEvent(core.Event{
		Type:      core.EventUserInputRequested,
		SessionID: cs.CurrentSessionID(),
		TurnID:    turnID,
		ItemID:    toolUseID,
		UserInput: &core.UserInputInteraction{
			InteractionID: iid,
			Status:        core.UserInputStatusPending,
			Questions:     normalized,
			CanRespond:    canRespond,
			CanReject:     canRespond,
			// 证据门未过：Claude 问答卡对 CordCode 客户端只读（owner 2026-08-31 裁决；
			// 作答在 Mac 端 Claude Code 会话里给）。live 与 cold/hydrate 的 observe_only
			// 语义对齐；ResolveUserInput 的 §9.3 作答/拒绝路径保留，供门翻转后放开。
			DiagnosticCode: diagnosticCode,
		},
	})
	cs.emitLegacyAskUserQuestion(requestID, parsed)
}

// emitUserInputEvent 把结构化用户输入事件投递到 events channel（与 v1 emit 同语义）。
//
// Turn attribution（设计 §10.2「requested event 必须有可证明的 turn attribution」）：Claude
// 的 turn 身份取当前正在 diff 的 assistant message id（activeMsgID）。这是 AskUserQuestion 抵达
// 时唯一可证明的活跃 assistant 身份；reducer 据此 upsert 该 turn 并把 user_input part 挂到其
// assistant message 上。Claude 的 content 事件当前不经 live reducer 归因（projection 走 cold
// hydrate source-read），因此 activeMsgID 既是 live 投影的 turn key，也与 hydrate 为同一 assistant
// turn 派生的身份一致（hydrate 以 user-message identity 作 turnId，assistant 内容共享之；live 与
// hydrate 的 turn 对齐属 Claude live projection 整体接入范畴，不在本 P3 reducer/events 范围内）。
func (cs *claudeSession) emitUserInputEvent(ev core.Event) {
	select {
	case cs.events <- cs.scopeEvent(ev):
	case <-cs.ctx.Done():
	}
}

func (cs *claudeSession) currentStructuredInputTurnID() string {
	streamID := cs.streamState.currentMsgID
	if streamID != "" {
		return streamID
	}
	id, _ := cs.activeMsgID.Load().(string)
	return id
}

// emitUserInputSubmitted 发射 submitted 事件（设计 v6 §4.5.2/§4.7）：payload 只带
// interactionId/turnId/itemId（registry 保存的 tool-use 派生身份），不带答案正文。
// at-least-once：首次 control write 成功后与幂等重放命中时都调用；reducer 对
// terminal/resolved 后的迟到 submitted 丢弃，重发安全。
func (cs *claudeSession) emitUserInputSubmitted(turnID, interactionID, toolUseID string) {
	cs.emitUserInputEvent(core.Event{
		Type:      core.EventUserInputSubmitted,
		SessionID: cs.CurrentSessionID(),
		TurnID:    turnID,
		ItemID:    toolUseID,
		UserInput: &core.UserInputInteraction{
			InteractionID: interactionID,
			Status:        core.UserInputStatusSubmitted,
		},
	})
}

// resolveUserInput 实现 core.UserInputResponder（§9.3；并发收口按设计 v6 §4.5）。
//
// 并发 claimant：entry 已占用时在 registry transition waiter 上等待（锁外）：
//   - A ConfirmCommitted → 本端返回 already_resolved（submitted 事件由 A 发出，
//     投影收口由调用方 waitForUserInputResolution 完成）；
//   - A ReleaseClaim → 本端在同一 RPC 内重新 Claim，成功则用本端 action/answers
//     写一次 control response；
//   - cancel/remove/session dead → 停止写，返回 typed error；
//   - ctx 超时 → retryable error（调用方按失败回执解锁客户端）。
//
// 不把 OutcomeInProgress+pending 作为成功回执暴露（§3.2）。
// ResolveUserInput 实现 core.UserInputResponder（§9.3）。
func (cs *claudeSession) ResolveUserInput(ctx context.Context, interactionID, clientActionID string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	return cs.resolveUserInput(ctx, interactionID, clientActionID, action, answers, "ios")
}

func (cs *claudeSession) resolveUserInput(ctx context.Context, interactionID, clientActionID string, action core.UserInputAction, answers []core.UserInputAnswer, source string) (core.UserInputResolution, error) {
	if err := ctx.Err(); err != nil {
		return core.UserInputResolution{}, err
	}
	if !cs.alive.Load() {
		return core.UserInputResolution{}, &core.UserInputError{Code: "session_not_active", Message: "claude session not active"}
	}

	var snap *claudeClaimSnapshot
	for {
		if err := ctx.Err(); err != nil {
			return core.UserInputResolution{}, &core.UserInputError{Code: "claim_timeout", Message: "timed out waiting for the in-flight claim to settle"}
		}
		dec := cs.claudeUserInputReg.Claim(interactionID, clientActionID)
		if dec.outcome == core.UserInputOutcomeAccepted {
			// 同 clientActionID 幂等重放：无条件重发 submitted（§4.5.2 at-least-once），
			// 绝不重写 control response。
			cs.reemitSubmittedIfCommitted(interactionID)
			return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
		}
		if dec.outcome == core.UserInputOutcomeAlreadyResolved {
			// 已 committed 的幂等命中（新 action id）：同样重发 submitted。
			cs.reemitSubmittedIfCommitted(interactionID)
			return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
		}
		if dec.claimed {
			snap = dec.snapshot
			break // 拿到 claim，进入写路径
		}
		switch dec.status {
		case claudeUIAbsent:
			return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "interaction not found"}
		case claudeUIResolved:
			cs.reemitSubmittedIfCommitted(interactionID)
			return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
		case claudeUIClaimed:
			// 等待当前 claimant 的确定结果（§4.5.1 waiter；锁外等待）。
			if dec.waitCh == nil {
				return core.UserInputResolution{}, &core.UserInputError{Code: "response_in_progress", Message: "another client is answering this question"}
			}
			status, err := cs.claudeUserInputReg.WaitTransition(interactionID, dec.waitGen, ctx)
			if err != nil {
				return core.UserInputResolution{}, &core.UserInputError{Code: "claim_timeout", Message: "timed out waiting for the in-flight claim to settle"}
			}
			switch status {
			case claudeUIAbsent:
				// cancel/remove/session teardown：停止写。
				return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "interaction was cancelled or removed while waiting"}
			case claudeUIResolved:
				cs.reemitSubmittedIfCommitted(interactionID)
				return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
			case claudeUIPending:
				// A ReleaseClaim → 本端循环重试自己 Claim。
				continue
			case claudeUIClaimed:
				// 新 claimant 抢到（罕见：A release 后 B 先到）——继续等待。
				continue
			}
		}
	}

	if action == core.UserInputActionReject {
		if err := cs.respondPermissionContext(ctx, snap.requestID, core.PermissionResult{Behavior: "deny", Message: "User skipped the question."}); err != nil {
			cs.claudeUserInputReg.ReleaseClaim(interactionID)
			return core.UserInputResolution{}, &core.UserInputError{Code: "backend_response_failed", Message: "failed to write claude deny control_response"}
		}
		if cs.claudeUserInputReg.ConfirmResolved(interactionID, clientActionID, source) {
			// control write 成功 → submitted 至少一次（§4.5.2）。deny 的耐久终态
			//（rejected）由 transcript tool_result 证明（§4.6 证据门 PASSED，
			// 2026-09-16 删除旧 live resolved producer）。
			cs.emitUserInputSubmitted(snap.owningTurnID, interactionID, snap.toolUseID)
		}
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusRejected}, nil
	}
	if action != core.UserInputActionAnswer {
		cs.claudeUserInputReg.ReleaseClaim(interactionID)
		return core.UserInputResolution{}, &core.UserInputError{Code: "invalid_answer_shape", Message: "unknown action"}
	}

	updatedInput, err := buildClaudeUpdatedInput(snap, answers)
	if err != nil {
		cs.claudeUserInputReg.ReleaseClaim(interactionID)
		return core.UserInputResolution{}, err
	}
	if err := cs.respondPermissionContext(ctx, snap.requestID, core.PermissionResult{Behavior: "allow", UpdatedInput: updatedInput}); err != nil {
		cs.claudeUserInputReg.ReleaseClaim(interactionID)
		return core.UserInputResolution{}, &core.UserInputError{Code: "backend_response_failed", Message: "failed to write claude allow control_response"}
	}
	if cs.claudeUserInputReg.ConfirmResolved(interactionID, clientActionID, source) {
		// control write 成功 → submitted 至少一次；answered 耐久终态由 transcript
		// tool_result 产生（§4.6 证据门 PASSED，2026-09-16 删除旧 live resolved
		// producer——transcript 是 resolved 唯一耐久证据）。
		cs.emitUserInputSubmitted(snap.owningTurnID, interactionID, snap.toolUseID)
	}
	return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
}

// reemitSubmittedIfCommitted 在幂等/already-resolved 命中时重发 submitted（§4.5.2）：
// registry 已 committed 但首次 submitted 可能在 map/publisher/Kernel 前丢失——
// 重发让投影可恢复为 submitted 一卡；control response 绝不重写。
func (cs *claudeSession) reemitSubmittedIfCommitted(interactionID string) {
	snap, status := cs.claudeUserInputReg.SnapshotByInteraction(interactionID)
	if status != claudeUIResolved || snap == nil {
		return
	}
	cs.emitUserInputSubmitted(snap.owningTurnID, interactionID, snap.toolUseID)
}

// UserInputAnswerable 实现 core.UserInputAnswerabilityOracle（设计 v6 §4.2）。
// 可答 = 证据门已翻转 + 会话存活 + registry 在 tool-use 派生键上仍 pending。
// 任一不满足即 false（fail closed）；不从 transcript 来源或 registry miss 推断可答。
// transcript mapper（冷拉/live batch/hydrate legacy row/pathless rich history）经此
// 谓词决定 canRespond，与 live requested 的判定同源。
func (cs *claudeSession) UserInputAnswerable(interactionID string) bool {
	if interactionID == "" || !claudeAskAnsweringEnabled || !cs.alive.Load() {
		return false
	}
	return cs.claudeUserInputReg.Status(interactionID) == claudeUIPending
}

// buildClaudeUpdatedInput 按 §9.3 构建 updatedInput = shallowCopy(originalInput) + answers。
// single → answers[qText]=label string；multiple → label array；每题恰好一个 entry。
// option value 映射回 label；custom text 原样写入 answers。
func buildClaudeUpdatedInput(snap *claudeClaimSnapshot, answers []core.UserInputAnswer) (map[string]any, error) {
	// questionId → questionText 反查（questionId = claudeQuestionID(interactionID, index)）。
	qTextByID := make(map[string]string, len(snap.questionOrder))
	for i, qText := range snap.questionOrder {
		qTextByID[claudeQuestionID(snap.interactionID, i)] = qText
	}

	out := make(map[string]any, len(snap.questionOrder))
	seen := make(map[string]bool, len(answers))
	for _, a := range answers {
		qText, ok := qTextByID[a.QuestionID]
		if !ok {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "unknown question id"}
		}
		if seen[qText] {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "duplicate question"}
		}
		seen[qText] = true
		val, err := claudeAnswerValue(snap, qText, snap.questionMode[qText], a.Values)
		if err != nil {
			return nil, err
		}
		out[qText] = val
	}
	for _, qText := range snap.questionOrder {
		if !seen[qText] {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "missing required question"}
		}
	}

	updated := copyStringAnyMap(snap.rawInput)
	if updated == nil {
		updated = map[string]any{}
	}
	updated["answers"] = out
	return updated, nil
}

func claudeAnswerValue(snap *claudeClaimSnapshot, qText string, mode core.UserInputAnswerMode, values []core.UserInputValue) (any, error) {
	labelByOptID := make(map[string]string, len(snap.questionOpts[qText]))
	for _, o := range snap.questionOpts[qText] {
		labelByOptID[o.id] = o.label
	}
	switch mode {
	case core.UserInputAnswerModeSingle:
		if len(values) != 1 {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "single requires exactly one value"}
		}
		return claudeAnswerString(labelByOptID, values[0])
	case core.UserInputAnswerModeMultiple:
		if len(values) < 1 {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "multiple requires at least one value"}
		}
		labels := make([]string, 0, len(values))
		for _, v := range values {
			label, err := claudeAnswerString(labelByOptID, v)
			if err != nil {
				return nil, err
			}
			labels = append(labels, label)
		}
		return labels, nil
	default:
		return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "unsupported answer mode for claude"}
	}
}

// claudeAnswerString 把 option value 映射回 label，并保留 Claude 客户端允许的非空 custom text。
func claudeAnswerString(labelByOptID map[string]string, v core.UserInputValue) (string, error) {
	switch v.Kind {
	case core.UserInputValueOption:
		label, ok := labelByOptID[v.OptionID]
		if !ok {
			return "", &core.UserInputError{Code: "invalid_answer_shape", Message: "unknown option"}
		}
		return label, nil
	case core.UserInputValueText:
		if text := strings.TrimSpace(v.Text); text != "" {
			return v.Text, nil
		}
		return "", &core.UserInputError{Code: "invalid_answer_shape", Message: "custom text must not be empty"}
	default:
		return "", &core.UserInputError{Code: "invalid_answer_shape", Message: "unsupported answer value"}
	}
}
