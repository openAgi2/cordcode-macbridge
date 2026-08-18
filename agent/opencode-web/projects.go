package opencodeweb

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// projects.go serves list_projects from GET /project (design §4.3.7). The
// elements are {id, worktree, vcs, time, sandboxes} — the directory
// suggestion reads the worktree field (评审 S2 活体), never directory/path.
// The v2 /api/location route only parses ONE location and is NOT a project
// list; on the v2 generation this surface stays not_supported rather than
// fabricating suggestions.

type ocwProjectEntry struct {
	ID       string `json:"id"`
	Worktree string `json:"worktree"`
}

// ListProjectSuggestions implements core.ProjectLister.
func (a *Agent) ListProjectSuggestions(ctx context.Context) ([]core.ProjectSuggestion, error) {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	if c.Generation() == generationV2 {
		return nil, core.ErrNotSupported
	}
	raw, err := c.fetchJSON(ctx, "/project", a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	items, err := decodeListPayload(raw)
	if err != nil {
		return nil, err
	}
	out := make([]core.ProjectSuggestion, 0, len(items))
	for _, item := range items {
		var entry ocwProjectEntry
		if err := json.Unmarshal(item, &entry); err != nil {
			continue
		}
		if entry.Worktree == "" {
			continue
		}
		name := filepath.Base(entry.Worktree)
		out = append(out, core.ProjectSuggestion{
			ID:        entry.ID,
			Directory: entry.Worktree,
			Name:      name,
		})
	}
	return out, nil
}

var _ core.ProjectLister = (*Agent)(nil)
