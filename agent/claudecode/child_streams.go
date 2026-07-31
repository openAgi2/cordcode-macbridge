package claudecode

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	"github.com/openAgi2/cordcode-macbridge/core"
)

const maxClaudeChildStreamDepth = 8

type childStreamScope struct {
	StreamID       string
	ParentStreamID string
}

type claudeChildStreamTracker struct {
	mu          sync.Mutex
	toolOwners  map[string]string
	agentParent map[string]string
	warned      map[string]struct{}
}

var agentIDLine = regexp.MustCompile(`(?m)^agentId:\s*([A-Za-z0-9_-]+)\b`)

func newClaudeChildStreamTracker() *claudeChildStreamTracker {
	return &claudeChildStreamTracker{
		toolOwners:  make(map[string]string),
		agentParent: make(map[string]string),
		warned:      make(map[string]struct{}),
	}
}

func (t *claudeChildStreamTracker) observe(raw map[string]any) childStreamScope {
	// Some focused tests construct claudeSession directly instead of using the
	// production constructor. Preserve the historical flat-stream behavior.
	if t == nil {
		return childStreamScope{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	explicitStream := firstString(raw, "streamId", "stream_id")
	explicitParent := firstString(raw, "parentStreamId", "parent_stream_id")
	agentID := firstString(raw, "agentId", "agent_id")
	parentToolUseID := firstString(raw, "parentToolUseId", "parent_tool_use_id")
	if explicitStream != "" {
		return childStreamScope{StreamID: explicitStream, ParentStreamID: explicitParent}
	}

	ownerStream := ""
	parentKnown := false
	if parentToolUseID != "" {
		ownerStream, parentKnown = t.toolOwners[parentToolUseID]
	}
	if agentID != "" && !parentKnown {
		ownerStream, parentKnown = t.agentParent[agentID]
	}
	scope := childStreamScope{}
	if agentID != "" && (parentKnown || explicitParent != "") {
		scope = childStreamScope{StreamID: "claude-agent:" + agentID, ParentStreamID: firstNonEmpty(explicitParent, ownerStream)}
	}

	// Register Agent/Task tool ownership using the enclosing proven stream. The
	// main stream is represented by an empty owner and creates a root child.
	if message, ok := raw["message"].(map[string]any); ok {
		if content, ok := message["content"].([]any); ok {
			for _, value := range content {
				item, ok := value.(map[string]any)
				if !ok {
					continue
				}
				switch item["type"] {
				case "tool_use":
					name, _ := item["name"].(string)
					id, _ := item["id"].(string)
					if id != "" && (name == "Agent" || name == "Task") {
						t.toolOwners[id] = scope.StreamID
					}
				case "tool_result":
					toolID, _ := item["tool_use_id"].(string)
					for _, discovered := range extractAgentIDs(item["content"]) {
						t.agentParent[discovered] = t.toolOwners[toolID]
					}
				}
			}
		}
	}

	if agentID != "" && scope.StreamID == "" {
		key := shortDiagnosticID(agentID)
		if _, exists := t.warned[key]; !exists {
			t.warned[key] = struct{}{}
			slog.Warn("claudeSession: child stream parent unresolved; keeping event flat", "agent_hash", key)
		}
	}
	return scope
}

func (cs *claudeSession) scopeEvent(event core.Event) core.Event {
	if event.StreamID == "" {
		event.StreamID = cs.currentStream.StreamID
	}
	if event.ParentStreamID == "" {
		event.ParentStreamID = cs.currentStream.ParentStreamID
	}
	return event
}

func extractAgentIDs(content any) []string {
	var texts []string
	switch value := content.(type) {
	case string:
		texts = append(texts, value)
	case []any:
		for _, raw := range value {
			if item, ok := raw.(map[string]any); ok {
				if text, ok := item["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
	}
	var ids []string
	for _, text := range texts {
		for _, match := range agentIDLine.FindAllStringSubmatch(text, -1) {
			ids = append(ids, match[1])
		}
	}
	return ids
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func shortDiagnosticID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:4])
}

type ClaudeGraphRecord struct {
	UUID           string   `json:"uuid"`
	ParentUUID     string   `json:"parentUuid,omitempty"`
	AgentID        string   `json:"agentId,omitempty"`
	IsSidechain    bool     `json:"isSidechain,omitempty"`
	ToolUseID      string   `json:"toolUseId,omitempty"`
	ToolName       string   `json:"toolName,omitempty"`
	ResultAgentIDs []string `json:"resultAgentIds,omitempty"`
}

type ClaudeChildStreamNode struct {
	StreamID       string
	ParentStreamID string
	Flat           bool
	Diagnostic     string
}

// BuildClaudeChildStreamGraph resolves the complete transcript in passes, so
// result metadata can link an agent back to the exact Task/Agent tool use even
// when the sidechain file was written first. It never uses "most recent Task".
func BuildClaudeChildStreamGraph(records []ClaudeGraphRecord) map[string]ClaudeChildStreamNode {
	toolOwner := make(map[string]string)
	for _, record := range records {
		if record.ToolUseID == "" || (record.ToolName != "Agent" && record.ToolName != "Task") {
			continue
		}
		owner := ""
		if record.AgentID != "" {
			owner = "claude-agent:" + record.AgentID
		}
		toolOwner[record.ToolUseID] = owner
	}
	agentParent := make(map[string]string)
	for _, record := range records {
		for _, agentID := range record.ResultAgentIDs {
			agentParent[agentID] = toolOwner[record.ToolUseID]
		}
	}
	nodes := make(map[string]ClaudeChildStreamNode)
	for _, record := range records {
		if !record.IsSidechain || record.AgentID == "" {
			continue
		}
		streamID := "claude-agent:" + record.AgentID
		nodes[streamID] = ClaudeChildStreamNode{StreamID: streamID, ParentStreamID: agentParent[record.AgentID]}
	}
	for id, node := range nodes {
		seen := map[string]struct{}{id: {}}
		parent := node.ParentStreamID
		depth := 0
		for parent != "" {
			depth++
			if depth > maxClaudeChildStreamDepth {
				node.Flat, node.Diagnostic = true, "max_depth"
				break
			}
			if _, exists := seen[parent]; exists {
				node.Flat, node.Diagnostic = true, "cycle"
				break
			}
			seen[parent] = struct{}{}
			parentNode, exists := nodes[parent]
			if !exists {
				node.Flat, node.Diagnostic = true, "orphan_parent"
				break
			}
			parent = parentNode.ParentStreamID
		}
		nodes[id] = node
	}
	return nodes
}

func normalizeFixtureLabel(value string) string { return strings.TrimSpace(strings.ToLower(value)) }
