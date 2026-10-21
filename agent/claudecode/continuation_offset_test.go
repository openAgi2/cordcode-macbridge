package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// S-2 suffix 读等价：单段 [from, cursor) 的解析结果 == 全前缀解析的对应尾部
//（dedup 键不相交时）。多段场景同构。
func TestLoadClaudeContinuationHistoryAtSegmentsFromSuffixEquivalence(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 0; i < 40; i++ {
		// 逐行合法 JSON（rich-history reader 逐行扫描；非 transcript 形状行被
		// 跳过——等价性断言以「行级 reader 输出」为准，构造被保留的行）。
		// 内容逐行唯一（dedup 键 = content+timestamp）——尾部等价断言的前提。
		lines = append(lines, `{"type":"user","message":{"role":"user","content":"q-`+itoa(i)+strings.Repeat("x", 20)+`"},"uuid":"u-`+itoa(i)+`","timestamp":"2026-10-19T00:00:00Z"}`)
	}
	content := strings.Join(lines, "\n") + "\n"
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	fullSegment := core.TranscriptSourceSegment{Identity: "s", Path: path, Cursor: int64(len(content))}
	full, _, err := loadClaudeContinuationHistoryAtSegments(context.Background(), []core.TranscriptSourceSegment{fullSegment})
	if err != nil {
		t.Fatal(err)
	}
	if len(full) == 0 {
		t.Fatal("fixture produced no entries — adjust fixture to reader contract")
	}

	// 找第 20 行末尾的字节偏移作为 from。
	offset := 0
	for i := 0; i < 20; i++ {
		offset += len(lines[i]) + 1
	}
	suffix, _, err := loadClaudeContinuationHistoryAtSegmentsFrom(context.Background(), []core.TranscriptSourceSegment{fullSegment}, []int64{int64(offset)})
	if err != nil {
		t.Fatal(err)
	}
	if len(suffix) == 0 || len(suffix) >= len(full) {
		t.Fatalf("suffix entries=%d full=%d — expect non-empty proper suffix", len(suffix), len(full))
	}
	// 尾部等价：full 的最后 len(suffix) 条 == suffix（dedup 键不相交构造下）。
	tail := full[len(full)-len(suffix):]
	for i := range suffix {
		if claudeHistoryDedupKey(suffix[i]) != claudeHistoryDedupKey(tail[i]) {
			t.Fatalf("suffix[%d] dedup key mismatch: %v != %v", i, claudeHistoryDedupKey(suffix[i]), claudeHistoryDedupKey(tail[i]))
		}
	}

	// 非法 offset 边界：from > cursor / 负值 → ErrUnexpectedEOF。
	if _, _, err := loadClaudeContinuationHistoryAtSegmentsFrom(context.Background(), []core.TranscriptSourceSegment{fullSegment}, []int64{int64(len(content)) + 1}); err == nil {
		t.Fatal("from > cursor must fail")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
