package gobridge

import (
	"strconv"
	"testing"
)

func TestFairSliceSessionList_CapsPerDirectoryHard(t *testing.T) {
	// dirA 50 条最新 + dirB 10 条更旧。纯 recency N=30 会几乎只剩 A。
	// 严格 K=5、N=30：A5+B5=10，**不回填**（避免 A 再吃光）。
	var sessions []map[string]interface{}
	for i := 50; i >= 1; i-- {
		sessions = append(sessions, map[string]interface{}{
			"id":              "a-" + strconv.Itoa(i),
			"directory":       "/proj/A",
			"updatedAtMillis": int64(1_000_000 + i),
		})
	}
	for i := 10; i >= 1; i-- {
		sessions = append(sessions, map[string]interface{}{
			"id":              "b-" + strconv.Itoa(i),
			"directory":       "/proj/B",
			"updatedAtMillis": int64(500_000 + i),
		})
	}

	fair := fairSliceSessionList(sessions, 5, 30)
	if len(fair) != 10 {
		t.Fatalf("fair len = %d, want 10 (A5+B5, no fill)", len(fair))
	}
	countA, countB := 0, 0
	for _, s := range fair {
		switch s["directory"] {
		case "/proj/A":
			countA++
		case "/proj/B":
			countB++
		}
	}
	if countA != 5 || countB != 5 {
		t.Fatalf("fair counts A=%d B=%d, want A=5 B=5 (hard K, no fill)", countA, countB)
	}
	if !fairHomeHasMore(sessions, fair, 5) {
		t.Fatal("hasMore should be true when full has more than fair")
	}
}

func TestFairSliceSessionList_SingleProjectCappedAtK(t *testing.T) {
	var sessions []map[string]interface{}
	for i := 40; i >= 1; i-- {
		sessions = append(sessions, map[string]interface{}{
			"id":              "only-" + strconv.Itoa(i),
			"directory":       "/only",
			"updatedAtMillis": int64(i),
		})
	}
	fair := fairSliceSessionList(sessions, 5, 30)
	// 单项目也只给 K=5（对齐 OpenCode 首页），深度靠 directory 分页。
	if len(fair) != 5 {
		t.Fatalf("single-project fair len = %d, want 5 (hard K)", len(fair))
	}
}

func TestPackageFairHomePage_NoNextCursor(t *testing.T) {
	sessions := []map[string]interface{}{
		{"id": "1", "directory": "/a", "updatedAtMillis": int64(2)},
		{"id": "2", "directory": "/a", "updatedAtMillis": int64(1)},
	}
	result := packageFairHomePage(sessions, 20, 100)
	if _, ok := result["nextCursor"]; ok {
		t.Fatalf("fair home must not emit nextCursor, got %#v", result["nextCursor"])
	}
	if result["hasMore"] != false {
		t.Fatalf("hasMore = %#v, want false when all fit", result["hasMore"])
	}
}

func TestPackageFairHomePage_DirectoryTotals(t *testing.T) {
	var sessions []map[string]interface{}
	for i := 0; i < 20; i++ {
		sessions = append(sessions, map[string]interface{}{
			"id": "a-" + strconv.Itoa(i), "directory": "/proj/A", "updatedAtMillis": int64(1000 - i),
		})
	}
	for i := 0; i < 9; i++ {
		sessions = append(sessions, map[string]interface{}{
			"id": "b-" + strconv.Itoa(i), "directory": "/proj/B", "updatedAtMillis": int64(500 - i),
		})
	}
	result := packageFairHomePage(sessions, 5, 150)
	totals, ok := result["directoryTotals"].(map[string]int)
	if !ok {
		t.Fatalf("directoryTotals type = %T, want map[string]int", result["directoryTotals"])
	}
	if totals["/proj/A"] != 20 || totals["/proj/B"] != 9 {
		t.Fatalf("totals = %#v, want A=20 B=9", totals)
	}
	// fair 切片每目录最多 5，但 totals 必须是 full 数（供 iOS 查看更多(N)）。
	fair, _ := result["sessions"].([]map[string]interface{})
	if len(fair) != 10 {
		t.Fatalf("fair len = %d, want 10", len(fair))
	}
}
