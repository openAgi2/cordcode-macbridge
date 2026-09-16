package gobridge

// claude_relay_coverage_test.go 锁定 relay coverage command/lease 的九项 barrier
//（设计 v6 §4.3.4）：
//  1. admission live → death → final coverage dead → hydrate 合成 abort；
//  2. first probe dead → tail 时 B live → B 在下一 poll 前死：B 已同步绑定，最终 terminal；
//  3. 绑 A → A 死 → replacement check 见 B → 同 tick rebind；B 随即死仍 terminal；
//  4. handshake timeout / 早退 / context cancel：retryable failure，不 commit；
//  5. 已运行 relay 的 cold hydrate 复用 handshake，不等 3 秒；
//  6. wait 中 generation 切换拒收旧结果；
//  7. handshake 完成后、最终 commit 前 generation 切换：lease invalid，拒绝 commit；
//  8. source identity/cut 不匹配：拒绝复用；
//  9. coverage mutex + Kernel commit barrier：锁顺序无死锁。
//
// registry 层用纯内存 barrier 测试（不依赖真实 transcript/进程）；loop 集成行为
//（同步绑定/rebind）由 handleClaudeCoverageCommand / claudeRelayReplacementRebind
// 的定向测试覆盖。

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func newCoverageRegistryForTest(t *testing.T) *claudeRelayCoverageRegistry {
	t.Helper()
	return newClaudeRelayCoverageRegistry()
}

// consumeCommands 模拟 relay loop 的命令处理 goroutine。
type coverageCommandConsumer struct {
	registry  *claudeRelayCoverageRegistry
	sessionID string
	gen       uint64
	stop      chan struct{}
	done      chan struct{}
	handler   func(req *claudeRelayCoverageRequest)
}

func startCoverageConsumer(t *testing.T, r *claudeRelayCoverageRegistry, sessionID string, gen uint64, handler func(*claudeRelayCoverageRequest)) *coverageCommandConsumer {
	t.Helper()
	c := &coverageCommandConsumer{
		registry:  r,
		sessionID: sessionID,
		gen:       gen,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		handler:   handler,
	}
	go func() {
		defer close(c.done)
		commands := r.commands(sessionID, gen)
		if commands == nil {
			return
		}
		for {
			select {
			case req := <-commands:
				if req == nil {
					return
				}
				c.handler(req)
			case <-c.stop:
				return
			}
		}
	}()
	return c
}

func (c *coverageCommandConsumer) close() {
	close(c.stop)
	<-c.done
}

// Barrier 1：admission live → 进程死 → coverage 命令回 dead → 冷事务合成 abort 的
// 决定路径（registry 层：dead 回执 → 无 lease → 正常 commit 路径 + 调用方合成）。
func TestClaudeCoverageBarrier1_AdmissionLiveDeathFinalDead(t *testing.T) {
	r := newCoverageRegistryForTest(t)
	const sessionID = "s-b1"
	gen := r.register(sessionID, sessionID, 100)
	consumer := startCoverageConsumer(t, r, sessionID, gen, func(req *claudeRelayCoverageRequest) {
		select {
		case req.reply <- claudeRelayCoverageResult{outcome: claudeCoverageDead}:
		default:
		}
	})
	defer consumer.close()

	res, err := r.requestCoverage(context.Background(), sessionID, sessionID, 100)
	if err != nil {
		t.Fatalf("requestCoverage: %v", err)
	}
	if res.outcome != claudeCoverageDead {
		t.Fatalf("outcome=%v, want dead", res.outcome)
	}
	// dead → 不签发 lease → commit 走普通路径（validateAndCommit 必须拒绝）。
	if err := r.validateAndCommit(sessionID, sessionID, 100, func() error { return nil }); !errors.Is(err, errClaudeCoverageLeaseInvalid) {
		t.Fatalf("dead 后无 lease，validateAndCommit 应拒绝，实际 %v", err)
	}
}

