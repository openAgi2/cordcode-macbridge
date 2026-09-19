package claudecode

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// writeContinuityFixture 在独立临时目录写一个 session transcript。
func writeContinuityFixture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session-a.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func reads() int64 { return claudeContinuityFileReads.Load() }

// warm hit 零读盘（评审 N1：用读盘计数证明，不再用共享底层数组 alias——
// 那会把危险契约固化）。cold miss 计数递增。
func TestInspectTranscriptContinuityWarmHitDoesNotReread(t *testing.T) {
	resetClaudeContinuityCacheForTest()
	path := writeContinuityFixture(t,
		`{"type":"system","subtype":"compact_boundary","uuid":"bnd-1","timestamp":"2026-09-19T00:00:00Z"}`,
	)
	first := InspectTranscriptContinuity(path)
	if len(first.BoundaryIDs) != 1 || first.BoundaryIDs[0] != "bnd-1" {
		t.Fatalf("first inspect boundaries = %v, want [bnd-1]", first.BoundaryIDs)
	}
	if got := reads(); got != 1 {
		t.Fatalf("cold miss reads = %d, want 1", got)
	}

	second := InspectTranscriptContinuity(path)
	if len(second.BoundaryIDs) != 1 || second.BoundaryIDs[0] != "bnd-1" {
		t.Fatalf("second inspect boundaries = %v, want [bnd-1]", second.BoundaryIDs)
	}
	if got := reads(); got != 1 {
		t.Fatalf("warm hit re-read the file: reads = %d, want 1", got)
	}

	// defensive copy（评审 N1）：调用方修改返回值不得污染缓存。
	second.BoundaryIDs[0] = "mutated"
	third := InspectTranscriptContinuity(path)
	if third.BoundaryIDs[0] != "bnd-1" {
		t.Fatalf("caller mutation polluted the cache: got %q", third.BoundaryIDs[0])
	}
}

