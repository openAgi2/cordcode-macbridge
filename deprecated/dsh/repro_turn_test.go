package dsh

// TestTurnAgainstSeededStoreRepro — env-gated real-spawn reproduction of the
// 2026-08-16 real-device failure (iOS new session → immediate turn_error,
// Mac web never sees the session). Root cause: the harness persistence
// backend's checkRootEncoding refuses a root that mixes encodings — the web
// profile writes zstd artifacts while the driver composition pinned
// compression:none, so the first session/prompt against a store that already
// holds a zstd artifact fails at materialization.
//
// Run with DSH_TURN_REPRO=1 (uses the real user-global npm dsh spawn chain;
// a mock provider key means no real LLM call ever leaves the machine — the
// encoding failure fires before any network I/O).
import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

func TestTurnAgainstSeededStoreRepro(t *testing.T) {
	if os.Getenv("DSH_TURN_REPRO") == "" {
		t.Skip("set DSH_TURN_REPRO=1 to run the real-spawn store repro")
	}
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	// Seed the store the way the harness web does: one zstd artifact.
	// DSH_TURN_REPRO=noseed runs the control arm without the seed.
	if os.Getenv("DSH_TURN_REPRO") != "noseed" {
		writeSessionLog(t, filepath.Join(home, "sessions", "--demo--", "session-web-1"),
			[]string{`{"type":"session","version":0,"id":"session-web-1","createdAt":1,"cwd":"/demo","delegationDepth":0}`}, true)
	}

	agent, err := New(map[string]any{
		"work_dir": t.TempDir(),
		"model":    "deepseek-chat",
		"mode":     "workspace-write",
	})
	if err != nil {
		t.Fatalf("New (probe/spawn chain): %v", err)
	}
	a, ok := agent.(*Agent)
	if !ok {
		t.Fatalf("New returned %T, want *dsh.Agent", agent)
	}
	a.SetProviders([]core.ProviderConfig{{Name: "mock", APIKey: "dsh-conn-fake-key"}})
	if !a.SetActiveProvider("mock") {
		t.Fatal("SetActiveProvider(mock) failed")
	}

	sess, err := a.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer sess.Close()

	// Raw probe: speak JSON-RPC to a fresh runtime spawn by hand so the
	// untruncated turn/end error payload is visible (the driver codec folds
	// it into a generic reason). The encoding-conflict text must be GONE
	// after the zstd fix; a mock-key auth failure is the expected terminal.
	if probe := rawProbe(t, a); strings.Contains(probe, "configured for compression") {
		t.Fatalf("ROOT CAUSE STILL PRESENT — driver compression conflicts with the seeded zstd store: %.400s", probe)
	} else {
		t.Logf("probe turn/end: %.300s", probe)
	}

	evCh := sess.Events()
	if err := sess.Send("hi", nil, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	terminal := ""
	deadline := time.After(60 * time.Second)
collect:
	for {
		select {
		case ev := <-evCh:
			t.Logf("event=%+v", ev)
			switch ev.Type {
			case core.EventResult, core.EventError:
				terminal = string(ev.Type) + ": " + ev.Content
				break collect
			}
		case <-deadline:
			t.Fatal("no terminal event within 60s")
		}
	}
	// With the fixed composition the turn reaches the LLM layer: the mock key
	// surfaces as an auth error (or, if a real key chain kicks in, a completed
	// turn). Materialization may be gated on the first successful flush, so
	// its absence under a mock-key auth failure is not itself a defect — the
	// encoding-conflict text above is the assertion that matters.
	sessionsRoot := filepath.Join(home, "sessions")
	found := false
	var projects []os.DirEntry
	if projects, err = os.ReadDir(sessionsRoot); err == nil {
		for _, project := range projects {
			entries, readErr := os.ReadDir(filepath.Join(sessionsRoot, project.Name()))
			if readErr != nil {
				continue
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "dsh-") {
					found = true
				}
			}
		}
	}
	t.Logf("materialized driver session in store: %v (terminal: %.200s)", found, terminal)
}

// rawProbe spawns the runtime exactly as the driver does and prints the raw
// JSON-RPC traffic for one initialize + session/prompt round — ground truth
// for turn-failure texts the codec otherwise folds away.
func rawProbe(t *testing.T, a *Agent) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if a.nodeBin != "" {
		args := append(append([]string{}, a.scriptPreArgs...), a.scriptPath)
		if !a.configViaEnv {
			args = append(args, a.cliExtraArgs...)
			args = append(args, a.configPath)
		}
		cmd = exec.CommandContext(ctx, a.nodeBin, args...)
		cmd.Dir = a.spawnDir
	} else {
		args := append([]string{a.configPath}, a.cliExtraArgs...)
		cmd = exec.CommandContext(ctx, a.cliBin, args...)
		cmd.Dir = a.workDir
	}
	cmd.Env = append(a.buildProcessEnv(), "DSH_CORDIS_CONFIG="+a.configPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("probe stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("probe stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("probe start: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	cwd, _ := os.Getwd()
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"cwd":` + jsonQuote(cwd) + `,"provider":"deepseek-official","model":"deepseek-chat"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"probe-` + fmt.Sprintf("%d", time.Now().UnixNano()) + `","contentBlocks":[{"type":"text","text":"hi"}]}}`,
	}
	go func() {
		for _, line := range lines {
			if _, err := stdin.Write([]byte(line + "\n")); err != nil {
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	deadline := time.After(20 * time.Second)
	turnEnd := ""
	for {
		select {
		case <-deadline:
			return turnEnd
		default:
		}
		if !scanner.Scan() {
			return turnEnd
		}
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		t.Logf("RAW: %s", line)
		if strings.Contains(line, `"turn/end"`) {
			turnEnd = line
		}
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
