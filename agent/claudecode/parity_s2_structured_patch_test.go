package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// S2 structuredPatch 统计切片（过程组对齐方案 v2.12 §5 S2 / §7）：
// transcript toolUseResult.structuredPatch → 投影 fileChanges。
//
// 分流规则按 structuredPatch 存在性、不按 type 字段（三轮 R22 证据：
// 6849 记录中 5859 条非空 patch 无 type 字段，858 条空 patch 全部
// type=create——按 type 分流会漏掉多数，且空 patch 禁止伪报 +0 −0）。
// 计数规则：全部 hunk 的 lines 首字符求和（'+' 计增、'-' 计删，
// ' ' context 与 '\' no-newline marker 忽略，跨 hunk 累加）。

func s2StepFileChanges(t *testing.T, useResult string) map[string]any {
	t.Helper()
	changes := claudeFileChangesFromToolUseResult(json.RawMessage(useResult))
	if len(changes) != 1 {
		t.Fatalf("changes = %d entries, want 1 (got %#v)", len(changes), changes)
	}
	return changes[0]
}

func TestS2UpdatePatchCountsAdditionsDeletions(t *testing.T) {
	// update 非空 hunk：'+'/'-' 前缀计数，' ' context 与 '\' marker 忽略。
	change := s2StepFileChanges(t, `{
		"type": "update",
		"filePath": "src/views/Foo.swift",
		"structuredPatch": [{
			"oldStart": 10, "oldLines": 4, "newStart": 10, "newLines": 5,
			"lines": [" context", "-removed", "+added", "+added2", "\\ No newline at end of file"]
		}]
	}`)
	if change["path"] != "src/views/Foo.swift" {
		t.Errorf("path = %v, want src/views/Foo.swift", change["path"])
	}
	if change["kind"] != "edit" {
		t.Errorf("kind = %v, want edit (update → edit 词汇对齐 codex)", change["kind"])
	}
	if add, ok := change["additions"].(int); !ok || add != 2 {
		t.Errorf("additions = %#v, want int 2", change["additions"])
	}
	if del, ok := change["deletions"].(int); !ok || del != 1 {
		t.Errorf("deletions = %#v, want int 1", change["deletions"])
	}
	diff, _ := change["diff"].(string)
	for _, want := range []string{"@@ -10,4 +10,5 @@", "-removed", "+added2", "\\ No newline at end of file"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff missing %q; diff=%q", want, diff)
		}
	}
}

func TestS2CreateEmptyPatchCarriesNoCounts(t *testing.T) {
	// create 空 patch：条目带 path/kind 但统计 nil——禁止写 0/0
	//（R22：create 行显示「已创建 文件名」无统计）。
	change := s2StepFileChanges(t, `{
		"type": "create",
		"filePath": "docs/new.md",
		"content": "# new\n",
		"structuredPatch": []
	}`)
	if change["path"] != "docs/new.md" {
		t.Errorf("path = %v, want docs/new.md", change["path"])
	}
	if change["kind"] != "create" {
		t.Errorf("kind = %v, want create", change["kind"])
	}
	if _, has := change["additions"]; has {
		t.Errorf("additions must be absent for empty structuredPatch (nil stats, not 0/0): %#v", change)
	}
	if _, has := change["deletions"]; has {
		t.Errorf("deletions must be absent for empty structuredPatch (nil stats, not 0/0): %#v", change)
	}
	if _, has := change["diff"]; has {
		t.Error("diff must be absent for empty structuredPatch (no hunks to render)")
	}
}

func TestS2MultiHunkSumsAcrossHunks(t *testing.T) {
	// 多 hunk 跨 hunk 累加（R22 实证最大 42 hunk；此处 3 hunk 验证累加）。
	change := s2StepFileChanges(t, `{
		"filePath": "big.go",
		"structuredPatch": [
			{"oldStart": 1, "oldLines": 2, "newStart": 1, "newLines": 3, "lines": [" a", "-b", "+c", "+d"]},
			{"oldStart": 20, "oldLines": 1, "newStart": 21, "newLines": 1, "lines": [" e", "-f"]},
			{"oldStart": 40, "oldLines": 1, "newStart": 41, "newLines": 2, "lines": [" g", "+h"]}
		]
	}`)
	if add, ok := change["additions"].(int); !ok || add != 3 {
		t.Errorf("additions = %#v, want int 3 (1+1+1 across hunks)", change["additions"])
	}
	if del, ok := change["deletions"].(int); !ok || del != 2 {
		t.Errorf("deletions = %#v, want int 2 (1+1 across hunks)", change["deletions"])
	}
	diff, _ := change["diff"].(string)
	if got := strings.Count(diff, "@@ -"); got != 3 {
		t.Errorf("diff should carry 3 hunk headers, got %d (diff=%q)", got, diff)
	}
}

