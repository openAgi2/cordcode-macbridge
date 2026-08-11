package gobridge

// projection_payload_benchmark_test.go — Phase 1 自主源头基线(纯测量,零 production 改动)。
//
// 目的:为 remote-web relay 长消息加载取证基线(docs/2026-07-31-remote-web-relay-session-loading-v3.md
// §5.2「基线须实测」)提供 owner-动作之外的**自主源头侧**实测:一个真实大 SessionProjection 经生产路径
// (ProjectionReducer.Restore → Snapshot 深拷贝 → json.Marshal → 同包真实 gzipPayload)后的字节与耗时,
// 以及按字段类别(text / toolResult / toolInput / diff / matches / subagentBlocks)的 leave-one-out 归因。
//
// 护栏合规:本文件只读生产类型与函数,不修改任何 production 逻辑,不触碰 timeline writer / hydrate
// 事务域 / readiness。它只回答「全量 payload 多大、肥在哪」,不实施任何 timeline 拆分或 limitTurns。
//
// 运行:
//   go test ./go-bridge/... -run TestProjectionPayloadBaseline -count=1 -v   # 基线 + 灵敏度 + 产物
//   go test ./go-bridge/... -run TestProjectionPayloadBaseline -bench=. -count=1 -benchmem  # 加 ns/op
//
// 产物:/tmp/cordcode-projection-payload-baseline.json

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// ---- 确定性的真实尺寸内容生成器(可压缩性近似真实文本/ diff / 命令输出,非随机) ----

func ppbFillText(n int) string {
	sentences := []string{
		"The assistant reviewed the changes and applied a fix to the renderer module before re-running the targeted tests. ",
		"After the patch the projection snapshot fence holds and the timeline no longer flickers on cold open. ",
		"Cross-repo protocol changes require updating the canonical pack first then mirroring to the iOS and web types. ",
		"The reducer deep-copies the projection on Snapshot so later reduce activity cannot mutate the served payload. ",
		"Hydrate and live share one Kernel; cold hydrate goes through private ingest inside a single source cut. ",
	}
	var b strings.Builder
	i := 0
	for b.Len() < n {
		b.WriteString(sentences[i%len(sentences)])
		i++
	}
	return b.String()[:n]
}

func ppbFillDiff(n int) string {
	var b strings.Builder
	i := 0
	for b.Len() < n {
		if i%2 == 0 {
			fmt.Fprintf(&b, "+\tline %d: clone projection before mutating parts in the reducer\n", i)
		} else {
			fmt.Fprintf(&b, "-\tline %d: old path mutated the served projection in place\n", i)
		}
		i++
	}
	return b.String()[:n]
}

func ppbFillToolResult(n int) string {
	var b strings.Builder
	i := 0
	for b.Len() < n {
		fmt.Fprintf(&b, "PASS: test_case_%d (%dms)\n", i, 10+i%80)
		i++
	}
	return b.String()[:n]
}

