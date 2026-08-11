package gobridge

// checkpoint.go implements the Mac-side half of §6.1 "checkpoint 只读 diff": each
// completed turn, MacBridge captures a HIDDEN git ref snapshot of the agent's
// workspace so iOS can later fetch a per-turn or full-thread file diff (read-only).
//
// CRITICAL invariants (plan §3 防呆 + SSV2 guardrails, answered in the task's 七问):
//
//  1. The git object DB is ONLY a workspace file snapshot. It is NOT a session
//     truth source. Session truth always stays in the official CLI. Checkpoint
//     capture never writes projection/timeline content; the `turn_diff_ready`
//     event is control-plane only (SSV2 guardrail 8 enumerated exception) and
//     goes out via EventPublisher.PublishLogical — the single Kernel→EventPublisher
//     exit. It NEVER mutates reducer state.
//  2. The single writer of git refs is the coalescer goroutine (single-flight).
//     The ProjectionKernel.IngestLive hook only stages intent under the coalescer's
//     own mutex; git I/O runs ONLY in the goroutine, NEVER under k.mu (which would
//     block all session event delivery).
//  3. Failures are honest: non-git workspace → workspace_not_git (no ref written,
//     no fake snapshot); unsupported backend → checkpoint_unsupported; git failure
//     → no ref, diff unavailable. NO mock/placeholder/fallback.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// checkpointRefPrefix is the git ref namespace MacBridge owns for workspace
// snapshots. It deliberately avoids the reserved refs/heads|tags|remotes prefixes
// and is per-backend/per-session scoped so the same turn number from different
// sessions never collides.
const checkpointRefPrefix = "refs/cordcode/checkpoints"

// CheckpointMaxInterval mirrors the projection coalescer's MaxInterval. The
// coalescer merges a burst of turn_completed events (e.g. catch-up hydrate
// replaying several settled turns) into one git I/O sweep.
const CheckpointMaxInterval = 2 * time.Second

// CheckpointMaxRevisions keeps the most-recent N turn refs per session, aligning
// with ProjectionCheckpointPolicy.MaxRevisions. Pruning removes the oldest refs
// beyond this count.
const CheckpointMaxRevisions = 128

// CheckpointIOTimeout bounds every git invocation done by the coalescer so a
// wedged git process cannot hold the coalescer goroutine forever. All checkpoint
// git calls wrap context.WithTimeout (plan §6.1).
const CheckpointIOTimeout = 10 * time.Second

// gitEmptyTreeSHA is git's well-known empty-tree object SHA
// (4b825dc642cb6eb9a060e54bf8d69288fbee4904). It is used as the diff baseline for
// the first turn of a session (no prior ref exists). It has been stable across
// every git release since 2005 and is the canonical way to express "empty tree".
const gitEmptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// CheckpointFileSummary is the per-file {path,+/-} tuple carried by turn_diff_ready
// and returned by get_turn_diff / get_full_thread_diff. The RPC responses include
// the unified patch per file; the control-plane turn_diff_ready event deliberately
// omits the patch to keep the broadcast frame small (plan §6.1 "不含 full patch").
type CheckpointFileSummary struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Diff      string `json:"diff,omitempty"`
}

// CheckpointDiffResult is the wire shape returned by get_turn_diff and
// get_full_thread_diff.
type CheckpointDiffResult struct {
	Files         []CheckpointFileSummary `json:"files"`
	Additions     int                     `json:"additions"`
	Deletions     int                     `json:"deletions"`
	Truncated     bool                    `json:"truncated"`
	CheckpointRef string                  `json:"checkpointRef,omitempty"`
	FromRef       string                  `json:"fromRef,omitempty"`
}

// checkpointMaxEventFiles caps the per-file list embedded in the turn_diff_ready
// event so a turn that touches many files does not bloat the broadcast frame.
// Clients fetch the full list via get_turn_diff.
const checkpointMaxEventFiles = 50

// checkpointMaxDiffFiles caps the per-file list returned by the diff RPCs. Beyond
// this the response is marked truncated=true and the client can narrow scope.
const checkpointMaxDiffFiles = 500

