package gobridge

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// catalog_snapshot_cache.go是 page-0 有序快照缓存（设计 §4.1.1 / §11，Phase 1 chunk 1B）。
//
// §4.1.1 快照模型：MacBridge 收到 page-0 请求后完成一次该 scope 的有界全量读取，缓存
// 得到的有序轻量集合（TTL=catalogSnapshotTTL，仅 metadata、有数量上限）；后续 page-N
// 从同一快照切片，使同一 cursor 链的多页来自同一有序集合，不因跨请求重抓产生漂移。
//
// 关键不变量（§4.1.1 + §11）：
//   - page-0（cursor 空）：fetch-or-reuse，TTL 过期则重建。
//   - page-N（cursor 非空）：**peek，不重建**。validateCursorV2 把 missing/expired/epoch
//     mismatch/v1 统一转成 cursor_stale；不盲切、不静默回 page-0（§4.1.1）。
//   - catalog 子进程/连接重启但数据未变 → fingerprint 不变 → epoch 不变 → 旧 cursor 仍有效。
//   - singleflight：同一 scope 的并发 page-0 复用一次 provider 调用（模板 claudeSessionCatalog.
//     refresh/inFlight）。
//
// Phase 1B 是纯 seam：cache 存在但**不**接入 live list handler（接入属 1C，capability 门控）。
// 缓存只保存 NormalizedSession 轻量元数据，不保存完整 session history（§4.3）。

// defaultCatalogPageLimit 是 q.Limit<=0 时 page 切片的兜底页大小。Phase 1C 接线时会从
// handler 传入真实 limit（effectiveSessionListLimit）；1B 仅在测试中使用此默认值。
const defaultCatalogPageLimit = 50

// maxCachedScopes 是 cache 同时持有的 scope 快照上限（§4.3「缓存只保存有限的」）。
// 一个 bridge 通常只有少数活跃 scope（backend × directory × roots）；32 给余量。
// 超出时按 createdAt 最旧优先淘汰。
const maxCachedScopes = 32

// CatalogPage 是一次切片后的 catalog 页（设计 §4.1 Page + §4.1.1 cursor 协议）。
//
// Stale=true 表示 page-N 的 cursor 命中 cursor_stale（validateCursorV2 返回 stale）；
// 调用方（Phase 1C list handler）据此向已声明 catalog_cursor_epoch_v2 的连接发射
// cursor_stale，**不**盲切、不静默回 page-0。StaleReason 是 catalog_cursor_v2.go 的
// cursorStale* 原因码。NextCursor 是 v2 opaque cursor（携带 epoch）；HasMore=false 时为空。
type CatalogPage struct {
	Sessions    []NormalizedSession
	NextCursor  string
	HasMore     bool
	Epoch       string
	Stale       bool
	StaleReason string
}

// cachedSnapshot 是一个 scope 的缓存快照（cache 内部类型）。sessions 是规范化后的有序
// 轻量集合；epoch = deriveSnapshotEpoch(page0.Fingerprint)；createdAt 用于 TTL 判定。
type cachedSnapshot struct {
	scope     string
	epoch     string
	createdAt time.Time
	sessions  []NormalizedSession
}

// catalogCacheEntry 包装 cachedSnapshot，预留 future TTL-refresh 命中等扩展点。
type catalogCacheEntry struct {
	snap *cachedSnapshot
}

// catalogSnapshotCache 按 scope 缓存 page-0 有序快照，提供 fetch-or-reuse（page-0）
// 与 peek+validate+slice（page-N）。
type catalogSnapshotCache struct {
	provider SessionCatalogProvider
	now      func() time.Time // 注入时钟便于 TTL 测试；默认 time.Now

	mu       sync.Mutex
	scopes   map[string]*catalogCacheEntry
	inFlight map[string]chan struct{} // scopeKey → singleflight signal

	maxScopes int
}

// newCatalogSnapshotCache 构造一个 snapshot cache。now 为 nil 时用 time.Now。
func newCatalogSnapshotCache(provider SessionCatalogProvider, now func() time.Time) *catalogSnapshotCache {
	if now == nil {
		now = time.Now
	}
	return &catalogSnapshotCache{
		provider:  provider,
		now:       now,
		scopes:    make(map[string]*catalogCacheEntry),
		inFlight:  make(map[string]chan struct{}),
		maxScopes: maxCachedScopes,
	}
}

