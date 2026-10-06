package dshweb

// session_media_dimensions_test.go — height-jump fix 2026-10-06 driver-level
// tests for ProbeSessionMediaDimensions: header parsers for every supported
// format (PNG IHDR / JPEG SOF / GIF logical screen / WebP VP8X / SVG declared
// size mirroring the iOS SessionSVGSupport semantics), the batch fail-soft
// contract (per-path failures absent from the map, session-level failures
// error out), and the official-route-only read (every probe goes through
// /api/file).

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func pngHead(w, h uint32) []byte {
	b := make([]byte, 24)
	copy(b, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	binary.BigEndian.PutUint32(b[8:12], 13) // IHDR length
	copy(b[12:16], "IHDR")
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	return b
}

func jpegHead(w, h uint16) []byte {
	b := []byte{0xFF, 0xD8}
	b = append(b, 0xFF, 0xE0) // APP0
	b = append(b, 0x00, 0x10) // segment length 16
	b = append(b, make([]byte, 14)...)
	b = append(b, 0xFF, 0xC0) // SOF0
	b = append(b, 0x00, 0x11) // segment length 17
	b = append(b, 0x08)       // precision
	b = binary.BigEndian.AppendUint16(b, h)
	b = binary.BigEndian.AppendUint16(b, w)
	b = append(b, 0x01) // component count
	return b
}

func gifHead(w, h uint16) []byte {
	b := []byte("GIF89a")
	b = binary.LittleEndian.AppendUint16(b, w)
	b = binary.LittleEndian.AppendUint16(b, h)
	return append(b, 0xF7, 0x00, 0x00)
}

func webpVP8XHead(w, h int) []byte {
	b := make([]byte, 30)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], 0xFF)
	copy(b[8:12], "WEBP")
	copy(b[12:16], "VP8X")
	b[24] = byte(w - 1)
	b[25] = byte((w - 1) >> 8)
	b[26] = byte((w - 1) >> 16)
	b[27] = byte(h - 1)
	b[28] = byte((h - 1) >> 8)
	b[29] = byte((h - 1) >> 16)
	return b
}

func TestBinaryImageDimensionsParsers(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		w, h int
		ok   bool
	}{
		{"png", pngHead(720, 420), 720, 420, true},
		{"png truncated", pngHead(720, 420)[:20], 0, 0, false},
		{"png bad signature", append([]byte{0x00, 0x01, 0x02, 0x03}, pngHead(1, 1)[4:]...), 0, 0, false},
		{"jpeg with app0", jpegHead(640, 480), 640, 480, true},
		{"jpeg progressive sof2", append([]byte{0xFF, 0xD8, 0xFF, 0xC2, 0x00, 0x11, 0x08}, append(binary.BigEndian.AppendUint16(nil, 100), append(binary.BigEndian.AppendUint16(nil, 200), 0x03)...)...), 200, 100, true},
		{"jpeg truncated", jpegHead(640, 480)[:12], 0, 0, false},
		{"gif", gifHead(100, 50), 100, 50, true},
		{"gif bad magic", append([]byte("GIF88a"), gifHead(1, 1)[6:]...), 0, 0, false},
		{"webp vp8x", webpVP8XHead(800, 600), 800, 600, true},
		{"webp vp8l only", func() []byte {
			b := make([]byte, 20)
			copy(b, "RIFF")
			copy(b[8:12], "WEBP")
			copy(b[12:16], "VP8L")
			return b
		}(), 0, 0, false},
		{"garbage", []byte("not an image at all"), 0, 0, false},
	}
	for _, tc := range cases {
		w, h, ok := binaryImageDimensions(tc.head)
		if ok != tc.ok || (ok && (w != tc.w || h != tc.h)) {
			t.Fatalf("%s: got (%d, %d, %v), want (%d, %d, %v)", tc.name, w, h, ok, tc.w, tc.h, tc.ok)
		}
	}
}