// ppbBuildLargeProjection 合成一个真实尺寸的大 SessionProjection(coding session 形态)。
// 每个 turn:user(短文本)+ assistant(长文本 + Bash 工具 + file edit diff),每 3 个 turn 加 Grep
// matches,每 5 个 turn 加一个 sync 子代理块(递归 SubagentBlocks)。
func ppbBuildLargeProjection(turnCount int) SessionProjection {
	turns := make([]TurnProjection, 0, turnCount)
	for i := 0; i < turnCount; i++ {
		turnID := fmt.Sprintf("turn-%d", i)
		user := &MessageProjection{
			ID:    fmt.Sprintf("msg-user-%d", i),
			Role:  "user",
			Parts: []ProjectionPart{{Type: "text", Text: ppbFillText(200), Presentation: "final"}},
		}
		parts := []ProjectionPart{
			{Type: "text", Text: ppbFillText(2048), Presentation: "final"},
			{
				Type:       "tool",
				ItemID:     fmt.Sprintf("call-%d-bash", i),
				ToolName:   "Bash",
				ToolInput:  map[string]interface{}{"command": ppbFillText(512), "workdir": "/repo"},
				ToolResult: ppbFillToolResult(10240),
				ToolStatus: "completed",
			},
			{
				Type: "file",
				Path: fmt.Sprintf("src/module_%d.go", i),
				Kind: "edit",
				Diff: ppbFillDiff(5120),
			},
		}
		if i%3 == 0 {
			matches := make([]interface{}, 0, 10)
			for j := 0; j < 10; j++ {
				matches = append(matches, map[string]interface{}{
					"path":     fmt.Sprintf("src/mod_%d_%d.go", i, j),
					"line":     j + 1,
					"snippet":  ppbFillText(120),
					"priority": j % 3,
				})
			}
			parts = append(parts, ProjectionPart{
				Type:       "tool",
				ItemID:     fmt.Sprintf("call-%d-grep", i),
				ToolName:   "Grep",
				ToolInput:  map[string]interface{}{"pattern": "Snapshot", "glob": "*.go"},
				Matches:    matches,
				ToolStatus: "completed",
			})
		}
		if i%5 == 0 {
			parts = append(parts, ProjectionPart{
				Type:           "subagent",
				AgentID:        fmt.Sprintf("agent-%d", i),
				SpawnToolUseID: fmt.Sprintf("call-%d-bash", i),
				SubagentType:   "sync",
				SubagentStatus: "completed",
				SubagentBlocks: []ProjectionPart{
					{Type: "text", Text: ppbFillText(2048), Presentation: "final"},
					{Type: "reasoning", Text: ppbFillText(1024)},
				},
			})
		}
		assistant := &MessageProjection{
			ID:    fmt.Sprintf("msg-asst-%d", i),
			Role:  "assistant",
			Parts: parts,
		}
		turns = append(turns, TurnProjection{
			TurnID:      turnID,
			Status:      "completed",
			StartedAt:   int64(i) * 1000,
			CompletedAt: int64(i)*1000 + 500,
			User:        user,
			Assistant:   assistant,
		})
	}
	return SessionProjection{
		SessionID:   "s1",
		SyncRev:     turnCount * 4,
		BridgeEpoch: "epoch-bench",
		UpdatedAt:   int64(turnCount) * 1000,
		Execution:   ExecutionView{Phase: "idle"},
		Turns:       turns,
	}
}

// ---- 测量辅助 ----

func ppbMedianDuration(runs int, fn func()) time.Duration {
	if runs < 1 {
		runs = 1
	}
	// 预热 1 次,排除首次分配/jit 化影响。
	fn()
	ds := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		t0 := time.Now()
		fn()
		ds = append(ds, time.Since(t0))
	}
	sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
	return ds[len(ds)/2]
}

func ppbCountPartsDeep(parts []ProjectionPart) int {
	n := len(parts)
	for _, p := range parts {
		if len(p.SubagentBlocks) > 0 {
			n += ppbCountPartsDeep(p.SubagentBlocks)
		}
	}
	return n
}

func ppbCountAllParts(proj SessionProjection) int {
	n := 0
	for _, t := range proj.Turns {
		for _, msg := range []*MessageProjection{t.User, t.Assistant, t.System} {
			if msg != nil {
				n += ppbCountPartsDeep(msg.Parts)
			}
		}
	}
	return n
}

// ppbZeroCategory 在深拷贝上把指定类别清空(omitempty 使其从 wire 消失),用于 leave-one-out 字节归因。
func ppbZeroCategory(proj SessionProjection, category string) SessionProjection {
	out := cloneSessionProjection(proj)
	for i := range out.Turns {
		t := &out.Turns[i]
		for _, msg := range []*MessageProjection{t.User, t.Assistant, t.System} {
			if msg != nil {
				ppbZeroCategoryInParts(msg.Parts, category)
			}
		}
	}
	return out
}

func ppbZeroCategoryInParts(parts []ProjectionPart, category string) {
	for i := range parts {
		switch category {
		case "text":
			parts[i].Text = ""
		case "toolResult":
			parts[i].ToolResult = nil
		case "toolInput":
			parts[i].ToolInput = nil
		case "diff":
			parts[i].Diff = ""
		case "matches":
			parts[i].Matches = nil
		case "subagentBlocks":
			parts[i].SubagentBlocks = nil
		}
		if len(parts[i].SubagentBlocks) > 0 {
			ppbZeroCategoryInParts(parts[i].SubagentBlocks, category)
		}
	}
}

