package admission

import (
	"encoding/json"
	"strings"
	"testing"
)

// numeric integer-token: base-10 非负整数；拒绝负数/小数/指数/quoted/null/bool/overflow。
func TestParseStrictUInt_AcceptsValid(t *testing.T) {
	cases := []string{"0", "1", "42", "4294967295", "18446744073709551615"}
	for _, c := range cases {
		raw := json.RawMessage(c)
		_, err := ParseStrictUInt(raw, 64)
		if err != nil {
			t.Errorf("ParseStrictUInt(%s) unexpected err: %v", c, err)
		}
	}
}

func TestParseStrictUInt_RejectsInvalid(t *testing.T) {
	// 每个 token 都必须被拒绝。
	cases := map[string]bool{ // value: true=must-reject
		`"-1"`:  true, // quoted
		`"123"`: true, // quoted number
		`null`:  true,
		`true`:  true,
		`false`: true,
		`-1`:    true, // negative
		`1.5`:   true, // float
		`1e3`:   true, // exponent
		`1_000`: true, // underscore
		`+5`:    true,
		`{}`:    true,
		`[]`:    true,
		` `:     true, // empty after trim
		`0x10`:  true,
	}
	for tok := range cases {
		raw := json.RawMessage(tok)
		if _, err := ParseStrictUInt(raw, 64); err == nil {
			t.Errorf("ParseStrictUInt(%s) expected rejection, got nil", tok)
		}
	}
}

func TestParseStrictUInt_RejectsOverflow(t *testing.T) {
	// 32-bit domain: 4294967296 (2^32) overflows
	if _, err := ParseStrictUInt(json.RawMessage("4294967296"), 32); err == nil {
		t.Error("expected overflow for 2^32 in 32-bit domain")
	}
	// boundary: 4294967295 ok in 32-bit
	if _, err := ParseStrictUInt(json.RawMessage("4294967295"), 32); err != nil {
		t.Errorf("4294967295 in 32-bit should be ok: %v", err)
	}
}

func TestParseStrictInt32Positive(t *testing.T) {
	if _, err := ParseStrictInt32Positive(json.RawMessage("0")); err == nil {
		t.Error("pid=0 must be rejected (must be >=1)")
	}
	if v, err := ParseStrictInt32Positive(json.RawMessage("1")); err != nil || v != 1 {
		t.Errorf("pid=1 want 1, got %v %v", v, err)
	}
	if _, err := ParseStrictInt32Positive(json.RawMessage("2147483647")); err != nil {
		t.Errorf("pid=Int32.max should be ok: %v", err)
	}
	if _, err := ParseStrictInt32Positive(json.RawMessage("2147483648")); err == nil {
		t.Error("pid=Int32.max+1 must overflow")
	}
}

// 32-char lowercase hex: operationId / token。
func TestOperationIDToken_HexValidation(t *testing.T) {
	good := "ffeeddccbbaa99887766554433221100"
	if _, err := DecodeOperationID(good); err != nil {
		t.Errorf("good operationId rejected: %v", err)
	}
	if _, err := DecodeToken(good); err != nil {
		t.Errorf("good token rejected: %v", err)
	}
	bad := []string{
		"FFEEDDCCBBAA99887766554433221100",  // uppercase
		"ffeeddccbbaa9988776655443322110",   // 31 chars
		"ffeeddccbbaa998877665544332211000", // 33 chars
		"ggeeddccbbaa99887766554433221100",  // non-hex
		"ffeeddccbbaa9988776655443322110z",  // non-hex
		"",                                  // empty
		"ff ee dd cc bb aa 99 88 77 66 55 44 33 22 11 00",
	}
	for _, b := range bad {
		if _, err := DecodeOperationID(b); err == nil {
			t.Errorf("bad operationId accepted: %q", b)
		}
		if _, err := DecodeToken(b); err == nil {
			t.Errorf("bad token accepted: %q", b)
		}
	}
}

// constant-time token 比较：相等 true，不等 false。
func TestConstantTimeCompareToken(t *testing.T) {
	a, _ := DecodeToken("ffeeddccbbaa99887766554433221100")
	b, _ := DecodeToken("ffeeddccbbaa99887766554433221100")
	c, _ := DecodeToken("00112233445566778899aabbccddeeff")
	if !ConstantTimeCompareToken(a, b) {
		t.Error("equal tokens should compare true")
	}
	if ConstantTimeCompareToken(a, c) {
		t.Error("different tokens should compare false")
	}
}

// strict-object: exact keys / no duplicate / no null / no extra / no missing。
func TestDecodeStrictObject_RejectsCorpus(t *testing.T) {
	allowed := []string{"a", "b"}
	cases := map[string]string{
		`{"a":1,"b":2}`:       "", // ok
		`{"a":1}`:             "missing b",
		`{"a":1,"b":2,"c":3}`: "unknown c",
		`{"a":1,"b":2,"a":3}`: "duplicate a",
		`{"a":1,"b":null}`:    "null b",
		`[1,2]`:               "not object",
		`""`:                  "not object",
	}
	for body, wantErr := range cases {
		_, err := DecodeStrictObject(json.RawMessage(body), allowed)
		if wantErr == "" && err != nil {
			t.Errorf("body %s: unexpected err %v", body, err)
		}
		if wantErr != "" && err == nil {
			t.Errorf("body %s: expected error (%s), got nil", body, wantErr)
		}
	}
}

// token 脱敏：RedactLogValue 必须把 32-hex token 从任意字符串中抹掉。
func TestRedactLogValue(t *testing.T) {
	tok := "ffeeddccbbaa99887766554433221100"
	in := "operation ok token=" + tok + " pid=12345"
	out := RedactLogValue(in)
	if strings.Contains(out, tok) {
		t.Errorf("redaction failed: token still present in %q", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Errorf("expected [redacted] placeholder, got %q", out)
	}
	// 非 token 文本保留
	if !strings.Contains(out, "pid=12345") {
		t.Errorf("redaction over-reached: pid lost in %q", out)
	}
}
