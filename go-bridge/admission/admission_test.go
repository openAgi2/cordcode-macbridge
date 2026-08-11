package admission

import (
	"errors"
	"testing"
	"time"
)

var errSentinel = errors.New("injected rng failure")

// fakeClock 是可变时钟，用于 fake-clock lease 边界测试。
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }
func (f *fakeClock) set(t time.Time) { f.t = t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

var (
	opA, _  = DecodeOperationID("ffeeddccbbaa99887766554433221100")
	opB, _  = DecodeOperationID("112233445566778899aabbccddeeff00")
	tokA, _ = DecodeToken("00112233445566778899aabbccddeeff")
	tokB, _ = DecodeToken("99887766554433221100ffeeddccbbaa")
	ident   = RuntimeIdentity{PID: 12345, BridgeEpoch: 1}
)

const testLeaseMillis uint32 = 30000

func newAcceptingMachine(clock *fakeClock) *AdmissionMachine {
	t0 := time.Unix(1750000000, 0).UTC()
	clock.set(t0)
	m := NewAdmissionMachine(ident, clock.now, testLeaseMillis)
	m.SetTokenGenerator(func() (Token, error) { return tokA, nil })
	return m
}

// quiesce opA 成功，机器进入 Quiescing + active(opA, quiesceEpoch=1, tokA)，时钟停在 t0（lease live）。
func quiesceActiveA(t *testing.T, m *AdmissionMachine) {
	t.Helper()
	r := m.Quiesce(QuiesceRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: ident, ExpectedHealthEpoch: 1})
	if r.Outcome != QuiesceSafe {
		t.Fatalf("setup quiesce: expected safe, got %s", r.Outcome)
	}
	if m.State() != StateQuiescing {
		t.Fatalf("setup quiesce: state=%s want quiescing", m.State())
	}
}

// ── Quiesce outcome 覆盖 ───────────────────────────────────────────────────────

func TestQuiesce_NewOpSafe(t *testing.T) {
	clock := &fakeClock{}
	m := newAcceptingMachine(clock)
	r := m.Quiesce(QuiesceRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: ident, ExpectedHealthEpoch: 1})
	if r.Outcome != QuiesceSafe {
		t.Fatalf("outcome=%s want safe", r.Outcome)
	}
	if r.QuiesceEpoch != 1 {
		t.Errorf("quiesceEpoch=%d want 1", r.QuiesceEpoch)
	}
	if r.Token != tokA {
		t.Errorf("token mismatch")
	}
	if r.LeaseMillis != testLeaseMillis || r.LeaseRemainingMillis != testLeaseMillis {
		t.Errorf("lease fields wrong: %+v", r)
	}
	if m.State() != StateQuiescing {
		t.Errorf("state=%s want quiescing", m.State())
	}
}

func TestQuiesce_DeferredWhenActiveTurns(t *testing.T) {
	clock := &fakeClock{}
	m := newAcceptingMachine(clock)
	m.SetActivity(1, 0) // bridge-owned active turn
	r := m.Quiesce(QuiesceRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: ident, ExpectedHealthEpoch: 1})
	if r.Outcome != QuiesceDeferred {
		t.Fatalf("outcome=%s want deferred", r.Outcome)
	}
	if m.State() != StateAccepting {
		t.Errorf("deferred must stay accepting, got %s", m.State())
	}
}

func TestQuiesce_AlreadyQuiescingOtherOp(t *testing.T) {
	clock := &fakeClock{}
	m := newAcceptingMachine(clock)
	quiesceActiveA(t, m) // active opA
	// opB quiesce while opA active
	r := m.Quiesce(QuiesceRequest{ManagementSchemaVersion: 1, OperationID: opB, ExpectedRuntime: ident, ExpectedHealthEpoch: 1})
	if r.Outcome != QuiesceAlreadyQuiescing {
		t.Fatalf("outcome=%s want already_quiescing", r.Outcome)
	}
	// token 不得泄露（already_quiescing 不返回 token）
	if r.Token != (Token{}) {
		t.Errorf("already_quiescing leaked token")
	}
}

