package gobridge

// catalog_wire_snapshot.go 是 OpenCode 专用 wire-map 快照缓存（跨后端 session catalog
// 同源改造 §4.1.1 / §5.3#3，Phase 1 chunk 1C）。它与 catalog_snapshot_cache.go（1B，
// NormalizedSession，面向 Phase 2-5 全新 provider）刻意分开：
//
//   - §5.3#3 明确要求 OpenCode「复用既有 paginateSessionList 富 wire 管线」，因此本缓存
//     直接缓存 enriched wire maps（[]map[string]interface{}，即 mapSession+enrich+
//     overlayPinned+sortSessionsByUpdatedAt 的产物），而不是 NormalizedSession；
//   - 切片时复用 catalog_cursor_v2.go 的全部原语（encodeListCursorV2/decodeListCursorV2
//     /validateCursorV2/deriveSnapshotEpoch/snapshotExpired + cursorStale* 原因码），
//     仅把 nextCursor 换成 epoch-bearing v2 形式；
//   - 仅对在 hello 声明 catalog_cursor_epoch_v2 的连接使用（ocHandleListSessions gate）。
//     未声明连接走既有 v1 paginateSessionList 路径 byte-for-byte 不变（§10 发布顺序：
//     capability 上线前 MacBridge 不得对任何连接发射 v2 cursor）。
//
// 快照语义与 1B 一致（设计 §4.1.1）：page-0 有界全量读，按 scope 缓存（TTL + singleflight）；
// page-N 复用同一冻结快照切片，不重建；cursor 携带 epoch；TTL 过期 / epoch 不匹配 /
// 该 scope 无快照 / v1 cursor → cursor_stale（不盲切、不静默回 page-0）。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// catalogWireSnapshot 是一个 OpenCode scope 的缓存快照：enriched wire maps + epoch +
// createdAt。maps 已按 (updatedAtMillis DESC, id ASC) 排序，切片与派生 cursor 直接复用。
type catalogWireSnapshot struct {
	scope     string
	epoch     string
	createdAt time.Time
	maps      []map[string]interface{}
}

// catalogWireSnapshotCache 按 scope 缓存 enriched wire maps（§4.1.1 快照模型）。
// now 注入便于 TTL 测试；inFlight 提供 page-0 singleflight（§11）。
type catalogWireSnapshotCache struct {
	now      func() time.Time
	mu       sync.Mutex
	scopes   map[string]*catalogWireSnapshot
	inFlight map[string]chan struct{}
}

func newCatalogWireSnapshotCache(now func() time.Time) *catalogWireSnapshotCache {
	if now == nil {
		now = time.Now
	}
	return &catalogWireSnapshotCache{
		now:      now,
		scopes:   make(map[string]*catalogWireSnapshot),
		inFlight: make(map[string]chan struct{}),
	}
}

// openCodeCatalogScopeKey 派生 OpenCode scope 缓存键：(backendID, directory, rootsOnly)。
// 三个维度都纳入：不同 backend / 不同目录 / root-only 与否互不复用快照。
func openCodeCatalogScopeKey(backendID, dir string, rootsOnly bool) string {
	roots := "0"
	if rootsOnly {
		roots = "1"
	}
	return backendID + "\x00" + dir + "\x00" + roots
}

