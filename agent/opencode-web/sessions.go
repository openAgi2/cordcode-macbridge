package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ocwModelRef is the {id, providerID} model reference shape carried by
// sessions and sends (design §3.6).
type ocwModelRef struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
}

func (m *ocwModelRef) qualified() string {
	if m == nil || m.ID == "" {
		return ""
	}
	if m.ProviderID == "" {
		return m.ID
	}
	return m.ProviderID + "/" + m.ID
}

// ocwSessionEntry mirrors the GET /session element (design §3.6). The
// top-level tokens field is deliberately NOT typed here: usage reads the
// message-level info.tokens per the official web formula (§3.3) — the two
// shapes differ (S1: top level has no `total`) and must not be mixed.
type ocwSessionEntry struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Directory string       `json:"directory"`
	Time      *ocwTime     `json:"time"`
	Model     *ocwModelRef `json:"model"`
}

type ocwTime struct {
	Created float64 `json:"created"`
	Updated float64 `json:"updated"`
}

func (t *ocwTime) updatedAt() time.Time {
	if t == nil || t.Updated <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(t.Updated)).UTC()
}

// decodeListPayload accepts both list shapes: the 1.18 bare array and the v2
// {data:[…]} envelope (design §3.2). Unknown extra fields are ignored
// everywhere downstream (§4.3.1: 未核实前不过滤).
func decodeListPayload(raw []byte) ([]json.RawMessage, error) {
	trimmed := trimSpaceBytes(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	return envelope.Data, nil
}

// ListSessions implements core.Agent: GET /session (v2: /api/session + {data}
// envelope). The x-opencode-directory header carries the current work dir
// (switched per RPC by the go-bridge dispatch特判, design §2.1 坑 5 / §4.1.5
// M2-1) — with no dir known, no pretending of a complete global view.
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	return a.listSessionsWith(ctx, c)
}

func (a *Agent) listSessionsWith(ctx context.Context, c *Client) ([]core.AgentSessionInfo, error) {
	raw, err := c.fetchJSON(ctx, c.apiPath("/session"), a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	items, err := decodeListPayload(raw)
	if err != nil {
		return nil, err
	}
	out := make([]core.AgentSessionInfo, 0, len(items))
	for _, item := range items {
		var entry ocwSessionEntry
		if err := json.Unmarshal(item, &entry); err != nil {
			// Unknown/malformed fields must not drop the whole row.
			continue
		}
		if entry.ID == "" {
			continue
		}
		info := core.AgentSessionInfo{
			ID:         entry.ID,
			Summary:    entry.Title,
			Directory:  entry.Directory,
			ModifiedAt: entry.Time.updatedAt(),
		}
		if entry.Model != nil {
			info.ModelID = entry.Model.ID
			info.ProviderID = entry.Model.ProviderID
		}
		out = append(out, info)
	}
	// Stable order for cursor pagination: newest first, ID as tiebreak.
	sort.SliceStable(out, func(i, j int) bool {
		mi, mj := out[i].ModifiedAt, out[j].ModifiedAt
		if !mi.Equal(mj) {
			return mi.After(mj)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// fetchSessionInfo fetches one session via GET /session/:id. The directory
// header uses the current work dir (the serve does not hard-scope single
// session GETs by it); the response carries the session's own directory for
// subsequent reads.
func (a *Agent) fetchSessionInfo(ctx context.Context, c *Client, sessionID string) (*ocwSessionEntry, error) {
	raw, err := c.fetchJSON(ctx, c.apiPath("/session/")+sessionID, a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	var entry ocwSessionEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, err
	}
	if entry.ID == "" {
		return nil, fmt.Errorf("opencode-web: session payload missing id")
	}
	return &entry, nil
}
