// store_test.go covers the DSH session-store reader: projectKey parity with
// the TS implementation (pin 47f9438 format.ts), dual-suffix artifact
// handling, header/title derivation, subagent filtering inputs, and id
// resolution. Fixtures are generated in-test (zstd via the same decoder's
// encoder counterpart); the real-store smoke case is opt-in via
// DSH_STORE_SMOKE=1 so CI never depends on a developer's ~/.dsh.
package dsh

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestProjectKeyParityWithTS(t *testing.T) {
	cases := []struct{ name, cwd, want string }{
		// Verified against dsh-session-persistence-jsonl format.ts (pin 47f9438)
		// and the real on-disk directory for this exact cwd.
		{"real repo path", "/Users/jacklee/Projects/cordcode-ios", "--Users-jacklee-Projects-cordcode-ios--"},
		{"separator runs collapse", "/a//b", "--a-b--"},
		{"windows separators", `C:\Users\jack\work`, "--C-Users-jack-work--"},
		{"tilde escapes itself", "/~/notes", "--~007E-notes--"},
		{"non-ascii escapes per utf16 unit", "/项目/x", "--~9879~76EE-x--"},
		{"astral pair escapes both units", "/\U0001F600/x", "--~D83D~DE00-x--"},
		{"leading separators stripped", "/--leading", "--leading--"},
		{"empty maps to root", "", ""},
		{"all-separator input", "///", "--root--"},
		{"safe punctuation stays", "/a.b_c-d", "--a.b_c-d--"},
	}
	for _, tc := range cases {
		if got := projectKey(tc.cwd); got != tc.want {
			t.Errorf("%s: projectKey(%q) = %q, want %q", tc.name, tc.cwd, got, tc.want)
		}
	}
}

func writeSessionLog(t *testing.T, dir string, records []string, compress bool) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte(strings.Join(records, "\n") + "\n")
	path := filepath.Join(dir, "session.jsonl")
	if compress {
		var buf bytes.Buffer
		enc, err := zstd.NewWriter(&buf, zstd.WithEncoderConcurrency(1))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enc.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
		path += ".zstd"
		payload = buf.Bytes()
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	plainHeader  = `{"type":"session","version":0,"id":"session-aaa","createdAt":1786778234856,"cwd":"/Users/jacklee/Projects/demo","delegationDepth":0,"agentPreset":"standard"}`
	subagentHdr  = `{"type":"session","version":0,"id":"child-1","createdAt":1786778234857,"cwd":"/Users/jacklee/Projects/demo","parentSession":"session-aaa","origin":"subagent","delegationDepth":1}`
	humanPrompt  = `{"type":"user/message","seq":9,"time":1786778234890,"data":{"content":[{"type":"text","text":"帮我读一下这个仓库的结构，然后给出接入建议清单"}],"source":{"kind":"user"},"role":"user","id":"m1"}}`
	pluginPrompt = `{"type":"user/message","seq":10,"time":1786778234891,"data":{"content":[{"type":"text","text":"<system-reminder>noise</system-reminder>"}],"source":{"kind":"plugin","plugin":"skills"},"role":"user","id":"m2"}}`
	titleEvent   = `{"type":"session/title","seq":13,"time":1786778234891,"data":{"title":"帮我读一下这个仓库","messageSeqs":[9],"source":{"kind":"fallback"}}}`
)

func TestScanSessionsDualSuffixAndHeaders(t *testing.T) {
	root := t.TempDir()
	writeSessionLog(t, filepath.Join(root, "--Users-jacklee-Projects-demo--", "session-aaa"),
		[]string{plainHeader, humanPrompt}, false)
	writeSessionLog(t, filepath.Join(root, "--Users-jacklee-Projects-demo--", "child-1"),
		[]string{subagentHdr}, true)
	// Garbage directories are skipped, not fatal.
	if err := os.MkdirAll(filepath.Join(root, "--broken--", "nope"), 0o755); err != nil {
		t.Fatal(err)
	}

	store := &dshSessionStore{root: root}
	sessions, err := store.scanSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("scan found %d sessions, want 2: %+v", len(sessions), sessions)
	}
	byID := map[string]storeSession{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	main := byID["session-aaa"]
	if main.Cwd != "/Users/jacklee/Projects/demo" || main.CreatedAt != 1786778234856 {
		t.Errorf("header fields not honored: %+v", main)
	}
	if !main.Plain {
		t.Errorf("session-aaa fixture is plaintext .jsonl: %+v", main)
	}
	if sub := byID["child-1"]; sub.Plain {
		t.Errorf("child fixture is zstd .jsonl.zstd: %+v", sub)
	}
}