func TestQuiesce_OperationReusedAfterExpiry(t *testing.T) {
	clock := &fakeClock{}
	m := newAcceptingMachine(clock)
	quiesceActiveA(t, m)
	clock.advance(time.Duration(testLeaseMillis) * time.Millisecond) // now == expiresAt
	r := m.Quiesce(QuiesceRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: ident, ExpectedHealthEpoch: 1})
	// 过期的 active 在 entry 被回收为 accepting + expired terminal；opA 命中 last-terminal => operation_reused
	if r.Outcome != QuiesceOperationReused {
		t.Fatalf("outcome=%s want operation_reused", r.Outcome)
	}
	if m.State() != StateAccepting {
		t.Errorf("state=%s want accepting after expiry restore", m.State())
	}
}

func TestQuiesce_TokenGenerationFail(t *testing.T) {
	clock := &fakeClock{}
	m := newAcceptingMachine(clock)
	m.SetTokenGenerator(func() (Token, error) { return Token{}, errSentinel })
	r := m.Quiesce(QuiesceRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: ident, ExpectedHealthEpoch: 1})
	if r.Outcome != QuiesceTokenGenerationFail {
		t.Fatalf("outcome=%s want token_generation_failed", r.Outcome)
	}
	if m.State() != StateAccepting {
		t.Errorf("token_generation_failed must stay accepting, got %s", m.State())
	}
}

func TestQuiesce_IdentityAndEpochMismatch(t *testing.T) {
	clock := &fakeClock{}
	m := newAcceptingMachine(clock)
	r := m.Quiesce(QuiesceRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: RuntimeIdentity{PID: 999, BridgeEpoch: 1}, ExpectedHealthEpoch: 1})
	if r.Outcome != QuiesceIdentityMismatch {
		t.Errorf("identity mismatch: got %s", r.Outcome)
	}
	r = m.Quiesce(QuiesceRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: ident, ExpectedHealthEpoch: 999})
	if r.Outcome != QuiesceEpochMismatch {
		t.Errorf("epoch mismatch: got %s", r.Outcome)
	}
}

// ── commit/abort 全函数总表（table-driven）─────────────────────────────────────

type caCase struct {
	name        string
	isCommit    bool
	setup       func(t *testing.T, m *AdmissionMachine, clock *fakeClock) // 把机器放到目标前态
	req         func() CommitRequest
	wantOutcome CommitOutcome
	wantState   AdmissionState
	wantTerm    terminalKind
	expectTerm  bool // 是否期望 lastTerminal 被写入
}

func reqA(qep uint64, tok Token) CommitRequest {
	return CommitRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: ident, ExpectedHealthEpoch: 1, QuiesceEpoch: qep, Token: tok}
}

