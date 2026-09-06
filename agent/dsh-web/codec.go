package dshweb

// Session-event codec: dsh SessionEvent → core.Event (design §3.3 mapping,
// §4.1/M3: COPIED from agent/dsh/codec.go, not imported — the mux
// session/event frame's event field is the same strict envelope + wide-data
// shape as the disk log, so the mapping table is identical).
//
// Two adaptations for the Web-API carrier, both documented against the
// official v1 semantics (§3.2):
//
//  1. Seq gate is BASELINE-TOLERANT. The stdio codec demanded seq 0 first
//     (full-log replay). A mux stream joins mid-log: the first frame's seq is
//     the baseline, and after a reconnect the officially unsupported `since`
//     means gaps are expected — a gap/conflict RESETS that session's codec
//     state (fresh baseline on the next frame) instead of killing a process
//     there is no process to kill. The §8-5 projection forceCold path
//     reconciles any lost window from session.history.
//
//  2. Orphan turn adoption. A turn scoped frame (assistant/chunk, tool/call…)
//     may arrive for a turn already in flight (external turn started before
//     this stream attached). The frame's own data carries turn/step; the
//     codec adopts that turn as active and emits the (real, data-derived)
//     turn_started so downstream sees the boundary. Content missed before
//     attach is the hydrate path's job, never fabricated here.
//
// Everything else — validate-then-map, chunk/assembled peer validation,
// fail-visibly terminals — is the copied stdio semantics unchanged.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	noTurn = -1
	noStep = -1
)

// codecReset marks a per-session codec state reset (non-fatal on this carrier).
type codecReset struct{ reason string }

func (e *codecReset) Error() string { return "dshweb codec reset: " + e.reason }

func resetf(format string, args ...any) *codecReset {
	return &codecReset{reason: fmt.Sprintf(format, args...)}
}

// sessionCodec decodes one session's event stream (owned by the mux pump
// goroutine — no locking).
type sessionCodec struct {
	sessionPrefix string // short session id for TurnID derivation

	activeTurn   int
	activeStep   int
	activeTurnID string

	toolCallNames map[string]string
	lastUsage     *dshUsage
	contextWindow int

	// seq gate state (baseline-tolerant variant).
	expectedSeq           int64
	sawFirstSeq           bool
	lastAcceptedCanonical string

	// chunk/assembled peer state（§3.7 唯一 owner + peer 校验）。openBlocks 只
	// 记 index → blockType 标记，用于 delta 的类型门；内容真值由 delta 路径直发，
	// block-end / assistant/message / tool/call 按官方 partial.ts 整值替换语义
	// 接管，不再累积比对（2026-09-05 真机 goal 轮五连 reset 教训）。
	openBlocks map[int]string

	// host 斜杠命令 + 计划模式折叠状态（command_fold.go；live 帧与 history
	// 冷拉共用同一官方折叠）。codec reset 会丢弃该状态——三类事件本身已
	// 收编不再触发 reset，仅其它未知类型 reset 后需等下一条 plan 事件或
	// 冷拉重建（计划模式 chip 短暂缺失，非数据损失）。
	runningCommands map[string]runningCommand
	planFold        planFold
	planViewKnown   bool
	planViewActive  bool
	planViewPending bool

	// 并行子代理 workflow 折叠状态（workflow_fold.go；官方 ui-workflow-run
	// workflowRunDefinition 同一折叠）。四类 tool-workflow 事件已收编不再触发
	// reset（2026-09-06 前每个事件都走 default resetf 杀码器——subagent 并行
	// 运行期间直播流反复重置、丢 openBlocks 增量）。
	workflowFold workflowFold

	// goal 投影整值快照（goal/change 为全量替换语义，无折叠状态机）。
	// goalViewKnown 标记已见首个快照；goalView nil = 当前无目标/已清除。
	// codec reset 丢弃后等下一条 goal/change 或冷拉重建（横条短暂缺失，
	// 非数据损失——与 planView 同类边界）。
	goalViewKnown bool
	goalView      *core.GoalEvent
	lastGoalView  *core.GoalEvent
}

// dshwSessionPrefix derives the short session prefix used in TurnID minting.
// Shared by the live codec and the cold history mapping so both sides stamp
// the same identity (cold/live merge, 2026-09-06 rework ⑧).
func dshwSessionPrefix(sessionID string) string {
	prefix := sessionID
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return prefix
}

// dshwTurnID mints the turn identity from the official turn/start number
// (journal ground truth: {"turn": N}). Same formula as live adoptTurn.
func dshwTurnID(sessionID string, turn int) string {
	return fmt.Sprintf("dshw-%s-t%d", dshwSessionPrefix(sessionID), turn)
}

func newSessionCodec(sessionID string) *sessionCodec {
	prefix := dshwSessionPrefix(sessionID)
	return &sessionCodec{
		sessionPrefix: prefix,
		activeTurn:    noTurn,
		activeStep:    noStep,
		toolCallNames: map[string]string{},
		openBlocks:    map[int]string{},
	}
}

func (c *sessionCodec) resetStepPeerState() {
	c.openBlocks = map[int]string{}
}

