package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// §4.1 B Phase 1 commit_and_push 测试（无需真实 agent CLI / 无需 GitHub 联网）。
//
// 覆盖：
//   - clean 工作区 → nothing_to_commit（不 commit/push）。
//   - dirty + caller 提供 message + 本地 bare origin → commit 成功 + push 成功 + 返回 head/pushed。
//   - message 为空且 agent 无 CommitMessageGenerator → commit_message_generation_unsupported。
//   - message 为空且有 generator → 生成 message 后 commit+push（用 stub generator）。
//
// push 用本地 bare repo 作 origin，避免真实网络。
func TestCommitAndPush_NothingToCommitWhenClean(t *testing.T) {
	repo := makeGitRepository(t)
	h := &Handlers{}
	conn := newCaptureConn()
	msg := WireMessage{RequestID: "r1", Params: mustJSON(t, map[string]string{"directory": repo, "message": "x"})}
	h.handleCommitAndPush(conn, msg, &stubCommitAgent{stubAgentCheckpoint: stubAgentCheckpoint{name: "x"}, generated: "x"})

	if conn.lastErrCode != "nothing_to_commit" {
		t.Fatalf("errCode = %q, want nothing_to_commit (clean)", conn.lastErrCode)
	}
}

func TestCommitAndPush_CommitsAndPushesWithProvidedMessageToLocalBareOrigin(t *testing.T) {
	repo := makeGitRepository(t)
	origin := setupLocalBareOrigin(t, repo)

	// dirty：新增一个 untracked 文件
	if err := os.WriteFile(filepath.Join(repo, "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &Handlers{}
	conn := newCaptureConn()
	msg := WireMessage{RequestID: "r2", Params: mustJSON(t, map[string]string{"directory": repo, "message": "feat: add feature.go"})}
	h.handleCommitAndPush(conn, msg, &stubCommitAgent{stubAgentCheckpoint: stubAgentCheckpoint{name: "x"}, generated: "x"})

	if conn.lastErr != nil {
		t.Fatalf("unexpected error: %v (code=%s)", conn.lastErr, conn.lastErrCode)
	}
	var result struct {
		Head   string `json:"head"`
		Pushed bool   `json:"pushed"`
		Remote string `json:"remote"`
	}
	if err := json.Unmarshal(conn.lastResultJSON, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Head == "" {
		t.Fatal("head empty")
	}
	if !result.Pushed {
		t.Fatal("pushed = false, want true")
	}
	if !strings.HasPrefix(result.Remote, "origin/") {
		t.Fatalf("remote = %q, want origin/<branch>", result.Remote)
	}

	// 验证 bare origin 确实收到了 commit
	latestOnOrigin := runGitCapture(t, origin, "log", "--oneline", "-n", "1")
	if !strings.Contains(latestOnOrigin, "add feature.go") && !strings.Contains(latestOnOrigin, "feature.go") {
		t.Fatalf("origin latest = %q, want commit referencing feature.go", latestOnOrigin)
	}
}

func TestCommitAndPush_MessageGenerationUnsupportedWhenNoGenerator(t *testing.T) {
	repo := makeGitRepository(t)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &Handlers{}
	conn := newCaptureConn()
	// 空 message + agent 不实现 CommitMessageGenerator（stubNoCommitAgent）
	msg := WireMessage{RequestID: "r3", Params: mustJSON(t, map[string]string{"directory": repo, "message": ""})}
	h.handleCommitAndPush(conn, msg, &stubNoCommitAgent{stubAgentCheckpoint: stubAgentCheckpoint{name: "x"}})

	if conn.lastErrCode != "commit_message_generation_unsupported" {
		t.Fatalf("errCode = %q, want commit_message_generation_unsupported", conn.lastErrCode)
	}
}

func TestCommitAndPush_GeneratesMessageThenCommitsAndPushes(t *testing.T) {
	repo := makeGitRepository(t)
	origin := setupLocalBareOrigin(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "gen.txt"), []byte("gen\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &Handlers{}
	conn := newCaptureConn()
	agent := &stubCommitAgent{stubAgentCheckpoint: stubAgentCheckpoint{name: "x"}, generated: "feat: generated message"}
	msg := WireMessage{RequestID: "r4", Params: mustJSON(t, map[string]string{"directory": repo, "message": ""})}
	h.handleCommitAndPush(conn, msg, agent)

	if conn.lastErr != nil {
		t.Fatalf("unexpected error: %v (code=%s)", conn.lastErr, conn.lastErrCode)
	}
	if !agent.genCalled {
		t.Fatal("generator.GenerateCommitMessage not called")
	}
	latest := runGitCapture(t, repo, "log", "--oneline", "-n", "1")
	if !strings.Contains(latest, "generated message") {
		t.Fatalf("repo latest = %q, want generated message committed", latest)
	}
	latestOrigin := runGitCapture(t, origin, "log", "--oneline", "-n", "1")
	if !strings.Contains(latestOrigin, "generated message") {
		t.Fatalf("origin latest = %q, want generated message pushed", latestOrigin)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────

// setupLocalBareOrigin 创建一个 bare repo 并在 repo 里 add 为 origin + push 当前分支，
// 使后续 push 有真实 upstream（无需网络）。返回 bare repo 路径。
func setupLocalBareOrigin(t *testing.T, repo string) string {
	t.Helper()
	barePath := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(barePath, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTestCommand(t, barePath, "init", "--bare", "-b", "main")
	runGitTestCommand(t, repo, "remote", "add", "origin", barePath)
	runGitTestCommand(t, repo, "push", "-u", "origin", "main")
	return barePath
}

func runGitCapture(t *testing.T, directory string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", directory}, args...)
	cmd := exec.Command("git", full...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, buf.String())
	}
	return strings.TrimSpace(buf.String())
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// stubNoCommitAgent 满足 core.Agent（嵌入 stubAgentCheckpoint）但不实现 CommitMessageGenerator
//（触发 commit_message_generation_unsupported）。
type stubNoCommitAgent struct {
	stubAgentCheckpoint
}

// stubCommitAgent 满足 core.Agent + CommitMessageGenerator（指针 receiver 以便测试观察 genCalled）。
type stubCommitAgent struct {
	stubAgentCheckpoint
	generated string
	genCalled bool
}

func (s *stubCommitAgent) GenerateCommitMessage(ctx context.Context, input core.CommitMessageInput) (core.CommitMessage, error) {
	s.genCalled = true
	msg := s.generated
	if msg == "" {
		msg = "feat: stub generated"
	}
	return core.CommitMessage{Message: msg}, nil
}

// captureConn 实现 Connection 接口，记录 handler 发出的 result/error。
type captureConn struct {
	lastResultJSON []byte
	lastErr        *WireError
	lastErrCode    string
}

func newCaptureConn() *captureConn { return &captureConn{} }

func (c *captureConn) SendJSON(v any) {}

func (c *captureConn) SendResult(requestID string, data interface{}, err *WireError) {
	if err != nil {
		c.lastErr = err
		c.lastErrCode = err.Code
		return
	}
	if data == nil {
		c.lastResultJSON = []byte("null")
		return
	}
	b, _ := json.Marshal(data)
	c.lastResultJSON = b
}

func (c *captureConn) AuthedDevice() *TrustedDeviceRecord { return nil }
func (c *captureConn) RemoteAddr() string                  { return "test" }
func (c *captureConn) Close() error                         { return nil }
