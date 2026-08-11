package gobridge

// catalog_snapshot_cache_test.go验证 page-0 快照缓存的 §4.1.1 / §11 行为：多页切片无漂移、
// TTL/epoch-mismatch/no-snapshot/v1 → cursor_stale（不盲切不静默回 page-0）、catalog 重启
// 数据未变 → 相同 epoch → 旧 cursor 仍有效、同 scope 并发 page-0 singleflight。
//
// 用 stubCatalogProvider 注入可控数据 + fake clock（TTL 测试），不触网络、不启动 live
// `opencode serve`。Phase 1B 是纯 seam：cache 尚未接入 live list handler（接入属 1C）。

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// stubCatalogProvider 回放可控的 NormalizedSession 集合，计数 FetchPage0 调用，可选通过
// slowCh 阻塞首次调用（singleflight 时序测试）。
type stubCatalogProvider struct {
	mu       sync.Mutex
	sessions []NormalizedSession
	err      error
	calls    int
	slowCh   chan struct{}
}

func (s *stubCatalogProvider) FetchPage0(_ context.Context, _ CatalogQuery) (CatalogPage0, error) {
	s.mu.Lock()
	s.calls++
	sessions := append([]NormalizedSession(nil), s.sessions...)
	err := s.err
	slow := s.slowCh
	s.mu.Unlock()
	if slow != nil {
		<-slow // gate：singleflight 时序测试时由测试方 close
	}
	if err != nil {
		return CatalogPage0{}, err
	}
	return CatalogPage0{Sessions: sessions, Fingerprint: fingerprintCatalog(sessions)}, nil
}

func (s *stubCatalogProvider) setSessions(se []NormalizedSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = append([]NormalizedSession(nil), se...)
}

func (s *stubCatalogProvider) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// synthSessions 构造 n 个确定性 session：StableID=ses_II，UpdatedAtMillis 严格递减（cursor
// 切片所需的 desc 序），无 parent（IsRoot）。
func synthSessions(n int) []NormalizedSession {
	out := make([]NormalizedSession, n)
	for i := 0; i < n; i++ {
		out[i] = NormalizedSession{
			StableID:        fmt.Sprintf("ses_%02d", i),
			BackendID:       "opencode",
			Title:           fmt.Sprintf("session %d", i),
			UpdatedAtMillis: int64(1_000_000 - i*1000),
			CreatedAtMillis: int64(1_000_000 - i*1000 - 500),
			Directory:       "/tmp/fixture-workspace",
			IsRoot:          true,
			OrderingKey:     fmt.Sprintf("ses_%02d", i),
		}
	}
	return out
}

func newCacheWithFakeNow(p SessionCatalogProvider, now time.Time) (*catalogSnapshotCache, *time.Time) {
	t := now
	c := newCatalogSnapshotCache(p, func() time.Time { return t })
	return c, &t
}

// TestCatalogSnapshotCache_MultiPageNoDrift：同一 cursor 链的多页切片来自同一有序集合，
// 拼回完整集合无重无缺、序保持；且 page-N 切的是 page-0 时冻结的快照，即便 provider 底层
// 数据之后变化，page-N 也不漂移（§11 page-0 快照缓存 / §4.1.1）。
func TestCatalogSnapshotCache_MultiPageNoDrift(t *testing.T) {
	stub := &stubCatalogProvider{sessions: synthSessions(5)}
	cache, _ := newCacheWithFakeNow(stub, time.UnixMilli(2_000_000))
	q := CatalogQuery{BackendID: "opencode", Limit: 2}

	p0, err := cache.Page(context.Background(), q)
	if err != nil {
		t.Fatalf("page0: %v", err)
	}
	if len(p0.Sessions) != 2 || !p0.HasMore || p0.NextCursor == "" {
		t.Fatalf("page0 = %+v, want 2 sessions + hasMore + cursor", p0)
	}
	p1, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 2, Cursor: p0.NextCursor})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	p2, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 2, Cursor: p1.NextCursor})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if p2.HasMore || p2.NextCursor != "" {
		t.Fatalf("page2 应是末页，got HasMore=%v NextCursor=%q", p2.HasMore, p2.NextCursor)
	}
	// 拼回完整集合，断言无重无缺、序保持。
	var got []string
	for _, p := range []CatalogPage{p0, p1, p2} {
		for _, s := range p.Sessions {
			got = append(got, s.StableID)
		}
	}
	want := []string{"ses_00", "ses_01", "ses_02", "ses_03", "ses_04"}
	if len(got) != len(want) {
		t.Fatalf("拼接页 = %v, want %v", got, want)
	}
	seen := map[string]int{}
	for i, id := range got {
		if id != want[i] {
			t.Errorf("拼接页[%d] = %q, want %q（序应保持）", i, id, want[i])
		}
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("拼接页出现重复 %q ×%d（跨页漂移）", id, n)
		}
	}

	// 漂移测试：page-0 已冻结快照；之后改 provider 底层数据，page-N（peek 不重建）
	// 必须仍切旧快照、不漂移。
	stub.setSessions(synthSessionsDrift())
	p1b, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 2, Cursor: p0.NextCursor})
	if err != nil {
		t.Fatalf("page1b (post-mutation): %v", err)
	}
	for i, s := range p1b.Sessions {
		if s.StableID != want[i+2] {
			t.Errorf("page1b[%d].StableID = %q, want %q（page-N 不应受 provider 数据变化漂移）", i, s.StableID, want[i+2])
		}
	}
}

