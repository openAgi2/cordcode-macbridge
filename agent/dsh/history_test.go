// history_test.go covers the DSH log → RichHistoryEntry mapping and the
// Agent-level ListSessions / GetRichSessionHistory wiring (design §4.2/§4.3).
package dsh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// miniSessionRecords is a compact but structurally faithful session: human
// prompt, plugin injection to skip, title event, one full turn with
// reasoning + tool-call + text and its correlated result, packed chunk rows
// that must be ignored, and a second aborted-less turn.
func miniSessionRecords() []string {
	return []string{
		`{"type":"session","version":0,"id":"session-hist","createdAt":1786778000000,"cwd":"/Users/jacklee/Projects/demo","delegationDepth":0}`,
		`{"type":"user/message","seq":9,"time":1786778000100,"data":{"content":[{"type":"text","text":"看看这个目录"}],"source":{"kind":"user"},"role":"user","id":"m1"}}`,
		`{"type":"user/message","seq":10,"time":1786778000110,"data":{"content":[{"type":"text","text":"<system-reminder>noise</system-reminder>"}],"source":{"kind":"plugin","plugin":"workspace"},"role":"user","id":"m2"}}`,
		`{"type":"session/title","seq":13,"time":1786778000111,"data":{"title":"看看这个目录","messageSeqs":[9],"source":{"kind":"fallback"}}}`,
		`{"type":"turn/start","seq":14,"time":1786778000120,"data":{"turn":1}}`,
		`{"type":"assistant/chunk","seq":15,"time":1786778000121,"data":{"chunk":{"kind":"text","index":0,"text":"raw delta"}}}`,
		`{"type":"text-chunks","seq":16,"time":1786778000122,"data":{"runs":[]}}`,
		`{"type":"assistant/message","seq":17,"time":1786778000200,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"reasoning","text":"先想一下"},{"type":"tool-call","id":"call-1","name":"bash","arguments":"{\"command\":\"ls /tmp\"}"}],"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"}}}}`,
		`{"type":"tool/result","seq":18,"time":1786778000300,"data":{"turn":1,"step":1,"message":{"source":{"kind":"tool","callId":"call-1"},"content":[{"type":"tool-result","toolCallId":"call-1","content":[{"type":"text","text":"file1\nfile2"}]}]}}}`,
		`{"type":"assistant/message","seq":19,"time":1786778000400,"data":{"turn":1,"step":2,"message":{"role":"assistant","content":[{"type":"reasoning","text":"再看看结果"},{"type":"text","text":"目录里有两个文件"}],"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"}}}}`,
		`{"type":"turn/end","seq":20,"time":1786778000500,"data":{"turn":1,"reason":{"kind":"completed"}}}`,
		`{"type":"user/message","seq":21,"time":1786778000600,"data":{"content":[{"type":"text","text":"第二问"}],"source":{"kind":"user"},"role":"user","id":"m3"}}`,
		`{"type":"turn/start","seq":22,"time":1786778000610,"data":{"turn":2}}`,
		`{"type":"assistant/message","seq":23,"time":1786778000700,"data":{"turn":2,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"第二个回答"}],"source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"}}}}`,
		`{"type":"turn/end","seq":24,"time":1786778000800,"data":{"turn":2,"reason":{"kind":"completed"}}}`,
	}
}

func writeHistoryFixture(t *testing.T, compress bool) storeSession {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "--demo--", "session-hist")
	path := writeSessionLog(t, dir, miniSessionRecords(), compress)
	return storeSession{ID: "session-hist", Path: path, Plain: !compress, Cwd: "/Users/jacklee/Projects/demo"}
}

func TestReadRichHistoryMapsTurnStructure(t *testing.T) {
	for _, compress := range []bool{false, true} {
		sess := writeHistoryFixture(t, compress)
		entries, err := readRichHistory(sess, 0)
		if err != nil {
			t.Fatalf("compress=%v: %v", compress, err)
		}
		if len(entries) != 4 {
			t.Fatalf("compress=%v: got %d entries, want 4 (user, turn1, user, turn2): %+v", compress, len(entries), entries)
		}
		user1 := entries[0]
		if user1.Role != "user" || user1.Content != "看看这个目录" || user1.ID != "session-hist:9" {
			t.Errorf("user1 wrong: %+v", user1)
		}
		turn1 := entries[1]
		if turn1.Role != "assistant" || turn1.Content != "目录里有两个文件" {
			t.Errorf("turn1 wrong: %+v", turn1)
		}
		if turn1.Thinking != "先想一下\n再看看结果" {
			t.Errorf("turn1 thinking = %q", turn1.Thinking)
		}
		if turn1.TurnStartedAt == nil || turn1.TurnStartedAt.UnixMilli() != 1786778000120 {
			t.Errorf("turn1 started = %v", turn1.TurnStartedAt)
		}
		if turn1.TurnCompletedAt == nil || turn1.TurnCompletedAt.UnixMilli() != 1786778000500 {
			t.Errorf("turn1 completed = %v", turn1.TurnCompletedAt)
		}
		if turn1.ProviderID != "deepseek-official" || turn1.ModelID != "deepseek-v4-flash" {
			t.Errorf("turn1 attribution = %q/%q", turn1.ProviderID, turn1.ModelID)
		}
		if len(turn1.Steps) != 1 {
			t.Fatalf("turn1 steps = %d, want 1", len(turn1.Steps))
		}
		step := turn1.Steps[0]
		if step["toolName"] != "bash" || step["title"] != "ls /tmp" {
			t.Errorf("step wrong: %v", step)
		}
		out := step["output"].(map[string]any)
		if out["text"] != "file1\nfile2" {
			t.Errorf("tool output not correlated: %v", out)
		}
		if got := partTypes(turn1.Parts); strings.Join(got, ",") != "reasoning,tool,reasoning,text" {
			t.Errorf("turn1 parts = %v, want reasoning,tool,reasoning,text", got)
		}
		// Plugin injections and chunk/packed rows never surface.
		for _, e := range entries {
			if strings.Contains(e.Content, "system-reminder") || strings.Contains(e.Content, "raw delta") {
				t.Errorf("noise leaked into history: %+v", e)
			}
		}
		user2 := entries[2]
		if user2.Role != "user" || user2.Content != "第二问" || user2.ID != "session-hist:21" {
			t.Errorf("user2 wrong: %+v", user2)
		}
		turn2 := entries[3]
		if turn2.Content != "第二个回答" || turn2.ID != "session-hist:22" {
			t.Errorf("turn2 wrong: %+v", turn2)
		}
	}
}

