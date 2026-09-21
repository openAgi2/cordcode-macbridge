package dshweb

import (
	"encoding/json"
	"strings"
)

// dshCleanToolDisplay maps the dsh-web journal's structured tool payloads
// (tool/result data.meta + tool/call arguments) into the projection's
// structured display vocabulary — parity plan §5 S3 clean-detail slice.
// The cold path previously dropped data.meta entirely, so iOS could only
// render the XML-shaped raw output for read/write/edit.
//
// Branch rules by meta shape (evidence corpus: 63 empty-diffs records, all
// write-creates; 279 single-hunk; 17 multi-hunk):
//
//	① read meta {path, offset, lines, totalLines} → fileDisplay
//	   (text = LF-joined line texts; line numbers validated at construction:
//	   integral, >=1, >=offset, consecutive (+1), <=totalLines, first==offset,
//	   per-line text carries no embedded LF — any violation → detailUnavailable,
//	   nine-round R55 fail-closed).
//	② write-create (diffs == []) → arguments {file_path, content} →
//	   fileDisplay {lineStart: 1, lineEnd: totalLines: lineCount} with the
//	   R33 formula (empty content → the 1/0/0 empty-file shape; CRLF/CR are
//	   not line separators — LF only, text preserved verbatim); arguments
//	   parse failure → detailUnavailable.
//	③ edit / write-update (diffs non-empty) → editRegions [{path, newText}]
//	   in file order, every hunk (never [0]); a malformed entry → the whole
//	   step degrades to detailUnavailable (R62 spirit).
//	④ error results never reach here (caller gates on !isError): the raw
//	   error text stays the output, honestly displayed.
//	⑥ unknown success shape (no usable meta) → detailUnavailable {path?}
//	   (path from the call arguments when obtainable) — iOS renders the
//	   diagnostic copy instead of falling back to the XML raw output.
//
// Statistics are NOT derived in this slice (plan §3.3 conclusion 2:
// oldText/newText counting is undefined for same-line replacements).
func dshCleanToolDisplay(toolName string, arguments json.RawMessage, meta json.RawMessage) (fileDisplay map[string]any, editRegions []map[string]any, detailUnavailable map[string]any) {
	args := dshUnwrapArguments(arguments)
	argPath := ""
	if v, ok := args["file_path"].(string); ok && strings.TrimSpace(v) != "" {
		argPath = v
	}

	if len(meta) > 0 {
		var probe map[string]json.RawMessage
		if json.Unmarshal(meta, &probe) == nil {
			if _, hasLines := probe["lines"]; hasLines && toolName == "read" {
				return dshReadFileDisplay(meta, argPath)
			}
			if _, hasDiffs := probe["diffs"]; hasDiffs {
				return dshDiffsPayload(meta, toolName, args, argPath)
			}
		}
	}
	// ⑥ unknown success shape: no usable meta for a file tool — fail closed
	// with the diagnostic object (never render the XML raw output).
	diag := map[string]any{}
	if argPath != "" {
		diag["path"] = argPath
	}
	return nil, nil, diag
}

// dshReadMeta is the read tool's presentationMeta (official read.ts:
// {path, offset, lines: [{number, text}], totalLines, lang?}).
type dshReadMeta struct {
	Path       string        `json:"path"`
	Offset     int           `json:"offset"`
	Lines      []dshReadLine `json:"lines"`
	TotalLines int           `json:"totalLines"`
}

type dshReadLine struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

// dshReadFileDisplay builds branch ①. Line-number invariants are validated
// here so malformed journals never fabricate a plausible-looking window.
func dshReadFileDisplay(meta json.RawMessage, argPath string) (map[string]any, []map[string]any, map[string]any) {
	var m dshReadMeta
	if err := json.Unmarshal(meta, &m); err != nil {
		return nil, nil, dshDiagnostic(argPath)
	}
	path := strings.TrimSpace(m.Path)
	if path == "" {
		path = argPath
	}
	if path == "" || m.Offset < 1 || m.TotalLines < 0 {
		return nil, nil, dshDiagnostic(path)
	}
	prev := 0
	for i, line := range m.Lines {
		if line.Number < 1 || line.Number < m.Offset || line.Number > m.TotalLines {
			return nil, nil, dshDiagnostic(path)
		}
		if i == 0 {
			if line.Number != m.Offset {
				return nil, nil, dshDiagnostic(path)
			}
		} else if line.Number != prev+1 {
			// 连续递增（next == prev+1）：跳行（[1,3,5]）与乱序都在此拦截。
			return nil, nil, dshDiagnostic(path)
		}
		if strings.Contains(line.Text, "\n") {
			// 行文本禁内嵌 LF：join 后会破坏行数与区间的对应。
			return nil, nil, dshDiagnostic(path)
		}
		prev = line.Number
	}
	texts := make([]string, len(m.Lines))
	for i, line := range m.Lines {
		texts[i] = line.Text
	}
	lineStart := m.Offset
	lineEnd := 0
	if len(m.Lines) > 0 {
		lineEnd = m.Lines[len(m.Lines)-1].Number
	} else if m.Offset > 1 {
		lineEnd = m.Offset - 1
	}
	return map[string]any{
		"path":       path,
		"text":       strings.Join(texts, "\n"),
		"lineStart":  lineStart,
		"lineEnd":    lineEnd,
		"totalLines": m.TotalLines,
		// S3 read 写推导值（§4.3 ⑦）：窗口未覆盖到文件末尾即截断。
		"truncated": lineEnd < m.TotalLines,
	}, nil, nil
}

