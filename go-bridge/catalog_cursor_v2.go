package gobridge

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// catalog_cursor_v2.go冻结 bridge-owned 合成 cursor v2 的契约面（设计 §4.1.1 / §10
// Phase 0）。本文件只定义 v2 cursor 的结构、编解码与校验行为并冻结 TTL，落 contract
// test；它**不**接入 live list 路径——paginateSessionList 仍走 v1 内容 cursor +
// malformed-时盲切首页，直到 Phase 1 实现 page-0 有序快照缓存**并且**连接在 hello
// 声明 catalog_cursor_epoch_v2 后才对 iOS 发射 v2（发布顺序约束，§10：capability 门控
// 上线前 MacBridge 不得对任何连接发射 v2 cursor）。
//
// Phase 0 的产物是「冻结 fixture」：v2 结构 + 4 个校验行为 + TTL 值，bind contract test。
// Phase 1 负责真正的快照缓存、list handler 接线与 capability 门控发射。

const listCursorVersionV2 = 2

// catalogSnapshotTTL 是 page-0 有序快照的最大有效时长（设计 §4.1.1，Phase 0 冻结）。
// 取值必须覆盖一次「慢速翻页会话」：用户在 session 列表里逐页浏览、被中途打断、稍后继续，
// 在此期间内同一 cursor 链的 page-N 应命中同一快照切片，而不是被 cursor_stale 打断回
// page-0。10 分钟覆盖慢速翻页绰绰有余，同时给缓存的 catalog 一个有界的陈旧上限；用户离开
// 更久时正确返回 cursor_stale，让下一次 page-0 补齐新增/更新 session。
//
// contract test 断言它大于慢速翻页阈值（catalogSlowPaginationSession），使冻结值可被校验。
const catalogSnapshotTTL = 10 * time.Minute

// catalogSlowPaginationSession 是一次「慢速翻页会话」的保守估计上限，用于 freeze 校验
// catalogSnapshotTTL 是否足以覆盖（设计 §4.1.1「TTL 覆盖一次慢速翻页会话」）。用户逐页
// 阅读、被打断后继续，5 分钟是合理上限；TTL 必须 > 该值。
const catalogSlowPaginationSession = 5 * time.Minute

// listCursorV2 是 v2 opaque bridge-owned cursor，指向某个 scope 的 page-0 有序快照。
// 相对 v1 增加 Epoch：由 catalog fingerprint 派生的短 hash，在 page-0 时随快照一并捕获。
// page-N 携带该 Epoch 回来时，MacBridge 据此判定底层 catalog 是否变化（或快照是否过期），
// 任一不满足返回 cursor_stale，而不是切片一个陈旧/已失效的视图（设计 §4.1.1）。
//
// Epoch 不是被第三轮删除的 generation：它绑定数据 fingerprint（catalog 成员/metadata 摘要），
// 编码在 opaque cursor 内，iOS 不订阅、不追踪；catalog 子进程/连接重启但数据未变时，
// 快照以相同 fingerprint 重建、Epoch 不变，旧 cursor 仍有效（设计 §4.1.1 末段）。
type listCursorV2 struct {
	Version         int    `json:"v"`
	Epoch           string `json:"ep"`
	UpdatedAtMillis int64  `json:"ts"`
	SessionID       string `json:"sid"`
}

// catalogSnapshot 是一次 page-0 有序（updatedAtMillis DESC, id ASC）轻量 session-metadata
// 集合，供同一 cursor 链的后续 page-N 切片。Epoch 是 page-0 时捕获的 fingerprint hash；
// CreatedAt 用于 TTL 判定。Phase 0 只定义结构与校验契约；真正的内存缓存与 list handler
// 接线属 Phase 1。
type catalogSnapshot struct {
	Scope     string
	Epoch     string
	CreatedAt time.Time
	Sessions  []map[string]interface{}
}

// cursor v2 校验原因码（设计 §4.1.1 / §4.1：cursor_stale 是 list/history 共享通用失效
// code，Phase 0 冻结触发原因）。
const (
	cursorStaleOK            = ""
	cursorStaleV1            = "v1 cursor has no epoch"
	cursorStaleEpochMismatch = "snapshot epoch mismatch"
	cursorStaleExpired       = "snapshot ttl expired"
	cursorStaleNoSnapshot    = "no valid snapshot for scope"
)