// synthSessionsDrift 产生一组**不同**的 session（不同 id/updatedAt），用于证明 page-N 切的是
// page-0 冻结的旧快照而非重新读取 provider。
func synthSessionsDrift() []NormalizedSession {
	out := make([]NormalizedSession, 5)
	for i := 0; i < 5; i++ {
		out[i] = NormalizedSession{
			StableID:        fmt.Sprintf("DRIFT_%02d", i),
			BackendID:       "opencode",
			UpdatedAtMillis: int64(9_000_000 - i*1000),
		}
	}
	return out
}

// TestCatalogSnapshotCache_TTLExpiryCursorStale：page-0 后推进时钟超过 catalogSnapshotTTL，
// page-N cursor 命中 cursorStaleExpired（§4.1.1「TTL 过期无有效快照返回 cursor_stale」）。
func TestCatalogSnapshotCache_TTLExpiryCursorStale(t *testing.T) {
	stub := &stubCatalogProvider{sessions: synthSessions(3)}
	cache, clock := newCacheWithFakeNow(stub, time.UnixMilli(2_000_000))
	p0, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 1})
	if err != nil {
		t.Fatalf("page0: %v", err)
	}
	*clock = clock.Add(catalogSnapshotTTL + time.Minute)
	p1, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Cursor: p0.NextCursor})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if !p1.Stale || p1.StaleReason != cursorStaleExpired {
		t.Fatalf("page1 = %+v, want Stale/cursorStaleExpired", p1)
	}
}

// TestCatalogSnapshotCache_EpochMismatchCursorStale：cursor 携带的 epoch 与当前快照不一致
// → cursorStaleEpochMismatch（§4.1.1 fingerprint 变化 → cursor_stale，不盲切）。
func TestCatalogSnapshotCache_EpochMismatchCursorStale(t *testing.T) {
	stub := &stubCatalogProvider{sessions: synthSessions(3)}
	cache, _ := newCacheWithFakeNow(stub, time.UnixMilli(2_000_000))
	// 先 page-0 建立 scope 快照（真实 epoch E1）。
	if _, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 1}); err != nil {
		t.Fatalf("page0: %v", err)
	}
	// 构造一个 epoch 故意错误的有效 v2 cursor。
	foreign, err := encodeListCursorV2(listCursorV2{Epoch: "deadbeef", UpdatedAtMillis: 999_999, SessionID: "ses_00"})
	if err != nil {
		t.Fatalf("encode foreign cursor: %v", err)
	}
	p, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Cursor: foreign})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if !p.Stale || p.StaleReason != cursorStaleEpochMismatch {
		t.Fatalf("got %+v, want Stale/cursorStaleEpochMismatch", p)
	}
}

// TestCatalogSnapshotCache_NoSnapshotStale：scope 从未 page-0，page-N 直接来 →
// cursorStaleNoSnapshot（§4.1.1「该 scope 无快照 → cursor_stale」，不盲切）。
func TestCatalogSnapshotCache_NoSnapshotStale(t *testing.T) {
	stub := &stubCatalogProvider{sessions: synthSessions(3)}
	cache, _ := newCacheWithFakeNow(stub, time.UnixMilli(2_000_000))
	cur, err := encodeListCursorV2(listCursorV2{Epoch: "cafebabe", UpdatedAtMillis: 999_999, SessionID: "ses_00"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	p, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Cursor: cur})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if !p.Stale || p.StaleReason != cursorStaleNoSnapshot {
		t.Fatalf("got %+v, want Stale/cursorStaleNoSnapshot", p)
	}
}

// TestCatalogSnapshotCache_V1CursorStale：v2 模式下 v1 cursor 无 epoch → cursorStaleV1
// （§4.1.1「v1 无 epoch 视为 stale」；用于 declared 客户端持有旧 cursor 的恢复）。
func TestCatalogSnapshotCache_V1CursorStale(t *testing.T) {
	stub := &stubCatalogProvider{sessions: synthSessions(3)}
	cache, _ := newCacheWithFakeNow(stub, time.UnixMilli(2_000_000))
	if _, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 1}); err != nil {
		t.Fatalf("page0: %v", err)
	}
	v1, err := encodeListCursor(listCursor{UpdatedAtMillis: 999_999, SessionID: "ses_00"})
	if err != nil {
		t.Fatalf("encode v1: %v", err)
	}
	p, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Cursor: v1})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if !p.Stale || p.StaleReason != cursorStaleV1 {
		t.Fatalf("got %+v, want Stale/cursorStaleV1", p)
	}
}

