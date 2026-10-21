package gobridge

// claude_source_blank_row_test.go — 修复方案 v2.1（docs/2026-10-21-claude-live-relay-
// zero-event-and-api-error-poison-fix-plan.md，8490a797，复审 R2 APPROVED）§7 T1–T3。
//
// RC-A：只含空白 thinking/text 的 assistant 行（K1/K2 白名单）映射出 0 个事件，
// ApplyClaudeSourceRecordBatch 在 len(Events)==0 时整行拒收（projection_source_
// transaction.go:133）→ 级联 source batch gap → 失败冷却。修复 = 三分类
// classifyClaudeSourceRowContent：「已证明无展示」走 AcceptedSourceOnly（游标前进、
// 账本照记、reducer 不变）；「不支持」照旧拒收；终态空行（T3 守护）仍可投影。
//
// 这些用例先于实现编写（先红：行为红——经 buildClaudeSourceRecordBatch →
// ApplyClaudeSourceRecordBatch 生产入口断言结果，不依赖新符号）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFileOrFatal(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(claudeSourceShapesDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// T1：白名单行（K1/K2 四形态）→ AcceptedSourceOnly；游标到 ByteEnd；紧随其后的
// tool_use 连续被接受（分类器使账本连续性保持）。
func TestBlankDisplayWhitelistAcceptsSourceOnlyAndKeepsContinuity(t *testing.T) {
	kernel, results := applyClaudeSourceFixture(t, "blank-display-blocks.jsonl")
	if len(results) != 7 {
		t.Fatalf("admitted rows = %d (want 7: user + blank + tool_use + tool_result + 3 blank)", len(results))
	}
	// 行序（真实 Claude 顺序）：user(0) blank-thinking(1) tool_use(2) tool_result(3)
	// blank-thinking-null(4) blank-text-tooluse(5) blank-text-null(6)。
	for _, i := range []int{0, 2, 3} {
		if results[i].Status != ClaudeSourceBatchAcceptedProjection {
			t.Fatalf("row %d status = %q, want accepted_projection", i, results[i].Status)
		}
	}
	for i := 1; i <= 6; i++ {
		if i == 2 || i == 3 {
			continue
		}
		if results[i].Status != ClaudeSourceBatchAcceptedSourceOnly {
			t.Fatalf("blank row %d status = %q, want accepted_source_only（RC-A 白名单）", i, results[i].Status)
		}
	}
	// tool_use(2) 紧随空白行(1)：连续被接受（拒收的行会让后续 gap）。
	if results[2].Status != ClaudeSourceBatchAcceptedProjection {
		t.Fatalf("tool_use row after blank status = %q（连续性破坏）", results[2].Status)
	}
	// reducer 只见到 user 提问与 tool_use，没有空白内容
	projection, ok := kernel.reducer.Snapshot("claude", "blank-display-blocks.jsonl")
	if !ok {
		t.Fatal("projection missing")
	}
	if len(projection.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(projection.Turns))
	}
	blank := 0
	for _, part := range projection.Turns[0].Assistant.Parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) == "" {
			blank++
		}
	}
	if blank != 0 {
		t.Fatalf("空白内容不得进入投影（blank parts=%d）", blank)
	}
}

