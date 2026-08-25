package codexweb

// provenance_test.go —— 结构红线测试（设计 §2.2/§13.1 provenance）。
//
// 断言本包不 import 旧 agent/codex、transcriptindex 或任何旧 rollout/file-relay/session
// scanner 路径；不与旧 backend 共享 session/history/cache type alias 或 wrapper。

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func forbiddenImportPrefixes() []string {
	return []string{
		"github.com/openAgi2/cordcode-macbridge/agent/codex",
		"github.com/openAgi2/cordcode-macbridge/transcriptindex",
	}
}

// forbiddenImportSubstrings 覆盖 §9.1 第 11 条的其余禁区措辞：旧 store、rollout
// parser、file relay、session scanner——任何 import 路径含这些词都属于越界。
func forbiddenImportSubstrings() []string {
	return []string{
		"/transcriptindex",
		"rolloutparser",
		"rollout-parser",
		"filerelay",
		"file-relay",
		"sessionscanner",
		"session-scanner",
		"transcriptscanner",
	}
}

func TestProvenanceNoForbiddenImports(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	for _, pkg := range pkgs {
		for fname, f := range pkg.Files {
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for _, forbidden := range forbiddenImportPrefixes() {
					if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
						t.Errorf("%s imports forbidden package %s（设计 §2.2 空目录原则）", fname, path)
					}
				}
				lower := strings.ToLower(path)
				for _, substr := range forbiddenImportSubstrings() {
					if strings.Contains(lower, substr) {
						t.Errorf("%s imports forbidden package %s（设计 §9.1 provenance 禁区）", fname, path)
					}
				}
			}
		}
	}
}

// TestProvenanceNoRolloutPathAccess 本包不得生成/查找 rollout 路径。唯一窄例外是
// context_usage.go 可以读取官方 API 返回的 Thread.path：app-server 对已加载 thread
// 没有 usage-read RPC。该文件仍不得包含任何路径发现字面量。
func TestProvenanceNoRolloutPathAccess(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	for _, pkg := range pkgs {
		for fname := range pkg.Files {
			if strings.HasSuffix(fname, "_test.go") {
				continue
			}
			b, err := os.ReadFile(fname)
			if err != nil {
				t.Fatalf("read %s: %v", fname, err)
			}
			src := string(b)
			for _, forbidden := range []string{"rollout-", "sessions/rollout", `threadStore`, `.jsonl"`} {
				if strings.Contains(src, forbidden) {
					t.Errorf("%s 引用旧 rollout/store 路径词汇 %q（§9.1 禁区）", fname, forbidden)
				}
			}
		}
	}
}

// TestProvenanceFilesAreNewlyAuthored 抽查：本包文件头部均含建立纪律注释锚
// （后续 review 以 git 首提交"纯新增"为准）。
func TestProvenanceFilesAreNewlyAuthored(t *testing.T) {
	required := map[string]string{
		"codexweb.go":     "从空目录建立",
		"lifecycle.go":    "官方服务生命周期",
		"transport.go":    "WebSocket over Unix socket",
		"rpc.go":          "app-server-client",
		"interactions.go": "availableDecisions",
	}
	for fname, marker := range required {
		b, err := os.ReadFile(filepath.Join(fname))
		if err != nil {
			t.Fatalf("read %s: %v", fname, err)
		}
		if !strings.Contains(string(b), marker) {
			t.Errorf("%s 缺少职责锚注释：%s", fname, marker)
		}
	}
}
