package dsh

// Root-session codec: DSH session.event → core.Event mapping (design §3.3),
// active-turn state machine and identity rules (§3.6.1), source.kind routing
// (§3.6.2). The codec is owned by the read loop goroutine — no locking.
//
// Identity (§3.6.1 identity matrix):
//   - TurnID (turn-scoped core.Event)  = "p{nonce}-t{activeTurn}"
//   - ItemID (assistant text/reasoning) == TurnID (reducer requirement)
//   - ItemID (user, source.kind=user)    = data.id
//   - RequestID/ItemID (tool)            = callId
//   - control-plane / session / internal  events carry no TurnID
//
// Fail-visible: except user/message (the only turnless timeline shape, which
// derives from active), every turn-scoped frame is validated against the
// active turn/step BEFORE mapping; a mismatch is a protocol violation, never
// a silent re-attribution (§3.6.1 validate-then-map).

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// dshProtocolError marks a protocol/codec violation — the fatal class in the
// §3.6.3② binary: framing/envelope/invariant damage means the decoder is
// polluted and the process must not serve another turn.
type dshProtocolError struct {
	Reason string
}

func (e *dshProtocolError) Error() string {
	return "dsh protocol violation: " + e.Reason
}

func protocolViolationf(format string, args ...any) *dshProtocolError {
	return &dshProtocolError{Reason: fmt.Sprintf(format, args...)}
}

const (
	noTurn = -1
	noStep = -1
)

// dshCodec holds the root session's decode state: active turn/step, chunk
// assembly for tool-call deltas, and the last usage snapshot of the active
// turn (used to fill turn terminal token totals).
type dshCodec struct {
	nonce string

	activeTurn   int
	activeStep   int
	activeTurnID string

	// toolCallNames remembers callId → tool name so tool/result can carry
	// ToolName without a second lookup path.
	toolCallNames map[string]string

	// lastUsage is the most recent usage snapshot inside the active turn;
	// cleared on turn/end after being folded into the terminal event.
	lastUsage *dshUsage

	// contextWindow is captured from request/context (model capacity); used
	// with usage snapshots for context pressure (§3.7).
	contextWindow int
}

func newCodec(nonce string) *dshCodec {
	return &dshCodec{
		nonce:         nonce,
		activeTurn:    noTurn,
		activeStep:    noStep,
		toolCallNames: make(map[string]string),
	}
}

// turnIDFor returns the active-turn identity. Caller must have checked that a
// turn is active.
func (c *dshCodec) turnID() string { return c.activeTurnID }

// apply maps one root-scoped session.event envelope to core.Events. A non-nil
// error return is a protocol violation: the session must emit a visible
// terminal and tear the process down (§3.6.3②); it must NOT continue decoding
// on the polluted stream.
func (c *dshCodec) apply(env *dshEvent) ([]core.Event, error) {
	// The ignorable:true marker is the ONLY safe-skip channel, for known or
	// unknown types alike (§3.10.2 class ④). false / non-bool / absent all
	// mean REQUIRED.
	if env.ignorableMarker() {
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
		// Control-plane (request header config/system/tools). Captured shape
		// is not mapped to a core.Event in phase 1.
		return nil, nil
	case "request/context":
		return c.applyRequestContext(env)

	// Class ② (§3.10.2): known control-plane/diagnostic events proven by the
	// real dumps to have no timeline effect. Explicitly ignored, one by one —
	// never a catch-all.
	case "permission/preset", "sandbox/mode", "approval/policy", "agent/inbox/spliced", "session/title":
		return nil, nil

	default:
		// Class ③ / unknown: known-name-but-unimplemented or entirely unknown
		// REQUIRED event. "No sample" is not "safe to ignore" — fail visibly.
		return nil, protocolViolationf("unimplemented required event type %q (seq %d)", env.Type, env.Seq)
	}
}

