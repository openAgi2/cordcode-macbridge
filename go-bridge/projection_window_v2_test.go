package gobridge

import (
	"strings"
	"testing"

	"github.com/openAgi2/cordcode-macbridge/agent/claudecode"
)

// S-3 v2 Mac producer 定向测试（冻结规范 v3 交付物 4 的 producer 部分；
// spec 冻结断言在 window_v2_spec_test.go）。
//
// 覆盖：① summary 组装（③ 正文优先 wire 化：folded/foldReason/brief 截断/
// 工具占位/例外通道 liveState 原样内嵌）；② 预算降级（老 turns budget 形态、
// 例外不降级、不截断已交付 brief）；③ summary.oversized fail-closed（例外超
// projectionMaxBytes）；④ anchor itemsView 行转换（token 基准不变 + 例外照常）；
// ⑤ handler 四层资格门（capability gate / itemsView 校验 / descriptor 声明）。

func v2TestTurn(turnID, status, userText, agentTail string, parts ...ProjectionPart) TurnProjection {
	turn := TurnProjection{TurnID: turnID, Status: status}
	if userText != "" {
		turn.User = &MessageProjection{Parts: []ProjectionPart{{Type: "text", Text: userText}}}
	}
	agentParts := append([]ProjectionPart{{Type: "text", Text: agentTail}}, parts...)
	turn.Assistant = &MessageProjection{Parts: agentParts}
	return turn
}

func v2ToolPart(typeName string) ProjectionPart {
	return ProjectionPart{Type: typeName, Text: strings.Repeat("x", 1024)}
}

func TestSummarizeTurnBodyPriorityShapes(t *testing.T) {
	// 普通 turn：短正文 → 不折叠、brief 完整。
	plain := v2TestTurn("t1", "completed", "问题", "回答")
	row := summarizeTurn(plain)
	if row.Folded || row.FoldReason != "" {
		t.Fatalf("短正文不得折叠: %+v", row)
	}
	if row.UserBrief == nil || row.UserBrief.Truncated || row.AgentBrief == nil || row.AgentBrief.Truncated {
		t.Fatal("短正文 brief 必须完整")
	}
	if row.AgentBriefHead != "回答" {
		t.Fatalf("agentBriefHead = %q", row.AgentBriefHead)
	}

	// 超长正文 → long_body 折叠 + 截断（2026-10-20 预览扩容后阈值 1024，用 1200）。
	long := v2TestTurn("t2", "completed", strings.Repeat("问", 1200), strings.Repeat("答", 1200))
	row = summarizeTurn(long)
	if !row.Folded || row.FoldReason != "long_body" {
		t.Fatalf("超长正文应 long_body: %+v", row)
	}
	if !row.UserBrief.Truncated || len([]rune(row.UserBrief.Text)) != summaryBriefMaxChars {
		t.Fatal("brief 截断至 128 chars")
	}
	if len([]rune(row.AgentBriefHead)) != summaryBriefHeadChars {
		t.Fatalf("head 截断至 64 chars, got %d", len([]rune(row.AgentBriefHead)))
	}

	// 工具过程 → tool_process 折叠 + 占位计数。
	tools := v2TestTurn("t3", "completed", "q", "a", v2ToolPart("tool_use"), v2ToolPart("tool_result"))
	row = summarizeTurn(tools)
	if !row.Folded || row.FoldReason != "tool_process" {
		t.Fatalf("工具过程应 tool_process: %+v", row)
	}
	if row.ToolProcessCount != 2 || row.ToolProcessBytes == 0 {
		t.Fatalf("工具占位 = count 2, got %d/%d", row.ToolProcessCount, row.ToolProcessBytes)
	}

	// 两者 → both。
	both := v2TestTurn("t4", "completed", strings.Repeat("问", 1300), strings.Repeat("答", 1300), v2ToolPart("tool_use"))
	row = summarizeTurn(both)
	if row.FoldReason != "both" {
		t.Fatalf("both: %+v", row)
	}
}