// CheckpointWorkspaceResolver resolves the workspace directory and capture
// eligibility for a session. The ProjectionKernel calls Stage() after a turn
// completes; the coalescer goroutine later calls ResolveWorkspace just before git
// I/O. ResolveWorkspace MUST NOT perform git I/O (only the coalescer goroutine
// does git). Returning "" disables capture for that session (honest no-op).
type CheckpointWorkspaceResolver interface {
	// CaptureEnabled reports whether checkpoint capture is enabled for the
	// (backendID, sessionID) pair. In production this derives from the agent
	// implementing core.CheckpointProvider; tests inject a stub.
	CaptureEnabled(backendID, sessionID string) bool

	// ResolveWorkspace returns the absolute workspace directory for the session,
	// or "" if unknown. Sourced from sessionRegistry.directoryForSession.
	ResolveWorkspace(backendID, sessionID string) string
}

// checkpointIntent is a pending capture registered by the ProjectionKernel hook.
// The coalescer drains all pending intents per sweep.
type checkpointIntent struct {
	backendID string
	sessionID string
	turnN     int // 1-based TurnCount() of the just-completed turn
}

// checkpointCoalescer owns the git-checkpoint write path. It is deliberately
// SEPARATE from the projection coalescer (projection_kernel.go scheduleCheckpointLocked
// / startCheckpointWriteLocked): that path is SSV2-critical and tightly coupled
// to ProjectionCheckpoint / projectionCheckpointPersistence. Mirroring the same
// policy here (MaxInterval=2s, single-flight, time.AfterFunc merge) reuses the
// MECHANISM without perturbing the projection path (plan §6.1 "复用其 coalescer").
type checkpointCoalescer struct {
	mu sync.Mutex

	// intents groups pending captures by session so one sweep can drain a burst
	// of turn_completed events for the same session. Key: backendID + "\x00" + sessionID.
	intents map[string]checkpointIntent

	inflight bool
	timer    *time.Timer

	// notify is closed when a sweep finishes; tests use it to wait for capture.
	notify chan struct{}

	resolver CheckpointWorkspaceResolver
	publish func(LogicalEvent) // = h.publishEvent (Kernel→EventPublisher exit)
	now     func() time.Time

	// onCaptureSync, if non-nil, is invoked synchronously inside the goroutine
	// after each session's capture+emit completes. Used by the anti-double-write
	// and capture tests to observe results without sleeping.
	onCaptureSync func(backendID, sessionID string, turnN int, ref string, err error)
}

func newCheckpointCoalescer(resolver CheckpointWorkspaceResolver, publish func(LogicalEvent)) *checkpointCoalescer {
	return &checkpointCoalescer{
		intents:  make(map[string]checkpointIntent),
		notify:   make(chan struct{}),
		resolver: resolver,
		publish:  publish,
		now:      time.Now,
	}
}

// stage registers a pending capture intent. It is the only entry point the
// ProjectionKernel calls (via SetTurnCheckpointStager). It takes only the
// coalescer mutex — NEVER the kernel's k.mu — so it cannot block session event
// delivery. The git I/O happens later in the coalescer goroutine.
func (c *checkpointCoalescer) stage(backendID, sessionID string, turnN int) {
	if c == nil || backendID == "" || sessionID == "" || turnN <= 0 {
		return
	}
	c.mu.Lock()
	c.intents[backendID+"\x00"+sessionID] = checkpointIntent{
		backendID: backendID, sessionID: sessionID, turnN: turnN,
	}
	if !c.inflight && c.timer == nil {
		c.timer = time.AfterFunc(CheckpointMaxInterval, c.drain)
	}
	c.mu.Unlock()
}

