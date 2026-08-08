package readfile

import (
	"encoding/binary"
	"crypto/sha256"
	"fmt"
	"unicode/utf8"
)

// read_file_v2 encoding classification（plan §3.6.1 / R4 P1-1 decision table）。
// 在 MacBridge owning layer 对原始 bytes 判定，不能放在 iOS/JSC（invalid UTF-8 经 string(data)
// 已被替换为 U+FFFD，无法恢复）。这是 R1.1 的核心纯函数：bytes -> kind + detected + displayBytes。

// EncodingClassification 是 ClassifyEncoding 的结果。
type EncodingClassification struct {
	Kind        string // "text" | "unsupported_encoding" | "binary"
	Detected    string // BOM-detected: "utf-32le"|"utf-32be"|"utf-16le"|"utf-16be"；text/binary/unknown 为 ""
	DisplayBytes []byte // UTF-8 BOM 剥离后的 display bytes（text 用；其它 = data 原样）
}

// ClassifyEncoding 按 plan §3.6.1 决策表分类。
//
//	BOM sniff 最长前缀：UTF-32LE/BE(4) → UTF-16LE/BE(2) → UTF-8 BOM(3)。
//	UTF-32/16 BOM => unsupported_encoding(detected)，不跑 binary heuristic。
//	UTF-8 BOM => 剥 BOM 后判 valid+heuristic；无 BOM => 直接判。
//	valid+heuristic-false => text；valid+heuristic-true => binary；
//	invalid+heuristic-true => binary；invalid+heuristic-false => unsupported_encoding(detected="")。
//
// heuristic（在 payload byte count 上，UTF-8 BOM 从计数与分母都排除；空 payload 两 heuristic 均 false）：
//
//	NUL(0x00) 比例 >= 1%（nulCount*100 >= payloadByteCount）
//	或 除 TAB/LF/CR/FF 外 C0 control(<0x20) 比例 >= 10%（controlCount*100 >= payloadByteCount*10）
func ClassifyEncoding(data []byte) EncodingClassification {
	// 1) BOM sniff（最长前缀）
	if len(data) >= 4 && binary.BigEndian.Uint32(data[0:4]) == 0x0000FEFF {
		return EncodingClassification{Kind: "unsupported_encoding", Detected: "utf-32be"}
	}
	if len(data) >= 4 && binary.LittleEndian.Uint32(data[0:4]) == 0x0000FEFF {
		// UTF-32LE BOM = FF FE 00 00 == 0xFFFE0000 little-endian read; 用 LE 解码 == 0xFEFF 即 utf-32le
		return EncodingClassification{Kind: "unsupported_encoding", Detected: "utf-32le"}
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		return EncodingClassification{Kind: "unsupported_encoding", Detected: "utf-16le"}
	}
	if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		return EncodingClassification{Kind: "unsupported_encoding", Detected: "utf-16be"}
	}

	// payload：UTF-8 BOM 剥离后；否则原样
	payload := data
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		payload = data[3:]
	}
	display := payload

	valid := utf8.Valid(payload)
	heuristic := binaryHeuristic(payload)

	switch {
	case valid && !heuristic:
		return EncodingClassification{Kind: "text", DisplayBytes: display}
	case valid && heuristic:
		return EncodingClassification{Kind: "binary"}
	case !valid && heuristic:
		return EncodingClassification{Kind: "binary"}
	default: // !valid && !heuristic
		return EncodingClassification{Kind: "unsupported_encoding", Detected: ""} // detected omit
	}
}

// binaryHeuristic：NUL>=1% 或（除 TAB/LF/CR/FF 外）C0 control>=10%。空 payload => false。
// 用 uint64 避免 2MiB 下乘法溢出（实际不会溢，但显式安全）。
func binaryHeuristic(payload []byte) bool {
	n := uint64(len(payload))
	if n == 0 {
		return false
	}
	var nul, ctrl uint64
	for _, b := range payload {
		switch {
		case b == 0x00:
			nul++
		case b < 0x20 && b != 0x09 && b != 0x0A && b != 0x0D && b != 0x0C:
			ctrl++
		}
	}
	if nul*100 >= n { // >= 1%
		return true
	}
	if ctrl*100 >= n*10 { // >= 10%
		return true
	}
	return false
}

// ContentRevision 计算 raw bytes 的 sha256 wire 形式：sha256:<64 lowercase hex>。
func ContentRevision(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", h[:])
}