// Barrier 2：首探 dead → 冷事务 tail 时见 B live → liveBound（B 已同步绑定）→
// B 在下一 poll 前死：lease 已签发，commit pin 放行（B 的死亡由已绑定 watcher 负责）。
func TestClaudeCoverageBarrier2_FirstProbeDeadTailBLiveBindBeforePoll(t *testing.T) {
	r := newCoverageRegistryForTest(t)
	const sessionID = "s-b2"
	gen := r.register(sessionID, sessionID, 200)
	var bound atomic.Bool
	consumer := startCoverageConsumer(t, r, sessionID, gen, func(req *claudeRelayCoverageRequest) {
		// 模拟 handleClaudeCoverageCommand 的 liveBound 前置动作：先 issueLease
		//（= 同步绑定 B），再回复。B 随即死亡不影响已签发的 lease。
		r.issueLease(sessionID, claudeRelayCoverageLease{
			generation: gen, sourceIdentity: sessionID, sourceCut: req.sourceCut, pid: 4242,
		})
		bound.Store(true)
		select {
		case req.reply <- claudeRelayCoverageResult{outcome: claudeCoverageLiveBound, pid: 4242}:
		default:
		}
	})
	defer consumer.close()

	res, err := r.requestCoverage(context.Background(), sessionID, sessionID, 200)
	if err != nil {
		t.Fatalf("requestCoverage: %v", err)
	}
	if res.outcome != claudeCoverageLiveBound {
		t.Fatalf("outcome=%v, want liveBound", res.outcome)
	}
	if !bound.Load() {
		t.Fatal("liveBound 回执前必须完成同步绑定（issueLease 先于 reply）")
	}
	// B 在下一 poll 前死亡：lease 仍有效，commit pin 放行（watcher 已绑定 B）。
	committed := false
	if err := r.validateAndCommit(sessionID, sessionID, 200, func() error { committed = true; return nil }); err != nil {
		t.Fatalf("lease 有效应放行 commit： %v", err)
	}
	if !committed {
		t.Fatal("commit 闭包未执行")
	}
}

// fakeLiveListerAgent 满足 core.LiveSessionLister 的测试 agent。
type fakeLiveListerAgent struct {
	fakeAgent
	proc core.LiveSessionProcess
	err  error
}

func (a *fakeLiveListerAgent) LiveSessionProcess(ctx context.Context, sessionID string) (core.LiveSessionProcess, error) {
	return a.proc, a.err
}

func (a *fakeLiveListerAgent) IsProcessAlive(ctx context.Context, pid int) bool {
	return a.proc.PID == pid && a.proc.Live
}

// Barrier 3：绑 A → A 死 → replacement 复查见 B → 同 tick rebind（不回 late-bind）。
// 直接测 claudeRelayReplacementRebind 的判定与状态转移。
func TestClaudeCoverageBarrier3_ReplacementSameTickRebind(t *testing.T) {
	h := newTestHandlers(t)
	const sessionID = "s-b3"
	agent := &fakeLiveListerAgent{proc: core.LiveSessionProcess{SessionID: sessionID, PID: 222, Live: true}}
	h.RegisterAgent("claude", agent)

	cachedPID := 111
	var liveLister core.LiveSessionLister = agent
	misses := 1
	if h.claudeRelayReplacementRebind(sessionID, "claude", &cachedPID, &liveLister, &misses) {
		if cachedPID != 222 {
			t.Fatalf("rebind 后 cachedPID=%d，应为 222", cachedPID)
		}
		if misses != 0 {
			t.Fatalf("rebind 后 misses=%d，应为 0", misses)
		}
	} else {
		t.Fatal("替代复查见 B 活应同 tick rebind，不得回 late-bind")
	}

	// 无替代者（B 也死）：不 rebind，调用方走合成 abort + late-bind。
	agent.proc.Live = false
	cachedPID = 222
	if h.claudeRelayReplacementRebind(sessionID, "claude", &cachedPID, &liveLister, &misses) {
		t.Fatal("无活替代者时不得 rebind")
	}
}

