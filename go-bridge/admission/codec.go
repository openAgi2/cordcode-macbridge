// Package admission 实现 CordCode Management API（/internal/runtime/quiesce,
// commit-quiesced-shutdown, abort-quiesce）的 strict codec、token 安全原语、有界 lease
// admission 状态机与时间预算校验。
//
// 这是 plan A-1 的 proof artifact：strict codec + 32-char hex ID/token + constant-time
// token 比较 + token 脱敏 + commit/abort 全状态表 + lease/HTTP/scheduling 时间不等式。
// ManagementServer 与 Bridge-owned turn admission 已在 R1.11 接入这些原语；本包仍保持
// 独立，以便状态机、strict codec 与资源预算可在不启动 runtime 的情况下穷举验证。
//
// 规范来源：docs(本仓)/2026-08-08-syntax-highlighting-shiki-jsc-plan.md §3.6.3 与
// R11 终止性复核 P1-1/P1-2/P1-3/P2-1。
package admission

import (
	"bytes"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ManagementSchemaVersion 是 v1 Management schema 的唯一版本号。
const ManagementSchemaVersion = 1

// operationOrTokenRegex 同时匹配 operationId 与 token：恰好 32 个小写 ASCII hex 字符。
// 16 random bytes 编码为 exactly 32 lowercase ASCII hex characters。
var operationOrTokenRegex = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Token 是 16 raw bytes 的短期授权 secret。DecodeToken 把 32 lowercase hex 解码成 16 bytes；
// 任何错误长度、大写、非 ASCII、非 hex 在进入业务锁前被拒绝。
type Token [16]byte

// OperationID 是 16 raw bytes 的 opaque correlation。可作为 hash-map key，不要求 constant-time。
type OperationID [16]byte

// DecodeOperationID 把 32 lowercase hex 解码为 16-byte operationId。
func DecodeOperationID(s string) (OperationID, error) {
	if !operationOrTokenRegex.MatchString(s) {
		return OperationID{}, ErrInvalidOperationID
	}
	var o OperationID
	if _, err := hex.Decode(o[:], []byte(s)); err != nil {
		return OperationID{}, ErrInvalidOperationID
	}
	return o, nil
}

// DecodeToken 把 32 lowercase hex 解码为 16-byte token。
func DecodeToken(s string) (Token, error) {
	if !operationOrTokenRegex.MatchString(s) {
		return Token{}, ErrInvalidToken
	}
	var t Token
	if _, err := hex.Decode(t[:], []byte(s)); err != nil {
		return Token{}, ErrInvalidToken
	}
	return t, nil
}

// EncodeHex 输出 32 lowercase hex（仅用于 fixture/调试；真实 token 永不进入日志）。
func (o OperationID) EncodeHex() string { return hex.EncodeToString(o[:]) }

// EncodeHex 只用于 authenticated Management response；调用方仍不得记录返回值。
func (t Token) EncodeHex() string { return hex.EncodeToString(t[:]) }

// ConstantTimeCompareToken 用 constant-time 字节比较两个 token。token 是短期授权 secret，
// 必须用此函数比较；operationId correlation 不应改用 constant-time（见 plan R10 P2-2）。
func ConstantTimeCompareToken(a, b Token) bool {
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// Equal 是 OperationID 的固定 16-byte 相等/hash-map 用比较（非 secret，不需 constant-time）。
func (o OperationID) Equal(b OperationID) bool { return o == b }

// ── strict integer token 解析 ──────────────────────────────────────────────────
// 所有 Management 数字字段必须是 base-10 JSON integer token：禁止负数、小数、指数、quoted
// number、null、bool、object/array、overflow。先检视 raw token 首字节与字符集，再 ParseUint。

// ParseStrictUInt 解析一个 json.RawMessage 为无符号整数，bits 限定宽度。
// 拒绝：空、quoted、null、bool、负号、小数点、指数符号、下划线、前导非数字、溢出。
func ParseStrictUInt(raw json.RawMessage, bits int) (uint64, error) {
	s := strings.TrimSpace(string(raw))
	if len(s) == 0 {
		return 0, ErrMissingNumber
	}
	// 任何非纯 base-10 非负整数的首字节直接拒绝。
	switch c := s[0]; {
	case c == '"':
		return 0, ErrQuotedNumber
	case c == 'n':
		return 0, ErrNullNumber
	case c == 't' || c == 'f':
		return 0, ErrBoolNumber
	case c == '-', c == '+', c == '.':
		return 0, ErrInvalidNumberToken
	case c >= '0' && c <= '9':
		// fall through 到逐字符校验
	default:
		return 0, ErrInvalidNumberToken
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, ErrInvalidNumberToken // 捕获 1.5 / 1e3 / 1_000 / 负号出现在后续位置
		}
	}
	v, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, ErrNumberOverflow
	}
	return v, nil
}

