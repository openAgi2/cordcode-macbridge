package dshweb

// Provider / model switching (design §4.3.5): the catalog ALWAYS comes from
// the runtime (llm.models / llm.providers / session.models — 坑 3 red line:
// no hand-written model copies). There is no official backend-global write
// surface: session.selectModel is session-scoped and persists as the
// deployment default, so a bridge-level switch_model targets the most
// recently started session, or is applied right after the next create.

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// modelCatalog is the runtime-fetched provider/model catalog (cached per
// fetch; never hand-written).
type modelCatalog struct {
	groups []modelProviderGroup
	// activeProviders carries the llm.providers rows filtered to active:true
	// (S1: dormant providers never reach list_providers; the full set with
	// state bits goes to diagnostics).
	activeProviders []configurableProviderView
}

// fetchCatalog pulls llm.providers + llm.models from the resolved instance.
func (a *Agent) fetchCatalog(ctx context.Context) (*modelCatalog, error) {
	client, err := a.clientFor(ctx)
	if err != nil {
		return nil, err
	}
	var provs llmProvidersValue
	if err := client.Call(ctx, "llm.providers", map[string]any{}, &provs); err != nil {
		return nil, err
	}
	var models llmModelsValue
	if err := client.Call(ctx, "llm.models", map[string]any{}, &models); err != nil {
		return nil, err
	}
	active := make([]configurableProviderView, 0, len(provs.Providers))
	for _, p := range provs.Providers {
		if p.Active {
			active = append(active, p)
		}
	}
	return &modelCatalog{groups: models.Groups, activeProviders: active}, nil
}

// selection is the recorded bridge-level selection (provider + model +
// reasoning effort), applied through session.selectModel.
type selection struct {
	provider string
	model    string
	effort   string
}

// ── ProviderSwitcher ────────────────────────────────────────────────────────

// SetProviders: providers come from the runtime; iOS-injected provider
// configs are NOT applicable (dsh owns its provider configuration via its own
// settings) — the call is accepted and ignored to satisfy the interface
// contract without fabricating a write surface that does not exist.
func (a *Agent) SetProviders(providers []core.ProviderConfig) {}

// SetActiveProvider records the provider half of the next selection. There is
// no official backend-global write surface (§4.3.5): the provider rides
// session.selectModel targeting the active/most-recent session.
func (a *Agent) SetActiveProvider(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if name == "" {
		a.pendingSel.provider = ""
		return true
	}
	a.pendingSel.provider = name
	return true
}

func (a *Agent) GetActiveProvider() *core.ProviderConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.pendingSel.provider == "" {
		return nil
	}
	models := a.providerModelsLocked(a.pendingSel.provider)
	return &core.ProviderConfig{Name: a.pendingSel.provider, Models: models}
}

// ListProviders returns the runtime's ACTIVE providers with their model
// lists (S1 filter).
func (a *Agent) ListProviders() []core.ProviderConfig {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cat, err := a.fetchCatalog(ctx)
	if err != nil {
		return nil
	}
	out := make([]core.ProviderConfig, 0, len(cat.activeProviders))
	for _, p := range cat.activeProviders {
		out = append(out, core.ProviderConfig{
			Name:   p.Provider,
			Model:  "",
			Models: groupModels(cat.groups, p.Provider),
		})
	}
	return out
}

// providerModelsLocked looks up cached group models (nil catalog → nil).
func (a *Agent) providerModelsLocked(provider string) []core.ModelOption {
	for _, g := range a.catalog.groups {
		if g.ID == provider {
			return groupModels(a.catalog.groups, provider)
		}
	}
	return nil
}

func groupModels(groups []modelProviderGroup, provider string) []core.ModelOption {
	for _, g := range groups {
		if g.ID != provider {
			continue
		}
		out := make([]core.ModelOption, 0, len(g.Models))
		for _, m := range g.Models {
			out = append(out, core.ModelOption{Name: g.ID + "/" + m.ID, Desc: m.Name})
		}
		return out
	}
	return nil
}

// ── ModelSwitcher ───────────────────────────────────────────────────────────

// SetModel records the model (accepting "provider/model" or bare ids per the
// bridge's parseModelID convention) and applies it through the official
// session-scoped surface: session.selectModel on the most recently started
// session when one exists (official semantics: takes effect in-session and
// persists as the deployment default), else right after the next create.
func (a *Agent) SetModel(model string) {
	provider, id := splitQualifiedModel(model)
	a.mu.Lock()
	a.pendingSel.model = id
	if provider != "" {
		a.pendingSel.provider = provider
	}
	sel := a.pendingSel
	target := a.lastActiveSessionID
	a.mu.Unlock()
	a.applySelection(context.Background(), sel, target)
}

