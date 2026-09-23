package dshweb

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/core"
)

// S3 clean-detail 切片（过程组对齐方案 v2.12 §5 S3）：journal/history 路径
// 消费 tool/result.data.meta 与 tool/call.arguments，映射投影结构化展示
// 载荷（fileDisplay / editRegions / detailUnavailable）+ block 级 isError
// 状态映射（与 live codec.go:747-757 同形）。
//
// live 不外推（R34/R35 同语义）：live codec 的 core.Event 无 display 字段，
// 结构上不可携带——live 观看的 dsh turn 在冷拉前保持现状（XML output），
// 为已知边界；本文件只测 history 路径。
//
// 分支基数实证（方案证据档案）：diffs 0→63（全 write-create）/ 1→279 /
// 多→17；错误 edit 15 + write 8。

// ── 分支① read → fileDisplay 五边界 ─────────────────────────────────────

func s3ReadStep(t *testing.T, meta string) map[string]any {
	t.Helper()
	fd, regions, diag := dshCleanToolDisplay("read", json.RawMessage(`{"file_path":"a.txt"}`), json.RawMessage(meta))
	if regions != nil || diag != nil {
		t.Fatalf("read meta must map to fileDisplay only: regions=%#v diag=%#v", regions, diag)
	}
	return fd
}

func TestS3ReadFileDisplayOffsetOne(t *testing.T) {
	fd := s3ReadStep(t, `{"path":"a.txt","offset":1,"totalLines":10,
		"lines":[{"number":1,"text":"one"},{"number":2,"text":"two"}]}`)
	if fd["path"] != "a.txt" || fd["text"] != "one\ntwo" {
		t.Fatalf("fileDisplay = %#v", fd)
	}
	if fd["lineStart"] != 1 || fd["lineEnd"] != 2 || fd["totalLines"] != 10 {
		t.Fatalf("range = %#v", fd)
	}
	if fd["truncated"] != true {
		t.Fatalf("window 1-2 of 10 must be truncated: %#v", fd)
	}
}

func TestS3ReadFileDisplayOffsetGreaterThanOne(t *testing.T) {
	fd := s3ReadStep(t, `{"path":"a.txt","offset":40,"totalLines":50,
		"lines":[{"number":40,"text":"x"},{"number":41,"text":"y"}]}`)
	if fd["lineStart"] != 40 || fd["lineEnd"] != 41 {
		t.Fatalf("window keeps the file's own numbering: %#v", fd)
	}
	if fd["text"] != "x\ny" {
		t.Fatalf("text = %#v", fd["text"])
	}
}

func TestS3ReadFileDisplayEOFFullWindow(t *testing.T) {
	// EOF：窗口覆盖到文件末尾 → truncated false。
	fd := s3ReadStep(t, `{"path":"a.txt","offset":9,"totalLines":10,
		"lines":[{"number":9,"text":"i"},{"number":10,"text":"j"}]}`)
	if fd["truncated"] != false || fd["lineEnd"] != 10 {
		t.Fatalf("EOF window must not be truncated: %#v", fd)
	}
}

func TestS3ReadFileDisplayEmptyLinesWindow(t *testing.T) {
	// 空 lines：lineStart=offset、lineEnd=offset-1（官方 ReadResultView.offset
	// 语义——字节预算在首行前耗尽的空窗口）；空文件（totalLines=0）则落
	// 1/0/0 空文件形态。
	fd := s3ReadStep(t, `{"path":"a.txt","offset":5,"totalLines":10,"lines":[]}`)
	if fd["lineStart"] != 5 || fd["lineEnd"] != 4 || fd["text"] != "" {
		t.Fatalf("empty window = %#v", fd)
	}
	fdEmptyFile := s3ReadStep(t, `{"path":"empty.txt","offset":1,"totalLines":0,"lines":[]}`)
	if fdEmptyFile["lineStart"] != 1 || fdEmptyFile["lineEnd"] != 0 || fdEmptyFile["totalLines"] != 0 {
		t.Fatalf("empty file shape = %#v", fdEmptyFile)
	}
}

func TestS3ReadFileDisplayJoinWithEmptyLines(t *testing.T) {
	// R46：text = lines.map(text).joined(separator: "\n")——空行/连续空行
	// 保留为独立行，禁无分隔符或 JSON 序列化拼接。
	fd := s3ReadStep(t, `{"path":"a.txt","offset":1,"totalLines":5,
		"lines":[{"number":1,"text":"a"},{"number":2,"text":""},{"number":3,"text":""},{"number":4,"text":"b"}]}`)
	if fd["text"] != "a\n\n\nb" {
		t.Fatalf("LF join with empty lines = %#v", fd["text"])
	}
}