// canonicalEnvelope normalizes one envelope for exact-replay comparison.
func canonicalEnvelope(env *sessionEventWire) string {
	var data any
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			data = nil
		}
	}
	canonical, err := json.Marshal(struct {
		Type      string          `json:"type"`
		Seq       int64           `json:"seq"`
		Time      int64           `json:"time"`
		Ignorable json.RawMessage `json:"ignorable,omitempty"`
		Data      any             `json:"data"`
	}{env.Type, env.Seq, env.Time, env.Ignorable, data})
	if err != nil {
		return ""
	}
	return string(canonical)
}

// checkSeq is the baseline-tolerant gate. Returns (accepted, replayed, err);
// err is *codecReset — the caller resets this session's codec and drops the
// frame (the frame itself is NOT re-mapped under polluted state).
func (c *sessionCodec) checkSeq(env *sessionEventWire) (accepted bool, replayed bool, err error) {
	if env.Seq < 0 {
		return false, false, resetf("negative seq %d (%s)", env.Seq, env.Type)
	}
	if !c.sawFirstSeq {
		// Mid-log join: adopt this seq as the baseline (adaptation 1).
		c.expectedSeq = env.Seq + 1
		c.sawFirstSeq = true
		c.lastAcceptedCanonical = canonicalEnvelope(env)
		return true, false, nil
	}
	switch {
	case env.Seq == c.expectedSeq:
		c.expectedSeq++
		c.lastAcceptedCanonical = canonicalEnvelope(env)
		return true, false, nil
	case env.Seq == c.expectedSeq-1:
		if canonicalEnvelope(env) == c.lastAcceptedCanonical {
			return false, true, nil // idempotent replay skip
		}
		return false, false, resetf("conflicting duplicate seq %d (%s)", env.Seq, env.Type)
	case env.Seq > c.expectedSeq:
		return false, false, resetf("seq gap: got %d, expected %d (%s)", env.Seq, c.expectedSeq, env.Type)
	default:
		return false, false, resetf("seq regression: got %d, expected %d (%s)", env.Seq, c.expectedSeq, env.Type)
	}
}

func (c *sessionCodec) ignorableMarker(env *sessionEventWire) bool {
	if len(env.Ignorable) == 0 {
		return false
	}
	var b bool
	return json.Unmarshal(env.Ignorable, &b) == nil && b
}

// adoptTurn activates the turn (and step) a turn-scoped frame names
// (adaptation 2), emitting the real, data-derived turn_started.
func (c *sessionCodec) adoptTurn(turn, step int) []core.Event {
	c.activeTurn = turn
	c.activeTurnID = dshwTurnID(c.sessionPrefix, turn)
	c.activeStep = step
	c.lastUsage = nil
	c.resetStepPeerState()
	return []core.Event{{Type: core.EventTurnStarted, TurnID: c.activeTurnID}}
}

// apply maps one envelope to core.Events. A *codecReset error means: drop the
// frame, replace this codec with a fresh one (the next frame re-baselines).
func (c *sessionCodec) apply(env *sessionEventWire) ([]core.Event, error) {
	_, replayed, err := c.checkSeq(env)
	if err != nil {
		return nil, err
	}
	if replayed {
		return nil, nil
	}
	if c.ignorableMarker(env) {
		return nil, nil
	}

	switch env.Type {
	case "turn/start":
		return c.applyTurnStart(env)
	case "turn/end":
		return c.applyTurnEnd(env)
	case "step/start":
		return c.applyStepStart(env)
	case "step/end":
		return c.applyStepEnd(env)
	case "user/message":
		return c.applyUserMessage(env)
	case "assistant/chunk":
		return c.applyAssistantChunk(env)
	case "assistant/message":
		return c.applyAssistantMessage(env)
	case "tool/call":
		return c.applyToolCall(env)
	case "tool/result":
		return c.applyToolResult(env)
	case "todo/write":
		return c.applyTodoWrite(env)
	case "command/run":
		return c.applyCommandRun(env)
	case "command/done":
		return c.applyCommandDone(env)
	case "plan/mode":
		return c.applyPlanMode(env)
	case "goal/change":
		return c.applyGoalChange(env)
	case "tool-workflow/run-start":
		return c.applyWorkflowRunStart(env)
	case "tool-workflow/agent-start":
		return c.applyWorkflowAgentStart(env)
	case "tool-workflow/agent-end":
		return c.applyWorkflowAgentEnd(env)
	case "tool-workflow/run-end":
		return c.applyWorkflowRunEnd(env)
	case "request/header":
		return nil, nil
	case "request/context":
		return c.applyRequestContext(env)

	// Class ②: known control-plane events with no timeline effect.
	// session/title-llm-request（标题生成的辅助 LLM 调用记录；标题本体经
	// session/title 事件与标题投影落地）在官方 known-event-types.ts 注册表
	// 中，且 2026-08-16 真机矩阵证实无 timeline 内容——此前两版因未知类型
	// 重置杀了码器状态（双 turn_started、身份断裂）。
	case "permission/preset", "sandbox/mode", "approval/policy", "agent/inbox/spliced", "session/title",
		"session/title-llm-request",
		// approval/asked + approval/decided 是官方 log-only 审计对（known-event-types
		// 注册表；user-approval README：「只写入日志，人类权限 UI 不属于上下文」）。
		// 真机 2026-08-16 行 6：当未知类型重置，杀 mid-turn 码器。真正的 iOS 权限
		// 面走 mux approval/requested → permission_request，不走这两条审计事件。
		"approval/asked", "approval/decided",
		// 斜杠命令执行的 durable 副产物（rc.2 known-event-types.ts 注册表内事件，
		// ignorable 旁路不适用）。命令卡片/计划模式/目标状态由已收编的
		// command/run|done + plan/mode + goal/change 承载（官方 web 同样不把这
		// 些画成时间线节点）；known-drop 防执行后 reset 码器。
		"compaction/start", "compaction/prune", "compaction/summary", "compaction/end",
		"feedback/record":
		return nil, nil

	default:
		// Class ③/unknown: REQUIRED — fail visibly for this frame (reset),
		// never a silent catch-all.
		return nil, resetf("unknown required event type %q (seq %d)", env.Type, env.Seq)
	}
}

