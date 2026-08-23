package codexweb

// sessions_test.go —— p2-catalog contract 测试（§13.1 catalog：cursor、archive、
// 官方排序、请求/响应字段逐字段冻结）。
//
// 证据来源：testdata/official-0.149.0-alpha.4/dumps/catalog/raw.jsonl（真实官方
// 二进制帧）+ dumps/ownership（-32600 writer 冲突原文）。fixture 字节直接驱动断言，
// 手写形状只用于请求捕获比较。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fixtureDump 解析一个 dump 组，返回（按请求 id 的服务端响应字节、按到达序的客户端请求）。
func fixtureDump(t *testing.T, group string) (map[string]json.RawMessage, []string, map[string]string) {
	t.Helper()
	p := filepath.Join("testdata", "official-0.149.0-alpha.4", "dumps", group, "raw.jsonl")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	responses := map[string]json.RawMessage{}
	var clientOrder []string
	methodByID := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e struct {
			Dir string          `json:"dir"`
			Msg json.RawMessage `json:"msg"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		var m struct {
			ID     *json.Number    `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(e.Msg, &m); err != nil {
			continue
		}
		switch e.Dir {
		case "client":
			if m.ID != nil && m.Method != "" {
				clientOrder = append(clientOrder, m.Method)
				methodByID[m.ID.String()] = m.Method
			}
		case "server":
			if m.ID != nil {
				responses[m.ID.String()] = m.Result
			}
		}
	}
	return responses, clientOrder, methodByID
}

func mustDecode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

// TestCatalogFixtureListDecode 逐字段冻结官方 thread/list 响应解码（真实帧）。
func TestCatalogFixtureListDecode(t *testing.T) {
	resp, order, byID := fixtureDump(t, "catalog")
	if len(order) == 0 {
		t.Fatal("fixture 客户端请求为空")
	}

	// 空 catalog：{data:[], nextCursor:null, backwardsCursor:null}
	empty := mustDecode[ThreadListPage](t, resp["2"])
	if len(empty.Data) != 0 || empty.NextCursor != "" || empty.BackwardsCursor != "" {
		t.Fatalf("空页解码错误：%+v", empty)
	}

	// 全量列表（4 threads，官方 desc：filler2 → filler1 → filler0 → catalog-first）
	all := mustDecode[ThreadListPage](t, resp["11"])
	if len(all.Data) != 4 {
		t.Fatalf("全量应 4 条，得 %d", len(all.Data))
	}
	wantOrder := []string{
		"01a02532-b19b-7110-a34e-ec8bf152be36",
		"01a02532-b13a-7972-b1fa-05b04aea78dd",
		"01a02532-b0e4-7a32-9e0d-76baaf659f08",
		"01a02532-b09d-72d3-82af-654bf25e88b3",
	}
	for i, want := range wantOrder {
		if all.Data[i].ID != want {
			t.Fatalf("官方排序位置 %d = %s，期望 %s", i, all.Data[i].ID, want)
		}
	}
	first := all.Data[0]
	if first.Preview != "MOCK:STREAM filler turn 2" || first.Title() != first.Preview {
		t.Fatalf("preview/Title 解码错误：%q", first.Preview)
	}
	if first.Status.Type != ThreadStatusIdle || first.ModelProvider != "mockpi" {
		t.Fatalf("status/provider 解码错误：%+v", first.Status)
	}
	if first.CreatedAt != 1787330474 || first.UpdatedAt != 1787330474 || first.RecencyAt != 1787330474 {
		t.Fatalf("时间戳解码错误：%d/%d/%d", first.CreatedAt, first.UpdatedAt, first.RecencyAt)
	}
	if first.CliVersion != "0.149.0-alpha.4" || first.Source != "vscode" || first.Cwd == "" {
		t.Fatalf("cliVersion/source/cwd 解码错误：%q/%q/%q", first.CliVersion, first.Source, first.Cwd)
	}
	if first.Name != nil || len(first.Turns) != 0 || first.Ephemeral {
		t.Fatalf("name/turns/ephemeral 解码错误：%v/%d/%v", first.Name, len(first.Turns), first.Ephemeral)
	}
	if first.Path == "" || first.SessionID != first.ID {
		t.Fatalf("path/sessionId 解码错误：%q/%q", first.Path, first.SessionID)
	}

	// limit=1 翻页：nextCursor/backwardsCursor 均为不透明字符串
	p1 := mustDecode[ThreadListPage](t, resp["12"])
	if len(p1.Data) != 1 || p1.Data[0].ID != wantOrder[0] {
		t.Fatalf("limit=1 页解码错误")
	}
	if p1.NextCursor != "2026-08-22T00:41:14Z" {
		t.Fatalf("nextCursor=%q，期望官方时间戳游标", p1.NextCursor)
	}
	if p1.BackwardsCursor != "2026-08-21T16:41:14.394Z" {
		t.Fatalf("backwardsCursor=%q", p1.BackwardsCursor)
	}

	// cursor 第二页：data 空且无 nextCursor = 翻页终止
	p2 := mustDecode[ThreadListPage](t, resp["13"])
	if len(p2.Data) != 0 || p2.NextCursor != "" {
		t.Fatalf("cursor 页应为空且无 nextCursor：%+v", p2)
	}

	// archived 列表：notLoaded + archived_sessions/ 路径，preview 保留
	arch := mustDecode[ThreadListPage](t, resp["15"])
	if len(arch.Data) != 1 {
		t.Fatalf("archived 应 1 条，得 %d", len(arch.Data))
	}
	a := arch.Data[0]
	if a.ID != "01a02532-b0e4-7a32-9e0d-76baaf659f08" {
		t.Fatalf("archived thread id=%s", a.ID)
	}
	if a.Status.Type != ThreadStatusNotLoaded {
		t.Fatalf("archived 列表 status=%s，期望 notLoaded", a.Status.Type)
	}
	if !strings.Contains(a.Path, "archived_sessions/") {
		t.Fatalf("archived path=%s", a.Path)
	}
	if a.Preview != "MOCK:STREAM filler turn 0" {
		t.Fatalf("archived preview=%q", a.Preview)
	}
	_ = byID
}

