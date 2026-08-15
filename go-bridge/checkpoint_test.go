package gobridge

// checkpoint_test.go covers the §6.1 directed unit tests required by the plan:
//   - ref capture: consecutive turn refs yield expected +/- via diffCheckpoints
//   - non-git workspace → honest workspace_not_git (no fake snapshot)
//   - ref retention: >128 turns → pruned to most-recent 128
//   - ref-format validity: check-ref-format accepts the generated ref for a
//     sessionID containing "/"
//   - capability gating: supports_checkpoint derives from CheckpointProvider;
//     supports_conversation_rollback from ConversationRollbackProvider; a backend
//     not implementing the interface → capability absent
//   - anti-double-write: checkpoint capture does not alter the reducer's
//     SessionProjection (SSV2 guardrail — turn_diff_ready is control-plane only)

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// --- Test fixtures / helpers -----------------------------------------------

// stubCheckpointResolver is a test-only CheckpointWorkspaceResolver.
type stubCheckpointResolver struct {
	enabled   bool
	workspace string
}

func (s *stubCheckpointResolver) CaptureEnabled(backendID, sessionID string) bool {
	return s.enabled
}

func (s *stubCheckpointResolver) ResolveWorkspace(backendID, sessionID string) string {
	return s.workspace
}

// stubAgentCheckpoint is a minimal Agent satisfying CheckpointProvider.
type stubAgentCheckpoint struct {
	name       string
	checkpoint bool
	rollback   bool
}

func (a *stubAgentCheckpoint) Name() string { return a.name }
func (a *stubAgentCheckpoint) StartSession(_ context.Context, _ string) (core.AgentSession, error) {
	return nil, nil
}
func (a *stubAgentCheckpoint) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	return nil, nil
}
func (a *stubAgentCheckpoint) Stop() error              { return nil }
func (a *stubAgentCheckpoint) SupportsCheckpoint() bool { return a.checkpoint }

// stubAgentRollback also satisfies ConversationRollbackProvider.
type stubAgentRollback struct {
	stubAgentCheckpoint
}

func (a *stubAgentRollback) RollbackConversationToTurn(_ context.Context, _ string, _ int) error {
	return nil
}

// initCheckpointRepo creates a git repo with one committed file to anchor HEAD.
func initCheckpointRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runTestGit(t, repo, "init", "-q")
	runTestGit(t, repo, "config", "user.email", "test@example.com")
	runTestGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repo, "add", "README.md")
	runTestGit(t, repo, "commit", "-qm", "initial")
	return repo
}

// writeTurnFileChange mutates a file in the repo to simulate a turn's work.
func writeTurnFileChange(t *testing.T, repo, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, path)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// captureTurn is a thin test helper that runs captureCheckpointRef for one turn.
func captureTurn(t *testing.T, repo, backendID, sessionID string, turnN int) (ref, prev string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), CheckpointIOTimeout)
	defer cancel()
	r, p, err := captureCheckpointRef(ctx, repo, backendID, sessionID, turnN)
	if err != nil {
		t.Fatalf("captureCheckpointRef turn %d: %v", turnN, err)
	}
	return r, p
}

// assertRefExists asserts the ref resolves to a commit.
func assertRefExists(t *testing.T, repo, ref string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), CheckpointIOTimeout)
	defer cancel()
	out, err := runGitInDirectoryWith(repo, []gitRunOption{WithContext(ctx)},
		"rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		t.Fatalf("ref %s does not exist: %v (out=%q)", ref, err, out)
	}
}

// --- Tests -----------------------------------------------------------------

