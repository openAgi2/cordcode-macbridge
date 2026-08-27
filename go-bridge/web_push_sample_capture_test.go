package gobridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 设计 delta §3（监工指令 1 号）—— 真实样本采集钩子：默认关闭、脱敏、0600 落盘。

func TestSampleCaptureDisabledByDefault(t *testing.T) {
	if webPushSampleCaptureEnabled.Load() {
		t.Fatal("capture must default to OFF")
	}
	// 关闭态调用是零行为差异的 no-op（不 panic、不落盘）。
	captureWebPushSample("EVT-TURN-1", map[string]interface{}{"backend": "codex"})
}

func TestSampleCaptureWritesRedactedJSONL(t *testing.T) {
	dir := t.TempDir()
	writer := &webPushSampleWriter{
		dir:  dir,
		ch:   make(chan webPushSampleRecord, 8),
		done: make(chan struct{}),
	}
	go writer.run()
	prevEnabled := webPushSampleCaptureEnabled.Swap(true)
	prevWriter := webPushSamples
	webPushSamples = writer
	t.Cleanup(func() {
		webPushSampleCaptureEnabled.Store(prevEnabled)
		webPushSamples = prevWriter
	})

	secret := "x" + strings.Repeat("S", 80) // 80 字符的敏感长串
	captureWebPushSample("EVT-TURN-1", map[string]interface{}{
		"backend": "codex",
		"session": webPushRedactID("session-abcdef1234567890"),
		"rawShape": webPushRedactShape(map[string]interface{}{
			"turnId": "turn-xyz-123456789",
			"done":   true,
			"count":  3.0,
			"nested": map[string]interface{}{"text": secret},
			"list":   []interface{}{secret, secret, secret, secret, secret, secret, secret, secret, secret, secret},
			"deep":   map[string]interface{}{"a": map[string]interface{}{"b": map[string]interface{}{"c": map[string]interface{}{"d": map[string]interface{}{"e": map[string]interface{}{"f": secret}}}}}},
		}, 0),
	})
	drainWebPushSamplesForTest()

	var raw []byte
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for {
		raw, err = os.ReadFile(filepath.Join(dir, "EVT-TURN-1.jsonl"))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sample file missing: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasSuffix(line, "}") {
		t.Fatalf("malformed jsonl: %q", line)
	}
	var record map[string]interface{}
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := record["capturedAt"]; !ok {
		t.Fatal("capturedAt missing")
	}
	if record["backend"] != "codex" {
		t.Fatalf("backend = %v", record["backend"])
	}
	if strings.Contains(line, secret) {
		t.Fatal("raw secret leaked into sample")
	}
	if !strings.Contains(line, "depth-cap") {
		t.Fatal("deep nesting must hit depth cap")
	}
	if !strings.Contains(line, "more-elems") {
		t.Fatal("long slices must be capped with marker")
	}

	info, err := os.Stat(filepath.Join(dir, "EVT-TURN-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("sample file perm = %v want 0600", info.Mode().Perm())
	}
}

func TestWebPushRedactID(t *testing.T) {
	if webPushRedactID("") != "" {
		t.Fatal("empty stays empty")
	}
	got := webPushRedactID("abcdefghijklmnop")
	if got != "abcdefgh:16" {
		t.Fatalf("redact = %q", got)
	}
	if got := webPushRedactID("短"); got != "短:1" {
		t.Fatalf("short redact = %q", got)
	}
}

func TestCaptureDoesNotBlockWhenBufferFull(t *testing.T) {
	writer := &webPushSampleWriter{
		dir:  t.TempDir(),
		ch:   make(chan webPushSampleRecord, 1), // 容量 1，无人消费
		done: make(chan struct{}),
	}
	prevEnabled := webPushSampleCaptureEnabled.Swap(true)
	prevWriter := webPushSamples
	webPushSamples = writer
	t.Cleanup(func() {
		webPushSampleCaptureEnabled.Store(prevEnabled)
		webPushSamples = prevWriter
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			captureWebPushSample("WP-RESP", map[string]interface{}{"i": float64(i)})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("capture must be non-blocking when buffer is full")
	}
}
