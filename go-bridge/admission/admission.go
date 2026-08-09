package admission

import (
	"crypto/rand"
	"sync"
	"time"
)

// AdmissionState 是 RuntimeAdmission 三态机（plan §3.6.3）。
// accepting ⇄ quiescing → shuttingDown（不可逆）。
type AdmissionState int

const (
	StateAccepting AdmissionState = iota
	StateQuiescing
	StateShuttingDown
)

func (s AdmissionState) String() string {
	switch s {
	case StateAccepting:
		return "accepting"
	case StateQuiescing:
		return "quiescing"
	case StateShuttingDown:
		return "shuttingDown"
	default:
		return "unknown"
	}
}

// HealthState 是 FileReadHealth 三态机，与 AdmissionState 正交。
type HealthState int

const (
	HealthHealthy HealthState = iota
	HealthDegrading
	HealthDegraded
)

func (s HealthState) String() string {
	switch s {
	case HealthHealthy:
		return "healthy"
	case HealthDegrading:
		return "degrading"
	case HealthDegraded:
		return "degraded"
	default:
		return "unknown"
	}
}

type terminalKind int

const (
	terminalCommitted terminalKind = iota
	terminalAborted
	terminalExpired
)

// RuntimeIdentity 是 Go 拥有的 runtime 身份（pid + bridgeEpoch）。
// wire identity 明确不含 Mac 私有 launch generation（R10 P0-2）。
type RuntimeIdentity struct {
	PID         int32
	BridgeEpoch uint64
}

type activeOp struct {
	id           OperationID
	quiesceEpoch uint64
	token        Token
	expiresAt    time.Time
	leaseMillis  uint32
}

type terminalOp struct {
	id           OperationID
	quiesceEpoch uint64
	token        Token
	kind         terminalKind
}

// AdmissionMachine 是 quiesce/commit/abort 的唯一业务锁拥有者。
// 所有状态转移在同一锁内原子完成；mismatch 永不改变状态。
type AdmissionMachine struct {
	mu                     sync.Mutex
	state                  AdmissionState
	identity               RuntimeIdentity
	health                 HealthState
	stateEpoch             uint64 // FileReadHealth stateEpoch；healthEpoch 是其同值回显
	quiesceEpoch           uint64
	active                 *activeOp
	lastTerminal           *terminalOp
	bridgeOwnedActiveTurns uint32
	pendingInteractions    uint32
	now                    func() time.Time
	tokenGen               func() (Token, error)
	leaseMillis            uint32
}

// NewAdmissionMachine 构造 accepting 态机器。clock/tokenGen 可注入（测试用 fake clock / 固定 RNG）。
func NewAdmissionMachine(identity RuntimeIdentity, clock func() time.Time, leaseMillis uint32) *AdmissionMachine {
	if clock == nil {
		clock = time.Now
	}
	return &AdmissionMachine{
		state:        StateAccepting,
		identity:     identity,
		health:       HealthHealthy,
		stateEpoch:   1, // UInt64, starts at 1
		quiesceEpoch: 0, // 递增至 1 在首次接受 quiesce operation 时
		now:          clock,
		tokenGen:     cryptoRandToken,
		leaseMillis:  leaseMillis,
	}
}

// cryptoRandToken 用 crypto/rand 生成 16-byte token。
func cryptoRandToken() (Token, error) {
	var t Token
	if _, err := rand.Read(t[:]); err != nil {
		return Token{}, err
	}
	return t, nil
}

// SetTokenGenerator 注入 token 生成器（测试用：固定 token 或强制失败）。
func (m *AdmissionMachine) SetTokenGenerator(gen func() (Token, error)) {
	if gen == nil {
		gen = cryptoRandToken
	}
	m.mu.Lock()
	m.tokenGen = gen
	m.mu.Unlock()
}

// SetActivity 注入 bridge-owned active turn / pending interaction 计数（影响 quiesce deferred 判定）。
func (m *AdmissionMachine) SetActivity(bridgeOwnedActiveTurns, pendingInteractions uint32) {
	m.mu.Lock()
	m.bridgeOwnedActiveTurns = bridgeOwnedActiveTurns
	m.pendingInteractions = pendingInteractions
	m.mu.Unlock()
}

