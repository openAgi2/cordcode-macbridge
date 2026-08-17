package dshweb

// Official agentPreset roster and create/select. List is the picker source;
// create carries the id; select is only legal on a still-blank session.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

type apiAgentPresetEntry struct {
	ID          string `json:"id"`
	Trust       string `json:"trust"`
	IsDefault   bool   `json:"isDefault"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Broken      string `json:"broken,omitempty"`
}

type agentPresetListValue struct {
	Presets []apiAgentPresetEntry `json:"presets"`
}

type agentPresetSelectRequest struct {
	SessionID   string `json:"sessionId"`
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
	if err := client.Call(ctx, "agentPreset.select", agentPresetSelectRequest{
		SessionID:   sessionID,
		AgentPreset: id,
	}, &val); err != nil {
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
	if err := client.Call(ctx, "agentPreset.list", struct{}{}, &val); err != nil {
		return nil, err
	}
	out := make([]core.AgentDescriptor, 0, len(val.Presets))
	for _, p := range val.Presets {
		if strings.TrimSpace(p.ID) == "" || p.Broken != "" {
			continue
		}
		display := strings.TrimSpace(p.Name)
		if display == "" {
			display = p.ID
		}
		out = append(out, core.AgentDescriptor{
			Name:        p.ID,
			DisplayName: display,
			Description: p.Description,
			IsDefault:   p.IsDefault,
			Mode:        "primary",
		})
	}
	return out, nil
}

var _ core.AgentLister = (*Agent)(nil)
var _ core.AgentPresetSelector = (*Agent)(nil)