// 指纹（size+mtime）变化必须失效重读：追加新 compact_boundary 后能看到。
func TestInspectTranscriptContinuityInvalidatesOnFingerprintChange(t *testing.T) {
	resetClaudeContinuityCacheForTest()
	path := writeContinuityFixture(t,
		`{"type":"user","uuid":"u1","timestamp":"2026-09-19T00:00:00Z"}`,
	)
	if got := InspectTranscriptContinuity(path); len(got.BoundaryIDs) != 0 {
		t.Fatalf("initial boundaries = %v, want none", got.BoundaryIDs)
	}
	if reads() != 1 {
		t.Fatalf("reads = %d, want 1", reads())
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"system","subtype":"compact_boundary","uuid":"bnd-2","timestamp":"2026-09-19T00:01:00Z"}` + "\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// size 已变化即构成新指纹；显式推进 mtime 兜底文件系统时间粒度。
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	updated := InspectTranscriptContinuity(path)
	if len(updated.BoundaryIDs) != 1 || updated.BoundaryIDs[0] != "bnd-2" {
		t.Fatalf("updated boundaries = %v, want [bnd-2] after fingerprint change", updated.BoundaryIDs)
	}
	if reads() != 2 {
		t.Fatalf("reads = %d, want 2 (fingerprint change must re-read)", reads())
	}
}

// 同尺寸但 mtime 变化（内容原地重写）：指纹失效，重读后看到新内容。
func TestInspectTranscriptContinuitySameSizeMtimeChangeInvalidates(t *testing.T) {
	resetClaudeContinuityCacheForTest()
	dir := t.TempDir()
	path := filepath.Join(dir, "session-a.jsonl")
	lineA := `{"type":"system","subtype":"compact_boundary","uuid":"bnd-aa","timestamp":"2026-09-19T00:00:00Z"}`
	lineB := `{"type":"system","subtype":"compact_boundary","uuid":"bnd-bb","timestamp":"2026-09-19T00:00:00Z"}`
	if len(lineA) != len(lineB) {
		t.Fatalf("fixture lines must be same length: %d != %d", len(lineA), len(lineB))
	}
	if err := os.WriteFile(path, []byte(lineA+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := InspectTranscriptContinuity(path); got.BoundaryIDs[0] != "bnd-aa" {
		t.Fatalf("initial boundary = %q, want bnd-aa", got.BoundaryIDs[0])
	}

	// 原地重写为同长度不同内容 + 推进 mtime（size 不变，只有 mtime 变）。
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(path, []byte(lineB+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if got := InspectTranscriptContinuity(path); got.BoundaryIDs[0] != "bnd-bb" {
		t.Fatalf("same-size rewrite boundary = %q, want bnd-bb (mtime must invalidate)", got.BoundaryIDs[0])
	}
	if reads() != 2 {
		t.Fatalf("reads = %d, want 2", reads())
	}
}

// 删除后重建 / path 复用：stat 失败返回空结果（无陈旧泄漏）；重建后按新指纹读。
func TestInspectTranscriptContinuityDeleteAndRecreate(t *testing.T) {
	resetClaudeContinuityCacheForTest()
	path := writeContinuityFixture(t,
		`{"type":"system","subtype":"compact_boundary","uuid":"bnd-old","timestamp":"2026-09-19T00:00:00Z"}`,
	)
	if got := InspectTranscriptContinuity(path); got.BoundaryIDs[0] != "bnd-old" {
		t.Fatalf("initial boundary = %q", got.BoundaryIDs[0])
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := InspectTranscriptContinuity(path); len(got.BoundaryIDs) != 0 {
		t.Fatalf("deleted file returned stale boundaries %v", got.BoundaryIDs)
	}
	// 同 path 重建为不同内容：不得命中旧条目。
	if err := os.WriteFile(path, []byte(`{"type":"system","subtype":"compact_boundary","uuid":"bnd-new","timestamp":"2026-09-19T00:02:00Z"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := InspectTranscriptContinuity(path); got.BoundaryIDs[0] != "bnd-new" {
		t.Fatalf("recreated file boundary = %q, want bnd-new", got.BoundaryIDs[0])
	}
}

// 并发 miss：同一指纹的并发检查合并为恰好一次真实读盘（singleflight，
// 评审 R2-B2），所有调用者拿到一致结果。
func TestInspectTranscriptContinuityConcurrentMiss(t *testing.T) {
	resetClaudeContinuityCacheForTest()
	path := writeContinuityFixture(t,
		`{"type":"system","subtype":"compact_boundary","uuid":"bnd-c","timestamp":"2026-09-19T00:00:00Z"}`,
		`{"type":"system","subtype":"compact_boundary","uuid":"bnd-d","timestamp":"2026-09-19T00:01:00Z"}`,
	)
	const n = 16
	results := make([]TranscriptContinuity, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = InspectTranscriptContinuity(path)
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if len(r.BoundaryIDs) != 2 || r.BoundaryIDs[0] != "bnd-c" || r.BoundaryIDs[1] != "bnd-d" {
			t.Fatalf("goroutine %d result = %v, want [bnd-c bnd-d]", i, r.BoundaryIDs)
		}
	}
	// 同一指纹的并发 miss 必须合并为恰好一次真实读，不允许 stampede。
	if reads() != 1 {
		t.Fatalf("reads = %d, want exactly 1 (concurrent same-fingerprint miss must merge)", reads())
	}
}

// 容量逐出（评审 B4：有界生命周期）：超容量的最旧条目被逐出，再访问即重读。
func TestInspectTranscriptContinuityCapacityEviction(t *testing.T) {
	resetClaudeContinuityCacheForTest()
	setClaudeContinuityCacheCapacityForTest(2)
	defer setClaudeContinuityCacheCapacityForTest(claudeContinuityCacheDefaultCapacity)

	p1 := writeContinuityFixture(t, `{"type":"user","uuid":"u1","timestamp":"2026-09-19T00:00:00Z"}`)
	p2 := writeContinuityFixture(t, `{"type":"user","uuid":"u2","timestamp":"2026-09-19T00:00:00Z"}`)
	p3 := writeContinuityFixture(t, `{"type":"user","uuid":"u3","timestamp":"2026-09-19T00:00:00Z"}`)
	InspectTranscriptContinuity(p1)
	InspectTranscriptContinuity(p2)
	InspectTranscriptContinuity(p3) // 容量 2：p1 被逐出
	if reads() != 3 {
		t.Fatalf("cold reads = %d, want 3", reads())
	}
	InspectTranscriptContinuity(p1) // 逐出后重读（read 4）；重插入 p1 又把 p2 挤出（FIFO）
	if reads() != 4 {
		t.Fatalf("post-eviction reads = %d, want 4 (evicted entry must re-read)", reads())
	}
	InspectTranscriptContinuity(p3) // 仍在缓存（此时 FIFO 序为 [p3,p1]）
	if reads() != 4 {
		t.Fatalf("cached entry re-read after eviction cycle: reads = %d, want 4", reads())
	}
}
