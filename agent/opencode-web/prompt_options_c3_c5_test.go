package opencodeweb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// prompt_options_c3_c5_test.go owns the C3 (prompt options/messageID/parts)
// and C5 (§6.6 selection chain) boundaries. Fixtures replay the E1b/E4b/E5b
// physical shapes: /provider {all,default,connected} with models.echo.variants
// {high,low}, /config with optional model string, /agent bare array, and the
// prompt_async body {messageID, agent, model{providerID,modelID}, parts,
// variant?}.

// e5bProviderFixture is the E4b/E5b-proven envelope: localmock connected,
// catalog order alpha/echo/zeta (stable sort), provider default zeta,
// variants only on echo.
const e5bProviderFixture = `{
  "all": [{"id": "localmock", "name": "Local Mock", "models": {
    "alpha": {"id": "alpha", "name": "Alpha"},
    "echo":  {"id": "echo",  "name": "Echo", "variants": {"high": {"reasoning": {"effort": "high"}}, "low": {"reasoning": {"effort": "low"}}}},
    "zeta":  {"id": "zeta",  "name": "Zeta"}
  }}],
  "default": {"localmock": "zeta"},
  "connected": ["localmock"]
}`

const e5bAgentsFixture = `[{"name":"build","mode":"primary","native":true,"description":"d"},{"name":"planner","mode":"primary","native":false,"description":"p"}]`

// newC3C5Agent boots a send-capable agent over the E5b-shaped serve.
func newC3C5Agent(t *testing.T, responses map[string]string) (*Agent, *recordingServe) {
	t.Helper()
	if _, ok := responses["/provider"]; !ok {
		responses["/provider"] = e5bProviderFixture
	}
	if _, ok := responses["/agent"]; !ok {
		responses["/agent"] = e5bAgentsFixture
	}
	if _, ok := responses["/session/ses_new/prompt_async"]; !ok {
		responses["/session/ses_new/prompt_async"] = `{}`
	}
	agent, serve := newSendAgent(t, responses)
	withCreateRoute(serve, `{"id":"ses_new"}`)
	return agent, serve
}

// promptBodies returns the parsed prompt_async request bodies.
func promptBodies(t *testing.T, s *recordingServe) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, req := range countRequests(s, "POST", "/session") {
		if !strings.HasSuffix(req.Path, "/prompt_async") {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
			t.Fatalf("prompt body not JSON: %s (%v)", req.Body, err)
		}
		out = append(out, body)
	}
	return out
}

func bodyModel(body map[string]any) (providerID, modelID string) {
	m, _ := body["model"].(map[string]any)
	providerID, _ = m["providerID"].(string)
	modelID, _ = m["modelID"].(string)
	return providerID, modelID
}

// TestPromptBodyCarriesMessageIDAgentModelParts pins the §6.4 verified
// request shape: Mac-generated-once stable messageID (msg_…, distinct per
// send), resolved agent, validated {providerID,modelID}, and text parts.
func TestPromptBodyCarriesMessageIDAgentModelParts(t *testing.T) {
	agent, serve := newC3C5Agent(t, map[string]string{})
	sess, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	sender, ok := sess.(core.PromptOptionsSender)
	if !ok {
		t.Fatal("serverSession must implement core.PromptOptionsSender")
	}
	if err := sender.SendWithOptions("first", nil, nil, core.PromptOptions{Agent: "build", ProviderID: "localmock", ModelID: "echo", Variant: "high"}); err != nil {
		t.Fatalf("send 1: %v", err)
	}
	if err := sender.SendWithOptions("second", nil, nil, core.PromptOptions{Agent: "build", ProviderID: "localmock", ModelID: "echo"}); err != nil {
		t.Fatalf("send 2: %v", err)
	}
	bodies := promptBodies(t, serve)
	if len(bodies) != 2 {
		t.Fatalf("two prompt POSTs expected, got %d", len(bodies))
	}
	ids := map[string]bool{}
	for i, body := range bodies {
		mid, _ := body["messageID"].(string)
		if !strings.HasPrefix(mid, "msg_") || len(mid) <= len("msg_")+8 {
			t.Fatalf("body %d: messageID must be a Mac-generated msg_ id, got %q", i, mid)
		}
		if ids[mid] {
			t.Fatalf("messageID %q reused across sends", mid)
		}
		ids[mid] = true
		if agent, _ := body["agent"].(string); agent != "build" {
			t.Fatalf("body %d: agent = %q, want build", i, agent)
		}
		if p, m := bodyModel(body); p != "localmock" || m != "echo" {
			t.Fatalf("body %d: model = %s/%s, want localmock/echo", i, p, m)
		}
		parts, _ := body["parts"].([]any)
		if len(parts) != 1 {
			t.Fatalf("body %d: parts = %v", i, parts)
		}
	}
	// Selected variant rides body 1 top-level; unset omits the field (E1b).
	if v, _ := bodies[0]["variant"].(string); v != "high" {
		t.Fatalf("body 0: variant = %v, want high", bodies[0]["variant"])
	}
	if _, has := bodies[1]["variant"]; has {
		t.Fatalf("body 1: unset variant must be omitted, got %v", bodies[1]["variant"])
	}
}

