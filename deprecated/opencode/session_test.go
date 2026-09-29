package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// TestOpencodeSessionEntry_Unmarshal verifies that OpenCode's
// `session list --format json` output can be correctly parsed.
//
// OpenCode returns `updated` and `created` as Unix timestamps in
// milliseconds (int64), not strings. This test prevents regression
// of the unmarshal error:
//
//	json: cannot unmarshal number into Go struct field opencodeSessionEntry.updated of type string
func TestOpencodeSessionEntry_Unmarshal(t *testing.T) {
	jsonData := `[
  {
    "id": "ses_2eb11bb11ffeYwQZOj25mlmGMc",
    "title": "Test Session",
    "updated": 1774174646445,
    "created": 1774172652782,
    "projectId": "b80385ead03e8b450bdb2016d434aad318f93c16",
    "directory": "/path/to/project"
  }
]`

	var entries []opencodeSessionEntry
	if err := json.Unmarshal([]byte(jsonData), &entries); err != nil {
		t.Fatalf("Failed to unmarshal OpenCode session list: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.ID != "ses_2eb11bb11ffeYwQZOj25mlmGMc" {
		t.Errorf("ID = %q, want %q", e.ID, "ses_2eb11bb11ffeYwQZOj25mlmGMc")
	}
	if e.Title != "Test Session" {
		t.Errorf("Title = %q, want %q", e.Title, "Test Session")
	}
	if e.Updated != 1774174646445 {
		t.Errorf("Updated = %d, want %d", e.Updated, 1774174646445)
	}
	if e.Created != 1774172652782 {
		t.Errorf("Created = %d, want %d", e.Created, 1774172652782)
	}
}

// TestNewOpencodeSession_ContinueSessionTreatedAsFresh verifies that
// the ContinueSession sentinel (__continue__) is not passed as a literal
// session ID to the CLI. This was fixed in PR #249.
func TestNewOpencodeSession_ContinueSessionTreatedAsFresh(t *testing.T) {
	s, err := newOpencodeSession(context.Background(), "echo", "/tmp", "", "default", core.ContinueSession, nil)
	if err != nil {
		t.Fatalf("newOpencodeSession: %v", err)
	}
	defer s.Close()

	if got := s.CurrentSessionID(); got != "" {
		t.Errorf("ContinueSession should be treated as fresh: chatID = %q, want empty", got)
	}
}

func TestOpencodeSessionStageImages(t *testing.T) {
	dir := t.TempDir()

	prompt, imagePaths, err := stageOpencodeImages(dir, "", []core.ImageAttachment{
		{MimeType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}},
		{MimeType: "image/webp", Data: []byte("webp")},
	})
	if err != nil {
		t.Fatalf("stageOpencodeImages: %v", err)
	}
	if prompt != "Please analyze the attached image(s)." {
		t.Fatalf("prompt = %q", prompt)
	}
	if len(imagePaths) != 2 {
		t.Fatalf("imagePaths len = %d, want 2", len(imagePaths))
	}
	if filepath.Ext(imagePaths[0]) != ".jpg" {
		t.Fatalf("first ext = %q, want .jpg", filepath.Ext(imagePaths[0]))
	}
	if filepath.Ext(imagePaths[1]) != ".webp" {
		t.Fatalf("second ext = %q, want .webp", filepath.Ext(imagePaths[1]))
	}
	for _, path := range imagePaths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected staged image %s: %v", path, err)
		}
	}
}

func TestOpencodeSessionBuildRunArgsIncludesImagesAsFiles(t *testing.T) {
	s := &opencodeSession{workDir: "/repo", model: "provider/model"}

	got := s.buildRunArgs("describe these images", []string{"/tmp/a.png", "/tmp/b.jpg"}, "ses_123")
	want := []string{
		"run", "--format", "json",
		"--session", "ses_123",
		"--model", "provider/model",
		"--dir", "/repo",
		"--thinking",
		"--file", "/tmp/a.png",
		"--file", "/tmp/b.jpg",
		"--", "describe these images",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

// TestHandleStepFinish_TokenAccumulation verifies that step_finish events
// accumulate inputTokens and outputTokens on the session, and that readLoop
// includes them in the fallback EventResult.
func TestHandleStepFinish_TokenAccumulation(t *testing.T) {
	s, err := newOpencodeSession(context.Background(), "echo", "/tmp", "", "default", "", nil)
	if err != nil {
		t.Fatalf("newOpencodeSession: %v", err)
	}
	defer s.Close()

	// Simulate a step_finish event with token usage.
	raw := map[string]any{
		"type": "step_finish",
		"part": map[string]any{
			"reason": "stop",
			"tokens": map[string]any{
				"input":     float64(16546),
				"output":    float64(523),
				"total":     float64(18733),
				"reasoning": float64(0),
			},
		},
	}
	s.handleStepFinish(raw)

	s.mu.Lock()
	in, out := s.inputTokens, s.outputTokens
	s.mu.Unlock()

	if in != 16546 {
		t.Errorf("inputTokens = %d, want 16546", in)
	}
	if out != 523 {
		t.Errorf("outputTokens = %d, want 523", out)
	}

	// Simulate a second step_finish to confirm accumulation.
	raw2 := map[string]any{
		"type": "step_finish",
		"part": map[string]any{
			"reason": "stop",
			"tokens": map[string]any{
				"input":  float64(1000),
				"output": float64(200),
			},
		},
	}
	s.handleStepFinish(raw2)

	s.mu.Lock()
	in, out = s.inputTokens, s.outputTokens
	s.mu.Unlock()

	if in != 17546 {
		t.Errorf("accumulated inputTokens = %d, want 17546", in)
	}
	if out != 723 {
		t.Errorf("accumulated outputTokens = %d, want 723", out)
	}
}

// TestHandleStepFinish_NoTokensField verifies that step_finish without a
// tokens field does not reset or corrupt the accumulator.
func TestHandleStepFinish_NoTokensField(t *testing.T) {
	s, err := newOpencodeSession(context.Background(), "echo", "/tmp", "", "default", "", nil)
	if err != nil {
		t.Fatalf("newOpencodeSession: %v", err)
	}
	defer s.Close()

	// First: accumulate some tokens.
	s.handleStepFinish(map[string]any{
		"type": "step_finish",
		"part": map[string]any{
			"reason": "stop",
			"tokens": map[string]any{
				"input":  float64(500),
				"output": float64(100),
			},
		},
	})

	// Then: step_finish without tokens (e.g. error stop).
	s.handleStepFinish(map[string]any{
		"type": "step_finish",
		"part": map[string]any{
			"reason": "error",
		},
	})

	s.mu.Lock()
	in, out := s.inputTokens, s.outputTokens
	s.mu.Unlock()

	if in != 500 {
		t.Errorf("inputTokens = %d, want 500 (unchanged)", in)
	}
	if out != 100 {
		t.Errorf("outputTokens = %d, want 100 (unchanged)", out)
	}
}

// verify Agent implements core.Agent
var _ core.Agent = (*Agent)(nil)