func decodeData(env *dshEvent, v any) error {
	if len(env.Data) == 0 {
		return protocolViolationf("event %q (seq %d) missing data payload", env.Type, env.Seq)
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		return protocolViolationf("event %q (seq %d) data schema violation: %v", env.Type, env.Seq, err)
	}
	return nil
}

// validateActiveTurnStep enforces §3.6.1 validate-then-map for turn-scoped
// frames: source turn/step must equal the active state.
func (c *dshCodec) validateActiveTurnStep(envType string, envSeq, turn, step int) error {
	if c.activeTurn == noTurn {
		return protocolViolationf("%s (seq %d) outside any active turn", envType, envSeq)
	}
	if turn != c.activeTurn {
		return protocolViolationf("%s (seq %d) turn %d does not match active turn %d", envType, envSeq, turn, c.activeTurn)
	}
	if step != c.activeStep {
		return protocolViolationf("%s (seq %d) step %d does not match active step %d", envType, envSeq, step, c.activeStep)
	}
	return nil
}

func (c *dshCodec) applyTurnStart(env *dshEvent) ([]core.Event, error) {
	var d struct {
		Turn int `json:"turn"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	if d.Turn < 1 {
		return nil, protocolViolationf("turn/start (seq %d) invalid turn %d", env.Seq, d.Turn)
	}
	if c.activeTurn != noTurn {
		return nil, protocolViolationf("nested turn/start (seq %d): turn %d still active", env.Seq, c.activeTurn)
	}
	c.activeTurn = d.Turn
	c.activeTurnID = fmt.Sprintf("p%s-t%d", c.nonce, d.Turn)
	c.activeStep = noStep
	c.lastUsage = nil
	return []core.Event{{Type: core.EventTurnStarted, TurnID: c.activeTurnID}}, nil
}

func (c *dshCodec) applyTurnEnd(env *dshEvent) ([]core.Event, error) {
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
		return nil, protocolViolationf("turn/end (seq %d) outside any active turn", env.Seq)
	}
	if d.Turn != c.activeTurn {
		return nil, protocolViolationf("turn/end (seq %d) turn %d does not match active turn %d", env.Seq, d.Turn, c.activeTurn)
	}
	if c.activeStep != noStep {
		return nil, protocolViolationf("turn/end (seq %d) with unclosed step %d", env.Seq, c.activeStep)
	}

	ev := core.Event{
		Type:   core.EventResult,
		Done:   true,
		TurnID: c.activeTurnID,
	}
	switch d.Reason.Kind {
	case "completed", "max-tokens":
		// Verified terminal outcomes (run1-run4). max-tokens is an accepted
		// token-cap completion, not an infrastructure error (run2).
	default:
		// error / aborted / interrupted / blocked — no frozen sample. The
		// source did close the turn, but never fabricate success: settle as
		// turn_error with the raw reason (visible failure, §16 honesty).
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
	return []core.Event{ev}, nil
}

func (c *dshCodec) applyStepStart(env *dshEvent) ([]core.Event, error) {
	var d struct {
		Turn int `json:"turn"`
		Step int `json:"step"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	if c.activeTurn == noTurn {
		return nil, protocolViolationf("step/start (seq %d) outside any active turn", env.Seq)
	}
	if d.Turn != c.activeTurn {
		return nil, protocolViolationf("step/start (seq %d) turn %d does not match active turn %d", env.Seq, d.Turn, c.activeTurn)
	}
	if d.Step < 1 {
		return nil, protocolViolationf("step/start (seq %d) invalid step %d", env.Seq, d.Step)
	}
	if c.activeStep != noStep {
		return nil, protocolViolationf("nested step/start (seq %d): step %d still open", env.Seq, c.activeStep)
	}
	c.activeStep = d.Step
	return nil, nil
}

func (c *dshCodec) applyStepEnd(env *dshEvent) ([]core.Event, error) {
	var d struct {
		Turn int `json:"turn"`
		Step int `json:"step"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	if err := c.validateActiveTurnStep("step/end", env.Seq, d.Turn, d.Step); err != nil {
		return nil, err
	}
	c.activeStep = noStep
	return nil, nil
}

// applyUserMessage implements §3.6.2 source.kind routing. user/message is the
// ONLY turnless shape that must reach the timeline; its turn identity is
// derived from the active turn (no source turn to validate — the sanctioned
// exception in §3.6.1).
func (c *dshCodec) applyUserMessage(env *dshEvent) ([]core.Event, error) {
	var d struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
		ID string `json:"id"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}

	switch d.Source.Kind {
	case "user":
		if c.activeTurn == noTurn {
			return nil, protocolViolationf("user/message (seq %d) with source.kind=user but no active turn", env.Seq)
		}
		var text strings.Builder
		for _, blk := range d.Content {
			if blk.Type == "text" {
				text.WriteString(blk.Text)
			}
		}
		return []core.Event{{
			Type:   core.EventUserMessage,
			Content: text.String(),
			TurnID: c.activeTurnID,
			ItemID: d.ID,
		}}, nil

	case "plugin":
		// Permission runtime context spliced into the log. Never a user
		// prompt: emitting would let the reducer's second same-turn upsert
		// overwrite the real prompt (run3/run4 frozen double-shape).
		return nil, nil

	default:
		// Unknown source — required unknown semantics: fail visibly, never
		// guess "user" (§3.3 table, §3.6.2).
		return nil, protocolViolationf("user/message (seq %d) unknown source.kind %q", env.Seq, d.Source.Kind)
	}
}

// dshChunk is the assistant/chunk payload. Field names follow the pinned
// schema: text-delta/reasoning-delta carry `text`; tool-call-delta carries
// `argumentsDelta` (NOT `arguments`) — design §3.3.
type dshChunk struct {
	Type     string `json:"type"`
	Index    int    `json:"index"`
	BlockType string `json:"blockType"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Text     string `json:"text"`
	ArgumentsDelta string `json:"argumentsDelta"`
	Usage    *dshUsage `json:"usage"`
	Reason   *struct {
		Kind string `json:"kind"`
	} `json:"reason"`
	Block *struct {
		Type      string     `json:"type"`
		Text      string     `json:"text"`
		ID        string     `json:"id"`
		Name      string     `json:"name"`
		Arguments string     `json:"arguments"`
	} `json:"block"`
}

func (c *dshCodec) applyAssistantChunk(env *dshEvent) ([]core.Event, error) {
	var d struct {
		Turn  int      `json:"turn"`
		Step  int      `json:"step"`
		Chunk dshChunk `json:"chunk"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	if err := c.validateActiveTurnStep("assistant/chunk", env.Seq, d.Turn, d.Step); err != nil {
		return nil, err
	}

	switch d.Chunk.Type {
	case "block-start":
		switch d.Chunk.BlockType {
		case "text", "reasoning", "tool-call":
			// Internal block boundary; phase 1 merges naturally (core.Event
			// has no newPart — §3.6.1).
			return nil, nil
		default:
			return nil, protocolViolationf("assistant/chunk block-start (seq %d) unknown blockType %q", env.Seq, d.Chunk.BlockType)
		}

	case "text-delta":
		// Live text owner is the chunk delta (§3.7). ItemID == TurnID.
		return []core.Event{{
			Type:    core.EventText,
			Content: d.Chunk.Text,
			TurnID:  c.activeTurnID,
			ItemID:  c.activeTurnID,
		}}, nil

	case "reasoning-delta":
		return []core.Event{{
			Type:    core.EventThinking,
			Content: d.Chunk.Text,
			TurnID:  c.activeTurnID,
			ItemID:  c.activeTurnID,
		}}, nil

	case "tool-call-delta":
		// Assemble-only: the tool start owner is tool/call (§3.7). Track the
		// name for result attribution.
		if d.Chunk.ID != "" && d.Chunk.Name != "" {
			c.toolCallNames[d.Chunk.ID] = d.Chunk.Name
		}
		return nil, nil

	case "block-end":
		if d.Chunk.Block != nil && d.Chunk.Block.Type == "tool-call" && d.Chunk.Block.ID != "" && d.Chunk.Block.Name != "" {
			c.toolCallNames[d.Chunk.Block.ID] = d.Chunk.Block.Name
		}
		return nil, nil

	case "usage":
		if d.Chunk.Usage == nil {
			return nil, protocolViolationf("assistant/chunk usage (seq %d) missing usage payload", env.Seq)
		}
		u := *d.Chunk.Usage
		c.lastUsage = &u
		return nil, nil

	case "finish":
		// Step-internal finish boundary (stop / tool-calls / max-tokens are
		// step outcomes; the turn outcome is turn/end only — §3.4).
		return nil, nil

	default:
		return nil, protocolViolationf("assistant/chunk (seq %d) unknown chunk type %q", env.Seq, d.Chunk.Type)
	}
}

// applyAssistantMessage — assembled message: validate only, never append
// (§3.7: live text/reasoning owner is the chunk delta path).
func (c *dshCodec) applyAssistantMessage(env *dshEvent) ([]core.Event, error) {
	var d struct {
		Turn int `json:"turn"`
		Step int `json:"step"`
	}
	if err := decodeData(env, &d); err != nil {
		return nil, err
	}
	if err := c.validateActiveTurnStep("assistant/message", env.Seq, d.Turn, d.Step); err != nil {
		return nil, err
	}
	return nil, nil
}

func (c *dshCodec) applyToolCall(env *dshEvent) ([]core.Event, error) {
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
	if err := c.validateActiveTurnStep("tool/call", env.Seq, d.Turn, d.Step); err != nil {
		return nil, err
	}
	if d.CallID == "" {
		return nil, protocolViolationf("tool/call (seq %d) missing callId", env.Seq)
	}
	c.toolCallNames[d.CallID] = d.Name
	return []core.Event{{
		Type:      core.EventToolUse,
		ToolName:  d.Name,
		ToolInput: d.Arguments,
		RequestID: d.CallID,
		ItemID:    d.CallID,
		TurnID:    c.activeTurnID,
	}}, nil
}

func (c *dshCodec) applyToolResult(env *dshEvent) ([]core.Event, error) {
	var d struct {
		Turn int `json:"turn"`
		Step int `json:"step"`
		Message struct {
			Source struct {
				Kind   string `json:"kind"`
				CallID string `json:"callId"`
			} `json:"source"`
			Content []struct {
				Type      string `json:"type"`
				ToolCallID string `json:"toolCallId"`
				Content   []struct {
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
	if err := c.validateActiveTurnStep("tool/result", env.Seq, d.Turn, d.Step); err != nil {
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
		return nil, protocolViolationf("tool/result (seq %d) missing tool-result content block", env.Seq)
	}
	if callID == "" {
		return nil, protocolViolationf("tool/result (seq %d) missing callId", env.Seq)
	}

	status := "completed"
	if isError {
		status = "failed"
	}
	success := !isError
	return []core.Event{{
		Type:        core.EventToolResult,
		ToolName:    c.toolCallNames[callID],
		ToolResult:  text.String(),
		ToolStatus:  status,
		ToolSuccess: &success,
		RequestID:   callID,
		ItemID:      callID,
		TurnID:      c.activeTurnID,
	}}, nil
}

func (c *dshCodec) applyTodoWrite(env *dshEvent) ([]core.Event, error) {
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
	// Control-plane shape: no TurnID (§3.6.1 identity scope).
	return []core.Event{{Type: core.EventPlan, Plan: todos}}, nil
}

func (c *dshCodec) applyRequestContext(env *dshEvent) ([]core.Event, error) {
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