// Barrier 4：handshake timeout / 无注册 relay / context cancel / 命令队列满：
// retryable failure，不 commit（validateAndCommit 拒绝）。
func TestClaudeCoverageBarrier4_TimeoutAndEarlyExitsRetryable(t *testing.T) {
	r := newCoverageRegistryForTest(t)

	// 4a. 无注册 relay。
	if _, err := r.requestCoverage(context.Background(), "s-none", "s-none", 0); !errors.Is(err, errClaudeCoverageNoRelay) {
		t.Fatalf("无 relay 应 errClaudeCoverageNoRelay，实际 %v", err)
	}

	// 4b. 注册了 relay 但命令无人消费 → 队列超时（claudeRelayCoverageTimeout=3s 太长，
	// 用 ctx cancel 验证同一路径的 fail closed）。
	const sessionID = "s-b4"
	r.register(sessionID, sessionID, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.requestCoverage(ctx, sessionID, sessionID, 0); err == nil {
		t.Fatal("ctx 超时应返回错误（fail closed）")
	}

	// 4c. source identity 不匹配。
	if _, err := r.requestCoverage(context.Background(), sessionID, "other-source", 0); !errors.Is(err, errClaudeCoverageSource) {
		t.Fatalf("source 不匹配应 errClaudeCoverageSource，实际 %v", err)
	}

	// 4d. 消费者回 failed → 错误透传。
	consumer := startCoverageConsumer(t, r, sessionID, r.currentGeneration(sessionID), func(req *claudeRelayCoverageRequest) {
		select {
		case req.reply <- claudeRelayCoverageResult{outcome: claudeCoverageFailed, err: errors.New("lookup error")}:
		default:
		}
	})
	defer consumer.close()
	if _, err := r.requestCoverage(context.Background(), sessionID, sessionID, 0); err == nil {
		t.Fatal("lookup error 应返回错误")
	}
}

// Barrier 5：已运行 relay 的 cold hydrate 复用 handshake（命令即时回执），不等 3 秒。
func TestClaudeCoverageBarrier5_RunningRelayReusesHandshakeFast(t *testing.T) {
	r := newCoverageRegistryForTest(t)
	const sessionID = "s-b5"
	gen := r.register(sessionID, sessionID, 500)
	consumer := startCoverageConsumer(t, r, sessionID, gen, func(req *claudeRelayCoverageRequest) {
		r.issueLease(sessionID, claudeRelayCoverageLease{
			generation: gen, sourceIdentity: sessionID, sourceCut: req.sourceCut, pid: 555,
		})
		select {
		case req.reply <- claudeRelayCoverageResult{outcome: claudeCoverageLiveBound, pid: 555}:
		default:
		}
	})
	defer consumer.close()

	start := time.Now()
	res, err := r.requestCoverage(context.Background(), sessionID, sessionID, 500)
	if err != nil {
		t.Fatalf("requestCoverage: %v", err)
	}
	if res.outcome != claudeCoverageLiveBound {
		t.Fatalf("outcome=%v, want liveBound", res.outcome)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("已运行 relay 的 coverage 应即时回执（<1s），实际 %v", elapsed)
	}
}

// Barrier 6：wait 中 generation 切换 → 旧 generation 的命令队列关闭，旧请求失败；
// 新 generation 正常服务。
func TestClaudeCoverageBarrier6_GenerationSwitchDuringWait(t *testing.T) {
	r := newCoverageRegistryForTest(t)
	const sessionID = "s-b6"
	gen1 := r.register(sessionID, sessionID, 0)
	// 旧队列不再消费（模拟旧 loop 即将退出）。
	_ = gen1
	// 新 loop 注册 → 旧命令队列被关闭。
	gen2 := r.register(sessionID, sessionID, 0)
	consumer := startCoverageConsumer(t, r, sessionID, gen2, func(req *claudeRelayCoverageRequest) {
		select {
		case req.reply <- claudeRelayCoverageResult{outcome: claudeCoverageDead}:
		default:
		}
	})
	defer consumer.close()

	// 旧 generation 的 commands 已失效。
	if commands := r.commands(sessionID, gen1); commands != nil {
		t.Fatal("旧 generation 的命令队列应已失效（nil）")
	}
	// 新请求由新 generation 服务。
	res, err := r.requestCoverage(context.Background(), sessionID, sessionID, 0)
	if err != nil || res.outcome != claudeCoverageDead {
		t.Fatalf("新 generation 请求失败：res=%+v err=%v", res, err)
	}
}

// Barrier 7：handshake 完成（lease 签发）后、最终 commit 前 generation 切换：
// register 使旧 lease 失效 → validateAndCommit 拒绝 commit。
func TestClaudeCoverageBarrier7_GenerationSwitchBeforeCommitInvalidatesLease(t *testing.T) {
	r := newCoverageRegistryForTest(t)
	const sessionID = "s-b7"
	gen1 := r.register(sessionID, sessionID, 700)
	consumer := startCoverageConsumer(t, r, sessionID, gen1, func(req *claudeRelayCoverageRequest) {
		r.issueLease(sessionID, claudeRelayCoverageLease{
			generation: gen1, sourceIdentity: sessionID, sourceCut: req.sourceCut, pid: 777,
		})
		select {
		case req.reply <- claudeRelayCoverageResult{outcome: claudeCoverageLiveBound, pid: 777}:
		default:
		}
	})
	res, err := r.requestCoverage(context.Background(), sessionID, sessionID, 700)
	if err != nil || res.outcome != claudeCoverageLiveBound {
		t.Fatalf("requestCoverage: res=%+v err=%v", res, err)
	}
	// commit 前 generation 切换（新 relay loop 注册）。
	r.register(sessionID, sessionID, 700)
	committed := false
	if err := r.validateAndCommit(sessionID, sessionID, 700, func() error { committed = true; return nil }); !errors.Is(err, errClaudeCoverageLeaseInvalid) {
		t.Fatalf("commit 前 generation 切换应拒绝 commit，实际 err=%v committed=%v", err, committed)
	}
	if committed {
		t.Fatal("lease 失效后 commit 闭包不得执行")
	}
	// 旧 loop 退出（注销）也使 lease 失效。
	consumer.close()
	r2 := newCoverageRegistryForTest(t)
	gen := r2.register(sessionID, sessionID, 700)
	c2 := startCoverageConsumer(t, r2, sessionID, gen, func(req *claudeRelayCoverageRequest) {
		r2.issueLease(sessionID, claudeRelayCoverageLease{
			generation: gen, sourceIdentity: sessionID, sourceCut: req.sourceCut, pid: 778,
		})
		select {
		case req.reply <- claudeRelayCoverageResult{outcome: claudeCoverageLiveBound, pid: 778}:
		default:
		}
	})
	defer c2.close()
	if _, err := r2.requestCoverage(context.Background(), sessionID, sessionID, 700); err != nil {
		t.Fatalf("requestCoverage: %v", err)
	}
	r2.unregister(sessionID, gen)
	if err := r2.validateAndCommit(sessionID, sessionID, 700, func() error { return nil }); !errors.Is(err, errClaudeCoverageLeaseInvalid) {
		t.Fatalf("relay 退出后应拒绝 commit，实际 %v", err)
	}
}

// Barrier 8：source identity/cut 不匹配 → 拒绝复用（requestCoverage 拒绝 +
// validateAndCommit 拒绝）。
func TestClaudeCoverageBarrier8_SourceCutMismatchRejected(t *testing.T) {
	r := newCoverageRegistryForTest(t)
	const sessionID = "s-b8"
	gen := r.register(sessionID, sessionID, 800)
	consumer := startCoverageConsumer(t, r, sessionID, gen, func(req *claudeRelayCoverageRequest) {
		r.issueLease(sessionID, claudeRelayCoverageLease{
			generation: gen, sourceIdentity: req.sourceIdentity, sourceCut: req.sourceCut, pid: 888,
		})
		select {
		case req.reply <- claudeRelayCoverageResult{outcome: claudeCoverageLiveBound, pid: 888}:
		default:
		}
	})
	defer consumer.close()

	if _, err := r.requestCoverage(context.Background(), sessionID, "other-identity", 800); !errors.Is(err, errClaudeCoverageSource) {
		t.Fatalf("source identity 不匹配应拒绝，实际 %v", err)
	}
	if _, err := r.requestCoverage(context.Background(), sessionID, sessionID, 800); err != nil {
		t.Fatalf("匹配请求应成功：%v", err)
	}
	// cut 不匹配 → lease invalid → 拒绝 commit。
	if err := r.validateAndCommit(sessionID, sessionID, 999, func() error { return nil }); !errors.Is(err, errClaudeCoverageLeaseInvalid) {
		t.Fatalf("source cut 不匹配应拒绝 commit，实际 %v", err)
	}
}

// Barrier 9：coverage mutex + Kernel commit barrier 锁顺序无死锁。commit 闭包内
// 模拟 Kernel 调用（再次进入 coverage registry 的只读方法会死锁——验证 Kernel 侧
// 不得反取 coverage mutex；正常方向 coverage → kernel 顺序完成）。
func TestClaudeCoverageBarrier9_LockOrderNoDeadlock(t *testing.T) {
	r := newCoverageRegistryForTest(t)
	const sessionID = "s-b9"
	gen := r.register(sessionID, sessionID, 900)
	consumer := startCoverageConsumer(t, r, sessionID, gen, func(req *claudeRelayCoverageRequest) {
		r.issueLease(sessionID, claudeRelayCoverageLease{
			generation: gen, sourceIdentity: sessionID, sourceCut: req.sourceCut, pid: 999,
		})
		select {
		case req.reply <- claudeRelayCoverageResult{outcome: claudeCoverageLiveBound, pid: 999}:
		default:
		}
	})
	defer consumer.close()
	if _, err := r.requestCoverage(context.Background(), sessionID, sessionID, 900); err != nil {
		t.Fatalf("requestCoverage: %v", err)
	}

	// 正常方向：coverage mutex 持有期间执行 Kernel commit（模拟）+ 并发 requestCoverage
	// （另一冷事务）必须能排队等待而不死锁。
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := r.validateAndCommit(sessionID, sessionID, 900, func() error {
			// 模拟 Kernel commit 耗时；并发方在 coverage mutex 上排队。
			time.Sleep(50 * time.Millisecond)
			return nil
		}); err != nil {
			t.Errorf("validateAndCommit: %v", err)
		}
		close(done)
	}()
	// 并发 register/unregister（generation mutation 同一边界）不阻塞死锁。
	go func() {
		time.Sleep(10 * time.Millisecond)
		r.invalidateLease(sessionID)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("coverage mutex + commit barrier 死锁")
	}
	wg.Wait()
}

