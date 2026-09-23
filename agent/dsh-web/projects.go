package dshweb

// list_projects mapping (design §4.3.7): the workspace/follow baseline →
// quick-pick directory suggestions. An empty registry returns empty (iOS's
// local directory service is the fallback there — existing behavior, zero
// new client logic). list_directory needs no backend code: the bridge
// serves the iOS directory picker from the local filesystem generically
// (verified handleListDirectory has no backend branch).

import (
	"context"
	"path/filepath"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ListProjectSuggestions implements core.ProjectLister. The workspace/follow
// baseline is the registry truth; before the first baseline arrives the
// suggestions are empty (the stream opens with the first subscription —
// callers re-list on refresh signals).
func (a *Agent) ListProjectSuggestions(ctx context.Context) ([]core.ProjectSuggestion, error) {
	if _, err := a.clientFor(ctx); err != nil {
		return nil, err
	}
	items, _, _, ready := a.ws.snapshot()
	if !ready {
		return []core.ProjectSuggestion{}, nil
	}
	out := make([]core.ProjectSuggestion, 0, len(items))
	for _, w := range items {
		if w.Path == "" {
			continue
		}
		name := w.Title
		if name == "" {
			name = filepath.Base(w.Path)
		}
		out = append(out, core.ProjectSuggestion{
			ID:        w.WorkspaceID,
			Directory: w.Path,
			Name:      name,
		})
	}
	return out, nil
}

var _ core.ProjectLister = (*Agent)(nil)