func decodeData(env *sessionEventWire, v any) error {
	if len(env.Data) == 0 {
		return resetf("event %q (seq %d) missing data payload", env.Type, env.Seq)
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		return resetf("event %q (seq %d) data schema violation: %v", env.Type, env.Seq, err)
	}
	return nil
}

// validateActiveTurnStep enforces validate-then-map; orphan frames ADOPT the
// turn they name (adaptation 2) — external turns in flight at attach time stay
// visible instead of erroring the stream.
func (c *sessionCodec) validateActiveTurnStep(envType string, envSeq int64, turn, step int) ([]core.Event, error) {
	if c.activeTurn == noTurn {
		if turn < 1 {
			return nil, resetf("%s (seq %d) orphan frame with invalid turn %d", envType, envSeq, turn)
		}
		return c.adoptTurn(turn, step), nil
	}
	if turn != c.activeTurn {
		// A turn switch without turn/end (missed boundary after a gap):
		// settle the old turn as an error terminal, then adopt the new one —
		// never attribute one turn's frames to another.
		settled := []core.Event{{
			Type:   core.EventResult,
			Done:   true,
			TurnID: c.activeTurnID,
			Error:  fmt.Errorf("turn %d superseded by %d without turn/end (stream gap)", c.activeTurn, turn),
		}}
		return append(settled, c.adoptTurn(turn, step)...), nil
	}
	if step != c.activeStep {
		return nil, resetf("%s (seq %d) step %d does not match active step %d", envType, envSeq, step, c.activeStep)
	}
	return nil, nil
}

func (c *sessionCodec) applyTurnStart(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Turn int `json:"turn"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	if d.Turn < 1 {
		return nil, resetf("turn/start (seq %d) invalid turn %d", env.Seq, d.Turn)
	}
	if c.activeTurn != noTurn {
		return nil, resetf("nested turn/start (seq %d): turn %d still active", env.Seq, c.activeTurn)
	}
	return c.adoptTurn(d.Turn, noStep), nil
}

func (c *sessionCodec) applyTurnEnd(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Turn   int `json:"turn"`
		Reason struct {
			Kind string `json:"kind"`
		} `json:"reason"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	if c.activeTurn == noTurn {
		// turn/end for a turn that started before attach and produced no
		// mapped frames: nothing to settle (the turn was never surfaced).
		return nil, nil
	}
	if d.Turn != c.activeTurn {
		return nil, resetf("turn/end (seq %d) turn %d does not match active turn %d", env.Seq, d.Turn, c.activeTurn)
	}
	if c.activeStep != noStep {
		return nil, resetf("turn/end (seq %d) with unclosed step %d", env.Seq, c.activeStep)
	}

	ev := core.Event{
		Type:   core.EventResult,
		Done:   true,
		TurnID: c.activeTurnID,
	}
	switch d.Reason.Kind {
	case "completed", "max-tokens":
		// Verified terminal outcomes; max-tokens is a token-cap completion.
	default:
		// 坑 7/8 红线: never fabricate success — the raw reason rides the
		// terminal verbatim.
		ev.Error = fmt.Errorf("turn ended with reason %q", d.Reason.Kind)
	}
	if c.lastUsage != nil {
		ev.InputTokens = c.lastUsage.InputTokens
		ev.OutputTokens = c.lastUsage.OutputTokens
	}

	c.activeTurn = noTurn
	c.activeStep = noStep
	c.activeTurnID = ""
	c.lastUsage = nil
	c.resetStepPeerState()
	return []core.Event{ev}, nil
}

