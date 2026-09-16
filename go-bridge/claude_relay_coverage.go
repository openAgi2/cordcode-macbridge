package gobridge

// claude_relay_coverage.go 实现 relay-owned coverage command/lease（设计 v6 §4.3，R5-P0-2）。
//
// 三条漏窗的封闭方式：
//  1. 每个 relay loop 启动时注册 {generation, sourceIdentity, admissionCut}；
//     generation 单调递增，旧 loop 退出/被替换即失效。
//  2. 冷事务在 transcript fold 完成、识别尾部未答 Ask 后，向**当前 generation 的
//     relay loop**发 coverCurrentProcess 命令。命令由 relay loop 串行处理：
//     - 无活进程 → dead → 冷事务在同一 hydrate 内追加 turn_aborted；
//     - 有 B → 在回复 liveBound 前**原子**设置 cachedPID=B.PID、liveLister=B.lister、
//       清 miss 计数（同步绑定）；只有收到 liveBound 的事务才可不合成；
//     - lookup error / generation 或 source 不匹配 → fail closed。
//  3. commit pin：cold transaction 获得 lease 后，generation replacement/clear 先
//     invalidate lease；最终 commit 在 coverage registry 的同一互斥边界内再次验证
//     generation、source identity/cut、lease valid，再调用 Kernel commit 并释放 lease。
//     generation mutation 使用同一边界，因此「校验后、commit 前切换」不可发生。
//
// 锁顺序（§4.3.3）：relay coverage registry → ProjectionKernel。禁止 Kernel 回调反取
// coverage mutex。relay loop 自身的 poll 状态（cachedPID/liveLister/misses）由 loop
// goroutine 持有，coverage command 处理与 poll 同一 goroutine 串行（无数据竞争）；
// registry 只保存命令队列与 generation/lease 元数据。

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// claudeRelayCoverageTimeout 是冷事务等待 coverage 回执的上限；超时按 hydrate
// failed(retryable) 处理，拒绝 commit（§4.3.1）。
const claudeRelayCoverageTimeout = 3 * time.Second

// claudeRelayCoverageOutcome 是 coverCurrentProcess 命令的结果。
type claudeRelayCoverageOutcome int

const (
	claudeCoverageDead      claudeRelayCoverageOutcome = iota // 无活进程：冷事务须合成 turn_aborted
	claudeCoverageLiveBound                                   // 活进程已同步绑定到当前 generation relay
	claudeCoverageFailed                                      // lookup error / generation 不匹配：fail closed
)

type claudeRelayCoverageResult struct {
	outcome claudeRelayCoverageOutcome
	pid     int
	err     error
}

// claudeRelayCoverageRequest 一次 coverCurrentProcess 命令。reply 缓冲 1——命令处理
// goroutine（relay loop）非阻塞回复；冷事务带超时等待。
type claudeRelayCoverageRequest struct {
	generation     uint64
	sourceIdentity string
	sourceCut      int64
	reply          chan claudeRelayCoverageResult
}

// claudeRelayCoverageLease 是一次成功 liveBound 后签发的 commit 许可。
type claudeRelayCoverageLease struct {
	generation     uint64
	sourceIdentity string
	sourceCut      int64
	pid            int
}

// valid 报告 lease 是否仍属于当前 generation 且 source 身份一致。
func (l *claudeRelayCoverageLease) valid(generation uint64, sourceIdentity string, sourceCut int64) bool {
	if l == nil {
		return false
	}
	return l.generation == generation && l.sourceIdentity == sourceIdentity && l.sourceCut == sourceCut
}

// claudeRelayCoverageEntry 每个 session 的当前 relay loop 注册项。
type claudeRelayCoverageEntry struct {
	generation     uint64
	sourceIdentity string
	admissionCut   int64
	commands       chan *claudeRelayCoverageRequest
}

// claudeRelayCoverageRegistry 是 relay coverage 的全局注册表。所有方法持有自己的
// mutex；锁顺序恒为 coverage registry → ProjectionKernel（commit pin 在同一互斥
// 边界内验证并调用 Kernel commit）。
type claudeRelayCoverageRegistry struct {
	mu      sync.Mutex
	nextGen uint64
	entries map[string]*claudeRelayCoverageEntry
	leases  map[string]*claudeRelayCoverageLease
}

func newClaudeRelayCoverageRegistry() *claudeRelayCoverageRegistry {
	return &claudeRelayCoverageRegistry{
		nextGen: 0,
		entries: make(map[string]*claudeRelayCoverageEntry),
		leases:  make(map[string]*claudeRelayCoverageLease),
	}
}

var (
	errClaudeCoverageNoRelay      = errors.New("claude relay coverage: no registered relay loop")
	errClaudeCoverageGeneration   = errors.New("claude relay coverage: generation mismatch")
	errClaudeCoverageSource       = errors.New("claude relay coverage: source identity/cut mismatch")
	errClaudeCoverageLeaseInvalid = errors.New("claude relay coverage: lease invalid or superseded")
)

