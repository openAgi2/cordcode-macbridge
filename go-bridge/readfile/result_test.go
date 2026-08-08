package readfile

import (
	"bytes"
	"encoding/json"
	"testing"
)

func ident() OwningIdentity {
	return OwningIdentity{Kind: "workspace", BackendID: "codex", CanonicalWorkspaceRoot: "/ws"}
}

func TestBuildResult_TextFull(t *testing.T) {
	r := BuildReadFileV2Result([]byte("a\nb\nc"), "/x.swift", "swift", ident(), DefaultMaxLines, DefaultTailLines)
	if r.Kind != "text" || r.Encoding != "utf-8" {
		t.Fatalf("kind=%s enc=%s want text/utf-8", r.Kind, r.Encoding)
	}
	if r.TotalLines != 3 {
		t.Errorf("totalLines=%d want 3", r.TotalLines)
	}
	if len(r.Segments) != 1 || r.Segments[0].Kind != "full" || r.Segments[0].SourceLineCount != 3 {
		t.Errorf("segments wrong: %+v", r.Segments)
	}
	if r.Segments[0].Content != "a\nb\nc" {
		t.Errorf("full content=%q", r.Segments[0].Content)
	}
}

func TestBuildResult_EmptyUniqueFullSegment(t *testing.T) {
	r := BuildReadFileV2Result([]byte{}, "/x.swift", "swift", ident(), DefaultMaxLines, DefaultTailLines)
	if r.TotalLines != 0 {
		t.Errorf("empty totalLines=%d want 0", r.TotalLines)
	}
	if len(r.Segments) != 1 || r.Segments[0].Kind != "full" {
		t.Fatalf("empty must be exactly one full segment: %+v", r.Segments)
	}
	if r.Segments[0].Content != "" || r.Segments[0].SourceLineStart != 1 || r.Segments[0].SourceLineCount != 0 {
		t.Errorf("empty full segment wrong: %+v", r.Segments[0])
	}
}

func TestBuildResult_TrailingNewline(t *testing.T) {
	// "a\nb\n": LF=2, last byte \n => totalLines=2（尾换行不产生额外空行）
	r := BuildReadFileV2Result([]byte("a\nb\n"), "/x.swift", "swift", ident(), DefaultMaxLines, DefaultTailLines)
	if r.TotalLines != 2 {
		t.Errorf("totalLines=%d want 2", r.TotalLines)
	}
}

func TestBuildResult_CRLFCountsAsOneLine(t *testing.T) {
	// "a\r\nb\r\n": 两个 \n，末字节 \n => totalLines=2（\r 行内）
	r := BuildReadFileV2Result([]byte("a\r\nb\r\n"), "/x.swift", "swift", ident(), DefaultMaxLines, DefaultTailLines)
	if r.TotalLines != 2 {
		t.Errorf("CRLF totalLines=%d want 2", r.TotalLines)
	}
}

func TestBuildResult_TruncatedHeadOmissionTailLineSum(t *testing.T) {
	// 5 行（无尾换行），maxLines=3, tailLines=1 => head(2)+omission(2)+tail(1)
	src := []byte("l0\nl1\nl2\nl3\nl4")
	r := BuildReadFileV2Result(src, "/x.swift", "swift", ident(), 3, 1)
	if r.TotalLines != 5 {
		t.Fatalf("totalLines=%d want 5", r.TotalLines)
	}
	if len(r.Segments) != 3 {
		t.Fatalf("want 3 segments (head/omission/tail), got %d", len(r.Segments))
	}
	head, omit, tail := r.Segments[0], r.Segments[1], r.Segments[2]
	// 行求和不变式：headCount + omittedCount + tailCount == totalLines
	sum := head.SourceLineCount + omit.SourceLineCount + tail.SourceLineCount
	if sum != r.TotalLines {
		t.Errorf("segment line sum=%d != totalLines=%d", sum, r.TotalLines)
	}
	if head.Kind != "head" || head.Content != "l0\nl1" || head.SourceLineStart != 1 || head.SourceLineCount != 2 {
		t.Errorf("head wrong: %+v", head)
	}
	if omit.Kind != "omission" || omit.Content != "" || omit.SourceLineStart != 3 || omit.SourceLineCount != 2 {
		t.Errorf("omission wrong: %+v", omit)
	}
	if tail.Kind != "tail" || tail.Content != "l4" || tail.SourceLineStart != 5 || tail.SourceLineCount != 1 {
		t.Errorf("tail wrong: %+v", tail)
	}
}

