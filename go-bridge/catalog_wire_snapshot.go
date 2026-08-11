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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// catalogWireSnapshot 是一个 OpenCode scope 的缓存快照：enriched wire maps + epoch +
// createdAt。maps 已按 (updatedAtMillis DESC, id ASC) 排序，切片与派生 cursor 直接复用。
type catalogWireSnapshot struct {
	scope      catalogWireScope
	generation uint64
	epoch      string
	createdAt  time.Time
	maps       []map[string]interface{}
}

// catalogWireSnapshotCache 按 scope 缓存 enriched wire maps（§4.1.1 快照模型）。
// now 注入便于 TTL 测试；inFlight 提供 page-0 singleflight（§11）。
type catalogWireSnapshotCache struct {
	now         func() time.Time
	mu          sync.Mutex
	scopes      map[catalogWireScope]*catalogWireSnapshot
	inFlight    map[catalogWireScope]*catalogWireBuild
	generations map[string]uint64
}

type catalogWireBuild struct {
	generation uint64
	done       chan struct{}
	once       sync.Once
	snapshot   *catalogWireSnapshot
	err        error
}

func (b *catalogWireBuild) complete() { b.once.Do(func() { close(b.done) }) }

func newCatalogWireSnapshotCache(now func() time.Time) *catalogWireSnapshotCache {
	if now == nil {
		now = time.Now
	}
	return &catalogWireSnapshotCache{
		now:         now,
		scopes:      make(map[catalogWireScope]*catalogWireSnapshot),
		inFlight:    make(map[catalogWireScope]*catalogWireBuild),
		generations: make(map[string]uint64),
	}
}

// catalogWireScope is the sole identity for a declared catalog snapshot. Keeping the complete
// visibility contract in one comparable value prevents cache ownership and cursor epochs from
// silently omitting a dimension as new backends are added.
type catalogWireScope struct {
	BackendID string
	Global    bool
	Directory string
	RootsOnly bool
}

func newCatalogWireScope(backendID, dir string, rootsOnly bool) catalogWireScope {
	backendID = strings.TrimSpace(backendID)
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return catalogWireScope{BackendID: backendID, Global: true, RootsOnly: rootsOnly}
	}
	if normalized, ok := normalizeCatalogDirectory(dir); ok {
		dir = normalized
	} else {
		dir = filepath.Clean(dir)
	}
	return catalogWireScope{BackendID: backendID, Directory: dir, RootsOnly: rootsOnly}
}

func (s catalogWireScope) validate() error {
	if s.BackendID == "" {
		return fmt.Errorf("catalog wire scope requires backend ID")
	}
	if s.Global == (s.Directory != "") {
		return fmt.Errorf("catalog wire scope must be exactly global or directory-scoped")
	}
	return nil
}

func (s catalogWireScope) identity() string {
	global := "0"
	if s.Global {
		global = "1"
	}
	roots := "0"
	if s.RootsOnly {
		roots = "1"
	}
	return s.BackendID + "\x00" + global + "\x00" + s.Directory + "\x00" + roots
}

// openCodeCatalogScopeKey 派生 OpenCode scope 缓存键：(backendID, directory, rootsOnly)。
// 三个维度都纳入：不同 backend / 不同目录 / root-only 与否互不复用快照。
func openCodeCatalogScopeKey(backendID, dir string, rootsOnly bool) catalogWireScope {
	return newCatalogWireScope(backendID, dir, rootsOnly)
}