func TestCommitAbort_FullTable(t *testing.T) {
	cases := []caCase{
		{
			name: "quiescing+active same+live+token match => committed", isCommit: true,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) { quiesceActiveA(t, m) },
			req: func() CommitRequest { return reqA(1, tokA) },
			wantOutcome: OutcomeCommitted, wantState: StateShuttingDown, wantTerm: terminalCommitted, expectTerm: true,
		},
		{
			name: "quiescing+active same+live+token match => aborted (abort)", isCommit: false,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) { quiesceActiveA(t, m) },
			req: func() CommitRequest { return reqA(1, tokA) },
			wantOutcome: OutcomeAborted, wantState: StateAccepting, wantTerm: terminalAborted, expectTerm: true,
		},
		{
			name: "quiescing+active same+token mismatch => token_mismatch (no mut)", isCommit: true,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) { quiesceActiveA(t, m) },
			req: func() CommitRequest { return reqA(1, tokB) },
			wantOutcome: OutcomeTokenMismatch, wantState: StateQuiescing, expectTerm: false,
		},
		{
			name: "quiescing+active same+quiesceEpoch mismatch => quiesce_mismatch (no mut)", isCommit: true,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) { quiesceActiveA(t, m) },
			req: func() CommitRequest { return reqA(999, tokA) },
			wantOutcome: OutcomeQuiesceMismatch, wantState: StateQuiescing, expectTerm: false,
		},
		{
			name: "quiescing+active other/unknown => quiesce_mismatch (no mut)", isCommit: true,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) { quiesceActiveA(t, m) },
			req: func() CommitRequest {
				return CommitRequest{ManagementSchemaVersion: 1, OperationID: opB, ExpectedRuntime: ident, ExpectedHealthEpoch: 1, QuiesceEpoch: 1, Token: tokA}
			},
			wantOutcome: OutcomeQuiesceMismatch, wantState: StateQuiescing, expectTerm: false,
		},
		{
			name: "accepting+no last/unknown => quiesce_mismatch (commit)", isCommit: true,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) {},
			req:   func() CommitRequest { return reqA(1, tokA) },
			wantOutcome: OutcomeQuiesceMismatch, wantState: StateAccepting, expectTerm: false,
		},
		{
			name: "accepting+no last/unknown => quiesce_mismatch (abort)", isCommit: false,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) {},
			req:   func() CommitRequest { return reqA(1, tokA) },
			wantOutcome: OutcomeQuiesceMismatch, wantState: StateAccepting, expectTerm: false,
		},
		{
			name: "shuttingDown+last same committed+token match => already_committed (commit)", isCommit: true,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) {
				quiesceActiveA(t, m)
				cr := m.Commit(reqA(1, tokA)) // -> committed -> shuttingDown
				if cr.Outcome != OutcomeCommitted {
					t.Fatalf("setup commit: %s", cr.Outcome)
				}
			},
			req: func() CommitRequest { return reqA(1, tokA) },
			wantOutcome: OutcomeAlreadyCommitted, wantState: StateShuttingDown, wantTerm: terminalCommitted, expectTerm: true,
		},
		{
			name: "shuttingDown+unknown => quiesce_mismatch (abort)", isCommit: false,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) {
				quiesceActiveA(t, m)
				_ = m.Commit(reqA(1, tokA))
			},
			req: func() CommitRequest {
				return CommitRequest{ManagementSchemaVersion: 1, OperationID: opB, ExpectedRuntime: ident, ExpectedHealthEpoch: 1, QuiesceEpoch: 1, Token: tokA}
			},
			wantOutcome: OutcomeQuiesceMismatch, wantState: StateShuttingDown, expectTerm: true,
		},
		{
			name: "identity mismatch first (no mut)", isCommit: true,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) { quiesceActiveA(t, m) },
			req: func() CommitRequest {
				return CommitRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: RuntimeIdentity{PID: 999, BridgeEpoch: 1}, ExpectedHealthEpoch: 1, QuiesceEpoch: 1, Token: tokA}
			},
			wantOutcome: OutcomeIdentityMismatch, wantState: StateQuiescing, expectTerm: false,
		},
		{
			name: "health epoch mismatch (no mut)", isCommit: true,
			setup: func(t *testing.T, m *AdmissionMachine, c *fakeClock) { quiesceActiveA(t, m) },
			req: func() CommitRequest {
				return CommitRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: ident, ExpectedHealthEpoch: 999, QuiesceEpoch: 1, Token: tokA}
			},
			wantOutcome: OutcomeEpochMismatch, wantState: StateQuiescing, expectTerm: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{}
			m := newAcceptingMachine(clock)
			tc.setup(t, m, clock)
			var r CommitResult
			if tc.isCommit {
				r = m.Commit(tc.req())
			} else {
				r = m.Abort(tc.req())
			}
			if r.Outcome != tc.wantOutcome {
				t.Errorf("outcome=%s want %s", r.Outcome, tc.wantOutcome)
			}
			if m.State() != tc.wantState {
				t.Errorf("state=%s want %s", m.State(), tc.wantState)
			}
			snap := m.LastTerminalKind()
			if tc.expectTerm {
				if !snap.HasTerminal || snap.Kind != tc.wantTerm {
					t.Errorf("terminal=%+v want kind=%d", snap, tc.wantTerm)
				}
			}
		})
	}
}

// ── lease 过期 fake-clock 边界 ─────────────────────────────────────────────────