// ── 分支① 行号 malformed fixture（R55 七不变量 fail-closed） ────────────

func TestS3ReadLineNumberInvariants(t *testing.T) {
	cases := map[string]string{
		"offset 0":        `{"path":"a","offset":0,"totalLines":3,"lines":[{"number":1,"text":"x"}]}`,
		"first 0":         `{"path":"a","offset":0,"totalLines":3,"lines":[{"number":0,"text":"x"}]}`,
		"乱序":              `{"path":"a","offset":1,"totalLines":3,"lines":[{"number":2,"text":"x"},{"number":1,"text":"y"}]}`,
		"[1,3,5] 跳行":      `{"path":"a","offset":1,"totalLines":5,"lines":[{"number":1,"text":"x"},{"number":3,"text":"y"},{"number":5,"text":"z"}]}`,
		"末行越界":            `{"path":"a","offset":1,"totalLines":2,"lines":[{"number":1,"text":"x"},{"number":3,"text":"y"}]}`,
		"first != offset": `{"path":"a","offset":2,"totalLines":3,"lines":[{"number":1,"text":"x"}]}`,
		"text 内嵌 LF":      `{"path":"a","offset":1,"totalLines":3,"lines":[{"number":1,"text":"x\ny"}]}`,
		"number < offset": `{"path":"a","offset":3,"totalLines":5,"lines":[{"number":2,"text":"x"}]}`,
		"totalLines 负":    `{"path":"a","offset":1,"totalLines":-1,"lines":[]}`,
	}
	for name, meta := range cases {
		fd, regions, diag := dshCleanToolDisplay("read", json.RawMessage(`{"file_path":"a"}`), json.RawMessage(meta))
		if fd != nil || regions != nil || diag == nil {
			t.Errorf("%s: want detailUnavailable, got fd=%#v regions=%#v diag=%#v", name, fd, regions, diag)
		}
	}
}

// ── 分支② write-create → arguments 解析 ─────────────────────────────────

func TestS3WriteCreateFromArguments(t *testing.T) {
	// diffs:[] + write → 从 arguments {file_path, content} 构造整文件展示。
	fd, regions, diag := dshCleanToolDisplay("write",
		json.RawMessage(`{"file_path":"new.md","content":"# hi\nline2\n"}`),
		json.RawMessage(`{"diffs":[]}`))
	if regions != nil || diag != nil {
		t.Fatalf("write-create maps to fileDisplay only: %#v %#v", regions, diag)
	}
	if fd["path"] != "new.md" || fd["text"] != "# hi\nline2\n" {
		t.Fatalf("fileDisplay = %#v", fd)
	}
	if fd["lineStart"] != 1 || fd["lineEnd"] != 2 || fd["totalLines"] != 2 {
		t.Fatalf("range = %#v (content with trailing LF = 2 lines)", fd)
	}
	if fd["truncated"] != false {
		t.Fatalf("write-create is never truncated: %#v", fd)
	}
}

func TestS3WriteCreateArgumentsParseFailure(t *testing.T) {
	// arguments 缺 content / 非法 JSON → detailUnavailable 对象（不回退 XML）。
	for _, args := range []string{
		`{"file_path":"new.md"}`,
		`{"file_path":"new.md","content":42}`,
		`not json`,
	} {
		fd, regions, diag := dshCleanToolDisplay("write", json.RawMessage(args), json.RawMessage(`{"diffs":[]}`))
		if fd != nil || regions != nil || diag == nil {
			t.Errorf("args %s: want detailUnavailable, got fd=%#v diag=%#v", args, fd, diag)
		}
	}
	// path 可得时随诊断携带。
	_, _, diag := dshCleanToolDisplay("write", json.RawMessage(`{"file_path":"new.md"}`), json.RawMessage(`{"diffs":[]}`))
	if diag["path"] != "new.md" {
		t.Errorf("diagnostic should carry the trustworthy path: %#v", diag)
	}
}

func TestS3WriteCreateEmptyContent(t *testing.T) {
	// 空内容 → 1/0/0 空文件形态（lineRange nil，禁止 1...0）。
	fd, _, diag := dshCleanToolDisplay("write",
		json.RawMessage(`{"file_path":"empty.md","content":""}`),
		json.RawMessage(`{"diffs":[]}`))
	if diag != nil {
		t.Fatalf("empty content is legal: %#v", diag)
	}
	if fd["lineStart"] != 1 || fd["lineEnd"] != 0 || fd["totalLines"] != 0 || fd["text"] != "" {
		t.Fatalf("empty-file shape = %#v", fd)
	}
}

