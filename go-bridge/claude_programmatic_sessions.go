package gobridge

import (
	"bufio"
	"encoding/json"
	"os"
)

// Claude's IDE session picker hides programmatic/headless sessions. These values
// mirror @anthropic-ai/claude-agent-sdk 0.3.301 listSessions(includeProgrammatic:false).
const claudeProgrammaticTailReadLimit = 64 * 1024

func isClaudeProgrammaticSession(entrypoint, sessionKind string) bool {
	switch entrypoint {
	case "sdk-cli", "sdk-ts", "sdk-py":
		return true
	}
	switch sessionKind {
	case "daemon", "daemon-worker":
		return true
	}
	return false
}

// claudeTailEntrypoint reads the last top-level string entrypoint from the final
// 64KiB window. The first line is skipped when that window starts mid-line.
// An empty head hit must not fall back, so the caller invokes this only when the
// head window contains no top-level entrypoint at all.
func claudeTailEntrypoint(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return ""
	}
	tailSize := info.Size()
	if tailSize > claudeProgrammaticTailReadLimit {
		tailSize = claudeProgrammaticTailReadLimit
	}
	if _, err := f.Seek(info.Size()-tailSize, 0); err != nil {
		return ""
	}
	reader := bufio.NewReader(f)
	if tailSize == claudeProgrammaticTailReadLimit {
		// Discard the potentially partial line at the window boundary.
		_, _ = reader.ReadString('\n')
	}

	var last string
	var found bool
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(line, &raw) != nil {
			continue
		}
		var value string
		if rawValue, ok := raw["entrypoint"]; ok && json.Unmarshal(rawValue, &value) == nil {
			last = value
			found = true
		}
	}
	if !found {
		return ""
	}
	return last
}
