package toolinventory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyInstallOriginExactNPMPrefix(t *testing.T) {
	root := t.TempDir()
	pkg := "@anthropic-ai/claude-code"
	artifact := filepath.Join(root, "lib", "node_modules", pkg, "bin", "claude")
	npm := filepath.Join(root, "bin", "npm")
	for _, path := range []string{artifact, npm} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	product, _ := ProductByID("claude")
	origin, err := ClassifyInstallOrigin(product, artifact, OriginDependencies{})
	if err != nil {
		t.Fatal(err)
	}
	expectedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if origin.Method != InstallMethodNPM || origin.PackageID != pkg || origin.Prefix != expectedRoot || origin.ManagerPath != filepath.Join(expectedRoot, "bin", "npm") {
		t.Fatalf("unexpected origin: %+v", origin)
	}
}

func TestClassifyInstallOriginDoesNotFallbackToAmbientNPM(t *testing.T) {
	root := t.TempDir()
	pkg := "opencode-ai"
	artifact := filepath.Join(root, "lib", "node_modules", pkg, "bin", "opencode")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	product, _ := ProductByID("opencode-web")
	origin, err := ClassifyInstallOrigin(product, artifact, OriginDependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if origin.Method != InstallMethodUnknown || origin.ManagerPath != "" {
		t.Fatalf("missing owning npm must stay unsupported: %+v", origin)
	}
}

func TestClassifyInstallOriginRejectsMissingArtifact(t *testing.T) {
	product, _ := ProductByID("grokbuild")
	if _, err := ClassifyInstallOrigin(product, filepath.Join(t.TempDir(), "missing"), OriginDependencies{}); err == nil {
		t.Fatal("expected missing artifact error")
	}
}