// ── 分支⑦ write-create 行数公式（R33）五输入 ───────────────────────────

func TestS3WriteCreateLineFormula(t *testing.T) {
	cases := map[string]int{
		"":       0, // 空串 → 0
		"a":      1, // 无尾 LF → N
		"a\n":    1, // 尾 LF → N（trailing LF 不产生额外空行）
		"a\n\n":  2, // 连续空行仍占行
		"a\r\nb": 2, // CRLF：行分隔只认 LF——\r 不是分隔符，"a\r\nb" = 1 LF + 无尾 LF = 2
		"a\rb":   1, // CR-only：只认 LF → 单行（text 原样保留不归一）
	}
	for content, want := range cases {
		if got := dshWriteCreateLineCount(content); got != want {
			t.Errorf("lineCount(%q) = %d, want %d", content, got, want)
		}
	}
}

// ── 分支③ edit / write-update → editRegions ─────────────────────────────

func s3EditMeta(hunks int) string {
	entries := make([]string, hunks)
	for i := 0; i < hunks; i++ {
		entries[i] = `{"path":"a.txt","oldText":"old` + string(rune('0'+i)) + `","newText":"new` + string(rune('0'+i)) + `"}`
	}
	return `{"diffs":[` + strings.Join(entries, ",") + `]}`
}

func TestS3EditSingleHunkEditRegions(t *testing.T) {
	fd, regions, diag := dshCleanToolDisplay("edit",
		json.RawMessage(`{"file_path":"a.txt","old_string":"o","new_string":"n"}`),
		json.RawMessage(s3EditMeta(1)))
	if fd != nil || diag != nil {
		t.Fatalf("edit maps to editRegions only: %#v %#v", fd, diag)
	}
	if len(regions) != 1 || regions[0]["path"] != "a.txt" || regions[0]["newText"] != "new0" {
		t.Fatalf("regions = %#v", regions)
	}
}

func TestS3EditMultiHunkAllRegionsInOrder(t *testing.T) {
	// 按序全量，不取 [0]（17/17 多 hunk 实证）。
	for _, hunks := range []int{2, 12} {
		_, regions, _ := dshCleanToolDisplay("edit", json.RawMessage(`{"file_path":"a.txt"}`),
			json.RawMessage(s3EditMeta(hunks)))
		if len(regions) != hunks {
			t.Fatalf("hunks=%d: regions=%d, want all", hunks, len(regions))
		}
		for i, region := range regions {
			if region["newText"] != "new"+string(rune('0'+i)) {
				t.Fatalf("region %d out of order: %#v", i, region)
			}
		}
	}
	// write-update（diffs 非空）同分支。
	_, writeRegions, _ := dshCleanToolDisplay("write", json.RawMessage(`{"file_path":"a.txt","content":"x"}`),
		json.RawMessage(s3EditMeta(2)))
	if len(writeRegions) != 2 {
		t.Fatalf("write-update multi-hunk = %#v", writeRegions)
	}
}

func TestS3EditMalformedDiffEntry(t *testing.T) {
	// 缺 newText（与合法空串 deletion-only 区分）/ 缺 path → 整体 detailUnavailable。
	for _, meta := range []string{
		`{"diffs":[{"path":"a.txt","oldText":"o"}]}`,
		`{"diffs":[{"oldText":"o","newText":"n"}]}`,
		`{"diffs":[{"path":"","newText":"n"}]}`,
	} {
		fd, regions, diag := dshCleanToolDisplay("edit", json.RawMessage(`{"file_path":"a.txt"}`), json.RawMessage(meta))
		if fd != nil || regions != nil || diag == nil {
			t.Errorf("meta %s: want detailUnavailable, got %#v %#v %#v", meta, fd, regions, diag)
		}
	}
	// 合法空串 newText（deletion-only hunk）不误伤。
	_, regions, diag := dshCleanToolDisplay("edit", json.RawMessage(`{"file_path":"a.txt"}`),
		json.RawMessage(`{"diffs":[{"path":"a.txt","oldText":"gone","newText":""}]}`))
	if diag != nil || len(regions) != 1 || regions[0]["newText"] != "" {
		t.Fatalf("deletion-only hunk is legal: %#v %#v", regions, diag)
	}
}

// ── 分支⑥ 未知成功形状 ───────────────────────────────────────────────────

