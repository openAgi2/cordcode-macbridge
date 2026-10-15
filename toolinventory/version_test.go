package toolinventory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeVersionScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultVersionRunnerParsesLastStrictSemVerField(t *testing.T) {
	path := writeVersionScript(t, "#!/bin/sh\necho 'CLI 1.2.3-rc.2+build'\n")
	observation, err := DefaultVersionRunner().Run(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Err != nil || observation.Value != "1.2.3-rc.2+build" {
		t.Fatalf("unexpected observation: %+v", observation)
	}
}

func TestDefaultVersionRunnerTimesOutAndReapsProcessGroup(t *testing.T) {
	path := writeVersionScript(t, "#!/bin/sh\nsleep 5\n")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	observation, err := DefaultVersionRunner().Run(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.TimedOut || observation.Err == nil {
		t.Fatalf("expected timeout observation: %+v", observation)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("process group was not reaped promptly: %s", elapsed)
	}
}

func TestCompareStrictFollowsSemVerPrereleaseRules(t *testing.T) {
	tests := []struct {
		installed, target string
		want              int
	}{
		{"1.0.0", "1.0.0", 0},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0-2", "1.0.0-11a", -1},
		{"1.0.0+first", "1.0.0+second", 0},
	}
	for _, test := range tests {
		got, err := CompareStrict(test.installed, test.target)
		if err != nil {
			t.Fatalf("%s vs %s: %v", test.installed, test.target, err)
		}
		if got != test.want {
			t.Fatalf("%s vs %s: got %d, want %d", test.installed, test.target, got, test.want)
		}
	}
}

func TestCompareStrictRejectsLooseVersions(t *testing.T) {
	for _, value := range []string{"1", "1.2", "01.2.3", "1.2.3-01"} {
		if _, err := CompareStrict(value, "1.0.0"); err == nil {
			t.Fatalf("expected strict rejection for %q", value)
		}
	}
}
