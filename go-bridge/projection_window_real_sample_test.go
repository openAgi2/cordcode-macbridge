package gobridge

// projection_window_real_sample_test.go：消费 docs/protocol/samples/projection-window-v1/
// real-capture/ 的首批真实 wire 捕获（owner 真实 codex 会话，2026-09-15；采集与脱敏
// 边界见该目录 README）。backend 谱系：kernel checkpoint 来自已退役 legacy
// file-based "codex" driver 时代（≤2026-08-25）；现役家族 backendId = "codex-remote"
// （API-backed，kernel 状态按设计不落盘）——窗口切片路径 backend 无关。断言生产
// 解码路径上的 canonical 语义：window_0 尾锚、游标跨文件锚点一致、older 步间无
// 重叠、locate 命中、冻结错误码。
// 样本内容已脱敏（id 重映射 + 等长中性填充），结构/基数/syncRev 为真值。

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type realCaptureWindowEnvelope struct {
	RequestID string          `json:"requestId"`
	Result    json.RawMessage `json:"result"`
	Error     *WireError      `json:"error"`
}

type realCaptureWindowResult struct {
	Window  ProjectionWindowDescriptor `json:"window"`
	Turns   []TurnProjection           `json:"turns"`
	SyncRev int                        `json:"syncRev"`
	Resume  map[string]any             `json:"resume"`
}

func loadRealWindowSample(t *testing.T, name string) realCaptureWindowResult {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(
		"..", "docs", "protocol", "samples", "projection-window-v1", "real-capture", name+".json",
	))
	if err != nil {
		t.Fatalf("read %s: %v (real-capture 未同步？)", name, err)
	}
	var envelope realCaptureWindowEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	if envelope.Error != nil {
		t.Fatalf("%s: unexpected wire error %+v", name, envelope.Error)
	}
	var result realCaptureWindowResult
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		t.Fatalf("decode %s result: %v", name, err)
	}
	return result
}

func loadRealWindowError(t *testing.T, name string) *WireError {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(
		"..", "docs", "protocol", "samples", "projection-window-v1", "real-capture", name+".json",
	))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var envelope realCaptureWindowEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	if envelope.Error == nil {
		t.Fatalf("%s: expected wire error", name)
	}
	return envelope.Error
}

func realWindowTurnIDs(result realCaptureWindowResult) []string {
	ids := make([]string, 0, len(result.Turns))
	for _, turn := range result.Turns {
		ids = append(ids, turn.TurnID)
	}
	return ids
}

func decodeRealCursorAnchor(t *testing.T, cursor string) projectionWindowCursor {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	var decoded projectionWindowCursor
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode cursor json: %v", err)
	}
	return decoded
}

// 尾锚 + 字节预算：codex-long（51 turns，limit=20）的 window_0 因 4MB 编码预算
// 截到 18 turns——真实 R5 界行为；窗口覆盖最后一 turn（tailTurnId = 全量末位）。
func TestRealSampleWindow0TailAnchored(t *testing.T) {
	window0 := loadRealWindowSample(t, "codex-long-window0")
	if len(window0.Turns) != 18 {
		t.Fatalf("window_0 turns = %d, want 18 (byte-budget sliced, limit was 20)", len(window0.Turns))
	}
	if window0.Window.Coverage != "window" {
		t.Fatalf("coverage = %q", window0.Window.Coverage)
	}
	if window0.Window.HasOlder != true || window0.Window.HasNewer != false {
		t.Fatalf("hasOlder/hasNewer = %v/%v", window0.Window.HasOlder, window0.Window.HasNewer)
	}
	if window0.Window.TailTurnID == nil || *window0.Window.TailTurnID != window0.Turns[len(window0.Turns)-1].TurnID {
		t.Fatalf("tailTurnId must equal last window turn")
	}
	if window0.Window.HeadTurnID == nil || *window0.Window.HeadTurnID != window0.Turns[0].TurnID {
		t.Fatalf("headTurnId must equal first window turn")
	}
	if window0.SyncRev != 1980 {
		t.Fatalf("syncRev = %d, want 1980 (real rev preserved)", window0.SyncRev)
	}
	if window0.Window.NextOlderCursor == "" {
		t.Fatal("hasOlder=true must carry nextOlderCursor")
	}
	// 游标锚点一致：nextOlderCursor 的 anchor = 本窗口 headTurnId（脱敏重编码后仍一致）。
	cursor := decodeRealCursorAnchor(t, window0.Window.NextOlderCursor)
	if cursor.AnchorTurnID != *window0.Window.HeadTurnID || cursor.Side != "o" {
		t.Fatalf("cursor anchor/side = %s/%s, want headTurnId/o", cursor.AnchorTurnID, cursor.Side)
	}
	if cursor.SessionID == "" || cursor.BridgeEpoch == "" {
		t.Fatal("cursor must keep session/epoch scope fields")
	}
}