// TestCatalogFixtureReadTurns 冻结 thread/read(includeTurns) 解码（rename 后 + reasoning turn）。
func TestCatalogFixtureReadTurns(t *testing.T) {
	resp, _, _ := fixtureDump(t, "catalog")
	var rr struct {
		Thread ThreadInfo `json:"thread"`
	}
	th := mustDecode[struct {
		Thread ThreadInfo `json:"thread"`
	}](t, resp["19"]).Thread
	if th.ID != "01a02532-b09d-72d3-82af-654bf25e88b3" {
		t.Fatalf("thread id=%s", th.ID)
	}
	if th.Name == nil || *th.Name != "phase0-mock-thread" || th.Title() != "phase0-mock-thread" {
		t.Fatalf("rename 后 name/Title 解码错误：%v", th.Name)
	}
	if len(th.Turns) != 2 {
		t.Fatalf("turns 应 2，得 %d", len(th.Turns))
	}
	t2 := th.Turns[1]
	if t2.ID != "01a02532-b1f9-7b60-979b-f4e87a543da9" || t2.Status != TurnStatusCompleted {
		t.Fatalf("turn2 id/status 解码错误：%s/%s", t2.ID, t2.Status)
	}
	if t2.ItemsView != TurnItemsViewFull {
		t.Fatalf("itemsView=%s", t2.ItemsView)
	}
	if len(t2.Items) != 3 {
		t.Fatalf("turn2 items 应 3（userMessage/reasoning/agentMessage），得 %d", len(t2.Items))
	}
	var kinds []string
	for _, raw := range t2.Items {
		var it struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &it)
		kinds = append(kinds, it.Type)
	}
	if !reflect.DeepEqual(kinds, []string{"userMessage", "reasoning", "agentMessage"}) {
		t.Fatalf("item 类型序列=%v", kinds)
	}
	if t2.StartedAt == nil || *t2.StartedAt != 1787330474 || t2.CompletedAt == nil || *t2.CompletedAt != 1787330474 {
		t.Fatalf("turn2 时间解码错误")
	}
	if t2.DurationMs == nil || *t2.DurationMs != 20 {
		t.Fatalf("turn2 durationMs=%v", t2.DurationMs)
	}
	_ = rr
}

// captureParams 记录某 method 的请求 params 并回放固定结果。
func captureParams(s *scriptedTransport, method string, result any) *[]json.RawMessage {
	var captured []json.RawMessage
	s.mu.Lock()
	prev := s.onSend
	s.onSend = func(payload []byte) {
		if prev != nil {
			prev(payload)
		}
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &req); err != nil || req.Method != method {
			return
		}
		captured = append(captured, req.Params)
		if result != nil {
			frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			s.push(string(frame))
		}
	}
	s.mu.Unlock()
	return &captured
}