// dshDiffMeta is the write/edit tool's presentationMeta (official write.ts /
// edit.ts: {diffs: [{path, oldText, newText}]} — oldText null for pure
// additions; a create carries diffs: []).
type dshDiffMeta struct {
	Diffs []dshFileDiff `json:"diffs"`
}

type dshFileDiff struct {
	Path    string  `json:"path"`
	OldText *string `json:"oldText"`
	// NewText is a pointer so a missing key (malformed) stays distinct from
	// a legal empty string (deletion-only hunk).
	NewText *string `json:"newText"`
}

// dshDiffsPayload routes branches ② (empty diffs → write-create) and ③
// (non-empty → editRegions).
func dshDiffsPayload(meta json.RawMessage, toolName string, args map[string]any, argPath string) (map[string]any, []map[string]any, map[string]any) {
	var m dshDiffMeta
	if err := json.Unmarshal(meta, &m); err != nil {
		return nil, nil, dshDiagnostic(argPath)
	}
	if len(m.Diffs) == 0 {
		// ② write-create：从 call arguments 构造整文件展示（不得回退 XML）。
		if toolName != "write" {
			// edit 产生空 diffs 属未知成功形状（官方 edit 恒输出 hunk）。
			return nil, nil, dshDiagnostic(argPath)
		}
		return dshWriteCreateFileDisplay(args, argPath)
	}
	// ③ edit / write-update：按序全量 editRegions（不取 [0]）。
	regions := make([]map[string]any, 0, len(m.Diffs))
	for _, diff := range m.Diffs {
		path := strings.TrimSpace(diff.Path)
		if path == "" || diff.NewText == nil {
			return nil, nil, dshDiagnostic(path)
		}
		regions = append(regions, map[string]any{"path": path, "newText": *diff.NewText})
	}
	return nil, regions, nil
}

// dshWriteCreateFileDisplay builds branch ② from the tool-call arguments
// ({file_path, content} — the journal's ground truth for a create, since
// presentationMeta carries diffs: [] and nothing else).
func dshWriteCreateFileDisplay(args map[string]any, argPath string) (map[string]any, []map[string]any, map[string]any) {
	path := argPath
	content, ok := args["content"].(string)
	if !ok {
		// content 缺失/非字符串：无法构造整文件展示，fail closed。
		return nil, nil, dshDiagnostic(path)
	}
	if path == "" {
		if v, ok := args["file_path"].(string); ok {
			path = strings.TrimSpace(v)
		}
	}
	if path == "" {
		return nil, nil, dshDiagnostic("")
	}
	lineCount := dshWriteCreateLineCount(content)
	return map[string]any{
		"path":       path,
		"text":       content,
		"lineStart":  1,
		"lineEnd":    lineCount,
		"totalLines": lineCount,
		// write-create 恒 false（§4.3 ⑦）：整文件即全部内容。
		"truncated": false,
	}, nil, nil
}

// dshWriteCreateLineCount is the R33 write-create line formula: an empty
// content is 0 lines; otherwise LF count plus 1 unless the content already
// ends with LF (a trailing LF does not open an extra line; consecutive empty
// lines still count — "a\n\n" is 2). CRLF/CR are NOT line separators (LF
// only, mirroring the official read renderer); text is preserved verbatim.
func dshWriteCreateLineCount(content string) int {
	if content == "" {
		return 0
	}
	lines := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lines++
	}
	return lines
}

// dshDiagnostic builds the fail-closed detailUnavailable object (branch ⑥ /
// malformed payloads): path only when trustworthy.
func dshDiagnostic(path string) map[string]any {
	if strings.TrimSpace(path) != "" {
		return map[string]any{"path": path}
	}
	return map[string]any{}
}

// dshUnwrapArguments decodes a tool-call block's arguments — the journal
// carries it as a JSON string wrapping the object (toolStepTitle unwraps the
// same double encoding); a plain object is accepted too. Nil on any failure.
func dshUnwrapArguments(arguments json.RawMessage) map[string]any {
	if len(arguments) == 0 {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(arguments, &args); err == nil {
		return args
	}
	var wrapped string
	if err := json.Unmarshal(arguments, &wrapped); err != nil {
		return nil
	}
	if err := json.Unmarshal([]byte(wrapped), &args); err != nil {
		return nil
	}
	return args
}