// ppbTruncateToolResults 深拷贝并把每个 ToolResult 字符串截短到 maxBytes,模拟一次真实「瘦身」。
func ppbTruncateToolResults(proj SessionProjection, maxBytes int) SessionProjection {
	out := cloneSessionProjection(proj)
	for i := range out.Turns {
		t := &out.Turns[i]
		for _, msg := range []*MessageProjection{t.User, t.Assistant, t.System} {
			if msg != nil {
				ppbTruncateToolResultsInParts(msg.Parts, maxBytes)
			}
		}
	}
	return out
}

func ppbTruncateToolResultsInParts(parts []ProjectionPart, maxBytes int) {
	for i := range parts {
		if s, ok := parts[i].ToolResult.(string); ok && len(s) > maxBytes {
			parts[i].ToolResult = s[:maxBytes]
		}
		if len(parts[i].SubagentBlocks) > 0 {
			ppbTruncateToolResultsInParts(parts[i].SubagentBlocks, maxBytes)
		}
	}
}

// ---- 主基线测试 ----

func TestProjectionPayloadBaseline(t *testing.T) {
	turnCounts := []int{25, 50, 100}
	categories := []string{"text", "toolResult", "toolInput", "diff", "matches", "subagentBlocks"}

	type sizePoint struct {
		Turns            int         `json:"turns"`
		PartsTotal       int         `json:"parts_total"`
		UncompressedBytes int        `json:"uncompressed_bytes"`
		CompressedBytes  int         `json:"compressed_bytes"`
		GzipRatio        float64     `json:"gzip_ratio"`
		CloneNsMedian    int64       `json:"clone_ns_median"`
		MarshalNsMedian  int64       `json:"marshal_ns_median"`
		GzipNsMedian     int64       `json:"gzip_ns_median"`
		CategoryBytes    map[string]int `json:"category_bytes"`
	}
	points := make([]sizePoint, 0, len(turnCounts))

	for _, tc := range turnCounts {
		reducer := NewProjectionReducer()
		reducer.Restore("bench", "s1", ppbBuildLargeProjection(tc))

		cloneNs := ppbMedianDuration(20, func() { _, _ = reducer.Snapshot("bench", "s1") })
		snapshot, ok := reducer.Snapshot("bench", "s1")
		if !ok {
			t.Fatalf("turns=%d: Snapshot not found after Restore", tc)
		}

		// 字节数是完全确定的:同一输入 → 同一 JSON → 同一字节。
		uncompressed, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatalf("turns=%d: marshal: %v", tc, err)
		}
		compressed, err := gzipPayload(uncompressed)
		if err != nil {
			t.Fatalf("turns=%d: gzipPayload: %v", tc, err)
		}
		marshalNs := ppbMedianDuration(20, func() { _, _ = json.Marshal(snapshot) })
		gzipNs := ppbMedianDuration(20, func() { _, _ = gzipPayload(uncompressed) })

		// 决定性校验:同 snapshot 二次 marshal 必须字节一致。
		uncompressed2, _ := json.Marshal(snapshot)
		if !bytes.Equal(uncompressed, uncompressed2) {
			t.Fatalf("turns=%d: non-deterministic marshal (byte mismatch on re-marshal)", tc)
		}

		catBytes := make(map[string]int, len(categories))
		for _, c := range categories {
			stripped, _ := json.Marshal(ppbZeroCategory(snapshot, c))
			catBytes[c] = len(uncompressed) - len(stripped)
		}

		ratio := 1.0
		if len(uncompressed) > 0 {
			ratio = float64(len(compressed)) / float64(len(uncompressed))
		}
		points = append(points, sizePoint{
			Turns:             tc,
			PartsTotal:        ppbCountAllParts(snapshot),
			UncompressedBytes: len(uncompressed),
			CompressedBytes:   len(compressed),
			GzipRatio:         ratio,
			CloneNsMedian:     int64(cloneNs),
			MarshalNsMedian:   int64(marshalNs),
			GzipNsMedian:      int64(gzipNs),
			CategoryBytes:     catBytes,
		})

		t.Logf("=== turns=%d  parts=%d ===", tc, ppbCountAllParts(snapshot))
		t.Logf("  uncompressed = %d bytes (%.2f MiB)", len(uncompressed), float64(len(uncompressed))/(1<<20))
		t.Logf("  compressed   = %d bytes (%.2f KiB)  gzip_ratio=%.3f", len(compressed), float64(len(compressed))/1024, ratio)
		t.Logf("  clone median = %s | marshal median = %s | gzip median = %s", cloneNs, marshalNs, gzipNs)
		t.Logf("  category bytes (leave-one-out):")
		for _, c := range categories {
			pct := 0.0
			if len(uncompressed) > 0 {
				pct = 100 * float64(catBytes[c]) / float64(len(uncompressed))
			}
			t.Logf("     %-16s %10d  (%5.1f%%)", c, catBytes[c], pct)
		}
	}

	// 灵敏度校验(证明这是有效的未来 perf 回归守卫):把 50-turn 的 ToolResult 截短到 200B,
	// 必须观察到 compressed 字节有 >=5% 的可检测下降。这不是 leave-one-out 的自洽性,而是
	// 「真实内容尺寸变化是否被基准反映」的独立校验。
	reducer50 := NewProjectionReducer()
	reducer50.Restore("bench", "s1", ppbBuildLargeProjection(50))
	snap50, ok := reducer50.Snapshot("bench", "s1")
	if !ok {
		t.Fatalf("sensitivity: Snapshot(50) not found")
	}
	full50, _ := json.Marshal(snap50)
	fullComp50, _ := gzipPayload(full50)
	reduced50, _ := json.Marshal(ppbTruncateToolResults(snap50, 200))
	reducedComp50, _ := gzipPayload(reduced50)
	if len(reducedComp50) >= len(fullComp50) {
		t.Errorf("sensitivity: truncating ToolResult did NOT reduce compressed bytes (full=%d reduced=%d) — benchmark would fail to detect a real payload reduction", len(fullComp50), len(reducedComp50))
	}
	dropRatio := 1 - float64(len(reducedComp50))/float64(len(fullComp50))
	if dropRatio < 0.05 {
		t.Errorf("sensitivity: compressed bytes dropped only %.2f%% after truncating ToolResult; benchmark insensitive to real reductions", dropRatio*100)
	}
	t.Logf("=== sensitivity (truncate ToolResult to 200B at 50 turns) ===")
	t.Logf("  full compressed=%d  reduced compressed=%d  drop=%.2f%%", len(fullComp50), len(reducedComp50), dropRatio*100)

	// 落盘 JSON 产物(供 P3 findings 文档引用真实数字)。
	artifact := map[string]interface{}{
		"plan":       "docs/2026-07-31-remote-web-relay-session-loading-v3.md",
		"phase":      "p1-autonomous-source-baseline",
		"note":       "源头侧合成 projection 基线;relay 传输侧(socket_send_ms/真实 RTT)仍需 owner §7 真机实测。",
		"size_points": points,
		"sensitivity": map[string]interface{}{
			"turns":              50,
			"full_compressed":    len(fullComp50),
			"reduced_compressed": len(reducedComp50),
			"drop_ratio":         dropRatio,
		},
	}
	artifactBytes, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	const artifactPath = "/tmp/cordcode-projection-payload-baseline.json"
	if err := os.WriteFile(artifactPath, artifactBytes, 0o644); err != nil {
		t.Fatalf("write artifact %s: %v", artifactPath, err)
	}
	t.Logf("baseline artifact written: %s", artifactPath)
}

// ---- ns/op 维度的 Benchmark(可选,-bench=. 时运行) ----

func BenchmarkProjectionSnapshotClone50(b *testing.B) {
	reducer := NewProjectionReducer()
	reducer.Restore("bench", "s1", ppbBuildLargeProjection(50))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = reducer.Snapshot("bench", "s1")
	}
}

func BenchmarkProjectionMarshal50(b *testing.B) {
	reducer := NewProjectionReducer()
	reducer.Restore("bench", "s1", ppbBuildLargeProjection(50))
	snap, _ := reducer.Snapshot("bench", "s1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(snap)
	}
}

func BenchmarkProjectionGzip50(b *testing.B) {
	reducer := NewProjectionReducer()
	reducer.Restore("bench", "s1", ppbBuildLargeProjection(50))
	snap, _ := reducer.Snapshot("bench", "s1")
	data, _ := json.Marshal(snap)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = gzipPayload(data)
	}
}
