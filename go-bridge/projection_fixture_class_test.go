package gobridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// P0 baseline fixtures: three sanitized REAL class projections (tool-dense /
// oversized-output / long-text) captured through the production Kernel pipeline
// (see docs/protocol/samples/session-projection-v2/fixtures/README.md). These
// tests pin the class-defining properties so the fixtures cannot silently rot
// into synthetic short samples.
func loadFixtureProjection(t *testing.T, class string) SessionProjection {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "..", "docs", "protocol", "samples", "session-projection-v2", "fixtures", class+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture not present: %v", err)
	}
	var proj SessionProjection
	if err := json.Unmarshal(raw, &proj); err != nil {
		t.Fatalf("%s: canonical decode failed: %v", class, err)
	}
	if len(proj.Turns) == 0 || proj.SyncRev <= 0 {
		t.Fatalf("%s: empty projection (forbidden)", class)
	}
	return proj
}

func fixturePartBytes(proj SessionProjection) (text int, tool int, parts int) {
	for i := range proj.Turns {
		turn := &proj.Turns[i]
		for _, msg := range []*MessageProjection{turn.User, turn.Assistant} {
			if msg == nil {
				continue
			}
			for _, part := range msg.Parts {
				parts++
				b, _ := json.Marshal(part)
				if part.Type == "tool" {
					tool += len(b)
				} else {
					text += len(b)
				}
			}
		}
	}
	return text, tool, parts
}

func TestFixtureToolDenseClassProperties(t *testing.T) {
	proj := loadFixtureProjection(t, "tool-dense")
	_, tool, parts := fixturePartBytes(proj)
	if parts < 1000 {
		t.Fatalf("tool-dense fixture lost its class: parts=%d (<1000)", parts)
	}
	if tool < 4_000_000 {
		t.Fatalf("tool-dense fixture lost its class: tool bytes=%d (<4MB)", tool)
	}
}

func TestFixtureOversizedOutputClassProperties(t *testing.T) {
	proj := loadFixtureProjection(t, "oversized-output")
	_, tool, parts := fixturePartBytes(proj)
	if parts < 200 {
		t.Fatalf("oversized-output fixture lost its class: parts=%d (<200)", parts)
	}
	if tool < 2_000_000 {
		t.Fatalf("oversized-output fixture lost its class: tool bytes=%d (<2MB)", tool)
	}
}

func TestFixtureLongTextClassProperties(t *testing.T) {
	proj := loadFixtureProjection(t, "long-text")
	text, tool, parts := fixturePartBytes(proj)
	if parts < 500 {
		t.Fatalf("long-text fixture lost its class: parts=%d (<500)", parts)
	}
	if tool != 0 {
		t.Fatalf("long-text fixture must contain zero tool bytes, got %d", tool)
	}
	if text < 300_000 {
		t.Fatalf("long-text fixture lost its class: text bytes=%d (<300KB)", text)
	}
}

// Fixture ids must stay internally consistent: an execution activeTurnId that
// is present must resolve to a declared turn in every class fixture.
func TestFixtureExecutionReferencesResolve(t *testing.T) {
	for _, class := range []string{"tool-dense", "oversized-output", "long-text"} {
		proj := loadFixtureProjection(t, class)
		byID := map[string]bool{}
		for _, turn := range proj.Turns {
			byID[turn.TurnID] = true
		}
		if active := proj.Execution.ActiveTurnID; active != "" && !byID[active] {
			t.Fatalf("%s: execution activeTurnId %q does not resolve to a declared turn", class, active)
		}
	}
}
