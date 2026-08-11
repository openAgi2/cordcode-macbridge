package claudecode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/openAgi2/cordcode-macbridge/core"
)

var _ core.PrContentGenerator = (*Agent)(nil)

const claudePrSchema = `{"type":"object","properties":{"title":{"type":"string"},"body":{"type":"string"}},"required":["title","body"]}`

// GeneratePrContent runs the installed Claude CLI in print mode with structured
// JSON output. It never touches a chat session or the timeline.
func (a *Agent) GeneratePrContent(ctx context.Context, input core.PrContentInput) (core.PrContent, error) {
	bin := a.cliBin
	if bin == "" {
		bin = "claude"
	}
	args := []string{
		"-p",
		"--output-format", "json",
		"--json-schema", claudePrSchema,
		"--dangerously-skip-permissions",
	}
	if a.model != "" {
		args = append(args, "--model", a.model)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = input.Cwd
	cmd.Env = append(os.Environ(), a.sessionEnv...)
	cmd.Stdin = strings.NewReader(core.BuildPrContentPrompt(input))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return core.PrContent{}, fmt.Errorf("claude PR generation failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var parsed core.PrContent
	if err := core.UnmarshalClaudePrintStructured(out, &parsed); err != nil {
		return core.PrContent{}, fmt.Errorf("claude PR generation returned invalid structured output: %w", err)
	}
	title := strings.TrimSpace(parsed.Title)
	body := strings.TrimSpace(parsed.Body)
	if title == "" || body == "" {
		return core.PrContent{}, fmt.Errorf("claude PR generation returned empty title/body")
	}
	return core.PrContent{Title: title, Body: body}, nil
}
