package gobridge

// catalog_redact_test.go 锁定 §444 脱敏：catalog 日志的 directory 字段绝不泄漏绝对路径。

import "testing"

func TestRedactDirForLog(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"absolute_home_project", "/Users/jacklee/Projects/cordcode-ios", "cordcode-ios"},
		{"absolute_tmp", "/tmp/codex-ws", "codex-ws"},
		{"trailing_slash", "/home/owner/work/x/", "x"},
		{"relative", "relative/dir", "dir"},
		{"empty", "", ""},
		{"root_only", "/", "/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redactDirForLog(c.in)
			if got != c.want {
				t.Fatalf("redactDirForLog(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestRedactDirForLogNeverAbsolute：脱敏结果绝不以 "/" 开头（除非输入是裸根 "/"），保证 §444
// 绝对路径不泄漏。basename 形式天然满足，但显式断言锁定契约。
func TestRedactDirForLogNeverAbsolute(t *testing.T) {
	for _, in := range []string{
		"/Users/jacklee/Projects/cordcode-ios",
		"/tmp/x",
		"/home/owner/secret/project",
		"/var/root/.config/private",
	} {
		got := redactDirForLog(in)
		if len(got) > 1 && got[0] == '/' {
			t.Fatalf("redactDirForLog(%q) = %q 泄漏绝对路径前缀（§444）", in, got)
		}
	}
}
