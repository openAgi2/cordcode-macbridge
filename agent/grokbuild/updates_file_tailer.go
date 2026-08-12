package grokbuild

// updates.jsonl file-watch fallback for live Grok session events.
//
// grok 的 leader socket (~/.grok/leader.sock) 只在 use_leader=true 时才存在;
// 默认 inline embedded agent 模式不创建它。SubscribeSessionEvents 的 leader
// 路径会因此 os.Stat 失败而无法观察外部 turn。但 grok 在所有模式下都把同样的
// session/update 通知以 JSON-RPC 行的形式追加写到
//   ~/.grok/sessions/<url-encoded-cwd>/<sessionId>/updates.jsonl
// 每行 shape 与 leader subscriber 收到的通知一致 ({method, params}),所以本
// tailer 直接复用 isSessionUpdateMethod / extractParams / isReplayUpdate /
// convertSessionUpdate——与 leader 路径走同一个 codec,下游 relay loop 的合成
// 逻辑 (turn_started / defer idle) 无需改动。
//
// 设计参照 codexSessionFileRelayLoop:从当前 EOF 起 tail 增量 (iOS 已通过
// get_session_messages load 过历史,不重放);truncate 重置;turn 完成后给一段
// grace 续看下一轮,超 grace 或绝对 hardCap 退出 (无 grok relay watcher,iOS
// 下次 poll 会经 startGrokLeaderSessionRelay 重启本 tailer)。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// 这几个旋钮用 var 而非 const,便于测试缩短轮询间隔 (对齐 codex relay 的可测试性做法)。
var (
	// grokUpdatesRelayPollInterval 是 updates.jsonl 增量轮询间隔,与 codex relay 对齐。
	grokUpdatesRelayPollInterval = 1 * time.Second
	// grokUpdatesRelayLateBindCap 是等待 grok 创建 session 目录 / updates.jsonl 的最长时间。
	grokUpdatesRelayLateBindCap = 2 * time.Minute
	// grokUpdatesRelayPostTurnGrace 是看到 turn_completed 后,无新增长时继续守候的时间窗,
	// 用来捕获紧邻的下一轮 turn;超时后退出 (iOS 下次 poll 会重启)。
	grokUpdatesRelayPostTurnGrace = 90 * time.Second
	// grokUpdatesRelayHardCap 是无论是否仍有增长都必须退出的绝对上限,防止 goroutine 永久滞留。
	grokUpdatesRelayHardCap = 30 * time.Minute
)

// updatesFileTailSubscriber tails one grok session's updates.jsonl as a read-only
// fallback for LeaderSubscriber. It does NOT spawn grok or drive the session — it
// only parses appended session/update lines through the shared codec.
type updatesFileTailSubscriber struct {
	grokHome  string
	sessionID string
}

func newUpdatesFileTailSubscriber(grokHome, sessionID string) *updatesFileTailSubscriber {
	return &updatesFileTailSubscriber{grokHome: grokHome, sessionID: sessionID}
}

// Run tails updates.jsonl from the current EOF and forwards each new
// session/update notification through convertSessionUpdate → onEvent, until ctx
// cancel, the post-turn grace elapsing, or the absolute hardCap. Returns nil on a
// clean exit (grace/hardCap) so the caller closes the event channel normally.
func (s *updatesFileTailSubscriber) Run(ctx context.Context, onEvent func(core.Event)) error {
	if strings.TrimSpace(s.sessionID) == "" {
		return fmt.Errorf("grokbuild: updates file tailer requires a sessionId")
	}
	home := resolveGrokHome(s.grokHome)

	// Late-bind: grok 可能在 session 创建后才写 updates.jsonl。
	path, err := s.waitForFile(ctx, home)
	if err != nil {
		return err
	}
	slog.Info("grokbuild: updates file tailer starting", "session", s.sessionID, "path", path)

	// 从当前 EOF 起 tail —— iOS 已 get_session_messages load 过权威历史,不重放。
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("grokbuild: stat updates.jsonl: %w", err)
	}
	// Attach 时补一条“未完成 turn 的 user prompt”: iOS 可能在 Mac 已发出 prompt
	// 之后才打开会话, user_message_chunk 已写在 EOF 之前。只补最后一个终态之后、
	// 尚未收到 turn_completed 的那条 prompt——已完成 turn 的 prompt 由冷 hydrate
	// 提供,这里不重放。
	// 2026-08-12: grok 在 turn 执行中就会把该 user 行追加进 chat_history.jsonl,
	// 冷 hydrate 基线已包含 prompt + 已生成的回复; 此时再补扫会把同一 prompt 重复
	// 投递给 iOS (问题/回复出现两次)。仅当 chat_history 尚未落盘该 prompt 的竞态
	// 窗口 (Mac 刚发出 prompt、iOS 立即冷开) 才需要补扫。
	if pending := latestPendingUserMessage(path); pending != "" &&
		!historyContainsUserPrompt(filepath.Join(filepath.Dir(path), "chat_history.jsonl"), pending) {
		onEvent(core.Event{Type: core.EventUserMessage, Content: pending})
	}
	offset := info.Size()

	ticker := time.NewTicker(grokUpdatesRelayPollInterval)
	defer ticker.Stop()

	lastGrowth := time.Now()
	sawTerminal := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		info, err := os.Stat(path)
		if err != nil {
			// 文件暂时消失 (session 删除 / 重写) —— 继续轮询直到 hardCap,代价低。
			if time.Since(lastGrowth) >= grokUpdatesRelayHardCap {
				return nil
			}
			continue
		}
		newSize := info.Size()
		switch {
		case newSize < offset:
			// 文件被截断重写 —— 从头开始。
			offset = 0
			lastGrowth = time.Now()
			sawTerminal = false
			continue
		case newSize == offset:
			idle := time.Since(lastGrowth)
			if sawTerminal && idle >= grokUpdatesRelayPostTurnGrace {
				slog.Info("grokbuild: updates file tailer idle after turn terminal, exiting",
					"session", s.sessionID, "idle", idle.String())
				return nil
			}
			if idle >= grokUpdatesRelayHardCap {
				slog.Info("grokbuild: updates file tailer hardCap elapsed, exiting",
					"session", s.sessionID, "idle", idle.String())
				return nil
			}
			continue
		}

		consumed, terminal, err := s.drainNew(path, offset, onEvent)
		if err != nil {
			slog.Debug("grokbuild: updates file tailer drain error", "session", s.sessionID, "error", err)
			continue
		}
		if consumed > 0 {
			offset += consumed
			lastGrowth = time.Now()
		}
		if terminal {
			sawTerminal = true
		}
	}
}

