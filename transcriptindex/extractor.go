package transcriptindex

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// SpanExtractor builds LogicalMessageSpans from transcript Records. Its
// boundary decisions MUST mirror the corresponding content grouping builder so
// that a ContentReplayer over a page's byte union produces the same logical
// messages in the same order (positional matching in PageIndex.Replay).
type SpanExtractor interface {
	// Process consumes one record and updates span state.
	Process(rec Record)
	// Finish seals any in-progress span after the last record. totalBytes is the
	// final file size, used to finalize the parse offset.
	Finish(totalBytes int64)
	// Spans returns the completed spans in ascending file order.
	Spans() []LogicalMessageSpan
}

// newExtractor returns the backend-specific span extractor, or nil if the
// backend is unknown.
func newExtractor(backend Backend) SpanExtractor {
	switch backend {
	case BackendCodex:
		return &codexSpanExtractor{}
	case BackendClaude:
		return &claudeSpanExtractor{
			byAssistant: make(map[string]*LogicalMessageSpan),
			toolOwner:   make(map[string]string),
			pending:     make(map[string]int64),
		}
	default:
		return nil
	}
}

// ── Codex ────────────────────────────────────────────────────────────────────
//
// The Codex builder (agent/codex/rich_history.go) keeps a single "current"
// assistant entry: assistant text/reasoning/tool_use merge into it, tool
// results close a tool in it, and a new user prompt flushes it. The span
// extractor records the byte span of each such logical message plus an
// OpenToolUses count that the three result variants decrement.

type codexSpanExtractor struct {
	spans   []LogicalMessageSpan
	current *LogicalMessageSpan
	ordinal int64
}

// ensureCurrent starts or extends the open assistant entry.
func (x *codexSpanExtractor) ensureCurrent(rec Record) {
	if x.current == nil {
		x.current = &LogicalMessageSpan{
			Ordinal:     x.ordinal,
			ReplayStart: rec.Start,
			EndOffset:   rec.End,
		}
		x.ordinal++
		return
	}
	if rec.End > x.current.EndOffset {
		x.current.EndOffset = rec.End
	}
}

func (x *codexSpanExtractor) flush() {
	if x.current != nil {
		x.spans = append(x.spans, *x.current)
		x.current = nil
	}
}

// emitUser flushes the current assistant entry (if any) and emits one user
// span for the line. Called once per qualifying prompt block to mirror the
// builder, which calls addEntry (flush + append) per block.
func (x *codexSpanExtractor) emitUser(rec Record) {
	x.flush()
	x.spans = append(x.spans, LogicalMessageSpan{
		Ordinal:     x.ordinal,
		ReplayStart: rec.Start,
		EndOffset:   rec.End,
	})
	x.ordinal++
}

// closeTool applies a tool-result line to the current entry.
func (x *codexSpanExtractor) closeTool(rec Record) {
	if x.current == nil {
		return
	}
	if rec.End > x.current.EndOffset {
		x.current.EndOffset = rec.End
	}
	// Codex results always target a tool in the current turn. Bounded decrement
	// (never negative) so stray results do not under-count.
	if x.current.OpenToolUses > 0 {
		x.current.OpenToolUses--
	}
}

type codexResponseItem struct {
	Role    string `json:"role"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Text    string   `json:"text"`
	Summary []string `json:"summary"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (x *codexSpanExtractor) Process(rec Record) {
	var envelope struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(rec.Bytes, &envelope) != nil {
		return
	}

	if envelope.Type == "event_msg" {
		if codexEventIsPatchApplyEnd(envelope.Payload) {
			x.closeTool(rec)
		}
		return
	}
	if envelope.Type != "response_item" {
		return
	}

	var item codexResponseItem
	if json.Unmarshal(envelope.Payload, &item) != nil {
		return
	}

	switch {
	case item.Role == "user" && len(item.Content) > 0:
		any := false
		for _, c := range item.Content {
			if c.Type == "input_text" && c.Text != "" && codexIsUserPrompt(c.Text) {
				any = true
			}
		}
		if !any {
			return
		}
		for _, c := range item.Content {
			if c.Type == "input_text" && c.Text != "" && codexIsUserPrompt(c.Text) {
				x.emitUser(rec)
			}
		}

	case item.Role == "assistant" && (item.Type == "" || item.Type == "message"):
		// Mirror builder.addText: only open/extend the entry when there is a
		// non-empty output_text block. An assistant message line with no visible
		// output_text is ignored by the builder, so it must not open a span here
		// either (otherwise the span count diverges and page replay fails safe).
		hasOutput := false
		for _, c := range item.Content {
			if c.Type == "output_text" && c.Text != "" {
				hasOutput = true
				break
			}
		}
		if hasOutput {
			x.ensureCurrent(rec)
		}

	case item.Type == "reasoning":
		// Mirror builder.addReasoning: only open/extend when text/summary is
		// non-empty. Empty reasoning records are skipped by the builder.
		text := item.Text
		if text == "" && len(item.Summary) > 0 {
			text = strings.Join(item.Summary, "\n")
		}
		if text != "" {
			x.ensureCurrent(rec)
		}

	case item.Type == "function_call" && item.Name == "update_plan":
		// The builder ignores update_plan; do not open a tool or extend the
		// entry for it. The trailing EndOffset already covers it when a later
		// record extends the entry; otherwise it is consistently excluded.

	case item.Type == "function_call":
		x.ensureCurrent(rec)
		x.current.OpenToolUses++

	case item.Type == "command_execution":
		x.ensureCurrent(rec)
		x.current.OpenToolUses++

	case item.Type == "function_call_output":
		x.closeTool(rec)

	case item.Type == "custom_tool_call" && item.Name == "apply_patch":
		x.ensureCurrent(rec)
		x.current.OpenToolUses++

	case item.Type == "custom_tool_call_output":
		x.closeTool(rec)
	}
}

func (x *codexSpanExtractor) Finish(int64) { x.flush() }

func (x *codexSpanExtractor) Spans() []LogicalMessageSpan {
	out := make([]LogicalMessageSpan, len(x.spans))
	copy(out, x.spans)
	return out
}

// codexIsUserPrompt mirrors agent/codex.isUserPrompt so the span extractor
// makes the same user-message boundary decisions as the content builder.
func codexIsUserPrompt(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "<") {
		return false
	}
	if strings.HasPrefix(t, "# AGENTS.md") || strings.HasPrefix(t, "#AGENTS.md") {
		return false
	}
	return true
}