func (c *sessionCodec) applyStepStart(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Turn int `json:"turn"`
		Step int `json:"step"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	if d.Turn < 1 || d.Step < 1 {
		return nil, resetf("step/start (seq %d) invalid turn/step %d/%d", env.Seq, d.Turn, d.Step)
	}
	var pre []core.Event
	if c.activeTurn == noTurn {
		pre = c.adoptTurn(d.Turn, noStep)
	} else if d.Turn != c.activeTurn {
		return nil, resetf("step/start (seq %d) turn %d does not match active turn %d", env.Seq, d.Turn, c.activeTurn)
	}
	if c.activeStep != noStep {
		return nil, resetf("nested step/start (seq %d): step %d still open", env.Seq, c.activeStep)
	}
	c.activeStep = d.Step
	c.resetStepPeerState()
	return pre, nil
}

func (c *sessionCodec) applyStepEnd(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Turn int `json:"turn"`
		Step int `json:"step"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	if c.activeTurn == noTurn {
		return nil, nil // step machinery of a pre-attach turn: no timeline effect
	}
	if d.Turn != c.activeTurn || d.Step != c.activeStep {
		return nil, resetf("step/end (seq %d) %d/%d does not match active %d/%d", env.Seq, d.Turn, d.Step, c.activeTurn, c.activeStep)
	}
	if len(c.openBlocks) != 0 {
		return nil, resetf("step/end (seq %d) with %d unclosed assistant block(s)", env.Seq, len(c.openBlocks))
	}
	c.activeStep = noStep
	return nil, nil
}

func (c *sessionCodec) applyUserMessage(env *sessionEventWire) ([]core.Event, error) {
	var d dshUserMessageData
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	switch {
	case d.Source == nil || d.Source.Kind == "user":
		// The only turnless timeline shape; turn identity derives from active
		// (adopt when an in-flight turn exists — a queued prompt echo).
		if c.activeTurn == noTurn {
			return nil, nil // pre-attach turn context: hydrate owns this
		}
		return []core.Event{{
			Type:    core.EventUserMessage,
			Content: joinTextBlocks(d.Content),
			TurnID:  c.activeTurnID,
			ItemID:  d.ID,
		}}, nil
	case d.Source.Kind == "plugin":
		return nil, nil // permission runtime context, never a user prompt
	case d.Source.Kind == "subagent-settled":
		// 官方 continuation.ts settle 通知（注入父会话的 user/message，
		// source{kind:"subagent-settled", form:"notice", summary, senderSessionId}）。
		// 官方 UI 渲染 ContextInjectionRow「上下文注入 · subagent-settled · <summary>」。
		// 独立系统行（turnId "ctx:<itemId>"，镜像 session_command 模式），不挂
		// activeTurn——注入可发生在父会话任意状态（idle followup / busy steer）。
		// Summary 空 = 未知形状，fail-open 静默丢（不造行、不 reset 流）。
		if strings.TrimSpace(d.Source.Summary) == "" {
			return nil, nil
		}
		return []core.Event{{
			Type: core.EventContextInjection,
			ContextInjection: &core.ContextInjectionEvent{
				ItemID:          fmt.Sprintf("ctxinj:%d", env.Seq),
				Kind:            d.Source.Kind,
				Form:            d.Source.Form,
				Summary:         d.Source.Summary,
				Text:            joinTextBlocks(d.Content),
				SenderSessionID: d.Source.SenderSessionID,
			},
		}}, nil
	default:
		// 官方 message.ts start()：source.kind != "user" 一律是注入上下文
		// （ContextMessageNode，渲染为「上下文注入 · <kind>」行）——goal 轮的
		// <goal_round>、会话基线的 agent-instructions / skill-catalog 都走
		// 这里（subagent-settled 已在上一分支单独出行为 2026-09-06 §13.3）。
		// 官方对未知 kind 按 label 合并扩展，从不视为协议违规；此处与其余 kind
		// 维持对称 known-drop（CordCode 投影仅落地 subagent-settled 行，其余
		// recorded difference 见 owner 矩阵 §2.9 / 方案 §13.7）。2026-09-05
		// 23:25 真机 goal 轮 seq 1481 source.kind "goal" 曾在此 reset 整条流
		// （P0）。
		return nil, nil
	}
}