func TestS3UnknownSuccessShapeFailClosed(t *testing.T) {
	// read/write/edit 成功但无可用 meta → detailUnavailable {path?}。
	_, _, diag := dshCleanToolDisplay("read", json.RawMessage(`{"file_path":"a.txt"}`), nil)
	if diag == nil || diag["path"] != "a.txt" {
		t.Fatalf("read without meta = %#v", diag)
	}
	_, _, diag = dshCleanToolDisplay("write", json.RawMessage(`{"file_path":"a.txt","content":"x"}`), json.RawMessage(`{}`))
	if diag == nil || diag["path"] != "a.txt" {
		t.Fatalf("write with unusable meta = %#v", diag)
	}
	// path 不可得 → 无 path 键。
	_, _, diag = dshCleanToolDisplay("edit", nil, nil)
	if diag == nil {
		t.Fatal("edit without meta must fail closed")
	}
	if _, has := diag["path"]; has {
		t.Fatalf("no trustworthy path → omit the key: %#v", diag)
	}
	// edit 产生空 diffs 属未知成功形状（官方 edit 恒输出 hunk）。
	_, _, diag = dshCleanToolDisplay("edit", json.RawMessage(`{"file_path":"a.txt"}`), json.RawMessage(`{"diffs":[]}`))
	if diag == nil {
		t.Fatal("edit with empty diffs must fail closed")
	}
}

// ── 状态映射⑤ + 端到端（journal → step） ────────────────────────────────

func s3ToolEvents(name, arguments, resultMeta string, isError bool) []sessionEventWire {
	resultBlocks := `"content": [{"type": "text", "text": "ok"}]`
	if isError {
		resultBlocks = `"content": [{"type": "text", "text": "Error: [sandbox: file access denied]"}]`
	}
	isErrJSON := "false"
	if isError {
		isErrJSON = "true"
	}
	metaField := ""
	if resultMeta != "" {
		metaField = `, "meta": ` + resultMeta
	}
	return []sessionEventWire{
		mkHistoryEntry("turn/start", 100, `{"turn": 1}`),
		mkHistoryEntry("assistant/message", 101, `{
			"turn": 1, "step": 1,
			"message": {"role": "assistant", "content": [
				{"type": "tool-call", "id": "call_s3", "name": "`+name+`", "arguments": `+arguments+`}
			]}
		}`),
		mkHistoryEntry("tool/result", 102, `{
			"turn": 1, "step": 1,
			"message": {"toolCallId": "call_s3", "isError": `+isErrJSON+`, "source": {"kind": "tool", "callId": "call_s3"},
			 `+resultBlocks+`}`+metaField+`
		}`),
		mkHistoryEntry("turn/end", 200, `{"turn": 1}`),
	}
}

// s3ToolEventsNoResult：journal 无该 call 的 tool/result（pending/中断残留）。
func s3ToolEventsNoResult(name, arguments string) []sessionEventWire {
	return []sessionEventWire{
		mkHistoryEntry("turn/start", 100, `{"turn": 1}`),
		mkHistoryEntry("assistant/message", 101, `{
			"turn": 1, "step": 1,
			"message": {"role": "assistant", "content": [
				{"type": "tool-call", "id": "call_s3", "name": "`+name+`", "arguments": `+arguments+`}
			]}
		}`),
		mkHistoryEntry("turn/end", 200, `{"turn": 1}`),
	}
}

func s3FirstToolStep(t *testing.T, evs []sessionEventWire) map[string]any {
	t.Helper()
	entries := mapHistoryEvents("s3sess", evs)
	parts := toolPartsOf(entries)
	if len(parts) == 0 {
		t.Fatal("no tool part")
	}
	step, ok := parts[0]["step"].(map[string]any)
	if !ok {
		t.Fatal("tool part has no step")
	}
	return step
}

func TestS3StatusMappingColdResults(t *testing.T) {
	// 成功 read/write/edit → completed；错误 → failed；无 result → unknown。
	readMeta := `{"path":"a.txt","offset":1,"totalLines":1,"lines":[{"number":1,"text":"x"}]}`
	for _, name := range []string{"read", "write", "edit"} {
		step := s3FirstToolStep(t, s3ToolEvents(name, `{"file_path":"a.txt"}`, readMeta, false))
		if step["status"] != "completed" {
			t.Errorf("%s success: status = %v, want completed", name, step["status"])
		}
		step = s3FirstToolStep(t, s3ToolEvents(name, `{"file_path":"a.txt"}`, "", true))
		if step["status"] != "failed" {
			t.Errorf("%s error: status = %v, want failed", name, step["status"])
		}
	}
	step := s3FirstToolStep(t, s3ToolEventsNoResult("read", `{"file_path":"a.txt"}`))
	if step["status"] != "unknown" {
		t.Errorf("no result: status = %v, want unknown (never fabricate a terminal state)", step["status"])
	}
}