// TryBeginBridgeTurn 与 Quiesce 使用同一把锁，消除“status 显示 idle 后新 turn 插入”竞态。
func (m *AdmissionMachine) TryBeginBridgeTurn() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireActiveIfDue()
	if m.state != StateAccepting || m.bridgeOwnedActiveTurns == ^uint32(0) {
		return false
	}
	m.bridgeOwnedActiveTurns++
	return true
}

// EndBridgeTurn 终结一个已 admit 的 Bridge-owned turn；重复终结幂等。
func (m *AdmissionMachine) EndBridgeTurn() {
	m.mu.Lock()
	if m.bridgeOwnedActiveTurns > 0 {
		m.bridgeOwnedActiveTurns--
	}
	m.mu.Unlock()
}

// SetPendingInteractions 同步 projection kernel 的 level-triggered pending 数。
func (m *AdmissionMachine) SetPendingInteractions(count uint32) {
	m.mu.Lock()
	m.pendingInteractions = count
	m.mu.Unlock()
}

// State 返回当前 admission state（测试观察用）。
func (m *AdmissionMachine) State() AdmissionState { m.mu.Lock(); defer m.mu.Unlock(); return m.state }

// Health 返回当前 FileReadHealth state（正交；测试观察用）。
func (m *AdmissionMachine) Health() HealthState { m.mu.Lock(); defer m.mu.Unlock(); return m.health }

// QuiesceEpoch 返回当前 quiesceEpoch。
func (m *AdmissionMachine) QuiesceEpoch() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.quiesceEpoch
}

// SnapshotTerminal 暴露 last-terminal（去 token）用于测试断言终态。
type TerminalSnapshot struct {
	Kind        terminalKind
	HasTerminal bool
}

// RuntimeSnapshot 是 Management status 使用的无 secret、level-triggered 快照。
// Token 永远不离开 AdmissionMachine；leased/committed 只暴露 operation correlation 与 epoch。
type RuntimeSnapshot struct {
	State                  AdmissionState
	Identity               RuntimeIdentity
	Health                 HealthState
	HealthEpoch            uint64
	BridgeOwnedActiveTurns uint32
	PendingInteractions    uint32
	OperationID            OperationID
	HasOperation           bool
	QuiesceEpoch           uint64
	LeaseRemainingMillis   uint32
}

// SetHealthSnapshot 把 filepool 拥有的正交 health 状态同步进 admission 的同一把业务锁。
// expectedHealthEpoch 的比较与 status snapshot 因而观察同一个线性化值。
func (m *AdmissionMachine) SetHealthSnapshot(state HealthState, epoch uint64) {
	m.mu.Lock()
	m.health = state
	m.stateEpoch = epoch
	m.mu.Unlock()
}

// Snapshot 返回当前 level-triggered 状态；读取也会线性化 lease expiry。
func (m *AdmissionMachine) Snapshot() RuntimeSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireActiveIfDue()
	s := RuntimeSnapshot{
		State: m.state, Identity: m.identity, Health: m.health, HealthEpoch: m.stateEpoch,
		BridgeOwnedActiveTurns: m.bridgeOwnedActiveTurns,
		PendingInteractions:    m.pendingInteractions,
	}
	if m.active != nil {
		s.OperationID = m.active.id
		s.HasOperation = true
		s.QuiesceEpoch = m.active.quiesceEpoch
		s.LeaseRemainingMillis = remainingLeaseMillis(m.now(), m.active.expiresAt, m.active.leaseMillis)
	} else if m.state == StateShuttingDown && m.lastTerminal != nil && m.lastTerminal.kind == terminalCommitted {
		s.OperationID = m.lastTerminal.id
		s.HasOperation = true
		s.QuiesceEpoch = m.lastTerminal.quiesceEpoch
	}
	return s
}

func (m *AdmissionMachine) LastTerminalKind() TerminalSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastTerminal == nil {
		return TerminalSnapshot{HasTerminal: false}
	}
	return TerminalSnapshot{Kind: m.lastTerminal.kind, HasTerminal: true}
}

