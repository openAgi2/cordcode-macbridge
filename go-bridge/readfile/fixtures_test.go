package readfile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func observedReadFileV2Fixtures(t *testing.T) map[string][]byte {
	t.Helper()
	workspace := OwningIdentity{Kind: "workspace", BackendID: "codex", CanonicalWorkspaceRoot: "/workspace"}
	session := OwningIdentity{Kind: "session", BackendID: "codex", SessionID: "sess-0001-test", CanonicalDirectory: "/workspace/src"}
	encode := func(result ReadFileV2Result) []byte {
		raw, err := json.Marshal(result.WirePayload())
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	text := []byte("import Foundation\nprint(\"hi\")\n")
	truncated := []byte("one\ntwo\nthree\nfour\nfive")
	unknownUnsupported := []byte{0x80, 0x81, 0x82, 0x83, 0x84}
	return map[string][]byte{
		"read-file-v2-text.json":                encode(BuildReadFileV2Result(text, "/workspace/src/main.swift", "swift", workspace, NoLineTruncation, 0)),
		"read-file-v2-text-empty.json":          encode(BuildReadFileV2Result(nil, "/workspace/src/empty.swift", "swift", workspace, NoLineTruncation, 0)),
		"read-file-v2-text-truncated.json":      encode(BuildReadFileV2Result(truncated, "/workspace/src/main.swift", "swift", workspace, 3, 1)),
		"read-file-v2-text-session.json":        encode(BuildReadFileV2Result(text, "/workspace/src/main.swift", "swift", session, NoLineTruncation, 0)),
		"read-file-v2-unsupported-utf16.json":   encode(BuildReadFileV2Result([]byte{0xff, 0xfe, 0x41, 0x00}, "/workspace/src/main.swift", "swift", workspace, NoLineTruncation, 0)),
		"read-file-v2-unsupported-unknown.json": encode(BuildReadFileV2Result(unknownUnsupported, "/workspace/src/main.swift", "swift", workspace, NoLineTruncation, 0)),
		"read-file-v2-binary.json":              encode(BuildReadFileV2Result(bytes.Repeat([]byte{0}, 100), "/workspace/src/blob.bin", "bin", workspace, NoLineTruncation, 0)),
		"read-file-v2-text-empty-ext.json":      encode(BuildReadFileV2Result([]byte("FROM scratch\n"), "/workspace/Dockerfile", "", workspace, NoLineTruncation, 0)),
	}
}

func readFileV2FixturePath(name string) string {
	return filepath.Join("..", "..", "docs", "protocol", "samples", "read-file-v2", name)
}

func TestObservedReadFileV2FixturesRoundTrip(t *testing.T) {
	for name, produced := range observedReadFileV2Fixtures(t) {
		committed, err := os.ReadFile(readFileV2FixturePath(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Equal(produced, committed) {
			t.Errorf("%s is not exact BuildReadFileV2Result/WirePayload producer bytes", name)
		}
		if _, err := DecodeReadFileV2Result(produced); err != nil {
			t.Errorf("%s strict consumer rejected producer bytes: %v", name, err)
		}
	}
}

func TestGenerateObservedReadFileV2Fixtures(t *testing.T) {
	if os.Getenv("CCCODEGEN_FIXTURES") != "1" {
		t.Skip("set CCCODEGEN_FIXTURES=1 to write fixtures")
	}
	for name, produced := range observedReadFileV2Fixtures(t) {
		if err := os.WriteFile(readFileV2FixturePath(name), produced, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}
