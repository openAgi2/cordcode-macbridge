package gobridge

// Production import guard for deprecated/ (2026-09-29 dsh+opencode migration):
// no non-test source file outside deprecated/ itself may import any
// github.com/openAgi2/cordcode-macbridge/deprecated/ package. Test files are
// exempt — they keep the archived packages compiling and honest. Mirrors the
// parser-based guard pattern of agent/opencode-web/import_guard_test.go.
import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const deprecatedImportPrefix = "github.com/openAgi2/cordcode-macbridge/deprecated/"

func TestProductionCodeNeverImportsDeprecated(t *testing.T) {
	repoRoot := ".."
	checked := 0
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "build", "dist", "node_modules", "vendor":
				return filepath.SkipDir
			}
			// deprecated/ 自身（含其测试）允许 import 自己。
			if path == filepath.Join(repoRoot, "deprecated") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			// 解析失败的文件交给 go build 把关；guard 只管可解析文件的 import 面。
			return nil
		}
		checked++
		for _, imp := range file.Imports {
			if strings.HasPrefix(strings.Trim(imp.Path.Value, `"`), deprecatedImportPrefix) {
				t.Errorf("%s imports deprecated package %s (production code must not depend on deprecated/)", path, imp.Path.Value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	if checked == 0 {
		t.Fatal("no production .go files checked — guard ran from the wrong directory")
	}
}