// drain runs in a timer goroutine. It snapshots the pending intents, marks
// inflight, and spawns the capture sweep off any lock.
func (c *checkpointCoalescer) drain() {
	c.mu.Lock()
	if c.inflight {
		c.timer = nil
		c.mu.Unlock()
		return
	}
	pending := make([]checkpointIntent, 0, len(c.intents))
	for _, in := range c.intents {
		pending = append(pending, in)
	}
	clear(c.intents)
	c.timer = nil
	c.inflight = true
	resolver := c.resolver
	publish := c.publish
	c.mu.Unlock()

	// Sweep serially. Concurrent git writes against the same repo are
	// unnecessary (single writer); cross-session fan-out is not worth the
	// complexity for a per-turn capture path.
	for _, in := range pending {
		ref, err := c.captureAndEmit(resolver, publish, in)
		if err != nil {
			slog.Error("checkpoint capture failed",
				"backendID", in.backendID,
				"sessionID", in.sessionID,
				"turn", in.turnN,
				"ref", ref,
				"error", err,
			)
		}
		if c.onCaptureSync != nil {
			c.onCaptureSync(in.backendID, in.sessionID, in.turnN, ref, err)
		}
	}

	c.mu.Lock()
	c.inflight = false
	// If more intents arrived during the sweep, re-arm the timer.
	if len(c.intents) > 0 {
		c.timer = time.AfterFunc(CheckpointMaxInterval, c.drain)
	}
	// Wake any waiter.
	n := c.notify
	c.notify = make(chan struct{})
	close(n)
	c.mu.Unlock()
}

// captureAndEmit resolves the workspace, captures the ref, prunes, and emits
// turn_diff_ready. Returns the ref written ("" if none) and any error.
func (c *checkpointCoalescer) captureAndEmit(
	resolver CheckpointWorkspaceResolver,
	publish func(LogicalEvent),
	in checkpointIntent,
) (string, error) {
	if resolver == nil || !resolver.CaptureEnabled(in.backendID, in.sessionID) {
		return "", nil // capability off: honest no-op, no ref, no event
	}
	workspace := resolver.ResolveWorkspace(in.backendID, in.sessionID)
	if workspace == "" {
		return "", nil // unknown workspace: honest no-op (no mock snapshot)
	}
	root, ok := isGitWorkspace(workspace)
	if !ok {
		// Non-git workspace. Honest behavior: do not write a ref; do not emit a
		// turn_diff_ready. The RPC path surfaces workspace_not_git to the client.
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), CheckpointIOTimeout)
	defer cancel()

	ref, prevRef, err := captureCheckpointRef(ctx, root, in.backendID, in.sessionID, in.turnN)
	if err != nil {
		// Git failure: no ref written. Honest: surface via slog; do NOT emit a
		// fake turn_diff_ready. The RPC will report the error when queried.
		return "", err
	}
	// Prune oldest refs beyond the retention window. Best-effort; prune failure
	// does not negate a successful capture.
	pruneCheckpointRefs(ctx, root, in.backendID, in.sessionID, CheckpointMaxRevisions)

	// Compute the per-file summary for the just-completed turn (prevRef → ref).
	files, _, _, _ := diffCheckpoints(ctx, root, prevRef, ref, checkpointMaxEventFiles)
	eventFiles := make([]CheckpointFileSummary, 0, len(files))
	for _, f := range files {
		eventFiles = append(eventFiles, CheckpointFileSummary{
			Path:      f.Path,
			Additions: f.Additions,
			Deletions: f.Deletions,
		})
	}

	// Emit turn_diff_ready via PublishLogical — the single Kernel→EventPublisher
	// outlet. This is control-plane only: it does NOT mutate reducer state
	// (turn_diff_ready is absent from the reducer switch), so it cannot
	// double-write the timeline (SSV2 guardrail 3 / 8).
	if publish != nil {
			data := map[string]interface{}{
				"checkpointRef": ref,
				"turnNumber":    in.turnN,
				"files":         eventFiles,
				"truncated":     len(eventFiles) >= checkpointMaxEventFiles,
			}
		publish(LogicalEvent{
			BackendID: in.backendID,
			SessionID: in.sessionID,
			Event:     "turn_diff_ready",
			Data:      data,
			Broadcast: true,
			// Offline:false — transient activity notification. iOS that misses it
			// falls back to the get_turn_diff / get_full_thread_diff RPCs.
		})
	}
	return ref, nil
}