// dshChunk is the assistant/chunk payload (field names per the pinned schema:
// text-delta/reasoning-delta carry `text`; tool-call-delta carries
// `argumentsDelta`).
type dshChunk struct {
	Type           string    `json:"type"`
	Index          int       `json:"index"`
	BlockType      string    `json:"blockType"`
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Text           string    `json:"text"`
	ArgumentsDelta string    `json:"argumentsDelta"`
	Usage          *dshUsage `json:"usage"`
	Block          *struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"block"`
}

func (c *sessionCodec) applyAssistantChunk(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Turn  int      `json:"turn"`
		Step  int      `json:"step"`
		Chunk dshChunk `json:"chunk"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	pre, err := c.validateActiveTurnStep("assistant/chunk", env.Seq, d.Turn, d.Step)
	if err != nil {
		return nil, err
	}
	// pre may carry an adopted turn_started; keep it first.
	emit := func(events ...core.Event) []core.Event { return append(pre, events...) }

	switch d.Chunk.Type {
	case "block-start":
		switch d.Chunk.BlockType {
		case "text", "reasoning", "tool-call":
			if _, exists := c.openBlocks[d.Chunk.Index]; exists {
				return nil, resetf("assistant/chunk block-start (seq %d) reopens index %d", env.Seq, d.Chunk.Index)
			}
			c.openBlocks[d.Chunk.Index] = d.Chunk.BlockType
			return pre, nil
		default:
			return nil, resetf("assistant/chunk block-start (seq %d) unknown blockType %q", env.Seq, d.Chunk.BlockType)
		}

	case "text-delta":
		if c.openBlocks[d.Chunk.Index] != "text" {
			return nil, resetf("assistant/chunk text-delta (seq %d) outside an open text block (index %d)", env.Seq, d.Chunk.Index)
		}
		return emit(core.Event{
			Type: core.EventText, Content: d.Chunk.Text,
			TurnID: c.activeTurnID, ItemID: c.activeTurnID,
		}), nil

	case "reasoning-delta":
		if c.openBlocks[d.Chunk.Index] != "reasoning" {
			return nil, resetf("assistant/chunk reasoning-delta (seq %d) outside an open reasoning block (index %d)", env.Seq, d.Chunk.Index)
		}
		return emit(core.Event{
			Type: core.EventThinking, Content: d.Chunk.Text,
			TurnID: c.activeTurnID, ItemID: c.activeTurnID,
		}), nil

	case "tool-call-delta":
		if c.openBlocks[d.Chunk.Index] != "tool-call" {
			return nil, resetf("assistant/chunk tool-call-delta (seq %d) outside an open tool-call block (index %d)", env.Seq, d.Chunk.Index)
		}
		if d.Chunk.ID != "" && d.Chunk.Name != "" {
			c.toolCallNames[d.Chunk.ID] = d.Chunk.Name
		}
		return pre, nil

	case "block-end":
		// 官方 PartialAccumulator.push block-end：无条件 blocks[index] =
		// block 整值替换——无 open 校验、无类型校验、无与 delta 累积的比对
		// （sparse on purpose：block-start 允许乱序留洞）。活体 2026-09-05
		// 23:32 seq 16586 实证 delta 流为带空格 JSON、block-end 为归一化
		// 紧凑 JSON，任何比对都必然分歧。文本/推理的流式真值已由 delta 路
		// 径发出，此处不重复发；无 open 块的 block-end（mid-log join 边界）
		// 同样只记账工具名——该块的内容增量冷拉由 hydrate 覆盖。
		delete(c.openBlocks, d.Chunk.Index)
		if d.Chunk.Block == nil {
			return nil, resetf("assistant/chunk block-end (seq %d) missing block payload", env.Seq)
		}
		if d.Chunk.Block.Type == "tool-call" && d.Chunk.Block.ID != "" && d.Chunk.Block.Name != "" {
			c.toolCallNames[d.Chunk.Block.ID] = d.Chunk.Block.Name
		}
		return pre, nil

	case "usage":
		if d.Chunk.Usage == nil {
			return nil, resetf("assistant/chunk usage (seq %d) missing usage payload", env.Seq)
		}
		u := *d.Chunk.Usage
		c.lastUsage = &u
		return emit(contextUsageEvent(&u, c.contextWindow)), nil

	case "finish":
		return pre, nil

	default:
		return nil, resetf("assistant/chunk (seq %d) unknown chunk type %q", env.Seq, d.Chunk.Type)
	}
}

// contextUsageEvent builds the pressure projection (§3.7).
func contextUsageEvent(u *dshUsage, window int) core.Event {
	used := u.InputTokens + u.CacheReadTokens
	return core.Event{
		Type: core.EventContextUsageUpdated,
		ContextUsage: &core.ContextUsage{
			InputTokens:           u.InputTokens,
			CachedInputTokens:     u.CacheReadTokens,
			OutputTokens:          u.OutputTokens,
			ReasoningOutputTokens: u.ReasoningTokens,
			UsedTokens:            used,
			TotalTokens:           used,
			ContextWindow:         window,
		},
	}
}