// expireActiveIfDue 在已持锁的前提下，若 active lease 已到期，原子恢复 accepting 并写 expired
// terminal。这把“lease 到期前未 commit 必须原子恢复 accepting”在同一锁内模型化（plan §3.6.3）。
func (m *AdmissionMachine) expireActiveIfDue() {
	if m.state == StateQuiescing && m.active != nil && !m.now().Before(m.active.expiresAt) {
		// now >= expiresAt => expired
		m.lastTerminal = &terminalOp{
			id: m.active.id, quiesceEpoch: m.active.quiesceEpoch, token: m.active.token, kind: terminalExpired,
		}
		m.active = nil
		m.state = StateAccepting
	}
}

// ── Quiesce ────────────────────────────────────────────────────────────────────
// 请求字段：managementSchemaVersion(=1), operationId, expectedRuntime, expectedHealthEpoch。
// 结果 outcome 集合：safe / deferred / identity_mismatch / epoch_mismatch / already_committed /
// already_quiescing / operation_reused / token_generation_failed。
type QuiesceRequest struct {
	ManagementSchemaVersion uint64
	OperationID             OperationID
	ExpectedRuntime         RuntimeIdentity
	ExpectedHealthEpoch     uint64
}

type QuiesceOutcome string

const (
	QuiesceSafe                QuiesceOutcome = "safe"
	QuiesceDeferred            QuiesceOutcome = "deferred"
	QuiesceIdentityMismatch    QuiesceOutcome = "identity_mismatch"
	QuiesceEpochMismatch       QuiesceOutcome = "epoch_mismatch"
	QuiesceAlreadyCommitted    QuiesceOutcome = "already_committed"
	QuiesceAlreadyQuiescing    QuiesceOutcome = "already_quiescing"
	QuiesceOperationReused     QuiesceOutcome = "operation_reused"
	QuiesceTokenGenerationFail QuiesceOutcome = "token_generation_failed"
)

type QuiesceResult struct {
	Outcome              QuiesceOutcome
	RuntimeIdentity      RuntimeIdentity // safe only
	HealthEpoch          uint64          // safe only
	QuiesceEpoch         uint64          // safe only
	Token                Token           // safe only (进程内返回给 Mac；不入日志)
	LeaseMillis          uint32          // safe only
	LeaseRemainingMillis uint32          // safe only
	ActiveTurns          uint32          // deferred only
	PendingInteractions  uint32          // deferred only
	RetryAfterMillis     uint32          // deferred only
}

func (m *AdmissionMachine) Quiesce(req QuiesceRequest) QuiesceResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireActiveIfDue()

	// 1. schema version
	if req.ManagementSchemaVersion != ManagementSchemaVersion {
		return QuiesceResult{Outcome: QuiesceIdentityMismatch} // 未知版本 fail closed（兼容规则）
	}
	// 2. runtime identity
	if req.ExpectedRuntime != m.identity {
		return QuiesceResult{Outcome: QuiesceIdentityMismatch}
	}
	// 3. expected health epoch
	if req.ExpectedHealthEpoch != m.stateEpoch {
		return QuiesceResult{Outcome: QuiesceEpochMismatch}
	}

	switch m.state {
	case StateShuttingDown:
		return QuiesceResult{Outcome: QuiesceAlreadyCommitted}
	case StateQuiescing:
		if m.active != nil && m.active.id.Equal(req.OperationID) {
			// active same operation + lease live => 幂等 safe（剩余 lease）
			leaseRemaining := remainingLeaseMillis(m.now(), m.active.expiresAt, m.active.leaseMillis)
			return QuiesceResult{
				Outcome:         QuiesceSafe,
				RuntimeIdentity: m.identity, HealthEpoch: m.stateEpoch,
				QuiesceEpoch: m.active.quiesceEpoch, Token: m.active.token,
				LeaseMillis: m.active.leaseMillis, LeaseRemainingMillis: leaseRemaining,
			}
		}
		// active different operation => already_quiescing（不泄露 token）
		return QuiesceResult{Outcome: QuiesceAlreadyQuiescing}
	case StateAccepting:
		// deferred：bridge-owned active turn 或 pending interaction 非零
		if m.bridgeOwnedActiveTurns > 0 || m.pendingInteractions > 0 {
			return QuiesceResult{
				Outcome:     QuiesceDeferred,
				ActiveTurns: m.bridgeOwnedActiveTurns, PendingInteractions: m.pendingInteractions,
				RetryAfterMillis: 0, // A0 冻结具体值；模型先置 0 占位由调用方换算
			}
		}
		// operation_reused：命中 last-terminal（含刚过期的 active）
		if m.lastTerminal != nil && m.lastTerminal.id.Equal(req.OperationID) {
			return QuiesceResult{Outcome: QuiesceOperationReused}
		}
		// 创建新 operation
		tok, err := m.tokenGen()
		if err != nil {
			return QuiesceResult{Outcome: QuiesceTokenGenerationFail} // 保持 accepting
		}
		m.quiesceEpoch++
		expiresAt := m.now().Add(time.Duration(m.leaseMillis) * time.Millisecond)
		m.active = &activeOp{id: req.OperationID, quiesceEpoch: m.quiesceEpoch, token: tok, expiresAt: expiresAt, leaseMillis: m.leaseMillis}
		m.state = StateQuiescing
		return QuiesceResult{
			Outcome:         QuiesceSafe,
			RuntimeIdentity: m.identity, HealthEpoch: m.stateEpoch,
			QuiesceEpoch: m.quiesceEpoch, Token: tok,
			LeaseMillis: m.leaseMillis, LeaseRemainingMillis: m.leaseMillis,
		}
	}
	return QuiesceResult{Outcome: QuiesceIdentityMismatch}
}

