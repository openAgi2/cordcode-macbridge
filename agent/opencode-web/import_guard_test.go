package opencodeweb

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// legacyImportPath is the hybrid package this backend physically replaces at
// the entry level but must never import (design §2.2 纪律 6 / §4.5).
const legacyImportPath = "github.com/openAgi2/cordcode-macbridge/agent/opencode"

// TestSourceContainsNoForbiddenSurfaces scans the package's non-test source
// files for the legacy hybrid's dirty surfaces (design §2.1 / §4.5):
// no CLI shelling of any kind, no sqlite access, no on-disk model cache file,
// no hard-coded fallback port. The forbidden fragments are assembled from
// concatenation so this guard file itself stays clean.
func TestSourceContainsNoForbiddenSurfaces(t *testing.T) {
	forbidden := map[string]string{
		"sqlite" + "3":                "direct storage access",
		"opencode " + "run":           "CLI turn execution",
		"session " + "list":           "CLI session enumeration",
		"opencode " + "models":        "CLI model enumeration",
		".opencode-models" + ".json":  "on-disk model cache",
		"64" + "667":                  "legacy port fallback",
		"40" + "97":                   "hard-coded sibling managed port",
		"--host 0.0.0.0":              "non-loopback bind",
		"OPENCODE_" + "BASE_URL":      "legacy env key (must read opencode_web_url)",
		"opts[\"opencode_" + "url\"]": "legacy opts key (must read opencode_web_url)",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no source files found — guard ran from the wrong directory")
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for frag, why := range forbidden {
			if strings.Contains(string(data), frag) {
				t.Errorf("%s contains forbidden fragment %q (%s)", name, frag, why)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test source files checked")
	}
}

// TestNoDirectImportOfLegacyPackage parses every package file (tests included)
// and asserts none imports the legacy hybrid package.
func TestNoDirectImportOfLegacyPackage(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse package dir: %v", err)
	}
	found := false
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if path == legacyImportPath {
					t.Errorf("%s imports the legacy hybrid package %s", name, legacyImportPath)
					found = true
				}
			}
		}
	}
	if !found && len(pkgs) == 0 {
		t.Fatal("no packages parsed")
	}
}

// TestTransitiveImportsExcludeLegacyPackage asserts the full dependency graph
// (go list -deps) contains the legacy hybrid package nowhere — a transitive
// leak through a helper would reintroduce the shared-state coupling the
// physical isolation is meant to prevent.
func TestTransitiveImportsExcludeLegacyPackage(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available in test environment")
	}
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list -deps failed: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == legacyImportPath {
			t.Fatalf("transitive dependency graph contains the legacy hybrid package %s", legacyImportPath)
		}
	}
}
