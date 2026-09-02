package grokbuild

// Model/effort switching over the ACP surface (grok 1.0.13, target binary
// 5e9a58528b76 — real protocol samples captured 2026-09-02):
//
//   - initialize response `_meta.modelState` = official catalog
//     {currentModelId, availableModels[].{modelId,name,description,_meta}}
//     with effort truth in per-model `_meta.{supportsReasoningEffort,
//     reasoningEffort, reasoningEfforts[]}`.
//   - session/new params `_meta.{modelId,reasoningEffort}` (both consumed —
//     sessionConfig options flip selected:true; result `models` reflects the
//     applied selection).
//   - session/set_model (SNAKE-CASE on 1.0.13; camelCase setModel → -32601)
//     {sessionId, modelId(required server-side), _meta:{reasoningEffort}};
//     persisted to summary.json (current_model_id / reasoning_effort).
//   - session/load accepts no model params; explicit selection applies via
//     set_model after load (official headless apply_headless_model_and_effort).

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// capturedRequest is one JSON-RPC request the fake agent observed.
type capturedRequest struct {
	Method string
	Params map[string]any
}

// startModelProbeAgent runs a fake stdio ACP agent over io.Pipe that answers
// initialize/authenticate/session-new/load/prompt/set_model immediately and
// records every request. initPayload overrides the initialize result (nil =
// default without modelState).
func startModelProbeAgent(t *testing.T, initPayload map[string]any, newResult map[string]any, loadResult map[string]any) (s *grokSession, requests *[]capturedRequest, reqMu *sync.Mutex, stop func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	var mu sync.Mutex
	var reqs []capturedRequest

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer outW.Close()
		sc := bufio.NewScanner(inR)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
				continue
			}
			var params map[string]any
			_ = json.Unmarshal(req.Params, &params)
			mu.Lock()
			reqs = append(reqs, capturedRequest{Method: req.Method, Params: params})
			mu.Unlock()

			var result any
			switch req.Method {
			case "initialize":
				if initPayload != nil {
					result = initPayload
				} else {
					result = map[string]any{
						"protocolVersion":   1,
						"agentCapabilities": map[string]any{"loadSession": true},
					}
				}
			case "authenticate":
				result = map[string]any{}
			case "session/new":
				if newResult != nil {
					result = newResult
				} else {
					result = map[string]any{"sessionId": "new-sess-1"}
				}
			case "session/load":
				if loadResult != nil {
					result = loadResult
				} else {
					result = map[string]any{}
				}
			case "session/set_model":
				result = map[string]any{"_meta": map[string]any{"model": map[string]any{"Ok": "grok-4.6"}}}
			case "session/prompt":
				result = map[string]any{"stopReason": "end_turn"}
			default:
				result = map[string]any{}
			}
			resultJSON, _ := json.Marshal(result)
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"result":  json.RawMessage(resultJSON),
			})
			_, _ = outW.Write(append(resp, '\n'))
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	sess := &grokSession{
		agent:        &Agent{workDir: t.TempDir()},
		stdin:        inW,
		stdout:       outR,
		events:       make(chan core.Event, 64),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	sess.alive.Store(true)
	sess.sessionID.Store("sess-1")
	go sess.readLoop()

	stop = func() {
		_ = inW.Close()
		_ = inR.Close()
		cancel()
		<-done
	}
	return sess, &reqs, &mu, stop
}

func findRequest(reqs *[]capturedRequest, mu *sync.Mutex, method string) *capturedRequest {
	mu.Lock()
	defer mu.Unlock()
	for i := range *reqs {
		if (*reqs)[i].Method == method {
			return &(*reqs)[i]
		}
	}
	return nil
}

func requestCount(reqs *[]capturedRequest, mu *sync.Mutex, method string) int {
	mu.Lock()
	defer mu.Unlock()
	n := 0
	for i := range *reqs {
		if (*reqs)[i].Method == method {
			n++
		}
	}
	return n
}

// Real initialize `_meta.modelState` shape from grok 1.0.13 (2026-09-02
// probe, sanitized: owner's second entry kept as-is, no credentials involved).
func testModelStatePayload() map[string]any {
	return map[string]any{
		"protocolVersion": 1,
		"agentCapabilities": map[string]any{
			"loadSession": true,
		},
		"_meta": map[string]any{
			"modelState": map[string]any{
				"currentModelId": "grok-4.5",
				"availableModels": []map[string]any{
					{
						"modelId":      "grok-4.6",
						"name":         "Grok 4.6",
						"description":  "SpaceXAI's latest frontier model",
						"_meta": map[string]any{
							"totalContextTokens":      500000,
							"agentType":               "grok-build-plan",
							"supportsReasoningEffort": true,
							"reasoningEffort":         "high",
							"reasoningEfforts": []map[string]any{
								{"id": "xhigh", "value": "xhigh", "label": "Extra High Effort", "default": false},
								{"id": "high", "value": "high", "label": "High Effort", "default": true},
								{"id": "medium", "value": "medium", "label": "Medium Effort", "default": false},
								{"id": "low", "value": "low", "label": "Low Effort", "default": false},
							},
						},
					},
					{
						"modelId": "grok-4.5",
						"name":    "glm",
					},
				},
			},
		},
	}
}