// checkpointRefName builds the git ref path for (backendID, sessionID, turnN).
// Format: refs/cordcode/checkpoints/<backendID>/turn/<N>/r<short>
// where short = hex(sha1(sessionID))[:12]. The sha1 hash gives cross-session
// namespace isolation: same turn number from different sessions lands in
// different r<short> branches and never collides. Same session + same turn is
// idempotent (update-ref overwrites), which is the expected behavior on replay.
func checkpointRefName(backendID, sessionID string, turnN int) string {
	sum := sha1.Sum([]byte(sessionID))
	short := hex.EncodeToString(sum[:])[:12]
	return fmt.Sprintf("%s/%s/turn/%d/r%s", checkpointRefPrefix, backendID, turnN, short)
}

// checkpointSessionPrefix is the glob/list prefix for all turn refs of one
// (backendID, sessionID). Used by prune + diff-range queries.
func checkpointSessionPrefix(backendID, sessionID string) string {
	sum := sha1.Sum([]byte(sessionID))
	short := hex.EncodeToString(sum[:])[:12]
	// for-each-ref glob: match refs/cordcode/checkpoints/<backendID>/turn/*/r<short>
	return fmt.Sprintf("%s/%s/turn/*/r%s", checkpointRefPrefix, backendID, short)
}

// captureCheckpointRef writes a working-tree snapshot into a fresh git commit and
// points `ref` at it. It uses a temp GIT_INDEX_FILE so the workspace's real
// index (.git/index) is never polluted. Returns (ref, prevRef, err): prevRef is
// the previous turn's ref ("" for turn 1), used by the caller to compute the
// per-turn diff.
func captureCheckpointRef(
	ctx context.Context,
	root, backendID, sessionID string,
	turnN int,
) (ref, prevRef string, err error) {
	ref = checkpointRefName(backendID, sessionID, turnN)
	if turnN > 1 {
		prevRef = checkpointRefName(backendID, sessionID, turnN-1)
		// Confirm the previous ref exists; if missing (e.g. prune ran), treat as
		// turn 1 baseline so the diff still has a base.
		if _, lerr := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)},
			"rev-parse", "--verify", "--quiet", prevRef+"^{commit}"); lerr != nil {
			prevRef = ""
		}
	}

	// Temp index: stage the working tree without touching .git/index. Reserve a
	// unique path via CreateTemp then immediately remove the file — git must
	// CREATE the index fresh (a 0-byte pre-existing file is rejected as "index
	// file smaller than expected"). The path stays stable and unique.
	tmpIndex, err := os.CreateTemp("", "cordcode-ckpt-index-*")
	if err != nil {
		return ref, prevRef, fmt.Errorf("create temp index: %w", err)
	}
	tmpPath := tmpIndex.Name()
	tmpIndex.Close()
	if err := os.Remove(tmpPath); err != nil {
		return ref, prevRef, fmt.Errorf("remove temp index placeholder: %w", err)
	}
	defer os.Remove(tmpPath)

	env := []string{"GIT_INDEX_FILE=" + tmpPath}
	opts := []gitRunOption{WithContext(ctx), WithEnv(env)}
	// `add -A` from an empty temp index stages every non-ignored file in the
	// working tree (modifications + untracked + deletions recorded as absence).
	// The resulting tree is a faithful workspace snapshot.
	if _, err := runGitInDirectoryWith(root, opts, "add", "-A"); err != nil {
		return ref, prevRef, fmt.Errorf("stage working tree: %w", err)
	}
	treeOut, err := runGitInDirectoryWith(root, opts, "write-tree")
	if err != nil {
		return ref, prevRef, fmt.Errorf("write-tree: %w", err)
	}
	treeSHA := strings.TrimSpace(treeOut)
	if treeSHA == "" {
		return ref, prevRef, fmt.Errorf("write-tree returned empty SHA")
	}
	// commit-tree needs author/committer identity. Set deterministic env so the
	// commit object is reproducible regardless of the user's git config.
	commitEnv := []string{
		"GIT_AUTHOR_NAME=CordCode Link",
		"GIT_AUTHOR_EMAIL=cordcode@localhost",
		"GIT_COMMITTER_NAME=CordCode Link",
		"GIT_COMMITTER_EMAIL=cordcode@localhost",
		"GIT_INDEX_FILE=" + tmpPath,
	}
	commitOpts := []gitRunOption{WithContext(ctx), WithEnv(commitEnv)}
	commitOut, err := runGitInDirectoryWith(root, commitOpts,
		"commit-tree", treeSHA, "-m",
		fmt.Sprintf("cordcode checkpoint %s/%s turn %d", backendID, sessionID, turnN))
	if err != nil {
		return ref, prevRef, fmt.Errorf("commit-tree: %w", err)
	}
	commitSHA := strings.TrimSpace(commitOut)
	if commitSHA == "" {
		return ref, prevRef, fmt.Errorf("commit-tree returned empty SHA")
	}
	// update-ref overwrites: same-session-same-turn recapture is idempotent.
	if _, err := runGitInDirectoryWith(root, opts, "update-ref", ref, commitSHA); err != nil {
		return ref, prevRef, fmt.Errorf("update-ref %s: %w", ref, err)
	}
	return ref, prevRef, nil
}

