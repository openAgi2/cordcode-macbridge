package opencodeweb

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// official_shapes_test.go：官方调用形状契约（owner 质询「如何证明其他地方没有
// 犯相同的错误」的机制化回答）。每一行断言都钉着官方 v1 SDK 的生成类型
// （~/Projects/opencode/packages/sdk/js/src/gen/types.gen.ts）——今后任何调用
// 形状漂移（路径/查询参数/body 键集），这些测试直接红。
//
// SDK 类型依据（2026-08-19 抽取）：
//   SessionListData    GET  /session              query{directory?}
//   SessionStatusData  GET  /session/status       query{directory?}
//   SessionGetData     GET  /session/{id}         query{directory?}
//   SessionCreateData  POST /session              query{directory?}  body{parentID?,title?}
//   SessionPromptData  POST /session/{id}/message query{directory?} body{parts,model{providerID,modelID},…}
//   SessionPromptAsyncData POST /session/{id}/prompt_async 同上
//   SessionMessagesData GET /session/{id}/message query{directory?,limit?}
//   SessionAbortData   POST /session/{id}/abort   query{directory?}
//   PostSessionIdPermissionsPermissionIdData
//                      POST /session/{id}/permissions/{permissionID}
//                                            body{"response":"once"|"always"|"reject"}
//   ProviderListData   GET  /provider            query{directory?}
//   ProjectListData    GET  /project             query{directory?}

func lastRequestFor(t *testing.T, serve *recordingServe, path string) recordedRequest {
	t.Helper()
	reqs := serve.requestsFor(path)
	if len(reqs) == 0 {
		t.Fatalf("no request recorded for %s", path)
	}
	return reqs[len(reqs)-1]
}

func directoryQuery(t *testing.T, req recordedRequest, wantDir string) {
	t.Helper()
	got := req.Query
	if got == "" {
		t.Fatalf("%s %s: expected ?directory= query (official SDK shape), got none", req.Method, req.Path)
	}
	values, err := url.ParseQuery(got)
	if err != nil {
		t.Fatalf("%s %s: unparseable query %q", req.Method, req.Path, got)
	}
	if values.Get("directory") != wantDir {
		t.Fatalf("%s %s: directory query = %q, want %q", req.Method, req.Path, values.Get("directory"), wantDir)
	}
}

func TestOfficialShape_SessionListCarriesDirectoryQuery(t *testing.T) {
	dir := t.TempDir()
	a, serve := newDataAgent(t, map[string]string{"/session": `[]`}, dir)
	if _, err := a.ListSessionsInDirectory(context.Background(), dir); err != nil {
		t.Fatalf("list: %v", err)
	}
	req := lastRequestFor(t, serve, "/session")
	if req.Method != "GET" {
		t.Fatalf("method = %s, want GET", req.Method)
	}
	directoryQuery(t, req, dir)
}

