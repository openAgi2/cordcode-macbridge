package codexremote

// codec.go — Phase 1 text-only live decode.
// turn/started is the only start truth; turn/completed is the only completion
// truth; item/agentMessage/delta is the only streaming text. Unknown methods
// are counted, never crash. Interrupt/reconnect are not advertised.

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type LiveCodec struct {
	mu           sync.Mutex
	turnByThread map[string]string
	unknown      map[string]int
}

func NewLiveCodec() *LiveCodec {
	return &LiveCodec{turnByThread: map[string]string{}, unknown: map[string]int{}}
}

func (c *LiveCodec) ActiveTurn(threadID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnByThread[threadID]
}

func (c *LiveCodec) UnknownMethods() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.unknown))
	for k, v := range c.unknown {
		out[k] = v
	}
	return out
}

func (c *LiveCodec) Decode(n Notification) []core.Event {
	switch n.Method {
	case "turn/started":
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if json.Unmarshal(n.Params, &p) != nil || p.ThreadID == "" || p.Turn.ID == "" {
			return nil
		}
		c.mu.Lock()
		c.turnByThread[p.ThreadID] = p.Turn.ID
		c.mu.Unlock()
		return []core.Event{{Type: core.EventTurnStarted, SessionID: p.ThreadID, ThreadID: p.ThreadID, TurnID: p.Turn.ID}}
	case "item/started":
		return decodeItemStarted(n)
	case "item/agentMessage/delta":
		var p struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if json.Unmarshal(n.Params, &p) != nil {
			return nil
		}
		return []core.Event{{
			Type: core.EventText, Content: p.Delta,
			SessionID: p.ThreadID, ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.ItemID,
		}}
	case "turn/completed":
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(n.Params, &p) != nil {
			return nil
		}
		c.mu.Lock()
		delete(c.turnByThread, p.ThreadID)
		c.mu.Unlock()
		ev := core.Event{
			Type: core.EventResult, Done: true,
			SessionID: p.ThreadID, ThreadID: p.ThreadID, TurnID: p.Turn.ID,
		}
		if p.Turn.Error != nil && p.Turn.Error.Message != "" {
			ev.Type = core.EventError
			ev.Content = p.Turn.Error.Message
		}
		return []core.Event{ev}
	default:
		c.mu.Lock()
		c.unknown[n.Method]++
		c.mu.Unlock()
		return nil
	}
}

type liveItemStarted struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Item     struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Text    string `json:"text"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"item"`
}

func decodeItemStarted(n Notification) []core.Event {
	var p liveItemStarted
	if json.Unmarshal(n.Params, &p) != nil {
		return nil
	}
	if p.Item.Type != "userMessage" || p.ThreadID == "" || p.TurnID == "" || p.Item.ID == "" {
		return nil
	}
	text := p.Item.Text
	if text == "" {
		var parts []string
		for _, c := range p.Item.Content {
			if c.Type == "text" && c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	return []core.Event{{
		Type:      core.EventUserMessage,
		SessionID: p.ThreadID,
		ThreadID:  p.ThreadID,
		TurnID:    p.TurnID,
		ItemID:    p.Item.ID,
		Content:   text,
	}}
}
