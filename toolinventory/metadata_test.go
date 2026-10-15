package toolinventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePackageArtifact(t *testing.T, pkg string, manifest string) string {
	t.Helper()
	artifact := filepath.Join(t.TempDir(), "lib", "node_modules", pkg, "bin", "tool")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(artifact)), "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return artifact
}

func TestReadNPMPackageMetadataExactIdentity(t *testing.T) {
	artifact := writePackageArtifact(t, "opencode-ai", `{"name":"opencode-ai","version":"1.19.0"}`)
	product, _ := ProductByID("opencode-web")
	metadata, err := ReadNPMPackageMetadata(product, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "opencode-ai" || metadata.Version != "1.19.0" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
}

func TestReadNPMPackageMetadataRejectsNameMismatch(t *testing.T) {
	artifact := writePackageArtifact(t, "opencode-ai", `{"name":"other-package","version":"1.19.0"}`)
	product, _ := ProductByID("opencode-web")
	if _, err := ReadNPMPackageMetadata(product, artifact); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("expected identity mismatch, got %v", err)
	}
}

func TestReadNPMPackageMetadataRejectsMalformedJSON(t *testing.T) {
	artifact := writePackageArtifact(t, "@deepseek-ai/dsh", `{"name":"@deepseek-ai/dsh","version":`)
	product, _ := ProductByID("dsh-web")
	if _, err := ReadNPMPackageMetadata(product, artifact); err == nil {
		t.Fatal("expected decode error")
	}
}
