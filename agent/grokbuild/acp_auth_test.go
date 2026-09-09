package grokbuild

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestSelectEagerAuthMethod_PrefersDefaultWhenAdvertised(t *testing.T) {
	methods := []authMethod{
		{ID: acpAuthMethodXAIAPIKey, Name: "xai.api_key"},
		{ID: acpAuthMethodCachedToken, Name: "cached_token"},
		{ID: "grok.com", Name: "grok.com"},
	}
	got := selectEagerAuthMethod(methods, acpAuthMethodCachedToken)
	if got != acpAuthMethodCachedToken {
		t.Fatalf("got %q, want cached_token (official pager defaultAuthMethodId)", got)
	}
}

func TestSelectEagerAuthMethod_IgnoresDefaultMissingFromList(t *testing.T) {
	methods := []authMethod{
		{ID: acpAuthMethodXAIAPIKey, Name: "xai.api_key"},
		{ID: acpAuthMethodCachedToken, Name: "cached_token"},
	}
	got := selectEagerAuthMethod(methods, "oidc")
	if got != acpAuthMethodCachedToken {
		t.Fatalf("got %q, want cached_token fallback when default is not advertised", got)
	}
}

func TestSelectEagerAuthMethod_CachedTokenBeatsFirstAPIKey(t *testing.T) {
	// This is the live 1.0.24 BYOK shape: xai.api_key first because a custom
	// model has its own api_key, but the user session is OIDC cached_token.
	methods := []authMethod{
		{ID: acpAuthMethodXAIAPIKey, Name: "xai.api_key"},
		{ID: acpAuthMethodCachedToken, Name: "cached_token"},
		{ID: "grok.com", Name: "Sign in with grok.com"},
	}
	got := selectEagerAuthMethod(methods, "")
	if got != acpAuthMethodCachedToken {
		t.Fatalf("got %q, want cached_token; methods[0] would send an empty API-key bearer", got)
	}
}

func TestSelectEagerAuthMethod_FallsBackToFirst(t *testing.T) {
	methods := []authMethod{
		{ID: acpAuthMethodXAIAPIKey, Name: "xai.api_key"},
		{ID: "grok.com", Name: "grok.com"},
	}
	got := selectEagerAuthMethod(methods, "")
	if got != acpAuthMethodXAIAPIKey {
		t.Fatalf("got %q, want xai.api_key when no cached_token is advertised", got)
	}
}

func TestSelectEagerAuthMethod_Empty(t *testing.T) {
	if got := selectEagerAuthMethod(nil, acpAuthMethodCachedToken); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestInitialize_AuthenticatesCachedTokenWhenAPIKeyIsFirst(t *testing.T) {
	// Live grok 1.0.24 shape with a BYOK custom model: xai.api_key is
	// advertised first, defaultAuthMethodId is cached_token. Pre-fix
	// initialize() called authMethods[0] and the sampler sent an empty bearer.
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	t.Cleanup(func() {
		_ = inW.Close()
		_ = inR.Close()
		_ = outW.Close()
		_ = outR.Close()
	})

	var gotMethod string
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
			var result any
			switch req.Method {
			case "initialize":
				result = map[string]any{
					"protocolVersion":   1,
					"agentCapabilities": map[string]any{"loadSession": true},
					"authMethods": []map[string]any{
						{"id": acpAuthMethodXAIAPIKey, "name": "xai.api_key"},
						{"id": acpAuthMethodCachedToken, "name": "cached_token"},
						{"id": "grok.com", "name": "grok.com"},
					},
					"_meta": map[string]any{"defaultAuthMethodId": acpAuthMethodCachedToken},
				}
			case "authenticate":
				var p authenticateParams
				_ = json.Unmarshal(req.Params, &p)
				gotMethod = p.MethodID
				result = map[string]any{}
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
	defer cancel()
	s := &grokSession{
		stdin:        inW,
		stdout:       outR,
		events:       make(chan core.Event, 16),
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		pendingPerms: make(map[string][]permissionOption),
		respChannels: make(map[int]chan *jsonrpcResponse),
	}
	s.alive.Store(true)
	go s.readLoop()

	if err := s.initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	_ = inW.Close()
	<-done
	if gotMethod != acpAuthMethodCachedToken {
		t.Fatalf("authenticate methodId=%q, want cached_token (not xai.api_key first)", gotMethod)
	}
}

func TestInitializeResultEagerAuthMethodIDFromMeta(t *testing.T) {
	raw := []byte(`{
		"protocolVersion": 1,
		"authMethods": [
			{"id": "xai.api_key", "name": "xai.api_key"},
			{"id": "cached_token", "name": "cached_token"},
			{"id": "grok.com", "name": "grok.com"}
		],
		"_meta": {"defaultAuthMethodId": "cached_token"}
	}`)
	var init initializeResult
	if err := json.Unmarshal(raw, &init); err != nil {
		t.Fatal(err)
	}
	if init.eagerAuthMethodID() != acpAuthMethodCachedToken {
		t.Fatalf("got %q, want cached_token from _meta.defaultAuthMethodId", init.eagerAuthMethodID())
	}
}
