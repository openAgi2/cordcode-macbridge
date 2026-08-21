package codexweb

// sessions_e2e_test.go —— p2-catalog-regression：隔离 CODEX_HOME 真实官方 app-server
// 上的 list/翻页/archive/rename/delete round-trip，形状必须与 fixture 冻结一致。
//
// 门控 CODEXWEB_E2E=1。不依赖 mock provider（thread/start/list/archive/rename/delete
// 不发起模型调用）；thread 创建用测试内联的官方 thread/start（产品 session 组件属 Phase 3）。

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// e2eStartThread 用官方 thread/start 建一个目录 thread（测试脚手架，非产品路径）。
func e2eStartThread(t *testing.T, cl *Client, cwd string) string {
	t.Helper()
	raw, rpcErr, err := cl.RequestContext(context.Background(), "thread/start", map[string]any{
		"cwd": cwd,
	})
	if err != nil || rpcErr != nil {
		t.Fatalf("thread/start: %v / %v", err, rpcErr)
	}
	var resp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.Thread.ID == "" {
		t.Fatalf("thread/start decode: %v (%s)", err, raw)
	}
	return resp.Thread.ID
}

// e2eMaterialize 发一个 turn 使 thread 物化（§22-5：无 turn 的 thread 不进 thread/list；
// mock provider 不可达 → turn 快速失败，但持久化已发生，与 turn 是否完成无关）。
func e2eMaterialize(t *testing.T, cl *Client, threadID string) {
	t.Helper()
	_, rpcErr, err := cl.RequestContext(context.Background(), "turn/start", map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": "MOCK: e2e materialize"}},
	})
	if err != nil || rpcErr != nil {
		t.Fatalf("turn/start(%s): %v / %v", threadID, err, rpcErr)
	}
}

