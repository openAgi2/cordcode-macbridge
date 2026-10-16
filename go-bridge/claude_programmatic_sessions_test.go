package gobridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsClaudeProgrammaticSessionOfficialSet(t *testing.T) {
	tests := []struct {
		entrypoint  string
		sessionKind string
		want        bool
	}{
		{entrypoint: "sdk-cli", want: true},
		{entrypoint: "sdk-ts", want: true},
		{entrypoint: "sdk-py", want: true},
		{sessionKind: "daemon", want: true},
		{sessionKind: "daemon-worker", want: true},
		{entrypoint: "claude-desktop", want: false},
		{entrypoint: "claude-vscode", want: false},
		{entrypoint: "future-sdk", want: false},
		{sessionKind: "worker", want: false},
		{want: false},
	}
	for _, tt := range tests {
		if got := isClaudeProgrammaticSession(tt.entrypoint, tt.sessionKind); got != tt.want {
			t.Fatalf("isClaudeProgrammaticSession(%q, %q) = %v, want %v", tt.entrypoint, tt.sessionKind, got, tt.want)
		}
	}
}

func writeClaudeProgrammaticLines(t *testing.T, lines ...any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var builder strings.Builder
	for _, line := range lines {
		raw, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		builder.Write(raw)
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func scanClaudeProgrammaticFixture(t *testing.T, path string) claudeSessionScanResult {
	t.Helper()
	return scanClaudeSessionMetadata(path, time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC))
}

func TestScanClaudeProgrammaticSessionKindOfficialBranches(t *testing.T) {
	tests := []struct {
		name string
		rows []any
		want bool
	}{
		{
			name: "first parent line daemon",
			rows: []any{
				map[string]any{"type": "user", "parentUuid": nil, "sessionKind": "daemon"},
				map[string]any{"type": "user", "parentUuid": nil, "sessionKind": "not-used"},
			},
			want: true,
		},
		{
			name: "first parent line without sessionKind stops search",
			rows: []any{
				map[string]any{"type": "user", "parentUuid": nil},
				map[string]any{"type": "assistant", "parentUuid": nil, "sessionKind": "daemon"},
			},
			want: false,
		},
		{
			name: "no parent line searches whole head",
			rows: []any{
				map[string]any{"type": "custom-title", "sessionKind": "daemon"},
			},
			want: true,
		},
		{
			name: "no parent line first empty sessionKind wins",
			rows: []any{
				map[string]any{"type": "custom-title", "sessionKind": ""},
				map[string]any{"type": "assistant", "sessionKind": "daemon"},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scanClaudeProgrammaticFixture(t, writeClaudeProgrammaticLines(t, tt.rows...))
			if got.Programmatic != tt.want {
				t.Fatalf("Programmatic = %v, want %v (entrypoint=%q sessionKind=%q)", got.Programmatic, tt.want, got.Entrypoint, got.SessionKind)
			}
		})
	}
}

func TestScanClaudeEntrypointHeadHitSuppressesTailFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	var builder strings.Builder
	builder.WriteString(`{"type":"user","entrypoint":"claude-desktop","parentUuid":null}` + "\n")
	builder.WriteString(strings.Repeat("a", claudeSessionSummaryReadLimit))
	builder.WriteString(`` + "\n")
	builder.WriteString(`{"type":"assistant","entrypoint":"sdk-py"}` + "\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got := scanClaudeProgrammaticFixture(t, path)
	if got.Entrypoint != "claude-desktop" || got.Programmatic {
		t.Fatalf("scan = %+v, want head claude-desktop and visible", got)
	}
}

func TestScanClaudeEntrypointEmptyHeadHitSuppressesTailFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	var builder strings.Builder
	builder.WriteString(`{"type":"user","entrypoint":"","parentUuid":null}` + "\n")
	builder.WriteString(strings.Repeat("a", claudeSessionSummaryReadLimit))
	builder.WriteString(`` + "\n")
	builder.WriteString(`{"type":"assistant","entrypoint":"sdk-py"}` + "\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got := scanClaudeProgrammaticFixture(t, path)
	if got.Entrypoint != "" || got.Programmatic {
		t.Fatalf("scan = %+v, want empty head entrypoint and visible", got)
	}
}

func TestScanClaudeEntrypointTailFallbackUsesLastTopLevelHit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	var builder strings.Builder
	// Real large-reviewer shape: the 512KiB head cut lands inside the giant
	// user row that carries entrypoint after its padding, so that row cannot be
	// parsed whole. The tail fallback then finds a later attachment row.
	builder.WriteString(`{"type":"queue-operation","padding":"` + strings.Repeat("a", 96*1024) + `"}` + "\n")
	builder.WriteString(`{"type":"user","parentUuid":null,"padding":"` + strings.Repeat("b", 480*1024) + `","entrypoint":"sdk-py"}` + "\n")
	builder.WriteString(`{"type":"attachment","entrypoint":"sdk-cli"}` + "\n")
	builder.WriteString(`{"type":"attachment","entrypoint":"sdk-py"}` + "\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got := scanClaudeProgrammaticFixture(t, path)
	if got.Entrypoint != "sdk-py" || !got.Programmatic {
		t.Fatalf("scan = %+v, want last tail sdk-py and programmatic", got)
	}
}

func TestScanClaudeEntrypointTailSkipsFirstWindowLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	var builder strings.Builder

	// The 64KiB tail window starts exactly at the next valid JSON line; the first
	// window line is deliberately discarded as if it could have been partial.
	first := `{"type":"assistant","entrypoint":"sdk-cli"}` + "\n"
	last := `{"type":"assistant","entrypoint":"sdk-py"}` + "\n"
	fillerLen := claudeProgrammaticTailReadLimit - len(first) - len(last)
	filler := strings.Repeat("x", fillerLen-1) + "\n"
	builder.WriteString(strings.Repeat("y", claudeSessionSummaryReadLimit-1) + "\n")
	builder.WriteString(first)
	builder.WriteString(filler)
	builder.WriteString(last)
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got := scanClaudeProgrammaticFixture(t, path)
	if got.Entrypoint != "sdk-py" || !got.Programmatic {
		t.Fatalf("scan = %+v, want first window line skipped and last sdk-py used", got)
	}
}

func TestScanClaudeSessionKindDoesNotReadTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	var builder strings.Builder
	builder.WriteString(`{"type":"user","parentUuid":null,"padding":"` + strings.Repeat("a", 384*1024) + `"}` + "\n")
	builder.WriteString(`{"type":"assistant","parentUuid":null,"padding":"` + strings.Repeat("b", 256*1024) + `"}` + "\n")
	builder.WriteString(`{"type":"assistant","entrypoint":"claude-desktop","sessionKind":"daemon"}` + "\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got := scanClaudeProgrammaticFixture(t, path)
	if got.Entrypoint != "claude-desktop" || got.SessionKind != "" || got.Programmatic {
		t.Fatalf("scan = %+v, want tail sessionKind ignored", got)
	}
}
