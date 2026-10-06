package dshweb

// session_media_test.go — §5.4 driver-level tests (items 1-9, 14) for
// ReadSessionMedia: resolution root from session truth (the session/list
// row's Cwd, never a grouping path), lexical preflight + containment
// (including symlink escape and the missing-target lexical fallback), the
// official /api/file error mapping (status + FsError body, with the 403
// three-code distinction), the image MIME gate, SHA-256, and the 401 cookie
// refresh retry on the raw route.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// mediaSessionList scripts session/list with one session row carrying cwd.
func mediaSessionList(f *fakeDSHServer, sessionID, cwd string) {
	f.handlers["session/list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{{"sessionId": sessionID, "cwd": cwd}},
	}}
}

// fileRequests returns the decoded path query of every /api/file request.
func fileRequests(f *fakeDSHServer) []string {
	f.files.mu.Lock()
	defer f.files.mu.Unlock()
	out := make([]string, 0, len(f.files.list))
	for _, r := range f.files.list {
		out = append(out, r.path)
	}
	return out
}

// mediaErrCode asserts err is a *core.SessionMediaError and returns its code.
func mediaErrCode(t *testing.T, err error) string {
	t.Helper()
	var me *core.SessionMediaError
	if !errors.As(err, &me) {
		t.Fatalf("error %v is not *core.SessionMediaError", err)
	}
	return me.Code
}

func TestReadSessionMediaResolvesRelativePath(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwd := t.TempDir()
	mediaSessionList(f, "sess-m", cwd)
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	f.file = fileScript{contentType: "image/png", data: png}

	m, err := a.ReadSessionMedia(context.Background(), "sess-m", "assets/render.png")
	if err != nil {
		t.Fatal(err)
	}
	// The target does not exist on this test box, so EvalSymlinks defers to
	// the lexical verdict — the path the provider is asked to read.
	want := filepath.Join(cwd, "assets", "render.png")
	if m.ResolvedPath != want {
		t.Fatalf("resolvedPath = %q, want %q", m.ResolvedPath, want)
	}
	if m.MediaType != "image/png" || string(m.Data) != string(png) {
		t.Fatalf("media = %q, %d bytes", m.MediaType, len(m.Data))
	}
	sum := sha256.Sum256(png)
	if m.ContentSha256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 = %q, want %q", m.ContentSha256, hex.EncodeToString(sum[:]))
	}
	paths := fileRequests(f)
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("api/file paths = %v, want [%q]", paths, want)
	}
}

func TestReadSessionMediaSpaceUnicodeAndPercentPaths(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwd := t.TempDir()
	mediaSessionList(f, "sess-m", cwd)
	// Space and CJK filenames must survive the query round-trip (one
	// server-side decode of the escaped query value); a literal % sequence
	// in the once-decoded wire path must NOT be decoded a second time.
	// (fileScript is one-shot: re-arm per request.)
	for _, rel := range []string{"assets/my image.png", "assets/灰度图.png", "assets/100%off.png"} {
		f.file = fileScript{contentType: "image/png", data: []byte("x")}
		if _, err := a.ReadSessionMedia(context.Background(), "sess-m", rel); err != nil {
			t.Fatalf("rel %q: %v", rel, err)
		}
		paths := fileRequests(f)
		want := filepath.Join(cwd, filepath.FromSlash(rel))
		if len(paths) != 1 || paths[0] != want {
			t.Fatalf("rel %q: api/file paths = %v, want [%q]", rel, paths, want)
		}
		f.files.mu.Lock()
		f.files.list = nil
		f.files.mu.Unlock()
	}
}

func TestReadSessionMediaRejectsEscapeAndNonRelative(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwd := t.TempDir()
	mediaSessionList(f, "sess-m", cwd)
	f.file = fileScript{contentType: "image/png", data: []byte("x")}

	cases := []struct {
		path string
		code string
	}{
		{"a/../../escape.png", core.SessionMediaPathEscape},
		{"../../escape.png", core.SessionMediaPathEscape},
		{"/etc/passwd", core.SessionMediaInvalidParams},
		{"file:///etc/passwd", core.SessionMediaInvalidParams},
		{"http://x/y.png", core.SessionMediaInvalidParams},
		{"https://x/y.png", core.SessionMediaInvalidParams},
		{"//host/y.png", core.SessionMediaInvalidParams},
		{"bad\x00path.png", core.SessionMediaInvalidParams},
		{"bad\npath.png", core.SessionMediaInvalidParams},
	}
	for _, c := range cases {
		_, err := a.ReadSessionMedia(context.Background(), "sess-m", c.path)
		if err == nil {
			t.Fatalf("path %q: want error", c.path)
		}
		if got := mediaErrCode(t, err); got != c.code {
			t.Fatalf("path %q: code = %s, want %s", c.path, got, c.code)
		}
	}
	if got := fileRequests(f); len(got) != 0 {
		t.Fatalf("rejected paths must not reach /api/file, saw %v", got)
	}
}