// TestCheckpointCaptureTurnRefAndDiff: capturing two consecutive turn refs yields
// the expected per-file +/- via diffCheckpoints. Verifies the core capture/diff
// mechanics (temp GIT_INDEX_FILE, commit-tree, update-ref, diff <from>^{commit}
// <to>^{commit}).
func TestCheckpointCaptureTurnRefAndDiff(t *testing.T) {
	repo := initCheckpointRepo(t)

	// Turn 1: add a new file with 3 lines.
	writeTurnFileChange(t, repo, "src/a.go", "package src\n\n// a\n")
	ref1, prev1 := captureTurn(t, repo, "codex", "sess-double-slash", 1)
	if prev1 != "" {
		t.Fatalf("turn 1 prevRef = %q, want empty (no prior turn)", prev1)
	}
	assertRefExists(t, repo, ref1)

	// Turn 2: modify a.go (add 1 line, remove 1 line) + add b.go (2 lines).
	writeTurnFileChange(t, repo, "src/a.go", "package src\n\n// a changed\n")
	writeTurnFileChange(t, repo, "src/b.go", "package src\n\n// b\n")
	ref2, prev2 := captureTurn(t, repo, "codex", "sess-double-slash", 2)
	if prev2 == "" {
		t.Fatal("turn 2 prevRef empty, want turn-1 ref")
	}
	if prev2 != ref1 {
		t.Fatalf("turn 2 prevRef = %q, want %q", prev2, ref1)
	}
	assertRefExists(t, repo, ref2)

	// Diff turn 1 → turn 2 should show a.go net change and b.go added.
	ctx, cancel := context.WithTimeout(context.Background(), CheckpointIOTimeout)
	defer cancel()
	files, add, del, trunc := diffCheckpoints(ctx, repo, prev2, ref2, checkpointMaxDiffFiles)
	if trunc {
		t.Fatal("truncated=true unexpectedly")
	}
	byPath := map[string]CheckpointFileSummary{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if a, ok := byPath["src/a.go"]; !ok {
		t.Fatalf("src/a.go missing from diff: %#v", files)
	} else {
		// a.go: "-// a\n" removed, "+// a changed\n" added → +1 -1
		if a.Additions != 1 || a.Deletions != 1 {
			t.Fatalf("src/a.go = +%d -%d, want +1 -1", a.Additions, a.Deletions)
		}
	}
	if b, ok := byPath["src/b.go"]; !ok {
		t.Fatalf("src/b.go missing from diff: %#v", files)
	} else {
		// b.go is new: package src, blank, // b → 3 additions.
		if b.Additions != 3 || b.Deletions != 0 {
			t.Fatalf("src/b.go = +%d -%d, want +3 -0", b.Additions, b.Deletions)
		}
	}
	if add != 4 || del != 1 {
		t.Fatalf("turn2 totals = +%d -%d, want +4 -1", add, del)
	}
}

// TestCheckpointCaptureTurn1DiffFromEmptyTree: turn 1's diff (no prior ref) uses
// the empty-tree baseline and reports every workspace delta as additions.
func TestCheckpointCaptureTurn1DiffFromEmptyTree(t *testing.T) {
	repo := initCheckpointRepo(t)
	// Modify README (already committed) so there is a delta vs the captured snapshot.
	// Note: capture snapshots the WORKING TREE; turn 1 captures README's "init\n"
	// plus any new files at capture time. To make the turn-1 diff observable we
	// change README before capture so the diff(empty, ref1) shows README as the
	// current content (3 lines added vs empty tree).
	writeTurnFileChange(t, repo, "README.md", "line1\nline2\nline3\n")
	ref1, _ := captureTurn(t, repo, "codex", "sess-t1", 1)
	ctx, cancel := context.WithTimeout(context.Background(), CheckpointIOTimeout)
	defer cancel()
	files, add, _, _ := diffCheckpoints(ctx, repo, "", ref1, checkpointMaxDiffFiles)
	if len(files) == 0 {
		t.Fatal("turn-1 diff vs empty tree yielded no files")
	}
	// README.md should appear with 3 additions (it is the only file in the repo
	// and the captured tree contains its 3-line working-tree version).
	var readme *CheckpointFileSummary
	for i := range files {
		if files[i].Path == "README.md" {
			readme = &files[i]
		}
	}
	if readme == nil {
		t.Fatalf("README.md missing from turn-1 diff: %#v", files)
	}
	if readme.Additions != 3 {
		t.Fatalf("README.md additions = %d, want 3", readme.Additions)
	}
	if add == 0 {
		t.Fatal("turn-1 total additions = 0, want >0")
	}
}

// TestCheckpointDiffNonASCIIPathIncludesPatch verifies two fixes at once:
// non-ASCII paths stay raw UTF-8 instead of Git's octal escapes, and the
// turn/thread diff carries the unified patch per file.
func TestCheckpointDiffNonASCIIPathIncludesPatch(t *testing.T) {
	repo := initCheckpointRepo(t)
	runTestGit(t, repo, "config", "core.quotePath", "true")
	writeTurnFileChange(t, repo, "童话故事.txt", "童话故事内容\n第二行\n")
	ref1, _ := captureTurn(t, repo, "codex", "sess-unicode", 1)

	ctx, cancel := context.WithTimeout(context.Background(), CheckpointIOTimeout)
	defer cancel()
	files, _, _, _ := diffCheckpoints(ctx, repo, "", ref1, checkpointMaxDiffFiles)

	var found *CheckpointFileSummary
	for i := range files {
		if files[i].Path == "童话故事.txt" {
			found = &files[i]
		}
	}
	if found == nil {
		t.Fatalf("童话故事.txt missing from diff, got %#v", files)
	}
	if found.Additions != 2 || found.Deletions != 0 {
		t.Fatalf("童话故事.txt = +%d -%d, want +2 -0", found.Additions, found.Deletions)
	}
	if !strings.Contains(found.Diff, "童话故事内容") {
		t.Fatalf("diff missing unified patch content: %q", found.Diff)
	}
	if strings.Contains(found.Path, "\\") {
		t.Fatalf("path still contains git octal escapes: %q", found.Path)
	}
}

// TestCheckpointNonGitWorkspaceHonestUnsupported: a non-git workspace produces no
// ref and no error-masking snapshot. captureAndEmit returns ("", nil) and no ref
// is written anywhere.
func TestCheckpointNonGitWorkspaceHonestUnsupported(t *testing.T) {
	nonGit := t.TempDir() // no `git init`

	coalescer := newCheckpointCoalescer(
		&stubCheckpointResolver{enabled: true, workspace: nonGit},
		func(LogicalEvent) { t.Errorf("turn_diff_ready must not be emitted for non-git workspace") },
	)
	// Drive captureAndEmit directly (bypass the 2s coalescer timer).
	ref, err := coalescer.captureAndEmit(
		&stubCheckpointResolver{enabled: true, workspace: nonGit},
		func(LogicalEvent) { t.Errorf("no publish expected") },
		checkpointIntent{backendID: "codex", sessionID: "sess-ng", turnN: 1},
	)
	if err != nil {
		t.Fatalf("non-git capture returned error %v (expected silent honest no-op)", err)
	}
	if ref != "" {
		t.Fatalf("non-git capture wrote ref %q — must be empty", ref)
	}
	_ = coalescer // keep referenced
}

// TestCheckpointRetentionPrunesBeyond128: capturing >128 turns prunes to
// most-recent 128. Uses the prune logic directly (stubbed ref list via real git)
// to keep the test fast.
func TestCheckpointRetentionPrunesBeyond128(t *testing.T) {
	repo := initCheckpointRepo(t)
	ctx := context.Background()
	const total = 130

	// Capture 130 turns (each touches a file so each tree differs; not strictly
	// required for prune but makes the test realistic). Each capture is cheap
	// (temp index + write-tree + commit-tree + update-ref).
	for i := 1; i <= total; i++ {
		writeTurnFileChange(t, repo, filepath.Join("turns", "f.go"),
			"// turn "+strconv.Itoa(i)+"\n")
		r, _ := captureTurn(t, repo, "codex", "sess-prune", i)
		if r == "" {
			t.Fatalf("turn %d capture returned empty ref", i)
		}
	}

	// Prune to 128.
	pruneCheckpointRefs(ctx, repo, "codex", "sess-prune", CheckpointMaxRevisions)

	// Count remaining refs.
	prefix := checkpointSessionPrefix("codex", "sess-prune")
	out, err := runGitInDirectoryWith(repo, []gitRunOption{WithContext(ctx)},
		"for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		t.Fatalf("for-each-ref: %v", err)
	}
	remaining := 0
	maxTurn := 0
	minTurn := 0
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		remaining++
		if n, ok := turnNFromRef(line); ok {
			if n > maxTurn {
				maxTurn = n
			}
			if minTurn == 0 || n < minTurn {
				minTurn = n
			}
		}
	}
	if remaining != CheckpointMaxRevisions {
		t.Fatalf("after prune, remaining refs = %d, want %d", remaining, CheckpointMaxRevisions)
	}
	// The 128 most-recent (turns 3..130) should survive; turns 1..2 pruned.
	if minTurn != 3 {
		t.Fatalf("oldest surviving turn = %d, want 3 (pruned turns 1..2)", minTurn)
	}
	if maxTurn != total {
		t.Fatalf("newest surviving turn = %d, want %d", maxTurn, total)
	}
}

