package gobridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
)

// messagesRevision returns a stable 16-hex-char hash of the marshaled wire
// messages, used as an ETag-style revision for get_session_messages. Empty
// string on marshal failure (caller then skips the short-circuit and attaches
// nothing — client falls back to fetching).
//
// 只 hash messages（不 hash contextUsage/cursors）：resident probe 关心的是消息有没有变；
// contextUsage 在 unchanged 时可能略滞后一次，下次内容变化时随之刷新，可接受。
func messagesRevision(messages []map[string]interface{}) string {
	raw, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16])
}

// applyIfNoneMatch implements the IfNoneMatchRevision short-circuit for
// get_session_messages: when the client's last revision matches the current
// messages' revision, return a tiny {unchanged:true, revision} body instead of
// the full (potentially ~hundreds-of-KB) message payload. Otherwise attach the
// fresh revision and return the full payload.
//
// 这把 idle resident probe 的单次成本从 ~685KB 全量传输降到几十字节；内容真变化时
// revision 必然不同 → 立即全量返回，延迟不变。对老客户端（不带 ifNoneMatchRevision）
// 透明：永远走全量分支（ifNoneMatch == ""）。
func applyIfNoneMatch(payload map[string]interface{}, ifNoneMatch string) map[string]interface{} {
	msgs, _ := payload["messages"].([]map[string]interface{})
	rev := messagesRevision(msgs)
	if rev == "" {
		return payload
	}
	if ifNoneMatch != "" && ifNoneMatch == rev {
		slog.Info("go-bridge: get_session_messages etag hit (returning unchanged, body omitted)",
			"revision", rev, "skipped_messages", len(msgs))
		return map[string]interface{}{"unchanged": true, "revision": rev}
	}
	payload["revision"] = rev
	return payload
}