func TestReadSessionMediaSessionNotFound(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	mediaSessionList(f, "sess-m", t.TempDir())

	_, err := a.ReadSessionMedia(context.Background(), "sess-unknown", "a.png")
	if got := mediaErrCode(t, err); got != core.SessionMediaSessionNotFound {
		t.Fatalf("code = %s, want session_not_found", got)
	}
	if got := fileRequests(f); len(got) != 0 {
		t.Fatalf("unknown session must not reach /api/file, saw %v", got)
	}
}

func TestReadSessionMediaProviderErrorMapping(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwd := t.TempDir()
	mediaSessionList(f, "sess-m", cwd)

	cases := []struct {
		status int
		body   string
		code   string
		detail string
	}{
		{404, "FS_NOT_FOUND", core.SessionMediaNotFound, "FS_NOT_FOUND"},
		{403, "FS_NOT_REGULAR_FILE", core.SessionMediaNotRegularFile, "FS_NOT_REGULAR_FILE"},
		{403, "FS_PERMISSION_DENIED", core.SessionMediaPermissionDenied, "FS_PERMISSION_DENIED"},
		{403, "FS_SANDBOX_DENIED", core.SessionMediaPermissionDenied, "FS_SANDBOX_DENIED"},
		{413, "FS_TOO_LARGE", core.SessionMediaTooLarge, "FS_TOO_LARGE"},
		{499, "FS_ABORTED", core.SessionMediaTransportFailed, "FS_ABORTED"},
		{400, "missing path", core.SessionMediaInvalidParams, "missing path"},
		{400, "absolute path required", core.SessionMediaInvalidParams, "absolute path required"},
		{500, "FS_WEIRD", core.SessionMediaTransportFailed, "HTTP 500: FS_WEIRD"},
		{403, "mystery", core.SessionMediaTransportFailed, "HTTP 403: mystery"},
	}
	for _, c := range cases {
		f.file = fileScript{status: c.status, body: c.body}
		_, err := a.ReadSessionMedia(context.Background(), "sess-m", "assets/x.png")
		if err == nil {
			t.Fatalf("status %d body %q: want error", c.status, c.body)
		}
		var me *core.SessionMediaError
		if !errors.As(err, &me) {
			t.Fatalf("status %d: error %v is not SessionMediaError", c.status, err)
		}
		if me.Code != c.code || me.Detail != c.detail {
			t.Fatalf("status %d body %q: got %s %q, want %s %q",
				c.status, c.body, me.Code, me.Detail, c.code, c.detail)
		}
	}
}

func TestReadSessionMediaNonImageMIME(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwd := t.TempDir()
	mediaSessionList(f, "sess-m", cwd)

	f.file = fileScript{contentType: "text/plain", data: []byte("hi")}
	_, err := a.ReadSessionMedia(context.Background(), "sess-m", "a.png")
	if got := mediaErrCode(t, err); got != core.SessionMediaUnsupportedMediaType {
		t.Fatalf("text/plain code = %s, want unsupported_media_type", got)
	}

	f.file = fileScript{contentType: "", data: []byte("hi")}
	_, err = a.ReadSessionMedia(context.Background(), "sess-m", "a.png")
	if got := mediaErrCode(t, err); got != core.SessionMediaUnsupportedMediaType {
		t.Fatalf("missing content-type code = %s, want unsupported_media_type", got)
	}

	// Parameters after the media type are stripped, not rejected.
	f.file = fileScript{contentType: "image/png; charset=binary", data: []byte{0x89}}
	if _, err := a.ReadSessionMedia(context.Background(), "sess-m", "a.png"); err != nil {
		t.Fatalf("parameterized image/png: %v", err)
	}

	// image/svg+xml passes the whitelist (2026-10-05-r1 parity: official
	// /api/file serves it, official Web <img> renders it; iOS rasterizes
	// client-side). The bridge carries the bytes verbatim.
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20"/></svg>`)
	f.file = fileScript{contentType: "image/svg+xml", data: svg}
	media, err := a.ReadSessionMedia(context.Background(), "sess-m", "a.svg")
	if err != nil {
		t.Fatalf("image/svg+xml: %v", err)
	}
	if media.MediaType != "image/svg+xml" || !bytes.Equal(media.Data, svg) {
		t.Fatalf("svg media = %+v (data %d bytes)", media.MediaType, len(media.Data))
	}
}