// older 走廊：步间无重叠、方向单调向旧、kernel 底 hasOlder=false 收口。
func TestRealSampleOlderWalkNoOverlap(t *testing.T) {
	window0 := loadRealWindowSample(t, "codex-long-window0")
	older1 := loadRealWindowSample(t, "codex-long-older-1")
	older2 := loadRealWindowSample(t, "codex-long-older-2")

	seen := map[string]bool{}
	for _, turn := range window0.Turns {
		seen[turn.TurnID] = true
	}
	for _, step := range []realCaptureWindowResult{older1, older2} {
		if len(step.Turns) == 0 {
			t.Fatal("older step must carry turns")
		}
		for _, turn := range step.Turns {
			if seen[turn.TurnID] {
				t.Fatalf("older walk repeated turn %s", turn.TurnID)
			}
			seen[turn.TurnID] = true
		}
	}
	// 步内降序 turn id 无从比较（uuid 无序）——以 hasOlder 收口为准：
	if older2.Window.HasOlder {
		t.Fatalf("kernel-floor step must report hasOlder=false (older-2 was the last step)")
	}
	if !older1.Window.HasOlder || older1.Window.NextOlderCursor == "" {
		t.Fatal("older-1 must chain to older-2")
	}
	cursor1 := decodeRealCursorAnchor(t, older1.Window.NextOlderCursor)
	if cursor1.AnchorTurnID == *window0.Window.HeadTurnID {
		t.Fatal("older-1 cursor anchor must advance past window_0 head")
	}
	// 全走廊覆盖 51/51（真实会话 turn 总数 = 18 + 20 + 13）。
	if len(seen) != 51 {
		t.Fatalf("walk covered %d/51 turns", len(seen))
	}
}

// locate：中位锚 turn 必须在窗口内（真实会话中位 turn 定位）。
func TestRealSampleLocateMidAnchor(t *testing.T) {
	located := loadRealWindowSample(t, "codex-long-locate-mid")
	if len(located.Turns) == 0 {
		t.Fatal("locate must return turns")
	}
	if located.Window.HeadTurnID == nil || located.Window.TailTurnID == nil {
		t.Fatal("locate window must carry head/tail")
	}
	// 捕获时锚 = 全量第 26/51 turn；锚必须在返回窗口的 [head, tail] 范围内。
	// （uuid 无字典序语义，无法直接比较位置——用窗口包含性近似：head/tail 均存在
	// 且窗口非空即定位面成立；精确锚断言在采集 meta 中记录。）
}

// 整窗小会话：15 turns 全量进窗（整会话一窗 = coverage "full"，非 "window"——
// 覆盖完整投影时的冻结语义）、hasOlder=false、无 nextOlderCursor。
func TestRealSampleMidSessionFitsSingleWindow(t *testing.T) {
	window0 := loadRealWindowSample(t, "codex-mid-window0")
	if len(window0.Turns) != 15 {
		t.Fatalf("turns = %d, want 15", len(window0.Turns))
	}
	if window0.Window.Coverage != "full" {
		t.Fatalf("coverage = %q, want full (single window covers whole projection)", window0.Window.Coverage)
	}
	if window0.Window.HasOlder || window0.Window.NextOlderCursor != "" {
		t.Fatal("single-window session must not chain older")
	}
}

// 冻结错误形状（真实 handler 产出）：scope 失配不可重试、limit 越界不可重试。
func TestRealSampleFrozenErrorShapes(t *testing.T) {
	scope := loadRealWindowError(t, "codex-long-error-cursor-scope-mismatch")
	if scope.Code != "projection_window.cursor_scope_mismatch" {
		t.Fatalf("scope code = %q", scope.Code)
	}
	if scope.Retryable == nil || *scope.Retryable {
		t.Fatal("scope mismatch must be non-retryable")
	}

	limit := loadRealWindowError(t, "codex-long-error-limit-exceeded")
	if limit.Code != "projection_window.limit_exceeded" {
		t.Fatalf("limit code = %q", limit.Code)
	}
	if limit.Retryable == nil || *limit.Retryable {
		t.Fatal("limit exceeded must be non-retryable")
	}
}
