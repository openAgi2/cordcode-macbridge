package readfile

import (
	"bytes"
	"testing"
)

func TestClassifyEncoding_DecisionTable(t *testing.T) {
	utf8BOM := []byte{0xEF, 0xBB, 0xBF}
	cases := []struct {
		name     string
		data     []byte
		wantKind string
		wantDet  string // detected; "" for text/binary/unknown-unsupported
	}{
		{"empty -> text", []byte{}, "text", ""},
		{"plain ascii -> text", []byte("hello world\n"), "text", ""},
		{"UTF-8 BOM + valid ascii -> text", append(utf8BOM, []byte("plain")...), "text", ""},
		{"UTF-8 BOM stripped in display", append(utf8BOM, []byte("x")...), "text", ""},
		{"UTF-32BE BOM -> unsupported utf-32be", []byte{0x00, 0x00, 0xFE, 0xFF, 0x41}, "unsupported_encoding", "utf-32be"},
		{"UTF-32LE BOM (FF FE 00 00) -> unsupported utf-32le", []byte{0xFF, 0xFE, 0x00, 0x00, 0x41}, "unsupported_encoding", "utf-32le"},
		{"UTF-16LE BOM (FF FE xx) -> unsupported utf-16le (not utf-32)", []byte{0xFF, 0xFE, 0x41, 0x00}, "unsupported_encoding", "utf-16le"},
		{"UTF-16BE BOM -> unsupported utf-16be", []byte{0xFE, 0xFF, 0x00, 0x41}, "unsupported_encoding", "utf-16be"},
		{"valid + NUL 1% boundary -> binary", makeBinNUL(100, 1), "binary", ""},
		{"valid + NUL just under 1% -> text", makeBinNUL(101, 1), "text", ""}, // 1/101 < 1%
		{"valid + control 10% boundary -> binary", makeBinCtrl(100, 10), "binary", ""},
		{"valid + control just under 10% -> text", makeBinCtrl(100, 9), "text", ""},
		{"invalid UTF-8 + heuristic false -> unsupported(nil detected)", []byte{0xE9}, "unsupported_encoding", ""},
		{"invalid + high NUL -> binary", append([]byte{0xE9}, bytes.Repeat([]byte{0x00}, 5)...), "binary", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyEncoding(tc.data)
			if got.Kind != tc.wantKind {
				t.Errorf("kind=%q want %q (detected=%q)", got.Kind, tc.wantKind, got.Detected)
			}
			if got.Detected != tc.wantDet {
				t.Errorf("detected=%q want %q", got.Detected, tc.wantDet)
			}
		})
	}
}

// makeBinNUL: n bytes, nul of them are 0x00, rest 'a'. -> nul/n ratio.
func makeBinNUL(n, nul int) []byte {
	b := bytes.Repeat([]byte{'a'}, n)
	for i := 0; i < nul && i < n; i++ {
		b[i] = 0x00
	}
	return b
}

// makeBinCtrl: n bytes, ctrl of them are 0x01 (C0 control, excl TAB/LF/CR/FF), rest 'a'.
func makeBinCtrl(n, ctrl int) []byte {
	b := bytes.Repeat([]byte{'a'}, n)
	for i := 0; i < ctrl && i < n; i++ {
		b[i] = 0x01
	}
	return b
}

func TestClassifyEncoding_UTF8BOMDisplayStripped(t *testing.T) {
	got := ClassifyEncoding(append([]byte{0xEF, 0xBB, 0xBF}, []byte("plain")...))
	if got.Kind != "text" {
		t.Fatalf("kind=%s want text", got.Kind)
	}
	if !bytes.Equal(got.DisplayBytes, []byte("plain")) {
		t.Errorf("displayBytes=%q want 'plain' (BOM stripped)", string(got.DisplayBytes))
	}
}

func TestContentRevision(t *testing.T) {
	rev := ContentRevision([]byte("hello"))
	if rev != "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("contentRevision mismatch: %s", rev)
	}
	// empty -> sha256 of empty
	if ContentRevision(nil) != "sha256:"+sha256EmptyHex {
		t.Error("empty contentRevision mismatch")
	}
}

const sha256EmptyHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
