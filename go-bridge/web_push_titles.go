package gobridge

import (
	"strings"
	"sync"
)

// web_push_titles.go — 通知标题缓存（设计 delta §2.2，监工指令 1 号）。
//
// 唯一写入路径是真实 list_sessions 响应（authoritative catalog 字段）；
// producer 在构造 PushIntent 时读取，用于通知 Title。缓存未命中返回空串，
// 调用方按设计 delta §2.1 诚实回退到无标题文案——绝不编造标题。

const (
	// webPushTitleCacheMax：bounded map 上限；超出时逐出任意条目（标题可由下次
	// catalog 刷新重新写入，逐出只影响通知标题的命中率，不影响正确性）。
	webPushTitleCacheMax = 1024
	// webPushTitleMaxRunes：通知 Title 中 session 标题的截断上限（设计 delta §2.3）。
	webPushTitleMaxRunes = 48
)

type webPushTitleCache struct {
	mu     sync.Mutex
	titles map[string]string // "backendID|sessionID" → sanitized title
}

func newWebPushTitleCache() *webPushTitleCache {
	return &webPushTitleCache{titles: make(map[string]string)}
}

// noteFromWire 从一条真实 list_sessions 响应提取 id→title。
// 只接受清洗后非空的标题；空标题不覆盖已有值（诚实保留上次 authoritative 读取）。
func (c *webPushTitleCache) noteFromWire(backendID string, result map[string]interface{}) {
	if c == nil || backendID == "" || result == nil {
		return
	}
	sessions, ok := result["sessions"].([]map[string]interface{})
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, session := range sessions {
		id, _ := session["id"].(string)
		if id == "" {
			continue
		}
		title, _ := session["title"].(string)
		title = webPushSanitizeSessionTitle(title)
		if title == "" {
			continue
		}
		c.titles[backendID+"|"+id] = title
	}
	for len(c.titles) > webPushTitleCacheMax {
		for key := range c.titles {
			delete(c.titles, key)
			break
		}
	}
}

func (c *webPushTitleCache) get(backendID, sessionID string) string {
	if c == nil || backendID == "" || sessionID == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.titles[backendID+"|"+sessionID]
}

// webPushSanitizeSessionTitle 剥控制字符、TrimSpace、按 rune 截断到 48 + "…"。
// 清洗后为空 → ""（视为缺失，调用方走无标题回退）。
func webPushSanitizeSessionTitle(s string) string {
	var builder strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		builder.WriteRune(r)
	}
	cleaned := strings.TrimSpace(builder.String())
	runes := []rune(cleaned)
	if len(runes) > webPushTitleMaxRunes {
		return string(runes[:webPushTitleMaxRunes]) + "…"
	}
	return cleaned
}
