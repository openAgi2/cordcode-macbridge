package claudecode

// user_input.go 是 Claude Code 结构化用户输入 v2 适配器（设计 §9）。
//
// 与 v1（emitAskUserQuestion → EventQuestionAsked）并行存在：仅当 session 的
// structuredUserInputV2 标志置位（P6 在 capability 协商后置位）时，AskUserQuestion 走 v2 路径
// ——在 permission-mode bypass 之前拦截（§9.1），emit EventUserInputRequested/Resolved 结构化
// 事件，并由 core.UserInputResponder.ResolveUserInput 回答/拒绝。v1 路径保留给未声明能力的会话。
//
// 关键不变量（设计已冻结）：
//   - Claude v1 固定 allowsCustomAnswer=false（即使 option label 是 "Other"，也不展开文本框）；
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
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ── 稳定 ID 派生（§6.1，Claude 分支）─────────────────────────────────────────
// 与 codex 包的派生语义一致（小写十六进制 SHA-256 前 32 字符、"ui_" 前缀），但 Claude 的 hash
// 输入是 "claudecode\0" + requestId。本地定义避免跨 adapter 包依赖；canonical 语义由本文件单测锁死。

const (
	claudeSUIHexLen = 32
	claudeSUIPrefix = "ui_"
)

func deriveClaudeInteractionID(requestID string) string {
	h := sha256.New()
	h.Write([]byte("claudecode\x00"))
	h.Write([]byte(requestID))
	sum := h.Sum(nil)
	return claudeSUIPrefix + hex.EncodeToString(sum[:claudeSUIHexLen/2])
}

// DeriveStructuredUserInputInteractionID exposes the Claude structured-input identity
// derivation to transcript consumers. Passive transcript projection must derive the same
// interactionId as the live Claude adapter from the persisted tool_use id; duplicating the
// hash here would let cold/live paths disagree silently.
func DeriveStructuredUserInputInteractionID(requestID string) string {
	return deriveClaudeInteractionID(requestID)
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

// claudeUIEntry 保存回答所需的原始 identity：Claude control_request request_id、
// 原始 input（shallowCopy 基底）、question text → mode/options 映射。
type claudeUIEntry struct {
	interactionID      string
	requestID          string
	rawInput           map[string]any
	questionMode       map[string]core.UserInputAnswerMode // questionText → single|multiple
	questionOpts       map[string][]claudePendingOption    // questionText → options
	questionOrder      []string                            // questionText 原序
	status             claudeUIEntryStatus
	resolvedAt         time.Time
	resolver           string
	outcomeByAction    map[string]core.UserInputResolutionOutcome
	activeClientAction string
}

// claudeClaimSnapshot 是 Claim 成功时返回的只读视图，供 session 层序列化 control_response。
type claudeClaimSnapshot struct {
	interactionID string
	requestID     string
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
}

type claudeUserInputRegistry struct {
	mu      sync.Mutex
	entries map[string]*claudeUIEntry
}

func newClaudeUserInputRegistry() *claudeUserInputRegistry {
	return &claudeUserInputRegistry{entries: make(map[string]*claudeUIEntry)}
}

func (r *claudeUserInputRegistry) Register(e claudeUIEntry) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[e.interactionID]; ok {
		return false
	}
	e.status = claudeEntryPending
	r.entries[e.interactionID] = &e
	return true
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
		return claudeClaimDecision{status: claudeUIClaimed}
	case claudeEntryPending:
		e.status = claudeEntryClaimed
		e.activeClientAction = clientActionID
		return claudeClaimDecision{claimed: true, snapshot: claudeSnapshotOf(e), status: claudeUIClaimed}
	}
	return claudeClaimDecision{status: claudeUIAbsent}
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
	return true
}

func (r *claudeUserInputRegistry) Remove(interactionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, interactionID)
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
		rawInput:      e.rawInput,
		questionMode:  mode,
		questionOpts:  opts,
		questionOrder: order,
	}
}

// ── 归一化（§9.2）──────────────────────────────────────────────────────────────

// normalizeClaudeUserQuestions 把 parseUserQuestions 的结果映射到 domain UserInputQuestion。
// multiSelect false→single、true→multiple；allowsCustomAnswer=false（Claude v1）；required=true；
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
			AllowsCustomAnswer: false, // Claude v1: 即使 label 是 "Other" 也不接受 custom text
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
func buildClaudePendingEntry(interactionID, requestID string, rawInput map[string]any, parsed []core.UserQuestion, normalized []core.UserInputQuestion) claudeUIEntry {
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
		rawInput:      rawInput,
		questionMode:  mode,
		questionOpts:  opts,
		questionOrder: order,
	}
}

// ── session 层：request 处理 + ResolveUserInput ───────────────────────────────