func TestS2TypeMissingRecordStillCounts(t *testing.T) {
	// type 缺失（5859/6849 真实记录形态）：按 structuredPatch 存在性分流，
	// 仍可计数；kind 缺省 edit。
	change := s2StepFileChanges(t, `{
		"filePath": "no-type.md",
		"structuredPatch": [{
			"oldStart": 1, "oldLines": 1, "newStart": 1, "newLines": 1,
			"lines": [" x", "-y", "+z"]
		}]
	}`)
	if change["kind"] != "edit" {
		t.Errorf("kind = %v, want edit (default when type missing)", change["kind"])
	}
	if add, ok := change["additions"].(int); !ok || add != 1 {
		t.Errorf("additions = %#v, want int 1", change["additions"])
	}
	if del, ok := change["deletions"].(int); !ok || del != 1 {
		t.Errorf("deletions = %#v, want int 1", change["deletions"])
	}
}

func TestS2LegitimateZeroVersusNil(t *testing.T) {
	// 合法 0：非空 hunk 但全部 context 行（真实零行变更）→ 写 0/0。
	legit := s2StepFileChanges(t, `{
		"filePath": "ctx-only.md",
		"structuredPatch": [{
			"oldStart": 1, "oldLines": 2, "newStart": 1, "newLines": 2,
			"lines": [" a", " b"]
		}]
	}`)
	if add, ok := legit["additions"].(int); !ok || add != 0 {
		t.Errorf("legitimate zero additions = %#v, want int 0 (present, not nil)", legit["additions"])
	}
	if del, ok := legit["deletions"].(int); !ok || del != 0 {
		t.Errorf("legitimate zero deletions = %#v, want int 0 (present, not nil)", legit["deletions"])
	}
	// nil：空 patch → 键缺失（与合法 0 区分——nil≠0，R56/R61）。
	nilStats := s2StepFileChanges(t, `{
		"type": "create", "filePath": "empty.md", "structuredPatch": []
	}`)
	if _, has := nilStats["additions"]; has {
		t.Error("empty patch must not carry additions (nil, distinct from legitimate 0)")
	}
}

func TestS2NonFileResultsProduceNoFileChanges(t *testing.T) {
	// Read（type=text，filePath 嵌套在 file 下）/ Bash（success）等非
	// Edit/Write 形状：无顶层 filePath → 不产 fileChanges（read 保持
	// output 档，§4.7 resolver 分层不变）。
	for _, useResult := range []string{
		`{"type":"text","file":{"filePath":"nested.md","content":"body"}}`,
		`{"success":true,"commandName":"git status"}`,
		`{"type":"image","source":{"type":"base64"}}`,
		`{}`,
	} {
		if changes := claudeFileChangesFromToolUseResult(json.RawMessage(useResult)); changes != nil {
			t.Errorf("useResult %s must not produce fileChanges, got %#v", useResult, changes)
		}
	}
	// 缺失 / 非法 JSON：fail-closed 不崩溃、不产条目。
	if changes := claudeFileChangesFromToolUseResult(nil); changes != nil {
		t.Errorf("nil useResult must not produce fileChanges, got %#v", changes)
	}
	if changes := claudeFileChangesFromToolUseResult(json.RawMessage(`{not json`)); changes != nil {
		t.Errorf("malformed useResult must not produce fileChanges, got %#v", changes)
	}
	// structuredPatch 非 list（形状异常）：按缺失处理 → 无统计条目。
	change := s2StepFileChanges(t, `{"type":"update","filePath":"weird.md","structuredPatch":{"lines":[]}}`)
	if _, has := change["additions"]; has {
		t.Errorf("non-list structuredPatch must carry no counts: %#v", change)
	}
}

