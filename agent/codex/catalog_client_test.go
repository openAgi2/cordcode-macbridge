package codex

// catalog_client_test.go 验证 Phase 2 Stream A 的 catalog client（设计 §5.1 step 1-4）。
//
// 覆盖（跨平台部分）：
//  1. 字段映射：codexThreadToAgentSessionInfo 对冻结 fixture 的每条 thread 正确映射
//     （id/name→title/cwd→directory/updatedAt→modifiedAt/providerID/gitBranch），且 name
//     空时回退 preview（§5.1 step 4）。
//  2. 冻结 scope 参数：frozenThreadListScopeParams 产出 Phase 0 冻结值。
//  3. ws 传输端到端：in-process ws fake app-server 跑完整 initialize + thread/list 握手，
//     证明 JSON-RPC 收发骨架（request/readLoop/handleResponse/pending 相关性）+ thread/list
//     + 映射正确串联（不依赖真实 codex 子进程）。
//  4. 传输选型：appServerURLSet=true→ws；否则→stdio（§4.3 冻结）。
//  5. 连接死亡显式失败：ws 断开后 listThreads 返回明确 error（§5.1 step 6：删除静默回退）。
//
// stdio 子进程生命周期（进程组回收）测试在 catalog_client_stdio_test.go（//go:build unix）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mapFrozenFixtureThreads 把冻结 fixture 的 response.result.data 反序列化成 []codexThread。
func mapFrozenFixtureThreads(t *testing.T) []codexThread {
	t.Helper()
	doc := loadThreadListFixture(t)
	var resp struct {
		Result struct {
			Data []codexThread `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(doc.Response, &resp); err != nil {
		t.Fatalf("unmarshal fixture response: %v", err)
	}
	if len(resp.Result.Data) == 0 {
		t.Fatal("fixture data empty")
	}
	return resp.Result.Data
}

// TestCodexThreadMapping_FrozenFixture 锁定 thread/list → AgentSessionInfo 字段映射（§5.1 step 3）。
func TestCodexThreadMapping_FrozenFixture(t *testing.T) {
	threads := mapFrozenFixtureThreads(t)
	for _, th := range threads {
		info := codexThreadToAgentSessionInfo(th)
		if info.ID != th.ID {
			t.Errorf("ID = %q, want thread id %q", info.ID, th.ID)
		}
		// 标题优先 name（§5.1 step 4）。
		wantTitle := th.Name
		if wantTitle == "" {
			wantTitle = th.Preview
		}
		if info.Summary != wantTitle {
			t.Errorf("Summary = %q, want %q (name, fallback preview)", info.Summary, wantTitle)
		}
		if info.Directory != th.Cwd {
			t.Errorf("Directory = %q, want cwd %q", info.Directory, th.Cwd)
		}
		if info.ProviderID != th.ModelProvider {
			t.Errorf("ProviderID = %q, want modelProvider %q", info.ProviderID, th.ModelProvider)
		}
		if info.GitBranch != th.GitInfo.Branch {
			t.Errorf("GitBranch = %q, want gitInfo.branch %q", info.GitBranch, th.GitInfo.Branch)
		}
		// updatedAt unix 秒 → ModifiedAt。
		if want := codexThreadUnixTime(th.UpdatedAt); !info.ModifiedAt.Equal(want) {
			t.Errorf("ModifiedAt = %v, want %v (updatedAt=%d sec)", info.ModifiedAt, want, th.UpdatedAt)
		}
		// catalog 不携带 message count；不得猜（§4.2 / 护栏 #6）。
		if info.MessageCount != 0 {
			t.Errorf("MessageCount = %d, want 0（catalog 不猜消息数）", info.MessageCount)
		}
	}
}

// TestCodexThreadMapping_NameEmptyFallsBackToPreview 锁定 name 空时用 preview（§5.1 step 4）。
func TestCodexThreadMapping_NameEmptyFallsBackToPreview(t *testing.T) {
	th := codexThread{ID: "x", Preview: "first user prompt", Name: ""}
	info := codexThreadToAgentSessionInfo(th)
	if info.Summary != "first user prompt" {
		t.Fatalf("Summary = %q, want preview fallback", info.Summary)
	}
}

// TestFrozenThreadListScopeParams 锁定 Phase 0 冻结的 thread/list scope 参数（§3.1）。
func TestFrozenThreadListScopeParams(t *testing.T) {
	p := frozenThreadListScopeParams("/tmp/fixture-workspace")
	if p.Cwd != "/tmp/fixture-workspace" {
		t.Errorf("Cwd = %q", p.Cwd)
	}
	if p.Archived != false {
		t.Errorf("Archived = %v, want false", p.Archived)
	}
	if p.Source != "interactive" {
		t.Errorf("Source = %q, want interactive", p.Source)
	}
	if p.SortKey != "recency_at" {
		t.Errorf("SortKey = %q, want recency_at", p.SortKey)
	}
	if p.SortDirection != "desc" {
		t.Errorf("SortDirection = %q, want desc", p.SortDirection)
	}
}

// TestCodexThreadUnixTime 锁定 unix 秒 → time.Time 转换（0/负值零值）。
func TestCodexThreadUnixTime(t *testing.T) {
	if got := codexThreadUnixTime(0); !got.IsZero() {
		t.Errorf("0 → %v, want zero", got)
	}
	if got := codexThreadUnixTime(-1); !got.IsZero() {
		t.Errorf("-1 → %v, want zero", got)
	}
	got := codexThreadUnixTime(1784082325)
	if got.IsZero() || got.Year() < 2020 || got.Year() > 2030 {
		t.Errorf("1784082325 → %v, want ~2026 (unix 秒)", got)
	}
}

// fakeCatalogWSServer 起一个 in-process ws server 模拟 codex app-server：
//   - initialize 请求 → 回 protocolVersion；
//   - initialized 通知 → 不回；
//   - thread/list 请求 → 回冻结 fixture 的 result.data + nextCursor。
//
// 返回 (url, stop)。stop 强制关闭所有活跃 ws 连接 + server（httptest.Server.Close 不会主动
// 中断阻塞在 ReadMessage 的 handler goroutine，故需显式关 conn 以触发客户端 readLoop 错误）。
func fakeCatalogWSServer(t *testing.T, listResult threadListResult) (string, func()) {
	t.Helper()
	var upgrader websocket.Upgrader
	var connMu sync.Mutex
	var conns []*websocket.Conn
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connMu.Lock()
		conns = append(conns, conn)
		connMu.Unlock()
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				ID     any             `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if msg.Method == "" {
				continue // notification（initialized）→ 不回
			}
			// request → 回 result。
			var result any
			switch msg.Method {
			case "initialize":
				result = map[string]any{"protocolVersion": "2025-11-01-codex"}
			case "thread/list":
				result = listResult
			default:
				result = struct{}{}
			}
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result":  result,
			})
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	})
	server := httptest.NewServer(mux)
	stop := func() {
		connMu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		conns = nil
		connMu.Unlock()
		server.Close()
	}
	return "ws" + server.URL[len("http"):], stop
}