func TestSVGDeclaredSizeVariants(t *testing.T) {
	cases := []struct {
		name   string
		source string
		w, h   int
		ok     bool
	}{
		{"plain attrs", `<svg width="720" height="420"></svg>`, 720, 420, true},
		{"px units", `<svg width="720px" height="420px"></svg>`, 720, 420, true},
		{"padded values", `<svg width=" 720 " height=" 420 "></svg>`, 720, 420, true},
		{"single quotes", `<svg width='720' height='420'></svg>`, 720, 420, true},
		{"percent falls back to viewBox", `<svg width="100%" height="100%" viewBox="0 0 720 420"></svg>`, 720, 420, true},
		{"viewBox commas", `<svg viewBox="0,0,720,420"></svg>`, 720, 420, true},
		{"viewBox padded", `<svg viewBox=" 0  0  720  420 "></svg>`, 720, 420, true},
		{"stroke-width boundary", `<svg stroke-width="2" width="50" height="30"></svg>`, 50, 30, true},
		{"data-width boundary", `<svg data-width="9" width="50" height="30"></svg>`, 50, 30, true},
		{"no dims", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`, 0, 0, false},
		{"viewBox three fields", `<svg viewBox="0 0 720"></svg>`, 0, 0, false},
		{"viewBox non-numeric", `<svg viewBox="0 0 a b"></svg>`, 0, 0, false},
		{"uppercase tag not root", `<SVG width="720" height="420"></SVG>`, 0, 0, false},
		{"xml prolog before root", `<?xml version="1.0"?><svg width="720" height="420"></svg>`, 720, 420, true},
		{"truncated tag", `<svg width="720"`, 0, 0, false},
	}
	for _, tc := range cases {
		w, h, ok := svgDeclaredSize([]byte(tc.source))
		if ok != tc.ok || (ok && (w != tc.w || h != tc.h)) {
			t.Fatalf("%s: got (%d, %d, %v), want (%d, %d, %v)", tc.name, w, h, ok, tc.w, tc.h, tc.ok)
		}
	}
}

func TestProbeSessionMediaDimensionsBatch(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwd := t.TempDir()
	mediaSessionList(f, "sess-d", cwd)
	pngPath := filepath.Join(cwd, "a.png")
	svgPath := filepath.Join(cwd, "b.svg")
	missingPath := filepath.Join(cwd, "missing.png")
	textPath := filepath.Join(cwd, "c.txt")
	f.fileByPath[pngPath] = fileScript{contentType: "image/png", data: pngHead(720, 420)}
	f.fileByPath[svgPath] = fileScript{contentType: "image/svg+xml", data: []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="720" height="420"></svg>`)}
	f.fileByPath[missingPath] = fileScript{status: 404, body: "FS_NOT_FOUND"}
	f.fileByPath[textPath] = fileScript{contentType: "text/plain", data: []byte("hi")}

	dims, err := a.ProbeSessionMediaDimensions(context.Background(), "sess-d", []string{
		"a.png", "b.svg", "missing.png", "c.txt", "../escape.png", "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dims) != 2 {
		t.Fatalf("dims = %v, want exactly a.png + b.svg", dims)
	}
	png, ok := dims["a.png"]
	if !ok || png.MediaType != "image/png" || png.Width != 720 || png.Height != 420 {
		t.Fatalf("a.png dims = %+v", png)
	}
	svg, ok := dims["b.svg"]
	if !ok || svg.MediaType != "image/svg+xml" || svg.Width != 720 || svg.Height != 420 {
		t.Fatalf("b.svg dims = %+v", svg)
	}
	// Per-path failures (missing file, non-image, escape, empty) must be
	// absent, and rejected paths must never reach the provider.
	paths := fileRequests(f)
	if len(paths) != 4 {
		t.Fatalf("api/file requests = %v, want the 4 valid paths", paths)
	}
	for _, p := range paths {
		if p == filepath.Join(cwd, "..", "escape.png") {
			t.Fatalf("escape path reached the provider: %v", paths)
		}
	}
}

func TestProbeSessionMediaDimensionsUnparseableHeadAbsent(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwd := t.TempDir()
	mediaSessionList(f, "sess-d", cwd)
	// Allowed image MIME but garbage bytes: absent from the map (the client
	// sizes at byte arrival — fail-soft hint semantics).
	f.fileByPath[filepath.Join(cwd, "broken.png")] = fileScript{contentType: "image/png", data: []byte("garbage")}

	dims, err := a.ProbeSessionMediaDimensions(context.Background(), "sess-d", []string{"broken.png"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dims) != 0 {
		t.Fatalf("dims = %v, want empty (unparseable head)", dims)
	}
}

func TestProbeSessionMediaDimensionsSessionLevelErrors(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwd := t.TempDir()
	mediaSessionList(f, "sess-d", cwd)

	if _, err := a.ProbeSessionMediaDimensions(context.Background(), "  ", []string{"a.png"}); mediaErrCode(t, err) != core.SessionMediaInvalidParams {
		t.Fatalf("empty session id: err = %v, want media.invalid_params", err)
	}
	if _, err := a.ProbeSessionMediaDimensions(context.Background(), "unknown", []string{"a.png"}); mediaErrCode(t, err) != core.SessionMediaSessionNotFound {
		t.Fatalf("unknown session: err = %v, want media.session_not_found", err)
	}
	if got := fileRequests(f); len(got) != 0 {
		t.Fatalf("session-level failures must not reach /api/file, saw %v", got)
	}
}