func TestParseInitializeModelState(t *testing.T) {
	raw, _ := json.Marshal(testModelStatePayload()["_meta"])
	var m initializeMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal _meta: %v", err)
	}
	ms := m.ModelState
	if ms == nil {
		t.Fatal("modelState missing")
	}
	if ms.CurrentModelID != "grok-4.5" {
		t.Fatalf("currentModelId = %q", ms.CurrentModelID)
	}
	if len(ms.AvailableModels) != 2 {
		t.Fatalf("availableModels = %d", len(ms.AvailableModels))
	}
	g46 := ms.AvailableModels[0]
	if g46.ModelID != "grok-4.6" || g46.Name != "Grok 4.6" {
		t.Fatalf("entry0 = %+v", g46)
	}
	if g46.Meta == nil || !g46.Meta.SupportsReasoningEffort {
		t.Fatalf("entry0 meta effort gate missing: %+v", g46.Meta)
	}
	if len(g46.Meta.ReasoningEfforts) != 4 || g46.Meta.ReasoningEfforts[1].ID != "high" || !g46.Meta.ReasoningEfforts[1].Default {
		t.Fatalf("reasoningEfforts = %+v", g46.Meta.ReasoningEfforts)
	}
	// Effort-less model (owner's GLM entry) parses without meta.
	if ms.AvailableModels[1].Meta != nil {
		t.Fatalf("entry1 meta = %+v, want nil", ms.AvailableModels[1].Meta)
	}
}

func TestNewSessionSendsModelMetaOnlyWhenExplicit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		model       string
		effort      string
		wantPresent bool
		wantModel   string
		wantEffort  string
	}{
		{"explicit both", "grok-4.6", "low", true, "grok-4.6", "low"},
		{"model only", "grok-4.6", "", true, "grok-4.6", ""},
		{"effort only", "", "low", true, "", "low"},
		{"nothing explicit", "", "", false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, reqs, mu, stop := startModelProbeAgent(t, nil, nil, nil)
			defer stop()
			s.agent.SetModel(tc.model)
			// The bridge only calls SetReasoningEffort for non-empty params
			// (applySendMessageRuntimeOptions), so "no explicit effort" is
			// simply no call — never SetReasoningEffort(""), which the
			// normalizer would fold to "medium".
			if tc.effort != "" {
				s.agent.SetReasoningEffort(tc.effort)
			}

			if err := s.newSession(); err != nil {
				t.Fatalf("newSession: %v", err)
			}
			req := findRequest(reqs, mu, "session/new")
			if req == nil {
				t.Fatal("session/new not sent")
			}
			meta, ok := req.Params["_meta"].(map[string]any)
			if tc.wantPresent && !ok {
				t.Fatalf("session/new params missing _meta: %v", req.Params)
			}
			if !tc.wantPresent {
				if ok {
					t.Fatalf("session/new must not carry _meta when nothing explicit: %v", req.Params)
				}
				return
			}
			if got, _ := meta["modelId"].(string); got != tc.wantModel {
				t.Fatalf("_meta.modelId = %q, want %q", got, tc.wantModel)
			}
			if got, _ := meta["reasoningEffort"].(string); got != tc.wantEffort {
				t.Fatalf("_meta.reasoningEffort = %q, want %q", got, tc.wantEffort)
			}
		})
	}
}

