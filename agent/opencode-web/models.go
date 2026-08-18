package opencodeweb

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// models.go serves list_models / switch_model from the runtime provider
// catalog (GET /provider; v2 under /api). Model windows, names, and ids only
// ever come from the runtime — no hand-written allowlist, no on-disk cache
// (design §2.2 纪律 3).

// ocwModelCatalog is one runtime provider/model snapshot.
type ocwModelCatalog struct {
	// Models are qualified "providerID/modelID" ModelOptions in stable order.
	Models []core.ModelOption
	// windows is the shared window map (also used by the usage formula).
	windows map[string]int
}

// parseQualifiedModel splits "providerID/modelID" (or a bare model id).
func parseQualifiedModel(model string) (providerID, modelID string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", ""
	}
	if idx := strings.Index(model, "/"); idx > 0 {
		return model[:idx], model[idx+1:]
	}
	return "", model
}

type catalogRow struct {
	qualified string
	desc      string
}

// fetchModelCatalog walks GET /provider recursively collecting every
// model-shaped node (id + limit.context). Provider-qualified names keep the
// "providerID/modelID" form the wire layer parses (parseModelID).
func (a *Agent) fetchModelCatalog(ctx context.Context, c *Client) (*ocwModelCatalog, error) {
	raw, err := c.fetchJSON(ctx, c.apiPath("/provider"), a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var rows []catalogRow
	seen := map[string]bool{}
	windows := map[string]int{}
	collectCatalogModels(root, "", &rows, seen, windows)
	catalog := &ocwModelCatalog{windows: windows}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].qualified < rows[j].qualified })
	for _, row := range rows {
		catalog.Models = append(catalog.Models, core.ModelOption{Name: row.qualified, Desc: row.desc})
	}
	return catalog, nil
}

// collectCatalogModels records model-shaped nodes: any map with id plus a
// limit.context window. providerID scopes qualified names while descending
// through a provider's models.
func collectCatalogModels(node any, providerID string, rows *[]catalogRow, seen map[string]bool, windows map[string]int) {
	switch typed := node.(type) {
	case map[string]any:
		id, _ := typed["id"].(string)
		limit, _ := typed["limit"].(map[string]any)
		window := 0
		if limit != nil {
			window = anyInt(limit["context"])
		}
		if id != "" && window > 0 {
			qualified := id
			if providerID != "" {
				qualified = providerID + "/" + id
			}
			windows[id] = window
			if providerID != "" {
				windows[qualified] = window
			}
			if !seen[qualified] {
				seen[qualified] = true
				name, _ := typed["name"].(string)
				*rows = append(*rows, catalogRow{qualified: qualified, desc: name})
			}
		}
		scope := providerID
		if id != "" && typed["models"] != nil {
			scope = id
		}
		for key, child := range typed {
			if key == "limit" {
				continue
			}
			collectCatalogModels(child, scope, rows, seen, windows)
		}
	case []any:
		for _, child := range typed {
			collectCatalogModels(child, providerID, rows, seen, windows)
		}
	}
}

// SetModel implements core.ModelSwitcher. The official 1.18 API has no
// dedicated switch endpoint: the selection is recorded as pending and rides
// the next prompt's model field (design §4.3.5 — UI semantics: takes effect
// on next send; diagnostics note it).
func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	a.pendingModel = strings.TrimSpace(model)
	a.mu.Unlock()
	slog.Info("opencode-web: model selection recorded (applies to next send)", "model", model)
}

func (a *Agent) GetModel() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.pendingModel
}

// AvailableModels implements core.ModelSwitcher from the runtime catalog.
func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	c, err := a.clientFor(ctx)
	if err != nil {
		return nil
	}
	catalog, err := a.fetchModelCatalog(ctx, c)
	if err != nil {
		slog.Debug("opencode-web: catalog fetch failed", "error", err)
		return nil
	}
	// Share the fresh window map with the usage formula.
	a.usageMu.Lock()
	a.modelWindows = catalog.windows
	a.modelWindowsAt = time.Now()
	a.usageMu.Unlock()
	out := make([]core.ModelOption, len(catalog.Models))
	copy(out, catalog.Models)
	return out
}

var _ core.ModelSwitcher = (*Agent)(nil)

// modelInCatalog resolves the qualified model against the runtime catalog —
// the send-time gate (design §4.3.4: catalog 没有 → send 错误、零 POST).
func (a *Agent) modelInCatalog(ctx context.Context, c *Client, providerID, modelID string) (ocwModelRef, bool) {
	catalog, err := a.fetchModelCatalog(ctx, c)
	if err != nil {
		return ocwModelRef{}, false
	}
	want := modelID
	if providerID != "" {
		want = providerID + "/" + modelID
	}
	for _, m := range catalog.Models {
		if m.Name == want {
			p, id := parseQualifiedModel(m.Name)
			return ocwModelRef{ProviderID: p, ID: id}, true
		}
	}
	return ocwModelRef{}, false
}