// TestClaudeCoverageCommandLiveBoundBindsBeforeReply：handleClaudeCoverageCommand 的
// liveBound 路径必须先完成同步绑定（cachedPID/liveLister/misses + issueLease）再回复——
// 冷事务收到回执时 watcher 已绑定 B，B 在下一 poll 前死亡由已绑定 watcher 收口。
func TestClaudeCoverageCommandLiveBoundBindsBeforeReply(t *testing.T) {
	h := newTestHandlers(t)
	const sessionID = "s-cmd-1"
	agent := &fakeLiveListerAgent{proc: core.LiveSessionProcess{SessionID: sessionID, PID: 333, Live: true}}
	h.RegisterAgent("claude", agent)

	cachedPID := 0
	var liveLister core.LiveSessionLister
	misses := 2
	req := &claudeRelayCoverageRequest{
		generation:     1,
		sourceIdentity: sessionID,
		sourceCut:      42,
		reply:          make(chan claudeRelayCoverageResult, 1),
	}
	h.claudeRelayCoverage.register(sessionID, sessionID, 42)
	h.handleClaudeCoverageCommand(sessionID, "claude", 1, req, &cachedPID, &liveLister, &misses)

	select {
	case res := <-req.reply:
		if res.outcome != claudeCoverageLiveBound || res.pid != 333 {
			t.Fatalf("res=%+v, want liveBound pid=333", res)
		}
	default:
		t.Fatal("命令未回复")
	}
	if cachedPID != 333 {
		t.Fatalf("回复前未同步绑定：cachedPID=%d", cachedPID)
	}
	if misses != 0 {
		t.Fatalf("回复前未清 miss：misses=%d", misses)
	}
	// lease 已签发（同 generation/source cut）。
	if err := h.claudeRelayCoverage.validateAndCommit(sessionID, sessionID, 42, func() error { return nil }); err != nil {
		t.Fatalf("liveBound 后 lease 应有效：%v", err)
	}
}

