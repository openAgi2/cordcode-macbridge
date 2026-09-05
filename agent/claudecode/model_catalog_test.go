package claudecode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// model_catalog_test.go —— Phase 1 真值链定向测试。
//
// fixture 纪律（设计 §7.1）：控制协议 fixture 全部来自 Phase 0 证据包的真实
// control_response 原文（scripts/claudecode-phase0/dumps/main.jsonl，CLI 2.1.234，
// 2026-09-04），逐字提取到 testdata/control_protocol/。未知形状必须让功能降级
// 而非测试绿。

func loadControlFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "control_protocol", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return obj
}

func fixtureInnerResponse(t *testing.T, name string) map[string]any {
	t.Helper()
	obj := loadControlFixture(t, name)
	resp, ok := obj["response"].(map[string]any)
	if !ok {
		t.Fatalf("fixture %s: missing response envelope", name)
	}
	return resp
}

// 真实 initialize 成功体（Phase 0 dump req_1）必须完整解码为 5 行目录，
// resolvedModel=canonical，effort 字段按行可选。
func TestParseModelCatalog_FromRealInitializeFixture(t *testing.T) {
	resp := fixtureInnerResponse(t, "initialize_response.json")
	cr := controlResponse{Subtype: "success", Raw: resp}
	payload, ok := controlPayload(cr)
	if !ok {
		t.Fatalf("controlPayload failed on real fixture")
	}
	entries, ok := parseModelCatalog(payload)
	if !ok {
		t.Fatalf("parseModelCatalog rejected the real initialize payload")
	}
	if len(entries) != 5 {
		t.Fatalf("entries = %d, want 5 (real dump had 5 slots)", len(entries))
	}
	byValue := map[string]claudeModelEntry{}
	for _, e := range entries {
		byValue[e.Value] = e
	}
	if e := byValue["sonnet"]; e.ResolvedModel != "claude-sonnet-5" || e.DisplayName != "Sonnet" {
		t.Errorf("sonnet row = %+v", e)
	}
	if e := byValue["haiku"]; e.SupportsEffort || len(e.SupportedEffortLevels) != 0 {
		t.Errorf("haiku row should carry no effort fields (real dump): %+v", e)
	}
	if e := byValue["sonnet"]; len(e.SupportedEffortLevels) != 5 {
		t.Errorf("sonnet effort levels = %v, want 5", e.SupportedEffortLevels)
	}
	if e := byValue["opus[1m]"]; e.ResolvedModel != "claude-opus-5[1m]" {
		t.Errorf("opus[1m] resolved = %q", e.ResolvedModel)
	}
}

// 真实 list_models 成功体（Phase 0 dump req_2）与 initialize 同构。
func TestParseModelCatalog_FromRealListModelsFixture(t *testing.T) {
	resp := fixtureInnerResponse(t, "list_models_response.json")
	payload, ok := controlPayload(controlResponse{Subtype: "success", Raw: resp})
	if !ok {
		t.Fatalf("controlPayload failed on real list_models fixture")
	}
	entries, ok := parseModelCatalog(payload)
	if !ok || len(entries) != 5 {
		t.Fatalf("list_models fixture decode: ok=%v entries=%d", ok, len(entries))
	}
}

// fail closed：未知/畸形形状必须整包拒绝，不得部分采信。
func TestParseModelCatalog_FailClosed(t *testing.T) {
	cases := []map[string]any{
		{},
		{"models": "not-an-array"},
		{"models": []any{}},
		{"models": []any{"not-an-object"}},
		{"models": []any{map[string]any{"displayName": "no-value"}}},
		{"models": []any{map[string]any{"value": 123}}},
	}
	for i, payload := range cases {
		if _, ok := parseModelCatalog(payload); ok {
			t.Errorf("case %d: parseModelCatalog accepted malformed payload %v", i, payload)
		}
	}
}

// 错误 subtype / 缺失载荷必须拒绝（能力降级，不是解析成功）。
func TestControlPayload_RejectsNonSuccess(t *testing.T) {
	if _, ok := controlPayload(controlResponse{Subtype: "error", Raw: map[string]any{"subtype": "error"}}); ok {
		t.Errorf("error subtype must not yield a payload")
	}
	if _, ok := controlPayload(controlResponse{Subtype: "success", Raw: map[string]any{"subtype": "success"}}); ok {
		t.Errorf("success without nested response payload must not yield a payload")
	}
}

