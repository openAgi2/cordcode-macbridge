package gobridge

// catalog_recent_view_test.go locks the `catalogView:"recent"` session-list view
// (docs/2026-09-17-session-list-chatgpt-parity-implementation-plan.md §6.1):
// fail-closed parameter matrix, capability gate, root filter + recency order,
// cursor-v2 paging (page 0 / continuation / EOF), and scope isolation between
// the recent view and the standard fair-home view.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// fakeRecentCatalogAgent embeds the shared fakeAgent and opts into
// core.RecentCatalogProvider. recentOK=false lets a test prove the fail-closed
// gate with a driver that implements the interface but declines.
type fakeRecentCatalogAgent struct {
	*fakeAgent
	recentOK bool
}

func (f *fakeRecentCatalogAgent) SupportsRecentCatalog() bool { return f.recentOK }

func recentFixtureInfos() []core.AgentSessionInfo {
	return []core.AgentSessionInfo{
		// Deliberately NOT recency-ordered and containing a child row (parentID
		// rides the wire via the opencode/dsh-web mapping; the generic path here
		// has no parentID so the child filter is exercised through the wire map
		// in prepareRecentSnapshot tests below).
		{ID: "old", Summary: "old", ModifiedAt: time.Unix(1_700_000_100, 0).UTC()},
		{ID: "newest", Summary: "newest", ModifiedAt: time.Unix(1_700_000_300, 0).UTC()},
		{ID: "mid", Summary: "mid", ModifiedAt: time.Unix(1_700_000_200, 0).UTC()},
	}
}

func recentRequestParams(t *testing.T, extra map[string]any) json.RawMessage {
	t.Helper()
	params := map[string]any{"catalogView": "recent", "rootsOnly": true, "limit": 2}
	for k, v := range extra {
		params[k] = v
	}
	return mustJSONRaw(t, params)
}

// TestParseCatalogView_ParameterMatrix freezes the §6.1 fail-closed matrix.
func TestParseCatalogView_ParameterMatrix(t *testing.T) {
	cases := []struct {
		name      string
		view      string
		directory string
		rootsOnly bool
		wantView  string
		wantErr   string
	}{
		{name: "omitted is standard", view: "", directory: "", rootsOnly: false, wantView: catalogViewStandard},
		{name: "explicit standard untouched", view: "standard", directory: "/ws", rootsOnly: true, wantView: catalogViewStandard},
		{name: "recent valid", view: "recent", directory: "", rootsOnly: true, wantView: catalogViewRecent},
		{name: "padded recent trims to valid", view: " recent ", directory: "", rootsOnly: true, wantView: catalogViewRecent},
		{name: "recent with directory rejected", view: "recent", directory: "/ws", rootsOnly: true, wantErr: "invalid_params"},
		{name: "recent without rootsOnly rejected", view: "recent", directory: "", rootsOnly: false, wantErr: "invalid_params"},
		{name: "unknown view rejected", view: "yesterday", directory: "", rootsOnly: true, wantErr: "invalid_params"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view, wireErr := parseCatalogView(tc.view, tc.directory, tc.rootsOnly)
			if tc.wantErr != "" {
				if wireErr == nil || wireErr.Code != tc.wantErr {
					t.Fatalf("parseCatalogView(%q,%q,%v) = (%q,%v), want error %s", tc.view, tc.directory, tc.rootsOnly, view, wireErr, tc.wantErr)
				}
				return
			}
			if wireErr != nil {
				t.Fatalf("parseCatalogView(%q,%q,%v) unexpected error %v", tc.view, tc.directory, tc.rootsOnly, wireErr)
			}
			if view != tc.wantView {
				t.Fatalf("parseCatalogView(%q,%q,%v) = %q, want %q", tc.view, tc.directory, tc.rootsOnly, view, tc.wantView)
			}
		})
	}
}

