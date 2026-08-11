package gobridge

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// catalog_cursor_v2_test.go冻结 bridge-owned cursor v2 的契约行为（设计 §4.1.1 / §10
// Phase 0 fixture）。这些是 Phase 0「冻结 fixture」的 contract test：v1→stale、epoch
// mismatch→cursor_stale、TTL 过期→cursor_stale、TTL 覆盖慢速翻页、重启透明。Phase 1 才把
// 快照缓存与 list handler 接线、capability 门控发射接进来。

func encodeV1CursorForFixture(t *testing.T, ts int64, sid string) string {
	t.Helper()
	// 复用 production v1 编码器，构造一条真实的 v1 opaque cursor（Version=1，无 Epoch）。
	c := listCursor{UpdatedAtMillis: ts, SessionID: sid}
	s, err := encodeListCursor(c)
	if err != nil {
		t.Fatalf("encode v1 cursor: %v", err)
	}
	return s
}

func TestCursorV2_RoundTripPreservesFields(t *testing.T) {
	c := listCursorV2{
		Epoch:           deriveSnapshotEpoch("fp-1"),
		UpdatedAtMillis: 1710000000000,
		SessionID:       "ses-a",
	}
	encoded, err := encodeListCursorV2(c)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, isV1, err := decodeListCursorV2(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if isV1 {
		t.Fatal("v2 cursor decoded as v1")
	}
	if decoded.Version != listCursorVersionV2 {
		t.Fatalf("Version = %d, want %d", decoded.Version, listCursorVersionV2)
	}
	if decoded.Epoch != c.Epoch {
		t.Fatalf("Epoch = %q, want %q", decoded.Epoch, c.Epoch)
	}
	if decoded.UpdatedAtMillis != c.UpdatedAtMillis || decoded.SessionID != c.SessionID {
		t.Fatalf("round-trip mismatch: ts=%d sid=%q", decoded.UpdatedAtMillis, decoded.SessionID)
	}
}

func TestCursorV2_V1CursorTreatedAsStale(t *testing.T) {
	// 冻结行为 1：v1 cursor 无 epoch，在 v2 模式下一律 stale（设计 §4.1.1）。
	v1 := encodeV1CursorForFixture(t, 1710000000000, "ses-a")
	cursor, isV1, err := decodeListCursorV2(v1)
	if err != nil {
		t.Fatalf("decode v1 cursor: %v", err)
	}
	if !isV1 {
		t.Fatal("v1 cursor not detected as v1")
	}
	now := time.Now()
	snap := &catalogSnapshot{Scope: "codex", Epoch: deriveSnapshotEpoch("fp-1"), CreatedAt: now}
	if stale, reason := validateCursorV2(cursor, isV1, snap, now); !stale {
		t.Fatal("v1 cursor should be stale under v2 mode")
	} else if reason != cursorStaleV1 {
		t.Fatalf("reason = %q, want %q", reason, cursorStaleV1)
	}
}

func TestCursorV2_MatchingEpochFreshSnapshotOK(t *testing.T) {
	// 冻结行为：epoch 匹配 + 快照未过期 → 可切片。
	now := time.Now()
	epoch := deriveSnapshotEpoch("fp-1")
	snap := &catalogSnapshot{Scope: "codex", Epoch: epoch, CreatedAt: now}
	cursor := listCursorV2{Epoch: epoch, UpdatedAtMillis: 1, SessionID: "ses-a"}
	encoded, _ := encodeListCursorV2(cursor)
	decoded, isV1, err := decodeListCursorV2(encoded)
	if err != nil || isV1 {
		t.Fatalf("decode: err=%v isV1=%v", err, isV1)
	}
	if stale, reason := validateCursorV2(decoded, isV1, snap, now); stale {
		t.Fatalf("matching-epoch fresh cursor should be OK, got stale: %q", reason)
	}
}

func TestCursorV2_EpochMismatchReturnsStale(t *testing.T) {
	// 冻结行为 2：epoch mismatch（catalog 数据变了）→ cursor_stale。
	now := time.Now()
	snap := &catalogSnapshot{Scope: "codex", Epoch: deriveSnapshotEpoch("fp-after-change"), CreatedAt: now}
	cursor := listCursorV2{Epoch: deriveSnapshotEpoch("fp-original"), UpdatedAtMillis: 1, SessionID: "ses-a"}
	encoded, _ := encodeListCursorV2(cursor)
	decoded, isV1, err := decodeListCursorV2(encoded)
	if err != nil || isV1 {
		t.Fatalf("decode: err=%v isV1=%v", err, isV1)
	}
	stale, reason := validateCursorV2(decoded, isV1, snap, now)
	if !stale {
		t.Fatal("epoch mismatch should be stale")
	}
	if reason != cursorStaleEpochMismatch {
		t.Fatalf("reason = %q, want %q", reason, cursorStaleEpochMismatch)
	}
}

func TestCursorV2_TTLExpiredReturnsStaleEvenWithMatchingEpoch(t *testing.T) {
	// 冻结行为 3：TTL 过期但 fingerprint（epoch）未变 → 仍 cursor_stale（设计 §4.1.1）。
	now := time.Now()
	epoch := deriveSnapshotEpoch("fp-stable")
	expired := &catalogSnapshot{
		Scope:     "codex",
		Epoch:     epoch,
		CreatedAt: now.Add(-(catalogSnapshotTTL + time.Second)), // 刚好越过 TTL
	}
	cursor := listCursorV2{Epoch: epoch, UpdatedAtMillis: 1, SessionID: "ses-a"}
	encoded, _ := encodeListCursorV2(cursor)
	decoded, isV1, err := decodeListCursorV2(encoded)
	if err != nil || isV1 {
		t.Fatalf("decode: err=%v isV1=%v", err, isV1)
	}
	stale, reason := validateCursorV2(decoded, isV1, expired, now)
	if !stale {
		t.Fatal("expired snapshot should be stale even with matching epoch")
	}
	if reason != cursorStaleExpired {
		t.Fatalf("reason = %q, want %q", reason, cursorStaleExpired)
	}
}

func TestCursorV2_NoSnapshotReturnsStale(t *testing.T) {
	// 冻结行为：该 scope 无快照 → cursor_stale。
	now := time.Now()
	cursor := listCursorV2{Epoch: deriveSnapshotEpoch("fp-1"), UpdatedAtMillis: 1, SessionID: "ses-a"}
	encoded, _ := encodeListCursorV2(cursor)
	decoded, isV1, err := decodeListCursorV2(encoded)
	if err != nil || isV1 {
		t.Fatalf("decode: err=%v isV1=%v", err, isV1)
	}
	stale, reason := validateCursorV2(decoded, isV1, nil, now)
	if !stale {
		t.Fatal("nil snapshot should be stale")
	}
	if reason != cursorStaleNoSnapshot {
		t.Fatalf("reason = %q, want %q", reason, cursorStaleNoSnapshot)
	}
}

func TestCursorV2_TTLCoversSlowPaginationSession(t *testing.T) {
	// 冻结行为 4：TTL 值覆盖一次慢速翻页会话（设计 §4.1.1「TTL 覆盖一次慢速翻页会话」）。
	if catalogSnapshotTTL <= catalogSlowPaginationSession {
		t.Fatalf("catalogSnapshotTTL (%v) must exceed slow-session (%v)",
			catalogSnapshotTTL, catalogSlowPaginationSession)
	}
	// 一个「慢速翻页会话」中段的 page-N：快照已建 9 分钟（仍在 TTL 内），epoch 匹配，
	// 必须命中而非 cursor_stale。
	now := time.Now()
	epoch := deriveSnapshotEpoch("fp-1")
	midSlowSession := &catalogSnapshot{
		Scope:     "opencode",
		Epoch:     epoch,
		CreatedAt: now.Add(-9 * time.Minute), // < catalogSnapshotTTL(10m), > slow-session(5m) 中段
	}
	cursor := listCursorV2{Epoch: epoch, UpdatedAtMillis: 1, SessionID: "ses-a"}
	encoded, _ := encodeListCursorV2(cursor)
	decoded, isV1, err := decodeListCursorV2(encoded)
	if err != nil || isV1 {
		t.Fatalf("decode: err=%v isV1=%v", err, isV1)
	}
	if stale, reason := validateCursorV2(decoded, isV1, midSlowSession, now); stale {
		t.Fatalf("mid-slow-session cursor within TTL should be OK, got stale: %q", reason)
	}
}

func TestCursorV2_CatalogRestartTransparentWhenDataUnchanged(t *testing.T) {
	// 冻结行为：catalog 子进程/连接重启但数据未变 → 快照以相同 fingerprint 重建，
	// epoch 不变，旧 cursor 仍有效（设计 §4.1.1 末段；cursor 不绑进程生命周期）。
	now := time.Now()
	epoch := deriveSnapshotEpoch("fp-stable") // 数据未变 → fingerprint 相同 → epoch 相同
	// 重启后重建的快照：新 CreatedAt（未过期），相同 Epoch。
	rebuilt := &catalogSnapshot{Scope: "codex", Epoch: epoch, CreatedAt: now}
	// 重启前 page-0 发出的 cursor（携带相同 epoch）。
	cursor := listCursorV2{Epoch: epoch, UpdatedAtMillis: 1, SessionID: "ses-a"}
	encoded, _ := encodeListCursorV2(cursor)
	decoded, isV1, err := decodeListCursorV2(encoded)
	if err != nil || isV1 {
		t.Fatalf("decode: err=%v isV1=%v", err, isV1)
	}
	if stale, reason := validateCursorV2(decoded, isV1, rebuilt, now); stale {
		t.Fatalf("cursor across restart with unchanged data should be OK, got stale: %q", reason)
	}
}

func TestDeriveSnapshotEpoch_DeterministicAndSensitive(t *testing.T) {
	// epoch 确定（同输入同输出）且对 fingerprint 变化敏感。
	a1 := deriveSnapshotEpoch("fp-1")
	a2 := deriveSnapshotEpoch("fp-1")
	b := deriveSnapshotEpoch("fp-2")
	if a1 != a2 {
		t.Fatalf("epoch not deterministic: %q vs %q", a1, a2)
	}
	if a1 == b {
		t.Fatalf("epoch not sensitive to fingerprint: %q == %q", a1, b)
	}
	if len(a1) != 8 {
		t.Fatalf("epoch len = %d, want 8 hex chars", len(a1))
	}
}

func TestDecodeListCursorV2_MalformedAndUnknownVersion(t *testing.T) {
	// 损坏 cursor → error（不是 stale；stale 只用于结构合法但失效）。
	if _, _, err := decodeListCursorV2("!!!not-base64!!!"); err == nil {
		t.Fatal("malformed base64 should error")
	}
	// 未知 version → error。
	raw, _ := json.Marshal(map[string]int{"v": 99})
	if _, _, err := decodeListCursorV2(base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("unknown version should error")
	}
}