func expectParams(t *testing.T, raw json.RawMessage, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("params 非对象: %v (%s)", err, raw)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("params=%v，期望逐字段一致 %v", got, want)
	}
}

func u32ptr(v uint32) *uint32 { return &v }
func boolptr(v bool) *bool    { return &v }

// TestCatalogRequestShapesFrozen 请求字段逐字段冻结（与 dumps/catalog 客户端帧一致）。
func TestCatalogRequestShapesFrozen(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()
	go drainNotifications(c)

	emptyPage := ThreadListPage{Data: []ThreadInfo{}}
	capList := captureParams(s, "thread/list", emptyPage)

	cases := []struct {
		name   string
		params ListThreadsParams
		want   map[string]any
	}{
		{"limit-only", ListThreadsParams{Limit: u32ptr(50)}, map[string]any{"limit": float64(50)}},
		{"limit-cursor", ListThreadsParams{Limit: u32ptr(1), Cursor: "2026-08-22T00:41:14Z"}, map[string]any{"limit": float64(1), "cursor": "2026-08-22T00:41:14Z"}},
		{"limit-archived", ListThreadsParams{Limit: u32ptr(50), Archived: boolptr(true)}, map[string]any{"limit": float64(50), "archived": true}},
		{"cwd-filter", func() ListThreadsParams {
			p := ListThreadsParams{Limit: u32ptr(50)}
			p.SetCWDFilter([]string{"/a", "/b"})
			return p
		}(), map[string]any{"limit": float64(50), "cwd": []any{"/a", "/b"}}},
	}
	for _, tc := range cases {
		if _, _, err := ListThreads(context.Background(), c, tc.params); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if len(*capList) != len(cases) {
		t.Fatalf("捕获 %d 次，期望 %d", len(*capList), len(cases))
	}
	for i, tc := range cases {
		expectParams(t, (*capList)[i], tc.want)
	}

	// void 操作与 rename 的请求形状
	capVoid := captureParams(s, "thread/archive", map[string]any{})
	if rpcErr := ArchiveThread(context.Background(), c, "th-1"); rpcErr != nil {
		t.Fatalf("archive: %v", rpcErr)
	}
	expectParams(t, (*capVoid)[0], map[string]any{"threadId": "th-1"})

	capRename := captureParams(s, "thread/name/set", map[string]any{})
	captureParams(s, "thread/read", map[string]any{"thread": ThreadInfo{ID: "th-1"}})
	if _, rpcErr, err := SetThreadName(context.Background(), c, "th-1", "n"); err != nil || rpcErr != nil {
		t.Fatalf("rename: %v/%v", rpcErr, err)
	}
	expectParams(t, (*capRename)[0], map[string]any{"threadId": "th-1", "name": "n"})
}

func drainNotifications(c *Client) {
	for range c.Notifications() {
	}
}

// TestCatalogListAllThreadsPagination 官方 cursor 翻页聚合 + 环保护。
func TestCatalogListAllThreadsPagination(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()
	go drainNotifications(c)

	pageByCursor := func(cursor string) ThreadListPage {
		switch cursor {
		case "":
			return ThreadListPage{Data: []ThreadInfo{{ID: "a"}, {ID: "b"}}, NextCursor: "c1"}
		case "c1":
			return ThreadListPage{Data: []ThreadInfo{{ID: "c"}}}
		default:
			return ThreadListPage{Data: []ThreadInfo{}}
		}
	}
	s.mu.Lock()
	s.onSend = func(payload []byte) {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(payload, &req)
		if req.Method != "thread/list" {
			return
		}
		var p struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(req.Params, &p)
		page := pageByCursor(p.Cursor)
		frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": page})
		s.push(string(frame))
	}
	s.mu.Unlock()

	all, rpcErr, err := ListAllThreads(context.Background(), c, ListThreadsParams{Limit: u32ptr(2)})
	if err != nil || rpcErr != nil {
		t.Fatalf("list all: %v/%v", rpcErr, err)
	}
	if len(all) != 3 || all[0].ID != "a" || all[2].ID != "c" {
		t.Fatalf("聚合顺序错误：%v", all)
	}

	// cursor 重复（服务端异常）→ 显式错误，不无限循环
	s2 := newScripted()
	c2 := NewClient(s2, 1)
	defer c2.Close()
	go drainNotifications(c2)
	loopPage := ThreadListPage{Data: []ThreadInfo{{ID: "x"}}, NextCursor: "c1"}
	s2.mu.Lock()
	s2.onSend = func(payload []byte) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(payload, &req)
		if req.Method != "thread/list" {
			return
		}
		frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": loopPage})
		s2.push(string(frame))
	}
	s2.mu.Unlock()
	if _, _, err := ListAllThreads(context.Background(), c2, ListThreadsParams{}); err == nil || !strings.Contains(err.Error(), "cursor repeated") {
		t.Fatalf("cursor 环应显式报错，得 %v", err)
	}
}

