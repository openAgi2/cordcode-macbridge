package gobridge

import "testing"

// 修复回归（2026-10-20 owner iPhone16 真机发现）：48 turns / 22MB 的大 turn 会话
// 在 itemsView=summary 下曾只返回 11 行——v2 复用 v1 切片的 full 4MiB 字节预算，
// 大 turn 只装得下尾部 11 个。修复后 summary 形态按摘要等价尺寸切窗（100 turns
// ≈150KB），窗口由 turn 上限决定。
func TestSummaryViewWindowNotTruncatedByFullByteBudget(t *testing.T) {
	// 构造 48 个 ~470KB 的大 turn（合计 ≈22MB，远超 4MiB full 预算）。
	turns := make([]TurnProjection, 0, 48)
	for i := 0; i < 48; i++ {
		big := make([]byte, 470*1024)
		for j := range big {
			big[j] = byte('a' + j%26)
		}
		turns = append(turns, TurnProjection{
			TurnID:    turnTestID(i),
			Status:    "completed",
			StartedAt: int64(i), CompletedAt: int64(i + 1),
			User:      &MessageProjection{ID: "u", Role: "user", Parts: []ProjectionPart{{Type: "text", Text: string(big)}}},
			Assistant: &MessageProjection{ID: "a", Role: "assistant", Parts: []ProjectionPart{{Type: "text", Text: string(big[:1024])}}},
		})
	}
	proj := SessionProjection{SessionID: "s-big", SyncRev: 100, Turns: turns}
	params := GetSessionProjectionWindowParams{BackendID: "claude", Direction: "window_0"}

	// full 形态：4MiB 门 → 只装尾部若干个（行为不变，锁 baseline）。
	fullResp, err := sliceProjectionWindowWithUpstream("claude", "s-big", "ep", proj, params, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fullResp.Turns) >= 48 {
		t.Fatalf("full 形态应受 4MiB 门约束（got %d turns）", len(fullResp.Turns))
	}

	// summary 形态：摘要等价尺寸 → 默认 100 turns 上限 → 全部 48 个都在窗口内。
	summarySizes := make([]int, len(proj.Turns))
	for i := range summarySizes {
		summarySizes[i] = 1536
	}
	summaryResp, err := sliceProjectionWindowWithBudget("claude", "s-big", "ep", proj, params, false, summarySizes)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaryResp.Turns) != 48 {
		t.Fatalf("summary 形态窗口应含全部 48 turns（got %d）——大 turn 会话不得被 full 字节预算截断", len(summaryResp.Turns))
	}
	if summaryResp.Window.HasOlder {
		t.Fatal("48 < 100 上限时 hasOlder 应为 false（无截断提示）")
	}
}

func turnTestID(i int) string {
	return "turn-" + string(rune('0'+i/10)) + string(rune('0'+i%10))
}
