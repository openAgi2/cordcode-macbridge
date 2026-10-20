package gobridge

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"time"
)

// claudeTailActivityScanLimit：尾部反扫上限（64KB——元数据行（last-prompt/
// cost-state/custom-title 等，无 timestamp 字段）之后的对话行通常就在近尾；
// 22MB 会话实测尾段密度远低于该窗口）。
const claudeTailActivityScanLimit = 64 * 1024

// claudeSessionLastActivityFromTail 返回文件尾部最后一条带 timestamp 的行时间。
// 背景（2026-10-20 owner 定案「仅真实消息活动才排序前移」）：>512KB 的大会话
// updatedAt 此前 fallback 到文件 mtime——open-spawn 启动的 Claude CLI 会写
// 无 timestamp 的元数据行（last-prompt/cost-state）刷新 mtime → 「仅打开会话」
// 就把该会话顶到列表最前。尾部反扫跳过这些元数据行，取真实对话行的最后
// timestamp 作为活动时间；窗口内找不到时退回 mtime（保守：宁可排序前移也不
// 把会话的时间显示为零值）。
func claudeSessionLastActivityFromTail(path string, mtime time.Time) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return mtime
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return mtime
	}
	size := info.Size()
	window := claudeTailActivityScanLimit
	if size < int64(window) {
		window = int(size)
	}
	if _, err := f.Seek(size-int64(window), io.SeekStart); err != nil {
		return mtime
	}
	scanner := bufio.NewScanner(io.LimitReader(f, int64(window)))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var last time.Time
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(line, &raw) != nil || raw.Timestamp == "" {
			continue // 元数据行（无 timestamp）不构成「消息活动」
		}
		if parsed, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil && parsed.After(last) {
			last = parsed
		}
	}
	if last.IsZero() {
		return mtime
	}
	return last
}
