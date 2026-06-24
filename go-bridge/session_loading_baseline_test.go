package gobridge

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/agent/claudecode"
	"github.com/openAgi2/cordcode-macbridge/agent/codex"
	"github.com/openAgi2/cordcode-macbridge/core"
)

const sessionLoadingBaselineRuns = 5

type baselineSession struct {
	ID        string
	Directory string
	Path      string
	Size      int64
}

func TestSessionLoadingRealDatasetBaseline(t *testing.T) {
	if os.Getenv("CCCODE_SESSION_LOADING_BASELINE") != "1" {
		t.Skip("set CCCODE_SESSION_LOADING_BASELINE=1 to run against real local transcripts")
	}

	metricsPath := os.Getenv("CCCODE_SESSION_LOADING_BASELINE_OUTPUT")
	if metricsPath == "" {
		t.Fatal("CCCODE_SESSION_LOADING_BASELINE_OUTPUT is required")
	}
	if err := os.MkdirAll(filepath.Dir(metricsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	metricsFile, err := os.Create(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer metricsFile.Close()

	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stderr, metricsFile), nil)))
	defer slog.SetDefault(previousLogger)

	codexAgent := mustBaselineAgent(t, "codex")
	claudeAgent := mustBaselineAgent(t, "claudecode")
	codexSessions := baselineListSessions(t, codexAgent, "codex")
	claudeSessions := baselineListSessions(t, claudeAgent, "claudecode")

	codexSmall, codexLarge := selectBaselineSessions(t, codexSessions, codexSessionFiles(t))
	claudeSmall, claudeLarge := selectBaselineSessions(t, claudeSessions, claudeSessionFiles(t))
	writeBaselineDatasetRecord(t, metricsFile, "codex", codexSessions, codexSmall, codexLarge)
	writeBaselineDatasetRecord(t, metricsFile, "claudecode", claudeSessions, claudeSmall, claudeLarge)

	for _, route := range []string{"direct", "relay"} {
		runListBaseline(t, route, "codex")
		runListBaseline(t, route, "claudecode")
		runHistoryBaseline(t, route, "codex", "small", codexSmall)
		runHistoryBaseline(t, route, "codex", "large", codexLarge)
		runHistoryBaseline(t, route, "claudecode", "small", claudeSmall)
		runHistoryBaseline(t, route, "claudecode", "large", claudeLarge)
	}
}

func mustBaselineAgent(t *testing.T, backendID string) core.Agent {
	t.Helper()
	var (
		agent core.Agent
		err   error
	)
	switch backendID {
	case "codex":
		agent, err = codex.New(map[string]any{"work_dir": "."})
	case "claudecode":
		agent, err = claudecode.New(map[string]any{"work_dir": "."})
	default:
		t.Fatalf("unsupported baseline backend %q", backendID)
	}
	if err != nil {
		t.Fatalf("create %s agent: %v", backendID, err)
	}
	return agent
}

func baselineListSessions(t *testing.T, agent core.Agent, backendID string) []baselineSession {
	t.Helper()
	conn := &sessionLoadCaptureConn{}
	handlers := NewHandlers()
	handlers.RegisterAgent(backendID, agent)
	handlers.HandleRPC(conn, WireMessage{
		BackendID: backendID,
		Method:    "list_sessions",
		RequestID: "baseline-discovery-" + backendID,
	})
	response, ok := conn.sent.(map[string]interface{})
	if !ok {
		t.Fatalf("%s discovery response type = %T", backendID, conn.sent)
	}
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s discovery response missing data: %#v", backendID, response)
	}

	var sessions []baselineSession
	switch values := data["sessions"].(type) {
	case []map[string]interface{}:
		for _, value := range values {
			sessions = append(sessions, baselineSession{
				ID:        stringValue(value["id"]),
				Directory: stringValue(value["directory"]),
			})
		}
	case []interface{}:
		for _, raw := range values {
			value, _ := raw.(map[string]interface{})
			sessions = append(sessions, baselineSession{
				ID:        stringValue(value["id"]),
				Directory: stringValue(value["directory"]),
			})
		}
	default:
		t.Fatalf("%s discovery sessions type = %T", backendID, data["sessions"])
	}
	if len(sessions) == 0 {
		t.Fatalf("%s discovery returned no sessions", backendID)
	}
	return sessions
}

func selectBaselineSessions(t *testing.T, sessions []baselineSession, files map[string]baselineSession) (baselineSession, baselineSession) {
	t.Helper()
	var matched []baselineSession
	for _, session := range sessions {
		file, ok := files[session.ID]
		if !ok || file.Size == 0 {
			continue
		}
		session.Path = file.Path
		session.Size = file.Size
		if session.Directory == "" {
			session.Directory = file.Directory
		}
		matched = append(matched, session)
	}
	if len(matched) < 2 {
		t.Fatalf("matched only %d real transcript files", len(matched))
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Size < matched[j].Size })

	small := matched[0]
	for _, candidate := range matched {
		if candidate.Size >= 64*1024 {
			small = candidate
			break
		}
	}
	return small, matched[len(matched)-1]
}

