package readfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join("..", "..", "docs", "protocol", "samples", "read-file-v2")
	if _, err := os.Stat(d); err != nil {
		t.Fatalf("fixture dir missing: %s (%v)", d, err)
	}
	return d
}

func readFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return json.RawMessage(b)
}

// 所有 committed v2 result/request fixtures 必须被 Go strict codec 成功解码（success bytes）。
func TestFixtures_DecodeSuccess(t *testing.T) {
	results := []string{
		"read-file-v2-text.json", "read-file-v2-text-empty.json", "read-file-v2-text-truncated.json",
		"read-file-v2-text-session.json", "read-file-v2-unsupported-utf16.json",
		"read-file-v2-unsupported-unknown.json", "read-file-v2-binary.json", "read-file-v2-text-empty-ext.json",
	}
	for _, f := range results {
		if _, err := DecodeReadFileV2Result(readFixture(t, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	// legacy 6-field
	if _, err := DecodeLegacyReadFileResult(readFixture(t, "legacy-read-file-result.json")); err != nil {
		t.Errorf("legacy: %v", err)
	}
	// requests
	if _, _, err := DecodeReadFileV2Request(readFixture(t, "read-file-v2-request-workspace.json")); err != nil {
		t.Errorf("req-workspace: %v", err)
	}
	if _, _, err := DecodeReadFileV2Request(readFixture(t, "read-file-v2-request-session.json")); err != nil {
		t.Errorf("req-session: %v", err)
	}
	// chunks
	if _, err := DecodeRelayChunk(readFixture(t, "relay-chunk-base.json")); err != nil {
		t.Errorf("chunk-base: %v", err)
	}
	if has, err := DecodeRelayChunk(readFixture(t, "relay-chunk-correlated.json")); err != nil || !has {
		t.Errorf("chunk-correlated: has=%v err=%v", has, err)
	}
}

// empty 文件：唯一空 full segment，totalLines=0，segments 不可为空数组。
func TestDecode_EmptyFileFullSegment(t *testing.T) {
	res, err := DecodeReadFileV2Result(readFixture(t, "read-file-v2-text-empty.json"))
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if res.TotalLines != 0 || len(res.Segments) != 1 || res.Segments[0].Kind != "full" {
		t.Errorf("empty must be exactly one full segment; got %+v", res)
	}
}

// truncated：head+omission+tail line 求和 == totalLines。
func TestDecode_TruncatedLineSum(t *testing.T) {
	res, err := DecodeReadFileV2Result(readFixture(t, "read-file-v2-text-truncated.json"))
	if err != nil {
		t.Fatalf("truncated: %v", err)
	}
	if len(res.Segments) != 3 {
		t.Errorf("expected 3 segments, got %d", len(res.Segments))
	}
	// 求和已在 decodeSegments 校验；这里再确认 head/omission/tail 序列
	kinds := []string{res.Segments[0].Kind, res.Segments[1].Kind, res.Segments[2].Kind}
	want := []string{"head", "omission", "tail"}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("segment[%d]=%s want %s", i, kinds[i], want[i])
		}
	}
}

// negative corpus：对合法 v2 text 注入各类 strict 违规，必须被拒绝。
func TestNegativeCorpus(t *testing.T) {
	base := `{"kind":"text","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":1,"contentRevision":"sha256:` + "0000000000000000000000000000000000000000000000000000000000000000" + `","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}},"encoding":"utf-8","totalLines":1,"segments":[{"kind":"full","content":"a","sourceLineStart":1,"sourceLineCount":1}]}`
	// base must decode OK
	if _, err := DecodeReadFileV2Result(json.RawMessage(base)); err != nil {
		t.Fatalf("base must decode OK: %v", err)
	}
	cases := map[string]string{
		"unknown top":        `{"kind":"text","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":1,"contentRevision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}},"encoding":"utf-8","totalLines":1,"segments":[{"kind":"full","content":"a","sourceLineStart":1,"sourceLineCount":1}],"extra":1}`,
		"missing metadata":   `{"kind":"text","encoding":"utf-8","totalLines":1,"segments":[]}`,
		"bad revision":       `{"kind":"text","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":1,"contentRevision":"not-a-sha","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}},"encoding":"utf-8","totalLines":1,"segments":[{"kind":"full","content":"a","sourceLineStart":1,"sourceLineCount":1}]}`,
		"non-utf8 text":      `{"kind":"text","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":1,"contentRevision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}},"encoding":"utf-16","totalLines":1,"segments":[{"kind":"full","content":"a","sourceLineStart":1,"sourceLineCount":1}]}`,
		"empty segments":     `{"kind":"text","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":1,"contentRevision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}},"encoding":"utf-8","totalLines":0,"segments":[]}`,
		"line sum mismatch":  `{"kind":"text","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":1,"contentRevision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}},"encoding":"utf-8","totalLines":2,"segments":[{"kind":"full","content":"a","sourceLineStart":1,"sourceLineCount":1}]}`,
		"quoted number":      `{"kind":"text","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":"1","contentRevision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}},"encoding":"utf-8","totalLines":1,"segments":[{"kind":"full","content":"a","sourceLineStart":1,"sourceLineCount":1}]}`,
		"sizeBytes over cap": `{"kind":"text","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":9999999999,"contentRevision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}},"encoding":"utf-8","totalLines":1,"segments":[{"kind":"full","content":"a","sourceLineStart":1,"sourceLineCount":1}]}`,
		"unknown kind":       `{"kind":"bogus","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":1,"contentRevision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}}}`,
		"owner unknown kind": `{"kind":"text","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":1,"contentRevision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","owningIdentity":{"kind":"bogus","backendId":"codex"}},"encoding":"utf-8","totalLines":1,"segments":[{"kind":"full","content":"a","sourceLineStart":1,"sourceLineCount":1}]}`,
		"null field":         `{"kind":"text","metadata":{"path":null,"extension":"swift","sizeBytes":1,"contentRevision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}},"encoding":"utf-8","totalLines":1,"segments":[{"kind":"full","content":"a","sourceLineStart":1,"sourceLineCount":1}]}`,
		"detected null":      `{"kind":"unsupported_encoding","metadata":{"path":"/x.swift","extension":"swift","sizeBytes":1,"contentRevision":"sha256:0000000000000000000000000000000000000000000000000000000000000000","owningIdentity":{"kind":"workspace","backendId":"codex","canonicalWorkspaceRoot":"/x"}},"detected":null}`,
		"bad correlation":    `{"groupId":"g","index":0,"count":1,"bulkCorrelationId":"ZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ"}`,
	}
	for name, body := range cases {
		// 路由到对应 decoder
		var err error
		if b := []byte(body); contains(b, `"bulkCorrelationId"`) {
			_, err = DecodeRelayChunk(json.RawMessage(body))
		} else {
			_, err = DecodeReadFileV2Result(json.RawMessage(body))
		}
		if err == nil {
			t.Errorf("case %q accepted but must be rejected", name)
		}
	}
}

// detected field OMIT（unknown encoding）合法；detected null 非法（已在 corpus）。
func TestDecode_DetectedOmit(t *testing.T) {
	res, err := DecodeReadFileV2Result(readFixture(t, "read-file-v2-unsupported-unknown.json"))
	if err != nil {
		t.Fatalf("detected-omit: %v", err)
	}
	if res.Detected != "" {
		t.Errorf("detected should be empty when omitted; got %q", res.Detected)
	}
}

func contains(b []byte, s string) bool {
	return stringContains(string(b), s)
}
func stringContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