func TestSessionTitleFoldAndFallback(t *testing.T) {
	root := t.TempDir()
	withTitle := writeSessionLog(t, filepath.Join(root, "a"),
		[]string{plainHeader, pluginPrompt, humanPrompt, titleEvent}, true)
	fallbackOnly := writeSessionLog(t, filepath.Join(root, "b"),
		[]string{plainHeader, pluginPrompt, humanPrompt}, false)

	if got := sessionTitle(withTitle, false); got != "帮我读一下这个仓库" {
		t.Errorf("title fold = %q, want session/title event title", got)
	}
	if got := sessionTitle(fallbackOnly, true); got == "" {
		t.Errorf("fallback title unexpectedly empty")
	}
	// The fallback must use the FIRST HUMAN prompt, never the plugin one.
	if got := sessionTitle(fallbackOnly, true); !strings.Contains(got, "帮我读一下") || strings.Contains(got, "system-reminder") {
		t.Errorf("fallback title = %q, want first human prompt trimmed", got)
	}
}

func TestFallbackTitleLimitsNeverSplitCodePoints(t *testing.T) {
	long := strings.Repeat("啊", 40)
	got := fallbackTitleFromBlocks([]dshContentBlock{{Type: "text", Text: long}})
	if len(got) > storeTitleFallbackMaxBytes {
		t.Fatalf("fallback exceeds byte budget: %d bytes", len(got))
	}
	for _, r := range got {
		if r == 0xFFFD {
			t.Fatalf("fallback split a code point: %q", got)
		}
	}
	words := strings.Fields(strings.Repeat("w ", 100))
	got = fallbackTitleFromBlocks([]dshContentBlock{{Type: "text", Text: strings.Join(words, " ")}})
	if got := strings.Fields(got); len(got) > storeTitleFallbackMaxWords {
		t.Fatalf("fallback exceeds word budget: %d words", len(got))
	}
}

func TestResolveSessionFile(t *testing.T) {
	root := t.TempDir()
	writeSessionLog(t, filepath.Join(root, "--demo--", "session-aaa"), []string{plainHeader}, false)
	store := &dshSessionStore{root: root}

	if s, ok := store.resolveSessionFile("session-aaa"); !ok || s.ID != "session-aaa" {
		t.Fatalf("resolve by id failed: %+v ok=%v", s, ok)
	}
	if _, ok := store.resolveSessionFile("../escape"); ok {
		t.Fatal("traversal id must be rejected")
	}
	if _, ok := store.resolveSessionFile("a/b"); ok {
		t.Fatal("separator id must be rejected")
	}
	if _, ok := store.resolveSessionFile("missing"); ok {
		t.Fatal("unknown id must not resolve")
	}
}

func TestOpenDshSessionStoreNoHome(t *testing.T) {
	t.Setenv("DSH_HOME", "")
	t.Setenv("HOME", "")
	store := openDshSessionStore()
	if store.root != "" {
		t.Fatalf("no HOME ⇒ empty store root, got %q", store.root)
	}
	if sessions, err := store.scanSessions(); err != nil || sessions != nil {
		t.Fatalf("no-home scan must be empty/nil, got %v %v", sessions, err)
	}
}

// TestScanRealStoreSmoke validates the reader against the developer's real
// harness store (zstd web artifacts). Opt-in: DSH_STORE_SMOKE=1.
func TestScanRealStoreSmoke(t *testing.T) {
	if os.Getenv("DSH_STORE_SMOKE") != "1" {
		t.Skip("set DSH_STORE_SMOKE=1 to scan the real ~/.dsh/sessions")
	}
	store := openDshSessionStore()
	sessions, err := store.scanSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) == 0 {
		t.Fatal("real store should contain sessions")
	}
	for _, s := range sessions {
		if s.ID == "" || s.Path == "" {
			t.Fatalf("malformed session: %+v", s)
		}
		t.Logf("id=%s cwd=%s plain=%v title=%q", s.ID, s.Cwd, s.Plain, sessionTitle(s.Path, s.Plain))
	}
}