func TestReadRichHistoryStableIDsAndLimit(t *testing.T) {
	sess := writeHistoryFixture(t, false)
	first, err := readRichHistory(sess, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readRichHistory(sess, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("unstable id at %d: %q vs %q", i, first[i].ID, second[i].ID)
		}
	}
	limited, err := readRichHistory(sess, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].Content != "第二问" || limited[1].Content != "第二个回答" {
		t.Errorf("limit must keep the trailing entries: %+v", limited)
	}
}

func partTypes(parts []map[string]any) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p["type"].(string))
	}
	return out
}

func TestAgentListSessionsAndRichHistoryWiring(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	writeHistoryFixtureAt(t, home)
	// Older session for ordering; subagent session that must be hidden.
	writeSessionLog(t, filepath.Join(home, "sessions", "--demo--", "session-old"),
		[]string{`{"type":"session","version":0,"id":"session-old","createdAt":1,"cwd":"/old","delegationDepth":0}`,
			`{"type":"user/message","seq":1,"time":2,"data":{"content":[{"type":"text","text":"旧会话第一条消息比较长需要截断"}],"source":{"kind":"user"},"role":"user","id":"m"}}`}, false)
	writeSessionLog(t, filepath.Join(home, "sessions", "--demo--", "child-x"),
		[]string{`{"type":"session","version":0,"id":"child-x","createdAt":3,"cwd":"/demo","origin":"subagent","delegationDepth":1}`}, true)

	a := &Agent{workDir: t.TempDir()}
	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("list = %d sessions, want 2 (subagent hidden): %+v", len(sessions), sessions)
	}
	if sessions[0].ID != "session-hist" || sessions[1].ID != "session-old" {
		t.Fatalf("list must be mtime-desc: %+v", sessions)
	}
	top := sessions[0]
	if top.Summary != "看看这个目录" || top.Directory != "/Users/jacklee/Projects/demo" {
		t.Errorf("top row wrong: %+v", top)
	}

	entries, err := a.GetRichSessionHistory(context.Background(), "session-hist", 0)
	if err != nil || len(entries) != 4 {
		t.Fatalf("rich history wiring: %d entries err=%v", len(entries), err)
	}
	if _, err := a.GetRichSessionHistory(context.Background(), "no-such", 0); err == nil {
		t.Fatal("unknown id must error")
	}
}

// writeHistoryFixtureAt writes the golden session into a caller-owned home.
func writeHistoryFixtureAt(t *testing.T, home string) {
	t.Helper()
	writeSessionLog(t, filepath.Join(home, "sessions", "--demo--", "session-hist"), miniSessionRecords(), true)
	if err := os.Chtimes(filepath.Join(home, "sessions", "--demo--", "session-hist"), nowPlus(60), nowPlus(60)); err != nil {
		t.Fatal(err)
	}
}

func nowPlus(seconds int64) (t time.Time) {
	t = time.Now().Add(time.Duration(seconds) * time.Second)
	return
}

// TestReadRealHistorySmoke maps a REAL harness session end-to-end (the
// developer's zstd web artifacts). Opt-in: DSH_STORE_SMOKE=1.
func TestReadRealHistorySmoke(t *testing.T) {
	if os.Getenv("DSH_STORE_SMOKE") != "1" {
		t.Skip("set DSH_STORE_SMOKE=1 to read the real ~/.dsh/sessions")
	}
	store := openDshSessionStore()
	sessions, err := store.scanSessions()
	if err != nil || len(sessions) == 0 {
		t.Fatalf("scan: %v %d", err, len(sessions))
	}
	var target *storeSession
	for i := range sessions {
		if sessions[i].ID == "420ea5bc-8572-4a47-b7d2-cfac0e39a127" {
			target = &sessions[i]
		}
	}
	if target == nil {
		t.Skip("real research session not present")
	}
	entries, err := readRichHistory(*target, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The research session is ONE 31-step turn: user entry + one rich
	// assistant entry is the structurally correct mapping.
	if len(entries) != 2 {
		t.Fatalf("real single-turn session should map 2 entries, got %d", len(entries))
	}
	toolSteps, textTurns := 0, 0
	for _, e := range entries {
		if e.Role == "assistant" {
			if e.Content != "" {
				textTurns++
			}
			toolSteps += len(e.Steps)
		}
	}
	if toolSteps == 0 || textTurns == 0 {
		t.Fatalf("real session mapping hollow: steps=%d textTurns=%d", toolSteps, textTurns)
	}
	t.Logf("entries=%d toolSteps=%d textTurns=%d firstUser=%.60q", len(entries), toolSteps, textTurns, entries[0].Content)
}