// register 启动一个 relay loop 时登记；返回新 generation。旧 entry（若有）的命令
// 队列被关闭，等待中的冷事务收到 failed（旧 generation 不得授权任何 commit）。
func (r *claudeRelayCoverageRegistry) register(sessionID, sourceIdentity string, admissionCut int64) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextGen++
	gen := r.nextGen
	if old, ok := r.entries[sessionID]; ok && old.commands != nil {
		close(old.commands)
	}
	r.entries[sessionID] = &claudeRelayCoverageEntry{
		generation:     gen,
		sourceIdentity: sourceIdentity,
		admissionCut:   admissionCut,
		commands:       make(chan *claudeRelayCoverageRequest, 8),
	}
	// 新 generation 使旧 lease 失效。
	delete(r.leases, sessionID)
	return gen
}

// unregister relay loop 退出时注销；等待中的冷事务收到 failed。
func (r *claudeRelayCoverageRegistry) unregister(sessionID string, generation uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[sessionID]
	if !ok || entry.generation != generation {
		return
	}
	close(entry.commands)
	delete(r.entries, sessionID)
	delete(r.leases, sessionID)
}

// commands 返回当前 generation 的命令队列（relay loop 串行消费）。generation 已被
// 替换时返回 nil（旧 loop 不得再处理命令）。
func (r *claudeRelayCoverageRegistry) commands(sessionID string, generation uint64) <-chan *claudeRelayCoverageRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[sessionID]
	if !ok || entry.generation != generation {
		return nil
	}
	return entry.commands
}

// currentGeneration 返回 session 当前注册的 generation（无注册为 0）。
func (r *claudeRelayCoverageRegistry) currentGeneration(sessionID string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.entries[sessionID]; ok {
		return entry.generation
	}
	return 0
}

// requestCoverage 冷事务向当前 generation 的 relay loop 请求 coverCurrentProcess。
// 无注册 relay、generation/source 不匹配或超时都返回错误（fail closed，不合成也不
// 授权 commit——调用方按 hydrate failed(retryable) 处理）。
func (r *claudeRelayCoverageRegistry) requestCoverage(
	ctx context.Context,
	sessionID, sourceIdentity string,
	sourceCut int64,
) (claudeRelayCoverageResult, error) {
	r.mu.Lock()
	entry, ok := r.entries[sessionID]
	if !ok {
		r.mu.Unlock()
		return claudeRelayCoverageResult{outcome: claudeCoverageFailed, err: errClaudeCoverageNoRelay}, errClaudeCoverageNoRelay
	}
	if entry.sourceIdentity != sourceIdentity {
		r.mu.Unlock()
		return claudeRelayCoverageResult{outcome: claudeCoverageFailed, err: errClaudeCoverageSource}, errClaudeCoverageSource
	}
	req := &claudeRelayCoverageRequest{
		generation:     entry.generation,
		sourceIdentity: sourceIdentity,
		sourceCut:      sourceCut,
		reply:          make(chan claudeRelayCoverageResult, 1),
	}
	commands := entry.commands
	r.mu.Unlock()

	select {
	case commands <- req:
	case <-ctx.Done():
		return claudeRelayCoverageResult{outcome: claudeCoverageFailed, err: ctx.Err()}, ctx.Err()
	case <-time.After(claudeRelayCoverageTimeout):
		err := errors.New("claude relay coverage: command queue timeout")
		return claudeRelayCoverageResult{outcome: claudeCoverageFailed, err: err}, err
	}

	select {
	case res := <-req.reply:
		if res.outcome == claudeCoverageFailed {
			return res, res.err
		}
		return res, nil
	case <-ctx.Done():
		return claudeRelayCoverageResult{outcome: claudeCoverageFailed, err: ctx.Err()}, ctx.Err()
	case <-time.After(claudeRelayCoverageTimeout):
		err := errors.New("claude relay coverage: reply timeout")
		return claudeRelayCoverageResult{outcome: claudeCoverageFailed, err: err}, err
	}
}

// issueLease 由 relay loop 在同步绑定活进程后调用：登记 lease（liveBound 回执的
// 前置动作——先原子登记 lease，再回复 liveBound，冷事务收到回执时 lease 必已存在）。
func (r *claudeRelayCoverageRegistry) issueLease(sessionID string, lease claudeRelayCoverageLease) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[sessionID]
	if !ok || entry.generation != lease.generation {
		return
	}
	r.leases[sessionID] = &claudeRelayCoverageLease{
		generation:     lease.generation,
		sourceIdentity: lease.sourceIdentity,
		sourceCut:      lease.sourceCut,
		pid:            lease.pid,
	}
}

// validateAndCommit 在 coverage registry 的同一互斥边界内验证 generation、source
// identity/cut、lease valid，然后执行 commit（Kernel 调用），最后释放 lease。
// 任何验证失败都返回错误且不调用 commit——「校验后、commit 前切换 generation」在
// 该边界内不可发生（generation mutation 走同一 mutex）。
// 锁顺序：coverage registry mutex → ProjectionKernel（commit 闭包内），禁止反向。
func (r *claudeRelayCoverageRegistry) validateAndCommit(
	sessionID, sourceIdentity string,
	sourceCut int64,
	commit func() error,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[sessionID]
	if !ok {
		return errClaudeCoverageLeaseInvalid
	}
	lease, ok := r.leases[sessionID]
	if !ok || !lease.valid(entry.generation, sourceIdentity, sourceCut) {
		return errClaudeCoverageLeaseInvalid
	}
	if err := commit(); err != nil {
		return err
	}
	delete(r.leases, sessionID)
	return nil
}

