package claudecode

// N1（评审 R1 复核，2026-10-21）：后缀读取 API 的边界回归。原实现 LimitReader+Seek
// 实际读 [from, from+cursor)——cursor 之后的行被多读。修复 = io.NewSectionReader
// 精确读 [from, cursor)。本测试用「文件比 cursor 长」的形态钉住边界：fixture 尾部
// 在 cut 之后追加行，后缀读取不得读进它们。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestSuffixReadRespectsCursorWhenFileGrew(t *testing.T) {
	dir := t.TempDir()
	var cutLines []string
	for i := 0; i < 10; i++ {
		cutLines = append(cutLines,
			`{"type":"user","message":{"role":"user","content":"q-`+itoa(i)+strings.Repeat("a", 30)+`"},"uuid":"u-`+itoa(i)+`","timestamp":"2026-10-21T00:00:00Z"}`)
	}
	var grownLines []string
	for i := 10; i < 20; i++ {
		grownLines = append(grownLines,
			`{"type":"user","message":{"role":"user","content":"q-`+itoa(i)+strings.Repeat("b", 30)+`"},"uuid":"u-`+itoa(i)+`","timestamp":"2026-10-21T00:01:00Z"}`)
	}
	cutContent := strings.Join(cutLines, "\n") + "\n"
	path := filepath.Join(dir, "transcript.jsonl")
	// 文件 = cut 前缀 + cut 之后的追加段（模拟 transcript 在 checkpoint 之后增长）。
	if err := os.WriteFile(path, []byte(cutContent+strings.Join(grownLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// from 取第 5 行末尾；cursor = cut（10 行边界）。合法后缀 = [from, cut) = 行 6-10。
	offset := 0
	for i := 0; i < 5; i++ {
		offset += len(cutLines[i]) + 1
	}
	segment := core.TranscriptSourceSegment{Identity: "s", Path: path, Cursor: int64(len(cutContent))}
	entries, _, err := loadClaudeContinuationHistoryAtSegmentsFrom(
		context.Background(), []core.TranscriptSourceSegment{segment}, []int64{int64(offset)})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("suffix read returned %d entries, want 5 ([from, cursor) = 行 6-10，不得读进 cut 之后的追加段)", len(entries))
	}
	for i := range entries {
		if !strings.Contains(entries[i].Content, "q-"+itoa(i+5)) {
			t.Fatalf("entry %d content=%q, want cut 之内的行 q-%d", i, entries[i].Content, i+5)
		}
	}
}
