package grokbuild

// turn_dispatch.go — 共用 turn dispatcher（方案 §4.2/§8 p1b）。
//
// 镜像 grok-build 的 turn 结算所有权结构（1.0.16，crates/codegen/xai-grok-shell/
// src/session/acp_session_impl/）：
//   - TurnReportSlot（turn_report_slot.rs:18-146）：Free → Held → Reported，
//     turn 只允许结算一次；epoch 每 promote 递增，旧 epoch 的报告被拒。
//   - FinalizationGate（turn_task.rs:113-137）：同一时刻只有一个结算租约
//     （operation lease：单 actor 单在飞 turn）。
//   - cancel.rs:643-650 的不变量：cancel/response 两侧谁先到谁结算 front，
//     后到者被拒——绝不双重结算，也绝不悬挂。
//   - app.rs EOF 路径：进程退出时未结算的 turn 报错误，绝不静默成功。
//
// 1.0.13 实测形状（scripts/grokbuild-phase0/samples/p6-turns.json）：
//   - session/prompt 响应 {stopReason: end_turn|cancelled|…, _meta:
//     {cancellationCategory, inputTokens, outputTokens, totalTokens}}；
//     硬错误是 RPC error 对象（无 stopReason）。
//   - session/cancel 是 notification（无响应帧），取消后 prompt 响应自行以
//     cancelled 终结（D turn）。
//   - hostTurn 反馈正文经 agent_message_chunk（_meta.hostTurn=true），A/B turn。
//
// Events 单消费者不因本文件改变：结算事件的发射仍走 s.emit → 同一个 events
// channel；dispatcher 只提供 settle-once 仲裁，不建第二消费轨。

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// 终态集合（[源码] turn.rs CompletedStop/TurnOutcome；[样本] C/D）。
const (
	stopReasonEndTurn   = "end_turn"
	stopReasonCancelled = "cancelled"
)

// turnOutcome is the terminal settle of one dispatched turn (mirror:
// PromptTurnOk{stop_reason, completion_kind} + prompt response _meta).
type turnOutcome struct {
	StopReason           string // end_turn | cancelled | max_tokens | refusal | ""(硬错误)
	CancellationCategory string // wire 值 MidTurnAbort/HookDenied/…；"" 无
	Err                  error  // 硬错误：RPC reject / 写失败 / EOF（EOF 不成功）
	InputTokens          int
	OutputTokens         int
	// HostText 是本 turn 期间收集的 hostTurn 反馈正文（§7 正文组——Execute 的
	// official settle resultText；普通 Send turn 恒为空，hooks 类 host 命令才有）。
	HostText string
}

// errTurnBusy reports the operation-lease rejection: one in-flight turn per
// actor (grok-build queues via prompt_queue; this driver rail fails fast —
// callers serialize turns and an interleaved second turn would mangle the
// projection's execution state).
var errTurnBusy = &turnBusyError{}

type turnBusyError struct{}

func (e *turnBusyError) Error() string {
	return "grokbuild: turn already in flight for this session actor"
}

// turnDispatch is the per-actor single-slot turn state machine. Zero value is
// ready (tests construct grokSession literally). All methods are goroutine
// safe; the single settle guarantee holds across the readLoop (response /
// notification / EOF) and any abandoning closer.
type turnDispatch struct {
	mu          sync.Mutex
	epoch       uint64 // bumped on promote; a settle carrying an older epoch is dropped
	reqID       int    // ACP request id of the in-flight session/prompt (0 = none)
	hostText    strings.Builder
	wait        chan turnOutcome // buffered 1; written exactly once per promote
	goalCreated chan string      // first durable goal_created observation for this turn
	goalUpdated chan struct{}    // first durable goal_updated observation for this turn
	settled     chan struct{}    // closed when this operation lease becomes free
}

// promote claims the turn slot for reqID (operation lease). Returns the epoch
// and the settle channel. Busy → errTurnBusy.
func (d *turnDispatch) promote(reqID int) (uint64, <-chan turnOutcome, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reqID != 0 {
		return 0, nil, errTurnBusy
	}
	d.epoch++
	d.reqID = reqID
	d.hostText.Reset()
	d.wait = make(chan turnOutcome, 1)
	d.goalCreated = make(chan string, 1)
	d.goalUpdated = make(chan struct{}, 1)
	d.settled = make(chan struct{})
	return d.epoch, d.wait, nil
}

func (d *turnDispatch) goalUpdatedSignal() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.goalUpdated
}

func (d *turnDispatch) notifyGoalUpdated() {
	d.mu.Lock()
	if d.reqID == 0 || d.goalUpdated == nil {
		d.mu.Unlock()
		return
	}
	ch := d.goalUpdated
	d.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (d *turnDispatch) goalCreatedSignal() <-chan string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.goalCreated
}

