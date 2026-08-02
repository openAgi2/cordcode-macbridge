package claudecode

// structured_user_input_contract_test.go 锁定 Claude Code AskUserQuestion 的外部 wire 格式。
// 直接读取 testdata/structured_user_input/ 下已归档的成对 request/response/result 证据
// (Claude Code CLI 2.1.209)，不依赖 adapter 代码，也不得自造 fixture。
// 依据：docs/2026-08-01-codex-claude-structured-user-input-design.md §3.3 / §9 / §14 P0。
//
// 这是 P0 的格式冻结门：Claude v1 answers 恒为 option label（single=string / multiple=string[]），
// 多问题在同一个 updatedInput.answers map 内按原 question text 各写一个 entry；missing-options
// 在 SDK 阶段被拒，不会产生 control_request，因此不能归一化为文本题。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const claudeSUIdir = "testdata/structured_user_input"

func loadClaudePaired(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(claudeSUIdir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	return m
}

func pairedQuestions(paired map[string]any) []any {
	req, _ := paired["control_request"].(map[string]any)
	request, _ := req["request"].(map[string]any)
	input, _ := request["input"].(map[string]any)
	q, _ := input["questions"].([]any)
	return q
}

func updatedInputAnswers(paired map[string]any) map[string]any {
	resp, _ := paired["control_response"].(map[string]any)
	response, _ := resp["response"].(map[string]any)
	inner, _ := response["response"].(map[string]any)
	updated, _ := inner["updatedInput"].(map[string]any)
	answers, _ := updated["answers"].(map[string]any)
	return answers
}

// TestClaudeThreePairedRequestsAreAskUserQuestion：三组成对证据都是
// control_request / can_use_tool / AskUserQuestion。
func TestClaudeThreePairedRequestsAreAskUserQuestion(t *testing.T) {
	for _, name := range []string{"single_paired.json", "multi_select_paired.json", "multi_question_paired.json"} {
		paired := loadClaudePaired(t, name)
		req, _ := paired["control_request"].(map[string]any)
		if tt, _ := req["type"].(string); tt != "control_request" {
			t.Fatalf("%s control_request.type 应为 control_request，实际 %q", name, tt)
		}
		request, _ := req["request"].(map[string]any)
		if st, _ := request["subtype"].(string); st != "can_use_tool" {
			t.Fatalf("%s subtype 应为 can_use_tool，实际 %q", name, st)
		}
		if tn, _ := request["tool_name"].(string); tn != "AskUserQuestion" {
			t.Fatalf("%s tool_name 应为 AskUserQuestion，实际 %q", name, tn)
		}
	}
}

// TestClaudeResponseEnvelopeMatchesRequest：response.request_id == request.request_id；
// subtype=success；behavior=allow；updatedInput.questions 与原 input.questions 深相等。
func TestClaudeResponseEnvelopeMatchesRequest(t *testing.T) {
	for _, name := range []string{"single_paired.json", "multi_select_paired.json", "multi_question_paired.json"} {
		paired := loadClaudePaired(t, name)
		reqID, _ := paired["control_request"].(map[string]any)["request_id"].(string)
		resp, _ := paired["control_response"].(map[string]any)
		response, _ := resp["response"].(map[string]any)
		if rid, _ := response["request_id"].(string); rid != reqID {
			t.Fatalf("%s response.request_id(%q) != request.request_id(%q)", name, rid, reqID)
		}
		if st, _ := response["subtype"].(string); st != "success" {
			t.Fatalf("%s response.subtype 应为 success，实际 %q", name, st)
		}
		inner, _ := response["response"].(map[string]any)
		if b, _ := inner["behavior"].(string); b != "allow" {
			t.Fatalf("%s behavior 应为 allow，实际 %q", name, b)
		}
		updated, _ := inner["updatedInput"].(map[string]any)
		if !reflect.DeepEqual(updated["questions"], pairedQuestions(paired)) {
			t.Fatalf("%s updatedInput.questions 必须与原 input.questions 深相等", name)
		}
	}
}

// TestClaudeAnswersTypedPerMultiSelect：single=string label；multiple=string[] labels；
// 多问题 map 覆盖全部原 question text，每题 value 类型按其 multiSelect 编码。
func TestClaudeAnswersTypedPerMultiSelect(t *testing.T) {
	types := map[string]string{
		"single_paired.json":       "single",
		"multi_select_paired.json": "multiple",
		"multi_question_paired.json": "multi_question",
	}
	for name, mode := range types {
		paired := loadClaudePaired(t, name)
		questions := pairedQuestions(paired)
		answers := updatedInputAnswers(paired)
		if len(answers) != len(questions) {
			t.Fatalf("%s answers 条目数 %d != questions 数 %d", name, len(answers), len(questions))
		}
		for _, q := range questions {
			qm, _ := q.(map[string]any)
			qText, _ := qm["question"].(string)
			multiSelect, _ := qm["multiSelect"].(bool)
			val, ok := answers[qText]
			if !ok {
				t.Fatalf("%s answers 缺少 question %q", name, qText)
			}
			switch v := val.(type) {
			case string:
				if multiSelect {
					t.Fatalf("%s question %q multiSelect=true 但 answer 是 string（应数组）", name, qText)
				}
			case []any:
				if !multiSelect {
					t.Fatalf("%s question %q multiSelect=false 但 answer 是数组（应字符串）", name, qText)
				}
				if mode == "multiple" && len(v) < 1 {
					t.Fatalf("%s multi-select answer 不应为空数组", name)
				}
			default:
				t.Fatalf("%s question %q answer 类型异常 %T", name, qText, val)
			}
		}
	}
}

// TestClaudeAnswersAreOptionLabelsNotCustom：每个 answer value 都是该题 option label，
// 证明 Claude v1 永不产生任意 custom 文本（allowsCustomAnswer=false 的产品决定）。
func TestClaudeAnswersAreOptionLabelsNotCustom(t *testing.T) {
	for _, name := range []string{"single_paired.json", "multi_select_paired.json", "multi_question_paired.json"} {
		paired := loadClaudePaired(t, name)
		answers := updatedInputAnswers(paired)
		for _, q := range pairedQuestions(paired) {
			qm, _ := q.(map[string]any)
			qText, _ := qm["question"].(string)
			labels := map[string]bool{}
			for _, opt := range qm["options"].([]any) {
				om, _ := opt.(map[string]any)
				labels[om["label"].(string)] = true
			}
			val := answers[qText]
			values := []string{}
			switch v := val.(type) {
			case string:
				values = append(values, v)
			case []any:
				for _, x := range v {
					values = append(values, x.(string))
				}
			}
			for _, v := range values {
				if !labels[v] {
					t.Fatalf("%s question %q 的 answer %q 不是 option label → 属 custom，违反 Claude v1 fail-closed", name, qText, v)
				}
			}
		}
	}
}

// TestClaudeAllPairedResultsSuccessNoDenials：三组同 turn result.subtype=success，
// permission_denials 为空。
func TestClaudeAllPairedResultsSuccessNoDenials(t *testing.T) {
	for _, name := range []string{"single_paired.json", "multi_select_paired.json", "multi_question_paired.json"} {
		paired := loadClaudePaired(t, name)
		result, _ := paired["result_evidence"].(map[string]any)
		if st, _ := result["subtype"].(string); st != "success" {
			t.Fatalf("%s result_evidence.subtype 应为 success，实际 %q", name, st)
		}
		denials, _ := result["permission_denials"].([]any)
		if len(denials) != 0 {
			t.Fatalf("%s permission_denials 应为空，实际 %v", name, denials)
		}
	}
}

// TestClaudeNoOptionsRejectedBeforeControlRequest：missing-options 在 SDK validation 阶段
// 被拒，不会产生 control_request → adapter 不得把无 options question 归一化为文本题。
func TestClaudeNoOptionsRejectedBeforeControlRequest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(claudeSUIdir, "no_options_rejected.jsonl"))
	if err != nil {
		t.Fatalf("read no_options_rejected.jsonl: %v", err)
	}
	var lines []map[string]any
	for _, line := range splitJSONL(data) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("unmarshal jsonl line: %v", err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 2 {
		t.Fatalf("no_options_rejected.jsonl 应有 2 行，实际 %d", len(lines))
	}
	// 第 1 行：agent 构造的 question 缺 options。
	if ev, _ := lines[0]["evidence"].(string); ev != "agent_constructed_no_options_question" {
		t.Fatalf("第 1 行 evidence 应为 agent_constructed_no_options_question，实际 %q", ev)
	}
	input, _ := lines[0]["tool_use_input"].(map[string]any)
	questions, _ := input["questions"].([]any)
	firstQ, _ := questions[0].(map[string]any)
	if _, hasOptions := firstQ["options"]; hasOptions {
		t.Fatalf("构造的 question 不应带 options（用于触发 SDK 拒绝）")
	}
	// 第 2 行：SDK 拒绝，错误信息含 questions[0].options is missing。
	if ev, _ := lines[1]["evidence"].(string); ev != "sdk_rejected_missing_options" {
		t.Fatalf("第 2 行 evidence 应为 sdk_rejected_missing_options，实际 %q", ev)
	}
	toolResult, _ := lines[1]["tool_result"].(string)
	// SDK 错误把字段名包在反引号里（`questions[0].options` is missing），故分别断言两个稳定片段。
	if !strings.Contains(toolResult, "questions[0].options") || !strings.Contains(toolResult, "is missing") {
		t.Fatalf("SDK 拒绝信息应含 'questions[0].options' 与 'is missing'，实际 %q", toolResult)
	}
	// 整个证据不含 control_request → 证明未到达交互阶段，无法归一化。
	for _, l := range lines {
		if _, has := l["control_request"]; has {
			t.Fatalf("no_options 路径不应产生 control_request")
		}
	}
}