// TestCatalogSnapshotCache_RestartSameDataSameEpoch：两个独立 cache（模拟 catalog 子进程/
// 连接重启）在同一数据上 → 相同 epoch；旧 cursor 在新 cache 上仍有效（§4.1.1「数据未变 →
// epoch 不变 → 旧 cursor 仍有效」）。
func TestCatalogSnapshotCache_RestartSameDataSameEpoch(t *testing.T) {
	data := synthSessions(4)
	stub1 := &stubCatalogProvider{sessions: append([]NormalizedSession(nil), data...)}
	stub2 := &stubCatalogProvider{sessions: append([]NormalizedSession(nil), data...)}
	c1, _ := newCacheWithFakeNow(stub1, time.UnixMilli(2_000_000))
	c2, _ := newCacheWithFakeNow(stub2, time.UnixMilli(2_000_000))

	p1, err := c1.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 2})
	if err != nil {
		t.Fatalf("c1 page0: %v", err)
	}
	p2, err := c2.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 2})
	if err != nil {
		t.Fatalf("c2 page0: %v", err)
	}
	if p1.Epoch == "" {
		t.Fatal("epoch 为空")
	}
	if p1.Epoch != p2.Epoch {
		t.Fatalf("相同数据 epoch 不一致：%s != %s（§4.1.1 数据未变须 epoch 不变）", p1.Epoch, p2.Epoch)
	}

	// 旧 cursor（来自 c1，携带 epoch E）在 c2（同 epoch、未过期）上仍有效。
	p1next, err := c2.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 2, Cursor: p1.NextCursor})
	if err != nil {
		t.Fatalf("c2 page-N with c1 cursor: %v", err)
	}
	if p1next.Stale {
		t.Fatalf("同数据重启后旧 cursor 应有效，got Stale reason=%q", p1next.StaleReason)
	}
	if len(p1next.Sessions) != 2 {
		t.Errorf("c2 page-N sessions = %d, want 2", len(p1next.Sessions))
	}
	if p1next.Sessions[0].StableID != "ses_02" {
		t.Errorf("c2 page-N[0] = %q, want ses_02（续 c1 page0 的下一页）", p1next.Sessions[0].StableID)
	}
}

// TestCatalogSnapshotCache_Singleflight：同 scope 并发 page-0 只触发一次 provider 调用，
// 两个并发请求复用同一快照（§11 singleflight）。
func TestCatalogSnapshotCache_Singleflight(t *testing.T) {
	stub := &stubCatalogProvider{sessions: synthSessions(5)}
	stub.slowCh = make(chan struct{}) // 首次 FetchPage0 阻塞到测试方 close
	cache, _ := newCacheWithFakeNow(stub, time.UnixMilli(2_000_000))

	var wg sync.WaitGroup
	results := make([]CatalogPage, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 2})
		}(i)
	}
	// 让两个 goroutine 都进入 page0Snapshot（一个装 inFlight，一个等 inFlight）。
	time.Sleep(50 * time.Millisecond)
	close(stub.slowCh)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine[%d] Page: %v", i, err)
		}
	}
	if got := stub.callCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1（同 scope 须 singleflight）", got)
	}
	if results[0].Epoch != results[1].Epoch {
		t.Errorf("两并发请求 epoch 不一致：%s != %s（应复用同一快照）", results[0].Epoch, results[1].Epoch)
	}
	if len(results[0].Sessions) != 2 || len(results[1].Sessions) != 2 {
		t.Errorf("并发请求页大小不一致：%d / %d", len(results[0].Sessions), len(results[1].Sessions))
	}
}

// TestCatalogSnapshotCache_MalformedCursorIsErrorNotStale：损坏 cursor 返回硬错误，不当作
// stale（Phase 0 冻结：损坏 vs stale 区分；不静默回 page-0）。
func TestCatalogSnapshotCache_MalformedCursorIsErrorNotStale(t *testing.T) {
	stub := &stubCatalogProvider{sessions: synthSessions(3)}
	cache, _ := newCacheWithFakeNow(stub, time.UnixMilli(2_000_000))
	if _, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Limit: 1}); err != nil {
		t.Fatalf("page0: %v", err)
	}
	_, err := cache.Page(context.Background(), CatalogQuery{BackendID: "opencode", Cursor: "!!!not-base64!!!"})
	if err == nil {
		t.Fatal("损坏 cursor 应返回错误，got nil")
	}
}