func TestBuildResult_UnsupportedEncoding(t *testing.T) {
	// UTF-16LE BOM
	r := BuildReadFileV2Result([]byte{0xFF, 0xFE, 0x41, 0x00}, "/x.txt", "txt", ident(), DefaultMaxLines, DefaultTailLines)
	if r.Kind != "unsupported_encoding" || r.Detected != "utf-16le" {
		t.Fatalf("kind=%s detected=%s want unsupported_encoding/utf-16le", r.Kind, r.Detected)
	}
	if r.Encoding != "" || r.TotalLines != 0 || len(r.Segments) != 0 {
		t.Errorf("unsupported must not carry encoding/totalLines/segments: %+v", r)
	}
}

func TestBuildResult_Binary(t *testing.T) {
	// high NUL
	r := BuildReadFileV2Result(bytes.Repeat([]byte{0x00}, 100), "/x.bin", "bin", ident(), DefaultMaxLines, DefaultTailLines)
	if r.Kind != "binary" {
		t.Fatalf("kind=%s want binary", r.Kind)
	}
	if r.Encoding != "" || len(r.Segments) != 0 {
		t.Errorf("binary must not carry encoding/segments: %+v", r)
	}
}

func TestBuildResult_RevisionCoversRawWithBOM(t *testing.T) {
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte("plain")...)
	r := BuildReadFileV2Result(withBOM, "/x.swift", "swift", ident(), DefaultMaxLines, DefaultTailLines)
	// contentRevision 覆盖含 BOM 的 raw bytes（不是 BOM-stripped display）
	if r.Metadata.ContentRevision != ContentRevision(withBOM) {
		t.Error("contentRevision must cover raw bytes incl BOM")
	}
	if r.Metadata.SizeBytes != uint64(len(withBOM)) {
		t.Error("sizeBytes must cover raw bytes incl BOM")
	}
	// 但 segment content 是 BOM-stripped display
	if r.Segments[0].Content != "plain" {
		t.Errorf("segment content=%q want 'plain' (BOM stripped)", r.Segments[0].Content)
	}
}

func TestBuildResult_MetadataFields(t *testing.T) {
	r := BuildReadFileV2Result([]byte("x"), "/ws/a.swift", "swift", ident(), DefaultMaxLines, DefaultTailLines)
	if r.Metadata.Path != "/ws/a.swift" || r.Metadata.Extension != "swift" {
		t.Errorf("metadata path/ext wrong: %+v", r.Metadata)
	}
	if r.Metadata.SizeBytes != 1 {
		t.Errorf("sizeBytes=%d want 1", r.Metadata.SizeBytes)
	}
	if r.Metadata.ContentRevision != ContentRevision([]byte("x")) {
		t.Error("contentRevision mismatch")
	}
	if r.Metadata.OwningIdentity.Kind != "workspace" {
		t.Error("identity not carried")
	}
}

// round-trip: BuildReadFileV2Result -> WirePayload -> JSON -> DecodeReadFileV2Result
// 证明 handler 的 wire 输出能被 iOS 侧 strict codec 消费（producer/consumer 同 success bytes）。
func TestWirePayload_RoundTripsCodec(t *testing.T) {
	cases := [][]byte{
		[]byte("let x = 1\nimport Foundation\n"),
		[]byte{},
		[]byte{0xEF, 0xBB, 0xBF, 'p', 'l', 'a', 'i', 'n'}, // UTF-8 BOM text
		[]byte{0xFF, 0xFE, 0x41, 0x00},                     // UTF-16LE -> unsupported
		bytes.Repeat([]byte{0x00}, 100),                    // binary
	}
	for i, data := range cases {
		r := BuildReadFileV2Result(data, "/ws/a.swift", "swift", ident(), DefaultMaxLines, DefaultTailLines)
		payload := r.WirePayload()
		js, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("case %d: marshal WirePayload: %v", i, err)
		}
		// strict decode 必须成功（wire shape 合法）
		decoded, err := DecodeReadFileV2Result(js)
		if err != nil {
			t.Errorf("case %d (kind=%s): strict decode failed: %v\nwire=%s", i, r.Kind, err, js)
			continue
		}
		if decoded.Kind != r.Kind {
			t.Errorf("case %d: kind round-trip %s != %s", i, decoded.Kind, r.Kind)
		}
	}
}
