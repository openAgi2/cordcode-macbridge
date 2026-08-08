package gobridge

// read_file_v2 handler integration test（R1.1 handler + R1.2 filepool wiring）。
// 复用 readFileCaptureConn + newTestHandlers 模式。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/go-bridge/admission"
	"github.com/openAgi2/cordcode-macbridge/go-bridge/filepool"
)

func TestReadFileV2_TextResult(t *testing.T) {
	workspace := t.TempDir()
	allowedPath := filepath.Join(workspace, "main.swift")
	if err := os.WriteFile(allowedPath, []byte("let x = 1\nimport Foundation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", workDir: workspace})

	params, _ := json.Marshal(map[string]interface{}{
		"path": allowedPath,
		"owner": map[string]interface{}{
			"kind": "workspace", "backendId": "codex", "workspaceRoot": workspace,
		},
	})
	conn := newReadFileCaptureConn()
	handlers.handleReadFileV2(conn, WireMessage{RequestID: "r1", BackendID: "codex", Params: params})
	conn.waitForResult(t)

	if conn.err != nil {
		t.Fatalf("read_file_v2 returned error: %+v", conn.err)
	}
	payload, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %#v", conn.data)
	}
	if payload["kind"] != "text" {
		t.Errorf("kind=%v want text", payload["kind"])
	}
	if payload["encoding"] != "utf-8" {
		t.Errorf("encoding=%v want utf-8", payload["encoding"])
	}
	// totalLines: "let x = 1\nimport Foundation\n" => 2 logical lines（raw map: uint64）
	tl, _ := payload["totalLines"].(uint64)
	if tl != 2 {
		t.Errorf("totalLines=%d want 2", tl)
	}
	// metadata.owningIdentity.canonicalWorkspaceRoot == server-canonical (realpath-resolved) workspace
	meta, _ := payload["metadata"].(map[string]interface{})
	ident, _ := meta["owningIdentity"].(map[string]interface{})
	if ident["kind"] != "workspace" || ident["backendId"] != "codex" {
		t.Errorf("identity wrong: %+v", ident)
	}
	expectedRoot, _ := filepath.EvalSymlinks(workspace)
	if ident["canonicalWorkspaceRoot"] != expectedRoot {
		t.Errorf("canonicalWorkspaceRoot=%v want %v (server-canonical realpath)", ident["canonicalWorkspaceRoot"], expectedRoot)
	}
	// segments: 1 full segment with the content
	segs, _ := payload["segments"].([]map[string]interface{})
	// NOTE: json.Marshal -> []interface{} after round-trip? conn.data is the RAW WirePayload map (not re-decoded),
	// so segments is []map[string]interface{} as built by segmentsWire.
	if len(segs) != 1 || segs[0]["kind"] != "full" {
		t.Errorf("segments wrong: %+v", segs)
	}
}

func TestReadFileV2_OutsideAuthorizedRoot(t *testing.T) {
	workspace := t.TempDir()
	secretDir := t.TempDir()
	secretPath := filepath.Join(secretDir, "secret")
	if err := os.WriteFile(secretPath, []byte("do-not-leak"), 0o600); err != nil {
		t.Fatal(err)
	}
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", workDir: workspace})

	params, _ := json.Marshal(map[string]interface{}{
		"path": secretPath,
		"owner": map[string]interface{}{
			"kind": "workspace", "backendId": "codex", "workspaceRoot": workspace,
		},
	})
	conn := newReadFileCaptureConn()
	handlers.handleReadFileV2(conn, WireMessage{RequestID: "r1", BackendID: "codex", Params: params})
	conn.waitForResult(t)

	if conn.err == nil || conn.err.Code != "file.outside_authorized_root" {
		t.Fatalf("want file.outside_authorized_root, got data=%#v err=%+v", conn.data, conn.err)
	}
}

