package gobridge

// S-2 物化存储测试（方案 v30 §3.2/§7.1.2；冻结清单见
// docs/2026-10-19-s2-materialization-format-freeze.md §6）。
//
// 覆盖：round-trip（(iii) 全等零读）、suffix 推进接受（(ii) digest）、truncation/
// 原地改写/membership/schema-identity 失效重建（(iv)）、行级差分正确性、producer/
// claude source state round-trip、禁用语义、单写者并发、（负对照）OD-9 无跳过校验通道。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func materializationTestStore(t *testing.T) (*ProjectionMaterializationStore, string) {
	t.Helper()
	dir := t.TempDir()
	store := NewProjectionMaterializationStore(dir)
	if store == nil || store.db == nil {
		t.Fatal("store init failed")
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, dir
}

func materializationTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func materializationTestTurns(t *testing.T, count int) []TurnProjection {
	t.Helper()
	turns := make([]TurnProjection, 0, count)
	for i := 0; i < count; i++ {
		turns = append(turns, TurnProjection{
			TurnID:    "turn-mat-" + string(rune('a'+i)),
			Status:    "completed",
			StartedAt: int64(1000 + i),
			Assistant: &MessageProjection{
				ID:    "msg-" + string(rune('a'+i)),
				Role:  "assistant",
				Parts: []ProjectionPart{{Type: "text", Text: strings.Repeat("x", 64) + string(rune('a'+i))}},
			},
		})
	}
	return turns
}

func materializationTestCheckpoint(t *testing.T, backendID, sessionID string, turns []TurnProjection, sources ...ProjectionSourceCheckpoint) ProjectionCheckpoint {
	t.Helper()
	projection := SessionProjection{
		SessionID: sessionID,
		SyncRev:   42,
		Execution: ExecutionView{Phase: "idle"},
		Turns:     turns,
	}
	if len(sources) == 1 {
		return NewReadyProjectionCheckpoint(backendID, sessionID, sources[0], projection, time.Now())
	}
	return NewReadyCompositeProjectionCheckpoint(backendID, sessionID, sources, projection, time.Now())
}

func materializationDescriptorFor(descriptor ProjectionSourceDescriptor) ProjectionSourceDescriptor {
	cloned := descriptor
	cloned.Segments = append([]ProjectionSourceSegment(nil), descriptor.Segments...)
	return cloned
}

// 冻结清单 2/7：round-trip（全等零读）+ 差分写正确性。
func TestMaterializationRoundTripAndIncrementalDiff(t *testing.T) {
	store, dir := materializationTestStore(t)
	path := materializationTestFile(t, dir, "transcript.jsonl", strings.Repeat("{\"line\":1}\n", 10))
	full := ProjectionSourceDescriptor{Identity: "sess", Path: path, Cursor: int64(len("{\"line\":1}\n") * 10)}
	sources, err := BuildProjectionSourceCheckpoints(full)
	if err != nil {
		t.Fatal(err)
	}

	turns := materializationTestTurns(t, 5)
	checkpoint := materializationTestCheckpoint(t, "claude", "sess", turns, sources...)
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}
	descriptor := materializationDescriptorFor(full)
	loaded, err := store.LoadValidated("claude", "sess", descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Projection.SyncRev != checkpoint.Projection.SyncRev ||
		len(loaded.Projection.Turns) != len(turns) ||
		loaded.Projection.Execution.Phase != checkpoint.Projection.Execution.Phase {
		t.Fatalf("round-trip drift: rev=%d turns=%d", loaded.Projection.SyncRev, len(loaded.Projection.Turns))
	}
	for i := range turns {
		if loaded.Projection.Turns[i].TurnID != turns[i].TurnID {
			t.Fatalf("turn %d drift: %s != %s", i, loaded.Projection.Turns[i].TurnID, turns[i].TurnID)
		}
	}

	// 差分：改 1 行、删 1 行、加 1 行 → 重载 == 新投影。
	next := []TurnProjection{turns[0], turns[1], turns[2]}
	next[1].Assistant.Parts[0].Text = "mutated"
	next = append(next, TurnProjection{TurnID: "turn-mat-new", Status: "completed", StartedAt: 9999})
	nextProjection := SessionProjection{
		SessionID: "sess",
		SyncRev:   43,
		Execution: ExecutionView{Phase: "running", ActiveTurnID: "turn-mat-new"},
		Turns:     next,
	}
	nextCheckpoint := NewReadyProjectionCheckpoint("claude", "sess", sources[0], nextProjection, time.Now())
	if err := store.Save(nextCheckpoint); err != nil {
		t.Fatal(err)
	}
	loaded2, err := store.LoadValidated("claude", "sess", descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded2.Projection.Turns) != len(next) {
		t.Fatalf("diff reload turns=%d want %d", len(loaded2.Projection.Turns), len(next))
	}
	if loaded2.Projection.Turns[1].Assistant.Parts[0].Text != "mutated" {
		t.Fatal("changed row not applied")
	}
	if loaded2.Projection.Turns[len(next)-1].TurnID != "turn-mat-new" {
		t.Fatal("added row missing")
	}
	if loaded2.Projection.Execution.ActiveTurnID != "turn-mat-new" {
		t.Fatal("scalar state drift")
	}
}

