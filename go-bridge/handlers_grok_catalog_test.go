package gobridge

// handlers_grok_catalog_test.go 验证 Phase 3 Grok catalog wiring（设计 §5.4 / §10 发布顺序）。
// 与 handlers_codex_catalog_test.go 刻意同构：4 个门控/管线/cursor 断言，注入 fake lister
// （不启动真实 grok 子进程）。
//
// 关键不变量（与 codex 对齐，差异点标注）：
//  1. capability 门控（§10）：DECLARED 连接 → grokHandleListSessions → FetchSessionList
//     （managed ACP session/list，**跨 cwd，不取 dir**）；UNDECLARED 连接 → 既有 generic
//     disk-scan（agent.ListSessions）byte-for-byte 不变。
//  2. §5.1 step 6 / §5.4 #5：FetchSessionList 失败 → list_failed 显式错误，不静默回退 disk-scan
//     （含握手缺 session/list 能力的 fail-closed 路径）。
//  3. DECLARED 路径发射 v2 epoch cursor（§10：declared-only，undeclared 结构上不可达 pageV2）。
//  4. Grok session/list 非 cwd-scoped → FetchSessionList 不取 dir（与 codex FetchThreadList(ctx,dir) 不同）。

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// fakeGrokCatalogAgent 嵌入共享 *fakeAgent（已满足 core.Agent）并追加 catalog seam：
// FetchSessionList（grokSessionLister）。独立类型避免改动被几十个测试共用的 fakeAgent。
// 与 fakeCodexCatalogAgent 的差异：FetchSessionList 不取 dir（Grok 非 cwd-scoped），故无 fetchDirs。
type fakeGrokCatalogAgent struct {
	*fakeAgent
	fetchErr error
	fetchFn  func(ctx context.Context) ([]core.AgentSessionInfo, error)
	fetchN   int
}

func (f *fakeGrokCatalogAgent) FetchSessionList(ctx context.Context) ([]core.AgentSessionInfo, error) {
	f.fetchN++
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	if f.fetchFn != nil {
		return f.fetchFn(ctx)
	}
	return nil, nil
}

// grokFixtureSessions 是 FetchSessionList 返回的 session/list 映射产物（ID 前缀 session_，
// 与 disk-scan 的 disk_ 区分，证明路由）。ws 必须是仍存在的目录（workspace 存在性过滤）。
func grokFixtureSessions(ws string) []core.AgentSessionInfo {
	return []core.AgentSessionInfo{
		{ID: "session_a", Summary: "grok A", Directory: ws,
			ProviderID: "grok", GitBranch: "main",
			ModifiedAt: time.Unix(1_700_000_100, 0).UTC()},
		{ID: "session_b", Summary: "grok B", Directory: ws,
			ProviderID: "grok", GitBranch: "feature/x",
			ModifiedAt: time.Unix(1_700_000_200, 0).UTC()},
	}
}