func TestReadFileV2_MissingOwnerKind(t *testing.T) {
	handlers := newTestHandlers(t)
	params, _ := json.Marshal(map[string]interface{}{"path": "/x", "owner": map[string]interface{}{"backendId": "codex"}})
	conn := newReadFileCaptureConn()
	handlers.handleReadFileV2(conn, WireMessage{RequestID: "r1", BackendID: "codex", Params: params})
	conn.waitForResult(t)
	if conn.err == nil || conn.err.Code != "invalid_params" {
		t.Fatalf("want invalid_params, got err=%+v", conn.err)
	}
}

// TestReadFileV2_PoolAsyncSuccess 证明 handleReadFileV2 经 file pool 异步执行（R1.2 wiring）：
// handler 提交后立即返回，结果由 pool worker 在 SendResult 写回。newTestHandlers 的 pool 非空。
func TestReadFileV2_PoolAsyncSuccess(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "a.go")
	os.WriteFile(path, []byte("package main\n"), 0o600)
	handlers := newTestHandlers(t)
	if handlers.filePool == nil {
		t.Fatal("newTestHandlers 应注入非空 filePool")
	}
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", workDir: workspace})

	params, _ := json.Marshal(map[string]interface{}{
		"path": path,
		"owner": map[string]interface{}{
			"kind": "workspace", "backendId": "codex", "workspaceRoot": workspace,
		},
	})
	conn := newReadFileCaptureConn()
	handlers.handleReadFileV2(conn, WireMessage{RequestID: "r1", BackendID: "codex", Params: params})
	// handler 应已返回（不阻塞）；结果异步到达。
	conn.waitForResult(t)
	if conn.err != nil {
		t.Fatalf("want success, got err=%+v", conn.err)
	}
	if payload, ok := conn.data.(map[string]interface{}); !ok || payload["kind"] != "text" {
		t.Errorf("want text result, got %#v", conn.data)
	}
}

// TestReadFileV2_DegradedReturnsReadDegraded 证明 pool 进入 degrading 后，handler 把
// Submit 的 ErrFileDegraded 映射为用户可见的 file.read_degraded（R1.2 wiring）。
func TestReadFileV2_DegradedReturnsReadDegraded(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "a.go")
	os.WriteFile(path, []byte("package main\n"), 0o600)
	handlers := newTestHandlers(t)
	handlers.RegisterAgent("codex", &fakeAgent{name: "codex", workDir: workspace})
	handlers.filePool.Close() // 关掉默认 pool，换快速退化配置

	fastPool, err := filepool.New(filepool.Config{
		PoolSize: 2, PerDeviceInFlight: 1, PerDeviceQueued: 2, GlobalQueued: 4,
		ReadTimeout: 5 * time.Second,
		Health: admission.FileReadHealthConfig{
			PoolSize: 2, MinHealthyFileSlots: 1, DegradeAt: 1, StuckAgeMillis: 20,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fastPool.Close)
	handlers.filePool = fastPool

	// 占用一个 worker 并卡住它，触发 watchdog 退化。
	release := make(chan struct{})
	defer close(release) // 保证 worker 总能解锁，避免 Close() 在 stuck worker 上挂死
	started := make(chan struct{})
	if err := fastPool.Submit(filepool.Job{
		DeviceID: "devA",
		Work:     func(ctx context.Context) { close(started); <-release },
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	// 等 watchdog 退化（stuckAge 20ms，tick ~5ms；轮询最多 1s）。
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fastPool.Health().Snapshot().State != admission.HealthHealthy {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s := fastPool.Health().Snapshot().State; s == admission.HealthHealthy {
		t.Fatalf("pool 未退化，state=%v", s)
	}

	params, _ := json.Marshal(map[string]interface{}{
		"path": path,
		"owner": map[string]interface{}{
			"kind": "workspace", "backendId": "codex", "workspaceRoot": workspace,
		},
	})
	conn := newReadFileCaptureConn()
	handlers.handleReadFileV2(conn, WireMessage{RequestID: "r2", BackendID: "codex", Params: params})
	conn.waitForResult(t)
	if conn.err == nil || conn.err.Code != "file.read_degraded" {
		t.Fatalf("want file.read_degraded, got data=%#v err=%+v", conn.data, conn.err)
	}
	// release 由 defer close 解锁，worker 退出后 fastPool.Close()（t.Cleanup）可 reap。
}