// diffCheckpoints computes the per-file numstat diff between two checkpoint refs.
// fromRef may be "" (→ git empty tree, used for turn 1). toRef must resolve to a
// commit. Returns the per-file summary (sorted by path), total additions/deletions,
// and a truncated flag (true if the file count hits the cap).
func diffCheckpoints(
	ctx context.Context,
	root, fromRef, toRef string,
	maxFiles int,
) ([]CheckpointFileSummary, int, int, bool) {
	from := strings.TrimSpace(fromRef)
	if from == "" {
		from = gitEmptyTreeSHA
	} else {
		from = from + "^{commit}"
	}
	to := toRef + "^{commit}"
	// -z + core.quotePath=false keeps non-ASCII paths raw instead of Git's
	// C-style octal escapes; --no-renames keeps paths stable across checkpoints.
	out, err := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)},
		"-c", "core.quotePath=false", "diff", "--no-ext-diff", "--no-renames", "--numstat", "-z", from, to)
	if err != nil {
		return nil, 0, 0, false
	}
	files := make([]CheckpointFileSummary, 0)
	add, del := 0, 0
	truncated := false
	count := 0
	for _, record := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		if record == "" {
			continue
		}
		// Binary files show "-\t-\tpath"; cap to 0.
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		a, _ := strconv.Atoi(parts[0])
		d, _ := strconv.Atoi(parts[1])
		path := parts[2]
		count++
		if maxFiles > 0 && count > maxFiles {
			truncated = true
			break
		}
		files = append(files, CheckpointFileSummary{Path: path, Additions: a, Deletions: d})
		add += a
		del += d
	}
	patches := unifiedDiffByPath(ctx, root, from, to)
	for i := range files {
		if p := patches[files[i].Path]; p != "" {
			files[i].Diff = p
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, add, del, truncated
}

// unifiedDiffByPath returns one unified patch per changed path between the two
// git revisions. It uses a single git invocation and splits on diff headers, so
// large turns do not spawn one git process per file.
func unifiedDiffByPath(ctx context.Context, root, from, to string) map[string]string {
	out, err := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)},
		"-c", "core.quotePath=false", "diff", "--no-ext-diff", "--no-renames", "--unified=3", from, to)
	if err != nil {
		return nil
	}
	patches := make(map[string]string)
	for _, block := range strings.Split(out, "\ndiff --git ") {
		if block == "" {
			continue
		}
		// The first split element still starts with "diff --git " because git
		// output begins with that header; later elements lost it to the split.
		header := block
		if !strings.HasPrefix(header, "diff --git ") {
			header = "diff --git " + header
		}
		if strings.Contains(header, "\nBinary files ") {
			continue
		}
		path := checkpointDiffPathFromHeader(header)
		if path == "" {
			continue
		}
		patches[path] = header
	}
	return patches
}