// wireFingerprint 由 enriched wire maps 派生确定摘要（id|updatedAtMillis，按 id 排序后换行拼接）。
// 与 fingerprintCatalog（NormalizedSession）同语义，只是字段从 wire map 读取。成员或 updatedAt
// 任一变化 → fingerprint 变 → epoch 变 → 旧 cursor cursor_stale（§4.1.1）。
func wireFingerprint(maps []map[string]interface{}) string {
	type kv struct{ id string; ts int64 }
	pairs := make([]kv, 0, len(maps))
	for _, m := range maps {
		id, _ := m["id"].(string)
		ts, _ := m["updatedAtMillis"].(int64)
		pairs = append(pairs, kv{id, ts})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].id < pairs[j].id })
	var b strings.Builder
	for _, p := range pairs {
		b.WriteString(p.id)
		b.WriteByte('|')
		b.WriteString(strconv.FormatInt(p.ts, 10))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// sortWireMapsForCursor 把 enriched wire maps 排成 cursor 切片所需的序：
// updatedAtMillis DESC，id ASC（与 sortSessionsByUpdatedAt / paginateSessionList 一致）。
func sortWireMapsForCursor(maps []map[string]interface{}) {
	sort.Slice(maps, func(i, j int) bool {
		ti, _ := maps[i]["updatedAtMillis"].(int64)
		tj, _ := maps[j]["updatedAtMillis"].(int64)
		if ti != tj {
			return ti > tj // DESC
		}
		idi, _ := maps[i]["id"].(string)
		idj, _ := maps[j]["id"].(string)
		return idi < idj // ASC
	})
}

// wireSnapshotExpired 复刻 snapshotExpired 语义但作用于 wire 快照（snapshotExpired 读
// catalogSnapshot.CreatedAt；这里读 catalogWireSnapshot.createdAt）。零值 createdAt 视为过期。
func wireSnapshotExpired(s *catalogWireSnapshot, now time.Time) bool {
	if s == nil || s.createdAt.IsZero() {
		return true
	}
	return now.Sub(s.createdAt) > catalogSnapshotTTL
}

// wireSnapshotView 把 wire 快照投影成 validateCursorV2 所需的 *catalogSnapshot。
// validateCursorV2 只读 Scope/Epoch/CreatedAt（见 catalog_cursor_v2.go），Sessions 字段留空。
func wireSnapshotView(s *catalogWireSnapshot) *catalogSnapshot {
	if s == nil {
		return nil
	}
	return &catalogSnapshot{Scope: s.scope, Epoch: s.epoch, CreatedAt: s.createdAt}
}

// FetchOrReuse 是 page-0 路径：缓存命中且未过期 → 复用；否则 singleflight 调 builder 构造
// enriched wire maps 并缓存。builder 只在 miss/expiry 时调用。返回的快照的 maps 是缓存内
// 副本的可读引用（切片元素仍共享 map；调用方只读不写，handler 不再 mutate）。
func (c *catalogWireSnapshotCache) FetchOrReuse(scopeKey string, builder func() ([]map[string]interface{}, error)) (*catalogWireSnapshot, error) {
	c.mu.Lock()
	if snap, ok := c.scopes[scopeKey]; ok && !wireSnapshotExpired(snap, c.now()) {
		c.mu.Unlock()
		return snap, nil
	}
	// 已有 page-0 在飞 → 等它完成，复用其结果（不重复打上游）。
	if wait, ok := c.inFlight[scopeKey]; ok {
		c.mu.Unlock()
		<-wait
		c.mu.Lock()
		snap := c.scopes[scopeKey]
		c.mu.Unlock()
		if snap != nil && !wireSnapshotExpired(snap, c.now()) {
			return snap, nil
		}
		return nil, fmt.Errorf("opencode catalog snapshot unavailable for scope %q", scopeKey)
	}
	wait := make(chan struct{})
	c.inFlight[scopeKey] = wait
	c.mu.Unlock()

	maps, err := builder()

	c.mu.Lock()
	delete(c.inFlight, scopeKey)
	if err != nil {
		close(wait)
		c.mu.Unlock()
		return nil, err
	}
	cp := append([]map[string]interface{}(nil), maps...)
	sortWireMapsForCursor(cp)
	snap := &catalogWireSnapshot{
		scope:     scopeKey,
		epoch:     deriveSnapshotEpoch(wireFingerprint(cp)),
		createdAt: c.now(),
		maps:      cp,
	}
	c.scopes[scopeKey] = snap
	close(wait)
	c.mu.Unlock()
	return snap, nil
}

// Peek 是 page-N 路径：返回缓存快照（不重建、不打上游）。可能返回 nil（该 scope 无快照）。
func (c *catalogWireSnapshotCache) Peek(scopeKey string) *catalogWireSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scopes[scopeKey]
}

// pageV2 为已声明 catalog_cursor_epoch_v2 的连接计算一页 v2 catalog：
//   - cursor==""（page-0）：FetchOrReuse(builder) 取/建快照，切首页；
//   - cursor!=""（page-N）：Peek（不重建）→ decodeListCursorV2 → validateCursorV2 → 切片；
//
// 返回 wire result map（{sessions, hasMore, nextCursor?}，shape 与 paginateSessionList 一致）
// 或 cursor_stale WireError（staleErr）或硬错误（err：list_failed / 损坏 cursor）。
// page-0 不会 stale（刚建/复用未过期快照）；page-N 的 stale 由 validateCursorV2 判定。
func (c *catalogWireSnapshotCache) pageV2(scopeKey, cursor string, limit int, builder func() ([]map[string]interface{}, error)) (map[string]interface{}, *WireError, error) {
	if cursor == "" {
		snap, err := c.FetchOrReuse(scopeKey, builder)
		if err != nil {
			return nil, nil, err
		}
		sessions, nextCursor, hasMore, _, derr := sliceCatalogWirePage(snap, "", limit, c.now())
		if derr != nil {
			return nil, nil, derr
		}
		return wireResultMap(sessions, nextCursor, hasMore), nil, nil
	}
	snap := c.Peek(scopeKey)
	sessions, nextCursor, hasMore, staleReason, derr := sliceCatalogWirePage(snap, cursor, limit, c.now())
	if derr != nil {
		return nil, nil, derr
	}
	if staleReason != cursorStaleOK {
		return nil, retryableSessionError("cursor_stale", staleReason), nil
	}
	return wireResultMap(sessions, nextCursor, hasMore), nil, nil
}