func TestS3JournalEndToEndStructuredPayloads(t *testing.T) {
	// read：fileDisplay 落到 step（hydrate/reducer plumbing 为 P2 既有）。
	readMeta := `{"path":"a.txt","offset":1,"totalLines":3,"lines":[{"number":1,"text":"x"},{"number":2,"text":"y"}]}`
	step := s3FirstToolStep(t, s3ToolEvents("read", `{"file_path":"a.txt"}`, readMeta, false))
	fd, ok := step["fileDisplay"].(map[string]any)
	if !ok {
		t.Fatalf("read step.fileDisplay = %#v", step["fileDisplay"])
	}
	if fd["text"] != "x\ny" || fd["lineStart"] != 1 || fd["lineEnd"] != 2 || fd["totalLines"] != 3 {
		t.Fatalf("fileDisplay = %#v", fd)
	}
	// write-create：arguments 构造整文件展示。
	step = s3FirstToolStep(t, s3ToolEvents("write", `"{\"file_path\":\"n.md\",\"content\":\"a\\nb\"}"`, `{"diffs":[]}`, false))
	fd, ok = step["fileDisplay"].(map[string]any)
	if !ok {
		t.Fatalf("write-create step.fileDisplay = %#v", step["fileDisplay"])
	}
	if fd["lineEnd"] != 2 || fd["text"] != "a\nb" {
		t.Fatalf("write-create fileDisplay = %#v", fd)
	}
	// edit：editRegions 按序全量。
	step = s3FirstToolStep(t, s3ToolEvents("edit", `{"file_path":"a.txt"}`, s3EditMeta(2), false))
	regions, ok := step["editRegions"].([]map[string]any)
	if !ok || len(regions) != 2 {
		t.Fatalf("edit step.editRegions = %#v", step["editRegions"])
	}
	if regions[0]["newText"] != "new0" || regions[1]["newText"] != "new1" {
		t.Fatalf("regions order = %#v", regions)
	}
	// 错误 result：原始错误文本 + failed，无结构化载荷（分支④）。
	step = s3FirstToolStep(t, s3ToolEvents("edit", `{"file_path":"a.txt"}`, readMeta, true))
	if step["status"] != "failed" {
		t.Fatalf("error status = %v", step["status"])
	}
	if _, has := step["fileDisplay"]; has {
		t.Fatal("error result must not carry fileDisplay (branch ④ keeps the raw error text)")
	}
	if _, has := step["editRegions"]; has {
		t.Fatal("error result must not carry editRegions")
	}
	// 未知成功形状：detailUnavailable 落到 step（分支⑥）。
	step = s3FirstToolStep(t, s3ToolEvents("read", `{"file_path":"a.txt"}`, `{"unexpected":true}`, false))
	diag, ok := step["detailUnavailable"].(map[string]any)
	if !ok {
		t.Fatalf("unknown success shape = %#v", step["detailUnavailable"])
	}
	if diag["path"] != "a.txt" {
		t.Fatalf("diagnostic path = %#v", diag)
	}
	// 非 file 工具（bash）：无 meta 成功 → output 档，无结构化载荷。
	step = s3FirstToolStep(t, s3ToolEvents("bash", `{"command":"ls"}`, "", false))
	if step["status"] != "completed" {
		t.Fatalf("bash status = %v", step["status"])
	}
	for _, key := range []string{"fileDisplay", "editRegions", "detailUnavailable"} {
		if _, has := step[key]; has {
			t.Errorf("bash step must not carry %s: %#v", key, step[key])
		}
	}
}

// ask_user_question 回归：状态映射改动不触碰问答卡路径（answered →
// completed 工具 step + 终态 user_input part）。
func TestS3AskUserQuestionRegression(t *testing.T) {
	entries := mapHistoryEvents("s3sess", askQuestionEvents(true))
	parts := toolPartsOf(entries)
	if len(parts) == 0 {
		t.Fatal("ask_user_question tool part missing")
	}
	step := parts[0]["step"].(map[string]any)
	if step["toolName"] != "ask_user_question" || step["status"] != "completed" {
		t.Fatalf("ask_user_question step = %#v", step)
	}
	ui := userInputPartsOf(entries)
	if len(ui) == 0 || ui[0]["status"] != "answered" {
		t.Fatalf("user_input parts = %#v", ui)
	}
}

// 编译期锚点：core.Event（live 路径）无 display 字段——live 不外推由构造保证。
var _ = core.Event{Type: core.EventToolResult}