func TestNewSessionRecordsAppliedModelState(t *testing.T) {
	// Real session/new result `models` shape (grok 1.0.13): selection applied
	// via _meta {modelId: grok-4.6, reasoningEffort: low} reflects back as
	// currentModelId=grok-4.6 and the entry's reasoningEffort=low.
	newResult := map[string]any{
		"sessionId": "new-sess-1",
		"models": map[string]any{
			"currentModelId": "grok-4.6",
			"availableModels": []map[string]any{
				{"modelId": "grok-4.6", "name": "Grok 4.6", "_meta": map[string]any{
					"supportsReasoningEffort": true,
					"reasoningEffort":         "low",
					"reasoningEfforts":        []map[string]any{{"id": "low", "value": "low", "default": false}},
				}},
			},
		},
	}
	s, reqs, mu, stop := startModelProbeAgent(t, nil, newResult, nil)
	defer stop()
	s.agent.SetModel("grok-4.6")
	s.agent.SetReasoningEffort("low")

	if err := s.newSession(); err != nil {
		t.Fatalf("newSession: %v", err)
	}
	if s.appliedModel != "grok-4.6" {
		t.Fatalf("appliedModel = %q", s.appliedModel)
	}
	if s.appliedEffort != "low" {
		t.Fatalf("appliedEffort = %q", s.appliedEffort)
	}
	// No drift → Send must NOT send an extra session/set_model before prompt.
	if err := s.Send("hi", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := requestCount(reqs, mu, "session/set_model"); n != 0 {
		t.Fatalf("session/set_model sent %d times, want 0 (no drift)", n)
	}
	if findRequest(reqs, mu, "session/prompt") == nil {
		t.Fatal("session/prompt not sent")
	}
}

func TestSendModelDriftTriggersSnakeCaseSetModel(t *testing.T) {
	s, reqs, mu, stop := startModelProbeAgent(t, nil, nil, nil)
	defer stop()
	s.sessionID.Store("sess-1")
	// Session is running grok-4.5 (loaded truth); the user now picks grok-4.6
	// with effort low.
	s.appliedModel = "grok-4.5"
	s.appliedEffort = "high"
	s.agent.SetModel("grok-4.6")
	s.agent.SetReasoningEffort("low")

	if err := s.Send("hi", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	req := findRequest(reqs, mu, "session/set_model")
	if req == nil {
		t.Fatal("session/set_model not sent for drifted selection")
	}
	if got, _ := req.Params["sessionId"].(string); got != "sess-1" {
		t.Fatalf("set_model sessionId = %v", req.Params["sessionId"])
	}
	if got, _ := req.Params["modelId"].(string); got != "grok-4.6" {
		t.Fatalf("set_model modelId = %v", req.Params["modelId"])
	}
	meta, _ := req.Params["_meta"].(map[string]any)
	if got, _ := meta["reasoningEffort"].(string); got != "low" {
		t.Fatalf("set_model _meta.reasoningEffort = %v", meta)
	}
	if s.appliedModel != "grok-4.6" || s.appliedEffort != "low" {
		t.Fatalf("applied not updated: %q/%q", s.appliedModel, s.appliedEffort)
	}
}

func TestEffortOnlyDriftResendsCurrentModel(t *testing.T) {
	s, reqs, mu, stop := startModelProbeAgent(t, nil, nil, nil)
	defer stop()
	s.appliedModel = "grok-4.5" // known truth from session/new|load models
	s.agent.SetReasoningEffort("low")

	if err := s.Send("hi", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	req := findRequest(reqs, mu, "session/set_model")
	if req == nil {
		t.Fatal("session/set_model not sent for effort-only drift")
	}
	// modelId is server-required; the effort-only switch must resend the
	// session's current model rather than an empty id.
	if got, _ := req.Params["modelId"].(string); got != "grok-4.5" {
		t.Fatalf("set_model modelId = %v, want current grok-4.5", req.Params["modelId"])
	}
}

func TestSendNoDriftNoSetModel(t *testing.T) {
	s, reqs, mu, stop := startModelProbeAgent(t, nil, nil, nil)
	defer stop()
	// Nothing explicit at all → never any set_model.
	if err := s.Send("hi", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := requestCount(reqs, mu, "session/set_model"); n != 0 {
		t.Fatalf("session/set_model sent %d times, want 0", n)
	}
}

func TestAdoptModelCatalogAndEffortSurfaces(t *testing.T) {
	a := &Agent{}

	// Before any catalog: provider fallback + nil efforts (existing behavior).
	a.SetProviders([]core.ProviderConfig{{Name: "p1", Models: []core.ModelOption{{Name: "m1", Desc: "M1"}}}})
	if ms := a.AvailableModels(context.Background()); len(ms) != 1 || ms[0].Name != "m1" {
		t.Fatalf("provider fallback = %+v", ms)
	}
	if eff := a.AvailableReasoningEfforts(); eff != nil {
		t.Fatalf("efforts before catalog = %v", eff)
	}

	// Empty state is ignored.
	a.adoptModelCatalog(&sessionModelState{})
	if a.modelCatalog != nil {
		t.Fatal("empty modelState must be ignored")
	}

	raw, _ := json.Marshal(testModelStatePayload()["_meta"])
	var m initializeMeta
	_ = json.Unmarshal(raw, &m)
	a.adoptModelCatalog(m.ModelState)

	// Catalog wins over provider models.
	ms := a.AvailableModels(context.Background())
	if len(ms) != 2 || ms[0].Name != "grok-4.6" || ms[0].Desc != "Grok 4.6" || ms[1].Name != "grok-4.5" {
		t.Fatalf("AvailableModels = %+v", ms)
	}

	// Per-model effort catalog: 4.6 has the menu with high default; 4.5 (GLM
	// entry, no meta) reports ok=false.
	efforts, def, ok := a.EffortsForModel(context.Background(), "grok-4.6")
	if !ok || len(efforts) != 4 || efforts[0] != "xhigh" || def != "high" {
		t.Fatalf("EffortsForModel(4.6) = %v/%s/%v", efforts, def, ok)
	}
	if _, _, ok := a.EffortsForModel(context.Background(), "grok-4.5"); ok {
		t.Fatal("EffortsForModel(4.5) must be ok=false (no declared efforts)")
	}
	if _, _, ok := a.EffortsForModel(context.Background(), "nope"); ok {
		t.Fatal("EffortsForModel(unknown) must be ok=false")
	}

	// Current model without explicit selection = catalog current (grok-4.5)
	// → no efforts advertised (honest: that model declares none).
	if eff := a.AvailableReasoningEfforts(); eff != nil {
		t.Fatalf("efforts for effort-less current = %v", eff)
	}
	// Explicit selection of the effort-capable model → its menu.
	a.SetModel("grok-4.6")
	if eff := a.AvailableReasoningEfforts(); len(eff) != 4 || eff[1] != "high" {
		t.Fatalf("efforts after selecting 4.6 = %v", eff)
	}
}

func TestLoadSessionAppliesExplicitSelectionAfterLoad(t *testing.T) {
	// session/load result restores the persisted selection (real sample:
	// currentModelId=grok-4.6, reasoningEffort=medium).
	loadResult := map[string]any{
		"models": map[string]any{
			"currentModelId": "grok-4.5",
			"availableModels": []map[string]any{
				{"modelId": "grok-4.5"},
			},
		},
	}
	s, reqs, mu, stop := startModelProbeAgent(t, nil, nil, loadResult)
	defer stop()
	s.agent.SetModel("grok-4.6")
	s.agent.SetReasoningEffort("low")

	if err := s.loadSession("sess-load-1"); err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	req := findRequest(reqs, mu, "session/set_model")
	if req == nil {
		t.Fatal("session/set_model not sent after load with explicit selection")
	}
	if got, _ := req.Params["modelId"].(string); got != "grok-4.6" {
		t.Fatalf("set_model modelId = %v", req.Params["modelId"])
	}
	if got, _ := req.Params["sessionId"].(string); got != "sess-load-1" {
		t.Fatalf("set_model sessionId = %v", req.Params["sessionId"])
	}
}

func TestLoadSessionWithoutExplicitSelectionSendsNoSetModel(t *testing.T) {
	s, reqs, mu, stop := startModelProbeAgent(t, nil, nil, nil)
	defer stop()
	if err := s.loadSession("sess-load-2"); err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if n := requestCount(reqs, mu, "session/set_model"); n != 0 {
		t.Fatalf("session/set_model sent %d times, want 0 (no explicit selection)", n)
	}
}

func TestInitializeAdoptsModelStateIntoAgent(t *testing.T) {
	s, _, _, stop := startModelProbeAgent(t, testModelStatePayload(), nil, nil)
	defer stop()
	if err := s.initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if s.agent.modelCatalog == nil || len(s.agent.modelCatalog.AvailableModels) != 2 {
		t.Fatalf("model catalog not adopted: %+v", s.agent.modelCatalog)
	}
	if ms := s.agent.AvailableModels(context.Background()); len(ms) != 2 || ms[0].Name != "grok-4.6" {
		t.Fatalf("AvailableModels after adopt = %+v", ms)
	}
}

// Slow-lane safety: the drift check must not hang when the fake responder is
// gone — applyModelSelection errors surface as a Send error (hard failure).
func TestSendSetModelFailureFailsTurn(t *testing.T) {
	s, _, _, stop := startModelProbeAgent(t, nil, nil, nil)
	s.appliedModel = "grok-4.5"
	s.agent.SetModel("grok-4.6")
	stop() // pipes closed: any set_model write/read fails

	err := s.Send("hi", nil, nil)
	if err == nil {
		t.Fatal("Send must fail when model selection cannot be applied")
	}
	if !strings.Contains(err.Error(), "apply model selection") {
		t.Fatalf("error = %v, want apply model selection failure", err)
	}
}
