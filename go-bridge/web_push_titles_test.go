package gobridge

import (
	"strings"
	"testing"
)

// 设计 delta §2.2/§2.3（监工指令 1 号）—— 通知标题缓存与清洗。

func TestSanitizeSessionTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   \t\n", ""},
		{"control chars stripped", "ab\x00\x1fc", "abc"},
		{"trim", "  hello  ", "hello"},
		{"ascii short", "Fix login bug", "Fix login bug"},
		{"cjk preserved", "修复登录问题", "修复登录问题"},
	}
	for _, tc := range cases {
		if got := webPushSanitizeSessionTitle(tc.in); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestSanitizeSessionTitleTruncatesAt48Runes(t *testing.T) {
	long := strings.Repeat("字", 60)
	got := webPushSanitizeSessionTitle(long)
	if want := strings.Repeat("字", 48) + "…"; got != want {
		t.Fatalf("truncation mismatch: got %d runes want %d runes", len([]rune(got)), len([]rune(want)))
	}
	exactly := strings.Repeat("a", 48)
	if got := webPushSanitizeSessionTitle(exactly); got != exactly {
		t.Fatalf("48-rune input must not be truncated, got %q", got)
	}
}

func TestTitleCacheNoteFromWireAndIsolation(t *testing.T) {
	cache := newWebPushTitleCache()
	cache.noteFromWire("codex", map[string]interface{}{
		"sessions": []map[string]interface{}{
			{"id": "s1", "title": "Alpha"},
			{"id": "s2", "title": ""},
			{"id": "", "title": "ghost"},
		},
	})
	if got := cache.get("codex", "s1"); got != "Alpha" {
		t.Fatalf("s1 = %q", got)
	}
	// 空标题不得覆盖已有值；缺失 session 读空。
	if got := cache.get("codex", "s2"); got != "" {
		t.Fatalf("s2 = %q want empty", got)
	}
	// backend 隔离
	cache.noteFromWire("claudecode", map[string]interface{}{
		"sessions": []map[string]interface{}{{"id": "s1", "title": "Beta"}},
	})
	if got := cache.get("codex", "s1"); got != "Alpha" {
		t.Fatalf("codex/s1 must stay Alpha, got %q", got)
	}
	if got := cache.get("claudecode", "s1"); got != "Beta" {
		t.Fatalf("claude/s1 = %q", got)
	}
	// nil cache 全部安全
	var nilCache *webPushTitleCache
	if nilCache.get("codex", "s1") != "" {
		t.Fatal("nil cache must return empty")
	}
	nilCache.noteFromWire("codex", nil) // must not panic
}

func TestTitleCacheBounded(t *testing.T) {
	cache := newWebPushTitleCache()
	for i := 0; i < webPushTitleCacheMax+50; i++ {
		cache.noteFromWire("codex", map[string]interface{}{
			"sessions": []map[string]interface{}{{"id": strings.Repeat("s", 8) + string(rune('a'+i%26)) + strings.Repeat("x", 20) + strings.Repeat("0", 20), "title": "t"}},
		})
	}
	cache.mu.Lock()
	size := len(cache.titles)
	cache.mu.Unlock()
	if size > webPushTitleCacheMax {
		t.Fatalf("cache size %d exceeds bound %d", size, webPushTitleCacheMax)
	}
}

// 设计 delta §2.1 —— 通知内容模型：authoritative 标题 + 无标题诚实回退。
// owner 2026-08-27 更新：completion 正文优先真实回复预览，缺失回退固定文案。
func TestBuildWebPushNotificationTextWithSessionTitle(t *testing.T) {
	title, body := buildWebPushNotificationText(WebPushKindCompletion, "修复登录问题", "")
	if title != "CordCode · 修复登录问题" {
		t.Fatalf("completion title = %q", title)
	}
	if body != "Mac 上的会话已完成，点击查看结果" {
		t.Fatalf("completion fallback body = %q", body)
	}
	title, body = buildWebPushNotificationText(WebPushKindCompletion, "修复登录问题", "已完成登录修复，共改动 3 个文件")
	if body != "已完成登录修复，共改动 3 个文件" {
		t.Fatalf("completion preview body = %q", body)
	}
	title, body = buildWebPushNotificationText(WebPushKindPermission, "  ", "")
	if title != "CordCode · 需要审批" {
		t.Fatalf("permission fallback title = %q", title)
	}
	if body != "Mac 上的会话需要审批，点击处理" {
		t.Fatalf("permission body = %q", body)
	}
	// 标题进入前先清洗截断——通知 Title 组装结果必须落在 SW 端 200 上限内。
	long := strings.Repeat("标", 300)
	title, _ = buildWebPushNotificationText(WebPushKindCompletion, long, "")
	if len([]rune(title)) > 200 {
		t.Fatalf("composed title exceeds SW limit: %d runes", len([]rune(title)))
	}
	if !strings.HasSuffix(title, "…") {
		t.Fatalf("long title must end with ellipsis, got %q", title)
	}
}

// 设计 delta §2.2 —— producer 携带 SessionTitle（缓存命中时）。
func TestProducerCarriesSessionTitle(t *testing.T) {
	enableKindGateForTest(t, WebPushKindCompletion)
	kernel := producerKernelWithRunningTurn(t)
	intent := pushIntentForRelayTerminal(kernel, "codex", "prod-1", "turn_completed", nil, "  带空格的标题  ")
	if intent == nil {
		t.Fatal("intent expected")
	}
	if intent.SessionTitle != "带空格的标题" {
		t.Fatalf("SessionTitle = %q want sanitized title", intent.SessionTitle)
	}
	// 被动路径同样携带
	passive := pushIntentForPassiveEvent(kernel, "codex", "prod-1", "turn_completed", nil, "外部")
	if passive == nil || passive.SessionTitle != "外部" {
		t.Fatalf("passive title carry failed: %+v", passive)
	}
}