// TestVariantOnlyLiveKeysZeroPOST: a variant not declared by the SELECTED
// model's live catalog keys is rejected before any POST (E1b rule).
func TestVariantOnlyLiveKeysZeroPOST(t *testing.T) {
	agent, serve := newC3C5Agent(t, map[string]string{})
	sess, _ := agent.StartSession(context.Background(), "")
	defer sess.Close()
	sender := sess.(core.PromptOptionsSender)

	cases := []struct{ model, variant string }{
		{"echo", "turbo"},  // not a live key of echo
		{"alpha", "high"},  // alpha declares NO variants at all
		{"alpha", "low"},   // variant of a DIFFERENT model
	}
	for _, tc := range cases {
		err := sender.SendWithOptions("x", nil, nil, core.PromptOptions{ProviderID: "localmock", ModelID: tc.model, Variant: tc.variant})
		if err == nil || !strings.Contains(err.Error(), "not a live key") {
			t.Fatalf("variant %s/%s must fail closed, got %v", tc.model, tc.variant, err)
		}
	}
	if posts := countRequests(serve, "POST", "/session"); len(posts) != 0 {
		t.Fatalf("unlisted variant must yield ZERO POSTs, got %+v", posts)
	}
	// A live key of the selected model is admitted.
	if err := sender.SendWithOptions("x", nil, nil, core.PromptOptions{ProviderID: "localmock", ModelID: "echo", Variant: "low"}); err != nil {
		t.Fatalf("live variant low must be admitted: %v", err)
	}
	bodies := promptBodies(t, serve)
	if len(bodies) != 1 || bodies[0]["variant"] != "low" {
		t.Fatalf("admitted prompt must carry variant low, got %+v", bodies)
	}
}

// TestUnavailableAgentZeroPOST: an agent not in the live registry is a
// zero-POST error — never a silent fallback to another agent.
func TestUnavailableAgentZeroPOST(t *testing.T) {
	agent, serve := newC3C5Agent(t, map[string]string{})
	sess, _ := agent.StartSession(context.Background(), "")
	defer sess.Close()
	sender := sess.(core.PromptOptionsSender)
	err := sender.SendWithOptions("x", nil, nil, core.PromptOptions{Agent: "ghost"})
	if err == nil || !strings.Contains(err.Error(), "not in the server's agent registry") {
		t.Fatalf("unknown agent must fail closed, got %v", err)
	}
	if posts := countRequests(serve, "POST", "/session"); len(posts) != 0 {
		t.Fatalf("unknown agent must yield ZERO POSTs, got %+v", posts)
	}
}