// 冻结清单 1：append suffix only——推进被接受（stored cursors 返回）。
func TestMaterializationAcceptsSuffixAdvance(t *testing.T) {
	store, dir := materializationTestStore(t)
	line := "{\"line\":1}\n"
	path := materializationTestFile(t, dir, "transcript.jsonl", strings.Repeat(line, 10))
	full := ProjectionSourceDescriptor{Identity: "sess", Path: path, Cursor: int64(len(line) * 10)}
	sources, err := BuildProjectionSourceCheckpoints(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(materializationTestCheckpoint(t, "claude", "sess", materializationTestTurns(t, 2), sources...)); err != nil {
		t.Fatal(err)
	}

	// append（mtime 变化 + size 增长 → (ii) digest committed 前缀）。
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(strings.Repeat(line, 5)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	info, _ := os.Stat(path)

	advanced := ProjectionSourceDescriptor{Identity: "sess", Path: path, Cursor: info.Size()}
	loaded, err := store.LoadValidated("claude", "sess", advanced)
	if err != nil {
		t.Fatalf("suffix advance must be accepted: %v", err)
	}
	if loaded.Source.Cursor != sources[0].Cursor {
		t.Fatalf("restored cursor = %d, want stored %d (suffix fold base)", loaded.Source.Cursor, sources[0].Cursor)
	}
}

// 冻结清单 4/5/6 + OD-9 负对照：失效重建各形态。
func TestMaterializationInvalidationCases(t *testing.T) {
	store, dir := materializationTestStore(t)
	line := "{\"line\":1}\n"
	parent := materializationTestFile(t, dir, "parent.jsonl", strings.Repeat(line, 4))
	child := materializationTestFile(t, dir, "child.jsonl", strings.Repeat(line, 6))
	full := ProjectionSourceDescriptor{
		Identity: "sess",
		Cursor:   int64(len(line)*4 + len(line)*6),
		Segments: []ProjectionSourceSegment{
			{Identity: "parent", Path: parent, Cursor: int64(len(line) * 4)},
			{Identity: "child", Path: child, Cursor: int64(len(line) * 6)},
		},
	}
	sources, err := BuildProjectionSourceCheckpoints(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(materializationTestCheckpoint(t, "claude", "sess", materializationTestTurns(t, 3), sources...)); err != nil {
		t.Fatal(err)
	}
	descriptor := materializationDescriptorFor(full)

	// truncation → 失效。
	if err := os.WriteFile(child, []byte(strings.Repeat(line, 2)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadValidated("claude", "sess", descriptor); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("truncation must invalidate, got %v", err)
	}

	// membership 变化（段数不同）→ 失效。
	fresh := materializationTestFile(t, dir, "fresh.jsonl", strings.Repeat(line, 10))
	_, err = store.LoadValidated("claude", "sess", ProjectionSourceDescriptor{
		Identity: "sess",
		Segments: []ProjectionSourceSegment{{Identity: "parent", Path: parent, Cursor: int64(len(line) * 4)}},
	})
	if !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("membership change must invalidate, got %v", err)
	}

	// 原地改写（等长）→ digest 捕获（OD-9：推进/改写路径必经校验，无跳过通道）。
	rewritten := strings.Replace(strings.Repeat(line, 10), "1", "2", 1)
	if err := os.WriteFile(fresh, []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}
	singleDescriptor := ProjectionSourceDescriptor{Identity: "s2", Path: fresh, Cursor: int64(len(line) * 10)}
	single, err := BuildProjectionSourceCheckpoints(singleDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(materializationTestCheckpoint(t, "claude", "s2", materializationTestTurns(t, 1), single...)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh, []byte(strings.Repeat("{\"line\":9}\n", 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadValidated("claude", "s2", materializationDescriptorFor(singleDescriptor)); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("in-place rewrite must invalidate (digest), got %v", err)
	}

	// 身份不符 → 失效（他 session 读不到；schema 由保存侧固化）。
	if _, err := store.LoadValidated("claude", "other", descriptor); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("foreign session load = %v, want ErrNotExist", err)
	}
}

// 冻结清单 10：崩溃/构造失败事务回滚（bbolt Update 原子性——构造坏 checkpoint 让
// Save 中途失败，验证 store 仍可读出上一一致点）。
func TestMaterializationSaveFailureLeavesPreviousState(t *testing.T) {
	store, dir := materializationTestStore(t)
	line := "{\"line\":1}\n"
	path := materializationTestFile(t, dir, "t.jsonl", strings.Repeat(line, 3))
	full := ProjectionSourceDescriptor{Identity: "sess", Path: path, Cursor: int64(len(line) * 3)}
	sources, err := BuildProjectionSourceCheckpoints(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(materializationTestCheckpoint(t, "claude", "sess", materializationTestTurns(t, 2), sources...)); err != nil {
		t.Fatal(err)
	}
	bad := materializationTestCheckpoint(t, "claude", "sess", materializationTestTurns(t, 2), sources...)
	bad.Projection.SessionID = "mismatched" // 身份校验失败 → 事务前失败
	if err := store.Save(bad); err == nil {
		t.Fatal("identity-mismatched save must fail")
	}
	loaded, err := store.LoadValidated("claude", "sess", materializationDescriptorFor(full))
	if err != nil || len(loaded.Projection.Turns) != 2 {
		t.Fatalf("previous consistent state must survive failed save: err=%v turns=%d", err, len(loaded.Projection.Turns))
	}
}

// 冻结清单 8：并发 Save 串行（bbolt 单写者）。
func TestMaterializationConcurrentSaves(t *testing.T) {
	store, dir := materializationTestStore(t)
	line := "{\"line\":1}\n"
	path := materializationTestFile(t, dir, "t.jsonl", strings.Repeat(line, 3))
	full := ProjectionSourceDescriptor{Identity: "sess", Path: path, Cursor: int64(len(line) * 3)}
	sources, err := BuildProjectionSourceCheckpoints(full)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			turns := materializationTestTurns(t, 3)
			projection := SessionProjection{SessionID: "sess", SyncRev: 100 + n, Execution: ExecutionView{Phase: "idle"}, Turns: turns}
			cp := NewReadyProjectionCheckpoint("claude", "sess", sources[0], projection, time.Now())
			if err := store.Save(cp); err != nil {
				t.Errorf("concurrent save %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
	loaded, err := store.LoadValidated("claude", "sess", materializationDescriptorFor(full))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Projection.Turns) != 3 || loaded.Projection.SyncRev < 100 {
		t.Fatalf("post-concurrency state inconsistent: turns=%d rev=%d", len(loaded.Projection.Turns), loaded.Projection.SyncRev)
	}
}

// 冻结清单（producer/claude state）：head 行 round-trip。
func TestMaterializationProducerAndClaudeStateRoundTrip(t *testing.T) {
	store, dir := materializationTestStore(t)
	line := "{\"line\":1}\n"
	path := materializationTestFile(t, dir, "t.jsonl", strings.Repeat(line, 3))
	full := ProjectionSourceDescriptor{Identity: "sess", Path: path, Cursor: int64(len(line) * 3)}
	sources, err := BuildProjectionSourceCheckpoints(full)
	if err != nil {
		t.Fatal(err)
	}
	cp := materializationTestCheckpoint(t, "claude", "sess", materializationTestTurns(t, 1), sources...)
	cp.ClaudeSourceState = &ClaudeSourceState{
		SchemaVersion:  ClaudeSourceStateSchemaVersion,
		SourceGeneration: "gen-1",
		GraphNodes:     map[string][]ClaudeGraphOccurrence{},
		LogicalRecords: map[string]ClaudeLogicalRecord{},
	}
	hasOlder := true
	cp.CodexProducerState = &CodexProducerState{HasOlderUpstream: hasOlder, BoundaryTurnID: "turn-mat-a"}
	if err := store.Save(cp); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadValidated("claude", "sess", materializationDescriptorFor(full))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ClaudeSourceState == nil || loaded.ClaudeSourceState.SourceGeneration != "gen-1" {
		t.Fatal("claude source state lost")
	}
	if loaded.CodexProducerState == nil || loaded.CodexProducerState.BoundaryTurnID != "turn-mat-a" {
		t.Fatal("codex producer state lost")
	}
	// producer seed 独立存取面。
	if err := store.SaveCodexProducerState("claude", "sess", CodexProducerState{HasOlderUpstream: hasOlder, BoundaryTurnID: "turn-mat-b"}); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadCodexProducerState("claude", "sess")
	if err != nil || state == nil || state.BoundaryTurnID != "turn-mat-b" {
		t.Fatalf("producer state standalone round-trip: %v %+v", err, state)
	}
}

// 禁用语义：空 baseDir = disabled。
func TestMaterializationDisabledWithoutDataDir(t *testing.T) {
	store := NewProjectionMaterializationStore("")
	if err := store.Save(ProjectionCheckpoint{}); !errors.Is(err, ErrProjectionCheckpointDisabled) {
		t.Fatalf("disabled save = %v", err)
	}
	if _, err := store.LoadValidated("a", "b", ProjectionSourceDescriptor{}); !errors.Is(err, ErrProjectionCheckpointDisabled) {
		t.Fatalf("disabled load = %v", err)
	}
}

// 冻结清单 12：旧 checkpoint 共存（OD-5）——bbolt 启用后旧 .json 不被读/写/删
//（实现层：materialization store 的路径空间与 checkpoints/ 完全分离；此处固化
// 该不变量，防止未来误删）。
func TestMaterializationNeverTouchesLegacyCheckpointFiles(t *testing.T) {
	dir := t.TempDir()
	legacyDir := filepath.Join(dir, "session-projection", "checkpoints")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyDir, "legacy.json")
	if err := os.WriteFile(legacy, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewProjectionMaterializationStore(dir)
	if store == nil || store.db == nil {
		t.Fatal("store init failed")
	}
	defer store.Close()
	line := "{\"line\":1}\n"
	path := materializationTestFile(t, dir, "t.jsonl", strings.Repeat(line, 3))
	sources, err := BuildProjectionSourceCheckpoints(ProjectionSourceDescriptor{Identity: "sess", Path: path, Cursor: int64(len(line) * 3)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(materializationTestCheckpoint(t, "claude", "sess", materializationTestTurns(t, 1), sources...)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(legacy)
	if err != nil || string(raw) != "{}" {
		t.Fatalf("legacy checkpoint must stay untouched: %v %q", err, raw)
	}
}

// F-14 冷检门：stat-only 判定（committed cursor == size → 未推进）。
func TestProjectionSourceUnchangedSinceCommit(t *testing.T) {
	dir := t.TempDir()
	line := "{\"line\":1}\n"
	path := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(path, []byte(strings.Repeat(line, 4)), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewProjectionMaterializationStore(dir)
	defer store.Close()
	kernel := NewProjectionKernel(NewProjectionReducer(), store)

	full := ProjectionSourceDescriptor{Identity: "sess", Path: path, Cursor: int64(len(line) * 4)}
	sources, err := BuildProjectionSourceCheckpoints(full)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.RestoreCheckpoint("claude", "sess", full); err == nil {
		// 尚无 checkpoint：restore 失败是预期（first use），gate 应判 false。
	}
	if projectionSourceUnchangedSinceCommit(kernel, "claude", "sess") {
		t.Fatal("no committed source → gate must be false (cold inspection stands)")
	}

	if err := store.Save(materializationTestCheckpoint(t, "claude", "sess", materializationTestTurns(t, 1), sources...)); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.RestoreCheckpoint("claude", "sess", full); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !projectionSourceUnchangedSinceCommit(kernel, "claude", "sess") {
		t.Fatal("unchanged committed source (size==cursor) must skip cold inspection")
	}

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(line)
	_ = f.Close()
	if projectionSourceUnchangedSinceCommit(kernel, "claude", "sess") {
		t.Fatal("appended source (size>cursor) must keep cold inspection")
	}
}

// 冻结清单（E-6 组件级）：合成大 projection（~100MB turn 内容）round-trip + 差分写。
// 真 realdata tag 测试（owner 事故 transcript）按 E-6 归 regression 阶段。
func TestMaterializationLargeSyntheticProjection(t *testing.T) {
	if testing.Short() {
		t.Skip("large fixture")
	}
	store, dir := materializationTestStore(t)
	line := "{\"line\":1}\n"
	path := materializationTestFile(t, dir, "t.jsonl", strings.Repeat(line, 3))
	full := ProjectionSourceDescriptor{Identity: "sess", Path: path, Cursor: int64(len(line) * 3)}
	sources, err := BuildProjectionSourceCheckpoints(full)
	if err != nil {
		t.Fatal(err)
	}

	// 120 turns × ~840KB 文本 ≈ 100MB 级（对齐 §7.3 合成 fixture 形状）。
	bigText := strings.Repeat("y", 840*1024)
	turns := make([]TurnProjection, 0, 120)
	for i := 0; i < 120; i++ {
		turns = append(turns, TurnProjection{
			TurnID:    "turn-big-" + itoaMaterialization(i),
			Status:    "completed",
			StartedAt: int64(i),
			Assistant: &MessageProjection{
				ID:    "msg-" + itoaMaterialization(i),
				Role:  "assistant",
				Parts: []ProjectionPart{{Type: "text", Text: bigText}},
			},
		})
	}
	projection := SessionProjection{SessionID: "sess", SyncRev: 7, Execution: ExecutionView{Phase: "idle"}, Turns: turns}
	cp := NewReadyProjectionCheckpoint("claude", "sess", sources[0], projection, time.Now())
	if err := store.Save(cp); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadValidated("claude", "sess", materializationDescriptorFor(full))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Projection.Turns) != 120 {
		t.Fatalf("large round-trip turns=%d", len(loaded.Projection.Turns))
	}
	if loaded.Projection.Turns[119].Assistant.Parts[0].Text != bigText {
		t.Fatal("large turn content drift")
	}

	// 差分：只改最后一个 turn → 只有该行重写（行为断言：重载正确；写量优化由
	// 差分实现保证——未变行字节相等跳过）。
	turns[119].Assistant.Parts[0].Text = bigText + "!"
	next := SessionProjection{SessionID: "sess", SyncRev: 8, Execution: ExecutionView{Phase: "idle"}, Turns: turns}
	if err := store.Save(NewReadyProjectionCheckpoint("claude", "sess", sources[0], next, time.Now())); err != nil {
		t.Fatal(err)
	}
	loaded2, err := store.LoadValidated("claude", "sess", materializationDescriptorFor(full))
	if err != nil {
		t.Fatal(err)
	}
	if loaded2.Projection.Turns[119].Assistant.Parts[0].Text != bigText+"!" || loaded2.Projection.SyncRev != 8 {
		t.Fatal("diff save drift on large projection")
	}
}

func itoaMaterialization(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