// TestPrepareRecentSnapshot_RootFilterAndRecencyOrder: parentID/parentId rows
// are dropped, remaining roots sort by updatedAtMillis DESC with id ASC
// tie-break.
func TestPrepareRecentSnapshot_RootFilterAndRecencyOrder(t *testing.T) {
	input := []map[string]interface{}{
		{"id": "a", "updatedAtMillis": int64(100)},
		{"id": "b", "updatedAtMillis": int64(300), "parentId": "a"},   // child (opencode wire key)
		{"id": "c", "updatedAtMillis": int64(300), "parentID": "a"},   // child (alt key)
		{"id": "d", "updatedAtMillis": int64(300)},                    // ties with c's ts → id order
		{"id": "e", "updatedAtMillis": int64(200)},
		{"id": "f", "updatedAtMillis": int64(300)},
	}
	got := prepareRecentSnapshot(input)
	ids := make([]string, 0, len(got))
	for _, m := range got {
		ids = append(ids, m["id"].(string))
	}
	want := []string{"d", "f", "e", "a"} // ts 300 (id ASC: d<f), 200, 100; b/c filtered as children
	if len(ids) != len(want) {
		t.Fatalf("prepareRecentSnapshot ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("prepareRecentSnapshot ids = %v, want %v", ids, want)
		}
	}
}

// TestPrepareRecentSnapshot_HidesArchivedRows: rows carrying a top-level
// archivedAtMillis marker must not enter the recent feed (Phase 3 §5.5 — the
// feed is the active timeline). Without this filter an archived row re-enters
// on the next refresh after client-side convergence removed it.
func TestPrepareRecentSnapshot_HidesArchivedRows(t *testing.T) {
	input := []map[string]interface{}{
		{"id": "live", "updatedAtMillis": int64(100)},
		{"id": "archived", "updatedAtMillis": int64(500), "archivedAtMillis": int64(400)},
		{"id": "archived-zero", "updatedAtMillis": int64(600), "archivedAtMillis": int64(0)},
	}
	got := prepareRecentSnapshot(input)
	ids := make([]string, 0, len(got))
	for _, m := range got {
		ids = append(ids, m["id"].(string))
	}
	want := []string{"archived-zero", "live"}
	if len(ids) != len(want) {
		t.Fatalf("prepareRecentSnapshot ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("prepareRecentSnapshot ids = %v, want %v", ids, want)
		}
	}
}

// TestRecentView_CapabilityGateFailsClosed: a backend that does not opt into
// RecentCatalogProvider gets not_supported (never a standard-view fallback).
// The connection must declare catalog_cursor_epoch_v2 first so the request
// reaches the recent branch (the unified dispatch gate would otherwise reject
// with protocol.capability_required before the capability check).
func TestRecentView_CapabilityGateFailsClosed(t *testing.T) {
	handlers := newTestHandlers(t)
	// Plain fakeAgent does NOT implement RecentCatalogProvider.
	handlers.RegisterAgent("claudecode", &fakeAgent{name: "claudecode", sessionInfos: recentFixtureInfos()})
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode", Method: "list_sessions", RequestID: "r1",
		Params: recentRequestParams(t, nil),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if msgs[0]["ok"] != false {
		t.Fatalf("recent on non-provider backend ok = %#v, want false", msgs[0]["ok"])
	}
	errObj, _ := msgs[0]["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "not_supported" {
		t.Fatalf("recent on non-provider backend code = %q, want not_supported", code)
	}
}

// TestRecentView_GenericBackend_OrdersRootsAndPages: end-to-end over the
// generic (dsh-web style) branch: page 0 is recency-ordered roots (NOT
// fair-home), continuation follows the opaque v2 cursor, EOF ends cleanly.
func TestRecentView_GenericBackend_OrdersRootsAndPages(t *testing.T) {
	handlers := newTestHandlers(t)
	agent := &fakeRecentCatalogAgent{
		fakeAgent: &fakeAgent{name: "dsh-web", sessionInfos: recentFixtureInfos()},
		recentOK:  true,
	}
	handlers.RegisterAgent("dsh-web", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

	// Page 0 (limit 2): newest, mid — NOT the fair-home per-directory slice.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web", Method: "list_sessions", RequestID: "r1",
		Params: recentRequestParams(t, nil),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if ids := resultSessionIDs(t, msgs[0]); len(ids) != 2 || ids[0] != "newest" || ids[1] != "mid" {
		t.Fatalf("recent page-0 ids = %v, want [newest mid] (recency order, not fair-home)", ids)
	}
	data, _ := msgs[0]["data"].(map[string]any)
	if data["hasMore"] != true {
		t.Fatalf("recent page-0 hasMore = %#v, want true", data["hasMore"])
	}
	next, _ := data["nextCursor"].(string)
	if next == "" {
		t.Fatal("recent page-0 nextCursor missing")
	}
	if _, has := data["directoryTotals"]; has {
		t.Fatal("recent response must not carry directoryTotals (§6.1)")
	}

	// Page 1 via the opaque cursor: old, then EOF.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web", Method: "list_sessions", RequestID: "r2",
		Params: recentRequestParams(t, map[string]any{"cursor": next}),
	})
	msgs = readJSONMaps(t, clientConn, 1)
	if ids := resultSessionIDs(t, msgs[0]); len(ids) != 1 || ids[0] != "old" {
		t.Fatalf("recent page-1 ids = %v, want [old]", ids)
	}
	data, _ = msgs[0]["data"].(map[string]any)
	if data["hasMore"] != false {
		t.Fatalf("recent page-1 hasMore = %#v, want false (EOF)", data["hasMore"])
	}

	// The builder must have run exactly once: page-1 sliced the frozen snapshot.
	if agent.listSessionsCalls.Load() != 1 {
		t.Fatalf("ListSessions calls = %d, want 1 (page-1 reuses the frozen snapshot)", agent.listSessionsCalls.Load())
	}
}