// wireFingerprint 由 enriched wire maps 派生确定摘要（id|updatedAtMillis，按 id 排序后换行拼接）。
// 与 fingerprintCatalog（NormalizedSession）同语义，只是字段从 wire map 读取。成员或 updatedAt
// 任一变化 → fingerprint 变 → epoch 变 → 旧 cursor cursor_stale（§4.1.1）。
func wireFingerprint(maps []map[string]interface{}) string {
	type kv struct {
		id string
		ts int64
	}
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

// wireListFingerprint is order-sensitive because declared snapshots preserve native provider
// order. A pure reordering must rotate the epoch even when membership is unchanged.
func wireListFingerprint(maps []map[string]interface{}) string {
	var b strings.Builder
	for _, item := range maps {
		id, _ := item["id"].(string)
		ts, _ := item["updatedAtMillis"].(int64)
		b.WriteString(id)
		b.WriteByte('|')
		b.WriteString(strconv.FormatInt(ts, 10))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// sortWireMapsForCursor 把 enriched wire maps 排成 cursor 切片所需的序：
// updatedAtMillis DESC，id ASC（与 sortSessionsByUpdatedAt / paginateSessionList 一致）。
func validateCatalogWireMaps(maps []map[string]interface{}) error {
	seen := make(map[string]struct{}, len(maps))
	for index, item := range maps {
		id, _ := item["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("catalog snapshot row %d has empty session ID", index)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("catalog snapshot has duplicate session ID %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
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
	return &catalogSnapshot{Scope: s.scope.identity(), Epoch: s.epoch, CreatedAt: s.createdAt}
}

// Invalidate 丢掉指定 scope 的缓存快照（不打断 inFlight）。用于 fair-home 需要立刻反映
// 磁盘目录删除时强制重建（owner 2026-08-11 幽灵 cccode-* 目录）。
func (c *catalogWireSnapshotCache) Invalidate(scope catalogWireScope) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.scopes, scope)
	c.mu.Unlock()
}

// FenceBackend advances one backend-wide generation under the same lock that owns committed and
// in-flight snapshots. Old builders may finish, but can neither commit nor satisfy waiters.
func (c *catalogWireSnapshotCache) FenceBackend(backendID string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generations[backendID]++
	for scope := range c.scopes {
		if scope.BackendID == backendID {
			delete(c.scopes, scope)
		}
	}
	for scope := range c.inFlight {
		if scope.BackendID == backendID {
			c.inFlight[scope].complete()
			delete(c.inFlight, scope)
		}
	}
	return c.generations[backendID]
}

// FetchOrReuse 是 page-0 路径：缓存命中且未过期 → 复用；否则 singleflight 调 builder 构造
// enriched wire maps 并缓存。builder 只在 miss/expiry 时调用。返回的快照的 maps 是缓存内
// 副本的可读引用（切片元素仍共享 map；调用方只读不写，handler 不再 mutate）。
func (c *catalogWireSnapshotCache) FetchOrReuse(scope catalogWireScope, builder func() ([]map[string]interface{}, error)) (*catalogWireSnapshot, error) {
	return c.FetchOrReuseContext(context.Background(), scope, builder)
}

func (c *catalogWireSnapshotCache) FetchOrReuseContext(ctx context.Context, scope catalogWireScope, builder func() ([]map[string]interface{}, error)) (*catalogWireSnapshot, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	for {
		c.mu.Lock()
		generation := c.generations[scope.BackendID]
		if snap := c.scopes[scope]; snap != nil && snap.generation == generation && !wireSnapshotExpired(snap, c.now()) {
			c.mu.Unlock()
			return snap, nil
		}
		if build := c.inFlight[scope]; build != nil && build.generation == generation {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-build.done:
			}
			c.mu.Lock()
			current := c.generations[scope.BackendID]
			snapshot, buildErr := build.snapshot, build.err
			c.mu.Unlock()
			if current != generation {
				continue
			}
			return snapshot, buildErr
		}
		build := &catalogWireBuild{generation: generation, done: make(chan struct{})}
		c.inFlight[scope] = build
		c.mu.Unlock()

		maps, err := builder()
		cp := append([]map[string]interface{}(nil), maps...)
		if err == nil {
			err = validateCatalogWireMaps(cp)
		}
		c.mu.Lock()
		current := c.generations[scope.BackendID]
		if current == generation {
			if err == nil {
				build.snapshot = &catalogWireSnapshot{scope: scope, generation: generation, epoch: deriveSnapshotEpoch(scope.identity() + "\x00" + wireListFingerprint(cp)), createdAt: c.now(), maps: cp}
				c.scopes[scope] = build.snapshot
			} else {
				build.err = err
			}
		}
		if c.inFlight[scope] == build {
			delete(c.inFlight, scope)
		}
		build.complete()
		c.mu.Unlock()
		if current != generation {
			continue
		}
		return build.snapshot, build.err
	}
}

// Peek 是 page-N 路径：返回缓存快照（不重建、不打上游）。可能返回 nil（该 scope 无快照）。
func (c *catalogWireSnapshotCache) Peek(scope catalogWireScope) *catalogWireSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.scopes[scope]
}

// pageV2 为已声明 catalog_cursor_epoch_v2 的连接计算一页 v2 catalog：
//   - cursor==""（page-0）：FetchOrReuse(builder) 取/建快照，切首页；
//   - cursor!=""（page-N）：Peek（不重建）→ decodeListCursorV2 → validateCursorV2 → 切片；
//
// 返回 wire result map（{sessions, hasMore, nextCursor?}，shape 与 paginateSessionList 一致）
// 或 cursor_stale WireError（staleErr）或硬错误（err：list_failed / 损坏 cursor）。
// page-0 不会 stale（刚建/复用未过期快照）；page-N 的 stale 由 validateCursorV2 判定。
func (c *catalogWireSnapshotCache) pageV2(scope catalogWireScope, cursor string, limit int, builder func() ([]map[string]interface{}, error)) (map[string]interface{}, *WireError, error) {
	if cursor == "" {
		snap, err := c.FetchOrReuse(scope, builder)
		if err != nil {
			return nil, nil, err
		}
		sessions, nextCursor, hasMore, _, derr := sliceCatalogWirePage(snap, "", limit, c.now())
		if derr != nil {
			return nil, nil, derr
		}
		return wireResultMap(sessions, nextCursor, hasMore), nil, nil
	}
	snap := c.Peek(scope)
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
	// Native order is authoritative. Locate the exact SessionID anchor in the frozen snapshot,
	// then slice by index; timestamps are cursor metadata, never a local ordering instruction.
	start := -1
	for i, m := range snap.maps {
		id, _ := m["id"].(string)
		if id == cur.SessionID {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, "", false, cursorStaleEpochMismatch, nil
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