// itoa placeholder removed — tests use strconv.Itoa directly.

// TestCheckpointRefFormatValidity: the generated ref for a sessionID containing
// "/" (hashed → short hex) is accepted by `git check-ref-format --allow-onelevel`.
func TestCheckpointRefFormatValidity(t *testing.T) {
	ref := checkpointRefName("codex", "project/sub/session/with/slashes", 42)
	cmd := exec.Command("git", "check-ref-format", "--allow-onelevel", ref)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("check-ref-format rejected %q: %v: %s", ref, err, out)
	}
}

// TestCheckpointCapabilityGating: supports_checkpoint derives from
// CheckpointProvider type assertion; supports_conversation_rollback from
// ConversationRollbackProvider. A backend not implementing either → both absent.
func TestCheckpointCapabilityGating(t *testing.T) {
	// Agent implementing neither.
	plain := &stubAgentCheckpoint{name: "x"}
	caps := deriveBackendCapabilities("x", plain, "")
	if capContains(caps, "supports_checkpoint") || capContains(caps, "supports_conversation_rollback") {
		t.Fatalf("plain agent must not advertise checkpoint/rollback caps: %v", caps)
	}

	// Agent implementing CheckpointProvider only.
	withCp := &stubAgentCheckpoint{name: "x", checkpoint: true}
	caps = deriveBackendCapabilities("x", withCp, "")
	if !capContains(caps, "supports_checkpoint") {
		t.Fatalf("supports_checkpoint missing: %v", caps)
	}
	if capContains(caps, "supports_conversation_rollback") {
		t.Fatalf("supports_conversation_rollback must be absent without ConversationRollbackProvider: %v", caps)
	}

	// Agent implementing ConversationRollbackProvider (embeds stub).
	withRb := &stubAgentRollback{stubAgentCheckpoint{name: "x", checkpoint: true}}
	caps = deriveBackendCapabilities("x", withRb, "")
	if !capContains(caps, "supports_conversation_rollback") {
		t.Fatalf("supports_conversation_rollback missing: %v", caps)
	}
}

func capContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// TestCheckpointAntiDoubleWriteDoesNotAlterProjection: after a checkpoint capture,
// the reducer's SessionProjection for the session is unchanged. This proves
// turn_diff_ready / checkpoint code is control-plane only (SSV2 guardrail — no
// second projection writer). The reducer must hold the same TurnCount / SyncRev
// and identical turn identities before and after capture.
func TestCheckpointAntiDoubleWriteDoesNotAlterProjection(t *testing.T) {
	repo := initCheckpointRepo(t)
	writeTurnFileChange(t, repo, "src/x.go", "package src\n")

	r := newTestReducer()
	// Build a minimal completed turn in the reducer.
	r.Apply(ev(1, "codex", "sess-adw", "turn_started", map[string]interface{}{"turnId": "T1"}))
	r.Apply(ev(2, "codex", "sess-adw", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "hello"}))
	r.Apply(ev(3, "codex", "sess-adw", "turn_completed", map[string]interface{}{"turnId": "T1"}))

	before, ok := r.Snapshot("codex", "sess-adw")
	if !ok {
		t.Fatal("missing projection before capture")
	}
	beforeRev := before.SyncRev
	beforeTurns := len(before.Turns)
	beforeTurnID := ""
	if beforeTurns > 0 {
		beforeTurnID = before.Turns[0].TurnID
	}

	// Run the checkpoint capture against the same workspace the reducer models.
	// The capture code path MUST NOT touch the reducer.
	ctx, cancel := context.WithTimeout(context.Background(), CheckpointIOTimeout)
	defer cancel()
	ref, _, err := captureCheckpointRef(ctx, repo, "codex", "sess-adw", 1)
	if err != nil {
		t.Fatalf("captureCheckpointRef: %v", err)
	}
	if ref == "" {
		t.Fatal("expected non-empty ref")
	}

	// Emit a turn_diff_ready via the coalescer publisher and assert it does not
	// re-enter and mutate the reducer. We use a live reducer-attached publisher
	// to prove the end-to-end control-plane path is mutation-free.
	// Note: the production publisher attaches a ProjectionKernel; here we only
	// need to prove the EVENT itself is absent from the reducer switch. We
	// apply it directly and assert no rev change.
	r.Apply(ev(4, "codex", "sess-adw", "turn_diff_ready",
		map[string]interface{}{"checkpointRef": ref, "turnNumber": 1}))

	after, ok := r.Snapshot("codex", "sess-adw")
	if !ok {
		t.Fatal("missing projection after capture")
	}
	if after.SyncRev != beforeRev {
		t.Fatalf("SyncRev changed: %d → %d (checkpoint must not advance projection)", beforeRev, after.SyncRev)
	}
	if len(after.Turns) != beforeTurns {
		t.Fatalf("TurnCount changed: %d → %d", beforeTurns, len(after.Turns))
	}
	if beforeTurns > 0 && after.Turns[0].TurnID != beforeTurnID {
		t.Fatalf("turn id changed: %q → %q", beforeTurnID, after.Turns[0].TurnID)
	}
}

