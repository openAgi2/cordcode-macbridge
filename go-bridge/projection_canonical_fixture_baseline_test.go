package gobridge

// projection_canonical_fixture_baseline_test.go — PERF-S0B（iOS 仓
// docs/2026-08-23-message-web-gpuix-borrowing-realistic-assessment.md §13）：
// canonical projection fixtures（真实 owner session + 官方样本管线产物）经生产
// Restore→Snapshot→Marshal→gzip 的可重复基线（中位数+字节），冻结 before 数据。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func canonicalFixturePaths(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "docs", "protocol", "samples", "session-projection-v2", "fixtures")
	out := map[string]string{
		"claude-tool-dense":       filepath.Join(root, "tool-dense.json"),
		"claude-oversized-output": filepath.Join(root, "oversized-output.json"),
		"claude-long-text":        filepath.Join(root, "long-text.json"),
		"codex-web-catalog":       filepath.Join(root, "web-perf", "codex-web-catalog.json"),
		"opencode-web-todos":      filepath.Join(root, "web-perf", "opencode-web-todos.json"),
	}
	for name, path := range out {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("fixture %s missing: %v", name, err)
		}
	}
	return out
}

func TestCanonicalFixtureServingBaseline(t *testing.T) {
	const warmup = 1
	const samples = 5
	type point struct {
		Turns    int     `json:"turns"`
		CloneMs  float64 `json:"cloneMedianMs"`
		Marshal  float64 `json:"marshalMedianMs"`
		GzipMs   float64 `json:"gzipMedianMs"`
		RawBytes int     `json:"rawBytes"`
		GzBytes  int     `json:"gzipBytes"`
	}
	points := map[string]point{}
	for name, path := range canonicalFixturePaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var projection SessionProjection
		if err := json.Unmarshal(data, &projection); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		reducer := NewProjectionReducer()
		reducer.Restore("bench", name, projection)

		cloneMs := medianDuration(warmup+samples, func() { _, _ = reducer.Snapshot("bench", name) })
		snapshot, ok := reducer.Snapshot("bench", name)
		if !ok {
			t.Fatalf("snapshot %s missing", name)
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		marshalMs := medianDuration(warmup+samples, func() { _, _ = json.Marshal(snapshot) })
		gz, err := gzipPayload(raw)
		if err != nil {
			t.Fatalf("gzip %s: %v", name, err)
		}
		gzipMs := medianDuration(warmup+samples, func() { _, _ = gzipPayload(raw) })

		points[name] = point{
			Turns:    len(snapshot.Turns),
			CloneMs:  cloneMs,
			Marshal:  marshalMs,
			GzipMs:   gzipMs,
			RawBytes: len(raw),
			GzBytes:  len(gz),
		}
		// 结构断言：fixtures 保持各自类别规模（防静默漂移）。
		if name == "claude-tool-dense" && len(snapshot.Turns) < 30 {
			t.Fatalf("tool-dense lost class: turns=%d", len(snapshot.Turns))
		}
	}

	// 落盘 before 基线（供 iOS 仓 S0B exit artifact 汇总；MAC_BASELINE_OUT 指定目录）。
	if outDir := os.Getenv("MAC_BASELINE_OUT"); outDir != "" {
		payload := map[string]interface{}{
			"schemaVersion": 1,
			"workUnit":      "PERF-S0B",
			"layer":         "mac-projection-serving",
			"warmup":        warmup,
			"samples":       samples,
			"fixtures":      points,
		}
		b, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outDir, "mac-baseline.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("baseline written to %s", outDir)
	}
}

func medianDuration(runs int, fn func()) float64 {
	fn()
	type dummy struct{}
	_ = dummy{}
	ds := make([]float64, 0, runs)
	for i := 0; i < runs; i++ {
		t0 := time.Now()
		fn()
		ds = append(ds, time.Since(t0).Seconds()*1000)
	}
	for i := 1; i < len(ds); i++ {
		for j := i; j > 0 && ds[j] < ds[j-1]; j-- {
			ds[j], ds[j-1] = ds[j-1], ds[j]
		}
	}
	return ds[len(ds)/2]
}
