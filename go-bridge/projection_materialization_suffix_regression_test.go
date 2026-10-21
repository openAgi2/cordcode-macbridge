package gobridge

// 2026-10-21 评审 R1 BLOCKER-1 / MAJOR-1 回归（docs/2026-10-21-long-session-
// projection-loading-session-code-review-r1.md）。
//
// BLOCKER-1：S-2 组合源「接受 cursor 推进 + 后缀折叠」对 Claude 不成立——段级
// 续读的行号派生 turn ID 从续读起点重新编号，与存量 turn 确定性碰撞 → 早期 turn
// 被新 prompt 覆盖、尾部 turn 虚构；损坏写回 checkpoint 并持续叠加（生产 18 次折叠
// 11 次分歧）。修复 = 恢复 main 严格语义（组合源任一段 cursor 不等 → Invalid 诚实
// 重建）+ materializationSchemaVersion bump 2 清掉损坏存档。本文件锁定：
//   1. 组合源段推进 → Invalid（单源 TestMaterializationAcceptsSuffixAdvance 语义不变）；
//   2. v1 schema head → Invalid（bump 对存量存档生效）；
//   3. handler 级：checkpoint 后 transcript 增长 → 重开结果与全量重建逐 turn 等价。
//
// MAJOR-1：d78100ee 扩语义把 CLI 本地命令回显 turn 计入 coverage → dead 进程下
// 合成 turn_aborted（2026-10-11 /model 修复回归）。修复 = coverage 豁免命令回显
// turn（commit 收尾结算 completed），悬挂 tool_use 等真正非终态仍在 coverage。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/openAgi2/cordcode-macbridge/agent/claudecode"
)
// --- BLOCKER-1 ①：组合源段 cursor 推进必须 Invalid（诚实重建），单源语义不变。 ---