// TestRecentView_ScopeIsolatedFromStandard: a recent cursor cannot be replayed
// against the standard view and vice versa — catalogView rides the scope
// identity, so the standard request finds no snapshot for its scope and the
// recent cursor fails cursor_stale there.
func TestRecentView_ScopeIsolatedFromStandard(t *testing.T) {
	handlers := newTestHandlers(t)
	agent := &fakeRecentCatalogAgent{
		fakeAgent: &fakeAgent{name: "dsh-web", sessionInfos: recentFixtureInfos()},
		recentOK:  true,
	}
	handlers.RegisterAgent("dsh-web", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

	// Build the recent snapshot and take its cursor.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web", Method: "list_sessions", RequestID: "r1",
		Params: recentRequestParams(t, nil),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	data, _ := msgs[0]["data"].(map[string]any)
	next, _ := data["nextCursor"].(string)
	if next == "" {
		t.Fatal("recent page-0 nextCursor missing")
	}

	// Replay that cursor WITHOUT catalogView (standard view): the standard scope
	// has no snapshot → cursor_stale, never a cross-view slice.
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web", Method: "list_sessions", RequestID: "r2",
		Params: mustJSONRaw(t, map[string]any{"cursor": next, "limit": 2}),
	})
	msgs = readJSONMaps(t, clientConn, 1)
	if msgs[0]["ok"] != false {
		t.Fatalf("cross-view cursor replay ok = %#v, want false", msgs[0]["ok"])
	}
	errObj, _ := msgs[0]["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "cursor_stale" {
		t.Fatalf("cross-view cursor replay code = %q, want cursor_stale", code)
	}
}

// TestRecentView_InvalidParamsFailClosed: recent + directory, and recent
// without rootsOnly, are rejected at dispatch before any backend call.
func TestRecentView_InvalidParamsFailClosed(t *testing.T) {
	handlers := newTestHandlers(t)
	agent := &fakeRecentCatalogAgent{
		fakeAgent: &fakeAgent{name: "dsh-web", sessionInfos: recentFixtureInfos()},
		recentOK:  true,
	}
	handlers.RegisterAgent("dsh-web", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web", Method: "list_sessions", RequestID: "r1",
		Params: recentRequestParams(t, map[string]any{"directory": "/ws"}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	errObj, _ := msgs[0]["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "invalid_params" {
		t.Fatalf("recent+directory code = %q, want invalid_params", code)
	}

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web", Method: "list_sessions", RequestID: "r2",
		Params: mustJSONRaw(t, map[string]any{"catalogView": "recent", "limit": 20}),
	})
	msgs = readJSONMaps(t, clientConn, 1)
	errObj, _ = msgs[0]["error"].(map[string]any)
	if code, _ := errObj["code"].(string); code != "invalid_params" {
		t.Fatalf("recent without rootsOnly code = %q, want invalid_params", code)
	}

	if agent.listSessionsCalls.Load() != 0 {
		t.Fatalf("ListSessions calls = %d, want 0 (validation precedes any backend fetch)", agent.listSessionsCalls.Load())
	}
}

