package readfile

import (
	"strings"
)

// read_file_v2 result assembly（plan §3.6.1 / R1.1）。
// 把 ClassifyEncoding 的分类结果 + raw bytes 组装成 ReadFileV2Result（text/unsupported/binary），
// 含 metadata（path/extension/sizeBytes/contentRevision/identity）、text 的 segments 与 totalLines
// 行语义、truncation head+omission+tail。复用 codec.go 的 wire 类型。

// 行数上限/尾段（与 handlers.go readFileMaxLines/readFileTailLines 对齐；参数化便于测试）。
const (
	DefaultMaxLines   = 5000
	DefaultTailLines  = 200
)

// BuildReadFileV2Result 构造 read_file_v2 的 tagged union 结果。
//   data: 原始 bytes（sizeBytes + contentRevision 覆盖含 BOM 的 raw bytes）；
//   display 由 ClassifyEncoding 剥 UTF-8 BOM。
//   maxLines/tailLines: 截断阈值；totalLines > maxLines => head(maxLines-tailLines)+omission+tail(tailLines)。
func BuildReadFileV2Result(data []byte, path, ext string, identity OwningIdentity, maxLines, tailLines int) ReadFileV2Result {
	class := ClassifyEncoding(data)
	meta := FileMetadata{
		Path:            path,
		Extension:       ext,
		SizeBytes:       uint64(len(data)),
		ContentRevision: ContentRevision(data),
		OwningIdentity:  identity,
	}
	switch class.Kind {
	case "text":
		display := class.DisplayBytes
		totalLines := countLogicalLines(display)
		segs := buildTextSegments(display, totalLines, maxLines, tailLines)
		return ReadFileV2Result{
			Kind: "text", Metadata: meta, Encoding: "utf-8",
			TotalLines: totalLines, Segments: segs,
		}
	case "unsupported_encoding":
		return ReadFileV2Result{Kind: "unsupported_encoding", Metadata: meta, Detected: class.Detected}
	default: // binary
		return ReadFileV2Result{Kind: "binary", Metadata: meta}
	}
}

// countLogicalLines: 空 => 0；否则 = LF 数 +（末字节不是 \n ? 1 : 0）。
// CRLF 的 \r\n 只计一个 \n（\r 是行内字符）；尾换行不产生额外空行（plan §3.6.1 行语义）。
func countLogicalLines(data []byte) uint64 {
	if len(data) == 0 {
		return 0
	}
	var lf uint64
	for _, b := range data {
		if b == '\n' {
			lf++
		}
	}
	if data[len(data)-1] != '\n' {
		return lf + 1
	}
	return lf
}

// buildTextSegments: 空 => 唯一空 full；非空 <= maxLines => 一个 full；> maxLines => head+omission+tail。
// 不变量：headCount + omittedCount + tailCount == totalLines。
func buildTextSegments(display []byte, totalLines uint64, maxLines, tailLines int) []Segment {
	if totalLines == 0 {
		// 空文件：唯一空 full segment（禁止 segments:[]）
		return []Segment{{Kind: "full", Content: "", SourceLineStart: 1, SourceLineCount: 0}}
	}
	if int(totalLines) <= maxLines {
		return []Segment{{Kind: "full", Content: string(display), SourceLineStart: 1, SourceLineCount: totalLines}}
	}
	// truncated: head(maxLines-tailLines) + omission + tail(tailLines)
	lines := strings.Split(string(display), "\n")
	// lines 长度应 == totalLines（countLogicalLines 的语义）；防御性 clamp
	tl := int(totalLines)
	if len(lines) < tl {
		tl = len(lines)
	}
	headCount := maxLines - tailLines
	if headCount < 0 {
		headCount = 0
	}
	omitted := tl - headCount - tailLines
	if omitted < 0 {
		omitted = 0
	}
	head := strings.Join(lines[:headCount], "\n")
	tailStart := tl - tailLines
	if tailStart < headCount {
		tailStart = headCount
	}
	tail := strings.Join(lines[tailStart:tl], "\n")
	return []Segment{
		{Kind: "head", Content: head, SourceLineStart: 1, SourceLineCount: uint64(headCount)},
		{Kind: "omission", Content: "", SourceLineStart: uint64(headCount + 1), SourceLineCount: uint64(omitted)},
		{Kind: "tail", Content: tail, SourceLineStart: uint64(tailStart + 1), SourceLineCount: uint64(tl - tailStart)},
	}
}