// invalidateLease 显式作废 lease（generation replacement/clear 路径与 relay 取消/
// 错误路径共用）。幂等。
func (r *claudeRelayCoverageRegistry) invalidateLease(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.leases, sessionID)
}

// ── 冷事务侧（设计 v6 §4.3.2/§4.3.3）──────────────────────────────────────────

// claudeTrailingUnansweredAskTurnIDs 扫描 hydrate 快照，找出「非终态 turn 上仍
// pending 的 user_input part」所属 turn（尾部未答 Ask）。冷折叠内早于尾部的 turn
// 都会带 stop_reason 终态，非终态 turn 只可能是尾部在飞/未答的 turn。
func claudeTrailingUnansweredAskTurnIDs(snap SessionProjection) []string {
	var ids []string
	for _, turn := range snap.Turns {
		if turn.Status == "completed" || turn.Status == "aborted" || turn.Status == "error" {
			continue
		}
		if turn.Assistant == nil {
			continue
		}
		for _, part := range turn.Assistant.Parts {
			if part.Type == "user_input" && part.UserInputStatus == "pending" {
				ids = append(ids, turn.TurnID)
				break
			}
		}
	}
	return ids
}

// coverClaudeTrailingUnansweredAsk 在冷折叠完成后、MarkHydrateSourceIngestComplete
// 之前执行（设计 v6 §4.3.2）：识别尾部未答 Ask 后向当前 generation 的 relay loop
// 请求 coverCurrentProcess。
//   - dead：在同一 hydrate 内对每个未答 turn 追加 turn_aborted（gate 可过，正常 commit）；
//   - liveBound：返回 true（lease 已签发），最终 commit 必须走 commitClaudeHydrateWithCoverage；
//   - failed：返回错误（调用方 markHydrateFailed retryable，拒绝 commit）。
//
// 无尾部未答 Ask 时返回 (false, nil)——不请求 coverage，正常 commit。
func (h *Handlers) coverClaudeTrailingUnansweredAsk(
	backendID, sessionID string,
	admission ProjectionHydrateAdmission,
) (bool, error) {
	snap, ok := h.projectionKernel.HydrateSnapshot(backendID, sessionID)
	if !ok {
		return false, nil
	}
	pendingTurnIDs := claudeTrailingUnansweredAskTurnIDs(snap)
	if len(pendingTurnIDs) == 0 {
		return false, nil
	}
	source, _ := h.projectionKernel.HydrateSource(backendID, sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), claudeRelayCoverageTimeout*2)
	defer cancel()
	res, err := h.claudeRelayCoverage.requestCoverage(ctx, sessionID, source.Identity, admission.StartCut)
	if err != nil {
		return false, err
	}
	switch res.outcome {
	case claudeCoverageDead:
		// 同一 hydrate 内追加 turn_aborted：未答 Ask 的 turn 收口为 terminal
		//（reducer 按 §4.4 把 pending user_input 翻 pending+turn_terminated）。
		for _, turnID := range pendingTurnIDs {
			h.projectionKernel.ApplyHydrateEvent(
				backendID, sessionID, h.eventPublisher.BridgeEpoch(),
				"turn_aborted", map[string]interface{}{
					"turnId": turnID, "reason": "process_death",
				},
			)
		}
		slog.Info("go-bridge: claude hydrate coverage dead; synthesized turn_aborted",
			"backendID", backendID, "sessionPrefix", projectionSessionLogPrefix(sessionID),
			"turns", len(pendingTurnIDs))
		return false, nil
	case claudeCoverageLiveBound:
		return true, nil
	default:
		if res.err != nil {
			return false, res.err
		}
		return false, errors.New("claude relay coverage: unexpected failed outcome")
	}
}

// commitClaudeHydrateWithCoverage 是 lease 持有下的最终 commit（设计 v6 §4.3.3）：
// 在 coverage registry 的同一互斥边界内验证 generation、source identity/cut、lease
// valid，再调用 Kernel commit 并释放 lease。锁顺序 coverage registry → Kernel。
func (h *Handlers) commitClaudeHydrateWithCoverage(
	backendID, sessionID string,
	admission ProjectionHydrateAdmission,
) (ProjectionHydrateCommit, error) {
	source, _ := h.projectionKernel.HydrateSource(backendID, sessionID)
	var commit ProjectionHydrateCommit
	err := h.claudeRelayCoverage.validateAndCommit(sessionID, source.Identity, admission.StartCut, func() error {
		var cerr error
		commit, cerr = h.projectionKernel.CommitHydrateTransaction(backendID, sessionID)
		return cerr
	})
	return commit, err
}
