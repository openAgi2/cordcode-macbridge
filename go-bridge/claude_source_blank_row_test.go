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
		if i == 0 {
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

// T2（负例，守护）：非空 assistant/tool_result、未知块、空 content 数组 → 分类
// 「不支持」，照旧拒收且游标不动（不许因分类器存在而推进账本）。
// 诊断断言（v2.1 §5.2）：拒收错误必须自带行形态（rowClass / 块类型及空白 /
// stop / type）——relay 的拒收 warn 打印该错误，即附行形态且不含正文。
// 先红：现状错误只有 "no projection subevents"，无形态。
func TestUnsupportedZeroEventRowsStayRejectedCursorUnmoved(t *testing.T) {
	data := readFileOrFatal(t, "unsupported-zero-event.jsonl")
	correlation := claudeSourceCorrelation{
		SegmentStableKey: "fixture-segment", SegmentGeneration: "fixture-generation",
	}
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
	rejected := 0
	var rejectErrs []string
	var cursorAfterFirst int64
	for index, record := range scan.Records {
		if !record.Admitted {
			continue
		}
		if index == 0 {
			// user row 先被接受，建立可投影基线与游标
			current, _ := kernel.ClaudeSourceStateSnapshot("claude", "unsupported")
			batch, err := buildClaudeSourceRecordBatch(current, record, "claude", "unsupported", "epoch", correlation, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := kernel.ApplyClaudeSourceRecordBatch(batch); err != nil {
				t.Fatal(err)
			}
			snapshot, _ := kernel.ClaudeSourceStateSnapshot("claude", "unsupported")
			cursorAfterFirst = snapshot.CursorVector[0].RawByteEnd
			continue
		}
		current, _ := kernel.ClaudeSourceStateSnapshot("claude", "unsupported")
		batch, err := buildClaudeSourceRecordBatch(current, record, "claude", "unsupported", "epoch", correlation, "", nil)
		if err != nil {
			// build 失败（如空 content）也算「不支持」拒收形态，不推进
			rejected++
			rejectErrs = append(rejectErrs, err.Error())
			continue
		}
		result, err := kernel.ApplyClaudeSourceRecordBatch(batch)
		if err != nil || result.Status == ClaudeSourceBatchRejected {
			rejected++
			if err != nil {
				rejectErrs = append(rejectErrs, err.Error())
			}
			continue
		}
		t.Fatalf("不支持形态（record %d）不得被接受：result=%+v err=%v", index, result, err)
	}
	if rejected != 3 {
		t.Fatalf("拒收数 = %d, want 3（非空 tool_result / 未知块 / 空 content 三个不支持形态）", rejected)
	}
	snapshot, _ := kernel.ClaudeSourceStateSnapshot("claude", "unsupported")
	if snapshot.CursorVector[0].RawByteEnd != cursorAfterFirst {
		t.Fatalf("拒收后游标移动：%d → %d（不允许推进）", cursorAfterFirst, snapshot.CursorVector[0].RawByteEnd)
	}
	// 诊断断言：至少一条拒收错误自带行形态（rowClass 与块形态；relay warn 打印
	// 该错误即携带形态），且不含正文。
	joined := strings.Join(rejectErrs, "\n")
	if !strings.Contains(joined, "rowClass") {
		t.Fatalf("拒收错误缺行形态诊断（rowClass）：%s", joined)
	}
	if strings.Contains(joined, "占位结果") {
		t.Fatalf("诊断不得记录正文：%s", joined)
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
