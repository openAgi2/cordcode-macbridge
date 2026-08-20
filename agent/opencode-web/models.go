package opencodeweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// defaults maps connected providerID → its default modelID (envelope
	// `default` field) — the official picker's fallback-chain input.
	defaults map[string]string
	// connectedOrder preserves the envelope's connected provider order (the
	// official fallback takes the FIRST connected provider's default).
	connectedOrder []string
	// variants maps qualified "providerID/modelID" → its live variant keys
	// (E1b: models[modelID].variants object keys; values are ignored — only
	// the keys are selectable).
	variants map[string][]string
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

// ocwProviderEnvelope is the live 1.18 /provider shape (E4b-proven:
// top level is exactly {all, default, connected}; provider rows carry
// {id,name,source,env,options,models}; model rows may carry a variants
// object whose KEYS are selectable and whose values stay opaque).
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
			Variants map[string]json.RawMessage `json:"variants"`
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
	catalog, err := parseProviderCatalog(raw)
	if err != nil {
		return nil, err
	}

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

// parseProviderCatalog builds the connected-filtered catalog from the verified
// 1.18.18 {all, connected, default} envelope. C1 fail-closed rule: any other
// shape is a diagnosable error — the former legacy recursive walk over
// arbitrary model-shaped JSON nodes is deleted (unknown-shape guessing can
// produce plausible-but-false catalogs; the catalog simply becomes
// unavailable and Send/list_models report it honestly).
func parseProviderCatalog(raw []byte) (*ocwModelCatalog, error) {
	// Shape first (E4b: the top level is exactly {all,connected,default});
	// a non-object top level is a shape violation, while type errors inside
	// otherwise-object rows are row malformations (audit-008 W2.2).
	if trimmed := trimSpaceBytes(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("opencode-web: provider catalog shape not recognized — expected the verified 1.18.18 {all,connected,default} envelope; failing closed instead of recursive shape guessing (C1): body=%s", truncateForError(string(raw)))
	}
	var envelope ocwProviderEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("opencode-web: provider catalog malformed (wrong types in a verified row): %w", err)
	}
	if len(envelope.All) == 0 {
		return nil, fmt.Errorf("opencode-web: provider catalog shape not recognized — expected the verified 1.18.18 {all,connected,default} envelope; failing closed instead of recursive shape guessing (C1): body=%s", truncateForError(string(raw)))
	}
	connected := map[string]bool{}
	for _, id := range envelope.Connected {
		connected[id] = true
	}
	catalog := &ocwModelCatalog{
		windows:  map[string]int{},
		defaults: map[string]string{},
		variants: map[string][]string{},
	}
	var rows []core.ModelOption
	for _, provider := range envelope.All {
		// Audit-008 W2.2: a row without `id` is an unidentifiable physical
		// row of the verified `all` array — fail closed, never silently
		// skipped and never repaired with a guess.
		if provider.ID == "" {
			return nil, fmt.Errorf("opencode-web: provider catalog row missing required provider id")
		}
		if !connected[provider.ID] {
			continue // 未配置凭据的 provider 不进选择框（对齐官方网页）；connected 为空 = 无可用模型
		}
		catalog.connectedOrder = append(catalog.connectedOrder, provider.ID)
		if def, ok := envelope.Default[provider.ID]; ok && def != "" {
			catalog.defaults[provider.ID] = def
		}
		for _, model := range provider.Models {
			// E4b: connected model rows carry their own non-empty id — the
			// former map-key fallback is deleted (audit-008 W2.2).
			if model.ID == "" {
				return nil, fmt.Errorf("opencode-web: provider %s model row missing required id", provider.ID)
			}
			id := model.ID
			qualified := provider.ID + "/" + id
			window := 0
			if model.Limit != nil {
				window = model.Limit.Context
			}
			if window > 0 {
				catalog.windows[id] = window
				catalog.windows[qualified] = window
			}
			// E1b: only the live variant KEYS are selectable; the values
			// (reasoning config etc.) stay opaque and never become product
			// configuration. Key order follows the raw object.
			if len(model.Variants) > 0 {
				keys := make([]string, 0, len(model.Variants))
				for key := range model.Variants {
					if key != "" {
						keys = append(keys, key)
					}
				}
				sort.Strings(keys)
				catalog.variants[qualified] = keys
			}
			desc := model.Name
			rows = append(rows, core.ModelOption{Name: qualified, Desc: desc, Variants: catalog.variants[qualified]})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	catalog.Models = rows
	return catalog, nil
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

// ── C5: official model-selection chain (canonical §6.6, E5b-pinned) ──────────

// ocwShapeError marks strict-decode failures: the payload answered but did
// not match the verified shape. Unlike a transport failure (route down,
// 404…), a shape error must fail the send — guessing past it is exactly the
// silent-fallback §6.6 forbids.
type ocwShapeError struct{ detail string }

func (e *ocwShapeError) Error() string { return e.detail }

// fetchConfiguredModel strictly decodes GET /config and returns the optional
// configured default model ("providerID/modelID", "" when absent). A
// non-object or malformed response is an ocwShapeError — /config answers, so
// its shape is evidence, not a guess.
func (a *Agent) fetchConfiguredModel(ctx context.Context, c *Client) (string, error) {
	raw, err := c.fetchJSON(ctx, c.apiPath("/config"), a.GetWorkDir())
	if err != nil {
		return "", err // transport: the caller skips this level
	}
	trimmed := trimSpaceBytes(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", &ocwShapeError{detail: fmt.Sprintf("opencode-web: config payload must be an object (generation-118 verified shape), got: %s", truncateForError(string(raw)))}
	}
	// Audit-008 W2.2: only an evidence-proven ABSENT `model` key means "no
	// configured model". A present-but-null / non-string / empty value is an
	// unproven shape and fails closed.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", &ocwShapeError{detail: fmt.Sprintf("opencode-web: config payload malformed: %v", err)}
	}
	rawModel, present := obj["model"]
	if !present {
		return "", nil
	}
	var model string
	if err := json.Unmarshal(rawModel, &model); err != nil || model == "" {
		return "", &ocwShapeError{detail: fmt.Sprintf("opencode-web: config model key present but not a non-empty string (generation-118 unproven shape): %s", truncateForError(string(rawModel)))}
	}
	if _, _, ok := strings.Cut(model, "/"); !ok {
		return "", &ocwShapeError{detail: fmt.Sprintf("opencode-web: configured model %q is not a providerID/modelID pair", model)}
	}
	return model, nil
}

// catalogValid mirrors the official picker's `valid` (prompt-model-selection.
// ts:19-23): the provider row exists in `all`, carries the model, and the
// provider is connected.
func (c *ocwModelCatalog) catalogValid(providerID, modelID string) (ocwModelRef, bool) {
	if providerID == "" || modelID == "" {
		return ocwModelRef{}, false
	}
	for _, m := range c.Models {
		if m.Name == providerID+"/"+modelID {
			return ocwModelRef{ProviderID: providerID, ID: modelID}, true
		}
	}
	return ocwModelRef{}, false
}

// configuredDefault implements selection level 3: resolveDefaultModel(
// providerDefault, config.model) — provider-catalog.ts:29-37 branch order,
// E5b-pinned. A defined /provider default for the FIRST connected provider
// wins BEFORE legacy /config.model; the config string is only consulted when
// that provider default is absent. A /config SHAPE error fails the chain
// (strict decode); a transport failure skips the level exactly like the
// official picker's configured() yielding undefined.
func (a *Agent) configuredDefault(ctx context.Context, c *Client, catalog *ocwModelCatalog) (ocwModelRef, bool, error) {
	if len(catalog.connectedOrder) > 0 {
		first := catalog.connectedOrder[0]
		if modelID, ok := catalog.defaults[first]; ok && modelID != "" {
			if ref, valid := catalog.catalogValid(first, modelID); valid {
				return ref, true, nil
			}
		}
	}
	configured, err := a.fetchConfiguredModel(ctx, c)
	if err != nil {
		var shape *ocwShapeError
		if errors.As(err, &shape) {
			return ocwModelRef{}, false, err
		}
		return ocwModelRef{}, false, nil
	}
	if configured == "" {
		return ocwModelRef{}, false, nil
	}
	providerID, modelID := parseQualifiedModel(configured)
	ref, valid := catalog.catalogValid(providerID, modelID)
	return ref, valid, nil
}

// resolvePromptModel walks the official chain (canonical §6.6): current →
// agent model → provider-default-over-config → recent → first-connected
// fallback. Each candidate must be catalog-valid before use; an invalid
// candidate advances to the next documented level, never to a guess. When no
// candidate validates the caller must issue ZERO prompt POSTs.
func (s *serverSession) resolvePromptModel(ctx context.Context, c *Client, explicit ocwModelRef, agentModel string) (ocwModelRef, error) {
	catalog, err := s.a.fetchModelCatalog(ctx, c)
	if err != nil {
		return ocwModelRef{}, fmt.Errorf("opencode-web: provider catalog unavailable: %w", err)
	}
	// (1) explicit current selection (per-request option or legacy pending).
	candidates := []ocwModelRef{explicit}
	if pending := s.a.GetModel(); pending != "" {
		p, id := parseQualifiedModel(pending)
		candidates = append(candidates, ocwModelRef{ProviderID: p, ID: id})
	}
	// (2) selected agent's configured model.
	if agentModel != "" {
		p, id := parseQualifiedModel(agentModel)
		candidates = append(candidates, ocwModelRef{ProviderID: p, ID: id})
	}
	for _, cand := range candidates {
		if ref, ok := catalog.catalogValid(cand.ProviderID, cand.ID); ok {
			return ref, nil
		}
	}
	// (3) resolveDefaultModel(providerDefault, config.model).
	if ref, ok, err := s.a.configuredDefault(ctx, c, catalog); err != nil {
		return ocwModelRef{}, err // strict shape error — zero POSTs
	} else if ok {
		return ref, nil
	}
	// (4) recent session model (resume-adopted server truth).
	if m, ok := s.model.Load().(*ocwModelRef); ok && m != nil {
		if ref, valid := catalog.catalogValid(m.ProviderID, m.ID); valid {
			return ref, nil
		}
	}
	// (5) first connected provider's default ?? its first catalog model.
	if ref, ok := catalog.fallbackModel(); ok {
		return ref, nil
	}
	return ocwModelRef{}, fmt.Errorf("opencode-web: no connected valid model — zero prompt POSTs (configure a provider in OpenCode first)")
}

// modelVariants returns the live variant keys for a resolved model
// (nil = the model declares no variants).
func (a *Agent) modelVariants(ctx context.Context, c *Client, providerID, modelID string) []string {
	catalog, err := a.fetchModelCatalog(ctx, c)
	if err != nil {
		return nil
	}
	return catalog.variants[providerID+"/"+modelID]
}