// TestClaudeManifestCrossCheck：锁定 testdata manifest 与归档 claude_manifest.json 一致。
func TestClaudeManifestCrossCheck(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(claudeSUIdir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	keys := make([]string, 0, len(manifest.Files))
	for k := range manifest.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, basename := range keys {
		// manifest key 是证据归档名（claude_xxx.json）；testdata 文件名去掉了 claude_ 前缀。
		tdName := basename
		if len(tdName) > 7 && tdName[:7] == "claude_" {
			tdName = tdName[7:]
		}
		data, err := os.ReadFile(filepath.Join(claudeSUIdir, tdName))
		if err != nil {
			t.Fatalf("manifest 引用文件缺失 %s (%s): %v", basename, tdName, err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if got != manifest.Files[basename] {
			t.Fatalf("%s SHA-256 不匹配 manifest: got=%s want=%s", tdName, got, manifest.Files[basename])
		}
	}
}

func splitJSONL(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			line := trimSpace(data[start:i])
			if len(line) > 0 {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		line := trimSpace(data[start:])
		if len(line) > 0 {
			out = append(out, line)
		}
	}
	return out
}

func trimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && isSpace(b[i]) {
		i++
	}
	for j > i && isSpace(b[j-1]) {
		j--
	}
	return b[i:j]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}
