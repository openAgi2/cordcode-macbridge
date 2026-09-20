package opencodeweb

// desktop_membership_test.go covers the off-home membership implementation
// (phase-1 plan §3–§8): window registry parsing, tabKey exact join, home
// four-state, per-tab by-ID proof, failure-domain separation, input hard
// limits, and the end-to-end off-home authorization against the verified
// 1.18.31 fixtures in testdata/desktop-persist-1.18.31/.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain makes the whole package hermetic: no test reads the real
// machine's Desktop persist unless it explicitly injects a lookup. Existing
// tests that relied on "real persist exists but its URLs never match the
// random test port" keep the same outcome (fallback) deterministically.
func TestMain(m *testing.M) {
	desktopPersistLookup = func() []string { return nil }
	desktopSettingsLookup = func() []string { return nil }
	os.Exit(m.Run())
}

func TestTabKeyMatchesOfficialAlgorithm(t *testing.T) {
	// URL-safe, no padding — "sidecar" encodes to c2lkZWNhcg (NOT c2lkZWNhcg==),
	// the variant the dual-method decode pinned against the real persist.
	if got := tabKey("sidecar", "ses_x"); got != "sidecar\n/server/c2lkZWNhcg/session/ses_x" {
		t.Fatalf("sidecar tabKey = %q", got)
	}
	if got := tabKey("http://127.0.0.1:4096", "ses_x"); got != "http://127.0.0.1:4096\n/server/aHR0cDovLzEyNy4wLjAuMTo0MDk2/session/ses_x" {
		t.Fatalf("full-URL tabKey = %q", got)
	}
}

func TestWindowDataFileSanitizes(t *testing.T) {
	if got := windowDataFile("de387f0b-1d50-4421-8bcc-e6530b051c20"); got != "opencode.window.de387f0b-1d50-4421-8bcc-e6530b051c20.dat" {
		t.Fatalf("uuid passthrough = %q", got)
	}
	if got := windowDataFile("a b/c:d"); got != "opencode.window.a-b-c-d.dat" {
		t.Fatalf("sanitized = %q", got)
	}
}

// fixturePath resolves a file inside the verified fixture set.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("testdata", "desktop-persist-1.18.31", "samples", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return p
}