// T2（R4-2 重构）：三类不支持形态（非空 assistant/tool_result、未知块、空
// content）**逐例独立**到达分类器——每个子例用全新 kernel 先接受 user 基线行，
// 再单独提交负例行（R4 指出原版三个负例连排，第②③个被 gap 拒收掩盖，没测到
// 分类判定）。逐条断言：rejected、错误自带 rowClass=unsupported 与对应块形态、
// 游标停在基线行尾（不许因分类器存在而推进账本）。
func TestUnsupportedZeroEventRowsStayRejectedCursorUnmoved(t *testing.T) {
	data := readFileOrFatal(t, "unsupported-zero-event.jsonl")
	correlation := claudeSourceCorrelation{
		SegmentStableKey: "fixture-segment", SegmentGeneration: "fixture-generation",
	}
	negatives := []struct {
		name      string
		recordIdx int // fixture 扫描记录序（0=user 基线，1..3=负例）
		wantShape string
	}{
		{"assistant-nonblank-tool-result", 1, "blocks=[tool_result]"},
		{"unknown-block-redacted-thinking", 2, "blocks=[redacted_thinking]"},
		{"empty-content-array", 3, "blocks=[]"},
	}
	for _, neg := range negatives {
		t.Run(neg.name, func(t *testing.T) {
			state := ClaudeSourceState{
				SchemaVersion: ClaudeSourceStateSchemaVersion, SourceGeneration: "fixture-source-generation",
				CursorVector: []ClaudeSourceCursor{{
					SegmentStableKey: correlation.SegmentStableKey, SegmentGeneration: correlation.SegmentGeneration,
					MembershipDigest: "fixture-membership",
				}},
				GraphNodes: map[string][]ClaudeGraphOccurrence{}, LogicalRecords: map[string]ClaudeLogicalRecord{},
			}
			kernel := NewProjectionKernel(NewProjectionReducer(), nil)
			if err := kernel.InstallClaudeSourceState("claude", "unsupported", state); err != nil {
				t.Fatal(err)
			}
			scan, err := scanCompleteClaudeRelayEntriesFromReader(strings.NewReader(data), 0, &claudeRelayScanState{})
			if err != nil || scan.Poison != nil {
				t.Fatalf("scan: poison=%+v err=%v", scan.Poison, err)
			}
			// 基线：接受 user 行（记录 0），游标推进到其 ByteEnd。
			current, _ := kernel.ClaudeSourceStateSnapshot("claude", "unsupported")
			baseline, err := buildClaudeSourceRecordBatch(current, scan.Records[0], "claude", "unsupported", "epoch", correlation, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := kernel.ApplyClaudeSourceRecordBatch(baseline); err != nil {
				t.Fatal(err)
			}
			snapshot, _ := kernel.ClaudeSourceStateSnapshot("claude", "unsupported")
			cursorAfterBaseline := snapshot.CursorVector[0].RawByteEnd
			// 负例行：重映射字节区间为紧跟基线行尾（R4：fixture 里第②③个负例
			// 的原始字节起点在基线行尾之后，会先撞 gap 拒收、走不到分类器——
			// 平移到连续位置使每个负例真正到达分类判定）。state 用 Apply 后的
			// 最新快照（GraphNodes 已含基线行，parent 链可解析）。
			negativeRecord := scan.Records[neg.recordIdx]
			negativeRecord.ByteStart = cursorAfterBaseline
			negativeRecord.ByteEnd = cursorAfterBaseline + (scan.Records[neg.recordIdx].ByteEnd - scan.Records[neg.recordIdx].ByteStart)
			currentAfter, _ := kernel.ClaudeSourceStateSnapshot("claude", "unsupported")
			negative, err := buildClaudeSourceRecordBatch(currentAfter, negativeRecord, "claude", "unsupported", "epoch", correlation, "", nil)
			if err == nil {
				result, applyErr := kernel.ApplyClaudeSourceRecordBatch(negative)
				if applyErr == nil && result.Status != ClaudeSourceBatchRejected {
					t.Fatalf("不支持形态不得被接受：result=%+v", result)
				}
				if applyErr == nil {
					t.Fatal("拒收必须返回错误")
				}
				errText := applyErr.Error()
				if !strings.Contains(errText, "rowClass=unsupported") {
					t.Fatalf("拒收错误缺 rowClass=unsupported：%s", errText)
				}
				if !strings.Contains(errText, neg.wantShape) {
					t.Fatalf("拒收错误缺块形态 %s：%s", neg.wantShape, errText)
				}
				if strings.Contains(errText, "占位") {
					t.Fatalf("诊断不得记录正文：%s", errText)
				}
			} else {
				// build 阶段失败（如结构性缺失）也要带形态。
				if !strings.Contains(err.Error(), "rowClass=unsupported") {
					t.Fatalf("build 失败错误缺形态：%v", err)
				}
			}
			snapshot, _ = kernel.ClaudeSourceStateSnapshot("claude", "unsupported")
			if snapshot.CursorVector[0].RawByteEnd != cursorAfterBaseline {
				t.Fatalf("拒收后游标移动：%d → %d（不允许推进）", cursorAfterBaseline, snapshot.CursorVector[0].RawByteEnd)
			}
		})
	}
}

// R4-2：分类器表驱动单测——四种白名单形态、未知块、空 content、混合块、终态
// 空行、API error 行、user 行全覆盖。变异验收：把未知块/空 content 改判白名单
// 时本表必须变红。
func TestClassifyClaudeSourceRowContent(t *testing.T) {
	line := func(t *testing.T, raw string) claudeTranscriptRelayEntry {
		t.Helper()
		var entry claudeTranscriptRelayEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			t.Fatal(err)
		}
		return entry
	}
	cases := []struct {
		name string
		raw  string
		want claudeSourceRowContentClass
	}{
		// 白名单四形态（K1/K2）
		{"k1-blank-thinking-stop-tooluse", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":" ","signature":"sig占位"}],"stop_reason":"tool_use"}}`, claudeRowContentProvenNoDisplay},
		{"k2-blank-thinking-stop-null-nosig", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":""}],"stop_reason":null}}`, claudeRowContentProvenNoDisplay},
		{"k1-blank-text-stop-tooluse", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":" "}],"stop_reason":"tool_use"}}`, claudeRowContentProvenNoDisplay},
		{"k2-blank-text-stop-null", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":""}],"stop_reason":null}}`, claudeRowContentProvenNoDisplay},
		// 可投影
		{"nonblank-thinking", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"有内容"}],"stop_reason":null}}`, claudeRowContentProjectable},
		{"nonblank-text", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"回复"}],"stop_reason":null}}`, claudeRowContentProjectable},
		{"tool-use", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}],"stop_reason":"tool_use"}}`, claudeRowContentProjectable},
		{"mixed-blank-text-plus-tool-use", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":""},{"type":"tool_use","id":"t1","name":"Read","input":{}}],"stop_reason":"tool_use"}}`, claudeRowContentProjectable},
		{"terminal-blank-thinking", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":" "}],"stop_reason":"end_turn"}}`, claudeRowContentProjectable},
		{"api-error-row", `{"type":"assistant","isApiErrorMessage":true,"message":{"role":"assistant","content":[],"stop_reason":null}}`, claudeRowContentProjectable},
		{"user-tool-result", `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"结果"}]}}`, claudeRowContentProjectable},
		{"user-text", `{"type":"user","message":{"role":"user","content":"提问"}}`, claudeRowContentProjectable},
		// 不支持
		{"unknown-block-redacted-thinking", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"redacted_thinking","data":"占位"}],"stop_reason":null}}`, claudeRowContentUnsupported},
		{"empty-content-array", `{"type":"assistant","message":{"role":"assistant","content":[],"stop_reason":null}}`, claudeRowContentUnsupported},
		{"assistant-nonblank-tool-result", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_result","tool_use_id":"t1","content":"结果"}],"stop_reason":"tool_use"}}`, claudeRowContentUnsupported},
		{"user-zero-event-blank-text", `{"type":"user","message":{"role":"user","content":[{"type":"text","text":""}]}}`, claudeRowContentUnsupported},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyClaudeSourceRowContent(line(t, c.raw)); got != c.want {
				t.Fatalf("classify = %s, want %s", got, c.want)
			}
		})
	}
}

// T3（守护）：终态（stop=end_turn）空白行仍走投影路径——turn_completed 必须产生。
func TestTerminalBlankRowStillProjectsTurnCompleted(t *testing.T) {
	kernel, results := applyClaudeSourceFixture(t, "blank-display-terminal.jsonl")
	if len(results) != 2 {
		t.Fatalf("admitted rows = %d (want 2)", len(results))
	}
	if results[0].Status != ClaudeSourceBatchAcceptedProjection {
		t.Fatalf("user row status = %q", results[0].Status)
	}
	if results[1].Status != ClaudeSourceBatchAcceptedProjection {
		t.Fatalf("终态空白行 status = %q, want accepted_projection（T3 守护：终态仍发 turn_completed）", results[1].Status)
	}
	projection, ok := kernel.reducer.Snapshot("claude", "blank-display-terminal.jsonl")
	if !ok {
		t.Fatal("projection missing")
	}
	if len(projection.Turns) != 1 || projection.Turns[0].Status != "completed" {
		if len(projection.Turns) == 0 {
			t.Fatal("终态空白行使回合未完成（turn_completed 丢失）")
		}
		t.Fatalf("turn status = %q, want completed", projection.Turns[0].Status)
	}
}