// checkpointDiffPathFromHeader extracts the new-side path from a "diff --git"
// header line. With --no-renames the old and new sides share one path, so the
// last " b/" separator is the boundary between them.
func checkpointDiffPathFromHeader(header string) string {
	line := strings.TrimSpace(strings.SplitN(header, "\n", 2)[0])
	line = strings.TrimPrefix(line, "diff --git ")
	if i := strings.LastIndex(line, " b/"); i >= 0 {
		return line[i+3:]
	}
	return ""
}

// pruneCheckpointRefs keeps only the most-recent `keep` turn refs for the session,
// deleting older ones. "Most-recent" = highest turn number embedded in the ref
// path (refs/.../turn/<N>/r<short>). Best-effort: prune errors are logged and do
// not fail the capture.
func pruneCheckpointRefs(ctx context.Context, root, backendID, sessionID string, keep int) {
	if keep <= 0 {
		return
	}
	prefix := checkpointSessionPrefix(backendID, sessionID)
	// for-each-ref with a format that yields "<turnN> <refname>"; we parse and
	// sort numerically. The glob matches refs/cordcode/checkpoints/<backendID>/turn/*/r<short>.
	out, err := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)},
		"for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return
	}
	type refTurn struct {
		ref  string
		turn int
	}
	refs := make([]refTurn, 0)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		turn, ok := turnNFromRef(line)
		if !ok {
			continue
		}
		refs = append(refs, refTurn{ref: line, turn: turn})
	}
	if len(refs) <= keep {
		return
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].turn < refs[j].turn })
	// Delete oldest beyond `keep`.
	toDelete := refs[:len(refs)-keep]
	for _, r := range toDelete {
		_, _ = runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)},
			"update-ref", "-d", r.ref)
	}
}

// turnNFromRef parses the turn number from a ref of the form
// refs/cordcode/checkpoints/<backendID>/turn/<N>/r<short>. Returns ok=false on
// any parse failure (defensive: a mangled ref is skipped, not deleted).
func turnNFromRef(ref string) (int, bool) {
	segments := strings.Split(ref, "/")
	// Expected layout: refs cordcode checkpoints <backendID> turn <N> r<short>
	for i := 0; i+2 < len(segments); i++ {
		if segments[i] == "turn" {
			n, err := strconv.Atoi(segments[i+1])
			if err == nil && n > 0 {
				return n, true
			}
		}
	}
	return 0, false
}

// checkpointLatestTurn returns the highest turn number captured for the session,
// or 0 if none. Used by get_full_thread_diff to find the latest ref.
func checkpointLatestTurn(ctx context.Context, root, backendID, sessionID string) (int, bool) {
	prefix := checkpointSessionPrefix(backendID, sessionID)
	out, err := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)},
		"for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return 0, false
	}
	max := 0
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if n, ok := turnNFromRef(strings.TrimSpace(line)); ok && n > max {
			max = n
		}
	}
	return max, max > 0
}

// checkpointHasTurn reports whether a given turn ref currently exists.
func checkpointHasTurn(ctx context.Context, root, backendID, sessionID string, turnN int) bool {
	ref := checkpointRefName(backendID, sessionID, turnN)
	_, err := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)},
		"rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// --- RPC handlers ----------------------------------------------------------

// checkpointCapabilityGated reports whether the agent supports checkpoint. Used
// by the RPC handlers to fast-fail with checkpoint_unsupported before any git I/O.
func (h *Handlers) checkpointCapabilityGated(agent core.Agent) bool {
	if cp, ok := agent.(core.CheckpointProvider); ok && cp.SupportsCheckpoint() {
		return true
	}
	return false
}

