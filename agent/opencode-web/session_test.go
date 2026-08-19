package opencodeweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const testProviderCatalog = `{"all":[{"id":"zhipuai-coding-plan","name":"Zhipu","models":{"glm-4.7":{"id":"glm-4.7","name":"GLM 4.7","limit":{"context":128000}},"glm-4.6":{"id":"glm-4.6","limit":{"context":64000}}}}],"default":{"zhipuai-coding-plan":"glm-4.7"},"connected":["zhipuai-coding-plan"]}`

func countRequests(s *recordingServe, method, pathPrefix string) []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []recordedRequest
	for _, req := range s.requests {
		if req.Method == method && strings.HasPrefix(req.Path, pathPrefix) {
			out = append(out, req)
		}
	}
	return out
}

func newSendAgent(t *testing.T, responses map[string]string) (*Agent, *recordingServe) {
	t.Helper()
	if _, ok := responses["/provider"]; !ok {
		responses["/provider"] = testProviderCatalog
	}
	return newDataAgent(t, responses, "/tmp/proj")
}

// withCreateRoute wires the POST /session create response on top of the
// GET /session list route the probe needs.
func withCreateRoute(s *recordingServe, createBody string) {
	s.methodResponses = map[string]string{"POST /session": createBody}
}

func TestSendCarriesCatalogModelOnCreateAndPrompt(t *testing.T) {
	agent, serve := newSendAgent(t, map[string]string{
		"/session/ses_new/prompt_async": `{}`,
	})
	withCreateRoute(serve, `{"id":"ses_new"}`)
	agent.SetModel("zhipuai-coding-plan/glm-4.7")

	sess, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	if err := sess.Send("hello", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}

	creates := countRequests(serve, "POST", "/session")
	var create, prompt recordedRequest
	for _, req := range creates {
		if strings.HasSuffix(req.Path, "/prompt_async") {
			prompt = req
		} else if req.Path == "/session" {
			create = req
		}
	}
	if create.Path == "" {
		t.Fatal("create POST /session missing")
	}
	if !strings.Contains(create.Body, `"directory":"/tmp/proj"`) {
		t.Fatalf("create body must carry directory, got %s", create.Body)
	}
	if !strings.Contains(create.Body, `"id":"glm-4.7"`) || !strings.Contains(create.Body, `"providerID":"zhipuai-coding-plan"`) {
		t.Fatalf("create body must carry the catalog model, got %s", create.Body)
	}
	if create.Directory != "/tmp/proj" {
		t.Fatalf("create must send x-opencode-directory header, got %q", create.Directory)
	}
	if prompt.Path == "" {
		t.Fatal("prompt POST missing")
	}
	if !strings.Contains(prompt.Body, `"model"`) || !strings.Contains(prompt.Body, `"modelID":"glm-4.7"`) || !strings.Contains(prompt.Body, `"providerID":"zhipuai-coding-plan"`) {
		t.Fatalf("prompt body must carry model {modelID, providerID} (live-pinned 1.18), got %s", prompt.Body)
	}
	if !strings.Contains(prompt.Body, `"parts"`) || !strings.Contains(prompt.Body, "hello") {
		t.Fatalf("prompt body must carry text parts, got %s", prompt.Body)
	}
	if sess.CurrentSessionID() != "ses_new" {
		t.Fatalf("session id = %q, want ses_new", sess.CurrentSessionID())
	}
}

func TestSendCatalogGateIsZeroPOST(t *testing.T) {
	agent, serve := newSendAgent(t, map[string]string{
		"/provider": `{"all":[{"id":"other-provider","models":{"other-model":{"id":"other-model","limit":{"context":1000}}}}],"connected":["other-provider"]}`,
	})
	agent.SetModel("zhipuai-coding-plan/glm-9.9")

	sess, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	err = sess.Send("hello", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not in the server's provider catalog") {
		t.Fatalf("expected catalog gate error, got %v", err)
	}
	if posts := countRequests(serve, "POST", "/session"); len(posts) != 0 {
		t.Fatalf("catalog gate must yield ZERO POSTs, got %+v", posts)
	}
}

