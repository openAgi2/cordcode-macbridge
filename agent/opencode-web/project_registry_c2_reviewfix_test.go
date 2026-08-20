package opencodeweb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// project_registry_c2_reviewfix_test.go owns the C2 strict generation-118
// /project decoder boundary (canonical §6.2 review-fix). The positive fixture
// is the corrected WP sample itself — every response replayed here comes from
// http[].response of the archived same-version capture, never from a
// hand-authored array.

const wpSamplePath = "testdata/official-1.18.18/samples/wp-workspace-project.sanitized.json"

// wpProjectResponses extracts the real GET /project response bodies from the
// corrected WP sample, mirroring check_workspace_project.py's http[]-only
// derivation (summaries/meta clones are never evidence).
func wpProjectResponses(t *testing.T) [][]byte {
	t.Helper()
	data, err := os.ReadFile(wpSamplePath)
	if err != nil {
		t.Fatalf("read WP sample: %v", err)
	}
	var doc struct {
		HTTP []struct {
			Method   string          `json:"method"`
			Path     string          `json:"path"`
			Status   int             `json:"status"`
			Response json.RawMessage `json:"response"`
		} `json:"http"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse WP sample: %v", err)
	}
	var out [][]byte
	for _, e := range doc.HTTP {
		if e.Method == "GET" && strings.HasSuffix(e.Path, "/project") && e.Status == 200 {
			out = append(out, e.Response)
		}
	}
	if len(out) == 0 {
		t.Fatal("WP sample contains no GET /project responses")
	}
	return out
}

// TestProjectRegistryReplaysCorrectedWPSample proves the strict decoder
// accepts every real archived /project response and derives the same registry
// facts the independent checker derived: global pseudo-project "/",
// two git-worktree rows, growth across responses, and the
// deleted-but-still-registered row (registry truth survives disk deletion).
func TestProjectRegistryReplaysCorrectedWPSample(t *testing.T) {
	responses := wpProjectResponses(t)
	type snap struct{ rows int; ids []string; worktrees []string }
	snaps := make([]snap, 0, len(responses))
	for i, raw := range responses {
		entries, err := decodeProjectRegistry(raw)
		if err != nil {
			t.Fatalf("real WP response %d must decode: %v", i, err)
		}
		s := snap{rows: len(entries)}
		for _, e := range entries {
			s.ids = append(s.ids, e.ID)
			s.worktrees = append(s.worktrees, e.Worktree)
		}
		snaps = append(snaps, s)
	}
	// First response: only the global pseudo-project.
	if snaps[0].rows != 1 || snaps[0].worktrees[0] != "/" {
		t.Fatalf("first response must be the global pseudo-project only, got %+v", snaps[0])
	}
	// Later responses: global + two git worktrees (growth), and the
	// deleted-on-disk worktree stays registered in the final response.
	last := snaps[len(snaps)-1]
	if last.rows != 3 {
		t.Fatalf("final response must carry 3 registered rows, got %+v", last)
	}
	hasGlobal, gitRows := false, 0
	for _, wt := range last.worktrees {
		if wt == "/" {
			hasGlobal = true
		} else if filepath.IsAbs(wt) {
			gitRows++
		}
	}
	if !hasGlobal || gitRows != 2 {
		t.Fatalf("final response = global + 2 git worktrees, got %+v", last)
	}
	// A registry row whose worktree no longer exists on disk is still a
	// decodable registry fact — hiding it is the visibility overlay's job,
	// never the decoder's.
	if _, err := os.Stat(last.worktrees[1]); err == nil {
		t.Logf("note: sanitized WP worktree %s unexpectedly exists locally", last.worktrees[1])
	}
}

// TestProjectRegistryRejectsNonBareArrayTopLevel: envelope, null, scalar, and
// object top levels all fail closed — the v2 {data:[…]} tolerance belongs to
// /session decodeListPayload only, never to /project.
func TestProjectRegistryRejectsNonBareArrayTopLevel(t *testing.T) {
	bad := map[string]string{
		"v2 envelope":       `{"data":[{"id":"p","worktree":"/x"}]}`,
		"null":              `null`,
		"scalar":            `42`,
		"string":            `"projects"`,
		"object":            `{"global":{"id":"global","worktree":"/"}}`,
		"empty":             ``,
		"truncated array":   `[{"id":"p","worktree":"/x"}`,
		"malformed array el": `[{"id":"p","worktree":"/x"},]`,
	}
	for name, payload := range bad {
		if _, err := decodeProjectRegistry([]byte(payload)); err == nil || !strings.Contains(err.Error(), "bare array") && !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("%s top level must fail closed, got %v (payload %s)", name, err, payload)
		}
	}
}

// TestProjectRegistryRejectsMalformedRows: non-object rows and
// missing/wrong/empty required id/worktree each fail the whole registry.
func TestProjectRegistryRejectsMalformedRows(t *testing.T) {
	bad := map[string]string{
		"non-object row (number)":  `[1]`,
		"non-object row (string)":  `["global"]`,
		"non-object row (null)":    `[null]`,
		"non-object row (array)":   `[[{"id":"g"}]]`,
		"missing id":               `[{"worktree":"/x"}]`,
		"empty id":                 `[{"id":"","worktree":"/x"}]`,
		"null id":                  `[{"id":null,"worktree":"/x"}]`,
		"wrong-type id":            `[{"id":7,"worktree":"/x"}]`,
		"missing worktree":         `[{"id":"g"}]`,
		"empty worktree":           `[{"id":"g","worktree":""}]`,
		"null worktree":            `[{"id":"g","worktree":null}]`,
		"wrong-type worktree":      `[{"id":"g","worktree":123}]`,
		"good row then bad row":    `[{"id":"g","worktree":"/"},{"id":""}]`,
		"row malformed after good": `[{"id":"g","worktree":"/"},{"id":}]`,
	}
	for name, payload := range bad {
		_, err := decodeProjectRegistry([]byte(payload))
		if err == nil {
			t.Fatalf("%s must fail closed, payload %s", name, payload)
		}
	}
	// Unknown extra fields stay allowed (WP rows carry time/sandboxes/vcs).
	if entries, err := decodeProjectRegistry([]byte(`[{"id":"g","worktree":"/","time":{"created":1},"sandboxes":[],"vcs":{"branch":"main"}}]`)); err != nil || len(entries) != 1 || entries[0].ID != "g" {
		t.Fatalf("unknown extra fields must stay allowed, got %+v err=%v", entries, err)
	}
}

// TestProjectRegistryMalformedShapeFailsList proves a malformed registry body
// fails the OD-2 global aggregation as a catalog error — it can never
// masquerade as an empty registry (which would be a silent empty list).
func TestProjectRegistryMalformedShapeFailsList(t *testing.T) {
	base := t.TempDir()
	s := &recordingServe{responses: map[string]string{
		"/global/health": `{"healthy":true}`,
		"/session":       `[]`,
		"/project":       `{"data":[{"id":"p","worktree":"/x"}]}`, // envelope must fail
	}}
	serverURL := s.start(t)
	aa, err := New(map[string]any{
		"work_dir":          base,
		"opencode_web_url":  serverURL,
		"opencode_web_user": "u",
		"opencode_web_pass": "pw",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := aa.(*Agent)
	t.Cleanup(func() { _ = a.Stop() })
	if _, err := a.ListSessions(context.Background()); err == nil || !strings.Contains(err.Error(), "project registry") {
		t.Fatalf("malformed registry shape must fail the global list as a registry error, got %v", err)
	}
	if _, err := a.ListProjectSuggestions(context.Background()); err == nil || !strings.Contains(err.Error(), "bare array") {
		t.Fatalf("malformed registry shape must fail project suggestions, got %v", err)
	}
}
