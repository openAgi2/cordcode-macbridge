package claudecode

import (
	"encoding/json"
	"os"
	"testing"
)

func TestClaudeChildStreamGraphRealShapeFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/child-stream-graph-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name    string              `json:"name"`
		Records []ClaudeGraphRecord `json:"records"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 5 {
		t.Fatalf("fixtures=%d", len(fixtures))
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			nodes := BuildClaudeChildStreamGraph(fixture.Records)
			if len(nodes) == 0 {
				t.Fatal("no child nodes")
			}
			for id, node := range nodes {
				if node.Flat {
					t.Fatalf("node %s unexpectedly flat: %s", id, node.Diagnostic)
				}
			}
			if fixture.Name == "nested-child" && nodes["claude-agent:a2"].ParentStreamID != "claude-agent:a1" {
				t.Fatalf("nested parent=%q", nodes["claude-agent:a2"].ParentStreamID)
			}
		})
	}
}

func TestClaudeChildStreamGraphOrphanCycleAndDepthFailOpen(t *testing.T) {
	cycle := []ClaudeGraphRecord{
		{ToolUseID: "t1", ToolName: "Agent", AgentID: "a2"}, {ToolUseID: "t1", ResultAgentIDs: []string{"a1"}},
		{ToolUseID: "t2", ToolName: "Agent", AgentID: "a1"}, {ToolUseID: "t2", ResultAgentIDs: []string{"a2"}},
		{AgentID: "a1", IsSidechain: true}, {AgentID: "a2", IsSidechain: true},
	}
	nodes := BuildClaudeChildStreamGraph(cycle)
	if !nodes["claude-agent:a1"].Flat || nodes["claude-agent:a1"].Diagnostic != "cycle" {
		t.Fatalf("cycle=%+v", nodes)
	}

	orphan := BuildClaudeChildStreamGraph([]ClaudeGraphRecord{{AgentID: "child", IsSidechain: true}, {ToolUseID: "t", ResultAgentIDs: []string{"child"}}, {ToolUseID: "t", ToolName: "Agent", AgentID: "missing"}})
	if !orphan["claude-agent:child"].Flat || orphan["claude-agent:child"].Diagnostic != "orphan_parent" {
		t.Fatalf("orphan=%+v", orphan)
	}

	var depth []ClaudeGraphRecord
	for i := 0; i < maxClaudeChildStreamDepth+2; i++ {
		agent := string(rune('a' + i))
		parent := ""
		if i > 0 {
			parent = string(rune('a' + i - 1))
		}
		tool := "t" + agent
		depth = append(depth, ClaudeGraphRecord{ToolUseID: tool, ToolName: "Agent", AgentID: parent}, ClaudeGraphRecord{ToolUseID: tool, ResultAgentIDs: []string{agent}}, ClaudeGraphRecord{AgentID: agent, IsSidechain: true})
	}
	nodes = BuildClaudeChildStreamGraph(depth)
	if !nodes["claude-agent:j"].Flat || nodes["claude-agent:j"].Diagnostic != "max_depth" {
		t.Fatalf("depth=%+v", nodes["claude-agent:j"])
	}
}

func TestClaudeChildTrackerUsesExactToolMappingAndScopesAllEvents(t *testing.T) {
	tracker := newClaudeChildStreamTracker()
	tracker.observe(map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Agent", "id": "task-1"}}}})
	tracker.observe(map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "task-1", "content": []any{map[string]any{"text": "agentId: a1 (sanitized metadata)"}}}}}})
	scope := tracker.observe(map[string]any{"agentId": "a1", "isSidechain": true})
	if scope.StreamID != "claude-agent:a1" || scope.ParentStreamID != "" {
		t.Fatalf("scope=%+v", scope)
	}
}