// codexEventIsPatchApplyEnd reports whether an event_msg payload is a Codex
// patch_apply_end, mirroring the builder's nested-payload handling.
func codexEventIsPatchApplyEnd(payload json.RawMessage) bool {
	var envelope map[string]any
	if json.Unmarshal(payload, &envelope) != nil {
		return false
	}
	p := envelope
	if nested, ok := envelope["payload"].(map[string]any); ok {
		p = nested
	}
	if s, ok := p["type"].(string); ok {
		return strings.TrimSpace(s) == "patch_apply_end"
	}
	return false
}

// ── Claude ───────────────────────────────────────────────────────────────────
//
// The Claude builder (agent/claudecode loadClaudeRichHistory) keys assistant
// messages by their message ID (streaming lines merge), maps each tool_use to
// its owning assistant message, and attributes a tool_result (carried by a
// later type:"user" line) to that owner — extending the owner's byte span past
// intervening messages. The span extractor records those possibly-overlapping
// spans and an OpenToolUses count per message.

type claudeSpanExtractor struct {
	spans       []*LogicalMessageSpan // pointers; mutated as tool results arrive
	byAssistant map[string]*LogicalMessageSpan
	toolOwner   map[string]string // tool_use id -> assistant message key
	pending     map[string]int64  // tool_use id -> result rec.End seen before owner
	ordinal     int64
	// skipNextResumeNoResponse mirrors LoadClaudeRichHistoryFromReader: after a
	// resume-meta user message ("Continue from where you left off."), the next
	// assistant ("No response requested.") is dropped. These records must NOT
	// produce spans here, otherwise the span count drifts from the replayer and
	// PageIndex.Replay reports "matched N of M" mismatches.
	skipNextResumeNoResponse bool
}

// assistantSpan returns the span for key, creating it on first sight. Empty
// message IDs get a per-line synthetic key so distinct streamed lines are not
// merged (mirrors the builder's assistant-line-N synthesis).
func (x *claudeSpanExtractor) assistantSpan(msgID string, rec Record) *LogicalMessageSpan {
	key := msgID
	if key == "" {
		key = "assistant@" + strconv.FormatInt(rec.Start, 10)
	}
	if s, ok := x.byAssistant[key]; ok {
		if rec.End > s.EndOffset {
			s.EndOffset = rec.End
		}
		return s
	}
	stableID := msgID
	s := &LogicalMessageSpan{
		Ordinal:     x.ordinal,
		StableID:    stableID,
		ReplayStart: rec.Start,
		EndOffset:   rec.End,
	}
	x.ordinal++
	x.byAssistant[key] = s
	x.spans = append(x.spans, s)
	return s
}

