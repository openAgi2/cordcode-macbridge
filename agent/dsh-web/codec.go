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

	// chunk/assembled peer state (§3.7 唯一 owner + peer 校验).
	openBlocks    map[int]*blockAccum
	stepText      strings.Builder
	stepReasoning strings.Builder
	toolArgsAccum map[string]string
}

// blockAccum tracks one open assistant block (by chunk index).
type blockAccum struct {
	blockType string
	text      strings.Builder
	toolID    string
	toolArgs  strings.Builder
	hasDelta  bool
}

func newSessionCodec(sessionID string) *sessionCodec {
	prefix := sessionID
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return &sessionCodec{
		sessionPrefix: prefix,
		activeTurn:    noTurn,
		activeStep:    noStep,
		toolCallNames: map[string]string{},
		openBlocks:    map[int]*blockAccum{},
		toolArgsAccum: map[string]string{},
	}
}

func (c *sessionCodec) resetStepPeerState() {
	c.openBlocks = map[int]*blockAccum{}
	c.stepText.Reset()
	c.stepReasoning.Reset()
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
	c.activeTurnID = fmt.Sprintf("dshw-%s-t%d", c.sessionPrefix, turn)
	c.activeStep = step
	c.lastUsage = nil
	c.resetStepPeerState()
	c.toolArgsAccum = map[string]string{}
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
	case "request/header":
		return nil, nil
	case "request/context":
		return c.applyRequestContext(env)

	// Class ②: known control-plane events with no timeline effect.
	case "permission/preset", "sandbox/mode", "approval/policy", "agent/inbox/spliced", "session/title":
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
	c.toolArgsAccum = map[string]string{}
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
	default:
		return nil, resetf("user/message (seq %d) unknown source.kind %q", env.Seq, d.Source.Kind)
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
			c.openBlocks[d.Chunk.Index] = &blockAccum{blockType: d.Chunk.BlockType}
			return pre, nil
		default:
			return nil, resetf("assistant/chunk block-start (seq %d) unknown blockType %q", env.Seq, d.Chunk.BlockType)
		}

	case "text-delta":
		accum := c.openBlocks[d.Chunk.Index]
		if accum == nil || accum.blockType != "text" {
			return nil, resetf("assistant/chunk text-delta (seq %d) outside an open text block (index %d)", env.Seq, d.Chunk.Index)
		}
		accum.text.WriteString(d.Chunk.Text)
		return emit(core.Event{
			Type: core.EventText, Content: d.Chunk.Text,
			TurnID: c.activeTurnID, ItemID: c.activeTurnID,
		}), nil

	case "reasoning-delta":
		accum := c.openBlocks[d.Chunk.Index]
		if accum == nil || accum.blockType != "reasoning" {
			return nil, resetf("assistant/chunk reasoning-delta (seq %d) outside an open reasoning block (index %d)", env.Seq, d.Chunk.Index)
		}
		accum.text.WriteString(d.Chunk.Text)
		return emit(core.Event{
			Type: core.EventThinking, Content: d.Chunk.Text,
			TurnID: c.activeTurnID, ItemID: c.activeTurnID,
		}), nil

	case "tool-call-delta":
		accum := c.openBlocks[d.Chunk.Index]
		if accum == nil || accum.blockType != "tool-call" {
			return nil, resetf("assistant/chunk tool-call-delta (seq %d) outside an open tool-call block (index %d)", env.Seq, d.Chunk.Index)
		}
		if d.Chunk.ID != "" && d.Chunk.Name != "" {
			c.toolCallNames[d.Chunk.ID] = d.Chunk.Name
		}
		if d.Chunk.ID != "" {
			accum.toolID = d.Chunk.ID
			accum.hasDelta = true
			accum.toolArgs.WriteString(d.Chunk.ArgumentsDelta)
			c.toolArgsAccum[d.Chunk.ID] += d.Chunk.ArgumentsDelta
		}
		return pre, nil

	case "block-end":
		accum := c.openBlocks[d.Chunk.Index]
		if accum == nil {
			return nil, resetf("assistant/chunk block-end (seq %d) without open block (index %d)", env.Seq, d.Chunk.Index)
		}
		delete(c.openBlocks, d.Chunk.Index)
		if d.Chunk.Block == nil || d.Chunk.Block.Type != accum.blockType {
			return nil, resetf("assistant/chunk block-end (seq %d) block type mismatch (open %q)", env.Seq, accum.blockType)
		}
		switch accum.blockType {
		case "text":
			if d.Chunk.Block.Text != accum.text.String() {
				return nil, resetf("text block-end (seq %d) disagrees with accumulated deltas", env.Seq)
			}
			c.stepText.WriteString(d.Chunk.Block.Text)
		case "reasoning":
			if d.Chunk.Block.Text != accum.text.String() {
				return nil, resetf("reasoning block-end (seq %d) disagrees with accumulated deltas", env.Seq)
			}
			c.stepReasoning.WriteString(d.Chunk.Block.Text)
		case "tool-call":
			if d.Chunk.Block.ID != "" && d.Chunk.Block.Name != "" {
				c.toolCallNames[d.Chunk.Block.ID] = d.Chunk.Block.Name
			}
			if accum.hasDelta && d.Chunk.Block.ID != "" {
				if assembled, ok := c.toolArgsAccum[d.Chunk.Block.ID]; ok && assembled != d.Chunk.Block.Arguments {
					return nil, resetf("tool-call block-end (seq %d) arguments disagree with accumulated deltas", env.Seq)
				}
			}
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

// applyAssistantMessage — assembled message: validate only, never re-source
// (live text/reasoning owner is the chunk delta path).
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

	var text, reasoning strings.Builder
	for _, blk := range d.Message.Content {
		switch blk.Type {
		case "text":
			text.WriteString(blk.Text)
		case "reasoning":
			reasoning.WriteString(blk.Text)
		case "tool-call":
			if assembled, ok := c.toolArgsAccum[blk.ID]; ok && assembled != blk.Arguments {
				return nil, resetf("assistant/message (seq %d) tool-call arguments disagree with accumulated deltas", env.Seq)
			}
		}
	}
	if text.String() != c.stepText.String() {
		return nil, resetf("assistant/message (seq %d) text disagrees with chunk deltas", env.Seq)
	}
	if reasoning.String() != c.stepReasoning.String() {
		return nil, resetf("assistant/message (seq %d) reasoning disagrees with chunk deltas", env.Seq)
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
	if assembled, ok := c.toolArgsAccum[d.CallID]; ok && assembled != d.Arguments {
		return nil, resetf("tool/call (seq %d) arguments disagree with accumulated deltas", env.Seq)
	}
	c.toolCallNames[d.CallID] = d.Name
	return append(pre, core.Event{
		Type:      core.EventToolUse,
		ToolName:  d.Name,
		ToolInput: d.Arguments,
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
	return nil, nil
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