// encodeListCursorV2 编码 v2 opaque cursor。调用方必须已填好 Epoch（来自 page-0 快照）。
func encodeListCursorV2(c listCursorV2) (string, error) {
	c.Version = listCursorVersionV2
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// decodeListCursorV2 解码 opaque cursor。返回 (cursor, isV1, err)：
//   - v==2：isV1=false，返回完整 v2 cursor。
//   - v==1：isV1=true（v1 无 epoch）。在 v2 模式下（连接已声明 catalog_cursor_epoch_v2）
//     v1 cursor 一律视为 stale（设计 §4.1.1「v1 无 epoch 视为 stale」）；未声明的旧连接
//     不走此路径（Phase 8B 起由统一 gate 拒绝）。
//   - 无 version / 未知 version / 损坏：err。
//
// isV1 由调用方配合 validateCursorV2 转成 cursorStaleV1，而不是直接报错，以便 capability
// 门控路径区分「v2 连接收到 v1 cursor → cursor_stale」与「损坏 cursor → error」。
func decodeListCursorV2(s string) (c listCursorV2, isV1 bool, err error) {
	data, derr := base64.RawURLEncoding.DecodeString(s)
	if derr != nil {
		return listCursorV2{}, false, fmt.Errorf("malformed list cursor")
	}
	var decoded listCursorV2
	if jerr := json.Unmarshal(data, &decoded); jerr != nil {
		return listCursorV2{}, false, fmt.Errorf("malformed list cursor")
	}
	switch decoded.Version {
	case listCursorVersionV2:
		return decoded, false, nil
	case listCursorVersion: // v1
		return listCursorV2{Version: decoded.Version}, true, nil
	default:
		return listCursorV2{}, false, fmt.Errorf("unsupported list cursor version %d", decoded.Version)
	}
}

// deriveSnapshotEpoch 由 catalog fingerprint 派生短 epoch hash（设计 §4.1.1）。fingerprint
// 是 catalog 成员/metadata 的有序摘要（具体构造在 Phase 1 provider 内）；epoch 取其 sha256
// 前 8 hex 字节，足以区分数据变化，又短到可编码进 opaque cursor。确定且无随机性，便于
// catalog 重启后以相同数据重建得到相同 epoch。
func deriveSnapshotEpoch(fingerprint string) string {
	sum := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(sum[:4]) // 8 hex chars
}

// snapshotExpired 判定快照是否超过 TTL（设计 §4.1.1「TTL 过期无有效快照返回 cursor_stale」）。
func snapshotExpired(s catalogSnapshot, now time.Time) bool {
	if s.CreatedAt.IsZero() {
		return true
	}
	return now.Sub(s.CreatedAt) > catalogSnapshotTTL
}

// validateCursorV2 是 v2 cursor 能否对当前快照切片的权威判定（设计 §4.1.1，Phase 0 冻结）。
// 返回 (stale, reason)；stale=false 时可切片。判定顺序固定，使 reason 在 contract test 里
// 确定可复现：
//  1. v1 cursor（无 epoch）→ cursorStaleV1。
//  2. 该 scope 无快照（snapshot==nil）→ cursorStaleNoSnapshot。
//  3. 快照 TTL 过期（即使 epoch 未变）→ cursorStaleExpired。
//  4. cursor.Epoch != 快照.Epoch（catalog 数据变了）→ cursorStaleEpochMismatch。
//
// 注意顺序：TTL 过期先于 epoch mismatch——「TTL 到期但 fingerprint 未变」也返回 cursor_stale
// （§4.1.1）；而 catalog 子进程重启数据未变时快照以相同 epoch 重建，过期检查在新快照上
// 通过，旧 cursor 仍有效。
func validateCursorV2(cursor listCursorV2, isV1 bool, snapshot *catalogSnapshot, now time.Time) (stale bool, reason string) {
	if isV1 {
		return true, cursorStaleV1
	}
	if snapshot == nil {
		return true, cursorStaleNoSnapshot
	}
	if snapshotExpired(*snapshot, now) {
		return true, cursorStaleExpired
	}
	if cursor.Epoch != snapshot.Epoch {
		return true, cursorStaleEpochMismatch
	}
	return false, cursorStaleOK
}
