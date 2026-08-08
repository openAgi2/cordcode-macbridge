package gobridge

// read_file_v2 handler integration test（R1.1）。复用 readFileCaptureConn + newTestHandlers 模式。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
	conn := &readFileCaptureConn{}
	handlers.handleReadFileV2(conn, WireMessage{RequestID: "r1", BackendID: "codex", Params: params})

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
	conn := &readFileCaptureConn{}
	handlers.handleReadFileV2(conn, WireMessage{RequestID: "r1", BackendID: "codex", Params: params})

	if conn.err == nil || conn.err.Code != "file.outside_authorized_root" {
		t.Fatalf("want file.outside_authorized_root, got data=%#v err=%+v", conn.data, conn.err)
	}
}

func TestReadFileV2_MissingOwnerKind(t *testing.T) {
	handlers := newTestHandlers(t)
	params, _ := json.Marshal(map[string]interface{}{"path": "/x", "owner": map[string]interface{}{"backendId": "codex"}})
	conn := &readFileCaptureConn{}
	handlers.handleReadFileV2(conn, WireMessage{RequestID: "r1", BackendID: "codex", Params: params})
	if conn.err == nil || conn.err.Code != "invalid_params" {
		t.Fatalf("want invalid_params, got err=%+v", conn.err)
	}
}
