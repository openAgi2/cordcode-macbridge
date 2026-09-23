package dshweb

// Official agentPreset roster and select. List is the picker source; select
// is only legal on a still-blank session (agentPresets/* — the typert
// gateway's plural namespace; the select parameter is agentId, the
// session-backed Agent identity).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// apiAgentPresetEntry is one agentPresets/list row (id/order/isDefault —
// the gateway generation carries no name/description/trust/broken fields;
// display degrades to the id).
type apiAgentPresetEntry struct {
	ID        string `json:"id"`
	Order     int    `json:"order"`
	IsDefault bool   `json:"isDefault"`
}

type agentPresetListValue struct {
	Presets              []apiAgentPresetEntry `json:"presets"`
	ModeSelectionEnabled bool                  `json:"modeSelectionEnabled"`
}

type agentPresetSelectRequest struct {
	AgentID     string `json:"agentId"`
	AgentPreset string `json:"agentPreset"`
}

type agentPresetSelectValue struct {
	AgentPreset string `json:"agentPreset"`
}

func (a *Agent) SetPendingAgentPreset(id string) {
	if a == nil {
		return
	}
	a.pendingPreset = strings.TrimSpace(id)
}

func (a *Agent) SelectAgentPreset(ctx context.Context, sessionID, id string) error {
	id = strings.TrimSpace(id)
	if sessionID == "" || id == "" {
		return fmt.Errorf("dsh-web: agent preset select needs sessionId and id")
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var val agentPresetSelectValue
	req := agentPresetSelectRequest{AgentID: sessionID, AgentPreset: id}
	if err := client.Call(ctx, "agentPresets/select", map[string]any{"request": req}, &val); err != nil {
		return err
	}
	a.pendingPreset = id
	return nil
}

func (a *Agent) ListAgents(ctx context.Context) ([]core.AgentDescriptor, error) {
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var val agentPresetListValue
	if err := client.Call(ctx, "agentPresets/list", map[string]any{}, &val); err != nil {
		return nil, err
	}
	out := make([]core.AgentDescriptor, 0, len(val.Presets))
	for _, p := range val.Presets {
		if strings.TrimSpace(p.ID) == "" {
			continue
		}
		out = append(out, core.AgentDescriptor{
			Name:        p.ID,
			DisplayName: p.ID,
			IsDefault:   p.IsDefault,
			Mode:        "primary",
		})
	}
	return out, nil
}

var _ core.AgentLister = (*Agent)(nil)
var _ core.AgentPresetSelector = (*Agent)(nil)