// request_id 配对收件：真实信封形状 → 命中 pending；未知 id / 畸形 → 不命中不 panic。
func TestDispatchControlResponse(t *testing.T) {
	cs := &claudeSession{done: make(chan struct{})}
	inner := fixtureInnerResponse(t, "list_models_response.json")
	frame := map[string]any{"type": "control_response", "response": inner}

	if cs.dispatchControlResponse(frame) {
		t.Errorf("dispatch on empty pending map must return false")
	}

	rid, _ := inner["request_id"].(string)
	ch := make(chan controlResponse, 1)
	cs.ctrlPending = map[string]chan controlResponse{rid: ch}
	if !cs.dispatchControlResponse(frame) {
		t.Fatalf("dispatch must route the matching request_id")
	}
	select {
	case got := <-ch:
		if got.Subtype != "success" {
			t.Errorf("subtype = %q", got.Subtype)
		}
		if _, ok := controlPayload(got); !ok {
			t.Errorf("delivered response must expose the nested payload")
		}
	default:
		t.Fatalf("response was not delivered")
	}

	foreign := map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": "someone-else"}}
	if cs.dispatchControlResponse(foreign) {
		t.Errorf("foreign request_id must not match")
	}
	if cs.dispatchControlResponse(map[string]any{"type": "control_response"}) {
		t.Errorf("malformed frame must not match")
	}
}

// sendControlRequest 端到端（无进程）：信封写入 stdin，响应经 dispatch 配对返回。
func TestSendControlRequest_EnvelopeAndPairing(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Skipf("pipe: %v", err)
	}
	cs := &claudeSession{stdin: w, done: make(chan struct{})}

	type envelope struct {
		Type      string         `json:"type"`
		RequestID string         `json:"request_id"`
		Request   map[string]any `json:"request"`
	}

	errCh := make(chan error, 1)
	go func() {
		var env envelope
		if err := json.NewDecoder(r).Decode(&env); err != nil {
			errCh <- err
			return
		}
		if env.Type != "control_request" {
			errCh <- json.Unmarshal([]byte(`{"type":"bad"}`), &struct{}{})
			return
		}
		// 回一个真实形状的响应（嵌套第二层 response）
		cs.dispatchControlResponse(map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": env.RequestID,
				"response":   map[string]any{"models": []any{map[string]any{"value": "sonnet"}}},
			},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := cs.sendControlRequest(ctx, map[string]any{"subtype": "list_models"})
	if err != nil {
		select {
		case e := <-errCh:
			t.Fatalf("envelope decode on reader side failed: %v", e)
		default:
		}
		t.Fatalf("sendControlRequest: %v", err)
	}
	payload, ok := controlPayload(resp)
	if !ok {
		t.Fatalf("response not a success payload: %+v", resp)
	}
	entries, ok := parseModelCatalog(payload)
	if !ok || len(entries) != 1 || entries[0].Value != "sonnet" {
		t.Fatalf("payload decode: ok=%v entries=%v", ok, entries)
	}
	select {
	case e := <-errCh:
		t.Fatalf("reader error: %v", e)
	default:
	}
}

// 主源渲染：init 目录 → wire 三键（id/resolved/observedModel 值）。
func TestInitCatalogOptions_ThreeKeys(t *testing.T) {
	a := &Agent{}
	resp := fixtureInnerResponse(t, "initialize_response.json")
	payload, _ := controlPayload(controlResponse{Subtype: "success", Raw: resp})
	entries, ok := parseModelCatalog(payload)
	if !ok {
		t.Fatalf("fixture decode failed")
	}
	a.catalog.adoptInit(entries)
	a.catalog.observe("sonnet", "glm-5.3")

	opts := a.initCatalogOptions()
	if len(opts) != 5 {
		t.Fatalf("opts = %d, want 5", len(opts))
	}
	var sonnet, haiku *core.ModelOption
	for i := range opts {
		switch opts[i].Name {
		case "sonnet":
			sonnet = &opts[i]
		case "haiku":
			haiku = &opts[i]
		}
	}
	if sonnet == nil || haiku == nil {
		t.Fatalf("missing rows: %+v", opts)
	}
	if sonnet.Resolved != "claude-sonnet-5" {
		t.Errorf("sonnet resolved = %q", sonnet.Resolved)
	}
	if sonnet.Observed != "glm-5.3" {
		t.Errorf("sonnet observed = %q, want glm-5.3", sonnet.Observed)
	}
	if haiku.Observed != "" {
		t.Errorf("haiku observed should be empty (no observation), got %q", haiku.Observed)
	}
}

// 观测只在请求名精确匹配时生效；同值/空值不记录（映射不稳定，禁止猜测）。
func TestCatalogObserve_Guards(t *testing.T) {
	a := &Agent{}
	a.observeAssistantModel("sonnet", "sonnet")
	a.observeAssistantModel("", "glm-5.3")
	a.observeAssistantModel("sonnet", "")
	if got := a.catalog.observedFor("sonnet"); got != "" {
		t.Errorf("identity/empty observations must not be recorded, got %q", got)
	}
	a.observeAssistantModel("sonnet", "glm-5.3")
	if got := a.catalog.observedFor("sonnet"); got != "glm-5.3" {
		t.Errorf("observedFor = %q", got)
	}
	if got := a.catalog.observedFor("opus"); got != "" {
		t.Errorf("unobserved row must stay empty, got %q", got)
	}
}