// catalogScopeKey 派生 scope 的缓存键：(BackendID, Directory, RootsOnly)。
// Cursor 不进键（同一 scope 的不同页共享快照）；Limit 不进键（切片参数，非 scope 身份）。
func catalogScopeKey(q CatalogQuery) string {
	roots := "0"
	if q.RootsOnly {
		roots = "1"
	}
	return q.BackendID + "\x00" + q.Directory + "\x00" + roots
}

// snapshotView 把 cachedSnapshot 投影成 Phase 0 的 catalogSnapshot（仅 Epoch/CreatedAt/Scope，
// validateCursorV2 只读这三项；Sessions 留空——cache 自存 typed sessions，不复存 untyped
// map）。snap==nil 时返回 nil，validateCursorV2 据此返回 cursorStaleNoSnapshot。
func snapshotView(s *cachedSnapshot) *catalogSnapshot {
	if s == nil {
		return nil
	}
	return &catalogSnapshot{Scope: s.scope, Epoch: s.epoch, CreatedAt: s.createdAt}
}

// Page 读取或切片一页 catalog（设计 §4.1.1）。
//   - q.Cursor 空（page-0）：fetch-or-reuse 该 scope 的快照（singleflight，TTL 过期则重建），
//     切前 limit 行，返回 page + v2 nextCursor（携带 epoch）。
//   - q.Cursor 非空（page-N）：decode v2 cursor → peek 该 scope 的缓存快照（**不重建**）
//     → validateCursorV2；stale 则返回 CatalogPage{Stale:true, StaleReason}（调用方发射
//     cursor_stale，不盲切）；否则按 cursor 切片。
//
// malformed cursor（decode 失败）返回硬错误，**不**当作 stale（Phase 0 冻结：损坏 vs stale
// 区分）。Phase 1B 不 gate capability（v2 始终内部使用）；1C 决定是否对连接发射 v2/cursor_stale。
func (c *catalogSnapshotCache) Page(ctx context.Context, q CatalogQuery) (CatalogPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultCatalogPageLimit
	}

	if q.Cursor == "" {
		snap, err := c.page0Snapshot(ctx, q)
		if err != nil {
			return CatalogPage{}, err
		}
		return c.slicePage0(snap, limit), nil
	}

	cur, isV1, err := decodeListCursorV2(q.Cursor)
	if err != nil {
		return CatalogPage{}, err
	}
	snap := c.peekSnapshot(q)
	if stale, reason := validateCursorV2(cur, isV1, snapshotView(snap), c.now()); stale {
		ep := ""
		if snap != nil {
			ep = snap.epoch
		}
		return CatalogPage{Stale: true, StaleReason: reason, Epoch: ep}, nil
	}
	return c.slicePageN(snap, cur, limit), nil
}

// page0Snapshot 是 page-0 的 fetch-or-reuse（设计 §4.1.1）。
// 缓存命中且未过期 → 复用；否则 singleflight 一次 provider.FetchPage0 并缓存。
func (c *catalogSnapshotCache) page0Snapshot(ctx context.Context, q CatalogQuery) (*cachedSnapshot, error) {
	key := catalogScopeKey(q)

	c.mu.Lock()
	if entry, ok := c.scopes[key]; ok && !snapshotExpired(*snapshotView(entry.snap), c.now()) {
		snap := entry.snap
		c.mu.Unlock()
		return snap, nil
	}
	if wait, ok := c.inFlight[key]; ok {
		c.mu.Unlock()
		<-wait
		c.mu.Lock()
		entry := c.scopes[key]
		c.mu.Unlock()
		if entry != nil && !snapshotExpired(*snapshotView(entry.snap), c.now()) {
			return entry.snap, nil
		}
		return nil, fmt.Errorf("catalog snapshot unavailable for scope %q", key)
	}
	wait := make(chan struct{})
	c.inFlight[key] = wait
	c.mu.Unlock()

	page0, err := c.provider.FetchPage0(ctx, q)

	c.mu.Lock()
	delete(c.inFlight, key)
	if err != nil {
		close(wait)
		c.mu.Unlock()
		return nil, err
	}
	sessions := append([]NormalizedSession(nil), page0.Sessions...)
	sortNormalizedForCursor(sessions) // 确定性序：UpdatedAtMillis DESC, StableID ASC（cursor 切片所需）
	snap := &cachedSnapshot{
		scope:     key,
		epoch:     deriveSnapshotEpoch(page0.Fingerprint),
		createdAt: c.now(),
		sessions:  sessions,
	}
	c.scopes[key] = &catalogCacheEntry{snap: snap}
	c.evictLocked()
	close(wait)
	c.mu.Unlock()
	return snap, nil
}