// TestClaudeCoverageCommandDeadAndGenerationMismatch：无活进程回 dead；
// generation 不匹配回 failed（fail closed）。
func TestClaudeCoverageCommandDeadAndGenerationMismatch(t *testing.T) {
	h := newTestHandlers(t)
	const sessionID = "s-cmd-2"
	agent := &fakeLiveListerAgent{proc: core.LiveSessionProcess{SessionID: sessionID, PID: 0, Live: false}}
	h.RegisterAgent("claude", agent)
	h.claudeRelayCoverage.register(sessionID, sessionID, 0)

	cachedPID := 0
	var liveLister core.LiveSessionLister
	misses := 0
	req := &claudeRelayCoverageRequest{generation: 1, sourceIdentity: sessionID, reply: make(chan claudeRelayCoverageResult, 1)}
	h.handleClaudeCoverageCommand(sessionID, "claude", 1, req, &cachedPID, &liveLister, &misses)
	select {
	case res := <-req.reply:
		if res.outcome != claudeCoverageDead {
			t.Fatalf("无活进程应回 dead，实际 %+v", res)
		}
	default:
		t.Fatal("命令未回复")
	}

	// generation 不匹配 → failed。
	req2 := &claudeRelayCoverageRequest{generation: 99, sourceIdentity: sessionID, reply: make(chan claudeRelayCoverageResult, 1)}
	h.handleClaudeCoverageCommand(sessionID, "claude", 1, req2, &cachedPID, &liveLister, &misses)
	select {
	case res := <-req2.reply:
		if res.outcome != claudeCoverageFailed {
			t.Fatalf("generation 不匹配应回 failed，实际 %+v", res)
		}
	default:
		t.Fatal("命令未回复")
	}
}
