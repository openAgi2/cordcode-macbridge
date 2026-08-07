package codex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.PrContentGenerator = (*Agent)(nil)

const codexPrSchema = `{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"}},"required":["title","body"]}`

// GeneratePrContent runs `codex exec` in ephemeral read-only mode and parses the
// structured output file. It never creates a persisted chat session.
func (a *Agent) GeneratePrContent(ctx context.Context, input core.PrContentInput) (core.PrContent, error) {
	bin := a.cliBin
	if bin == "" {
		bin = "codex"
	}
	schemaFile, err := os.CreateTemp("", "cordcode-pr-schema-*.json")
	if err != nil {
		return core.PrContent{}, fmt.Errorf("codex PR generation: create schema file: %w", err)
	}
	defer os.Remove(schemaFile.Name())
	if _, err := schemaFile.WriteString(codexPrSchema); err != nil {
		schemaFile.Close()
		return core.PrContent{}, fmt.Errorf("codex PR generation: write schema file: %w", err)
	}
	schemaFile.Close()

	outputFile, err := os.CreateTemp("", "cordcode-pr-output-*.json")
	if err != nil {
		return core.PrContent{}, fmt.Errorf("codex PR generation: create output file: %w", err)
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
	cmd.Stdin = strings.NewReader(core.BuildPrContentPrompt(input))
	if out, err := cmd.CombinedOutput(); err != nil {
		return core.PrContent{}, fmt.Errorf("codex PR generation failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		return core.PrContent{}, fmt.Errorf("codex PR generation: read output: %w", err)
	}
	var parsed core.PrContent
	if err := core.UnmarshalJSONPayload(raw, &parsed); err != nil {
		return core.PrContent{}, fmt.Errorf("codex PR generation returned invalid output: %w", err)
	}
	parsed.Title = strings.TrimSpace(parsed.Title)
	parsed.Body = strings.TrimSpace(parsed.Body)
	if parsed.Title == "" || parsed.Body == "" {
		return core.PrContent{}, fmt.Errorf("codex PR generation returned empty title/body")
	}
	return parsed, nil
}