func TestCompositeSuffixAdvanceInvalidatesCheckpoint(t *testing.T) {
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

	// child 段追加增长（组合源推进）。
	f, err := os.OpenFile(child, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(strings.Repeat(line, 2)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	info, err := os.Stat(child)
	if err != nil {
		t.Fatal(err)
	}

	advanced := ProjectionSourceDescriptor{
		Identity: "sess",
		Cursor:   int64(len(line)*4) + info.Size(),
		Segments: []ProjectionSourceSegment{
			{Identity: "parent", Path: parent, Cursor: int64(len(line) * 4)},
			{Identity: "child", Path: child, Cursor: info.Size()},
		},
	}
	if _, err := store.LoadValidated("claude", "sess", advanced); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("composite suffix advance must invalidate (BLOCKER-1), got %v", err)
	}
}

// --- BLOCKER-1 ②：v1 schema head 行（bump 前存量/可能已损坏）必须 Invalid。 ---

func TestMaterializationV1HeadInvalidatedBySchemaBump(t *testing.T) {
	store, dir := materializationTestStore(t)
	path := materializationTestFile(t, dir, "transcript.jsonl", strings.Repeat("{\"line\":1}\n", 10))
	full := ProjectionSourceDescriptor{Identity: "sess", Path: path, Cursor: int64(len("{\"line\":1}\n") * 10)}
	sources, err := BuildProjectionSourceCheckpoints(full)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := materializationTestCheckpoint(t, "claude", "sess", materializationTestTurns(t, 2), sources...)
	if err := store.Save(checkpoint); err != nil {
		t.Fatal(err)
	}
	descriptor := materializationDescriptorFor(full)
	if _, err := store.LoadValidated("claude", "sess", descriptor); err != nil {
		t.Fatalf("current-schema head must load, got %v", err)
	}

	// 手工把 head 行降级为 v1（模拟 2026-10-21 bump 前写入的存量存档——其中
	// 可能已包含折叠损坏的投影，schema bump 必须让它们全部失效走诚实重建）。
	if err := store.db.Update(func(tx *bolt.Tx) error {
		sessionBkt, err := materializationSessionBucket(tx, "claude", "sess", false)
		if err != nil || sessionBkt == nil {
			return fmt.Errorf("session bucket: %v", err)
		}
		raw := sessionBkt.Get([]byte(materializationHeadKey))
		if raw == nil {
			return fmt.Errorf("head row missing")
		}
		var head map[string]any
		if err := json.Unmarshal(raw, &head); err != nil {
			return err
		}
		head["schemaVersion"] = materializationSchemaVersion - 1
		downgraded, err := json.Marshal(head)
		if err != nil {
			return err
		}
		return sessionBkt.Put([]byte(materializationHeadKey), downgraded)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadValidated("claude", "sess", descriptor); !errors.Is(err, ErrProjectionCheckpointInvalid) {
		t.Fatalf("v1 head must be invalidated by schema bump, got %v", err)
	}
}

// --- BLOCKER-1 ③：handler 级——checkpoint 后 transcript 增长，重开必须等于全量重建。 ---

// suffixRegressionTurn 生成一对带行级 uuid 的 user/assistant 行（user 行无
// message.id——冷解析 ID 为行号派生，正是折叠重编号碰撞的形态）。
func suffixRegressionTurn(n int, prompt, answer string) []string {
	ts := func(sec int) string {
		return time.Date(2026, 10, 21, 11, n, sec, 0, time.UTC).Format(time.RFC3339)
	}
	return []string{
		`{"type":"user","uuid":"row-u` + strconv.Itoa(n) + `","timestamp":"` + ts(0) + `","message":{"role":"user","content":"` + prompt + `"}}`,
		`{"type":"assistant","uuid":"row-a` + strconv.Itoa(n) + `","timestamp":"` + ts(1) + `","message":{"id":"msg_` + strconv.Itoa(n) + `","role":"assistant","content":[{"type":"text","text":"` + answer + `"}],"stop_reason":"end_turn"}}`,
	}
}

func suffixRegressionTurnSummary(p SessionProjection) []string {
	out := make([]string, 0, len(p.Turns))
	for _, turn := range p.Turns {
		user := ""
		if turn.User != nil {
			for _, part := range turn.User.Parts {
				if part.Type == "text" {
					user = part.Text
					break
				}
			}
		}
		asst := ""
		if turn.Assistant != nil {
			var texts []string
			for _, part := range turn.Assistant.Parts {
				if part.Type == "text" {
					texts = append(texts, part.Text)
				}
			}
			asst = strings.Join(texts, "|")
		}
		out = append(out, fmt.Sprintf("%s status=%s user=%q asst=%q", turn.TurnID, turn.Status, user, asst))
	}
	return out
}

func suffixRegressionPull(t *testing.T, h *Handlers, agent *claudecode.Agent, sessionID, workDir string) SessionProjection {
	t.Helper()
	conn := &readFileCaptureConn{}
	params, _ := json.Marshal(map[string]interface{}{"sessionId": sessionID, "directory": workDir})
	h.handleGetSessionProjection(conn, WireMessage{
		RequestID: "r-suffix-regression", BackendID: "claude", Method: "get_session_projection", Params: params,
	}, agent)
	if conn.err != nil {
		t.Fatalf("projection pull failed: %+v", conn.err)
	}
	dataMap, ok := conn.data.(map[string]interface{})
	if !ok {
		t.Fatalf("data type %T", conn.data)
	}
	proj, ok := dataMap["projection"].(SessionProjection)
	if !ok {
		t.Fatalf("projection type %T", dataMap["projection"])
	}
	return proj
}

func TestClaudeRestartAfterGrowthMatchesFullRebuild(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := filepath.Join(home, "Projects", "suffix-regression")
	projectDir := filepath.Join(home, ".claude", "projects", encodeProjectKey(workDir))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const sessionID = "suffix-regression-session"
	path := filepath.Join(projectDir, sessionID+".jsonl")
	var lines []string
	lines = append(lines, suffixRegressionTurn(1, "first prompt", "first answer")...)
	lines = append(lines, suffixRegressionTurn(2, "second prompt", "second answer")...)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dataDir := t.TempDir()
	newHandlers := func(withDataDir bool) (*Handlers, *claudecode.Agent) {
		raw, err := claudecode.New(map[string]any{"work_dir": workDir})
		if err != nil {
			t.Fatal(err)
		}
		agent := raw.(*claudecode.Agent)
		h := NewHandlers()
		if withDataDir {
			h.SetDataDir(dataDir)
		}
		h.RegisterAgent("claudecode", agent)
		return h, agent
	}

	// Run 1：冷开 → hydrate commit → checkpoint 落 bbolt。
	h1, a1 := newHandlers(true)
	p1 := suffixRegressionPull(t, h1, a1, sessionID, workDir)
	if len(p1.Turns) != 2 {
		t.Fatalf("run1 turns=%d want 2: %v", len(p1.Turns), suffixRegressionTurnSummary(p1))
	}
	store, ok := h1.projectionKernel.store.(*ProjectionMaterializationStore)
	if !ok || store.db == nil {
		t.Fatalf("materialization store not installed: %T", h1.projectionKernel.store)
	}
	segments, okSeg := h1.projectionKernel.CommittedSourceSegments("claude", sessionID)
	if !okSeg {
		t.Fatal("no committed source segments")
	}
	var total int64
	for _, s := range segments {
		total += s.Cursor
	}
	source := ProjectionSourceDescriptor{Identity: sessionID, Cursor: total, Segments: segments}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := store.LoadValidated("claude", sessionID, source); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("checkpoint never persisted")
		}
		time.Sleep(20 * time.Millisecond)
	}
	h1.Shutdown(context.Background())
	_ = store.Close() // 释放 bbolt flock（等价进程退出）

	// 停机期间 transcript 增长（Mac 终端外部 turn / runtime 定时重启窗口）。
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range suffixRegressionTurn(3, "third prompt", "third answer") {
		_, _ = f.WriteString(line + "\n")
	}
	_ = f.Close()

	// Run 2：同 dataDir「重启」→ checkpoint 段 cursor 已推进 → 必须 Invalid →
	// 诚实全量重建（修复前：后缀折叠把第 3 轮折进 user-line-1，覆盖第 1 轮）。
	h2, a2 := newHandlers(true)
	p2 := suffixRegressionPull(t, h2, a2, sessionID, workDir)
	h2.Shutdown(context.Background())

	// 参照：无 dataDir（无 checkpoint）的全量重建。
	h3, a3 := newHandlers(false)
	p3 := suffixRegressionPull(t, h3, a3, sessionID, workDir)
	h3.Shutdown(context.Background())

	got := strings.Join(suffixRegressionTurnSummary(p2), "\n")
	want := strings.Join(suffixRegressionTurnSummary(p3), "\n")
	if got != want {
		t.Fatalf("restart-after-growth projection diverges from full rebuild (BLOCKER-1 regression):\ngot:\n  %s\nwant:\n  %s",
			strings.ReplaceAll(got, "\n", "\n  "), strings.ReplaceAll(want, "\n", "\n  "))
	}
	if len(p2.Turns) != 3 {
		t.Fatalf("turns=%d want 3", len(p2.Turns))
	}
}
