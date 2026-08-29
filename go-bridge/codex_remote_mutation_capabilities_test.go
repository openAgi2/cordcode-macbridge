package gobridge

import (
	"testing"

	codexremote "github.com/openAgi2/cordcode-macbridge/agent/codex-remote"
)

func TestCodexRemoteAdvertisesMutationAndWorkDirCapabilities(t *testing.T) {
	agent := codexremote.New(nil)
	caps := deriveBackendCapabilities(codexremote.BackendID, agent, "")
	want := map[string]bool{
		"session_mutation": false,
		"session_delete":   false,
		"workspace_diff":   false,
	}
	for _, capability := range caps {
		if _, required := want[capability]; required {
			want[capability] = true
		}
	}
	for capability, present := range want {
		if !present {
			t.Fatalf("codex-remote capability %q missing from %v", capability, caps)
		}
	}
}
