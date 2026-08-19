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

// ─────────────────────────────────────────────────────────────────────────────
// v2 形状契约（v2/gen types.gen.ts，2026-08-19 审计；owner 现网 1.18，v2 接入
// 前钉死请求形状，防漂移）：
//
//	V2SessionListData        GET  /api/session                  query{directory?,…}
//	V2SessionGetData         GET  /api/session/{id}             query?: never
//	V2SessionCreateData      POST /api/session                  body{id?,agent?,model?,location?} query?: never
//	V2SessionPromptData      POST /api/session/{id}/prompt      body{id?,prompt,delivery?,resume?}
//	V2SessionSwitchModelData POST /api/session/{id}/model       body{"model":ModelRef{id,providerID,variant?}}
//	V2SessionInterruptData   POST /api/session/{id}/interrupt   body?: never
//	V2SessionMessagesData    GET  /api/session/{id}/message     query{limit?,order?,cursor?}（无 directory）
//	V2SessionPermissionReplyData
//	                        POST /api/session/{id}/permission/{requestID}/reply
//	                                                            body{"reply":"once"|"always"|"reject",message?}
//	（x-opencode-directory 头为 1.18 惯例，v2 wire 不带。）
// ─────────────────────────────────────────────────────────────────────────────

// newV2DataAgent boots an Agent against a recording serve shaped like a v2
// instance: /global/health 404 → /api/health 200 → /api/session {data:[]}
// envelope (the probe pins generationV2).
func newV2DataAgent(t *testing.T, responses map[string]string, workDir string) (*Agent, *recordingServe) {
	t.Helper()
	return newV2DataAgentWithMethods(t, responses, nil, workDir)
}

func newV2DataAgentWithMethods(t *testing.T, responses map[string]string, methodResponses map[string]string, workDir string) (*Agent, *recordingServe) {
	t.Helper()
	responses["/api/health"] = `{"healthy":true}`
	responses["/api/session"] = `{"data":[]}`
	s := &recordingServe{responses: responses, methodResponses: methodResponses}
	base := s.start(t)
	a, err := New(map[string]any{
		"work_dir":          workDir,
		"opencode_web_url":  base,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a.(*Agent), s
}

func mustV2Client(t *testing.T, a *Agent) *Client {
	t.Helper()
	c, err := a.clientFor(context.Background())
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	if c.Generation() != generationV2 {
		t.Fatalf("generation = %q, want v2 (probe must pin v2 against the /api fixture)", c.Generation())
	}
	return c
}

// v2 list 保留 directory query（V2SessionListData 是 v2 唯一声明它的会话路由）。
func TestOfficialShape_V2_ListCarriesDirectoryQuery(t *testing.T) {
	dir := t.TempDir()
	a, serve := newV2DataAgent(t, map[string]string{}, dir)
	mustV2Client(t, a)
	if _, err := a.ListSessionsInDirectory(context.Background(), dir); err != nil {
		t.Fatalf("list: %v", err)
	}
	req := lastRequestFor(t, serve, "/api/session")
	if req.Method != "GET" {
		t.Fatalf("method = %s, want GET", req.Method)
	}
	directoryQuery(t, req, dir)
}

// v2 非列表路由不带 directory query（官方 v2 客户端从不发送）。
func TestOfficialShape_V2_NonListRoutesHaveNoDirectoryQuery(t *testing.T) {
	dir := t.TempDir()
	a, serve := newV2DataAgent(t, map[string]string{
		"/api/session/ses_1":          `{"data":{"id":"ses_1"}}`,
		"/api/session/ses_1/message":  `{"data":[]}`,
		"/api/session/ses_1/model":    `{}`,
		"/api/session/ses_1/prompt":   `{}`,
		"/api/session/ses_1/interrupt": `{}`,
	}, dir)
	c := mustV2Client(t, a)

	if _, err := a.fetchSessionInfo(context.Background(), c, "ses_1"); err != nil {
		t.Fatalf("fetchSessionInfo: %v", err)
	}
	if _, err := a.GetRichSessionHistory(context.Background(), "ses_1", 0); err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, path := range []string{"/api/session/ses_1", "/api/session/ses_1/message"} {
		req := lastRequestFor(t, serve, path)
		if strings.Contains(req.Query, "directory=") {
			t.Fatalf("%s: v2 non-list route must NOT carry a directory query (query?: never), got %q", path, req.Query)
		}
		if req.Directory != "" {
			t.Fatalf("%s: x-opencode-directory header is a 1.18 convention; v2 wire must not carry it", path)
		}
	}
}

// v2 模型切换：嵌套 body {"model":{id,providerID}}（非扁平）。
func TestOfficialShape_V2_ModelSwitchNestedBody(t *testing.T) {
	dir := t.TempDir()
	a, serve := newV2DataAgentWithMethods(t, map[string]string{}, map[string]string{
		"POST /api/session":             `{"data":{"id":"ses_1"}}`,
		"POST /api/session/ses_1/model": `{}`,
	}, dir)
	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	ss := sess.(*serverSession)
	if err := ss.postModel(ocwModelRef{ProviderID: "prov", ID: "mdl"}, "ses_1"); err != nil {
		t.Fatalf("postModel: %v", err)
	}
	req := lastRequestFor(t, serve, "/api/session/ses_1/model")
	if !strings.Contains(req.Body, `"model"`) || !strings.Contains(req.Body, `"providerID":"prov"`) || !strings.Contains(req.Body, `"id":"mdl"`) {
		t.Fatalf("v2 model body = %q, want nested {model:{id,providerID}}", req.Body)
	}
	if strings.Contains(req.Query, "directory=") {
		t.Fatalf("v2 model route must not carry directory query, got %q", req.Query)
	}
}

// v2 中断：/api/session/{id}/interrupt，无 JSON body（body?: never）。
func TestOfficialShape_V2_InterruptHasNoBody(t *testing.T) {
	dir := t.TempDir()
	a, serve := newV2DataAgent(t, map[string]string{
		"/api/session/ses_1/interrupt": `{}`,
	}, dir)
	mustV2Client(t, a)
	sess, err := a.StartSession(context.Background(), "ses_1")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	ss := sess.(*serverSession)
	if err := ss.CancelTurn(context.Background()); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	req := lastRequestFor(t, serve, "/api/session/ses_1/interrupt")
	if req.Method != "POST" {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if req.Body != "" {
		t.Fatalf("v2 interrupt body = %q, want empty (body?: never)", req.Body)
	}
}

// v2 权限回复：/api/session/{id}/permission/{rid}/reply body {"reply":<enum>}。
func TestOfficialShape_V2_PermissionReplyPathAndBody(t *testing.T) {
	dir := t.TempDir()
	a, serve := newV2DataAgent(t, map[string]string{
		"/api/session/ses_1/permission/per_1/reply": `true`,
	}, dir)
	mustV2Client(t, a)
	if err := a.RespondSessionPermission(context.Background(), "ses_1", "per_1", core.PermissionResult{Behavior: "always"}); err != nil {
		t.Fatalf("RespondSessionPermission: %v", err)
	}
	req := lastRequestFor(t, serve, "/api/session/ses_1/permission/per_1/reply")
	if req.Method != "POST" {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if !strings.Contains(req.Body, `"reply":"always"`) {
		t.Fatalf("v2 permission reply body = %q, want {\"reply\":\"always\"}", req.Body)
	}
	if strings.Contains(req.Query, "directory=") {
		t.Fatalf("v2 permission reply must not carry directory query, got %q", req.Query)
	}
}