// TestCatalogClient_WS_HandshakeAndListThreads 证明 ws 传输下 catalog client 完整跑通
// initialize + thread/list 并正确映射（不依赖真实 codex 子进程）。
func TestCatalogClient_WS_HandshakeAndListThreads(t *testing.T) {
	threads := mapFrozenFixtureThreads(t)
	listResult := threadListResult{Data: threads, NextCursor: "2026-07-14T00:00:00Z|" + threads[0].ID}
	url, stop := fakeCatalogWSServer(t, listResult)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := newCatalogClient(ctx, catalogClientConfig{
		appServerURL:    url,
		appServerURLSet: true, // → ws 传输
	})
	if err != nil {
		t.Fatalf("newCatalogClient(ws): %v", err)
	}
	defer c.Close()

	if c.transport != appServerTransportWebSocket {
		t.Fatalf("transport = %q, want websocket", c.transport)
	}
	if c.cmd != nil {
		t.Fatalf("cmd non-nil in ws mode (应为无子进程)")
	}

	got, err := c.listThreads(ctx, frozenThreadListScopeParams("/tmp/fixture-workspace"))
	if err != nil {
		t.Fatalf("listThreads: %v", err)
	}
	if len(got.Data) != len(threads) {
		t.Fatalf("data len = %d, want %d", len(got.Data), len(threads))
	}
	// 顺序与上游一致（§4.2：provider 保持上游序，不本地重排）。
	for i, th := range got.Data {
		info := codexThreadToAgentSessionInfo(th)
		if info.ID != threads[i].ID {
			t.Errorf("data[%d].ID = %q, want %q", i, info.ID, threads[i].ID)
		}
	}
	if got.NextCursor != listResult.NextCursor {
		t.Errorf("NextCursor = %q, want %q", got.NextCursor, listResult.NextCursor)
	}
}

// TestCatalogClient_WSDeadReturnsExplicitError 锁定 §5.1 step 6：连接死亡后 listThreads
// 返回明确 error，不静默回退。
func TestCatalogClient_WSDeadReturnsExplicitError(t *testing.T) {
	threads := mapFrozenFixtureThreads(t)
	listResult := threadListResult{Data: threads}
	url, stop := fakeCatalogWSServer(t, listResult)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := newCatalogClient(ctx, catalogClientConfig{appServerURL: url, appServerURLSet: true})
	if err != nil {
		t.Fatalf("newCatalogClient: %v", err)
	}
	// 第一次 list 成功。
	if _, err := c.listThreads(ctx, frozenThreadListScopeParams("/x")); err != nil {
		t.Fatalf("first listThreads: %v", err)
	}
	// 关掉 server 模拟连接死亡。
	stop()
	// 等待 readLoop 感知断开。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && c.Alive() {
		time.Sleep(20 * time.Millisecond)
	}
	if c.Alive() {
		t.Fatal("client 仍 alive，断开未感知")
	}
	// 死亡后 listThreads 必须显式失败（不静默回退 JSONL）。
	if _, err := c.listThreads(ctx, frozenThreadListScopeParams("/x")); err == nil {
		t.Fatal("dead client listThreads 返回 nil error，应显式失败（§5.1 step 6）")
	}
	c.Close()
}
