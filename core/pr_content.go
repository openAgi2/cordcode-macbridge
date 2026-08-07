package core

import (
	"context"
	"strings"
)

// PrContentInput carries the real diff and repository template so the agent can
// generate a truthful PR title/body on the Mac side (T3-style).
type PrContentInput struct {
	Cwd           string
	BaseBranch    string
	HeadBranch    string
	CommitSummary string
	DiffSummary   string
	DiffPatch     string
	Template      string
}

// PrContent is the generated pull request title and body.
type PrContent struct {
	Title string
	Body  string
}

// PrContentGenerator is implemented by agents that can run a one-off,
// non-interactive generation prompt. Agents that do not implement it must not
// advertise supports_pull_requests.
type PrContentGenerator interface {
	GeneratePrContent(ctx context.Context, input PrContentInput) (PrContent, error)
}

// BuildPrContentPrompt mirrors the T3 prompt: real commits/diff + template
// (if any) are fed to the model; the model is the merger.
func BuildPrContentPrompt(input PrContentInput) string {
	var b strings.Builder
	b.WriteString("You write source control change request content.\n")
	b.WriteString("Return a JSON object with keys: title, body.\n")
	b.WriteString("Rules:\n")
	b.WriteString("- title should be concise and specific\n")
	template := strings.TrimSpace(input.Template)
	if template != "" {
		b.WriteString("- body must be markdown and follow the repository change request template structure\n")
		b.WriteString("- fill in the template sections appropriately for this change\n")
		b.WriteString("- drop HTML comments from the template in the generated body\n")
		b.WriteString("- keep the template's markdown structure\n")
	} else {
		b.WriteString("- body must be markdown and include headings '## Summary' and '## Testing'\n")
		b.WriteString("- under Summary, provide short bullet points\n")
		b.WriteString("- under Testing, include bullet points with concrete checks or 'Not run' where appropriate\n")
	}
	if template != "" {
		b.WriteString("\nRepository change request template:\n")
		b.WriteString(limitPRContext(template, 8_000))
		b.WriteString("\n")
	}
	b.WriteString("\nBase branch: ")
	b.WriteString(input.BaseBranch)
	b.WriteString("\nHead branch: ")
	b.WriteString(input.HeadBranch)
	b.WriteString("\n\nCommits:\n")
	b.WriteString(limitPRContext(input.CommitSummary, 12_000))
	b.WriteString("\n\nDiff stat:\n")
	b.WriteString(limitPRContext(input.DiffSummary, 12_000))
	b.WriteString("\n\nDiff patch:\n")
	b.WriteString(limitPRContext(input.DiffPatch, 40_000))
	b.WriteString("\n")
	return b.String()
}

func limitPRContext(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n[truncated]"
}