// resolveCheckpointWorkspace returns the workspace directory for the RPC, using
// the wire directory first (mirrors handleGetWorkspaceDiff) and the session
// registry as fallback. Returns ("", workspace_missing) when no directory can be
// resolved, and (dir, nil) after confirming the dir exists on disk.
func (h *Handlers) resolveCheckpointWorkspace(msg WireMessage, sessionID string) (string, *WireError) {
	workDir := extractDir(msg)
	if workDir == "" && sessionID != "" {
		workDir = h.sessions.directoryForSession(sessionID)
	}
	if workDir == "" {
		if agent, ok := h.firstAgentForBackend(msg.BackendID); ok {
			if wd, ok := agent.(core.WorkDirSwitcher); ok {
				workDir = wd.GetWorkDir()
			}
		}
	}
	if workDir == "" {
		return "", &WireError{Code: "workspace_missing", Message: "workspace directory is required"}
	}
	if err := validateGitDirectory(workDir); err != nil {
		return "", gitWireError("invalid_directory", err)
	}
	return workDir, nil
}

// handleGetTurnDiff implements the get_turn_diff RPC: returns the per-file diff
// for a specific completed turn (between ref(N-1) and ref(N)).
func (h *Handlers) handleGetTurnDiff(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		SessionID  string `json:"sessionId"`
		TurnNumber int    `json:"turnNumber"`
		Directory  string `json:"directory,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
		return
	}
	if !h.checkpointCapabilityGated(agent) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "checkpoint_unsupported", Message: "backend does not support checkpoint"})
		return
	}
	workDir, werr := h.resolveCheckpointWorkspace(msg, params.SessionID)
	if werr != nil {
		conn.SendResult(msg.RequestID, nil, werr)
		return
	}
	root, ok := isGitWorkspace(workDir)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "workspace_not_git", Message: "workspace is not a git repository"})
		return
	}
	if params.TurnNumber <= 0 {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: "turnNumber must be >= 1"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), CheckpointIOTimeout)
	defer cancel()
	if !checkpointHasTurn(ctx, root, msg.BackendID, params.SessionID, params.TurnNumber) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "checkpoint_not_found", Message: fmt.Sprintf("no checkpoint for turn %d", params.TurnNumber)})
		return
	}
	ref := checkpointRefName(msg.BackendID, params.SessionID, params.TurnNumber)
	var prevRef string
	if params.TurnNumber > 1 {
		prevRef = checkpointRefName(msg.BackendID, params.SessionID, params.TurnNumber-1)
		if !checkpointHasTurn(ctx, root, msg.BackendID, params.SessionID, params.TurnNumber-1) {
			prevRef = ""
		}
	}
	files, add, del, trunc := diffCheckpoints(ctx, root, prevRef, ref, checkpointMaxDiffFiles)
	conn.SendResult(msg.RequestID, CheckpointDiffResult{
		Files:         files,
		Additions:     add,
		Deletions:     del,
		Truncated:     trunc,
		CheckpointRef: ref,
		FromRef:       prevRef,
	}, nil)
}

// handleGetFullThreadDiff implements the get_full_thread_diff RPC: returns the
// aggregate per-file diff from the earliest captured turn to the latest.
func (h *Handlers) handleGetFullThreadDiff(conn Connection, msg WireMessage, agent core.Agent) {
	var params struct {
		SessionID string `json:"sessionId"`
		Directory string `json:"directory,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "invalid_params", Message: err.Error()})
		return
	}
	if !h.checkpointCapabilityGated(agent) {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "checkpoint_unsupported", Message: "backend does not support checkpoint"})
		return
	}
	workDir, werr := h.resolveCheckpointWorkspace(msg, params.SessionID)
	if werr != nil {
		conn.SendResult(msg.RequestID, nil, werr)
		return
	}
	root, ok := isGitWorkspace(workDir)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "workspace_not_git", Message: "workspace is not a git repository"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), CheckpointIOTimeout)
	defer cancel()
	latest, ok := checkpointLatestTurn(ctx, root, msg.BackendID, params.SessionID)
	if !ok {
		conn.SendResult(msg.RequestID, nil, &WireError{Code: "checkpoint_not_found", Message: "no checkpoints for session"})
		return
	}
	// Earliest captured turn forms the baseline. If only one turn exists, the
	// baseline falls back to the empty tree (shows that turn's files as added).
	earliest, found := checkpointEarliestTurn(ctx, root, msg.BackendID, params.SessionID)
	var fromRef string
	if found && earliest < latest {
		fromRef = checkpointRefName(msg.BackendID, params.SessionID, earliest)
	}
	toRef := checkpointRefName(msg.BackendID, params.SessionID, latest)
	files, add, del, trunc := diffCheckpoints(ctx, root, fromRef, toRef, checkpointMaxDiffFiles)
	conn.SendResult(msg.RequestID, CheckpointDiffResult{
		Files:         files,
		Additions:     add,
		Deletions:     del,
		Truncated:     trunc,
		CheckpointRef: toRef,
		FromRef:       fromRef,
	}, nil)
}

