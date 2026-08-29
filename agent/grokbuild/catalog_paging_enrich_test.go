package grokbuild

// Tests for the 2026-08-14 catalog completeness fix: session/list cursor
// paging (Grok 1.0.0 caps every page at 30 rows regardless of limit — 91
// on-disk sessions paged as 30/30/30/1) and empty-title enrichment from
// on-disk summary.json generated_title (page 1 carried exactly one title, so
// the placeholder filter reduced the whole catalog to a single iOS card).

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func page(ids ...string) grokSessionListResult {
	sessions := make([]grokSessionListItem, 0, len(ids))
	for _, id := range ids {
		sessions = append(sessions, grokSessionListItem{SessionID: id, Cwd: "/w"})
	}
	return grokSessionListResult{Sessions: sessions}
}

func TestListSessionsPaged_WalksAllPagesAndDedupes(t *testing.T) {
	calls := []string{}
	fetch := func(_ context.Context, cursor string) (grokSessionListResult, error) {
		calls = append(calls, cursor)
		switch cursor {
		case "":
			return grokSessionListResult{
				Sessions:   page("s1", "s2", "s1").Sessions, // upstream emits duplicate rows
				NextCursor: "c2",
			}, nil
		case "c2":
			return grokSessionListResult{
				Sessions:   page("s2", "s3").Sessions,
				NextCursor: "c3",
			}, nil
		default:
			return page("s4"), nil // last page: no cursor
		}
	}
	out, err := listSessionsPaged(context.Background(), fetch)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range out {
		ids = append(ids, s.ID)
	}
	want := []string{"s1", "s2", "s3", "s4"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v (first occurrence recency order)", ids, want)
		}
	}
	if len(calls) != 3 {
		t.Fatalf("fetch calls = %v, want 3 pages", calls)
	}
	if calls[0] != "" || calls[1] != "c2" || calls[2] != "c3" {
		t.Fatalf("cursor sequence = %v", calls)
	}
}

func TestListSessionsPaged_SinglePageNoCursor(t *testing.T) {
	fetch := func(_ context.Context, cursor string) (grokSessionListResult, error) {
		if cursor != "" {
			t.Fatalf("second fetch with cursor %q — must stop after cursorless page", cursor)
		}
		return page("only"), nil
	}
	out, err := listSessionsPaged(context.Background(), fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ID != "only" {
		t.Fatalf("out = %+v", out)
	}
}

func TestListSessionsPaged_PageCapBoundsPathologicalCursor(t *testing.T) {
	n := 0
	fetch := func(_ context.Context, _ string) (grokSessionListResult, error) {
		n++
		return grokSessionListResult{
			Sessions:   page(fmt.Sprintf("s%d", n)).Sessions,
			NextCursor: fmt.Sprintf("c%d", n), // never ends
		}, nil
	}
	out, err := listSessionsPaged(context.Background(), fetch)
	if err != nil {
		t.Fatal(err)
	}
	if n != grokCatalogMaxListPages {
		t.Fatalf("fetch calls = %d, want page cap %d", n, grokCatalogMaxListPages)
	}
	if len(out) != grokCatalogMaxListPages {
		t.Fatalf("unique sessions = %d, want %d", len(out), grokCatalogMaxListPages)
	}
}

func writeGrokSummary(t *testing.T, home, cwd, sessionID, generatedTitle string) {
	t.Helper()
	encoded := url.PathEscape(cwd)
	dir := filepath.Join(home, "sessions", encoded, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"generated_title":%q,"info":{"id":%q,"cwd":%q}}`, generatedTitle, sessionID, cwd)
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnrichGrokCatalogTitles_FillsOnlyEmptyTitlesFromDisk(t *testing.T) {
	home := t.TempDir()
	writeGrokSummary(t, home, "/w", "titled-on-disk", "Disk Title")
	writeGrokSummary(t, home, "/w", "untitled-on-disk", "")
	writeGrokSummary(t, home, "/w", "whitespace-disk-title", "   ")
	// "missing-on-disk" and "cli-titled" get no disk directory at all.

	sessions := []core.AgentSessionInfo{
		{ID: "cli-titled", Summary: "CLI Title"},
		{ID: "empty-cli-titled-on-disk", Summary: ""},
		{ID: "untitled-on-disk", Summary: ""},
		{ID: "whitespace-disk-title", Summary: "  "},
		{ID: "missing-on-disk", Summary: ""},
	}
	writeGrokSummary(t, home, "/w", "empty-cli-titled-on-disk", "Revived Title")

	enrichGrokCatalogTitles(home, sessions)

	want := map[string]string{
		"cli-titled":               "CLI Title", // non-empty CLI title untouched
		"empty-cli-titled-on-disk": "Revived Title",
		"untitled-on-disk":         "", // no fabricated title
		"whitespace-disk-title":    "", // whitespace title treated as absent
		"missing-on-disk":          "", // no disk dir: stays empty
	}
	for id, expected := range want {
		for _, s := range sessions {
			if s.ID == id && s.Summary != expected {
				t.Fatalf("%s summary = %q, want %q", id, s.Summary, expected)
			}
		}
	}
}

func TestEnrichGrokCatalogTitles_EmptyHomeIsNoop(t *testing.T) {
	sessions := []core.AgentSessionInfo{{ID: "x", Summary: ""}}
	enrichGrokCatalogTitles("", sessions)
	if sessions[0].Summary != "" {
		t.Fatalf("summary = %q, want untouched", sessions[0].Summary)
	}
}

func TestFindGrokCatalogSessionDirUsesCatalogCwdLayout(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "workspace with spaces")
	const sessionID = "catalog-direct"
	writeGrokSummary(t, home, cwd, sessionID, "Direct title")

	got := findGrokCatalogSessionDir(home, cwd, sessionID)
	want := filepath.Join(home, "sessions", url.PathEscape(cwd), sessionID)
	if got != want {
		t.Fatalf("direct catalog lookup = %q, want %q", got, want)
	}
}

func TestReadGrokGeneratedTitleEdgeCases(t *testing.T) {
	dir := t.TempDir()
	if got := readGrokGeneratedTitle(filepath.Join(dir, "absent.json")); got != "" {
		t.Fatalf("absent file → %q", got)
	}
	path := filepath.Join(dir, "s.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readGrokGeneratedTitle(path); got != "" {
		t.Fatalf("malformed json → %q", got)
	}
	if err := os.WriteFile(path, []byte(`{"generated_title":"  padded  "}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readGrokGeneratedTitle(path); got != "padded" {
		t.Fatalf("trim → %q", got)
	}
}
