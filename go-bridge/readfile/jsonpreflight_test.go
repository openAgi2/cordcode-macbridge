package readfile

import (
	"encoding/json"
	"testing"
	"testing/quick"
	"unicode/utf8"
)

// 真值：json.Marshal(s) 默认 escaping 下产生的字节数（含引号）。
func actualJSONStringLen(s string) int {
	b, _ := json.Marshal(s)
	return len(b)
}

func TestJSONStringLen_Corpus(t *testing.T) {
	cases := []string{
		"",
		"plain ascii",
		"with <html> chars & more",
		"quotes \" and backslash \\",
		"line\nbreak\ttab\rcr",
		"control\x01\x02\x1f",
		"unicode emoji \U0001F600 \U0001F680",
		"chinese 中文 日本語",
		"line sep para sep end",       // U+2028 / U+2029（显式）
		"mixed <\"a\\b\"> &     \U0001F600",
		"\xff invalid byte",                      // 非法 UTF-8（0xff）
		"lone continuation \x80",                 // 非法 UTF-8（0x80）
		string([]byte{0x41, 0xe2, 0x28, 0xa1, 0x42}), // 非法 UTF-8 序列（中间断）
	}
	for _, s := range cases {
		got := JSONStringLen(s)
		want := actualJSONStringLen(s)
		if got != want {
			t.Errorf("JSONStringLen(%q)=%d, json.Marshal=%d (diff %d)", s, got, want, got-want)
		}
	}
}

// 全 ASCII 字节空间：单字节 0x00..0x7f 各自的 escape 长度必须匹配。
func TestJSONStringLen_AllASCIIBytes(t *testing.T) {
	for b := 0; b < 0x80; b++ {
		s := string([]byte{byte(b)})
		if JSONStringLen(s) != actualJSONStringLen(s) {
			t.Errorf("ASCII byte 0x%02x: JSONStringLen=%d want=%d", b, JSONStringLen(s), actualJSONStringLen(s))
		}
	}
}

// property：随机合法 UTF-8 串（含特殊 rune）逐字节匹配 json.Marshal。
func TestJSONStringLen_RandomValidUTF8(t *testing.T) {
	runes := []rune("abcd \"\\ <>& \n\t\r `<>&` 中文 \U0001F600    \x01\x1f 0123")
	gen := func() string {
		n := int(genByte()) % 33
		rs := make([]rune, n)
		for i := range rs {
			rs[i] = runes[int(genByte())%len(runes)]
		}
		return string(rs)
	}
	for i := 0; i < 5000; i++ {
		s := gen()
		if !utf8.ValidString(s) {
			t.Fatalf("generated invalid utf8: %q", s) // gen 只用合法 rune，不该发生
		}
		if JSONStringLen(s) != actualJSONStringLen(s) {
			t.Fatalf("mismatch on %q: %d vs %d", s, JSONStringLen(s), actualJSONStringLen(s))
		}
	}
}

// property：随机字节（含非法 UTF-8）也必须匹配（Go 对非法字节输出 �，6 字节）。
func TestJSONStringLen_RandomBytesArbitrary(t *testing.T) {
	f := func(b [16]byte) bool {
		s := string(b[:])
		return JSONStringLen(s) == actualJSONStringLen(s)
	}
	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}

// genByte 返回确定性伪随机字节（不依赖全局 rand，便于复现；A-1 proof 不需要密码学随机）。
var genByteState byte = 0x1

func genByte() byte {
	genByteState = genByteState*31 + 17
	return genByteState
}