// TestRecentView_CapabilityDerivation: deriveBackendCapabilities advertises
// session_catalog_recent only for RecentCatalogProvider implementers.
func TestRecentView_CapabilityDerivation(t *testing.T) {
	provider := &fakeRecentCatalogAgent{
		fakeAgent: &fakeAgent{name: "dsh-web"},
		recentOK:  true,
	}
	caps := deriveBackendCapabilities("dsh-web", provider, "")
	found := false
	for _, c := range caps {
		if c == "session_catalog_recent" {
			found = true
		}
	}
	if !found {
		t.Fatal("RecentCatalogProvider implementer must advertise session_catalog_recent")
	}

	decliner := &fakeRecentCatalogAgent{
		fakeAgent: &fakeAgent{name: "dsh-web"},
		recentOK:  false,
	}
	caps = deriveBackendCapabilities("dsh-web", decliner, "")
	for _, c := range caps {
		if c == "session_catalog_recent" {
			t.Fatal("declining RecentCatalogProvider must NOT advertise session_catalog_recent")
		}
	}

	plain := &fakeAgent{name: "dsh"}
	caps = deriveBackendCapabilities("dsh", plain, "")
	for _, c := range caps {
		if c == "session_catalog_recent" {
			t.Fatal("non-implementer must NOT advertise session_catalog_recent")
		}
	}
}

// TestRecentView_ClaudeBranchUsesGlobalCatalog: claudecode routes the recent
// builder through the claude session catalog — the injected (empty, temp-dir)
// catalog answers, and the agent's own ListSessions is never called.
func TestRecentView_ClaudeBranchUsesGlobalCatalog(t *testing.T) {
	handlers := newTestHandlers(t)
	// Point the claude catalog at an empty temp projects dir: the recent view
	// must read the catalog, not the agent fixture and not the real ~/.claude.
	handlers.claudeSessions = newClaudeSessionCatalog(t.TempDir())
	agent := &fakeRecentCatalogAgent{
		fakeAgent: &fakeAgent{name: "claudecode", sessionInfos: recentFixtureInfos()},
		recentOK:  true,
	}
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode", Method: "list_sessions", RequestID: "r1",
		Params: recentRequestParams(t, nil),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	// Empty catalog → empty page, hasMore=false; the fixture rows must NOT leak
	// in (that would prove the branch read agent.ListSessions instead).
	if ids := resultSessionIDs(t, msgs[0]); len(ids) != 0 {
		t.Fatalf("claude recent ids = %v, want [] (injected empty catalog; fixture leak = wrong branch)", ids)
	}
	if agent.listSessionsCalls.Load() != 0 {
		t.Fatalf("agent ListSessions calls = %d, want 0 (claude branch must use the session catalog)", agent.listSessionsCalls.Load())
	}
	data, _ := msgs[0]["data"].(map[string]any)
	if data["hasMore"] != false {
		t.Fatalf("empty catalog hasMore = %#v, want false", data["hasMore"])
	}
}

var _ core.Agent = (*fakeRecentCatalogAgent)(nil)
var _ core.RecentCatalogProvider = (*fakeRecentCatalogAgent)(nil)

// context compile guard: the builder closures must accept a context.Context.
var _ = context.Background

