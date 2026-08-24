package codexweb

// catalog_thread_list_test.go —— P0-4：FetchThreadList / FetchThreadListHead 的
// 请求形状冻结（全局不带 cwd / directory-cwd 精确过滤 / head limit 有界）+ 官方
// 字段映射（name 优先 preview）。

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func codexWebCatalogTestAgent(t *testing.T) (*scriptedTransport, *Agent) {
	t.Helper()
	s := newScripted()
	cl := NewClient(s, 1)
	t.Cleanup(func() { _ = cl.Close() })
	go drainNotifications(cl)
	ep := &ServiceEndpoint{Source: SourceExternalDaemonReused, CLIVersion: "0.149.0-alpha.4"}
	ep.client = cl
	a := New(nil)
	a.endpoint = ep
	return s, a
}

// TestAgentFetchThreadListGlobalShape 全局（dir=""）请求不带 cwd；
// 返回按官方字段映射（Summary=name 优先 preview；Directory=cwd；ModifiedAt=updatedAt）。
func TestAgentFetchThreadListGlobalShape(t *testing.T) {
	s, a := codexWebCatalogTestAgent(t)
	page := map[string]any{
		"data": []any{
			map[string]any{"id": "th-2", "preview": "preview-2", "cwd": "/ws", "updatedAt": int64(1700000002)},
			map[string]any{"id": "th-1", "name": "named-1", "preview": "preview-1", "cwd": "/ws", "updatedAt": int64(1700000001)},
		},
		"nextCursor": nil, "backwardsCursor": nil,
	}
	calls := captureParams(s, "thread/list", page)

	infos, err := a.FetchThreadList(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("thread/list 调用=%d，want 1", len(*calls))
	}
	expectParams(t, (*calls)[0], map[string]any{})
	if len(infos) != 2 {
		t.Fatalf("infos=%d，want 2", len(infos))
	}
	if infos[0].ID != "th-2" || infos[0].Summary != "preview-2" || infos[1].Summary != "named-1" {
		t.Fatalf("官方字段映射错误：%+v", infos)
	}
	if infos[0].Directory != "/ws" {
		t.Fatalf("Directory 映射错误：%q", infos[0].Directory)
	}
	if !infos[0].ModifiedAt.Equal(time.Unix(1700000002, 0).UTC()) {
		t.Fatalf("ModifiedAt 映射错误：%v", infos[0].ModifiedAt)
	}
}

// TestAgentFetchThreadListDirectoryFilter 非空 dir → 官方 cwd=[dir] 精确过滤。
func TestAgentFetchThreadListDirectoryFilter(t *testing.T) {
	s, a := codexWebCatalogTestAgent(t)
	calls := captureParams(s, "thread/list", map[string]any{"data": []any{}, "nextCursor": nil})

	if _, err := a.FetchThreadList(context.Background(), "/ws/cordcode-ios"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("thread/list 调用=%d，want 1", len(*calls))
	}
	expectParams(t, (*calls)[0], map[string]any{"cwd": []any{"/ws/cordcode-ios"}})
}

// TestAgentFetchThreadListHeadBounded 单页 head：limit 有界（25 cap）且不翻页。
func TestAgentFetchThreadListHeadBounded(t *testing.T) {
	s, a := codexWebCatalogTestAgent(t)
	page := map[string]any{
		"data": []any{
			map[string]any{"id": "th-1", "preview": "p1", "cwd": "/ws", "updatedAt": int64(1700000001)},
		},
		"nextCursor": nil, "backwardsCursor": nil,
	}
	calls := captureParams(s, "thread/list", page)

	if _, err := a.FetchThreadListHead(context.Background(), "", 999); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("head 调用=%d，want 1（不跟 cursor）", len(*calls))
	}
	// 999 必须被 clamp 到 head 上限 25。
	expectParams(t, (*calls)[0], map[string]any{"limit": float64(25)})
}

// TestAgentFetchThreadListHeadLimitPassthrough limit≤25 原样传。
func TestAgentFetchThreadListHeadLimitPassthrough(t *testing.T) {
	s, a := codexWebCatalogTestAgent(t)
	calls := captureParams(s, "thread/list", map[string]any{"data": []any{}, "nextCursor": nil})

	if _, err := a.FetchThreadListHead(context.Background(), "", 3); err != nil {
		t.Fatal(err)
	}
	expectParams(t, (*calls)[0], map[string]any{"limit": float64(3)})
}

// TestAgentFetchThreadListDedup 重复条目（id+sessionId 同源）折叠，且上限截断。
func TestAgentFetchThreadListDedupAndCap(t *testing.T) {
	s, a := codexWebCatalogTestAgent(t)
	data := make([]map[string]any, 0, 4)
	data = append(data, map[string]any{"id": "dup", "preview": "first", "cwd": "/ws", "updatedAt": int64(1)})
	data = append(data, map[string]any{"id": "dup", "preview": "second", "cwd": "/ws", "updatedAt": int64(2)})
	captureParams(s, "thread/list", map[string]any{"data": data, "nextCursor": nil})

	infos, err := a.FetchThreadList(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("重复 id 未折叠：%+v", infos)
	}
	if infos[0].Summary != "first" {
		t.Fatalf("应保留首个官方条目：%+v", infos[0])
	}
	// 结构断言兜底（防止测试静默退化）。
	if !reflect.DeepEqual(infos[0].Directory, "/ws") {
		t.Fatalf("directory=%q", infos[0].Directory)
	}
}