// latestPendingUserMessage scans updates.jsonl and returns the text of the most
// recent user_message_chunk that appears after the last turn_completed line.
// Returns "" when the tail is settled (all turns completed) — completed history is
// owned by the cold-hydrate path, never replayed here.
func latestPendingUserMessage(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	pending := ""
	for sc.Scan() {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var head struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(raw, &head) != nil || !isSessionUpdateMethod(head.Method) {
			continue
		}
		params := extractParams(raw)
		if len(params) == 0 || isReplayUpdate(params) {
			continue
		}
		var upd struct {
			Update sessionUpdatePayload `json:"update"`
		}
		if json.Unmarshal(params, &upd) != nil {
			continue
		}
		switch upd.Update.SessionUpdate {
		case "turn_completed":
			// 终态之后的 prompt 才属于未完成 turn; 终态前的 prompt 已由冷 hydrate
			// / 此前 live 路径覆盖, 不补。
			pending = ""
		case "user_message_chunk":
			if upd.Update.hasContent() {
				text := strings.TrimSpace(upd.Update.contentText())
				if text != "" {
					pending = text
				}
			}
		}
	}
	return pending
}

// historyContainsUserPrompt reports whether chat_history.jsonl already contains a
// user row whose normalized prompt text equals pending. Normalization matches
// readRichSessionHistory's user branch (unwrap <user_query>, trim, skip
// synthetic/bootstrap rows), so the cross-file comparison is exact — in the real
// running-session repro the chat_history <user_query> row and the updates.jsonl
// user_message_chunk carry byte-identical prompt text. A missing/unreadable
// chat_history returns false (the "prompt not yet persisted" race), so the
// attach-time replay is preserved for that window.
func historyContainsUserPrompt(chatHistoryPath, pending string) bool {
	f, err := os.Open(chatHistoryPath)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	want := strings.TrimSpace(pending)
	for sc.Scan() {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var row grokHistoryLine
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(row.Type)) != "user" || row.SyntheticReason != "" {
			continue
		}
		text := strings.TrimSpace(unwrapUserQuery(extractTextContent(row.Content)))
		if text == "" || looksLikeFrameworkBootstrap(text) {
			continue
		}
		if text == want {
			return true
		}
	}
	return false
}

// waitForFile polls until the session's updates.jsonl exists, returning its path.
// Fails after grokUpdatesRelayLateBindCap (session 真的不存在,不是迟建)。
func (s *updatesFileTailSubscriber) waitForFile(ctx context.Context, home string) (string, error) {
	deadline := time.Now().Add(grokUpdatesRelayLateBindCap)
	for {
		if dir := findSessionDir(home, s.sessionID); dir != "" {
			p := filepath.Join(dir, "updates.jsonl")
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("grokbuild: updates.jsonl not found for session %s within late-bind cap", s.sessionID)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(grokUpdatesRelayPollInterval):
		}
	}
}

// drainNew reads complete newline-terminated records appended after start,
// forwarding session/update lines through the shared codec. Returns the number of
// fully-consumed bytes (unterminated tail is retained for the next poll), whether
// a terminal turn event (turn_completed / error) was observed, and any read error.
func (s *updatesFileTailSubscriber) drainNew(path string, start int64, onEvent func(core.Event)) (consumed int64, terminal bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, false, err
	}
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		raw, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF {
			// 末尾未结束的字节留给下一次 poll (等分隔符到达)。
			break
		}
		if readErr != nil {
			return consumed, terminal, readErr
		}
		consumed += int64(len(raw))
		line := raw[:len(raw)-1]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		// 复用 leader subscriber 的通知解析路径:method 检查 → params → replay 过滤 →
		// convertSessionUpdate。updates.jsonl 行有相同 {method, params} shape (多一个顶层 timestamp)。
		var head struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(line, &head) != nil {
			continue
		}
		if !isSessionUpdateMethod(head.Method) {
			continue
		}
		params := extractParams(line)
		if len(params) == 0 {
			continue
		}
		if isReplayUpdate(params) {
			continue
		}
		for _, ev := range convertSessionUpdate(params, s.sessionID) {
			if ev.Done && (ev.Type == core.EventResult || ev.Type == core.EventError) {
				terminal = true
			}
			if onEvent != nil {
				onEvent(ev)
			}
		}
	}
	return consumed, terminal, nil
}
