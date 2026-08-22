package codexweb

// Managed-loopback legacy record cleanup (design §5.1/§6.3).
// 新产品路径不再创建或恢复该服务；这里只保留旧 owned process 的安全收口。
// A recorded PID is never trusted by itself: executable, exact --listen argv,
// process start time, and owning listen port must all still match before termination.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const (
	managedStateFile    = "codex-web-managed-server.json"
	managedStateVersion = 1
)

type managedState struct {
	Version      int    `json:"version"`
	Source       string `json:"source"`
	URL          string `json:"url"`
	Port         int    `json:"port"`
	PID          int    `json:"pid"`
	ProcessStart string `json:"process_start"`
	Binary       string `json:"binary"`
	CodexHome    string `json:"codex_home"`
	UpdatedAt    string `json:"updated_at"`
}

func managedStatePath(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		return ""
	}
	return filepath.Join(dataDir, managedStateFile)
}

func persistManagedRecord(opts ProbeOptions, ep *ServiceEndpoint) error {
	path := managedStatePath(opts.DataDir)
	if path == "" || ep == nil || ep.managed == nil {
		return nil
	}
	m := ep.managed
	if m.pid <= 0 || m.port <= 0 || m.url == "" || m.binary == "" || m.startTime == "" {
		return fmt.Errorf("managed process identity incomplete: pid=%d port=%d url=%q binary=%q start=%q", m.pid, m.port, m.url, m.binary, m.startTime)
	}
	state := managedState{
		Version: managedStateVersion, Source: string(SourceManagedLoopbackWS),
		URL: m.url, Port: m.port, PID: m.pid, ProcessStart: m.startTime,
		Binary: filepath.Clean(m.binary), CodexHome: opts.CodexHome,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return err
	}
	return core.AtomicWriteFile(path, raw, 0o600)
}

func readManagedRecord(path string) (*managedState, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state managedState
	if err := json.Unmarshal(raw, &state); err != nil {
		_ = os.Remove(path)
		return nil, nil
	}
	return &state, nil
}

func validateManagedRecord(state *managedState, expectedBinary string, deps LifecycleDeps) bool {
	if state == nil || state.Version != managedStateVersion || state.Source != string(SourceManagedLoopbackWS) ||
		state.PID <= 0 || state.Port <= 0 || state.ProcessStart == "" || state.Binary == "" {
		return false
	}
	parsed, err := url.Parse(state.URL)
	if err != nil || parsed.Scheme != "ws" || parsed.Hostname() != "127.0.0.1" {
		return false
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port != state.Port {
		return false
	}
	if filepath.Clean(state.Binary) != filepath.Clean(expectedBinary) {
		return false
	}
	command, startTime, alive := deps.InspectProcess(state.PID)
	if !alive || startTime != state.ProcessStart {
		return false
	}
	expectedCommand := filepath.Clean(state.Binary) + " app-server --listen " + state.URL
	if strings.TrimSpace(command) != expectedCommand {
		return false
	}
	return deps.ProcessOwnsPort(state.PID, state.Port)
}

func cleanupRecordedManaged(opts ProbeOptions, expectedBinary string, deps LifecycleDeps) {
	path := managedStatePath(opts.DataDir)
	state, err := readManagedRecord(path)
	if err != nil || state == nil {
		return
	}
	if validateManagedRecord(state, expectedBinary, deps) {
		_ = deps.TerminateProcess(state.PID)
	}
	_ = os.Remove(path)
}

func removeManagedRecordIfOwned(path string, process *managedProcess) {
	if path == "" || process == nil {
		return
	}
	state, err := readManagedRecord(path)
	if err != nil || state == nil {
		return
	}
	if state.PID == process.pid && state.Port == process.port && state.ProcessStart == process.startTime {
		_ = os.Remove(path)
	}
}
