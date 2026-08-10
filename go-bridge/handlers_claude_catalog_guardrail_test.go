package gobridge

// handlers_claude_catalog_guardrail_test.go 锁定 Claude Code catalog 的 §5.2 诚实边界
// （docs/2026-08-09-cross-backend-session-catalog-parity-implementation-plan.md §5.2）：
// Claude 没有公开的原生 catalog API（见 claude_session_catalog.go 头注释 +
// docs/2026-08-10-claude-catalog-supported-interface-investigation.md），所以它的 catalog
// 是**文件派生的 compatibility catalog**，**不**走 catalog_cursor_epoch_v2 v2 主线。
//
// 本测试守护的不变量（「不宣称 false parity」的结构保证）：
// 即使连接在 hello 声明了 catalog_cursor_epoch_v2（DECLARED），claudecode 的 list_sessions
// 响应里的 nextCursor 也**必须**是 v1（无 epoch），而不是 v2 epoch cursor。若未来有人为 claude
// 加了 ConnCatalogCursorEpochV2 门控的 v2 分支（模仿 codex/grokbuild），本测试会失败——
// 因为那等于在没有原生 catalog 数据源的前提下伪造「与 Mac native 同源」的 v2 cursor。
//
// 对照：codex（handlers_codex_catalog_test.go）/ grokbuild（handlers_grok_catalog_test.go）
// 在 DECLARED 时发射 v2 epoch cursor；claudecode 在 DECLARED 时仍发射 v1 cursor。三者差异即
// 「有原生 catalog API 的 backend 迁移到 v2 主线；没有的 backend 诚实停留在 v1 compatibility」。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClaudeCatalog_Declared_NeverEmitsV2EpochCursor：DECLARED 连接（hello 声明
// catalog_cursor_epoch_v2）+ claudecode + 2 session / limit=1 → hasMore=true + nextCursor，
// 且 nextCursor 解码后**必须是 v1**（isV1=true，Version==listCursorVersion），**不得**是 v2
// epoch cursor。这是 §5.2 「Claude 不宣称 false parity」的核心结构保证。
func TestClaudeCatalog_Declared_NeverEmitsV2EpochCursor(t *testing.T) {
	agent := &fakeAgent{name: "claudecode", reasoningEffort: "high"}

	// 2 个 session（不同 UpdatedAt），limit=1 → hasMore=true → 产出 nextCursor。
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-claude-guardrail")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ses_a.jsonl", "ses_b.jsonl"} {
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	catalog := newClaudeSessionCatalog(projectsDir)
	catalog.parseSession = func(path string, _ time.Time) claudeSessionScanResult {
		// 按 filename 区分两条 session 的 UpdatedAt，使 v1 cursor 携带可区分的 (ts, id)。
		ts := time.Unix(1710000500, 0).UTC()
		if strings.Contains(path, "ses_b") {
			ts = time.Unix(1710000600, 0).UTC() // ses_b 更新
		}
		return claudeSessionScanResult{
			Title:     "guardrail session",
			CreatedAt: time.Unix(1710000000, 0).UTC(),
			UpdatedAt: ts,
		}
	}

	handlers := newTestHandlers(t)
	handlers.claudeSessions = catalog
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	// 关键：DECLARED —— 即使连接声明了 catalog_cursor_epoch_v2，claudecode 也不应被升级到 v2。
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true)

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "list_sessions",
		RequestID: "claude-guard-1",
		Params:    mustJSONRaw(t, map[string]any{"limit": 1}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if msgs[0]["ok"] != true {
		t.Fatalf("ok = %#v, want true（claude compatibility catalog 应正常返回）: %#v", msgs[0]["ok"], msgs[0])
	}
	data, _ := msgs[0]["data"].(map[string]any)
	if data["hasMore"] != true {
		t.Fatalf("hasMore = %#v, want true（2 session / limit 1，需产出 nextCursor 才能断言其版本）", data["hasMore"])
	}
	nextCursor, ok := data["nextCursor"].(string)
	if !ok || nextCursor == "" {
		t.Fatalf("nextCursor = %#v, want 非空（无 cursor 则无法守护「v1 而非 v2」不变量）", data["nextCursor"])
	}

	// 核心断言：nextCursor 必须是 v1（isV1=true），不是 v2 epoch cursor。
	decoded, isV1, derr := decodeListCursorV2(nextCursor)
	if derr != nil {
		t.Fatalf("nextCursor 解码失败（既不是合法 v1 也不是 v2）：%v", derr)
	}
	if !isV1 {
		t.Fatalf("nextCursor 是 v2 epoch cursor（epoch=%q）—— claudecode 在 DECLARED 连接下不得发射 v2，"+
			"这等于在没有原生 catalog 数据源时伪造 false parity（§5.2）", decoded.Epoch)
	}
	if decoded.Version != listCursorVersion {
		t.Fatalf("nextCursor version = %d, want %d（v1）", decoded.Version, listCursorVersion)
	}
	if decoded.Epoch != "" {
		t.Fatalf("v1 cursor 不应携带 epoch，got epoch=%q", decoded.Epoch)
	}
}

// TestClaudeCatalog_Declared_StillReturnsSessions：DECLARED 连接不会让 claudecode list_sessions
// 报错或空返回——capability 声明对 claude 是 no-op（不改变行为），catalog 继续从文件派生正常返回。
// 防止「为门控 claude v2 而误伤了 compatibility 路径」这类回归。
func TestClaudeCatalog_Declared_StillReturnsSessions(t *testing.T) {
	agent := &fakeAgent{name: "claudecode"}
	projectsDir := t.TempDir()
	projectDir := filepath.Join(projectsDir, "-tmp-claude-returns")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "ses_1.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := newClaudeSessionCatalog(projectsDir)
	catalog.parseSession = func(_ string, _ time.Time) claudeSessionScanResult {
		return claudeSessionScanResult{
			Title:     "compat session",
			CreatedAt: time.Unix(1710000000, 0).UTC(),
			UpdatedAt: time.Unix(1710000500, 0).UTC(),
		}
	}

	handlers := newTestHandlers(t)
	handlers.claudeSessions = catalog
	handlers.RegisterAgent("claudecode", agent)
	serverConn, clientConn, cleanup := openTestConn(t)
	defer cleanup()
	handlers.eventPublisher.SetConnCatalogCursorEpochV2(serverConn, true) // DECLARED

	handlers.HandleRPC(serverConn, WireMessage{
		BackendID: "claudecode",
		Method:    "list_sessions",
		RequestID: "claude-returns-1",
		Params:    mustJSONRaw(t, map[string]any{}),
	})
	msgs := readJSONMaps(t, clientConn, 1)
	if msgs[0]["ok"] != true {
		t.Fatalf("ok = %#v, want true（DECLARED 不应让 claude compatibility catalog 失败）", msgs[0]["ok"])
	}
	data, _ := msgs[0]["data"].(map[string]any)
	sessionsRaw, _ := data["sessions"].([]any)
	if len(sessionsRaw) != 1 {
		t.Fatalf("DECLARED claude sessions = %d, want 1（capability 声明对 claude 是 no-op）", len(sessionsRaw))
	}
}