func TestCommitAbort_LeaseBoundary(t *testing.T) {
	t.Run("now==expiresAt => lease_expired + restore accepting + expired terminal", func(t *testing.T) {
		clock := &fakeClock{}
		m := newAcceptingMachine(clock)
		quiesceActiveA(t, m) // expiresAt = t0 + 30s
		clock.advance(time.Duration(testLeaseMillis) * time.Millisecond) // now == expiresAt
		r := m.Commit(reqA(1, tokA))
		if r.Outcome != OutcomeLeaseExpired {
			t.Fatalf("outcome=%s want lease_expired", r.Outcome)
		}
		if m.State() != StateAccepting {
			t.Errorf("state=%s want accepting (lease restore)", m.State())
		}
		snap := m.LastTerminalKind()
		if !snap.HasTerminal || snap.Kind != terminalExpired {
			t.Errorf("terminal=%+v want expired", snap)
		}
	})
	t.Run("now==expiresAt-1ms => still live => committed", func(t *testing.T) {
		clock := &fakeClock{}
		m := newAcceptingMachine(clock)
		quiesceActiveA(t, m)
		clock.advance(time.Duration(testLeaseMillis)*time.Millisecond - time.Millisecond) // 1ms before expiry
		r := m.Commit(reqA(1, tokA))
		if r.Outcome != OutcomeCommitted {
			t.Fatalf("outcome=%s want committed (still live)", r.Outcome)
		}
		if m.State() != StateShuttingDown {
			t.Errorf("state=%s want shuttingDown", m.State())
		}
	})
	t.Run("abort after expiry => lease_expired (accepting+last expired)", func(t *testing.T) {
		clock := &fakeClock{}
		m := newAcceptingMachine(clock)
		quiesceActiveA(t, m)
		clock.advance(time.Duration(testLeaseMillis+1000) * time.Millisecond) // past expiry
		// 先触发一次 Quiesce(opA) 让 entry 回收过期 active 为 accepting+expired terminal
		_ = m.Quiesce(QuiesceRequest{ManagementSchemaVersion: 1, OperationID: opA, ExpectedRuntime: ident, ExpectedHealthEpoch: 1})
		r := m.Abort(reqA(1, tokA))
		if r.Outcome != OutcomeLeaseExpired {
			t.Fatalf("outcome=%s want lease_expired", r.Outcome)
		}
		if m.State() != StateAccepting {
			t.Errorf("state=%s want accepting", m.State())
		}
	})
	t.Run("commit then late abort => already_committed (no resurrec)", func(t *testing.T) {
		clock := &fakeClock{}
		m := newAcceptingMachine(clock)
		quiesceActiveA(t, m)
		if r := m.Commit(reqA(1, tokA)); r.Outcome != OutcomeCommitted {
			t.Fatalf("commit: %s", r.Outcome)
		}
		// 迟到的 abort 不能复活；shuttingDown + last same committed => already_committed
		r := m.Abort(reqA(1, tokA))
		if r.Outcome != OutcomeAlreadyCommitted {
			t.Fatalf("late abort outcome=%s want already_committed", r.Outcome)
		}
		if m.State() != StateShuttingDown {
			t.Errorf("late abort must not change state; got %s", m.State())
		}
	})
}

// 接受 quiesce 后又 abort 回 accepting：再次 commit 同 op => quiesce_mismatch (aborted terminal)。
func TestCommitAbort_AcceptingAfterAbort(t *testing.T) {
	clock := &fakeClock{}
	m := newAcceptingMachine(clock)
	quiesceActiveA(t, m)
	if r := m.Abort(reqA(1, tokA)); r.Outcome != OutcomeAborted {
		t.Fatalf("abort setup: %s", r.Outcome)
	}
	// accepting + last aborted
	if r := m.Commit(reqA(1, tokA)); r.Outcome != OutcomeQuiesceMismatch {
		t.Errorf("commit after aborted: %s want quiesce_mismatch", r.Outcome)
	}
	if r := m.Abort(reqA(1, tokA)); r.Outcome != OutcomeAlreadyAccepting {
		t.Errorf("re-abort after aborted: %s want already_accepting", r.Outcome)
	}
}

// 并发：commit 与 abort 竞争同一 active op —— 先取得锁者唯一生效，后到者按更新后的 terminal 返回。
func TestCommitAbort_ConcurrentFirstLockerWins(t *testing.T) {
	clock := &fakeClock{}
	m := newAcceptingMachine(clock)
	quiesceActiveA(t, m)
	const N = 32
	outs := make(chan CommitResult, N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			if i%2 == 0 {
				outs <- m.Commit(reqA(1, tokA))
			} else {
				outs <- m.Abort(reqA(1, tokA))
			}
		}()
	}
	committed, aborted, other := 0, 0, 0
	for i := 0; i < N; i++ {
		r := <-outs
		switch r.Outcome {
		case OutcomeCommitted:
			committed++
		case OutcomeAborted:
			aborted++
		default:
			other++
		}
	}
	// 恰好一个 mutation 生效；另一侧后到者看到 terminal 返回 already_committed / already_accepting。
	if committed+aborted != 1 {
		t.Errorf("expected exactly one mutating winner, got committed=%d aborted=%d other=%d", committed, aborted, other)
	}
}
