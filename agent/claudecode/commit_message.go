package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.CommitMessageGenerator = (*Agent)(nil)

const claudeCommitSchema = `{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`

// GenerateCommitMessage runs the installed Claude CLI in print mode with structured
// JSON output to draft a commit message. It never touches a chat session or the timeline.
func (a *Agent) GenerateCommitMessage(ctx context.Context, input core.CommitMessageInput) (core.CommitMessage, error) {
	bin := a.cliBin
	if bin == "" {
		bin = "claude"
	}
	args := []string{
		"-p",
		"--output-format", "json",
		"--json-schema", claudeCommitSchema,
		"--dangerously-skip-permissions",
	}
	if a.model != "" {
		args = append(args, "--model", a.model)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = input.Cwd
	cmd.Env = append(os.Environ(), a.sessionEnv...)
	cmd.Stdin = strings.NewReader(core.BuildCommitMessagePrompt(input))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return core.CommitMessage{}, fmt.Errorf("claude commit message generation failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var envelope struct {
		Result           json.RawMessage `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return core.CommitMessage{}, fmt.Errorf("claude commit message generation returned invalid JSON: %w", err)
	}
	raw := envelope.Result
	if len(raw) == 0 {
		raw = envelope.StructuredOutput
	}
	if len(raw) > 0 && raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return core.CommitMessage{}, fmt.Errorf("claude commit message generation returned invalid structured output: %w", err)
		}
		raw = []byte(encoded)
	}
	var parsed struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return core.CommitMessage{}, fmt.Errorf("claude commit message generation returned invalid structured output: %w", err)
	}
	message := strings.TrimSpace(parsed.Message)
	if message == "" {
		return core.CommitMessage{}, fmt.Errorf("claude commit message generation returned empty message")
	}
	return core.CommitMessage{Message: message}, nil
}
