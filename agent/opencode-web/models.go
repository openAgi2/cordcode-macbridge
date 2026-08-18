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
// catalog (GET /provider). Model windows, names, and ids only ever come from
// the runtime — no hand-written allowlist, no on-disk cache (design §2.2
// 纪律 3).
//
// Live-pinned 1.18.18 envelope (2026-08-19, owner serve read-only probe):
//
//	{
//	  "all":       [ {"id","name","source","env","options","models": {mid: {…, "limit": {"context": n}}}} ],  // models.dev 全量，192 providers / 6600+ models / ~5MB
//	  "default":   { providerID → default modelID },
//	  "connected": ["zhipuai-coding-plan", …]        // ← 已配置凭据的 provider；官方网页选择框只渲染这些
//	}
//
// The catalog therefore filters to `connected` providers (matching the
// official web picker — surfacing 6600+ unconfigured models is pollution),
// and the parsed result is cached (the raw JSON is ~5MB; list_models, the
// send-time catalog gate, and the usage-window lookup all share one fetch).

// ocwModelCatalog is one runtime provider/model snapshot.
type ocwModelCatalog struct {
	// Models are qualified "providerID/modelID" ModelOptions in stable order,
	// connected providers only.
	Models []core.ModelOption
	// windows is the shared window map (also used by the usage formula).
	windows map[string]int
}

// catalogCacheTTL bounds catalog freshness; one 5MB fetch per window at most.
const catalogCacheTTL = 60 * time.Second

type catalogCacheEntry struct {
	catalog *ocwModelCatalog
	at      time.Time
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

// ocwProviderEnvelope is the live 1.18 /provider shape.
type ocwProviderEnvelope struct {
	All []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Source string `json:"source"`
		Models map[string]struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Limit *struct {
				Context int `json:"context"`
			} `json:"limit"`
		} `json:"models"`
	} `json:"all"`
	Default   map[string]string `json:"default"`
	Connected []string          `json:"connected"`
}

// fetchModelCatalog returns the cached connected-provider catalog, refreshing
// on TTL expiry. The single fetch point for every consumer.
func (a *Agent) fetchModelCatalog(ctx context.Context, c *Client) (*ocwModelCatalog, error) {
	a.catalogEntryMu.Lock()
	if a.catalogEntry != nil && time.Since(a.catalogEntry.at) < catalogCacheTTL {
		entry := a.catalogEntry.catalog
		a.catalogEntryMu.Unlock()
		return entry, nil
	}
	a.catalogEntryMu.Unlock()

	raw, err := c.fetchJSON(ctx, c.apiPath("/provider"), a.GetWorkDir())
	if err != nil {
		return nil, err
	}
	catalog := parseProviderCatalog(raw)

	a.catalogEntryMu.Lock()
	a.catalogEntry = &catalogCacheEntry{catalog: catalog, at: time.Now()}
	a.catalogEntryMu.Unlock()

	// Keep the window map shared with the usage formula.
	a.usageMu.Lock()
	a.modelWindows = catalog.windows
	a.modelWindowsAt = time.Now()
	a.usageMu.Unlock()
	return catalog, nil
}

// parseProviderCatalog builds the connected-filtered catalog; falls back to
// the legacy recursive walk when the envelope shape is absent (other
// generations / future drift).
func parseProviderCatalog(raw []byte) *ocwModelCatalog {
	var envelope ocwProviderEnvelope
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.All) > 0 {
		connected := map[string]bool{}
		for _, id := range envelope.Connected {
			connected[id] = true
		}
		catalog := &ocwModelCatalog{windows: map[string]int{}}
		var rows []core.ModelOption
		for _, provider := range envelope.All {
			if provider.ID == "" || len(provider.Models) == 0 {
				continue
			}
			if !connected[provider.ID] {
				continue // 未配置凭据的 provider 不进选择框（对齐官方网页）；connected 为空 = 无可用模型
			}
			for _, model := range provider.Models {
				id := model.ID
				if id == "" {
					continue
				}
				window := 0
				if model.Limit != nil {
					window = model.Limit.Context
				}
				if window > 0 {
					catalog.windows[id] = window
					catalog.windows[provider.ID+"/"+id] = window
				}
				desc := model.Name
				rows = append(rows, core.ModelOption{Name: provider.ID + "/" + id, Desc: desc})
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		catalog.Models = rows
		return catalog
	}

	// Legacy/unknown shape: recursive walk of any model-shaped node.
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return &ocwModelCatalog{windows: map[string]int{}}
	}
	var rows []core.ModelOption
	seen := map[string]bool{}
	windows := map[string]int{}
	collectCatalogModels(root, "", &rows, seen, windows)
	catalog := &ocwModelCatalog{windows: windows}
	for _, row := range rows {
		catalog.Models = append(catalog.Models, row)
	}
	return catalog
}

type catalogRow struct {
	qualified string
	desc      string
}

// collectCatalogModels is the legacy fallback walk (envelope absent).
func collectCatalogModels(node any, providerID string, rows *[]core.ModelOption, seen map[string]bool, windows map[string]int) {
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
				*rows = append(*rows, core.ModelOption{Name: qualified, Desc: name})
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

// AvailableModels implements core.ModelSwitcher from the runtime catalog
// (connected providers only, cached).
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
