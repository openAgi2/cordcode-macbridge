package gobridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// catalog_provider.go定义跨后端 Session Catalog 的统一 provider 边界（设计 §4.1 /
// §4.1.1 / §4.2，Phase 1 chunk 1A）。
//
// catalog 与 Session Sync v2 投影是两个平面（§4.2 数据所有权 / §9 SSV2 护栏）：
//   - catalog provider 只回答「这个 scope 有哪些 session、它们的列表元数据是什么」
//     （成员、标题、workspace/project 归属、创建/更新时间、归档/parent/root 状态）。
//   - 它**不**持有任何消息时间线。消息时间线的唯一真相是 Mac Projection Kernel。
//     NormalizedSession 绝不生长 content/message 字段，catalog writer 也不修改 timeline。
//
// 本文件是纯 seam：只定义接口与规范化类型，不接入 live list 路径（接入属 Phase 1C，
// 并由 catalog_cursor_epoch_v2 capability 门控）。

// NormalizedSession 是单个 session 的 catalog 投影（设计 §4.1「Normalized session」）。
// 只承载列表语义字段；非 timeline 消息。字段语义：
//   - StableID：backend catalog 返回的稳定 session id（§4.2 列表成员权威）。
//   - BackendID：所属 backend（opencode/codex/grok/claudecode）。
//   - Title：backend catalog 返回的原生标题（§4.2 标题权威）。provider 不做「首条 assistant
//     文本兜底」——那是 Claude scanner 的兼容行为，不在 native catalog provider 范围。
//   - Directory / ProjectID：workspace/project 归属（§4.2）。
//   - ParentID / IsRoot：parent/root 状态（§4.1）。IsRoot = ParentID 为空。
//   - CreatedAtMillis / UpdatedAtMillis：创建/更新时间（§4.1 recency）。
//   - Archived / ArchivedAtMillis：归档状态（§4.1）。
//   - OrderingKey：backend 排序键（§4.1「backend ordering key (internal only)」）。
//     用于稳定 tie-break；不进入 wire。
type NormalizedSession struct {
	StableID         string
	BackendID        string
	Title            string
	Directory        string
	ProjectID        string
	ParentID         string
	CreatedAtMillis  int64
	UpdatedAtMillis  int64
	Archived         bool
	ArchivedAtMillis int64
	IsRoot           bool
	OrderingKey      string
}

// CatalogQuery 是一次 per-scope catalog 请求（设计 §4.1 Query）。
//
// Cursor 是 bridge-owned opaque cursor（v1 今天；v2 epoch cursor 在 Phase 1C 由
// catalog_cursor_epoch_v2 capability 门控后启用）。它**只**在 bridge 内部用于切片
// 已缓存的 page-0 快照，永不越过 provider 边界进入上游调用：OpenCode /session 是
// array-only 无上游 cursor（Phase 0 冻结）；Codex thread/list / Grok ACP 的上游
// cursor 只用于 MacBridge 内部有界读取，不对外暴露（§4.1「统一分页边界」）。
//
// FetchPage0 在 Phase 1A 只消费 Directory/RootsOnly（决定上游 scope）；Limit/Cursor
// 的真正切片由 Phase 1B 的 snapshot cache + 1C 的 list-handler 接线负责。
type CatalogQuery struct {
	BackendID string
	Directory string
	RootsOnly bool
	Limit     int
	Cursor    string
}

// CatalogPage0 是一次 scope catalog 的有界全量读取结果（设计 §4.1.1 快照模型）。
//
// 快照是 page-0 only：MacBridge 收到 page-0 请求后完成一次该 scope 的有界全量读取，
// 缓存得到的有序轻量集合；后续 page-N 从同一快照切片（§4.1.1）。因此 Page0 故意**不**
// 携带 nextCursor/hasMore——那是 snapshot-cache + cursor 切片层（Phase 1B/1C）的产物，
// 不是 provider 的产物。
//
// Sessions 按 backend 的稳定序返回（§4.2「顺序 = backend catalog 排序」）：OpenCode
// /session 上游按 time.updated desc（Phase 0 冻结），provider 保持上游序，不本地重排。
// Fingerprint 是该集合的确定摘要，由 fingerprintCatalog 派生；catalog_cursor_v2.go
// 的 deriveSnapshotEpoch 再把它压成短 epoch，编码进 v2 cursor。
type CatalogPage0 struct {
	Sessions    []NormalizedSession
	Fingerprint string
}

// SessionCatalogProvider 是每个 backend 的 catalog 源统一边界（设计 §4.1）。
//
// FetchPage0 完成一次 scope 的有界全量读取，返回规范化 catalog 元数据 + 有序
// fingerprint。它**不**按 client cursor/limit 切片——切片是 Phase 1B snapshot cache
// 与 1C list-handler 接线的职责。返回的 Sessions 必须确定性有序，使等量数据下
// fingerprint 稳定（§4.1.1）。
//
// ctx 契约：provider 接受 ctx；但 1A 阶段 OpenCode 适配器尚未把 ctx 透传进 proxy 的
// HTTP client（中途取消/超时真正中断上游调用属 Phase 1D 的 singleflight/timeout/
// cancel/reconnect 装饰器）。在 1D 前，ctx 不会被 provider 中途 honor。
type SessionCatalogProvider interface {
	FetchPage0(ctx context.Context, q CatalogQuery) (CatalogPage0, error)
}

// fingerprintCatalog 由规范化 catalog 集合派生确定摘要（设计 §4.1.1「catalog
// fingerprint」）。摘要覆盖有序的成员 + recency：每个 session 取 `StableID|UpdatedAtMillis`，
// 按 StableID 排序后拼接、sha256。等量数据 → 相同摘要 → 相同 epoch
// （catalog_cursor_v2.go deriveSnapshotEpoch）；新增/删除/更新 session → 摘要变化 →
// 旧 v2 cursor 命中 cursorStaleEpochMismatch。
//
// 内部按 StableID 排序使 fingerprint 与 backend 瞬时返回序解耦：catalog 子进程/连接
// 重启但数据未变时，即便上游行序略有差异，fingerprint 仍不变（§4.1.1「数据未变 →
// epoch 不变」）。fingerprint 不是 wire 字段，不发给 iOS。
func fingerprintCatalog(sessions []NormalizedSession) string {
	sorted := make([]NormalizedSession, len(sessions))
	copy(sorted, sessions)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].StableID < sorted[j].StableID })
	var b strings.Builder
	for _, s := range sorted {
		b.WriteString(s.StableID)
		b.WriteByte('|')
		b.WriteString(strconv.FormatInt(s.UpdatedAtMillis, 10))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
