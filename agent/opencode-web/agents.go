package opencodeweb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	// Directive-010: mode is guaranteed non-empty by the strict decoder — the
	// former missing-mode→primary default is deleted (audit-009 tail).
	d := core.AgentDescriptor{
		Name:        e.Name,
		Mode:        e.Mode,
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
// Directive-010 tail: every verified row must EXPLICITLY carry name,
// description, mode, and native with the correct type (the 1.18.18 built-ins
// all do) — missing or null fails the whole list; only same-version-evidenced
// optional fields (model/displayName/hidden) stay optional.
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
		var presence struct {
			Name        json.RawMessage `json:"name"`
			Description json.RawMessage `json:"description"`
			Mode        json.RawMessage `json:"mode"`
			Native      json.RawMessage `json:"native"`
			Hidden      bool            `json:"hidden"`
		}
		if err := json.Unmarshal(row, &presence); err != nil {
			return nil, fmt.Errorf("opencode-web: agent registry row %d malformed: %w", i, err)
		}
		type requiredField struct {
			key    string
			raw    json.RawMessage
			isBool bool
		}
		for _, field := range []requiredField{
			{"name", presence.Name, false},
			{"mode", presence.Mode, false},
			{"native", presence.Native, true},
		} {
			if len(field.raw) == 0 || string(trimSpaceBytes(field.raw)) == "null" {
				return nil, fmt.Errorf("opencode-web: agent registry row %d missing required %s (explicit presence + type are evidence-proven; no defaults)", i, field.key)
			}
			if field.isBool {
				var b bool
				if err := json.Unmarshal(field.raw, &b); err != nil {
					return nil, fmt.Errorf("opencode-web: agent registry row %d field %s must be a boolean: %w", i, field.key, err)
				}
				continue
			}
			var s string
			if err := json.Unmarshal(field.raw, &s); err != nil {
				return nil, fmt.Errorf("opencode-web: agent registry row %d field %s must be a string: %w", i, field.key, err)
			}
			if strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("opencode-web: agent registry row %d field %s must be non-empty", i, field.key)
			}
		}
		// description: the official schema is optional and the same-version
		// real serve exercises exactly one pattern — the hidden internal
		// agents (compaction/summary/title) omit it; every non-hidden row
		// carries it. Evidence-gated optionality: present must be a string
		// (null fails), absent is legal ONLY on a hidden row.
		if descRaw := trimSpaceBytes(presence.Description); len(descRaw) > 0 && string(descRaw) != "null" {
			var s string
			if err := json.Unmarshal(presence.Description, &s); err != nil {
				return nil, fmt.Errorf("opencode-web: agent registry row %d field description must be a string: %w", i, err)
			}
		} else if !presence.Hidden {
			return nil, fmt.Errorf("opencode-web: agent registry row %d missing required description (only hidden internal agents omit it on 1.18.18)", i)
		}
		var entry ocwAgentEntry
		if err := json.Unmarshal(row, &entry); err != nil {
			return nil, fmt.Errorf("opencode-web: agent registry row %d malformed: %w", i, err)
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
		// Mode is strict-decoded non-empty; the primary pick stays
		// evidence-driven (the official composer's initial agent).
		if entry.Hidden {
			continue
		}
		if entry.Mode == "primary" {
			return entry.Name, entry.Model, nil
		}
	}
	return "", "", fmt.Errorf("opencode-web: the server's agent registry has no non-hidden primary agent — zero prompt POSTs")
}
