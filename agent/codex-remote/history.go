package codexremote

// history.go — bounded official thread/read(includeTurns) cold baseline.
//
// Proven on this controller stream (attempt-008): thread/list, thread/resume,
// turn/started, item/agentMessage/delta, item/completed, turn/completed.
// thread/read itself was still UNVERIFIED on that probe. This adapter
// fail-closes on missing thread identity or decode error. Item types without
// a proven sample on this stream are skipped, never guessed.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type historyThread struct {
	ID    string        `json:"id"`
	Turns []historyTurn `json:"turns"`
}

type historyTurn struct {
	ID          string            `json:"id"`
	Items       []json.RawMessage `json:"items"`
	ItemsView   string            `json:"itemsView"`
	Status      string            `json:"status"`
	StartedAt   *int64            `json:"startedAt"`
	CompletedAt *int64            `json:"completedAt"`
	Error       *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type historyItem struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Text    string `json:"text"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Summary []string `json:"summary"`
}

func (a *Agent) readThread(ctx context.Context, threadID string) (*historyThread, error) {
	a.mu.Lock()
	cl := a.client
	a.mu.Unlock()
	if cl == nil {
		return nil, ErrNotConfigured
	}
	raw, rpcErr, err := cl.RequestContext(ctx, "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": true,
	})
	if err != nil {
		return nil, err
	}
	if rpcErr != nil {
		return nil, rpcErr
	}
	var resp struct {
		Thread *historyThread `json:"thread"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("codex-remote: thread/read decode: %w", err)
	}
	if resp.Thread == nil || resp.Thread.ID == "" {
		return nil, fmt.Errorf("codex-remote: thread/read missing thread identity")
	}
	return resp.Thread, nil
}

func mapHistoryTurns(th *historyThread, limit int) []core.TurnScopedHistoryTurn {
	out := make([]core.TurnScopedHistoryTurn, 0, len(th.Turns))
	for _, t := range th.Turns {
		ht := core.TurnScopedHistoryTurn{TurnID: t.ID, Status: t.Status}
		if t.Error != nil {
			ht.ErrorMessage = t.Error.Message
		}
		if t.StartedAt != nil {
			ht.StartedAt = time.Unix(*t.StartedAt, 0).UTC()
			ht.HasTime = true
		}
		if t.CompletedAt != nil {
			ht.CompletedAt = time.Unix(*t.CompletedAt, 0).UTC()
		}
		if t.ItemsView == "notLoaded" {
			ht.SkippedTypes = append(ht.SkippedTypes, "itemsView:notLoaded")
		}
		for _, raw := range t.Items {
			var it historyItem
			if json.Unmarshal(raw, &it) != nil || it.Type == "" {
				continue
			}
			switch it.Type {
			case "userMessage":
				text := it.Text
				if text == "" {
					var parts []string
					for _, p := range it.Content {
						if p.Type == "text" && p.Text != "" {
							parts = append(parts, p.Text)
						}
					}
					text = strings.Join(parts, "\n")
				}
				if ht.UserItemID == "" {
					ht.UserItemID = it.ID
					ht.UserText = text
				} else if text != "" {
					ht.Parts = append(ht.Parts, map[string]any{"type": "text", "content": text, "itemId": it.ID})
				}
			case "agentMessage":
				if it.Text == "" {
					continue
				}
				ht.Parts = append(ht.Parts, map[string]any{"type": "text", "content": it.Text, "itemId": it.ID})
			case "reasoning":
				text := strings.Join(it.Summary, "\n")
				if strings.TrimSpace(text) == "" {
					text = it.Text
				}
				if strings.TrimSpace(text) == "" {
					continue
				}
				ht.Parts = append(ht.Parts, map[string]any{"type": "reasoning", "content": text, "itemId": it.ID})
			default:
				ht.SkippedTypes = append(ht.SkippedTypes, it.Type)
			}
		}
		out = append(out, ht)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (a *Agent) inProgressTurn(ctx context.Context, threadID string) string {
	th, err := a.readThread(ctx, threadID)
	if err != nil || th == nil {
		return ""
	}
	for i := len(th.Turns) - 1; i >= 0; i-- {
		if th.Turns[i].Status == "inProgress" && th.Turns[i].ID != "" {
			return th.Turns[i].ID
		}
	}
	return ""
}

func (a *Agent) GetTurnScopedRichHistory(ctx context.Context, sessionID string, limit int) ([]core.TurnScopedHistoryTurn, error) {
	th, err := a.readThread(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return mapHistoryTurns(th, limit), nil
}

func (a *Agent) GetRichSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.RichHistoryEntry, error) {
	turns, err := a.GetTurnScopedRichHistory(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.RichHistoryEntry, 0, len(turns)*2)
	for _, t := range turns {
		started := t.StartedAt
		if !t.HasTime {
			started = t.CompletedAt
		}
		if t.UserItemID != "" && t.UserText != "" {
			out = append(out, core.RichHistoryEntry{ID: t.UserItemID, Role: "user", Content: t.UserText, Timestamp: started})
		}
		if len(t.Parts) == 0 && t.ErrorMessage == "" {
			continue
		}
		entry := core.RichHistoryEntry{ID: t.TurnID, Role: "assistant", Parts: t.Parts, Timestamp: started}
		if t.ErrorMessage != "" {
			entry.Content = t.ErrorMessage
		}
		out = append(out, entry)
	}
	return out, nil
}

func (a *Agent) GetSessionHistory(ctx context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	rich, err := a.GetRichSessionHistory(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.HistoryEntry, 0, len(rich))
	for _, e := range rich {
		content := e.Content
		if content == "" {
			for _, part := range e.Parts {
				if part["type"] == "text" {
					if s, ok := part["content"].(string); ok {
						content += s
					}
				}
			}
		}
		out = append(out, core.HistoryEntry{Role: e.Role, Content: content, Timestamp: e.Timestamp})
	}
	return out, nil
}

var (
	_ core.TurnScopedRichHistoryProvider = (*Agent)(nil)
	_ core.RichHistoryProvider           = (*Agent)(nil)
	_ core.HistoryProvider               = (*Agent)(nil)
)
