package gobridge

// handlers_opencode_catalog_convergence_test.go 验证 Phase 4 OpenCode 收敛（设计 §5.3#3 /
// Phase 4 §421）：上游 /session 有定义序（time.updated desc，frozen fixture +
// TestOpenCodeCatalog_SessionDescByTimeUpdated 锁定）时，**v2 DECLARED 路径信任上游序**（停用
// sortSessionsByUpdatedAt 覆盖），**v1 UNDECLARED 路径仍本地排序**（§10 byte-for-byte 不变）。
//
// discriminator 设计：fake proxy 返回一组 **故意非 updated-desc** 顺序的 session
// （[B(updated=100), A(updated=300), C(updated=200)]）。若 builder 仍在 tail-sort（收敛前的
// 旧行为），v2 与 v1 都会输出 [A, C, B]（updated desc）。收敛后：
//   - v2（信任上游）：输出 [B, A, C]（透传上游真实序）。
//   - v1（本地排序）：输出 [A, C, B]（updated desc）。
// 两者差异即证明 sortSessionsByUpdatedAt 覆盖已从 v2 收敛路径移除、v1 保留。

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// nonDescOrderSessions 是 fake proxy 返回的「上游序」：故意非 updated-desc（[100, 300, 200]），
// 用于区分「信任上游序」（v2，输出 [B,A,C]）与「本地 updated desc 排序」（v1，输出 [A,C,B]）。
const nonDescOrderSessions = `[
	{"id":"ses_B","title":"B","time":{"created":1000,"updated":100}},
	{"id":"ses_A","title":"A","time":{"created":1000,"updated":300}},
	{"id":"ses_C","title":"C","time":{"created":1000,"updated":200}}
]`

// resultSessionIDsInOrder 解包 result envelope 的 data.sessions 成有序 id 列表（保序，用于顺序断言）。
func resultSessionIDsInOrder(t *testing.T, msg map[string]any) []string {
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

// nonDescOrderProxy 启动一个返回 nonDescOrderSessions 的 fake OpenCode proxy。
func nonDescOrderProxy(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/session" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(nonDescOrderSessions))
	}))
}

// quietLogger 静默 list_sessions 的 info 日志，避免污染测试输出。
func quietLogger(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

// TestOpenCodeCatalogBuilder_PreservesUpstreamOrder：直接调用 buildOpenCodeEnrichedSessions，
// 注入非 desc 顺序 [B(100), A(300), C(200)]。收敛后 builder **不再 tail-sort** → 返回上游真实
// 序 [B, A, C]。若 builder 仍调 sortSessionsByUpdatedAt，返回 [A, C, B]（updated desc）→ 失败。
//
// 注意：v2 handler 路径（ocHandleListSessions）的输出序由 catalogWireSnapshotCache 内部
// canonicalize 为 (updatedAtMillis DESC, id ASC) 作 cursor 稳定性保证（catalog_wire_snapshot.go:97，
// 非 sortSessionsByUpdatedAt），故 v2 输出恒为 desc —— 这与 OpenCode 上游（frozen fixture
// updated-desc）一致，正是 Phase 4 收敛要的「iOS 见到 = Mac native」。本测试直接打到 builder，
// 证明 sortSessionsByUpdatedAt 覆盖已从 builder 移除（snapshot 的独立 canonicalization 不受影响）。
func TestOpenCodeCatalogBuilder_PreservesUpstreamOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	quietLogger(t)
	proxy := nonDescOrderProxy(t)
	defer proxy.Close()

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{name: "opencode"})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxy.URL, "", ""))

	mapped, err := handlers.buildOpenCodeEnrichedSessions("opencode", "/tmp/conv-builder", false)
	if err != nil {
		t.Fatalf("buildOpenCodeEnrichedSessions: %v", err)
	}
	ids := make([]string, 0, len(mapped))
	for _, m := range mapped {
		if id, ok := m["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	// builder 透传上游序：[B, A, C]（不 tail-sort）。
	if len(ids) != 3 || ids[0] != "ses_B" || ids[1] != "ses_A" || ids[2] != "ses_C" {
		t.Fatalf("builder 输出序 = %v, want [ses_B ses_A ses_C]（透传上游序，不 sortSessionsByUpdatedAt）", ids)
	}
}

// TestOpenCodeCatalogV1_StillSortsByUpdatedDesc：UNDECLARED 连接 → ocHandleListSessionsV1
// （legacy v1 路径）。同一 fake proxy 非 desc 顺序输入，v1 在 builder 后显式
// sortSessionsByUpdatedAt → 输出 [A(300), C(200), B(100)]（updated desc）。证明 §10 v1
// byte-for-byte 不变（本地排序保留），收敛只对 v2 生效。
func TestOpenCodeCatalogV1_StillSortsByUpdatedDesc(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	quietLogger(t)
	proxy := nonDescOrderProxy(t)
	defer proxy.Close()

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{name: "opencode"})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxy.URL, "", ""))
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	// 不 SetConnCatalogCursorEpochV2 → UNDECLARED → v1。

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode", Method: "list_sessions", RequestID: "oc-v1-order",
		Params: mustJSONRaw(t, map[string]any{"directory": "/tmp/conv-v1", "limit": 10}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	ids := resultSessionIDsInOrder(t, msgs[0])
	// v1 本地排序：updated desc [A(300), C(200), B(100)]。
	if len(ids) != 3 || ids[0] != "ses_A" || ids[1] != "ses_C" || ids[2] != "ses_B" {
		t.Fatalf("v1 输出序 = %v, want [ses_A ses_C ses_B]（本地 updated desc，§10 不变）", ids)
	}
}

// TestOpenCodeCatalogV2_CursorStaleOnEpochMismatch：DECLARED 连接 + foreign-epoch cursor →
// pageV2 必须返回 cursor_stale（Retryable），不返回过时数据。校验 §4.1.1 跨快照拒绝语义。
func TestOpenCodeCatalogV2_CursorStaleOnEpochMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	quietLogger(t)
	proxy := nonDescOrderProxy(t)
	defer proxy.Close()

	handlers := newTestHandlers(t)
	handlers.RegisterAgent("opencode", &fakeAgent{name: "opencode"})
	handlers.RegisterOpenCodeProxy(NewOpenCodeProxy(proxy.URL, "", ""))
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true) // DECLARED

	// 构造一个 epoch 完全不匹配的 foreign v2 cursor（epoch="deadbeef" 永远不等于真实快照 epoch）。
	foreign, err := encodeListCursorV2(listCursorV2{Epoch: "deadbeef", UpdatedAtMillis: 50, SessionID: "ses_B"})
	if err != nil {
		t.Fatalf("encode foreign cursor: %v", err)
	}

	handlers.handleOpenCodeRPC(serverConn, WireMessage{
		BackendID: "opencode", Method: "list_sessions", RequestID: "oc-v2-stale",
		Params: mustJSONRaw(t, map[string]any{
			"directory": "/tmp/conv-stale", "limit": 10, "cursor": foreign,
		}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if msgs[0]["ok"] != false {
		t.Fatalf("ok = %#v, want false（foreign-epoch cursor 必须 cursor_stale 拒绝）", msgs[0]["ok"])
	}
	errMap, _ := msgs[0]["error"].(map[string]any)
	if errMap == nil || errMap["code"] != "cursor_stale" {
		t.Fatalf("error = %#v, want code=cursor_stale（跨快照 epoch mismatch）", msgs[0]["error"])
	}
}
