package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// agents.go serves list_agents from GET /agent. The verified 1.18.18 shape is
// a bare array of agent objects (live-pinned; each row carries at least
// name/description/mode/native, and MAY carry an optional `model`
// "providerID/modelID" when the agent definition pins one). C5 strictness: a
// malformed row fails the whole list — a silently trimmed agent list would
// make the default-agent resolution lie. An empty array is legal.

// ocwAgentEntry is one strict /agent row.
type ocwAgentEntry struct {
	Name        string `json:"name"`
	Mode        string `json:"mode"`
	Description string `json:"description"`
	Hidden      bool   `json:"hidden"`
	Native      bool   `json:"native"`
	Model       string `json:"model"` // optional "providerID/modelID"
	DisplayName string `json:"displayName"`
}

func (e ocwAgentEntry) descriptor() core.AgentDescriptor {
	mode := e.Mode
	if mode == "" {
		mode = "primary"
	}
	d := core.AgentDescriptor{
		Name:        e.Name,
		Mode:        mode,
		Hidden:      e.Hidden,
		Native:      e.Native,
		Description: e.Description,
	}
	if e.DisplayName != "" {
		d.DisplayName = e.DisplayName
	}
	return d
}

// decodeAgentRegistry strictly parses the verified bare-array /agent shape.
func decodeAgentRegistry(raw []byte) ([]ocwAgentEntry, error) {
	trimmed := trimSpaceBytes(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("opencode-web: agent registry must be a bare array (generation-118 verified shape), got: %s", truncateForError(string(raw)))
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("opencode-web: agent registry array malformed: %w", err)
	}
	out := make([]ocwAgentEntry, 0, len(rows))
	for i, row := range rows {
		rowBytes := trimSpaceBytes(row)
		if len(rowBytes) == 0 || rowBytes[0] != '{' {
			return nil, fmt.Errorf("opencode-web: agent registry row %d must be an object, got: %s", i, truncateForError(string(row)))
		}
		var entry ocwAgentEntry
		if err := json.Unmarshal(row, &entry); err != nil {
			return nil, fmt.Errorf("opencode-web: agent registry row %d malformed: %w", i, err)
		}
		if entry.Name == "" {
			return nil, fmt.Errorf("opencode-web: agent registry row %d missing required name", i)
		}
		out = append(out, entry)
	}
	return out, nil
}

// fetchAgents returns the strict /agent registry (no cache: the list is small
// and the send path needs current truth for agent/model resolution).
func (a *Agent) fetchAgents(ctx context.Context, c *Client) ([]ocwAgentEntry, error) {
	raw, err := c.fetchJSON(ctx, c.apiPath("/agent"), a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	return decodeAgentRegistry(raw)
}

// ListAgents implements core.AgentLister.
func (a *Agent) ListAgents(ctx context.Context) ([]core.AgentDescriptor, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := a.fetchAgents(ctx, c)
	if err != nil {
		return nil, err
	}
	result := make([]core.AgentDescriptor, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.descriptor())
	}
	return result, nil
}

var _ core.AgentLister = (*Agent)(nil)

// resolvePromptAgent returns the agent id and its optional configured model
// for the prompt. An explicit requested agent must exist in the live /agent
// registry — an unavailable agent is a zero-POST error (canonical §6.6), not
// a silent fallback to another agent. When no agent was requested the
// default is derived from live truth: the first non-hidden primary agent
// (the official composer's initial agent — `build` on every observed
// 1.18.18 serve). A registry without any non-hidden primary agent is a
// zero-POST error.
func (a *Agent) resolvePromptAgent(ctx context.Context, c *Client, requested string) (agentID string, agentModel string, err error) {
	entries, err := a.fetchAgents(ctx, c)
	if err != nil {
		return "", "", fmt.Errorf("opencode-web: agent registry unavailable: %w", err)
	}
	if requested != "" {
		for _, entry := range entries {
			if entry.Name == requested {
				return entry.Name, entry.Model, nil
			}
		}
		return "", "", fmt.Errorf("opencode-web: agent %q is not in the server's agent registry; refresh agents and pick one from list_agents", requested)
	}
	for _, entry := range entries {
		if entry.Hidden {
			continue
		}
		if entry.Mode == "" || entry.Mode == "primary" {
			return entry.Name, entry.Model, nil
		}
	}
	return "", "", fmt.Errorf("opencode-web: the server's agent registry has no non-hidden primary agent — zero prompt POSTs")
}
