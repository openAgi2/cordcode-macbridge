package gobridge

import (
	"fmt"
	"sync"
	"testing"
)

// 2026-10-19 验收登记的 followup（anchor stale 55ms「fence/快照不一致」假设）定向复现：
// hydrate CommitHydrateTransaction 与 Snapshot 并发时，快照是否可能给出
// 「SyncRev 新但 turns 缺行」的不一致视图。
// 结论判据：若多轮全部一致（rev 单调、turns 不缩水、武装 turn 在 commit 后
// 必在 turns 中）→ 竞态假设不成立，cursor_stale 属合法回退（anchor 真离开
// 保留区另有原因；观测日志 anchor-newer stale slice 已就位等待真实复发数据）。
func TestKernelSnapshotConsistentAcrossConcurrentCommit(t *testing.T) {
	h := NewHandlers()
	const sid = "s-race"
	// 建立 hydrate 事务并武装 60 个已完成 turn（fixture 事件形状按 reducer 契约）。
	source := ProjectionSourceDescriptor{Identity: sid, Path: "/tmp/" + sid + ".jsonl", Cursor: 1}
	if _, err := h.projectionKernel.BeginHydrateTransaction("claude", sid, source, false, false, false); err != nil {
		t.Fatalf("begin hydrate: %v", err)
	}
	for i := 0; i < 60; i++ {
		turnID := fmt.Sprintf("t-%02d", i)
		_ = h.projectionKernel.ApplyHydrateEvent("claude", sid, "test", "turn_started", map[string]interface{}{"turnId": turnID})
		_ = h.projectionKernel.ApplyHydrateEvent("claude", sid, "test", "user_message", map[string]interface{}{"turnId": turnID, "itemId": "u-" + turnID, "text": "q"})
		_ = h.projectionKernel.ApplyHydrateEvent("claude", sid, "test", "text_delta", map[string]interface{}{"turnId": turnID, "itemId": "a-" + turnID, "delta": "done"})
		_ = h.projectionKernel.ApplyHydrateEvent("claude", sid, "test", "turn_completed", map[string]interface{}{"turnId": turnID})
	}
	h.projectionKernel.MarkHydrateSourceIngestComplete("claude", sid)

	var wg sync.WaitGroup
	inconsistent := make(chan string, 8)
	stop := make(chan struct{})

	// 读者：高频快照，断言内部一致（rev 单调不减、turn 行数不缩水）。
	lastRev := -1
	lastTurnCount := -1
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			proj, ok := h.projectionKernel.Snapshot("claude", sid)
			if !ok {
				continue // Hydrating 相位期间合法缺席
			}
			if proj.SyncRev < lastRev {
				inconsistent <- fmt.Sprintf("rev regressed: %d < %d", proj.SyncRev, lastRev)
				return
			}
			if len(proj.Turns) < lastTurnCount {
				inconsistent <- fmt.Sprintf("turns shrank: %d < %d (rev %d)", len(proj.Turns), lastTurnCount, proj.SyncRev)
				return
			}
			lastRev = proj.SyncRev
			lastTurnCount = len(proj.Turns)
		}
	}()

	// 写者：合法 commit（全武装已终态 → gate 直接过）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := h.projectionKernel.CommitHydrateTransaction("claude", sid); err != nil {
			inconsistent <- fmt.Sprintf("commit failed: %v", err)
		}
		close(stop)
	}()

	wg.Wait()
	select {
	case msg := <-inconsistent:
		t.Fatalf("快照不一致复现（55ms 竞态假设成立）：%s", msg)
	default:
	}

	// commit 后终态核验：全部 60 个武装 turn 必须在 turns 中（含最早 t-00——
	// 事故形态「anchor=较早 turn 在 head 推进后消失」若为快照缺陷，此处必现）。
	final, ok := h.projectionKernel.Snapshot("claude", sid)
	if !ok {
		t.Fatal("commit 后快照必须可用")
	}
	seen := map[string]bool{}
	for _, turn := range final.Turns {
		seen[turn.TurnID] = true
	}
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("t-%02d", i)
		if !seen[id] {
			t.Fatalf("武装 turn %s 在 commit 后消失（turns=%d rev=%d）——anchor 丢失形态复现", id, len(final.Turns), final.SyncRev)
		}
	}
}
