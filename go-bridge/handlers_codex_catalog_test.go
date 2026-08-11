package gobridge

// handlers_codex_catalog_test.go 验证 Phase 2 Stream A chunk 2A-2/3：codex catalog wiring
// （设计 §5.1 step 5-6 / §10 发布顺序）。覆盖 handleListSessions 的 codex 分支门控与
// buildCodexEnrichedSessions 富 wire 管线串联，不启动真实 codex app-server（注入 fake lister）。
//
// 关键不变量：
//  1. capability 门控（§10）：DECLARED 连接 → codexHandleListSessions → FetchThreadList（thread/list，
//     默认 global scope / dir=""）；UNDECLARED 连接 → 既有 generic disk-scan（agent.ListSessions）
//     byte-for-byte 不变。
//  2. §5.1 step 6：FetchThreadList 失败 → list_failed 显式错误，不静默回退 disk-scan。
//  3. DECLARED 路径发射 v2 epoch cursor（§10：declared-only，undeclared 结构上不可达 pageV2）。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// fakeCodexCatalogAgent 嵌入共享 *fakeAgent（已满足 core.Agent）并追加 catalog seam：
// FetchThreadList（codexThreadLister）+ GetWorkDir（core.WorkDirSwitcher）。独立类型避免改动被
// 几十个测试共用的 fakeAgent。
type fakeCodexCatalogAgent struct {
	*fakeAgent
	fetchErr  error
	fetchFn   func(ctx context.Context, dir string) ([]core.AgentSessionInfo, error)
	fetchN    int
	fetchDirs []string
	workDirV  string
}

func (f *fakeCodexCatalogAgent) FetchThreadList(ctx context.Context, dir string) ([]core.AgentSessionInfo, error) {
	f.fetchN++
	f.fetchDirs = append(f.fetchDirs, dir)
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	if f.fetchFn != nil {
		return f.fetchFn(ctx, dir)
	}
	return nil, nil
}

func (f *fakeCodexCatalogAgent) GetWorkDir() string { return f.workDirV }

// withCodexRootsDisabled 让 catalog 单测 fixture 的 t.TempDir 不被本机 Mac Codex roots 白名单滤掉。
func withCodexRootsDisabled(t *testing.T) {
	t.Helper()
	prev := loadCodexWorkspaceRootsFn
	loadCodexWorkspaceRootsFn = func() []string { return nil }
	t.Cleanup(func() { loadCodexWorkspaceRootsFn = prev })
}

// threadFixtureSessions 是 FetchThreadList 返回的 thread/list 映射产物（ID 前缀 thread_，与
// disk-scan 的 disk_ 区分，便于证明路由）。dir 必须是真实存在的目录（catalog 会过滤已删 cwd）。
func threadFixtureSessions(dir string) []core.AgentSessionInfo {
	return []core.AgentSessionInfo{
		{ID: "thread_a", Summary: "thread A", Directory: dir,
			ProviderID: "openai", ModifiedAt: time.Unix(1_700_000_100, 0).UTC()},
		{ID: "thread_b", Summary: "thread B", Directory: dir,
			ProviderID: "openai", ModifiedAt: time.Unix(1_700_000_200, 0).UTC()},
	}
}