// checkpointEarliestTurn returns the lowest turn number captured for the session.
func checkpointEarliestTurn(ctx context.Context, root, backendID, sessionID string) (int, bool) {
	prefix := checkpointSessionPrefix(backendID, sessionID)
	out, err := runGitInDirectoryWith(root, []gitRunOption{WithContext(ctx)},
		"for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return 0, false
	}
	min := 0
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if n, ok := turnNFromRef(strings.TrimSpace(line)); ok {
			if min == 0 || n < min {
				min = n
			}
		}
	}
	return min, min > 0
}

// firstAgentForBackend returns the registered agent for msg.BackendID. Used by
// the RPC handlers' workspace fallback.
func (h *Handlers) firstAgentForBackend(backendID string) (core.Agent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Backend id may map to a registered agent name. Try exact key first, then
	// the canonical claude/claudecode aliasing.
	if a, ok := h.agents[backendID]; ok {
		return a, true
	}
	for _, a := range h.agents {
		if a.Name() == backendID {
			return a, true
		}
	}
	return nil, false
}

// --- Filepath helpers ------------------------------------------------------

// checkpointTempIndexDir is exported as a helper for tests that need to assert the
// temp index lives outside the repo (it does: os.CreateTemp uses os.TempDir).
func checkpointTempIndexDir() string {
	return filepath.Join(os.TempDir())
}

// --- Production resolver ---------------------------------------------------

// handlersCheckpointResolver is the production CheckpointWorkspaceResolver backed
// by Handlers' session registry + agent map. CaptureEnabled derives from the
// backend's agent implementing core.CheckpointProvider; ResolveWorkspace reads
// sessionRegistry.directoryForSession. Neither method performs git I/O — git runs
// only in the coalescer goroutine via isGitWorkspace/captureCheckpointRef.
//
// NOTE on cold/external sessions: sessionRegistry.directory is populated when a
// session is started/resumed through THIS bridge process (handleSendMessage →
// putSessionWithMeta). A purely external session (e.g. Claude turn started in
// another Terminal, observed only via file-relay/hydrate) may have an empty
// directory entry; in that case ResolveWorkspace returns "" and capture honestly
// no-ops (no mock snapshot). Enriching the resolver to fall back to the Claude
// session catalog is a follow-up; §6.1 ships with the registry-backed path.
type handlersCheckpointResolver struct {
	h *Handlers
}

func (r *handlersCheckpointResolver) CaptureEnabled(backendID, sessionID string) bool {
	if r == nil || r.h == nil {
		return false
	}
	r.h.mu.Lock()
	agent, ok := r.h.agents[backendID]
	if !ok {
		// Allow canonical name aliasing (claude vs claudecode) when the backend
		// id was not registered verbatim.
		for _, a := range r.h.agents {
			if a.Name() == backendID {
				agent = a
				ok = true
				break
			}
		}
	}
	r.h.mu.Unlock()
	if !ok {
		return false
	}
	if cp, ok := agent.(core.CheckpointProvider); ok {
		return cp.SupportsCheckpoint()
	}
	return false
}

func (r *handlersCheckpointResolver) ResolveWorkspace(backendID, sessionID string) string {
	if r == nil || r.h == nil || sessionID == "" {
		return ""
	}
	return r.h.sessions.directoryForSession(sessionID)
}