// handleAskUserQuestionV2 处理 v2 路径的 AskUserQuestion（§9.1/§9.2）。
// 在 permission-mode bypass 之前由 handleControlRequest 调用。
func (cs *claudeSession) handleAskUserQuestionV2(requestID string, input map[string]any) {
	iid := deriveClaudeInteractionID(requestID)
	parsed := parseUserQuestions(input)
	normalized, err := normalizeClaudeUserQuestions(iid, parsed)
	if err != nil || len(normalized) == 0 {
		slog.Warn("claudeSession: AskUserQuestion v2 malformed", "request_id", requestID, "error", err)
		cs.emitUserInputEvent(core.Event{
			Type:      core.EventUserInputRequested,
			SessionID: cs.CurrentSessionID(),
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

	entry := buildClaudePendingEntry(iid, requestID, input, parsed, normalized)
	if !cs.claudeUserInputReg.Register(entry) {
		// 重放：只在仍 pending 时重发 pending（幂等 upsert）；已 resolved 不降级。
		if cs.claudeUserInputReg.Status(iid) != claudeUIPending {
			return
		}
	}

	cs.emitUserInputEvent(core.Event{
		Type:      core.EventUserInputRequested,
		SessionID: cs.CurrentSessionID(),
		UserInput: &core.UserInputInteraction{
			InteractionID: iid,
			Status:        core.UserInputStatusPending,
			Questions:     normalized,
			CanRespond:    true,
			CanReject:     true, // Claude 有真实 deny control_response 路径（§9.3）
		},
	})
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
	if ev.TurnID == "" {
		if id, _ := cs.activeMsgID.Load().(string); id != "" {
			ev.TurnID = id
		}
	}
	select {
	case cs.events <- cs.scopeEvent(ev):
	case <-cs.ctx.Done():
	}
}

func (cs *claudeSession) emitUserInputResolved(iid string, status core.UserInputStatus, source string) {
	cs.emitUserInputEvent(core.Event{
		Type:      core.EventUserInputResolved,
		SessionID: cs.CurrentSessionID(),
		UserInput: &core.UserInputInteraction{
			InteractionID:    iid,
			Status:           status,
			ResolutionSource: source,
		},
	})
}

// ResolveUserInput 实现 core.UserInputResponder（§9.3）。
func (cs *claudeSession) ResolveUserInput(_ context.Context, interactionID, clientActionID string, action core.UserInputAction, answers []core.UserInputAnswer) (core.UserInputResolution, error) {
	if !cs.alive.Load() {
		return core.UserInputResolution{}, &core.UserInputError{Code: "session_not_active", Message: "claude session not active"}
	}

	dec := cs.claudeUserInputReg.Claim(interactionID, clientActionID)
	if dec.outcome == core.UserInputOutcomeAccepted {
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
	}
	if dec.outcome == core.UserInputOutcomeAlreadyResolved {
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
	}
	if !dec.claimed {
		if dec.status == claudeUIAbsent {
			return core.UserInputResolution{}, &core.UserInputError{Code: "interaction_not_found", Message: "interaction not found"}
		}
		return core.UserInputResolution{Outcome: core.UserInputOutcomeAlreadyResolved, CurrentStatus: core.UserInputStatusAnswered}, nil
	}
	snap := dec.snapshot

	if action == core.UserInputActionReject {
		if err := cs.RespondPermission(snap.requestID, core.PermissionResult{Behavior: "deny", Message: "User skipped the question."}); err != nil {
			cs.claudeUserInputReg.ReleaseClaim(interactionID)
			return core.UserInputResolution{}, &core.UserInputError{Code: "backend_response_failed", Message: "failed to write claude deny control_response"}
		}
		if cs.claudeUserInputReg.ConfirmResolved(interactionID, clientActionID, "ios") {
			cs.emitUserInputResolved(interactionID, core.UserInputStatusRejected, "ios")
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
	if err := cs.RespondPermission(snap.requestID, core.PermissionResult{Behavior: "allow", UpdatedInput: updatedInput}); err != nil {
		cs.claudeUserInputReg.ReleaseClaim(interactionID)
		return core.UserInputResolution{}, &core.UserInputError{Code: "backend_response_failed", Message: "failed to write claude allow control_response"}
	}
	if cs.claudeUserInputReg.ConfirmResolved(interactionID, clientActionID, "ios") {
		cs.emitUserInputResolved(interactionID, core.UserInputStatusAnswered, "ios")
	}
	return core.UserInputResolution{Outcome: core.UserInputOutcomeAccepted, CurrentStatus: core.UserInputStatusAnswered}, nil
}

// buildClaudeUpdatedInput 按 §9.3 构建 updatedInput = shallowCopy(originalInput) + answers。
// single → answers[qText]=label string；multiple → label array；每题恰好一个 entry。
// Claude v1 allowsCustomAnswer=false：只接受 option value，text value → invalid。
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
		return claudeOptionLabel(labelByOptID, values[0])
	case core.UserInputAnswerModeMultiple:
		if len(values) < 1 {
			return nil, &core.UserInputError{Code: "invalid_answer_shape", Message: "multiple requires at least one value"}
		}
		labels := make([]string, 0, len(values))
		for _, v := range values {
			label, err := claudeOptionLabel(labelByOptID, v)
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

// claudeOptionLabel 把单个 value 解析为 option label；Claude v1 不接受 custom text。
func claudeOptionLabel(labelByOptID map[string]string, v core.UserInputValue) (string, error) {
	if v.Kind != core.UserInputValueOption {
		return "", &core.UserInputError{Code: "invalid_answer_shape", Message: "Claude v1 does not accept custom text answers"}
	}
	label, ok := labelByOptID[v.OptionID]
	if !ok {
		return "", &core.UserInputError{Code: "invalid_answer_shape", Message: "unknown option"}
	}
	return label, nil
}