func TestOfficialShape_CreateIsQueryDirectoryWithOptionalBody(t *testing.T) {
	dir := t.TempDir()
	// Connected catalog fixture so the send gate resolves a model and the
	// write paths actually fire.
	provider := `{"all":[{"id":"mockprov","models":{"m1":{"id":"m1","name":"M1","limit":{"context":100000}}}}],
		"default":{"mockprov":"m1"},"connected":["mockprov"]}`
	a, serve := newDataAgent(t, map[string]string{
		"/session":  `[]`,
		"/provider": provider,
	}, dir)
	serve.methodResponses = map[string]string{
		"POST /session": `{"id":"ses_new_shape"}`,
		"POST /session/ses_new_shape/prompt_async": ``,
	}

	c, err := a.clientFor(context.Background())
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	s := newServerSessionForShapeTest(a, c)
	s.alive.Store(true)
	if err := s.Send("hello", nil, nil); err != nil {
		t.Fatalf("send: %v", err)
	}

	var create recordedRequest
	found := false
	for _, r := range serve.requestsFor("/session") {
		if r.Method == "POST" && strings.HasSuffix(strings.SplitN(r.Path, "?", 2)[0], "/session") {
			create, found = r, true
		}
	}
	if !found {
		t.Fatal("create POST not recorded")
	}
	directoryQuery(t, create, dir)
	body := strings.TrimSpace(create.Body)
	if body != "{}" {
		t.Fatalf("create body = %q, want {} (official SessionCreateData body is optional {parentID,title} — no directory, no model)", body)
	}

	// prompt_async：官方 SessionPromptAsyncData —— model 对象键 providerID/modelID。
	pa := lastRequestFor(t, serve, "/session/ses_new_shape/prompt_async")
	directoryQuery(t, pa, dir)
	if !strings.Contains(pa.Body, `"modelID":"m1"`) || !strings.Contains(pa.Body, `"providerID":"mockprov"`) {
		t.Fatalf("prompt_async body model keys deviate from official {providerID,modelID}: %s", pa.Body)
	}
	if !strings.Contains(pa.Body, `"parts"`) {
		t.Fatalf("prompt_async body missing parts: %s", pa.Body)
	}
}

func TestOfficialShape_AbortPathAndQuery(t *testing.T) {
	dir := t.TempDir()
	a, serve := newDataAgent(t, map[string]string{"/session": `[]`}, dir)
	serve.methodResponses = map[string]string{"POST /session/ses_shape_1/abort": ``}
	c, err := a.clientFor(context.Background())
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	s := newServerSessionForShapeTest(a, c)
	s.chatID.Store("ses_shape_1")
	if err := s.CancelTurn(context.Background()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	req := lastRequestFor(t, serve, "/session/ses_shape_1/abort")
	if req.Method != "POST" {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	directoryQuery(t, req, dir)
}

func TestOfficialShape_PermissionReplyIsPluralPathWithResponseLiteral(t *testing.T) {
	dir := t.TempDir()
	a, serve := newDataAgent(t, map[string]string{"/session": `[]`}, dir)
	serve.methodResponses = map[string]string{"POST /session/ses_shape_1/permissions/perm_1": ``}
	c, err := a.clientFor(context.Background())
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}

	for behavior, wantLiteral := range map[string]string{"allow": "once", "deny": "reject"} {
		err := a.respondPermission(context.Background(), c, "ses_shape_1", "perm_1",
			core.PermissionResult{Behavior: behavior})
		if err != nil {
			t.Fatalf("respondPermission(%s): %v", behavior, err)
		}
		req := lastRequestFor(t, serve, "/session/ses_shape_1/permissions/perm_1")
		if req.Method != "POST" {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		directoryQuery(t, req, dir)
		if !strings.Contains(req.Body, `"response":"`+wantLiteral+`"`) {
			t.Fatalf("permission body = %q, want response=%q (official enum once|always|reject)", req.Body, wantLiteral)
		}
	}
}

func TestOfficialShape_ProviderAndProjectCarryDirectoryQuery(t *testing.T) {
	dir := t.TempDir()
	a, serve := newDataAgent(t, map[string]string{
		"/provider": `{}`,
		"/project":  `[]`,
	}, dir)
	if _, err := a.ListProjectSuggestions(context.Background()); err != nil {
		t.Fatalf("projects: %v", err)
	}
	proj := lastRequestFor(t, serve, "/project")
	directoryQuery(t, proj, dir)

	a.AvailableModels(context.Background())
	prov := lastRequestFor(t, serve, "/provider")
	directoryQuery(t, prov, dir)
}

// newServerSessionForShapeTest builds a bound serverSession without the full
// StartSession handshake — the shape contract only needs the write paths.
func newServerSessionForShapeTest(a *Agent, c *Client) *serverSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &serverSession{
		a:      a,
		client: c,
		sub:    newSSESubscriber(ctx, a, c),
		ctx:    ctx,
		cancel: cancel,
	}
}
