package gobridge

import (
	"reflect"
	"testing"
)

func TestSplitDriverIDsPreservesExplicitEmpty(t *testing.T) {
	if got := splitDriverIDs(" "); len(got) != 0 {
		t.Fatalf("explicit whitespace should stay empty: %q", got)
	}
	got := splitDriverIDs(" claude , codex-remote ,, grokbuild ")
	if want := []string{"claude", "codex-remote", "grokbuild"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseAgentSelectionRevision(t *testing.T) {
	if value, err := parseAgentSelectionRevision(""); err != nil || value != nil {
		t.Fatalf("absent revision: value=%v err=%v", value, err)
	}
	value, err := parseAgentSelectionRevision("18446744073709551615")
	if err != nil || value == nil || *value != 18446744073709551615 {
		t.Fatalf("max revision: value=%v err=%v", value, err)
	}
	for _, invalid := range []string{"-1", "1.0", "0x10", " 1"} {
		if _, err := parseAgentSelectionRevision(invalid); err == nil {
			t.Fatalf("expected %q to fail", invalid)
		}
	}
}

func TestExplicitEmptyDriverSelectionPolicy(t *testing.T) {
	if !isExplicitEmptyDriverSelection(nil, true) {
		t.Fatal("provided empty drivers must be legal zero-agent mode")
	}
	if isExplicitEmptyDriverSelection(nil, false) {
		t.Fatal("omitted drivers must retain legacy no-agent failure")
	}
	if isExplicitEmptyDriverSelection([]string{"claude"}, true) {
		t.Fatal("nonempty explicit selection must not use zero-agent escape")
	}
}
