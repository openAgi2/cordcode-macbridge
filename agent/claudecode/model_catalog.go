package claudecode

import (
	"context"
	"net/url"
	"os"
	"sync"
	"time"

	"log/slog"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// model_catalog.go 实现模型目录真值链（设计 §6 Phase 1，v2.2）：
//
//	① initialize.models（typed 主源，会话建立时发送并缓存）
//	② list_models（刷新/对照；成功体形状以 Phase 0 dump 为准）
//	③ 观测补充：assistant message.model → seen-alive + 请求名→改写名映射
//	④ 降级源：settings.json 三槽位别名；真网关（非 loopback）双头拉取
//
// 三键展示：槽位（wire id，请求侧名）/ resolved（canonical）/
// observedModel（网关改写后的执行名）。Phase 0 证据（CLI 2.1.234，
// scripts/claudecode-phase0/）：
//   - initialize.models 与 list_models.models 同构；无需先 initialize
//   - ModelInfo 字段：value/resolvedModel/displayName/description/
//     supportsEffort/supportedEffortLevels/supportsAdaptiveThinking/
//     supportsFastMode/supportsAutoMode —— effort 族为可选（haiku 无）
//   - resolvedModel 恒为 canonical（claude-sonnet-5 等），cc-switch env 别名
//     不改变目录映射；执行侧改写只能靠 message.model 观测
//   - 成功体载荷嵌套在 control_response.response.response

// claudeModelEntry is one catalog row from the official control plane
// (initialize.models / list_models; both dump-verified isomorphic).
type claudeModelEntry struct {
	Value                    string   `json:"value"`
	ResolvedModel            string   `json:"resolvedModel"`
	DisplayName              string   `json:"displayName"`
	Description              string   `json:"description"`
	SupportsEffort           bool     `json:"supportsEffort"`
	SupportedEffortLevels    []string `json:"supportedEffortLevels"`
	SupportsAdaptiveThinking bool     `json:"supportsAdaptiveThinking"`
	SupportsFastMode         bool     `json:"supportsFastMode"`
	SupportsAutoMode         bool     `json:"supportsAutoMode"`
}

// parseModelCatalog decodes the models array from an initialize/list_models
// control_response payload. Fail closed: any element that is not an object
// with a non-empty string value aborts the whole catalog (unknown shape ⇒ no
// catalog from this source, never a partial guess).
func parseModelCatalog(payload map[string]any) ([]claudeModelEntry, bool) {
	rawList, ok := payload["models"].([]any)
	if !ok || len(rawList) == 0 {
		return nil, false
	}
	out := make([]claudeModelEntry, 0, len(rawList))
	for _, item := range rawList {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		value, _ := m["value"].(string)
		if value == "" {
			return nil, false
		}
		entry := claudeModelEntry{Value: value}
		entry.ResolvedModel, _ = m["resolvedModel"].(string)
		entry.DisplayName, _ = m["displayName"].(string)
		entry.Description, _ = m["description"].(string)
		entry.SupportsEffort, _ = m["supportsEffort"].(bool)
		entry.SupportsAdaptiveThinking, _ = m["supportsAdaptiveThinking"].(bool)
		entry.SupportsFastMode, _ = m["supportsFastMode"].(bool)
		entry.SupportsAutoMode, _ = m["supportsAutoMode"].(bool)
		if levels, ok := m["supportedEffortLevels"].([]any); ok {
			for _, lv := range levels {
				if s, ok := lv.(string); ok {
					entry.SupportedEffortLevels = append(entry.SupportedEffortLevels, s)
				}
			}
		}
		out = append(out, entry)
	}
	return out, true
}

// controlPayload extracts the nested success payload from a control_response
// envelope object ({"subtype":"success","request_id":…,"response":{…}}).
// Returns ok=false when subtype != "success" or the payload is missing.
func controlPayload(resp controlResponse) (map[string]any, bool) {
	if resp.Subtype != "success" {
		return nil, false
	}
	payload, ok := resp.Raw["response"].(map[string]any)
	return payload, ok
}

// catalogState is the agent-level truth-chain cache. All fields guarded by mu.
type catalogState struct {
	mu          sync.Mutex
	fromInit    []claudeModelEntry // 主源：initialize.models（会话建立时刷新）
	fromInitAt  time.Time
	observed    map[string]string // 请求侧名（slot/canonical）→ 执行侧观测名
	liveSession *claudeSession    // 最近一次成功建立的会话（list_models 刷新通道）
}

func (c *catalogState) adoptInit(entries []claudeModelEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fromInit = entries
	c.fromInitAt = time.Now()
}

func (c *catalogState) snapshotInit() ([]claudeModelEntry, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]claudeModelEntry, len(c.fromInit))
	copy(out, c.fromInit)
	return out, c.fromInitAt
}

func (c *catalogState) observe(requested, observed string) {
	if requested == "" || observed == "" || requested == observed {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.observed == nil {
		c.observed = make(map[string]string)
	}
	c.observed[requested] = observed
}

// observedFor returns the latest observed execution model for a requested
// name (exact match only — rewrite mappings are unstable across alias
// families, never guessed; 设计 §3.3).
func (c *catalogState) observedFor(requested string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observed[requested]
}

// setLiveSession records the most recently established session for
// list_models refresh; nil clears it.
func (c *catalogState) setLiveSession(s *claudeSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.liveSession = s
}

func (c *catalogState) liveSessionSnapshot() *claudeSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.liveSession
}