func (x *claudeSpanExtractor) Process(rec Record) {
	var env struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
		IsMeta  bool            `json:"isMeta"`
	}
	if json.Unmarshal(rec.Bytes, &env) != nil {
		return
	}
	if env.Type != "user" && env.Type != "assistant" {
		return
	}
	if len(env.Message) == 0 || string(env.Message) == "null" {
		return
	}
	// Content blocks live at message.content, not on the message object itself.
	var msg struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(env.Message, &msg) != nil {
		return
	}
	blocks := decodeClaudeContentBlocks(msg.Content)

	// Resume-meta skip logic — MUST mirror LoadClaudeRichHistoryFromReader so the
	// span count matches the replayer exactly. A resume ("Continue from where you
	// left off.") is injected as an isMeta user message, immediately followed by
	// a placeholder assistant ("No response requested."). Both are dropped from
	// history; without dropping them here, replay reports "matched N of M".
	if isClaudeResumeMetaUserRecord(env.Type, env.IsMeta, blocks) {
		x.skipNextResumeNoResponse = true
		return
	}
	if x.skipNextResumeNoResponse {
		x.skipNextResumeNoResponse = false
		if isClaudeResumeNoResponseAssistantRecord(env.Type, blocks) {
			return
		}
	}

	if env.Type == "assistant" {
		msgID := strings.TrimSpace(msg.ID)
		s := x.assistantSpan(msgID, rec)
		for _, b := range blocks {
			if b.Type != "tool_use" {
				continue
			}
			id := strings.TrimSpace(b.ID)
			s.OpenToolUses++
			if id == "" {
				continue
			}
			x.toolOwner[id] = spanKeyForAssistant(msgID, rec.Start)
			if end, ok := x.pending[id]; ok {
				// Result arrived before its tool_use in file order; close it now.
				delete(x.pending, id)
				if end > s.EndOffset {
					s.EndOffset = end
				}
				if s.OpenToolUses > 0 {
					s.OpenToolUses--
				}
			}
		}
		return
	}

	// user line
	hasVisible := false
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			id := strings.TrimSpace(b.ToolUseID)
			if id == "" {
				continue
			}
			if ownerKey, ok := x.toolOwner[id]; ok {
				if s := x.byAssistant[ownerKey]; s != nil {
					if rec.End > s.EndOffset {
						s.EndOffset = rec.End
					}
					if s.OpenToolUses > 0 {
						s.OpenToolUses--
					}
				}
			} else {
				// Owner not seen yet (defensive): stash to resolve on arrival.
				x.pending[id] = rec.End
			}
		case "text":
			if isClaudeSkillInstructionText(b.Text) {
				continue
			}
			if normalizeClaudeUserText(b.Text) == "" {
				continue
			}
			hasVisible = true
		}
	}
	if hasVisible {
		x.spans = append(x.spans, &LogicalMessageSpan{
			Ordinal:     x.ordinal,
			ReplayStart: rec.Start,
			EndOffset:   rec.End,
		})
		x.ordinal++
	}
}

// spanKeyForAssistant mirrors the key logic in assistantSpan.
func spanKeyForAssistant(msgID string, start int64) string {
	if msgID == "" {
		return "assistant@" + strconv.FormatInt(start, 10)
	}
	return msgID
}

func (x *claudeSpanExtractor) Finish(int64) {}

func (x *claudeSpanExtractor) Spans() []LogicalMessageSpan {
	out := make([]LogicalMessageSpan, len(x.spans))
	for i, s := range x.spans {
		out[i] = *s
	}
	return out
}

type claudeContentBlock struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	ToolUseID string `json:"tool_use_id"`
	Text      string `json:"text"`
}

// decodeClaudeContentBlocks mirrors decodeTranscriptContentBlocks for the block
// fields the span extractor needs (type, id, tool_use_id, text).
func decodeClaudeContentBlocks(raw json.RawMessage) []claudeContentBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return []claudeContentBlock{{Type: "text", Text: plain}}
	}
	var blocks []claudeContentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}

func isClaudeSkillInstructionText(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "Base directory for this skill:") &&
		strings.Contains(trimmed, "\n# ")
}

var (
	commandNameRe  = regexp.MustCompile(`(?s)<command-name>\s*(.*?)\s*</command-name>`)
	commandArgsRe  = regexp.MustCompile(`(?s)<command-args>\s*(.*?)\s*</command-args>`)
	localCommandRe = regexp.MustCompile(`(?s)<local-command-(?:stdout|stderr|caveat)>`)
)

const commandArgsPreviewLimit = 120

func normalizeClaudeUserText(text string) string {
	if localCommandRe.MatchString(text) {
		return ""
	}
	nameMatch := commandNameRe.FindStringSubmatch(text)
	if nameMatch == nil {
		return text
	}
	name := strings.TrimSpace(nameMatch[1])
	args := ""
	if argsMatch := commandArgsRe.FindStringSubmatch(text); argsMatch != nil {
		args = strings.TrimSpace(argsMatch[1])
	}
	if args == "" {
		return name
	}
	flat := strings.Join(strings.Fields(args), " ")
	if runes := []rune(flat); len(runes) > commandArgsPreviewLimit {
		flat = string(runes[:commandArgsPreviewLimit]) + "…"
	}
	return name + " " + flat
}

// isClaudeResumeMetaUserRecord mirrors agent/claudecode.isClaudeResumeMetaUser:
// an isMeta user message whose text is exactly "Continue from where you left off."
// signals a Claude Code resume; the following placeholder assistant must be skipped.
func isClaudeResumeMetaUserRecord(msgType string, isMeta bool, blocks []claudeContentBlock) bool {
	if !isMeta || msgType != "user" {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) == "Continue from where you left off." {
			return true
		}
	}
	return false
}

// isClaudeResumeNoResponseAssistantRecord mirrors
// agent/claudecode.isClaudeResumeNoResponseAssistant: an assistant message whose
// text is exactly "No response requested." — the placeholder paired with a resume.
func isClaudeResumeNoResponseAssistantRecord(msgType string, blocks []claudeContentBlock) bool {
	if msgType != "assistant" {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) == "No response requested." {
			return true
		}
	}
	return false
}