// sliceCatalogWirePage 切一页 v2。cursor=="" → page-0（snap 必须非 nil，调用方已 FetchOrReuse）；
// cursor!="" → page-N（decode → validate → 切严格后继）。返回 sessions/nextCursor/hasMore +
// staleReason（cursor_stale 原因码，"" 表示不 stale）+ err（损坏 cursor 硬错误）。
func sliceCatalogWirePage(snap *catalogWireSnapshot, cursor string, limit int, now time.Time) (sessions []map[string]interface{}, nextCursor string, hasMore bool, staleReason string, err error) {
	if cursor == "" {
		end := limit
		if limit <= 0 || end > len(snap.maps) {
			end = len(snap.maps)
		}
		sessions = copyWireRange(snap.maps, 0, end)
		hasMore = limit > 0 && len(snap.maps) > end
		nextCursor = encodeWireNext(snap, sessions, hasMore)
		return sessions, nextCursor, hasMore, cursorStaleOK, nil
	}

	cur, isV1, derr := decodeListCursorV2(cursor)
	if derr != nil {
		return nil, "", false, "", derr // 损坏 cursor → 硬错误（不静默回 page-0）
	}
	if stale, reason := validateCursorV2(cur, isV1, wireSnapshotView(snap), now); stale {
		return nil, "", false, reason, nil
	}
	// 切严格后继：(ts, id) 排在 cursor 之后的行。maps 已按 (ts DESC, id ASC) 排序。
	start := len(snap.maps)
	for i, m := range snap.maps {
		ts, _ := m["updatedAtMillis"].(int64)
		id, _ := m["id"].(string)
		if ts < cur.UpdatedAtMillis || (ts == cur.UpdatedAtMillis && id > cur.SessionID) {
			start = i
			break
		}
	}
	end := start + limit
	if limit <= 0 || end > len(snap.maps) {
		end = len(snap.maps)
	}
	sessions = copyWireRange(snap.maps, start, end)
	hasMore = limit > 0 && len(snap.maps) > end
	nextCursor = encodeWireNext(snap, sessions, hasMore)
	return sessions, nextCursor, hasMore, cursorStaleOK, nil
}

// copyWireRange 防御性拷贝 [start,end)（切片元素 map 共享，但 handler 只读不写）。
func copyWireRange(maps []map[string]interface{}, start, end int) []map[string]interface{} {
	if start < 0 {
		start = 0
	}
	if start >= len(maps) || start >= end {
		return nil
	}
	if end > len(maps) {
		end = len(maps)
	}
	out := make([]map[string]interface{}, end-start)
	copy(out, maps[start:end])
	return out
}

// encodeWireNext 末页 / 空页 → ""；否则编码最后一行的 (epoch, ts, id) 为 v2 cursor。
// nextCursor 的存在性（仅在 hasMore && len>0 时非空）与 paginateSessionList 一致。
func encodeWireNext(snap *catalogWireSnapshot, page []map[string]interface{}, hasMore bool) string {
	if !hasMore || len(page) == 0 {
		return ""
	}
	last := page[len(page)-1]
	ts, _ := last["updatedAtMillis"].(int64)
	id, _ := last["id"].(string)
	next, err := encodeListCursorV2(listCursorV2{Epoch: snap.epoch, UpdatedAtMillis: ts, SessionID: id})
	if err != nil {
		return ""
	}
	return next
}

// wireResultMap 组装与 paginateSessionList 同 shape 的 wire result：sessions + hasMore 恒在，
// nextCursor 仅在 hasMore && len(sessions)>0 时存在。
func wireResultMap(sessions []map[string]interface{}, nextCursor string, hasMore bool) map[string]interface{} {
	result := map[string]interface{}{"sessions": sessions, "hasMore": hasMore}
	if hasMore && len(sessions) > 0 && nextCursor != "" {
		result["nextCursor"] = nextCursor
	}
	return result
}
