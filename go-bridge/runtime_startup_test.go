package gobridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadyFrameRoundTrip(t *testing.T) {
	drivers := []string{"claude", "codex", "opencode"}
	frame := RuntimeReadyFrame{
		Type:        "runtime_ready",
		Port:        8777,
		BridgeEpoch: "12345-12345",
		Drivers:     drivers,
	}

	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal ready frame: %v", err)
	}

	parsed, err := ParseReadyFrame(data)
	if err != nil {
		t.Fatalf("parse ready frame: %v", err)
	}

	if parsed.Type != "runtime_ready" {
		t.Errorf("type = %q, want %q", parsed.Type, "runtime_ready")
	}
	if parsed.Port != 8777 {
		t.Errorf("port = %d, want %d", parsed.Port, 8777)
	}
	if parsed.BridgeEpoch != "12345-12345" {
		t.Errorf("bridgeEpoch = %q, want %q", parsed.BridgeEpoch, "12345-12345")
	}
	if len(parsed.Drivers) != 3 {
		t.Fatalf("drivers len = %d, want 3", len(parsed.Drivers))
	}
	for i, d := range drivers {
		if parsed.Drivers[i] != d {
			t.Errorf("drivers[%d] = %q, want %q", i, parsed.Drivers[i], d)
		}
	}
}

func TestReadyFrameJSONKeys(t *testing.T) {
	// 确认 camelCase JSON tag
	frame := RuntimeReadyFrame{
		Type:        "runtime_ready",
		Port:        9999,
		BridgeEpoch: "ep",
		Drivers:     []string{"x"},
	}
	data, _ := json.Marshal(frame)
	s := string(data)

	// camelCase 校验
	if !strings.Contains(s, `"type"`) {
		t.Error("missing type key")
	}
	if !strings.Contains(s, `"bridgeEpoch"`) {
		t.Error("missing bridgeEpoch key (camelCase)")
	}
	if strings.Contains(s, `"BridgeEpoch"`) {
		t.Error("found PascalCase BridgeEpoch — should be camelCase")
	}
}

func TestErrorFrameRoundTrip(t *testing.T) {
	frame := RuntimeErrorFrame{
		Type:    "runtime_error",
		Code:    RuntimeErrorPortBindFailed,
		Message: "bind: address already in use",
	}

	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal error frame: %v", err)
	}

	parsed, err := ParseErrorFrame(data)
	if err != nil {
		t.Fatalf("parse error frame: %v", err)
	}

	if parsed.Type != "runtime_error" {
		t.Errorf("type = %q, want %q", parsed.Type, "runtime_error")
	}
	if parsed.Code != RuntimeErrorPortBindFailed {
		t.Errorf("code = %q, want %q", parsed.Code, RuntimeErrorPortBindFailed)
	}
	if parsed.Message != "bind: address already in use" {
		t.Errorf("message = %q, want %q", parsed.Message, "bind: address already in use")
	}
}

func TestParseReadyFrameWrongType(t *testing.T) {
	data := []byte(`{"type":"runtime_error","code":"x","message":"y"}`)
	_, err := ParseReadyFrame(data)
	if err == nil {
		t.Error("expected error for wrong type, got nil")
	}
}

func TestParseErrorFrameWrongType(t *testing.T) {
	data := []byte(`{"type":"runtime_ready","port":1}`)
	_, err := ParseErrorFrame(data)
	if err == nil {
		t.Error("expected error for wrong type, got nil")
	}
}

func TestErrorCodeConstants(t *testing.T) {
	tests := []struct {
		constant string
		want     string
	}{
		{RuntimeErrorPortBindFailed, "runtime_error.port_bind_failed"},
		{RuntimeErrorNoAgents, "runtime_error.no_agents"},
		{RuntimeErrorConfigInvalid, "runtime_error.config_invalid"},
		{RuntimeErrorManagementBindFailed, "runtime.management_bind_failed"},
		{RuntimeErrorManagementURLMissing, "runtime.management_url_missing"},
		{RuntimeErrorBootstrapPersistFailed, "runtime_error.bootstrap_persist_failed"},
	}
	for _, tt := range tests {
		if tt.constant != tt.want {
			t.Errorf("constant = %q, want %q", tt.constant, tt.want)
		}
	}
}

func TestReadyFrameContainsCorrectFields(t *testing.T) {
	drivers := []string{"codex"}
	frame := RuntimeReadyFrame{
		Type:        "runtime_ready",
		Port:        8080,
		BridgeEpoch: "abc",
		Drivers:     drivers,
	}
	data, _ := json.Marshal(frame)

	// 验证所有字段都出现在 JSON 中
	s := string(data)
	if !strings.Contains(s, `"port":8080`) {
		t.Errorf("JSON missing port: %s", s)
	}
	if !strings.Contains(s, `"drivers":["codex"]`) {
		t.Errorf("JSON missing drivers: %s", s)
	}
	if !strings.Contains(s, `"bridgeEpoch":"abc"`) {
		t.Errorf("JSON missing bridgeEpoch: %s", s)
	}
}

