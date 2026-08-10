package gobridge

// catalog_wire_snapshot_test.go 验证 Phase 1 chunk 1C：cursor v2 live-path wiring (capability-gated)。
//
// 三组不变量：
//  1. capability 门控（§10 发布顺序）：SetConnCatalogCursorEpochV2/ConnCatalogCursorEpochV2 往返 +
//     连接隔离（一个连接声明不影响另一个）+ UnregisterConnection 清除 + helloSupportsCatalogCursorEpochV2
//     检测字符串。
//  2. wire-map 快照缓存（§4.1.1）：多页切片无漂移、TTL/epoch-mismatch/no-snapshot/v1 → cursor_stale
//     （不盲切不静默回 page-0）、损坏 cursor → 硬错误、singleflight、stale 是 Retryable WireError。
//  3. 无 v1 泄漏（§10 关键不变量）：声明路径发射的 nextCursor 一律 v2（version=2 + 携带 epoch）。
//
// 用 publisherCaptureConn 作 fake Connection（已实现 Connection 接口）；cache 测试用可控 builder +
// fake clock，不触网络、不启动 live `opencode serve`。handler gate 本身由 capability 门控测试覆盖
// （undeclared → v1 路径，结构上不可能到达 pageV2）。

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// ── 1. capability 门控 ─────────────────────────────────────────────────────────

// TestCatalogCursorEpochV2_RoundTripAndIsolation：声明只对被声明的连接生效；另一个连接不受影响。
func TestCatalogCursorEpochV2_RoundTripAndIsolation(t *testing.T) {
	ep := NewEventPublisher("epoch-catalog-isolation")
	connA := newPublisherCaptureConn(nil)
	connB := newPublisherCaptureConn(nil)

	if ep.ConnCatalogCursorEpochV2(connA) || ep.ConnCatalogCursorEpochV2(connB) {
		t.Fatal("初始状态不应有任何连接声明 v2")
	}

	ep.SetConnCatalogCursorEpochV2(connA, true)
	if !ep.ConnCatalogCursorEpochV2(connA) {
		t.Fatal("connA 声明后应报告 v2 = true")
	}
	if ep.ConnCatalogCursorEpochV2(connB) {
		t.Fatal("connB 未声明，应仍为 false（连接隔离）")
	}

	// 关闭（设 false）后再查应为 false。
	ep.SetConnCatalogCursorEpochV2(connA, false)
	if ep.ConnCatalogCursorEpochV2(connA) {
		t.Fatal("connA 关闭后应报告 v2 = false")
	}
}

// TestCatalogCursorEpochV2_UnregisterClears：连接注销后 capability 标记被清除（替换连接须重新协商）。
func TestCatalogCursorEpochV2_UnregisterClears(t *testing.T) {
	ep := NewEventPublisher("epoch-catalog-unregister")
	conn := newPublisherCaptureConn(nil)
	ep.SetConnCatalogCursorEpochV2(conn, true)
	if !ep.ConnCatalogCursorEpochV2(conn) {
		t.Fatal("声明后应为 true")
	}
	ep.UnregisterConnection(conn) // UnregisterConnection 对未注册 sink 安全（sink==nil 跳过 close）
	if ep.ConnCatalogCursorEpochV2(conn) {
		t.Fatal("UnregisterConnection 后应清除 capability 标记")
	}
}