// remainingLeaseMillis 计算 now 到 expiresAt 的剩余毫秒，clamp 到 [0, leaseMillis]。
func remainingLeaseMillis(now, expiresAt time.Time, leaseMillis uint32) uint32 {
	d := expiresAt.Sub(now)
	if d <= 0 {
		return 0
	}
	ms := uint32(d / time.Millisecond)
	if ms > leaseMillis {
		return leaseMillis
	}
	return ms
}

// ── Commit / Abort ─────────────────────────────────────────────────────────────
// 请求字段：managementSchemaVersion(=1), operationId, expectedRuntime, expectedHealthEpoch,
// quiesceEpoch, token。判定优先级（持锁内）：identity → healthEpoch → operationId relation →
// quiesceEpoch → token constant-time → lease deadline。mismatch 永不改变状态。
type CommitRequest struct {
	ManagementSchemaVersion uint64
	OperationID             OperationID
	ExpectedRuntime         RuntimeIdentity
	ExpectedHealthEpoch     uint64
	QuiesceEpoch            uint64
	Token                   Token
}

type CommitOutcome string

const (
	OutcomeCommitted        CommitOutcome = "committed"
	OutcomeAlreadyCommitted CommitOutcome = "already_committed"
	OutcomeAborted          CommitOutcome = "aborted"
	OutcomeAlreadyAccepting CommitOutcome = "already_accepting"
	OutcomeIdentityMismatch CommitOutcome = "identity_mismatch"
	OutcomeEpochMismatch    CommitOutcome = "epoch_mismatch"
	OutcomeQuiesceMismatch  CommitOutcome = "quiesce_mismatch"
	OutcomeTokenMismatch    CommitOutcome = "token_mismatch"
	OutcomeLeaseExpired     CommitOutcome = "lease_expired"
)

type CommitResult struct {
	Outcome         CommitOutcome
	RuntimeIdentity RuntimeIdentity // committed/already_committed/aborted
	HealthEpoch     uint64          // committed/already_committed/aborted
	QuiesceEpoch    uint64          // committed/already_committed
}