// --- transcript 路径端到端（LoadClaudeRichHistoryFromReader） ---

const s2AssistantToolUseLine = `{"type":"assistant","timestamp":"2026-09-22T10:00:00.000Z","uuid":"a1","message":{"id":"msg_a1","role":"assistant","content":[{"type":"tool_use","id":"toolu_s2_1","name":"Edit","input":{"file_path":"src/Foo.swift","old_string":"x","new_string":"y"}}]}}`

const s2UserToolResultLine = `{"type":"user","timestamp":"2026-09-22T10:00:01.000Z","uuid":"u1","message":{"id":"msg_u1","role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_s2_1","content":"The file has been updated."}]},"toolUseResult":{"filePath":"src/Foo.swift","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":2,"lines":[" keep","-x","+y"]}]}}`

func TestS2TranscriptPathMapsFileChanges(t *testing.T) {
	// transcript/history 路径映射：JSONL → rich history step 携带
	// fileChanges（path/kind=edit/diff/additions/deletions）。
	entries, err := LoadClaudeRichHistoryFromReader(strings.NewReader(s2AssistantToolUseLine+"\n"+s2UserToolResultLine+"\n"), "test.jsonl")
	if err != nil {
		t.Fatalf("LoadClaudeRichHistoryFromReader: %v", err)
	}
	var step map[string]any
	for _, entry := range entries {
		for _, candidate := range entry.Steps {
			if candidate["toolName"] == "Edit" {
				step = candidate
			}
		}
	}
	if step == nil {
		t.Fatalf("Edit step not found in %d entries", len(entries))
	}
	changes, ok := step["fileChanges"].([]map[string]any)
	if !ok || len(changes) != 1 {
		t.Fatalf("step.fileChanges = %#v, want 1 entry (transcript path mapping)", step["fileChanges"])
	}
	change := changes[0]
	if change["path"] != "src/Foo.swift" {
		t.Errorf("path = %v, want src/Foo.swift", change["path"])
	}
	if add, ok := change["additions"].(int); !ok || add != 1 {
		t.Errorf("additions = %#v, want int 1", change["additions"])
	}
	if del, ok := change["deletions"].(int); !ok || del != 1 {
		t.Errorf("deletions = %#v, want int 1", change["deletions"])
	}
	if diff, _ := change["diff"].(string); !strings.Contains(diff, "+y") || !strings.Contains(diff, "-x") {
		t.Errorf("diff = %q, want rendered hunk containing -x/+y", diff)
	}
}

func TestS2TranscriptPathCreateRowHasNoCounts(t *testing.T) {
	// 端到端 create：Write 空 patch → step 携带无统计条目（矩阵第 15 行：
	// create 行「已创建 文件名」无统计数字，不显示 0/0）。
	assistant := `{"type":"assistant","timestamp":"2026-09-22T10:00:00.000Z","uuid":"a2","message":{"id":"msg_a2","role":"assistant","content":[{"type":"tool_use","id":"toolu_s2_2","name":"Write","input":{"file_path":"docs/new.md","content":"# hi\n"}}]}}`
	user := `{"type":"user","timestamp":"2026-09-22T10:00:01.000Z","uuid":"u2","message":{"id":"msg_u2","role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_s2_2","content":"File created successfully."}]},"toolUseResult":{"type":"create","filePath":"docs/new.md","content":"# hi\n","structuredPatch":[]}}`
	entries, err := LoadClaudeRichHistoryFromReader(strings.NewReader(assistant+"\n"+user+"\n"), "test.jsonl")
	if err != nil {
		t.Fatalf("LoadClaudeRichHistoryFromReader: %v", err)
	}
	var step map[string]any
	for _, entry := range entries {
		for _, candidate := range entry.Steps {
			if candidate["toolName"] == "Write" {
				step = candidate
			}
		}
	}
	if step == nil {
		t.Fatalf("Write step not found")
	}
	changes, ok := step["fileChanges"].([]map[string]any)
	if !ok || len(changes) != 1 {
		t.Fatalf("step.fileChanges = %#v, want 1 path-bearing entry", step["fileChanges"])
	}
	if _, has := changes[0]["additions"]; has {
		t.Errorf("create entry must not carry additions (nil stats): %#v", changes[0])
	}
	if changes[0]["kind"] != "create" {
		t.Errorf("kind = %v, want create", changes[0]["kind"])
	}
}