// TestGrokCatalog_V2Declared_RoutesToFetchSessionList：DECLARED 连接（hello 声明
// catalog_cursor_epoch_v2）→ grokHandleListSessions → FetchSessionList（session/list，跨 cwd）。
// 返回 session_* ID 证明走了 session/list，而非 disk-scan（fakeAgent.sessionInfos 的 disk_* 不可达）。
func TestGrokCatalog_V2Declared_RoutesToFetchSessionList(t *testing.T) {
	ws := t.TempDir()
	agent := &fakeGrokCatalogAgent{
		fakeAgent: &fakeAgent{name: "grokbuild", sessionInfos: []core.AgentSessionInfo{
			{ID: "disk_should_not_appear", Directory: ws},
		}},
		fetchFn: func(context.Context) ([]core.AgentSessionInfo, error) { return grokFixtureSessions(ws), nil },
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("grokbuild", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true) // DECLARED

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "grokbuild", Method: "list_sessions", RequestID: "r1",
		Params: mustJSONRaw(t, map[string]any{}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	ids := resultSessionIDs(t, msgs[0])
	// Declared v2 preserves the native session/list order end-to-end.
	if len(ids) != 2 || ids[0] != "session_a" || ids[1] != "session_b" {
		t.Fatalf("DECLARED sessions = %v, want native order [session_a session_b]", ids)
	}
	if agent.fetchN != 1 {
		t.Fatalf("FetchSessionList calls = %d, want 1", agent.fetchN)
	}
}

// TestGrokBuildEnrichedSessions_PreservesUpstreamOrder：直接调用 buildGrokEnrichedSessions
// （绕过 catalogWireSnapshotCache 的 sortWireMapsForCursor 规范化），断言 builder 自身**不再**
// sortSessionsByUpdatedAt——保留 FetchSessionList 的上游输入序。这是 Phase 7 §445 的直接验证：
// fixture 故意返回非 updatedAt-DESC 序 [session_a(100), session_b(200)]（本地排序会给 [b,a]），
// builder 输出仍为 [a,b] 即证明未本地重排。cache 层的规范化由 integration 测试 + cache 专项测试覆盖。
func TestGrokBuildEnrichedSessions_PreservesUpstreamOrder(t *testing.T) {
	ws := t.TempDir()
	agent := &fakeGrokCatalogAgent{
		fakeAgent: &fakeAgent{name: "grokbuild"},
		fetchFn:   func(context.Context) ([]core.AgentSessionInfo, error) { return grokFixtureSessions(ws), nil },
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("grokbuild", agent)

	mapped, err := handlers.buildGrokEnrichedSessions(context.Background(), "grokbuild")
	if err != nil {
		t.Fatalf("buildGrokEnrichedSessions failed: %v", err)
	}
	if len(mapped) != 2 {
		t.Fatalf("mapped len = %d, want 2", len(mapped))
	}
	id0, _ := mapped[0]["id"].(string)
	id1, _ := mapped[1]["id"].(string)
	// fixture 输入序 [session_a, session_b]；builder 保留原样（不再 sortSessionsByUpdatedAt）。
	if id0 != "session_a" || id1 != "session_b" {
		t.Fatalf("builder order = [%s %s], want [session_a session_b]（保留 FetchSessionList 上游序，Phase 7 §445 不本地重排）", id0, id1)
	}
}

// Undeclared keeps v1 presentation while sharing native membership with declared/poller.
func TestGrokCatalog_UndeclaredUsesNativeMembershipWithV1(t *testing.T) {
	ws := t.TempDir()
	agent := &fakeGrokCatalogAgent{
		fakeAgent: &fakeAgent{name: "grokbuild", sessionInfos: []core.AgentSessionInfo{
			{ID: "disk_only", Directory: ws},
		}},
		fetchFn: func(context.Context) ([]core.AgentSessionInfo, error) { return grokFixtureSessions(ws), nil },
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("grokbuild", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	// 不 SetConnCatalogCursorEpochV2 → UNDECLARED。

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "grokbuild", Method: "list_sessions", RequestID: "r1",
		Params: mustJSONRaw(t, map[string]any{}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	ids := resultSessionIDs(t, msgs[0])
	if len(ids) != 2 || ids[0] != "session_b" || ids[1] != "session_a" {
		t.Fatalf("UNDECLARED sessions = %v, want v1-sorted native membership", ids)
	}
	if agent.fetchN != 1 {
		t.Fatalf("FetchSessionList calls = %d, want 1", agent.fetchN)
	}
}

// TestGrokCatalog_FetchFailureReturnsExplicitError_NoSilentFallback：DECLARED 连接 +
// FetchSessionList 失败（含 §5.4 #5 fail-closed：握手缺 session/list 能力）→ list_failed
// 显式错误。断言 envelope ok=false + error.code=list_failed，且不返回任何 session（即使
// disk-scan 有数据，也不得作为 fallback 返回）。
func TestGrokCatalog_FetchFailureReturnsExplicitError_NoSilentFallback(t *testing.T) {
	agent := &fakeGrokCatalogAgent{
		fakeAgent: &fakeAgent{name: "grokbuild", sessionInfos: []core.AgentSessionInfo{
			{ID: "disk_must_not_leak"}, // 即使 disk-scan 有数据，也不得作为 fallback 返回
		}},
		fetchErr: errors.New("grok backend does not advertise session/list"),
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("grokbuild", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true) // DECLARED

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "grokbuild", Method: "list_sessions", RequestID: "r1",
		Params: mustJSONRaw(t, map[string]any{}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if msgs[0]["ok"] != false {
		t.Fatalf("ok = %#v, want false（FetchSessionList 失败必须显式错误）", msgs[0]["ok"])
	}
	errMap, _ := msgs[0]["error"].(map[string]any)
	if errMap == nil || errMap["code"] != "list_failed" {
		t.Fatalf("error = %#v, want code=list_failed", msgs[0]["error"])
	}
	if agent.fetchN != 1 {
		t.Fatalf("FetchSessionList calls = %d, want 1（失败前确实调了 session/list）", agent.fetchN)
	}
}

// TestGrokCatalog_V2Declared_EmitsV2EpochCursor pins the directory-bound declared v2 cursor.
func TestGrokCatalog_V2Declared_EmitsV2EpochCursor(t *testing.T) {
	ws := t.TempDir()
	sessions := make([]core.AgentSessionInfo, 5)
	for i := range sessions {
		sessions[i] = core.AgentSessionInfo{
			ID:        "session_" + string(rune('a'+i)),
			Summary:   "g",
			Directory: ws,
			// 严格递减 updatedAtMillis，使排序稳定（session_a 最新）。
			ModifiedAt: time.Unix(int64(1_700_000_000+i*100), 0).UTC(),
		}
	}
	agent := &fakeGrokCatalogAgent{
		fakeAgent: &fakeAgent{name: "grokbuild"},
		fetchFn:   func(context.Context) ([]core.AgentSessionInfo, error) { return sessions, nil },
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("grokbuild", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true) // DECLARED

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "grokbuild", Method: "list_sessions", RequestID: "r1",
		Params: mustJSONRaw(t, map[string]any{"limit": 2, "directory": ws}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	data, _ := msgs[0]["data"].(map[string]any)
	if data["hasMore"] != true {
		t.Fatalf("hasMore = %#v, want true（5 session / limit 2）", data["hasMore"])
	}
	nextCursor, ok := data["nextCursor"].(string)
	if !ok || nextCursor == "" {
		t.Fatalf("nextCursor = %#v, want 非空 pagination cursor", data["nextCursor"])
	}
	decoded, isV1, derr := decodeListCursorV2(nextCursor)
	if derr != nil {
		t.Fatalf("nextCursor 解码失败：%v", derr)
	}
	if isV1 || decoded.Version != listCursorVersionV2 || decoded.Epoch == "" {
		t.Fatalf("declared directory cursor = %+v isV1=%v, want epoch-bearing v2", decoded, isV1)
	}
	wireSessions, _ := data["sessions"].([]any)
	if len(wireSessions) != 2 {
		t.Fatalf("page-0 sessions = %d, want 2（limit 2）", len(wireSessions))
	}
}

func TestGrokCatalog_DirectoryV2FenceAndCrossScopeStale(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	sessions := []core.AgentSessionInfo{
		{ID: "a1", Summary: "A one", Directory: dirA, ModifiedAt: time.Unix(3, 0)},
		{ID: "a2", Summary: "A two", Directory: dirA, ModifiedAt: time.Unix(2, 0)},
		{ID: "b1", Summary: "B one", Directory: dirB, ModifiedAt: time.Unix(1, 0)},
	}
	agent := &fakeGrokCatalogAgent{fakeAgent: &fakeAgent{name: "grokbuild"}, fetchFn: func(context.Context) ([]core.AgentSessionInfo, error) {
		return append([]core.AgentSessionInfo(nil), sessions...), nil
	}}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("grokbuild", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

	request := func(id, directory, cursor string) map[string]any {
		params := map[string]any{"directory": directory, "limit": 1}
		if cursor != "" {
			params["cursor"] = cursor
		}
		handlers.HandleRPC(serverConn, WireMessage{BackendID: "grokbuild", Method: "list_sessions", RequestID: id, Params: mustJSONRaw(t, params)})
		return readJSONMaps(t, clientConn, 1)[0]
	}
	p0 := request("a0", dirA, "")
	data := p0["data"].(map[string]any)
	cursor := data["nextCursor"].(string)
	if cross := request("cross", dirB, cursor); cross["ok"] != false || cross["error"].(map[string]any)["code"] != "cursor_stale" {
		t.Fatalf("cross-directory cursor accepted: %#v", cross)
	}

	handlers.grokCatalogWireCache().FenceBackend("grokbuild")
	if old := request("old", dirA, cursor); old["ok"] != false || old["error"].(map[string]any)["code"] != "cursor_stale" {
		t.Fatalf("old directory cursor survived global fence: %#v", old)
	}
	sessions = []core.AgentSessionInfo{{ID: "a-new", Summary: "A new", Directory: dirA, ModifiedAt: time.Unix(4, 0)}}
	newPage := request("new", dirA, "")
	if ids := resultSessionIDs(t, newPage); len(ids) != 1 || ids[0] != "a-new" {
		t.Fatalf("rebuilt directory view=%v", ids)
	}
}

func TestGrokCatalog_UndeclaredDirectoryRemainsV1(t *testing.T) {
	dir := t.TempDir()
	agent := &fakeGrokCatalogAgent{
		fakeAgent: &fakeAgent{name: "grokbuild", sessionInfos: []core.AgentSessionInfo{{ID: "disk-must-not-appear", Directory: dir}}},
		fetchFn: func(context.Context) ([]core.AgentSessionInfo, error) {
			return []core.AgentSessionInfo{
				{ID: "native-a", Summary: "native A", Directory: dir, ModifiedAt: time.Unix(2, 0)},
				{ID: "native-b", Summary: "native B", Directory: dir, ModifiedAt: time.Unix(1, 0)},
			}, nil
		},
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("grokbuild", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.HandleRPC(serverConn, WireMessage{BackendID: "grokbuild", Method: "list_sessions", RequestID: "legacy-dir", Params: mustJSONRaw(t, map[string]any{"directory": dir, "limit": 1})})
	msg := readJSONMaps(t, clientConn, 1)[0]
	data := msg["data"].(map[string]any)
	cursor := data["nextCursor"].(string)
	_, isV1, err := decodeListCursorV2(cursor)
	if err != nil || !isV1 {
		t.Fatalf("undeclared directory cursor isV1=%v err=%v", isV1, err)
	}
	if agent.fetchN != 1 {
		t.Fatalf("undeclared directory native catalog calls=%d want 1", agent.fetchN)
	}
}