func (a *Agent) GetModel() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.pendingSel.provider != "" && a.pendingSel.model != "" {
		return a.pendingSel.provider + "/" + a.pendingSel.model
	}
	return a.pendingSel.model
}

// AvailableModels returns the runtime catalog flattened (provider-qualified
// ids so iOS groups by provider).
func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	cat, err := a.fetchCatalog(ctx)
	if err != nil {
		return nil
	}
	a.mu.Lock()
	a.catalog = cat
	a.mu.Unlock()
	var out []core.ModelOption
	for _, g := range cat.groups {
		out = append(out, groupModels(cat.groups, g.ID)...)
	}
	return out
}

// applySelection performs session.selectModel on target (skipped when the
// selection is incomplete or target is empty) and reports via slog.
func (a *Agent) applySelection(ctx context.Context, sel selection, targetSession string) {
	if sel.provider == "" || sel.model == "" || targetSession == "" {
		return
	}
	client, err := a.clientFor(ctx)
	if err != nil {
		return
	}
	req := sessionSelectModelRequest{
		SessionID:       targetSession,
		Provider:        sel.provider,
		Model:           sel.model,
		ReasoningEffort: sel.effort,
	}
	if err := client.Call(ctx, "session.selectModel", req, nil); err != nil {
		slog.Warn("dsh-web: session.selectModel failed", "session", targetSession, "error", err)
	}
}

// applyPendingModelSelection runs after session.create — the only window a
// pre-create bridge-level selection can reach the official surface.
func (a *Agent) applyPendingModelSelection(ctx context.Context, client *Client, sessionID string) {
	a.mu.RLock()
	sel := a.pendingSel
	a.mu.RUnlock()
	if sel.provider == "" || sel.model == "" {
		return
	}
	req := sessionSelectModelRequest{
		SessionID:       sessionID,
		Provider:        sel.provider,
		Model:           sel.model,
		ReasoningEffort: sel.effort,
	}
	if err := client.Call(ctx, "session.selectModel", req, nil); err != nil {
		slog.Warn("dsh-web: session.selectModel after create failed", "session", sessionID, "error", err)
	}
}

func splitQualifiedModel(raw string) (provider, model string) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "/"); i > 0 {
		return raw[:i], raw[i+1:]
	}
	return "", raw
}

// ── ReasoningEffortSwitcher ────────────────────────────────────────────────

// SetReasoningEffort records the effort half of the selection (rides
// session.selectModel's reasoningEffort field).
func (a *Agent) SetReasoningEffort(effort string) {
	a.mu.Lock()
	a.pendingSel.effort = strings.TrimSpace(effort)
	sel := a.pendingSel
	target := a.lastActiveSessionID
	a.mu.Unlock()
	a.applySelection(context.Background(), sel, target)
}

func (a *Agent) GetReasoningEffort() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.pendingSel.effort
}

// AvailableReasoningEfforts returns the runtime-declared efforts of the
// currently selected model (empty when unknown — never invented).
func (a *Agent) AvailableReasoningEfforts() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, g := range a.catalog.groups {
		for _, m := range g.Models {
			if m.ID != a.pendingSel.model || m.Reasoning == nil {
				continue
			}
			efforts := make([]string, 0, len(m.Reasoning.Efforts))
			for _, e := range m.Reasoning.Efforts {
				efforts = append(efforts, e.ID)
			}
			return efforts
		}
	}
	return nil
}

// ── SessionModelSelectionReader (session truth for get_session) ────────────

// GetSessionModelSelection reads the official per-session current selection
// (session.models → current{provider, model, reasoningEffort}) — the layer-1
// truth of the selection priority chain (session truth > history > cache >
// default). go-bridge merges it into get_session so iOS opens the session
// with its REAL model instead of a global default. ok=false when the RPC
// fails or the session has no current selection — callers must not fabricate.
func (a *Agent) GetSessionModelSelection(ctx context.Context, sessionID string) (core.SessionModelSelection, bool) {
	client, err := a.clientFor(ctx)
	if err != nil {
		return core.SessionModelSelection{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var val sessionModelsValue
	if err := client.Call(ctx, "session.models", sessionModelsRequest{SessionID: sessionID}, &val); err != nil {
		return core.SessionModelSelection{}, false
	}
	if strings.TrimSpace(val.Current.Model) == "" && strings.TrimSpace(val.Current.Provider) == "" {
		return core.SessionModelSelection{}, false
	}
	return core.SessionModelSelection{
		Provider:        val.Current.Provider,
		Model:           val.Current.Model,
		ReasoningEffort: val.Current.ReasoningEffort,
	}, true
}

var _ core.ProviderSwitcher = (*Agent)(nil)
var _ core.ModelSwitcher = (*Agent)(nil)
var _ core.ReasoningEffortSwitcher = (*Agent)(nil)
var _ core.SessionModelSelectionReader = (*Agent)(nil)