// ParseStrictInt32 解析 [1, math.MaxInt32] 范围的 pid。
func ParseStrictInt32Positive(raw json.RawMessage) (int32, error) {
	v, err := ParseStrictUInt(raw, 31) // 31 bits => max 2^31-1 = math.MaxInt32
	if err != nil {
		return 0, err
	}
	if v < 1 {
		return 0, ErrInvalidPID
	}
	return int32(v), nil
}

// ── strict object 解码（exact key set / no duplicate / no null） ───────────────
// 不能依赖 Go 普通 Unmarshal 忽略 unknown 字段；这里显式校验 exact key set、duplicate key、null。

// assertNoDuplicateKeys 用 token 流扫描原始 JSON object，发现重复 key 即报错。
// Go encoding/json 默认对重复 key “last wins” 静默接受，必须自行扫描。
func assertNoDuplicateKeys(raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return ErrInvalidJSON
	}
	delim, ok := t.(json.Delim)
	if !ok || delim != '{' {
		return ErrInvalidJSON
	}
	seen := make(map[string]bool)
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return ErrInvalidJSON
		}
		key, ok := kt.(string)
		if !ok {
			return ErrInvalidJSON
		}
		if seen[key] {
			return fmt.Errorf("%w: %s", ErrDuplicateKey, key)
		}
		seen[key] = true
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return ErrInvalidJSON
		}
	}
	return nil
}

// DecodeStrictObject 把 raw 解码为 per-key RawMessage map，并校验：
//   - 是 JSON object；
//   - key set 恰好等于 allowed（无 missing、无 extra/unknown）；
//   - 无 duplicate key；
//   - 无 null 值。
func DecodeStrictObject(raw json.RawMessage, allowed []string) (map[string]json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, ErrInvalidJSON
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, ErrInvalidJSON
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allowedSet[k] = true
	}
	for k := range m {
		if !allowedSet[k] {
			return nil, fmt.Errorf("%w: %s", ErrUnknownField, k)
		}
	}
	for _, k := range allowed {
		if _, ok := m[k]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrMissingField, k)
		}
	}
	for k, v := range m {
		if strings.TrimSpace(string(v)) == "null" {
			return nil, fmt.Errorf("%w: %s", ErrNullField, k)
		}
	}
	if err := assertNoDuplicateKeys(raw); err != nil {
		return nil, err
	}
	return m, nil
}

// DecodeStrictString 解码一个 string 字段（拒绝非 string、null、空-only）。
func DecodeStrictString(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 {
		return "", ErrMissingField
	}
	if trimmed == "null" {
		return "", ErrNullField
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", ErrInvalidString
	}
	return s, nil
}

// ── token 脱敏 ─────────────────────────────────────────────────────────────────
// token 绝不进入 OSLog / Go log / stdout/stderr / metrics / support info / crash annotation /
// status。RedactLogValue 把任何 32-hex 串替换为 [redacted]，供日志/错误渲染管线兜底。

var redactHexRegex = regexp.MustCompile(`[0-9a-fA-F]{32}`)

// RedactLogValue 把出现在字符串中的 32-hex token（以及等长大写/混合 hex）替换为 [redacted]。
// 用于把任意 log/error 文本送出前做兜底脱敏；不是“允许记录 token 然后脱敏”的许可证。
func RedactLogValue(s string) string {
	return redactHexRegex.ReplaceAllString(s, "[redacted]")
}

// ── sentinel errors ────────────────────────────────────────────────────────────

var (
	ErrInvalidOperationID = errors.New("invalid operationId: must be exactly 32 lowercase ASCII hex")
	ErrInvalidToken       = errors.New("invalid token: must be exactly 32 lowercase ASCII hex")
	ErrMissingNumber      = errors.New("missing numeric field")
	ErrQuotedNumber       = errors.New("number field must not be quoted")
	ErrNullNumber         = errors.New("number field must not be null")
	ErrBoolNumber         = errors.New("number field must not be a bool")
	ErrInvalidNumberToken = errors.New("number field must be a base-10 non-negative integer token")
	ErrNumberOverflow     = errors.New("number field overflow")
	ErrInvalidPID         = errors.New("pid must be in 1..Int32.max")
	ErrInvalidJSON        = errors.New("invalid JSON object")
	ErrUnknownField       = errors.New("unknown field")
	ErrMissingField       = errors.New("missing required field")
	ErrNullField          = errors.New("field must not be null")
	ErrDuplicateKey       = errors.New("duplicate key")
	ErrInvalidString      = errors.New("invalid string field")
)