// TestHelloSupportsCatalogCursorEpochV2：检测 Capabilities 列表中的 catalog_cursor_epoch_v2 字符串。
func TestHelloSupportsCatalogCursorEpochV2(t *testing.T) {
	cases := []struct {
		name string
		caps []string
		want bool
	}{
		{"declared", []string{"session_sync_v2", "catalog_cursor_epoch_v2", "read_file_v2"}, true},
		{"declared-only", []string{"catalog_cursor_epoch_v2"}, true},
		{"absent", []string{"session_sync_v2", "read_file_v2"}, false},
		{"empty", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hello := &HelloMessage{Capabilities: tc.caps}
			if got := helloSupportsCatalogCursorEpochV2(hello); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// ── 2. wire-map 快照缓存 ───────────────────────────────────────────────────────

// synthWireMaps 构造 n 个 enriched wire map：id=ses_II，updatedAtMillis 严格递减（与
// sortSessionsByUpdatedAt 输出序一致），含 mapSession 产物必需的 id/updatedAtMillis 字段。
func synthWireMaps(n int) []map[string]interface{} {
	out := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		out[i] = map[string]interface{}{
			"id":               fmt.Sprintf("ses_%02d", i),
			"updatedAtMillis":  int64(1_000_000 - i*1000),
			"createdAtMillis":  int64(1_000_000 - i*1000 - 500),
			"title":            fmt.Sprintf("session %d", i),
			"directory":        "/tmp/fixture-workspace",
		}
	}
	return out
}

// synthWireMapsDrift 产生一组不同的 wire map（不同 id/updatedAt），用于证明 page-N 切的是
// page-0 冻结的旧快照而非重新读取。
func synthWireMapsDrift() []map[string]interface{} {
	out := make([]map[string]interface{}, 5)
	for i := 0; i < 5; i++ {
		out[i] = map[string]interface{}{
			"id":              fmt.Sprintf("DRIFT_%02d", i),
			"updatedAtMillis": int64(9_000_000 - i*1000),
		}
	}
	return out
}

func newWireCacheWithFakeNow(now time.Time) (*catalogWireSnapshotCache, *time.Time) {
	t := now
	c := newCatalogWireSnapshotCache(func() time.Time { return t })
	return c, &t
}

// builderFromMaps 返回一个闭包 builder，每次调用返回 maps 的拷贝并计数。
func builderFromMaps(maps []map[string]interface{}, callCount *int) func() ([]map[string]interface{}, error) {
	return func() ([]map[string]interface{}, error) {
		*callCount++
		return append([]map[string]interface{}(nil), maps...), nil
	}
}

// TestCatalogWireSnapshot_MultiPageNoDrift：同一 cursor 链多页来自同一有序集合，拼回无重无缺、
// 序保持；page-N 切的是 page-0 冻结快照，builder 底层数据之后变化也不漂移（§4.1.1 / §11）。
func TestCatalogWireSnapshot_MultiPageNoDrift(t *testing.T) {
	calls := 0
	cache, _ := newWireCacheWithFakeNow(time.UnixMilli(2_000_000))
	scope := openCodeCatalogScopeKey("opencode", "/tmp/fixture-workspace", false)
	builder := builderFromMaps(synthWireMaps(5), &calls)

	p0, staleErr, err := cache.pageV2(scope, "", 2, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("page0: err=%v staleErr=%v", err, staleErr)
	}
	if len(p0["sessions"].([]map[string]interface{})) != 2 || p0["hasMore"] != true || p0["nextCursor"] == nil || p0["nextCursor"] == "" {
		t.Fatalf("page0 = %+v, want 2 sessions + hasMore + cursor", p0)
	}
	cursor0, _ := p0["nextCursor"].(string)

	p1, staleErr, err := cache.pageV2(scope, cursor0, 2, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("page1: err=%v staleErr=%v", err, staleErr)
	}
	cursor1, _ := p1["nextCursor"].(string)
	p2, staleErr, err := cache.pageV2(scope, cursor1, 2, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("page2: err=%v staleErr=%v", err, staleErr)
	}
	if p2["hasMore"] == true || p2["nextCursor"] != nil {
		t.Fatalf("page2 应是末页，got hasMore=%v nextCursor=%v", p2["hasMore"], p2["nextCursor"])
	}
	// page-0 / page-N：page-0 调一次 builder；page-N 走 Peek，不再调 builder。
	if calls != 1 {
		t.Fatalf("builder calls = %d, want 1（page-N 须 Peek 不重建）", calls)
	}

	// 拼回完整集合，断言无重无缺、序保持。
	var got []string
	for _, p := range []map[string]interface{}{p0, p1, p2} {
		for _, m := range p["sessions"].([]map[string]interface{}) {
			got = append(got, m["id"].(string))
		}
	}
	want := []string{"ses_00", "ses_01", "ses_02", "ses_03", "ses_04"}
	if len(got) != len(want) {
		t.Fatalf("拼接页 = %v, want %v", got, want)
	}
	seen := map[string]int{}
	for i, id := range got {
		if id != want[i] {
			t.Errorf("拼接页[%d] = %q, want %q", i, id, want[i])
		}
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("拼接页出现重复 %q ×%d（跨页漂移）", id, n)
		}
	}

	// 漂移测试：换 builder 底层数据，page-N（Peek）必须仍切旧快照、不漂移。
	driftCalls := 0
	p1b, staleErr, err := cache.pageV2(scope, cursor0, 2, builderFromMaps(synthWireMapsDrift(), &driftCalls))
	if err != nil || staleErr != nil {
		t.Fatalf("page1b (post-mutation): err=%v staleErr=%v", err, staleErr)
	}
	if driftCalls != 0 {
		t.Errorf("page-N 不应调 drift builder（应 Peek 旧快照），calls=%d", driftCalls)
	}
	for i, m := range p1b["sessions"].([]map[string]interface{}) {
		if m["id"] != want[i+2] {
			t.Errorf("page1b[%d].id = %q, want %q（page-N 不应受 builder 数据变化漂移）", i, m["id"], want[i+2])
		}
	}
}

// TestCatalogWireSnapshot_TTLExpiredCursorStale：page-0 后推进时钟超过 catalogSnapshotTTL，
// page-N cursor 命中 cursorStaleExpired。
func TestCatalogWireSnapshot_TTLExpiredCursorStale(t *testing.T) {
	calls := 0
	cache, clock := newWireCacheWithFakeNow(time.UnixMilli(2_000_000))
	scope := openCodeCatalogScopeKey("opencode", "/d", false)
	builder := builderFromMaps(synthWireMaps(3), &calls)

	p0, _, err := cache.pageV2(scope, "", 1, builder)
	if err != nil {
		t.Fatalf("page0: %v", err)
	}
	cursor0, _ := p0["nextCursor"].(string)
	*clock = clock.Add(catalogSnapshotTTL + time.Minute)

	_, staleErr, err := cache.pageV2(scope, cursor0, 1, builder)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if staleErr == nil || staleErr.Code != "cursor_stale" || staleErr.Message != cursorStaleExpired {
		t.Fatalf("got staleErr=%+v, want Code=cursor_stale Message=%q", staleErr, cursorStaleExpired)
	}
}

// TestCatalogWireSnapshot_EpochMismatchCursorStale：cursor 携带的外来 epoch 与快照不一致 →
// cursorStaleEpochMismatch（fingerprint 变化即成员/updatedAt 变化）。
func TestCatalogWireSnapshot_EpochMismatchCursorStale(t *testing.T) {
	calls := 0
	cache, _ := newWireCacheWithFakeNow(time.UnixMilli(2_000_000))
	scope := openCodeCatalogScopeKey("opencode", "/d", false)
	if _, _, err := cache.pageV2(scope, "", 1, builderFromMaps(synthWireMaps(3), &calls)); err != nil {
		t.Fatalf("page0: %v", err)
	}
	foreign, err := encodeListCursorV2(listCursorV2{Epoch: "deadbeef", UpdatedAtMillis: 999_999, SessionID: "ses_00"})
	if err != nil {
		t.Fatalf("encode foreign cursor: %v", err)
	}
	_, staleErr, err := cache.pageV2(scope, foreign, 1, builderFromMaps(synthWireMaps(3), &calls))
	if err != nil {
		t.Fatalf("pageV2: %v", err)
	}
	if staleErr == nil || staleErr.Message != cursorStaleEpochMismatch {
		t.Fatalf("got staleErr=%+v, want Message=%q", staleErr, cursorStaleEpochMismatch)
	}
}

// TestCatalogWireSnapshot_NoSnapshotCursorStale：scope 从未 page-0，page-N 直接来 → cursorStaleNoSnapshot。
func TestCatalogWireSnapshot_NoSnapshotCursorStale(t *testing.T) {
	calls := 0
	cache, _ := newWireCacheWithFakeNow(time.UnixMilli(2_000_000))
	scope := openCodeCatalogScopeKey("opencode", "/d", false)
	cur, err := encodeListCursorV2(listCursorV2{Epoch: "cafebabe", UpdatedAtMillis: 999_999, SessionID: "ses_00"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	_, staleErr, err := cache.pageV2(scope, cur, 1, builderFromMaps(synthWireMaps(3), &calls))
	if err != nil {
		t.Fatalf("pageV2: %v", err)
	}
	if staleErr == nil || staleErr.Message != cursorStaleNoSnapshot {
		t.Fatalf("got staleErr=%+v, want Message=%q", staleErr, cursorStaleNoSnapshot)
	}
}

// TestCatalogWireSnapshot_V1CursorStale：v2 模式下 v1 cursor 无 epoch → cursorStaleV1。
func TestCatalogWireSnapshot_V1CursorStale(t *testing.T) {
	calls := 0
	cache, _ := newWireCacheWithFakeNow(time.UnixMilli(2_000_000))
	scope := openCodeCatalogScopeKey("opencode", "/d", false)
	if _, _, err := cache.pageV2(scope, "", 1, builderFromMaps(synthWireMaps(3), &calls)); err != nil {
		t.Fatalf("page0: %v", err)
	}
	v1, err := encodeListCursor(listCursor{UpdatedAtMillis: 999_999, SessionID: "ses_00"})
	if err != nil {
		t.Fatalf("encode v1: %v", err)
	}
	_, staleErr, err := cache.pageV2(scope, v1, 1, builderFromMaps(synthWireMaps(3), &calls))
	if err != nil {
		t.Fatalf("pageV2: %v", err)
	}
	if staleErr == nil || staleErr.Message != cursorStaleV1 {
		t.Fatalf("got staleErr=%+v, want Message=%q", staleErr, cursorStaleV1)
	}
}

// TestCatalogWireSnapshot_MalformedCursorIsError：损坏 cursor 返回硬错误（err），不当作 stale。
func TestCatalogWireSnapshot_MalformedCursorIsError(t *testing.T) {
	calls := 0
	cache, _ := newWireCacheWithFakeNow(time.UnixMilli(2_000_000))
	scope := openCodeCatalogScopeKey("opencode", "/d", false)
	if _, _, err := cache.pageV2(scope, "", 1, builderFromMaps(synthWireMaps(3), &calls)); err != nil {
		t.Fatalf("page0: %v", err)
	}
	_, staleErr, err := cache.pageV2(scope, "!!!not-base64!!!", 1, builderFromMaps(synthWireMaps(3), &calls))
	if err == nil {
		t.Fatalf("损坏 cursor 应返回硬错误，got err=nil staleErr=%+v", staleErr)
	}
	if staleErr != nil {
		t.Fatalf("损坏 cursor 不应是 cursor_stale，got staleErr=%+v", staleErr)
	}
}

// TestCatalogWireSnapshot_StaleIsRetryable：cursor_stale 的 WireError 必须 Retryable=&true
// （客户端应重新 page-0 而非放弃）。
func TestCatalogWireSnapshot_StaleIsRetryable(t *testing.T) {
	cache, _ := newWireCacheWithFakeNow(time.UnixMilli(2_000_000))
	scope := openCodeCatalogScopeKey("opencode", "/d", false)
	cur, _ := encodeListCursorV2(listCursorV2{Epoch: "cafebabe", UpdatedAtMillis: 999_999, SessionID: "ses_00"})
	calls := 0
	_, staleErr, err := cache.pageV2(scope, cur, 1, builderFromMaps(synthWireMaps(3), &calls))
	if err != nil {
		t.Fatalf("pageV2: %v", err)
	}
	if staleErr == nil || staleErr.Code != "cursor_stale" {
		t.Fatalf("got staleErr=%+v, want cursor_stale", staleErr)
	}
	if staleErr.Retryable == nil || *staleErr.Retryable != true {
		t.Fatalf("cursor_stale 必须 Retryable=&true，got Retryable=%v", staleErr.Retryable)
	}
}

// TestCatalogWireSnapshot_Singleflight：同 scope 并发 page-0 只触发一次 builder，两个并发请求
// 复用同一快照（§11）。
func TestCatalogWireSnapshot_Singleflight(t *testing.T) {
	cache, _ := newWireCacheWithFakeNow(time.UnixMilli(2_000_000))
	scope := openCodeCatalogScopeKey("opencode", "/d", false)

	slowCh := make(chan struct{}) // 首次 builder 阻塞到测试方 close
	calls := 0
	builder := func() ([]map[string]interface{}, error) {
		calls++ // 仅计数，不加锁：两个 goroutine 中只有一个会进入（singleflight 保证）
		<-slowCh
		return append([]map[string]interface{}(nil), synthWireMaps(5)...), nil
	}

	var wg sync.WaitGroup
	results := make([]map[string]interface{}, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], _, errs[idx] = cache.pageV2(scope, "", 2, builder)
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // 让两个 goroutine 都进入 FetchOrReuse
	close(slowCh)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine[%d] pageV2: %v", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("builder calls = %d, want 1（同 scope 须 singleflight）", calls)
	}
	for i, r := range results {
		ws, ok := r["sessions"].([]map[string]interface{})
		if !ok || len(ws) != 2 {
			t.Errorf("results[%d] sessions len = %d, want 2", i, len(ws))
		}
	}
}

// ── 3. 无 v1 泄漏（§10 关键不变量）────────────────────────────────────────────

// TestCatalogWireSnapshot_DeclaredPathEmitsV2CursorsOnly：声明路径（pageV2）发射的 nextCursor
// 一律 v2（decode → isV1=false, Version=2）且携带快照 epoch。handler gate（capability 门控测试
// 已覆盖）保证 undeclared 连接结构上不可能到达 pageV2 → 无 v2 cursor / cursor_stale 泄漏到旧连接。
func TestCatalogWireSnapshot_DeclaredPathEmitsV2CursorsOnly(t *testing.T) {
	calls := 0
	cache, _ := newWireCacheWithFakeNow(time.UnixMilli(2_000_000))
	scope := openCodeCatalogScopeKey("opencode", "/d", false)
	builder := builderFromMaps(synthWireMaps(5), &calls)

	p0, staleErr, err := cache.pageV2(scope, "", 2, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("page0: err=%v staleErr=%v", err, staleErr)
	}
	cursor0, ok := p0["nextCursor"].(string)
	if !ok || cursor0 == "" {
		t.Fatalf("page0 nextCursor 缺失或为空：%v", p0["nextCursor"])
	}
	decoded, isV1, derr := decodeListCursorV2(cursor0)
	if derr != nil {
		t.Fatalf("nextCursor 解码失败（不是合法 v2）：%v", derr)
	}
	if isV1 {
		t.Fatal("声明路径的 nextCursor 是 v1（无 epoch）—— §10 泄漏")
	}
	if decoded.Version != listCursorVersionV2 {
		t.Fatalf("nextCursor version = %d, want %d", decoded.Version, listCursorVersionV2)
	}
	// epoch 必须匹配 page-0 快照的真实 epoch（Peek 取回核对）。
	snap := cache.Peek(scope)
	if snap == nil {
		t.Fatal("page-0 后应存在缓存快照")
	}
	if decoded.Epoch != snap.epoch {
		t.Fatalf("nextCursor epoch = %q, want 快照 epoch %q", decoded.Epoch, snap.epoch)
	}

	// page-N cursor 也应是 v2。
	p1, staleErr, err := cache.pageV2(scope, cursor0, 2, builder)
	if err != nil || staleErr != nil {
		t.Fatalf("page1: err=%v staleErr=%v", err, staleErr)
	}
	if cursor1, ok := p1["nextCursor"].(string); ok && cursor1 != "" {
		if _, isV1, derr := decodeListCursorV2(cursor1); derr != nil || isV1 {
			t.Fatalf("page1 nextCursor 非 v2：derr=%v isV1=%v", derr, isV1)
		}
	}
}