func TestE2ECatalogRoundTrip(t *testing.T) {
	if !e2eEnabled(t) {
		return
	}
	_, home, workDir := e2eSetup(t)

	ep, err := Probe(ProbeOptions{CodexHome: home, WorkDir: workDir})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer func() {
		if err := ep.Close(); err != nil {
			t.Logf("endpoint close: %v", err)
		}
	}()
	cl := ep.Client()
	ctx := context.Background()

	// 1. 初始空列表
	page, rpcErr, err := ListThreads(ctx, cl, ListThreadsParams{Limit: u32ptr(50)})
	if err != nil || rpcErr != nil {
		t.Fatalf("初始 list: %v/%v", rpcErr, err)
	}
	if len(page.Data) != 0 || page.NextCursor != "" {
		t.Fatalf("全新 HOME 列表应为空：%+v", page)
	}

	// 2. 建 3 个 thread 并各发一个 turn 物化（§22-5）→ 官方 desc 排序（最新在前）
	ids := []string{e2eStartThread(t, cl, workDir), e2eStartThread(t, cl, workDir), e2eStartThread(t, cl, workDir)}
	for _, id := range ids {
		e2eMaterialize(t, cl, id)
	}
	// 空 thread（从未有 turn）不进 thread/list——官方物化语义的直接断言
	emptyID := e2eStartThread(t, cl, workDir)
	var all []ThreadInfo
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		// 服务端默认页大小（单页覆盖）：不传 limit，避免触发官方同秒 cursor 跳过（§22-6）
		all, rpcErr, err = ListAllThreads(ctx, cl, ListThreadsParams{})
		if err != nil || rpcErr != nil {
			t.Fatalf("list all: %v/%v", rpcErr, err)
		}
		if len(all) == 3 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(all) != 3 {
		t.Fatalf("应 3 条，得 %d", len(all))
	}
	// 官方同秒 cursor 边界（§22-6）：limit=1 首页后，同秒创建的其余条目会被
	// cursor 严格小于比较跳过——这是官方行为，聚合层不得用小页深翻页补全。
	p1, _, err := ListThreads(ctx, cl, ListThreadsParams{Limit: u32ptr(1)})
	if err != nil {
		t.Fatalf("limit=1: %v", err)
	}
	if len(p1.Data) != 1 || p1.NextCursor == "" {
		t.Fatalf("limit=1 首页应 1 条 + nextCursor：%+v", p1)
	}
	p2, _, err := ListThreads(ctx, cl, ListThreadsParams{Limit: u32ptr(1), Cursor: p1.NextCursor})
	if err != nil {
		t.Fatalf("cursor 页: %v", err)
	}
	if len(p2.Data) > 1 {
		t.Fatalf("同秒 cursor 页最多 1 条（跳过语义），得 %d", len(p2.Data))
	}
	for _, th := range all {
		if th.ID == emptyID {
			t.Fatal("无 turn 的 thread 不应出现在 thread/list（§22-5 物化语义）")
		}
	}
	if all[0].ID != ids[2] || all[2].ID != ids[0] {
		t.Fatalf("官方 desc 排序错误：首=%s 尾=%s（创建序 %v）", all[0].ID, all[2].ID, ids)
	}
	for _, th := range all {
		// thread status 的精确语义（active/idle/failed 轮廓）属 Phase 3 turn 域；
		// 此处只校验官方枚举不越界 + cwd 透传。
		switch th.Status.Type {
		case ThreadStatusActive, ThreadStatusIdle, ThreadStatusNotLoaded, ThreadStatusSystemError:
		default:
			t.Fatalf("未知 thread status：%q", th.Status.Type)
		}
		if th.Cwd != workDir {
			t.Fatalf("cwd=%s 期望 %s", th.Cwd, workDir)
		}
	}

	// 3. rename → 重读确认（服务端真相，非本地乐观）
	got, rpcErr, err := SetThreadName(ctx, cl, ids[0], "e2e-catalog-name")
	if err != nil || rpcErr != nil {
		t.Fatalf("rename: %v/%v", rpcErr, err)
	}
	if got == nil || *got != "e2e-catalog-name" {
		t.Fatalf("rename 重读确认失败：%v", got)
	}

	// 4. archive → 非归档列表消失、archived 列表出现（notLoaded 语义）
	if rpcErr := ArchiveThread(ctx, cl, ids[1]); rpcErr != nil {
		t.Fatalf("archive: %v", rpcErr)
	}
	normal, rpcErr, err := ListAllThreads(ctx, cl, ListThreadsParams{Limit: u32ptr(50)})
	if err != nil || rpcErr != nil {
		t.Fatalf("archive 后 list: %v/%v", rpcErr, err)
	}
	for _, th := range normal {
		if th.ID == ids[1] {
			t.Fatal("已归档 thread 不应出现在默认列表")
		}
	}
	archived, rpcErr, err := ListAllThreads(ctx, cl, ListThreadsParams{Limit: u32ptr(50), Archived: boolptr(true)})
	if err != nil || rpcErr != nil {
		t.Fatalf("archived list: %v/%v", rpcErr, err)
	}
	if len(archived) != 1 || archived[0].ID != ids[1] {
		t.Fatalf("archived 应只有 ids[1]：%v", archived)
	}
	if archived[0].Status.Type != ThreadStatusNotLoaded {
		t.Fatalf("archived 条目应为 notLoaded（fixture 冻结），得 %s", archived[0].Status.Type)
	}

	// 5. unarchive → 回到默认列表
	if rpcErr := UnarchiveThread(ctx, cl, ids[1]); rpcErr != nil {
		t.Fatalf("unarchive: %v", rpcErr)
	}
	normal2, _, err := ListAllThreads(ctx, cl, ListThreadsParams{Limit: u32ptr(50)})
	if err != nil {
		t.Fatalf("unarchive 后 list: %v", err)
	}
	found := false
	for _, th := range normal2 {
		if th.ID == ids[1] {
			found = true
		}
	}
	if !found || len(normal2) != 3 {
		t.Fatalf("unarchive 后应回到 3 条：%d found=%v", len(normal2), found)
	}

	// 6. cwd 官方过滤（exact match）：命中 1 条、非命中 0 条
	filtered, _, err := ListAllThreads(ctx, cl, func() ListThreadsParams {
		p := ListThreadsParams{Limit: u32ptr(50)}
		p.SetCWDFilter([]string{workDir})
		return p
	}())
	if err != nil {
		t.Fatalf("cwd 过滤: %v", err)
	}
	if len(filtered) != 3 {
		t.Fatalf("cwd=%s 应命中 3 条，得 %d", workDir, len(filtered))
	}
	missed, _, err := ListAllThreads(ctx, cl, func() ListThreadsParams {
		p := ListThreadsParams{Limit: u32ptr(50)}
		p.SetCWDFilter([]string{workDir + "/nonexistent"})
		return p
	}())
	if err != nil {
		t.Fatalf("cwd 非命中: %v", err)
	}
	if len(missed) != 0 {
		t.Fatalf("cwd 非命中应为空，得 %d", len(missed))
	}

	// 7. delete → 列表减少；被删 thread 不再出现
	if rpcErr := DeleteThread(ctx, cl, ids[2]); rpcErr != nil {
		t.Fatalf("delete: %v", rpcErr)
	}
	delDeadline := time.Now().Add(10 * time.Second)
	deleted := false
	for time.Now().Before(delDeadline) {
		final, _, err := ListAllThreads(ctx, cl, ListThreadsParams{Limit: u32ptr(50)})
		if err != nil {
			t.Fatalf("delete 后 list: %v", err)
		}
		if len(final) == 2 {
			deleted = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !deleted {
		final, _, _ := ListAllThreads(ctx, cl, ListThreadsParams{Limit: u32ptr(50)})
		t.Fatalf("delete 后应剩 2 条，得 %d", len(final))
	}
	_ = os.Environ
}