// applyAssistantMessage — settled message: 官方语义里 settlement 整值取代
// partial（partial.ts：finish 后的 assistant/message supersedes the partial），
// 从不与 delta 累积比对或 reset（活体 seq 16589：settlement content 与 delta
// 流并存且合法）。live 文本/推理的真值属 chunk delta 路径，此处不 re-source。
func (c *sessionCodec) applyAssistantMessage(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Turn    int `json:"turn"`
		Step    int `json:"step"`
		Message struct {
			Content []struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"content"`
		} `json:"message"`
		Usage *dshUsage `json:"usage"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	pre, err := c.validateActiveTurnStep("assistant/message", env.Seq, d.Turn, d.Step)
	if err != nil {
		return nil, err
	}

	// tool-call settlement 只记账工具名（供 tool/result 命中）。arguments 的
	// 权威值随事件本身下发到 reducer，codec 不再缓存比对。
	for _, blk := range d.Message.Content {
		if blk.Type == "tool-call" && blk.ID != "" && blk.Name != "" {
			c.toolCallNames[blk.ID] = blk.Name
		}
	}
	return pre, nil
}

func (c *sessionCodec) applyToolCall(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Turn      int    `json:"turn"`
		Step      int    `json:"step"`
		CallID    string `json:"callId"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	pre, err := c.validateActiveTurnStep("tool/call", env.Seq, d.Turn, d.Step)
	if err != nil {
		return nil, err
	}
	if d.CallID == "" {
		return nil, resetf("tool/call (seq %d) missing callId", env.Seq)
	}
	// 官方 tool.ts：tool/call 是工具执行的权威事件，arguments 随事件整值下发
	// （同 block-end 替换语义；活体 seq 16590 紧随归一化 block-end）。codec
	// 只记账名字，不缓存参数比对。
	c.toolCallNames[d.CallID] = d.Name
	// ToolInput 契约是 "human-readable summary"（core/message.go）：直传原始
	// JSON 会让 iOS running-status ticker 与活动行的 detail 变成
	// `{"command": ...}`，且被 ticker 的 sanitizer 打回通用「正在执行工具」
	// （owner 2026-08-28）。与 history.go hydration 的 toolStepTitle 同源，
	// 保证冷/热渲染一致。
	return append(pre, core.Event{
		Type:      core.EventToolUse,
		ToolName:  d.Name,
		ToolInput: toolStepTitle(d.Name, []byte(d.Arguments)),
		RequestID: d.CallID,
		ItemID:    d.CallID,
		TurnID:    c.activeTurnID,
	}), nil
}

func (c *sessionCodec) applyToolResult(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Turn    int `json:"turn"`
		Step    int `json:"step"`
		Message struct {
			Source struct {
				Kind   string `json:"kind"`
				CallID string `json:"callId"`
			} `json:"source"`
			Content []struct {
				Type       string `json:"type"`
				ToolCallID string `json:"toolCallId"`
				Content    []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	pre, err := c.validateActiveTurnStep("tool/result", env.Seq, d.Turn, d.Step)
	if err != nil {
		return nil, err
	}

	callID := d.Message.Source.CallID
	var text strings.Builder
	isError := false
	found := false
	for _, blk := range d.Message.Content {
		if blk.Type != "tool-result" {
			continue
		}
		found = true
		if blk.ToolCallID != "" {
			callID = blk.ToolCallID
		}
		if blk.IsError {
			isError = true
		}
		for _, inner := range blk.Content {
			if inner.Type == "text" {
				text.WriteString(inner.Text)
			}
		}
	}
	if !found {
		return nil, resetf("tool/result (seq %d) missing tool-result content block", env.Seq)
	}
	if callID == "" {
		return nil, resetf("tool/result (seq %d) missing callId", env.Seq)
	}

	status := "completed"
	if isError {
		status = "failed"
	}
	success := !isError
	return append(pre, core.Event{
		Type:        core.EventToolResult,
		ToolName:    c.toolCallNames[callID],
		ToolResult:  text.String(),
		ToolStatus:  status,
		ToolSuccess: &success,
		RequestID:   callID,
		ItemID:      callID,
		TurnID:      c.activeTurnID,
	}), nil
}

func (c *sessionCodec) applyTodoWrite(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	todos := make([]core.Todo, 0, len(d.Todos))
	for _, t := range d.Todos {
		todos = append(todos, core.Todo{Content: t.Content, Status: t.Status})
	}
	return []core.Event{{Type: core.EventPlan, Plan: todos}}, nil
}

func (c *sessionCodec) applyRequestContext(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Provider      string `json:"provider"`
		Model         string `json:"model"`
		ContextWindow int    `json:"contextWindow"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	c.contextWindow = d.ContextWindow
	if c.lastUsage != nil && c.contextWindow > 0 {
		return []core.Event{contextUsageEvent(c.lastUsage, c.contextWindow)}, nil
	}
	return nil, nil
}

// dshCommandRunData 是 command/run 的 data 载荷（座位活体形状）：
// {commandId, name, args?, source{kind}}。args 用 *string 区分缺席（官方
// 折叠里 args === undefined 不改变 plan 状态）与空串（= 进计划模式）。
type dshCommandRunData struct {
	CommandID string  `json:"commandId"`
	Name      string  `json:"name"`
	Args      *string `json:"args"`
}

// dshCommandDoneData 是 command/done 的 data 载荷：
// {commandId, kind: success|error, text?, sourceEventSeq?}。
type dshCommandDoneData struct {
	CommandID string  `json:"commandId"`
	Kind      string  `json:"kind"`
	Text      *string `json:"text"`
}

// applyCommandRun 收编 command/run：暂存 name/args 供 done 续接（官方
// commandFromDone 的 previous 续接），发 running 事件；命令名进入 plan
// 折叠（/plan 在下个 accepted pre-step 前是 pending）。
func (c *sessionCodec) applyCommandRun(env *sessionEventWire) ([]core.Event, error) {
	var d dshCommandRunData
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	commandID := strings.TrimSpace(d.CommandID)
	name := strings.TrimSpace(d.Name)
	if commandID == "" || name == "" {
		return nil, resetf("command/run (seq %d) missing commandId/name", env.Seq)
	}
	args := ""
	argsPresent := false
	if d.Args != nil {
		args = *d.Args
		argsPresent = true
	}
	if c.runningCommands == nil {
		c.runningCommands = map[string]runningCommand{}
	}
	c.runningCommands[commandID] = runningCommand{name: name, args: args, argsPresent: argsPresent}
	c.planFold.onCommandRun(commandID, name, args, argsPresent)
	inputLine := ""
	if name == "goal" {
		// 官方 ui-goal goal-command-input.ts goalCommandText：只有 goal 注册
		// command-input 投影（右对齐用户气泡），"/goal" + args 去尾空白。
		inputLine = "/" + name + strings.TrimRight(args, " \t\n\r\v\f")
	}
	out := []core.Event{{Type: core.EventSessionCommand, SessionCommand: &core.SessionCommandEvent{
		CommandID: commandID,
		Name:      name,
		Args:      args,
		Kind:      "running",
		InputLine: inputLine,
	}}}
	out = append(out, c.planViewEvents()...)
	return out, nil
}

// applyCommandDone 收编 command/done：run 已见则续接 name/args（未见时
// Name 为空，镜像官方 done-only CommandNode.name=null），发 settle 事件；
// kind 词表外（success|error 之外）按 schema violation reset。
func (c *sessionCodec) applyCommandDone(env *sessionEventWire) ([]core.Event, error) {
	var d dshCommandDoneData
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	commandID := strings.TrimSpace(d.CommandID)
	if commandID == "" {
		return nil, resetf("command/done (seq %d) missing commandId", env.Seq)
	}
	if d.Kind != "success" && d.Kind != "error" {
		return nil, resetf("command/done (seq %d) unknown kind %q", env.Seq, d.Kind)
	}
	name, args := "", ""
	if run, ok := c.runningCommands[commandID]; ok {
		name, args = run.name, run.args
		delete(c.runningCommands, commandID)
	}
	text := ""
	if d.Text != nil {
		text = *d.Text
	}
	inputLine := ""
	if name == "goal" {
		// settle 折叠续接输入行（reducer part 整体替换，不带则气泡随
		// settle 消失）；done-only 行 name 为空，与官方无 run 不回显一致。
		inputLine = "/goal" + strings.TrimRight(args, " \t\n\r\v\f")
	}
	c.planFold.onCommandDone(commandID, d.Kind)
	out := []core.Event{{Type: core.EventSessionCommand, SessionCommand: &core.SessionCommandEvent{
		CommandID: commandID,
		Name:      name,
		Args:      args,
		Kind:      d.Kind,
		Text:      text,
		InputLine: inputLine,
	}}}
	out = append(out, c.planViewEvents()...)
	return out, nil
}

// applyPlanMode 收编 plan/mode {active}（整值替换，last wins）。
func (c *sessionCodec) applyPlanMode(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Active bool `json:"active"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	c.planFold.onPlanMode(d.Active)
	return c.planViewEvents(), nil
}

// planViewEvents 在 {active, pending} 视图变化时发一条计划模式快照事件
// （官方客户端从投影派生，无独立事件；我们的 reducer 存快照，故只在变化
// 时发，避免每帧 patch churn）。静默初值 {false,false} 不上报：芯片公式
// target = pending ? !active : active 在该值下恒 false，等价于投影为空
// （nil = 无 plan 态），避免每条命令都拖一份 planMode patch。
func (c *sessionCodec) planViewEvents() []core.Event {
	active, pending := c.planFold.view()
	if !active && !pending && !c.planViewKnown {
		return nil
	}
	if c.planViewKnown && c.planViewActive == active && c.planViewPending == pending {
		return nil
	}
	c.planViewKnown = true
	c.planViewActive = active
	c.planViewPending = pending
	return []core.Event{{Type: core.EventSessionPlanMode, PlanMode: &core.PlanModeEvent{
		Active:  active,
		Pending: pending,
	}}}
}

// applyGoalChange 收编 goal/change（官方全量快照替换：GoalSnapshotChangeMeta
// 或 GoalClearChangeMeta 墓碑；domain.ts）。事件不带 turn/step 框架（会话级
// 投影，2026-09-05 活体座位样本证实）。phase 词表严格校验，未知值 fail
// visibly（reset），不从快照猜状态。
func (c *sessionCodec) applyGoalChange(env *sessionEventWire) ([]core.Event, error) {
	var d struct {
		Operation string `json:"operation"`
		Goal      *struct {
			ID            string                  `json:"id"`
			Revision      int64                   `json:"revision"`
			Objective     string                  `json:"objective"`
			Phase         string                  `json:"phase"`
			BlockedReason *core.GoalBlockedReason `json:"blockedReason"`
			MaxGoalRounds int                     `json:"maxGoalRounds"`
		} `json:"goal,omitempty"`
		Cleared *struct {
			ID       string `json:"id"`
			Revision int64  `json:"revision"`
		} `json:"cleared,omitempty"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	switch d.Operation {
	case "create", "edit", "pause", "resume", "complete", "block":
		if d.Goal == nil {
			return nil, resetf("goal/change (seq %d) operation %q missing goal snapshot", env.Seq, d.Operation)
		}
		switch d.Goal.Phase {
		case "active", "paused", "blocked", "complete":
		default:
			return nil, resetf("goal/change (seq %d) unknown phase %q", env.Seq, d.Goal.Phase)
		}
		c.goalView = &core.GoalEvent{
			ID:            d.Goal.ID,
			Revision:      d.Goal.Revision,
			Objective:     d.Goal.Objective,
			Phase:         d.Goal.Phase,
			BlockedReason: d.Goal.BlockedReason,
			MaxGoalRounds: d.Goal.MaxGoalRounds,
		}
	case "clear":
		// 墓碑：当前目标清除，投影回到无目标态。
		c.goalView = nil
	default:
		return nil, resetf("goal/change (seq %d) unknown operation %q", env.Seq, d.Operation)
	}
	return c.goalViewEvents(), nil
}

// goalViewEvents 在目标快照变化时发一条整值快照事件（官方 goal 投影 whole
// snapshot 语义）。静默规则镜像 planViewEvents：未见首个快照且当前无目标
// （!known && nil）不上报——等价于投影为空，避免每帧 patch churn；清除
// （known && 变为 nil）必须上报，否则远端横条残留。
func (c *sessionCodec) goalViewEvents() []core.Event {
	if !c.goalViewKnown && c.goalView == nil {
		return nil
	}
	if c.goalViewKnown && goalViewEqual(c.goalView, c.lastGoalView) {
		return nil
	}
	c.goalViewKnown = true
	c.lastGoalView = cloneGoalView(c.goalView)
	return []core.Event{{Type: core.EventSessionGoal, Goal: cloneGoalView(c.goalView)}}
}

// goalViewEqual 比较两个 goal 快照（nil 与 nil 相等）。
func goalViewEqual(a, b *core.GoalEvent) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if *a != *b {
		return false
	}
	if (a.BlockedReason == nil) != (b.BlockedReason == nil) {
		return false
	}
	if a.BlockedReason != nil && *a.BlockedReason != *b.BlockedReason {
		return false
	}
	return true
}

