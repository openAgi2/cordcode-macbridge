package dshweb

// Rich-history mapping: session.history rows → core.RichHistoryEntry.
// The turn/user/tool accumulation is COPIED from agent/dsh history.go
// (design §4.1/M3 copy-not-import; §7 owner 兜底许可 covers read-side reuse)
// and adapted to the HTTP history source: pages arrive NEWEST-FIRST
// (beforeSeq walks backwards), so pages are collected until the entry budget
// is met, then reversed to oldest-first for the wire.
//
// Part/step shapes follow the grokbuild catalog convention iOS already
// renders (reasoning → tool steps → narrative text).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// getRichHistory maps session.history pages to rich entries (oldest first).
// limit<=0 means unlimited.
func (a *Agent) getRichHistory(ctx context.Context, client *Client, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	// Collect pages newest→older until the entry budget is met or exhausted.
	// budget is counted in MAPPED entries; over-fetch events per page because
	// many rows (chunks, control-plane) map to nothing.
	budget := limit
	if budget <= 0 {
		budget = 500
	}
	maxPerReq := budget * 4
	if maxPerReq > 2000 {
		maxPerReq = 2000
	}

	var pages [][]apiHistoryEntry
	collected := 0
	var before *int64
	for collected < budget {
		req := sessionHistoryRequest{SessionID: sessionID}
		if before != nil {
			seq := *before
			req.BeforeSeq = &seq
		}
		max := maxPerReq
		req.MaxMessages = &max
		var val sessionHistoryValue
		if err := client.Call(ctx, "session.history", req, &val); err != nil {
			return nil, err
		}
		if len(val.Events) == 0 {
			break
		}
		pages = append(pages, val.Events)
		collected += countMappableEntries(val.Events)
		if !val.HasMore {
			break
		}
		// Next page walks backwards from this page's first (oldest-in-page) seq.
		oldest := val.Events[0].Event.Seq
		if oldest <= 0 {
			break
		}
		n := oldest // beforeSeq is exclusive of that seq
		before = &n
	}

	// Flatten pages (newest page first) into one oldest-first slice.
	var total int
	for _, p := range pages {
		total += len(p)
	}
	flat := make([]apiHistoryEntry, 0, total)
	for i := len(pages) - 1; i >= 0; i-- {
		flat = append(flat, pages[i]...)
	}

	entries := mapHistoryEvents(sessionID, flat)
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// countMappableEntries estimates mapped-entry yield: turn boundaries plus
// user messages.
func countMappableEntries(evs []apiHistoryEntry) int {
	n := 0
	for _, e := range evs {
		switch e.Event.Type {
		case "turn/end", "user/message":
			n++
		}
	}
	if n == 0 {
		n = len(evs) / 8
	}
	return n
}

// mapHistoryEvents maps a oldest-first event slice onto rich entries — the
// copied agent/dsh accumulator flow: pass 1 tool outputs by callId, pass 2
// turn accumulation.
func mapHistoryEvents(sessionID string, evs []apiHistoryEntry) []core.RichHistoryEntry {
	// Pass 1: tool/result outputs by callId.
	outputs := map[string]string{}
	for _, e := range evs {
		if e.Event.Type != "tool/result" {
			continue
		}
		var d dshToolResultData
		if jsonUnmarshal(e.Event.Data, &d) != nil {
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

	var entries []core.RichHistoryEntry
	acc := &dshTurnAccumulator{sessionID: sessionID}
	flushTurn := func(endSeq int64, endTime int64) {
		if entry, ok := acc.flush(endSeq, endTime); ok {
			entries = append(entries, entry)
		}
	}
	for _, e := range evs {
		switch e.Event.Type {
		case "turn/start":
			acc.start(e.Event.Seq, e.Event.Time)
		case "turn/end":
			flushTurn(e.Event.Seq, e.Event.Time)
		case "user/message":
			var d dshUserMessageData
			if jsonUnmarshal(e.Event.Data, &d) != nil {
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
				ID:        fmt.Sprintf("%s:%d", sessionID, e.Event.Seq),
				Role:      "user",
				Content:   text,
				Timestamp: dshLogTime(e.Event.Time),
			})
		case "assistant/message":
			var d dshAssistantData
			if jsonUnmarshal(e.Event.Data, &d) != nil {
				continue
			}
			acc.addMessage(e.Event.Seq, e.Event.Time, d, outputs)
		}
	}
	flushTurn(0, 0) // torn tail: serve the committed prefix
	return entries
}

// jsonUnmarshal is a small alias to keep the copied logic tidy.
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// dshAssistantData is assistant/message's data payload.
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

// dshTurnAccumulator assembles one assistant turn entry in grokbuild's part
// order: reasoning flush → tool steps → narrative text.
type dshTurnAccumulator struct {
	sessionID string
	open      bool
	startSeq  int64
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

func (t *dshTurnAccumulator) start(seq int64, at int64) {
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

func (t *dshTurnAccumulator) addMessage(seq int64, at int64, d dshAssistantData, outputs map[string]string) {
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

func (t *dshTurnAccumulator) flush(endSeq int64, endTime int64) (core.RichHistoryEntry, bool) {
	if !t.open || (!t.hasData && t.content.Len() == 0 && t.thinking.Len() == 0 && len(t.parts) == 0) {
		t.reset()
		return core.RichHistoryEntry{}, false
	}
	entry := core.RichHistoryEntry{
		ID:         fmt.Sprintf("%s:%d", t.sessionID, t.startSeq),
		Role:       "assistant",
		Content:    t.content.String(),
		Thinking:   t.thinking.String(),
		Parts:      t.parts,
		Steps:      t.steps,
		Timestamp:  dshLogTime(t.startTime),
		ModelID:    t.model,
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
// (bash command / file path). DSH logs the arguments as a JSON-encoded
// *string* on tool-call blocks, so both object and string-wrapped-object
// forms are accepted.
func toolStepTitle(name string, arguments []byte) string {
	if len(arguments) == 0 {
		return ""
	}
	var args map[string]any
	if err := jsonUnmarshal(arguments, &args); err != nil {
		var wrapped string
		if err := jsonUnmarshal(arguments, &wrapped); err != nil {
			return ""
		}
		if err := jsonUnmarshal([]byte(wrapped), &args); err != nil {
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
