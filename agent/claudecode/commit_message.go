package claudecode

import (
	"context"
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

	var parsed struct {
		Message string `json:"message"`
	}
	// Shared envelope handling: result/structured_output × object|string.
	if err := core.UnmarshalClaudePrintStructured(out, &parsed); err != nil {
		return core.CommitMessage{}, fmt.Errorf("claude commit message generation returned invalid structured output: %w", err)
	}
	message := strings.TrimSpace(parsed.Message)
	if message == "" {
		return core.CommitMessage{}, fmt.Errorf("claude commit message generation returned empty message")
	}
	return core.CommitMessage{Message: message}, nil
}