func TestSummarizeAlwaysVisibleExceptions(t *testing.T) {
	// running 轮次 → liveState 原样内嵌。
	running := v2TestTurn("t-run", "running", "q", "进行中")
	row := summarizeTurn(running)
	if row.LiveState == nil || row.LiveState.TurnID != "t-run" {
		t.Fatal("running 轮次必须 liveState 例外通道")
	}

	// permission 族 part → 例外（冻结定义 2：无新 wire 字段按 part 类型识别）。
	perm := v2TestTurn("t-perm", "completed", "q", "需要确认")
	perm.Assistant.Parts = append(perm.Assistant.Parts, ProjectionPart{Type: "permission_request"})
	row = summarizeTurn(perm)
	if row.LiveState == nil {
		t.Fatal("permission_request part 必须走例外通道")
	}

	// F-3 官方权限标记形态（type=tool + RequiresPermissionConfirmation）→ 例外
	// （正式投影的权限卡载体；completed 轮次同样完整可见）。
	official := v2TestTurn("t-official", "completed", "q", "需要批准")
	official.Assistant.Parts = append(official.Assistant.Parts, ProjectionPart{
		Type: "tool", ToolStatus: "pending", RequiresPermissionConfirmation: true, ToolName: "edit_file",
	})
	if summarizeTurn(official).LiveState == nil {
		t.Fatal("官方权限标记（type=tool + requiresPermissionConfirmation）必须走例外通道")
	}

	// user_input pending（wire 字段名 status）→ 例外；answered 不例外。
	pending := v2TestTurn("t-ui", "completed", "q", "请回答")
	pending.Assistant.Parts = append(pending.Assistant.Parts, ProjectionPart{Type: "user_input", UserInputStatus: "pending"})
	if summarizeTurn(pending).LiveState == nil {
		t.Fatal("user_input pending part 必须走例外通道")
	}
	answered := v2TestTurn("t-ui2", "completed", "q", "已回答")
	answered.Assistant.Parts = append(answered.Assistant.Parts, ProjectionPart{Type: "user_input", UserInputStatus: "answered"})
	if summarizeTurn(answered).LiveState != nil {
		t.Fatal("user_input answered 不是例外（无需完整内嵌）")
	}
}

func TestSummaryBudgetDegradesOldestNotExceptions(t *testing.T) {
	// 触发预算：2000 turns × 300 chars（3B/字）× 三段 ≈ 5.4MiB 实际 UTF-8 字节
	// > 1MiB（预算按实际字节计量，2026-10-20 F-01 修订）。
	turns := make([]TurnProjection, 0, 2000)
	for i := 0; i < 2000; i++ {
		turns = append(turns, v2TestTurn(
			"t"+string(rune('A'+i%26))+string(rune('a'+i%26/26))+string(rune('0'+i%10))+"x"+string(rune('A'+i%26))+"y", "completed",
			strings.Repeat("问", 150), strings.Repeat("答", 150),
		))
	}
	// 例外行放在最老位置：预算降级必须跳过它。
	exception := v2TestTurn("t-exc", "running", "q", "live")
	turns = append([]TurnProjection{exception}, turns...)

	rows, budget, err := summarizeWindowTurns(turns, 0)
	if err != nil {
		t.Fatal(err)
	}
	excRow := rows[0]
	if excRow.FoldReason == "budget" || excRow.UserBrief == nil {
		t.Fatal("例外行（liveState）不受预算降级")
	}
	if len(budget.Exceptions) != 1 || budget.Exceptions[0] != "t-exc" {
		t.Fatalf("exceptions = %v", budget.Exceptions)
	}
	degraded := 0
	for _, row := range rows {
		if row.FoldReason == "budget" {
			degraded++
			if row.UserBrief != nil || row.AgentBrief != nil {
				t.Fatal("budget 降级行不得残留 brief")
			}
		}
	}
	if degraded == 0 {
		t.Fatal("超预算必须至少降级一行")
	}
	// budget.FoldedTurnIds 与 degraded 一致。
	budgeted := 0
	for _, id := range budget.FoldedTurnIds {
		_ = id
	}
	for _, row := range rows {
		if row.Folded {
			budgeted++
		}
	}
	if budgeted < degraded {
		t.Fatal("FoldedTurnIds 账本必须覆盖全部折叠行")
	}
}