func codexSessionFiles(t *testing.T) map[string]baselineSession {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]baselineSession)
	err = filepath.Walk(filepath.Join(home, ".codex", "sessions"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		parts := strings.Split(base, "-")
		if len(parts) < 5 {
			return nil
		}
		id := strings.Join(parts[len(parts)-5:], "-")
		result[id] = baselineSession{ID: id, Path: path, Size: info.Size()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func claudeSessionFiles(t *testing.T) map[string]baselineSession {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]baselineSession)
	pattern := filepath.Join(home, ".claude", "projects", "*", "*.jsonl")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		result[id] = baselineSession{
			ID:        id,
			Directory: resolveProjectRealDirectory(filepath.Dir(path)),
			Path:      path,
			Size:      info.Size(),
		}
	}
	return result
}

func runListBaseline(t *testing.T, route, backendID string) {
	t.Helper()
	for run := 1; run <= sessionLoadingBaselineRuns; run++ {
		agent := mustBaselineAgent(t, backendID)
		runBaselineRequest(t, route, agent, WireMessage{
			BackendID: backendID,
			Method:    "list_sessions",
			RequestID: baselineRequestID(route, backendID, "list", "cold", run),
		})
	}

	agent := mustBaselineAgent(t, backendID)
	handlers := NewHandlers()
	handlers.RegisterAgent(backendID, agent)
	runBaselineRequestWithHandlers(t, route, handlers, WireMessage{
		BackendID: backendID,
		Method:    "list_sessions",
		RequestID: baselineRequestID(route, backendID, "list", "warmup", 0),
	})
	for run := 1; run <= sessionLoadingBaselineRuns; run++ {
		runBaselineRequestWithHandlers(t, route, handlers, WireMessage{
			BackendID: backendID,
			Method:    "list_sessions",
			RequestID: baselineRequestID(route, backendID, "list", "hot", run),
		})
	}
}

func runHistoryBaseline(t *testing.T, route, backendID, sizeClass string, session baselineSession) {
	t.Helper()
	params, err := json.Marshal(GetSessionMessagesParams{
		SessionID: session.ID,
		Directory: session.Directory,
		Limit:     200,
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := mustBaselineAgent(t, backendID)
	for _, temperature := range []string{"cold", "hot"} {
		for run := 1; run <= sessionLoadingBaselineRuns; run++ {
			runBaselineRequest(t, route, agent, WireMessage{
				BackendID: backendID,
				Method:    "get_session_messages",
				RequestID: baselineRequestID(route, backendID, "history-"+sizeClass, temperature, run),
				Params:    params,
			})
		}
	}
}

func runBaselineRequest(t *testing.T, route string, agent core.Agent, message WireMessage) {
	t.Helper()
	handlers := NewHandlers()
	handlers.RegisterAgent(message.BackendID, agent)
	runBaselineRequestWithHandlers(t, route, handlers, message)
}

func runBaselineRequestWithHandlers(t *testing.T, route string, handlers *Handlers, message WireMessage) {
	t.Helper()
	var conn Connection
	var waitForResponse func()
	switch route {
	case "direct":
		serverConn, clientConn, cleanup := openTestConn(t)
		conn = adaptDirectConn(serverConn)
		responseErr := make(chan error, 1)
		go func() {
			_, _, err := clientConn.ReadMessage()
			responseErr <- err
		}()
		waitForResponse = func() {
			t.Helper()
			if err := <-responseErr; err != nil {
				t.Fatalf("read direct baseline response: %v", err)
			}
			cleanup()
		}
	case "relay":
		key := make([]byte, 32)
		for i := range key {
			key[i] = byte(i + 1)
		}
		conn = NewRelayDeviceConn(
			"baseline-device",
			"baseline-bridge",
			"baseline-route",
			1,
			nil,
			key,
			key,
			func(json.RawMessage) error { return nil },
		)
	default:
		t.Fatalf("unsupported route %q", route)
	}
	handlers.HandleRPC(conn, message)
	if waitForResponse != nil {
		waitForResponse()
	}
}

func baselineRequestID(route, backendID, scenario, temperature string, run int) string {
	return fmt.Sprintf("baseline:%s:%s:%s:%s:%d", route, backendID, scenario, temperature, run)
}

func writeBaselineDatasetRecord(
	t *testing.T,
	writer io.Writer,
	backendID string,
	sessions []baselineSession,
	small baselineSession,
	large baselineSession,
) {
	t.Helper()
	record := map[string]any{
		"record_type":      "session_loading_dataset",
		"backend_id":       backendID,
		"session_count":    len(sessions),
		"small_session_id": small.ID,
		"small_file_bytes": small.Size,
		"large_session_id": large.ID,
		"large_file_bytes": large.Size,
		"large_directory":  large.Directory,
		"small_directory":  small.Directory,
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		t.Fatal(err)
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
