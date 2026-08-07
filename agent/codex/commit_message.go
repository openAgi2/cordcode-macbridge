package codex

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

const codexCommitSchema = `{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`

// GenerateCommitMessage runs `codex exec` in ephemeral read-only mode and parses the
// structured output file to draft a commit message. It never creates a persisted chat
// session or touches the timeline.
func (a *Agent) GenerateCommitMessage(ctx context.Context, input core.CommitMessageInput) (core.CommitMessage, error) {
	bin := a.cliBin
	if bin == "" {
		bin = "codex"
	}
	schemaFile, err := os.CreateTemp("", "cordcode-commit-schema-*.json")
	if err != nil {
		return core.CommitMessage{}, fmt.Errorf("codex commit message generation: create schema file: %w", err)
	}
	defer os.Remove(schemaFile.Name())
	if _, err := schemaFile.WriteString(codexCommitSchema); err != nil {
		schemaFile.Close()
		return core.CommitMessage{}, fmt.Errorf("codex commit message generation: write schema file: %w", err)
	}
	schemaFile.Close()

	outputFile, err := os.CreateTemp("", "cordcode-commit-output-*.json")
	if err != nil {
		return core.CommitMessage{}, fmt.Errorf("codex commit message generation: create output file: %w", err)
	}
	outputPath := outputFile.Name()
	outputFile.Close()
	defer os.Remove(outputPath)

	args := []string{
		"exec",
		"--ephemeral",
		"--skip-git-repo-check",
		"-s", "read-only",
		"--output-schema", schemaFile.Name(),
		"--output-last-message", outputPath,
	}
	if a.model != "" {
		args = append(args, "--model", a.model)
	}
	args = append(args, "-")

	env := append(os.Environ(), a.sessionEnv...)
	if a.codexHome != "" {
		env = append(env, "CODEX_HOME="+a.codexHome)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = input.Cwd
	cmd.Env = env
	cmd.Stdin = strings.NewReader(core.BuildCommitMessagePrompt(input))
	if out, err := cmd.CombinedOutput(); err != nil {
		return core.CommitMessage{}, fmt.Errorf("codex commit message generation failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		return core.CommitMessage{}, fmt.Errorf("codex commit message generation: read output: %w", err)
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return core.CommitMessage{}, fmt.Errorf("codex commit message generation returned invalid output envelope: %w", err)
	}
	var parsed struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(encoded), &parsed); err != nil {
		return core.CommitMessage{}, fmt.Errorf("codex commit message generation returned invalid JSON: %w", err)
	}
	message := strings.TrimSpace(parsed.Message)
	if message == "" {
		return core.CommitMessage{}, fmt.Errorf("codex commit message generation returned empty message")
	}
	return core.CommitMessage{Message: message}, nil
}