// peekSnapshot 返回 scope 的缓存快照（**不**fetch/rebuild）。page-N 用：validateCursorV2
// 把 missing（nil）转成 cursorStaleNoSnapshot（§4.1.1 page-N 不盲切不静默回 page-0）。
func (c *catalogSnapshotCache) peekSnapshot(q CatalogQuery) *cachedSnapshot {
	key := catalogScopeKey(q)
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.scopes[key]; ok {
		return entry.snap
	}
	return nil
}

// evictLocked 在持锁状态下把 scope 数量压到 maxScopes（最旧 createdAt 优先淘汰）。
func (c *catalogSnapshotCache) evictLocked() {
	for len(c.scopes) > c.maxScopes {
		var oldestKey string
		var oldest time.Time
		for k, e := range c.scopes {
			if oldestKey == "" || e.snap.createdAt.Before(oldest) {
				oldestKey = k
				oldest = e.snap.createdAt
			}
		}
		delete(c.scopes, oldestKey)
	}
}

// slicePage0 切 page-0 的前 limit 行，并在 hasMore 时编码 v2 nextCursor（携带 epoch）。
func (c *catalogSnapshotCache) slicePage0(snap *cachedSnapshot, limit int) CatalogPage {
	end := limit
	if end > len(snap.sessions) {
		end = len(snap.sessions)
	}
	out := make([]NormalizedSession, end)
	copy(out, snap.sessions[:end])
	hasMore := limit > 0 && len(snap.sessions) > end
	return CatalogPage{
		Sessions:   out,
		HasMore:    hasMore,
		NextCursor: c.encodeNext(snap, out, hasMore),
		Epoch:      snap.epoch,
	}
}

// slicePageN 从 snap 切「严格在 cursor 之后」的 limit 行（序：UpdatedAtMillis DESC,
// StableID ASC）。nextCursor 携带同一 epoch（同 cursor 链）。
func (c *catalogSnapshotCache) slicePageN(snap *cachedSnapshot, cur listCursorV2, limit int) CatalogPage {
	start := len(snap.sessions)
	for i, s := range snap.sessions {
		if s.UpdatedAtMillis < cur.UpdatedAtMillis || (s.UpdatedAtMillis == cur.UpdatedAtMillis && s.StableID > cur.SessionID) {
			start = i
			break
		}
	}
	end := start + limit
	if end > len(snap.sessions) {
		end = len(snap.sessions)
	}
	out := make([]NormalizedSession, end-start)
	copy(out, snap.sessions[start:end])
	hasMore := limit > 0 && len(snap.sessions) > end
	return CatalogPage{
		Sessions:   out,
		HasMore:    hasMore,
		NextCursor: c.encodeNext(snap, out, hasMore),
		Epoch:      snap.epoch,
	}
}

// encodeNext 在 hasMore 且页非空时，用最后一行的 (UpdatedAtMillis, StableID) + 快照 epoch
// 编码 v2 nextCursor。epoch 随 cursor 链携带，使 page-N 的 validateCursorV2 能比对。
func (c *catalogSnapshotCache) encodeNext(snap *cachedSnapshot, page []NormalizedSession, hasMore bool) string {
	if !hasMore || len(page) == 0 {
		return ""
	}
	last := page[len(page)-1]
	next, err := encodeListCursorV2(listCursorV2{
		Epoch:           snap.epoch,
		UpdatedAtMillis: last.UpdatedAtMillis,
		SessionID:       last.StableID,
	})
	if err != nil {
		return ""
	}
	return next
}

// sortNormalizedForCursor 把 sessions 排成 cursor 切片所需的确定性全序：
// UpdatedAtMillis DESC，StableID ASC。主键（updatedAt DESC）与 backend 上游序一致
// （OpenCode /session 上游 time.updated desc，Phase 0 冻结）；StableID 仅作确定性
// tie-break，作为 epoch-bearing cursor 的稳定 canonical order。
func sortNormalizedForCursor(s []NormalizedSession) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].UpdatedAtMillis != s[j].UpdatedAtMillis {
			return s[i].UpdatedAtMillis > s[j].UpdatedAtMillis
		}
		return s[i].StableID < s[j].StableID
	})
}
