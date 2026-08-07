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

	var envelope struct {
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return core.PrContent{}, fmt.Errorf("claude PR generation returned invalid JSON: %w", err)
	}
	raw := envelope.StructuredOutput
	if len(raw) > 0 && raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return core.PrContent{}, fmt.Errorf("claude PR generation returned invalid structured output: %w", err)
		}
		raw = []byte(encoded)
	}
	var parsed core.PrContent
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return core.PrContent{}, fmt.Errorf("claude PR generation returned invalid structured output: %w", err)
	}
	title := strings.TrimSpace(parsed.Title)
	body := strings.TrimSpace(parsed.Body)
	if title == "" || body == "" {
		return core.PrContent{}, fmt.Errorf("claude PR generation returned empty title/body")
	}
	return core.PrContent{Title: title, Body: body}, nil
}