// TestRecentView_ArchiveFencesWireSnapshot: archive_session 成功后必须 fence 该
// backend 的 catalog wire cache（catalogSnapshotTTL=10min，不 fence 的话归档行在
// TTL 窗口内持续从 recent feed 回流，压过客户端收敛——2026-09-17 真机回归）。
// 流程：page-0 建快照（含 target）→ agent 侧 target 获得归档标记 → archive_session →
// 再 page-0：快照必须重建，target 不得返回。
func TestRecentView_ArchiveFencesWireSnapshot(t *testing.T) {
	base := []core.AgentSessionInfo{
		{ID: "live", Summary: "live", ModifiedAt: time.Unix(1710000100, 0).UTC()},
		{ID: "target", Summary: "to archive", ModifiedAt: time.Unix(1710000200, 0).UTC()},
	}
	agent := &fakeRecentCatalogAgent{
		fakeAgent:    &fakeAgent{name: "dsh-web", sessionInfos: append([]core.AgentSessionInfo(nil), base...)},
		recentOK:     true,
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("dsh-web", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

	// 1. page-0：建快照，target 在 feed 里。
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web", Method: "list_sessions", RequestID: "r1",
		Params: recentRequestParams(t, map[string]any{"limit": 10}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if ids := resultSessionIDs(t, msgs[0]); len(ids) != 2 {
		t.Fatalf("page-0 ids = %v, want both sessions", ids)
	}

	// 2. 归档 target：agent 侧集合更新为带 ArchivedAt 标记的 target。
	archivedAt := time.Unix(1710000300, 0).UTC()
	agent.fakeAgent.sessionInfos = []core.AgentSessionInfo{
		base[0],
		{ID: "target", Summary: "to archive", ModifiedAt: base[1].ModifiedAt, ArchivedAt: archivedAt},
	}
	agent.fakeAgent.archiveResult = &core.AgentSessionInfo{
		ID: "target", Summary: "to archive", ModifiedAt: base[1].ModifiedAt, ArchivedAt: archivedAt,
	}
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web", Method: "archive_session", RequestID: "arch-1",
		Params: mustJSONRaw(t, map[string]any{"sessionId": "target", "archivedAtMillis": float64(archivedAt.UnixMilli())}),
	})
	if msgs := readJSONMaps(t, clientConn, 1); msgs[0]["ok"] != true {
		t.Fatalf("archive_session ok = %#v, want true", msgs[0]["ok"])
	}

	// 3. 再 page-0：fence 必须强制重建快照；带标记的 target 被
	// prepareRecentSnapshot 过滤，且不得从缓存快照回流。
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "dsh-web", Method: "list_sessions", RequestID: "r2",
		Params: recentRequestParams(t, map[string]any{"limit": 10}),
	})
	msgs = readJSONMaps(t, clientConn, 1)
	if ids := resultSessionIDs(t, msgs[0]); len(ids) != 1 || ids[0] != "live" {
		t.Fatalf("post-archive page-0 ids = %v, want [live] (fence rebuilt snapshot; archived row must not return)", ids)
	}
}

// TestRecentView_CodexRemoteColdStartRetry: codex-remote 首次 page-0 在 8s 预算内超时后，
// 必须自动重试一次（30s 预算），重试成功后返回正常结果。
func TestRecentView_CodexRemoteColdStartRetry(t *testing.T) {
	base := []core.AgentSessionInfo{
		{ID: "session-1", Summary: "first", ModifiedAt: time.Unix(1710000100, 0).UTC()},
		{ID: "session-2", Summary: "second", ModifiedAt: time.Unix(1710000200, 0).UTC()},
	}

	// 自定义 agent：第一次调用时阻塞直到 context deadline 过期并返回 DeadlineExceeded，
	// 第二次调用立即返回成功。
	agent := &codexRemoteTimeoutAgent{
		fakeRecentCatalogAgent: &fakeRecentCatalogAgent{
			fakeAgent: &fakeAgent{name: "codex-remote", sessionInfos: base},
			recentOK:  true,
		},
	}

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex-remote", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

	// page-0：首次 8s 超时 → 自动重试 30s → 成功
	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "codex-remote", Method: "list_sessions", RequestID: "r1",
		Params: recentRequestParams(t, map[string]any{"limit": 10}),
	})
	msgs := readJSONMaps(t, clientConn, 1)

	// 验证结果：重试成功
	if msgs[0]["ok"] != true {
		t.Fatalf("codex-remote recent page-0 ok = %#v, want true (retry should succeed)", msgs[0]["ok"])
	}
	if ids := resultSessionIDs(t, msgs[0]); len(ids) != 2 {
		t.Fatalf("codex-remote recent page-0 ids = %v, want both sessions", ids)
	}

	// 验证 ListSessions 被调用了 2 次（首次 + 重试）
	if agent.listSessionsCalls.Load() != 2 {
		t.Fatalf("ListSessions calls = %d, want 2 (initial + retry)", agent.listSessionsCalls.Load())
	}
}

// codexRemoteTimeoutAgent 是一个测试用 agent，第一次 ListSessions 调用时等待 context
// deadline 过期并返回 DeadlineExceeded（模拟 codex-remote 冷启动超时），后续调用正常返回。
type codexRemoteTimeoutAgent struct {
	*fakeRecentCatalogAgent
}

func (a *codexRemoteTimeoutAgent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	callCount := a.listSessionsCalls.Add(1)
	if callCount == 1 {
		// 等待 context deadline 过期
		<-ctx.Done()
		return nil, ctx.Err()
	}
	// 第二次调用正常返回
	return append([]core.AgentSessionInfo(nil), a.sessionInfos...), nil
}