func (d *turnDispatch) notifyGoalCreated(objective string) {
	d.mu.Lock()
	if d.reqID == 0 || d.goalCreated == nil {
		d.mu.Unlock()
		return
	}
	ch := d.goalCreated
	d.mu.Unlock()
	select {
	case ch <- objective:
	default:
	}
}

// settle delivers the outcome iff epoch is the live turn's. Exactly one
// settle per promote wins; later ones (stale response vs cancel vs EOF) are
// dropped — the mirror of TurnReportSlot's Free→Reported transition guarded
// by epoch (turn_report_slot.rs:90-103).
func (d *turnDispatch) settle(epoch uint64, out turnOutcome) bool {
	d.mu.Lock()
	if d.reqID == 0 || epoch != d.epoch {
		d.mu.Unlock()
		return false
	}
	out.HostText = d.hostText.String()
	d.reqID = 0
	ch := d.wait
	settled := d.settled
	d.mu.Unlock()

	close(settled)
	// The slot is free again; a new promote may replace d.wait, but this send
	// targets the channel captured under the lock. Buffered 1 + single winner
	// means it never blocks and never double-settles.
	ch <- out
	return true
}

// settleForRequest settles the live turn only when it is exactly reqID's —
// the response-rail arbitration (a stale prompt response must lose to a newer
// turn's slot, mirroring claim_task_finalization's identity check).
func (d *turnDispatch) settleForRequest(reqID int, out turnOutcome) bool {
	d.mu.Lock()
	if d.reqID == 0 || d.reqID != reqID {
		d.mu.Unlock()
		return false
	}
	epoch := d.epoch
	d.mu.Unlock()
	return d.settle(epoch, out)
}

// settleActive settles whatever turn is live (agent-initiated cancel rail,
// readLoop EOF, Close). false when idle.
func (d *turnDispatch) settleActive(out turnOutcome) bool {
	d.mu.Lock()
	if d.reqID == 0 {
		d.mu.Unlock()
		return false
	}
	epoch := d.epoch
	d.mu.Unlock()
	return d.settle(epoch, out)
}

// collectHostText appends host-turn feedback text to the live turn (§7 正文组
// collector). No-op when no turn is in flight.
func (d *turnDispatch) collectHostText(text string) {
	if text == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reqID != 0 {
		d.hostText.WriteString(text)
	}
}

// activeReqID reports the in-flight turn's request id (0 = idle).
func (d *turnDispatch) activeReqID() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.reqID
}

// activeSettledSignal returns a non-consuming completion signal for the
// current operation lease. Goal controls use it to wait for cancellation
// without racing the original Send/Execute owner for the turnOutcome value.
func (d *turnDispatch) activeSettledSignal() (<-chan struct{}, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reqID == 0 || d.settled == nil {
		return nil, false
	}
	return d.settled, true
}

// promptResponseMeta is the subset of session/prompt response result we act on
// (p6-turns.json C/D: result carries stopReason + _meta usage/category).
type promptResponseMeta struct {
	StopReason string `json:"stopReason"`
	Meta       struct {
		CancellationCategory string `json:"cancellationCategory,omitempty"`
		TotalTokens          int    `json:"totalTokens,omitempty"`
		InputTokens          int    `json:"inputTokens,omitempty"`
		OutputTokens         int    `json:"outputTokens,omitempty"`
	} `json:"_meta,omitempty"`
}

// parseHostTurnChunk extracts the feedback text of an agent_message_chunk
// stamped hostTurn. 1.0.13 puts the stamp on the UPDATE-level meta
// ({update:{sessionUpdate, content, _meta:{hostTurn:true}}}) while the
// transport-level params._meta carries promptId/totalTokens (p6 A/B samples —
// two distinct meta layers). ok=false for every other shape.
func parseHostTurnChunk(params []byte) (text string, ok bool) {
	var outer struct {
		Update struct {
			SessionUpdate string          `json:"sessionUpdate"`
			Content       json.RawMessage `json:"content"`
			Meta          struct {
				HostTurn bool `json:"hostTurn,omitempty"`
			} `json:"_meta,omitempty"`
		} `json:"update"`
	}
	if err := json.Unmarshal(params, &outer); err != nil {
		return "", false
	}
	if outer.Update.SessionUpdate != "agent_message_chunk" || !outer.Update.Meta.HostTurn {
		return "", false
	}
	p := sessionUpdatePayload{Content: outer.Update.Content}
	if !p.hasContent() {
		return "", false
	}
	return p.contentText(), true
}

// --- grokSession glue (shared dispatch rail for Send + Execute) ---

// dispatchTurn promotes the shared turn slot, performs the Send-time pre-turn
// bookkeeping, and writes session/prompt. The slot is claimed BEFORE the write
// so a fast local response cannot be lost (audit P0-2, preserved from the
// former pendingPromptID registration). Callers: Send (user prompt, async —
// ignores the channel) and executeHostCommand (waits on it, ctx-bounded).
func (s *grokSession) dispatchTurn(content []contentBlock) (<-chan turnOutcome, error) {
	return s.dispatchTurnWithStart(content, nil)
}

