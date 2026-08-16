package dshweb

// list_projects mapping (design §4.3.7): workspace.list → quick-pick
// directory suggestions. An empty registry returns empty (iOS's local
// directory service is the fallback there — existing behavior, zero new
// client logic). list_directory needs no backend code: the bridge serves the
// iOS directory picker from the local filesystem generically (verified
// handleListDirectory has no backend branch), and the official
// host.listDirectory would browse the very same Mac filesystem through an
// extra dependency for no functional delta.

import (
	"context"
	"path/filepath"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// ListProjectSuggestions implements core.ProjectLister.
func (a *Agent) ListProjectSuggestions(ctx context.Context) ([]core.ProjectSuggestion, error) {
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var val workspaceListValue
	if err := client.Call(ctx, "workspace.list", map[string]any{}, &val); err != nil {
		return nil, err
	}
	out := make([]core.ProjectSuggestion, 0, len(val.Items))
	for _, w := range val.Items {
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
