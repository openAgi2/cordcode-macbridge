package core

import (
	"context"
	"strings"
)

// CommitMessageInput carries the staged/unstaged diff context so the agent can
// generate a truthful, conventional commit message on the Mac side.
// Generation is one-off and non-interactive (mirrors PrContentGenerator); it never
// touches a chat session or the timeline.
type CommitMessageInput struct {
	Cwd         string
	DiffSummary string // `git diff --stat` (staged + unstaged)
	DiffPatch   string // unified diff of the changes being committed
	Hint        string // optional user-provided hint/seed (may be "")
}

// CommitMessage is the generated commit subject and body.
type CommitMessage struct {
	Message string // full commit message: first line subject + optional body
}

// CommitMessageGenerator is implemented by agents that can run a one-off,
// non-interactive prompt to draft a commit message. Agents that do not implement
// it must not advertise supports_commit_message. Modeled on PrContentGenerator.
type CommitMessageGenerator interface {
	GenerateCommitMessage(ctx context.Context, input CommitMessageInput) (CommitMessage, error)
}

// BuildCommitMessagePrompt asks the model for a conventional commit message from the
// real diff. The model returns a single string (the full message); the caller uses it
// verbatim as `git commit -m`.
func BuildCommitMessagePrompt(input CommitMessageInput) string {
	var b strings.Builder
	b.WriteString("You write a git commit message for the staged/unstaged changes below.\n")
	b.WriteString("Return a JSON object with a single key \"message\" whose value is the full commit message.\n")
	b.WriteString("Rules:\n")
	b.WriteString("- first line is a concise imperative subject (<=72 chars), no trailing period\n")
	b.WriteString("- if the change benefits from detail, add a blank line then a wrapped body\n")
	b.WriteString("- do not invent changes not present in the diff; stay truthful\n")
	b.WriteString("- prefer Conventional Commits style (e.g. \"feat: ...\", \"fix: ...\") when it fits\n")
	if hint := strings.TrimSpace(input.Hint); hint != "" {
		b.WriteString("- honor the user's hint/seed where consistent with the diff\n")
		b.WriteString("\nUser hint:\n")
		b.WriteString(limitPRContext(hint, 4_000))
		b.WriteString("\n")
	}
	b.WriteString("\nDiff stat:\n")
	b.WriteString(limitPRContext(input.DiffSummary, 12_000))
	b.WriteString("\n\nDiff patch:\n")
	b.WriteString(limitPRContext(input.DiffPatch, 40_000))
	b.WriteString("\n")
	return b.String()
}