// TestWriteReadyFrame_ReturnsErrorOnUnwritableDataDir verifies the T06 contract:
// WriteReadyFrame returns a non-nil error when runtime.json cannot be written
// (read-only dataDir). Pre-fix it only slog.Error'd and returned silently,
// letting the bridge publish ready against an unwritten runtime.json.
func TestWriteReadyFrame_ReturnsErrorOnUnwritableDataDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses file permissions; cannot simulate read-only dir")
	}
	dataDir := t.TempDir()
	// Make the directory read-only (0500): AtomicWriteFile's temp-rename needs
	// write permission, which will fail.
	if err := os.Chmod(dataDir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o755) })

	err := WriteReadyFrame(8777, []string{"claude"}, "http://127.0.0.1:9999", dataDir, "test-epoch")
	if err == nil {
		t.Fatal("WriteReadyFrame returned nil error for unwritable dataDir; expected fail-fast error")
	}

	// runtime.json must NOT exist — the atomic write failed, no partial/stale
	// ready file is left for the Mac App to misread.
	if _, statErr := os.Stat(filepath.Join(dataDir, "runtime.json")); statErr == nil {
		t.Fatal("runtime.json exists after failed write — partial/stale ready frame must not persist")
	}
}

// TestWriteReadyFrame_SuccessWritesRuntimeJSONWithEpoch verifies the happy path
// writes runtime.json with bridgeEpoch present (T06: epoch in the file for Swift
// cross-validation).
func TestWriteReadyFrame_SuccessWritesRuntimeJSONWithEpoch(t *testing.T) {
	dataDir := t.TempDir()
	if err := WriteReadyFrame(8777, []string{"claude", "codex"}, "http://127.0.0.1:9999", dataDir, "test-epoch"); err != nil {
		t.Fatalf("WriteReadyFrame error on writable dataDir: %v", err)
	}

	runtimePath := filepath.Join(dataDir, "runtime.json")
	data, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatalf("read runtime.json: %v", err)
	}
	var frame RuntimeReadyFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("unmarshal runtime.json: %v", err)
	}
	if frame.Type != "runtime_ready" {
		t.Errorf("type = %q, want runtime_ready", frame.Type)
	}
	if frame.BridgeEpoch != "test-epoch" {
		t.Errorf("BridgeEpoch = %q, want injected process epoch", frame.BridgeEpoch)
	}
	if frame.Port != 8777 {
		t.Errorf("port = %d, want 8777", frame.Port)
	}
	if frame.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", frame.PID, os.Getpid())
	}
}

// TestWriteReadyFrame_EmptyDataDirNoError verifies empty dataDirPath (dev mode)
// does not error — only product mode persists.
func TestWriteReadyFrame_EmptyDataDirNoError(t *testing.T) {
	if err := WriteReadyFrame(8777, []string{"claude"}, "", "", "test-epoch"); err != nil {
		t.Fatalf("WriteReadyFrame with empty dataDir returned error: %v", err)
	}
}

// TestRewriteRuntimeJSON_RestoresDeletedRuntimeJSON covers the 2026-08-24 wedge:
// Mac App 每次 launchBridgeProcess 先删除 runtime.json；若 launch 因 controller 持有
// 健康进程抛 alreadyRunning，删除后无人补写 → App 管理轮询卡死。周期重写必须把被删的
// runtime.json 原样恢复（同内容，含 pid/epoch/managementUrl）。
func TestRewriteRuntimeJSON_RestoresDeletedRuntimeJSON(t *testing.T) {
	dataDir := t.TempDir()
	if err := WriteReadyFrame(8777, []string{"claude"}, "http://127.0.0.1:9999", dataDir, "rewrite-epoch"); err != nil {
		t.Fatalf("WriteReadyFrame error: %v", err)
	}
	original, err := os.ReadFile(filepath.Join(dataDir, "runtime.json"))
	if err != nil {
		t.Fatalf("read runtime.json: %v", err)
	}

	// 模拟 App launch 前的删除
	if err := os.Remove(filepath.Join(dataDir, "runtime.json")); err != nil {
		t.Fatalf("remove runtime.json: %v", err)
	}

	if err := RewriteRuntimeJSON(); err != nil {
		t.Fatalf("RewriteRuntimeJSON error: %v", err)
	}

	restored, err := os.ReadFile(filepath.Join(dataDir, "runtime.json"))
	if err != nil {
		t.Fatalf("runtime.json not restored by RewriteRuntimeJSON: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("rewritten runtime.json mismatch:\n original=%s\n restored=%s", original, restored)
	}
}

// TestRewriteRuntimeJSON_EmptyDataDirNoOp 验证 dev 模式（无 data-dir）下重写是 no-op，
// 不产生文件也不报错。
func TestRewriteRuntimeJSON_EmptyDataDirNoOp(t *testing.T) {
	if err := WriteReadyFrame(8777, []string{"claude"}, "", "", "noop-epoch"); err != nil {
		t.Fatalf("WriteReadyFrame error: %v", err)
	}
	if err := RewriteRuntimeJSON(); err != nil {
		t.Fatalf("RewriteRuntimeJSON with empty dataDir must not error: %v", err)
	}
}
