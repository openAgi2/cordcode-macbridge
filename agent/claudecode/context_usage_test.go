package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInferClaudeContextWindow(t *testing.T) {
	if got := inferClaudeContextWindow("claude-sonnet-4-6", 0); got != 200000 {
		t.Fatalf("sonnet window = %d", got)
	}
	if got := inferClaudeContextWindow("claude-opus-4-8[1M]", 0); got != 1000000 {
		t.Fatalf("1m window = %d", got)
	}
	if got := inferClaudeContextWindow("sonnet", 180000); got != 180000 {
		t.Fatalf("override = %d", got)
	}
}

func TestOccupancyFromClaudeUsage(t *testing.T) {
	usage := occupancyFromClaudeUsageMap(map[string]any{
		"input_tokens":                float64(1824),
		"cache_read_input_tokens":     float64(84800),
		"cache_creation_input_tokens": float64(0),
		"output_tokens":               float64(5739),
	}, "claude-sonnet-4-6", 0)
	if usage == nil {
		t.Fatal("usage = nil")
	}
	if usage.UsedTokens != 1824+84800 {
		t.Fatalf("UsedTokens = %d, want 86624", usage.UsedTokens)
	}
	if usage.ContextWindow != 200000 {
		t.Fatalf("ContextWindow = %d", usage.ContextWindow)
	}
	if usage.OutputTokens != 5739 {
		t.Fatalf("OutputTokens = %d", usage.OutputTokens)
	}
}

func TestLoadClaudeContextUsageFromTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	body := `{"type":"user","message":{"role":"user"}}
{"type":"assistant","message":{"id":"a1","model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":2,"cache_creation_input_tokens":1},"content":[{"type":"text","text":"old"}]}}
{"type":"assistant","message":{"id":"a2","model":"claude-sonnet-4-6","usage":{"input_tokens":1824,"output_tokens":100,"cache_read_input_tokens":84800},"content":[{"type":"text","text":"new"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	usage := loadClaudeContextUsage(path, "sonnet", 0)
	if usage == nil || usage.UsedTokens != 1824+84800 {
		t.Fatalf("usage = %+v", usage)
	}
}