// TestCheckpointKernelHookStagesOnTurnCompleted: a full Kernel + coalescer wiring
// test. Driving IngestLive with a turn_completed event must stage an intent and,
// after the coalescer drains, write a ref + emit turn_diff_ready. Uses a stub
// resolver backed by a real temp git repo.
func TestCheckpointKernelHookStagesOnTurnCompleted(t *testing.T) {
	repo := initCheckpointRepo(t)
	writeTurnFileChange(t, repo, "src/y.go", "package src\n")

	resolver := &stubCheckpointResolver{enabled: true, workspace: repo}
	var (
		emittedMu sync.Mutex
		emitted   []string
	)
	coalescer := newCheckpointCoalescer(resolver, func(le LogicalEvent) {
		if le.Event != "turn_diff_ready" {
			return
		}
		emittedMu.Lock()
		emitted = append(emitted, le.Event)
		emittedMu.Unlock()
	})
	// Synchronous capture observation: close a chan when a sweep finishes.
	done := make(chan struct{}, 16)
	coalescer.onCaptureSync = func(backendID, sessionID string, turnN int, ref string, err error) {
		done <- struct{}{}
	}

	kernel := NewProjectionKernel(newTestReducer(), nil)
	kernel.SetTurnCheckpointStager(coalescer.stage)

	// Drive a real turn_completed through IngestLive.
	msg := ev(1, "codex", "sess-khook", "turn_started", map[string]interface{}{"turnId": "T1"})
	kernel.IngestLive(msg)
	msg2 := ev(2, "codex", "sess-khook", "text_delta", map[string]interface{}{"itemId": "T1", "delta": "hi"})
	kernel.IngestLive(msg2)
	msg3 := ev(3, "codex", "sess-khook", "turn_completed", map[string]interface{}{"turnId": "T1"})
	kernel.IngestLive(msg3)

	// The coalescer timer fires after CheckpointMaxInterval (2s). To keep the test
	// fast, drain() directly here. Production uses the timer.
	coalescer.drain()
	<-done

	// Assert a ref exists for turn 1.
	ctx, cancel := context.WithTimeout(context.Background(), CheckpointIOTimeout)
	defer cancel()
	ref := checkpointRefName("codex", "sess-khook", 1)
	if _, err := runGitInDirectoryWith(repo, []gitRunOption{WithContext(ctx)},
		"rev-parse", "--verify", ref+"^{commit}"); err != nil {
		t.Fatalf("kernel hook did not stage a capture (ref %s missing): %v", ref, err)
	}

	emittedMu.Lock()
	got := len(emitted)
	emittedMu.Unlock()
	if got != 1 {
		t.Fatalf("turn_diff_ready emitted %d times, want 1", got)
	}
}
