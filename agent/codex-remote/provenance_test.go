package codexremote

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvenanceNoForbiddenImports(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	forbiddenExact := []string{
		"github.com/openAgi2/cordcode-macbridge/agent/codex",
		"github.com/openAgi2/cordcode-macbridge/agent/codex-web",
		"github.com/openAgi2/cordcode-macbridge/transcriptindex",
	}
	forbiddenSubstr := []string{
		"/transcriptindex",
		"rolloutparser",
		"filerelay",
		"file-relay",
		"sessionscanner",
	}
	for _, pkg := range pkgs {
		for fname, f := range pkg.Files {
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for _, forbidden := range forbiddenExact {
					if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
						t.Errorf("%s imports forbidden package %s", fname, path)
					}
				}
				lower := strings.ToLower(path)
				for _, substr := range forbiddenSubstr {
					if strings.Contains(lower, substr) {
						t.Errorf("%s imports forbidden package %s", fname, path)
					}
				}
			}
		}
	}
}

func TestGoBridgeBlankImportsCodexRemote(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "go-bridge", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := `_ "github.com/openAgi2/cordcode-macbridge/agent/codex-remote"`
	if !strings.Contains(string(src), want) {
		t.Fatalf("go-bridge/main.go must blank-import %s", want)
	}
}