// TestCatalogRenameNoLocalOptimism rename 结果以服务端重读为准。
func TestCatalogRenameNoLocalOptimism(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()
	go drainNotifications(c)

	serverName := "server-normalized"
	captureParams(s, "thread/name/set", map[string]any{})
	captureParams(s, "thread/read", map[string]any{"thread": ThreadInfo{ID: "th-1", Name: &serverName}})

	got, rpcErr, err := SetThreadName(context.Background(), c, "th-1", "user typed!!!")
	if err != nil || rpcErr != nil {
		t.Fatalf("rename: %v/%v", rpcErr, err)
	}
	if got == nil || *got != serverName {
		t.Fatalf("rename 确认应返回服务端观测值 %q，得 %v", serverName, got)
	}

	// 服务端 read 失败 → 返回官方错误，不回退本地猜测
	s3 := newScripted()
	c3 := NewClient(s3, 1)
	defer c3.Close()
	go drainNotifications(c3)
	s3.mu.Lock()
	s3.onSend = func(payload []byte) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(payload, &req)
		var frame []byte
		if req.Method == "thread/name/set" {
			frame, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		} else if req.Method == "thread/read" {
			frame, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": "thread not found"}})
		} else {
			return
		}
		s3.push(string(frame))
	}
	s3.mu.Unlock()
	if _, rpcErr, err := SetThreadName(context.Background(), c3, "th-x", "n"); err != nil || rpcErr == nil || rpcErr.Message != "thread not found" {
		t.Fatalf("read 失败应透传官方错误，得 err=%v rpcErr=%v", err, rpcErr)
	}
}

// TestOwnershipConflictTranslation -32600 writer 冲突翻译（dumps/ownership 原文）。
func TestOwnershipConflictTranslation(t *testing.T) {
	official := &RPCError{Code: -32600, Message: "thread 01a0256a-ecaf-7e92-b057-649b2b6eaaf9 already has an active writer"}
	if !IsOwnershipConflict(official) {
		t.Fatal("官方 writer 冲突应被识别")
	}
	if IsOwnershipConflict(&RPCError{Code: -32600, Message: "invalid request"}) {
		t.Fatal("非 writer 的 -32600 不应误判")
	}
	if IsOwnershipConflict(&RPCError{Code: -32601, Message: "method not found"}) {
		t.Fatal("非 -32600 不应误判")
	}

	oc := TranslateOwnershipConflict("thread/archive", SourceExternalDaemonReused, "th-9", official)
	if oc == nil {
		t.Fatal("应生成 OwnershipConflictError")
	}
	if oc.ThreadID != "th-9" || oc.Method != "thread/archive" || oc.TransportSource != SourceExternalDaemonReused ||
		oc.OfficialCode != -32600 || !strings.Contains(oc.OfficialMessage, "active writer") {
		t.Fatalf("冲突翻译字段缺失：%+v", oc)
	}
	if !strings.Contains(oc.Error(), "另一个 Codex app-server") ||
		!strings.Contains(oc.Error(), "打开着的该会话窗口") ||
		!strings.Contains(oc.Error(), "只读投影") ||
		!strings.Contains(oc.Error(), "不会终止") {
		t.Fatalf("共享 daemon 冲突提示应说明 writer 被同 daemon 另一客户端持有（2026-08-23 实测），且不抢进程：%s", oc.Error())
	}

	wrapped := errOwnershipOrRPC("thread/delete", SourceCordCodeStartedDaemon, "th-9", official, nil)
	if _, ok := asOwnership(wrapped); !ok {
		t.Fatal("errors.As 应命中 OwnershipConflictError")
	}
	// 非冲突官方错误原样保留
	plain := errOwnershipOrRPC("thread/delete", SourceCordCodeStartedDaemon, "th-9", &RPCError{Code: -32000, Message: "boom"}, nil)
	var rpcOut *RPCError
	if !errors.As(plain, &rpcOut) || rpcOut.Message != "boom" {
		t.Fatalf("非冲突错误应保留官方原文，得 %v", plain)
	}
}