// commitAbort 实现 commit 与 abort 共享的判定总表（plan §3.6.3）。isCommit 区分二者。
func (m *AdmissionMachine) commitAbort(isCommit bool, req CommitRequest) CommitResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireActiveIfDue()

	if req.ManagementSchemaVersion != ManagementSchemaVersion {
		return CommitResult{Outcome: OutcomeIdentityMismatch}
	}
	// 1. runtime identity
	if req.ExpectedRuntime != m.identity {
		return CommitResult{Outcome: OutcomeIdentityMismatch}
	}
	// 2. expected health epoch
	if req.ExpectedHealthEpoch != m.stateEpoch {
		return CommitResult{Outcome: OutcomeEpochMismatch}
	}

	switch m.state {
	case StateQuiescing:
		if m.active != nil && m.active.id.Equal(req.OperationID) {
			// active same operation
			if req.QuiesceEpoch != m.active.quiesceEpoch {
				return CommitResult{Outcome: OutcomeQuiesceMismatch}
			}
			if !ConstantTimeCompareToken(req.Token, m.active.token) {
				return CommitResult{Outcome: OutcomeTokenMismatch}
			}
			// lease 已过期？expireActiveIfDue 已处理：若过期则状态已是 accepting + expired terminal。
			// 因此到这里 active 一定 live。
			if isCommit {
				m.lastTerminal = &terminalOp{id: m.active.id, quiesceEpoch: m.active.quiesceEpoch, token: m.active.token, kind: terminalCommitted}
				m.active = nil
				m.state = StateShuttingDown
				return CommitResult{Outcome: OutcomeCommitted, RuntimeIdentity: m.identity, HealthEpoch: m.stateEpoch, QuiesceEpoch: m.lastTerminal.quiesceEpoch}
			}
			m.lastTerminal = &terminalOp{id: m.active.id, quiesceEpoch: m.active.quiesceEpoch, token: m.active.token, kind: terminalAborted}
			m.active = nil
			m.state = StateAccepting
			return CommitResult{Outcome: OutcomeAborted, RuntimeIdentity: m.identity, HealthEpoch: m.stateEpoch}
		}
		// active other / unknown
		return CommitResult{Outcome: OutcomeQuiesceMismatch}
	case StateAccepting:
		if m.lastTerminal != nil && m.lastTerminal.id.Equal(req.OperationID) {
			// last same operation
			if req.QuiesceEpoch != m.lastTerminal.quiesceEpoch {
				return CommitResult{Outcome: OutcomeQuiesceMismatch}
			}
			if !ConstantTimeCompareToken(req.Token, m.lastTerminal.token) {
				return CommitResult{Outcome: OutcomeTokenMismatch}
			}
			switch m.lastTerminal.kind {
			case terminalAborted:
				if isCommit {
					return CommitResult{Outcome: OutcomeQuiesceMismatch}
				}
				return CommitResult{Outcome: OutcomeAlreadyAccepting}
			case terminalExpired:
				return CommitResult{Outcome: OutcomeLeaseExpired}
			case terminalCommitted:
				// accepting 不应出现 committed terminal（committed → shuttingDown）；防御性 fail。
				return CommitResult{Outcome: OutcomeQuiesceMismatch}
			}
		}
		// no last / unknown / replaced
		return CommitResult{Outcome: OutcomeQuiesceMismatch}
	case StateShuttingDown:
		if m.lastTerminal != nil && m.lastTerminal.id.Equal(req.OperationID) {
			if req.QuiesceEpoch != m.lastTerminal.quiesceEpoch {
				return CommitResult{Outcome: OutcomeQuiesceMismatch}
			}
			if !ConstantTimeCompareToken(req.Token, m.lastTerminal.token) {
				return CommitResult{Outcome: OutcomeTokenMismatch}
			}
			return CommitResult{Outcome: OutcomeAlreadyCommitted, RuntimeIdentity: m.identity, HealthEpoch: m.stateEpoch, QuiesceEpoch: m.lastTerminal.quiesceEpoch}
		}
		return CommitResult{Outcome: OutcomeQuiesceMismatch}
	}
	return CommitResult{Outcome: OutcomeQuiesceMismatch}
}

// Commit 执行 commit-quiesced-shutdown 判定。
func (m *AdmissionMachine) Commit(req CommitRequest) CommitResult { return m.commitAbort(true, req) }

// Abort 执行 abort-quiesce 判定（与 commit 共用总表）。
func (m *AdmissionMachine) Abort(req CommitRequest) CommitResult { return m.commitAbort(false, req) }