// beginCatalogSync is called after a session is established (StartSession).
// It fires the bare initialize (no hooks field — settings-layer HTTP hooks
// are unaffected, Phase 0 R2-S7 comparison) and adopts the returned catalog
// as the primary source. Fail closed: on any error/timeout/unknown shape the
// previous catalog (if any) stays and the next session establishment retries.
func (a *Agent) beginCatalogSync(sess *claudeSession) {
	a.catalog.setLiveSession(sess)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		resp, err := sess.sendControlRequest(ctx, map[string]any{"subtype": "initialize"})
		if err != nil {
			slog.Info("claudecode: catalog initialize unavailable (fail-closed)", "error", err)
			return
		}
		payload, ok := controlPayload(resp)
		if !ok {
			slog.Info("claudecode: catalog initialize non-success or unknown shape", "subtype", resp.Subtype)
			return
		}
		entries, ok := parseModelCatalog(payload)
		if !ok {
			slog.Info("claudecode: catalog initialize payload rejected (fail-closed)")
			return
		}
		a.catalog.adoptInit(entries)
		slog.Info("claudecode: catalog source=initialize", "models", len(entries),
			"first", entries[0].Value, "resolved", entries[0].ResolvedModel)
	}()
}

// refreshCatalogViaListModels refreshes the catalog through the most recent
// live session using the side-effect-free list_models control request
// (thin-client refresh, 设计 §6 Phase 1.2). No session / failure / unknown
// shape ⇒ no-op (fail closed).
func (a *Agent) refreshCatalogViaListModels(ctx context.Context) {
	sess := a.catalog.liveSessionSnapshot()
	if sess == nil || !sess.alive.Load() {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := sess.sendControlRequest(rctx, map[string]any{"subtype": "list_models"})
	if err != nil {
		slog.Debug("claudecode: catalog list_models refresh failed (fail-closed)", "error", err)
		return
	}
	payload, ok := controlPayload(resp)
	if !ok {
		slog.Debug("claudecode: catalog list_models non-success or unknown shape", "subtype", resp.Subtype)
		return
	}
	entries, ok := parseModelCatalog(payload)
	if !ok {
		slog.Debug("claudecode: catalog list_models payload rejected (fail-closed)")
		return
	}
	a.catalog.adoptInit(entries)
	slog.Info("claudecode: catalog source=list_models(refresh)", "models", len(entries))
}

// observeAssistantModel records request→execution model observations from
// live assistant frames (wired via claudeSession.onAssistantModel).
func (a *Agent) observeAssistantModel(requested, observed string) {
	a.catalog.observe(requested, observed)
}

// initCatalogOptions renders the initialize/list_models catalog as wire
// options with resolved + observed keys.
func (a *Agent) initCatalogOptions() []core.ModelOption {
	entries, capturedAt := a.catalog.snapshotInit()
	if len(entries) == 0 {
		return nil
	}
	_ = capturedAt // freshness only matters for refresh decisions, not rendering
	out := make([]core.ModelOption, 0, len(entries))
	for _, e := range entries {
		out = append(out, core.ModelOption{
			Name:        e.Value,
			Desc:        e.DisplayName,
			Description: e.Description,
			Resolved:    e.ResolvedModel,
			Observed:    a.catalog.observedFor(e.Value),
		})
	}
	return out
}

// enrichObservations overlays observed execution models onto option rows
// matched by requested name (exact match only).
func (a *Agent) enrichObservations(opts []core.ModelOption) []core.ModelOption {
	for i := range opts {
		if opts[i].Observed == "" {
			opts[i].Observed = a.catalog.observedFor(opts[i].Name)
		}
	}
	return opts
}

// isLoopbackBaseURL reports whether a base URL points at a loopback proxy
// (GUI-leaked cc-switch style). Such endpoints are NOT real gateways: they
// don't reliably proxy /v1/models and must not drive gateway merging
// (设计 §6 Phase 1.4 / S7).
func isLoopbackBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// usesRealGateway reports whether a real model gateway is deliberately in
// play (the only case where /v1/models pulling is meaningful). Explicit
// configuration (routerURL = Claude Code Router, provider BaseURL) always
// counts — CCR canonically runs on 127.0.0.1 and is a real gateway. The
// ambient ANTHROPIC_BASE_URL env var is only trusted when NON-loopback: a
// loopback value there is the GUI-leaked cc-switch proxy shape (设计 §2.4/
// §6 Phase 1.4 / S7：真网关与 GUI 泄漏代理不得归为同类), which must not
// drive gateway merging — those deployments fall to the alias/observation
// level instead.
func (a *Agent) usesRealGateway() bool {
	a.mu.RLock()
	routerURL := a.routerURL
	a.mu.RUnlock()
	if routerURL != "" {
		return true
	}
	if a.activeProviderBaseURL() != "" {
		return true
	}
	base := os.Getenv("ANTHROPIC_BASE_URL")
	return base != "" && !isLoopbackBaseURL(base)
}

func (a *Agent) activeProviderBaseURL() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.activeIdx >= 0 && a.activeIdx < len(a.providers) {
		return a.providers[a.activeIdx].BaseURL
	}
	return ""
}

// EffortsForModel implements core.ModelEffortCatalog using the official
// per-model effort truth from initialize/list_models. Rows covered by the
// catalog return their own truth (an entry without effort support reports no
// levels — e.g. haiku per Phase 0 dump); rows outside the catalog fall back
// to the agent-level static list so the fallback chain keeps today's UX.
func (a *Agent) EffortsForModel(ctx context.Context, model string) ([]string, string, bool) {
	entries, _ := a.catalog.snapshotInit()
	for _, e := range entries {
		if e.Value != model {
			continue
		}
		if !e.SupportsEffort {
			return nil, "", true
		}
		if len(e.SupportedEffortLevels) == 0 {
			return nil, "", true
		}
		levels := make([]string, len(e.SupportedEffortLevels))
		copy(levels, e.SupportedEffortLevels)
		return levels, "", true
	}
	static := a.AvailableReasoningEfforts()
	return static, "", true
}