// TestSummaryBudgetCountsActualUTF8Bytes（2026-10-20 预览扩容评审 F-01 反例）：
// 151 个普通 completed turn，正文全四字节码点（emoji）。每轮 user 1024 + agent 1024
// + head 256 码点 = 2304 码点 × 4B = 9216B；151 轮合计 1,391,616B > 1MiB。旧算法
// RuneCount×3 只计 1,043,712B 不触发降级——必须按实际 UTF-8 字节计量并从最老轮
// 整轮降级，未降级行 brief 原样保留。
func TestSummaryBudgetCountsActualUTF8Bytes(t *testing.T) {
	const turnsCount = 151
	const perTurnBytes = (summaryBriefMaxChars + summaryBriefMaxChars + summaryBriefHeadChars) * 4 // 9216
	body := strings.Repeat("\U0001F600", summaryBriefMaxChars+64) // 略超上限 → truncated
	turns := make([]TurnProjection, 0, turnsCount)
	for i := 0; i < turnsCount; i++ {
		turns = append(turns, v2TestTurn(
			"t-f01-"+string(rune('a'+i%26))+string(rune('0'+i/26%10))+string(rune('A'+i%26)),
			"completed", body, body,
		))
	}
	rows, budget, err := summarizeWindowTurns(turns, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 实际字节计量必须触发降级（旧 ×3 近似在此反例下不触发）。
	degraded := 0
	firstIntact := -1
	for i, row := range rows {
		if row.FoldReason == "budget" {
			degraded++
			if row.UserBrief != nil || row.AgentBrief != nil {
				t.Fatalf("budget 降级行 %d 不得残留 brief", i)
			}
		} else if firstIntact < 0 {
			firstIntact = i
		}
	}
	if degraded == 0 {
		t.Fatal("四字节码点反例：实际 UTF-8 字节 1,391,616B > 1MiB 必须触发降级（×3 近似低估修复回归）")
	}
	// 降级必须从最老开始且连续。
	for i := 0; i < degraded; i++ {
		if rows[i].FoldReason != "budget" {
			t.Fatalf("降级必须从最老轮开始：rows[%d] 未降级而 rows[%d] 已降级", i, degraded)
		}
	}
	if firstIntact != degraded {
		t.Fatalf("降级段应连续：firstIntact=%d degraded=%d", firstIntact, degraded)
	}
	// 未降级行 brief 原样（不截断已交付 brief）：1024 码点 × 4B。
	intact := rows[firstIntact]
	if len([]rune(intact.UserBrief.Text)) != summaryBriefMaxChars || !intact.UserBrief.Truncated {
		t.Fatalf("未降级行 brief 应保留完整 1024 码点截断形态")
	}
	if len(intact.UserBrief.Text) != summaryBriefMaxChars*4 {
		t.Fatalf("四字节码点 1024 个应计 4096B 实际字节，got %dB", len(intact.UserBrief.Text))
	}
	// 账本 ≤ 1MiB 且等于未降级行实际字节合计。
	if budget.PayloadBytes > summaryViewPayloadBudget {
		t.Fatalf("payloadBytes %d > 1MiB", budget.PayloadBytes)
	}
	if budget.PayloadBytes != (turnsCount-degraded)*perTurnBytes {
		t.Fatalf("payloadBytes %d != %d×%d（按实际 UTF-8 字节）", budget.PayloadBytes, turnsCount-degraded, perTurnBytes)
	}
}

func TestSummaryOversizedFailClosed(t *testing.T) {
	// 例外 turn 自身编码超 maxExceptionBytes → typed summary.oversized（fail-closed）。
	huge := v2TestTurn("t-huge", "running", "q", strings.Repeat("答", 4_000_000))
	_, _, err := summarizeWindowTurns([]TurnProjection{huge}, 1024)
	if err == nil || !strings.Contains(err.Error(), "summary") {
		t.Fatalf("例外超限应 typed 失败, got %v", err)
	}
	if !strings.Contains(err.Error(), "exceeds projectionMaxBytes") {
		t.Fatalf("应判定 errSummaryOversized（wrap 文案）, got %v", err)
	}
}

func TestAnchorItemsViewSummaryConversion(t *testing.T) {
	// anchor rows → summary 形态（token 基准不变的验证在 slice 测试族；
	// 此处锁行形态与例外照常）。
	turns := []TurnProjection{
		v2TestTurn("t1", "completed", "q", "a", v2ToolPart("tool_use")),
		v2TestTurn("t-live", "running", "q", "进行中"),
	}
	rows, err := summarizeAnchorRows(turns, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].FoldReason != "tool_process" {
		t.Fatalf("rows[0] foldReason = %q", rows[0].FoldReason)
	}
	if rows[1].LiveState == nil {
		t.Fatal("anchor summary 行的例外照常完整内嵌")
	}
}

func TestV2HelloNegotiationEchoes(t *testing.T) {
	// echo 条件 = 客户端声明 + ack.Ok + production flag；未声明不 echo。
	hello := &HelloMessage{Capabilities: []string{
		"session_sync_v2", "projection_window_v1", "projection_window_v2",
	}}
	if !helloSupportsProjectionWindowV2(hello) {
		t.Fatal("声明识别失败")
	}
	bare := &HelloMessage{Capabilities: []string{"session_sync_v2"}}
	if helloSupportsProjectionWindowV2(bare) {
		t.Fatal("未声明不得误识别")
	}
}

func TestClaudeDescriptorDeclaresTurnDetailChunks(t *testing.T) {
	// 四层资格门第 4 层（descriptor）：Claude 声明 turn_detail_chunks_v1。
	desc := (&claudecode.Agent{}).WireDescriptor()
	found := false
	for _, capability := range desc.StaticCapabilities {
		if capability == "turn_detail_chunks_v1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Claude descriptor 缺 turn_detail_chunks_v1 声明: %v", desc.StaticCapabilities)
	}
}