// cloneGoalView 深拷贝一份 goal 快照（BlockedReason 指针字段独立）。
func cloneGoalView(g *core.GoalEvent) *core.GoalEvent {
	if g == nil {
		return nil
	}
	out := *g
	if g.BlockedReason != nil {
		br := *g.BlockedReason
		out.BlockedReason = &br
	}
	return &out
}

// applyWorkflowEvent 收编 tool-workflow/* 四事件（官方 invariant 镜像校验；
// workflow_fold.go 单一折叠真值）。事件不带 turn/step 框架（log-only journal
// 记录，2026-09-06 真机样本证实），归属由 codec 当前 active turn 决定：
// run-start 紧跟 workflow 工具 tool/call 之后、恒在开放 turn 内。
// 无 active turn（mux 附着落在 run 进行中的窗口）→ 折叠继续、事件不发射
// （fail-open；后续事件首次可归属时整卡补齐——官方「无 start 不渲染节点」
// 对位）。折叠违规 → resetf（validate-then-map，官方 append 时已保证不变量，
// 违规 = 我们与官方形状脱节，必须 fail visibly）。
func (c *sessionCodec) applyWorkflowEvent(env *sessionEventWire) ([]core.Event, error) {
	if err := foldWorkflowJournalEvent(&c.workflowFold, env.Type, env.Data); err != nil {
		return nil, resetf("%s (seq %d): %v", env.Type, env.Seq, err)
	}
	runID := workflowEventRunID(env.Type, env.Data)
	snapshot, ok := c.workflowFold.snapshot(runID)
	if !ok {
		return nil, resetf("%s (seq %d) folded to no snapshot for run %q", env.Type, env.Seq, runID)
	}
	if c.activeTurn == noTurn || c.activeTurnID == "" {
		return nil, nil
	}
	return []core.Event{{
		Type:        core.EventWorkflowRun,
		TurnID:      c.activeTurnID,
		WorkflowRun: &snapshot,
	}}, nil
}