func TestS2TranscriptPathReadStaysOutputTier(t *testing.T) {
	// 端到端 Read：toolUseResult type=text（filePath 嵌套）→ 不产
	// fileChanges，read 详情保持行号纯文本（output 档）。
	assistant := `{"type":"assistant","timestamp":"2026-09-22T10:00:00.000Z","uuid":"a3","message":{"id":"msg_a3","role":"assistant","content":[{"type":"tool_use","id":"toolu_s2_3","name":"Read","input":{"file_path":"nested.md"}}]}}`
	user := `{"type":"user","timestamp":"2026-09-22T10:00:01.000Z","uuid":"u3","message":{"id":"msg_u3","role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_s2_3","content":"     1\tline one"}]},"toolUseResult":{"type":"text","file":{"filePath":"nested.md","content":"line one"}}}`
	entries, err := LoadClaudeRichHistoryFromReader(strings.NewReader(assistant+"\n"+user+"\n"), "test.jsonl")
	if err != nil {
		t.Fatalf("LoadClaudeRichHistoryFromReader: %v", err)
	}
	for _, entry := range entries {
		for _, step := range entry.Steps {
			if step["toolName"] == "Read" {
				if _, has := step["fileChanges"]; has {
					t.Errorf("Read step must not carry fileChanges: %#v", step["fileChanges"])
				}
			}
		}
	}
}

func TestS2PendingResultCarriesUseResult(t *testing.T) {
	// 结果先于 tool_use 到达（pendingToolResults 路径）：UseResult 随
	// transcriptToolResult 暂存，tool_use 出现时同样挂上 fileChanges。
	user := `{"type":"user","timestamp":"2026-09-22T10:00:00.000Z","uuid":"u4","message":{"id":"msg_u4","role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_s2_4","content":"ok"}]},"toolUseResult":{"filePath":"early.swift","structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":1,"lines":[" a","+b"]}]}}`
	assistant := `{"type":"assistant","timestamp":"2026-09-22T10:00:01.000Z","uuid":"a4","message":{"id":"msg_a4","role":"assistant","content":[{"type":"tool_use","id":"toolu_s2_4","name":"Edit","input":{"file_path":"early.swift","old_string":"a","new_string":"b"}}]}}`
	entries, err := LoadClaudeRichHistoryFromReader(strings.NewReader(user+"\n"+assistant+"\n"), "test.jsonl")
	if err != nil {
		t.Fatalf("LoadClaudeRichHistoryFromReader: %v", err)
	}
	for _, entry := range entries {
		for _, step := range entry.Steps {
			if step["toolName"] == "Edit" {
				changes, ok := step["fileChanges"].([]map[string]any)
				if !ok || len(changes) != 1 {
					t.Fatalf("pending-result Edit step.fileChanges = %#v, want 1 entry", step["fileChanges"])
				}
				if add, ok := changes[0]["additions"].(int); !ok || add != 1 {
					t.Errorf("additions = %#v, want int 1 (UseResult survived the pending path)", changes[0]["additions"])
				}
				return
			}
		}
	}
	t.Fatal("Edit step not found")
}

// 防回归：既有 builder 行为不受 UseResult 影响（无 toolUseResult 的
// tool_result 不产 fileChanges，title/toolInput 照旧）。
func TestS2NoUseResultKeepsPriorShape(t *testing.T) {
	b := newRichHistoryMessageBuilder("msg-s2", "assistant", time.Time{})
	input := json.RawMessage(`{"file_path":"plain.swift","old_string":"a","new_string":"b"}`)
	id := b.addToolUse("toolu_s2_5", "Edit", input)
	ok := b.applyToolResult(id, transcriptToolResult{Output: "done", IsError: false})
	if !ok {
		t.Fatal("applyToolResult failed")
	}
	step := b.Steps[id]
	if _, has := step["fileChanges"]; has {
		t.Errorf("step must not carry fileChanges without toolUseResult: %#v", step["fileChanges"])
	}
	if title, _ := step["title"].(string); title != "plain.swift" {
		t.Errorf("title = %q, want plain.swift (L-α untouched)", title)
	}
}
