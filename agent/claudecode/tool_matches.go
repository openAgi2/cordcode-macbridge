package claudecode

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// parseClaudeToolMatches accepts only Claude Code's native structured union or
// the fixture-backed Glob path-list format. Unknown/truncated text is omitted.
func parseClaudeToolMatches(toolName string, content any, isError bool) *core.ToolMatches {
	if isError {
		return nil
	}
	if structured := parseNativeToolMatches(content); structured != nil {
		return structured
	}
	if toolName != "Glob" {
		return nil
	}
	text, ok := content.(string)
	if !ok {
		return nil
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "No files found" {
		return &core.ToolMatches{Kind: "paths", Paths: []string{}}
	}
	if trimmed == "" || strings.Contains(strings.ToLower(trimmed), "results are truncated") {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	paths := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		path := strings.TrimSpace(line)
		if !isClaudeGlobPath(path) {
			return nil
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return &core.ToolMatches{Kind: "paths", Paths: paths}
}

func parseNativeToolMatches(content any) *core.ToolMatches {
	container, ok := content.(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := container["matches"]
	if !ok {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var matches core.ToolMatches
	if json.Unmarshal(encoded, &matches) != nil || !validToolMatches(matches) {
		return nil
	}
	return &matches
}

func validToolMatches(matches core.ToolMatches) bool {
	switch matches.Kind {
	case "count":
		return matches.Count != nil && *matches.Count >= 0
	case "paths":
		return matches.Paths != nil
	case "detailed":
		return matches.Items != nil
	default:
		return false
	}
}

func isClaudeGlobPath(path string) bool {
	if path == "" || path == "." || path == ".." {
		return false
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return false
		}
	}
	return !strings.HasPrefix(path, "(") && !strings.HasPrefix(path, "{") && !strings.HasPrefix(path, "[")
}