func TestReadSessionMediaUngroupedSessionCwdIsResolutionRoot(t *testing.T) {
	// §5.4 test 14: sessions resolve against their REAL cwd from session
	// truth. The driver reads item.Cwd directly — the workspace grouping
	// layer (which would label one of these 未分组 and the other with a
	// group path) never enters this path, so a grouping path ≠ cwd can
	// never masquerade as the resolution root (F-B1).
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwdA, cwdB := t.TempDir(), t.TempDir()
	f.handlers["session/list"] = fakeRPCResponse{value: map[string]any{
		"items": []map[string]any{
			{"sessionId": "sess-a", "cwd": cwdA},
			{"sessionId": "sess-b", "cwd": cwdB},
		},
	}}
	for _, tc := range []struct{ sessionID, want string }{
		{"sess-a", filepath.Join(cwdA, "assets", "render.png")},
		{"sess-b", filepath.Join(cwdB, "assets", "render.png")},
	} {
		f.file = fileScript{contentType: "image/png", data: []byte("x")} // one-shot script
		f.files.mu.Lock()
		f.files.list = nil
		f.files.mu.Unlock()
		m, err := a.ReadSessionMedia(context.Background(), tc.sessionID, "assets/render.png")
		if err != nil {
			t.Fatalf("%s: %v", tc.sessionID, err)
		}
		if m.ResolvedPath != tc.want {
			t.Fatalf("%s resolvedPath = %q, want %q", tc.sessionID, m.ResolvedPath, tc.want)
		}
		if paths := fileRequests(f); len(paths) != 1 || paths[0] != tc.want {
			t.Fatalf("%s api/file paths = %v, want [%q]", tc.sessionID, paths, tc.want)
		}
	}
}

func TestReadSessionMediaSymlinkContainment(t *testing.T) {
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwd := t.TempDir()
	mediaSessionList(f, "sess-m", cwd)
	f.file = fileScript{contentType: "image/png", data: []byte("x")}

	// In-cwd symlink to an in-cwd target: resolves to the target and reads.
	inside := filepath.Join(cwd, "real.png")
	if err := os.WriteFile(inside, []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, filepath.Join(cwd, "link.png")); err != nil {
		t.Fatal(err)
	}
	m, err := a.ReadSessionMedia(context.Background(), "sess-m", "link.png")
	if err != nil {
		t.Fatal(err)
	}
	resolvedInside, rerr := filepath.EvalSymlinks(inside)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if m.ResolvedPath != resolvedInside {
		t.Fatalf("resolvedPath = %q, want %q", m.ResolvedPath, resolvedInside)
	}

	// In-cwd symlink pointing OUTSIDE the cwd: rejected as path_escape.
	outside := t.TempDir()
	outFile := filepath.Join(outside, "secret.png")
	if err := os.WriteFile(outFile, []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outFile, filepath.Join(cwd, "escape.png")); err != nil {
		t.Fatal(err)
	}
	_, err = a.ReadSessionMedia(context.Background(), "sess-m", "escape.png")
	if got := mediaErrCode(t, err); got != core.SessionMediaPathEscape {
		t.Fatalf("symlink escape code = %s, want path_escape", got)
	}
}

func TestReadSessionMediaMissingTargetKeepsLexicalVerdict(t *testing.T) {
	// EvalSymlinks fails on a missing target: the lexical verdict stands and
	// the provider answers FS_NOT_FOUND — the missing-file semantics survive
	// the symlink check (§5.2 item 2).
	f := newFakeDSHServer(t)
	defer f.Close()
	a := newTestAgent(t, f)
	cwd := t.TempDir()
	mediaSessionList(f, "sess-m", cwd)
	f.file = fileScript{status: 404, body: "FS_NOT_FOUND"}

	_, err := a.ReadSessionMedia(context.Background(), "sess-m", "assets/nope.png")
	if got := mediaErrCode(t, err); got != core.SessionMediaNotFound {
		t.Fatalf("code = %s, want not_found", got)
	}
	paths := fileRequests(f)
	want := filepath.Join(cwd, "assets", "nope.png")
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("api/file paths = %v, want [%q]", paths, want)
	}
}

func TestReadFile401RefreshesOnceAndRetries(t *testing.T) {
	token := "media-token"
	var mints sync.Mutex
	var minted string
	var fileHits atomic.Int32
	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/file", func(w http.ResponseWriter, r *http.Request) {
		fileHits.Add(1)
		mints.Lock()
		ok := minted != "" && strings.Contains(r.Header.Get("Cookie"), minted)
		mints.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P'})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || r.URL.Query().Get("token") != token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		name, value := mintBrowserCookie(authorityOf(serverURL), testSecret(3), time.Now())
		mints.Lock()
		minted = name + "=" + value
		mints.Unlock()
		http.SetCookie(w, &http.Cookie{Name: name, Value: value, MaxAge: int(dshCookieMaxAge.Seconds())})
		w.Header().Set("Location", "./")
		w.WriteHeader(http.StatusSeeOther)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	serverURL = srv.URL

	auth := newSeatAuth(t.TempDir(), nil)
	auth.setLaunchURL(srv.URL + "/?token=" + token)
	c := NewClient(srv.URL, nil)
	c.SetAuth(auth)

	contentType, data, err := c.ReadFile(context.Background(), "/abs/x.png", 1<<20)
	if err != nil {
		t.Fatalf("ReadFile after refresh: %v", err)
	}
	if contentType != "image/png" || len(data) != 2 {
		t.Fatalf("read = %q, %d bytes", contentType, len(data))
	}
	if fileHits.Load() != 2 {
		t.Fatalf("api/file hits = %d, want 2 (401 + refresh retry)", fileHits.Load())
	}
}
