package codex

// user_input_registry.go 是 Codex 结构化用户输入的 pending 状态机（设计 §7/§8/§12）。
// pending → claimed → resolved，first-writer-wins；clientActionId 提供幂等。
//
// registry 只持有「完成 backend response 所需的原始 identity」与「派生 id → backend label」映射，
// 不写入 protocol、不进普通日志、不保存答案正文（§6.1/§6 数据约束）。
//
// transport（向 app-server 写 JSON-RPC response）由 session 层在 Claim 成功后执行；
// 本 registry 只暴露 Claim / ConfirmResolved / ReleaseClaim 的原子状态转移，使写后端、写失败回滚、
// 外部先解决三类时序都可被单测覆盖，无需真实 app-server 连接。

import (
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// userInputEntryStatus 是 registry 内部状态机的离散状态。
type userInputEntryStatus int

const (
	entryPending userInputEntryStatus = iota
	entryClaimed
	entryResolved
)

// pendingEntry 是一次未决（或已决）结构化用户输入的 registry 记录。
type pendingEntry struct {
	interactionID string
	// rawRequestID 是 app-server server request 的原始 JSON-RPC id（string|number）。
	// 写 response envelope 时必须原样 marshal 回同一 id。
	rawRequestID any
	// rawQuestionID[derivedQuestionID] = backend 原 question id（params.questions[].id），
	// 写 wire answers map 时作 key。
	rawQuestionID map[string]string
	// optionLabel[derivedOptionID] = 该 option 的 label；single 答案 optionId→label 用。
	optionLabel map[string]string
	// questionMode[derivedQuestionID] = single|text（Codex 不产生 multiple）。
	questionMode map[string]core.UserInputAnswerMode
	// questionOrder 是 derivedQuestionID 的原序，用于稳定序列化与校验。
	questionOrder []string

	status     userInputEntryStatus
	resolvedAt time.Time
	resolver   string // resolution source 标签（ios|mac|other_client|backend），仅记录

	// idempotency: clientActionID → 已成功处理的 outcome。重试返回同一 outcome，不重复 claim/写。
	outcomeByAction map[string]core.UserInputResolutionOutcome
	// activeClientAction 是当前 claimed 状态下正在处理的 clientActionID（用于并发去重）。
	activeClientAction string
}

// registryStatus 是对外的离散查询结果。
type registryStatus int

const (
	registryAbsent registryStatus = iota
	registryPending
	registryClaimed
	registryResolved
)

// registrySnapshot 是 Claim 成功时返回给 session 层的只读视图，供其序列化 wire response。
type registrySnapshot struct {
	InteractionID  string
	RawRequestID   any
	RawQuestionID  map[string]string
	OptionLabel    map[string]string
	QuestionMode   map[string]core.UserInputAnswerMode
	QuestionOrder  []string
	ResolvedAtUnix int64
}

// claimDecision 描述一次 Claim 的结果。
type claimDecision struct {
	claimed  bool
	snapshot *registrySnapshot // claimed=true 时非 nil
	outcome  core.UserInputResolutionOutcome
	status   registryStatus
}

// userInputRegistry 是 Codex session 内的 pending interaction 注册表。
// 并发安全：所有方法在 mu 下完成状态读改写。
type userInputRegistry struct {
	mu      sync.Mutex
	entries map[string]*pendingEntry
}

func newUserInputRegistry() *userInputRegistry {
	return &userInputRegistry{entries: make(map[string]*pendingEntry)}
}

// Register 记录一个 pending interaction。若同 interactionID 已存在，不覆盖，返回 false。
func (r *userInputRegistry) Register(e pendingEntry) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[e.interactionID]; ok {
		return false
	}
	if e.status == entryPending {
		// zero value 就是 pending；显式赋值便于阅读。
	}
	e.status = entryPending
	r.entries[e.interactionID] = &e
	return true
}

// Status 返回某 interaction 的当前对外状态。
func (r *userInputRegistry) Status(interactionID string) registryStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[interactionID]
	if !ok {
		return registryAbsent
	}
	switch e.status {
	case entryPending:
		return registryPending
	case entryClaimed:
		return registryClaimed
	case entryResolved:
		return registryResolved
	}
	return registryAbsent
}

