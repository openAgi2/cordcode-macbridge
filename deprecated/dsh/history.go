// history.go maps a decoded DSH session log onto core.RichHistoryEntry
// (design §4.3). The part/step shapes follow the grokbuild catalog convention
// iOS already renders (reasoning/tool process group before the narrative
// text), so a reopened dead session looks like its live counterpart:
//
//   - human `user/message` → one user entry each (plugin/system injections skipped)
//   - `assistant/message` blocks accumulate into the open turn: reasoning →
//     pendingThinking (flushed before tools/text), tool-call blocks → step +
//     `{"type":"tool","step":…}` parts with outputs correlated from
//     `tool/result` records by callId, text blocks → content + text part
//   - `turn/start`/`turn/end` bound one assistant entry (timestamps from the
//     envelope); a torn tail flushes what was committed
//   - chunk/packed rows and control-plane records are skipped: the committed
//     `assistant/message` events already carry full content
//
// Entry IDs are `<sessionID>:<seq>` — the harness's own sequence number makes
// them deterministic across reads (iOS external-turn probing never sees new
// ids for the same log, mirroring the grokbuild stable-id rationale).
package dsh

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// dshAssistantData is assistant/message's data payload. The message source
// is the model-sourced variant of dshSource (provider/model ride along).
type dshAssistantData struct {
	Turn    int `json:"turn"`
	Step    int `json:"step"`
	Message struct {
		Role    string            `json:"role"`
		Content []dshContentBlock `json:"content"`
		Source  *dshModelSource   `json:"source,omitempty"`
	} `json:"message"`
}

// dshToolResultData is tool/result's data payload.
type dshToolResultData struct {
	Turn    int `json:"turn"`
	Step    int `json:"step"`
	Message struct {
		Source  *dshSource        `json:"source,omitempty"`
		Content []dshContentBlock `json:"content"`
	} `json:"message"`
}