// Official picker semantics (prompt-model-selection.ts): with no explicit
// selection, the FIRST connected provider's default model rides the send —
// never an invented id.
func TestSendFallsBackToConnectedDefaultModel(t *testing.T) {
	agent, serve := newSendAgent(t, map[string]string{
		"/session/ses_new/prompt_async": `{}`,
	})
	withCreateRoute(serve, `{"id":"ses_new"}`)
	// No SetModel, no resume id — fallback must pick zhipuai-coding-plan's
	// default glm-4.7 from the envelope `default` map.
	sess, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	if err := sess.Send("hello", nil, nil); err != nil {
		t.Fatalf("Send via fallback: %v", err)
	}
	prompts := countRequests(serve, "POST", "/session/ses_new/prompt_async")
	if len(prompts) != 1 || !strings.Contains(prompts[0].Body, `"modelID":"glm-4.7"`) {
		t.Fatalf("fallback must send the connected default glm-4.7, got %+v", prompts)
	}
}

// Empty connected catalog = nothing usable: honest error, zero POST.
func TestSendFailsWhenConnectedCatalogEmpty(t *testing.T) {
	agent, serve := newSendAgent(t, map[string]string{
		"/provider": `{"all":[{"id":"other-provider","models":{"other-model":{"id":"other-model","limit":{"context":1000}}}}],"connected":[]}`,
	})
	sess, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	err = sess.Send("hello", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "connected provider catalog is empty") {
		t.Fatalf("expected honest empty-catalog error, got %v", err)
	}
	if posts := countRequests(serve, "POST", "/session"); len(posts) != 0 {
		t.Fatalf("empty-catalog send must yield ZERO POSTs, got %+v", posts)
	}
}

func TestSendRejectsAttachmentsLoudly(t *testing.T) {
	agent, serve := newSendAgent(t, map[string]string{})
	agent.SetModel("zhipuai-coding-plan/glm-4.7")
	sess, _ := agent.StartSession(context.Background(), "")
	defer sess.Close()
	err := sess.Send("see this", []core.ImageAttachment{{Data: []byte("x")}}, nil)
	if err == nil || !strings.Contains(err.Error(), "attachments are not supported") {
		t.Fatalf("image attachment must fail loudly, got %v", err)
	}
	err = sess.Send("see this", nil, []core.FileAttachment{{FileName: "a.txt", Data: []byte("x")}})
	if err == nil || !strings.Contains(err.Error(), "attachments are not supported") {
		t.Fatalf("file attachment must fail loudly, got %v", err)
	}
	if posts := countRequests(serve, "POST", "/session"); len(posts) != 0 {
		t.Fatalf("attachment rejection must yield ZERO POSTs, got %+v", posts)
	}
}

// routeResponse is one canned route for the 4xx-passthrough mux.
type routeResponse struct {
	status int
	body   string
}

// newMuxWithStatuses builds an authed mux with per-route status codes; keys
// are "METHOD /path" (a bare "/path" matches any method); event streams hang
// open like the real serve.
func newMuxWithStatuses(t *testing.T, routes map[string]routeResponse) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/event") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		route, found := routes[r.Method+" "+r.URL.Path]
		if !found {
			route, found = routes[r.URL.Path]
		}
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(route.status)
		_, _ = w.Write([]byte(route.body))
	})
	return mux
}