// Claim 尝试 pending→claimed（first-writer-wins）。
//   - interaction 不存在 → claimed=false, status=absent。
//   - 已 resolved → claimed=false, status=resolved（caller 返回 already_resolved）。
//   - 已被其他 clientAction claimed → claimed=false, status=claimed。
//   - 同 clientActionID 已成功处理过（幂等）→ claimed=false, outcome=缓存值，不重复 claim/写。
//   - pending 且无幂等命中 → claimed=true，返回 snapshot 供 session 写 backend response。
func (r *userInputRegistry) Claim(interactionID, clientActionID string) claimDecision {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[interactionID]
	if !ok {
		return claimDecision{claimed: false, status: registryAbsent}
	}
	// 幂等：同 clientActionID 已成功处理 → 返回缓存 outcome，不再 claim。
	if clientActionID != "" {
		if out, hit := e.outcomeByAction[clientActionID]; hit {
			return claimDecision{claimed: false, outcome: out, status: registryResolved}
		}
	}
	switch e.status {
	case entryResolved:
		return claimDecision{claimed: false, outcome: core.UserInputOutcomeAlreadyResolved, status: registryResolved}
	case entryClaimed:
		// 并发竞争：另一提交正在处理。
		return claimDecision{claimed: false, status: registryClaimed}
	case entryPending:
		e.status = entryClaimed
		e.activeClientAction = clientActionID
		return claimDecision{claimed: true, snapshot: snapshotOf(e), status: registryClaimed}
	}
	return claimDecision{claimed: false, status: registryAbsent}
}

// ConfirmResolved 在 backend response 写成功后 claimed→resolved（设计 §8.2/§12）。
// 仅当前为 claimed 时成功；记录 clientActionID 的幂等 outcome。
// resolver 是 resolution source 标签（ios|mac/other_client）。返回是否实际转移。
func (r *userInputRegistry) ConfirmResolved(interactionID, clientActionID, resolver string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[interactionID]
	if !ok || e.status != entryClaimed {
		return false
	}
	e.status = entryResolved
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

// ReleaseClaim 在 backend response 写失败时 claimed→pending，允许重试（设计 §12）。
// 不记录幂等 outcome（写失败没有成功结果可缓存）。返回是否实际释放。
func (r *userInputRegistry) ReleaseClaim(interactionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[interactionID]
	if !ok || e.status != entryClaimed {
		return false
	}
	e.status = entryPending
	e.activeClientAction = ""
	return true
}

// MarkExternallyResolved 处理 serverRequest/resolved（设计 §8.3）：原子标记 resolved。
// 若本端已 resolved/claimed-并-完成，幂等 no-op。返回状态是否实际变化（caller 据此决定是否
// 发新 Kernel revision / 第二张 part）。
func (r *userInputRegistry) MarkExternallyResolved(interactionID string) (changed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[interactionID]
	if !ok {
		return false
	}
	if e.status == entryResolved {
		return false
	}
	e.status = entryResolved
	e.resolvedAt = time.Now()
	if e.resolver == "" {
		e.resolver = "backend"
	}
	e.activeClientAction = ""
	return true
}

// Remove 删除一个 entry（session 结束 / 清理用）。
func (r *userInputRegistry) Remove(interactionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, interactionID)
}

func snapshotOf(e *pendingEntry) *registrySnapshot {
	rawQ := make(map[string]string, len(e.rawQuestionID))
	for k, v := range e.rawQuestionID {
		rawQ[k] = v
	}
	opt := make(map[string]string, len(e.optionLabel))
	for k, v := range e.optionLabel {
		opt[k] = v
	}
	mode := make(map[string]core.UserInputAnswerMode, len(e.questionMode))
	for k, v := range e.questionMode {
		mode[k] = v
	}
	order := make([]string, len(e.questionOrder))
	copy(order, e.questionOrder)
	return &registrySnapshot{
		InteractionID:  e.interactionID,
		RawRequestID:   e.rawRequestID,
		RawQuestionID:  rawQ,
		OptionLabel:    opt,
		QuestionMode:   mode,
		QuestionOrder:  order,
		ResolvedAtUnix: 0,
	}
}