// dshModelSource extends the shared source discriminant with the model
// attribution assistant messages carry.
type dshModelSource struct {
	dshSource
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// readRichHistory decodes one session log and maps it to rich history
// entries (oldest first). limit<=0 means unlimited; a positive limit keeps
// the trailing N entries.
func readRichHistory(sess storeSession, limit int) ([]core.RichHistoryEntry, error) {
	closer, err := openSessionLog(sess.Path, sess.Plain)
	if err != nil {
		return nil, fmt.Errorf("dsh history: open %s: %w", sess.Path, err)
	}
	defer closer.Close()

	// Pass 1: tool/result outputs by callId.
	dec := json.NewDecoder(closer)
	outputs := map[string]string{}
	for {
		var rec dshLogRecord
		if err := dec.Decode(&rec); err != nil {
			break // torn tail: history serves the committed prefix
		}
		if rec.Type != "tool/result" {
			continue
		}
		var d dshToolResultData
		if json.Unmarshal(rec.Data, &d) != nil {
			continue
		}
		callID := ""
		if d.Message.Source != nil {
			callID = strings.TrimSpace(d.Message.Source.CallID)
		}
		if callID == "" {
			continue
		}
		var sb strings.Builder
		for _, block := range d.Message.Content {
			if block.Type != "tool-result" {
				continue
			}
			for _, piece := range block.Content {
				if piece.Type == "text" && piece.Text != "" {
					if sb.Len() > 0 {
						sb.WriteByte('\n')
					}
					sb.WriteString(piece.Text)
				}
			}
		}
		outputs[callID] = sb.String()
	}

	// Pass 2 needs a second decode stream over the same file.
	closer.Close()
	closer, err = openSessionLog(sess.Path, sess.Plain)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	dec = json.NewDecoder(closer)

	var entries []core.RichHistoryEntry
	acc := &dshTurnAccumulator{sessionID: sess.ID}
	flushTurn := func(endSeq int, endTime int64) {
		if entry, ok := acc.flush(endSeq, endTime); ok {
			entries = append(entries, entry)
		}
	}
	for {
		var rec dshLogRecord
		if err := dec.Decode(&rec); err != nil {
			break
		}
		switch rec.Type {
		case "turn/start":
			acc.start(rec.Seq, rec.Time)
		case "turn/end":
			flushTurn(rec.Seq, rec.Time)
		case "user/message":
			var d dshUserMessageData
			if json.Unmarshal(rec.Data, &d) != nil {
				continue
			}
			if d.Source == nil || d.Source.Kind != "user" {
				continue // plugin/system injections are not conversation turns
			}
			text := joinTextBlocks(d.Content)
			if strings.TrimSpace(text) == "" {
				continue
			}
			entries = append(entries, core.RichHistoryEntry{
				ID:        fmt.Sprintf("%s:%d", sess.ID, rec.Seq),
				Role:      "user",
				Content:   text,
				Timestamp: dshLogTime(rec.Time),
			})
		case "assistant/message":
			var d dshAssistantData
			if json.Unmarshal(rec.Data, &d) != nil {
				continue
			}
			acc.addMessage(rec.Seq, rec.Time, d, outputs)
		}
	}
	flushTurn(0, 0) // torn tail: serve the committed prefix
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// dshTurnAccumulator assembles one assistant turn entry in grokbuild's part
// order: reasoning flush → tool steps → narrative text.
type dshTurnAccumulator struct {
	sessionID string
	open      bool
	startSeq  int
	startTime int64
	thinking  strings.Builder
	pending   strings.Builder
	content   strings.Builder
	parts     []map[string]any
	steps     []map[string]any
	model     string
	provider  string
	hasData   bool
}

func (t *dshTurnAccumulator) start(seq int, at int64) {
	t.open = true
	t.startSeq = seq
	t.startTime = at
}

func (t *dshTurnAccumulator) flushPendingReasoning() {
	if t.pending.Len() == 0 {
		return
	}
	t.parts = append(t.parts, map[string]any{"type": "reasoning", "content": t.pending.String()})
	t.pending.Reset()
}

func (t *dshTurnAccumulator) addMessage(seq int, at int64, d dshAssistantData, outputs map[string]string) {
	if !t.open {
		t.start(seq, at)
	}
	t.hasData = true
	if d.Message.Source != nil {
		if t.provider == "" {
			t.provider = d.Message.Source.Provider
		}
		if t.model == "" {
			t.model = d.Message.Source.Model
		}
	}
	for _, block := range d.Message.Content {
		switch block.Type {
		case "reasoning":
			if block.Text == "" {
				continue
			}
			if t.thinking.Len() > 0 {
				t.thinking.WriteByte('\n')
			}
			t.thinking.WriteString(block.Text)
			if t.pending.Len() > 0 {
				t.pending.WriteByte('\n')
			}
			t.pending.WriteString(block.Text)
		case "tool-call":
			t.flushPendingReasoning()
			name := strings.TrimSpace(block.Name)
			if name == "" {
				continue
			}
			output := outputs[strings.TrimSpace(block.ID)]
			stepID := fmt.Sprintf("%s:%d:%s", t.sessionID, seq, strings.TrimSpace(block.ID))
			step := map[string]any{
				"id":                             stepID,
				"toolName":                       name,
				"status":                         "unknown",
				"output":                         map[string]any{"kind": "inline", "text": output},
				"duration":                       nil,
				"requiresPermissionConfirmation": false,
				"availablePermissionOptions":     []any{},
			}
			if title := toolStepTitle(name, block.Arguments); title != "" {
				step["title"] = title
			}
			t.steps = append(t.steps, step)
			t.parts = append(t.parts, map[string]any{"type": "tool", "step": step})
		case "text":
			if block.Text == "" {
				continue
			}
			t.flushPendingReasoning()
			if t.content.Len() > 0 {
				t.content.WriteByte('\n')
			}
			t.content.WriteString(block.Text)
			t.parts = append(t.parts, map[string]any{"type": "text", "content": block.Text})
		}
	}
}

func (t *dshTurnAccumulator) flush(endSeq int, endTime int64) (core.RichHistoryEntry, bool) {
	if !t.open || (!t.hasData && t.content.Len() == 0 && t.thinking.Len() == 0 && len(t.parts) == 0) {
		t.reset()
		return core.RichHistoryEntry{}, false
	}
	entry := core.RichHistoryEntry{
		ID:        fmt.Sprintf("%s:%d", t.sessionID, t.startSeq),
		Role:      "assistant",
		Content:   t.content.String(),
		Thinking:  t.thinking.String(),
		Parts:     t.parts,
		Steps:     t.steps,
		Timestamp: dshLogTime(t.startTime),
		ModelID:   t.model,
		ProviderID: t.provider,
	}
	start := dshLogTime(t.startTime)
	entry.TurnStartedAt = &start
	if endTime > 0 {
		completed := dshLogTime(endTime)
		entry.TurnCompletedAt = &completed
	}
	t.reset()
	return entry, true
}

func (t *dshTurnAccumulator) reset() {
	*t = dshTurnAccumulator{sessionID: t.sessionID}
}

func joinTextBlocks(blocks []dshContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// toolStepTitle derives a short step title from common tool argument shapes
// (bash command / file path), mirroring the claudecode/grokbuild title hints.
// DSH logs the arguments as a JSON-encoded *string* on tool-call blocks, so
// both object and string-wrapped-object forms are accepted.
func toolStepTitle(name string, arguments json.RawMessage) string {
	if len(arguments) == 0 {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal(arguments, &args); err != nil {
		var wrapped string
		if err := json.Unmarshal(arguments, &wrapped); err != nil {
			return ""
		}
		if err := json.Unmarshal([]byte(wrapped), &args); err != nil {
			return ""
		}
	}
	for _, key := range []string{"command", "file_path", "path", "pattern", "query", "url"} {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			title := strings.TrimSpace(v)
			if len(title) > 80 {
				title = title[:80]
			}
			return title
		}
	}
	return ""
}

func dshLogTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
