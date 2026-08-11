package readfile

// JSON exact-length preflight (plan §3.6.3 / A-1.6 / R10 P1-4).
//
// 目标：在不构造输出的前提下，逐字节计算 Go encoding/json.Marshal 默认 escaping
// （SetEscapeHTML=true：`<>&` → \u00XX、U+2028/2029 →  /9、control + `"` `\` 转义）
// 下某个字符串值的 JSON 编码长度。schema-specific preflight（read_file_v2 text 等）用它组合
// 出整个 payload 的精确长度，超过 maxReadFileSerializedBytes 即在 marshal 前失败，不构造 >cap buffer。
//
// proof 策略：JSONStringLen 与 json.Marshal(string) 逐字节比对（property test），覆盖
// ASCII / HTML-sensitive / U+2028-9 / control / 引号反斜杠 / 多字节 UTF-8 / 空串 / 逐字节全空间。

import "unicode/utf8"

// JSONStringLen 返回 json.Marshal(s) 对字符串 s 产生的字节数（含两侧引号），
// 严格匹配 Go encoding/json 默认 escaping（SetEscapeHTML=true）。
// 输入应为合法 UTF-8（codec 的内容域；非法 UTF-8 由上层 encoding 检测拒绝）。
// 非法 UTF-8 字节按 Go 行为计为 `�`（6 字节）——property test 覆盖此情形。
func JSONStringLen(s string) int {
	n := 2 // 两侧引号
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			// ASCII fast path
			switch b {
			case '"', '\\':
				n += 2 // \"  \\
			case '<', '>', '&':
				n += 6 // < > &（HTML escape，默认 on）
			case '\n', '\t', '\r', '\b', '\f':
				n += 2 // 短转义 \n \t \r \b \f
			default:
				if b < 0x20 {
					n += 6 // \u00XX
				} else {
					n++ // 原样
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// 非法 UTF-8 字节：Go appendString 写 `�`（6 字节）
			n += 6
			i++
			continue
		}
		if r == 0x2028 || r == 0x2029 { // U+2028 / U+2029
			n += 6 //   /  （HTML escape 默认 on）
		} else {
			n += size // 合法 rune 原样 UTF-8
		}
		i += size
	}
	return n
}

// maxReadFileSerializedBytes 复用自 codec.go（A0.5 named cap）。
// PreflightReadFileText 给出 read_file_v2 text 结果在当前 schema 下的精确序列化长度的核心
// 可变部分（content 串 + segments content 串），供上层组合固定 key/结构开销后做 cap 判定。
// 这里只暴露字符串 preflight 能力的证明入口；完整 schema 组合 preflight 在 R1 handler 接入时落。
