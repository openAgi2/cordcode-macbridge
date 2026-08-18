package opencodeweb

import (
	"context"
	"encoding/json"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// agents.go serves list_agents from GET /agent (1.18 bare array; v2 under
// /api). An empty array is legal (design §4.3.5) — only transport/HTTP
// failures error.

// ListAgents implements core.AgentLister.
func (a *Agent) ListAgents(ctx context.Context) ([]core.AgentDescriptor, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := c.fetchJSON(ctx, c.apiPath("/agent"), a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	items, err := decodeListPayload(raw)
	if err != nil {
		return nil, err
	}
	result := make([]core.AgentDescriptor, 0, len(items))
	for _, item := range items {
		var agent map[string]any
		if err := json.Unmarshal(item, &agent); err != nil {
			continue
		}
		name, _ := agent["name"].(string)
		if name == "" {
			name, _ = agent["id"].(string)
		}
		mode, _ := agent["mode"].(string)
		if mode == "" {
			mode = "primary"
		}
		description, _ := agent["description"].(string)
		hidden, _ := agent["hidden"].(bool)
		native, _ := agent["native"].(bool)
		result = append(result, core.AgentDescriptor{
			Name:        name,
			Mode:        mode,
			Hidden:      hidden,
			Native:      native,
			Description: description,
		})
	}
	return result, nil
}

var _ core.AgentLister = (*Agent)(nil)