// resultSessionIDs 解包 result envelope 的 data.sessions 成 id 列表。
func resultSessionIDs(t *testing.T, msg map[string]any) []string {
	t.Helper()
	if got := msg["ok"]; got != true {
		t.Fatalf("ok = %#v, want true（envelope=%#v）", got, msg)
	}
	data, _ := msg["data"].(map[string]any)
	raw, _ := data["sessions"].([]any)
	ids := make([]string, 0, len(raw))
	for _, s := range raw {
		if m, ok := s.(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// TestCodexCatalog_V2Declared_RoutesToFetchThreadList：DECLARED 连接（hello 声明
// catalog_cursor_epoch_v2）→ codexHandleListSessions → FetchThreadList（thread/list，
// 默认 global dir=""；不读 agent.workDir）。
// 返回 thread_* ID 证明走了 thread/list，而非 disk-scan（fakeAgent.sessionInfos 的 disk_* 不可达）。
func TestCodexCatalog_V2Declared_RoutesToFetchThreadList(t *testing.T) {
	withCodexRootsDisabled(t)
	ws := t.TempDir()
	agent := &fakeCodexCatalogAgent{
		fakeAgent: &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{
			{ID: "disk_should_not_appear"},
		}},
		fetchFn:  func(context.Context, string) ([]core.AgentSessionInfo, error) { return threadFixtureSessions(ws), nil },
		workDirV: ws, // 故意设非空：断言 catalog 不再被 workDir 劫持
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true) // DECLARED

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex", Method: "list_sessions", RequestID: "r1",
		Params: mustJSONRaw(t, map[string]any{}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	ids := resultSessionIDs(t, msgs[0])
	// Declared v2 preserves the native thread/list order end-to-end.
	if len(ids) != 2 || ids[0] != "thread_a" || ids[1] != "thread_b" {
		t.Fatalf("DECLARED sessions = %v, want native order [thread_a thread_b]", ids)
	}
	if agent.fetchN != 1 {
		t.Fatalf("FetchThreadList calls = %d, want 1", agent.fetchN)
	}
	if len(agent.fetchDirs) != 1 || agent.fetchDirs[0] != "" {
		t.Fatalf("FetchThreadList dir = %v, want [\"\"]（root-only 全局 catalog，不读 workDir）", agent.fetchDirs)
	}
}

// TestCodexCatalog_V2Declared_HonorsDirectoryParam：wire 带 directory 时 cwd 精确过滤到该
// workspace（§3.1），仍不读 agent.workDir。
func TestCodexCatalog_V2Declared_HonorsDirectoryParam(t *testing.T) {
	withCodexRootsDisabled(t)
	ws := t.TempDir()
	target := filepath.Join(ws, "cordcode-ios")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := &fakeCodexCatalogAgent{
		fakeAgent: &fakeAgent{name: "codex"},
		fetchFn: func(context.Context, string) ([]core.AgentSessionInfo, error) {
			return threadFixtureSessions(target), nil
		},
		workDirV: filepath.Join(ws, "should-not-be-used"),
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex", Method: "list_sessions", RequestID: "r-dir",
		Params: mustJSONRaw(t, map[string]any{"directory": target}),
	})
	_ = readJSONMaps(t, clientConn, 1)
	if len(agent.fetchDirs) != 1 || agent.fetchDirs[0] != target {
		t.Fatalf("FetchThreadList dir = %v, want [%s]", agent.fetchDirs, target)
	}
}

// TestCodexBuildEnrichedSessions_PreservesUpstreamOrder：直接调用 buildCodexEnrichedSessions
// （绕过 catalogWireSnapshotCache 的 sortWireMapsForCursor 规范化），断言 builder 自身**不再**
// sortSessionsByUpdatedAt——保留 FetchThreadList 的上游输入序。这是 Phase 7 §445 的直接验证：
// fixture 故意返回非 updatedAt-DESC 序 [thread_a(100), thread_b(200)]（本地排序会给 [b,a]），
// builder 输出仍为 [a,b] 即证明未本地重排。cache 层的规范化由 integration 测试 + cache 专项测试覆盖。
func TestCodexBuildEnrichedSessions_PreservesUpstreamOrder(t *testing.T) {
	withCodexRootsDisabled(t)
	ws := t.TempDir()
	agent := &fakeCodexCatalogAgent{
		fakeAgent: &fakeAgent{name: "codex"},
		fetchFn:   func(context.Context, string) ([]core.AgentSessionInfo, error) { return threadFixtureSessions(ws), nil },
		workDirV:  ws,
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)

	mapped, err := handlers.buildCodexEnrichedSessions("codex", ws)
	if err != nil {
		t.Fatalf("buildCodexEnrichedSessions failed: %v", err)
	}
	if len(mapped) != 2 {
		t.Fatalf("mapped len = %d, want 2", len(mapped))
	}
	id0, _ := mapped[0]["id"].(string)
	id1, _ := mapped[1]["id"].(string)
	// fixture 输入序 [thread_a, thread_b]；builder 保留原样（不再 sortSessionsByUpdatedAt）。
	if id0 != "thread_a" || id1 != "thread_b" {
		t.Fatalf("builder order = [%s %s], want [thread_a thread_b]（保留 FetchThreadList 上游序，Phase 7 §445 不本地重排）", id0, id1)
	}
}

// TestCodexCatalog_Undeclared_RoutesToGenericDiskScan：UNDECLARED 连接 → 既有 generic
// disk-scan（agent.ListSessions）路径 byte-for-byte 不变（§10）。FetchThreadList 不可达，
// 返回 disk_* ID 证明走了 disk-scan 而非 thread/list。
func TestCodexCatalog_Undeclared_RoutesToGenericDiskScan(t *testing.T) {
	withCodexRootsDisabled(t)
	ws := t.TempDir()
	agent := &fakeCodexCatalogAgent{
		fakeAgent: &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{
			// Directory 必须是仍存在的目录：generic 路径也会 filterSessionsMissingWorkspace。
			{ID: "disk_only", Directory: ws},
		}},
		fetchFn:  func(context.Context, string) ([]core.AgentSessionInfo, error) { return threadFixtureSessions(ws), nil },
		workDirV: ws,
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	// 不 SetConnCatalogCursorEpochV2 → UNDECLARED。

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex", Method: "list_sessions", RequestID: "r1",
		Params: mustJSONRaw(t, map[string]any{}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	ids := resultSessionIDs(t, msgs[0])
	if len(ids) != 1 || ids[0] != "disk_only" {
		t.Fatalf("UNDECLARED sessions = %v, want [disk_only]（generic disk-scan，不触 thread/list）", ids)
	}
	if agent.fetchN != 0 {
		t.Fatalf("FetchThreadList calls = %d, want 0（undeclared 不可达 catalog 主线）", agent.fetchN)
	}
}

// TestCodexCatalog_FetchFailureReturnsExplicitError_NoSilentFallback：DECLARED 连接 +
// FetchThreadList 失败 → list_failed 显式错误（§5.1 step 6：删除 catalog 失败时静默回退
// JSONL 的路径）。断言 envelope ok=false + error.code=list_failed，且不返回任何 session。
func TestCodexCatalog_FetchFailureReturnsExplicitError_NoSilentFallback(t *testing.T) {
	withCodexRootsDisabled(t)
	ws := t.TempDir()
	agent := &fakeCodexCatalogAgent{
		fakeAgent: &fakeAgent{name: "codex", sessionInfos: []core.AgentSessionInfo{
			{ID: "disk_must_not_leak"}, // 即使 disk-scan 有数据，也不得作为 fallback 返回
		}},
		fetchErr: errors.New("codex app-server unreachable"),
		workDirV: ws,
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true) // DECLARED

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex", Method: "list_sessions", RequestID: "r1",
		Params: mustJSONRaw(t, map[string]any{}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if msgs[0]["ok"] != false {
		t.Fatalf("ok = %#v, want false（FetchThreadList 失败必须显式错误）", msgs[0]["ok"])
	}
	errMap, _ := msgs[0]["error"].(map[string]any)
	if errMap == nil || errMap["code"] != "list_failed" {
		t.Fatalf("error = %#v, want code=list_failed", msgs[0]["error"])
	}
	if agent.fetchN != 1 {
		t.Fatalf("FetchThreadList calls = %d, want 1（失败前确实调了 thread/list）", agent.fetchN)
	}
}

// TestCodexCatalog_V2Declared_EmitsV2EpochCursor：DECLARED 连接 + directory-scoped + 5 thread
// + limit=2 → page-0 返回 2 sessions + hasMore + nextCursor（v2 epoch）。
// 全局首页走 fair-home（无 nextCursor）；v2 cursor 在 directory 深挖路径验证。
func TestCodexCatalog_V2Declared_EmitsV2EpochCursor(t *testing.T) {
	withCodexRootsDisabled(t)
	ws := t.TempDir()
	threads := make([]core.AgentSessionInfo, 5)
	for i := range threads {
		threads[i] = core.AgentSessionInfo{
			ID:        "thread_" + string(rune('a'+i)),
			Summary:   "t",
			Directory: ws,
			// 严格递减 updatedAtMillis，使排序稳定（thread_a 最新）。
			ModifiedAt: time.Unix(int64(1_700_000_000+i*100), 0).UTC(),
		}
	}
	agent := &fakeCodexCatalogAgent{
		fakeAgent: &fakeAgent{name: "codex"},
		fetchFn:   func(context.Context, string) ([]core.AgentSessionInfo, error) { return threads, nil },
		workDirV:  ws,
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true) // DECLARED

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex", Method: "list_sessions", RequestID: "r1",
		// directory-scoped → pageV2（非 fair-home）
		Params: mustJSONRaw(t, map[string]any{"limit": 2, "directory": ws}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	data, _ := msgs[0]["data"].(map[string]any)
	if data["hasMore"] != true {
		t.Fatalf("hasMore = %#v, want true（5 thread / limit 2）", data["hasMore"])
	}
	nextCursor, ok := data["nextCursor"].(string)
	if !ok || nextCursor == "" {
		t.Fatalf("nextCursor = %#v, want 非空 v2 cursor", data["nextCursor"])
	}
	decoded, isV1, derr := decodeListCursorV2(nextCursor)
	if derr != nil {
		t.Fatalf("nextCursor 非 v2（解码失败）：%v", derr)
	}
	if isV1 {
		t.Fatal("nextCursor 是 v1（无 epoch）—— DECLARED 路径不得发射 v1 cursor（§10 泄漏）")
	}
	if decoded.Version != listCursorVersionV2 {
		t.Fatalf("nextCursor version = %d, want %d", decoded.Version, listCursorVersionV2)
	}
	if decoded.Epoch == "" {
		t.Fatal("nextCursor 缺 epoch（DECLARED 路径 cursor 必须携带快照 epoch）")
	}
	sessions, _ := data["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("page-0 sessions = %d, want 2（limit 2）", len(sessions))
	}
}