// TestCatalogCtxCancel ctx 取消不泄漏 pending。
func TestCatalogCtxCancel(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()
	go drainNotifications(c)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, _, err := ListThreads(ctx, c, ListThreadsParams{Limit: u32ptr(1)}); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("取消的 ctx 应立即报错，得 %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("取消应即时生效")
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending 应清空，剩 %d", pending)
	}
}

// TestCatalogBadResponseShape 非对象响应显式报错（不 panic/不猜测）。
func TestCatalogBadResponseShape(t *testing.T) {
	s := newScripted()
	c := NewClient(s, 1)
	defer c.Close()
	go drainNotifications(c)

	s.mu.Lock()
	s.onSend = func(payload []byte) {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(payload, &req)
		if req.Method != "thread/list" {
			return
		}
		frame, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": []any{1, 2}})
		s.push(string(frame))
	}
	s.mu.Unlock()
	if _, _, err := ListThreads(context.Background(), c, ListThreadsParams{}); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("坏形状应报 decode 错误，得 %v", err)
	}
	_ = fmt.Sprint()
}

func TestThreadListDecodesLiveSectionShapes(t *testing.T) {
	raw, err := os.ReadFile("testdata/official-0.149.0-alpha.4/dumps/catalog-live/raw.jsonl")
	if err != nil {
		t.Fatalf("read live sample: %v", err)
	}
	var page ThreadListPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("live thread/list wire must decode: %v", err)
	}
	if len(page.Data) < 4 {
		t.Fatalf("sample rows missing: %d", len(page.Data))
	}
	objectSection := 0
	for _, th := range page.Data {
		switch {
		case th.Section != nil && th.Section.ID != "":
			objectSection++
		case th.Section == nil:
			// null section must stay nil, not error out
		default:
			t.Fatalf("unexpected section value shape: %+v", th.Section)
		}
	}
	if objectSection == 0 {
		t.Fatal("live sample must include object-shaped section rows")
	}
}

func TestThreadSectionDecodeAcceptsLegacyString(t *testing.T) {
	var th ThreadInfo
	if err := json.Unmarshal([]byte(`{"id":"t1","section":"Pinned","status":{"type":"idle"}}`), &th); err != nil {
		t.Fatalf("legacy string section must decode: %v", err)
	}
	if th.Section == nil || th.Section.Name != "Pinned" {
		t.Fatalf("want name Pinned, got %+v", th.Section)
	}
}

// TestIsConnectionLossCoversDeadSocketShapes — daemon restart 后旧连接写出即
// broken pipe（"write: broken pipe" / syscall.EPIPE 包装）；漏判则 withClient 永不
// 重 Probe（2026-08-23 真机回归：水化/目录/指纹全部停在死连接直到 MacBridge 重启）。
func TestIsConnectionLossCoversDeadSocketShapes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"write broken pipe text", fmt.Errorf("write unix ->/x/app-server-control.sock: write: broken pipe"), true},
		{"syscall EPIPE wrapped", &os.SyscallError{Syscall: "write", Err: syscall.EPIPE}, true},
		{"connection reset text", fmt.Errorf("read unix ->/x.sock: read: connection reset by peer"), true},
		{"connection refused text", fmt.Errorf("dial unix /x.sock: connect: connection refused"), true},
		{"closed network connection", fmt.Errorf("use of closed network connection"), true},
		{"ws close", fmt.Errorf("websocket: close 1005"), true},
		{"connection closed", fmt.Errorf("connection closed"), true},
		{"nil", nil, false},
		{"rpc rejection must stay out", &RPCError{Code: -32000, Message: "boom"}, false},
	}
	for _, c := range cases {
		if got := isConnectionLoss(c.err); got != c.want {
			t.Fatalf("%s: isConnectionLoss=%v want %v (%v)", c.name, got, c.want, c.err)
		}
	}
}