// TestSelectionChainLevels walks every §6.6 level with a distinct fixture
// (C5 owning test): current → agent model → provider-default-over-config →
// config-when-no-provider-default → recent → provider-default fallback →
// catalog-first fallback.
func TestSelectionChainLevels(t *testing.T) {
	t.Run("level1-current-wins", func(t *testing.T) {
		agent, serve := newC3C5Agent(t, map[string]string{
			"/config": `{"model":"localmock/alpha"}`,
		})
		sess, _ := agent.StartSession(context.Background(), "")
		defer sess.Close()
		sender := sess.(core.PromptOptionsSender)
		if err := sender.SendWithOptions("x", nil, nil, core.PromptOptions{ProviderID: "localmock", ModelID: "alpha"}); err != nil {
			t.Fatalf("send: %v", err)
		}
		bodies := promptBodies(t, serve)
		if p, m := bodyModel(bodies[0]); p != "localmock" || m != "alpha" {
			t.Fatalf("explicit current selection must win over zeta/alpha defaults, got %s/%s", p, m)
		}
	})

	t.Run("level2-agent-model", func(t *testing.T) {
		// planner pins a model; no explicit selection → agent model rides.
		agent, serve := newC3C5Agent(t, map[string]string{
			"/agent": `[{"name":"build","mode":"primary","native":true},{"name":"planner","mode":"primary","model":"localmock/alpha"}]`,
			"/config": `{"model":"localmock/alpha"}`,
		})
		sess, _ := agent.StartSession(context.Background(), "")
		defer sess.Close()
		sender := sess.(core.PromptOptionsSender)
		if err := sender.SendWithOptions("x", nil, nil, core.PromptOptions{Agent: "planner"}); err != nil {
			t.Fatalf("send: %v", err)
		}
		bodies := promptBodies(t, serve)
		if p, m := bodyModel(bodies[0]); m != "alpha" {
			t.Fatalf("agent-configured model must win over provider default zeta, got %s/%s", p, m)
		}
	})

	t.Run("level3-provider-default-over-config", func(t *testing.T) {
		// E5b decisive case: /provider.default= zeta beats /config.model=alpha.
		agent, serve := newC3C5Agent(t, map[string]string{
			"/config": `{"model":"localmock/alpha"}`,
		})
		sess, _ := agent.StartSession(context.Background(), "")
		defer sess.Close()
		sender := sess.(core.PromptOptionsSender)
		if err := sender.SendWithOptions("x", nil, nil, core.PromptOptions{Agent: "build"}); err != nil {
			t.Fatalf("send: %v", err)
		}
		bodies := promptBodies(t, serve)
		if p, m := bodyModel(bodies[0]); m != "zeta" {
			t.Fatalf("provider default zeta must win over config alpha (E5b), got %s/%s", p, m)
		}
	})

	t.Run("level3b-config-when-no-provider-default", func(t *testing.T) {
		// No /provider default → legacy /config.model decides.
		agent, serve := newC3C5Agent(t, map[string]string{
			"/provider": `{"all":[{"id":"localmock","models":{"alpha":{"id":"alpha"},"echo":{"id":"echo"},"zeta":{"id":"zeta"}}}],"connected":["localmock"]}`,
			"/config":   `{"model":"localmock/alpha"}`,
		})
		sess, _ := agent.StartSession(context.Background(), "")
		defer sess.Close()
		sender := sess.(core.PromptOptionsSender)
		if err := sender.SendWithOptions("x", nil, nil, core.PromptOptions{Agent: "build"}); err != nil {
			t.Fatalf("send: %v", err)
		}
		bodies := promptBodies(t, serve)
		if _, m := bodyModel(bodies[0]); m != "alpha" {
			t.Fatalf("absent provider default must fall to config alpha, got %s", m)
		}
	})

	t.Run("level4-recent-session-model", func(t *testing.T) {
		// No explicit, no agent model, no provider default, no /config route:
		// the resume-adopted server session model (recent) rides the send.
		agent, serve := newC3C5Agent(t, map[string]string{
			"/provider":    `{"all":[{"id":"localmock","models":{"alpha":{"id":"alpha"},"echo":{"id":"echo"},"zeta":{"id":"zeta"}}}],"connected":["localmock"]}`,
			"/session/ses_x":              `{"id":"ses_x","directory":"/tmp/proj","model":{"id":"echo","providerID":"localmock"}}`,
			"/session/ses_x/prompt_async": `{}`,
		})
		sess, err := agent.StartSession(context.Background(), "ses_x")
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		defer sess.Close()
		if err := sess.Send("x", nil, nil); err != nil {
			t.Fatalf("send: %v", err)
		}
		bodies := promptBodies(t, serve)
		if _, m := bodyModel(bodies[0]); m != "echo" {
			t.Fatalf("recent session model echo must ride the send, got %s", m)
		}
	})

	t.Run("level5-provider-default-fallback", func(t *testing.T) {
		// No default in envelope: first connected provider's DEFAULT is used
		// when present (this sub-fixture keeps zeta), else its first catalog
		// model — both exercised below.
		agent, serve := newC3C5Agent(t, map[string]string{})
		sess, _ := agent.StartSession(context.Background(), "")
		defer sess.Close()
		if err := sess.Send("x", nil, nil); err != nil {
			t.Fatalf("send: %v", err)
		}
		bodies := promptBodies(t, serve)
		if _, m := bodyModel(bodies[0]); m != "zeta" {
			t.Fatalf("fallback must take the first connected provider's default zeta, got %s", m)
		}
	})

	t.Run("level5b-catalog-first-fallback", func(t *testing.T) {
		// No default anywhere: the provider's first catalog model (stable
		// sorted → alpha) is the fallback.
		agent, serve := newC3C5Agent(t, map[string]string{
			"/provider": `{"all":[{"id":"localmock","models":{"alpha":{"id":"alpha"},"echo":{"id":"echo"},"zeta":{"id":"zeta"}}}],"connected":["localmock"]}`,
		})
		sess, _ := agent.StartSession(context.Background(), "")
		defer sess.Close()
		if err := sess.Send("x", nil, nil); err != nil {
			t.Fatalf("send: %v", err)
		}
		bodies := promptBodies(t, serve)
		if _, m := bodyModel(bodies[0]); m != "alpha" {
			t.Fatalf("catalog-first fallback must be alpha, got %s", m)
		}
	})
}