func TestParseDesktopWindowTabsFromVerifiedFixtures(t *testing.T) {
	tabs, err := parseDesktopWindowTabs(mustRead(t, fixturePath(t, "window-registered.json")))
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 3 {
		t.Fatalf("window-registered tabs = %d, want 3", len(tabs))
	}
	for _, tab := range tabs {
		if tab.Server != "sidecar" || tab.InfoDirectory == "" {
			t.Fatalf("tab = %+v (want sidecar + joined info dir)", tab)
		}
	}

	full, err := parseDesktopWindowTabs(mustRead(t, fixturePath(t, "window-fullurl-archived-tab.json")))
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 4 {
		t.Fatalf("window-fullurl-archived tabs = %d, want 4", len(full))
	}
	sawFullURL := false
	for _, tab := range full {
		if tab.Server == "http://127.0.0.1:4096" {
			sawFullURL = true
			if tab.SessionID != "ses_sample000000000000000eE5" {
				t.Fatalf("full-URL tab session = %q", tab.SessionID)
			}
		}
	}
	if !sawFullURL {
		t.Fatal("fixture must carry the full-URL server tab")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// writePersistRoot materializes a Desktop persist root for injection.
type persistRoot struct {
	dir string
}

func writePersistRoot(t *testing.T, homeRow string, windowIDs []string, windowFiles map[string]string) *persistRoot {
	t.Helper()
	dir := t.TempDir()
	if homeRow != "" {
		writeFile(t, filepath.Join(dir, "opencode.global.dat"), homeRow)
	}
	if windowIDs != nil {
		ids, _ := json.Marshal(windowIDs)
		writeFile(t, filepath.Join(dir, "opencode.settings"), fmt.Sprintf(`{"windowIds":%s,"defaultServerUrl":"http://127.0.0.1:4096"}`, ids))
	}
	for name, body := range windowFiles {
		writeFile(t, filepath.Join(dir, name), body)
	}
	return &persistRoot{dir: dir}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func injectPersist(t *testing.T, root *persistRoot) {
	t.Helper()
	prevPersist, prevSettings := desktopPersistLookup, desktopSettingsLookup
	desktopPersistLookup = func() []string { return []string{filepath.Join(root.dir, "opencode.global.dat")} }
	desktopSettingsLookup = func() []string { return []string{filepath.Join(root.dir, "opencode.settings")} }
	t.Cleanup(func() { desktopPersistLookup, desktopSettingsLookup = prevPersist, prevSettings })
}

func homePersist(serverURL string, worktrees ...string) string {
	rows := make([]string, 0, len(worktrees))
	for _, wt := range worktrees {
		rows = append(rows, fmt.Sprintf(`{"worktree":%q}`, wt))
	}
	inner := fmt.Sprintf(`{"projects":{%q:[%s]}}`, serverURL, strings.Join(rows, ","))
	return fmt.Sprintf(`{"server":%q}`, inner)
}

func windowPersist(tabs, info, closed string) string {
	return fmt.Sprintf(`{"tabs":%q,"tabs.info":%q,"tabs.closed":%q}`, tabs, info, closed)
}

func TestReadDesktopWindowTabsRegistrySemantics(t *testing.T) {
	tab := `{"type":"session","server":"sidecar","sessionId":"ses_t1"}`
	key := tabKey("sidecar", "ses_t1")
	root := writePersistRoot(t, "",
		[]string{"11111111-2222-4333-8444-555555555555"},
		map[string]string{
			"opencode.window.11111111-2222-4333-8444-555555555555.dat": windowPersist(
				fmt.Sprintf("[%s]", tab),
				fmt.Sprintf(`{%q:{"title":"t","directory":"/tmp/dir-d"}}`, key),
				"[]"),
			// Orphan window file: NOT in windowIds, must be ignored entirely.
			"opencode.window.99999999-8888-4777-8666-555555555555.dat": windowPersist(
				`[{"type":"session","server":"sidecar","sessionId":"ses_orphan"}]`, "{}", "[]"),
		})
	injectPersist(t, root)

	tabs, err := readDesktopWindowTabs()
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 1 || tabs[0].SessionID != "ses_t1" || tabs[0].InfoDirectory != "/tmp/dir-d" {
		t.Fatalf("tabs = %+v, want the single registered-window tab with joined info", tabs)
	}
}

func TestReadDesktopWindowTabsRegisteredIDWithoutFileIsEmpty(t *testing.T) {
	root := writePersistRoot(t, "", []string{"11111111-2222-4333-8444-555555555555"}, nil)
	injectPersist(t, root)
	tabs, err := readDesktopWindowTabs()
	if err != nil {
		t.Fatalf("missing window file must be an empty window, got %v", err)
	}
	if len(tabs) != 0 {
		t.Fatalf("tabs = %+v, want none", tabs)
	}
}

func TestReadDesktopWindowTabsCorruptFileFailsClosed(t *testing.T) {
	root := writePersistRoot(t, "",
		[]string{"11111111-2222-4333-8444-555555555555"},
		map[string]string{"opencode.window.11111111-2222-4333-8444-555555555555.dat": `{"tabs": "[broken"`})
	injectPersist(t, root)
	if _, err := readDesktopWindowTabs(); err == nil {
		t.Fatal("corrupt registered window persist must be a membership parse failure")
	}
}

func TestReadDesktopWindowTabsInputLimits(t *testing.T) {
	many := make([]string, 0, maxDesktopWindowIDs+1)
	for i := 0; i <= maxDesktopWindowIDs; i++ {
		many = append(many, fmt.Sprintf("w%d", i))
	}
	root := writePersistRoot(t, "", many, nil)
	injectPersist(t, root)
	if _, err := readDesktopWindowTabs(); err == nil || !strings.Contains(err.Error(), "windowIds") {
		t.Fatalf("over-limit windowIds must fail, got %v", err)
	}

	// Over-limit tabs in one window (valid JSON array, one row too many).
	rows := make([]string, 0, maxDesktopTabsPerWindow+1)
	for i := 0; i <= maxDesktopTabsPerWindow; i++ {
		rows = append(rows, fmt.Sprintf(`{"type":"session","server":"sidecar","sessionId":"ses_%d"}`, i))
	}
	root2 := writePersistRoot(t, "", []string{"w1"},
		map[string]string{"opencode.window.w1.dat": windowPersist("["+strings.Join(rows, ",")+",{}]", "{}", "[]")})
	injectPersist(t, root2)
	if _, err := readDesktopWindowTabs(); err == nil || !strings.Contains(err.Error(), "tabs (limit") {
		t.Fatalf("over-limit tabs must fail, got %v", err)
	}
}

func TestParseDesktopHomeStateFourStates(t *testing.T) {
	// Row present.
	state, dirs := parseDesktopHomeState([]byte(homePersist("http://s", "/tmp/a", "/tmp/b")), "http://s")
	if state != desktopHomeRowPresent || len(dirs) != 2 {
		t.Fatalf("rowPresent: state=%d dirs=%v", state, dirs)
	}
	// Row authoritatively empty.
	state, dirs = parseDesktopHomeState([]byte(homePersist("http://s")), "http://s")
	if state != desktopHomeRowEmpty || dirs != nil {
		t.Fatalf("rowEmpty: state=%d dirs=%v", state, dirs)
	}
	// Row missing for this server URL.
	state, _ = parseDesktopHomeState([]byte(homePersist("http://other", "/tmp/a")), "http://s")
	if state != desktopHomeRowMissing {
		t.Fatalf("rowMissing: state=%d", state)
	}
	// Corrupt outer JSON.
	if state, _ = parseDesktopHomeState([]byte(`{"server": `), "http://s"); state != desktopHomeCorrupt {
		t.Fatalf("corrupt outer: state=%d", state)
	}
	// Corrupt inner (server value) JSON.
	bad := `{"server":"{\"projects\":{\"http://s\":[BROKEN"}}`
	if state, _ = parseDesktopHomeState([]byte(bad), "http://s"); state != desktopHomeCorrupt {
		t.Fatalf("corrupt inner: state=%d", state)
	}
	// File missing (empty blob).
	if state, _ = parseDesktopHomeState(nil, "http://s"); state != desktopHomeFileMissing {
		t.Fatalf("fileMissing: state=%d", state)
	}
}

func TestLoadHomeProjectDirsFourStateBehavior(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	agent, serve := newC2Agent(t, dirA, dirB) // registry fallback source
	ctx := context.Background()
	c, err := agent.clientFor(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Row authoritatively empty → empty home, NO GET /project fallback.
	root := writePersistRoot(t, homePersist(c.baseURL), nil, nil)
	injectPersist(t, root)
	agent.invalidateProjectCache()
	dirs, source, err := agent.loadHomeProjectDirs(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 0 || !strings.Contains(source, "desktop-persist") {
		t.Fatalf("rowEmpty must be an empty home with no fallback, got dirs=%v source=%q", dirs, source)
	}
	if got := len(serve.requestsFor("/project")); got != 0 {
		t.Fatalf("rowEmpty must not hit GET /project, got %d requests", got)
	}

	// Row missing → fallback to the serve registry.
	root = writePersistRoot(t, homePersist("http://other-server", "/tmp/x"), nil, nil)
	injectPersist(t, root)
	agent.invalidateProjectCache()
	dirs, source, err = agent.loadHomeProjectDirs(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source, "get-project-registry") || len(dirs) != 2 {
		t.Fatalf("rowMissing must fall back to the registry, got dirs=%v source=%q", dirs, source)
	}

	// Corrupt persist → membership parse failure (catalog error upstream).
	root = writePersistRoot(t, `{"server":"[broken"`, nil, nil)
	injectPersist(t, root)
	agent.invalidateProjectCache()
	if _, _, err := agent.loadHomeProjectDirs(ctx, c); err == nil {
		t.Fatal("corrupt home persist must be an error, not a fallback")
	}
}

func TestMembershipOffHomeTabAuthorizesDirectory(t *testing.T) {
	dirA, dirB, dirC := t.TempDir(), t.TempDir(), t.TempDir()
	agent, serve := newC2Agent(t, dirA, dirB)
	ctx := context.Background()
	c, err := agent.clientFor(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const sessionID = "ses_tab1"
	root := writePersistRoot(t, homePersist(c.baseURL, dirA, dirB),
		[]string{"w1"},
		map[string]string{"opencode.window.w1.dat": windowPersist(
			fmt.Sprintf(`[{"type":"session","server":"sidecar","sessionId":%q}]`, sessionID),
			fmt.Sprintf(`{%q:{"title":"t","directory":%q}}`, tabKey("sidecar", sessionID), dirC),
			"[]")})
	injectPersist(t, root)

	// By-ID proof response: the session lives in dirC (server truth).
	serve.responses["/session/"+sessionID] = fmt.Sprintf(
		`{"id":%q,"directory":%q,"title":"off-home session","time":{"created":1,"updated":2}}`, sessionID, dirC)
	// Scoped list for dirC returns one root session.
	serve.dirResponses = map[string]string{"/session|" + dirC: c2Rows(t, dirC, "ses_in_c")}

	agent.invalidateProjectCache()
	dirs, err := agent.projectWorktreeDirs(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, d := range dirs {
		found[d] = true
	}
	if !found[dirC] {
		t.Fatalf("off-home tab directory must be authorized, dirs=%v", dirs)
	}

	// The proof request must route through the tab's own directory (plan §3.3).
	proofs := serve.requestsFor("/session/" + sessionID)
	if len(proofs) != 1 || proofs[0].Directory != dirC {
		t.Fatalf("proof request = %+v, want one routed with directory %s", proofs, dirC)
	}

	// End to end: ListSessions covers dirC's session.
	sessions, err := agent.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sawC := false
	for _, s := range sessions {
		if s.ID == "ses_in_c" {
			sawC = true
		}
	}
	if !sawC {
		t.Fatalf("ListSessions must include the off-home directory's session, got %v", sessions)
	}
}

func TestMembershipProofFailureSkipsTabWithoutAmplifying(t *testing.T) {
	dirA, dirB, dirC := t.TempDir(), t.TempDir(), t.TempDir()
	agent, _ := newC2Agent(t, dirA, dirB)
	ctx := context.Background()
	c, err := agent.clientFor(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const sessionID = "ses_stale"
	root := writePersistRoot(t, homePersist(c.baseURL, dirA, dirB),
		[]string{"w1"},
		map[string]string{"opencode.window.w1.dat": windowPersist(
			fmt.Sprintf(`[{"type":"session","server":"sidecar","sessionId":%q}]`, sessionID),
			fmt.Sprintf(`{%q:{"title":"t","directory":%q}}`, tabKey("sidecar", sessionID), dirC),
			"[]")})
	injectPersist(t, root)
	// No by-ID response configured → recordingServe answers 404 (stale tab).

	agent.invalidateProjectCache()
	dirs, err := agent.projectWorktreeDirs(ctx, c)
	if err != nil {
		t.Fatalf("proof failure must skip the tab, not fail the catalog: %v", err)
	}
	for _, d := range dirs {
		if d == dirC {
			t.Fatalf("stale tab must not authorize its directory, dirs=%v", dirs)
		}
	}
	// Home-only catalog stays usable end to end.
	if _, err := agent.ListSessions(ctx); err != nil {
		t.Fatalf("home-only catalog must stay usable: %v", err)
	}
}

func TestMembershipArchivedTabAuthorizesDirectoryButSessionStaysHidden(t *testing.T) {
	dirA, dirB, dirC := t.TempDir(), t.TempDir(), t.TempDir()
	agent, serve := newC2Agent(t, dirA, dirB)
	ctx := context.Background()
	c, err := agent.clientFor(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const sessionID = "ses_archived"
	root := writePersistRoot(t, homePersist(c.baseURL, dirA, dirB),
		[]string{"w1"},
		map[string]string{"opencode.window.w1.dat": windowPersist(
			fmt.Sprintf(`[{"type":"session","server":"sidecar","sessionId":%q}]`, sessionID),
			fmt.Sprintf(`{%q:{"title":"t","directory":%q}}`, tabKey("sidecar", sessionID), dirC),
			"[]")})
	injectPersist(t, root)

	// By-ID proof succeeds for the archived session (type-blind proof).
	serve.responses["/session/"+sessionID] = fmt.Sprintf(
		`{"id":%q,"directory":%q,"title":"archived","time":{"created":1,"updated":2,"archived":1789919863000}}`, sessionID, dirC)
	// Scoped list for dirC: one archived row (must be OD-1 filtered) + one live row.
	serve.dirResponses = map[string]string{"/session|" + dirC: fmt.Sprintf(`[
		{"id":"ses_archived","directory":%q,"title":"archived","time":{"created":1,"updated":2,"archived":1789919863000}},
		{"id":"ses_live","directory":%q,"title":"live","time":{"created":3,"updated":4}}
	]`, dirC, dirC)}

	agent.invalidateProjectCache()
	sessions, err := agent.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sawLive, sawArchived := false, false
	for _, s := range sessions {
		if s.ID == "ses_live" {
			sawLive = true
		}
		if s.ID == "ses_archived" {
			sawArchived = true
		}
	}
	if !sawLive {
		t.Fatalf("archived tab's directory must be authorized and its live session listed, got %v", sessions)
	}
	if sawArchived {
		t.Fatalf("archived session must stay hidden from default enumeration (OD-1), got %v", sessions)
	}
}

func TestMembershipCorruptWindowFailsClosed(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	agent, _ := newC2Agent(t, dirA, dirB)
	ctx := context.Background()
	c, err := agent.clientFor(ctx)
	if err != nil {
		t.Fatal(err)
	}

	root := writePersistRoot(t, homePersist(c.baseURL, dirA, dirB),
		[]string{"w1"},
		map[string]string{"opencode.window.w1.dat": `{"tabs": 42}`})
	injectPersist(t, root)
	agent.invalidateProjectCache()
	if _, err := agent.projectWorktreeDirs(ctx, c); err == nil {
		t.Fatal("corrupt registered window persist must fail the catalog (no partial publish)")
	}
}

// TestMembershipDedupedDirLimitFailsClosed covers the merged-directory hard
// limit (plan §7): a home row with more than maxDesktopDedupedDirs distinct
// existing worktrees is a membership parse failure, not a truncated list.
func TestMembershipDedupedDirLimitFailsClosed(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	agent, _ := newC2Agent(t, dirA, dirB)
	c, err := agent.clientFor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	worktrees := make([]string, 0, maxDesktopDedupedDirs+1)
	for i := 0; i <= maxDesktopDedupedDirs; i++ {
		worktrees = append(worktrees, t.TempDir())
	}
	root := writePersistRoot(t, homePersist(c.baseURL, worktrees...), nil, nil)
	injectPersist(t, root)
	agent.invalidateProjectCache()

	_, err = agent.projectWorktreeDirs(context.Background(), c)
	if err == nil {
		t.Fatal("over-limit deduped directory set must fail closed")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListProjectSuggestionsUsesMembershipResolver(t *testing.T) {
	dirA, dirB, dirC := t.TempDir(), t.TempDir(), t.TempDir()
	agent, serve := newC2Agent(t, dirA, dirB)
	ctx := context.Background()
	c, err := agent.clientFor(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const sessionID = "ses_sugg"
	root := writePersistRoot(t, homePersist(c.baseURL, dirA, dirB),
		[]string{"w1"},
		map[string]string{"opencode.window.w1.dat": windowPersist(
			fmt.Sprintf(`[{"type":"session","server":"sidecar","sessionId":%q}]`, sessionID),
			fmt.Sprintf(`{%q:{"title":"t","directory":%q}}`, tabKey("sidecar", sessionID), dirC),
			"[]")})
	injectPersist(t, root)
	serve.responses["/session/"+sessionID] = fmt.Sprintf(
		`{"id":%q,"directory":%q,"title":"t","time":{"created":1,"updated":2}}`, sessionID, dirC)

	suggestions, err := agent.ListProjectSuggestions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sawC := false
	for _, s := range suggestions {
		if s.Directory == dirC {
			sawC = true
		}
	}
	if !sawC {
		t.Fatalf("suggestions must include the proof-authorized off-home directory, got %v", suggestions)
	}
}

// TestMembershipProofTransportFailureSkipsTab proves the transport-failure
// branch (plan §4: a wedged directory instance starves only its own tab).
// recordingServe cannot hang, so this uses a raw httptest server that never
// answers and a tiny per-tab timeout via a pre-expired context.
func TestMembershipProofTransportFailureSkipsTab(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	agent, _ := newC2Agent(t, dirA, dirB)
	c, err := agent.clientFor(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// A context that is already cancelled makes the proof's HTTP request fail
	// with a transport error without any network dependence.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	const sessionID = "ses_wedge"
	root := writePersistRoot(t, homePersist(c.baseURL, dirA, dirB),
		[]string{"w1"},
		map[string]string{"opencode.window.w1.dat": windowPersist(
			fmt.Sprintf(`[{"type":"session","server":"sidecar","sessionId":%q}]`, sessionID),
			fmt.Sprintf(`{%q:{"title":"t","directory":"/tmp/wedge"}}`, tabKey("sidecar", sessionID)),
			"[]")})
	injectPersist(t, root)
	agent.invalidateProjectCache()

	// The resolver deadline is also derived from the cancelled context, so
	// loadMemberProjectDirs returns the home-only set without error.
	dirs, err := agent.projectWorktreeDirs(ctx, c)
	if err != nil {
		t.Fatalf("transport-starved proof must skip, not fail: %v", err)
	}
	for _, d := range dirs {
		if d == "/tmp/wedge" {
			t.Fatalf("unproven tab must not authorize, dirs=%v", dirs)
		}
	}
}