// 真值链 ①：有 init 目录时 AvailableModels 直接主源返回（不再走网关/别名）。
func TestAvailableModels_InitCatalogPrimary(t *testing.T) {
	a := &Agent{}
	resp := fixtureInnerResponse(t, "initialize_response.json")
	payload, _ := controlPayload(controlResponse{Subtype: "success", Raw: resp})
	entries, _ := parseModelCatalog(payload)
	a.catalog.adoptInit(entries)

	got := a.AvailableModels(context.Background())
	if len(got) != 5 {
		t.Fatalf("AvailableModels = %d rows, want 5 (init primary)", len(got))
	}
	if got[0].Resolved == "" {
		t.Errorf("init rows must carry resolved keys")
	}
}

// S7 分类：显式 routerURL/provider=真网关（含 loopback CCR）；env loopback=泄漏代理
// 不算；env 非 loopback=真网关。
func TestUsesRealGateway_Classification(t *testing.T) {
	a := &Agent{routerURL: "http://127.0.0.1:3456"}
	if !a.usesRealGateway() {
		t.Errorf("explicit loopback routerURL (CCR) is a real gateway")
	}

	a = &Agent{providers: []core.ProviderConfig{{BaseURL: "http://127.0.0.1:9999"}}, activeIdx: 0}
	if !a.usesRealGateway() {
		t.Errorf("explicit provider baseURL is a real gateway")
	}

	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:15721/claude-desktop")
	a = &Agent{}
	if a.usesRealGateway() {
		t.Errorf("loopback env BASE_URL (GUI-leaked proxy shape) must NOT be a real gateway")
	}

	t.Setenv("ANTHROPIC_BASE_URL", "https://open.bigmodel.cn/api/anthropic")
	if !a.usesRealGateway() {
		t.Errorf("non-loopback env BASE_URL is a real gateway")
	}
}

// 降级去向：env loopback 泄漏型不进网关合并分支 → 落别名/观测级。
func TestAvailableModels_LoopbackEnvFallsToAliases(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:15721/claude-desktop")
	writeClaudeSettings(t, dir, `{
		"env": {
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "claude-haiku-4-5",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME": "glm-4.7",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-4-6",
			"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "glm-5-turbo",
			"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-8",
			"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME": "glm-5.2"
		}
	}`)

	// 这个"网关"对 /v1/models 返回一堆模型；泄漏型分类下绝不能把它们合并进来。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gateway-only-model","display_name":"X"}]}`))
	}))
	defer srv.Close()

	a := &Agent{}
	// 把 env 指到 httptest（loopback）模拟泄漏代理。
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)

	models := a.AvailableModels(context.Background())
	for _, m := range models {
		if m.Name == "gateway-only-model" {
			t.Fatalf("loopback leaked-proxy env must not drive gateway merging; got %v", models)
		}
	}
	if len(models) == 0 {
		t.Fatalf("alias fallback should have produced rows")
	}
}

// 真网关拉取走双头（x-api-key + Bearer）——M6 定位为网关兼容，非官方标准。
func TestFetchModelsFromAPI_DualAuthHeaders(t *testing.T) {
	var gotXAPIKey, gotBearer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXAPIKey = r.Header.Get("x-api-key")
		gotBearer = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3","display_name":"GLM 5.3"}]}`))
	}))
	defer srv.Close()

	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok-123")

	a := &Agent{}
	models := a.fetchModelsFromAPI(context.Background())
	if len(models) != 1 || models[0].Name != "glm-5.3" {
		t.Fatalf("fetchModelsFromAPI = %v", models)
	}
	if gotXAPIKey != "tok-123" || gotBearer != "Bearer tok-123" {
		t.Errorf("dual headers: x-api-key=%q Authorization=%q", gotXAPIKey, gotBearer)
	}
}

// per-model effort 真值：目录行内 haiku 不支持 effort；目录外行回落静态表。
func TestEffortsForModel_PerRowTruth(t *testing.T) {
	a := &Agent{}
	resp := fixtureInnerResponse(t, "initialize_response.json")
	payload, _ := controlPayload(controlResponse{Subtype: "success", Raw: resp})
	entries, _ := parseModelCatalog(payload)
	a.catalog.adoptInit(entries)

	levels, _, ok := a.EffortsForModel(context.Background(), "sonnet")
	if !ok || len(levels) != 5 {
		t.Fatalf("sonnet efforts = %v ok=%v", levels, ok)
	}
	levels, _, ok = a.EffortsForModel(context.Background(), "haiku")
	if !ok || len(levels) != 0 {
		t.Fatalf("haiku must report no effort levels (real dump truth), got %v ok=%v", levels, ok)
	}
	levels, _, ok = a.EffortsForModel(context.Background(), "glm-unknown-row")
	if !ok || len(levels) == 0 {
		t.Fatalf("out-of-catalog row falls back to static list, got %v ok=%v", levels, ok)
	}
}
