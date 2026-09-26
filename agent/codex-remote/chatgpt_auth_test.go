package codexremote

import "testing"

func TestResolveChatGPTCodexPathPrefersPackageLayout(t *testing.T) {
	const packageLayoutBin = "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex"
	got := resolveChatGPTCodexPath(func(p string) bool { return p == packageLayoutBin })
	if got != packageLayoutBin {
		t.Fatalf("expected package-layout binary %s, got %s", packageLayoutBin, got)
	}
}

func TestResolveChatGPTCodexPathFallsBackToLegacyFlatBinary(t *testing.T) {
	const legacyBin = "/Applications/ChatGPT.app/Contents/Resources/codex"
	got := resolveChatGPTCodexPath(func(p string) bool { return p == legacyBin })
	if got != legacyBin {
		t.Fatalf("expected legacy flat binary %s, got %s", legacyBin, got)
	}
}

func TestResolveChatGPTCodexPathMissingKeepsLegacyForInstallGuidance(t *testing.T) {
	const legacyBin = "/Applications/ChatGPT.app/Contents/Resources/codex"
	got := resolveChatGPTCodexPath(func(string) bool { return false })
	if got != legacyBin {
		t.Fatalf("expected missing install to keep legacy path %s for install guidance, got %s", legacyBin, got)
	}
}