func (c *sessionCodec) applyWorkflowRunStart(env *sessionEventWire) ([]core.Event, error) {
	return c.applyWorkflowEvent(env)
}

func (c *sessionCodec) applyWorkflowAgentStart(env *sessionEventWire) ([]core.Event, error) {
	return c.applyWorkflowEvent(env)
}

func (c *sessionCodec) applyWorkflowAgentEnd(env *sessionEventWire) ([]core.Event, error) {
	return c.applyWorkflowEvent(env)
}

func (c *sessionCodec) applyWorkflowRunEnd(env *sessionEventWire) ([]core.Event, error) {
	return c.applyWorkflowEvent(env)
}

// workflowEventRunID 从 tool-workflow 事件 data 提取 runId（四类载荷共有首键；
// 供折叠后定位快照）。
func workflowEventRunID(eventType string, data []byte) string {
	var d struct {
		RunID string `json:"runId"`
	}
	if err := jsonUnmarshal(data, &d); err != nil {
		return ""
	}
	return d.RunID
}

// feedWithReset runs one envelope through the codec, replacing it on reset
// (non-fatal on this carrier) and dropping the offending frame.
func feedWithReset(codecs map[string]*sessionCodec, sessionID string, env *sessionEventWire, deliver func([]core.Event)) {
	c := codecs[sessionID]
	if c == nil {
		c = newSessionCodec(sessionID)
		codecs[sessionID] = c
	}
	events, err := c.apply(env)
	if err != nil {
		if reset, ok := err.(*codecReset); ok {
			slog.Warn("dsh-web: session codec reset", "sessionPrefix", shortLog(sessionID), "reason", reset.reason)
			codecs[sessionID] = newSessionCodec(sessionID)
			return
		}
		// Non-reset errors cannot escape apply's contract; treat defensively.
		slog.Error("dsh-web: session codec unexpected error", "sessionPrefix", shortLog(sessionID), "error", err)
		codecs[sessionID] = newSessionCodec(sessionID)
		return
	}
	if len(events) > 0 {
		deliver(events)
	}
}

func shortLog(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}