// TestNoValidModelZeroPOST: an empty connected set means no level can
// validate — the honest zero-POST error.
func TestNoValidModelZeroPOST(t *testing.T) {
	agent, serve := newC3C5Agent(t, map[string]string{
		"/provider": `{"all":[{"id":"localmock","models":{"alpha":{"id":"alpha"}}}],"connected":[]}`,
		"/config":   `{"model":"localmock/alpha"}`,
	})
	sess, _ := agent.StartSession(context.Background(), "")
	defer sess.Close()
	err := sess.Send("x", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no connected valid model") {
		t.Fatalf("expected honest no-valid-model error, got %v", err)
	}
	if posts := countRequests(serve, "POST", "/session"); len(posts) != 0 {
		t.Fatalf("no-valid-model must yield ZERO POSTs, got %+v", posts)
	}
}

// TestConfigShapeErrorFailsClosed: a /config that ANSWERS with an unverified
// shape is a strict-decode failure (zero POSTs) — never a guess; a transport
// failure merely skips the level (official picker parity). /config is only
// consulted when the provider default is absent (resolveDefaultModel order).
func TestConfigShapeErrorFailsClosed(t *testing.T) {
	agent, serve := newC3C5Agent(t, map[string]string{
		"/provider": `{"all":[{"id":"localmock","models":{"alpha":{"id":"alpha"},"zeta":{"id":"zeta"}}}],"connected":["localmock"]}`,
		"/config":   `["not","an","object"]`,
	})
	sess, _ := agent.StartSession(context.Background(), "")
	defer sess.Close()
	err := sess.Send("x", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "config payload must be an object") {
		t.Fatalf("config shape error must fail the send, got %v", err)
	}
	if posts := countRequests(serve, "POST", "/session"); len(posts) != 0 {
		t.Fatalf("config shape error must yield ZERO POSTs, got %+v", posts)
	}
}

// TestCatalogVariantsExposeLiveKeysOnly: list_models surfaces exactly the
// live variant keys of the model that declares them (E1b) and nothing for
// models without variants.
func TestCatalogVariantsExposeLiveKeysOnly(t *testing.T) {
	agent, _ := newC3C5Agent(t, map[string]string{})
	models := agent.AvailableModels(context.Background())
	saw := map[string][]string{}
	for _, m := range models {
		saw[m.Name] = m.Variants
	}
	if len(saw["localmock/echo"]) != 2 {
		t.Fatalf("echo must expose exactly its live keys {high,low}, got %v", saw["localmock/echo"])
	}
	has := map[string]bool{}
	for _, k := range saw["localmock/echo"] {
		has[k] = true
	}
	if !has["high"] || !has["low"] {
		t.Fatalf("echo variants = %v, want high+low", saw["localmock/echo"])
	}
	if len(saw["localmock/alpha"]) != 0 || len(saw["localmock/zeta"]) != 0 {
		t.Fatalf("models without variants must expose none, got alpha=%v zeta=%v", saw["localmock/alpha"], saw["localmock/zeta"])
	}
}

// TestAdmissionWritesNothing: HTTP 204 admission alone produces zero timeline
// evidence — the session's event channel receives nothing synthetic.
func TestAdmissionWritesNothing(t *testing.T) {
	agent, serve := newC3C5Agent(t, map[string]string{
		"/session/ses_new/prompt_async": ``,
	})
	// 204-style admission: empty body with a 2xx code is what the recording
	// serve returns; the send must treat it as admission only.
	sess, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	if err := sess.Send("hello", nil, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	// Drain any events for a moment — admission itself must not emit.
	select {
	case ev := <-sess.Events():
		t.Fatalf("admission must not synthesize timeline events, got %+v", ev)
	default:
	}
	if posts := countRequests(serve, "POST", "/session"); len(posts) != 2 { // create + prompt
		t.Fatalf("exactly create+prompt POSTs expected, got %+v", posts)
	}
}
