package gobridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// owner 定案（2026-10-20）「仅真实消息活动才排序前移」回归：
// 大文件（>512KB 头读上限）的 updatedAt 用尾部反扫——open-spawn 的 CLI 写入
// 无 timestamp 元数据行（last-prompt/cost-state）刷新 mtime 后，「仅打开」
// 不得把会话活动时间顶到当前；真实对话行（带 timestamp）才推进。
func TestTailActivityIgnoresMetadataOnlyMtime(t *testing.T) {
	dialogAt := time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)
	// 构造 >64KB 会话文件：真实对话行（timestamp=15:00）+ 大段内容 + 尾部元数据行。
	var content []byte
	line := func(obj string) []byte { return []byte(obj + "\n") }
	content = append(content, line(`{"type":"user","timestamp":"2026-10-20T15:00:00Z","message":{"role":"user"}}`)...)
	// 中段撑过 64KB 窗口起点之外（保证对话行在窗口外、元数据行在窗口内）。
	for i := 0; i < 900; i++ {
		content = append(content, line(`{"type":"assistant","timestamp":"2026-10-20T15:00:00Z","padding":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)...)
	}
	content = append(content, line(`{"type":"last-prompt","lastPrompt":"x"}`)...)
	content = append(content, line(`{"type":"cost-state","totalCostUSD":1.0}`)...)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	// 模拟「打开后」：mtime 被刷到 20:00，但最后对话行 timestamp 仍是 15:00。
	openedMtime := time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, openedMtime, openedMtime); err != nil {
		t.Fatal(err)
	}

	got := claudeSessionLastActivityFromTail(path, openedMtime)
	if got.Equal(openedMtime) {
		t.Fatalf("元数据行刷新的 mtime 不得作为活动时间（got mtime %v）", got)
	}
	if !got.Equal(dialogAt) {
		t.Fatalf("活动时间应为最后对话行 timestamp %v，got %v", dialogAt, got)
	}
}

func TestTailActivityFallbackWhenNoTimestampedLines(t *testing.T) {
	// 窗口内完全无 timestamp 行（纯元数据尾 + 文件 < 窗口）：保守退回 mtime。
	path := filepath.Join(t.TempDir(), "meta-only.jsonl")
	content := []byte(`{"type":"cost-state","totalCostUSD":2.0}` + "\n" + `{"type":"last-prompt","lastPrompt":"y"}` + "\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2026, 10, 20, 19, 0, 0, 0, time.UTC)
	got := claudeSessionLastActivityFromTail(path, mtime)
	if !got.Equal(mtime) {
		t.Fatalf("无对话行时退回 mtime %v，got %v", mtime, got)
	}
}