func agentAgainstMux(t *testing.T, mux *http.ServeMux) *Agent {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	a, err := New(map[string]any{
		"opencode_web_url":  srv.URL,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a.(*Agent)
}

func TestSendHTTPErrorSurfacesBody(t *testing.T) {
	mux := newMuxWithStatuses(t, map[string]routeResponse{
		"/global/health":                {200, `{"healthy":true}`},
		"GET /session":                  {200, `[]`},
		"POST /session":                 {200, `{"id":"ses_new"}`},
		"/session/ses_new/prompt_async": {400, "model glm-9.9 not available on this provider"},
		"/provider":                     {200, testProviderCatalog},
	})
	agent := agentAgainstMux(t, mux)
	agent.SetModel("zhipuai-coding-plan/glm-4.7")
	sess, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	err = sess.Send("hello", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "not available on this provider") {
		t.Fatalf("4xx body must flow verbatim into the send error, got %v", err)
	}
}

func TestResumeAdoptsServeSessionModel(t *testing.T) {
	agent, serve := newSendAgent(t, map[string]string{
		"/session/ses_x":              `{"id":"ses_x","directory":"/tmp/proj","model":{"id":"glm-4.7","providerID":"zhipuai-coding-plan"}}`,
		"/session/ses_x/prompt_async": `{"messageID":"msg_1"}`,
	})
	sess, err := agent.StartSession(context.Background(), "ses_x")
	if err != nil {
		t.Fatalf("StartSession resume: %v", err)
	}
	defer sess.Close()
	if err := sess.Send("continue", nil, nil); err != nil {
		t.Fatalf("Send with adopted model: %v", err)
	}
	prompts := countRequests(serve, "POST", "/session/ses_x/prompt_async")
	if len(prompts) != 1 || !strings.Contains(prompts[0].Body, `"modelID":"glm-4.7"`) {
		t.Fatalf("resume send must carry the serve session model (modelID key), got %+v", prompts)
	}
	if prompts[0].Directory != "/tmp/proj" {
		t.Fatalf("resume send must use the session's own directory header, got %q", prompts[0].Directory)
	}
	var creates []recordedRequest
	for _, req := range countRequests(serve, "POST", "/session") {
		if req.Path == "/session" {
			creates = append(creates, req)
		}
	}
	if len(creates) != 0 {
		t.Fatalf("resume must not create a new session, got %+v", creates)
	}
}

func TestCancelTurnByGeneration(t *testing.T) {
	agent, serve := newSendAgent(t, map[string]string{
		"/session/ses_x":       `{"id":"ses_x","model":{"id":"glm-4.7","providerID":"zhipuai-coding-plan"}}`,
		"/session/ses_x/abort": `{}`,
	})
	sess, err := agent.StartSession(context.Background(), "ses_x")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	canceler, ok := sess.(core.TurnCanceler)
	if !ok {
		t.Fatal("session must implement core.TurnCanceler")
	}
	if err := canceler.CancelTurn(context.Background()); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	aborts := countRequests(serve, "POST", "/session/ses_x/abort")
	if len(aborts) != 1 {
		t.Fatalf("1.18 cancel must POST /session/:id/abort, got %+v", aborts)
	}
	// Close must NOT abort (design §4.3.4: Close only tears the SSE binding).
	before := len(countRequests(serve, "POST", "/session/ses_x/abort"))
	_ = sess.Close()
	after := len(countRequests(serve, "POST", "/session/ses_x/abort"))
	if after != before {
		t.Fatalf("Close must not abort the turn (%d → %d)", before, after)
	}
}

func TestCancelTurnV2Interrupt(t *testing.T) {
	// v2 endpoint: only /api/session/:id/interrupt is mapped — a wrong path
	// (e.g. the 1.18 abort route) would 404 and fail CancelTurn.
	mux := newMuxWithStatuses(t, map[string]routeResponse{
		"/global/health":               {404, ``},
		"/api/health":                  {200, `{"healthy":true}`},
		"/api/session":                 {200, `{"data":[]}`},
		"/provider":                    {200, testProviderCatalog},
		"/session/ses_x":               {200, `{"id":"ses_x","model":{"id":"glm-4.7","providerID":"zhipuai-coding-plan"}}`},
		"/api/session/ses_x/interrupt": {200, `{}`},
	})
	agent := agentAgainstMux(t, mux)
	if c, err := agent.clientFor(context.Background()); err != nil || c.Generation() != generationV2 {
		t.Fatalf("generation must resolve to v2, got %v err=%v", c.Generation(), err)
	}
	sess, err := agent.StartSession(context.Background(), "ses_x")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()
	canceler := sess.(core.TurnCanceler)
	if err := canceler.CancelTurn(context.Background()); err != nil {
		t.Fatalf("CancelTurn on v2 must POST /api/session/:id/interrupt: %v", err)
	}
}

func TestQuestionsNotSupported(t *testing.T) {
	agent, _ := newSendAgent(t, map[string]string{})
	sess, _ := agent.StartSession(context.Background(), "ses_x")
	defer sess.Close()
	if err := sess.RespondQuestion("q1", []string{"a"}); err != core.ErrNotSupported {
		t.Fatalf("RespondQuestion must be core.ErrNotSupported, got %v", err)
	}
	if err := sess.RejectQuestion("q1"); err != core.ErrNotSupported {
		t.Fatalf("RejectQuestion must be core.ErrNotSupported, got %v", err)
	}
}