// dispatchTurnWithStart is dispatchTurn with one synchronous lifecycle hook.
// The hook runs only after the turn lease is acquired, and before the request
// can produce updates or a terminal. It lets host-owned UI lifecycle (currently
// the official pager's manual-compaction row) preserve causal event order
// without weakening the shared dispatcher's single-active-turn gate.
func (s *grokSession) dispatchTurnWithStart(content []contentBlock, onStart func()) (<-chan turnOutcome, error) {
	id := s.idCounter.next()
	epoch, wait, err := s.turn.promote(id)
	if err != nil {
		return nil, err
	}
	// A stale un-stamped echo from a previous turn must not leak into this
	// one; stale questions from a finished turn must not survive either
	// (their wire ids are already dead — upstream drops late responses).
	s.pendingPermsMu.Lock()
	s.pendingUserEcho = ""
	staleQuestions := make([]string, 0, len(s.pendingQuestions))
	for toolCallID := range s.pendingQuestions {
		staleQuestions = append(staleQuestions, toolCallID)
	}
	s.pendingQuestions = make(map[string]*pendingAskUserQuestion)
	s.pendingPermsMu.Unlock()
	for _, toolCallID := range staleQuestions {
		markQuestionConsumed(toolCallID)
	}
	// Reset terminal flag for the new turn.
	s.terminalDone.Store(false)

	s.emit(core.Event{Type: core.EventTurnStarted})
	if onStart != nil {
		onStart()
	}

	if err := s.writeRequest(id, "session/prompt", sessionPromptParams{
		SessionID: s.CurrentSessionID(),
		Prompt:    content,
	}); err != nil {
		// Fail the waiter explicitly — a turn whose request never left the
		// process must not hang an Execute caller (cancel.rs:643-650
		// invariant: the front's waiter is ALWAYS resolved).
		s.turn.settle(epoch, turnOutcome{Err: fmt.Errorf("grokbuild: write session/prompt: %w", err)})
		return nil, err
	}
	return wait, nil
}

// settlePromptResponse arbitrates a session/prompt response against the live
// turn slot. The winner emits the terminal event with the preserved
// stopReason/_meta (§8: stopReason 保留). Returns the settled outcome.
func (s *grokSession) settlePromptResponse(resp *jsonrpcResponse) (turnOutcome, bool) {
	var idNum int
	if err := json.Unmarshal(resp.ID, &idNum); err != nil {
		return turnOutcome{}, false
	}
	out := turnOutcome{}
	if resp.Error != nil {
		// Hard error = RPC reject, no stopReason (ADMISSION.md §二.1).
		out.Err = fmt.Errorf("session/prompt error %d", resp.Error.Code)
	} else if len(resp.Result) > 0 {
		var meta promptResponseMeta
		if err := json.Unmarshal(resp.Result, &meta); err != nil {
			out.Err = fmt.Errorf("grokbuild: decode session/prompt result: %w", err)
		} else {
			out.StopReason = meta.StopReason
			out.CancellationCategory = meta.Meta.CancellationCategory
			out.InputTokens = meta.Meta.InputTokens
			out.OutputTokens = meta.Meta.OutputTokens
		}
	}
	if !s.turn.settleForRequest(idNum, out) {
		return turnOutcome{}, false
	}
	return out, true
}

// emitTurnTerminal emits the single terminal event for a settled turn. Only
// the settling winner calls this — the events channel stays single-writer
// (readLoop) for every other event, and the terminal dedupe rides the
// dispatcher's settle-once guarantee instead of a second flag.
func (s *grokSession) emitTurnTerminal(out turnOutcome) {
	if out.Err != nil {
		// Log only the fixed wrapper text; never the agent payload.
		slog.Warn("grokbuild: turn settled as error", "error_class", rpcErrorClass(out.Err))
		s.emit(core.Event{
			Type:  core.EventError,
			Error: out.Err,
			Content: fmt.Sprintf("grok turn ended with error: %s",
				logErrorClass(out.Err)),
			Done: true,
		})
		return
	}
	s.emit(core.Event{
		Type:                 core.EventResult,
		Done:                 true,
		StopReason:           out.StopReason,
		CancellationCategory: out.CancellationCategory,
		InputTokens:          out.InputTokens,
		OutputTokens:         out.OutputTokens,
	})
}

// errTurnActorDead settles turns whose actor ended before completion — EOF
// never succeeds (grok-build app.rs shutdown: an unresolved turn surfaces as
// an error, never a silent EndTurn).
var errTurnActorDead = errors.New("grokbuild: session actor ended before turn completed")

// abandonTurn settles the live turn as an actor-death error and emits the
// terminal. Called from readLoop exit and Close.
func (s *grokSession) abandonTurn() {
	if s.turn.settleActive(turnOutcome{Err: errTurnActorDead}) {
		s.emitTurnTerminal(turnOutcome{Err: errTurnActorDead})
	}
}
